package store

import (
	"context"
	"errors"
	"strings"
)

const maxSelectedDuplicateIDs = 200

// SelectedDuplicateCluster is one canonical survivor and the exact live
// membership MergeApply would consider for it.
type SelectedDuplicateCluster struct {
	Survivor MediaID
	Members  []MediaID
}

// SelectedDuplicateClusters returns only duplicate clusters intersecting the
// bounded selected set. It discovers canonical survivors in one query and
// batch-loads their exact merge membership in one more, independent of the
// Library's total number of duplicate groups.
func (s *Store) SelectedDuplicateClusters(ctx context.Context, libraryID LibraryID, selected []MediaID, filter LibraryFilter) ([]SelectedDuplicateCluster, error) {
	if libraryID == "" {
		return nil, ValidationError("library ID is required")
	}
	if len(selected) == 0 {
		return []SelectedDuplicateCluster{}, nil
	}
	if len(selected) > maxSelectedDuplicateIDs {
		return nil, ValidationError("too many selected media IDs")
	}
	selected = uniqueMediaIDs(selected)

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	visible, err := s.libraryVisibleLocked(ctx, libraryID, filter)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, ValidationError("unknown library")
	}

	selectedValues, selectedArgs := mediaIDValues(selected)
	args := append([]any{}, selectedArgs...)
	args = append(args, libraryID)
	rows, err := s.catalogR.QueryContext(ctx, `
		WITH selected(id) AS (VALUES `+selectedValues+`),
		selected_hash(hash) AS (
			SELECT DISTINCT f.oshash
			FROM selected s
			JOIN media m ON m.id = s.id
			JOIN media_file f ON f.media_id = m.id
			WHERE m.library_id = ? AND m.trashed_at_ms IS NULL
			  AND m.extra = 0 AND f.missing_at_ms IS NULL AND f.oshash <> ''
		)
		SELECT f.oshash, MIN(m.id)
		FROM selected_hash sh
		JOIN media_file f ON f.oshash = sh.hash AND f.missing_at_ms IS NULL
		JOIN media m ON m.id = f.media_id
		WHERE m.library_id = ? AND m.trashed_at_ms IS NULL AND m.extra = 0
		GROUP BY f.oshash
		HAVING COUNT(DISTINCT m.id) > 1
		ORDER BY f.oshash
	`, append(args, libraryID)...)
	if err != nil {
		return nil, err
	}
	var survivors []MediaID
	seenSurvivor := map[MediaID]bool{}
	for rows.Next() {
		var hash string
		var survivor MediaID
		if err := rows.Scan(&hash, &survivor); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		if !seenSurvivor[survivor] {
			seenSurvivor[survivor] = true
			survivors = append(survivors, survivor)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(survivors) == 0 {
		return []SelectedDuplicateCluster{}, nil
	}

	survivorValues, survivorArgs := mediaIDValues(survivors)
	membershipArgs := append([]any{}, survivorArgs...)
	membershipArgs = append(membershipArgs, libraryID)
	rows, err = s.catalogR.QueryContext(ctx, `
		WITH survivor(id) AS (VALUES `+survivorValues+`)
		SELECT s.id, m.id
		FROM survivor s
		JOIN media_file sf ON sf.media_id = s.id
		  AND sf.missing_at_ms IS NULL AND sf.oshash <> ''
		JOIN media_file f ON f.oshash = sf.oshash
		  AND f.missing_at_ms IS NULL AND f.oshash <> ''
		JOIN media m ON m.id = f.media_id
		WHERE m.library_id = ? AND m.trashed_at_ms IS NULL AND m.extra = 0
		GROUP BY s.id, m.id
		ORDER BY s.id, m.id
	`, membershipArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	clusters := make([]SelectedDuplicateCluster, len(survivors))
	indexes := make(map[MediaID]int, len(survivors))
	for i, survivor := range survivors {
		clusters[i].Survivor = survivor
		indexes[survivor] = i
	}
	for rows.Next() {
		var survivor, member MediaID
		if err := rows.Scan(&survivor, &member); err != nil {
			return nil, err
		}
		clusters[indexes[survivor]].Members = append(clusters[indexes[survivor]].Members, member)
	}
	return clusters, rows.Err()
}

func uniqueMediaIDs(ids []MediaID) []MediaID {
	unique := make([]MediaID, 0, len(ids))
	seen := make(map[MediaID]bool, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	return unique
}

func mediaIDValues(ids []MediaID) (string, []any) {
	values := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		values[i] = "(?)"
		args[i] = id
	}
	return strings.Join(values, ","), args
}
