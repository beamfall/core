//go:build windows

package integrationhost

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type workerProcessTree struct {
	cmd    *exec.Cmd
	job    windows.Handle
	jobOps windowsJobOps
}

func newWorkerProcessTree(cmd *exec.Cmd) (*workerProcessTree, error) {
	return newWorkerProcessTreeWithOps(cmd, systemWindowsJobOps)
}

type windowsJobOps struct {
	createJobObject    func() (windows.Handle, error)
	configureJobObject func(windows.Handle, *windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION) error
	closeHandle        func(windows.Handle) error
	terminateJobObject func(windows.Handle, uint32) error
	activeJobProcesses func(windows.Handle) (uint32, error)
}

var systemWindowsJobOps = windowsJobOps{
	createJobObject: func() (windows.Handle, error) {
		return windows.CreateJobObject(nil, nil)
	},
	configureJobObject: func(job windows.Handle, info *windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION) error {
		_, err := windows.SetInformationJobObject(
			job,
			windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(info)),
			uint32(unsafe.Sizeof(*info)),
		)
		return err
	},
	closeHandle:        windows.CloseHandle,
	terminateJobObject: windows.TerminateJobObject,
	activeJobProcesses: activeJobProcesses,
}

func newWorkerProcessTreeWithOps(cmd *exec.Cmd, jobOps windowsJobOps) (*workerProcessTree, error) {
	job, err := jobOps.createJobObject()
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if err := jobOps.configureJobObject(job, &info); err != nil {
		_ = jobOps.closeHandle(job)
		return nil, err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	return &workerProcessTree{cmd: cmd, job: job, jobOps: jobOps}, nil
}

func (t *workerProcessTree) start() error {
	if err := t.cmd.Start(); err != nil {
		return err
	}
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(t.cmd.Process.Pid),
	)
	if err != nil {
		_ = t.cmd.Process.Kill()
		return err
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(t.job, process); err != nil {
		_ = t.cmd.Process.Kill()
		return err
	}
	if err := resumeProcess(uint32(t.cmd.Process.Pid)); err != nil {
		_ = windows.TerminateJobObject(t.job, 1)
		return err
	}
	return nil
}

func (t *workerProcessTree) retire(_ time.Duration, killDeadline time.Duration) error {
	if t.job == 0 {
		return nil
	}
	if err := t.jobOps.terminateJobObject(t.job, 1); err != nil {
		return err
	}
	deadline := time.Now().Add(killDeadline)
	for {
		active, err := t.jobOps.activeJobProcesses(t.job)
		if err != nil {
			return err
		}
		if active == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("job retained %d processes after termination", active)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (t *workerProcessTree) close() {
	if t.job != 0 {
		_ = t.jobOps.closeHandle(t.job)
		t.job = 0
	}
}

func resumeProcess(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	err = windows.Thread32First(snapshot, &entry)
	for err == nil {
		if entry.OwnerProcessID == pid {
			thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if openErr != nil {
				return openErr
			}
			_, resumeErr := windows.ResumeThread(thread)
			windows.CloseHandle(thread)
			return resumeErr
		}
		err = windows.Thread32Next(snapshot, &entry)
	}
	if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return errors.New("worker primary thread not found")
	}
	return err
}

type jobBasicAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

func activeJobProcesses(job windows.Handle) (uint32, error) {
	var info jobBasicAccounting
	err := windows.QueryInformationJobObject(
		job,
		windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
		nil,
	)
	return info.ActiveProcesses, err
}
