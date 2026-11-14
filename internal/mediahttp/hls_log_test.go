package mediahttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func captureDefaultLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestLogHLSServeFailureClientCancelIsDebug(t *testing.T) {
	buf := captureDefaultLogger(t)
	logHLSServeFailure(context.Background(), fmt.Errorf("segment wait: %w", context.Canceled))
	out := buf.String()
	if !strings.Contains(out, "level=DEBUG") || strings.Contains(out, "level=ERROR") {
		t.Fatalf("client cancel should log at DEBUG only, got %q", out)
	}
}

func TestLogHLSServeFailureCanceledRequestContextIsDebug(t *testing.T) {
	buf := captureDefaultLogger(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	logHLSServeFailure(ctx, errors.New("transcoder exited"))
	if out := buf.String(); strings.Contains(out, "level=ERROR") {
		t.Fatalf("failure after client disconnect should not log ERROR, got %q", out)
	}
}

func TestLogHLSServeFailureRealErrorStaysError(t *testing.T) {
	buf := captureDefaultLogger(t)
	logHLSServeFailure(context.Background(), errors.New("ffmpeg: exit status 1"))
	out := buf.String()
	if !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "hls serve failed") {
		t.Fatalf("real failure should log ERROR, got %q", out)
	}
}
