package scan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/beamfall/core/internal/store"
)

// Rescan preview (CORE-1054). A preview walks a Library's roots the way a scan
// would and reports what the scan would change, without writing anything: no
// hashing, no probing, no store writes. It compares each walked file with the
// catalogued row through the scan's own fast-path triple (fingerprint present,
// size, millisecond mtime), so "update" means "the scan will re-hash this
// file", not "the content is known to differ".

// Preview change kinds, named by shape (invariant #1).
const (
	PreviewChangeAdd     = "add"
	PreviewChangeUpdate  = "update"
	PreviewChangeMissing = "missing"
)

// DefaultPreviewChangeLimit bounds the listed changes when the caller asks for
// none or for more than MaxPreviewChangeLimit. The counts always cover every
// change; only the list is bounded.
const (
	DefaultPreviewChangeLimit = 100
	MaxPreviewChangeLimit     = 500
)

// PreviewChange is one planned change: a file the scan would add, re-hash, or
// mark missing.
type PreviewChange struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

// Preview is the planned outcome of rescanning a Library's roots.
type Preview struct {
	Adds      int `json:"adds"`
	Updates   int `json:"updates"`
	Missing   int `json:"missing"`
	Unchanged int `json:"unchanged"`
	// Changes lists planned changes in walk order (adds and updates), then
	// missing files, capped at the requested limit. Truncated reports that
	// the counts hold more changes than the list.
	Changes   []PreviewChange `json:"changes"`
	Truncated bool            `json:"truncated"`
	// MissingSkipped carries, per root, why the scan's missing-pass guard
	// would skip that root. Its unseen files are then not counted as missing,
	// because the scan would not mark them.
	MissingSkipped []string `json:"missingSkipped"`
}

// PreviewRoots walks roots for libraryID and returns the planned changes,
// listing at most limit of them. It applies the scan's directory exclusions,
// media gates and stability window, and its missing-pass guard. An unreadable
// root fails the preview, as it fails the scan. rule is the Library's image
// rule, so images count as items exactly when the scan imports them
// (CORE-0043, CORE-2180, CORE-2361).
func PreviewRoots(ctx context.Context, st *store.Store, libraryID store.LibraryID, roots []string, limit int, rule ImageRule) (Preview, error) {
	if st == nil {
		return Preview{}, errors.New("store is required")
	}
	if libraryID == "" {
		return Preview{}, errors.New("library ID is required")
	}
	if limit <= 0 || limit > MaxPreviewChangeLimit {
		limit = DefaultPreviewChangeLimit
	}
	stored := map[string]store.MediaFile{}
	if err := st.ForEachMediaFileForLibrary(ctx, libraryID, func(file store.MediaFile) error {
		stored[file.Path] = file
		return nil
	}); err != nil {
		return Preview{}, err
	}
	p := previewState{preview: Preview{Changes: []PreviewChange{}, MissingSkipped: []string{}}, limit: limit, stored: stored, imageRule: rule}
	var missing []PreviewChange
	for _, root := range roots {
		rootMissing, err := p.walkRoot(ctx, root)
		if err != nil {
			return Preview{}, err
		}
		missing = append(missing, rootMissing...)
	}
	for _, change := range missing {
		p.record(change)
	}
	return p.preview, nil
}

type previewState struct {
	preview   Preview
	limit     int
	stored    map[string]store.MediaFile
	imageRule ImageRule
}

func (p *previewState) record(change PreviewChange) {
	switch change.Kind {
	case PreviewChangeAdd:
		p.preview.Adds++
	case PreviewChangeUpdate:
		p.preview.Updates++
	case PreviewChangeMissing:
		p.preview.Missing++
	}
	if len(p.preview.Changes) < p.limit {
		p.preview.Changes = append(p.preview.Changes, change)
		return
	}
	p.preview.Truncated = true
}

// walkRoot classifies every walked file under root and returns the root's
// missing files, which the caller records after all walks so the list keeps
// adds and updates first.
func (p *previewState) walkRoot(ctx context.Context, root string) ([]PreviewChange, error) {
	if err := checkScanRootReadable(root); err != nil {
		return nil, err
	}
	root = filepath.Clean(root)
	seen := map[string]bool{}
	walked, added := 0, 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			if entry != nil && entry.IsDir() && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if skipErr := skipExcludedScanDirectory(entry.Name(), entry.IsDir()); skipErr != nil {
			return skipErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		path = filepath.Clean(path)
		if !previewSupportedMediaFile(path, info, p.imageRule) || now().Sub(info.ModTime()) < stabilityWindow {
			return nil
		}
		walked++
		seen[path] = true
		file, known := p.stored[path]
		switch {
		case !known:
			added++
			p.record(PreviewChange{Kind: PreviewChangeAdd, Path: path})
		case previewUnchanged(file, info):
			p.preview.Unchanged++
		default:
			p.record(PreviewChange{Kind: PreviewChangeUpdate, Path: path})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	prefix := root + string(filepath.Separator)
	present := 0
	var missing []PreviewChange
	for path := range p.stored {
		if path != root && !strings.HasPrefix(path, prefix) {
			continue
		}
		if !seen[path] && p.imageRule.skips(path) {
			continue
		}
		present++
		if !seen[path] {
			missing = append(missing, PreviewChange{Kind: PreviewChangeMissing, Path: path})
		}
	}
	// The scan counts present files after its imports, so the files this walk
	// would add join the guard's denominator.
	if reason := missingPassSkipReason(present+added, len(missing), walked); reason != "" {
		p.preview.MissingSkipped = append(p.preview.MissingSkipped, root+": "+reason)
		return nil, nil
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i].Path < missing[j].Path })
	return missing, nil
}

func previewSupportedMediaFile(path string, info fs.FileInfo, rule ImageRule) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	return info.Mode().IsRegular() && titleFromPath(path) != "" && mediaExtensions[ext] && !rule.skips(path)
}

// previewUnchanged is the scan's fast-path test (workerTask.fill): a file the
// scan would skip without hashing.
func previewUnchanged(file store.MediaFile, info fs.FileInfo) bool {
	return file.ContentFingerprint != "" &&
		file.SizeBytes == info.Size() &&
		store.MediaFileModTimeMatches(file.ModTime, info.ModTime())
}

// LibraryStateDigest fingerprints what a preview was planned against: the
// roots and every live catalogued file of the Library with its fingerprint,
// size, mtime and last-seen time. Any scan, import, missing-mark or root
// change in between moves the digest, so a commit can refuse a stale preview.
// A file changing on disk without a catalog write does not move it; the scan
// the commit starts reads the disk as it is then.
func LibraryStateDigest(ctx context.Context, st *store.Store, libraryID store.LibraryID, roots []string) (string, error) {
	if st == nil {
		return "", errors.New("store is required")
	}
	hash := sha256.New()
	sortedRoots := append([]string(nil), roots...)
	sort.Strings(sortedRoots)
	for _, root := range sortedRoots {
		fmt.Fprintf(hash, "root\x00%s\x00", filepath.Clean(root))
	}
	err := st.ForEachMediaFileForLibrary(ctx, libraryID, func(file store.MediaFile) error {
		fmt.Fprintf(hash, "file\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d\x00%d\x00%d\x00",
			file.MediaID, file.Path, file.ContentFingerprint, file.VersionLabel, file.PartOrdinal,
			file.SizeBytes, file.ModTime.UnixMilli(), file.LastSeenAt.UnixMilli())
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
