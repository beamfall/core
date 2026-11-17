package artwork

import (
	"fmt"
	"image"
	// The stored poster is a JPEG when the scaler normalized it and the
	// imported bytes otherwise, so both decoders are registered.
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/beamfall/core/internal/store"
)

const (
	// dominantColorMaxPixels bounds the poster the colour is read from; a
	// larger image gets no colour rather than a large decode.
	dominantColorMaxPixels = 1 << 24
	// dominantColorSamples is the most pixels sampled along either axis.
	dominantColorSamples = 96
	// noDominantColor is recorded for a poster no colour can be read from.
	noDominantColor = "-"
)

// dominantColorPath is the record of a poster's dominant colour: the poster's
// content version and the colour, so a record left by other poster bytes is
// never used.
func dominantColorPath(poster string) string { return poster + ".color" }

// DominantColor returns the dominant colour of a Media item's poster as a
// lowercase #rrggbb string. ok is false when the item has no poster or the
// poster holds no decodable colour.
func (s *Service) DominantColor(id store.MediaID) (string, bool) {
	return s.dominantColor(s.PosterPath(id))
}

// EntityDominantColor is DominantColor for an Entity's poster.
func (s *Service) EntityDominantColor(id store.EntityID) (string, bool) {
	return s.dominantColor(s.EntityPosterPath(id))
}

// dominantColor reads the colour recorded for the poster's current bytes, and
// derives and records it when there is none: a poster stored before colours
// were recorded, or one written by a path that records nothing.
func (s *Service) dominantColor(poster string) (string, bool) {
	version, ok := s.versions.version(poster)
	if !ok {
		return "", false
	}
	// #nosec G304 -- the record sits beside a poster under the server-owned artwork root.
	if data, err := os.ReadFile(dominantColorPath(poster)); err == nil {
		if recorded, colour, found := strings.Cut(strings.TrimSpace(string(data)), " "); found && recorded == version {
			return colour, colour != noDominantColor
		}
	}
	// A current small bucket shows the same picture and decodes many times
	// faster than the full-size poster.
	colour, ok := "", false
	if small := dominantColorBucket(poster); small != "" {
		colour, ok = dominantColorOf(small)
	}
	if !ok {
		colour, ok = dominantColorOf(poster)
	}
	if !ok {
		colour = noDominantColor
	}
	// A failed write costs only a repeated decode on the next read.
	_ = writeAtomicFile(dominantColorPath(poster), []byte(version+" "+colour+"\n"), nil)
	return colour, ok
}

// dominantColorBucket returns the poster's smallest bucket when one exists
// and is no older than the poster, or "" otherwise.
func dominantColorBucket(poster string) string {
	info, err := os.Stat(poster)
	if err != nil {
		return ""
	}
	ext := filepath.Ext(poster)
	small := strings.TrimSuffix(poster, ext) + ".w320" + ext
	if !derivativeFresh(small, info.ModTime()) {
		return ""
	}
	return small
}

// recordDominantColor records the colour of a poster an import just wrote at
// dst. Other slots carry no colour.
func (s *Service) recordDominantColor(dst, slot string) {
	if slot == SlotPoster {
		_, _ = s.dominantColor(dst)
	}
}

// dominantColorOf decodes the image at path and returns the mean of its most
// common colour, as lowercase #rrggbb. Pixels are sampled on a grid and
// grouped by the top three bits of each channel; pixels that are mostly
// transparent are ignored. ok is false for an undecodable or oversized image
// and for one with no opaque pixels.
func dominantColorOf(path string) (string, bool) {
	// #nosec G304 -- path is a poster under the server-owned artwork root.
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > dominantColorMaxPixels/config.Height {
		return "", false
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", false
	}
	img, _, err := image.Decode(file)
	if err != nil {
		return "", false
	}
	type bin struct{ count, r, g, b uint32 }
	var bins [512]bin
	bounds := img.Bounds()
	step := max(1, (max(bounds.Dx(), bounds.Dy())+dominantColorSamples-1)/dominantColorSamples)
	for y := bounds.Min.Y; y < bounds.Max.Y; y += step {
		for x := bounds.Min.X; x < bounds.Max.X; x += step {
			r, g, b, a := img.At(x, y).RGBA()
			if a < 0x8000 {
				continue
			}
			// RGBA is alpha-premultiplied 16-bit; recover 8-bit straight colour.
			r, g, b = r*0xff/a, g*0xff/a, b*0xff/a
			group := &bins[(r>>5)<<6|(g>>5)<<3|b>>5]
			group.count++
			group.r += r
			group.g += g
			group.b += b
		}
	}
	// The first group with the highest count wins, so ties resolve the same
	// way on every run.
	best := &bins[0]
	for i := range bins {
		if bins[i].count > best.count {
			best = &bins[i]
		}
	}
	if best.count == 0 {
		return "", false
	}
	half := best.count / 2
	return fmt.Sprintf("#%02x%02x%02x", (best.r+half)/best.count, (best.g+half)/best.count, (best.b+half)/best.count), true
}
