package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
)

func newStrictTestHDRMedia(t *testing.T) (*Server, store.MediaID) {
	t.Helper()
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hdr10plus.mp4")
	if err := os.WriteFile(path, []byte("beamfall hdr bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:           "library-local",
		Path:                path,
		Title:               "Strict HDR test item",
		Container:           "mp4",
		Codec:               "hevc",
		Class:               store.MediaClassVideo,
		ContentFingerprint:  "fp-playback-info-strict-hdr",
		DurationSecondsHint: 120,
		VideoWidthHint:      3840,
		VideoHeightHint:     2160,
		SizeBytes:           50_000_000,
		Streams: []store.MediaFileStream{
			{
				Index:         0,
				Type:          "video",
				Codec:         "hevc",
				Profile:       "Main 10",
				ColorTransfer: "smpte2084",
				HDR10Plus:     true,
			},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2},
		},
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	return srv, media.ID
}

func postPlaybackInfoRaw(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := newJSONRequest(http.MethodPost, "/api/playback-info", strings.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func decodeErrorBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var payload map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error JSON %q: %v", rec.Body.String(), err)
	}
	return payload["error"]
}

func TestPlaybackInfoRefusesUnknownKeysAndNamesField(t *testing.T) {
	srv, mediaID := newStrictTestHDRMedia(t)

	cases := []struct {
		name      string
		body      string
		wantField string
	}{
		{
			name:      "unknown top-level key",
			body:      `{"mediaID":"` + string(mediaID) + `","unknownTopLevel":true,"capabilityProfile":{"client":"beamfall-test","containers":["mp4"],"directCodecs":["hevc","aac"]}}`,
			wantField: "unknownTopLevel",
		},
		{
			name:      "misspelled capabilityProfile.hdrFormat",
			body:      `{"mediaID":"` + string(mediaID) + `","capabilityProfile":{"client":"beamfall-test","containers":["mp4"],"directCodecs":["hevc","aac"],"videoCodecs":[{"codec":"hevc","profiles":["Main 10"],"maxHeight":2160}],"hdrFormat":["hdr10"]}}`,
			wantField: "hdrFormat",
		},
		{
			name:      "unknown key inside capabilityProfile",
			body:      `{"mediaID":"` + string(mediaID) + `","capabilityProfile":{"client":"beamfall-test","containers":["mp4"],"directCodecs":["hevc","aac"],"directVideoCodecs":["hevc"]}}`,
			wantField: "directVideoCodecs",
		},
		{
			name:      "unknown key inside videoCodecs element",
			body:      `{"mediaID":"` + string(mediaID) + `","capabilityProfile":{"client":"beamfall-test","containers":["mp4"],"directCodecs":["hevc"],"videoCodecs":[{"codec":"hevc","maxWidth":3840}]}}`,
			wantField: "maxWidth",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postPlaybackInfoRaw(t, srv, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			errMsg := decodeErrorBody(t, rec)
			if !strings.Contains(errMsg, tc.wantField) {
				t.Fatalf("error = %q, want it to name %q", errMsg, tc.wantField)
			}
		})
	}
}

func TestPlaybackInfoHDRFormatsGatedAndLegacyAbsent(t *testing.T) {
	srv, mediaID := newStrictTestHDRMedia(t)

	// Valid capabilityProfile.hdrFormats=["hdr10"] succeeds and gates HDR10+ off direct-play.
	gatedBody := `{"mediaID":"` + string(mediaID) + `","capabilityProfile":{"client":"beamfall-test","containers":["mp4"],"directCodecs":["hevc","aac"],"videoCodecs":[{"codec":"hevc","profiles":["Main 10"],"maxHeight":2160}],"hdrFormats":["hdr10"]}}`
	gatedRec := postPlaybackInfoRaw(t, srv, gatedBody)
	if gatedRec.Code != http.StatusOK {
		t.Fatalf("hdrFormats status = %d, want 200: %s", gatedRec.Code, gatedRec.Body.String())
	}
	var gatedResp playback.InfoResponse
	if err := json.Unmarshal(gatedRec.Body.Bytes(), &gatedResp); err != nil {
		t.Fatalf("decode gated playbackInfo: %v", err)
	}
	if gatedResp.Decision == "direct-play" {
		t.Fatalf("gated decision = %q, want HDR10+ gated off direct-play when hdrFormats=[\"hdr10\"]", gatedResp.Decision)
	}

	// Absent hdrFormats (and other absent optional fields) preserves legacy ungated HDR behavior.
	legacyBody := `{"mediaID":"` + string(mediaID) + `","capabilityProfile":{"client":"beamfall-test","containers":["mp4"],"directCodecs":["hevc","aac"],"videoCodecs":[{"codec":"hevc","profiles":["Main 10"],"maxHeight":2160}]}}`
	legacyRec := postPlaybackInfoRaw(t, srv, legacyBody)
	if legacyRec.Code != http.StatusOK {
		t.Fatalf("legacy status = %d, want 200: %s", legacyRec.Code, legacyRec.Body.String())
	}
	var legacyResp playback.InfoResponse
	if err := json.Unmarshal(legacyRec.Body.Bytes(), &legacyResp); err != nil {
		t.Fatalf("decode legacy playbackInfo: %v", err)
	}
	if legacyResp.Decision != "direct-play" {
		t.Fatalf("legacy decision = %q, want direct-play when hdrFormats is absent", legacyResp.Decision)
	}
}

func TestPlaybackInfoStrictDecodePreservesMalformedAndOversizedHandling(t *testing.T) {
	srv, _ := newStrictTestHDRMedia(t)

	malformed := postPlaybackInfoRaw(t, srv, `{"mediaID":`)
	if malformed.Code != http.StatusBadRequest {
		t.Fatalf("malformed status = %d, want 400: %s", malformed.Code, malformed.Body.String())
	}
	if got := decodeErrorBody(t, malformed); got != "invalid JSON body" {
		t.Fatalf("malformed error = %q, want %q", got, "invalid JSON body")
	}

	oversized := `{"mediaID":"` + strings.Repeat("a", maxControlBody+1) + `"}`
	tooLarge := postPlaybackInfoRaw(t, srv, oversized)
	if tooLarge.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d, want 413: %s", tooLarge.Code, tooLarge.Body.String())
	}
}
