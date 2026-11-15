package genassets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/store"
)

// encPlaintextMarker is embedded in every non-image byte stream the fake
// encrypted generator returns, so a disk walk can prove no plaintext copy
// survived anywhere.
const encPlaintextMarker = "beamfall-encrypted-asset-plaintext-marker"

type encMemSecrets map[string]string

func (s encMemSecrets) Secret(ref string) (string, error) {
	value, ok := s[ref]
	if !ok {
		return "", fmt.Errorf("secret %q: %w", ref, fs.ErrNotExist)
	}
	return value, nil
}

func (s encMemSecrets) SetSecret(ref, value string) error {
	if value == "" {
		delete(s, ref)
		return nil
	}
	s[ref] = value
	return nil
}

// managerObjects adapts a real libencrypt.Manager to EncryptedObjects, the way
// the app's DB cipher adapter does.
type managerObjects struct{ m *libencrypt.Manager }

func objectContext(libraryID store.LibraryID, class, id string) libencrypt.ObjectContext {
	return libencrypt.ObjectContext{LibraryID: string(libraryID), ObjectClass: libencrypt.ObjectClass(class), ImmutableObjectID: id}
}

func (o managerObjects) EncryptObject(libraryID store.LibraryID, src io.Reader, ciphertextPath, class, id string) error {
	return o.m.EncryptObjectFrom(string(libraryID), src, ciphertextPath, objectContext(libraryID, class, id))
}

func (o managerObjects) OpenDecryptReader(libraryID store.LibraryID, ciphertextPath, class, id string) (io.ReadSeekCloser, error) {
	return o.m.OpenDecryptReader(string(libraryID), ciphertextPath, objectContext(libraryID, class, id))
}

// fakeEncGen stands in for the shared-helper generator: it checks revocation
// first, exactly as encderiv.RunCapture does, and returns in-memory bytes.
type fakeEncGen struct {
	mu      sync.Mutex
	calls   int
	sources []EncryptedSource
	before  func() // runs before the revocation check (e.g. seals mid-run)
}

func (f *fakeEncGen) start(ctx context.Context, src EncryptedSource) error {
	f.mu.Lock()
	f.calls++
	f.sources = append(f.sources, src)
	before := f.before
	f.mu.Unlock()
	if before != nil {
		before()
	}
	return src.Revoked(ctx)
}

func encJPEG(width, height int) []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, width, height)), nil); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func (f *fakeEncGen) SpriteSheets(ctx context.Context, src EncryptedSource, _, _, _, _, maxSheets int) ([][]byte, error) {
	if err := f.start(ctx, src); err != nil {
		return nil, err
	}
	sheets := [][]byte{encJPEG(40, 50), encJPEG(40, 50)}
	if len(sheets) > maxSheets {
		return nil, errors.New("sheet budget exceeded")
	}
	return sheets, nil
}

func (f *fakeEncGen) SampleClip(ctx context.Context, src EncryptedSource, _ []float64, _ float64, _, _ int) ([]byte, error) {
	if err := f.start(ctx, src); err != nil {
		return nil, err
	}
	return []byte("\x00\x00\x00\x18ftypisom" + encPlaintextMarker), nil
}

func (f *fakeEncGen) FrameGrab(ctx context.Context, src EncryptedSource, _ float64, _ int) ([]byte, error) {
	if err := f.start(ctx, src); err != nil {
		return nil, err
	}
	return encJPEG(32, 18), nil
}

func (f *fakeEncGen) WaveformAmplitudes(ctx context.Context, src EncryptedSource) ([]float64, error) {
	if err := f.start(ctx, src); err != nil {
		return nil, err
	}
	return []float64{0.2, 0.4, 0.1}, nil
}

func (f *fakeEncGen) SilenceSpans(ctx context.Context, src EncryptedSource) ([][2]float64, error) {
	if err := f.start(ctx, src); err != nil {
		return nil, err
	}
	return [][2]float64{{1, 2.5}}, nil
}

func (f *fakeEncGen) LoudnessMeasure(ctx context.Context, src EncryptedSource) (float64, float64, float64, error) {
	if err := f.start(ctx, src); err != nil {
		return 0, 0, 0, err
	}
	return -14, 6, -1, nil
}

type encryptedFixture struct {
	base     string
	svc      *Service
	st       *store.Store
	gen      *fakeGen
	enc      *fakeEncGen
	manager  *libencrypt.Manager
	sealed   map[store.LibraryID]bool
	sealedMu sync.Mutex

	videoLib  store.LibraryID
	videoFP   string
	audio     store.Media
	audioFP   string
	image     store.Media
	imageFP   string
	imagePath string
}

func (f *encryptedFixture) setSealed(libraryID store.LibraryID, sealed bool) {
	f.sealedMu.Lock()
	defer f.sealedMu.Unlock()
	f.sealed[libraryID] = sealed
}

// newEncryptedFixture seeds a video (media-alpha, with a Marker), an audio and
// an image Media in Encrypted Libraries with the encrypted plane wired. Every
// temp directory, including TMPDIR, lives under base so the plaintext walk sees
// all of it.
func newEncryptedFixture(t *testing.T) *encryptedFixture {
	t.Helper()
	base := t.TempDir()
	t.Setenv("TMPDIR", base)
	ctx := context.Background()
	f := &encryptedFixture{base: base, gen: &fakeGen{}, enc: &fakeEncGen{}, sealed: map[store.LibraryID]bool{}}
	f.svc, f.st, f.videoFP = newTestService(t, f.gen)
	video, err := f.st.Media(ctx, "media-alpha", serverFilter)
	if err != nil {
		t.Fatal(err)
	}
	f.videoLib = video.LibraryID
	if _, err := f.st.CreateMarker(ctx, store.Marker{MediaID: "media-alpha", Title: "Point", StartSeconds: 12.5},
		store.LibraryFilter{Revealed: true, AllLibraries: true}, time.Unix(100, 0).UTC()); err != nil {
		t.Fatal(err)
	}

	f.manager = libencrypt.NewManagerWithParams(encMemSecrets{}, libencrypt.TestArgon2Params)
	encrypted := map[store.LibraryID]bool{f.videoLib: true, "library-local": true}
	for lib := range encrypted {
		if _, err := f.manager.Provision(string(lib), libencrypt.UnsealModeAuto, ""); err != nil {
			t.Fatalf("Provision(%s): %v", lib, err)
		}
	}
	f.svc.SetSealed(func(lib store.LibraryID) bool {
		f.sealedMu.Lock()
		defer f.sealedMu.Unlock()
		return f.sealed[lib]
	})
	f.svc.SetEncrypted(func(lib store.LibraryID) bool { return encrypted[lib] })
	f.svc.SetEncryptedPlane(f.enc, managerObjects{m: f.manager})

	track := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(track, []byte("audio source bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.audio, f.audioFP = seedAudioMedia(t, f.st, track, "Encrypted Track")

	f.imagePath = writePNG(t, t.TempDir(), "photo.png", structuredImage(64, 48))
	f.image, f.imageFP = seedImageMedia(t, f.st, f.imagePath, "Encrypted Photo")
	encryptInPlace(t, f.manager, f.image.LibraryID, f.imagePath, f.imageFP)
	return f
}

// encryptInPlace replaces a source file with its media-bytes ciphertext object.
func encryptInPlace(t *testing.T, manager *libencrypt.Manager, libraryID store.LibraryID, path, fingerprint string) {
	t.Helper()
	plain, err := os.Open(path) // #nosec G304 -- test-owned temp path.
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	tmp := path + ".enc"
	obj := libencrypt.ObjectContext{LibraryID: string(libraryID), ObjectClass: libencrypt.ObjectClassMediaBytes, ImmutableObjectID: fingerprint}
	if err := manager.EncryptObjectFrom(string(libraryID), plain, tmp, obj); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func (f *encryptedFixture) generateAll(t *testing.T) error {
	t.Helper()
	ctx := context.Background()
	return errors.Join(
		f.svc.GenerateTrickplay(ctx, "media-alpha"),
		f.svc.GeneratePreviewClip(ctx, "media-alpha"),
		f.svc.GenerateMarkerThumbs(ctx, "media-alpha"),
		f.svc.GenerateWaveform(ctx, f.audio.ID),
		f.svc.GenerateSilenceMap(ctx, f.audio.ID),
		f.svc.GenerateLoudness(ctx, f.audio.ID),
		f.svc.GenerateImageEnrich(ctx, f.image.ID),
	)
}

func (f *encryptedFixture) assertPublicGeneratorUnused(t *testing.T) {
	t.Helper()
	g := f.gen
	if g.spriteCalls+g.clipCalls+len(g.grabTimes)+g.waveformCalls+g.silenceCalls+g.loudnessCalls != 0 {
		t.Fatalf("path-based generator ran for an Encrypted Library: %+v", g)
	}
}

// decryptAsset asserts the published asset is a header-bearing ciphertext
// object and returns its decrypted bytes.
func decryptAsset(t *testing.T, svc *Service, libraryID store.LibraryID, fingerprint, name string) []byte {
	t.Helper()
	path, err := svc.AssetPath(fingerprint, name)
	if err != nil {
		t.Fatalf("AssetPath(%s): %v", name, err)
	}
	file, err := os.Open(path) // #nosec G304 -- path from AssetPath under a test-owned dir.
	if err != nil {
		t.Fatal(err)
	}
	ok, err := libencrypt.HasObjectHeader(file)
	_ = file.Close()
	if err != nil || !ok {
		t.Fatalf("%s is not a ciphertext object (ok=%v, err=%v)", name, ok, err)
	}
	data, err := svc.readAsset(libraryID, fingerprint, name)
	if err != nil {
		t.Fatalf("decrypting %s: %v", name, err)
	}
	return data
}

func assertJPEG(t *testing.T, name string, data []byte) {
	t.Helper()
	if _, err := jpeg.DecodeConfig(bytes.NewReader(data)); err != nil {
		t.Fatalf("%s does not decrypt to a JPEG: %v", name, err)
	}
}

// assertNoPlaintextOnDisk walks every file under base: none may carry the
// plaintext marker, start with a JPEG SOI, or hold a JSON document, and every
// file in the generated-assets tree must be a ciphertext object.
func assertNoPlaintextOnDisk(t *testing.T, base, generatedDir string) {
	t.Helper()
	soi := []byte{0xFF, 0xD8, 0xFF}
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		data, err := os.ReadFile(path) // #nosec G304 G122 -- test-owned temp-dir walk.
		if err != nil {
			return err
		}
		trimmed := bytes.TrimSpace(data)
		switch {
		case bytes.Contains(data, []byte(encPlaintextMarker)):
			t.Errorf("plaintext marker on disk: %s", path)
		case bytes.HasPrefix(data, soi):
			t.Errorf("plaintext JPEG on disk: %s", path)
		case len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') && json.Valid(trimmed):
			t.Errorf("plaintext JSON on disk: %s", path)
		}
		if rel, relErr := filepath.Rel(generatedDir, path); relErr == nil && filepath.IsLocal(rel) {
			if ok, _ := libencrypt.HasObjectHeader(bytes.NewReader(data)); !ok {
				t.Errorf("generated asset is not a ciphertext object: %s", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEncryptedLibraryPublishesCiphertextForEveryKind(t *testing.T) {
	// feature:generated-assets CORE-1966: an Unsealed Encrypted Library gets
	// every Generated Asset, written only as ciphertext objects.
	f := newEncryptedFixture(t)
	if err := f.generateAll(t); err != nil {
		t.Fatalf("generation: %v", err)
	}
	f.assertPublicGeneratorUnused(t)

	var manifest TrickplayManifest
	if err := json.Unmarshal(decryptAsset(t, f.svc, f.videoLib, f.videoFP, ManifestName), &manifest); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if len(manifest.Sheets) != 2 || manifest.TileHeight != 50/SheetRows {
		t.Fatalf("manifest = %+v, want 2 sheets with tile height %d", manifest, 50/SheetRows)
	}
	for _, sheet := range manifest.Sheets {
		assertJPEG(t, sheet, decryptAsset(t, f.svc, f.videoLib, f.videoFP, sheet))
	}
	if clip := decryptAsset(t, f.svc, f.videoLib, f.videoFP, PreviewClipName); !bytes.Contains(clip, []byte(encPlaintextMarker)) {
		t.Fatalf("preview clip decrypted to %q", clip)
	}
	assertJPEG(t, "marker thumb", decryptAsset(t, f.svc, f.videoLib, f.videoFP, MarkerThumbName(12.5)))
	for _, name := range []string{WaveformName, SilenceMapName} {
		if data := decryptAsset(t, f.svc, f.audio.LibraryID, f.audioFP, name); !json.Valid(data) {
			t.Fatalf("%s decrypted to invalid JSON %q", name, data)
		}
	}
	loudness, ok := f.svc.LoudnessForFingerprint(f.audio.LibraryID, f.audioFP)
	if !ok || loudness.IntegratedLUFS != -14 || loudness.Source != LoudnessSourceR128 {
		t.Fatalf("LoudnessForFingerprint = %+v, %v", loudness, ok)
	}
	var enrich ImageEnrich
	if err := json.Unmarshal(decryptAsset(t, f.svc, f.image.LibraryID, f.imageFP, ImageEnrichName), &enrich); err != nil {
		t.Fatalf("image enrich: %v", err)
	}
	if enrich.Flat {
		t.Fatalf("image enrich of the decrypted source is flat: %+v", enrich)
	}
	for _, src := range f.enc.sources {
		if src.Fingerprint == "" || src.Path == "" || src.Revoked == nil {
			t.Fatalf("encrypted source missing identity: %+v", src)
		}
	}

	// A second pass is a no-op: the published ciphertext counts as existing.
	calls := f.enc.calls
	if err := f.generateAll(t); err != nil {
		t.Fatalf("second generation: %v", err)
	}
	if f.enc.calls != calls {
		t.Fatalf("second pass regenerated: calls %d -> %d", calls, f.enc.calls)
	}

	// A Sealed Library's measurement no longer decrypts.
	f.manager.Seal(string(f.audio.LibraryID))
	if _, ok := f.svc.LoudnessForFingerprint(f.audio.LibraryID, f.audioFP); ok {
		t.Fatal("loudness read back after the Library was Sealed")
	}
	assertNoPlaintextOnDisk(t, f.base, f.svc.dir)
}

func TestEncryptedLibrarySealedRefusesGeneration(t *testing.T) {
	f := newEncryptedFixture(t)
	f.setSealed(f.videoLib, true)
	f.setSealed("library-local", true)
	if err := f.generateAll(t); err != nil {
		t.Fatalf("generation for Sealed Libraries returned error: %v", err)
	}
	if f.enc.calls != 0 {
		t.Fatalf("encrypted generator ran %d times for Sealed Libraries", f.enc.calls)
	}
	f.assertPublicGeneratorUnused(t)
	for _, fp := range []string{f.videoFP, f.audioFP, f.imageFP} {
		if entries, err := os.ReadDir(f.svc.assetDir(fp)); err == nil && len(entries) > 0 {
			t.Fatalf("assets published for a Sealed Library: %v", entries)
		}
	}
	assertNoPlaintextOnDisk(t, f.base, f.svc.dir)
}

func TestEncryptedLibrarySealedMidRunRevokes(t *testing.T) {
	f := newEncryptedFixture(t)
	f.enc.before = func() { f.setSealed("library-local", true) }
	err := f.svc.GenerateWaveform(context.Background(), f.audio.ID)
	if !errors.Is(err, ErrLibrarySealed) {
		t.Fatalf("GenerateWaveform sealed mid-run = %v, want ErrLibrarySealed", err)
	}
	if _, err := f.svc.AssetPath(f.audioFP, WaveformName); err == nil {
		t.Fatal("waveform published after the Library was Sealed mid-run")
	}
	assertNoPlaintextOnDisk(t, f.base, f.svc.dir)
}

func TestEncryptedLibraryWithoutPlaneStillRefuses(t *testing.T) {
	f := newEncryptedFixture(t)
	f.svc.SetEncryptedPlane(nil, nil)
	if err := f.generateAll(t); err != nil {
		t.Fatalf("generation without the encrypted plane returned error: %v", err)
	}
	if f.enc.calls != 0 {
		t.Fatalf("encrypted generator ran %d times without the plane", f.enc.calls)
	}
	f.assertPublicGeneratorUnused(t)
	assertNoPlaintextOnDisk(t, f.base, f.svc.dir)
}

func TestEncryptedPlaneLeavesPublicLibrariesOnThePathGenerator(t *testing.T) {
	gen := &fakeGen{waveformSamples: []float64{0.5, 1}}
	svc, st, _ := newTestService(t, gen)
	enc := &fakeEncGen{}
	svc.SetEncrypted(func(store.LibraryID) bool { return false })
	svc.SetEncryptedPlane(enc, managerObjects{m: libencrypt.NewManagerWithParams(encMemSecrets{}, libencrypt.TestArgon2Params)})
	track := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(track, []byte("public audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, fingerprint := seedAudioMedia(t, st, track, "Public Track")
	if err := svc.GenerateWaveform(context.Background(), media.ID); err != nil {
		t.Fatal(err)
	}
	if gen.waveformCalls != 1 || enc.calls != 0 {
		t.Fatalf("waveformCalls = %d, encrypted calls = %d; want 1 and 0", gen.waveformCalls, enc.calls)
	}
	data, err := os.ReadFile(svc.WaveformPath(fingerprint))
	if err != nil || !json.Valid(data) {
		t.Fatalf("public waveform not plaintext JSON: %q, %v", data, err)
	}
}

func TestReconcileEncryptedFingerprintKeepsOnlyCiphertext(t *testing.T) {
	f := newEncryptedFixture(t)
	if err := f.svc.GenerateWaveform(context.Background(), f.audio.ID); err != nil {
		t.Fatal(err)
	}
	dir := f.svc.assetDir(f.audioFP)
	stray := filepath.Join(dir, SilenceMapName)
	if err := os.WriteFile(stray, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	junk := filepath.Join(dir, "unexpected.tmp")
	if err := os.WriteFile(junk, []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ReconcileEncryptedFingerprint(f.audioFP, libencrypt.HasObjectHeader); err != nil {
		t.Fatalf("ReconcileEncryptedFingerprint: %v", err)
	}
	for _, removed := range []string{stray, junk} {
		if _, err := os.Lstat(removed); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s survived reconcile: %v", filepath.Base(removed), err)
		}
	}
	if data := decryptAsset(t, f.svc, f.audio.LibraryID, f.audioFP, WaveformName); !json.Valid(data) {
		t.Fatalf("waveform after reconcile decrypted to %q", data)
	}
	assertNoPlaintextOnDisk(t, f.base, f.svc.dir)
}
