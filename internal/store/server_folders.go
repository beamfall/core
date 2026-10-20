package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// linuxPseudoRoots are top-level Linux pseudo-/virtual filesystems that are
// never valid Library roots and are noisy or slow to enumerate (/proc lists one
// directory per process; /run and /snap churn). They are skipped from the
// synthetic "/" listing so the operator sees real storage, not kernel state.
var linuxPseudoRoots = map[string]bool{
	"/proc": true,
	"/sys":  true,
	"/dev":  true,
	"/run":  true,
	"/snap": true,
}

var (
	serverFolderGOOS        = runtime.GOOS
	serverFolderVolumesRoot = "/Volumes"
)

// BrowseServerFolders returns a bounded, read-only, DIRECTORIES-ONLY view of the
// server filesystem for the first-run setup folder picker. Unlike
// FolderTree it is NOT scoped to a Library's roots — at setup time no Library
// exists yet — so it is gated at the resolver by the MANAGER role instead. An
// empty path lists platform roots ("/", plus /Volumes/* on Darwin); a non-empty
// path is canonicalized (abs + symlink-resolved) and listed one bounded level
// deep. Only directories are returned, so no file names or sizes leak through
// the picker.
func (s *Store) BrowseServerFolders(ctx context.Context, path string, depth int) (FolderEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return FolderEntry{}, err
	}
	depth = clampFolderTreeDepth(depth)
	target := strings.TrimSpace(path)
	if target == "" {
		return serverFolderRoots(depth)
	}
	canonical, err := canonicalExistingPath(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FolderEntry{}, ValidationError("folder does not exist")
		}
		if errors.Is(err, os.ErrPermission) {
			return FolderEntry{}, ValidationError("folder is not readable")
		}
		return FolderEntry{}, err
	}
	return serverFolderEntry(canonical, depth)
}

func serverFolderRoots(depth int) (FolderEntry, error) {
	children := []FolderEntry{}
	root, err := serverFolderEntry(string(filepath.Separator), max(0, depth-1))
	if err != nil {
		return FolderEntry{}, err
	}
	children = append(children, root)
	if serverFolderGOOS == "darwin" {
		volumes, err := os.ReadDir(serverFolderVolumesRoot)
		if err == nil {
			for _, volume := range volumes {
				full := filepath.Join(serverFolderVolumesRoot, volume.Name())
				if !childIsDir(volume, full) {
					continue
				}
				entry, err := serverFolderEntry(full, max(0, depth-1))
				if err != nil {
					if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
						continue
					}
					return FolderEntry{}, err
				}
				children = append(children, entry)
			}
		} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, os.ErrPermission) {
			return FolderEntry{}, err
		}
	}
	return FolderEntry{Name: "Server roots", Path: string(filepath.Separator), Kind: "folder", Children: children}, nil
}

// serverFolderEntry builds a dirs-only FolderEntry rooted at an already
// canonical path. Symlinks to directories are followed (os.Stat) so symlinked
// mounts stay navigable; everything that is not a directory is dropped.
func serverFolderEntry(path string, depth int) (FolderEntry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FolderEntry{}, err
	}
	if !info.IsDir() {
		return FolderEntry{}, ValidationError("path is not a folder")
	}
	entry := FolderEntry{
		Name:       info.Name(),
		Path:       filepath.Clean(path),
		Kind:       "folder",
		ModifiedAt: info.ModTime().UTC(),
	}
	if entry.Name == "" {
		entry.Name = entry.Path // the filesystem root "/" has an empty Base.
	}
	if depth <= 0 {
		return entry, nil
	}
	children, err := os.ReadDir(path)
	if err != nil {
		// A directory we can stat but not read (permission) is a leaf, not a
		// hard error — the operator can still pick it or navigate elsewhere.
		if errors.Is(err, os.ErrPermission) {
			return entry, nil
		}
		return FolderEntry{}, err
	}
	atRoot := entry.Path == string(filepath.Separator)
	sort.Slice(children, func(i, j int) bool {
		return strings.ToLower(children[i].Name()) < strings.ToLower(children[j].Name())
	})
	entry.Children = make([]FolderEntry, 0, len(children))
	for _, child := range children {
		full := filepath.Join(path, child.Name())
		if child.Name() == managedFileTrashDir {
			continue
		}
		if atRoot && serverFolderGOOS == "linux" && linuxPseudoRoots[full] {
			continue
		}
		if !childIsDir(child, full) {
			continue
		}
		childEntry, err := serverFolderEntry(full, depth-1)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
				continue
			}
			return FolderEntry{}, err
		}
		entry.Children = append(entry.Children, childEntry)
	}
	return entry, nil
}

// childIsDir reports whether a directory entry is (or symlinks to) a directory.
// os.ReadDir reports a symlink as a symlink, not a dir, so symlinked mounts need
// an explicit os.Stat to resolve their target.
func childIsDir(child os.DirEntry, full string) bool {
	if child.IsDir() {
		return true
	}
	if child.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(full)
	return err == nil && info.IsDir()
}
