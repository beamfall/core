package store

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"
)

// TrashedMedia is one entry in the server-side trash.
type TrashedMedia struct {
	ID        MediaID
	LibraryID LibraryID
	Title     string
	TrashedAt time.Time
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	media, err := s.visibleMediaSQL(ctx, id, filter)
	if err != nil {
		return err
	}
	now = now.UTC()
	result, err := s.catalogW.ExecContext(ctx,
		`UPDATE media SET trashed_at_ms = ? WHERE id = ? AND trashed_at_ms IS NULL`,
		timeToMillis(now), id)
	if err != nil {
		return err
	}
	// Only a real soft-delete emits an event: re-trashing an already-trashed
	// item (or one another writer just trashed) affects no row.
	if affected, raErr := result.RowsAffected(); raErr == nil && affected > 0 {
		// Emit AFTER the catalog write committed (cross-DB ordering: see
		// appendMediaEventLocked). The event carries the Media's LibraryID so the
		// delivery side reveal-filters by Library restriction (the now-trashed row
		// would resolve to not-found on a live read). See MediaEventDeleted.
		payload := mediaDeletedPayload(media.LibraryID)
		if eventErr := s.appendMediaEventLocked(ctx, MediaEventDeleted, id, payload, now); eventErr != nil {
			slog.Warn("media outbox event append failed", "kind", MediaEventDeleted, "media", id, "error", eventErr)
		}
	}
	return nil
}

// RestoreMedia returns a trashed Media item to its Library. The restore is
// reveal-gated the same way the trash listing is: the target Library must be
// visible to the caller.
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
	now := time.Now().UTC()
	if _, err = s.catalogW.ExecContext(ctx,
		`UPDATE media SET trashed_at_ms = NULL WHERE id = ?`, id); err != nil {
		return err
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
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT id, library_id, title, trashed_at_ms
		FROM media
		WHERE trashed_at_ms IS NOT NULL
		ORDER BY trashed_at_ms DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []TrashedMedia{}
	for rows.Next() {
		var entry TrashedMedia
		var trashedAt int64
		if err := rows.Scan(&entry.ID, &entry.LibraryID, &entry.Title, &trashedAt); err != nil {
			return nil, err
		}
		if !visible[entry.LibraryID] {
			continue
		}
		entry.TrashedAt = millisToTime(trashedAt)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// purgeableTrashedMediaSelect is the inner selection of Media eligible for a
// retention purge: trashed before the cutoff AND NOT in a Custodial Library.
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
	// media_embedding is keyed by content fingerprint, not media_id, and that
	// fingerprint deterministically regenerates the same Media id on re-import.
	// Delete it here — before media_file is dropped, while the fingerprint is
	// still resolvable — so a purge does not leave a semantic-search vector of
	// the deleted (possibly restricted) content that resurrects on rescan.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM media_embedding WHERE media_fingerprint IN (
			SELECT content_fingerprint FROM media_file WHERE media_id IN (`+purgeableTrashedMediaSelect+`)
		)
	`, timeToMillis(cutoff)); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM media_file WHERE media_id IN (`+purgeableTrashedMediaSelect+`)
	`, timeToMillis(cutoff)); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM media_external_id WHERE media_id IN (`+purgeableTrashedMediaSelect+`)
	`, timeToMillis(cutoff)); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM artwork_candidate WHERE media_id IN (`+purgeableTrashedMediaSelect+`)
	`, timeToMillis(cutoff)); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM media WHERE id IN (`+purgeableTrashedMediaSelect+`)
	`, timeToMillis(cutoff))
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
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
