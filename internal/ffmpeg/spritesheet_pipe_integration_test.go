package ffmpeg

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Run the production pipe argv against a real HTTP source. Confinement itself
// is covered by pipe_test; this tests the filter on hosts with FFmpeg installed.
func TestPipeSpriteSheetsPadLongGOP(t *testing.T) {
	binary, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed; skipping real-binary sprite pipe test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// The production pipe argv sets the HTTP protocol's -max_redirects, which
	// older FFmpeg builds (8.0.1 on wsl-lanes) reject as an unknown option.
	// #nosec G204 -- resolved FFmpeg with fixed help arguments.
	help, err := exec.CommandContext(ctx, binary, "-hide_banner", "-h", "protocol=http").CombinedOutput()
	if err != nil || !bytes.Contains(help, []byte("max_redirects")) {
		t.Skip("ffmpeg http protocol lacks -max_redirects; skipping real-binary sprite pipe test")
	}
	path := filepath.Join(t.TempDir(), "long.mp4")
	// #nosec G204 -- resolved FFmpeg with fixed lavfi arguments and a test-owned output path.
	cmd := exec.CommandContext(ctx, binary, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=10:duration=40",
		"-c:v", "libx264", "-g", "600", "-keyint_min", "600", "-sc_threshold", "0", "-y", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthesize long GOP: %v\n%s", err, out)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- clip generated in t.TempDir above.
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "long.mp4", time.Time{}, bytes.NewReader(data))
	}))
	defer server.Close()
	args := pipeSpriteSheetArgs(server.URL, 10*time.Second, 10, 320, 1, 1, 4)
	// #nosec G204 -- resolved FFmpeg with production pipe arguments and a test-owned loopback URL.
	cmd = exec.CommandContext(ctx, binary, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("pipe sprites: %v\n%s", err, &stderr)
	}
	frames, err := SplitJPEGStream(out, 4)
	if err != nil || len(frames) != 4 {
		t.Fatalf("pipe frames = %d, %v; want 4", len(frames), err)
	}
}
