package compat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func compatRequest(t *testing.T, method, path string) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, Prefix+path, nil)
	req.RemoteAddr = "127.0.0.1:1234"
	return req
}

func sendCompat(handler *Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func newIdentityHandler(t *testing.T, cfg Config, service *auth.Service) *Handler {
	t.Helper()
	cfg.AllowedCIDRs = []string{"127.0.0.0/8"}
	handler, err := New(cfg, service, nil)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func integrationService(t *testing.T, clock func() time.Time) (*store.Store, *auth.Service) {
	t.Helper()
	st := storetest.Open(t)
	enabled := true
	if _, err := st.UpdateProfileSettings(t.Context(), "profile-default", store.ProfileSettings{IntegrationSignIn: &enabled}); err != nil {
		t.Fatal(err)
	}
	return st, auth.NewServiceWithClock(st, clock)
}

func loginIntegration(t *testing.T, handler *Handler) string {
	t.Helper()
	req := compatRequest(t, http.MethodPost, "Users/AuthenticateByName")
	req.Body = io.NopCloser(strings.NewReader(`{"Username":"Default","Pw":"2468"}`))
	rec := sendCompat(handler, req)
	var body struct {
		AccessToken string
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.AccessToken == "" {
		t.Fatalf("login returned %d or no valid bearer", rec.Code)
	}
	return body.AccessToken
}

func TestJellyfinSystemInfo(t *testing.T) {
	const version = "v1.2.3"
	st, service := integrationService(t, time.Now)
	handler := newIdentityHandler(t, Config{
		Enabled: true, ServerID: "stable-id", ServerName: "Home Server", ReleaseVersion: version,
		SetupRequired: func() bool { return false },
	}, service)
	token := loginIntegration(t, handler)
	if err := st.SetSessionRevealed(t.Context(), store.SessionID(token), true); err != nil {
		t.Fatal(err)
	}
	restricted := restrictedFixture(t, st)
	check := func(rec *httptest.ResponseRecorder, authenticated bool) {
		t.Helper()
		restricted.assertAbsent(t, rec.Body.Bytes())
		var body map[string]any
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
			t.Fatalf("system info: %d %s", rec.Code, rec.Body.String())
		}
		want := map[string]any{
			"Id": "stable-id", "ProductName": "Beamfall", "Version": version,
			"ServerName": "Home Server", "OperatingSystem": runtime.GOOS, "StartupWizardCompleted": true,
		}
		if authenticated {
			want["SystemArchitecture"] = runtime.GOARCH
			want["CanSelfRestart"] = false
		}
		if !reflect.DeepEqual(body, want) {
			t.Fatalf("system info = %v, want %v", body, want)
		}
		if strings.Contains(strings.ToLower(rec.Body.String()), "jellyfin") {
			t.Fatal("system info exposes foreign product identity")
		}
	}
	check(sendCompat(handler, compatRequest(t, http.MethodGet, "System/Info/Public")), false)
	for _, tc := range []struct{ name, header, value string }{
		{"token header", "X-Emby-Token", token},
		{"legacy authorization", "X-Emby-Authorization", `MediaBrowser Client="Test, Inc.", Token="` + token + `", Version="1"`},
		{"standard authorization", "Authorization", `mEdIaBrOwSeR token = "` + token + `", Device="Desktop"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := compatRequest(t, http.MethodGet, "System/Info")
			req.Header.Set(tc.header, tc.value)
			check(sendCompat(handler, req), true)
		})
	}
}

func TestJellyfinAuthenticationFailuresAreUniform(t *testing.T) {
	now := time.Now()
	_, service := integrationService(t, func() time.Time { return now })
	handler := newIdentityHandler(t, Config{Enabled: true}, service)
	expired := loginIntegration(t, handler)
	now = now.Add(365 * 24 * time.Hour)
	revoked := loginIntegration(t, handler)
	logout := compatRequest(t, http.MethodPost, "Sessions/Logout")
	logout.Header.Set("Authorization", `MediaBrowser Token="`+revoked+`"`)
	if rec := sendCompat(handler, logout); rec.Code != http.StatusNoContent {
		t.Fatalf("logout returned %d", rec.Code)
	}
	valid := loginIntegration(t, handler)
	for _, route := range []struct {
		jellyfinRoute
		otherScope store.IntegrationScope
	}{
		{jellyfinRoute{http.MethodGet, "System/Info"}, store.IntegrationScopeRefresh},
		{jellyfinRoute{http.MethodPost, "Library/Refresh"}, store.IntegrationScopeRead},
	} {
		t.Run(route.path, func(t *testing.T) {
			scoped, err := service.AuthenticateIntegrationByName(t.Context(), "Default", "2468", "192.0.2.10", []store.IntegrationScope{route.otherScope})
			if err != nil {
				t.Fatal(err)
			}
			want := sendCompat(handler, compatRequest(t, route.method, route.path))
			if want.Code != http.StatusUnauthorized || want.Body.String() != "{\"error\":\"authentication failed\"}\n" {
				t.Fatalf("missing token response = %d %q", want.Code, want.Body)
			}
			for _, tc := range []struct{ name, header, value, query string }{
				{"unknown", "X-Emby-Token", strings.Repeat("0", 64), ""},
				{"expired", "X-Emby-Token", expired, ""},
				{"revoked", "X-Emby-Token", revoked, ""},
				{"wrong scope", "X-Emby-Token", scoped.Token, ""},
				{"query only", "", "", "?api_key=" + url.QueryEscape(valid)},
				{"wrong scheme", "Authorization", "Bearer " + valid, ""},
				{"unquoted", "Authorization", "MediaBrowser Token=" + valid, ""},
				{"duplicate parameter", "Authorization", `MediaBrowser Token="` + valid + `", Token="other"`, ""},
				{"incomplete parameter list", "Authorization", `MediaBrowser Token="` + valid + `",`, ""},
				{"token inside metadata", "Authorization", `MediaBrowser Device="fake, Token=\"` + valid + `\""`, ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					req := compatRequest(t, route.method, route.path+tc.query)
					if tc.header != "" {
						req.Header.Set(tc.header, tc.value)
					}
					rec := sendCompat(handler, req)
					if rec.Code != want.Code || rec.Body.String() != want.Body.String() || !reflect.DeepEqual(rec.Header(), want.Header()) {
						t.Fatalf("response differs from the missing token response (status %d)", rec.Code)
					}
				})
			}
		})
	}
}

func TestJellyfinLogoutRevokesRefreshOnlySession(t *testing.T) {
	_, service := integrationService(t, time.Now)
	session, err := service.AuthenticateIntegrationByName(t.Context(), "Default", "2468", "127.0.0.1", []store.IntegrationScope{store.IntegrationScopeRefresh})
	if err != nil {
		t.Fatal(err)
	}
	handler := newIdentityHandler(t, Config{Enabled: true}, service)
	for _, tc := range []struct {
		path string
		want int
	}{
		{"Sessions/Logout", http.StatusNoContent},
		{"Library/Refresh", http.StatusUnauthorized},
	} {
		req := compatRequest(t, http.MethodPost, tc.path)
		req.Header.Set("X-Emby-Token", session.Token)
		if rec := sendCompat(handler, req); rec.Code != tc.want {
			t.Fatalf("%s returned %d, want %d", tc.path, rec.Code, tc.want)
		}
	}
}

func TestJellyfinRefreshBurst(t *testing.T) {
	_, service := integrationService(t, time.Now)
	var enqueues atomic.Int32
	handler := newIdentityHandler(t, Config{
		Enabled: true, EnqueueRefresh: func(_ context.Context, _ auth.Scope, _ []string) { enqueues.Add(1) },
	}, service)
	tokens := []string{loginIntegration(t, handler), loginIntegration(t, handler)}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 24 {
		req := compatRequest(t, http.MethodPost, "Library/Refresh")
		req.Header.Set("X-Emby-Token", tokens[i%len(tokens)])
		wg.Go(func() {
			<-start
			rec := sendCompat(handler, req)
			if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
				t.Errorf("refresh returned %d with %d body bytes", rec.Code, rec.Body.Len())
			}
		})
	}
	close(start)
	wg.Wait()
	if got := enqueues.Load(); got != 1 {
		t.Fatalf("refresh enqueues = %d, want 1", got)
	}
}

func TestJellyfinRefreshBoundary(t *testing.T) {
	_, service := integrationService(t, time.Now)
	login := newIdentityHandler(t, Config{Enabled: true}, service)
	token := loginIntegration(t, login)
	for _, tc := range []struct {
		name, addr       string
		enabled, relayed bool
		want             int
	}{
		{"toggle off", "127.0.0.1:1234", false, false, http.StatusNotFound},
		{"outside trust zone", "203.0.113.9:1234", true, false, http.StatusForbidden},
		{"relay", "127.0.0.1:1234", true, true, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := newIdentityHandler(t, Config{
				Enabled: tc.enabled,
				EnqueueRefresh: func(context.Context, auth.Scope, []string) {
					t.Error("refresh crossed the request boundary")
				},
			}, service)
			handler.SetRelayedRequestPredicate(func(*http.Request) bool { return tc.relayed })
			req := compatRequest(t, http.MethodPost, "Library/Refresh")
			req.RemoteAddr = tc.addr
			req.Header.Set("X-Emby-Token", token)
			req.Header.Set("X-Forwarded-For", "127.0.0.1")
			if rec := sendCompat(handler, req); rec.Code != tc.want {
				t.Fatalf("refresh returned %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
