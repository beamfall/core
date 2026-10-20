// Package graph exposes the generic API contract used by Beamfall clients.
package graph

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/conductor"
	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/integrationhost"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/observability"
	"github.com/beamfall/core/internal/offlinepack"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scan"
	schedulepkg "github.com/beamfall/core/internal/schedule"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

// ServerInfo describes the connected Beamfall server.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// serverVersionString is the running server version reported by ServerInfo and
// used as the comparison baseline for the opt-in version check.
// It is the placeholder release identifier until a release pipeline injects a
// real version; "development" is deliberately non-numeric so the version check
// never claims an update is available against an unversioned dev build.
const serverVersionString = "development"

// SessionInfo describes the current server-side session scope. The track
// preference fields mirror the session Profile so clients can pre-select
// audio/subtitle tracks deterministically (session-scoped client overrides
// stay client-side).
type SessionInfo struct {
	ID                string   `json:"id"`
	DeviceRole        string   `json:"deviceRole"`
	ProfileID         string   `json:"profileID"`
	Profile           string   `json:"profile"`
	Revealed          bool     `json:"revealed"`
	ExpiresAt         string   `json:"expiresAt,omitempty"`
	AudioLanguages    []string `json:"audioLanguages,omitempty"`
	SubtitleLanguages []string `json:"subtitleLanguages,omitempty"`
	SubtitleMode      string   `json:"subtitleMode,omitempty"`
	PreferSDH         bool     `json:"preferSDH,omitempty"`
}

// SourceItem is one rendered item returned by a Section Source.
type SourceItem struct {
	ID           string            `json:"id"`
	Kind         string            `json:"kind"`
	Title        string            `json:"title"`
	Summary      string            `json:"summary,omitempty"`
	ArtworkSlots map[string]string `json:"artworkSlots,omitempty"`
	// Artwork is present only when the item really has stored artwork —
	// clients render the fallback frame on absence instead of probing
	// by-convention URLs for 404s.
	Artwork *SourceItemArtwork `json:"artwork,omitempty"`
	// Restricted marks that the item's owning Library is restricted (ADR-0004
	// §4 — Library-level granularity, NOT a per-item secret flag). It is
	// emitted ONLY to a Revealed scope; an unrevealed/locked session never
	// receives this key at all (the absence is the privacy enforcement — no
	// count or placement of restricted titles leaks to an unauthorized
	// viewer). With omitempty, even a Revealed response carries the key only on
	// actually-restricted items: ABSENT MEANS false (clients decode missing as
	// not-restricted).
	Restricted bool `json:"restricted,omitempty"`
	// Locked marks that the item is visible but blocked by a certification
	// ceiling whose FilterVisibility is "locked".
	// Clients render a visual lock treatment; the server denies the byte gate.
	// With omitempty, absent means not-locked (most items). NEVER set on
	// restricted-Library items — Restricted and Locked are orthogonal.
	Locked bool `json:"locked,omitempty"`
	// Muted marks that the item is reached by the acting Profile's Per-Profile
	// Mute: it is surfaced on purpose (explicit search, direct
	// navigation) or survives a passive rail via a positive signal (a Favorite-
	// overridden broad-category mute). Clients render a subtle "Muted" badge +
	// inline Unmute control — marked, not hidden, so the state is legible and
	// reversible in place. With omitempty, absent means not-muted (most items).
	// Domain-neutral, named by shape (invariants 1-2); mirrors the Restricted/
	// Locked per-item flag treatment.
	Muted bool `json:"muted,omitempty"`
}

// SourceItemArtwork describes an item's stored artwork. Poster is the
// canonical serve path; clients append the compiled-in derivative bucket
// vocabulary (?bucket=w320/w640/w1280, ADR-0014 §2) — buckets are named by
// shape and never enumerated per item.
type SourceItemArtwork struct {
	Poster string `json:"poster"`
}

// JobInfo describes one durable background job without exposing payloads or paths.
type JobInfo struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Class      string          `json:"class"`
	Status     string          `json:"status"`
	Progress   JobProgressInfo `json:"progress"`
	Attempts   int             `json:"attempts"`
	CreatedAt  string          `json:"createdAt,omitempty"`
	UpdatedAt  string          `json:"updatedAt,omitempty"`
	StartedAt  string          `json:"startedAt,omitempty"`
	FinishedAt string          `json:"finishedAt,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// JobProgressInfo describes user-visible job progress.
type JobProgressInfo struct {
	Current int64  `json:"current"`
	Total   int64  `json:"total"`
	Unit    string `json:"unit"`
	Message string `json:"message,omitempty"`
}

// JobEventInfo is one durable job history/log event. Payload is omitted when
// the job targets an unrevealed or unknown Library because future event payloads
// may carry filesystem details.
type JobEventInfo struct {
	ID         string `json:"id"`
	JobID      string `json:"jobID"`
	OccurredAt string `json:"occurredAt"`
	Type       string `json:"type"`
	Payload    string `json:"payload,omitempty"`
}

// OperationsGovernorInfo reports the live resource-governor state behind
// Operations controls: configured generation workers, background CPU budget,
// low-power posture, and whether realtime transcode is preempting generation.
type OperationsGovernorInfo struct {
	Available                     bool `json:"available"`
	GenerationWorkers             int  `json:"generationWorkers"`
	BackgroundCPUBudget           int  `json:"backgroundCPUBudget"`
	ActiveTranscodes              int  `json:"activeTranscodes"`
	ActiveGeneration              int  `json:"activeGeneration"`
	GenerationAllowed             bool `json:"generationAllowed"`
	LowPowerMode                  bool `json:"lowPowerMode"`
	TranscodePreemptingBackground bool `json:"transcodePreemptingBackground"`
}

// ArtworkLocator reports stored-artwork presence and a cache-busting version
// token per slot; *artwork.Service satisfies it (interface at the consumer —
// graph asks whether a slot exists and, when it does, for its version so the
// emitted URL is content-addressed and caches hard, ADR-0014 §2). ok=false
// means the slot has no stored artwork (omit the URL — reveal-safe).
type ArtworkLocator interface {
	ArtworkVersion(id store.MediaID, slot string) (string, bool)
	EntityArtworkVersion(id store.EntityID, slot string) (string, bool)
}

type ArtworkCandidateFetcher interface {
	FetchResource(ctx context.Context, rawURL string) ([]byte, error)
}

type ArtworkCandidateImporter interface {
	ImportArtwork(ctx context.Context, id store.MediaID, slot string, data []byte) error
}

// ScanObserver receives the Library/root pair after a scan enqueue succeeds.
type ScanObserver interface {
	Observe(store.LibraryID, string)
}

// WatchedObserver receives the unwatched→watched transition for a Profile's
// Media. The Scrobble Integration Plugin is the consumer: it
// maps the transition to the Profile's linked, eligible scrobble targets and
// pushes a submission. This is an IN-PROCESS callback seam, NOT a new Core SSE
// event kind — ADR-0040 §2.5 holds (the scrobble plugin "adds no event kinds").
// Nil (the default) means no observer, so a build with the plugin off does
// nothing here. The callback fires only on the TRANSITION (not on every report
// of an already-watched item) so a service is never double-scrobbled by repeated
// position reports past the watched threshold.
type WatchedObserver interface {
	Watched(WatchedTransition)
}

// WatchedTransition is the payload of the unwatched→watched callback. It carries
// the Profile, the watched Media's generic facts (id, Library, class, title,
// year), its External IDs (the scrobble identity handles, resolved by the
// resolver from the store), and the watched instant. It names no domain concept
// — the service-specific mapping lives entirely in the plugin's adapters
// (invariant #1).
type WatchedTransition struct {
	ProfileID   store.ProfileID
	MediaID     store.MediaID
	LibraryID   store.LibraryID
	Class       store.MediaClass
	Title       string
	Year        int
	ExternalIDs []store.MediaExternalID
	// Audio carries optional richer outbound metadata (ADR-0098 area 7) the
	// scrobble plugin attaches to audio submissions: contributor/release display
	// names plus MusicBrainz ids. It is resolved by the OutboundMetadataResolver
	// seam (nil → empty) and is named by SHAPE, not domain — a video transition
	// leaves it zero and the plugin's adapters degrade when it is absent.
	Audio     OutboundAudioMetadata
	WatchedAt time.Time
}

// OutboundAudioMetadata is the generic richer-metadata carrier the watched
// transition hands to the scrobble plugin (ADR-0098 area 7). Core names no
// domain concept here: it is the resolver's job (composition root, driven by
// the activated Music plugin's declared relations/fields) to fill these from
// generic Entity/relation/external-ID reads. All fields are optional.
type OutboundAudioMetadata struct {
	ContributorName string
	ReleaseName     string
	ContributorMBID string
	ReleaseMBID     string
}

// OutboundMetadataResolver resolves the richer outbound audio metadata for a
// watched Media (ADR-0098 area 7). It is an interface-at-consumer seam:
// the composition root supplies it using the activated Music
// plugin's declared relation/field/scheme selectors, so Core hardcodes no
// Music slugs (ADR-0098: "the matcher receives plugin-owned relation
// selectors"). Resolution is best-effort — any miss returns the zero value and
// the submission degrades, never failing the playback report. Nil resolver (the
// default) means no enrichment, preserving the pre-MUSIC-7 behavior.
type OutboundMetadataResolver interface {
	ResolveOutboundAudioMetadata(ctx context.Context, mediaID store.MediaID) OutboundAudioMetadata
}

type LibraryMutationObserver interface {
	LibraryCreated(context.Context, store.LibraryID, []string)
	LibraryRootsChanged(context.Context, store.LibraryID, []string)
}

type ScheduleUpdater interface {
	Update(context.Context, store.ScheduleID, *store.ScheduleTrigger, *bool) (store.Schedule, error)
}

type JobRunner interface {
	RunOne(context.Context, []store.JobClass, time.Time) (store.Job, error)
}

type GovernorStatusProvider interface {
	GenerationWorkers() int
	BackgroundCPUBudget() int
	ActiveTranscodes() int
	ActiveGeneration() int
	GenerationAllowed() bool
}

// Options carries optional production wiring for NewHandler. Nil interface
// fields preserve the same "capability unavailable" behavior as the zero-value
// Handler wiring used by tests.
type Options struct {
	AuditRecorder                observability.AuditRecorder
	ObservabilityController      ObservabilityController
	SuppressedPlugins            []plugin.ID
	HardwareAcceleration         config.HardwareAccelReport
	Identify                     *identify.Service
	Artwork                      ArtworkLocator
	StreamTokens                 *streamtoken.Service
	ArtworkCandidateFetcher      ArtworkCandidateFetcher
	ArtworkCandidateImporter     ArtworkCandidateImporter
	ScanObserver                 ScanObserver
	LibraryMutationObserver      LibraryMutationObserver
	WatchedObserver              WatchedObserver
	OutboundMetadataResolver     OutboundMetadataResolver
	ScrobbleStatusSource         ScrobbleStatusSource
	ScheduleUpdater              ScheduleUpdater
	JobRunner                    JobRunner
	PlaybackCache                PlaybackCacheController
	Governor                     GovernorStatusProvider
	FeedSearcher                 FeedSearcher
	AssetSweep                   func(context.Context)
	SubscriptionSecrets          SubscriptionSecretStore
	ScraperCredentialStore       ScraperCredentialStore
	ScraperCredentialInvalidator ScraperCredentialInvalidator
	PushRegistrar                PushRegistrar
	Conductor                    *conductor.Registry
	QueryEmbedder                QueryEmbedder
	TranscodeTerminator          TranscodeTerminator
	TranscodeAdmission           TranscodeCapacityProbe
	UpdateChecker                UpdateChecker
	ServerVersion                string
	WatcherStatus                WatcherStatusProvider
	QBittorrentIngest            QBittorrentIngestProvider
	QBittorrentIngestConfigurer  QBittorrentIngestConfigurer
	IntegrationLifecycle         IntegrationLifecycle
	IntegrationActionRunner      integrationhost.ActionRunner
	RemoteAccess                 RemoteAccessProvider
	LibEncrypt                   LibEncryptManager
	EncryptedLibraryDBLifecycle  EncryptedLibraryDBLifecycle
	EncryptedGeneratedAssets     EncryptedGeneratedAssetRewrapper
	ExtensionPusher              ExtensionPusher
	AccessRequestPusher          AccessRequestPusher
	OfflinePacks                 OfflinePackService
}

// Handler serves the GraphQL HTTP endpoint.
type Handler struct {
	manifest        *manifest.Service
	auth            *auth.Service
	store           *store.Store
	plugins         *plugin.Registry
	identify        *identify.Service
	artwork         ArtworkLocator
	tokens          *streamtoken.Service
	candidateFetch  ArtworkCandidateFetcher
	candidateImport ArtworkCandidateImporter
	scanObserver    ScanObserver
	libraryObserver LibraryMutationObserver
	hardwareAccel   config.HardwareAccelReport
	// mutationAuditAppend is a test seam for the request-level audit lifecycle.
	// Nil uses store.AppendActivity in production.
	mutationAuditAppend func(context.Context, store.ActivityEntry) error
	// audit records every admin-scoped GraphQL operation, including reads and
	// authorization refusals. It never records arguments, values, or error text.
	audit observability.AuditRecorder
	// watchedObserver receives the unwatched→watched transition for the Scrobble
	// Integration Plugin. Nil (the default) leaves the playback
	// path unchanged — the plugin is off.
	watchedObserver WatchedObserver
	// outboundMetadata resolves richer outbound audio metadata for a watched
	// audio Media (ADR-0098 area 7). Nil (the default) → no enrichment.
	outboundMetadata OutboundMetadataResolver
	// scrobbleStatus exposes the Scrobble Integration Plugin's read-only status
	// (dead-letter/skip feeds + configured adapters) for the operator diagnostic
	// surface (ADR-0098 area 7). Nil (the default, plugin off) → empty reads.
	scrobbleStatus  ScrobbleStatusSource
	scheduleUpdater ScheduleUpdater
	jobRunner       JobRunner
	governor        GovernorStatusProvider
	feedSearcher    FeedSearcher
	// playbackCache wires the precomputed-playback-rendition cache controls:
	// explicit operator generation, on-demand eviction, and the
	// cache-health snapshot the Console surfaces. Nil (the default) reports the
	// cache as unconfigured and refuses the write mutations, honest for a build
	// with no cache root/budget set.
	playbackCache PlaybackCacheController
	// assetSweep re-arms Generated Asset generation after Marker mutations
	// (ADR-0033 §4: thumbnails are generated when a Marker is created or its
	// timestamp edited). Nil (the default) disables.
	assetSweep func(context.Context)
	// remoteAccess reports direct-HTTPS/ACME + relay carrier status to the
	// manager Console (ADR-0012/0018/0025). Nil (the default) reports both
	// paths disabled — honest for a LAN-only server.
	remoteAccess RemoteAccessProvider
	// watcherProvider reports the fsnotify per-Library Watcher state to the
	// manager Console. Nil (the default) reports every Library as
	// unavailable — honest for a build with no Watcher running.
	watcherProvider WatcherStatusProvider
	// qbittorrentIngest reports the qBittorrent ingest config + status feed to
	// the manager Console. Nil (the default) reports ingest disabled.
	qbittorrentIngest QBittorrentIngestProvider
	// qbittorrentConfigurer is the write half of the ingest seam (ADR-0020 §6,
	// FEAT-10): it persists routing + connection, stores the password in the
	// secure store, and reconfigures the running ingestor without a restart. Nil
	// (the default) means the build cannot edit ingest config — the mutation
	// reports it unavailable rather than silently dropping the write.
	qbittorrentConfigurer QBittorrentIngestConfigurer
	// queryEmbedder is the read-side seam of the ADR-0041 Enrichment Plugin: it
	// turns a query string into a vector of one EmbeddingKind so Core can run
	// SemanticSearch. It is the symmetric partner of the write-only
	// EmbeddingProvider — the same plugin that produced the indexed vectors must
	// embed the query in the same space. Nil (the default) means no Enrichment
	// Plugin is active, so semantic search is honestly unavailable and search
	// falls back to keyword.
	queryEmbedder QueryEmbedder
	// suppressedPlugins hides plugin-contributed operator surfaces while leaving
	// the registry loaded for local/self-hosted runtime use.
	suppressedPlugins map[plugin.ID]bool
	// transcodeTerminator aborts a Session's in-flight transcodes when its
	// device key is revoked (ADR-0012 §7 — revocation kills live transports, not
	// just future requests). Satisfied by *playback.Sessions. Nil (the default)
	// means no transcoder is wired, so device revocation still blocks future auth
	// and ends the device's event streams (those self-terminate on the next
	// per-event scope re-evaluation), it just has no in-flight transcode to abort.
	transcodeTerminator TranscodeTerminator
	// transcodeAdmission is the join-time capacity probe:
	// acceptWatchParty consults it to tell a joining member whether a transcode-
	// needing client will be degraded to the lowest rung under current server
	// load. Satisfied by *playback.Sessions. Nil (the default) leaves the join
	// unannotated (Degraded=false) — honest for a build with no transcoder.
	transcodeAdmission TranscodeCapacityProbe
	// updateChecker performs the opt-in, default-off version check (ADR-0013
	// §7). Nil (the default) reports the check unavailable — the updateStatus
	// field then honestly mirrors the stored opt-in config with no outbound
	// request. Satisfied by *updatecheck.Service. No telemetry (invariant #5).
	updateChecker UpdateChecker
	// observability flips ADR-0146's local OTel metrics layer at runtime. Nil
	// means the config persists but the running process has no exporter seam.
	observability ObservabilityController
	// serverVersion is the running server version the version check compares
	// against. Empty defaults to the same value ServerInfo reports.
	serverVersion string
	// subscriptionSecrets is the write-side seam for a private feed's
	// Authorization header: the composition root
	// wires in the 0600 secretFiles store. addSubscription writes the secret
	// here keyed by a per-Subscription ref and persists only the ref in the DB;
	// removeSubscription clears it. Nil (the default) means a build with no
	// secure store wired cannot accept a private-feed credential — the mutation
	// rejects an inbound header rather than persisting it plaintext.
	subscriptionSecrets SubscriptionSecretStore
	// scraperCredentials is the read/write seam for plugin-declared scraper
	// AuthRefs (ADR-0003/0061, invariant #5). It is separate from
	// subscriptionSecrets so callers that only need private-feed writes do not
	// inherit read access.
	scraperCredentials ScraperCredentialStore
	// integrationLifecycle performs ADR-0058 server-owned Integration Worker
	// lifecycle actions (the re-approval-on-permission-expansion rule lives here).
	// The composition root wires it; nil means lifecycle actions that need the
	// controller (apply-update) are unavailable on this server.
	integrationLifecycle IntegrationLifecycle
	// integrationActionRunner dispatches server-declared Plugin Action
	// Descriptors to the worker transport after graph validates the descriptor
	// and its required grants. Nil means run-action is unavailable.
	integrationActionRunner integrationhost.ActionRunner
	// scraperCredentialInvalidator clears runtime auth caches after an AuthRef
	// rotates or is disabled. The scrape engine caches login-bearer tokens by
	// endpoint+AuthRef; mutation success must make old tokens stop being reused.
	scraperCredentialInvalidator ScraperCredentialInvalidator
	// libEncrypt is the per-Encrypted-Library key model seam (ADR-0045 §2.4,
	// ENC-2): the composition root wires the libencrypt.Manager (over the 0600
	// secretFiles secure store) in here. It provisions the DEK + three wraps,
	// seals/unseals, and surfaces the user-held Recovery Key exactly once. Nil
	// (the default) means key management is unavailable on this build — the
	// provisioning/unseal mutations report it disabled rather than touching keys.
	libEncrypt LibEncryptManager
	// encryptedLibraryDBLifecycle is the ENC-DB-4 store-owned metadata DB
	// lifecycle. When wired, graph seal/unseal status is the combined key+DB
	// lifecycle rather than key-only.
	encryptedLibraryDBLifecycle EncryptedLibraryDBLifecycle
	// encryptedGeneratedAssets rewrites existing fingerprint-keyed Generated
	// Asset ciphertext during encrypted-Library DEK rewrap.
	encryptedGeneratedAssets EncryptedGeneratedAssetRewrapper
	// pushRegistrar persists a viewer's device push token at enrollment (ADR-0039
	// §2.5, PUSH-1). The composition root wires in the persisting push.Registrar.
	// Nil (the default) means push-token registration is unavailable on this
	// build — registerDevicePushToken reports it disabled and the client falls
	// back to the polled inbox. The seam takes a token + platform only; the
	// resolver supplies the calling session's own Profile/Device, so a session
	// can never register a token for another device.
	pushRegistrar PushRegistrar
	// extensionPusher fans a parental extension request out to every adult
	// Profile's devices. Nil (the default) or a dormant
	// dispatcher makes the push a no-op — the request, the adults' inbox
	// notifications, and the managed Profile's SSE outcome are unaffected; only
	// the real-time push accelerator is skipped (clients fall back to the polled
	// inbox). The payload carries only {type, eventId} (invariant #5).
	extensionPusher ExtensionPusher
	// accessRequestPusher fans an access request out to every adult Profile's
	// enrolled devices (ADR-0113 §2.2 + ADR-0039 push reuse). Nil (the default)
	// or a dormant dispatcher makes the push a no-op.
	accessRequestPusher AccessRequestPusher
	// conductor is the in-memory Ambient Group Conductor: the
	// ephemeral registry of beat-accurate multi-room audio sync groups. Nil (the
	// default) means the build has no Conductor wired, so the group resolvers
	// report it unavailable. Group state is NEVER persisted (no migration) — it is
	// reassembled on reconnect like the Ambient Queue. Sync signals it produces
	// ride the existing /api/events SSE outbox via store.AppendGroupSyncEvent.
	conductor *conductor.Registry
	// offlinePacks is the generic Offline Pack substrate (ADR-0098 area 6): the
	// composition root wires *offlinepack.Service. Nil (the default) means the
	// build has no pack substrate, so the pack mutations/queries report it
	// unavailable. The Music target resolvers are registered into the service at
	// composition time, so graph stays domain-agnostic — it only builds, lists,
	// revokes, and mints grants for opaque (kind, ref) targets.
	offlinePacks OfflinePackService
}

// OfflinePackService is the consumer-side seam for the Offline Pack substrate
// (ADR-0098 area 6), satisfied by *offlinepack.Service. Targets are opaque
// (kind, ref) pairs the registered resolvers interpret; graph never names a
// music kind, keeping the wire surface domain-agnostic.
type OfflinePackService interface {
	BuildPack(ctx context.Context, scope auth.Scope, req offlinepack.BuildPackRequest) (store.OfflinePack, error)
	Revoke(ctx context.Context, id store.OfflinePackID, profileID store.ProfileID) (store.OfflinePack, error)
	Pack(ctx context.Context, id store.OfflinePackID, profileID store.ProfileID) (store.OfflinePack, error)
	Packs(ctx context.Context, profileID store.ProfileID) ([]store.OfflinePack, error)
	SyncProgress(ctx context.Context, id store.OfflinePackID, profileID store.ProfileID, bytesReady, bytesTotal int64) (store.OfflinePack, error)
	IssuePackGrant(ctx context.Context, scope auth.Scope, id store.OfflinePackID) (string, error)
}

// SetOfflinePacks wires the Offline Pack substrate (ADR-0098 area 6).
func (h *Handler) SetOfflinePacks(svc OfflinePackService) {
	h.offlinePacks = svc
}

// PushRegistrar is the device-push-token registration seam (ADR-0039 §2.5 /
// PUSH-1; interface at the consumer, ADR-0016). The composition root wires in
// the persisting push.Registrar. Register returns push.ErrPushDisabled when
// registration is gated off so the resolver can report "push unavailable"
// honestly. The push token is an opaque routing handle, not an invariant-#5
// secret; the credentials that sign dispatch live in the secure store, never
// here.
type PushRegistrar interface {
	Register(ctx context.Context, deviceID, profileID, platform, token string) error
	Enabled() bool
}

// SubscriptionSecretStore is the secure-store seam for private-feed credentials
// (invariant #5): the composition root wires in the same 0600 secretFiles store
// the scraper AuthRefs and qBittorrent password use. The graph layer writes the
// literal Authorization header here keyed by a per-Subscription ref and persists
// only the ref to the catalog DB; the credential never touches SQLite or a log.
type SubscriptionSecretStore interface {
	// SetSecret stores value under ref, or clears the stored secret when value
	// is empty (a feed switched back to anonymous).
	SetSecret(ref, value string) error
}

// ScraperCredentialStore is the secure-store seam for scraper AuthRefs
// (invariant #5). Secret values live in the composition root's secure store,
// never in GraphQL, SQLite, logs, or activity rows.
type ScraperCredentialStore interface {
	Secret(ref string) (string, error)
	SetSecret(ref, value string) error
}

// ScraperCredentialInvalidator clears runtime auth state derived from an AuthRef.
type ScraperCredentialInvalidator interface {
	InvalidateCredential(ref string)
}

// UpdateChecker performs the opt-in version check: given the
// opt-in flag, channel, and running version, it returns the comparison result.
// Defined at the consumer (ADR-0016 §seams); *updatecheck.Service satisfies it.
// When disabled it makes NO outbound request (default-off; invariant #5).
type UpdateChecker interface {
	Check(ctx context.Context, enabled bool, channel, currentVersion string) (UpdateStatusInfo, error)
}

// TranscodeTerminator aborts every in-flight transcode owned by one auth
// Session. Defined at the consumer (ADR-0016 §seams); the
// playback Sessions manager satisfies it. Returns how many transcodes it
// stopped so the revocation mutation can report propagation honestly.
type TranscodeTerminator interface {
	TerminateSession(sessionID store.SessionID) int
}

// TranscodeCapacityProbe reports whether a normal-priority transcode for
// (session, media) could start right now. Defined at the consumer
// (the playback Sessions manager satisfies it via CanAdmitSession). The Watch-
// Party accept path consults it to annotate a join with whether a transcode-
// needing client will be degraded to the lowest rung under current load.
// Advisory only — the authoritative decision is the playbackInfo +
// byte path. Nil leaves the join unannotated (Degraded=false).
type TranscodeCapacityProbe interface {
	CanAdmitSession(sessionID store.SessionID, mediaID store.MediaID) bool
}

// QueryEmbedder turns a search query into an embedding vector for one kind
// (ADR-0041 semantic-search read seam). The active Enrichment Plugin satisfies
// it; Kind reports the embedding kind it indexes so Core scopes SemanticSearch
// to the matching vectors. Available reports whether the embedder can serve a
// query right now (model loaded). Defined at the consumer (ADR-0016 §seams).
type QueryEmbedder interface {
	Kind() store.EmbeddingKind
	Available() bool
	EmbedQuery(ctx context.Context, query string) ([]float32, error)
}

// NewHandler creates a GraphQL handler. plugins may be nil (no overrides).
func NewHandler(manifests *manifest.Service, authService *auth.Service, st *store.Store, plugins *plugin.Registry, options ...Options) *Handler {
	h := &Handler{
		manifest: manifests,
		auth:     authService,
		store:    st,
		plugins:  plugins,
		audit:    observability.NewAuditLogger(nil),
	}
	if len(options) == 0 {
		return h
	}
	o := options[0]
	if o.AuditRecorder != nil {
		h.audit = o.AuditRecorder
	}
	h.observability = o.ObservabilityController
	if len(o.SuppressedPlugins) > 0 {
		h.suppressedPlugins = make(map[plugin.ID]bool, len(o.SuppressedPlugins))
		for _, id := range o.SuppressedPlugins {
			if id != "" {
				h.suppressedPlugins[id] = true
			}
		}
	}
	h.hardwareAccel = o.HardwareAcceleration
	h.identify = o.Identify
	h.artwork = o.Artwork
	h.tokens = o.StreamTokens
	h.candidateFetch = o.ArtworkCandidateFetcher
	h.candidateImport = o.ArtworkCandidateImporter
	h.scanObserver = o.ScanObserver
	h.libraryObserver = o.LibraryMutationObserver
	h.watchedObserver = o.WatchedObserver
	h.outboundMetadata = o.OutboundMetadataResolver
	h.scrobbleStatus = o.ScrobbleStatusSource
	h.scheduleUpdater = o.ScheduleUpdater
	h.jobRunner = o.JobRunner
	h.playbackCache = o.PlaybackCache
	h.governor = o.Governor
	h.feedSearcher = o.FeedSearcher
	h.assetSweep = o.AssetSweep
	h.subscriptionSecrets = o.SubscriptionSecrets
	h.scraperCredentials = o.ScraperCredentialStore
	h.scraperCredentialInvalidator = o.ScraperCredentialInvalidator
	h.pushRegistrar = o.PushRegistrar
	h.conductor = o.Conductor
	h.queryEmbedder = o.QueryEmbedder
	h.transcodeTerminator = o.TranscodeTerminator
	h.transcodeAdmission = o.TranscodeAdmission
	h.updateChecker = o.UpdateChecker
	h.serverVersion = o.ServerVersion
	h.watcherProvider = o.WatcherStatus
	h.qbittorrentIngest = o.QBittorrentIngest
	h.qbittorrentConfigurer = o.QBittorrentIngestConfigurer
	h.integrationLifecycle = o.IntegrationLifecycle
	h.integrationActionRunner = o.IntegrationActionRunner
	h.remoteAccess = o.RemoteAccess
	h.libEncrypt = o.LibEncrypt
	h.encryptedLibraryDBLifecycle = o.EncryptedLibraryDBLifecycle
	h.encryptedGeneratedAssets = o.EncryptedGeneratedAssets
	h.extensionPusher = o.ExtensionPusher
	h.accessRequestPusher = o.AccessRequestPusher
	h.offlinePacks = o.OfflinePacks
	return h
}

func (h *Handler) SetAuditRecorder(recorder observability.AuditRecorder) {
	if recorder == nil {
		recorder = observability.NewAuditLogger(nil)
	}
	h.audit = recorder
}

// SetSuppressedPlugins hides plugin-contributed acquisition/configuration
// surfaces. Unknown IDs are harmless; mutation guards report them as unknown.
func (h *Handler) SetSuppressedPlugins(ids []plugin.ID) {
	h.suppressedPlugins = map[plugin.ID]bool{}
	for _, id := range ids {
		if id != "" {
			h.suppressedPlugins[id] = true
		}
	}
}

func (h *Handler) pluginSuppressed(id plugin.ID) bool {
	return h.suppressedPlugins != nil && h.suppressedPlugins[id]
}

func (h *Handler) visiblePluginIDs() []plugin.ID {
	if h.plugins == nil {
		return nil
	}
	ids := []plugin.ID{}
	for _, id := range h.plugins.IDs() {
		if h.pluginSuppressed(id) {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// SetHardwareAcceleration wires the sanitized startup probe report shown by the
// Console transcode config resolver. The zero value is software-only.
func (h *Handler) SetHardwareAcceleration(report config.HardwareAccelReport) {
	h.hardwareAccel = report
}

// SetIdentify wires the manual-identification service. Nil (the default)
// leaves the identify mutations cleanly unavailable.
func (h *Handler) SetIdentify(service *identify.Service) {
	h.identify = service
}

// SetArtwork wires artwork-presence reporting on SourceItems. Nil (the
// default) honestly omits the artwork field rather than guessing.
func (h *Handler) SetArtwork(locator ArtworkLocator) {
	h.artwork = locator
}

func (h *Handler) SetStreamTokens(tokens *streamtoken.Service) {
	h.tokens = tokens
}

func (h *Handler) SetArtworkCandidateServices(fetcher ArtworkCandidateFetcher, importer ArtworkCandidateImporter) {
	h.candidateFetch = fetcher
	h.candidateImport = importer
}

// SetScanObserver wires the watcher accelerator. Nil (the default) leaves
// scan enqueueing as the only trigger source.
func (h *Handler) SetScanObserver(observer ScanObserver) {
	h.scanObserver = observer
}

// SetWatchedObserver wires the Scrobble Integration Plugin's watched-transition
// consumer. Nil (the default) leaves the playback path silent.
func (h *Handler) SetWatchedObserver(observer WatchedObserver) {
	h.watchedObserver = observer
}

// SetOutboundMetadataResolver wires the richer-outbound-audio-metadata resolver
// (ADR-0098 area 7). Nil leaves submissions un-enriched (metadata degrades).
func (h *Handler) SetOutboundMetadataResolver(resolver OutboundMetadataResolver) {
	h.outboundMetadata = resolver
}

// SetScrobbleStatusSource wires the Scrobble Integration Plugin's read-only
// status source (ADR-0098 area 7). Nil leaves the status reads empty.
func (h *Handler) SetScrobbleStatusSource(source ScrobbleStatusSource) {
	h.scrobbleStatus = source
}

func (h *Handler) SetJobRunner(runner JobRunner) {
	h.jobRunner = runner
}

// SetPlaybackCache wires the precomputed-playback-rendition cache controller.
// Nil (the default) leaves the Console cache controls reporting the
// cache unconfigured and refusing the generation/eviction mutations.
func (h *Handler) SetPlaybackCache(controller PlaybackCacheController) {
	h.playbackCache = controller
}

func (h *Handler) SetGovernor(provider GovernorStatusProvider) {
	h.governor = provider
}

type FeedSearchResult struct {
	Title       string `json:"title"`
	FeedURL     string `json:"feedURL"`
	ArtworkURL  string `json:"artworkURL"`
	Description string `json:"description"`
}

type FeedSearcher interface {
	SearchFeeds(context.Context, string) ([]FeedSearchResult, error)
}

type feedSearchFunc func(context.Context, string) ([]FeedSearchResult, error)

func (fn feedSearchFunc) SearchFeeds(ctx context.Context, query string) ([]FeedSearchResult, error) {
	return fn(ctx, query)
}

func (h *Handler) SetFeedSearcher(searcher FeedSearcher) {
	h.feedSearcher = searcher
}

// SetAssetSweep wires the callback run after Marker create/update so Marker
// thumbnails regenerate.
func (h *Handler) SetAssetSweep(fn func(context.Context)) {
	h.assetSweep = fn
}

// SetSubscriptionSecrets wires the secure-store seam for private-feed
// credentials (invariant #5). Nil (the default) means an inbound private-feed
// Authorization header is rejected rather than persisted plaintext.
func (h *Handler) SetSubscriptionSecrets(store SubscriptionSecretStore) {
	h.subscriptionSecrets = store
}

// SetScraperCredentialStore wires the secure-store seam for plugin-declared
// scraper AuthRefs. Nil reports credentials unavailable without leaking secrets.
func (h *Handler) SetScraperCredentialStore(store ScraperCredentialStore) {
	h.scraperCredentials = store
}

// SetScraperCredentialInvalidator wires runtime credential-cache invalidation.
func (h *Handler) SetScraperCredentialInvalidator(invalidator ScraperCredentialInvalidator) {
	h.scraperCredentialInvalidator = invalidator
}

// SetPushRegistrar wires the device-push-token registration seam (ADR-0039 §2.5,
// PUSH-1). Nil (the default) leaves registerDevicePushToken cleanly unavailable —
// the mutation reports push disabled and clients fall back to the polled inbox.
func (h *Handler) SetPushRegistrar(r PushRegistrar) {
	h.pushRegistrar = r
}

// SetConductor wires the in-memory Ambient Group Conductor.
// Nil (the default) leaves the group resolvers cleanly unavailable — formGroup
// and friends report "group conductor unavailable". The Registry is ephemeral;
// nothing it holds is persisted, so wiring it adds no storage and no migration.
func (h *Handler) SetConductor(r *conductor.Registry) {
	h.conductor = r
}

// SetQueryEmbedder wires the ADR-0041 Enrichment Plugin's query-embedding seam.
// Nil (the default) leaves semantic search unavailable — search runs keyword-only
// and the capability endpoint reports the plugin absent.
func (h *Handler) SetQueryEmbedder(embedder QueryEmbedder) {
	h.queryEmbedder = embedder
}

// SetTranscodeTerminator wires the playback session-termination seam so device
// revocation can abort the revoked device's in-flight transcodes.
// Nil (the default) leaves transcode-abort a no-op — revocation still ends the
// device's auth and event streams.
func (h *Handler) SetTranscodeTerminator(t TranscodeTerminator) {
	h.transcodeTerminator = t
}

// SetTranscodeAdmission wires the join-time transcode-capacity probe (ADR-0010
// §6; PANEL-16) so acceptWatchParty can tell a joining member whether a
// transcode-needing client will be degraded to the lowest rung under current
// load. Nil (the default) leaves the join unannotated (Degraded=false).
func (h *Handler) SetTranscodeAdmission(p TranscodeCapacityProbe) {
	h.transcodeAdmission = p
}

// SetUpdateChecker wires the opt-in version-check service and the
// running server version it compares against. Nil checker (the default) leaves
// updateStatus reporting the stored opt-in config with no latest-version probe.
func (h *Handler) SetUpdateChecker(checker UpdateChecker, serverVersion string) {
	h.updateChecker = checker
	h.serverVersion = serverVersion
}

// sourceItem maps one store item onto the wire shape, attaching artwork only
// when a poster really exists. The item is already reveal-filtered by the
// store read, so presence here leaks nothing the response doesn't.
//
// revealed gates the Restricted flag: it is set on the wire ONLY for a Revealed
// scope. An unrevealed/locked caller passes revealed=false and the field stays
// absent (ADR-0004 — no restricted count/placement signal reaches a locked
// session). Locked sessions are never served restricted-Library items anyway,
// so item.Restricted is false for them regardless; gating here is belt-and-
// suspenders against any future caller that forgets the upstream filter.
// item.Locked is propagated unconditionally: the client needs to know whether
// to render the lock treatment, and locked items are always visible to the scope
// that receives them (the byte gate is server-side, not UI-side).
func (h *Handler) sourceItem(item store.SourceItem, revealed bool) SourceItem {
	resp := SourceItem{
		ID:      string(item.ID),
		Kind:    item.Kind,
		Title:   item.Title,
		Summary: item.Summary,
		Locked:  item.Locked,
		Muted:   item.Muted,
	}
	if revealed {
		resp.Restricted = item.Restricted
	}
	if h.artwork == nil {
		return resp
	}
	if item.Kind == "entity" {
		base := "/api/entity/artwork/" + string(item.ID)
		if v, ok := h.artwork.EntityArtworkVersion(store.EntityID(item.ID), artwork.SlotPoster); ok {
			resp.Artwork = &SourceItemArtwork{Poster: base + "/poster?v=" + v}
		}
		resp.ArtworkSlots = h.entityArtworkSlots(store.EntityID(item.ID), base)
	} else {
		base := "/api/media/artwork/" + string(item.ID)
		if v, ok := h.artwork.ArtworkVersion(item.ID, artwork.SlotPoster); ok {
			resp.Artwork = &SourceItemArtwork{Poster: base + "/poster?v=" + v}
		}
		resp.ArtworkSlots = h.mediaArtworkSlots(item.ID, base)
	}
	return resp
}

func (h *Handler) mediaArtworkSlots(id store.MediaID, base string) map[string]string {
	slots := map[string]string{}
	for _, slot := range artwork.KnownSlots {
		if v, ok := h.artwork.ArtworkVersion(id, slot); ok {
			slots[slot] = base + "/" + slot + "?v=" + v
		}
	}
	if len(slots) == 0 {
		return nil
	}
	return slots
}

func (h *Handler) entityArtworkSlots(id store.EntityID, base string) map[string]string {
	slots := map[string]string{}
	for _, slot := range artwork.KnownSlots {
		if v, ok := h.artwork.EntityArtworkVersion(id, slot); ok {
			slots[slot] = base + "/" + slot + "?v=" + v
		}
	}
	if len(slots) == 0 {
		return nil
	}
	return slots
}

// maxGraphQLBody caps the GraphQL request document. A GraphQL query/variables
// payload is small; this ceiling stops an unauthenticated client from forcing
// unbounded allocation in the pre-scope decode below (graph.go decodes the body
// before requiresScope/ScopeFromRequest run), a cheap memory-exhaustion DoS.
const maxGraphQLBody = 256 << 10 // 256 KiB

// requestCarriesScopedKey reports whether the request authenticates with a
// scoped developer API key (ADR-0040 §2.2, Bearer bfk_…). It mirrors the
// credential routing in auth.ScopeFromRequest byte for byte, so any request
// that auth would resolve through the scoped-key path is classified the same
// way here. ServeHTTP uses it to force scope resolution for scoped-key
// callers even when the requested fields need no scope, so the surface-level
// refusal covers every scoped key.
func requestCarriesScopedKey(r *http.Request) bool {
	bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(bearer), store.ScopedKeyPrefix)
}

// ServeHTTP implements the starter GraphQL JSON-over-HTTP contract.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeGraphError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxGraphQLBody)

	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeGraphError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeGraphError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	plan, err := prepareOperation(req)
	if err != nil {
		writeJSON(w, http.StatusOK, response{Errors: []graphError{{Message: err.Error()}}})
		return
	}
	fields := plan.fields()
	privilegedNames := requestedPrivilegedOperationFields(plan)
	var scope auth.Scope
	if requiresScope(fields) || requestCarriesScopedKey(r) {
		scope, err = h.auth.ScopeFromRequest(r.Context(), r)
		if err != nil {
			if auditErr := h.recordPrivilegedAudit(r.Context(), privilegedNames, observability.AuditActor{}, observability.AuditRefused); auditErr != nil {
				slog.Error("privileged refusal audit failed", "error", auditErr)
			}
			writeGraphError(w, http.StatusUnauthorized, "invalid session")
			return
		}
	}
	privilegedActor := privilegedAuditActor(scope)
	// Scoped developer keys belong to the /v1/ surface; the private
	// GraphQL surface refuses them outright, whatever their scope set. Dispatch
	// gates on role — an admin-scoped key resolves manager-class — so without
	// this surface-level refusal a key could invoke manager mutations.
	// Refusing here, before the
	// mutation-audit lifecycle below, keeps the audit trail unambiguous: a
	// refused key never produces an attempt or finished entry. The scope was
	// still resolved above (requestCarriesScopedKey forces it even for fields
	// that need no scope), so the privileged refusal is attributed to the key.
	if scope.IsScopedKey() {
		if auditErr := h.recordPrivilegedAudit(r.Context(), privilegedNames, privilegedActor, observability.AuditRefused); auditErr != nil {
			slog.Error("privileged refusal audit failed", "error", auditErr)
		}
		writeGraphError(w, http.StatusForbidden, "scoped API keys are not accepted here; use the /v1/ developer surface")
		return
	}
	mutationNames := requestedMutationFields(plan)
	privilegedMutationNames := requestedPrivilegedMutationFields(plan)
	operationID := ""
	if len(mutationNames) > 0 {
		if h.store == nil {
			h.recordPrivilegedAuditFailure(r.Context(), privilegedNames, privilegedActor)
			writeJSON(w, http.StatusOK, response{Errors: []graphError{{Message: "mutation audit unavailable"}}})
			return
		}
		operationID, err = newMutationOperationID()
		if err != nil {
			h.recordPrivilegedAuditFailure(r.Context(), privilegedNames, privilegedActor)
			writeJSON(w, http.StatusOK, response{Errors: []graphError{{Message: "mutation audit unavailable"}}})
			return
		}
		if err := h.recordPrivilegedAudit(r.Context(), privilegedMutationNames, privilegedActor, observability.AuditAttempt); err != nil {
			slog.Error("privileged mutation attempt audit failed", "error", err)
			writeJSON(w, http.StatusOK, response{Errors: []graphError{{Message: "privileged audit unavailable"}}})
			return
		}
		if err := h.appendMutationAudit(r.Context(), mutationAuditEntry(scope, operationID+":attempt", store.ActivityMutationAttempt, "graphql:"+strings.Join(mutationNames, ","), operationID)); err != nil {
			h.recordPrivilegedAuditFailure(r.Context(), privilegedNames, privilegedActor)
			writeJSON(w, http.StatusOK, response{Errors: []graphError{{Message: "mutation audit unavailable"}}})
			return
		}
	}
	resp := h.execute(r.Context(), scope, req, plan)
	if auditErr := h.recordPrivilegedResults(r.Context(), plan, privilegedActor, resp); auditErr != nil {
		slog.Error("privileged operation audit failed", "error", auditErr)
		// Reads can still fail closed because no durable state changed. Once a
		// mutation resolver returns, replacing its response would falsely imply
		// that dispatch did not happen and invite a destructive/non-idempotent
		// retry. Preserve that known resolver outcome; the durable request
		// lifecycle below records it under the stable operation ID.
		if operationID == "" && !resp.authorizationRefused {
			resp = response{Errors: []graphError{{Message: "privileged audit unavailable"}}}
		}
	}
	if operationID != "" {
		outcome := "success"
		if resp.authorizationRefused {
			outcome = "refused"
		} else if len(resp.Errors) > 0 {
			outcome = "error"
		}
		entry := mutationAuditEntry(scope, operationID+":finished", store.ActivityMutationFinished, "graphql:"+outcome, operationID)
		if err := h.appendMutationAudit(r.Context(), entry); err != nil {
			slog.Error("mutation finish audit append failed", "operation", operationID, "error", err)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) appendMutationAudit(ctx context.Context, entry store.ActivityEntry) error {
	if h.mutationAuditAppend != nil {
		return h.mutationAuditAppend(ctx, entry)
	}
	return h.store.AppendActivity(ctx, entry)
}

// BeginAuditedCommand opens the transport-independent privileged mutation
// lifecycle. The operator audit and durable actor-attributed attempt must both
// succeed before a caller dispatches side effects.
func (h *Handler) BeginAuditedCommand(ctx context.Context, scope auth.Scope, operation string) (string, error) {
	operationID, err := newMutationOperationID()
	if err != nil {
		return "", err
	}
	actor := privilegedAuditActor(scope)
	if err := h.recordAuditOperation(ctx, operation, actor, observability.AuditAttempt); err != nil {
		return "", err
	}
	entry := mutationAuditEntry(
		scope,
		operationID+":attempt",
		store.ActivityMutationAttempt,
		operation,
		operationID,
	)
	if err := h.appendMutationAudit(ctx, entry); err != nil {
		h.recordAuditOperationFailure(ctx, operation, actor)
		return "", err
	}
	return operationID, nil
}

// FinishAuditedCommand records the final operator and durable mutation outcome
// under the operation ID returned by BeginAuditedCommand. A returned error is
// post-dispatch: callers must preserve the already-known command result rather
// than turning it into a retryable not-executed response.
func (h *Handler) FinishAuditedCommand(
	ctx context.Context,
	scope auth.Scope,
	operation, operationID string,
	outcome observability.AuditOutcome,
) error {
	actor := privilegedAuditActor(scope)
	recorderErr := h.recordAuditOperation(ctx, operation, actor, outcome)
	entry := mutationAuditEntry(
		scope,
		operationID+":finished",
		store.ActivityMutationFinished,
		operation+":"+string(outcome),
		operationID,
	)
	appendErr := h.appendMutationAudit(ctx, entry)
	return errors.Join(recorderErr, appendErr)
}

func requestedMutationFields(plan operationPlan) []string {
	var names []string
	for _, occurrence := range plan.occurrences {
		if mutationFields[occurrence.canonical] {
			names = append(names, occurrence.canonical)
		}
	}
	return names
}

func requestedPrivilegedOperationFields(plan operationPlan) []string {
	var names []string
	for _, occurrence := range plan.occurrences {
		if adminFields[occurrence.canonical] {
			names = append(names, occurrence.canonical)
		}
	}
	return names
}

func requestedPrivilegedMutationFields(plan operationPlan) []string {
	var names []string
	for _, occurrence := range plan.occurrences {
		if adminFields[occurrence.canonical] && mutationFields[occurrence.canonical] {
			names = append(names, occurrence.canonical)
		}
	}
	return names
}

func (h *Handler) recordPrivilegedAudit(ctx context.Context, names []string, actor observability.AuditActor, outcome observability.AuditOutcome) error {
	var errs []error
	for _, name := range names {
		errs = append(errs, h.recordAuditOperation(ctx, "graphql:"+name, actor, outcome))
	}
	return errors.Join(errs...)
}

func (h *Handler) recordAuditOperation(ctx context.Context, operation string, actor observability.AuditActor, outcome observability.AuditOutcome) error {
	recorder := h.audit
	if recorder == nil {
		recorder = observability.NewAuditLogger(nil)
	}
	return recorder.RecordAudit(ctx, observability.AuditRecord{
		Actor:     actor,
		Operation: operation,
		Outcome:   outcome,
	})
}

func (h *Handler) recordAuditOperationFailure(ctx context.Context, operation string, actor observability.AuditActor) {
	if err := h.recordAuditOperation(ctx, operation, actor, observability.AuditError); err != nil {
		slog.Error("privileged operation audit failed", "error", err)
	}
}

func (h *Handler) recordPrivilegedAuditFailure(ctx context.Context, names []string, actor observability.AuditActor) {
	if err := h.recordPrivilegedAudit(ctx, names, actor, observability.AuditError); err != nil {
		slog.Error("privileged operation audit failed", "error", err)
	}
}

func (h *Handler) recordPrivilegedResults(ctx context.Context, plan operationPlan, actor observability.AuditActor, resp response) error {
	var errs []error
	for _, occurrence := range plan.occurrences {
		if !adminFields[occurrence.canonical] {
			continue
		}
		outcome := privilegedOperationOutcome(occurrence.responseKey, resp)
		errs = append(errs, h.recordPrivilegedAudit(ctx, []string{occurrence.canonical}, actor, outcome))
	}
	return errors.Join(errs...)
}

func privilegedOperationOutcome(responseKey string, resp response) observability.AuditOutcome {
	if outcome, ok := resp.privilegedOutcomes[responseKey]; ok {
		return outcome
	}
	if resp.authorizationRefused {
		return observability.AuditRefused
	}
	for _, graphErr := range resp.Errors {
		if len(graphErr.Path) == 0 {
			return observability.AuditError
		}
		if graphErr.Path[0] == responseKey {
			return observability.AuditError
		}
	}
	return observability.AuditSuccess
}

func privilegedAuditActor(scope auth.Scope) observability.AuditActor {
	return observability.AuditActor{
		Authenticated: true,
		SessionID:     string(scope.SessionID),
		DeviceID:      string(scope.DeviceID),
		ProfileID:     string(scope.ProfileID),
		APITokenID:    string(scope.APITokenID),
		APIKeyID:      string(scope.APIKeyID),
	}
}

func newMutationOperationID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "mutation-" + hex.EncodeToString(raw[:]), nil
}

func mutationAuditEntry(scope auth.Scope, id, action, targetType, targetID string) store.ActivityEntry {
	return store.ActivityEntry{
		OperationID: id,
		OccurredAt:  time.Now().UTC(),
		Action:      action,
		SessionID:   scope.SessionID,
		DeviceID:    scope.DeviceID,
		ProfileID:   scope.ProfileID,
		TargetType:  targetType,
		TargetID:    targetID,
		TokenID:     scope.APITokenID,
		APIKeyID:    scope.APIKeyID,
	}
}

func selectedFieldDispatchOrders(fields map[string]bool) fieldDispatchPlan {
	selected := make(map[fieldDispatchOrder]bool)
	for field, requested := range fields {
		if !requested {
			continue
		}
		registered, ok := fieldHandlerRegistry[field]
		if !ok {
			continue
		}
		for _, order := range registered.run {
			selected[order] = true
		}
	}
	orders := make(fieldDispatchPlan, 0, len(selected))
	for order := range selected {
		orders = append(orders, order)
	}
	slices.Sort(orders)
	return orders
}

func (h *Handler) execute(ctx context.Context, scope auth.Scope, req request, plan operationPlan) response {
	out := response{Data: map[string]any{}, privilegedOutcomes: map[string]observability.AuditOutcome{}}
	// Authorize the whole operation before any resolver runs. This keeps a later
	// privileged occurrence from turning an earlier mutation into a partial,
	// unauthorized batch.
	for _, occurrence := range plan.occurrences {
		probe := fieldDispatchContext{scope: scope, fields: map[string]bool{occurrence.canonical: true}}
		for _, order := range selectedFieldDispatchOrders(probe.fields) {
			if resp := authorizeDispatchOrder(&probe, order); len(resp.Errors) > 0 {
				return completePrivilegedOutcomes(resp, plan, out.privilegedOutcomes)
			}
		}
	}
	for _, occurrence := range plan.occurrences {
		occurrenceRequest := req
		occurrenceRequest.Variables = occurrence.input
		dispatch := fieldDispatchContext{
			handler:            h,
			ctx:                ctx,
			scope:              scope,
			req:                occurrenceRequest,
			fields:             map[string]bool{occurrence.canonical: true},
			data:               map[string]any{},
			privilegedOutcomes: out.privilegedOutcomes,
			canonical:          occurrence.canonical,
			responseKey:        occurrence.responseKey,
		}
		for _, order := range selectedFieldDispatchOrders(dispatch.fields) {
			resp := fieldDispatchRuns[order](&dispatch)
			if len(resp.Errors) > 0 {
				// Mutation errors abort with no data. Read errors are accumulated by
				// readField and do not reach this branch.
				return completePrivilegedOutcomes(resp, plan, out.privilegedOutcomes)
			}
		}
		for key, value := range dispatch.data {
			out.Data[key] = value
		}
		out.Errors = append(out.Errors, dispatch.errs...)
	}
	return completePrivilegedOutcomes(out, plan, out.privilegedOutcomes)
}

func completePrivilegedOutcomes(resp response, plan operationPlan, outcomes map[string]observability.AuditOutcome) response {
	fallback := observability.AuditError
	if resp.authorizationRefused {
		fallback = observability.AuditRefused
	}
	for _, occurrence := range plan.occurrences {
		if !adminFields[occurrence.canonical] {
			continue
		}
		if _, ok := outcomes[occurrence.responseKey]; !ok {
			outcomes[occurrence.responseKey] = fallback
		}
	}
	resp.privilegedOutcomes = outcomes
	return resp
}

// clientError marks an error the client caused — a bad or missing argument,
// or a lookup the client named that doesn't exist. Its stable message travels
// on the wire verbatim. Every other handler error is internal: logged
// server-side and surfaced as a generic "<field> unavailable" (graphFail).
type clientError string

func (e clientError) Error() string { return string(e) }

// graphFailMessage resolves the wire-safe message for one failed top-level
// field: client-caused errors (graph clientError, store ValidationError) carry
// their stable message verbatim; anything else is an internal failure — logged
// here, never leaked to the client.
func graphFailMessage(field string, err error) string {
	message := field + " unavailable"
	var ce clientError
	var ve store.ValidationError
	switch {
	case errors.As(err, &ce):
		message = string(ce)
	case errors.As(err, &ve):
		message = string(ve)
	default:
		slog.Error("graph field failed", "field", field, "error", err)
	}
	return message
}

// graphFail resolves the abort-the-batch wire response for one failed top-level
// field. MUTATION fields use this: a half-applied mutation batch is worse than
// none, so the first mutation error discards the whole response (M2). READ
// fields instead accumulate a per-field error via readField and resolve the
// rest of the batch.
func graphFail(field string, err error) response {
	return response{Errors: []graphError{{Message: graphFailMessage(field, err)}}}
}

// readField records one READ field's outcome under M2 partial-success: on
// success it writes data[dataKey]=result; on error it writes data[dataKey]=null
// and appends a {message, path:[field]} entry to errs, leaving the rest of the
// batch to resolve. dataKey is the camelCase wire key (often equal to field;
// "manifest" → "navigationManifest", "clientprefs" → "clientPrefs"). field is
// the wire-safe error attribution. Unlike graphFail it never aborts — a flaky
// or forbidden read no longer blanks an entire batched screen.
func readField[T any](data map[string]any, errs *[]graphError, dataKey, errorField, errorPath string, result T, err error) {
	if err != nil {
		data[dataKey] = nil
		*errs = append(*errs, graphError{Message: graphFailMessage(errorField, err), Path: []string{errorPath}})
		return
	}
	data[dataKey] = result
}

func (h *Handler) sourceItems(ctx context.Context, scope auth.Scope, req request) ([]SourceItem, error) {
	if h.store == nil {
		return []SourceItem{}, nil
	}
	entityKind := trimmedVariable(req, "entityKind")
	libraryID := trimmedVariable(req, "libraryID")
	entityID := trimmedVariable(req, "entityID")
	kind := stringVar(req, "kind")
	queryText := trimmedVariable(req, "query")
	if kind == "entity-media" && entityID == "" {
		return nil, clientError("entityID is required")
	}
	// entityFilter echoes a manifest SourceSpec's entityFilter object back,
	// like fieldFilters. A partial object is a malformed echo and answers
	// with a stable message — silently dropping it would quietly widen a
	// filtered shelf to the whole catalog. A well-formed filter naming an
	// entity that doesn't exist (or is hidden) yields an empty list.
	var entityFilter *store.EntityFilter
	if raw := stringMapVar(req, "entityFilter"); raw != nil {
		filterKind := strings.TrimSpace(raw["kind"])
		filterName := strings.TrimSpace(raw["name"])
		if filterKind == "" || filterName == "" {
			return nil, clientError("entityFilter requires kind and name")
		}
		entityFilter = &store.EntityFilter{Kind: filterKind, Name: filterName}
	}
	if kind == "all-items" && queryText != "" {
		mode, err := parseSearchMode(req)
		if err != nil {
			return nil, err
		}
		if mode == SearchModeSemantic || mode == SearchModeAuto {
			semanticFilter, ok := sourceItemsLibraryFilter(scope.LibraryFilter(), store.LibraryID(libraryID))
			if !ok {
				return []SourceItem{}, nil
			}
			available, err := h.semanticAvailable(ctx, semanticFilter)
			if err != nil {
				return nil, err
			}
			if available {
				items, err := h.semanticItems(ctx, scope.ProfileID, semanticFilter, queryText)
				if err == nil {
					out := make([]SourceItem, len(items))
					for i, item := range items {
						out[i] = item.SourceItem
					}
					return out, nil
				}
				if mode == SearchModeSemantic && !errors.Is(err, errQueryEmbeddingUnavailable) {
					return nil, err
				}
			}
			if mode == SearchModeSemantic {
				return []SourceItem{}, nil
			}
		}
	}
	items, err := h.sourceItemsWithEntityFilters(ctx, scope.LibraryFilter(), req, store.SourceQuery{
		Kind:         kind,
		Limit:        boundedIntVar(req, "limit", 50, 200),
		Offset:       nonNegativeIntVar(req, "offset"),
		ProfileID:    scope.ProfileID,
		Query:        queryText,
		EntityKind:   entityKind,
		EntityID:     store.EntityID(entityID),
		LibraryID:    store.LibraryID(libraryID),
		FieldFilters: anyMapVar(req, "fieldFilters"),
		EntityFilter: entityFilter,
		// seed parameterizes the "random" Source kind; an opaque token, not
		// trimmed (distinct seeds must stay distinct). Empty is a valid seed.
		Seed: stringVar(req, "seed"),
		// value selects a valued predicate Source's band (resume-progress /
		// availability, ADR-0052 §2.6); echoed back from the manifest like the
		// other Source parameters. An unrecognized value yields an empty feed.
		Value: trimmedVariable(req, "value"),
	})
	if err != nil {
		return nil, err
	}
	// SourceItems already suppressed muted items via the ProfileID predicate; the
	// SURVIVORS still include broad-muted-but-favorited cards (§2.6 positive-signal
	// override) that §2.7 requires badged ("a Favorite-overridden card").
	if err := h.store.AnnotateMuted(ctx, scope.ProfileID, scope.LibraryFilter(), items); err != nil {
		return nil, err
	}
	resp := make([]SourceItem, len(items))
	for i, item := range items {
		resp[i] = h.sourceItem(item, scope.Revealed)
	}
	return resp, nil
}

func sourceItemsLibraryFilter(filter store.LibraryFilter, libraryID store.LibraryID) (store.LibraryFilter, bool) {
	if libraryID == "" {
		return filter, true
	}
	if len(filter.AllowedIDs) == 0 && !filter.AllLibraries {
		return filter, false
	}
	if len(filter.AllowedIDs) > 0 && !slices.Contains(filter.AllowedIDs, libraryID) {
		return filter, false
	}
	filter.AllowedIDs = []store.LibraryID{libraryID}
	return filter, true
}

// ambientQueue assembles the Ambient Queue on demand from the
// Collection/Source the caller names, scoped to the session's revealed view.
// The queue is NOT a persisted server object: it is reassembled from the named
// Source every call — at ambient session start and on every reconnect — so two
// calls with the same arguments each run a fresh Source evaluation (a "random"
// Source reshuffles unless the client pins a Seed). The Reveal contract applies
// in full because assembly rides the same reveal-filtered SourceItems funnel
// every other rail uses: restricted Media is never served to an unrevealed
// ambient session. The Navigation Manifest's "Ambient" entry stores the
// last-used Source reference as client configuration; the server
// holds no ambient-queue state of its own.
//
// It deliberately reuses sourceItems: an Ambient Queue is just a Source feed
// consumed by the ambient posture, not a new query vocabulary.
func (h *Handler) ambientQueue(ctx context.Context, scope auth.Scope, req request) ([]SourceItem, error) {
	return h.sourceItems(ctx, scope, req)
}

func (h *Handler) jobs(ctx context.Context, scope auth.Scope) ([]JobInfo, error) {
	if h.store == nil {
		return []JobInfo{}, nil
	}
	jobs, err := h.store.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	resp := make([]JobInfo, len(jobs))
	for i, job := range jobs {
		resp[i] = h.jobInfo(ctx, scope, job)
	}
	return resp, nil
}

func (h *Handler) job(ctx context.Context, scope auth.Scope, req request) (JobInfo, error) {
	if h.store == nil {
		return JobInfo{}, store.ErrJobNotFound
	}
	id := trimmedVariable(req, "id")
	if id == "" {
		return JobInfo{}, clientError(store.ErrJobNotFound.Error())
	}
	job, err := h.store.Job(ctx, store.JobID(id))
	if errors.Is(err, store.ErrJobNotFound) {
		return JobInfo{}, clientError(err.Error())
	}
	if err != nil {
		return JobInfo{}, err
	}
	return h.jobInfo(ctx, scope, job), nil
}

func (h *Handler) jobEvents(ctx context.Context, scope auth.Scope, req request) ([]JobEventInfo, error) {
	if h.store == nil {
		return []JobEventInfo{}, nil
	}
	id := trimmedVariable(req, "id")
	if id == "" {
		return nil, clientError(store.ErrJobNotFound.Error())
	}
	job, err := h.store.Job(ctx, store.JobID(id))
	if errors.Is(err, store.ErrJobNotFound) {
		return nil, clientError(err.Error())
	}
	if err != nil {
		return nil, err
	}
	events, err := h.store.ListJobEvents(ctx, store.JobID(id))
	if err != nil {
		return nil, err
	}
	redact := h.jobNeedsRedaction(ctx, scope, job)
	resp := make([]JobEventInfo, len(events))
	for i, event := range events {
		resp[i] = JobEventInfo{
			ID:         strconv.FormatInt(event.ID, 10),
			JobID:      string(event.JobID),
			OccurredAt: formatOptionalTime(event.OccurredAt),
			Type:       event.Type,
		}
		if !redact {
			resp[i].Payload = string(event.Payload)
		}
	}
	return resp, nil
}

func (h *Handler) jobCancel(ctx context.Context, scope auth.Scope, req request) (JobInfo, error) {
	id := trimmedVariable(req, "id")
	if id == "" {
		return JobInfo{}, clientError(store.ErrJobNotFound.Error())
	}
	if err := h.store.CancelJob(ctx, store.JobID(id), time.Now().UTC()); err != nil {
		if errors.Is(err, store.ErrJobTerminal) || errors.Is(err, store.ErrJobNotFound) {
			return JobInfo{}, clientError(err.Error())
		}
		return JobInfo{}, err
	}
	job, err := h.store.Job(ctx, store.JobID(id))
	if err != nil {
		return JobInfo{}, err
	}
	return h.jobInfo(ctx, scope, job), nil
}

func (h *Handler) jobRetry(ctx context.Context, scope auth.Scope, req request) (JobInfo, error) {
	id := trimmedVariable(req, "id")
	if id == "" {
		return JobInfo{}, clientError(store.ErrJobNotFound.Error())
	}
	job, err := h.store.RetryJob(ctx, store.JobID(id), time.Now().UTC())
	if err != nil {
		if errors.Is(err, store.ErrJobNotTerminal) || errors.Is(err, store.ErrJobNotFound) {
			return JobInfo{}, clientError(err.Error())
		}
		return JobInfo{}, err
	}
	return h.jobInfo(ctx, scope, job), nil
}

func (h *Handler) operationsGovernor(ctx context.Context) (OperationsGovernorInfo, error) {
	if h.governor == nil {
		cfg, err := h.store.ConsoleConfig(ctx)
		if err != nil {
			return OperationsGovernorInfo{}, err
		}
		return OperationsGovernorInfo{
			Available:         false,
			GenerationWorkers: cfg.BackgroundWorkers,
			LowPowerMode:      cfg.BackgroundWorkers <= 1,
		}, nil
	}
	activeTranscodes := h.governor.ActiveTranscodes()
	activeGeneration := h.governor.ActiveGeneration()
	workers := h.governor.GenerationWorkers()
	budget := h.governor.BackgroundCPUBudget()
	return OperationsGovernorInfo{
		Available:                     true,
		GenerationWorkers:             workers,
		BackgroundCPUBudget:           budget,
		ActiveTranscodes:              activeTranscodes,
		ActiveGeneration:              activeGeneration,
		GenerationAllowed:             h.governor.GenerationAllowed(),
		LowPowerMode:                  workers <= 1 && budget <= 1,
		TranscodePreemptingBackground: activeTranscodes > 0 && activeGeneration > 0,
	}, nil
}

const eventsCompactionJobKind = store.JobKind("events-compaction")

func (h *Handler) runJob(ctx context.Context, scope auth.Scope) (JobInfo, error) {
	if h.jobRunner == nil {
		return JobInfo{}, clientError("job runner unavailable")
	}
	job, err := h.jobRunner.RunOne(ctx, []store.JobClass{store.JobClassBackground}, time.Now().UTC())
	if errors.Is(err, store.ErrNoRunnableJob) {
		return JobInfo{}, clientError("no runnable job")
	}
	if err != nil {
		return JobInfo{}, err
	}
	return h.jobInfo(ctx, scope, job), nil
}

func (h *Handler) runEventsCompaction(ctx context.Context, scope auth.Scope) (JobInfo, error) {
	now := time.Now().UTC()
	job, err := h.store.EnqueueJob(ctx, store.Job{
		Kind:      eventsCompactionJobKind,
		Class:     store.JobClassBackground,
		DedupeKey: string(eventsCompactionJobKind),
		Payload:   []byte(`{"source":"console"}`),
		CreatedAt: now,
		RunAfter:  now,
	})
	if err != nil {
		return JobInfo{}, err
	}
	return h.jobInfo(ctx, scope, job), nil
}

// ProfileInfo is the wire shape for a created or updated Profile.
type ProfileInfo struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	LibraryIDs []string `json:"libraryIDs"`
	// MaxConcurrentStreams is null when the Profile has no stream cap.
	MaxConcurrentStreams *int     `json:"maxConcurrentStreams"`
	TranscodeAllowed     bool     `json:"transcodeAllowed"`
	Disabled             bool     `json:"disabled"`
	AudioLanguages       []string `json:"audioLanguages"`
	SubtitleLanguages    []string `json:"subtitleLanguages"`
	SubtitleMode         string   `json:"subtitleMode"`
	PreferSDH            bool     `json:"preferSDH"`
	// CertificationCeilings are the Profile's per-plugin certification
	// ceilings.
	CertificationCeilings []CertificationCeilingInfo `json:"certificationCeilings"`
}

// CertificationCeilingInfo is the wire shape for one Profile certification
// ceiling.
type CertificationCeilingInfo struct {
	PluginID     string `json:"pluginID"`
	Ceiling      string `json:"ceiling"`
	AllowUnrated bool   `json:"allowUnrated"`
}

func profileInfo(profile store.Profile) ProfileInfo {
	audioLanguages := profile.AudioLanguages
	if audioLanguages == nil {
		audioLanguages = []string{}
	}
	subtitleLanguages := profile.SubtitleLanguages
	if subtitleLanguages == nil {
		subtitleLanguages = []string{}
	}
	ceilings := make([]CertificationCeilingInfo, 0, len(profile.CertificationCeilings))
	for _, ceiling := range profile.CertificationCeilings {
		ceilings = append(ceilings, CertificationCeilingInfo{
			PluginID:     ceiling.PluginID,
			Ceiling:      ceiling.Ceiling,
			AllowUnrated: ceiling.AllowUnrated,
		})
	}
	return ProfileInfo{
		ID:                    string(profile.ID),
		Name:                  profile.Name,
		Kind:                  string(profile.Kind),
		LibraryIDs:            libraryIDs(profile.Library),
		MaxConcurrentStreams:  profile.MaxConcurrentStreams,
		TranscodeAllowed:      profile.TranscodeAllowed,
		Disabled:              profile.Disabled,
		AudioLanguages:        audioLanguages,
		SubtitleLanguages:     subtitleLanguages,
		SubtitleMode:          string(profile.SubtitleMode),
		PreferSDH:             profile.PreferSDH,
		CertificationCeilings: ceilings,
	}
}

func libraryIDs(ids []store.LibraryID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	return out
}

// ProfileSummary is the wire shape for one profile-picker entry:
// just enough to choose — policy detail stays on the manager shapes. The
// per-plugin certification ceilings ride along so the Console's Enforced
// Filters picker can render and edit them without a second round trip
// (ADR-0028 §5; matches setCertificationCeiling's return shape).
type ProfileSummary struct {
	ID                    string                     `json:"id"`
	Name                  string                     `json:"name"`
	Kind                  string                     `json:"kind"`
	CertificationCeilings []CertificationCeilingInfo `json:"certificationCeilings"`
}

// profiles lists the Profiles a picker may offer to any session: Profiles
// are not accounts, so the list is not role-gated, and disabled
// Profiles are hidden — they refuse new sessions and profile switches alike.
func (h *Handler) profiles(ctx context.Context) ([]ProfileSummary, error) {
	all, err := h.store.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	summaries := make([]ProfileSummary, 0, len(all))
	for _, profile := range all {
		if profile.Disabled {
			continue
		}
		ceilings := make([]CertificationCeilingInfo, 0, len(profile.CertificationCeilings))
		for _, ceiling := range profile.CertificationCeilings {
			ceilings = append(ceilings, CertificationCeilingInfo{
				PluginID:     ceiling.PluginID,
				Ceiling:      ceiling.Ceiling,
				AllowUnrated: ceiling.AllowUnrated,
			})
		}
		summaries = append(summaries, ProfileSummary{
			ID:                    string(profile.ID),
			Name:                  profile.Name,
			Kind:                  string(profile.Kind),
			CertificationCeilings: ceilings,
		})
	}
	return summaries, nil
}

// switchProfile rebinds the calling session to another Profile. The rebind
// always relocks server-side (ADR-0004 — Reveal never survives a profile
// switch), and the returned session reflects the new binding so clients can
// re-render without a second round trip.
func (h *Handler) switchProfile(ctx context.Context, scope auth.Scope, req request) (SessionInfo, error) {
	profileID := trimmedVariable(req, "profileID")
	if profileID == "" {
		return SessionInfo{}, clientError("profileID is required")
	}
	targetID := store.ProfileID(profileID)
	profile, err := h.store.Profile(ctx, targetID)
	if err != nil {
		return SessionInfo{}, err
	}
	adult := profile.Kind == store.ProfileAdult
	attemptKey := profileID + ":" + string(scope.DeviceID) + ":" + string(scope.SessionID)
	now := time.Now().UTC()
	if adult {
		until, err := h.store.AuthBackoffUntil(ctx, profileEntryAttemptAction, attemptKey, now, profileEntryBackoff)
		if err != nil {
			return SessionInfo{}, err
		}
		if now.Before(until) {
			return SessionInfo{}, clientError("profile PIN attempts throttled")
		}
	}
	switchErr := h.store.SetSessionProfileWithEntryPIN(ctx, scope.SessionID, targetID, trimmedVariable(req, "pin"))
	if adult {
		recordErr := h.store.RecordAuthAttempt(ctx, store.AuthAttempt{
			OccurredAt: now,
			Action:     profileEntryAttemptAction,
			Key:        attemptKey,
			Success:    switchErr == nil,
		})
		if recordErr != nil {
			if switchErr != nil {
				return SessionInfo{}, recordErr
			}
			slog.Error("profile entry success attempt append failed", "profile", profileID, "error", recordErr)
		}
	}
	if switchErr != nil {
		if errors.Is(switchErr, store.ErrNoProfileEntryPIN) || errors.Is(switchErr, store.ErrProfileEntryPINMismatch) {
			return SessionInfo{}, clientError("profile PIN required")
		}
		if errors.Is(switchErr, store.ErrProfileDisabled) {
			return SessionInfo{}, clientError(switchErr.Error())
		}
		return SessionInfo{}, switchErr
	}
	if err := h.logActivity(ctx, scope, store.ActivityProfileSwitch, "profile", profileID, ""); err != nil {
		return SessionInfo{}, err
	}
	expiresAt := ""
	if !scope.ExpiresAt.IsZero() {
		expiresAt = scope.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return SessionInfo{
		ID:         string(scope.SessionID),
		DeviceRole: string(scope.DeviceRole),
		ProfileID:  string(profile.ID),
		Profile:    string(profile.Kind),
		Revealed:   false,
		ExpiresAt:  expiresAt,
	}, nil
}

// createProfile inserts a Profile granted Libraries from the creating scope:
// managed Profiles never receive restricted Libraries (they have no reveal
// path), and an unrevealed creator cannot grant what it cannot see. Optional
// policy fields (stream cap, transcode gate, track preferences) apply in the
// same request.
func (h *Handler) createProfile(ctx context.Context, scope auth.Scope, req request) (ProfileInfo, error) {
	name := stringVar(req, "name")
	kind := stringVar(req, "kind")
	settings, hasSettings, err := profileSettingsFromVariables(req)
	if err != nil {
		return ProfileInfo{}, err
	}
	profile, err := h.store.CreateProfile(ctx, name, store.ProfileKind(kind), scope.LibraryFilter())
	if err != nil {
		return ProfileInfo{}, err
	}
	if hasSettings {
		if profile, err = h.store.UpdateProfileSettings(ctx, profile.ID, settings); err != nil {
			return ProfileInfo{}, err
		}
	}
	return profileInfo(profile), nil
}

// updateProfile applies a partial Profile policy write (manager-gated in
// execute). Disabling a Profile refuses new sessions and hides it from
// pickers; engagement history stays untouched.
func (h *Handler) updateProfile(ctx context.Context, req request) (ProfileInfo, error) {
	profileID := trimmedVariable(req, "profileID")
	if profileID == "" {
		return ProfileInfo{}, clientError("profileID is required")
	}
	settings, hasSettings, err := profileSettingsFromVariables(req)
	if err != nil {
		return ProfileInfo{}, err
	}
	v := newVars(req)
	entryPIN, hasEntryPIN := v.String("entryPIN")
	if err := v.Err(); err != nil {
		return ProfileInfo{}, err
	}
	if !hasSettings && !hasEntryPIN {
		return ProfileInfo{}, clientError("no profile settings to update")
	}
	if hasSettings && hasEntryPIN {
		return ProfileInfo{}, clientError("entryPIN must be updated separately from profile settings")
	}
	id := store.ProfileID(profileID)
	if hasEntryPIN {
		if err := h.store.SetProfileEntryPIN(ctx, id, strings.TrimSpace(entryPIN)); err != nil {
			return ProfileInfo{}, err
		}
		profile, err := h.store.Profile(ctx, id)
		if err != nil {
			return ProfileInfo{}, err
		}
		return profileInfo(profile), nil
	}
	var before store.Profile
	if settings.Kind != nil {
		before, err = h.store.Profile(ctx, id)
		if err != nil {
			return ProfileInfo{}, err
		}
	}
	profile, err := h.store.UpdateProfileSettings(ctx, id, settings)
	if err != nil {
		return ProfileInfo{}, err
	}
	if settings.Kind != nil && before.Kind != profile.Kind && profile.Kind == store.ProfileManaged {
		if err := h.emitAccessCeilingInvalidation(ctx, id); err != nil {
			return ProfileInfo{}, err
		}
	}
	return profileInfo(profile), nil
}

func (h *Handler) setProfileLibraries(ctx context.Context, scope auth.Scope, req request) (ProfileInfo, error) {
	profileID := store.ProfileID(trimmedVariable(req, "profileID"))
	if profileID == "" {
		return ProfileInfo{}, clientError("profileID is required")
	}
	v := newVars(req)
	rawIDs, ok := v.StringList("libraryIDs", "library IDs")
	if err := v.Err(); err != nil {
		return ProfileInfo{}, err
	}
	if !ok {
		return ProfileInfo{}, clientError("libraryIDs is required")
	}
	ids := make([]store.LibraryID, len(rawIDs))
	for i, rawID := range rawIDs {
		id := store.LibraryID(strings.TrimSpace(rawID))
		if id == "" {
			return ProfileInfo{}, clientError("library IDs cannot be empty")
		}
		restricted, err := h.store.LibraryRestricted(ctx, id)
		if err != nil {
			return ProfileInfo{}, err
		}
		if restricted && !scope.Revealed {
			return ProfileInfo{}, clientError("reveal required to grant restricted libraries")
		}
		ids[i] = id
	}
	profile, err := h.store.SetProfileLibraries(ctx, profileID, ids)
	if err != nil {
		return ProfileInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityProfileLibrary, "profile", string(profileID), ""); err != nil {
		return ProfileInfo{}, err
	}
	if err := h.emitAccessCeilingInvalidation(ctx, profileID); err != nil {
		return ProfileInfo{}, err
	}
	return profileInfo(profile), nil
}

// setCertificationCeiling pins or clears a Profile's certification ceiling
// on one plugin's ordered scale (ADR-0028 §5; manager-gated in execute).
// An absent or empty ceiling clears the constraint; a set ceiling must be a
// value of the plugin's declared scale. allowUnrated overrides the
// fail-closed unrated-blocked default.
func (h *Handler) setCertificationCeiling(ctx context.Context, req request) (ProfileInfo, error) {
	profileID := trimmedVariable(req, "profileID")
	pluginID := trimmedVariable(req, "pluginID")
	if profileID == "" || pluginID == "" {
		return ProfileInfo{}, clientError("profileID and pluginID are required")
	}
	v := newVars(req)
	ceiling, hasCeiling := v.String("ceiling")
	allowUnrated, _ := v.Bool("allowUnrated")
	if err := v.Err(); err != nil {
		return ProfileInfo{}, err
	}
	if !hasCeiling || strings.TrimSpace(ceiling) == "" {
		profile, err := h.store.ClearProfileCertificationCeiling(ctx, store.ProfileID(profileID), pluginID)
		if err != nil {
			return ProfileInfo{}, err
		}
		// Clearing a ceiling changes what this Profile may see → invalidate its
		// still-connected OS surfaces.
		if err := h.emitAccessCeilingInvalidation(ctx, store.ProfileID(profileID)); err != nil {
			return ProfileInfo{}, err
		}
		return profileInfo(profile), nil
	}
	ceiling = strings.TrimSpace(ceiling)
	if h.plugins == nil {
		return ProfileInfo{}, clientError("unknown plugin")
	}
	bundle, ok := h.plugins.Bundle(plugin.ID(pluginID))
	if !ok {
		return ProfileInfo{}, clientError("unknown plugin")
	}
	if !slices.Contains(bundle.CertificationScale, ceiling) {
		return ProfileInfo{}, clientError(fmt.Sprintf("ceiling %q is not on the plugin's certification scale", ceiling))
	}
	profile, err := h.store.SetProfileCertificationCeiling(ctx, store.ProfileID(profileID), store.ProfileCertificationCeiling{
		PluginID:     pluginID,
		Ceiling:      ceiling,
		AllowUnrated: allowUnrated,
	})
	if err != nil {
		return ProfileInfo{}, err
	}
	// Tightening/raising a ceiling changes what this Profile may see → invalidate
	// its still-connected OS surfaces.
	if err := h.emitAccessCeilingInvalidation(ctx, store.ProfileID(profileID)); err != nil {
		return ProfileInfo{}, err
	}
	return profileInfo(profile), nil
}

// profileSettingsFromVariables reads the optional Profile policy variables.
// Absent (or JSON-null) variables leave the stored value unchanged;
// maxConcurrentStreams 0 clears the stream cap.
func profileSettingsFromVariables(req request) (store.ProfileSettings, bool, error) {
	var settings store.ProfileSettings
	has := false
	v := newVars(req)
	if name, ok := v.String("name"); ok {
		settings.Name = &name
		has = true
	}
	if kindText, ok := v.String("kind"); ok {
		kind := store.ProfileKind(kindText)
		settings.Kind = &kind
		has = true
	}
	if number, ok := v.Number("maxConcurrentStreams"); ok {
		limit := int(number)
		settings.MaxConcurrentStreams = &limit
		has = true
	}
	for name, target := range map[string]**bool{
		"transcodeAllowed": &settings.TranscodeAllowed,
		"disabled":         &settings.Disabled,
		"preferSDH":        &settings.PreferSDH,
	} {
		if flag, ok := v.Bool(name); ok {
			*target = &flag
			has = true
		}
	}
	for name, target := range map[string]**[]string{
		"audioLanguages":    &settings.AudioLanguages,
		"subtitleLanguages": &settings.SubtitleLanguages,
	} {
		if languages, ok := v.StringList(name, "language tags"); ok {
			*target = &languages
			has = true
		}
	}
	if text, ok := v.String("subtitleMode"); ok {
		mode := store.SubtitleMode(text)
		settings.SubtitleMode = &mode
		has = true
	}
	if err := v.Err(); err != nil {
		return store.ProfileSettings{}, false, err
	}
	return settings, has, nil
}

// LibraryInfo is the wire shape for Library management mutations.
type LibraryInfo struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	PluginID     *string  `json:"pluginID"`
	PreviewClips bool     `json:"previewClips"`
	Restricted   bool     `json:"restricted"`
	Roots        []string `json:"roots,omitempty"`
	// FamilyScope is the plugin-declared family scope the Library was created
	// with; "" when unset. Omitted from the wire when empty.
	FamilyScope string `json:"familyScope,omitempty"`
	// Custodial and Encrypted are the two orthogonal Library attributes:
	// Custodial means Beamfall holds the canonical copy (its
	// trashed Media is never auto-purged — the durability contract), Encrypted
	// marks the at-rest regime active. Both default false (Scanned + not
	// encrypted). ENC-1 surfaces the attributes only — the key model and cipher
	// are ENC-2/ENC-3.
	Custodial bool `json:"custodial"`
	Encrypted bool `json:"encrypted"`
}

// createLibrary inserts a Library granted to the manager's Profile.
// A restricted Library may only be created by a revealed session — otherwise
// its existence would leak into the unrevealed world that just created it.
func (h *Handler) createLibrary(ctx context.Context, scope auth.Scope, req request) (LibraryInfo, error) {
	title := stringVar(req, "title")
	restricted := boolVar(req, "restricted")
	pluginID := trimmedVariable(req, "pluginID")
	familyScope := trimmedVariable(req, "familyScope")
	var bundle plugin.Bundle
	var hasBundle bool
	if pluginID != "" && h.plugins != nil {
		bundle, hasBundle = h.plugins.Bundle(plugin.ID(pluginID))
		if hasBundle && bundle.LibraryDefaults.Restricted {
			restricted = true
		}
	}
	v := newVars(req)
	roots, hasRoots := v.StringList("roots", "roots")
	if err := v.Err(); err != nil {
		return LibraryInfo{}, err
	}
	if restricted && !scope.Revealed {
		return LibraryInfo{}, clientError("reveal required to create a restricted library")
	}
	if !hasRoots {
		roots = nil
	}
	// A family scope is only meaningful against a plugin that declares it:
	// reject a scope on an unbound Library, or one the bound
	// plugin doesn't offer. An empty scope is always accepted (unset).
	if familyScope != "" {
		if pluginID == "" {
			return LibraryInfo{}, clientError("family scope requires a plugin-bound library")
		}
		if h.plugins == nil {
			return LibraryInfo{}, clientError("unknown plugin")
		}
		if !hasBundle {
			return LibraryInfo{}, clientError("unknown plugin")
		}
		if !bundle.FamilyScopeValid(familyScope) {
			return LibraryInfo{}, clientError("unknown family scope")
		}
	}
	library, err := h.store.CreateLibraryWithRoots(ctx, title, restricted, pluginID, scope.ProfileID, roots, familyScope)
	if err != nil {
		return LibraryInfo{}, err
	}
	normalizedRoots, err := h.store.LibraryRoots(ctx, library.ID)
	if err != nil {
		return LibraryInfo{}, err
	}
	if h.libraryObserver != nil {
		h.libraryObserver.LibraryCreated(ctx, library.ID, normalizedRoots)
	}
	// Echo the server-truth binding (not the request value) so clients
	// never have to guess what was stored.
	return h.libraryInfo(ctx, library)
}

// deleteLibrary soft-deletes a Library (master #42). The Library is
// immediately hidden from all store queries; its scan schedule, profile grants,
// and media rows are preserved until a future purge. Manager role required.
func (h *Handler) deleteLibrary(ctx context.Context, scope auth.Scope, req request) error {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return clientError("libraryID is required")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return err
	}
	if !visible {
		return clientError("unknown library")
	}
	if err := h.store.DeleteLibrary(ctx, store.LibraryID(libraryID)); err != nil {
		return err
	}
	if err := h.logActivity(ctx, scope, store.ActivityLibraryDelete, "library", libraryID, store.LibraryID(libraryID)); err != nil {
		return err
	}
	return nil
}

func (h *Handler) libraryInfo(ctx context.Context, library store.Library) (LibraryInfo, error) {
	roots, err := h.store.LibraryRoots(ctx, library.ID)
	if err != nil {
		return LibraryInfo{}, err
	}
	return LibraryInfo{
		ID:           string(library.ID),
		Title:        library.Title,
		PluginID:     library.PluginID,
		PreviewClips: library.PreviewClips,
		Restricted:   library.IsRestricted,
		Roots:        roots,
		FamilyScope:  library.FamilyScope,
		Custodial:    library.Custodial,
		Encrypted:    library.Encrypted,
	}, nil
}

func (h *Handler) libraries(ctx context.Context, scope auth.Scope) ([]LibraryInfo, error) {
	libraries, err := h.store.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	infos := make([]LibraryInfo, 0, len(libraries))
	for _, library := range libraries {
		info, err := h.libraryInfo(ctx, library)
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func (h *Handler) setLibraryRoots(ctx context.Context, scope auth.Scope, req request) (LibraryInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return LibraryInfo{}, clientError("libraryID is required")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return LibraryInfo{}, err
	}
	if !visible {
		return LibraryInfo{}, clientError("unknown library")
	}
	v := newVars(req)
	roots, ok := v.StringList("roots", "roots")
	if err := v.Err(); err != nil {
		return LibraryInfo{}, err
	}
	if !ok {
		return LibraryInfo{}, clientError("roots is required")
	}
	if err := h.store.SetLibraryRoots(ctx, store.LibraryID(libraryID), roots); err != nil {
		return LibraryInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityLibraryRoots, "library", libraryID, store.LibraryID(libraryID)); err != nil {
		return LibraryInfo{}, err
	}
	normalizedRoots, err := h.store.LibraryRoots(ctx, store.LibraryID(libraryID))
	if err != nil {
		return LibraryInfo{}, err
	}
	if h.libraryObserver != nil {
		h.libraryObserver.LibraryRootsChanged(ctx, store.LibraryID(libraryID), normalizedRoots)
	}
	libraries, err := h.store.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		return LibraryInfo{}, err
	}
	for _, library := range libraries {
		if library.ID == store.LibraryID(libraryID) {
			return h.libraryInfo(ctx, library)
		}
	}
	return LibraryInfo{}, clientError("unknown library")
}

func (h *Handler) setLibraryPreviewClips(ctx context.Context, scope auth.Scope, req request) (LibraryInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return LibraryInfo{}, clientError("libraryID is required")
	}
	v := newVars(req)
	enabled, ok := v.Bool("enabled")
	if err := v.Err(); err != nil {
		return LibraryInfo{}, err
	}
	if !ok {
		return LibraryInfo{}, clientError("enabled is required")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return LibraryInfo{}, err
	}
	if !visible {
		return LibraryInfo{}, clientError("unknown library")
	}
	if err := h.store.SetLibraryPreviewClips(ctx, store.LibraryID(libraryID), enabled); err != nil {
		return LibraryInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityLibraryPreviewClips, "library", libraryID, store.LibraryID(libraryID)); err != nil {
		return LibraryInfo{}, err
	}
	libraries, err := h.store.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		return LibraryInfo{}, err
	}
	for _, library := range libraries {
		if library.ID == store.LibraryID(libraryID) {
			return h.libraryInfo(ctx, library)
		}
	}
	return LibraryInfo{}, clientError("unknown library")
}

// setLibraryRestricted toggles a Library's restriction flag.
// Changing IsRestricted moves the reveal boundary itself, so it requires a
// revealed session in BOTH directions: it mirrors createLibrary's reveal-to-create
// a restricted Library, and it is what makes the now-restricted Library still
// visible to the acting session for the server-truth echo below. The visibility
// check additionally enforces reveal when the current target is already
// restricted (an unrevealed session cannot see it to flip it back). A no-op
// toggle (value already current) short-circuits — no write, no cache purge.
func (h *Handler) setLibraryRestricted(ctx context.Context, scope auth.Scope, req request) (LibraryInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return LibraryInfo{}, clientError("libraryID is required")
	}
	v := newVars(req)
	restricted, ok := v.Bool("restricted")
	if err := v.Err(); err != nil {
		return LibraryInfo{}, err
	}
	if !ok {
		return LibraryInfo{}, clientError("restricted is required")
	}
	if !scope.Revealed {
		return LibraryInfo{}, clientError("reveal required to change library restriction")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return LibraryInfo{}, err
	}
	if !visible {
		return LibraryInfo{}, clientError("unknown library")
	}
	current, err := h.store.LibraryRestricted(ctx, store.LibraryID(libraryID))
	if err != nil {
		return LibraryInfo{}, err
	}
	if current != restricted {
		if err := h.store.UpdateLibraryRestricted(ctx, store.LibraryID(libraryID), restricted); err != nil {
			return LibraryInfo{}, err
		}
		if err := h.logActivity(ctx, scope, store.ActivityLibraryRestriction, "library", libraryID, store.LibraryID(libraryID)); err != nil {
			return LibraryInfo{}, err
		}
		if err := h.emitLibraryRestrictionInvalidation(ctx, store.LibraryID(libraryID)); err != nil {
			return LibraryInfo{}, err
		}
	}
	libraries, err := h.store.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		return LibraryInfo{}, err
	}
	for _, library := range libraries {
		if library.ID == store.LibraryID(libraryID) {
			return h.libraryInfo(ctx, library)
		}
	}
	return LibraryInfo{}, clientError("unknown library")
}

// emitLibraryRestrictionInvalidation fans a content-free client.invalidate out
// to every Profile granted the Library. A restriction
// toggle changes the visible set for many Profiles at once, so — unlike the
// per-Profile emitAccessCeilingInvalidation — it enumerates the affected
// Profiles and emits one per-Profile event each. Each event carries only the
// Profile and a generic Reason (no Library id, no titles — invariant #5), so the
// existing eventPermitted Profile gate delivers it to exactly that Profile's
// still-connected sessions and never becomes a reveal/existence side-channel. A
// nil store is a no-op.
func (h *Handler) emitLibraryRestrictionInvalidation(ctx context.Context, libraryID store.LibraryID) error {
	if h.store == nil || libraryID == "" {
		return nil
	}
	profiles, err := h.store.ProfilesGrantedLibrary(ctx, libraryID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, profileID := range profiles {
		if err := h.store.AppendInvalidationEvent(ctx, store.InvalidationPayload{
			ProfileID: profileID,
			Reason:    store.InvalidationReasonLibraryRestrictionChanged,
		}, now); err != nil {
			return err
		}
	}
	return nil
}

// scanLibrary enqueues the durable scan job for a Library the manager can see.
// The job dedupes by (library, root) while one is already queued; the existing
// jobs query is the progress surface.
func (h *Handler) scanLibrary(ctx context.Context, scope auth.Scope, req request) (JobInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	root := trimmedVariable(req, "root")
	// Optional backfill flag: re-runs music hydration for audio that
	// lacks the marker. Absent defaults to false — a normal scan, unchanged.
	backfill, _ := newVars(req).Bool("backfill")
	if libraryID == "" {
		return JobInfo{}, clientError("libraryID is required")
	}
	libraries, err := h.store.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		return JobInfo{}, err
	}
	known := false
	for _, library := range libraries {
		if library.ID == store.LibraryID(libraryID) {
			known = true
			break
		}
	}
	if !known {
		return JobInfo{}, clientError("unknown library")
	}
	roots := []string{root}
	if root == "" {
		persisted, err := h.store.LibraryRoots(ctx, store.LibraryID(libraryID))
		if err != nil {
			return JobInfo{}, err
		}
		if len(persisted) == 0 {
			return JobInfo{}, clientError("library has no roots")
		}
		roots = persisted
	} else {
		entry, err := h.store.FolderTree(ctx, store.LibraryID(libraryID), root, 0, scope.LibraryFilter())
		if err != nil {
			return JobInfo{}, err
		}
		if entry.Kind != "folder" {
			return JobInfo{}, clientError("scan root is not a folder")
		}
	}
	var first store.Job
	for _, scanRoot := range roots {
		job, err := scan.NewJob(scan.JobRequest{
			LibraryID: store.LibraryID(libraryID),
			Root:      scanRoot,
			CreatedAt: time.Now().UTC(),
			Backfill:  backfill,
		})
		if err != nil {
			return JobInfo{}, err
		}
		created, err := h.store.EnqueueJob(ctx, job)
		if err != nil {
			return JobInfo{}, err
		}
		if first.ID == "" {
			first = created
		}
		if h.scanObserver != nil {
			h.scanObserver.Observe(store.LibraryID(libraryID), scanRoot)
		}
	}
	return h.jobInfo(ctx, scope, first), nil
}

// SubscriptionInfo is the wire shape for one Library-level feed Subscription.
// Manager-gated: feedURL can carry private-feed credentials
// (invariant 5) — it appears here so the Console can manage the Subscription,
// and nowhere else. The private-feed Authorization header is NEVER carried
// here: it lives in the secure store (invariant #5), and the Console only needs
// to know WHETHER a feed has a stored secret (hasAuth), never its value.
type SubscriptionInfo struct {
	ID        string `json:"id"`
	LibraryID string `json:"libraryID"`
	FeedURL   string `json:"feedURL"`
	// HasAuth reports whether a private-feed Authorization header is stored in
	// the secure store for this Subscription. The header value is never
	// returned (invariant #5) — only its presence, so the Console can render an
	// "authenticated feed" indicator and offer to replace the secret.
	HasAuth       bool   `json:"hasAuth"`
	Root          string `json:"root"`
	Title         string `json:"title"`
	AddedAt       string `json:"addedAt,omitempty"`
	LastPolledAt  string `json:"lastPolledAt,omitempty"`
	Followed      bool   `json:"followed"`
	RetentionKind string `json:"retentionKind"`
	RetentionN    *int   `json:"retentionN"`
}

type SubscriptionEntryAssociationInfo struct {
	SubscriptionID         string `json:"subscriptionID"`
	GUID                   string `json:"guid"`
	MediaID                string `json:"mediaID"`
	Path                   string `json:"path"`
	ReplayedItemStateSweep int    `json:"replayedItemStateSweep"`
}

func (h *Handler) subscriptionInfo(ctx context.Context, scope auth.Scope, subscription store.Subscription) (SubscriptionInfo, error) {
	followed, err := h.store.SubscriptionFollowed(ctx, scope.ProfileID, subscription.ID)
	if err != nil {
		return SubscriptionInfo{}, err
	}
	return SubscriptionInfo{
		ID:            string(subscription.ID),
		LibraryID:     string(subscription.LibraryID),
		FeedURL:       subscription.FeedURL,
		HasAuth:       subscription.AuthRef != "",
		Root:          subscription.Root,
		Title:         subscription.Title,
		AddedAt:       formatOptionalTime(subscription.AddedAt),
		LastPolledAt:  formatOptionalTime(subscription.LastPolledAt),
		Followed:      followed,
		RetentionKind: string(subscription.RetentionKind),
		RetentionN:    subscription.RetentionN,
	}, nil
}

// visibleLibrary reports whether the scope can currently see the Library —
// the same gate scanLibrary applies before touching Library-scoped state.
func (h *Handler) visibleLibrary(ctx context.Context, scope auth.Scope, id store.LibraryID) (bool, error) {
	libraries, err := h.store.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		return false, err
	}
	for _, library := range libraries {
		if library.ID == id {
			return true, nil
		}
	}
	return false, nil
}

// addSubscription creates a Library-level feed Subscription (manager-gated in
// execute) for a Library the session can currently see.
func (h *Handler) addSubscription(ctx context.Context, scope auth.Scope, req request) (SubscriptionInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	feedURL := stringVar(req, "feedURL")
	root := stringVar(req, "root")
	authHeader := stringVar(req, "authHeader")
	title := stringVar(req, "title")
	if libraryID == "" || strings.TrimSpace(feedURL) == "" {
		return SubscriptionInfo{}, clientError("libraryID and feedURL are required")
	}
	// A private-feed Authorization header is a secret (invariant #5): it goes
	// to the secure store, never the catalog DB. Refuse it up front when no
	// secure store is wired rather than silently dropping or plaintext-storing
	// it. A blank header is an anonymous feed — no secret, no rejection.
	hasAuth := strings.TrimSpace(authHeader) != ""
	if hasAuth && h.subscriptionSecrets == nil {
		return SubscriptionInfo{}, clientError("private-feed credentials are unavailable on this server")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return SubscriptionInfo{}, err
	}
	if !visible {
		return SubscriptionInfo{}, clientError("unknown library")
	}
	// Persist the row WITHOUT the credential. The auth_ref is set in a second
	// step because it derives from the server-issued Subscription ID.
	subscription, err := h.store.AddSubscription(ctx, store.Subscription{
		LibraryID: store.LibraryID(libraryID),
		FeedURL:   feedURL,
		Root:      root,
		Title:     title,
	}, time.Now().UTC())
	if err != nil {
		return SubscriptionInfo{}, err
	}
	if hasAuth {
		ref := subscriptionAuthRef(subscription.ID)
		// Secure store first, then the ref: a stored ref must always resolve.
		if err := h.subscriptionSecrets.SetSecret(ref, authHeader); err != nil {
			return SubscriptionInfo{}, err
		}
		if err := h.store.SetSubscriptionAuthRef(ctx, subscription.ID, ref); err != nil {
			return SubscriptionInfo{}, err
		}
		subscription.AuthRef = ref
	}
	return h.subscriptionInfo(ctx, scope, subscription)
}

// subscriptionAuthRef derives the secure-store key for a Subscription's
// private-feed Authorization header from its server-issued ID. The ID is
// already path-safe (a "subscription-" + hex string), so the ref is a stable,
// collision-free secretFiles entry name carrying no credential.
func subscriptionAuthRef(id store.SubscriptionID) string {
	return "subscription-auth-" + string(id)
}

// removeSubscription deletes a Subscription whose Library the session can
// currently see and returns the removed row. Downloaded Media stays.
func (h *Handler) removeSubscription(ctx context.Context, scope auth.Scope, req request) (SubscriptionInfo, error) {
	id := trimmedVariable(req, "id")
	if id == "" {
		return SubscriptionInfo{}, clientError("id is required")
	}
	subscription, err := h.store.Subscription(ctx, store.SubscriptionID(id))
	if err != nil {
		return SubscriptionInfo{}, clientError("unknown subscription")
	}
	visible, err := h.visibleLibrary(ctx, scope, subscription.LibraryID)
	if err != nil {
		return SubscriptionInfo{}, err
	}
	if !visible {
		// Indistinguishable from a missing Subscription: a restricted
		// Library's subscriptions must not exist for unrevealed sessions.
		return SubscriptionInfo{}, clientError("unknown subscription")
	}
	removed, err := h.store.RemoveSubscription(ctx, store.SubscriptionID(id))
	if err != nil {
		return SubscriptionInfo{}, clientError("unknown subscription")
	}
	// Reap the private-feed secret so a removed Subscription leaves no orphaned
	// credential in the secure store (invariant #5). Best-effort: the row is
	// already gone, and SetSecret("") on a missing file is a no-op.
	if removed.AuthRef != "" && h.subscriptionSecrets != nil {
		_ = h.subscriptionSecrets.SetSecret(removed.AuthRef, "")
	}
	return h.subscriptionInfo(ctx, scope, removed)
}

// subscriptions lists a visible Library's feed Subscriptions.
func (h *Handler) subscriptions(ctx context.Context, scope auth.Scope, req request) ([]SubscriptionInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return nil, clientError("libraryID is required")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, clientError("unknown library")
	}
	subscriptions, err := h.store.Subscriptions(ctx, store.LibraryID(libraryID))
	if err != nil {
		return nil, err
	}
	infos := make([]SubscriptionInfo, len(subscriptions))
	for i, subscription := range subscriptions {
		info, err := h.subscriptionInfo(ctx, scope, subscription)
		if err != nil {
			return nil, err
		}
		infos[i] = info
	}
	return infos, nil
}

// FollowInfo is the viewer-safe setFollow echo: identity, display title,
// and the new follow state. Never SubscriptionInfo — that shape carries
// feedURL and root, which are manager-gated (invariant 5) and must not reach
// viewer sessions. (The private-feed credential itself is never in
// SubscriptionInfo at all — it lives in the secure store.)
type FollowInfo struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Followed bool   `json:"followed"`
}

func (h *Handler) setFollow(ctx context.Context, scope auth.Scope, req request) (FollowInfo, error) {
	id := store.SubscriptionID(trimmedVariable(req, "subscriptionID"))
	if id == "" {
		id = store.SubscriptionID(trimmedVariable(req, "id"))
	}
	if id == "" {
		return FollowInfo{}, clientError("subscriptionID is required")
	}
	subscription, err := h.store.Subscription(ctx, id)
	if err != nil {
		return FollowInfo{}, clientError("unknown subscription")
	}
	visible, err := h.visibleLibrary(ctx, scope, subscription.LibraryID)
	if err != nil {
		return FollowInfo{}, err
	}
	if !visible {
		// A subscription in a Library the caller cannot see reads exactly
		// as absent — no existence oracle for restricted Libraries.
		return FollowInfo{}, clientError("unknown subscription")
	}
	on := boolVar(req, "on")
	if err := h.store.SetFollow(ctx, scope.ProfileID, id, on, time.Now().UTC()); err != nil {
		return FollowInfo{}, err
	}
	followed, err := h.store.SubscriptionFollowed(ctx, scope.ProfileID, id)
	if err != nil {
		return FollowInfo{}, err
	}
	return FollowInfo{ID: string(subscription.ID), Title: subscription.Title, Followed: followed}, nil
}

func (h *Handler) setSubscriptionRetention(ctx context.Context, scope auth.Scope, req request) (SubscriptionInfo, error) {
	id := store.SubscriptionID(trimmedVariable(req, "id"))
	kind := store.RetentionKind(trimmedVariable(req, "kind"))
	if id == "" || kind == "" {
		return SubscriptionInfo{}, clientError("id and kind are required")
	}
	subscription, err := h.store.Subscription(ctx, id)
	if err != nil {
		return SubscriptionInfo{}, clientError("unknown subscription")
	}
	visible, err := h.visibleLibrary(ctx, scope, subscription.LibraryID)
	if err != nil {
		return SubscriptionInfo{}, err
	}
	if !visible {
		return SubscriptionInfo{}, clientError("unknown subscription")
	}
	v := newVars(req)
	var n *int
	if number, ok := v.Number("n"); ok {
		value := int(number)
		n = &value
	}
	if err := v.Err(); err != nil {
		return SubscriptionInfo{}, err
	}
	updated, err := h.store.SetSubscriptionRetention(ctx, id, kind, n)
	if errors.Is(err, store.ErrSubscriptionNotFound) {
		return SubscriptionInfo{}, clientError("unknown subscription")
	}
	if err != nil {
		return SubscriptionInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivitySubscriptionRetention, "subscription", string(id), updated.LibraryID); err != nil {
		return SubscriptionInfo{}, err
	}
	return h.subscriptionInfo(ctx, scope, updated)
}

func (h *Handler) associateSubscriptionEntry(ctx context.Context, scope auth.Scope, req request) (SubscriptionEntryAssociationInfo, error) {
	id := store.SubscriptionID(trimmedVariable(req, "subscriptionID"))
	guid := trimmedVariable(req, "guid")
	mediaID := store.MediaID(trimmedVariable(req, "mediaID"))
	if id == "" || guid == "" || mediaID == "" {
		return SubscriptionEntryAssociationInfo{}, clientError("subscriptionID, guid, and mediaID are required")
	}
	media, err := h.store.Media(ctx, mediaID, scope.LibraryFilter())
	if err != nil {
		return SubscriptionEntryAssociationInfo{}, clientError("unknown media")
	}
	subscription, err := h.store.Subscription(ctx, id)
	if err != nil {
		return SubscriptionEntryAssociationInfo{}, clientError("unknown subscription")
	}
	visible, err := h.visibleLibrary(ctx, scope, subscription.LibraryID)
	if err != nil {
		return SubscriptionEntryAssociationInfo{}, err
	}
	if !visible {
		return SubscriptionEntryAssociationInfo{}, clientError("unknown subscription")
	}
	if media.LibraryID != subscription.LibraryID {
		return SubscriptionEntryAssociationInfo{}, clientError("media does not belong to the subscription library")
	}
	association, err := h.store.AssociateSubscriptionEntryMedia(ctx, id, guid, mediaID, time.Now().UTC())
	if errors.Is(err, store.ErrSubscriptionNotFound) {
		return SubscriptionEntryAssociationInfo{}, clientError("unknown subscription")
	}
	if err != nil {
		return SubscriptionEntryAssociationInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivitySubscriptionEntryAssociation, "subscription", string(id), subscription.LibraryID); err != nil {
		return SubscriptionEntryAssociationInfo{}, err
	}
	return SubscriptionEntryAssociationInfo{
		SubscriptionID:         string(association.SubscriptionID),
		GUID:                   association.GUID,
		MediaID:                string(association.MediaID),
		Path:                   association.Path,
		ReplayedItemStateSweep: association.ReplayedItemStateSweep,
	}, nil
}

// search returns reveal-filtered Media title matches for the session scope.
func (h *Handler) search(ctx context.Context, scope auth.Scope, req request) ([]SourceItem, error) {
	text := stringVar(req, "query")
	items, err := h.store.SearchMedia(ctx, scope.LibraryFilter(), text, 50)
	if err != nil {
		return nil, err
	}
	// Explicit search is a deliberately-surfaced surface: muted items appear on
	// purpose (§2.2 "searchable on purpose") and carry the Muted badge (§2.7).
	if err := h.store.AnnotateMuted(ctx, scope.ProfileID, scope.LibraryFilter(), items); err != nil {
		return nil, err
	}
	resp := make([]SourceItem, len(items))
	for i, item := range items {
		resp[i] = h.sourceItem(item, scope.Revealed)
	}
	return resp, nil
}

func (h *Handler) searchFeeds(ctx context.Context, scope auth.Scope, req request) ([]FeedSearchResult, error) {
	if scope.DeviceRole != store.DeviceRoleManager && scope.Profile != store.ProfileAdult {
		return nil, clientError("adult profile or manager role required")
	}
	query := strings.TrimSpace(stringVar(req, "query"))
	if query == "" {
		return nil, clientError("query is required")
	}
	if h.feedSearcher == nil {
		return []FeedSearchResult{}, nil
	}
	results, err := h.feedSearcher.SearchFeeds(ctx, query)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	deduped := make([]FeedSearchResult, 0, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.FeedURL) == "" || seen[result.FeedURL] {
			continue
		}
		seen[result.FeedURL] = true
		deduped = append(deduped, result)
	}
	return deduped, nil
}

// searchEntities returns reveal-filtered Entity matches (title + declared
// Searchable field values) for the session scope — the second result list
// beside search's Media list, kept a sibling field so the existing search
// shape stays a bare item array.
func (h *Handler) searchEntities(ctx context.Context, scope auth.Scope, req request) ([]SourceItem, error) {
	text := stringVar(req, "query")
	items, err := h.store.SearchEntities(ctx, scope.LibraryFilter(), text, 50)
	if err != nil {
		return nil, err
	}
	// Entity search results badge muted entities (kind-aware: an entity id that is
	// a direct or broad ENTITY mute target, §2.7).
	if err := h.store.AnnotateMuted(ctx, scope.ProfileID, scope.LibraryFilter(), items); err != nil {
		return nil, err
	}
	resp := make([]SourceItem, len(items))
	for i, item := range items {
		resp[i] = h.sourceItem(item, scope.Revealed)
	}
	return resp, nil
}

// RevealState reports the session's reveal state after a reveal mutation.
type RevealState struct {
	Revealed bool `json:"revealed"`
}

const (
	revealAttemptAction       = "reveal"
	revealAttemptWindow       = 5 * time.Minute
	profileEntryAttemptAction = "profile-entry"
)

// revealBackoff is the Reveal-PIN lane's exponential-backoff curve (ADR-0012
// §3): the lockout grows with consecutive failed reveal attempts and resets on
// a correct PIN. Reveal is the family-safety boundary — a flat counter falls to
// a cheap brute force, so the delay doubles per failure up to MaxDelay.
var revealBackoff = store.AuthBackoff{
	Window:       revealAttemptWindow,
	FreeAttempts: 5,
	BaseDelay:    2 * time.Second,
	MaxDelay:     revealAttemptWindow,
}

var profileEntryBackoff = store.AuthBackoff{
	Window:       revealAttemptWindow,
	FreeAttempts: 5,
	BaseDelay:    2 * time.Second,
	MaxDelay:     revealAttemptWindow,
}

// APITokenInfo is the wire shape for one admin API token —
// list metadata only; no secret material in any form.
type APITokenInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  string `json:"createdAt"`
	LastUsedAt string `json:"lastUsedAt,omitempty"`
	Revoked    bool   `json:"revoked"`
}

// CreatedAPITokenInfo carries the full token string exactly once, from the
// create mutation's response. It is never queryable again.
type CreatedAPITokenInfo struct {
	Token string       `json:"token"`
	Info  APITokenInfo `json:"info"`
}

type BackupInfo struct {
	Manifest string `json:"manifest"`
}

// BackupHistoryEntry is one published backup set surfaced to admin clients:
// its directory, ISO-8601 creation time, on-disk size, whether it passes
// confined integrity verification, and a sanitized failure reason. It never
// carries a computed checksum of an arbitrary server file.
type BackupHistoryEntry struct {
	Directory string `json:"directory"`
	CreatedAt string `json:"createdAt"`
	SizeBytes int64  `json:"sizeBytes"`
	Valid     bool   `json:"valid"`
	Error     string `json:"error,omitempty"`
}

// VerifyBackupInfo is the result of the admin verifyBackup mutation: whether the
// backup set passed confined integrity verification and a sanitized reason on
// failure.
type VerifyBackupInfo struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (h *Handler) backup(ctx context.Context, req request) (BackupInfo, error) {
	dest := stringVar(req, "dest")
	if strings.TrimSpace(dest) == "" {
		return BackupInfo{}, clientError("dest is required")
	}
	var (
		result store.BackupResult
		err    error
	)
	if h.libEncrypt != nil {
		result, err = h.store.BackupWithOptions(ctx, dest, store.BackupOptions{EncryptedLibraryDBCipher: h.libEncryptDBCipher()})
	} else {
		result, err = h.store.Backup(ctx, dest)
	}
	if err != nil {
		return BackupInfo{}, err
	}
	return BackupInfo{Manifest: result.Manifest}, nil
}

// backupHistory lists the published backup sets under dest (newest first),
// verifying each through the store's confined VerifyBackupSet so the read cannot
// be aimed at arbitrary server files. dest is guarded to the sanctioned backup
// allowlist by the store.
func (h *Handler) backupHistory(ctx context.Context, req request) ([]BackupHistoryEntry, error) {
	dest := stringVar(req, "dest")
	if strings.TrimSpace(dest) == "" {
		return nil, clientError("dest is required")
	}
	summaries, err := h.store.BackupHistory(ctx, dest)
	if err != nil {
		return nil, err
	}
	entries := make([]BackupHistoryEntry, 0, len(summaries))
	for _, summary := range summaries {
		createdAt := ""
		if !summary.CreatedAt.IsZero() {
			createdAt = summary.CreatedAt.UTC().Format(time.RFC3339Nano)
		}
		entries = append(entries, BackupHistoryEntry{
			Directory: summary.Directory,
			CreatedAt: createdAt,
			SizeBytes: summary.SizeBytes,
			Valid:     summary.Valid,
			Error:     summary.Error,
		})
	}
	return entries, nil
}

// verifyBackup runs the store's confined integrity verification over a published
// backup set identified by its manifest path. It never restores or mutates and
// returns only a sanitized pass/fail — never a checksum of an arbitrary file.
func (h *Handler) verifyBackup(ctx context.Context, req request) (VerifyBackupInfo, error) {
	manifestPath := stringVar(req, "manifest")
	if strings.TrimSpace(manifestPath) == "" {
		return VerifyBackupInfo{}, clientError("manifest is required")
	}
	if err := h.store.VerifyBackupSet(ctx, manifestPath); err != nil {
		return VerifyBackupInfo{OK: false, Error: err.Error()}, nil
	}
	return VerifyBackupInfo{OK: true}, nil
}

func (h *Handler) apiTokens(ctx context.Context) ([]APITokenInfo, error) {
	tokens, err := h.store.APITokens(ctx)
	if err != nil {
		return nil, err
	}
	infos := make([]APITokenInfo, 0, len(tokens))
	for _, token := range tokens {
		infos = append(infos, apiTokenInfo(token))
	}
	return infos, nil
}

func (h *Handler) createAPIToken(ctx context.Context, scope auth.Scope, req request) (CreatedAPITokenInfo, error) {
	name := trimmedVariable(req, "name")
	if name == "" {
		return CreatedAPITokenInfo{}, clientError("name is required")
	}
	token, secret, err := h.store.CreateAPIToken(ctx, name, time.Now().UTC())
	if err != nil {
		return CreatedAPITokenInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityTokenCreate, "token", token.ID, ""); err != nil {
		return CreatedAPITokenInfo{}, err
	}
	return CreatedAPITokenInfo{Token: secret, Info: apiTokenInfo(token)}, nil
}

func (h *Handler) revokeAPIToken(ctx context.Context, scope auth.Scope, req request) (APITokenInfo, error) {
	id := trimmedVariable(req, "id")
	if id == "" {
		return APITokenInfo{}, clientError("id is required")
	}
	if err := h.store.RevokeAPIToken(ctx, id); err != nil {
		return APITokenInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityTokenRevoke, "token", id, ""); err != nil {
		return APITokenInfo{}, err
	}
	tokens, err := h.store.APITokens(ctx)
	if err != nil {
		return APITokenInfo{}, err
	}
	for _, token := range tokens {
		if token.ID == id {
			return apiTokenInfo(token), nil
		}
	}
	return APITokenInfo{}, clientError("token not found")
}

func apiTokenInfo(token store.APIToken) APITokenInfo {
	info := APITokenInfo{
		ID:        token.ID,
		Name:      token.Name,
		CreatedAt: token.CreatedAt.UTC().Format(time.RFC3339),
		Revoked:   token.Revoked,
	}
	if !token.LastUsedAt.IsZero() {
		info.LastUsedAt = token.LastUsedAt.UTC().Format(time.RFC3339)
	}
	return info
}

// AdminSessionInfo reports the caller's Admin Session state: the
// boolean the Console reads to decide whether to surface admin-gated screens,
// and the window's end so the UI can prompt re-auth before it lapses.
type AdminSessionInfo struct {
	Active    bool   `json:"active"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

func (h *Handler) adminSession(scope auth.Scope) AdminSessionInfo {
	if !scope.Admin {
		return AdminSessionInfo{Active: false}
	}
	return AdminSessionInfo{Active: true, ExpiresAt: scope.AdminUntil.UTC().Format(time.RFC3339)}
}

// verifyAdminSession re-authenticates with the server admin password and opens
// the short-lived Admin Session window. On any failure it returns
// the same generic client error — no signal about whether a password is set,
// whether the Profile was eligible, or whether the lockout tripped beyond the
// throttle message. The password arrives in a variable and is never logged.
func (h *Handler) verifyAdminSession(ctx context.Context, scope auth.Scope, req request) (AdminSessionInfo, error) {
	password := stringVar(req, "password")
	if strings.TrimSpace(password) == "" {
		return AdminSessionInfo{}, clientError("password is required")
	}
	until, err := h.auth.BeginAdminSession(ctx, scope, password)
	if err != nil {
		if errors.Is(err, auth.ErrAdminLocked) {
			return AdminSessionInfo{}, clientError("admin re-auth throttled")
		}
		return AdminSessionInfo{}, clientError("admin password rejected")
	}
	return AdminSessionInfo{Active: true, ExpiresAt: until.UTC().Format(time.RFC3339)}, nil
}

func (h *Handler) endAdminSession(ctx context.Context, scope auth.Scope) (AdminSessionInfo, error) {
	if err := h.auth.EndAdminSession(ctx, scope); err != nil {
		return AdminSessionInfo{}, err
	}
	return AdminSessionInfo{Active: false}, nil
}

// ScopedAPIKeyInfo is the wire shape for one scoped API key —
// list metadata only: name, granted scopes, optional Profile binding; no
// secret material in any form.
type ScopedAPIKeyInfo struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes"`
	ProfileID  string   `json:"profileID,omitempty"`
	CreatedAt  string   `json:"createdAt"`
	LastUsedAt string   `json:"lastUsedAt,omitempty"`
	Revoked    bool     `json:"revoked"`
}

// CreatedScopedAPIKeyInfo carries the full key string exactly once, from the
// create mutation's response. It is never queryable again.
type CreatedScopedAPIKeyInfo struct {
	Key  string           `json:"key"`
	Info ScopedAPIKeyInfo `json:"info"`
}

func (h *Handler) apiKeys(ctx context.Context) ([]ScopedAPIKeyInfo, error) {
	keys, err := h.store.ScopedAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	infos := make([]ScopedAPIKeyInfo, 0, len(keys))
	for _, key := range keys {
		infos = append(infos, scopedAPIKeyInfo(key))
	}
	return infos, nil
}

func (h *Handler) createAPIKey(ctx context.Context, scope auth.Scope, req request) (CreatedScopedAPIKeyInfo, error) {
	name := trimmedVariable(req, "name")
	if name == "" {
		return CreatedScopedAPIKeyInfo{}, clientError("name is required")
	}
	scopes := stringSliceVar(req, "scopes")
	apiScopes := make([]store.APIScope, len(scopes))
	for i, s := range scopes {
		apiScopes[i] = store.APIScope(s)
	}
	profileID := store.ProfileID(trimmedVariable(req, "profileID"))
	key, secret, err := h.store.CreateScopedAPIKey(ctx, name, apiScopes, profileID, time.Now().UTC())
	if err != nil {
		var verr store.ValidationError
		if errors.As(err, &verr) {
			return CreatedScopedAPIKeyInfo{}, clientError(string(verr))
		}
		return CreatedScopedAPIKeyInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityKeyCreate, "key", key.ID, ""); err != nil {
		return CreatedScopedAPIKeyInfo{}, err
	}
	return CreatedScopedAPIKeyInfo{Key: secret, Info: scopedAPIKeyInfo(key)}, nil
}

func (h *Handler) revokeAPIKey(ctx context.Context, scope auth.Scope, req request) (ScopedAPIKeyInfo, error) {
	id := trimmedVariable(req, "id")
	if id == "" {
		return ScopedAPIKeyInfo{}, clientError("id is required")
	}
	if err := h.store.RevokeScopedAPIKey(ctx, id); err != nil {
		var verr store.ValidationError
		if errors.As(err, &verr) {
			return ScopedAPIKeyInfo{}, clientError(string(verr))
		}
		return ScopedAPIKeyInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityKeyRevoke, "key", id, ""); err != nil {
		return ScopedAPIKeyInfo{}, err
	}
	keys, err := h.store.ScopedAPIKeys(ctx)
	if err != nil {
		return ScopedAPIKeyInfo{}, err
	}
	for _, key := range keys {
		if key.ID == id {
			return scopedAPIKeyInfo(key), nil
		}
	}
	return ScopedAPIKeyInfo{}, clientError("key not found")
}

func scopedAPIKeyInfo(key store.ScopedAPIKey) ScopedAPIKeyInfo {
	scopes := make([]string, len(key.Scopes))
	for i, s := range key.Scopes {
		scopes[i] = string(s)
	}
	info := ScopedAPIKeyInfo{
		ID:        key.ID,
		Name:      key.Name,
		Scopes:    scopes,
		ProfileID: string(key.ProfileID),
		CreatedAt: key.CreatedAt.UTC().Format(time.RFC3339),
		Revoked:   key.Revoked,
	}
	if !key.LastUsedAt.IsZero() {
		info.LastUsedAt = key.LastUsedAt.UTC().Format(time.RFC3339)
	}
	return info
}

// logActivity appends one ADR-0012 §11 activity-log entry for the acting
// scope. The closed entry shape carries identifiers only — never a PIN,
// code, token, or path. Reveal/relock entries carry no target: they must
// never name restricted content in their payload.
func (h *Handler) logActivity(ctx context.Context, scope auth.Scope, action, targetType, targetID string, libraryID store.LibraryID) error {
	err := h.store.AppendActivity(ctx, store.ActivityEntry{
		OccurredAt: time.Now().UTC(),
		Action:     action,
		SessionID:  scope.SessionID,
		DeviceID:   scope.DeviceID,
		ProfileID:  scope.ProfileID,
		TargetType: targetType,
		TargetID:   targetID,
		LibraryID:  libraryID,
		TokenID:    scope.APITokenID,
		APIKeyID:   scope.APIKeyID,
	})
	if err != nil {
		// The request-level mutation.attempt is durable before dispatch, so a
		// detail-row failure cannot turn already-committed state into a false
		// client failure. The unmatched attempt remains an explicit recovery clue.
		slog.Error("mutation detail audit append failed", "action", action, "error", err)
	}
	return nil
}

// revealOps executes the Reveal-contract session mutations:
// reveal(pin) is PIN-gated and throttled per session, relock always works,
// setRevealPin is manager-only. Managed profiles have no reveal path.
func (h *Handler) revealOps(ctx context.Context, scope auth.Scope, fields map[string]bool, req request) (map[string]any, error) {
	data := map[string]any{}
	if fields["setrevealpin"] {
		pin := stringVar(req, "pin")
		if err := h.store.SetProfileRevealPIN(ctx, scope.ProfileID, strings.TrimSpace(pin)); err != nil {
			if errors.Is(err, store.ErrNoRevealPath) {
				return nil, clientError(err.Error())
			}
			return nil, err
		}
		data["setrevealpin"] = RevealState{Revealed: scope.Revealed}
	}
	if fields["reveal"] {
		now := time.Now().UTC()
		until, err := h.store.AuthBackoffUntil(ctx, revealAttemptAction, string(scope.SessionID), now, revealBackoff)
		if err != nil {
			return nil, err
		}
		if now.Before(until) {
			return nil, clientError("reveal attempts throttled")
		}
		pin := stringVar(req, "pin")
		verifyErr := h.store.VerifyRevealPIN(ctx, scope.ProfileID, strings.TrimSpace(pin))
		if recordErr := h.store.RecordAuthAttempt(ctx, store.AuthAttempt{
			OccurredAt: now,
			Action:     revealAttemptAction,
			Key:        string(scope.SessionID),
			Success:    verifyErr == nil,
		}); recordErr != nil {
			return nil, recordErr
		}
		if verifyErr != nil {
			if logErr := h.logActivity(ctx, scope, store.ActivityRevealDenied, "", "", ""); logErr != nil {
				return nil, logErr
			}
			if errors.Is(verifyErr, store.ErrNoRevealPath) {
				return nil, clientError(verifyErr.Error())
			}
			return nil, clientError("reveal PIN mismatch")
		}
		if err := h.store.SetSessionRevealed(ctx, scope.SessionID, true); err != nil {
			return nil, err
		}
		if err := h.logActivity(ctx, scope, store.ActivityReveal, "", "", ""); err != nil {
			return nil, err
		}
		data["reveal"] = RevealState{Revealed: true}
	}
	if fields["relock"] {
		if err := h.store.SetSessionRevealed(ctx, scope.SessionID, false); err != nil {
			return nil, err
		}
		if err := h.logActivity(ctx, scope, store.ActivityRelock, "", "", ""); err != nil {
			return nil, err
		}
		data["relock"] = RevealState{Revealed: false}
	}
	return data, nil
}

// TrashEntry is one trashed Media item in the manager trash view.
type TrashEntry struct {
	ID        string `json:"id"`
	LibraryID string `json:"libraryID"`
	Title     string `json:"title"`
	TrashedAt string `json:"trashedAt"`
}

// trashMedia soft-deletes a visible Media item and returns the updated trash.
func (h *Handler) trashMedia(ctx context.Context, scope auth.Scope, req request) ([]TrashEntry, error) {
	mediaID := trimmedVariable(req, "mediaID")
	if mediaID == "" {
		return nil, clientError("mediaID is required")
	}
	media, err := h.store.Media(ctx, store.MediaID(mediaID), scope.LibraryFilter())
	if err != nil {
		return nil, clientError("unknown media")
	}
	if err := h.store.TrashMedia(ctx, store.MediaID(mediaID), scope.LibraryFilter()); err != nil {
		return nil, clientError("unknown media")
	}
	if err := h.logActivity(ctx, scope, store.ActivityMediaTrash, "media", mediaID, media.LibraryID); err != nil {
		return nil, err
	}
	return h.trashList(ctx, scope)
}

// restoreMedia returns a trashed item to its Library and the updated trash.
func (h *Handler) restoreMedia(ctx context.Context, scope auth.Scope, req request) ([]TrashEntry, error) {
	mediaID := trimmedVariable(req, "mediaID")
	if mediaID == "" {
		return nil, clientError("mediaID is required")
	}
	trashed, err := h.store.TrashedMediaList(ctx, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	var libraryID store.LibraryID
	for _, entry := range trashed {
		if entry.ID == store.MediaID(mediaID) {
			libraryID = entry.LibraryID
			break
		}
	}
	if err := h.store.RestoreMedia(ctx, store.MediaID(mediaID), scope.LibraryFilter()); err != nil {
		return nil, clientError("unknown media")
	}
	if err := h.logActivity(ctx, scope, store.ActivityMediaRestore, "media", mediaID, libraryID); err != nil {
		return nil, err
	}
	return h.trashList(ctx, scope)
}

// purgeTrash permanently removes retained trashed Media older than the supplied
// cutoff. It is Admin-Session gated in execute(), requires a revealed manager
// session because the purge is global across Libraries, and still delegates the
// Custodial-Library exemption to store.PurgeTrashedBefore.
func (h *Handler) purgeTrash(ctx context.Context, scope auth.Scope, req request) (BulkOpResult, error) {
	if !scope.Revealed {
		return BulkOpResult{}, clientError("reveal required to purge trash")
	}
	v := newVars(req)
	confirm, ok := v.Bool("confirm")
	if err := v.Err(); err != nil {
		return BulkOpResult{}, err
	}
	if !ok || !confirm {
		return BulkOpResult{}, clientError("confirm is required to purge trash")
	}
	beforeText := strings.TrimSpace(stringVar(req, "before"))
	if beforeText == "" {
		return BulkOpResult{}, clientError("before is required")
	}
	before, err := time.Parse(time.RFC3339, beforeText)
	if err != nil {
		return BulkOpResult{}, clientError("before must be an RFC3339 date-time")
	}
	purged, err := h.store.PurgeTrashedBefore(ctx, before.UTC())
	if err != nil {
		return BulkOpResult{}, err
	}
	return BulkOpResult{Affected: purged}, nil
}

func (h *Handler) trashList(ctx context.Context, scope auth.Scope) ([]TrashEntry, error) {
	entries, err := h.store.TrashedMediaList(ctx, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	resp := make([]TrashEntry, len(entries))
	for i, entry := range entries {
		resp[i] = TrashEntry{
			ID:        string(entry.ID),
			LibraryID: string(entry.LibraryID),
			Title:     entry.Title,
			TrashedAt: entry.TrashedAt.UTC().Format(time.RFC3339),
		}
	}
	return resp, nil
}

// ActivityEntryInfo is one activity-log entry on the wire.
type ActivityEntryInfo struct {
	ID         string `json:"id"`
	OccurredAt string `json:"occurredAt"`
	Action     string `json:"action"`
	// Reversible is true when the action is recoverable via Restore (trash-routed
	// operations). Derived from the closed action vocabulary; not stored. Used by
	// E2E audit assertions.
	Reversible bool   `json:"reversible"`
	SessionID  string `json:"sessionID"`
	DeviceID   string `json:"deviceID"`
	ProfileID  string `json:"profileID"`
	TargetType string `json:"targetType,omitempty"`
	TargetID   string `json:"targetID,omitempty"`
	LibraryID  string `json:"libraryID,omitempty"`
	TokenID    string `json:"tokenID,omitempty"`
	APIKeyID   string `json:"apiKeyID,omitempty"`
}

// activityReversible returns true for operations routed through Trash and
// recoverable via Restore. False for config changes, auth events, and
// irreversible purges.
func activityReversible(action string) bool {
	switch action {
	case store.ActivityMediaTrash, store.ActivityMediaRestore:
		return true
	default:
		return false
	}
}

// activityLog pages the activity log newest first. Managers read the whole
// log; every other session reads only its own Profile's entries. Both reads
// pass through the caller's reveal filter — an entry referencing a Library
// the session cannot see is omitted as if it never existed.
func (h *Handler) activityLog(ctx context.Context, scope auth.Scope, req request) ([]ActivityEntryInfo, error) {
	query := store.ActivityQuery{
		Filter: scope.LibraryFilter(),
		Limit:  boundedIntVar(req, "limit", 50, 200),
		Offset: nonNegativeIntVar(req, "offset"),
	}
	if scope.DeviceRole != store.DeviceRoleManager {
		query.ProfileID = scope.ProfileID
	}
	entries, err := h.store.ActivityEntries(ctx, query)
	if err != nil {
		return nil, err
	}
	resp := make([]ActivityEntryInfo, len(entries))
	for i, entry := range entries {
		resp[i] = ActivityEntryInfo{
			ID:         strconv.FormatInt(entry.ID, 10),
			OccurredAt: entry.OccurredAt.UTC().Format(time.RFC3339),
			Action:     entry.Action,
			Reversible: activityReversible(entry.Action),
			SessionID:  string(entry.SessionID),
			DeviceID:   string(entry.DeviceID),
			ProfileID:  string(entry.ProfileID),
			TargetType: entry.TargetType,
			TargetID:   entry.TargetID,
			LibraryID:  string(entry.LibraryID),
		}
		if scope.DeviceRole == store.DeviceRoleManager {
			resp[i].TokenID = entry.TokenID
			resp[i].APIKeyID = entry.APIKeyID
		}
	}
	return resp, nil
}

// activityEntry returns one activity-log entry by its string id (ATEST-26:
// E2E audit assertion). Access is self-scoped like activityLog: managers can
// read any entry; non-managers can only read their own Profile's entries.
func (h *Handler) activityEntry(ctx context.Context, scope auth.Scope, req request) (ActivityEntryInfo, error) {
	idStr := trimmedVariable(req, "id")
	if idStr == "" {
		return ActivityEntryInfo{}, clientError("id is required")
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		return ActivityEntryInfo{}, clientError("id must be a positive integer string")
	}
	entry, err := h.store.ActivityByID(ctx, id, scope.LibraryFilter())
	if err != nil {
		return ActivityEntryInfo{}, err
	}
	if scope.DeviceRole != store.DeviceRoleManager && entry.ProfileID != scope.ProfileID {
		return ActivityEntryInfo{}, clientError("not found")
	}
	info := ActivityEntryInfo{
		ID:         strconv.FormatInt(entry.ID, 10),
		OccurredAt: entry.OccurredAt.UTC().Format(time.RFC3339),
		Action:     entry.Action,
		Reversible: activityReversible(entry.Action),
		SessionID:  string(entry.SessionID),
		DeviceID:   string(entry.DeviceID),
		ProfileID:  string(entry.ProfileID),
		TargetType: entry.TargetType,
		TargetID:   entry.TargetID,
		LibraryID:  string(entry.LibraryID),
	}
	if scope.DeviceRole == store.DeviceRoleManager {
		info.TokenID = entry.TokenID
		info.APIKeyID = entry.APIKeyID
	}
	return info, nil
}

// PlaybackReport echoes the stored engagement state after reportPlayback.
type PlaybackReport struct {
	MediaID         string `json:"mediaID"`
	PositionSeconds int    `json:"positionSeconds"`
	Watched         bool   `json:"watched"`
}

// reportPlayback records the session Profile's playback position for a Media
// item it can currently see (the engagement write is reveal-gated the same
// way the read is).
//
// The ambient flag marks an engagement-silent ambient play: the
// Ambient Display posture advances muted media as atmosphere, not intent, and
// must emit no Engagement Event — no resume position, no play count, no
// recently-played row — so ambient viewing never pollutes continue-watching or
// any engagement projection. An ambient report still reveal-resolves the Media
// (so it cannot probe hidden items) and echoes the stored state back, but
// performs NO write: the engagement DB is left untouched.
func (h *Handler) reportPlayback(ctx context.Context, scope auth.Scope, req request) (PlaybackReport, error) {
	mediaID := trimmedVariable(req, "mediaID")
	if mediaID == "" {
		return PlaybackReport{}, clientError("mediaID is required")
	}
	positionSeconds := intVar(req, "positionSeconds")
	durationSeconds := intVar(req, "durationSeconds")
	finished := boolVar(req, "finished")
	ambient := boolVar(req, "ambient")
	// eventTimeMS is the client's monotonic instant the
	// reported position was observed — never server-arrival time. A pre-upgrade
	// client omits it (int64Var reads missing/zero the same, 0), which the store
	// guard treats as "no event-time" (legacy unconditional-overwrite behavior).
	eventTimeMS := int64Var(req, "eventTimeMS")
	media, err := h.store.Media(ctx, store.MediaID(mediaID), scope.PlaybackFilter())
	if err != nil {
		return PlaybackReport{}, clientError("unknown media")
	}
	if durationSeconds <= 0 {
		durationSeconds = media.DurationSeconds
	}
	if ambient {
		// Engagement-silent: no write. Echo back the Profile's
		// EXISTING stored state for the Media unchanged — an ambient play never
		// creates, advances, or clears a progress row. With no prior row the
		// zero value (unwatched, position 0) is returned.
		progress, _, err := h.store.PlaybackProgressFor(ctx, scope.ProfileID, media.ID)
		if err != nil {
			return PlaybackReport{}, err
		}
		return PlaybackReport{
			MediaID:         string(media.ID),
			PositionSeconds: progress.PositionSeconds,
			Watched:         progress.Watched,
		}, nil
	}
	// Capture the prior watched state so we can detect the unwatched→watched
	// TRANSITION below: the scrobble observer must fire exactly
	// once per item, not on every position report past the threshold. A missing
	// prior row reads as unwatched (the zero value), which is correct.
	priorProgress, _, err := h.store.PlaybackProgressFor(ctx, scope.ProfileID, media.ID)
	if err != nil {
		return PlaybackReport{}, err
	}
	if err := h.store.UpsertPlaybackProgress(ctx, store.PlaybackProgress{
		ProfileID:       scope.ProfileID,
		MediaID:         media.ID,
		PositionSeconds: positionSeconds,
		DurationSeconds: durationSeconds,
		EventTimeMS:     eventTimeMS,
	}, finished, h.resumePolicy(ctx, media)); err != nil {
		return PlaybackReport{}, err
	}
	progress, _, err := h.store.PlaybackProgressFor(ctx, scope.ProfileID, media.ID)
	if err != nil {
		return PlaybackReport{}, err
	}
	// Scrobble: on the unwatched→watched transition, notify the
	// observer (the Scrobble Integration Plugin). It is best-effort and
	// non-blocking (the plugin enqueues and returns) — a third-party service must
	// never slow or fail the playback write. External IDs are resolved here (the
	// generic identity handles); the plugin maps them to each service.
	if h.watchedObserver != nil && progress.Watched && !priorProgress.Watched {
		h.notifyWatched(ctx, scope.ProfileID, media)
	}
	return PlaybackReport{
		MediaID:         string(media.ID),
		PositionSeconds: progress.PositionSeconds,
		Watched:         progress.Watched,
	}, nil
}

// notifyWatched resolves the watched Media's External IDs and hands the
// transition to the scrobble observer. A failure to read External IDs is
// swallowed — a scrobble is best-effort and must never fail the playback report;
// the plugin will simply skip-with-reason on no identity. The Media's
// LibraryID/Class/Title/Year ride the transition so the plugin can gate
// eligibility and map a submission without a second read.
func (h *Handler) notifyWatched(ctx context.Context, profileID store.ProfileID, media store.Media) {
	externalIDs, err := h.store.MediaExternalIDs(ctx, media.ID)
	if err != nil {
		externalIDs = nil
	}
	// Richer outbound metadata (ADR-0098 area 7): resolve contributor/release +
	// MBIDs
	// for audio Media only. Best-effort — a nil resolver or a miss yields the
	// zero value and the submission degrades, never failing the playback report.
	var audio OutboundAudioMetadata
	if h.outboundMetadata != nil && media.Class == store.MediaClassAudio {
		audio = h.outboundMetadata.ResolveOutboundAudioMetadata(ctx, media.ID)
	}
	h.watchedObserver.Watched(WatchedTransition{
		ProfileID:   profileID,
		MediaID:     media.ID,
		LibraryID:   media.LibraryID,
		Class:       media.Class,
		Title:       media.Title,
		Year:        media.Year,
		ExternalIDs: externalIDs,
		Audio:       audio,
		WatchedAt:   time.Now().UTC(),
	})
}

func (h *Handler) queueOps(ctx context.Context, scope auth.Scope, fields map[string]bool, req request) (map[string]any, error) {
	data := map[string]any{}
	if fields["queueadd"] {
		mediaID := store.MediaID(trimmedVariable(req, "mediaID"))
		if mediaID == "" {
			return nil, clientError("mediaID is required")
		}
		var position *int
		v := newVars(req)
		if value, ok := v.Number("position"); ok {
			p := int(value)
			position = &p
		}
		if err := v.Err(); err != nil {
			return nil, err
		}
		if err := h.store.QueueAdd(ctx, scope.ProfileID, mediaID, position, scope.LibraryFilter()); err != nil {
			return nil, clientError("unknown media")
		}
		items, err := h.queueItems(ctx, scope)
		if err != nil {
			return nil, err
		}
		data["queueadd"] = items
	}
	if fields["queueremove"] {
		mediaID := store.MediaID(trimmedVariable(req, "mediaID"))
		if mediaID == "" {
			return nil, clientError("mediaID is required")
		}
		if err := h.store.QueueRemove(ctx, scope.ProfileID, mediaID, scope.LibraryFilter()); err != nil {
			return nil, err
		}
		items, err := h.queueItems(ctx, scope)
		if err != nil {
			return nil, err
		}
		data["queueremove"] = items
	}
	if fields["queuemove"] {
		mediaID := store.MediaID(trimmedVariable(req, "mediaID"))
		if mediaID == "" {
			return nil, clientError("mediaID is required")
		}
		v := newVars(req)
		number, ok := v.Number("position")
		if err := v.Err(); err != nil {
			return nil, err
		}
		if !ok {
			return nil, clientError("position is required")
		}
		if err := h.store.QueueMove(ctx, scope.ProfileID, mediaID, int(number), scope.LibraryFilter()); err != nil {
			return nil, err
		}
		items, err := h.queueItems(ctx, scope)
		if err != nil {
			return nil, err
		}
		data["queuemove"] = items
	}
	if fields["queueclear"] {
		if err := h.store.QueueClear(ctx, scope.ProfileID, scope.LibraryFilter()); err != nil {
			return nil, err
		}
		items, err := h.queueItems(ctx, scope)
		if err != nil {
			return nil, err
		}
		data["queueclear"] = items
	}
	return data, nil
}

func (h *Handler) queueItems(ctx context.Context, scope auth.Scope) ([]SourceItem, error) {
	items, err := h.store.SourceItems(ctx, scope.LibraryFilter(), store.SourceQuery{
		Kind:      "queue",
		ProfileID: scope.ProfileID,
	})
	if err != nil {
		return nil, err
	}
	resp := make([]SourceItem, len(items))
	for i, item := range items {
		resp[i] = h.sourceItem(item, scope.Revealed)
	}
	return resp, nil
}

// IdentifyCandidate is the wire shape for one scraper search candidate
// (ADR-0003 §4: search → candidates → user confirms). Artwork carries
// display-only remote URLs; the server never downloads them — artwork
// application is out of scope for manual identification v1.
type IdentifyCandidate struct {
	ID      string            `json:"id"`
	Title   string            `json:"title"`
	Summary string            `json:"summary,omitempty"`
	Year    int               `json:"year,omitempty"`
	Artwork map[string]string `json:"artwork,omitempty"`
}

// MediaInfo is the wire shape for an identified Media row.
type MediaInfo struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Summary   string `json:"summary,omitempty"`
	Year      int    `json:"year,omitempty"`
	Favorited bool   `json:"favorited"`
}

func (h *Handler) mediaInfo(ctx context.Context, scope auth.Scope, media store.Media) (MediaInfo, error) {
	favorited, err := h.store.MediaFavorited(ctx, scope.ProfileID, media.ID)
	if err != nil {
		return MediaInfo{}, err
	}
	return MediaInfo{
		ID:        string(media.ID),
		Title:     media.Title,
		Summary:   media.Summary,
		Year:      media.Year,
		Favorited: favorited,
	}, nil
}

func (h *Handler) media(ctx context.Context, scope auth.Scope, req request) (MediaInfo, error) {
	mediaID := store.MediaID(trimmedVariable(req, "id"))
	if mediaID == "" {
		return MediaInfo{}, clientError("id is required")
	}
	media, err := h.store.Media(ctx, mediaID, scope.LibraryFilter())
	if err != nil {
		return MediaInfo{}, clientError("unknown media")
	}
	return h.mediaInfo(ctx, scope, media)
}

func (h *Handler) setFavorite(ctx context.Context, scope auth.Scope, req request) (MediaInfo, error) {
	mediaID := store.MediaID(trimmedVariable(req, "mediaID"))
	if mediaID == "" {
		return MediaInfo{}, clientError("mediaID is required")
	}
	v := newVars(req)
	favorite, ok := v.Bool("favorite")
	if err := v.Err(); err != nil {
		return MediaInfo{}, err
	}
	if !ok {
		return MediaInfo{}, clientError("favorite is required")
	}
	if err := h.store.SetMediaFavorite(ctx, scope.ProfileID, mediaID, favorite, scope.LibraryFilter()); err != nil {
		return MediaInfo{}, clientError("unknown media")
	}
	media, err := h.store.Media(ctx, mediaID, scope.LibraryFilter())
	if err != nil {
		return MediaInfo{}, clientError("unknown media")
	}
	return h.mediaInfo(ctx, scope, media)
}

// identifySearch runs a plugin scraper's search and returns the candidate
// pick list. The optional mediaID names the Media item being identified: it
// sources the scraper's fingerprint variables ({oshash}/{phash}/{duration},
// ADR-0027 D2 §7) and is reveal-gated like identifyApply — a hidden item's
// fingerprints must not leak into outbound queries. With a mediaID the query
// may be empty (fingerprint-driven search).
func (h *Handler) identifySearch(ctx context.Context, scope auth.Scope, req request) ([]IdentifyCandidate, error) {
	pluginID := trimmedVariable(req, "pluginID")
	scraperID := trimmedVariable(req, "scraperID")
	text := stringVar(req, "query")
	mediaID := trimmedVariable(req, "mediaID")
	if pluginID == "" || scraperID == "" {
		return nil, clientError("pluginID and scraperID are required")
	}
	if strings.TrimSpace(text) == "" && mediaID == "" {
		return nil, clientError("query or mediaID is required")
	}
	if mediaID != "" {
		if _, err := h.store.Media(ctx, store.MediaID(mediaID), scope.LibraryFilter()); err != nil {
			return nil, clientError("unknown media")
		}
	}
	candidates, err := h.identify.Search(ctx, pluginID, scraperID, text, store.MediaID(mediaID))
	if err != nil {
		return nil, err
	}
	resp := make([]IdentifyCandidate, len(candidates))
	for i, candidate := range candidates {
		resp[i] = IdentifyCandidate{
			ID:      candidate.ID,
			Title:   candidate.Title,
			Summary: candidate.Summary,
			Year:    candidate.Year,
			Artwork: candidate.Artwork,
		}
	}
	return resp, nil
}

// identifyApply applies a chosen candidate's lookup document to a Media item
// the session can currently see — the metadata write is reveal-gated the
// same way the read is (the store write itself is server-internal).
func (h *Handler) identifyApply(ctx context.Context, scope auth.Scope, req request) (MediaInfo, error) {
	mediaID := trimmedVariable(req, "mediaID")
	pluginID := trimmedVariable(req, "pluginID")
	scraperID := trimmedVariable(req, "scraperID")
	candidateID := trimmedVariable(req, "candidateID")
	if mediaID == "" || pluginID == "" || scraperID == "" || candidateID == "" {
		return MediaInfo{}, clientError("mediaID, pluginID, scraperID, and candidateID are required")
	}
	if _, err := h.store.Media(ctx, store.MediaID(mediaID), scope.LibraryFilter()); err != nil {
		return MediaInfo{}, clientError("unknown media")
	}
	if err := h.identify.Apply(ctx, store.MediaID(mediaID), pluginID, scraperID, candidateID); err != nil {
		return MediaInfo{}, err
	}
	media, err := h.store.Media(ctx, store.MediaID(mediaID), scope.LibraryFilter())
	if err != nil {
		return MediaInfo{}, clientError("unknown media")
	}
	return h.mediaInfo(ctx, scope, media)
}

// PluginInfo describes one activated plugin bundle for clients (the Manager's
// createLibrary picker).
type PluginInfo struct {
	ID          string              `json:"id"`
	DisplayName plugin.LocaleString `json:"displayName"`
	MediaLabel  plugin.LocaleString `json:"mediaLabel"`
	// MediaClassLabels overrides MediaLabel per value of the conventional
	// "class" Media field (locale maps, ADR-0034); omitted when the bundle
	// declares none — clients fall back to MediaLabel.
	MediaClassLabels map[string]plugin.LocaleString `json:"mediaClassLabels,omitempty"`
	// CertificationScale is the plugin's ordered certification scale (ADR-0028
	// §5), low→high. It is the value vocabulary the enforced-filter Console
	// screen offers as a managed Profile's ceiling; omitted when the plugin
	// declares no scale. The values are plugin data, never core domain vocab.
	CertificationScale []string `json:"certificationScale,omitempty"`
	libraryDefaults    plugin.LibraryDefaults
}

func (p PluginInfo) MarshalJSON() ([]byte, error) {
	type pluginInfo PluginInfo
	out := struct {
		pluginInfo
		LibraryDefaults *plugin.LibraryDefaults `json:"libraryDefaults,omitempty"`
	}{pluginInfo: pluginInfo(p)}
	if p.libraryDefaults.Restricted {
		out.LibraryDefaults = &p.libraryDefaults
	}
	return json.Marshal(out)
}

// pluginInfos lists the activated bundles in activation order.
func (h *Handler) pluginInfos() []PluginInfo {
	infos := []PluginInfo{}
	for _, id := range h.visiblePluginIDs() {
		bundle, ok := h.plugins.Bundle(id)
		if !ok {
			continue
		}
		infos = append(infos, PluginInfo{
			ID:                 string(bundle.ID),
			DisplayName:        bundle.DisplayName,
			MediaLabel:         bundle.MediaLabel,
			MediaClassLabels:   bundle.MediaClassLabels,
			CertificationScale: bundle.CertificationScale,
			libraryDefaults:    bundle.LibraryDefaults,
		})
	}
	return infos
}

// EntityFieldInfo is one declared field value, stringified per its type.
type EntityFieldInfo struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	// Label is the plugin-declared display label for the field (ADR-0034
	// locale map); omitted when the declaration carries none — clients fall
	// back to Name.
	Label plugin.LocaleString `json:"label,omitempty"`
}

// EntityInfo is the wire shape for one Entity instance.
type EntityInfo struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	// KindDisplayName is the plugin-declared display name for Kind (ADR-0034
	// locale map); omitted when the declaration carries none — clients fall
	// back to Kind.
	KindDisplayName plugin.LocaleString `json:"kindDisplayName,omitempty"`
	Fields          []EntityFieldInfo   `json:"fields"`
}

// RelatedEntityInfo pairs an Entity with the relation attributes that
// reached it (credit-style links, ADR-0003 §2).
type RelatedEntityInfo struct {
	EntityInfo
	Attributes map[string]string `json:"attributes"`
}

// entityQuery resolves entity(id) — reveal-filtered like every read.
func (h *Handler) entityQuery(ctx context.Context, scope auth.Scope, req request) (EntityInfo, error) {
	id := trimmedVariable(req, "id")
	if id == "" {
		return EntityInfo{}, clientError("id is required")
	}
	entity, err := h.store.Entity(ctx, store.EntityID(id), scope.LibraryFilter())
	if err != nil {
		return EntityInfo{}, clientError("unknown entity")
	}
	return entityInfo(entity, h.entityKindSpec(ctx, entity.LibraryID, entity.Kind)), nil
}

// entitiesForMedia resolves the Media item's credit-style relations.
func (h *Handler) entitiesForMedia(ctx context.Context, scope auth.Scope, req request) ([]RelatedEntityInfo, error) {
	mediaID := trimmedVariable(req, "mediaID")
	relation := trimmedVariable(req, "relation")
	if mediaID == "" || relation == "" {
		return nil, clientError("mediaID and relation are required")
	}
	related, err := h.store.EntitiesForMedia(ctx, store.MediaID(mediaID), relation, scope.LibraryFilter())
	if err != nil {
		return nil, clientError("unknown media")
	}
	resp := make([]RelatedEntityInfo, len(related))
	for i, item := range related {
		attributes := item.Attributes
		if attributes == nil {
			attributes = map[string]string{}
		}
		resp[i] = RelatedEntityInfo{
			EntityInfo: entityInfo(item.Entity, h.entityKindSpec(ctx, item.Entity.LibraryID, item.Entity.Kind)),
			Attributes: attributes,
		}
	}
	return resp, nil
}

// mediaFieldsQuery resolves mediaFields(mediaID) — the Media item's
// plugin-declared field values, stringified per type like EntityFieldInfo.
// Reveal-filtered like every read.
func (h *Handler) mediaFieldsQuery(ctx context.Context, scope auth.Scope, req request) ([]EntityFieldInfo, error) {
	mediaID := trimmedVariable(req, "mediaID")
	if mediaID == "" {
		return nil, clientError("mediaID is required")
	}
	fields, err := h.store.MediaFields(ctx, store.MediaID(mediaID), scope.LibraryFilter())
	if err != nil {
		return nil, clientError("unknown media")
	}
	specs := h.mediaFieldSpecs(ctx, store.MediaID(mediaID))
	infos := []EntityFieldInfo{}
	for _, field := range fields {
		spec := specs[field.Name]
		if spec.Sensitive {
			continue
		}
		infos = append(infos, EntityFieldInfo{
			Name:  field.Name,
			Type:  string(field.Type),
			Value: fieldValueString(field.Type, field.Text, field.Number, field.Flag, field.Date),
			Label: spec.Label,
		})
	}
	return infos, nil
}

// mediaFieldSpecs maps a Media item's declared MediaFields to the owning
// bundle declaration. Empty when the item is plugin-less or undeclared.
func (h *Handler) mediaFieldSpecs(ctx context.Context, mediaID store.MediaID) map[string]plugin.FieldSpec {
	specs := map[string]plugin.FieldSpec{}
	libraryID, err := h.store.MediaLibrary(ctx, mediaID)
	if err != nil {
		return specs
	}
	pluginID, err := h.store.LibraryPluginID(ctx, libraryID)
	if err != nil || pluginID == "" {
		return specs
	}
	bundle, ok := h.plugins.Bundle(plugin.ID(pluginID))
	if !ok {
		return specs
	}
	for _, field := range bundle.MediaFields {
		specs[field.Name] = field
	}
	return specs
}

type ArtworkCandidateInfo struct {
	ID         string `json:"id"`
	Slot       string `json:"slot"`
	Applied    bool   `json:"applied"`
	PluginID   string `json:"pluginID,omitempty"`
	ScraperID  string `json:"scraperID,omitempty"`
	PreviewURL string `json:"previewURL,omitempty"`
}

func (h *Handler) artworkCandidates(ctx context.Context, scope auth.Scope, req request) ([]ArtworkCandidateInfo, error) {
	mediaID := store.MediaID(trimmedVariable(req, "mediaID"))
	slot := trimmedVariable(req, "slot")
	if mediaID == "" || slot == "" {
		return nil, clientError("mediaID and slot are required")
	}
	if !artwork.ValidSlot(slot) {
		return nil, clientError("unknown artwork slot")
	}
	candidates, err := h.store.ArtworkCandidates(ctx, mediaID, slot, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	resp := make([]ArtworkCandidateInfo, len(candidates))
	for i, candidate := range candidates {
		info, err := h.artworkCandidateInfo(ctx, scope, candidate)
		if err != nil {
			return nil, err
		}
		resp[i] = info
	}
	return resp, nil
}

func (h *Handler) pinArtworkCandidate(ctx context.Context, scope auth.Scope, req request) (ArtworkCandidateInfo, error) {
	mediaID := store.MediaID(trimmedVariable(req, "mediaID"))
	slot := trimmedVariable(req, "slot")
	candidateID := trimmedVariable(req, "candidateID")
	if mediaID == "" || slot == "" || candidateID == "" {
		return ArtworkCandidateInfo{}, clientError("mediaID, slot, and candidateID are required")
	}
	if !artwork.ValidSlot(slot) {
		return ArtworkCandidateInfo{}, clientError("unknown artwork candidate")
	}
	if h.candidateFetch == nil || h.candidateImport == nil {
		return ArtworkCandidateInfo{}, clientError("artwork candidate pin unavailable")
	}
	candidate, err := h.store.ArtworkCandidate(ctx, candidateID, scope.LibraryFilter())
	if err != nil || candidate.MediaID != mediaID || candidate.Slot != slot {
		return ArtworkCandidateInfo{}, clientError("unknown artwork candidate")
	}
	media, err := h.store.Media(ctx, candidate.MediaID, scope.LibraryFilter())
	if err != nil {
		return ArtworkCandidateInfo{}, clientError("unknown artwork candidate")
	}
	data, err := h.candidateFetch.FetchResource(ctx, candidate.SourceURL)
	if err != nil {
		return ArtworkCandidateInfo{}, err
	}
	if err := h.candidateImport.ImportArtwork(ctx, candidate.MediaID, candidate.Slot, data); err != nil {
		return ArtworkCandidateInfo{}, err
	}
	if err := h.store.MarkArtworkCandidateApplied(ctx, candidate.ID); err != nil {
		return ArtworkCandidateInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityArtworkPin, "media", string(candidate.MediaID), media.LibraryID); err != nil {
		return ArtworkCandidateInfo{}, err
	}
	candidate.Applied = true
	return h.artworkCandidateInfo(ctx, scope, candidate)
}

func (h *Handler) artworkCandidateInfo(ctx context.Context, scope auth.Scope, candidate store.ArtworkCandidate) (ArtworkCandidateInfo, error) {
	info := ArtworkCandidateInfo{
		ID:        candidate.ID,
		Slot:      candidate.Slot,
		Applied:   candidate.Applied,
		PluginID:  candidate.PluginID,
		ScraperID: candidate.ScraperID,
	}
	if h.tokens == nil || scope.SessionID == "" || scope.DeviceID == "" || scope.ProfileID == "" {
		return info, nil
	}
	media, err := h.store.Media(ctx, candidate.MediaID, scope.LibraryFilter())
	if err != nil {
		return ArtworkCandidateInfo{}, clientError("unknown artwork candidate")
	}
	restricted, err := h.store.LibraryRestricted(ctx, media.LibraryID)
	if err != nil {
		return ArtworkCandidateInfo{}, err
	}
	now := time.Now().UTC()
	expires := now.Add(15 * time.Minute)
	if !scope.ExpiresAt.IsZero() && scope.ExpiresAt.Before(expires) {
		expires = scope.ExpiresAt
	}
	token, err := h.tokens.Issue(ctx, streamtoken.Grant{
		Target:     streamtoken.TargetArtworkCandidate,
		MediaID:    candidate.MediaID,
		DeviceID:   scope.DeviceID,
		SessionID:  scope.SessionID,
		ProfileID:  scope.ProfileID,
		Restricted: restricted,
		ExpiresAt:  expires,
	})
	if err != nil {
		return ArtworkCandidateInfo{}, err
	}
	info.PreviewURL = "/api/media/artwork-candidate/" + token + "/" + candidate.ID
	return info, nil
}

// MarkerInfo is the wire shape for one Marker: a named
// point or range on one Media item's canonical timeline. endSeconds is
// absent for point markers; segmentKind is from the closed skip-segment
// vocabulary (clients ignore unknown kinds); tags are free-form.
type MarkerInfo struct {
	ID           string   `json:"id"`
	MediaID      string   `json:"mediaID"`
	Title        string   `json:"title,omitempty"`
	StartSeconds float64  `json:"startSeconds"`
	EndSeconds   *float64 `json:"endSeconds,omitempty"`
	SegmentKind  string   `json:"segmentKind,omitempty"`
	Tags         []string `json:"tags"`
	TrashedAt    string   `json:"trashedAt,omitempty"`
}

func markerInfo(marker store.Marker) MarkerInfo {
	tags := marker.Tags
	if tags == nil {
		tags = []string{}
	}
	return MarkerInfo{
		ID:           string(marker.ID),
		MediaID:      string(marker.MediaID),
		Title:        marker.Title,
		StartSeconds: marker.StartSeconds,
		EndSeconds:   marker.EndSeconds,
		SegmentKind:  marker.SegmentKind,
		Tags:         tags,
		TrashedAt:    formatOptionalTime(marker.TrashedAt),
	}
}

// markersQuery resolves markers(mediaID) — the item's Markers ordered by
// start time. Reveal-filtered like every read; viewer-allowed (clients need
// markers to render skip affordances and timeline chapters).
func (h *Handler) markersQuery(ctx context.Context, scope auth.Scope, req request) ([]MarkerInfo, error) {
	mediaID := trimmedVariable(req, "mediaID")
	if mediaID == "" {
		return nil, clientError("mediaID is required")
	}
	markers, err := h.store.MarkersForMedia(ctx, store.MediaID(mediaID), scope.LibraryFilter())
	if err != nil {
		return nil, clientError("unknown media")
	}
	infos := make([]MarkerInfo, len(markers))
	for i, marker := range markers {
		infos[i] = markerInfo(marker)
	}
	return infos, nil
}

// trashedMarkersQuery resolves trashedMarkers(mediaID) for the manager's
// restore drawer. Normal playback/editor reads use markers(mediaID), which
// remains live-only.
func (h *Handler) trashedMarkersQuery(ctx context.Context, scope auth.Scope, req request) ([]MarkerInfo, error) {
	mediaID := trimmedVariable(req, "mediaID")
	if mediaID == "" {
		return nil, clientError("mediaID is required")
	}
	markers, err := h.store.TrashedMarkersForMedia(ctx, store.MediaID(mediaID), scope.LibraryFilter())
	if err != nil {
		return nil, clientError("unknown media")
	}
	infos := make([]MarkerInfo, len(markers))
	for i, marker := range markers {
		infos[i] = markerInfo(marker)
	}
	return infos, nil
}

// markerFromVariables reads the shared create/update marker variables.
// Validation (start >= 0, end > start, segment kind in vocabulary, at least
// one display handle) lives in the store write.
func markerFromVariables(req request) (store.Marker, error) {
	var marker store.Marker
	v := newVars(req)
	if title, ok := v.String("title"); ok {
		marker.Title = title
	}
	if start, ok := v.Number("startSeconds"); ok {
		marker.StartSeconds = start
	}
	if end, ok := v.Number("endSeconds"); ok {
		marker.EndSeconds = &end
	}
	if kind, ok := v.String("segmentKind"); ok {
		marker.SegmentKind = strings.TrimSpace(kind)
	}
	if tags, ok := v.StringList("tags", "tags"); ok {
		marker.Tags = tags
	}
	if err := v.Err(); err != nil {
		return store.Marker{}, err
	}
	return marker, nil
}

// createMarker inserts a Marker on a Media item the manager can see
// (manager-gated in execute; the store write is reveal-gated too).
func (h *Handler) createMarker(ctx context.Context, scope auth.Scope, req request) (MarkerInfo, error) {
	mediaID := trimmedVariable(req, "mediaID")
	if mediaID == "" {
		return MarkerInfo{}, clientError("mediaID is required")
	}
	marker, err := markerFromVariables(req)
	if err != nil {
		return MarkerInfo{}, err
	}
	marker.MediaID = store.MediaID(mediaID)
	marker.CreatedByProfile = scope.ProfileID // ADR-0036: stamp authorship so My-Data export/disown is live
	created, err := h.store.CreateMarker(ctx, marker, scope.LibraryFilter(), time.Now().UTC())
	if err != nil {
		return MarkerInfo{}, err
	}
	if h.assetSweep != nil {
		h.assetSweep(ctx)
	}
	return markerInfo(created), nil
}

// updateMarker fully replaces a visible Marker's mutable fields.
func (h *Handler) updateMarker(ctx context.Context, scope auth.Scope, req request) (MarkerInfo, error) {
	markerID := trimmedVariable(req, "markerID")
	if markerID == "" {
		return MarkerInfo{}, clientError("markerID is required")
	}
	marker, err := markerFromVariables(req)
	if err != nil {
		return MarkerInfo{}, err
	}
	marker.ID = store.MarkerID(markerID)
	updated, err := h.store.UpdateMarker(ctx, marker, scope.LibraryFilter(), time.Now().UTC())
	if errors.Is(err, store.ErrMarkerNotFound) {
		return MarkerInfo{}, clientError(err.Error())
	}
	if err != nil {
		return MarkerInfo{}, err
	}
	if h.assetSweep != nil {
		h.assetSweep(ctx)
	}
	return markerInfo(updated), nil
}

// deleteMarker soft-deletes a visible Marker and returns the removed row.
func (h *Handler) deleteMarker(ctx context.Context, scope auth.Scope, req request) (MarkerInfo, error) {
	markerID := trimmedVariable(req, "markerID")
	if markerID == "" {
		return MarkerInfo{}, clientError("markerID is required")
	}
	removed, err := h.store.DeleteMarker(ctx, store.MarkerID(markerID), scope.LibraryFilter())
	if err != nil {
		return MarkerInfo{}, clientError("unknown marker")
	}
	if h.assetSweep != nil {
		h.assetSweep(ctx)
	}
	if err := h.logActivity(ctx, scope, store.ActivityMarkerDelete, "marker", string(removed.ID), ""); err != nil {
		return MarkerInfo{}, err
	}
	return markerInfo(removed), nil
}

// restoreMarker restores a soft-deleted visible Marker.
func (h *Handler) restoreMarker(ctx context.Context, scope auth.Scope, req request) (MarkerInfo, error) {
	markerID := trimmedVariable(req, "markerID")
	if markerID == "" {
		return MarkerInfo{}, clientError("markerID is required")
	}
	restored, err := h.store.RestoreMarker(ctx, store.MarkerID(markerID), scope.LibraryFilter(), time.Now().UTC())
	if err != nil {
		return MarkerInfo{}, clientError("unknown marker")
	}
	if h.assetSweep != nil {
		h.assetSweep(ctx)
	}
	return markerInfo(restored), nil
}

// fieldValueString stringifies one typed field value for the wire.
func fieldValueString(fieldType store.EntityFieldType, text string, number float64, flag bool, date time.Time) string {
	switch fieldType {
	case store.EntityFieldText, store.EntityFieldURL:
		return text
	case store.EntityFieldNumber:
		return strconv.FormatFloat(number, 'f', -1, 64)
	case store.EntityFieldFlag:
		return strconv.FormatBool(flag)
	case store.EntityFieldDate:
		return date.UTC().Format(time.RFC3339)
	}
	return ""
}

// entityKindSpec resolves the declaration behind one stored Entity: the
// entity's Library names the bundle, the bundle declares the kind. The zero
// EntityKind (no display strings) when the Library is plugin-less, the
// registry is absent, or the kind is undeclared — instance reads still work,
// clients fall back to the schema names.
func (h *Handler) entityKindSpec(ctx context.Context, libraryID store.LibraryID, kindName string) plugin.EntityKind {
	pluginID, err := h.store.LibraryPluginID(ctx, libraryID)
	if err != nil || pluginID == "" {
		return plugin.EntityKind{}
	}
	bundle, ok := h.plugins.Bundle(plugin.ID(pluginID))
	if !ok {
		return plugin.EntityKind{}
	}
	for _, kind := range bundle.EntityKinds {
		if kind.Name == kindName {
			return kind
		}
	}
	return plugin.EntityKind{}
}

// entityInfo builds the wire shape for one Entity, decorated with the display
// strings its kind declaration carries: kind display name plus
// per-field labels, matched by schema name.
func entityInfo(entity store.Entity, kind plugin.EntityKind) EntityInfo {
	info := EntityInfo{
		ID:              string(entity.ID),
		Kind:            entity.Kind,
		Title:           entity.Title,
		KindDisplayName: kind.DisplayName,
		Fields:          []EntityFieldInfo{},
	}
	specs := map[string]plugin.FieldSpec{}
	for _, field := range kind.Fields {
		specs[field.Name] = field
	}
	for _, field := range entity.Fields {
		spec := specs[field.Name]
		if spec.Sensitive {
			continue
		}
		info.Fields = append(info.Fields, EntityFieldInfo{
			Name:  field.Name,
			Type:  string(field.Type),
			Value: fieldValueString(field.Type, field.Text, field.Number, field.Flag, field.Date),
			Label: spec.Label,
		})
	}
	return info
}

// mediaForEntity resolves the Media items related to one Entity — the
// detail-page item list. Reveal-filtered twice over: the Entity must be
// visible to the session, and each related item's Library is filtered again.
func (h *Handler) mediaForEntity(ctx context.Context, scope auth.Scope, req request) ([]SourceItem, error) {
	entityID := trimmedVariable(req, "entityID")
	if entityID == "" {
		return nil, clientError("entityID is required")
	}
	items, err := h.store.MediaForEntity(ctx, store.EntityID(entityID), scope.LibraryFilter())
	if err != nil {
		return nil, clientError("unknown entity")
	}
	// Direct navigation reaches muted Media on purpose (§2.2) and badges them (§2.7).
	if err := h.store.AnnotateMuted(ctx, scope.ProfileID, scope.LibraryFilter(), items); err != nil {
		return nil, err
	}
	resp := make([]SourceItem, len(items))
	for i, item := range items {
		resp[i] = h.sourceItem(item, scope.Revealed)
	}
	return resp, nil
}

// ClientPrefInfo is the wire shape for one per-Profile client preference
// (competitor-gap A23): namespaced key, opaque value the server never
// interprets, and the server-stamped update time.
type ClientPrefInfo struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt string `json:"updatedAt"`
}

// setClientPref stores (or, with an empty value, clears) one display
// preference for the session's Profile. Viewer-allowed by design: these are
// the Profile's own client display prefs, not catalog mutations.
func (h *Handler) setClientPref(ctx context.Context, scope auth.Scope, req request) (ClientPrefInfo, error) {
	key := stringVar(req, "key")
	value := stringVar(req, "value")
	now := time.Now().UTC()
	if err := h.store.SetClientPref(ctx, scope.ProfileID, key, value, now); err != nil {
		return ClientPrefInfo{}, err
	}
	return ClientPrefInfo{
		Key:       strings.TrimSpace(key),
		Value:     value,
		UpdatedAt: now.Format(time.RFC3339),
	}, nil
}

// clientPrefs lists every stored preference for the session's Profile.
func (h *Handler) clientPrefs(ctx context.Context, scope auth.Scope) ([]ClientPrefInfo, error) {
	prefs, err := h.store.ClientPrefs(ctx, scope.ProfileID)
	if err != nil {
		return nil, err
	}
	infos := make([]ClientPrefInfo, len(prefs))
	for i, pref := range prefs {
		infos[i] = ClientPrefInfo{
			Key:       pref.Key,
			Value:     pref.Value,
			UpdatedAt: pref.UpdatedAt.Format(time.RFC3339),
		}
	}
	return infos, nil
}

// resumePolicy resolves the declarative resume eligibility for one Media item:
// the class default, overridden by the owning Library's plugin
// engagement posture when one is declared.
func (h *Handler) resumePolicy(ctx context.Context, media store.Media) store.ResumePolicy {
	policy := store.ResumePolicy{Class: media.Class}
	pluginID, err := h.store.LibraryPluginID(ctx, media.LibraryID)
	if err != nil || pluginID == "" {
		return policy
	}
	if bundle, ok := h.plugins.Bundle(plugin.ID(pluginID)); ok {
		policy.Override = bundle.Engagement.ResumeMedia
	}
	return policy
}

// redactedJobError replaces operational text withheld from unrevealed sessions.
const redactedJobError = "error withheld — reveal required"

func (h *Handler) jobInfo(ctx context.Context, scope auth.Scope, job store.Job) JobInfo {
	info := JobInfo{
		ID:         string(job.ID),
		Kind:       string(job.Kind),
		Class:      string(job.Class),
		Status:     string(job.Status),
		Progress:   jobProgressInfo(job.Progress),
		Attempts:   job.Attempts,
		CreatedAt:  formatOptionalTime(job.CreatedAt),
		UpdatedAt:  formatOptionalTime(job.UpdatedAt),
		StartedAt:  formatOptionalTime(job.StartedAt),
		FinishedAt: formatOptionalTime(job.FinishedAt),
		Error:      job.Error,
	}
	if h.jobNeedsRedaction(ctx, scope, job) {
		if info.Error != "" {
			info.Error = redactedJobError
		}
		info.Progress.Message = ""
	}
	return info
}

// jobNeedsRedaction applies the Reveal contract to the operational job read:
// scan errors and progress strings can embed filesystem paths under restricted
// Libraries. Unrevealed sessions get full text only for jobs whose target
// Library is known and unrestricted; restricted or unknown targets are
// withheld until reveal.
func (h *Handler) jobNeedsRedaction(ctx context.Context, scope auth.Scope, job store.Job) bool {
	if scope.Revealed {
		return false
	}
	var payload struct {
		LibraryID string `json:"library_id"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.LibraryID == "" {
		return true
	}
	restricted, err := h.store.LibraryRestricted(ctx, store.LibraryID(payload.LibraryID))
	if err != nil {
		return true
	}
	return restricted
}

func jobProgressInfo(progress store.JobProgress) JobProgressInfo {
	return JobProgressInfo{
		Current: progress.Current,
		Total:   progress.Total,
		Unit:    progress.Unit,
		Message: progress.Message,
	}
}

type ScheduleTriggerInfo struct {
	Kind    string `json:"kind"`
	N       int    `json:"n"`
	Unit    string `json:"unit"`
	Weekday int    `json:"weekday"`
	Hour    int    `json:"hour"`
	Minute  int    `json:"minute"`
}

type ScheduleInfo struct {
	ID           string              `json:"id"`
	JobKind      string              `json:"jobKind"`
	Trigger      ScheduleTriggerInfo `json:"trigger"`
	Enabled      bool                `json:"enabled"`
	Orphaned     bool                `json:"orphaned"`
	NextDue      string              `json:"nextDue"`
	LastEnqueued string              `json:"lastEnqueued"`
}

func (h *Handler) schedules(ctx context.Context) ([]ScheduleInfo, error) {
	rows, err := h.store.ListSchedules(ctx)
	if err != nil {
		return nil, err
	}
	infos := make([]ScheduleInfo, len(rows))
	for i, row := range rows {
		infos[i] = scheduleInfo(row)
	}
	return infos, nil
}

func (h *Handler) updateSchedule(ctx context.Context, scope auth.Scope, req request) (ScheduleInfo, error) {
	id := trimmedVariable(req, "id")
	if id == "" {
		return ScheduleInfo{}, clientError("id is required")
	}
	trigger, hasTrigger, err := scheduleTriggerFromVariables(req)
	if err != nil {
		return ScheduleInfo{}, err
	}
	v := newVars(req)
	enabled, hasEnabled := v.Bool("enabled")
	if err := v.Err(); err != nil {
		return ScheduleInfo{}, err
	}
	if !hasTrigger && !hasEnabled {
		return ScheduleInfo{}, clientError("trigger or enabled is required")
	}
	var enabledPtr *bool
	if hasEnabled {
		enabledPtr = &enabled
	}
	var updated store.Schedule
	if h.scheduleUpdater != nil {
		updated, err = h.scheduleUpdater.Update(ctx, store.ScheduleID(id), trigger, enabledPtr)
	} else {
		updated, err = h.store.UpdateSchedule(ctx, store.ScheduleID(id), trigger, enabledPtr)
	}
	if err != nil {
		return ScheduleInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityScheduleUpdate, "schedule", id, ""); err != nil {
		return ScheduleInfo{}, err
	}
	return scheduleInfo(updated), nil
}

func scheduleInfo(row store.Schedule) ScheduleInfo {
	nextDue := schedulepkg.NextDue(row.LastCompletedEnqueue, row.Trigger, time.Local)
	return ScheduleInfo{
		ID:           string(row.ID),
		JobKind:      string(row.JobKind),
		Trigger:      scheduleTriggerInfo(row.Trigger),
		Enabled:      row.Enabled,
		Orphaned:     row.Orphaned,
		NextDue:      formatOptionalTime(nextDue),
		LastEnqueued: formatOptionalTime(row.LastCompletedEnqueue),
	}
}

func scheduleTriggerInfo(trigger store.ScheduleTrigger) ScheduleTriggerInfo {
	return ScheduleTriggerInfo{
		Kind:    string(trigger.Kind),
		N:       trigger.N,
		Unit:    string(trigger.Unit),
		Weekday: trigger.Weekday,
		Hour:    trigger.Hour,
		Minute:  trigger.Minute,
	}
}

func scheduleTriggerFromVariables(req request) (*store.ScheduleTrigger, bool, error) {
	value, ok := req.Variables["trigger"]
	if !ok || value == nil {
		return nil, false, nil
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, false, clientError("trigger must be an object")
	}
	kind, ok := raw["kind"].(string)
	if !ok || strings.TrimSpace(kind) == "" {
		return nil, false, clientError("trigger kind is required")
	}
	trigger := store.ScheduleTrigger{Kind: store.ScheduleTriggerKind(strings.TrimSpace(kind))}
	switch trigger.Kind {
	case store.ScheduleTriggerEvery:
		n, hasN := numberField(raw, "n")
		unit, hasUnit := raw["unit"].(string)
		if !hasN || !hasUnit || strings.TrimSpace(unit) == "" {
			return nil, false, clientError("every trigger requires n and unit")
		}
		trigger.N = int(n)
		trigger.Unit = store.ScheduleEveryUnit(strings.TrimSpace(unit))
	case store.ScheduleTriggerDaily:
		hour, hasHour := numberField(raw, "hour")
		minute, hasMinute := numberField(raw, "minute")
		if !hasHour || !hasMinute {
			return nil, false, clientError("daily trigger requires hour and minute")
		}
		trigger.Hour = int(hour)
		trigger.Minute = int(minute)
	case store.ScheduleTriggerWeekly:
		weekday, hasWeekday := numberField(raw, "weekday")
		hour, hasHour := numberField(raw, "hour")
		minute, hasMinute := numberField(raw, "minute")
		if !hasWeekday || !hasHour || !hasMinute {
			return nil, false, clientError("weekly trigger requires weekday, hour, and minute")
		}
		trigger.Weekday = int(weekday)
		trigger.Hour = int(hour)
		trigger.Minute = int(minute)
	default:
		return nil, false, clientError("trigger kind must be every, daily, or weekly")
	}
	if err := store.ValidateScheduleTrigger(trigger); err != nil {
		return nil, false, err
	}
	return &trigger, true, nil
}

func numberField(raw map[string]any, name string) (float64, bool) {
	number, ok := raw[name].(float64)
	return number, ok
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

// queryFields and mutationFields together form the registry of top-level
// fields that need an authenticated session scope before execute runs. The
// split classifies each scoped field by effect: queryFields are pure reads
// (including dry-run previews and computed status); mutationFields write or
// command state. Keys are lowercased, matching topLevelFields normalization.
//
// scopedFields below is the union of the two and is the only set requiresScope
// consults — so the auth gate is byte-for-byte identical to the pre-split map.
// MutationFields() exports the mutation half for the action-conformance gate.

// queryFields are the scoped top-level fields that only read state.
var queryFields = map[string]bool{
	"connectionendpoints":    true,
	"navigationmanifest":     true,
	"manifest":               true,
	"plugins":                true,
	"playbackrenditioncache": true,
	"entity":                 true,
	"entitiesformedia":       true,
	"entityrelations":        true,
	"mediaforentity":         true,
	"mediafiles":             true,
	"mediarelations":         true,
	"mediafields":            true,
	"artworkcandidates":      true,
	"markers":                true,
	"trashedmarkers":         true,
	"sourceitems":            true,
	"ambientqueue":           true,
	"currentsession":         true,
	"profiles":               true,
	"search":                 true,
	"searchresult":           true,
	"semanticcapability":     true,
	"searchentities":         true,
	"searchfeeds":            true,
	"media":                  true,
	"trashlist":              true,
	"tags":                   true,
	"tagassignments":         true,
	"libraries":              true,
	"qualitypolicy":          true,
	"encryptedlibrarystatus": true, // ENC-2 (ADR-0045 §2.4): credential-free Sealed/Unsealed + mode read
	"accessschedule":         true,
	"clientprefs":            true,
	"identifysearch":         true,
	"identifylookup":         true, // METAPARITY-4 (ADR-0068 §A): paste-an-id direct fetch, read-only
	"fixmatchcandidates":     true, // METAPARITY-4 (ADR-0068 §A): scored candidate list, read-only
	"fixmatchpreview":        true, // METAPARITY-4 (ADR-0068 §A): preview-before-apply diff, writes nothing
	"unmatchedqueue":         true,
	"unmatchedrecords":       true, // METAPARITY-6 (ADR-0068 §A): the Media-independent review inbox
	"unmatchedsuppressions":  true, // METAPARITY-7 (ADR-0068 §A): active scoped suppressions + provenance
	"navoverride":            true, // WEB-5J (ADR-0061 §Layout editor): current nav/layout override blob
	"navoverridehistory":     true, // WEB-5J (ADR-0061 §Layout editor): audit log of prior overrides
	"subscriptions":          true,
	"jobs":                   true,
	"job":                    true,
	"jobevents":              true,
	"operationsgovernor":     true,
	"schedules":              true,
	"backuphistory":          true,
	"apitokens":              true,
	"adminsession":           true,
	"apikeys":                true,
	"activitylog":            true,
	"activity":               true, // ATEST-26: singular by-ID activity entry for E2E audit assertions
	"myengagement":           true,
	"mydatasummary":          true,
	"myengagementlog":        true,
	"mysessions":             true,
	"scrobblelinks":          true, // MUSIC-7 (ADR-0098 area 7): own Profile's scrobble links (non-secret)
	"scrobbleeligibility":    true, // MUSIC-7 (ADR-0098 area 7): per-visible-Library scrobble eligibility
	"scrobblestatus":         true, // MUSIC-7 (ADR-0098 area 7): admin diagnostic (dead-letter/skip/adapters)
	"offlinepacks":           true, // MUSIC-6 (ADR-0098 area 6): own Offline Packs + their fetch tokens
	"mutes":                  true, // ENR-4 (ADR-0054 §2.8): own self-authored mute list
	"engagementcounter":      true, // ADULT-1 (ADR-0027 D1): own per-Profile engagement counter (count + history)
	"profilesessions":        true,
	"libraryhealth":          true,
	"libraryhealthitems":     true,
	"libraryduplicategroups": true,
	"metadataexport":         true,
	"libraryindexstatus":     true,
	"pendingaitagqueue":      true,
	// ENR-7 (ADR-0048 Decision 3/6): operator read of the per-Library
	// skip-detection toggle (resolver in graph_skipdetect.go).
	"skipdetectionenabled": true,
	"notifications":        true,
	"extensionrequest":     true,
	// ADR-0113 §2.2: per-title access request reads.
	"accessrequest":         true,
	"pendingaccessrequests": true,
	// ADR-0113 §2.3: per-title access grant read.
	"accessgrantsforprofile": true,
	"remoteaccess":           true,
	"watcherstatus":          true,
	"mediafieldlocks":        true,
	"consoleconfig":          true,
	"pluginworker":           true,
	"updatestatus":           true,
	"transcodeconfig":        true,
	"livesessions":           true,
	"devices":                true,
	"setupstate":             true,
	"importers":              true,
	"importstatus":           true,
	"scanestimate":           true,
	"savedfilters":           true,
	"collections":            true,
	"smartcollections":       true,
	"channels":               true, // ADR-0158 CHAN-1: Library Channels (per-Profile + server-shared)
	"retentionrules":         true, // ADR-0051 RET-2; admin-gated (see adminFields)
	// Stations (MUSIC-5, ADR-0098 area 5): saved-Station reads + the read-only
	// generation preview (computes a rolling window, mutates nothing).
	"stations":        true,
	"stationgenerate": true,
	// Manage curation helpers: bounded reads and dry-run previews.
	"foldertree":      true,
	"bulkeditpreview": true,
	"organizepreview": true,
	// Setup folder picker: manager-gated, non-Library-scoped server
	// filesystem browse for the first-run wizard's root field.
	"browseserverfolders": true,
	"pathpicker":          true,
}

// mutationFields are the scoped top-level fields that write or command state.
var mutationFields = map[string]bool{
	"switchprofile":                true,
	"precomputeplaybackrenditions": true,
	"evictplaybackrenditioncache":  true,
	"reveal":                       true,
	"relock":                       true,
	"setrevealpin":                 true,
	"reportplayback":               true,
	"marknotificationread":         true,
	"registerdevicepushtoken":      true,
	"requestextension":             true,
	"respondextension":             true,
	"setextensionpreauthorization": true,
	// ADR-0113 §2.2: per-title access request mutations.
	"requestaccess":        true,
	"resolveaccessrequest": true,
	// ADR-0113 §2.3: per-title access grant mutation.
	"revokeaccessgrant":                  true,
	"setfavorite":                        true,
	"queueadd":                           true,
	"queueremove":                        true,
	"queuemove":                          true,
	"queueclear":                         true,
	"stationstart":                       true, // MUSIC-5 (ADR-0098 area 5): materialize a Station window into the Play Queue (own Profile)
	"stationsave":                        true, // MUSIC-5 (ADR-0098 area 5): persist a saved Station (own Profile)
	"stationdelete":                      true, // MUSIC-5 (ADR-0098 area 5): delete a saved Station (own Profile)
	"linkscrobble":                       true, // SCROBBLE-1 (ADR-0049 §2.2): link own Profile to a scrobble service
	"unlinkscrobble":                     true, // SCROBBLE-1 (ADR-0049 §2.9): unlink own Profile from a scrobble service
	"setclientpref":                      true,
	"setfollow":                          true,
	"associatesubscriptionentry":         true,
	"setmute":                            true, // ENR-4 (ADR-0054 §2.8): set "Not interested" on an Entity/Tag (own Profile)
	"clearmute":                          true, // ENR-4 (ADR-0054 §2.8): unmute one target (own Profile)
	"clearallmutes":                      true, // ENR-4 (ADR-0054 §2.8): My-Data clear-all reset (own Profile)
	"recordengagement":                   true, // ADULT-1 (ADR-0027 D1): increment a per-Profile engagement counter (own Profile)
	"removelastengagement":               true, // ADULT-1 (ADR-0027 D1): remove-last correction (compensating event; own Profile)
	"deletemyengagement":                 true,
	"syncofflinepackprogress":            true,
	"operatordeleteprofiledata":          true,
	"transferprofileowner":               true,
	"createmarker":                       true,
	"updatemarker":                       true,
	"deletemarker":                       true,
	"restoremarker":                      true,
	"pinartworkcandidate":                true,
	"trashmedia":                         true,
	"restoremedia":                       true,
	"createtag":                          true,
	"updatetag":                          true,
	"deletetag":                          true,
	"restoretag":                         true,
	"assigntag":                          true,
	"unassigntag":                        true,
	"bulkassigntags":                     true,
	"purgetrash":                         true,
	"setmediafieldlock":                  true,
	"bulkeditapply":                      true,
	"registerimportsource":               true, // admin-gated (see adminFields); apple ADR-0003 §3
	"importmanagedfile":                  true, // admin-gated (see adminFields); apple ADR-0003 §1
	"createmanagedfolder":                true,
	"renamemanagedpath":                  true,
	"batchrenamemanagedpaths":            true,
	"movemanagedpath":                    true,
	"deletemanagedpath":                  true,
	"restoremanagedpath":                 true,
	"organizeapply":                      true,
	"mergemedia":                         true,
	"mergeentities":                      true,
	"upsertentityrelation":               true,
	"linkmediarelation":                  true,
	"undobatch":                          true,
	"identifyapply":                      true,
	"fixmatch":                           true,
	"dismissunmatched":                   true,
	"createmediafromunmatched":           true, // METAPARITY-6 (ADR-0068 §A): hand-authored no-match Media
	"suppressunmatched":                  true, // METAPARITY-7 (ADR-0068 §A): one scoped suppression
	"resetunmatchedsuppression":          true, // METAPARITY-7 (ADR-0068 §A): lift one scope, siblings untouched
	"addmediaexternalid":                 true,
	"setnavoverride":                     true, // WEB-5J (ADR-0061 §Layout editor): apply partial override patch
	"resetnavoverride":                   true, // WEB-5J (ADR-0061 §Layout editor): clear all overrides
	"restorenavoverride":                 true, // WEB-5J (ADR-0061 §Layout editor): restore a history entry
	"createprofile":                      true,
	"updateprofile":                      true,
	"setprofilelibraries":                true,
	"setcertificationceiling":            true,
	"setaccessschedule":                  true,
	"createlibrary":                      true,
	"setlibraryroots":                    true,
	"addpathpickerroot":                  true,
	"setlibrarypreviewclips":             true,
	"setlibrarybacking":                  true,
	"setlibraryrestricted":               true,
	"setqualitypolicy":                   true,
	"deletelibrary":                      true,
	"scanlibrary":                        true,
	"addsubscription":                    true,
	"removesubscription":                 true,
	"setsubscriptionretention":           true,
	"runjob":                             true,
	"runeventscompaction":                true,
	"jobcancel":                          true,
	"jobretry":                           true,
	"updateschedule":                     true,
	"backup":                             true,
	"verifybackup":                       true,
	"runimport":                          true,
	"verifyadminsession":                 true,
	"endadminsession":                    true,
	"createapikey":                       true,
	"revokeapikey":                       true,
	"createapitoken":                     true,
	"revokeapitoken":                     true,
	"setconsoleconfig":                   true,
	"setpluginactive":                    true,
	"uninstallplugindata":                true,
	"setpluginconfig":                    true,
	"approvepluginpermission":            true,
	"setpluginupdatesource":              true,
	"runpluginlifecycleaction":           true,
	"setscrapercredential":               true,
	"validatescrapercredential":          true,
	"disablescrapercredential":           true,
	"settranscodeconfig":                 true,
	"bulkrescanunmatched":                true,
	"bulktrashbroken":                    true,
	"bulkmergeduplicates":                true,
	"bulkrescrapescrapefailures":         true,
	"bulkfetchmissingartwork":            true,
	"bulkrescanunmatchedselected":        true,
	"bulktrashbrokenselected":            true,
	"bulkrescrapescrapefailuresselected": true,
	"bulkfetchmissingartworkselected":    true,
	"bulkmergeduplicatesselected":        true,
	"terminatesession":                   true,
	"revokedevice":                       true,
	"revokealldevices":                   true,
	"setdevicerole":                      true,
	"controlsession":                     true,
	// Ambient Group Conductor: session-scoped, same-Profile-
	// gated group lifecycle + sync. NOT admin-gated — like controlSession these
	// sit in the viewer's own scope, bounded by the same-Profile rule in the
	// resolvers. Membership is ephemeral (in-memory); no persisted state.
	"formgroup":       true,
	"joingroup":       true,
	"leavegroup":      true,
	"transitiongroup": true,
	"reportdrift":     true,
	// Watch Party: host-role transport authority +
	// cross-Profile formation with a Reveal intersection at accept. Session-
	// scoped like the Ambient Group fields (NOT admin-gated); the cross-Profile
	// gate is invite-acceptance + the per-session Reveal check in the resolvers.
	"createwatchparty":           true,
	"invitetowatchparty":         true,
	"acceptwatchparty":           true,
	"watchpartytransport":        true,
	"requestwatchpartytransport": true,
	// Auto-skip vs party precedence: a member's
	// client-side auto-skip routed through host authority — authoritative
	// broadcast (host/relaxed) or suppressed + re-sync (non-host strict). Session-
	// scoped like the other party fields; the host gate lives in the resolver.
	"watchpartyautoskip": true,
	// leaveWatchParty: cross-Profile leave (ADR-0111 §4 revocation propagation).
	// Does not assert same-Profile ownership — a party guest can leave without
	// knowing the host's Profile (unlike leaveGroup which is same-Profile only).
	"leavewatchparty":        true,
	"createsavedfilter":      true,
	"deletesavedfilter":      true,
	"createcollection":       true,
	"updatecollection":       true,
	"deletecollection":       true,
	"restorecollection":      true,
	"addcollectionmedia":     true,
	"removecollectionmedia":  true,
	"reordercollectionmedia": true,
	"createofflinepack":      true, // MUSIC-6 (ADR-0098 area 6): build an Offline Pack from an opaque (kind, ref) target
	"revokeofflinepack":      true, // MUSIC-6 (ADR-0098 area 6): revoke an owned Offline Pack (serving layer then denies bytes)
	"deletemypacktokens":     true, // MUSIC-6 (ADR-0036 + ADR-0098 area 6): clear own outstanding pack-token ledger rows
	"createsmartcollection":  true,
	"updatesmartcollection":  true,
	"deletesmartcollection":  true,
	"createchannel":          true, // ADR-0158 CHAN-1: Library Channels edited via the collection-editor surface
	"updatechannel":          true,
	"deletechannel":          true,
	// Retention rules: Console-authored leaving-soon policy.
	"createretentionrule":       true, // admin-gated (see adminFields)
	"updateretentionrule":       true, // admin-gated
	"deleteretentionrule":       true, // admin-gated
	"setretentioncandidatehold": true, // manager-tier viewer-writable keep flag
	"acceptpendingaitag":        true,
	"rejectpendingaitag":        true,
	"setlibraryaiindexing":      true,
	// ENR-7 (ADR-0048 Decision 3/6): operator write of the per-Library
	// skip-detection toggle (resolver in graph_skipdetect.go).
	"setskipdetectionenabled": true,
	// Encrypted-Library key model: provision the DEK +
	// three wraps (Recovery Key once), and the Sealed/Unsealed lifecycle.
	"provisionencryptedlibrary": true,
	"unsealencryptedlibrary":    true,
	"sealencryptedlibrary":      true,
	"rewrapencryptedlibrary":    true,
	// Scrobble eligibility: per-Library opt-in is an
	// operator decision, admin-gated like the other Console mutations. Adult/
	// Restricted Libraries are never eligible by default.
	"setscrobbleeligibility": true,
}

// adminFields are the Console operations: the mutation class
// gated by the Admin Session tier, orthogonal to Device Role (ADR-0007 §2 —
// "Console operations are their own mutation class gated by admin session").
// Every field here MUST sit inside an `if !scope.Admin` guard in execute(); the
// H7 authz-tier test (TestConsoleFieldsRequireAdminSession) drives each one
// with a non-admin manager session and fails the build if any is reachable
// without an open Admin Session — so a new Console resolver can't silently
// ship at manager tier.
//
// The line follows the ADR's own split, not just operation sensitivity:
//   - §4 Admin/Console (here): Libraries (create/delete/roots/scan/schedules),
//     Plugins (activate/config + server settings), Identification *config*,
//     Profiles & access (CRUD + policies + scoped API keys/tokens), Operations
//     (job queue, live sessions + kill, HW transcode config, backups, remote
//     access + watcher + ingest status), Library Health, Enrichment review,
//     migration wizard.
//   - §3 Manage (NOT here — stays manager-tier): bulk edit, file operations,
//     merge/dedupe, Trash review, Unmatched queue/fix-match (identifySearch/
//     identifyApply — these are the per-item match flow, NOT scraper config),
//     markers, field locks, artwork candidates. These are routine curation and
//     ADR-0007 §4 does not enumerate them as Console.
//   - Subscriptions and Smart Collections are content/Library curation that §4
//     does not enumerate; left at manager tier to avoid over-gating (§4 is
//     declared v1-complete).
var adminFields = map[string]bool{
	// Libraries (§4)
	"createlibrary":          true,
	"libraries":              true,
	"setlibraryroots":        true,
	"setlibrarypreviewclips": true,
	"setlibrarybacking":      true,
	"setlibraryrestricted":   true,
	"qualitypolicy":          true,
	"setqualitypolicy":       true,
	"deletelibrary":          true,
	"scanlibrary":            true,
	// Authorized native→server import transport (apple ADR-0003 §1/§3,
	// POWERTOOLS-6B): the Admin Session tier the loopback bearer authenticates as
	// — only the native Companion authority can register a source token or import.
	"registerimportsource": true,
	"importmanagedfile":    true,
	// Encrypted-Library key model: operator-only key
	// provisioning + Sealed/Unsealed lifecycle — a Console operation class like
	// the other Library management mutations.
	"provisionencryptedlibrary": true,
	"unsealencryptedlibrary":    true,
	"sealencryptedlibrary":      true,
	"rewrapencryptedlibrary":    true,
	"encryptedlibrarystatus":    true,
	// Plugins + server config (§4)
	"consoleconfig":             true,
	"setconsoleconfig":          true,
	"setpluginactive":           true,
	"uninstallplugindata":       true,
	"setpluginconfig":           true,
	"pluginworker":              true,
	"approvepluginpermission":   true,
	"setpluginupdatesource":     true,
	"runpluginlifecycleaction":  true,
	"setscrapercredential":      true,
	"validatescrapercredential": true,
	"disablescrapercredential":  true,
	// Opt-in update awareness (ADR-0013 §7 — Console operation class)
	"updatestatus": true,
	// Profiles & access — CRUD + policies (§4)
	"createprofile":           true,
	"updateprofile":           true,
	"setprofilelibraries":     true,
	"setcertificationceiling": true,
	"accessschedule":          true,
	"setaccessschedule":       true,
	// Parental extension pre-authorization toggle (ADR-0039 §2.4 — a managed
	// Profile policy an adult sets in the Console/Manager, like the schedule).
	"setextensionpreauthorization": true,
	// My-Data operator-delete + managed-Profile guardian path (ADR-0036
	// §2.5/§2.6): the Console parallel to the self-service deleteMyEngagement.
	"operatordeleteprofiledata": true,
	// Owner transfer: move the single owner flag between adult
	// Profiles. Admin-Session-gated Console op.
	"transferprofileowner": true,
	// Scoped API keys + admin tokens (§4 Profiles & access / Operations)
	"apikeys":        true,
	"createapikey":   true,
	"revokeapikey":   true,
	"apitokens":      true,
	"createapitoken": true,
	"revokeapitoken": true,
	// Operations — jobs, schedules, HW transcode, backups (§4)
	"jobs":                true,
	"job":                 true,
	"jobevents":           true,
	"operationsgovernor":  true,
	"runjob":              true,
	"runeventscompaction": true,
	"jobcancel":           true,
	"jobretry":            true,
	"schedules":           true,
	"updateschedule":      true,
	"transcodeconfig":     true,
	"settranscodeconfig":  true,
	"backup":              true,
	"backuphistory":       true,
	"verifybackup":        true,
	"purgetrash":          true,
	// Server filesystem enumeration/root registration is an Admin-Session
	// surface: manager role alone must not list host paths.
	"browseserverfolders": true,
	"pathpicker":          true,
	"addpathpickerroot":   true,
	// Retention rules: Console-authored leaving-soon rules.
	"retentionrules":      true,
	"createretentionrule": true,
	"updateretentionrule": true,
	"deleteretentionrule": true,
	// Operations — live sessions dashboard + kill (§4)
	"livesessions":     true,
	"terminatesession": true,
	// Operations — device-key revocation propagation
	"revokedevice": true,
	// Device administration — list + revoke-all
	"devices":          true,
	"revokealldevices": true,
	"setdevicerole":    true,
	// Operations — remote access / watcher / ingest status (§4 server settings
	// + §5 real-time monitoring)
	"remoteaccess":  true,
	"watcherstatus": true,
	// Scrobble eligibility: per-Library opt-in is an
	// operator decision, gated like the other Library/Console mutations.
	"setscrobbleeligibility": true,
	// Scrobble diagnostic status (ADR-0098 area 7, MUSIC-7): the dead-letter +
	// skip feeds expose cross-Profile ids, so the read is an Admin-Session
	// operator surface — the same tier as setScrobbleEligibility above.
	"scrobblestatus": true,
	// Migration wizard (ADR-0037; self-declared §4 Console mutation class)
	"importers":    true,
	"runimport":    true,
	"importstatus": true,
	// First-scan estimate (OPS-4, ADR-0013 §2 — Console / first-run setup flow)
	"scanestimate": true,
	// Library Health Dashboard (ADR-0042 §2.1 — Admin Session)
	"libraryhealth":                      true,
	"libraryhealthitems":                 true,
	"libraryduplicategroups":             true,
	"bulkrescanunmatched":                true,
	"bulktrashbroken":                    true,
	"bulkmergeduplicates":                true,
	"bulkrescrapescrapefailures":         true,
	"bulkfetchmissingartwork":            true,
	"bulkrescanunmatchedselected":        true,
	"bulktrashbrokenselected":            true,
	"bulkrescrapescrapefailuresselected": true,
	"bulkfetchmissingartworkselected":    true,
	"bulkmergeduplicatesselected":        true,
	// Precomputed playback rendition cache (ADR-0057 — operator-only cache
	// controls: health snapshot, explicit generation, budget eviction)
	"playbackrenditioncache":       true,
	"precomputeplaybackrenditions": true,
	"evictplaybackrenditioncache":  true,
	// Full metadata JSON export (ADR-0005 §5 — operator portability, Admin Session)
	"metadataexport": true,
	// Console Enrichment panel (ADR-0041 #107 — Admin Session)
	"libraryindexstatus":   true,
	"pendingaitagqueue":    true,
	"acceptpendingaitag":   true,
	"rejectpendingaitag":   true,
	"setlibraryaiindexing": true,
	// Skip-segment detection per-Library toggle (ENR-7, ADR-0048 Decision
	// 3/6 — Admin Session, the same tier as setLibraryAIIndexing).
	"skipdetectionenabled":    true,
	"setskipdetectionenabled": true,
}

// scopedFields are the top-level fields that need an authenticated session
// scope before execute runs. Everything but serverInfo: unknown fields fail
// as "unsupported query" without touching auth. It is the union of
// queryFields and mutationFields, assembled at init so the auth gate stays
// identical to the pre-split single map.
var scopedFields = map[string]bool{}

func init() {
	// The capability runtime-gating control surface is registered
	// here, BEFORE scopedFields is assembled, so its fields participate in the
	// auth gate and schema manifest. On an App Store binary (beamfall_appstore)
	// the registration is a no-op: the control fields are never added to the
	// registries, so they are not advertised in the manifest and are rejected as
	// unknown top-level fields — not advertised, not reachable (invariant 8).
	registerCapabilityControlFields()
	registerQBittorrentFields()
	for name := range queryFields {
		scopedFields[name] = true
	}
	for name := range mutationFields {
		if scopedFields[name] {
			panic("graph: field classified as both query and mutation: " + name)
		}
		scopedFields[name] = true
	}
	// Every Console (admin-tier) field must also be a known scoped field — its
	// tier classification is orthogonal to its read/write effect class.
	for name := range adminFields {
		if !scopedFields[name] {
			panic("graph: adminFields lists unknown scoped field: " + name)
		}
	}
}

func requiresScope(fields map[string]bool) bool {
	for name := range fields {
		handler, ok := fieldHandlerRegistry[name]
		if ok && scopedFields[name] && handler.roleGate&roleSession != 0 {
			return true
		}
	}
	return false
}

// unknownTopLevelFields returns the sorted requested top-level fields (H23-C)
// that the dispatcher does not recognize — neither serverInfo nor a scope-
// registry entry. An empty result means every requested top-level field is
// known. The client audit (apple-ui, android-ui, core internal/web; the
// beamfall-web marketing site never calls /api/graphql) confirmed no shipped
// client sends a top-level field outside this set, so rejecting unknowns is
// safe to enable wholesale.
func unknownTopLevelFields(fields map[string]bool) []string {
	var unknown []string
	for name := range fields {
		if name == "serverinfo" || scopedFields[name] {
			continue
		}
		unknown = append(unknown, name)
	}
	slices.Sort(unknown)
	return unknown
}

// UnknownTopLevelFields exposes the dispatcher's unknown-field verdict so
// conformance can assert that a field is not reachable on a given build (e.g. the
// capability-control surface on an App Store binary, ADR-0124 §5 / invariant 8).
func UnknownTopLevelFields(fields map[string]bool) []string {
	return unknownTopLevelFields(fields)
}

// MutationFields returns the sorted lowercased names of every scoped
// top-level field that writes or commands state. It is the source of truth
// for the action-completeness conformance gate (every mutation must have a
// behavior test).
func MutationFields() []string {
	names := make([]string, 0, len(mutationFields))
	for name := range mutationFields {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// SchemaField is one entry in the committed schema manifest (H23-A): a
// top-level GraphQL field with its effect class and required authorization
// tier, both derived from the dispatcher's own registries.
type SchemaField struct {
	// Field is the lowercased top-level field name (topLevelFields normalization).
	Field string `json:"field"`
	// Effect is "read" (queryFields) or "write" (mutationFields). serverInfo, the
	// one unauthenticated field, is "read".
	Effect string `json:"effect"`
	// Tier is the required authorization tier: "public" (serverInfo only),
	// "admin" (adminFields — the ADR-0007 §4 Console mutation class), or
	// "session" (any other scoped field; finer manager-role gating is a
	// dispatcher-runtime concern not declared in these registries).
	Tier string `json:"tier"`
}

// SchemaManifest returns every dispatchable top-level field with its effect
// class and required tier, sorted by name — the committed source-of-truth
// artifact (H23-A). It is generated from queryFields/mutationFields/adminFields
// + the serverInfo exception, so the manifest can never silently diverge from
// what execute() actually dispatches and gates: the conformance gate
// regenerates this and byte-compares against the committed JSON.
func SchemaManifest() []SchemaField {
	fields := make([]SchemaField, 0, len(scopedFields)+1)
	tier := func(name string) string {
		if adminFields[name] {
			return "admin"
		}
		return "session"
	}
	// serverInfo is the single unauthenticated field (see scopedFields comment).
	fields = append(fields, SchemaField{Field: "serverinfo", Effect: "read", Tier: "public"})
	for name := range queryFields {
		fields = append(fields, SchemaField{Field: name, Effect: "read", Tier: tier(name)})
	}
	for name := range mutationFields {
		fields = append(fields, SchemaField{Field: name, Effect: "write", Tier: tier(name)})
	}
	slices.SortFunc(fields, func(a, b SchemaField) int {
		return strings.Compare(a.Field, b.Field)
	})
	return fields
}

type request struct {
	Query         string         `json:"query"`
	OperationName string         `json:"operationName,omitempty"`
	Variables     map[string]any `json:"variables,omitempty"`
}

type response struct {
	Data                 map[string]any                        `json:"data,omitempty"`
	Errors               []graphError                          `json:"errors,omitempty"`
	authorizationRefused bool                                  `json:"-"`
	privilegedOutcomes   map[string]observability.AuditOutcome `json:"-"`
}

// MarshalJSON distinguishes an intentionally empty successful data object
// (all selections excluded by directives) from an errors-only mutation, whose
// data member must be absent.
func (r response) MarshalJSON() ([]byte, error) {
	type wireResponse struct {
		Data   json.RawMessage `json:"data,omitempty"`
		Errors []graphError    `json:"errors,omitempty"`
	}
	wire := wireResponse{Errors: r.Errors}
	if r.Data != nil {
		data, err := json.Marshal(r.Data)
		if err != nil {
			return nil, err
		}
		wire.Data = data
	}
	return json.Marshal(wire)
}

type graphError struct {
	Message string `json:"message"`
	// Path names the top-level field this error belongs to (M2 partial-success
	// reads). Omitted for whole-response errors (auth gate, unsupported query,
	// aborted mutation batch) so their wire shape is unchanged.
	Path []string `json:"path,omitempty"`
}

func writeGraphError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, response{Errors: []graphError{{Message: message}}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
