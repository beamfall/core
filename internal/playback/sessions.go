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
	session     *hlsSession
	proc        *ffmpeg.HLSProcess
	ctx         context.Context
	cancel      context.CancelFunc
	reaped      chan struct{}
	exited      chan struct{}
	outcome     encoderExit
	hasReason   bool
	stopIssued  bool
	outputOwner bool
	carry       *encoderStart
	transfer    *encoderStart
	waiting     atomic.Int32
	live        *encderiv.LiveRun
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
	cutoffCtx             context.Context
	cancelCutoff          context.CancelFunc
	admitted              bool
	library               store.LibraryID
	profile               store.ProfileID
	loadProfile           func(context.Context, store.ProfileID) (store.Profile, error)
	binders               int
	probes                map[*probeOwner]struct{}
	keyframeLoads         int
	setup                 *setupOperation
	failedSetup           *setupOperation
	writing               *encoderRun
	artifactVersion       uint64
	inspections           int
	inspectionDone        chan struct{}
	predecessorWait       []<-chan struct{}
	sourceDurationSeconds int
	key                   SessionKey
	state                 sessionState
	teardown              *sessionTeardown
	dir                   string
	scratch               *scratchPath
	joins                 map[string]chan struct{}
	src                   string
	sourceInfo            os.FileInfo
	sourceOwner           *sourceGenerationOwner
	sourceGeneration      string
	durationSeconds       int
	rungs                 []Rung
	audioProfile          ffmpeg.AudioProfile
	audioStreamIndex      *int
	sourceVideo           VideoSignal
	burnSubtitleIndex     int
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
	charged        bool
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
	rootLock      *os.File
	rootInfo      fs.FileInfo
	preparing     int
	probing       int
	allocations   map[*scratchAllocation]struct{}
	paths         map[string]*scratchPath
	removing      int
	sweeping      bool
	closeOnce     sync.Once
	closeErr      error
	closeSweepErr error
	readScratch   func(fs.FS, string) ([]fs.DirEntry, error)
	mkdirDir      func(string, fs.FileMode) error
	statSource    func(string) (os.FileInfo, error)
	lstatScratch  func(string) (fs.FileInfo, error)
	join          func(*os.File, func(string) (*os.File, error), []string) error

	// Admission and operations share the session transition lock.
	admissionClock DrainFence
	admission      map[store.LibraryID]*libraryAdmission
	libraryOps     map[*libraryOperation]struct{}
	libraryChanged chan struct{}

	writeSetupPlaylist func(*hlsSession, string, []byte) error
	// beforeSetupCommit lets tests pause after writing the unpublished playlists.
	beforeSetupCommit func()
	// durationProbes caches on-demand runtime probes by source path and generation.
	durationProbes    map[string]durationProbeEntry
	durationRetained  map[durationKey]*retainedDuration
	durationFlights   map[string]*durationFlight
	incompleteSources incompleteSourceCache
	sourceOwners      map[sourceOwnerKey]*sourceGenerationOwner
	sourceClaimOrder  uint64
	sourceStore       sourceEndObserver
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
	// windowExpired, when set, runs each time a wait window on a segment
	// expires, before the request checks the encoder's progress. Tests set it
	// to write a segment inside the window instead of racing a timer.
	windowExpired func()
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
	// charges includes all capacity owners through reap, even detached ones.
	charges       map[*hlsSession]struct{}
	revocations   map[*hlsSession]*sessionTeardown
	continuations map[*hlsSession]struct{}
	// starting counts ensureRung starts running with mu released.
	starting int
	// detached counts sessionTeardowns handed out under mu that runTeardowns
	// has not finished, including ones that only remove a directory. Close
	// waits for starting and detached to reach zero so no encoder,
	// lease or directory outlives it.
	detached int
	// offLockIdle is closed when all admitted off-lock work finishes.
	offLockIdle chan struct{}
	// closed is set by Close; once set no session is created.
	closed bool

	// keyframes holds audio-copy keyframe timelines under mu.
	keyframes keyframeCache
}

// sessionTeardown is the slow half of removing a session: stopping its FFmpeg
// process (which waits for the exit) and deleting its working directory. A
// caller collects it under s.mu and runs it after releasing the lock, so one
// slow teardown never stalls requests for every other session. Whoever builds
// one under mu adds it to s.detached; runTeardowns takes it back out.
type sessionTeardown struct {
	libraryDrain     bool
	session          *hlsSession
	run              *encoderRun
	preparing        *encoderRun
	liveRun          *encderiv.LiveRun
	preparingLive    *encderiv.LiveRun
	live             *encderiv.LiveSession
	cleanupOnRelease bool
	cutoffDone       chan struct{}
	probeReaps       []<-chan struct{}
	spawnDone        <-chan struct{}
	lateChildReap    <-chan struct{} // Published and read under Sessions.mu.
	stopChild        bool
}

func (s *Sessions) runTeardowns(teardowns []*sessionTeardown) {
	s.signalTeardowns(teardowns)
	// A detached session must finish independently of other libraries in
	// the batch, even when this caller waits for the whole batch.
	var pending sync.WaitGroup
	for _, t := range teardowns {
		pending.Go(func() { s.joinTeardown(t) })
	}
	pending.Wait()
}

func (s *Sessions) signalTeardowns(teardowns []*sessionTeardown) {
	for _, t := range teardowns {
		if t.session.cancelCutoff != nil {
			t.session.cancelCutoff()
		}
		if t.liveRun != nil {
			t.liveRun.Abort()
		}
		if t.preparingLive != nil {
			t.preparingLive.Abort()
		}
		if t.preparing != nil {
			t.preparing.cancel()
		}
		if t.stopChild && s.stopEncoder == nil {
			t.run.proc.RequestStop()
		}
		close(t.cutoffDone)
	}
}

func (s *Sessions) joinTeardown(t *sessionTeardown) {
	for _, probe := range t.probeReaps {
		<-probe
	}
	if t.run != nil {
		if t.stopChild && s.stopEncoder != nil {
			s.stop(t.run.proc)
		}
		<-t.run.reaped
	}
	p := t.session.scratch
	if t.cleanupOnRelease && p != nil {
		s.mu.Lock()
		if p.users > 0 {
			p.cleanup = t
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
	}
	s.finishTeardown(t)
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
	if s.libraryChanged != nil {
		close(s.libraryChanged)
		s.libraryChanged = nil
	}
	if s.offLockIdle != nil && s.offLockIdleLocked() {
		close(s.offLockIdle)
		s.offLockIdle = nil
	}
}

// offLockIdleLocked reports whether the manager has joined its owned work. Callers hold mu.
func (s *Sessions) offLockIdleLocked() bool {
	if len(s.libraryOps) != 0 || len(s.keyframes.inflight) != 0 || s.preparing != 0 || s.probing != 0 || s.starting != 0 || s.detached != 0 || s.removing != 0 || s.sweeping {
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
	return session.charged
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
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if session, ok := s.sessions[key]; ok && session.holdsTranscodeLocked() {
		return true
	}
	if s.liveCount() < s.cap {
		return true
	}
	return s.liveCount() <= s.cap && s.preemptableLocked(key, priority, now) != nil
}

// liveCount counts sessions holding a live FFmpeg process, plus removed
// sessions whose encoder has not stopped yet. Callers hold mu.
func (s *Sessions) liveCount() int {
	return len(s.charges)
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
func (s *Sessions) preemptableLocked(key SessionKey, priority Priority, now time.Time) *hlsSession {
	cutoff := now.Add(-preemptIdleAfter)
	var victim *hlsSession
	var oldest time.Time
	for other, session := range s.sessions {
		if other == key || session.proc == nil || !session.charged {
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
	return s.terminate(func(key SessionKey) bool { return key.SessionID == sessionID })
}

// TerminatePlayback aborts the in-flight transcodes one auth Session holds for
// one Media and returns how many it stopped (CORE-2345). A player that closes
// calls endPlayback, and without this its FFmpeg process kept running until
// idle reap. It stops every profile-capability variant of that (SessionID,
// MediaID) pair through the same detachment and teardown path as
// TerminateSession, and leaves the Session's other Media alone. Ending a
// playback with no live transcode (direct play) is a zero-stop no-op.
func (s *Sessions) TerminatePlayback(sessionID store.SessionID, mediaID store.MediaID) int {
	stopped, wait := s.DetachPlayback(sessionID, mediaID)
	wait()
	return stopped
}

// DetachPlayback is TerminatePlayback split at its blocking point: it detaches
// the matching transcodes, so no later request reaches them, and returns how
// many it stopped with a wait that signals and joins their teardown. A caller
// holding a lock that other requests need detaches under it and waits after
// releasing it (CORE-2923).
func (s *Sessions) DetachPlayback(sessionID store.SessionID, mediaID store.MediaID) (int, func()) {
	return s.detach(func(key SessionKey) bool { return key.SessionID == sessionID && key.MediaID == mediaID })
}

func (s *Sessions) terminate(matches func(SessionKey) bool) int {
	stopped, wait := s.detach(matches)
	wait()
	return stopped
}

func (s *Sessions) detach(matches func(SessionKey) bool) (int, func()) {
	s.mu.Lock()
	revoked := 0
	for allocation := range s.allocations {
		if matches(allocation.key) {
			allocation.revoked = true
		}
	}
	var joins []<-chan struct{}
	for h, t := range s.revocations {
		if matches(h.key) {
			joins = append(joins, t.cutoffDone)
			joins = append(joins, t.probeReaps...)
			if t.run != nil {
				joins = append(joins, t.run.reaped)
			}
			if t.lateChildReap != nil {
				joins = append(joins, t.lateChildReap)
			}
		}
	}
	var teardowns []*sessionTeardown
	for key, session := range s.sessions {
		if matches(key) && session.state < sessionRevoking {
			if session.admitted || session.state == sessionRunning {
				revoked++
			}
			teardowns = append(teardowns, s.transitionLocked(session, detachSession{cleanupOnRelease: true}).teardown)
		}
	}
	for session := range s.continuations {
		if matches(session.key) {
			if session.admitted {
				revoked++
			}
			teardowns = append(teardowns, s.transitionLocked(session, detachSession{cleanupOnRelease: true}).teardown)
		}
	}
	s.mu.Unlock()
	return revoked, func() {
		s.runTeardowns(teardowns)
		for _, done := range joins {
			<-done
		}
	}
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

// LiveArtifactShaped selects and holds one live artifact. Its caller releases
// it after closing any reader opened from it.
func (s *Sessions) LiveArtifactShaped(ctx context.Context, key SessionKey, src string, durationSeconds, videoWidth, videoHeight int, audioCopy, degraded bool, caps ladderCaps, sourceVideo VideoSignal, burnSubtitleIndex int, audioProfile ffmpeg.AudioProfile, audioStreamIndex *int, shape StreamShape, priority Priority, name string) (*LiveArtifact, error) {
	binding, err := s.BindLive(key)
	if err != nil {
		return nil, err
	}
	defer binding.Release()
	library := shape.LibraryID
	if shape.Encrypted != nil {
		library = store.LibraryID(shape.Encrypted.LibraryID)
	}
	if err := binding.Attach(library, "", nil); err != nil {
		return nil, err
	}
	return binding.ArtifactShaped(ctx, src, durationSeconds, videoWidth, videoHeight, audioCopy, degraded, caps, sourceVideo, burnSubtitleIndex, audioProfile, audioStreamIndex, shape, priority, name)
}

// ArtifactShaped resolves an artifact using this exact verified binding.
func (b *LiveBinding) ArtifactShaped(ctx context.Context, src string, durationSeconds, videoWidth, videoHeight int, audioCopy, degraded bool, caps ladderCaps, sourceVideo VideoSignal, burnSubtitleIndex int, audioProfile ffmpeg.AudioProfile, audioStreamIndex *int, shape StreamShape, priority Priority, name string) (*LiveArtifact, error) {
	s := b.manager
	session, err := b.prepare(ctx, src, durationSeconds, videoWidth, videoHeight, audioCopy, degraded, caps, sourceVideo, burnSubtitleIndex, audioProfile, audioStreamIndex, shape, priority)
	if err != nil {
		return nil, err
	}
	rung, file, _ := strings.Cut(name, "/")
	artifact := &LiveArtifact{
		manager: s, session: session, ctx: ctx, name: name,
		trackRun: file != "" && (file != "index.m3u8" || session.durationSeconds == 0),
	}
	if artifact.trackRun {
		artifact.budget = s.transcodeLatencyBudget(session, filepath.Join(session.dir, name))
		artifact.deadline = time.Now().Add(artifact.budget)
		if file != "index.m3u8" {
			artifact.delivery = rung
		}
	}
	resolved, err := artifact.resolve(false)
	if err != nil {
		artifact.Release()
		return nil, err
	}
	artifact.path, artifact.version = resolved.path, resolved.version
	return artifact, nil
}

func (b *LiveBinding) prepare(ctx context.Context, src string, durationSeconds, videoWidth, videoHeight int, audioCopy, degraded bool, caps ladderCaps, sourceVideo VideoSignal, burnSubtitleIndex int, audioProfile ffmpeg.AudioProfile, audioStreamIndex *int, shape StreamShape, priority Priority) (prepared *hlsSession, result error) {
	s := b.manager
	release, err := b.HoldSource(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if shape.Encrypted != nil && shape.Encrypted.Fingerprint == "" {
		return nil, ErrEncryptedLiveUnavailable
	}
	source := s.claimSource(src, shape.Encrypted)
	defer s.releaseSource(source)
	inputs := setupInputs{
		videoWidth: videoWidth, videoHeight: videoHeight,
		duration: durationSeconds, burnSubtitle: burnSubtitleIndex,
		audioCopy: audioCopy, degraded: degraded, composed: shape.ComposedPicture, audioOnly: shape.AudioOnly,
		caps: caps, video: sourceVideo, audio: audioProfile, stream: audioStreamIndex, adjust: shape.Adjust,
	}
	claim, err := b.claimSetup(ctx, source, inputs)
	if err != nil {
		return nil, err
	}
	owner := b.session
	defer func() { b.finishSetup(owner, claim, result) }()
	workCtx, cancel := b.Context(context.WithoutCancel(ctx))
	defer cancel()
	defer func() {
		if err := b.Check(); err != nil {
			if prepared != nil {
				s.releaseScratch(prepared.scratch)
				prepared = nil
			}
			result = err
		}
	}()
	if shape.Encrypted != nil && shape.ComposedPicture {
		return nil, ErrEncryptedLiveUnavailable
	}
	if b.duration != nil && !source.matches(b.duration.source) {
		return nil, errSetupUnavailable
	}
	if shape.Encrypted == nil && (source.generation == "" || !source.info.Mode().IsRegular()) {
		return nil, errSetupUnavailable
	}
	picture, err := s.livePicture(workCtx, b.session, source, shape)
	if err != nil {
		return nil, err
	}
	if err := b.publishSetupPicture(claim, picture); err != nil {
		return nil, err
	}
	if shape.AudioOnly {
		audioCopy = false
	}
	for {
		s.mu.Lock()
		if b.duration != nil && b.duration.seconds > 0 {
			durationSeconds = b.duration.seconds
		} else if owner.state == sessionRunning && durationSeconds <= 0 && source.matches(owner.sourceCapture()) {
			durationSeconds = owner.sourceDurationSeconds
		}
		s.mu.Unlock()
		var timeline *keyframeTimeline
		if audioCopy && durationSeconds > 0 && picture == nil && shape.Adjust.IsZero() && shape.Encrypted == nil {
			timeline, err = s.boundVideoTimeline(workCtx, b, source)
			if err != nil {
				return nil, err
			}
		}
		if !source.matches(captureSource(src, shape.Encrypted, s.statSource)) {
			return nil, errSetupUnavailable
		}
		audioOnlyPeak := audioOnlyBandwidth(audioProfile, shape.AudioSource, !shape.Adjust.IsZero())
		prepared, err := s.session(ctx, b, source, durationSeconds, timeline, videoWidth, videoHeight, audioCopy, degraded, caps, sourceVideo, burnSubtitleIndex, audioProfile, audioStreamIndex, claim.picture, shape.Adjust, shape.AudioOnly, shape.AudioSource, audioOnlyPeak, shape.Encrypted, b.library, priority)
		if !errors.Is(err, errDurationChanged) {
			return prepared, err
		}
	}
}

func (s *Sessions) resolveArtifactPath(ctx context.Context, key SessionKey, session *hlsSession, name string, budget time.Duration) (artifactObservation, error) {
	var err error
	if name == masterPlaylistName {
		return artifactObservation{path: filepath.Join(session.dir, masterPlaylistName)}, nil
	}
	rungID, file, ok := strings.Cut(name, "/")
	if !ok || !rungFileName.MatchString(file) {
		return artifactObservation{}, errors.New("unknown stream file")
	}
	rung, ok := session.rung(rungID)
	if !ok {
		return artifactObservation{}, errors.New("unknown stream file")
	}
	path := filepath.Join(session.dir, rungID, file)

	segment := 0
	var run *encoderRun
	switch {
	case file == "index.m3u8":
		if session.durationSeconds > 0 {
			return artifactObservation{path: path}, nil // synthesized at session creation
		}
		// Linear mode: ffmpeg owns the variant playlist, so it must run.
		if run, err = s.ensureRung(ctx, key, session, rung, 0, path); err != nil {
			return artifactObservation{}, err
		}
	case file == initSegmentName:
		segment = initRequest
		if run, err = s.ensureRung(ctx, key, session, rung, segment, path); err != nil {
			return artifactObservation{}, err
		}
	default:
		match := segmentName.FindStringSubmatch(file)
		segment, _ = strconv.Atoi(match[1])
		if session.durationSeconds > 0 && segment >= session.playlistSegmentCount(rung) {
			return artifactObservation{}, errors.New("unknown stream file")
		}
		return s.playlistSegment(ctx, key, session, rung, segment, budget)
	}
	return s.waitWhileEncoderProgresses(ctx, key, session, rung, segment, false, path, run, budget)
}

// playlistSegment resolves the media segment rung's playlist lists at index
// segment. On the uniform grid that is the encoder's own file. On a keyframe
// timeline a segment that merges several GOPs (CORE-1843) is joined from their
// files once and kept beside them; when a truncated source ends inside it, the
// segment carries the media before the end.
func (s *Sessions) playlistSegment(ctx context.Context, key SessionKey, session *hlsSession, rung Rung, segment int, budget time.Duration) (artifactObservation, error) {
	first, end := session.encoderSegments(rung, segment)
	if end-first == 1 {
		return s.encoderSegment(ctx, key, session, rung, first, budget)
	}
	joined := filepath.Join(session.dir, rung.ID, fmt.Sprintf("joined%05d.m4s", segment))
	if artifact, ready := s.readyArtifact(session, joined); ready {
		return artifact, nil
	}
	parts := make([]string, 0, end-first)
	var firstArtifact artifactObservation
	for part := first; part < end; part++ {
		artifact, err := s.encoderSegment(ctx, key, session, rung, part, budget)
		if errors.Is(err, ErrSourceEnded) && len(parts) > 0 {
			break
		}
		if err != nil {
			return artifactObservation{}, err
		}
		if len(parts) == 0 {
			firstArtifact = artifact
		} else if artifact.version != firstArtifact.version {
			return artifactObservation{}, ErrArtifactChanged
		}
		parts = append(parts, artifact.path)
	}
	if len(parts) == 1 {
		return firstArtifact, nil
	}
	if err := s.joinArtifact(ctx, session, joined, parts, firstArtifact.version); err != nil {
		return artifactObservation{}, err
	}
	firstArtifact.path = joined
	return firstArtifact, nil
}

// encoderSegment waits for encoder segment file segment of rung, starting or
// moving the encoder when the running one will not reach it.
func (s *Sessions) encoderSegment(ctx context.Context, key SessionKey, session *hlsSession, rung Rung, segment int, budget time.Duration) (artifactObservation, error) {
	path := filepath.Join(session.dir, rung.ID, fmt.Sprintf("seg%05d.m4s", segment))
	if s.beyondSourceEnd(session, rung, segment) {
		return artifactObservation{}, ErrSourceEnded
	}
	run, err := s.ensureRung(ctx, key, session, rung, segment, path)
	if err != nil {
		return artifactObservation{}, err
	}
	artifact, err := s.waitWhileEncoderProgresses(ctx, key, session, rung, segment, true, path, run, budget)
	if err != nil {
		return artifactObservation{}, err
	}
	observeCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	_, observed, err := s.observeIncompleteSource(observeCtx, key, session, artifact, false, func() (time.Duration, bool) {
		empty, _ := session.inspectSegment(path)
		return session.segmentStart(rung, segment), empty
	})
	if err != nil {
		return artifactObservation{}, err
	}
	if observed {
		if err := s.noteSourceEnd(key, session, artifact, rung, segment, ""); err != nil {
			return artifactObservation{}, err
		}
		return artifactObservation{}, ErrSourceEnded
	}
	return artifact, nil
}

// waitForEncoder waits for path while watching the encoder run that should
// produce it. An encoder that crashes under the wait is restarted at the
// requested segment inside this request (at most maxRequestEncoderRestarts
// times, and within the session's crash-restart budget); an encoder that hit
// end of input fails the request at once instead of letting it wait out its
// budget; an encoder the server stopped (switch, seek, teardown) leaves the
// request waiting for its replacement as before.
func (s *Sessions) waitForEncoder(ctx context.Context, key SessionKey, session *hlsSession, rung Rung, segment int, isSegment bool, path string, run *encoderRun) (artifactObservation, error) {
	restarts := 0
	for {
		if run != nil {
			run.waiting.Add(1)
		}
		var artifact artifactObservation
		err := waitForArtifact(ctx, path, run.exitedChan(), session.scratch.cutoff, func(path string) bool {
			var ready bool
			artifact, ready = s.readyArtifact(session, path)
			return ready
		})
		if run != nil {
			run.waiting.Add(-1)
		}
		if !errors.Is(err, errEncoderExited) {
			return artifact, err
		}
		s.mu.Lock()
		live := session.state == sessionRunning
		stale := session.run != run || session.writing != nil && session.writing != run
		s.mu.Unlock()
		if !live {
			return artifactObservation{}, ErrSessionEvicted
		}
		if stale && run.outcome != exitStopped {
			return artifactObservation{}, ErrArtifactChanged
		}
		switch run.outcome {
		case exitStopped:
			run = nil // the replacement owns any future output on this path
		case exitEndOfInput:
			if isSegment && s.beyondSourceEnd(session, rung, segment) {
				return artifactObservation{}, ErrSourceEnded
			}
			return artifactObservation{}, errEncoderEnded
		default:
			if restarts >= maxRequestEncoderRestarts {
				return artifactObservation{}, errEncoderFailing
			}
			restarts++
			if run, err = s.ensureRung(ctx, key, session, rung, segment, path); err != nil {
				return artifactObservation{}, err
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
func (s *Sessions) waitWhileEncoderProgresses(ctx context.Context, key SessionKey, session *hlsSession, rung Rung, segment int, isSegment bool, path string, run *encoderRun, budget time.Duration) (artifactObservation, error) {
	for {
		windowStart := time.Now()
		current, _, tracked := s.rungEncoder(session, rung)
		waitCtx, cancel := context.WithTimeout(ctx, budget)
		artifact, err := s.waitForEncoder(waitCtx, key, session, rung, segment, isSegment, path, run)
		cancel()
		if err == nil || !isSegment || ctx.Err() != nil || !errors.Is(err, context.DeadlineExceeded) {
			return artifact, err
		}
		if s.windowExpired != nil {
			s.windowExpired()
		}
		after, base, stillTracked := s.rungEncoder(session, rung)
		if !tracked || !stillTracked || current != run || after != run || base > segment ||
			!session.segmentWrittenSince(rung.ID, base, windowStart) {
			return artifactObservation{}, err
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
	entries, err := h.readRung(rung)
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
		f, info, err := h.openArtifact(filepath.Join(h.dir, rung, entry.Name()))
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
func (s *Sessions) noteSourceEnd(key SessionKey, session *hlsSession, artifact artifactObservation, rung Rung, segment int, stderr string) error {
	s.mu.Lock()
	if session.state != sessionRunning {
		s.mu.Unlock()
		return ErrSessionEvicted
	}
	if session.run != artifact.run || session.artifactVersion != artifact.version {
		s.mu.Unlock()
		return ErrArtifactChanged
	}
	if !session.currentSourceLocked() {
		s.mu.Unlock()
		return errSetupUnavailable
	}
	result := s.transitionLocked(session, recordSourceEnd{rung: rung, segment: segment})
	s.mu.Unlock()
	if result.lowered {
		logSourceEnd(key, session, rung, segment, stderr)
	}
	return nil
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
func (s *Sessions) session(ctx context.Context, binding *LiveBinding, source sourceCapture, durationSeconds int, timeline *keyframeTimeline, videoWidth, videoHeight int, audioCopy, degraded bool, caps ladderCaps, sourceVideo VideoSignal, burnSubtitleIndex int, audioProfile ffmpeg.AudioProfile, audioStreamIndex *int, picture *ComposedPicture, adjust ffmpeg.AudioAdjust, audioOnly bool, audioSourceStream store.MediaFileStream, audioOnlyPeak int, secure *EncryptedSource, library store.LibraryID, priority Priority) (*hlsSession, error) {
	key, src := binding.session.key, source.path
	sourceInfo, sourceGeneration := source.info, source.generation
	var watchSession *hlsSession
	var watchDone <-chan struct{}
	var discard, predecessor *sessionTeardown
	s.mu.Lock()
	defer func() {
		s.mu.Unlock()
		if discard != nil {
			s.runTeardowns([]*sessionTeardown{discard})
		}
		if predecessor != nil {
			s.signalTeardowns([]*sessionTeardown{predecessor})
			go s.joinTeardown(predecessor)
		}
		if watchDone != nil {
			go s.watchLiveRevocation(watchSession, watchDone, secure.Revoked, s.liveRevocationInterval())
		}
	}()
	if !binding.validLocked() {
		return nil, ErrSessionEvicted
	}
	// One encode policy decides both the ladder and the encoder (CORE-2452):
	// an existing session's pinned snapshot, which a reshaped replacement
	// inherits, so a Console save in either direction never changes the rungs
	// of a session already playing; otherwise the current policy, read once.
	existing := binding.session
	if existing.state != sessionRunning {
		existing = nil
	}

	s.mu.Unlock()
	policy, hevcAllowed := s.sessionEncodePolicy(existing)
	now := s.now()
	s.mu.Lock()
	if !binding.validLocked() {
		return nil, ErrSessionEvicted
	}
	if binding.duration != nil && binding.duration.seconds > 0 && binding.duration.seconds != durationSeconds {
		return nil, errDurationChanged
	}
	if source.supersededLocked() {
		return nil, errSetupUnavailable
	}
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
	sourceDuration := durationSeconds
	durationSeconds = adjust.OutputSeconds(sourceDuration)
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
	if existing != nil && existing.scratch.library == library &&
		existing.sourceGeneration == sourceGeneration && (existing.sourceInfo == nil || sameSourceFile(existing.sourceInfo, sourceInfo)) &&
		existing.src == src && existing.durationSeconds == durationSeconds && existing.primed == primed &&
		existing.timeline == timeline && existing.degraded == degraded && rungsEqual(existing.rungs, requestedRungs) &&
		existing.sourceVideo == sourceVideo && existing.burnSubtitleIndex == burnSubtitleIndex &&
		audioProfilesEqual(existing.audioProfile, audioProfile) && equalOptionalInt(existing.audioStreamIndex, audioStreamIndex) &&
		(existing.composed == nil) == (picture == nil) && existing.adjust == adjust && (existing.secure == nil) == (secure == nil) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// A resume hint noted while this encoder ran applies only to a replacement.
		s.transitionLocked(existing, reuseSession{priority: priority, at: now})
		source.publishLocked()
		existing.scratch.users++
		return existing, nil
	}
	allocation := &scratchAllocation{key: key, library: library}
	if s.allocations == nil {
		s.allocations = make(map[*scratchAllocation]struct{})
	}
	s.allocations[allocation] = struct{}{}
	defer func() { delete(s.allocations, allocation) }()
	scratch, err := s.allocateScratchLocked(allocation, library)
	if err != nil {
		return nil, err
	}
	s.mu.Unlock()
	dir := filepath.Join(s.dir, scratch.name)
	session := &hlsSession{
		key:                   key,
		dir:                   dir,
		scratch:               scratch,
		src:                   src,
		sourceInfo:            sourceInfo,
		sourceGeneration:      sourceGeneration,
		sourceDurationSeconds: sourceDuration,
		durationSeconds:       durationSeconds,
		timeline:              timeline,
		rungs:                 requestedRungs,
		audioProfile:          audioProfile,
		audioStreamIndex:      audioStreamIndex,
		sourceVideo:           sourceVideo,
		burnSubtitleIndex:     burnSubtitleIndex,
		audioSource:           audioSource,
		primed:                primed,
		priority:              priority,
		degraded:              degraded,
		adjust:                adjust,
		secure:                secure,
		encodePolicy:          policy,
	}
	if secure != nil {
		session.live, err = encderiv.NewLiveSession(dir, secure.LibraryID)
	}
	if err == nil {
		write := session.writePlaylist
		if s.writeSetupPlaylist != nil {
			write = func(rel string, data []byte) error { return s.writeSetupPlaylist(session, rel, data) }
		}
		err = writeSessionPlaylists(dir, requestedRungs, durationSeconds, timeline, primed, write)
	}
	var preparedPicture *ffmpeg.ComposedPicture
	if err == nil && picture != nil {
		preparedPicture, err = writeLivePicture(dir, *picture)
	}
	if s.beforeSetupCommit != nil {
		s.beforeSetupCommit()
	}
	current := captureSource(src, secure, s.statSource)
	now = s.now()
	s.mu.Lock()
	// The setup claim excludes other publishers for this instance. Termination can still revoke this claim
	// while its directory is being prepared.
	if !binding.validLocked() || allocation.revoked {
		err = ErrSessionEvicted
	} else if canceled := ctx.Err(); canceled != nil {
		err = canceled
	} else if !source.matches(current) || source.supersededLocked() {
		err = errSetupUnavailable
	} else if binding.duration != nil && binding.duration.seconds > 0 && binding.duration.seconds != sourceDuration {
		err = errDurationChanged
	}
	if err != nil {
		scratch.users--
		discard = s.transitionLocked(session, detachSession{}).teardown
		if !errors.Is(err, errDurationChanged) &&
			!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			binding.recordSetupLocked(binding.session, binding.session.setup, err)
		}
		return nil, err
	}
	previous := binding.session
	if existing != nil {
		t := s.transitionLocked(existing, detachSession{cleanupOnRelease: true}).teardown
		predecessor = t
		for _, done := range existing.predecessorWait {
			select {
			case <-done:
			default:
				session.predecessorWait = append(session.predecessorWait, done)
			}
		}
		if t.run != nil {
			session.predecessorWait = append(session.predecessorWait, t.run.reaped)
		}
		if t.spawnDone != nil {
			session.predecessorWait = append(session.predecessorWait, t.spawnDone)
		}
	}
	// Promotion transfers only the publishing binder. Waiters on an initial
	// reservation share that same instance, including its cutoff.
	if existing == nil {
		session = promoteReservation(previous, session)
	} else {
		previous.binders--
		session.binders = 1
		binding.session = session
		session.cutoffCtx, session.cancelCutoff = context.WithCancel(context.Background())
	}
	session.admitted, session.profile, session.library = true, binding.profile, library
	session.loadProfile = binding.loadProfile
	if sourceGeneration != "" {
		source.owner.users++
		session.sourceOwner = source.owner
		source.publishLocked()
	}
	session.lastAccess = now
	if hint, ok := s.resumeHints[key]; ok {
		delete(s.resumeHints, key)
		if session.lastAccess.Sub(hint.at) < sessionIdleTimeout {
			session.resumeSeconds = hint.seconds
		}
	}
	s.transitionLocked(session, publishSession{composed: picture, picture: preparedPicture})
	scratch.allocating = false
	if session.revokeDone != nil {
		watchSession, watchDone = session, session.revokeDone
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
func writeSessionPlaylists(dir string, rungs []Rung, durationSeconds int, timeline *keyframeTimeline, primed primedGrid, write func(rel string, data []byte) error) error {
	if err := write(masterPlaylistName, []byte(masterPlaylist(rungs))); err != nil {
		return err
	}
	for _, rung := range rungs {
		if err := os.Mkdir(filepath.Join(dir, rung.ID), 0o700); err != nil {
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
func (s *Sessions) ensureRung(ctx context.Context, key SessionKey, session *hlsSession, rung Rung, requested int, path string) (*encoderRun, error) {
	for {
		select {
		case <-session.scratch.cutoff:
			return nil, ErrSessionEvicted
		default:
		}
		ready := session.artifactReady(path)
		s.mu.Lock()
		if session.state != sessionRunning {
			s.mu.Unlock()
			return nil, ErrSessionEvicted
		}
		if ready {
			s.mu.Unlock()
			return nil, nil
		}
		if err := ctx.Err(); err != nil {
			s.mu.Unlock()
			return nil, err
		}
		if pending := session.start; pending != nil {
			s.mu.Unlock()
			if err := waitEncoderOwner(ctx, session, pending.done); err != nil {
				return nil, err
			}
			s.mu.Lock()
			live := session.state == sessionRunning
			s.mu.Unlock()
			if !live {
				return nil, ErrSessionEvicted
			}
			if pending.err != nil && !errors.Is(pending.err, context.Canceled) && !errors.Is(pending.err, context.DeadlineExceeded) {
				return nil, pending.err
			}
			continue
		}
		segment := requested
		if requested == initRequest {
			segment = session.resumeSegmentLocked(rung)
		}
		old := session.run
		if session.proc != nil && session.currentRung == rung.ID {
			if requested == initRequest || session.durationSeconds == 0 ||
				segment >= session.baseSegment && segment <= session.frontier+seekLookaheadSegments {
				s.mu.Unlock()
				return old, nil
			}
			base := session.baseSegment
			s.mu.Unlock()
			frontier, scanned := session.scanFrontier(rung.ID, base)
			ready = session.artifactReady(path)
			s.mu.Lock()
			if session.state != sessionRunning {
				s.mu.Unlock()
				return nil, ErrSessionEvicted
			}
			if ready {
				s.mu.Unlock()
				return nil, nil
			}
			if session.start != nil || session.run != old {
				s.mu.Unlock()
				continue
			}
			if scanned && s.transitionLocked(session, advanceFrontier{run: old, rungID: rung.ID, base: base, scanned: frontier}).accepted &&
				segment >= base && segment <= session.frontier+seekLookaheadSegments {
				s.mu.Unlock()
				return old, nil
			}
		}
		runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		run := &encoderRun{session: session, ctx: runCtx, cancel: cancel, reaped: make(chan struct{}), exited: make(chan struct{})}
		start := &encoderStart{ctx: ctx, session: session, run: run, done: make(chan struct{})}
		session.start = start
		s.starting++
		stopOld := session.proc != nil
		if stopOld {
			s.acceptEndpointFailureLocked(old)
			old.acceptReasonLocked(exitStopped)
			old.carry = start
			old.stopIssued = true
		}
		s.mu.Unlock()
		launched, err := s.startEncoder(ctx, key, start, old, stopOld, rung, segment, requested != initRequest)
		s.finishEncoderStart(start, err)
		return launched, err
	}
}

func (s *Sessions) launchEncoder(ctx context.Context, start *encoderStart, rung Rung, segment int) (*ffmpeg.HLSProcess, error) {
	session := start.session
	rungDir := filepath.Join(session.dir, rung.ID)
	if session.secure != nil {
		return s.startEncryptedRung(ctx, start, rung, segment)
	}
	if timeline := session.keyframeTimeline(rung); timeline != nil && session.durationSeconds > 0 {
		return s.transcoder.(keyframeTranscoder).StartHLSKeyframeSegments(ctx, session.src, rungDir, segment, timeline.segmentStart(segment), session.rendition(rung))
	}
	if session.durationSeconds > 0 {
		return s.transcoder.StartHLSSegments(ctx, session.src, rungDir, segment, rungSegmentSeconds(rung), session.rendition(rung))
	}
	return s.transcoder.StartHLS(ctx, session.src, rungDir, session.rendition(rung))
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
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
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
func (s *Sessions) watchForStall(run *encoderRun, stall time.Duration) {
	ticker := time.NewTicker(max(stall/4, time.Millisecond))
	defer ticker.Stop()
	for {
		select {
		case <-run.reaped:
			return
		case <-ticker.C:
			if run.waiting.Load() > 0 && time.Since(run.proc.ProgressedAt()) >= stall {
				s.mu.Lock()
				active := run.session.proc == run.proc && run.session.run == run
				if active {
					run.acceptReasonLocked(exitStalled)
				}
				s.mu.Unlock()
				if active {
					run.proc.Kill()
				}
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
		defer s.releaseScratch(session.scratch)
		defer run.cancel()
		waitErr := run.proc.Wait()
		s.mu.Lock()
		s.acceptEndpointFailureLocked(run)
		s.childReapedLocked(run)
		s.mu.Unlock()
		if run.live != nil {
			run.live.Finish()
		}
		s.mu.Lock()
		s.acceptEndpointFailureLocked(run)
		outcome := exitCrashed
		switch {
		case run.hasReason:
			outcome = run.outcome
		case run.proc.StopRequested():
			outcome = exitStopped
		case waitErr == nil:
			outcome = exitEndOfInput
		}
		run.acceptReasonLocked(outcome)
		s.mu.Unlock()
		// Filesystem reads stay off-lock, as with ensureRung's frontier scan.
		rungDir := filepath.Join(session.dir, rung.ID)
		frontier, scanned := session.scanFrontier(rung.ID, base)
		wroteSegment := session.segmentWrittenSince(rung.ID, base, started)
		sourceEnd := -1 // the first segment with no media, when the source ends early
		if outcome == exitEndOfInput && scanned && session.durationSeconds > 0 {
			observeCtx, cancel := context.WithTimeout(run.ctx, 5*time.Second)
			_, observed, _ := s.observeIncompleteSource(observeCtx, key, session, artifactObservation{run: run}, true, func() (time.Duration, bool) {
				last := frontier
				for last >= base {
					empty, readable := session.inspectSegment(filepath.Join(rungDir, fmt.Sprintf("seg%05d.m4s", last)))
					if !readable {
						return 0, false
					}
					if !empty {
						break
					}
					last--
				}
				if end := last + 1; end < session.segmentCount(rung) {
					sourceEnd = end
					return session.segmentStart(rung, end), true
				}
				return 0, false
			})
			cancel()
			if !observed {
				sourceEnd = -1
			}
		}

		s.mu.Lock()
		result := s.transitionLocked(session, reapEncoder{
			run: run, outcome: outcome, wroteSegment: wroteSegment, sourceEnd: sourceEnd, rung: rung,
		})
		if session.writing == run {
			session.writing = nil
		}
		close(run.exited)
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
func (h *hlsSession) scanFrontier(rung string, base int) (int, bool) {
	frontier := base - 1
	entries, err := h.readRung(rung)
	if err != nil {
		return frontier, false
	}
	for _, entry := range entries {
		match := segmentName.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		if !entry.Type().IsRegular() {
			return base - 1, false
		}
		segment, _ := strconv.Atoi(match[1])
		if segment >= base && segment > frontier {
			frontier = segment
		}
	}
	return frontier, true
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
	cutoff := s.now().Add(-sessionIdleTimeout)
	s.mu.Lock()
	var teardowns []*sessionTeardown
	for _, session := range s.sessions {
		if session.state == sessionRunning && session.lastAccess.Before(cutoff) {
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
		cancels := s.libraryCancelsLocked(func(store.LibraryID) bool { return true })
		s.wakeCloseLocked()
		var teardowns []*sessionTeardown
		for _, session := range s.sessions {
			teardowns = append(teardowns, s.transitionLocked(session, detachSession{}).teardown)
		}
		for session := range s.continuations {
			teardowns = append(teardowns, s.transitionLocked(session, detachSession{}).teardown)
		}
		s.mu.Unlock()
		for _, cancel := range cancels {
			cancel()
		}
		s.runTeardowns(teardowns)

		s.mu.Lock()
		for !s.offLockIdleLocked() {
			s.offLockIdle = make(chan struct{})
			idle := s.offLockIdle
			s.mu.Unlock()
			<-idle
			s.mu.Lock()
		}
		s.mu.Unlock()
		if s.rootLock != nil {
			s.closeErr = errors.Join(s.closeSweepErr, s.retryScratch(true), unlockScratchRoot(s.rootLock))
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

// inspectMediaBoxes reports whether a complete ISO-BMFF segment has media.
// It reads box headers and distinguishes malformed or unreadable data.
func inspectMediaBoxes(r io.ReaderAt, size int64) (empty, readable bool) {
	if size == 0 {
		return false, false
	}
	var header [16]byte
	for off := int64(0); off < size; {
		if size-off < 8 {
			return false, false // trailing bytes that are not a box
		}
		if _, err := r.ReadAt(header[:8], off); err != nil {
			return false, false
		}
		boxSize := int64(binary.BigEndian.Uint32(header[:4]))
		headerSize := int64(8)
		switch boxSize {
		case 0: // box extends to end of file
			boxSize = size - off
		case 1: // 64-bit largesize follows the type
			if size-off < 16 {
				return false, false
			}
			if _, err := r.ReadAt(header[8:16], off+8); err != nil {
				return false, false
			}
			large := binary.BigEndian.Uint64(header[8:16])
			// #nosec G115 -- size-off is positive (loop guard), so it fits in uint64.
			if large > uint64(size-off) {
				return false, false
			}
			boxSize = int64(large) // #nosec G115 -- large <= size-off, an int64, so it fits.
			headerSize = 16
		}
		if boxSize < headerSize || boxSize > size-off {
			return false, false
		}
		if string(header[4:8]) == "mdat" && boxSize > headerSize {
			return false, true
		}
		off += boxSize
	}
	return true, true
}
