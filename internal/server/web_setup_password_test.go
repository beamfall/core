package server

import (
	"net/http"
	"strings"
	"testing"
)

// CORE-1601: a too-short admin password is a 400 that carries the shared rule
// text, and the refusal leaves the one-time token installed.
func TestWebSetupRefusesShortAdminPasswordWithoutConsumingToken(t *testing.T) {
	srv := newTestServer(t)
	srv.pairing.SetBootstrapToken("setup-tok-1")

	rec := postSetup(srv, `{"token":"setup-tok-1","deviceName":"B","profileName":"Owner","adminPassword":"short"}`, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Admin password must be at least 8 characters.") {
		t.Fatalf("body = %q, want the minimum-length message", rec.Body.String())
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatalf("refused setup set cookies: %v", rec.Result().Cookies())
	}
	if !srv.pairing.SetupRequired() {
		t.Fatal("refused setup consumed the one-time token")
	}
}
