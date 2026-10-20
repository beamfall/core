//go:build unix

package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func runProcessTree(mode, pidFile, holdFile string, termResistant bool) {
	readyFile := pidFile + ".child-ready"
	child := exec.Command(os.Args[0],
		"--descendant",
		"--term-resistant="+strconv.FormatBool(termResistant),
		"--ready-file="+readyFile,
	)
	if err := child.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "start descendant: %v\n", err)
		os.Exit(10)
	}
	if err := waitForFile(readyFile, 2*time.Second); err != nil {
		_ = child.Process.Kill()
		fmt.Fprintln(os.Stderr, err)
		os.Exit(11)
	}
	pids := fmt.Sprintf("%d\n%d\n", os.Getpid(), child.Process.Pid)
	pidFileTemp := pidFile + ".tmp"
	if err := os.WriteFile(pidFileTemp, []byte(pids), 0o600); err != nil {
		_ = child.Process.Kill()
		fmt.Fprintf(os.Stderr, "write pid file: %v\n", err)
		os.Exit(12)
	}
	if err := os.Rename(pidFileTemp, pidFile); err != nil {
		_ = child.Process.Kill()
		fmt.Fprintf(os.Stderr, "publish pid file: %v\n", err)
		os.Exit(12)
	}
	if holdFile != "" {
		if err := waitForFile(holdFile, 5*time.Second); err != nil {
			_ = child.Process.Kill()
			fmt.Fprintln(os.Stderr, err)
			os.Exit(16)
		}
	}

	switch mode {
	case "success":
		return
	case "failure":
		os.Exit(7)
	case "hang":
		waitForTermination(false)
	default:
		fmt.Fprintf(os.Stderr, "unknown tree mode %q\n", mode)
		os.Exit(13)
	}
}

func runDescendant(termResistant bool, readyFile string) {
	if termResistant {
		signal.Ignore(syscall.SIGTERM, os.Interrupt)
	}
	if err := os.WriteFile(readyFile, []byte("ready\n"), 0o600); err != nil {
		os.Exit(14)
	}
	waitForTermination(termResistant)
}

func waitForTermination(termResistant bool) {
	if termResistant {
		for {
			time.Sleep(time.Hour)
		}
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	<-signals
}

func waitForFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("descendant did not become ready")
}
