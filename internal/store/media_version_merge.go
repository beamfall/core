package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

// opKindMergeVersion is the op_batch.kind of a version merge: a rescan folding
// a title's already-split unlabelled copy (its own Media) into the title's
// Media as a Version (CORE-2490).
const opKindMergeVersion = "merge-version"

// ErrVersionMergeEngagementPending means the catalog committed and playback
// recovery is pending. The scan must stop before attempting another merge.
var ErrVersionMergeEngagementPending = errors.New("engagement pending")

// VersionMerge reports the selected survivor and the Media folded into it,
// now trashed. BatchID is "" when nothing merged.
type VersionMerge struct {
	BatchID  string
	Survivor MediaID
	Merged   []MediaID
}

// VersionMergeOptions selects a survivor and allows placement without
// derived labels when the caller has verified a sampled and legacy copy.
type VersionMergeOptions struct {
	Survivor     MediaID
	VerifiedCopy *VersionCopyPair
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
	// MovedFiles records the losing Media's original Version labels for a
	// verified-copy merge. Older batches moved one unlabelled file.
	MovedFiles   map[string]string `json:"movedFiles,omitempty"`
	MissingFiles []string          `json:"missingFiles,omitempty"`
	// Relabels maps a survivor file's fingerprint to the label it held before
	// the merge, for each part-0 file the merge relabelled.
	Relabels      map[string]string `json:"relabels,omitempty"`
	SurvivorFacts versionMergeFacts `json:"survivorFacts"`
	// PriorFirst is the survivor's first Version before the merge, the file
	// SurvivorFacts describe.
	PriorFirst string `json:"priorFirst"`
	// Collections and Tags are the memberships the merge copied onto the
	// survivor; undo removes exactly these.
	Collections []string `json:"collections,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	// Queue holds the queue entries for the merged Media the merge pointed
	// at the survivor.
	Queue []queueMove `json:"queue,omitempty"`
	// The catalog commit makes a verified merge eligible for engagement
	// recovery. The append-only activity entry acknowledges the events commit.
	EngagementLibraryID LibraryID `json:"engagementLibraryId,omitempty"`
	SurvivorInsertSeq   int64     `json:"survivorInsertSeq,omitempty"`
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
// copied in the merge's catalog transaction; favourites, ratings, watchlist
// entries and watch progress are copied, and queue entries repointed, in an
// events transaction. A verified merge commits the catalog journal first so
// playback stays untouched on catalog failure and can recover on reopen
// after an events failure. Copies stay on undo, as an entity merge's do;
// undo repoints queue entries.
//
// It does nothing for an encrypted Library (its catalog lives on its own
// plane), when trash is disabled (the merged Media could not be undone), for
// fewer than two candidates, or for a Media a merge of this kind already
// folded and the user undid. An Extra, or a Media in any Media relation (an
// explicit edition-of cut, an extra-of parent or child), is never a candidate:
// those are separate Media on purpose. The default merge requires distinct
// derived labels. A verified copy can use ordinal labels, keeps the requested
// sampled Media first, and combines the two Media's playback progress.
func (s *Store) MergeSplitVersions(ctx context.Context, libraryID LibraryID, mediaIDs []MediaID, now time.Time, opts ...VersionMergeOptions) (VersionMerge, error) {
	if libraryID == "" {
		return VersionMerge{}, errors.New("library ID is required")
	}
	var options VersionMergeOptions
	if len(opts) > 1 {
		return VersionMerge{}, errors.New("only one set of Version merge options is supported")
	}
	if len(opts) == 1 {
		options = opts[0]
	}
	verified := options.VerifiedCopy
	if verified != nil && (len(mediaIDs) != 2 || options.Survivor != verified.Sample.MediaID ||
		!slices.Contains(mediaIDs, verified.Sample.MediaID) || !slices.Contains(mediaIDs, verified.Legacy.MediaID) ||
		verified.Sample.MediaID == verified.Legacy.MediaID) {
		return VersionMerge{}, errors.New("invalid verified Version copy pair")
	}
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return VersionMerge{}, err
	}
	if err := s.guardAuthority(serverrole.FaceEngagement); err != nil {
		return VersionMerge{}, err
	}
	if verified != nil {
		// Keep undo and user writes outside the catalog commit and inline retry.
		s.mu.Lock()
		defer s.mu.Unlock()
	} else {
		s.mu.RLock()
		defer s.mu.RUnlock()
	}
	if err := s.guardLocked(); err != nil {
		return VersionMerge{}, err
	}
	if len(mediaIDs) < 2 || s.trashDisabled {
		return VersionMerge{}, nil
	}
	var encrypted int
	if err := s.catalogR.QueryRowContext(ctx, `SELECT encrypted FROM library WHERE id = ? AND trashed_at_ms IS NULL`, libraryID).Scan(&encrypted); err != nil {
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
	survivor := selectVersionMergeSurvivor(candidates, options.Survivor)
	if survivor.id == "" {
		return VersionMerge{}, nil
	}
	if verified != nil {
		current, err := verifiedCopyStillCurrentTx(ctx, tx, *verified)
		if err != nil || !current {
			return VersionMerge{}, err
		}
	}
	if err := insertBatchTx(ctx, tx, batchID, opKindMergeVersion, now); err != nil {
		return VersionMerge{}, err
	}
	result := VersionMerge{BatchID: batchID, Survivor: survivor.id}
	var states []versionMergeItemState
	for _, loser := range candidates {
		if loser.id == survivor.id || (verified == nil && !loser.single) {
			continue
		}
		state, merged, err := mergeVersionTx(ctx, tx, survivor.id, loser, now, verified != nil)
		if err != nil {
			return VersionMerge{}, err
		}
		if merged {
			result.Merged = append(result.Merged, loser.id)
			states = append(states, state)
		}
	}
	if len(result.Merged) == 0 {
		return VersionMerge{Survivor: survivor.id}, nil
	}

	if verified != nil {
		states[0].EngagementLibraryID = libraryID
		if err := tx.QueryRowContext(ctx, `SELECT seq FROM media_insert_seq WHERE media_id = ?`, survivor.id).
			Scan(&states[0].SurvivorInsertSeq); err != nil {
			return VersionMerge{}, err
		}
		if err := s.stageVersionMergePlayback(ctx, batchID, result.Merged[0], survivor.id); err != nil {
			return VersionMerge{}, err
		}
	}
	etx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return VersionMerge{}, err
	}
	defer etx.Rollback()
	if err := copyVersionMergeEngagementTx(ctx, etx, libraryID, batchID, survivor.id, result.Merged, states, now, verified != nil); err != nil {
		return VersionMerge{}, err
	}
	for i, id := range result.Merged {
		prior, err := json.Marshal(states[i])
		if err != nil {
			return VersionMerge{}, err
		}
		if err := appendBatchItemTx(ctx, tx, batchID, i, id, prior); err != nil {
			return VersionMerge{}, err
		}
	}
	var engagementErr error
	if verified != nil {
		if err := tx.Commit(); err != nil {
			return VersionMerge{}, fmt.Errorf("version merge %s: catalog commit failed: %w", batchID, err)
		}
		if err := etx.Commit(); err != nil {
			if retryErr := s.finishVersionCopyEngagement(ctx, batchID, libraryID, now); retryErr != nil {
				engagementErr = fmt.Errorf("version merge %s: %w: %w", batchID, ErrVersionMergeEngagementPending, errors.Join(err, retryErr))
			}
		}
		if engagementErr == nil {
			if err := clearVersionCopyEngagement(ctx, s.catalogW, batchID); err != nil {
				slog.Warn("could not clear completed Version merge engagement", "batch", batchID, "error", err)
			}
		}
	} else if err := etx.Commit(); err != nil {
		return VersionMerge{}, err
	} else if err := tx.Commit(); err != nil {
		moved := make([]versionMergeRestored, len(states))
		for i, state := range states {
			moved[i] = versionMergeRestored{result.Merged[i], state}
		}
		return VersionMerge{}, errors.Join(fmt.Errorf("version merge %s: catalog commit failed: %w", batchID, err),
			s.revertQueueMovesLocked(ctx, moved, true))
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

	return result, engagementErr
}

func selectVersionMergeSurvivor(candidates []versionMergeCandidate, preferred MediaID) versionMergeCandidate {
	if preferred != "" {
		for _, candidate := range candidates {
			if candidate.id == preferred {
				return candidate
			}
		}
		return versionMergeCandidate{}
	}
	survivor := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.files > survivor.files || (candidate.files == survivor.files && candidate.rowid < survivor.rowid) {
			survivor = candidate
		}
	}
	return survivor
}

// versionMergeCandidatesTx loads the live Media of libraryID among ids, in
// order and without duplicates, leaving out any Media an undone version merge
// journaled: the user split it back, so a rescan must not fold it again. It
// also leaves out Extras and any Media in a Media relation: an edition-of cut
// keeps its own timeline and engagement (CORE-2467), and folding an extra-of
// parent or child would leave its relation pointing at a trashed Media. A
// candidate's files are its live ones; a missing predecessor a replacement
// left behind does not count.
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
			  (SELECT COUNT(*) FROM media_file f WHERE f.media_id = m.id AND f.missing_at_ms IS NULL),
			  EXISTS (SELECT 1 FROM op_batch_item i JOIN op_batch b ON b.id = i.batch_id
			          WHERE i.media_id = m.id AND b.kind = ? AND b.undone_at_ms <> 0)
			FROM media m
			WHERE m.id = ? AND m.library_id = ? AND m.trashed_at_ms IS NULL AND m.extra = 0
			  AND NOT EXISTS (SELECT 1 FROM media_relation r WHERE r.from_media_id = m.id OR r.to_media_id = m.id)
			  AND NOT EXISTS (SELECT 1 FROM op_batch_item
			                  WHERE json_extract(prior_state, '$.engagementLibraryId') IS NOT NULL
			                    AND json_extract(prior_state, '$.survivor') = m.id)
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
// copies its collection and tag memberships and trashes it. merged is false,
// with nothing written, when no derived label tells the file from the
// survivor's Versions.
func mergeVersionTx(ctx context.Context, tx *sql.Tx, survivor MediaID, loser versionMergeCandidate, now time.Time, verifiedCopy bool) (versionMergeItemState, bool, error) {
	state := versionMergeItemState{Survivor: survivor}
	priorFirst, err := firstVersionFingerprintSQL(ctx, tx, survivor)
	if err != nil {
		return state, false, err
	}
	state.PriorFirst = priorFirst
	if verifiedCopy {
		state.MovedFiles, state.MissingFiles, err = placeMergedVersionsTx(ctx, tx, survivor, loser.id, priorFirst)
		if err != nil || len(state.MovedFiles) == 0 {
			return state, false, err
		}
	} else if err := placeDerivedMergedVersionTx(ctx, tx, loser, &state); err != nil {
		if errors.Is(err, ErrVersionSlotTaken) {
			return state, false, nil
		}
		return state, false, err
	}
	if err := finishVersionMergeTx(ctx, tx, loser.id, &state, now); err != nil {
		return state, false, err
	}
	return state, true, nil
}

func placeDerivedMergedVersionTx(ctx context.Context, tx *sql.Tx, loser versionMergeCandidate, state *versionMergeItemState) error {
	survivor, priorFirst := state.Survivor, state.PriorFirst
	state.Fingerprint = loser.file.ContentFingerprint
	priorLabels, err := partZeroLabelsTx(ctx, tx, survivor)
	if err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT container, codec, duration_seconds, video_width, video_height FROM media WHERE id = ?
	`, survivor).Scan(&state.SurvivorFacts.Container, &state.SurvivorFacts.Codec,
		&state.SurvivorFacts.DurationSeconds, &state.SurvivorFacts.VideoWidth, &state.SurvivorFacts.VideoHeight); err != nil {
		return err
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
	if err != nil {
		return err
	}
	state.Label = label
	afterLabels, err := partZeroLabelsTx(ctx, tx, survivor)
	if err != nil {
		return err
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
		return err
	}
	return refreshFirstVersionFactsTx(ctx, tx, survivor, file, priorFirst)
}

func finishVersionMergeTx(ctx context.Context, tx *sql.Tx, loser MediaID, state *versionMergeItemState, now time.Time) error {
	survivor := state.Survivor
	var err error
	if state.Collections, err = copyMediaMembershipTx(ctx, tx, `
		SELECT collection_id FROM collection_media AS c
		WHERE media_id = ? AND NOT EXISTS (
		  SELECT 1 FROM collection_media WHERE collection_id = c.collection_id AND media_id = ?)
		ORDER BY collection_id
	`, `
		INSERT INTO collection_media(collection_id, media_id, sort_order, added_at_ms)
		SELECT collection_id, ?, sort_order, added_at_ms FROM collection_media
		WHERE collection_id = ? AND media_id = ?
	`, loser, survivor); err != nil {
		return err
	}
	if state.Tags, err = copyMediaMembershipTx(ctx, tx, `
		SELECT tag_id FROM media_tag AS t
		WHERE media_id = ? AND NOT EXISTS (
		  SELECT 1 FROM media_tag WHERE tag_id = t.tag_id AND media_id = ?)
		ORDER BY tag_id
	`, `
		INSERT INTO media_tag(tag_id, media_id)
		SELECT tag_id, ? FROM media_tag WHERE tag_id = ? AND media_id = ?
	`, loser, survivor); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE media SET trashed_at_ms = ? WHERE id = ? AND trashed_at_ms IS NULL
	`, timeToMillis(now), loser)
	return err
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

// copyVersionMergeEngagementTx copies each merged Media's per-Profile state
// onto the survivor in the events transaction tx, never overwriting a value
// the survivor holds. For verified copies, both Media share a timeline so
// playback progress and counts combine. Queue entries are repointed and
// recorded in states for undo. The merge also goes in the activity log.
func copyVersionMergeEngagementTx(ctx context.Context, tx *sql.Tx, libraryID LibraryID, batchID string, survivor MediaID, merged []MediaID, states []versionMergeItemState, now time.Time, verifiedCopy bool) error {
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
		if verifiedCopy {
			if err := applyPendingVersionMergePlayback(ctx, tx, batchID); err != nil {
				return err
			}
		} else {
			progress, err := versionMergeProgressEventsTx(ctx, tx, loser, survivor, false)
			if err != nil {
				return err
			}
			for _, event := range progress {
				if err := recordVersionMergeProgressTx(ctx, tx, event); err != nil {
					return err
				}
			}
		}
		moves, err := moveQueueEntriesTx(ctx, tx, loser, survivor)
		if err != nil {
			return err
		}
		states[i].Queue = moves
	}
	return appendActivitySQL(ctx, tx, ActivityEntry{
		OccurredAt: now,
		Action:     ActivityMediaMerge,
		TargetType: "batch",
		TargetID:   batchID,
		LibraryID:  libraryID,
	})
}

// Only verified copies combine plays when the survivor already has progress.
func versionMergeProgressEventsTx(ctx context.Context, tx *sql.Tx, from, to MediaID, verifiedCopy bool) ([]playbackProjectionEvent, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT profile_id, position_seconds, duration_seconds, watched, updated_at_ms, play_count,
		  last_played_at_ms, provenance, occurred_at_ms, COALESCE(import_run_id, ''),
		  COALESCE(import_importer_id, ''), COALESCE(import_match_method, ''), event_time_ms, channel_context,
		  continue_hidden
		FROM playback_progress AS p
		WHERE media_id = ? AND (? OR NOT EXISTS (
		  SELECT 1 FROM playback_progress WHERE profile_id = p.profile_id AND media_id = ?))
		ORDER BY profile_id
	`, from, verifiedCopy, to)
	if err != nil {
		return nil, err
	}
	var events []playbackProjectionEvent
	for rows.Next() {
		event := playbackProjectionEvent{Operation: playbackProjectionOperationVersionMerge, MediaID: string(to)}
		var watched, channel, hidden int
		var lastPlayed, occurred, eventTime sql.NullInt64
		if err := rows.Scan(&event.ProfileID, &event.PositionSeconds, &event.DurationSeconds, &watched,
			&event.UpdatedAtMS, &event.PlayCount, &lastPlayed, &event.Provenance, &occurred,
			&event.ImportRunID, &event.ImportImporterID, &event.ImportMatch, &eventTime, &channel, &hidden); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		event.Watched = watched != 0
		event.ChannelContext = channel != 0
		event.ContinueHidden = hidden != 0
		event.ExplicitCount = event.PlayCount
		event.LastPlayedAtMS = nullInt64Ptr(lastPlayed)
		event.OccurredAtMS = nullInt64Ptr(occurred)
		event.EventTimeMS = nullInt64Ptr(eventTime)
		events = append(events, event)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if verifiedCopy {
		for i := range events {
			event := &events[i]
			var copied int
			if err := tx.QueryRowContext(ctx, `
				SELECT COALESCE((
					SELECT json_extract(payload, '$.explicitCount') FROM engagement_event
					WHERE profile_id = ? AND media_id = ? AND kind = ?
					  AND json_extract(payload, '$.mergedFromMediaId') = ?
					ORDER BY rowid DESC LIMIT 1
				), 0)
			`, event.ProfileID, to, playbackProjectionEventKind, from).Scan(&copied); err != nil {
				return nil, err
			}
			event.PlayCount = max(0, event.ExplicitCount-copied)
			event.ExplicitCount = max(event.ExplicitCount, copied)
			event.MergedFromMediaID = string(from)
		}
	}
	return events, nil
}

func recordVersionMergeProgressTx(ctx context.Context, tx *sql.Tx, event playbackProjectionEvent) error {
	if err := appendPlaybackProjectionEvent(ctx, tx, event); err != nil {
		return err
	}
	return applyPlaybackProjectionEvent(ctx, tx, event)
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
// memberships, and the merged Media leaves the trash; queue entries the merge
// repointed point back, in an events transaction that commits just before
// the catalog one. Copied favourites, ratings, watchlist entries and progress
// stay on the survivor, as an entity merge's copies do. A merged Media
// already purged is skipped; one outside the caller's scope, or whose file
// has since left the survivor, gone missing or been replaced, refuses the undo.
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

	// Check every item before replaying any, so a hidden item reads as not
	// found rather than surfacing another item's file error first.
	var pending []versionMergeRestored
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		var state versionMergeItemState
		if err := json.Unmarshal([]byte(item.PriorState), &state); err != nil {
			return BulkOpBatch{}, err
		}
		if _, err := visibleMediaTx(ctx, tx, item.MediaID, filter); errors.Is(err, errMediaNotVisible) {
			// A purged copy has nothing to restore. One the caller cannot see
			// refuses the whole undo: skipping it would mark the batch undone
			// while its file stays on the survivor, leaving no way to split it.
			// A merge never spans Libraries, so the batch reads as not found.
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM media WHERE id = ?)`, item.MediaID).Scan(&exists); err != nil {
				return BulkOpBatch{}, err
			}
			if exists {
				return BulkOpBatch{}, ValidationError("batch not found")
			}
			continue
		} else if err != nil {
			return BulkOpBatch{}, err
		}
		// The caller must also pass the certification ceiling for the folded
		// item and the survivor: Trash names neither when one is hidden, and
		// an undo would rewrite the survivor's Versions and metadata.
		if ok, err := certifiedMediaTx(ctx, tx, filter, item.MediaID, state.Survivor); err != nil {
			return BulkOpBatch{}, err
		} else if !ok {
			return BulkOpBatch{}, ValidationError("batch not found")
		}
		pending = append(pending, versionMergeRestored{item.MediaID, state})
	}
	var done []versionMergeRestored
	for _, r := range pending {
		if err := undoVersionMergeItemTx(ctx, tx, r.loser, r.state); err != nil {
			return BulkOpBatch{}, err
		}
		done = append(done, r)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE op_batch SET undone_at_ms = ? WHERE id = ?`, timeToMillis(now), batchID); err != nil {
		return BulkOpBatch{}, err
	}
	if err := clearVersionCopyEngagement(ctx, tx, batchID); err != nil {
		return BulkOpBatch{}, err
	}
	etx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return BulkOpBatch{}, err
	}
	defer etx.Rollback()
	// Only the original activity entry certifies the planned queue moves.
	// Playback-only recovery uses the batch ID as its activity operation ID.
	var queueMoved bool
	if err := etx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM activity_entry WHERE action = ? AND target_id = ? AND operation_id IS NULL)
	`, ActivityMediaMerge, batchID).Scan(&queueMoved); err != nil {
		return BulkOpBatch{}, err
	}
	for i := range done {
		r := &done[i]
		if !queueMoved {
			r.state.Queue = nil
		}
		for _, m := range r.state.Queue {
			if err := repointQueueEntryTx(ctx, etx, m, r.state.Survivor, r.loser); err != nil {
				return BulkOpBatch{}, err
			}
		}
	}
	if err := etx.Commit(); err != nil {
		return BulkOpBatch{}, err
	}
	if err := tx.Commit(); err != nil {
		return BulkOpBatch{}, errors.Join(fmt.Errorf("version merge %s undo: catalog commit failed: %w", batchID, err),
			s.revertQueueMovesLocked(ctx, done, false))
	}
	if _, err := s.eventsW.ExecContext(ctx, `DELETE FROM version_merge_playback WHERE batch_id = ?`, batchID); err != nil {
		slog.Warn("could not clear undone Version merge engagement", "batch", batchID, "error", err)
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
	return BulkOpBatch{ID: batchID, Kind: opKindMergeVersion, Affected: len(done)}, nil
}

// revertQueueMovesLocked reverses the queue moves an events commit carried
// when the catalog commit that should have followed it failed: toLoser undoes
// a merge's moves, otherwise an undo's. It runs in its own events transaction,
// outside the caller's possibly cancelled context.
func (s *Store) revertQueueMovesLocked(ctx context.Context, items []versionMergeRestored, toLoser bool) error {
	ctx = context.WithoutCancel(ctx)
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range items {
		from, to := r.loser, r.state.Survivor
		if toLoser {
			from, to = to, from
		}
		for _, m := range r.state.Queue {
			if err := repointQueueEntryTx(ctx, tx, m, from, to); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// undoVersionMergeItemTx puts one merged Media back as it was before the
// merge, and the survivor back as it was before this Media joined it.
func undoVersionMergeItemTx(ctx context.Context, tx *sql.Tx, loser MediaID, state versionMergeItemState) error {
	firstBefore, err := firstVersionFingerprintSQL(ctx, tx, state.Survivor)
	if err != nil {
		return err
	}
	files := state.MovedFiles
	if len(files) == 0 {
		files = map[string]string{state.Fingerprint: ""}
	}
	for fingerprint, label := range files {
		if err := undoVersionMergeFileTx(ctx, tx, loser, state.Survivor, fingerprint, label, slices.Contains(state.MissingFiles, fingerprint)); err != nil {
			return err
		}
	}
	for fingerprint, label := range state.Relabels {
		if _, err := tx.ExecContext(ctx, `
			UPDATE media_file SET version_label = ? WHERE content_fingerprint = ? AND media_id = ? AND part_ordinal = 0
		`, label, fingerprint, state.Survivor); err != nil {
			return err
		}
	}
	if err := restoreVersionMergeFactsTx(ctx, tx, state, firstBefore); err != nil {
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

func undoVersionMergeFileTx(ctx context.Context, tx *sql.Tx, loser, survivor MediaID, fingerprint, label string, wasMissing bool) error {
	var owner MediaID
	var missing bool
	err := tx.QueryRowContext(ctx, `
		SELECT media_id, missing_at_ms IS NOT NULL FROM media_file WHERE content_fingerprint = ?
	`, fingerprint).Scan(&owner, &missing)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && owner != survivor) {
		return ValidationError("a merged Version's file has left its title; the merge cannot be undone")
	}
	if err != nil {
		return err
	}
	if missing && !wasMissing {
		// A replacement in place would stay behind on the survivor, and the
		// restored Media would hold no live file.
		return ValidationError("a merged Version's file is missing or was replaced; the merge cannot be undone")
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE media_file SET media_id = ?, version_label = ? WHERE content_fingerprint = ? AND media_id = ?
	`, loser, label, fingerprint, survivor)
	return err
}

// restoreVersionMergeFactsTx brings the survivor's probe facts back in line
// with its first Version once a merged file has left; firstBefore was first
// just before it left. When the first Version is again the one first before
// the merge, its snapshot facts return, preserving the current runtime if the
// primary fingerprint stayed the same. Otherwise, if the departed file was
// first (so the row's facts were its), the row stops claiming its frame size,
// as refreshFirstVersionFactsTx does for a first file the store holds no facts
// for. Merges undone out of order thus never restore stale facts.
func restoreVersionMergeFactsTx(ctx context.Context, tx *sql.Tx, state versionMergeItemState, firstBefore string) error {
	if len(state.MovedFiles) != 0 {
		// The verified-copy merge never changed the first Version's facts.
		if _, moved := state.MovedFiles[firstBefore]; !moved {
			return nil
		}
		_, err := tx.ExecContext(ctx, `UPDATE media SET video_width = 0, video_height = 0 WHERE id = ?`, state.Survivor)
		return err
	}
	first, err := firstVersionFingerprintSQL(ctx, tx, state.Survivor)
	if err != nil {
		return err
	}
	switch {
	case first == state.PriorFirst:
		facts := state.SurvivorFacts
		_, err = tx.ExecContext(ctx, `
			UPDATE media SET container = ?, codec = ?,
				duration_seconds = CASE WHEN ? THEN duration_seconds ELSE ? END,
				video_width = ?, video_height = ? WHERE id = ?
		`, facts.Container, facts.Codec, first == firstBefore, facts.DurationSeconds, facts.VideoWidth, facts.VideoHeight, state.Survivor)
	case firstBefore == state.Fingerprint:
		_, err = tx.ExecContext(ctx, `UPDATE media SET video_width = 0, video_height = 0 WHERE id = ?`, state.Survivor)
	}
	return err
}

// certifiedMediaTx reports whether every id passes the caller's
// certification ceiling and access grants. A merge runs only on the shared
// catalog, so the shared-catalog clause applies.
func certifiedMediaTx(ctx context.Context, tx *sql.Tx, filter LibraryFilter, ids ...MediaID) (bool, error) {
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	if certClause == "" {
		return true, nil
	}
	for _, id := range ids {
		var found bool
		args := append([]any{id}, certArgs...)
		if err := tx.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM media m WHERE m.id = ?`+certClause+`)`, args...).Scan(&found); err != nil {
			return false, err
		}
		if !found {
			return false, nil
		}
	}
	return true, nil
}
