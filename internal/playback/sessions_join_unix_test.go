//go:build darwin || linux

package playback

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSessionsCloseDrainsInProgressFragmentJoin(t *testing.T) {
	t.Parallel()
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	key := SessionKey{SessionID: "join-close", MediaID: "media-a"}
	if _, err := filePath720(sessions, key, masterPlaylistName); err != nil {
		t.Fatal(err)
	}
	session := registeredSession(t, sessions, key)
	joined, release := blockFragmentJoin(t, session)
	sessions.removeDir = func(dir string) error {
		entries, err := filepath.Glob(filepath.Join(dir, "720p", ".join-*"))
		if err != nil || len(entries) != 0 {
			t.Errorf("teardown reached directory removal before join cleanup: %v, %v", entries, err)
		}
		return os.RemoveAll(dir)
	}

	closed := make(chan struct{})
	go func() {
		sessions.Close()
		close(closed)
	}()
	waitSessionCondition(t, "shutdown detached the joining session", session.detached.Load)
	select {
	case <-closed:
		t.Fatal("Close returned while the join held an open descriptor")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	assertJoinEvicted(t, joined)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the join drained")
	}
	if _, err := os.Stat(session.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session dir after Close: %v", err)
	}
}

func TestSessionsPreemptionDuringSessionIO(t *testing.T) {
	t.Parallel()
	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	sessions.SetTranscodeCap(1)
	t.Cleanup(sessions.Close)
	victim := SessionKey{SessionID: "join-victim", MediaID: "media-a"}
	if _, err := filePath720(sessions, victim, "720p/seg00000.m4s"); err != nil {
		t.Fatal(err)
	}
	session := registeredSession(t, sessions, victim)
	joined, release := blockFragmentJoin(t, session)
	// Hold the victim's state lock as publication would. Preemption must
	// detach from the map before it waits for either the lock or the join.
	session.mu.Lock()
	unlock := sync.OnceFunc(session.mu.Unlock)
	defer unlock()
	foreground := SessionKey{SessionID: "join-preemptor", MediaID: "media-b"}
	started := make(chan error, 1)
	go func() {
		_, err := filePath720Priority(sessions, foreground, PriorityForeground, "720p/seg00000.m4s")
		started <- err
	}()
	waitSessionCondition(t, "preemption detached the joining session", session.detached.Load)
	finishesWhileBlocked(t, "unrelated playlist request", func() error {
		other := SessionKey{SessionID: "join-bystander", MediaID: "media-c"}
		_, err := filePath720(sessions, other, masterPlaylistName)
		return err
	})
	unlock()
	if got := transcoder.startCount(); got != 1 {
		t.Fatalf("encoder starts while the victim is draining = %d, want 1", got)
	}
	if err := sessions.PurgeOrphanedTranscodeDirs(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(session.dir); err != nil {
		t.Fatalf("purge removed a directory still owned by teardown: %v", err)
	}
	release()
	assertJoinEvicted(t, joined)
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("foreground start: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("foreground start did not finish after the join drained")
	}
	if _, err := os.Stat(session.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("victim dir after preemption: %v", err)
	}
}

// blockFragmentJoin stalls the real join after it opens its temporary output.
func blockFragmentJoin(t *testing.T, session *hlsSession) (<-chan error, func()) {
	t.Helper()
	fragment := mediaSegment()
	input := t.TempDir()
	first := filepath.Join(input, "first.m4s")
	if err := os.WriteFile(first, fragment, 0o600); err != nil {
		t.Fatal(err)
	}
	pipe := filepath.Join(input, "second.m4s")
	if err := unix.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(session.dir, "720p")
	joined := make(chan error, 1)
	go func() {
		joined <- session.joinPlaylistFragments(filepath.Join(output, "joined00000.m4s"), []string{first, pipe})
	}()
	release := sync.OnceFunc(func() {
		var fd int
		var err error
		waitSessionCondition(t, "join opened its FIFO input", func() bool {
			fd, err = unix.Open(pipe, unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
			return !errors.Is(err, unix.ENXIO)
		})
		if err != nil {
			t.Fatal(err)
		}
		defer unix.Close(fd) //nolint:errcheck // Closing the writer releases the reader.
		if _, err := unix.Write(fd, fragment); err != nil {
			t.Fatal(err)
		}
	})
	t.Cleanup(release)
	waitSessionCondition(t, "join opened its temporary output", func() bool {
		entries, _ := filepath.Glob(filepath.Join(output, ".join-*"))
		return len(entries) == 1
	})
	return joined, release
}

func assertJoinEvicted(t *testing.T, joined <-chan error) {
	t.Helper()
	select {
	case err := <-joined:
		if !errors.Is(err, ErrSessionEvicted) {
			t.Fatalf("join err = %v, want ErrSessionEvicted", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("join did not finish")
	}
}
