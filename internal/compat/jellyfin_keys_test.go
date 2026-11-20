package compat

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

type compatClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *compatClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *compatClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type signInResult struct {
	AccessToken string
	ServerID    string `json:"ServerId"`
	User        struct {
		ID         string `json:"Id"`
		Name       string
		ServerID   string `json:"ServerId"`
		ServerName string
		Policy     struct{ IsAdministrator bool }
	}
}

func signIn(t *testing.T, handler *Handler, username, password string) signInResult {
	t.Helper()
	req := compatRequest(t, http.MethodPost, "Users/AuthenticateByName")
	body, _ := json.Marshal(map[string]string{"Username": username, "Pw": password})
	req.Body = io.NopCloser(strings.NewReader(string(body)))
	rec := sendCompat(handler, req)
	var result signInResult
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &result) != nil || result.AccessToken == "" {
		t.Fatalf("sign-in as %q returned %d %s", username, rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("sign-in Cache-Control = %q, want no-store", got)
	}
	return result
}

// setAdminSetup makes the default Profile the owner and sets the admin
// password, so "admin" signs in to the bounded setup session.
func setAdminSetup(t *testing.T, st *store.Store, now time.Time) {
	t.Helper()
	if err := st.SetProfileOwner(t.Context(), "profile-default"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAdminPassword(t.Context(), "setup-secret", now); err != nil {
		t.Fatal(err)
	}
}

func seerrRequest(t *testing.T, method, path, token string) *http.Request {
	t.Helper()
	req := compatRequest(t, method, path)
	req.Header.Set("Authorization", `MediaBrowser Client="Seerr", Device="Seerr", DeviceId="BOT_seerr", Version="3.5.2", Token="`+token+`"`)
	return req
}

func TestJellyfinSignInDescribesUser(t *testing.T) {
	st, service := integrationService(t, time.Now)
	setAdminSetup(t, st, time.Now())
	handler := newIdentityHandler(t, Config{Enabled: true, ServerID: "stable-id", ServerName: "Home Server"}, service)

	admin := signIn(t, handler, "admin", "setup-secret")
	if !admin.User.Policy.IsAdministrator || admin.ServerID != "stable-id" || admin.User.ServerID != "stable-id" ||
		admin.User.ServerName != "Home Server" || admin.User.Name != "admin" || len(admin.User.ID) != 32 {
		t.Fatalf("admin sign-in = %+v", admin)
	}
	// The owner's PIN sign-in is a different account: Seerr keeps the admin it
	// creates at setup and matches later sign-ins by Id alone.
	owner := signIn(t, handler, "Default", "2468")
	if owner.User.Policy.IsAdministrator || owner.User.Name != "Default" || owner.User.ServerID != "stable-id" ||
		owner.User.ID != "profile-default" || owner.User.ID == admin.User.ID {
		t.Fatalf("owner PIN sign-in = %+v, admin Id %q", owner, admin.User.ID)
	}
	if again := signIn(t, handler, "admin", "setup-secret"); again.User.ID != admin.User.ID {
		t.Fatalf("setup account Id changed between sign-ins: %q, %q", admin.User.ID, again.User.ID)
	}
	other := newIdentityHandler(t, Config{Enabled: true, ServerID: "other-id"}, service)
	if got := signIn(t, other, "admin", "setup-secret"); got.User.ID == admin.User.ID {
		t.Fatal("setup account Id is shared across servers")
	}
}

func TestJellyfinAPIKeysEchoOnlyTheSetupToken(t *testing.T) {
	clock := &compatClock{now: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}
	st, service := integrationService(t, clock.Now)
	setAdminSetup(t, st, clock.Now())
	handler := newIdentityHandler(t, Config{Enabled: true, ServerID: "stable-id"}, service)
	setup := signIn(t, handler, "admin", "setup-secret").AccessToken
	profile := signIn(t, handler, "Default", "2468").AccessToken

	if rec := sendCompat(handler, seerrRequest(t, http.MethodPost, "Auth/Keys?App=Seerr", setup)); rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("setup POST Auth/Keys = %d %q, want empty 204", rec.Code, rec.Body)
	}
	rec := sendCompat(handler, seerrRequest(t, http.MethodGet, "Auth/Keys", setup))
	var keys struct {
		Items []struct {
			AccessToken string
			AppName     string
		}
		TotalRecordCount int
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &keys) != nil {
		t.Fatalf("setup GET Auth/Keys = %d %s", rec.Code, rec.Body)
	}
	if len(keys.Items) != 1 || keys.TotalRecordCount != 1 || keys.Items[0].AccessToken != setup || keys.Items[0].AppName != "Seerr" {
		t.Fatalf("Auth/Keys = %+v, want only the setup token named Seerr", keys)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Auth/Keys Cache-Control = %q, want no-store", got)
	}

	revoked := signIn(t, handler, "admin", "setup-secret").AccessToken
	if rec := sendCompat(handler, seerrRequest(t, http.MethodPost, "Sessions/Logout", revoked)); rec.Code != http.StatusNoContent {
		t.Fatalf("setup logout = %d", rec.Code)
	}
	for name, token := range map[string]string{"profile": profile, "invalid": "not-a-token", "revoked setup": revoked} {
		assertLikeMissingToken(t, handler, name, token)
	}

	// The listed key is the setup session itself: it dies with the setup TTL.
	clock.Advance(15*time.Minute + time.Second)
	assertLikeMissingToken(t, handler, "expired setup", keys.Items[0].AccessToken)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "Auth/Keys"}, {http.MethodGet, "System/Info"}, {http.MethodPost, "Library/Refresh"},
	} {
		if rec := sendCompat(handler, seerrRequest(t, route.method, route.path, keys.Items[0].AccessToken)); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s with the expired setup key = %d, want 401", route.method, route.path, rec.Code)
		}
	}
	if rec := sendCompat(handler, seerrRequest(t, http.MethodGet, "System/Info", profile)); rec.Code != http.StatusOK {
		t.Fatalf("profile session after the setup TTL = %d, want 200", rec.Code)
	}
}

func TestJellyfinSetupScopeRefusedForProfiles(t *testing.T) {
	_, service := integrationService(t, time.Now)
	scopes := []store.IntegrationScope{store.IntegrationScopeRead, store.IntegrationScopeSetup}
	if _, err := service.AuthenticateIntegrationByName(t.Context(), "Default", "2468", "127.0.0.1", scopes); err == nil {
		t.Fatal("a Profile session was granted the setup scope")
	}
}

// assertLikeMissingToken checks that both Auth/Keys methods answer token
// exactly as they answer a request with no credential.
func assertLikeMissingToken(t *testing.T, handler *Handler, name, token string) {
	t.Helper()
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		missing := compatRequest(t, method, "Auth/Keys")
		want := sendCompat(handler, missing)
		got := sendCompat(handler, seerrRequest(t, method, "Auth/Keys", token))
		if want.Code != http.StatusUnauthorized || got.Code != want.Code || got.Body.String() != want.Body.String() ||
			!reflect.DeepEqual(got.Header(), want.Header()) {
			t.Fatalf("%s Auth/Keys with the %s credential = %d %q, want the missing-token response %d %q",
				method, name, got.Code, got.Body, want.Code, want.Body)
		}
	}
}

func TestJellyfinAPIKeysNameTheTokenHeadersClient(t *testing.T) {
	st, service := integrationService(t, time.Now)
	setAdminSetup(t, st, time.Now())
	handler := newIdentityHandler(t, Config{Enabled: true, ServerID: "stable-id"}, service)
	setup := signIn(t, handler, "admin", "setup-secret").AccessToken

	req := compatRequest(t, http.MethodGet, "Auth/Keys")
	req.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Decoy", Device="d", DeviceId="d", Version="1"`)
	req.Header.Set("Authorization", `MediaBrowser Client="Se\"err", Device="Seerr", DeviceId="BOT_seerr", Version="3.5.2", Token="`+setup+`"`)
	rec := sendCompat(handler, req)
	var keys struct{ Items []struct{ AppName string } }
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &keys) != nil || len(keys.Items) != 1 {
		t.Fatalf("GET Auth/Keys = %d %s", rec.Code, rec.Body)
	}
	if got := keys.Items[0].AppName; got != `Se"err` {
		t.Fatalf("AppName = %q, want the decoded Client of the header that carried the token", got)
	}

	// A header carrying another token never names this one.
	req = compatRequest(t, http.MethodGet, "Auth/Keys")
	req.Header.Set("X-Emby-Authorization", `MediaBrowser Device="Seerr", DeviceId="BOT_seerr", Version="3.5.2", Token="`+setup+`"`)
	req.Header.Set("Authorization", `MediaBrowser Client="Decoy", Device="d", DeviceId="d", Version="1", Token="other"`)
	rec = sendCompat(handler, req)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &keys) != nil || len(keys.Items) != 1 || keys.Items[0].AppName != "" {
		t.Fatalf("GET Auth/Keys with a Client-less token header = %d %s, want an empty AppName", rec.Code, rec.Body)
	}

	// With the token in X-Emby-Token, a header with any Token parameter is not
	// a fallback, and "token=" inside another value does not disqualify one.
	req = compatRequest(t, http.MethodGet, "Auth/Keys")
	req.Header.Set("X-Emby-Token", setup)
	req.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Decoy", Token = "a"`)
	req.Header.Set("Authorization", `MediaBrowser Client="Seerr", Device="token=reader"`)
	rec = sendCompat(handler, req)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &keys) != nil || len(keys.Items) != 1 || keys.Items[0].AppName != "Seerr" {
		t.Fatalf("GET Auth/Keys with the token in X-Emby-Token = %d %s, want AppName Seerr", rec.Code, rec.Body)
	}
}
