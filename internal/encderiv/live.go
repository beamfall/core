package encderiv

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beamfall/core/internal/libencrypt"
)

// Live HLS on the encrypted plane (ADR-0236). The encoder reads plaintext from
// a run's loopback endpoint and uploads each init segment, media segment and
// playlist back to the same endpoint; core encrypts every upload as it arrives
// under a session key that lives only in memory.

const (
	// liveInitName and livePlaylistName are the muxer's fixed upload names.
	liveInitName     = "init.mp4"
	livePlaylistName = "live.m3u8"
	// Upload ceilings. An init segment is a few kilobytes and a media segment
	// a few seconds of video; the playlist grows with the segment count.
	liveInitLimit     = 4 << 20
	liveSegmentLimit  = 512 << 20
	livePlaylistLimit = 4 << 20
	// livePartSuffix marks a file still being written.
	livePartSuffix = ".part"
)

var (
	// liveSegmentName is the session engine's segment pattern.
	liveSegmentName = regexp.MustCompile(`^seg(\d{5}|[1-9]\d{5,8})\.m4s$`)
	// liveRungName bounds a rung to one URL- and path-safe component.
	liveRungName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	// ErrLiveSessionClosed is returned once the session key has been zeroed.
	ErrLiveSessionClosed = errors.New("encderiv: live session closed")
)

// LiveSession is one encrypted live session: its directory and the session key
// every file in it is encrypted under. The owner closes the session before
// removing its directory.
type LiveSession struct {
	dir       string
	libraryID string

	// mu guards the key and I/O and reader registration. It is never held during I/O.
	mu        sync.Mutex
	key       []byte
	files     map[*LiveFile]struct{}
	inFlight  sync.WaitGroup
	revoked   atomic.Bool
	closeOnce sync.Once

	// publish serialises the exists check and .part creation, so two uploads
	// of one name cannot both proceed.
	publish sync.Mutex
	rename  func(string, string) error
}

// NewLiveSession draws a fresh session key for the files under dir, which must
// be absolute. libraryID binds every file to the session's Library.
func NewLiveSession(dir, libraryID string) (*LiveSession, error) {
	if dir == "" || !filepath.IsAbs(dir) {
		return nil, errors.New("encderiv: live session directory must be absolute")
	}
	if libraryID == "" {
		return nil, errors.New("encderiv: live session needs a Library")
	}
	key, err := libencrypt.NewRandomKey()
	if err != nil {
		return nil, err
	}
	return &LiveSession{dir: filepath.Clean(dir), libraryID: libraryID, key: key, rename: os.Rename}, nil
}

// Dir is the session directory.
func (s *LiveSession) Dir() string { return s.dir }

// Revoke zeroes the session key and invalidates every reader immediately,
// including readers whose underlying file operations are still in progress.
func (s *LiveSession) Revoke() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked.Store(true)
	libencrypt.ZeroKey(s.key)
	s.key = nil
}

// Close revokes the session and drains readers, opens, and encrypted writes.
func (s *LiveSession) Close() {
	s.Revoke()
	s.closeOnce.Do(func() {
		s.mu.Lock()
		files := s.files
		s.files = nil
		s.mu.Unlock()
		for f := range files {
			f.end()
		}
		s.inFlight.Wait()
	})
}

// beginIO registers work before it can open a file or copy the key. Revoke
// closes admission under the same lock before Close waits for that work.
func (s *LiveSession) beginIO() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revoked.Load() {
		return false
	}
	s.inFlight.Add(1)
	return true
}

func (s *LiveSession) copyKey() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revoked.Load() {
		return nil, ErrLiveSessionClosed
	}
	return bytes.Clone(s.key), nil
}

// object binds a file to its Library and its place in the session, so a file
// moved to another name fails authentication.
func (s *LiveSession) object(rel string) libencrypt.ObjectContext {
	return libencrypt.ObjectContext{
		LibraryID:         s.libraryID,
		ObjectClass:       libencrypt.ObjectClassGeneratedAsset,
		ImmutableObjectID: "live/" + rel,
	}
}

// resolve turns a slash-separated relative name into a path inside the
// session directory.
func (s *LiveSession) resolve(rel string) (string, error) {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.Contains(rel, "\\") {
		return "", fmt.Errorf("encderiv: invalid live file name %q", rel)
	}
	for part := range strings.SplitSeq(rel, "/") {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, livePartSuffix) {
			return "", fmt.Errorf("encderiv: invalid live file name %q", rel)
		}
	}
	return filepath.Join(s.dir, filepath.FromSlash(rel)), nil
}

// Rel names path relative to the session directory, slash-separated, or
// reports false when path lies outside it.
func (s *LiveSession) Rel(path string) (string, bool) {
	rel, err := filepath.Rel(s.dir, filepath.Clean(path))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// encrypt writes src encrypted to f under the session key.
func (s *LiveSession) encrypt(f *os.File, src io.Reader, rel string) error {
	key, err := s.copyKey()
	if err != nil {
		return err
	}
	defer libencrypt.ZeroKey(key)
	return libencrypt.EncryptStreamV2(f, src, key, s.object(rel))
}

func (s *LiveSession) publishFile(part, path string) error {
	if s.revoked.Load() {
		return ErrLiveSessionClosed
	}
	if err := s.rename(part, path); err != nil {
		return err
	}
	if !s.revoked.Load() {
		return nil
	}
	// Revoke can run during rename. Roll back before Close finishes draining
	// this write, including when it replaced an older playlist.
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return errors.Join(ErrLiveSessionClosed, err)
	}
	return ErrLiveSessionClosed
}

// WriteFile replaces the file at rel with data, encrypted. Core uses it for
// the playlists it serves itself.
func (s *LiveSession) WriteFile(rel string, data []byte) error {
	if !s.beginIO() {
		return ErrLiveSessionClosed
	}
	defer s.inFlight.Done()
	path, err := s.resolve(rel)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*"+livePartSuffix)
	if err != nil {
		return err
	}
	tmp := f.Name()
	err = s.encrypt(f, bytes.NewReader(data), rel)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = s.publishFile(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// put stores one upload at rel: encrypted into rel.part, then renamed into
// place. A name already published, or one another upload is writing, is
// refused with errUploadExists before body is read (first writer wins).
func (s *LiveSession) put(rel string, body io.Reader) error {
	if !s.beginIO() {
		return ErrLiveSessionClosed
	}
	defer s.inFlight.Done()
	path, err := s.resolve(rel)
	if err != nil {
		return err
	}
	part := path + livePartSuffix
	s.publish.Lock()
	if _, err := os.Lstat(path); err == nil {
		s.publish.Unlock()
		return errUploadExists
	} else if !errors.Is(err, fs.ErrNotExist) {
		s.publish.Unlock()
		return err
	}
	f, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- part is a validated name inside the session's own directory
	s.publish.Unlock()
	if errors.Is(err, fs.ErrExist) {
		return errUploadExists
	}
	if err != nil {
		return err
	}
	err = s.encrypt(f, body, rel)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = s.publishFile(part, path)
	}
	if err != nil {
		_ = os.Remove(part)
	}
	return err
}

// Open returns a decrypting view of the file at rel. A file that fails
// authentication does not open.
func (s *LiveSession) Open(rel string) (*LiveFile, error) {
	return s.open(rel, libencrypt.NewDecryptReaderV2)
}

func (s *LiveSession) open(rel string, decrypt func(io.ReaderAt, int64, []byte, libencrypt.ObjectContext) (*libencrypt.DecryptReaderV2, error)) (*LiveFile, error) {
	if !s.beginIO() {
		return nil, ErrLiveSessionClosed
	}
	defer s.inFlight.Done()

	path, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}
	f, err := openLiveFile(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, os.ErrInvalid
	}
	key, err := s.copyKey()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	r, err := decrypt(f, info.Size(), key, s.object(rel))
	libencrypt.ZeroKey(key)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	size, err := r.Seek(0, io.SeekEnd)
	if err == nil {
		_, err = r.Seek(0, io.SeekStart)
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	lf := &LiveFile{owner: s, file: f, r: r, size: size, modTime: info.ModTime()}
	s.mu.Lock()
	if s.revoked.Load() {
		s.mu.Unlock()
		_ = f.Close()
		return nil, ErrLiveSessionClosed
	}
	if s.files == nil {
		s.files = make(map[*LiveFile]struct{})
	}
	s.files[lf] = struct{}{}
	s.mu.Unlock()
	return lf, nil
}

// LiveFile is a decrypted view of one live session file. Once its session
// closes, every read fails with ErrLiveSessionClosed.
type LiveFile struct {
	owner   *LiveSession
	mu      sync.Mutex
	ended   bool
	file    *os.File
	r       *libencrypt.DecryptReaderV2
	size    int64
	modTime time.Time
}

// Size is the plaintext length.
func (f *LiveFile) Size() int64 { return f.size }

// ModTime is when the ciphertext was published.
func (f *LiveFile) ModTime() time.Time { return f.modTime }

func (f *LiveFile) Read(p []byte) (int, error) {
	if f.owner.revoked.Load() {
		return 0, ErrLiveSessionClosed
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ended || f.owner.revoked.Load() {
		return 0, ErrLiveSessionClosed
	}
	n, err := f.r.Read(p)
	if f.owner.revoked.Load() {
		clear(p[:n])
		return 0, ErrLiveSessionClosed
	}
	return n, err
}

func (f *LiveFile) Seek(offset int64, whence int) (int64, error) {
	if f.owner.revoked.Load() {
		return 0, ErrLiveSessionClosed
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ended || f.owner.revoked.Load() {
		return 0, ErrLiveSessionClosed
	}
	return f.r.Seek(offset, whence)
}

// ReadAt reads at off without moving the Read offset.
func (f *LiveFile) ReadAt(p []byte, off int64) (int, error) {
	if f.owner.revoked.Load() {
		return 0, ErrLiveSessionClosed
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ended || f.owner.revoked.Load() {
		return 0, ErrLiveSessionClosed
	}
	pos, err := f.r.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	if _, err := f.r.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := io.ReadFull(f.r, p)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF
	}
	if _, seekErr := f.r.Seek(pos, io.SeekStart); err == nil {
		err = seekErr
	}
	if f.owner.revoked.Load() {
		clear(p[:n])
		return 0, ErrLiveSessionClosed
	}
	return n, err
}

// Close closes the underlying file. It ends the file before leaving the
// session's set, so a concurrent session Close never misses a live reader.
func (f *LiveFile) Close() error {
	f.mu.Lock()
	err := f.endLocked()
	f.mu.Unlock()
	f.owner.mu.Lock()
	delete(f.owner.files, f)
	f.owner.mu.Unlock()
	return err
}

// end closes the file for its session's Close.
func (f *LiveFile) end() {
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = f.endLocked()
}

func (f *LiveFile) endLocked() error {
	if f.ended {
		return nil
	}
	f.ended = true
	f.r = nil
	return f.file.Close()
}

// liveSink accepts one run's uploads into its rung directory.
type liveSink struct {
	session *LiveSession
	rung    string
}

func (k liveSink) uploadLimit(name string) (int64, bool) {
	switch {
	case name == liveInitName:
		return liveInitLimit, true
	case name == livePlaylistName:
		return livePlaylistLimit, true
	case liveSegmentName.MatchString(name):
		return liveSegmentLimit, true
	}
	return 0, false
}

func (k liveSink) upload(name string, body io.Reader) error {
	if name == livePlaylistName {
		// Core writes the playlists it serves; the muxer's own is discarded.
		_, err := io.Copy(io.Discard, body)
		return err
	}
	return k.session.put(k.rung+"/"+name, body)
}

// LiveRunConfig describes one encoder run of a live session.
type LiveRunConfig struct {
	// Session receives the run's uploads.
	Session *LiveSession
	// Rung is the run's rendition directory, one path component.
	Rung string
	// Open and Size are the plaintext source, as for a Job.
	Open Opener
	Size int64
	// TTL and IdleTimeout default to the package defaults. The idle timeout
	// counts encoder activity only (ADR-0236 D3).
	TTL         time.Duration
	IdleTimeout time.Duration

	now  func() time.Time
	peer peerChecker
}

// LiveRun is one encoder run's endpoint: the source the encoder reads and the
// sink it uploads to, under one token. Revoke ends both.
type LiveRun struct {
	ep *endpoint
}

// StartLiveRun opens a run's loopback endpoint.
func StartLiveRun(cfg LiveRunConfig) (*LiveRun, error) {
	switch {
	case cfg.Session == nil || cfg.Open == nil:
		return nil, errors.New("encderiv: live run is missing its session or source")
	case cfg.Size < 0:
		return nil, errors.New("encderiv: negative source size")
	case !liveRungName.MatchString(cfg.Rung):
		return nil, errors.New("encderiv: invalid live rung")
	}
	if cfg.TTL <= 0 {
		cfg.TTL = DefaultTTL
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = DefaultIdleTimeout
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	if cfg.peer == nil {
		cfg.peer = checkPeer
	}
	ep, err := startEndpoint(endpointConfig{
		open:     cfg.Open,
		size:     cfg.Size,
		deadline: cfg.now().Add(cfg.TTL),
		idle:     cfg.IdleTimeout,
		now:      cfg.now,
		peer:     cfg.peer,
		sink:     liveSink{session: cfg.Session, rung: cfg.Rung},
		rung:     cfg.Rung,
	})
	if err != nil {
		return nil, err
	}
	return &LiveRun{ep: ep}, nil
}

// SourceURL is the plaintext source the encoder reads. It carries the token.
func (r *LiveRun) SourceURL() string { return r.ep.URL() }

// OutputURL is the base the encoder uploads under. It carries the token.
func (r *LiveRun) OutputURL() string { return r.ep.OutputURL() }

// Port is the loopback port the encoder's sandbox must allow.
func (r *LiveRun) Port() int { return r.ep.Port() }

// Secret is the token, for redacting the URLs from diagnostics.
func (r *LiveRun) Secret() string { return r.ep.token }

// Revoke ends the token and waits for requests in flight; an upload cut off
// leaves no file behind.
func (r *LiveRun) Revoke() { r.ep.Revoke() }

// SignalRevoke invalidates the token without waiting for the endpoint's I/O.
func (r *LiveRun) SignalRevoke() { r.ep.signalRevoke() }

// WaitRevoked closes connections and waits for handlers after SignalRevoke.
func (r *LiveRun) WaitRevoked() { r.ep.waitRevoked() }

// Finish ends the token after the encoder has exited, once the uploads it
// already sent are stored (see endpoint.Drain).
func (r *LiveRun) Finish() { r.ep.Drain() }

// Failed is closed once the endpoint refused a request with the token, saw an
// upload fail, or expired.
func (r *LiveRun) Failed() <-chan struct{} { return r.ep.Failed() }

// Err is the endpoint's first failure, or nil.
func (r *LiveRun) Err() error { return r.ep.Err() }
