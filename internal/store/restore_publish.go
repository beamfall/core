package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

const (
	restorePublishTransactionName = ".beamfall-restore-publish"
	restorePublishJournalVersion  = 1
	restorePublishStatePrepared   = "prepared"
	restorePublishStateCommitted  = "committed"
)

var errRestorePublishInterrupted = errors.New("restore publish interrupted")

type restorePublishJournal struct {
	Version    int                       `json:"version"`
	State      string                    `json:"state"`
	Operations []restorePublishJournalOp `json:"operations"`
}

type restorePublishJournalOp struct {
	RelativePath string   `json:"relativePath"`
	HasStaged    bool     `json:"hasStaged"`
	HadDest      bool     `json:"hadDest"`
	CreatedDirs  []string `json:"createdDirs,omitempty"`
}

type restorePublishInterrupt func(boundary string) error

func publishStagedRestore(stageDir, dataDir string, manifest backupSetManifest, replaceIdentity bool) error {
	return publishStagedRestoreInterruptibly(stageDir, dataDir, manifest, replaceIdentity, nil)
}

func publishStagedRestoreInterruptibly(
	stageDir, dataDir string,
	manifest backupSetManifest,
	replaceIdentity bool,
	interrupt restorePublishInterrupt,
) error {
	if err := recoverRestorePublish(dataDir); err != nil {
		return fmt.Errorf("recover prior restore publish: %w", err)
	}
	if err := ensureNoIdentityConflict(dataDir, manifest, replaceIdentity); err != nil {
		return err
	}
	for _, payload := range manifest.OperationalFiles {
		rel, err := cleanOperationalRelativePath(payload.RelativePath)
		if err != nil {
			return err
		}
		if err := ensureOperationalDestinationSafe(dataDir, rel); err != nil {
			return err
		}
	}
	if info, err := os.Lstat(filepath.Join(dataDir, "db")); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("restore database destination is a symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	journal, err := buildRestorePublishJournal(stageDir, dataDir, manifest, replaceIdentity)
	if err != nil {
		return err
	}
	transactionDir := filepath.Join(dataDir, restorePublishTransactionName)
	if err := os.MkdirAll(filepath.Join(transactionDir, "new"), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(transactionDir, "old"), 0o700); err != nil {
		return err
	}
	for _, op := range journal.Operations {
		if !op.HasStaged {
			continue
		}
		source := filepath.Join(stageDir, filepath.FromSlash(op.RelativePath))
		dest := restorePublishPath(transactionDir, "new", op.RelativePath)
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		if err := syncTree(source); err != nil {
			return err
		}
		if err := durableRename(source, dest); err != nil {
			return err
		}
	}
	if err := syncTree(filepath.Join(transactionDir, "new")); err != nil {
		return err
	}
	if err := callRestorePublishInterrupt(interrupt, "staged"); err != nil {
		return err
	}
	if err := writeRestorePublishJournal(transactionDir, journal); err != nil {
		return err
	}
	if err := syncDir(dataDir); err != nil {
		return err
	}

	if err := finishRestorePublish(dataDir, &journal, interrupt); err != nil {
		if errors.Is(err, errRestorePublishInterrupted) {
			return err
		}
		if rollbackErr := rollbackRestorePublish(dataDir, journal); rollbackErr != nil {
			return fmt.Errorf("restore publish failed (%w) and rollback failed: %v", err, rollbackErr)
		}
		return err
	}
	return nil
}

func buildRestorePublishJournal(
	stageDir, dataDir string,
	manifest backupSetManifest,
	replaceIdentity bool,
) (restorePublishJournal, error) {
	relativePaths := []string{"db"}
	keys := make([]string, 0, len(manifest.OperationalFiles))
	backedIdentity := map[string]struct{}{}
	for key, payload := range manifest.OperationalFiles {
		keys = append(keys, key)
		if isIdentityRelativePath(filepath.FromSlash(payload.RelativePath)) {
			backedIdentity[filepath.ToSlash(payload.RelativePath)] = struct{}{}
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		relativePaths = append(relativePaths, filepath.ToSlash(manifest.OperationalFiles[key].RelativePath))
	}
	deletePaths := []string{}
	if replaceIdentity {
		liveIdentity, err := liveIdentityChecksums(dataDir)
		if err != nil {
			return restorePublishJournal{}, err
		}
		for rel := range liveIdentity {
			if _, ok := backedIdentity[rel]; !ok {
				deletePaths = append(deletePaths, rel)
			}
		}
		sort.Strings(deletePaths)
	}

	journal := restorePublishJournal{
		Version: restorePublishJournalVersion,
		State:   restorePublishStatePrepared,
	}
	for _, rel := range relativePaths {
		if _, err := os.Lstat(filepath.Join(stageDir, filepath.FromSlash(rel))); err != nil {
			return restorePublishJournal{}, err
		}
		op, err := newRestorePublishJournalOp(dataDir, rel, true)
		if err != nil {
			return restorePublishJournal{}, err
		}
		journal.Operations = append(journal.Operations, op)
	}
	for _, rel := range deletePaths {
		op, err := newRestorePublishJournalOp(dataDir, rel, false)
		if err != nil {
			return restorePublishJournal{}, err
		}
		journal.Operations = append(journal.Operations, op)
	}
	return journal, nil
}

func newRestorePublishJournalOp(dataDir, rel string, hasStaged bool) (restorePublishJournalOp, error) {
	nativeRel := filepath.FromSlash(rel)
	if filepath.IsAbs(nativeRel) || nativeRel == "." || nativeRel == ".." ||
		!pathWithinRoot(dataDir, filepath.Join(dataDir, nativeRel)) {
		return restorePublishJournalOp{}, errors.New("restore publish path escapes data directory")
	}
	dest := filepath.Join(dataDir, nativeRel)
	_, err := os.Lstat(dest)
	hadDest := err == nil
	if err != nil && !os.IsNotExist(err) {
		return restorePublishJournalOp{}, err
	}
	created, err := missingRestoreParentDirs(dataDir, filepath.Dir(dest))
	if err != nil {
		return restorePublishJournalOp{}, err
	}
	createdRel := make([]string, 0, len(created))
	for _, path := range created {
		rel, err := filepath.Rel(dataDir, path)
		if err != nil {
			return restorePublishJournalOp{}, err
		}
		createdRel = append(createdRel, filepath.ToSlash(rel))
	}
	return restorePublishJournalOp{
		RelativePath: filepath.ToSlash(filepath.Clean(nativeRel)),
		HasStaged:    hasStaged,
		HadDest:      hadDest,
		CreatedDirs:  createdRel,
	}, nil
}

func finishRestorePublish(dataDir string, journal *restorePublishJournal, interrupt restorePublishInterrupt) error {
	transactionDir := filepath.Join(dataDir, restorePublishTransactionName)
	for _, op := range journal.Operations {
		dest := filepath.Join(dataDir, filepath.FromSlash(op.RelativePath))
		old := restorePublishPath(transactionDir, "old", op.RelativePath)
		staged := restorePublishPath(transactionDir, "new", op.RelativePath)

		if op.HadDest {
			oldExists, err := pathExists(old)
			if err != nil {
				return err
			}
			if !oldExists {
				stagedExists, err := pathExists(staged)
				if err != nil {
					return err
				}
				destExists, err := pathExists(dest)
				if err != nil {
					return err
				}
				pendingMove := stagedExists || !op.HasStaged
				if pendingMove {
					if !destExists {
						return fmt.Errorf("restore publish lost existing destination %s", op.RelativePath)
					}
					if err := os.MkdirAll(filepath.Dir(old), 0o700); err != nil {
						return err
					}
					if err := syncCreatedParents(transactionDir, filepath.Dir(old)); err != nil {
						return err
					}
					if err := durableRename(dest, old); err != nil {
						return err
					}
					if err := callRestorePublishInterrupt(interrupt, "old:"+op.RelativePath); err != nil {
						return err
					}
				}
			}
		}

		if op.HasStaged {
			stagedExists, err := pathExists(staged)
			if err != nil {
				return err
			}
			if stagedExists {
				destExists, err := pathExists(dest)
				if err != nil {
					return err
				}
				if destExists {
					return fmt.Errorf("restore publish destination unexpectedly exists: %s", op.RelativePath)
				}
				if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
					return err
				}
				if err := syncCreatedParents(dataDir, filepath.Dir(dest)); err != nil {
					return err
				}
				if err := durableRename(staged, dest); err != nil {
					return err
				}
				if err := callRestorePublishInterrupt(interrupt, "new:"+op.RelativePath); err != nil {
					return err
				}
			}
		}
	}
	for _, op := range journal.Operations {
		if !op.HasStaged {
			continue
		}
		if err := syncTree(filepath.Join(dataDir, filepath.FromSlash(op.RelativePath))); err != nil {
			return err
		}
	}
	journal.State = restorePublishStateCommitted
	if err := writeRestorePublishJournal(transactionDir, *journal); err != nil {
		return err
	}
	return cleanupRestorePublish(dataDir)
}

func recoverRestorePublish(dataDir string) error {
	transactionDir := filepath.Join(dataDir, restorePublishTransactionName)
	info, err := os.Lstat(transactionDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("restore publish transaction path is not a directory")
	}
	journal, err := readRestorePublishJournal(transactionDir)
	if os.IsNotExist(err) {
		return cleanupRestorePublish(dataDir)
	}
	if err != nil {
		return err
	}
	if journal.State == restorePublishStateCommitted {
		return cleanupRestorePublish(dataDir)
	}
	if journal.State != restorePublishStatePrepared {
		return fmt.Errorf("unknown restore publish state %q", journal.State)
	}
	return finishRestorePublish(dataDir, &journal, nil)
}

func rollbackRestorePublish(dataDir string, journal restorePublishJournal) error {
	transactionDir := filepath.Join(dataDir, restorePublishTransactionName)
	for i := len(journal.Operations) - 1; i >= 0; i-- {
		op := journal.Operations[i]
		dest := filepath.Join(dataDir, filepath.FromSlash(op.RelativePath))
		old := restorePublishPath(transactionDir, "old", op.RelativePath)
		staged := restorePublishPath(transactionDir, "new", op.RelativePath)
		stagedExists, err := pathExists(staged)
		if err != nil {
			return err
		}
		destExists, err := pathExists(dest)
		if err != nil {
			return err
		}
		if op.HasStaged && !stagedExists && destExists {
			if err := os.MkdirAll(filepath.Dir(staged), 0o700); err != nil {
				return err
			}
			if err := durableRename(dest, staged); err != nil {
				return err
			}
			destExists = false
		}
		oldExists, err := pathExists(old)
		if err != nil {
			return err
		}
		if op.HadDest && oldExists {
			if destExists {
				return fmt.Errorf("cannot roll back occupied restore destination %s", op.RelativePath)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
				return err
			}
			if err := durableRename(old, dest); err != nil {
				return err
			}
		}
		for _, rel := range op.CreatedDirs {
			path := filepath.Join(dataDir, filepath.FromSlash(rel))
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				if !errors.Is(err, fs.ErrInvalid) {
					return err
				}
			}
		}
	}
	return cleanupRestorePublish(dataDir)
}

func restorePublishPath(transactionDir, generation, rel string) string {
	return filepath.Join(transactionDir, generation, filepath.FromSlash(rel))
}

func callRestorePublishInterrupt(interrupt restorePublishInterrupt, boundary string) error {
	if interrupt == nil {
		return nil
	}
	return interrupt(boundary)
}

func writeRestorePublishJournal(transactionDir string, journal restorePublishJournal) error {
	data, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	path := filepath.Join(transactionDir, "journal.json")
	temp := path + ".tmp"
	if err := writeFileSync0600(temp, data); err != nil {
		return err
	}
	if err := durableRename(temp, path); err != nil {
		return err
	}
	return syncDir(transactionDir)
}

func readRestorePublishJournal(transactionDir string) (restorePublishJournal, error) {
	//nolint:gosec // transactionDir is the fixed private child of the validated data directory.
	data, err := os.ReadFile(filepath.Join(transactionDir, "journal.json"))
	if err != nil {
		return restorePublishJournal{}, err
	}
	var journal restorePublishJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return restorePublishJournal{}, err
	}
	if journal.Version != restorePublishJournalVersion {
		return restorePublishJournal{}, fmt.Errorf("unsupported restore publish journal version %d", journal.Version)
	}
	for _, op := range journal.Operations {
		if _, err := newRestorePublishJournalOpPathOnly(op.RelativePath); err != nil {
			return restorePublishJournal{}, err
		}
		for _, rel := range op.CreatedDirs {
			if _, err := newRestorePublishJournalOpPathOnly(rel); err != nil {
				return restorePublishJournal{}, err
			}
		}
	}
	return journal, nil
}

func newRestorePublishJournalOpPathOnly(rel string) (string, error) {
	native := filepath.FromSlash(rel)
	clean := filepath.Clean(native)
	if filepath.IsAbs(native) || clean == "." || clean == ".." ||
		(clean != native && filepath.ToSlash(clean) != rel) ||
		(len(clean) >= 3 && clean[:3] == ".."+string(filepath.Separator)) {
		return "", errors.New("invalid restore publish journal path")
	}
	return clean, nil
}

func durableRename(source, dest string) error {
	if err := os.Rename(source, dest); err != nil {
		return err
	}
	if err := syncDir(filepath.Dir(source)); err != nil {
		return err
	}
	if filepath.Dir(dest) != filepath.Dir(source) {
		if err := syncDir(filepath.Dir(dest)); err != nil {
			return err
		}
	}
	return nil
}

func syncCreatedParents(root, parent string) error {
	for current := parent; ; current = filepath.Dir(current) {
		if err := syncDir(current); err != nil {
			return err
		}
		if current == root {
			return nil
		}
		if !pathWithinRoot(root, current) {
			return errors.New("restore destination parent escapes data directory")
		}
	}
}

func syncTree(root string) error {
	root = filepath.Clean(root)
	parentRoot, err := os.OpenRoot(filepath.Dir(root))
	if err != nil {
		return err
	}
	info, statErr := parentRoot.Lstat(filepath.Base(root))
	closeErr := parentRoot.Close()
	if statErr != nil {
		return statErr
	}
	if closeErr != nil {
		return closeErr
	}
	if !info.IsDir() {
		//nolint:gosec // root is a restore path confined by the manifest/data-directory validators.
		file, err := os.Open(root)
		if err != nil {
			return err
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return err
		}
		return file.Close()
	}
	dirs := []string{}
	//nolint:gosec // Backup/restore callers validate or construct root under trusted directories; the callback rejects symlink entries.
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("restore publish tree contains symlink: %s", path)
		}
		if entry.IsDir() {
			dirs = append(dirs, path)
			return nil
		}
		//nolint:gosec // WalkDir yields descendants of the already-confined restore root.
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return err
		}
		return file.Close()
	})
	if err != nil {
		return err
	}
	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i]) > len(dirs[j])
	})
	for _, dir := range dirs {
		if err := syncDir(dir); err != nil {
			return err
		}
	}
	return nil
}

func cleanupRestorePublish(dataDir string) error {
	transactionDir := filepath.Join(dataDir, restorePublishTransactionName)
	if err := os.RemoveAll(transactionDir); err != nil {
		return err
	}
	return syncDir(dataDir)
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
