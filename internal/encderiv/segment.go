package encderiv

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"time"
)

// defaultMaxSegmentBytes bounds one buffered HLS segment when the caller sets
// no limit.
const defaultMaxSegmentBytes = 64 << 20

// Segment describes one HLS media segment cut from a fragmented MP4. Start is
// measured from the first fragment's decode time on the reference track.
type Segment struct {
	Index    int
	Start    time.Duration
	Duration time.Duration
	Size     int64
}

// SegmentOptions configures SegmentFMP4. Target is the segment cadence the
// transcoder forced keyframes on; MaxSegmentBytes bounds the one segment held
// in memory (64 MiB when zero).
type SegmentOptions struct {
	Target          time.Duration
	MaxSegmentBytes int64
}

// SegmentSink receives the segmenter's output in order: Init once with the
// ftyp and moov, then Segment once per segment with its moof and mdat boxes.
// The byte slices are reused after each call returns.
type SegmentSink struct {
	Init    func([]byte) error
	Segment func(Segment, []byte) error
}

// SegmentFMP4 splits a fragmented MP4 (the layout Result.Open returns) into an
// HLS init section and media segments without touching disk. A segment is cut
// at the first reference-track fragment that starts on a sync sample at or
// beyond the next multiple of Target; fragments that carry only other tracks
// stay with the segment before them. The index before the first fragment, any
// free or skip box and a trailing mfra are dropped. Only the init section and
// the current segment are held in memory, each bounded. It returns the
// segments it emitted.
func SegmentFMP4(ctx context.Context, src io.Reader, opts SegmentOptions, sink SegmentSink) ([]Segment, error) {
	if opts.Target <= 0 || sink.Init == nil || sink.Segment == nil {
		return nil, errors.New("encderiv: segmenter needs a target duration and both sink functions")
	}
	limit := opts.MaxSegmentBytes
	if limit <= 0 {
		limit = defaultMaxSegmentBytes
	}
	s := &segmenter{ctx: ctx, src: src, opts: opts, limit: limit, sink: sink}
	if err := s.run(); err != nil {
		return nil, err
	}
	return s.segments, nil
}

type segmenter struct {
	ctx   context.Context
	src   io.Reader
	opts  SegmentOptions
	limit int64
	sink  SegmentSink

	parser   fragmentReader
	init     []byte
	buf      []byte
	segments []Segment

	seenFtyp, seenMoov, seenSidx, seenMfra bool
	needMdat                               bool
	open                                   bool
	first                                  uint64
	segStart                               uint64
	lastEnd                                uint64
	haveRef                                bool
}

func (s *segmenter) run() error {
	for {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		typ, header, boxLen, err := s.readHeader()
		if errors.Is(err, io.EOF) {
			return s.finish()
		}
		if err != nil {
			return err
		}
		if err := s.box(typ, header, boxLen); err != nil {
			return err
		}
	}
}

func (s *segmenter) readHeader() (string, []byte, int64, error) {
	var hdr [16]byte
	if _, err := io.ReadFull(s.src, hdr[:8]); err != nil {
		if errors.Is(err, io.EOF) {
			return "", nil, 0, io.EOF
		}
		return "", nil, 0, s.parser.readFailure(err)
	}
	size := uint64(binary.BigEndian.Uint32(hdr[:4]))
	typ := string(hdr[4:8])
	headerLen := uint64(8)
	switch size {
	case 0:
		return "", nil, 0, invalid("box %q runs to end of stream", typ)
	case 1:
		if _, err := io.ReadFull(s.src, hdr[8:16]); err != nil {
			return "", nil, 0, s.parser.readFailure(err)
		}
		size = binary.BigEndian.Uint64(hdr[8:16])
		headerLen = 16
	}
	if size < headerLen || size > math.MaxInt64 {
		return "", nil, 0, invalid("box %q has an impossible size", typ)
	}
	return typ, append([]byte(nil), hdr[:headerLen]...), int64(size), nil
}

func (s *segmenter) box(typ string, header []byte, boxLen int64) error {
	if !s.seenFtyp && typ != "ftyp" {
		return invalid("stream does not start with ftyp")
	}
	if s.seenMfra {
		return invalid("box %q follows mfra", typ)
	}
	if s.needMdat && typ != "mdat" {
		return invalid("moof is not followed by mdat")
	}
	switch typ {
	case "ftyp", "moov":
		if typ == "ftyp" && s.seenFtyp || typ == "moov" && (s.seenMoov || !s.seenFtyp) {
			return invalid("%s out of order", typ)
		}
		box, err := s.readBox(header, boxLen, maxBufferedBox)
		if err != nil {
			return err
		}
		if typ == "ftyp" {
			s.seenFtyp = true
			s.init = box
			return nil
		}
		if err := s.parser.parseMoov(box[len(header):]); err != nil {
			return err
		}
		s.seenMoov = true
		s.init = append(s.init, box...)
		return s.sink.Init(s.init)
	case "sidx":
		if !s.seenMoov || s.seenSidx || s.open {
			return invalid("sidx out of place")
		}
		s.seenSidx = true
		return s.skip(header, boxLen)
	case "free", "skip":
		return s.skip(header, boxLen)
	case "mfra":
		if !s.open {
			return invalid("mfra before any fragment")
		}
		s.seenMfra = true
		return s.skip(header, boxLen)
	case "moof":
		if !s.seenMoov {
			return invalid("moof before moov")
		}
		return s.moof(header, boxLen)
	case "mdat":
		if !s.needMdat {
			return invalid("mdat without a moof")
		}
		s.needMdat = false
		return s.appendBox(header, boxLen)
	}
	return invalid("unexpected box %q", typ)
}

func (s *segmenter) moof(header []byte, boxLen int64) error {
	box, err := s.readBox(header, boxLen, maxBufferedBox)
	if err != nil {
		return err
	}
	s.parser.fragments = s.parser.fragments[:0]
	if err := s.parser.parseMoof(box[len(header):], 0); err != nil {
		return err
	}
	s.needMdat = true
	if len(s.parser.fragments) == 0 {
		if !s.open {
			return invalid("first fragment lacks the reference track")
		}
		return s.appendBytes(box)
	}
	frag := s.parser.fragments[0]
	if s.haveRef && frag.decodeTime < s.lastEnd {
		return invalid("fragment decode time goes backwards")
	}
	if frag.duration > math.MaxUint64-frag.decodeTime {
		return invalid("fragment duration overflows")
	}
	if !s.haveRef {
		s.haveRef = true
		s.first = frag.decodeTime
		s.segStart = frag.decodeTime
		s.open = true
	} else if frag.firstFlags&sampleNonSync == 0 && s.cutsAt(frag.decodeTime) {
		if err := s.flush(frag.decodeTime); err != nil {
			return err
		}
		s.segStart = frag.decodeTime
	}
	s.lastEnd = frag.decodeTime + frag.duration
	return s.appendBytes(box)
}

// cutsAt reports whether a sync fragment at decodeTime starts a new segment:
// it does once it reaches the multiple of Target that follows the current
// segment's start.
func (s *segmenter) cutsAt(decodeTime uint64) bool {
	target := s.opts.Target
	start := s.ticksToDuration(s.segStart - s.first)
	next := time.Duration(math.MaxInt64)
	if start/target < math.MaxInt64/target-1 {
		next = (start/target + 1) * target
	}
	return s.ticksToDuration(decodeTime-s.first) >= next
}

func (s *segmenter) ticksToDuration(ticks uint64) time.Duration {
	timescale := uint64(s.parser.ref.timescale)
	secs, rem := ticks/timescale, ticks%timescale
	if secs >= math.MaxInt64/uint64(time.Second) {
		return math.MaxInt64
	}
	return time.Duration(secs)*time.Second + time.Duration(rem*uint64(time.Second)/timescale) //nolint:gosec // rem < 2^32 bounds the product
}

func (s *segmenter) flush(end uint64) error {
	if len(s.buf) == 0 {
		return nil
	}
	if len(s.segments) >= maxReferences {
		return invalid("more than %d segments", maxReferences)
	}
	start := s.ticksToDuration(s.segStart - s.first)
	seg := Segment{
		Index:    len(s.segments),
		Start:    start,
		Duration: s.ticksToDuration(end-s.first) - start,
		Size:     int64(len(s.buf)),
	}
	if err := s.sink.Segment(seg, s.buf); err != nil {
		return err
	}
	s.segments = append(s.segments, seg)
	s.buf = s.buf[:0]
	return nil
}

func (s *segmenter) finish() error {
	switch {
	case !s.seenMoov:
		return invalid("no moov")
	case !s.open:
		return invalid("no fragment carries the reference track")
	case s.needMdat:
		return invalid("last fragment has no media data")
	}
	return s.flush(s.lastEnd)
}

func (s *segmenter) readBox(header []byte, boxLen, limit int64) ([]byte, error) {
	if boxLen > limit {
		return nil, invalid("box exceeds %d bytes", limit)
	}
	box := make([]byte, boxLen)
	copy(box, header)
	if _, err := io.ReadFull(s.src, box[len(header):]); err != nil {
		return nil, s.parser.readFailure(err)
	}
	return box, nil
}

func (s *segmenter) appendBytes(b []byte) error {
	if int64(len(b)) > s.limit-int64(len(s.buf)) {
		return ErrTooLarge
	}
	s.buf = append(s.buf, b...)
	return nil
}

func (s *segmenter) appendBox(header []byte, boxLen int64) error {
	if boxLen > s.limit-int64(len(s.buf)) {
		return ErrTooLarge
	}
	at := len(s.buf)
	s.buf = append(s.buf, header...)
	s.buf = append(s.buf, make([]byte, boxLen-int64(len(header)))...)
	if _, err := io.ReadFull(s.src, s.buf[at+len(header):]); err != nil {
		return s.parser.readFailure(err)
	}
	return nil
}

func (s *segmenter) skip(header []byte, boxLen int64) error {
	if _, err := io.CopyN(io.Discard, s.src, boxLen-int64(len(header))); err != nil {
		return s.parser.readFailure(err)
	}
	return nil
}
