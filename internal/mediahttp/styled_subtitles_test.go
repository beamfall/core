package mediahttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

func TestStyledSubtitleAndFontAttachment(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is unavailable")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is unavailable")
	}

	ctx := context.Background()
	dir := t.TempDir()
	ass := filepath.Join(dir, "styled.ass")
	font := filepath.Join(dir, "attachment.ttf")
	other := filepath.Join(dir, "attachment.txt")
	mkv := filepath.Join(dir, "styled.mkv")
	const fontBytes = "\x00\x01\x00\x00" + "fixture font payload 12345"
	writeFile := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(ass, `[Script Info]
ScriptType: v4.00+
PlayResX: 640
PlayResY: 360

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Fixture,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,0,2,10,10,10,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:00.00,0:00:01.00,Default,,0,0,0,,{\pos(20,40)}Styled
`)
	writeFile(font, fontBytes)
	writeFile(other, "arbitrary non-font attachment")
	cmd := exec.CommandContext(ctx, ffmpegPath, "-v", "error", // #nosec G204 -- resolved test binary, fixed args and temp paths
		"-i", ass, "-map", "0:s", "-c:s", "copy",
		"-attach", font, "-metadata:s:1", "mimetype=application/x-truetype-font",
		"-attach", other, "-metadata:s:2", "mimetype=text/plain", mkv)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create Matroska fixture: %v: %s", err, output)
	}

	st := storetest.Open(t)
	const fingerprint = "sha256:styled-subtitle-version"
	if err := st.AddMediaFileVersion(ctx, "media-alpha", store.MediaFile{
		Path: mkv, ContentFingerprint: fingerprint, VersionLabel: "Styled",
	}, []store.MediaFileStream{
		{Index: 0, Type: "subtitle", Codec: "ass"},
	}); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(st)
	scope, err := authService.Scope(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	grant := streamtoken.Grant{
		Target: streamtoken.TargetMediaSubtitle, MediaID: "media-alpha",
		SessionID: scope.SessionID, DeviceID: scope.DeviceID, ProfileID: scope.ProfileID,
		SubtitleStreamIndex: 1, SubtitleFileFingerprint: fingerprint, ExpiresAt: now.Add(time.Hour),
	}
	token, err := tokens.Issue(ctx, grant)
	if err != nil {
		t.Fatal(err)
	}
	noFileGrant := grant
	noFileGrant.SubtitleFileFingerprint = ""
	noFileToken, err := tokens.Issue(ctx, noFileGrant)
	if err != nil {
		t.Fatal(err)
	}
	runner := new(ffmpeg.Runner)
	indexes, err := runner.SubtitleFontIndexes(ctx, mkv)
	if err != nil || !reflect.DeepEqual(indexes, []int{1}) {
		t.Fatalf("fixture font stream indexes = %v, error = %v", indexes, err)
	}
	handler := NewHandler(st, authService, tokens, nil, nil)
	handler.SetSubtitles(runner, t.TempDir())

	for _, tc := range []struct {
		name, token, path, contentType, body string
		status                               int
	}{
		{"styled subtitle", token, "0.ass", "text/x-ssa; charset=utf-8", `{\pos(20,40)}Styled`, http.StatusOK},
		{"font indexes", token, "0.ass/fonts", "application/json", "[1]", http.StatusOK},
		{"font", token, "0.ass/fonts/1", "application/octet-stream", fontBytes, http.StatusOK},
		{"invalid token", "invalid", "0.ass/fonts/1", "", "", http.StatusUnauthorized},
		{"non-font attachment", token, "0.ass/fonts/2", "", "", http.StatusNotFound},
		{"non-attachment stream", token, "0.ass/fonts/0", "", "", http.StatusNotFound},
		{"wrong subtitle track", token, "2.ass/fonts/1", "", "", http.StatusNotFound},
		{"unbound token", noFileToken, "0.ass/fonts/1", "", "", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/media/subtitle/"+tc.token+"/"+tc.path, nil)
			rec := httptest.NewRecorder()
			handler.Subtitles(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if tc.contentType != "" && rec.Header().Get("Content-Type") != tc.contentType {
				t.Errorf("Content-Type = %q, want %q", rec.Header().Get("Content-Type"), tc.contentType)
			}
			if tc.body != "" && !strings.Contains(rec.Body.String(), tc.body) {
				t.Errorf("response did not contain %q: %q", tc.body, rec.Body.String())
			}
			assertTokenByteCacheControl(t, rec)
		})
	}

	handler.SetSealed(func(store.LibraryID) bool { return true })
	rec := httptest.NewRecorder()
	handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, "/api/media/subtitle/"+token+"/0.ass/fonts/1", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("sealed library returned %d, want 404", rec.Code)
	}
	assertTokenByteCacheControl(t, rec)

}
