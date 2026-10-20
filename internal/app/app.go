// Package app is the importable composition root for Beamfall.
package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/beam"
	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/compat"
	"github.com/beamfall/core/internal/conductor"
	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/decodeverify"
	"github.com/beamfall/core/internal/discovery"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/genassets"
	"github.com/beamfall/core/internal/governor"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/ingest"
	"github.com/beamfall/core/internal/integrationhost"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/migrate"
	"github.com/beamfall/core/internal/moderation"
	"github.com/beamfall/core/internal/nodehost"
	"github.com/beamfall/core/internal/nodehostd"
	"github.com/beamfall/core/internal/observability"
	"github.com/beamfall/core/internal/offlinepack"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/photoexport"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/playback/subtitleingest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/proxytee"
	"github.com/beamfall/core/internal/push"
	"github.com/beamfall/core/internal/qbittorrent"
	"github.com/beamfall/core/internal/remoteaccess"
	"github.com/beamfall/core/internal/reveal"
	scanpkg "github.com/beamfall/core/internal/scan"
	schedulepkg "github.com/beamfall/core/internal/schedule"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/server"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
	"github.com/beamfall/core/internal/updatecheck"
	"github.com/beamfall/core/internal/watcher"
	"github.com/beamfall/core/internal/webhook"
	"github.com/beamfall/core/internal/workflow/convert"
	scrobble "github.com/beamfall/plugin-sdk/scrobble"
)

var scanHandlerWithWorkerGovernor = scanpkg.HandlerWithWorkerGovernor

// App contains the wired Beamfall process.
type App struct {
	cfg             config.Config
	store           *store.Store
	plugins         *plugin.Registry
	server          *server.Server
	compat          http.Handler
	jobs            *jobs.Registry
	hls             *playback.Sessions
	governor        *governor.Governor
	surfaces        *reveal.Registry
	watcher         *watcher.Service
	watcherDisabled bool
	schedule        *schedulepkg.Service
	proxyTee        *proxytee.Service
	// webhook is the Webhook Emitter Integration Plugin, nil
	// when no endpoints are configured (the plugin is dormant — opt-in,
	// ADR-0003). When non-nil, Run drives it as a long-lived goroutine.
	webhook *webhook.Emitter
	// scrobble is the Scrobble Integration Plugin dispatcher, nil when
	// no adapters are configured (the plugin is dormant — opt-in, ADR-0003). When
	// non-nil, Run drives its queue-drain goroutine and the graph playback path
	// feeds it watched transitions.
	scrobble *scrobble.Dispatcher
	// integration is the ADR-0058 third-party Integration Worker manager, nil when
	// no worker artifact is installed (the runtime is dormant — opt-in, like the
	// webhook/scrobble plugins). When non-nil, Run supervises each installed
	// worker out-of-process under the shared jobs lifecycle.
	integration *integrationManager
	logger      *slog.Logger
	setupToken  string
	jobWake     *jobWake
	// jobClaimSafetyInterval discovers jobs persisted outside this process. Local
	// enqueue seams wake claimers immediately; the poll is deliberately slower
	// so idle workers do not continuously query an empty queue.
	jobClaimSafetyInterval time.Duration

	// directHTTPS is the autocert-backed direct-HTTPS path, nil
	// when LAN-only. Run serves the HTTPS listener with its TLS config and the
	// ACME HTTP-01 challenge on the plain-HTTP listener when present.
	directHTTPS *remoteaccess.DirectHTTPS
	// selfSigned is the persistent baseline TLS identity every server holds:
	// a self-signed leaf whose SPKI is pinned at LAN enrollment.
	// It backs client TLS on the overlay path and the relay carrier
	// WITHOUT operator ACME; the port is served through a TLS-mux so LAN cleartext
	// still works alongside it. When ACME (directHTTPS) is configured it is composed
	// by SNI (ComposeWithACME): only the public FQDN is served the ACME cert for
	// browser trust; every other SNI stays on this self-signed identity — which is
	// also the identity a native client pins at LAN enrollment.
	selfSigned *remoteaccess.SelfSignedIdentity
	// relay is the self-hosted relay carrier client (ADR-0018/0025), nil when
	// no relay endpoint is configured.
	relay *remoteaccess.RelayClient

	// addr holds the live listener address once Run binds it, so a caller that
	// started the server on an ephemeral port (":0") can discover the chosen
	// port. Guarded by addrMu; addrReady closes once it is set.
	addrMu          sync.Mutex
	addr            string
	addrReady       chan struct{}
	directTLSActive *atomic.Bool

	// drainGrace is an internal lifecycle bound, not operator configuration.
	// drainAfter is replaceable only by package tests so the
	// cancellation boundary can be exercised without wall-clock sleeps.
	drainGrace time.Duration
	drainAfter func(time.Duration) <-chan time.Time

	// runStarted fences Run to one owner. Run binds the listener, closes the
	// address latch, and closes the store; a second Run would double-own all
	// three and panic on the latch.
	runStarted atomic.Bool
}

const (
	retentionSweepJobKind   store.JobKind = "retention-sweep"
	databaseBackupJobKind   store.JobKind = "database-backup"
	eventsCompactionJobKind store.JobKind = "events-compaction"
	jobClaimSafetyInterval                = 30 * time.Second
)

// jobWake broadcasts coalescing notifications to the active claimer pool.
// Each claimer owns a one-slot subscription so a burst wakes every worker at
// most once while preserving the configured claim concurrency.
type jobWake struct {
	mu          sync.Mutex
	subscribers map[chan struct{}]struct{}
}

func newJobWake() *jobWake {
	return &jobWake{subscribers: map[chan struct{}]struct{}{}}
}

func (w *jobWake) subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	w.mu.Lock()
	w.subscribers[ch] = struct{}{}
	w.mu.Unlock()
	return ch, func() {
		w.mu.Lock()
		delete(w.subscribers, ch)
		w.mu.Unlock()
	}
}

func (w *jobWake) notify() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for ch := range w.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

type wakingScheduleStore struct {
	*store.Store
	wake *jobWake
}

func (s wakingScheduleStore) EnqueueJob(ctx context.Context, job store.Job) (store.Job, error) {
	return enqueueAndWake(ctx, s.Store, s.wake, job)
}

// EnqueueScheduleOccurrenceMember publishes one journaled schedule member and
// wakes the job workers, mirroring the EnqueueJob wake path for the durable
// scheduler (AUDITFIX-SCHEDULER-DURABILITY).
func (s wakingScheduleStore) EnqueueScheduleOccurrenceMember(ctx context.Context, id store.ScheduleID, due time.Time, ordinal int, now time.Time) (store.Job, error) {
	created, err := s.Store.EnqueueScheduleOccurrenceMember(ctx, id, due, ordinal, now)
	if err == nil {
		s.wake.notify()
	}
	return created, err
}

func enqueueAndWake(ctx context.Context, st *store.Store, wake *jobWake, job store.Job) (store.Job, error) {
	created, err := st.EnqueueJob(ctx, job)
	if err == nil {
		wake.notify()
	}
	return created, err
}

var (
	listenTCP                                 = net.Listen
	assertIntegrationHostPermissionVocabulary = integrationhost.AssertPermissionVocabularyInSync
	closeOwnedWatcher                         = func(service *watcher.Service) error { return service.Close() }
	newWatcherService                         = watcher.NewService
)

// conformanceStreamTokenKey is the fixed 32-byte HMAC key the conformance seed
// signs Stream Tokens with — identical to the key
// internal/conformance/golden_corpus_test.go composes, so the corpus
// playback-info goldens hash byte-identically over a real HTTP listener. It is
// a publicly known test key; it is only ever installed for the gated,
// test/dev-only conformance seed profile (never a production serve).
func conformanceStreamTokenKey() []byte {
	return []byte("12345678901234567890123456789012")
}

func hardwareAccelReport(result ffmpeg.HWAccelResult) config.HardwareAccelReport {
	report := config.HardwareAccelReport{
		Working: make([]string, len(result.Working)),
		Results: make([]config.HardwareAccelProbe, len(result.Results)),
	}
	for i, accel := range result.Working {
		report.Working[i] = string(accel)
	}
	for i, probe := range result.Results {
		report.Results[i] = config.HardwareAccelProbe{
			Accelerator: string(probe.Accelerator),
			Status:      string(probe.Status),
		}
	}
	return report
}

// New wires the server capabilities. A nil logger defaults to slog.Default();
// bundles are the executable-owned plugin set — invalid bundles fail boot.
// The optional Hooks value carries compiled plugin-module behavior from the
// executable composition root; callers that need only declarative bundles may
// omit it. More than one Hooks value is invalid.
func New(ctx context.Context, cfg config.Config, logger *slog.Logger, bundles []plugin.Bundle, hookValues ...Hooks) (*App, error) {
	executableBundles := append([]plugin.Bundle(nil), bundles...)
	hooks, err := resolveHooks(hookValues)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	if err := assertIntegrationHostPermissionVocabulary(); err != nil {
		return nil, err
	}
	// The conformance seed boots the deterministic corpus world: the read-path
	// contract services run on a fixed clock and a fixed Stream-Token key so a
	// real HTTP listener serves the corpus goldens byte-identically (mirroring
	// internal/conformance/golden_corpus_test.go). config gates this profile
	// behind AllowConformanceSeed, so it is never reachable in a normal serve.
	// The fixed clock is confined to the wire-contract services — the job loop,
	// scheduler, and retention sweeps keep wall-clock time so the process does
	// not deadlock at the epoch.
	readClock := time.Now
	conformanceMode := cfg.SeedProfile == string(store.SeedProfileConformance)
	if conformanceMode {
		cfg.StreamTokenKey = conformanceStreamTokenKey()
		readClock = func() time.Time { return time.Unix(0, 0).UTC() }
	}
	if len(cfg.StreamTokenKey) == 0 {
		return nil, errors.New("stream token key is required")
	}
	if cfg.TranscodeScratchDir == "" {
		cfg.TranscodeScratchDir = filepath.Join(cfg.DataDir, "transcode")
	}
	st, err := store.Open(ctx, cfg.DataDir, store.Options{
		SeedDevelopmentData:               cfg.SeedDevelopmentData,
		SeedProfile:                       store.SeedProfile(cfg.SeedProfile),
		AllowNetworkDataDir:               cfg.AllowNetworkDataDir,
		TrashDisabled:                     !cfg.TrashEnabled,
		Logger:                            logger,
		EnableEncryptedLibraryDBLifecycle: true,
		Role:                              cfg.Role,
	})
	if err != nil {
		return nil, err
	}
	st.SetSnapshotSealer(backupcore.NewDirectorySealer(cfg.DataDir))
	if err := ensureBackupBootstrap(cfg.DataDir, time.Now().UTC()); err != nil {
		_ = st.Close()
		return nil, err
	}
	jobWake := newJobWake()
	inventory, err := plugin.OpenInventory(filepath.Join(cfg.DataDir, "plugins", "inventory"), executableBundles)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	bundles, applied, inventoryWarning := inventory.Apply()
	if inventoryWarning != nil {
		if applied {
			logger.Warn("staged plugin inventory applied but cleanup failed", "error", inventoryWarning)
		} else {
			logger.Warn("staged plugin inventory rejected; retaining last-good", "error", inventoryWarning)
		}
	}
	if applied {
		logger.Info("staged plugin inventory applied")
	}
	inventoryStandings, err := inventory.ActiveStandings()
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("load active plugin inventory standing: %w", err)
	}
	// Enforce the Console's plugin-activation overrides (migration 0035): the
	// `setpluginactive` mutation persists a per-bundle active/inactive override
	// keyed on the plugin Bundle ID, and the Console reads it back — but a
	// deactivated bundle must also be ABSENT from the live registry, or its
	// Entity Kinds, Sources, and scrapers would still load. A bundle with no
	// override row is active by default (first boot has an empty table, so all
	// declared bundles load), matching store.PluginActivation's contract.
	overrides, err := st.PluginActivations(ctx)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	bundles = activePluginBundles(bundles, overrides, logger)
	executableBundles = activePluginBundles(executableBundles, overrides, logger)
	plugins, err := plugin.NewInventoryRegistry(bundles, executablePluginIDs(executableBundles), inventoryStandings)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	executablePlugins, err := plugin.NewBuiltinRegistry(executableBundles)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	var mediaFieldSearchPolicies []store.MediaFieldSearchPolicy
	for _, id := range plugins.IDs() {
		bundle, _ := plugins.Bundle(id)
		for _, field := range bundle.MediaFields {
			mediaFieldSearchPolicies = append(mediaFieldSearchPolicies, store.MediaFieldSearchPolicy{
				PluginID: string(id), Field: field.Name, Type: store.EntityFieldType(field.Type), Searchable: field.Searchable,
			})
		}
	}
	if err := st.ReplaceMediaFieldSearchPolicies(ctx, mediaFieldSearchPolicies); err != nil {
		_ = st.Close()
		return nil, err
	}
	// First-run admin-password bootstrap: BEAMFALL_ADMIN_PASSWORD
	// allows headless setup that skips the web wizard. It is read from the
	// environment, never config, so it never lands in a logged Config (hard
	// invariant 5); it is applied ONLY when no admin password exists yet, so a
	// restart with the env still set never silently rotates a chosen password.
	// The plaintext is never logged.
	if err := bootstrapAdminPassword(ctx, st, logger); err != nil {
		_ = st.Close()
		return nil, err
	}
	// Entity field index postures (ADR-0011 FTS + side-table indexes): lower
	// the activated bundles' declared Filterable/Sortable/Searchable Entity
	// Kind fields, plus numeric ordering sources, into store indexes and the
	// entity FTS document set.
	var fieldIndexes []store.EntityFieldIndex
	for _, id := range plugins.IDs() {
		bundle, _ := plugins.Bundle(id)
		for _, kind := range bundle.EntityKinds {
			for _, field := range kind.Fields {
				if !field.Filterable && !field.Sortable && !field.Searchable && field.Name != kind.OrderingSourceField {
					continue
				}
				fieldIndexes = append(fieldIndexes, store.EntityFieldIndex{
					Kind:       kind.Name,
					Field:      field.Name,
					Type:       store.EntityFieldType(field.Type),
					Filterable: field.Filterable,
					Sortable:   field.Sortable,
					Searchable: field.Searchable,
				})
			}
		}
	}
	if err := st.RegisterEntityFieldIndexes(ctx, fieldIndexes); err != nil {
		_ = st.Close()
		return nil, err
	}
	if err := st.RegisterEntityKindPolicies(ctx, entityKindPolicies(plugins)); err != nil {
		_ = st.Close()
		return nil, err
	}
	if err := st.RegisterEntityKindOrderings(ctx, entityKindOrderings(plugins)); err != nil {
		_ = st.Close()
		return nil, err
	}
	// Field-schema evolution: a revision that REMOVED a declared
	// field or Entity Kind leaves a now-dead query surface (composite index + FTS
	// contribution) the line above does not recreate. Reconcile the persisted
	// schema down to the active set just registered — dropping only surfaces no
	// active plugin still declares (collision-checked), never the stored values,
	// which stay inert until an explicit Uninstall.
	if err := st.ReconcileEntityFieldIndexes(ctx); err != nil {
		_ = st.Close()
		return nil, err
	}
	ffmpegRunner := ffmpeg.New()
	transcodePolicy, err := st.TranscodeConfig(ctx)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	configuredTranscodeCap := cfg.TranscodeCap
	defaultTranscodeCap := config.Default().TranscodeCap
	// Hardware-acceleration probe: test-drive each candidate
	// accelerator with a short, time-boxed trivial encode at startup and record
	// what actually worked, so the Console reports verified accelerators instead
	// of a faith-based dropdown. The probe never errors — an empty working list
	// means software-only — so it never blocks boot; it is skipped under the
	// conformance seed to keep that deterministic world from shelling out to
	// ffmpeg. The result is stashed on the live config for the Console and logged
	// once here. A stored device hint is treated as a configured accelerator only
	// when it exactly matches the closed vocabulary; arbitrary device paths stay
	// compatible but are never echoed through the probe report.
	if !conformanceMode {
		var configured ffmpeg.HWAccel
		if accel, ok := ffmpeg.HWAccelFromString(transcodePolicy.Device); ok {
			configured = accel
		}
		probe := ffmpeg.ProbeHardwareAccel(ctx, ffmpeg.HWAccelOptions{ConfiguredPreference: configured})
		ffmpegRunner.SetHardwareAccel(ffmpeg.SelectHWAccel(probe, configured))
		cfg.HardwareAcceleration = hardwareAccelReport(probe)
		cfg.TranscodeCap = ffmpeg.TranscodeCapForHWAccel(probe)
		if configuredTranscodeCap != defaultTranscodeCap {
			cfg.TranscodeCap = configuredTranscodeCap
		}
		if cfg.HardwareAcceleration.SoftwareOnly() {
			logger.Info("hardware acceleration probe: none verified, using software transcode")
		} else {
			logger.Info("hardware acceleration probe: verified working", "accelerators", cfg.HardwareAcceleration.Working)
		}
	}
	// Resource governance for weak hardware: background generation
	// runs against a worker-pool size + CPU budget and a niced FFmpeg, while
	// realtime transcode is never niced or budgeted (it preempts generation).
	ffmpegRunner.SetGenerationNiceness(cfg.GenerationNiceness)
	// FFmpeg invocation hardening: when the server entrypoint has
	// set the shim path (its own executable), every ffmpeg/ffprobe spawn re-execs
	// through it to apply per-process rlimits and — on Linux — landlock
	// filesystem confinement. Empty (tests, or an unresolvable executable) leaves
	// hardening off so a non-server process never re-execs itself.
	ffmpegRunner.SetHardeningShim(cfg.FFmpegHardeningShimPath)
	consoleConfig, err := st.ConsoleConfigWithDefaults(ctx, store.ConsoleConfig{
		BackgroundWorkers:   cfg.GenerationWorkers,
		EventsRetentionDays: cfg.EventsRetentionDays,
	})
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	resourceGovernor := governor.NewWithOptions(governor.Options{
		GenerationWorkers:   consoleConfig.BackgroundWorkers,
		BackgroundCPUBudget: cfg.BackgroundCPUBudget,
	})
	observabilityController := observability.New(observability.DefaultRingSize)
	if err := observabilityController.SetEnabled(ctx, consoleConfig.ObservabilityEnabled); err != nil {
		_ = st.Close()
		return nil, err
	}
	// The conformance seed lands placeholder artwork with a pinned mtime so the
	// artwork version token is deterministic; serve it without a scaler so every
	// size bucket resolves to those exact vendored bytes (the FFmpeg scaler would
	// otherwise re-encode each bucket into machine-dependent JPEG, breaking
	// byte-stable UI-catalog capture). The frame-grab sweep is moot here — the
	// corpus Media are single-byte placeholders and already have seeded posters.
	artworkGrabber := artwork.FrameGrabber(ffmpegRunner)
	if conformanceMode {
		artworkGrabber = nil
	}
	artworkService := artwork.NewService(st, artworkGrabber, cfg.DataDir)
	jobRegistry := jobs.NewRegistry(st, "beamfall-server")
	jobRegistry.SetLogger(logger)
	// A completed scan re-arms the artwork sweep so new imports get posters
	// without a restart; the sweep job dedupes while one is already queued.
	assetService := genassets.NewService(st, ffmpegRunner, cfg.DataDir)
	skipDetection := configureSkipDetectionJobs(cfg.DataDir, st, ffmpegRunner, jobRegistry, resourceGovernor, jobWake, logger)
	// Manual identification uses the hardened scrape engine. Local image
	// metadata is plugin hook code, wired below by Library plugin ID.
	scrapeEngine := scrape.New(
		scrape.WithCredentials(secretFiles{dir: filepath.Join(cfg.DataDir, "secrets")}),
		scrape.WithConfigSource(st),
	)
	// Compiled filename-parser hooks, keyed by active plugin ID.
	// Hook implementations live in first-party plugin modules; Core only wires
	// hooks for bundles admitted by plugin.NewRegistry above.
	parsers := scanParsers(executablePlugins, hooks.Parsers)
	// Composed-picture renditions: parsers that describe a picture
	// for picture-less Media, discovered by their optional method.
	composers := newPictureComposers(st, parsers)
	// PIPE-1: scan triggers auto-identify per imported item through
	// this adapter. identifyService is constructed below (it needs scrapeEngine,
	// also built below), so the adapter holds a pointer set after construction; a
	// scan job can only fire after jobRegistry.Resume/enqueue further down, by
	// which point the pointer is set. The adapter bridges scan.ParsedMedia →
	// identify.ParsedMedia, keeping scan free of an internal/identify import (arch
	// §4 forbids that edge).
	autoIdentifier := &scanIdentifier{registry: executablePlugins}
	// Interactive-sidecar extensions the activated plugins recognise (ADR-0005
	// §4): scan records a matching sibling as an interactive sidecar track. The
	// extension VALUES are plugin data declared by the bundle; Core reads them
	// off the registry and never names one.
	interactiveSidecarExts := plugins.InteractiveSidecarExts()
	localExtractors := scanLocalExtractors(executablePlugins, hooks.LocalExtractors)
	// Plugin-owned local metadata mappers are validated against each activated
	// bundle and adapted into scan's shape-neutral hook, keyed by plugin ID.
	metadataHydrators := newHydrationAdapters(executablePlugins, st, hooks.HydrationMappers)
	scanHandler := scanHandlerWithWorkerGovernor(st, observingProber{inner: ffmpegRunner, observability: observabilityController}, ffmpegRunner, parsers, interactiveSidecarExts, autoIdentifier, localExtractors, metadataHydrators, resourceGovernor, artworkService)
	// First-party EmbeddingProvider seam. The flag records
	// operator intent and is read at process start. The bundled provider contains
	// non-semantic fingerprints, so it remains unavailable and indexes nothing
	// even when enabled. This adapter is transitional: no provider may become
	// Available() until separate reviewed work persists complete EmbeddingIdentity,
	// performs a bounded re-index, and defines an atomic cutover with rollback.
	enrichmentEnabled, err := st.EnrichmentEnabled(ctx)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	var embeddingProvider EnrichmentProvider
	if hooks.EnrichmentProvider != nil {
		embeddingProvider = hooks.EnrichmentProvider(enrichmentEnabled)
	}
	var encryptedBackupCipher func() store.EncryptedLibraryDBCipher
	jobRegistry.Register(scanpkg.JobKind, func(ctx context.Context, job store.Job) (jobs.Result, error) {
		start := time.Now()
		result, err := scanHandler(ctx, job)
		recordScanMetrics(ctx, observabilityController, st, job, time.Since(start), err)
		if err == nil {
			enqueueArtworkSweep(ctx, st, logger, jobWake)
			enqueueAssetSweep(ctx, st, logger, jobWake)
			skipDetection.enqueue(ctx, string(job.ID))
			// Populate the vector store for any newly scanned/identified Media
			// (ADR-0041 §2.3 post-scan pipeline). No-op unless a semantic provider
			// reports available; the bundled fingerprint provider never does.
			// Best-effort: a failure is logged but never fails the scan result.
			if n, embedErr := indexEmbeddings(ctx, st, embeddingProvider); embedErr != nil {
				logger.Warn("enrichment indexing failed", "error", embedErr)
			} else if n > 0 {
				logger.Info("enrichment indexed media", "count", n)
			}
			// Pre-compute library health counts so the Console dashboard reads
			// from a single cached row per Library rather than 5 live COUNT
			// queries per Library. Best-effort: a failure is logged
			// but never blocks the scan result.
			if cacheErr := st.RefreshLibraryHealthCache(ctx, time.Now().UTC()); cacheErr != nil {
				logger.Warn("library health cache refresh failed", "error", cacheErr)
			}
		}
		return result, err
	})
	jobRegistry.Register(retentionSweepJobKind, retentionSweepHandler(st, artworkService, logger))
	// Subtitle offset-nudge sweep. Nudge rows are keyed on a
	// subtitle track's byte fingerprint, so a row whose track no longer exists can
	// never match a lookup again; without this sweep they accumulate for the life
	// of the install. The sweep hashes the live sidecar inventory and deletes only
	// old rows whose fingerprint is no longer reachable.
	jobRegistry.Register(jobs.SubtitleOffsetPruneJobKind, jobs.SubtitleOffsetPruneHandler(st, subtitleSidecarGuard{}, time.Now))
	jobRegistry.Register(eventsCompactionJobKind, eventsCompactionHandler(st, logger))
	jobRegistry.Register(databaseBackupJobKind, databaseBackupHandler(st, filepath.Join(cfg.DataDir, "backups"), func() store.EncryptedLibraryDBCipher {
		if encryptedBackupCipher == nil {
			return nil
		}
		return encryptedBackupCipher()
	}))
	// ADR-0013: realtime transcode preempts generation — the sweeps yield and
	// retry while any transcode session is active.
	sweepHandler := artwork.SweepHandler(artworkService)
	jobRegistry.Register(artwork.SweepJobKind, governedGeneration(resourceGovernor, sweepHandler))
	assetSweepHandler := genassets.SweepHandler(assetService)
	jobRegistry.Register(genassets.SweepJobKind, governedGeneration(resourceGovernor, assetSweepHandler))
	// Decode-verify sampling: a low-priority background
	// sweep that sample-decodes media bytes and flags corrupt/truncated files as
	// broken in Library Health. Governed like the other generation sweeps so a
	// realtime transcode preempts it; seeded as the weekly Schedule
	// below (registerBuiltInSchedules).
	decodeVerifyService := decodeverify.NewService(st, ffmpegRunner)
	decodeVerifyHandler := decodeverify.SweepHandler(decodeVerifyService)
	jobRegistry.Register(decodeverify.SweepJobKind, governedGeneration(resourceGovernor, decodeVerifyHandler))
	// Conversion Workflow (apple ADR-0003 §1): a user-directed convert-to-managed-file
	// that drives the core FFmpeg engine to write a new managed file, preserves the
	// original in trash (invariant 6), and is per-op Profile-authority gated by the
	// store. Generation-class, so an interactive transcode/playback preempts it
	// through the governor like the other FFmpeg-driven jobs.
	convertService := convert.NewService(st, convert.NewFFmpegConverter(ffmpegRunner))
	jobRegistry.Register(convert.JobKind, governedGeneration(resourceGovernor, convert.Handler(convertService)))
	// Precomputed playback rendition cache root: the disposable,
	// budget-governed cache directory the precompute generation writes into and the
	// budget eviction sweeps. Disk-backed under DataDir, never a Library Version.
	precomputeCacheRoot := filepath.Join(cfg.DataDir, "playback-cache")
	precomputeAdmission := playback.NewPrecomputeResourceGovernor(cfg.PlaybackCacheBudgetBytes)
	precomputeHandler := playback.PrecomputeJobHandler{
		Store:     st,
		Generator: playback.FFmpegPrecomputeGenerator{Runner: ffmpegRunner, Composer: composers.generationComposer()},
		Admission: precomputeAdmission,
		Now:       time.Now,
	}
	jobRegistry.Register(playback.PrecomputeJobKind, governedGeneration(resourceGovernor, func(ctx context.Context, job store.Job) (jobs.Result, error) {
		err := precomputeHandler.Handle(ctx, job)
		if errors.Is(err, playback.ErrPrecomputeAdmission) {
			return jobs.Result{RunAfter: time.Now().UTC().Add(generationRetryDelay)}, jobs.ErrYielded
		}
		return jobs.Result{}, err
	}))
	precomputeEvictHandler := playback.PrecomputeEvictJobHandler{Store: st, Now: time.Now}
	jobRegistry.Register(playback.PrecomputeEvictJobKind, func(ctx context.Context, job store.Job) (jobs.Result, error) {
		return jobs.Result{}, precomputeEvictHandler.Handle(ctx, job)
	})
	// Composed-picture sweep (ADR-0057 §2 Schedule trigger): enqueues the
	// missing composed rendition of each Media whose plugin describes a picture.
	jobRegistry.Register(playback.ComposedSweepJobKind, func(ctx context.Context, _ store.Job) (jobs.Result, error) {
		_, err := composers.sweep(ctx, precomputeCacheRoot, time.Now().UTC(), logger)
		return jobs.Result{}, err
	})
	// Sweep dead-generation convert staging BEFORE any job worker resumes
	// (ADR-0174 ownership succession): after a restart no prior lease
	// generation can still be executing, so every convert staging entry is
	// residue by construction; recovery always re-encodes from source.
	if _, err := st.SweepConvertStaging(); err != nil {
		_ = st.Close()
		return nil, err
	}
	if _, err := playback.SweepPrecomputeResidue(ctx, st, precomputeCacheRoot); err != nil {
		_ = st.Close()
		return nil, err
	}
	if _, err := jobRegistry.Resume(ctx, time.Now()); err != nil {
		_ = st.Close()
		return nil, err
	}
	enqueueArtworkSweep(ctx, st, logger, jobWake)
	enqueueAssetSweep(ctx, st, logger, jobWake)
	skipDetection.enqueueBoot(ctx)
	authService := auth.NewServiceWithClock(st, readClock)
	authService.SetIntegrationAttemptSecret(cfg.StreamTokenKey)
	// Certification ceilings: hand auth the activated
	// plugins' ordered scales so Profile ceilings resolve into read-path
	// constraints.
	scales := map[string][]string{}
	for _, id := range plugins.IDs() {
		if bundle, ok := plugins.Bundle(id); ok && len(bundle.CertificationScale) > 0 {
			scales[string(id)] = bundle.CertificationScale
		}
	}
	authService.SetCertificationScales(scales)
	manifests := manifest.NewService(st, readClock, plugins)
	tokenService := streamtoken.NewService(cfg.StreamTokenKey, readClock)
	// Authority face: an Attached Origin Node never
	// issues Stream Tokens — issuance stays main-side. Main keeps issuing.
	tokenService.SetRole(cfg.Role)
	// Record issued Stream Tokens in the transparency ledger so the My Data
	// panel can present a Profile its own outstanding tokens. The
	// ledger never authorizes bytes — that stays the signed grant's job.
	tokenService.SetRecorder(st)
	playbackInfo := playback.NewService(st, tokenService, readClock)
	hlsSessions := playback.NewSessionsInDir(ffmpegRunner, cfg.TranscodeScratchDir, time.Now)
	hlsSessions.SetGovernor(resourceGovernor)
	hlsSessions.SetTranscodeCap(cfg.TranscodeCap)
	playbackInfo.SetSessions(hlsSessions)
	playbackInfo.SetPictureComposer(composers.playbackComposer())
	playbackInfo.SetAssets(playbackAssetsLocator{assetService})
	// Opaque selected-file selector (ADR-0098 area 2): minted from the same root
	// Stream Token key under a domain-separated derivation, so playbackInfo
	// advertises a selector instead of a raw content fingerprint.
	playbackInfo.SetSelector(playback.NewSelectorService(cfg.StreamTokenKey, readClock))
	pairingService := pairing.NewService(st, readClock)
	setupToken, err := bootstrapSetupToken(ctx, st, pairingService)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	if cfg.ReviewDemoEnrollment.Enabled {
		if err := configureReviewDemoEnrollment(cfg.ReviewDemoEnrollment.ProfileID, cfg.DataDir, pairingService, logger); err != nil {
			_ = st.Close()
			return nil, err
		}
	}
	mediaHTTP := mediahttp.NewHandler(st, authService, tokenService, artworkService, hlsSessions)
	mediaHTTP.SetAssets(assetService)
	mediaHTTP.SetSubtitles(ffmpegRunner, filepath.Join(cfg.DataDir, "subtitles"))
	// Bounded RAM hot-segment fan-out cache: only enabled when the
	// operator sets a positive budget; otherwise every precomputed segment stays
	// disk-served. Playlists are never RAM-cached; Stream Token + Reveal checks
	// still run before any cache lookup (the SetHotSegmentCache contract).
	if cfg.HotSegmentCacheBytes > 0 {
		mediaHTTP.SetHotSegmentCache(cfg.HotSegmentCacheBytes)
	}
	surfaces := reveal.NewRegistry()
	surfaces.Register(reveal.Surface{ID: "Query.navigationManifest", Class: reveal.ContentJSON})
	surfaces.Register(reveal.Surface{ID: "Query.sourceItems", Class: reveal.ContentJSON})
	surfaces.Register(reveal.Surface{ID: "Query.mediaForEntity", Class: reveal.ContentJSON})
	surfaces.Register(reveal.Surface{ID: "Query.search", Class: reveal.ContentJSON})
	// ADR-0041 semantic search (#62/#106): searchResult carries reveal-filtered
	// result titles + scores; semanticCapability is an aggregate of per-Library
	// index counts. Both are reveal-filtered in the resolver — registered here so
	// the Reveal-contract audit catalog stays complete.
	surfaces.Register(reveal.Surface{ID: "Query.searchResult", Class: reveal.ContentJSON})
	surfaces.Register(reveal.Surface{ID: "Query.semanticCapability", Class: reveal.Aggregate})
	surfaces.Register(reveal.Surface{ID: "Query.artworkCandidates", Class: reveal.ContentJSON})
	surfaces.Register(reveal.Surface{ID: "Mutation.pinArtworkCandidate", Class: reveal.ContentJSON})
	surfaces.Register(reveal.Surface{ID: "Query.currentSession", Class: reveal.ContentJSON})
	surfaces.Register(reveal.Surface{ID: "Query.jobs", Class: reveal.Aggregate})
	surfaces.Register(reveal.Surface{ID: "Query.job", Class: reveal.Aggregate})
	// Manual identification: the scrape engine reads scraper
	// credentials from per-secret 0600 files under <DataDir>/secrets/<authref>
	// (invariant 5). A missing secret errors at use, never at boot.
	mediaHTTP.SetArtworkCandidateFetcher(scrapeEngine)
	// Subscription ingest: the poll job fetches every feed
	// Subscription through the same hardened engine, downloads new enclosures,
	// and hands them to the standard scan pipeline. A completed pass re-arms
	// itself at the default cadence (ADR-0032's Scheduler owns cadence later).
	subscriptionPoller := ingest.NewPoller(st, scrapeEngine)
	subscriptionPoller.SetLogger(logger)
	pollHandler := ingest.Handler(subscriptionPoller)
	jobRegistry.Register(ingest.PollJobKind, pollHandler)
	// qBittorrent ingest: a poll job pulls completed downloads,
	// routes them by the explicit category->Library map, hardlinks payloads into
	// each target Library's Incoming Folder, and hands off to the standard scan.
	// The handler is a no-op when qBittorrent is unconfigured, so the job and its
	// schedule always exist (the Console wires config live later).
	qbitIngestor, err := buildQBittorrentIngestor(ctx, cfg, st, logger, jobWake)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	jobRegistry.Register(qbittorrent.PollJobKind, qbittorrent.Handler(qbitIngestor))
	// Migration Wizard import: the generic import job handler.
	// It resolves the run's source parser, runs the path-based match chain, and
	// applies behavioral state append-only. migrationSourceResolver dispatches
	// the Film & TV plugin's compiled importers (Plex/Emby/Jellyfin/NFO, MIG-2)
	// over the operator-supplied source path the run carries; an enqueued run
	// for an unknown (plugin, importer) pair fails with a clear "no parser" error
	// rather than silently importing nothing.
	// Scrobble-import match relations (ADR-0098 area 1): read the plugin-owned
	// relation slugs off the activated bundles so the generic matcher confirms
	// artist/album without hardcoding a music relation name.
	scrobbleSelectors := scrobbleRelationSelectors(executablePlugins)
	jobRegistry.Register(migrate.JobKind, migrate.Handler(st, migrationSourceResolver{plugins: executablePlugins, factories: hooks.MigrationSourceFactories}, migrate.WithResumePolicyResolver(migrationResumePolicyResolver{store: st, plugins: executablePlugins}), migrate.WithScrobbleRelationSelectors(scrobbleSelectors)))
	identifyService := identify.NewService(scrapeEngine, executablePlugins, st)
	identifyService.SetArtwork(scrapeEngine, artworkService)
	identifyService.SetArtworkCandidates(st)
	identifyService.SetFields(st)
	identifyService.SetEntities(st)
	identifyService.SetFingerprints(st)
	registerIdentifyScorers(identifyService, executablePlugins, hooks.IdentifyScorers)
	// Wire PIPE-1's scan-time pipeline: the Unmatched queue (below-threshold
	// parking) + the FREEZE-3 import match-pin (rescan respects an operator fix),
	// then hand the configured service to the scan adapter created above.
	identifyService.SetAutoStores(st, st)
	autoIdentifier.service = identifyService
	// Construct the confined proxy archive before native root observation starts.
	// A malformed/replaced archive root therefore cannot leak watcher resources.
	proxyTee, err := proxytee.New(
		st,
		authService,
		tokenService,
		scrapeEngine,
		filepath.Join(cfg.DataDir, "proxy-archive"),
	)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	proxyTeeOwned := true
	defer func() {
		if proxyTeeOwned {
			_ = proxyTee.Close()
		}
	}()
	watcherDisabled := envEnabled("BEAMFALL_WATCHER_DISABLED")
	watcherService := newWatcherService(watcher.Options{
		Debounce: 100 * time.Millisecond,
		EnqueueScan: func(ctx context.Context, libraryID store.LibraryID, root string) {
			enqueueScanJob(ctx, st, logger, jobWake, libraryID, root)
		},
		Logger:   logger,
		Disabled: watcherDisabled,
	})
	watcherOwned := true
	defer func() {
		if watcherOwned {
			_ = closeOwnedWatcher(watcherService)
		}
	}()
	scheduleService := schedulepkg.NewService(wakingScheduleStore{Store: st, wake: jobWake})
	scheduleService.SetLogger(logger)
	registerBuiltInSchedules(ctx, st, scheduleService, precomputeCacheRoot, cfg.PlaybackCacheBudgetBytes, composers != nil)
	if err := scheduleService.Reconcile(ctx, time.Now().UTC()); err != nil {
		_ = st.Close()
		return nil, err
	}
	// ADR-0058 third-party Integration Worker lifecycle controller: the Console
	// Plugin Surface routes apply-update through it so the permission-expansion
	// re-approval rule runs server-side. Dormant until a worker artifact exists.
	integrationController := integrationLifecycle(st)
	// Provider subtitle downloads ride the same operator-approved outbound
	// allowlist the Integration Workers ride: the lifecycle controller is the
	// policy source, HardenedOutboundProxy enforces it, and the adapter in
	// provider_subtitle_egress.go translates mediahttp's own request/response
	// types onto it. Without this line the fetch seam stays unavailable — it
	// never degrades to a raw client.
	wireProviderSubtitleFetch(mediaHTTP, integrationhost.NewHardenedOutboundProxy(integrationController))
	// Encrypted-Library key model: the libencrypt.Manager
	// persists the per-Library DEK wraps + KEK to the same 0600 secretFiles store
	// (invariant #5 — no key material in any DB column). It uses production
	// argon2id params for the passphrase-unseal path. Auto-unseal at boot happens
	// below, once the Library list is readable.
	libEncryptManager := libencrypt.NewManager(secretFiles{dir: filepath.Join(cfg.DataDir, "secrets"), dataDir: cfg.DataDir})
	cryptoOwners, err := st.LibraryEncryptionOwners(ctx)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	ownerIDs := make([]string, 0, len(cryptoOwners))
	for _, id := range cryptoOwners {
		ownerIDs = append(ownerIDs, string(id))
	}
	refMigration, err := libEncryptManager.MigrateLegacyRefs(ownerIDs)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	if len(refMigration.Moved) != 0 {
		logger.Info("libencrypt secure-store legacy refs migrated", "moved", len(refMigration.Moved))
	}
	if len(refMigration.Orphans) != 0 {
		logger.Warn("libencrypt secure-store refs have no Library owner; retained for recovery", "orphan_refs", refMigration.Orphans)
	}
	if err := libEncryptManager.RecoverProvisionJournals(ownerIDs, func(owner string) error {
		return st.AbortEncryptedLibraryDBProvision(ctx, store.LibraryID(owner))
	}); err != nil {
		_ = st.Close()
		return nil, err
	}
	if err := libEncryptManager.RecoverRewrapJournals(ownerIDs); err != nil {
		_ = st.Close()
		return nil, err
	}
	encryptedBackupCipher = func() store.EncryptedLibraryDBCipher {
		return appLibEncryptDBCipher{manager: libEncryptManager}
	}
	// Sealed Library byte/asset exclusion (ADR-0098 area 2): the same Sealed/
	// Unsealed state that gates key material now gates every Media byte/asset
	// route, the playbackInfo decision, and Generated Asset generation — a Sealed
	// Library gets no stream/sidecar/asset/artwork and serves no previously minted
	// token. nil predicate (no encryption) would leave these unchanged; here the
	// manager always exists, so wire it.
	// Only an ENCRYPTED (provisioned) Library can be Sealed. Manager.Sealed
	// reports "DEK not in memory", which is true for every ordinary unprovisioned
	// Library too — so gate on Provisioned first or every plain Library would read
	// as sealed and lose all bytes.
	sealedPredicate := func(libraryID store.LibraryID) bool {
		id := string(libraryID)
		return libEncryptManager.Provisioned(id) && libEncryptManager.Sealed(id)
	}
	// Encrypted (provisioned) predicate — Sealed OR Unsealed. The
	// plaintext-derivative generators fail closed for every Encrypted Library, not
	// just the Sealed sub-case: no encrypted-plane write path exists yet,
	// so an Unsealed Encrypted Library must still get no plaintext
	// generated asset or artwork on the shared plane.
	encryptedPredicate := func(libraryID store.LibraryID) bool {
		return libEncryptManager.Provisioned(string(libraryID))
	}
	playbackInfo.SetSealed(sealedPredicate)
	mediaHTTP.SetSealed(sealedPredicate)
	mediaHTTP.SetEncryptedBytes(appLibEncryptDBCipher{manager: libEncryptManager})
	assetService.SetSealed(sealedPredicate)
	assetService.SetEncrypted(encryptedPredicate)
	artworkService.SetSealed(sealedPredicate)
	artworkService.SetEncrypted(encryptedPredicate)
	// Encrypted-Library crash-recovery derivative reconcile (ADR-0045 §reconcile,
	// ENCPRIV-10d): install the app-layer purge seam BEFORE the boot-unseal loop
	// below, so the first unseal of each Library already runs the reconcile. At the
	// unseal→register flip the store hands the reconciler the live media inventory
	// (IDs + fingerprints) and it purges any crash-stranded plaintext derivative on
	// the shared plane — artwork, generated assets, subtitle sidecars, precomputed
	// HLS, and orphaned live-transcode session dirs. If a purge fails the store
	// fails the unseal closed (Library stays Sealed),
	// never serving a plane with unaccounted derivatives.
	st.SetEncryptedDerivativeReconciler(newEncryptedDerivativeReconciler(artworkService, assetService, mediaHTTP, hlsSessions, precomputeCacheRoot))
	// Auto-unseal at boot: each auto-mode encrypted Library is
	// unsealed via its KEK; passphrase-mode Libraries stay Sealed until the
	// operator unseals them (absent from search/Sources, like Reveal-filtering).
	// An operator-scope filter (Revealed, no allow-list) enumerates every Library
	// including restricted/encrypted ones. A per-Library failure never aborts
	// boot; the count of still-Sealed Libraries is logged (no key material).
	if encLibs, err := st.Libraries(ctx, store.LibraryFilter{Revealed: true, AllLibraries: true}); err != nil {
		logger.Warn("encrypted-library boot unseal skipped: library list unavailable", "error", err)
	} else {
		var encIDs []string
		for _, lib := range encLibs {
			if lib.Encrypted {
				encIDs = append(encIDs, string(lib.ID))
			}
		}
		sealed := libEncryptManager.BootUnsealPrepared(encIDs, func(id string, cipher libencrypt.OperationCipher) error {
			err := st.UnsealEncryptedLibraryDB(ctx, store.LibraryID(id), appOperationDBCipher{cipher: cipher})
			if err != nil {
				logger.Warn("encrypted-library DB boot unseal failed closed", "libraryID", id, "error", err)
			}
			return err
		})
		if len(sealed) > 0 {
			logger.Info("encrypted libraries awaiting unlock", "sealed", len(sealed), "total_encrypted", len(encIDs))
		}
	}
	// Device push-token registration: the persisting
	// registrar replaces the old no-op seam. It is gated by the same default-off
	// flag the seam used (config.PushRegistrationEnabled) and persists the token
	// against the enrolled-device record so a later credential configure can
	// dispatch without re-enrollment. The token is an opaque routing handle, not
	// an invariant-#5 secret. Dispatch (APNs/FCM/Web-Push) is a separate,
	// operator-credential-gated layer built in internal/push; it stays dormant
	// until credentials are configured.
	graphPushRegistrar := pushRegistrarAdapter{
		registrar: push.NewRegistrar(pushTokenStore{store: st}, cfg.PushRegistrationEnabled),
	}
	// Parental extension-request push fan-out: an
	// approval-pending request is pushed to every adult Profile's enrolled
	// devices through the push.Dispatcher. The dispatcher is built from whatever
	// operator backends are configured; APNs/FCM/Web-Push credentials are still
	// deferred, so it is DORMANT (no backends → Configured()==false) and the
	// fan-out is a best-effort no-op today — the request, the adults' inbox
	// notifications, and the managed Profile's SSE outcome all still fire, and a
	// later credential configure activates real push without touching this wiring.
	// The payload carries only {type, eventId} (invariant #5).
	pushDispatcher := push.NewDispatcher(nil)
	extensionPusher := graph.NewExtensionStorePusher(st, pushDispatcher)
	accessRequestPusher := graph.NewAccessRequestStorePusher(st, pushDispatcher)
	// Ambient Group Conductor: the in-memory registry of
	// beat-accurate multi-room audio sync groups. Ephemeral by construction —
	// nothing it holds is persisted (no migration), so a restart dissolves every
	// group and members re-join, exactly like the Ambient Queue. Its sync signals
	// ride the existing /api/events SSE outbox; the LAN-only calibration endpoint
	// lives in the server package.
	// One shared conductor Registry backs both the Watch-Party graph surface and
	// playbackInfo's party-membership oracle: a member blocked
	// by the transcode cap degrades to the lowest rung instead of being refused,
	// and party membership is read from the same authoritative
	// in-memory state the accept path mutates.
	conductorRegistry := conductor.NewRegistry()
	playbackInfo.SetPartyRoster(conductorRegistry)
	// Precomputed playback rendition cache Console controls: wired only
	// when the operator set a disk budget, so a build with no cache configured
	// reports the cache disabled and refuses the generation/eviction mutations.
	var graphPlaybackCache graph.PlaybackCacheController
	if cfg.PlaybackCacheBudgetBytes > 0 {
		graphPlaybackCache = playbackCacheController{
			store:     st,
			cacheRoot: precomputeCacheRoot,
			budget:    cfg.PlaybackCacheBudgetBytes,
			now:       time.Now,
			wake:      jobWake,
			composer:  composers.playbackComposer(),
		}
	}
	// Device-key revocation aborts the revoked device's in-flight transcodes
	// through the playback Sessions manager. The device's event
	// streams self-terminate on their next per-event scope re-evaluation; this
	// closes the one live transport the Revoked flag does not stop on its own.
	// Opt-in version check. Default-OFF: the service is wired but
	// inert — it makes NO outbound request until the operator opts in via the
	// stored console config. NO telemetry (invariant #5): the check is a plain
	// GET of the public release manifest and sends nothing about this server.
	// Empty serverVersion lets the graph layer use its own running-version
	// constant as the single source of truth.
	// Semantic-search read seam: wiring requires both operator
	// opt-in and an available semantic provider. The bundled fingerprint provider
	// is unavailable, so it drives neither indexing nor query search and clients
	// fall back to keyword search. The transitional fence above applies to both
	// seams: no provider may become Available() until the separately reviewed
	// identity-persistence, bounded-re-index, and atomic-cutover-with-rollback work
	// is complete.
	var graphQueryEmbedder graph.QueryEmbedder
	if enrichmentEnabled && embeddingProvider != nil && embeddingProvider.Available() {
		graphQueryEmbedder = queryEmbedder{p: embeddingProvider}
	}
	// LB-2: BEAMFALL_UPDATE_CHECK_BASE_URL lets operators point the version-check
	// at a self-hosted or air-gapped mirror without recompiling. When unset the
	// default https://updates.beamfall.com/channels is used. The check remains
	// default-off (opt-in in the setup wizard); this env var only affects WHERE
	// the request goes, never WHETHER it fires (invariant #5 still enforced).
	updateOpts := []updatecheck.Option{}
	if baseURL := os.Getenv("BEAMFALL_UPDATE_CHECK_BASE_URL"); baseURL != "" {
		updateOpts = append(updateOpts, updatecheck.WithBaseURL(baseURL))
	}
	graphUpdateChecker := updateCheckAdapter{service: updatecheck.New(updateOpts...)}
	graphFeedSearcher := activeFeedSearcher(executablePlugins, hooks.FeedSearcher, scrapeEngine, secretFiles{dir: filepath.Join(cfg.DataDir, "secrets")})
	// Remote access (ADR-0012 §1 direct HTTPS + ACME; ADR-0018/0025 self-hosted
	// relay carrier). Both paths are off by default (LAN-only). The relay token
	// lives in <DataDir>/secrets/relay-token (0600, invariant 5).
	directHTTPS, err := remoteaccess.NewDirectHTTPS(cfg.RemoteAccess)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	// Baseline self-signed TLS identity: every server holds one,
	// generated + persisted under <DataDir>/tls on first boot and reused after,
	// so overlay endpoints and the relay carrier can terminate client TLS without
	// operator ACME and the SPKI pin stays stable across restarts.
	selfSigned, err := remoteaccess.LoadOrCreateSelfSigned(filepath.Join(cfg.DataDir, "tls"))
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	relayClient, err := remoteaccess.NewRelayClient(cfg.RemoteAccess, filepath.Join(cfg.DataDir, "secrets"))
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	// LAN port for the dual-URL endpoint set: the configured
	// listen port the server binds. An ephemeral ":0" config yields 0 here and
	// the resolver omits LAN/overlay URLs until a real port is known — honest
	// rather than advertising a port no client can reach.
	lanPort := 0
	if _, portStr, err := net.SplitHostPort(cfg.Addr); err == nil {
		lanPort, _ = strconv.Atoi(portStr)
	}
	directTLSActive := &atomic.Bool{}
	graphRemoteAccess := remoteAccessProvider{direct: directHTTPS, directTLSActive: directTLSActive, relay: relayClient, lanPort: lanPort, overlayTLSActive: true}
	// Client certificate pinning: hand the pairing service
	// the SPKI of the SELF-SIGNED identity — always, regardless of whether direct
	// HTTPS/ACME is configured. This is the identity that actually backs every
	// self-signed advertised endpoint: overlay (LAN/overlay-IP SNI), the relay
	// carrier (loopback backend, no FQDN SNI), and direct when no ACME. The serving
	// path composes by SNI (SelfSignedIdentity.ComposeWithACME): only the operator's
	// public FQDN is served the ACME cert (for browser trust — browsers cannot pin),
	// while every other ServerName is served this self-signed identity. So the pin a
	// native client captures at LAN enrollment MUST be the self-signed SPKI, or its
	// pinned reconnect over overlay/relay would fail the pin check against the
	// self-signed cert those paths present. Pinning ACME here (the former wiring)
	// captured the WRONG identity whenever direct HTTPS was configured. ACME stays
	// purely browser SNI on the direct FQDN; it is never the pinned identity.
	pairingService.SetSPKIProvider(selfSigned)
	beamService := beam.NewService(st, authService, time.Now)
	graphOptions := graph.Options{
		AuditRecorder:                observability.NewAuditLogger(logger),
		ObservabilityController:      observabilityController,
		HardwareAcceleration:         cfg.HardwareAcceleration,
		Identify:                     identifyService,
		Artwork:                      artworkService,
		StreamTokens:                 tokenService,
		ArtworkCandidateFetcher:      scrapeEngine,
		ArtworkCandidateImporter:     artworkService,
		ScanObserver:                 scanObserverAdapter{service: watcherService},
		LibraryMutationObserver:      libraryMutationObserver{store: st, watcher: watcherService, schedule: scheduleService, logger: logger},
		ScheduleUpdater:              scheduleService,
		JobRunner:                    jobRegistry,
		PlaybackCache:                graphPlaybackCache,
		Governor:                     resourceGovernor,
		FeedSearcher:                 graphFeedSearcher,
		AssetSweep:                   func(ctx context.Context) { enqueueAssetSweep(ctx, st, logger, jobWake) },
		SubscriptionSecrets:          secretFiles{dir: filepath.Join(cfg.DataDir, "secrets")},
		ScraperCredentialStore:       secretFiles{dir: filepath.Join(cfg.DataDir, "secrets")},
		ScraperCredentialInvalidator: scrapeEngine,
		PushRegistrar:                graphPushRegistrar,
		Conductor:                    conductorRegistry,
		QueryEmbedder:                graphQueryEmbedder,
		TranscodeTerminator:          hlsSessions,
		TranscodeAdmission:           hlsSessions,
		UpdateChecker:                graphUpdateChecker,
		WatcherStatus:                watcherStatusAdapter{service: watcherService},
		IntegrationLifecycle:         integrationController,
		RemoteAccess:                 graphRemoteAccess,
		LibEncrypt:                   libEncryptManager,
		EncryptedLibraryDBLifecycle:  st,
		EncryptedGeneratedAssets:     assetService,
		ExtensionPusher:              extensionPusher,
		AccessRequestPusher:          accessRequestPusher,
	}
	// The ingestor is always non-nil now (inert until configured), so both the
	// read adapter and the write-side configurer are
	// always wired: the Console can configure ingest from scratch at runtime.
	if qbitIngestor != nil {
		graphOptions.QBittorrentIngest = qbittorrentIngestAdapter{ingestor: qbitIngestor}
		graphOptions.QBittorrentIngestConfigurer = qbittorrentConfigurer{
			ingestor: qbitIngestor,
			store:    st,
			secrets:  secretFiles{dir: filepath.Join(cfg.DataDir, "secrets")},
		}
	}
	graphHandler := graph.NewHandler(manifests, authService, st, plugins, graphOptions)
	graphServer := serverGraphHandler{Handler: graphHandler}
	srv := server.New(
		manifests,
		playbackInfo,
		beamService,
		pairingService,
		graphServer,
		mediaHTTP,
		authService,
		st,
		server.WithUnmatchedArtworkCreator(graphServer),
		server.WithCompatRefusalJournalDir(filepath.Join(cfg.DataDir, "compat-refusals")),
	)
	// Guideline 1.2 UGC moderation substrate (NATIVEMGR-4, apple ADR-0004 §4):
	// file-backed per-Profile content report/block store + published moderation
	// contact path under the data dir (versioned bounded JSON, 0600 — no schema
	// migration, no GraphQL type; invariants 1-3/5). A corrupt store fails app.New
	// loudly rather than silently resetting, so a clobbered block record is noticed.
	moderationService, err := moderation.Open(
		filepath.Join(cfg.DataDir, "moderation", "feed.json"),
		moderationContactFromEnv(),
	)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	srv.SetModeration(moderationService)
	// Seed /health with the runtime capability state: apply the
	// operator's persisted Workflow toggle overrides so a flipped capability is
	// reflected. A read error degrades to the compiled-in default rather than
	// failing boot.
	capabilityOverrides, _ := st.CapabilityStates(ctx)
	capabilityReport := capabilities.CurrentWithOverrides(capabilityOverrides)
	srv.SetCapabilities(capabilityReport.Capabilities)
	srv.SetSourceOfferURL(capabilityReport.CorrespondingSourceOfferURL)
	srv.SetPublicMetrics(cfg.PublicMetrics)
	// GCR-012-H: /metrics admission cannot rest on the peer address, because the
	// default bind is loopback and a same-host reverse proxy presents a loopback
	// peer for traffic it relays from anywhere — and one that neither declares
	// itself nor adds a hop header is indistinguishable from a local client. So
	// every non-public scrape must present this run's credential, which lives
	// 0600 under the data dir: scraping follows from being able to read that
	// file, not from what a fronting proxy chose to send. A provisioning failure
	// leaves no credential installed, which refuses every bearer: /metrics closes
	// entirely rather than opening up, and boot continues because an unwritable
	// secrets directory must not take the server down.
	if metricsScrapeToken, err := provisionMetricsScrapeToken(cfg.DataDir); err != nil {
		logger.Error("metrics scrape credential unavailable; /metrics is unscrapeable until the secrets directory is writable", "err", err)
	} else {
		srv.SetMetricsScrapeToken(metricsScrapeToken)
	}
	// Reveal-filtered SSE event stream: the store is both the
	// outbox reader and the reveal-filtered Media lookup the surface gates on.
	srv.SetEvents(st)
	srv.SetPushNotifications(st)
	// The /v1/ stable developer contract: the store supplies the
	// reveal-filtered catalog/session reads, while scan-job construction lives
	// here at the composition root since server cannot import internal/scan.
	srv.SetV1Store(st)
	srv.SetV1ScanEnqueuer(v1ScanEnqueuer(st, jobWake))
	srv.SetBackupIngestEncryptor(appLibEncryptDBCipher{manager: libEncryptManager})
	// Proxy-tee stream-first archive (ADR-0052 §2.1-2.2): on first play of a
	// provisional Media the server ranges the origin enclosure to the client
	// through a Stream Token while teeing the bytes to disk, then promotes the
	// copy in place. It fetches the origin through the same hardened (ADR-0012 §9
	// SSRF) engine as scraping/ingest, so a malicious feed cannot make the server
	// hit internal addresses. The route is always mounted (server.proxyStream);
	// the registry that maps a provisional Media to its server-only origin URL is
	// driven by the podcast plugin (built behind Music, ADR-0030 §10).
	// Generic Offline Pack substrate (ADR-0098 area 6): domain-agnostic pack
	// rows + Pack-subject Stream Token grants + manifest/key serving. The Music
	// target resolvers (Albums/Playlists/Collections/Tracks/frozen Stations)
	// register as a plugin surface; the Station play-queue generator is a later
	// MUSIC ticket, so the stub StationSnapshotSource is wired until it lands.
	offlinePackPin, err := selfSigned.ServerSPKIPin(ctx)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	offlinePackAuthority, err := offlinepack.LoadOrCreateAuthority(
		filepath.Join(cfg.DataDir, "offline-pack"),
		"spki-sha256("+offlinePackPin+")",
		readClock,
	)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	offlinePackAuthority.SetIssueEnabled(!cfg.Role.Attached())
	offlinePacks := offlinepack.NewService(st, offlinePackAuthority, readClock)
	offlinePacks.SetPackRoot(filepath.Join(cfg.DataDir, "offline-packs"))
	offlinePacks.SetDeviceKeyAttestationVerifier(hooks.OfflinePackAttestationVerifier)
	// A Sealed Library serves NOTHING (ADR-0098 area 2): stamp the same Sealed
	// predicate used by the byte routes so a Sealed Library's Media is never
	// frozen into a pack manifest and a member sealed AFTER issue is denied at
	// serve time.
	offlinePacks.SetSealed(sealedPredicate)
	// The pack byte route resolves each encrypted member's immutable identity
	// from its persisted media-file row (content fingerprint keyed by the
	// library's file inventory) — the path is only the lookup key, never the
	// identity.
	offlinePacks.SetEncryptedContent(appEncryptedContentSource{manager: libEncryptManager}, encryptedPredicate, func(libraryID store.LibraryID, path string) (string, string, error) {
		files, err := st.MediaFilesForLibrary(context.Background(), libraryID)
		if err != nil {
			return "", "", err
		}
		for _, file := range files {
			if file.Path == path && file.ContentFingerprint != "" {
				return string(libencrypt.ObjectClassMediaBytes), file.ContentFingerprint, nil
			}
		}
		return "", "", errors.New("encrypted media object identity unavailable")
	})
	registerOfflinePackResolvers(offlinePacks, st, executablePlugins, hooks.OfflinePackResolvers)
	jobRegistry.Register(offlinepack.BuildJobKind, func(ctx context.Context, job store.Job) (jobs.Result, error) {
		_, err := offlinePacks.HandleBuildJob(ctx, job.Payload)
		return jobs.Result{}, err
	})
	jobRegistry.Register(offlinepack.ExpireJobKind, func(ctx context.Context, job store.Job) (jobs.Result, error) {
		_, err := offlinePacks.HandleExpireJob(ctx)
		return jobs.Result{}, err
	})
	// Bulk photo export (ADR-0045 §2.9 mode 2): a job-substrate job that emits
	// every byte-exact original for a Custodial Library plus a Takeout-style
	// sidecar manifest, so a Profile's own photo data exports elsewhere. The
	// same libencrypt seam that serves Encrypted-Library bytes recovers byte-exact
	// originals from at-rest ciphertext; a Sealed Library is absent until Unsealed.
	photoExport := photoexport.NewService(st, appLibEncryptDBCipher{manager: libEncryptManager})
	jobRegistry.Register(photoexport.JobKind, func(ctx context.Context, job store.Job) (jobs.Result, error) {
		return jobs.Result{}, photoExport.RunJob(ctx, job.Payload)
	})
	packHandlers := offlinepack.NewHTTPHandlers(offlinePacks, authService)
	srv.SetPackHandlers(packHandlers.Authority, packHandlers.Manifest, packHandlers.Content, packHandlers.Key, packHandlers.Status)
	graphHandler.SetOfflinePacks(offlinePacks)
	srv.SetBuildMetadata("", cfg.SeedProfile, seedProfileVersion(cfg.SeedProfile))
	srv.SetTestFaults(cfg.SeedProfile == string(store.SeedProfileAcceptance))
	srv.SetObservability(observabilityController)
	// Ops observability: /health readiness + active-session and
	// library-size gauges come from the store; the active-transcode gauge from
	// the resource governor (its live count is the same one generation
	// preemption keys on). No new bookkeeping — these read existing state.
	srv.SetMetricsSources(st, st, resourceGovernor, st)
	// ADR-0146 §8 job-queue-age SLI: the store answers "how long has the oldest
	// waiting job sat", derived from its durable server-clock timestamps.
	srv.SetJobQueueSource(st)
	for _, surface := range srv.Surfaces() {
		surfaces.Register(surface)
	}
	// Webhook Emitter Integration Plugin: an opt-in consumer of
	// the same event outbox the SSE surface reads. nil (dormant) unless the
	// operator has configured at least one endpoint in
	// <DataDir>/webhook-endpoints.json; Run starts its goroutine only then.
	webhookEmitter, err := buildWebhookEmitter(cfg, st, logger)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	// Scrobble Integration Plugin: an opt-in consumer of the
	// unwatched→watched transition (the graph playback path, NOT a new SSE event
	// kind). Dormant unless the operator has activated an adapter in
	// <DataDir>/scrobble-adapters.json. When active, the graph handler's
	// WatchedObserver feeds it and Run drains its queue.
	scrobbleDispatcher, err := buildScrobbleDispatcher(cfg, st, logger, executablePlugins, hooks.ScrobbleAdapters)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	// Third-party Integration Worker manager: supervises any installed,
	// enabled, verified worker out-of-process. nil (dormant) unless an operator has
	// installed a worker through the Console lifecycle flow; Run starts its
	// supervision goroutine only then.
	integrationMgr, err := buildIntegrationManager(ctx, st, executablePlugins, secretFiles{dir: filepath.Join(cfg.DataDir, "secrets")}, integrationController, logger, cfg.IntegrationWorkerHardeningShimPath)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	if integrationMgr != nil {
		graphHandler.SetIntegrationActionRunner(integrationMgr)
	}
	if scrobbleDispatcher != nil {
		graphHandler.SetWatchedObserver(scrobbleWatchedObserver{dispatcher: scrobbleDispatcher})
		// MUSIC-7 (ADR-0098 area 7): the read-only status source (dead-letter +
		// skip feeds + configured adapters) for the operator diagnostic surface,
		// and the richer-outbound-metadata resolver (artist/album + MBIDs from the
		// activated Music plugin's declared relations/fields).
		graphHandler.SetScrobbleStatusSource(scrobbleStatusSource{dispatcher: scrobbleDispatcher})
		graphHandler.SetOutboundMetadataResolver(newScrobbleMetadataResolver(st, executablePlugins))
	}
	compatHandler, err := compat.New(compat.Config{
		Enabled: cfg.JellyfinCompat.Enabled, AllowedCIDRs: cfg.JellyfinCompat.AllowedCIDRs,
		TrustedProxyCIDRs: cfg.JellyfinCompat.TrustedProxyCIDRs,
	}, authService, nil)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	compatHandler.SetRelayedRequestPredicate(server.RequestFromRelay)
	srv.SetProxyTee(proxyTee.ServeProxy)
	app := &App{
		cfg:                    cfg,
		store:                  st,
		plugins:                plugins,
		server:                 srv,
		compat:                 compatHandler,
		jobs:                   jobRegistry,
		hls:                    hlsSessions,
		governor:               resourceGovernor,
		surfaces:               surfaces,
		watcher:                watcherService,
		watcherDisabled:        watcherDisabled,
		schedule:               scheduleService,
		proxyTee:               proxyTee,
		webhook:                webhookEmitter,
		scrobble:               scrobbleDispatcher,
		integration:            integrationMgr,
		logger:                 logger,
		setupToken:             setupToken,
		jobWake:                jobWake,
		jobClaimSafetyInterval: jobClaimSafetyInterval,
		directHTTPS:            directHTTPS,
		selfSigned:             selfSigned,
		relay:                  relayClient,
		addrReady:              make(chan struct{}),
		directTLSActive:        directTLSActive,
		drainGrace:             defaultJobDrainGracePeriod,
		drainAfter:             time.After,
	}
	proxyTeeOwned = false
	watcherOwned = false
	return app, nil
}

type subtitleSidecarGuard struct{}

func (subtitleSidecarGuard) CheckSize(format string, size int64) error {
	err := subtitleingest.CheckSize(format, size)
	if errors.Is(err, subtitleingest.ErrTooLarge) {
		return jobs.ErrSubtitleSidecarTooLarge
	}
	return err
}

func (subtitleSidecarGuard) Guard(format string, raw []byte) error {
	return subtitleingest.Guard(format, raw)
}

func activePluginBundles(bundles []plugin.Bundle, overrides map[string]bool, logger *slog.Logger) []plugin.Bundle {
	active := make([]plugin.Bundle, 0, len(bundles))
	for _, bundle := range bundles {
		if state, has := overrides[string(bundle.ID)]; has && !state {
			logger.Info("plugin deactivated by Console override; skipping bundle", "plugin", string(bundle.ID))
			continue
		}
		active = append(active, bundle)
	}
	return active
}

func executablePluginIDs(bundles []plugin.Bundle) map[plugin.ID]bool {
	ids := make(map[plugin.ID]bool, len(bundles))
	for _, bundle := range bundles {
		ids[bundle.ID] = true
	}
	return ids
}

func seedProfileVersion(profile string) string {
	switch store.SeedProfile(profile) {
	case store.SeedProfileAcceptance:
		return store.AcceptanceSeedVersion
	case store.SeedProfileConformance:
		return string(store.SeedProfileConformance)
	default:
		return ""
	}
}

type observingProber struct {
	inner         scanpkg.Prober
	observability *observability.Controller
}

func (p observingProber) Probe(ctx context.Context, path string) (ffmpeg.Probe, error) {
	start := time.Now()
	probe, err := p.inner.Probe(ctx, path)
	result := "ok"
	if err != nil {
		result = "error"
	}
	_ = p.observability.RecordGauge(ctx, "beamfall_observability_ffprobe_latency_ms", time.Since(start).Milliseconds(), map[string]string{"result": result})
	if err != nil {
		_ = p.observability.RecordGauge(ctx, "beamfall_observability_ffprobe_failures", 1, nil)
	}
	return probe, err
}

func recordScanMetrics(ctx context.Context, controller *observability.Controller, st *store.Store, job store.Job, duration time.Duration, err error) {
	result := "ok"
	if errors.Is(err, jobs.ErrYielded) {
		result = "yielded"
	} else if err != nil {
		result = "error"
	}
	_ = controller.RecordGauge(ctx, "beamfall_observability_scan_duration_ms", duration.Milliseconds(), map[string]string{
		"kind":   string(job.Kind),
		"result": result,
	})
	// ADR-0146 §8 scan-throughput SLI: files-processed count for this run.
	// Paired with scan_duration_ms it yields items/sec. The scan handler
	// persists its file count as durable job progress; we read it back only when
	// observability is enabled (Decision 3/10: inert — no extra store read — when
	// the toggle is off) and only on a completed run (a yield/error count is
	// partial and would skew throughput).
	if err != nil || !controller.Enabled() {
		return
	}
	settled, jerr := st.Job(ctx, job.ID)
	if jerr != nil {
		return
	}
	_ = controller.RecordGauge(ctx, "beamfall_observability_scan_files_total", settled.Progress.Current, map[string]string{
		"kind": string(job.Kind),
	})
}

// generationRetryDelay is how far out a preempted or throttled background
// generation job re-queues: far enough that it does not busy-retry
// against a live transcode, near enough that generation resumes promptly once
// the transcode clears.
const generationRetryDelay = 30 * time.Second

// governedGeneration wraps a background generation handler in the resource
// governor's transcode-preemption + background-CPU budget. It
// takes a generation lease before running: no lease means a realtime transcode
// is active (generation yields and retries) or the budget is already spent
// (generation throttles), so the job re-queues rather than running. The handler
// runs under the lease's cancellable context; if a transcode preempts it
// mid-run the context is cancelled with governor.ErrPreempted and the job yields
// with its checkpoint preserved — a durable, resumable yield, never a failure.
// Any unrelated error or cancellation keeps its normal semantics.
func governedGeneration(g *governor.Governor, handler jobs.Handler) jobs.Handler {
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		lease, ok := g.BeginGeneration(ctx)
		if !ok {
			return jobs.Result{Checkpoint: job.Checkpoint, RunAfter: time.Now().UTC().Add(generationRetryDelay)}, jobs.ErrYielded
		}
		defer lease.Release()
		result, err := handler(lease.Context(), job)
		if errors.Is(context.Cause(lease.Context()), governor.ErrPreempted) {
			checkpoint := result.Checkpoint
			if checkpoint == nil {
				checkpoint = job.Checkpoint
			}
			return jobs.Result{Checkpoint: checkpoint, RunAfter: time.Now().UTC().Add(generationRetryDelay)}, jobs.ErrYielded
		}
		return result, err
	}
}

// remoteAccessProvider adapts the remoteaccess package to the graph's
// consumer-defined RemoteAccessProvider seam. It maps each path's
// presence (nil = not configured) to the graph's ok flag and translates the
// honest cert/connection states — never reading or surfacing the carrier token.
type remoteAccessProvider struct {
	direct          *remoteaccess.DirectHTTPS
	directTLSActive *atomic.Bool
	relay           *remoteaccess.RelayClient
	// lanPort is the configured listen port used to build LAN/overlay URLs in
	// the ordered endpoint set; 0 omits those paths.
	lanPort int
	// overlayTLSActive reports whether a TLS identity terminates on the LAN port,
	// gating the overlay https candidate. Every server now holds the
	// persistent self-signed identity, so this is true; it is kept explicit so
	// the "no dead https endpoint" gate is a wired fact, not an assumption.
	overlayTLSActive bool
}

func (p remoteAccessProvider) DirectHTTPS(ctx context.Context) (graph.DirectHTTPSStatus, bool) {
	if p.direct == nil {
		return graph.DirectHTTPSStatus{}, false
	}
	return graph.DirectHTTPSStatus{
		Domain:    p.direct.Domain(),
		CertState: string(p.direct.CertState(ctx)),
	}, true
}

func (p remoteAccessProvider) Relay(ctx context.Context) (graph.RelayStatus, bool) {
	if p.relay == nil {
		return graph.RelayStatus{}, false
	}
	state, lastErr := p.relay.Status()
	return graph.RelayStatus{
		Endpoint:        p.relay.Endpoint(),
		PublicHost:      p.relay.PublicHost(),
		ConnectionState: string(state),
		LastError:       lastErr,
	}, true
}

// Endpoints builds the ordered remote-access URL set (LAN → direct → overlay →
// self-hosted-relay → hosted; ADR-0012 §1 dual-URL model, SEC-10 path order).
// It hands the resolver the configured LAN port, the direct domain, and the
// self-hosted relay's public host; the resolver detects LAN/overlay interface
// addresses and returns the deterministic ordered list. The hosted-relay path
// is demoted to future and has no server-side config — its host
// is left empty here, so the hosted candidate is omitted. The LAN port is served
// through a TLS-mux: LAN clients use plain HTTP (cleartext permitted to
// private destinations only, ADR-0012 §1) while the overlay candidate is genuine
// https terminated by the server's self-signed identity — overlay is emitted
// only when that TLS identity is active (OverlayTLSActive), never as a dead
// endpoint. remote/overlay/relay are always https.
func (p remoteAccessProvider) Endpoints(ctx context.Context) []graph.RemoteEndpoint {
	lanScheme := "http"
	directTLSActive := p.directTLSActive != nil && p.directTLSActive.Load()
	if directTLSActive {
		lanScheme = "https"
	}
	in := remoteaccess.EndpointInputs{
		LANScheme:        lanScheme,
		LANPort:          p.lanPort,
		DirectTLSActive:  directTLSActive,
		OverlayTLSActive: p.overlayTLSActive,
	}
	if p.direct != nil {
		in.DirectDomain = p.direct.Domain()
	}
	if p.relay != nil {
		in.SelfHostedRelayPublicHost = p.relay.PublicHost()
	}
	resolved := remoteaccess.ResolveEndpoints(in)
	out := make([]graph.RemoteEndpoint, 0, len(resolved))
	for _, e := range resolved {
		out = append(out, graph.RemoteEndpoint{Kind: string(e.Kind), URL: e.URL})
	}
	return out
}

// Addr blocks until Run binds the HTTP listener (or ctx is done) and returns
// the live listener address — the resolved host:port even when the configured
// Addr requested an ephemeral port (":0"). Returns "" if ctx is done first.
func (a *App) Addr(ctx context.Context) string {
	select {
	case <-a.addrReady:
		a.addrMu.Lock()
		defer a.addrMu.Unlock()
		return a.addr
	case <-ctx.Done():
		return ""
	}
}

func searchFeedDirectories(ctx context.Context, query string, searchITunes, searchExtendedDirectory func(context.Context, string) ([]graph.FeedSearchResult, error)) ([]graph.FeedSearchResult, error) {
	results, err := searchITunes(ctx, query)
	if err != nil {
		return nil, err
	}
	// Podcast Index is the optional richer directory: when no credentials are
	// configured, or the signed request fails, fall back silently to the
	// iTunes results already gathered.
	if extended, err := searchExtendedDirectory(ctx, query); err == nil {
		results = append(results, extended...)
	}
	return results, nil
}

// Handler returns the production HTTP handler.
func (a *App) Handler() http.Handler {
	serverHandler := http.Handler(a.server)
	if a.compat != nil {
		serverHandler = compatPrefixHandler{compat: a.compat, next: serverHandler}
	}
	return backupBootstrapHTTPHandler{
		next:    wakingHTTPHandler{next: serverHandler, wake: a.jobWake},
		dataDir: a.cfg.DataDir,
		now:     time.Now,
	}
}

type compatPrefixHandler struct {
	compat http.Handler
	next   http.Handler
}

func (h compatPrefixHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, compat.Prefix) {
		h.compat.ServeHTTP(w, r)
		return
	}
	h.next.ServeHTTP(w, r)
}

type backupBootstrapHTTPHandler struct {
	next    http.Handler
	dataDir string
	now     func() time.Time
}

func (h backupBootstrapHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/api/graphql" {
		if err := ensureBackupBootstrap(h.dataDir, h.now().UTC()); err != nil {
			http.Error(w, "backup key bootstrap unavailable", http.StatusInternalServerError)
			return
		}
	}
	h.next.ServeHTTP(w, r)
}

func ensureBackupBootstrap(dataDir string, now time.Time) error {
	_, err := backupcore.CreateBootstrapEscrow(dataDir, now)
	if errors.Is(err, backupcore.ErrBootstrapEscrowNotFound) {
		return nil
	}
	return err
}

type wakingHTTPHandler struct {
	next http.Handler
	wake *jobWake
}

func (h wakingHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		defer h.wake.notify()
	}
	h.next.ServeHTTP(w, r)
}

// Routes returns the derived route table.
func (a *App) Routes() []server.Route {
	return a.server.Routes()
}

// Surfaces returns the Reveal-contract surface registry.
func (a *App) Surfaces() *reveal.Registry {
	return a.surfaces
}

// Close releases process resources owned by the app.
func (a *App) Close() error {
	checkpointErr := a.checkpointRuntimeState()
	return errors.Join(checkpointErr, a.store.Close())
}

// checkpointRuntimeState stops stateful runtime resources while the store is
// still open. Shutdown calls this only after job claimers and other in-flight
// workers have drained, so their final durable bookkeeping lands first. Every
// close it drives is itself idempotent, so Close after Run — the in-process
// harness pattern — replays it harmlessly.
func (a *App) checkpointRuntimeState() error {
	var err error
	if a.watcher != nil {
		if closeErr := a.watcher.Close(); closeErr != nil {
			a.logger.Warn("watcher close failed", "error", closeErr)
			err = errors.Join(err, closeErr)
		}
	}
	a.hls.Close()
	if a.proxyTee != nil {
		if closeErr := a.proxyTee.Close(); closeErr != nil {
			a.logger.Warn("proxy archive root close failed", "error", closeErr)
			err = errors.Join(err, closeErr)
		}
	}
	return err
}

// RunPendingJobs drains the queued jobs of the given classes (default: both
// background and interactive), running each once until none remain, and returns
// the number run. Jobs that enqueue follow-on jobs (scan → ingest → precompute)
// are drained in the same call. It exists for the in-process test harness
// (internal/apptest) and tooling to drive the background job pipeline
// synchronously, WITHOUT standing up the Run worker loop (which binds a real
// listener). It is never invoked by production serve. A handler error stops the
// drain and is returned (tests want it loud); a generous iteration cap guards
// against a pathologically self-re-enqueuing job.
func (a *App) RunPendingJobs(ctx context.Context, classes ...store.JobClass) (int, error) {
	if len(classes) == 0 {
		classes = []store.JobClass{store.JobClassBackground, store.JobClassInteractive}
	}
	const maxJobs = 1000
	n := 0
	for n < maxJobs {
		if _, err := a.jobs.RunOne(ctx, classes, time.Now()); err != nil {
			if errors.Is(err, jobs.ErrNoRunnable) {
				return n, nil
			}
			return n, err
		}
		n++
	}
	return n, fmt.Errorf("RunPendingJobs: drained %d jobs without reaching empty (runaway re-enqueue?)", maxJobs)
}

// Run starts the HTTP server and shuts it down when ctx is canceled. The jobs
// goroutine is stopped and awaited before the store closes, so no job write
// can race a closing store.
func (a *App) Run(ctx context.Context) error {
	if !a.runStarted.CompareAndSwap(false, true) {
		return errAlreadyRun
	}
	// Preserve request-scoped values while making job cancellation an explicit
	// shutdown phase. Deriving from ctx would cancel handlers immediately and
	// bypass the bounded graceful-drain interval.
	jobsCtx, cancelJobs := context.WithCancel(context.WithoutCancel(ctx))
	claimsCtx, stopClaims := context.WithCancel(context.Background())
	claimsDrained := make(chan struct{})
	var jobsDone sync.WaitGroup
	jobsDone.Add(1)
	go func() {
		defer jobsDone.Done()
		a.runJobsWithClaims(jobsCtx, claimsCtx, claimsDrained)
	}()
	jobsDone.Add(1)
	go func() {
		defer jobsDone.Done()
		a.hls.Run(jobsCtx)
	}()
	// Webhook Emitter: a long-lived consumer of the event
	// outbox, started only when the operator configured at least one endpoint
	// (a.webhook is nil and this goroutine is skipped otherwise). It shares the
	// jobsCtx lifecycle so it stops and is awaited before the store closes.
	if a.webhook != nil {
		jobsDone.Add(1)
		go func() {
			defer jobsDone.Done()
			a.webhook.Run(jobsCtx)
		}()
	}
	// Scrobble dispatcher: a long-lived queue-drain worker, started
	// only when the operator activated at least one adapter (a.scrobble is nil
	// and this goroutine is skipped otherwise). It shares the jobsCtx lifecycle
	// so it stops and is awaited before the store closes.
	if a.scrobble != nil {
		jobsDone.Add(1)
		go func() {
			defer jobsDone.Done()
			a.scrobble.Run(jobsCtx)
		}()
	}
	// Integration Worker manager: supervises each installed worker
	// out-of-process, started only when a worker is installed (a.integration is
	// nil and this goroutine is skipped otherwise). It shares the jobsCtx lifecycle
	// so a worker is stopped and awaited before the store closes.
	if a.integration != nil {
		jobsDone.Add(1)
		go func() {
			defer jobsDone.Done()
			a.integration.Run(jobsCtx)
		}()
	}
	defer func() {
		shutdown := shutdownSequence{
			stopClaims:    stopClaims,
			claimsDrained: claimsDrained,
			gracePeriod:   a.drainGrace,
			after:         a.drainAfter,
			cancelJobs:    cancelJobs,
			joinWorkers:   jobsDone.Wait,
			joinBound:     workerJoinBound,
			joinAfter:     time.After,
			checkpoint:    a.checkpointRuntimeState,
			closeStore:    a.store.Close,
		}
		if err := shutdown.run(); err != nil {
			a.logger.Error("ordered shutdown failed", "error", err)
		}
		a.logger.Info("server stopped")
	}()

	// TLS identity. Every server holds the
	// persistent self-signed identity; it terminates client TLS for the overlay
	// path and the relay carrier without operator ACME, and its SPKI is pinned at
	// LAN enrollment. When the operator configures direct HTTPS + ACME, carrierTLS
	// is COMPOSED by SNI: the public FQDN is served the ACME cert
	// (browser trust), while every other ServerName — overlay/LAN IP, the relay
	// carrier backend, or no-SNI — stays on the self-signed identity so those paths
	// never dead-end on the ACME HostPolicy. carrierTLS is therefore always
	// non-nil and always backs overlay + relay with the pinned self-signed cert
	// regardless of whether ACME is configured.
	handler := a.Handler()
	carrierTLS := a.selfSigned.TLSConfig()
	// directTLSActive gates the `direct` advertised endpoint (the operator's
	// public domain), which is true only on the ACME path — the self-signed
	// baseline never advertises a public domain.
	directTLSActive := false
	if a.directHTTPS != nil {
		handler = a.directHTTPS.Manager().HTTPHandler(handler)
		// Compose (do NOT replace): FQDN SNI → ACME cert; overlay/LAN-IP/relay/no-SNI
		// → self-signed identity. Fixes the overlay + relay dead-endpoint that the
		// wholesale carrierTLS = ACME replacement caused (GAP 1/2, NGAUDIT-B12).
		carrierTLS = a.selfSigned.ComposeWithACME(a.directHTTPS.TLSConfig(), a.directHTTPS.Domain())
		directTLSActive = true
		a.logger.Info("direct HTTPS enabled", "domain", a.directHTTPS.Domain())
	}

	httpServer := &http.Server{
		Handler:           handler,
		TLSConfig:         carrierTLS,
		ReadHeaderTimeout: 5 * time.Second,
		// ReadTimeout bounds the whole request read (headers + body), so a
		// slowloris that dribbles the body one byte at a time cannot pin a
		// connection forever; it is generous enough for any control-JSON body.
		ReadTimeout: 60 * time.Second,
		// IdleTimeout reaps kept-alive connections that go silent. There is
		// deliberately NO WriteTimeout: byte routes stream large media via
		// mediahttp (http.ServeContent/ServeFile) and a blanket write deadline
		// would truncate long downloads.
		IdleTimeout: 120 * time.Second,
	}

	listener, err := listenTCP("tcp", a.cfg.Addr)
	if err != nil {
		a.logger.Error("http server failed", "error", err)
		return err
	}
	a.addrMu.Lock()
	a.addr = listener.Addr().String()
	a.addrMu.Unlock()
	close(a.addrReady)
	if a.setupToken != "" {
		// First-run setup happens on the LAN over cleartext (the TLS-mux serves
		// both); advertise https only when the ACME direct domain is the served
		// identity, never for the self-signed baseline.
		scheme := "http"
		if directTLSActive {
			scheme = "https"
		}
		// SEC-LOG-1: the one-time bootstrap token is a secret (like a pairing code)
		// and must never land in a structured/persisted log. The tokened URL goes to
		// the operator's console (stdout) only; the slog line carries a REDACTED URL.
		fmt.Fprintf(os.Stdout, "Beamfall first-run setup: %s\n", firstRunSetupURL(scheme, listener.Addr().String(), a.setupToken))
		a.logger.Info(
			"=== Beamfall first-run setup ===",
			"setupURL", firstRunSetupURLRedacted(scheme, listener.Addr().String()),
			"headless", "BEAMFALL_ADMIN_PASSWORD",
		)
	}
	if a.cfg.Discovery.Enabled {
		advertiser := discovery.New()
		if err := advertiser.Start(a.cfg.ServerName, listenPort(listener.Addr())); err != nil {
			a.logger.Warn("discovery advertise failed", "error", err)
		} else {
			defer advertiser.Stop()
		}
	}
	// TLS-mux the bound port: one listener serves cleartext HTTP for the
	// LAN path AND terminates client TLS (self-signed baseline, or ACME when
	// active) for the overlay path — so the advertised overlay https endpoint
	// actually handshakes instead of dead-ending on a plain-HTTP listener. The
	// ACME HTTP-01 challenge handler is already wired onto `handler`, and autocert's
	// TLS-ALPN challenge rides carrierTLS, so ACME issuance still completes here.
	serveListener := remoteaccess.NewTLSMuxListener(listener, carrierTLS)
	if directTLSActive {
		a.directTLSActive.Store(true)
		defer a.directTLSActive.Store(false)
	}

	// Self-hosted relay carrier (ADR-0018/0025): dial OUT, register the public
	// host, and carry inbound relayed connections byte-blind. The relay opens
	// one yamux stream per inbound public client; the carrier splices each to a
	// loopback TLS listener that terminates the client's TLS against the
	// server's own certificate (ADR-0018 sec 1/sec 2) and serves the same app
	// handler, so the relay forwards raw bytes and never sees plaintext. It runs
	// in the background and never blocks or fails boot; the Console reads the
	// honest connection state. The carrier token is resolved from a 0600 secret
	// in RelayClient and never logged.
	//
	// Carrying inbound client TLS requires the server's own cert. Every server now
	// holds one — the persistent self-signed identity when direct HTTPS is
	// off, or the ACME cert when it is on — so the carrier no longer stays off for
	// cert-less LAN servers. Native clients pin the identity's SPKI at LAN
	// enrollment, so a byte-blind relay still cannot MITM. This amends
	// ADR-0118's "no cert ⇒ carrier off" premise, which assumed a LAN-only server
	// had no identity to terminate with.
	if a.relay != nil {
		if backendLn, lerr := tls.Listen("tcp", "127.0.0.1:0", carrierTLS); lerr != nil {
			a.logger.Warn("relay carrier backend listener failed", "error", lerr)
		} else {
			bsrv := &http.Server{
				Handler: handler,
				ConnContext: func(ctx context.Context, _ net.Conn) context.Context {
					return server.ContextWithRelayedRequest(ctx)
				},
				ReadHeaderTimeout: 5 * time.Second,
				ReadTimeout:       60 * time.Second,
				IdleTimeout:       120 * time.Second,
			}
			jobsDone.Add(1)
			go func() {
				defer jobsDone.Done()
				errc := make(chan error, 1)
				go func() {
					err := bsrv.Serve(backendLn)
					if errors.Is(err, http.ErrServerClosed) {
						err = nil
					}
					errc <- err
				}()
				select {
				case <-jobsCtx.Done():
					shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					_ = bsrv.Shutdown(shutdownCtx)
					cancel()
					if err := <-errc; err != nil {
						a.logger.Warn("relay carrier backend stopped", "error", err)
					}
				case err := <-errc:
					if err != nil {
						a.logger.Warn("relay carrier backend stopped", "error", err)
					}
				}
			}()
			a.relay.SetBackendAddr(backendLn.Addr().String())
			jobsDone.Add(1)
			go func() {
				defer jobsDone.Done()
				a.runRelayCarrier(jobsCtx)
			}()
		}
	}

	// Node-facing mTLS SESSION listener (ADR-0129 §5: the node dials the main;
	// ADR-0123 §5 / ADR-0046 §2.1 SPKI pinning). Opt-in and OFF by default
	// (empty BEAMFALL_NODEHOST_LISTEN), so single-machine installs are
	// unaffected. It serves a CLONE of the self-signed identity config with
	// RequireAnyClientCert — never the shared browser config — and pins the
	// presented node leaf via nodehost.OpenSession; the node dialer pins this
	// served identity's SPKI in turn. Lifecycle mirrors the relay backend: it
	// runs under jobsCtx and is joined before the store closes.
	if nodeAddr := strings.TrimSpace(a.cfg.NodeHostListen); nodeAddr != "" {
		if nodeLn, lerr := net.Listen("tcp", nodeAddr); lerr != nil {
			a.logger.Warn("node-host session listener failed", "error", lerr)
		} else {
			mtls := a.selfSigned.TLSConfig().Clone()
			mtls.ClientAuth = tls.RequireAnyClientCert
			nodeSvc := nodehost.NewServiceWithRegistry(time.Now, a.selfSigned, nodehost.NewStoreRegistry(a.store))
			nsrv := nodehostd.NewServer(nodeSvc, nodehostd.ProductionPolicy(), mtls, a.logger)
			a.logger.Info("node-host session listener starting", "addr", nodeLn.Addr().String())
			jobsDone.Add(1)
			go func() {
				defer jobsDone.Done()
				serveErr := make(chan error, 1)
				go func() { serveErr <- nsrv.Serve(nodeLn) }()
				select {
				case <-jobsCtx.Done():
					_ = nodeLn.Close() // Serve returns with the closed-listener error
					<-serveErr
				case err := <-serveErr:
					// Serve failed on its own; the listener is this
					// goroutine's to release, or the port stays bound.
					_ = nodeLn.Close()
					if err != nil {
						a.logger.Warn("node-host session listener stopped", "error", err)
					}
				}
				// Per-connection handlers outlive the listener by up to the
				// handshake timeout, so join them here — the worker group must
				// not report done while a node connection is still open.
				nsrv.Shutdown()
			}()
		}
	}

	a.logger.Info("server starting", "addr", a.cfg.Addr)
	errc := make(chan error, 1)
	go func() {
		err := httpServer.Serve(serveListener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errc <- err
	}()
	// Live watching is an accelerator. Persisted roots may be slow or
	// unreachable, so observe them only after Serve owns the bound listener.
	// Scheduled scans remain registered as the correctness backstop.
	if a.watcherDisabled {
		a.logger.Warn("live watcher disabled; using scheduled scans", "reason", "BEAMFALL_WATCHER_DISABLED")
	}
	jobsDone.Add(1)
	go func() {
		defer jobsDone.Done()
		observePersistedRoots(jobsCtx, a.store, a.watcher, a.logger)
	}()

	select {
	case <-ctx.Done():
		// Stop background claim acceptance at the first shutdown signal. The
		// bounded drain below retires the listener and every connection behind
		// it; the deferred ordered sequence then joins any job already in its
		// handler before checkpointing state.
		stopClaims()
		forced, err := drainHTTPServer(httpServer, errc, httpDrainBound)
		if forced {
			// A streaming byte route has no write deadline by design, so a
			// forced close at the bound is an ordinary outcome, not a failure.
			a.logger.Warn("http graceful drain bound elapsed; live connections closed", "bound", httpDrainBound)
		}
		return err
	case err := <-errc:
		if err != nil {
			a.logger.Error("http server failed", "error", err)
		}
		return err
	}
}

// runRelayCarrier owns the self-hosted relay carrier lifecycle (ADR-0018/0025):
// dial + register + carry, and on carrier loss re-dial with bounded exponential
// backoff until the context is cancelled. The relay token is re-presented on
// every reconnect, so a service-side cancellation propagates in minutes.
// RelayConnected is only ever set while a yamux session is live
// and carrying — a dropped carrier reverts the Console state, so the status is
// truthful rather than reporting a connection that forwards nothing.
func (a *App) runRelayCarrier(ctx context.Context) {
	const (
		backoffMin = time.Second
		backoffMax = 60 * time.Second
		// A carrier session must survive this long before the backoff resets.
		relayCarrierHealthyDuration = 30 * time.Second
	)
	backoff := backoffMin
	for {
		if ctx.Err() != nil {
			return
		}
		session, err := a.relay.Connect(ctx)
		if err != nil {
			a.logger.Warn("relay carrier connect failed", "endpoint", a.relay.Endpoint(), "error", err)
			if !relayCarrierShouldRetry(err) {
				return
			}
		} else {
			a.logger.Info("relay carrier connected", "endpoint", a.relay.Endpoint(), "publicHost", a.relay.PublicHost())
			carryStart := time.Now()
			if cerr := a.relay.Carry(ctx, session); cerr != nil {
				a.logger.Warn("relay carrier session ended", "endpoint", a.relay.Endpoint(), "error", cerr)
			}
			// Reset the backoff only after a session that actually stayed up:
			// resetting on dial success alone lets a flapping relay (accepts,
			// then drops immediately) pin the loop at backoffMin forever.
			if time.Since(carryStart) >= relayCarrierHealthyDuration {
				backoff = backoffMin
			}
		}
		if ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(relayCarrierBackoffDelay(backoff, rand.Int63n))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff *= 2; backoff > backoffMax {
			backoff = backoffMax
		}
	}
}

func relayCarrierShouldRetry(err error) bool {
	return !errors.Is(err, remoteaccess.ErrRelayInvalidHello)
}

// relayCarrierBackoffDelay returns d with up to +/-20% full jitter applied.
func relayCarrierBackoffDelay(d time.Duration, randInt63n func(int64) int64) time.Duration {
	if d <= 0 {
		return 0
	}
	span := d / 5
	if span <= 0 {
		return d
	}
	delta := time.Duration(randInt63n(int64(2*span))) - span
	out := d + delta
	if out < 0 {
		return 0
	}
	return out
}

func listenPort(addr net.Addr) int {
	if tcpAddr, ok := addr.(*net.TCPAddr); ok {
		return tcpAddr.Port
	}
	_, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return 0
	}
	value, err := strconv.Atoi(port)
	if err != nil {
		return 0
	}
	return value
}

// runJobs is the standalone worker-loop entry point used by in-process tests.
// Production Run uses runJobsWithClaims so shutdown can stop claim acceptance
// independently from the context held by already-running handlers.
func (a *App) runJobs(ctx context.Context) {
	claimsDrained := make(chan struct{})
	a.runJobsWithClaims(ctx, ctx, claimsDrained)
}

func (a *App) runJobsWithClaims(ctx, claimsCtx context.Context, claimsDrained chan<- struct{}) {
	workers := 1
	if a.governor != nil {
		workers = a.governor.GenerationWorkers()
	}
	var claimers sync.WaitGroup
	claimers.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer claimers.Done()
			a.runJobClaimer(ctx, claimsCtx)
		}()
	}
	go func() {
		claimers.Wait()
		close(claimsDrained)
	}()
	defer claimers.Wait()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			recovered, err := a.jobs.RecoverExpiredLeases(ctx, now)
			if err != nil && !errors.Is(err, context.Canceled) {
				a.logger.Error("job lease recovery failed", "error", err)
			} else if recovered > 0 {
				a.jobWake.notify()
			}
			if a.schedule != nil {
				if err := a.schedule.Tick(ctx, now); err != nil && !errors.Is(err, context.Canceled) {
					a.logger.Error("schedule tick failed", "error", err)
				}
			}
		}
	}
}

func (a *App) runJobClaimer(ctx, claimsCtx context.Context) {
	wake, unsubscribe := a.jobWake.subscribe()
	defer unsubscribe()
	a.drainBackgroundJobsUntil(ctx, claimsCtx)

	ticker := time.NewTicker(a.jobClaimSafetyInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-claimsCtx.Done():
			return
		case <-wake:
			a.drainBackgroundJobsUntil(ctx, claimsCtx)
		case <-ticker.C:
			a.drainBackgroundJobsUntil(ctx, claimsCtx)
		}
	}
}

// drainBackgroundJobs runs every currently-runnable background job. RunOne
// claims exactly one, so claiming only once per tick would pin background
// throughput to one job per second per worker regardless of queue depth. A
// yielded job re-queues runnable and keeps progressing within the same drain;
// a failure defers to the next tick.
func (a *App) drainBackgroundJobs(ctx context.Context) {
	a.drainBackgroundJobsUntil(ctx, ctx)
}

func (a *App) drainBackgroundJobsUntil(ctx, claimsCtx context.Context) {
	for ctx.Err() == nil && claimsCtx.Err() == nil {
		if _, err := a.jobs.RunOne(ctx, []store.JobClass{store.JobClassBackground}, time.Now()); err != nil {
			if !errors.Is(err, jobs.ErrNoRunnable) && !errors.Is(err, context.Canceled) {
				a.logger.Error("job run failed", "error", err)
			}
			return
		}
	}
}

// trashDisabledConfirmationRequired identifies the normal outcome in which an
// autonomous destructive sweep was refused pending human confirmation. A plain
// skipped result has nil err; every other non-nil error is a genuine failure.
func trashDisabledConfirmationRequired(skipped bool, err error) bool {
	return skipped && errors.Is(err, store.ErrTrashDisabledConfirmationRequired)
}

// enqueueArtworkSweep schedules the missing-poster sweep; failure to enqueue
// is logged, never fatal — the next scan or restart re-arms it.
// runRetentionPurge runs the retention sweeps at boot and after each scan:
// trash entries past the window (hard invariant 6 — destruction stays
// recoverable until then), then Entities orphaned by purged Media, plus their
// artwork files. Failures are logged, never fatal.
func runRetentionPurge(ctx context.Context, st *store.Store, art *artwork.Service, logger *slog.Logger) {
	if resolved, skippedBlank, err := st.ReconcileSubscriptionSeenEntries(ctx); err != nil {
		logger.Warn("subscription seen-entry reconciliation failed", "error", err)
	} else {
		if skippedBlank > 0 {
			logger.Debug("subscription seen-entry reconciliation skipped entries without paths", "entries", skippedBlank)
		}
		if resolved > 0 {
			logger.Info("subscription seen entries reconciled", "entries", resolved)
		}
	}
	if trashed, skipped, err := st.ApplySubscriptionRetentionPolicies(ctx); trashDisabledConfirmationRequired(skipped, err) {
		// One line per sweep, not per item (hard invariant 6: autonomous
		// destruction requires human confirmation while trash is disabled).
		logger.Info("subscription retention skipped: confirmation required", "reason", "trash disabled", "skips", st.TrashDisabledSweepSkips())
	} else if err != nil {
		logger.Warn("subscription retention failed", "error", err)
	} else if skipped {
		logger.Info("subscription retention skipped")
	} else if trashed > 0 {
		logger.Info("subscription retention trashed media", "media", trashed)
	}
	if trashed, skipped, err := st.ApplyRetentionRules(ctx, time.Now().UTC()); trashDisabledConfirmationRequired(skipped, err) {
		logger.Info("retention rules skipped: confirmation required", "reason", "trash disabled", "skips", st.TrashDisabledSweepSkips())
	} else if err != nil {
		logger.Warn("retention rules failed", "error", err)
	} else if skipped {
		logger.Info("retention rules skipped")
	} else if trashed > 0 {
		logger.Info("retention rules trashed media", "media", trashed)
	}
	trashPurgeSkipped := false
	if purged, skipped, err := st.SweepPurgeTrashedBefore(ctx, time.Now().UTC().Add(-store.TrashRetention)); trashDisabledConfirmationRequired(skipped, err) {
		trashPurgeSkipped = true
		logger.Info("trash purge skipped: confirmation required", "reason", "trash disabled", "skips", st.TrashDisabledSweepSkips())
	} else if err != nil {
		logger.Warn("trash purge failed", "media", purged, "error", err)
	} else if skipped {
		trashPurgeSkipped = true
		logger.Info("trash purge skipped")
	} else if purged > 0 {
		logger.Info("trash purged", "media", purged)
	}
	if purged, skipped, err := st.SweepManagedFileTrash(ctx, time.Now().UTC()); trashDisabledConfirmationRequired(skipped, err) {
		logger.Info("managed file trash purge skipped: confirmation required", "reason", "trash disabled", "skips", st.TrashDisabledSweepSkips())
	} else if err != nil {
		logger.Warn("managed file trash purge failed", "entries", purged, "error", err)
	} else if skipped {
		logger.Info("managed file trash purge skipped")
	} else if purged > 0 {
		logger.Info("managed file trash purged", "entries", purged)
	}
	// Reconcile the candidate-preview cache against the table now that the purge
	// deleted the orphaned rows: it removes only cache directories whose
	// artwork_candidate row is already gone, so a crash mid-purge self-heals.
	if liveCandidates, err := st.AllArtworkCandidateIDs(ctx); err != nil {
		logger.Warn("candidate preview cache reconciliation failed", "error", err)
	} else if removed, err := art.SweepCandidatePreviewCache(liveCandidates); err != nil {
		logger.Warn("candidate preview cache reconciliation failed", "error", err)
	} else if removed > 0 {
		logger.Info("candidate preview cache reconciled", "directories", removed)
	}
	if !trashPurgeSkipped {
		orphans, err := st.PurgeOrphanEntities(ctx)
		if err != nil {
			logger.Warn("orphan entity purge failed", "error", err)
			return
		}
		for _, id := range orphans {
			if err := art.RemoveEntityArtwork(id); err != nil {
				logger.Warn("orphan entity artwork removal failed", "entity", string(id), "error", err)
			}
		}
		if len(orphans) > 0 {
			logger.Info("orphan entities purged", "entities", len(orphans))
		}
	}
}

func retentionSweepHandler(st *store.Store, art *artwork.Service, logger *slog.Logger) jobs.Handler {
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		runRetentionPurge(ctx, st, art, logger)
		return jobs.Result{}, nil
	}
}

// eventsCompactionHandler runs the daily events.db retention compaction (PANEL-9
// / ADR-0011): prune expired Stream-Token ledger rows + auth attempts older than
// retention, then reclaim space. The audit log (append-only, ADR-0012 §11) and
// engagement events (canonical) are never pruned — see store.CompactEvents.
func eventsCompactionHandler(st *store.Store, logger *slog.Logger) jobs.Handler {
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		cfg, err := st.ConsoleConfig(ctx)
		if err != nil {
			return jobs.Result{}, err
		}
		now := time.Now().UTC()
		retention := time.Duration(cfg.EventsRetentionDays) * 24 * time.Hour
		res, err := st.CompactEvents(ctx, retention, now)
		if err != nil {
			return jobs.Result{}, err
		}
		payload, err := json.Marshal(map[string]any{
			"retentionDays":       cfg.EventsRetentionDays,
			"expiredStreamTokens": res.ExpiredStreamTokens,
			"oldAuthAttempts":     res.OldAuthAttempts,
			"oldOutboxEvents":     res.OldOutboxEvents,
			"vacuumed":            res.Vacuumed,
		})
		if err != nil {
			return jobs.Result{}, err
		}
		if err := st.AppendJobEvent(ctx, job.ID, "compaction", payload, now); err != nil {
			return jobs.Result{}, err
		}
		logger.Info("events compaction",
			"retentionDays", cfg.EventsRetentionDays,
			"expiredStreamTokens", res.ExpiredStreamTokens,
			"oldAuthAttempts", res.OldAuthAttempts,
			"oldOutboxEvents", res.OldOutboxEvents,
			"vacuumed", res.Vacuumed)
		return jobs.Result{}, nil
	}
}

// databaseBackupKeepN is the retention floor for the scheduled database backup:
// keep the newest N published sets under the destination and prune older ones
// after a successful backup. Pruning old backup *copies* is not a media delete
// (invariant #6); the live data + trash are untouched and PruneBackups never
// wipes the set entirely (keep >= 1).
const databaseBackupKeepN = 14

func databaseBackupHandler(st *store.Store, dest string, encryptedBackupCipher func() store.EncryptedLibraryDBCipher) jobs.Handler {
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		var backupErr error
		if encryptedBackupCipher != nil {
			_, backupErr = st.BackupWithOptions(ctx, dest, store.BackupOptions{EncryptedLibraryDBCipher: encryptedBackupCipher()})
		} else {
			_, backupErr = st.Backup(ctx, dest)
		}
		if backupErr != nil {
			return jobs.Result{}, backupErr
		}
		// Retention pruning runs only after a successful backup and is
		// non-fatal: a prune error is logged but does not fail the job, so a
		// cleanup failure can never trigger a retry that writes another full
		// backup (Gate-A MED). The backup itself already succeeded.
		if _, pruneErr := st.PruneBackups(ctx, dest, databaseBackupKeepN); pruneErr != nil {
			slog.WarnContext(ctx, "backup retention prune failed", "dest", dest, "keep", databaseBackupKeepN, "error", pruneErr)
		}
		return jobs.Result{}, nil
	}
}

func enqueueArtworkSweep(ctx context.Context, st *store.Store, logger *slog.Logger, wake *jobWake) {
	job, err := artwork.NewSweepJob(time.Now().UTC())
	if err == nil {
		_, err = enqueueAndWake(ctx, st, wake, job)
	}
	if err != nil {
		logger.Warn("artwork sweep enqueue failed", "error", err)
	}
}

// buildWebhookEmitter wires the ADR-0040 §2.5 Webhook Emitter Integration
// Plugin. It returns (nil, nil) — dormant — when no endpoints are configured
// (the opt-in default). Config is plugin-private JSON at
// <DataDir>/webhook-endpoints.json; each endpoint's HMAC secret is resolved
// from a 0600 secret file under <DataDir>/secrets (invariant #5, the same
// secretFiles reader the scrape engine and qBittorrent ingest use) and never
// read from the JSON. The dead-letter is plugin-private JSON at
// <DataDir>/webhook-dead-letter.json — no core schema (invariant #3).
func buildWebhookEmitter(cfg config.Config, st *store.Store, logger *slog.Logger) (*webhook.Emitter, error) {
	secrets := secretFiles{dir: filepath.Join(cfg.DataDir, "secrets")}
	emitterCfg, err := webhook.LoadConfig(filepath.Join(cfg.DataDir, "webhook-endpoints.json"), secrets)
	if err != nil {
		return nil, fmt.Errorf("webhook emitter: %w", err)
	}
	if len(emitterCfg.Endpoints) == 0 {
		return nil, nil // dormant: no endpoints configured
	}
	deadLetter, err := webhook.OpenDeadLetter(filepath.Join(cfg.DataDir, "webhook-dead-letter.json"))
	if err != nil {
		return nil, fmt.Errorf("webhook emitter: %w", err)
	}
	return webhook.New(emitterCfg, st, nil, deadLetter, logger)
}

// buildScrobbleDispatcher wires the ADR-0049 Scrobble Integration Plugin. It is
// nil (dormant) unless the operator has activated at least one adapter in
// <DataDir>/scrobble-adapters.json (the opt-in default, ADR-0003). Each
// adapter's operator app credentials are resolved from a 0600 secret file under
// <DataDir>/secrets (invariant #5, the same secretFiles reader the scrape engine
// and Webhook Emitter use) and never read from the JSON. Per-Profile links and
// per-Library eligibility are core-owned (scrobbleLinkSource over *store.Store);
// the dead-letter is plugin-private JSON at <DataDir>/scrobble-dead-letter.json
// — no core schema for the volatile delivery state (invariant #3). Outbound
// requests use a timeout-bounded client that refuses origin-changing redirects,
// wrapped by scrobble.LoadAdapters with each adapter's declared-endpoint
// allowlist so an adapter cannot dispatch outside its declared origins.
func buildScrobbleDispatcher(cfg config.Config, st *store.Store, logger *slog.Logger, plugins *plugin.Registry, hooks []ScrobbleAdapterHook) (*scrobble.Dispatcher, error) {
	registry := map[scrobble.Service]scrobble.AdapterFactory{}
	inactive := map[scrobble.Service]bool{}
	for _, hook := range hooks {
		if hook.Service == "" || hook.Factory == nil {
			continue
		}
		if _, exists := registry[hook.Service]; exists {
			continue
		}
		if _, ok := activeBundle(plugins, hook.PluginID); ok {
			registry[hook.Service] = hook.Factory
			continue
		}
		service := hook.Service
		registry[service] = func(_ scrobble.Doer, _ scrobble.AdapterConfig, _ scrobble.SecretSource) (scrobble.Adapter, error) {
			return inactiveScrobbleAdapter{service: service}, nil
		}
		inactive[service] = true
	}
	return buildScrobbleDispatcherWithRegistryAndInactive(cfg, st, logger, newScrobbleHTTPClient(), registry, inactive)
}

func newScrobbleHTTPClient() *http.Client {
	//io:ignore — transport-policy construction only; adapter requests are covered by the egress-scrobble-* I/O ledger entries.
	return &http.Client{
		Transport: http.DefaultTransport,
		Timeout:   15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) == 0 {
				return nil
			}
			previous := via[len(via)-1].URL
			if !strings.EqualFold(req.URL.Scheme, previous.Scheme) || !strings.EqualFold(req.URL.Host, previous.Host) {
				return errors.New("scrobble redirect refused: origin changed")
			}
			return nil
		},
	}
}

func buildScrobbleDispatcherWithRegistry(cfg config.Config, st *store.Store, logger *slog.Logger, client scrobble.Doer, registry map[scrobble.Service]scrobble.AdapterFactory) (*scrobble.Dispatcher, error) {
	return buildScrobbleDispatcherWithRegistryAndInactive(cfg, st, logger, client, registry, nil)
}

func buildScrobbleDispatcherWithRegistryAndInactive(cfg config.Config, st *store.Store, logger *slog.Logger, client scrobble.Doer, registry map[scrobble.Service]scrobble.AdapterFactory, inactive map[scrobble.Service]bool) (*scrobble.Dispatcher, error) {
	secrets := secretFiles{dir: filepath.Join(cfg.DataDir, "secrets")}
	adapters, scrobbleCfg, err := scrobble.LoadAdapters(
		filepath.Join(cfg.DataDir, "scrobble-adapters.json"), secrets, client, registry)
	if err != nil {
		return nil, fmt.Errorf("scrobble dispatcher: %w", err)
	}
	active := adapters[:0]
	for _, adapter := range adapters {
		if !inactive[adapter.Service()] {
			active = append(active, adapter)
		}
	}
	adapters = active
	if len(adapters) == 0 {
		return nil, nil // dormant: no adapters activated
	}
	deadLetter, err := scrobble.OpenDeadLetter(filepath.Join(cfg.DataDir, "scrobble-dead-letter.json"))
	if err != nil {
		return nil, fmt.Errorf("scrobble dispatcher: %w", err)
	}
	scrobbleCfg.DeadLetter = deadLetter
	scrobbleCfg.Skips = scrobble.NewMemorySkipSink(0)
	return scrobble.New(scrobbleCfg, adapters, scrobbleLinkSource{store: st}, secrets, logger)
}

type inactiveScrobbleAdapter struct{ service scrobble.Service }

func (a inactiveScrobbleAdapter) Service() scrobble.Service                     { return a.service }
func (inactiveScrobbleAdapter) Class() scrobble.MediaClass                      { return "" }
func (inactiveScrobbleAdapter) IdentitySchemes() []string                       { return nil }
func (inactiveScrobbleAdapter) DeclaredEndpoints() []string                     { return nil }
func (inactiveScrobbleAdapter) Submit(context.Context, scrobble.Scrobble) error { return nil }

// buildQBittorrentIngestor wires the ADR-0020 ingest pipeline. It ALWAYS returns
// a non-nil ingestor so the Console's setQBittorrentIngest mutation can
// configure ingest at runtime even on a server that
// booted with no qBittorrent endpoint — the ingestor is simply inert (Run is a
// no-op) until a Source is set. Connection + routing come from the persisted
// Console config first (so a Console-configured ingest survives restart),
// falling back to the boot/env config; the password is resolved from the secure
// store (<DataDir>/secrets/qbittorrent, 0600 invariant 5) and never read from
// the catalog DB or the bootstrap config.
func buildQBittorrentIngestor(ctx context.Context, cfg config.Config, st *store.Store, logger *slog.Logger, wake *jobWake) (*qbittorrent.Ingestor, error) {
	ledger, err := qbittorrent.OpenLedger(filepath.Join(cfg.DataDir, "qbittorrent-ingest.json"))
	if err != nil {
		return nil, err
	}
	// Persisted Console config takes precedence over the boot/env config (a
	// Console edit is the operator's latest intent); fall back to env when no
	// Console config has been written.
	url := cfg.QBittorrentURL
	username := cfg.QBittorrentUsername
	routingSource := cfg.QBittorrentRouting
	persisted, err := st.QBittorrentConfig(ctx)
	if err != nil {
		return nil, err
	}
	if persisted.Configured {
		url = persisted.URL
		username = persisted.Username
		routingSource = map[string]string{}
		for _, route := range persisted.Routes {
			routingSource[route.Category] = route.LibraryID
		}
	}
	routing := map[string]store.LibraryID{}
	for category, library := range routingSource {
		routing[category] = store.LibraryID(library)
	}
	var source qbittorrent.Source
	var approvedRoots []string
	if url != "" {
		creds := qbittorrent.Credentials{BaseURL: url, Username: username}
		// A username without a stored password is a misconfiguration worth
		// failing on; a fully anonymous endpoint (no username) needs no secret.
		if username != "" {
			password, err := secretFiles{dir: filepath.Join(cfg.DataDir, "secrets")}.Secret("qbittorrent")
			if err != nil {
				return nil, fmt.Errorf("qbittorrent ingest: %w", err)
			}
			creds.Password = password
		}
		client, err := qbittorrent.NewClient(creds)
		if err != nil {
			return nil, err
		}
		source = qbittorrent.NewSource(client)
		// Capture the endpoint's category/default save paths NOW — the moment
		// the operator's configuration is applied — as the canonical approved
		// download roots. Ingest treats every endpoint-nominated payload path
		// as untrusted and confines it beneath these roots; a capture failure
		// leaves the roots empty, so imports stay refused (fail-closed) until
		// the Console reconfigures ingest or the server restarts with the
		// endpoint reachable. The fetch is time-boxed so a wedged endpoint
		// cannot stall boot.
		rootsCtx, cancelRoots := context.WithTimeout(ctx, 15*time.Second)
		approvedRoots, err = qbittorrentSavePaths(rootsCtx, client)
		cancelRoots()
		if err != nil {
			approvedRoots = nil
			logger.WarnContext(ctx, "qbittorrent ingest: approved download roots unavailable at boot; imports are refused until ingest is reconfigured")
		}
	}
	return qbittorrent.NewIngestor(qbittorrent.Config{
		Source:        source,
		Catalog:       st,
		Scan:          qbitScanEnqueuer{store: st, logger: logger, wake: wake},
		Ledger:        ledger,
		Routing:       routing,
		ApprovedRoots: approvedRoots,
		Logger:        logger,
	})
}

// qbitScanEnqueuer adapts the store's scan-enqueue path to the qBittorrent
// ScanEnqueuer seam: ingested files enter the one standard scan pipeline.
type qbitScanEnqueuer struct {
	store  *store.Store
	logger *slog.Logger
	wake   *jobWake
}

func (q qbitScanEnqueuer) EnqueueScan(ctx context.Context, libraryID store.LibraryID, root string) error {
	enqueueScanJob(ctx, q.store, q.logger, q.wake, libraryID, root)
	return nil
}

// v1ScanEnqueuer builds scan jobs at the composition root so server keeps its
// no-scan-import boundary (ARCHITECTURE.md §4).
func v1ScanEnqueuer(st *store.Store, wake *jobWake) server.V1ScanEnqueuer {
	return func(ctx context.Context, libraryID store.LibraryID, root string) (store.JobID, error) {
		job, err := newScanJob(libraryID, root, time.Now().UTC())
		if err != nil {
			return "", err
		}
		created, err := enqueueAndWake(ctx, st, wake, job)
		if err != nil {
			return "", err
		}
		return created.ID, nil
	}
}

func enqueueScanJob(ctx context.Context, st *store.Store, logger *slog.Logger, wake *jobWake, libraryID store.LibraryID, root string) {
	job, err := newScanJob(libraryID, root, time.Now().UTC())
	if err == nil {
		_, err = enqueueAndWake(ctx, st, wake, job)
	}
	if err != nil {
		logger.Warn("scan enqueue failed", "library", string(libraryID), "root", root, "error", err)
	}
}

func newScanJob(libraryID store.LibraryID, root string, createdAt time.Time) (store.Job, error) {
	return scanpkg.NewJob(scanpkg.JobRequest{
		LibraryID: libraryID,
		Root:      root,
		CreatedAt: createdAt,
	})
}

func activeBundle(registry *plugin.Registry, id string) (plugin.Bundle, bool) {
	if registry == nil || id == "" {
		return plugin.Bundle{}, false
	}
	return registry.Bundle(plugin.ID(id))
}

func scanParsers(registry *plugin.Registry, supplied map[string]scanpkg.FilenameParser) map[string]scanpkg.FilenameParser {
	parsers := map[string]scanpkg.FilenameParser{}
	for pluginID, parser := range supplied {
		if parser == nil {
			continue
		}
		if _, ok := activeBundle(registry, pluginID); ok {
			parsers[pluginID] = parser
		}
	}
	if len(parsers) == 0 {
		return nil
	}
	return parsers
}

func registerIdentifyScorers(service *identify.Service, registry *plugin.Registry, supplied map[string]IdentifyScorerHook) {
	if service == nil {
		return
	}
	for pluginID, hook := range supplied {
		if _, ok := activeBundle(registry, pluginID); !ok {
			continue
		}
		service.RegisterScorer(pluginID, hook.Scorer, hook.Threshold)
	}
}

// scanIdentifier adapts identify.Service to scan.Identifier (PIPE-1, ADR-0003
// §4). It is the composition-root bridge that lets scan trigger auto-identify
// without importing internal/identify (arch §4 forbids the edge; identify also
// cannot import scan). It maps scan.ParsedMedia onto identify's domain-agnostic
// twin and drops the AutoOutcome — scan only needs the error. service is set
// after identify.Service is fully wired; a nil service (before wiring, or in a
// build with no identification) is a no-op.
type scanIdentifier struct {
	service  *identify.Service
	registry *plugin.Registry
}

func (a *scanIdentifier) AutoIdentify(ctx context.Context, mediaID store.MediaID, pluginID, fingerprint string, parsed scanpkg.ParsedMedia) error {
	if a.service == nil {
		return nil
	}
	if bundle, ok := activeBundle(a.registry, pluginID); ok && bundle.IdentificationPolicy == plugin.IdentificationPolicyQueue {
		_, err := a.service.QueueManual(ctx, mediaID, pluginID, fingerprint, identify.ParsedMedia{
			Title: parsed.Title,
			Class: parsed.Class,
			Year:  parsed.Year,
		})
		return err
	}
	_, err := a.service.AutoIdentify(ctx, mediaID, pluginID, fingerprint, identify.ParsedMedia{
		Title:    parsed.Title,
		Class:    parsed.Class,
		Year:     parsed.Year,
		Sequence: parsed.Sequence,
		Index:    parsed.Index,
		IndexEnd: parsed.IndexEnd,
	})
	return err
}

type validatingLocalExtractor struct {
	inner     scanpkg.LocalExtractor
	relations map[string]string
}

func (e validatingLocalExtractor) ExtractLocal(ctx context.Context, class, root, path string) (scanpkg.LocalMetadata, error) {
	result, err := e.inner.ExtractLocal(ctx, class, root, path)
	if err != nil {
		return scanpkg.LocalMetadata{}, err
	}
	entities := make([]scanpkg.LocalEntity, 0, len(result.Entities))
	for _, entity := range result.Entities {
		cardinality, ok := e.relations[entity.Kind+"\x00"+entity.Relation]
		if !ok {
			continue
		}
		entity.Cardinality = cardinality
		entities = append(entities, entity)
	}
	return scanpkg.LocalMetadata{Fields: result.Fields, ImpliedMediaFields: result.ImpliedMediaFields, Entities: entities}, nil
}

func scanLocalExtractors(registry *plugin.Registry, supplied map[string][]scanpkg.LocalExtractor) map[string][]scanpkg.LocalExtractor {
	out := map[string][]scanpkg.LocalExtractor{}
	for pluginID, extractors := range supplied {
		bundle, ok := activeBundle(registry, pluginID)
		if !ok || !hasLocalFileScraper(bundle) {
			continue
		}
		relations := mediaRelationCardinalities(bundle)
		for _, extractor := range extractors {
			if extractor != nil {
				out[pluginID] = append(out[pluginID], validatingLocalExtractor{inner: extractor, relations: relations})
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func entityKindPolicies(plugins *plugin.Registry) []store.EntityKindPolicy {
	var policies []store.EntityKindPolicy
	for _, id := range plugins.IDs() {
		bundle, _ := plugins.Bundle(id)
		for _, kind := range bundle.EntityKinds {
			if !kind.FullTextSearchable && kind.OrphanPolicy != "gc" && !kind.CursorPaginated {
				continue
			}
			policies = append(policies, store.EntityKindPolicy{
				Kind:               kind.Name,
				FullTextSearchable: kind.FullTextSearchable,
				OrphanGC:           kind.OrphanPolicy == "gc",
				CursorPaginated:    kind.CursorPaginated,
			})
		}
	}
	return policies
}

func entityKindOrderings(plugins *plugin.Registry) []store.EntityKindOrdering {
	var orderings []store.EntityKindOrdering
	for _, id := range plugins.IDs() {
		bundle, _ := plugins.Bundle(id)
		for _, kind := range bundle.EntityKinds {
			if kind.OrderingSourceField != "" {
				orderings = append(orderings, store.EntityKindOrdering{
					Kind: kind.Name, SourceField: kind.OrderingSourceField,
				})
			}
		}
	}
	return orderings
}

func hasLocalFileScraper(bundle plugin.Bundle) bool {
	for _, spec := range bundle.Scrapers {
		if plugin.LocalFileScraperKind(spec.Kind) {
			return true
		}
	}
	return false
}

func mediaRelationCardinalities(bundle plugin.Bundle) map[string]string {
	out := map[string]string{}
	for _, kind := range bundle.EntityKinds {
		for _, relation := range kind.Relations {
			if relation.ToKind == plugin.MediaKindName {
				out[kind.Name+"\x00"+relation.Name] = relation.Cardinality
			}
		}
	}
	return out
}

// scrobbleRelationSelectors reads the plugin-owned scrobble-match relation slugs
// off the activated bundles (ADR-0098 area 1), so the generic migrate matcher
// receives data instead of hardcoding a music relation name. Every activated
// bundle that declares a ScrobbleMatch contributes its slugs (in practice the
// Music plugin); the merged set is deduped and order-stable.
func scrobbleRelationSelectors(registry *plugin.Registry) migrate.ScrobbleRelationSelectors {
	var selectors migrate.ScrobbleRelationSelectors
	if registry == nil {
		return selectors
	}
	seenArtist := map[string]bool{}
	seenAlbum := map[string]bool{}
	for _, id := range registry.IDs() {
		bundle, ok := registry.Bundle(id)
		if !ok || bundle.ScrobbleMatch == nil {
			continue
		}
		for _, name := range bundle.ScrobbleMatch.ArtistConfirm {
			if name != "" && !seenArtist[name] {
				seenArtist[name] = true
				selectors.ArtistConfirm = append(selectors.ArtistConfirm, name)
			}
		}
		for _, name := range bundle.ScrobbleMatch.AlbumTieBreak {
			if name != "" && !seenAlbum[name] {
				seenAlbum[name] = true
				selectors.AlbumTieBreak = append(selectors.AlbumTieBreak, name)
			}
		}
	}
	return selectors
}

// migrationSourceResolver maps a Migration Wizard run's (pluginID, importerID,
// sourcePath) to the plugin parser that yields its foreign items (ADR-0037
// §2.1). It constructs only importers declared by an active, validated bundle;
// undeclared pairs fail closed instead of letting Core invent source systems.
type migrationSourceResolver struct {
	plugins   *plugin.Registry
	factories map[MigrationSourceKey]MigrationSourceFactory
}

func (r migrationSourceResolver) Resolve(pluginID, importerID string, categories []string, sourcePath string) (migrate.Source, bool) {
	if !declaresImporter(r.plugins, pluginID, importerID) {
		return nil, false
	}
	factory, ok := r.factories[MigrationSourceKey{PluginID: pluginID, ImporterID: importerID}]
	if !ok {
		return nil, false
	}
	return factory(categories, sourcePath), true
}

func declaresImporter(registry *plugin.Registry, pluginID, importerID string) bool {
	bundle, ok := activeBundle(registry, pluginID)
	if !ok {
		return false
	}
	for _, importer := range bundle.Importers {
		if importer.ID == importerID {
			return true
		}
	}
	return false
}

type migrationResumePolicyResolver struct {
	store   *store.Store
	plugins *plugin.Registry
}

func (r migrationResumePolicyResolver) ResumePolicy(ctx context.Context, mediaID store.MediaID) (store.ResumePolicy, error) {
	class, err := r.store.MediaClass(ctx, mediaID)
	if err != nil {
		return store.ResumePolicy{}, err
	}
	policy := store.ResumePolicy{Class: class}
	libraryID, err := r.store.MediaLibrary(ctx, mediaID)
	if err != nil {
		return policy, err
	}
	pluginID, err := r.store.LibraryPluginID(ctx, libraryID)
	if err != nil || pluginID == "" || r.plugins == nil {
		return policy, err
	}
	if bundle, ok := r.plugins.Bundle(plugin.ID(pluginID)); ok {
		policy.Override = bundle.Engagement.ResumeMedia
	}
	return policy, nil
}

// playbackCacheController adapts the precomputed-playback-rendition cache
// substrate into the graph handler's PlaybackCacheController seam:
// explicit operator generation (playback.PrecomputeMediaRenditions, §2 — never
// automatic), on-demand budget eviction (playback.EvictPrecomputedRenditions-
// OverBudget, §4), and the configured disk budget. It holds the cache root and
// budget so the graph package never carries those policy values.
type playbackCacheController struct {
	store     *store.Store
	cacheRoot string
	budget    int64
	now       func() time.Time
	wake      *jobWake
	// composer, when set, lets a picture-less Media precompute as its
	// composed-picture rendition instead of the ladder.
	composer playback.PictureComposer
}

func (c playbackCacheController) PrecomputeMedia(ctx context.Context, mediaID store.MediaID) (int, error) {
	jobs, err := playback.PrecomputeComposedRendition(ctx, c.store, mediaID, c.composer, c.cacheRoot, c.clock())
	if err == nil && len(jobs) == 0 {
		jobs, err = playback.PrecomputeMediaRenditions(ctx, c.store, mediaID, "h264", c.cacheRoot, c.clock())
	}
	if err == nil && len(jobs) > 0 {
		c.wake.notify()
	}
	return len(jobs), err
}

func (c playbackCacheController) EvictOverBudget(ctx context.Context) (int, error) {
	evicted, err := playback.EvictPrecomputedRenditionsOverBudget(ctx, c.store, c.cacheRoot, c.budget, c.clock())
	return len(evicted), err
}

func (c playbackCacheController) BudgetBytes() int64 { return c.budget }

func (c playbackCacheController) clock() time.Time {
	if c.now != nil {
		return c.now().UTC()
	}
	return time.Now().UTC()
}

// playbackAssetsLocator bridges *genassets.Service into playback's AssetsLocator
// seam (ADR-0098 area 3). The trickplay/preview/loudness existence methods pass
// through; LoudnessForFingerprint maps genassets' wire LoudnessAnalysis into
// playback's package-owned LoudnessData value type, so neither package imports
// the other and the loudness measurement crosses the boundary by shape, not by a
// shared struct.
type playbackAssetsLocator struct {
	service *genassets.Service
}

func (l playbackAssetsLocator) TrickplayAsset(fingerprint string) (string, bool) {
	return l.service.TrickplayAsset(fingerprint)
}

func (l playbackAssetsLocator) PreviewClipAsset(fingerprint string) (string, bool) {
	return l.service.PreviewClipAsset(fingerprint)
}

// Waveform/BeatGrid/SyncLanes/SilenceMap forward the first-class optional audio Generated
// Asset locators (ADR-0098 area 2).
func (l playbackAssetsLocator) WaveformAsset(fingerprint string) (string, bool) {
	return l.service.WaveformAsset(fingerprint)
}

func (l playbackAssetsLocator) BeatGridAsset(fingerprint string) (string, bool) {
	return l.service.BeatGridAsset(fingerprint)
}

func (l playbackAssetsLocator) SyncLanesAsset(fingerprint string) (string, bool) {
	return l.service.SyncLanesAsset(fingerprint)
}

func (l playbackAssetsLocator) SilenceMapAsset(fingerprint string) (string, bool) {
	return l.service.SilenceMapAsset(fingerprint)
}

func (l playbackAssetsLocator) LoudnessAsset(fingerprint string) (string, bool) {
	return l.service.LoudnessAsset(fingerprint)
}

func (l playbackAssetsLocator) LoudnessForFingerprint(fingerprint string) (playback.LoudnessData, bool) {
	analysis, ok := l.service.LoudnessForFingerprint(fingerprint)
	if !ok {
		return playback.LoudnessData{}, false
	}
	return playback.LoudnessData{
		IntegratedLUFS:  analysis.IntegratedLUFS,
		LoudnessRangeLU: analysis.LoudnessRangeLU,
		TruePeakDBTP:    analysis.TruePeakDBTP,
		ReferenceLUFS:   analysis.ReferenceLUFS,
		GainDB:          analysis.GainDB,
		Source:          analysis.Source,
	}, true
}

type scanObserverAdapter struct {
	service *watcher.Service
}

func (a scanObserverAdapter) Observe(libraryID store.LibraryID, root string) {
	if a.service != nil {
		_ = a.service.Observe(libraryID, root)
	}
}

// watcherStatusAdapter bridges the watcher.Service into the graph handler's
// WatcherStatusProvider seam. It exposes only the
// non-secret aggregate Watcher state per Library to the manager Console.
type watcherStatusAdapter struct {
	service *watcher.Service
}

func (a watcherStatusAdapter) Status(libraryID store.LibraryID) (active bool, available bool, reason string) {
	if a.service == nil {
		return false, false, ""
	}
	status := a.service.Status(libraryID)
	return status.Active, status.Available, status.Error
}

// qbittorrentIngestAdapter bridges the qBittorrent Ingestor into the graph
// handler's QBittorrentIngestProvider seam. It exposes
// only the non-secret config (configured-flag + routing map) and the status
// feed — never the endpoint credential, which lives in a 0600 secret file.
type qbittorrentIngestAdapter struct {
	ingestor *qbittorrent.Ingestor
}

func (a qbittorrentIngestAdapter) Config(ctx context.Context) (graph.QBittorrentConfig, bool) {
	// An inert ingestor (no endpoint configured) reports unconfigured even
	// though it exists, so the Console read shows "ingest disabled" honestly
	// until setQBittorrentIngest sets an endpoint.
	if a.ingestor == nil || !a.ingestor.Configured() {
		return graph.QBittorrentConfig{}, false
	}
	routes := []graph.QBittorrentRoute{}
	for category, library := range a.ingestor.Routes() {
		routes = append(routes, graph.QBittorrentRoute{Category: category, LibraryID: string(library)})
	}
	return graph.QBittorrentConfig{Routes: routes}, true
}

func (a qbittorrentIngestAdapter) Status(ctx context.Context) []graph.QBittorrentStatusEntry {
	if a.ingestor == nil {
		return nil
	}
	feed := a.ingestor.StatusFeed()
	out := make([]graph.QBittorrentStatusEntry, len(feed))
	for i, entry := range feed {
		at := ""
		if !entry.At.IsZero() {
			at = entry.At.UTC().Format(time.RFC3339)
		}
		out[i] = graph.QBittorrentStatusEntry{
			ReleaseName: entry.ReleaseName,
			Category:    entry.Category,
			Outcome:     string(entry.Outcome),
			LibraryID:   entry.LibraryID,
			Detail:      entry.Detail,
			At:          at,
		}
	}
	return out
}

// qbittorrentConfigurer bridges the Console's setQBittorrentIngest mutation
// into the running ingestor. It is the write half of the
// ingest seam: it builds a fresh qBittorrent Source from the operator's
// connection settings + the inbound password, validates the candidate routing
// (the ingestor's Reconfigure runs ValidateRouting before committing), and only
// on success persists the password to the 0600 secret file and the routing +
// connection to the config store. The password never touches the catalog DB or
// a log line (invariant #5). A rejected routing leaves the running ingestor and
// the persisted config untouched.
type qbittorrentConfigurer struct {
	ingestor *qbittorrent.Ingestor
	store    *store.Store
	secrets  secretFiles
}

func (c qbittorrentConfigurer) Configure(ctx context.Context, in graph.QBittorrentIngestInput) error {
	if c.ingestor == nil {
		return fmt.Errorf("qbittorrent ingest: not running on this server")
	}
	// Build the candidate client from the inbound connection + password. When
	// the operator left the password unchanged (SetPassword false), reuse the
	// one already in the secure store so a routing-only edit need not resend it.
	creds := qbittorrent.Credentials{
		BaseURL:  in.URL,
		Username: in.Username,
	}
	if in.SetPassword {
		creds.Password = in.Password
	} else if in.Username != "" {
		stored, err := c.secrets.Secret("qbittorrent")
		if err == nil {
			creds.Password = stored
		}
	}
	client, err := qbittorrent.NewClient(creds)
	if err != nil {
		// An invalid URL is operator-supplied: surface it as a client-safe
		// validation error rather than a 500.
		return store.ValidationError(err.Error())
	}
	routing := map[string]store.LibraryID{}
	for _, route := range in.Routes {
		routing[route.Category] = store.LibraryID(route.LibraryID)
	}
	// Save paths feed ValidateRouting's download-dir-overlap check (§2). A fetch
	// error (qBittorrent unreachable / bad creds) surfaces as the validation
	// failure, since Reconfigure's ValidateRouting calls Categories itself; we
	// only need the save-path list, fetched here from the same candidate client.
	downloadDirs, err := qbittorrentSavePaths(ctx, client)
	if err != nil {
		return store.ValidationError("qBittorrent unreachable or rejected the credentials: " + err.Error())
	}
	source := qbittorrent.NewSource(client)
	// Reconfigure validates the candidate routing (category existence +
	// save-path overlap) and only swaps the live source/routing on success —
	// applied without a process restart (§6).
	if err := c.ingestor.Reconfigure(ctx, source, routing, downloadDirs); err != nil {
		return store.ValidationError(err.Error())
	}
	// Live config accepted: persist the password to the secure store (invariant
	// #5) and the routing + connection to the config store so the next process
	// start rebuilds the same ingestor.
	if in.SetPassword {
		if err := c.secrets.SetSecret("qbittorrent", in.Password); err != nil {
			return fmt.Errorf("qbittorrent ingest: storing password: %w", err)
		}
	}
	routes := make([]store.QBittorrentRouteConfig, 0, len(in.Routes))
	for _, route := range in.Routes {
		routes = append(routes, store.QBittorrentRouteConfig{Category: route.Category, LibraryID: route.LibraryID})
	}
	patch := store.QBittorrentConfigPatch{
		URL:      &in.URL,
		Username: &in.Username,
		Routes:   &routes,
	}
	if _, err := c.store.SetQBittorrentConfig(ctx, patch, time.Now().UTC()); err != nil {
		return err
	}
	return nil
}

// qbittorrentSavePaths reads the candidate client's default save path plus its
// category save paths: the download roots the operator is approving. They feed
// ValidateRouting's download-dir-overlap validation and become
// the Ingestor's canonical approved download roots — the containment bound on
// every endpoint-nominated payload path. The default save path is included
// because a category with no save path of its own lands downloads there. The
// Source seam exposes only category names, so both come straight off the
// Client.
func qbittorrentSavePaths(ctx context.Context, client *qbittorrent.Client) ([]string, error) {
	categories, err := client.Categories(ctx)
	if err != nil {
		return nil, err
	}
	defaultRoot, err := client.DefaultSavePath(ctx)
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(categories)+1)
	if defaultRoot != "" {
		dirs = append(dirs, defaultRoot)
	}
	for _, category := range categories {
		if category.SavePath != "" {
			dirs = append(dirs, category.SavePath)
		}
	}
	return dirs, nil
}

// updateCheckAdapter bridges the opt-in version-check service into the graph
// handler's UpdateChecker seam. It only translates the
// result shape; the no-telemetry / default-off behavior lives in the service.
// The State token crosses the seam as its stable string form so the graph
// package keeps no dependency on updatecheck; app_updatecheck_adapter_test
// pins the two token sets to each other.
type updateCheckAdapter struct {
	service *updatecheck.Service
}

func (a updateCheckAdapter) Check(ctx context.Context, enabled bool, channel, currentVersion string) (graph.UpdateStatusInfo, error) {
	status, err := a.service.Check(ctx, enabled, channel, currentVersion)
	if err != nil {
		return graph.UpdateStatusInfo{}, err
	}
	return graph.UpdateStatusInfo{
		State:           status.State.String(),
		Enabled:         status.Enabled,
		Channel:         status.Channel,
		CurrentVersion:  status.CurrentVersion,
		LatestVersion:   status.LatestVersion,
		UpdateAvailable: status.UpdateAvailable,
	}, nil
}

// pushTokenStore adapts *store.Store to push.TokenStore: the push
// package owns its persistence seam and does not import the store, so the
// composition root translates between push.TokenRecord and store.DevicePushToken.
type pushTokenStore struct {
	store *store.Store
}

func (s pushTokenStore) UpsertDevicePushToken(ctx context.Context, t push.TokenRecord) error {
	return s.store.UpsertDevicePushToken(ctx, store.DevicePushToken{
		DeviceID:  store.DeviceID(t.DeviceID),
		ProfileID: store.ProfileID(t.ProfileID),
		Platform:  t.Platform,
		Token:     t.Token,
	})
}

// pushRegistrarAdapter bridges the persisting push.Registrar into the graph
// handler's PushRegistrar seam. It only translates the
// (deviceID, profileID, platform, token) argument shape into a push.Registration;
// the default-off gate and the validation live in the registrar.
type pushRegistrarAdapter struct {
	registrar *push.Registrar
}

func (a pushRegistrarAdapter) Register(ctx context.Context, deviceID, profileID, platform, token string) error {
	return a.registrar.Register(ctx, push.Registration{
		ProfileID: profileID,
		DeviceID:  deviceID,
		Platform:  push.PushPlatform(platform),
		Token:     token,
	})
}

func (a pushRegistrarAdapter) Enabled() bool { return a.registrar.Enabled() }

// scrobbleLinkSource adapts *store.Store to scrobble.LinkSource: the
// scrobble package owns its consumer seam and does not import the store, so the
// composition root translates between the store's ScrobbleLink rows and the
// plugin's Link/eligibility view. The SecretRef rides through (the dispatcher
// resolves it via the secure store at push time); no token ever crosses here.
type scrobbleLinkSource struct {
	store *store.Store
}

func (s scrobbleLinkSource) LinksFor(ctx context.Context, profileID string) ([]scrobble.Link, error) {
	rows, err := s.store.ScrobbleLinksFor(ctx, store.ProfileID(profileID))
	if err != nil {
		return nil, err
	}
	links := make([]scrobble.Link, 0, len(rows))
	for _, row := range rows {
		links = append(links, scrobble.Link{
			Service:      scrobble.Service(row.Service),
			SecretRef:    row.SecretRef,
			AccountLabel: row.AccountLabel,
		})
	}
	return links, nil
}

func (s scrobbleLinkSource) LibraryEligible(ctx context.Context, libraryID string) (bool, error) {
	return s.store.ScrobbleLibraryEligible(ctx, store.LibraryID(libraryID))
}

// scrobbleWatchedObserver bridges the graph handler's WatchedObserver seam
// into the scrobble dispatcher: it translates the generic
// graph.WatchedTransition into the plugin's scrobble.WatchedEvent (mapping the
// store's MediaClass + External IDs into the plugin's generic shape) and
// enqueues it. Non-blocking by construction — the dispatcher's Watched returns
// immediately, so the playback path never waits on a third-party service.
type scrobbleWatchedObserver struct {
	dispatcher *scrobble.Dispatcher
}

func (o scrobbleWatchedObserver) Watched(t graph.WatchedTransition) {
	class := scrobble.ClassVideo
	if t.Class == store.MediaClassAudio {
		class = scrobble.ClassAudio
	}
	ids := make([]scrobble.ExternalID, 0, len(t.ExternalIDs))
	for _, id := range t.ExternalIDs {
		ids = append(ids, scrobble.ExternalID{Scheme: id.Scheme, Value: id.Value})
	}
	o.dispatcher.Watched(scrobble.WatchedEvent{
		ProfileID:   string(t.ProfileID),
		MediaID:     string(t.MediaID),
		LibraryID:   string(t.LibraryID),
		Class:       class,
		Title:       t.Title,
		Year:        t.Year,
		ExternalIDs: ids,
		Music: scrobble.MusicMetadata{
			Artist:      t.Audio.ContributorName,
			Album:       t.Audio.ReleaseName,
			ArtistMBID:  t.Audio.ContributorMBID,
			ReleaseMBID: t.Audio.ReleaseMBID,
		},
		WatchedAt: t.WatchedAt,
	})
}

// scrobbleStatusSource adapts *scrobble.Dispatcher to graph.ScrobbleStatusSource
// (ADR-0098 area 7, interface-at-consumer): it translates the dispatcher's
// dead-letter / skip feeds and configured-adapter descriptors into the graph's
// generic status view. It carries no secret — dead-letter/skip rows hold only
// generic ids + sanitized reasons (invariant #5), and adapter descriptors are
// the public service token + class + schemes.
type scrobbleStatusSource struct {
	dispatcher *scrobble.Dispatcher
}

func (s scrobbleStatusSource) DeadLetters() []graph.ScrobbleDeadLetterInfo {
	rows := s.dispatcher.DeadLetters()
	out := make([]graph.ScrobbleDeadLetterInfo, 0, len(rows))
	for _, r := range rows {
		at := ""
		if !r.At.IsZero() {
			at = r.At.UTC().Format(time.RFC3339)
		}
		out = append(out, graph.ScrobbleDeadLetterInfo{
			ProfileID: r.ProfileID,
			Service:   r.Service,
			MediaID:   r.MediaID,
			Attempts:  r.Attempts,
			LastError: r.LastError,
			At:        at,
		})
	}
	return out
}

func (s scrobbleStatusSource) Skips() []graph.ScrobbleSkipInfo {
	rows := s.dispatcher.Skips()
	out := make([]graph.ScrobbleSkipInfo, 0, len(rows))
	for _, r := range rows {
		at := ""
		if !r.At.IsZero() {
			at = r.At.UTC().Format(time.RFC3339)
		}
		out = append(out, graph.ScrobbleSkipInfo{
			ProfileID: r.ProfileID,
			MediaID:   r.MediaID,
			Service:   r.Service,
			Reason:    r.Reason,
			At:        at,
		})
	}
	return out
}

func (s scrobbleStatusSource) Adapters() []graph.ScrobbleAdapterInfo {
	rows := s.dispatcher.AdapterDescriptors()
	out := make([]graph.ScrobbleAdapterInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, graph.ScrobbleAdapterInfo{
			Service:         r.Service,
			MediaClass:      r.Class,
			IdentitySchemes: r.IdentitySchemes,
		})
	}
	return out
}

// musicSelectors names the plugin-owned relation/field/scheme selectors the
// outbound-metadata resolver walks (ADR-0098 area 7 / hydration: "the matcher
// receives plugin-owned relation selectors"). Core hardcodes no Music slugs;
// the composition root supplies them, defaulted from the Music bundle's declared
// relations/fields. The Media→Entity relations (artistRelation/albumRelation)
// are the ones MUSIC-1 hydration writes; until that lands, the generic reads
// simply find nothing and the metadata degrades cleanly (ADR-0098: "degrade when
// absent").
type musicSelectors struct {
	// artistRelation is the relation reaching the Media's performing Artist
	// entity (ADR-0029 §3 "appears-on" / ADR-0098 "artist/appears-on" to Media).
	artistRelation string
	// albumRelation is the relation reaching the Media's Album entity
	// (ADR-0098 "album/album-track" to Media).
	albumRelation string
	// artistMBIDField / releaseMBIDField are the entity text fields carrying the
	// MusicBrainz ids (ADR-0029 §4 Picard tags).
	artistMBIDField  string
	releaseMBIDField string
}

// scrobbleMetadataResolver fills the richer outbound music metadata for a
// watched audio Media (ADR-0098 area 7) from generic Entity/relation/field reads
// driven by plugin-owned selectors. It runs server-internally (no user session)
// so it reads with the full-visibility grant; its only consumer is the
// already-eligible outbound dispatch (the per-Library eligibility gate has
// already passed), so it widens nothing — it only enriches an event already
// cleared for outbound.
type scrobbleMetadataResolver struct {
	store     *store.Store
	selectors musicSelectors
}

// newScrobbleMetadataResolver builds the resolver with selectors defaulted from
// the activated Music bundle's declared relations/fields when present, else the
// ADR-0029/0098-named defaults. plugins may be nil (tests) → defaults.
func newScrobbleMetadataResolver(st *store.Store, plugins *plugin.Registry) *scrobbleMetadataResolver {
	sel := musicSelectors{
		artistRelation:   "appears-on",
		albumRelation:    "album-track",
		artistMBIDField:  "musicbrainz-id",
		releaseMBIDField: "musicbrainz-release-id",
	}
	return &scrobbleMetadataResolver{store: st, selectors: sel}
}

// metadataLibraryFilter is the server-internal full-visibility grant the
// resolver reads with (markers/artwork use the same): a zero-value filter would
// fail closed and silently empty every lookup.
var metadataLibraryFilter = store.LibraryFilter{AllLibraries: true, Revealed: true}

func (r *scrobbleMetadataResolver) ResolveOutboundAudioMetadata(ctx context.Context, mediaID store.MediaID) graph.OutboundAudioMetadata {
	if r == nil || r.store == nil {
		return graph.OutboundAudioMetadata{}
	}
	var md graph.OutboundAudioMetadata
	// Artist: the Media's related Artist entity (display name + MBID). Best-
	// effort: any miss leaves the field empty and the submission degrades.
	if artist, ok := r.firstRelatedEntity(ctx, mediaID, r.selectors.artistRelation); ok {
		md.ContributorName = artist.Title
		md.ContributorMBID = entityFieldText(artist, r.selectors.artistMBIDField)
	}
	// Album: the Media's related Album entity (display name + release MBID).
	if album, ok := r.firstRelatedEntity(ctx, mediaID, r.selectors.albumRelation); ok {
		md.ReleaseName = album.Title
		md.ReleaseMBID = entityFieldText(album, r.selectors.releaseMBIDField)
	}
	return md
}

// firstRelatedEntity returns the first Entity reached from the Media by the
// named relation, with its fields loaded. ok=false on any miss/error (degrade).
func (r *scrobbleMetadataResolver) firstRelatedEntity(ctx context.Context, mediaID store.MediaID, relation string) (store.Entity, bool) {
	if relation == "" {
		return store.Entity{}, false
	}
	related, err := r.store.EntitiesForMedia(ctx, mediaID, relation, metadataLibraryFilter)
	if err != nil || len(related) == 0 {
		return store.Entity{}, false
	}
	// Load the full entity for its fields (EntitiesForMedia returns no fields).
	full, err := r.store.Entity(ctx, related[0].Entity.ID, metadataLibraryFilter)
	if err != nil {
		// Fall back to the relation-row entity (display name only, no MBID).
		return related[0].Entity, true
	}
	return full, true
}

// entityFieldText returns the named text field's value, or "" when absent.
func entityFieldText(entity store.Entity, name string) string {
	if name == "" {
		return ""
	}
	for _, f := range entity.Fields {
		if f.Name == name {
			return strings.TrimSpace(f.Text)
		}
	}
	return ""
}

// enqueueAssetSweep schedules the Generated Assets sweep; failure
// to enqueue is logged, never fatal — the next scan or restart re-arms it.
// The job dedupes while one is queued, running, or yielded.
func enqueueAssetSweep(ctx context.Context, st *store.Store, logger *slog.Logger, wake *jobWake) {
	job, err := genassets.NewSweepJob(time.Now().UTC())
	if err == nil {
		_, err = enqueueAndWake(ctx, st, wake, job)
	}
	if err != nil {
		logger.Warn("asset sweep enqueue failed", "error", err)
	}
}

func registerBuiltInSchedules(ctx context.Context, st *store.Store, service *schedulepkg.Service, precomputeCacheRoot string, precomputeBudgetBytes int64, composesPictures bool) {
	service.Register(singleJobSchedule(retentionSweepJobKind, schedulepkg.Daily(4, 0)))
	service.Register(singleJobSchedule(databaseBackupJobKind, schedulepkg.Daily(3, 0)))
	// Events.db retention compaction: 03:30 daily, just after
	// the backup, so a corrupt prune is captured by that morning's pre-compaction
	// backup. DedupeKey-deduped like every seeded Schedule.
	service.Register(singleJobSchedule(eventsCompactionJobKind, schedulepkg.Daily(3, 30)))
	service.Register(singleJobSchedule(offlinepack.ExpireJobKind, schedulepkg.Daily(3, 45)))
	// Subtitle offset-nudge sweep: WEEKLY, not daily. The retention
	// window is store.SubtitleOffsetRetention (a year of disuse) and the rows are a
	// few dozen bytes each, so a daily pass would be churn against a table that
	// can barely grow; a weekly sweep bounds it just as tightly. Sunday 04:30 sits
	// clear of the nightly backup/compaction window above. DedupeKey-deduped like
	// every seeded Schedule.
	service.Register(singleJobSchedule(jobs.SubtitleOffsetPruneJobKind, schedulepkg.Weekly(time.Sunday, 4, 30)))
	// Precomputed playback rendition cache eviction (ADR-0057 §4: disk-budget
	// governance). Seeded only when the operator set a positive budget; the sweep
	// runs hourly, evicting least-recently-used cache objects over budget and
	// removing their disposable bytes. DedupeKey-deduped so a slow sweep cannot
	// stack jobs. No cache configured → no Schedule (the cache controls are off).
	if precomputeBudgetBytes > 0 && precomputeCacheRoot != "" {
		service.Register(schedulepkg.Spec{
			ID:             string(playback.PrecomputeEvictJobKind),
			JobKind:        playback.PrecomputeEvictJobKind,
			DefaultTrigger: schedulepkg.Every(1, schedulepkg.Hours),
			BuildJobs: func(_ context.Context, now time.Time) ([]store.Job, error) {
				return playback.PrecomputeEvictScheduleJob(precomputeCacheRoot, precomputeBudgetBytes)(now)
			},
		})
	}
	// Composed-picture renditions (ADR-0057 §2: a Schedule is an explicit
	// trigger): hourly, render each Media whose plugin describes a picture and
	// has no rendition for it yet, so a new song plays as video without an
	// operator action. Seeded only when a plugin composes pictures and the cache
	// has a budget (with none, admission refuses every render); disableable and
	// DedupeKey-deduped like every seeded Schedule.
	if composesPictures && precomputeBudgetBytes > 0 && precomputeCacheRoot != "" {
		service.Register(singleJobSchedule(playback.ComposedSweepJobKind, schedulepkg.Every(1, schedulepkg.Hours)))
	}
	service.Register(singleJobSchedule(ingest.PollJobKind, schedulepkg.Every(1, schedulepkg.Hours)))
	service.Register(singleJobSchedule(qbittorrent.PollJobKind, schedulepkg.Every(5, schedulepkg.Minutes)))
	// Decode-verify sampling: weekly on Sunday at 05:00,
	// heavy I/O / low urgency. Disableable like every seeded Schedule; the
	// DedupeKey (singleJobSchedule sets it to the job kind) means a slow week of
	// Sundays cannot stack jobs — re-enqueue while one is active is a no-op.
	service.Register(singleJobSchedule(decodeverify.SweepJobKind, schedulepkg.Weekly(time.Sunday, 5, 0)))
	libraries, err := st.Libraries(ctx, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		return
	}
	for _, library := range libraries {
		service.Register(scanScheduleSpec(st, library.ID))
	}
}

func singleJobSchedule(kind store.JobKind, trigger schedulepkg.Trigger) schedulepkg.Spec {
	return schedulepkg.Spec{
		ID:             string(kind),
		JobKind:        kind,
		DefaultTrigger: trigger,
		BuildJobs: func(_ context.Context, now time.Time) ([]store.Job, error) {
			return []store.Job{{
				Kind:      kind,
				Class:     store.JobClassBackground,
				DedupeKey: string(kind),
				CreatedAt: now,
			}}, nil
		},
	}
}

func scanScheduleSpec(st *store.Store, libraryID store.LibraryID) schedulepkg.Spec {
	return schedulepkg.Spec{
		ID:             string(schedulepkg.ScanScheduleID(libraryID)),
		JobKind:        scanpkg.JobKind,
		DefaultTrigger: schedulepkg.Every(12, schedulepkg.Hours),
		BuildJobs: func(ctx context.Context, now time.Time) ([]store.Job, error) {
			roots, err := st.LibraryRoots(ctx, libraryID)
			if err != nil {
				return nil, err
			}
			jobs := make([]store.Job, 0, len(roots))
			for _, root := range roots {
				job, err := newScanJob(libraryID, root, now)
				if err != nil {
					return nil, err
				}
				jobs = append(jobs, job)
			}
			return jobs, nil
		},
	}
}

// appEncryptedContentSource adapts the Offline Pack byte route's plain-string
// object identity onto libencrypt's ObjectContext (the pack substrate stays
// decoupled from the crypto layer).
type appEncryptedContentSource struct {
	manager *libencrypt.Manager
}

func (s appEncryptedContentSource) OpenDecryptReader(libraryID, ciphertextPath, objectClass, immutableObjectID string) (io.ReadSeekCloser, error) {
	return s.manager.OpenDecryptReader(libraryID, ciphertextPath, libencrypt.ObjectContext{
		LibraryID:         libraryID,
		ObjectClass:       libencrypt.ObjectClass(objectClass),
		ImmutableObjectID: immutableObjectID,
	})
}

type appLibEncryptDBCipher struct {
	manager interface {
		EncryptFile(libraryID, plaintextPath, ciphertextPath string, obj libencrypt.ObjectContext) error
		OpenDecryptReader(libraryID, ciphertextPath string, obj libencrypt.ObjectContext) (io.ReadSeekCloser, error)
		UnsealDBFile(libraryID, ciphertextPath, workingPath string, obj libencrypt.ObjectContext) error
		SealDBFile(libraryID, workingPath, ciphertextPath string, obj libencrypt.ObjectContext) error
	}
}

// The encrypted-byte adapter wired into mediahttp must satisfy BOTH the base
// decryptor and the streaming extension the direct-play route probes for at
// run time. A probe that misses is silent: the route falls back to whole-object
// buffering and answers 416 to every Range request, so the compile-time
// assertion is the only thing that keeps encrypted-library direct play working
// in production.
var (
	_ mediahttp.EncryptedByteDecryptor  = appLibEncryptDBCipher{}
	_ mediahttp.EncryptedByteReadSeeker = appLibEncryptDBCipher{}
)

type appOperationDBCipher struct{ cipher libencrypt.OperationCipher }

func (c appOperationDBCipher) UnsealDBFile(_ context.Context, libraryID store.LibraryID, ciphertextPath, workingPath string) error {
	return c.cipher.UnsealDBFile(string(libraryID), ciphertextPath, workingPath, libencrypt.LibraryDBObjectContext(string(libraryID)))
}

func (c appOperationDBCipher) SealDBFile(_ context.Context, libraryID store.LibraryID, workingPath, ciphertextPath string) error {
	return c.cipher.SealDBFile(string(libraryID), workingPath, ciphertextPath, libencrypt.LibraryDBObjectContext(string(libraryID)))
}

// OpenDecryptReader adapts the byte routes' plain-string object identity onto
// libencrypt's ObjectContext and hands back the seekable decrypting reader
// direct play needs, so a Range request decrypts only the chunks it asked for
// instead of buffering the whole object. An unknown class or empty id fails
// closed inside libencrypt's context validation before any plaintext is
// produced. libencrypt holds the shared ownership lease for the life of the
// reader; mediahttp owns closing it.
func (c appLibEncryptDBCipher) OpenDecryptReader(libraryID store.LibraryID, ciphertextPath, objectClass, immutableObjectID string) (io.ReadSeekCloser, error) {
	return c.manager.OpenDecryptReader(string(libraryID), ciphertextPath, libencrypt.ObjectContext{
		LibraryID:         string(libraryID),
		ObjectClass:       libencrypt.ObjectClass(objectClass),
		ImmutableObjectID: immutableObjectID,
	})
}

// DecryptBytes is the whole-object form used by the small server-derived
// members (artwork, generated assets). It carries the same fail-closed object
// identity as OpenDecryptReader because it is built on it.
func (c appLibEncryptDBCipher) DecryptBytes(libraryID store.LibraryID, ciphertextPath, objectClass, immutableObjectID string) ([]byte, error) {
	reader, err := c.OpenDecryptReader(libraryID, ciphertextPath, objectClass, immutableObjectID)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

// EncryptFile is the legacy context-free BackupIngestEncryptor method. Every
// real write must carry object identity, so this fails closed; the ingest path
// prefers EncryptObjectFile (store.BackupIngestObjectEncryptor).
func (c appLibEncryptDBCipher) EncryptFile(libraryID, plaintextPath, ciphertextPath string) error {
	return errors.New("encrypted-library writes require an object context")
}

// EncryptObjectFile encrypts one uploaded media byte object, bound to its
// persisted content fingerprint (never its path).
func (c appLibEncryptDBCipher) EncryptObjectFile(libraryID, plaintextPath, ciphertextPath, contentFingerprint string) error {
	return c.manager.EncryptFile(libraryID, plaintextPath, ciphertextPath, libencrypt.MediaObjectContext(libraryID, contentFingerprint))
}

func (c appLibEncryptDBCipher) UnsealDBFile(ctx context.Context, libraryID store.LibraryID, ciphertextPath, workingPath string) error {
	return c.manager.UnsealDBFile(string(libraryID), ciphertextPath, workingPath, libencrypt.LibraryDBObjectContext(string(libraryID)))
}

func (c appLibEncryptDBCipher) SealDBFile(ctx context.Context, libraryID store.LibraryID, workingPath, ciphertextPath string) error {
	return c.manager.SealDBFile(string(libraryID), workingPath, ciphertextPath, libencrypt.LibraryDBObjectContext(string(libraryID)))
}

func observePersistedRoots(ctx context.Context, st *store.Store, watcherService *watcher.Service, logger *slog.Logger) {
	libraries, err := st.Libraries(ctx, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		logger.Warn("library root observe failed", "error", err)
		return
	}
	for _, library := range libraries {
		roots, err := st.LibraryRoots(ctx, library.ID)
		if err != nil {
			logger.Warn("library roots unavailable", "library", string(library.ID), "error", err)
			continue
		}
		for _, root := range roots {
			_ = watcherService.Observe(library.ID, root)
		}
	}
}

func envEnabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

type libraryMutationObserver struct {
	store    *store.Store
	watcher  *watcher.Service
	schedule *schedulepkg.Service
	logger   *slog.Logger
}

func (o libraryMutationObserver) LibraryCreated(ctx context.Context, libraryID store.LibraryID, roots []string) {
	if o.schedule != nil {
		o.schedule.Register(scanScheduleSpec(o.store, libraryID))
		if err := o.schedule.Reload(ctx); err != nil {
			o.logger.Warn("schedule reload after library create failed", "library", string(libraryID), "error", err)
		}
	}
	o.LibraryRootsChanged(ctx, libraryID, roots)
}

func (o libraryMutationObserver) LibraryRootsChanged(ctx context.Context, libraryID store.LibraryID, roots []string) {
	if o.watcher == nil {
		return
	}
	for _, root := range roots {
		_ = o.watcher.Observe(libraryID, root)
	}
}

// moderationContactFromEnv builds the published Guideline 1.2 moderation/contact
// path from the environment, falling back to a generic local-operator note when
// unset. Local config only (invariant 5: no telemetry, no outbound) — these are
// the operator's own moderation contacts surfaced to viewers, never a service
// name (invariant 1: generic framing).
func moderationContactFromEnv() moderation.ContactInfo {
	c := moderation.ContactInfo{
		Email: os.Getenv("BEAMFALL_MODERATION_CONTACT_EMAIL"),
		URL:   os.Getenv("BEAMFALL_MODERATION_CONTACT_URL"),
		Note:  os.Getenv("BEAMFALL_MODERATION_CONTACT_NOTE"),
	}
	if c.Email == "" && c.URL == "" && c.Note == "" {
		c.Note = "Contact this Beamfall server's operator to report content or a moderation concern."
	}
	return c
}
