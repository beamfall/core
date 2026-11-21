package ffmpeg

import (
	"encoding/binary"
	"io"
)

// TIFF structure reading for still-image inspection: orientation and ICC from
// IFD0, and the embedded JPEG previews a TIFF-family RAW (DNG, CR2, NEF, NRW,
// ARW, PEF, SRW, 3FR, ORF, RW2) carries.

const (
	tagCompression     = 0x0103
	tagPhotometric     = 0x0106
	tagStripOffsets    = 0x0111
	tagOrientation     = 0x0112
	tagStripByteCounts = 0x0117
	tagSubIFDs         = 0x014A
	tagJPEGOffset      = 0x0201
	tagJPEGLength      = 0x0202
	tagExifIFD         = 0x8769
	tagICCProfile      = 0x8773
	tagDNGVersion      = 0xC612
	tagRW2JpgFromRaw   = 0x002E

	maxIFDs       = 32
	maxIFDEntries = 1024
)

type tiffEntry struct {
	typ   uint16
	count uint32
	value []byte // the raw 4-byte value/offset field
}

type tiffReader struct {
	r     io.ReaderAt
	size  int64
	order binary.ByteOrder
	base  int64 // file offset of the TIFF header
	magic uint16
}

// newTIFFReader accepts classic TIFF byte orders with the standard magic (42)
// and the RAW variants (ORF "RO"/"RS", RW2 0x55).
func newTIFFReader(r io.ReaderAt, base, size int64) (*tiffReader, int64, bool) {
	head := make([]byte, 8)
	if _, err := r.ReadAt(head, base); err != nil {
		return nil, 0, false
	}
	var order binary.ByteOrder
	switch string(head[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return nil, 0, false
	}
	magic := order.Uint16(head[2:])
	switch magic {
	case 42, 0x4F52, 0x5352, 0x55:
	default:
		return nil, 0, false
	}
	return &tiffReader{r: r, size: size, order: order, base: base, magic: magic}, int64(order.Uint32(head[4:])), true
}

func (t *tiffReader) ifd(off int64) (map[uint16]tiffEntry, int64, bool) {
	if off < 8 || t.base+off+2 > t.size {
		return nil, 0, false
	}
	head := make([]byte, 2)
	if _, err := t.r.ReadAt(head, t.base+off); err != nil {
		return nil, 0, false
	}
	n := int(t.order.Uint16(head))
	if n == 0 || n > maxIFDEntries {
		return nil, 0, false
	}
	buf := make([]byte, n*12+4)
	if _, err := t.r.ReadAt(buf, t.base+off+2); err != nil {
		return nil, 0, false
	}
	entries := make(map[uint16]tiffEntry, n)
	for i := range n {
		e := buf[i*12:]
		tag := t.order.Uint16(e)
		if _, dup := entries[tag]; dup {
			continue
		}
		entries[tag] = tiffEntry{typ: t.order.Uint16(e[2:]), count: t.order.Uint32(e[4:]), value: e[8:12]}
	}
	return entries, int64(t.order.Uint32(buf[n*12:])), true
}

// uints returns up to limit SHORT/LONG/IFD values of an entry.
func (t *tiffReader) uints(e tiffEntry, limit int) []int64 {
	width := 0
	switch e.typ {
	case 3:
		width = 2
	case 4, 13:
		width = 4
	default:
		return nil
	}
	n := min(int(e.count), limit)
	if n == 0 {
		return nil
	}
	data := e.value
	if int64(width)*int64(e.count) > 4 {
		data = make([]byte, n*width)
		if _, err := t.r.ReadAt(data, t.base+int64(t.order.Uint32(e.value))); err != nil {
			return nil
		}
	}
	out := make([]int64, n)
	for i := range n {
		if width == 2 {
			out[i] = int64(t.order.Uint16(data[i*2:]))
		} else {
			out[i] = int64(t.order.Uint32(data[i*4:]))
		}
	}
	return out
}

func (t *tiffReader) first(entries map[uint16]tiffEntry, tag uint16) (int64, bool) {
	e, ok := entries[tag]
	if !ok {
		return 0, false
	}
	v := t.uints(e, 1)
	if len(v) == 0 {
		return 0, false
	}
	return v[0], true
}

// blob returns an UNDEFINED/BYTE entry's out-of-line bytes as a span.
func (t *tiffReader) blob(e tiffEntry) (off, length int64, ok bool) {
	if (e.typ != 7 && e.typ != 1) || e.count <= 4 {
		return 0, 0, false
	}
	return t.base + int64(t.order.Uint32(e.value)), int64(e.count), true
}

type byteSpan struct{ off, length int64 }

// tiffScan is what one walk of a TIFF's IFD tree reports.
type tiffScan struct {
	orientation int
	icc         byteSpan
	raw         bool // a RAW header magic, sensor-data IFD, DNGVersion or CR2 signature was seen
	previews    []byteSpan
}

// scanTIFF walks IFD0's chain, SubIFDs and the Exif IFD (bounded), collecting
// IFD0's orientation and ICC profile and every span that may hold a JPEG
// preview: JPEGInterchangeFormat, a single JPEG-compressed strip, and the RW2
// JpgFromRaw tag.
func scanTIFF(r io.ReaderAt, base, size int64) (tiffScan, bool) {
	t, first, ok := newTIFFReader(r, base, size)
	if !ok {
		return tiffScan{}, false
	}
	var scan tiffScan
	// The ORF and RW2 header magics are RAW signatures on their own.
	scan.raw = t.magic != 42
	if sig := make([]byte, 2); base == 0 {
		if _, err := r.ReadAt(sig, 8); err == nil && string(sig) == "CR" {
			scan.raw = true
		}
	}
	seen := map[int64]bool{}
	queue := []int64{first}
	for len(queue) > 0 && len(seen) < maxIFDs {
		off := queue[0]
		queue = queue[1:]
		if seen[off] {
			continue
		}
		seen[off] = true
		entries, next, ok := t.ifd(off)
		if !ok {
			if off == first {
				return tiffScan{}, false
			}
			continue
		}
		if off == first {
			if v, ok := t.first(entries, tagOrientation); ok && v >= 1 && v <= 8 {
				scan.orientation = int(v)
			}
			if e, ok := entries[tagICCProfile]; ok {
				if o, n, ok := t.blob(e); ok && n <= maxICCBytes {
					scan.icc = byteSpan{o, n}
				}
			}
			if _, ok := entries[tagDNGVersion]; ok {
				scan.raw = true
			}
		}
		if v, ok := t.first(entries, tagPhotometric); ok && (v == 32803 || v == 34892) {
			scan.raw = true
		}
		if o, ok := t.first(entries, tagJPEGOffset); ok {
			if n, ok := t.first(entries, tagJPEGLength); ok && n > 0 {
				scan.previews = append(scan.previews, byteSpan{t.base + o, n})
			}
		}
		if c, ok := t.first(entries, tagCompression); ok && (c == 6 || c == 7) {
			offs := t.uints(entries[tagStripOffsets], 2)
			lens := t.uints(entries[tagStripByteCounts], 2)
			if len(offs) == 1 && len(lens) == 1 && lens[0] > 0 {
				scan.previews = append(scan.previews, byteSpan{t.base + offs[0], lens[0]})
			}
		}
		if e, ok := entries[tagRW2JpgFromRaw]; ok {
			if o, n, ok := t.blob(e); ok {
				scan.previews = append(scan.previews, byteSpan{o, n})
			}
		}
		if e, ok := entries[tagSubIFDs]; ok {
			queue = append(queue, t.uints(e, 8)...)
		}
		if o, ok := t.first(entries, tagExifIFD); ok {
			queue = append(queue, o)
		}
		if next != 0 {
			queue = append(queue, next)
		}
	}
	return scan, true
}

// tiffOrientation reads IFD0's orientation from an in-memory TIFF (an Exif
// payload). 0 means absent or invalid.
func tiffOrientation(b []byte) int {
	r := bytesReaderAt(b)
	t, first, ok := newTIFFReader(r, 0, int64(len(b)))
	if !ok {
		return 0
	}
	entries, _, ok := t.ifd(first)
	if !ok {
		return 0
	}
	if v, ok := t.first(entries, tagOrientation); ok && v >= 1 && v <= 8 {
		return int(v)
	}
	return 0
}
