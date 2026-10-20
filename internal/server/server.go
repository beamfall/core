// Package server wires the HTTP surface and records every mounted route.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/beam"
	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/moderation"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/reveal"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/web"
)

// maxControlBody caps the request body of the JSON control endpoints. These
// bodies are small (enrollment, pairing, playback-info, promote/logout), so a
// 1 MiB ceiling is generous while preventing an unauthenticated client from
// forcing unbounded allocation by streaming a giant body (DoS). It is NOT
// applied to byte/stream routes served through mediahttp.
const maxControlBody = 1 << 20 // 1 MiB

const plainHTTPPublicWarningContextKey = "beamfall.plainHTTPPublicWarning"

// Route is a mounted HTTP route.
type Route struct {
	Method      string
	Pattern     string
	RevealClass reveal.SurfaceClass
}

// Server is the HTTP handler for the Beamfall process.
type Server struct {
	mux               *http.ServeMux
	handler           http.Handler
	routes            []Route
	accessClasses     map[string]AccessClass
	manifest          *manifest.Service
	playback          *playback.Service
	beam              *beam.Service
	pairing           *pairing.Service
	graph             http.Handler
	unmatchedArtwork  unmatchedArtworkCreator
	media             *mediahttp.Handler
	auth              *auth.Service
	jobStats          JobStatsProvider
	jobQueue          JobQueueAgeProvider
	events            EventStore
	pushNotifications PushNotificationStore
	v1                V1Store
	v1ScanEnqueue     V1ScanEnqueuer
	backupEncryptor   store.BackupIngestEncryptor
	readiness         ReadinessProvider
	sessions          SessionCountProvider
	transcode         TranscodeCountProvider
	library           LibrarySizeProvider
	publicMetrics     bool
	// metricsScrapeDigest is the SHA-256 of this run's /metrics scrape
	// credential; nil means none was established, which refuses every bearer.
	metricsScrapeDigest []byte
	testFaults          atomic.Pointer[testFaults]
	buildSHA            string
	seedProfile         string
	seedVersion         string
	capabilities        []string
	sourceOfferURL      string
	proxyTee            http.HandlerFunc
	packAuthority       http.HandlerFunc
	packManifest        http.HandlerFunc
	packContent         http.HandlerFunc
	packKey             http.HandlerFunc
	packStatus          http.HandlerFunc
	moderation          *moderation.Service
	observability       ObservabilityController
	baseURL             *url.URL
	knownProxies        *KnownProxies
	artworkImport       ArtworkImporter
	artworkApply        artworkApplyStore
	compat              *compatState
	compatRefusalDir    string
	deviceSurfaces      deviceSurfaceStore
}

type relayedRequestKey struct{}

// ContextWithRelayedRequest marks requests accepted by the relay-only backend
// listener. It is process-local; clients cannot spoof it with headers.
func ContextWithRelayedRequest(ctx context.Context) context.Context {
	return context.WithValue(ctx, relayedRequestKey{}, true)
}

func requestFromRelay(r *http.Request) bool {
	v, _ := r.Context().Value(relayedRequestKey{}).(bool)
	return v
}

// RequestFromRelay reports the process-local relay marker to separately
// mounted, removable route groups. Clients cannot forge the context value.
func RequestFromRelay(r *http.Request) bool {
	return requestFromRelay(r)
}

// requestIDKey is the per-request correlation ID stored in the request context.
// The ID is generated at the ServeHTTP boundary, written as X-Beamfall-Request-Id
// on the response, and included in every 5xx log line so operator log streams
// can correlate a server error with a specific client request.
type requestIDKey struct{}

// requestIDFromContext returns the correlation ID for the request, or "" when
// none has been attached (e.g. internal synthetic requests in tests).
func requestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey{}).(string)
	return v
}

// requestIDHeader is the response header that carries the per-request correlation
// ID so clients can include it in support reports.
const requestIDHeader = "X-Beamfall-Request-Id"

// newRequestID generates a short opaque hex correlation ID. 8 bytes of randomness
// gives 1-in-2^64 collision odds per server restart — sufficient for local ops logs.
func newRequestID() string {
	return fmt.Sprintf("%016x", rand.Uint64())
}

// JobStatsProvider supplies ops counters for /metrics.
type JobStatsProvider interface {
	JobStats(context.Context) ([]store.JobStatsRow, error)
}

type idempotentLogoutJobStats struct {
	JobStatsProvider
	revoker sessionRevoker
}

func (p idempotentLogoutJobStats) TerminateSession(ctx context.Context, id store.SessionID) error {
	err := p.revoker.TerminateSession(ctx, id)
	if errors.Is(err, store.ValidationError("session not found")) {
		return nil
	}
	return err
}

func makeLogoutIdempotent(provider JobStatsProvider) JobStatsProvider {
	revoker, ok := provider.(sessionRevoker)
	if !ok {
		return provider
	}
	return idempotentLogoutJobStats{JobStatsProvider: provider, revoker: revoker}
}

// JobQueueAgeProvider supplies the oldest-waiting-job age for ADR-0146 §8's
// job-queue-age SLI. A nil provider omits the gauge rather than fabricating one.
type JobQueueAgeProvider interface {
	OldestQueuedJobAgeMs(context.Context, time.Time) (int64, bool, error)
}

// ReadinessProvider answers the /health readiness probe: nil means the store is
// reachable and migrated, a non-nil error names why it is not.
type ReadinessProvider interface {
	Ready(context.Context) error
}

// SessionCountProvider supplies the active-playback-session gauge for /metrics.
type SessionCountProvider interface {
	ActiveSessionCount(context.Context, time.Time) (int, error)
}

// TranscodeCountProvider supplies the active-realtime-transcode gauge for
// /metrics (the resource governor's live count).
type TranscodeCountProvider interface {
	ActiveTranscodes() int
}

// LibrarySizeProvider supplies the total-Media gauge for /metrics.
type LibrarySizeProvider interface {
	LibraryMediaTotal(context.Context) (int, error)
}

type ObservabilityController interface {
	RecordGauge(context.Context, string, int64, map[string]string) error
	PrometheusText(*http.Request) string
}

// Option configures a Server at construction.
type Option func(*Server)

// WithUnmatchedArtworkCreator supplies the command capability used by the
// unmatched-artwork upload route independently of the GraphQL HTTP transport.
func WithUnmatchedArtworkCreator(creator unmatchedArtworkCreator) Option {
	return func(s *Server) { s.unmatchedArtwork = creator }
}

// WithCompatRefusalJournalDir supplies the independent refusal-only journal
// directory used when the catalog cannot accept either invalidation write.
func WithCompatRefusalJournalDir(path string) Option {
	return func(s *Server) { s.compatRefusalDir = strings.TrimSpace(path) }
}

// New creates an HTTP server with every route mounted through the recording helper.
func New(manifests *manifest.Service, playbackInfo *playback.Service, beamService *beam.Service, pairingService *pairing.Service, graphHandler http.Handler, mediaHandler *mediahttp.Handler, authService *auth.Service, jobStats JobStatsProvider, opts ...Option) *Server {
	baseURL, err := parseBaseURL(os.Getenv("BEAMFALL_BASE_URL"))
	if err != nil {
		panic(fmt.Errorf("invalid BEAMFALL_BASE_URL: %w", err))
	}
	knownProxies, err := ParseKnownProxies(
		os.Getenv("BEAMFALL_KNOWN_PROXIES"),
		strings.EqualFold(strings.TrimSpace(os.Getenv("BEAMFALL_ALLOW_BROAD_KNOWN_PROXIES")), "true"),
	)
	if err != nil {
		panic(fmt.Errorf("invalid BEAMFALL_KNOWN_PROXIES: %w", err))
	}
	defaultCapabilities := capabilities.EnabledIDs(nil)
	s := &Server{
		mux:           http.NewServeMux(),
		accessClasses: make(map[string]AccessClass),
		manifest:      manifests,
		playback:      playbackInfo,
		beam:          beamService,
		pairing:       pairingService,
		graph:         graphHandler,
		media:         mediaHandler,
		auth:          authService,
		jobStats:      jobStats,
		buildSHA:      detectBuildSHA(),
		capabilities:  append([]string(nil), defaultCapabilities...),
		baseURL:       baseURL,
		knownProxies:  knownProxies,
	}
	for _, opt := range opts {
		opt(s)
	}
	s.compat = newCompatState(s.buildSHA, s.capabilities)
	s.bindCompatStreamTokens()
	s.jobStats = makeLogoutIdempotent(s.jobStats)
	if knownProxies != nil {
		slog.Info("known proxies enabled", "cidrs", knownProxies.String())
	}
	// The GraphQL endpoint is wired in as an opaque http.Handler (no
	// server→graph import, ARCHITECTURE.md §4 "no transport leaks"); this
	// structural assertion recovers just the one exported capability the
	// artwork upload route needs. graph.Handler
	// already satisfies it via PrepareUploadedArtwork.
	if importer, ok := graphHandler.(ArtworkImporter); ok {
		s.artworkImport = importer
	}
	if applyStore, ok := jobStats.(artworkApplyStore); ok {
		s.artworkApply = applyStore
	}
	if deviceSurfaces, ok := jobStats.(deviceSurfaceStore); ok {
		s.deviceSurfaces = deviceSurfaces
	}
	s.handle(http.MethodGet, "/health", "", s.health)
	s.handle(http.MethodGet, "/metrics", reveal.Aggregate, s.metrics)
	// AGPL §13 source offer for the exact running release. The redirect target
	// is injected at build time through capabilities.SourceOfferURL and is never
	// accepted from a request. Development builds with no offer fail closed.
	s.handle(http.MethodGet, "/source", "", s.sourceOffer)
	s.handle(http.MethodPost, "/api/graphql", reveal.ContentJSON, s.graphQL)
	s.handle(http.MethodPost, "/api/compat/handshake", reveal.ContentJSON, s.compatHandshake)
	s.handle(http.MethodPost, createMediaArtworkRoutePattern, reveal.ContentJSON, s.createMediaWithArtwork)
	s.handle(http.MethodGet, "/api/manifest", reveal.ContentJSON, s.manifestDocument)
	s.handle(http.MethodPost, "/api/playback-info", reveal.ContentJSON, s.playbackInfo)
	s.handle(http.MethodPost, "/api/preview-info", reveal.ContentJSON, s.previewInfo)
	s.handle(http.MethodPost, "/api/beam/offers", reveal.ContentJSON, s.createBeamOffer)
	s.handle(http.MethodPost, "/api/beam/claims", reveal.ContentJSON, s.claimBeamOffer)
	s.handle(http.MethodPost, "/api/pairing-codes", reveal.ContentJSON, s.createPairingCode)
	s.handle(http.MethodPost, "/api/enroll", reveal.ContentJSON, s.enrollDevice)
	// Guideline 1.2 UGC moderation (NATIVEMGR-4, apple ADR-0004 §4): in-app
	// content reporting + per-Profile content blocking for the self-hosted
	// client. These are VIEWER content-safety surfaces consumed by the client
	// (like /api/playback-info), NOT management/Console routes, so they are
	// deliberately absent from tools/mgmt-manifest managementHTTPRoutes. The
	// JSON write routes carry the ContentJSON Reveal class; /api/moderation/
	// contact returns only the published contact (no content) so it has no
	// Reveal class and is allowlisted in conformance/route_test.go.
	s.handle(http.MethodPost, "/api/moderation/report", reveal.ContentJSON, s.moderationReport)
	s.handle(http.MethodPost, "/api/moderation/block", reveal.ContentJSON, s.moderationBlock)
	s.handle(http.MethodGet, "/api/moderation/blocks", reveal.ContentJSON, s.moderationBlocks)
	s.handle(http.MethodGet, "/api/moderation/contact", "", s.moderationContact)
	s.handle(http.MethodGet, "/api/push/notifications/{id}", reveal.ContentJSON, s.pushNotification)
	s.handle(http.MethodGet, "/api/media/direct/", reveal.ContentBytes, s.media.Direct)
	s.handle(http.MethodGet, "/api/media/artwork-candidate/", reveal.ContentBytes, s.media.ArtworkCandidate)
	s.handle(http.MethodGet, "/api/media/artwork/", reveal.ContentBytes, s.media.Artwork)
	// Create-Media artwork upload for a hand-authored, no-match Media
	//: the binary-upload seam Core lacked —
	// artwork previously entered only by sourceURL. Session/CSRF/manager-gated
	// like the other web-Console write routes; the multipart body is excluded
	// from the JSON-Content-Type check below, mirroring "/v1/backups/media".
	s.handle(http.MethodPost, artworkUploadPattern, reveal.ContentJSON, s.uploadArtwork)
	s.handle(http.MethodGet, "/api/entity/artwork/", reveal.ContentBytes, s.media.EntityArtwork)
	s.handle(http.MethodGet, "/api/media/hls/", reveal.ContentBytes, s.media.HLS)
	s.handle(http.MethodGet, "/api/media/assets/", reveal.ContentBytes, s.media.Assets)
	s.handle(http.MethodGet, "/api/media/subtitle/", reveal.ContentBytes, s.media.Subtitles)
	// Sidecar caption track: an external .srt/.vtt file
	// discovered next to the media file, served as a WebVTT sidecar under its own
	// Stream-Token target. Serves media bytes, so it joins the ContentBytes
	// Reveal class like the muxed-subtitle route above.
	s.handle(http.MethodGet, "/api/media/sidecar/", reveal.ContentBytes, s.media.Sidecars)
	// The subtitle MUTATION surface: the write halves of the
	// pinned subtitle choice and the offset nudge, whose read halves are the
	// playback pre-selection and the sidecar route's server-side cue shift.
	// Unlike the byte routes above these are Profile-SESSION writes carrying
	// JSON, not Stream-Token byte reads, so they take the ContentJSON Reveal
	// class like the other per-Profile command routes. They live on the media
	// handler because the nudge is keyed on the sidecar's own track fingerprint,
	// which only that package derives.
	s.handle(http.MethodPost, "/api/media/subtitle-pin", reveal.ContentJSON, s.media.SetSubtitlePin)
	s.handle(http.MethodPost, "/api/media/subtitle-offset", reveal.ContentJSON, s.media.SetSubtitleOffset)
	// Proxy-tee stream of a provisional Media: the server ranges
	// the origin enclosure to the client through this Stream-Token'd endpoint and
	// tees the bytes to disk, then promotes the copy in place on completion. It
	// serves media bytes, so it joins the ContentBytes Reveal class like Direct.
	// Always mounted; an unwired proxy-tee service degrades to 503 (proxyStream).
	s.handle(http.MethodGet, "/api/media/proxy/", reveal.ContentBytes, s.proxyStream)
	// Offline Pack manifest + server-delivered key material (ADR-0098 area 6):
	// Pack-subject Stream Token routes. They serve pack bytes/material, so they
	// join the ContentBytes Reveal class. Always mounted; an unwired offlinepack
	// service degrades to 503. The handlers run the dual check (token verify +
	// live pack-state/Reveal re-check) before serving, so a revoked/relocked/
	// sealed pack is denied even when the token still verifies.
	s.handle(http.MethodGet, "/api/pack/authority", reveal.ContentJSON, s.packAuthorityDocument)
	s.handle(http.MethodGet, "/api/pack/manifest/", reveal.ContentBytes, s.packManifestStream)
	s.handle(http.MethodGet, "/api/pack/content/", reveal.ContentBytes, s.packContentStream)
	s.handle(http.MethodGet, "/api/pack/key/", reveal.ContentBytes, s.packKeyStream)
	s.handle(http.MethodGet, "/api/pack/status/", reveal.ContentJSON, s.packStatusStream)
	// Process-root convenience redirect: exact-root only. First-run
	// setup branching remains inside the Core Web UI after GET /api/setup.
	s.handle(http.MethodGet, "/{$}", "", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/web/", http.StatusTemporaryRedirect)
	})
	// The embedded web app shell: the React SPA (Viewer / Manage /
	// Console), static bytes identical for every session — content only ever
	// arrives through the API surfaces above. An SPA fallback serves the shell
	// for client-routed deep links (BrowserRouter resolves the route).
	s.handle(http.MethodGet, "/web/", reveal.ContentBytes, webAppHandler(baseURL))
	s.mountWebPrivateHandlers()
	// Reveal-filtered SSE event stream: a long-lived delivery
	// channel, so it joins the Reveal registry as an EventFanout surface — a
	// restricted-Media event never reaches an unrevealed session.
	s.handle(http.MethodGet, "/api/events", reveal.EventFanout, func(w http.ResponseWriter, r *http.Request) {
		s.eventStream(w, r, s.compatEventGuard(r), "no-store")
	})
	// Ambient Group Conductor latency calibration: the
	// LAN-only four-timestamp NTP handshake a device runs when it joins an
	// Ambient Group. Session-authenticated and LAN-gated in the handler
	// (requestIsLAN), so the conductor is never reachable over the relay/remote
	// path. It returns only server-clock timestamps — no library content — so it
	// joins the ContentJSON Reveal class like the other control endpoints.
	s.handle(http.MethodPost, "/api/group/calibrate", reveal.ContentJSON, s.groupCalibrate)
	// The ADR-0040 /v1/ stable developer contract: per-route scope-gated REST
	// endpoints over existing reveal-filtered store reads, plus a /v1/events
	// alias of the SSE stream. Always mounted; an unwired V1Store degrades to
	// 503 on the read routes (mountV1).
	s.mountV1()
	s.handler = recoverPanics(s.mux)
	return s
}

func detectBuildSHA() string {
	if sha := strings.TrimSpace(os.Getenv("BEAMFALL_BUILD_SHA")); sha != "" {
		return sha
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				return setting.Value
			}
		}
	}
	return "development"
}

// SetBackupIngestEncryptor wires the ENC-4 upload byte encryptor at composition
// time. The server keeps only the store-owned interface so libencrypt remains a
// leaf package and encrypted bytes are written by the approved cipher seam.
func (s *Server) SetBackupIngestEncryptor(encryptor store.BackupIngestEncryptor) {
	s.backupEncryptor = encryptor
}

func init() {
	// Go's mime database has no built-in entry for .webmanifest, so
	// http.ServeContent would content-sniff the PWA manifest as text/plain.
	// Register the spec-correct type once at startup (process-global) so the
	// embedded file server serves it with the right Content-Type.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// webAppHandler serves the embedded React bundle (web.AppAssets) at /web/.
// Real asset requests are served from the bundle; any other path under the
// prefix returns index.html so the client-side router (BrowserRouter) can
// resolve the route — the SPA deep-link fallback. The bundle is static bytes;
// all content still arrives only through the API surfaces.
func webAppHandler(configuredBase ...*url.URL) http.HandlerFunc {
	var base *url.URL
	if len(configuredBase) != 0 {
		base = configuredBase[0]
	}
	assets := web.AppAssets()
	fileServer := http.StripPrefix("/web/", http.FileServerFS(assets))
	index, _ := fs.ReadFile(assets, "index.html")
	rewrittenText := make(map[string][]byte)
	if base != nil && base.Path != "" {
		_ = fs.WalkDir(assets, ".", func(name string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !textWebAsset(name) {
				return nil
			}
			body, readErr := fs.ReadFile(assets, name)
			if readErr == nil {
				rewrittenText[name] = rewriteWebBasePaths(body, base)
			}
			return nil
		})
		if rewritten, ok := rewrittenText["index.html"]; ok {
			index = rewritten
		}
	}
	serveShell := func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		// The shell is the only un-hashed entry point: a cached copy after a
		// binary upgrade references chunk hashes the new server no longer has,
		// so it must never be stored by the browser (the worker precaches its
		// own content-versioned copy for offline). A <meta http-equiv> cannot
		// express this — browsers ignore it — so the header is the contract.
		h.Set("Cache-Control", "no-store")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// CSP: an XSS backstop for the embedded React surface, whose
		// CSRF model relies on same-origin script. All scripts/styles/assets load
		// from same-origin /web/, so default-src 'self' is the base. Provable
		// additions the bundle needs (verified against app-dist): the Vite vendor
		// chunks inject runtime <style> elements and inline element styles that no
		// static hash can cover (style-src 'unsafe-inline'); HLS.js runs its demuxer
		// in a blob: Worker and assigns a blob: MediaSource URL to <video>.src
		// (worker-src/media-src blob:). No inline SCRIPT is allowed — the XSS
		// backstop stays intact.
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"worker-src 'self' blob:; "+
				"media-src 'self' blob:; "+
				"object-src 'none'; "+
				"frame-ancestors 'self'")
		_, _ = w.Write(index)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, "/web/")
		if rel == "sw.js" {
			// The worker script is also un-hashed: revalidate on every update
			// check so a new build's worker installs on the next navigation.
			w.Header().Set("Cache-Control", "no-cache")
		}
		if rel == "" || rel == "index.html" {
			serveShell(w, r)
			return
		}
		if _, err := fs.Stat(assets, rel); err != nil {
			if strings.HasPrefix(rel, "assets/") {
				http.NotFound(w, r)
				return
			}
			serveShell(w, r) // unknown client route → SPA shell
			return
		}
		if body, ok := rewrittenText[rel]; ok {
			if contentType := mime.TypeByExtension(path.Ext(rel)); contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			_, _ = w.Write(body)
			return
		}
		fileServer.ServeHTTP(w, r)
	}
}

func textWebAsset(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".html", ".js", ".css", ".json", ".webmanifest":
		return true
	default:
		return false
	}
}

func rewriteWebBasePaths(body []byte, base *url.URL) []byte {
	if base == nil || base.Path == "" {
		return body
	}
	result := bytes.Clone(body)
	for _, root := range []string{"web", "api", "v1"} {
		for _, lead := range []string{"\"", "'", "`", "url("} {
			old := []byte(lead + "/" + root)
			replacement := []byte(lead + base.Path + "/" + root)
			result = bytes.ReplaceAll(result, old, replacement)
		}
	}
	return result
}

// SetProxyTee wires the proxy-tee stream handler. The
// /api/media/proxy/ route is always mounted; until this is set it degrades to
// 503 (an unwired service, like the V1 read routes), so New keeps its signature
// and the proxy-tee stays an optional, podcast-plugin-driven capability.
func (s *Server) SetProxyTee(h http.HandlerFunc) {
	s.proxyTee = h
}

// SetModeration wires the Guideline 1.2 moderation service. The
// /api/moderation/ routes are always mounted; until this is set the write/read
// routes degrade to 503, so New keeps its signature.
func (s *Server) SetModeration(m *moderation.Service) {
	s.moderation = m
}

// moderationScope resolves a session-bound moderation scope from the request, or
// writes the appropriate error and returns ok=false. Report/block/blocks are
// session-only UI surfaces: a real Profile session is required, and an admin
// token or scoped developer API key is rejected — moderation state is per-Profile
// and not part of the scoped-key closed vocabulary.
func (s *Server) moderationScope(w http.ResponseWriter, r *http.Request) (moderation.Scope, bool) {
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return moderation.Scope{}, false
	}
	if scope.APITokenID != "" || scope.APIKeyID != "" || scope.ProfileID == "" {
		writeError(w, r, http.StatusForbidden, "moderation requires a Profile session")
		return moderation.Scope{}, false
	}
	return moderation.Scope{ProfileID: scope.ProfileID, DeviceID: scope.DeviceID}, true
}

func (s *Server) moderationReport(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	if s.moderation == nil {
		writeError(w, r, http.StatusServiceUnavailable, "moderation unavailable")
		return
	}
	scope, ok := s.moderationScope(w, r)
	if !ok {
		return
	}
	var req moderation.ReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if err := s.moderation.Report(scope, req); err != nil {
		if errors.Is(err, moderation.ErrInvalidRequest) {
			writeError(w, r, http.StatusBadRequest, "invalid report")
			return
		}
		writeErrorCause(w, r, http.StatusInternalServerError, "report failed", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"reported": true})
}

func (s *Server) moderationBlock(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	if s.moderation == nil {
		writeError(w, r, http.StatusServiceUnavailable, "moderation unavailable")
		return
	}
	scope, ok := s.moderationScope(w, r)
	if !ok {
		return
	}
	var req moderation.BlockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if err := s.moderation.Block(scope, req); err != nil {
		switch {
		case errors.Is(err, moderation.ErrInvalidRequest):
			writeError(w, r, http.StatusBadRequest, "invalid block")
		case errors.Is(err, moderation.ErrBlockLimit):
			writeError(w, r, http.StatusConflict, "block limit reached")
		default:
			writeErrorCause(w, r, http.StatusInternalServerError, "block failed", err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"blocked": true})
}

func (s *Server) moderationBlocks(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	if s.moderation == nil {
		writeError(w, r, http.StatusServiceUnavailable, "moderation unavailable")
		return
	}
	scope, ok := s.moderationScope(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"blocks": s.moderation.Blocks(scope)})
}

// moderationContact returns the published moderation/contact path. It is public
// (no session) and carries no content, so it has no Reveal surface class.
func (s *Server) moderationContact(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	if s.moderation == nil {
		writeError(w, r, http.StatusServiceUnavailable, "moderation unavailable")
		return
	}
	writeJSON(w, http.StatusOK, s.moderation.Contact())
}

// proxyStream dispatches the proxy-tee route, degrading to 503 when no proxy-tee
// service is wired (the same honest-degrade posture as the V1 read routes).
func (s *Server) proxyStream(w http.ResponseWriter, r *http.Request) {
	// Proxy bytes live outside mediahttp, so join its token-response boundary
	// before either this dispatcher or the proxy-tee handler can commit headers.
	w = mediahttp.MarkStreamTokenResponseNonStorable(w)
	if s.proxyTee == nil {
		writeError(w, r, http.StatusServiceUnavailable, "proxy-tee unavailable")
		return
	}
	s.proxyTee(w, r)
}

// SetPackHandlers wires the Offline Pack authority and artifact-serving
// handlers. The routes are always mounted and degrade to 503 until wired.
func (s *Server) SetPackHandlers(authority, manifest, content, key, status http.HandlerFunc) {
	s.packAuthority = authority
	s.packManifest = manifest
	s.packContent = content
	s.packKey = key
	s.packStatus = status
}

func (s *Server) packAuthorityDocument(w http.ResponseWriter, r *http.Request) {
	if s.packAuthority == nil {
		writeError(w, r, http.StatusServiceUnavailable, "offline pack unavailable")
		return
	}
	s.packAuthority(w, r)
}

func (s *Server) packManifestStream(w http.ResponseWriter, r *http.Request) {
	if s.packManifest == nil {
		writeError(w, r, http.StatusServiceUnavailable, "offline pack unavailable")
		return
	}
	s.packManifest(w, r)
}

func (s *Server) packContentStream(w http.ResponseWriter, r *http.Request) {
	if s.packContent == nil {
		writeError(w, r, http.StatusServiceUnavailable, "offline pack unavailable")
		return
	}
	s.packContent(w, r)
}

func (s *Server) packKeyStream(w http.ResponseWriter, r *http.Request) {
	if s.packKey == nil {
		writeError(w, r, http.StatusServiceUnavailable, "offline pack unavailable")
		return
	}
	s.packKey(w, r)
}

func (s *Server) packStatusStream(w http.ResponseWriter, r *http.Request) {
	if s.packStatus == nil {
		writeError(w, r, http.StatusServiceUnavailable, "offline pack unavailable")
		return
	}
	s.packStatus(w, r)
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r = s.resolveProxyRequest(r)
	var ok bool
	w, r, ok = s.applyBaseURL(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodOptions && apiPath(r.URL.Path) {
		class, ok := s.requestAccessClass(r, strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")))
		if !ok || class == AccessClassUnclassified {
			writeError(w, r, http.StatusNotFound, "route not found")
			return
		}
		if !applyAccessPolicy(w, r, class) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if class, matched := s.requestAccessClass(r, r.Method); matched {
		if !applyAccessPolicy(w, r, class) {
			return
		}
	}
	// Attach a per-request correlation ID so 5xx log lines and the response
	// header carry the same token (ADR-0013 §5 ops log correlation).
	reqID := newRequestID()
	w.Header().Set(requestIDHeader, reqID)
	ctx := context.WithValue(r.Context(), requestIDKey{}, reqID)
	if !requestIsHTTPS(r) && !requestIsLAN(r) {
		ctx = context.WithValue(ctx, plainHTTPPublicWarningContextKey, true)
	}
	r = r.WithContext(ctx)
	s.handler.ServeHTTP(w, r)
}

// Routes returns a copy of the mounted route table.
func (s *Server) Routes() []Route {
	routes := make([]Route, len(s.routes))
	copy(routes, s.routes)
	return routes
}

// Surfaces returns route-level Reveal surfaces derived from the mounted routes.
func (s *Server) Surfaces() []reveal.Surface {
	surfaces := make([]reveal.Surface, 0, len(s.routes))
	for _, route := range s.routes {
		if route.RevealClass == "" {
			continue
		}
		surfaces = append(surfaces, reveal.Surface{
			ID:    route.Method + " " + route.Pattern,
			Class: route.RevealClass,
		})
	}
	return surfaces
}

func (s *Server) handle(method, pattern string, class reveal.SurfaceClass, h http.HandlerFunc) {
	s.routes = append(s.routes, Route{Method: method, Pattern: pattern, RevealClass: class})
	s.accessClasses[method+" "+pattern] = routeAccessClass(method, pattern)
	handler := h
	h = func(w http.ResponseWriter, r *http.Request) {
		if s.applyTestFault(w, r) {
			return
		}
		multipartRequest := pattern == "/v1/backups/media" ||
			pattern == createMediaArtworkRoutePattern ||
			pattern == artworkUploadPattern
		if method == http.MethodPost && class == reveal.ContentJSON && !multipartRequest && !requireJSONContentType(w, r) {
			return
		}
		handler(w, r)
	}
	next := h
	h = func(w http.ResponseWriter, r *http.Request) {
		if compatRouteExempt(method, pattern) {
			next(w, r)
			return
		}
		s.requireCompatDecision(next)(w, r)
	}
	s.mux.HandleFunc(method+" "+pattern, h)
}

func requireJSONContentType(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil || (r.ContentLength == 0 && len(r.TransferEncoding) == 0) {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		writeError(w, r, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	return true
}

// SetMetricsSources wires the ops observability providers: the
// store's readiness probe and active-session/library-size reads, and the
// resource governor's live transcode count. /health and /metrics are always
// mounted; a nil provider degrades honestly (readiness reports "ready" on
// liveness alone, a gauge whose source is unwired is simply omitted) rather than
// fabricating a number. Following SetEvents, this is a composition-root setter so
// New keeps its existing signature (the conformance harness calls it directly).
func (s *Server) SetMetricsSources(readiness ReadinessProvider, sessions SessionCountProvider, transcode TranscodeCountProvider, library LibrarySizeProvider) {
	s.readiness = readiness
	s.sessions = sessions
	s.transcode = transcode
	s.library = library
}

// SetJobQueueSource wires the oldest-waiting-job age SLI. Like the
// other metrics sources this is a composition-root setter so New keeps its
// signature; a nil source simply omits the queue-age gauge.
func (s *Server) SetJobQueueSource(jobQueue JobQueueAgeProvider) {
	s.jobQueue = jobQueue
}

func (s *Server) SetBuildMetadata(buildSHA, seedProfile, seedVersion string) {
	if buildSHA = strings.TrimSpace(buildSHA); buildSHA != "" {
		s.buildSHA = buildSHA
	}
	s.seedProfile = strings.TrimSpace(seedProfile)
	s.seedVersion = strings.TrimSpace(seedVersion)
	s.refreshCompatPolicy()
}

func (s *Server) SetCapabilities(capabilities []string) {
	s.capabilities = append([]string(nil), capabilities...)
	s.refreshCompatPolicy()
}

// SetSourceOfferURL wires the release-injected, version-specific
// corresponding-source offer into the public /source redirect.
func (s *Server) SetSourceOfferURL(offerURL string) {
	s.sourceOfferURL = strings.TrimSpace(offerURL)
}

// SetPublicMetrics allows /metrics to be scraped from non-loopback clients.
// The default is false: internet-exposed metrics require an explicit operator
// opt-in and should sit behind the hardened reverse-proxy path.
func (s *Server) SetPublicMetrics(public bool) {
	s.publicMetrics = public
}

func (s *Server) SetObservability(observability ObservabilityController) {
	s.observability = observability
}

// health answers liveness and readiness in one document. Liveness
// is implicit: the handler responding at all proves the process is up, so live
// is always true. Readiness reflects store/DB reachability plus migrations
// applied — when the store reports not-ready (or no readiness source is wired and
// we can only attest liveness), ready is false. A not-ready result returns 503 so
// a Docker HEALTHCHECK / uptime probe sheds the instance until the store recovers,
// while a live-but-not-ready process is still distinguishable in the body.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ready := false
	if s.readiness != nil {
		ready = s.readiness.Ready(r.Context()) == nil
	}
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{
		"status":       "ok",
		"live":         true,
		"ready":        ready,
		"buildSHA":     s.buildSHA,
		"seedProfile":  s.seedProfile,
		"seedVersion":  s.seedVersion,
		"capabilities": s.capabilities,
	})
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	if !s.metricsScrapeAllowed(r) {
		// One constant refusal for every failing shape: a host-local client, an
		// off-host one, a relayed one, an absent bearer, and a wrong bearer are
		// all answered identically, so the response carries no signal a caller
		// could probe.
		writeError(w, r, http.StatusForbidden, "metrics require the scrape credential")
		return
	}
	var body strings.Builder
	body.WriteString("beamfall_up 1\n")
	if s.jobStats != nil {
		stats, err := s.jobStats.JobStats(r.Context())
		if err != nil {
			body.WriteString("beamfall_jobs_stats_error 1\n")
		} else {
			queueDepth := int64(0)
			for _, row := range stats {
				_, _ = fmt.Fprintf(&body, "beamfall_jobs{class=%q,status=%q} %d\n", row.Class, row.Status, row.Count)
				s.recordGauge(r.Context(), "beamfall_observability_jobs", int64(row.Count), map[string]string{
					"class":  string(row.Class),
					"status": string(row.Status),
				})
				if row.Status == store.JobStatusQueued || row.Status == store.JobStatusYielded {
					queueDepth += int64(row.Count)
				}
			}
			// ADR-0146 §8 job-queue-depth SLI: total jobs waiting to run, the
			// low-cardinality aggregate operators alert on (derived from the
			// stats already fetched — no extra query).
			_, _ = fmt.Fprintf(&body, "beamfall_jobs_queue_depth %d\n", queueDepth)
			s.recordGauge(r.Context(), "beamfall_observability_job_queue_depth", queueDepth, nil)
		}
	}
	if s.jobQueue != nil {
		if ageMs, ok, err := s.jobQueue.OldestQueuedJobAgeMs(r.Context(), time.Now().UTC()); err != nil {
			body.WriteString("beamfall_jobs_queue_oldest_age_ms_error 1\n")
		} else if ok {
			// ADR-0146 §8 job-queue-age SLI: age of the oldest waiting job, so a
			// stalled queue is visible even when depth alone looks healthy.
			_, _ = fmt.Fprintf(&body, "beamfall_jobs_queue_oldest_age_ms %d\n", ageMs)
			s.recordGauge(r.Context(), "beamfall_observability_job_queue_oldest_age_ms", ageMs, nil)
		}
	}
	if s.sessions != nil {
		if count, err := s.sessions.ActiveSessionCount(r.Context(), time.Now().UTC()); err != nil {
			body.WriteString("beamfall_sessions_active_error 1\n")
		} else {
			_, _ = fmt.Fprintf(&body, "beamfall_sessions_active %d\n", count)
			s.recordGauge(r.Context(), "beamfall_observability_sessions_active", int64(count), nil)
		}
	}
	if s.transcode != nil {
		count := s.transcode.ActiveTranscodes()
		_, _ = fmt.Fprintf(&body, "beamfall_transcode_active %d\n", count)
		s.recordGauge(r.Context(), "beamfall_observability_transcode_sessions_active", int64(count), nil)
	}
	if s.library != nil {
		if total, err := s.library.LibraryMediaTotal(r.Context()); err != nil {
			body.WriteString("beamfall_library_media_total_error 1\n")
		} else {
			_, _ = fmt.Fprintf(&body, "beamfall_library_media_total %d\n", total)
			s.recordGauge(r.Context(), "beamfall_observability_library_media_total", int64(total), nil)
		}
	}
	if s.observability != nil {
		body.WriteString(s.observability.PrometheusText(r))
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body.String()))
}

func (s *Server) recordGauge(ctx context.Context, name string, value int64, labels map[string]string) {
	if s.observability != nil {
		_ = s.observability.RecordGauge(ctx, name, value, labels)
	}
}

// requestIsLoopback reports whether the request came from a client on this host
// with nothing in between — the condition the test-fault control gates on. The
// TCP peer address alone cannot answer it: the default bind is 127.0.0.1:8800,
// so a reverse proxy in the documented topology necessarily runs on this same
// host and presents a loopback peer address for traffic it relays from the
// internet. requestRelayedByProxy therefore disqualifies a relayed request on
// signals the remote client cannot set in its own favour — the operator's
// declared known-proxies allowlist, and the forwarding headers a relaying proxy
// adds. A proxy that is neither declared nor adds any forwarding header stays
// indistinguishable from a local client at this layer, so a false positive here
// is possible: this predicate narrows an unauthenticated surface, it does not
// close it. /metrics no longer consults it at all — GCR-012-H requires the
// scrape credential in metrics_credential.go unconditionally, precisely because
// that residual cannot be removed. The test-fault control keeps it because it
// is armed only by an explicit SetTestFaults opt-in that production never
// takes, so its exposure is bounded by the operator's own decision to arm it.
func requestIsLoopback(r *http.Request) bool {
	return !requestFromRelay(r) && !requestRelayedByProxy(r) && loopbackRemote(r.RemoteAddr)
}

func loopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func (s *Server) graphQL(w http.ResponseWriter, r *http.Request) {
	s.graph.ServeHTTP(w, r)
}

func (s *Server) manifestDocument(w http.ResponseWriter, r *http.Request) {
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}
	document, err := s.manifest.Document(r.Context(), scope)
	if err != nil {
		writeErrorCause(w, r, http.StatusInternalServerError, "manifest unavailable", err)
		return
	}
	writeJSON(w, http.StatusOK, ManifestResponse{
		Document:                document,
		SystemSurfaceDisclosure: s.manifestSystemSurfaceDisclosure(r.Context(), scope, document),
	})
}

func (s *Server) playbackInfo(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}

	var req playback.InfoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	resp, err := s.playback.Info(r.Context(), scope, req)
	if err != nil {
		writePlaybackError(w, r, "playback info unavailable", err)
		return
	}
	writeJSON(w, http.StatusOK, PlaybackInfoResponse{
		InfoResponse:            resp,
		SystemSurfaceDisclosure: s.mediaSystemSurfaceDisclosure(r.Context(), scope, store.MediaID(req.MediaID)),
	})
}

func (s *Server) previewInfo(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}

	var req playback.PreviewInfoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	resp, err := s.playback.PreviewInfo(r.Context(), scope, req)
	if err != nil {
		writePlaybackError(w, r, "preview info unavailable", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func writePlaybackError(w http.ResponseWriter, r *http.Request, internalMessage string, err error) {
	switch {
	case errors.Is(err, playback.ErrInvalidRequest):
		writeError(w, r, http.StatusBadRequest, err.Error())
	case errors.Is(err, playback.ErrInvalidSelector):
		writeError(w, r, http.StatusBadRequest, "invalid selector")
	case errors.Is(err, playback.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "media unavailable")
	default:
		writeErrorCause(w, r, http.StatusInternalServerError, internalMessage, err)
	}
}

func (s *Server) createBeamOffer(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	if s.beam == nil {
		writeError(w, r, http.StatusServiceUnavailable, "beam handoff unavailable")
		return
	}
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}

	var req beam.OfferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	resp, err := s.beam.CreateOffer(r.Context(), scope, req)
	if err != nil {
		switch {
		case errors.Is(err, beam.ErrInvalidRequest):
			writeError(w, r, http.StatusBadRequest, "invalid beam handoff request")
		case errors.Is(err, beam.ErrMediaUnavailable):
			writeError(w, r, http.StatusNotFound, "media unavailable")
		default:
			writeErrorCause(w, r, http.StatusInternalServerError, "beam handoff failed", err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) claimBeamOffer(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	if s.beam == nil {
		writeError(w, r, http.StatusServiceUnavailable, "beam handoff unavailable")
		return
	}
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}

	var req beam.ClaimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	resp, err := s.beam.ClaimOffer(r.Context(), scope, req)
	if err != nil {
		if errors.Is(err, beam.ErrInvalidToken) {
			writeError(w, r, http.StatusUnauthorized, "invalid handoff token")
			return
		}
		writeErrorCause(w, r, http.StatusInternalServerError, "beam handoff failed", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) createPairingCode(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}

	var req pairing.CodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	resp, err := s.pairing.CreateCode(r.Context(), scope, req)
	if err != nil {
		switch {
		case errors.Is(err, pairing.ErrForbidden):
			writeError(w, r, http.StatusForbidden, "pairing forbidden")
		case errors.Is(err, pairing.ErrInvalidRequest):
			writeError(w, r, http.StatusBadRequest, "invalid pairing request")
		default:
			writeErrorCause(w, r, http.StatusInternalServerError, "pairing failed", err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) enrollDevice(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	var req pairing.EnrollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	req.RemoteAddr = r.RemoteAddr
	req.IsLAN = requestIsLAN(r)
	resp, err := s.pairing.Enroll(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, pairing.ErrThrottled):
			writeError(w, r, http.StatusTooManyRequests, "pairing attempts throttled")
		case errors.Is(err, pairing.ErrInvalidCode):
			writeError(w, r, http.StatusUnauthorized, "invalid pairing code")
		case errors.Is(err, pairing.ErrInvalidRequest):
			writeError(w, r, http.StatusBadRequest, "invalid enrollment request")
		default:
			writeErrorCause(w, r, http.StatusInternalServerError, "enrollment failed", err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

// requestIsHTTPS reports whether the request reached the server over an
// effectively-HTTPS connection. The server terminates TLS directly (ADR-0012 §1
// direct HTTPS via ACME), so r.TLS is set for the direct-TLS path and nil for a
// plain-HTTP LAN listener. The server does not trust X-Forwarded-Proto or any
// forwarded header anywhere, so this deliberately does NOT consult them — a
// reverse-proxy recipe that terminates TLS speaks plain HTTP to this server, and
// inventing a forwarded-header trust model here would be a new (and unguarded)
// trust surface.
func requestIsHTTPS(r *http.Request) bool {
	resolution, ok := ProxyResolutionFromRequest(r)
	return r.TLS != nil || ok && resolution.Trusted && resolution.Scheme == "https"
}

// requestIsLAN reports whether the request originated on the local network: the
// source IP is loopback or RFC1918/ULA/link-local private. This is the
// distinction ADR-0046 §2.1 turns on — only a LAN enrollment captures the
// server's SPKI pin. A relay-fronted remote enrollment arrives over the
// outbound carrier and presents a public source address, so it reads as
// non-LAN and never (re-)pins. This reads r.RemoteAddr post-resolution
// (resolveProxyRequest may have rewritten it from X-Forwarded-For), which is
// the real peer unless a KnownProxies-trusted proxy supplied a single
// unambiguous forwarded address: singleForwardedAddress requires exactly one
// X-Forwarded-For header containing exactly one valid address, so a standard
// appending proxy ($proxy_add_x_forwarded_for) defeats header injection — a
// proxy misconfigured to pass a client's X-Forwarded-For through verbatim is
// the residual trust surface, not a spoof of this check itself. When a declared
// proxy relays a request whose forwarded address does not reduce to exactly one
// value, no client address was resolved at all and the address left on the
// request is the proxy's own; requestForwardedUnresolved makes that read as
// non-LAN rather than letting an ambiguous input inherit the proxy's proximity.
// A request that carries forwarding evidence but was not relayed through a
// declared known proxy is read the same way: r.RemoteAddr is then some
// upstream hop's own address (for example the shipped reverse proxy's private
// container address when the operator never set BEAMFALL_KNOWN_PROXIES), not
// the client's, so it must not stand in for the client's proximity either.
func requestIsLAN(r *http.Request) bool {
	if requestFromRelay(r) || requestForwardedUnresolved(r) {
		return false
	}
	resolution, ok := ProxyResolutionFromRequest(r)
	viaDeclaredProxy := ok && resolution.ViaKnownProxy
	if requestWasForwarded(r) && !viaDeclaredProxy {
		return false
	}
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	if zone := strings.LastIndexByte(host, '%'); zone >= 0 {
		host = host[:zone]
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// setWebCredentialCookie issues a browser credential cookie. The
// Secure flag mirrors the connection's effective scheme: true over direct TLS so
// the cookie is HTTPS-only when exposed remotely, false over plain-HTTP LAN so
// the common first-run/LAN case can actually receive the cookie and log in.
// HttpOnly and SameSite=Strict are unconditional.
func setWebCredentialCookie(w http.ResponseWriter, r *http.Request, name, value string, expires time.Time) {
	path := "/"
	if base := baseURLFromRequest(r); base != nil && base.Path != "" {
		path = base.Path + "/"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		Expires:  expires,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	// Marshal before WriteHeader: once the status is committed, an encode
	// failure can no longer be reported cleanly (http.Error after a partial
	// body would append plaintext to truncated JSON and double-write the header).
	data, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
	_, _ = w.Write([]byte("\n"))
}

func logRequestFailure(ctx context.Context, r *http.Request, status int, message string, cause error) {
	attrs := []any{"status", status, "error", message}
	if r != nil {
		// URL.Path never contains the query string so tokens/secrets in ?query
		// params are not logged here. The request ID correlates this log line
		// with the X-Beamfall-Request-Id response header.
		attrs = append(attrs, "method", r.Method, "path", redactTokenPath(r.URL.Path))
		if reqID := requestIDFromContext(ctx); reqID != "" {
			attrs = append(attrs, "requestID", reqID)
		}
	}
	if cause != nil {
		attrs = append(attrs, "cause", cause)
	}
	slog.ErrorContext(ctx, "http request failed", attrs...)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, message string) {
	writeErrorCause(w, r, status, message, nil)
}

func writeErrorCause(w http.ResponseWriter, r *http.Request, status int, message string, cause error) {
	if status >= http.StatusInternalServerError {
		ctx := context.Background()
		if r != nil {
			ctx = r.Context()
		}
		logRequestFailure(ctx, r, status, message, cause)
	}
	writeJSON(w, status, map[string]string{"error": message})
}

// writeDecodeError reports a JSON-body decode failure: a body that exceeded the
// http.MaxBytesReader cap becomes 413 Request Entity Too Large, anything else a
// generic 400.
func writeDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeError(w, r, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	writeError(w, r, http.StatusBadRequest, "invalid JSON body")
}
