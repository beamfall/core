package store

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"math"
	"time"
)

// TrashedLibrary is one soft-deleted Library: still whole in the catalog,
// hidden from every read, and waiting to be restored or purged.
type TrashedLibrary struct {
	Library   Library
	TrashedAt time.Time
	// MediaCount is the number of Media a restore would bring back; Media
	// trashed on their own before the Library was deleted are not counted.
	MediaCount int
}

// Purgeable reports whether the Library may ever be purged. A Custodial Library
// holds the only copy of its bytes (ADR-0045 §2.1), so it stays restorable
// until an owner restores it, exactly as its trashed Media does. An encrypted
// Library is purgeable: its purge removes its private database and keys.
func (t TrashedLibrary) Purgeable() bool {
	return !t.Library.Custodial
}

// PurgeAfter is when the retention sweep removes the Library. ok is false when
// the Library is not Purgeable.
func (t TrashedLibrary) PurgeAfter() (at time.Time, ok bool) {
	if !t.Purgeable() {
		return time.Time{}, false
	}
	return t.TrashedAt.Add(TrashRetention), true
}

// LibraryPurge reports what a Library purge removed. The ids let the caller
// remove server-generated artwork, which lives outside the catalog.
type LibraryPurge struct {
	LibraryID LibraryID
	Title     string
	// Restricted records whether the purged Library was restricted, so the
	// activity record of its purge can stay behind the reveal gate.
	Restricted bool
	MediaIDs   []MediaID
	EntityIDs  []EntityID
}

// libraryPurgedConfigPrefix keys a tombstone server_config row per purged
// Library id. Activity, notifications and history keep naming a purged
// restricted Library's id so they stay hidden; CreateLibraryWithRoots never
// hands that id out again, or a new Library would reveal them to its scope.
const libraryPurgedConfigPrefix = "library.purged."

// ActivityLibraryID is the Library id a library.purge activity record carries.
// The activity log hides a record naming a Library that no longer exists, which
// is every record naming a purged Library, so an unrestricted purge is recorded
// against no Library and stays readable. A restricted Library's purge keeps its
// id and therefore stays hidden: the log never names a restricted Library to a
// scope that has not revealed it.
func (p LibraryPurge) ActivityLibraryID() LibraryID {
	if p.Restricted {
		return p.LibraryID
	}
	return ""
}

// TrashedLibraries returns the soft-deleted Libraries visible under the filter,
// most recently deleted first. Visibility follows the live-Library rules, so a
// restricted Library is listed only for a revealed scope.
func (s *Store) TrashedLibraries(ctx context.Context, filter LibraryFilter) ([]TrashedLibrary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT `+libraryColumns+`,
			trashed_at_ms,
			(SELECT count(*) FROM media WHERE media.library_id = library.id AND media.trashed_at_ms IS NULL)
		FROM library WHERE trashed_at_ms IS NOT NULL
		ORDER BY trashed_at_ms DESC, rowid DESC`, libraryIconConfigPrefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	trashed := []TrashedLibrary{}
	for rows.Next() {
		var entry TrashedLibrary
		var trashedAt int64
		if entry.Library, err = scanLibrary(rows, &trashedAt, &entry.MediaCount); err != nil {
			return nil, err
		}
		if !libraryVisible(entry.Library, filter) {
			continue
		}
		entry.TrashedAt = millisToTime(trashedAt)
		trashed = append(trashed, entry)
	}
	return trashed, rows.Err()
}

// RestoreLibrary brings a soft-deleted Library back. DeleteLibrary removed
// nothing, so its Media, engagement history, profile grants, root folders and
// scan schedule are all visible again the moment this returns. The caller owns
// resuming the folder watcher and scan schedule and queueing a rescan.
func (s *Store) RestoreLibrary(ctx context.Context, id LibraryID) error {
	if id == "" {
		return ValidationError("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	res, err := s.catalogW.ExecContext(ctx,
		`UPDATE library SET trashed_at_ms = NULL WHERE id = ? AND trashed_at_ms IS NOT NULL`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ValidationError("library not found or not deleted")
	}
	return nil
}

// PurgeLibrary permanently removes a soft-deleted Library: its Media, entities,
// profile grants, root-folder records, scan schedule and every row that hangs
// off them, then the engagement history of the removed Media. It refuses a
// Library that is not already deleted, so one call can never take a live
// Library to an unrecoverable state, and it refuses a Custodial Library.
//
// It only ever deletes database rows. The files under the Library's root
// folders are never opened, moved or removed; a new Library pointed at the same
// folders re-imports them. An encrypted Library's catalog lives in its own
// private database, which is server state rather than media: purging the
// Library removes that database and destroys its key material (see
// completeLibraryPurge for the ordering).
//
// PurgeLibrary writes no activity record: the caller is a human-confirmed
// mutation that records the actor. The autonomous path is
// SweepPurgeTrashedLibrariesBefore, which records its own.
func (s *Store) PurgeLibrary(ctx context.Context, id LibraryID) (LibraryPurge, error) {
	if id == "" {
		return LibraryPurge{}, ValidationError("library ID is required")
	}
	return s.purgeLibrary(ctx, id, math.MaxInt64)
}

// purgeLibrary commits the catalog purge under the read lock, then tears down
// an encrypted Library's private database and keys with that lock released.
func (s *Store) purgeLibrary(ctx context.Context, id LibraryID, trashedBeforeMs int64) (LibraryPurge, error) {
	s.mu.RLock()
	if err := s.guardLocked(); err != nil {
		s.mu.RUnlock()
		return LibraryPurge{}, err
	}
	purge, encrypted, err := s.purgeLibraryLocked(ctx, id, trashedBeforeMs, s.encKeyEraser != nil)
	s.mu.RUnlock()
	if err != nil {
		return LibraryPurge{}, err
	}
	if encrypted {
		if err := s.completeLibraryPurge(ctx, id); err != nil {
			// The purge has happened; its pending row keeps the teardown owed
			// until a retention sweep or the next start finishes it.
			slog.WarnContext(ctx, "library purge: encrypted library teardown incomplete, will retry", "library", id, "error", err)
		}
	}
	return purge, nil
}

// purgeLibraryLocked removes a Library that was deleted before trashedBeforeMs
// (math.MaxInt64 for no bound). The bound is checked inside the transaction so
// the retention sweep cannot purge a Library an admin restored and deleted again
// after the sweep chose its candidates. encrypted reports that the Library kept
// a private database, whose teardown the caller owes once the lock is released.
func (s *Store) purgeLibraryLocked(ctx context.Context, id LibraryID, trashedBeforeMs int64, canEraseKeys bool) (purge LibraryPurge, encrypted bool, err error) {
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return LibraryPurge{}, false, err
	}
	defer tx.Rollback()
	purge = LibraryPurge{LibraryID: id}
	var custodial int
	err = tx.QueryRowContext(ctx,
		`SELECT title, is_restricted, custodial, encrypted FROM library
		 WHERE id = ? AND trashed_at_ms IS NOT NULL AND trashed_at_ms < ?`, id, trashedBeforeMs).
		Scan(&purge.Title, &purge.Restricted, &custodial, &encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return LibraryPurge{}, false, ValidationError("library not found or not deleted")
	}
	if err != nil {
		return LibraryPurge{}, false, err
	}
	if custodial != 0 {
		return LibraryPurge{}, false, ValidationError("this library stores the only copy of its media and cannot be removed permanently")
	}
	catalog := rowQuerier(tx)
	if encrypted {
		if err := s.checkEncryptedLibraryPurgeableTx(ctx, tx, id, canEraseKeys); err != nil {
			return LibraryPurge{}, false, err
		}
		// The Library's Media and entities live in its private database, so
		// their ids can be listed for the caller only while it is unsealed.
		// Whatever a sealed Library's ids key stays encrypted, and erasing the
		// keys makes it unreadable.
		catalog = nil
		if db := s.encryptedDBs[id]; db != nil && db.state == "unsealed-registered" && db.reader != nil {
			catalog = db.reader
		}
	}
	if catalog != nil {
		if purge.MediaIDs, err = queryIDs[MediaID](ctx, catalog, `SELECT id FROM media WHERE library_id = ? ORDER BY id`, id); err != nil {
			return LibraryPurge{}, false, err
		}
		if purge.EntityIDs, err = queryIDs[EntityID](ctx, catalog, `SELECT id FROM entity WHERE library_id = ? ORDER BY id`, id); err != nil {
			return LibraryPurge{}, false, err
		}
	}
	var packCiphertexts []string
	if encrypted {
		if packCiphertexts, err = s.purgeEncryptedLibraryPackItemsTx(ctx, tx, id, purge.MediaIDs, catalog != nil); err != nil {
			return LibraryPurge{}, false, err
		}
	}
	// media and entity reference library without a cascade, so they go first.
	// Deleting an entity cascades its fields and relations (see PurgePluginData).
	if _, err := purgeMediaRowsTx(ctx, tx, `SELECT id FROM media WHERE library_id = ?`, id); err != nil {
		return LibraryPurge{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM entity WHERE library_id = ?`, id); err != nil {
		return LibraryPurge{}, false, err
	}
	// Neither the scan schedule nor the icon choice is keyed by a foreign key.
	if _, err := tx.ExecContext(ctx, `DELETE FROM schedules WHERE id = ?`, scanScheduleID(id)); err != nil {
		return LibraryPurge{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM server_config WHERE key = ?`, libraryIconConfigPrefix+string(id)); err != nil {
		return LibraryPurge{}, false, err
	}
	// Grants, root folders and every other per-Library table cascade from here.
	if _, err := tx.ExecContext(ctx, `DELETE FROM library WHERE id = ?`, id); err != nil {
		return LibraryPurge{}, false, err
	}
	nowMs := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO server_config(key, value, updated_at_ms) VALUES (?, '1', ?)`,
		libraryPurgedConfigPrefix+string(id), nowMs); err != nil {
		return LibraryPurge{}, false, err
	}
	if encrypted {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO server_config(key, value, updated_at_ms) VALUES (?, '1', ?)`,
			libraryPurgePendingConfigPrefix+string(id), nowMs); err != nil {
			return LibraryPurge{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return LibraryPurge{}, false, err
	}
	// The catalog is the authority and is already committed, so the purge has
	// happened: callers must still record it and drop its artwork. The
	// events-side cleanup below is idempotent and the next retention sweep
	// repeats it, so a failure here is logged rather than returned.
	if _, err := s.eventsW.ExecContext(ctx, `DELETE FROM schedule_occurrence WHERE schedule_id = ?`, scanScheduleID(id)); err != nil {
		slog.WarnContext(ctx, "library purge: schedule occurrence cleanup failed", "library", id, "error", err)
	}
	if _, err := s.pruneOrphanedEngagementRowsLocked(ctx); err != nil {
		slog.WarnContext(ctx, "library purge: engagement cleanup failed", "library", id, "error", err)
	}
	s.removeOfflinePackCiphertexts(ctx, packCiphertexts)
	return purge, encrypted, nil
}

// SweepPurgeTrashedLibrariesBefore is the autonomous retention purge for
// Libraries: it purges every Library soft-deleted before the cutoff, under the
// rules SweepPurgeTrashedBefore applies to Media. It yields while trash is
// disabled, never touches a Custodial Library, and records a library.purge
// activity entry per Library because no human actor is behind it. It first
// finishes any encrypted Library purge whose teardown is still owed, which is
// not a new removal and so runs even while trash is disabled.
func (s *Store) SweepPurgeTrashedLibrariesBefore(ctx context.Context, cutoff time.Time) (purged []LibraryPurge, skipped bool, err error) {
	if err := s.CompletePendingLibraryPurges(ctx); err != nil {
		slog.WarnContext(ctx, "library retention sweep: encrypted library teardown incomplete, will retry", "error", err)
	}
	if s.trashDisabled {
		s.trashDisabledSweepSkips.Add(1)
		return nil, true, ErrTrashDisabledConfirmationRequired
	}
	s.mu.RLock()
	err = s.guardLocked()
	var ids []LibraryID
	if err == nil {
		ids, err = queryIDs[LibraryID](ctx, s.catalogR, `
			SELECT id FROM library
			WHERE trashed_at_ms IS NOT NULL AND trashed_at_ms < ? AND custodial = 0
			ORDER BY trashed_at_ms, rowid`, timeToMillis(cutoff))
	}
	s.mu.RUnlock()
	if err != nil {
		return nil, false, err
	}
	for _, id := range ids {
		purge, err := s.purgeLibrary(ctx, id, timeToMillis(cutoff))
		var refused ValidationError
		if errors.As(err, &refused) {
			// Restored (or purged by hand) since the selection above, or an
			// encrypted Library this store cannot erase the keys of yet.
			continue
		}
		if err != nil {
			return purged, false, err
		}
		purged = append(purged, purge)
		if err := s.appendLibraryPurgeActivity(ctx, purge); err != nil {
			return purged, false, err
		}
	}
	return purged, false, nil
}

func (s *Store) appendLibraryPurgeActivity(ctx context.Context, purge LibraryPurge) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	return appendActivitySQL(ctx, s.eventsW, ActivityEntry{
		OccurredAt: time.Now().UTC(),
		Action:     ActivityLibraryPurge,
		TargetType: "library",
		TargetID:   string(purge.LibraryID),
		LibraryID:  purge.ActivityLibraryID(),
	})
}

type rowQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// queryIDs runs a single-column id query.
func queryIDs[T ~string](ctx context.Context, db rowQuerier, query string, args ...any) ([]T, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []T
	for rows.Next() {
		var id T
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
