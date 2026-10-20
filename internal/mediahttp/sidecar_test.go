package mediahttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// newSidecarTestHandler seeds a file-backed media item with one .srt sidecar on
// disk next to it and the matching sidecar row, and returns the handler and a
// sidecar-target token. dir is the media file's directory so a test can drop a
// traversal sidecar next to it.
func newSidecarTestHandler(t *testing.T, sessionID store.SessionID, mediaID store.MediaID, restricted bool, sidecars []store.MediaFileSidecarCaption, sidecarRawASS ...bool) (*Handler, string, string) {
	t.Helper()
	st := storetest.Open(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(path, []byte("mkv bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	// movie.en.srt is the on-disk sidecar the happy-path row points at.
	if err := os.WriteFile(filepath.Join(dir, "movie.en.srt"),
		[]byte("1\n00:00:01,000 --> 00:00:02,000\nHello sidecar\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(context.Background(), mediaID, path); err != nil {
		t.Fatal(err)
	}
	file, err := st.PrimaryMediaFile(context.Background(), mediaID, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	libraryID, err := st.MediaLibrary(context.Background(), mediaID)
	if err != nil {
		t.Fatal(err)
	}
	// Seed the sidecar rows on the file's fingerprint by re-importing with the
	// Sidecars slice (the store full-replaces them).
	if _, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          libraryID,
		Path:               path,
		Title:              "movie",
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: file.ContentFingerprint,
		Sidecars:           sidecars,
	}); err != nil {
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
		Target:        streamtoken.TargetMediaSidecar,
		MediaID:       mediaID,
		DeviceID:      scope.DeviceID,
		SessionID:     scope.SessionID,
		ProfileID:     scope.ProfileID,
		Restricted:    restricted,
		ExpiresAt:     now.Add(time.Hour),
		SidecarRawASS: len(sidecarRawASS) > 0 && sidecarRawASS[0],
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil))
	return handler, token, dir
}

func TestSidecarsServesTokenGatedWebVTT(t *testing.T) {
	handler, token, _ := newSidecarTestHandler(t, "session-default", "media-alpha", false,
		[]store.MediaFileSidecarCaption{{RelPath: "movie.en.srt", Format: "srt", Language: "en"}})

	rec := httptest.NewRecorder()
	handler.Sidecars(rec, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+token+"/0.vtt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/vtt; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/vtt; charset=utf-8", got)
	}
	body := rec.Body.String()
	// The .srt was converted to WebVTT in-process: a WEBVTT header plus dot
	// decimal separators in the cue timing.
	if want := "WEBVTT\n"; body[:len(want)] != want {
		t.Fatalf("body = %q, want WEBVTT header", body)
	}
	if want := "00:00:01.000 --> 00:00:02.000"; !strings.Contains(body, want) {
		t.Fatalf("body = %q, want converted cue timing %q", body, want)
	}
	assertTokenByteCacheControl(t, rec)
}

func TestSidecarsRejectsSealedLibrary(t *testing.T) {
	handler, token, _ := newSidecarTestHandler(t, "session-default", "media-alpha", false,
		[]store.MediaFileSidecarCaption{{RelPath: "movie.en.srt", Format: "srt", Language: "en"}})
	handler.SetSealed(func(libraryID store.LibraryID) bool { return libraryID == "library-local" })
	rec := httptest.NewRecorder()

	handler.Sidecars(rec, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+token+"/0.vtt", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for sealed sidecar", rec.Code)
	}
}

func TestSidecarsRejectsUnauthAndWrongTarget(t *testing.T) {
	handler, token, _ := newSidecarTestHandler(t, "session-default", "media-alpha", false,
		[]store.MediaFileSidecarCaption{{RelPath: "movie.en.srt", Format: "srt"}})

	// Tampered token → 401.
	rec := httptest.NewRecorder()
	handler.Sidecars(rec, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+token+"x/0.vtt", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered token status = %d, want 401", rec.Code)
	}

	// A direct-play token must not unlock the sidecar surface.
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
	handler.Sidecars(rec, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+directToken+"/0.vtt", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("direct token on sidecar route status = %d, want 401", rec.Code)
	}
}

func TestSidecarsRejectsRevealGatedRestricted(t *testing.T) {
	// media-archive is in the restricted Library; session-default holds no
	// reveal, so its restricted sidecar token must fail verification.
	handler, token, _ := newSidecarTestHandler(t, "session-default", "media-archive", true,
		[]store.MediaFileSidecarCaption{{RelPath: "movie.en.srt", Format: "srt"}})

	rec := httptest.NewRecorder()
	handler.Sidecars(rec, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+token+"/0.vtt", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unrevealed restricted sidecar status = %d, want 401", rec.Code)
	}
}

func TestSidecarsRejectsTraversalRelPath(t *testing.T) {
	// A recorded rel_path that climbs out of the media file's directory is
	// 404'd by the path-escape guard before any filesystem read — even though a
	// real "secret.srt" sits one level up.
	handler, token, dir := newSidecarTestHandler(t, "session-default", "media-alpha", false,
		[]store.MediaFileSidecarCaption{{RelPath: "../secret.srt", Format: "srt"}})
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.srt"), []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	handler.Sidecars(rec, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+token+"/0.vtt", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("traversal rel_path status = %d, want 404 (must not serve outside the media dir)", rec.Code)
	}
}

func TestSidecarsRejectsSymlinkEscape(t *testing.T) {
	handler, token, dir := newSidecarTestHandler(t, "session-default", "media-alpha", false,
		[]store.MediaFileSidecarCaption{{RelPath: "movie.link.srt", Format: "srt"}})
	outside := filepath.Join(t.TempDir(), "secret.srt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "movie.link.srt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	rec := httptest.NewRecorder()
	handler.Sidecars(rec, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+token+"/0.vtt", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("symlink escape status = %d, want 404", rec.Code)
	}
}

// TestSidecarsServesInteractiveAsRawBytes asserts the ADULT-3 interactive
// sidecar serving path: an interactive-kind sidecar is served behind the same
// Stream Token gate as a caption, but as RAW BYTES with a generic content type
// — never run through the WebVTT transform, which would corrupt a non-caption
// body. The "interactive" sidecar sorts before "movie.en.srt" by rel_path, so
// it is index 0.
func TestSidecarsServesInteractiveAsRawBytes(t *testing.T) {
	raw := []byte("interactive control payload, definitely not WebVTT")
	handler, token, dir := newSidecarTestHandler(t, "session-default", "media-alpha", false,
		[]store.MediaFileSidecarCaption{
			{RelPath: "interactive.dat", Kind: store.SidecarKindInteractive, Format: "dat"},
			{RelPath: "movie.en.srt", Format: "srt"},
		})
	if err := os.WriteFile(filepath.Join(dir, "interactive.dat"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	handler.Sidecars(rec, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+token+"/0.vtt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream", got)
	}
	if got := rec.Body.Bytes(); string(got) != string(raw) {
		t.Fatalf("body = %q, want raw bytes %q (must not be WebVTT-transformed)", got, raw)
	}
}

func TestSidecarsRejectsBadNamesAndOutOfRange(t *testing.T) {
	handler, token, _ := newSidecarTestHandler(t, "session-default", "media-alpha", false,
		[]store.MediaFileSidecarCaption{{RelPath: "movie.en.srt", Format: "srt"}})

	for _, name := range []string{"abc.vtt", "-1.vtt", "0.srt", "..%2F0.vtt"} {
		rec := httptest.NewRecorder()
		handler.Sidecars(rec, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+token+"/"+name, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", name, rec.Code)
		}
	}
	// Index past the recorded sidecar list → 404.
	rec := httptest.NewRecorder()
	handler.Sidecars(rec, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+token+"/5.vtt", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("out-of-range index status = %d, want 404", rec.Code)
	}
}
