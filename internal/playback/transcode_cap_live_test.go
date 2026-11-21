//go:build unix

package playback

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
)

// liveProcessCounter counts encoder processes the way a process listing
// would: by whether the OS process still exists, not by the Sessions charge
// table. Each child holds the write end of its own pipe, and the kernel closes
// it when the process dies, so EOF on the read end is the death of that
// process. The read end is non-blocking, so a probe sees the state at that
// instant and gives a dying process no grace. No signal is sent to probe it.
type liveProcessCounter struct {
	mu       sync.Mutex
	alive    []childPipe
	starts   int
	peak     int
	probeErr error
}

// childPipe is the read end of one child's liveness pipe and its raw
// non-blocking descriptor.
type childPipe struct {
	file *os.File
	fd   int
}

// launch starts one held child, then counts the children alive with it, so
// the peak never includes a child that died before the new one existed.
func (c *liveProcessCounter) launch(ctx context.Context) (*ffmpeg.HLSProcess, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	fd := int(r.Fd()) // #nosec G115 -- a descriptor fits in int
	if err := syscall.SetNonblock(fd, true); err != nil {
		_, _ = r.Close(), w.Close()
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cmd := exec.CommandContext(ctx, "sleep", "60")
	cmd.ExtraFiles = []*os.File{w}
	proc, err := ffmpeg.StartHLSCommand(cmd)
	_ = w.Close()
	if err != nil {
		_ = r.Close()
		return nil, err
	}
	c.alive = append(c.alive, childPipe{file: r, fd: fd})
	c.pruneLocked()
	c.starts++
	c.peak = max(c.peak, len(c.alive))
	return proc, nil
}

// pruneLocked drops every child whose pipe reads EOF. A live child's empty
// pipe reports EAGAIN instead. Any other result keeps the child and is
// recorded, and result fails the test on it.
func (c *liveProcessCounter) pruneLocked() {
	kept := c.alive[:0]
	for _, child := range c.alive {
		n, err := syscall.Read(child.fd, make([]byte, 1))
		for errors.Is(err, syscall.EINTR) {
			n, err = syscall.Read(child.fd, make([]byte, 1))
		}
		switch {
		case n == 0 && err == nil:
			_ = child.file.Close()
			continue
		case !errors.Is(err, syscall.EAGAIN) && c.probeErr == nil:
			c.probeErr = fmt.Errorf("liveness probe read %d bytes, err %v", n, err)
		}
		kept = append(kept, child)
	}
	c.alive = kept
}

func (c *liveProcessCounter) result(t *testing.T) (starts, peak, alive int) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked()
	if c.probeErr != nil {
		t.Fatal(c.probeErr)
	}
	return c.starts, c.peak, len(c.alive)
}

func (c *liveProcessCounter) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, child := range c.alive {
		_ = child.file.Close()
	}
	c.alive = nil
}

// TestLiveEncoderProcessesNeverExceedTheCapAcrossRestarts is the CORE-2165
// evidence. Two normal sessions keep switching renditions, so every request
// replaces a running process, while a third normal session asks for a slot
// over and over; then a foreground session preempts one of them at the full
// cap. Every encoder stop is slowed, so a process outlives its stop request
// as a real FFmpeg does. A replacement or a preemption may launch only after
// the process it replaces is gone, so the OS never holds more encoder
// processes than the cap.
//
// Not parallel: a child forked by another test while a liveness pipe's write
// end is open would hold it until exec and delay that pipe's EOF.
func TestLiveEncoderProcessesNeverExceedTheCapAcrossRestarts(t *testing.T) {
	const capLimit = 2
	counter := &liveProcessCounter{}
	t.Cleanup(counter.close)
	transcoder := &fakeTranscoder{holdChild: true, segmentsToLay: 1, launch: counter.launch}
	sessions := newTestSessions(t, transcoder, t.TempDir(), nil)
	defer sessions.Close()
	sessions.SetTranscodeCap(capLimit)
	sessions.stopEncoder = func(proc *ffmpeg.HLSProcess) {
		time.Sleep(30 * time.Millisecond)
		proc.Stop()
	}

	switchers := []SessionKey{
		{SessionID: "session-a", MediaID: "media-alpha"},
		{SessionID: "session-b", MediaID: "media-alpha"},
	}
	for _, key := range switchers {
		if _, err := filePath720(sessions, key, "720p/seg00000.m4s"); err != nil {
			t.Fatalf("%s first start: %v", key.SessionID, err)
		}
	}

	const rounds = 12
	// Every request alternates rungs, so an admitted one always replaces the
	// running process: the fake writes only the segment it starts at.
	segment := func(i int) string {
		rung := "480p"
		if i%2 == 0 {
			rung = "720p"
		}
		return fmt.Sprintf("%s/seg%05d.m4s", rung, i)
	}
	errs := make(chan error, 3*rounds)
	var wg sync.WaitGroup
	for _, key := range switchers {
		wg.Go(func() {
			for i := 1; i <= rounds; i++ {
				if _, err := filePath720(sessions, key, segment(i)); err != nil {
					errs <- fmt.Errorf("%s round %d: %w", key.SessionID, i, err)
				}
			}
		})
	}
	wg.Go(func() {
		// Both slots belong to actively-streaming sessions of equal rank, so
		// every attempt is refused.
		newcomer := SessionKey{SessionID: "session-c", MediaID: "media-alpha"}
		for i := range rounds {
			if _, err := filePath720(sessions, newcomer, segment(i)); !errors.Is(err, ErrTranscodeCapacity) {
				errs <- fmt.Errorf("normal newcomer round %d: %v, want ErrTranscodeCapacity", i, err)
			}
		}
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	starts, peak, _ := counter.result(t)
	if want := 2 + 2*rounds; starts != want {
		t.Fatalf("encoder launches = %d, want %d (two starts and every rendition restart)", starts, want)
	}
	if peak > capLimit {
		t.Fatalf("peak live encoder processes across restarts = %d, above the cap of %d", peak, capLimit)
	}

	// A foreground stream outranks both normal ones at the full cap: it is
	// admitted, and its process launches only once its victim's has exited.
	if _, _, alive := counter.result(t); alive != capLimit {
		t.Fatalf("live encoder processes before the foreground start = %d, want the full cap of %d", alive, capLimit)
	}
	foreground := SessionKey{SessionID: "session-d", MediaID: "media-alpha"}
	if _, err := filePath720Priority(sessions, foreground, PriorityForeground, segment(0)); err != nil {
		t.Fatalf("foreground at the full cap: %v", err)
	}
	starts, peak, alive := counter.result(t)
	if want := 3 + 2*rounds; starts != want {
		t.Fatalf("encoder launches = %d, want %d with the foreground start", starts, want)
	}
	if alive != capLimit {
		t.Fatalf("live encoder processes after the foreground start = %d, want %d", alive, capLimit)
	}
	charged := map[string]bool{}
	for _, key := range chargedKeys(sessions) {
		charged[string(key.SessionID)] = true
	}
	if len(charged) != capLimit || !charged["session-d"] || charged["session-a"] == charged["session-b"] {
		t.Fatalf("charged sessions = %v, want session-d and exactly one switcher", charged)
	}
	if peak != capLimit {
		t.Fatalf("peak live encoder processes = %d, want exactly the cap of %d", peak, capLimit)
	}

	_ = sessions.Close()
	if _, _, alive := counter.result(t); alive != 0 {
		t.Fatalf("live encoder processes after Close = %d, want 0", alive)
	}
}
