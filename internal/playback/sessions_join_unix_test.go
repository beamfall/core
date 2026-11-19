//go:build darwin || linux

package playback

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/beamfall/core/internal/encderiv"
	"github.com/beamfall/core/internal/governor"
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
	joined, release := blockFragmentJoin(t, session, regularJoinPart(t))
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
	joined, release := blockFragmentJoin(t, session, regularJoinPart(t))
	removing := make(chan struct{})
	sessions.removeDir = func(dir string) error {
		if dir == session.dir {
			close(removing)
		}
		return os.RemoveAll(dir)
	}
	// Hold the victim's state lock as publication would. Preemption must
	// detach and start the new encoder while publication is blocked.
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
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("foreground start: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("foreground start waited for the blocked fragment join")
	}
	if got := transcoder.startCount(); got != 2 {
		t.Fatalf("encoder starts with the join blocked = %d, want 2", got)
	}
	select {
	case <-removing:
		t.Fatal("directory removal started while the join was blocked")
	default:
	}
	if err := sessions.PurgeOrphanedTranscodeDirs(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(session.dir); err != nil {
		t.Fatalf("purge removed a directory still owned by teardown: %v", err)
	}
	unlock()
	release()
	assertJoinEvicted(t, joined)
	select {
	case <-session.teardownDone:
	case <-time.After(5 * time.Second):
		t.Fatal("victim directory removal did not finish")
	}
	if _, err := os.Stat(session.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("victim dir after preemption: %v", err)
	}
}

func TestSessionsPreemptionDuringJoinPublication(t *testing.T) {
	t.Parallel()
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	sessions.SetTranscodeCap(1)
	t.Cleanup(sessions.Close)
	victim := SessionKey{SessionID: "join-publish-victim", MediaID: "media-a"}
	if _, err := filePath720(sessions, victim, "720p/seg00000.m4s"); err != nil {
		t.Fatal(err)
	}
	session := registeredSession(t, sessions, victim)
	part := regularJoinPart(t)
	dst := filepath.Join(session.dir, "720p", "joined00000.m4s")
	sessions.removeDir = func(dir string) error {
		if dir == session.dir {
			if _, err := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("joined artifact before directory removal: %v, want absent", err)
			}
		}
		return os.RemoveAll(dir)
	}
	entered, resume := make(chan string, 1), make(chan struct{})
	release := sync.OnceFunc(func() { close(resume) })
	defer release()
	joined := make(chan error, 1)
	go func() {
		joined <- session.joinPlaylistFragments(dst, []string{part, part}, readJoinFragment, func(from, to string) error {
			entered <- from
			<-resume
			return os.Rename(from, to)
		})
	}()
	select {
	case tmp := <-entered:
		if info, err := os.Stat(tmp); err != nil || info.Size() == 0 {
			t.Fatalf("rename reached without assembled fragments: %v, %v", info, err)
		}
	case err := <-joined:
		t.Fatalf("join ended before rename: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("join did not reach rename")
	}
	foreground := SessionKey{SessionID: "join-publish-preemptor", MediaID: "media-b"}
	started := make(chan error, 1)
	go func() {
		_, err := filePath720Priority(sessions, foreground, PriorityForeground, "720p/seg00000.m4s")
		started <- err
	}()
	waitSessionCondition(t, "preemption detached the session during rename", session.detached.Load)
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("foreground start: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("foreground start waited for the blocked fragment rename")
	}
	select {
	case <-session.teardownDone:
		t.Fatal("victim teardown finished while the fragment rename was blocked")
	default:
	}
	release()
	assertJoinEvicted(t, joined)
	select {
	case <-session.teardownDone:
	case <-time.After(5 * time.Second):
		t.Fatal("victim directory removal did not finish")
	}
}

func TestSessionsDetachedSpawnReapsDuringJoinPublication(t *testing.T) {
	t.Parallel()
	spawn := make(chan struct{})
	returnSpawn := sync.OnceFunc(func() { close(spawn) })
	launcher := &scriptedTranscoder{cmds: make(chan *exec.Cmd, 1)}
	launcher.start = func(_ int, dir string, _ int) []string {
		writeArtifact(t, dir, "seg00000.m4s", mediaSegment())
		return []string{"sleep", "60"}
	}
	g := governor.New()
	sessions := NewSessions(&pausedSpawn{scriptedTranscoder: launcher, release: spawn}, t.TempDir(), nil)
	sessions.SetTranscodeCap(1)
	sessions.SetGovernor(g)
	t.Cleanup(sessions.Close)
	defer returnSpawn()
	key := SessionKey{SessionID: "spawn-join-victim", MediaID: "media-a"}
	fetched := make(chan error, 1)
	go func() {
		_, err := filePath720(sessions, key, "720p/seg00000.m4s")
		fetched <- err
	}()
	var child *exec.Cmd
	select {
	case child = <-launcher.cmds:
	case err := <-fetched:
		t.Fatalf("spawn did not start: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("spawn did not reach the latch")
	}
	session := registeredSession(t, sessions, key)
	removing := make(chan struct{})
	sessions.removeDir = func(dir string) error {
		if dir == session.dir {
			close(removing)
		}
		return os.RemoveAll(dir)
	}
	part := regularJoinPart(t)
	entered, resume := make(chan struct{}), make(chan struct{})
	releaseRename := sync.OnceFunc(func() { close(resume) })
	defer releaseRename()
	joined := make(chan error, 1)
	go func() {
		dst := filepath.Join(session.dir, "720p", "joined00000.m4s")
		joined <- session.joinPlaylistFragments(dst, []string{part, part}, readJoinFragment, func(from, to string) error {
			close(entered)
			<-resume
			return os.Rename(from, to)
		})
	}()
	select {
	case <-entered:
	case err := <-joined:
		t.Fatalf("join ended before rename: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("join did not reach rename")
	}
	finishesWhileBlocked(t, "termination during spawn", func() error {
		if n := sessions.TerminateSession(key.SessionID); n != 1 {
			return fmt.Errorf("TerminateSession = %d, want 1", n)
		}
		return nil
	})
	other := SessionKey{SessionID: "spawn-join-next", MediaID: "media-b"}
	if sessions.CanAdmit(other) || g.ActiveTranscodes() != 1 {
		t.Fatal("pending spawn lost its slot or lease before returning the child")
	}
	returnSpawn()
	waitSessionCondition(t, "spawned child to release its slot and lease during rename", func() bool {
		return sessions.CanAdmit(other) && g.ActiveTranscodes() == 0
	})
	if child.ProcessState == nil {
		t.Fatal("slot released before the child was reaped")
	}
	finishesWhileBlocked(t, "start with the freed slot", func() error {
		_, err := filePath720(sessions, other, "720p/seg00000.m4s")
		return err
	})
	select {
	case <-removing:
		t.Fatal("directory removal started while the fragment rename was blocked")
	default:
	}
	releaseRename()
	assertJoinEvicted(t, joined)
	select {
	case err := <-fetched:
		if !errors.Is(err, ErrSessionEvicted) {
			t.Fatalf("fetch error = %v, want ErrSessionEvicted", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("detached fetch did not finish")
	}
	if _, err := os.Stat(session.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("victim directory after teardown: %v", err)
	}
}

func TestTerminateSessionRevokesAllReadersWhileJoinsDrain(t *testing.T) {
	t.Parallel()
	sessions := NewSessions(&fakeEncryptedTranscoder{}, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	secure := testEncryptedSource(nil)
	first := SessionKey{SessionID: "revoke-while-joining", MediaID: "media-a"}
	second := SessionKey{SessionID: first.SessionID, MediaID: "media-b"}

	openReader := func(key SessionKey) *encderiv.LiveFile {
		t.Helper()
		path, err := encryptedFilePath(sessions, key, secure, 120, -1, false, "720p/seg00000.m4s")
		if err != nil {
			t.Fatal(err)
		}
		reader, err := sessions.OpenLiveArtifact(key, path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reader.Close() })
		var buf [1]byte
		if _, err := io.ReadFull(reader, buf[:]); err != nil {
			t.Fatalf("%s: read before revocation: %v", key.MediaID, err)
		}
		return reader
	}
	firstReader, secondReader := openReader(first), openReader(second)

	// Stall both joins so the first teardown cannot finish, regardless of
	// which session TerminateSession finds first in the map.
	firstSession := registeredSession(t, sessions, first)
	firstJoin, releaseFirst := blockFragmentJoin(t, firstSession, regularJoinPart(t))
	secondJoin, releaseSecond := blockFragmentJoin(t, registeredSession(t, sessions, second), regularJoinPart(t))
	firstSession.mu.Lock()
	unlock := sync.OnceFunc(firstSession.mu.Unlock)
	defer unlock()
	terminated := make(chan int, 1)
	go func() { terminated <- sessions.TerminateSession(first.SessionID) }()

	waitSessionCondition(t, "both readers to close while both joins are blocked", func() bool {
		var buf [1]byte
		firstN, firstErr := firstReader.ReadAt(buf[:], 0)
		secondN, secondErr := secondReader.ReadAt(buf[:], 0)
		return firstN == 0 && errors.Is(firstErr, encderiv.ErrLiveSessionClosed) &&
			secondN == 0 && errors.Is(secondErr, encderiv.ErrLiveSessionClosed)
	})
	select {
	case n := <-terminated:
		t.Fatalf("TerminateSession returned %d while the joins were blocked", n)
	default:
	}
	unlock()
	releaseFirst()
	releaseSecond()
	assertJoinEvicted(t, firstJoin)
	assertJoinEvicted(t, secondJoin)
	select {
	case n := <-terminated:
		if n != 2 {
			t.Fatalf("TerminateSession = %d, want 2", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("TerminateSession did not return after the joins drained")
	}
}

func TestSessionsCloseRejectsFragmentFIFOWithoutWriter(t *testing.T) {
	t.Parallel()
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	key := SessionKey{SessionID: "join-fifo", MediaID: "media-a"}
	if _, err := filePath720(sessions, key, masterPlaylistName); err != nil {
		t.Fatal(err)
	}
	session := registeredSession(t, sessions, key)
	pipe := filepath.Join(t.TempDir(), "input.m4s")
	if err := unix.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	joined, release := blockFragmentJoin(t, session, pipe)

	closed := make(chan struct{})
	go func() {
		sessions.Close()
		close(closed)
	}()
	waitSessionCondition(t, "shutdown detached the session with a FIFO input", session.detached.Load)
	release()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked on a fragment FIFO with no writer")
	}
	select {
	case err := <-joined:
		if !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("join err = %v, want invalid fragment", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("join did not reject the FIFO")
	}
	if _, err := os.Stat(session.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session dir after Close: %v", err)
	}
}

func regularJoinPart(t *testing.T) string {
	t.Helper()
	part := filepath.Join(t.TempDir(), "input.m4s")
	if err := os.WriteFile(part, mediaSegment(), 0o600); err != nil {
		t.Fatal(err)
	}
	return part
}

// blockFragmentJoin stalls the real join after it opens its temporary output.
func blockFragmentJoin(t *testing.T, session *hlsSession, part string) (<-chan error, func()) {
	t.Helper()
	first := regularJoinPart(t)
	output := filepath.Join(session.dir, "720p")
	joined := make(chan error, 1)
	entered := make(chan struct{})
	resume := make(chan struct{})
	release := sync.OnceFunc(func() { close(resume) })
	t.Cleanup(release)
	go func() {
		readPart := func(path string) ([]byte, error) {
			if path == part {
				close(entered)
				<-resume
			}
			return readJoinFragment(path)
		}
		joined <- session.joinPlaylistFragments(filepath.Join(output, "joined00000.m4s"), []string{first, part}, readPart, os.Rename)
	}()
	select {
	case <-entered:
	case err := <-joined:
		t.Fatalf("join ended before reading its part: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("join did not reach its fragment read")
	}
	entries, err := filepath.Glob(filepath.Join(output, ".join-*"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("join scratch before detach: %v, %v", entries, err)
	}
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
