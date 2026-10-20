package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrEncryptedLibraryDBDisabled    = errors.New("encrypted-library DB lifecycle is disabled")
	ErrEncryptedLibraryDBQuarantined = errors.New("encrypted-library DB is quarantined")
)

const (
	encryptedLibraryDBLabel            = "encrypted_library"
	encryptedLibraryDBCipherExt        = ".db.libencrypt"
	cacheDirTagName                    = "CACHEDIR.TAG"
	cacheDirTagHeader                  = "Signature: 8a477f597d28d172789f06886806bc55\n"
	encryptedLibraryLeftoverQuarantine = "leftover plaintext working files"
)

type EncryptedLibraryDBCipher interface {
	UnsealDBFile(ctx context.Context, libraryID LibraryID, ciphertextPath, workingPath string) error
	SealDBFile(ctx context.Context, libraryID LibraryID, workingPath, ciphertextPath string) error
}

type encryptedLibraryDB struct {
	libraryID      LibraryID
	generation     uint64
	state          string
	writer         *sql.DB
	reader         *sql.DB
	ciphertextPath string
	workingPath    string
	quarantineKind string
	quarantine     string
}

func (s *Store) EncryptedLibraryDBRegistered(libraryID LibraryID) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	db := s.encryptedDBs[libraryID]
	return db != nil && db.state == "unsealed-registered" && db.writer != nil && db.reader != nil
}

func (s *Store) EncryptedLibraryDBLifecycleAvailable() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.closed && s.encryptedDBLifecycleEnable
}

func (s *Store) EncryptedLibraryDBQuarantine(libraryID LibraryID) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	db := s.encryptedDBs[libraryID]
	if db == nil || db.state != "failed-quarantined" {
		return "", false
	}
	return db.quarantine, true
}

func (s *Store) UnsealEncryptedLibraryDB(ctx context.Context, libraryID LibraryID, cipher EncryptedLibraryDBCipher) error {
	if libraryID == "" {
		return errors.New("library ID is required")
	}
	if cipher == nil {
		return errors.New("encrypted-library DB cipher is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if !s.encryptedDBLifecycleEnable {
		return ErrEncryptedLibraryDBDisabled
	}
	if db := s.encryptedDBs[libraryID]; db != nil {
		if db.state == "failed-quarantined" {
			return fmt.Errorf("%w: %s", ErrEncryptedLibraryDBQuarantined, db.quarantine)
		}
		if db.state == "unsealed-registered" {
			return nil
		}
	}
	paths := s.encryptedLibraryDBPaths(libraryID)
	if err := os.MkdirAll(filepath.Dir(paths.ciphertext), 0o700); err != nil {
		return err
	}
	if err := ensureEncryptedLibraryWorkingRoot(s.encryptedDBWorkingRoot); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(paths.working), 0o700); err != nil {
		return err
	}
	if leftovers, err := encryptedLibraryPlaintextLeftovers(filepath.Dir(paths.working), paths.working); err != nil {
		return err
	} else if len(leftovers) > 0 {
		s.quarantineEncryptedLibraryDBLocked(libraryID, encryptedLibraryLeftoverQuarantine)
		return fmt.Errorf("%w: %s", ErrEncryptedLibraryDBQuarantined, encryptedLibraryLeftoverQuarantine)
	}
	s.encryptedDBs[libraryID] = &encryptedLibraryDB{
		libraryID:      libraryID,
		state:          "unsealing",
		ciphertextPath: paths.ciphertext,
		workingPath:    paths.working,
	}
	if err := cipher.UnsealDBFile(ctx, libraryID, paths.ciphertext, paths.working); err != nil {
		return s.unwindEncryptedLibraryUnsealLocked(libraryID, nil, nil, paths.working, err)
	}
	writer, err := openEncryptedLibrarySQLiteWriter(paths.working)
	if err != nil {
		return s.unwindEncryptedLibraryUnsealLocked(libraryID, nil, nil, paths.working, err)
	}
	if err := migrateEncryptedLibrary(ctx, s.logger, writer, paths.working, paths.ciphertext, filepath.Join(s.dataDir, "backups", "pre-migration", "encrypted-library", safeLibraryPathComponent(libraryID))); err != nil {
		return s.unwindEncryptedLibraryUnsealLocked(libraryID, nil, writer, paths.working, err)
	}
	reader, err := openEncryptedLibrarySQLiteReader(paths.working)
	if err != nil {
		return s.unwindEncryptedLibraryUnsealLocked(libraryID, nil, writer, paths.working, err)
	}
	s.encryptedDBs[libraryID] = &encryptedLibraryDB{
		libraryID:      libraryID,
		generation:     s.nextEncryptedLibraryGenerationLocked(),
		state:          "unsealed-registered",
		writer:         writer,
		reader:         reader,
		ciphertextPath: paths.ciphertext,
		workingPath:    paths.working,
	}
	if err := s.replayEntityFieldIndexesForEncryptedDBLocked(ctx, s.encryptedDBs[libraryID]); err != nil {
		return s.unwindEncryptedLibraryUnsealLocked(libraryID, reader, writer, paths.working, err)
	}
	// Snapshot-before-flip derivative reconcile (ADR-0045 §reconcile): the media
	// inventory is enumerable only now that the DB is registered, so this is the
	// exact point at which protected-plane derivative invariants can be
	// re-established. Fail closed — if the reconciler cannot re-protect/purge the
	// orphaned derivatives, unwind the registration (like the replay path above)
	// so the Library stays Sealed until an explicit lifecycle repair succeeds,
	// rather than serving a plane whose derivatives are unaccounted for.
	if s.encDerivativeReconciler != nil {
		liveKeys, err := s.snapshotEncryptedLibraryMediaLocked(ctx, s.encryptedDBs[libraryID])
		if err == nil {
			err = s.encDerivativeReconciler.ReconcileEncryptedDerivatives(ctx, libraryID, liveKeys)
		}
		if err != nil {
			return s.unwindEncryptedLibraryUnsealLocked(libraryID, reader, writer, paths.working, err)
		}
	}
	return nil
}

func (s *Store) unwindEncryptedLibraryUnsealLocked(libraryID LibraryID, reader, writer *sql.DB, workingPath string, cause error) error {
	joined := cause
	if reader != nil {
		joined = errors.Join(joined, reader.Close())
	}
	if writer != nil {
		joined = errors.Join(joined, writer.Close())
	}
	cleanupErr := cleanupEncryptedLibraryWorkingDir(filepath.Dir(workingPath), workingPath)
	joined = errors.Join(joined, cleanupErr)
	if cleanupErr != nil {
		s.quarantineEncryptedLibraryDBWithKindLocked(libraryID, encryptedLibraryLeftoverQuarantine, cleanupErr.Error())
	} else {
		delete(s.encryptedDBs, libraryID)
	}
	return joined
}

func (s *Store) SealEncryptedLibraryDB(ctx context.Context, libraryID LibraryID, cipher EncryptedLibraryDBCipher) error {
	if libraryID == "" {
		return errors.New("library ID is required")
	}
	if cipher == nil {
		return errors.New("encrypted-library DB cipher is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if !s.encryptedDBLifecycleEnable {
		return ErrEncryptedLibraryDBDisabled
	}
	db := s.encryptedDBs[libraryID]
	if db == nil {
		paths := s.encryptedLibraryDBPaths(libraryID)
		return cleanupEncryptedLibraryWorkingDir(filepath.Dir(paths.working), paths.working)
	}
	if db.state == "failed-quarantined" {
		return fmt.Errorf("%w: %s", ErrEncryptedLibraryDBQuarantined, db.quarantine)
	}
	db.state = "sealing"
	var err error
	if db.reader != nil {
		err = errors.Join(err, db.reader.Close())
		db.reader = nil
	}
	if db.writer != nil {
		_, checkpointErr := db.writer.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
		err = errors.Join(err, checkpointErr)
		err = errors.Join(err, db.writer.Close())
		db.writer = nil
	}
	if err != nil {
		s.quarantineEncryptedLibraryDBLocked(libraryID, err.Error())
		return err
	}
	if err := cipher.SealDBFile(ctx, libraryID, db.workingPath, db.ciphertextPath); err != nil {
		s.quarantineEncryptedLibraryDBLocked(libraryID, err.Error())
		return err
	}
	delete(s.encryptedDBs, libraryID)
	if err := cleanupEncryptedLibraryWorkingDir(filepath.Dir(db.workingPath), db.workingPath); err != nil {
		s.quarantineEncryptedLibraryDBWithKindLocked(libraryID, encryptedLibraryLeftoverQuarantine, err.Error())
		return err
	}
	return nil
}

func (s *Store) RewrapEncryptedLibraryDB(ctx context.Context, libraryID LibraryID, rewrap func(string) error) error {
	if libraryID == "" {
		return errors.New("library ID is required")
	}
	if rewrap == nil {
		return errors.New("encrypted-library DB rewrap is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if !s.encryptedDBLifecycleEnable {
		return ErrEncryptedLibraryDBDisabled
	}
	if db := s.encryptedDBs[libraryID]; db != nil {
		if db.state == "failed-quarantined" {
			return fmt.Errorf("%w: %s", ErrEncryptedLibraryDBQuarantined, db.quarantine)
		}
		return errors.New("encrypted-library DB must be sealed before rewrap")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	paths := s.encryptedLibraryDBPaths(libraryID)
	if _, err := os.Stat(paths.ciphertext); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := rewrap(paths.ciphertext); err != nil {
		s.quarantineEncryptedLibraryDBLocked(libraryID, err.Error())
		return err
	}
	return nil
}

func (s *Store) RepairEncryptedLibraryDBQuarantine(ctx context.Context, libraryID LibraryID, cipher EncryptedLibraryDBCipher) error {
	if libraryID == "" {
		return errors.New("library ID is required")
	}
	if cipher == nil {
		return errors.New("encrypted-library DB cipher is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if !s.encryptedDBLifecycleEnable {
		return ErrEncryptedLibraryDBDisabled
	}
	db := s.encryptedDBs[libraryID]
	if db == nil {
		return nil
	}
	if db.state != "failed-quarantined" {
		return nil
	}
	if db.quarantineKind != encryptedLibraryLeftoverQuarantine {
		return fmt.Errorf("%w: %s", ErrEncryptedLibraryDBQuarantined, db.quarantine)
	}
	return s.repairEncryptedLibraryPlaintextQuarantineLocked(ctx, libraryID, cipher)
}

func migrateEncryptedLibrary(ctx context.Context, logger *slog.Logger, db *sql.DB, workingPath, ciphertextPath, backupDir string) error {
	if logger == nil {
		logger = slog.Default()
	}
	return migrateWithBackup(ctx, logger, db, workingPath, encryptedLibraryDBLabel, encryptedLibraryMigrations, "migrate/encrypted_library", backupDir,
		func(ctx context.Context, logger *slog.Logger, db *sql.DB, dbPath string, name string, backupDir string) error {
			return backupEncryptedLibraryCiphertext(ctx, logger, ciphertextPath, name, backupDir)
		})
}

func backupEncryptedLibraryCiphertext(ctx context.Context, logger *slog.Logger, ciphertextPath, name, backupDir string) error {
	if _, err := os.Stat(ciphertextPath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return err
	}
	stem := timeNowUTC().Format("20060102T150405.000000000Z") + "-" + name
	backupPath := filepath.Join(backupDir, stem+encryptedLibraryDBCipherExt)
	if err := copyFile0600(ciphertextPath, backupPath); err != nil {
		return err
	}
	logger.InfoContext(ctx, "encrypted-library pre-migration ciphertext backup written", "database", name, "path", backupPath)
	return nil
}

func (s *Store) reconcileEncryptedLibraryDBs(ctx context.Context) error {
	if err := os.MkdirAll(s.encryptedDBRoot, 0o700); err != nil {
		return err
	}
	if err := ensureEncryptedLibraryWorkingRoot(s.encryptedDBWorkingRoot); err != nil {
		return err
	}
	entries, err := os.ReadDir(s.encryptedDBWorkingRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == cacheDirTagName {
			continue
		}
		if !entry.IsDir() {
			continue
		}
		libraryID := LibraryID(entry.Name())
		dir := filepath.Join(s.encryptedDBWorkingRoot, entry.Name())
		working := filepath.Join(dir, "working.db")
		leftovers, err := encryptedLibraryPlaintextLeftovers(dir, working)
		if err != nil {
			return err
		}
		if len(leftovers) == 0 {
			continue
		}
		s.encryptedDBs[libraryID] = &encryptedLibraryDB{
			libraryID:      libraryID,
			state:          "failed-quarantined",
			workingPath:    working,
			quarantineKind: encryptedLibraryLeftoverQuarantine,
			quarantine:     encryptedLibraryLeftoverQuarantine,
		}
		if s.logger != nil {
			s.logger.WarnContext(ctx, "encrypted-library DB quarantined at startup", "libraryID", libraryID, "leftovers", len(leftovers))
		}
	}
	return nil
}

func (s *Store) repairEncryptedLibraryPlaintextQuarantineLocked(ctx context.Context, libraryID LibraryID, cipher EncryptedLibraryDBCipher) error {
	paths := s.encryptedLibraryDBPaths(libraryID)
	if err := cipher.SealDBFile(ctx, libraryID, paths.working, paths.ciphertext); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrEncryptedLibraryDBQuarantined, encryptedLibraryLeftoverQuarantine, err)
	}
	if err := cleanupEncryptedLibraryWorkingDir(filepath.Dir(paths.working), paths.working); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrEncryptedLibraryDBQuarantined, encryptedLibraryLeftoverQuarantine, err)
	}
	delete(s.encryptedDBs, libraryID)
	return nil
}

func (s *Store) quarantineEncryptedLibraryDBLocked(libraryID LibraryID, reason string) {
	s.quarantineEncryptedLibraryDBWithKindLocked(libraryID, reason, reason)
}

func (s *Store) quarantineEncryptedLibraryDBWithKindLocked(libraryID LibraryID, kind, detail string) {
	paths := s.encryptedLibraryDBPaths(libraryID)
	s.encryptedDBs[libraryID] = &encryptedLibraryDB{
		libraryID:      libraryID,
		state:          "failed-quarantined",
		ciphertextPath: paths.ciphertext,
		workingPath:    paths.working,
		quarantineKind: kind,
		quarantine:     detail,
	}
}

func (s *Store) nextEncryptedLibraryGenerationLocked() uint64 {
	var max uint64
	for _, db := range s.encryptedDBs {
		if db.generation > max {
			max = db.generation
		}
	}
	return max + 1
}

type encryptedLibraryDBPaths struct {
	ciphertext string
	working    string
}

func (s *Store) encryptedLibraryDBPaths(libraryID LibraryID) encryptedLibraryDBPaths {
	component := safeLibraryPathComponent(libraryID)
	return encryptedLibraryDBPaths{
		ciphertext: filepath.Join(s.encryptedDBRoot, component, "encrypted_library"+encryptedLibraryDBCipherExt),
		working:    filepath.Join(s.encryptedDBWorkingRoot, component, "working.db"),
	}
}

func ensureEncryptedLibraryWorkingRoot(root string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	tag := filepath.Join(root, cacheDirTagName)
	if _, err := os.Stat(tag); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(tag, []byte(cacheDirTagHeader+"# Beamfall encrypted-library plaintext working area.\n"), 0o600)
}

func openEncryptedLibrarySQLiteWriter(path string) (*sql.DB, error) {
	return openSQLiteWriterWithPragmas(path, encryptedLibrarySQLiteTempPragmas(filepath.Dir(path)))
}

func openEncryptedLibrarySQLiteReader(path string) (*sql.DB, error) {
	return openSQLiteReaderWithPragmas(path, encryptedLibrarySQLiteTempPragmas(filepath.Dir(path)))
}

func encryptedLibrarySQLiteTempPragmas(dir string) []string {
	return []string{
		"temp_store(FILE)",
		"temp_store_directory(" + sqliteQuote(dir) + ")",
	}
}

func sqliteQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func encryptedLibraryPlaintextLeftovers(dir, workingPath string) ([]string, error) {
	var leftovers []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		if entry.Name() == cacheDirTagName {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		leftovers = append(leftovers, path)
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return leftovers, nil
}

var cleanupEncryptedLibraryWorkingDir = cleanupEncryptedLibraryWorkingDirDefault

func cleanupEncryptedLibraryWorkingDirDefault(dir, workingPath string) error {
	leftovers, err := encryptedLibraryPlaintextLeftovers(dir, workingPath)
	if err != nil {
		return err
	}
	var joined error
	for _, path := range leftovers {
		joined = errors.Join(joined, wipeOrRemoveFile(path))
	}
	if joined != nil {
		return joined
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return os.Remove(dir)
	}
	return nil
}

func wipeOrRemoveFile(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.RemoveAll(path)
	}
	var wipeErr error
	f, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		wipeErr = fmt.Errorf("wipe %s: %w", path, err)
	} else {
		zeros := make([]byte, 32*1024)
		remaining := info.Size()
		for remaining > 0 {
			n := int64(len(zeros))
			if n > remaining {
				n = remaining
			}
			if _, writeErr := f.Write(zeros[:n]); writeErr != nil {
				err = writeErr
				break
			}
			remaining -= n
		}
		if syncErr := f.Sync(); err == nil {
			err = syncErr
		}
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			wipeErr = fmt.Errorf("wipe %s: %w", path, err)
		}
	}
	return errors.Join(wipeErr, os.Remove(path))
}

func copyFile0600(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".encrypted-library-backup-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if _, err := io.Copy(tmp, in); err != nil {
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
	return os.Rename(tmpName, dst)
}

func safeLibraryPathComponent(libraryID LibraryID) string {
	var b strings.Builder
	for _, r := range string(libraryID) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func timeNowUTC() time.Time {
	return time.Now().UTC()
}
