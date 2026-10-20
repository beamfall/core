//go:build windows

package main

import (
	"context"
	"errors"
	"testing"
)

func TestWindowsShimsFailClosed(t *testing.T) {
	tests := map[string]func() error{
		"ffmpeg": func() error {
			return ffmpegShim(context.Background(), []string{"--ro", `C:\media`, "--", `C:\missing\ffmpeg.exe`})
		},
		"local-inference": func() error {
			return localInferenceShim(context.Background(), []string{
				"--input-ro", `C:\media`,
				"--model-ro", `\\server\models`,
				"--work-rw", `C:\work`,
				"--", `C:\missing\infer.exe`,
			})
		},
		"worker": func() error {
			return workerShim(context.Background(), []string{"--", `C:\missing\worker.exe`})
		},
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			if err := run(); !errors.Is(err, errUnsupportedShimHardening) {
				t.Fatalf("error = %v, want %v", err, errUnsupportedShimHardening)
			}
		})
	}
}

func TestWindowsLocalInferencePaths(t *testing.T) {
	for _, path := range []string{`C:\media`, `\\server\share`} {
		_, _, _, _, err := parseLocalInferenceShimArgs([]string{
			"--input-ro", path,
			"--model-ro", `C:\models`,
			"--work-rw", `C:\work`,
			"--", `C:\bin\infer.exe`,
		})
		if err != nil {
			t.Fatalf("absolute path %q: %v", path, err)
		}
	}
	if _, _, _, _, err := parseLocalInferenceShimArgs([]string{
		"--input-ro", "relative",
		"--model-ro", `C:\models`,
		"--work-rw", `C:\work`,
		"--", `C:\bin\infer.exe`,
	}); err == nil {
		t.Fatal("accepted relative Windows path")
	}
}
