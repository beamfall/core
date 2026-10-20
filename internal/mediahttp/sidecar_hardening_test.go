package mediahttp

import (
	"bytes"
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/playback/subtitleingest"
	"github.com/beamfall/core/internal/store"
)

const rawASSFixture = "[Script Info]\n" +
	"ScriptType: v4.00+\n" +
	"[Events]\n" +
	"Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
	"Dialogue: 0,0:00:01.00,0:00:02.50,Default,,0,0,0,,{\\pos(400,570)\\i1}Styled sign{\\i0}\n"

func TestSidecarsRejectsRawASSWithoutCapability(t *testing.T) {
	handler, token, dir := newSidecarTestHandler(t, "session-default", "media-alpha", false,
		[]store.MediaFileSidecarCaption{{RelPath: "movie.en.ass", Format: "ass", Language: "en"}})
	if err := os.WriteFile(filepath.Join(dir, "movie.en.ass"), []byte(rawASSFixture), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	handler.Sidecars(rec, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+token+"/0.ass", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("extension-rewritten raw ASS status = %d, want 404", rec.Code)
	}
}

// A ".ass" request for an ass caption serves the authored bytes byte-identically
// (raw-ASS passthrough for a declared-`ass` profile), while a ".vtt" request for
// the same sidecar still flattens.
func TestSidecarsRawASSPassthroughByteIdentical(t *testing.T) {
	handler, token, dir := newSidecarTestHandler(t, "session-default", "media-alpha", false,
		[]store.MediaFileSidecarCaption{{RelPath: "movie.en.ass", Format: "ass", Language: "en"}}, true)
	if err := os.WriteFile(filepath.Join(dir, "movie.en.ass"), []byte(rawASSFixture), 0o600); err != nil {
		t.Fatal(err)
	}

	// .ass route: byte-identical authored ASS.
	rawRec := httptest.NewRecorder()
	handler.Sidecars(rawRec, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+token+"/0.ass", nil))
	if rawRec.Code != http.StatusOK {
		t.Fatalf("ass route status = %d, want 200: %s", rawRec.Code, rawRec.Body.String())
	}
	if rawRec.Body.String() != rawASSFixture {
		t.Fatalf("ass route not byte-identical:\n got=%q\nwant=%q", rawRec.Body.String(), rawASSFixture)
	}
	if ct := rawRec.Header().Get("Content-Type"); ct != "text/x-ssa" {
		t.Fatalf("ass Content-Type = %q, want text/x-ssa without an unverified charset", ct)
	}

	// .vtt route: same sidecar, flattened to WebVTT (styling dropped).
	vttRec := httptest.NewRecorder()
	handler.Sidecars(vttRec, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+token+"/0.vtt", nil))
	if vttRec.Code != http.StatusOK {
		t.Fatalf("vtt route status = %d, want 200: %s", vttRec.Code, vttRec.Body.String())
	}
	body := vttRec.Body.String()
	if !strings.HasPrefix(body, "WEBVTT") || strings.Contains(body, "\\pos") {
		t.Fatalf("vtt route not flattened: %q", body)
	}
	if !strings.Contains(body, "00:00:01.000 --> 00:00:02.500") || !strings.Contains(body, "Styled sign") {
		t.Fatalf("vtt route wrong cue: %q", body)
	}
}

func TestSidecarsRawASSDoesNotClaimUTF8ForCP1252Bytes(t *testing.T) {
	handler, token, dir := newSidecarTestHandler(t, "session-default", "media-alpha", false,
		[]store.MediaFileSidecarCaption{{RelPath: "movie.fr.ass", Format: "ass", Language: "fr"}}, true)
	raw := append([]byte("[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,caf"), 0xe9)
	raw = append(raw, '\n')
	if err := os.WriteFile(filepath.Join(dir, "movie.fr.ass"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	handler.Sidecars(rec, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+token+"/0.ass", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), raw) {
		t.Fatalf("raw ASS body changed:\n got=%q\nwant=%q", rec.Body.Bytes(), raw)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/x-ssa" {
		t.Fatalf("Content-Type = %q, want text/x-ssa without a charset", ct)
	}
}

// The viewer's persisted offset nudge is applied server-side on playback: the
// served WebVTT carries shifted cue timings, keyed on the track's byte
// fingerprint.
func TestSidecarsAppliesPersistedOffsetNudge(t *testing.T) {
	handler, token, dir := newSidecarTestHandler(t, "session-default", "media-alpha", false,
		[]store.MediaFileSidecarCaption{{RelPath: "movie.en.srt", Format: "srt", Language: "en"}})

	// Unshifted baseline: the harness sidecar's first cue starts at 1.000s.
	base := httptest.NewRecorder()
	handler.Sidecars(base, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+token+"/0.vtt", nil))
	if base.Code != http.StatusOK {
		t.Fatalf("baseline status = %d: %s", base.Code, base.Body.String())
	}
	if !strings.Contains(base.Body.String(), "00:00:01.000 --> 00:00:02.000") {
		t.Fatalf("baseline cue timing unexpected: %q", base.Body.String())
	}

	// Persist a +750ms nudge against this track's byte fingerprint.
	raw, err := os.ReadFile(filepath.Join(dir, "movie.en.srt")) // #nosec G304 -- fixed name under this test's own temp directory
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := store.SubtitleTrackFingerprint(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.store.PutSubtitleOffset(context.Background(), "profile-default", fingerprint, 750, time.Unix(100, 0).UTC()); err != nil {
		t.Fatalf("PutSubtitleOffset: %v", err)
	}

	shifted := httptest.NewRecorder()
	handler.Sidecars(shifted, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+token+"/0.vtt", nil))
	if shifted.Code != http.StatusOK {
		t.Fatalf("shifted status = %d: %s", shifted.Code, shifted.Body.String())
	}
	if !strings.Contains(shifted.Body.String(), "00:00:01.750 --> 00:00:02.750") {
		t.Fatalf("offset nudge not applied on playback: %q", shifted.Body.String())
	}
}

// A hostile .vtt sidecar reaches the browser DOM verbatim today; readSidecarAsWebVTT
// must run it through the shared ingest-hardening boundary so injected markup is
// defanged before serve.
func TestReadSidecarAsWebVTTSanitizesMarkup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hostile.vtt")
	hostile := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\n<i>ok</i><script>alert(1)</script>\n"
	if err := os.WriteFile(path, []byte(hostile), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := readSidecarAsWebVTT(rootedFile(path), "vtt")
	if err != nil {
		t.Fatalf("readSidecarAsWebVTT err = %v, want nil", err)
	}
	if strings.Contains(out, "<script>") {
		t.Fatalf("served sidecar still carries raw <script>: %q", out)
	}
	if !strings.Contains(out, "<i>ok</i>") {
		t.Fatalf("served sidecar dropped allowed <i>: %q", out)
	}
}

// A CP1252 .srt sidecar is decoded to UTF-8 on serve so accented text does not
// render as mojibake.
func TestReadSidecarAsWebVTTDecodesCP1252(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "movie.fr.srt")
	// "café" with a CP1252 0xE9 for 'é', plus a 0x92 smart apostrophe.
	raw := []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\ncaf\xe9 l\x92ami\r\n")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := readSidecarAsWebVTT(rootedFile(path), "srt")
	if err != nil {
		t.Fatalf("readSidecarAsWebVTT err = %v, want nil", err)
	}
	if !strings.Contains(out, "café") {
		t.Fatalf("CP1252 é not decoded: %q", out)
	}
	if strings.Contains(out, "�") {
		t.Fatalf("served sidecar carries replacement chars (mojibake): %q", out)
	}
}

// An .ass sidecar is flattened to timed WebVTT cues on serve for non-libass
// clients.
func TestReadSidecarAsWebVTTFlattensASS(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "movie.ass")
	ass := "[Events]\n" +
		"Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:01.00,0:00:02.50,Default,,0,0,0,,{\\i1}Hi there{\\i0}\n"
	if err := os.WriteFile(path, []byte(ass), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := readSidecarAsWebVTT(rootedFile(path), "ass")
	if err != nil {
		t.Fatalf("readSidecarAsWebVTT err = %v, want nil", err)
	}
	if !strings.HasPrefix(out, "WEBVTT") {
		t.Fatalf("ASS flatten missing WEBVTT header: %q", out)
	}
	if !strings.Contains(out, "00:00:01.000 --> 00:00:02.500") || !strings.Contains(out, "Hi there") {
		t.Fatalf("ASS flatten wrong cue: %q", out)
	}
}

// An oversize sidecar is rejected before the in-process parse, failing closed.
func TestReadSidecarAsWebVTTRejectsOversize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.srt")
	big := strings.Repeat("A", subtitleingest.MaxTextBytes+1)
	if err := os.WriteFile(path, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readSidecarAsWebVTT(rootedFile(path), "srt"); err == nil {
		t.Fatal("readSidecarAsWebVTT(oversize) err = nil, want rejection")
	}
}

// The size cap must be a MEMORY bound, not a value check: an oversize sidecar is
// refused from its directory entry, before its bytes are ever allocated. The
// file here is sparse, so a route that reads first and checks after would have
// to materialise MaxTextBytes+1 bytes of resident memory to fail — this test
// asserts the refusal happens without that allocation.
func TestReadGuardedSidecarRefusesOversizeWithoutAllocating(t *testing.T) {
	for _, tc := range []struct {
		format string
		size   int64
	}{
		{"srt", subtitleingest.MaxTextBytes + 1},
		{"sup", subtitleingest.MaxBitmapBytes + 1},
	} {
		path := filepath.Join(t.TempDir(), "movie.en."+tc.format)
		writeSparseFile(t, path, tc.size)
		before := residentAllocBytes()
		raw, err := readGuardedSidecar(rootedFile(path), tc.format)
		after := residentAllocBytes()
		if !errors.Is(err, subtitleingest.ErrTooLarge) {
			t.Fatalf("%s: readGuardedSidecar err = %v, want ErrTooLarge", tc.format, err)
		}
		if raw != nil {
			t.Fatalf("%s: readGuardedSidecar returned %d bytes on a refusal", tc.format, len(raw))
		}
		if grew := after - before; grew > tc.size/2 {
			t.Fatalf("%s: refusal allocated %d bytes for a %d-byte file — the cap ran after the read", tc.format, grew, tc.size)
		}
	}
}

// MaxBitmapBytes is enforced on the production byte route, not merely defined:
// the bitmap sidecar branch passes its bytes through the ingest boundary.
func TestSidecarsBitmapRouteEnforcesBitmapCap(t *testing.T) {
	handler, token, dir := newSidecarTestHandler(t, "session-default", "media-alpha", false,
		[]store.MediaFileSidecarCaption{{RelPath: "movie.en.sup", Format: "sup", Language: "en"}})
	writeSparseFile(t, filepath.Join(dir, "movie.en.sup"), subtitleingest.MaxBitmapBytes+1)

	rec := httptest.NewRecorder()
	handler.Sidecars(rec, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+token+"/0.sup", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown extension status = %d, want 404", rec.Code)
	}
	// The bitmap sidecar is minted as a ".vtt" URL (the byte route resolves the
	// row and serves it raw), so that is the request an oversize payload arrives on.
	rec = httptest.NewRecorder()
	handler.Sidecars(rec, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+token+"/0.vtt", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("oversize bitmap status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
}

// A muxed subtitle is not safer than an external sidecar: ffmpeg's webvtt muxer
// re-emits an ASS/SRT source's inline markup verbatim, so the extracted WebVTT
// carries the same cue-injection surface. It must pass the same hardening
// boundary before it can reach a renderer.
func TestSubtitlesSanitizesExtractedMuxedWebVTT(t *testing.T) {
	hostile := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\n" +
		"<i>ok</i><img src=x onerror=alert(1)><b onmouseover=alert(2)>x</b>" +
		"<a href=\"javascript:alert(3)\">link</a>\n"
	conv := &fakeSubtitleConverter{body: hostile}
	handler, token := newSubtitleTestHandler(t, "session-default", "media-alpha", false, conv)

	rec := httptest.NewRecorder()
	handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, "/api/media/subtitle/"+token+"/2.vtt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Non-WebVTT markup is defanged: the tag is escaped, so its attributes reach
	// the DOM as literal text rather than as an element. (An attribute on an
	// ALLOWED cue tag — "<b onmouseover=…>" — still passes through; the sanitiser
	// allowlists tag names but not attributes, which is tracked separately.)
	for _, injected := range []string{"<img", "<a href", "<script"} {
		if strings.Contains(body, injected) {
			t.Fatalf("muxed WebVTT still carries %q verbatim: %q", injected, body)
		}
	}
	if !strings.Contains(body, "&lt;img src=x onerror=alert(1)&gt;") {
		t.Fatalf("injected <img> was not escaped into literal text: %q", body)
	}
	if !strings.Contains(body, "<i>ok</i>") {
		t.Fatalf("sanitiser dropped the allowed <i> cue tag: %q", body)
	}
	if !strings.Contains(body, "00:00:01.000 --> 00:00:02.000") {
		t.Fatalf("sanitiser corrupted the cue timing line: %q", body)
	}

	// The hardening is paid ONCE, at cache-write time: the second fetch is served
	// from the cache file, which is already sanitised on disk.
	cached, err := os.ReadFile(filepath.Join(handler.subtitleDir, "sha256_"+strings.TrimPrefix(mustFingerprint(t, handler), "sha256:")+".2.vtt")) // #nosec G304 -- composed from this test's own temp cache dir
	if err != nil {
		t.Fatalf("read cached sidecar: %v", err)
	}
	if strings.Contains(string(cached), "<img") {
		t.Fatalf("cache file on disk is unsanitised: %q", string(cached))
	}
	if conv.calls.Load() != 1 {
		t.Fatalf("converter calls = %d, want 1", conv.calls.Load())
	}
}

// An extracted muxed subtitle that cannot clear the boundary is never published
// to the cache, so the failure is not cached and the route fails closed.
func TestSubtitlesRefusesUnhardenableExtraction(t *testing.T) {
	conv := &fakeSubtitleConverter{body: strings.Repeat("A", subtitleingest.MaxTextBytes+1)}
	handler, token := newSubtitleTestHandler(t, "session-default", "media-alpha", false, conv)

	rec := httptest.NewRecorder()
	handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, "/api/media/subtitle/"+token+"/2.vtt", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 for an unhardenable extraction (body %d bytes)", rec.Code, rec.Body.Len())
	}
	entries, err := os.ReadDir(handler.subtitleDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".vtt") {
			t.Fatalf("refused extraction left a servable cache file: %s", e.Name())
		}
	}
}

// mustFingerprint returns the content fingerprint of the harness's media file,
// which keys the subtitle cache filename.
func mustFingerprint(t *testing.T, handler *Handler) string {
	t.Helper()
	file, err := handler.store.PrimaryMediaFile(context.Background(), "media-alpha", store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	return file.ContentFingerprint
}

// writeSparseFile creates a file of the given logical length without writing its
// bytes, so an oversize fixture costs no disk and any resident-memory growth in
// the test is the code under test allocating, not the fixture.
func writeSparseFile(t *testing.T, path string, size int64) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 -- test-owned temp path
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
}

// residentAllocBytes reports the process's currently-allocated heap bytes,
// saturated into int64 (no host reaches a heap past MaxInt64).
func residentAllocBytes() int64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	if m.HeapAlloc > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(m.HeapAlloc)
}
