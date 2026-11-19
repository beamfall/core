package ffmpeg

import (
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// Only the out-of-group supervisor adopts descendants. Its socket also
	// ties the encoder lifetime to the server, including abrupt server exit.
	hlsSupervisorArgv0 = "beamfall-hls-supervisor"
	hlsStopBound       = time.Second
	hlsStartupBound    = 5 * time.Second
	hlsPauseCommand    = 'P'
	hlsResumeCommand   = 'R'
)

// SupportsHLSPause reports whether the HLS process group can be paused safely.
const SupportsHLSPause = true

type hlsReaper struct {
	control *os.File
	done    chan struct{}
	pid     int
}

type hlsLaunch struct {
	Path       string
	Args       []string
	ExtraFiles []bool
}

type hlsLaunchResult struct {
	PID   int
	Error string
	Errno syscall.Errno
}

func (p *HLSProcess) start() error {
	cmd := p.cmd
	if cmd.Err != nil {
		return cmd.Err
	}
	if cmd.Path == "" {
		return errors.New("exec: no command")
	}
	argv := cmd.Args
	if len(argv) == 0 {
		argv = []string{cmd.Path}
	}
	req := hlsLaunch{Path: cmd.Path, Args: argv, ExtraFiles: make([]bool, len(cmd.ExtraFiles))}
	for i, file := range cmd.ExtraFiles {
		req.ExtraFiles[i] = file != nil
	}
	controlFDs, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	control := os.NewFile(uintptr(controlFDs[0]), "hls-control")
	childControl := os.NewFile(uintptr(controlFDs[1]), "hls-supervisor-control")

	p.reaper = hlsReaper{control: control, done: make(chan struct{})}
	path, args, files := cmd.Path, cmd.Args, cmd.ExtraFiles
	cmd.Path = "/proc/self/exe"
	cmd.Args = []string{hlsSupervisorArgv0, strconv.Itoa(3 + len(files))}
	cmd.ExtraFiles = append(append([]*os.File(nil), files...), childControl)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// exec's context watchdog must never kill the process still responsible
	// for reaping. Its Cancel closes the control socket and requests SIGKILL.
	cmd.WaitDelay = 0
	err = cmd.Start()
	_ = childControl.Close()
	cmd.Path, cmd.Args, cmd.ExtraFiles = path, args, files
	if err != nil {
		_ = control.Close()
		return err
	}

	p.reaper.pid, err = launchHLSEncoder(control, req)
	if err != nil {
		p.killTree()
	}
	go func() {
		_ = p.Wait()
		close(p.reaper.done)
	}()
	return err
}

func launchHLSEncoder(control *os.File, req hlsLaunch) (int, error) {
	if err := control.SetDeadline(time.Now().Add(hlsStartupBound)); err != nil {
		return 0, err
	}
	// Gob preserves byte strings such as non-UTF-8 Unix paths and arguments.
	if err := gob.NewEncoder(control).Encode(req); err != nil {
		return 0, fmt.Errorf("sending HLS launch request: %w", err)
	}
	var result hlsLaunchResult
	if err := gob.NewDecoder(control).Decode(&result); err != nil {
		return 0, fmt.Errorf("reading HLS launch result: %w", err)
	}
	if result.Errno != 0 {
		return 0, &os.PathError{Op: "fork/exec", Path: req.Path, Err: result.Errno}
	}
	if result.Error != "" {
		return 0, errors.New(result.Error)
	}
	if result.PID <= 0 {
		return 0, errors.New("supervisor did not report an encoder PID")
	}
	return result.PID, nil
}

func (p *HLSProcess) pauseGroup() bool  { return p.controlGroup(hlsPauseCommand) }
func (p *HLSProcess) resumeGroup() bool { return p.controlGroup(hlsResumeCommand) }

// controlGroup is called with signalMu held. If the result is unknown, close
// the socket to stop the encoder so it cannot be left paused indefinitely.
func (p *HLSProcess) controlGroup(command byte) bool {
	control := p.reaper.control
	if control.SetDeadline(time.Now().Add(hlsStopBound)) == nil {
		if _, err := control.Write([]byte{command}); err == nil {
			var reply [1]byte
			if _, err := io.ReadFull(control, reply[:]); err == nil {
				return reply[0] == command
			}
		}
	}
	p.stopping = true
	p.terminateGroup()
	return false
}

func (p *HLSProcess) terminateGroup() {
	if p.reaper.control != nil {
		_ = p.reaper.control.Close()
	}
}

func (p *HLSProcess) waitForExit() error {
	err := p.cmd.Wait()
	p.killTree()
	return err
}

func (p *HLSProcess) waitAfterStop() {
	timer := time.NewTimer(hlsStopBound)
	defer timer.Stop()
	select {
	case <-p.reaper.done:
	case <-timer.C:
		// The supervisor still owns the group. Wait remains a full join.
	}
}

// A private argv[0] dispatch also works when an embedded caller or test starts
// HLS without configuring Beamfall's optional FFmpeg hardening shim.
func init() {
	if os.Args[0] != hlsSupervisorArgv0 || len(os.Args) != 2 {
		return
	}
	fd, err := strconv.Atoi(os.Args[1])
	if err != nil || fd < 3 {
		os.Exit(1)
	}
	unix.CloseOnExec(fd)
	control := os.NewFile(uintptr(fd), "hls-supervisor-control")
	// Linux Pdeathsig belongs to the thread that spawns the encoder.
	runtime.LockOSThread()
	status, err := superviseHLSGroup(control, fd)
	if err != nil {
		os.Exit(1)
	}
	if status.Signaled() {
		sig := status.Signal()
		if sig == unix.SIGKILL {
			_ = unix.Kill(unix.Getpid(), sig)
		}
		// Preserve a failing status without replaying a potentially
		// core-dumping signal in the supervisor.
		os.Exit(128 + int(sig))
	}
	os.Exit(status.ExitStatus())
}

func superviseHLSGroup(control *os.File, controlFD int) (syscall.WaitStatus, error) {
	defer control.Close()
	var req hlsLaunch
	if err := gob.NewDecoder(control).Decode(&req); err != nil {
		return 0, err
	}
	encoder, err := launchHLSChild(req, controlFD)
	reply := gob.NewEncoder(control)
	if err != nil {
		result := hlsLaunchResult{Error: err.Error()}
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			_ = errors.As(pathErr.Err, &result.Errno)
		}
		_ = reply.Encode(result)
		return 0, err
	}

	var groupMu sync.Mutex
	stopping := false
	stop := func() {
		groupMu.Lock()
		defer groupMu.Unlock()
		if !stopping {
			stopping = true
			_ = unix.Kill(-encoder.Pid, unix.SIGKILL)
			_ = encoder.Kill()
		}
	}

	if err := reply.Encode(hlsLaunchResult{PID: encoder.Pid}); err != nil {
		stop()
	} else {
		go func() {
			defer stop()
			var command [1]byte
			for {
				if _, err := io.ReadFull(control, command[:]); err != nil {
					return
				}
				var sig syscall.Signal
				switch command[0] {
				case hlsPauseCommand:
					sig = unix.SIGSTOP
				case hlsResumeCommand:
					sig = unix.SIGCONT
				default:
					return
				}
				var result [1]byte
				groupMu.Lock()
				if !stopping && unix.Kill(-encoder.Pid, sig) == nil {
					result = command
				}
				groupMu.Unlock()
				if _, err := control.Write(result[:]); err != nil {
					return
				}
			}
		}()
	}
	// Leave the leader waitable until every group signal is disarmed. Its
	// reserved PID prevents a signal from reaching a recycled group.
	exitErr := waitForProcessExit(encoder.Pid)
	stop()
	_ = control.Close()
	state, waitErr := encoder.Wait()
	reapHLSChildren()
	if err := errors.Join(exitErr, waitErr); err != nil {
		return 0, err
	}
	return state.Sys().(syscall.WaitStatus), nil
}

func launchHLSChild(req hlsLaunch, controlFD int) (*os.Process, error) {
	if controlFD != 3+len(req.ExtraFiles) || len(req.Args) == 0 {
		return nil, errors.New("invalid HLS launch request")
	}
	files := []*os.File{os.Stdin, os.Stdout, os.Stderr}
	defer func() {
		for _, file := range files[3:] {
			if file != nil {
				_ = file.Close()
			}
		}
	}()
	for i, present := range req.ExtraFiles {
		var file *os.File
		if present {
			file = os.NewFile(uintptr(3+i), "hls-extra")
		}
		files = append(files, file)
	}
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		return nil, fmt.Errorf("enable HLS descendant reaping: %w", err)
	}
	// #nosec G204 -- the server passes the already-built HLS command on a private socket.
	return os.StartProcess(req.Path, req.Args, &os.ProcAttr{
		Env:   os.Environ(),
		Files: files,
		Sys:   &syscall.SysProcAttr{Setpgid: true, Pdeathsig: unix.SIGKILL},
	})
}

// This process has exactly one owned tree. The kernel reparents orphans here
// before their parent becomes waitable. ECHILD therefore proves there are no
// descendants still arriving or exiting, including a child forked at SIGKILL.
func reapHLSChildren() {
	for {
		_, err := unix.Wait4(-1, nil, 0, nil)
		if err != nil && err != unix.EINTR {
			return
		}
	}
}
