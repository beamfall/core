package ffmpeg

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"io"
	"path/filepath"
	"strings"
)

// Still-image inspection for ScaleImage (CORE-0310): before FFmpeg runs,
// Beamfall reads the source's container to decide three things FFmpeg cannot be
// trusted to get right across versions and formats:
//
//   - RAW sources are thumbnailed from the largest embedded JPEG preview the
//     camera wrote, not from FFmpeg's TIFF decoder (which reads a tiny IFD0
//     thumbnail or the undemosaiced sensor strip, or fails outright).
//   - EXIF orientation is applied explicitly, with FFmpeg's own autorotation
//     off, so a derivative is upright whichever FFmpeg build runs and carries no
//     orientation tag of its own.
//   - A supported wide-gamut ICC profile or nclx colour box is converted to sRGB.
//
// Every parser is bounded and fails soft: an unreadable structure yields the
// zero stillSource, which is the pre-CORE-0310 direct decode.

const (
	// maxPreviewBytes bounds one embedded preview read into memory.
	maxPreviewBytes = 64 << 20
	// previewScanBytes bounds the fallback byte scan for JPEG previews that no
	// container structure points at (ORF maker notes, unusual layouts).
	previewScanBytes = 64 << 20
	// previewScanWork bounds the bytes the fallback scan's JPEG validation may
	// examine in all, since overlapping candidates can each walk a long suffix.
	previewScanWork = 4 * previewScanBytes
	// maxPreviewReadBytes bounds the structured preview bytes read for one source.
	maxPreviewReadBytes = 256 << 20
	// smallPreviewSide is the long side below which the structured previews are
	// considered thumbnails, so the fallback scan runs.
	smallPreviewSide = 1024
)

// rawExtensions are the RAW formats the scanner admits as image media. The
// extension is a hint; container signatures decide on their own as well, since
// importers hand ScaleImage sources without an image extension.
var rawExtensions = map[string]bool{
	".cr2": true, ".cr3": true, ".nef": true, ".nrw": true, ".arw": true, ".dng": true,
	".raf": true, ".orf": true, ".rw2": true, ".pef": true, ".srw": true, ".3fr": true,
}

type stillSource struct {
	// preview is the embedded JPEG FFmpeg decodes instead of the source.
	preview []byte
	// orientation is the EXIF orientation (1–8) Beamfall applies itself with
	// FFmpeg's autorotation off; 0 leaves orientation to FFmpeg (HEIF/AVIF
	// transforms, formats without orientation, unparsed sources).
	orientation int
	colour      stillColour
}

func bytesReaderAt(b []byte) io.ReaderAt { return bytes.NewReader(b) }

func readSpan(r io.ReaderAt, size int64, span byteSpan, limit int64) []byte {
	if span.off < 0 || span.length <= 0 || span.length > limit || span.off > size || span.length > size-span.off {
		return nil
	}
	buf := make([]byte, span.length)
	if _, err := r.ReadAt(buf, span.off); err != nil {
		return nil
	}
	return buf
}

// inspectStill reads the source's structure. name is only an extension hint.
func inspectStill(r io.ReaderAt, size int64, name string) stillSource {
	head := make([]byte, 16)
	if n, _ := r.ReadAt(head, 0); n < 12 {
		return stillSource{}
	}
	hintRAW := rawExtensions[strings.ToLower(filepath.Ext(name))]
	switch {
	case head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF:
		return inspectJPEGFile(r, size)
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		return inspectPNG(r, size)
	case string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		return inspectWebP(r, size)
	case bytes.HasPrefix(head, []byte("FUJIFILMCCD-RAW")):
		return inspectRAF(r, size)
	case string(head[4:8]) == "ftyp":
		return inspectBMFF(r, size)
	case string(head[:2]) == "II" || string(head[:2]) == "MM":
		return inspectTIFF(r, size, hintRAW)
	}
	return stillSource{}
}

func inspectJPEGFile(r io.ReaderAt, size int64) stillSource {
	orientation, icc, complete := jpegMetadataAt(r, size)
	if complete {
		// Every header segment was read, so no orientation tag means upright.
		orientation = max(orientation, 1)
	}
	return stillSource{orientation: orientation, colour: iccColour(icc)}
}

func inspectTIFF(r io.ReaderAt, size int64, hintRAW bool) stillSource {
	scan, ok := scanTIFF(r, 0, size)
	if !ok {
		return stillSource{}
	}
	if !scan.raw && !hintRAW {
		src := stillSource{orientation: max(scan.orientation, 1)}
		src.colour = iccColour(readSpan(r, size, scan.icc, maxICCBytes))
		return src
	}
	return rawStill(r, size, scan.previews, scan.orientation)
}

// rawStill picks the RAW's best preview. The container orientation wins over
// the preview's own EXIF; the preview's ICC profile describes its pixels.
func rawStill(r io.ReaderAt, size int64, spans []byteSpan, orientation int) stillSource {
	preview := bestPreview(r, size, spans)
	if preview == nil {
		// No preview: FFmpeg decodes what it can of the container directly.
		return stillSource{orientation: max(orientation, 1)}
	}
	previewOrientation, icc := jpegMetadata(preview)
	if orientation == 0 {
		orientation = previewOrientation
	}
	return stillSource{preview: preview, orientation: max(orientation, 1), colour: iccColour(icc)}
}

// bestPreview returns the largest valid preview among the structured spans,
// falling back to a bounded scan of the file when none is at least
// smallPreviewSide on its long side. Spans are read once each, only when they
// start with a JPEG SOI, and within maxPreviewReadBytes in all; the scan's
// validation work is bounded by previewScanWork.
func bestPreview(r io.ReaderAt, size int64, spans []byteSpan) []byte {
	var best []byte
	var bestShape jpegShape
	consider := func(b []byte) {
		shape, ok := validJPEG(b)
		if !ok || shape.width*shape.height <= bestShape.width*bestShape.height {
			return
		}
		best, bestShape = b[:shape.length], shape
	}
	seen := map[byteSpan]bool{}
	budget := int64(maxPreviewReadBytes)
	soi := make([]byte, 3)
	for _, span := range spans {
		if seen[span] || span.length > budget {
			continue
		}
		seen[span] = true
		if _, err := r.ReadAt(soi, span.off); err != nil || !bytes.Equal(soi, jpegSOI) {
			continue
		}
		if b := readSpan(r, size, span, maxPreviewBytes); b != nil {
			budget -= int64(len(b))
			consider(b)
		}
	}
	if max(bestShape.width, bestShape.height) >= smallPreviewSide {
		return best
	}
	buf := readSpan(r, size, byteSpan{0, min(size, previewScanBytes)}, previewScanBytes)
	work := 0
	for pos := 0; pos+3 <= len(buf) && work < previewScanWork; {
		i := bytes.Index(buf[pos:], jpegSOI)
		if i < 0 {
			break
		}
		start := pos + i
		shape, walked, ok := walkJPEG(buf[start:min(len(buf), start+maxPreviewBytes)])
		work += walked
		if !ok {
			pos = start + 3
			continue
		}
		if shape.width*shape.height > bestShape.width*bestShape.height {
			best, bestShape = bytes.Clone(buf[start:start+shape.length]), shape
		}
		// Thumbnails nested in this stream's APP1 are smaller than it is.
		pos = start + shape.length
	}
	return best
}

var jpegSOI = []byte{0xFF, 0xD8, 0xFF}

// inspectRAF reads a Fujifilm RAF: a big-endian header whose bytes 84–91 give
// the offset and length of the embedded JPEG (with its own EXIF).
func inspectRAF(r io.ReaderAt, size int64) stillSource {
	head := make([]byte, 92)
	if _, err := r.ReadAt(head, 0); err != nil {
		return stillSource{}
	}
	span := byteSpan{int64(binary.BigEndian.Uint32(head[84:])), int64(binary.BigEndian.Uint32(head[88:]))}
	return rawStill(r, size, []byteSpan{span}, 0)
}

type bmffBox struct {
	typ     string
	payload byteSpan // after the size/type (and uuid) header
	uuid    []byte
}

// bmffBoxes lists the boxes inside span (bounded).
func bmffBoxes(r io.ReaderAt, span byteSpan) []bmffBox {
	var boxes []bmffBox
	pos, end := span.off, span.off+span.length
	for pos+8 <= end && len(boxes) < 256 {
		head := make([]byte, 32)
		n, _ := r.ReadAt(head, pos)
		if n < 8 {
			break
		}
		size := int64(binary.BigEndian.Uint32(head))
		typ := string(head[4:8])
		hdr := int64(8)
		switch size {
		case 0:
			size = end - pos
		case 1:
			if n < 16 {
				return boxes
			}
			large := binary.BigEndian.Uint64(head[8:])
			if large > uint64(end-pos) { // #nosec G115 -- end-pos is non-negative inside the loop
				return boxes
			}
			size = int64(large) // #nosec G115 -- bounded by the enclosing span just above
			hdr = 16
		}
		if size < hdr || size > end-pos {
			break
		}
		box := bmffBox{typ: typ}
		if typ == "uuid" {
			if size < hdr+16 || n < int(hdr)+16 {
				break
			}
			box.uuid = bytes.Clone(head[hdr : hdr+16])
			hdr += 16
		}
		box.payload = byteSpan{pos + hdr, size - hdr}
		boxes = append(boxes, box)
		pos += size
	}
	return boxes
}

func findBox(boxes []bmffBox, typ string, uuid []byte) (bmffBox, bool) {
	for _, b := range boxes {
		if b.typ == typ && (uuid == nil || bytes.Equal(b.uuid, uuid)) {
			return b, true
		}
	}
	return bmffBox{}, false
}

var (
	cr3PreviewUUID  = []byte{0xea, 0xf4, 0x2b, 0x5e, 0x1c, 0x98, 0x4b, 0x88, 0xb9, 0xfb, 0xb7, 0xdc, 0x40, 0x6e, 0x4d, 0x16}
	cr3MetadataUUID = []byte{0x85, 0xc0, 0xb6, 0x87, 0x82, 0x0f, 0x11, 0xe0, 0x81, 0x11, 0xf4, 0xce, 0x46, 0x2b, 0x6a, 0x48}
)

func inspectBMFF(r io.ReaderAt, size int64) stillSource {
	top := bmffBoxes(r, byteSpan{0, size})
	ftyp, ok := findBox(top, "ftyp", nil)
	if !ok || ftyp.payload.length < 8 || ftyp.payload.length > 4096 {
		return stillSource{}
	}
	brands := readSpan(r, size, ftyp.payload, 4096)
	for i := 0; i+4 <= len(brands); i += 4 {
		if i == 4 { // minor version
			continue
		}
		if string(brands[i:i+4]) == "crx " {
			return inspectCR3(r, size, top)
		}
	}
	return stillSource{colour: heifColour(r, size, top)}
}

// inspectCR3 reads a Canon CR3: the PRVW preview JPEG in its top-level preview
// uuid box, the full-size JPEG track in mdat, and IFD0 orientation from the
// CMT1 TIFF in moov's metadata uuid box.
func inspectCR3(r io.ReaderAt, size int64, top []bmffBox) stillSource {
	var spans []byteSpan
	if box, ok := findBox(top, "uuid", cr3PreviewUUID); ok && box.payload.length > 8 {
		// The payload is 8 bytes of header, then the PRVW box; its JPEG
		// starts at the first SOI inside it.
		prvw := readSpan(r, size, box.payload, maxPreviewBytes)
		if i := bytes.Index(prvw, jpegSOI); i >= 0 {
			spans = append(spans, byteSpan{box.payload.off + int64(i), box.payload.length - int64(i)})
		}
	}
	orientation := 0
	if moov, ok := findBox(top, "moov", nil); ok {
		children := bmffBoxes(r, moov.payload)
		// Each track's first sample is a candidate too: one of them is the
		// full-size JPEG (the others are CRX sensor data, which fails the SOI
		// check before it is read).
		tracks := 0
		for _, trak := range children {
			if trak.typ == "trak" && tracks < 8 {
				tracks++
				if span, ok := firstSample(r, trak); ok {
					spans = append(spans, span)
				}
			}
		}
		if meta, ok := findBox(children, "uuid", cr3MetadataUUID); ok {
			if cmt1, ok := findBox(bmffBoxes(r, meta.payload), "CMT1", nil); ok {
				if scan, ok := scanTIFF(r, cmt1.payload.off, cmt1.payload.off+cmt1.payload.length); ok {
					orientation = scan.orientation
				}
			}
		}
	}
	return rawStill(r, size, spans, orientation)
}

// firstSample returns the span of a track's first sample, from its sample
// table: the first chunk offset (stco or co64) and the first sample size (stsz).
func firstSample(r io.ReaderAt, trak bmffBox) (byteSpan, bool) {
	box := trak
	for _, typ := range []string{"mdia", "minf", "stbl"} {
		next, ok := findBox(bmffBoxes(r, box.payload), typ, nil)
		if !ok {
			return byteSpan{}, false
		}
		box = next
	}
	stbl := bmffBoxes(r, box.payload)
	stsz, ok := findBox(stbl, "stsz", nil)
	if !ok {
		return byteSpan{}, false
	}
	// stsz: version/flags, sample_size, sample_count, then per-sample sizes
	// when sample_size is 0.
	head := make([]byte, 16)
	n, _ := r.ReadAt(head, stsz.payload.off)
	if n < 12 || stsz.payload.length < 12 || binary.BigEndian.Uint32(head[8:]) == 0 {
		return byteSpan{}, false
	}
	length := binary.BigEndian.Uint32(head[4:])
	if length == 0 {
		if n < 16 || stsz.payload.length < 16 {
			return byteSpan{}, false
		}
		length = binary.BigEndian.Uint32(head[12:])
	}
	var off int64
	if stco, ok := findBox(stbl, "stco", nil); ok {
		if n, _ := r.ReadAt(head[:12], stco.payload.off); n < 12 || stco.payload.length < 12 || binary.BigEndian.Uint32(head[4:]) == 0 {
			return byteSpan{}, false
		}
		off = int64(binary.BigEndian.Uint32(head[8:]))
	} else if co64, ok := findBox(stbl, "co64", nil); ok {
		if n, _ := r.ReadAt(head, co64.payload.off); n < 16 || co64.payload.length < 16 || binary.BigEndian.Uint32(head[4:]) == 0 {
			return byteSpan{}, false
		}
		v := binary.BigEndian.Uint64(head[8:])
		if v > 1<<62 {
			return byteSpan{}, false
		}
		off = int64(v) // #nosec G115 -- bounded just above
	} else {
		return byteSpan{}, false
	}
	return byteSpan{off, int64(length)}, length > 0
}

// heifColour reads the colr property associated with a HEIF/AVIF file's
// primary item (meta → pitm, iprp → ipco/ipma); other items (thumbnails, depth
// or gain maps) can carry a different one. Without pitm or ipma the first colr
// is used. Orientation is left to FFmpeg, which applies irot/imir itself.
func heifColour(r io.ReaderAt, size int64, top []bmffBox) stillColour {
	meta, ok := findBox(top, "meta", nil)
	if !ok || meta.payload.length < 4 {
		return stillColour{}
	}
	// meta is a full box: skip version and flags.
	metaBoxes := bmffBoxes(r, byteSpan{meta.payload.off + 4, meta.payload.length - 4})
	iprp, ok := findBox(metaBoxes, "iprp", nil)
	if !ok {
		return stillColour{}
	}
	iprpBoxes := bmffBoxes(r, iprp.payload)
	ipco, ok := findBox(iprpBoxes, "ipco", nil)
	if !ok {
		return stillColour{}
	}
	// Property indices in ipma are 1-based positions in ipco.
	colrs := map[int]bmffBox{}
	first := 0
	for i, box := range bmffBoxes(r, ipco.payload) {
		if box.typ == "colr" {
			colrs[i+1] = box
			if first == 0 {
				first = i + 1
			}
		}
	}
	index := first
	primary, hasPrimary := heifPrimaryItem(r, size, metaBoxes)
	if ipma, ok := findBox(iprpBoxes, "ipma", nil); ok && hasPrimary {
		index = 0
		for _, p := range heifAssociations(r, size, ipma, primary) {
			if _, ok := colrs[p]; ok {
				index = p
				break
			}
		}
	}
	box, ok := colrs[index]
	if !ok {
		return stillColour{}
	}
	colr := readSpan(r, size, box.payload, maxICCBytes+4)
	if len(colr) < 4 {
		return stillColour{}
	}
	switch string(colr[:4]) {
	case "prof", "rICC":
		return iccColour(colr[4:])
	case "nclx":
		if len(colr) >= 8 {
			return colourFromNCLX(binary.BigEndian.Uint16(colr[4:]), binary.BigEndian.Uint16(colr[6:]))
		}
	}
	return stillColour{}
}

// heifPrimaryItem reads the pitm box's item ID.
func heifPrimaryItem(r io.ReaderAt, size int64, metaBoxes []bmffBox) (uint32, bool) {
	pitm, ok := findBox(metaBoxes, "pitm", nil)
	if !ok {
		return 0, false
	}
	b := readSpan(r, size, pitm.payload, 16)
	switch {
	case len(b) >= 6 && b[0] == 0:
		return uint32(binary.BigEndian.Uint16(b[4:])), true
	case len(b) >= 8:
		return binary.BigEndian.Uint32(b[4:]), true
	}
	return 0, false
}

// heifAssociations returns the 1-based ipco property indices an ipma box
// associates with item.
func heifAssociations(r io.ReaderAt, size int64, ipma bmffBox, item uint32) []int {
	b := readSpan(r, size, ipma.payload, 1<<20)
	if len(b) < 8 {
		return nil
	}
	version, wide := b[0], b[3]&1 == 1
	count := binary.BigEndian.Uint32(b[4:])
	pos := 8
	for e := uint32(0); e < count; e++ {
		var id uint32
		if version < 1 {
			if pos+2 > len(b) {
				return nil
			}
			id = uint32(binary.BigEndian.Uint16(b[pos:]))
			pos += 2
		} else {
			if pos+4 > len(b) {
				return nil
			}
			id = binary.BigEndian.Uint32(b[pos:])
			pos += 4
		}
		if pos >= len(b) {
			return nil
		}
		n := int(b[pos])
		pos++
		width := 1
		if wide {
			width = 2
		}
		if pos+n*width > len(b) {
			return nil
		}
		if id != item {
			pos += n * width
			continue
		}
		indices := make([]int, 0, n)
		for range n {
			if wide {
				indices = append(indices, int(binary.BigEndian.Uint16(b[pos:])&0x7FFF))
			} else {
				indices = append(indices, int(b[pos]&0x7F))
			}
			pos += width
		}
		return indices
	}
	return nil
}

// inspectPNG reads iCCP (unless an sRGB chunk declares sRGB) and eXIf from the
// chunks before IDAT, and eXIf after it.
func inspectPNG(r io.ReaderAt, size int64) stillSource {
	src := stillSource{orientation: 1}
	var icc []byte
	isSRGB, sawICCP := false, false
	pos := int64(8)
	for range 4096 {
		head := make([]byte, 8)
		if _, err := r.ReadAt(head, pos); err != nil {
			break
		}
		length := int64(binary.BigEndian.Uint32(head))
		typ := string(head[4:8])
		data := byteSpan{pos + 8, length}
		switch typ {
		case "iCCP":
			// PNG allows one iCCP; only the first is inflated.
			if !sawICCP {
				sawICCP = true
				icc = pngICC(readSpan(r, size, data, maxICCBytes))
			}
		case "sRGB":
			isSRGB = true
		case "eXIf":
			if o := tiffOrientation(readSpan(r, size, data, 1<<20)); o != 0 {
				src.orientation = o
			}
		case "IEND":
			pos = size
		}
		pos += 12 + length
		if pos >= size {
			break
		}
	}
	if !isSRGB {
		src.colour = iccColour(icc)
	}
	return src
}

func pngICC(chunk []byte) []byte {
	nul := bytes.IndexByte(chunk, 0)
	if nul < 0 || nul+2 > len(chunk) || chunk[nul+1] != 0 {
		return nil
	}
	zr, err := zlib.NewReader(bytes.NewReader(chunk[nul+2:]))
	if err != nil {
		return nil
	}
	defer func() { _ = zr.Close() }()
	icc, err := io.ReadAll(io.LimitReader(zr, maxICCBytes+1))
	if err != nil || len(icc) > maxICCBytes {
		return nil
	}
	return icc
}

// inspectWebP reads the ICCP and EXIF chunks of an extended (VP8X) WebP.
func inspectWebP(r io.ReaderAt, size int64) stillSource {
	src := stillSource{orientation: 1}
	pos := int64(12)
	for range 4096 {
		head := make([]byte, 8)
		if _, err := r.ReadAt(head, pos); err != nil {
			break
		}
		length := int64(binary.LittleEndian.Uint32(head[4:]))
		data := byteSpan{pos + 8, length}
		switch string(head[:4]) {
		case "ICCP":
			src.colour = iccColour(readSpan(r, size, data, maxICCBytes))
		case "EXIF":
			exif := readSpan(r, size, data, 1<<20)
			exif = bytes.TrimPrefix(exif, []byte("Exif\x00\x00"))
			if o := tiffOrientation(exif); o != 0 {
				src.orientation = o
			}
		}
		pos += 8 + length + length&1
		if pos >= size {
			break
		}
	}
	return src
}

// orientationFilter returns the filter that makes an EXIF-oriented image
// upright.
func orientationFilter(orientation int) string {
	switch orientation {
	case 2:
		return "hflip"
	case 3:
		return "hflip,vflip"
	case 4:
		return "vflip"
	case 5:
		return "transpose=cclock_flip"
	case 6:
		return "transpose=clock"
	case 7:
		return "transpose=clock_flip"
	case 8:
		return "transpose=cclock"
	}
	return ""
}
