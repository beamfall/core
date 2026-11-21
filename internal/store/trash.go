package store

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// TrashedMedia is one entry in the server-side trash.
type TrashedMedia struct {
	ID        MediaID
	LibraryID LibraryID
	Title     string
	TrashedAt time.Time
	// Merge is set when a Version merge that has not been undone folded this
	// Media into another, rather than anyone deleting it (CORE-2916).
	Merge *TrashedMediaMerge
}

// TrashedMediaMerge names the merge batch that trashed a Media and the Media
// that kept its files as Versions.
type TrashedMediaMerge struct {
	BatchID       string
	Survivor      MediaID
	SurvivorTitle string
	MergedAt      time.Time
}

// TrashRetention is how long trashed media stays restorable before purge.
const TrashRetention = 30 * 24 * time.Hour

// ErrTrashDisabledConfirmationRequired reports that an autonomous destructive
// sweep refused to run because trash is disabled. The operator may perform the
// corresponding human-confirmed operation instead. All autonomous sweeps use
// the same result contract: skipped=true with this sentinel means confirmation
// required; skipped=true with nil means an ordinary skip; skipped=false with nil
// means success; every other non-nil error means failure.
var ErrTrashDisabledConfirmationRequired = errors.New("trash disabled: confirmation required")

// TrashMedia soft-deletes a Media item the caller can see (hard invariant 6:
// destruction is recoverable — the catalog row and source file both survive
// until the retention purge, and the purge never touches source files).
//
// trashDisabled asymmetry: unlike the autonomous retention sweep
// (ApplySubscriptionRetentionPolicies), TrashMedia is deliberately NOT gated
// on s.trashDisabled. Invariant 6 gates *autonomous* destruction ("destructive
// operations may be autonomous only while trash is enabled; with trash disabled
// they require human confirmation"). TrashMedia and its bulk wrapper
// HealthBulkTrash are reached only through human-initiated, admin-gated GraphQL
// mutations that the web Console already fronts with an explicit confirmation
// (e.g. strings.confirmnBroken). The trashDisabled toggle still works as
// intended: when trash is off these confirmed deletes route to the same audited
// soft-delete (the catalog row survives a TrashRetention window and the purge
// never deletes source files), so destruction stays recoverable. Gating here
// would silently break a legitimately human-confirmed operation, which the
// invariant does not require — only the unattended sweep must yield.
func (s *Store) TrashMedia(ctx context.Context, id MediaID, filter LibraryFilter) error {
	return s.trashMediaAt(ctx, id, filter, time.Now().UTC())
}

func (s *Store) trashMediaAt(ctx context.Context, id MediaID, filter LibraryFilter, now time.Time) error {
	_, err := s.trashMediaReporting(ctx, id, filter, now)
	return err
}

// trashMediaReporting is trashMediaAt reporting whether this call moved the
// Media to the trash, false when it was already there.
func (s *Store) trashMediaReporting(ctx context.Context, id MediaID, filter LibraryFilter, now time.Time) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	media, err := s.visibleMediaSQL(ctx, id, filter)
	if err != nil {
		return false, err
	}
	now = now.UTC()
	result, err := s.catalogW.ExecContext(ctx,
		`UPDATE media SET trashed_at_ms = ? WHERE id = ? AND trashed_at_ms IS NULL`,
		timeToMillis(now), id)
	if err != nil {
		return false, err
	}
	// Only a real soft-delete emits an event: re-trashing an already-trashed
	// item (or one another writer just trashed) affects no row.
	affected, raErr := result.RowsAffected()
	if raErr != nil || affected == 0 {
		return false, nil
	}
	// Emit AFTER the catalog write committed (cross-DB ordering: see
	// appendMediaEventLocked). The event carries the Media's LibraryID so the
	// delivery side reveal-filters by Library restriction (the now-trashed row
	// would resolve to not-found on a live read). See MediaEventDeleted.
	payload := mediaDeletedPayload(media.LibraryID)
	if eventErr := s.appendMediaEventLocked(ctx, MediaEventDeleted, id, payload, now); eventErr != nil {
		slog.Warn("media outbox event append failed", "kind", MediaEventDeleted, "media", id, "error", eventErr)
	}
	return true, nil
}

// TrashMediaRetiredByScan moves to the trash the Media of one Library that a
// scan's current rule no longer catalogues (CORE-2361). Each goes through the
// same soft-delete as TrashMedia, so the row and its source file survive the
// TrashRetention window and the Media can be restored, and each Media this
// call trashes gets a media.trash activity entry with no actor, because no
// human is behind it. Only ids that are live Media of class in libraryID are
// trashed; any other id is left alone. Like the other autonomous sweeps it
// refuses while trash is disabled (hard invariant 6): skipped is true with
// ErrTrashDisabledConfirmationRequired and nothing changes.
func (s *Store) TrashMediaRetiredByScan(ctx context.Context, libraryID LibraryID, class MediaClass, ids []MediaID) (trashed int, skipped bool, err error) {
	if libraryID == "" {
		return 0, false, errors.New("library ID is required")
	}
	if len(ids) == 0 {
		return 0, false, nil
	}
	if s.trashDisabled {
		s.trashDisabledSweepSkips.Add(1)
		return 0, true, ErrTrashDisabledConfirmationRequired
	}
	filter := LibraryFilter{AllowedIDs: []LibraryID{libraryID}, Revealed: true}
	media, err := s.MediaByIDs(ctx, ids, filter)
	if err != nil {
		return 0, false, err
	}
	for _, id := range ids {
		item, ok := media[id]
		if !ok || item.LibraryID != libraryID || item.Class != class {
			continue
		}
		now := time.Now().UTC()
		moved, err := s.trashMediaReporting(ctx, id, filter, now)
		if err != nil {
			return trashed, false, err
		}
		if !moved {
			continue
		}
		trashed++
		if err := s.AppendActivity(ctx, ActivityEntry{
			OccurredAt: now,
			Action:     ActivityMediaTrash,
			TargetType: "media",
			TargetID:   string(id),
			LibraryID:  libraryID,
		}); err != nil {
			return trashed, false, err
		}
	}
	return trashed, false, nil
}

// ErrRestoreMergedVersion reports that a trashed Media was folded into
// another Media as a Version by a rescan's version merge (CORE-2490): its file
// now lives on the survivor, so a plain restore would bring back an empty
// Media. Undoing the merge batch splits it back with its file.
var ErrRestoreMergedVersion = errors.New("this item was merged into another item as a Version; undo the merge to split it back")

// mergedAsVersion reports whether a version merge that has not been undone
// trashed this Media. Every path that clears a Media's trashed_at_ms, other
// than that merge's own undo, refuses such a Media.
func mergedAsVersion(ctx context.Context, q queryRower, id MediaID) (bool, error) {
	var merged bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM op_batch_item i JOIN op_batch b ON b.id = i.batch_id
		               WHERE i.media_id = ? AND b.kind = ? AND b.undone_at_ms = 0)
	`, id, opKindMergeVersion).Scan(&merged)
	return merged, err
}

// RestoreMedia returns a trashed Media item to its Library. The restore is
// reveal-gated the same way the trash listing is: the target Library must be
// visible to the caller. A Media a live version merge trashed is refused with
// ErrRestoreMergedVersion.
func (s *Store) RestoreMedia(ctx context.Context, id MediaID, filter LibraryFilter) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	entries, err := s.trashedMediaSQL(ctx, filter)
	if err != nil {
		return err
	}
	known := false
	for _, entry := range entries {
		if entry.ID == id {
			known = true
			break
		}
	}
	if !known {
		return errors.New("media not found")
	}
	// The merge check and the restore share one write transaction, so a
	// version merge cannot land between them.
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	merged, err := mergedAsVersion(ctx, tx, id)
	if err != nil {
		return err
	}
	if merged {
		return ErrRestoreMergedVersion
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx,
		`UPDATE media SET trashed_at_ms = NULL WHERE id = ? AND trashed_at_ms IS NOT NULL`, id)
	if err != nil {
		return err
	}
	restored, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if restored == 0 {
		// A concurrent restore already returned it and announced it.
		return nil
	}
	// The item is back in the catalog — emit media.created so a client that
	// dropped it on the matching media.deleted re-adds it (created/updated both
	// trigger a refetch; created matches "this item reappeared"). Emit AFTER the
	// catalog write committed (cross-DB ordering: see appendMediaEventLocked); a
	// failed outbox append must not fail the restore. The now-untrashed row
	// resolves on a live read, so this event needs no library payload — the
	// delivery side reveal-filters it through the standard Media read.
	if eventErr := s.appendMediaEventLocked(ctx, MediaEventCreated, id, nil, now); eventErr != nil {
		slog.Warn("media outbox event append failed", "kind", MediaEventCreated, "media", id, "error", eventErr)
	}
	return nil
}

// TrashedMediaList returns the trash contents visible to the caller's scope.
func (s *Store) TrashedMediaList(ctx context.Context, filter LibraryFilter) ([]TrashedMedia, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	return s.trashedMediaSQL(ctx, filter)
}

func (s *Store) trashedMediaSQL(ctx context.Context, filter LibraryFilter) ([]TrashedMedia, error) {
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	visible := map[LibraryID]bool{}
	for _, library := range libraries {
		if libraryVisible(library, filter) {
			visible[library.ID] = true
		}
	}
	// The live merge-version batch item, if any, explains the trash. A Media
	// a merge folded and the user undid is never folded again, so at most one
	// such batch is live; the newest wins defensively.
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT m.id, m.library_id, m.title, m.trashed_at_ms,
		  COALESCE(b.id, ''), COALESCE(json_extract(i.prior_state, '$.survivor'), ''),
		  COALESCE(sm.title, ''), COALESCE(b.created_at_ms, 0)
		FROM media m
		LEFT JOIN op_batch_item i ON i.rowid = (
		  SELECT i2.rowid FROM op_batch_item i2 JOIN op_batch b2 ON b2.id = i2.batch_id
		  WHERE i2.media_id = m.id AND b2.kind = ? AND b2.undone_at_ms = 0
		  ORDER BY b2.created_at_ms DESC, b2.id DESC LIMIT 1)
		LEFT JOIN op_batch b ON b.id = i.batch_id
		LEFT JOIN media sm ON sm.id = json_extract(i.prior_state, '$.survivor')
		WHERE m.trashed_at_ms IS NOT NULL
		ORDER BY m.trashed_at_ms DESC
	`, opKindMergeVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []TrashedMedia{}
	for rows.Next() {
		var entry TrashedMedia
		var trashedAt, mergedAt int64
		var merge TrashedMediaMerge
		if err := rows.Scan(&entry.ID, &entry.LibraryID, &entry.Title, &trashedAt,
			&merge.BatchID, &merge.Survivor, &merge.SurvivorTitle, &mergedAt); err != nil {
			return nil, err
		}
		if !visible[entry.LibraryID] {
			continue
		}
		entry.TrashedAt = millisToTime(trashedAt)
		if merge.BatchID != "" && merge.Survivor != "" {
			merge.MergedAt = millisToTime(mergedAt)
			entry.Merge = &merge
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.hideUnseenMergeSurvivors(ctx, entries, filter); err != nil {
		return nil, err
	}
	return entries, nil
}

// hideUnseenMergeSurvivors redacts the survivor of a merge whose survivor is
// above the caller's certification ceiling, so trash never names a Media the
// caller cannot see (CORE-2916). The entry keeps its merge batch: it is still
// a merge to undo, not a deletion to restore. A Version merge never runs in an
// encrypted Library, so the survivor is always in the shared catalog.
func (s *Store) hideUnseenMergeSurvivors(ctx context.Context, entries []TrashedMedia, filter LibraryFilter) error {
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	if certClause == "" {
		return nil
	}
	unique := map[MediaID]bool{}
	survivors := []any{}
	for _, entry := range entries {
		if entry.Merge != nil && !unique[entry.Merge.Survivor] {
			unique[entry.Merge.Survivor] = true
			survivors = append(survivors, entry.Merge.Survivor)
		}
	}
	seen := map[MediaID]bool{}
	for start := 0; start < len(survivors); start += survivorLookupChunk {
		chunk := survivors[start:min(start+survivorLookupChunk, len(survivors))]
		if err := s.visibleSurvivors(ctx, chunk, certClause, certArgs, seen); err != nil {
			return err
		}
	}
	for i := range entries {
		if merge := entries[i].Merge; merge != nil && !seen[merge.Survivor] {
			redacted := *merge
			redacted.Survivor, redacted.SurvivorTitle = "", ""
			entries[i].Merge = &redacted
		}
	}
	return nil
}

// survivorLookupChunk bounds the survivor ids bound in one query, well under
// SQLite's variable limit whatever the certification clause adds.
const survivorLookupChunk = 500

func (s *Store) visibleSurvivors(ctx context.Context, ids []any, certClause string, certArgs []any, seen map[MediaID]bool) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")
	args := append(append([]any{}, ids...), certArgs...)
	// #nosec G202 -- only generated placeholders and the closed certification clause are interpolated.
	rows, err := s.catalogR.QueryContext(ctx,
		`SELECT m.id FROM media m WHERE m.id IN (`+placeholders+`)`+certClause, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return err
		}
		seen[id] = true
	}
	return rows.Err()
}

// purgeableTrashedMediaSelect is the inner selection of Media eligible for a
// retention purge: trashed before the cutoff, outside a Custodial Library,
// with no verified Version merge still waiting for its engagement commit.
// A Custodial Library holds the canonical (only) copy of its bytes (ADR-0045
// §2.1), so purging its catalog row would be unrecoverable data loss — there
// is no operator-on-disk copy to re-import on the next scan. Its durability
// contract therefore keeps such Media in trash indefinitely (invariant #6:
// destruction stays recoverable). A Scanned Library is unaffected: its
// fingerprint re-imports from the operator's files post-purge as before.
const purgeableTrashedMediaSelect = `
	SELECT id FROM media
	WHERE trashed_at_ms IS NOT NULL AND trashed_at_ms < ?
	  AND library_id NOT IN (SELECT id FROM library WHERE custodial = 1)
	  AND NOT EXISTS (SELECT 1 FROM op_batch_item i
	    WHERE i.media_id = media.id AND json_extract(i.prior_state, '$.engagementLibraryId') IS NOT NULL)
`

// PurgeTrashedBefore removes catalog rows for media trashed before the cutoff.
// Source files on disk are never touched; their fingerprints would re-import
// on the next scan, which is the correct post-purge behavior — the curated
// record is what the trash protected, and it is now intentionally gone. Media
// in a Custodial Library is exempt (purgeableTrashedMediaSelect): the server
// holds its only copy, so its trash is never auto-purged.
func (s *Store) PurgeTrashedBefore(ctx context.Context, cutoff time.Time) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	affected, err := purgeMediaRowsTx(ctx, tx, purgeableTrashedMediaSelect, timeToMillis(cutoff))
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if _, err := s.pruneOrphanedEngagementRowsLocked(ctx); err != nil {
		return int(affected), err
	}
	if affected > 0 {
		if err := appendActivitySQL(ctx, s.eventsW, ActivityEntry{
			OccurredAt: time.Now().UTC(),
			Action:     ActivityTrashPurge,
			TargetType: "trash",
			TargetID:   strconv.FormatInt(affected, 10),
		}); err != nil {
			return int(affected), err
		}
	}
	return int(affected), nil
}

// purgeMediaRowsTx removes the catalog rows of every Media named by selection,
// a SELECT returning Media ids, and reports how many Media rows went. Rows that
// hang off media by a cascading foreign key go with the media row; the tables
// deleted explicitly here either have no foreign key or must go first.
func purgeMediaRowsTx(ctx context.Context, tx *sql.Tx, selection string, args ...any) (int64, error) {
	// media_embedding is keyed by content fingerprint, not media_id, and that
	// fingerprint deterministically regenerates the same Media id on re-import.
	// Delete it here — before media_file is dropped, while the fingerprint is
	// still resolvable — so a purge does not leave a semantic-search vector of
	// the deleted (possibly restricted) content that resurrects on rescan.
	//nolint:gosec // G202: selection is a fixed internal SQL fragment; every value is bound as an argument.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM media_embedding WHERE media_fingerprint IN (
			SELECT content_fingerprint FROM media_file WHERE media_id IN (`+selection+`)
		)
	`, args...); err != nil {
		return 0, err
	}
	// artwork_candidate and media_field_provenance carry no foreign key, and a
	// re-import regenerates the same Media id, so a row left behind would attach
	// itself to the re-imported Media.
	for _, table := range []string{"media_file", "media_external_id", "artwork_candidate", "media_field_provenance"} {
		//nolint:gosec // G202: table comes from a fixed list and selection is an internal SQL fragment.
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE media_id IN (`+selection+`)`, args...); err != nil {
			return 0, err
		}
	}
	//nolint:gosec // G202: selection is a fixed internal SQL fragment; every value is bound as an argument.
	result, err := tx.ExecContext(ctx, `DELETE FROM media WHERE id IN (`+selection+`)`, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// SweepPurgeTrashedBefore runs the autonomous retained-trash purge. Human
// confirmed purge calls PurgeTrashedBefore directly and remains legal while
// trash is disabled.
func (s *Store) SweepPurgeTrashedBefore(ctx context.Context, cutoff time.Time) (purged int, skipped bool, err error) {
	if s.trashDisabled {
		s.trashDisabledSweepSkips.Add(1)
		return 0, true, ErrTrashDisabledConfirmationRequired
	}
	purged, err = s.PurgeTrashedBefore(ctx, cutoff)
	return purged, false, err
}
