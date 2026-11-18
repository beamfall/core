package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
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
	"github.com/beamfall/core/internal/genassets"
	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// encryptedAssetMarker is embedded in the fake preview clip so the
// no-plaintext walk can find any copy of it.
const encryptedAssetMarker = "amber corridor preview marker"

// endpointAssetRunner stands in for FFmpeg's asset passes: every call reads the
// whole decrypted source from the loopback endpoint, as FFmpeg would, and then
// writes its asset to stdout or returns its analysis.
type endpointAssetRunner struct {
	sources map[string][]byte // the decrypted sources the endpoint may serve
	calls   atomic.Int32
	during  func()
}

func (r *endpointAssetRunner) read(ctx context.Context, in ffmpeg.PipeInput) error {
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
	matched := false
	for _, source := range r.sources {
		matched = matched || bytes.Equal(got, source)
	}
	if !matched {
		return errors.New("endpoint did not serve a decrypted source")
	}
	if r.during != nil {
		r.during()
	}
	return ctx.Err()
}

func assetTestJPEG(width, height int) []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, width, height)), nil); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func (r *endpointAssetRunner) SpriteSheetsToPipe(ctx context.Context, in ffmpeg.PipeInput, _, _, _, _, _ int, stdout io.Writer) error {
	if err := r.read(ctx, in); err != nil {
		return err
	}
	_, err := stdout.Write(append(assetTestJPEG(40, 50), assetTestJPEG(40, 50)...))
	return err
}

func (r *endpointAssetRunner) FrameGrabToPipe(ctx context.Context, in ffmpeg.PipeInput, _ float64, _ int, stdout io.Writer) error {
	if err := r.read(ctx, in); err != nil {
		return err
	}
	_, err := stdout.Write(assetTestJPEG(32, 18))
	return err
}

func (r *endpointAssetRunner) SampleClipToPipe(ctx context.Context, in ffmpeg.PipeInput, _ []float64, _ float64, _, _ int, stdout io.Writer) error {
	if err := r.read(ctx, in); err != nil {
		return err
	}
	_, err := io.WriteString(stdout, "\x00\x00\x00\x18ftypisom"+encryptedAssetMarker)
	return err
}

func (r *endpointAssetRunner) WaveformAmplitudesFromPipe(ctx context.Context, in ffmpeg.PipeInput) ([]float64, error) {
	return []float64{0.1, 0.5, 0.25}, r.read(ctx, in)
}

func (r *endpointAssetRunner) SilenceSpansFromPipe(ctx context.Context, in ffmpeg.PipeInput) ([][2]float64, error) {
	return [][2]float64{{1, 2}}, r.read(ctx, in)
}

func (r *endpointAssetRunner) LoudnessMeasureFromPipe(ctx context.Context, in ffmpeg.PipeInput) (float64, float64, float64, error) {
	return -16, 5, -2, r.read(ctx, in)
}

type encryptedAssetsFixture struct {
	handler      *mediahttp.Handler
	assets       *genassets.Service
	manager      *libencrypt.Manager
	runner       *endpointAssetRunner
	videoToken   string
	audioToken   string
	audioID      store.MediaID
	videoFP      string
	audioFP      string
	library      store.LibraryID
	dataDir      string
	generatedDir string
	tempDir      string
}

// newEncryptedAssetsFixture builds generation and the Assets route with the
// production adapters (a real libencrypt manager, the encderiv-backed asset
// generator and the object writer) and only FFmpeg faked. Every directory the
// pipeline can write, including TMPDIR, is test-owned so the plaintext walk
// covers it.
func newEncryptedAssetsFixture(t *testing.T) encryptedAssetsFixture {
	t.Helper()
	const libraryID = store.LibraryID("library-local")
	const videoID = store.MediaID("media-alpha")
	const sessionID = store.SessionID("session-default")
	ctx := context.Background()

	root := t.TempDir()
	tempDir := filepath.Join(root, "tmp")
	dataDir := filepath.Join(root, "data")
	generatedDir := filepath.Join(root, "generated")
	for _, dir := range []string{tempDir, dataDir, generatedDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMPDIR", tempDir)

	st := storetest.Open(t)
	manager := libencrypt.NewManagerWithParams(memSecretStore{}, libencrypt.TestArgon2Params)
	if _, err := manager.Provision(string(libraryID), libencrypt.UnsealModeAuto, ""); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	adapter := appLibEncryptDBCipher{manager: manager}
	encryptSource := func(path string, raw []byte) string {
		t.Helper()
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		fingerprint, err := store.HashMediaFileContent(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := adapter.EncryptObjectFile(string(libraryID), path, path+".enc", fingerprint); err != nil {
			t.Fatalf("EncryptObjectFile: %v", err)
		}
		if err := os.Rename(path+".enc", path); err != nil {
			t.Fatal(err)
		}
		return fingerprint
	}

	videoRaw := []byte("synthetic container bytes for the video source")
	videoPath := filepath.Join(dataDir, "video.bin")
	if err := os.WriteFile(videoPath, videoRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(ctx, videoID, videoPath); err != nil {
		t.Fatalf("SetPrimaryMediaFile: %v", err)
	}
	videoFP := encryptSource(videoPath, videoRaw)
	filter := store.LibraryFilter{Revealed: true, AllLibraries: true}
	if _, err := st.CreateMarker(ctx, store.Marker{MediaID: videoID, Title: "Point", StartSeconds: 12.5}, filter, time.Unix(100, 0).UTC()); err != nil {
		t.Fatal(err)
	}

	audioRaw := []byte("synthetic container bytes for the audio source")
	audioPath := filepath.Join(dataDir, "audio.bin")
	audioFP := encryptSource(audioPath, audioRaw)
	audio, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: libraryID, Path: audioPath, Title: "Encrypted Track", Container: "mp3", Codec: "mp3",
		Class: store.MediaClassAudio, ContentFingerprint: audioFP, DurationSecondsHint: 200,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	// The Library turns Encrypted after its media is in place: the store
	// refuses plain catalog imports into an Encrypted Library.
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}

	sealed := func(id store.LibraryID) bool {
		return manager.Provisioned(string(id)) && manager.Sealed(string(id))
	}
	runner := &endpointAssetRunner{sources: map[string][]byte{"video": videoRaw, "audio": audioRaw}}
	// The wirings mirror the composition root (app.New), with FFmpeg faked.
	assets := genassets.NewService(st, ffmpeg.New(), generatedDir)
	assets.SetSealed(sealed)
	assets.SetEncrypted(func(id store.LibraryID) bool { return id == libraryID })
	assets.SetEncryptedPlane(encryptedAssetGenerator{content: adapter, runner: runner}, adapter)

	authService := auth.NewService(st)
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	scope, err := authService.Scope(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	issue := func(mediaID store.MediaID) string {
		t.Helper()
		token, err := tokens.Issue(ctx, streamtoken.Grant{
			Target: streamtoken.TargetMediaAssets, MediaID: mediaID, DeviceID: scope.DeviceID,
			SessionID: scope.SessionID, ProfileID: scope.ProfileID, ExpiresAt: now.Add(time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	handler := mediahttp.NewHandler(st, authService, tokens, artwork.NewService(st, nil, dataDir), playback.NewSessions(nil, filepath.Join(dataDir, "transcode"), nil))
	handler.SetSealed(sealed)
	handler.SetEncryptedBytes(adapter)
	handler.SetAssets(assets)

	return encryptedAssetsFixture{
		handler: handler, assets: assets, manager: manager, runner: runner,
		videoToken: issue(videoID), audioToken: issue(audio.ID), audioID: audio.ID, videoFP: videoFP, audioFP: audioFP, library: libraryID,
		dataDir: dataDir, generatedDir: generatedDir, tempDir: tempDir,
	}
}

func (f encryptedAssetsFixture) generate(t *testing.T) error {
	t.Helper()
	ctx := context.Background()
	return errors.Join(
		f.assets.GenerateTrickplay(ctx, "media-alpha"),
		f.assets.GeneratePreviewClip(ctx, "media-alpha"),
		f.assets.GenerateMarkerThumbs(ctx, "media-alpha"),
		f.assets.GenerateWaveform(ctx, f.audioID),
		f.assets.GenerateSilenceMap(ctx, f.audioID),
		f.assets.GenerateLoudness(ctx, f.audioID),
	)
}

func (f encryptedAssetsFixture) get(t *testing.T, token, name string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.handler.Assets(rec, httptest.NewRequest(http.MethodGet, "/api/media/assets/"+token+"/"+name, nil))
	return rec
}

// assertNoPlaintextAssets walks dirs: no file may carry the marker, start with
// a JPEG SOI, or hold a JSON document, and every file under generatedDir must
// be a ciphertext object.
func assertNoPlaintextAssets(t *testing.T, generatedDir string, dirs ...string) {
	t.Helper()
	if hits := filesContaining(t, encryptedAssetMarker, dirs...); len(hits) != 0 {
		t.Fatalf("plaintext marker on disk: %v", hits)
	}
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return err
			}
			raw, err := os.ReadFile(path) // #nosec G304 G122 -- test-owned temp-dir walk.
			if err != nil {
				return err
			}
			trimmed := bytes.TrimSpace(raw)
			if bytes.HasPrefix(raw, []byte{0xFF, 0xD8, 0xFF}) {
				t.Errorf("plaintext JPEG on disk: %s", path)
			}
			if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') && json.Valid(trimmed) {
				t.Errorf("plaintext JSON on disk: %s", path)
			}
			// The reference ledger holds empty per-Library markers, not assets.
			if rel, relErr := filepath.Rel(generatedDir, path); relErr == nil && filepath.IsLocal(rel) && !strings.Contains(filepath.ToSlash(rel), ".encrypted-refs/") {
				if ok, _ := libencrypt.HasObjectHeader(bytes.NewReader(raw)); !ok {
					t.Errorf("generated asset is not a ciphertext object: %s", path)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}

func TestEncryptedLibraryAssetsServeDecryptedBytesThroughProductionAdapters(t *testing.T) {
	// CORE-1966: generation runs through encderiv's loopback endpoint and the
	// Assets route serves the decrypted bytes of the ciphertext objects.
	f := newEncryptedAssetsFixture(t)
	if err := f.generate(t); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got := f.runner.calls.Load(); got != 6 {
		t.Fatalf("runner calls = %d, want 6 (one per asset pass)", got)
	}

	rec := f.get(t, f.videoToken, genassets.ManifestName)
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest status = %d body=%q", rec.Code, rec.Body.String())
	}
	var manifest genassets.TrickplayManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil || len(manifest.Sheets) != 2 {
		t.Fatalf("manifest = %+v, err %v", manifest, err)
	}
	for _, name := range append(manifest.Sheets, genassets.MarkerThumbName(12.5)) {
		rec := f.get(t, f.videoToken, name)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d", name, rec.Code)
		}
		if _, err := jpeg.DecodeConfig(bytes.NewReader(rec.Body.Bytes())); err != nil {
			t.Fatalf("%s is not a decrypted JPEG: %v", name, err)
		}
	}
	if rec := f.get(t, f.videoToken, genassets.PreviewClipName); rec.Code != http.StatusOK ||
		!bytes.Contains(rec.Body.Bytes(), []byte(encryptedAssetMarker)) {
		t.Fatalf("preview status = %d body=%q", rec.Code, rec.Body.String())
	}
	for _, name := range []string{genassets.WaveformName, genassets.SilenceMapName, genassets.LoudnessName} {
		rec := f.get(t, f.audioToken, name)
		if rec.Code != http.StatusOK || !json.Valid(rec.Body.Bytes()) {
			t.Fatalf("%s status = %d body=%q", name, rec.Code, rec.Body.String())
		}
	}
	if loudness, ok := f.assets.LoudnessForFingerprint(f.library, f.audioFP); !ok || loudness.IntegratedLUFS != -16 {
		t.Fatalf("LoudnessForFingerprint = %+v, %v", loudness, ok)
	}
	assertNoPlaintextAssets(t, f.generatedDir, f.dataDir, f.generatedDir, f.tempDir)

	f.manager.Seal(string(f.library))
	if rec := f.get(t, f.videoToken, genassets.ManifestName); rec.Code != http.StatusNotFound {
		t.Fatalf("manifest after Seal status = %d, want 404", rec.Code)
	}
}

func TestEncryptedLibraryAssetsRefuseWhenSealed(t *testing.T) {
	f := newEncryptedAssetsFixture(t)
	f.manager.Seal(string(f.library))
	if err := f.generate(t); err != nil {
		t.Fatalf("generate for a Sealed Library returned error: %v", err)
	}
	if got := f.runner.calls.Load(); got != 0 {
		t.Fatalf("runner calls = %d for a Sealed Library, want 0", got)
	}
	if rec := f.get(t, f.videoToken, genassets.ManifestName); rec.Code != http.StatusNotFound {
		t.Fatalf("manifest status = %d, want 404", rec.Code)
	}
	assertNoPlaintextAssets(t, f.generatedDir, f.dataDir, f.generatedDir, f.tempDir)
}

func TestEncryptedLibraryAssetsStopWhenSealedMidGeneration(t *testing.T) {
	f := newEncryptedAssetsFixture(t)
	f.runner.during = func() { f.manager.Seal(string(f.library)) }
	err := f.assets.GeneratePreviewClip(context.Background(), "media-alpha")
	if err == nil {
		t.Fatal("GeneratePreviewClip succeeded after the Library was Sealed mid-run")
	}
	if _, pathErr := f.assets.AssetPath(f.videoFP, genassets.PreviewClipName); pathErr == nil {
		t.Fatal("preview clip published after the Library was Sealed mid-run")
	}
	assertNoPlaintextAssets(t, f.generatedDir, f.dataDir, f.generatedDir, f.tempDir)
}
