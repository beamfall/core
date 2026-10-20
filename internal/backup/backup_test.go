package backup

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/beamfall/core/internal/libencrypt"
)

// memBucket is an in-memory BucketClient for tests: no network, no MinIO required.
type memBucket struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newMemBucket() *memBucket { return &memBucket{objects: map[string][]byte{}} }

func (m *memBucket) Put(_ context.Context, key string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.objects[key] = data
	m.mu.Unlock()
	return nil
}

func (m *memBucket) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	data, ok := m.objects[key]
	m.mu.Unlock()
	if !ok {
		return nil, errors.New("backup/test: key not found: " + key)
	}
	// Return a copy so mutation of the returned data doesn't affect the store.
	cp := make([]byte, len(data))
	copy(cp, data)
	return io.NopCloser(bytes.NewReader(cp)), nil
}

func (m *memBucket) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	delete(m.objects, key)
	m.mu.Unlock()
	return nil
}

func (m *memBucket) List(_ context.Context, prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for k := range m.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
}

// rawBytes returns the stored bytes for a backup key (for black-box inspection).
func (m *memBucket) rawBytes(backupID string) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.objects[bucketKey(backupID)]
}

type failingReader struct {
	data      []byte
	failAfter int
	err       error
	off       int
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.off >= r.failAfter {
		return 0, r.err
	}
	n := copy(p, r.data[r.off:min(len(r.data), r.failAfter)])
	r.off += n
	return n, nil
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	t.Parallel()
	bucket := newMemBucket()
	svc := NewService(bucket)
	ctx := context.Background()

	key, err := MintBackupKey()
	if err != nil {
		t.Fatal("MintBackupKey:", err)
	}

	want := []byte("hello, zero-knowledge backup world — some media bytes")
	if err := svc.Backup(ctx, key, "lib1/snap1", bytes.NewReader(want)); err != nil {
		t.Fatal("Backup:", err)
	}

	// Verify the stored bytes are NOT the plaintext (ciphertext-only at rest).
	stored := bucket.rawBytes("lib1/snap1")
	if bytes.Contains(stored, want) {
		t.Error("bucket contains plaintext — ciphertext-only invariant violated")
	}

	// Restore using only the BackupKey — no Manager, no server, no library ID.
	var got bytes.Buffer
	if err := svc.Restore(ctx, key, "lib1/snap1", &got); err != nil {
		t.Fatal("Restore:", err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Errorf("round-trip mismatch: got %q, want %q", got.Bytes(), want)
	}
}

func TestBackupWithPublicKeyNeedsNoSecret(t *testing.T) {
	t.Parallel()
	bucket := newMemBucket()
	svc := NewService(bucket)
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := key.PublicWrapKey()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("sealed using the public key only")
	if err := svc.BackupWithPublicKey(context.Background(), publicKey, "snap/public-only", bytes.NewReader(want)); err != nil {
		t.Fatal("BackupWithPublicKey:", err)
	}
	stored := bucket.rawBytes("snap/public-only")
	if len(stored) < bbk1HeaderFixed || stored[4] != bbk1VersionV2 {
		t.Fatalf("envelope header = %x, want BBK1 v2", stored[:min(len(stored), bbk1HeaderFixed)])
	}
	var got bytes.Buffer
	if err := svc.Restore(context.Background(), key, "snap/public-only", &got); err != nil {
		t.Fatal("Restore:", err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("restored %q, want %q", got.Bytes(), want)
	}
}

func TestEnvelopeV2UsesFreshEphemeralSenderPerSet(t *testing.T) {
	t.Parallel()
	bucket := newMemBucket()
	svc := NewService(bucket)
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := key.PublicWrapKey()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"snap/one", "snap/two"} {
		if err := svc.BackupWithPublicKey(context.Background(), publicKey, id, strings.NewReader("same")); err != nil {
			t.Fatal(err)
		}
	}
	one := bucket.rawBytes("snap/one")
	two := bucket.rawBytes("snap/two")
	oneSender := one[bbk1HeaderFixed : bbk1HeaderFixed+x25519KeySize]
	twoSender := two[bbk1HeaderFixed : bbk1HeaderFixed+x25519KeySize]
	if bytes.Equal(oneSender, twoSender) {
		t.Fatal("two sets reused the same ephemeral X25519 sender public key")
	}
}

func TestRestoreReadsBBK1V1(t *testing.T) {
	t.Parallel()
	bucket := newMemBucket()
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("legacy BBK1 v1 payload")
	bucket.objects[bucketKey("snap/v1")] = makeV1Envelope(t, key, want)
	var got bytes.Buffer
	if err := NewService(bucket).Restore(context.Background(), key, "snap/v1", &got); err != nil {
		t.Fatal("Restore v1:", err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("restored %q, want %q", got.Bytes(), want)
	}
}

func TestStorePublicWrapKeyPersistsNoPrivateMaterial(t *testing.T) {
	t.Parallel()
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := key.PublicWrapKey()
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	path, err := StorePublicWrapKey(dataDir, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != publicWrapKeyFileName {
		t.Fatalf("data dir entries = %v, want only %s", entries, publicWrapKeyFileName)
	}
	stored, err := os.ReadFile(path) //nolint:gosec // path is returned from StorePublicWrapKey under t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	secret := key.Bytes()
	defer zero(secret)
	if bytes.Contains(stored, secret) || strings.Contains(string(stored), key.Display()) {
		t.Fatal("persisted wrap-key file contains private BackupKey material")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("public wrap key mode = %o, want 600", got)
	}
	loaded, err := LoadPublicWrapKey(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded.Bytes(), publicKey.Bytes()) {
		t.Fatal("loaded public wrap key differs")
	}
}

func TestInspectEnvelopeDecryptsAndReportsV2(t *testing.T) {
	t.Parallel()
	bucket := newMemBucket()
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("inspectable plaintext")
	if err := NewService(bucket).Backup(context.Background(), key, "snap/inspect", bytes.NewReader(want)); err != nil {
		t.Fatal(err)
	}
	publicKey, err := key.PublicWrapKey()
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	info, err := InspectEnvelope(key, bytes.NewReader(bucket.rawBytes("snap/inspect")), &got)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != bbk1VersionV2 || info.RecipientKeyFingerprint != publicKey.Fingerprint() {
		t.Fatalf("inspect info = %+v", info)
	}
	if len(info.EphemeralSenderPublicKey.Bytes()) != x25519KeySize {
		t.Fatalf("sender public key length = %d", len(info.EphemeralSenderPublicKey.Bytes()))
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("inspected plaintext = %q, want %q", got.Bytes(), want)
	}
}

func TestInspectEnvelopeRejectsV2WrappedDEKTampering(t *testing.T) {
	t.Parallel()
	bucket := newMemBucket()
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := NewService(bucket).Backup(context.Background(), key, "snap/tamper", strings.NewReader("authenticated payload")); err != nil {
		t.Fatal(err)
	}
	envelope := bucket.rawBytes("snap/tamper")
	tests := []struct {
		name       string
		offset     int
		acceptable []error
	}{
		{name: "sender", offset: bbk1HeaderFixed, acceptable: []error{ErrWrongKey, ErrCorrupt}},
		{name: "nonce", offset: bbk1HeaderFixed + x25519KeySize, acceptable: []error{ErrWrongKey}},
		{name: "ciphertext", offset: bbk1HeaderFixed + x25519KeySize + gcmNonceSize, acceptable: []error{ErrWrongKey}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tampered := append([]byte(nil), envelope...)
			tampered[tc.offset] ^= 0x80
			var got bytes.Buffer
			_, err := InspectEnvelope(key, bytes.NewReader(tampered), &got)
			if err == nil {
				t.Fatal("InspectEnvelope accepted tampered wrapped DEK")
			}
			matched := false
			for _, acceptable := range tc.acceptable {
				matched = matched || errors.Is(err, acceptable)
			}
			if !matched {
				t.Fatalf("InspectEnvelope error = %v, want one of %v", err, tc.acceptable)
			}
			if got.Len() != 0 {
				t.Fatalf("InspectEnvelope wrote %d plaintext bytes", got.Len())
			}
		})
	}
}

func makeV1Envelope(t *testing.T, key BackupKey, plaintext []byte) []byte {
	t.Helper()
	dek, err := libencrypt.NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	defer libencrypt.ZeroKey(dek)
	keyBytes := key.Bytes()
	defer zero(keyBytes)
	wrapped, err := libencrypt.AEADWrap(keyBytes, dek)
	if err != nil {
		t.Fatal(err)
	}
	var envelope bytes.Buffer
	envelope.WriteString(bbk1Magic)
	envelope.WriteByte(bbk1VersionV1)
	if err := binary.Write(&envelope, binary.BigEndian, uint16(len(wrapped))); err != nil { //nolint:gosec // AEADWrap output is fixed at 60 bytes
		t.Fatal(err)
	}
	envelope.Write(wrapped)
	if err := libencrypt.EncryptStream(&envelope, bytes.NewReader(plaintext), dek); err != nil {
		t.Fatal(err)
	}
	return envelope.Bytes()
}

func TestBackupReadErrorDoesNotOverwriteExistingObject(t *testing.T) {
	t.Parallel()
	bucket := newMemBucket()
	svc := NewService(bucket)
	ctx := context.Background()

	key, err := MintBackupKey()
	if err != nil {
		t.Fatal("MintBackupKey:", err)
	}

	backupID := "snap/keep-good"
	want := []byte("previous good backup")
	if err := svc.Backup(ctx, key, backupID, bytes.NewReader(want)); err != nil {
		t.Fatal("Backup good object:", err)
	}
	before := append([]byte(nil), bucket.rawBytes(backupID)...)

	sourceErr := errors.New("source read failed")
	err = svc.Backup(ctx, key, backupID, &failingReader{
		data:      []byte("replacement backup that fails mid-stream"),
		failAfter: 12,
		err:       sourceErr,
	})
	if !errors.Is(err, sourceErr) {
		t.Fatalf("Backup error = %v, want source error", err)
	}
	if after := bucket.rawBytes(backupID); !bytes.Equal(after, before) {
		t.Fatal("failed backup overwrote previous good object")
	}

	var got bytes.Buffer
	if err := svc.Restore(ctx, key, backupID, &got); err != nil {
		t.Fatal("Restore preserved object:", err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("restored preserved object = %q, want %q", got.Bytes(), want)
	}
}

func TestRestoreWrongKey(t *testing.T) {
	t.Parallel()
	bucket := newMemBucket()
	svc := NewService(bucket)
	ctx := context.Background()

	rightKey, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Backup(ctx, rightKey, "snap/x", bytes.NewReader([]byte("secret"))); err != nil {
		t.Fatal("Backup:", err)
	}

	wrongKey, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	var dst bytes.Buffer
	err = svc.Restore(ctx, wrongKey, "snap/x", &dst)
	if err == nil {
		t.Fatal("expected error with wrong key, got nil")
	}
	if !errors.Is(err, libencrypt.ErrUnwrap) {
		t.Errorf("expected ErrUnwrap chain, got: %v", err)
	}
}

func TestRestoreTruncatedCiphertext(t *testing.T) {
	t.Parallel()
	bucket := newMemBucket()
	svc := NewService(bucket)
	ctx := context.Background()

	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Backup(ctx, key, "snap/trunc", bytes.NewReader([]byte("some data"))); err != nil {
		t.Fatal("Backup:", err)
	}

	// Truncate the stored ciphertext to only the fixed header (discard the rest).
	stored := bucket.rawBytes("snap/trunc")
	if len(stored) <= bbk1HeaderFixed {
		t.Skip("stored too short to truncate meaningfully")
	}
	// Keep only the fixed header — this truncates the wrapped DEK field and the stream.
	bucket.mu.Lock()
	bucket.objects[bucketKey("snap/trunc")] = stored[:bbk1HeaderFixed]
	bucket.mu.Unlock()

	var dst bytes.Buffer
	err = svc.Restore(ctx, key, "snap/trunc", &dst)
	if err == nil {
		t.Fatal("expected error on truncated ciphertext, got nil")
	}
	// Should be ErrCorrupt (header malformed) or ErrUnwrap/ErrWrongKey (AEAD fail).
	if !errors.Is(err, ErrCorrupt) && !errors.Is(err, libencrypt.ErrUnwrap) {
		t.Errorf("unexpected error type for truncated input: %v", err)
	}
}

func TestBackupKeyIsolation(t *testing.T) {
	t.Parallel()
	bucket := newMemBucket()
	svc := NewService(bucket)
	ctx := context.Background()

	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte("same plaintext for both backups")

	// Two Backups under the same backupID: each uses a fresh ephemeral DEK, so
	// the ciphertexts differ (even with the same key + plaintext).
	if err := svc.Backup(ctx, key, "snap/iso", bytes.NewReader(payload)); err != nil {
		t.Fatal("Backup 1:", err)
	}
	first := make([]byte, len(bucket.rawBytes("snap/iso")))
	copy(first, bucket.rawBytes("snap/iso"))

	if err := svc.Backup(ctx, key, "snap/iso", bytes.NewReader(payload)); err != nil {
		t.Fatal("Backup 2:", err)
	}
	second := bucket.rawBytes("snap/iso")

	if bytes.Equal(first, second) {
		t.Error("two backups of the same plaintext produced identical ciphertext — ephemeral DEK not randomized")
	}

	// Both are still restorable.
	var got bytes.Buffer
	if err := svc.Restore(ctx, key, "snap/iso", &got); err != nil {
		t.Fatal("Restore after second Backup:", err)
	}
	if !bytes.Equal(got.Bytes(), payload) {
		t.Errorf("restored bytes differ: got %q, want %q", got.Bytes(), payload)
	}
}

func TestRestoreBadMagic(t *testing.T) {
	t.Parallel()
	bucket := newMemBucket()
	svc := NewService(bucket)
	ctx := context.Background()

	// Store random-looking bytes that don't start with BBK1.
	bucket.mu.Lock()
	bucket.objects[bucketKey("snap/bad")] = []byte("NOTBK1somerandombytes0000000000000000000000000000000")
	bucket.mu.Unlock()

	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	var dst bytes.Buffer
	err = svc.Restore(ctx, key, "snap/bad", &dst)
	if !errors.Is(err, ErrCorrupt) {
		t.Errorf("expected ErrCorrupt for bad magic, got: %v", err)
	}
}

func TestParseBackupKeyRoundTrip(t *testing.T) {
	t.Parallel()
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal("MintBackupKey:", err)
	}
	display := key.Display()
	if display == "" {
		t.Fatal("Display() is empty")
	}
	parsed, err := ParseBackupKey(display)
	if err != nil {
		t.Fatalf("ParseBackupKey(%q): %v", display, err)
	}
	if !bytes.Equal(key.Bytes(), parsed.Bytes()) {
		t.Error("parsed BackupKey Bytes() differ from original")
	}
}
