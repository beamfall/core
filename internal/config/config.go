// Package config loads the small bootstrap configuration needed before the app starts.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/beamfall/core/internal/serverrole"
)

const qbittorrentRoutingHelp = `qBittorrent category->Library map, e.g. "inbox=lib-a,archive=lib-b"`

// Config contains process-level settings for a Beamfall server instance.
type Config struct {
	Addr       string
	DataDir    string
	ServerName string
	// NodeHostListen is the bind address of the node-facing mTLS SESSION
	// listener (ADR-0129 §5: the node dials the main). Empty (the default)
	// DISABLES it, so single-machine installs are unaffected; set via
	// BEAMFALL_NODEHOST_LISTEN to host Origin Nodes.
	NodeHostListen       string
	PublicMetrics        bool
	Discovery            DiscoveryConfig
	StreamTokenKey       []byte
	SeedDevelopmentData  bool
	SeedProfile          string
	ReviewDemoEnrollment ReviewDemoEnrollmentConfig
	// AllowConformanceSeed gates the test/dev-only `--seed=conformance` profile
	// (the deterministic corpus world over a fixed clock + fixed Stream-Token
	// key). It is set only by BEAMFALL_ALLOW_CONFORMANCE_SEED=1 so a normal
	// production serve can never boot the seeded world (hard invariant: seeding
	// stays test/dev-only).
	AllowConformanceSeed bool
	// AllowTestSeed gates the test/dev-only acceptance seed profile and
	// --seed-development-data. Both plant publicly known PIN/secret/admin-
	// password credentials, so they hard-refuse to boot unless this is set
	// (--allow-test-seed / BEAMFALL_ALLOW_TEST_SEED=1). A production config
	// leaves it unset, so a normal serve can never plant the known-credential
	// world (the known credentials are only safe on a non-production host).
	AllowTestSeed       bool
	AllowNetworkDataDir bool
	// TranscodeCap is the per-server concurrent-transcode cap (ADR-0010 §6
	// admission control; ADR-0013 resource governance).
	TranscodeCap int
	// TranscodeScratchDir is the ephemeral working directory for live HLS
	// transcodes. Empty input normalizes to <DataDir>/transcode.
	TranscodeScratchDir string
	// LogLevel controls the server's operator-facing slog threshold.
	LogLevel slog.Level
	// GenerationWorkers is the background generation worker-pool size and
	// BackgroundCPUBudget caps concurrent background generation slots (ADR-0013
	// §2 resource governance for weak hardware). GenerationNiceness is the Unix
	// niceness applied to background FFmpeg generation processes on Linux so a
	// transcode (run at normal priority) always wins the CPU. All three default
	// to honest low-power values suitable for Celeron/N100-class boxes; an
	// operator with stronger hardware raises them. Realtime transcode is never
	// niced or budgeted — it preempts generation (the existing invariant).
	GenerationWorkers   int
	BackgroundCPUBudget int
	GenerationNiceness  int
	// PlaybackCacheBudgetBytes is the disk budget the precomputed-playback-
	// rendition cache is governed by (ADR-0057 §4: disk-backed, budget-governed,
	// disposable). It defaults to DefaultPlaybackCacheBudgetBytes so precomputed
	// and composed-picture renditions work out of the box; an explicit 0 disables
	// the cache controls entirely — no eviction or composed-render Schedule is
	// seeded and the Console reports the cache unconfigured.
	// HotSegmentCacheBytes bounds the in-RAM hot-segment fan-out cache (§4: bounded
	// RAM only for hot segments, never whole-media residency); 0 keeps every
	// precomputed segment disk-served.
	PlaybackCacheBudgetBytes int64
	HotSegmentCacheBytes     int64

	// EventsRetentionDays is the age cutoff for time-based events.db compaction
	//: auth-attempt bookkeeping older than this is pruned by
	// the daily events-compaction job, alongside already-expired Stream-Token
	// ledger rows. Default 180 days; <= 0 falls back to the default. The audit log
	// (activity_entry, append-only per ADR-0012 §11) and engagement events (the
	// canonical projection source) are never pruned regardless of this value.
	EventsRetentionDays int

	// TrashEnabled is the server-side trash switch (hard invariant 6; ADR-0005:
	// trash ships ON by default). While disabled, autonomous destruction paths
	// skip; already-trashed items stay restorable and expired trash still
	// purges. Changing it requires a restart (v1 — no live toggle).
	TrashEnabled bool

	// PushRegistrationEnabled gates push-token registration.
	// Default OFF: APNs/FCM dispatch needs operator-configured credentials (an
	// Apple push key + a Firebase project) that are explicitly deferred, so the
	// server refuses to accept device push tokens until the operator opts in.
	// The in-app notification inbox (ADR-0039 §2.5 fallback) works fully
	// regardless of this flag — clients poll it on foreground. Flipping this on
	// today only enables the no-op registration seam (push.NoopRegistrar); no
	// real network dispatch exists yet.
	PushRegistrationEnabled bool

	// --- qBittorrent ingest ---
	// QBittorrentURL is the qBittorrent Web API base, e.g.
	// "http://localhost:8080". Empty disables ingest entirely (the poll job
	// becomes a no-op). Operator-configured; points at the operator's own
	// LAN/localhost service.
	QBittorrentURL string
	// QBittorrentUsername authenticates the Web API session. The password is
	// never config — it lives in <DataDir>/secrets/qbittorrent (0600,
	// invariant 5), resolved at app start, never logged.
	QBittorrentUsername string
	// QBittorrentRouting maps qBittorrent category -> Library ID. A category
	// absent here is unrouted: ignored and listed, never guessed (ADR-0020 §3 —
	// a misroute is a Reveal-contract incident). The Console owns the rich
	// editor later; this is the server-side seed.
	QBittorrentRouting map[string]string

	// RemoteAccess holds the two server-side remote-access paths (ADR-0012 §1
	// direct HTTPS + ACME; ADR-0018/0025 self-hosted relay carrier).
	RemoteAccess RemoteAccessConfig

	// HardwareAcceleration reports the video accelerators the startup probe
	// VERIFIED working, not a faith-based static list. It is not
	// loaded from env/file — the app runs the test-drive probe at boot and sets
	// this field on the live config so the Console (and logs) report only
	// accelerators whose trivial encode actually succeeded. The zero value
	// (empty Working) means software-only transcode, a normal outcome on a box
	// with no usable hardware encoder.
	HardwareAcceleration HardwareAccelReport

	// FFmpegHardeningShimPath is the beamfall binary the FFmpeg hardening shim
	// re-execs (ADR-0012 §10: per-process rlimits + Linux landlock confinement).
	// Like HardwareAcceleration it is NOT loaded from env/file — the real `serve`
	// entrypoint sets it to its own executable at boot so only the running server
	// hardens its ffmpeg spawns. It is intentionally left empty when app.New is
	// driven by a test binary, so a test never re-execs itself as a "shim" and
	// recurses into its own suite. Empty means un-hardened direct exec.
	FFmpegHardeningShimPath string
	// IntegrationWorkerHardeningShimPath is the beamfall binary the Integration
	// Worker shim re-execs (ADR-0058 §4: worker rlimits). It follows the same
	// entrypoint-only rule as FFmpegHardeningShimPath.
	IntegrationWorkerHardeningShimPath string

	// Role is the process authority posture (ADR-0123 §2, Origin Nodes). The
	// default is Main (the sole catalog authority + Stream-Token issuer). Setting
	// --attached makes this an Attached Origin Node: it performs byte-local work
	// (Identification/transcode/file-mutation) but refuses to author catalog rows,
	// issue Stream Tokens, or author engagement. It is operator infrastructure
	// config, not a person's device.
	Role serverrole.Role

	// JellyfinCompat is the dark-by-default compatibility route group. Admission
	// is bound to transport RemoteAddr; proxy headers are honored only for an
	// explicitly trusted proxy CIDR.
	JellyfinCompat JellyfinCompatConfig
}

type JellyfinCompatConfig struct {
	Enabled           bool
	AllowedCIDRs      []string
	TrustedProxyCIDRs []string
}

// HardwareAccelReport is the verified-working hardware-acceleration result the
// startup probe records. The accelerator names mirror the closed
// vocabulary the ffmpeg probe emits ("qsv", "vaapi", "nvenc", "videotoolbox");
// config holds them as plain strings so this bootstrap package stays decoupled
// from internal/ffmpeg. Working lists them in the order they were probed;
// empty means software-only.
type HardwareAccelReport struct {
	Working []string
	Results []HardwareAccelProbe
}

// HardwareAccelProbe is one sanitized accelerator probe status. Status is one
// of "unavailable", "configured-but-failed", or "working".
type HardwareAccelProbe struct {
	Accelerator string `json:"accelerator"`
	Status      string `json:"status"`
}

// ReviewDemoEnrollmentConfig enables the App Review demo enrollment grant.
// The key itself is a credential and never lives here; when enabled, app.New
// reads it from <DataDir>/secrets/review-demo-enrollment-key (0600).
type ReviewDemoEnrollmentConfig struct {
	Enabled   bool
	ProfileID string
}

// SoftwareOnly reports whether the probe verified no hardware accelerator, so
// transcode falls back to software (libx264). Not an error — software is always
// available.
func (r HardwareAccelReport) SoftwareOnly() bool {
	return len(r.Working) == 0
}

// RemoteAccessConfig configures how the server is reachable from outside the
// LAN. The two paths are independent and may both be off (the default — a
// LAN-only server). Neither path holds a secret in this struct: the relay
// carrier token lives in <DataDir>/secrets/relay-token (0600, invariant 5),
// resolved at use time and never logged (ADR-0018 byte-blindness).
type RemoteAccessConfig struct {
	DirectHTTPS DirectHTTPSConfig
	Relay       RelayConfig
}

// DirectHTTPSConfig is the direct-HTTPS + ACME path. When a
// PublicDomain is set the server obtains and serves an autocert-managed TLS
// certificate for it; the private key and ACME client live on this server and
// nowhere else.
type DirectHTTPSConfig struct {
	// PublicDomain is the FQDN ACME issues a certificate for, e.g.
	// "media.example.com". Empty disables direct HTTPS entirely (the server
	// keeps serving plain HTTP on Addr — LAN-only).
	PublicDomain string
	// ACMETermsAccepted records the operator's acceptance of the ACME CA's
	// Terms of Service. autocert refuses to register an ACME account without
	// it, so direct HTTPS stays disabled until the operator opts in — an
	// honest gate, never auto-accepted on the operator's behalf.
	ACMETermsAccepted bool
	// ACMECacheDir is where autocert persists issued certificates and the ACME
	// account key (0700). Empty defaults to <DataDir>/acme.
	ACMECacheDir string
}

// RelayConfig is the self-hosted relay carrier path (ADR-0018/0025). The
// server dials OUT to the operator's relay and registers its public host; the
// relay forwards inbound connections byte-blind (it never terminates TLS).
type RelayConfig struct {
	// Endpoint is the relay's carrier address the server dials out to, e.g.
	// "relay.example.com:7443". Empty disables the relay path. The carrier
	// token authenticating the registration is NOT here — it lives in
	// <DataDir>/secrets/relay-token (0600), resolved at use time.
	Endpoint string
	// PublicHost is the SNI hostname the relay routes inbound connections by
	// (the host clients reach the server at through the relay). Empty defaults
	// to DirectHTTPS.PublicDomain when that is set; otherwise the relay path is
	// misconfigured (validated at use time, not boot — a LAN server must boot
	// without remote access configured).
	PublicHost string
	// CAFile optionally pins the relay carrier CA. Empty uses system roots.
	CAFile string
	// Transport selects the carrier wire transport. "tcp" is the compatible
	// v1 TLS/yamux carrier; "quic" is the ADR-0018 v2 carrier with independent
	// per-stream flow control.
	Transport string
}

const (
	RelayTransportTCP  = "tcp"
	RelayTransportQUIC = "quic"
)

// DiscoveryConfig controls unauthenticated LAN service discovery.
type DiscoveryConfig struct {
	Enabled bool
}

// DefaultPlaybackCacheBudgetBytes is the default disk budget of the
// precomputed playback rendition cache: 20 GiB, roughly a thousand composed
// four-minute renditions at the composed rung's 1.5 Mb/s.
const DefaultPlaybackCacheBudgetBytes int64 = 20 << 30

// Default returns the local development configuration.
func Default() Config {
	return Config{
		Addr:         "127.0.0.1:8800",
		DataDir:      ".beamfall",
		ServerName:   "Beamfall",
		Discovery:    DiscoveryConfig{Enabled: true},
		TranscodeCap: 4,
		// Low-power defaults: one generation worker, one
		// background-CPU budget slot, and a niced (de-prioritized) FFmpeg so a
		// Celeron/N100-class box stays responsive. Stronger hardware raises these.
		GenerationWorkers:   1,
		BackgroundCPUBudget: 1,
		GenerationNiceness:  10,
		// A bounded default playback cache so composed-picture
		// renditions render without setup; LRU eviction keeps it under budget.
		PlaybackCacheBudgetBytes: DefaultPlaybackCacheBudgetBytes,
		EventsRetentionDays:      180,
		TrashEnabled:             true,
		JellyfinCompat: JellyfinCompatConfig{
			AllowedCIDRs: []string{"127.0.0.0/8", "::1/128"},
		},
	}
}

// Load resolves configuration from environment variables and command-line flags.
func Load(args []string, lookupEnv func(string) (string, bool)) (Config, error) {
	return LoadWithOutput(args, lookupEnv, io.Discard)
}

// LoadWithOutput resolves configuration and writes flag help to output.
func LoadWithOutput(args []string, lookupEnv func(string) (string, bool), output io.Writer) (Config, error) {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	if output == nil {
		output = io.Discard
	}

	cfg := Default()
	if value, ok := lookupEnv("BEAMFALL_ADDR"); ok && value != "" {
		cfg.Addr = value
	}
	if value, ok := lookupEnv("BEAMFALL_DATA_DIR"); ok && value != "" {
		cfg.DataDir = value
	}
	if value, ok := lookupEnv("BEAMFALL_SERVER_NAME"); ok && value != "" {
		cfg.ServerName = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv("BEAMFALL_NODEHOST_LISTEN"); ok && value != "" {
		cfg.NodeHostListen = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv("BEAMFALL_PUBLIC_METRICS"); ok && value != "" {
		publicMetrics, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("invalid BEAMFALL_PUBLIC_METRICS: %w", err)
		}
		cfg.PublicMetrics = publicMetrics
	}
	if value, ok := lookupEnv("BEAMFALL_DISCOVERY_ENABLED"); ok && value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("invalid BEAMFALL_DISCOVERY_ENABLED: %w", err)
		}
		cfg.Discovery.Enabled = enabled
	}
	if value, ok := lookupEnv("BEAMFALL_STREAM_TOKEN_KEY"); ok && value != "" {
		key, err := decodeKey(value)
		if err != nil {
			return Config{}, err
		}
		cfg.StreamTokenKey = key
	}
	if value, ok := lookupEnv("BEAMFALL_SEED_DEVELOPMENT_DATA"); ok && value != "" {
		seed, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("invalid BEAMFALL_SEED_DEVELOPMENT_DATA: %w", err)
		}
		cfg.SeedDevelopmentData = seed
	}
	if value, ok := lookupEnv("BEAMFALL_SEED"); ok && value != "" {
		cfg.SeedProfile = value
	}
	// Parsed after BEAMFALL_SEED so the contract-named env wins when both are set
	// (the Apple harness names --seed-profile / BEAMFALL_SEED_PROFILE verbatim).
	if value, ok := lookupEnv("BEAMFALL_SEED_PROFILE"); ok && value != "" {
		cfg.SeedProfile = value
	}
	if value, ok := lookupEnv("BEAMFALL_REVIEW_DEMO_ENROLLMENT_ENABLED"); ok && value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("invalid BEAMFALL_REVIEW_DEMO_ENROLLMENT_ENABLED: %w", err)
		}
		cfg.ReviewDemoEnrollment.Enabled = enabled
	}
	if value, ok := lookupEnv("BEAMFALL_REVIEW_DEMO_PROFILE_ID"); ok {
		cfg.ReviewDemoEnrollment.ProfileID = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv("BEAMFALL_ALLOW_CONFORMANCE_SEED"); ok && value != "" {
		allow, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("invalid BEAMFALL_ALLOW_CONFORMANCE_SEED: %w", err)
		}
		cfg.AllowConformanceSeed = allow
	}
	if value, ok := lookupEnv("BEAMFALL_ALLOW_TEST_SEED"); ok && value != "" {
		allow, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("invalid BEAMFALL_ALLOW_TEST_SEED: %w", err)
		}
		cfg.AllowTestSeed = allow
	}
	if value, ok := lookupEnv("BEAMFALL_ALLOW_NETWORK_DATA_DIR"); ok && value != "" {
		allow, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("invalid BEAMFALL_ALLOW_NETWORK_DATA_DIR: %w", err)
		}
		cfg.AllowNetworkDataDir = allow
	}

	if value, ok := lookupEnv("BEAMFALL_TRANSCODE_CAP"); ok && value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 {
			return Config{}, fmt.Errorf("invalid BEAMFALL_TRANSCODE_CAP %q: must be a positive integer", value)
		}
		cfg.TranscodeCap = limit
	}
	if value, ok := lookupEnv("BEAMFALL_TRANSCODE_TMP"); ok {
		cfg.TranscodeScratchDir = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv("BEAMFALL_LOG_LEVEL"); ok && value != "" {
		level, err := parseLogLevel(value)
		if err != nil {
			return Config{}, fmt.Errorf("invalid BEAMFALL_LOG_LEVEL %q: %w", value, err)
		}
		cfg.LogLevel = level
	}
	if value, ok := lookupEnv("BEAMFALL_GENERATION_WORKERS"); ok && value != "" {
		workers, err := strconv.Atoi(value)
		if err != nil || workers < 1 {
			return Config{}, fmt.Errorf("invalid BEAMFALL_GENERATION_WORKERS %q: must be a positive integer", value)
		}
		cfg.GenerationWorkers = workers
	}
	if value, ok := lookupEnv("BEAMFALL_BACKGROUND_CPU_BUDGET"); ok && value != "" {
		budget, err := strconv.Atoi(value)
		if err != nil || budget < 1 {
			return Config{}, fmt.Errorf("invalid BEAMFALL_BACKGROUND_CPU_BUDGET %q: must be a positive integer", value)
		}
		cfg.BackgroundCPUBudget = budget
	}
	if value, ok := lookupEnv("BEAMFALL_GENERATION_NICENESS"); ok && value != "" {
		niceness, err := strconv.Atoi(value)
		if err != nil || niceness < 0 || niceness > 19 {
			return Config{}, fmt.Errorf("invalid BEAMFALL_GENERATION_NICENESS %q: must be 0..19", value)
		}
		cfg.GenerationNiceness = niceness
	}
	if value, ok := lookupEnv("BEAMFALL_PLAYBACK_CACHE_BUDGET_BYTES"); ok && value != "" {
		budget, err := strconv.ParseInt(value, 10, 64)
		if err != nil || budget < 0 {
			return Config{}, fmt.Errorf("invalid BEAMFALL_PLAYBACK_CACHE_BUDGET_BYTES %q: must be a non-negative integer", value)
		}
		cfg.PlaybackCacheBudgetBytes = budget
	}
	if value, ok := lookupEnv("BEAMFALL_HOT_SEGMENT_CACHE_BYTES"); ok && value != "" {
		bytes, err := strconv.ParseInt(value, 10, 64)
		if err != nil || bytes < 0 {
			return Config{}, fmt.Errorf("invalid BEAMFALL_HOT_SEGMENT_CACHE_BYTES %q: must be a non-negative integer", value)
		}
		cfg.HotSegmentCacheBytes = bytes
	}
	if value, ok := lookupEnv("BEAMFALL_TRASH_ENABLED"); ok && value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("invalid BEAMFALL_TRASH_ENABLED: %w", err)
		}
		cfg.TrashEnabled = enabled
	}
	if value, ok := lookupEnv("BEAMFALL_PUSH_REGISTRATION_ENABLED"); ok && value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("invalid BEAMFALL_PUSH_REGISTRATION_ENABLED: %w", err)
		}
		cfg.PushRegistrationEnabled = enabled
	}
	if value, ok := lookupEnv("BEAMFALL_JELLYFIN_COMPAT_ENABLED"); ok && value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("invalid BEAMFALL_JELLYFIN_COMPAT_ENABLED: %w", err)
		}
		cfg.JellyfinCompat.Enabled = enabled
	}
	if value, ok := lookupEnv("BEAMFALL_JELLYFIN_COMPAT_CIDRS"); ok {
		cfg.JellyfinCompat.AllowedCIDRs = parseList(value)
	}
	if value, ok := lookupEnv("BEAMFALL_JELLYFIN_COMPAT_TRUSTED_PROXIES"); ok {
		cfg.JellyfinCompat.TrustedProxyCIDRs = parseList(value)
	}
	if value, ok := lookupEnv("BEAMFALL_QBITTORRENT_URL"); ok {
		cfg.QBittorrentURL = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv("BEAMFALL_QBITTORRENT_USERNAME"); ok {
		cfg.QBittorrentUsername = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv("BEAMFALL_QBITTORRENT_ROUTING"); ok {
		routing, err := parseRouting(value)
		if err != nil {
			return Config{}, err
		}
		cfg.QBittorrentRouting = routing
	}
	if value, ok := lookupEnv("BEAMFALL_PUBLIC_DOMAIN"); ok {
		cfg.RemoteAccess.DirectHTTPS.PublicDomain = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv("BEAMFALL_ACME_TERMS_ACCEPTED"); ok && value != "" {
		accepted, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("invalid BEAMFALL_ACME_TERMS_ACCEPTED: %w", err)
		}
		cfg.RemoteAccess.DirectHTTPS.ACMETermsAccepted = accepted
	}
	if value, ok := lookupEnv("BEAMFALL_ACME_CACHE_DIR"); ok {
		cfg.RemoteAccess.DirectHTTPS.ACMECacheDir = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv("BEAMFALL_RELAY_ENDPOINT"); ok {
		cfg.RemoteAccess.Relay.Endpoint = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv("BEAMFALL_RELAY_PUBLIC_HOST"); ok {
		cfg.RemoteAccess.Relay.PublicHost = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv("BEAMFALL_RELAY_CA"); ok {
		cfg.RemoteAccess.Relay.CAFile = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv("BEAMFALL_RELAY_TRANSPORT"); ok {
		cfg.RemoteAccess.Relay.Transport = strings.TrimSpace(value)
	}

	fs := flag.NewFlagSet("beamfall serve", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "listen address")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "data directory")
	fs.StringVar(&cfg.ServerName, "server-name", cfg.ServerName, "server name advertised to clients")
	fs.BoolVar(&cfg.PublicMetrics, "public-metrics", cfg.PublicMetrics, "allow /metrics from non-loopback clients (default false)")
	fs.BoolVar(&cfg.Discovery.Enabled, "discovery-enabled", cfg.Discovery.Enabled, "advertise this server with Bonjour/mDNS")
	fs.BoolVar(&cfg.SeedDevelopmentData, "seed-development-data", cfg.SeedDevelopmentData, "seed deterministic development catalog and credentials")
	fs.StringVar(&cfg.SeedProfile, "seed-profile", cfg.SeedProfile, "seed profile: development, demo, conformance, or acceptance (conformance/acceptance are test/dev-only)")
	fs.StringVar(&cfg.SeedProfile, "seed", cfg.SeedProfile, "back-compat alias for --seed-profile")
	fs.BoolVar(&cfg.ReviewDemoEnrollment.Enabled, "review-demo-enrollment-enabled", cfg.ReviewDemoEnrollment.Enabled, "enable App Review demo enrollment key from <data-dir>/secrets/review-demo-enrollment-key")
	fs.StringVar(&cfg.ReviewDemoEnrollment.ProfileID, "review-demo-profile-id", cfg.ReviewDemoEnrollment.ProfileID, "Profile ID used for App Review demo enrollment")
	fs.BoolVar(&cfg.AllowConformanceSeed, "allow-conformance-seed", cfg.AllowConformanceSeed, "permit the test/dev-only conformance seed profile")
	fs.BoolVar(&cfg.AllowTestSeed, "allow-test-seed", cfg.AllowTestSeed, "permit the test/dev-only acceptance seed profile and --seed-development-data (plants known credentials; never a production serve)")
	fs.BoolVar(&cfg.AllowNetworkDataDir, "allow-network-data-dir", cfg.AllowNetworkDataDir, "allow SQLite data directory on a network filesystem")
	fs.IntVar(&cfg.TranscodeCap, "transcode-cap", cfg.TranscodeCap, "maximum concurrent transcode sessions")
	fs.StringVar(&cfg.TranscodeScratchDir, "transcode-tmp", cfg.TranscodeScratchDir, "existing scratch directory for live HLS transcodes (empty = <data-dir>/transcode)")
	fs.TextVar(&cfg.LogLevel, "log-level", &cfg.LogLevel, "operator log level: debug, info, warn, or error")
	fs.IntVar(&cfg.GenerationWorkers, "generation-workers", cfg.GenerationWorkers, "background generation worker-pool size (low-power default 1)")
	fs.IntVar(&cfg.BackgroundCPUBudget, "background-cpu-budget", cfg.BackgroundCPUBudget, "concurrent background generation slot budget (low-power default 1)")
	fs.IntVar(&cfg.GenerationNiceness, "generation-niceness", cfg.GenerationNiceness, "Unix niceness 0..19 for background FFmpeg generation on Linux (low-power default 10)")
	fs.Int64Var(&cfg.PlaybackCacheBudgetBytes, "playback-cache-budget-bytes", cfg.PlaybackCacheBudgetBytes, "disk budget for the precomputed playback rendition cache in bytes (default 20 GiB; 0 disables the cache, ADR-0057)")
	fs.Int64Var(&cfg.HotSegmentCacheBytes, "hot-segment-cache-bytes", cfg.HotSegmentCacheBytes, "RAM budget for the hot HLS segment fan-out cache in bytes (0 keeps segments disk-served, ADR-0057)")
	fs.BoolVar(&cfg.TrashEnabled, "trash-enabled", cfg.TrashEnabled, "server-side trash (disabling skips autonomous deletion; restart required)")
	fs.BoolVar(&cfg.PushRegistrationEnabled, "push-registration-enabled", cfg.PushRegistrationEnabled, "accept device push tokens (ADR-0039; APNs/FCM dispatch deferred — inbox polling works regardless)")
	fs.BoolVar(&cfg.JellyfinCompat.Enabled, "jellyfin-compat-enabled", cfg.JellyfinCompat.Enabled, "enable the local Jellyfin API compatibility route group")
	var jellyfinCompatCIDRsFlag string
	var jellyfinCompatTrustedProxiesFlag string
	fs.StringVar(&jellyfinCompatCIDRsFlag, "jellyfin-compat-cidrs", "", "comma-separated client CIDRs allowed to reach compatibility routes")
	fs.StringVar(&jellyfinCompatTrustedProxiesFlag, "jellyfin-compat-trusted-proxies", "", "comma-separated reverse-proxy CIDRs whose single X-Forwarded-For hop is trusted")
	fs.StringVar(&cfg.QBittorrentURL, "qbittorrent-url", cfg.QBittorrentURL, "qBittorrent Web API base URL (empty disables ingest)")
	fs.StringVar(&cfg.QBittorrentUsername, "qbittorrent-username", cfg.QBittorrentUsername, "qBittorrent Web API username (password lives in the secure store)")
	var routingFlag string
	fs.StringVar(&routingFlag, "qbittorrent-routing", "", qbittorrentRoutingHelp)
	fs.StringVar(&cfg.RemoteAccess.DirectHTTPS.PublicDomain, "public-domain", cfg.RemoteAccess.DirectHTTPS.PublicDomain, "public FQDN to obtain an ACME TLS cert for (empty = LAN-only HTTP)")
	fs.BoolVar(&cfg.RemoteAccess.DirectHTTPS.ACMETermsAccepted, "acme-terms-accepted", cfg.RemoteAccess.DirectHTTPS.ACMETermsAccepted, "accept the ACME CA Terms of Service (required before any cert is issued)")
	fs.StringVar(&cfg.RemoteAccess.DirectHTTPS.ACMECacheDir, "acme-cache-dir", cfg.RemoteAccess.DirectHTTPS.ACMECacheDir, "directory for autocert's cert/account cache (empty = <data-dir>/acme)")
	fs.StringVar(&cfg.RemoteAccess.Relay.Endpoint, "relay-endpoint", cfg.RemoteAccess.Relay.Endpoint, "self-hosted relay carrier address to dial out to (empty disables the relay path)")
	fs.StringVar(&cfg.RemoteAccess.Relay.PublicHost, "relay-public-host", cfg.RemoteAccess.Relay.PublicHost, "SNI host the relay routes inbound connections by (empty = public-domain)")
	fs.StringVar(&cfg.RemoteAccess.Relay.CAFile, "relay-ca", cfg.RemoteAccess.Relay.CAFile, "relay carrier CA PEM file (empty = system roots)")
	fs.StringVar(&cfg.RemoteAccess.Relay.Transport, "relay-transport", cfg.RemoteAccess.Relay.Transport, "relay carrier transport: tcp or quic (default tcp)")
	attached := cfg.Role == serverrole.Attached
	fs.BoolVar(&attached, "attached", attached, "run as an attached Origin Node (ADR-0123): performs byte-local work but never authors catalog rows, issues Stream Tokens, or authors engagement (default: main server)")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if attached {
		cfg.Role = serverrole.Attached
	} else {
		cfg.Role = serverrole.Main
	}
	if routingFlag != "" {
		routing, err := parseRouting(routingFlag)
		if err != nil {
			return Config{}, err
		}
		cfg.QBittorrentRouting = routing
	}
	if jellyfinCompatCIDRsFlag != "" {
		cfg.JellyfinCompat.AllowedCIDRs = parseList(jellyfinCompatCIDRsFlag)
	}
	if jellyfinCompatTrustedProxiesFlag != "" {
		cfg.JellyfinCompat.TrustedProxyCIDRs = parseList(jellyfinCompatTrustedProxiesFlag)
	}
	if fs.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	cfg, err := validate(cfg)
	if err != nil {
		return Config{}, err
	}
	if len(cfg.StreamTokenKey) == 0 {
		key, err := loadOrCreateStreamTokenKey(cfg.DataDir)
		if err != nil {
			return Config{}, err
		}
		cfg.StreamTokenKey = key
	}
	cfg.RemoteAccess = cfg.RemoteAccess.normalize(cfg.DataDir)
	return cfg, nil
}

// normalize resolves the relative defaults that depend on DataDir: the ACME
// cache directory falls back to <DataDir>/acme, and the relay's routed public
// host falls back to the direct-HTTPS PublicDomain when not set explicitly
// (the common single-domain case). Both are pure derivations — no I/O, no
// secret access.
func (r RemoteAccessConfig) normalize(dataDir string) RemoteAccessConfig {
	if r.DirectHTTPS.PublicDomain != "" && r.DirectHTTPS.ACMECacheDir == "" {
		r.DirectHTTPS.ACMECacheDir = filepath.Join(dataDir, "acme")
	}
	if r.Relay.Endpoint != "" && r.Relay.PublicHost == "" {
		r.Relay.PublicHost = r.DirectHTTPS.PublicDomain
	}
	return r
}

// DirectHTTPSEnabled reports whether the direct-HTTPS + ACME path is active: a
// public domain set AND the ACME terms accepted. A domain without accepted
// terms is honestly NOT enabled — autocert would refuse to register, so the
// server stays on plain HTTP and the Console shows the gate.
func (r RemoteAccessConfig) DirectHTTPSEnabled() bool {
	return r.DirectHTTPS.PublicDomain != "" && r.DirectHTTPS.ACMETermsAccepted
}

// RelayConfigured reports whether the relay carrier path has an endpoint set.
// It does NOT assert the token secret exists — that is resolved (and may fail)
// at dial time, so a missing secret surfaces as an honest connection error
// rather than hiding the configured intent.
func (r RemoteAccessConfig) RelayConfigured() bool {
	return r.Relay.Endpoint != ""
}

func validate(cfg Config) (Config, error) {
	if cfg.Addr == "" {
		return Config{}, errors.New("addr is required")
	}
	if _, _, err := net.SplitHostPort(cfg.Addr); err != nil {
		return Config{}, fmt.Errorf("invalid addr %q: %w", cfg.Addr, err)
	}
	cfg.ServerName = strings.TrimSpace(cfg.ServerName)
	if cfg.ServerName == "" {
		return Config{}, errors.New("server-name is required")
	}
	if cfg.DataDir == "" {
		return Config{}, errors.New("data-dir is required")
	}
	if cfg.TranscodeCap < 1 {
		return Config{}, errors.New("transcode-cap must be at least 1")
	}
	cfg.TranscodeScratchDir = strings.TrimSpace(cfg.TranscodeScratchDir)
	if cfg.TranscodeScratchDir != "" {
		if err := validateScratchDir(cfg.TranscodeScratchDir); err != nil {
			return Config{}, err
		}
	} else {
		cfg.TranscodeScratchDir = filepath.Join(cfg.DataDir, "transcode")
	}
	if cfg.GenerationWorkers < 1 {
		return Config{}, errors.New("generation-workers must be at least 1")
	}
	if cfg.BackgroundCPUBudget < 1 {
		return Config{}, errors.New("background-cpu-budget must be at least 1")
	}
	if cfg.GenerationNiceness < 0 || cfg.GenerationNiceness > 19 {
		return Config{}, errors.New("generation-niceness must be between 0 and 19")
	}
	if cfg.PlaybackCacheBudgetBytes < 0 {
		return Config{}, errors.New("playback-cache-budget-bytes must be non-negative")
	}
	if cfg.HotSegmentCacheBytes < 0 {
		return Config{}, errors.New("hot-segment-cache-bytes must be non-negative")
	}
	for _, cidr := range append(append([]string(nil), cfg.JellyfinCompat.AllowedCIDRs...), cfg.JellyfinCompat.TrustedProxyCIDRs...) {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return Config{}, fmt.Errorf("invalid jellyfin compatibility CIDR %q: %w", cidr, err)
		}
	}
	if cfg.JellyfinCompat.Enabled && len(cfg.JellyfinCompat.AllowedCIDRs) == 0 {
		return Config{}, errors.New("jellyfin compatibility mode requires at least one allowed CIDR")
	}
	cfg.RemoteAccess.DirectHTTPS.PublicDomain = strings.TrimSpace(cfg.RemoteAccess.DirectHTTPS.PublicDomain)
	cfg.RemoteAccess.DirectHTTPS.ACMECacheDir = strings.TrimSpace(cfg.RemoteAccess.DirectHTTPS.ACMECacheDir)
	cfg.RemoteAccess.Relay.Endpoint = strings.TrimSpace(cfg.RemoteAccess.Relay.Endpoint)
	cfg.RemoteAccess.Relay.PublicHost = strings.TrimSpace(cfg.RemoteAccess.Relay.PublicHost)
	cfg.RemoteAccess.Relay.CAFile = strings.TrimSpace(cfg.RemoteAccess.Relay.CAFile)
	cfg.RemoteAccess.Relay.Transport = strings.TrimSpace(cfg.RemoteAccess.Relay.Transport)
	switch cfg.RemoteAccess.Relay.Transport {
	case "":
		cfg.RemoteAccess.Relay.Transport = RelayTransportTCP
	case RelayTransportTCP, RelayTransportQUIC:
	default:
		return Config{}, fmt.Errorf("unknown relay transport %q (want %q or %q)", cfg.RemoteAccess.Relay.Transport, RelayTransportTCP, RelayTransportQUIC)
	}
	cfg.ReviewDemoEnrollment.ProfileID = strings.TrimSpace(cfg.ReviewDemoEnrollment.ProfileID)
	if cfg.ReviewDemoEnrollment.Enabled && cfg.ReviewDemoEnrollment.ProfileID == "" {
		cfg.ReviewDemoEnrollment.ProfileID = "profile-demo-default"
	}
	// --seed-development-data plants known dev credentials, so it is test/dev-only
	// and hard-refuses under a production config (AllowTestSeed unset). This is a
	// separate boolean field from the named seed profile, so it must be guarded
	// before the SeedProfile switch (cfg.SeedProfile is "" when only this is set).
	if cfg.SeedDevelopmentData && !cfg.AllowTestSeed {
		return Config{}, errors.New("--seed-development-data is test/dev-only: set --allow-test-seed or BEAMFALL_ALLOW_TEST_SEED=1")
	}
	switch cfg.SeedProfile {
	case "", "development", "demo":
	case "conformance":
		// Test/dev-only: the conformance seed boots a deterministic, publicly
		// known world (fixed clock + fixed Stream-Token key) and must never be
		// reachable from a normal production serve.
		if !cfg.AllowConformanceSeed {
			return Config{}, errors.New("seed profile \"conformance\" is test/dev-only: set --allow-conformance-seed or BEAMFALL_ALLOW_CONFORMANCE_SEED=1")
		}
	case "acceptance":
		// Test/dev-only: the acceptance seed plants publicly known PIN/secret/
		// admin-password credentials (the Apple e2e fixture world) and must never
		// be reachable from a normal production serve.
		if !cfg.AllowTestSeed {
			return Config{}, errors.New("seed profile \"acceptance\" is test/dev-only: set --allow-test-seed or BEAMFALL_ALLOW_TEST_SEED=1")
		}
	default:
		return Config{}, fmt.Errorf("unknown seed profile %q", cfg.SeedProfile)
	}
	return cfg, nil
}

func parseLogLevel(value string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToUpper(strings.TrimSpace(value)))); err != nil {
		return 0, err
	}
	return level, nil
}

func validateScratchDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("invalid transcode scratch dir %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("invalid transcode scratch dir %q: not a directory", dir)
	}
	probe, err := os.CreateTemp(dir, ".beamfall-write-test-*")
	if err != nil {
		return fmt.Errorf("invalid transcode scratch dir %q: not writable: %w", dir, err)
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("invalid transcode scratch dir %q: write test failed: %w", dir, err)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("invalid transcode scratch dir %q: write test cleanup failed: %w", dir, err)
	}
	return nil
}

func loadOrCreateStreamTokenKey(dataDir string) ([]byte, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "stream-token.key")
	if data, err := os.ReadFile(path); err == nil {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return nil, statErr
		}
		if info.Mode().Perm() != 0o600 {
			return nil, fmt.Errorf("%s must be mode 0600", path)
		}
		return decodeKey(string(data))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	encoded := base64.RawStdEncoding.EncodeToString(key) + "\n"
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// parseRouting parses a "category=library,category2=library2" string into a
// category->Library map. Whitespace around tokens is trimmed; a
// malformed pair, an empty category, or an empty Library is a hard error rather
// than a silent drop — a missing route must be visible, never guessed.
func parseRouting(value string) (map[string]string, error) {
	routing := map[string]string{}
	for _, pair := range strings.Split(value, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		category, library, ok := strings.Cut(pair, "=")
		category = strings.TrimSpace(category)
		library = strings.TrimSpace(library)
		if !ok || category == "" || library == "" {
			return nil, fmt.Errorf("invalid qbittorrent routing entry %q: expected category=library", pair)
		}
		if existing, dup := routing[category]; dup && existing != library {
			return nil, fmt.Errorf("conflicting qbittorrent routing for category %q", category)
		}
		routing[category] = library
	}
	if len(routing) == 0 {
		return nil, nil
	}
	return routing, nil
}

func parseList(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func decodeKey(value string) ([]byte, error) {
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("invalid stream token key: %w", err)
	}
	if len(key) < 32 {
		return nil, errors.New("stream token key must be at least 32 bytes")
	}
	return key, nil
}
