//go:build io

package ioe2e

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/beamfall/core/internal/release"
	"github.com/beamfall/core/internal/updatecheck"
)

// TestEgress_updatecheck_manifest drives the REAL updatecheck.Service against a
// loopback double serving a SIGNED manifest, and asserts the GET of
// /<channel>.json plus the parsed + verified result.
//
// io:egress-updatecheck-manifest
func TestEgress_updatecheck_manifest(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	seed := priv.Seed()

	manifest := release.Manifest{Version: "2.0.0", Channel: "stable"}
	sig, err := release.Sign(seed, manifest)
	if err != nil {
		t.Fatalf("sign manifest: %v", err)
	}
	manifest.Signature = sig
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	var (
		hits     int32
		gotPath  string
		gotMeth  string
		gotAgent string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotPath = r.URL.Path
		gotMeth = r.Method
		gotAgent = r.Header.Get("User-Agent")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	svc := updatecheck.New(
		updatecheck.WithBaseURL(srv.URL),
		updatecheck.WithInsecureLoopback(),
		updatecheck.WithVerificationKey(pub),
	)
	status, err := svc.Check(context.Background(), true, "stable", "1.0.0")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("manifest double hit %d times, want 1", got)
	}
	if gotMeth != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMeth)
	}
	if gotPath != "/stable.json" {
		t.Errorf("path = %q, want /stable.json", gotPath)
	}
	if gotAgent == "" {
		t.Error("User-Agent header empty; updatecheck must send the generic UA")
	}
	if !status.Verified {
		t.Error("status.Verified = false, want true for a correctly signed manifest")
	}
	if status.LatestVersion != "2.0.0" {
		t.Errorf("LatestVersion = %q, want 2.0.0", status.LatestVersion)
	}
	if !status.UpdateAvailable {
		t.Error("UpdateAvailable = false, want true (2.0.0 > 1.0.0)")
	}
}
