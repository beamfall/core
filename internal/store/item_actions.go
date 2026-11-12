package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

// Item target kinds, as stored in watchlist_item.target_kind and
// item_rating.target_kind and returned in ItemState.Kind.
const (
	ItemTargetMedia  = "media"
	ItemTargetEntity = "entity"

	// ItemRatingMax is the highest personal rating; 0 clears a rating.
	ItemRatingMax = 10
)

// ItemTarget names the one Media or the one Entity an item action applies to.
// Exactly one of the two IDs is set.
type ItemTarget struct {
	MediaID  MediaID
	EntityID EntityID
}

func (t ItemTarget) kindAndID() (string, string, error) {
	switch {
	case t.MediaID != "" && t.EntityID == "":
		return ItemTargetMedia, string(t.MediaID), nil
	case t.EntityID != "" && t.MediaID == "":
		return ItemTargetEntity, string(t.EntityID), nil
	default:
		return "", "", ValidationError("exactly one of media ID and entity ID is required")
	}
}

// ItemState is one Profile's state for one Media or Entity. Rating is 0 when
// the Profile has not rated the item.
type ItemState struct {
	ID          string
	Kind        string
	Watched     bool
	Watchlisted bool
	Rating      int
}

type itemMedia struct {
	id              MediaID
	durationSeconds int
}

// itemTargetMediaLocked checks that the target is visible under filter and
// returns the Media a watched mark applies to: the Media itself, or every
// visible non-Extra Media in the Entity's tree. A hidden target errors the
// same way a missing one does.
func (s *Store) itemTargetMediaLocked(ctx context.Context, target ItemTarget, filter LibraryFilter) ([]itemMedia, error) {
	if target.MediaID != "" {
		media, err := s.visibleMediaSQL(ctx, target.MediaID, filter)
		if err != nil {
			return nil, err
		}
		return []itemMedia{{id: media.ID, durationSeconds: media.DurationSeconds}}, nil
	}
	return s.entityTreeMediaLocked(ctx, target.EntityID, filter)
}

// itemTargetVisibleLocked is the visibility check for actions that store the
// target itself (watchlist, rating) rather than its Media.
func (s *Store) itemTargetVisibleLocked(ctx context.Context, target ItemTarget, filter LibraryFilter) error {
	if target.MediaID != "" {
		_, err := s.visibleMediaSQL(ctx, target.MediaID, filter)
		return err
	}
	_, err := s.visibleEntitySQL(ctx, target.EntityID, filter)
	return err
}

// entityTreeMediaLocked returns the visible non-Extra Media an Entity relates
// to, directly or through its same-Kind child Entities at any depth. A child
// links to its parent (identify.applyEntityParent), so descendants are the
// from_entity_id of rows whose target is already in the tree. Only same-Kind
// edges are followed, so a curated relation to an unrelated Kind never widens
// the mark.
func (s *Store) entityTreeMediaLocked(ctx context.Context, entityID EntityID, filter LibraryFilter) ([]itemMedia, error) {
	// An Encrypted Library keeps its Entities, relations and Media in its own
	// catalog plane, so the tree is read from the plane that holds the root.
	plane, err := s.catalogPlaneForEntityLocked(ctx, entityID)
	if err != nil {
		return nil, err
	}
	if _, err := s.visibleEntitySQLFrom(ctx, plane.reader, entityID, filter); err != nil {
		return nil, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	var libArgs []any
	planePluginID := ""
	for _, library := range libraries {
		if libraryVisible(library, filter) {
			libArgs = append(libArgs, library.ID)
		}
		if library.ID == plane.libraryID && library.PluginID != nil {
			planePluginID = *library.PluginID
		}
	}
	if len(libArgs) == 0 {
		return nil, nil
	}
	libIn := strings.TrimSuffix(strings.Repeat("?, ", len(libArgs)), ", ")
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	if plane.encrypted {
		// The plane has no library table for the shared clause to join.
		certClause, certArgs = planeCertificationFilterSQL(planePluginID, filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	}
	args := make([]any, 0, 1+len(libArgs)*2+len(certArgs))
	args = append(args, entityID)
	args = append(args, libArgs...)
	args = append(args, libArgs...)
	args = append(args, certArgs...)
	rows, err := plane.reader.QueryContext(ctx, `
		WITH RECURSIVE subtree(id, kind) AS (
			SELECT id, kind FROM entity WHERE id = ? AND trashed_at_ms IS NULL
			UNION
			SELECT child.id, child.kind
			FROM entity_relation r
			JOIN subtree ON subtree.id = r.to_entity_id
			JOIN entity child ON child.id = r.from_entity_id
			WHERE child.kind = subtree.kind
				AND child.trashed_at_ms IS NULL
				AND child.library_id IN (`+libIn+`)
		)
		SELECT m.id, MAX(m.duration_seconds)
		FROM subtree
		JOIN entity_relation r ON r.from_entity_id = subtree.id
		JOIN media m ON m.id = r.to_media_id
		WHERE m.trashed_at_ms IS NULL AND m.extra = 0
			AND m.library_id IN (`+libIn+`)`+certClause+`
		GROUP BY m.id
		ORDER BY m.id
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var media []itemMedia
	for rows.Next() {
		var item itemMedia
		if err := rows.Scan(&item.id, &item.durationSeconds); err != nil {
			return nil, err
		}
		media = append(media, item)
	}
	return media, rows.Err()
}

type itemProgress struct {
	watched  bool
	hasState bool // watched, a resume position or a counted play
}

// itemProgressLocked reads the Profile's playback state for ids from q, which
// is the events reader or an events write transaction.
func itemProgressLocked(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, profileID ProfileID, ids []MediaID) (map[MediaID]itemProgress, error) {
	state := make(map[MediaID]itemProgress, len(ids))
	const chunkSize = 500
	for start := 0; start < len(ids); start += chunkSize {
		chunk := ids[start:min(start+chunkSize, len(ids))]
		args := make([]any, 0, len(chunk)+1)
		args = append(args, profileID)
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := q.QueryContext(ctx, `
			SELECT media_id, watched, position_seconds, play_count
			FROM playback_progress
			WHERE profile_id = ? AND media_id IN (`+strings.TrimSuffix(strings.Repeat("?, ", len(chunk)), ", ")+`)
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id MediaID
			var watched, position, plays int
			if err := rows.Scan(&id, &watched, &position, &plays); err != nil {
				rows.Close()
				return nil, err
			}
			state[id] = itemProgress{watched: watched != 0, hasState: watched != 0 || position > 0 || plays > 0}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return state, nil
}

// SetItemWatched marks one visible Media, or every visible Media in an
// Entity's tree, watched or unwatched for profileID. Each Media whose state
// changes gets one manual-mark event in the engagement log, applied to the
// playback projection in the same transaction, so a projection rebuild
// reproduces the mark. It returns the Media that went from unwatched to
// watched.
func (s *Store) SetItemWatched(ctx context.Context, profileID ProfileID, target ItemTarget, watched bool, filter LibraryFilter) ([]MediaID, error) {
	// Authority face: watched marks, the watchlist and ratings are engagement,
	// which is main-authored. An Attached node refuses.
	if err := s.guardAuthority(serverrole.FaceEngagement); err != nil {
		return nil, err
	}
	if profileID == "" {
		return nil, ValidationError("profile ID is required")
	}
	if _, _, err := target.kindAndID(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	media, err := s.itemTargetMediaLocked(ctx, target, filter)
	if err != nil {
		return nil, err
	}
	if len(media) == 0 {
		return nil, nil
	}
	ids := make([]MediaID, len(media))
	for i, item := range media {
		ids[i] = item.id
	}
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	prior, err := itemProgressLocked(ctx, tx, profileID, ids)
	if err != nil {
		return nil, err
	}
	now := timeToMillis(time.Now().UTC())
	var changed []MediaID
	for _, item := range media {
		if watched == prior[item.id].watched && (watched || !prior[item.id].hasState) {
			continue
		}
		event := playbackProjectionEvent{
			Operation:       playbackProjectionOperationMark,
			ProfileID:       string(profileID),
			MediaID:         string(item.id),
			DurationSeconds: item.durationSeconds,
			Watched:         watched,
			UpdatedAtMS:     now,
			LastPlayedAtMS:  optionalMillis(watched, now),
		}
		if err := appendPlaybackProjectionEvent(ctx, tx, event); err != nil {
			return nil, err
		}
		if err := applyPlaybackProjectionEvent(ctx, tx, event); err != nil {
			return nil, err
		}
		if watched {
			changed = append(changed, item.id)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return changed, nil
}

// applyMarkProjectionEvent applies a manual watched mark. Watched clears the
// resume position and counts one play on the unwatched -> watched transition,
// the same rule a finished playback follows. Unwatched clears the watched
// bit, the resume position and the play count; it only ever updates an
// existing row, so it cannot create progress for a Media never touched.
//
// The mark's own time becomes the row's event time, so a playback report
// observed before the mark and delivered after it cannot undo the mark.
func applyMarkProjectionEvent(ctx context.Context, tx *sql.Tx, event playbackProjectionEvent) error {
	if !event.Watched {
		_, err := tx.ExecContext(ctx, `
			UPDATE playback_progress
			SET watched = 0, position_seconds = 0, play_count = 0,
			    last_played_at_ms = NULL, updated_at_ms = ?, event_time_ms = ?
			WHERE profile_id = ? AND media_id = ?
		`, event.UpdatedAtMS, event.UpdatedAtMS, event.ProfileID, event.MediaID)
		return err
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO playback_progress(profile_id, media_id, position_seconds, duration_seconds, watched, updated_at_ms, play_count, last_played_at_ms, event_time_ms)
		VALUES (?, ?, 0, ?, 1, ?, 1, ?, ?)
		ON CONFLICT(profile_id, media_id) DO UPDATE SET
		  position_seconds = 0,
		  play_count = play_count + (CASE WHEN playback_progress.watched = 0 THEN 1 ELSE 0 END),
		  last_played_at_ms = CASE WHEN playback_progress.watched = 0 THEN excluded.updated_at_ms ELSE last_played_at_ms END,
		  watched = 1,
		  updated_at_ms = excluded.updated_at_ms,
		  event_time_ms = excluded.event_time_ms
	`, event.ProfileID, event.MediaID, event.DurationSeconds, event.UpdatedAtMS, event.UpdatedAtMS, event.UpdatedAtMS)
	return err
}

// SetItemWatchlisted adds one visible Media or Entity to profileID's
// watchlist, or removes it. The visibility check runs before both paths, so a
// hidden target is indistinguishable from a missing one.
func (s *Store) SetItemWatchlisted(ctx context.Context, profileID ProfileID, target ItemTarget, listed bool, filter LibraryFilter) error {
	// Authority face: watched marks, the watchlist and ratings are engagement,
	// which is main-authored. An Attached node refuses.
	if err := s.guardAuthority(serverrole.FaceEngagement); err != nil {
		return err
	}
	if profileID == "" {
		return ValidationError("profile ID is required")
	}
	kind, id, err := target.kindAndID()
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if err := s.itemTargetVisibleLocked(ctx, target, filter); err != nil {
		return err
	}
	if listed {
		// An item already listed keeps its place: re-adding is not a bump.
		_, err = s.eventsW.ExecContext(ctx, `
			INSERT INTO watchlist_item(profile_id, target_kind, target_id, added_at_ms)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(profile_id, target_kind, target_id) DO NOTHING
		`, profileID, kind, id, timeToMillis(time.Now().UTC()))
		return err
	}
	_, err = s.eventsW.ExecContext(ctx, `
		DELETE FROM watchlist_item
		WHERE profile_id = ? AND target_kind = ? AND target_id = ?
	`, profileID, kind, id)
	return err
}

// SetItemRating records profileID's rating (1..ItemRatingMax) for one visible
// Media or Entity; 0 clears it.
func (s *Store) SetItemRating(ctx context.Context, profileID ProfileID, target ItemTarget, rating int, filter LibraryFilter) error {
	// Authority face: watched marks, the watchlist and ratings are engagement,
	// which is main-authored. An Attached node refuses.
	if err := s.guardAuthority(serverrole.FaceEngagement); err != nil {
		return err
	}
	if profileID == "" {
		return ValidationError("profile ID is required")
	}
	kind, id, err := target.kindAndID()
	if err != nil {
		return err
	}
	if rating < 0 || rating > ItemRatingMax {
		return ValidationError("rating must be between 0 and " + strconv.Itoa(ItemRatingMax))
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if err := s.itemTargetVisibleLocked(ctx, target, filter); err != nil {
		return err
	}
	if rating == 0 {
		_, err = s.eventsW.ExecContext(ctx, `
			DELETE FROM item_rating
			WHERE profile_id = ? AND target_kind = ? AND target_id = ?
		`, profileID, kind, id)
		return err
	}
	_, err = s.eventsW.ExecContext(ctx, `
		INSERT INTO item_rating(profile_id, target_kind, target_id, rating, rated_at_ms)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, target_kind, target_id) DO UPDATE SET
		  rating = excluded.rating,
		  rated_at_ms = excluded.rated_at_ms
	`, profileID, kind, id, rating, timeToMillis(time.Now().UTC()))
	return err
}

// ItemStateFor returns profileID's watched, watchlist and rating state for
// one visible Media or Entity. An Entity reads as watched only when its tree
// holds at least one visible Media and every one of them is watched.
func (s *Store) ItemStateFor(ctx context.Context, profileID ProfileID, target ItemTarget, filter LibraryFilter) (ItemState, error) {
	if profileID == "" {
		return ItemState{}, ValidationError("profile ID is required")
	}
	kind, id, err := target.kindAndID()
	if err != nil {
		return ItemState{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ItemState{}, err
	}
	media, err := s.itemTargetMediaLocked(ctx, target, filter)
	if err != nil {
		return ItemState{}, err
	}
	state := ItemState{ID: id, Kind: kind, Watched: len(media) > 0}
	ids := make([]MediaID, len(media))
	for i, item := range media {
		ids[i] = item.id
	}
	progress, err := itemProgressLocked(ctx, s.eventsR, profileID, ids)
	if err != nil {
		return ItemState{}, err
	}
	for _, mediaID := range ids {
		if !progress[mediaID].watched {
			state.Watched = false
			break
		}
	}
	var one int
	err = s.eventsR.QueryRowContext(ctx, `
		SELECT 1 FROM watchlist_item
		WHERE profile_id = ? AND target_kind = ? AND target_id = ?
	`, profileID, kind, id).Scan(&one)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ItemState{}, err
	}
	state.Watchlisted = err == nil
	err = s.eventsR.QueryRowContext(ctx, `
		SELECT rating FROM item_rating
		WHERE profile_id = ? AND target_kind = ? AND target_id = ?
	`, profileID, kind, id).Scan(&state.Rating)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ItemState{}, err
	}
	return state, nil
}

// watchlistItemsSQL backs the watchlist Source: the Profile's listed Media and
// Entities, most recently added first.
func (s *Store) watchlistItemsSQL(ctx context.Context, profileID ProfileID, filter LibraryFilter, limit, offset int) ([]SourceItem, error) {
	return s.profileItemsSQL(ctx, filter, `
		SELECT target_kind, target_id, 0
		FROM watchlist_item
		WHERE profile_id = ?
		ORDER BY added_at_ms DESC, target_kind, target_id
		LIMIT ? OFFSET ?
	`, profileID, limit, offset)
}

// ratedItemsSQL backs the rated Source: the Profile's rated Media and
// Entities, highest rating first.
func (s *Store) ratedItemsSQL(ctx context.Context, profileID ProfileID, filter LibraryFilter, limit, offset int) ([]SourceItem, error) {
	return s.profileItemsSQL(ctx, filter, `
		SELECT target_kind, target_id, rating
		FROM item_rating
		WHERE profile_id = ?
		ORDER BY rating DESC, rated_at_ms DESC, target_kind, target_id
		LIMIT ? OFFSET ?
	`, profileID, limit, offset)
}

// profileItemsPageSize is how many stored rows profileItemsSQL resolves per
// round while it looks for a page of visible items.
const profileItemsPageSize = 200

// profileItemsSQL walks (kind, id, rating) rows from the events DB in their
// stored order and resolves them against the catalog under filter. Targets the
// filter hides, and targets that left the catalog, are dropped before paging,
// so offset and limit count visible items and a page is short only at the end
// of the list. A negative limit returns every visible item past offset.
func (s *Store) profileItemsSQL(ctx context.Context, filter LibraryFilter, query string, profileID ProfileID, limit, offset int) ([]SourceItem, error) {
	items := []SourceItem{}
	if limit == 0 {
		return items, nil
	}
	skip := max(offset, 0)
	for stored := 0; ; stored += profileItemsPageSize {
		rows, more, err := s.profileItemRowsSQL(ctx, query, profileID, profileItemsPageSize, stored)
		if err != nil {
			return nil, err
		}
		visible, err := s.visibleProfileItemsSQL(ctx, rows, filter)
		if err != nil {
			return nil, err
		}
		for _, item := range visible {
			if skip > 0 {
				skip--
				continue
			}
			items = append(items, item)
			if limit > 0 && len(items) == limit {
				return items, nil
			}
		}
		if !more {
			return items, nil
		}
	}
}

type profileItemRow struct {
	kind, id string
	rating   int
}

// profileItemRowsSQL reads one stored-order window and reports whether more
// rows may follow it.
func (s *Store) profileItemRowsSQL(ctx context.Context, query string, profileID ProfileID, limit, offset int) ([]profileItemRow, bool, error) {
	rows, err := s.eventsR.QueryContext(ctx, query, profileID, limit, offset)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []profileItemRow
	for rows.Next() {
		var row profileItemRow
		if err := rows.Scan(&row.kind, &row.id, &row.rating); err != nil {
			return nil, false, err
		}
		out = append(out, row)
	}
	return out, len(out) == limit, rows.Err()
}

// visibleProfileItemsSQL keeps the rows whose target is visible under filter,
// in order, as Source items.
func (s *Store) visibleProfileItemsSQL(ctx context.Context, rows []profileItemRow, filter LibraryFilter) ([]SourceItem, error) {
	var mediaIDs []MediaID
	for _, row := range rows {
		if row.kind == ItemTargetMedia {
			mediaIDs = append(mediaIDs, MediaID(row.id))
		}
	}
	visible, err := s.visibleMediaBatchSQL(ctx, mediaIDs, filter)
	if err != nil {
		return nil, err
	}
	items := make([]SourceItem, 0, len(rows))
	for _, row := range rows {
		item := SourceItem{ID: MediaID(row.id), Kind: row.kind}
		if row.kind == ItemTargetMedia {
			media, ok := visible[MediaID(row.id)]
			if !ok {
				continue
			}
			item.Title = media.Title
			item.Summary = mediaDurationSummary(media.DurationSeconds)
		} else {
			entity, err := s.visibleEntitySQL(ctx, EntityID(row.id), filter)
			if errors.Is(err, errVisibleEntityNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			item.Title = entity.Title
		}
		if row.rating > 0 {
			item.Summary = strconv.Itoa(row.rating) + "/" + strconv.Itoa(ItemRatingMax)
		}
		items = append(items, item)
	}
	return items, nil
}
