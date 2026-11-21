package compat

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// assertLikeMissingCredential checks that route answers token exactly as it
// answers a request with no credential: the uniform 401.
func assertLikeMissingCredential(t *testing.T, handler *Handler, method, path, name, token string) {
	t.Helper()
	want := sendCompat(handler, compatRequest(t, method, path))
	got := sendCompat(handler, seerrRequest(t, method, path, token))
	if want.Code != http.StatusUnauthorized || got.Code != want.Code || got.Body.String() != want.Body.String() ||
		!reflect.DeepEqual(got.Header(), want.Header()) {
		t.Fatalf("%s %s with the %s credential = %d %q, want the missing-token response %d %q",
			method, path, name, got.Code, got.Body, want.Code, want.Body)
	}
}

// TestJellyfinIntegrationKeyOutlivesSessionsUntilRevoked covers CORE-2725
// (ADR-0171 §2.4): a minted key keeps reading and refreshing long past every
// Session TTL, cannot reach the setup-only key route or log itself out, and
// fails with the uniform 401 the moment it is revoked.
func TestJellyfinIntegrationKeyOutlivesSessionsUntilRevoked(t *testing.T) {
	clock := &compatClock{now: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}
	st, service := integrationService(t, clock.Now)
	setAdminSetup(t, st, clock.Now())
	handler := newIdentityHandler(t, Config{Enabled: true, ServerID: "stable-id"}, service)
	key, token, err := st.CreateCompatIntegrationKey(t.Context(), "profile-default", "Seerr",
		[]store.IntegrationScope{store.IntegrationScopeRead, store.IntegrationScopeRefresh}, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	session := signIn(t, handler, "Default", "2468").AccessToken

	// Seerr's Auth/Keys step is a no-op for durable credentials: the bounded
	// setup Session cannot mint a key from the admin password.
	setup := signIn(t, handler, "admin", "setup-secret").AccessToken
	if rec := sendCompat(handler, seerrRequest(t, http.MethodPost, "Auth/Keys?App=Seerr", setup)); rec.Code != http.StatusNoContent {
		t.Fatalf("setup POST Auth/Keys = %d", rec.Code)
	}
	if keys, err := st.CompatIntegrationKeys(t.Context()); err != nil || len(keys) != 1 || keys[0].ID != key.ID {
		t.Fatalf("keys after setup POST Auth/Keys = %+v, %v; want only the Console-minted key", keys, err)
	}

	use := func(when string) {
		t.Helper()
		if rec := sendCompat(handler, seerrRequest(t, http.MethodGet, "System/Info", token)); rec.Code != http.StatusOK {
			t.Fatalf("System/Info with the key %s = %d", when, rec.Code)
		}
		req := compatRequest(t, http.MethodGet, "System/Info")
		req.Header.Set("X-Emby-Token", token)
		if rec := sendCompat(handler, req); rec.Code != http.StatusOK {
			t.Fatalf("System/Info with the key in X-Emby-Token %s = %d", when, rec.Code)
		}
		if rec := sendCompat(handler, seerrRequest(t, http.MethodPost, "Library/Refresh", token)); rec.Code != http.StatusNoContent {
			t.Fatalf("Library/Refresh with the key %s = %d", when, rec.Code)
		}
	}
	use("at mint")
	assertLikeMissingToken(t, handler, "integration key", token)
	assertLikeMissingCredential(t, handler, http.MethodPost, "Sessions/Logout", "integration key", token)

	clock.Advance(25 * time.Hour)
	assertLikeMissingCredential(t, handler, http.MethodGet, "System/Info", "expired session", session)
	use("after 25 hours")
	if got, err := st.CompatIntegrationKeys(t.Context()); err != nil || !got[0].LastUsedAt.Equal(clock.Now()) {
		t.Fatalf("last use after 25 hours = %+v, %v", got, err)
	}

	// The key is never a native credential.
	if _, err := service.Scope(t.Context(), store.SessionID(token)); err == nil {
		t.Fatal("native auth accepted an integration key")
	}
	// Relayed requests never reach authentication.
	handler.SetRelayedRequestPredicate(func(*http.Request) bool { return true })
	if rec := sendCompat(handler, seerrRequest(t, http.MethodGet, "System/Info", token)); rec.Code != http.StatusNotFound {
		t.Fatalf("relayed System/Info with the key = %d, want 404", rec.Code)
	}
	handler.SetRelayedRequestPredicate(func(*http.Request) bool { return false })

	if _, err := st.RevokeCompatIntegrationKey(t.Context(), key.ID, clock.Now()); err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "System/Info"}, {http.MethodPost, "Library/Refresh"}, {http.MethodGet, "Auth/Keys"},
	} {
		assertLikeMissingCredential(t, handler, route.method, route.path, "revoked key", token)
	}
}

// TestJellyfinIntegrationKeyDiesWithTheProfileOptOut covers CORE-2725: opting
// the Profile out revokes its keys, and opting back in does not revive them.
func TestJellyfinIntegrationKeyDiesWithTheProfileOptOut(t *testing.T) {
	st, service := integrationService(t, time.Now)
	handler := newIdentityHandler(t, Config{Enabled: true, ServerID: "stable-id"}, service)
	_, token, err := st.CreateCompatIntegrationKey(t.Context(), "profile-default", "Seerr",
		[]store.IntegrationScope{store.IntegrationScopeRead}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rec := sendCompat(handler, seerrRequest(t, http.MethodGet, "System/Info", token)); rec.Code != http.StatusOK {
		t.Fatalf("System/Info with the key = %d", rec.Code)
	}
	assertLikeMissingCredential(t, handler, http.MethodPost, "Library/Refresh", "read-only key", token)
	for _, enabled := range []bool{false, true} {
		if _, err := st.SetProfileIntegrationSignIn(t.Context(), "profile-default", enabled); err != nil {
			t.Fatal(err)
		}
	}
	assertLikeMissingCredential(t, handler, http.MethodGet, "System/Info", "opted-out key", token)
}
