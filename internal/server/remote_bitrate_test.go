package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
)

// TestPlaybackInfoRemoteBitrateCap covers CORE-1717 at the route: the server
// marks a request remote from its connection, so the admin's remote cap binds
// a public-address request and leaves a LAN request alone.
func TestPlaybackInfoRemoteBitrateCap(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "remote-cap.bin")
	if err := os.WriteFile(path, []byte("beamfall bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:           "library-local",
		Path:                path,
		Title:               "Probed remote cap",
		Container:           "mkv",
		Codec:               "hevc",
		Class:               store.MediaClassVideo,
		ContentFingerprint:  "fp-remote-cap-route",
		DurationSecondsHint: 120,
		VideoWidthHint:      1920,
		VideoHeightHint:     1080,
		SizeBytes:           90_000_000,
		Streams: []store.MediaFileStream{
			{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10"},
			{Index: 1, Type: "audio", Codec: "eac3", Channels: 6},
		},
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	limit := 4_000_000
	if _, err := st.SetConsoleConfig(ctx, store.ConsoleConfigPatch{RemoteMaxBitrate: &limit}, time.Unix(0, 0)); err != nil {
		t.Fatalf("SetConsoleConfig: %v", err)
	}
	body := `{"mediaID":"` + string(media.ID) + `","capabilityProfile":{"client":"beamfall-test","containers":["mkv","mp4"],"directCodecs":["hevc","eac3"],` +
		`"videoCodecs":[{"codec":"hevc","profiles":["Main","Main 10"],"maxHeight":1080}],"audioCodecs":[{"codec":"eac3","maxChannels":6},{"codec":"aac","maxChannels":6}]}}`

	info := func(remoteAddr string) playback.InfoResponse {
		t.Helper()
		req := newJSONRequest(http.MethodPost, "/api/playback-info", bytes.NewReader([]byte(body)))
		req.RemoteAddr = remoteAddr
		authorizeViewer(req)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want %d: %s", remoteAddr, rec.Code, http.StatusOK, rec.Body.String())
		}
		var resp playback.InfoResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode playbackInfo: %v", err)
		}
		return resp
	}

	if resp := info("127.0.0.1:5000"); resp.Decision != "direct-play" {
		t.Fatalf("LAN decision = %q/%q, want direct-play", resp.Decision, resp.Reason)
	}
	if resp := info("203.0.113.9:5000"); resp.Decision != "transcode" || resp.Reason != "policy-bitrate-ceiling" {
		t.Fatalf("remote decision = %q/%q, want transcode/policy-bitrate-ceiling", resp.Decision, resp.Reason)
	}
}
