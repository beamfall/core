package jsonstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// PostRenameSyncError indicates that the state file was atomically renamed into
// place on disk, but syncing its parent directory failed afterward. Callers
// that pair file updates with external mutations should treat the new file as
// installed rather than rolling back companion state that the new file uses.
type PostRenameSyncError struct {
	Err error
}

func (e *PostRenameSyncError) Error() string {
	return "jsonstate: post-rename directory sync: " + e.Err.Error()
}

func (e *PostRenameSyncError) Unwrap() error { return e.Err }

var (
	syncDirMu             sync.Mutex
	syncDirFunc           = syncDir
	pendingParentSyncDirs = map[string]struct{}{}
)

// WithSyncDirForTest replaces parent-directory fsyncing in tests and returns a
// cleanup callback restoring the prior implementation.
func WithSyncDirForTest(fn func(string) error) func() {
	syncDirMu.Lock()
	prev := syncDirFunc
	if fn != nil {
		syncDirFunc = fn
	} else {
		syncDirFunc = syncDir
	}
	syncDirMu.Unlock()
	return func() {
		syncDirMu.Lock()
		syncDirFunc = prev
		syncDirMu.Unlock()
	}
}

// Load reads a JSON state file into out. Missing files are fresh state.
func Load[T any](path string, out *T) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(data, out)
}

// Save writes a JSON state file atomically under the secrets-adjacent data tree.
func Save(path string, state any) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	_, statErr := os.Stat(dir)
	createdDir := errors.Is(statErr, os.ErrNotExist)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// A unique temp per Save (not a fixed ".tmp" sibling) keeps concurrent
	// saves from interleaving into the same staging file, and the Sync before
	// Rename keeps a crash from promoting a truncated file.
	tmp, err := os.CreateTemp(dir, ".jsonstate-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	syncDirMu.Lock()
	syncFn := syncDirFunc
	syncDirMu.Unlock()
	if err := syncFn(dir); err != nil {
		return &PostRenameSyncError{Err: err}
	}
	syncDirMu.Lock()
	_, retryParent := pendingParentSyncDirs[dir]
	syncDirMu.Unlock()
	if createdDir || retryParent {
		if parent := filepath.Dir(dir); parent != dir {
			if err := syncFn(parent); err != nil {
				syncDirMu.Lock()
				pendingParentSyncDirs[dir] = struct{}{}
				syncDirMu.Unlock()
				return &PostRenameSyncError{Err: err}
			}
		}
		if retryParent {
			syncDirMu.Lock()
			delete(pendingParentSyncDirs, dir)
			syncDirMu.Unlock()
		}
	}
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // G304: dir is filepath.Dir of caller state path already prepared by Save.
	if err != nil {
		return err
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
