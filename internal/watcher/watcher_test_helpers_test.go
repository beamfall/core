package watcher

import (
	"path/filepath"

	"github.com/beamfall/core/internal/store"
)

func (s *Service) watchedCount(libraryID store.LibraryID, root string) int {
	key := rootKey{libraryID: libraryID, root: filepath.Clean(root)}
	s.mu.Lock()
	state, ok := s.roots[key]
	s.mu.Unlock()
	if !ok {
		return 0
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return len(state.watched)
}

func (s *Service) rootStateForTest(libraryID store.LibraryID, root string) (*rootState, bool) {
	key := rootKey{libraryID: libraryID, root: filepath.Clean(root)}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.roots[key]
	return state, ok
}
