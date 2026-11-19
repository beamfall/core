package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/playback/sessiontest"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// encryptedSubtitleCue is the cue text the fake extractor emits. The
// no-plaintext test searches every file for it.
const encryptedSubtitleCue = "quiet harbor lantern cue"

// endpointSubtitleRunner stands in for FFmpeg's subtitle extraction: it reads
// the whole decrypted source from the loopback endpoint, as FFmpeg would, and
// writes a WebVTT track to stdout.
type endpointSubtitleRunner struct {
	source []byte
	calls  atomic.Int32
	during func()
}

func (r *endpointSubtitleRunner) ExtractSubtitleToPipe(ctx context.Context, in ffmpeg.PipeInput, streamIndex, limit int, stdout io.Writer) error {
	r.calls.Add(1)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	got, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	if !bytes.Equal(got, r.source) {
		return errors.New("endpoint did not serve the decrypted source")
	}
	if r.during != nil {
		r.during()
	}
	vtt := "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\n" + encryptedSubtitleCue + " <b onclick=x>bold</b>\n"
	if len(vtt) > limit {
		return ffmpeg.ErrOutputOverflow
	}
	_, err = io.WriteString(stdout, vtt)
	return err
}

type encryptedSubtitleFixture struct {
	handler  *mediahttp.Handler
	manager  *libencrypt.Manager
	runner   *endpointSubtitleRunner
	token    string
	dataDir  string
	subsDir  string
	tempDir  string
	library  store.LibraryID
	mediaRaw []byte
}

// newEncryptedSubtitleFixture builds the subtitle route with the production
// adapters (a real libencrypt manager, the encderiv-backed extractor and the
// object writer) and only FFmpeg faked. TMPDIR points at a directory the test
// owns so the no-plaintext walk can cover the temp dir too.
func newEncryptedSubtitleFixture(t *testing.T) encryptedSubtitleFixture {
	t.Helper()
	const libraryID = store.LibraryID("library-local")
	const mediaID = store.MediaID("media-alpha")
	const sessionID = store.SessionID("session-default")
	ctx := context.Background()

	root := t.TempDir()
	tempDir := filepath.Join(root, "tmp")
	dataDir := filepath.Join(root, "data")
	for _, dir := range []string{tempDir, dataDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMPDIR", tempDir)

	st := storetest.Open(t)
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	manager := libencrypt.NewManagerWithParams(memSecretStore{}, libencrypt.TestArgon2Params)
	if _, err := manager.Provision(string(libraryID), libencrypt.UnsealModeAuto, ""); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	adapter := appLibEncryptDBCipher{manager: manager}

	mediaRaw := []byte("synthetic container bytes for the subtitle source")
	mediaPath := filepath.Join(dataDir, "media.bin")
	if err := os.WriteFile(mediaPath, mediaRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(ctx, mediaID, mediaPath); err != nil {
		t.Fatalf("SetPrimaryMediaFile: %v", err)
	}
	fingerprint, err := store.HashMediaFileContent(mediaPath)
	if err != nil {
		t.Fatal(err)
	}
	ciphertextPath := filepath.Join(dataDir, "media.enc")
	if err := adapter.EncryptObjectFile(string(libraryID), mediaPath, ciphertextPath, fingerprint); err != nil {
		t.Fatalf("EncryptObjectFile: %v", err)
	}
	if err := os.Rename(ciphertextPath, mediaPath); err != nil {
		t.Fatal(err)
	}

	authService := auth.NewService(st)
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	scope, err := authService.Scope(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	token, err := tokens.Issue(ctx, streamtoken.Grant{
		Target:    streamtoken.TargetMediaSubtitle,
		MediaID:   mediaID,
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	runner := &endpointSubtitleRunner{source: mediaRaw}
	subsDir := filepath.Join(dataDir, "subtitles")
	handler := mediahttp.NewHandler(st, authService, tokens, artwork.NewService(st, nil, dataDir), sessiontest.New(t, nil, filepath.Join(dataDir, "transcode"), nil))
	// The wirings mirror the composition root (app.New), with FFmpeg faked.
	handler.SetSubtitles(ffmpeg.New(), subsDir)
	handler.SetSealed(func(id store.LibraryID) bool {
		return manager.Provisioned(string(id)) && manager.Sealed(string(id))
	})
	handler.SetEncryptedBytes(adapter)
	handler.SetEncryptedSubtitles(encryptedSubtitleExtractor{content: adapter, runner: runner}, adapter)

	return encryptedSubtitleFixture{
		handler: handler, manager: manager, runner: runner, token: token,
		dataDir: dataDir, subsDir: subsDir, tempDir: tempDir, library: libraryID, mediaRaw: mediaRaw,
	}
}

func (f encryptedSubtitleFixture) get(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, "/api/media/subtitle/"+f.token+"/2.vtt", nil))
	return rec
}

// filesContaining walks dirs and returns every regular file whose bytes
// contain needle.
func filesContaining(t *testing.T, needle string, dirs ...string) []string {
	t.Helper()
	var hits []string
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.Type().IsRegular() {
				return nil
			}
			raw, err := os.ReadFile(path) // #nosec G304 G122 -- test-owned temp-dir walk.
			if err != nil {
				return err
			}
			if bytes.Contains(raw, []byte(needle)) {
				hits = append(hits, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	return hits
}

func TestEncryptedLibrarySubtitleServesSanitizedVTTThroughProductionAdapters(t *testing.T) {
	fixture := newEncryptedSubtitleFixture(t)

	rec := fixture.get(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "WEBVTT") || !strings.Contains(body, encryptedSubtitleCue) || strings.Contains(body, "onclick") {
		t.Fatalf("body = %q, want sanitized WebVTT carrying the cue", body)
	}
	if again := fixture.get(t); again.Code != http.StatusOK || again.Body.String() != body {
		t.Fatalf("cached fetch = %d %q", again.Code, again.Body.String())
	}
	if got := fixture.runner.calls.Load(); got != 1 {
		t.Fatalf("extractions = %d, want 1 (the second fetch decrypts the cache)", got)
	}
}

func TestEncryptedLibrarySubtitleWritesNoPlaintext(t *testing.T) {
	fixture := newEncryptedSubtitleFixture(t)
	if rec := fixture.get(t); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	if hits := filesContaining(t, encryptedSubtitleCue, fixture.dataDir, fixture.subsDir, fixture.tempDir); len(hits) != 0 {
		t.Fatalf("subtitle plaintext on disk: %v", hits)
	}
	var objects []string
	err := filepath.WalkDir(fixture.subsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			objects = append(objects, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 1 || !strings.Contains(objects[0], string(filepath.Separator)+"encrypted"+string(filepath.Separator)) {
		t.Fatalf("subtitle cache files = %v, want one object under the encrypted cache", objects)
	}
	file, err := os.Open(objects[0])
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if ok, err := libencrypt.HasObjectHeader(file); err != nil || !ok {
		t.Fatalf("cache object is not a libencrypt object: ok=%v err=%v", ok, err)
	}
}

func TestEncryptedLibrarySubtitleRefusesWhenSealed(t *testing.T) {
	fixture := newEncryptedSubtitleFixture(t)
	fixture.manager.Seal(string(fixture.library))

	if rec := fixture.get(t); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a Sealed Library", rec.Code)
	}
	if got := fixture.runner.calls.Load(); got != 0 {
		t.Fatalf("extractions = %d, want 0", got)
	}
}

func TestEncryptedLibrarySubtitleStopsWhenSealedMidExtraction(t *testing.T) {
	fixture := newEncryptedSubtitleFixture(t)
	fixture.runner.during = func() { fixture.manager.Seal(string(fixture.library)) }

	if rec := fixture.get(t); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 once the Library seals mid-extraction", rec.Code)
	}
	if hits := filesContaining(t, encryptedSubtitleCue, fixture.dataDir, fixture.tempDir); len(hits) != 0 {
		t.Fatalf("subtitle plaintext on disk: %v", hits)
	}
	entries, _ := filepath.Glob(filepath.Join(fixture.subsDir, "encrypted", "*", "*"))
	if len(entries) != 0 {
		t.Fatalf("cache object written after revocation: %v", entries)
	}
}
