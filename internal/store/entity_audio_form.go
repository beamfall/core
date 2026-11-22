package store

import (
	"context"
	"errors"
)

// FirstMediaForEntity returns the first item MediaForEntity lists for the
// Entity under the filter, with MediaForEntity's visibility, plane and order,
// and false when it lists none (CORE-2288). It reads one row however large
// the Entity is.
func (s *Store) FirstMediaForEntity(ctx context.Context, entityID EntityID, filter LibraryFilter) (SourceItem, bool, error) {
	if entityID == "" {
		return SourceItem{}, false, errors.New("entity ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return SourceItem{}, false, err
	}
	items, err := s.mediaForEntitySQL(ctx, entityID, filter, 1, 0, nil)
	if err != nil || len(items) == 0 {
		return SourceItem{}, false, err
	}
	return items[0], true, nil
}
