package server

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
)

// Profile picture normalization (CORE-1240). Every stored picture is the
// server's own re-encode: a center-cropped square, upright per the upload's
// EXIF orientation, at most profilePictureSize pixels a side, flattened onto
// an opaque backdrop and written as a baseline JPEG. Re-encoding drops every
// metadata segment (location, camera, comments) the upload carried.
const (
	profilePictureSize        = 512
	profilePictureJPEGQuality = 85
	// A picture is far smaller than artwork; the pixel cap still admits a
	// full-resolution phone photo while bounding the decode allocation.
	maxProfilePicturePixels = 50_000_000
)

// profilePictureBackdrop fills transparent regions of a PNG upload. It is the
// dark surface every client draws avatars on, so a transparent cut-out reads
// as intended rather than as a white box.
var profilePictureBackdrop = color.RGBA{R: 0x14, G: 0x16, B: 0x1a, A: 0xff}

var errUnsupportedProfilePicture = errors.New("unsupported image")

// normalizeProfilePicture validates and re-encodes an uploaded JPEG or PNG.
func normalizeProfilePicture(file io.Reader) ([]byte, error) {
	prefix := make([]byte, maxArtworkImageSniffBytes)
	n, err := io.ReadFull(file, prefix)
	switch {
	case err == nil, errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		prefix = prefix[:n]
	default:
		return nil, errors.New("unable to read upload")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(prefix))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, errUnsupportedProfilePicture
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxProfilePicturePixels {
		return nil, errors.New("image dimensions exceed upload limit")
	}
	data, err := io.ReadAll(io.MultiReader(bytes.NewReader(prefix), file))
	if err != nil {
		return nil, errors.New("unable to read upload")
	}
	img, decodedFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil || decodedFormat != format {
		return nil, errUnsupportedProfilePicture
	}
	orientation := 1
	if format == "jpeg" {
		orientation = jpegOrientation(data)
	}
	square := squareThumbnail(img, profilePictureSize)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, orient(square, orientation), &jpeg.Options{Quality: profilePictureJPEGQuality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// squareThumbnail center-crops img to a square and box-filters it down to at
// most size pixels a side (never up), compositing any transparency onto the
// backdrop. The center square is the same region under every EXIF
// orientation, so orientation is applied afterwards to the small result.
func squareThumbnail(img image.Image, size int) *image.RGBA {
	bounds := img.Bounds()
	side := min(bounds.Dx(), bounds.Dy())
	x0 := bounds.Min.X + (bounds.Dx()-side)/2
	y0 := bounds.Min.Y + (bounds.Dy()-side)/2
	n := min(side, size)
	at := func(x, y int) color.RGBA64 {
		c, _ := color.RGBA64Model.Convert(img.At(x, y)).(color.RGBA64)
		return c
	}
	if fast, ok := img.(image.RGBA64Image); ok {
		at = fast.RGBA64At
	}
	dst := image.NewRGBA(image.Rect(0, 0, n, n))
	back := [3]uint64{uint64(profilePictureBackdrop.R) * 0x101, uint64(profilePictureBackdrop.G) * 0x101, uint64(profilePictureBackdrop.B) * 0x101}
	for dy := range n {
		sy0, sy1 := y0+dy*side/n, y0+(dy+1)*side/n
		for dx := range n {
			sx0, sx1 := x0+dx*side/n, x0+(dx+1)*side/n
			var r, g, b, a, count uint64
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					c := at(sx, sy)
					r += uint64(c.R)
					g += uint64(c.G)
					b += uint64(c.B)
					a += uint64(c.A)
					count++
				}
			}
			// Premultiplied average, then "over" the opaque backdrop.
			r, g, b, a = r/count, g/count, b/count, a/count
			inv := 0xffff - a
			dst.SetRGBA(dx, dy, color.RGBA{
				R: channel8(r + back[0]*inv/0xffff),
				G: channel8(g + back[1]*inv/0xffff),
				B: channel8(b + back[2]*inv/0xffff),
				A: 0xff,
			})
		}
	}
	return dst
}

// channel8 narrows a 16-bit channel to 8 bits, saturating.
func channel8(v uint64) uint8 {
	if v > 0xffff {
		return 0xff
	}
	return uint8(v >> 8)
}

// orient returns src (a square) transformed so EXIF orientation o displays
// upright. Orientations outside 2..8 return src unchanged.
func orient(src *image.RGBA, o int) *image.RGBA {
	if o < 2 || o > 8 {
		return src
	}
	n := src.Bounds().Dx()
	dst := image.NewRGBA(image.Rect(0, 0, n, n))
	last := n - 1
	for y := range n {
		for x := range n {
			var sx, sy int
			switch o {
			case 2: // mirror horizontal
				sx, sy = last-x, y
			case 3: // rotate 180
				sx, sy = last-x, last-y
			case 4: // mirror vertical
				sx, sy = x, last-y
			case 5: // transpose
				sx, sy = y, x
			case 6: // rotate 90 clockwise
				sx, sy = y, last-x
			case 7: // transverse
				sx, sy = last-y, last-x
			case 8: // rotate 90 counter-clockwise
				sx, sy = last-y, x
			}
			dst.SetRGBA(x, y, src.RGBAAt(sx, sy))
		}
	}
	return dst
}

// jpegOrientation reads the EXIF orientation (tag 0x0112 in IFD0) from a
// JPEG's APP1 segment, returning 1 when it is absent or malformed.
func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return 1
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xff {
			return 1
		}
		marker := data[i+1]
		if marker == 0xd8 || (marker >= 0xd0 && marker <= 0xd7) || marker == 0x01 || marker == 0xff {
			i += 2
			continue
		}
		if marker == 0xda || marker == 0xd9 { // start of scan or end: no more headers
			return 1
		}
		length := int(binary.BigEndian.Uint16(data[i+2:]))
		if length < 2 || i+2+length > len(data) {
			return 1
		}
		segment := data[i+4 : i+2+length]
		if marker == 0xe1 && bytes.HasPrefix(segment, []byte("Exif\x00\x00")) {
			return exifOrientation(segment[6:])
		}
		i += 2 + length
	}
	return 1
}

func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var header [8]byte
	copy(header[:], tiff)
	var order binary.ByteOrder
	switch string(header[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(header[2:4]) != 42 {
		return 1
	}
	ifd := int(order.Uint32(header[4:8]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 1
	}
	entries := int(order.Uint16(tiff[ifd:]))
	for e := range entries {
		at := ifd + 2 + e*12
		if at+12 > len(tiff) {
			return 1
		}
		if order.Uint16(tiff[at:]) != 0x0112 {
			continue
		}
		if order.Uint16(tiff[at+2:]) != 3 { // SHORT
			return 1
		}
		if value := int(order.Uint16(tiff[at+8:])); value >= 1 && value <= 8 {
			return value
		}
		return 1
	}
	return 1
}
