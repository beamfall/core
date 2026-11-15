package encderiv

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"testing/iotest"
)

// A synthetic fragmented MP4, shaped like FFmpeg's
// frag_keyframe+empty_moov+default_base_moof output: ftyp, a moov with an audio
// and a video trak and their trex defaults, then moof/mdat pairs and an
// optional trailing mfra.

func mp4Box(typ string, parts ...[]byte) []byte {
	size := 8
	for _, p := range parts {
		size += len(p)
	}
	out := make([]byte, 8, size)
	binary.BigEndian.PutUint32(out, uint32(size)) //nolint:gosec // test boxes are small
	copy(out[4:], typ)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func mp4FullBox(typ string, version byte, flags uint32, parts ...[]byte) []byte {
	head := binary.BigEndian.AppendUint32(nil, uint32(version)<<24|flags&0xffffff)
	return mp4Box(typ, append([][]byte{head}, parts...)...)
}

func u32b(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }
func u64b(v uint64) []byte { return binary.BigEndian.AppendUint64(nil, v) }

const (
	testVideoID        = 1
	testAudioID        = 2
	testVideoTimescale = 12288
	testFrameDuration  = 512 // 24 fps at 12288
	testSyncFlags      = 0x02000000
	testNonSyncFlags   = 0x01000000 | sampleNonSync
)

type synthFragment struct {
	decodeTime uint64
	samples    int
	firstFlags uint32
	firstCTS   uint32
	payload    []byte
}

func synthTrak(id, timescale uint32, handler string) []byte {
	tkhd := mp4FullBox("tkhd", 0, 3, u32b(0), u32b(0), u32b(id), make([]byte, 68))
	mdhd := mp4FullBox("mdhd", 0, 0, u32b(0), u32b(0), u32b(timescale), u32b(0), make([]byte, 4))
	hdlr := mp4FullBox("hdlr", 0, 0, u32b(0), []byte(handler), make([]byte, 13))
	return mp4Box("trak", tkhd, mp4Box("mdia", mdhd, hdlr))
}

func synthTrex(id, duration, flags uint32) []byte {
	return mp4FullBox("trex", 0, 0, u32b(id), u32b(1), u32b(duration), u32b(0), u32b(flags))
}

func synthFtyp() []byte { return mp4Box("ftyp", []byte("iso5"), u32b(512), []byte("iso5iso6mp41")) }

func synthMoov() []byte {
	return mp4Box("moov",
		mp4FullBox("mvhd", 0, 0, make([]byte, 96)),
		// Audio comes first so the vide handler, not trak order, picks the
		// reference track.
		synthTrak(testAudioID, 48000, "soun"),
		synthTrak(testVideoID, testVideoTimescale, "vide"),
		mp4Box("mvex", synthTrex(testVideoID, 0, sampleNonSync), synthTrex(testAudioID, 1024, 0)),
	)
}

func synthVideoTraf(frag synthFragment) []byte {
	var samples []byte
	for i := range frag.samples {
		cts := uint32(0)
		if i == 0 {
			cts = frag.firstCTS
		}
		samples = append(samples, u32b(testFrameDuration)...)
		samples = append(samples, u32b(cts)...)
	}
	return mp4Box("traf",
		mp4FullBox("tfhd", 0, 0x020000, u32b(testVideoID)),
		mp4FullBox("tfdt", 1, 0, u64b(frag.decodeTime)),
		mp4FullBox("trun", 0, 0x001|0x004|0x100|0x800,
			u32b(uint32(frag.samples)), u32b(0), u32b(frag.firstFlags), samples), //nolint:gosec // small test counts
	)
}

func synthAudioTraf(decodeTime uint64) []byte {
	return mp4Box("traf",
		mp4FullBox("tfhd", 0, 0x020008, u32b(testAudioID), u32b(1024)),
		mp4FullBox("tfdt", 1, 0, u64b(decodeTime)),
		mp4FullBox("trun", 0, 0x001|0x200, u32b(2), u32b(0), u32b(10), u32b(10)),
	)
}

func synthMoof(seq uint32, frag synthFragment) []byte {
	return mp4Box("moof", mp4FullBox("mfhd", 0, 0, u32b(seq)), synthAudioTraf(frag.decodeTime*4), synthVideoTraf(frag))
}

func synthMfra() []byte {
	return mp4Box("mfra",
		mp4FullBox("tfra", 1, 0, u32b(testVideoID), u32b(0), u32b(0)),
		mp4FullBox("mfro", 0, 0, u32b(16)))
}

// synthStream is a built stream and what a correct scan of it must report.
type synthStream struct {
	raw       []byte // what the transcoder writes
	body      []byte // what the scanner yields: raw without its mfra
	insertAt  int64
	moofAt    []int64
	durations []uint64
}

func buildSynth(frags []synthFragment, withMfra bool) synthStream {
	s := synthStream{raw: append(synthFtyp(), synthMoov()...)}
	s.insertAt = int64(len(s.raw))
	for i, f := range frags {
		s.moofAt = append(s.moofAt, int64(len(s.raw)))
		s.raw = append(s.raw, synthMoof(uint32(i+1), f)...) //nolint:gosec // small test counts
		s.raw = append(s.raw, mp4Box("mdat", f.payload)...)
		s.durations = append(s.durations, uint64(f.samples*testFrameDuration)) //nolint:gosec // small test counts
	}
	s.body = bytes.Clone(s.raw)
	if withMfra {
		s.raw = append(s.raw, synthMfra()...)
	}
	return s
}

// synthFrags splits payload across three fragments: two opening on a sync
// sample and a last one opening on a non-sync sample.
func synthFrags(payload []byte) []synthFragment {
	third := len(payload) / 3
	return []synthFragment{
		{decodeTime: 0, samples: 24, firstFlags: testSyncFlags, firstCTS: 1024, payload: payload[:third]},
		{decodeTime: 24 * testFrameDuration, samples: 24, firstFlags: testSyncFlags, payload: payload[third : 2*third]},
		{decodeTime: 48 * testFrameDuration, samples: 12, firstFlags: testNonSyncFlags, payload: payload[2*third:]},
	}
}

func testPayload(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(i*7 + i>>9)
	}
	return out
}

func finalLayout(body, index []byte, insertAt int64) []byte {
	out := append([]byte(nil), body[:insertAt]...)
	out = append(out, index...)
	return append(out, body[insertAt:]...)
}

type topBox struct {
	typ    string
	offset int64
	size   int64
}

func topLevelBoxes(t *testing.T, b []byte) []topBox {
	t.Helper()
	var boxes []topBox
	for pos := int64(0); pos < int64(len(b)); {
		if int64(len(b))-pos < 8 {
			t.Fatalf("trailing %d bytes at %d", int64(len(b))-pos, pos)
		}
		size := int64(binary.BigEndian.Uint32(b[pos:]))
		typ := string(b[pos+4 : pos+8])
		if size == 1 {
			size = int64(binary.BigEndian.Uint64(b[pos+8:])) //nolint:gosec // test stream
		}
		if size < 8 || pos+size > int64(len(b)) {
			t.Fatalf("box %q at %d has size %d", typ, pos, size)
		}
		boxes = append(boxes, topBox{typ: typ, offset: pos, size: size})
		pos += size
	}
	return boxes
}

type sidxReference struct {
	size     uint32
	duration uint32
	sap      uint32
}

type sidxInfo struct {
	referenceID uint32
	timescale   uint32
	ept         uint64
	refs        []sidxReference
}

// checkFinalLayout asserts that final carries exactly one version-1 sidx,
// placed immediately before the first moof, whose references start at the end
// of the index, land on every moof and nothing else, and end exactly at the end
// of the last mdat.
func checkFinalLayout(t *testing.T, final []byte) sidxInfo {
	t.Helper()
	boxes := topLevelBoxes(t, final)
	if len(boxes) == 0 || boxes[0].typ != "ftyp" {
		t.Fatalf("final layout does not start with ftyp: %+v", boxes)
	}
	sidxAt := -1
	moofs := map[int64]bool{}
	for i, box := range boxes {
		switch box.typ {
		case "sidx":
			if sidxAt >= 0 {
				t.Fatal("more than one sidx")
			}
			sidxAt = i
		case "moof":
			if sidxAt < 0 {
				t.Fatalf("moof at %d precedes the sidx", box.offset)
			}
			moofs[box.offset] = true
		case "mfra":
			t.Fatal("final layout keeps an mfra")
		}
	}
	if sidxAt < 0 || sidxAt+1 >= len(boxes) || boxes[sidxAt+1].typ != "moof" {
		t.Fatalf("sidx is not immediately before the first moof: %+v", boxes)
	}
	box := boxes[sidxAt]
	b := final[box.offset : box.offset+box.size]
	if b[8] != 1 {
		t.Fatalf("sidx version = %d, want 1", b[8])
	}
	info := sidxInfo{
		referenceID: binary.BigEndian.Uint32(b[12:]),
		timescale:   binary.BigEndian.Uint32(b[16:]),
		ept:         binary.BigEndian.Uint64(b[20:]),
	}
	if first := binary.BigEndian.Uint64(b[28:]); first != 0 {
		t.Fatalf("first_offset = %d, want 0", first)
	}
	count := int(binary.BigEndian.Uint16(b[38:]))
	if int64(sidxHeaderBytes+sidxReferenceBytes*count) != box.size {
		t.Fatalf("sidx size %d does not fit %d references", box.size, count)
	}
	pos := box.offset + box.size
	referenced := map[int64]bool{}
	for i := range count {
		at := sidxHeaderBytes + sidxReferenceBytes*i
		ref := sidxReference{
			size:     binary.BigEndian.Uint32(b[at:]),
			duration: binary.BigEndian.Uint32(b[at+4:]),
			sap:      binary.BigEndian.Uint32(b[at+8:]),
		}
		if ref.size>>31 != 0 {
			t.Fatalf("reference %d points at a sub-index", i)
		}
		if !moofs[pos] {
			t.Fatalf("reference %d offset %d does not land on a moof", i, pos)
		}
		referenced[pos] = true
		info.refs = append(info.refs, ref)
		pos += int64(ref.size)
	}
	for offset := range moofs {
		if !referenced[offset] {
			t.Fatalf("moof at %d is not referenced", offset)
		}
	}
	last := boxes[len(boxes)-1]
	if last.typ != "mdat" || pos != last.offset+last.size {
		t.Fatalf("references end at %d, want the end of the last mdat (%+v)", pos, last)
	}
	return info
}

func scanAll(raw []byte, maxBytes int64) (*fragmentReader, []byte, error) {
	f := newFragmentReader(bytes.NewReader(raw), maxBytes)
	out, err := io.ReadAll(f)
	return f, out, err
}

func TestFragmentReaderYieldsBodyAndIndexesEveryFragment(t *testing.T) {
	s := buildSynth(synthFrags(testPayload(30_000)), true)
	for name, read := range map[string]func(*fragmentReader) ([]byte, error){
		"whole":    func(f *fragmentReader) ([]byte, error) { return io.ReadAll(f) },
		"one byte": func(f *fragmentReader) ([]byte, error) { return io.ReadAll(iotest.OneByteReader(f)) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFragmentReader(iotest.HalfReader(bytes.NewReader(s.raw)), 0)
			out, err := read(f)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out, s.body) {
				t.Fatalf("body differs from the stream without its mfra (%d vs %d bytes)", len(out), len(s.body))
			}
			if f.n != int64(len(s.body)) || !bytes.Equal(f.sum.Sum(nil), sha256Of(s.body)) {
				t.Fatal("streamed count or hash does not match the body")
			}
			if f.insertAt != s.insertAt {
				t.Fatalf("insertAt = %d, want %d", f.insertAt, s.insertAt)
			}
			index, err := f.sidx()
			if err != nil {
				t.Fatal(err)
			}
			info := checkFinalLayout(t, finalLayout(s.body, index, f.insertAt))
			if info.referenceID != testVideoID || info.timescale != testVideoTimescale {
				t.Fatalf("index references track %d at %d, want the video track", info.referenceID, info.timescale)
			}
			if info.ept != 1024 {
				t.Fatalf("earliest presentation time = %d, want tfdt plus the first composition offset", info.ept)
			}
			var total, want uint64
			for i, ref := range info.refs {
				if uint64(ref.duration) != s.durations[i] {
					t.Fatalf("reference %d duration = %d, want %d", i, ref.duration, s.durations[i])
				}
				total += uint64(ref.duration)
				want += s.durations[i]
			}
			if total != want || len(info.refs) != len(s.moofAt) {
				t.Fatalf("index covers %d over %d references, want %d over %d", total, len(info.refs), want, len(s.moofAt))
			}
			for i, wantSAP := range []bool{true, true, false} {
				if got := info.refs[i].sap == 1<<31|1<<28; got != wantSAP {
					t.Fatalf("reference %d SAP = %#x, want starts-with-SAP %v", i, info.refs[i].sap, wantSAP)
				}
			}
		})
	}
}

func TestFragmentReaderAcceptsLargesizeMdat(t *testing.T) {
	frags := synthFrags(testPayload(3000))
	raw := append(synthFtyp(), synthMoov()...)
	raw = append(raw, synthMoof(1, frags[0])...)
	mdat := make([]byte, 16, 16+len(frags[0].payload))
	binary.BigEndian.PutUint32(mdat, 1)
	copy(mdat[4:], "mdat")
	binary.BigEndian.PutUint64(mdat[8:], uint64(16+len(frags[0].payload)))
	raw = append(raw, append(mdat, frags[0].payload...)...)
	f, out, err := scanAll(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, raw) {
		t.Fatal("largesize mdat did not pass through unchanged")
	}
	index, err := f.sidx()
	if err != nil {
		t.Fatal(err)
	}
	checkFinalLayout(t, finalLayout(out, index, f.insertAt))
}

func TestFragmentReaderRefusesUnusableOutput(t *testing.T) {
	frags := synthFrags(testPayload(3000))
	head := append(synthFtyp(), synthMoov()...)
	moof := synthMoof(1, frags[0])
	mdat := mp4Box("mdat", frags[0].payload)
	whole := buildSynth(frags, false).raw
	join := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	absoluteBase := mp4Box("moof", mp4FullBox("mfhd", 0, 0, u32b(1)), mp4Box("traf",
		mp4FullBox("tfhd", 0, 0x000001, u32b(testVideoID), u64b(0)),
		mp4FullBox("trun", 0, 0x004|0x100, u32b(1), u32b(testSyncFlags), u32b(testFrameDuration))))
	noSamples := mp4Box("moof", mp4FullBox("mfhd", 0, 0, u32b(1)), mp4Box("traf",
		mp4FullBox("tfhd", 0, 0x020000, u32b(testVideoID)),
		mp4FullBox("trun", 0, 0x004, u32b(0), u32b(testSyncFlags))))
	overrun := mp4Box("moof", mp4FullBox("mfhd", 0, 0, u32b(1)), mp4Box("traf",
		mp4FullBox("tfhd", 0, 0x020000, u32b(testVideoID)),
		mp4FullBox("trun", 0, 0x100, u32b(1<<30))))
	zeroSize := []byte{0, 0, 0, 0, 'm', 'd', 'a', 't', 1, 2, 3}
	hugeMoov := append(u32b(maxBufferedBox+1), []byte("moov")...)

	cases := map[string][]byte{
		"empty":                       nil,
		"no ftyp first":               join(synthMoov(), moof, mdat),
		"second ftyp":                 join(head, synthFtyp(), moof, mdat),
		"moof before moov":            join(synthFtyp(), moof, mdat),
		"moov after moof":             join(head, moof, mdat, synthMoov()),
		"no fragment":                 head,
		"last fragment has no mdat":   join(head, moof),
		"absolute base data offset":   join(head, absoluteBase, mdat),
		"transcoder sidx":             join(head, mp4FullBox("sidx", 1, 0, make([]byte, 28)), moof, mdat),
		"truncated mid mdat":          whole[:len(whole)-5],
		"truncated mid moof":          join(head, moof[:len(moof)-3]),
		"truncated header":            join(head, moof, mdat, []byte{0, 0, 0}),
		"zero size box":               join(head, moof, zeroSize),
		"box after mfra":              join(head, moof, mdat, synthMfra(), mdat),
		"mfra before any fragment":    join(head, synthMfra()),
		"reference with no samples":   join(head, noSamples, mdat),
		"sample table overruns":       join(head, overrun, mdat),
		"moov larger than the buffer": join(synthFtyp(), hugeMoov),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			f, _, err := scanAll(raw, 0)
			if !errors.Is(err, ErrInvalidOutput) {
				t.Fatalf("err = %v, want ErrInvalidOutput", err)
			}
			if _, err := f.sidx(); err == nil {
				t.Fatal("sidx built from a refused stream")
			}
		})
	}
}

func TestFragmentReaderEnforcesSizeLimit(t *testing.T) {
	s := buildSynth(synthFrags(testPayload(30_000)), false)
	if _, _, err := scanAll(s.raw, int64(len(s.body))); err != nil {
		t.Fatalf("stream at the limit refused: %v", err)
	}
	if _, _, err := scanAll(s.raw, int64(len(s.body))-1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestFragmentReaderNeverReportsSourceFailureAsCleanEnd(t *testing.T) {
	s := buildSynth(synthFrags(testPayload(30_000)), false)
	errBoom := errors.New("transcoder failed")
	for name, tc := range map[string]struct {
		src  io.Reader
		want error
	}{
		"transcoder error mid box":   {io.MultiReader(bytes.NewReader(s.raw[:len(s.raw)/2]), iotest.ErrReader(errBoom)), errBoom},
		"unexpected EOF from source": {io.MultiReader(bytes.NewReader(s.raw[:len(s.raw)/2]), iotest.ErrReader(io.ErrUnexpectedEOF)), ErrInvalidOutput},
		"error between boxes":        {io.MultiReader(bytes.NewReader(s.raw[:s.insertAt]), iotest.ErrReader(errBoom)), errBoom},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := io.ReadAll(newFragmentReader(tc.src, 0))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			// EncryptStream treats a bare io.EOF or io.ErrUnexpectedEOF from its
			// source as a clean final chunk, so neither may surface unwrapped.
			if err == io.EOF || err == io.ErrUnexpectedEOF { //nolint:errorlint // identity is the point
				t.Fatalf("source failure surfaced as a bare %v", err)
			}
		})
	}
}

func sha256Of(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}
