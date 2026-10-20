package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxFolderTreeDepth = 3

// FolderEntry is one read-only server folder-tree node for the Manage folder
// picker. It is intentionally generic: folder/file shape, no domain vocabulary.
type FolderEntry struct {
	Name       string
	Path       string
	Kind       string
	Size       int64
	ModifiedAt time.Time
	Children   []FolderEntry
}

// FolderTree returns a bounded read-only view rooted in a visible Library's
// configured roots. A caller may omit root to list configured Library roots, or
// provide a root that must resolve under one of those roots. Symlink-resolved
// containment prevents a picker request from escaping the Library root set.
func (s *Store) FolderTree(ctx context.Context, libraryID LibraryID, root string, depth int, filter LibraryFilter) (FolderEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return FolderEntry{}, err
	}
	visible, err := s.libraryVisibleLocked(ctx, libraryID, filter)
	if err != nil {
		return FolderEntry{}, err
	}
	if !visible {
		return FolderEntry{}, ValidationError("unknown library")
	}
	roots, err := libraryRootsSQL(ctx, s.catalogR, libraryID)
	if err != nil {
		return FolderEntry{}, err
	}
	if len(roots) == 0 {
		return FolderEntry{}, ValidationError("library has no roots")
	}
	depth = clampFolderTreeDepth(depth)
	if strings.TrimSpace(root) == "" {
		children := make([]FolderEntry, 0, len(roots))
		for _, configured := range roots {
			entry, err := folderEntry(configured, max(0, depth-1))
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					children = append(children, FolderEntry{
						Name: filepath.Base(filepath.Clean(configured)),
						Path: filepath.Clean(configured),
						Kind: "folder",
					})
					continue
				}
				return FolderEntry{}, err
			}
			children = append(children, entry)
		}
		return FolderEntry{Name: "Library roots", Kind: "folder", Children: children}, nil
	}

	selected, rootOK, err := containedFolderRoot(root, roots)
	if err != nil {
		return FolderEntry{}, err
	}
	if !rootOK {
		return FolderEntry{}, ValidationError("folder is outside the library roots")
	}
	return folderEntry(selected, depth)
}

func clampFolderTreeDepth(depth int) int {
	switch {
	case depth < 0:
		return 0
	case depth > maxFolderTreeDepth:
		return maxFolderTreeDepth
	default:
		return depth
	}
}

func containedFolderRoot(root string, roots []string) (string, bool, error) {
	selected, err := canonicalExistingPath(root)
	if err != nil {
		return "", false, err
	}
	for _, configured := range roots {
		base, err := canonicalExistingPath(configured)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", false, err
		}
		if pathWithinRoot(base, selected) {
			return selected, true, nil
		}
	}
	return "", false, nil
}

func canonicalExistingPath(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func folderEntry(path string, depth int) (FolderEntry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return FolderEntry{}, err
	}
	kind := "file"
	if info.IsDir() {
		kind = "folder"
	}
	entry := FolderEntry{
		Name:       info.Name(),
		Path:       filepath.Clean(path),
		Kind:       kind,
		Size:       info.Size(),
		ModifiedAt: info.ModTime().UTC(),
	}
	if !info.IsDir() || depth <= 0 {
		return entry, nil
	}
	children, err := os.ReadDir(path)
	if err != nil {
		return FolderEntry{}, err
	}
	sort.Slice(children, func(i, j int) bool {
		leftFolder := children[i].IsDir()
		rightFolder := children[j].IsDir()
		if leftFolder != rightFolder {
			return leftFolder
		}
		return strings.ToLower(children[i].Name()) < strings.ToLower(children[j].Name())
	})
	entry.Children = make([]FolderEntry, 0, len(children))
	for _, child := range children {
		if child.Name() == managedFileTrashDir {
			continue
		}
		childEntry, err := folderEntry(filepath.Join(path, child.Name()), depth-1)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return FolderEntry{}, err
		}
		entry.Children = append(entry.Children, childEntry)
	}
	return entry, nil
}
