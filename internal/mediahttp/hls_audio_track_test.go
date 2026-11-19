package mediahttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/playback/sessiontest"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// TestHLSSelectedAudioTrackReachesFFmpegStreamMap drives a segment request
// through the production byte route and the real FFmpeg runner (CORE-1842):
// the audio track playbackInfo signed into the grant must become the encoder's
// -map, and a grant without one must not map that track.
func TestHLSSelectedAudioTrackReachesFFmpegStreamMap(t *testing.T) {
	// feature:hls-transcode
	if runtime.GOOS == "windows" {
		t.Skip("the fake ffmpeg is a POSIX shell script")
	}
	binDir := t.TempDir()
	// The fake encoder records its argv, then writes the init and first media
	// segment into the directory of its final (playlist) argument.
	script := `#!/bin/sh
printf '%s\n' "$@" > "$BEAMFALL_TEST_FFMPEG_ARGS"
last=
for arg in "$@"; do last=$arg; done
dir=$(dirname "$last")
printf init > "$dir/init.mp4"
printf m4s > "$dir/seg00000.m4s"
`
	ffmpegPath := filepath.Join(binDir, "ffmpeg")
	if err := os.WriteFile(ffmpegPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	// #nosec G302 -- this is a test-owned executable fixture in a private temp directory.
	if err := os.Chmod(ffmpegPath, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+"/usr/bin:/bin")

	for _, tc := range []struct {
		name       string
		grantIndex int
		wantMap    bool
	}{
		{name: "selected non-default track", grantIndex: 3, wantMap: true},
		{name: "no selection", grantIndex: 0, wantMap: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argsPath := filepath.Join(t.TempDir(), "args")
			t.Setenv("BEAMFALL_TEST_FFMPEG_ARGS", argsPath)
			st := storetest.Open(t)
			src := filepath.Join(t.TempDir(), "movie.bin")
			if err := os.WriteFile(src, []byte("source"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := st.SetPrimaryMediaFile(context.Background(), "media-alpha", src); err != nil {
				t.Fatal(err)
			}
			authService := auth.NewService(st)
			scope, err := authService.Scope(context.Background(), "session-default")
			if err != nil {
				t.Fatal(err)
			}
			now := time.Unix(100, 0).UTC()
			tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
			token, err := tokens.Issue(context.Background(), streamtoken.Grant{
				Target:           streamtoken.TargetMediaHLS,
				MediaID:          "media-alpha",
				DeviceID:         scope.DeviceID,
				SessionID:        scope.SessionID,
				ProfileID:        scope.ProfileID,
				AudioStreamIndex: tc.grantIndex,
				ExpiresAt:        now.Add(time.Hour),
			})
			if err != nil {
				t.Fatal(err)
			}
			sessions := sessiontest.New(t, ffmpeg.New(), t.TempDir(), nil)
			defer sessions.Close()
			handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), sessions)

			rec := httptest.NewRecorder()
			handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00000.m4s", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("segment status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			raw, err := os.ReadFile(argsPath) // #nosec G304 -- argsPath is under this test's TempDir
			if err != nil {
				t.Fatalf("ffmpeg was not started: %v", err)
			}
			args := strings.Split(strings.TrimSpace(string(raw)), "\n")
			// The grant stores the stream index plus one, so 3 names stream 2.
			if got := hasArgPair(args, "-map", "0:2"); got != tc.wantMap {
				t.Fatalf("-map 0:2 present = %v, want %v; args = %q", got, tc.wantMap, args)
			}
		})
	}
}

func hasArgPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}
