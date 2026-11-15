package store

import (
	"context"
	"database/sql"
	"errors"
)

// An admin-set sort title (CORE-1715) lives in media_sort_title, one row per
// catalog-plane Media item that has one. It is written through
// UpdateMediaMetadata (field "sortTitle", source-precedence gated like the
// other built-ins) and read by the per-item editor and by Library browse,
// which sorts and letter-indexes by it in place of the title.

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

// MediaSortTitle returns a visible item's admin-set sort title, or "" when it
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
