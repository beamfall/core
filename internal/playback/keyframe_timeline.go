package playback

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
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

// keyframeProbeTimeout is the least time one keyframe probe is given. The
// probe only demuxes, but it reads the whole file once, so a large file gets
// longer: see keyframeProbeTimeoutFor.
const keyframeProbeTimeout = 5 * time.Minute

// keyframeProbeMinBytesPerSecond is the slowest read a probe is allowed to
// finish at. A 17 GB remux on a parity array read at about 36 MB/s, so a fixed
// five minutes timed out on every play and the index was never kept.
const keyframeProbeMinBytesPerSecond = 8 << 20

// keyframeProbeTimeoutFor bounds the probe of a file of size bytes.
func keyframeProbeTimeoutFor(size int64) time.Duration {
	return max(keyframeProbeTimeout, time.Duration(size/keyframeProbeMinBytesPerSecond)*time.Second)
}

// keyframeFirstPlayWait bounds how long a first audio-copy play waits for the
// keyframe probe. A player gives up on its first playlist long before a large
// file's probe ends; past this wait the session starts on the uniform copy
// grid and the probe finishes in the background for the next play.
const keyframeFirstPlayWait = 5 * time.Second

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
	identity  string
}

// keyframeCache holds probed timelines and makes concurrent first plays of
// one file share a single probe. Its zero value is ready to use.
type keyframeCache struct {
	store        KeyframeStore
	done         map[keyframeGeneration]*keyframeTimeline
	inflight     map[keyframeLoadKey]*keyframeLoad
	publications map[string]*keyframePublication
	// firstPlayWait overrides keyframeFirstPlayWait when positive (tests).
	firstPlayWait time.Duration
}

type keyframeLoadKey struct {
	library    store.LibraryID
	generation keyframeGeneration
}

type keyframePublication struct {
	latest  *keyframeLoad
	order   uint64
	running int
}

type keyframeLoad struct {
	finished chan struct{}
	cancel   context.CancelFunc
	timeline *keyframeTimeline
	err      error
}

// SetKeyframeStore wires the catalog that persists probed keyframe indexes.
// Optional: without it each Sessions keeps a bounded in-memory cache only.
func (s *Sessions) SetKeyframeStore(keyframes KeyframeStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keyframes.store = keyframes
}

// copyVideoTimeline returns src's keyframe timeline for an audio-copy session,
// or nil when the session uses the uniform copy grid: the transcoder cannot
// segment at keyframes, or the probe is still running when the first-play
// wait ends. The session that already exists for key keeps the layout it was
// created with, nil included, so a timeline that arrives later never reshapes
// (and tears down) a session that is playing. Otherwise the file's current
// generation is looked up in memory, then in the store, and probed only when
// neither has it.
func (s *Sessions) boundVideoTimeline(ctx context.Context, binding *LiveBinding, source sourceCapture) (*keyframeTimeline, error) {
	prober, ok := s.transcoder.(keyframeTranscoder)
	if !ok {
		return nil, nil
	}
	s.mu.Lock()
	session := binding.session
	if binding.validLocked() && session.state == sessionRunning && source.info != nil && source.matches(session.sourceCapture()) && slices.ContainsFunc(session.rungs, func(r Rung) bool { return r.CopyVideo }) {
		timeline := session.timeline
		s.mu.Unlock()
		return timeline, nil
	}
	s.mu.Unlock()
	info := source.info
	if info == nil {
		return nil, errSetupUnavailable
	}
	gen := keyframeGeneration{path: source.path, size: info.Size(), modTimeNs: info.ModTime().UnixNano(), identity: source.generation}
	timeline, err := s.loadKeyframes(ctx, binding, gen, source.order, prober)
	if errors.Is(err, errKeyframesPending) {
		// A slow probe must not keep the title from playing: the uniform
		// grid plays, only its seeks land on the keyframe before.
		slog.Warn("keyframe probe still running; audio-copy session uses the uniform segment grid", "media", session.key.MediaID)
		return nil, nil
	}
	return timeline, err
}

// errKeyframesPending reports a probe still running when the first-play wait
// ran out. It keeps running and its result is kept for the next play.
var errKeyframesPending = errors.New("keyframe probe still running")

// load returns gen's timeline, starting a probe when none is cached or
// running. It waits at most the first-play wait for a running probe.
func (s *Sessions) loadKeyframes(ctx context.Context, binding *LiveBinding, gen keyframeGeneration, order uint64, prober keyframeTranscoder) (*keyframeTimeline, error) {
	s.mu.Lock()
	if !binding.validLocked() {
		s.mu.Unlock()
		return nil, ErrSessionEvicted
	}
	c := &s.keyframes
	pending := c.publications[gen.path]
	if timeline, ok := c.done[gen]; ok {
		// A cache hit can arrive after a newer generation already claimed a load.
		if pending != nil && order > pending.order {
			pending.latest, pending.order = nil, order
		}
		s.mu.Unlock()
		return timeline, nil
	}
	key := keyframeLoadKey{binding.library, gen}
	load := c.inflight[key]
	if load == nil {
		h := binding.session
		owner, cancel := context.WithTimeout(h.cutoffCtx, keyframeProbeTimeoutFor(gen.size))
		load = &keyframeLoad{finished: make(chan struct{}), cancel: cancel}
		if c.inflight == nil {
			c.inflight = make(map[keyframeLoadKey]*keyframeLoad)
		}
		c.inflight[key] = load
		if c.publications == nil {
			c.publications = make(map[string]*keyframePublication)
		}
		if pending == nil {
			pending = &keyframePublication{}
			c.publications[gen.path] = pending
		}
		pending.running++
		h.keyframeLoads++
		s.trackContinuationLocked(h)
		go s.probeKeyframes(binding.probeContext(owner, true), key, h, binding.stamp, load, c.store, prober)
	}
	if order > pending.order {
		pending.latest, pending.order = load, order
	}
	wait := c.firstPlayWait
	if wait <= 0 {
		wait = keyframeFirstPlayWait
	}
	s.mu.Unlock()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-load.finished:
		return load.timeline, load.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, errKeyframesPending
	}
}

// probeKeyframes owns the load until the probe is reaped and publication is settled.
func (s *Sessions) probeKeyframes(ctx context.Context, key keyframeLoadKey, h *hlsSession, stamp DrainFence, load *keyframeLoad, keyframes KeyframeStore, prober keyframeTranscoder) {
	defer load.cancel()
	gen := key.generation
	load.timeline, load.err = resolveKeyframes(ctx, gen, keyframes, prober)
	if load.err == nil && gen.identity != "" && captureSource(gen.path, nil, nil).generation != gen.identity {
		load.err = errSetupUnavailable
	}
	if load.err != nil && !errors.Is(load.err, context.Canceled) {
		slog.Warn("keyframe probe failed", "err", load.err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	c := &s.keyframes
	pending := c.publications[gen.path]
	if !s.admittedLocked(key.library, stamp) || h.state >= sessionRevoking {
		load.err = ErrSessionEvicted
	} else if load.err == nil {
		load.err = ctx.Err()
	}
	if c.inflight[key] == load && pending.latest == load && load.err == nil {
		if c.done == nil {
			c.done = make(map[keyframeGeneration]*keyframeTimeline)
		}
		if c.done[gen] == nil && len(c.done) == keyframeCacheEntries {
			for old := range c.done {
				delete(c.done, old)
				break
			}
		}
		c.done[gen] = load.timeline
	}
	delete(c.inflight, key)
	pending.running--
	if pending.running == 0 {
		delete(c.publications, gen.path)
	}
	h.keyframeLoads--
	s.trackContinuationLocked(h)
	close(load.finished)
	s.wakeCloseLocked()
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
	if err := ctx.Err(); err != nil {
		return nil, err
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
