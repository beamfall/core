package mediahttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// fakeSubtitleConverter stands in for *ffmpeg.Runner: it writes a fixed WebVTT
// body to dst and counts conversions so the cache can be asserted without an
// ffmpeg binary.
type fakeSubtitleConverter struct {
	body  string
	calls atomic.Int32
	err   error
}

func (f *fakeSubtitleConverter) ExtractSubtitleToWebVTT(_ context.Context, _ string, _ int, dst string) error {
	f.calls.Add(1)
	if f.err != nil {
		return f.err
	}
	return os.WriteFile(dst, []byte(f.body), 0o600)
}

// newSubtitleTestHandler seeds a file-backed media item, wires a fake subtitle
// converter under a temp cache dir, and returns the handler, the converter, and
// a subtitle-target token.
func newSubtitleTestHandler(t *testing.T, sessionID store.SessionID, mediaID store.MediaID, restricted bool, conv SubtitleConverter) (*Handler, string) {
	t.Helper()
	st := storetest.Open(t)
	path := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(path, []byte("mkv bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(context.Background(), mediaID, path); err != nil {
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
		Target:     streamtoken.TargetMediaSubtitle,
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
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil))
	handler.SetSubtitles(conv, t.TempDir())
	return handler, token
}

func TestSubtitlesServesTokenGatedWebVTT(t *testing.T) {
	conv := &fakeSubtitleConverter{body: "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nHello sidecar\n"}
	handler, token := newSubtitleTestHandler(t, "session-default", "media-alpha", false, conv)
	resolveCalls := countMediaResolves(handler)

	rec := httptest.NewRecorder()
	handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, "/api/media/subtitle/"+token+"/2.vtt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/vtt; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/vtt; charset=utf-8", got)
	}
	if rec.Body.String() != conv.body {
		t.Fatalf("body = %q, want the WebVTT sidecar", rec.Body.String())
	}
	assertTokenByteCacheControl(t, rec)

	// A second fetch (and a seek-driven re-fetch) hits the disk cache: the
	// converter runs exactly once for the (fingerprint, index) pair.
	rec2 := httptest.NewRecorder()
	handler.Subtitles(rec2, httptest.NewRequest(http.MethodGet, "/api/media/subtitle/"+token+"/2.vtt", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("second fetch status = %d, want 200", rec2.Code)
	}
	if got := conv.calls.Load(); got != 1 {
		t.Fatalf("converter calls = %d, want 1 (cached after first)", got)
	}
	if *resolveCalls != 2 {
		t.Fatalf("Media resolve calls = %d, want exactly one per request", *resolveCalls)
	}
}

func TestSubtitleSidecarPathSerializesCacheFill(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	conv := &blockingSubtitleConverter{started: started, release: release}
	handler := &Handler{subtitles: conv, subtitleDir: t.TempDir()}
	results := make(chan error, 2)

	go func() {
		_, err := handler.subtitleSidecarPath(context.Background(), "sha256:test", "movie.mkv", 2)
		results <- err
	}()
	<-started
	go func() {
		_, err := handler.subtitleSidecarPath(context.Background(), "sha256:test", "movie.mkv", 2)
		results <- err
	}()

	concurrentFill := false
	select {
	case <-started:
		concurrentFill = true
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if concurrentFill {
		t.Fatal("second conversion started before the first cache fill completed")
	}
	if got := conv.calls.Load(); got != 1 {
		t.Fatalf("converter calls = %d, want 1", got)
	}
}

func TestSubtitleSidecarPathUnlocksAfterConversionError(t *testing.T) {
	conv := &fakeSubtitleConverter{body: "WEBVTT\n", err: errors.New("conversion failed")}
	handler := &Handler{subtitles: conv, subtitleDir: t.TempDir()}
	if _, err := handler.subtitleSidecarPath(context.Background(), "sha256:test", "movie.mkv", 2); err == nil {
		t.Fatal("first conversion unexpectedly succeeded")
	}
	conv.err = nil
	if _, err := handler.subtitleSidecarPath(context.Background(), "sha256:test", "movie.mkv", 2); err != nil {
		t.Fatalf("retry after conversion error: %v", err)
	}
}

type blockingSubtitleConverter struct {
	started chan<- struct{}
	release <-chan struct{}
	calls   atomic.Int32
}

func (f *blockingSubtitleConverter) ExtractSubtitleToWebVTT(_ context.Context, _ string, _ int, dst string) error {
	f.calls.Add(1)
	f.started <- struct{}{}
	<-f.release
	return os.WriteFile(dst, []byte("WEBVTT\n"), 0o600)
}

func TestSubtitlesRejectsSealedLibrary(t *testing.T) {
	conv := &fakeSubtitleConverter{body: "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nHello\n"}
	handler, token := newSubtitleTestHandler(t, "session-default", "media-alpha", false, conv)
	resolveCalls := countMediaResolves(handler)
	handler.SetSealed(func(libraryID store.LibraryID) bool { return libraryID == "library-local" })
	rec := httptest.NewRecorder()

	handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, "/api/media/subtitle/"+token+"/2.vtt", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for sealed subtitle", rec.Code)
	}
	if conv.calls.Load() != 0 {
		t.Fatalf("converter calls = %d, want 0 for sealed Library", conv.calls.Load())
	}
	if *resolveCalls != 1 {
		t.Fatalf("Media resolve calls = %d, want 1", *resolveCalls)
	}
}

func TestSubtitlesRejectsUnresolvableMediaBeforeConversion(t *testing.T) {
	conv := &fakeSubtitleConverter{body: "WEBVTT\n"}
	handler, token := newSubtitleTestHandler(t, "session-default", "media-alpha", false, conv)
	resolveCalls := 0
	handler.mediaResolver = func(context.Context, store.MediaID, store.LibraryFilter) (store.Media, error) {
		resolveCalls++
		return store.Media{}, errors.New("hidden Media")
	}
	rec := httptest.NewRecorder()

	handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, "/api/media/subtitle/"+token+"/2.vtt", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unresolvable Media subtitle", rec.Code)
	}
	if resolveCalls != 1 {
		t.Fatalf("Media resolve calls = %d, want 1", resolveCalls)
	}
	if conv.calls.Load() != 0 {
		t.Fatalf("converter calls = %d, want 0 for unresolvable Media", conv.calls.Load())
	}
}

func TestSubtitlesRejectsEncryptedUnsealedLibrary(t *testing.T) {
	// ENCPRIV-10b: extracting a muxed subtitle caches a plaintext WebVTT sidecar —
	// a plaintext derivative of the encrypted Media. An Encrypted Library that is
	// currently Unsealed (no sealed predicate wired, so sealedMedia is false) must
	// still fail closed: the converter never runs and no sidecar is written.
	conv := &fakeSubtitleConverter{body: "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nHello\n"}
	handler, token := newSubtitleTestHandler(t, "session-default", "media-alpha", false, conv)
	if err := handler.store.SetLibraryBacking(context.Background(), "library-local", false, true); err != nil {
		t.Fatalf("SetLibraryBacking encrypted: %v", err)
	}
	rec := httptest.NewRecorder()

	handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, "/api/media/subtitle/"+token+"/2.vtt", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an Unsealed encrypted-Library subtitle", rec.Code)
	}
	if conv.calls.Load() != 0 {
		t.Fatalf("converter calls = %d, want 0 for an encrypted Library", conv.calls.Load())
	}
}

func TestSubtitlesRejectsBadTokens(t *testing.T) {
	conv := &fakeSubtitleConverter{body: "WEBVTT\n"}
	handler, token := newSubtitleTestHandler(t, "session-default", "media-alpha", false, conv)

	// Tampered token.
	rec := httptest.NewRecorder()
	handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, "/api/media/subtitle/"+token+"x/0.vtt", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered token status = %d, want 401", rec.Code)
	}

	// A direct-play token must not unlock the subtitle surface.
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
	handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, "/api/media/subtitle/"+directToken+"/0.vtt", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("direct token on subtitle route status = %d, want 401", rec.Code)
	}

	// Missing name segment.
	rec = httptest.NewRecorder()
	handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, "/api/media/subtitle/"+token, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing name status = %d, want 401", rec.Code)
	}
	if conv.calls.Load() != 0 {
		t.Fatalf("converter ran for a rejected request (calls=%d)", conv.calls.Load())
	}
}

func TestSubtitlesRejectsBadNames(t *testing.T) {
	conv := &fakeSubtitleConverter{body: "WEBVTT\n"}
	handler, token := newSubtitleTestHandler(t, "session-default", "media-alpha", false, conv)

	for _, name := range []string{"abc.vtt", "-1.vtt", "2.srt", "2", "..%2F0.vtt", ".vtt"} {
		rec := httptest.NewRecorder()
		handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, "/api/media/subtitle/"+token+"/"+name, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", name, rec.Code)
		}
	}
	if conv.calls.Load() != 0 {
		t.Fatalf("converter ran for a malformed name (calls=%d)", conv.calls.Load())
	}
}

func TestSubtitlesKeepsRevealGating(t *testing.T) {
	// media-archive lives in the restricted Library; session-default holds no
	// reveal, so its restricted subtitle token must fail verification.
	conv := &fakeSubtitleConverter{body: "WEBVTT\n"}
	handler, token := newSubtitleTestHandler(t, "session-default", "media-archive", true, conv)

	rec := httptest.NewRecorder()
	handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, "/api/media/subtitle/"+token+"/2.vtt", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unrevealed restricted subtitle status = %d, want 401", rec.Code)
	}
	if conv.calls.Load() != 0 {
		t.Fatalf("converter ran for a reveal-gated request (calls=%d)", conv.calls.Load())
	}
}

func TestSubtitlesUnconfiguredServesNothing(t *testing.T) {
	// Default handler (no SetSubtitles) serves no sidecars.
	handler, token := newSubtitleTestHandler(t, "session-default", "media-alpha", false, nil)
	handler.subtitles = nil
	handler.subtitleDir = ""

	rec := httptest.NewRecorder()
	handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, "/api/media/subtitle/"+token+"/2.vtt", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unconfigured subtitle route status = %d, want 404", rec.Code)
	}
}

// TestPurgeSubtitlesForFingerprintRemovesDerivedResidue guards ENCPRIV-10d: the
// purge must also catch the in-flight staging files subtitleSidecarPath and
// hardenExtractedWebVTT create alongside the published .vtt (a stranded one is
// an invariant-#5 at-rest violation per PurgeSubtitlesForFingerprint's doc
// comment), not just the final cached sidecar.
func TestPurgeSubtitlesForFingerprintRemovesDerivedResidue(t *testing.T) {
	dir := t.TempDir()
	handler := &Handler{subtitleDir: dir}
	stem := safeSubtitleFingerprint("sha256:deadbeef")

	files := []string{
		stem + ".2.vtt",
		stem + ".2.vtt.staged",
		stem + ".2.vtt.tmp",
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("WEBVTT\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := handler.PurgeSubtitlesForFingerprint("sha256:deadbeef"); err != nil {
		t.Fatalf("PurgeSubtitlesForFingerprint: %v", err)
	}

	for _, name := range files {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("residue survived purge: %s (stat err = %v)", name, err)
		}
	}
}
