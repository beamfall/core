//go:build unix

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestLogLevelWatchReloadsOnSIGHUP(t *testing.T) {
	r, level, _, dir := newTestReloader(t, slog.LevelInfo)
	if err := os.WriteFile(filepath.Join(dir, logLevelFileName), []byte("debug"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.watch(ctx)
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for level.Level() != slog.LevelDebug {
		if time.Now().After(deadline) {
			t.Fatalf("level = %v after SIGHUP, want DEBUG", level.Level())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
