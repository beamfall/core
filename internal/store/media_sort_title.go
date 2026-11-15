package store

import (
	"context"
	"database/sql"
	"errors"
)

// A sort title lives in media_sort_title, one row per catalog-plane Media item
// that has one. An admin sets it through the per-item editor (CORE-1715) and a
// metadata provider can supply it on identification (CORE-1768); both write
// through UpdateMediaMetadata (field "sortTitle", source-precedence gated like
// the other built-ins), so an admin's value is locked against a refresh. It is
// read by the per-item editor and by Library browse, which sorts and
// letter-indexes by it in place of the title. Core holds no language rule for
// deriving one (no article list): the key comes from a plugin or an admin.

// updateSortTitleTx applies one source-precedence gated sort-title write. A
// value lands like any built-in field: only when the write's source rank is at
// least the field's recorded rank. An empty value from a manual write clears
// the sort title. An empty value from any other source is a provider saying
// it has no sort title: it retracts only a sort title that same source tier
// supplied, so a re-identification against a candidate without one drops the
// previous provider's key, while a sort title an admin entered (locked, or
// unlocked and so carrying no provenance) is left alone.
func updateSortTitleTx(ctx context.Context, tx *sql.Tx, id MediaID, sortTitle string, source MutationSource) error {
	if sortTitle == "" && source != MutationSourceManual {
		stored, err := fieldProvenanceTx(ctx, tx, id, "sortTitle")
		if err != nil || stored != source {
			return err
		}
		if err := writeSortTitleTx(ctx, tx, id, ""); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			"DELETE FROM media_field_provenance WHERE media_id = ? AND field = 'sortTitle'", id)
		return err
	}
	allowed, err := canWriteFieldTx(ctx, tx, id, "sortTitle", source)
	if err != nil || !allowed {
		return err
	}
	if err := writeSortTitleTx(ctx, tx, id, sortTitle); err != nil {
		return err
	}
	return recordFieldProvenanceTx(ctx, tx, id, "sortTitle", source)
}

// writeSortTitleTx stores or, for "", clears one item's sort title.
func writeSortTitleTx(ctx context.Context, tx *sql.Tx, id MediaID, sortTitle string) error {
	if sortTitle == "" {
		_, err := tx.ExecContext(ctx, "DELETE FROM media_sort_title WHERE media_id = ?", id)
		return err
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO media_sort_title(media_id, sort_title) VALUES (?, ?)
		ON CONFLICT(media_id) DO UPDATE SET sort_title = excluded.sort_title`, id, sortTitle)
	return err
}

// MediaSortTitle returns a visible item's sort title, or "" when it
// has none. A hidden or unknown Media answers "media not found".
func (s *Store) MediaSortTitle(ctx context.Context, id MediaID, filter LibraryFilter) (string, error) {
	if id == "" {
		return "", ValidationError("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", err
	}
	if _, err := s.visibleMediaSQL(ctx, id, filter); err != nil {
		return "", err
	}
	var sortTitle string
	err := s.catalogR.QueryRowContext(ctx,
		"SELECT sort_title FROM media_sort_title WHERE media_id = ?", id).Scan(&sortTitle)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return sortTitle, err
}

// librarySortTitles adds the sort titles set on one catalog-plane Library's
// items to out, keyed by Media ID.
func librarySortTitles(ctx context.Context, reader *sql.DB, libraryID LibraryID, out map[MediaID]string) error {
	rows, err := reader.QueryContext(ctx, `
		SELECT s.media_id, s.sort_title FROM media_sort_title s
		JOIN media m ON m.id = s.media_id
		WHERE m.library_id = ?`, libraryID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id MediaID
		var sortTitle string
		if err := rows.Scan(&id, &sortTitle); err != nil {
			return err
		}
		out[id] = sortTitle
	}
	return rows.Err()
}
