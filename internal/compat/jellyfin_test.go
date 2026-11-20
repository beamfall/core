package compat

import (
	"bytes"
	"fmt"
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
		{http.MethodGet, Prefix + "System/Info"},
		{http.MethodGet, Prefix + "System/Info/Public"},
		{http.MethodPost, Prefix + "Library/Refresh"},
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
	enabled, err := New(Config{
		Enabled: true, AllowedCIDRs: []string{"127.0.0.0/8"},
		SetupRequired: func() bool { panic("boom") },
	}, nil, nil)
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
			req := httptest.NewRequest(http.MethodGet, Prefix+"System/Info/Public", nil)
			req.RemoteAddr = tc.remoteAddr
			rec := httptest.NewRecorder()
			tc.handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}

	disabled, err := New(Config{AllowedCIDRs: []string{"127.0.0.0/8"}}, nil, nil)
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
	untrusted, err := New(Config{Enabled: true, AllowedCIDRs: []string{"127.0.0.0/8"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, Prefix+"System/Info/Public", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec := httptest.NewRecorder()
	untrusted.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("untrusted proxy header changed admission: %d", rec.Code)
	}

	trusted, err := New(Config{
		Enabled: true, AllowedCIDRs: []string{"127.0.0.0/8"}, TrustedProxyCIDRs: []string{"127.0.0.0/8"},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	trusted.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("trusted proxy client CIDR status = %d, want 403", rec.Code)
	}
}

func TestBoundaryRejectsUnspecifiedClientAddresses(t *testing.T) {
	handler, err := New(Config{
		Enabled:           true,
		AllowedCIDRs:      []string{"0.0.0.0/0", "::/0"},
		TrustedProxyCIDRs: []string{"127.0.0.1/32"},
	}, nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("request reached fallback") }))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ remote, forwarded string }{
		{"0.0.0.0:1234", ""},
		{"[::]:1234", ""},
		{"127.0.0.1:1234", "0.0.0.0"},
		{"127.0.0.1:1234", "::"},
	} {
		req := httptest.NewRequest(http.MethodPost, Prefix+"Users/AuthenticateByName", strings.NewReader(`{}`))
		req.RemoteAddr = tc.remote
		req.Header.Set("X-Forwarded-For", tc.forwarded)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden || rec.Body.String() != "compatibility route unavailable\n" {
			t.Errorf("remote %q, forwarded %q returned %d %q, want existing forbidden response", tc.remote, tc.forwarded, rec.Code, rec.Body)
		}
	}
}

func TestAuthenticateByNameFailureResponsesAreUniform(t *testing.T) {
	ctx := t.Context()
	dataDir := storetest.MigratedDataDir(t)
	st := storetest.OpenAt(t, dataDir)
	restricted := restrictedFixture(t, st)
	now := time.Now().UTC()

	seedProfile := func(name string, optIn, disabled bool) store.ProfileID {
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
		return profile.ID
	}
	seedProfile("Opted In", true, false)
	seedProfile("Opted Out", false, false)
	seedProfile("Disabled", true, true)
	nameProbe := seedProfile("Name Lockout Probe", true, false)
	ipProbe := seedProfile("IP Lockout Probe", true, false)

	newHandler := func(st *store.Store) *Handler {
		t.Helper()
		service := auth.NewServiceWithClock(st, func() time.Time { return now })
		service.SetIntegrationAttemptSecret([]byte("uniform-response-secret"))
		handler, err := New(Config{
			Enabled: true, AllowedCIDRs: []string{"192.0.2.0/24"}, TrustedProxyCIDRs: []string{"127.0.0.1/32"},
		}, service, nil)
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
		refusal auth.RefusalReason
	}
	request := func(body, clientIP string) response {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, Prefix+"Users/AuthenticateByName", strings.NewReader(body))
		req = req.WithContext(auth.WithRefusalNote(ctx))
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set("X-Forwarded-For", clientIP)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		restricted.assertAbsent(t, rec.Body.Bytes())
		refusal, _ := auth.RefusalFromContext(req.Context())
		res := rec.Result()
		defer res.Body.Close()
		return response{res.StatusCode, res.Header, rec.Body.Bytes(), refusal.Reason}
	}
	requireLogin := func(body, clientIP string) {
		t.Helper()
		if got := request(body, clientIP); got.status != http.StatusOK {
			t.Fatalf("valid login returned %d, want 200", got.status)
		}
	}

	want := request(`{"Username":"Opted In","Pw":"wrong"}`, "192.0.2.1")
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
	}
	checkFailure("wrong password", want, auth.RefusalIntegrationRejected)

	const (
		validNameLogin = `{"Username":"Name Lockout Probe","Pw":"2468"}`
		validIPLogin   = `{"Username":"IP Lockout Probe","Pw":"2468"}`
		wrongNameLogin = `{"Username":"Name Lockout Probe","Pw":"wrong"}`
		repeatedIP     = "192.0.2.2"
		budget         = 5
	)
	for range budget - 1 {
		checkFailure("failed attempt before successful login", request(wrongNameLogin, repeatedIP), auth.RefusalIntegrationRejected)
	}
	// The successful login is the fifth reservation on both lanes and resets
	// both. The later requests exercise each reset in isolation.
	requireLogin(validNameLogin, repeatedIP)
	for i := range budget {
		clientIP := fmt.Sprintf("192.0.2.%d", 10+i)
		got := request(wrongNameLogin, clientIP)
		checkFailure("one name from many IPs", got, auth.RefusalIntegrationRejected)

		got = request(fmt.Sprintf(`{"Username":"unknown-%d","Pw":"wrong"}`, i), repeatedIP)
		checkFailure("one IP with many names", got, auth.RefusalIntegrationRejected)
	}
	checkFailure("name locked before restart", request(validNameLogin, "192.0.2.20"), auth.RefusalIntegrationLocked)
	checkFailure("IP locked before restart", request(validIPLogin, repeatedIP), auth.RefusalIntegrationLocked)
	requireLogin(validIPLogin, "192.0.2.21")

	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st = storetest.OpenAt(t, dataDir)
	handler = newHandler(st)
	checkFailure("name locked after restart", request(validNameLogin, "192.0.2.22"), auth.RefusalIntegrationLocked)
	checkFailure("IP locked after restart", request(validIPLogin, repeatedIP), auth.RefusalIntegrationLocked)

	entries, err := st.ActivityEntries(ctx, store.ActivityQuery{
		Filter: store.LibraryFilter{AllLibraries: true, Revealed: true}, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	denials := make(map[store.ProfileID]int)
	for _, entry := range entries {
		if entry.Action == "integration.login.denied" {
			denials[entry.ProfileID]++
		}
	}
	if denials[nameProbe] != 2*budget+1 || denials[ipProbe] != 2 {
		t.Fatalf("audited denials for name probe = %d and IP probe = %d, want %d and 2", denials[nameProbe], denials[ipProbe], 2*budget+1)
	}

	cases := []struct {
		name string
		body string
	}{
		{"unknown profile", `{"Username":"missing-profile-sensitive-name","Pw":"2468"}`},
		{"wrong password", `{"Username":"Opted In","Pw":"wrong"}`},
		{"opted out", `{"Username":"Opted Out","Pw":"2468"}`},
		{"disabled", `{"Username":"Disabled","Pw":"2468"}`},
		{"managed", `{"Username":"Managed","Pw":"anything"}`},
		{"malformed", `{"Username":`},
	}
	for i, tc := range cases {
		got := request(tc.body, fmt.Sprintf("192.0.2.%d", 30+i))
		checkFailure(tc.name, got, auth.RefusalIntegrationRejected)
	}

	now = now.Add(time.Hour)
	requireLogin(validNameLogin, "192.0.2.23")
	requireLogin(validIPLogin, repeatedIP)
}
