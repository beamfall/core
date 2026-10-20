package scan

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"testing"
)

// gradientImage brightens left to right: every dHash difference bit fires.
func gradientImage() image.Image {
	img := image.NewGray(image.Rect(0, 0, 90, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 90; x++ {
			img.SetGray(x, y, color.Gray{Y: uint8(x * 255 / 89)})
		}
	}
	return img
}

func TestDHash64Gradient(t *testing.T) {
	if got := dHash64(gradientImage()); got != ^uint64(0) {
		t.Fatalf("dHash64(gradient) = %016x, want ffffffffffffffff", got)
	}
}

func TestDHash64Flat(t *testing.T) {
	if got := dHash64(image.NewGray(image.Rect(0, 0, 90, 80))); got != 0 {
		t.Fatalf("dHash64(flat) = %016x, want 0", got)
	}
}

// jpegGrabber stands in for FFmpeg: it encodes a fixed image to dst.
type jpegGrabber struct {
	img       image.Image
	atSeconds float64
	err       error
}

func (g *jpegGrabber) FrameGrab(_ context.Context, _ string, atSeconds float64, _ int, dst string) error {
	if g.err != nil {
		return g.err
	}
	g.atSeconds = atSeconds
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	return jpeg.Encode(f, g.img, nil)
}

func TestPHashSamplesMidFile(t *testing.T) {
	grabber := &jpegGrabber{img: gradientImage()}
	got, err := PHash(context.Background(), grabber, "/media/clip.mkv", 120)
	if err != nil {
		t.Fatalf("PHash returned error: %v", err)
	}
	if got != "ffffffffffffffff" {
		t.Fatalf("PHash = %q, want ffffffffffffffff", got)
	}
	if grabber.atSeconds != 60 {
		t.Fatalf("grab timestamp = %v, want mid-file 60", grabber.atSeconds)
	}
}

func TestPHashGrabFailure(t *testing.T) {
	grabber := &jpegGrabber{err: errors.New("ffmpeg is not available")}
	if _, err := PHash(context.Background(), grabber, "/media/clip.mkv", 120); err == nil {
		t.Fatal("PHash with failing grabber returned nil error")
	}
}
