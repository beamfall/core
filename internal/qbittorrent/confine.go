package qbittorrent

import (
	"context"
	"os"
	"path/filepath"
)

// rootGuard is the per-pass containment context for endpoint-nominated payload
// paths. Every path a Source reports is untrusted: a compromised
// or MITM'd qBittorrent endpoint can nominate any readable path as a
// completed download. The guard admits only paths whose fully resolved form
// lies beneath a canonical operator-approved download root AND outside every
// Library root, and it refuses symlink leaves outright (no-follow), so
// a link swapped in after resolution can never be fingerprinted or
// hardlinked as if it were the checked file.
type rootGuard struct {
	// approved are the symlink-resolved approved download roots. Empty means
	// nothing can pass (fail-closed until ingest is configured with roots).
	approved []string
	// library are the symlink-resolved roots of every Library. Config
	// validation already rejects overlapping download dirs, but Library roots
	// can drift after approval; this keeps the exclusion live at import time.
	library []string
}

// buildGuard resolves the current approved roots and every Library root into a
// rootGuard. Approved roots that do not resolve are dropped
// (an unresolvable root can contain no existing payload); a Library-root
// lookup failure is an error, because the Library exclusion cannot be proven.
func (i *Ingestor) buildGuard(ctx context.Context, approved []string) (rootGuard, error) {
	guard := rootGuard{}
	for _, root := range approved {
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		guard.approved = append(guard.approved, resolved)
	}
	roots, err := i.catalog.AllLibraryRoots(ctx)
	if err != nil {
		return rootGuard{}, err
	}
	for _, root := range roots {
		resolved, resolveErr := filepath.EvalSymlinks(root)
		if resolveErr != nil {
			// A Library root that does not resolve cannot contain a
			// resolved payload path; keep its cleaned form anyway so the
			// exclusion still covers a literal-prefix match.
			resolved = filepath.Clean(root)
		}
		guard.library = append(guard.library, resolved)
	}
	return guard, nil
}

// confinesPath reports whether path — with every symlink resolved — lies
// beneath (or at) an approved download root and outside every Library root.
// Any resolution failure is a refusal.
func (g rootGuard) confinesPath(path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	underApproved := false
	for _, root := range g.approved {
		if resolved == root || isSubpath(resolved, root) {
			underApproved = true
			break
		}
	}
	if !underApproved {
		return false
	}
	for _, root := range g.library {
		if resolved == root || isSubpath(resolved, root) {
			return false
		}
	}
	return true
}

// confinesFile is confinesPath plus the no-follow leaf check: the path itself
// must be a regular file (never a symlink), so the exact inode admitted here
// is the one the fingerprint/link/copy/sidecar steps operate on.
func (g rootGuard) confinesFile(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return g.confinesPath(path)
}
