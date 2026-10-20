package backup

import (
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/beamfall/core/internal/libencrypt"
)

const (
	publicWrapKeySize     = 32
	publicWrapKeyFileName = "backup-wrap-public.key"
)

var wrapKeyDerivationInfo = []byte("beamfall backup x25519 recipient v2")

const acknowledgedBackupKeyDerivationInfo = "beamfall backup escrow acknowledgement replacement v1"

// PublicWrapKey is the non-secret X25519 recipient key used to seal backup
// DEKs. It is the only wrapping key material a running box needs to retain.
type PublicWrapKey struct {
	bytes [publicWrapKeySize]byte
}

// ParsePublicWrapKey validates a raw X25519 public wrap key.
func ParsePublicWrapKey(raw []byte) (PublicWrapKey, error) {
	if len(raw) != publicWrapKeySize {
		return PublicWrapKey{}, fmt.Errorf("backup: public wrap key length %d, want %d", len(raw), publicWrapKeySize)
	}
	if _, err := ecdh.X25519().NewPublicKey(raw); err != nil {
		return PublicWrapKey{}, fmt.Errorf("backup: invalid public wrap key: %w", err)
	}
	var key PublicWrapKey
	copy(key.bytes[:], raw)
	return key, nil
}

// Bytes returns a copy of the raw public key bytes.
func (key PublicWrapKey) Bytes() []byte {
	return append([]byte(nil), key.bytes[:]...)
}

// String returns the public key in unpadded base64url form.
func (key PublicWrapKey) String() string {
	return base64.RawURLEncoding.EncodeToString(key.bytes[:])
}

// Fingerprint returns a short stable identifier suitable for archive labels.
func (key PublicWrapKey) Fingerprint() string {
	digest := sha256.Sum256(key.bytes[:])
	return base64.RawURLEncoding.EncodeToString(digest[:12])
}

// BackupKey is the user-held Recovery-Key-shaped credential for zero-knowledge
// backup. It is minted locally and normally shown once. The only
// server-side secret persistence permitted is the short-lived bootstrap escrow
// managed by BootstrapEscrow.
// A user with the ciphertext and this key can restore with no server, account,
// subscription, or Beamfall service alive.
//
// BackupKey wraps libencrypt.RecoveryKey: the same 256-bit Crockford-base32
// grouped, checksummed credential shape used for Encrypted-Library recovery.
// The type alias makes the backup API self-documenting.
type BackupKey struct {
	rk libencrypt.RecoveryKey
}

// MintBackupKey generates a fresh random 256-bit BackupKey. The Display string
// is shown once at provisioning and must be saved by the operator — it is not
// stored server-side in recoverable form ("you must save this" gate, ADR-0115 §4).
func MintBackupKey() (BackupKey, error) {
	rk, err := libencrypt.MintRecoveryKey()
	if err != nil {
		return BackupKey{}, err
	}
	return BackupKey{rk: rk}, nil
}

func deriveAcknowledgedBackupKey(bootstrap BackupKey, acknowledgementID string) (BackupKey, error) {
	secret := bootstrap.Bytes()
	if len(secret) != 32 || acknowledgementID == "" {
		zero(secret)
		return BackupKey{}, libencrypt.ErrUnwrap
	}
	defer zero(secret)
	derived, err := hkdf.Key(
		sha256.New,
		secret,
		[]byte(acknowledgementID),
		acknowledgedBackupKeyDerivationInfo,
		32,
	)
	if err != nil {
		return BackupKey{}, fmt.Errorf("backup: derive acknowledged key: %w", err)
	}
	defer zero(derived)
	rk, err := libencrypt.RecoveryKeyFromBytes(derived)
	if err != nil {
		return BackupKey{}, err
	}
	return BackupKey{rk: rk}, nil
}

// ParseBackupKey decodes a BackupKey from its display string. It is lenient on
// input: case-insensitive, ignores hyphens/spaces, maps Crockford look-alikes.
// A wrong or truncated key fails with libencrypt.ErrUnwrap (same as a wrong
// Recovery Key — no information about why the parse failed, invariant #5).
func ParseBackupKey(s string) (BackupKey, error) {
	rk, err := libencrypt.ParseRecoveryKey(s)
	if err != nil {
		return BackupKey{}, err
	}
	return BackupKey{rk: rk}, nil
}

// Display returns the grouped, checksummed, human-readable display string (e.g.
// "ABCDE-FGHJK-…-CKSM"). This is what the operator saves at provisioning time.
func (bk BackupKey) Display() string { return bk.rk.Display }

// Bytes returns a copy of the raw 32-byte secret. Callers must not persist or
// log it and may zero the returned copy after use.
func (bk BackupKey) Bytes() []byte { return append([]byte(nil), bk.rk.Bytes()...) }

// PublicWrapKey deterministically derives the X25519 public recipient key from
// the user-held BackupKey. The private key exists only in this call.
func (bk BackupKey) PublicWrapKey() (PublicWrapKey, error) {
	private, err := bk.x25519PrivateKey()
	if err != nil {
		return PublicWrapKey{}, err
	}
	return ParsePublicWrapKey(private.PublicKey().Bytes())
}

func (bk BackupKey) x25519PrivateKey() (*ecdh.PrivateKey, error) {
	secret := bk.Bytes()
	if len(secret) != 32 {
		return nil, libencrypt.ErrUnwrap
	}
	defer zero(secret)
	derived, err := hkdf.Key(sha256.New, secret, nil, string(wrapKeyDerivationInfo), 32)
	if err != nil {
		return nil, fmt.Errorf("backup: derive X25519 key: %w", err)
	}
	defer zero(derived)
	private, err := ecdh.X25519().NewPrivateKey(derived)
	if err != nil {
		return nil, fmt.Errorf("backup: derive X25519 key: %w", err)
	}
	return private, nil
}

// StorePublicWrapKey atomically stores only the public wrap key in dataDir.
// The directory and file are owner-only; no BackupKey or private key is
// persisted. The returned path is dataDir/backup-wrap-public.key.
func StorePublicWrapKey(dataDir string, key PublicWrapKey) (string, error) {
	if dataDir == "" {
		return "", errors.New("backup: data directory is required")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", fmt.Errorf("backup: create wrap-key directory: %w", err)
	}
	if err := os.Chmod(dataDir, 0o700); err != nil { //nolint:gosec // directories require execute permission; 0700 is owner-only
		return "", fmt.Errorf("backup: secure wrap-key directory: %w", err)
	}
	tmp, err := os.CreateTemp(dataDir, ".backup-wrap-public-*")
	if err != nil {
		return "", fmt.Errorf("backup: create public wrap key: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("backup: secure public wrap key: %w", err)
	}
	encoded := []byte(key.String() + "\n")
	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("backup: write public wrap key: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("backup: sync public wrap key: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("backup: close public wrap key: %w", err)
	}
	path := filepath.Join(dataDir, publicWrapKeyFileName)
	if err := os.Rename(tmpName, path); err != nil {
		return "", fmt.Errorf("backup: install public wrap key: %w", err)
	}
	if err := syncDirectory(dataDir); err != nil {
		return "", fmt.Errorf("backup: sync public wrap-key directory: %w", err)
	}
	return path, nil
}

// LoadPublicWrapKey loads the public-only recipient key from dataDir.
func LoadPublicWrapKey(dataDir string) (PublicWrapKey, error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, publicWrapKeyFileName)) //nolint:gosec // fixed filename beneath operator-selected data dir
	if err != nil {
		return PublicWrapKey{}, fmt.Errorf("backup: read public wrap key: %w", err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(string(bytesTrimSpace(raw)))
	if err != nil {
		return PublicWrapKey{}, fmt.Errorf("backup: decode public wrap key: %w", err)
	}
	return ParsePublicWrapKey(decoded)
}

func bytesTrimSpace(raw []byte) []byte {
	start, end := 0, len(raw)
	for start < end && (raw[start] == ' ' || raw[start] == '\n' || raw[start] == '\r' || raw[start] == '\t') {
		start++
	}
	for end > start && (raw[end-1] == ' ' || raw[end-1] == '\n' || raw[end-1] == '\r' || raw[end-1] == '\t') {
		end--
	}
	return raw[start:end]
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
