package backup

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/libencrypt"
)

// Context-bound v3 envelope tests: the payload stream
// is cryptographically bound to the caller-supplied backup ID + backup-payload
// class, and the authenticated envelope version declares the expected at-rest
// format — never sniffed from the payload, never derived from paths.

func mintTestKey(t *testing.T) BackupKey {
	t.Helper()
	key, err := MintBackupKey()
	if err != nil {
		t.Fatalf("MintBackupKey: %v", err)
	}
	return key
}

func TestBackupObjectWritesV3AndRestores(t *testing.T) {
	ctx := context.Background()
	bucket := newMemBucket()
	svc := NewService(bucket)
	key := mintTestKey(t)
	want := []byte("context-bound backup payload bytes")

	if err := svc.BackupObject(ctx, key, "lib1/snap-ctx", bytes.NewReader(want)); err != nil {
		t.Fatalf("BackupObject: %v", err)
	}
	stored := bucket.rawBytes("lib1/snap-ctx")
	if len(stored) < bbk1HeaderFixed || stored[4] != bbk1VersionV3 {
		t.Fatalf("stored envelope version = %d, want v3", stored[4])
	}
	// The v3 payload is a libencrypt v2 stream (BFE2), not the legacy v1 one.
	body := stored[bbk1HeaderFixed+wrappedDEKV2Len:]
	if !bytes.HasPrefix(body, []byte("BFE2")) {
		t.Fatalf("v3 payload magic = %q, want context-bound BFE2 stream", body[:4])
	}

	var got bytes.Buffer
	if err := svc.Restore(ctx, key, "lib1/snap-ctx", &got); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatal("v3 restore not byte-exact")
	}
}

// TestV3RestoreRejectsSwappedBackupID: the same ciphertext presented under a
// different backup ID (a whole-object swap between bucket keys) fails before
// any plaintext is written.
func TestV3RestoreRejectsSwappedBackupID(t *testing.T) {
	ctx := context.Background()
	bucket := newMemBucket()
	svc := NewService(bucket)
	key := mintTestKey(t)

	if err := svc.BackupObject(ctx, key, "snap/original", strings.NewReader("payload A")); err != nil {
		t.Fatalf("BackupObject: %v", err)
	}
	// Swap: copy the ciphertext under a different backup ID's bucket key.
	swapped := bucket.rawBytes("snap/original")
	if err := bucket.Put(ctx, bucketKey("snap/other"), bytes.NewReader(swapped)); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := svc.Restore(ctx, key, "snap/other", &out)
	if err == nil {
		t.Fatal("restore under a swapped backup ID must fail")
	}
	if out.Len() != 0 {
		t.Fatalf("swapped restore wrote %d plaintext bytes before failing", out.Len())
	}
	// The genuine ID still restores.
	if err := svc.Restore(ctx, key, "snap/original", &out); err != nil {
		t.Fatalf("genuine restore: %v", err)
	}
}

// TestV3EnvelopeVersionIsAuthenticated: flipping the (header) version byte in
// either direction fails DEK unseal — the expected at-rest format travels with
// the envelope, authenticated by the sealed-box AAD, and is never sniffed.
func TestV3EnvelopeVersionIsAuthenticated(t *testing.T) {
	ctx := context.Background()
	bucket := newMemBucket()
	svc := NewService(bucket)
	key := mintTestKey(t)

	if err := svc.BackupObject(ctx, key, "snap/v3", strings.NewReader("v3 payload")); err != nil {
		t.Fatalf("BackupObject: %v", err)
	}
	publicKey, err := key.PublicWrapKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.BackupWithPublicKey(ctx, publicKey, "snap/v2", strings.NewReader("v2 payload")); err != nil {
		t.Fatalf("BackupWithPublicKey: %v", err)
	}

	flip := func(id string, version byte) []byte {
		raw := append([]byte{}, bucket.rawBytes(id)...)
		raw[4] = version
		return raw
	}
	var out bytes.Buffer
	// v3 → v2 downgrade: sealed-box AAD mismatch (and a v2 decoder would face a
	// BFE2 stream). Must fail with no plaintext.
	if _, err := InspectEnvelope(key, bytes.NewReader(flip("snap/v3", bbk1VersionV2)), &out); err == nil || out.Len() != 0 {
		t.Fatalf("v3→v2 version flip accepted (err=%v, wrote %d bytes)", err, out.Len())
	}
	// v2 → v3 upgrade forgery.
	out.Reset()
	if _, err := InspectEnvelopeWithID(key, "snap/v2", bytes.NewReader(flip("snap/v2", bbk1VersionV3)), &out); err == nil || out.Len() != 0 {
		t.Fatalf("v2→v3 version flip accepted (err=%v, wrote %d bytes)", err, out.Len())
	}
}

func TestInspectEnvelopeV3RequiresBackupID(t *testing.T) {
	ctx := context.Background()
	bucket := newMemBucket()
	svc := NewService(bucket)
	key := mintTestKey(t)

	if err := svc.BackupObject(ctx, key, "snap/needs-id", strings.NewReader("payload")); err != nil {
		t.Fatalf("BackupObject: %v", err)
	}
	raw := bucket.rawBytes("snap/needs-id")

	var out bytes.Buffer
	if _, err := InspectEnvelope(key, bytes.NewReader(raw), &out); err == nil {
		t.Fatal("ID-less InspectEnvelope must refuse a v3 envelope")
	}
	info, err := InspectEnvelopeWithID(key, "snap/needs-id", bytes.NewReader(raw), &out)
	if err != nil {
		t.Fatalf("InspectEnvelopeWithID: %v", err)
	}
	if info.Version != bbk1VersionV3 || out.String() != "payload" {
		t.Fatalf("inspect = version %d payload %q", info.Version, out.String())
	}
	// A wrong backup ID fails before plaintext.
	out.Reset()
	if _, err := InspectEnvelopeWithID(key, "snap/wrong-id", bytes.NewReader(raw), &out); err == nil || out.Len() != 0 {
		t.Fatalf("wrong backup ID accepted (err=%v, wrote %d bytes)", err, out.Len())
	}
}

// TestV3WrongKeyStillFailsClosed: the classic wrong-BackupKey failure mode is
// unchanged for v3 envelopes.
func TestV3WrongKeyStillFailsClosed(t *testing.T) {
	ctx := context.Background()
	bucket := newMemBucket()
	svc := NewService(bucket)
	rightKey := mintTestKey(t)
	wrongKey := mintTestKey(t)

	if err := svc.BackupObject(ctx, rightKey, "snap/keyed", strings.NewReader("secret")); err != nil {
		t.Fatalf("BackupObject: %v", err)
	}
	var out bytes.Buffer
	err := svc.Restore(ctx, wrongKey, "snap/keyed", &out)
	if !errors.Is(err, libencrypt.ErrUnwrap) || out.Len() != 0 {
		t.Fatalf("wrong key: err = %v, wrote %d bytes; want ErrUnwrap and nothing", err, out.Len())
	}
}
