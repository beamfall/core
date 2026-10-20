package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/migrate/pathmap"
	"github.com/beamfall/core/internal/store"
	sdk "github.com/beamfall/plugin-sdk"
)

// Store is the migrate handler's slice of the store (consumer-defined seam,
// ARCHITECTURE.md §4: an interface at the consumer, not store exporting one).
// The match chain reads file identity by path; the apply step writes
// import-provenance engagement and pins matched files; progress rides the job's
// own progress row.
type Store interface {
	// MediaFileFingerprintByPath returns the stored content fingerprint for an
	// available file at path (the match-chain path key → file identity).
	MediaFileFingerprintByPath(ctx context.Context, path string) (store.StoredFingerprint, error)
	// MediaFileExistsByFingerprint resolves a content fingerprint to its owning
	// Media (file identity → Media, the second half of the path match).
	MediaFileExistsByFingerprint(ctx context.Context, fingerprint string) (store.MediaID, bool, error)
	// MediaClass returns the matched Media's playback class without reveal
	// filtering; imported engagement applies to the operator-selected Profile
	// even when the matched Library is Restricted.
	MediaClass(ctx context.Context, mediaID store.MediaID) (store.MediaClass, error)
	// MediaLibraryID returns the matched Media's owning Library, used to honor a
	// run's Profile/Library mapping: when the operator scoped the
	// import to a target Library, a path match whose Library differs is treated
	// as unmatched for this run rather than written to the wrong Library.
	MediaLibraryID(ctx context.Context, mediaID store.MediaID) (store.LibraryID, error)
	// UpsertPlaybackProgressWithProvenance records imported watch-state with its
	// source discriminator and true historical timestamp.
	UpsertPlaybackProgressWithProvenance(ctx context.Context, progress store.PlaybackProgress, finished bool, policy store.ResumePolicy, prov store.EngagementProvenance) error
	// PlaybackProgressFor reads a Profile's current progress for a Media item —
	// the additive gate (ADR-0037 §2.4: only fill a gap or advance a
	// strictly-ahead position, never overwrite/downgrade).
	PlaybackProgressFor(ctx context.Context, profileID store.ProfileID, mediaID store.MediaID) (store.PlaybackProgress, bool, error)
	// SearchMedia is the metadata match chain's title key: an
	// FTS lookup over Media titles/fields, reveal-filtered, used to find the
	// candidate Media for a Last.fm scrobble that carries no file path.
	SearchMedia(ctx context.Context, filter store.LibraryFilter, query string, limit int) ([]store.SourceItem, error)
	// EntitiesForMedia reads the Entities a candidate Media relates to under a
	// named relation — the scrobble match chain confirms a title hit by its
	// artist (the music `album-artist` relation) and disambiguates by album.
	EntitiesForMedia(ctx context.Context, mediaID store.MediaID, relationName string, filter store.LibraryFilter) ([]store.RelatedEntity, error)
	// ApplyImportedPlays records imported play history (play count + last-played)
	// onto the existing per-Profile projection, strictly additive (ADR-0037
	// §2.4/§2.5). The scrobble apply path; it never touches resume/watched.
	ApplyImportedPlays(ctx context.Context, profileID store.ProfileID, mediaID store.MediaID, playCount int, playedAt time.Time, prov store.EngagementProvenance) error
	// MediaIDsByExternalID resolves plugin-contributed external IDs for
	// history-backfill sources that have no trustworthy file path.
	MediaIDsByExternalID(ctx context.Context, scheme string, value string) ([]store.MediaID, error)
	// GetOrCreateSubscription resolves or inserts a Library-level feed
	// subscription idempotently for subscription-list imports.
	GetOrCreateSubscription(ctx context.Context, subscription store.Subscription, now time.Time) (store.Subscription, bool, error)
	// SetFollow records the importing Profile's follow on the subscription.
	SetFollow(ctx context.Context, profileID store.ProfileID, subscriptionID store.SubscriptionID, on bool, now time.Time) error
	// RecordItemStateImport records a feed-scoped item-state row for replay.
	RecordItemStateImport(ctx context.Context, state store.ItemStateImport, now time.Time) error
	// ReplayItemStateImports applies pending item-state rows whose feed-scoped
	// GUID now resolves to a Media via subscription seen-entry paths.
	ReplayItemStateImports(ctx context.Context, now time.Time) (int, error)
	// ImportCollectionSnapshot atomically creates an imported static Collection
	// and fills its ordered current-member gaps.
	ImportCollectionSnapshot(ctx context.Context, collection store.Collection, members []store.MediaID, now time.Time) (store.Collection, error)
	// SetMatchPinned pins a matched file's identity so a later rescan leaves the
	// import-applied match alone.
	SetMatchPinned(ctx context.Context, fingerprint string, pinned bool) (bool, error)
	// QueueUnmatchedRecordOnce persists one stable Media-INDEPENDENT Unmatched
	// Record so an import item the match chain could not resolve (ADR-0037 §2.3
	// step 4) enters the single review inbox instead of vanishing into
	// a per-run count. Repeating the same refusal returns the existing record so
	// interrupted and operator-triggered reruns do not duplicate inbox entries.
	QueueUnmatchedRecordOnce(ctx context.Context, rec store.UnmatchedRecord) (string, error)
	// UpdateJobCheckpoint persists the run's progress (match-rate report) onto
	// the job's progress row — the status surface the wizard reads (ADR-0037
	// §2.3).
	UpdateJobCheckpoint(ctx context.Context, id store.JobID, leaseOwner string, checkpoint []byte, progress store.JobProgress, now time.Time) error
}

// ResumePolicyResolver resolves the engagement resume policy for a matched
// Media. The app supplies a plugin-aware resolver; tests and bare core usage
// fall back to class defaults.
type ResumePolicyResolver interface {
	ResumePolicy(ctx context.Context, mediaID store.MediaID) (store.ResumePolicy, error)
}

// ScrobbleRelationSelectors carries the plugin-owned relation slugs the scrobble
// match chain reads, so core/migrate never hardcodes a music relation name
// (ADR-0098 area 1: "Generic migration code must not hardcode Music relation
// slugs … the matcher receives plugin-owned relation selectors"). The names are
// the bare, unqualified relation slugs the store's EntitiesForMedia matches
// against entity_relation.relation — NOT "kind/relation" qualified. ArtistConfirm
// confirms a title hit by the scrobble's artist; AlbumTieBreak disambiguates
// several artist-confirmed candidates by the scrobble's album. Only relations
// that target Media (queryable from a Media via EntitiesForMedia) belong here.
type ScrobbleRelationSelectors struct {
	ArtistConfirm []string
	AlbumTieBreak []string
}

type handlerConfig struct {
	resumePolicy      ResumePolicyResolver
	scrobbleSelectors ScrobbleRelationSelectors
	pathRewrites      *pathmap.Ruleset
}

// HandlerOption configures migration import job handling.
type HandlerOption func(*handlerConfig)

// WithResumePolicyResolver lets the composition root supply plugin engagement
// overrides for imported watch-state.
func WithResumePolicyResolver(resolver ResumePolicyResolver) HandlerOption {
	return func(cfg *handlerConfig) {
		if resolver != nil {
			cfg.resumePolicy = resolver
		}
	}
}

// WithScrobbleRelationSelectors supplies the plugin-owned relation slugs the
// scrobble matcher uses (ADR-0098 area 1). The composition root populates these
// from the activated Music bundle so core/migrate stays free of music slugs; a
// run with no selectors confirms nothing by artist/album (title-only matching).
func WithScrobbleRelationSelectors(selectors ScrobbleRelationSelectors) HandlerOption {
	return func(cfg *handlerConfig) {
		cfg.scrobbleSelectors = selectors
	}
}

// WithPathRewrites supplies the operator's prefix rewrite rules:
// source paths are rewritten before the ADR-0037 §2.3 path-match step so a
// moved mount, a Docker volume prefix, or a Windows drive letter still resolves
// to the same Beamfall Media. The wizard builds the Ruleset (from suggested or
// manually-entered rules) and passes it here; a nil/empty ruleset leaves the
// match chain exactly as before. It is a normalization in front of the chain,
// never a new match tier — core/migrate hardcodes no source paths.
func WithPathRewrites(rules *pathmap.Ruleset) HandlerOption {
	return func(cfg *handlerConfig) {
		cfg.pathRewrites = rules
	}
}

type mediaClassResumePolicyResolver struct {
	store Store
}

func (r mediaClassResumePolicyResolver) ResumePolicy(ctx context.Context, mediaID store.MediaID) (store.ResumePolicy, error) {
	class, err := r.store.MediaClass(ctx, mediaID)
	if err != nil {
		return store.ResumePolicy{}, err
	}
	return store.ResumePolicy{Class: class}, nil
}

// Payload is the import run's stored request (mirrors the graph wizard's
// migrationJobPayload, plus the target Profile). The wizard marshals it when it
// enqueues the run; the handler unmarshals it to drive the import. Credentials
// are never stored here (invariant #5) — an export-file importer references an
// uploaded file by identifier, a credentials importer a secret entry.
type Payload struct {
	Version    int      `json:"version"`
	PluginID   string   `json:"plugin_id"`
	ImporterID string   `json:"importer_id"`
	Categories []string `json:"categories"`
	DryRun     bool     `json:"dry_run,omitempty"`
	// ProfileID is the Profile whose engagement the watch-state import writes
	// to (watch-state is per-Profile, ADR-0037 §2.2 / ADR-0005). The wizard sets
	// it to the operator's Profile; an empty value fails the run rather than
	// guessing whose history this is.
	ProfileID string `json:"profile_id"`
	// SourceUserProfileIDs maps each source system's opaque user identifier to
	// the Beamfall Profile that receives that user's per-Profile state. Sources
	// that expose ItemsBySourceUser use this map instead of ProfileID; an absent
	// mapping skips only that source user's items and never falls back to the
	// run-level Profile.
	SourceUserProfileIDs map[string]string `json:"source_user_profile_ids,omitempty"`
	// SourcePath is the operator-supplied location the source reads from — an
	// export-file path (Plex DB, NFO root) or connection string the resolver
	// needs to construct a path-needing Source (ADR-0037 §2.1: the wizard
	// "collects credentials or export files for the selected source"). It is a
	// plain location string, never a secret (invariant #5); the resolver, not
	// this package, interprets it. Passed verbatim to SourceResolver.Resolve so
	// a path-needing parser can be built; empty when a source needs none.
	SourcePath string `json:"source_path"`
	// LibraryID serves two roles. For subscription-list / item-state imports it is
	// the required target Library the imported feeds/state attach to. For
	// path/scrobble matches it optionally scopes the match chain to one target
	// Library — the operator's Profile/Library mapping: empty means
	// match every Library by path, while a set value records a match
	// whose owning Library differs as unmatched, routing the import to the chosen
	// Library only.
	LibraryID string `json:"library_id,omitempty"`
	// Root is the optional target download root for imported feed subscriptions.
	// If empty, store defaults from the Library's persisted roots.
	Root string `json:"root,omitempty"`
}

// Result is the import run's outcome, surfaced on the job progress row as the
// match-rate report (ADR-0037 §2.3: "1,847 of 1,912 items matched"). Matched +
// Unmatched = total foreign items the source yielded. PerCategory is the
// per-category breakdown — how many matched items carried each admitted data
// category — so the unified report can convey not just an overall match rate but
// which kinds of state were applied or previewed.
type Result struct {
	Matched               int
	Unmatched             int
	SkippedUnresolvedUser int
	// skippedBeamfallNewer identifies source rows whose matched target already
	// held engagement at least as advanced as the import. Indices are safe for
	// the privacy-filtered report; source paths and IDs are not.
	skippedBeamfallNewer []int
	dryRun               bool
	dryRunItemCount      int
	dryRunItems          []dryRunItemDisposition
	sourceUsers          []sourceUserDisposition
	// PerCategory counts matched state applied (or previewed) by admitted
	// category, keyed by the fixed category vocabulary (watch-state, ratings,
	// …). A matched item with no admitted category present contributes to
	// Matched but to no category bucket. Members of an all-or-nothing skipped
	// collection likewise contribute to the match rate, but not this applied
	// state count.
	PerCategory map[string]int
	// MatchStrategy counts matched rows by the strategy that resolved identity.
	// Unmatched rows are reported from Unmatched so the breakdown includes the
	// non-guessing tail of the chain.
	MatchStrategy map[string]int
	// SmartCollectionsStatic counts smart collections/playlists the run imported
	// as static current-member snapshots (ADR-0028 §E / ADR-0037 §2.2). It is
	// deliberately NOT folded into Matched/PerCategory: a smart→static conversion
	// is a silent behavioral downgrade (the imported collection will not
	// auto-update), so the report names it on its own distinct warning line
	// rather than hiding it inside the match-rate number.
	SmartCollectionsStatic int
	// SmartCollectionsIncomplete counts smart collections whose declared
	// current-member snapshot contained one or more unresolved members. The
	// all-or-nothing policy writes no partial collection for these inputs.
	SmartCollectionsIncomplete int
	// PathCollisions records each case where N distinct source paths rewrote onto
	// one Beamfall path. Their state merged additively under §2.4;
	// each collision becomes its own explicit report line so the union is never
	// silent. Like SmartCollectionsStatic it is a distinct warning, not part of
	// the match-rate number (both source records still counted as Matched).
	PathCollisions []pathmap.Collision
}

const (
	payloadVersion       = 1
	checkpointEveryItems = 100
)

// Match-method discriminators recorded in engagement provenance (MIGREV-7,
// ADR-0037 §2.4). Each names the strategy that ACTUALLY resolved a row's
// identity — the producing branch of the match chain — never an ADR-ideal
// ordering. They are shape-named (invariant #1), stored verbatim, only ever
// compared/grouped for per-run audit and re-run skip-explanation.
const (
	matchMethodPath       = "path"
	matchMethodExternalID = "external-id"
	matchMethodFilename   = "filename"
	matchMethodScrobble   = "scrobble"
	// matchMethodFeedGUID is item-state's identity resolution: the row is keyed
	// by feed URL + item GUID (subscription scope), resolved to a concrete
	// Media only later at ReplayItemStateImports time via
	// subscription_seen_entry.path. It is recorded at write time because that
	// resolution strategy never varies for this category.
	matchMethodFeedGUID = "feed-guid"
)

func newResult() Result {
	return Result{PerCategory: map[string]int{}, MatchStrategy: map[string]int{}}
}

// SourceUserItemsSource is the optional multi-user extension to Source. Its
// structural method uses only plugin-sdk item types, so a plugin module can
// implement it without importing this internal package. Each map key is the
// source system's opaque user identifier and each value is that user's items.
type SourceUserItemsSource interface {
	ItemsBySourceUser(ctx context.Context) (map[string][]ForeignItem, error)
}

type resolvedSourceItem struct {
	item      ForeignItem
	profileID store.ProfileID
}

type sourceUserDisposition struct {
	sourceUserID string
	mapped       bool
	itemCount    int
}

func resolveSourceItems(ctx context.Context, source Source, payload Payload) ([]resolvedSourceItem, []sourceUserDisposition, error) {
	sourceUsers, ok := source.(SourceUserItemsSource)
	if !ok {
		items := []ForeignItem{}
		err := sdk.StreamSource(ctx, source, "", func(_ context.Context, page []ForeignItem, _ string) error {
			items = append(items, page...)
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
		resolved := make([]resolvedSourceItem, 0, len(items))
		for _, item := range items {
			resolved = append(resolved, resolvedSourceItem{item: item, profileID: store.ProfileID(payload.ProfileID)})
		}
		return resolved, nil, nil
	}

	itemsBySourceUser, err := sourceUsers.ItemsBySourceUser(ctx)
	if err != nil {
		return nil, nil, err
	}
	sourceUserIDs := make([]string, 0, len(itemsBySourceUser))
	for sourceUserID := range itemsBySourceUser {
		sourceUserIDs = append(sourceUserIDs, sourceUserID)
	}
	sort.Strings(sourceUserIDs)

	var resolved []resolvedSourceItem
	dispositions := make([]sourceUserDisposition, 0, len(sourceUserIDs))
	for _, sourceUserID := range sourceUserIDs {
		items := itemsBySourceUser[sourceUserID]
		profileID := store.ProfileID(payload.SourceUserProfileIDs[sourceUserID])
		dispositions = append(dispositions, sourceUserDisposition{
			sourceUserID: sourceUserID,
			mapped:       profileID != "",
			itemCount:    len(items),
		})
		for _, item := range items {
			resolved = append(resolved, resolvedSourceItem{item: item, profileID: profileID})
		}
	}
	return resolved, dispositions, nil
}

func (r Result) sourceUserReport() string {
	if len(r.sourceUsers) == 0 {
		return ""
	}
	parts := make([]string, 0, len(r.sourceUsers))
	for _, sourceUser := range r.sourceUsers {
		disposition := "unmapped"
		if sourceUser.mapped {
			disposition = "mapped"
		}
		parts = append(parts, fmt.Sprintf("%s=%s(items=%d)", sourceUser.sourceUserID, disposition, sourceUser.itemCount))
	}
	return strings.Join(parts, ", ")
}

// recordUnmatched routes one import item the match chain could not resolve into
// the Media-independent Unmatched Record inbox. Before this, such an
// item was only counted (result.Unmatched++) and never reviewable; now it is a
// real record carrying its run/importer provenance, a generic source handle, and
// a failure-reason so the operator can Fix Match it. The record is Library-less
// (an unmatched import resolved to no Media and no Library) so it surfaces to an
// all-Libraries operator scope. A dry-run preview writes nothing — it must not
// mutate durable state. Persistence failure fails the run (a silently-dropped
// record would recreate the very gap this closes).
func recordUnmatched(ctx context.Context, st Store, payload Payload, runID string, item ForeignItem, reason string, now time.Time) error {
	return recordUnmatchedWithHandle(ctx, st, payload, runID, unmatchedSourceHandle(item), reason, now)
}

func recordUnmatchedWithHandle(ctx context.Context, st Store, payload Payload, runID, sourceHandle, reason string, now time.Time) error {
	if payload.DryRun {
		return nil
	}
	_, err := st.QueueUnmatchedRecordOnce(ctx, store.UnmatchedRecord{
		Origin:        store.UnmatchedOriginImport,
		ImportRunID:   runID,
		ImporterID:    payload.ImporterID,
		SourceHandle:  sourceHandle,
		FailureReason: reason,
		QueuedAt:      now,
	})
	return err
}

// unmatchedSourceHandle renders a generic, operator-recognizable handle for an
// unmatched import item — never domain vocabulary (invariant #1), just the raw
// key the source recorded. Preference mirrors the match chain: a file path when
// present, else an external-id summary, else the scrobble's artist/track.
func unmatchedSourceHandle(item ForeignItem) string {
	if item.Path != "" {
		return item.Path
	}
	if len(item.ExternalIDs) > 0 {
		parts := make([]string, 0, len(item.ExternalIDs))
		for _, id := range item.ExternalIDs {
			parts = append(parts, id.Scheme+":"+id.Value)
		}
		return strings.Join(parts, " ")
	}
	if item.Scrobble.Present {
		return strings.TrimSpace(item.Scrobble.Artist + " — " + item.Scrobble.Track)
	}
	return ""
}

// categoryReport renders the per-category breakdown into the deterministic
// "watch-state=12, ratings=not yet importable" suffix the wire message and the
// web report parse. Categories are emitted in the fixed vocabulary order (not
// map order) so the message — and the golden/snapshot expectations over it —
// are stable.
func (r Result) categoryReport(categories map[string]bool) string {
	order := []string{CategoryWatchState, CategoryRatings, CategoryCollections, CategorySubscriptions, CategoryItemState}
	var parts []string
	for _, cat := range order {
		if cat == CategoryRatings && categories[cat] {
			parts = append(parts, fmt.Sprintf("%s=not yet importable", cat))
			continue
		}
		if cat == CategoryCollections && categories[cat] {
			if r.SmartCollectionsStatic > 0 || r.SmartCollectionsIncomplete > 0 || r.PerCategory[cat] > 0 {
				parts = append(parts, fmt.Sprintf("%s=%d", cat, r.PerCategory[cat]))
			} else {
				parts = append(parts, fmt.Sprintf("%s=not yet importable", cat))
			}
			continue
		}
		if n, ok := r.PerCategory[cat]; ok && n > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", cat, n))
		}
	}
	return strings.Join(parts, ", ")
}

func (r Result) strategyReport() string {
	if len(r.MatchStrategy) == 0 && r.Unmatched == 0 {
		return ""
	}
	order := []string{matchMethodExternalID, matchMethodPath, matchMethodFilename}
	parts := make([]string, 0, len(order)+3)
	seen := map[string]bool{}
	for _, method := range order {
		parts = append(parts, fmt.Sprintf("%s=%d", method, r.MatchStrategy[method]))
		seen[method] = true
	}
	for _, method := range []string{matchMethodScrobble, matchMethodFeedGUID} {
		if n := r.MatchStrategy[method]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", method, n))
			seen[method] = true
		}
	}
	for method, n := range r.MatchStrategy {
		if !seen[method] && n > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", method, n))
		}
	}
	parts = append(parts, fmt.Sprintf("unmatched=%d", r.Unmatched))
	return strings.Join(parts, ", ")
}

func (r Result) total() int {
	return r.Matched + r.Unmatched + r.SkippedUnresolvedUser
}

// smartCollectionWarning renders the distinct smart-collection→static warning
// line the report carries when the run imported one or more smart collections as
// static current-member snapshots (ADR-0028 §E / ADR-0037 §2.2). The wording is
// contract text — a fixed, parseable line separate from the match-rate number so
// the operator sees the silent behavioral downgrade explicitly ("they will not
// auto-update"). Empty when the run converted no smart collections.
func (r Result) smartCollectionWarning() string {
	if r.SmartCollectionsStatic <= 0 {
		return ""
	}
	return fmt.Sprintf("%d smart collections imported as static — they will not auto-update", r.SmartCollectionsStatic)
}

func (r Result) skippedBeamfallNewerReport() string {
	if len(r.skippedBeamfallNewer) == 0 {
		return ""
	}
	items := make([]string, 0, len(r.skippedBeamfallNewer))
	for _, index := range r.skippedBeamfallNewer {
		items = append(items, fmt.Sprintf("#%d", index))
	}
	return "skipped-beamfall-newer: " + strings.Join(items, ", ")
}

func (r Result) incompleteSmartCollectionWarning() string {
	if r.SmartCollectionsIncomplete <= 0 {
		return ""
	}
	return fmt.Sprintf("%d smart collections skipped — one or more current members were unmatched", r.SmartCollectionsIncomplete)
}

// pathCollisionWarnings renders one contract line per N-source→1-Beamfall-path
// collision. The wording is a fixed, parseable line distinct from
// the match-rate number so the operator sees the additive union explicitly ("2
// source paths mapped to <path> — merged additively"), never a silent merge.
// Empty when the run had no path collisions. Collisions arrive sorted by target
// (DetectCollisions), so the lines are deterministic.
func (r Result) pathCollisionWarnings() []string {
	if len(r.PathCollisions) == 0 {
		return nil
	}
	lines := make([]string, 0, len(r.PathCollisions))
	for _, c := range r.PathCollisions {
		lines = append(lines, fmt.Sprintf("%d source paths mapped to %s — merged additively", len(c.Sources), c.Target))
	}
	return lines
}

// Handler builds the generic import job handler. It registers under
// JobKind on the jobs.Registry. resolver maps the run to its source parser;
//
// Imported watch-state resolves the matched Media's resume policy before
// writing progress, so video importers preserve resume positions while audio
// importers follow the owning plugin's declared engagement posture (ADR-0029
// §7).
func Handler(st Store, resolver SourceResolver, opts ...HandlerOption) jobs.Handler {
	cfg := handlerConfig{resumePolicy: mediaClassResumePolicyResolver{store: st}}
	for _, opt := range opts {
		opt(&cfg)
	}
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		var payload Payload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return jobs.Result{}, fmt.Errorf("migrate: decode payload: %w", err)
		}
		if payload.Version != payloadVersion {
			return jobs.Result{}, fmt.Errorf("migrate: unsupported payload version %d (want %d)", payload.Version, payloadVersion)
		}
		source, ok := resolver.Resolve(payload.PluginID, payload.ImporterID, payload.Categories, payload.SourcePath)
		if !ok {
			return jobs.Result{}, fmt.Errorf("migrate: no parser for %s/%s", payload.PluginID, payload.ImporterID)
		}
		if err := checkForeignDBCompatibility(ctx, payload); err != nil {
			return jobs.Result{}, fmt.Errorf("migrate: %w", err)
		}
		items, sourceUsers, err := resolveSourceItems(ctx, source, payload)
		if err != nil {
			return jobs.Result{}, fmt.Errorf("migrate: read source items: %w", err)
		}
		categories := categorySet(source.Categories())
		if !payload.DryRun && targetLibraryRequired(categories, items) && strings.TrimSpace(payload.LibraryID) == "" {
			return jobs.Result{}, errors.New("migrate: payload is missing the target library")
		}
		targetLibrary := store.LibraryID(payload.LibraryID)

		result := newResult()
		result.dryRun = payload.DryRun
		result.dryRunItemCount = len(items)
		result.sourceUsers = sourceUsers
		now := time.Now().UTC()
		if payload.ProfileID == "" && len(sourceUsers) == 0 {
			result.SkippedUnresolvedUser = len(items)
			for i := range items {
				result.recordDryRunItem(i+1, dryRunSkip, "", dryRunReasonUnresolved)
			}
			if err := recordFinalProgress(ctx, st, job, result, categories, now); err != nil {
				return jobs.Result{}, fmt.Errorf("migrate: record progress: %w", err)
			}
			return jobs.Result{}, nil
		}
		// Scrobbles are metadata-keyed play events with no file path: one CSV row
		// per play. They aggregate by matched Media so each Media's running play
		// total is applied once (idempotent re-run — ADR-0037 §2.4 / §2.5). A
		// file-keyed item (watch-state) applies inline as before.
		plays := newPlayTally()
		// pathMappings records each path-MATCHED item's (source path → Beamfall
		// path) so an N-source→1-Beamfall-path collision is reported explicitly
		// after the run rather than merging silently.
		var pathMappings []pathmap.Mapping
		for i, sourceItem := range items {
			if err := ctx.Err(); err != nil {
				return jobs.Result{}, fmt.Errorf("migrate: context canceled: %w", err)
			}
			if i > 0 && i%checkpointEveryItems == 0 {
				if err := recordRunningProgress(ctx, st, job, result, i, len(items), now); err != nil {
					return jobs.Result{}, fmt.Errorf("migrate: record progress: %w", err)
				}
			}
			item := sourceItem.item
			if sourceItem.profileID == "" {
				result.SkippedUnresolvedUser++
				result.recordDryRunItem(i+1, dryRunSkip, "", dryRunReasonUnresolved)
				continue
			}
			itemPayload := payload
			itemPayload.ProfileID = string(sourceItem.profileID)
			if item.Subscription.Present {
				if !categories[CategorySubscriptions] {
					result.recordDryRunItem(i+1, dryRunSkip, "", dryRunReasonCategory)
					continue
				}
				if !payload.DryRun {
					if err := applySubscription(ctx, st, itemPayload, item.Subscription, now); err != nil {
						return jobs.Result{}, fmt.Errorf("migrate: apply subscription: %w", err)
					}
				}
				if categories[CategorySubscriptions] {
					result.PerCategory[CategorySubscriptions]++
				}
				result.Matched++
				result.recordDryRunItem(i+1, dryRunWouldApply, "", "")
				continue
			}
			if item.ItemState.Present {
				if !categories[CategoryItemState] {
					result.recordDryRunItem(i+1, dryRunSkip, "", dryRunReasonCategory)
					continue
				}
				if !payload.DryRun {
					if err := applyItemState(ctx, st, itemPayload, string(job.ID), item.ItemState, now); err != nil {
						return jobs.Result{}, fmt.Errorf("migrate: record item-state: %w", err)
					}
				}
				if categories[CategoryItemState] {
					result.PerCategory[CategoryItemState]++
				}
				result.Matched++
				result.MatchStrategy[matchMethodFeedGUID]++
				result.recordDryRunItem(i+1, dryRunWouldApply, "", "")
				continue
			}
			if item.SmartCollection.Present {
				// A smart collection has no rule-import path, so persist its
				// static Collection baseline and surface the distinct
				// smart-collection→static warning line (ADR-0028 §E / ADR-0037 §2.2),
				// counted separately from the member match-rate number. Its ordered
				// current members drive Matched/Unmatched and PerCategory.
				// Skipped entirely when the run does not admit the collections
				// category. Both a dry-run preview and a real run count it (the
				// warning is a preview signal about the behavioral downgrade, so it
				// must appear in the dry-run report the operator reviews before
				// committing); only the real run writes the baseline.
				if !categories[CategoryCollections] {
					result.recordDryRunItem(i+1, dryRunSkip, "", dryRunReasonCategory)
					continue
				}
				if !payload.DryRun && targetLibrary == "" {
					return jobs.Result{}, errors.New("migrate: payload is missing the target library")
				}
				if err := validateSmartCollection(item.SmartCollection); err != nil {
					return jobs.Result{}, fmt.Errorf("migrate: smart collection %q: %w", item.SmartCollection.Name, err)
				}
				memberIDs := make([]store.MediaID, 0, len(item.SmartCollection.CurrentMembers))
				seenMemberIDs := make(map[store.MediaID]bool, len(item.SmartCollection.CurrentMembers))
				complete := true
				for memberPosition, member := range item.SmartCollection.CurrentMembers {
					mediaID, matched, method, reason, mapping, err := matchCollectionMember(ctx, st, member, cfg, targetLibrary)
					if err != nil {
						return jobs.Result{}, fmt.Errorf("migrate: match smart collection %q member: %w", item.SmartCollection.Name, err)
					}
					if !matched {
						sourceHandle := unmatchedSourceHandle(member)
						if sourceHandle == "" {
							sourceHandle = fmt.Sprintf("collection:%s member:%d", item.SmartCollection.Identity, memberPosition)
						}
						if err := recordUnmatchedWithHandle(ctx, st, itemPayload, string(job.ID), sourceHandle, reason, now); err != nil {
							return jobs.Result{}, fmt.Errorf("migrate: record smart collection %q unmatched member: %w", item.SmartCollection.Name, err)
						}
						result.Unmatched++
						complete = false
						continue
					}
					if seenMemberIDs[mediaID] {
						return jobs.Result{}, fmt.Errorf("migrate: smart collection %q: duplicate resolved member %q cannot preserve exact order", item.SmartCollection.Name, mediaID)
					}
					seenMemberIDs[mediaID] = true
					memberIDs = append(memberIDs, mediaID)
					result.Matched++
					result.MatchStrategy[method]++
					if mapping != nil {
						pathMappings = append(pathMappings, *mapping)
					}
				}
				if !complete {
					result.SmartCollectionsIncomplete++
					result.recordDryRunItem(i+1, dryRunSkip, matchDispositionUnmatched, store.UnmatchedReasonNoCandidates)
					continue
				}
				if !payload.DryRun {
					if err := applySmartCollection(ctx, st, itemPayload, item.SmartCollection, memberIDs, now); err != nil {
						return jobs.Result{}, fmt.Errorf("migrate: apply smart collection %q: %w", item.SmartCollection.Name, err)
					}
				}
				result.PerCategory[CategoryCollections] += len(memberIDs)
				result.SmartCollectionsStatic++
				result.recordDryRunItem(i+1, dryRunWouldConvert, "", "")
				continue
			}
			if item.Scrobble.Present {
				mediaID, matched, method, err := matchByScrobble(ctx, st, item, cfg.scrobbleSelectors)
				if err != nil {
					return jobs.Result{}, fmt.Errorf("migrate: match scrobble %q/%q: %w", item.Scrobble.Artist, item.Scrobble.Track, err)
				}
				if !matched {
					// Unmatched scrobbles are counted/reported, never create Media
					// (MIG-4 scope; ADR-0037 §2.3 step 4), and routed to the review
					// inbox so the operator can Fix Match them.
					if err := recordUnmatched(ctx, st, itemPayload, string(job.ID), item, store.UnmatchedReasonNoCandidates, now); err != nil {
						return jobs.Result{}, fmt.Errorf("migrate: record unmatched scrobble %q/%q: %w", item.Scrobble.Artist, item.Scrobble.Track, err)
					}
					result.Unmatched++
					result.recordDryRunItem(i+1, dryRunSkip, matchDispositionUnmatched, store.UnmatchedReasonNoCandidates)
					continue
				}
				// Honor the operator's Profile/Library mapping: a match outside the
				// chosen Library is unmatched for this run.
				inScope, err := mediaInLibrary(ctx, st, mediaID, targetLibrary)
				if err != nil {
					return jobs.Result{}, fmt.Errorf("migrate: scope scrobble %q/%q: %w", item.Scrobble.Artist, item.Scrobble.Track, err)
				}
				if !inScope {
					if err := recordUnmatched(ctx, st, itemPayload, string(job.ID), item, store.UnmatchedReasonLibraryMismatch, now); err != nil {
						return jobs.Result{}, fmt.Errorf("migrate: record scoped-out scrobble %q/%q: %w", item.Scrobble.Artist, item.Scrobble.Track, err)
					}
					result.Unmatched++
					result.recordDryRunItem(i+1, dryRunSkip, reportMatchDisposition(method), store.UnmatchedReasonLibraryMismatch)
					continue
				}
				plays.add(sourceItem.profileID, mediaID, item.Scrobble.PlayedAt, method)
				result.Matched++
				result.MatchStrategy[method]++
				// A scrobble is a play-history record; it lands under watch-state
				// when the run admits it (the engagement projection it advances).
				if categories[CategoryWatchState] {
					result.PerCategory[CategoryWatchState]++
				}
				result.recordDryRunItem(i+1, dryRunWouldApply, reportMatchDisposition(method), "")
				continue
			}
			// Identity-resolution chain per ADR-0037 §2.3: external-ID before path.
			// External IDs (TMDB/TVDB/MusicBrainz) are the most durable key — a
			// file can move but its external ID stays constant — so they are tried
			// first; a path match is the reliable fallback for same-storage
			// migrations. matchMethod records the branch that ACTUALLY resolved
			// identity: external-id when the ID lookup hit, path when the
			// fallback did. Only meaningful once matched is true.
			var mediaID store.MediaID
			var fingerprint string
			var matched bool
			var err error
			matchMethod := matchMethodExternalID
			if len(item.ExternalIDs) > 0 {
				var decisive bool
				mediaID, matched, decisive, err = matchByExternalIDs(ctx, st, item.ExternalIDs)
				if err != nil {
					return jobs.Result{}, fmt.Errorf("migrate: match external IDs: %w", err)
				}
				// A decisive external-ID conflict (ambiguous/duplicate) is unmatched
				// for this run and never falls through to a path match — the handler
				// does not guess when the durable key is contradictory.
				if decisive && !matched {
					if err := recordUnmatched(ctx, st, itemPayload, string(job.ID), item, store.UnmatchedReasonExternalIDConflict, now); err != nil {
						return jobs.Result{}, fmt.Errorf("migrate: record external-ID conflict %q: %w", item.Path, err)
					}
					result.Unmatched++
					result.recordDryRunItem(i+1, dryRunSkip, matchDispositionUnmatched, store.UnmatchedReasonExternalIDConflict)
					continue
				}
			}
			if !matched {
				// Prefix rewrite normalizes the source path before the §2.3 path
				// match: a moved mount / Docker volume / drive-letter
				// path still resolves to the same Beamfall Media. No rule (or nil
				// ruleset) leaves the path untouched.
				matchPath := item.Path
				if rewritten, ok := cfg.pathRewrites.Rewrite(item.Path); ok {
					matchPath = rewritten
				}
				mediaID, fingerprint, matched, err = matchByPath(ctx, st, matchPath)
				if err != nil {
					return jobs.Result{}, fmt.Errorf("migrate: match %q: %w", item.Path, err)
				}
				if matched {
					matchMethod = matchMethodPath
					pathMappings = append(pathMappings, pathmap.Mapping{Source: item.Path, Target: matchPath})
				}
			}
			if !matched {
				mediaID, matched, err = matchByFilename(ctx, st, item.Path)
				if err != nil {
					return jobs.Result{}, fmt.Errorf("migrate: match filename for %q: %w", item.Path, err)
				}
				if matched {
					matchMethod = matchMethodFilename
					fingerprint = ""
				}
			}
			if !matched {
				// Unmatched items are counted for the run's completeness report
				// (ADR-0037 §2.3 step 4) AND routed to the review inbox
				// so identity resolution is a reviewable Fix-Match record, not a
				// silent drop. The handler still never guesses.
				if err := recordUnmatched(ctx, st, itemPayload, string(job.ID), item, store.UnmatchedReasonNoCandidates, now); err != nil {
					return jobs.Result{}, fmt.Errorf("migrate: record unmatched %q: %w", item.Path, err)
				}
				result.Unmatched++
				result.recordDryRunItem(i+1, dryRunSkip, matchDispositionUnmatched, store.UnmatchedReasonNoCandidates)
				continue
			}
			// Honor the operator's Profile/Library mapping: a path
			// match in a different Library than the chosen one is unmatched here.
			inScope, err := mediaInLibrary(ctx, st, mediaID, targetLibrary)
			if err != nil {
				return jobs.Result{}, fmt.Errorf("migrate: scope %q: %w", item.Path, err)
			}
			if !inScope {
				if err := recordUnmatched(ctx, st, itemPayload, string(job.ID), item, store.UnmatchedReasonLibraryMismatch, now); err != nil {
					return jobs.Result{}, fmt.Errorf("migrate: record scoped-out %q: %w", item.Path, err)
				}
				result.Unmatched++
				result.recordDryRunItem(i+1, dryRunSkip, reportMatchDisposition(matchMethod), store.UnmatchedReasonLibraryMismatch)
				continue
			}
			var applied []string
			var skippedBeamfallNewer bool
			if payload.DryRun {
				applied = previewItemCategories(categories, item)
			} else {
				applied, skippedBeamfallNewer, err = applyItem(ctx, st, cfg.resumePolicy, itemPayload, categories, string(job.ID), matchMethod, mediaID, fingerprint, item)
				if err != nil {
					return jobs.Result{}, fmt.Errorf("migrate: apply %q: %w", item.Path, err)
				}
			}
			if skippedBeamfallNewer {
				result.skippedBeamfallNewer = append(result.skippedBeamfallNewer, i+1)
			}
			for _, cat := range applied {
				result.PerCategory[cat]++
			}
			result.Matched++
			result.MatchStrategy[matchMethod]++
			result.recordDryRunItem(i+1, dryRunWouldApply, reportMatchDisposition(matchMethod), "")
		}
		if len(items) > 0 && len(items)%checkpointEveryItems == 0 {
			if err := recordRunningProgress(ctx, st, job, result, len(items), len(items), now); err != nil {
				return jobs.Result{}, fmt.Errorf("migrate: record progress: %w", err)
			}
		}
		// Apply each matched Media's running play total once (additive; a re-run
		// of the same export is a MAX no-op). Provenance is the shape-named import
		// discriminator (invariant #1), the historical time the latest scrobble.
		if !payload.DryRun {
			if err := plays.apply(ctx, st, payload, string(job.ID)); err != nil {
				return jobs.Result{}, fmt.Errorf("migrate: apply plays: %w", err)
			}
		}
		if categories[CategoryItemState] && !payload.DryRun {
			if _, err := st.ReplayItemStateImports(ctx, now); err != nil {
				return jobs.Result{}, fmt.Errorf("migrate: replay item-state: %w", err)
			}
		}

		// N distinct source paths that rewrote onto one Beamfall path merged
		// additively above (§2.4, via applyWatchState); surface each such union as
		// an explicit report line so it is never silent.
		result.PathCollisions = pathmap.DetectCollisions(pathMappings)
		if err := recordFinalProgress(ctx, st, job, result, categories, now); err != nil {
			return jobs.Result{}, fmt.Errorf("migrate: record progress: %w", err)
		}
		return jobs.Result{}, nil
	}
}

func previewItemCategories(categories map[string]bool, item ForeignItem) []string {
	if categories[CategoryWatchState] && item.WatchState.Present {
		return []string{CategoryWatchState}
	}
	return nil
}

// matchByPath is the file-path step of the match chain (ADR-0028 §6,
// ADR-0037 §2.3): map a foreign item to a core Media by file path, regardless
// of which plugin owns the Library. The chain is an ordered strategy — exact
// path, then a normalized path (filepath.Clean, collapsing "." / ".." /
// duplicate separators a source export may carry). It returns the matched
// Media, the matched file's content fingerprint (for the match-pin), and
// whether the step matched.
func matchByPath(ctx context.Context, st Store, path string) (store.MediaID, string, bool, error) {
	if path == "" {
		return "", "", false, nil
	}
	for _, candidate := range pathCandidates(path) {
		fp, err := st.MediaFileFingerprintByPath(ctx, candidate)
		if err != nil {
			return "", "", false, err
		}
		if !fp.OK {
			continue
		}
		mediaID, ok, err := st.MediaFileExistsByFingerprint(ctx, fp.ContentFingerprint)
		if err != nil {
			return "", "", false, err
		}
		if !ok {
			continue
		}
		return mediaID, fp.ContentFingerprint, true, nil
	}
	return "", "", false, nil
}

// pathCandidates is the ordered path-match strategy: the exact path first, then
// the cleaned path when it differs (so a single matchByPath loop expresses the
// chain). Duplicates collapse — an already-clean path yields one candidate.
func pathCandidates(path string) []string {
	cleaned := filepath.Clean(path)
	if cleaned == path {
		return []string{path}
	}
	return []string{path, cleaned}
}

const filenameSearchLimit = 20

// matchByFilename is ADR-0037 §2.3 step 3: strip the source path and extension,
// then resolve the filename stem against Media titles. The FTS lookup finds
// candidates, but only one exact case-insensitive title match is accepted; zero
// or multiple exact matches fall through to unmatched rather than guessing.
func matchByFilename(ctx context.Context, st Store, path string) (store.MediaID, bool, error) {
	if path == "" {
		return "", false, nil
	}
	normalizedPath := strings.ReplaceAll(path, `\`, "/")
	base := filepath.Base(filepath.Clean(normalizedPath))
	stem := strings.TrimSpace(strings.TrimSuffix(base, filepath.Ext(base)))
	if stem == "" || stem == "." {
		return "", false, nil
	}
	candidates, err := st.SearchMedia(ctx, store.LibraryFilter{AllLibraries: true}, stem, filenameSearchLimit)
	if err != nil {
		return "", false, err
	}
	var matched store.MediaID
	for _, candidate := range candidates {
		if !strings.EqualFold(strings.TrimSpace(candidate.Title), stem) {
			continue
		}
		if matched != "" {
			return "", false, nil
		}
		matched = candidate.ID
	}
	return matched, matched != "", nil
}

// applyItem applies one identity-matched item's admitted state append-only
// and pins path matches. Feed-scoped item-state is handled before
// the match chain because it replays through the subscription/GUID ledger rather
// than through a filesystem path. v1 carries watch-state; ratings/collections/
// subscriptions/item-state are recorded as admitted categories but have no apply
// path yet (their store projections are MIG-2/plugin scope), so an item that
// carries only those is matched and pinned but writes no engagement. It returns
// the admitted categories the item carried and whether Beamfall's newer target
// state caused the watch-state write to be skipped.
func applyItem(ctx context.Context, st Store, resumePolicy ResumePolicyResolver, payload Payload, categories map[string]bool, runID, matchMethod string, mediaID store.MediaID, fingerprint string, item ForeignItem) ([]string, bool, error) {
	var applied []string
	var skippedBeamfallNewer bool
	if categories[CategoryWatchState] && item.WatchState.Present {
		var err error
		var wrote bool
		wrote, err = applyWatchState(ctx, st, resumePolicy, store.ProfileID(payload.ProfileID), payload, runID, matchMethod, mediaID, item.WatchState)
		if err != nil {
			return nil, false, err
		}
		applied = append(applied, CategoryWatchState)
		if !wrote {
			skippedBeamfallNewer = true
		}
	}
	if fingerprint != "" {
		// Pin the matched file so a later rescan leaves the import-applied identity
		// alone. Re-runnable: pinning twice is idempotent. An external-ID
		// match has no path fingerprint to pin, so it is skipped.
		if _, err := st.SetMatchPinned(ctx, fingerprint, true); err != nil {
			return nil, false, err
		}
	}
	return applied, skippedBeamfallNewer, nil
}

// mediaInLibrary reports whether a matched Media belongs to the run's target
// Library (the Profile/Library mapping, ADR-0037 §2.2). An empty target means
// "any Library" — the default cross-Library path match — so it is
// always in scope. Otherwise the matched Media's owning Library must equal the
// chosen one; a mismatch routes the item to the unmatched count for this run.
func mediaInLibrary(ctx context.Context, st Store, mediaID store.MediaID, target store.LibraryID) (bool, error) {
	if target == "" {
		return true, nil
	}
	owning, err := st.MediaLibraryID(ctx, mediaID)
	if err != nil {
		return false, err
	}
	return owning == target, nil
}

func applySubscription(ctx context.Context, st Store, payload Payload, sub Subscription, now time.Time) error {
	_, err := getOrCreateSubscription(ctx, st, payload, sub, now)
	return err
}

func validateSmartCollection(collection SmartCollection) error {
	if strings.TrimSpace(collection.Identity) == "" {
		return errors.New("stable identity is required")
	}
	if strings.TrimSpace(collection.Name) == "" {
		return errors.New("name is required")
	}
	if !collection.CurrentMembersPresent {
		return errors.New("ordered current-member identities are required")
	}
	if collection.MemberCount != len(collection.CurrentMembers) {
		return fmt.Errorf("member count %d does not match %d ordered current-member identities", collection.MemberCount, len(collection.CurrentMembers))
	}
	return nil
}

func importedCollectionID(payload Payload, sourceIdentity string) store.CollectionID {
	key := strings.Join([]string{payload.LibraryID, payload.PluginID, payload.ImporterID, sourceIdentity}, "\x00")
	sum := sha256.Sum256([]byte(key))
	return store.CollectionID(fmt.Sprintf("collection-import-%x", sum[:16]))
}

func applySmartCollection(ctx context.Context, st Store, payload Payload, collection SmartCollection, members []store.MediaID, now time.Time) error {
	_, err := st.ImportCollectionSnapshot(ctx, store.Collection{
		ID:        importedCollectionID(payload, collection.Identity),
		LibraryID: store.LibraryID(payload.LibraryID),
		Name:      collection.Name,
	}, members, now)
	return err
}

func matchCollectionMember(ctx context.Context, st Store, item ForeignItem, cfg handlerConfig, targetLibrary store.LibraryID) (store.MediaID, bool, string, string, *pathmap.Mapping, error) {
	if len(item.ExternalIDs) > 0 {
		mediaID, matched, decisive, err := matchByExternalIDs(ctx, st, item.ExternalIDs)
		if err != nil {
			return "", false, "", "", nil, err
		}
		if decisive {
			if !matched {
				return "", false, "", store.UnmatchedReasonExternalIDConflict, nil, nil
			}
			inScope, err := mediaInLibrary(ctx, st, mediaID, targetLibrary)
			if err != nil {
				return "", false, "", "", nil, err
			}
			if !inScope {
				return "", false, "", store.UnmatchedReasonLibraryMismatch, nil, nil
			}
			return mediaID, true, matchMethodExternalID, "", nil, nil
		}
	}
	if item.Path != "" {
		matchPath := item.Path
		if rewritten, ok := cfg.pathRewrites.Rewrite(item.Path); ok {
			matchPath = rewritten
		}
		mediaID, _, matched, err := matchByPath(ctx, st, matchPath)
		if err != nil {
			return "", false, "", "", nil, err
		}
		if matched {
			inScope, err := mediaInLibrary(ctx, st, mediaID, targetLibrary)
			if err != nil {
				return "", false, "", "", nil, err
			}
			if !inScope {
				return "", false, "", store.UnmatchedReasonLibraryMismatch, nil, nil
			}
			mapping := &pathmap.Mapping{Source: item.Path, Target: matchPath}
			return mediaID, true, matchMethodPath, "", mapping, nil
		}
		mediaID, matched, err = matchByFilename(ctx, st, item.Path)
		if err != nil {
			return "", false, "", "", nil, err
		}
		if matched {
			inScope, err := mediaInLibrary(ctx, st, mediaID, targetLibrary)
			if err != nil {
				return "", false, "", "", nil, err
			}
			if !inScope {
				return "", false, "", store.UnmatchedReasonLibraryMismatch, nil, nil
			}
			return mediaID, true, matchMethodFilename, "", nil, nil
		}
	}
	if item.Scrobble.Present {
		item.ExternalIDs = nil
		mediaID, matched, method, err := matchByScrobble(ctx, st, item, cfg.scrobbleSelectors)
		if err != nil {
			return "", false, "", "", nil, err
		}
		if matched {
			inScope, err := mediaInLibrary(ctx, st, mediaID, targetLibrary)
			if err != nil {
				return "", false, "", "", nil, err
			}
			if !inScope {
				return "", false, "", store.UnmatchedReasonLibraryMismatch, nil, nil
			}
			return mediaID, true, method, "", nil, nil
		}
	}
	return "", false, "", store.UnmatchedReasonNoCandidates, nil, nil
}

func getOrCreateSubscription(ctx context.Context, st Store, payload Payload, sub Subscription, now time.Time) (store.Subscription, error) {
	root := strings.TrimSpace(sub.Root)
	if root == "" {
		root = strings.TrimSpace(payload.Root)
	}
	added, _, err := st.GetOrCreateSubscription(ctx, store.Subscription{
		LibraryID: store.LibraryID(strings.TrimSpace(payload.LibraryID)),
		FeedURL:   sub.FeedURL,
		Root:      root,
		Title:     sub.Title,
	}, now)
	if err != nil {
		return store.Subscription{}, err
	}
	if err := st.SetFollow(ctx, store.ProfileID(payload.ProfileID), added.ID, true, now); err != nil {
		return store.Subscription{}, err
	}
	return added, nil
}

func applyItemState(ctx context.Context, st Store, payload Payload, runID string, state ItemState, now time.Time) error {
	sub, err := getOrCreateSubscription(ctx, st, payload, Subscription{
		Present: true,
		FeedURL: state.FeedURL,
	}, now)
	if err != nil {
		return err
	}
	return st.RecordItemStateImport(ctx, store.ItemStateImport{
		ProfileID:         store.ProfileID(payload.ProfileID),
		SubscriptionID:    sub.ID,
		Source:            "import:" + payload.PluginID,
		ItemGUID:          state.GUID,
		Played:            state.Played,
		HasProgress:       state.HasProgress,
		Progress:          state.Progress,
		Starred:           state.Starred,
		ImportRunID:       runID,
		ImportImporterID:  payload.ImporterID,
		ImportMatchMethod: matchMethodFeedGUID,
	}, now)
}

func watchStatePlayCount(ws WatchState) int {
	field := reflect.ValueOf(ws).FieldByName("PlayCount")
	if !field.IsValid() {
		return 0
	}
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if n := field.Int(); n > 0 {
			return int(n)
		}
	}
	return 0
}

// applyWatchState writes imported watch-state through the import-provenance
// engagement path, enforcing the additive rule: write only when
// no engagement exists yet OR the source position is strictly ahead of
// Beamfall's last recorded position. It reports whether it wrote so the run can
// name same-or-older source items in the final disposition report. A re-run that
// brings the same or an older position is a no-op, so importing twice never
// double-applies or downgrades.
// The provenance Source is a shape-named import discriminator (invariant #1 —
// "import:" + the plugin id, never a source-system name); OccurredAt preserves
// the true historical instant without moving the recency key.
func applyWatchState(ctx context.Context, st Store, resumePolicy ResumePolicyResolver, profileID store.ProfileID, payload Payload, runID, matchMethod string, mediaID store.MediaID, ws WatchState) (bool, error) {
	playCount := watchStatePlayCount(ws)
	finished := ws.Watched
	position := ws.PositionSeconds
	duration := ws.DurationSeconds
	existing, found, err := st.PlaybackProgressFor(ctx, profileID, mediaID)
	if err != nil {
		return false, err
	}
	if found {
		// Already complete, or the source isn't strictly ahead: additive
		// no-op. A watched item never downgrades to unwatched; an in-progress
		// item only advances. The write below is reached only on a real advance,
		// so the run-scoped provenance columns are never stamped on a skip.
		if existing.Watched && playCount <= existing.PlayCount {
			return false, nil
		}
		if !ws.Watched && ws.PositionSeconds <= existing.PositionSeconds && playCount <= existing.PlayCount {
			return false, nil
		}
		if existing.Watched {
			finished = true
		}
		if !ws.Watched && ws.PositionSeconds <= existing.PositionSeconds {
			position = existing.PositionSeconds
			duration = existing.DurationSeconds
		}
	}
	policy, err := resumePolicy.ResumePolicy(ctx, mediaID)
	if err != nil {
		return false, err
	}
	err = st.UpsertPlaybackProgressWithProvenance(ctx, store.PlaybackProgress{
		ProfileID:       profileID,
		MediaID:         mediaID,
		PositionSeconds: position,
		DurationSeconds: duration,
		PlayCount:       playCount,
	}, finished, policy, store.EngagementProvenance{
		Source:      "import:" + payload.PluginID,
		OccurredAt:  ws.OccurredAt,
		RunID:       runID,
		ImporterID:  payload.ImporterID,
		MatchMethod: matchMethod,
	})
	return err == nil, err
}

// scrobbleSearchLimit caps the candidate Media a scrobble's title query pulls.
// A handful of candidates is plenty to disambiguate by artist/album; the cap
// keeps a pathological common-word title from scanning the whole library.
const scrobbleSearchLimit = 20

var schemePrecedence = map[string]int{
	"tmdb": 0,
	"imdb": 1,
	"tvdb": 2,
}

func schemePrecedenceRank(scheme string) int {
	if rank, ok := schemePrecedence[scheme]; ok {
		return rank
	}
	return len(schemePrecedence)
}

// matchByScrobble is the metadata match chain: resolve a
// Last.fm scrobble — which carries no file path — to a core Media by metadata.
// It searches Media titles for the track (the existing reveal-filtered FTS
// match infrastructure), then confirms a candidate by its artist (and uses the
// album as a tiebreaker). A scrobble with no track cannot be matched. When more
// than one candidate survives artist confirmation, the album tiebreaker decides;
// if it can't, the match is ambiguous and reported unmatched (the handler never
// guesses which Media a play belongs to — ADR-0037 §2.3 step 4).
//
// The relation slugs used for artist/album confirmation are plugin-owned, passed
// in by the composition root (ADR-0098 area 1) — core/migrate names none.
// It also returns the match method that produced the result: a
// scrobble item resolved by the delegated external-ID branch is "external-id",
// and only a metadata/title-relation match is "scrobble" — so the persisted
// import_match_method never mislabels the delegated branch. The method is only
// meaningful when matched is true.
func matchByScrobble(ctx context.Context, st Store, item ForeignItem, selectors ScrobbleRelationSelectors) (store.MediaID, bool, string, error) {
	if len(item.ExternalIDs) > 0 {
		mediaID, matched, decisive, err := matchByExternalIDs(ctx, st, item.ExternalIDs)
		if err != nil || decisive {
			return mediaID, matched, matchMethodExternalID, err
		}
	}
	scr := item.Scrobble
	track := strings.TrimSpace(scr.Track)
	if track == "" {
		return "", false, "", nil
	}
	candidates, err := st.SearchMedia(ctx, store.LibraryFilter{AllLibraries: true}, track, scrobbleSearchLimit)
	if err != nil {
		return "", false, "", err
	}
	if len(candidates) == 0 {
		return "", false, "", nil
	}
	// A title-only run with exactly one hit and no artist to confirm against is
	// taken as the match (the export omitted artist); otherwise artist confirms.
	artist := strings.TrimSpace(scr.Artist)
	if artist == "" {
		if len(candidates) == 1 {
			return candidates[0].ID, true, matchMethodScrobble, nil
		}
		return "", false, "", nil
	}

	var confirmed []store.MediaID
	for _, cand := range candidates {
		ok, err := scrobbleRelationMatches(ctx, st, cand.ID, artist, selectors.ArtistConfirm)
		if err != nil {
			return "", false, "", err
		}
		if ok {
			confirmed = append(confirmed, cand.ID)
		}
	}
	switch len(confirmed) {
	case 0:
		return "", false, "", nil
	case 1:
		return confirmed[0], true, matchMethodScrobble, nil
	}
	// Multiple artist-confirmed candidates (same track title across releases):
	// the album tiebreaker decides. No album, or no album hit, stays ambiguous.
	album := strings.TrimSpace(scr.Album)
	if album == "" {
		return "", false, "", nil
	}
	var byAlbum []store.MediaID
	for _, id := range confirmed {
		ok, err := scrobbleRelationMatches(ctx, st, id, album, selectors.AlbumTieBreak)
		if err != nil {
			return "", false, "", err
		}
		if ok {
			byAlbum = append(byAlbum, id)
		}
	}
	if len(byAlbum) == 1 {
		return byAlbum[0], true, matchMethodScrobble, nil
	}
	return "", false, "", nil
}

// externalIDs is the seam's store-free ExternalID slice (scheme+value); the
// store lookup below consumes the scheme/value directly, so no adapter type is
// needed at the boundary.
func matchByExternalIDs(ctx context.Context, st Store, externalIDs []ExternalID) (store.MediaID, bool, bool, error) {
	var matched store.MediaID
	matchedRank := len(schemePrecedence)
	for _, id := range externalIDs {
		scheme := strings.TrimSpace(id.Scheme)
		value := strings.TrimSpace(id.Value)
		if scheme == "" || value == "" {
			continue
		}
		ids, err := st.MediaIDsByExternalID(ctx, scheme, value)
		if err != nil {
			return "", false, false, err
		}
		switch len(ids) {
		case 0:
			continue
		case 1:
			rank := schemePrecedenceRank(scheme)
			if matched == "" {
				matched = ids[0]
				matchedRank = rank
				continue
			}
			if rank < matchedRank {
				matched = ids[0]
				matchedRank = rank
				continue
			}
			if rank == matchedRank && matched != ids[0] {
				return "", false, true, nil
			}
		default:
			return "", false, true, nil
		}
	}
	return matched, matched != "", matched != "", nil
}

// scrobbleRelationMatches reports whether a candidate Media carries an Entity
// whose title equals want (case-insensitive) under any of the given plugin-owned
// relation names. Artist confirmation passes the ArtistConfirm selectors; the
// album tiebreaker passes AlbumTieBreak — each reads its own list, so the album
// check never falls back to the artist relations. Only Media-targeted relations
// are reachable here (EntitiesForMedia queries to_media_id), which is exactly
// what the selectors carry (ADR-0098 area 1). An empty selector list (or a
// candidate with no matching relation) does NOT match — artist/album are
// required disambiguators when the export supplies them.
func scrobbleRelationMatches(ctx context.Context, st Store, mediaID store.MediaID, want string, relations []string) (bool, error) {
	for _, relation := range relations {
		if relation == "" {
			continue
		}
		related, err := st.EntitiesForMedia(ctx, mediaID, relation, store.LibraryFilter{AllLibraries: true})
		if err != nil {
			return false, err
		}
		for _, r := range related {
			if strings.EqualFold(strings.TrimSpace(r.Entity.Title), want) {
				return true, nil
			}
		}
	}
	return false, nil
}

// playTally aggregates matched scrobbles by Profile and Media within one run: a
// running play count and the latest play instant per projection. Aggregating
// before the write lets each projection's full running total apply once through
// the additive ApplyImportedPlays (idempotent re-run — ADR-0037 §2.4/§2.5).
type playTally struct {
	count    map[playTallyKey]int
	lastPlay map[playTallyKey]time.Time
	// method is the match method persisted for the Media's aggregated projection
	// row. A Media's scrobbles almost always share a method,
	// but if they differ the deterministic rule is "the method of the advancing
	// latest play" — the row whose PlayedAt becomes lastPlay wins the tiebreak, so
	// the persisted import_match_method is stable regardless of add() order.
	method map[playTallyKey]string
	order  []playTallyKey
}

type playTallyKey struct {
	profileID store.ProfileID
	mediaID   store.MediaID
}

func newPlayTally() *playTally {
	return &playTally{count: map[playTallyKey]int{}, lastPlay: map[playTallyKey]time.Time{}, method: map[playTallyKey]string{}}
}

func (t *playTally) add(profileID store.ProfileID, mediaID store.MediaID, playedAt time.Time, method string) {
	key := playTallyKey{profileID: profileID, mediaID: mediaID}
	if _, seen := t.count[key]; !seen {
		t.order = append(t.order, key)
		t.method[key] = method
	}
	t.count[key]++
	// The advancing latest play sets the persisted method: a later PlayedAt (or
	// the first play for a zero-timestamp Media) carries its method forward, so
	// the aggregated row's method matches the play that set lastPlay.
	if playedAt.After(t.lastPlay[key]) {
		t.lastPlay[key] = playedAt
		t.method[key] = method
	}
}

// apply writes each tallied Media's running play total + latest play instant to
// the per-Profile projection. The provenance Source is the shape-named import
// discriminator (invariant #1 — "import:" + plugin id); OccurredAt carries the
// latest scrobble's true historical time without moving the recency key.
func (t *playTally) apply(ctx context.Context, st Store, payload Payload, runID string) error {
	for _, key := range t.order {
		last := t.lastPlay[key]
		if err := st.ApplyImportedPlays(ctx, key.profileID, key.mediaID, t.count[key], last, store.EngagementProvenance{
			Source:      "import:" + payload.PluginID,
			OccurredAt:  last,
			RunID:       runID,
			ImporterID:  payload.ImporterID,
			MatchMethod: t.method[key],
		}); err != nil {
			return err
		}
	}
	return nil
}

func recordRunningProgress(ctx context.Context, st Store, job store.Job, result Result, processed, total int, now time.Time) error {
	return st.UpdateJobCheckpoint(ctx, job.ID, job.LeaseOwner, job.Checkpoint, store.JobProgress{
		Current: int64(processed),
		Total:   int64(total),
		Unit:    "items",
		Message: fmt.Sprintf("%d of %d items processed (%d matched, %d unmatched)", processed, total, result.Matched, result.Unmatched),
	}, now)
}

func recordFinalProgress(ctx context.Context, st Store, job store.Job, result Result, categories map[string]bool, now time.Time) error {
	message := fmt.Sprintf("%d of %d items matched", result.Matched, result.total())
	itemReport, err := result.dryRunItemReport()
	if err != nil {
		return err
	}
	var reports []string
	if breakdown := result.categoryReport(categories); breakdown != "" {
		reports = append(reports, breakdown)
	}
	if strategies := result.strategyReport(); strategies != "" {
		reports = append(reports, "strategies: "+strategies)
	}
	if result.SkippedUnresolvedUser > 0 {
		reports = append(reports, fmt.Sprintf("skipped-unresolved-user=%d", result.SkippedUnresolvedUser))
	}
	if sourceUsers := result.sourceUserReport(); sourceUsers != "" {
		reports = append(reports, "source-users: "+sourceUsers)
	}
	if len(reports) > 0 {
		message += " (" + strings.Join(reports, "; ") + ")"
	}
	if warning := result.smartCollectionWarning(); warning != "" {
		message += "; " + warning
	}
	if warning := result.incompleteSmartCollectionWarning(); warning != "" {
		message += "; " + warning
	}
	for _, line := range result.pathCollisionWarnings() {
		message += "; " + line
	}
	if skipped := result.skippedBeamfallNewerReport(); skipped != "" {
		message += "; " + skipped
	}
	if itemReport != "" {
		message += "; " + itemReport
	}
	return st.UpdateJobCheckpoint(ctx, job.ID, job.LeaseOwner, job.Checkpoint, store.JobProgress{
		Current: int64(result.Matched),
		Total:   int64(result.total()),
		Unit:    "items",
		Message: message,
	}, now)
}

func categorySet(categories []string) map[string]bool {
	set := make(map[string]bool, len(categories))
	for _, c := range categories {
		set[c] = true
	}
	return set
}

func targetLibraryRequired(categories map[string]bool, items []resolvedSourceItem) bool {
	if categories[CategorySubscriptions] || categories[CategoryItemState] {
		return true
	}
	if !categories[CategoryCollections] {
		return false
	}
	for _, item := range items {
		if item.item.SmartCollection.Present {
			return true
		}
	}
	return false
}
