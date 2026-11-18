package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

// opKindMergeVersion is the op_batch.kind of a version merge: a rescan folding
// a title's already-split unlabelled copy (its own Media) into the title's
// Media as a Version (CORE-2490).
const opKindMergeVersion = "merge-version"

// VersionMerge reports a version merge: the Media that kept the title and the
// Media folded into it, now trashed. BatchID is "" when nothing merged.
type VersionMerge struct {
	BatchID  string
	Survivor MediaID
	Merged   []MediaID
}

// versionMergeFacts are the Media row's probe facts, which describe its first
// Version (refreshFirstVersionFactsTx).
type versionMergeFacts struct {
	Container       string `json:"container"`
	Codec           string `json:"codec"`
	DurationSeconds int    `json:"durationSeconds"`
	VideoWidth      int    `json:"videoWidth"`
	VideoHeight     int    `json:"videoHeight"`
}

// versionMergeItemState is one merged Media's journal entry: enough to put the
// file back on its own Media and the survivor back as it was.
type versionMergeItemState struct {
	Survivor    MediaID `json:"survivor"`
	Fingerprint string  `json:"fingerprint"`
	Label       string  `json:"label"`
	// Relabels maps a survivor file's fingerprint to the label it held before
	// the merge, for each part-0 file the merge relabelled.
	Relabels      map[string]string `json:"relabels,omitempty"`
	SurvivorFacts versionMergeFacts `json:"survivorFacts"`
	// Collections and Tags are the memberships the merge copied onto the
	// survivor; undo removes exactly these.
	Collections []string `json:"collections,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	// QueueProfiles are the Profiles whose queue entry for the merged Media
	// the merge points at the survivor.
	QueueProfiles []ProfileID `json:"queueProfiles,omitempty"`
}

// versionMergeRestored is a merged Media an undo put back.
type versionMergeRestored struct {
	loser MediaID
	state versionMergeItemState
}

type versionMergeCandidate struct {
	id     MediaID
	rowid  int64
	files  int
	facts  versionMergeFacts
	file   MediaFile
	single bool // exactly one live, part-0, unlabelled file
}

// MergeSplitVersions heals a title a scan split before it labelled Versions
// (CORE-2473): mediaIDs are Media of libraryID whose files the caller grouped
// under one version group key. The Media with the most files, the earliest
// imported on a tie, survives; each other candidate holding a single live
// unlabelled file moves that file onto the survivor under a derived label
// (placeDerivedVersionTx), is trashed and is journaled as one undoable batch
// (UndoBatch). User state follows the CopyEntityItemState pattern: copies
// that never overwrite the survivor's own values. Collections and tags are
// copied in the merge's transaction; favourites, ratings, watchlist entries
// and watch progress are copied after it, and queue entries are repointed.
// Those copies stay on undo, as an entity merge's do.
//
// It does nothing for an encrypted Library (its catalog lives on its own
// plane), when trash is disabled (the merged Media could not be undone), for
// fewer than two candidates, or for a Media a merge of this kind already
// folded and the user undid. A file no derived label tells apart stays on its
// own Media.
func (s *Store) MergeSplitVersions(ctx context.Context, libraryID LibraryID, mediaIDs []MediaID, now time.Time) (VersionMerge, error) {
	if libraryID == "" {
		return VersionMerge{}, errors.New("library ID is required")
	}
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return VersionMerge{}, err
	}
	if err := s.guardAuthority(serverrole.FaceEngagement); err != nil {
		return VersionMerge{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return VersionMerge{}, err
	}
	if len(mediaIDs) < 2 || s.trashDisabled {
		return VersionMerge{}, nil
	}
	var encrypted int
	if err := s.catalogR.QueryRowContext(ctx, `SELECT encrypted FROM library WHERE id = ?`, libraryID).Scan(&encrypted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return VersionMerge{}, nil
		}
		return VersionMerge{}, err
	}
	if encrypted != 0 {
		return VersionMerge{}, nil
	}
	now = now.UTC()
	batchID, err := newBatchID()
	if err != nil {
		return VersionMerge{}, err
	}

	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return VersionMerge{}, err
	}
	defer tx.Rollback()

	candidates, err := versionMergeCandidatesTx(ctx, tx, libraryID, mediaIDs)
	if err != nil {
		return VersionMerge{}, err
	}
	if len(candidates) < 2 {
		return VersionMerge{}, nil
	}
	survivor := candidates[0]
	for _, c := range candidates[1:] {
		if c.files > survivor.files || (c.files == survivor.files && c.rowid < survivor.rowid) {
			survivor = c
		}
	}
	if err := insertBatchTx(ctx, tx, batchID, opKindMergeVersion, now); err != nil {
		return VersionMerge{}, err
	}
	result := VersionMerge{BatchID: batchID, Survivor: survivor.id}
	var states []versionMergeItemState
	queued := map[ProfileID]bool{}
	for _, loser := range candidates {
		if loser.id == survivor.id || !loser.single {
			continue
		}
		state, merged, err := s.mergeVersionTx(ctx, tx, survivor.id, loser, queued, now)
		if err != nil {
			return VersionMerge{}, err
		}
		if !merged {
			continue
		}
		prior, err := json.Marshal(state)
		if err != nil {
			return VersionMerge{}, err
		}
		if err := appendBatchItemTx(ctx, tx, batchID, len(result.Merged), loser.id, prior); err != nil {
			return VersionMerge{}, err
		}
		result.Merged = append(result.Merged, loser.id)
		states = append(states, state)
	}
	if len(result.Merged) == 0 {
		return VersionMerge{}, nil
	}
	if err := tx.Commit(); err != nil {
		return VersionMerge{}, err
	}

	// Emit after the catalog commit (appendMediaEventLocked): each merged
	// Media is gone like a trash, and the survivor gained a Version.
	deletePayload := mediaDeletedPayload(libraryID)
	for _, id := range result.Merged {
		if eventErr := s.appendMediaEventLocked(ctx, MediaEventDeleted, id, deletePayload, now); eventErr != nil {
			slog.Warn("media outbox event append failed", "kind", MediaEventDeleted, "media", id, "error", eventErr)
		}
	}
	if eventErr := s.appendMediaEventLocked(ctx, MediaEventUpdated, survivor.id, nil, now); eventErr != nil {
		slog.Warn("media outbox event append failed", "kind", MediaEventUpdated, "media", survivor.id, "error", eventErr)
	}

	// The merge is committed and undoable either way; a failed copy leaves the
	// merged Media's state on that (trashed) Media, where undo finds it again.
	if err := s.copyVersionMergeEngagementLocked(ctx, libraryID, batchID, survivor.id, result.Merged, states, now); err != nil {
		return result, fmt.Errorf("version merge %s committed; copying user state failed: %w", batchID, err)
	}
	return result, nil
}

// versionMergeCandidatesTx loads the live Media of libraryID among ids, in
// order and without duplicates, leaving out any Media an undone version merge
// journaled: the user split it back, so a rescan must not fold it again.
func versionMergeCandidatesTx(ctx context.Context, tx *sql.Tx, libraryID LibraryID, ids []MediaID) ([]versionMergeCandidate, error) {
	seen := map[MediaID]bool{}
	var out []versionMergeCandidate
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		var c versionMergeCandidate
		c.id = id
		var undone int
		err := tx.QueryRowContext(ctx, `
			SELECT m.rowid, m.container, m.codec, m.duration_seconds, m.video_width, m.video_height,
			  (SELECT COUNT(*) FROM media_file f WHERE f.media_id = m.id),
			  EXISTS (SELECT 1 FROM op_batch_item i JOIN op_batch b ON b.id = i.batch_id
			          WHERE i.media_id = m.id AND b.kind = ? AND b.undone_at_ms <> 0)
			FROM media m
			WHERE m.id = ? AND m.library_id = ? AND m.trashed_at_ms IS NULL
		`, opKindMergeVersion, id, libraryID).Scan(&c.rowid, &c.facts.Container, &c.facts.Codec,
			&c.facts.DurationSeconds, &c.facts.VideoWidth, &c.facts.VideoHeight, &c.files, &undone)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if undone != 0 {
			continue
		}
		if c.files == 1 {
			err := tx.QueryRowContext(ctx, `
				SELECT path, content_fingerprint FROM media_file
				WHERE media_id = ? AND part_ordinal = 0 AND version_label = '' AND missing_at_ms IS NULL
			`, id).Scan(&c.file.Path, &c.file.ContentFingerprint)
			switch {
			case err == nil:
				c.single = true
			case !errors.Is(err, sql.ErrNoRows):
				return nil, err
			}
		}
		out = append(out, c)
	}
	return out, nil
}

// mergeVersionTx moves loser's file onto survivor under a derived label,
// copies its collection and tag memberships, plans its queue repoints and
// trashes it. merged is false, with nothing written, when no derived label
// tells the file from the survivor's Versions.
func (s *Store) mergeVersionTx(ctx context.Context, tx *sql.Tx, survivor MediaID, loser versionMergeCandidate, queued map[ProfileID]bool, now time.Time) (versionMergeItemState, bool, error) {
	state := versionMergeItemState{Survivor: survivor, Fingerprint: loser.file.ContentFingerprint}
	priorFirst, err := firstVersionFingerprintSQL(ctx, tx, survivor)
	if err != nil {
		return state, false, err
	}
	priorLabels, err := partZeroLabelsTx(ctx, tx, survivor)
	if err != nil {
		return state, false, err
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT container, codec, duration_seconds, video_width, video_height FROM media WHERE id = ?
	`, survivor).Scan(&state.SurvivorFacts.Container, &state.SurvivorFacts.Codec,
		&state.SurvivorFacts.DurationSeconds, &state.SurvivorFacts.VideoWidth, &state.SurvivorFacts.VideoHeight); err != nil {
		return state, false, err
	}
	file := ImportMediaFile{
		Path:                loser.file.Path,
		Container:           loser.facts.Container,
		Codec:               loser.facts.Codec,
		ContentFingerprint:  loser.file.ContentFingerprint,
		DurationSecondsHint: loser.facts.DurationSeconds,
		VideoWidthHint:      loser.facts.VideoWidth,
		VideoHeightHint:     loser.facts.VideoHeight,
	}
	label, err := placeDerivedVersionTx(ctx, tx, MediaVersionImport{
		MediaID:           survivor,
		File:              file,
		AnchorFingerprint: priorFirst,
		DeriveLabels:      true,
	}, priorFirst)
	if errors.Is(err, ErrVersionSlotTaken) {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}
	state.Label = label
	afterLabels, err := partZeroLabelsTx(ctx, tx, survivor)
	if err != nil {
		return state, false, err
	}
	for fingerprint, prior := range priorLabels {
		if afterLabels[fingerprint] != prior {
			if state.Relabels == nil {
				state.Relabels = map[string]string{}
			}
			state.Relabels[fingerprint] = prior
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE media_file SET media_id = ?, version_label = ?
		WHERE content_fingerprint = ? AND media_id = ?
	`, survivor, label, loser.file.ContentFingerprint, loser.id); err != nil {
		return state, false, err
	}
	if err := refreshFirstVersionFactsTx(ctx, tx, survivor, file, priorFirst); err != nil {
		return state, false, err
	}
	if state.Collections, err = copyMediaMembershipTx(ctx, tx, `
		SELECT collection_id FROM collection_media AS c
		WHERE media_id = ? AND NOT EXISTS (
		  SELECT 1 FROM collection_media WHERE collection_id = c.collection_id AND media_id = ?)
		ORDER BY collection_id
	`, `
		INSERT INTO collection_media(collection_id, media_id, sort_order, added_at_ms)
		SELECT collection_id, ?, sort_order, added_at_ms FROM collection_media
		WHERE collection_id = ? AND media_id = ?
	`, loser.id, survivor); err != nil {
		return state, false, err
	}
	if state.Tags, err = copyMediaMembershipTx(ctx, tx, `
		SELECT tag_id FROM media_tag AS t
		WHERE media_id = ? AND NOT EXISTS (
		  SELECT 1 FROM media_tag WHERE tag_id = t.tag_id AND media_id = ?)
		ORDER BY tag_id
	`, `
		INSERT INTO media_tag(tag_id, media_id)
		SELECT tag_id, ? FROM media_tag WHERE tag_id = ? AND media_id = ?
	`, loser.id, survivor); err != nil {
		return state, false, err
	}
	profiles, err := queueProfilesHoldingOnly(ctx, s.eventsR, loser.id, survivor)
	if err != nil {
		return state, false, err
	}
	for _, profile := range profiles {
		if queued[profile] {
			continue
		}
		queued[profile] = true
		state.QueueProfiles = append(state.QueueProfiles, profile)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE media SET trashed_at_ms = ? WHERE id = ? AND trashed_at_ms IS NULL
	`, timeToMillis(now), loser.id); err != nil {
		return state, false, err
	}
	return state, true, nil
}

// partZeroLabelsTx maps each part-0 file of the Media to its Version label.
func partZeroLabelsTx(ctx context.Context, tx *sql.Tx, mediaID MediaID) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT content_fingerprint, version_label FROM media_file WHERE media_id = ? AND part_ordinal = 0
	`, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	labels := map[string]string{}
	for rows.Next() {
		var fingerprint, label string
		if err := rows.Scan(&fingerprint, &label); err != nil {
			return nil, err
		}
		labels[fingerprint] = label
	}
	return labels, rows.Err()
}

// copyMediaMembershipTx lists the memberships from holds and to lacks (list
// takes from, to), copies each onto to (insert takes to, the key, from) and
// returns their keys.
func copyMediaMembershipTx(ctx context.Context, tx *sql.Tx, list, insert string, from, to MediaID) ([]string, error) {
	rows, err := tx.QueryContext(ctx, list, from, to)
	if err != nil {
		return nil, err
	}
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		keys = append(keys, key)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, insert, to, key, from); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// copyVersionMergeEngagementLocked copies each merged Media's per-Profile
// state onto the survivor in one events transaction, never overwriting a
// value the survivor holds: favourites, ratings and watchlist entries by
// INSERT OR IGNORE; watch progress, for a Profile with none on the survivor,
// through a playback projection event so a projection rebuild replays it (a
// copied event counts as one play in derived play history); queue entries by
// repointing. It also records the merge in the activity log.
func (s *Store) copyVersionMergeEngagementLocked(ctx context.Context, libraryID LibraryID, batchID string, survivor MediaID, merged []MediaID, states []versionMergeItemState, now time.Time) error {
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, loser := range merged {
		for _, stmt := range []string{
			`INSERT OR IGNORE INTO media_favorite(profile_id, media_id, favorited_at_ms)
			 SELECT profile_id, ?, favorited_at_ms FROM media_favorite WHERE media_id = ?`,
			`INSERT OR IGNORE INTO item_rating(profile_id, target_kind, target_id, rating, rated_at_ms)
			 SELECT profile_id, target_kind, ?, rating, rated_at_ms FROM item_rating
			 WHERE target_kind = 'media' AND target_id = ?`,
			`INSERT OR IGNORE INTO watchlist_item(profile_id, target_kind, target_id, added_at_ms)
			 SELECT profile_id, target_kind, ?, added_at_ms FROM watchlist_item
			 WHERE target_kind = 'media' AND target_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, stmt, survivor, loser); err != nil {
				return err
			}
		}
		if err := copyVersionMergeProgressTx(ctx, tx, loser, survivor); err != nil {
			return err
		}
		for _, profile := range states[i].QueueProfiles {
			if err := repointQueueMediaTx(ctx, tx, profile, loser, survivor); err != nil {
				return err
			}
		}
	}
	if err := appendActivitySQL(ctx, tx, ActivityEntry{
		OccurredAt: now,
		Action:     ActivityMediaMerge,
		TargetType: "batch",
		TargetID:   batchID,
		LibraryID:  libraryID,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

// copyVersionMergeProgressTx copies from's watch progress onto to for each
// Profile with no progress row on to.
func copyVersionMergeProgressTx(ctx context.Context, tx *sql.Tx, from, to MediaID) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT profile_id, position_seconds, duration_seconds, watched, updated_at_ms, play_count,
		  last_played_at_ms, provenance, occurred_at_ms, COALESCE(import_run_id, ''),
		  COALESCE(import_importer_id, ''), COALESCE(import_match_method, ''), event_time_ms, channel_context
		FROM playback_progress AS p
		WHERE media_id = ? AND NOT EXISTS (
		  SELECT 1 FROM playback_progress WHERE profile_id = p.profile_id AND media_id = ?)
		ORDER BY profile_id
	`, from, to)
	if err != nil {
		return err
	}
	var events []playbackProjectionEvent
	for rows.Next() {
		event := playbackProjectionEvent{Operation: playbackProjectionOperationProgress, MediaID: string(to)}
		var watched, channel int
		var lastPlayed, occurred, eventTime sql.NullInt64
		if err := rows.Scan(&event.ProfileID, &event.PositionSeconds, &event.DurationSeconds, &watched,
			&event.UpdatedAtMS, &event.PlayCount, &lastPlayed, &event.Provenance, &occurred,
			&event.ImportRunID, &event.ImportImporterID, &event.ImportMatch, &eventTime, &channel); err != nil {
			return errors.Join(err, rows.Close())
		}
		event.Watched = watched != 0
		event.ChannelContext = channel != 0
		event.ExplicitCount = event.PlayCount
		event.LastPlayedAtMS = nullInt64Ptr(lastPlayed)
		event.OccurredAtMS = nullInt64Ptr(occurred)
		event.EventTimeMS = nullInt64Ptr(eventTime)
		events = append(events, event)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, event := range events {
		if err := appendPlaybackProjectionEvent(ctx, tx, event); err != nil {
			return err
		}
		if err := applyPlaybackProjectionEvent(ctx, tx, event); err != nil {
			return err
		}
	}
	return nil
}

func nullInt64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

// undoVersionMergeBatch reverses a version merge (op_batch.kind
// "merge-version"), last merged first: each merged Media gets its file back
// in the default slot, the survivor its prior labels, probe facts and
// memberships, and the merged Media leaves the trash; then queue entries the
// merge repointed point back. Copied favourites, ratings, watchlist entries
// and progress stay on the survivor, as an entity merge's copies do. A merged
// Media outside the caller's scope or already purged is skipped; one whose
// file has since left the survivor refuses the undo.
func (s *Store) undoVersionMergeBatch(ctx context.Context, batchID string, filter LibraryFilter, now time.Time) (BulkOpBatch, error) {
	items, err := batchItems(ctx, s.catalogR, batchID)
	if err != nil {
		return BulkOpBatch{}, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return BulkOpBatch{}, err
	}
	defer tx.Rollback()

	var done []versionMergeRestored
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		var state versionMergeItemState
		if err := json.Unmarshal([]byte(item.PriorState), &state); err != nil {
			return BulkOpBatch{}, err
		}
		if _, err := visibleMediaTx(ctx, tx, item.MediaID, filter); errors.Is(err, errMediaNotVisible) {
			continue
		} else if err != nil {
			return BulkOpBatch{}, err
		}
		if err := undoVersionMergeItemTx(ctx, tx, item.MediaID, state); err != nil {
			return BulkOpBatch{}, err
		}
		done = append(done, versionMergeRestored{item.MediaID, state})
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE op_batch SET undone_at_ms = ? WHERE id = ?`, timeToMillis(now), batchID); err != nil {
		return BulkOpBatch{}, err
	}
	if err := tx.Commit(); err != nil {
		return BulkOpBatch{}, err
	}

	updated := map[MediaID]bool{}
	for _, r := range done {
		if eventErr := s.appendMediaEventLocked(ctx, MediaEventCreated, r.loser, nil, now); eventErr != nil {
			slog.Warn("media outbox event append failed", "kind", MediaEventCreated, "media", r.loser, "error", eventErr)
		}
		if !updated[r.state.Survivor] {
			updated[r.state.Survivor] = true
			if eventErr := s.appendMediaEventLocked(ctx, MediaEventUpdated, r.state.Survivor, nil, now); eventErr != nil {
				slog.Warn("media outbox event append failed", "kind", MediaEventUpdated, "media", r.state.Survivor, "error", eventErr)
			}
		}
	}
	if err := s.restoreVersionMergeQueuesLocked(ctx, done); err != nil {
		return BulkOpBatch{ID: batchID, Kind: opKindMergeVersion, Affected: len(done)},
			fmt.Errorf("version merge %s undone; restoring queue entries failed: %w", batchID, err)
	}
	return BulkOpBatch{ID: batchID, Kind: opKindMergeVersion, Affected: len(done)}, nil
}

// restoreVersionMergeQueuesLocked points the queue entries a version merge
// moved to the survivor back at their merged Media, in one events
// transaction. An entry the Profile has since removed stays removed.
func (s *Store) restoreVersionMergeQueuesLocked(ctx context.Context, done []versionMergeRestored) error {
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range done {
		for _, profile := range r.state.QueueProfiles {
			if err := repointQueueMediaTx(ctx, tx, profile, r.state.Survivor, r.loser); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// undoVersionMergeItemTx puts one merged Media back as it was before the
// merge, and the survivor back as it was before this Media joined it.
func undoVersionMergeItemTx(ctx context.Context, tx *sql.Tx, loser MediaID, state versionMergeItemState) error {
	var owner MediaID
	err := tx.QueryRowContext(ctx, `SELECT media_id FROM media_file WHERE content_fingerprint = ?`, state.Fingerprint).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && owner != state.Survivor) {
		return ValidationError("a merged Version's file has left its title; the merge cannot be undone")
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE media_file SET media_id = ?, version_label = '' WHERE content_fingerprint = ? AND media_id = ?
	`, loser, state.Fingerprint, state.Survivor); err != nil {
		return err
	}
	for fingerprint, label := range state.Relabels {
		if _, err := tx.ExecContext(ctx, `
			UPDATE media_file SET version_label = ? WHERE content_fingerprint = ? AND media_id = ? AND part_ordinal = 0
		`, label, fingerprint, state.Survivor); err != nil {
			return err
		}
	}
	facts := state.SurvivorFacts
	if _, err := tx.ExecContext(ctx, `
		UPDATE media SET container = ?, codec = ?, duration_seconds = ?, video_width = ?, video_height = ? WHERE id = ?
	`, facts.Container, facts.Codec, facts.DurationSeconds, facts.VideoWidth, facts.VideoHeight, state.Survivor); err != nil {
		return err
	}
	for _, collection := range state.Collections {
		if _, err := tx.ExecContext(ctx, `DELETE FROM collection_media WHERE collection_id = ? AND media_id = ?`, collection, state.Survivor); err != nil {
			return err
		}
	}
	for _, tag := range state.Tags {
		if _, err := tx.ExecContext(ctx, `DELETE FROM media_tag WHERE tag_id = ? AND media_id = ?`, tag, state.Survivor); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE media SET trashed_at_ms = NULL WHERE id = ?`, loser)
	return err
}
