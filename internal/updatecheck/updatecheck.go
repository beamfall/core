// Package updatecheck implements the opt-in, default-off version check.
// When the operator enables it (in the setup wizard's final
// step), the service fetches the latest published version for the chosen
// release channel (stable or beta), compares it to the running version, and
// reports an "update available" signal the Console renders as a banner.
//
// HARD CONSTRAINT — invariant #5, NO telemetry: the request sends NOTHING
// about this server. It is a plain GET of a static, public version manifest:
// no server identity, no library data, no analytics, no request body, and no
// identifying headers beyond a generic Beamfall User-Agent. The server only
// READS the latest version; it never reports anything outward. checkChannel
// below enforces this — it constructs a bare http.Request with no headers
// other than the generic UA and no body.
//
// The outbound GET is hardened with the same ADR-0012 §9 SSRF gate the scraper
// uses (https only, no literal IPs, no private/loopback/link-local ranges, no
// cross-host redirects, size + time caps), so a hostile or compromised
// manifest URL cannot pivot to internal services. The base URL is injectable
// so tests target an httptest server (via WithInsecureLoopback).
//
// SUPPLY-CHAIN GATE (PANEL-11, ADR-0013 §A): the fetched channel manifest is the
// SIGNED release manifest. Before an update is ever reported as available
// ("blessed"), its ed25519 signature is verified against the pinned trust anchor
// (internal/release). This is the in-server enforcement of "verify the signature
// before applying" — an unsigned, tampered, or wrong-key manifest is REFUSED
// (Verified=false, UpdateAvailable=false), so a compromised mirror cannot drive
// a blessed unattended upgrade. Verification is fail-closed.
package updatecheck

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/beamfall/core/internal/release"
)

const (
	// defaultBaseURL is the public release-manifest host. Each channel reads
	// <base>/<channel>.json. Overridable with WithBaseURL (tests point it at an
	// httptest server).
	//
	// The publisher behind this host is release-pipeline infrastructure that is
	// not stood up from this repository. Until it serves a signed manifest, an
	// enabled check against this default fails to fetch — and reports
	// State=StateUnavailable, which is a distinct answer from "you are up to
	// date" and can never be read as one. See
	// docs/release/update-manifest-availability.md for the decision and its
	// reasoning. The default-off gate means a normal server never makes this
	// request at all.
	defaultBaseURL = "https://updates.beamfall.com/channels"

	// requestTimeout caps the whole version-check request. A manifest is tiny;
	// a slow endpoint must not stall the Console status read.
	requestTimeout = 10 * time.Second

	// maxResponseBytes caps the manifest body — it is a one-line JSON document,
	// not a media payload.
	maxResponseBytes = 64 << 10

	// maxRedirects bounds redirect following; every hop is re-vetted and pinned
	// to the original host.
	maxRedirects = 3

	// userAgent is the ONLY identifying-ish header the request carries, and it
	// is generic: a product name + version, never a server id, install id, or
	// any per-instance value. Remote-source etiquette only (invariant #5).
	userAgent = "Beamfall/dev (+https://beamfall.com)"
)

// Status is the version-check result the Console reads. It is a
// pure projection of the opt-in config plus, when enabled, the fetched latest
// version — it carries nothing about library content.
type Status struct {
	// State is the authoritative outcome of the check and the ONLY field a
	// caller may route the "you are up to date" answer through. Its zero value
	// is StateUnavailable, so a Status that did not come from a verified fetch
	// never reads as reassuring. The three fields below are supporting detail,
	// not a substitute: an empty LatestVersion with UpdateAvailable=false is
	// produced by BOTH a current server and a failed check, and distinguishing
	// them is exactly what State is for.
	State State `json:"state"`
	// Enabled mirrors the opt-in config. When false, no outbound request was
	// made and the version fields below are empty.
	Enabled bool `json:"enabled"`
	// Channel is the release channel the check reads ("stable" or "beta").
	Channel string `json:"channel"`
	// CurrentVersion is the running server version.
	CurrentVersion string `json:"currentVersion"`
	// LatestVersion is the latest published version on Channel. Empty when the
	// check is disabled or the fetch failed.
	LatestVersion string `json:"latestVersion"`
	// UpdateAvailable is true only when a newer version than CurrentVersion was
	// fetched AND the manifest's signature verified. Equal or older latest
	// versions, any fetch failure, and any signature/verification failure all
	// report false (fail-closed). It is NOT the inverse of "up to date" — read
	// State for that.
	UpdateAvailable bool `json:"updateAvailable"`
	// Verified is true only when the fetched channel manifest carried a valid
	// signature over the pinned trust anchor. An unsigned or tampered
	// manifest reports Verified=false and is never blessed. Internal gate signal;
	// UpdateAvailable already subsumes it for the Console banner.
	Verified bool `json:"verified"`
}

// IsUpToDate reports whether this check actually established that the running
// version is current. It delegates to State, so a fetch failure, an absent
// manifest, a refused signature, and the default-off projection all report
// false — none of them is evidence of anything about the running version.
func (s Status) IsUpToDate() bool { return s.State.IsUpToDate() }

// Option configures a Service.
type Option func(*Service)

// WithBaseURL overrides the release-manifest base URL. Tests point it at an
// httptest server.
func WithBaseURL(base string) Option {
	return func(s *Service) { s.baseURL = strings.TrimRight(base, "/") }
}

// WithInsecureLoopback permits plain-http requests to loopback addresses so
// tests can target httptest servers.
//
// TESTS ONLY. Never pass this in production wiring: it relaxes the https
// requirement and the loopback denial for loopback targets only. Every other
// SSRF rule (private ranges, redirect pinning, caps) still holds.
func WithInsecureLoopback() Option {
	return func(s *Service) { s.insecureLoopback = true }
}

// WithVerificationKey overrides the trust anchor the supply-chain gate verifies
// against. Production wiring uses the pinned key baked into the binary; tests
// inject an ephemeral key so they can sign manifests without the real release
// secret.
//
// TESTS ONLY. Never pass this in production wiring — it replaces the pinned
// trust anchor.
func WithVerificationKey(pub ed25519.PublicKey) Option {
	return func(s *Service) { s.verifyKey = pub }
}

// Service performs the opt-in version check. Construct once with New and share;
// it holds no per-request state.
type Service struct {
	baseURL          string
	insecureLoopback bool
	verifyKey        ed25519.PublicKey // nil ⇒ use the pinned trust anchor (production)
	client           *http.Client
}

// verify runs the fail-closed supply-chain gate over a fetched manifest, using
// the injected test key when present and the pinned trust anchor otherwise.
func (s *Service) verify(m release.Manifest) error {
	if s.verifyKey != nil {
		return release.Verify(s.verifyKey, m)
	}
	return release.VerifyWithPinnedKey(m)
}

// New builds a Service with the ADR-0012 §9 hardened HTTP client.
func New(opts ...Option) *Service {
	s := &Service{baseURL: defaultBaseURL}
	for _, opt := range opts {
		opt(s)
	}
	dialer := &net.Dialer{
		Timeout: requestTimeout,
		// Control runs on the concrete IP the socket is about to connect to,
		// after DNS resolution — the DNS-rebinding-safe choke point.
		Control: s.controlDial,
	}
	s.client = &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			Proxy:                 nil, // never route the check through env proxies
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   requestTimeout,
			ResponseHeaderTimeout: requestTimeout,
		},
		CheckRedirect: s.checkRedirect,
	}
	return s
}

// Check returns the version-check Status for the given config and running
// version. When enabled is false it returns immediately WITHOUT making any
// outbound request (default-off; invariant #5) — only the config projection,
// State=StateDisabled.
//
// Every failure — an unreachable host, an absent or malformed manifest, a
// refused signature, a wrong-channel manifest — returns State=StateUnavailable
// alongside the error. That is a DISTINCT state from StateUpToDate, which this
// function assigns at exactly one place: after a manifest has been fetched,
// parsed, signature-verified against the trust anchor, and matched to the
// requested channel. There is no ordering of failures that reaches it.
func (s *Service) Check(ctx context.Context, enabled bool, channel, currentVersion string) (Status, error) {
	status := Status{
		State:          StateUnavailable,
		Enabled:        enabled,
		Channel:        channel,
		CurrentVersion: currentVersion,
	}
	if !enabled {
		// Inert until opted in: NO outbound request is made here. Disabled is
		// its own state — it asserts nothing about the running version.
		status.State = StateDisabled
		return status, nil
	}
	m, err := s.fetchManifest(ctx, channel)
	if err != nil {
		return status, err
	}
	// Supply-chain gate: refuse to bless an update whose signature does not
	// verify against the pinned trust anchor. Fail-closed — an
	// unsigned/old-shape or tampered manifest leaves Verified=false and
	// UpdateAvailable=false, and surfaces the refusal error for the caller to
	// log.
	if err := s.verify(m); err != nil {
		return status, fmt.Errorf("updatecheck: refused unverified %s manifest: %w", channel, err)
	}
	if m.Channel != channel {
		return status, fmt.Errorf("updatecheck: refused %q manifest for requested %q channel", m.Channel, channel)
	}
	// Past every gate: this is the ONLY reachable assignment of an answered
	// state, and it requires a verified, channel-matched manifest in hand.
	status.Verified = true
	status.LatestVersion = m.Version
	status.UpdateAvailable = isNewer(m.Version, currentVersion)
	status.State = StateUpToDate
	if status.UpdateAvailable {
		status.State = StateUpdateAvailable
	}
	return status, nil
}

// fetchManifest performs the plain GET of the SIGNED channel manifest. The
// request carries NO body and NO identifying headers beyond the generic
// User-Agent (invariant #5); the caller verifies the returned manifest's
// signature before trusting any field.
func (s *Service) fetchManifest(ctx context.Context, channel string) (release.Manifest, error) {
	manifestURL := fmt.Sprintf("%s/%s.json", s.baseURL, channel)
	parsed, err := url.Parse(manifestURL)
	if err != nil {
		return release.Manifest{}, fmt.Errorf("updatecheck: invalid manifest URL: %w", err)
	}
	if err := s.vetURL(parsed); err != nil {
		return release.Manifest{}, err
	}
	// A bare GET: no body, no cookies, no custom headers — only the generic UA.
	// This is the byte-for-byte guarantee that the request says nothing about
	// this server (invariant #5).
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return release.Manifest{}, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return release.Manifest{}, fmt.Errorf("updatecheck: fetch %s: %w", channel, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return release.Manifest{}, fmt.Errorf("updatecheck: %s manifest returned status %d", channel, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return release.Manifest{}, fmt.Errorf("updatecheck: read %s manifest: %w", channel, err)
	}
	if len(body) > maxResponseBytes {
		return release.Manifest{}, fmt.Errorf("updatecheck: %s manifest exceeds the %d-byte cap", channel, maxResponseBytes)
	}
	var m release.Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return release.Manifest{}, fmt.Errorf("updatecheck: parse %s manifest: %w", channel, err)
	}
	if strings.TrimSpace(m.Version) == "" {
		return release.Manifest{}, fmt.Errorf("updatecheck: %s manifest has no version", channel)
	}
	m.Version = strings.TrimSpace(m.Version)
	return m, nil
}
