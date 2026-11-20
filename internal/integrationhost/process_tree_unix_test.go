//go:build linux || darwin

package integrationhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestSupervisorRetiresCompleteWorkerTree(t *testing.T) {
	sentinel := startSentinel(t)
	tests := []struct {
		name      string
		mode      string
		resistant bool
		cancel    bool
	}{
		{name: "success normal grandchild", mode: "success"},
		{name: "success resistant grandchild", mode: "success", resistant: true},
		{name: "failure normal grandchild", mode: "failure"},
		{name: "failure resistant grandchild", mode: "failure", resistant: true},
		{name: "cancel normal grandchild", mode: "hang", cancel: true},
		{name: "cancel resistant grandchild", mode: "hang", resistant: true, cancel: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "worker.pids")
			worker := treeWorkerPath
			digest, err := HashFile(worker)
			if err != nil {
				t.Fatal(err)
			}
			host := NewHost("tree-test", Deps{}, fixedGrants{}, nil)
			supervisor := NewSupervisor(
				"tree-test",
				Artifact{
					PluginID: "tree-test", Path: worker, VerifiedSHA256: digest,
					Args:          []string{"--tree-mode", tc.mode, "--pid-file", pidFile, "--hold-file", pidFile + ".release", fmt.Sprintf("--term-resistant=%t", tc.resistant)},
					WritablePaths: []string{filepath.Dir(pidFile)},
				},
				host,
				nil,
				nil,
			)
			supervisor.SetHardeningShim(beamfallShimPath)

			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(context.Canceled)
			done := make(chan error, 1)
			go func() { done <- supervisor.Run(ctx) }()
			pids := hostPIDs(t, waitFixturePIDs(t, pidFile), os.Getpid())
			assertOwnedProcessGroup(t, pids, sentinel.Process.Pid)
			releaseFixture(t, pidFile)
			if tc.cancel {
				cancel(ErrPluginDeactivated)
			}
			err = waitResult(t, done)
			cancel(context.Canceled)
			if tc.cancel {
				if !errors.Is(err, ErrPluginDeactivated) {
					t.Fatalf("Run error = %v, want plugin-deactivated cancellation", err)
				}
			} else if !errors.Is(err, ErrWorkerExited) {
				t.Fatalf("Run error = %v, want ErrWorkerExited", err)
			}
			assertPIDsGone(t, pids)
			assertProcessAlive(t, sentinel.Process.Pid)
		})
	}
}

func TestLocalInferenceCancellationRetiresCompleteTree(t *testing.T) {
	req := localInferenceRequest(t)
	pidFile := filepath.Join(req.WorkRoot, "inference.pids")
	req.Binary = treeWorkerPath
	req.Args = []string{
		"--tree-mode", "hang", "--pid-file", pidFile,
		"--hold-file", pidFile + ".release", "--term-resistant=true",
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan error, 1)
	if runtime.GOOS == "darwin" {
		// sandbox-exec cannot read the separately built fixture from its private
		// test directory; exercise the identical tree supervisor directly.
		// #nosec G204 -- treeWorkerPath and all arguments are test-owned.
		cmd := exec.CommandContext(ctx, treeWorkerPath, req.Args...)
		go func() { done <- superviseWorker(ctx, cmd, nil) }()
	} else {
		go func() {
			_, err := (LocalInferenceRuntime{ShimPath: beamfallShimPath}).Run(ctx, req)
			done <- err
		}()
	}
	pids := hostPIDs(t, waitFixturePIDs(t, pidFile), os.Getpid())
	releaseFixture(t, pidFile)
	cancel(context.Canceled)
	if err := waitResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("local inference cancellation = %v, want context canceled", err)
	}
	assertPIDsGone(t, pids)
}

func TestSupervisorSignalAndUnexpectedParentContainment(t *testing.T) {
	sentinel := startSentinel(t)
	tests := []struct {
		name      string
		signal    syscall.Signal
		resistant bool
	}{
		{name: "interrupt normal grandchild", signal: syscall.SIGINT},
		{name: "interrupt resistant grandchild", signal: syscall.SIGINT, resistant: true},
		{name: "terminate normal grandchild", signal: syscall.SIGTERM},
		{name: "terminate resistant grandchild", signal: syscall.SIGTERM, resistant: true},
		{name: "unexpected parent death normal grandchild", signal: syscall.SIGKILL},
		{name: "unexpected parent death resistant grandchild", signal: syscall.SIGKILL, resistant: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "worker.pids")
			worker := treeWorkerPath
			// #nosec G204 -- supervisorFixturePath is built by TestMain; all arguments are test temp paths.
			harness := exec.Command(
				supervisorFixturePath,
				"--worker", worker,
				"--shim", beamfallShimPath,
				"--scratch", filepath.Dir(pidFile),
				"--pid-file", pidFile,
				"--term-resistant="+strconv.FormatBool(tc.resistant),
			)
			if err := harness.Start(); err != nil {
				t.Fatalf("start supervisor fixture: %v", err)
			}
			t.Cleanup(func() {
				if harness.ProcessState == nil {
					_ = harness.Process.Kill()
					_, _ = harness.Process.Wait()
				}
			})
			pids := hostPIDs(t, waitFixturePIDs(t, pidFile), harness.Process.Pid)
			assertOwnedProcessGroup(t, pids, sentinel.Process.Pid)
			releaseFixture(t, pidFile)
			if err := harness.Process.Signal(tc.signal); err != nil {
				t.Fatalf("signal supervisor fixture: %v", err)
			}
			harnessDone := make(chan error, 1)
			go func() { harnessDone <- harness.Wait() }()
			_ = waitResult(t, harnessDone)
			assertPIDsGone(t, pids)
			assertProcessAlive(t, sentinel.Process.Pid)
		})
	}
}

func releaseFixture(t *testing.T, pidFile string) {
	t.Helper()
	if err := os.WriteFile(pidFile+".release", []byte("release\n"), 0o600); err != nil {
		t.Fatalf("release fixture: %v", err)
	}
}

func startSentinel(t *testing.T) *exec.Cmd {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "sentinel.ready")
	// #nosec G204 -- treeWorkerPath is built by TestMain and receives only fixed test arguments.
	cmd := exec.Command(
		treeWorkerPath,
		"--descendant",
		"--term-resistant",
		"--ready-file", ready,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sentinel: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	waitForPath(t, ready)
	return cmd
}

func waitFixturePIDs(t *testing.T, path string) []int {
	t.Helper()
	waitForPath(t, path)
	// #nosec G304 -- path is fixture PID evidence created under this test's t.TempDir.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture pids: %v", err)
	}
	var pids []int
	for _, field := range strings.Fields(string(raw)) {
		pid, err := strconv.Atoi(field)
		if err != nil {
			t.Fatalf("parse fixture pid %q: %v", field, err)
		}
		pids = append(pids, pid)
	}
	if len(pids) != 2 {
		t.Fatalf("fixture pids = %v, want worker and grandchild", pids)
	}
	return pids
}

func waitForPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

// hostPIDs maps the pids a fixture reported about itself to host pids. On Linux
// the worker runs in a bwrap PID namespace, so it reports namespace pids (the
// shim is 1, the worker 2) that mean nothing, or the wrong process, to the test.
// The kernel publishes the mapping as NSpid in /proc/<pid>/status: among
// descendants of root, the process whose innermost NSpid equals the reported pid
// is the fixture. Elsewhere reported pids already are host pids.
func hostPIDs(t *testing.T, reported []int, root int) []int {
	t.Helper()
	if runtime.GOOS != "linux" {
		return reported
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatalf("read /proc: %v", err)
	}
	parent := map[int]int{}
	nspid := map[int][]int{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		// #nosec G304 -- pid comes from numeric /proc entry names.
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "status"))
		if err != nil {
			continue // exited while scanning
		}
		for _, line := range strings.Split(string(raw), "\n") {
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			switch key {
			case "PPid":
				parent[pid], _ = strconv.Atoi(strings.TrimSpace(value))
			case "NSpid":
				for _, field := range strings.Fields(value) {
					n, _ := strconv.Atoi(field)
					nspid[pid] = append(nspid[pid], n)
				}
			}
		}
	}
	descendsFromRoot := func(pid int) bool {
		for hops := 0; pid > 1 && hops < 64; hops++ {
			if pid == root {
				return true
			}
			pid = parent[pid]
		}
		return false
	}
	mapped := make([]int, 0, len(reported))
	for _, want := range reported {
		found := 0
		for pid, chain := range nspid {
			if len(chain) > 0 && chain[len(chain)-1] == want && descendsFromRoot(pid) {
				if found != 0 {
					t.Fatalf("reported pid %d maps to host pids %d and %d under %d", want, found, pid, root)
				}
				found = pid
			}
		}
		if found == 0 {
			t.Fatalf("reported pid %d has no host process under %d", want, root)
		}
		mapped = append(mapped, found)
	}
	return mapped
}

func assertOwnedProcessGroup(t *testing.T, pids []int, sentinelPID int) {
	t.Helper()
	want := pids[0]
	if runtime.GOOS == "linux" {
		// The supervisor owns the group of the sandbox launcher, an ancestor of
		// the namespaced worker, so the worker is a member but not its leader.
		group, err := syscall.Getpgid(pids[0])
		if err != nil {
			t.Fatalf("getpgid(%d): %v", pids[0], err)
		}
		if group == syscall.Getpgrp() {
			t.Fatalf("worker %d stayed in the test's own process group %d", pids[0], group)
		}
		want = group
	}
	for _, pid := range pids {
		pgid, err := syscall.Getpgid(pid)
		if err != nil {
			t.Fatalf("getpgid(%d): %v", pid, err)
		}
		if pgid != want {
			t.Fatalf("pid %d pgid = %d, want birth-identity pgid %d", pid, pgid, want)
		}
	}
	sentinelPGID, err := syscall.Getpgid(sentinelPID)
	if err != nil {
		t.Fatalf("getpgid(sentinel %d): %v", sentinelPID, err)
	}
	if sentinelPGID == want {
		t.Fatalf("sentinel unexpectedly joined owned process group %d", want)
	}
}

func assertPIDsGone(t *testing.T, pids []int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		allGone := true
		for _, pid := range pids {
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				allGone = false
				break
			}
		}
		if allGone {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("owned pids still exist after containment: %v", pids)
}

func assertProcessAlive(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("unrelated sentinel %d did not survive: %v", pid, err)
	}
}

func waitResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(8 * time.Second):
		t.Fatal("process containment did not finish")
		return nil
	}
}

// TestRetireKillsMidForkChildren is the CORE-2716 regression: on macOS one
// group SIGKILL misses a member's child that is still being forked. The
// workers ignore TERM, as the shim guardian does, so retire reaches its KILL
// while they fork; concurrent trees add the load the miss needs.
func TestRetireKillsMidForkChildren(t *testing.T) {
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
				outlived, retireErr, err := retireForkingTree()
				// Off macOS a PID 1 that leaves orphans unreaped can hold
				// zombies in the group past the deadline; the witness still
				// decides survival there.
				if retireErr != nil {
					t.Errorf("retire: %v", retireErr)
				}
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
		t.Fatalf("%d of %d retired worker trees kept a live member", n, workers*rounds)
	}
}

// retireForkingTree starts a TERM-ignoring worker that forks as fast as it
// can, retires it after a short random delay, and reports whether any member
// outlived retire. Every member inherits the write end of a witness pipe, so
// the read end reaches EOF only once all of them have exited; zombies hold no
// descriptors. Children end by themselves after a few seconds, so the test
// never signals a group it may no longer own. A retire error is reported apart
// from the witness result.
func retireForkingTree() (outlived bool, retireErr, err error) {
	const script = `trap '' TERM; i=0; while [ $i -lt 200 ]; do sleep 3 & i=$((i+1)); done; wait`
	r, w, err := os.Pipe()
	if err != nil {
		return false, nil, err
	}
	defer r.Close()
	// #nosec G204 -- fixed shell and test-owned script.
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.ExtraFiles = []*os.File{w}
	tree, err := newWorkerProcessTree(cmd)
	if err == nil {
		err = tree.start()
	}
	_ = w.Close()
	if err != nil {
		return false, nil, fmt.Errorf("start worker tree: %w", err)
	}
	waitDone := make(chan struct{})
	go func() { _ = cmd.Wait(); close(waitDone) }()
	//nolint:gosec // jitter only, not security
	time.Sleep(time.Duration(rand.IntN(20000)) * time.Microsecond)
	retireErr = tree.retire(0, workerKillDeadline)
	<-waitDone
	if err := r.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return false, nil, err
	}
	_, readErr := io.Copy(io.Discard, r)
	return readErr != nil, retireErr, nil
}
