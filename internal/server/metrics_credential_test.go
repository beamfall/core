package server

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// scrapeProbeValue mints a throwaway credential for a test, the same width and
// encoding the process mints at startup. Generating it keeps every credential
// in this file synthetic and per-run.
func scrapeProbeValue(t *testing.T) string {
	t.Helper()
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(raw[:])
}

// authorizedScrape installs a fresh credential on srv and returns a /metrics
// request presenting it, from a client on this host. Every scrape now needs the
// bearer, so a test about the scrape *body* would otherwise be silently
// re-testing admission and reading a 403 body instead of an exposition.
func authorizedScrape(t *testing.T, srv *Server) *http.Request {
	t.Helper()
	credential := scrapeProbeValue(t)
	srv.SetMetricsScrapeToken(credential)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("Authorization", "Bearer "+credential)
	return req
}

func declaredLoopbackProxy(t *testing.T, srv *Server) {
	t.Helper()
	proxies, err := ParseKnownProxies("127.0.0.0/8", false)
	if err != nil {
		t.Fatal(err)
	}
	srv.knownProxies = proxies
}

// TestMetricsScrapeCredentialClosesTheUnprovableCase is the whole of the
// GCR-012-H invariant. The host-side proximity signals can only refuse a
// relayed request they can recognise, and a bare `proxy_pass` adds no hop
// header at all — so a relayed request can be byte-for-byte what a local client
// sends, and no predicate separates them. Admission therefore rests on
// something the fronting proxy has no part in: the scrape credential, minted at
// startup into a 0600 file, so scraping follows from being able to read that
// file rather than from a peer address or a header.
//
// Both directions are pinned. Every request without the bearer is refused —
// including one from a client that really is on this host, which is the
// operator's 2026-08-07 decision and the breaking half of this change — while
// the same shapes presenting the bearer are served. The public opt-in remains a
// third path that serves whoever reaches it. Every ambiguous bearer shape is
// refused rather than guessed at.
func TestMetricsScrapeCredentialClosesTheUnprovableCase(t *testing.T) {
	tests := []struct {
		name          string
		remoteAddr    string
		header        http.Header
		authorization func(credential, other string) []string
		declaredProxy bool
		// install overrides what the process installed for this run. It returns
		// the value and whether anything was installed at all; nil installs the
		// minted credential unchanged.
		install       func(credential string) (string, bool)
		publicMetrics bool
		want          int
	}{
		{
			// The breaking case. Round 2 served this, because a host-local client
			// could be recognised; the operator withdrew that requirement once it
			// was shown a bare `proxy_pass` produces an identical request. Serving
			// it uncredentialed would serve the relayed one too, so it is refused.
			name:       "client on this host is refused with no credential",
			remoteAddr: "127.0.0.1:5000",
			want:       http.StatusForbidden,
		},
		{
			// The way back in for the local scraper the row above now refuses:
			// present the contents of <DataDir>/secrets/metrics-scrape-token.
			name:       "client on this host presenting the credential is served",
			remoteAddr: "127.0.0.1:5000",
			authorization: func(credential, _ string) []string {
				return []string{"Bearer " + credential}
			},
			want: http.StatusOK,
		},
		{
			// A host-local client whose request carries no hop header and whose
			// peer address is loopback is exactly the shape round 2 carved out.
			// Nothing about the request moves the outcome any more — only the
			// bearer does — so the wrong credential is refused here too.
			name:       "client on this host presenting a different credential is refused",
			remoteAddr: "127.0.0.1:5000",
			authorization: func(_, other string) []string {
				return []string{"Bearer " + other}
			},
			want: http.StatusForbidden,
		},
		{
			name:       "relayed client presenting the credential is served",
			remoteAddr: "127.0.0.1:5000",
			header:     http.Header{"X-Forwarded-For": {"203.0.113.7"}},
			authorization: func(credential, _ string) []string {
				return []string{"Bearer " + credential}
			},
			want: http.StatusOK,
		},
		{
			name:       "relayed client with no credential is refused",
			remoteAddr: "127.0.0.1:5000",
			header:     http.Header{"X-Forwarded-For": {"203.0.113.7"}},
			want:       http.StatusForbidden,
		},
		{
			name:       "relayed client presenting a different credential is refused",
			remoteAddr: "127.0.0.1:5000",
			header:     http.Header{"X-Forwarded-For": {"203.0.113.7"}},
			authorization: func(_, other string) []string {
				return []string{"Bearer " + other}
			},
			want: http.StatusForbidden,
		},
		{
			// The topology round 1 left unscrapeable. Declaring the same-host
			// proxy's CIDR no longer changes anything in either direction — the
			// credential decides — but the pair is kept because it is the
			// configuration an operator following the runbook actually runs.
			name:          "declared same-host proxy with no hop header is refused without the credential",
			remoteAddr:    "127.0.0.1:5000",
			declaredProxy: true,
			want:          http.StatusForbidden,
		},
		{
			name:       "declared same-host proxy is served for the holder of the credential",
			remoteAddr: "127.0.0.1:5000",
			authorization: func(credential, _ string) []string {
				return []string{"Bearer " + credential}
			},
			declaredProxy: true,
			want:          http.StatusOK,
		},
		{
			name:       "off-host client presenting the credential is served",
			remoteAddr: "203.0.113.7:5000",
			authorization: func(credential, _ string) []string {
				return []string{"Bearer " + credential}
			},
			want: http.StatusOK,
		},
		{
			name:       "off-host client with no credential is refused",
			remoteAddr: "203.0.113.7:5000",
			want:       http.StatusForbidden,
		},
		{
			name:       "no credential established refuses the bearer it would have matched",
			remoteAddr: "203.0.113.7:5000",
			authorization: func(credential, _ string) []string {
				return []string{"Bearer " + credential}
			},
			install: func(string) (string, bool) { return "", false },
			want:    http.StatusForbidden,
		},
		{
			// A provisioning path that yielded a blank value must install
			// nothing, not a credential whose value is the empty string —
			// otherwise `Authorization: Bearer` with nothing after it becomes
			// the key to the surface.
			name:       "a blank credential installs nothing and refuses an empty bearer",
			remoteAddr: "203.0.113.7:5000",
			authorization: func(_, _ string) []string {
				return []string{"Bearer "}
			},
			install: func(string) (string, bool) { return "   ", true },
			want:    http.StatusForbidden,
		},
		{
			// The operator reads the credential out of a file, so the value that
			// reaches a scraper routinely carries the trailing newline the file
			// ends with. Stored and presented values are normalized the same way
			// so that does not silently refuse a correct credential.
			name:       "a credential stored with surrounding whitespace still matches",
			remoteAddr: "203.0.113.7:5000",
			authorization: func(credential, _ string) []string {
				return []string{"Bearer " + credential}
			},
			install: func(credential string) (string, bool) { return "  " + credential + "\n", true },
			want:    http.StatusOK,
		},
		{
			name:       "lower-case bearer scheme is accepted",
			remoteAddr: "203.0.113.7:5000",
			authorization: func(credential, _ string) []string {
				return []string{"bearer " + credential}
			},
			want: http.StatusOK,
		},
		{
			// RFC 7235 §2.1 permits padding between the scheme and the
			// credential, and the installed value is trimmed the same way, so
			// spacing must not decide whether a correct credential is accepted.
			name:       "padding between the scheme and the value is accepted",
			remoteAddr: "203.0.113.7:5000",
			authorization: func(credential, _ string) []string {
				return []string{"Bearer   " + credential}
			},
			want: http.StatusOK,
		},
		{
			name:       "bearer with an empty value is refused",
			remoteAddr: "203.0.113.7:5000",
			authorization: func(_, _ string) []string {
				return []string{"Bearer    "}
			},
			want: http.StatusForbidden,
		},
		{
			name:       "another authentication scheme is refused",
			remoteAddr: "203.0.113.7:5000",
			authorization: func(credential, _ string) []string {
				return []string{"Basic " + credential}
			},
			want: http.StatusForbidden,
		},
		{
			name:       "a bare value with no scheme is refused",
			remoteAddr: "203.0.113.7:5000",
			authorization: func(credential, _ string) []string {
				return []string{credential}
			},
			want: http.StatusForbidden,
		},
		{
			name:       "trailing text after the value is refused",
			remoteAddr: "203.0.113.7:5000",
			authorization: func(credential, _ string) []string {
				return []string{"Bearer " + credential + " extra"}
			},
			want: http.StatusForbidden,
		},
		{
			name:       "two authorization headers are refused even when one matches",
			remoteAddr: "203.0.113.7:5000",
			authorization: func(credential, _ string) []string {
				return []string{"Bearer " + credential, "Bearer " + credential}
			},
			want: http.StatusForbidden,
		},
		{
			name:          "the public opt-in still serves with no credential",
			remoteAddr:    "203.0.113.7:5000",
			publicMetrics: true,
			want:          http.StatusOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			credential := scrapeProbeValue(t)
			other := scrapeProbeValue(t)
			srv := newTestServer(t)
			if tt.declaredProxy {
				declaredLoopbackProxy(t, srv)
			}
			srv.SetPublicMetrics(tt.publicMetrics)
			install, installed := credential, true
			if tt.install != nil {
				install, installed = tt.install(credential)
			}
			if installed {
				srv.SetMetricsScrapeToken(install)
			}
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			req.RemoteAddr = tt.remoteAddr
			for name, values := range tt.header {
				for _, value := range values {
					req.Header.Add(name, value)
				}
			}
			if tt.authorization != nil {
				for _, value := range tt.authorization(credential, other) {
					req.Header.Add("Authorization", value)
				}
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d; body %q", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

// TestMetricsRefusalIsNotAnOracle keeps the refusal from answering the question
// a caller would otherwise probe it with. A wrong credential and an absent one
// must produce byte-identical responses, and neither the response nor any log
// line may repeat a credential — the presented one or the installed one.
func TestMetricsRefusalIsNotAnOracle(t *testing.T) {
	credential := scrapeProbeValue(t)
	presented := scrapeProbeValue(t)

	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	refuse := func(t *testing.T, authorization string) *httptest.ResponseRecorder {
		t.Helper()
		srv := newTestServer(t)
		declaredLoopbackProxy(t, srv)
		srv.SetMetricsScrapeToken(credential)
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.RemoteAddr = "127.0.0.1:5000"
		req.Header.Set("X-Forwarded-For", "203.0.113.7")
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
		}
		return rec
	}

	absent := refuse(t, "")
	wrong := refuse(t, "Bearer "+presented)
	if absent.Body.String() != wrong.Body.String() {
		t.Fatalf("a wrong credential is distinguishable from an absent one: %q vs %q", absent.Body.String(), wrong.Body.String())
	}
	for _, value := range []string{credential, presented} {
		if strings.Contains(wrong.Body.String(), value) {
			t.Fatalf("the refusal body repeated a credential: %q", wrong.Body.String())
		}
		if strings.Contains(logged.String(), value) {
			t.Fatal("a credential reached a log line")
		}
	}
}

// TestMetricsCredentialComparisonStaysConstantTime keeps the timing property
// from regressing to a comparison that returns as soon as two values differ.
// No black-box assertion can observe that from outside the process, so the
// guard is the source itself: the comparison must run through crypto/subtle,
// and must not fall back to a short-circuiting equality.
func TestMetricsCredentialComparisonStaysConstantTime(t *testing.T) {
	source, err := os.ReadFile("metrics_credential.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "subtle.ConstantTimeCompare") {
		t.Fatal("the scrape-credential comparison no longer runs through subtle.ConstantTimeCompare")
	}
	if strings.Contains(string(source), "bytes.Equal") {
		t.Fatal("the scrape-credential comparison fell back to a short-circuiting equality")
	}
}
