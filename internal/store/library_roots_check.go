package store

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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
	default:
		return ValidationError("The server can't open " + e.Path + ".")
	}
}
