//go:build unix

package ffmpeg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// installFakeFFmpeg puts an executable "ffmpeg" shell script with body on a
// PATH of its own for one test, so DecodeVerify resolves and runs it.
func installFakeFFmpeg(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte(script), 0o700); err != nil { // #nosec G306 -- test fixture must be executable
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	t.Setenv("PATH", dir)
}

// mediaFixture returns an existing file DecodeVerify can be pointed at; the
// fake ffmpeg never reads it.
func mediaFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clip.mkv")
	if err := os.WriteFile(path, []byte("not decoded"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// TestDecodeVerifyOnlyDecoderDataExitsMarkBroken pins the CORE-0700 verdict
// split: only FFmpeg's decoder data statuses report broken, while a signal
// death, a hardening-shim refusal, or a system/resource errno is an error that
// must never flag the file.
func TestDecodeVerifyOnlyDecoderDataExitsMarkBroken(t *testing.T) {
	// feature:decode-verify
	cases := map[string]struct {
		body       string
		wantBroken bool
		wantErr    string
	}{
		"invalid data":              {body: "exit 183", wantBroken: true},
		"premature end of file":     {body: "exit 187", wantBroken: true},
		"legacy conversion failure": {body: "exit 1", wantBroken: true},
		"legacy error rate":         {body: "exit 69", wantBroken: true},
		"clean decode":              {body: "exit 0"},
		"shim refusal":              {body: "echo 'ffmpeg-shim: applying filesystem confinement: denied' >&2; exit 126", wantErr: "hardening shim failed"},
		"oom killed":                {body: "kill -KILL $$", wantErr: "signal"},
		"cpu rlimit":                {body: "kill -XCPU $$", wantErr: "signal"},
		"decoder crash":             {body: "kill -SEGV $$", wantErr: "signal"},
		"file vanished (ENOENT)":    {body: "exit 254", wantErr: "exit status 254"},
		"permission denied":         {body: "echo 'Permission denied' >&2; exit 243", wantErr: "Permission denied"},
		"out of memory (ENOMEM)":    {body: "exit 244", wantErr: "exit status 244"},
		"decoder not in this build": {body: "exit 8", wantErr: "exit status 8"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			installFakeFFmpeg(t, tc.body)
			broken, err := New().DecodeVerify(context.Background(), mediaFixture(t), 0)
			if broken != tc.wantBroken {
				t.Fatalf("broken = %v, want %v (err %v)", broken, tc.wantBroken, err)
			}
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("err = %v, want nil", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("err = %v, want one mentioning %q", err, tc.wantErr)
			}
		})
	}
}

// TestDecodeVerifyWindowTimeoutKillsTreeAndIsNotBroken proves a wedged decode
// is bounded in wall-clock time, reported as an error rather than a broken
// verdict, and leaves no descendant behind: the fake ffmpeg forks a sleeper
// into its process group and then hangs.
func TestDecodeVerifyWindowTimeoutKillsTreeAndIsNotBroken(t *testing.T) {
	// feature:decode-verify
	prev := decodeVerifyWindowTimeout
	decodeVerifyWindowTimeout = 300 * time.Millisecond
	t.Cleanup(func() { decodeVerifyWindowTimeout = prev })

	pidFile := filepath.Join(t.TempDir(), "child.pid")
	installFakeFFmpeg(t, "/bin/sleep 60 &\necho $! > '"+pidFile+"'\nwait")
	start := time.Now()
	broken, err := New().DecodeVerify(context.Background(), mediaFixture(t), 0)
	if broken {
		t.Fatal("a timed-out decode reported broken=true")
	}
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a wrapped context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("DecodeVerify took %s, want it bounded by the window timeout", elapsed)
	}
	assertProcessGone(t, pidFile)
}

// TestDecodeVerifyCancellationIsNotBroken proves a canceled sweep never turns
// into a broken verdict and that the canceled run's descendants are reaped.
func TestDecodeVerifyCancellationIsNotBroken(t *testing.T) {
	// feature:decode-verify
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	installFakeFFmpeg(t, "/bin/sleep 60 &\necho $! > '"+pidFile+"'\nwait")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(pidFile); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	broken, err := New().DecodeVerify(ctx, mediaFixture(t), 0)
	if broken {
		t.Fatal("a canceled decode reported broken=true")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	assertProcessGone(t, pidFile)
}

// assertProcessGone waits briefly for the pid recorded in pidFile to stop
// existing; init/launchd reaps the killed orphan asynchronously.
func assertProcessGone(t *testing.T, pidFile string) {
	t.Helper()
	data, err := os.ReadFile(pidFile) // #nosec G304 -- test-owned temp file
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		t.Fatalf("child pid %q: %v", data, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("descendant %d survived the decode-verify teardown", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
