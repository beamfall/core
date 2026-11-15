package encderiv

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"testing/iotest"
	"time"
)

// secondFrags builds n one-second fragments (24 frames at 24 fps); the ones
// listed in nonSync start on a non-sync sample.
func secondFrags(n int, nonSync ...int) []synthFragment {
	frags := make([]synthFragment, n)
	for i := range frags {
		frags[i] = synthFragment{
			decodeTime: uint64(i * 24 * testFrameDuration), //nolint:gosec // small test counts
			samples:    24,
			firstFlags: testSyncFlags,
			payload:    testPayload(100 + i),
		}
	}
	for _, i := range nonSync {
		frags[i].firstFlags = testNonSyncFlags
	}
	return frags
}

// fragmentBytes is the moof and mdat of fragment i in a buildSynth stream.
func fragmentBytes(s synthStream, i int) []byte {
	end := int64(len(s.body))
	if i+1 < len(s.moofAt) {
		end = s.moofAt[i+1]
	}
	return s.body[s.moofAt[i]:end]
}

type collected struct {
	init     []byte
	segments [][]byte
}

func (c *collected) sink() SegmentSink {
	return SegmentSink{
		Init:    func(b []byte) error { c.init = bytes.Clone(b); return nil },
		Segment: func(_ Segment, b []byte) error { c.segments = append(c.segments, bytes.Clone(b)); return nil },
	}
}

func finalOf(t *testing.T, s synthStream) []byte {
	t.Helper()
	f, body, err := scanAll(s.raw, 0)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	index, err := f.sidx()
	if err != nil {
		t.Fatalf("sidx: %v", err)
	}
	return finalLayout(body, index, f.insertAt)
}

func TestSegmentFMP4CutsOnTargetAndKeepsFragmentsWhole(t *testing.T) {
	s := buildSynth(secondFrags(10), true)
	inputs := map[string][]byte{"final layout": finalOf(t, s), "raw with mfra": s.raw}
	for name, raw := range inputs {
		t.Run(name, func(t *testing.T) {
			var got collected
			segs, err := SegmentFMP4(context.Background(), iotest.OneByteReader(bytes.NewReader(raw)), SegmentOptions{Target: 4 * time.Second}, got.sink())
			if err != nil {
				t.Fatalf("SegmentFMP4: %v", err)
			}
			if want := append(synthFtyp(), synthMoov()...); !bytes.Equal(got.init, want) {
				t.Fatal("init section is not ftyp+moov")
			}
			wantDur := []time.Duration{4 * time.Second, 4 * time.Second, 2 * time.Second}
			groups := [][]int{{0, 1, 2, 3}, {4, 5, 6, 7}, {8, 9}}
			if len(segs) != len(wantDur) || len(got.segments) != len(wantDur) {
				t.Fatalf("segments = %d/%d, want %d", len(segs), len(got.segments), len(wantDur))
			}
			var start time.Duration
			for i, seg := range segs {
				var want []byte
				for _, frag := range groups[i] {
					want = append(want, fragmentBytes(s, frag)...)
				}
				if !bytes.Equal(got.segments[i], want) {
					t.Fatalf("segment %d bytes differ from fragments %v", i, groups[i])
				}
				if seg.Index != i || seg.Start != start || seg.Duration != wantDur[i] || seg.Size != int64(len(want)) {
					t.Fatalf("segment %d = %+v, want start %v duration %v size %d", i, seg, start, wantDur[i], len(want))
				}
				start += seg.Duration
			}
		})
	}
}

func TestSegmentFMP4CutsOnlyAtSyncSamples(t *testing.T) {
	// The fragment at 4s is not a sync sample, so the cut waits for 5s; the
	// next boundary is then 8s.
	s := buildSynth(secondFrags(10, 4), false)
	var got collected
	segs, err := SegmentFMP4(context.Background(), bytes.NewReader(s.raw), SegmentOptions{Target: 4 * time.Second}, got.sink())
	if err != nil {
		t.Fatalf("SegmentFMP4: %v", err)
	}
	want := []time.Duration{5 * time.Second, 3 * time.Second, 2 * time.Second}
	if len(segs) != len(want) {
		t.Fatalf("segments = %+v", segs)
	}
	for i := range want {
		if segs[i].Duration != want[i] {
			t.Fatalf("segment %d duration = %v, want %v", i, segs[i].Duration, want[i])
		}
	}
}

func TestSegmentFMP4BoundsTheBufferedSegment(t *testing.T) {
	s := buildSynth(secondFrags(8), false)
	var got collected
	_, err := SegmentFMP4(context.Background(), bytes.NewReader(s.raw), SegmentOptions{Target: 4 * time.Second, MaxSegmentBytes: 1024}, got.sink())
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	limit := int64(len(fragmentBytes(s, 0)) * 4)
	if _, err := SegmentFMP4(context.Background(), bytes.NewReader(s.raw), SegmentOptions{Target: 4 * time.Second, MaxSegmentBytes: limit + 64}, (&collected{}).sink()); err != nil {
		t.Fatalf("segment within the bound refused: %v", err)
	}
	huge := append(synthFtyp(), mp4Box("moov", make([]byte, maxBufferedBox))...)
	if _, err := SegmentFMP4(context.Background(), bytes.NewReader(huge), SegmentOptions{Target: time.Second}, (&collected{}).sink()); !errors.Is(err, ErrInvalidOutput) {
		t.Fatalf("oversized moov err = %v", err)
	}
}

func TestSegmentFMP4RefusesMalformedStreams(t *testing.T) {
	frags := secondFrags(3)
	s := buildSynth(frags, false)
	head := append(synthFtyp(), synthMoov()...)
	moof := func(i int) []byte { return synthMoof(uint32(i+1), frags[i]) } //nolint:gosec // small test counts
	mdat := func(i int) []byte { return mp4Box("mdat", frags[i].payload) }
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	backwards := frags[0]
	backwards.decodeTime = 0
	cases := map[string][]byte{
		"empty":               nil,
		"no ftyp first":       cat(synthMoov(), moof(0), mdat(0)),
		"no moov":             synthFtyp(),
		"no fragments":        head,
		"moof before moov":    cat(synthFtyp(), moof(0), mdat(0)),
		"moof without mdat":   cat(head, moof(0), moof(1), mdat(1)),
		"trailing moof":       cat(head, moof(0), mdat(0), moof(1)),
		"mdat without moof":   cat(head, mdat(0)),
		"unknown box":         cat(head, mp4Box("uuid", []byte("x")), moof(0), mdat(0)),
		"box after mfra":      cat(s.raw, synthMfra(), moof(1), mdat(1)),
		"mfra first":          cat(head, synthMfra()),
		"sidx after fragment": cat(head, moof(0), mdat(0), mp4Box("sidx", make([]byte, 32)), moof(1), mdat(1)),
		"second moov":         cat(head, synthMoov(), moof(0), mdat(0)),
		"decode backwards":    cat(head, moof(0), mdat(0), moof(1), mdat(1), synthMoof(3, backwards), mdat(0)),
		"truncated":           s.raw[:len(s.raw)-5],
		"zero size box":       cat(head, []byte{0, 0, 0, 0, 'm', 'o', 'o', 'f'}),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := SegmentFMP4(context.Background(), bytes.NewReader(raw), SegmentOptions{Target: time.Second}, (&collected{}).sink())
			if !errors.Is(err, ErrInvalidOutput) {
				t.Fatalf("err = %v, want ErrInvalidOutput", err)
			}
		})
	}
}

func TestSegmentFMP4StopsOnSinkErrorAndCancellation(t *testing.T) {
	s := buildSynth(secondFrags(8), false)
	boom := errors.New("sink failed")
	calls := 0
	sink := SegmentSink{
		Init:    func([]byte) error { return nil },
		Segment: func(Segment, []byte) error { calls++; return boom },
	}
	if _, err := SegmentFMP4(context.Background(), bytes.NewReader(s.raw), SegmentOptions{Target: time.Second}, sink); !errors.Is(err, boom) || calls != 1 {
		t.Fatalf("err = %v after %d calls, want the sink error after one", err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SegmentFMP4(ctx, bytes.NewReader(s.raw), SegmentOptions{Target: time.Second}, (&collected{}).sink()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled err = %v", err)
	}
	if _, err := SegmentFMP4(context.Background(), bytes.NewReader(s.raw), SegmentOptions{}, (&collected{}).sink()); err == nil {
		t.Fatal("zero target accepted")
	}
}
