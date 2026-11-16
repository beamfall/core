package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestReloader(t *testing.T, base slog.Level) (*logLevelReloader, *slog.LevelVar, *bytes.Buffer, string) {
	t.Helper()
	dir := t.TempDir()
	level := new(slog.LevelVar)
	level.Set(base)
	var buf bytes.Buffer
	return newLogLevelReloader(level, dir, newOperatorLogger(&buf, level)), level, &buf, dir
}

func TestLogLevelReloadAppliesFileAndRestoresOnRemoval(t *testing.T) {
	r, level, buf, dir := newTestReloader(t, slog.LevelInfo)
	path := filepath.Join(dir, logLevelFileName)
	if err := os.WriteFile(path, []byte(" Debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.reload()
	if got := level.Level(); got != slog.LevelDebug {
		t.Fatalf("level = %v, want DEBUG", got)
	}
	if !strings.Contains(buf.String(), "log level changed") || !strings.Contains(buf.String(), "to=DEBUG") {
		t.Fatalf("change not logged: %q", buf.String())
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	r.reload()
	if got := level.Level(); got != slog.LevelInfo {
		t.Fatalf("level after removal = %v, want INFO", got)
	}
}

func TestLogLevelReloadLogsChangeToError(t *testing.T) {
	r, level, buf, dir := newTestReloader(t, slog.LevelInfo)
	if err := os.WriteFile(filepath.Join(dir, logLevelFileName), []byte("error"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.reload()
	if got := level.Level(); got != slog.LevelError {
		t.Fatalf("level = %v, want ERROR", got)
	}
	if !strings.Contains(buf.String(), "to=ERROR") {
		t.Fatalf("change to ERROR not logged: %q", buf.String())
	}
}

func TestLogLevelReloadKeepsLevelOnInvalidValue(t *testing.T) {
	r, level, buf, dir := newTestReloader(t, slog.LevelWarn)
	if err := os.WriteFile(filepath.Join(dir, logLevelFileName), []byte("loud"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.reload()
	if got := level.Level(); got != slog.LevelWarn {
		t.Fatalf("level = %v, want WARN", got)
	}
	if !strings.Contains(buf.String(), "log level file invalid") {
		t.Fatalf("invalid value not logged: %q", buf.String())
	}
}

func TestLogLevelReloadKeepsLevelOnUnreadableFile(t *testing.T) {
	r, level, buf, dir := newTestReloader(t, slog.LevelInfo)
	// A directory in place of the file cannot be read.
	if err := os.Mkdir(filepath.Join(dir, logLevelFileName), 0o700); err != nil {
		t.Fatal(err)
	}
	r.reload()
	if got := level.Level(); got != slog.LevelInfo {
		t.Fatalf("level = %v, want INFO", got)
	}
	if !strings.Contains(buf.String(), "log level file unreadable") {
		t.Fatalf("unreadable file not logged: %q", buf.String())
	}
}
