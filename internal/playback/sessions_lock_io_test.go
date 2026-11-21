package playback

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ioLatch parks the first filesystem call it is armed for until released.
type ioLatch struct {
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	open    func()
}

func newIOLatch(t *testing.T) *ioLatch {
	t.Helper()
	l := &ioLatch{entered: make(chan struct{}), release: make(chan struct{})}
	l.open = sync.OnceFunc(func() { close(l.release) })
	t.Cleanup(l.open)
	return l
}

// park blocks the first armed caller; every other call passes through.
func (l *ioLatch) park() {
	if !l.armed.CompareAndSwap(true, false) {
		return
	}
	l.once.Do(func() { close(l.entered) })
	<-l.release
}

func (l *ioLatch) waitEntered(t *testing.T, what string) {
	t.Helper()
	select {
	case <-l.entered:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s never reached its blocked filesystem call", what)
	}
}

// completesWhileBlocked fails the test when request does not return while
// another session's filesystem call is parked.
func completesWhileBlocked(t *testing.T, what string, request func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- request() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s stalled behind another session's filesystem I/O", what)
	}
}

// requireUnrelatedProgress proves that Sessions.mu is free and that an
// already-running session and a brand-new session both make progress.
func requireUnrelatedProgress(t *testing.T, s *Sessions, bystander SessionKey, segment string) {
	t.Helper()
	completesWhileBlocked(t, "Sessions.mu", func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		return nil
	})
	completesWhileBlocked(t, "running session restart", func() error {
		_, err := filePath720(s, bystander, segment)
		return err
	})
	completesWhileBlocked(t, "new session creation", func() error {
		_, err := filePath720(s, SessionKey{SessionID: "newcomer", MediaID: "newcomer"}, "720p/seg00000.m4s")
		return err
	})
}

// TestSessionsBlockedSessionIODoesNotStallOthers parks one owner's filesystem
// call at each site that used to run under Sessions.mu (CORE-2636) and
// requires unrelated sessions to keep being served meanwhile.
func TestSessionsBlockedSessionIODoesNotStallOthers(t *testing.T) {
	type fixture struct {
		s       *Sessions
		latch   *ioLatch
		slowSrc string
		slow    SessionKey
	}
	createSlow := func(f fixture) func() error {
		return func() error {
			_, err := f.s.FilePath(context.Background(), f.slow, f.slowSrc, 120, 1280, 720, false, masterPlaylistName)
			return err
		}
	}
	type ioCase struct {
		name string
		// install wires the latch into one filesystem hook before any request.
		install func(f fixture)
		// block arms the latch and starts the slow operation; it returns the
		// operation's result channel.
		block func(t *testing.T, f fixture) func() error
	}
	cases := []ioCase{
		{
			name: "scratch mkdir during session creation",
			install: func(f fixture) {
				f.s.mkdirDir = func(path string, mode fs.FileMode) error {
					f.latch.park()
					return os.Mkdir(path, mode)
				}
			},
			block: func(_ *testing.T, f fixture) func() error { return createSlow(f) },
		},
		{
			name: "playlist write during session creation",
			install: func(f fixture) {
				f.s.writeSetupPlaylist = func(session *hlsSession, rel string, data []byte) error {
					if session.key == f.slow && rel == masterPlaylistName {
						f.latch.park()
					}
					return session.writePlaylist(rel, data)
				}
			},
			block: func(_ *testing.T, f fixture) func() error { return createSlow(f) },
		},
		{
			name:    "source end inspection",
			install: func(fixture) {},
			block: func(t *testing.T, f fixture) func() error {
				f.latch.armed.Store(false)
				if err := createSlow(f)(); err != nil {
					t.Fatal(err)
				}
				session := registeredSession(t, f.s, f.slow)
				f.latch.armed.Store(true)
				return func() error {
					_, _, err := f.s.observeIncompleteSource(context.Background(), f.slow, session, artifactObservation{}, false, func() (time.Duration, bool) {
						f.latch.park()
						return 0, false
					})
					return err
				}
			},
		},
		{
			name: "orphan purge listing",
			install: func(f fixture) {
				f.s.readScratch = func(root fs.FS, name string) ([]fs.DirEntry, error) {
					f.latch.park()
					return fs.ReadDir(root, name)
				}
			},
			block: func(_ *testing.T, f fixture) func() error { return f.s.PurgeOrphanedTranscodeDirs },
		},
		{
			name: "scratch removal during teardown",
			install: func(f fixture) {
				f.s.removeDir = func(path string) error {
					f.latch.park()
					return os.RemoveAll(path) // #nosec G703 -- path is the test-owned scratch dir.
				}
			},
			block: func(t *testing.T, f fixture) func() error {
				f.latch.armed.Store(false)
				if err := createSlow(f)(); err != nil {
					t.Fatal(err)
				}
				dir := registeredSession(t, f.s, f.slow).dir
				f.latch.armed.Store(true)
				return func() error {
					if n := f.s.TerminateSession(f.slow.SessionID); n != 1 {
						return errors.New("teardown did not detach the slow session")
					}
					if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
						return errors.New("teardown left the slow session's directory")
					}
					return nil
				}
			},
		},
	}
	// Session creation stats the source three times: the initial capture, the
	// pre-setup recheck and the promotion recheck. Park each in turn.
	for call, stage := range []string{"initial capture", "pre-setup recheck", "promotion recheck"} {
		cases = append(cases, ioCase{
			name: "source stat " + stage,
			install: func(f fixture) {
				var calls atomic.Int32
				f.s.statSource = func(path string) (os.FileInfo, error) {
					if path == f.slowSrc && int(calls.Add(1)) == call+1 {
						f.latch.park()
					}
					return os.Stat(path) // #nosec G703 -- test-owned fixture path.
				}
			},
			block: func(_ *testing.T, f fixture) func() error { return createSlow(f) },
		})
	}
	// ensureRung checks readiness before deciding to start an encoder, and again
	// after a frontier scan when a seek lands past the produced window.
	for check, stage := range []string{"initial check", "post-scan recheck"} {
		cases = append(cases, ioCase{
			name: "ensureRung artifact " + stage,
			install: func(f fixture) {
				var calls atomic.Int32
				f.s.rungReady = func(session *hlsSession, path string) bool {
					if session.key == f.slow && f.latch.armed.Load() && int(calls.Add(1)) == check+1 {
						f.latch.park()
					}
					return session.artifactReady(path)
				}
			},
			block: func(t *testing.T, f fixture) func() error {
				f.latch.armed.Store(false)
				if _, err := filePath720(f.s, f.slow, "720p/seg00000.m4s"); err != nil {
					t.Fatal(err)
				}
				f.latch.armed.Store(true)
				return func() error {
					_, err := filePath720(f.s, f.slow, "720p/seg00012.m4s")
					return err
				}
			},
		})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestSessions(t, &fakeTranscoder{holdChild: true}, t.TempDir(), nil)
			f := fixture{
				s:       s,
				latch:   newIOLatch(t),
				slowSrc: filepath.Join(t.TempDir(), "slow-source"),
				slow:    SessionKey{SessionID: "slow", MediaID: "slow"},
			}
			seedScratchFile(t, f.slowSrc)
			bystander := SessionKey{SessionID: "bystander", MediaID: "bystander"}
			if _, err := filePath720(s, bystander, "720p/seg00000.m4s"); err != nil {
				t.Fatal(err)
			}
			tc.install(f)
			f.latch.armed.Store(true)
			operation := tc.block(t, f)
			result := make(chan error, 1)
			go func() { result <- operation() }()
			f.latch.waitEntered(t, tc.name)

			requireUnrelatedProgress(t, s, bystander, "720p/seg00015.m4s")

			f.latch.open()
			select {
			case err := <-result:
				if err != nil {
					t.Fatalf("%s after release: %v", tc.name, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s did not finish after release", tc.name)
			}
		})
	}
}

// TestSessionsSlowInitializationAllowsUnrelatedSessionsAndDrainsOnClose is
// the unpaced form of the CORE-0022 seed test: a session whose directory
// setup is stuck in a playlist write holds no global lock, and Close waits
// for that setup to drain and removes its directory.
func TestSessionsSlowInitializationAllowsUnrelatedSessionsAndDrainsOnClose(t *testing.T) {
	s := newTestSessions(t, &fakeTranscoder{holdChild: true}, t.TempDir(), nil)
	bystander := SessionKey{SessionID: "bystander", MediaID: "bystander"}
	slow := SessionKey{SessionID: "slow", MediaID: "slow"}
	if _, err := filePath720(s, bystander, "720p/seg00000.m4s"); err != nil {
		t.Fatal(err)
	}
	latch := newIOLatch(t)
	var slowDir atomic.Pointer[string]
	s.writeSetupPlaylist = func(session *hlsSession, rel string, data []byte) error {
		if session.key == slow && rel == masterPlaylistName {
			slowDir.Store(&session.dir)
			latch.park()
		}
		return session.writePlaylist(rel, data)
	}
	latch.armed.Store(true)
	initialized := make(chan error, 1)
	go func() {
		_, err := filePath720(s, slow, masterPlaylistName)
		initialized <- err
	}()
	latch.waitEntered(t, "slow initialization")

	requireUnrelatedProgress(t, s, bystander, "720p/seg00015.m4s")

	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	// Close parks on offLockIdle only once teardowns are done and it is waiting
	// for the in-flight initialization itself.
	idle := waitScratchCloseBlocked(t, s)
	select {
	case err := <-closed:
		t.Fatalf("Close returned before the slow initialization drained: %v", err)
	case <-idle:
		t.Fatal("Close saw off-lock work idle while initialization was parked")
	case <-time.After(100 * time.Millisecond):
	}
	latch.open()
	select {
	case err := <-initialized:
		if !errors.Is(err, ErrSessionEvicted) {
			t.Fatalf("initialization after Close = %v, want ErrSessionEvicted", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("slow initialization did not finish after release")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the initialization drained")
	}
	requireScratchMissing(t, *slowDir.Load())
}
