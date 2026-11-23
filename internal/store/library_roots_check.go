package store

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// CheckLibraryRoots confirms each root exists on the server, is a directory and
// can be read, before a Library is created with it or pointed at it (CORE-1598).
// A refusal is a *LibraryRootError: a ValidationError that names the path and
// the reason in plain words, so a client can put it next to the folder field.
func CheckLibraryRoots(roots []string) error {
	for _, root := range roots {
		if err := checkLibraryRoot(root); err != nil {
			return err
		}
	}
	return nil
}

// CheckAddedLibraryRoots runs CheckLibraryRoots on the submitted roots that a
// Library update adds. A root already saved on the Library may be kept even
// when the server can no longer read it, so a Library whose folders have gone
// missing can still be repaired by removing them one at a time (CORE-1606).
func CheckAddedLibraryRoots(saved, submitted []string) error {
	kept := make(map[string]bool, len(saved))
	for _, root := range saved {
		kept[cleanLibraryRoot(root)] = true
	}
	for _, root := range submitted {
		if strings.TrimSpace(root) != "" && kept[cleanLibraryRoot(root)] {
			continue
		}
		if err := checkLibraryRoot(root); err != nil {
			return err
		}
	}
	return nil
}

// CheckLibraryRootsOverlap refuses a submitted root that equals, holds or sits
// inside a folder of another live Library, or of another submitted root, so one
// folder never feeds two Libraries (CORE-1202). saved is the Library's current
// roots (nil for a new Library): a kept root is never refused, and a Library's
// own other roots are not "another Library". libraryID is that Library, or empty
// for a new one.
func (s *Store) CheckLibraryRootsOverlap(ctx context.Context, libraryID LibraryID, saved, submitted []string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	others, err := s.liveLibraryRootsLocked(ctx, libraryID)
	if err != nil {
		return err
	}
	return checkLibraryRootsOverlap(saved, submitted, others)
}

// liveLibraryRootsLocked lists the folders of every live Library except
// exceptID, with the Store's read lock held.
func (s *Store) liveLibraryRootsLocked(ctx context.Context, exceptID LibraryID) ([]string, error) {
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT lr.root
		FROM library_roots AS lr
		JOIN library AS l ON l.id = lr.library_id
		WHERE l.trashed_at_ms IS NULL AND lr.library_id <> ?
		ORDER BY lr.root`, exceptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roots []string
	for rows.Next() {
		var root string
		if err := rows.Scan(&root); err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}
	return roots, rows.Err()
}

func checkLibraryRootsOverlap(saved, submitted, others []string) error {
	kept := make(map[string]bool, len(saved))
	for _, root := range saved {
		kept[cleanLibraryRoot(root)] = true
	}
	for i, root := range submitted {
		cleaned := cleanLibraryRoot(root)
		if kept[cleaned] {
			continue
		}
		overlapsOther := func(other string) bool { return LibraryRootsOverlap(cleaned, other) }
		overlapsPeer := func(peer string) bool { return cleanLibraryRoot(peer) != cleaned && LibraryRootsOverlap(cleaned, peer) }
		if slices.ContainsFunc(others, overlapsOther) ||
			slices.ContainsFunc(submitted[:i], overlapsPeer) || slices.ContainsFunc(submitted[i+1:], overlapsPeer) {
			return &LibraryRootError{Reason: LibraryRootOverlaps, Path: cleaned}
		}
	}
	return nil
}

// cleanLibraryRoot is the absolute, cleaned form a root is saved in.
func cleanLibraryRoot(root string) string {
	root = strings.TrimSpace(root)
	if absolute, err := filepath.Abs(root); err == nil {
		return filepath.Clean(absolute)
	}
	return root
}

func checkLibraryRoot(root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return ValidationError("library root is required")
	}
	path := cleanLibraryRoot(root)
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return &LibraryRootError{Reason: LibraryRootMissing, Path: path}
	case errors.Is(err, fs.ErrPermission):
		return &LibraryRootError{Reason: LibraryRootUnreadable, Path: path}
	case err != nil:
		return &LibraryRootError{Reason: LibraryRootCantOpen, Path: path}
	case !info.IsDir():
		return &LibraryRootError{Reason: LibraryRootNotFolder, Path: path}
	}
	dir, err := os.Open(path) // #nosec G304 -- the owner-chosen folder is only listed to prove it is readable
	if err != nil {
		return &LibraryRootError{Reason: LibraryRootUnreadable, Path: path}
	}
	defer dir.Close()
	if _, err := dir.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return &LibraryRootError{Reason: LibraryRootUnreadable, Path: path}
	}
	return nil
}

// LibraryRootReason says why CheckLibraryRoots refused a folder.
type LibraryRootReason string

// The CheckLibraryRoots refusal reasons.
const (
	LibraryRootMissing    LibraryRootReason = "missing"
	LibraryRootCantOpen   LibraryRootReason = "cant-open"
	LibraryRootNotFolder  LibraryRootReason = "not-folder"
	LibraryRootUnreadable LibraryRootReason = "unreadable"
	LibraryRootOverlaps   LibraryRootReason = "overlaps"
)

// LibraryRootError is a CheckLibraryRoots refusal. It unwraps to the English
// ValidationError a client shows as is; the graph layer renders the same
// sentence in the request locale from Reason and Path (CORE-2102).
type LibraryRootError struct {
	Reason LibraryRootReason
	Path   string
}

func (e *LibraryRootError) Error() string { return string(e.validation()) }

// Unwrap exposes the English ValidationError to errors.As.
func (e *LibraryRootError) Unwrap() error { return e.validation() }

func (e *LibraryRootError) validation() ValidationError {
	switch e.Reason {
	case LibraryRootMissing:
		return ValidationError("The folder " + e.Path + " doesn't exist on the server.")
	case LibraryRootNotFolder:
		return ValidationError(e.Path + " is a file, not a folder.")
	case LibraryRootUnreadable:
		return ValidationError("The server doesn't have permission to read " + e.Path + ".")
	case LibraryRootOverlaps:
		return ValidationError("The folder " + e.Path + " is already part of a library.")
	default:
		return ValidationError("The server can't open " + e.Path + ".")
	}
}
