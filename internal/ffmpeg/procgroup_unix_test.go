//go:build linux || darwin

package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// forkingGroupScript forks children as fast as it can, so a group KILL is
// likely to land while a member is inside fork. It writes one readiness byte
// to fd 3 after its first fork, so a test can hold the KILL until forking is
// under way. Every child ends by itself after a few seconds, so the test never
// has to signal a group it does not own.
const forkingGroupScript = `sleep 3 & printf r >&3; i=1; while [ $i -lt 200 ]; do sleep 3 & i=$((i+1)); done; wait`

// minExercisedShare is the share of rounds whose KILL must land after the
// readiness byte; fewer means the stress test stopped testing the race.
const minExercisedShare = 0.75

// TestSupervisorKillLeavesNoMidForkSurvivor is the CORE-2716 regression: on
// macOS a single group SIGKILL misses a member's child that is still being
// forked, and that child outlives the supervisor. Concurrent groups add the
// load the miss needs.
func TestSupervisorKillLeavesNoMidForkSurvivor(t *testing.T) {
	if testing.Short() {
		t.Skip("forks thousands of processes")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("the mid-fork miss is an XNU behaviour; Linux restarts the fork")
	}
	const (
		workers = 8
		rounds  = 40
	)
	var survived, exercised atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				outlived, ready, err := superviseForkingGroup()
				if err != nil {
					t.Error(err)
					return
				}
				if ready {
					exercised.Add(1)
				}
				if outlived {
					survived.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if n := survived.Load(); n > 0 {
		t.Fatalf("%d of %d killed process groups kept a live member", n, workers*rounds)
	}
	if n := exercised.Load(); float64(n) < minExercisedShare*workers*rounds {
		t.Fatalf("only %d of %d rounds killed a group that was forking", n, workers*rounds)
	}
}

// superviseForkingGroup runs forkingGroupScript under supervise, cancels it
// after a short random delay, and reports whether any group member outlived
// supervise. Every process in the group inherits the write end of a witness
// pipe, so the read end reaches EOF only once all of them have exited. Zombies
// hold no descriptors, which keeps the check exact on Linux too. The cancel
// waits for the script's readiness byte, and ready reports whether it came.
func superviseForkingGroup() (outlived, ready bool, err error) {
	r, w, err := os.Pipe()
	if err != nil {
		return false, false, err
	}
	defer r.Close()
	// #nosec G204 -- fixed shell and test-owned script.
	cmd := exec.Command("/bin/sh", "-c", forkingGroupScript)
	cmd.ExtraFiles = []*os.File{w}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readyCh := make(chan bool, 1)
	go func() {
		var b [1]byte
		_, readErr := io.ReadFull(r, b[:])
		if readErr == nil {
			//nolint:gosec // jitter only, not security
			time.Sleep(time.Duration(rand.IntN(20000)) * time.Microsecond)
		}
		readyCh <- readErr == nil
		cancel()
	}()
	// No output pipes: a survivor must not stall Wait.
	superviseErr := supervise(ctx, cmd, nil, nil)
	_ = w.Close()
	// The deadline also ends the readiness read if the byte never came.
	if err := r.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return false, false, err
	}
	ready = <-readyCh
	if errors.Is(superviseErr, errGroupSurvived) ||
		superviseErr != nil && !errors.Is(superviseErr, context.Canceled) {
		return false, ready, fmt.Errorf("supervise: %w", superviseErr)
	}
	_, readErr := io.Copy(io.Discard, r)
	return readErr != nil, ready, nil
}

// TestProcessGroupKillAfterReapSignalsNothing is the CORE-2749 regression: an
// overflow callback that runs only after the supervisor reaped the leader must
// not signal the group, whose ID may already belong to another process.
func TestProcessGroupKillAfterReapSignalsNothing(t *testing.T) {
	var signals atomic.Int64
	orig := signalProcessGroup
	signalProcessGroup = func(cmd *exec.Cmd) {
		signals.Add(1)
		orig(cmd)
	}
	t.Cleanup(func() { signalProcessGroup = orig })

	// #nosec G204 -- fixed shell and test-owned script.
	cmd := exec.Command("/bin/sh", "-c", "printf ok")
	group := newProcessGroup(cmd)
	group.kill()
	if n := signals.Load(); n != 0 {
		t.Fatalf("kill before Start sent %d signals", n)
	}
	fired := make(chan struct{})
	late := &boundedBuffer{limit: 1, onOverflow: func() {
		group.kill()
		close(fired)
	}}
	if err := superviseGroup(context.Background(), group, io.Discard, io.Discard); err != nil {
		t.Fatalf("supervise: %v", err)
	}
	before := signals.Load()
	_, _ = late.Write([]byte("overflow"))
	<-fired
	if n := signals.Load(); n != before {
		t.Fatalf("kill after the reap sent %d signals", n-before)
	}
}
