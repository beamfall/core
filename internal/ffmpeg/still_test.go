package ffmpeg

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Fixtures here are built in-test: real JPEG streams from image/jpeg, wrapped
// in synthetic RAW containers whose structure matches what each camera format
// stores (TIFF IFDs, RAF header, CR3 boxes). FFmpeg decodes only the embedded
// preview, so the container need not hold real sensor data.

func solidImage(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// quadrantImage is red top-left, blue top-right, green bottom-left, white
// bottom-right, so every flip and rotation is distinguishable.
func quadrantImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, quadrantColour(x < w/2, y < h/2))
		}
	}
	return img
}

func quadrantColour(left, top bool) color.RGBA {
	switch {
	case left && top:
		return color.RGBA{255, 0, 0, 255}
	case !left && top:
		return color.RGBA{0, 0, 255, 255}
	case left && !top:
		return color.RGBA{0, 255, 0, 255}
	}
	return color.RGBA{255, 255, 255, 255}
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withSegments inserts marker segments directly after SOI.
func withSegments(jpg []byte, segments ...[]byte) []byte {
	out := append([]byte{}, jpg[:2]...)
	for _, s := range segments {
		out = append(out, s...)
	}
	return append(out, jpg[2:]...)
}

func segment(marker byte, payload []byte) []byte {
	s := []byte{0xFF, marker, 0, 0}
	binary.BigEndian.PutUint16(s[2:], uint16(len(payload)+2)) // #nosec G115 -- test payloads are far below 64 KiB
	return append(s, payload...)
}

// tiffWithOrientation is a minimal little-endian TIFF whose IFD0 holds only
// the orientation tag: an Exif payload.
func tiffWithOrientation(orientation int) []byte {
	b := []byte("II\x2a\x00\x08\x00\x00\x00\x01\x00")
	entry := make([]byte, 12)
	binary.LittleEndian.PutUint16(entry, tagOrientation)
	binary.LittleEndian.PutUint16(entry[2:], 3)
	binary.LittleEndian.PutUint32(entry[4:], 1)
	binary.LittleEndian.PutUint16(entry[8:], uint16(orientation)) // #nosec G115 -- orientations are 1–8
	b = append(b, entry...)
	return append(b, 0, 0, 0, 0)
}

func exifSegment(orientation int) []byte {
	return segment(0xE1, append([]byte("Exif\x00\x00"), tiffWithOrientation(orientation)...))
}

func iccSegment(profile []byte) []byte {
	return segment(0xE2, append(append(append([]byte{}, iccMarker...), 1, 1), profile...))
}

// tiffBuilder lays out a little-endian TIFF: IFDs first, then data blobs.
type tiffBuilder struct {
	magic []byte // the 4-byte header start; defaults to "II*\0"
	ifds  [][]tiffField
	blobs [][]byte
}

type tiffField struct {
	tag, typ uint16
	values   []uint32 // SHORT/LONG values; blobRef fields are filled at build
	blob     int      // 1-based blob index whose offset is the value (LONG) or payload (UNDEFINED)
	ifdRef   int      // 1-based IFD index whose offset is the value
}

func (b *tiffBuilder) addBlob(data []byte) int {
	b.blobs = append(b.blobs, data)
	return len(b.blobs)
}

func (b *tiffBuilder) build() []byte {
	magic := b.magic
	if magic == nil {
		magic = []byte("II*\x00")
	}
	// Layout: header (8), padding to 16 (room for a CR2 signature), IFDs, blobs.
	ifdOffsets := make([]uint32, len(b.ifds))
	pos := uint32(16)
	if string(magic) == "IIU\x00" {
		pos = 0x18
	}
	for i, ifd := range b.ifds {
		ifdOffsets[i] = pos
		pos += uint32(2 + 12*len(ifd) + 4) // #nosec G115 -- small test fixtures
	}
	blobOffsets := make([]uint32, len(b.blobs))
	for i, blob := range b.blobs {
		blobOffsets[i] = pos
		pos += uint32(len(blob)) // #nosec G115 -- small test fixtures
	}
	out := make([]byte, pos)
	copy(out, magic)
	binary.LittleEndian.PutUint32(out[4:], ifdOffsets[0])
	for i, ifd := range b.ifds {
		p := ifdOffsets[i]
		binary.LittleEndian.PutUint16(out[p:], uint16(len(ifd))) // #nosec G115 -- small test fixtures
		for j, f := range ifd {
			e := out[p+2+uint32(12*j):] // #nosec G115 -- small test fixtures
			binary.LittleEndian.PutUint16(e, f.tag)
			binary.LittleEndian.PutUint16(e[2:], f.typ)
			switch {
			case f.blob > 0 && f.typ == 7:
				binary.LittleEndian.PutUint32(e[4:], uint32(len(b.blobs[f.blob-1]))) // #nosec G115 -- test blobs are small
				binary.LittleEndian.PutUint32(e[8:], blobOffsets[f.blob-1])
			case f.blob > 0:
				binary.LittleEndian.PutUint32(e[4:], 1)
				binary.LittleEndian.PutUint32(e[8:], blobOffsets[f.blob-1])
			case f.ifdRef > 0:
				binary.LittleEndian.PutUint32(e[4:], 1)
				binary.LittleEndian.PutUint32(e[8:], ifdOffsets[f.ifdRef-1])
			case f.typ == 3:
				binary.LittleEndian.PutUint32(e[4:], 1)
				binary.LittleEndian.PutUint16(e[8:], uint16(f.values[0])) // #nosec G115 -- SHORT fixture values
			default:
				binary.LittleEndian.PutUint32(e[4:], 1)
				binary.LittleEndian.PutUint32(e[8:], f.values[0])
			}
		}
	}
	for i, blob := range b.blobs {
		copy(out[blobOffsets[i]:], blob)
	}
	return out
}

func short(tag uint16, v uint32) tiffField { return tiffField{tag: tag, typ: 3, values: []uint32{v}} }
func long(tag uint16, v uint32) tiffField  { return tiffField{tag: tag, typ: 4, values: []uint32{v}} }

// jpegFields points JPEGInterchangeFormat at blob.
func jpegFields(blob int, length int) []tiffField {
	return []tiffField{{tag: tagJPEGOffset, typ: 4, blob: blob}, long(tagJPEGLength, uint32(length))} // #nosec G115 -- small test fixtures
}

// stripFields describes blob as a single JPEG-compressed strip.
func stripFields(blob int, length int) []tiffField {
	return []tiffField{short(tagCompression, 6), {tag: tagStripOffsets, typ: 4, blob: blob}, long(tagStripByteCounts, uint32(length))} // #nosec G115 -- small test fixtures
}

var (
	previewColour = color.RGBA{30, 200, 60, 255}
	thumbColour   = color.RGBA{200, 30, 200, 255}
)

// rawFixture is one synthetic RAW: its bytes carry a 1200×800 preview in
// previewColour plus a smaller decoy (a 160×120 thumbnail and, for CR2, a
// larger lossless strip).
type rawFixture struct {
	ext         string
	data        []byte
	orientation int
}

// losslessDecoy is a valid JPEG stream relabelled as lossless (SOF3): the shape
// of a CR2's raw sensor strip, larger than the preview.
func losslessDecoy(t *testing.T) []byte {
	b := encodeJPEG(t, solidImage(2000, 1400, thumbColour))
	if i := bytes.Index(b, []byte{0xFF, 0xC0}); i >= 0 {
		b[i+1] = 0xC3
	}
	return b
}

func rawFixtures(t *testing.T) []rawFixture {
	t.Helper()
	preview := encodeJPEG(t, solidImage(1200, 800, previewColour))
	thumb := encodeJPEG(t, solidImage(160, 120, thumbColour))
	cfa := short(tagPhotometric, 32803)

	tiffRAW := func(magic []byte, layout func(b *tiffBuilder)) []byte {
		b := &tiffBuilder{magic: magic}
		layout(b)
		return b.build()
	}
	// IFD0: orientation + thumbnail; SubIFD: preview; second SubIFD: CFA.
	nefLike := func(magic []byte) []byte {
		return tiffRAW(magic, func(b *tiffBuilder) {
			tb := b.addBlob(thumb)
			pb := b.addBlob(preview)
			b.ifds = [][]tiffField{
				append([]tiffField{short(tagOrientation, 1), {tag: tagSubIFDs, typ: 4, ifdRef: 2}}, jpegFields(tb, len(thumb))...),
				append([]tiffField{{tag: tagSubIFDs, typ: 4, ifdRef: 3}}, jpegFields(pb, len(preview))...),
				{cfa, short(tagCompression, 1)},
			}
		})
	}
	// IFD0: preview by JPEGInterchangeFormat and CFA; IFD1 thumbnail chained.
	ifd0Preview := func(magic []byte) []byte {
		return tiffRAW(magic, func(b *tiffBuilder) {
			pb := b.addBlob(preview)
			tb := b.addBlob(thumb)
			b.ifds = [][]tiffField{
				append([]tiffField{cfa}, jpegFields(pb, len(preview))...),
				jpegFields(tb, len(thumb)),
			}
		})
	}
	var fixtures []rawFixture
	for _, ext := range []string{"nef", "nrw", "pef", "srw", "3fr"} {
		fixtures = append(fixtures, rawFixture{ext: ext, data: nefLike(nil), orientation: 1})
	}
	fixtures = append(fixtures, rawFixture{ext: "arw", data: ifd0Preview(nil), orientation: 1})

	// DNG: IFD0 thumbnail + DNGVersion; SubIFD preview as a JPEG strip.
	fixtures = append(fixtures, rawFixture{ext: "dng", orientation: 8, data: tiffRAW(nil, func(b *tiffBuilder) {
		tb := b.addBlob(thumb)
		pb := b.addBlob(preview)
		b.ifds = [][]tiffField{
			append([]tiffField{short(tagOrientation, 8), {tag: tagDNGVersion, typ: 1, values: []uint32{0x01040000}}, {tag: tagSubIFDs, typ: 4, ifdRef: 2}}, jpegFields(tb, len(thumb))...),
			stripFields(pb, len(preview)),
		}
	})})

	// CR2: "CR" signature at byte 8; IFD0 strip is the preview; IFD3 is the
	// larger lossless sensor strip, which must be ignored.
	cr2 := tiffRAW(nil, func(b *tiffBuilder) {
		pb := b.addBlob(preview)
		tb := b.addBlob(thumb)
		lb := b.addBlob(losslessDecoy(t))
		b.ifds = [][]tiffField{
			append([]tiffField{short(tagOrientation, 6)}, stripFields(pb, len(preview))...),
			jpegFields(tb, len(thumb)),
			stripFields(lb, len(b.blobs[lb-1])),
		}
		// Chain IFD0 → IFD1 → IFD2 through the next-IFD pointers below.
	})
	chainIFDs(cr2, 3)
	copy(cr2[8:], "CR\x02\x00")
	fixtures = append(fixtures, rawFixture{ext: "cr2", data: cr2, orientation: 6})

	// ORF: "IIRO" magic; the preview lives in maker notes no standard tag
	// reaches, so only the byte scan finds it.
	orf := tiffRAW([]byte("IIRO"), func(b *tiffBuilder) {
		tb := b.addBlob(thumb)
		b.addBlob([]byte("maker-note-padding"))
		b.addBlob(preview)
		b.ifds = [][]tiffField{append([]tiffField{short(tagOrientation, 3)}, jpegFields(tb, len(thumb))...)}
	})
	fixtures = append(fixtures, rawFixture{ext: "orf", data: orf, orientation: 3})

	// RW2: "IIU\0" magic, IFD0 at 0x18, preview in the JpgFromRaw tag.
	fixtures = append(fixtures, rawFixture{ext: "rw2", orientation: 1, data: tiffRAW([]byte("IIU\x00"), func(b *tiffBuilder) {
		pb := b.addBlob(preview)
		tb := b.addBlob(thumb)
		b.ifds = [][]tiffField{
			append([]tiffField{{tag: tagRW2JpgFromRaw, typ: 7, blob: pb}}, jpegFields(tb, len(thumb))...),
		}
	})})

	// RAF: header with the JPEG offset/length at 84/88; the JPEG's own EXIF
	// carries the orientation.
	rafJPEG := withSegments(preview, exifSegment(6))
	raf := make([]byte, 160)
	copy(raf, "FUJIFILMCCD-RAW 0201FF383501")
	binary.BigEndian.PutUint32(raf[84:], uint32(len(raf)))     // #nosec G115 -- small test header
	binary.BigEndian.PutUint32(raf[88:], uint32(len(rafJPEG))) // #nosec G115 -- small test preview
	raf = append(raf, rafJPEG...)
	fixtures = append(fixtures, rawFixture{ext: "raf", data: raf, orientation: 6})

	fixtures = append(fixtures, rawFixture{ext: "cr3", data: cr3Fixture(preview, thumb, 8), orientation: 8})
	return fixtures
}

// chainIFDs links IFD i to IFD i+1 for the first n IFDs of a tiffBuilder
// output laid out from offset 16.
func chainIFDs(b []byte, n int) {
	pos := uint32(16)
	for i := range n {
		count := uint32(binary.LittleEndian.Uint16(b[pos:]))
		next := pos + 2 + 12*count
		if i < n-1 {
			binary.LittleEndian.PutUint32(b[next:], next+4)
		}
		pos = next + 4
	}
}

func box(typ string, payload ...[]byte) []byte {
	var body []byte
	for _, p := range payload {
		body = append(body, p...)
	}
	b := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(b, uint32(8+len(body))) // #nosec G115 -- test boxes are small
	copy(b[4:], typ)
	return append(b, body...)
}

// cr3Fixture lays out a CR3: ftyp crx, moov → metadata uuid → CMT1 (TIFF IFD0
// with orientation) and THMB, the top-level preview uuid with PRVW, then mdat.
func cr3Fixture(preview, thumb []byte, orientation int) []byte {
	prvwHeader := make([]byte, 16)
	binary.BigEndian.PutUint32(prvwHeader[12:], uint32(len(preview))) // #nosec G115 -- small test preview
	meta := box("uuid", cr3MetadataUUID, box("CMT1", tiffWithOrientation(orientation)), box("THMB", make([]byte, 16), thumb))
	return append(append(append(
		box("ftyp", []byte("crx \x00\x00\x00\x01crx isom")),
		box("moov", meta)...),
		box("uuid", cr3PreviewUUID, make([]byte, 8), box("PRVW", prvwHeader, preview))...),
		box("mdat", make([]byte, 64))...)
}

func TestInspectStillPicksEmbeddedPreview(t *testing.T) {
	for _, fx := range rawFixtures(t) {
		t.Run(fx.ext, func(t *testing.T) {
			src := inspectStill(bytes.NewReader(fx.data), int64(len(fx.data)), "frame."+fx.ext)
			shape, ok := validJPEG(src.preview)
			if !ok {
				t.Fatalf("no valid preview extracted")
			}
			if shape.width != 1200 || shape.height != 800 {
				t.Fatalf("preview = %dx%d, want the 1200x800 preview", shape.width, shape.height)
			}
			if src.orientation != fx.orientation {
				t.Fatalf("orientation = %d, want %d", src.orientation, fx.orientation)
			}
			// Extension-blind: importers pass sources without one.
			blind := inspectStill(bytes.NewReader(fx.data), int64(len(fx.data)), "frame.import.tmp")
			if blind.preview == nil {
				t.Fatalf("preview not found without the extension hint")
			}
		})
	}
}

func TestValidJPEG(t *testing.T) {
	jpg := encodeJPEG(t, solidImage(40, 20, previewColour))
	shape, ok := validJPEG(append(append([]byte{}, jpg...), "trailing"...))
	if !ok || shape.width != 40 || shape.height != 20 || shape.length != len(jpg) {
		t.Fatalf("validJPEG = %+v %v, want 40x20 length %d", shape, ok, len(jpg))
	}
	if _, ok := validJPEG(jpg[:len(jpg)-10]); ok {
		t.Fatal("a truncated stream validated")
	}
	if _, ok := validJPEG(losslessDecoy(t)); ok {
		t.Fatal("a lossless (SOF3) stream validated as a preview")
	}
}

func TestPlainTIFFIsNotARAW(t *testing.T) {
	b := &tiffBuilder{}
	tb := b.addBlob(encodeJPEG(t, solidImage(160, 120, thumbColour)))
	b.ifds = [][]tiffField{append([]tiffField{short(tagOrientation, 6), short(tagPhotometric, 2)}, jpegFields(tb, len(b.blobs[0]))...)}
	data := b.build()
	src := inspectStill(bytes.NewReader(data), int64(len(data)), "scan.tif")
	if src.preview != nil {
		t.Fatal("a plain TIFF was thumbnailed from an embedded JPEG")
	}
	if src.orientation != 6 {
		t.Fatalf("orientation = %d, want 6", src.orientation)
	}
}

func TestInspectPNGAndWebPMetadata(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, solidImage(4, 4, previewColour)); err != nil {
		t.Fatal(err)
	}
	p := buf.Bytes()
	exif := tiffWithOrientation(5)
	chunk := make([]byte, 8)
	binary.BigEndian.PutUint32(chunk, uint32(len(exif))) // #nosec G115 -- small test chunk
	copy(chunk[4:], "eXIf")
	chunk = append(append(chunk, exif...), 0, 0, 0, 0) // CRC is not checked
	withExif := append(append(append([]byte{}, p[:33]...), chunk...), p[33:]...)
	if got := inspectStill(bytes.NewReader(withExif), int64(len(withExif)), "a.png").orientation; got != 5 {
		t.Fatalf("PNG eXIf orientation = %d, want 5", got)
	}

	webp := []byte("RIFF\x00\x00\x00\x00WEBP")
	vp8x := append([]byte("VP8X\x0a\x00\x00\x00"), make([]byte, 10)...)
	exifChunk := []byte("EXIF\x00\x00\x00\x00")
	binary.LittleEndian.PutUint32(exifChunk[4:], uint32(len(exif))) // #nosec G115 -- small test chunk
	webp = append(append(append(webp, vp8x...), exifChunk...), exif...)
	if got := inspectStill(bytes.NewReader(webp), int64(len(webp)), "a.webp").orientation; got != 5 {
		t.Fatalf("WebP EXIF orientation = %d, want 5", got)
	}
}

var (
	p3Colorants    = d50Colorants([3]chromaticity{{0.680, 0.320}, {0.265, 0.690}, {0.150, 0.060}})
	adobeColorants = d50Colorants([3]chromaticity{{0.640, 0.330}, {0.210, 0.710}, {0.150, 0.060}})
)

func TestICCClassification(t *testing.T) {
	cases := []struct {
		name    string
		profile []byte
		want    stillColour
	}{
		{"sRGB", srgbICC, stillColour{"bt709", "srgb"}},
		{"Display P3", buildDisplayICC("Display P3", p3Colorants), stillColour{"smpte432", "srgb"}},
		{"Adobe RGB colorants", buildDisplayICC("Adobe RGB", adobeColorants), stillColour{}},
		{"garbage", []byte("not a profile"), stillColour{}},
	}
	for _, sys := range []struct {
		path string
		want stillColour
	}{
		{"/System/Library/ColorSync/Profiles/Display P3.icc", stillColour{"smpte432", "srgb"}},
		{"/System/Library/ColorSync/Profiles/sRGB Profile.icc", stillColour{"bt709", "srgb"}},
		{"/System/Library/ColorSync/Profiles/AdobeRGB1998.icc", stillColour{}},
	} {
		if b, err := os.ReadFile(sys.path); err == nil {
			cases = append(cases, struct {
				name    string
				profile []byte
				want    stillColour
			}{filepath.Base(sys.path), b, sys.want})
		}
	}
	for _, c := range cases {
		if got := iccColour(c.profile); got != c.want {
			t.Errorf("%s: iccColour = %+v, want %+v", c.name, got, c.want)
		}
	}
	if srgb := (stillColour{"bt709", "srgb"}); srgb.converts() {
		t.Error("sRGB must not convert")
	}
	if p3 := (stillColour{"smpte432", "srgb"}); !p3.converts() {
		t.Error("Display P3 must convert")
	}
}

func TestJPEGWithICCReplacesProfiles(t *testing.T) {
	jpg := withSegments(encodeJPEG(t, solidImage(8, 8, previewColour)), iccSegment([]byte("old profile")))
	tagged, err := jpegWithICC(jpg, srgbICC)
	if err != nil {
		t.Fatal(err)
	}
	_, icc := jpegMetadata(tagged)
	if !bytes.Equal(icc, srgbICC) {
		t.Fatalf("embedded profile is %d bytes, want the %d-byte sRGB profile only", len(icc), len(srgbICC))
	}
	if _, err := jpeg.Decode(bytes.NewReader(tagged)); err != nil {
		t.Fatalf("tagged JPEG does not decode: %v", err)
	}
}

// --- real-binary tests -------------------------------------------------------

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed; skipping real-binary still test")
	}
}

func scaleToJPEG(t *testing.T, srcName string, data []byte, width int) (image.Image, []byte) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, srcName)
	if err := os.WriteFile(src, data, 0o600); err != nil { // #nosec G703 -- src is under this test's t.TempDir()
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out.w320.jpg")
	if err := New().ScaleImage(context.Background(), src, width, dst); err != nil {
		t.Fatalf("ScaleImage: %v", err)
	}
	out, err := os.ReadFile(dst) // #nosec G304 -- output under this test's t.TempDir().
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("derivative is not a JPEG: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("directory holds %d entries after the scale, want source and derivative only", len(entries))
	}
	return img, out
}

func near(c color.Color, want color.RGBA, tol int) bool {
	r, g, b, _ := c.RGBA()
	d := func(a uint32, w uint8) bool { x := int(a>>8) - int(w); return x <= tol && x >= -tol }
	return d(r, want.R) && d(g, want.G) && d(b, want.B)
}

func rgbOf(c color.Color) [3]int {
	r, g, b, _ := c.RGBA()
	return [3]int{int(r >> 8), int(g >> 8), int(b >> 8)}
}

func TestScaleImageUsesRAWPreview(t *testing.T) {
	requireFFmpeg(t)
	for _, fx := range rawFixtures(t) {
		t.Run(fx.ext, func(t *testing.T) {
			img, _ := scaleToJPEG(t, "frame."+fx.ext, fx.data, 320)
			wantW, wantH := 320, 214 // -2 keeps the height even
			if fx.orientation >= 5 {
				wantW, wantH = 320, 480
			}
			if b := img.Bounds(); b.Dx() != wantW || b.Dy() != wantH {
				t.Fatalf("derivative = %dx%d, want %dx%d", b.Dx(), b.Dy(), wantW, wantH)
			}
			if c := img.At(wantW/2, wantH/2); !near(c, previewColour, 6) {
				t.Fatalf("derivative pixel = %v, want the preview colour %v", rgbOf(c), previewColour)
			}
		})
	}
}

// displayedQuadrant maps a displayed-image coordinate back to the w×h stored
// image for an EXIF orientation (TIFF 6.0 / Exif 2.3 semantics) and returns
// the stored quadrant's colour.
func displayedQuadrant(orientation int, dx, dy, w, h int) color.RGBA {
	var sx, sy int
	switch orientation {
	case 1:
		sx, sy = dx, dy
	case 2:
		sx, sy = w-1-dx, dy
	case 3:
		sx, sy = w-1-dx, h-1-dy
	case 4:
		sx, sy = dx, h-1-dy
	case 5:
		sx, sy = dy, dx
	case 6:
		sx, sy = dy, h-1-dx
	case 7:
		sx, sy = w-1-dy, h-1-dx
	case 8:
		sx, sy = w-1-dy, dx
	}
	return quadrantColour(sx < w/2, sy < h/2)
}

func TestScaleImageAppliesEXIFOrientation(t *testing.T) {
	requireFFmpeg(t)
	const w, h = 64, 32
	base := encodeJPEG(t, quadrantImage(w, h))
	for orientation := 1; orientation <= 8; orientation++ {
		img, out := scaleToJPEG(t, "photo.jpg", withSegments(base, exifSegment(orientation)), 320)
		dw, dh := w, h
		if orientation >= 5 {
			dw, dh = h, w
		}
		if b := img.Bounds(); b.Dx() != dw || b.Dy() != dh {
			t.Fatalf("orientation %d: derivative = %dx%d, want %dx%d", orientation, b.Dx(), b.Dy(), dw, dh)
		}
		for _, p := range [][2]int{{dw / 4, dh / 4}, {3 * dw / 4, dh / 4}, {dw / 4, 3 * dh / 4}, {3 * dw / 4, 3 * dh / 4}} {
			want := displayedQuadrant(orientation, p[0], p[1], w, h)
			if c := img.At(p[0], p[1]); !near(c, want, 40) {
				t.Fatalf("orientation %d: pixel %v = %v, want %v", orientation, p, rgbOf(c), want)
			}
		}
		if o, _ := jpegMetadata(out); o != 0 {
			t.Fatalf("orientation %d: derivative carries orientation tag %d", orientation, o)
		}
	}
}

var (
	p3Source       = color.RGBA{180, 120, 60, 255}
	p3SourceInSRGB = color.RGBA{190, 117, 45, 255} // ImageMagick/lcms2 reference
)

func TestScaleImageConvertsDisplayP3ToSRGB(t *testing.T) {
	requireFFmpeg(t)
	// 31 wide: the colorspace filter refuses odd sizes, so the scale rounds.
	for _, w := range []int{32, 31} {
		src := withSegments(encodeJPEG(t, solidImage(w, 20, p3Source)), iccSegment(buildDisplayICC("Display P3", p3Colorants)))
		img, out := scaleToJPEG(t, "wide.jpg", src, 320)
		if c := img.At(10, 10); !near(c, p3SourceInSRGB, 3) {
			t.Fatalf("width %d: converted pixel = %v, want ≈%v", w, rgbOf(c), p3SourceInSRGB)
		}
		if _, icc := jpegMetadata(out); !bytes.Equal(icc, srgbICC) {
			t.Fatalf("width %d: derivative profile is %d bytes, want the sRGB profile", w, len(icc))
		}
	}
}

func TestScaleImageKeepsUnsupportedProfile(t *testing.T) {
	requireFFmpeg(t)
	adobe := buildDisplayICC("Adobe RGB", adobeColorants)
	src := withSegments(encodeJPEG(t, solidImage(32, 32, p3Source)), iccSegment(adobe))
	img, out := scaleToJPEG(t, "adobe.jpg", src, 320)
	if c := img.At(16, 16); !near(c, p3Source, 3) {
		t.Fatalf("pixel = %v, want the unconverted %v", rgbOf(c), p3Source)
	}
	if _, icc := jpegMetadata(out); !bytes.Equal(icc, adobe) {
		t.Fatalf("derivative profile is %d bytes, want the source profile carried through", len(icc))
	}
}

// testdata/display-p3.heic is a 64×64 solid Display P3 (180,120,60) HEIC
// with an embedded Display P3 profile (colr prof), made on macOS with
// `sips -s format heic -m "/System/Library/ColorSync/Profiles/Display P3.icc"`
// from a PNG of that colour.
func TestScaleImageConvertsDisplayP3HEIC(t *testing.T) {
	requireFFmpeg(t)
	data, err := os.ReadFile(filepath.Join("testdata", "display-p3.heic"))
	if err != nil {
		t.Fatal(err)
	}
	if src := inspectStill(bytes.NewReader(data), int64(len(data)), "a.heic"); src.colour != (stillColour{"smpte432", "srgb"}) || src.orientation != 0 {
		t.Fatalf("HEIC inspection = %+v, want Display P3 colour and FFmpeg-managed orientation", src)
	}
	probe := exec.Command("ffmpeg", "-v", "error", "-i", filepath.Join("testdata", "display-p3.heic"), "-f", "null", "-") // #nosec G204 -- fixed arguments
	if out, err := probe.CombinedOutput(); err != nil {
		t.Skipf("this ffmpeg cannot decode HEIC: %s", out)
	}
	img, out := scaleToJPEG(t, "wide.heic", data, 320)
	if c := img.At(32, 32); !near(c, p3SourceInSRGB, 4) {
		t.Fatalf("converted pixel = %v, want ≈%v", rgbOf(c), p3SourceInSRGB)
	}
	if _, icc := jpegMetadata(out); !bytes.Equal(icc, srgbICC) {
		t.Fatalf("derivative profile is %d bytes, want the sRGB profile", len(icc))
	}
}

// fullBox is an ISO BMFF full box with version 0 and the given flags.
func fullBox(typ string, flags uint32, payload ...[]byte) []byte {
	vf := make([]byte, 4)
	binary.BigEndian.PutUint32(vf, flags)
	return box(typ, append([][]byte{vf}, payload...)...)
}

func be32(v int) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(v)) // #nosec G115 -- test offsets and sizes are small
	return b
}

// cr3TrackFixture is a CR3 whose PRVW is a usable 1100×700 preview while one
// moov track's first sample, in mdat, is a larger 1300×900 JPEG; a second
// track points at non-JPEG (CRX-like) bytes.
func cr3TrackFixture(t *testing.T) []byte {
	prvw := encodeJPEG(t, solidImage(1100, 700, thumbColour))
	full := encodeJPEG(t, solidImage(1300, 900, previewColour))
	sensor := bytes.Repeat([]byte{0x12, 0x34}, 512)
	layout := func(fullOff, sensorOff int) []byte {
		trak := func(off, size int) []byte {
			stbl := box("stbl", fullBox("stsz", 0, be32(0), be32(1), be32(size)), fullBox("stco", 0, be32(1), be32(off)))
			return box("trak", box("mdia", box("minf", stbl)))
		}
		prvwHeader := make([]byte, 16)
		binary.BigEndian.PutUint32(prvwHeader[12:], uint32(len(prvw))) // #nosec G115 -- small test preview
		var b []byte
		b = append(b, box("ftyp", []byte("crx \x00\x00\x00\x01crx isom"))...)
		b = append(b, box("moov", trak(sensorOff, len(sensor)), trak(fullOff, len(full)))...)
		b = append(b, box("uuid", cr3PreviewUUID, make([]byte, 8), box("PRVW", prvwHeader, prvw))...)
		return append(b, box("mdat", sensor, full)...)
	}
	probe := layout(0, 0)
	mdat := len(probe) - len(sensor) - len(full)
	return layout(mdat+len(sensor), mdat)
}

func TestInspectCR3PrefersTheFullSizeJPEGTrack(t *testing.T) {
	data := cr3TrackFixture(t)
	src := inspectStill(bytes.NewReader(data), int64(len(data)), "frame.cr3")
	shape, ok := validJPEG(src.preview)
	if !ok || shape.width != 1300 || shape.height != 900 {
		t.Fatalf("preview = %+v %v, want the 1300x900 track JPEG", shape, ok)
	}
}

// heifFixture is a HEIF whose ipco holds an sRGB nclx (index 1) and a Display
// P3 nclx (index 2); item 1 is associated with index 2, item 2 with index 1.
func heifFixture(primary int) []byte {
	nclx := func(primaries, transfer uint16) []byte {
		b := []byte("nclx\x00\x00\x00\x00\x00\x01\x80")
		binary.BigEndian.PutUint16(b[4:], primaries)
		binary.BigEndian.PutUint16(b[6:], transfer)
		return box("colr", b)
	}
	pitm := make([]byte, 2)
	binary.BigEndian.PutUint16(pitm, uint16(primary)) // #nosec G115 -- 1 or 2
	ipma := []byte{0, 0, 0, 2, 0, 1, 1, 0x82, 0, 2, 1, 0x01}
	meta := fullBox("meta", 0,
		fullBox("pitm", 0, pitm),
		box("iprp", box("ipco", nclx(1, 13), nclx(12, 13)), fullBox("ipma", 0, ipma)))
	return append(box("ftyp", []byte("heic\x00\x00\x00\x00mif1heic")), meta...)
}

func TestHEIFColourFollowsThePrimaryItem(t *testing.T) {
	for primary, want := range map[int]bool{1: true, 2: false} {
		data := heifFixture(primary)
		src := inspectStill(bytes.NewReader(data), int64(len(data)), "a.heic")
		if src.colour.converts() != want {
			t.Fatalf("primary item %d: colour %+v converts = %v, want %v", primary, src.colour, !want, want)
		}
	}
}

func TestInspectJPEGFindsLateEXIF(t *testing.T) {
	filler := segment(0xE0, make([]byte, 60000))
	var prefix [][]byte
	for range 48 { // ≈2.9 MB of other header segments before the Exif
		prefix = append(prefix, filler)
	}
	jpg := encodeJPEG(t, solidImage(16, 16, previewColour))
	data := withSegments(jpg, append(prefix, exifSegment(6))...)
	if src := inspectStill(bytes.NewReader(data), int64(len(data)), "a.jpg"); src.orientation != 6 {
		t.Fatalf("orientation = %d, want 6 from the late Exif segment", src.orientation)
	}
	// A header that cannot be walked to the scan leaves orientation to FFmpeg.
	cut := data[:2+len(filler)/2]
	if src := inspectStill(bytes.NewReader(cut), int64(len(cut)), "a.jpg"); src.orientation != 0 {
		t.Fatalf("orientation = %d for an unwalkable header, want 0 (FFmpeg decides)", src.orientation)
	}
}

type countingReaderAt struct {
	r    io.ReaderAt
	read int
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.r.ReadAt(p, off)
	c.read += n
	return n, err
}

func TestBestPreviewReadsEachSpanOnce(t *testing.T) {
	jpg := encodeJPEG(t, solidImage(1200, 800, previewColour))
	spans := make([]byteSpan, 1000)
	for i := range spans {
		spans[i] = byteSpan{0, int64(len(jpg))}
	}
	r := &countingReaderAt{r: bytes.NewReader(jpg)}
	if bestPreview(r, int64(len(jpg)), spans) == nil {
		t.Fatal("no preview")
	}
	if r.read > 2*len(jpg) {
		t.Fatalf("read %d bytes for a %d-byte span named 1000 times", r.read, len(jpg))
	}
}

func TestBestPreviewScanIsBoundedOnNestedCandidates(t *testing.T) {
	// Every 7-byte unit is a header segment whose payload is another SOI, so
	// each candidate's walk runs to the end of the buffer.
	unit := []byte{0xFF, 0xE0, 0x00, 0x05, 0xFF, 0xD8, 0xFF}
	data := append([]byte{0xFF, 0xD8}, bytes.Repeat(unit, (8<<20)/len(unit))...)
	done := make(chan struct{})
	go func() {
		bestPreview(bytes.NewReader(data), int64(len(data)), nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the fallback scan did not finish: its validation work is unbounded")
	}
}

func heifColourOf(t *testing.T, props [][]byte, ipmas [][]byte, iref []byte) stillColour {
	t.Helper()
	children := [][]byte{fullBox("pitm", 0, []byte{0, 1})}
	if iref != nil {
		children = append(children, fullBox("iref", 0, iref))
	}
	iprp := [][]byte{box("ipco", props...)}
	for _, entries := range ipmas {
		iprp = append(iprp, fullBox("ipma", 0, entries))
	}
	children = append(children, box("iprp", iprp...))
	data := append(box("ftyp", []byte("heic\x00\x00\x00\x00mif1heic")), fullBox("meta", 0, children...)...)
	return inspectStill(bytes.NewReader(data), int64(len(data)), "a.heic").colour
}

func TestHEIFColourResolvesThePrimaryItemsProperty(t *testing.T) {
	nclx := func(primaries uint16) []byte {
		b := []byte("nclx\x00\x00\x00\x0d\x00\x01\x80")
		binary.BigEndian.PutUint16(b[4:], primaries)
		return box("colr", b)
	}
	srgb, p3 := nclx(1), nclx(12)
	p3ICC := box("colr", append([]byte("prof"), buildDisplayICC("Display P3", p3Colorants)...))
	// ipma entries: count, then item ID, association count, property indices.
	cases := []struct {
		name  string
		props [][]byte
		ipmas [][]byte
		iref  []byte
		want  bool
	}{
		{"ICC listed after nclx", [][]byte{srgb, p3ICC}, [][]byte{{0, 0, 0, 1, 0, 1, 2, 0x81, 0x82}}, nil, true},
		{"association in a later ipma", [][]byte{srgb, p3}, [][]byte{{0, 0, 0, 1, 0, 2, 1, 0x81}, {0, 0, 0, 1, 0, 1, 1, 0x82}}, nil, true},
		{"grid inherits the first tile", [][]byte{p3}, [][]byte{{0, 0, 0, 1, 0, 2, 1, 0x81}}, box("dimg", []byte{0, 1, 0, 2, 0, 2, 0, 3}), true},
		{"grid without a dimg reference", [][]byte{p3}, [][]byte{{0, 0, 0, 1, 0, 2, 1, 0x81}}, nil, false},
		{"empty ICC does not hide nclx", [][]byte{box("colr", []byte("prof")), p3}, [][]byte{{0, 0, 0, 1, 0, 1, 2, 0x81, 0x82}}, nil, true},
		{"no ipma takes the first colr", [][]byte{p3}, nil, nil, true},
	}
	for _, c := range cases {
		if got := heifColourOf(t, c.props, c.ipmas, c.iref); got.converts() != c.want {
			t.Errorf("%s: colour %+v converts = %v, want %v", c.name, got, got.converts(), c.want)
		}
	}
}

func TestJPEGMetadataReadsOnlyWhatItKeeps(t *testing.T) {
	xmp := segment(0xE1, append([]byte("http://ns.adobe.com/xap/1.0/\x00"), make([]byte, 60000)...))
	dupICC := iccSegment(make([]byte, 60000))
	var segs [][]byte
	for range 40 { // ≈4.8 MB of APP1 that is not Exif and repeated ICC chunks
		segs = append(segs, xmp, dupICC)
	}
	segs = append(segs, exifSegment(6), exifSegment(3))
	// Only the first Exif counts, even when it has no orientation.
	noOrientation := withSegments(encodeJPEG(t, solidImage(16, 16, previewColour)), exifSegment(0), exifSegment(6))
	if o, _, _ := jpegMetadataAt(bytes.NewReader(noOrientation), int64(len(noOrientation))); o != 0 {
		t.Fatalf("orientation = %d from a second Exif, want 0", o)
	}
	jpg := withSegments(encodeJPEG(t, solidImage(16, 16, previewColour)), segs...)
	r := &countingReaderAt{r: bytes.NewReader(jpg)}
	orientation, icc, complete := jpegMetadataAt(r, int64(len(jpg)))
	if orientation != 6 || !complete || len(icc) != 60000 {
		t.Fatalf("orientation %d, icc %d bytes, complete %v; want 6 from the first Exif, one ICC chunk, complete", orientation, len(icc), complete)
	}
	if r.read > 200_000 {
		t.Fatalf("read %d bytes of metadata, want only the kept segments and signatures", r.read)
	}
}
