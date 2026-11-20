//go:build !beamfall_appstore

package compat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func compatStatus(t *testing.T, h *Handler, remoteAddr string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, Prefix+"System/Info/Public", nil)
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// TestSettingsControllerAppliesWithoutRestart covers CORE-2683: a Console
// write turns the route group on or off and changes the trust zone for the
// next request, and a restart reads the stored settings back.
func TestSettingsControllerAppliesWithoutRestart(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	handler, err := New(Config{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := NewSettingsController(ctx, st, store.JellyfinCompatConfig{}, handler)
	if err != nil {
		t.Fatal(err)
	}
	if code := compatStatus(t, handler, "127.0.0.1:1234"); code != http.StatusNotFound {
		t.Fatalf("default status = %d, want 404", code)
	}

	on := true
	if _, err := controller.Configure(ctx, store.JellyfinCompatPatch{Enabled: &on, AllowedCIDRs: &[]string{"10.0.0.0/8"}}); err != nil {
		t.Fatal(err)
	}
	if code := compatStatus(t, handler, "127.0.0.1:1234"); code != http.StatusForbidden {
		t.Fatalf("enabled, outside the new CIDR: status = %d, want 403", code)
	}

	restarted, err := New(Config{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSettingsController(ctx, st, store.JellyfinCompatConfig{}, restarted); err != nil {
		t.Fatal(err)
	}
	if code := compatStatus(t, restarted, "127.0.0.1:1234"); code != http.StatusForbidden {
		t.Fatalf("after restart: status = %d, want the stored settings (403)", code)
	}

	off := false
	if _, err := controller.Configure(ctx, store.JellyfinCompatPatch{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if code := compatStatus(t, handler, "10.1.2.3:1234"); code != http.StatusNotFound {
		t.Fatalf("turned off: status = %d, want 404", code)
	}

	bad := []string{"100.64.0.0/10"}
	if _, err := controller.Configure(ctx, store.JellyfinCompatPatch{Enabled: &on, AllowedCIDRs: &bad}); err == nil {
		t.Fatal("overlay CIDR accepted")
	}
	if code := compatStatus(t, handler, "10.1.2.3:1234"); code != http.StatusNotFound {
		t.Fatalf("refused write changed the live settings: status = %d", code)
	}
}
