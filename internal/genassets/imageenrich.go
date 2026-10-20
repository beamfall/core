package genassets

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"  // decode GIF-class stills
	_ "image/jpeg" // decode JPEG stills
	_ "image/png"  // decode PNG stills
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/beamfall/core/internal/store"
)

// Image-enrichment spec: the optional depth map,
// salient region, and palette Generated Assets the image-capable photo Visuals
// consume. Like the beat grid it is the optional, enrichment-produced sibling of
// the still itself — clients degrade gracefully when it is absent (photo Visuals
// fall back to a flat-but-flashy shader-only path: Ken-Burns instead of 2.5D
// parallax, center crop instead of subject framing, theme palette instead of
// photo-driven lighting), so its existence is never a hard gate. Like every
// Generated Asset it is content-fingerprint-keyed (a quality-upgrade replacement
// → new bytes → new fingerprint → new directory regenerates; a metadata edit
// never does) and served only behind Stream Tokens, inheriting the still's
// Reveal contract (Decision 6: no new privacy contract).
//
// *** Generation is a dependency-light PURE-GO image-analysis STAND-IN, not the
// *** production Enrichment Plugin pipeline. ADR-0047 §6 routes the enhanced
// *** layer through the Enrichment Plugin — a monocular depth model
// *** (e.g. MiDaS / Depth-Anything) and a salient-object detector — heavy native
// *** dependencies NOT present in the build/gate environment. This stand-in
// *** derives all three signals from the decoded pixels with no model and no cgo:
// ***   - depth: a coarse luminance + center-bias proxy (brighter/central →
// ***     nearer), downsampled to a small grid. A real MDE backend emits a true
// ***     per-pixel disparity at higher resolution.
// ***   - saliency: a contrast/edge-energy map → the bounding box of the
// ***     high-energy region (subject-aware framing). A real detector emits an
// ***     object mask.
// ***   - palette: the dominant colors by quantized-bucket population (photo
// ***     lighting). This one is close to production already.
// *** It is deterministic and good enough to exercise the Generated Asset
// *** contract, the serving path, and the client flat-vs-enhanced fallback
// *** end-to-end, offline. A future Enrichment Plugin backend swaps in behind the
// *** analyzeImage seam below with NO Core contract change (same asset name, same
// *** JSON shape, same regeneration semantics); bump imageEnrichVersion only if
// *** the wire shape changes. TODO: swap the stand-in analyzer for
// *** the Enrichment Plugin MDE + saliency backend when it lands.
const imageEnrichVersion = 1

// ImageEnrichName is the wire vocabulary asset name for the bundled
// depth/saliency/palette enrichment — added to the closed validAssetName set and
// served by the .json content-type branch exactly like the beat grid.
const ImageEnrichName = "image-enrich.json"

// Analysis grid / palette sizes. The depth grid is deliberately coarse: it is a
// parallax displacement hint, not a render-resolution disparity map, so a small
// grid keeps the asset tiny and the client upsamples. paletteColors caps the
// dominant-color list a photo-driven-lighting Visual reads.
const (
	depthGridCols = 16
	depthGridRows = 16
	paletteColors = 6
	// saliencyMinFraction guards against a near-uniform image reporting a
	// pinhole "subject": a salient box smaller than this fraction of the frame
	// is treated as "no usable subject" and the box falls back to full-frame.
	saliencyMinFraction = 0.04
)

// PaletteColor is one dominant color (0..255 sRGB) plus its population weight in
// [0,1] (fraction of sampled pixels in its bucket). A photo-driven-lighting
// Visual reads the top colors and their weights to tint its lighting.
type PaletteColor struct {
	R      int     `json:"r"`
	G      int     `json:"g"`
	B      int     `json:"b"`
	Weight float64 `json:"weight"`
}

// SalientRegion is the subject-aware framing rect in normalized [0,1] image
// coordinates (origin top-left). A subject-aware Visual frames on this box; the
// flat fallback ignores it and center-crops. Flat==true marks "no usable
// subject found" — the box is the full frame and the client must not over-zoom.
type SalientRegion struct {
	MinX float64 `json:"minX"`
	MinY float64 `json:"minY"`
	MaxX float64 `json:"maxX"`
	MaxY float64 `json:"maxY"`
	Flat bool    `json:"flat"`
}

// ImageEnrich is the JSON document the photo Visuals consume: the version they
// branch on, a coarse row-major depth grid (depthGridRows × depthGridCols,
// values in [0,1] where 1 = nearest), the salient framing region, and the
// dominant palette. A present document with Flat==true and a uniform depth grid
// is the "analysed, no usable structure" marker — distinct from "not yet
// analysed" (absent) — so a client never blocks waiting on enrichment that will
// never improve, and falls back to the shader-only path knowing the still was
// seen.
type ImageEnrich struct {
	Version int            `json:"version"`
	Cols    int            `json:"cols"`
	Rows    int            `json:"rows"`
	Depth   []float64      `json:"depth"` // row-major, len == Cols*Rows, each in [0,1]
	Salient SalientRegion  `json:"salient"`
	Palette []PaletteColor `json:"palette"`
	Flat    bool           `json:"flat"` // true → degenerate image, client uses shader-only fallback
}

// ImageEnrichPath returns where a fingerprint's image-enrichment asset lives
// (whether or not it exists yet).
func (s *Service) ImageEnrichPath(fingerprint string) string {
	return filepath.Join(s.assetDir(fingerprint), ImageEnrichName)
}

// ImageEnrichAsset reports the image-enrichment name when one exists for the
// fingerprint (the enhanced-photo-Visual availability seam — optional,
// ADR-0047 §6).
func (s *Service) ImageEnrichAsset(fingerprint string) (string, bool) {
	if fingerprint == "" || !regularFileExists(s.ImageEnrichPath(fingerprint)) {
		return "", false
	}
	return ImageEnrichName, true
}

// imageSource loads the Media row and primary file when the item is image-class
// with a content fingerprint; ok=false means "nothing to generate", which is not
// an error. The image-class gate is the enrichment asset's analogue of
// videoSource/audioSource — depth/saliency/palette are still-image properties, so
// video- and audio-class Media never get them.
func (s *Service) imageSource(ctx context.Context, id store.MediaID) (store.Media, store.MediaFile, bool, error) {
	media, err := s.store.Media(ctx, id, serverFilter)
	if err != nil {
		return store.Media{}, store.MediaFile{}, false, fmt.Errorf("loading media %s: %w", id, err)
	}
	if media.Class != store.MediaClassImage {
		return store.Media{}, store.MediaFile{}, false, nil
	}
	// Sealed Libraries are skipped entirely (like selectSource): a Sealed Library
	// gets no plaintext enrichment derivative or existence marker.
	if s.sealed != nil && s.sealed(media.LibraryID) {
		return store.Media{}, store.MediaFile{}, false, nil
	}
	// Encrypted Libraries fail closed even while Unsealed: the
	// enrichment doc is a plaintext derivative with no encrypted-plane write path
	// yet, so refuse rather than write it to the shared unencrypted plane.
	if s.encrypted != nil && s.encrypted(media.LibraryID) {
		return store.Media{}, store.MediaFile{}, false, nil
	}
	file, err := s.store.PrimaryMediaFile(ctx, id, serverFilter)
	if err != nil || file.ContentFingerprint == "" {
		return store.Media{}, store.MediaFile{}, false, nil
	}
	return media, file, true, nil
}

// GenerateImageEnrich produces the depth/saliency/palette enrichment asset for
// one image-class Media item's primary file. Like every other Generated Asset it
// is keyed by content fingerprint, written last and atomically — it is the
// publication marker, so a present file is the "analysed" marker (Flat may be
// true) and an absent file is "not yet analysed" (or never will be, for a
// non-image item).
//
// Decoding happens in pure Go (no FFmpeg seam): depth/saliency/palette are
// derived from the still's pixels directly. An image that fails to decode (a
// corrupt or non-image file mis-classified as image) publishes a FLAT asset
// rather than erroring — the still was seen, it just yields no usable structure,
// and the client uses the shader-only fallback.
func (s *Service) GenerateImageEnrich(ctx context.Context, id store.MediaID) error {
	_, file, ok, err := s.imageSource(ctx, id)
	if err != nil || !ok {
		return err
	}
	dst := s.ImageEnrichPath(file.ContentFingerprint)
	if regularFileExists(dst) {
		return nil
	}

	doc := analyzeImageFile(file.Path)
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if err := s.publishBytes(string(id), file.ContentFingerprint, "image-enrich:v1", ImageEnrichName, data); err != nil {
		return fmt.Errorf("publishing image enrichment for %s: %w", id, err)
	}
	return nil
}

// flatImageEnrich is the degenerate "analysed, no usable structure" document:
// a uniform mid-depth grid, a full-frame salient region, and an empty palette.
// It is what a Visual gets when the still cannot be decoded or carries no useful
// contrast — the flat-but-flashy fallback marker.
func flatImageEnrich() ImageEnrich {
	depth := make([]float64, depthGridCols*depthGridRows)
	for i := range depth {
		depth[i] = 0.5
	}
	return ImageEnrich{
		Version: imageEnrichVersion,
		Cols:    depthGridCols,
		Rows:    depthGridRows,
		Depth:   depth,
		Salient: SalientRegion{MinX: 0, MinY: 0, MaxX: 1, MaxY: 1, Flat: true},
		Palette: []PaletteColor{},
		Flat:    true,
	}
}

// analyzeImageFile decodes the still at path and runs the stand-in analysis. A
// decode failure (corrupt or non-image bytes) yields the flat document — the
// still was seen, it just yields no structure, so the client takes the
// shader-only fallback. This never returns an error: a present asset is the
// "analysed" marker, and "analysed but flat" is a valid outcome.
// maxEnrichPixels caps the decoded raster (width×height) before the full
// image.Decode: a crafted small file declaring huge dimensions (decompression
// bomb) would otherwise allocate the whole raster and can OOM the server.
// 1<<26 (~67 MP) clears any real still camera with room to spare.
const maxEnrichPixels = 1 << 26

func analyzeImageFile(path string) ImageEnrich {
	f, err := os.Open(path)
	if err != nil {
		return flatImageEnrich()
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return flatImageEnrich()
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxEnrichPixels {
		return flatImageEnrich()
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return flatImageEnrich()
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return flatImageEnrich()
	}
	return analyzeImage(img)
}

// analyzeImage is the pure-Go stand-in: it samples the decoded image once into a
// luminance/edge grid plus a color histogram, then derives the three signals.
// Deterministic: identical pixels → identical document. A degenerate image
// (zero area, or no contrast anywhere) returns the flat document.
func analyzeImage(img image.Image) ImageEnrich {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return flatImageEnrich()
	}

	// One coarse luminance grid drives both depth and saliency. We average the
	// source pixels falling in each cell (a box downsample) so the analysis cost
	// is bounded by the grid size, not the image resolution.
	cols, rows := depthGridCols, depthGridRows
	lum := make([]float64, cols*rows)
	count := make([]int, cols*rows)

	// Color histogram for the palette: 4 bits per channel (4096 buckets) keeps
	// the count small while distinguishing the dominant tones.
	const palBits = 4
	const palLevels = 1 << palBits // 16 per channel
	hist := map[int]int{}
	histTotal := 0

	for y := b.Min.Y; y < b.Max.Y; y++ {
		gy := (y - b.Min.Y) * rows / h
		if gy >= rows {
			gy = rows - 1
		}
		for x := b.Min.X; x < b.Max.X; x++ {
			r16, g16, b16, _ := img.At(x, y).RGBA() // 0..65535
			r := r16 >> 8
			g := g16 >> 8
			bl := b16 >> 8
			l := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl) // 0..255

			gx := (x - b.Min.X) * cols / w
			if gx >= cols {
				gx = cols - 1
			}
			idx := gy*cols + gx
			lum[idx] += l
			count[idx]++

			rb := int(r) >> (8 - palBits)
			gb := int(g) >> (8 - palBits)
			bb := int(bl) >> (8 - palBits)
			key := (rb*palLevels+gb)*palLevels + bb
			hist[key]++
			histTotal++
		}
	}

	for i := range lum {
		if count[i] > 0 {
			lum[i] /= float64(count[i])
		}
	}

	depth := depthFromLuminance(lum, cols, rows)
	salient := salientFromLuminance(lum, cols, rows)
	palette := paletteFromHistogram(hist, histTotal, palBits)

	flat := salient.Flat && len(palette) == 0
	return ImageEnrich{
		Version: imageEnrichVersion,
		Cols:    cols,
		Rows:    rows,
		Depth:   depth,
		Salient: salient,
		Palette: palette,
		Flat:    flat,
	}
}

// depthFromLuminance builds the coarse depth proxy: brighter and more-central
// cells read as nearer. The cell luminance is normalized to [0,1] across the
// grid, then blended with a radial center bias (subjects tend to sit center and
// catch the light). A uniform-luminance image yields a pure center-bias bowl,
// which is still a usable (if weak) parallax hint.
func depthFromLuminance(lum []float64, cols, rows int) []float64 {
	out := make([]float64, len(lum))
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, v := range lum {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	span := hi - lo
	cx, cy := float64(cols-1)/2, float64(rows-1)/2
	maxR := math.Hypot(cx, cy)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			i := r*cols + c
			var ln float64
			if span > 1e-6 {
				ln = (lum[i] - lo) / span
			} else {
				ln = 0.5
			}
			dist := math.Hypot(float64(c)-cx, float64(r)-cy) / maxR
			center := 1 - dist // 1 at center, 0 at corners
			d := 0.6*ln + 0.4*center
			out[i] = math.Max(0, math.Min(1, d))
		}
	}
	return out
}

// salientFromLuminance derives the subject framing box from local contrast
// (edge energy = sum of |neighbor differences| per cell). The salient region is
// the tight bounding box of the cells whose energy is above the grid mean by a
// margin; if too little energy stands out (a flat or uniform image) the box
// falls back to the full frame with Flat=true. Coordinates are normalized cell
// boundaries in [0,1].
func salientFromLuminance(lum []float64, cols, rows int) SalientRegion {
	energy := make([]float64, len(lum))
	var sum float64
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			i := r*cols + c
			var e float64
			if c > 0 {
				e += math.Abs(lum[i] - lum[i-1])
			}
			if c < cols-1 {
				e += math.Abs(lum[i] - lum[i+1])
			}
			if r > 0 {
				e += math.Abs(lum[i] - lum[i-cols])
			}
			if r < rows-1 {
				e += math.Abs(lum[i] - lum[i+cols])
			}
			energy[i] = e
			sum += e
		}
	}
	mean := sum / float64(len(energy))
	// A cell is "salient" if its edge energy is clearly above the grid mean.
	thresh := mean * 1.5

	minC, minR, maxC, maxR := cols, rows, -1, -1
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			if energy[r*cols+c] > thresh {
				if c < minC {
					minC = c
				}
				if c > maxC {
					maxC = c
				}
				if r < minR {
					minR = r
				}
				if r > maxR {
					maxR = r
				}
			}
		}
	}

	full := SalientRegion{MinX: 0, MinY: 0, MaxX: 1, MaxY: 1, Flat: true}
	if maxC < minC || maxR < minR || mean < 1e-6 {
		return full
	}
	// Normalized box (inclusive cell range → boundary coords).
	minX := float64(minC) / float64(cols)
	minY := float64(minR) / float64(rows)
	maxX := float64(maxC+1) / float64(cols)
	maxY := float64(maxR+1) / float64(rows)
	if (maxX-minX)*(maxY-minY) < saliencyMinFraction {
		// Subject too small to frame on — treat as no usable subject.
		return full
	}
	return SalientRegion{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY, Flat: false}
}

// paletteFromHistogram returns the top dominant colors by bucket population,
// each as the bucket center in 0..255 sRGB plus its population weight in [0,1].
// An empty histogram (zero-area image) yields an empty palette.
func paletteFromHistogram(hist map[int]int, total, palBits int) []PaletteColor {
	if total == 0 || len(hist) == 0 {
		return []PaletteColor{}
	}
	type bucket struct {
		key, n int
	}
	buckets := make([]bucket, 0, len(hist))
	for k, n := range hist {
		buckets = append(buckets, bucket{k, n})
	}
	// Population desc, key asc as a deterministic tiebreak.
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].n != buckets[j].n {
			return buckets[i].n > buckets[j].n
		}
		return buckets[i].key < buckets[j].key
	})

	levels := 1 << palBits
	// Reconstruct the bucket center in 0..255: bucket index * cell + half cell.
	cell := 256 / levels
	half := cell / 2
	n := paletteColors
	if n > len(buckets) {
		n = len(buckets)
	}
	out := make([]PaletteColor, 0, n)
	for i := 0; i < n; i++ {
		key := buckets[i].key
		bb := key % levels
		gb := (key / levels) % levels
		rb := key / (levels * levels)
		out = append(out, PaletteColor{
			R:      rb*cell + half,
			G:      gb*cell + half,
			B:      bb*cell + half,
			Weight: math.Round(float64(buckets[i].n)/float64(total)*1000) / 1000,
		})
	}
	return out
}
