//go:build windows

package integrationhost

import (
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestNewWorkerProcessTreeConfiguresKillOnClose(t *testing.T) {
	const job = windows.Handle(41)
	var configured bool
	closed := 0
	ops := successfulWindowsJobOps(job)
	ops.configureJobObject = func(
		gotJob windows.Handle,
		info *windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION,
	) error {
		if gotJob != job {
			t.Fatalf("configured job = %d, want %d", gotJob, job)
		}
		if info.BasicLimitInformation.LimitFlags != windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE {
			t.Fatalf("limit flags = %#x, want kill-on-close", info.BasicLimitInformation.LimitFlags)
		}
		configured = true
		return nil
	}
	ops.closeHandle = func(got windows.Handle) error {
		if got != job {
			t.Fatalf("closed handle = %d, want %d", got, job)
		}
		closed++
		return nil
	}

	cmd := exec.Command("worker")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	tree, err := newWorkerProcessTreeWithOps(cmd, ops)
	if err != nil {
		t.Fatalf("new worker process tree: %v", err)
	}
	if !configured {
		t.Fatal("job object was not configured")
	}
	wantFlags := uint32(windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_SUSPENDED)
	if cmd.SysProcAttr.CreationFlags != wantFlags {
		t.Fatalf("creation flags = %#x, want %#x", cmd.SysProcAttr.CreationFlags, wantFlags)
	}
	if tree.job != job {
		t.Fatalf("owned job = %d, want %d", tree.job, job)
	}

	tree.close()
	tree.close()
	if closed != 1 {
		t.Fatalf("close calls = %d, want 1", closed)
	}
	if tree.job != 0 {
		t.Fatalf("owned job after close = %d, want 0", tree.job)
	}
}

func TestNewWorkerProcessTreeReportsJobErrors(t *testing.T) {
	createErr := errors.New("create job")
	ops := successfulWindowsJobOps(41)
	ops.createJobObject = func() (windows.Handle, error) {
		return 0, createErr
	}
	tree, err := newWorkerProcessTreeWithOps(exec.Command("worker"), ops)
	if !errors.Is(err, createErr) || tree != nil {
		t.Fatalf("create result = (%v, %v), want nil tree and %v", tree, err, createErr)
	}

	configureErr := errors.New("configure job")
	closed := 0
	ops = successfulWindowsJobOps(42)
	ops.configureJobObject = func(
		windows.Handle,
		*windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION,
	) error {
		return configureErr
	}
	ops.closeHandle = func(handle windows.Handle) error {
		if handle != 42 {
			t.Fatalf("closed handle = %d, want 42", handle)
		}
		closed++
		return nil
	}
	tree, err = newWorkerProcessTreeWithOps(exec.Command("worker"), ops)
	if !errors.Is(err, configureErr) || tree != nil {
		t.Fatalf("configure result = (%v, %v), want nil tree and %v", tree, err, configureErr)
	}
	if closed != 1 {
		t.Fatalf("close calls after configuration failure = %d, want 1", closed)
	}
}

func TestNewWorkerProcessTreeInitializesProcessAttributes(t *testing.T) {
	cmd := exec.Command("worker")
	tree, err := newWorkerProcessTreeWithOps(cmd, successfulWindowsJobOps(43))
	if err != nil {
		t.Fatalf("new worker process tree: %v", err)
	}
	if cmd.SysProcAttr == nil {
		t.Fatal("process attributes were not initialized")
	}
	if cmd.SysProcAttr.CreationFlags != windows.CREATE_SUSPENDED {
		t.Fatalf("creation flags = %#x, want suspended", cmd.SysProcAttr.CreationFlags)
	}
	tree.close()
}

func TestWorkerProcessTreeRetireStateMachine(t *testing.T) {
	const job = windows.Handle(44)
	active := []uint32{2, 1, 0, 0}
	queries := 0
	terminations := 0
	ops := successfulWindowsJobOps(job)
	ops.terminateJobObject = func(got windows.Handle, exitCode uint32) error {
		if got != job || exitCode != 1 {
			t.Fatalf("terminate arguments = (%d, %d), want (%d, 1)", got, exitCode, job)
		}
		terminations++
		return nil
	}
	ops.activeJobProcesses = func(gotJob windows.Handle) (uint32, error) {
		if gotJob != job {
			t.Fatalf("queried job = %d, want %d", gotJob, job)
		}
		result := active[queries]
		queries++
		return result, nil
	}
	tree := &workerProcessTree{job: job, jobOps: ops}

	if err := tree.retire(0, time.Second); err != nil {
		t.Fatalf("first retire: %v", err)
	}
	if err := tree.retire(0, time.Second); err != nil {
		t.Fatalf("idempotent retire: %v", err)
	}
	if terminations != 2 || queries != 4 {
		t.Fatalf("retire calls = terminate %d, query %d; want terminate 2, query 4", terminations, queries)
	}
}

func TestWorkerProcessTreeRetireReportsErrors(t *testing.T) {
	terminateErr := errors.New("terminate job")
	ops := successfulWindowsJobOps(45)
	ops.terminateJobObject = func(windows.Handle, uint32) error { return terminateErr }
	tree := &workerProcessTree{job: 45, jobOps: ops}
	if err := tree.retire(0, time.Second); !errors.Is(err, terminateErr) {
		t.Fatalf("terminate error = %v, want %v", err, terminateErr)
	}

	queryErr := errors.New("query job")
	ops = successfulWindowsJobOps(46)
	ops.activeJobProcesses = func(windows.Handle) (uint32, error) {
		return 0, queryErr
	}
	tree = &workerProcessTree{job: 46, jobOps: ops}
	if err := tree.retire(0, time.Second); !errors.Is(err, queryErr) {
		t.Fatalf("query error = %v, want %v", err, queryErr)
	}

	ops = successfulWindowsJobOps(47)
	ops.activeJobProcesses = func(windows.Handle) (uint32, error) {
		return 3, nil
	}
	tree = &workerProcessTree{job: 47, jobOps: ops}
	err := tree.retire(0, -time.Nanosecond)
	if err == nil || !strings.Contains(err.Error(), "job retained 3 processes") {
		t.Fatalf("retire timeout error = %v, want retained-process count", err)
	}
}

func TestWorkerProcessTreeClosedStateIsIdempotent(t *testing.T) {
	calls := 0
	ops := successfulWindowsJobOps(48)
	ops.closeHandle = func(windows.Handle) error {
		calls++
		return nil
	}
	ops.terminateJobObject = func(windows.Handle, uint32) error {
		t.Fatal("closed tree attempted termination")
		return nil
	}
	tree := &workerProcessTree{job: 48, jobOps: ops}

	tree.close()
	tree.close()
	if err := tree.retire(0, time.Second); err != nil {
		t.Fatalf("retire closed tree: %v", err)
	}
	if calls != 1 {
		t.Fatalf("close calls = %d, want 1", calls)
	}
}

func successfulWindowsJobOps(job windows.Handle) windowsJobOps {
	return windowsJobOps{
		createJobObject: func() (windows.Handle, error) {
			return job, nil
		},
		configureJobObject: func(
			windows.Handle,
			*windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION,
		) error {
			return nil
		},
		closeHandle:        func(windows.Handle) error { return nil },
		terminateJobObject: func(windows.Handle, uint32) error { return nil },
		activeJobProcesses: func(windows.Handle) (uint32, error) { return 0, nil },
	}
}
