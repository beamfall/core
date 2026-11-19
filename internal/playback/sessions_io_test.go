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
	"testing/synctest"
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

func TestPlaylistSetupWaiterCancellationAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name              string
		cancelWaiter      bool
		replaceSourceFile bool
	}{
		{name: "write failure"},
		{name: "canceled waiter", cancelWaiter: true},
		{name: "different source generation", replaceSourceFile: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
				defer sessions.Close()
				latch := newSessionIOLatch(t)
				defer latch.unblock()
				writeErr := errors.New("playlist write failed")
				var writes atomic.Int32
				sessions.writePlaylists = func(dir string, rungs []Rung, duration int, timeline *keyframeTimeline, primed primedGrid, write func(string, []byte) error) error {
					if writes.Add(1) == 1 {
						writeArtifact := write
						write = func(rel string, data []byte) error {
							if rel == masterPlaylistName {
								latch.block()
								if !tc.cancelWaiter {
									return writeErr
								}
							}
							return writeArtifact(rel, data)
						}
					}
					return writeSessionPlaylists(dir, rungs, duration, timeline, primed, write)
				}
				key := SessionKey{SessionID: "same-device", MediaID: "same-media"}
				src := filepath.Join(t.TempDir(), "source.mkv")
				if err := os.WriteFile(src, []byte("source"), 0o600); err != nil {
					t.Fatal(err)
				}
				creator := make(chan error, 1)
				go func() {
					_, err := sessions.FilePath(t.Context(), key, src, 120, 1280, 720, false, masterPlaylistName)
					creator <- err
				}()
				latch.wait(t)
				if tc.replaceSourceFile {
					next := src + ".replacement"
					if err := os.WriteFile(next, []byte("new source"), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(next, src); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				waiter := make(chan error, 1)
				go func() {
					_, err := sessions.FilePath(ctx, key, src, 120, 1280, 720, false, masterPlaylistName)
					waiter <- err
				}()
				synctest.Wait()
				select {
				case err := <-waiter:
					t.Fatalf("same-key request returned before setup finished: %v", err)
				default:
				}
				if tc.cancelWaiter {
					cancel()
					if err := awaitSessionOperation(t, waiter); !errors.Is(err, context.Canceled) {
						t.Fatalf("canceled waiter returned %v, want context.Canceled", err)
					}
					latch.unblock()
					if err := awaitSessionOperation(t, creator); err != nil {
						t.Fatal(err)
					}
					return
				}
				latch.unblock()
				if err := awaitSessionOperation(t, creator); !errors.Is(err, writeErr) {
					t.Fatalf("creator returned %v, want %v", err, writeErr)
				}
				if tc.replaceSourceFile {
					if err := awaitSessionOperation(t, waiter); err != nil {
						t.Fatalf("waiter for the replacement source inherited setup failure: %v", err)
					}
					if writes.Load() != 2 {
						t.Fatalf("playlist writes = %d, want separate setups for two generations", writes.Load())
					}
					return
				}
				if err := awaitSessionOperation(t, waiter); !errors.Is(err, writeErr) {
					t.Fatalf("waiter returned %v, want %v", err, writeErr)
				}
				if writes.Load() != 1 {
					t.Fatal("same-key requests performed separate playlist writes")
				}
				if _, err := sessions.FilePath(t.Context(), key, src, 120, 1280, 720, false, masterPlaylistName); err != nil {
					t.Fatalf("retry after failed setup: %v", err)
				}
			})
		})
	}
}

func TestReshapeStopsPredecessorBeforeWritingReplacement(t *testing.T) {
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	latch := newSessionIOLatch(t)
	sessions.SetTranscodeCap(1)
	key := SessionKey{SessionID: "reshaped", MediaID: "media"}
	stopped := make(chan *ffmpeg.HLSProcess, 2)
	sessions.stopEncoder = func(proc *ffmpeg.HLSProcess) { stopped <- proc }
	var blocked atomic.Bool
	sessions.writePlaylists = func(dir string, rungs []Rung, duration int, timeline *keyframeTimeline, primed primedGrid, write func(string, []byte) error) error {
		if duration == 121 && blocked.CompareAndSwap(false, true) {
			latch.block()
		}
		return writeSessionPlaylists(dir, rungs, duration, timeline, primed, write)
	}
	if _, err := filePath720(sessions, key, "720p/init.mp4"); err != nil {
		t.Fatal(err)
	}
	old := registeredSession(t, sessions, key)
	proc := old.proc
	if proc == nil || sessions.CanAdmit(SessionKey{SessionID: "unrelated", MediaID: "media"}) {
		t.Fatal("fixture did not start an encoder at capacity")
	}
	done := make(chan error, 1)
	go func() {
		_, err := sessions.FilePath(t.Context(), key, old.src, 121, 1280, 720, false, masterPlaylistName)
		done <- err
	}()
	latch.wait(t)
	select {
	case got := <-stopped:
		if got != proc {
			t.Fatal("reshape stopped the wrong encoder")
		}
	default:
		t.Fatal("predecessor was still running when replacement entered its playlist write")
	}
	finishesWhileBlocked(t, "admission into the predecessor's slot", func() error {
		_, err := filePath720(sessions, SessionKey{SessionID: "unrelated", MediaID: "media"}, "720p/init.mp4")
		return err
	})
	latch.unblock()
	if err := awaitSessionOperation(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestTerminateSessionStopsEncoderWhilePlaylistSetupBlocked(t *testing.T) {
	// Exercise repeated revocations because the order of the two map entries
	// varies. Both playbacks belong to the same device in every revocation.
	for range 16 {
		synctest.Test(t, func(t *testing.T) {
			sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
			defer sessions.Close()
			latch := newSessionIOLatch(t)
			defer latch.unblock()
			pending := SessionKey{SessionID: "revoked-device", MediaID: "pending"}
			running := SessionKey{SessionID: pending.SessionID, MediaID: "running"}
			var firstWrite atomic.Bool
			sessions.writePlaylists = func(dir string, rungs []Rung, duration int, timeline *keyframeTimeline, primed primedGrid, write func(string, []byte) error) error {
				if firstWrite.CompareAndSwap(false, true) {
					latch.block()
				}
				return writeSessionPlaylists(dir, rungs, duration, timeline, primed, write)
			}
			stopped := make(chan *ffmpeg.HLSProcess, 1)
			sessions.stopEncoder = func(proc *ffmpeg.HLSProcess) { stopped <- proc }
			setup := make(chan error, 1)
			go func() {
				_, err := filePath720(sessions, pending, masterPlaylistName)
				setup <- err
			}()
			latch.wait(t)
			if _, err := filePath720(sessions, running, "720p/init.mp4"); err != nil {
				t.Fatal(err)
			}
			proc := registeredSession(t, sessions, running).proc
			if proc == nil {
				t.Fatal("fixture did not start an encoder")
			}
			terminated := make(chan int, 1)
			go func() { terminated <- sessions.TerminateSession(pending.SessionID) }()
			synctest.Wait()
			select {
			case got := <-stopped:
				if got != proc {
					t.Fatal("revocation stopped the wrong encoder")
				}
			default:
				t.Fatal("revocation did not stop the encoder while playlist setup was blocked")
			}
			select {
			case n := <-terminated:
				t.Fatalf("revocation returned before pending setup cleaned up: %d", n)
			default:
			}
			latch.unblock()
			if err := awaitSessionOperation(t, setup); !errors.Is(err, ErrSessionEvicted) {
				t.Fatalf("pending setup returned %v, want ErrSessionEvicted", err)
			}
			if n := <-terminated; n != 2 {
				t.Fatalf("revocation detached %d playbacks, want 2", n)
			}
		})
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

func TestOverlappingComposedSetupReusesResolution(t *testing.T) {
	for _, writeFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("write fails %t", writeFails), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
				defer sessions.Close()
				latch := newSessionIOLatch(t)
				defer latch.unblock()
				resolveErr := errors.New("picture sidecar no longer readable")
				writeErr := errors.New("picture write failed")
				var resolves atomic.Int32
				sessions.SetPictureComposer(func(context.Context, store.MediaID, string) (ComposedPicture, bool, error) {
					if resolves.Add(1) != 1 {
						return ComposedPicture{}, false, resolveErr
					}
					return ComposedPicture{TimedText: composedTestText}, true, nil
				})
				sessions.writePicture = func(dir string, picture ComposedPicture) (*ffmpeg.ComposedPicture, error) {
					latch.block()
					if writeFails {
						return nil, writeErr
					}
					return writeLivePicture(dir, picture)
				}
				key := SessionKey{SessionID: "device", MediaID: "media"}
				request := func(composed bool) <-chan error {
					done := make(chan error, 1)
					go func() {
						_, err := sessions.FilePathShaped(t.Context(), key, "/src/audio", 30, 0, 0, false, false,
							ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil,
							StreamShape{ComposedPicture: composed, AudioOnly: true}, PriorityNormal, masterPlaylistName)
						done <- err
					}()
					return done
				}
				creator := request(true)
				latch.wait(t)
				overlap := request(true)
				var plain <-chan error
				if writeFails {
					plain = request(false)
				}
				synctest.Wait()
				if got := resolves.Load(); got != 1 {
					t.Fatalf("picture resolutions = %d while first write blocked, want 1", got)
				}
				latch.unblock()
				var want error
				if writeFails {
					want = writeErr
				}
				for _, done := range []<-chan error{creator, overlap} {
					if err := awaitSessionOperation(t, done); !errors.Is(err, want) {
						t.Fatalf("composed setup returned %v, want %v", err, want)
					}
				}
				if writeFails {
					if err := awaitSessionOperation(t, plain); err != nil {
						t.Fatalf("plain setup inherited composed write failure: %v", err)
					}
					if registeredSession(t, sessions, key).picture != nil {
						t.Fatal("plain setup has a composed encoder input")
					}
				} else {
					picture := registeredSession(t, sessions, key).picture
					if picture == nil {
						t.Fatal("composed encoder input was not published")
					}
					if _, err := os.Stat(picture.TimedTextPath); err != nil {
						t.Fatalf("composed encoder input was not prepared: %v", err)
					}
				}
			})
		})
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

func TestCloseWaitsForOrphanSweepBeforeScratchHandoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		sessions := NewSessionsInDir(&fakeTranscoder{}, root, nil)
		defer sessions.Close()
		latch := newSessionIOLatch(t)
		defer latch.unblock()
		orphan := filepath.Join(root, "session-42")
		if err := os.MkdirAll(orphan, 0o700); err != nil {
			t.Fatal(err)
		}
		var reads atomic.Int32
		sessions.readOrphanDirs = func(dir string) ([]os.DirEntry, error) {
			if reads.Add(1) == 1 {
				latch.block()
			}
			return os.ReadDir(dir)
		}
		sweep := make(chan error, 1)
		go func() { sweep <- sessions.PurgeOrphanedTranscodeDirs() }()
		latch.wait(t)
		closed := make(chan struct{})
		go func() {
			sessions.Close()
			close(closed)
		}()
		synctest.Wait()
		select {
		case <-closed:
			t.Fatal("Close returned while the old manager was still reading the scratch directory")
		default:
		}
		if err := sessions.PurgeOrphanedTranscodeDirs(); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("sweep during Close returned %v, want os.ErrClosed", err)
		}
		latch.unblock()
		if err := awaitSessionOperation(t, sweep); err != nil {
			t.Fatal(err)
		}
		<-closed
		if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("admitted sweep left orphan behind: %v", err)
		}

		successor := NewSessionsInDir(&fakeTranscoder{}, root, nil)
		defer successor.Close()
		master, err := filePath720(successor, SessionKey{SessionID: "new-manager", MediaID: "media"}, masterPlaylistName)
		if err != nil {
			t.Fatal(err)
		}
		if err := sessions.PurgeOrphanedTranscodeDirs(); !errors.Is(err, os.ErrClosed) || reads.Load() != 1 {
			t.Fatalf("old manager swept after handoff: err=%v, reads=%d", err, reads.Load())
		}
		if _, err := os.Stat(master); err != nil {
			t.Fatalf("old manager removed the successor's playlist: %v", err)
		}
	})
}
