package playback

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// SelectorService mints and resolves the opaque selected-file selector (ADR-0098
// area 2). The selector replaces the raw content fingerprint that playbackInfo
// previously exposed in PartInfo: it identifies the selected Version + Part of a
// Media WITHOUT embedding any path or fingerprint, so a client never holds a
// file-plane identity. The file-plane identity is re-resolved server-side from
// the (mediaID, versionLabel, partOrdinal) coordinates under the request scope's
// LibraryFilter, so Library hide, Reveal relock, device revocation, and Sealed
// Library lock all fail closed at resolution time — the selector grants nothing
// on its own.
//
// Key lineage (invariant #5; mirrors streamtoken's key contract): the selector
// HMAC key is DERIVED from the same root stream-token key with a fixed domain
// prefix so the selector and Stream Token HMAC families can never validate under
// each other's verifier, even when the deployment shares one root key. The
// selector signs a JSON payload distinct from the Stream Token grant, and the
// domain-separated key makes cross-family substitution impossible by
// construction rather than merely by payload shape.
type SelectorService struct {
	key []byte
	now func() time.Time
}

// selectorDomain is the HKDF-style domain label that separates the selector key
// from the raw stream-token key (see SelectorService doc).
const selectorDomain = "beamfall-selector-v1"

// selectorPayload is the signed coordinate set. It carries no path or
// fingerprint — only the logical Media + Version + Part coordinates plus the
// owning Profile and an expiry that fails the selector closed.
type selectorPayload struct {
	MediaID      store.MediaID   `json:"m"`
	ProfileID    store.ProfileID `json:"p"`
	VersionLabel string          `json:"v"`
	PartOrdinal  int             `json:"o"`
	ExpiresAt    time.Time       `json:"x"`
}

// NewSelectorService derives a selector service from the root stream-token key.
// now defaults to time.Now. A zero-length root key disables minting (Mint
// returns "" so degenerate/test paths carry an empty selector, never a forgeable
// one).
func NewSelectorService(rootKey []byte, now func() time.Time) *SelectorService {
	if now == nil {
		now = time.Now
	}
	var key []byte
	if len(rootKey) > 0 {
		mac := hmac.New(sha256.New, rootKey)
		mac.Write([]byte(selectorDomain))
		key = mac.Sum(nil)
	}
	return &SelectorService{key: key, now: now}
}

// Mint returns an opaque selector for the (media, version, part) the playback
// decision chose, scoped to profileID and bounded by expiresAt (which callers
// pass as the enclosing Stream Token grant's expiry so the selector and token
// share one expiry boundary). A service with no key returns "".
func (s *SelectorService) Mint(mediaID store.MediaID, profileID store.ProfileID, versionLabel string, partOrdinal int, expiresAt time.Time) string {
	if len(s.key) == 0 {
		return ""
	}
	payload, err := json.Marshal(selectorPayload{
		MediaID:      mediaID,
		ProfileID:    profileID,
		VersionLabel: versionLabel,
		PartOrdinal:  partOrdinal,
		ExpiresAt:    expiresAt,
	})
	if err != nil {
		return ""
	}
	signature := s.sign(payload)
	return encodeSel(payload) + "." + encodeSel(signature)
}

// SelectedSource is the logical, file-plane-free identity a resolved selector
// names. Callers re-load the actual MediaFile from these coordinates under the
// request scope's LibraryFilter (and sealed predicate), where hide/relock/seal
// fail closed.
type SelectedSource struct {
	MediaID      store.MediaID
	VersionLabel string
	PartOrdinal  int
}

// Resolve validates a selector's signature, expiry, and Profile binding against
// the request scope, returning the logical coordinates it names. Resolve does
// NOT perform Reveal/Sealed/visibility checks itself — that is the caller's job
// at byte-serve time via the live LibraryFilter and sealed predicate. Resolve is
// the server-side resolution seam: this slice exercises it via unit round-trip
// tests; a future selector-accepting byte route (Part-navigate / asset-by-
// selector) is its production consumer (listed as a MUSIC-2 residual).
func (s *SelectorService) Resolve(selector string, scope auth.Scope) (SelectedSource, error) {
	if len(s.key) == 0 {
		return SelectedSource{}, errors.New("selector key is required")
	}
	parts := strings.Split(selector, ".")
	if len(parts) != 2 {
		return SelectedSource{}, ErrInvalidSelector
	}
	payload, err := decodeSel(parts[0])
	if err != nil {
		return SelectedSource{}, ErrInvalidSelector
	}
	signature, err := decodeSel(parts[1])
	if err != nil {
		return SelectedSource{}, ErrInvalidSelector
	}
	if !hmac.Equal(signature, s.sign(payload)) {
		return SelectedSource{}, ErrInvalidSelector
	}
	var p selectorPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return SelectedSource{}, ErrInvalidSelector
	}
	if !p.ExpiresAt.After(s.now()) {
		return SelectedSource{}, ErrInvalidSelector
	}
	if p.ProfileID != scope.ProfileID {
		return SelectedSource{}, ErrInvalidSelector
	}
	return SelectedSource{MediaID: p.MediaID, VersionLabel: p.VersionLabel, PartOrdinal: p.PartOrdinal}, nil
}

func (s *SelectorService) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write(payload)
	return mac.Sum(nil)
}

func encodeSel(data []byte) string { return base64.RawURLEncoding.EncodeToString(data) }

func decodeSel(text string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(text) }
