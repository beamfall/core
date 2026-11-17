package ffmpeg

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestScaleImageKeepsAlphaForPNGDestination runs the real binary: a destination
// ending in .png is an RGBA PNG whose transparent pixels stay transparent, and
// any other destination is the same JPEG as before. Skipped without ffmpeg.
func TestScaleImageKeepsAlphaForPNGDestination(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed; skipping real-binary image scale test")
	}
	dir := t.TempDir()
	// Left half transparent, right half opaque red.
	src := image.NewNRGBA(image.Rect(0, 0, 64, 32))
	for y := range 32 {
		for x := 32; x < 64; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	// The artwork importer hands the scaler a source with no image extension.
	srcPath := filepath.Join(dir, "logo.png.import.tmp")
	if err := os.WriteFile(srcPath, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := New()

	pngPath := filepath.Join(dir, "logo.w320.png")
	if err := runner.ScaleImage(context.Background(), srcPath, 32, pngPath); err != nil {
		t.Fatalf("ScaleImage to .png: %v", err)
	}
	data, err := os.ReadFile(pngPath) // #nosec G304 -- scaled output under this test's t.TempDir().
	if err != nil {
		t.Fatal(err)
	}
	scaled, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("a .png destination is not a PNG: %v", err)
	}
	if got := scaled.Bounds().Dx(); got != 32 {
		t.Fatalf("scaled width = %d, want 32", got)
	}
	if _, _, _, a := scaled.At(2, 8).RGBA(); a != 0 {
		t.Fatalf("transparent source pixel has alpha %#x after scaling, want 0", a)
	}
	if r, _, _, a := scaled.At(29, 8).RGBA(); a != 0xffff || r != 0xffff {
		t.Fatalf("opaque red source pixel = r %#x a %#x after scaling", r, a)
	}

	jpgPath := filepath.Join(dir, "poster.w320.jpg")
	if err := runner.ScaleImage(context.Background(), srcPath, 32, jpgPath); err != nil {
		t.Fatalf("ScaleImage to .jpg: %v", err)
	}
	data, err = os.ReadFile(jpgPath) // #nosec G304 -- scaled output under this test's t.TempDir().
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("a .jpg destination is not a JPEG: %v", err)
	}
}
