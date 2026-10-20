package qbittorrent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/beamfall/core/internal/store"
)

// ValidateRouting checks the operator's routing config against reality before
// ingest runs:
//
//   - every routed category exists in qBittorrent (a typo'd category would
//     silently never route), and
//   - no qBittorrent save path lies inside a routed Library root — otherwise the
//     scanner would see the same fingerprint at two paths (§2).
//
// It returns the first problem found, or nil. Unrouted categories are NOT an
// error: ignoring an unmapped category is the designed behavior (§3).
//
// downloadDirs lets callers supply qBittorrent's save paths (per category, plus
// any default). When nil, only the category-existence check runs.
//
// It validates the live config; Reconfigure validates a candidate config the
// same way via validateRouting before committing it.
func (i *Ingestor) ValidateRouting(ctx context.Context, downloadDirs []string) error {
	i.mu.RLock()
	source := i.source
	routing := i.routing
	i.mu.RUnlock()
	if source == nil {
		// Inert: no endpoint to validate against. Nothing is routed, so there is
		// nothing to reject (ADR-0020 §3 — an empty routing is valid).
		return nil
	}
	return i.validateRouting(ctx, source, routing, downloadDirs)
}

// validateRouting runs the ADR-0020 §2/§3 checks against an explicit source +
// routing pair so the Console's setQBittorrentIngest can validate a candidate
// config before it goes live (the live source/routing are unchanged until the
// candidate passes). The catalog (Library roots) is the live one either way.
func (i *Ingestor) validateRouting(ctx context.Context, source Source, routing map[string]store.LibraryID, downloadDirs []string) error {
	known, err := source.Categories(ctx)
	if err != nil {
		return err
	}
	knownSet := map[string]bool{}
	for _, name := range known {
		knownSet[name] = true
	}
	for category := range routing {
		if !knownSet[category] {
			return fmt.Errorf("qbittorrent: routed category %q does not exist in qBittorrent", category)
		}
	}
	// A save path must not sit inside any routed Library root.
	seenRoots := map[store.LibraryID]bool{}
	for _, libraryID := range routing {
		if seenRoots[libraryID] {
			continue
		}
		seenRoots[libraryID] = true
		roots, err := i.catalog.LibraryRoots(ctx, libraryID)
		if err != nil {
			return err
		}
		for _, root := range roots {
			for _, dir := range downloadDirs {
				if pathOverlaps(dir, root) {
					return fmt.Errorf("qbittorrent: download dir overlaps Library %s root — downloads must lie outside Library roots", libraryID)
				}
			}
		}
	}
	return nil
}

// pathOverlaps reports whether a and b are the same directory or one contains
// the other. Cleaned, absolute comparison; a trailing separator is normalized.
func pathOverlaps(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if a == "" || b == "" || a == "." || b == "." {
		return false
	}
	if a == b {
		return true
	}
	return isSubpath(a, b) || isSubpath(b, a)
}

// isSubpath reports whether child is inside parent.
func isSubpath(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// escapesRoot reports whether a cleaned, relative member path would resolve
// outside the directory it is joined onto: an absolute path, the parent itself,
// or any path reaching upward through "..". qBittorrent supplies member paths
// verbatim (source.go) and filepath.Clean does NOT strip a leading "..", so a
// crafted torrent could otherwise steer relocateFile/writeSidecar outside the
// Incoming Folder (ADR-0020 §2 — downloads must stay outside Library roots).
// Pass the result of filepath.Clean here.
func escapesRoot(rel string) bool {
	return filepath.IsAbs(rel) ||
		rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolvesWithinRoot reports whether path, with all symlinks resolved, still
// lies inside root (also symlink-resolved). escapesRoot only inspects the
// literal member-path string, so a member with no ".." can still BE — or
// traverse — a symlink whose target escapes the download root; ingest would then
// fingerprint and hardlink content from outside the operator's intended roots.
// Resolving with filepath.EvalSymlinks before the relocate is the
// containment check escapesRoot cannot make.
//
// The file being ingested exists (it is a completed download), but a member or
// an intermediate component may not, and EvalSymlinks errors on a missing path.
// In that case we resolve the nearest existing ancestor — the directory ingest
// actually opens through — and contain it, since the unresolved literal tail was
// already proven ".."-free by escapesRoot upstream. An err result is a refusal:
// callers must skip the file.
func resolvesWithinRoot(path, root string) (bool, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false, err
	}
	resolved, err := resolveExistingPrefix(path)
	if err != nil {
		return false, err
	}
	return isSubpath(resolved, realRoot) || resolved == realRoot, nil
}

// resolveExistingPrefix resolves symlinks for the longest existing prefix of
// path and re-attaches the cleaned, not-yet-existing tail. filepath.EvalSymlinks
// fails on a missing path, so this walks up to the nearest extant ancestor (the
// directory that is actually opened), resolves that, and joins back the literal
// remainder — which escapesRoot already guarantees is ".."-free.
func resolveExistingPrefix(path string) (string, error) {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		// Reached the filesystem root without finding an existing ancestor.
		return "", os.ErrNotExist
	}
	resolvedParent, err := resolveExistingPrefix(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(path)), nil
}
