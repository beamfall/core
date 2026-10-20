package mediahttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/genassets"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/playback/subtitleingest"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

func TestRedactMediaPathError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library", "title.mkv")
	msg := redactMediaPathError(errors.New("open "+path+": permission denied"), path)
	if strings.Contains(msg, path) {
		t.Fatalf("redacted error leaked path: %q", msg)
	}
	if !strings.Contains(msg, "[media-path]") {
		t.Fatalf("redacted error = %q, want marker", msg)
	}
}

func assertTokenByteCacheControl(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got, want := rec.Header().Get("Cache-Control"), "private, no-store"; got != want {
		t.Fatalf("Cache-Control = %q, want %q", got, want)
	}
}

type readerFromRecorder struct {
	*httptest.ResponseRecorder
	called bool
}

func (w *readerFromRecorder) ReadFrom(src io.Reader) (int64, error) {
	w.called = true
	return io.Copy(w.ResponseRecorder, src)
}

func TestStreamTokenResponseWriterPreservesReaderFrom(t *testing.T) {
	base := &readerFromRecorder{ResponseRecorder: httptest.NewRecorder()}
	w := MarkStreamTokenResponseNonStorable(base)
	readerFrom, ok := w.(io.ReaderFrom)
	if !ok {
		t.Fatal("wrapped ResponseWriter does not implement io.ReaderFrom")
	}
	if _, err := readerFrom.ReadFrom(strings.NewReader("beamfall bytes")); err != nil {
		t.Fatal(err)
	}
	if !base.called {
		t.Fatal("wrapped ResponseWriter did not delegate io.ReaderFrom")
	}
	assertTokenByteCacheControl(t, base.ResponseRecorder)
}

func TestStreamTokenByteRoutesMarkErrorsPrivateAndUnstorable(t *testing.T) {
	handler := &Handler{}
	routes := []struct {
		name  string
		path  string
		serve func(http.ResponseWriter, *http.Request)
	}{
		{name: "assets", path: "/api/media/assets/", serve: handler.Assets},
		{name: "subtitles", path: "/api/media/subtitle/", serve: handler.Subtitles},
		{name: "sidecars", path: "/api/media/sidecar/", serve: handler.Sidecars},
		{name: "hls", path: "/api/media/hls/", serve: handler.HLS},
		{name: "artwork candidate", path: "/api/media/artwork-candidate/", serve: handler.ArtworkCandidate},
		{name: "direct", path: "/api/media/direct/", serve: handler.Direct},
	}
	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			route.serve(rec, httptest.NewRequest(http.MethodGet, route.path, nil))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			assertTokenByteCacheControl(t, rec)
		})
	}
}

func TestDirectServesTokenGatedBytes(t *testing.T) {
	handler, token := newTestHandlerWithBytes(t, "session-default", "media-alpha", []byte("beamfall file bytes"))
	req := httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil)
	rec := httptest.NewRecorder()

	handler.Direct(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Body.String() != "beamfall file bytes" {
		t.Fatalf("body = %q, want file bytes", rec.Body.String())
	}
	assertTokenByteCacheControl(t, rec)
}

func TestDirectServesRangeRequests(t *testing.T) {
	handler, token := newTestHandlerWithBytes(t, "session-default", "media-alpha", []byte("0123456789"))
	req := httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil)
	req.Header.Set("Range", "bytes=2-5")
	rec := httptest.NewRecorder()

	handler.Direct(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusPartialContent, rec.Body.String())
	}
	if rec.Body.String() != "2345" {
		t.Fatalf("body = %q, want range bytes", rec.Body.String())
	}
	if got, want := rec.Header().Get("Content-Range"), "bytes 2-5/10"; got != want {
		t.Fatalf("Content-Range = %q, want %q", got, want)
	}
	assertTokenByteCacheControl(t, rec)
}

func TestDirectServeContentErrorIsPrivateAndUnstorable(t *testing.T) {
	handler, token := newTestHandlerWithBytes(t, "session-default", "media-alpha", []byte("0123456789"))
	req := httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil)
	req.Header.Set("Range", "bytes=invalid")
	rec := httptest.NewRecorder()

	handler.Direct(rec, req)

	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusRequestedRangeNotSatisfiable, rec.Body.String())
	}
	assertTokenByteCacheControl(t, rec)
}

func TestDirectRejectsEncryptedLibraryRangeBeforeDecrypt(t *testing.T) {
	handler, token := newTestHandlerWithBytes(t, "session-default", "media-alpha", []byte("ciphertext"))
	if err := handler.store.SetLibraryBacking(context.Background(), "library-local", false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	decryptor := &fakeEncryptedByteDecryptor{body: []byte("0123456789")}
	handler.SetEncryptedBytes(decryptor)
	req := httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil)
	req.Header.Set("Range", "bytes=2-5")
	rec := httptest.NewRecorder()

	handler.Direct(rec, req)

	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want 416: %s", rec.Code, rec.Body.String())
	}
	if decryptor.calls != 0 {
		t.Fatalf("decryptor calls = %d, want 0 before rejecting encrypted range", decryptor.calls)
	}
	assertTokenByteCacheControl(t, rec)
}

func TestDirectStreamsEncryptedLibraryRange(t *testing.T) {
	handler, token := newTestHandlerWithBytes(t, "session-default", "media-alpha", []byte("ciphertext"))
	if err := handler.store.SetLibraryBacking(context.Background(), "library-local", false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	decryptor := &fakeEncryptedStreamingDecryptor{
		fakeEncryptedByteDecryptor: fakeEncryptedByteDecryptor{body: []byte("0123456789")},
	}
	handler.SetEncryptedBytes(decryptor)
	req := httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil)
	req.Header.Set("Range", "bytes=2-5")
	rec := httptest.NewRecorder()

	handler.Direct(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "2345" {
		t.Fatalf("body = %q, want range bytes", rec.Body.String())
	}
	if decryptor.openCalls != 1 || decryptor.calls != 0 {
		t.Fatalf("open calls = %d, buffered decrypt calls = %d", decryptor.openCalls, decryptor.calls)
	}
	// Caller-bound identity: the route hands the decryptor the file's persisted
	// content fingerprint (never the path) under the media-bytes class.
	if decryptor.objectClass != "media-bytes" || decryptor.objectID == "" {
		t.Fatalf("object identity = %q/%q, want media-bytes + persisted fingerprint", decryptor.objectClass, decryptor.objectID)
	}
	wantFP, err := store.HashMediaFileContent(decryptor.path)
	if err != nil {
		t.Fatal(err)
	}
	if decryptor.objectID != wantFP {
		t.Fatalf("object id = %q, want the persisted content fingerprint %q", decryptor.objectID, wantFP)
	}
	assertTokenByteCacheControl(t, rec)
}

func TestDirectDecryptsEncryptedLibraryWholeRequest(t *testing.T) {
	handler, token := newTestHandlerWithBytes(t, "session-default", "media-alpha", []byte("ciphertext"))
	if err := handler.store.SetLibraryBacking(context.Background(), "library-local", false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	decryptor := &fakeEncryptedByteDecryptor{body: []byte("0123456789")}
	handler.SetEncryptedBytes(decryptor)
	req := httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil)
	rec := httptest.NewRecorder()

	handler.Direct(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "0123456789" {
		t.Fatalf("body = %q, want decrypted bytes", rec.Body.String())
	}
	if decryptor.calls != 1 || decryptor.libraryID != "library-local" || !strings.HasSuffix(decryptor.path, "media.bin") {
		t.Fatalf("decryptor call = calls:%d library:%q path:%q", decryptor.calls, decryptor.libraryID, decryptor.path)
	}
	assertTokenByteCacheControl(t, rec)
}

func TestDirectEncryptedLibraryFailsClosedWithoutDecryptor(t *testing.T) {
	handler, token := newTestHandlerWithBytes(t, "session-default", "media-alpha", []byte("ciphertext"))
	if err := handler.store.SetLibraryBacking(context.Background(), "library-local", false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	rec := httptest.NewRecorder()

	handler.Direct(rec, httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for encrypted library without decryptor", rec.Code)
	}
}

// TestDirectRejectsTokenAfterSeal: a previously-minted, otherwise-valid byte
// token fails closed once the Media's Library is sealed (ADR-0098 area 2).
func TestDirectRejectsTokenAfterSeal(t *testing.T) {
	handler, token := newTestHandlerWithBytes(t, "session-default", "media-alpha", []byte("beamfall bytes"))
	// Token mints + serves fine before sealing.
	pre := httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil)
	preRec := httptest.NewRecorder()
	handler.Direct(preRec, pre)
	if preRec.Code != http.StatusOK {
		t.Fatalf("pre-seal status = %d, want 200: %s", preRec.Code, preRec.Body.String())
	}
	// Seal media-alpha's Library: the same token now fails closed.
	handler.SetSealed(func(libraryID store.LibraryID) bool { return libraryID == "library-local" })
	req := httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil)
	rec := httptest.NewRecorder()
	handler.Direct(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("post-seal status = %d, want 404 (sealed Library denies bytes)", rec.Code)
	}
}

func TestDirectRejectsTamperedToken(t *testing.T) {
	handler, token := newTestHandler(t, "session-default", "media-alpha")
	req := httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token+"x", nil)
	rec := httptest.NewRecorder()

	handler.Direct(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	assertTokenByteCacheControl(t, rec)
}

func TestHLSConditionalResponseIsPrivateAndUnstorable(t *testing.T) {
	handler, token, cacheDir := newPrecomputedHLSTestHandler(t, "session-default", "media-alpha", false)
	modTime := time.Unix(100, 0).UTC()
	path := filepath.Join(cacheDir, "720p", "seg00000.m4s")
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00000.m4s", nil)
	req.Header.Set("If-Modified-Since", modTime.Add(time.Second).Format(http.TimeFormat))
	rec := httptest.NewRecorder()

	handler.HLS(rec, req)

	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusNotModified, rec.Body.String())
	}
	assertTokenByteCacheControl(t, rec)
}

// indexRecordingSubtitleConverter stands in for ffmpeg: it records the stream
// index it was asked to map and writes a WebVTT body that carries inline markup,
// so the test can observe the ingest boundary rewriting it.
type indexRecordingSubtitleConverter struct {
	indexes []int
	paths   []string
	body    string
	bodies  map[string]string
}

func (f *indexRecordingSubtitleConverter) ExtractSubtitleToWebVTT(_ context.Context, src string, streamIndex int, dst string) error {
	f.indexes = append(f.indexes, streamIndex)
	f.paths = append(f.paths, src)
	body := f.body
	if f.bodies != nil {
		body = f.bodies[src]
	}
	return os.WriteFile(dst, []byte(body), 0o600) // #nosec G703 -- test double; dst is the handler-chosen temp path under t.TempDir()
}

// newMuxedSubtitleTestHandler seeds a transcoded MKV carrying muxed srt + ass +
// pgs tracks, runs playbackInfo so the subtitle URLs are minted by production
// code, and returns the handler plus the caption tracks it advertised.
func newMuxedSubtitleTestHandler(t *testing.T, converter *indexRecordingSubtitleConverter) (*Handler, []playback.CaptionTrack) {
	t.Helper()
	st := storetest.Open(t)
	path := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(path, []byte("mkv bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              "movie",
		Container:          "mkv",
		Codec:              "hevc",
		ContentFingerprint: "sha256:muxed-subtitle-route",
		Streams: []store.MediaFileStream{
			{Index: 0, Type: "video", Codec: "hevc"},
			{Index: 1, Type: "audio", Codec: "aac"},
			{Index: 2, Type: "subtitle", Codec: "subrip", Language: "eng"},
			{Index: 3, Type: "subtitle", Codec: "ass", Language: "jpn"},
			{Index: 4, Type: "subtitle", Codec: "hdmv_pgs_subtitle", Language: "eng"},
		},
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	clock := func() time.Time { return now }
	tokens := streamtoken.NewService([]byte("test-token-key"), clock)
	resp, err := playback.NewService(st, tokens, clock).Info(context.Background(), scope, playback.InfoRequest{
		MediaID: string(media.ID),
		CapabilityProfile: playback.CapabilityProfile{
			Client:          "beamfall-web",
			Containers:      []string{"mp4"},
			DirectCodecs:    []string{"h264"},
			SubtitleFormats: []string{"webvtt"},
			DownstreamAudio: "stereo",
		},
	})
	if err != nil {
		t.Fatalf("playback Info: %v", err)
	}
	if resp.Decision != "transcode" {
		t.Fatalf("Decision = %q, want transcode", resp.Decision)
	}
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil))
	handler.SetSubtitles(converter, t.TempDir())
	return handler, resp.CaptionTracks
}

func muxedTrackURL(t *testing.T, tracks []playback.CaptionTrack, index int) string {
	t.Helper()
	for _, track := range tracks {
		if track.StreamIndex != nil && *track.StreamIndex == index {
			return track.URL
		}
	}
	t.Fatalf("no caption track with stream index %d in %+v", index, tracks)
	return ""
}

// TestSubtitlesServesMintedMuxedTrackAsSanitizedWebVTT covers
// CORE-MUXED-SUBTITLE-GRANT-1: GET on the URL playbackInfo minted for a muxed
// text track is 200 text/vtt carrying the ingest-sanitized body, the converter
// was asked for exactly that stream index, a PGS track advertises no URL, and
// revoking the session turns the URL into 410.
func TestSubtitlesServesMintedMuxedTrackAsSanitizedWebVTT(t *testing.T) {
	raw := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\n<script>alert(1)</script>Hello <b>muxed</b>\n"
	converter := &indexRecordingSubtitleConverter{body: raw}
	handler, tracks := newMuxedSubtitleTestHandler(t, converter)
	if url := muxedTrackURL(t, tracks, 4); url != "" {
		t.Fatalf("PGS track URL = %q, want none", url)
	}
	for _, index := range []int{2, 3} {
		url := muxedTrackURL(t, tracks, index)
		if !strings.HasPrefix(url, "/api/media/subtitle/") {
			t.Fatalf("track %d URL = %q, want a /api/media/subtitle/ URL", index, url)
		}
		rec := httptest.NewRecorder()
		handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, url, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("track %d status = %d, want 200: %s", index, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != "text/vtt; charset=utf-8" {
			t.Fatalf("track %d Content-Type = %q, want text/vtt; charset=utf-8", index, got)
		}
		body := rec.Body.String()
		if strings.Contains(body, "<script>") {
			t.Fatalf("track %d body kept inline script markup: %q", index, body)
		}
		if want := subtitleingest.SanitizeWebVTT(raw); body != want {
			t.Fatalf("track %d body = %q, want the sanitized WebVTT %q", index, body, want)
		}
		assertTokenByteCacheControl(t, rec)
	}
	if got := fmt.Sprint(converter.indexes); got != "[2 3]" {
		t.Fatalf("converter stream indexes = %s, want [2 3] (one -map 0:<idx> extract per track)", got)
	}
	trackTwoURL := muxedTrackURL(t, tracks, 2)
	rec := httptest.NewRecorder()
	handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, strings.TrimSuffix(trackTwoURL, "/2.vtt")+"/3.vtt", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("track 2 token used for track 3 status = %d, want 404", rec.Code)
	}

	if err := handler.store.RevokeDevice(context.Background(), "device-tv"); err != nil {
		t.Fatalf("RevokeDevice returned error: %v", err)
	}
	rec = httptest.NewRecorder()
	handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, muxedTrackURL(t, tracks, 2), nil))
	if rec.Code != http.StatusGone {
		t.Fatalf("status after revocation = %d, want %d: %s", rec.Code, http.StatusGone, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "Hello") {
		t.Fatalf("revoked subtitle leaked cue text: %q", rec.Body.String())
	}
	assertTokenByteCacheControl(t, rec)
}

// TestSubtitlesUsesNegotiatedVersionFile proves a muxed-subtitle URL is bound
// to the exact non-primary Version selected by playbackInfo, not merely to its
// Media and stream index. It also rejects a signed fingerprint that belongs to
// another Media.
func TestSubtitlesUsesNegotiatedVersionFile(t *testing.T) {
	st := storetest.Open(t)
	defaultPath := filepath.Join(t.TempDir(), "default-dolby-vision.mkv")
	selectedPath := filepath.Join(t.TempDir(), "selected-sdr.mkv")
	otherPath := filepath.Join(t.TempDir(), "other-media.mkv")
	for path, body := range map[string]string{
		defaultPath:  "default version bytes",
		selectedPath: "selected version bytes",
		otherPath:    "other media bytes",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               defaultPath,
		Title:              "versioned movie",
		Container:          "mkv",
		Codec:              "hevc",
		Class:              store.MediaClassVideo,
		ContentFingerprint: "sha256:subtitle-default-version",
		VideoWidthHint:     1920,
		VideoHeightHint:    1080,
		Streams: []store.MediaFileStream{
			{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10", DoViProfile: 5},
			{Index: 1, Type: "audio", Codec: "eac3", Channels: 6},
			{Index: 5, Type: "subtitle", Codec: "subrip", Language: "eng"},
		},
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	const selectedFingerprint = "sha256:subtitle-selected-version"
	if err := st.AddMediaFileVersion(context.Background(), media.ID, store.MediaFile{
		Path:               selectedPath,
		ContentFingerprint: selectedFingerprint,
		VersionLabel:       "sdr",
	}, []store.MediaFileStream{
		{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10"},
		{Index: 1, Type: "audio", Codec: "eac3", Channels: 6},
		{Index: 5, Type: "subtitle", Codec: "subrip", Language: "eng"},
	}); err != nil {
		t.Fatalf("AddMediaFileVersion: %v", err)
	}
	other, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               otherPath,
		Title:              "other movie",
		Container:          "mkv",
		Codec:              "hevc",
		Class:              store.MediaClassVideo,
		ContentFingerprint: "sha256:subtitle-other-media",
	})
	if err != nil {
		t.Fatalf("Import other MediaFile: %v", err)
	}
	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	clock := func() time.Time { return now }
	tokens := streamtoken.NewService([]byte("test-token-key"), clock)
	resp, err := playback.NewService(st, tokens, clock).Info(context.Background(), scope, playback.InfoRequest{
		MediaID: string(media.ID),
		CapabilityProfile: playback.CapabilityProfile{
			Client:     "beamfall-test",
			Containers: []string{"mkv"},
			VideoCodecs: []playback.VideoCodecCap{
				{Codec: "hevc", Profiles: []string{"Main 10"}, MaxHeight: 1080},
			},
			AudioCodecs:     []playback.AudioCodecCap{{Codec: "eac3", MaxChannels: 6}},
			HDRFormats:      []string{"hdr10"},
			SubtitleFormats: []string{"subrip", "webvtt"},
		},
	})
	if err != nil {
		t.Fatalf("playback Info: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play of non-primary SDR Version", resp.Decision)
	}
	url := muxedTrackURL(t, resp.CaptionTracks, 5)
	token := strings.TrimSuffix(strings.TrimPrefix(url, "/api/media/subtitle/"), "/5.vtt")
	grant, err := tokens.UnverifiedGrant(token, streamtoken.TargetMediaSubtitle)
	if err != nil {
		t.Fatalf("UnverifiedGrant: %v", err)
	}
	if grant.MediaID != media.ID || grant.SubtitleFileFingerprint != selectedFingerprint || grant.SubtitleStreamIndex != 6 {
		t.Fatalf("grant = %+v, want media %s, selected fingerprint %s, encoded stream 6", grant, media.ID, selectedFingerprint)
	}
	selectedRaw := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\n<script>bad()</script>Selected version\n"
	converter := &indexRecordingSubtitleConverter{bodies: map[string]string{
		defaultPath:  "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nWrong primary version\n",
		selectedPath: selectedRaw,
	}}
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil))
	handler.SetSubtitles(converter, t.TempDir())
	rec := httptest.NewRecorder()
	handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("selected Version subtitle status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), subtitleingest.SanitizeWebVTT(selectedRaw); got != want {
		t.Fatalf("selected Version body = %q, want sanitized %q", got, want)
	}
	if got := fmt.Sprint(converter.paths); got != fmt.Sprintf("[%s]", selectedPath) {
		t.Fatalf("converter source paths = %s, want only negotiated file %s", got, selectedPath)
	}

	wrongMediaToken, err := tokens.Issue(context.Background(), streamtoken.Grant{
		Target:                  streamtoken.TargetMediaSubtitle,
		MediaID:                 media.ID,
		DeviceID:                scope.DeviceID,
		SessionID:               scope.SessionID,
		ProfileID:               scope.ProfileID,
		SubtitleStreamIndex:     6,
		SubtitleFileFingerprint: "sha256:subtitle-other-media",
		ExpiresAt:               now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Issue wrong-Media fingerprint token: %v", err)
	}
	if other.ID == media.ID {
		t.Fatalf("other Media ID = %s, want distinct from %s", other.ID, media.ID)
	}
	rec = httptest.NewRecorder()
	handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, "/api/media/subtitle/"+wrongMediaToken+"/5.vtt", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("other Media fingerprint through this Media grant status = %d, want 404", rec.Code)
	}
}

func TestDirectRejectsTokenAfterDeviceRevocation(t *testing.T) {
	handler, token := newTestHandler(t, "session-default", "media-alpha")
	if err := handler.store.RevokeDevice(context.Background(), "device-tv"); err != nil {
		t.Fatalf("RevokeDevice returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil)
	rec := httptest.NewRecorder()

	handler.Direct(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestDirectRejectsTokenAfterProfileDisabled(t *testing.T) {
	handler, token := newTestHandlerWithBytes(t, "session-managed", "media-alpha", []byte("beamfall bytes"))
	disabled := true
	if _, err := handler.store.UpdateProfileSettings(context.Background(), "profile-managed", store.ProfileSettings{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()

	handler.Direct(rec, httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 after Profile disable", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "beamfall bytes") {
		t.Fatalf("direct body leaked bytes after Profile disable: %q", rec.Body.String())
	}
}

func TestDirectRejectsExpiredStreamToken(t *testing.T) {
	handler, _ := newTestHandlerWithBytes(t, "session-default", "media-alpha", []byte("beamfall bytes"))
	now := time.Unix(100, 0).UTC()
	handler.tokens = streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	scope, err := handler.auth.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	token, err := handler.tokens.Issue(context.Background(), streamtoken.Grant{
		Target:    streamtoken.TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	rec := httptest.NewRecorder()

	handler.Direct(rec, httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestDirectKeepsRevealGating(t *testing.T) {
	handler, _ := newTestHandlerWithBytes(t, "session-revealed", "media-archive", []byte("restricted bytes"))
	scope, err := handler.auth.Scope(context.Background(), "session-revealed")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	token, err := handler.tokens.Issue(context.Background(), streamtoken.Grant{
		Target:     streamtoken.TargetMediaDirect,
		MediaID:    "media-archive",
		DeviceID:   scope.DeviceID,
		SessionID:  scope.SessionID,
		ProfileID:  scope.ProfileID,
		Restricted: true,
		ExpiresAt:  now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.store.SetSessionRevealed(context.Background(), "session-revealed", false); err != nil {
		t.Fatalf("SetSessionRevealed: %v", err)
	}
	rec := httptest.NewRecorder()

	handler.Direct(rec, httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 after relock", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "restricted bytes") {
		t.Fatalf("direct body leaked restricted bytes after relock: %q", rec.Body.String())
	}
}

func TestDirectRejectsTokenAfterBackingSessionExpiry(t *testing.T) {
	st := storetest.Open(t)
	if err := st.EnrollDevice(
		context.Background(),
		store.Device{ID: "device-expired", Name: "Expired", Role: store.DeviceRoleViewer, SecretHash: store.HashDeviceSecret("expired-secret")},
		store.Session{
			ID:        "session-expired",
			DeviceID:  "device-expired",
			ProfileID: "profile-default",
			ExpiresAt: time.Unix(150, 0).UTC(),
		},
	); err != nil {
		t.Fatal(err)
	}
	tokenNow := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return tokenNow })
	token, err := tokens.Issue(context.Background(), streamtoken.Grant{
		Target:    streamtoken.TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  "device-expired",
		SessionID: "session-expired",
		ProfileID: "profile-default",
		ExpiresAt: tokenNow.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewServiceWithClock(st, func() time.Time { return time.Unix(151, 0).UTC() })
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil))
	req := httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil)
	rec := httptest.NewRecorder()

	handler.Direct(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func newTestHandler(t *testing.T, sessionID store.SessionID, mediaID store.MediaID) (*Handler, string) {
	t.Helper()
	st := storetest.Open(t)
	authService := auth.NewService(st)
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	scope, err := authService.Scope(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	token, err := tokens.Issue(context.Background(), streamtoken.Grant{
		Target:    streamtoken.TargetMediaDirect,
		MediaID:   mediaID,
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil)), token
}

func newTestHandlerWithBytes(t *testing.T, sessionID store.SessionID, mediaID store.MediaID, body []byte) (*Handler, string) {
	t.Helper()
	handler, token := newTestHandler(t, sessionID, mediaID)
	path := filepath.Join(t.TempDir(), "media.bin")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := handler.store.SetPrimaryMediaFile(context.Background(), mediaID, path); err != nil {
		t.Fatal(err)
	}
	return handler, token
}

type fakeEncryptedByteDecryptor struct {
	body        []byte
	calls       int
	libraryID   store.LibraryID
	path        string
	objectClass string
	objectID    string
}

type fakeEncryptedStreamingDecryptor struct {
	fakeEncryptedByteDecryptor
	openCalls int
}

func (f *fakeEncryptedStreamingDecryptor) OpenDecryptReader(libraryID store.LibraryID, ciphertextPath, objectClass, immutableObjectID string) (io.ReadSeekCloser, error) {
	f.openCalls++
	f.libraryID = libraryID
	f.path = ciphertextPath
	f.objectClass = objectClass
	f.objectID = immutableObjectID
	return &testReadSeekCloser{Reader: bytes.NewReader(f.body)}, nil
}

type testReadSeekCloser struct {
	*bytes.Reader
}

func (*testReadSeekCloser) Close() error { return nil }

func (f *fakeEncryptedByteDecryptor) DecryptBytes(libraryID store.LibraryID, ciphertextPath, objectClass, immutableObjectID string) ([]byte, error) {
	f.calls++
	f.libraryID = libraryID
	f.path = ciphertextPath
	f.objectClass = objectClass
	f.objectID = immutableObjectID
	return append([]byte(nil), f.body...), nil
}

func newArtworkTestHandler(t *testing.T) (*Handler, *artwork.Service) {
	t.Helper()
	st := storetest.Open(t)
	authService := auth.NewService(st)
	tokens := streamtoken.NewService([]byte("test-token-key"), time.Now)
	artworkService := artwork.NewService(st, nil, t.TempDir())
	return NewHandler(st, authService, tokens, artworkService, playback.NewSessions(nil, t.TempDir(), nil)), artworkService
}

func writeTestPoster(t *testing.T, svc *artwork.Service, mediaID store.MediaID) {
	t.Helper()
	path := svc.PosterPath(mediaID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("jpeg bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTestArtwork(t *testing.T, svc *artwork.Service, mediaID store.MediaID, slot, body string) {
	t.Helper()
	path := filepath.Join(filepath.Dir(svc.PosterPath(mediaID)), slot+".jpg")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func artworkRequest(mediaID, slot string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/media/artwork/"+mediaID+"/"+slot, nil)
	req.Header.Set("X-Beamfall-Session", "session-default")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	return req
}

func TestArtworkServesPosterForVisibleMedia(t *testing.T) {
	handler, svc := newArtworkTestHandler(t)
	writeTestPoster(t, svc, "media-alpha")
	rec := httptest.NewRecorder()

	handler.Artwork(rec, artworkRequest("media-alpha", "poster"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("Content-Type = %q, want image/jpeg", got)
	}
	if rec.Body.String() != "jpeg bytes" {
		t.Fatalf("body = %q, want poster bytes", rec.Body.String())
	}
}

func TestArtworkVersionedRequestCachesHardAndHonorsETag(t *testing.T) {
	handler, svc := newArtworkTestHandler(t)
	writeTestPoster(t, svc, "media-alpha")
	version, ok := svc.ArtworkVersion("media-alpha", artwork.SlotPoster)
	if !ok {
		t.Fatal("ArtworkVersion = absent, want poster version")
	}

	// A bare request (no ?v) keeps today's behavior: no long-cache header.
	bare := httptest.NewRecorder()
	handler.Artwork(bare, artworkRequest("media-alpha", "poster"))
	if bare.Code != http.StatusOK {
		t.Fatalf("bare status = %d, want 200: %s", bare.Code, bare.Body.String())
	}
	if got := bare.Header().Get("Cache-Control"); got != "" {
		t.Fatalf("bare Cache-Control = %q, want absent", got)
	}

	// With ?v present the response is immutable for a year and carries an ETag.
	req := artworkRequest("media-alpha", "poster?v="+version)
	rec := httptest.NewRecorder()
	handler.Artwork(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("versioned status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q, want private immutable long-cache", got)
	}
	if got, want := rec.Header().Get("ETag"), `"`+version+`"`; got != want {
		t.Fatalf("ETag = %q, want %q", got, want)
	}
	if rec.Body.String() != "jpeg bytes" {
		t.Fatalf("body = %q, want poster bytes", rec.Body.String())
	}

	// A matching If-None-Match short-circuits to 304 without a body.
	cond := artworkRequest("media-alpha", "poster?v="+version)
	cond.Header.Set("If-None-Match", `"`+version+`"`)
	condRec := httptest.NewRecorder()
	handler.Artwork(condRec, cond)
	if condRec.Code != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want 304", condRec.Code)
	}
	if condRec.Body.Len() != 0 {
		t.Fatalf("304 body = %q, want empty", condRec.Body.String())
	}

	// A stale or arbitrary version token is advisory only: serve bytes but do
	// not bless the mismatched URL as immutable, and never 304 it.
	stale := artworkRequest("media-alpha", "poster?v=stale")
	stale.Header.Set("If-None-Match", `"stale"`)
	staleRec := httptest.NewRecorder()
	handler.Artwork(staleRec, stale)
	if staleRec.Code != http.StatusOK {
		t.Fatalf("stale version status = %d, want 200", staleRec.Code)
	}
	if got := staleRec.Header().Get("Cache-Control"); got != "" {
		t.Fatalf("stale Cache-Control = %q, want absent", got)
	}
	if staleRec.Body.String() != "jpeg bytes" {
		t.Fatalf("stale body = %q, want poster bytes", staleRec.Body.String())
	}
}

func TestArtworkServesBackdropForVisibleMedia(t *testing.T) {
	handler, svc := newArtworkTestHandler(t)
	writeTestArtwork(t, svc, "media-alpha", artwork.SlotBackdrop, "backdrop bytes")
	rec := httptest.NewRecorder()

	handler.Artwork(rec, artworkRequest("media-alpha", artwork.SlotBackdrop))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("Content-Type = %q, want image/jpeg", got)
	}
	if rec.Body.String() != "backdrop bytes" {
		t.Fatalf("body = %q, want backdrop bytes", rec.Body.String())
	}
}

func TestArtworkFallsBackToPosterForMissingSlot(t *testing.T) {
	handler, svc := newArtworkTestHandler(t)
	// Only a poster exists; the client asks for the backdrop slot. Per-slot
	// fallback resolves at the endpoint, so the request serves the
	// poster bytes rather than 404ing — the client never re-implements the chain.
	writeTestPoster(t, svc, "media-alpha")
	rec := httptest.NewRecorder()

	handler.Artwork(rec, artworkRequest("media-alpha", artwork.SlotBackdrop))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (poster fallback): %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "jpeg bytes" {
		t.Fatalf("body = %q, want poster bytes served as backdrop fallback", rec.Body.String())
	}
}

func TestArtworkHidesRestrictedMediaWithoutReveal(t *testing.T) {
	handler, svc := newArtworkTestHandler(t)
	writeTestPoster(t, svc, "media-archive")
	rec := httptest.NewRecorder()

	// session-default is not revealed; media-archive lives in the restricted Library.
	handler.Artwork(rec, artworkRequest("media-archive", "poster"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unrevealed restricted artwork", rec.Code)
	}
}

func TestArtworkRequiresCredentials(t *testing.T) {
	handler, svc := newArtworkTestHandler(t)
	writeTestPoster(t, svc, "media-alpha")
	req := httptest.NewRequest(http.MethodGet, "/api/media/artwork/media-alpha/poster", nil)
	rec := httptest.NewRecorder()

	handler.Artwork(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without credentials", rec.Code)
	}
}

func TestArtworkRejectsUnknownSlot(t *testing.T) {
	handler, svc := newArtworkTestHandler(t)
	writeTestPoster(t, svc, "media-alpha")
	rec := httptest.NewRecorder()

	handler.Artwork(rec, artworkRequest("media-alpha", "banner"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unknown slot", rec.Code)
	}
}

type fakeCandidatePreviewFetcher struct {
	seen []string
	data []byte
}

func (f *fakeCandidatePreviewFetcher) FetchResource(_ context.Context, rawURL string) ([]byte, error) {
	f.seen = append(f.seen, rawURL)
	return f.data, nil
}

func TestArtworkCandidatePreviewServesTokenGatedCachedBytes(t *testing.T) {
	st := storetest.Open(t)
	rawURL := "https://img.example.test/a.jpg"
	if err := st.UpsertArtworkCandidate(context.Background(), "media-alpha", artwork.SlotPoster, rawURL, "demo", "lookup"); err != nil {
		t.Fatal(err)
	}
	candidateID := store.ArtworkCandidateID("media-alpha", artwork.SlotPoster, rawURL)
	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	token, err := tokens.Issue(context.Background(), streamtoken.Grant{
		Target:    streamtoken.TargetArtworkCandidate,
		MediaID:   "media-alpha",
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &fakeCandidatePreviewFetcher{data: []byte("preview bytes")}
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil))
	handler.SetArtworkCandidateFetcher(fetcher)
	req := httptest.NewRequest(http.MethodGet, "/api/media/artwork-candidate/"+token+"/"+candidateID, nil)

	rec := httptest.NewRecorder()
	handler.ArtworkCandidate(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "preview bytes" {
		t.Fatalf("body = %q, want preview bytes", rec.Body.String())
	}
	assertTokenByteCacheControl(t, rec)
	if len(fetcher.seen) != 1 || fetcher.seen[0] != rawURL {
		t.Fatalf("fetcher saw %#v, want %q", fetcher.seen, rawURL)
	}

	rec = httptest.NewRecorder()
	handler.ArtworkCandidate(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "preview bytes" {
		t.Fatalf("cached response status=%d body=%q", rec.Code, rec.Body.String())
	}
	if len(fetcher.seen) != 1 {
		t.Fatalf("fetcher called %d times, want cached second response", len(fetcher.seen))
	}

	hiddenURL := "https://img.example.test/hidden.jpg"
	if err := st.UpsertArtworkCandidate(context.Background(), "media-archive", artwork.SlotPoster, hiddenURL, "demo", "lookup"); err != nil {
		t.Fatal(err)
	}
	hiddenID := store.ArtworkCandidateID("media-archive", artwork.SlotPoster, hiddenURL)
	hiddenToken, err := tokens.Issue(context.Background(), streamtoken.Grant{
		Target:    streamtoken.TargetArtworkCandidate,
		MediaID:   "media-archive",
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	hiddenRec := httptest.NewRecorder()
	handler.ArtworkCandidate(hiddenRec, httptest.NewRequest(http.MethodGet, "/api/media/artwork-candidate/"+hiddenToken+"/"+hiddenID, nil))
	unknownRec := httptest.NewRecorder()
	handler.ArtworkCandidate(unknownRec, httptest.NewRequest(http.MethodGet, "/api/media/artwork-candidate/"+hiddenToken+"/"+strings.Repeat("0", 64), nil))
	if hiddenRec.Code != http.StatusNotFound || unknownRec.Code != http.StatusNotFound || hiddenRec.Body.String() != unknownRec.Body.String() {
		t.Fatalf("hidden status/body = %d/%q, unknown = %d/%q", hiddenRec.Code, hiddenRec.Body.String(), unknownRec.Code, unknownRec.Body.String())
	}
}

func TestArtworkCandidateRejectsSealedLibrary(t *testing.T) {
	st := storetest.Open(t)
	rawURL := "https://img.example.test/sealed.jpg"
	if err := st.UpsertArtworkCandidate(context.Background(), "media-alpha", artwork.SlotPoster, rawURL, "demo", "lookup"); err != nil {
		t.Fatal(err)
	}
	candidateID := store.ArtworkCandidateID("media-alpha", artwork.SlotPoster, rawURL)
	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	token, err := tokens.Issue(context.Background(), streamtoken.Grant{
		Target:    streamtoken.TargetArtworkCandidate,
		MediaID:   "media-alpha",
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &fakeCandidatePreviewFetcher{data: []byte("preview bytes")}
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil))
	handler.SetArtworkCandidateFetcher(fetcher)
	handler.SetSealed(func(libraryID store.LibraryID) bool { return libraryID == "library-local" })
	rec := httptest.NewRecorder()

	handler.ArtworkCandidate(rec, httptest.NewRequest(http.MethodGet, "/api/media/artwork-candidate/"+token+"/"+candidateID, nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for sealed Library candidate", rec.Code)
	}
	if len(fetcher.seen) != 0 {
		t.Fatalf("fetcher called for sealed Library: %#v", fetcher.seen)
	}
}

type fakeHLSTranscoder struct{}

func (fakeHLSTranscoder) StartHLS(_ context.Context, _, dir string, _ ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	if err := os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte("#EXTM3U\n"), 0o600); err != nil {
		return nil, err
	}
	return &ffmpeg.HLSProcess{}, nil
}

func (fakeHLSTranscoder) StartHLSSegments(_ context.Context, _, dir string, startSegment, _ int, _ ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	if err := os.WriteFile(filepath.Join(dir, "init.mp4"), []byte("init"), 0o600); err != nil {
		return nil, err
	}
	name := filepath.Join(dir, fmt.Sprintf("seg%05d.m4s", startSegment))
	if err := os.WriteFile(name, []byte("m4s"), 0o600); err != nil {
		return nil, err
	}
	return &ffmpeg.HLSProcess{}, nil
}

func TestHLSServesPlaylistForValidToken(t *testing.T) {
	// feature:hls-transcode
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
		Target:    streamtoken.TargetMediaHLS,
		MediaID:   "media-alpha",
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions := playback.NewSessions(fakeHLSTranscoder{}, t.TempDir(), nil)
	defer sessions.Close()
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), sessions)

	// The master playlist advertises only rungs the seeded 720p source can
	// honestly back and is served without starting a transcode.
	req := httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/master.m3u8", nil)
	rec := httptest.NewRecorder()
	handler.HLS(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/vnd.apple.mpegurl" {
		t.Fatalf("Content-Type = %q, want application/vnd.apple.mpegurl", got)
	}
	master := rec.Body.String()
	if !strings.Contains(master, "#EXT-X-STREAM-INF") ||
		!strings.Contains(master, "720p/index.m3u8") ||
		strings.Contains(master, "1080p") {
		t.Fatalf("body = %q, want master playlist with honest rungs", master)
	}

	rec = httptest.NewRecorder()
	handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/index.m3u8", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("variant status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	playlist := rec.Body.String()
	if !strings.Contains(playlist, "#EXT-X-PLAYLIST-TYPE:VOD") ||
		!strings.Contains(playlist, "#EXT-X-MAP:URI=\"init.mp4\"") ||
		!strings.Contains(playlist, "#EXT-X-ENDLIST") ||
		!strings.Contains(playlist, "seg00029.m4s") ||
		strings.Contains(playlist, "seg00030.m4s") {
		t.Fatalf("body = %q, want synthesized VOD playlist covering 120s", playlist)
	}

	// The init segment and the fMP4 media segments serve as MP4 bytes with
	// Cache-Control: private, no-store: stream-token-gated bytes must not be
	// cached by shared caches or persisted by private caches.
	for _, name := range []string{"720p/init.mp4", "720p/seg00000.m4s"} {
		rec := httptest.NewRecorder()
		handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/"+name, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200: %s", name, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != "video/mp4" {
			t.Fatalf("%s: Content-Type = %q, want video/mp4", name, got)
		}
		assertTokenByteCacheControl(t, rec)
	}

	// A direct-play token must not unlock the HLS surface.
	directToken, err := tokens.Issue(context.Background(), streamtoken.Grant{
		Target:    streamtoken.TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+directToken+"/master.m3u8", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d for direct token on HLS route, want 401", rec.Code)
	}
}

func TestHLSRejectsSealedLibrary(t *testing.T) {
	handler, token := newHLSSealedTestHandler(t)
	handler.SetSealed(func(libraryID store.LibraryID) bool { return libraryID == "library-local" })
	rec := httptest.NewRecorder()

	handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00000.m4s", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for sealed Library HLS", rec.Code)
	}
}

func TestHLSLiveEncryptedLibraryFailsClosed(t *testing.T) {
	handler, token := newHLSSealedTestHandler(t)
	if err := handler.store.SetLibraryBacking(context.Background(), "library-local", false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	rec := httptest.NewRecorder()

	handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00000.m4s", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for encrypted live HLS without decrypted source", rec.Code)
	}
}

func newHLSSealedTestHandler(t *testing.T) (*Handler, string) {
	t.Helper()
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
		Target:    streamtoken.TargetMediaHLS,
		MediaID:   "media-alpha",
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions := playback.NewSessions(fakeHLSTranscoder{}, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	return NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), sessions), token
}

func TestHLSPrecomputedRenditionServesTokenGatedCacheBytes(t *testing.T) {
	handler, token, cacheDir := newPrecomputedHLSTestHandler(t, "session-default", "media-alpha", false)

	rec := httptest.NewRecorder()
	handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/master.m3u8", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("master status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/vnd.apple.mpegurl" {
		t.Fatalf("master Content-Type = %q, want application/vnd.apple.mpegurl", got)
	}
	if rec.Body.String() != "#EXTM3U\n# cached master\n" {
		t.Fatalf("master body = %q, want cached master bytes from %s", rec.Body.String(), cacheDir)
	}

	rec = httptest.NewRecorder()
	handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00000.m4s", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("segment status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "video/mp4" {
		t.Fatalf("segment Content-Type = %q, want video/mp4", got)
	}
	assertTokenByteCacheControl(t, rec)
	if rec.Body.String() != "cached segment" {
		t.Fatalf("segment body = %q, want cached segment bytes", rec.Body.String())
	}
}

func TestHLSPrecomputedRenditionKeepsRevealGating(t *testing.T) {
	handler, token, _ := newPrecomputedHLSTestHandler(t, "session-revealed", "media-archive", true)
	if err := handler.store.SetSessionRevealed(context.Background(), "session-revealed", false); err != nil {
		t.Fatalf("SetSessionRevealed: %v", err)
	}

	rec := httptest.NewRecorder()
	handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/master.m3u8", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 after relock: %s", rec.Code, rec.Body.String())
	}
}

func TestHLSPrecomputedRenditionRejectsWrongTargetToken(t *testing.T) {
	handler, token, _ := newPrecomputedHLSTestHandler(t, "session-default", "media-alpha", false)
	grant, err := handler.tokens.UnverifiedGrant(token, streamtoken.TargetMediaHLS)
	if err != nil {
		t.Fatal(err)
	}
	grant.Target = streamtoken.TargetMediaDirect
	directToken, err := handler.tokens.Issue(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+directToken+"/master.m3u8", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for direct token on HLS route", rec.Code)
	}
}

func TestHLSPrecomputedHotSegmentCacheActivatesOnlyDuringConcurrentFanout(t *testing.T) {
	handler, token, _ := newPrecomputedHLSTestHandler(t, "session-default", "media-alpha", false)
	handler.SetHotSegmentCache(1024)

	first := startBlockedHLSFetch(t, handler, token, "720p/seg00000.m4s")
	defer first.release()
	second := httptest.NewRecorder()
	rangeReq := httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00000.m4s", nil)
	rangeReq.Header.Set("Range", "bytes=0-5")
	handler.HLS(second, rangeReq)
	if second.Code != http.StatusPartialContent {
		t.Fatalf("second status = %d, want 206: %s", second.Code, second.Body.String())
	}
	if second.Body.String() != "cached" {
		t.Fatalf("range body = %q, want cached", second.Body.String())
	}
	if entries, bytes := handler.hotSegments.stats(); entries != 1 || bytes == 0 {
		t.Fatalf("hot cache stats during fan-out = %d/%d, want one resident segment", entries, bytes)
	}

	first.release()
	if entries, bytes := handler.hotSegments.stats(); entries != 0 || bytes != 0 {
		t.Fatalf("hot cache stats after fan-out = %d/%d, want empty", entries, bytes)
	}

	third := httptest.NewRecorder()
	handler.HLS(third, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00000.m4s", nil))
	if third.Code != http.StatusOK {
		t.Fatalf("third status = %d, want 200: %s", third.Code, third.Body.String())
	}
	if entries, bytes := handler.hotSegments.stats(); entries != 0 || bytes != 0 {
		t.Fatalf("hot cache stats after non-concurrent fetch = %d/%d, want empty", entries, bytes)
	}
}

func TestHLSPrecomputedHotSegmentCacheIsBounded(t *testing.T) {
	handler, token, cacheDir := newPrecomputedHLSTestHandler(t, "session-default", "media-alpha", false)
	if err := os.WriteFile(filepath.Join(cacheDir, "720p", "seg00001.m4s"), []byte("second segment"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler.SetHotSegmentCache(int64(len("cached segment")))

	first := startBlockedHLSFetch(t, handler, token, "720p/seg00000.m4s")
	defer first.release()
	rec := httptest.NewRecorder()
	handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00000.m4s", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("first fan-out status = %d: %s", rec.Code, rec.Body.String())
	}

	second := startBlockedHLSFetch(t, handler, token, "720p/seg00001.m4s")
	defer second.release()
	rec = httptest.NewRecorder()
	handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00001.m4s", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("second fan-out status = %d: %s", rec.Code, rec.Body.String())
	}
	if entries, bytes := handler.hotSegments.stats(); entries != 1 || bytes > int64(len("cached segment")) {
		t.Fatalf("hot cache stats = %d/%d, want one bounded resident segment", entries, bytes)
	}

	first.release()
	second.release()
}

func TestHLSPrecomputedHotSegmentCacheStillRequiresReveal(t *testing.T) {
	handler, token, _ := newPrecomputedHLSTestHandler(t, "session-revealed", "media-archive", true)
	handler.SetHotSegmentCache(1024)

	first := startBlockedHLSFetch(t, handler, token, "720p/seg00000.m4s")
	defer first.release()
	rec := httptest.NewRecorder()
	handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00000.m4s", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("fan-out status = %d: %s", rec.Code, rec.Body.String())
	}
	if entries, _ := handler.hotSegments.stats(); entries != 1 {
		t.Fatalf("hot cache entries = %d, want cached segment before relock", entries)
	}
	if err := handler.store.SetSessionRevealed(context.Background(), "session-revealed", false); err != nil {
		t.Fatalf("SetSessionRevealed: %v", err)
	}

	rec = httptest.NewRecorder()
	handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00000.m4s", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 after relock despite hot cache: %s", rec.Code, rec.Body.String())
	}
	first.release()
}

// TestHLSPrecomputedRenditionServesByteRange proves a disk-served (cold, no hot
// cache) precomputed segment honours an HTTP Range request: 206 status, the
// exact requested byte slice, and a Content-Range header naming the slice over
// the full segment size. This is the playlist/segment/range trio's range leg on
// the cold path — the only other 206 coverage runs through the hot-RAM cache.
func TestHLSPrecomputedRenditionServesByteRange(t *testing.T) {
	handler, token, _ := newPrecomputedHLSTestHandler(t, "session-default", "media-alpha", false)
	// "cached segment" is 14 bytes; request the slice bytes[7..11] => "segme".
	const full = "cached segment"

	req := httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00000.m4s", nil)
	req.Header.Set("Range", "bytes=7-11")
	rec := httptest.NewRecorder()
	handler.HLS(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want 206: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "segme" {
		t.Fatalf("range body = %q, want %q", rec.Body.String(), "segme")
	}
	wantCR := fmt.Sprintf("bytes 7-11/%d", len(full))
	if got := rec.Header().Get("Content-Range"); got != wantCR {
		t.Fatalf("Content-Range = %q, want %q", got, wantCR)
	}
	assertTokenByteCacheControl(t, rec)
	if got := rec.Header().Get("Content-Type"); got != "video/mp4" {
		t.Fatalf("Content-Type = %q, want video/mp4", got)
	}

	// A whole-file fetch of the same segment still returns the full bytes (200).
	whole := httptest.NewRecorder()
	handler.HLS(whole, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00000.m4s", nil))
	if whole.Code != http.StatusOK {
		t.Fatalf("whole-file status = %d, want 200", whole.Code)
	}
	if whole.Body.String() != full {
		t.Fatalf("whole-file body = %q, want %q", whole.Body.String(), full)
	}
}

func TestHLSPrecomputedEncryptedLibraryDecryptsDerivativeBytes(t *testing.T) {
	handler, token, _ := newPrecomputedHLSTestHandler(t, "session-default", "media-alpha", false)
	if err := handler.store.SetLibraryBacking(context.Background(), "library-local", false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	decryptor := &fakeEncryptedByteDecryptor{body: []byte("decrypted segment")}
	handler.SetEncryptedBytes(decryptor)
	req := httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00000.m4s", nil)
	req.Header.Set("Range", "bytes=10-16")
	rec := httptest.NewRecorder()

	handler.HLS(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "segment" {
		t.Fatalf("body = %q, want decrypted range", rec.Body.String())
	}
	if decryptor.calls != 1 || decryptor.libraryID != "library-local" || !strings.HasSuffix(decryptor.path, "seg00000.m4s") {
		t.Fatalf("decryptor call = calls:%d library:%q path:%q", decryptor.calls, decryptor.libraryID, decryptor.path)
	}
}

// TestHLSPrecomputedRenditionGatesSegmentAndRange proves Stream Token Verify and
// the Reveal contract run BEFORE any segment or range bytes are served — not
// only before the playlist. A wrong-target token and a relocked restricted
// session must both be rejected on the segment route and the range route, with
// no body leaked.
func TestHLSPrecomputedRenditionGatesSegmentAndRange(t *testing.T) {
	t.Run("wrong-target token on segment + range", func(t *testing.T) {
		handler, token, _ := newPrecomputedHLSTestHandler(t, "session-default", "media-alpha", false)
		grant, err := handler.tokens.UnverifiedGrant(token, streamtoken.TargetMediaHLS)
		if err != nil {
			t.Fatal(err)
		}
		grant.Target = streamtoken.TargetMediaDirect
		directToken, err := handler.tokens.Issue(context.Background(), grant)
		if err != nil {
			t.Fatal(err)
		}

		seg := httptest.NewRecorder()
		handler.HLS(seg, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+directToken+"/720p/seg00000.m4s", nil))
		if seg.Code != http.StatusUnauthorized {
			t.Fatalf("segment status = %d, want 401 for wrong-target token", seg.Code)
		}
		if strings.Contains(seg.Body.String(), "cached segment") {
			t.Fatalf("segment body leaked bytes under wrong token: %q", seg.Body.String())
		}

		rng := httptest.NewRequest(http.MethodGet, "/api/media/hls/"+directToken+"/720p/seg00000.m4s", nil)
		rng.Header.Set("Range", "bytes=0-4")
		rngRec := httptest.NewRecorder()
		handler.HLS(rngRec, rng)
		if rngRec.Code != http.StatusUnauthorized {
			t.Fatalf("range status = %d, want 401 for wrong-target token", rngRec.Code)
		}
		if rngRec.Code == http.StatusPartialContent || strings.Contains(rngRec.Body.String(), "cache") {
			t.Fatalf("range leaked bytes under wrong token: %q", rngRec.Body.String())
		}
	})

	t.Run("relocked restricted session on segment + range", func(t *testing.T) {
		handler, token, _ := newPrecomputedHLSTestHandler(t, "session-revealed", "media-archive", true)
		if err := handler.store.SetSessionRevealed(context.Background(), "session-revealed", false); err != nil {
			t.Fatalf("SetSessionRevealed: %v", err)
		}

		seg := httptest.NewRecorder()
		handler.HLS(seg, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00000.m4s", nil))
		if seg.Code != http.StatusUnauthorized {
			t.Fatalf("segment status = %d, want 401 after relock", seg.Code)
		}
		if strings.Contains(seg.Body.String(), "cached segment") {
			t.Fatalf("segment body leaked bytes after relock: %q", seg.Body.String())
		}

		rng := httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00000.m4s", nil)
		rng.Header.Set("Range", "bytes=0-4")
		rngRec := httptest.NewRecorder()
		handler.HLS(rngRec, rng)
		if rngRec.Code != http.StatusUnauthorized {
			t.Fatalf("range status = %d, want 401 after relock", rngRec.Code)
		}
	})
}

// TestHLSPrecomputedHotSegmentCacheServesFromRAM proves the active-fan-out RAM
// cache actually serves a hot segment's BYTES from memory rather than re-reading
// the segment body off disk. A first request is held open so the entry stays
// resident; a second fetch populates the cache. We then OVERWRITE the on-disk
// segment with poison bytes and fire concurrent requests for the same segment:
// every one returns the original cached body (a disk re-read of the body would
// return the poison). The byte budget bound and reveal-on-hot-hit are proven by
// the sibling Bounded/StillRequiresReveal tests.
func TestHLSPrecomputedHotSegmentCacheServesFromRAM(t *testing.T) {
	handler, token, cacheDir := newPrecomputedHLSTestHandler(t, "session-default", "media-alpha", false)
	handler.SetHotSegmentCache(1024)
	segPath := filepath.Join(cacheDir, "720p", "seg00000.m4s")

	// Hold one fan-out fetch open so the segment is resident in RAM.
	first := startBlockedHLSFetch(t, handler, token, "720p/seg00000.m4s")
	defer first.release()

	// A second concurrent fetch populates the cache (read once from disk) and
	// serves; afterwards the entry is resident.
	populate := httptest.NewRecorder()
	handler.HLS(populate, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00000.m4s", nil))
	if populate.Code != http.StatusOK || populate.Body.String() != "cached segment" {
		t.Fatalf("populate status/body = %d/%q, want 200/cached segment", populate.Code, populate.Body.String())
	}
	assertTokenByteCacheControl(t, populate)
	if entries, n := handler.hotSegments.stats(); entries != 1 || n == 0 {
		t.Fatalf("hot cache stats after populate = %d/%d, want one resident segment", entries, n)
	}

	// Poison the disk body but keep the file present (the serve path stats it for
	// modTime). Any body re-read from disk would now return the poison; only a
	// RAM serve returns the original "cached segment".
	if err := os.WriteFile(segPath, []byte("POISON-DISK-RE"), 0o600); err != nil {
		t.Fatalf("poison disk segment: %v", err)
	}

	// Fire concurrent hits while the entry is still resident; all must return the
	// original cached bytes from memory, proving the body was not re-read.
	const fanout = 8
	var wg sync.WaitGroup
	results := make([]string, fanout)
	codes := make([]int, fanout)
	for i := 0; i < fanout; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00000.m4s", nil))
			results[i] = rec.Body.String()
			codes[i] = rec.Code
		}(i)
	}
	wg.Wait()
	for i := 0; i < fanout; i++ {
		if codes[i] != http.StatusOK {
			t.Fatalf("concurrent hit %d status = %d, want 200", i, codes[i])
		}
		if results[i] != "cached segment" {
			t.Fatalf("concurrent hit %d body = %q, want cached segment from RAM (not the poisoned disk bytes)", i, results[i])
		}
	}
}

type blockedHLSFetch struct {
	rec      *blockingResponseWriter
	done     chan struct{}
	released bool
}

func startBlockedHLSFetch(t *testing.T, handler *Handler, token, name string) *blockedHLSFetch {
	t.Helper()
	rec := newBlockingResponseWriter()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/"+name, nil))
	}()
	select {
	case <-rec.blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for blocked HLS fetch")
	}
	return &blockedHLSFetch{rec: rec, done: done}
}

func (f *blockedHLSFetch) release() {
	if f.released {
		return
	}
	f.released = true
	close(f.rec.release)
	<-f.done
}

type blockingResponseWriter struct {
	header  http.Header
	blocked chan struct{}
	release chan struct{}
	body    bytes.Buffer
	code    int
	once    sync.Once
}

func newBlockingResponseWriter() *blockingResponseWriter {
	return &blockingResponseWriter{
		header:  make(http.Header),
		blocked: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (w *blockingResponseWriter) Header() http.Header {
	return w.header
}

func (w *blockingResponseWriter) WriteHeader(status int) {
	w.code = status
}

func (w *blockingResponseWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.blocked) })
	<-w.release
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.body.Write(data)
}

func newPrecomputedHLSTestHandler(t *testing.T, sessionID store.SessionID, mediaID store.MediaID, restricted bool) (*Handler, string, string) {
	t.Helper()
	st := storetest.Open(t)
	src := filepath.Join(t.TempDir(), "source.bin")
	if err := os.WriteFile(src, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(context.Background(), mediaID, src); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(t.TempDir(), "precomputed")
	if err := os.MkdirAll(filepath.Join(cacheDir, "720p"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "master.m3u8"), []byte("#EXTM3U\n# cached master\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "720p", "seg00000.m4s"), []byte("cached segment"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPlaybackRendition(context.Background(), store.PlaybackRendition{
		ID:                 "cached-hls",
		MediaID:            mediaID,
		ContentFingerprint: "sha256:cached-hls",
		Kind:               store.PlaybackRenditionKindHLS,
		Status:             store.PlaybackRenditionReady,
		RungID:             "720p",
		Codec:              "h264",
		Height:             720,
		Bandwidth:          4_256_000,
		Path:               cacheDir,
		SizeBytes:          128,
		CreatedAt:          time.Unix(100, 0).UTC(),
		LastUsedAt:         time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("UpsertPlaybackRendition: %v", err)
	}
	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	token, err := tokens.Issue(context.Background(), streamtoken.Grant{
		Target:              streamtoken.TargetMediaHLS,
		MediaID:             mediaID,
		DeviceID:            scope.DeviceID,
		SessionID:           scope.SessionID,
		ProfileID:           scope.ProfileID,
		Restricted:          restricted,
		PlaybackRenditionID: "cached-hls",
		ExpiresAt:           now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(fakeHLSTranscoder{}, t.TempDir(), nil))
	return handler, token, cacheDir
}

// TestHLSMasterFromGrantVerdictNotRungName is the PLAY-3b-residual regression:
// the audio-only-transcode verdict rides the signed grant, so a
// pure-HLS player that fetches the shared master.m3u8 BEFORE any audio-copy/*
// artifact still gets the single audio-copy rung — not the 1080/720/480 ladder
// it would have seen when the byte layer derived audioCopy from the requested
// rung name alone. A standard grant still seeds the standard ladder.
func TestHLSMasterFromGrantVerdictNotRungName(t *testing.T) {
	// feature:hls-transcode
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

	grant := func(audioCopy bool) string {
		token, err := tokens.Issue(context.Background(), streamtoken.Grant{
			Target:    streamtoken.TargetMediaHLS,
			MediaID:   "media-alpha",
			DeviceID:  scope.DeviceID,
			SessionID: scope.SessionID,
			ProfileID: scope.ProfileID,
			AudioCopy: audioCopy,
			ExpiresAt: now.Add(time.Hour),
		})
		if err != nil {
			t.Fatalf("Issue(audioCopy=%v): %v", audioCopy, err)
		}
		return token
	}

	// An audio-copy grant whose master.m3u8 is fetched first (no audio-copy/*
	// artifact requested yet) must advertise the single audio-copy rung, never
	// the resolution ladder. A fresh Sessions per case isolates the seeded rungs.
	sessions := playback.NewSessions(fakeHLSTranscoder{}, t.TempDir(), nil)
	defer sessions.Close()
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), sessions)

	rec := httptest.NewRecorder()
	handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+grant(true)+"/master.m3u8", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("audio-copy master status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	master := rec.Body.String()
	if !strings.Contains(master, "audio-copy/index.m3u8") {
		t.Fatalf("audio-copy master = %q, want the audio-copy rung", master)
	}
	for _, ladder := range []string{"1080p", "720p", "480p"} {
		if strings.Contains(master, ladder) {
			t.Fatalf("audio-copy master = %q, must not advertise %s ladder rung", master, ladder)
		}
	}

	// A standard grant seeds the standard transcode ladder for the same source.
	stdSessions := playback.NewSessions(fakeHLSTranscoder{}, t.TempDir(), nil)
	defer stdSessions.Close()
	stdHandler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), stdSessions)

	rec = httptest.NewRecorder()
	stdHandler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+grant(false)+"/master.m3u8", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("standard master status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	stdMaster := rec.Body.String()
	if strings.Contains(stdMaster, "audio-copy/index.m3u8") {
		t.Fatalf("standard master = %q, must not advertise the audio-copy rung", stdMaster)
	}
	if !strings.Contains(stdMaster, "p/index.m3u8") {
		t.Fatalf("standard master = %q, want the resolution ladder", stdMaster)
	}
}

// TestHLSSessionIsRecreatedForIncompatibleModeShaping asserts the same
// (session, media) pair can not reuse a transcode session when the mode-shaping
// inputs differ. Reusing would bind the old master/rung plan to a newer request
// and break audio-copy or capacity-degraded playback.
func TestHLSSessionRecreatedForModeShapingChange(t *testing.T) {
	// feature:hls-transcode
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

	issue := func(audioCopy, degraded bool) string {
		token, err := tokens.Issue(context.Background(), streamtoken.Grant{
			Target:    streamtoken.TargetMediaHLS,
			MediaID:   "media-alpha",
			DeviceID:  scope.DeviceID,
			SessionID: scope.SessionID,
			ProfileID: scope.ProfileID,
			AudioCopy: audioCopy,
			Degraded:  degraded,
			ExpiresAt: now.Add(time.Hour),
		})
		if err != nil {
			t.Fatalf("Issue(audioCopy=%v, degraded=%v): %v", audioCopy, degraded, err)
		}
		return token
	}

	sessions := playback.NewSessions(fakeHLSTranscoder{}, t.TempDir(), nil)
	defer sessions.Close()
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), sessions)
	fetchSegment := func(token, path string) int {
		rec := httptest.NewRecorder()
		handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+path, nil))
		return rec.Code
	}

	// Standard then audio-copy: same media and scope should recreate under
	// incompatible rung shape; the audio-copy segment must serve 200.
	if code := fetchSegment(issue(false, false), "/720p/seg00000.m4s"); code != http.StatusOK {
		t.Fatalf("standard seg status = %d, want 200", code)
	}
	if code := fetchSegment(issue(true, false), "/audio-copy/seg00000.m4s"); code != http.StatusOK {
		t.Fatalf("audio-copy seg status = %d, want 200", code)
	}
	if len(sessions.ActiveKeys()) != 1 {
		t.Fatalf("ActiveKeys = %d, want 1 (recreated standard→audio-copy session)", len(sessions.ActiveKeys()))
	}

	// Degraded mode must also recreate and refuse an inherited standard segment.
	degradedSessions := playback.NewSessions(fakeHLSTranscoder{}, t.TempDir(), nil)
	defer degradedSessions.Close()
	degradedHandler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), degradedSessions)
	degradedFetch := func(token, path string) int {
		rec := httptest.NewRecorder()
		degradedHandler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+path, nil))
		return rec.Code
	}
	if code := degradedFetch(issue(false, false), "/720p/seg00000.m4s"); code != http.StatusOK {
		t.Fatalf("standard seg status = %d, want 200", code)
	}
	if code := degradedFetch(issue(false, true), "/720p/seg00000.m4s"); code != http.StatusServiceUnavailable {
		t.Fatalf("degraded wrong-rung seg status = %d, want %d", code, http.StatusServiceUnavailable)
	}
	if code := degradedFetch(issue(false, true), "/480p/seg00000.m4s"); code != http.StatusOK {
		t.Fatalf("degraded seg status = %d, want 200", code)
	}
	if len(degradedSessions.ActiveKeys()) != 1 {
		t.Fatalf("ActiveKeys = %d, want 1 (recreated standard→degraded session)", len(degradedSessions.ActiveKeys()))
	}
}

// TestHLSForegroundGrantControlsPreemption is the production wire-up for
// priority preemption: only the server-signed grant may raise a
// stream to PriorityForeground. The request header is advisory and cannot grant
// cross-Profile eviction authority.
func TestHLSForegroundGrantControlsPreemption(t *testing.T) {
	st := storetest.Open(t)
	src := filepath.Join(t.TempDir(), "movie.bin")
	if err := os.WriteFile(src, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(context.Background(), "media-alpha", src); err != nil {
		t.Fatal(err)
	}
	challengerProfile, err := st.CreateProfile(context.Background(), "Foreground challenger", store.ProfileAdult, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if err := st.SetSessionProfile(context.Background(), "session-manager", challengerProfile.ID); err != nil {
		t.Fatalf("SetSessionProfile: %v", err)
	}
	authService := auth.NewService(st)
	incumbentScope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatalf("Scope(session-default): %v", err)
	}
	challengerScope, err := authService.Scope(context.Background(), "session-manager")
	if err != nil {
		t.Fatalf("Scope(session-manager): %v", err)
	}
	if incumbentScope.ProfileID == challengerScope.ProfileID {
		t.Fatalf("preemption fixture Profiles match: %q", incumbentScope.ProfileID)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })

	// One transcode slot, so the second start must preempt to be admitted.
	sessions := playback.NewSessions(fakeHLSTranscoder{}, t.TempDir(), nil)
	sessions.SetTranscodeCap(1)
	defer sessions.Close()
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), sessions)

	// hlsToken issues an HLS grant for a seeded session on media-alpha.
	hlsToken := func(sessionID store.SessionID, foreground bool) string {
		scope, err := authService.Scope(context.Background(), sessionID)
		if err != nil {
			t.Fatalf("Scope(%s): %v", sessionID, err)
		}
		token, err := tokens.Issue(context.Background(), streamtoken.Grant{
			Target:     streamtoken.TargetMediaHLS,
			MediaID:    "media-alpha",
			DeviceID:   scope.DeviceID,
			SessionID:  scope.SessionID,
			ProfileID:  scope.ProfileID,
			Foreground: foreground,
			ExpiresAt:  now.Add(time.Hour),
		})
		if err != nil {
			t.Fatalf("Issue(%s): %v", sessionID, err)
		}
		return token
	}

	// fetchSegment drives a real segment request through the HLS handler, which
	// starts (admits) the transcode. hint is the advisory priority header.
	fetchSegment := func(token, hint string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00000.m4s", nil)
		if hint != "" {
			req.Header.Set(ForegroundHeader, hint)
		}
		rec := httptest.NewRecorder()
		handler.HLS(rec, req)
		return rec.Code
	}

	// A normal stream claims the only slot (its transcode process starts live).
	if code := fetchSegment(hlsToken("session-default", false), ""); code != http.StatusOK {
		t.Fatalf("normal stream start: status = %d, want 200", code)
	}

	// A second normal stream is refused: the slot is held by an active
	// equal-rank peer, which the idle-only rule never preempts.
	unsignedForeground := hlsToken("session-manager", false)
	if code := fetchSegment(unsignedForeground, ""); code == http.StatusOK {
		t.Fatal("second normal stream was admitted at cap 1 against an active equal-rank peer; want refusal")
	}

	// An unsigned client header cannot raise the grant's priority and therefore
	// cannot evict another Profile's active stream.
	if code := fetchSegment(unsignedForeground, "1"); code == http.StatusOK {
		t.Fatal("unsigned foreground header preempted another Profile's active stream")
	}

	// An explicit background hint can lower signed foreground priority.
	if code := fetchSegment(hlsToken("session-manager", true), "false"); code == http.StatusOK {
		t.Fatal("background hint failed to lower signed foreground priority")
	}

	// The signed server verdict reaches the byte layer and may preempt the lower-
	// priority active stream when the client does not lower it.
	if code := fetchSegment(hlsToken("session-manager", true), ""); code != http.StatusOK {
		t.Fatalf("signed foreground stream preemption: status = %d, want 200", code)
	}
}

// fakeArtworkScaler satisfies artwork's FrameGrabber + ImageScaler seams, like
// the real *ffmpeg.Runner, so handler tests can exercise bucket derivatives.
type fakeArtworkScaler struct {
	scaleErr error
}

func (fakeArtworkScaler) FrameGrab(_ context.Context, _ string, _ float64, _ int, _ string) error {
	return nil
}

func (f fakeArtworkScaler) ScaleImage(_ context.Context, _ string, _ int, dst string) error {
	if f.scaleErr != nil {
		return f.scaleErr
	}
	return os.WriteFile(dst, []byte("scaled jpeg bytes"), 0o600)
}

func newArtworkScalerTestHandler(t *testing.T, scaleErr error) (*Handler, *artwork.Service) {
	t.Helper()
	st := storetest.Open(t)
	authService := auth.NewService(st)
	tokens := streamtoken.NewService([]byte("test-token-key"), time.Now)
	artworkService := artwork.NewService(st, fakeArtworkScaler{scaleErr: scaleErr}, t.TempDir())
	return NewHandler(st, authService, tokens, artworkService, playback.NewSessions(nil, t.TempDir(), nil)), artworkService
}

func TestArtworkServesBucketedDerivative(t *testing.T) {
	handler, svc := newArtworkScalerTestHandler(t, nil)
	writeTestPoster(t, svc, "media-alpha")
	rec := httptest.NewRecorder()

	handler.Artwork(rec, artworkRequest("media-alpha", "poster?bucket=w320"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("Content-Type = %q, want image/jpeg", got)
	}
	if rec.Body.String() != "scaled jpeg bytes" {
		t.Fatalf("body = %q, want derivative bytes", rec.Body.String())
	}

	// The derivative is cached next to the full-size file and re-served.
	rec = httptest.NewRecorder()
	handler.Artwork(rec, artworkRequest("media-alpha", "poster?bucket=w320"))
	if rec.Code != http.StatusOK || rec.Body.String() != "scaled jpeg bytes" {
		t.Fatalf("cached serve = %d %q, want 200 with derivative bytes", rec.Code, rec.Body.String())
	}
}

func TestArtworkBucketFallsBackToFullSizeOnScaleFailure(t *testing.T) {
	handler, svc := newArtworkScalerTestHandler(t, fmt.Errorf("boom"))
	writeTestPoster(t, svc, "media-alpha")
	rec := httptest.NewRecorder()

	handler.Artwork(rec, artworkRequest("media-alpha", "poster?bucket=w320"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 fallback: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "jpeg bytes" {
		t.Fatalf("body = %q, want full-size poster bytes", rec.Body.String())
	}
}

func TestArtworkRejectsSealedLibrary(t *testing.T) {
	handler, svc := newArtworkScalerTestHandler(t, nil)
	writeTestPoster(t, svc, "media-alpha")
	handler.SetSealed(func(libraryID store.LibraryID) bool { return libraryID == "library-local" })
	rec := httptest.NewRecorder()

	handler.Artwork(rec, artworkRequest("media-alpha", "poster?bucket=w320"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for sealed Library artwork", rec.Code)
	}
}

func TestArtworkEncryptedLibraryDecryptsDerivativeBytes(t *testing.T) {
	handler, svc := newArtworkScalerTestHandler(t, nil)
	writeTestPoster(t, svc, "media-alpha")
	if err := handler.store.SetLibraryBacking(context.Background(), "library-local", false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	decryptor := &fakeEncryptedByteDecryptor{body: []byte("decrypted artwork")}
	handler.SetEncryptedBytes(decryptor)
	rec := httptest.NewRecorder()

	handler.Artwork(rec, artworkRequest("media-alpha", "poster?bucket=w320"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "decrypted artwork" {
		t.Fatalf("body = %q, want decrypted artwork", rec.Body.String())
	}
	if decryptor.calls != 1 || decryptor.libraryID != "library-local" {
		t.Fatalf("decryptor call = calls:%d library:%q path:%q", decryptor.calls, decryptor.libraryID, decryptor.path)
	}
}

func TestArtworkRejectsUnknownBucket(t *testing.T) {
	handler, svc := newArtworkScalerTestHandler(t, nil)
	writeTestPoster(t, svc, "media-alpha")
	rec := httptest.NewRecorder()

	handler.Artwork(rec, artworkRequest("media-alpha", "poster?bucket=w999"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unknown bucket", rec.Code)
	}
}

func TestArtworkBucketKeepsRevealGating(t *testing.T) {
	handler, svc := newArtworkScalerTestHandler(t, nil)
	writeTestPoster(t, svc, "media-archive")

	// session-default is not revealed; the bucket variant must stay gated.
	rec := httptest.NewRecorder()
	handler.Artwork(rec, artworkRequest("media-archive", "poster?bucket=w320"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unrevealed restricted derivative", rec.Code)
	}

	// And it must stay credential-gated.
	req := httptest.NewRequest(http.MethodGet, "/api/media/artwork/media-alpha/poster?bucket=w320", nil)
	rec = httptest.NewRecorder()
	handler.Artwork(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without credentials", rec.Code)
	}
}

func writeTestEntityPoster(t *testing.T, svc *artwork.Service, entityID store.EntityID) {
	t.Helper()
	path := svc.EntityPosterPath(entityID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("entity jpeg bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func upsertTestEntity(t *testing.T, handler *Handler, library store.LibraryID, id store.EntityID) {
	t.Helper()
	if err := handler.store.UpsertEntity(context.Background(), store.Entity{
		ID:        id,
		LibraryID: library,
		Kind:      "maker",
		Title:     "Sample Maker",
	}); err != nil {
		t.Fatal(err)
	}
}

func entityArtworkRequest(entityID, slot string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/entity/artwork/"+entityID+"/"+slot, nil)
	req.Header.Set("X-Beamfall-Session", "session-default")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	return req
}

func TestEntityArtworkServesPosterForVisibleEntity(t *testing.T) {
	handler, svc := newArtworkTestHandler(t)
	upsertTestEntity(t, handler, "library-local", "entity-maker")
	writeTestEntityPoster(t, svc, "entity-maker")
	rec := httptest.NewRecorder()

	handler.EntityArtwork(rec, entityArtworkRequest("entity-maker", "poster"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("Content-Type = %q, want image/jpeg", got)
	}
	if rec.Body.String() != "entity jpeg bytes" {
		t.Fatalf("body = %q, want poster bytes", rec.Body.String())
	}
}

func TestEntityArtworkRejectsSealedLibrary(t *testing.T) {
	handler, svc := newArtworkTestHandler(t)
	upsertTestEntity(t, handler, "library-local", "entity-maker")
	writeTestEntityPoster(t, svc, "entity-maker")
	handler.SetSealed(func(libraryID store.LibraryID) bool { return libraryID == "library-local" })
	rec := httptest.NewRecorder()

	handler.EntityArtwork(rec, entityArtworkRequest("entity-maker", "poster"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for sealed entity artwork", rec.Code)
	}
}

func TestEntityArtworkHidesRestrictedEntityWithoutReveal(t *testing.T) {
	handler, svc := newArtworkTestHandler(t)
	// session-default is not revealed; library-archive is the restricted Library.
	upsertTestEntity(t, handler, "library-archive", "entity-hidden")
	writeTestEntityPoster(t, svc, "entity-hidden")

	hidden := httptest.NewRecorder()
	handler.EntityArtwork(hidden, entityArtworkRequest("entity-hidden", "poster"))
	missing := httptest.NewRecorder()
	handler.EntityArtwork(missing, entityArtworkRequest("entity-nonexistent", "poster"))

	if hidden.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unrevealed restricted entity artwork", hidden.Code)
	}
	// Reveal contract: a restricted entity's 404 must be observably identical
	// to a nonexistent one's.
	if hidden.Code != missing.Code || hidden.Body.String() != missing.Body.String() {
		t.Fatalf("restricted (%d %q) and missing (%d %q) artwork responses differ",
			hidden.Code, hidden.Body.String(), missing.Code, missing.Body.String())
	}
}

func TestEntityArtworkRequiresCredentials(t *testing.T) {
	handler, svc := newArtworkTestHandler(t)
	upsertTestEntity(t, handler, "library-local", "entity-maker")
	writeTestEntityPoster(t, svc, "entity-maker")
	req := httptest.NewRequest(http.MethodGet, "/api/entity/artwork/entity-maker/poster", nil)
	rec := httptest.NewRecorder()

	handler.EntityArtwork(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without credentials", rec.Code)
	}
}

func TestEntityArtworkRejectsUnknownSlotAndBucket(t *testing.T) {
	handler, svc := newArtworkTestHandler(t)
	upsertTestEntity(t, handler, "library-local", "entity-maker")
	writeTestEntityPoster(t, svc, "entity-maker")

	slot := httptest.NewRecorder()
	handler.EntityArtwork(slot, entityArtworkRequest("entity-maker", "banner"))
	bucket := httptest.NewRecorder()
	handler.EntityArtwork(bucket, entityArtworkRequest("entity-maker", "poster?bucket=w999"))

	if slot.Code != http.StatusNotFound {
		t.Fatalf("unknown slot status = %d, want 404", slot.Code)
	}
	if bucket.Code != http.StatusNotFound {
		t.Fatalf("unknown bucket status = %d, want 404", bucket.Code)
	}
}

// newAssetsTestHandler seeds a file-backed media item, wires a Generated
// Assets service, and returns the handler plus an assets-target token and the
// file's fingerprint.
func newAssetsTestHandler(t *testing.T, sessionID store.SessionID, mediaID store.MediaID, restricted bool) (*Handler, *genassets.Service, string, string) {
	t.Helper()
	st := storetest.Open(t)
	path := filepath.Join(t.TempDir(), "media.bin")
	if err := os.WriteFile(path, []byte("beamfall bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(context.Background(), mediaID, path); err != nil {
		t.Fatal(err)
	}
	file, err := st.PrimaryMediaFile(context.Background(), mediaID, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	token, err := tokens.Issue(context.Background(), streamtoken.Grant{
		Target:     streamtoken.TargetMediaAssets,
		MediaID:    mediaID,
		DeviceID:   scope.DeviceID,
		SessionID:  scope.SessionID,
		ProfileID:  scope.ProfileID,
		Restricted: restricted,
		ExpiresAt:  now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	assets := genassets.NewService(st, nil, t.TempDir())
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil))
	handler.SetAssets(assets)
	return handler, assets, token, file.ContentFingerprint
}

func writeAssetFile(t *testing.T, assets *genassets.Service, fingerprint, name string, body []byte) {
	t.Helper()
	path := filepath.Join(filepath.Dir(assets.ManifestPath(fingerprint)), name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func countMediaResolves(handler *Handler) *int {
	resolve := handler.mediaResolver
	calls := new(int)
	handler.mediaResolver = func(ctx context.Context, mediaID store.MediaID, filter store.LibraryFilter) (store.Media, error) {
		*calls++
		return resolve(ctx, mediaID, filter)
	}
	return calls
}

func TestAssetsServesTokenGatedBytes(t *testing.T) {
	handler, assets, token, fingerprint := newAssetsTestHandler(t, "session-default", "media-alpha", false)
	resolveCalls := countMediaResolves(handler)
	writeAssetFile(t, assets, fingerprint, genassets.ManifestName, []byte(`{"version":1}`))
	writeAssetFile(t, assets, fingerprint, "sheet-0001.jpg", []byte("sheet bytes"))
	writeAssetFile(t, assets, fingerprint, genassets.PreviewClipName, []byte("clip bytes"))

	cases := []struct{ name, contentType, body string }{
		{genassets.ManifestName, "application/json", `{"version":1}`},
		{"sheet-0001.jpg", "image/jpeg", "sheet bytes"},
		{genassets.PreviewClipName, "video/mp4", "clip bytes"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		handler.Assets(rec, httptest.NewRequest(http.MethodGet, "/api/media/assets/"+token+"/"+c.name, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200: %s", c.name, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != c.contentType {
			t.Fatalf("%s: Content-Type = %q, want %q", c.name, got, c.contentType)
		}
		if rec.Body.String() != c.body {
			t.Fatalf("%s: body = %q, want asset bytes", c.name, rec.Body.String())
		}
		assertTokenByteCacheControl(t, rec)
	}
	if *resolveCalls != len(cases) {
		t.Fatalf("Media resolve calls = %d, want exactly one per request", *resolveCalls)
	}
}

func TestAssetsServeContentErrorIsPrivateAndUnstorable(t *testing.T) {
	handler, assets, token, fingerprint := newAssetsTestHandler(t, "session-default", "media-alpha", false)
	writeAssetFile(t, assets, fingerprint, genassets.ManifestName, []byte(`{"version":1}`))
	req := httptest.NewRequest(http.MethodGet, "/api/media/assets/"+token+"/"+genassets.ManifestName, nil)
	req.Header.Set("Range", "bytes=invalid")
	rec := httptest.NewRecorder()

	handler.Assets(rec, req)

	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusRequestedRangeNotSatisfiable, rec.Body.String())
	}
	assertTokenByteCacheControl(t, rec)
}

func TestAssetsRejectsSealedLibrary(t *testing.T) {
	handler, assets, token, fingerprint := newAssetsTestHandler(t, "session-default", "media-alpha", false)
	resolveCalls := countMediaResolves(handler)
	writeAssetFile(t, assets, fingerprint, genassets.ManifestName, []byte(`{"version":1}`))
	handler.SetSealed(func(libraryID store.LibraryID) bool { return libraryID == "library-local" })
	rec := httptest.NewRecorder()

	handler.Assets(rec, httptest.NewRequest(http.MethodGet, "/api/media/assets/"+token+"/"+genassets.ManifestName, nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for sealed Library asset", rec.Code)
	}
	if *resolveCalls != 1 {
		t.Fatalf("Media resolve calls = %d, want 1", *resolveCalls)
	}
}

func TestAssetsRejectsUnresolvableMedia(t *testing.T) {
	handler, assets, token, fingerprint := newAssetsTestHandler(t, "session-default", "media-alpha", false)
	writeAssetFile(t, assets, fingerprint, genassets.ManifestName, []byte(`{"version":1}`))
	resolveCalls := 0
	handler.mediaResolver = func(context.Context, store.MediaID, store.LibraryFilter) (store.Media, error) {
		resolveCalls++
		return store.Media{}, errors.New("hidden Media")
	}
	rec := httptest.NewRecorder()

	handler.Assets(rec, httptest.NewRequest(http.MethodGet, "/api/media/assets/"+token+"/"+genassets.ManifestName, nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unresolvable Media asset", rec.Code)
	}
	if resolveCalls != 1 {
		t.Fatalf("Media resolve calls = %d, want 1", resolveCalls)
	}
}

func TestAssetsEncryptedLibraryDecryptsDerivativeBytes(t *testing.T) {
	handler, assets, token, fingerprint := newAssetsTestHandler(t, "session-default", "media-alpha", false)
	writeAssetFile(t, assets, fingerprint, genassets.ManifestName, []byte("cipher manifest"))
	if err := handler.store.SetLibraryBacking(context.Background(), "library-local", false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	decryptor := &fakeEncryptedByteDecryptor{body: []byte(`{"version":1,"encrypted":true}`)}
	handler.SetEncryptedBytes(decryptor)
	rec := httptest.NewRecorder()

	handler.Assets(rec, httptest.NewRequest(http.MethodGet, "/api/media/assets/"+token+"/"+genassets.ManifestName, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != `{"version":1,"encrypted":true}` {
		t.Fatalf("body = %q, want decrypted asset", rec.Body.String())
	}
	if decryptor.calls != 1 || decryptor.libraryID != "library-local" || !strings.HasSuffix(decryptor.path, genassets.ManifestName) {
		t.Fatalf("decryptor call = calls:%d library:%q path:%q", decryptor.calls, decryptor.libraryID, decryptor.path)
	}
}

// TestAssetsBeatGridServedViaStreamToken: the beat grid generated asset is
// served under a Stream Token with application/json content-type, exactly like
// the waveform envelope — ADR-0109 §3 (Core serves beat grid via Stream Token).
func TestAssetsBeatGridServedViaStreamToken(t *testing.T) {
	handler, assets, token, fingerprint := newAssetsTestHandler(t, "session-default", "media-alpha", false)
	beatGridBody := `{"version":1,"bpm":120,"confidence":0.85,"beats":[0.0,0.5,1.0]}`
	writeAssetFile(t, assets, fingerprint, genassets.BeatGridName, []byte(beatGridBody))

	rec := httptest.NewRecorder()
	handler.Assets(rec, httptest.NewRequest(http.MethodGet, "/api/media/assets/"+token+"/"+genassets.BeatGridName, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if rec.Body.String() != beatGridBody {
		t.Fatalf("body = %q, want beat grid JSON", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.Assets(rec, httptest.NewRequest(http.MethodGet, "/api/media/assets/"+token+"x/"+genassets.BeatGridName, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered beat grid token status = %d, want 401", rec.Code)
	}

	authService := auth.NewService(handler.store)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	directToken, err := tokens.Issue(context.Background(), streamtoken.Grant{
		Target:    streamtoken.TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	handler.Assets(rec, httptest.NewRequest(http.MethodGet, "/api/media/assets/"+directToken+"/"+genassets.BeatGridName, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("direct token on beat grid route status = %d, want 401", rec.Code)
	}
}

func TestAssetsSyncLanesServedViaStreamToken(t *testing.T) {
	handler, assets, token, fingerprint := newAssetsTestHandler(t, "session-default", "media-alpha", false)
	body := `{"version":1,"meter":4,"confidence":0.82,"bars":[{"time":0.48,"confidence":0.8}],"phrases":[{"time":32.48,"bars":16,"confidence":0.7}]}`
	writeAssetFile(t, assets, fingerprint, genassets.SyncLanesName, []byte(body))

	rec := httptest.NewRecorder()
	handler.Assets(rec, httptest.NewRequest(http.MethodGet, "/api/media/assets/"+token+"/"+genassets.SyncLanesName, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if rec.Body.String() != body {
		t.Fatalf("body = %q, want sync lanes JSON", rec.Body.String())
	}
}

func TestAssetsRejectsBadTokens(t *testing.T) {
	handler, assets, token, fingerprint := newAssetsTestHandler(t, "session-default", "media-alpha", false)
	writeAssetFile(t, assets, fingerprint, genassets.ManifestName, []byte(`{"version":1}`))

	// Tampered token.
	rec := httptest.NewRecorder()
	handler.Assets(rec, httptest.NewRequest(http.MethodGet, "/api/media/assets/"+token+"x/"+genassets.ManifestName, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered token status = %d, want 401", rec.Code)
	}

	// A direct-play token must not unlock the assets surface.
	authService := auth.NewService(handler.store)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	directToken, err := tokens.Issue(context.Background(), streamtoken.Grant{
		Target:    streamtoken.TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	handler.Assets(rec, httptest.NewRequest(http.MethodGet, "/api/media/assets/"+directToken+"/"+genassets.ManifestName, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("direct token on assets route status = %d, want 401", rec.Code)
	}

	// Missing name segment.
	rec = httptest.NewRecorder()
	handler.Assets(rec, httptest.NewRequest(http.MethodGet, "/api/media/assets/"+token, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing name status = %d, want 401", rec.Code)
	}
}

func TestAssetsKeepsRevealGating(t *testing.T) {
	// media-archive lives in the restricted Library; session-default holds no
	// reveal, so its restricted token must fail verification — hidden media's
	// Generated Assets are not servable to non-revealed sessions.
	handler, assets, token, fingerprint := newAssetsTestHandler(t, "session-default", "media-archive", true)
	writeAssetFile(t, assets, fingerprint, genassets.ManifestName, []byte(`{"version":1}`))

	rec := httptest.NewRecorder()
	handler.Assets(rec, httptest.NewRequest(http.MethodGet, "/api/media/assets/"+token+"/"+genassets.ManifestName, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unrevealed restricted asset status = %d, want 401", rec.Code)
	}
}

func TestAssetsRejectsUnknownNames(t *testing.T) {
	handler, assets, token, fingerprint := newAssetsTestHandler(t, "session-default", "media-alpha", false)
	writeAssetFile(t, assets, fingerprint, genassets.ManifestName, []byte(`{"version":1}`))
	writeAssetFile(t, assets, fingerprint, "secret.txt", []byte("never served"))

	for _, name := range []string{"secret.txt", "..%2Ftrickplay.json", "sheet-9999.jpg"} {
		rec := httptest.NewRecorder()
		handler.Assets(rec, httptest.NewRequest(http.MethodGet, "/api/media/assets/"+token+"/"+name, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", name, rec.Code)
		}
	}
}
