package jsonstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// A unique temp per Save (not a fixed ".tmp" sibling) keeps concurrent
	// saves from interleaving into the same staging file, and the Sync before
	// Rename keeps a crash from promoting a truncated file.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".jsonstate-*.tmp")
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
	return nil
}
