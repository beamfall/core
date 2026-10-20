package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSourceOfferIsPublicAndUsesReleaseInjectedHTTPSURL(t *testing.T) {
	srv := newTestServer(t)
	srv.SetSourceOfferURL("https://source.beamfall.example/releases/v1.2.3/corresponding-source")
	rec := httptest.NewRecorder()
	// Deliberately omit credentials: AGPL §13 requires this offer to be public.
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/source", nil))
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("GET /source status = %d, want %d", rec.Code, http.StatusTemporaryRedirect)
	}
	if got := rec.Header().Get("Location"); got != "https://source.beamfall.example/releases/v1.2.3/corresponding-source" {
		t.Fatalf("GET /source Location = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("GET /source Cache-Control = %q, want no-store", got)
	}
}

func TestSourceOfferFailsClosedWithoutValidReleaseURL(t *testing.T) {
	for _, offerURL := range []string{"", "http://source.example/archive", "https://user@example.com/archive", "://bad"} {
		t.Run(offerURL, func(t *testing.T) {
			srv := newTestServer(t)
			srv.SetSourceOfferURL(offerURL)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/source", nil))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("GET /source status = %d, want %d", rec.Code, http.StatusNotFound)
			}
		})
	}
}
