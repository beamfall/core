package backup

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/beamfall/core/internal/libencrypt"
)

// Sentinel errors returned by the backup package.
var (
	// ErrWrongKey is returned when Restore fails to unwrap the ephemeral DEK:
	// the supplied BackupKey is wrong, or the stored ciphertext is corrupt at the
	// envelope header. It wraps libencrypt.ErrUnwrap so callers can check via
	// errors.Is(err, libencrypt.ErrUnwrap).
	ErrWrongKey = fmt.Errorf("backup: wrong key: %w", libencrypt.ErrUnwrap)

	// ErrCorrupt is returned when the BBK1 envelope header is malformed:
	// bad magic, unknown version, impossible wrapped-DEK length, or a truncated
	// header. The underlying data may be intact; only the header is unreadable.
	ErrCorrupt = errors.New("backup: corrupt BBK1 envelope")
)

// bbk1Magic is the four-byte prefix that identifies a Beamfall Backup ciphertext.
const bbk1Magic = "BBK1"

const (
	bbk1VersionV1 = 1
	bbk1VersionV2 = 2
	// bbk1VersionV3 seals the same X25519 sealed-box wrapped DEK as v2 but the
	// payload is the context-bound libencrypt v2 at-rest stream
	//: the stream is cryptographically bound to the
	// caller-supplied backup ID + backup-payload class, independently of any
	// path or bucket key, and the envelope version (authenticated through the
	// sealed-box AAD) declares the expected at-rest format — the payload is
	// never sniffed.
	bbk1VersionV3 = 3
	// bbk1Version is the version the legacy path-identified Backup API writes;
	// BackupObject* writes bbk1VersionV3.
	bbk1Version = bbk1VersionV2
)

// backupContextScope is the fixed non-secret scope under which every backup
// payload's ObjectContext lives (backups are server-scoped, not per-library).
const backupContextScope = "beamfall-backup"

// backupObjectContext binds one backup payload to its caller-supplied backup
// ID — the immutable identity a restore/inspect must present.
func backupObjectContext(backupID string) libencrypt.ObjectContext {
	return libencrypt.ObjectContext{
		LibraryID:         backupContextScope,
		ObjectClass:       libencrypt.ObjectClassBackupPayload,
		ImmutableObjectID: backupID,
	}
}

// bbk1HeaderFixed is the length of the fixed part of the BBK1 header before the
// wrapped-DEK bytes: magic(4) + version(1) + wrappedDEKLen(2).
const bbk1HeaderFixed = 7

const (
	wrappedDEKV1Max = 64
	x25519KeySize   = 32
	gcmNonceSize    = 12
	wrappedDEKV2Len = x25519KeySize + gcmNonceSize + 32 + 16
	wrappedDEKMax   = 128
)

var sealedBoxInfo = []byte("beamfall backup sealed DEK v2")

// EnvelopeInfo is safe diagnostic metadata returned by InspectEnvelope.
type EnvelopeInfo struct {
	Version                  int
	RecipientKeyFingerprint  string
	EphemeralSenderPublicKey PublicWrapKey
}

// bucketKey returns the deterministic bucket key for a backupID.
func bucketKey(backupID string) string { return "backup/" + backupID }

// Service orchestrates zero-knowledge off-site backup and restore.
//
// Backup() client-side-encrypts the source stream under a fresh ephemeral DEK,
// wraps the DEK in the BackupKey, and stores the BBK1 ciphertext in the bucket.
// Restore() reads the ciphertext, unwraps the DEK using only the BackupKey, and
// decrypts the stream — no server, no Manager, no Beamfall account required.
type Service struct {
	bucket BucketClient
}

// NewService creates a Service backed by the given BucketClient. The Service
// stores only opaque ciphertext; no plaintext ever crosses the BucketClient seam.
func NewService(bucket BucketClient) *Service {
	return &Service{bucket: bucket}
}

// Backup encrypts src and replicates the ciphertext to the bucket under backupID.
//
// This compatibility convenience derives the public wrap key transiently and
// delegates to BackupWithPublicKey. Normal server operation should call
// BackupWithPublicKey so sealing never receives the user-held secret.
//
// backupID is an opaque caller-supplied identifier (library + snapshot label). It
// is used as the bucket key suffix; it is not validated beyond being non-empty.
func (s *Service) Backup(ctx context.Context, key BackupKey, backupID string, src io.Reader) error {
	publicKey, err := key.PublicWrapKey()
	if err != nil {
		return fmt.Errorf("backup: derive public wrap key: %w", err)
	}
	return s.BackupWithPublicKey(ctx, publicKey, backupID, src)
}

// BackupWithPublicKey writes envelope v2 using only the persisted public wrap
// key. A fresh X25519 sender keypair is generated for every call, and only its
// public key is included in the envelope. The v2 payload is the legacy v1
// at-rest stream; new context-bound writes use BackupObjectWithPublicKey.
func (s *Service) BackupWithPublicKey(ctx context.Context, publicKey PublicWrapKey, backupID string, src io.Reader) error {
	return s.backupWithPublicKey(ctx, publicKey, backupID, src, bbk1VersionV2)
}

// BackupObject encrypts src as a v3 envelope whose payload stream is
// cryptographically bound to backupID (backup-payload class) — the
// context-bound successor to Backup. Restore presents the same backupID; a
// payload swapped between backup IDs fails before any plaintext is returned.
func (s *Service) BackupObject(ctx context.Context, key BackupKey, backupID string, src io.Reader) error {
	publicKey, err := key.PublicWrapKey()
	if err != nil {
		return fmt.Errorf("backup: derive public wrap key: %w", err)
	}
	return s.BackupObjectWithPublicKey(ctx, publicKey, backupID, src)
}

// BackupObjectWithPublicKey is BackupObject using only the persisted public
// wrap key (normal server operation — sealing never sees the user-held secret).
func (s *Service) BackupObjectWithPublicKey(ctx context.Context, publicKey PublicWrapKey, backupID string, src io.Reader) error {
	return s.backupWithPublicKey(ctx, publicKey, backupID, src, bbk1VersionV3)
}

func (s *Service) backupWithPublicKey(ctx context.Context, publicKey PublicWrapKey, backupID string, src io.Reader, version byte) error {
	if backupID == "" {
		return errors.New("backup: backupID is required")
	}

	// Generate a fresh ephemeral DEK for this backup. It is independent of any
	// library DEK held by the Manager — restore requires only the BackupKey and
	// the ciphertext, with no running server.
	ephemeralDEK, err := libencrypt.NewRandomKey()
	if err != nil {
		return fmt.Errorf("backup: generate ephemeral DEK: %w", err)
	}
	defer libencrypt.ZeroKey(ephemeralDEK)

	wrappedDEK, err := sealDEK(publicKey, ephemeralDEK, version)
	if err != nil {
		return fmt.Errorf("backup: wrap DEK: %w", err)
	}
	if len(wrappedDEK) > wrappedDEKMax {
		return fmt.Errorf("backup: wrapped DEK length %d exceeds max %d (programming error)", len(wrappedDEK), wrappedDEKMax)
	}

	// Build the BBK1 envelope header.
	// Layout: magic(4) | version(1) | wrappedDEKLen(2,BE) | wrappedDEK(variable)
	var hdr bytes.Buffer
	hdr.WriteString(bbk1Magic)
	hdr.WriteByte(version)
	binary.Write(&hdr, binary.BigEndian, uint16(len(wrappedDEK))) //nolint:errcheck // bytes.Buffer.Write never fails
	hdr.Write(wrappedDEK)

	// Pipe the header + encrypted stream to the bucket. A goroutine writes to pw;
	// the main path reads from pr and uploads. We capture the write-side error and
	// return it after bucket.Put returns.
	pr, pw := io.Pipe()
	var writeErr error
	done := make(chan struct{})
	go func() {
		defer func() {
			pw.CloseWithError(writeErr)
			close(done)
		}()
		if _, err := pw.Write(hdr.Bytes()); err != nil {
			writeErr = fmt.Errorf("backup: write header: %w", err)
			return
		}
		var streamErr error
		if version == bbk1VersionV3 {
			streamErr = libencrypt.EncryptStreamV2(pw, src, ephemeralDEK, backupObjectContext(backupID))
		} else {
			streamErr = libencrypt.EncryptStream(pw, src, ephemeralDEK)
		}
		if streamErr != nil {
			writeErr = fmt.Errorf("backup: encrypt stream: %w", streamErr)
		}
	}()

	putErr := s.bucket.Put(ctx, bucketKey(backupID), pr)
	// Drain and close the pipe so the goroutine can exit cleanly even if Put failed.
	pr.CloseWithError(putErr)
	<-done

	if writeErr != nil {
		return writeErr
	}
	return putErr
}

// Restore fetches the ciphertext for backupID, unwraps the ephemeral DEK using
// key, and decrypts the stream to dst.
//
// Restore requires only the BackupKey and the ciphertext — no Manager, no
// SecretStore, no library ID, no Beamfall server, account, or subscription alive
// (ADR-0115 §4: "a user with the ciphertext + the Recovery Key can restore with
// no Beamfall server, account, subscription, or Hosted Store alive").
func (s *Service) Restore(ctx context.Context, key BackupKey, backupID string, dst io.Writer) error {
	if backupID == "" {
		return errors.New("backup: backupID is required")
	}

	rc, err := s.bucket.Get(ctx, bucketKey(backupID))
	if err != nil {
		return fmt.Errorf("backup: get ciphertext: %w", err)
	}
	defer rc.Close()

	_, err = inspectEnvelope(key, backupID, rc, dst)
	return err
}

// InspectEnvelope decrypts one raw BBK1 envelope with the supplied BackupKey
// and streams plaintext to dst. It is the package backend for the operator
// `beamfall backup inspect --key` debugging command and never logs key bytes.
// A context-bound v3 envelope needs the backup ID it was written under — use
// InspectEnvelopeWithID for those.
func InspectEnvelope(key BackupKey, src io.Reader, dst io.Writer) (EnvelopeInfo, error) {
	return inspectEnvelope(key, "", src, dst)
}

// InspectEnvelopeWithID is InspectEnvelope for context-bound v3 envelopes: the
// caller supplies the backup ID the envelope was written under (its immutable
// object identity — never derived from the file's path or name). A wrong ID
// fails before any plaintext is written.
func InspectEnvelopeWithID(key BackupKey, backupID string, src io.Reader, dst io.Writer) (EnvelopeInfo, error) {
	if backupID == "" {
		return EnvelopeInfo{}, errors.New("backup: backupID is required")
	}
	return inspectEnvelope(key, backupID, src, dst)
}

func inspectEnvelope(key BackupKey, backupID string, src io.Reader, dst io.Writer) (EnvelopeInfo, error) {
	if dst == nil {
		dst = io.Discard
	}

	fixed := make([]byte, bbk1HeaderFixed)
	if _, err := io.ReadFull(src, fixed); err != nil {
		return EnvelopeInfo{}, ErrCorrupt
	}
	if string(fixed[:4]) != bbk1Magic {
		return EnvelopeInfo{}, ErrCorrupt
	}
	version := fixed[4]
	if version != bbk1VersionV1 && version != bbk1VersionV2 && version != bbk1VersionV3 {
		return EnvelopeInfo{}, ErrCorrupt
	}
	if version == bbk1VersionV3 && backupID == "" {
		return EnvelopeInfo{}, errors.New("backup: v3 envelope requires the backup ID it was written under")
	}
	wdLen := int(binary.BigEndian.Uint16(fixed[5:7]))
	if wdLen == 0 || wdLen > wrappedDEKMax {
		return EnvelopeInfo{}, ErrCorrupt
	}
	if version == bbk1VersionV1 && wdLen > wrappedDEKV1Max {
		return EnvelopeInfo{}, ErrCorrupt
	}
	if version != bbk1VersionV1 && wdLen != wrappedDEKV2Len {
		return EnvelopeInfo{}, ErrCorrupt
	}

	// Read the wrapped DEK.
	wrappedDEK := make([]byte, wdLen)
	if _, err := io.ReadFull(src, wrappedDEK); err != nil {
		return EnvelopeInfo{}, ErrCorrupt
	}

	var (
		ephemeralDEK []byte
		info         = EnvelopeInfo{Version: int(version)}
		err          error
	)
	if version == bbk1VersionV1 {
		ephemeralDEK, err = libencrypt.AEADUnwrap(key.Bytes(), wrappedDEK)
	} else {
		ephemeralDEK, info, err = unsealDEK(key, wrappedDEK, version)
	}
	if err != nil {
		if errors.Is(err, ErrCorrupt) {
			return EnvelopeInfo{}, ErrCorrupt
		}
		return EnvelopeInfo{}, ErrWrongKey
	}
	defer libencrypt.ZeroKey(ephemeralDEK)

	// Decrypt the libencrypt at-rest stream. The authenticated envelope version
	// declares the expected at-rest format — a v3 payload must be a
	// context-bound v2 stream under this backup ID, and earlier versions must
	// be the legacy v1 stream. The payload is never sniffed.
	if version == bbk1VersionV3 {
		if err := libencrypt.DecryptStreamV2(dst, src, ephemeralDEK, backupObjectContext(backupID)); err != nil {
			return EnvelopeInfo{}, fmt.Errorf("backup: decrypt stream: %w", err)
		}
		return info, nil
	}
	if err := libencrypt.DecryptStream(dst, src, ephemeralDEK); err != nil {
		return EnvelopeInfo{}, fmt.Errorf("backup: decrypt stream: %w", err)
	}
	return info, nil
}

func sealDEK(recipient PublicWrapKey, dek []byte, version byte) ([]byte, error) {
	curve := ecdh.X25519()
	recipientPublic, err := curve.NewPublicKey(recipient.bytes[:])
	if err != nil {
		return nil, err
	}
	senderPrivate, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	senderPublic := senderPrivate.PublicKey().Bytes()
	shared, err := senderPrivate.ECDH(recipientPublic)
	if err != nil {
		return nil, err
	}
	defer zero(shared)
	key, err := sealedBoxKey(shared, recipient.bytes[:], senderPublic)
	if err != nil {
		return nil, err
	}
	defer zero(key)
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcmNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	aad := sealedBoxAAD(version, recipient.bytes[:], senderPublic)
	wrapped := make([]byte, 0, wrappedDEKV2Len)
	wrapped = append(wrapped, senderPublic...)
	wrapped = append(wrapped, nonce...)
	wrapped = aead.Seal(wrapped, nonce, dek, aad)
	return wrapped, nil
}

func unsealDEK(key BackupKey, wrapped []byte, version byte) ([]byte, EnvelopeInfo, error) {
	if len(wrapped) != wrappedDEKV2Len {
		return nil, EnvelopeInfo{}, ErrCorrupt
	}
	curve := ecdh.X25519()
	senderRaw := wrapped[:x25519KeySize]
	senderPublic, err := curve.NewPublicKey(senderRaw)
	if err != nil {
		return nil, EnvelopeInfo{}, ErrCorrupt
	}
	recipientPrivate, err := key.x25519PrivateKey()
	if err != nil {
		return nil, EnvelopeInfo{}, err
	}
	recipientRaw := recipientPrivate.PublicKey().Bytes()
	shared, err := recipientPrivate.ECDH(senderPublic)
	if err != nil {
		return nil, EnvelopeInfo{}, ErrCorrupt
	}
	defer zero(shared)
	wrapKey, err := sealedBoxKey(shared, recipientRaw, senderRaw)
	if err != nil {
		return nil, EnvelopeInfo{}, err
	}
	defer zero(wrapKey)
	aead, err := newGCM(wrapKey)
	if err != nil {
		return nil, EnvelopeInfo{}, err
	}
	nonce := wrapped[x25519KeySize : x25519KeySize+gcmNonceSize]
	ciphertext := wrapped[x25519KeySize+gcmNonceSize:]
	dek, err := aead.Open(nil, nonce, ciphertext, sealedBoxAAD(version, recipientRaw, senderRaw))
	if err != nil {
		return nil, EnvelopeInfo{}, libencrypt.ErrUnwrap
	}
	recipient, err := ParsePublicWrapKey(recipientRaw)
	if err != nil {
		zero(dek)
		return nil, EnvelopeInfo{}, err
	}
	sender, err := ParsePublicWrapKey(senderRaw)
	if err != nil {
		zero(dek)
		return nil, EnvelopeInfo{}, err
	}
	return dek, EnvelopeInfo{
		Version:                  int(version),
		RecipientKeyFingerprint:  recipient.Fingerprint(),
		EphemeralSenderPublicKey: sender,
	}, nil
}

func sealedBoxKey(shared, recipientPublic, senderPublic []byte) ([]byte, error) {
	info := make([]byte, 0, len(sealedBoxInfo)+len(recipientPublic)+len(senderPublic))
	info = append(info, sealedBoxInfo...)
	info = append(info, recipientPublic...)
	info = append(info, senderPublic...)
	return hkdf.Key(sha256.New, shared, nil, string(info), 32)
}

// sealedBoxAAD binds the envelope version into the sealed-box AAD, so flipping
// the (otherwise unauthenticated) header version byte — e.g. replaying a
// context-bound v3 envelope as a context-free v2 — fails DEK unseal.
func sealedBoxAAD(version byte, recipientPublic, senderPublic []byte) []byte {
	aad := make([]byte, 0, len(bbk1Magic)+1+len(recipientPublic)+len(senderPublic))
	aad = append(aad, bbk1Magic...)
	aad = append(aad, version)
	aad = append(aad, recipientPublic...)
	aad = append(aad, senderPublic...)
	return aad
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
