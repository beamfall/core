package playback

import (
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

func testSelectorScope() auth.Scope {
	return auth.Scope{DeviceID: "device-1", SessionID: "session-1", ProfileID: "profile-1"}
}

// TestSelectorRoundTrip: a minted selector resolves back to the exact
// (media, version, part) coordinates under the matching scope (ADR-0098 area 2).
func TestSelectorRoundTrip(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	sel := NewSelectorService([]byte("root-key"), func() time.Time { return now })
	scope := testSelectorScope()
	token := sel.Mint("media-7", scope.ProfileID, "sdr", 2, now.Add(time.Hour))
	if token == "" {
		t.Fatal("Mint returned empty selector")
	}
	src, err := sel.Resolve(token, scope)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if src.MediaID != store.MediaID("media-7") || src.VersionLabel != "sdr" || src.PartOrdinal != 2 {
		t.Fatalf("resolved %#v, want media-7/sdr#2", src)
	}
}

// TestSelectorFailsAfterExpiry: the selector fails closed once expired.
func TestSelectorFailsAfterExpiry(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	clock := func() time.Time { return now }
	sel := NewSelectorService([]byte("root-key"), clock)
	scope := testSelectorScope()
	token := sel.Mint("media-7", scope.ProfileID, "", 0, now.Add(time.Minute))
	now = now.Add(2 * time.Minute) // advance past expiry
	if _, err := sel.Resolve(token, scope); !errors.Is(err, ErrInvalidSelector) {
		t.Fatalf("Resolve error = %v, want ErrInvalidSelector", err)
	}
}

// TestSelectorFailsProfileMismatch: a selector minted for one Profile does not
// resolve under another (a relock / Profile-switch fails closed).
func TestSelectorFailsProfileMismatch(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	sel := NewSelectorService([]byte("root-key"), func() time.Time { return now })
	scope := testSelectorScope()
	token := sel.Mint("media-7", scope.ProfileID, "", 0, now.Add(time.Hour))
	other := scope
	other.ProfileID = "profile-2"
	if _, err := sel.Resolve(token, other); !errors.Is(err, ErrInvalidSelector) {
		t.Fatalf("Resolve error = %v, want ErrInvalidSelector", err)
	}
}

// TestSelectorRejectsForgedSignature: a tampered selector is rejected, and a
// selector minted under one key does not validate under another (key domain
// separation guarantees the same against the stream-token family).
func TestSelectorRejectsForgedSignature(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	clock := func() time.Time { return now }
	scope := testSelectorScope()
	token := NewSelectorService([]byte("root-key"), clock).Mint("media-7", scope.ProfileID, "", 0, now.Add(time.Hour))

	// Different key → invalid signature.
	if _, err := NewSelectorService([]byte("other-key"), clock).Resolve(token, scope); !errors.Is(err, ErrInvalidSelector) {
		t.Fatalf("different-key error = %v, want ErrInvalidSelector", err)
	}
	// Flipped payload byte → invalid.
	bad := []byte(token)
	bad[0] ^= 0xFF
	if _, err := NewSelectorService([]byte("root-key"), clock).Resolve(string(bad), scope); !errors.Is(err, ErrInvalidSelector) {
		t.Fatalf("tampered error = %v, want ErrInvalidSelector", err)
	}
}

// TestSelectorKeyDomainSeparation: the selector key is derived from the root key
// with a domain prefix, so a raw-root-key signer cannot forge a selector that the
// real verifier accepts (and vice-versa) — the cross-family substitution guard.
func TestSelectorKeyDomainSeparation(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	clock := func() time.Time { return now }
	root := []byte("shared-root-key")
	scope := testSelectorScope()
	// A "selector" forged by signing with the RAW root key (as the stream-token
	// service would) must not validate under the real selector verifier.
	rawSigner := &SelectorService{key: root, now: clock}
	forged := rawSigner.Mint("media-7", scope.ProfileID, "", 0, now.Add(time.Hour))
	if _, err := NewSelectorService(root, clock).Resolve(forged, scope); err == nil {
		t.Fatal("raw-root-key selector validated under the domain-separated verifier")
	}
}

// TestSelectorEmptyKeyMintsEmpty: a service with no key mints an empty selector
// (degenerate/test path) rather than a forgeable one.
func TestSelectorEmptyKeyMintsEmpty(t *testing.T) {
	sel := NewSelectorService(nil, nil)
	if got := sel.Mint("media-7", "profile-1", "", 0, time.Now().Add(time.Hour)); got != "" {
		t.Fatalf("Mint with no key = %q, want empty", got)
	}
}
