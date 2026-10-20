package offlinepack

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
)

type authorityTestScoper struct {
	err error
}

func (s authorityTestScoper) ScopeFromRequest(context.Context, *http.Request) (auth.Scope, error) {
	return auth.Scope{}, s.err
}

func TestAuthorityEndpointServesAuthenticatedSigningTrust(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	now := time.Date(2026, time.July, 29, 20, 0, 0, 0, time.UTC)
	authority, err := NewAuthority("spki-sha256(test)", "offline-pack-test", privateKey, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandlers(NewService(nil, authority, func() time.Time { return now }), authorityTestScoper{})
	rec := httptest.NewRecorder()

	handler.Authority(rec, httptest.NewRequest(http.MethodGet, "/api/pack/authority", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	var document AuthorityDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	wantKey := base64.RawURLEncoding.EncodeToString(publicKey)
	if document.V != 1 || document.Issuer != "spki-sha256(test)" || document.ServerTime != now.Unix() {
		t.Fatalf("document = %+v", document)
	}
	if len(document.Keys) != 1 || document.Keys["offline-pack-test"] != wantKey {
		t.Fatalf("keys = %#v, want raw Ed25519 public key", document.Keys)
	}
}

func TestAuthorityEndpointRequiresAuthenticatedSession(t *testing.T) {
	handler := NewHTTPHandlers(NewService(nil, nil, time.Now), authorityTestScoper{err: errors.New("denied")})
	rec := httptest.NewRecorder()

	handler.Authority(rec, httptest.NewRequest(http.MethodGet, "/api/pack/authority", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if rec.Body.String() != "invalid session\n" {
		t.Fatalf("body = %q, want generic auth failure", rec.Body.String())
	}
}
