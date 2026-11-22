package scan

import (
	"context"
	"os"
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

// discStreamName matches a disc rip's stream file: a bare five-digit stream
// number with a transport-stream extension, as a disc's STREAM folder names
// them ("00019.m2ts").
var discStreamName = regexp.MustCompile(`(?i)^\d{5}\.(?:m2ts|mts)$`)

// isExtraPath reports whether a scanned file is an Extra by the established
// folder/suffix conventions. It looks at the filename stem and at the
// directory components between the Library root and the file; the root
// itself and the folders above it never count, so a Library stored under a
// folder named "Trailers" or "Extras" still holds main Media (CORE-2189).
// Only a disc stream file reads its folder's listing (see strayDiscStream).
func isExtraPath(root, path string) bool {
	return matchExtraPath(root, path).isExtra()
}

// extraMatch records every Extra convention a path matches, so parent
// resolution can fall back from one to the next.
type extraMatch struct {
	suffix     string // the matched filename-stem suffix, if any
	container  string // the nearest Extras folder holding the file, if any
	discStream bool   // a disc stream file beside a titled main file
}

// isExtra reports whether any Extra convention matched.
func (m extraMatch) isExtra() bool {
	return m.suffix != "" || m.container != "" || m.discStream
}

// matchExtraPath is the single classifier behind isExtraPath and
// extraParentMediaID, so every path classified an Extra is also considered
// for an extra-of relation.
func matchExtraPath(root, path string) extraMatch {
	var match extraMatch
	stem := strings.ToLower(titleFromPath(path))
	for _, suffix := range extraNameSuffixes {
		if strings.HasSuffix(stem, suffix) {
			match.suffix = suffix
			break
		}
	}
	match.container = extraContainerDir(root, path)
	if !match.isExtra() {
		match.discStream = strayDiscStream(root, path)
	}
	return match
}

// strayDiscStream reports whether path is a disc stream file ("00019.m2ts")
// in a folder strictly below root that also holds a titled video file: a
// disc rip's featurette or trailer kept beside the folder's main file
// (CORE-3060). A folder holding only disc stream files keeps them all as
// main Media, since nothing says which one is the main file.
func strayDiscStream(root, path string) bool {
	if root == "" || !discStreamName.MatchString(filepath.Base(path)) {
		return false
	}
	root = filepath.Clean(root)
	dir := filepath.Clean(filepath.Dir(path))
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || discStreamName.MatchString(name) || titleFromPath(name) == "" {
			continue
		}
		if !videoExtensions[strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")] {
			continue
		}
		if matchExtraPath(root, filepath.Join(dir, name)).suffix == "" {
			return true
		}
	}
	return false
}

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
		if existing := byDirTitle[key]; existing != "" {
			// Two Versions of one Media may share a title; only a different
			// Media makes the title ambiguous.
			if existing != imported.MediaID {
				ambiguousTitle[key] = true
			}
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

// extraParentMediaID resolves the main Media an Extra belongs to. Every
// convention the path matches is tried in turn, so an Extra matching both (a
// "-trailer" file inside an Extras folder) still resolves through the folder
// when the suffix finds nothing:
//   - a stem suffix names its parent by title in the same folder
//     ("Title-trailer.mkv" beside "Title.mkv");
//   - an Extras folder belongs to the sole main Media of the folder holding
//     it;
//   - a disc stream file belongs to the sole main Media of its own folder.
//
// It returns "" when neither resolves, and the file stays an unlinked Extra:
// picking one of several candidates would attach it to the wrong item.
func extraParentMediaID(root, path string, byDir map[string][]store.MediaID, byDirTitle map[extraParentTitleKey]store.MediaID) store.MediaID {
	match := matchExtraPath(root, path)
	dir := filepath.Clean(filepath.Dir(path))
	if match.suffix != "" {
		parentTitle := strings.TrimSuffix(strings.ToLower(titleFromPath(path)), match.suffix)
		if parentTitle != "" {
			if id := byDirTitle[extraParentTitleKey{dir: dir, title: parentTitle}]; id != "" {
				return id
			}
		}
	}
	if match.container != "" {
		return soleMediaID(byDir[filepath.Clean(filepath.Dir(match.container))])
	}
	if match.discStream {
		return soleMediaID(byDir[dir])
	}
	return ""
}

// soleMediaID returns the only distinct ID in candidates, or "" when there is
// not exactly one. A Media with several Versions in the folder lists its ID
// once per file, and still counts as one.
func soleMediaID(candidates []store.MediaID) store.MediaID {
	sole := store.MediaID("")
	for _, id := range candidates {
		if sole != "" && id != sole {
			return ""
		}
		sole = id
	}
	return sole
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
	dir := filepath.Clean(filepath.Dir(path))
	for dir != root {
		rel, err := filepath.Rel(root, dir)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return ""
		}
		if extraFolderNames[strings.ToLower(filepath.Base(dir))] {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	return ""
}
