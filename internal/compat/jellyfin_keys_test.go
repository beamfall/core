package compat

import (
	"encoding/json"
	"io"
	"net/http"
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
		admin.User.ServerName != "Home Server" || admin.User.ID == "" || admin.User.Name == "" {
		t.Fatalf("admin sign-in = %+v", admin)
	}
	profile := signIn(t, handler, "Default", "2468")
	if profile.User.Policy.IsAdministrator || profile.User.Name != "Default" || profile.User.ServerID != "stable-id" {
		t.Fatalf("profile sign-in = %+v", profile)
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

	for _, token := range []string{profile, "", "not-a-token"} {
		for _, method := range []string{http.MethodPost, http.MethodGet} {
			if rec := sendCompat(handler, seerrRequest(t, method, "Auth/Keys", token)); rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s Auth/Keys with a non-setup credential = %d, want 401", method, rec.Code)
			}
		}
	}

	// The listed key is the setup session itself: it dies with the setup TTL.
	clock.Advance(15*time.Minute + time.Second)
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
