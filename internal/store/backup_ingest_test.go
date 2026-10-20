package store

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func (wrappingEncryptedLibraryDBCipher) EncryptFile(libraryID, plaintextPath, ciphertextPath string) error {
	data, err := os.ReadFile(plaintextPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ciphertextPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(ciphertextPath, append(append([]byte{}, wrappingEncryptedLibraryPrefix...), data...), 0o600)
}

type countingBackupIngestCipher struct {
	wrappingEncryptedLibraryDBCipher
	calls int
}

func (c *countingBackupIngestCipher) EncryptFile(libraryID, plaintextPath, ciphertextPath string) error {
	c.calls++
	return c.wrappingEncryptedLibraryDBCipher.EncryptFile(libraryID, plaintextPath, ciphertextPath)
}

type failingBackupIngestCipher struct{}

func (failingBackupIngestCipher) EncryptFile(libraryID, plaintextPath, ciphertextPath string) error {
	if err := os.WriteFile(ciphertextPath, []byte("partial ciphertext"), 0o600); err != nil {
		return err
	}
	return errors.New("encrypt failed")
}

func TestBackupIngestRoutesEncryptedCustodialUpload(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	st, err := Open(ctx, dataDir, Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, true, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	cipher := wrappingEncryptedLibraryDBCipher{}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	if err := st.SetBackupIngestPolicy(ctx, BackupIngestPolicy{
		ProfileID:  "profile-default",
		LibraryID:  libraryID,
		QuotaBytes: 1024,
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SetBackupIngestPolicy: %v", err)
	}

	body := []byte("encrypted backup upload bytes")
	result, err := st.BackupIngest(ctx, BackupIngestRequest{
		LibraryID:   libraryID,
		ProfileID:   "profile-default",
		Title:       "Camera upload",
		Container:   "jpg",
		Class:       MediaClassImage,
		SizeBytes:   int64(len(body)),
		ModTime:     time.Now().UTC(),
		Filter:      LibraryFilter{AllowedIDs: []LibraryID{libraryID}, Revealed: true},
		Encryptor:   cipher,
		PlainReader: bytes.NewReader(body),
	})
	if err != nil {
		t.Fatalf("BackupIngest: %v", err)
	}
	if result.Media.ID == "" || result.File.Path == "" || result.Fingerprint != backupIngestFingerprintForTest(body) {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.File.Path, filepath.Join("backups", "ingest", string(libraryID))) {
		t.Fatalf("ciphertext path = %q, want backup ingest path", result.File.Path)
	}
	raw, err := os.ReadFile(result.File.Path)
	if err != nil {
		t.Fatalf("read ciphertext: %v", err)
	}
	if !bytes.HasPrefix(raw, wrappingEncryptedLibraryPrefix) {
		t.Fatal("backup artifact was not written through encryptor")
	}
	if strings.Contains(string(raw[:len(wrappingEncryptedLibraryPrefix)]), string(body)) {
		t.Fatal("test cipher prefix unexpectedly includes plaintext body")
	}
	working := filepath.Join(dataDir, "backups", "ingest", "working")
	entries, err := os.ReadDir(working)
	if err != nil {
		t.Fatalf("read working dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("plaintext working files remained: %v", entries)
	}
	var catalogRows int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM media WHERE id = ?`, result.Media.ID).Scan(&catalogRows); err != nil {
		t.Fatalf("catalog media count: %v", err)
	}
	if catalogRows != 0 {
		t.Fatalf("encrypted upload wrote %d catalog rows, want encrypted DB only", catalogRows)
	}
	var encryptedRows int
	if err := st.encryptedDBs[libraryID].reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM media WHERE id = ?`, result.Media.ID).Scan(&encryptedRows); err != nil {
		t.Fatalf("encrypted media count: %v", err)
	}
	if encryptedRows != 1 {
		t.Fatalf("encrypted media rows = %d, want 1", encryptedRows)
	}
}

func TestBackupIngestContentAddressedDedup(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, true, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	cipher := &countingBackupIngestCipher{}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	body := []byte("same camera upload bytes")
	req := BackupIngestRequest{
		LibraryID:   libraryID,
		ProfileID:   "profile-default",
		Title:       "Camera upload",
		Container:   "jpg",
		Class:       MediaClassImage,
		SizeBytes:   int64(len(body)),
		ModTime:     time.Now().UTC(),
		Filter:      LibraryFilter{AllowedIDs: []LibraryID{libraryID}, Revealed: true},
		Encryptor:   cipher,
		PlainReader: bytes.NewReader(body),
	}
	first, err := st.BackupIngest(ctx, req)
	if err != nil {
		t.Fatalf("first BackupIngest: %v", err)
	}
	req.PlainReader = bytes.NewReader(body)
	second, err := st.BackupIngest(ctx, req)
	if err != nil {
		t.Fatalf("second BackupIngest: %v", err)
	}
	if first.Deduplicated {
		t.Fatal("first upload reported deduplicated")
	}
	if !second.Deduplicated {
		t.Fatal("second upload did not report content-addressed dedup")
	}
	if cipher.calls != 1 {
		t.Fatalf("encrypt calls = %d, want 1", cipher.calls)
	}
	if first.Fingerprint != second.Fingerprint || first.Media.ID != second.Media.ID {
		t.Fatalf("dedup result mismatch: first=%+v second=%+v", first, second)
	}
}

func TestBackupIngestFailedEncryptDoesNotPublishFinalCiphertext(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, true, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, wrappingEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	body := []byte("partial encrypt upload")
	req := BackupIngestRequest{
		LibraryID:   libraryID,
		ProfileID:   "profile-default",
		Title:       "Camera upload",
		Container:   "jpg",
		Class:       MediaClassImage,
		SizeBytes:   int64(len(body)),
		ModTime:     time.Now().UTC(),
		Filter:      LibraryFilter{AllowedIDs: []LibraryID{libraryID}, Revealed: true},
		Encryptor:   failingBackupIngestCipher{},
		PlainReader: bytes.NewReader(body),
	}
	if _, err := st.BackupIngest(ctx, req); err == nil {
		t.Fatal("BackupIngest succeeded with failing encryptor")
	}
	finalPath := st.backupIngestCiphertextPath(libraryID, strings.TrimPrefix(backupIngestFingerprintForTest(body), "sha256:"))
	if _, err := os.Stat(finalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("final ciphertext stat err = %v, want not-exist", err)
	}
	req.Encryptor = wrappingEncryptedLibraryDBCipher{}
	req.PlainReader = bytes.NewReader(body)
	result, err := st.BackupIngest(ctx, req)
	if err != nil {
		t.Fatalf("retry BackupIngest: %v", err)
	}
	if result.Deduplicated {
		t.Fatal("retry reported deduplicated after failed encrypt published no final ciphertext")
	}
}

func TestBackupIngestFailedImportRemovesNewCiphertext(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, true, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	cipher := wrappingEncryptedLibraryDBCipher{}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	body := []byte("valid encrypted upload with invalid import metadata")
	req := BackupIngestRequest{
		LibraryID:   libraryID,
		ProfileID:   "profile-default",
		Title:       "Camera upload",
		Container:   "mp4",
		Class:       MediaClassVideo,
		SizeBytes:   int64(len(body)),
		ModTime:     time.Now().UTC(),
		Filter:      LibraryFilter{AllowedIDs: []LibraryID{libraryID}, Revealed: true},
		Encryptor:   cipher,
		PlainReader: bytes.NewReader(body),
	}
	if _, err := st.BackupIngest(ctx, req); err == nil {
		t.Fatal("BackupIngest succeeded with invalid media import metadata")
	}
	finalPath := st.backupIngestCiphertextPath(libraryID, strings.TrimPrefix(backupIngestFingerprintForTest(body), "sha256:"))
	if _, err := os.Stat(finalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("final ciphertext stat err = %v, want not-exist", err)
	}
	req.Codec = "h264"
	req.PlainReader = bytes.NewReader(body)
	result, err := st.BackupIngest(ctx, req)
	if err != nil {
		t.Fatalf("retry BackupIngest: %v", err)
	}
	if result.Deduplicated {
		t.Fatal("retry reported deduplicated after failed import removed new ciphertext")
	}
}

func TestBackupIngestDurabilityPrecedesCatalogCommit(t *testing.T) {
	for _, tc := range []struct {
		name       string
		failAt     string
		wantEvents string
	}{
		{name: "payload sync", failAt: "payload", wantEvents: "payload"},
		{name: "directory sync", failAt: "parents", wantEvents: "payload,parents"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			libraryID := LibraryID("library-local")
			if err := st.SetLibraryBacking(ctx, libraryID, true, true); err != nil {
				t.Fatalf("SetLibraryBacking: %v", err)
			}
			cipher := wrappingEncryptedLibraryDBCipher{}
			if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
				t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
			}

			oldSyncPayload := backupIngestSyncPayload
			oldSyncParents := backupIngestSyncParents
			t.Cleanup(func() {
				backupIngestSyncPayload = oldSyncPayload
				backupIngestSyncParents = oldSyncParents
			})
			syncFailure := errors.New("injected power-loss boundary")
			events := []string{}
			backupIngestSyncPayload = func(path string) error {
				events = append(events, "payload")
				if tc.failAt == "payload" {
					return syncFailure
				}
				return syncTree(path)
			}
			backupIngestSyncParents = func(root, parent string) error {
				events = append(events, "parents")
				if tc.failAt == "parents" {
					return syncFailure
				}
				return syncCreatedParents(root, parent)
			}

			body := []byte("durability ordered backup ingest")
			fingerprint := backupIngestFingerprintForTest(body)
			_, err = st.BackupIngest(ctx, BackupIngestRequest{
				LibraryID:   libraryID,
				ProfileID:   "profile-default",
				Title:       "Durability",
				Container:   "jpg",
				Class:       MediaClassImage,
				SizeBytes:   int64(len(body)),
				ModTime:     time.Now().UTC(),
				Filter:      LibraryFilter{AllowedIDs: []LibraryID{libraryID}, Revealed: true},
				Encryptor:   cipher,
				PlainReader: bytes.NewReader(body),
			})
			if !errors.Is(err, syncFailure) {
				t.Fatalf("BackupIngest err = %v, want injected sync failure", err)
			}
			if got := strings.Join(events, ","); got != tc.wantEvents {
				t.Fatalf("durability events = %q, want %q", got, tc.wantEvents)
			}
			var rows int
			if err := st.encryptedDBs[libraryID].reader.QueryRowContext(
				ctx,
				`SELECT COUNT(*) FROM media_file WHERE content_fingerprint = ?`,
				fingerprint,
			).Scan(&rows); err != nil {
				t.Fatalf("query encrypted media file: %v", err)
			}
			if rows != 0 {
				t.Fatalf("catalog rows after sync failure = %d, want 0", rows)
			}
			finalPath := st.backupIngestCiphertextPath(libraryID, strings.TrimPrefix(fingerprint, "sha256:"))
			if _, statErr := os.Stat(finalPath); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("ciphertext after sync failure stat err = %v, want not-exist", statErr)
			}
		})
	}
}

func TestBackupIngestOpenSweepsOrphanedPlainScratch(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	cipher := wrappingEncryptedLibraryDBCipher{}
	st, err := Open(ctx, dataDir, Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, true, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	media, _, err := st.ImportMediaFile(ctx, encryptedImport(libraryID, "sha256:orphan-sweep", "/enc/orphan-sweep.mkv"))
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	var beforeEncryptedRows int
	if err := st.encryptedDBs[libraryID].reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM media WHERE id = ?`, media.ID).Scan(&beforeEncryptedRows); err != nil {
		t.Fatalf("encrypted media count before reopen: %v", err)
	}
	if beforeEncryptedRows != 1 {
		t.Fatalf("encrypted media rows before reopen = %d, want 1", beforeEncryptedRows)
	}
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	working := filepath.Join(dataDir, "backups", "ingest", "working")
	if err := os.MkdirAll(working, 0o700); err != nil {
		t.Fatalf("mkdir working: %v", err)
	}
	orphan := filepath.Join(working, ".backup-upload-orphan.plain")
	secret := []byte("orphaned plaintext backup upload")
	if err := os.WriteFile(orphan, secret, 0o600); err != nil {
		t.Fatalf("write orphan scratch: %v", err)
	}
	probe := filepath.Join(t.TempDir(), "orphan-probe")
	if err := os.Link(orphan, probe); err != nil {
		t.Fatalf("link orphan probe: %v", err)
	}

	reopened, err := Open(ctx, dataDir, Options{EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan scratch stat err = %v, want not-exist", err)
	}
	probeBytes, err := os.ReadFile(probe)
	if err != nil {
		t.Fatalf("read wipe probe: %v", err)
	}
	if !bytes.Equal(probeBytes, make([]byte, len(secret))) {
		t.Fatalf("orphan scratch probe = %q, want wiped zeros", probeBytes)
	}
	if reopened.EncryptedLibraryDBRegistered(libraryID) {
		t.Fatal("sealed encrypted library registered during store open")
	}
	var catalogRows int
	if err := reopened.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM media WHERE id = ?`, media.ID).Scan(&catalogRows); err != nil {
		t.Fatalf("catalog media count after reopen: %v", err)
	}
	if catalogRows != 0 {
		t.Fatalf("catalog media rows after reopen = %d, want 0", catalogRows)
	}
	if err := reopened.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB after reopen: %v", err)
	}
	var afterEncryptedRows int
	if err := reopened.encryptedDBs[libraryID].reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM media WHERE id = ?`, media.ID).Scan(&afterEncryptedRows); err != nil {
		t.Fatalf("encrypted media count after reopen: %v", err)
	}
	if afterEncryptedRows != beforeEncryptedRows {
		t.Fatalf("encrypted media rows after reopen = %d, want %d", afterEncryptedRows, beforeEncryptedRows)
	}
}

func TestBackupIngestPolicyFailures(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, true, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	cipher := wrappingEncryptedLibraryDBCipher{}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	req := BackupIngestRequest{
		LibraryID:   libraryID,
		ProfileID:   "profile-default",
		Title:       "Upload",
		Container:   "bin",
		SizeBytes:   4,
		Filter:      LibraryFilter{AllowedIDs: []LibraryID{libraryID}, Revealed: true},
		Encryptor:   cipher,
		PlainReader: strings.NewReader("data"),
	}
	if err := st.SetBackupIngestPolicy(ctx, BackupIngestPolicy{ProfileID: "profile-default", LibraryID: libraryID, Paused: true, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("SetBackupIngestPolicy paused: %v", err)
	}
	if _, err := st.BackupIngest(ctx, req); !errors.Is(err, ErrBackupIngestPaused) {
		t.Fatalf("paused BackupIngest err = %v, want ErrBackupIngestPaused", err)
	}
	if err := st.SetBackupIngestPolicy(ctx, BackupIngestPolicy{ProfileID: "profile-default", LibraryID: libraryID, QuotaBytes: 1, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("SetBackupIngestPolicy quota: %v", err)
	}
	req.PlainReader = strings.NewReader("data")
	if _, err := st.BackupIngest(ctx, req); !errors.Is(err, ErrBackupIngestQuotaExceeded) {
		t.Fatalf("quota BackupIngest err = %v, want ErrBackupIngestQuotaExceeded", err)
	}
	if err := st.SetBackupIngestPolicy(ctx, BackupIngestPolicy{ProfileID: "profile-default", LibraryID: libraryID, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("SetBackupIngestPolicy headroom: %v", err)
	}
	req.SizeBytes = math.MaxInt64
	req.PlainReader = strings.NewReader("data")
	if _, err := st.BackupIngest(ctx, req); !errors.Is(err, ErrBackupIngestHeadroom) {
		t.Fatalf("headroom BackupIngest err = %v, want ErrBackupIngestHeadroom", err)
	}
}

func TestBackupIngestRejectsDeclaredSizeMismatch(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, true, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, wrappingEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	body := []byte("actual bytes")
	for _, declared := range []int64{int64(len(body) - 1), int64(len(body) + 1)} {
		cipher := &countingBackupIngestCipher{}
		_, err := st.BackupIngest(ctx, BackupIngestRequest{
			LibraryID: libraryID, ProfileID: "profile-default", SizeBytes: declared,
			Title: "Upload", Container: "jpg", Class: MediaClassImage,
			Filter:    LibraryFilter{AllowedIDs: []LibraryID{libraryID}, Revealed: true},
			Encryptor: cipher, PlainReader: bytes.NewReader(body),
		})
		if err == nil || !strings.Contains(err.Error(), "size") {
			t.Fatalf("declared size %d: err = %v, want size validation error", declared, err)
		}
		if cipher.calls != 0 {
			t.Fatalf("declared size %d: encrypt calls = %d, want 0", declared, cipher.calls)
		}
	}
}

// objectBoundBackupIngestCipher implements both the legacy and the
// identity-bound encryptor seams and records which one the ingest path used.
type objectBoundBackupIngestCipher struct {
	wrappingEncryptedLibraryDBCipher
	legacyCalls  int
	objectCalls  int
	fingerprints []string
}

func (c *objectBoundBackupIngestCipher) EncryptFile(libraryID, plaintextPath, ciphertextPath string) error {
	c.legacyCalls++
	return c.wrappingEncryptedLibraryDBCipher.EncryptFile(libraryID, plaintextPath, ciphertextPath)
}

func (c *objectBoundBackupIngestCipher) EncryptObjectFile(libraryID, plaintextPath, ciphertextPath, contentFingerprint string) error {
	c.objectCalls++
	c.fingerprints = append(c.fingerprints, contentFingerprint)
	return c.wrappingEncryptedLibraryDBCipher.EncryptFile(libraryID, plaintextPath, ciphertextPath)
}

// TestBackupIngestPrefersObjectBoundEncryptor proves the upload write carries
// the object's persisted content fingerprint: when the
// encryptor understands object identity, ingest routes every write through it
// and never falls back to the context-free seam.
func TestBackupIngestPrefersObjectBoundEncryptor(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, true, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	cipher := &objectBoundBackupIngestCipher{}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, wrappingEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	if err := st.SetBackupIngestPolicy(ctx, BackupIngestPolicy{
		ProfileID:  "profile-default",
		LibraryID:  libraryID,
		QuotaBytes: 1024,
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SetBackupIngestPolicy: %v", err)
	}

	body := []byte("object-bound upload bytes")
	result, err := st.BackupIngest(ctx, BackupIngestRequest{
		LibraryID:   libraryID,
		ProfileID:   "profile-default",
		Title:       "Object Bound",
		Container:   "mp4",
		Codec:       "h264",
		Class:       MediaClassVideo,
		SizeBytes:   int64(len(body)),
		ModTime:     time.Now().UTC(),
		Filter:      LibraryFilter{AllowedIDs: []LibraryID{libraryID}, Revealed: true},
		Encryptor:   cipher,
		PlainReader: bytes.NewReader(body),
	})
	if err != nil {
		t.Fatalf("BackupIngest: %v", err)
	}
	if cipher.objectCalls != 1 || cipher.legacyCalls != 0 {
		t.Fatalf("object calls = %d, legacy calls = %d; want the identity-bound seam only", cipher.objectCalls, cipher.legacyCalls)
	}
	if len(cipher.fingerprints) != 1 || cipher.fingerprints[0] != result.Fingerprint {
		t.Fatalf("encryptor fingerprint = %v, want the persisted %q", cipher.fingerprints, result.Fingerprint)
	}
	if !strings.HasPrefix(result.Fingerprint, "sha256:") {
		t.Fatalf("fingerprint = %q, want a persisted sha256 identity", result.Fingerprint)
	}
}
