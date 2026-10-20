// Package libencrypt is the per-Encrypted-Library key model:
// DEK / KEK / passphrase-KDF / Recovery Key, plus the Sealed/Unsealed state and
// the provision + unseal operations. It is domain-agnostic crypto infrastructure
// (invariant #1) — it knows nothing about photos, Media, or Libraries beyond an
// opaque library-id string used to key artifacts.
//
// What this package does:
//   - Generate a random 256-bit per-library Data Encryption Key (DEK).
//   - Wrap the DEK three independent ways so any one path can unwrap it:
//     1. KEK     — a random 256-bit key-encryption key held in the secure store;
//     the auto-unseal path (read the KEK at boot, unwrap the DEK).
//     2. passphrase — a key derived from an operator passphrase via argon2id;
//     the passphrase-unseal path (Sealed at boot until the operator supplies it).
//     3. Recovery Key — a high-entropy, user-held, License-Key-shaped credential
//     that independently unwraps the DEK with no server/account alive.
//   - Hold unsealed DEKs in memory, keyed by library id (the Sealed/Unsealed state).
//
// What this package deliberately does NOT do: it never encrypts any actual
// bytes, derivatives, or metadata DB. That at-rest cipher is ENC-3; the DEK
// produced here is its input. The DEK consumer does not exist yet.
//
// Invariant #5 (the central rule here): no key, passphrase, Recovery Key, or DEK
// is ever written in the clear, logged, put in telemetry, or exported in
// My-Data. Only WRAPPED blobs and non-secret KDF parameters leave this package
// at rest, persisted through the secure-store seam (0600 files). The KEK itself
// is a secret and lives in its own secure-store entry; everything in KeyBundle
// is useless without one of the three keys.
package libencrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

// dekLen is the DEK and KEK length in bytes: a 256-bit key for AES-256-GCM
// (ADR-0045 §2.4 — per-library 256-bit data key).
const dekLen = 32

// gcmNonceLen is the AES-GCM nonce length (12 bytes, the GCM standard). A fresh
// random nonce is generated per wrap and prepended to the ciphertext; the same
// (key, nonce) pair is never deliberately reused because every wrap re-randomizes
// the nonce and the key inputs are themselves high-entropy.
const gcmNonceLen = 12

// argon2Params are the memory-hard KDF parameters for the passphrase-unseal path
// (ADR-0045 §2.4 — "a memory-hard KDF"). These are the PRODUCTION defaults;
// tests override them with TestArgon2Params for speed. argon2id is the recommended
// hybrid mode (resists both side-channel and GPU/TMTO attacks).
//
// time=3, memory=64 MiB, threads=4 follows the OWASP argon2id baseline for
// interactive secret derivation. KeyLen=32 yields a 256-bit AES key. The salt is
// 16 random bytes, generated per library at provisioning and stored alongside the
// passphrase-wrapped blob (a salt is non-secret).
type Argon2Params struct {
	Time    uint32
	Memory  uint32 // KiB
	Threads uint8
	KeyLen  uint32
}

// ProdArgon2Params is the production memory-hard KDF profile (64 MiB, 3 passes).
var ProdArgon2Params = Argon2Params{Time: 3, Memory: 64 * 1024, Threads: 4, KeyLen: dekLen}

// TestArgon2Params is a deliberately weak profile for fast tests ONLY — never
// use it to wrap a real DEK. Documented as test-only so a reviewer can see the
// production params are the ones that ship.
var TestArgon2Params = Argon2Params{Time: 1, Memory: 8, Threads: 1, KeyLen: dekLen}

// saltLen is the argon2id salt length in bytes.
const saltLen = 16

// ErrUnwrap is returned when a wrapped DEK fails to decrypt: a wrong passphrase,
// a wrong Recovery Key, a wrong/rotated KEK, or tampered ciphertext. The error is
// deliberately generic — it never reveals which input was wrong and never echoes
// any key material (invariant #5).
var ErrUnwrap = errors.New("libencrypt: unwrap failed (wrong key/passphrase or corrupt data)")

// newDEK generates a fresh random 256-bit DEK.
func newDEK() ([]byte, error) {
	return randomBytes(dekLen)
}

// newKEK generates a fresh random 256-bit KEK.
func newKEK() ([]byte, error) {
	return randomBytes(dekLen)
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, fmt.Errorf("libencrypt: random generation failed: %w", err)
	}
	return b, nil
}

// NewRandomKey generates a fresh random 256-bit key, suitable for use as an
// ephemeral DEK by packages outside libencrypt (e.g. the backup package).
// The returned slice is the caller's responsibility to zero after use via ZeroKey.
func NewRandomKey() ([]byte, error) { return newDEK() }

// ZeroKey overwrites a key buffer. Best-effort hygiene — callers should defer
// this after obtaining a key to shrink the window during which a memory scrape
// could observe it (same pattern as Manager.Seal / the internal zero helper).
func ZeroKey(b []byte) { zero(b) }

// AEADWrap is the exported form of aeadWrap, used by packages outside libencrypt
// (e.g. the backup package) that need to wrap a 32-byte secret under a key using
// the same nonce||ciphertext||tag convention. The key must be exactly 32 bytes.
// The wrapped blob is safe to store; it reveals nothing without the key.
func AEADWrap(key, plaintext []byte) ([]byte, error) { return aeadWrap(key, plaintext) }

// AEADUnwrap is the exported form of aeadUnwrap. It reverses AEADWrap: wrong key,
// tampered blob, or truncated input all collapse to ErrUnwrap (invariant #5).
func AEADUnwrap(key, blob []byte) ([]byte, error) { return aeadUnwrap(key, blob) }

// aeadWrap encrypts plaintext under a 32-byte AES-256 key with AES-GCM, returning
// nonce||ciphertext. key must be exactly 32 bytes. The plaintext here is always a
// DEK; the result is a wrapped DEK blob safe to persist.
func aeadWrap(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce, err := randomBytes(gcmNonceLen)
	if err != nil {
		return nil, err
	}
	// Seal appends ciphertext+tag to nonce, giving nonce||ciphertext||tag.
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// aeadUnwrap reverses aeadWrap: it splits nonce||ciphertext and decrypts. Any
// authentication failure (wrong key, tamper) collapses to ErrUnwrap — the caller
// learns only that unwrap failed, never why, and never sees key material.
func aeadUnwrap(key, blob []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(blob) < gcmNonceLen {
		return nil, ErrUnwrap
	}
	nonce, ciphertext := blob[:gcmNonceLen], blob[gcmNonceLen:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrUnwrap
	}
	return plaintext, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != dekLen {
		// A programming error (not a secret-bearing condition): the key length is
		// fixed at 256 bits everywhere it is generated/derived.
		return nil, fmt.Errorf("libencrypt: key must be %d bytes, got %d", dekLen, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// derivePassphraseKey derives a 256-bit AES key from a passphrase + salt via
// argon2id with the given params. The passphrase and the derived key are
// transient: callers must not persist either (the derived key is held in memory
// only and re-derived on each unseal — ADR-0045 §2.4 passphrase-unseal).
func derivePassphraseKey(passphrase string, salt []byte, p Argon2Params) []byte {
	return argon2.IDKey([]byte(passphrase), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
}

// recoveryKeyAESKey turns decoded Recovery-Key bytes into the AES-256 key used to
// wrap/unwrap the DEK. The Recovery Key is itself 256 bits of entropy
// (License-Key-shaped, §2.4), so it is used directly — no KDF stretch is needed
// for a full-entropy key, and adding one would only obscure that the Recovery Key
// alone unwraps the DEK with no server-held secret.
func recoveryKeyAESKey(decoded []byte) ([]byte, error) {
	if len(decoded) != dekLen {
		return nil, ErrUnwrap
	}
	return decoded, nil
}

// constantTimeEqual compares two byte slices without leaking length-dependent or
// content-dependent timing. Used where a non-AEAD equality check is needed.
func constantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
