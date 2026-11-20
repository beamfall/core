//go:build unix

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/beamfall/core/internal/integrationhost"
)

const workerShimGuardianInputFD = 3

const (
	workerShimGuardianReadyFD      = 4
	workerShimGuardianReadyByte    = 0xa5
	workerShimGuardianReadyTimeout = 2 * time.Second
	workerShimGuardianKillDeadline = 2 * time.Second
)

func runWorkerShimWithGuardian(argv []string, lifetimeFD int) error {
	if err := startWorkerShimGuardian(lifetimeFD); err != nil {
		return err
	}
	return runWorkerShim(argv)
}

func startWorkerShimGuardian(lifetimeFD int) error {
	if lifetimeFD == 0 {
		return errors.New("worker-shim: guardian lifetime descriptor is required")
	}
	lifetime := os.NewFile(uintptr(lifetimeFD), "worker-supervisor-lifetime")
	if lifetime == nil {
		return errors.New("worker-shim: invalid guardian lifetime descriptor")
	}
	defer func() { _ = lifetime.Close() }()

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("worker-shim: resolve guardian executable: %w", err)
	}
	// #nosec G204 -- executable is this trusted Beamfall binary and argv is fixed.
	cmd := exec.Command(executable, "worker-shim", "--guardian")
	return startWorkerShimGuardianProcess(lifetime, cmd, workerShimGuardianReadyTimeout)
}

func startWorkerShimGuardianProcess(lifetime *os.File, cmd *exec.Cmd, timeout time.Duration) error {
	readyR, readyW, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("worker-shim: guardian readiness pipe: %w", err)
	}
	defer func() { _ = readyR.Close() }()
	defer func() { _ = readyW.Close() }()

	cmd.Env = []string{}
	cmd.ExtraFiles = []*os.File{lifetime, readyW}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("worker-shim: start guardian: %w", err)
	}
	_ = readyW.Close()
	if err := waitWorkerShimGuardianReady(readyR, timeout); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	if err := cmd.Process.Release(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("worker-shim: release guardian: %w", err)
	}
	return nil
}

func waitWorkerShimGuardianReady(ready *os.File, timeout time.Duration) error {
	if err := ready.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("worker-shim: guardian readiness deadline: %w", err)
	}
	var value [1]byte
	if _, err := io.ReadFull(ready, value[:]); err != nil {
		return fmt.Errorf("worker-shim: guardian readiness: %w", err)
	}
	if value[0] != workerShimGuardianReadyByte {
		return fmt.Errorf("worker-shim: guardian readiness byte 0x%x", value[0])
	}
	return nil
}

func runWorkerShimGuardian() error {
	lifetime := os.NewFile(workerShimGuardianInputFD, "worker-supervisor-lifetime")
	if lifetime == nil {
		return errors.New("worker-shim guardian: missing lifetime descriptor")
	}
	defer func() { _ = lifetime.Close() }()
	ready := os.NewFile(workerShimGuardianReadyFD, "worker-guardian-ready")
	if ready == nil {
		return errors.New("worker-shim guardian: missing readiness descriptor")
	}
	defer func() { _ = ready.Close() }()

	// The guardian is the process-group birth-identity pin. It survives TERM so
	// the owned PGID cannot disappear and be reused before the final KILL.
	signal.Ignore(os.Interrupt, syscall.SIGTERM)
	pgid := syscall.Getpgrp()
	// The guardian leaves the group before its final KILL (below), so it pins
	// the ID first, while it is still a member, and refuses the worker if it
	// cannot.
	pin, err := integrationhost.PinProcessGroup(pgid)
	if err != nil {
		return fmt.Errorf("worker-shim guardian: %w", err)
	}
	defer pin.Release()
	if _, err := ready.Write([]byte{workerShimGuardianReadyByte}); err != nil {
		return fmt.Errorf("worker-shim guardian: signal readiness: %w", err)
	}
	_ = ready.Close()
	if _, err := io.Copy(io.Discard, lifetime); err != nil {
		return fmt.Errorf("worker-shim guardian: lifetime read: %w", err)
	}
	// The guardian is still a member here, so kill(0) reaches the group; in a
	// bwrap sandbox the group is 1, and kill(-1) would broadcast.
	_ = syscall.Kill(0, syscall.SIGTERM)
	time.Sleep(integrationhost.WorkerTerminationGrace)
	// One group KILL can miss a member's mid-fork child on macOS (CORE-2716),
	// so the guardian leaves the group before the KILL and repeats it while
	// members remain; its pin keeps the group ID reserved meanwhile.
	if err := integrationhost.KillProcessGroup(pgid, true, workerShimGuardianKillDeadline); err != nil {
		return fmt.Errorf("worker-shim guardian: kill process group %d: %w", pgid, err)
	}
	return nil
}
