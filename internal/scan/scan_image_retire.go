package scan

import (
	"log/slog"
	"path/filepath"
	"slices"
	"strings"

	"github.com/beamfall/core/internal/store"
)

// retireExcludedImages moves to the recoverable trash every image-class Media
// under the scan root whose files the Library's image rule no longer imports
// (CORE-2361). A Library whose Plugin declares classes without image catalogued
// sidecar or stray images before the rule skipped them; the walk now leaves
// those rows behind, and the trash takes them off the grid.
//
// It runs only for ImagesAsArtwork and only on a walk that found media files:
// a walk the empty-root guard treats as empty says nothing about which files
// exist, so it retires nothing. A Media is retired only when every live file
// it has lies under the scan root and is skipped by the rule. It runs before
// the missing pass, which would otherwise hide those files from the stream.
func (s *scanWalkState) retireExcludedImages() error {
	if s.imageRule != ImagesAsArtwork || s.walkMediaFiles == 0 {
		return nil
	}
	root := filepath.Clean(s.req.Root)
	prefix := root + string(filepath.Separator)
	retire := map[store.MediaID]bool{}
	err := s.st.ForEachMediaFileForLibrary(s.ctx, s.req.LibraryID, func(file store.MediaFile) error {
		excluded := (file.Path == root || strings.HasPrefix(file.Path, prefix)) && s.imageRule.skips(file.Path)
		if prior, seen := retire[file.MediaID]; seen {
			retire[file.MediaID] = prior && excluded
		} else {
			retire[file.MediaID] = excluded
		}
		return nil
	})
	if err != nil {
		return err
	}
	ids := make([]store.MediaID, 0, len(retire))
	for id, ok := range retire {
		if ok {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	slices.Sort(ids)
	trashed, skipped, err := s.st.TrashMediaRetiredByScan(s.ctx, s.req.LibraryID, store.MediaClassImage, ids)
	if skipped {
		slog.Warn("scan: not retiring images the library no longer imports", "library", string(s.req.LibraryID), "root", s.req.Root, "reason", "trash disabled", "candidates", len(ids))
		return nil
	}
	if err != nil {
		return err
	}
	slog.Info("scan: retired images the library no longer imports", "library", string(s.req.LibraryID), "root", s.req.Root, "trashed", trashed)
	return nil
}
