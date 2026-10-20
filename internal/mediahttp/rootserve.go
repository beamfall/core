package mediahttp

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// errRootScope is the single fail-closed refusal every root-scoped filesystem
// check returns. It is deliberately opaque: the byte routes map it onto the same
// 404/503 they already use for "no such file", so a probe cannot distinguish a
// refused traversal from a missing member.
var errRootScope = errors.New("mediahttp: path failed root-scoped validation")

// rootedPath names a file by the directory it must stay inside plus the
// root-relative member name within it. It exists so validation and use can never
// drift apart: the byte routes carry the (root, member) pair instead of a
// flattened absolute path, and every read goes through openRooted, which
// re-derives the file from a descriptor on that root rather than from the string.
//
// A flattened path cannot express this. Once "root/member" is joined into one
// string, an opener has no way to re-establish which prefix was the trusted
// boundary, so it can only re-walk the whole name — and every component of that
// walk is a fresh, unvalidated resolution the caller already believed it had
// checked.
type rootedPath struct {
	// root is the directory the member must resolve inside. It is server-owned
	// (a rendition directory, the subtitle cache, a media file's own directory),
	// never caller-supplied.
	root string
	// rel is the root-relative member name, already reduced to its cleaned form.
	rel string
}

// String renders the rooted path for diagnostics, encryption-plane lookups, and
// the persisted identities that still take a path. It is NOT an opening path —
// nothing in this package opens the result.
func (p rootedPath) String() string {
	if p.root == "" || p.rel == "" {
		return ""
	}
	return filepath.Join(p.root, p.rel)
}

// valid reports whether the pair was populated by a validating constructor.
func (p rootedPath) valid() bool { return p.root != "" && p.rel != "" }

// newRootedPath validates a caller-influenced member name against a server-owned
// root. The name is rejected when it is empty, absolute, volume-rooted, carries a
// NUL, or climbs out of the root once cleaned. This is a cheap prefilter, NOT the
// containment guarantee — openRooted's descriptor-relative resolution is what
// actually confines the read, and it re-checks every component of the name at
// open time.
func newRootedPath(root, name string) (rootedPath, error) {
	if root == "" || name == "" {
		return rootedPath{}, errRootScope
	}
	if strings.ContainsRune(name, 0) {
		return rootedPath{}, errRootScope
	}
	local := filepath.FromSlash(name)
	if filepath.IsAbs(local) || filepath.VolumeName(local) != "" {
		return rootedPath{}, errRootScope
	}
	// A leading separator is not "absolute" on Windows but still addresses the
	// volume root, so reject it explicitly rather than relying on IsAbs.
	if strings.HasPrefix(local, string(filepath.Separator)) || strings.HasPrefix(name, "/") {
		return rootedPath{}, errRootScope
	}
	clean := filepath.Clean(local)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return rootedPath{}, errRootScope
	}
	return rootedPath{root: root, rel: clean}, nil
}

// rootedFile splits an already-server-derived absolute path into the directory it
// lives in and its final component, so a legacy path-producing seam can still be
// read through the root-scoped opener. The containment win is the final
// component: it is resolved relative to a held descriptor on the directory and
// bound to the directory entry, so it can no longer be swapped for a symlink
// between the producer's check and this read.
func rootedFile(path string) rootedPath {
	if path == "" {
		return rootedPath{}
	}
	dir, base := filepath.Split(path)
	dir = strings.TrimSuffix(dir, string(filepath.Separator))
	if dir == "" || base == "" {
		return rootedPath{}
	}
	return rootedPath{root: dir, rel: base}
}

// openRooted opens a rooted path for reading using root-scoped,
// descriptor-relative operations, and binds the result to the exact directory
// entry it resolved. It fails closed on every swap the byte routes are exposed
// to:
//
//   - Ancestor traversal: os.OpenRoot holds a descriptor on the root and
//     resolves every later component beneath it, so an intermediate symlink
//     cannot leave the root — not even one planted after the caller's own checks.
//   - Root replacement: the held descriptor keeps naming the directory that was
//     opened. Comparing it against a fresh resolution of the root NAME catches a
//     root that was renamed, deleted, or replaced by another directory or a
//     symlink, and refuses rather than serving out of a stale or attacker-placed
//     tree. The check runs again after the open, so a replacement landing inside
//     the open window is caught too.
//   - Final-component swap: the member is Lstat'd, required to be a REGULAR file
//     (a symlink final component is refused outright — never followed), then
//     opened and re-Stat'd through the returned descriptor. os.SameFile binds the
//     opened file to the entry that was checked, so a name swapped between the
//     check and the open yields a mismatch instead of bytes.
//
// The returned *os.File is the ONLY handle callers may read from; re-opening by
// name would reintroduce exactly the window this closes. The caller owns closing
// it.
func openRooted(p rootedPath) (*os.File, fs.FileInfo, error) {
	if !p.valid() {
		return nil, nil, errRootScope
	}
	root, err := os.OpenRoot(p.root)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	if err := verifyRootUnreplaced(root, p.root); err != nil {
		return nil, nil, err
	}
	entry, err := root.Lstat(p.rel)
	if err != nil {
		return nil, nil, err
	}
	if !entry.Mode().IsRegular() {
		return nil, nil, errRootScope
	}
	file, err := root.OpenFile(p.rel, os.O_RDONLY, 0)
	if err != nil {
		return nil, nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(entry, opened) {
		_ = file.Close()
		return nil, nil, errRootScope
	}
	if err := verifyRootUnreplaced(root, p.root); err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	return file, opened, nil
}

// statRooted resolves a rooted path to its directory entry without keeping the
// file open, for the callers that only need existence, size, or mod time. It
// runs the same root-replacement and regular-file checks as openRooted, so a
// member reached through a symlink is reported as absent rather than described.
func statRooted(p rootedPath) (fs.FileInfo, error) {
	if !p.valid() {
		return nil, errRootScope
	}
	root, err := os.OpenRoot(p.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := verifyRootUnreplaced(root, p.root); err != nil {
		return nil, err
	}
	entry, err := root.Lstat(p.rel)
	if err != nil {
		return nil, err
	}
	if !entry.Mode().IsRegular() {
		return nil, errRootScope
	}
	return entry, nil
}

// verifyRootUnreplaced refuses a root whose NAME no longer resolves to the
// directory the held descriptor references. os.SameFile compares the underlying
// device/inode identity (and the platform equivalent elsewhere), so this is an
// identity check, not a string comparison: a root that was swapped for a
// different directory, replaced by a symlink to one, or unlinked entirely all
// fail here. A root reached THROUGH a stable symlink still passes, because both
// sides resolve to the same directory.
func verifyRootUnreplaced(root *os.Root, name string) error {
	held, err := root.Stat(".")
	if err != nil {
		return err
	}
	current, err := os.Stat(name)
	if err != nil {
		return err
	}
	if !os.SameFile(held, current) {
		return errRootScope
	}
	return nil
}

// serveRooted streams a rooted path's bytes to the client from the bound
// descriptor. Range requests, conditional requests, and Content-Length all come
// from http.ServeContent reading that descriptor — the file is never re-opened
// by name, so the bytes served are exactly the ones the identity binding
// approved.
func serveRooted(w http.ResponseWriter, r *http.Request, name string, p rootedPath, modTime time.Time) error {
	file, info, err := openRooted(p)
	if err != nil {
		return err
	}
	defer file.Close()
	if modTime.IsZero() {
		modTime = info.ModTime()
	}
	http.ServeContent(w, r, name, modTime, file)
	return nil
}

// rootScopeStatus maps a root-scoped failure onto the status the route already
// uses for an absent member. A refused traversal and a missing file are reported
// identically so the refusal leaks no information about what is on disk.
func rootScopeStatus(err error) int {
	if errors.Is(err, errRootScope) || errors.Is(err, os.ErrNotExist) {
		return http.StatusNotFound
	}
	return http.StatusServiceUnavailable
}
