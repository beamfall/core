package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

// My Data: the Viewer-resident privacy panel's store half. A
// session can export and hard-DELETE its own Profile's engagement data. The
// profile is always the caller's own — there is no other-profile parameter on
// any method here; the graph layer passes scope.ProfileID and nothing else.

// MyEngagementExport is the self-describing export payload: the
// caller's engagement event log and projections. Reveal-filtered: rows that
// reference Media in restricted Libraries appear only while the session is
// revealed (the export's filter is the caller's current LibraryFilter). The
// Reveal contract outranks export completeness — a revealed session exports
// everything; a relocked one exports only what it can currently see.
type MyEngagementExport struct {
	Profile           MyProfileMeta            `json:"profile"`
	Complete          bool                     `json:"complete"`
	ExcludedLibraries []MyDataExcludedLibrary  `json:"excludedLibraries"`
	PlaybackProgress  []MyPlaybackProgress     `json:"playbackProgress"`
	Favorites         []MyFavorite             `json:"favorites"`
	Queue             []MyQueueEntry           `json:"queue"`
	SavedFilters      []MySavedFilter          `json:"savedFilters"`
	Stations          []MySavedStation         `json:"stations"`
	Collections       []MyCollection           `json:"collections"`
	Markers           []MyMarker               `json:"markers"`
	Mutes             []MuteTarget             `json:"mutes"`
	EngagementEvents  []MyEngagementEventEntry `json:"engagementEvents"`
	// Connections is the ADR-0036 "Where your data goes" row: active third-party
	// links for this Profile (ADR-0049 scrobble links and ADR-0058 Integration
	// Plugin profile links). It is a TRANSPARENCY citizen — it names the service
	// and an optional non-secret account label/ref, NEVER the OAuth token/session
	// key (those live only in the 0600 secure store and are excluded from the
	// export by construction — see MyScrobbleConnection; invariant #5).
	Connections []MyScrobbleConnection `json:"connections"`
}

type MyDataExcludedLibrary struct {
	LibraryID LibraryID `json:"libraryID"`
	Reason    string    `json:"reason"`
}

const myDataSealedLibraryReason = "sealed library excluded; unseal to include"

// MyScrobbleConnection is one exported third-party link for the My-Data "Where
// your data goes" panel (ADR-0049 §2.9, ADR-0058 profile-link). It is
// deliberately NON-SECRET: it carries the Service/Plugin the Profile linked, an
// optional human-readable AccountLabel/ref, and the LinkedAt instant — and
// pointedly has NO field for the credential. The backing row may hold only a
// secret-store ref (scrobble) or an opaque non-secret account ref (integration
// profile-link); the export never carries token material or a token handle
// (invariant #5). A reviewer adding a "token" or "secretRef" field here would
// be making the leak explicit rather than slipping it in;
// TestScrobbleExportCarriesNoToken asserts the marshaled bytes hold no secret.
type MyScrobbleConnection struct {
	Service      string    `json:"service"`
	AccountLabel string    `json:"accountLabel,omitempty"`
	LinkedAt     time.Time `json:"linkedAt"`
}

// MyEngagementEventEntry is one exported row of the ADR-0005 §1 engagement event
// log. It is Profile-private behavioral data, so it joins the My-Data export.
// The plugin-declared Kind is carried verbatim (Core never names it); Value is
// the signed event value (for example, a +1 increment or a -1 remove-last
// correction). A row may target a Media and/or an Entity (the entity-aggregate
// target); both ids are exported so the timeline is reconstructible. Reveal: a
// row that references a Media is
// dropped while that Media is hidden, exactly like the playback rows; an
// Entity-only row carries no Media reference and is exported whole (the entity
// roll-up is keyed by Entity, not the per-Media Reveal filter).
type MyEngagementEventEntry struct {
	Kind       string    `json:"kind"`
	MediaID    MediaID   `json:"mediaID,omitempty"`
	EntityID   EntityID  `json:"entityID,omitempty"`
	Value      int       `json:"value"`
	Payload    string    `json:"payload,omitempty"`
	OccurredAt time.Time `json:"occurredAt"`
}

// MyProfileMeta is the exported Profile metadata: display name,
// kind, and the creation timestamp (epoch ms; 0 for Profiles predating
// migration 0069, which the export wire then omits).
type MyProfileMeta struct {
	ID          ProfileID `json:"id"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	CreatedAtMs int64     `json:"createdAtMs"`
}

// MySavedFilter is one exported Saved Filter (ADR-0036 §2.7: name + serialized
// filter expression). LibraryID is carried so an importer can re-home it.
type MySavedFilter struct {
	Name       string    `json:"name"`
	LibraryID  LibraryID `json:"libraryID"`
	FilterJSON string    `json:"filterJSON"`
	CreatedAt  time.Time `json:"createdAt"`
}

// MySavedStation is one exported saved Station (ADR-0098 area 5 / ADR-0036
// §2.7). A Station is product language over a generated Play Queue, so the
// export carries only the Profile-owned generation inputs — name, seed, rules,
// tuning (opaque JSON) — and NEVER a generated track order (which a Station
// never stores). LibraryID is carried so an importer can re-home it.
type MySavedStation struct {
	Name       string    `json:"name"`
	LibraryID  LibraryID `json:"libraryID"`
	SeedJSON   string    `json:"seedJSON"`
	RulesJSON  string    `json:"rulesJSON"`
	TuningJSON string    `json:"tuningJSON"`
	CreatedAt  time.Time `json:"createdAt"`
}

// MyCollection is one exported Collection the Profile authored.
// SourceExpr is the opaque membership expression; the server never interprets
// it. (Beamfall's per-Profile Collection is a profile-authored SmartCollection
// — there is no separate static-list Collection entity.)
type MyCollection struct {
	Name       string    `json:"name"`
	LibraryID  LibraryID `json:"libraryID"`
	SourceExpr string    `json:"sourceExpr"`
	CreatedAt  time.Time `json:"createdAt"`
}

// MyMarker is one exported Marker the Profile authored (ADR-0036 §2.7: media
// fingerprint + timestamp + kind + tag list). Media is identified by id only.
type MyMarker struct {
	MediaID      MediaID  `json:"mediaID"`
	Title        string   `json:"title"`
	StartSeconds float64  `json:"startSeconds"`
	EndSeconds   *float64 `json:"endSeconds,omitempty"`
	SegmentKind  string   `json:"segmentKind,omitempty"`
	Tags         []string `json:"tags"`
}

// MyPlaybackProgress is one exported resume/play-count projection row.
type MyPlaybackProgress struct {
	MediaID         MediaID   `json:"mediaID"`
	PositionSeconds int       `json:"positionSeconds"`
	DurationSeconds int       `json:"durationSeconds"`
	Watched         bool      `json:"watched"`
	PlayCount       int       `json:"playCount"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// MyFavorite is one exported favorite row.
type MyFavorite struct {
	MediaID     MediaID   `json:"mediaID"`
	FavoritedAt time.Time `json:"favoritedAt"`
}

// MyQueueEntry is one exported play-queue row, in queue order.
type MyQueueEntry struct {
	MediaID MediaID   `json:"mediaID"`
	AddedAt time.Time `json:"addedAt"`
}

// MyDataDeleteScope is the closed vocabulary of deleteMyEngagement targets:
// the whole engagement log, or one Media item's rows.
type MyDataDeleteScope string

const (
	// MyDataDeleteAll hard-deletes every engagement row for the Profile,
	// including rows that reference currently-hidden Media: deleting your own
	// data must not require revealing first (deletion is not disclosure).
	MyDataDeleteAll MyDataDeleteScope = "all"
	// MyDataDeleteMedia hard-deletes one Media item's rows, resolved through
	// the caller's visibility filter first — a hidden id fails identically to
	// an unknown one (delete-by-id is otherwise an existence oracle).
	MyDataDeleteMedia MyDataDeleteScope = "media"
)

// ExportMyEngagement returns the caller's reveal-filtered engagement export.
func (s *Store) ExportMyEngagement(ctx context.Context, profileID ProfileID, filter LibraryFilter) (MyEngagementExport, error) {
	if profileID == "" {
		return MyEngagementExport{}, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return MyEngagementExport{}, err
	}

	meta, err := s.myProfileMeta(ctx, profileID)
	if err != nil {
		return MyEngagementExport{}, err
	}
	progress, err := s.myPlaybackProgress(ctx, profileID)
	if err != nil {
		return MyEngagementExport{}, err
	}
	favorites, err := s.myFavorites(ctx, profileID)
	if err != nil {
		return MyEngagementExport{}, err
	}
	queue, err := s.myQueueRows(ctx, profileID)
	if err != nil {
		return MyEngagementExport{}, err
	}
	savedFilters, err := s.mySavedFilters(ctx, profileID, filter)
	if err != nil {
		return MyEngagementExport{}, err
	}
	stations, err := s.mySavedStations(ctx, profileID, filter)
	if err != nil {
		return MyEngagementExport{}, err
	}
	collections, err := s.myCollections(ctx, profileID, filter)
	if err != nil {
		return MyEngagementExport{}, err
	}
	markers, err := s.myMarkers(ctx, profileID, filter)
	if err != nil {
		return MyEngagementExport{}, err
	}
	// The mute list is a My-Data export citizen. It is keyed by
	// Entity/Tag id, not Media, so it is NOT part of the per-Media Reveal filter
	// below — the negative Source predicate that consumes it is where Reveal
	// applies (mute.go), not the list itself. The list is small and
	// Profile-private; the export reports it whole.
	mutes, err := s.mutedTargetsLocked(ctx, profileID)
	if err != nil {
		return MyEngagementExport{}, err
	}
	// The engagement event log is Profile-private behavioral data, so it is a
	// My-Data export citizen. Media-targeted rows are reveal-filtered below with
	// the rest; Entity-only rows carry no Media reference and export whole (the
	// entity roll-up is keyed by Entity).
	engagementEvents, err := s.myEngagementEventsLocked(ctx, profileID)
	if err != nil {
		return MyEngagementExport{}, err
	}
	// The active third-party connections (ADR-0049 §2.9, ADR-0058 profile-link):
	// transparency rows, not behavioral data, and NOT reveal-filtered (a link is
	// per-Profile, names no Media). The reads drop secret refs so the export
	// carries no token handle.
	connections, err := s.myScrobbleConnectionsLocked(ctx, profileID)
	if err != nil {
		return MyEngagementExport{}, err
	}
	pluginLinks, err := s.myPluginProfileLinksLocked(ctx, profileID)
	if err != nil {
		return MyEngagementExport{}, err
	}
	connections = append(connections, pluginLinks...)

	// Reveal filtering: gather every Media id the export references and resolve
	// visibility once. A row whose Media the caller cannot currently see is
	// dropped from the export — the Reveal contract outranks completeness.
	ids := make([]MediaID, 0, len(progress)+len(favorites)+len(queue)+len(engagementEvents))
	for _, row := range engagementEvents {
		if row.MediaID != "" {
			ids = append(ids, row.MediaID)
		}
	}
	for _, row := range progress {
		ids = append(ids, row.MediaID)
	}
	for _, row := range favorites {
		ids = append(ids, row.MediaID)
	}
	for _, row := range queue {
		ids = append(ids, row.MediaID)
	}
	visible, err := s.visibleMediaBatchSQL(ctx, ids, filter)
	if err != nil {
		return MyEngagementExport{}, err
	}

	excludedLibraries := sealedLibraryExclusions(filter)
	export := MyEngagementExport{
		Profile:           meta,
		Complete:          len(excludedLibraries) == 0,
		ExcludedLibraries: excludedLibraries,
		PlaybackProgress:  []MyPlaybackProgress{},
		Favorites:         []MyFavorite{},
		Queue:             []MyQueueEntry{},
		SavedFilters:      savedFilters,
		Stations:          stations,
		Collections:       collections,
		Markers:           markers,
		Mutes:             mutes,
		EngagementEvents:  []MyEngagementEventEntry{},
		Connections:       connections,
	}
	if export.Mutes == nil {
		export.Mutes = []MuteTarget{}
	}
	for _, row := range engagementEvents {
		// A Media-targeted event appears only while its Media is visible; an
		// Entity-only event has no Media reference and always exports.
		if row.MediaID != "" {
			if _, ok := visible[row.MediaID]; !ok {
				continue
			}
		}
		export.EngagementEvents = append(export.EngagementEvents, row)
	}
	for _, row := range progress {
		if _, ok := visible[row.MediaID]; ok {
			export.PlaybackProgress = append(export.PlaybackProgress, row)
		}
	}
	for _, row := range favorites {
		if _, ok := visible[row.MediaID]; ok {
			export.Favorites = append(export.Favorites, row)
		}
	}
	for _, row := range queue {
		if _, ok := visible[row.MediaID]; ok {
			export.Queue = append(export.Queue, row)
		}
	}
	return export, nil
}

func sealedLibraryExclusions(filter LibraryFilter) []MyDataExcludedLibrary {
	if len(filter.SealedIDs) == 0 {
		return nil
	}
	ids := append([]LibraryID(nil), filter.SealedIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]MyDataExcludedLibrary, 0, len(ids))
	for _, id := range ids {
		out = append(out, MyDataExcludedLibrary{
			LibraryID: id,
			Reason:    myDataSealedLibraryReason,
		})
	}
	return out
}

// myProfileMeta reads the caller's Profile metadata for the export (ADR-0036
// §2.7). Name and kind only — the profile schema has no created date.
func (s *Store) myProfileMeta(ctx context.Context, profileID ProfileID) (MyProfileMeta, error) {
	meta := MyProfileMeta{ID: profileID}
	err := s.catalogR.QueryRowContext(ctx,
		`SELECT name, kind, created_at_ms FROM profile WHERE id = ?`, profileID).Scan(&meta.Name, &meta.Kind, &meta.CreatedAtMs)
	if errors.Is(err, sql.ErrNoRows) {
		return MyProfileMeta{}, ValidationError("profile not found")
	}
	if err != nil {
		return MyProfileMeta{}, err
	}
	return meta, nil
}

// myScrobbleConnectionsLocked reads the caller's active scrobble links for the
// "Where your data goes" export row. It is the lock-free body
// the export calls while already holding s.mu; the SELECT deliberately omits
// secret_ref so no token handle ever reaches the archive (invariant #5).
func (s *Store) myScrobbleConnectionsLocked(ctx context.Context, profileID ProfileID) ([]MyScrobbleConnection, error) {
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT service, account_label, linked_at_ms
		FROM scrobble_link
		WHERE profile_id = ?
		ORDER BY service
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	connections := []MyScrobbleConnection{}
	for rows.Next() {
		var conn MyScrobbleConnection
		var linkedAt int64
		if err := rows.Scan(&conn.Service, &conn.AccountLabel, &linkedAt); err != nil {
			return nil, err
		}
		conn.LinkedAt = millisToTime(linkedAt)
		connections = append(connections, conn)
	}
	return connections, rows.Err()
}

func (s *Store) myPluginProfileLinksLocked(ctx context.Context, profileID ProfileID) ([]MyScrobbleConnection, error) {
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT key, value, updated_at_ms
		FROM server_config
		WHERE key LIKE ?
		ORDER BY key
	`, pluginProfileLinkPrefix+"%."+string(profileID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	connections := []MyScrobbleConnection{}
	for rows.Next() {
		var key, ref string
		var updatedAt int64
		if err := rows.Scan(&key, &ref, &updatedAt); err != nil {
			return nil, err
		}
		rest, ok := strings.CutPrefix(key, pluginProfileLinkPrefix)
		if !ok {
			continue
		}
		pluginID, ok := strings.CutSuffix(rest, "."+string(profileID))
		if !ok || pluginID == "" {
			continue
		}
		connections = append(connections, MyScrobbleConnection{
			Service:      pluginID,
			AccountLabel: ref,
			LinkedAt:     millisToTime(updatedAt),
		})
	}
	return connections, rows.Err()
}

// mySavedFilters returns the caller's Saved Filters, reveal-filtered: a filter
// bound to a currently-hidden Library is dropped, like every read.
func (s *Store) mySavedFilters(ctx context.Context, profileID ProfileID, filter LibraryFilter) ([]MySavedFilter, error) {
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT name, library_id, filter_json, created_at_ms
		FROM saved_filter
		WHERE profile_id = ?
		ORDER BY created_at_ms DESC, id
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MySavedFilter{}
	for rows.Next() {
		var f MySavedFilter
		var createdMS int64
		if err := rows.Scan(&f.Name, &f.LibraryID, &f.FilterJSON, &createdMS); err != nil {
			return nil, err
		}
		f.CreatedAt = millisToTime(createdMS)
		if err := s.libraryVisibleSQL(ctx, f.LibraryID, filter); err != nil {
			continue
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// mySavedStations returns the caller's saved Stations, reveal-filtered: a
// Station bound to a currently-hidden Library is dropped, like every read. Only
// the generation inputs are exported (seed/rules/tuning) — never a generated
// track order, which a Station never persists (ADR-0098 area 5).
func (s *Store) mySavedStations(ctx context.Context, profileID ProfileID, filter LibraryFilter) ([]MySavedStation, error) {
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT name, library_id, seed_json, rules_json, tuning_json, created_at_ms
		FROM saved_station
		WHERE profile_id = ?
		ORDER BY created_at_ms DESC, id
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MySavedStation{}
	for rows.Next() {
		var st MySavedStation
		var createdMS int64
		if err := rows.Scan(&st.Name, &st.LibraryID, &st.SeedJSON, &st.RulesJSON, &st.TuningJSON, &createdMS); err != nil {
			return nil, err
		}
		st.CreatedAt = millisToTime(createdMS)
		if err := s.libraryVisibleSQL(ctx, st.LibraryID, filter); err != nil {
			continue
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// myCollections returns the Collections the caller authored, reveal-filtered by
// the owning Library. A profile-authored SmartCollection is Beamfall's
// per-Profile Collection; operator-created collections have a
// NULL author and are not the caller's to export.
func (s *Store) myCollections(ctx context.Context, profileID ProfileID, filter LibraryFilter) ([]MyCollection, error) {
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT name, library_id, source_expr, created_at_ms
		FROM smart_collection
		WHERE created_by_profile_id = ?
		ORDER BY created_at_ms DESC, id
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MyCollection{}
	for rows.Next() {
		var c MyCollection
		var createdMS int64
		if err := rows.Scan(&c.Name, &c.LibraryID, &c.SourceExpr, &createdMS); err != nil {
			return nil, err
		}
		c.CreatedAt = millisToTime(createdMS)
		if err := s.libraryVisibleSQL(ctx, c.LibraryID, filter); err != nil {
			continue
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// myMarkers returns the Markers the caller authored, reveal-filtered by the
// Marker's Media. Tags are loaded per marker.
func (s *Store) myMarkers(ctx context.Context, profileID ProfileID, filter LibraryFilter) ([]MyMarker, error) {
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT id, media_id, title, start_seconds, end_seconds, segment_kind
		FROM marker
		WHERE created_by_profile_id = ?
		ORDER BY created_at_ms DESC, id
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type pending struct {
		id MarkerID
		m  MyMarker
	}
	var collected []pending
	for rows.Next() {
		var id MarkerID
		var m MyMarker
		var end sql.NullFloat64
		var segmentKind sql.NullString
		if err := rows.Scan(&id, &m.MediaID, &m.Title, &m.StartSeconds, &end, &segmentKind); err != nil {
			return nil, err
		}
		if end.Valid {
			value := end.Float64
			m.EndSeconds = &value
		}
		m.SegmentKind = segmentKind.String
		collected = append(collected, pending{id: id, m: m})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []MyMarker{}
	for _, p := range collected {
		if _, err := s.visibleMediaSQL(ctx, p.m.MediaID, filter); err != nil {
			continue
		}
		tags, err := s.markerTagsSQL(ctx, p.id)
		if err != nil {
			return nil, err
		}
		p.m.Tags = tags
		out = append(out, p.m)
	}
	return out, nil
}

func (s *Store) myPlaybackProgress(ctx context.Context, profileID ProfileID) ([]MyPlaybackProgress, error) {
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT media_id, position_seconds, duration_seconds, watched, play_count, updated_at_ms
		FROM playback_progress
		WHERE profile_id = ?
		ORDER BY updated_at_ms DESC, media_id
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MyPlaybackProgress
	for rows.Next() {
		var row MyPlaybackProgress
		var watched int
		var updated int64
		if err := rows.Scan(&row.MediaID, &row.PositionSeconds, &row.DurationSeconds, &watched, &row.PlayCount, &updated); err != nil {
			return nil, err
		}
		row.Watched = watched != 0
		row.UpdatedAt = millisToTime(updated)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) myFavorites(ctx context.Context, profileID ProfileID) ([]MyFavorite, error) {
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT media_id, favorited_at_ms
		FROM media_favorite
		WHERE profile_id = ?
		ORDER BY favorited_at_ms DESC, media_id
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MyFavorite
	for rows.Next() {
		var row MyFavorite
		var at int64
		if err := rows.Scan(&row.MediaID, &at); err != nil {
			return nil, err
		}
		row.FavoritedAt = millisToTime(at)
		out = append(out, row)
	}
	return out, rows.Err()
}

// myEngagementEventsLocked reads the caller's raw engagement event log rows for
// the export, newest first across every kind and target. Profile-scoped — the
// read filters on profile_id like every other engagement read. Reveal filtering
// of Media-targeted rows happens in the export assembly (Entity-only rows carry
// no Media reference and export whole). The caller holds s.mu (RLock) and has
// run guardLocked.
func (s *Store) myEngagementEventsLocked(ctx context.Context, profileID ProfileID) ([]MyEngagementEventEntry, error) {
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT media_id, entity_id, kind, value, payload, occurred_at_ms
		FROM engagement_event
		WHERE profile_id = ?
		ORDER BY occurred_at_ms DESC, id DESC
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MyEngagementEventEntry
	for rows.Next() {
		var (
			entry    MyEngagementEventEntry
			mediaID  sql.NullString
			entityID sql.NullString
			payload  sql.NullString
			occurred int64
		)
		if err := rows.Scan(&mediaID, &entityID, &entry.Kind, &entry.Value, &payload, &occurred); err != nil {
			return nil, err
		}
		entry.MediaID = MediaID(mediaID.String)
		entry.EntityID = EntityID(entityID.String)
		entry.Payload = payload.String
		entry.OccurredAt = millisToTime(occurred)
		out = append(out, entry)
	}
	return out, rows.Err()
}

// DeleteMyEngagement hard-DELETEs the caller's own engagement rows (ADR-0036
// explicitly sanctions hard delete here: this is the user's own behavioral
// data, not library content — hard invariant 6's recoverable-by-default trash
// does not apply; a privacy panel that soft-deletes the user's data is a lie).
//
// scope MyDataDeleteAll removes every engagement row for the Profile across all
// three projection tables, INCLUDING rows that reference currently-hidden
// Media: deleting your own data must not require revealing first (deletion is
// not disclosure). scope MyDataDeleteMedia removes one Media item's rows,
// resolved through the caller's visibility filter FIRST — a hidden mediaID
// fails identically to an unknown one, so delete-by-id cannot be used as an
// existence oracle for hidden Media.
//
// A one-Media delete is one events transaction. A full-Profile delete uses a
// durable, replayable cross-file state machine because catalog.db and events.db
// cannot share a transaction. Play-queue positions simply disappear (the queue
// is read position-ordered, so contiguity is a read-time concern).
func (s *Store) DeleteMyEngagement(ctx context.Context, profileID ProfileID, scope MyDataDeleteScope, mediaID MediaID, filter LibraryFilter) error {
	activity := ActivityEntry{
		OccurredAt: s.serverNow().UTC(),
		Action:     ActivityMyDataDelete,
		ProfileID:  profileID,
		TargetType: string(scope),
	}
	return s.DeleteMyEngagementAudited(ctx, profileID, scope, mediaID, filter, activity)
}

// DeleteProfileFootprint hard-DELETEs a TARGET Profile's entire engagement
// footprint on an operator's (or guardian's) behalf (ADR-0036 §2.5/§2.6 — the
// Console operator-delete and the managed-Profile guardian path). It is the
// MyDataDeleteAll self-delete with the target parameterized: the self-delete is
// this method with target == the caller's own Profile, so the recoverability
// posture and the disown-shared-contributions semantics are byte-identical —
// no new policy. Engagement projections are hard-deleted (ADR-0036 sanctions
// hard delete for the user's own behavioral data; hard invariant 6's recoverable
// trash does not apply), Saved Filters are deleted, and server-global Markers /
// SmartCollections are DISOWNED (created_by_profile_id → NULL) so removing one
// Profile's data never degrades the library for everyone else.
//
// There is NO LibraryFilter parameter: like MyDataDeleteAll, this removes every
// row for the Profile across all Libraries, including rows that reference
// restricted Media — an operator offboarding a household (GDPR) or a guardian
// clearing a managed Profile must not be required to reveal first, and deletion
// is not disclosure. The graph layer gates WHO may call this (Admin Session +
// managed-target validation, ADR-0036 §2.5/§2.6); the store performs the delete.
//
// The target Profile must exist; an unknown id is a ValidationError so the
// operator-delete cannot silently no-op against a typo.
func (s *Store) DeleteProfileFootprint(ctx context.Context, profileID ProfileID) error {
	activity := ActivityEntry{
		OccurredAt: s.serverNow().UTC(),
		Action:     ActivityOperatorDataDelete,
		TargetType: "profile",
		TargetID:   string(profileID),
	}
	return s.DeleteProfileFootprintAudited(ctx, profileID, activity)
}

// My Data Summary + Event log (ADR-0036 §2.2 layers 1 & 2). Both are COMPUTED
// ON READ from the calling Profile's OWN engagement projections, Reveal-filtered
// at the store boundary — never a stored analytics table (invariant: no
// server-side analytics). Summary uses the projection helpers; Event log
// keyset-pages bounded Media-ID chunks and retains only offset+limit global
// candidates. Both resolve each candidate through the caller's LibraryFilter
// before counting or returning it, so a relocked session's Summary counts and
// Event log never disclose restricted activity.

// MyEngagementEventKind is the closed, shape-named vocabulary of My-Data event
// log rows. Each engagement projection contributes exactly one kind; the names
// are generic (invariant #1: no domain terms).
type MyEngagementEventKind string

const (
	// MyEventPlay is one playback_progress row (a play / resume of one Media).
	MyEventPlay MyEngagementEventKind = "play"
	// MyEventFavorite is one media_favorite row.
	MyEventFavorite MyEngagementEventKind = "favorite"
	// MyEventQueue is one queue entry (a queue add).
	MyEventQueue MyEngagementEventKind = "queue"
)

// MyLibraryBreakdown is one per-Library play tally in the Summary: the Library
// the plays landed in and how many distinct Media plays it holds.
type MyLibraryBreakdown struct {
	LibraryID LibraryID `json:"libraryID"`
	Plays     int       `json:"plays"`
}

// MyDataSummary is the ADR-0036 §2.2 layer-1 aggregate over the caller's own
// engagement, computed on read and Reveal-filtered. Counts cover only Media the
// caller can currently see. FirstActivity/LastActivity bound the activity date
// range across every counted event (zero when there is no engagement).
type MyDataSummary struct {
	Plays            int                  `json:"plays"`
	Watched          int                  `json:"watched"`
	Favorites        int                  `json:"favorites"`
	QueueEntries     int                  `json:"queueEntries"`
	LibraryCount     int                  `json:"libraryCount"`
	LibraryBreakdown []MyLibraryBreakdown `json:"libraryBreakdown"`
	FirstActivity    time.Time            `json:"firstActivity"`
	LastActivity     time.Time            `json:"lastActivity"`
}

// MyEngagementEvent is one row of the ADR-0036 §2.2 layer-2 event log: a single
// engagement act on one Media, with the kind, the owning Library (for the
// client's filter chips), and the true event time. Media is identified by id
// only — the log carries no title, mirroring the rest of My Data.
type MyEngagementEvent struct {
	Kind       MyEngagementEventKind `json:"kind"`
	MediaID    MediaID               `json:"mediaID"`
	LibraryID  LibraryID             `json:"libraryID"`
	OccurredAt time.Time             `json:"occurredAt"`
}

// MyEngagementLogQuery is the layer-2 event-log filter/page request. LibraryID
// (optional) restricts to one Library; From/To (optional, inclusive) bound the
// event time. Limit caps the page (clamped to a sane ceiling); Offset is the
// page cursor. All filtering is applied AFTER Reveal filtering, so a hidden
// Library can never be named to widen visibility.
type MyEngagementLogQuery struct {
	LibraryID LibraryID
	From      time.Time
	To        time.Time
	Limit     int
	Offset    int
}

// MyEngagementLogPage is one page of the event log plus the total count of
// events matching the filter (across all pages), so the client can render
// pagination controls without fetching every page.
type MyEngagementLogPage struct {
	Events []MyEngagementEvent `json:"events"`
	Total  int                 `json:"total"`
}

// myEngagementLogDefaultLimit / myEngagementLogMaxLimit bound a single event-log
// page. An absent or non-positive Limit falls to the default; an oversized one
// is clamped so a client cannot ask the server to materialize an unbounded page.
const (
	myEngagementLogDefaultLimit = 50
	myEngagementLogMaxLimit     = 500
)

// gatherMyEvents reads the caller's engagement rows from the three events
// projections and resolves every referenced Media once through the caller's
// LibraryFilter, returning the Reveal-filtered events in descending event-time
// order (newest first). This is the shared primitive behind both the Summary
// and the Event log: both operate ONLY over the rows this returns, so neither
// can ever count or list activity on Media the caller cannot currently see.
func (s *Store) gatherMyEvents(ctx context.Context, profileID ProfileID, filter LibraryFilter) ([]MyEngagementEvent, error) {
	progress, err := s.myPlaybackProgress(ctx, profileID)
	if err != nil {
		return nil, err
	}
	favorites, err := s.myFavorites(ctx, profileID)
	if err != nil {
		return nil, err
	}
	queue, err := s.myQueueRows(ctx, profileID)
	if err != nil {
		return nil, err
	}
	// The true event time for a play is COALESCE(occurred_at_ms, updated_at_ms)
	// (0013_engagement_provenance): myPlaybackProgress reads updated_at_ms into
	// UpdatedAt, which equals the event time for native rows. Imported rows that
	// backdate occurred_at_ms are read through the same column projection here;
	// the My-Data log reports the recency time the panel sorts on.
	ids := make([]MediaID, 0, len(progress)+len(favorites)+len(queue))
	for _, row := range progress {
		ids = append(ids, row.MediaID)
	}
	for _, row := range favorites {
		ids = append(ids, row.MediaID)
	}
	for _, row := range queue {
		ids = append(ids, row.MediaID)
	}
	visible, err := s.visibleMediaBatchSQL(ctx, ids, filter)
	if err != nil {
		return nil, err
	}
	events := make([]MyEngagementEvent, 0, len(ids))
	for _, row := range progress {
		if m, ok := visible[row.MediaID]; ok {
			events = append(events, MyEngagementEvent{Kind: MyEventPlay, MediaID: row.MediaID, LibraryID: m.LibraryID, OccurredAt: row.UpdatedAt})
		}
	}
	for _, row := range favorites {
		if m, ok := visible[row.MediaID]; ok {
			events = append(events, MyEngagementEvent{Kind: MyEventFavorite, MediaID: row.MediaID, LibraryID: m.LibraryID, OccurredAt: row.FavoritedAt})
		}
	}
	for _, row := range queue {
		if m, ok := visible[row.MediaID]; ok {
			events = append(events, MyEngagementEvent{Kind: MyEventQueue, MediaID: row.MediaID, LibraryID: m.LibraryID, OccurredAt: row.AddedAt})
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].OccurredAt.After(events[j].OccurredAt)
	})
	return events, nil
}

// MyDataSummary computes the ADR-0036 §2.2 layer-1 aggregate over the caller's
// own engagement, Reveal-filtered. Computed on read — there is no stored
// analytics table. Watched counts the play events whose row is marked watched.
func (s *Store) MyDataSummary(ctx context.Context, profileID ProfileID, filter LibraryFilter) (MyDataSummary, error) {
	if profileID == "" {
		return MyDataSummary{}, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return MyDataSummary{}, err
	}

	// Watched needs the watched flag, which gatherMyEvents drops; read the
	// reveal-filtered play projection separately for the watched tally so the
	// aggregate still counts only visible rows.
	progress, err := s.myPlaybackProgress(ctx, profileID)
	if err != nil {
		return MyDataSummary{}, err
	}
	playIDs := make([]MediaID, 0, len(progress))
	for _, row := range progress {
		playIDs = append(playIDs, row.MediaID)
	}
	visiblePlays, err := s.visibleMediaBatchSQL(ctx, playIDs, filter)
	if err != nil {
		return MyDataSummary{}, err
	}

	events, err := s.gatherMyEvents(ctx, profileID, filter)
	if err != nil {
		return MyDataSummary{}, err
	}

	summary := MyDataSummary{}
	playsByLibrary := map[LibraryID]int{}
	var libraryOrder []LibraryID
	for _, ev := range events {
		switch ev.Kind {
		case MyEventPlay:
			summary.Plays++
			if _, seen := playsByLibrary[ev.LibraryID]; !seen {
				libraryOrder = append(libraryOrder, ev.LibraryID)
			}
			playsByLibrary[ev.LibraryID]++
		case MyEventFavorite:
			summary.Favorites++
		case MyEventQueue:
			summary.QueueEntries++
		}
		if summary.FirstActivity.IsZero() || ev.OccurredAt.Before(summary.FirstActivity) {
			summary.FirstActivity = ev.OccurredAt
		}
		if ev.OccurredAt.After(summary.LastActivity) {
			summary.LastActivity = ev.OccurredAt
		}
	}
	for _, row := range progress {
		if _, ok := visiblePlays[row.MediaID]; ok && row.Watched {
			summary.Watched++
		}
	}
	summary.LibraryCount = len(libraryOrder)
	summary.LibraryBreakdown = make([]MyLibraryBreakdown, 0, len(libraryOrder))
	for _, id := range libraryOrder {
		summary.LibraryBreakdown = append(summary.LibraryBreakdown, MyLibraryBreakdown{LibraryID: id, Plays: playsByLibrary[id]})
	}
	return summary, nil
}

// MyEngagementLog returns one Reveal-filtered, Library/date-filtered, paginated
// page of the caller's engagement event log (ADR-0036 §2.2 layer 2). Reveal
// filtering happens before an event contributes to Total or the bounded page;
// the Library and date-range filters only narrow that visible set, so naming a
// hidden Library can never widen what the page shows. Total is exact across all
// pages without materializing the complete event log.
func (s *Store) MyEngagementLog(ctx context.Context, profileID ProfileID, filter LibraryFilter, query MyEngagementLogQuery) (MyEngagementLogPage, error) {
	if profileID == "" {
		return MyEngagementLogPage{}, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return MyEngagementLogPage{}, err
	}

	return s.myEngagementLogBoundedLocked(ctx, profileID, filter, query)
}

// MyProfileSession is one of a Profile's active Sessions for the My Data view.
// Read-only: revocation stays on the existing session surfaces.
// The device name and role come from the joined Device row.
type MyProfileSession struct {
	ID         SessionID
	DeviceID   DeviceID
	DeviceName string
	DeviceRole DeviceRole
	ExpiresAt  time.Time
}

// MyProfileSessions returns every active Session bound to the Profile, joined
// to its Device for name and role. Always the caller's own Profile — there is
// no other-profile parameter. Ordered by id for a stable read.
//
// Note (ADR-0036 deviation): the auth_session schema records no created-at or
// last-seen timestamp, so those fields the panel might display are not available
// from the store today; only id, device name/role, and expiry are.
func (s *Store) MyProfileSessions(ctx context.Context, profileID ProfileID) ([]MyProfileSession, error) {
	if profileID == "" {
		return nil, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT a.id, a.device_id, d.name, d.role, a.expires_at_ms
		FROM auth_session a
		JOIN device d ON d.id = a.device_id
		WHERE a.profile_id = ?
		ORDER BY a.id
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []MyProfileSession
	for rows.Next() {
		var session MyProfileSession
		var role string
		var expires sql.NullInt64
		if err := rows.Scan(&session.ID, &session.DeviceID, &session.DeviceName, &role, &expires); err != nil {
			return nil, err
		}
		session.DeviceRole = DeviceRole(role)
		if expires.Valid {
			session.ExpiresAt = millisToTime(expires.Int64)
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

// MyStreamToken is one outstanding Stream Token in the My Data view:
// the target KIND and expiry ONLY. No token value, no media id, no title — a
// token for hidden Media is reported with the same shape as one for visible
// Media (the caller cannot tell what a hidden token points at).
type MyStreamToken struct {
	TargetKind string
	ExpiresAt  time.Time
}

// RecordStreamToken records (or refreshes) one issued Stream Token in the
// transparency ledger. It is called centrally at token issue time;
// it never participates in authorizing bytes (that is the signed grant's job).
// Re-issuing the same (session, target, media) grant upserts the expiry rather
// than accumulating duplicate rows, keeping the ledger bounded.
func (s *Store) RecordStreamToken(ctx context.Context, sessionID SessionID, profileID ProfileID, deviceID DeviceID, target string, mediaID MediaID, restricted bool, expiresAt time.Time) error {
	if sessionID == "" || profileID == "" || target == "" || mediaID == "" {
		return ValidationError("stream token ledger row is incomplete")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.eventsW.ExecContext(ctx, `
		INSERT INTO stream_token_ledger(session_id, profile_id, device_id, target, media_id, restricted, expires_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, target, media_id) DO UPDATE SET
		  profile_id = excluded.profile_id,
		  device_id = excluded.device_id,
		  restricted = excluded.restricted,
		  expires_at_ms = excluded.expires_at_ms
	`, sessionID, profileID, deviceID, target, mediaID, boolToInt(restricted), timeToMillis(expiresAt))
	return err
}

// MyStreamTokens returns the Profile's outstanding (unexpired) Stream Tokens,
// reveal-blind by construction: the ledger read returns target kind and expiry
// for every non-expired row, with NO media-identifying field. A row's media id
// stays in the ledger only so this read could reveal-filter, but the My Data
// contract reports hidden and visible tokens identically (target kind + expiry
// only), so no media lookup is needed and a relocked session cannot learn
// which hidden Media its live tokens reference. Expired rows are filtered out
// (and lazily swept) so the view shows only what is actually live.
func (s *Store) MyStreamTokens(ctx context.Context, profileID ProfileID, now time.Time) ([]MyStreamToken, error) {
	return s.myStreamTokens(ctx, profileID, true, now)
}

// MyStreamTokensVisible applies the caller's current Reveal state before row
// materialization. Hidden restricted grants contribute neither rows nor timing.
func (s *Store) MyStreamTokensVisible(ctx context.Context, profileID ProfileID, revealed bool, now time.Time) ([]MyStreamToken, error) {
	return s.myStreamTokens(ctx, profileID, revealed, now)
}

func (s *Store) myStreamTokens(ctx context.Context, profileID ProfileID, revealed bool, now time.Time) ([]MyStreamToken, error) {
	if profileID == "" {
		return nil, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	nowMS := timeToMillis(now)
	// Lazy sweep of expired rows keeps the ledger bounded without a scheduled
	// job; failure to sweep is non-fatal (the read still filters by expiry).
	_, _ = s.eventsW.ExecContext(ctx, `DELETE FROM stream_token_ledger WHERE expires_at_ms <= ?`, nowMS)
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT target, expires_at_ms
		FROM stream_token_ledger
		WHERE profile_id = ? AND expires_at_ms > ? AND (? OR restricted = 0)
		ORDER BY expires_at_ms, target, media_id
	`, profileID, nowMS, boolToInt(revealed))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tokens []MyStreamToken
	for rows.Next() {
		var token MyStreamToken
		var expires int64
		if err := rows.Scan(&token.TargetKind, &expires); err != nil {
			return nil, err
		}
		token.ExpiresAt = millisToTime(expires)
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

// MyPackToken is one outstanding Pack-subject Stream Token in the My Data view
// (ADR-0098 area 6 + ADR-0036): the target KIND and expiry ONLY. Like
// MyStreamToken it carries no token value and no pack-identifying field — a
// token for a restricted pack is shape-identical to one for a visible pack.
type MyPackToken struct {
	TargetKind string
	ExpiresAt  time.Time
}

// RecordPackToken records (or refreshes) one issued Pack-subject Stream Token in
// the transparency ledger. It is the pack-grant analogue of
// RecordStreamToken: the generic stream_token_ledger keys on media_id NOT NULL,
// which a Pack-subject grant lacks, so pack grants ride this parallel table. It
// never participates in authorizing bytes (the serving layer re-checks the
// signed grant + live pack state + Reveal/seal). Re-issuing the same
// (session, target, pack) grant upserts the expiry rather than accumulating
// duplicate rows.
func (s *Store) RecordPackToken(ctx context.Context, sessionID SessionID, profileID ProfileID, deviceID DeviceID, target string, packID OfflinePackID, restricted bool, expiresAt time.Time) error {
	if sessionID == "" || profileID == "" || target == "" || packID == "" {
		return ValidationError("pack token ledger row is incomplete")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.eventsW.ExecContext(ctx, `
		INSERT INTO pack_token_ledger(session_id, profile_id, device_id, target, pack_id, restricted, expires_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, target, pack_id) DO UPDATE SET
		  profile_id = excluded.profile_id,
		  device_id = excluded.device_id,
		  restricted = excluded.restricted,
		  expires_at_ms = excluded.expires_at_ms
	`, sessionID, profileID, deviceID, target, packID, boolToInt(restricted), timeToMillis(expiresAt))
	return err
}

// MyPackTokens returns the Profile's outstanding (unexpired) Pack-subject Stream
// Tokens, reveal-blind by construction — the read returns target kind and expiry
// for every non-expired row with NO pack-identifying field, so a relocked
// session cannot learn which restricted pack its live tokens reference. Expired
// rows are filtered out (and lazily swept).
func (s *Store) MyPackTokens(ctx context.Context, profileID ProfileID, now time.Time) ([]MyPackToken, error) {
	return s.myPackTokens(ctx, profileID, true, now)
}

// MyPackTokensVisible is the Reveal-filtered My Data ledger read.
func (s *Store) MyPackTokensVisible(ctx context.Context, profileID ProfileID, revealed bool, now time.Time) ([]MyPackToken, error) {
	return s.myPackTokens(ctx, profileID, revealed, now)
}

func (s *Store) myPackTokens(ctx context.Context, profileID ProfileID, revealed bool, now time.Time) ([]MyPackToken, error) {
	if profileID == "" {
		return nil, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	nowMS := timeToMillis(now)
	_, _ = s.eventsW.ExecContext(ctx, `DELETE FROM pack_token_ledger WHERE expires_at_ms <= ?`, nowMS)
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT target, expires_at_ms
		FROM pack_token_ledger
		WHERE profile_id = ? AND expires_at_ms > ? AND (? OR restricted = 0)
		ORDER BY expires_at_ms, target, pack_id
	`, profileID, nowMS, boolToInt(revealed))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tokens []MyPackToken
	for rows.Next() {
		var token MyPackToken
		var expires int64
		if err := rows.Scan(&token.TargetKind, &expires); err != nil {
			return nil, err
		}
		token.ExpiresAt = millisToTime(expires)
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

// DeleteMyPackTokens hard-DELETEs the caller's outstanding Pack-subject Stream
// Token ledger rows (ADR-0036 My Data delete). The signed grants themselves are
// stateless and stay valid until expiry, but the serving layer denies bytes for
// a revoked/expired pack; clearing the transparency rows removes them from the
// caller's My Data view.
func (s *Store) DeleteMyPackTokens(ctx context.Context, profileID ProfileID) error {
	if profileID == "" {
		return ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.eventsW.ExecContext(ctx, `DELETE FROM pack_token_ledger WHERE profile_id = ?`, profileID)
	return err
}
