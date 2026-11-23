package store

import (
	"os"
	"path/filepath"
	"strings"
)

// LibraryRootsOverlap reports whether two library folders are the same folder
// or one holds the other (CORE-1202). It only compares folders: nothing that
// reads or changes a file may use the identity it computes.
func LibraryRootsOverlap(a, b string) bool {
	ka, kb := folderIdentityKey(a), folderIdentityKey(b)
	return pathWithinRoot(ka, kb) || pathWithinRoot(kb, ka) ||
		sameFolderAncestor(ka, kb) || sameFolderAncestor(kb, ka)
}

// folderIdentityKey is the absolute, cleaned path with every symlink in its
// longest existing prefix resolved and the missing suffix kept as spelled, so a
// link into a Library's folder compares equal to the folder itself.
func folderIdentityKey(path string) string {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return filepath.Clean(path)
	}
	for existing, suffix := abs, ""; ; {
		if resolved, err := filepath.EvalSymlinks(existing); err == nil {
			return filepath.Join(resolved, suffix)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return abs
		}
		suffix = filepath.Join(filepath.Base(existing), suffix)
		existing = parent
	}
}

// sameFolderAncestor reports whether some ancestor of inner (or inner itself)
// is the very folder outer names, even when the two spell it with different
// letter case on a case-insensitive volume. It asks the file system rather than
// guessing which volumes fold case.
func sameFolderAncestor(inner, outer string) bool {
	outerInfo, err := os.Stat(outer)
	if err != nil {
		return false
	}
	for cur := inner; ; cur = filepath.Dir(cur) {
		if info, err := os.Stat(cur); err == nil && os.SameFile(info, outerInfo) {
			return true
		}
		if filepath.Dir(cur) == cur {
			return false
		}
	}
}
