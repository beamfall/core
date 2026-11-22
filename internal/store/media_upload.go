package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Media upload sessions (CORE-3192) stage an admin's uploaded file in a
// server-owned directory under the data dir, outside every Library root, until
// it is complete and committed. Each session is an immutable JSON sidecar plus
// a ".part" data file whose size is the resume offset, so no catalog table is
// needed and an interrupted chunk resumes at exactly the bytes on disk.
const (
	mediaUploadStagingDirName = "media-upload-staging"
	// MaxMediaUploadBytes caps one uploaded file.
	MaxMediaUploadBytes int64 = 256 << 30
	// MaxMediaUploadSessions caps open sessions, so staging cannot grow without
	// bound even inside the size cap.
	MaxMediaUploadSessions = 512
	// MediaUploadTTL is how long an idle session survives.
	MediaUploadTTL          = 24 * time.Hour
	mediaUploadMinFreeBytes = 64 << 20
	mediaUploadMaxSuffix    = 999
)

// MediaUploadConflict says what a commit does when the destination name exists.
type MediaUploadConflict string

const (
	// MediaUploadKeepBoth lands the new file under a suffixed name.
	MediaUploadKeepBoth MediaUploadConflict = "keep"
	// MediaUploadReplace moves the existing file to the managed trash first.
	MediaUploadReplace MediaUploadConflict = "replace"
	// MediaUploadFail refuses the commit on a conflict.
	MediaUploadFail MediaUploadConflict = "fail"
)

// MediaUpload is one staged upload session.
type MediaUpload struct {
	ID          string    `json:"id"`
	LibraryID   LibraryID `json:"libraryID"`
	FileName    string    `json:"fileName"`
	RelativeDir string    `json:"relativeDir"`
	Size        int64     `json:"size"`
	CreatedAt   time.Time `json:"createdAt"`
	Offset      int64     `json:"-"`
}

// Complete reports whether every declared byte is staged.
func (u MediaUpload) Complete() bool { return u.Offset == u.Size }

// MediaUploadCommit is the result of placing one upload in a Library root.
type MediaUploadCommit struct {
	Path    string
	Root    string
	Trashed *ManagedTrash
}

// ErrMediaUploadOffset is returned when a chunk does not start at the staged
// offset; the caller reports the current offset so the client can resume.
var ErrMediaUploadOffset = errors.New("store: upload chunk does not start at the staged offset")

// ErrMediaUploadNotFound is returned for an unknown or expired session.
var ErrMediaUploadNotFound = errors.New("store: upload session not found")

var (
	mediaUploadLocks    sync.Map // id -> *sync.Mutex
	mediaUploadCreateMu sync.Mutex
)

func lockMediaUpload(id string) func() {
	m, _ := mediaUploadLocks.LoadOrStore(id, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *Store) mediaUploadStaging() (string, error) {
	if s.dataDir == "" {
		return "", errors.New("media upload staging requires a data dir")
	}
	root := filepath.Join(s.dataDir, mediaUploadStagingDirName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(root, 0o700); err != nil { // #nosec G302 -- a directory needs its execute bit; 0700 is owner-only
		return "", err
	}
	return root, nil
}

func validateMediaUploadID(id string) error {
	if len(id) != 32 {
		return ErrMediaUploadNotFound
	}
	if _, err := hex.DecodeString(id); err != nil {
		return ErrMediaUploadNotFound
	}
	return nil
}

// validateUploadSegment is validateManagedName plus a case-insensitive refusal
// of the managed trash directory, which a case-insensitive volume would
// otherwise resolve to.
func validateUploadSegment(name string) error {
	if err := validateManagedName(name); err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(name), managedFileTrashDir) {
		return ValidationError("name must be a safe path segment")
	}
	return nil
}

// rejectEncryptedUploadLibraryLocked refuses an Encrypted Library: staging and
// placement write plaintext, which such a Library must never hold.
func (s *Store) rejectEncryptedUploadLibraryLocked(ctx context.Context, libraryID LibraryID) error {
	var encrypted bool
	err := s.catalogR.QueryRowContext(ctx, `SELECT encrypted FROM library WHERE id = ?`, libraryID).Scan(&encrypted)
	if err != nil {
		return err
	}
	if encrypted {
		return ValidationError("uploads are unavailable for encrypted libraries")
	}
	return nil
}

// cleanUploadRelativeDir validates a client-relative folder ("Group/Part 1")
// segment by segment and returns it with forward slashes.
func cleanUploadRelativeDir(dir string) (string, error) {
	dir = strings.Trim(strings.ReplaceAll(strings.TrimSpace(dir), `\`, "/"), "/")
	if dir == "" {
		return "", nil
	}
	parts := strings.Split(dir, "/")
	if len(parts) > 16 {
		return "", ValidationError("relative folder is too deep")
	}
	for _, part := range parts {
		if err := validateUploadSegment(part); err != nil {
			return "", ValidationError("relative folder must be safe path segments")
		}
	}
	return strings.Join(parts, "/"), nil
}

// CreateMediaUpload opens a staged upload session for one file.
func (s *Store) CreateMediaUpload(ctx context.Context, libraryID LibraryID, fileName, relativeDir string, size int64, filter LibraryFilter, now time.Time) (MediaUpload, error) {
	fileName = strings.TrimSpace(fileName)
	if err := validateUploadSegment(fileName); err != nil {
		return MediaUpload{}, err
	}
	rel, err := cleanUploadRelativeDir(relativeDir)
	if err != nil {
		return MediaUpload{}, err
	}
	if size <= 0 {
		return MediaUpload{}, ValidationError("upload size must be positive")
	}
	if size > MaxMediaUploadBytes {
		return MediaUpload{}, ValidationError("upload exceeds the size limit")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return MediaUpload{}, err
	}
	if _, err := s.visibleLibraryRootsLocked(ctx, libraryID, filter); err != nil {
		return MediaUpload{}, err
	}
	if err := s.rejectEncryptedUploadLibraryLocked(ctx, libraryID); err != nil {
		return MediaUpload{}, err
	}
	// Creates serialize so two cannot both pass the session cap or free-space
	// check on one sweep's numbers.
	mediaUploadCreateMu.Lock()
	defer mediaUploadCreateMu.Unlock()
	staging, err := s.mediaUploadStaging()
	if err != nil {
		return MediaUpload{}, err
	}
	open, reserved, err := sweepMediaUploads(staging, now)
	if err != nil {
		return MediaUpload{}, err
	}
	if open >= MaxMediaUploadSessions {
		return MediaUpload{}, ValidationError("too many open uploads")
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(staging, &stat); err != nil {
		return MediaUpload{}, err
	}
	free := int64(stat.Bavail) * int64(stat.Bsize) //nolint:gosec // block counts fit int64 on supported filesystems
	if free-reserved-size < mediaUploadMinFreeBytes {
		return MediaUpload{}, ValidationError("not enough free space for this upload")
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return MediaUpload{}, err
	}
	upload := MediaUpload{
		ID: hex.EncodeToString(raw[:]), LibraryID: libraryID, FileName: fileName,
		RelativeDir: rel, Size: size, CreatedAt: now.UTC(),
	}
	data, err := json.Marshal(upload)
	if err != nil {
		return MediaUpload{}, err
	}
	// The .part goes first: a sidecar is only ever visible beside its data file.
	part := filepath.Join(staging, upload.ID+".part")
	if err := writeFileExclusive(part, nil); err != nil {
		return MediaUpload{}, err
	}
	if err := writeFileExclusive(filepath.Join(staging, upload.ID+".json"), data); err != nil {
		_ = os.Remove(part)
		return MediaUpload{}, err
	}
	return upload, nil
}

func writeFileExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- callers pass a path under the server-owned staging dir
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	return f.Close()
}

// SweepMediaUploads expires idle upload sessions; the periodic maintenance
// sweep calls it so a discarded upload does not wait for the next create.
func (s *Store) SweepMediaUploads(now time.Time) error {
	if s.dataDir == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	mediaUploadCreateMu.Lock()
	defer mediaUploadCreateMu.Unlock()
	staging, err := s.mediaUploadStaging()
	if err != nil {
		return err
	}
	_, _, err = sweepMediaUploads(staging, now)
	return err
}

// sweepMediaUploads removes sessions idle past MediaUploadTTL and returns the
// count of open sessions and the bytes they still expect.
func sweepMediaUploads(staging string, now time.Time) (open int, reserved int64, err error) {
	entries, err := os.ReadDir(staging)
	if err != nil {
		return 0, 0, err
	}
	for _, entry := range entries {
		if id, ok := strings.CutSuffix(entry.Name(), ".part"); ok && validateMediaUploadID(id) == nil {
			// A .part without its sidecar is a create that died between writes.
			if _, err := os.Lstat(filepath.Join(staging, id+".json")); errors.Is(err, os.ErrNotExist) {
				if info, err := entry.Info(); err == nil && now.Sub(info.ModTime()) > MediaUploadTTL {
					_ = os.Remove(filepath.Join(staging, entry.Name()))
				}
			}
			continue
		}
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || validateMediaUploadID(id) != nil {
			continue
		}
		upload, readErr := readMediaUpload(staging, id)
		if errors.Is(readErr, ErrMediaUploadNotFound) || (readErr == nil && now.Sub(upload.lastActive) > MediaUploadTTL) {
			removeMediaUploadFiles(staging, id)
			continue
		}
		if readErr != nil {
			err = errors.Join(err, readErr)
			continue
		}
		open++
		reserved += upload.Size - upload.Offset
	}
	return open, reserved, err
}

type stagedMediaUpload struct {
	MediaUpload
	lastActive time.Time
}

func readMediaUpload(staging, id string) (stagedMediaUpload, error) {
	data, err := os.ReadFile(filepath.Join(staging, id+".json")) // #nosec G304 -- id is a validated hex upload id under the staging dir
	if errors.Is(err, os.ErrNotExist) {
		return stagedMediaUpload{}, ErrMediaUploadNotFound
	}
	if err != nil {
		return stagedMediaUpload{}, err
	}
	var upload MediaUpload
	if err := json.Unmarshal(data, &upload); err != nil || upload.ID != id {
		return stagedMediaUpload{}, ErrMediaUploadNotFound
	}
	info, err := os.Lstat(filepath.Join(staging, id+".part"))
	if errors.Is(err, os.ErrNotExist) {
		return stagedMediaUpload{}, ErrMediaUploadNotFound
	}
	if err != nil {
		return stagedMediaUpload{}, err
	}
	if !info.Mode().IsRegular() {
		return stagedMediaUpload{}, ErrMediaUploadNotFound
	}
	upload.Offset = info.Size()
	return stagedMediaUpload{MediaUpload: upload, lastActive: info.ModTime()}, nil
}

func removeMediaUploadFiles(staging, id string) {
	_ = os.Remove(filepath.Join(staging, id+".part"))
	_ = os.Remove(filepath.Join(staging, id+".json"))
}

// mediaUploadLocked loads a session the filter may see. Callers hold s.mu.
func (s *Store) mediaUploadLocked(ctx context.Context, id string, filter LibraryFilter, now time.Time) (string, MediaUpload, error) {
	if err := validateMediaUploadID(id); err != nil {
		return "", MediaUpload{}, err
	}
	staging, err := s.mediaUploadStaging()
	if err != nil {
		return "", MediaUpload{}, err
	}
	upload, err := readMediaUpload(staging, id)
	if err != nil {
		return "", MediaUpload{}, err
	}
	if now.Sub(upload.lastActive) > MediaUploadTTL {
		removeMediaUploadFiles(staging, id)
		return "", MediaUpload{}, ErrMediaUploadNotFound
	}
	visible, err := s.libraryVisibleLocked(ctx, upload.LibraryID, filter)
	if err != nil {
		return "", MediaUpload{}, err
	}
	if !visible {
		return "", MediaUpload{}, ErrMediaUploadNotFound
	}
	return staging, upload.MediaUpload, nil
}

// MediaUploadStatus reports a session and its staged offset.
func (s *Store) MediaUploadStatus(ctx context.Context, id string, filter LibraryFilter, now time.Time) (MediaUpload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return MediaUpload{}, err
	}
	_, upload, err := s.mediaUploadLocked(ctx, id, filter, now)
	return upload, err
}

// AppendMediaUploadChunk writes body at offset, which must equal the staged
// offset. Bytes that arrive before an interrupted read stay staged, so the
// client resumes from the reported offset. A chunk that would pass the declared
// size is refused and truncated back.
func (s *Store) AppendMediaUploadChunk(ctx context.Context, id string, offset int64, body io.Reader, filter LibraryFilter, now time.Time) (MediaUpload, error) {
	if validateMediaUploadID(id) != nil {
		return MediaUpload{}, ErrMediaUploadNotFound
	}
	unlock := lockMediaUpload(id)
	defer unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return MediaUpload{}, err
	}
	staging, upload, err := s.mediaUploadLocked(ctx, id, filter, now)
	if err != nil {
		return MediaUpload{}, err
	}
	if offset != upload.Offset {
		return upload, ErrMediaUploadOffset
	}
	f, err := os.OpenFile(filepath.Join(staging, id+".part"), os.O_WRONLY|os.O_APPEND, 0) // #nosec G304 -- id is a validated hex upload id under the staging dir
	if err != nil {
		return upload, err
	}
	defer f.Close()
	remaining := upload.Size - upload.Offset
	n, copyErr := io.Copy(f, io.LimitReader(body, remaining+1))
	if n > remaining {
		_ = f.Truncate(upload.Offset)
		_ = f.Sync()
		return upload, ValidationError("chunk passes the declared upload size")
	}
	if err := f.Sync(); err != nil {
		return upload, err
	}
	upload.Offset += n
	if copyErr != nil {
		return upload, copyErr
	}
	return upload, nil
}

// CancelMediaUpload discards a session and its staged bytes.
func (s *Store) CancelMediaUpload(ctx context.Context, id string, filter LibraryFilter, now time.Time) error {
	if validateMediaUploadID(id) != nil {
		return ErrMediaUploadNotFound
	}
	unlock := lockMediaUpload(id)
	defer unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	staging, _, err := s.mediaUploadLocked(ctx, id, filter, now)
	if err != nil {
		return err
	}
	removeMediaUploadFiles(staging, id)
	mediaUploadLocks.Delete(id)
	return nil
}

// CommitMediaUpload places a complete upload in destDir, an absolute folder
// under a visible root of the session's Library, creating missing folders. The
// admission is no-replace: a conflict keeps both files or fails. For
// MediaUploadReplace the upload is first admitted beside the existing file,
// then the existing file moves to the managed trash and the upload takes its
// name; a failure on the way restores the original. Every folder and file
// operation resolves through a descriptor on the Library root, so a folder
// swapped for a link mid-commit cannot redirect the file outside it. The caller
// enqueues the scan that catalogs the file.
func (s *Store) CommitMediaUpload(ctx context.Context, id, destDir string, conflict MediaUploadConflict, filter LibraryFilter, now time.Time) (MediaUploadCommit, error) {
	switch conflict {
	case MediaUploadKeepBoth, MediaUploadReplace, MediaUploadFail:
	case "":
		conflict = MediaUploadKeepBoth
	default:
		return MediaUploadCommit{}, ValidationError("unknown conflict choice")
	}
	if validateMediaUploadID(id) != nil {
		return MediaUploadCommit{}, ErrMediaUploadNotFound
	}
	unlock := lockMediaUpload(id)
	defer unlock()

	staging, upload, rootPath, rel, err := s.prepareMediaUploadCommit(ctx, id, destDir, filter, now)
	if err != nil {
		return MediaUploadCommit{}, err
	}
	root, err := openManagedDir(rootPath)
	if err != nil {
		return MediaUploadCommit{}, err
	}
	defer root.Close()
	dir, err := ensureUploadFolders(root, rel)
	if err != nil {
		return MediaUploadCommit{}, err
	}
	defer dir.Close()
	part := filepath.Join(staging, id+".part")
	finish := func(name string, trashed *ManagedTrash) MediaUploadCommit {
		_ = os.Remove(part)
		_ = os.Remove(filepath.Join(staging, id+".json"))
		mediaUploadLocks.Delete(id)
		return MediaUploadCommit{Path: filepath.Join(rootPath, rel, name), Root: rootPath, Trashed: trashed}
	}

	if conflict == MediaUploadReplace {
		info, err := dir.Lstat(upload.FileName)
		switch {
		case err == nil && info.Mode().IsRegular():
			return s.replaceWithMediaUpload(ctx, dir, part, filepath.Join(rootPath, rel), upload, filter, now, finish)
		case err == nil:
			return MediaUploadCommit{}, ValidationError("only a regular file can be replaced")
		case !errors.Is(err, os.ErrNotExist):
			return MediaUploadCommit{}, err
		}
	}

	stem, ext := splitUploadName(upload.FileName)
	for n := 1; n <= mediaUploadMaxSuffix; n++ {
		name := upload.FileName
		if n > 1 {
			name = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		err := admitUploadIntoDir(dir, part, name)
		if err == nil {
			return finish(name, nil), nil
		}
		if !errors.Is(err, os.ErrExist) {
			return MediaUploadCommit{}, err
		}
		if conflict != MediaUploadKeepBoth {
			return MediaUploadCommit{}, ValidationError("destination already holds a file with this name")
		}
	}
	return MediaUploadCommit{}, ValidationError("no free name for this file in the destination")
}

// replaceWithMediaUpload admits the upload under a hidden temporary name, moves
// the existing file to the managed trash, then publishes the upload at the
// original name. If publishing fails the trashed file is restored, so a
// failed Replace never leaves the folder without the original.
func (s *Store) replaceWithMediaUpload(ctx context.Context, dir *os.Root, part, dirPath string, upload MediaUpload, filter LibraryFilter, now time.Time, finish func(string, *ManagedTrash) MediaUploadCommit) (MediaUploadCommit, error) {
	var token [8]byte
	if _, err := rand.Read(token[:]); err != nil {
		return MediaUploadCommit{}, err
	}
	tmp := "." + upload.FileName + ".upload-" + hex.EncodeToString(token[:])
	if err := admitUploadIntoDir(dir, part, tmp); err != nil {
		return MediaUploadCommit{}, err
	}
	trash, err := s.DeleteManagedPath(ctx, upload.LibraryID, filepath.Join(dirPath, upload.FileName), filter, now)
	if err != nil {
		_ = dir.Remove(tmp)
		return MediaUploadCommit{}, err
	}
	if err := publishMediaUploadTemp(dir, tmp, upload.FileName); err != nil {
		_ = dir.Remove(tmp)
		if _, restoreErr := s.RestoreManagedPath(ctx, upload.LibraryID, trash.TrashID, filter, now); restoreErr != nil {
			return MediaUploadCommit{}, fmt.Errorf("replace failed (%w); the original stays in trash %s: %w", err, trash.TrashID, restoreErr)
		}
		return MediaUploadCommit{}, err
	}
	_ = dir.Remove(tmp)
	return finish(upload.FileName, &trash), nil
}

// ensureUploadFolders creates the missing folders of rel under root, refusing
// any existing component that is not a real directory, and returns a
// descriptor on the destination folder. Each step resolves through root, so no
// component can lead outside it.
func ensureUploadFolders(root *os.Root, rel string) (*os.Root, error) {
	current := "."
	if rel != "." {
		for _, segment := range strings.Split(rel, string(filepath.Separator)) {
			current = filepath.Join(current, segment)
			info, err := root.Lstat(current)
			switch {
			case err == nil && info.IsDir():
				continue
			case err == nil:
				return nil, ValidationError("destination crosses a file or link")
			case !errors.Is(err, os.ErrNotExist):
				return nil, err
			}
			// Library folders must stay readable by the media server and its
			// scanners, like the scan-created ones.
			if err := root.Mkdir(current, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
				return nil, err
			}
			if info, err := root.Lstat(current); err != nil || !info.IsDir() {
				return nil, ValidationError("destination crosses a file or link")
			}
		}
	}
	return root.OpenRoot(current)
}

// admitUploadIntoDir places the staged part at name inside dir without
// replacing anything: a hard link from staging when both share a filesystem,
// else a copy into a sibling temp that is published whole. The part stays staged
// until the caller finishes, so a failed commit can be retried. EEXIST is the
// no-replace refusal.
func admitUploadIntoDir(dir *os.Root, part, name string) error {
	dirFile, err := dir.Open(".")
	if err != nil {
		return err
	}
	defer dirFile.Close()
	linkErr := unix.Linkat(unix.AT_FDCWD, part, int(dirFile.Fd()), name, 0) //nolint:gosec // descriptors fit int
	if linkErr == nil {
		return nil
	}
	if !errors.Is(linkErr, unix.EXDEV) && !mediaUploadLinkUnavailable(linkErr) {
		return &os.LinkError{Op: "link", Old: part, New: name, Err: linkErr}
	}
	src, err := os.Open(part) // #nosec G304 -- part is a validated upload id under the staging dir
	if err != nil {
		return err
	}
	defer src.Close()
	tmp, err := copyIntoRootTemp(dir, src, "."+name+".upload-")
	if err != nil {
		return err
	}
	defer func() { _ = dir.Remove(tmp) }()
	return publishMediaUploadTemp(dir, tmp, name)
}

func mediaUploadLinkUnavailable(err error) bool {
	return errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.ENOSYS) ||
		errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES)
}

func publishMediaUploadTemp(dir *os.Root, tmp, name string) error {
	if err := dir.Link(tmp, name); !mediaUploadLinkUnavailable(err) {
		return err
	}
	fd, err := dir.Open(".")
	if err != nil {
		return err
	}
	defer fd.Close()
	err = renameMediaUploadNoReplace(int(fd.Fd()), tmp, name) //nolint:gosec // descriptors fit int
	if !errors.Is(err, unix.ENOTSUP) && !errors.Is(err, unix.ENOSYS) && !errors.Is(err, unix.EINVAL) {
		return err
	}
	return publishMediaUploadOverReservation(dir, tmp, name)
}

// Some volumes (including exFAT drivers) lack both links and exclusive rename.
// Reserve the name atomically and rename the completed copy over our reservation.
func publishMediaUploadOverReservation(dir *os.Root, tmp, name string) error {
	reservation, err := dir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer reservation.Close()
	created, err := reservation.Stat()
	if err != nil {
		return err
	}
	ownsName := func() bool {
		current, err := dir.Lstat(name)
		return err == nil && os.SameFile(created, current)
	}
	if !ownsName() {
		return os.ErrExist
	}
	if err := dir.Rename(tmp, name); err != nil {
		if ownsName() {
			_ = dir.Remove(name)
		}
		return err
	}
	return nil
}

func splitUploadName(name string) (stem, ext string) {
	ext = filepath.Ext(name)
	if ext == name {
		return name, ""
	}
	return strings.TrimSuffix(name, ext), ext
}

// prepareMediaUploadCommit validates the session and destination and returns
// the canonical root and the destination relative to it.
func (s *Store) prepareMediaUploadCommit(ctx context.Context, id, destDir string, filter LibraryFilter, now time.Time) (staging string, upload MediaUpload, root, rel string, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err = s.guardLocked(); err != nil {
		return
	}
	staging, upload, err = s.mediaUploadLocked(ctx, id, filter, now)
	if err != nil {
		return
	}
	if !upload.Complete() {
		err = ValidationError("upload is not complete")
		return
	}
	if err = s.rejectEncryptedUploadLibraryLocked(ctx, upload.LibraryID); err != nil {
		return
	}
	var dir string
	root, dir, err = s.resolveMediaUploadDestinationLocked(ctx, upload.LibraryID, destDir, filter)
	if err != nil {
		return
	}
	rel, err = filepath.Rel(root, dir)
	if err != nil {
		return
	}
	if rel != "." {
		for _, segment := range strings.Split(rel, string(filepath.Separator)) {
			if err = validateUploadSegment(segment); err != nil {
				return
			}
		}
	}
	return staging, upload, root, rel, nil
}

// resolveMediaUploadDestinationLocked maps destDir onto a canonical visible
// root. destDir may be spelled through a configured (non-canonical) root, as
// catalog paths are.
func (s *Store) resolveMediaUploadDestinationLocked(ctx context.Context, libraryID LibraryID, destDir string, filter LibraryFilter) (string, string, error) {
	destDir = strings.TrimSpace(destDir)
	if destDir == "" || !filepath.IsAbs(destDir) {
		return "", "", ValidationError("destination must be an absolute folder")
	}
	destDir = filepath.Clean(destDir)
	canonical, err := s.visibleLibraryRootsLocked(ctx, libraryID, filter)
	if err != nil {
		return "", "", err
	}
	configured, err := libraryRootsSQL(ctx, s.catalogR, libraryID)
	if err != nil {
		return "", "", err
	}
	candidates := make([][2]string, 0, len(canonical)+len(configured))
	for _, root := range canonical {
		candidates = append(candidates, [2]string{root, root})
	}
	for _, conf := range configured {
		root, err := canonicalExistingPath(conf)
		if err != nil {
			continue
		}
		candidates = append(candidates, [2]string{filepath.Clean(conf), root})
	}
	for _, c := range candidates {
		if !pathWithinRoot(c[0], destDir) {
			continue
		}
		rel, err := filepath.Rel(c[0], destDir)
		if err != nil {
			continue
		}
		dir := filepath.Join(c[1], rel)
		if err := validateManagedTarget(c[1], dir); err != nil {
			return "", "", err
		}
		if err := s.rejectNestedLibraryRootLocked(ctx, libraryID, c[1], dir); err != nil {
			return "", "", err
		}
		return c[1], dir, nil
	}
	return "", "", ValidationError("destination is outside the library roots")
}

// rejectNestedLibraryRootLocked refuses a destination inside another Library's
// root that sits below this Library's root: placing or replacing there would
// act on the other Library under this one's permission and audit.
func (s *Store) rejectNestedLibraryRootLocked(ctx context.Context, libraryID LibraryID, root, dir string) error {
	rows, err := s.catalogR.QueryContext(ctx, `SELECT root FROM library_roots WHERE library_id <> ?`, libraryID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var configured string
		if err := rows.Scan(&configured); err != nil {
			return err
		}
		other, err := canonicalExistingPath(configured)
		if err != nil {
			continue
		}
		if other != root && pathWithinRoot(root, other) && pathWithinRoot(other, dir) {
			return ValidationError("destination is inside another library")
		}
		// Spelling comparison misses a case-insensitive volume, where "other"
		// and "Other" name one folder: compare the existing ancestors by identity.
		if destinationReachesFolder(root, dir, other) {
			return ValidationError("destination is inside another library")
		}
	}
	return rows.Err()
}

// destinationReachesFolder reports whether dir or one of its existing
// ancestors strictly below root is the same file as folder.
func destinationReachesFolder(root, dir, folder string) bool {
	target, err := os.Stat(folder)
	if err != nil || !target.IsDir() {
		return false
	}
	if rootInfo, err := os.Stat(root); err == nil && os.SameFile(rootInfo, target) {
		return false
	}
	for p := filepath.Clean(dir); p != root && pathWithinRoot(root, p); p = filepath.Dir(p) {
		if info, err := os.Stat(p); err == nil && os.SameFile(info, target) {
			return true
		}
	}
	return false
}
