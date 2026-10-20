package compat

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

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
	st := storetest.Open(t)
	now := time.Now().UTC()
	authService := auth.NewServiceWithClock(st, func() time.Time { return now })
	authService.SetIntegrationAttemptSecret([]byte("uniform-response-secret"))
	calibrationProfile, err := st.CreateProfile(t.Context(), "Timing Calibration", store.ProfileAdult, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetProfileEntryPIN(t.Context(), calibrationProfile.ID, "2468"); err != nil {
		t.Fatal(err)
	}
	integrationSignIn := true
	if _, err := st.UpdateProfileSettings(t.Context(), calibrationProfile.ID, store.ProfileSettings{IntegrationSignIn: &integrationSignIn}); err != nil {
		t.Fatal(err)
	}
	handler, err := New(Config{Enabled: true, AllowedCIDRs: []string{"127.0.0.0/8"}}, authService, nil)
	if err != nil {
		t.Fatal(err)
	}

	type response struct {
		status  int
		body    []byte
		elapsed time.Duration
	}
	request := func(body string) response {
		req := httptest.NewRequest(http.MethodPost, Prefix+"Users/AuthenticateByName", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1234"
		rec := httptest.NewRecorder()
		started := time.Now()
		handler.ServeHTTP(rec, req)
		return response{status: rec.Code, body: rec.Body.Bytes(), elapsed: time.Since(started)}
	}
	// Timing is compared on the per-case minimum of three samples: the minimum keeps a systematic
	// enumeration oracle visible while discarding scheduler noise that a loaded -race gate adds on
	// top of the uniform failure floor.
	measured := func(body string) response {
		best := request(body)
		for range 2 {
			if again := request(body); again.elapsed < best.elapsed {
				best = again
			}
		}
		return best
	}

	calibrationBody := `{"Username":"Timing Calibration","Pw":"wrong"}`
	calibrate := func() response {
		// The successful request resets this Profile's backoff lane so every ambient sample
		// exercises the same credential-verification path instead of drifting into lockout.
		failed := request(calibrationBody)
		reset := request(`{"Username":"Timing Calibration","Pw":"2468"}`)
		if reset.status != http.StatusOK {
			t.Fatalf("calibration reset response = %d/%q", reset.status, reset.body)
		}
		return failed
	}
	failureBodies := []string{
		`{"Username":"missing-profile-sensitive-name","Pw":"2468"}`,
		`{"Username":"Default","Pw":"wrong"}`,
		`{"Username":"Default","Pw":"2468"}`,
		`{"Username":"Managed","Pw":"anything"}`,
		`{"Username":`,
	}
	responses := make([]response, 0, len(failureBodies)+1)
	ambient := []response{calibrate()}
	for _, body := range failureBodies {
		responses = append(responses, measured(body))
		ambient = append(ambient, calibrate())
	}
	for range 8 {
		_ = request(`{"Username":"Default","Pw":"wrong"}`)
	}
	responses = append(responses, measured(`{"Username":"Default","Pw":"2468"}`))
	ambient = append(ambient, calibrate())

	ambientFastest := ambient[0].elapsed
	ambientSlowest := ambient[0].elapsed
	for _, sample := range ambient[1:] {
		if sample.elapsed < ambientFastest {
			ambientFastest = sample.elapsed
		}
		if sample.elapsed > ambientSlowest {
			ambientSlowest = sample.elapsed
		}
	}
	ambientJitter := ambientSlowest - ambientFastest

	want := responses[0]
	var fastest, slowest time.Duration
	fastest = want.elapsed
	for i, got := range responses {
		if got.status != want.status || !bytes.Equal(got.body, want.body) {
			t.Fatalf("failure %d response = %d/%q, want %d/%q", i, got.status, got.body, want.status, want.body)
		}
		if got.elapsed < 70*time.Millisecond {
			t.Fatalf("failure %d elapsed = %s, escaped uniform timing class", i, got.elapsed)
		}
		if got.elapsed < fastest {
			fastest = got.elapsed
		}
		if got.elapsed > slowest {
			slowest = got.elapsed
		}
	}
	for i, got := range ambient {
		if got.status != want.status || !bytes.Equal(got.body, want.body) {
			t.Fatalf("calibration failure %d response = %d/%q, want %d/%q", i, got.status, got.body, want.status, want.body)
		}
		if got.elapsed < 70*time.Millisecond {
			t.Fatalf("calibration failure %d elapsed = %s, escaped uniform timing class", i, got.elapsed)
		}
	}
	// The fixed margin covers bounded path work after the common floor. At idle it is tighter
	// than the former 75 ms absolute bound; the measured term expands only with host jitter.
	const jitterMargin = 50 * time.Millisecond
	if spread := slowest - fastest; spread > ambientJitter+jitterMargin {
		t.Fatalf("failure timing spread = %s (fastest %s, slowest %s), same-case jitter = %s + margin %s", spread, fastest, slowest, ambientJitter, jitterMargin)
	}
	if want.status != http.StatusUnauthorized || string(want.body) != "{\"error\":\"authentication failed\"}\n" {
		t.Fatalf("uniform failure response = %d/%q", want.status, want.body)
	}
}
