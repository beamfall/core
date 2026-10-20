package app

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMetricsScrapeTokenIsMintedIntoA0600File pins the property the whole
// GCR-012-H mechanism rests on: the ability to scrape /metrics without proving
// locality follows from the ability to read one file, and that file is readable
// only by the user the server runs as. A mode that let group or other read it
// would hand the credential to every account on the host.
func TestMetricsScrapeTokenIsMintedIntoA0600File(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "secrets", metricsScrapeSecretRef)

	minted, err := provisionMetricsScrapeToken(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hex.DecodeString(minted); err != nil || len(minted) != 64 {
		t.Fatalf("minted credential is not 32 bytes of hex: len %d, err %v", len(minted), err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("%s has mode %v, want 0600", path, info.Mode().Perm())
	}
	stored, err := os.ReadFile(path) //nolint:gosec // test-owned path inside t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(stored)) != minted {
		t.Fatal("the file does not carry the credential that was returned to the caller")
	}
}

// TestMetricsScrapeTokenRotatesAndRewrites pins the restart contract the runbook
// states: every start mints a new credential and replaces the file rather than
// appending to it, so a scraper that cached the old value is refused and the
// file never accumulates superseded credentials that would each still work.
func TestMetricsScrapeTokenRotatesAndRewrites(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "secrets", metricsScrapeSecretRef)

	first, err := provisionMetricsScrapeToken(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := provisionMetricsScrapeToken(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("a restart re-issued the same credential instead of rotating it")
	}
	stored, err := os.ReadFile(path) //nolint:gosec // test-owned path inside t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(stored)) != second {
		t.Fatal("the file does not carry the credential from the most recent start")
	}
	if strings.Contains(string(stored), first) {
		t.Fatal("the superseded credential is still in the file — it was appended to, not rewritten")
	}
}

// TestMetricsScrapeTokenProvisioningFailsClosed keeps a provisioning failure
// from becoming an availability-driven excuse to install something. The caller
// installs a credential only on success, so an unwritable secrets directory
// leaves the server with none — and a server with no credential refuses every
// bearer, closing /metrics entirely rather than opening it.
func TestMetricsScrapeTokenProvisioningFailsClosed(t *testing.T) {
	dataDir := t.TempDir()
	// A regular file where the secrets directory belongs: MkdirAll cannot
	// create the directory, so the write cannot succeed.
	if err := os.WriteFile(filepath.Join(dataDir, "secrets"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	minted, err := provisionMetricsScrapeToken(dataDir)
	if err == nil {
		t.Fatal("an unwritable secrets directory produced a credential")
	}
	if minted != "" {
		t.Fatal("a failed provisioning still returned a credential to install")
	}
}

// TestBootInstallsTheMetricsScrapeCredential drives the real composition root
// rather than the helper it calls: a booted process must have written the
// credential and handed it to the HTTP surface, so a request carrying the value
// from that file is served while the same request without it is not. Removing
// the wiring in New leaves both requests refused and fails this.
//
// Both shapes are driven, and the host-local one is the operator-facing half of
// the 2026-08-07 decision: a client on this host, sending no hop header, gets a
// 403 until it reads <DataDir>/secrets/metrics-scrape-token and presents it.
// That is what a scraper surviving the upgrade actually has to do.
func TestBootInstallsTheMetricsScrapeCredential(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	beamfall, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()

	stored, err := os.ReadFile(filepath.Join(cfg.DataDir, "secrets", metricsScrapeSecretRef))
	if err != nil {
		t.Fatalf("boot did not write the scrape credential: %v", err)
	}
	credential := strings.TrimSpace(string(stored))

	scrape := func(hopHeader, authorization string) int {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.RemoteAddr = "127.0.0.1:5000"
		if hopHeader != "" {
			req.Header.Set("X-Forwarded-For", hopHeader)
		}
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		rec := httptest.NewRecorder()
		beamfall.Handler().ServeHTTP(rec, req)
		return rec.Code
	}

	for _, shape := range []struct {
		name string
		// hopHeader empty is a client on this host with nothing in between;
		// set, it is a hop header this process reads as host-side evidence
		// that the peer address is the proxy's, not the client's.
		hopHeader string
	}{
		{name: "host-local", hopHeader: ""},
		{name: "relayed", hopHeader: "203.0.113.7"},
	} {
		t.Run(shape.name, func(t *testing.T) {
			if code := scrape(shape.hopHeader, ""); code != http.StatusForbidden {
				t.Fatalf("scrape without the credential: status = %d, want %d", code, http.StatusForbidden)
			}
			if code := scrape(shape.hopHeader, "Bearer "+credential); code != http.StatusOK {
				t.Fatalf("scrape with the booted credential: status = %d, want %d", code, http.StatusOK)
			}
		})
	}
}
