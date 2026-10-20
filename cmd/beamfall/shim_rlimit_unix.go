//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package main

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/integrationhost"
)

// applyRlimits lowers this process's resource limits to the ffmpeg ceilings
// before exec, so the exec'd ffmpeg/ffprobe inherits them. Each
// limit sets both soft and hard to the ceiling, so the child cannot raise its
// own soft limit back up.
func applyRlimits(lim ffmpeg.Rlimits) error {
	if err := capRlimit(unix.RLIMIT_CPU, lim.CPUSeconds); err != nil {
		return fmt.Errorf("RLIMIT_CPU: %w", err)
	}
	if err := capRlimit(unix.RLIMIT_FSIZE, lim.FSizeBytes); err != nil {
		return fmt.Errorf("RLIMIT_FSIZE: %w", err)
	}
	if err := capRlimit(unix.RLIMIT_NOFILE, lim.NoFile); err != nil {
		return fmt.Errorf("RLIMIT_NOFILE: %w", err)
	}
	if err := capRlimit(unix.RLIMIT_AS, lim.ASBytes); err != nil && !errors.Is(err, unix.EINVAL) {
		return fmt.Errorf("RLIMIT_AS: %w", err)
	}
	return nil
}

func applyWorkerRlimits(lim integrationhost.WorkerRlimits) error {
	if err := capRlimit(unix.RLIMIT_CPU, lim.CPUSeconds); err != nil {
		return fmt.Errorf("worker-shim: RLIMIT_CPU: %w", err)
	}
	if err := capRlimit(unix.RLIMIT_NOFILE, lim.NoFile); err != nil {
		return fmt.Errorf("worker-shim: RLIMIT_NOFILE: %w", err)
	}
	if err := capRlimit(unix.RLIMIT_AS, lim.ASBytes); err != nil && !errors.Is(err, unix.EINVAL) {
		return fmt.Errorf("worker-shim: RLIMIT_AS: %w", err)
	}
	return nil
}

// capRlimit lowers a single rlimit's soft and hard ceiling to want, leaving it
// untouched when the inherited hard limit is already at or below want.
func capRlimit(resource int, want uint64) error {
	var cur unix.Rlimit
	if err := unix.Getrlimit(resource, &cur); err != nil {
		return err
	}
	if cur.Max != unix.RLIM_INFINITY && cur.Max <= want {
		return nil
	}
	next := unix.Rlimit{Cur: want, Max: want}
	return unix.Setrlimit(resource, &next)
}
