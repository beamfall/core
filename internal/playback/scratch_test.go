package playback

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/encderiv"
	"github.com/beamfall/core/internal/ffmpeg"
	"golang.org/x/sys/unix"
)

func newTestSessions(t testing.TB, transcoder Transcoder, dir string, now func() time.Time) *Sessions {
	t.Helper()
	s, err := NewSessions(transcoder, dir, now)
	return ownTestSessions(t, s, err)
}

func newTestSessionsInDir(t testing.TB, transcoder Transcoder, dir string, now func() time.Time) *Sessions {
	t.Helper()
	s, err := NewSessionsInDir(transcoder, dir, now)
	return ownTestSessions(t, s, err)
}

func ownTestSessions(t testing.TB, s *Sessions, err error) *Sessions {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

// Bare-path helpers support fixtures that control the session lifetime. Use
// LiveArtifactShaped when the test exercises concurrent teardown.
func (s *Sessions) FilePath(ctx context.Context, key SessionKey, src string, durationSeconds, videoWidth, videoHeight int, audioCopy bool, name string) (string, error) {
	return s.FilePathPriority(ctx, key, src, durationSeconds, videoWidth, videoHeight, audioCopy, false, VideoSignal{}, PriorityNormal, name)
}

func (s *Sessions) FilePathPriority(ctx context.Context, key SessionKey, src string, durationSeconds, videoWidth, videoHeight int, audioCopy, degraded bool, sourceVideo VideoSignal, priority Priority, name string) (string, error) {
	return s.FilePathPriorityWithBurnSubtitle(ctx, key, src, durationSeconds, videoWidth, videoHeight, audioCopy, degraded, sourceVideo, -1, priority, name)
}

func (s *Sessions) FilePathPriorityWithBurnSubtitle(ctx context.Context, key SessionKey, src string, durationSeconds, videoWidth, videoHeight int, audioCopy, degraded bool, sourceVideo VideoSignal, burnSubtitleIndex int, priority Priority, name string) (string, error) {
	return s.FilePathPriorityWithBurnSubtitleAudio(ctx, key, src, durationSeconds, videoWidth, videoHeight, audioCopy, degraded, sourceVideo, burnSubtitleIndex, ffmpeg.AudioProfile{}, priority, name)
}

func (s *Sessions) FilePathPriorityWithBurnSubtitleAudio(ctx context.Context, key SessionKey, src string, durationSeconds, videoWidth, videoHeight int, audioCopy, degraded bool, sourceVideo VideoSignal, burnSubtitleIndex int, audioProfile ffmpeg.AudioProfile, priority Priority, name string) (string, error) {
	return s.FilePathPriorityWithBurnSubtitleAudioLadder(ctx, key, src, durationSeconds, videoWidth, videoHeight, audioCopy, degraded, ladderCaps{}, sourceVideo, burnSubtitleIndex, audioProfile, priority, name)
}

func (s *Sessions) FilePathPriorityWithBurnSubtitleAudioLadder(ctx context.Context, key SessionKey, src string, durationSeconds, videoWidth, videoHeight int, audioCopy, degraded bool, caps ladderCaps, sourceVideo VideoSignal, burnSubtitleIndex int, audioProfile ffmpeg.AudioProfile, priority Priority, name string) (string, error) {
	return s.FilePathPriorityWithBurnSubtitleAudioLadderMap(ctx, key, src, durationSeconds, videoWidth, videoHeight, audioCopy, degraded, caps, sourceVideo, burnSubtitleIndex, audioProfile, nil, priority, name)
}

func (s *Sessions) FilePathPriorityWithBurnSubtitleAudioLadderMap(ctx context.Context, key SessionKey, src string, durationSeconds, videoWidth, videoHeight int, audioCopy, degraded bool, caps ladderCaps, sourceVideo VideoSignal, burnSubtitleIndex int, audioProfile ffmpeg.AudioProfile, audioStreamIndex *int, priority Priority, name string) (string, error) {
	return s.FilePathShaped(ctx, key, src, durationSeconds, videoWidth, videoHeight, audioCopy, degraded, caps, sourceVideo, burnSubtitleIndex, audioProfile, audioStreamIndex, StreamShape{}, priority, name)
}

func (s *Sessions) FilePathShaped(ctx context.Context, key SessionKey, src string, durationSeconds, videoWidth, videoHeight int, audioCopy, degraded bool, caps ladderCaps, sourceVideo VideoSignal, burnSubtitleIndex int, audioProfile ffmpeg.AudioProfile, audioStreamIndex *int, shape StreamShape, priority Priority, name string) (string, error) {
	artifact, err := s.LiveArtifactShaped(ctx, key, src, durationSeconds, videoWidth, videoHeight, audioCopy, degraded, caps, sourceVideo, burnSubtitleIndex, audioProfile, audioStreamIndex, shape, priority, name)
	if err != nil {
		return "", err
	}
	defer artifact.Release()
	return artifact.path, nil
}

func testArtifactForPath(s *Sessions, key SessionKey, path string) (*LiveArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.sessions[key]
	if h == nil {
		return nil, ErrSessionEvicted
	}
	h.scratch.users++
	return &LiveArtifact{manager: s, session: h, path: path}, nil
}

func requireScratchMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scratch %s still present: %v", path, err)
	}
}

func (s *Sessions) removeScratchFixture(path string) error {
	return os.RemoveAll(filepath.Join(s.dir, filepath.Base(path)))
}

func TestNewSessionsCreatesDefaultAndValidatesExplicitRoot(t *testing.T) {
	data := t.TempDir()
	def := filepath.Join(data, "transcode")
	requireScratchMissing(t, def)
	newTestSessions(t, nil, data, nil)
	if info, err := os.Stat(def); err != nil || !info.IsDir() {
		t.Fatalf("default scratch = %v, %v", info, err)
	}

	missing := filepath.Join(data, "missing")
	if s, err := NewSessionsInDir(nil, missing, nil); err == nil {
		_ = s.Close()
		t.Fatal("constructor accepted a missing explicit root")
	}
	requireScratchMissing(t, missing)
	file := filepath.Join(data, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := NewSessionsInDir(nil, file, nil); err == nil {
		_ = s.Close()
		t.Fatal("constructor accepted a regular file as the explicit root")
	}
}

func TestNewSessionsUsesLexicalDataDirForDefaultScratch(t *testing.T) {
	dataDir := t.TempDir()
	otherDir := t.TempDir()
	child := filepath.Join(otherDir, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dataDir, "alias")
	if err := os.Symlink(child, alias); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(otherDir, "transcode", "session-42", "marker")
	seedScratchFile(t, marker)

	// The default follows Store's lexical data path before resolving scratch.
	configured := alias + "/.."
	s := newTestSessions(t, nil, configured, nil)
	want, err := filepath.EvalSymlinks(filepath.Join(dataDir, "transcode"))
	if err != nil {
		t.Fatal(err)
	}
	if s.ScratchDir() != want {
		t.Fatalf("locked root = %q, want %q", s.ScratchDir(), want)
	}
	if resolved, err := ResolveDefaultScratchDir(configured); err != nil || resolved != want {
		t.Fatalf("reported default root = %q, %v, want %q", resolved, err, want)
	}
	if got := string(mustRead(t, marker)); got != "stale" {
		t.Fatalf("unrelated scratch marker changed: %q", got)
	}
}

func TestNewSessionsRejectsEmptyExplicitRootBeforeSweeping(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	marker := filepath.Join(dir, "session-42", "marker")
	seedScratchFile(t, marker)
	if s, err := NewSessionsInDir(nil, "", nil); err == nil {
		_ = s.Close()
		t.Error("constructor accepted an empty explicit root")
	}
	if got := string(mustRead(t, marker)); got != "stale" {
		t.Fatalf("working-directory marker changed: %q", got)
	}
}

func requireScratchLocked(t *testing.T, root string) {
	t.Helper()
	s, err := NewSessionsInDir(nil, root, nil)
	if s != nil {
		_ = s.Close()
	}
	if !errors.Is(err, unix.EWOULDBLOCK) {
		t.Fatalf("constructor for %s returned %v, want a lock conflict", root, err)
	}
}

func TestNewSessionsRejectsLiveRootAndAliasBeforeSweep(t *testing.T) {
	s := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), nil)
	path, err := filePath720(s, SessionKey{SessionID: "owner"}, masterPlaylistName)
	if err != nil {
		t.Fatal(err)
	}
	want := mustRead(t, path)
	orphan := filepath.Join(s.dir, "session-42", "marker")
	seedScratchFile(t, orphan)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(s.dir, alias); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{s.dir, alias} {
		requireScratchLocked(t, root)
		if got := mustRead(t, path); string(got) != string(want) {
			t.Fatalf("owner's playlist changed via %s", root)
		}
		if got := mustRead(t, orphan); string(got) != "stale" {
			t.Fatalf("root was swept via %s", root)
		}
	}
}

func TestNewSessionsResolvesSymlinkBeforeParentTraversal(t *testing.T) {
	media := t.TempDir()
	child := filepath.Join(media, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	newTestSessionsInDir(t, nil, media, nil)
	scratch := t.TempDir()
	alias := filepath.Join(scratch, "alias")
	if err := os.Symlink(child, alias); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(scratch, "session-42", "marker")
	seedScratchFile(t, marker)
	// Join would erase the parent traversal before the constructor sees it.
	s, err := NewSessionsInDir(nil, alias+"/..", nil)
	if s != nil {
		_ = s.Close()
	}
	if !errors.Is(err, unix.EWOULDBLOCK) {
		t.Errorf("constructor returned %v, want a lock conflict on %s", err, media)
	}
	if got := string(mustRead(t, marker)); got != "stale" {
		t.Fatalf("unrelated scratch marker changed: %q", got)
	}
}

func TestCloseWaitsForOpenArtifactReader(t *testing.T) {
	s := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), nil)
	a, err := s.LiveArtifactShaped(t.Context(), SessionKey{SessionID: "reader"}, "/src", 120, 1280, 720, false, false,
		ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{}, PriorityNormal, masterPlaylistName)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	f, _, err := a.OpenPlain()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	waitScratchSignal(t, a.session.scratch.cutoff)
	requireScratchLocked(t, s.dir)
	select {
	case err := <-closed:
		t.Fatalf("Close returned with an open reader: %v", err)
	default:
	}
	if _, err := f.Read(make([]byte, 1)); err != nil {
		t.Fatalf("read held descriptor: %v", err)
	}
	if len(mustRead(t, a.path)) == 0 {
		t.Fatal("held artifact path is empty during Close")
	}
	_ = f.Close()
	a.Release()
	if err := waitScratchResult(t, closed); err != nil {
		t.Fatal(err)
	}
	requireScratchMissing(t, a.path)
	newTestSessionsInDir(t, nil, s.dir, nil)
}

func TestTerminateCutsOffEncryptedReaderBeforeWaitingForRelease(t *testing.T) {
	s := newTestSessions(t, &fakeEncryptedTranscoder{}, t.TempDir(), nil)
	key := SessionKey{SessionID: "encrypted-reader"}
	a, err := s.LiveArtifactShaped(t.Context(), key, "", 120, 1280, 720, false, false, ladderCaps{}, VideoSignal{}, -1,
		ffmpeg.AudioProfile{Codec: "aac", Channels: 2}, nil, StreamShape{Encrypted: testEncryptedSource(nil)}, PriorityNormal, masterPlaylistName)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	f, err := a.OpenEncrypted()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, 1)
	if _, err := f.Read(buf); err != nil {
		t.Fatal(err)
	}

	terminated := make(chan int, 1)
	go func() { terminated <- s.TerminateSession(key.SessionID) }()
	waitScratchSignal(t, a.session.scratch.cutoff)
	if _, err := f.Read(buf); !errors.Is(err, encderiv.ErrLiveSessionClosed) {
		t.Fatalf("next read after cutoff: %v", err)
	}
	if _, err := os.Stat(a.session.dir); err != nil {
		t.Fatalf("directory removed with reader still held: %v", err)
	}
	select {
	case n := <-terminated:
		t.Fatalf("termination returned while reader still held: %d", n)
	default:
	}
	_ = f.Close()
	a.Release()
	select {
	case n := <-terminated:
		if n != 1 {
			t.Fatalf("terminated %d sessions", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("termination kept waiting after reader release")
	}
	requireScratchMissing(t, a.session.dir)
}

func TestCloseWaitsForFragmentJoin(t *testing.T) {
	transcoder := &keyframeFakeTranscoder{index: ffmpeg.KeyframeIndex{
		Keyframes: []time.Duration{0, time.Second}, Duration: 2 * time.Second,
	}}
	s := newTestSessions(t, transcoder, t.TempDir(), nil)
	key := SessionKey{SessionID: "join"}
	src := filepath.Join(t.TempDir(), "source")
	seedScratchFile(t, src)
	path, err := s.FilePath(t.Context(), key, src, 2, 1280, 720, true, masterPlaylistName)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	h := s.sessions[key]
	s.mu.Unlock()
	dir := filepath.Join(filepath.Dir(path), audioCopyRungID)
	for _, part := range []string{"seg00000.m4s", "seg00001.m4s"} {
		if err := os.WriteFile(filepath.Join(dir, part), mediaSegment(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entered, release := scratchLatch(t)
	s.join = func(dst *os.File, open func(string) (*os.File, error), parts []string) error {
		if err := joinFragments(dst, open, parts); err != nil {
			return err
		}
		close(entered)
		<-release.ch
		return nil
	}
	joined := make(chan error, 1)
	go func() {
		_, err := s.FilePath(t.Context(), key, src, 2, 1280, 720, true, audioCopyRungID+"/seg00000.m4s")
		joined <- err
	}()
	waitScratchSignal(t, entered)
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	waitScratchSignal(t, h.scratch.cutoff)
	requireScratchLocked(t, s.dir)
	requireScratchMissing(t, filepath.Join(dir, "joined00000.m4s"))
	release.open()
	if err := waitScratchResult(t, joined); !errors.Is(err, ErrSessionEvicted) {
		t.Fatalf("join after cutoff: %v", err)
	}
	if err := waitScratchResult(t, closed); err != nil {
		t.Fatal(err)
	}
	requireScratchMissing(t, h.dir)
	newTestSessionsInDir(t, nil, s.dir, nil)
}

func TestCloseWaitsForAdmittedScratchSweepAndSharesItsError(t *testing.T) {
	s, err := NewSessions(nil, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	entered, release := scratchLatch(t)
	readErr := &os.PathError{Op: "readdir", Path: s.dir, Err: os.ErrPermission}
	s.readScratch = func(fs.FS, string) ([]fs.DirEntry, error) {
		close(entered)
		<-release.ch
		return nil, readErr
	}
	swept := make(chan error, 1)
	go func() { swept <- s.PurgeOrphanedTranscodeDirs() }()
	waitScratchSignal(t, entered)
	if err := s.PurgeOrphanedTranscodeDirs(); !errors.Is(err, errIncompleteScratchSweep) {
		t.Fatalf("concurrent sweep: %v", err)
	}

	closed := make(chan error, 2)
	go func() { closed <- s.Close() }()
	go func() { closed <- s.Close() }()
	waitScratchCloseBlocked(t, s)
	requireScratchLocked(t, s.dir)
	select {
	case err := <-closed:
		t.Fatalf("Close returned during ReadDir: %v", err)
	default:
	}
	release.open()
	if err := waitScratchResult(t, swept); !errors.Is(err, readErr) {
		t.Fatalf("sweep returned %v", err)
	}
	first := waitScratchResult(t, closed)
	second := waitScratchResult(t, closed)
	if !errors.Is(first, readErr) || second != first {
		t.Fatalf("Close callers returned %v and %v", first, second)
	}
	newTestSessionsInDir(t, nil, s.dir, nil)
}

func TestScratchSweepReconcilesCollisionAndPreservesUnpublishedAllocation(t *testing.T) {
	s := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), nil)
	collision := fmt.Sprintf("session-%s-1", s.bootNonce)
	future := fmt.Sprintf("session-%s-900000", s.bootNonce)
	seedScratchFile(t, filepath.Join(s.dir, collision, "720p", "seg00000.m4s"))
	seedScratchFile(t, filepath.Join(s.dir, future, "marker"))
	entered, release := scratchLatch(t)
	s.mkdirDir = func(path string, mode fs.FileMode) error {
		if err := os.Mkdir(path, mode); err != nil {
			return err
		}
		close(entered)
		<-release.ch
		return nil
	}
	result := make(chan error, 1)
	key := SessionKey{SessionID: "first"}
	go func() {
		_, err := filePath720(s, key, "720p/seg00000.m4s")
		result <- err
	}()
	waitScratchSignal(t, entered)
	newDir := filepath.Join(s.dir, fmt.Sprintf("session-%s-2", s.bootNonce))
	if err := s.PurgeOrphanedTranscodeDirs(); !errors.Is(err, errIncompleteScratchSweep) {
		t.Fatalf("sweep with unresolved mkdir: %v", err)
	}
	requireScratchMissing(t, filepath.Join(s.dir, collision))
	requireScratchMissing(t, filepath.Join(s.dir, future))
	if _, err := os.Stat(newDir); err != nil {
		t.Fatalf("sweep removed unresolved allocation: %v", err)
	}
	release.open()
	if err := waitScratchResult(t, result); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(newDir, "720p", "seg00000.m4s")); string(got) == "stale" {
		t.Fatal("new session served the collision's segment")
	}

	// Model the state after exclusive mkdir and before publication.
	s.mkdirDir = nil
	s.mu.Lock()
	unpublished, err := s.allocateScratchLocked(t.Context(), SessionKey{}, "library")
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		s.releaseScratch(unpublished)
		s.mu.Lock()
		td := s.transitionLocked(&hlsSession{scratch: unpublished}, detachSession{}).teardown
		s.mu.Unlock()
		s.runTeardowns([]*sessionTeardown{td})
	}()
	if err := s.PurgeOrphanedTranscodeDirs(); err != nil {
		t.Fatalf("sweep with confirmed unpublished allocation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, unpublished.name)); err != nil {
		t.Fatalf("sweep removed confirmed unpublished allocation: %v", err)
	}
}

func TestScratchSweepRecordsCollisionFailureAsRootDebt(t *testing.T) {
	s := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), nil)
	name := fmt.Sprintf("session-%s-1", s.bootNonce)
	collision := filepath.Join(s.dir, name)
	seedScratchFile(t, filepath.Join(collision, "marker"))
	fail := errors.New("injected remove failure")
	s.removeDir = func(path string) error {
		if path == collision {
			return fail
		}
		return s.removeScratchFixture(path)
	}
	key := SessionKey{SessionID: "collision"}
	a, err := s.LiveArtifactShaped(t.Context(), key, "/src", 120, 1280, 720, false, false,
		ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{LibraryID: "library"}, PriorityNormal, masterPlaylistName)
	if err != nil {
		t.Fatal(err)
	}
	a.Release()
	if err := s.PurgeOrphanedTranscodeDirs(); !errors.Is(err, fail) {
		t.Fatalf("sweep returned %v", err)
	}
	s.mu.Lock()
	debt := s.paths[name]
	if debt == nil || debt.state != scratchDebt || debt.library != "" || debt.retryAt.IsZero() {
		t.Errorf("collision debt = %+v, want root-only debt with retry scheduled", debt)
	}
	s.mu.Unlock()
	s.removeDir = nil
	if err := s.PurgeOrphanedTranscodeDirs(); err != nil {
		t.Fatal(err)
	}
	requireScratchMissing(t, collision)
}

func TestScratchReaperRetriesDetachedDebtWithSuccessorOrVacantHead(t *testing.T) {
	for _, successor := range []bool{true, false} {
		t.Run(fmt.Sprintf("successor=%t", successor), func(t *testing.T) {
			var millis atomic.Int64
			s := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), func() time.Time { return time.UnixMilli(millis.Load()) })
			key := SessionKey{SessionID: "same-key"}
			a, err := s.LiveArtifactShaped(t.Context(), key, "/src", 120, 1280, 720, false, false, ladderCaps{},
				VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{LibraryID: "library"}, PriorityNormal, masterPlaylistName)
			if err != nil {
				t.Fatal(err)
			}
			old := a.session.dir
			a.Release()

			entered, release := scratchLatch(t)
			var tries atomic.Int64
			s.removeDir = func(path string) error {
				if path == old && tries.Add(1) == 1 {
					close(entered)
					<-release.ch
					return os.ErrPermission
				}
				return s.removeScratchFixture(path)
			}
			terminated := make(chan struct{})
			go func() {
				s.TerminateSession(key.SessionID)
				close(terminated)
			}()
			waitScratchSignal(t, entered)
			// The path registry must report pending cleanup even if an entry is
			// absent from the sweep listing.
			s.readScratch = func(fs.FS, string) ([]fs.DirEntry, error) { return nil, nil }
			if err := s.PurgeOrphanedTranscodeDirs(); !errors.Is(err, errIncompleteScratchSweep) {
				t.Fatalf("sweep during initial removal: %v", err)
			}
			release.open()
			waitScratchSignal(t, terminated)
			s.mu.Lock()
			debt := s.paths[filepath.Base(old)]
			if debt == nil || debt.state != scratchDebt || debt.library != "library" {
				t.Errorf("detached debt = %+v", debt)
			}
			s.mu.Unlock()
			var fresh string
			if successor {
				fresh, err = filePath720(s, key, masterPlaylistName)
				if err != nil {
					t.Fatal(err)
				}
			}
			s.reap()
			if tries.Load() != 1 {
				t.Fatal("reaper ignored removal backoff")
			}
			millis.Add(1000)
			s.reap()
			if tries.Load() != 2 {
				t.Fatalf("reaper tried old directory %d times", tries.Load())
			}
			requireScratchMissing(t, old)
			if successor {
				if after, err := filePath720(s, key, masterPlaylistName); err != nil || after != fresh {
					t.Fatalf("successor changed after retry: %s, %v", after, err)
				}
			}
		})
	}
}

func TestScratchOwnershipStaysBoundedDuringLatchedSweep(t *testing.T) {
	s := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), nil)
	orphan := filepath.Join(s.dir, "session-9999")
	seedScratchFile(t, filepath.Join(orphan, "marker"))
	entered, release := scratchLatch(t)
	s.readScratch = func(root fs.FS, name string) ([]fs.DirEntry, error) {
		entries, err := fs.ReadDir(root, name)
		close(entered)
		<-release.ch
		return entries, err
	}
	result := make(chan error, 1)
	go func() { result <- s.PurgeOrphanedTranscodeDirs() }()
	waitScratchSignal(t, entered)
	key := SessionKey{SessionID: "churn"}
	for range 40 {
		if _, err := filePath720(s, key, masterPlaylistName); err != nil {
			t.Fatal(err)
		}
		s.TerminateSession(key.SessionID)
	}
	live, err := filePath720(s, key, masterPlaylistName)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	paths := len(s.paths)
	s.mu.Unlock()
	if paths != 1 {
		t.Fatalf("churn retained %d paths with one live session", paths)
	}
	release.open()
	if err := waitScratchResult(t, result); err != nil {
		t.Fatal(err)
	}
	requireScratchMissing(t, orphan)
	if _, err := os.Stat(live); err != nil {
		t.Fatalf("sweep removed current session: %v", err)
	}
}

func TestScratchArtifactOpensRefuseSymlinksAndFIFO(t *testing.T) {
	s := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), nil)
	key := SessionKey{SessionID: "unsafe"}
	path, err := filePath720(s, key, masterPlaylistName)
	if err != nil {
		t.Fatal(err)
	}
	a, err := testArtifactForPath(s, key, path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	seedScratchFile(t, outside)
	inRoot := filepath.Join(a.session.dir, "target.m3u8")
	seedScratchFile(t, inRoot)
	for _, target := range []string{outside, filepath.Base(inRoot)} {
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if f, _, err := a.OpenPlain(); err == nil {
			_ = f.Close()
			t.Fatalf("opened top-level symlink to %q", target)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	seedScratchFile(t, filepath.Join(a.session.dir, "720p", "seg00000.m4s"))
	if err := os.Symlink("720p", filepath.Join(a.session.dir, "rung-alias")); err != nil {
		t.Fatal(err)
	}
	if f, _, err := a.session.openArtifact(filepath.Join(a.session.dir, "rung-alias", "seg00000.m4s")); err == nil {
		_ = f.Close()
		t.Fatal("opened artifact through an in-root rung symlink")
	}
	if entries, err := a.session.readRung("rung-alias"); err == nil {
		t.Fatalf("listed a symlinked rung: %v", entries)
	}
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		f, _, err := a.OpenPlain()
		if f != nil {
			_ = f.Close()
		}
		result <- err
	}()
	if err := waitScratchResult(t, result); err == nil {
		t.Fatal("opened a writerless FIFO")
	}
	if err := s.joinArtifact(context.Background(), a.session, filepath.Join(a.session.dir, "720p", "joined00000.m4s"), []string{path}); err == nil {
		t.Fatal("join read a writerless FIFO")
	}
}

func seedScratchFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
}

type scratchRelease struct {
	ch   chan struct{}
	once sync.Once
}

func (r *scratchRelease) open() {
	r.once.Do(func() { close(r.ch) })
}

func scratchLatch(t *testing.T) (chan struct{}, *scratchRelease) {
	t.Helper()
	r := &scratchRelease{ch: make(chan struct{})}
	t.Cleanup(r.open)
	return make(chan struct{}), r
}

func waitScratchSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for scratch owner")
	}
}

func waitScratchResult(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for scratch result")
		return nil
	}
}

func waitScratchCloseBlocked(t *testing.T, s *Sessions) <-chan struct{} {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		idle := s.offLockIdle
		s.mu.Unlock()
		if idle != nil {
			return idle
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Close did not wait for scratch ownership")
	return nil
}

func TestScratchCleanupOwnsLateMkdirAfterCancellation(t *testing.T) {
	s := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), nil)
	entered, release := scratchLatch(t)
	s.mkdirDir = func(path string, mode fs.FileMode) error {
		close(entered)
		<-release.ch
		return os.Mkdir(path, mode)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := s.FilePath(ctx, SessionKey{SessionID: "canceled"}, "/src", 120, 1280, 720, false, masterPlaylistName)
		result <- err
	}()
	waitScratchSignal(t, entered)
	cancel()
	release.open()
	if err := waitScratchResult(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled creator returned %v", err)
	}
	requireScratchMissing(t, filepath.Join(s.dir, fmt.Sprintf("session-%s-1", s.bootNonce)))
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.paths) != 0 || len(s.sessions) != 0 {
		t.Fatalf("late mkdir retained %d paths and %d sessions", len(s.paths), len(s.sessions))
	}
}

func TestTerminationRevokesScratchAllocation(t *testing.T) {
	cases := []struct {
		name      string
		terminate func(*Sessions, SessionKey) int
	}{
		{"session", func(s *Sessions, key SessionKey) int { return s.TerminateSession(key.SessionID) }},
		{"playback", func(s *Sessions, key SessionKey) int { return s.TerminatePlayback(key.SessionID, key.MediaID) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transcoder := &fakeTranscoder{}
			s := newTestSessions(t, transcoder, t.TempDir(), nil)
			entered, release := scratchLatch(t)
			s.mkdirDir = func(path string, mode fs.FileMode) error {
				close(entered)
				<-release.ch
				return os.Mkdir(path, mode)
			}
			key := SessionKey{SessionID: "viewer", MediaID: "item", ProfileCapability: "receiver"}
			request := make(chan error, 1)
			go func() {
				_, err := filePath720(s, key, "720p/seg00000.m4s")
				request <- err
			}()
			waitScratchSignal(t, entered)
			terminated := make(chan error, 1)
			go func() {
				if count := s.TerminatePlayback(key.SessionID, "other"); count != 0 {
					terminated <- fmt.Errorf("unrelated playback revoked %d sessions", count)
					return
				}
				if count := tc.terminate(s, key); count != 1 {
					terminated <- fmt.Errorf("termination revoked %d sessions, want 1", count)
					return
				}
				if count := tc.terminate(s, key); count != 0 {
					terminated <- fmt.Errorf("repeated termination revoked %d sessions", count)
					return
				}
				terminated <- nil
			}()
			if err := waitScratchResult(t, terminated); err != nil {
				t.Fatal(err)
			}
			release.open()
			if err := waitScratchResult(t, request); !errors.Is(err, ErrSessionEvicted) {
				t.Fatalf("revoked creator returned %v", err)
			}
			requireScratchMissing(t, filepath.Join(s.dir, fmt.Sprintf("session-%s-1", s.bootNonce)))
			if got := len(transcoder.starts); got != 0 {
				t.Fatalf("revoked creator started %d encoders", got)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.allocation != nil || len(s.sessions) != 0 || len(s.paths) != 0 {
				t.Fatal("revoked creator retained a scratch claim or published a session")
			}
		})
	}
}

func TestCloseWaitsForRemovalAfterLateScratchMkdir(t *testing.T) {
	s := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), nil)
	mkdirEntered, mkdirRelease := scratchLatch(t)
	removeEntered, removeRelease := scratchLatch(t)
	s.mkdirDir = func(path string, mode fs.FileMode) error {
		close(mkdirEntered)
		<-mkdirRelease.ch
		return os.Mkdir(path, mode)
	}
	s.removeDir = func(path string) error {
		close(removeEntered)
		<-removeRelease.ch
		return s.removeScratchFixture(path)
	}
	created := make(chan error, 1)
	go func() {
		_, err := filePath720(s, SessionKey{SessionID: "late-mkdir"}, masterPlaylistName)
		created <- err
	}()
	waitScratchSignal(t, mkdirEntered)
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	idle := waitScratchCloseBlocked(t, s)
	requireScratchLocked(t, s.dir)
	mkdirRelease.open()
	waitScratchSignal(t, removeEntered)
	select {
	case <-idle:
		t.Fatal("late mkdir signaled idle before its removal completed")
	default:
	}
	requireScratchLocked(t, s.dir)
	select {
	case err := <-closed:
		t.Fatalf("Close returned during late directory removal: %v", err)
	default:
	}
	removeRelease.open()
	if err := waitScratchResult(t, created); !errors.Is(err, ErrSessionEvicted) {
		t.Fatalf("late creator returned %v", err)
	}
	if err := waitScratchResult(t, closed); err != nil {
		t.Fatal(err)
	}
	requireScratchMissing(t, filepath.Join(s.dir, fmt.Sprintf("session-%s-1", s.bootNonce)))
	newTestSessionsInDir(t, nil, s.dir, nil)
}

func TestShapeReplacementServesBeforePredecessorReaderReleases(t *testing.T) {
	transcoder := &fakeTranscoder{}
	s := newTestSessions(t, transcoder, t.TempDir(), nil)
	s.SetTranscodeCap(1)
	slow := newSlowStop(s)
	defer slow.unblock()
	key := SessionKey{SessionID: "replace-with-open-reader"}
	held, err := s.LiveArtifactShaped(t.Context(), key, "/src/movie.mkv", 120, 1280, 720, false, false,
		ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{}, PriorityNormal, "720p/seg00000.m4s")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	reader, _, err := held.OpenPlain()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	slow.armed.Store(true)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	replaced := make(chan error, 1)
	go func() {
		_, err := s.FilePath(ctx, key, "/src/movie.mkv", 60, 1280, 720, false, masterPlaylistName)
		replaced <- err
	}()
	slow.waitEntered(t)
	select {
	case err := <-replaced:
		t.Fatalf("replacement served before stopping predecessor: %v", err)
	default:
	}
	slow.unblock()
	if err := waitScratchResult(t, replaced); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FilePath(ctx, key, "/src/movie.mkv", 60, 1280, 720, false, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("successor could not use the freed encoder slot: %v", err)
	}
	if got := string(mustRead(t, held.path)); got != "m4s" {
		t.Fatalf("predecessor artifact while retained = %q", got)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	held.Release()
	requireScratchMissing(t, held.session.dir)
}

func TestPreemptionServesBeforeVictimReaderReleases(t *testing.T) {
	s := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), nil)
	s.SetTranscodeCap(1)
	slow := newSlowStop(s)
	defer slow.unblock()
	const segment = "720p/seg00000.m4s"
	victim, err := s.LiveArtifactShaped(t.Context(), SessionKey{SessionID: "victim"}, "/src", 120, 1280, 720, false, false,
		ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{}, PriorityNormal, segment)
	if err != nil {
		t.Fatal(err)
	}
	defer victim.Release()
	reader, _, err := victim.OpenPlain()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	slow.armed.Store(true)
	started := make(chan error, 1)
	go func() {
		next, err := s.LiveArtifactShaped(t.Context(), SessionKey{SessionID: "preemptor"}, "/src", 120, 1280, 720, false, false,
			ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{}, PriorityForeground, segment)
		if err == nil {
			next.Release()
		}
		started <- err
	}()
	slow.waitEntered(t)
	select {
	case err := <-started:
		t.Fatalf("preemptor served before the victim stopped: %v", err)
	default:
	}
	slow.unblock()
	if err := waitScratchResult(t, started); err != nil {
		t.Fatal(err)
	}
	if got := string(mustRead(t, victim.path)); got != "m4s" {
		t.Fatalf("victim's retained segment = %q", got)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	victim.Release()
	requireScratchMissing(t, victim.session.dir)
}

func TestScratchReaperRetriesDebtWhileIdleReaderIsHeld(t *testing.T) {
	var millis atomic.Int64
	s := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), func() time.Time { return time.UnixMilli(millis.Load()) })
	debtKey := SessionKey{SessionID: "debt"}
	path, err := filePath720(s, debtKey, masterPlaylistName)
	if err != nil {
		t.Fatal(err)
	}
	debtDir := filepath.Dir(path)
	var attempts atomic.Int64
	s.removeDir = func(path string) error {
		if path == debtDir && attempts.Add(1) == 1 {
			return os.ErrPermission
		}
		return s.removeScratchFixture(path)
	}
	s.TerminateSession(debtKey.SessionID)
	if attempts.Load() != 1 {
		t.Fatal("failed to seed removal debt")
	}
	key := SessionKey{SessionID: "idle"}
	path, err = filePath720(s, key, masterPlaylistName)
	if err != nil {
		t.Fatal(err)
	}
	a, err := testArtifactForPath(s, key, path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	f, _, err := a.OpenPlain()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	millis.Store((sessionIdleTimeout + time.Second).Milliseconds())
	reaped := make(chan struct{})
	go func() {
		s.reap()
		close(reaped)
	}()
	waitScratchSignal(t, a.session.scratch.cutoff)
	waitScratchSignal(t, reaped)
	if attempts.Load() != 2 {
		t.Fatalf("unrelated debt received %d removal attempts, want 2", attempts.Load())
	}
	requireScratchMissing(t, debtDir)
	if _, err := f.Read(make([]byte, 1)); err != nil {
		t.Fatalf("read from the held artifact: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("idle directory removed before reader release: %v", err)
	}
	_ = f.Close()
	a.Release()
	requireScratchMissing(t, filepath.Dir(path))
}
