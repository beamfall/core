package playback

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

// keyframeTranscoder is the optional Transcoder extension that segments copied
// video at the source's own keyframes (CORE-0021). *ffmpeg.Runner satisfies
// it; a Transcoder without it keeps the uniform audio-copy segment grid.
type keyframeTranscoder interface {
	ProbeKeyframes(ctx context.Context, src string) (ffmpeg.KeyframeIndex, error)
	StartHLSKeyframeSegments(ctx context.Context, src, dir string, startSegment int, start time.Duration, rendition ffmpeg.Rendition) (*ffmpeg.HLSProcess, error)
}

// KeyframeStore persists probed keyframe indexes per file generation so a
// source is probed once, not once per session or per server start. *store.Store
// satisfies it.
type KeyframeStore interface {
	PlaybackKeyframes(ctx context.Context, path string, sizeBytes, modTimeNs int64) (store.PlaybackKeyframes, bool, error)
	PutPlaybackKeyframes(ctx context.Context, index store.PlaybackKeyframes) error
}

// keyframeProbeTimeout bounds one keyframe probe. The probe only demuxes, but
// it reads the whole file once.
const keyframeProbeTimeout = 5 * time.Minute

// keyframeCacheEntries bounds the in-memory timelines kept per Sessions.
const keyframeCacheEntries = 64

// keyframeTimeline is the audio-copy rung's segment layout. Copied video can
// only start at a keyframe, so the encoder writes one file per GOP: encoder
// segment i spans starts[i] to starts[i+1] (the last one to end), and a restart
// at segment i begins exactly at that keyframe. The playlist merges
// consecutive GOPs up to the target segment length (CORE-1843): playlist
// segment j is GOPs groups[j] up to groups[j+1], so a short-GOP source does not
// list one segment per GOP.
type keyframeTimeline struct {
	starts []time.Duration
	groups []int
	end    time.Duration
}

// newKeyframeTimeline builds the layout from a keyframe index. The first
// segment starts at 0 and runs to the second keyframe: the encoder opens its
// first segment with the first keyframe whatever its time (video that starts
// after the audio does not get a segment of its own). Boundaries are kept at
// least a millisecond apart, the playlist's resolution, and keyframes at or
// past the end are dropped.
func newKeyframeTimeline(index ffmpeg.KeyframeIndex) *keyframeTimeline {
	end := index.Duration
	if end < time.Millisecond || len(index.Keyframes) == 0 {
		return nil
	}
	starts := []time.Duration{0}
	for _, k := range index.Keyframes[1:] {
		if k.Round(time.Millisecond) <= starts[len(starts)-1].Round(time.Millisecond) {
			continue
		}
		if k.Round(time.Millisecond) >= end.Round(time.Millisecond) {
			break
		}
		starts = append(starts, k)
	}
	target := time.Duration(audioCopySegmentSeconds) * time.Second
	return &keyframeTimeline{starts: starts, groups: mergeGOPs(starts, end, target), end: end}
}

// mergeGOPs returns the first GOP of each playlist segment. A segment takes
// the next GOP while it still ends within target of the segment's start, so
// segments run up to target long and a GOP longer than target stands alone.
// Lengths use the playlist's millisecond-rounded boundaries.
func mergeGOPs(starts []time.Duration, end, target time.Duration) []int {
	ms := func(d time.Duration) int64 { return d.Round(time.Millisecond).Milliseconds() }
	groups := []int{0}
	for gop := 1; gop < len(starts); gop++ {
		gopEnd := end
		if gop+1 < len(starts) {
			gopEnd = starts[gop+1]
		}
		if ms(gopEnd)-ms(starts[groups[len(groups)-1]]) > target.Milliseconds() {
			groups = append(groups, gop)
		}
	}
	return groups
}

// segmentCount is how many encoder segments (GOPs) the source has.
func (t *keyframeTimeline) segmentCount() int { return len(t.starts) }

// playlistSegmentCount is how many segments the playlist lists.
func (t *keyframeTimeline) playlistSegmentCount() int { return len(t.groups) }

// encoderSegments is the range of encoder segments [first, end) that playlist
// segment carries.
func (t *keyframeTimeline) encoderSegments(segment int) (first, end int) {
	first = t.groups[segment]
	end = len(t.starts)
	if segment+1 < len(t.groups) {
		end = t.groups[segment+1]
	}
	return first, end
}

// segmentStart is where segment begins on the source timeline; a segment past
// the last starts at the end.
func (t *keyframeTimeline) segmentStart(segment int) time.Duration {
	if segment < 0 {
		return 0
	}
	if segment >= len(t.starts) {
		return t.end
	}
	return t.starts[segment]
}

// segmentAt is the encoder segment playback from at begins with: the first
// GOP of the playlist segment containing at, which is the first file a player
// starting there asks for.
func (t *keyframeTimeline) segmentAt(at time.Duration) int {
	segment := 0
	for _, first := range t.groups {
		if t.starts[first] > at {
			break
		}
		segment = first
	}
	return segment
}

// playlist renders the VOD playlist. EXTINF values are differences of
// millisecond-rounded boundaries, so their running sum is each segment's start
// to the millisecond and the total is the probed duration.
func (t *keyframeTimeline) playlist() string {
	bounds := make([]int64, 0, len(t.groups)+1)
	for _, first := range t.groups {
		bounds = append(bounds, t.starts[first].Round(time.Millisecond).Milliseconds())
	}
	bounds = append(bounds, t.end.Round(time.Millisecond).Milliseconds())
	target := int64(1)
	for i := 1; i < len(bounds); i++ {
		target = max(target, (bounds[i]-bounds[i-1]+500)/1000)
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:7\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", target)
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	fmt.Fprintf(&b, "#EXT-X-MAP:URI=%q\n", initSegmentName)
	for i := 1; i < len(bounds); i++ {
		ms := bounds[i] - bounds[i-1]
		fmt.Fprintf(&b, "#EXTINF:%d.%03d,\n", ms/1000, ms%1000)
		fmt.Fprintf(&b, "seg%05d.m4s\n", i-1)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

// keyframeGeneration names one version of a source file: a rewritten or
// replaced file has a new size or modification time and is probed again.
type keyframeGeneration struct {
	path      string
	size      int64
	modTimeNs int64
}

// keyframeCache holds probed timelines and makes concurrent first plays of
// one file share a single probe. Its zero value is ready to use.
type keyframeCache struct {
	mu       sync.Mutex
	store    KeyframeStore
	done     map[keyframeGeneration]*keyframeTimeline
	inflight map[keyframeGeneration]*keyframeLoad
	// closed refuses new probes once close has begun. stopProbes cancels the
	// probes still running (stopCtx is created with the first probe), and
	// probes counts them so close can wait for each to return.
	closed     bool
	stopCtx    context.Context
	stopProbes context.CancelFunc
	probes     sync.WaitGroup
}

type keyframeLoad struct {
	finished chan struct{}
	timeline *keyframeTimeline
	err      error
}

// SetKeyframeStore wires the catalog that persists probed keyframe indexes.
// Optional: without it each Sessions keeps a bounded in-memory cache only.
func (s *Sessions) SetKeyframeStore(keyframes KeyframeStore) {
	s.keyframes.mu.Lock()
	defer s.keyframes.mu.Unlock()
	s.keyframes.store = keyframes
}

// copyVideoTimeline returns src's keyframe timeline for an audio-copy session,
// or nil when the transcoder cannot segment at keyframes. The session that
// already exists for key reuses its own timeline; otherwise the file's current
// generation is looked up in memory, then in the store, and probed only when
// neither has it.
func (s *Sessions) copyVideoTimeline(ctx context.Context, key SessionKey, src string) (*keyframeTimeline, error) {
	prober, ok := s.transcoder.(keyframeTranscoder)
	if !ok {
		return nil, nil
	}
	s.mu.Lock()
	if existing, ok := s.sessions[key]; ok && existing.src == src && existing.timeline != nil {
		timeline := existing.timeline
		s.mu.Unlock()
		return timeline, nil
	}
	s.mu.Unlock()
	info, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	gen := keyframeGeneration{path: src, size: info.Size(), modTimeNs: info.ModTime().UnixNano()}
	return s.keyframes.load(ctx, gen, prober)
}

func (c *keyframeCache) load(ctx context.Context, gen keyframeGeneration, prober keyframeTranscoder) (*keyframeTimeline, error) {
	c.mu.Lock()
	if timeline, ok := c.done[gen]; ok {
		c.mu.Unlock()
		return timeline, nil
	}
	if load, ok := c.inflight[gen]; ok {
		c.mu.Unlock()
		select {
		case <-load.finished:
			return load.timeline, load.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if c.closed {
		c.mu.Unlock()
		return nil, ErrSessionEvicted
	}
	load := &keyframeLoad{finished: make(chan struct{})}
	if c.inflight == nil {
		c.inflight = map[keyframeGeneration]*keyframeLoad{}
	}
	c.inflight[gen] = load
	keyframes := c.store
	if c.stopCtx == nil {
		c.stopCtx, c.stopProbes = context.WithCancel(context.Background())
	}
	stopCtx := c.stopCtx
	c.probes.Add(1)
	c.mu.Unlock()
	defer c.probes.Done()

	// The probe outlives the request that started it: other first plays may be
	// waiting on it, and its result is kept. Only close cancels it early.
	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), keyframeProbeTimeout)
	stopProbe := context.AfterFunc(stopCtx, cancel)
	load.timeline, load.err = resolveKeyframes(probeCtx, gen, keyframes, prober)
	stopProbe()
	cancel()

	c.mu.Lock()
	delete(c.inflight, gen)
	if load.err == nil {
		if c.done == nil {
			c.done = map[keyframeGeneration]*keyframeTimeline{}
		}
		for old := range c.done {
			if old.path == gen.path {
				delete(c.done, old) // an earlier generation of the same file
			}
		}
		for old := range c.done {
			if len(c.done) < keyframeCacheEntries {
				break
			}
			delete(c.done, old)
		}
		c.done[gen] = load.timeline
	}
	c.mu.Unlock()
	close(load.finished)
	return load.timeline, load.err
}

// close refuses new probes, cancels the running ones and waits for them to
// return, so no probe outlives the Sessions that started it.
func (c *keyframeCache) close() {
	c.mu.Lock()
	c.closed = true
	if c.stopProbes != nil {
		c.stopProbes()
	}
	c.mu.Unlock()
	c.probes.Wait()
}

// resolveKeyframes reads gen's index from the store, or probes the file and
// records the result. A store failure only costs a probe; a probe failure
// fails the request.
func resolveKeyframes(ctx context.Context, gen keyframeGeneration, keyframes KeyframeStore, prober keyframeTranscoder) (*keyframeTimeline, error) {
	if keyframes != nil {
		if cached, ok, err := keyframes.PlaybackKeyframes(ctx, gen.path, gen.size, gen.modTimeNs); err == nil && ok {
			if timeline := newKeyframeTimeline(ffmpeg.KeyframeIndex{Keyframes: cached.Keyframes, Duration: cached.Duration}); timeline != nil {
				return timeline, nil
			}
		}
	}
	index, err := prober.ProbeKeyframes(ctx, gen.path)
	if err != nil {
		return nil, err
	}
	timeline := newKeyframeTimeline(index)
	if timeline == nil {
		return nil, fmt.Errorf("probe keyframes: duration %v is too short", index.Duration)
	}
	if keyframes != nil {
		if err := keyframes.PutPlaybackKeyframes(ctx, store.PlaybackKeyframes{
			Path:      gen.path,
			SizeBytes: gen.size,
			ModTimeNs: gen.modTimeNs,
			Duration:  index.Duration,
			Keyframes: index.Keyframes,
		}); err != nil {
			slog.Warn("could not record probed keyframes; the file will be probed again", "err", err)
		}
	}
	return timeline, nil
}
