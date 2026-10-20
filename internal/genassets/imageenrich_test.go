package genassets

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// seedImageMedia imports an image-class Media item backed by a real file at
// path, returning the item and its content fingerprint. Editing the bytes at
// path and re-importing yields a fresh fingerprint — the regeneration key.
func seedImageMedia(t *testing.T, st *store.Store, path, title string) (store.Media, string) {
	t.Helper()
	fingerprint, err := store.HashMediaFileContent(path)
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              title,
		Container:          "png",
		Codec:              "png",
		Class:              store.MediaClassImage,
		ContentFingerprint: fingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	return media, fingerprint
}

// writePNG renders img to a PNG file and returns its path.
func writePNG(t *testing.T, dir, name string, img image.Image) string {
	t.Helper()
	p := filepath.Join(dir, name)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// structuredImage builds a deterministic image with a clear bright subject (a
// centered square) on a dark background — strong center contrast, so the
// analyzer should report a non-flat salient box, a depth bias toward the bright
// center, and a palette with both tones.
func structuredImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{10, 12, 20, 255} // dark blue background
			if x > w/4 && x < 3*w/4 && y > h/4 && y < 3*h/4 {
				c = color.RGBA{240, 200, 60, 255} // bright warm subject
			}
			img.Set(x, y, c)
		}
	}
	return img
}

func TestGenerateImageEnrichProducesStructuredAssetAndIsServable(t *testing.T) {
	// feature:image-enrich feature:generated-assets feature:visuals-photo
	svc, st, _ := newTestService(t, &fakeGen{})
	path := writePNG(t, t.TempDir(), "still.png", structuredImage(64, 64))
	media, fingerprint := seedImageMedia(t, st, path, "Photo One")

	if err := svc.GenerateImageEnrich(context.Background(), media.ID); err != nil {
		t.Fatalf("GenerateImageEnrich returned error: %v", err)
	}

	data, err := os.ReadFile(svc.ImageEnrichPath(fingerprint))
	if err != nil {
		t.Fatalf("image enrichment missing: %v", err)
	}
	var doc ImageEnrich
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("image enrichment is not JSON: %v", err)
	}
	if doc.Version != imageEnrichVersion {
		t.Fatalf("version = %d, want %d", doc.Version, imageEnrichVersion)
	}
	if doc.Cols != depthGridCols || doc.Rows != depthGridRows {
		t.Fatalf("grid = %dx%d, want %dx%d", doc.Cols, doc.Rows, depthGridCols, depthGridRows)
	}
	if len(doc.Depth) != doc.Cols*doc.Rows {
		t.Fatalf("depth len = %d, want %d", len(doc.Depth), doc.Cols*doc.Rows)
	}
	for i, d := range doc.Depth {
		if d < 0 || d > 1 {
			t.Fatalf("depth[%d] = %v outside [0,1]", i, d)
		}
	}
	if doc.Flat {
		t.Fatalf("structured image reported Flat — expected usable structure")
	}
	if doc.Salient.Flat {
		t.Fatalf("centered subject yielded no salient region")
	}
	// The salient box should be interior (not full-frame) for a centered subject.
	if doc.Salient.MinX <= 0 && doc.Salient.MaxX >= 1 && doc.Salient.MinY <= 0 && doc.Salient.MaxY >= 1 {
		t.Fatalf("salient box is full-frame for a centered subject: %+v", doc.Salient)
	}
	if len(doc.Palette) == 0 {
		t.Fatalf("palette is empty for a two-tone image")
	}
	for _, c := range doc.Palette {
		if c.R < 0 || c.R > 255 || c.G < 0 || c.G > 255 || c.B < 0 || c.B > 255 {
			t.Fatalf("palette color out of range: %+v", c)
		}
		if c.Weight < 0 || c.Weight > 1 {
			t.Fatalf("palette weight %v outside [0,1]", c.Weight)
		}
	}

	// Center depth should read nearer than a corner (bright + central bias).
	center := doc.Depth[(depthGridRows/2)*depthGridCols+depthGridCols/2]
	corner := doc.Depth[0]
	if center <= corner {
		t.Fatalf("center depth %v not greater than corner %v (subject should read nearer)", center, corner)
	}

	// Servable through the same fingerprint-keyed route as every other asset.
	if name, ok := svc.ImageEnrichAsset(fingerprint); !ok || name != ImageEnrichName {
		t.Fatalf("ImageEnrichAsset = (%q, %v), want (%q, true)", name, ok, ImageEnrichName)
	}
	if _, err := svc.AssetPath(fingerprint, ImageEnrichName); err != nil {
		t.Fatalf("image enrichment not servable: %v", err)
	}
}

// A uniform image has no contrast and no usable subject — the asset must still
// be PRESENT (analysed marker) but FLAT, so the client takes the shader-only
// fallback rather than blocking on enrichment that will never improve.
func TestGenerateImageEnrichFlatOnUniformImage(t *testing.T) {
	// feature:image-enrich feature:visuals-photo
	svc, st, _ := newTestService(t, &fakeGen{})
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.RGBA{80, 80, 80, 255})
		}
	}
	path := writePNG(t, t.TempDir(), "flat.png", img)
	media, fingerprint := seedImageMedia(t, st, path, "Flat Photo")

	if err := svc.GenerateImageEnrich(context.Background(), media.ID); err != nil {
		t.Fatalf("GenerateImageEnrich returned error: %v", err)
	}
	data, err := os.ReadFile(svc.ImageEnrichPath(fingerprint))
	if err != nil {
		t.Fatalf("uniform image must still publish an asset (analysed marker): %v", err)
	}
	var doc ImageEnrich
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Salient.Flat {
		t.Fatalf("uniform image reported a salient subject: %+v", doc.Salient)
	}
	if doc.Salient.MinX != 0 || doc.Salient.MinY != 0 || doc.Salient.MaxX != 1 || doc.Salient.MaxY != 1 {
		t.Fatalf("flat salient region must be full-frame, got %+v", doc.Salient)
	}
}

// A file mis-classified as image that does not decode must NOT error: the asset
// is published FLAT (the still was seen, it yields no structure) so the client
// falls back gracefully.
func TestGenerateImageEnrichFlatOnUndecodableFile(t *testing.T) {
	// feature:image-enrich feature:visuals-photo
	svc, st, _ := newTestService(t, &fakeGen{})
	path := filepath.Join(t.TempDir(), "broken.png")
	if err := os.WriteFile(path, []byte("not really an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, fingerprint := seedImageMedia(t, st, path, "Broken Photo")

	if err := svc.GenerateImageEnrich(context.Background(), media.ID); err != nil {
		t.Fatalf("undecodable image must publish FLAT, not error: %v", err)
	}
	data, err := os.ReadFile(svc.ImageEnrichPath(fingerprint))
	if err != nil {
		t.Fatalf("undecodable image must still publish a FLAT asset: %v", err)
	}
	var doc ImageEnrich
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Flat {
		t.Fatalf("undecodable image must be Flat=true, got %+v", doc)
	}
	if len(doc.Depth) != depthGridCols*depthGridRows {
		t.Fatalf("flat asset must still carry a full depth grid, len=%d", len(doc.Depth))
	}
}

func TestGenerateImageEnrichRegeneratesOnBytesNotMetadata(t *testing.T) {
	// feature:image-enrich feature:generated-assets
	svc, st, _ := newTestService(t, &fakeGen{})
	dir := t.TempDir()
	path := writePNG(t, dir, "v1.png", structuredImage(48, 48))
	media, fp1 := seedImageMedia(t, st, path, "Photo")

	if err := svc.GenerateImageEnrich(context.Background(), media.ID); err != nil {
		t.Fatal(err)
	}
	if !regularFileExists(svc.ImageEnrichPath(fp1)) {
		t.Fatal("first generation did not publish")
	}

	// Same bytes, re-run → no-op (idempotent on the same fingerprint).
	stat1, _ := os.Stat(svc.ImageEnrichPath(fp1))
	if err := svc.GenerateImageEnrich(context.Background(), media.ID); err != nil {
		t.Fatal(err)
	}
	stat2, _ := os.Stat(svc.ImageEnrichPath(fp1))
	if !stat1.ModTime().Equal(stat2.ModTime()) {
		t.Fatal("re-run on the same fingerprint regenerated (should be a no-op)")
	}

	// New bytes → new fingerprint → fresh asset directory.
	if err := os.WriteFile(path, mustPNGBytes(t, structuredImage(50, 50)), 0o600); err != nil {
		t.Fatal(err)
	}
	media2, fp2 := seedImageMedia(t, st, path, "Photo Upgraded")
	if fp2 == fp1 {
		t.Fatal("expected a new fingerprint after a byte change")
	}
	if err := svc.GenerateImageEnrich(context.Background(), media2.ID); err != nil {
		t.Fatal(err)
	}
	if !regularFileExists(svc.ImageEnrichPath(fp2)) {
		t.Fatal("byte-changed image did not regenerate under the new fingerprint")
	}
}

// Image enrichment is image-class only: audio/video Media never get one.
func TestGenerateImageEnrichGatedToImageClass(t *testing.T) {
	// feature:image-enrich
	svc, st, _ := newTestService(t, &fakeGen{})
	track := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(track, []byte("audio bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, fingerprint := seedAudioMedia(t, st, track, "Track")
	if err := svc.GenerateImageEnrich(context.Background(), media.ID); err != nil {
		t.Fatalf("GenerateImageEnrich returned error on audio media: %v", err)
	}
	if regularFileExists(svc.ImageEnrichPath(fingerprint)) {
		t.Fatal("audio-class media got an image-enrichment asset")
	}
}

// TestGenerateImageEnrichSkipsSealedLibrary: a Sealed Library yields no
// plaintext enrichment derivative or existence marker (ENCPRIV-10b-LIVE / ADR-0098
// area 2). imageSource short-circuits before any decode or write.
func TestGenerateImageEnrichSkipsSealedLibrary(t *testing.T) {
	// feature:image-enrich
	svc, st, _ := newTestService(t, &fakeGen{})
	svc.SetSealed(func(libraryID store.LibraryID) bool { return libraryID == "library-local" })
	path := writePNG(t, t.TempDir(), "sealed.png", structuredImage(40, 40))
	media, fingerprint := seedImageMedia(t, st, path, "Sealed Photo") // library-local

	if err := svc.GenerateImageEnrich(context.Background(), media.ID); err != nil {
		t.Fatalf("GenerateImageEnrich on sealed-Library media returned error: %v", err)
	}
	if regularFileExists(svc.ImageEnrichPath(fingerprint)) {
		t.Fatal("sealed-Library media got a plaintext image-enrichment derivative")
	}
	if _, ok := svc.ImageEnrichAsset(fingerprint); ok {
		t.Fatal("sealed-Library media got an image-enrichment existence marker")
	}
}

func TestGenerateImageEnrichSkipsEncryptedUnsealedLibrary(t *testing.T) {
	// feature:image-enrich — ENCPRIV-10b: an Encrypted Library that is currently
	// Unsealed (sealed predicate returns false) still gets no plaintext derivative.
	svc, st, _ := newTestService(t, &fakeGen{})
	svc.SetSealed(func(store.LibraryID) bool { return false })
	svc.SetEncrypted(func(libraryID store.LibraryID) bool { return libraryID == "library-local" })
	path := writePNG(t, t.TempDir(), "encrypted.png", structuredImage(40, 40))
	media, fingerprint := seedImageMedia(t, st, path, "Encrypted Photo") // library-local

	if err := svc.GenerateImageEnrich(context.Background(), media.ID); err != nil {
		t.Fatalf("GenerateImageEnrich on encrypted-Library media returned error: %v", err)
	}
	if regularFileExists(svc.ImageEnrichPath(fingerprint)) {
		t.Fatal("Unsealed encrypted-Library media got a plaintext image-enrichment derivative")
	}
	if _, ok := svc.ImageEnrichAsset(fingerprint); ok {
		t.Fatal("Unsealed encrypted-Library media got an image-enrichment existence marker")
	}
}

// analyzeImage is deterministic: identical pixels → identical document.
func TestAnalyzeImageIsDeterministic(t *testing.T) {
	img := structuredImage(40, 40)
	a := analyzeImage(img)
	b := analyzeImage(img)
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if !bytes.Equal(ja, jb) {
		t.Fatalf("analyzeImage non-deterministic:\n%s\n%s", ja, jb)
	}
}

// Sanity: a JPEG decodes through the same path (the asset is format-agnostic).
func TestAnalyzeImageFileHandlesJPEG(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "still.jpg")
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, structuredImage(64, 64), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	doc := analyzeImageFile(p)
	if doc.Flat {
		t.Fatalf("structured JPEG analysed as Flat")
	}
}

func mustPNGBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
