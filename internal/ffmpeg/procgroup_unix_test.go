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
// likely to land while a member is inside fork. Every child ends by itself
// after a few seconds, so the test never has to signal a group it does not own.
const forkingGroupScript = `i=0; while [ $i -lt 200 ]; do sleep 3 & i=$((i+1)); done; wait`

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
	var survived atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				outlived, err := superviseForkingGroup()
				if err != nil {
					t.Error(err)
					return
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
}

// superviseForkingGroup runs forkingGroupScript under supervise, cancels it
// after a short random delay, and reports whether any group member outlived
// supervise. Every process in the group inherits the write end of a witness
// pipe, so the read end reaches EOF only once all of them have exited. Zombies
// hold no descriptors, which keeps the check exact on Linux too.
func superviseForkingGroup() (outlived bool, err error) {
	r, w, err := os.Pipe()
	if err != nil {
		return false, err
	}
	defer r.Close()
	// #nosec G204 -- fixed shell and test-owned script.
	cmd := exec.Command("/bin/sh", "-c", forkingGroupScript)
	cmd.ExtraFiles = []*os.File{w}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	//nolint:gosec // jitter only, not security
	time.AfterFunc(time.Duration(rand.IntN(20000))*time.Microsecond, cancel)
	// No output pipes: a survivor must not stall Wait.
	superviseErr := supervise(ctx, cmd, nil, nil)
	_ = w.Close()
	if errors.Is(superviseErr, errGroupSurvived) ||
		superviseErr != nil && !errors.Is(superviseErr, context.Canceled) {
		return false, fmt.Errorf("supervise: %w", superviseErr)
	}
	if err := r.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return false, err
	}
	_, readErr := io.Copy(io.Discard, r)
	return readErr != nil, nil
}
