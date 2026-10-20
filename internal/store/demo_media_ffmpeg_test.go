//go:build ffmpeg

package store_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

func TestDemoSeedGeneratedMediaDrivesPlaybackInfo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not available")
	}

	dataDir := t.TempDir()
	root := repoRoot(t)
	cmd := exec.Command(filepath.Join(root, "script", "gen_demo_media.sh"), "--data-dir", dataDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gen_demo_media.sh failed: %v\n%s", err, output)
	}

	st, err := store.Open(context.Background(), dataDir, store.Options{SeedProfile: store.SeedProfileDemo})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer st.Close()

	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-demo-open", "library-demo-archive"}}
	file, err := st.PrimaryMediaFile(context.Background(), "media-demo-sintel", filter)
	if err != nil {
		t.Fatalf("PrimaryMediaFile returned error: %v", err)
	}
	if filepath.Ext(file.Path) != ".mp4" {
		t.Fatalf("seed file path = %q, want generated mp4", file.Path)
	}
	if !strings.HasPrefix(file.ContentFingerprint, "sha256:") {
		t.Fatalf("ContentFingerprint = %q, want real sha256", file.ContentFingerprint)
	}

	scope, err := auth.NewServiceWithClock(st, func() time.Time { return time.Unix(100, 0).UTC() }).Scope(context.Background(), "session-demo-viewer")
	if err != nil {
		t.Fatalf("Scope returned error: %v", err)
	}
	tokens := streamtoken.NewService([]byte("12345678901234567890123456789012"), func() time.Time { return time.Unix(100, 0).UTC() })
	service := playback.NewService(st, tokens, func() time.Time { return time.Unix(100, 0).UTC() })
	resp, err := service.Info(context.Background(), scope, playback.InfoRequest{
		MediaID:           "media-demo-sintel",
		CapabilityProfile: playback.CapabilityProfile{Client: "ffmpeg-test", Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play", resp.Decision)
	}
	if resp.Timeline.DurationSeconds <= 0 || resp.Timeline.DurationSeconds == 888 {
		t.Fatalf("DurationSeconds = %d, want generated probe duration", resp.Timeline.DurationSeconds)
	}

	resp, err = service.Info(context.Background(), scope, playback.InfoRequest{
		MediaID:           "media-demo-orbit",
		CapabilityProfile: playback.CapabilityProfile{Client: "ffmpeg-test", Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info orbit returned error: %v", err)
	}
	if resp.Decision != "transcode" {
		t.Fatalf("Orbit Decision = %q, want transcode", resp.Decision)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := exec.LookPath("git"); err == nil {
			cmd := exec.Command("git", "rev-parse", "--show-toplevel")
			cmd.Dir = dir
			if output, err := cmd.Output(); err == nil {
				return string(output[:len(output)-1])
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root")
		}
		dir = parent
	}
}
