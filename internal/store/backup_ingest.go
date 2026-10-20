package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const backupIngestMinFreeBytes int64 = 64 << 20
const backupIngestPlainTempPattern = ".backup-upload-*.plain"

var (
	ErrBackupIngestPaused        = errors.New("backup ingest is paused")
	ErrBackupIngestQuotaExceeded = errors.New("backup ingest quota exceeded")
	ErrBackupIngestHeadroom      = errors.New("backup ingest disk headroom exhausted")
	backupIngestSyncPayload      = syncTree
	backupIngestSyncParents      = syncCreatedParents
)

type BackupIngestEncryptor interface {
	EncryptFile(libraryID, plaintextPath, ciphertextPath string) error
}

// BackupIngestObjectEncryptor is the identity-bound extension of
// BackupIngestEncryptor: the upload's persisted content
// fingerprint travels with the write so the ciphertext is cryptographically
// bound to the object's immutable identity, never its path. The ingest path
// prefers this interface whenever the encryptor implements it.
type BackupIngestObjectEncryptor interface {
	EncryptObjectFile(libraryID, plaintextPath, ciphertextPath, contentFingerprint string) error
}

type BackupIngestPolicy struct {
	ProfileID  ProfileID
	LibraryID  LibraryID
	QuotaBytes int64
	Paused     bool
	UpdatedAt  time.Time
}

type BackupIngestRequest struct {
	LibraryID   LibraryID
	ProfileID   ProfileID
	Title       string
	Container   string
	Codec       string
	Class       MediaClass
	SizeBytes   int64
	ModTime     time.Time
	Filter      LibraryFilter
	Encryptor   BackupIngestEncryptor
	PlainReader io.Reader
}

// backupIngestEncryptFile routes one upload write through the identity-bound
// encryptor when available, falling back to the legacy context-free seam only
// for encryptors that predate object binding.
func backupIngestEncryptFile(encryptor BackupIngestEncryptor, libraryID, plaintextPath, ciphertextPath, contentFingerprint string) error {
	if objectEncryptor, ok := encryptor.(BackupIngestObjectEncryptor); ok {
		return objectEncryptor.EncryptObjectFile(libraryID, plaintextPath, ciphertextPath, contentFingerprint)
	}
	return encryptor.EncryptFile(libraryID, plaintextPath, ciphertextPath)
}

type BackupIngestResult struct {
	Media        Media
	File         MediaFile
	Deduplicated bool
	Fingerprint  string
}

func (s *Store) SetBackupIngestPolicy(ctx context.Context, policy BackupIngestPolicy) error {
	if policy.ProfileID == "" {
		return ValidationError("profile ID is required")
	}
	if policy.LibraryID == "" {
		return ValidationError("library ID is required")
	}
	if policy.QuotaBytes < 0 {
		return ValidationError("quota bytes cannot be negative")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if _, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO backup_ingest_policy(profile_id, library_id, quota_bytes, paused, updated_at_ms)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, library_id) DO UPDATE SET
		  quota_bytes = excluded.quota_bytes,
		  paused = excluded.paused,
		  updated_at_ms = excluded.updated_at_ms
	`, policy.ProfileID, policy.LibraryID, policy.QuotaBytes, boolToInt(policy.Paused), timeToMillis(policy.UpdatedAt)); err != nil {
		return err
	}
	return nil
}

func (s *Store) BackupIngestStatus(ctx context.Context, profileID ProfileID, libraryID LibraryID) (BackupIngestPolicy, error) {
	if profileID == "" || libraryID == "" {
		return BackupIngestPolicy{}, ValidationError("profile ID and library ID are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return BackupIngestPolicy{}, err
	}
	return s.backupIngestPolicyLocked(ctx, profileID, libraryID)
}

func (s *Store) BackupIngest(ctx context.Context, req BackupIngestRequest) (BackupIngestResult, error) {
	if req.ProfileID == "" {
		return BackupIngestResult{}, ValidationError("profile-bound backup key is required")
	}
	if req.LibraryID == "" {
		return BackupIngestResult{}, ValidationError("library ID is required")
	}
	if req.PlainReader == nil {
		return BackupIngestResult{}, ValidationError("backup bytes are required")
	}
	if req.Encryptor == nil {
		return BackupIngestResult{}, errors.New("backup encryptor is required")
	}
	if req.SizeBytes <= 0 {
		return BackupIngestResult{}, ValidationError("backup size is required")
	}

	s.backupIngestMu.Lock()
	defer s.backupIngestMu.Unlock()

	s.mu.RLock()
	if err := s.guardLocked(); err != nil {
		s.mu.RUnlock()
		return BackupIngestResult{}, err
	}
	library, err := s.backupIngestLibraryLocked(ctx, req.LibraryID, req.Filter)
	if err != nil {
		s.mu.RUnlock()
		return BackupIngestResult{}, err
	}
	if !library.Custodial || !library.Encrypted {
		s.mu.RUnlock()
		return BackupIngestResult{}, ValidationError("backup ingest requires a custodial encrypted library")
	}
	policy, err := s.backupIngestPolicyLocked(ctx, req.ProfileID, req.LibraryID)
	if err != nil {
		s.mu.RUnlock()
		return BackupIngestResult{}, err
	}
	if policy.Paused {
		s.mu.RUnlock()
		return BackupIngestResult{}, ErrBackupIngestPaused
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, req.LibraryID)
	if err != nil {
		s.mu.RUnlock()
		return BackupIngestResult{}, err
	}
	currentBytes, err := backupIngestLibraryBytes(ctx, plane.reader)
	if err != nil {
		s.mu.RUnlock()
		return BackupIngestResult{}, err
	}
	if policy.QuotaBytes > 0 && currentBytes+req.SizeBytes > policy.QuotaBytes {
		s.mu.RUnlock()
		return BackupIngestResult{}, ErrBackupIngestQuotaExceeded
	}
	if err := s.backupIngestHasHeadroom(req.SizeBytes); err != nil {
		s.mu.RUnlock()
		return BackupIngestResult{}, err
	}
	s.mu.RUnlock()

	tmpPath, fingerprint, actualSize, err := s.writeBackupIngestPlainTemp(req.PlainReader, req.SizeBytes)
	if err != nil {
		return BackupIngestResult{}, err
	}
	defer wipeAndRemove(tmpPath)
	if actualSize != req.SizeBytes {
		return BackupIngestResult{}, ValidationError("backup size does not match bytes")
	}

	ciphertextPath := s.backupIngestCiphertextPath(req.LibraryID, fingerprint)
	deduplicated := false
	createdCiphertext := false
	if _, err := os.Stat(ciphertextPath); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(ciphertextPath), 0o700); err != nil {
			return BackupIngestResult{}, err
		}
		cipherTmp, err := os.CreateTemp(filepath.Dir(ciphertextPath), "."+filepath.Base(ciphertextPath)+".*.partial")
		if err != nil {
			return BackupIngestResult{}, err
		}
		cipherTmpPath := cipherTmp.Name()
		if err := cipherTmp.Close(); err != nil {
			os.Remove(cipherTmpPath)
			return BackupIngestResult{}, err
		}
		if err := os.Remove(cipherTmpPath); err != nil {
			return BackupIngestResult{}, err
		}
		defer os.Remove(cipherTmpPath)
		if err := backupIngestEncryptFile(req.Encryptor, string(req.LibraryID), tmpPath, cipherTmpPath, "sha256:"+fingerprint); err != nil {
			return BackupIngestResult{}, err
		}
		if err := os.Rename(cipherTmpPath, ciphertextPath); err != nil {
			return BackupIngestResult{}, err
		}
		createdCiphertext = true
	} else if err != nil {
		return BackupIngestResult{}, err
	} else {
		deduplicated = true
	}
	if err := backupIngestSyncPayload(ciphertextPath); err != nil {
		if createdCiphertext {
			_ = os.Remove(ciphertextPath)
		}
		return BackupIngestResult{}, fmt.Errorf("sync backup ingest ciphertext: %w", err)
	}
	if err := backupIngestSyncParents(s.dataDir, filepath.Dir(ciphertextPath)); err != nil {
		if createdCiphertext {
			_ = os.Remove(ciphertextPath)
		}
		return BackupIngestResult{}, fmt.Errorf("sync backup ingest directories: %w", err)
	}

	media, file, err := s.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          req.LibraryID,
		Path:               ciphertextPath,
		Title:              firstNonEmpty(strings.TrimSpace(req.Title), "Uploaded Media"),
		Container:          firstNonEmpty(strings.TrimSpace(req.Container), "bin"),
		Codec:              strings.TrimSpace(req.Codec),
		Class:              req.Class,
		ContentFingerprint: "sha256:" + fingerprint,
		SizeBytes:          req.SizeBytes,
		ModTime:            req.ModTime,
	})
	if err != nil {
		if createdCiphertext {
			_ = os.Remove(ciphertextPath)
		}
		return BackupIngestResult{}, err
	}
	return BackupIngestResult{
		Media:        media,
		File:         file,
		Deduplicated: deduplicated,
		Fingerprint:  "sha256:" + fingerprint,
	}, nil
}

func (s *Store) backupIngestPolicyLocked(ctx context.Context, profileID ProfileID, libraryID LibraryID) (BackupIngestPolicy, error) {
	var policy BackupIngestPolicy
	var paused int
	var updatedMS int64
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT quota_bytes, paused, updated_at_ms
		FROM backup_ingest_policy
		WHERE profile_id = ? AND library_id = ?
	`, profileID, libraryID).Scan(&policy.QuotaBytes, &paused, &updatedMS)
	if errors.Is(err, sql.ErrNoRows) {
		return BackupIngestPolicy{ProfileID: profileID, LibraryID: libraryID}, nil
	}
	if err != nil {
		return BackupIngestPolicy{}, err
	}
	policy.ProfileID = profileID
	policy.LibraryID = libraryID
	policy.Paused = paused != 0
	policy.UpdatedAt = millisToTime(updatedMS)
	return policy, nil
}

func (s *Store) backupIngestLibraryLocked(ctx context.Context, libraryID LibraryID, filter LibraryFilter) (Library, error) {
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return Library{}, err
	}
	for _, library := range libraries {
		if library.ID == libraryID && libraryVisible(library, filter) {
			return library, nil
		}
	}
	return Library{}, ValidationError("unknown library")
}

func backupIngestLibraryBytes(ctx context.Context, db *sql.DB) (int64, error) {
	var total int64
	err := db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(size_bytes), 0)
		FROM media_file
		WHERE missing_at_ms IS NULL
	`).Scan(&total)
	return total, err
}

func (s *Store) backupIngestHasHeadroom(sizeBytes int64) error {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(s.dataDir, &stat); err != nil {
		return err
	}
	free := int64(stat.Bavail) * int64(stat.Bsize)
	if free-sizeBytes < backupIngestMinFreeBytes {
		return ErrBackupIngestHeadroom
	}
	return nil
}

func (s *Store) writeBackupIngestPlainTemp(r io.Reader, expectedSize int64) (string, string, int64, error) {
	dir := s.backupIngestWorkingDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", 0, err
	}
	tmp, err := os.CreateTemp(dir, backupIngestPlainTempPattern)
	if err != nil {
		return "", "", 0, err
	}
	tmpPath := tmp.Name()
	hash := sha256.New()
	limit := expectedSize
	if limit < math.MaxInt64 {
		limit++
	}
	written, copyErr := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(r, limit))
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = wipeAndRemove(tmpPath)
		return "", "", 0, copyErr
	}
	if closeErr != nil {
		_ = wipeAndRemove(tmpPath)
		return "", "", 0, closeErr
	}
	return tmpPath, hex.EncodeToString(hash.Sum(nil)), written, nil
}

func (s *Store) backupIngestWorkingDir() string {
	return filepath.Join(s.dataDir, "backups", "ingest", "working")
}

func (s *Store) sweepBackupIngestPlainScratch() error {
	matches, err := filepath.Glob(filepath.Join(s.backupIngestWorkingDir(), backupIngestPlainTempPattern))
	if err != nil {
		return err
	}
	for _, path := range matches {
		if err := wipeAndRemove(path); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) backupIngestCiphertextPath(libraryID LibraryID, fingerprint string) string {
	prefix := fingerprint
	if len(prefix) > 2 {
		prefix = prefix[:2]
	}
	return filepath.Join(s.dataDir, "backups", "ingest", safeLibraryPathComponent(libraryID), prefix, fingerprint+".libencrypt")
}

func wipeAndRemove(path string) error {
	if path == "" {
		return nil
	}
	info, err := os.Lstat(path)
	if err == nil && info.Mode().IsRegular() {
		if f, openErr := os.OpenFile(path, os.O_WRONLY|syscall.O_NOFOLLOW, 0); openErr == nil {
			_, _ = io.CopyN(f, zeroReader{}, info.Size())
			_ = f.Close()
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func backupIngestFingerprintForTest(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("sha256:%s", hex.EncodeToString(sum[:]))
}
