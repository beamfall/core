package ffmpeg

import (
	"bytes"
	"encoding/binary"
	"io"
)

// jpegShape is what validating a JPEG stream reports.
type jpegShape struct {
	width, height int
	length        int // bytes from SOI through EOI
}

// validJPEG walks a JPEG stream starting at SOI and reports its frame size and
// length. Only baseline (SOF0), extended sequential (SOF1) and progressive
// (SOF2) Huffman frames qualify: a lossless SOF3 stream is the raw sensor strip
// of a CR2/DNG, not a viewable preview, and arithmetic or hierarchical frames are
// not decodable everywhere. The entropy-coded data of every scan is walked
// (stuffed bytes and restart markers skipped) until EOI.
func validJPEG(b []byte) (jpegShape, bool) {
	shape, _, ok := walkJPEG(b)
	return shape, ok
}

// walkJPEG is validJPEG that also reports how many bytes it examined, which
// bounds the work of a scan that validates many candidates.
func walkJPEG(b []byte) (jpegShape, int, bool) {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return jpegShape{}, 2, false
	}
	var shape jpegShape
	sawFrame, sawScan := false, false
	pos := 2
	for pos+1 < len(b) {
		if b[pos] != 0xFF {
			return jpegShape{}, pos + 2, false
		}
		marker := b[pos+1]
		switch {
		case marker == 0xFF:
			pos++
			continue
		case marker == 0xD9:
			if !sawFrame || !sawScan {
				return jpegShape{}, pos + 2, false
			}
			shape.length = pos + 2
			return shape, shape.length, true
		case marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7):
			pos += 2
			continue
		case marker == 0xD8 || marker == 0x00:
			return jpegShape{}, pos + 2, false
		}
		if pos+4 > len(b) {
			return jpegShape{}, pos + 2, false
		}
		length := int(binary.BigEndian.Uint16(b[pos+2:]))
		end := pos + 2 + length
		if length < 2 || end > len(b) {
			return jpegShape{}, pos + 2, false
		}
		segment := b[pos+4 : end]
		switch {
		case marker == 0xC0 || marker == 0xC1 || marker == 0xC2:
			if sawFrame || len(segment) < 6 {
				return jpegShape{}, pos + 2, false
			}
			shape.height = int(binary.BigEndian.Uint16(segment[1:]))
			shape.width = int(binary.BigEndian.Uint16(segment[3:]))
			if components := segment[5]; shape.width == 0 || shape.height == 0 || (components != 1 && components != 3) {
				return jpegShape{}, pos + 2, false
			}
			sawFrame = true
		case marker >= 0xC3 && marker <= 0xCF && marker != 0xC4 && marker != 0xC8 && marker != 0xCC:
			return jpegShape{}, pos + 2, false
		case marker == 0xDA:
			if !sawFrame {
				return jpegShape{}, pos + 2, false
			}
			sawScan = true
			next, ok := skipEntropyData(b, end)
			if !ok {
				return jpegShape{}, len(b), false
			}
			pos = next
			continue
		}
		pos = end
	}
	return jpegShape{}, pos + 2, false
}

// skipEntropyData returns the offset of the first marker after entropy-coded
// data starting at pos.
func skipEntropyData(b []byte, pos int) (int, bool) {
	for {
		i := bytes.IndexByte(b[pos:], 0xFF)
		if i < 0 {
			return 0, false
		}
		pos += i
		if pos+1 >= len(b) {
			return 0, false
		}
		next := b[pos+1]
		if next == 0x00 || (next >= 0xD0 && next <= 0xD7) || next == 0xFF {
			pos++
			if next != 0xFF {
				pos++
			}
			continue
		}
		return pos, true
	}
}

// maxJPEGSegments bounds the header-segment walk of jpegMetadataAt.
const maxJPEGSegments = 4096

// jpegMetadata reads the orientation (APP1 Exif) and ICC profile (APP2,
// reassembled in sequence order) from an in-memory JPEG's header segments.
func jpegMetadata(b []byte) (orientation int, icc []byte) {
	orientation, icc, _ = jpegMetadataAt(bytesReaderAt(b), int64(len(b)))
	return orientation, icc
}

// jpegMetadataAt walks a JPEG's header segments through r, reading only the
// APP1/APP2 payloads, so metadata after a large prefix of other segments is
// still found. complete reports that the walk reached the first scan: when it
// is false, an orientation of 0 means unknown rather than absent.
func jpegMetadataAt(r io.ReaderAt, size int64) (orientation int, icc []byte, complete bool) {
	head := make([]byte, 4)
	if n, _ := r.ReadAt(head, 0); n < 2 || head[0] != 0xFF || head[1] != 0xD8 {
		return 0, nil, false
	}
	chunks := map[byte][]byte{}
	total := 0
	exifSeen := false
	pos := int64(2)
	for range maxJPEGSegments {
		if n, _ := r.ReadAt(head, pos); n < 2 || head[0] != 0xFF {
			break
		}
		marker := head[1]
		if marker == 0xFF {
			pos++
			continue
		}
		if marker == 0xDA || marker == 0xD9 {
			complete = true
			break
		}
		if pos+4 > size {
			break
		}
		length := int64(binary.BigEndian.Uint16(head[2:]))
		end := pos + 2 + length
		if length < 2 || end > size {
			break
		}
		// Only the first Exif segment and each ICC chunk once are read whole,
		// after their signature, so the walk reads a bounded total.
		exif := marker == 0xE1 && !exifSeen
		iccChunk := marker == 0xE2 && total+int(length) <= maxICCBytes
		if exif || iccChunk {
			prefix := make([]byte, min(length-2, int64(len(iccMarker)+2)))
			if _, err := r.ReadAt(prefix, pos+4); err != nil {
				break
			}
			wanted := false
			switch {
			case exif && bytes.HasPrefix(prefix, []byte("Exif\x00\x00")):
				exifSeen, wanted = true, true
			case iccChunk && bytes.HasPrefix(prefix, iccMarker) && len(prefix) == len(iccMarker)+2 && length-2 > int64(len(prefix)):
				_, dup := chunks[prefix[len(iccMarker)]]
				wanted = !dup
			}
			if wanted {
				segment := make([]byte, length-2)
				if _, err := r.ReadAt(segment, pos+4); err != nil {
					break
				}
				if exif {
					orientation = tiffOrientation(segment[6:])
				} else {
					chunks[segment[len(iccMarker)]] = segment[len(iccMarker)+2:]
					total += len(segment)
				}
			}
		}
		pos = end
	}
	for seq := 1; seq <= len(chunks); seq++ {
		chunk, ok := chunks[byte(seq)]
		if !ok {
			return orientation, nil, complete
		}
		icc = append(icc, chunk...)
	}
	return orientation, icc, complete
}
