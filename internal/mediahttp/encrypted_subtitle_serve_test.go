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
	"sync/atomic"
	"testing"

	"github.com/beamfall/core/internal/playback/subtitleingest"
	"github.com/beamfall/core/internal/store"
)

// encryptedSubtitleBody carries markup the ingest boundary must defang, so a
// served body proves the in-memory hardening ran.
const encryptedSubtitleBody = "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nEncrypted cue <script>x</script>\n"

// fakeSubtitleExtractor stands in for the encderiv-backed extractor.
type fakeSubtitleExtractor struct {
	body   string
	err    error
	calls  atomic.Int32
	mu     sync.Mutex
	got    EncryptedSubtitleRequest
	during func(EncryptedSubtitleRequest) error
}

func (f *fakeSubtitleExtractor) ExtractEncryptedSubtitle(ctx context.Context, req EncryptedSubtitleRequest) ([]byte, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.got = req
	f.mu.Unlock()
	if f.during != nil {
		if err := f.during(req); err != nil {
			return nil, err
		}
	}
	if err := req.Revoked(ctx); err != nil {
		return nil, err
	}
	if f.err != nil {
		return nil, f.err
	}
	return []byte(f.body), nil
}

// fakeObjectCipher stands in for libencrypt: an object is a magic prefix, the
// identity it is bound to, and the XOR-masked plaintext. Decryption under any
// other identity fails, like an AEAD with the context as AAD.
type fakeObjectCipher struct {
	writes atomic.Int32
}

const fakeObjectMagic = "BFE2"

func fakeMask(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = c ^ 0x5a
	}
	return out
}

func (f *fakeObjectCipher) EncryptObject(libraryID store.LibraryID, src io.Reader, ciphertextPath, objectClass, immutableObjectID string) error {
	f.writes.Add(1)
	plain, err := io.ReadAll(src)
	if err != nil {
		return err
	}
	header := fakeObjectMagic + string(libraryID) + "|" + objectClass + "|" + immutableObjectID + "\n"
	return os.WriteFile(ciphertextPath, append([]byte(header), fakeMask(plain)...), 0o600)
}

func (f *fakeObjectCipher) DecryptBytes(libraryID store.LibraryID, ciphertextPath, objectClass, immutableObjectID string) ([]byte, error) {
	raw, err := os.ReadFile(ciphertextPath) // #nosec G304 -- test temp-dir path.
	if err != nil {
		return nil, err
	}
	header := fakeObjectMagic + string(libraryID) + "|" + objectClass + "|" + immutableObjectID + "\n"
	rest, ok := bytes.CutPrefix(raw, []byte(header))
	if !ok {
		return nil, errors.New("fake: authentication failed")
	}
	return fakeMask(rest), nil
}

func newEncryptedSubtitleTestHandler(t *testing.T, extractor *fakeSubtitleExtractor) (*Handler, string, *fakeObjectCipher) {
	t.Helper()
	conv := &fakeSubtitleConverter{err: errors.New("the plaintext converter must not run for an Encrypted Library")}
	handler, token := newSubtitleTestHandler(t, "session-default", "media-alpha", false, conv)
	if err := handler.store.SetLibraryBacking(context.Background(), "library-local", false, true); err != nil {
		t.Fatalf("SetLibraryBacking encrypted: %v", err)
	}
	cipher := &fakeObjectCipher{}
	handler.SetEncryptedBytes(cipher)
	handler.SetEncryptedSubtitles(extractor, cipher)
	return handler, token, cipher
}

func getEncryptedSubtitle(handler *Handler, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, "/api/media/subtitle/"+token+"/2.vtt", nil))
	return rec
}

func encryptedSubtitleCachePath(handler *Handler, fingerprint string, streamIndex int) string {
	return filepath.Join(handler.subtitleDir, encryptedSubtitleLibraryRel("library-local"), fmt.Sprintf("%s.%d.vtt", safeSubtitleFingerprint(fingerprint), streamIndex))
}

func TestSubtitlesServesSanitizedVTTForAnUnsealedEncryptedLibrary(t *testing.T) {
	extractor := &fakeSubtitleExtractor{body: encryptedSubtitleBody}
	handler, token, cipher := newEncryptedSubtitleTestHandler(t, extractor)

	rec := getEncryptedSubtitle(handler, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	want := subtitleingest.SanitizeWebVTT(encryptedSubtitleBody)
	if rec.Body.String() != want || strings.Contains(rec.Body.String(), "<script>") {
		t.Fatalf("body = %q, want the sanitized WebVTT %q", rec.Body.String(), want)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/vtt; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	assertTokenByteCacheControl(t, rec)

	extractor.mu.Lock()
	req := extractor.got
	extractor.mu.Unlock()
	if req.LibraryID != "library-local" || req.StreamIndex != 2 || req.Fingerprint == "" || req.SourcePath == "" || req.MaxBytes != subtitleingest.MaxTextBytes {
		t.Fatalf("extract request = %+v", req)
	}

	// The object is bound to the subtitle identity and is not the plaintext.
	cachePath := encryptedSubtitleCachePath(handler, req.Fingerprint, 2)
	raw, err := os.ReadFile(cachePath) // #nosec G304 -- test temp-dir path.
	if err != nil {
		t.Fatalf("cache object: %v", err)
	}
	if !bytes.HasPrefix(raw, []byte(fakeObjectMagic)) || bytes.Contains(raw, []byte("Encrypted cue")) {
		t.Fatalf("cache object is not ciphertext: %q", raw)
	}
	if !bytes.Contains(raw, []byte(encryptedSubtitleObjectID(req.Fingerprint, 2))) {
		t.Fatalf("cache object is not bound to the subtitle identity: %q", raw)
	}
	// No plaintext sidecar lands at the top level of the cache.
	if top, _ := filepath.Glob(filepath.Join(handler.subtitleDir, "*.vtt*")); len(top) != 0 {
		t.Fatalf("plaintext sidecar written: %v", top)
	}

	// A second fetch decrypts the cached object without extracting again.
	if rec := getEncryptedSubtitle(handler, token); rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("cached fetch = %d %q", rec.Code, rec.Body.String())
	}
	if extractor.calls.Load() != 1 || cipher.writes.Load() != 1 {
		t.Fatalf("extracts = %d, writes = %d, want 1 each", extractor.calls.Load(), cipher.writes.Load())
	}
}

func TestSubtitlesRegeneratesAnEncryptedCacheObjectThatNoLongerDecrypts(t *testing.T) {
	extractor := &fakeSubtitleExtractor{body: encryptedSubtitleBody}
	handler, token, _ := newEncryptedSubtitleTestHandler(t, extractor)
	if rec := getEncryptedSubtitle(handler, token); rec.Code != http.StatusOK {
		t.Fatalf("first fetch = %d", rec.Code)
	}
	extractor.mu.Lock()
	cachePath := encryptedSubtitleCachePath(handler, extractor.got.Fingerprint, 2)
	extractor.mu.Unlock()
	if err := os.WriteFile(cachePath, []byte("torn"), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := getEncryptedSubtitle(handler, token)
	if rec.Code != http.StatusOK || rec.Body.String() != subtitleingest.SanitizeWebVTT(encryptedSubtitleBody) {
		t.Fatalf("regenerated fetch = %d %q", rec.Code, rec.Body.String())
	}
	if extractor.calls.Load() != 2 {
		t.Fatalf("extracts = %d, want 2 (the torn object regenerates)", extractor.calls.Load())
	}
}

func TestSubtitlesRefusesASealedEncryptedLibrary(t *testing.T) {
	extractor := &fakeSubtitleExtractor{body: encryptedSubtitleBody}
	handler, token, cipher := newEncryptedSubtitleTestHandler(t, extractor)
	handler.SetSealed(func(libraryID store.LibraryID) bool { return libraryID == "library-local" })

	if rec := getEncryptedSubtitle(handler, token); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a Sealed Library", rec.Code)
	}
	if extractor.calls.Load() != 0 || cipher.writes.Load() != 0 {
		t.Fatalf("extracts = %d, writes = %d, want none", extractor.calls.Load(), cipher.writes.Load())
	}
}

func TestSubtitlesRefusesAnUnauthorizedEncryptedRequest(t *testing.T) {
	extractor := &fakeSubtitleExtractor{body: encryptedSubtitleBody}
	handler, token, _ := newEncryptedSubtitleTestHandler(t, extractor)

	if rec := getEncryptedSubtitle(handler, token+"x"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a token that does not verify", rec.Code)
	}
	if extractor.calls.Load() != 0 {
		t.Fatalf("extracts = %d, want none", extractor.calls.Load())
	}
}

func TestSubtitlesStopsAnEncryptedExtractionSealedMidway(t *testing.T) {
	var sealed atomic.Bool
	extractor := &fakeSubtitleExtractor{body: encryptedSubtitleBody, during: func(EncryptedSubtitleRequest) error {
		sealed.Store(true)
		return nil
	}}
	handler, token, cipher := newEncryptedSubtitleTestHandler(t, extractor)
	handler.SetSealed(func(store.LibraryID) bool { return sealed.Load() })

	if rec := getEncryptedSubtitle(handler, token); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 once the Library seals mid-extraction", rec.Code)
	}
	if cipher.writes.Load() != 0 {
		t.Fatalf("writes = %d, want no cache object after revocation", cipher.writes.Load())
	}
}

func TestSubtitlesDoesNotCacheAFailedEncryptedExtraction(t *testing.T) {
	extractor := &fakeSubtitleExtractor{err: errors.New("tool failed")}
	handler, token, cipher := newEncryptedSubtitleTestHandler(t, extractor)

	if rec := getEncryptedSubtitle(handler, token); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	extractor.err = nil
	extractor.body = strings.Repeat("x", int(subtitleingest.MaxTextBytes)+1)
	if rec := getEncryptedSubtitle(handler, token); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("oversize status = %d, want 503", rec.Code)
	}
	if cipher.writes.Load() != 0 {
		t.Fatalf("writes = %d, want none", cipher.writes.Load())
	}
}

// TestSubtitlesStopsAnEncryptedExtractionWhoseLibraryGrantIsWithdrawn pins the
// scope re-check: the grant still verifies after the Profile loses its Library
// grant, so only re-resolving the Media under the refreshed playback filter
// stops the extraction.
func TestSubtitlesStopsAnEncryptedExtractionWhoseLibraryGrantIsWithdrawn(t *testing.T) {
	extractor := &fakeSubtitleExtractor{body: encryptedSubtitleBody}
	handler, token, cipher := newEncryptedSubtitleTestHandler(t, extractor)
	ctx := context.Background()
	scope, err := handler.auth.Scope(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	extractor.during = func(EncryptedSubtitleRequest) error {
		_, err := handler.store.SetProfileLibraries(ctx, scope.ProfileID, nil)
		return err
	}

	if rec := getEncryptedSubtitle(handler, token); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 once the Library grant is withdrawn mid-extraction: %s", rec.Code, rec.Body.String())
	}
	if cipher.writes.Load() != 0 {
		t.Fatalf("writes = %d, want no cache object after the grant is withdrawn", cipher.writes.Load())
	}
}

// TestSubtitlesReauthorizesACachedEncryptedSubtitleBeforeServing pins the final
// check: a request that finds the object cached (or waited for another
// request's extraction) still re-checks authorization before bytes leave.
func TestSubtitlesReauthorizesACachedEncryptedSubtitleBeforeServing(t *testing.T) {
	extractor := &fakeSubtitleExtractor{body: encryptedSubtitleBody}
	handler, token, _ := newEncryptedSubtitleTestHandler(t, extractor)
	if rec := getEncryptedSubtitle(handler, token); rec.Code != http.StatusOK {
		t.Fatalf("first fetch = %d", rec.Code)
	}
	// The Library reads Unsealed at the request's entry check and Sealed from
	// then on, as when a Seal lands while the request is in flight.
	var checks atomic.Int32
	handler.SetSealed(func(store.LibraryID) bool { return checks.Add(1) > 1 })

	if rec := getEncryptedSubtitle(handler, token); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "Encrypted cue") {
		t.Fatalf("cached fetch after revocation = %d %q, want 404 and no cue", rec.Code, rec.Body.String())
	}
	if extractor.calls.Load() != 1 {
		t.Fatalf("extracts = %d, want 1 (the second fetch is a cache hit)", extractor.calls.Load())
	}
	if checks.Load() < 2 {
		t.Fatalf("sealed checks = %d, want the entry check and the pre-serve check", checks.Load())
	}
}

// TestSubtitlesRefusesASymlinkedEncryptedCacheDirectory pins that a planted
// link at the Library's cache directory is refused rather than followed, so
// regeneration can never write or remove files outside the subtitle cache.
func TestSubtitlesRefusesASymlinkedEncryptedCacheDirectory(t *testing.T) {
	extractor := &fakeSubtitleExtractor{body: encryptedSubtitleBody}
	handler, token, cipher := newEncryptedSubtitleTestHandler(t, extractor)
	outside := t.TempDir()
	link := filepath.Join(handler.subtitleDir, encryptedSubtitleLibraryRel("library-local"))
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	if rec := getEncryptedSubtitle(handler, token); rec.Code == http.StatusOK {
		t.Fatalf("status = 200, want a refusal for a symlinked cache directory")
	}
	if extractor.calls.Load() != 0 || cipher.writes.Load() != 0 {
		t.Fatalf("extract calls = %d, writes = %d, want none", extractor.calls.Load(), cipher.writes.Load())
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("outside dir entries = %v (err %v), want none", entries, err)
	}
}
