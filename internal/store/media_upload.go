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

var mediaUploadLocks sync.Map // id -> *sync.Mutex

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
	if err := os.Chmod(root, 0o700); err != nil {
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

// cleanUploadRelativeDir validates a client-relative folder ("Show/Season 1")
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
		if err := validateManagedName(part); err != nil {
			return "", ValidationError("relative folder must be safe path segments")
		}
	}
	return strings.Join(parts, "/"), nil
}

// CreateMediaUpload opens a staged upload session for one file.
func (s *Store) CreateMediaUpload(ctx context.Context, libraryID LibraryID, fileName, relativeDir string, size int64, filter LibraryFilter, now time.Time) (MediaUpload, error) {
	fileName = strings.TrimSpace(fileName)
	if err := validateManagedName(fileName); err != nil {
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
	sidecar := filepath.Join(staging, upload.ID+".json")
	if err := writeFileExclusive(sidecar, data); err != nil {
		return MediaUpload{}, err
	}
	if err := writeFileExclusive(filepath.Join(staging, upload.ID+".part"), nil); err != nil {
		_ = os.Remove(sidecar)
		return MediaUpload{}, err
	}
	return upload, nil
}

func writeFileExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
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

// sweepMediaUploads removes sessions idle past MediaUploadTTL and returns the
// count of open sessions and the bytes they still expect.
func sweepMediaUploads(staging string, now time.Time) (open int, reserved int64, err error) {
	entries, err := os.ReadDir(staging)
	if err != nil {
		return 0, 0, err
	}
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || validateMediaUploadID(id) != nil {
			continue
		}
		upload, err := readMediaUpload(staging, id)
		if err != nil || now.Sub(upload.lastActive) > MediaUploadTTL {
			removeMediaUploadFiles(staging, id)
			continue
		}
		open++
		reserved += upload.Size - upload.Offset
	}
	return open, reserved, nil
}

type stagedMediaUpload struct {
	MediaUpload
	lastActive time.Time
}

func readMediaUpload(staging, id string) (stagedMediaUpload, error) {
	data, err := os.ReadFile(filepath.Join(staging, id+".json"))
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
	if err != nil || !info.Mode().IsRegular() {
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
	f, err := os.OpenFile(filepath.Join(staging, id+".part"), os.O_WRONLY|os.O_APPEND, 0)
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
// admission is no-replace: a conflict keeps both files, fails, or, for
// MediaUploadReplace, moves the existing file to the managed trash first. The
// caller enqueues the scan that catalogs the file.
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

	staging, upload, root, dir, err := s.prepareMediaUploadCommit(ctx, id, destDir, filter, now)
	if err != nil {
		return MediaUploadCommit{}, err
	}
	result := MediaUploadCommit{Root: root}
	target := filepath.Join(dir, upload.FileName)
	if conflict == MediaUploadReplace {
		info, err := os.Lstat(target)
		switch {
		case err == nil && info.Mode().IsRegular():
			trash, err := s.DeleteManagedPath(ctx, upload.LibraryID, target, filter, now)
			if err != nil {
				return MediaUploadCommit{}, err
			}
			result.Trashed = &trash
		case err == nil:
			return MediaUploadCommit{}, ValidationError("only a regular file can be replaced")
		case !errors.Is(err, os.ErrNotExist):
			return MediaUploadCommit{}, err
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return MediaUploadCommit{}, err
	}
	part := filepath.Join(staging, id+".part")
	stem, ext := splitUploadName(upload.FileName)
	for n := 1; n <= mediaUploadMaxSuffix; n++ {
		name := upload.FileName
		if n > 1 {
			name = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		candidate := filepath.Join(dir, name)
		err := admitStagedFileNoReplace(part, candidate)
		if err == nil {
			_ = os.Remove(filepath.Join(staging, id+".json"))
			mediaUploadLocks.Delete(id)
			result.Path = candidate
			return result, nil
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

func splitUploadName(name string) (stem, ext string) {
	ext = filepath.Ext(name)
	if ext == name {
		return name, ""
	}
	return strings.TrimSuffix(name, ext), ext
}

// prepareMediaUploadCommit validates the session and destination and creates
// missing destination folders, refusing any existing component that is not a
// real directory.
func (s *Store) prepareMediaUploadCommit(ctx context.Context, id, destDir string, filter LibraryFilter, now time.Time) (staging string, upload MediaUpload, root, dir string, err error) {
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
	root, dir, err = s.resolveMediaUploadDestinationLocked(ctx, upload.LibraryID, destDir, filter)
	if err != nil {
		return
	}
	rel, _ := filepath.Rel(root, dir)
	current := root
	if rel != "." {
		for _, segment := range strings.Split(rel, string(filepath.Separator)) {
			if err = validateManagedName(segment); err != nil {
				return
			}
			current = filepath.Join(current, segment)
			info, statErr := os.Lstat(current)
			switch {
			case statErr == nil && info.IsDir():
			case statErr == nil:
				err = ValidationError("destination crosses a file or link")
				return
			case errors.Is(statErr, os.ErrNotExist):
				if mkErr := os.Mkdir(current, 0o755); mkErr != nil && !errors.Is(mkErr, os.ErrExist) {
					err = mkErr
					return
				}
				if info, statErr = os.Lstat(current); statErr != nil || !info.IsDir() {
					err = ValidationError("destination crosses a file or link")
					return
				}
			default:
				err = statErr
				return
			}
		}
	}
	return staging, upload, root, dir, nil
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
		return c[1], dir, nil
	}
	return "", "", ValidationError("destination is outside the library roots")
}
