package store

import "context"

// LibraryEncryptionOwners returns every Library ID, including soft-deleted
// rows. Secure-store ref migration must retain trashed ownership so it cannot
// misclassify a recoverable Library's keys as orphaned or assign a colliding
// legacy suffix to a live row.
func (s *Store) LibraryEncryptionOwners(ctx context.Context) ([]LibraryID, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `SELECT id FROM library ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []LibraryID
	for rows.Next() {
		var id LibraryID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
