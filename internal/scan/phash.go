package scan

import (
	"context"
	"fmt"
	"image"
	_ "image/jpeg" // FrameGrab emits JPEG frames
	"os"
	"path/filepath"
)

// FrameGrabber is scan's slice of the FFmpeg seam for perceptual hashing
// (interface at the consumer, ADR-0016); *ffmpeg.Runner satisfies it — the
// same machinery artwork's poster generation reuses.
type FrameGrabber interface {
	FrameGrab(ctx context.Context, src string, atSeconds float64, width int, dst string) error
}

// phashFrameWidth is the extraction width for the sampled frame — plenty for
// the 9×8 difference grid while keeping the grab cheap.
const phashFrameWidth = 160

// PHash computes a 64-bit perceptual difference hash (dHash) over one frame
// sampled from the middle of the file, as a lowercase 16-hex string
// (ADR-0027 D2 §7 v1: coarse single-frame dHash). Identical content hashes
// identically; the grid is too coarse for sub-second jitter to matter.
func PHash(ctx context.Context, grabber FrameGrabber, path string, durationSeconds int) (string, error) {
	dir, err := os.MkdirTemp("", "beamfall-phash-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	frame := filepath.Join(dir, "frame.jpg")
	if err := grabber.FrameGrab(ctx, path, float64(durationSeconds)/2, phashFrameWidth, frame); err != nil {
		return "", err
	}
	f, err := os.Open(frame)
	if err != nil {
		return "", err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%016x", dHash64(img)), nil
}

// dHash64 is the classic difference hash: average the luminance over a 9×8
// grid of cells, then emit one bit per horizontally adjacent pair (1 when the
// left cell is darker), row-major from the most significant bit.
func dHash64(img image.Image) uint64 {
	const cols, rows = 9, 8
	var cells [rows][cols]float64
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			x0 := bounds.Min.X + col*width/cols
			x1 := bounds.Min.X + (col+1)*width/cols
			y0 := bounds.Min.Y + row*height/rows
			y1 := bounds.Min.Y + (row+1)*height/rows
			var total, count float64
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					r, g, b, _ := img.At(x, y).RGBA()
					// Rec. 601 luma over the 16-bit channel values.
					total += 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
					count++
				}
			}
			if count > 0 {
				cells[row][col] = total / count
			}
		}
	}
	var hash uint64
	for row := 0; row < rows; row++ {
		for col := 0; col < cols-1; col++ {
			hash <<= 1
			if cells[row][col] < cells[row][col+1] {
				hash |= 1
			}
		}
	}
	return hash
}
