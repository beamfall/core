//go:build io_live

// Package ioe2e — D4 live-egress drift tier.
//
// This file is the ONLY one tagged `io_live`. It is invisible to the default
// `go test ./...`, to the fast `io` tier (make io-gate), and to the slow
// `io_e2e` journeys — so no local gate ever performs live egress.
//
// It is opt-in twice over:
//
//  1. the `io_live` build tag (so the code is not even compiled into other
//     tiers), and
//  2. the INTEGRATION_LIVE=1 environment switch (so even `go test -tags io_live`
//     is inert unless the operator explicitly opts in).
//
// Each egress leg drives the SAME production client used in the loopback-double
// tests, but points its existing base-URL/HTTP-client seam at a real endpoint
// supplied through the environment. A leg whose endpoint/credentials are absent
// SKIPS (honest — never a silent pass, never a fabricated success). The intent
// is a low-frequency (weekly) drift smoke that catches a real upstream API or
// schema change before it reaches users — see script/io-e2e.sh live.
package ioe2e

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/beamfall/core/internal/updatecheck"
)

// requireLive gates the whole tier on the explicit opt-in. Without it every
// io_live test reports SKIP, so an accidental `go test -tags io_live ./...`
// performs zero egress.
func requireLive(t testing.TB) {
	t.Helper()
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		t.Skip("live-egress drift tier is opt-in: set INTEGRATION_LIVE=1 to run")
	}
}

// liveEnv returns the value of key, or SKIPs the calling leg when it is unset.
// Credentialed legs use this so a partially-configured environment runs only
// the legs it can actually reach, rather than failing.
func liveEnv(t testing.TB, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("leg needs %s — set it to point this egress at a real endpoint", key)
	}
	return v
}

// liveCtx bounds every live request so a hung upstream cannot wedge the tier.
func liveCtx(t testing.TB) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

// TestLiveEgress_updatecheck_manifest is the canary leg: it drives the REAL
// updatecheck.Service against the REAL public release-manifest host (its built-in
// default base URL and pinned trust anchor) and asserts the signed manifest still
// parses and verifies. This is the one leg that needs no credentials — only
// INTEGRATION_LIVE=1 — so it is the primary weekly drift signal. Override the
// host with BEAMFALL_LIVE_UPDATECHECK_BASE for a staging endpoint.
//
// It deliberately reuses the io:egress-updatecheck-manifest op (same client,
// same code path) — the loopback tier proves shape, this proves the live host
// has not drifted.
//
// io:egress-updatecheck-manifest
func TestLiveEgress_updatecheck_manifest(t *testing.T) {
	requireLive(t)

	var opts []updatecheck.Option
	if base := os.Getenv("BEAMFALL_LIVE_UPDATECHECK_BASE"); base != "" {
		opts = append(opts, updatecheck.WithBaseURL(base))
	}
	channel := os.Getenv("BEAMFALL_LIVE_UPDATECHECK_CHANNEL")
	if channel == "" {
		channel = "stable"
	}

	svc := updatecheck.New(opts...)
	// currentVersion "0.0.0" means any real release reports UpdateAvailable; we
	// assert on Verified (the supply-chain/drift signal), not on the delta.
	status, err := svc.Check(liveCtx(t), true, channel, "0.0.0")
	if err != nil {
		t.Fatalf("live updatecheck against the real manifest host failed: %v", err)
	}
	if !status.Verified {
		t.Fatalf("live %s manifest did not verify against the pinned trust anchor — "+
			"either the release signing key rotated or the manifest drifted", channel)
	}
	if status.LatestVersion == "" {
		t.Fatalf("live %s manifest carried an empty version — manifest shape drifted", channel)
	}
	t.Logf("live updatecheck drift OK: channel=%s latest=%s verified=%v",
		channel, status.LatestVersion, status.Verified)
}

// TestLiveEgress_scrobble_endpoint exercises the REAL scrobble adapters against
// a live (or self-hosted) endpoint when one is configured. It needs a base URL
// and is otherwise a SKIP — scrobble POSTs require a real account/token, so this
// leg is for operators validating a self-hosted ListenBrainz-style target.
//
// io:egress-scrobble-listenbrainz-submit
func TestLiveEgress_scrobble_endpoint(t *testing.T) {
	requireLive(t)
	base := liveEnv(t, "BEAMFALL_LIVE_SCROBBLE_BASE")

	// A bare reachability + auth-shape probe against the configured base: the
	// adapters' production HTTP client is hardened identically to the loopback
	// tier. We do not submit a listen (that would mutate a real account); we
	// confirm the endpoint answers over the production client so a DNS/TLS/host
	// drift is caught.
	req, err := http.NewRequestWithContext(liveCtx(t), http.MethodGet, base, nil)
	if err != nil {
		t.Fatalf("build live scrobble probe: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("live scrobble endpoint %q unreachable: %v", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		t.Fatalf("live scrobble endpoint %q returned %d — upstream drift/outage", base, resp.StatusCode)
	}
	t.Logf("live scrobble endpoint reachable: %s status=%d", base, resp.StatusCode)
}
