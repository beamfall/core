package playback

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beamfall/core/internal/encderiv"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/governor"
	"github.com/beamfall/core/internal/store"
	"github.com/fsnotify/fsnotify"
)

// Transcoder is playback's slice of the FFmpeg seam (interface at the
// consumer), satisfied by *ffmpeg.Runner. Both starters encode exactly one
// rendition — ADR-0010 §8: a live FFmpeg session produces one rendition.
type Transcoder interface {
	// StartHLS is the legacy linear mode for media with unknown duration:
	// ffmpeg owns the event playlist and segments grow from zero.
	StartHLS(ctx context.Context, src, dir string, rendition ffmpeg.Rendition) (*ffmpeg.HLSProcess, error)
	// StartHLSSegments produces numbered segments from an offset, backing the
	// synthesized VOD playlist (seek-ahead and rendition-switch restarts land
	// here).
	StartHLSSegments(ctx context.Context, src, dir string, startSegment, segmentSeconds int, rendition ffmpeg.Rendition) (*ffmpeg.HLSProcess, error)
}

type transcodeCodecSupporter interface {
	SupportsTranscodeCodec(codec string) bool
}

// encodePolicySource is a transcoder that applies the Console encode policy
// (CORE-2452). Each session snapshots it once at creation and decides both its
// ladder and its encoder from that snapshot, so a later Console save changes
// only sessions started after it.
type encodePolicySource interface {
	EncodePolicy() ffmpeg.EncodePolicy
	SupportsTranscodeCodecUnder(policy ffmpeg.EncodePolicy, codec string) bool
}

// SessionKey identifies one transcode session: the same auth session asking
// for the same Media under the same declared capability reuses the running
// transcode instead of forking another. ProfileCapability is the third axis of
// ADR-0010 §6 identity; a cast/AirPlay receiver profile therefore addresses a
// new session.
type SessionKey struct {
	SessionID         store.SessionID
	MediaID           store.MediaID
	ProfileCapability string
}

const (
	segmentSeconds = 4
	// TranscodeStartLatencyBudget is the server-side SLO from a first HLS
	// segment request until that segment is playable. It is deliberately
	// distinct from ADR-0008's direct-play-only <2s first-frame target.
	TranscodeStartLatencyBudget = 8 * time.Second
	// PostSeekTranscodeLatencyBudget bounds a missing segment request after a
	// session has encoded at least once, including far-seek and rendition-switch
	// restarts. Keeping this separate makes live-encode seek cost measurable.
	PostSeekTranscodeLatencyBudget = 6 * time.Second
	// Copy-mode HLS can only cut at source keyframes. The common long-GOP case
	// this path exists for is 10s, so advertise and seek on that clock instead
	// of pretending copied video can honor the re-encode path's 4s cadence.
	audioCopySegmentSeconds = 10
	// seekLookaheadSegments is how far past the produced frontier a request
	// may wait for the running transcode instead of forcing a restart.
	seekLookaheadSegments = 3
	sessionIdleTimeout    = 5 * time.Minute
	fileWaitTimeout       = 15 * time.Second
	// safetyPollInterval is the coarse re-check cadence in the event-driven
	// waitForFile: long enough that it is not a busy-poll (the old loop spun at
	// 100ms), short enough that a missed/coalesced fsnotify event still resolves
	// within a segment's worth of time so playback never stalls on a dropped
	// notification.
	safetyPollInterval = 1 * time.Second
	// fileEventSettleDelay closes the create-before-write race: some backends
	// report Create before the first bytes are visible and then coalesce the
	// following Write. One delayed, event-triggered stat is enough to observe
	// the settled file without returning to periodic busy-polling.
	fileEventSettleDelay = 25 * time.Millisecond
	// defaultTranscodeCap is the per-server concurrent-transcode cap
	// (ADR-0010 §6 admission control), overridable via SetTranscodeCap.
	defaultTranscodeCap = 4
	// preemptIdleAfter is how long a session must sit without a fetch before
	// admission control may preempt it for a new session. Keepalive is the
	// segment/playlist fetch stream, so an actively-streaming
	// client is never preempted — it never fails mid-stream — while a reaped
	// paused client resumes transparently at position.
	preemptIdleAfter = 30 * time.Second
	// maxRequestEncoderRestarts bounds how many times one waiting request
	// restarts an encoder that died under it before failing fast.
	maxRequestEncoderRestarts = 2
	// encoderRestartBudget restarts per encoderRestartWindow bound crash
	// recovery across all of a session's requests, so a source that kills
	// FFmpeg every time fails fast instead of respawning on every fetch.
	encoderRestartBudget = 3
	encoderRestartWindow = time.Minute
	// encoderStallTimeout is how long a live encoder's output position may stay
	// still while a request waits on it before the encoder counts as hung and
	// is killed. It exceeds both latency budgets so a slow but working start
	// is never mistaken for a hang.
	encoderStallTimeout = 15 * time.Second
	// encoderMaxFailedRuns is how many encoder runs in a row may crash or
	// stall without writing a segment before the session is marked failed.
	// The sliding restart budget refills every encoderRestartWindow, so on
	// its own it lets a source that always hangs the encoder (a 15 s stall,
	// a restart, another stall) cycle forever while the player retries 503s
	// (CORE-2379). Once failed, the session answers ErrTranscodeFailed.
	encoderMaxFailedRuns = 4
)

// errEncoderExited is waitForArtifact's signal that the encoder producing the
// awaited artifact has exited without producing it.
var errEncoderExited = errors.New("live transcode exited")

// errEncoderFailing is returned once crash recovery is exhausted: the
// encoder keeps dying and the restart budget is spent.
var errEncoderFailing = errors.New("live transcode keeps failing")

// errEncoderEnded is returned when the encoder reached the end of its input
// without producing the awaited artifact and the source is not known to be
// short — an encoder fault rather than a missing segment.
var errEncoderEnded = errors.New("live transcode ended without producing the artifact")

// ErrTranscodeFailed is returned once a session's encoder has crashed or
// stalled encoderMaxFailedRuns times in a row without writing a segment.
// Unlike errEncoderFailing it is terminal for the session: retrying the same
// session cannot succeed, so callers answer a non-retryable status.
var ErrTranscodeFailed = errors.New("live transcode failed")

// encoderExit classifies how a live FFmpeg process ended.
type encoderExit int

const (
	// exitStopped: the server stopped it (rendition switch, far seek,
	// eviction, shutdown). Expected; never restarted on its behalf.
	exitStopped encoderExit = iota
	// exitEndOfInput: FFmpeg exited cleanly after reading its whole input.
	exitEndOfInput
	// exitCrashed: FFmpeg died on its own with a failure status or signal.
	exitCrashed
	// exitStalled: the stall watchdog killed FFmpeg because its output
	// position stopped advancing while a request waited. Recovered like a crash.
	exitStalled
)

// encoderRun is one FFmpeg process generation. exited is closed by the
// process reaper after it has recorded outcome and released the session's
// hold on the process, so a request waiting on an artifact notices the exit
// at once instead of waiting out its latency budget. exited is nil for a
// process that is not running (test transcoders), which never signals.
type encoderRun struct {
	session *hlsSession
	proc    *ffmpeg.HLSProcess
	exited  chan struct{}
	outcome encoderExit // written under Sessions.mu before exited closes
	// waiting counts the requests currently waiting on this run's artifacts;
	// the stall watchdog only acts while it is non-zero.
	waiting atomic.Int32
	// stalled is set by the stall watchdog just before it kills the process.
	stalled atomic.Bool
	// live is the run's loopback endpoint on an encrypted session, revoked
	// when the process exits or is stopped (ADR-0236 D3); nil otherwise.
	live *encderiv.LiveRun
}

func (r *encoderRun) exitedChan() <-chan struct{} {
	if r == nil {
		return nil
	}
	return r.exited
}

// ErrTranscodeCapacity is returned when admission control cannot start
// another transcode: every slot is held by an actively-streaming session.
var ErrTranscodeCapacity = errors.New("transcode capacity reached")

// ErrSessionEvicted is returned when the session a request is serving was torn
// down (device revocation, idle reap, preemption, mode-shaping replacement)
// before the request could act on it. The request fails rather than resurrecting
// a session the server has already decided to stop — ADR-0012 §7's revoked
// device must not keep streaming.
var ErrSessionEvicted = errors.New("playback session evicted")

// Priority ranks a transcode session for admission (ADR-0010 §6: "higher-
// priority preempts lower-priority"). Higher value wins. The default zero
// value is PriorityNormal, so a caller that supplies no priority — including
// the existing CanAdmit/FilePath entry points, which delegate at the default —
// behaves exactly as before: every session is equal-priority and only the idle
// rule preempts.
//
// The rule the two ranks express: a strictly-higher-priority newcomer may
// preempt a lower-priority session *even while it is actively streaming* (the
// lower-priority stream is the one that drops to a lower quality or queues —
// ADR-0010 §6's "drop or queue, never fail the privileged stream"); an equal-
// or-lower-priority newcomer falls back to the idle-only rule and never
// interrupts an active peer of equal-or-higher standing.
type Priority int

const (
	// PriorityNormal is the default rank for an ordinary viewer stream.
	PriorityNormal Priority = 0
	// PriorityForeground ranks a stream the server must protect over an
	// ordinary one — the foreground/interactive playhead the operator chose to
	// privilege. It outranks PriorityNormal, so a foreground start may preempt a
	// normal stream that has no free slot to give it, while a normal start can
	// never preempt a foreground one.
	PriorityForeground Priority = 10
)

type hlsSession struct {
	key               SessionKey
	state             sessionState
	teardown          *sessionTeardown
	dir               string
	scratch           *scratchPath
	joins             map[string]chan struct{}
	src               string
	sourceInfo        os.FileInfo
	sourceGeneration  string
	durationSeconds   int
	rungs             []Rung
	audioProfile      ffmpeg.AudioProfile
	audioStreamIndex  *int
	sourceVideo       VideoSignal
	burnSubtitleIndex int
	// encodePolicy is the Console encode policy this session started under
	// (CORE-2452); nil when the transcoder applies none.
	encodePolicy *ffmpeg.EncodePolicy
	// audioSource marks a source without a picture (no frame size): an encode
	// from its start keeps the source's priming (ffmpeg.Rendition.KeepPriming),
	// so the output carries exactly the encoder delay playbackInfo reports.
	audioSource bool
	// primed, when set, is the exact uniform-grid layout of a primed AAC
	// encode of an audio-only source (CORE-1863): the encoder's priming and
	// end padding run past the source's whole seconds, so its last segment
	// can lie beyond the grid the integer duration implies.
	primed primedGrid
	// composed, when set, is the composed picture the session's single rung
	// draws over the source's audio (see StreamShape.ComposedPicture), and
	// picture is its encoder input as laid out in the session directory.
	composed *ComposedPicture
	picture  *ffmpeg.ComposedPicture
	// adjust is the pitch shift and tempo change every encode applies (see
	// StreamShape.Adjust). durationSeconds, segments and the source end are
	// then on the adjusted output timeline.
	adjust ffmpeg.AudioAdjust
	// timeline lays out the audio-copy rung's segments at the source's own
	// keyframes (CORE-0021); nil keeps the uniform grid.
	timeline *keyframeTimeline
	// currentRung is the rendition the live FFmpeg process encodes; "" until
	// the first segment request starts one (one rendition per live session,
	// ADR-0010 §8 — switching rungs replaces the process).
	currentRung string
	// deliveredRung is the rendition whose media the viewer was last served,
	// from a running encoder or from segments already on disk. Unlike
	// currentRung it outlives the encoder: a finished encode is still what the
	// viewer is being served until the session ends.
	deliveredRung string
	proc          *ffmpeg.HLSProcess
	// start is held while ensureRung stops a replaced process and spawns
	// the next one with mu released. The session keeps its transcode slot and
	// lease meanwhile.
	start       *encoderStart
	baseSegment int
	// startMu serializes encoder starts and replacements for this session, so
	// ensureRung can release mu around Stop and spawn without a second request
	// on the same session starting another encoder meanwhile. Lock order:
	// startMu before mu, never the reverse.
	startMu sync.Mutex
	// frontier caches the highest produced segment for the current
	// (proc, baseSegment) generation so the common seek-lookahead check needs no
	// syscall under the lock. It only ever advances forward within a generation
	// (ffmpeg writes segments monotonically from baseSegment) and is reset to
	// baseSegment-1 whenever a new process starts. A cache miss (a request beyond
	// the cached frontier) refreshes it off-lock via refreshFrontier, never under
	// s.mu, so a directory ReadDir never blocks the session lock.
	frontier   int
	lastAccess time.Time
	// priority is this session's admission rank. It is set the
	// first time the session is admitted and raised, never lowered, if a later
	// request asks for a higher rank — a session that has earned foreground
	// standing keeps it for the rest of its life so a normal sibling cannot
	// later evict an actively-streaming privileged stream.
	priority Priority
	// degraded marks a capacity-degraded Watch-Party session (ADR-0010 §6 "drop
	// to this quality"; PANEL-16): its rung set is capped to the single lowest
	// rung and its admission is allowed to exceed the concurrent-transcode cap
	// when no slot can be freed, so the member keeps a (lowest-quality) stream
	// rather than being refused and fractured out of the party.
	degraded bool
	// started records that this session has successfully started an encoder at
	// least once. It remains true after process exit/reap so later misses use the
	// post-seek budget rather than being misclassified as first start.
	started bool
	// transcodeLease is the governor realtime-transcode lease this session holds
	// while it has a live FFmpeg process. It is acquired before the
	// process launches (so generation is preempted the moment the session commits
	// to spawning), carried across in-place process replacements, and released
	// exactly once on launch-failure rollback, process reap, or teardown. nil
	// means the session holds no live process (or the governor is unwired).
	transcodeLease *governor.TranscodeLease
	// run is the generation of the current or most recent process (proc).
	run *encoderRun
	// lastCrashed records that the most recent process exit was a crash, so
	// the next start is a crash restart and draws on crashRestarts.
	lastCrashed bool
	// crashRestarts holds the start times of recent crash restarts, bounding
	// them to encoderRestartBudget per encoderRestartWindow.
	crashRestarts []time.Time
	// failedRuns counts encoder runs in a row that crashed or stalled
	// without writing a segment; transcodeFailed latches once it reaches
	// encoderMaxFailedRuns.
	failedRuns      int
	transcodeFailed bool
	// sourceEnd is an upper bound on where the source's media ends, known
	// once an encoder hit end of input early or produced a segment with no
	// media (a truncated file whose probed duration overstates it). It only
	// ever lowers. Segments starting at or after it are refused as
	// ErrSourceEnded instead of being encoded as empty segments.
	sourceEnd      time.Duration
	sourceEndKnown bool
	// resumeSeconds is where playback info resumed the viewer, as a source
	// position. The session's first encoder starts at the segment containing
	// it, so a resume spawns FFmpeg once instead of starting at 0 for init.mp4
	// and restarting at the first media request. Cleared once an encoder
	// starts.
	resumeSeconds int
	// secure is the source of a session on an Encrypted Library and live its
	// encrypted file store (ADR-0236); both nil otherwise. revokeDone ends
	// the session's revocation watcher; detachment closes it.
	secure     *EncryptedSource
	live       *encderiv.LiveSession
	revokeDone chan struct{}
}

// beyondSourceEndLocked reports whether a segment starts at or past the known
// source end.
func (h *hlsSession) beyondSourceEndLocked(rung Rung, segment int) bool {
	return h.sourceEndKnown && h.segmentStart(rung, segment) >= h.sourceEnd
}

// keyframeTimeline returns the session's keyframe layout for rung, or nil
// when rung uses the uniform segment grid.
func (h *hlsSession) keyframeTimeline(rung Rung) *keyframeTimeline {
	if rung.CopyVideo {
		return h.timeline
	}
	return nil
}

// segmentCount is how many segments rung's playlist lists.
func (h *hlsSession) segmentCount(rung Rung) int {
	if t := h.keyframeTimeline(rung); t != nil {
		return t.segmentCount()
	}
	if h.primed.segments > 0 {
		return h.primed.segments
	}
	return segmentCount(h.durationSeconds, rungSegmentSeconds(rung))
}

// playlistSegmentCount is how many segments rung's playlist lists: one per
// encoder segment on the uniform grid, one per merged run of GOPs on a
// keyframe timeline.
func (h *hlsSession) playlistSegmentCount(rung Rung) int {
	if t := h.keyframeTimeline(rung); t != nil {
		return t.playlistSegmentCount()
	}
	return h.segmentCount(rung)
}

// encoderSegments is the range [first, end) of encoder segments that rung's
// playlist segment is made of.
func (h *hlsSession) encoderSegments(rung Rung, segment int) (first, end int) {
	if t := h.keyframeTimeline(rung); t != nil {
		return t.encoderSegments(segment)
	}
	return segment, segment + 1
}

// segmentStart is where rung's segment begins on the source timeline.
func (h *hlsSession) segmentStart(rung Rung, segment int) time.Duration {
	if t := h.keyframeTimeline(rung); t != nil {
		return t.segmentStart(segment)
	}
	return time.Duration(segment*rungSegmentSeconds(rung)) * time.Second
}

func (h *hlsSession) rung(id string) (Rung, bool) {
	for _, rung := range h.rungs {
		if rung.ID == id {
			return rung, true
		}
	}
	return Rung{}, false
}

// IsAudioCopyArtifact reports whether name addresses the audio-copy rung
// — the master URL is shared with the standard transcode, so the
// requested rung path is the byte layer's signal that the playback decision
// selected audio-only transcode (video copied, only audio transcoded).
func IsAudioCopyArtifact(name string) bool {
	return name == audioCopyRungID || strings.HasPrefix(name, audioCopyRungID+"/")
}

func (h *hlsSession) rendition(rung Rung) ffmpeg.Rendition {
	rendition := ffmpeg.Rendition{
		Height:           rung.Height,
		VideoCodec:       rung.Codec,
		VideoKbps:        rung.VideoKbps,
		CopyVideo:        rung.CopyVideo,
		AudioCodec:       h.audioProfile.Codec,
		AudioChannels:    h.audioProfile.Channels,
		AudioGainDB:      h.audioProfile.GainDB,
		AudioStreamIndex: h.audioStreamIndex,
		AudioOnly:        rung.AudioOnly,
		KeepPriming:      h.audioSource,
		ColorTransfer:    h.sourceVideo.ColorTransfer,
		ColorPrimaries:   h.sourceVideo.ColorPrimaries,
		DoViProfile:      h.sourceVideo.DoViProfile,
		DoViBLCompatID:   h.sourceVideo.DoViBLCompatID,

		SourceVideoCodec:    h.sourceVideo.Codec,
		SourceVideoBitDepth: h.sourceVideo.BitDepth,
	}
	if h.burnSubtitleIndex >= 0 {
		rendition = rendition.WithBurnSubtitle(h.burnSubtitleIndex)
	}
	if h.picture != nil {
		rendition = rendition.WithComposedPicture(*h.picture)
	}
	if h.encodePolicy != nil {
		rendition = rendition.WithEncodePolicy(*h.encodePolicy)
	}
	return rendition.WithAudioAdjust(h.adjust)
}

// Sessions owns the lifecycle of HLS transcode sessions:
// lazily started on first segment request, restarted on far seeks and
// rendition switches, capped server-wide with idle preemption, reaped after
// idleness, all stopped on shutdown.
type Sessions struct {
	transcoder    Transcoder
	dir           string
	now           func() time.Time
	root          *os.Root
	rootLock      *os.File
	setupMu       sync.Mutex
	paths         map[string]*scratchPath
	removing      int
	sweeping      bool
	closeOnce     sync.Once
	closeErr      error
	closeSweepErr error
	readScratch   func(fs.FS, string) ([]fs.DirEntry, error)
	mkdirDir      func(string, fs.FileMode) error
	join          func(*os.File, func(string) (*os.File, error), []string) error
	// durationProbes caches on-demand runtime probes (ProbedDurationSeconds).
	durationProbes    durationProbeCache
	incompleteSources incompleteSourceCache
	sourceStore       *store.Store
	// bootNonce tags every session directory this instance mints (see
	// newBootNonce): nextID resets to zero each process lifetime, so without it
	// a fresh instance's first session would mint the exact directory name a
	// prior instance already used.
	bootNonce string

	mu       sync.Mutex
	sessions map[SessionKey]*hlsSession
	nextID   int
	cap      int
	governor *governor.Governor
	// composer resolves the picture of a composed-picture session (see
	// SetPictureComposer); nil refuses such sessions.
	composer GenerationComposer
	// waitWindow, when positive, replaces the latency budgets as the length of
	// one artifact wait window. Tests set it so a slow-encoder scenario runs in
	// milliseconds instead of whole budgets.
	waitWindow time.Duration
	// stallTimeout, when positive, replaces encoderStallTimeout. Tests set it.
	stallTimeout time.Duration
	// resumeHints holds resume positions noted for sessions that do not exist
	// yet; session creation consumes them. Entries expire with the idle timeout.
	resumeHints map[SessionKey]resumeHint
	// stopEncoder, when set, replaces (*ffmpeg.HLSProcess).Stop. Tests set it
	// to a slow Stop.
	stopEncoder func(*ffmpeg.HLSProcess)
	// removeDir, when set, replaces scratch removal. Tests set it
	// to a slow removal.
	removeDir func(string) error
	// revocationPoll, when positive, replaces liveRevocationPoll. Tests set it.
	revocationPoll time.Duration
	// stopping counts encoders removed from the map whose teardown has not yet
	// stopped them (or whose off-lock start has not yet given up). They still
	// run, so they still count against the cap.
	stopping int
	// starting counts ensureRung starts running with mu released.
	starting int
	// detached counts sessionTeardowns handed out under mu that runTeardowns
	// has not finished, including ones that only remove a directory. Close
	// waits for starting, stopping and detached to reach zero so no encoder,
	// lease or directory outlives it.
	detached int
	// offLockIdle is closed when all admitted off-lock work finishes.
	offLockIdle chan struct{}
	// closed is set by Close; once set no session is created.
	closed bool

	// keyframes holds audio-copy keyframe timelines; it has its own lock.
	keyframes keyframeCache
}

// sessionTeardown is the slow half of removing a session: stopping its FFmpeg
// process (which waits for the exit) and deleting its working directory. A
// caller collects it under s.mu and runs it after releasing the lock, so one
// slow teardown never stalls requests for every other session. Whoever builds
// one under mu adds it to s.detached; runTeardowns takes it back out.
type sessionTeardown struct {
	session *hlsSession
	proc    *ffmpeg.HLSProcess
	// lease is the session's transcode lease, released only once proc has
	// stopped so the governor keeps background generation out until then.
	lease *governor.TranscodeLease
	// counted means detachment added this teardown to s.stopping.
	counted bool
	// Late starts and the idle reaper leave removal to the last user if busy.
	cleanupOnRelease bool
	// End access before waiting for directory owners to exit.
	liveRun *encderiv.LiveRun
	live    *encderiv.LiveSession
}

// runTeardowns stops detached encoders before waiting on directory references.
// Every detached path has exactly one initial removal owner.
func (s *Sessions) runTeardowns(teardowns []*sessionTeardown) {
	for _, t := range teardowns {
		if t.liveRun != nil {
			t.liveRun.Revoke()
		}
		s.stop(t.proc)
		if t.lease != nil {
			t.lease.Release()
		}
	}
	for _, t := range teardowns {
		p := t.session.scratch
		if t.cleanupOnRelease && p != nil {
			s.mu.Lock()
			if p.users > 0 {
				p.cleanup = t
				s.mu.Unlock()
				continue
			}
			s.mu.Unlock()
		}
		s.finishTeardown(t)
	}
}

func (s *Sessions) finishTeardown(t *sessionTeardown) {
	p := t.session.scratch
	if p != nil {
		<-p.drained
	}
	if t.live != nil {
		t.live.Close()
	}
	if p != nil {
		s.mu.Lock()
		s.claimScratchRemovalLocked(p)
		s.mu.Unlock()
		if err := s.removeScratch(p); err != nil {
			slog.Warn("live transcode directory cleanup failed", "error", err)
		}
	}
	s.mu.Lock()
	s.transitionLocked(t.session, t)
	s.mu.Unlock()
}

// wakeCloseLocked wakes Close once admitted off-lock work is complete.
// Callers hold mu.
func (s *Sessions) wakeCloseLocked() {
	if s.offLockIdle != nil && s.offLockIdleLocked() {
		close(s.offLockIdle)
		s.offLockIdle = nil
	}
}

// offLockIdleLocked reports whether the manager has joined its owned work. Callers hold mu.
func (s *Sessions) offLockIdleLocked() bool {
	if s.starting != 0 || s.stopping != 0 || s.detached != 0 || s.removing != 0 || s.sweeping {
		return false
	}
	// An allocation is an owner from reservation through setup, even before
	// its instance can appear among the heads Close just detached.
	for _, p := range s.paths {
		if p.state != scratchDebt {
			return false
		}
	}
	return true
}

// stop stops proc and waits for it to exit. Call it without s.mu held.
func (s *Sessions) stop(proc *ffmpeg.HLSProcess) {
	if proc == nil {
		return
	}
	if s.stopEncoder != nil {
		s.stopEncoder(proc)
		return
	}
	proc.Stop()
}

// holdsTranscodeLocked reports whether session occupies a transcode slot: it
// has a live process, or ensureRung is starting one. Callers hold mu.
func (session *hlsSession) holdsTranscodeLocked() bool {
	return session.proc != nil || session.start != nil
}

// resumeHint is a resume position noted by playback info.
type resumeHint struct {
	seconds int
	at      time.Time
}

// initRequest is the segment ensureRung is given for an init.mp4 request: any
// encoder already running for the rendition writes the init segment, and with
// none running the session starts one at its resume segment.
const initRequest = -1

// SetGovernor wires transcode-activity reporting (ADR-0013: transcode
// preempts generation). Optional; nil means no reporting.
func (s *Sessions) SetGovernor(g *governor.Governor) {
	s.governor = g
}

// SetTranscodeCap sets the per-server concurrent-transcode cap (ADR-0010 §6
// admission control). Values below 1 are clamped to 1.
func (s *Sessions) SetTranscodeCap(limit int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cap = max(1, limit)
}

// sessionEncodePolicy is the encode policy a session for existing's key
// runs under, and whether that policy lets it encode HEVC: existing's pinned
// snapshot when it has one, else the transcoder's current policy. A
// transcoder without an encode policy pins nothing.
func (s *Sessions) sessionEncodePolicy(existing *hlsSession) (*ffmpeg.EncodePolicy, bool) {
	source, ok := s.transcoder.(encodePolicySource)
	if !ok {
		return nil, s.SupportsTranscodeCodec("hevc")
	}
	var policy ffmpeg.EncodePolicy
	if existing != nil && existing.encodePolicy != nil {
		policy = *existing.encodePolicy
	} else {
		policy = source.EncodePolicy()
	}
	return &policy, source.SupportsTranscodeCodecUnder(policy, "hevc")
}

func (s *Sessions) SupportsTranscodeCodec(codec string) bool {
	supporter, ok := s.transcoder.(transcodeCodecSupporter)
	return ok && supporter.SupportsTranscodeCodec(codec)
}

// beginTranscode acquires one realtime-transcode lease from the governor before
// an FFmpeg process launches, so background generation is preempted the moment
// the session commits to spawning. A nil governor (tests) yields a
// nil lease and no reporting.
func (s *Sessions) beginTranscode() *governor.TranscodeLease {
	if s.governor == nil {
		return nil
	}
	return s.governor.BeginTranscode()
}

// segmentName matches a media segment as FFmpeg and the playlists spell it
// (seg%05d.m4s): five digits, or more past seg99999 without a leading zero.
// Capping the digits keeps the number within int, and rejecting padded aliases
// keeps one file name per segment.
var segmentName = regexp.MustCompile(`^seg(\d{5}|[1-9]\d{5,8})\.m4s$`)

// rungFileName matches the per-rendition artifacts under a rung directory.
var rungFileName = regexp.MustCompile(`^(index\.m3u8|init\.mp4|seg(?:\d{5}|[1-9]\d{5,8})\.m4s)$`)

// initSegmentName is the fMP4 initialization segment every #EXT-X-MAP line
// points at; ffmpeg writes it once per (re)start with identical parameters.
const initSegmentName = "init.mp4"

// masterPlaylistName is the multi-rendition entry point clients fetch first.
const masterPlaylistName = "master.m3u8"

// ActiveKeys returns the keys of every session with a live transcode
// process. The playbackInfo stream-cap check counts these per Profile.
func (s *Sessions) ActiveKeys() []SessionKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]SessionKey, 0, len(s.sessions))
	for key, session := range s.sessions {
		if session.holdsTranscodeLocked() {
			keys = append(keys, key)
		}
	}
	return keys
}

// CanAdmit reports whether a transcode for key could start now at the default
// PriorityNormal rank. It is the unchanged entry point the byte/decision layer
// calls (it has no priority signal); CanAdmitPriority carries an explicit rank.
func (s *Sessions) CanAdmit(key SessionKey) bool {
	return s.CanAdmitPriority(key, PriorityNormal)
}

// CanAdmitSession reports whether a normal-priority transcode for
// (sessionID, mediaID) could start right now. It is the join-time
// capacity probe the Watch-Party accept path consults to tell a joining member
// whether a transcode-needing client will be degraded to the lowest rung under
// current load. Defined on store-typed args so the graph consumer
// needs no playback.SessionKey import. The verdict is advisory and conservative
// (PriorityNormal): a member that later declares foreground may still be
// admitted at full quality by preempting a normal stream — the authoritative
// decision is the playbackInfo + byte path, never this hint.
func (s *Sessions) CanAdmitSession(sessionID store.SessionID, mediaID store.MediaID) bool {
	return s.CanAdmit(SessionKey{SessionID: sessionID, MediaID: mediaID})
}

// CanAdmitPriority reports whether a transcode for key at rank priority could
// start now (ADR-0010 §6: admission control is checked at start, so
// playbackInfo never advertises a transcode the server would refuse). True when
// key already holds a live transcode, a slot is free, or a session could be
// preempted for it — an idle session of any rank, or, for a higher-priority
// newcomer, a strictly-lower-priority session even while it streams.
func (s *Sessions) CanAdmitPriority(key SessionKey, priority Priority) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session, ok := s.sessions[key]; ok && session.holdsTranscodeLocked() {
		return true
	}
	if s.liveCount() < s.cap {
		return true
	}
	return s.preemptableLocked(key, priority) != nil
}

// liveCount counts sessions holding a live FFmpeg process, plus removed
// sessions whose encoder has not stopped yet. Callers hold mu.
func (s *Sessions) liveCount() int {
	count := s.stopping
	for _, session := range s.sessions {
		if session.holdsTranscodeLocked() {
			count++
		}
	}
	return count
}

// preemptableLocked picks the session a newcomer at rank priority may evict to
// claim a slot, or nil when none may be. Two kinds of session are
// eligible; the most-idle eligible one is chosen so an actively-streaming peer
// is sacrificed last:
//
//   - any other session idle longer than preemptIdleAfter, regardless of rank —
//     a paused client resumes transparently at position, so reaping it never
//     fails a stream (the established idle rule); and
//   - for a strictly-higher-priority newcomer only, an actively-streaming
//     session of strictly-lower priority — the privileged start wins the slot
//     and the lower-priority stream is the one that drops/queues.
//
// An active session of equal-or-higher rank is never a victim, so oversubscrip-
// tion never fails an equal-or-better stream mid-play. Callers hold mu.
func (s *Sessions) preemptableLocked(key SessionKey, priority Priority) *hlsSession {
	cutoff := s.now().Add(-preemptIdleAfter)
	var victim *hlsSession
	var oldest time.Time
	for other, session := range s.sessions {
		if other == key || !session.holdsTranscodeLocked() {
			continue
		}
		if session.start != nil {
			// Its encoder start runs with mu released and may still be stopping
			// the process it replaces, which a teardown could not wait for, so
			// the newcomer would launch while that process still held the slot.
			continue
		}
		idle := session.lastAccess.Before(cutoff)
		outranked := session.priority < priority
		if !idle && !outranked {
			continue // active and of equal-or-higher rank: protected
		}
		if victim == nil || session.lastAccess.Before(oldest) {
			oldest = session.lastAccess
			victim = session
		}
	}
	return victim
}

// admitLocked claims a transcode slot for key at rank priority, preempting the
// most-idle eligible session at capacity (an idle session of any rank, or a
// strictly-lower-priority active one for a higher-priority newcomer). A degraded
// (capacity-degraded Watch-Party) start is the bounded overflow valve: when no
// slot is free and none can be preempted, it is admitted over the cap rather
// than refused — ADR-0010 §6's "drop to this quality" answer, at the cheapest
// (lowest) rung, so the party stays whole. The over-cap
// admission is bounded server-side: only a real party member for the party's
// content reaches the byte layer with grant.Degraded set (the conductor roster
// is the gate), so a client cannot use it as a general cap bypass. Callers hold
// mu.
// A preempted victim's teardown is returned for the caller to run after
// releasing mu.
func (s *Sessions) admitLocked(key SessionKey, priority Priority, degraded bool) (*sessionTeardown, error) {
	if s.liveCount() < s.cap {
		return nil, nil
	}
	if victim := s.preemptableLocked(key, priority); victim != nil {
		return s.transitionLocked(victim, detachSession{}).teardown, nil
	}
	if degraded {
		return nil, nil // over-cap, lowest rung only: keep the party member streaming
	}
	return nil, ErrTranscodeCapacity
}

// TerminateSession aborts every in-flight transcode owned by one auth Session
// and returns how many it stopped (ADR-0012 §7: a revoked device's in-flight
// transcodes die, not just its future requests). Sessions are keyed by
// (SessionID, MediaID) — one Session may hold several live transcodes — so this
// stops all of them at once. It shares the detachment and teardown path with
// idle reaping and preemption. After releasing the lock, it stops the FFmpeg
// processes, deletes their artifacts, and releases their transcode slots.
// Calling it for a Session with no live transcode is a zero-stop no-op — a
// direct-play session has nothing to abort. The caller (the audited
// device-revocation mutation) invokes this after RevokeDevice has already
// blocked future auth; this closes the live transport that revocation alone
// leaves running until idle reap.
func (s *Sessions) TerminateSession(sessionID store.SessionID) int {
	s.mu.Lock()
	var teardowns []*sessionTeardown
	for key, session := range s.sessions {
		if key.SessionID == sessionID {
			teardowns = append(teardowns, s.transitionLocked(session, detachSession{}).teardown)
		}
	}
	s.mu.Unlock()
	s.runTeardowns(teardowns)
	return len(teardowns)
}

// TerminatePlayback aborts the in-flight transcodes one auth Session holds for
// one Media and returns how many it stopped (CORE-2345). A player that closes
// calls endPlayback, and without this its FFmpeg process kept running until
// idle reap. It stops every profile-capability variant of that (SessionID,
// MediaID) pair through the same detachment and teardown path as
// TerminateSession, and leaves the Session's other Media alone. Ending a
// playback with no live transcode (direct play) is a zero-stop no-op.
func (s *Sessions) TerminatePlayback(sessionID store.SessionID, mediaID store.MediaID) int {
	s.mu.Lock()
	var teardowns []*sessionTeardown
	for key, session := range s.sessions {
		if key.SessionID == sessionID && key.MediaID == mediaID {
			teardowns = append(teardowns, s.transitionLocked(session, detachSession{}).teardown)
		}
	}
	s.mu.Unlock()
	s.runTeardowns(teardowns)
	return len(teardowns)
}

// newBootNonce mints an 8-byte crypto-random hex tag, ignoring the
// (practically unreachable, per crypto/rand's contract) read error the same
// way this package's other ephemeral-id generators do: it is not a durable
// key, only a per-process disambiguator, so a crypto-random hex string is
// sufficient and collision-free in practice.
func newBootNonce() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// FilePath returns the on-disk path of one HLS artifact, starting (or seek- or
// rendition-switch-restarting) the transcode as needed and waiting for the
// file to appear. The name space is master.m3u8 plus <rung>/<artifact>; the
// rung set is derived from the source dimensions, so a rung above the source
// does not exist (honest ABR — ADR-0010 §8). durationSeconds drives the
// synthesized per-rung VOD playlists; zero falls back to the legacy linear
// mode where ffmpeg owns the variant playlist. audioCopy selects the
// audio-only-transcode mode: the session offers the single
// audio-copy rung (video muxed through, only audio transcoded) instead of the
// resolution ladder, so the served rendition carries -c:v copy.
func (s *Sessions) FilePath(ctx context.Context, key SessionKey, src string, durationSeconds, videoWidth, videoHeight int, audioCopy bool, name string) (string, error) {
	return s.FilePathPriority(ctx, key, src, durationSeconds, videoWidth, videoHeight, audioCopy, false, VideoSignal{}, PriorityNormal, name)
}

// FilePathPriority is FilePath with an explicit admission rank.
// The rank is the session's standing the first time an encode is admitted and
// is raised, never lowered, on a later higher-rank request; it governs which
// other session this one may preempt at capacity. degraded caps a capacity-
// degraded Watch-Party session to the single lowest rung and lets its admission
// exceed the cap (ADR-0010 §6 "drop to this quality"; PANEL-16). FilePath
// delegates here at PriorityNormal and degraded=false, so the unchanged entry
// point keeps the equal-priority, idle-only, full-ladder behaviour.
func (s *Sessions) FilePathPriority(ctx context.Context, key SessionKey, src string, durationSeconds, videoWidth, videoHeight int, audioCopy, degraded bool, sourceVideo VideoSignal, priority Priority, name string) (string, error) {
	return s.FilePathPriorityWithBurnSubtitle(ctx, key, src, durationSeconds, videoWidth, videoHeight, audioCopy, degraded, sourceVideo, -1, priority, name)
}

// FilePathPriorityWithBurnSubtitle is FilePathPriority plus a selected subtitle
// stream index for bitmap-subtitle burn-in.
func (s *Sessions) FilePathPriorityWithBurnSubtitle(ctx context.Context, key SessionKey, src string, durationSeconds, videoWidth, videoHeight int, audioCopy, degraded bool, sourceVideo VideoSignal, burnSubtitleIndex int, priority Priority, name string) (string, error) {
	return s.FilePathPriorityWithBurnSubtitleAudio(ctx, key, src, durationSeconds, videoWidth, videoHeight, audioCopy, degraded, sourceVideo, burnSubtitleIndex, ffmpeg.AudioProfile{}, priority, name)
}

func (s *Sessions) FilePathPriorityWithBurnSubtitleAudio(ctx context.Context, key SessionKey, src string, durationSeconds, videoWidth, videoHeight int, audioCopy, degraded bool, sourceVideo VideoSignal, burnSubtitleIndex int, audioProfile ffmpeg.AudioProfile, priority Priority, name string) (string, error) {
	return s.FilePathPriorityWithBurnSubtitleAudioLadder(ctx, key, src, durationSeconds, videoWidth, videoHeight, audioCopy, degraded, ladderCaps{}, sourceVideo, burnSubtitleIndex, audioProfile, priority, name)
}

func (s *Sessions) FilePathPriorityWithBurnSubtitleAudioLadder(ctx context.Context, key SessionKey, src string, durationSeconds, videoWidth, videoHeight int, audioCopy, degraded bool, caps ladderCaps, sourceVideo VideoSignal, burnSubtitleIndex int, audioProfile ffmpeg.AudioProfile, priority Priority, name string) (string, error) {
	return s.FilePathPriorityWithBurnSubtitleAudioLadderMap(ctx, key, src, durationSeconds, videoWidth, videoHeight, audioCopy, degraded, caps, sourceVideo, burnSubtitleIndex, audioProfile, nil, priority, name)
}

func (s *Sessions) FilePathPriorityWithBurnSubtitleAudioLadderMap(ctx context.Context, key SessionKey, src string, durationSeconds, videoWidth, videoHeight int, audioCopy, degraded bool, caps ladderCaps, sourceVideo VideoSignal, burnSubtitleIndex int, audioProfile ffmpeg.AudioProfile, audioStreamIndex *int, priority Priority, name string) (string, error) {
	return s.FilePathShaped(ctx, key, src, durationSeconds, videoWidth, videoHeight, audioCopy, degraded, caps, sourceVideo, burnSubtitleIndex, audioProfile, audioStreamIndex, StreamShape{}, priority, name)
}

// FilePathShaped is FilePathPriorityWithBurnSubtitleAudioLadderMap with the
// grant's StreamShape, which can reshape the session beyond its ladder.
func (s *Sessions) FilePathShaped(ctx context.Context, key SessionKey, src string, durationSeconds, videoWidth, videoHeight int, audioCopy, degraded bool, caps ladderCaps, sourceVideo VideoSignal, burnSubtitleIndex int, audioProfile ffmpeg.AudioProfile, audioStreamIndex *int, shape StreamShape, priority Priority, name string) (string, error) {
	artifact, err := s.LiveArtifactShaped(ctx, key, src, durationSeconds, videoWidth, videoHeight, audioCopy, degraded, caps, sourceVideo, burnSubtitleIndex, audioProfile, audioStreamIndex, shape, priority, name)
	if err != nil {
		return "", err
	}
	defer artifact.Release()
	return artifact.path, nil
}

// LiveArtifactShaped selects and holds one live artifact. Its caller releases
// it after closing any reader opened from it.
func (s *Sessions) LiveArtifactShaped(ctx context.Context, key SessionKey, src string, durationSeconds, videoWidth, videoHeight int, audioCopy, degraded bool, caps ladderCaps, sourceVideo VideoSignal, burnSubtitleIndex int, audioProfile ffmpeg.AudioProfile, audioStreamIndex *int, shape StreamShape, priority Priority, name string) (*LiveArtifact, error) {
	if shape.Encrypted != nil && shape.ComposedPicture {
		return nil, ErrEncryptedLiveUnavailable // ADR-0236 D4
	}
	picture, err := s.livePicture(ctx, key, src, shape)
	if err != nil {
		return nil, err
	}
	// The keyframe timeline lays out the copied-video rung, which a composed
	// picture or an audio adjustment replaces with a single encoded rung.
	// An audio-only shape never copies a picture stream through: the audio
	// class's embedded art is not video.
	if shape.AudioOnly {
		audioCopy = false
	}
	var timeline *keyframeTimeline
	if audioCopy && durationSeconds > 0 && picture == nil && shape.Adjust.IsZero() && shape.Encrypted == nil {
		if timeline, err = s.copyVideoTimeline(ctx, key, src); err != nil {
			return nil, err
		}
	}
	audioOnlyPeak := audioOnlyBandwidth(audioProfile, shape.AudioSource, !shape.Adjust.IsZero())
	session, err := s.session(ctx, key, src, durationSeconds, timeline, videoWidth, videoHeight, audioCopy, degraded, caps, sourceVideo, burnSubtitleIndex, audioProfile, audioStreamIndex, picture, shape.Adjust, shape.AudioOnly, shape.AudioSource, audioOnlyPeak, shape.Encrypted, shape.LibraryID, priority)
	if err != nil {
		return nil, err
	}
	path, err := s.resolveArtifactPath(ctx, key, session, name)
	s.mu.Lock()
	evicted := session.state != sessionRunning
	s.mu.Unlock()
	if evicted || err != nil {
		s.releaseScratch(session.scratch)
		if evicted {
			return nil, ErrSessionEvicted
		}
		return nil, err
	}
	return &LiveArtifact{manager: s, session: session, path: path}, nil
}

func (s *Sessions) resolveArtifactPath(ctx context.Context, key SessionKey, session *hlsSession, name string) (string, error) {
	var err error
	if name == masterPlaylistName {
		return filepath.Join(session.dir, masterPlaylistName), nil
	}
	rungID, file, ok := strings.Cut(name, "/")
	if !ok || !rungFileName.MatchString(file) {
		return "", errors.New("unknown stream file")
	}
	rung, ok := session.rung(rungID)
	if !ok {
		return "", errors.New("unknown stream file")
	}
	path := filepath.Join(session.dir, rungID, file)
	waitBudget := s.transcodeLatencyBudget(session, path)

	segment := 0
	var run *encoderRun
	switch {
	case file == "index.m3u8":
		if session.durationSeconds > 0 {
			return path, nil // synthesized at session creation
		}
		// Linear mode: ffmpeg owns the variant playlist, so it must run.
		if run, err = s.ensureRung(ctx, key, session, rung, 0, path); err != nil {
			return "", err
		}
	case file == initSegmentName:
		segment = initRequest
		if run, err = s.ensureRung(ctx, key, session, rung, segment, path); err != nil {
			return "", err
		}
	default:
		match := segmentName.FindStringSubmatch(file)
		segment, _ = strconv.Atoi(match[1])
		if session.durationSeconds > 0 && segment >= session.playlistSegmentCount(rung) {
			return "", errors.New("unknown stream file")
		}
		path, err := s.playlistSegment(ctx, key, session, rung, segment)
		if err != nil {
			return "", err
		}
		s.markDelivered(session, rung.ID)
		return path, nil
	}
	if err := s.waitWhileEncoderProgresses(ctx, key, session, rung, segment, false, path, run, waitBudget); err != nil {
		return "", err
	}
	if file != "index.m3u8" {
		s.markDelivered(session, rung.ID)
	}
	return path, nil
}

// playlistSegment resolves the media segment rung's playlist lists at index
// segment. On the uniform grid that is the encoder's own file. On a keyframe
// timeline a segment that merges several GOPs (CORE-1843) is joined from their
// files once and kept beside them; when a truncated source ends inside it, the
// segment carries the media before the end.
func (s *Sessions) playlistSegment(ctx context.Context, key SessionKey, session *hlsSession, rung Rung, segment int) (string, error) {
	first, end := session.encoderSegments(rung, segment)
	if end-first == 1 {
		return s.encoderSegment(ctx, key, session, rung, first)
	}
	joined := filepath.Join(session.dir, rung.ID, fmt.Sprintf("joined%05d.m4s", segment))
	if session.artifactReady(joined) {
		return joined, nil
	}
	parts := make([]string, 0, end-first)
	for part := first; part < end; part++ {
		path, err := s.encoderSegment(ctx, key, session, rung, part)
		if errors.Is(err, ErrSourceEnded) && len(parts) > 0 {
			break
		}
		if err != nil {
			return "", err
		}
		parts = append(parts, path)
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	if err := s.joinArtifact(ctx, session, joined, parts); err != nil {
		return "", err
	}
	return joined, nil
}

// encoderSegment waits for encoder segment file segment of rung, starting or
// moving the encoder when the running one will not reach it.
func (s *Sessions) encoderSegment(ctx context.Context, key SessionKey, session *hlsSession, rung Rung, segment int) (string, error) {
	path := filepath.Join(session.dir, rung.ID, fmt.Sprintf("seg%05d.m4s", segment))
	waitBudget := s.transcodeLatencyBudget(session, path)
	if s.beyondSourceEnd(session, rung, segment) {
		return "", ErrSourceEnded // known short source: never encode an empty segment
	}
	run, err := s.ensureRung(ctx, key, session, rung, segment, path)
	if err != nil {
		return "", err
	}
	if err := s.waitWhileEncoderProgresses(ctx, key, session, rung, segment, true, path, run, waitBudget); err != nil {
		return "", err
	}
	_, observed, observeErr := s.observeIncompleteSource(key, session, func() (time.Duration, bool) {
		return session.segmentStart(rung, segment), session.segmentLacksMedia(path)
	})
	if observed {
		// FFmpeg started past the real end of a truncated source writes a
		// segment with no media; serving it as playable would stall the player.
		s.noteSourceEnd(key, session, rung, segment, "")
		return "", ErrSourceEnded
	}
	if observeErr != nil {
		return "", observeErr
	}
	return path, nil
}

// waitForEncoder waits for path while watching the encoder run that should
// produce it. An encoder that crashes under the wait is restarted at the
// requested segment inside this request (at most maxRequestEncoderRestarts
// times, and within the session's crash-restart budget); an encoder that hit
// end of input fails the request at once instead of letting it wait out its
// budget; an encoder the server stopped (switch, seek, teardown) leaves the
// request waiting for its replacement as before.
func (s *Sessions) waitForEncoder(ctx context.Context, key SessionKey, session *hlsSession, rung Rung, segment int, isSegment bool, path string, run *encoderRun) error {
	restarts := 0
	for {
		if run != nil {
			run.waiting.Add(1)
		}
		err := waitForArtifact(ctx, path, run.exitedChan(), session.scratch.cutoff, session.artifactReady)
		if run != nil {
			run.waiting.Add(-1)
		}
		if !errors.Is(err, errEncoderExited) {
			return err
		}
		switch run.outcome {
		case exitStopped:
			s.mu.Lock()
			live := session.state == sessionRunning
			s.mu.Unlock()
			if !live {
				return ErrSessionEvicted
			}
			run = nil // replaced by another request's encoder: keep waiting
		case exitEndOfInput:
			if isSegment && s.beyondSourceEnd(session, rung, segment) {
				return ErrSourceEnded
			}
			return errEncoderEnded
		default:
			if restarts >= maxRequestEncoderRestarts {
				return errEncoderFailing
			}
			restarts++
			if run, err = s.ensureRung(ctx, key, session, rung, segment, path); err != nil {
				return err
			}
			slog.Warn("restarted live transcode at the requested segment",
				"media", key.MediaID, "rung", rung.ID, "segment", segment, "attempt", restarts)
		}
	}
}

// waitWhileEncoderProgresses runs waitForEncoder in budget-long windows. When
// a window expires on a segment and the session is healthy but behind the
// playhead, the request keeps waiting for the bytes instead of failing into a
// retryable 503 (CORE-0044). Healthy means the session's encoder for rung was
// this request's run for the whole window, that run started at or before the
// requested segment, and it wrote a segment during the window. Anything else,
// such as a stalled run, a run another request replaced or restarted, or a run
// started past the segment, fails as before. A run writes its segments in
// order from its base, and the request is at most seekLookaheadSegments past
// the frontier when the wait starts, so the number of extra windows is bounded.
func (s *Sessions) waitWhileEncoderProgresses(ctx context.Context, key SessionKey, session *hlsSession, rung Rung, segment int, isSegment bool, path string, run *encoderRun, budget time.Duration) error {
	for {
		windowStart := time.Now()
		current, _, tracked := s.rungEncoder(session, rung)
		waitCtx, cancel := context.WithTimeout(ctx, budget)
		err := s.waitForEncoder(waitCtx, key, session, rung, segment, isSegment, path, run)
		cancel()
		if err == nil || !isSegment || ctx.Err() != nil || !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		after, base, stillTracked := s.rungEncoder(session, rung)
		if !tracked || !stillTracked || current != run || after != run || base > segment ||
			!session.segmentWrittenSince(rung.ID, base, windowStart) {
			return err
		}
	}
}

// rungEncoder returns the session's running encoder for rung and the segment it
// started at. tracked is false when the session is gone or is not encoding rung.
func (s *Sessions) rungEncoder(session *hlsSession, rung Rung) (run *encoderRun, base int, tracked bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session.state != sessionRunning || session.proc == nil || session.currentRung != rung.ID {
		return nil, 0, false
	}
	return session.run, session.baseSegment, true
}

// segmentWrittenSince reports whether rungDir holds a segment at or past base
// modified at or after since. Segments cached by earlier runs keep their older
// times, so only this run's writes count.
func (h *hlsSession) segmentWrittenSince(rung string, base int, since time.Time) bool {
	entries, err := h.scratch.readRung(rung)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		match := segmentName.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		if segment, _ := strconv.Atoi(match[1]); segment < base {
			continue
		}
		f, info, err := openScratchMember(h.scratch.dir, filepath.Join(rung, entry.Name()))
		if err != nil {
			continue
		}
		_ = f.Close()
		if !info.ModTime().Before(since) {
			return true
		}
	}
	return false
}

// beyondSourceEnd reports whether segment starts past the session's known
// source end.
func (s *Sessions) beyondSourceEnd(session *hlsSession, rung Rung, segment int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return session.beyondSourceEndLocked(rung, segment)
}

// noteSourceEnd records that the source has no media from rung's segment on
// and logs it once per new (lower) bound.
func (s *Sessions) noteSourceEnd(key SessionKey, session *hlsSession, rung Rung, segment int, stderr string) {
	s.mu.Lock()
	lowered := s.transitionLocked(session, recordSourceEnd{rung: rung, segment: segment}).lowered
	s.mu.Unlock()
	if lowered {
		logSourceEnd(key, session, rung, segment, stderr)
	}
}

func logSourceEnd(key SessionKey, session *hlsSession, rung Rung, segment int, stderr string) {
	slog.Warn("media source ends before its probed duration; refusing segments past the end",
		"media", key.MediaID,
		"session_dir", filepath.Base(session.dir),
		"rung", rung.ID,
		"source_end_seconds", session.segmentStart(rung, segment).Seconds(),
		"source_end_segment", segment,
		"probed_duration_seconds", session.durationSeconds,
		"stderr", stderr)
}

// markDelivered records the rendition a media request was just served from. A
// player switching back to a rendition already on disk starts no encoder, so
// the encoder start alone would keep reporting the previous rendition.
// Variant playlists do not count: a player may read several while choosing.
func (s *Sessions) markDelivered(session *hlsSession, rungID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transitionLocked(session, deliverSessionRung{id: rungID})
}

// transcodeLatencyBudget classifies a missing artifact before ensureRung
// starts or replaces FFmpeg. Cached artifacts are returned before waiting, so
// only first-start and post-start encode paths consume these numeric SLOs.
func (s *Sessions) transcodeLatencyBudget(session *hlsSession, path string) time.Duration {
	if session.artifactReady(path) {
		return fileWaitTimeout
	}
	if s.waitWindow > 0 {
		return s.waitWindow
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if session.started {
		return PostSeekTranscodeLatencyBudget
	}
	return TranscodeStartLatencyBudget
}

// session returns the (created-on-demand) session state for key. Creation
// writes the master playlist and the synthesized per-rung VOD playlists; no
// FFmpeg process starts until a rung artifact is requested, so admission
// control runs at real encode start. audioCopy seeds the single audio-copy
// rung in place of the resolution ladder, so the master and
// served rendition copy the source video and transcode only the audio. A
// non-zero adjust stretches durationSeconds (a source duration) onto the
// adjusted output timeline and, without a picture, offers the single
// audio-only rung. audioOnly (an audio-class Media) offers that rung too.
func (s *Sessions) session(ctx context.Context, key SessionKey, src string, durationSeconds int, timeline *keyframeTimeline, videoWidth, videoHeight int, audioCopy, degraded bool, caps ladderCaps, sourceVideo VideoSignal, burnSubtitleIndex int, audioProfile ffmpeg.AudioProfile, audioStreamIndex *int, picture *ComposedPicture, adjust ffmpeg.AudioAdjust, audioOnly bool, audioSourceStream store.MediaFileStream, audioOnlyPeak int, secure *EncryptedSource, library store.LibraryID, priority Priority) (*hlsSession, error) {
	// Capture the generation before encoding, outside the session lock.
	var sourceInfo os.FileInfo
	if secure == nil {
		sourceInfo, _ = os.Stat(src)
	}
	sourceGeneration := store.SourceFileGeneration(sourceInfo)
	if secure != nil {
		sourceGeneration = store.EncryptedSourceGeneration(secure.Fingerprint)
	}
	var teardowns []*sessionTeardown
	s.setupMu.Lock()
	s.mu.Lock()
	defer func() {
		s.mu.Unlock()
		s.setupMu.Unlock()
		s.runTeardowns(teardowns)
	}()
	if s.closed {
		return nil, ErrSessionEvicted
	}
	// One encode policy decides both the ladder and the encoder (CORE-2452):
	// an existing session's pinned snapshot, which a reshaped replacement
	// inherits, so a Console save in either direction never changes the rungs
	// of a session already playing; otherwise the current policy, read once.
	existing, hasExisting := s.sessions[key]
	policy, hevcAllowed := s.sessionEncodePolicy(existing)
	if caps.HEVC && !hevcAllowed {
		caps.HEVC = false
		caps.HEVCMaxHeight = 0
	}
	requestedRungs := rungsForSession(videoWidth, videoHeight, audioCopy, degraded, caps)
	if picture != nil {
		rung, err := composedLiveRung(caps)
		if err != nil {
			return nil, err
		}
		requestedRungs = []Rung{rung}
	} else if audioOnly || !adjust.IsZero() {
		requestedRungs = []Rung{audioOnlyRung(audioOnlyPeak)}
	}
	durationSeconds = adjust.OutputSeconds(durationSeconds)
	audioSource := videoWidth <= 0 && videoHeight <= 0 && picture == nil
	var primed primedGrid
	if audioOnly && audioSource && adjust.IsZero() && durationSeconds > 0 {
		primed = primedAACGrid(audioSourceStream, audioProfile, segmentSeconds)
	}
	if secure != nil {
		if err := encryptedSessionRefusal(secure, durationSeconds, burnSubtitleIndex, picture); err != nil {
			return nil, err
		}
	}
	if secure != nil {
		library = store.LibraryID(secure.LibraryID)
	}
	if hasExisting {
		if existing.scratch.library != library || existing.sourceGeneration != sourceGeneration || (existing.sourceInfo != nil && !sameSourceFile(existing.sourceInfo, sourceInfo)) || existing.src != src || existing.durationSeconds != durationSeconds || existing.primed != primed || existing.timeline != timeline || existing.degraded != degraded || !rungsEqual(existing.rungs, requestedRungs) || existing.sourceVideo != sourceVideo || existing.burnSubtitleIndex != burnSubtitleIndex || !audioProfilesEqual(existing.audioProfile, audioProfile) || !equalOptionalInt(existing.audioStreamIndex, audioStreamIndex) || (existing.composed != nil) != (picture != nil) || existing.adjust != adjust || (existing.secure != nil) != (secure != nil) {
			teardowns = append(teardowns, s.transitionLocked(existing, detachSession{}).teardown) // mode-shaping changed; old artifacts are incompatible
		} else {
			// A position noted while this session ran was kept only for a
			// reshaped replacement; the unchanged session ignores it.
			s.transitionLocked(existing, reuseSession{priority: priority})
			existing.scratch.users++
			return existing, nil
		}
	}
	scratch, err := s.allocateScratchLocked(ctx, library)
	if scratch == nil {
		return nil, err
	}
	dir := filepath.Join(s.dir, scratch.name)
	lastAccess := s.now()
	var resumeSeconds int
	if hint, ok := s.resumeHints[key]; ok {
		delete(s.resumeHints, key)
		if s.now().Sub(hint.at) < sessionIdleTimeout {
			resumeSeconds = hint.seconds
		}
	}
	session := &hlsSession{
		key:               key,
		dir:               dir,
		scratch:           scratch,
		src:               src,
		sourceInfo:        sourceInfo,
		sourceGeneration:  sourceGeneration,
		durationSeconds:   durationSeconds,
		timeline:          timeline,
		rungs:             requestedRungs,
		audioProfile:      audioProfile,
		audioStreamIndex:  audioStreamIndex,
		sourceVideo:       sourceVideo,
		burnSubtitleIndex: burnSubtitleIndex,
		audioSource:       audioSource,
		primed:            primed,
		lastAccess:        lastAccess,
		priority:          priority,
		degraded:          degraded,
		adjust:            adjust,
		secure:            secure,
		encodePolicy:      policy,
		resumeSeconds:     resumeSeconds,
	}
	if err == nil {
		err = ctx.Err()
		if s.closed {
			err = ErrSessionEvicted
		}
	}
	if err == nil && secure != nil {
		session.live, err = encderiv.NewLiveSession(scratch.writerDir, secure.LibraryID)
	}
	if err == nil {
		err = writeSessionPlaylists(scratch.root, requestedRungs, durationSeconds, timeline, primed, session.writePlaylist)
	}
	var preparedPicture *ffmpeg.ComposedPicture
	if err == nil && picture != nil {
		preparedPicture, err = writeLivePicture(scratch, *picture)
	}
	if err != nil {
		scratch.users--
		teardowns = append(teardowns, s.transitionLocked(session, detachSession{}).teardown)
		return nil, err
	}
	if !s.transitionLocked(session, publishSession{composed: picture, picture: preparedPicture}).accepted {
		scratch.users--
		teardowns = append(teardowns, s.transitionLocked(session, detachSession{}).teardown)
		return nil, ErrSessionEvicted
	}
	if session.revokeDone != nil {
		go s.watchLiveRevocation(session, session.revokeDone, secure.Revoked, s.liveRevocationInterval())
	}
	return session, nil
}

func equalOptionalInt(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func audioProfilesEqual(a, b ffmpeg.AudioProfile) bool {
	if a.Codec != b.Codec || a.Channels != b.Channels {
		return false
	}
	switch {
	case a.GainDB == nil && b.GainDB == nil:
		return true
	case a.GainDB == nil || b.GainDB == nil:
		return false
	default:
		return *a.GainDB == *b.GainDB
	}
}

func rungsForSession(videoWidth, videoHeight int, audioCopy, degraded bool, caps ladderCaps) []Rung {
	rungs := rungsForLadder(videoWidth, videoHeight, caps)
	if audioCopy {
		rungs = []Rung{audioCopyRung()}
	}
	// Capacity-degrade: cap to the single lowest rung so
	// the session advertises one rendition (also honors §8: one rendition per
	// live session). For audio-copy this is already one rung, so the cap is a no-op.
	if degraded {
		rungs = []Rung{lowestRung(rungs)}
	}
	return rungs
}

func rungsEqual(left, right []Rung) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// writeSessionPlaylists lays out the session directory: the master playlist,
// one subdirectory per rung, and (for known durations) the synthesized VOD
// playlist each variant serves before a single segment exists. The audio-copy
// rung's playlist follows timeline when there is one. write stores one
// playlist under its slash-separated name relative to dir.
func writeSessionPlaylists(root *os.Root, rungs []Rung, durationSeconds int, timeline *keyframeTimeline, primed primedGrid, write func(rel string, data []byte) error) error {
	if err := write(masterPlaylistName, []byte(masterPlaylist(rungs))); err != nil {
		return err
	}
	for _, rung := range rungs {
		if err := root.Mkdir(rung.ID, 0o700); err != nil {
			return err
		}
		if durationSeconds > 0 {
			playlist := vodPlaylist(durationSeconds, rungSegmentSeconds(rung), primed)
			if rung.CopyVideo && timeline != nil {
				playlist = timeline.playlist()
			}
			if err := write(rung.ID+"/index.m3u8", []byte(playlist)); err != nil {
				return err
			}
		}
	}
	return nil
}

// ensureRung makes the live FFmpeg process cover (rung, segment): it starts
// the first process (through admission control), replaces it on a rendition
// switch (same session, new rendition stream — ADR-0010 §8's restart-at-a-
// new-bitrate, reusing the seek-restart machinery), and restarts it when a
// seek lands outside the produced window. It returns the encoder run that will
// produce path, or nil when path already exists.
func (s *Sessions) ensureRung(ctx context.Context, key SessionKey, session *hlsSession, rung Rung, segment int, path string) (*encoderRun, error) {
	select {
	case <-session.scratch.cutoff:
		return nil, ErrSessionEvicted
	default:
	}
	if session.artifactReady(path) {
		return nil, nil // already produced (possibly by an earlier rendition run)
	}
	session.startMu.Lock()
	defer session.startMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if session.state != sessionRunning {
		return nil, ErrSessionEvicted // torn down between s.session() and this lock
	}
	if session.artifactReady(path) {
		return nil, nil
	}
	rungDir := filepath.Join(session.scratch.writerDir, rung.ID)
	if segment == initRequest {
		if session.proc != nil && session.currentRung == rung.ID {
			return session.run, nil // every encoder writes init.mp4 when it starts
		}
		segment = session.resumeSegmentLocked(rung)
	}
	if session.proc != nil && session.currentRung == rung.ID {
		if session.durationSeconds == 0 {
			return session.run, nil // linear mode: the running process will reach it
		}
		if segment >= session.baseSegment && segment <= session.frontier+seekLookaheadSegments {
			return session.run, nil // within the cached frontier; the running transcode reaches it
		}
		// Cache miss: the request is beyond the cached frontier, so refresh from
		// disk — but never hold s.mu across the ReadDir. Drop the lock, scan, then
		// re-acquire and re-validate before trusting the refreshed frontier: the
		// session may have been evicted or its run may have exited.
		base := session.baseSegment
		run := session.run
		s.mu.Unlock()
		scanned := session.scanFrontier(rung.ID, base)
		s.mu.Lock()
		if session.state != sessionRunning {
			return nil, ErrSessionEvicted // torn down while the frontier scan ran off-lock
		}
		if s.transitionLocked(session, advanceFrontier{run: run, rungID: rung.ID, base: base, scanned: scanned}).accepted &&
			segment >= session.baseSegment && segment <= session.frontier+seekLookaheadSegments {
			return session.run, nil // the running transcode will reach it; wait outside the lock
		}
	}
	var victim *sessionTeardown
	if session.proc == nil {
		if session.transcodeFailed {
			return nil, ErrTranscodeFailed
		}
		// The last encoder crashed: this start is a crash restart, bounded by
		// the session budget so a source that kills FFmpeg fails fast.
		if session.lastCrashed && !s.transitionLocked(session, takeEncoderRestart{}).accepted {
			return nil, errEncoderFailing
		}
		var err error
		if victim, err = s.admitLocked(key, session.priority, session.degraded); err != nil {
			return nil, err
		}
	}
	// Stop and spawn outside mu. The claim keeps the slot counted while
	// startMu prevents a second request from starting another encoder.
	start := &encoderStart{}
	s.transitionLocked(session, start)
	s.mu.Unlock()
	if victim != nil {
		s.runTeardowns([]*sessionTeardown{victim})
	}
	if start.replacedLive != nil {
		start.replacedLive.Revoke()
	}
	s.stop(start.replaced)
	procCtx := context.WithoutCancel(ctx)
	var proc *ffmpeg.HLSProcess
	var liveRun *encderiv.LiveRun
	var err error
	if session.secure != nil {
		// The encrypted encode shares the primed grid; segments the earlier
		// start rewrites are refused as existing and the first copy stays.
		segment = session.primed.restartSegment(segment)
		proc, liveRun, err = s.startEncryptedRung(procCtx, session, rung, segment)
	} else if t := session.keyframeTimeline(rung); t != nil && session.durationSeconds > 0 {
		// Copied video restarts at segment's keyframe, stamped with that
		// keyframe's real time, so the media matches the playlist (CORE-0021).
		proc, err = s.transcoder.(keyframeTranscoder).StartHLSKeyframeSegments(procCtx, session.src, rungDir, segment, t.segmentStart(segment), session.rendition(rung))
	} else if session.durationSeconds > 0 {
		segment = session.primed.restartSegment(segment)
		proc, err = s.transcoder.StartHLSSegments(procCtx, session.src, rungDir, segment, rungSegmentSeconds(rung), session.rendition(rung))
	} else {
		proc, err = s.transcoder.StartHLS(procCtx, session.src, rungDir, session.rendition(rung))
	}
	s.mu.Lock()
	var run *encoderRun
	if err == nil {
		run = &encoderRun{proc: proc, live: liveRun}
	}
	if !s.transitionLocked(session, completeEncoderStart{start: start, run: run, rungID: rung.ID, segment: segment}).accepted {
		// The artifact request still holds the directory. Stop the late
		// child and finish its claim before releasing that reference.
		s.mu.Unlock()
		if err == nil {
			if liveRun != nil {
				liveRun.Revoke()
			}
			s.stop(proc)
		}
		s.mu.Lock()
		s.transitionLocked(session, finishDetachedStart{start: start})
		return nil, ErrSessionEvicted
	}
	if err != nil {
		return nil, err
	}
	if run.exited != nil {
		session.scratch.users++
		s.reapProcess(key, session, run, rung, segment)
		if proc.ReportsProgress() {
			go watchForStall(run, s.encoderStallTimeout())
		}
		if liveRun != nil {
			go watchLiveRun(run)
		}
	} else if liveRun != nil {
		liveRun.Revoke() // no process to serve
	}
	return run, nil
}

// resumeSegmentLocked is the segment an encoder started for an init.mp4
// request begins at: the one containing the resume position, or 0 when there
// is none or it lies past the known end of the source.
func (session *hlsSession) resumeSegmentLocked(rung Rung) int {
	if session.resumeSeconds <= 0 || session.durationSeconds <= 0 {
		return 0
	}
	segment := session.adjust.OutputPosition(session.resumeSeconds) / rungSegmentSeconds(rung)
	if t := session.keyframeTimeline(rung); t != nil {
		segment = t.segmentAt(time.Duration(session.resumeSeconds) * time.Second)
	}
	if segment >= session.segmentCount(rung) || session.beyondSourceEndLocked(rung, segment) {
		return 0
	}
	return segment
}

// NoteResumePosition records where playback info resumed the viewer of key's
// HLS session, so the session's first encoder starts there. A position of 0
// clears it. A session that already started an encoder ignores it.
func (s *Sessions) NoteResumePosition(key SessionKey, seconds int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, hint := range s.resumeHints {
		if now.Sub(hint.at) >= sessionIdleTimeout {
			delete(s.resumeHints, k)
		}
	}
	if session, ok := s.sessions[key]; ok {
		delete(s.resumeHints, key)
		if s.transitionLocked(session, setSessionResume{seconds: seconds}).accepted {
			return
		}
		// A running session keeps its encoder, but a request that reshapes
		// it (a new pitch shift or tempo, say) replaces it: that replacement
		// starts at this position. The next request that reuses the session
		// unchanged drops it.
		if seconds > 0 {
			s.resumeHints[key] = resumeHint{seconds: seconds, at: now}
		}
		return
	}
	if seconds <= 0 {
		delete(s.resumeHints, key)
		return
	}
	s.resumeHints[key] = resumeHint{seconds: seconds, at: now}
}

func (s *Sessions) encoderStallTimeout() time.Duration {
	if s.stallTimeout > 0 {
		return s.stallTimeout
	}
	return encoderStallTimeout
}

// watchForStall kills run's process when its output position has not advanced
// for stall while at least one request is waiting on it, and returns once the
// process has exited. An encoder nobody waits on is left alone: it may simply
// be ahead of a paused viewer. The reaper classifies the kill as exitStalled.
func watchForStall(run *encoderRun, stall time.Duration) {
	ticker := time.NewTicker(max(stall/4, time.Millisecond))
	defer ticker.Stop()
	for {
		select {
		case <-run.exited:
			return
		case <-ticker.C:
			if run.waiting.Load() > 0 && time.Since(run.proc.ProgressedAt()) >= stall {
				run.stalled.Store(true)
				run.proc.Kill()
				return
			}
		}
	}
}

// reapProcess waits for run's process to exit, classifies the exit, releases
// the session's hold on it (when it is still the session's process), records
// where a truncated source ended, logs the exit once, and then closes
// run.exited so waiting requests react immediately. Server-initiated stops and
// clean end-of-input exits log at debug; a crash logs at error with its exit
// status and stderr tail.
func (s *Sessions) reapProcess(key SessionKey, session *hlsSession, run *encoderRun, rung Rung, base int) {
	started := time.Now() // before the goroutine, so no segment of this run predates it
	go func() {
		defer close(run.exited)
		defer s.releaseScratch(session.scratch)
		waitErr := run.proc.Wait()
		if run.live != nil {
			// The token lives exactly as long as the process, plus the time
			// to store the uploads it sent before exiting.
			run.live.Finish()
		}
		outcome := exitCrashed
		switch {
		case run.proc.StopRequested():
			outcome = exitStopped
		case waitErr == nil:
			outcome = exitEndOfInput
		case run.stalled.Load():
			outcome = exitStalled
		}
		// Filesystem reads stay off-lock, as with ensureRung's frontier scan.
		rungDir := filepath.Join(session.dir, rung.ID)
		frontier := session.scanFrontier(rung.ID, base)
		wroteSegment := session.segmentWrittenSince(rung.ID, base, started)
		sourceEnd := -1 // the first segment with no media, when the source ends early
		if outcome == exitEndOfInput && session.durationSeconds > 0 {
			_, _, _ = s.observeIncompleteSource(key, session, func() (time.Duration, bool) {
				last := frontier
				for last >= base && session.segmentLacksMedia(filepath.Join(rungDir, fmt.Sprintf("seg%05d.m4s", last))) {
					last--
				}
				if end := last + 1; end < session.segmentCount(rung) {
					sourceEnd = end
					return session.segmentStart(rung, end), true
				}
				return 0, false
			})
		}

		s.mu.Lock()
		result := s.transitionLocked(session, reapEncoder{
			run: run, outcome: outcome, wroteSegment: wroteSegment, sourceEnd: sourceEnd, rung: rung,
		})
		s.mu.Unlock()

		stderr := run.proc.StderrTail()
		attrs := []any{
			"media", key.MediaID,
			"session_dir", filepath.Base(session.dir),
			"rung", rung.ID,
			"base_segment", base,
			"frontier_segment", frontier,
		}
		switch outcome {
		case exitStopped:
			slog.Debug("live transcode stopped", attrs...)
		case exitEndOfInput:
			slog.Debug("live transcode reached end of input", attrs...)
		case exitStalled:
			slog.Error("live transcode stalled; killed it", append(attrs, "progressed_at", run.proc.ProgressedAt(), "stderr", stderr)...)
		default:
			status := "unknown"
			if waitErr != nil {
				status = waitErr.Error()
			}
			slog.Error("live transcode exited unexpectedly", append(attrs, "status", status, "stderr", stderr)...)
		}
		if result.lowered {
			logSourceEnd(key, session, rung, sourceEnd, stderr)
		}
		if result.failedNow {
			slog.Error("live transcode keeps crashing or stalling without output; failing the session",
				append(attrs, "failed_runs", encoderMaxFailedRuns)...)
		}
	}()
}

// scanFrontier reads the held rung directory. Callers fold the result back
// into the session's cached frontier under the manager lock.
func (h *hlsSession) scanFrontier(rung string, base int) int {
	frontier := base - 1
	entries, err := h.scratch.readRung(rung)
	if err != nil {
		return frontier
	}
	for _, entry := range entries {
		match := segmentName.FindStringSubmatch(entry.Name())
		if match == nil || !entry.Type().IsRegular() {
			continue
		}
		segment, _ := strconv.Atoi(match[1])
		if segment >= base && segment > frontier {
			frontier = segment
		}
	}
	return frontier
}

func rungSegmentSeconds(rung Rung) int {
	if rung.CopyVideo {
		return audioCopySegmentSeconds
	}
	return segmentSeconds
}

func segmentCount(durationSeconds, segmentSeconds int) int {
	return (durationSeconds + segmentSeconds - 1) / segmentSeconds
}

// primedGrid is the uniform-grid layout of a primed AAC encode: its segment
// count and the length of its last segment. The zero value means the layout
// follows the integer duration.
type primedGrid struct {
	segments       int
	tailSeconds    float64
	decodedSamples int64
	gridSamples    int64
}

// restartSegment returns the segment a primed encode asked to start at
// segment must start from so it still writes the advertised tail. An encode
// started past segment zero seeks, primes afresh and counts its frames from
// its own start, so it can end a segment short of a grid laid out from zero;
// starting it earlier writes the tail, and segments past the playlist are
// never requested.
func (g primedGrid) restartSegment(segment int) int {
	if g.segments <= 0 || g.gridSamples <= 0 || segment <= 0 || segment >= g.segments {
		return segment
	}
	for j := segment; j > 0; j-- {
		start := int64(j) * g.gridSamples
		// An encode seeked to or past the last decoded sample has no input
		// and writes no frames at all.
		if start >= g.decodedSamples {
			continue
		}
		frames := (aacEncoderDelaySamples + g.decodedSamples - start + aacFrameSamples - 1) / aacFrameSamples
		if int((start+(frames-1)*aacFrameSamples)/g.gridSamples)+1 >= g.segments {
			return j
		}
	}
	return 0
}

// primedAACGrid lays out the live encode of an audio-only source that keeps
// its priming (ffmpeg.Rendition.KeepPriming) on a grid of segmentSeconds
// (CORE-1863). The AAC encoder emits whole frames covering its priming and
// the source's decoded samples, each frame stamped from zero at the source's
// rate, and the segmenter starts a segment at the first frame at or past each
// grid line. The layout needs the same facts as the gapless trim
// (stampGapless); without them it is the zero value.
func primedAACGrid(source store.MediaFileStream, audioProfile ffmpeg.AudioProfile, segmentSeconds int) primedGrid {
	codec := normalizedAudioCodec(audioProfile.Codec)
	if source.Type != "audio" || source.DecodedSamples <= 0 || !aacSampleRates[source.SampleRate] || (codec != "aac" && codec != "") || segmentSeconds <= 0 {
		return primedGrid{}
	}
	rate := int64(source.SampleRate)
	frames := (aacEncoderDelaySamples + source.DecodedSamples + aacFrameSamples - 1) / aacFrameSamples
	gridSamples := int64(segmentSeconds) * rate
	segments := (frames-1)*aacFrameSamples/gridSamples + 1
	tailFirstFrame := ((segments-1)*gridSamples + aacFrameSamples - 1) / aacFrameSamples
	return primedGrid{
		segments:       int(segments),
		tailSeconds:    float64((frames-tailFirstFrame)*aacFrameSamples) / float64(rate),
		decodedSamples: source.DecodedSamples,
		gridSamples:    gridSamples,
	}
}

// masterPlaylist renders the multi-rendition entry point. Only rungs the
// source can back appear (rungsFor already enforced the never-upscale rule),
// and BANDWIDTH is the encoder's enforced ceiling — the advertisement is
// honest by construction. RESOLUTION is declared only when the source
// dimensions are known.
func masterPlaylist(rungs []Rung) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:7\n")
	for _, rung := range rungs {
		b.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d", rung.Bandwidth))
		if rung.Width > 0 && rung.Height > 0 {
			b.WriteString(fmt.Sprintf(",RESOLUTION=%dx%d", rung.Width, rung.Height))
		}
		b.WriteString("\n")
		b.WriteString(rung.ID + "/index.m3u8\n")
	}
	return b.String()
}

// vodPlaylist synthesizes the full VOD playlist for a known duration so the
// player has the complete seek range before a single segment exists. Version 7
// with an #EXT-X-MAP line: segments are fMP4 (CMAF), initialized by init.mp4.
func vodPlaylist(durationSeconds, segmentSeconds int, primed primedGrid) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:7\n")
	b.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", segmentSeconds))
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	b.WriteString(fmt.Sprintf("#EXT-X-MAP:URI=%q\n", initSegmentName))
	count := segmentCount(durationSeconds, segmentSeconds)
	if primed.segments > 0 {
		count = primed.segments
	}
	for i := 0; i < count; i++ {
		length := float64(segmentSeconds)
		if i == count-1 {
			if primed.segments > 0 {
				length = primed.tailSeconds
			} else if remainder := durationSeconds - segmentSeconds*(count-1); remainder > 0 {
				length = float64(remainder)
			}
		}
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n", length)
		fmt.Fprintf(&b, "seg%05d.m4s\n", i)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

// Run reaps idle sessions until ctx is done, then stops every transcode.
func (s *Sessions) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Close()
			return
		case <-ticker.C:
			s.reap()
		}
	}
}

func (s *Sessions) reap() {
	s.mu.Lock()
	var teardowns []*sessionTeardown
	cutoff := s.now().Add(-sessionIdleTimeout)
	for _, session := range s.sessions {
		if session.lastAccess.Before(cutoff) {
			teardowns = append(teardowns, s.transitionLocked(session, detachSession{cleanupOnRelease: true}).teardown)
		}
	}
	s.mu.Unlock()
	s.runTeardowns(teardowns)
	if err := s.retryScratch(false); err != nil {
		slog.Warn("live transcode directory retry failed", "error", err)
	}
}

// Close waits for admitted users and reconciliation before releasing the
// root. Concurrent callers join the same cleanup attempt and result.
func (s *Sessions) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		var teardowns []*sessionTeardown
		for _, session := range s.sessions {
			teardowns = append(teardowns, s.transitionLocked(session, detachSession{}).teardown)
		}
		s.mu.Unlock()
		s.runTeardowns(teardowns)
		s.keyframes.close()

		s.mu.Lock()
		for !s.offLockIdleLocked() {
			s.offLockIdle = make(chan struct{})
			idle := s.offLockIdle
			s.mu.Unlock()
			<-idle
			s.mu.Lock()
		}
		s.mu.Unlock()
		if s.root != nil {
			s.closeErr = errors.Join(s.closeSweepErr, s.retryScratch(true), s.root.Close(), s.rootLock.Close())
		}
	})
	return s.closeErr
}

// fileReady reports whether path exists with a non-empty body — the readiness
// condition both wait paths share (a zero-byte file is ffmpeg mid-write, not
// ready). It is the single predicate so the event-driven and fallback waits
// agree exactly.
func fileReady(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

// waitForFile blocks until path appears with a non-empty body, the deadline
// passes ("stream not ready"), or ctx is cancelled. It is event-driven: a
// per-call fsnotify watch on the parent directory wakes it the moment ffmpeg
// creates or grows the file, so it neither busy-polls nor pins a goroutine in a
// 100ms spin per pending segment. A coarse safety re-check (safetyInterval)
// backstops the rare missed/coalesced event — fsnotify does not guarantee every
// event — so liveness never depends on a single event arriving. If the watch
// cannot be armed (the directory is gone, or the OS refuses a handle), it falls
// back to the slow polling loop so a watcher failure degrades, never errors.
func waitForFile(ctx context.Context, path string) error {
	return waitForArtifact(ctx, path, nil, nil, fileReady)
}

// waitForArtifact is waitForFile that also watches the producing encoder:
// when exited closes before path is ready it returns errEncoderExited, so a
// dead encoder is noticed in milliseconds rather than at the deadline. A nil
// exited never fires.
func waitForArtifact(ctx context.Context, path string, exited, cutoff <-chan struct{}, ready func(string) bool) error {
	if ready(path) {
		return nil // fast path: already produced, no watch needed
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return pollForArtifact(ctx, path, exited, cutoff, ready)
	}
	defer watcher.Close()
	// Arm the watch on the parent dir BEFORE the post-arm stat: a file that
	// appears between arming and the stat is caught by the stat; one that appears
	// after is caught by the event. Arming after the stat would leave a window
	// where an event is missed and the file already exists — the classic
	// inotify setup race.
	if err := watcher.Add(filepath.Dir(path)); err != nil {
		return pollForArtifact(ctx, path, exited, cutoff, ready)
	}
	if ready(path) {
		return nil // appeared during arm
	}
	deadline := time.NewTimer(fileWaitTimeout)
	defer deadline.Stop()
	safety := time.NewTicker(safetyPollInterval)
	defer safety.Stop()
	settle := time.NewTimer(fileEventSettleDelay)
	if !settle.Stop() {
		<-settle.C
	}
	defer settle.Stop()
	var settleC <-chan time.Time
	for {
		select {
		case <-cutoff:
			return ErrSessionEvicted
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("stream not ready")
		case <-exited:
			if ready(path) {
				return nil // the encoder's last write landed just before it exited
			}
			return errEncoderExited
		case event := <-watcher.Events:
			if filepath.Clean(event.Name) != filepath.Clean(path) {
				continue
			}
			if ready(path) {
				return nil
			}
			// A Create notification can precede the first write, and that Write
			// may be coalesced. Schedule exactly one post-event readiness check.
			if settleC == nil {
				settle.Reset(fileEventSettleDelay)
				settleC = settle.C
			}
		case <-watcher.Errors:
			return pollForArtifact(ctx, path, exited, cutoff, ready) // watch broke; degrade to polling
		case <-settleC:
			settleC = nil
			if ready(path) {
				return nil
			}
		case <-safety.C:
			if ready(path) {
				return nil // backstop a missed/coalesced event
			}
		}
	}
}

// pollForFile is the watcher-unavailable fallback: the original slow stat poll,
// preserving waitForFile's exact contract (ready → nil, deadline → "stream not
// ready", cancel → ctx.Err()) when an fsnotify watch cannot be established.
func pollForFile(ctx context.Context, path string) error {
	return pollForArtifact(ctx, path, nil, nil, fileReady)
}

// pollForArtifact is pollForFile that also returns errEncoderExited once
// exited closes with path still not ready.
func pollForArtifact(ctx context.Context, path string, exited, cutoff <-chan struct{}, ready func(string) bool) error {
	deadline := time.Now().Add(fileWaitTimeout)
	for {
		if ready(path) {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("stream not ready")
		}
		select {
		case <-cutoff:
			return ErrSessionEvicted
		case <-ctx.Done():
			return ctx.Err()
		case <-exited:
			if ready(path) {
				return nil
			}
			return errEncoderExited
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// readerLacksMedia reports whether a complete ISO-BMFF segment contains no
// media payload. It reads only box headers and declines malformed input.
func readerLacksMedia(r io.ReaderAt, size int64) bool {
	if size == 0 {
		return false
	}
	var header [16]byte
	for off := int64(0); off < size; {
		if size-off < 8 {
			return false // trailing bytes that are not a box
		}
		if _, err := r.ReadAt(header[:8], off); err != nil {
			return false
		}
		boxSize := int64(binary.BigEndian.Uint32(header[:4]))
		headerSize := int64(8)
		switch boxSize {
		case 0: // box extends to end of file
			boxSize = size - off
		case 1: // 64-bit largesize follows the type
			if size-off < 16 {
				return false
			}
			if _, err := r.ReadAt(header[8:16], off+8); err != nil {
				return false
			}
			large := binary.BigEndian.Uint64(header[8:16])
			// #nosec G115 -- size-off is positive (loop guard), so it fits in uint64.
			if large > uint64(size-off) {
				return false
			}
			boxSize = int64(large) // #nosec G115 -- large <= size-off, an int64, so it fits.
			headerSize = 16
		}
		if boxSize < headerSize || boxSize > size-off {
			return false
		}
		if string(header[4:8]) == "mdat" && boxSize > headerSize {
			return false
		}
		off += boxSize
	}
	return true
}
