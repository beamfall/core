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
		failWrite         bool
		cancelWaiter      bool
		replaceSourceFile bool
	}{
		{name: "write failure", failWrite: true},
		{name: "canceled waiter", cancelWaiter: true},
		{name: "different source generation", replaceSourceFile: true},
		{name: "write failure with different source generation", failWrite: true, replaceSourceFile: true},
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
								if tc.failWrite {
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
				var wantCreator error
				if tc.failWrite {
					wantCreator = writeErr
				}
				if err := awaitSessionOperation(t, creator); !errors.Is(err, wantCreator) {
					t.Fatalf("creator returned %v, want %v", err, wantCreator)
				}
				if err := awaitSessionOperation(t, waiter); !errors.Is(err, errSessionSetupRetry) {
					t.Fatalf("waiter returned %v, want a transient setup error", err)
				}
				if writes.Load() != 1 {
					t.Fatal("waiter started a second setup")
				}
				if _, err := sessions.FilePath(t.Context(), key, src, 120, 1280, 720, false, masterPlaylistName); err != nil {
					t.Fatalf("fresh request after transient result: %v", err)
				}
				if writes.Load() != 2 {
					t.Fatal("fresh request did not start a new setup")
				}
			})
		})
	}
}

type setupWaitContext struct {
	context.Context
	latch *sessionIOLatch
	once  sync.Once
}

func (ctx *setupWaitContext) Done() <-chan struct{} {
	ctx.once.Do(ctx.latch.block)
	return ctx.Context.Done()
}

func TestFailedPlaylistSetupWaiterCannotActOnSuccessor(t *testing.T) {
	t.Run("live successor", func(t *testing.T) { testFailedPlaylistSetupWaiter(t, false) })
	t.Run("revoked successor", func(t *testing.T) { testFailedPlaylistSetupWaiter(t, true) })
}

func TestStaleSourceStatCannotStopSuccessorEncoder(t *testing.T) {
	transcoder := &scriptedTranscoder{start: func(_ int, dir string, _ int) []string {
		writeArtifact(t, dir, initSegmentName, []byte("init"))
		return []string{"sleep", "60"}
	}}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	latch := newSessionIOLatch(t)
	seedUnrelatedSessions(t, sessions)
	src := filepath.Join(t.TempDir(), "source.mkv")
	if err := os.WriteFile(src, []byte("old source"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := SessionKey{SessionID: "device", MediaID: "media"}
	request := func(name string) error {
		_, err := sessions.FilePath(t.Context(), key, src, 120, 1280, 720, false, name)
		return err
	}
	if err := request(masterPlaylistName); err != nil {
		t.Fatal(err)
	}
	old := registeredSession(t, sessions, key)
	if old.sourceInfo == nil {
		t.Fatal("fixture did not capture the initial source")
	}
	sessions.statSource = func(path string) (os.FileInfo, error) {
		info, err := os.Stat(path) // #nosec G703 -- fake source paths come from this test.
		if path == src && err == nil && sameSourceFile(old.sourceInfo, info) {
			latch.block()
		}
		return info, err
	}
	stale := make(chan error, 1)
	go func() { stale <- request(masterPlaylistName) }()
	latch.wait(t)
	assertUnrelatedSessionsProgress(t, sessions)

	next := src + ".replacement"
	if err := os.WriteFile(next, []byte("new source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, src); err != nil {
		t.Fatal(err)
	}
	if err := request("720p/init.mp4"); err != nil {
		t.Fatalf("start successor: %v", err)
	}
	successor := registeredSession(t, sessions, key)
	run := successor.run
	if successor == old || run == nil || !run.proc.Running() {
		t.Fatal("fixture did not start the successor encoder")
	}
	latch.unblock()
	if err := awaitSessionOperation(t, stale); !errors.Is(err, errSessionSetupRetry) {
		t.Fatalf("stale request returned %v, want a transient setup error", err)
	}
	if run.proc.StopRequested() || registeredSession(t, sessions, key) != successor {
		t.Fatal("stale source stat stopped the successor encoder")
	}
	select {
	case <-run.exited:
		t.Fatal("successor encoder exited")
	default:
	}
}

func testFailedPlaylistSetupWaiter(t *testing.T, revokeSuccessor bool) {
	t.Helper()
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	writeLatch := newSessionIOLatch(t)
	waiterLatch := newSessionIOLatch(t)
	writeErr := errors.New("playlist write failed")
	var writes atomic.Int32
	sessions.writePlaylists = func(dir string, rungs []Rung, duration int, timeline *keyframeTimeline, primed primedGrid, write func(string, []byte) error) error {
		if writes.Add(1) == 1 {
			writeLatch.block()
			return writeErr
		}
		return writeSessionPlaylists(dir, rungs, duration, timeline, primed, write)
	}
	stopped := make(chan *ffmpeg.HLSProcess, 2)
	sessions.stopEncoder = func(proc *ffmpeg.HLSProcess) { stopped <- proc }
	key := SessionKey{SessionID: "device", MediaID: "media"}
	request := func(ctx context.Context, duration int, name string) error {
		_, err := sessions.FilePath(ctx, key, "source.mkv", duration, 1280, 720, false, name)
		return err
	}
	creator := make(chan error, 1)
	go func() { creator <- request(t.Context(), 120, masterPlaylistName) }()
	writeLatch.wait(t)
	first := registeredSession(t, sessions, key)
	waiter := make(chan error, 1)
	go func() {
		waiter <- request(&setupWaitContext{Context: t.Context(), latch: waiterLatch}, 90, "720p/init.mp4")
	}()
	waiterLatch.wait(t)
	writeLatch.unblock()
	if err := awaitSessionOperation(t, creator); !errors.Is(err, writeErr) {
		t.Fatalf("creator returned %v, want %v", err, writeErr)
	}
	if err := request(t.Context(), 120, "720p/init.mp4"); err != nil {
		t.Fatalf("start successor: %v", err)
	}
	successor := registeredSession(t, sessions, key)
	encoder := successor.proc
	if successor == first || encoder == nil {
		t.Fatal("fixture did not install a successor with an encoder")
	}
	if revokeSuccessor {
		if n := sessions.TerminateSession(key.SessionID); n != 1 {
			t.Fatalf("terminated %d sessions, want 1", n)
		}
		select {
		case proc := <-stopped:
			if proc != encoder {
				t.Fatal("termination stopped the wrong encoder")
			}
		default:
			t.Fatal("termination left the successor's encoder running")
		}
	}
	waiterLatch.unblock()
	err := awaitSessionOperation(t, waiter)
	select {
	case proc := <-stopped:
		t.Fatalf("waiter stopped encoder %p, successor encoder %p", proc, encoder)
	default:
	}
	if !errors.Is(err, errSessionSetupRetry) {
		t.Fatalf("stale waiter returned %v, want a transient setup error", err)
	}
	if writes.Load() != 2 {
		t.Fatalf("stale waiter started a new setup, total playlist writes = %d", writes.Load())
	}
	if !revokeSuccessor && registeredSession(t, sessions, key) != successor {
		t.Fatal("stale waiter replaced the successor's encoder")
	}
	if revokeSuccessor && len(sessions.ActiveKeys()) != 0 {
		t.Fatal("stale waiter started an encoder after revocation")
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
				var wantCreator, wantWaiter error
				if writeFails {
					wantCreator, wantWaiter = writeErr, errSessionSetupRetry
				}
				if err := awaitSessionOperation(t, creator); !errors.Is(err, wantCreator) {
					t.Fatalf("composed creator returned %v, want %v", err, wantCreator)
				}
				if err := awaitSessionOperation(t, overlap); !errors.Is(err, wantWaiter) {
					t.Fatalf("composed waiter returned %v, want %v", err, wantWaiter)
				}
				if writeFails {
					if err := awaitSessionOperation(t, plain); !errors.Is(err, errSessionSetupRetry) {
						t.Fatalf("plain waiter returned %v, want a transient setup error", err)
					}
					if err := awaitSessionOperation(t, request(false)); err != nil {
						t.Fatalf("fresh plain setup: %v", err)
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

func TestOrphanSweepSkipsCurrentBootDuringDirectoryCollision(t *testing.T) {
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	latch := newSessionIOLatch(t)
	seedUnrelatedSessions(t, sessions)
	collision := filepath.Join(sessions.dir, fmt.Sprintf("session-%s-%d", sessions.bootNonce, sessions.nextID+1))
	if err := os.Mkdir(collision, 0o700); err != nil {
		t.Fatal(err)
	}
	sessions.mkdirSessionDir = func(path string, mode os.FileMode) error {
		if path == collision {
			latch.block()
		}
		return os.Mkdir(path, mode)
	}
	key := SessionKey{SessionID: "blocked-mkdir", MediaID: "media"}
	setup := make(chan error, 1)
	go func() {
		_, err := filePath720(sessions, key, masterPlaylistName)
		setup <- err
	}()
	latch.wait(t)
	finishesWhileBlocked(t, "sweep during directory allocation", sessions.PurgeOrphanedTranscodeDirs)
	assertUnrelatedSessionsProgress(t, sessions)
	latch.unblock()
	if err := awaitSessionOperation(t, setup); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(collision); err != nil {
		t.Fatalf("sweep touched the current boot's colliding directory: %v", err)
	}
	if registeredSession(t, sessions, key).dir == collision {
		t.Fatal("setup reused the colliding directory")
	}
}

func TestTranscodeReconcileFailsClosedOnTeardownFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
		defer sessions.Close()
		latch := newSessionIOLatch(t)
		defer latch.unblock()
		seedUnrelatedSessions(t, sessions)
		key := SessionKey{SessionID: "ending", MediaID: "media"}
		if _, err := filePath720(sessions, key, masterPlaylistName); err != nil {
			t.Fatal(err)
		}
		dir := registeredSession(t, sessions, key).dir
		var removals, reads atomic.Int32
		sessions.removeDir = func(path string) error {
			if path == dir {
				if removals.Add(1) == 1 {
					latch.block()
				}
				return os.ErrPermission
			}
			return os.RemoveAll(path)
		}
		sessions.readOrphanDirs = func(path string) ([]os.DirEntry, error) {
			reads.Add(1)
			return os.ReadDir(path)
		}

		detached := make(chan int, 1)
		go func() { detached <- sessions.TerminatePlayback(key.SessionID, key.MediaID) }()
		latch.wait(t)
		reconciled := make(chan error, 1)
		go func() { reconciled <- sessions.WaitForTeardowns(t.Context()) }()
		synctest.Wait()
		finishesWhileBlocked(t, "sweep during detached cleanup", sessions.PurgeOrphanedTranscodeDirs)
		if reads.Load() != 1 || removals.Load() != 1 {
			t.Fatal("the sweep did not finish independently of current-boot removal")
		}
		if err := sessions.CheckTeardowns(); err == nil {
			t.Fatal("the locked lifecycle check accepted an unfinished teardown")
		}
		select {
		case err := <-reconciled:
			t.Fatalf("reconcile returned before current-boot teardown finished: %v", err)
		default:
		}
		assertUnrelatedSessionsProgress(t, sessions)
		latch.unblock()
		if count := <-detached; count != 1 {
			t.Fatalf("detached %d sessions, want 1", count)
		}
		if err := awaitSessionOperation(t, reconciled); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("reconcile returned %v, want teardown failure", err)
		}
		if _, err := os.Stat(filepath.Join(dir, masterPlaylistName)); err != nil {
			t.Fatalf("fixture did not leave plaintext behind: %v", err)
		}
		if err := sessions.CheckTeardowns(); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("completed cleanup returned %v, want recorded removal failure", err)
		}
	})
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
