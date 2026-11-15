package encderiv

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
)

const (
	// maxBufferedBox bounds the ftyp, moov and moof boxes held in memory while
	// they are parsed.
	maxBufferedBox = 16 << 20
	// maxReferences is the sidx reference_count ceiling.
	maxReferences = math.MaxUint16
	// sidxHeaderBytes and sidxReferenceBytes size a version-1 sidx box.
	sidxHeaderBytes    = 40
	sidxReferenceBytes = 12
	// tfhdBaseDataOffset marks an absolute base offset, which moving the
	// fragments by the index's length would break.
	tfhdBaseDataOffset = 0x000001
	sampleNonSync      = 0x00010000
)

var (
	// ErrInvalidOutput is returned when the transcoder's output is not a
	// fragmented MP4 this package can index and serve.
	ErrInvalidOutput = errors.New("encderiv: transcoder output is not a usable fragmented MP4")
	// ErrTooLarge is returned when the derivative exceeds the job's byte limit.
	ErrTooLarge = errors.New("encderiv: derivative exceeds the size limit")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidOutput}, args...)...)
}

// trackInfo is what the index needs from one moov trak and its trex.
type trackInfo struct {
	id        uint32
	timescale uint32
	handler   string
	trex      trexDefaults
	hasTrex   bool
}

type trexDefaults struct {
	duration uint32
	flags    uint32
}

// fragment is one moof's contribution from the reference track.
type fragment struct {
	offset     int64
	decodeTime uint64
	duration   uint64
	firstFlags uint32
	firstCTS   int64
}

// fragmentReader reads the transcoder's stdout and yields the derivative body:
// the stream with any top-level mfra (whose offsets the index would
// invalidate) removed. While it streams it hashes and counts the body and
// records each fragment so the caller can build a sidx afterwards. Media data
// passes through without being buffered; only ftyp, moov and moof are held,
// each bounded.
type fragmentReader struct {
	src      io.Reader
	maxBytes int64
	sum      hash.Hash
	n        int64

	pending []byte
	stream  int64

	seenFtyp, seenMoov, seenMoof, seenMfra bool
	done                                   bool
	err                                    error

	tracks      []trackInfo
	ref         *trackInfo
	insertAt    int64
	fragments   []fragment
	lastMdatEnd int64
}

func newFragmentReader(src io.Reader, maxBytes int64) *fragmentReader {
	return &fragmentReader{src: src, maxBytes: maxBytes, sum: sha256.New(), insertAt: -1}
}

func (f *fragmentReader) Read(p []byte) (int, error) {
	for {
		if f.err != nil {
			return 0, f.err
		}
		if len(p) == 0 {
			return 0, nil
		}
		if len(f.pending) > 0 {
			n := copy(p, f.pending)
			f.pending = f.pending[n:]
			f.account(p[:n])
			return n, nil
		}
		if f.stream > 0 {
			n, err := f.src.Read(p[:min(int64(len(p)), f.stream)])
			if n > 0 {
				f.stream -= int64(n)
				f.account(p[:n])
				return n, nil
			}
			if err == nil {
				continue
			}
			f.err = f.readFailure(err)
			continue
		}
		if f.done {
			return 0, io.EOF
		}
		if err := f.nextBox(); err != nil {
			f.err = err
		}
	}
}

func (f *fragmentReader) account(b []byte) {
	f.sum.Write(b)
	f.n += int64(len(b))
}

// readFailure maps a read error from the transcoder stream. End of stream
// inside a box is a truncated output; any other error (the transcoder's own
// failure, delivered through the pipe) is wrapped so it can never be mistaken
// for a clean end of stream downstream.
func (f *fragmentReader) readFailure(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return invalid("truncated box")
	}
	return fmt.Errorf("encderiv: transcoder output: %w", err)
}

func (f *fragmentReader) nextBox() error {
	var hdr [16]byte
	if _, err := io.ReadFull(f.src, hdr[:8]); err != nil {
		if errors.Is(err, io.EOF) {
			return f.finish()
		}
		return f.readFailure(err)
	}
	size := uint64(binary.BigEndian.Uint32(hdr[:4]))
	typ := string(hdr[4:8])
	headerLen := uint64(8)
	switch size {
	case 0:
		return invalid("box %q runs to end of stream", typ)
	case 1:
		if _, err := io.ReadFull(f.src, hdr[8:16]); err != nil {
			return f.readFailure(err)
		}
		size = binary.BigEndian.Uint64(hdr[8:16])
		headerLen = 16
	}
	if size < headerLen || size > math.MaxInt64 {
		return invalid("box %q has an impossible size", typ)
	}
	if !f.seenFtyp && typ != "ftyp" {
		return invalid("stream does not start with ftyp")
	}
	if f.seenMfra {
		return invalid("box %q follows mfra", typ)
	}
	boxLen := int64(size)
	if typ == "mfra" {
		if !f.seenMoof {
			return invalid("mfra before any fragment")
		}
		f.seenMfra = true
		if _, err := io.CopyN(io.Discard, f.src, boxLen-int64(headerLen)); err != nil {
			return f.readFailure(err)
		}
		return nil
	}
	if f.maxBytes > 0 && boxLen > f.maxBytes-f.n {
		return ErrTooLarge
	}
	switch typ {
	case "ftyp", "moov", "moof":
		return f.bufferBox(typ, hdr[:headerLen], boxLen)
	case "sidx":
		return invalid("transcoder wrote its own sidx")
	case "mdat":
		if f.seenMoof {
			f.lastMdatEnd = f.n + boxLen
		}
	}
	f.pending = append(f.pending[:0], hdr[:headerLen]...)
	f.stream = boxLen - int64(headerLen)
	return nil
}

func (f *fragmentReader) bufferBox(typ string, header []byte, boxLen int64) error {
	if boxLen > maxBufferedBox {
		return invalid("box %q exceeds %d bytes", typ, maxBufferedBox)
	}
	box := make([]byte, boxLen)
	copy(box, header)
	if _, err := io.ReadFull(f.src, box[len(header):]); err != nil {
		return f.readFailure(err)
	}
	payload := box[len(header):]
	var err error
	switch typ {
	case "ftyp":
		if f.seenFtyp {
			return invalid("second ftyp")
		}
		f.seenFtyp = true
	case "moov":
		if f.seenMoov || f.seenMoof {
			return invalid("moov out of order")
		}
		f.seenMoov = true
		err = f.parseMoov(payload)
	case "moof":
		if !f.seenMoov {
			return invalid("moof before moov")
		}
		if !f.seenMoof {
			f.insertAt = f.n
		}
		f.seenMoof = true
		err = f.parseMoof(payload, f.n)
	}
	if err != nil {
		return err
	}
	f.pending = box
	return nil
}

func (f *fragmentReader) finish() error {
	f.done = true
	switch {
	case !f.seenMoov:
		return invalid("no moov")
	case len(f.fragments) == 0:
		return invalid("no fragment carries the reference track")
	case f.lastMdatEnd <= f.fragments[len(f.fragments)-1].offset:
		return invalid("last fragment has no media data")
	}
	return nil
}

func (f *fragmentReader) parseMoov(payload []byte) error {
	trex := map[uint32]trexDefaults{}
	err := walkBoxes(payload, func(typ string, body []byte) error {
		switch typ {
		case "trak":
			track, err := parseTrak(body)
			if err != nil {
				return err
			}
			f.tracks = append(f.tracks, track)
		case "mvex":
			return walkBoxes(body, func(typ string, body []byte) error {
				if typ != "trex" {
					return nil
				}
				if len(body) < 24 {
					return invalid("short trex")
				}
				trex[be32(body[4:])] = trexDefaults{duration: be32(body[12:]), flags: be32(body[20:])}
				return nil
			})
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := range f.tracks {
		if d, ok := trex[f.tracks[i].id]; ok {
			f.tracks[i].trex, f.tracks[i].hasTrex = d, true
		}
	}
	for i := range f.tracks {
		if f.tracks[i].handler == "vide" {
			f.ref = &f.tracks[i]
			break
		}
	}
	if f.ref == nil && len(f.tracks) > 0 {
		f.ref = &f.tracks[0]
	}
	if f.ref == nil {
		return invalid("moov has no track")
	}
	if f.ref.timescale == 0 {
		return invalid("reference track has no timescale")
	}
	return nil
}

func parseTrak(body []byte) (trackInfo, error) {
	var track trackInfo
	err := walkBoxes(body, func(typ string, body []byte) error {
		switch typ {
		case "tkhd":
			v, ok := fullBoxVersion(body)
			off := 12
			if v == 1 {
				off = 20
			}
			if !ok || len(body) < off+4 {
				return invalid("short tkhd")
			}
			track.id = be32(body[off:])
		case "mdia":
			return walkBoxes(body, func(typ string, body []byte) error {
				switch typ {
				case "mdhd":
					v, ok := fullBoxVersion(body)
					off := 12
					if v == 1 {
						off = 20
					}
					if !ok || len(body) < off+4 {
						return invalid("short mdhd")
					}
					track.timescale = be32(body[off:])
				case "hdlr":
					if len(body) < 12 {
						return invalid("short hdlr")
					}
					track.handler = string(body[8:12])
				}
				return nil
			})
		}
		return nil
	})
	if err == nil && track.id == 0 {
		err = invalid("trak has no track_ID")
	}
	return track, err
}

func (f *fragmentReader) parseMoof(payload []byte, offset int64) error {
	frag := fragment{offset: offset}
	hasRef := false
	err := walkBoxes(payload, func(typ string, body []byte) error {
		if typ != "traf" {
			return nil
		}
		ref, ok, err := f.parseTraf(body)
		if err != nil || !ok {
			return err
		}
		if hasRef {
			return invalid("moof carries the reference track twice")
		}
		hasRef = true
		frag.decodeTime, frag.duration = ref.decodeTime, ref.duration
		frag.firstFlags, frag.firstCTS = ref.firstFlags, ref.firstCTS
		return nil
	})
	if err != nil {
		return err
	}
	if !hasRef {
		if len(f.fragments) == 0 {
			return invalid("first fragment lacks the reference track")
		}
		return nil
	}
	if len(f.fragments) >= maxReferences {
		return invalid("more than %d fragments", maxReferences)
	}
	f.fragments = append(f.fragments, frag)
	return nil
}

// parseTraf reads one traf. ok is false for a track other than the reference.
func (f *fragmentReader) parseTraf(body []byte) (fragment, bool, error) {
	var (
		frag       fragment
		sawTfhd    bool
		isRef      bool
		defDur     uint32
		defFlags   uint32
		hasDefDur  bool
		hasDefFlag bool
		firstDone  bool
	)
	err := walkBoxes(body, func(typ string, body []byte) error {
		switch typ {
		case "tfhd":
			if len(body) < 8 {
				return invalid("short tfhd")
			}
			flags := be24(body[1:])
			if flags&tfhdBaseDataOffset != 0 {
				return invalid("tfhd carries an absolute base_data_offset")
			}
			sawTfhd = true
			isRef = be32(body[4:]) == f.ref.id
			pos := 8
			for _, field := range []struct {
				flag uint32
				size int
			}{{0x02, 4}, {0x08, 4}, {0x10, 4}, {0x20, 4}} {
				if flags&field.flag == 0 {
					continue
				}
				if len(body) < pos+field.size {
					return invalid("short tfhd")
				}
				switch field.flag {
				case 0x08:
					defDur, hasDefDur = be32(body[pos:]), true
				case 0x20:
					defFlags, hasDefFlag = be32(body[pos:]), true
				}
				pos += field.size
			}
		case "tfdt":
			if !isRef {
				return nil
			}
			v, ok := fullBoxVersion(body)
			switch {
			case ok && v == 1 && len(body) >= 12:
				frag.decodeTime = binary.BigEndian.Uint64(body[4:])
			case ok && v == 0 && len(body) >= 8:
				frag.decodeTime = uint64(be32(body[4:]))
			default:
				return invalid("short tfdt")
			}
		case "trun":
			if !sawTfhd {
				return invalid("trun before tfhd")
			}
			if !isRef {
				return nil
			}
			defaults := trunDefaults{duration: defDur, flags: defFlags}
			if !hasDefDur {
				defaults.duration, defaults.hasDuration = f.ref.trex.duration, f.ref.hasTrex
			} else {
				defaults.hasDuration = true
			}
			if !hasDefFlag {
				defaults.flags, defaults.hasFlags = f.ref.trex.flags, f.ref.hasTrex
			} else {
				defaults.hasFlags = true
			}
			run, err := parseTrun(body, defaults)
			if err != nil {
				return err
			}
			if frag.duration > math.MaxUint64-run.duration {
				return invalid("fragment duration overflows")
			}
			frag.duration += run.duration
			if !firstDone && run.samples > 0 {
				frag.firstFlags, frag.firstCTS = run.firstFlags, run.firstCTS
				firstDone = true
			}
		}
		return nil
	})
	if err != nil {
		return fragment{}, false, err
	}
	if !sawTfhd {
		return fragment{}, false, invalid("traf has no tfhd")
	}
	if isRef && !firstDone {
		return fragment{}, false, invalid("reference fragment has no samples")
	}
	return frag, isRef, nil
}

type trunDefaults struct {
	duration, flags       uint32
	hasDuration, hasFlags bool
}

type trunSummary struct {
	samples    uint32
	duration   uint64
	firstFlags uint32
	firstCTS   int64
}

func parseTrun(body []byte, defaults trunDefaults) (trunSummary, error) {
	v, ok := fullBoxVersion(body)
	if !ok || len(body) < 8 {
		return trunSummary{}, invalid("short trun")
	}
	flags := be24(body[1:])
	count := be32(body[4:])
	pos := 8
	if flags&0x001 != 0 {
		pos += 4
	}
	firstFlags, hasFirstFlags := uint32(0), false
	if flags&0x004 != 0 {
		if len(body) < pos+4 {
			return trunSummary{}, invalid("short trun")
		}
		firstFlags, hasFirstFlags = be32(body[pos:]), true
		pos += 4
	}
	per := 0
	for _, bit := range []uint32{0x100, 0x200, 0x400, 0x800} {
		if flags&bit != 0 {
			per += 4
		}
	}
	if pos > len(body) || int64(count)*int64(per) > int64(len(body)-pos) {
		return trunSummary{}, invalid("trun sample table overruns its box")
	}
	if flags&0x100 == 0 && !defaults.hasDuration {
		return trunSummary{}, invalid("no sample duration for the reference track")
	}
	out := trunSummary{samples: count}
	for i := uint32(0); i < count; i++ {
		duration := defaults.duration
		sampleFlags := defaults.flags
		hasSampleFlags := defaults.hasFlags
		var cts int64
		if flags&0x100 != 0 {
			duration = be32(body[pos:])
			pos += 4
		}
		if flags&0x200 != 0 {
			pos += 4
		}
		if flags&0x400 != 0 {
			sampleFlags, hasSampleFlags = be32(body[pos:]), true
			pos += 4
		}
		if flags&0x800 != 0 {
			raw := be32(body[pos:])
			if v == 1 {
				cts = int64(int32(raw)) //nolint:gosec // version-1 trun offsets are signed by definition
			} else {
				cts = int64(raw)
			}
			pos += 4
		}
		out.duration += uint64(duration)
		if i == 0 {
			switch {
			case hasFirstFlags:
				out.firstFlags = firstFlags
			case hasSampleFlags:
				out.firstFlags = sampleFlags
			default:
				return trunSummary{}, invalid("no sample flags for the reference track")
			}
			out.firstCTS = cts
		}
	}
	return out, nil
}

// sidx builds the version-1 segment index for the body: one reference per
// fragment carrying the reference track, each running to the next such
// fragment and the last to the end of the last mdat. It is inserted
// immediately before the first fragment, so first_offset is zero and every
// referenced offset is measured from the end of the index.
func (f *fragmentReader) sidx() ([]byte, error) {
	if !f.done || f.err != nil || len(f.fragments) == 0 {
		return nil, invalid("index requested before a complete stream")
	}
	count := len(f.fragments)
	size := sidxHeaderBytes + sidxReferenceBytes*count
	out := make([]byte, size)
	binary.BigEndian.PutUint32(out[0:], uint32(size)) //nolint:gosec // bounded by maxReferences
	copy(out[4:], "sidx")
	out[8] = 1
	binary.BigEndian.PutUint32(out[12:], f.ref.id)
	binary.BigEndian.PutUint32(out[16:], f.ref.timescale)
	first := f.fragments[0]
	ept := first.decodeTime
	switch {
	case first.firstCTS > 0:
		ept += uint64(first.firstCTS)
	case first.firstCTS < 0:
		ept -= min(ept, uint64(-first.firstCTS))
	}
	binary.BigEndian.PutUint64(out[20:], ept)
	binary.BigEndian.PutUint64(out[28:], 0)
	binary.BigEndian.PutUint16(out[38:], uint16(count)) //nolint:gosec // bounded by maxReferences
	for i, frag := range f.fragments {
		end := f.lastMdatEnd
		if i+1 < count {
			end = f.fragments[i+1].offset
		}
		refSize := end - frag.offset
		if refSize <= 0 || refSize > math.MaxInt32 {
			return nil, invalid("fragment %d spans %d bytes", i, refSize)
		}
		if frag.duration > math.MaxUint32 {
			return nil, invalid("fragment %d duration overflows", i)
		}
		at := sidxHeaderBytes + sidxReferenceBytes*i
		binary.BigEndian.PutUint32(out[at:], uint32(refSize))
		binary.BigEndian.PutUint32(out[at+4:], uint32(frag.duration))
		var sap uint32
		if frag.firstFlags&sampleNonSync == 0 {
			sap = 1<<31 | 1<<28
		}
		binary.BigEndian.PutUint32(out[at+8:], sap)
	}
	return out, nil
}

// walkBoxes calls fn for each child box in b. A size of zero runs to the end
// of the container; anything that does not fit is refused.
func walkBoxes(b []byte, fn func(typ string, body []byte) error) error {
	for len(b) > 0 {
		if len(b) < 8 {
			return invalid("truncated child box")
		}
		size := uint64(be32(b))
		typ := string(b[4:8])
		headerLen := uint64(8)
		switch size {
		case 0:
			size = uint64(len(b))
		case 1:
			if len(b) < 16 {
				return invalid("truncated child box")
			}
			size = binary.BigEndian.Uint64(b[8:])
			headerLen = 16
		}
		if size < headerLen || size > uint64(len(b)) {
			return invalid("child box %q has an impossible size", typ)
		}
		if err := fn(typ, b[headerLen:size]); err != nil {
			return err
		}
		b = b[size:]
	}
	return nil
}

func fullBoxVersion(body []byte) (byte, bool) {
	if len(body) < 4 {
		return 0, false
	}
	return body[0], true
}

func be32(b []byte) uint32 { return binary.BigEndian.Uint32(b) }

func be24(b []byte) uint32 { return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2]) }
