//go:build linux || darwin

package integrationhost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
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
			worker := fixtureWorkerPath
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
			pids := waitFixturePIDs(t, pidFile)
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
	req.Binary = fixtureWorkerPath
	req.Args = []string{
		"--tree-mode", "hang", "--pid-file", pidFile,
		"--hold-file", pidFile + ".release", "--term-resistant=true",
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan error, 1)
	if runtime.GOOS == "darwin" {
		// sandbox-exec cannot read the separately built fixture from its private
		// test directory; exercise the identical tree supervisor directly.
		// #nosec G204 -- fixtureWorkerPath and all arguments are test-owned.
		cmd := exec.CommandContext(ctx, fixtureWorkerPath, req.Args...)
		go func() { done <- superviseWorker(ctx, cmd, nil) }()
	} else {
		go func() {
			_, err := (LocalInferenceRuntime{ShimPath: beamfallShimPath}).Run(ctx, req)
			done <- err
		}()
	}
	pids := waitFixturePIDs(t, pidFile)
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
			worker := fixtureWorkerPath
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
			pids := waitFixturePIDs(t, pidFile)
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
	// #nosec G204 -- fixtureWorkerPath is built by TestMain and receives only fixed test arguments.
	cmd := exec.Command(
		fixtureWorkerPath,
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
	return hostFixturePIDs(t, pids, path)
}

// hostFixturePIDs maps the pids the fixture reported from its own PID
// namespace into this test's. On Linux the supervisor launches workers through
// bwrap --unshare-all, so os.Getpid inside the sandbox is namespace-local (the
// worker is pid 2) and names an unrelated host process. Each fixture process is
// found by its innermost NSpid plus the test-unique pid-file path on its command
// line; without a PID namespace NSpid is the host pid and the mapping is the
// identity.
func hostFixturePIDs(t *testing.T, reported []int, pidFile string) []int {
	t.Helper()
	if runtime.GOOS != "linux" {
		return reported
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatalf("list /proc: %v", err)
	}
	host := make([]int, len(reported))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		// #nosec G304 -- a /proc entry named by a numeric pid.
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil || !strings.Contains(string(cmdline), pidFile) {
			continue
		}
		nsPIDs := procStatusInts(pid, "NSpid")
		if len(nsPIDs) == 0 {
			continue
		}
		for i, want := range reported {
			if nsPIDs[len(nsPIDs)-1] != want {
				continue
			}
			if host[i] != 0 {
				t.Fatalf("fixture pid %d matches host pids %d and %d", want, host[i], pid)
			}
			host[i] = pid
		}
	}
	for i, pid := range host {
		if pid == 0 {
			t.Fatalf("no host process for fixture pid %d (reported %v)", reported[i], reported)
		}
	}
	return host
}

// procStatusInts returns the integer fields of one /proc/<pid>/status line,
// or nil when the process or the line is absent.
func procStatusInts(pid int, key string) []int {
	// #nosec G304 -- a /proc path built from an integer pid.
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(raw), "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok || name != key {
			continue
		}
		var values []int
		for _, field := range strings.Fields(value) {
			n, err := strconv.Atoi(field)
			if err != nil {
				return nil
			}
			values = append(values, n)
		}
		return values
	}
	return nil
}

// ownedGroupLeader returns the process that must lead the worker tree's
// process group. Unsandboxed, the supervisor's Setpgid makes the worker its own
// birth-identity leader. Under bwrap --new-session the setsid runs in the
// sandbox's PID-namespace init (bwrap's pid 1), so that init leads the group:
// it must be the worker's direct parent, at the worker's namespace depth, with
// namespace pid 1.
func ownedGroupLeader(t *testing.T, worker int) int {
	t.Helper()
	if runtime.GOOS != "linux" {
		return worker
	}
	pgid, err := syscall.Getpgid(worker)
	if err != nil {
		t.Fatalf("getpgid(%d): %v", worker, err)
	}
	if pgid == worker {
		return worker
	}
	parent := procStatusInts(worker, "PPid")
	workerNS := procStatusInts(worker, "NSpid")
	leaderNS := procStatusInts(pgid, "NSpid")
	if len(parent) == 1 && parent[0] == pgid && len(workerNS) > 1 &&
		len(leaderNS) == len(workerNS) && leaderNS[len(leaderNS)-1] == 1 {
		return pgid
	}
	return worker
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

func assertOwnedProcessGroup(t *testing.T, pids []int, sentinelPID int) {
	t.Helper()
	want := ownedGroupLeader(t, pids[0])
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
