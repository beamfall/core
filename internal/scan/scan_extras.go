package scan

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/beamfall/core/internal/store"
)

// extraFolderNames are the established (Plex/Kodi/Jellyfin) directory names
// whose contents are Extras. A file anywhere under a directory
// with one of these names — case-insensitively — is an Extra. These are
// filesystem CONVENTIONS the scanner recognises, not stored labels: the
// detected bit is the generic media.extra flag (invariant #1), never the
// folder's word.
var extraFolderNames = map[string]bool{
	"extras":      true,
	"featurettes": true,
	"trailers":    true,
	"interviews":  true,
	"shorts":      true,
}

// extraNameSuffixes are the established filename-stem suffixes that mark a
// single file as an Extra: e.g. "Title-trailer.mkv" or
// "Title-sample.mp4". Matched case-insensitively against the stem (filename
// without extension).
var extraNameSuffixes = []string{
	"-trailer",
	"-sample",
	"-interview",
	"-featurette",
	"-short",
	"-behindthescenes",
}

// isExtraPath reports whether a scanned file is an Extra by the established
// folder/suffix conventions. It looks at the path's own
// directory components and its filename stem; it does not stat the filesystem.
func isExtraPath(path string) bool {
	stem := strings.ToLower(titleFromPath(path))
	for _, suffix := range extraNameSuffixes {
		if strings.HasSuffix(stem, suffix) {
			return true
		}
	}
	dir := filepath.Dir(path)
	for {
		base := strings.ToLower(filepath.Base(dir))
		if extraFolderNames[base] {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

type extraParentTitleKey struct {
	dir   string
	title string
}

func linkExtraRelationsForLibrary(ctx context.Context, st *store.Store, libraryID store.LibraryID) error {
	byDir := map[string][]store.MediaID{}
	byDirTitle := map[extraParentTitleKey]store.MediaID{}
	ambiguousTitle := map[extraParentTitleKey]bool{}
	extras := []ImportedFile{}
	if err := st.ForEachMediaFileForLibrary(ctx, libraryID, func(file store.MediaFile) error {
		imported := ImportedFile{
			Path:               file.Path,
			MediaID:            file.MediaID,
			ContentFingerprint: file.ContentFingerprint,
		}
		if imported.MediaID == "" || imported.Path == "" {
			return nil
		}
		if isExtraPath(imported.Path) {
			extras = append(extras, imported)
			return nil
		}
		dir := filepath.Clean(filepath.Dir(imported.Path))
		byDir[dir] = append(byDir[dir], imported.MediaID)
		key := extraParentTitleKey{dir: dir, title: strings.ToLower(titleFromPath(imported.Path))}
		if byDirTitle[key] != "" {
			ambiguousTitle[key] = true
			return nil
		}
		byDirTitle[key] = imported.MediaID
		return nil
	}); err != nil {
		return err
	}
	for key := range ambiguousTitle {
		delete(byDirTitle, key)
	}
	for _, file := range extras {
		parentID := extraParentMediaID(file.Path, byDir, byDirTitle)
		if parentID == "" {
			continue
		}
		if err := st.LinkMediaRelation(ctx, file.MediaID, store.MediaRelationExtraOf, parentID); err != nil {
			return err
		}
	}
	return nil
}

func extraParentMediaID(path string, byDir map[string][]store.MediaID, byDirTitle map[extraParentTitleKey]store.MediaID) store.MediaID {
	dir := filepath.Clean(filepath.Dir(path))
	stem := strings.ToLower(titleFromPath(path))
	for _, suffix := range extraNameSuffixes {
		if strings.HasSuffix(stem, suffix) {
			parentTitle := strings.TrimSuffix(stem, suffix)
			if parentTitle == "" {
				return ""
			}
			return byDirTitle[extraParentTitleKey{dir: dir, title: parentTitle}]
		}
	}
	if extrasDir := extraContainerDir(path); extrasDir != "" {
		candidates := byDir[filepath.Clean(filepath.Dir(extrasDir))]
		if len(candidates) == 1 {
			return candidates[0]
		}
	}
	return ""
}

func extraContainerDir(path string) string {
	dir := filepath.Clean(filepath.Dir(path))
	for {
		if extraFolderNames[strings.ToLower(filepath.Base(dir))] {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
