package playback

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

type sessionIOLatch struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newSessionIOLatch(t *testing.T) *sessionIOLatch {
	t.Helper()
	l := &sessionIOLatch{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(l.unblock)
	return l
}

func (l *sessionIOLatch) block() {
	close(l.entered)
	<-l.release
}

func (l *sessionIOLatch) unblock() { l.once.Do(func() { close(l.release) }) }

func (l *sessionIOLatch) wait(t *testing.T) {
	t.Helper()
	select {
	case <-l.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not reach the I/O latch")
	}
}

func awaitSessionOperation(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("session operation did not finish")
		return nil
	}
}

func assertUnrelatedSessionsProgress(t *testing.T, sessions *Sessions) {
	t.Helper()
	detach := SessionKey{SessionID: "detach", MediaID: "detach-media"}
	revoke := SessionKey{SessionID: "revoke", MediaID: "revoke-media"}
	admit := SessionKey{SessionID: "admit", MediaID: "admit-media"}

	finishesWhileBlocked(t, "unrelated transcode admission", func() error {
		_, err := filePath720(sessions, admit, "720p/init.mp4")
		return err
	})
	finishesWhileBlocked(t, "unrelated playback detach", func() error {
		if n := sessions.TerminatePlayback(detach.SessionID, detach.MediaID); n != 1 {
			return fmt.Errorf("detached %d playbacks, want 1", n)
		}
		return nil
	})
	finishesWhileBlocked(t, "unrelated session termination", func() error {
		if n := sessions.TerminateSession(revoke.SessionID); n != 1 {
			return fmt.Errorf("terminated %d sessions, want 1", n)
		}
		return nil
	})
}

func seedUnrelatedSessions(t *testing.T, sessions *Sessions) {
	t.Helper()
	for _, key := range []SessionKey{
		{SessionID: "detach", MediaID: "detach-media"},
		{SessionID: "revoke", MediaID: "revoke-media"},
	} {
		if _, err := filePath720(sessions, key, "720p/init.mp4"); err != nil {
			t.Fatalf("start %s session: %v", key.SessionID, err)
		}
	}
}

func TestPlaylistSetupDiscardsEvictedResult(t *testing.T) {
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	latch := newSessionIOLatch(t)
	seedUnrelatedSessions(t, sessions)
	var blocked atomic.Bool
	var oldDir string
	sessions.writePlaylists = func(dir string, rungs []Rung, duration int, timeline *keyframeTimeline, primed primedGrid, write func(string, []byte) error) error {
		if blocked.CompareAndSwap(false, true) {
			oldDir = dir
			writeArtifact := write
			write = func(rel string, data []byte) error {
				if rel == masterPlaylistName {
					latch.block()
				}
				return writeArtifact(rel, data)
			}
		}
		return writeSessionPlaylists(dir, rungs, duration, timeline, primed, write)
	}
	key := SessionKey{SessionID: "blocked", MediaID: "media"}
	start := make(chan error, 1)
	go func() {
		_, err := filePath720(sessions, key, masterPlaylistName)
		start <- err
	}()
	latch.wait(t)
	assertUnrelatedSessionsProgress(t, sessions)
	finishesWhileBlocked(t, "orphan sweep during playlist setup", sessions.PurgeOrphanedTranscodeDirs)
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatalf("sweep removed the pending session's directory: %v", err)
	}
	ready := registeredSession(t, sessions, key).initializing.ready
	terminated := make(chan error, 1)
	go func() {
		if n := sessions.TerminateSession(key.SessionID); n != 1 {
			terminated <- fmt.Errorf("terminated %d pending sessions, want 1", n)
			return
		}
		terminated <- nil
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("termination did not detach the pending session")
	}
	master, err := filePath720(sessions, key, masterPlaylistName)
	if err != nil {
		t.Fatal(err)
	}
	replacement := registeredSession(t, sessions, key)
	if replacement.dir == oldDir {
		t.Fatal("replacement reused the pending session's directory")
	}
	select {
	case err := <-terminated:
		t.Fatalf("termination returned before the pending setup could clean up: %v", err)
	default:
	}
	latch.unblock()
	if err := awaitSessionOperation(t, start); !errors.Is(err, ErrSessionEvicted) {
		t.Fatalf("evicted setup returned %v, want ErrSessionEvicted", err)
	}
	if err := awaitSessionOperation(t, terminated); err != nil {
		t.Fatal(err)
	}
	if registeredSession(t, sessions, key) != replacement {
		t.Fatal("evicted setup overwrote the replacement")
	}
	if _, err := os.Stat(master); err != nil {
		t.Fatalf("evicted setup removed the replacement's playlist: %v", err)
	}
	if _, err := os.Stat(oldDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("evicted setup left its directory behind: stat returned %v", err)
	}
}

func TestComposedPictureWriteRunsOutsideSessionsLock(t *testing.T) {
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	latch := newSessionIOLatch(t)
	seedUnrelatedSessions(t, sessions)
	sessions.SetPictureComposer(func(context.Context, store.MediaID, string) (ComposedPicture, bool, error) {
		return ComposedPicture{TimedText: composedTestText}, true, nil
	})
	sessions.writePicture = func(dir string, picture ComposedPicture) (*ffmpeg.ComposedPicture, error) {
		latch.block()
		return writeLivePicture(dir, picture)
	}
	key := SessionKey{SessionID: "blocked", MediaID: "media"}
	done := make(chan error, 1)
	go func() {
		_, err := sessions.FilePathShaped(context.Background(), key, "/src/audio", 30, 0, 0, false, false,
			ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{ComposedPicture: true}, PriorityNormal, masterPlaylistName)
		done <- err
	}()
	latch.wait(t)
	assertUnrelatedSessionsProgress(t, sessions)
	latch.unblock()
	if err := awaitSessionOperation(t, done); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(registeredSession(t, sessions, key).picture.TimedTextPath); err != nil {
		t.Fatalf("timed text was not written: %v", err)
	}
}

func TestArtifactRecheckRunsOutsideSessionsLock(t *testing.T) {
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	latch := newSessionIOLatch(t)
	seedUnrelatedSessions(t, sessions)
	key := SessionKey{SessionID: "blocked", MediaID: "media"}
	master, err := filePath720(sessions, key, masterPlaylistName)
	if err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(filepath.Dir(master), "720p", initSegmentName)
	sessions.statArtifact = func(path string) (os.FileInfo, error) {
		if path == artifact {
			latch.block()
		}
		return nil, os.ErrNotExist // these init segments are requested before their first encoder starts
	}
	done := make(chan error, 1)
	go func() {
		_, err := filePath720(sessions, key, "720p/init.mp4")
		done <- err
	}()
	latch.wait(t)
	assertUnrelatedSessionsProgress(t, sessions)
	latch.unblock()
	if err := awaitSessionOperation(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestOrphanSweepRunsOutsideSessionsLock(t *testing.T) {
	for _, operation := range []string{"read directory", "remove directory"} {
		t.Run(operation, func(t *testing.T) {
			sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
			t.Cleanup(sessions.Close)
			latch := newSessionIOLatch(t)
			seedUnrelatedSessions(t, sessions)
			orphan := filepath.Join(sessions.dir, "session-42")
			if err := os.Mkdir(orphan, 0o700); err != nil {
				t.Fatal(err)
			}
			if operation == "read directory" {
				sessions.readOrphanDirs = func(dir string) ([]os.DirEntry, error) {
					latch.block()
					return os.ReadDir(dir)
				}
			} else {
				sessions.removeDir = func(path string) error {
					if path == orphan {
						latch.block()
					}
					return os.RemoveAll(path)
				}
			}
			done := make(chan error, 1)
			go func() { done <- sessions.PurgeOrphanedTranscodeDirs() }()
			latch.wait(t)
			assertUnrelatedSessionsProgress(t, sessions)
			latch.unblock()
			if err := awaitSessionOperation(t, done); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("orphan stat returned %v, want not-exist", err)
			}
			admitted := registeredSession(t, sessions, SessionKey{SessionID: "admit", MediaID: "admit-media"})
			if _, err := os.Stat(filepath.Join(admitted.dir, masterPlaylistName)); err != nil {
				t.Fatalf("sweep removed the newly allocated session's playlist: %v", err)
			}
		})
	}
}
