package artwork

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"os"
	"slices"

	"github.com/beamfall/core/internal/store"
)

const (
	// mosaicTiles is how many distinct source posters a mosaic needs; fewer
	// fall back to a copy of the first.
	mosaicTiles = 4
	// mosaicMaxCandidates bounds the source posters one Entity's mosaic
	// examines per source, so an Entity whose many members share a few
	// posters costs a bounded number of stats.
	mosaicMaxCandidates = 4 * entityPosterSourcePage
	// mosaicCellWidth and mosaicCellHeight are one tile, a 2:3 poster; the
	// grid is two by two, so the mosaic is a 2:3 poster too.
	mosaicCellWidth  = 400
	mosaicCellHeight = 600
	// mosaicGutter is the dark rule between tiles, in pixels.
	mosaicGutter = 4
	// mosaicSampleGrid is the most source pixels averaged along each axis for
	// one mosaic pixel.
	mosaicSampleGrid = 4
	mosaicQuality    = 85
	// mosaicTileBucket is the derivative a tile is decoded from: no smaller
	// than a cell, so a tile is never upscaled from a small bucket.
	mosaicTileBucket = "w400"
)

var mosaicGutterColor = color.RGBA{R: 0x11, G: 0x11, B: 0x11, A: 0xff}

// mosaicTile is one distinct source poster chosen for a mosaic.
type mosaicTile struct {
	candidate posterCandidate
	info      os.FileInfo
}

// inheritMosaicPoster brings an Entity's inherited mosaic in line with its
// sources. handled is false when the sources offer fewer than mosaicTiles
// distinct posters, or a tile cannot be decoded; the caller then inherits a
// single poster instead. A mosaic whose recorded tiles are unchanged is left
// alone, so a steady-state sweep costs relation reads and stats, never a
// decode.
func (s *Service) inheritMosaicPoster(ctx context.Context, ref store.EntityRef, rule EntityPosterRule, hasPoster bool, recorded inheritedSource) (wrote, handled bool, err error) {
	tiles, err := s.mosaicTiles(ctx, ref.ID, rule)
	if err != nil || len(tiles) < mosaicTiles {
		return false, false, err
	}
	record := inheritedSource{Tiles: make([]inheritedSource, len(tiles))}
	for i, tile := range tiles {
		record.Tiles[i] = inheritedSource{
			Source:    s.sourceName(tile.candidate.path),
			Size:      tile.info.Size(),
			ModTimeNS: tile.info.ModTime().UnixNano(),
		}
	}
	if hasPoster && slices.EqualFunc(record.Tiles, recorded.Tiles, sameSource) {
		return false, true, nil
	}
	data, ok := s.composeMosaic(ctx, tiles)
	if !ok {
		return false, false, nil
	}
	dst := s.EntityPosterPath(ref.ID)
	wrote, err = s.publishInheritedPoster(ctx, dst, data, record)
	if err != nil || !wrote {
		return false, true, err
	}
	for bucket := range bucketWidths {
		if ctx.Err() != nil {
			break
		}
		_, _ = s.EntityPosterDerivative(ctx, ref.ID, bucket)
	}
	return true, true, nil
}

// sameSource reports whether two recorded tiles name the same unchanged
// source poster.
func sameSource(a, b inheritedSource) bool {
	return a.Source == b.Source && a.Size == b.Size && a.ModTimeNS == b.ModTimeNS
}

// mosaicTiles returns up to mosaicTiles usable source posters in source
// order, skipping a poster the same size as one already chosen: members that
// share one picture (copies of one container's poster) store byte-identical
// files, and two different covers of exactly the same size are rare enough
// that losing one costs a tile, never a wrong picture.
func (s *Service) mosaicTiles(ctx context.Context, id store.EntityID, rule EntityPosterRule) ([]mosaicTile, error) {
	var tiles []mosaicTile
	sizes := map[int64]bool{}
	for _, source := range rule.Sources {
		for offset := 0; offset < mosaicMaxCandidates; offset += entityPosterSourcePage {
			page, err := s.posterCandidates(ctx, id, source, offset)
			if err != nil {
				return nil, err
			}
			for _, candidate := range page {
				info, ok := candidate.usable()
				if !ok || sizes[info.Size()] {
					continue
				}
				sizes[info.Size()] = true
				tiles = append(tiles, mosaicTile{candidate: candidate, info: info})
				if len(tiles) == mosaicTiles {
					return tiles, nil
				}
			}
			if len(page) < entityPosterSourcePage {
				break
			}
		}
	}
	return tiles, nil
}

// composeMosaic draws the tiles two by two into one JPEG poster, each tile
// scaled to cover its cell and centred. ok is false when a tile cannot be
// decoded.
func (s *Service) composeMosaic(ctx context.Context, tiles []mosaicTile) ([]byte, bool) {
	canvas := image.NewRGBA(image.Rect(0, 0, 2*mosaicCellWidth, 2*mosaicCellHeight))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(mosaicGutterColor), image.Point{}, draw.Src)
	half := mosaicGutter / 2
	for i, tile := range tiles {
		path, err := s.derivative(ctx, tile.candidate.path, "", SlotPoster, mosaicTileBucket)
		if err != nil {
			return nil, false
		}
		img, ok := decodeBoundedImage(path)
		if !ok {
			return nil, false
		}
		col, row := i%2, i/2
		cell := image.Rect(col*mosaicCellWidth, row*mosaicCellHeight, (col+1)*mosaicCellWidth, (row+1)*mosaicCellHeight)
		// Inset each cell on its inner edges only, so the gutter sits
		// between tiles and the mosaic runs to the poster's edges.
		if col == 0 {
			cell.Max.X -= half
		} else {
			cell.Min.X += half
		}
		if row == 0 {
			cell.Max.Y -= half
		} else {
			cell.Min.Y += half
		}
		drawCover(canvas, cell, img)
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, canvas, &jpeg.Options{Quality: mosaicQuality}); err != nil {
		return nil, false
	}
	return out.Bytes(), true
}

// decodeBoundedImage decodes the image at path unless it is larger than the
// dominant-colour reader accepts.
func decodeBoundedImage(path string) (image.Image, bool) {
	// #nosec G304 -- path is a poster or its derivative under the artwork root.
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > dominantColorMaxPixels/config.Height {
		return nil, false
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, false
	}
	img, _, err := image.Decode(file)
	return img, err == nil
}

// drawCover scales src to cover cell, cropping the overflow evenly, and
// draws it there. Each destination pixel averages a grid of up to
// mosaicSampleGrid² source pixels from the area it covers.
func drawCover(dst *image.RGBA, cell image.Rectangle, src image.Image) {
	bounds := src.Bounds()
	sw, sh := float64(bounds.Dx()), float64(bounds.Dy())
	cw, ch := float64(cell.Dx()), float64(cell.Dy())
	scale := max(cw/sw, ch/sh)
	// The part of src that maps onto the cell, centred.
	visW, visH := cw/scale, ch/scale
	x0 := float64(bounds.Min.X) + (sw-visW)/2
	y0 := float64(bounds.Min.Y) + (sh-visH)/2
	step := 1 / scale
	samples := min(mosaicSampleGrid, max(1, int(step)))
	for y := cell.Min.Y; y < cell.Max.Y; y++ {
		sy := y0 + float64(y-cell.Min.Y)*step
		for x := cell.Min.X; x < cell.Max.X; x++ {
			sx := x0 + float64(x-cell.Min.X)*step
			var r, g, b, n uint32
			for j := range samples {
				py := min(bounds.Max.Y-1, int(sy+(float64(j)+0.5)*step/float64(samples)))
				for i := range samples {
					px := min(bounds.Max.X-1, int(sx+(float64(i)+0.5)*step/float64(samples)))
					cr, cg, cb, _ := src.At(px, py).RGBA()
					r, g, b, n = r+cr, g+cg, b+cb, n+1
				}
			}
			offset := dst.PixOffset(x, y)
			dst.Pix[offset] = channel8(r, n)
			dst.Pix[offset+1] = channel8(g, n)
			dst.Pix[offset+2] = channel8(b, n)
			dst.Pix[offset+3] = 0xff
		}
	}
}

// channel8 is the mean of n summed 16-bit colour channels, as 8 bits.
func channel8(sum, n uint32) uint8 {
	return uint8(sum / n >> 8) // #nosec G115 -- a mean of 16-bit channels shifted right 8 fits a byte.
}
