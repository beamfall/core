package release

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
)

// errNoPinnedKey is the fail-closed sentinel returned when no usable trust
// anchor is pinned into the binary.
var errNoPinnedKey = errors.New("release: refused — no pinned verification key in this build")

// pinnedPublicKeyB64 is the committed trust anchor: the base64 (std) ed25519
// public key whose private seed signs official Beamfall server releases. This
// is the cosign-style pin — the upgrade path trusts a manifest ONLY if it
// verifies against this key.
//
// The matching private seed is a release secret (BEAMFALL_RELEASE_SIGNING_KEY),
// held only by the release pipeline and NEVER committed. Rotating it is a
// coordinated act: publish a new binary embedding the new public key, then sign
// future releases with the new seed. Old binaries fail-closed against new-key
// signatures until upgraded (ADR-0013 Amendment §A) — acceptable because the
// version check is opt-in and only surfaces a banner, never auto-applies.
//
// An empty pin (e.g. a community fork that has not set its own key) means
// PinnedKey returns ok=false and every Verify fails closed: no update is ever
// blessed. That is the safe default.
const pinnedPublicKeyB64 = "1qIFUr8HcMce4+Gi1OD9yCf8TsLsM+FWb4pd/BdEA7I="

// PinnedKey returns the pinned ed25519 trust-anchor public key. ok is false when
// no key is pinned or the pin is malformed — in which case the caller must
// treat verification as failed (fail-closed).
func PinnedKey() (ed25519.PublicKey, bool) {
	return pinnedKey(pinnedPublicKeyB64)
}

func pinnedKey(encoded string) (ed25519.PublicKey, bool) {
	raw := strings.TrimSpace(encoded)
	if raw == "" {
		return nil, false
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, false
	}
	return ed25519.PublicKey(b), true
}

// VerifyWithPinnedKey verifies m against the pinned trust anchor. It refuses
// (non-nil error) when no key is pinned, the manifest is unsigned, or the
// signature does not verify — the fail-closed gate the upgrade path calls.
func VerifyWithPinnedKey(m Manifest) error {
	pub, ok := PinnedKey()
	if !ok {
		return errNoPinnedKey
	}
	return Verify(pub, m)
}
