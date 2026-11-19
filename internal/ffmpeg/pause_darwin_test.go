package ffmpeg

import "golang.org/x/sys/unix"

func hlsEncoderPID(p *HLSProcess) int { return p.cmd.Process.Pid }

func hlsProcessStopped(pid int) (bool, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return false, err
	}
	const stopped = 4 // SSTOP in <sys/proc.h>
	return info.Proc.P_stat == stopped, nil
}
