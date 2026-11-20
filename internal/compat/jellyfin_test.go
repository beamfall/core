package compat

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestJellyfinRouteInventory(t *testing.T) {
	want := []jellyfinRoute{
		{http.MethodPost, Prefix + "Users/AuthenticateByName"},
		{http.MethodPost, Prefix + "Sessions/Logout"},
	}
	if len(jellyfinRoutes) != len(want) {
		t.Errorf("registered %d compat routes, want %d", len(jellyfinRoutes), len(want))
	}
	for route, serve := range jellyfinRoutes {
		if !slices.Contains(want, route) || serve == nil {
			t.Errorf("unexpected compat route %s %s", route.method, route.path)
		}
	}
}

func TestJellyfinUnregisteredRoutesFallThrough(t *testing.T) {
	const fallbackBody = "handled by fall-through"
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Fall-Through", "yes")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(fallbackBody))
	})
	handler, err := New(Config{Enabled: true, AllowedCIDRs: []string{"127.0.0.0/8"}}, nil, next)
	if err != nil {
		t.Fatal(err)
	}
	tests := []jellyfinRoute{
		{http.MethodGet, "Videos/item-id/stream"},
		{http.MethodGet, "Audio/item-id/universal"},
		{http.MethodGet, "Videos/item-id/master.m3u8"},
		{http.MethodGet, "Items/item-id/PlaybackInfo"},
		{http.MethodPost, "Items/item-id/PlaybackInfo"},
		{http.MethodPost, "Sessions/Playing"},
		{http.MethodGet, "Users/AuthenticateByName"},
		{http.MethodDelete, "Sessions/Logout"},
		{http.MethodPost, "Users/AuthenticateByName/"},
		{http.MethodPost, "sessions/Logout"},
		{http.MethodGet, ""},
		{http.MethodPost, "unknown"},
	}
	for _, route := range tests {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			req := httptest.NewRequest(route.method, Prefix+route.path, nil)
			req.RemoteAddr = "127.0.0.1:1234"
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusTeapot || rec.Header().Get("X-Fall-Through") != "yes" || rec.Body.String() != fallbackBody {
				t.Fatalf("response = %d %v %q, want fall-through", rec.Code, rec.Header(), rec.Body)
			}
		})
	}
}

func TestBoundaryToggleTrustZonePanicAndRelay(t *testing.T) {
	panicHandler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
	enabled, err := New(Config{Enabled: true, AllowedCIDRs: []string{"127.0.0.0/8"}}, nil, panicHandler)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		handler    *Handler
		remoteAddr string
		relayed    bool
		want       int
	}{
		{"panic recovers", enabled, "127.0.0.1:1234", false, http.StatusInternalServerError},
		{"out of CIDR rejected", enabled, "203.0.113.9:1234", false, http.StatusForbidden},
		{"relay path hidden", enabled, "127.0.0.1:1234", true, http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.handler.SetRelayedRequestPredicate(func(*http.Request) bool { return tc.relayed })
			req := httptest.NewRequest(http.MethodGet, Prefix+"System/Info", nil)
			req.RemoteAddr = tc.remoteAddr
			rec := httptest.NewRecorder()
			tc.handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}

	disabled, err := New(Config{AllowedCIDRs: []string{"127.0.0.0/8"}}, nil, panicHandler)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, Prefix+"Users/AuthenticateByName", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	disabled.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled prefix status = %d, want 404", rec.Code)
	}
}

func TestProxyHeadersAreUntrustedUnlessProxyCIDRConfigured(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	untrusted, err := New(Config{Enabled: true, AllowedCIDRs: []string{"127.0.0.0/8"}}, nil, next)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, Prefix+"System/Info", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec := httptest.NewRecorder()
	untrusted.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("untrusted proxy header changed admission: %d", rec.Code)
	}

	trusted, err := New(Config{
		Enabled: true, AllowedCIDRs: []string{"127.0.0.0/8"}, TrustedProxyCIDRs: []string{"127.0.0.0/8"},
	}, nil, next)
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	trusted.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("trusted proxy client CIDR status = %d, want 403", rec.Code)
	}
}

func TestAuthenticateByNameFailureResponsesAreUniform(t *testing.T) {
	ctx := t.Context()
	dataDir := storetest.MigratedDataDir(t)
	st := storetest.OpenAt(t, dataDir)
	restricted := restrictedFixture(t, st)
	now := time.Now().UTC()

	seedProfile := func(name string, optIn, disabled bool) {
		t.Helper()
		profile, err := st.CreateProfile(ctx, name, store.ProfileAdult, store.LibraryFilter{Revealed: true, AllLibraries: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetProfileEntryPIN(ctx, profile.ID, "2468"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.UpdateProfileSettings(ctx, profile.ID, store.ProfileSettings{IntegrationSignIn: &optIn, Disabled: &disabled}); err != nil {
			t.Fatal(err)
		}
	}
	seedProfile("Timing Calibration", true, false)
	seedProfile("Opted In", true, false)
	seedProfile("Opted Out", false, false)
	seedProfile("Disabled", true, true)
	seedProfile("Lockout Probe", true, false)

	newHandler := func(st *store.Store) *Handler {
		t.Helper()
		service := auth.NewServiceWithClock(st, func() time.Time { return now })
		service.SetIntegrationAttemptSecret([]byte("uniform-response-secret"))
		handler, err := New(Config{Enabled: true, AllowedCIDRs: []string{"127.0.0.0/8"}}, service, nil)
		if err != nil {
			t.Fatal(err)
		}
		return handler
	}
	handler := newHandler(st)

	type response struct {
		status  int
		header  http.Header
		body    []byte
		elapsed time.Duration
		refusal auth.RefusalReason
	}
	request := func(body string) response {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, Prefix+"Users/AuthenticateByName", strings.NewReader(body))
		req = req.WithContext(auth.WithRefusalNote(ctx))
		req.RemoteAddr = "127.0.0.1:1234"
		rec := httptest.NewRecorder()
		started := time.Now()
		handler.ServeHTTP(rec, req)
		elapsed := time.Since(started)
		restricted.assertAbsent(t, rec.Body.Bytes())
		refusal, _ := auth.RefusalFromContext(req.Context())
		res := rec.Result()
		defer res.Body.Close()
		return response{res.StatusCode, res.Header, rec.Body.Bytes(), elapsed, refusal.Reason}
	}
	requireLogin := func(body string) {
		t.Helper()
		if got := request(body); got.status != http.StatusOK {
			t.Fatalf("valid login returned %d, want 200", got.status)
		}
	}
	calibrate := func() response {
		t.Helper()
		failed := request(`{"Username":"Timing Calibration","Pw":"wrong"}`)
		// Reset this Profile's backoff so each calibration verifies the PIN.
		requireLogin(`{"Username":"Timing Calibration","Pw":"2468"}`)
		return failed
	}

	want := calibrate()
	if want.status != http.StatusUnauthorized || string(want.body) != "{\"error\":\"authentication failed\"}\n" {
		t.Fatalf("wrong-password response status %d or body differs from the expected authentication failure", want.status)
	}
	checkFailure := func(name string, got response, reason auth.RefusalReason) {
		t.Helper()
		if got.refusal != reason {
			t.Fatalf("%s reached refusal %q, want %q", name, got.refusal, reason)
		}
		sameHeader := reflect.DeepEqual(got.header, want.header)
		sameBody := bytes.Equal(got.body, want.body)
		if got.status != want.status || !sameHeader || !sameBody {
			t.Fatalf("%s response differs from wrong password: status %d, headers match %t, body matches %t", name, got.status, sameHeader, sameBody)
		}
		if got.elapsed < 70*time.Millisecond {
			t.Fatalf("%s elapsed = %s, escaped uniform timing class", name, got.elapsed)
		}
	}
	checkFailure("wrong password", want, auth.RefusalIntegrationRejected)

	const validLockoutLogin = `{"Username":"Lockout Probe","Pw":"2468"}`
	requireLogin(validLockoutLogin)
	for range 5 {
		got := request(`{"Username":"Lockout Probe","Pw":"wrong"}`)
		checkFailure("failed lockout attempt", got, auth.RefusalIntegrationRejected)
	}
	checkFailure("locked before restart", request(validLockoutLogin), auth.RefusalIntegrationLocked)

	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	handler = newHandler(storetest.OpenAt(t, dataDir))

	cases := []struct {
		name   string
		body   string
		reason auth.RefusalReason
	}{
		{"unknown profile", `{"Username":"missing-profile-sensitive-name","Pw":"2468"}`, auth.RefusalIntegrationRejected},
		{"wrong password", `{"Username":"Opted In","Pw":"wrong"}`, auth.RefusalIntegrationRejected},
		{"opted out", `{"Username":"Opted Out","Pw":"2468"}`, auth.RefusalIntegrationRejected},
		{"disabled", `{"Username":"Disabled","Pw":"2468"}`, auth.RefusalIntegrationRejected},
		{"managed", `{"Username":"Managed","Pw":"anything"}`, auth.RefusalIntegrationRejected},
		{"malformed", `{"Username":`, auth.RefusalIntegrationRejected},
		{"locked after restart", validLockoutLogin, auth.RefusalIntegrationLocked},
	}

	// Interleave the cases and calibration samples and compare their minima so
	// isolated scheduling delays do not make equivalent paths appear different.
	minimums := make([]time.Duration, len(cases))
	ambient := make([]time.Duration, len(cases))
	for round := range 4 {
		for i, tc := range cases {
			got := request(tc.body)
			checkFailure(tc.name, got, tc.reason)
			cal := calibrate()
			checkFailure("calibration", cal, auth.RefusalIntegrationRejected)
			if round == 0 || got.elapsed < minimums[i] {
				minimums[i] = got.elapsed
			}
			if round == 0 || cal.elapsed < ambient[i] {
				ambient[i] = cal.elapsed
			}
		}
	}
	const jitterMargin = 50 * time.Millisecond
	jitter := slices.Max(ambient) - slices.Min(ambient)
	if spread := slices.Max(minimums) - slices.Min(minimums); spread > jitter+jitterMargin {
		t.Fatalf("failure timing spread = %s (samples %v), same-case jitter = %s + margin %s", spread, minimums, jitter, jitterMargin)
	}

	now = now.Add(time.Hour)
	requireLogin(validLockoutLogin)
}
