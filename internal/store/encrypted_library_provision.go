package store

import (
	"context"
	"errors"
	"path/filepath"
)

// AbortEncryptedLibraryDBProvision compensates a graph/store preparation that
// failed before the libencrypt key commit. It unregisters the candidate DB and
// removes every plaintext working artifact. Existing ciphertext is never
// deleted: it may predate this attempted provision and remain operator-
// recoverable even when the current key inventory is incomplete.
func (s *Store) AbortEncryptedLibraryDBProvision(ctx context.Context, libraryID LibraryID) error {
	if libraryID == "" {
		return errors.New("library ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	paths := s.encryptedLibraryDBPaths(libraryID)
	var joined error
	if db := s.encryptedDBs[libraryID]; db != nil {
		if db.reader != nil {
			joined = errors.Join(joined, db.reader.Close())
		}
		if db.writer != nil {
			joined = errors.Join(joined, db.writer.Close())
		}
		delete(s.encryptedDBs, libraryID)
	}
	joined = errors.Join(joined, cleanupEncryptedLibraryWorkingDir(filepath.Dir(paths.working), paths.working))
	return joined
}
