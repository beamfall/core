package store

import (
	"context"

	"golang.org/x/crypto/argon2"
)

// CredentialDerivation describes a completed Argon2id derivation without
// exposing the credential, salt, or derived key.
type CredentialDerivation struct {
	SaltBytes int
	Passes    uint32
	MemoryKiB uint32
	Lanes     uint8
	KeyBytes  uint32
}

type credentialDerivationObserverKey struct{}

// WithCredentialDerivationObserver lets tests observe the real KDF on a request.
func WithCredentialDerivationObserver(ctx context.Context, observe func(CredentialDerivation)) context.Context {
	return context.WithValue(ctx, credentialDerivationObserverKey{}, observe)
}

func deriveCredentialKey(ctx context.Context, credential string, salt []byte, passes, memory uint32, lanes uint8, keyLen uint32) []byte {
	key := argon2.IDKey([]byte(credential), salt, passes, memory, lanes, keyLen)
	if observe, ok := ctx.Value(credentialDerivationObserverKey{}).(func(CredentialDerivation)); ok {
		observe(CredentialDerivation{SaltBytes: len(salt), Passes: passes, MemoryKiB: memory, Lanes: lanes, KeyBytes: keyLen})
	}
	return key
}
