package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

// PlaybackProgress is the per-Profile resume projection for one Media item
// (ADR-0005 engagement; ADR-0011 projections live in the events database).
type PlaybackProgress struct {
	ProfileID       ProfileID
	MediaID         MediaID
	PositionSeconds int
	DurationSeconds int
	Watched         bool
	PlayCount       int
	UpdatedAt       time.Time
	// ChannelContext flags this play as originating from a tuned Library Channel.
	// A channel-context partial play is EXCLUDED from the
	// continue-watching/resume projection (inProgressItemsSQL) so a shuffle
	// Channel cannot poison Continue Watching — the Channel itself is the
	// resumable object, not each grazed item. It does NOT suppress the real
	// signals: a channel play past the completion threshold still marks watched,
	// and play_count/history still record it. False (the zero value) is ordinary
	// direct playback; every pre-CHAN-3 caller leaves it false.
	ChannelContext bool
	// EventTimeMS is the client-reported monotonic instant the
	// position was observed — never server-arrival time. Zero (unset) means the
	// caller sent no event-time; the UPSERT then falls back to the pre-ABPARITY-1
	// unconditional-overwrite behavior. A non-zero value guards position_seconds/
	// updated_at_ms: an incoming write whose EventTimeMS is older than the row's
	// last-applied event-time is a no-op, so a stale offline-flush from one device
	// cannot rewind a newer position already recorded by another.
	EventTimeMS int64
	// Provenance is the ADR-0009 §7 import discriminator: "" native,
	// a shape-named source value for imported engagement. Write-only on input
	// is via UpsertPlaybackProgressWithProvenance; this field is the read-back.
	Provenance string
	// OccurredAt is the true historical event time when the row was backdated by
	// an importer; zero when native (the event time is then UpdatedAt). It is
	// COALESCE(occurred_at_ms, updated_at_ms) on read, so a native read still
	// yields a meaningful timestamp.
	OccurredAt time.Time
	// ImportRunID / ImportImporterID / ImportMatchMethod are the MIGREV-7
	// run-scoped import provenance read back: the run that wrote
	// this row, the importer that produced it, and the match method that resolved
	// identity. All empty on native rows (the columns are NULL). They let a re-run
	// derive a skip reason ("already applied by run X via a path match").
	ImportRunID       string
	ImportImporterID  string
	ImportMatchMethod string
}

// watchedThreshold marks video media watched once this fraction has played.
const watchedThreshold = 0.95

// playedThreshold marks audio media played: min(80%, track end).
const playedThreshold = 0.8

// resumeFloorSeconds hides barely-started playback from Continue Watching.
const resumeFloorSeconds = 10

// nextUpEngagementWindowMS caps how far back nextUpSQL looks for progress rows
// (90 days). Progress older than this threshold cannot meaningfully seed a
// "next up" shelf — the user has likely abandoned the series — and the cap
// prevents the initial events-DB scan from growing without bound on large
// long-running libraries.
const nextUpEngagementWindowMS = 90 * 24 * 3600 * 1000

// nextUpRowLimit caps the number of progress rows fetched in a single nextUpSQL
// scan. Combined with nextUpEngagementWindowMS it bounds both the scan size and
// the subsequent chunked catalog IN-list queries.
const nextUpRowLimit = 500

// ResumePolicy is the declarative resume-eligibility input:
// the class default (video resumes, audio does not), optionally overridden by
// the owning plugin's declared engagement posture (podcasts: audio resumes).
type ResumePolicy struct {
	Class MediaClass
	// Override, when non-nil, replaces the class default.
	Override *bool
}

// Eligible resolves the policy. Only video resumes by default: audio plays
// from the top and an image is a still with no playhead
// to resume, so neither stores a resume position.
func (p ResumePolicy) Eligible() bool {
	if p.Override != nil {
		return *p.Override
	}
	return p.Class == MediaClassVideo
}

// EngagementProvenance is the ADR-0009 §7 import-readiness reservation
// for the engagement write path. It is a schema reservation: the IMPORTERS that
// populate it (Plex/Jellyfin/predecessor watch-state, ADR-0037 Migration Wizard) ship
// LATER as Integration Plugins. The native write path leaves this zero, which is
// byte-for-byte the pre-FREEZE-3 behavior.
type EngagementProvenance struct {
	// Source is a SHAPE-named discriminator (invariant #1 — never a domain term):
	// "" is native (every locally-recorded play), an importer stamps an opaque
	// source-shape value (e.g. "import" or "import:<source>"). It is stored
	// verbatim and only ever compared/grouped, never parsed by core.
	Source string
	// OccurredAt, when non-zero, is the REAL historical instant the engagement
	// happened — for backdated imports ("watched 3 years ago" keeps its true
	// timestamp). It is recorded WITHOUT moving the recency key (updated_at_ms),
	// so a years-old import never masquerades as a fresh play on the
	// continue-watching / recently-played / next-up shelves. Zero = native: the
	// column stays NULL and the true event time is updated_at_ms.
	OccurredAt time.Time
	// RunID is the import job's ID — the RUN that wrote this row (MIGREV-7,
	// ADR-0037 §2.4). Empty on native writes (column stays NULL). A per-run audit
	// groups every engagement row a run wrote; a re-run explains a skip by naming
	// the run that already applied the row.
	RunID string
	// ImporterID is the importer that produced the row — the run's source
	// discriminator (a shape-named plugin-owned id, invariant #1, never a
	// source-system domain term). Stored verbatim, only ever compared/grouped.
	// Empty on native writes.
	ImporterID string
	// MatchMethod is how the item's identity was resolved: "path", "external-id",
	// or "scrobble". It records the strategy that ACTUALLY matched this row (the
	// producing branch), never an ADR-ideal ordering. Empty on native writes.
	MatchMethod string
}

// UpsertPlaybackProgress records playback for a Profile. Crossing the class
// threshold (or an explicit finished report) marks the item watched, clears
// the resume point, and counts a play. Resume eligibility is declarative:
// non-eligible media never stores a resume position, so it
// can never surface in Continue Watching on any client.
func (s *Store) UpsertPlaybackProgress(ctx context.Context, progress PlaybackProgress, finished bool, policy ResumePolicy) error {
	// Native append: zero-value provenance (Source "", OccurredAt zero) is the
	// pre-FREEZE-3 behavior — provenance column stays '' and occurred_at_ms NULL.
	return s.UpsertPlaybackProgressWithProvenance(ctx, progress, finished, policy, EngagementProvenance{})
}

// UpsertPlaybackProgressWithProvenance is the ADR-0009 §7
// import-admitting variant of UpsertPlaybackProgress: identical recording logic,
// plus an optional EngagementProvenance carrying an import source-shape
// discriminator and a backdated historical timestamp. Native callers use
// UpsertPlaybackProgress (zero provenance); a future importer calls
// this with a non-empty Source and the source's real OccurredAt so imported
// history is distinguishable AND keeps its true historical time without polluting
// the recency-ordered shelves (updated_at_ms stays now()). This is the write path
// the schema reservation exists to admit; the importers themselves ship later.
func (s *Store) UpsertPlaybackProgressWithProvenance(ctx context.Context, progress PlaybackProgress, finished bool, policy ResumePolicy, prov EngagementProvenance) error {
	// Authority face: resume/playback-progress is part
	// of the engagement projection, which is main-authored. Attached refuses.
	if err := s.guardAuthority(serverrole.FaceEngagement); err != nil {
		return err
	}
	if progress.ProfileID == "" || progress.MediaID == "" {
		return ValidationError("profile ID and media ID are required")
	}
	if progress.PositionSeconds < 0 || progress.DurationSeconds < 0 {
		return ValidationError("playback position must not be negative")
	}
	if progress.PlayCount < 0 {
		return ValidationError("play count must not be negative")
	}
	threshold := watchedThreshold
	if policy.Class == MediaClassAudio {
		threshold = playedThreshold
	}
	resumeEligible := policy.Eligible()
	watched := finished
	if progress.DurationSeconds > 0 &&
		float64(progress.PositionSeconds) >= threshold*float64(progress.DurationSeconds) {
		watched = true
	}
	position := progress.PositionSeconds
	if watched || !resumeEligible {
		position = 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	now := timeToMillis(time.Now().UTC())
	playIncrement := 0
	if watched {
		// A play is counted on the unwatched -> watched transition; the
		// CASE below suppresses the increment while the row stays watched.
		playIncrement = 1
	}
	explicitPlayCount := progress.PlayCount
	insertPlayCount := playIncrement
	if explicitPlayCount > insertPlayCount {
		insertPlayCount = explicitPlayCount
	}
	// occurredAt is the true historical instant (backdated imports); NULL on
	// native rows, where updated_at_ms is the event time. It deliberately does
	// NOT feed updated_at_ms (the recency key), so a backdated import never
	// surfaces as a fresh play on the recency shelves.
	var occurredAt *int64
	if !prov.OccurredAt.IsZero() {
		millis := timeToMillis(prov.OccurredAt.UTC())
		occurredAt = &millis
	}
	// Run-scoped import provenance. The additive gate lives in the
	// import handler (applyWatchState returns early on a no-op), so this write
	// only runs on a real advance — the columns are set unconditionally here.
	// Native callers pass a zero prov, so they stay NULL (nullString maps "" →
	// NULL), byte-for-byte the pre-MIGREV-7 native write.
	//
	// eventTimeMS is the ABPARITY-1 monotonic playback event-time guard input
	// (NULL when the caller sent none — pre-upgrade clients). positionAdvanced
	// (below) is the ON CONFLICT predicate deciding whether THIS write is allowed
	// to move position_seconds/updated_at_ms/event_time_ms: true when the row has
	// no event-time yet (legacy row or first write), the incoming write carries no
	// event-time (legacy client — preserves prior unconditional-overwrite
	// behavior), or the incoming event-time is >= the row's stored one (latest
	// event-time wins, ADR-0011 §3 — a late replay of a stale position is then a
	// no-op; a genuine newer position, including a deliberate rewind, still
	// advances). play_count/last_played_at_ms/provenance/import columns are NEVER
	// gated by this predicate — they advance on their own existing rules
	// regardless of the position guard.
	var eventTimeMS *int64
	if progress.EventTimeMS > 0 {
		eventTimeMS = &progress.EventTimeMS
	}
	event := playbackProjectionEvent{
		Operation:        playbackProjectionOperationProgress,
		ProfileID:        string(progress.ProfileID),
		MediaID:          string(progress.MediaID),
		PositionSeconds:  position,
		DurationSeconds:  progress.DurationSeconds,
		Watched:          watched,
		UpdatedAtMS:      now,
		PlayCount:        insertPlayCount,
		ExplicitCount:    explicitPlayCount,
		LastPlayedAtMS:   optionalMillis(insertPlayCount > 0, now),
		Provenance:       prov.Source,
		OccurredAtMS:     occurredAt,
		ImportRunID:      prov.RunID,
		ImportImporterID: prov.ImporterID,
		ImportMatch:      prov.MatchMethod,
		EventTimeMS:      eventTimeMS,
		ChannelContext:   progress.ChannelContext,
	}
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := appendPlaybackProjectionEvent(ctx, tx, event); err != nil {
		return err
	}
	if err := applyPlaybackProjectionEvent(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit()
}

// positionAdvancedPredicate is the ON CONFLICT test
// for whether an incoming playback-progress write may move position_seconds/
// watched/updated_at_ms/event_time_ms: the row has no event-time yet, the
// incoming write carries none (legacy client), or the incoming event-time is
// >= the row's stored event-time (latest-event-time-wins, not max(position) —
// a deliberate rewind is a newer event-time and still advances).
const positionAdvancedPredicate = `
	playback_progress.event_time_ms IS NULL
	OR excluded.event_time_ms IS NULL
	OR excluded.event_time_ms >= playback_progress.event_time_ms`

func optionalMillis(set bool, millis int64) *int64 {
	if !set {
		return nil
	}
	return &millis
}

// ApplyImportedPlays records imported play history onto the per-Profile
// play-count / last-played projection (ADR-0037 §2.5 Music scrobble import,
// MIG-4). A music scrobble is a play event — not a resume — so this writes only
// the engagement columns play_count + last_played_at_ms, never position or the
// watched flag (audio media has no resume point, ADR-0029 §7).
//
// It is strictly additive, so a re-import never double-applies:
//   - play_count is RAISED to at least playCount (the source's running total for
//     this Media), never lowered — re-running with the same or a smaller total
//     is a no-op, and a larger total only adds the delta;
//   - last_played_at_ms only advances FORWARD to playedAt, never backward.
//
// playedAt is the scrobble's true historical instant, carried as the
// last-played time AND as the import OccurredAt provenance, while updated_at_ms
// stays the import instant so a years-old scrobble never masquerades as a fresh
// play on the recency shelves (same recency-key discipline as
// UpsertPlaybackProgressWithProvenance). A zero playedAt leaves last-played
// unset (the play still counts). prov carries the shape-named import
// discriminator (invariant #1).
func (s *Store) ApplyImportedPlays(ctx context.Context, profileID ProfileID, mediaID MediaID, playCount int, playedAt time.Time, prov EngagementProvenance) error {
	// Authority face: imported plays author engagement — main-only.
	if err := s.guardAuthority(serverrole.FaceEngagement); err != nil {
		return err
	}
	if profileID == "" || mediaID == "" {
		return ValidationError("profile ID and media ID are required")
	}
	if playCount < 0 {
		return ValidationError("play count must not be negative")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	now := timeToMillis(time.Now().UTC())
	var playedAtMS *int64
	if !playedAt.IsZero() {
		millis := timeToMillis(playedAt.UTC())
		playedAtMS = &millis
	}
	var occurredAt *int64
	if !prov.OccurredAt.IsZero() {
		millis := timeToMillis(prov.OccurredAt.UTC())
		occurredAt = &millis
	}
	// On insert: the row is seeded with the source's full count + timestamp (no
	// resume position, not watched). On conflict: play_count rises to MAX(old,
	// source total) and last_played_at_ms advances to MAX(old, scrobble time),
	// leaving any native resume/watched state on the row untouched.
	//
	// Run-scoped import provenance: the run/importer/method
	// columns update ONLY when this re-run actually ADVANCES the row — either the
	// play_count rises or last_played advances. A pure no-op re-run (same or older
	// export) must NOT relabel the row as freshly applied by the new run, so it
	// leaves the originally-applied run's provenance intact. `provenance` and
	// `updated_at_ms` keep their pre-MIGREV-7 unconditional overwrite (updated_at
	// is the import-instant recency key, not the historical-play key; the new
	// audit columns are the ones that must stay truthful across a no-op).
	event := playbackProjectionEvent{
		Operation:        playbackProjectionOperationImport,
		ProfileID:        string(profileID),
		MediaID:          string(mediaID),
		UpdatedAtMS:      now,
		PlayCount:        playCount,
		LastPlayedAtMS:   playedAtMS,
		Provenance:       prov.Source,
		OccurredAtMS:     occurredAt,
		ImportRunID:      prov.RunID,
		ImportImporterID: prov.ImporterID,
		ImportMatch:      prov.MatchMethod,
	}
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := appendPlaybackProjectionEvent(ctx, tx, event); err != nil {
		return err
	}
	if err := applyPlaybackProjectionEvent(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit()
}

// importPlayAdvancedPredicate is the ON CONFLICT test for whether an imported
// play actually ADVANCED the projection row: the play_count
// rose above the stored total, or the last-played instant moved forward. Only
// then are the run-scoped import provenance columns relabeled to the new run; a
// pure no-op re-run leaves the originally-applied run's provenance intact.
const importPlayAdvancedPredicate = `
	excluded.play_count > playback_progress.play_count
	OR (excluded.last_played_at_ms IS NOT NULL
	    AND (playback_progress.last_played_at_ms IS NULL OR excluded.last_played_at_ms > playback_progress.last_played_at_ms))`

// PlaybackProgressFor returns the Profile's progress for one Media item.
func (s *Store) PlaybackProgressFor(ctx context.Context, profileID ProfileID, mediaID MediaID) (PlaybackProgress, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PlaybackProgress{}, false, err
	}
	var progress PlaybackProgress
	var watched int
	var updated int64
	var occurred sql.NullInt64
	var eventTime sql.NullInt64
	var runID, importerID, matchMethod sql.NullString
	err := s.eventsR.QueryRowContext(ctx, `
		SELECT profile_id, media_id, position_seconds, duration_seconds, watched, updated_at_ms, play_count, provenance, occurred_at_ms, import_run_id, import_importer_id, import_match_method, event_time_ms
		FROM playback_progress
		WHERE profile_id = ? AND media_id = ?
	`, profileID, mediaID).Scan(&progress.ProfileID, &progress.MediaID,
		&progress.PositionSeconds, &progress.DurationSeconds, &watched, &updated, &progress.PlayCount,
		&progress.Provenance, &occurred, &runID, &importerID, &matchMethod, &eventTime)
	if errors.Is(err, sql.ErrNoRows) {
		return PlaybackProgress{}, false, nil
	}
	if err != nil {
		return PlaybackProgress{}, false, err
	}
	progress.Watched = watched != 0
	progress.UpdatedAt = millisToTime(updated)
	// occurred_at_ms is NULL on native rows (OccurredAt stays zero); set only for
	// backdated imports, where it carries the true historical event time.
	if occurred.Valid {
		progress.OccurredAt = millisToTime(occurred.Int64)
	}
	// event_time_ms is NULL until a client sends a playback event-time;
	// EventTimeMS then stays the zero value.
	if eventTime.Valid {
		progress.EventTimeMS = eventTime.Int64
	}
	// Run-scoped import provenance: NULL on native rows, so the read
	// leaves the fields empty; set only on imported rows.
	progress.ImportRunID = runID.String
	progress.ImportImporterID = importerID.String
	progress.ImportMatchMethod = matchMethod.String
	return progress, true, nil
}

// extraMediaIDsSQL returns the subset of the given Media IDs that are Extras
// (ADR-0009 §3 — trailers/BTS/deleted-scenes). The engagement shelves resolve
// catalog Media through the shared visibleMediaBatchSQL / mediaForEntitySQL
// funnels, which must NOT carry the default Extra exclusion (the explicit
// "extras of X" entity-media Source rides the same funnel and must still see
// them, exactly as browse leaves the funnels alone and filters at the Source).
// So the shelves filter their own candidate set against this helper instead.
// Returns an empty set for an empty input, never touching the catalog.
func (s *Store) extraMediaIDsSQL(ctx context.Context, ids []MediaID) (map[MediaID]bool, error) {
	extras := map[MediaID]bool{}
	if len(ids) == 0 {
		return extras, nil
	}
	const chunkSize = 500
	for start := 0; start < len(ids); start += chunkSize {
		chunk := ids[start:min(start+chunkSize, len(ids))]
		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, id := range chunk {
			placeholders[i] = "?"
			args[i] = id
		}
		rows, err := s.catalogR.QueryContext(ctx, `
			SELECT m.id FROM media m
			WHERE m.id IN (`+strings.Join(placeholders, ", ")+`) AND m.extra = 1
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id MediaID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			extras[id] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return extras, nil
}

// continueWatchingSQL returns in-progress items for a Profile, most recent
// first, reveal-filtered through catalog Media visibility. Extras
// are excluded by default — a trailer/BTS clip never appears on
// the Continue Watching shelf even if the Profile has a resume point for it.
func (s *Store) continueWatchingSQL(ctx context.Context, profileID ProfileID, filter LibraryFilter, limit, offset int) ([]SourceItem, error) {
	return s.inProgressItemsSQL(ctx, profileID, filter, limit, offset)
}

// inProgressItemsSQL is the shared "in-progress" engagement projection: the
// Profile's Media started past the resume floor but not yet finished
// (watched = 0 AND position_seconds >= resumeFloorSeconds), most-recent first,
// reveal-filtered and Extras-excluded. It backs both continue-watching (the
// discovery shelf, ADR-0028) and the resume-progress filter predicate's
// "in-progress" band — one query, one definition of "in
// progress," so the shelf and the filter can never disagree.
//
// channel_context = 0 excludes channel-context playback:
// a tuned Library Channel's grazed partial plays are flagged and must not poison
// Continue Watching — the Channel itself is the resumable object. The exclusion
// is here, in the single shared projection, so both the shelf and the filter
// drop channel plays by construction.
func (s *Store) inProgressItemsSQL(ctx context.Context, profileID ProfileID, filter LibraryFilter, limit, offset int) ([]SourceItem, error) {
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT media_id, position_seconds, duration_seconds
		FROM playback_progress
		WHERE profile_id = ? AND watched = 0 AND position_seconds >= ? AND channel_context = 0
		ORDER BY updated_at_ms DESC
		LIMIT ? OFFSET ?
	`, profileID, resumeFloorSeconds, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type progressRow struct {
		mediaID         MediaID
		positionSeconds int
		durationSeconds int
	}
	var candidates []progressRow
	for rows.Next() {
		var row progressRow
		if err := rows.Scan(&row.mediaID, &row.positionSeconds, &row.durationSeconds); err != nil {
			return nil, err
		}
		candidates = append(candidates, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	ids := make([]MediaID, len(candidates))
	for i := range candidates {
		ids[i] = candidates[i].mediaID
	}
	visible, err := s.visibleMediaBatchSQL(ctx, ids, filter)
	if err != nil {
		return nil, err
	}
	extras, err := s.extraMediaIDsSQL(ctx, ids)
	if err != nil {
		return nil, err
	}

	items := []SourceItem{}
	for _, candidate := range candidates {
		media, ok := visible[candidate.mediaID]
		if !ok {
			continue // hidden or deleted media never reaches the wire
		}
		if extras[candidate.mediaID] {
			continue // Extras excluded by default (ADR-0009 §3)
		}
		items = append(items, SourceItem{
			ID:      media.ID,
			Kind:    "media",
			Title:   media.Title,
			Summary: remainingSummary(candidate.positionSeconds, candidate.durationSeconds),
		})
	}
	return items, nil
}

// resumeProgressSQL backs the "resume-progress" Source predicate (ADR-0052
// §2.6): the per-Profile playback-progress axis. Only the "in-progress" band
// is server-evaluated — it reuses the same engagement projection
// continue-watching reads (started past the resume floor, not finished), so
// the predicate and the shelf share one definition. An unrecognized value
// yields an empty feed (never an error), like a missing entity.
func (s *Store) resumeProgressSQL(ctx context.Context, profileID ProfileID, value string, filter LibraryFilter, limit, offset int) ([]SourceItem, error) {
	if value != "in-progress" {
		return []SourceItem{}, nil
	}
	return s.inProgressItemsSQL(ctx, profileID, filter, limit, offset)
}

// watchedByAnyProfileSQL backs the "watched-by-any-profile" Source predicate:
// a household aggregate boolean over Profile-scoped playback
// rows. It never names the Profile that watched the Media.
func (s *Store) watchedByAnyProfileSQL(ctx context.Context, value string, filter LibraryFilter, limit, offset int) ([]SourceItem, error) {
	if value != "any" && value != "none" {
		return []SourceItem{}, nil
	}
	watchedIDs, err := s.watchedMediaIDsSQL(ctx)
	if err != nil {
		return nil, err
	}
	if value == "any" && len(watchedIDs) == 0 {
		return []SourceItem{}, nil
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	var visible []LibraryID
	for _, library := range libraries {
		if libraryVisible(library, filter) {
			visible = append(visible, library.ID)
		}
	}
	if len(visible) == 0 {
		return []SourceItem{}, nil
	}

	libraryPlaceholders := make([]string, len(visible))
	args := make([]any, 0, len(visible)+len(watchedIDs)+2)
	for i, id := range visible {
		libraryPlaceholders[i] = "?"
		args = append(args, id)
	}
	var bandClause string
	if len(watchedIDs) > 0 {
		watchedPlaceholders := make([]string, len(watchedIDs))
		for i, id := range watchedIDs {
			watchedPlaceholders[i] = "?"
			args = append(args, id)
		}
		operator := "IN"
		if value == "none" {
			operator = "NOT IN"
		}
		bandClause = ` AND m.id ` + operator + ` (` + strings.Join(watchedPlaceholders, ", ") + `)`
	}
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	args = append(args, certArgs...)
	args = append(args, limit, offset)
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT m.id, m.title, m.duration_seconds
		FROM media m
		WHERE m.library_id IN (`+strings.Join(libraryPlaceholders, ", ")+`)
		  AND m.trashed_at_ms IS NULL`+mediaExcludeExtrasClause+certClause+bandClause+`
		ORDER BY m.title COLLATE NOCASE, m.id
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SourceItem{}
	for rows.Next() {
		var media Media
		if err := rows.Scan(&media.ID, &media.Title, &media.DurationSeconds); err != nil {
			return nil, err
		}
		items = append(items, SourceItem{
			ID:      media.ID,
			Kind:    "media",
			Title:   media.Title,
			Summary: mediaDurationSummary(media.DurationSeconds),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Store) watchedMediaIDsSQL(ctx context.Context) ([]MediaID, error) {
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT DISTINCT media_id
		FROM playback_progress
		WHERE watched = 1
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []MediaID
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func remainingSummary(positionSeconds, durationSeconds int) string {
	if durationSeconds <= positionSeconds {
		return ""
	}
	remaining := durationSeconds - positionSeconds
	minutes := remaining / 60
	if remaining%60 != 0 {
		minutes++
	}
	if minutes <= 1 {
		return "1 min left"
	}
	return strconv.Itoa(minutes) + " min left"
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// nextUpSQL backs the "next-up" Source kind: for each ordered
// container the Profile has engagement in, the first unwatched child in
// play-next order — one item per container, containers ordered by recency of
// the Profile's last engagement within them.
//
// Distinct from continue-watching by construction (the ADR's separate-shelves
// ruling — Plex's merged On Deck is the cautionary tale): when a container's
// frontier child is itself resumable (in progress past the resume floor), the
// container yields nothing here — that child already lives on
// continue-watching, and skipping past it would advance the shelf beyond what
// the Profile has actually finished. Barely-started children (under the
// resume floor) count as unwatched, mirroring continue-watching's floor, so
// no item falls between the two shelves.
//
// Ordered containers are recognized by their data, never the manifest: a
// relation row carrying a sort_order is the play-next seed (only
// OrderedChildren container kinds get one written), which keeps the store
// free of plugin-manifest knowledge and keeps unordered fan-out entities
// (category-style relations) off the shelf.
//
// Engagement rows live in the events database and relations in the catalog,
// so the composition happens here rather than in one SQL statement. Child
// visibility (reveal, certification, trash, Library allowlist) rides on
// mediaForEntitySQL's standard LibraryFilter funnel; a container hidden under
// the filter is skipped whole.
func (s *Store) nextUpSQL(ctx context.Context, profileID ProfileID, filter LibraryFilter, limit, offset int) ([]SourceItem, error) {
	type progress struct {
		watched    bool
		inProgress bool
		updatedAt  int64
	}
	windowStart := timeToMillis(time.Now().UTC()) - nextUpEngagementWindowMS
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT media_id, watched, position_seconds, updated_at_ms
		FROM playback_progress
		WHERE profile_id = ? AND updated_at_ms > ?
		LIMIT ?
	`, profileID, windowStart, nextUpRowLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	progressByMedia := map[MediaID]progress{}
	for rows.Next() {
		var mediaID MediaID
		var watched, positionSeconds int
		var updatedAt int64
		if err := rows.Scan(&mediaID, &watched, &positionSeconds, &updatedAt); err != nil {
			return nil, err
		}
		progressByMedia[mediaID] = progress{
			watched:    watched != 0,
			inProgress: watched == 0 && positionSeconds >= resumeFloorSeconds,
			updatedAt:  updatedAt,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(progressByMedia) == 0 {
		return []SourceItem{}, nil
	}

	// Engaged ordered containers, with the recency of the Profile's last
	// engagement in each (catalog database; chunked IN over engaged Media).
	engaged := make([]MediaID, 0, len(progressByMedia))
	for id := range progressByMedia {
		engaged = append(engaged, id)
	}
	sort.Slice(engaged, func(i, j int) bool { return engaged[i] < engaged[j] })
	lastEngagedMs := map[EntityID]int64{}
	const chunkSize = 500
	for start := 0; start < len(engaged); start += chunkSize {
		chunk := engaged[start:min(start+chunkSize, len(engaged))]
		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, id := range chunk {
			placeholders[i] = "?"
			args[i] = id
		}
		relationRows, err := s.catalogR.QueryContext(ctx, `
			SELECT from_entity_id, to_media_id
			FROM entity_relation
			WHERE sort_order IS NOT NULL AND to_media_id IN (`+strings.Join(placeholders, ", ")+`)
		`, args...)
		if err != nil {
			return nil, err
		}
		for relationRows.Next() {
			var containerID EntityID
			var mediaID MediaID
			if err := relationRows.Scan(&containerID, &mediaID); err != nil {
				relationRows.Close()
				return nil, err
			}
			if engagedAt := progressByMedia[mediaID].updatedAt; engagedAt > lastEngagedMs[containerID] {
				lastEngagedMs[containerID] = engagedAt
			}
		}
		if err := relationRows.Err(); err != nil {
			relationRows.Close()
			return nil, err
		}
		relationRows.Close()
	}
	containers := make([]EntityID, 0, len(lastEngagedMs))
	for id := range lastEngagedMs {
		containers = append(containers, id)
	}
	sort.Slice(containers, func(i, j int) bool {
		if lastEngagedMs[containers[i]] != lastEngagedMs[containers[j]] {
			return lastEngagedMs[containers[i]] > lastEngagedMs[containers[j]]
		}
		return containers[i] < containers[j]
	})

	// Per container, the frontier: the first visible child in play-next
	// order the Profile has not watched. A frontier that is resumable
	// belongs to continue-watching, so the container yields nothing. The
	// same Media fronting two containers (a child in its container and in
	// an ordered Collection) appears once, for the most recent.
	items := []SourceItem{}
	seen := map[MediaID]bool{}
	for _, containerID := range containers {
		children, err := s.mediaForEntitySQL(ctx, containerID, filter, -1, 0)
		if err != nil {
			if errors.Is(err, errVisibleEntityNotFound) {
				continue // container hidden under the filter never reaches the wire
			}
			return nil, err
		}
		// Extras are excluded from next-up by default: a trailer
		// sharing the container must never be picked as the frontier child.
		// mediaForEntitySQL is the shared funnel (also the explicit "extras of
		// X" Source), so the exclusion is applied here at the shelf, not in it.
		childIDs := make([]MediaID, len(children))
		for i := range children {
			childIDs[i] = children[i].ID
		}
		childExtras, err := s.extraMediaIDsSQL(ctx, childIDs)
		if err != nil {
			return nil, err
		}
		var frontier *SourceItem
		for i := range children {
			if childExtras[children[i].ID] {
				continue
			}
			p, engaged := progressByMedia[children[i].ID]
			if engaged && p.watched {
				continue
			}
			if !engaged || !p.inProgress {
				frontier = &children[i]
			}
			break
		}
		if frontier == nil || seen[frontier.ID] {
			continue
		}
		seen[frontier.ID] = true
		items = append(items, *frontier)
		if len(items) >= offset+limit {
			break
		}
	}
	if offset >= len(items) {
		return []SourceItem{}, nil
	}
	items = items[offset:]
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

// playedItemsSQL backs the recently-played and most-played Source kinds:
// engagement-projection-backed, reveal-filtered, generic.
//
// The events-DB page is fetched first (ordered, cheap), then all candidate IDs
// are resolved in a single visibleMediaBatchSQL call instead of one
// visibleMediaSQL per row — eliminating the N+1 that caused one catalog round-
// trip per played item.
func (s *Store) playedItemsSQL(ctx context.Context, profileID ProfileID, filter LibraryFilter, orderBy string, limit, offset int) ([]SourceItem, error) {
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT media_id, play_count
		FROM playback_progress
		WHERE profile_id = ? AND play_count > 0
		ORDER BY `+orderBy+`
		LIMIT ? OFFSET ?
	`, profileID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type playedRow struct {
		mediaID   MediaID
		playCount int
	}
	var candidates []playedRow
	for rows.Next() {
		var row playedRow
		if err := rows.Scan(&row.mediaID, &row.playCount); err != nil {
			return nil, err
		}
		candidates = append(candidates, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return []SourceItem{}, nil
	}
	ids := make([]MediaID, len(candidates))
	for i, c := range candidates {
		ids[i] = c.mediaID
	}
	visible, err := s.visibleMediaBatchSQL(ctx, ids, filter)
	if err != nil {
		return nil, err
	}
	extras, err := s.extraMediaIDsSQL(ctx, ids)
	if err != nil {
		return nil, err
	}
	items := make([]SourceItem, 0, len(candidates))
	for _, candidate := range candidates {
		media, ok := visible[candidate.mediaID]
		if !ok {
			continue
		}
		if extras[candidate.mediaID] {
			continue // Extras excluded from recently/most-played by default (ADR-0009 §3)
		}
		summary := "1 play"
		if candidate.playCount != 1 {
			summary = strconv.Itoa(candidate.playCount) + " plays"
		}
		items = append(items, SourceItem{
			ID:      media.ID,
			Kind:    "media",
			Title:   media.Title,
			Summary: summary,
		})
	}
	return items, nil
}

// ListeningStats is the Profile-scoped playback rollup for music-style summary
// surfaces. All aggregation starts from rows whose Media survives LibraryFilter,
// so hidden/restricted-unrevealed/Sealed items do not affect counts or ordering.
type ListeningStats struct {
	Period     string
	TopTracks  []ListeningStatsItem
	TopArtists []ListeningStatsItem
	TopAlbums  []ListeningStatsItem
	Summary    ListeningPeriodSummary
}

type ListeningStatsItem struct {
	ID        string
	Kind      string
	Title     string
	PlayCount int
}

type ListeningPeriodSummary struct {
	Plays       int
	TrackCount  int
	ArtistCount int
	AlbumCount  int
}

type listeningPlay struct {
	mediaID      MediaID
	title        string
	playCount    int
	lastPlayedMS int64
}

// ListeningStatsFor returns recently-played/top-track/top-entity summaries for
// one Profile. artistKinds/albumKinds are plugin-owned entity kind names; Core
// groups only by those opaque strings.
func (s *Store) ListeningStatsFor(ctx context.Context, profileID ProfileID, filter LibraryFilter, period string, artistKinds, albumKinds []string, limit int) (ListeningStats, error) {
	if profileID == "" {
		return ListeningStats{}, ValidationError("profile ID is required")
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	startMS, label := listeningPeriodStart(period)

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ListeningStats{}, err
	}

	plays, err := s.visibleListeningPlaysLocked(ctx, profileID, filter, startMS)
	if err != nil {
		return ListeningStats{}, err
	}
	sort.Slice(plays, func(i, j int) bool {
		if plays[i].playCount != plays[j].playCount {
			return plays[i].playCount > plays[j].playCount
		}
		if plays[i].lastPlayedMS != plays[j].lastPlayedMS {
			return plays[i].lastPlayedMS > plays[j].lastPlayedMS
		}
		return plays[i].mediaID < plays[j].mediaID
	})

	stats := ListeningStats{Period: label}
	trackSeen := map[MediaID]bool{}
	for _, p := range plays {
		stats.Summary.Plays += p.playCount
		trackSeen[p.mediaID] = true
		if len(stats.TopTracks) < limit {
			stats.TopTracks = append(stats.TopTracks, ListeningStatsItem{
				ID: string(p.mediaID), Kind: "track", Title: p.title, PlayCount: p.playCount,
			})
		}
	}
	stats.Summary.TrackCount = len(trackSeen)

	stats.TopArtists, stats.Summary.ArtistCount, err = s.topListeningEntitiesLocked(ctx, plays, artistKinds, "artist", limit)
	if err != nil {
		return ListeningStats{}, err
	}
	stats.TopAlbums, stats.Summary.AlbumCount, err = s.topListeningEntitiesLocked(ctx, plays, albumKinds, "album", limit)
	if err != nil {
		return ListeningStats{}, err
	}
	return stats, nil
}

func (s *Store) listeningStatsSourceItems(ctx context.Context, profileID ProfileID, filter LibraryFilter, query SourceQuery, limit, offset int) ([]SourceItem, error) {
	artistKinds := []string{"artist"}
	if query.Kind == "top-artists" && strings.TrimSpace(query.EntityKind) != "" {
		artistKinds = []string{strings.TrimSpace(query.EntityKind)}
	}
	albumKinds := []string{"album"}
	if query.Kind == "top-albums" && strings.TrimSpace(query.EntityKind) != "" {
		albumKinds = []string{strings.TrimSpace(query.EntityKind)}
	}
	stats, err := s.ListeningStatsFor(ctx, profileID, filter, query.Value, artistKinds, albumKinds, limit+offset)
	if err != nil {
		return nil, err
	}
	var items []SourceItem
	switch query.Kind {
	case "top-tracks":
		items = listeningStatsSourceItemsFrom(stats.TopTracks)
	case "top-artists":
		items = listeningStatsSourceItemsFrom(stats.TopArtists)
	case "top-albums":
		items = listeningStatsSourceItemsFrom(stats.TopAlbums)
	case "listening-summary":
		items = []SourceItem{{
			ID:      MediaID(stats.Period),
			Kind:    "listening-summary",
			Title:   stats.Period,
			Summary: strconv.Itoa(stats.Summary.Plays) + " plays across " + strconv.Itoa(stats.Summary.TrackCount) + " tracks",
		}}
	}
	if offset >= len(items) {
		return []SourceItem{}, nil
	}
	items = items[offset:]
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func listeningStatsSourceItemsFrom(stats []ListeningStatsItem) []SourceItem {
	items := make([]SourceItem, len(stats))
	for i, item := range stats {
		summary := "1 play"
		if item.PlayCount != 1 {
			summary = strconv.Itoa(item.PlayCount) + " plays"
		}
		items[i] = SourceItem{ID: MediaID(item.ID), Kind: item.Kind, Title: item.Title, Summary: summary}
	}
	return items
}

func listeningPeriodStart(period string) (int64, string) {
	now := time.Now().UTC()
	if period == "" || period == "all" {
		return 0, "all"
	}
	if year, err := strconv.Atoi(period); err == nil && year >= 1 {
		return timeToMillis(time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)), period
	}
	if period == "year" {
		return timeToMillis(time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)), strconv.Itoa(now.Year())
	}
	return 0, "all"
}

func (s *Store) visibleListeningPlaysLocked(ctx context.Context, profileID ProfileID, filter LibraryFilter, startMS int64) ([]listeningPlay, error) {
	query := `
		SELECT media_id, play_count, COALESCE(last_played_at_ms, updated_at_ms)
		FROM playback_progress
		WHERE profile_id = ? AND play_count > 0`
	args := []any{profileID}
	if startMS > 0 {
		query += ` AND COALESCE(last_played_at_ms, updated_at_ms) >= ?`
		args = append(args, startMS)
	}
	rows, err := s.eventsR.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type row struct {
		mediaID      MediaID
		playCount    int
		lastPlayedMS int64
	}
	var raw []row
	var ids []MediaID
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.mediaID, &r.playCount, &r.lastPlayedMS); err != nil {
			return nil, err
		}
		raw = append(raw, r)
		ids = append(ids, r.mediaID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	visible, err := s.visibleMediaBatchSQL(ctx, ids, filter)
	if err != nil {
		return nil, err
	}
	extras, err := s.extraMediaIDsSQL(ctx, ids)
	if err != nil {
		return nil, err
	}
	plays := make([]listeningPlay, 0, len(raw))
	for _, r := range raw {
		media, ok := visible[r.mediaID]
		if !ok || extras[r.mediaID] {
			continue
		}
		plays = append(plays, listeningPlay{
			mediaID: r.mediaID, title: media.Title, playCount: r.playCount, lastPlayedMS: r.lastPlayedMS,
		})
	}
	return plays, nil
}

func (s *Store) topListeningEntitiesLocked(ctx context.Context, plays []listeningPlay, kinds []string, kindLabel string, limit int) ([]ListeningStatsItem, int, error) {
	if len(plays) == 0 || len(kinds) == 0 {
		return []ListeningStatsItem{}, 0, nil
	}
	playByMedia := map[MediaID]listeningPlay{}
	ids := make([]MediaID, 0, len(plays))
	for _, p := range plays {
		playByMedia[p.mediaID] = p
		ids = append(ids, p.mediaID)
	}
	kindSet := map[string]bool{}
	for _, k := range kinds {
		if k = strings.TrimSpace(k); k != "" {
			kindSet[k] = true
		}
	}
	if len(kindSet) == 0 {
		return []ListeningStatsItem{}, 0, nil
	}

	type agg struct {
		id           EntityID
		title        string
		playCount    int
		lastPlayedMS int64
	}
	byEntity := map[EntityID]*agg{}
	const chunkSize = 500
	for start := 0; start < len(ids); start += chunkSize {
		chunk := ids[start:min(start+chunkSize, len(ids))]
		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, id := range chunk {
			placeholders[i] = "?"
			args[i] = id
		}
		rows, err := s.catalogR.QueryContext(ctx, `
			SELECT e.id, e.kind, e.title, er.to_media_id
			FROM entity_relation er
			JOIN entity e ON e.id = er.from_entity_id
			WHERE er.to_media_id IN (`+strings.Join(placeholders, ", ")+`)
		`, args...)
		if err != nil {
			return nil, 0, err
		}
		for rows.Next() {
			var entityID EntityID
			var kind, title string
			var mediaID MediaID
			if err := rows.Scan(&entityID, &kind, &title, &mediaID); err != nil {
				rows.Close()
				return nil, 0, err
			}
			if !kindSet[kind] {
				continue
			}
			p := playByMedia[mediaID]
			a := byEntity[entityID]
			if a == nil {
				a = &agg{id: entityID, title: title}
				byEntity[entityID] = a
			}
			a.playCount += p.playCount
			if p.lastPlayedMS > a.lastPlayedMS {
				a.lastPlayedMS = p.lastPlayedMS
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, 0, err
		}
		rows.Close()
	}
	entities := make([]agg, 0, len(byEntity))
	for _, a := range byEntity {
		entities = append(entities, *a)
	}
	sort.Slice(entities, func(i, j int) bool {
		if entities[i].playCount != entities[j].playCount {
			return entities[i].playCount > entities[j].playCount
		}
		if entities[i].lastPlayedMS != entities[j].lastPlayedMS {
			return entities[i].lastPlayedMS > entities[j].lastPlayedMS
		}
		return entities[i].id < entities[j].id
	})
	out := make([]ListeningStatsItem, 0, min(limit, len(entities)))
	for _, e := range entities {
		if len(out) >= limit {
			break
		}
		out = append(out, ListeningStatsItem{ID: string(e.id), Kind: kindLabel, Title: e.title, PlayCount: e.playCount})
	}
	return out, len(entities), nil
}
