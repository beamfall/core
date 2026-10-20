package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const managedFileTrashDir = ".beamfall-trash"

// convertStagingDirName is the server-owned staging area under the data dir
// (outside every Library root) where the FFmpeg engine writes converted output
// before admission. Keeping the engine's output OUTSIDE any
// Library root until admission completes is what makes invariant 6 hold "by
// construction": the catalog only ever sees a file the store atomically moves
// into the root, never a half-written engine artifact, and a co-tenant process
// cannot read or replace the artifact in the root during the admission window
// (closing the write→admission TOCTOU).
const convertStagingDirName = "convert-staging"

// convertStagingRoot is the server-owned convert-staging directory: a fixed
// child of the data dir, so it is guaranteed outside every Library root.
func convertStagingRoot(dataDir string) string {
	return filepath.Join(dataDir, convertStagingDirName)
}

// prepareConvertStaging creates (idempotently) the server-owned convert-staging
// root mode 0700 and returns its path. The explicit Chmod after
// MkdirAll forces the 0700 posture even when a wider umask or a pre-existing
// looser directory would otherwise leave it group/other-accessible — the
// staging area must be owner-only so no co-tenant process can read or replace a
// converted artifact during the admission window. The directory lives under the
// data dir (s.dataDir), which is outside every Library root.
func (s *Store) prepareConvertStaging() (string, error) {
	if s.dataDir == "" {
		return "", errors.New("convert staging requires a data dir")
	}
	root := convertStagingRoot(s.dataDir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return "", err
	}
	return root, nil
}

// errManagedPathIdentity is the fail-closed refusal every root-scoped check in
// the managed-file path returns. An operation ending in it published nothing and
// left the Library root byte-identical to what it found.
var errManagedPathIdentity = errors.New("store: managed path changed identity during the operation")

// openManagedDir opens a directory as an operation root and proves the NAME
// still resolves to the directory the descriptor holds. Every later step in an
// admission, publish, rollback, or cleanup runs through that one descriptor, so
// the directory each step acts on is the same one that was validated — a
// directory renamed, deleted, or replaced between two steps is refused instead
// of silently becoming the target of the second step.
func openManagedDir(dir string) (*os.Root, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	if err := verifyManagedDirUnreplaced(root, dir); err != nil {
		_ = root.Close()
		return nil, err
	}
	return root, nil
}

// verifyManagedDirUnreplaced compares the directory the descriptor references
// against a fresh resolution of its name. os.SameFile is an inode-level identity
// comparison, so a replaced or unlinked root fails here while a root reached
// through a STABLE symlink still passes — both sides resolve to the same inode.
func verifyManagedDirUnreplaced(root *os.Root, name string) error {
	held, err := root.Stat(".")
	if err != nil {
		return err
	}
	current, err := os.Stat(name)
	if err != nil {
		return err
	}
	if !os.SameFile(held, current) {
		return errManagedPathIdentity
	}
	return nil
}

// convertStagingSuffix names convert-job staging artifacts inside the shared
// staging root. Import staging (".import-tmp") shares the same directory, so
// every convert-side sweep MUST filter on this suffix and never wipe the
// directory wholesale.
const convertStagingSuffix = ".convert-tmp"

// SweepConvertStaging removes convert-job staging residue left by generations
// that died before their admission (ADR-0174 ownership succession: a staged
// artifact belongs to exactly one lease generation, and after a restart no
// prior generation can still be executing, so every ".convert-tmp" entry is a
// dead generation's residue by construction). It runs once at startup, BEFORE
// job workers begin, and touches ONLY the convert suffix — an import's
// ".import-tmp" staging in the same directory is never removed. A missing
// staging root (fresh data dir) is a no-op.
func (s *Store) SweepConvertStaging() (removed int, err error) {
	if s.dataDir == "" {
		return 0, nil
	}
	root := convertStagingRoot(s.dataDir)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), convertStagingSuffix) {
			continue
		}
		if err := os.Remove(filepath.Join(root, entry.Name())); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// stagedSource is an open handle on a staging artifact plus a descriptor on the
// staging directory, so the bytes that are copied and the file that is later
// removed are provably the same one.
type stagedSource struct {
	file *os.File
	root *os.Root
	name string
	id   fs.FileInfo
}

// openStagedSource opens a staging artifact as a stable reference. The final
// component must be a regular file, never a symlink: the staging area is
// server-owned, so a link appearing in it is a swap, not a layout choice.
func openStagedSource(stagingPath string) (*stagedSource, error) {
	dir := filepath.Dir(stagingPath)
	name := filepath.Base(stagingPath)
	root, err := openManagedDir(dir)
	if err != nil {
		return nil, err
	}
	entry, err := root.Lstat(name)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	if !entry.Mode().IsRegular() {
		_ = root.Close()
		return nil, errManagedPathIdentity
	}
	file, err := root.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		_ = root.Close()
		return nil, err
	}
	if !os.SameFile(entry, opened) {
		_ = file.Close()
		_ = root.Close()
		return nil, errManagedPathIdentity
	}
	return &stagedSource{file: file, root: root, name: name, id: opened}, nil
}

// remove deletes the staging artifact through the staging-directory descriptor,
// and only when the name still points at the file that was copied. A cleanup can
// therefore never delete a same-named file that arrived after the copy.
func (s *stagedSource) remove() {
	current, err := s.root.Lstat(s.name)
	if err != nil || !os.SameFile(s.id, current) {
		return
	}
	_ = s.root.Remove(s.name)
}

func (s *stagedSource) close() {
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
	if s.root != nil {
		_ = s.root.Close()
		s.root = nil
	}
}

// copyIntoRootTemp writes src's bytes into a freshly created, uniquely named
// 0600 temp inside root and returns the temp's member name. O_EXCL is what makes
// the create fail closed: openat refuses any existing entry at the name,
// symlinks included, so the copy can never be redirected through a planted link.
func copyIntoRootTemp(root *os.Root, src *os.File, prefix string) (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	name := prefix + hex.EncodeToString(token[:])
	tmp, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		_ = root.Remove(name)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = root.Remove(name)
		return "", err
	}
	return name, nil
}

// newConvertStagingPath returns a fresh, unique staging path for one conversion
// under the server-owned 0700 staging root. The FFmpeg engine
// writes here — outside any Library root — and the store admits the result by
// atomically moving it into the Library root. The path carries no container
// extension (the converter reports the real container for naming at admission)
// and a random token so concurrent conversions never collide. The profile token
// is sanitized into the filename purely for operator legibility of staging.
func (s *Store) newConvertStagingPath(profile string) (string, error) {
	root, err := s.prepareConvertStaging()
	if err != nil {
		return "", err
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	name := sanitizeProfile(profile) + "-" + hex.EncodeToString(raw[:]) + convertStagingSuffix
	return filepath.Join(root, name), nil
}

// ImportManagedFile stages an out-of-Library source file UNDER a destination
// Library root, turning it into a cataloged managed Media that the existing
// convert primitive (ConvertManagedFile/ResolveManagedMediaFile) accepts (apple
// ADR-0003 §1). The server never operates on an arbitrary disk path: the caller
// passes an opaque SERVER-MINTED import token (RegisterImportSource), which the
// store resolves to the registered locator and refuses if it never vended it
// ("unknown import token") — the ADR's server-side token-provenance gate. The
// source is opened ONCE O_NOFOLLOW as the stable reference; every byte admitted
// comes from that fd, so a path-level symlink swap after the open is inert.
//
// container/codec/class are the probe-derived metadata the caller supplies
// (exactly as ConvertManagedFile sources them from its converter); class MUST be
// carried so audio/image imports are not silently defaulted to video. No `now`
// param: catalog admission stamps time.Now().UTC() internally.
//
// Symlink policy (ADR-0003 §1, secure default): a source whose FINAL component
// is a symlink is REJECTED. EvalSymlinks canonicalizes INTERMEDIATE components
// only for the out-of-library containment check; the actual open is O_NOFOLLOW
// on the fully-resolved physical path, which fails if that path's final
// component is (or is raced into) a symlink. This is the ADR-faithful
// "reject symlink escape" posture; it can be loosened to "follow unless it
// escapes" later without changing callers.
func (s *Store) ImportManagedFile(ctx context.Context, libraryID LibraryID, importToken, destParentPath, container, codec string, class MediaClass, filter LibraryFilter) (ManagedPath, error) {
	sourcePath, ok := s.resolveImportToken(importToken)
	if !ok {
		return ManagedPath{}, ValidationError("unknown import token")
	}

	// Canonicalize the source's INTERMEDIATE components only, then open the
	// result O_NOFOLLOW (ADR-0003 §1 secure default):
	//   - EvalSymlinks on the PARENT directory resolves every intermediate
	//     symlink, so an intermediate symlink pointing into a Library root can no
	//     longer disguise an in-library source as out-of-library (the
	//     containment check below uses this fully-resolved physical path).
	//   - The FINAL component is rejoined UN-resolved and opened O_RDONLY|
	//     O_NOFOLLOW, so a source whose final component is a symlink is REJECTED
	//     (ELOOP) rather than silently followed — the secure, ADR-faithful
	//     posture. (A future ticket can loosen this to "follow unless it escapes"
	//     by EvalSymlinks-ing the whole path before the containment check.)
	// O_NOFOLLOW also guards against a final-component symlink-swap race between
	// canonicalization and open; the fd that survives is the stable reference.
	abs, err := filepath.Abs(filepath.Clean(sourcePath))
	if err != nil {
		return ManagedPath{}, err
	}
	parentDir, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return ManagedPath{}, err
	}
	physical := filepath.Join(parentDir, filepath.Base(abs))
	srcFd, err := os.OpenFile(physical, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return ManagedPath{}, err
	}
	defer srcFd.Close()
	info, err := srcFd.Stat()
	if err != nil {
		return ManagedPath{}, err
	}
	if !info.Mode().IsRegular() {
		return ManagedPath{}, ValidationError("import source is not a regular file")
	}
	// Bind the open fd to the directory entry it came from, read back through a
	// descriptor on the parent. The containment check below reasons about the
	// STRING `physical`, so the import is only sound if the fd and that string
	// name the same file: without this, a source swapped between the open and the
	// check could be admitted under a containment verdict computed for a different
	// file. The parent's identity is proved at the same time, so a parent
	// directory replaced after canonicalization fails closed too.
	if err := verifySourceEntryIdentity(parentDir, filepath.Base(physical), info); err != nil {
		return ManagedPath{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ManagedPath{}, err
	}

	// Reject a source that already lives under THIS library's visible roots: it
	// is already a managed file of the destination library — the caller must
	// rename/move, not import. (A source under a *different* library's root is a
	// legitimate cross-library copy; the destination is still validated to a
	// visible root of libraryID below.)
	roots, err := s.visibleLibraryRootsLocked(ctx, libraryID, filter)
	if err != nil {
		return ManagedPath{}, err
	}
	for _, root := range roots {
		if pathWithinRoot(root, physical) {
			return ManagedPath{}, ValidationError("import source is already in the destination library")
		}
	}

	// Resolve the destination parent folder (must exist, be a folder, under a
	// visible root) and compute the in-root target. validateManagedName +
	// validateManagedTarget reject `..` escape and trash internals.
	parent, root, err := s.managedExistingPathLocked(ctx, libraryID, destParentPath, filter)
	if err != nil {
		return ManagedPath{}, err
	}
	if parent.Kind != "folder" {
		return ManagedPath{}, ValidationError("destination path is not a folder")
	}
	base := filepath.Base(physical)
	if err := validateManagedName(base); err != nil {
		return ManagedPath{}, err
	}
	target := filepath.Join(parent.Path, base)
	if err := validateManagedTarget(root, target); err != nil {
		return ManagedPath{}, err
	}
	if _, statErr := os.Lstat(target); statErr == nil {
		return ManagedPath{}, ValidationError("import target already exists")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return ManagedPath{}, statErr
	}

	// Copy FROM THE OPEN SOURCE FD into the server-owned 0700 staging area
	// (outside every Library root), then admit into the root with a NO-REPLACE,
	// NO-PARTIAL admission. The copy is bound to the fd, so the admitted bytes
	// are exactly those the fstat'd stable reference points at.
	stagingRoot, err := s.prepareConvertStaging()
	if err != nil {
		return ManagedPath{}, err
	}
	stagingPath, err := newImportStagingPath(stagingRoot)
	if err != nil {
		return ManagedPath{}, err
	}
	defer os.Remove(stagingPath)
	if err := copyOpenFileToStaging(srcFd, stagingPath); err != nil {
		return ManagedPath{}, err
	}
	if err := admitStagedFileNoReplace(stagingPath, target); err != nil {
		return ManagedPath{}, err
	}

	// Catalog the now-on-disk file as managed Media via the existing audited
	// admission — the SAME admission the convert path uses — so
	// ResolveManagedMediaFile/ConvertManagedFile accept it. On failure, undo the
	// on-disk write so no un-cataloged orphan is left.
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	_, _, err = s.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          libraryID,
		Path:               target,
		Title:              stem,
		Container:          container,
		Codec:              codec,
		Class:              class,
		ContentFingerprint: HashMediaFilePath(target),
	})
	if err != nil {
		_ = os.Remove(target)
		return ManagedPath{}, fmt.Errorf("admit imported file: %w", err)
	}
	return ManagedPath{Path: target, Kind: "file"}, nil
}

// verifySourceEntryIdentity proves that name, resolved through a descriptor on
// dir, is a regular file and is the SAME file as the already-open handle the
// caller holds. It is the join between an fd-based read and a string-based
// policy check: the policy verdict is only transferable to the fd's bytes when
// the two are proven to be one file.
func verifySourceEntryIdentity(dir, name string, opened fs.FileInfo) error {
	root, err := openManagedDir(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	entry, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !entry.Mode().IsRegular() || !os.SameFile(entry, opened) {
		return errManagedPathIdentity
	}
	return nil
}

// newImportStagingPath returns a fresh unique staging path under the server-owned
// 0700 staging root for one import (mirrors newConvertStagingPath, no profile
// token). Concurrent imports never collide on the random suffix.
func newImportStagingPath(stagingRoot string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return filepath.Join(stagingRoot, "import-"+hex.EncodeToString(raw[:])+".import-tmp"), nil
}

// copyOpenFileToStaging copies FROM the already-open source fd (the stable
// reference bound by ImportManagedFile) into a fresh 0600 staging file. It NEVER
// reopens the source by path, so the admitted bytes are exactly those the fd
// points at.
//
// The write runs through a descriptor on the staging directory, so the file is
// created inside the directory that was proved rather than wherever the staging
// name resolves at write time.
func copyOpenFileToStaging(src *os.File, stagingPath string) error {
	dir := filepath.Dir(stagingPath)
	name := filepath.Base(stagingPath)
	root, err := openManagedDir(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	dst, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = root.Remove(name)
		return err
	}
	if err := dst.Close(); err != nil {
		_ = root.Remove(name)
		return err
	}
	return nil
}

// admitStagedFileNoReplace positions a staged file into the Library root with a
// NO-REPLACE, NO-PARTIAL admission. Every admission path — import AND convert —
// uses it: an existing destination is never overwritten, because under
// ownership succession "its own output" may in fact belong to a
// NEWER lease generation of the same job, and a replace-capable admission would
// let a stale generation clobber the bytes under the successor's already
// cataloged file. os.Link is the commit primitive: atomic, and it fails with
// EEXIST when dst already exists.
//
// Fast path: when staging and the Library root share a filesystem, the staged
// inode is linked DIRECTLY at dst (hard links span directories within one
// filesystem), then the destination directory identity is re-proved and the
// landed name is required to BE the staged inode — the same proof the old
// rename admission carried. No byte copy happens in this common case. When the
// link crosses filesystems (EXDEV or an equivalent link refusal), it falls back
// to copying staging into a SIBLING TEMP of dst (so a fully-written file exists
// beside dst before any name is exposed in the root) and finalizing with
// os.Link inside the proven directory descriptor. On any failure the temp is
// removed (deferred unless committed), so no orphan partial is ever left and an
// existing dst is never touched.
//
// Copy, link, and cleanup all run through ONE descriptor on dst's directory, and
// the directory identity is re-proved immediately before the link — the only step
// that exposes a new name in the Library root. A destination directory replaced
// mid-admission is refused rather than becoming the place the file lands.
func admitStagedFileNoReplace(stagingPath, dst string) error {
	source, err := openStagedSource(stagingPath)
	if err != nil {
		return err
	}
	defer source.close()
	dstDir := filepath.Dir(dst)
	root, err := openManagedDir(dstDir)
	if err != nil {
		return err
	}
	defer root.Close()
	// Same-filesystem fast path: link the staged inode at dst without copying.
	// A cross-directory link cannot be expressed through os.Root (two roots), so
	// it is made safe the way the rename admission was: the staging entry is
	// already bound to an inode above, and after the link the destination member
	// is required to BE that inode, read back through the destination
	// descriptor. EEXIST is the no-replace refusal and is returned as-is.
	if linkErr := os.Link(stagingPath, dst); linkErr == nil {
		if err := verifyManagedDirUnreplaced(root, dstDir); err != nil {
			return err
		}
		landed, err := root.Lstat(filepath.Base(dst))
		if err != nil {
			return err
		}
		if !os.SameFile(source.id, landed) {
			return errManagedPathIdentity
		}
		source.remove()
		return nil
	} else if !errors.Is(linkErr, syscall.EXDEV) {
		// Includes EEXIST — the no-replace refusal — returned as-is.
		return linkErr
	}
	tmpName, err := copyIntoRootTemp(root, source.file, "."+filepath.Base(dst)+".import-")
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = root.Remove(tmpName)
		}
	}()
	if err := verifyManagedDirUnreplaced(root, dstDir); err != nil {
		return err
	}
	if err := root.Link(tmpName, filepath.Base(dst)); err != nil {
		return err
	}
	committed = true
	_ = root.Remove(tmpName)
	source.remove()
	return nil
}

// ManagedPath is the result shape for a Library-root-scoped folder/file
// mutation. Kind is "folder" or "file".
type ManagedPath struct {
	Path string
	Kind string
}

// ManagedPathRename is one managed file rename request.
type ManagedPathRename struct {
	Path string
	Name string
}

// ManagedTrash is one recoverable managed folder/file delete.
type ManagedTrash struct {
	TrashID      string
	OriginalPath string
	TrashPath    string
	Kind         string
}

type managedTrashManifest struct {
	ID           string    `json:"id"`
	OriginalPath string    `json:"originalPath"`
	TrashPath    string    `json:"trashPath"`
	Kind         string    `json:"kind"`
	DeletedAt    time.Time `json:"deletedAt"`
}

// CreateManagedFolder creates one folder beneath an existing Library root.
func (s *Store) CreateManagedFolder(ctx context.Context, libraryID LibraryID, parentPath, name string, filter LibraryFilter) (ManagedPath, error) {
	if err := validateManagedName(name); err != nil {
		return ManagedPath{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ManagedPath{}, err
	}
	parent, root, err := s.managedExistingPathLocked(ctx, libraryID, parentPath, filter)
	if err != nil {
		return ManagedPath{}, err
	}
	if parent.Kind != "folder" {
		return ManagedPath{}, ValidationError("parent path is not a folder")
	}
	target := filepath.Join(parent.Path, name)
	if err := validateManagedTarget(root, target); err != nil {
		return ManagedPath{}, err
	}
	if _, err := os.Lstat(target); err == nil {
		return ManagedPath{}, ValidationError("target already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return ManagedPath{}, err
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		return ManagedPath{}, err
	}
	return ManagedPath{Path: target, Kind: "folder"}, nil
}

// RenameManagedPath renames one folder/file in place under a visible Library root.
func (s *Store) RenameManagedPath(ctx context.Context, libraryID LibraryID, path, name string, filter LibraryFilter) (ManagedPath, error) {
	if err := validateManagedName(name); err != nil {
		return ManagedPath{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ManagedPath{}, err
	}
	source, root, err := s.managedExistingPathLocked(ctx, libraryID, path, filter)
	if err != nil {
		return ManagedPath{}, err
	}
	if source.Path == root {
		return ManagedPath{}, ValidationError("library root cannot be renamed")
	}
	target := filepath.Join(filepath.Dir(source.Path), name)
	return s.renameManagedPathLocked(ctx, source, root, target)
}

// BatchRenameManagedPaths renames cataloged managed files as one journaled undo
// unit. Empty folders and uncataloged files are left to RenameManagedPath: the
// existing batch journal is keyed by Media, so only Media-backed file moves can
// be reversed by UndoBatch without a new schema arm.
func (s *Store) BatchRenameManagedPaths(ctx context.Context, libraryID LibraryID, renames []ManagedPathRename, filter LibraryFilter, now time.Time) (BulkOpBatch, error) {
	if len(renames) == 0 {
		return BulkOpBatch{}, ValidationError("at least one rename is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return BulkOpBatch{}, err
	}
	plan := make([]organizeMove, 0, len(renames))
	for _, rename := range renames {
		if err := validateManagedName(rename.Name); err != nil {
			return BulkOpBatch{}, err
		}
		source, root, err := s.managedExistingPathLocked(ctx, libraryID, rename.Path, filter)
		if err != nil {
			return BulkOpBatch{}, err
		}
		if source.Path == root {
			return BulkOpBatch{}, ValidationError("library root cannot be renamed")
		}
		if source.Kind != "file" {
			return BulkOpBatch{}, ValidationError("batch rename only supports media files")
		}
		target := filepath.Join(filepath.Dir(source.Path), rename.Name)
		if err := validateManagedTarget(root, target); err != nil {
			return BulkOpBatch{}, err
		}
		if source.Path == target {
			continue
		}
		mediaID, title, err := s.mediaForManagedFilePath(ctx, libraryID, source.Path)
		if err != nil {
			return BulkOpBatch{}, err
		}
		plan = append(plan, organizeMove{mediaID: mediaID, title: title, oldPath: source.Path, newPath: target})
	}
	if len(plan) == 0 {
		return BulkOpBatch{ID: "", Kind: opKindMove, Affected: 0}, nil
	}
	plan, err := s.prepareFileMoves(plan)
	if err != nil {
		return BulkOpBatch{}, err
	}

	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return BulkOpBatch{}, err
	}
	defer tx.Rollback()
	batchID, err := newBatchID()
	if err != nil {
		return BulkOpBatch{}, err
	}
	if err := insertBatchTx(ctx, tx, batchID, opKindMove, now); err != nil {
		return BulkOpBatch{}, err
	}
	moved, err := applyFileMovesTx(ctx, tx, batchID, plan)
	if err != nil {
		return BulkOpBatch{}, err
	}
	if err := tx.Commit(); err != nil {
		return BulkOpBatch{}, compensateFileMoves(err, plan[:moved])
	}
	for _, item := range plan[:moved] {
		if eventErr := s.appendMediaEventLocked(ctx, MediaEventUpdated, item.mediaID, nil, now); eventErr != nil {
			slog.Warn("media outbox event append failed", "kind", MediaEventUpdated, "media", item.mediaID, "error", eventErr)
		}
	}
	return BulkOpBatch{ID: batchID, Kind: opKindMove, Affected: moved}, nil
}

// MoveManagedPath moves one folder/file to an existing destination folder under
// the same visible Library root.
func (s *Store) MoveManagedPath(ctx context.Context, libraryID LibraryID, path, destinationParentPath string, filter LibraryFilter) (ManagedPath, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ManagedPath{}, err
	}
	source, root, err := s.managedExistingPathLocked(ctx, libraryID, path, filter)
	if err != nil {
		return ManagedPath{}, err
	}
	if source.Path == root {
		return ManagedPath{}, ValidationError("library root cannot be moved")
	}
	parent, parentRoot, err := s.managedExistingPathLocked(ctx, libraryID, destinationParentPath, filter)
	if err != nil {
		return ManagedPath{}, err
	}
	if parentRoot != root {
		return ManagedPath{}, ValidationError("destination must be under the same library root")
	}
	if parent.Kind != "folder" {
		return ManagedPath{}, ValidationError("destination path is not a folder")
	}
	if source.Kind == "folder" && pathWithinRoot(source.Path, parent.Path) {
		return ManagedPath{}, ValidationError("folder cannot be moved into itself")
	}
	target := filepath.Join(parent.Path, filepath.Base(source.Path))
	return s.renameManagedPathLocked(ctx, source, root, target)
}

// DeleteManagedPath moves one folder/file into the Library-root-local managed
// trash and marks matching media_file records missing. RestoreManagedPath is the
// named recovery path.
func (s *Store) DeleteManagedPath(ctx context.Context, libraryID LibraryID, path string, filter LibraryFilter, now time.Time) (ManagedTrash, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ManagedTrash{}, err
	}
	source, root, err := s.managedExistingPathLocked(ctx, libraryID, path, filter)
	if err != nil {
		return ManagedTrash{}, err
	}
	if source.Path == root {
		return ManagedTrash{}, ValidationError("library root cannot be deleted")
	}
	trashID, err := newManagedTrashID()
	if err != nil {
		return ManagedTrash{}, err
	}
	trashRoot := managedTrashRoot(root)
	trashDir := filepath.Join(trashRoot, trashID)
	trashPath := filepath.Join(trashDir, "entry")
	move, err := s.prepareManagedMove(source.Path, trashPath)
	if err != nil {
		return ManagedTrash{}, err
	}
	if err := os.MkdirAll(trashDir, 0o700); err != nil {
		return ManagedTrash{}, err
	}
	moved := false
	defer func() {
		if !moved {
			_ = os.RemoveAll(trashDir)
		}
	}()
	// Record the manifest BEFORE the rename commits the move (hard invariant 6).
	// writeManagedTrashManifest fsyncs the manifest and its directory, so the
	// record is durable before the rename is even issued and a crash can only land
	// on a recoverable state: either the entry is still at its original path (the
	// manifest is a dangling record — restore refuses it non-destructively and the
	// sweep reclaims it once expired), or it is in the trash WITH the manifest that
	// makes it restorable. The reverse order left a window in which a renamed entry
	// had no manifest at all: invisible to both RestoreManagedPath and the sweep,
	// i.e. an unrecoverable orphan.
	manifest := managedTrashManifest{
		ID:           trashID,
		OriginalPath: source.Path,
		TrashPath:    trashPath,
		Kind:         source.Kind,
		DeletedAt:    now.UTC(),
	}
	if err := writeManagedTrashManifest(trashDir, manifest); err != nil {
		return ManagedTrash{}, err
	}
	if err := executeManagedMove(move, source.Path, trashPath); err != nil {
		return ManagedTrash{}, err
	}
	moved = true
	// unwind returns the entry to its original path when a later step fails. When
	// that succeeds the manifest describes an entry that is no longer trashed, so
	// clearing moved lets the deferred cleanup discard the whole record; when it
	// fails the entry is still in the trash and the record MUST survive.
	unwind := func() {
		if err := executeManagedMove(move, trashPath, source.Path); err == nil {
			moved = false
		}
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		unwind()
		return ManagedTrash{}, err
	}
	defer tx.Rollback()
	if err := markManagedMediaMissingTx(ctx, tx, source.Path, source.Kind, now); err != nil {
		unwind()
		return ManagedTrash{}, err
	}
	if err := tx.Commit(); err != nil {
		unwind()
		return ManagedTrash{}, err
	}
	return ManagedTrash{TrashID: trashID, OriginalPath: source.Path, TrashPath: trashPath, Kind: source.Kind}, nil
}

// SweepManagedFileTrash purges on-disk managed-file trash entries whose
// retention window has expired, mirroring the catalog purge (PurgeTrashedBefore)
// for the .beamfall-trash/managed-fileops/ directories DeleteManagedPath writes.
// An entry is purged only when its manifest DeletedAt + TrashRetention is at or
// before now — an in-window (not-yet-expired) entry is never touched, preserving
// the trash-enabled recoverable-by-default semantics (hard invariant 6). With
// trash disabled the sweep refuses with ErrTrashDisabledConfirmationRequired,
// skipped is true, and the shared trash-disabled skip counter increments once
// for the sweep. The irreversible purge is audited once with the
// affected count via the existing ActivityTrashPurge mutation path. Unreadable
// or malformed manifests are left in place: a purge must never destroy an entry
// it cannot prove is expired.
func (s *Store) SweepManagedFileTrash(ctx context.Context, now time.Time) (purged int, skipped bool, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, false, err
	}
	if s.trashDisabled {
		s.trashDisabledSweepSkips.Add(1)
		return 0, true, ErrTrashDisabledConfirmationRequired
	}
	now = now.UTC()
	roots, err := s.allLibraryRootsLocked(ctx)
	if err != nil {
		return 0, false, err
	}
	for _, root := range roots {
		removed, err := purgeExpiredManagedTrash(managedTrashRoot(root), now)
		if err != nil {
			return purged, false, err
		}
		purged += removed
	}
	if purged > 0 {
		if err := appendActivitySQL(ctx, s.eventsW, ActivityEntry{
			OccurredAt: now,
			Action:     ActivityTrashPurge,
			TargetType: "managed_file_trash",
			TargetID:   strconv.Itoa(purged),
		}); err != nil {
			return purged, false, err
		}
	}
	return purged, false, nil
}

// allLibraryRootsLocked returns the canonicalized existing roots of every
// non-trashed Library, skipping configured roots that no longer exist on disk.
func (s *Store) allLibraryRootsLocked(ctx context.Context) ([]string, error) {
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(libraries))
	for _, library := range libraries {
		configured, err := libraryRootsSQL(ctx, s.catalogR, library.ID)
		if err != nil {
			return nil, err
		}
		for _, raw := range configured {
			root, err := canonicalExistingPath(raw)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return nil, err
			}
			out = append(out, root)
		}
	}
	return out, nil
}

// purgeExpiredManagedTrash removes every managed-trash entry directory under
// trashRoot whose manifest DeletedAt + TrashRetention is at or before now, and
// returns the count removed. An in-window entry is skipped (its window has not
// expired), and an entry with an unreadable/malformed manifest is left in place
// so the purge never destroys what it cannot prove is expired.
//
// The enumeration and every removal run through ONE descriptor on the trash
// root, and each candidate must still be a real DIRECTORY (never a symlink) at
// removal time. A recursive delete is the most destructive step in this file, so
// it is the one that must never follow a name: a trash entry replaced by a link
// to a Library folder between the listing and the purge would otherwise delete
// live media.
func purgeExpiredManagedTrash(trashRoot string, now time.Time) (int, error) {
	root, err := openManagedDir(trashRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	purged := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifest, err := readManagedTrashManifest(filepath.Join(trashRoot, entry.Name()))
		if err != nil {
			continue
		}
		if manifest.DeletedAt.Add(TrashRetention).After(now) {
			continue
		}
		current, err := root.Lstat(entry.Name())
		if err != nil || !current.IsDir() {
			continue
		}
		if err := root.RemoveAll(entry.Name()); err != nil {
			return purged, err
		}
		purged++
	}
	return purged, nil
}

// RestoreManagedPath restores a prior DeleteManagedPath result by trash id.
func (s *Store) RestoreManagedPath(ctx context.Context, libraryID LibraryID, trashID string, filter LibraryFilter, now time.Time) (ManagedPath, error) {
	if err := validateManagedTrashID(trashID); err != nil {
		return ManagedPath{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ManagedPath{}, err
	}
	roots, err := s.visibleLibraryRootsLocked(ctx, libraryID, filter)
	if err != nil {
		return ManagedPath{}, err
	}
	var manifest managedTrashManifest
	var trashDir string
	var root string
	for _, candidateRoot := range roots {
		candidateDir := filepath.Join(managedTrashRoot(candidateRoot), trashID)
		candidateManifest, err := readManagedTrashManifest(candidateDir)
		if err == nil {
			manifest = candidateManifest
			trashDir = candidateDir
			root = candidateRoot
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return ManagedPath{}, err
		}
	}
	if manifest.ID == "" {
		return ManagedPath{}, ValidationError("managed trash entry not found")
	}
	if !pathWithinRoot(root, manifest.OriginalPath) || isManagedTrashInternal(root, manifest.OriginalPath) {
		return ManagedPath{}, ValidationError("restore target escapes the library root")
	}
	if _, err := os.Lstat(manifest.OriginalPath); err == nil {
		return ManagedPath{}, ValidationError("restore target already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return ManagedPath{}, err
	}
	move, err := s.prepareManagedMove(manifest.TrashPath, manifest.OriginalPath)
	if err != nil {
		return ManagedPath{}, err
	}
	if err := os.MkdirAll(filepath.Dir(manifest.OriginalPath), 0o755); err != nil {
		return ManagedPath{}, err
	}
	if err := executeManagedMove(move, manifest.TrashPath, manifest.OriginalPath); err != nil {
		return ManagedPath{}, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		_ = executeManagedMove(move, manifest.OriginalPath, manifest.TrashPath)
		return ManagedPath{}, err
	}
	defer tx.Rollback()
	if err := restoreManagedMediaTx(ctx, tx, manifest.OriginalPath, manifest.Kind, now); err != nil {
		_ = executeManagedMove(move, manifest.OriginalPath, manifest.TrashPath)
		return ManagedPath{}, err
	}
	if err := tx.Commit(); err != nil {
		_ = executeManagedMove(move, manifest.OriginalPath, manifest.TrashPath)
		return ManagedPath{}, err
	}
	_ = os.RemoveAll(trashDir)
	return ManagedPath{Path: manifest.OriginalPath, Kind: manifest.Kind}, nil
}

func (s *Store) renameManagedPathLocked(ctx context.Context, source ManagedPath, root, target string) (ManagedPath, error) {
	if source.Path == target {
		return source, nil
	}
	if err := validateManagedTarget(root, target); err != nil {
		return ManagedPath{}, err
	}
	move, err := s.prepareManagedMove(source.Path, target)
	if err != nil {
		return ManagedPath{}, err
	}
	if err := executeManagedMove(move, source.Path, target); err != nil {
		return ManagedPath{}, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		_ = executeManagedMove(move, target, source.Path)
		return ManagedPath{}, err
	}
	defer tx.Rollback()
	if err := updateManagedMediaPathsTx(ctx, tx, source.Path, target, source.Kind); err != nil {
		_ = executeManagedMove(move, target, source.Path)
		return ManagedPath{}, err
	}
	if err := tx.Commit(); err != nil {
		_ = executeManagedMove(move, target, source.Path)
		return ManagedPath{}, err
	}
	return ManagedPath{Path: target, Kind: source.Kind}, nil
}

func (s *Store) managedExistingPathLocked(ctx context.Context, libraryID LibraryID, path string, filter LibraryFilter) (ManagedPath, string, error) {
	roots, err := s.visibleLibraryRootsLocked(ctx, libraryID, filter)
	if err != nil {
		return ManagedPath{}, "", err
	}
	selected, rootOK, err := containedFolderRoot(path, roots)
	if err != nil {
		return ManagedPath{}, "", err
	}
	if !rootOK {
		return ManagedPath{}, "", ValidationError("path is outside the library roots")
	}
	for _, root := range roots {
		if pathWithinRoot(root, selected) {
			if isManagedTrashInternal(root, selected) {
				return ManagedPath{}, "", ValidationError("managed trash internals cannot be mutated")
			}
			info, err := os.Lstat(selected)
			if err != nil {
				return ManagedPath{}, "", err
			}
			kind := "file"
			if info.IsDir() {
				kind = "folder"
			}
			return ManagedPath{Path: selected, Kind: kind}, root, nil
		}
	}
	return ManagedPath{}, "", ValidationError("path is outside the library roots")
}

func (s *Store) mediaForManagedFilePath(ctx context.Context, libraryID LibraryID, path string) (MediaID, string, error) {
	var mediaID MediaID
	var title string
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT m.id, m.title
		FROM media m
		JOIN media_file f ON f.media_id = m.id AND f.path = ? AND f.missing_at_ms IS NULL
		WHERE m.library_id = ? AND m.trashed_at_ms IS NULL`+mediaExcludeExtrasClause+`
		ORDER BY m.id
		LIMIT 1
	`, path, libraryID).Scan(&mediaID, &title)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ValidationError("managed path is not cataloged media")
	}
	return mediaID, title, err
}

func (s *Store) visibleLibraryRootsLocked(ctx context.Context, libraryID LibraryID, filter LibraryFilter) ([]string, error) {
	visible, err := s.libraryVisibleLocked(ctx, libraryID, filter)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, ValidationError("unknown library")
	}
	roots, err := libraryRootsSQL(ctx, s.catalogR, libraryID)
	if err != nil {
		return nil, err
	}
	if len(roots) == 0 {
		return nil, ValidationError("library has no roots")
	}
	out := make([]string, 0, len(roots))
	for _, configured := range roots {
		root, err := canonicalExistingPath(configured)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		out = append(out, root)
	}
	if len(out) == 0 {
		return nil, ValidationError("library has no existing roots")
	}
	return out, nil
}

func validateManagedName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ValidationError("name is required")
	}
	if strings.ContainsRune(name, 0) || strings.ContainsAny(name, `/\`) {
		return ValidationError("name must be a single path segment")
	}
	if name == "." || name == ".." || name == managedFileTrashDir || filepath.Base(name) != name {
		return ValidationError("name must be a safe path segment")
	}
	return nil
}

func validateManagedTarget(root, target string) error {
	target = filepath.Clean(target)
	if !pathWithinRoot(root, target) {
		return ValidationError("target escapes the library root")
	}
	if isManagedTrashInternal(root, target) {
		return ValidationError("managed trash internals cannot be mutated")
	}
	return nil
}

func isManagedTrashInternal(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == managedFileTrashDir || strings.HasPrefix(rel, managedFileTrashDir+string(filepath.Separator))
}

func managedTrashRoot(root string) string {
	return filepath.Join(root, managedFileTrashDir, "managed-fileops")
}

// writeManagedTrashManifest records the trash manifest atomically and durably:
// the JSON is written to a sibling temp in the entry's own directory, fsynced,
// renamed over manifest.json, and the directory fsynced so the name is durable
// too. A crash therefore never exposes a truncated manifest — manifest.json is
// either absent or complete. A plain os.WriteFile could leave malformed JSON that
// readManagedTrashManifest rejects, which SweepManagedFileTrash then refuses to
// purge and RestoreManagedPath cannot read: neither restorable nor purgeable.
//
// The temp write, the rename, and the cleanup all run through one descriptor on
// the entry directory, so the durable record lands in the directory that was
// proved — a trash entry directory replaced mid-write cannot capture the
// manifest, and the rollback removal cannot reach outside it.
func writeManagedTrashManifest(dir string, manifest managedTrashManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	root, err := openManagedDir(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	const tmpName = ".manifest.json.tmp"
	if err := root.Remove(tmpName); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := writeRootFileSync0600(root, tmpName, data); err != nil {
		_ = root.Remove(tmpName)
		return err
	}
	if err := verifyManagedDirUnreplaced(root, dir); err != nil {
		_ = root.Remove(tmpName)
		return err
	}
	if err := root.Rename(tmpName, "manifest.json"); err != nil {
		_ = root.Remove(tmpName)
		return err
	}
	return syncDir(dir)
}

// writeRootFileSync0600 creates one 0600 file inside root and fsyncs it before
// returning, so the caller's rename publishes durable content. O_EXCL keeps the
// create fail-closed against a planted entry at the temp name.
func writeRootFileSync0600(root *os.Root, name string, data []byte) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// readManagedTrashManifest reads one entry's manifest through a descriptor on
// its directory. A manifest whose name is a symlink is refused rather than
// followed: the restore path acts on the OriginalPath it finds there, so a
// substituted manifest would redirect a restore to a name the delete never
// recorded.
func readManagedTrashManifest(dir string) (managedTrashManifest, error) {
	root, err := openManagedDir(dir)
	if err != nil {
		return managedTrashManifest{}, err
	}
	defer root.Close()
	entry, err := root.Lstat("manifest.json")
	if err != nil {
		return managedTrashManifest{}, err
	}
	if !entry.Mode().IsRegular() {
		return managedTrashManifest{}, errManagedPathIdentity
	}
	data, err := root.ReadFile("manifest.json")
	if err != nil {
		return managedTrashManifest{}, err
	}
	var manifest managedTrashManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return managedTrashManifest{}, err
	}
	if manifest.ID == "" || manifest.OriginalPath == "" || manifest.TrashPath == "" || manifest.Kind == "" {
		return managedTrashManifest{}, ValidationError("managed trash manifest is invalid")
	}
	return manifest, nil
}

func validateManagedTrashID(id string) error {
	if id == "" {
		return ValidationError("trashID is required")
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return ValidationError("trashID is invalid")
	}
	return nil
}

func newManagedTrashID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func updateManagedMediaPathsTx(ctx context.Context, tx *sql.Tx, oldPath, newPath, kind string) error {
	pairs, err := managedMediaPathPairs(ctx, tx, oldPath, newPath, kind)
	if err != nil {
		return err
	}
	for old, next := range pairs {
		if _, err := tx.ExecContext(ctx, `UPDATE media_file SET path = ? WHERE path = ?`, next, old); err != nil {
			return err
		}
	}
	return nil
}

func markManagedMediaMissingTx(ctx context.Context, tx *sql.Tx, path, kind string, now time.Time) error {
	paths, err := managedMediaPaths(ctx, tx, path, kind, true)
	if err != nil {
		return err
	}
	for _, mediaPath := range paths {
		if _, err := tx.ExecContext(ctx, `UPDATE media_file SET missing_at_ms = ? WHERE path = ?`, timeToMillis(now.UTC()), mediaPath); err != nil {
			return err
		}
	}
	return nil
}

func restoreManagedMediaTx(ctx context.Context, tx *sql.Tx, path, kind string, _ time.Time) error {
	paths, err := managedMediaPaths(ctx, tx, path, kind, false)
	if err != nil {
		return err
	}
	for _, mediaPath := range paths {
		if _, err := tx.ExecContext(ctx, `UPDATE media_file SET missing_at_ms = NULL WHERE path = ?`, mediaPath); err != nil {
			return err
		}
	}
	return nil
}

func managedMediaPathPairs(ctx context.Context, tx *sql.Tx, oldPath, newPath, kind string) (map[string]string, error) {
	paths, err := managedMediaPaths(ctx, tx, oldPath, kind, true)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(paths))
	for _, path := range paths {
		if path == oldPath {
			out[path] = newPath
			continue
		}
		out[path] = filepath.Join(newPath, strings.TrimPrefix(path, oldPath+string(filepath.Separator)))
	}
	return out, nil
}

// managedMediaPaths returns the paths of media_file rows that match path exactly
// (for any kind) or under path as a directory prefix (for kind == "folder").
// available selects the availability state to match: true queries rows where
// missing_at_ms IS NULL (letting the media_file_available_path_uq partial index
// serve the lookup); false queries rows where missing_at_ms IS NOT NULL.
func managedMediaPaths(ctx context.Context, tx *sql.Tx, path, kind string, available bool) ([]string, error) {
	missingPredicate := `missing_at_ms IS NOT NULL`
	if available {
		missingPredicate = `missing_at_ms IS NULL`
	}
	prefix := path + string(filepath.Separator)
	var (
		rows *sql.Rows
		err  error
	)
	if kind == "folder" {
		rows, err = tx.QueryContext(ctx,
			`SELECT path FROM media_file WHERE `+missingPredicate+
				` AND (path = ? OR path LIKE ? ESCAPE '\')`,
			path, escapeSQLLike(prefix)+"%")
	} else {
		rows, err = tx.QueryContext(ctx,
			`SELECT path FROM media_file WHERE `+missingPredicate+` AND path = ?`,
			path)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var mediaPath string
		if err := rows.Scan(&mediaPath); err != nil {
			return nil, err
		}
		out = append(out, mediaPath)
	}
	return out, rows.Err()
}

// escapeSQLLike escapes '%' and '_' wildcards and the escape character '\' in s
// for use in a SQL LIKE pattern with ESCAPE '\'.
func escapeSQLLike(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, c := range s {
		switch c {
		case '\\', '%', '_':
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	return b.String()
}
