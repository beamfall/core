package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var ErrCollectionNotFound = errors.New("collection not found")

type CollectionID string

type Collection struct {
	ID        CollectionID
	LibraryID LibraryID
	ParentID  CollectionID
	Name      string
	SortOrder int
	TrashedAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
	// MemberCount is the raw storage count of collection_media rows, unfiltered
	// by Reveal/Sealed visibility. For the client-visible count that excludes
	// inaccessible/Sealed members (ADR-0098 area 4), use the length of
	// ResolveCollectionMembers with the caller's LibraryFilter.
	MemberCount int
}

type CollectionMember struct {
	CollectionID CollectionID
	MediaID      MediaID
	SortOrder    int
	AddedAt      time.Time
}

type CollectionPatch struct {
	Name         *string
	ParentID     *CollectionID
	MoveToRoot   bool
	SortOrder    *int
	IncludeTrash bool
}

func (s *Store) Collections(ctx context.Context, libraryID LibraryID, includeTrashed bool) ([]Collection, error) {
	if libraryID == "" {
		return nil, ValidationError("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	where := "WHERE c.library_id = ?"
	if !includeTrashed {
		where += " AND c.trashed_at_ms IS NULL"
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT c.id, c.library_id, c.parent_id, c.name, c.sort_order, c.trashed_at_ms,
		       c.created_at_ms, c.updated_at_ms, COUNT(cm.media_id)
		FROM collection c
		LEFT JOIN collection_media cm ON cm.collection_id = c.id
		`+where+`
		GROUP BY c.id
		ORDER BY COALESCE(c.parent_id, ''), c.sort_order, c.name COLLATE NOCASE, c.id
	`, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Collection
	for rows.Next() {
		c, err := scanCollection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if out == nil {
		out = []Collection{}
	}
	return out, rows.Err()
}

func (s *Store) CollectionByID(ctx context.Context, id CollectionID, includeTrash bool) (Collection, error) {
	if id == "" {
		return Collection{}, ValidationError("id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Collection{}, err
	}
	return s.collectionByIDLocked(ctx, id, includeTrash)
}

func (s *Store) CreateCollection(ctx context.Context, c Collection, now time.Time) (Collection, error) {
	c.Name = strings.TrimSpace(c.Name)
	if c.LibraryID == "" {
		return Collection{}, ValidationError("library ID is required")
	}
	if c.Name == "" {
		return Collection{}, ValidationError("name is required")
	}
	id, err := randomCollectionID()
	if err != nil {
		return Collection{}, err
	}
	c.ID = id
	c.CreatedAt = now.UTC()
	c.UpdatedAt = c.CreatedAt
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Collection{}, err
	}
	if c.ParentID != "" {
		parent, err := s.collectionByIDLocked(ctx, c.ParentID, false)
		if err != nil {
			return Collection{}, ErrCollectionNotFound
		}
		if parent.LibraryID != c.LibraryID {
			return Collection{}, ValidationError("parent collection must be in the same library")
		}
	}
	if c.SortOrder == 0 {
		order, err := s.nextCollectionSortOrderLocked(ctx, c.LibraryID, c.ParentID)
		if err != nil {
			return Collection{}, err
		}
		c.SortOrder = order
	}
	_, err = s.catalogW.ExecContext(ctx, `
		INSERT INTO collection (id, library_id, parent_id, name, sort_order, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, c.ID, c.LibraryID, nullString(string(c.ParentID)), c.Name, c.SortOrder, c.CreatedAt.UnixMilli(), c.UpdatedAt.UnixMilli())
	if err != nil {
		return Collection{}, err
	}
	return s.collectionByIDLocked(ctx, c.ID, true)
}

// ImportCollectionSnapshot creates an imported static Collection and fills its
// ordered membership gaps in one transaction. The caller supplies a stable ID
// derived from the source collection identity. Replays preserve existing
// collection fields and memberships, adding only missing source members per
// ADR-0037/ADR-0178's additive-only import rule.
func (s *Store) ImportCollectionSnapshot(ctx context.Context, c Collection, members []MediaID, now time.Time) (Collection, error) {
	c.Name = strings.TrimSpace(c.Name)
	if c.ID == "" {
		return Collection{}, ValidationError("id is required")
	}
	if c.LibraryID == "" {
		return Collection{}, ValidationError("library ID is required")
	}
	if c.Name == "" {
		return Collection{}, ValidationError("name is required")
	}
	seen := make(map[MediaID]bool, len(members))
	for _, id := range members {
		if id == "" {
			return Collection{}, ValidationError("media ID is required")
		}
		if seen[id] {
			return Collection{}, ValidationError("duplicate collection media is not supported")
		}
		seen[id] = true
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Collection{}, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return Collection{}, err
	}
	defer tx.Rollback()

	existingMembers := make(map[MediaID]bool, len(members))
	for start := 0; start < len(members); start += bulkWriteChunkSize {
		chunk := members[start:min(start+bulkWriteChunkSize, len(members))]
		placeholders := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)+1)
		args = append(args, c.ID)
		for i, id := range chunk {
			placeholders[i] = "?"
			args = append(args, id)
		}
		//nolint:gosec // G202: placeholders is generated only from the bounded member-ID batch length; all IDs are bound via args.
		rows, err := tx.QueryContext(ctx, `
			SELECT m.id, m.library_id, EXISTS (
				SELECT 1 FROM collection_media cm
				WHERE cm.collection_id = ? AND cm.media_id = m.id
			)
			FROM media m
			WHERE m.id IN (`+strings.Join(placeholders, ", ")+`)
		`, args...)
		if err != nil {
			return Collection{}, err
		}
		found := 0
		for rows.Next() {
			var id MediaID
			var libraryID LibraryID
			var membershipExists int
			if err := rows.Scan(&id, &libraryID, &membershipExists); err != nil {
				_ = rows.Close()
				return Collection{}, err
			}
			if libraryID != c.LibraryID {
				_ = rows.Close()
				return Collection{}, ValidationError("collection media must be in the same library")
			}
			existingMembers[id] = membershipExists != 0
			found++
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return Collection{}, err
		}
		if err := rows.Close(); err != nil {
			return Collection{}, err
		}
		if found != len(chunk) {
			return Collection{}, ValidationError("media not found")
		}
	}

	var existingLibrary LibraryID
	err = tx.QueryRowContext(ctx, `SELECT library_id FROM collection WHERE id = ?`, c.ID).Scan(&existingLibrary)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(sort_order), 0) + 1
			FROM collection
			WHERE library_id = ? AND parent_id IS NULL
		`, c.LibraryID).Scan(&c.SortOrder); err != nil {
			return Collection{}, err
		}
		c.CreatedAt = now.UTC()
		c.UpdatedAt = c.CreatedAt
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO collection (id, library_id, parent_id, name, sort_order, created_at_ms, updated_at_ms)
			VALUES (?, ?, NULL, ?, ?, ?, ?)
		`, c.ID, c.LibraryID, c.Name, c.SortOrder, c.CreatedAt.UnixMilli(), c.UpdatedAt.UnixMilli()); err != nil {
			return Collection{}, err
		}
	case err != nil:
		return Collection{}, err
	case existingLibrary != c.LibraryID:
		return Collection{}, ValidationError("collection belongs to a different library")
	}

	created := errors.Is(err, sql.ErrNoRows)
	membershipChanged := false
	nowMS := now.UTC().UnixMilli()
	var nextMemberSortOrder int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(sort_order), -1) + 1
		FROM collection_media
		WHERE collection_id = ?
	`, c.ID).Scan(&nextMemberSortOrder); err != nil {
		return Collection{}, err
	}
	for start := 0; start < len(members); start += bulkWriteChunkSize {
		chunk := members[start:min(start+bulkWriteChunkSize, len(members))]
		placeholders := make([]string, 0, len(chunk))
		args := make([]any, 0, len(chunk)*4)
		for _, id := range chunk {
			if existingMembers[id] {
				continue
			}
			placeholders = append(placeholders, "(?, ?, ?, ?)")
			args = append(args, c.ID, id, nextMemberSortOrder, nowMS)
			nextMemberSortOrder++
		}
		if len(placeholders) == 0 {
			continue
		}
		//nolint:gosec // G202: the concatenation is only joined literal "(?, ?, ?, ?)" placeholder groups; all values are bound via args.
		result, err := tx.ExecContext(ctx, `
			INSERT INTO collection_media (collection_id, media_id, sort_order, added_at_ms)
			VALUES `+strings.Join(placeholders, ", ")+`
			ON CONFLICT (collection_id, media_id) DO NOTHING`,
			args...)
		if err != nil {
			return Collection{}, err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return Collection{}, err
		}
		membershipChanged = membershipChanged || inserted > 0
	}
	if !created && membershipChanged {
		if _, err := tx.ExecContext(ctx, `UPDATE collection SET updated_at_ms = ? WHERE id = ?`, nowMS, c.ID); err != nil {
			return Collection{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Collection{}, err
	}
	return s.collectionByIDLocked(ctx, c.ID, true)
}

func (s *Store) UpdateCollection(ctx context.Context, id CollectionID, patch CollectionPatch, now time.Time) (Collection, error) {
	if id == "" {
		return Collection{}, ValidationError("id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Collection{}, err
	}
	current, err := s.collectionByIDLocked(ctx, id, patch.IncludeTrash)
	if err != nil {
		return Collection{}, err
	}
	name := current.Name
	if patch.Name != nil {
		name = strings.TrimSpace(*patch.Name)
		if name == "" {
			return Collection{}, ValidationError("name is required")
		}
	}
	parent := current.ParentID
	if patch.MoveToRoot {
		parent = ""
	}
	if patch.ParentID != nil {
		parent = *patch.ParentID
	}
	if parent == id {
		return Collection{}, ValidationError("collection cannot parent itself")
	}
	if parent != "" {
		parentRow, err := s.collectionByIDLocked(ctx, parent, false)
		if err != nil {
			return Collection{}, ErrCollectionNotFound
		}
		if parentRow.LibraryID != current.LibraryID {
			return Collection{}, ValidationError("parent collection must be in the same library")
		}
		descendant, err := s.collectionDescendantLocked(ctx, parent, id)
		if err != nil {
			return Collection{}, err
		}
		if descendant {
			return Collection{}, ValidationError("collection cannot parent itself through a descendant")
		}
	}
	sortOrder := current.SortOrder
	if patch.SortOrder != nil {
		sortOrder = *patch.SortOrder
	}
	updated := now.UTC()
	_, err = s.catalogW.ExecContext(ctx, `
		UPDATE collection
		SET name = ?, parent_id = ?, sort_order = ?, updated_at_ms = ?
		WHERE id = ?
	`, name, nullString(string(parent)), sortOrder, updated.UnixMilli(), id)
	if err != nil {
		return Collection{}, err
	}
	return s.collectionByIDLocked(ctx, id, true)
}

func (s *Store) TrashCollection(ctx context.Context, id CollectionID, now time.Time) (Collection, error) {
	return s.setCollectionTrash(ctx, id, true, now)
}

func (s *Store) RestoreCollection(ctx context.Context, id CollectionID, now time.Time) (Collection, error) {
	return s.setCollectionTrash(ctx, id, false, now)
}

func (s *Store) setCollectionTrash(ctx context.Context, id CollectionID, trashed bool, now time.Time) (Collection, error) {
	if id == "" {
		return Collection{}, ValidationError("id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Collection{}, err
	}
	if _, err := s.collectionByIDLocked(ctx, id, true); err != nil {
		return Collection{}, err
	}
	var trashedAt any
	updated := now.UTC()
	if trashed {
		trashedAt = updated.UnixMilli()
	}
	_, err := s.catalogW.ExecContext(ctx, `
		UPDATE collection SET trashed_at_ms = ?, updated_at_ms = ? WHERE id = ?
	`, trashedAt, updated.UnixMilli(), id)
	if err != nil {
		return Collection{}, err
	}
	return s.collectionByIDLocked(ctx, id, true)
}

// ResolveCollectionMembers returns the ordered static membership of a Collection
// with Reveal/Sealed visibility applied (ADR-0098 area 4): any member whose Media
// is inaccessible or in a Sealed/restricted-unrevealed Library under filter is
// dropped, never gapped — the surviving members keep their relative order. This
// is the canonical ordered-membership interface that MUSIC-5 (Collection Station
// seeds) and MUSIC-6 (Playlist/Collection Offline-Pack targets) consume to turn a
// Collection into a visibility-safe ordered Media list. Inaccessible/Sealed
// members must not leak through counts, gaps, ordering, or downstream
// explanations, so callers building seeds/targets/counts must source membership
// here rather than from the raw collection_media rows.
func (s *Store) ResolveCollectionMembers(ctx context.Context, collectionID CollectionID, filter LibraryFilter) ([]CollectionMember, error) {
	if collectionID == "" {
		return nil, ValidationError("collection ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	members, err := s.collectionMembersLocked(ctx, collectionID)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return members, nil
	}
	ids := make([]MediaID, len(members))
	for i, m := range members {
		ids[i] = m.MediaID
	}
	visible, err := s.visibleMediaBatchSQL(ctx, ids, filter)
	if err != nil {
		return nil, err
	}
	out := make([]CollectionMember, 0, len(members))
	for _, m := range members {
		if _, ok := visible[m.MediaID]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// CollectionMemberMediaIDs returns just the ordered, visibility-filtered MediaIDs
// of a Collection — the seed/pack-target shape built on ResolveCollectionMembers.
func (s *Store) CollectionMemberMediaIDs(ctx context.Context, collectionID CollectionID, filter LibraryFilter) ([]MediaID, error) {
	members, err := s.ResolveCollectionMembers(ctx, collectionID, filter)
	if err != nil {
		return nil, err
	}
	ids := make([]MediaID, len(members))
	for i, m := range members {
		ids[i] = m.MediaID
	}
	return ids, nil
}

func (s *Store) AddCollectionMedia(ctx context.Context, collectionID CollectionID, ids []MediaID, filter LibraryFilter, now time.Time) (Collection, error) {
	if collectionID == "" {
		return Collection{}, ValidationError("collection ID is required")
	}
	ids = compactMediaIDs(ids)
	if len(ids) == 0 {
		return Collection{}, ValidationError("media IDs are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Collection{}, err
	}
	c, err := s.collectionByIDVisibleLocked(ctx, collectionID, false, filter)
	if err != nil {
		return Collection{}, err
	}
	visible, err := s.visibleMediaBatchSQL(ctx, ids, filter)
	if err != nil {
		return Collection{}, err
	}
	if len(visible) != len(ids) {
		return Collection{}, ValidationError("media not found")
	}
	next, err := s.nextCollectionMemberSortOrderLocked(ctx, collectionID)
	if err != nil {
		return Collection{}, err
	}
	nowMS := now.UTC().UnixMilli()
	// One transaction for the whole membership change: a mid-loop failure
	// must not leave the collection partially mutated.
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return Collection{}, err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if visible[id].LibraryID != c.LibraryID {
			return Collection{}, ValidationError("collection media must be in the same library")
		}
	}
	// Set-based INSERT chunked by ID: one multi-row
	// statement per 500-ID chunk instead of one INSERT per Media, still inside
	// the single transaction above so a mid-chunk failure rolls back cleanly.
	for start := 0; start < len(ids); start += bulkWriteChunkSize {
		chunk := ids[start:min(start+bulkWriteChunkSize, len(ids))]
		placeholders := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)*4)
		for i, id := range chunk {
			placeholders[i] = "(?, ?, ?, ?)"
			args = append(args, collectionID, id, next, nowMS)
			next++
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO collection_media (collection_id, media_id, sort_order, added_at_ms)
			VALUES `+strings.Join(placeholders, ", "),
			args...); err != nil {
			return Collection{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE collection SET updated_at_ms = ? WHERE id = ?`, nowMS, collectionID); err != nil {
		return Collection{}, err
	}
	if err := tx.Commit(); err != nil {
		return Collection{}, err
	}
	return s.collectionByIDLocked(ctx, collectionID, true)
}

func (s *Store) RemoveCollectionMedia(ctx context.Context, collectionID CollectionID, ids []MediaID, filter LibraryFilter, now time.Time) (Collection, error) {
	if collectionID == "" {
		return Collection{}, ValidationError("collection ID is required")
	}
	ids = compactMediaIDs(ids)
	if len(ids) == 0 {
		return Collection{}, ValidationError("media IDs are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Collection{}, err
	}
	if _, err := s.collectionByIDVisibleLocked(ctx, collectionID, true, filter); err != nil {
		return Collection{}, err
	}
	// One transaction for the whole membership change: a mid-loop failure
	// must not leave the collection partially mutated.
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return Collection{}, err
	}
	defer tx.Rollback()
	// Set-based DELETE chunked by ID: one statement per
	// 500-ID chunk instead of one DELETE per Media, still inside the single
	// transaction above.
	for start := 0; start < len(ids); start += bulkWriteChunkSize {
		chunk := ids[start:min(start+bulkWriteChunkSize, len(ids))]
		placeholders := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)+1)
		args = append(args, collectionID)
		for i, id := range chunk {
			placeholders[i] = "?"
			args = append(args, id)
		}
		//nolint:gosec // G202: the concatenation is only joined "?" placeholders; all values bound via args.
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM collection_media WHERE collection_id = ? AND media_id IN (`+strings.Join(placeholders, ", ")+`)
		`, args...); err != nil {
			return Collection{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE collection SET updated_at_ms = ? WHERE id = ?`, now.UTC().UnixMilli(), collectionID); err != nil {
		return Collection{}, err
	}
	if err := tx.Commit(); err != nil {
		return Collection{}, err
	}
	return s.collectionByIDLocked(ctx, collectionID, true)
}

func (s *Store) ReorderCollectionMedia(ctx context.Context, collectionID CollectionID, ids []MediaID, filter LibraryFilter, now time.Time) (Collection, error) {
	if collectionID == "" {
		return Collection{}, ValidationError("collection ID is required")
	}
	ids = compactMediaIDs(ids)
	if len(ids) == 0 {
		return Collection{}, ValidationError("media IDs are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Collection{}, err
	}
	if _, err := s.collectionByIDVisibleLocked(ctx, collectionID, false, filter); err != nil {
		return Collection{}, err
	}
	// One transaction for the whole reorder: failing on member N must not
	// leave members 1..N-1 with their new sort order committed.
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return Collection{}, err
	}
	defer tx.Rollback()
	// Set-based UPDATE chunked by ID: one CASE-driven
	// statement assigns every id's new sort_order per 500-ID chunk instead of
	// one UPDATE per Media. RowsAffected for the chunk must still equal the
	// chunk size — anything less means some id wasn't an existing member,
	// exactly like the old per-id zero-rows check, just detected per chunk
	// instead of per item; either way the whole transaction rolls back.
	for start := 0; start < len(ids); start += bulkWriteChunkSize {
		chunk := ids[start:min(start+bulkWriteChunkSize, len(ids))]
		caseWhens := make([]string, len(chunk))
		caseArgs := make([]any, 0, len(chunk)*2)
		idPlaceholders := make([]string, len(chunk))
		idArgs := make([]any, 0, len(chunk))
		for i, id := range chunk {
			caseWhens[i] = "WHEN ? THEN ?"
			caseArgs = append(caseArgs, id, start+i+1)
			idPlaceholders[i] = "?"
			idArgs = append(idArgs, id)
		}
		args := make([]any, 0, len(caseArgs)+1+len(idArgs))
		args = append(args, caseArgs...)
		args = append(args, collectionID)
		args = append(args, idArgs...)
		result, err := tx.ExecContext(ctx, `
			UPDATE collection_media
			SET sort_order = CASE media_id `+strings.Join(caseWhens, " ")+` END
			WHERE collection_id = ? AND media_id IN (`+strings.Join(idPlaceholders, ", ")+`)
		`, args...)
		if err != nil {
			return Collection{}, err
		}
		if n, _ := result.RowsAffected(); n != int64(len(chunk)) {
			return Collection{}, ValidationError("collection member not found")
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE collection SET updated_at_ms = ? WHERE id = ?`, now.UTC().UnixMilli(), collectionID); err != nil {
		return Collection{}, err
	}
	if err := tx.Commit(); err != nil {
		return Collection{}, err
	}
	return s.collectionByIDLocked(ctx, collectionID, true)
}

func (s *Store) collectionByIDLocked(ctx context.Context, id CollectionID, includeTrash bool) (Collection, error) {
	query := `
		SELECT c.id, c.library_id, c.parent_id, c.name, c.sort_order, c.trashed_at_ms,
		       c.created_at_ms, c.updated_at_ms, COUNT(cm.media_id)
		FROM collection c
		LEFT JOIN collection_media cm ON cm.collection_id = c.id
		WHERE c.id = ?`
	if !includeTrash {
		query += ` AND c.trashed_at_ms IS NULL`
	}
	query += ` GROUP BY c.id`
	c, err := scanCollection(s.catalogR.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Collection{}, ErrCollectionNotFound
	}
	return c, err
}

func (s *Store) collectionByIDVisibleLocked(ctx context.Context, id CollectionID, includeTrash bool, filter LibraryFilter) (Collection, error) {
	c, err := s.collectionByIDLocked(ctx, id, includeTrash)
	if err != nil {
		return Collection{}, err
	}
	if err := s.libraryVisibleSQL(ctx, c.LibraryID, filter); err != nil {
		return Collection{}, ErrCollectionNotFound
	}
	return c, nil
}

func (s *Store) collectionMembersLocked(ctx context.Context, collectionID CollectionID) ([]CollectionMember, error) {
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT collection_id, media_id, sort_order, added_at_ms
		FROM collection_media
		WHERE collection_id = ?
		ORDER BY sort_order, media_id
	`, collectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CollectionMember
	for rows.Next() {
		var m CollectionMember
		var addedAtMS int64
		if err := rows.Scan(&m.CollectionID, &m.MediaID, &m.SortOrder, &addedAtMS); err != nil {
			return nil, err
		}
		m.AddedAt = time.UnixMilli(addedAtMS).UTC()
		out = append(out, m)
	}
	if out == nil {
		out = []CollectionMember{}
	}
	return out, rows.Err()
}

func (s *Store) nextCollectionSortOrderLocked(ctx context.Context, libraryID LibraryID, parentID CollectionID) (int, error) {
	var max sql.NullInt64
	var err error
	if parentID == "" {
		err = s.catalogR.QueryRowContext(ctx, `
			SELECT MAX(sort_order) FROM collection
			WHERE library_id = ? AND parent_id IS NULL
		`, libraryID).Scan(&max)
	} else {
		err = s.catalogR.QueryRowContext(ctx, `
			SELECT MAX(sort_order) FROM collection
			WHERE library_id = ? AND parent_id = ?
		`, libraryID, parentID).Scan(&max)
	}
	if err != nil {
		return 0, err
	}
	return int(max.Int64) + 1, nil
}

func (s *Store) nextCollectionMemberSortOrderLocked(ctx context.Context, collectionID CollectionID) (int, error) {
	var max sql.NullInt64
	if err := s.catalogR.QueryRowContext(ctx, `
		SELECT MAX(sort_order) FROM collection_media WHERE collection_id = ?
	`, collectionID).Scan(&max); err != nil {
		return 0, err
	}
	return int(max.Int64) + 1, nil
}

func (s *Store) collectionDescendantLocked(ctx context.Context, candidate, ancestor CollectionID) (bool, error) {
	var one int
	err := s.catalogR.QueryRowContext(ctx, `
		WITH RECURSIVE descendants(id) AS (
			SELECT id FROM collection WHERE parent_id = ?
			UNION ALL
			SELECT c.id FROM collection c JOIN descendants d ON c.parent_id = d.id
		)
		SELECT 1 FROM descendants WHERE id = ? LIMIT 1
	`, ancestor, candidate).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

type collectionScanner interface {
	Scan(dest ...any) error
}

func scanCollection(scanner collectionScanner) (Collection, error) {
	var c Collection
	var parent sql.NullString
	var trashedAt sql.NullInt64
	var createdAtMS, updatedAtMS int64
	if err := scanner.Scan(&c.ID, &c.LibraryID, &parent, &c.Name, &c.SortOrder, &trashedAt, &createdAtMS, &updatedAtMS, &c.MemberCount); err != nil {
		return Collection{}, err
	}
	if parent.Valid {
		c.ParentID = CollectionID(parent.String)
	}
	if trashedAt.Valid {
		t := time.UnixMilli(trashedAt.Int64).UTC()
		c.TrashedAt = &t
	}
	c.CreatedAt = time.UnixMilli(createdAtMS).UTC()
	c.UpdatedAt = time.UnixMilli(updatedAtMS).UTC()
	return c, nil
}

func compactMediaIDs(ids []MediaID) []MediaID {
	out := make([]MediaID, 0, len(ids))
	seen := map[MediaID]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func randomCollectionID() (CollectionID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return CollectionID("collection-" + hex.EncodeToString(raw[:])), nil
}
