package updatecheck

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/beamfall/core/internal/release"
)

// testKeypair returns a fresh ed25519 keypair (public + 32-byte seed) so tests
// can sign manifests without the real pinned release secret.
func testKeypair(t *testing.T) (ed25519.PublicKey, []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	return pub, priv.Seed()
}

// signedManifestJSON builds and signs a release manifest with seed, returning
// the JSON the channel endpoint serves.
func signedManifestJSON(t *testing.T, seed []byte, version, channel string) string {
	t.Helper()
	m := release.Manifest{Version: version, Channel: channel}
	sig, err := release.Sign(seed, m)
	if err != nil {
		t.Fatalf("sign manifest: %v", err)
	}
	m.Signature = sig
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return string(b)
}

// newTestService points a Service at an httptest server, relaxing only the
// loopback denial and injecting a test trust anchor (pub) so signed test
// manifests verify. Every other SSRF rule still holds.
func newTestService(baseURL string, pub ed25519.PublicKey) *Service {
	return New(WithBaseURL(baseURL), WithInsecureLoopback(), WithVerificationKey(pub))
}

// TestDisabledMakesNoRequest is the default-off acceptance: when enabled=false
// the service makes NO outbound request and reports enabled=false (ADR-0013 §7,
// invariant #5).
func TestDisabledMakesNoRequest(t *testing.T) {
	pub, seed := testKeypair(t)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		fmt.Fprint(w, signedManifestJSON(t, seed, "2.0.0", "stable"))
	}))
	defer srv.Close()

	svc := newTestService(srv.URL, pub)
	status, err := svc.Check(context.Background(), false, "stable", "1.0.0")
	if err != nil {
		t.Fatalf("Check(disabled) error = %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("disabled check made %d outbound requests, want 0", got)
	}
	if status.Enabled {
		t.Fatalf("status.Enabled = true, want false")
	}
	if status.UpdateAvailable {
		t.Fatalf("status.UpdateAvailable = true, want false when disabled")
	}
	if status.LatestVersion != "" {
		t.Fatalf("status.LatestVersion = %q, want empty when disabled", status.LatestVersion)
	}
}

// TestEnabledNewerOlderEqual checks the comparison once the operator opts in,
// over a properly SIGNED manifest (Verified=true is a precondition for any
// update to be reported).
func TestEnabledNewerOlderEqual(t *testing.T) {
	pub, seed := testKeypair(t)
	cases := []struct {
		name    string
		latest  string
		current string
		want    bool
	}{
		{"newer", "1.6.0", "1.5.0", true},
		{"newer patch", "1.5.1", "1.5.0", true},
		{"equal", "1.5.0", "1.5.0", false},
		{"older", "1.4.0", "1.5.0", false},
		{"newer with v prefix", "v2.0.0", "1.9.9", true},
		{"dev current never updates", "1.6.0", "development", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, signedManifestJSON(t, seed, tc.latest, "stable"))
			}))
			defer srv.Close()

			svc := newTestService(srv.URL, pub)
			status, err := svc.Check(context.Background(), true, "stable", tc.current)
			if err != nil {
				t.Fatalf("Check error = %v", err)
			}
			if !status.Enabled {
				t.Fatalf("status.Enabled = false, want true")
			}
			if !status.Verified {
				t.Fatalf("status.Verified = false over a signed manifest, want true")
			}
			if status.LatestVersion != normalizeLatest(tc.latest) {
				t.Fatalf("status.LatestVersion = %q, want %q", status.LatestVersion, normalizeLatest(tc.latest))
			}
			if status.UpdateAvailable != tc.want {
				t.Fatalf("UpdateAvailable(latest=%q current=%q) = %v, want %v",
					tc.latest, tc.current, status.UpdateAvailable, tc.want)
			}
		})
	}
}

// normalizeLatest mirrors the service's TrimSpace on the parsed version (the v
// prefix is preserved in LatestVersion — only comparison strips it).
func normalizeLatest(v string) string { return v }

// TestUnsignedManifestRefused is the supply-chain acceptance for the old-shape
// migration case: a manifest with NO signature field — the exact
// document a pre-PANEL-11 channel publishes — is REFUSED. Verified=false,
// UpdateAvailable=false, and the error names the refusal. This is the
// fail-closed deployment-ordering guarantee.
func TestUnsignedManifestRefused(t *testing.T) {
	pub, _ := testKeypair(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"version":"9.9.9"}`) // old shape: no signature
	}))
	defer srv.Close()

	svc := newTestService(srv.URL, pub)
	status, err := svc.Check(context.Background(), true, "stable", "1.0.0")
	if err == nil {
		t.Fatal("unsigned manifest was accepted, want refusal")
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Fatalf("error = %v, want a 'refused' supply-chain error", err)
	}
	if status.Verified {
		t.Fatal("status.Verified = true for an unsigned manifest, want false")
	}
	if status.UpdateAvailable {
		t.Fatal("status.UpdateAvailable = true for an unsigned manifest, want false (fail-closed)")
	}
}

// TestTamperedManifestRefused proves a manifest whose body was altered AFTER
// signing (here: a bumped version) fails verification and is refused — a
// compromised mirror cannot inflate the version to drive a blessed upgrade.
func TestTamperedManifestRefused(t *testing.T) {
	pub, seed := testKeypair(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signed := signedManifestJSON(t, seed, "1.5.0", "stable")
		// Tamper: change the version, keeping the now-stale signature.
		tampered := strings.Replace(signed, `"version":"1.5.0"`, `"version":"9.9.9"`, 1)
		fmt.Fprint(w, tampered)
	}))
	defer srv.Close()

	svc := newTestService(srv.URL, pub)
	status, err := svc.Check(context.Background(), true, "stable", "1.0.0")
	if err == nil {
		t.Fatal("tampered manifest was accepted, want refusal")
	}
	if status.Verified || status.UpdateAvailable {
		t.Fatalf("tampered manifest blessed (Verified=%v UpdateAvailable=%v), want both false", status.Verified, status.UpdateAvailable)
	}
}

// TestWrongKeyRefused proves a manifest signed by a DIFFERENT key than the
// pinned/injected trust anchor is refused — the pin is what is trusted, not the
// mere presence of a signature.
func TestWrongKeyRefused(t *testing.T) {
	pub, _ := testKeypair(t)          // the trusted anchor
	_, attackerSeed := testKeypair(t) // a different signer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, signedManifestJSON(t, attackerSeed, "9.9.9", "stable"))
	}))
	defer srv.Close()

	svc := newTestService(srv.URL, pub)
	_, err := svc.Check(context.Background(), true, "stable", "1.0.0")
	if err == nil {
		t.Fatal("manifest signed by a non-pinned key was accepted, want refusal")
	}
}

func TestSignedManifestForWrongChannelRefused(t *testing.T) {
	pub, seed := testKeypair(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, signedManifestJSON(t, seed, "2.0.0-beta.1", "beta"))
	}))
	defer srv.Close()

	svc := newTestService(srv.URL, pub)
	status, err := svc.Check(context.Background(), true, "stable", "1.0.0")
	if err == nil {
		t.Fatal("signed beta manifest was accepted for stable channel")
	}
	if status.Verified || status.UpdateAvailable {
		t.Fatalf("wrong-channel manifest blessed (Verified=%v UpdateAvailable=%v)", status.Verified, status.UpdateAvailable)
	}
}

func TestOversizedManifestRefused(t *testing.T) {
	pub, seed := testKeypair(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, signedManifestJSON(t, seed, "2.0.0", "stable"))
		fmt.Fprint(w, strings.Repeat(" ", maxResponseBytes))
	}))
	defer srv.Close()

	svc := newTestService(srv.URL, pub)
	status, err := svc.Check(context.Background(), true, "stable", "1.0.0")
	if err == nil {
		t.Fatal("oversized manifest was accepted")
	}
	if status.Verified || status.UpdateAvailable {
		t.Fatalf("oversized manifest blessed (Verified=%v UpdateAvailable=%v)", status.Verified, status.UpdateAvailable)
	}
}

func TestAllowsConfiguredRedirectCount(t *testing.T) {
	pub, seed := testKeypair(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stable.json":
			http.Redirect(w, r, "/redirect-1", http.StatusFound)
		case "/redirect-1":
			http.Redirect(w, r, "/redirect-2", http.StatusFound)
		case "/redirect-2":
			http.Redirect(w, r, "/redirect-3", http.StatusFound)
		case "/redirect-3":
			fmt.Fprint(w, signedManifestJSON(t, seed, "2.0.0", "stable"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	svc := newTestService(srv.URL, pub)
	status, err := svc.Check(context.Background(), true, "stable", "1.0.0")
	if err != nil {
		t.Fatalf("three redirects: %v", err)
	}
	if !status.Verified || !status.UpdateAvailable {
		t.Fatalf("status after three redirects = %+v", status)
	}
}

// TestRequestCarriesNoIdentifyingPayload is the invariant #5 acceptance: the
// outbound request is a bare GET — no body, no query string, no cookies, and no
// identifying headers beyond the generic User-Agent. The httptest handler
// inspects exactly what the server sent.
func TestRequestCarriesNoIdentifyingPayload(t *testing.T) {
	pub, seed := testKeypair(t)
	var seen *http.Request
	var bodyLen int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Clone(context.Background())
		bodyLen = r.ContentLength
		fmt.Fprint(w, signedManifestJSON(t, seed, "1.0.0", "stable"))
	}))
	defer srv.Close()

	svc := newTestService(srv.URL, pub)
	if _, err := svc.Check(context.Background(), true, "stable", "0.9.0"); err != nil {
		t.Fatalf("Check error = %v", err)
	}
	if seen == nil {
		t.Fatal("handler saw no request")
	}
	if seen.Method != http.MethodGet {
		t.Fatalf("method = %q, want GET", seen.Method)
	}
	if bodyLen > 0 {
		t.Fatalf("request carried a body of %d bytes, want none", bodyLen)
	}
	if seen.URL.RawQuery != "" {
		t.Fatalf("request URL had a query string %q, want none", seen.URL.RawQuery)
	}
	if len(seen.Cookies()) != 0 {
		t.Fatalf("request carried cookies %v, want none", seen.Cookies())
	}
	// The ONLY identifying-ish header is the generic UA — generic product name,
	// no per-instance value. Any other client-set header is a telemetry leak.
	allowed := map[string]bool{
		"User-Agent":      true,
		"Accept-Encoding": true, // Go transport default, carries no server identity
	}
	for name, values := range seen.Header {
		if !allowed[name] {
			t.Fatalf("request carried unexpected header %q=%v (possible telemetry leak)", name, values)
		}
	}
	if ua := seen.Header.Get("User-Agent"); ua != userAgent {
		t.Fatalf("User-Agent = %q, want generic %q", ua, userAgent)
	}
	// And the path is the static channel manifest — channel name only, nothing
	// about this server.
	if seen.URL.Path != "/stable.json" {
		t.Fatalf("request path = %q, want /stable.json", seen.URL.Path)
	}
}

// TestSSRFRejectsPlainHTTP proves the hardened client refuses a non-https
// scheme in production (no insecure loopback). Mirrors the scraper's ADR-0012
// §9 gate.
func TestSSRFRejectsPlainHTTP(t *testing.T) {
	// No WithInsecureLoopback: a plain-http base URL must be refused by vetURL.
	svc := New(WithBaseURL("http://updates.example.com/channels"))
	_, err := svc.Check(context.Background(), true, "stable", "1.0.0")
	if err == nil {
		t.Fatal("plain-http base URL was accepted, want https-only refusal")
	}
}
