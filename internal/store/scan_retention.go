package store

import (
	"context"
	"strings"
)

// LibraryHasUndeclaredMedia reports whether a plugin declaration is too narrow
// to authorize a scan's retirement/missing pass. An empty declaration keeps the legacy unrestricted scan policy.
func (s *Store) LibraryHasUndeclaredMedia(ctx context.Context, libraryID LibraryID, classes []MediaClass) (bool, error) {
	if len(classes) == 0 {
		return false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if err != nil {
		return false, err
	}
	args := []any{libraryID}
	placeholders := make([]string, len(classes))
	for i, class := range classes {
		placeholders[i] = "?"
		args = append(args, class)
	}
	var found bool
	err = plane.reader.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM media WHERE library_id = ? AND media_class NOT IN (`+strings.Join(placeholders, ",")+`))`, args...).Scan(&found)
	return found, err
}
