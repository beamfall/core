package scan

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/beamfall/core/internal/store"
)

// extraFolderNames are the established (Plex/Kodi/Jellyfin) directory names
// whose contents are Extras. A file anywhere under a directory
// with one of these names — case-insensitively — below the Library root is an
// Extra. These are
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

// titleExtraFolderNames are the further extras folders Plex and Jellyfin
// recognise. Their names are common words a library may use as a category
// ("Other", "Scenes"), so, as in those servers, they count only inside a
// title folder, never directly under the Library root, and only for video
// files: a music or photo folder named "Other" holds main Media.
var titleExtraFolderNames = map[string]bool{
	"behind the scenes": true,
	"deleted scenes":    true,
	"scenes":            true,
	"clips":             true,
	"samples":           true,
	"other":             true,
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

// videoExtraNameSuffixes are the further suffixes Plex and Jellyfin
// recognise. Words like "-other" or "-scene" also end ordinary song and
// photo names, so they mark only a video file as an Extra.
var videoExtraNameSuffixes = []string{
	"-deleted",
	"-deletedscene",
	"-scene",
	"-clip",
	"-other",
	"-extra",
}

// isVideoPath reports whether path has a video file extension.
func isVideoPath(path string) bool {
	return videoExtensions[strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")]
}

// isExtraPath reports whether a scanned file is an Extra by the established
// folder/suffix conventions. It looks at the filename stem and at the
// directory components between the Library root and the file; the root
// itself and the folders above it never count, so a Library stored under a
// folder named "Trailers" or "Extras" still holds main Media (CORE-2189). It
// does not stat the filesystem.
func isExtraPath(root, path string) bool {
	_, ok := matchExtraPath(root, path)
	return ok
}

// extraMatch says which convention marked a file as an Extra, carrying what
// parent resolution needs. Exactly one of suffix, ordinal or container is set.
type extraMatch struct {
	suffix    string // the matched filename-stem suffix
	ordinal   bool   // the stem matched extraOrdinalStem
	container string // the nearest Extras folder holding the file
}

// matchExtraPath is the single classifier behind isExtraPath and
// extraParentMediaID, so every path classified an Extra is also considered
// for an extra-of relation. Conventions are tried in a fixed order: stem
// suffixes, then the video-only suffixes and ordinal stem, then folders.
func matchExtraPath(root, path string) (extraMatch, bool) {
	stem := strings.ToLower(titleFromPath(path))
	for _, suffix := range extraNameSuffixes {
		if strings.HasSuffix(stem, suffix) {
			return extraMatch{suffix: suffix}, true
		}
	}
	if isVideoPath(path) {
		for _, suffix := range videoExtraNameSuffixes {
			if strings.HasSuffix(stem, suffix) {
				return extraMatch{suffix: suffix}, true
			}
		}
		if extraOrdinalStem.MatchString(stem) {
			return extraMatch{ordinal: true}, true
		}
	}
	if dir := extraContainerDir(root, path); dir != "" {
		return extraMatch{container: dir}, true
	}
	return extraMatch{}, false
}

// extraOrdinalStem matches a filename stem numbered by a group but not by an
// item within it, with an "x" in the item's place and a label after it ("3.x
// Out Takes", "2x Set Tour"): the disc-rip convention for a group's or
// volume's bonus material. The label must start with a letter, so an ordinal
// pair ("3x07") never matches. It applies to video files only, and a dash
// may not sit between the number and the "x", so a numbered music track
// ("01 - X Ambassadors - Renegades.flac") is never an Extra.
var extraOrdinalStem = regexp.MustCompile(`^\d{1,2}[ ._]*x[ ._-]+[a-z]`)

type extraParentTitleKey struct {
	dir   string
	title string
}

// linkExtraRelationsForLibrary links each Extra in the Library to its main
// Media. root is the Library root that bounds the folder conventions (see
// extrasRootFor).
func linkExtraRelationsForLibrary(ctx context.Context, st *store.Store, libraryID store.LibraryID, root string) error {
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
		if isExtraPath(root, imported.Path) {
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
		parentID := extraParentMediaID(root, file.Path, byDir, byDirTitle)
		if parentID == "" {
			continue
		}
		if err := st.LinkMediaRelation(ctx, file.MediaID, store.MediaRelationExtraOf, parentID); err != nil {
			return err
		}
	}
	return nil
}

// extraParentMediaID resolves the main Media an Extra belongs to, following
// the convention matchExtraPath reported for it:
//   - a stem suffix names its parent by title in the same folder
//     ("Title-deleted.mkv" beside "Title.mkv");
//   - an ordinal stem ("3.x Out Takes") belongs to its folder's group, so the
//     parent is the folder's main Media when there is exactly one;
//   - an Extras folder belongs to the folder holding it, again only when
//     that folder has exactly one main Media.
//
// When no single parent can be determined (no title match, or a folder with
// several main Media) it returns "" and the file stays an unlinked Extra:
// guessing among several candidates would attach it to the wrong item, and
// the classification must not depend on what else the folder holds, so a
// rescan never flips a file between Extra and main Media.
func extraParentMediaID(root, path string, byDir map[string][]store.MediaID, byDirTitle map[extraParentTitleKey]store.MediaID) store.MediaID {
	match, ok := matchExtraPath(root, path)
	if !ok {
		return ""
	}
	dir := filepath.Clean(filepath.Dir(path))
	switch {
	case match.suffix != "":
		parentTitle := strings.TrimSuffix(strings.ToLower(titleFromPath(path)), match.suffix)
		if parentTitle == "" {
			return ""
		}
		return byDirTitle[extraParentTitleKey{dir: dir, title: parentTitle}]
	case match.ordinal:
		return soleMediaID(byDir[dir])
	default:
		return soleMediaID(byDir[filepath.Clean(filepath.Dir(match.container))])
	}
}

// soleMediaID returns the only ID in candidates, or "" when there is not
// exactly one.
func soleMediaID(candidates []store.MediaID) store.MediaID {
	if len(candidates) == 1 {
		return candidates[0]
	}
	return ""
}

// extrasRootFor returns the folder that bounds the Extras folder conventions
// for a scan of scanRoot: the longest configured root of the Library that
// holds scanRoot, so a scan aimed at a folder inside the Library (even an
// Extras folder) still sees the folders between the Library root and it. A
// scanRoot under no configured root bounds itself.
func extrasRootFor(ctx context.Context, st *store.Store, libraryID store.LibraryID, scanRoot string) (string, error) {
	roots, err := st.LibraryRoots(ctx, libraryID)
	if err != nil {
		return "", err
	}
	scanRoot = filepath.Clean(scanRoot)
	best := ""
	for _, root := range roots {
		root = filepath.Clean(root)
		rel, err := filepath.Rel(root, scanRoot)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		if len(root) > len(best) {
			best = root
		}
	}
	if best == "" {
		return scanRoot, nil
	}
	return best, nil
}

// extraContainerDir returns the nearest ancestor folder of path, strictly
// below root, whose name is an Extras convention; "" when there is none or
// path is not under root.
func extraContainerDir(root, path string) string {
	if root == "" {
		return ""
	}
	root = filepath.Clean(root)
	video := isVideoPath(path)
	dir := filepath.Clean(filepath.Dir(path))
	for dir != root {
		rel, err := filepath.Rel(root, dir)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return ""
		}
		name := strings.ToLower(filepath.Base(dir))
		if extraFolderNames[name] || (video && titleExtraFolderNames[name] && strings.ContainsRune(rel, filepath.Separator)) {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	return ""
}
