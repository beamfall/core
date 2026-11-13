package playback

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	proc    *ffmpeg.HLSProcess
	exited  chan struct{}
	outcome encoderExit // written under Sessions.mu before exited closes
	// waiting counts the requests currently waiting on this run's artifacts;
	// the stall watchdog only acts while it is non-zero.
	waiting atomic.Int32
	// stalled is set by the stall watchdog just before it kills the process.
	stalled atomic.Bool
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
	dir               string
	src               string
	durationSeconds   int
	rungs             []Rung
	audioProfile      ffmpeg.AudioProfile
	audioStreamIndex  *int
	sourceVideo       VideoSignal
	burnSubtitleIndex int
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
	// startPending is set while ensureRung stops a replaced process and spawns
	// the next one with mu released. The session keeps its transcode slot and
	// lease meanwhile.
	startPending bool
	baseSegment  int
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
	// sourceEndSeconds is an upper bound on where the source's media ends,
	// known once an encoder hit end of input early or produced a segment with
	// no media (a truncated file whose probed duration overstates it). It only
	// ever lowers. Segments starting at or after it are refused as
	// ErrSourceEnded instead of being encoded as empty segments.
	sourceEndSeconds int
	sourceEndKnown   bool
	// resumeSeconds is where playback info resumed the viewer. The session's
	// first encoder starts at the segment containing it, so a resume spawns
	// FFmpeg once instead of starting at 0 for init.mp4 and restarting at the
	// first media request. Cleared once an encoder starts.
	resumeSeconds int
}

// takeRestartLocked spends one crash restart from the session's sliding
// window budget, reporting false when the budget is exhausted.
func (h *hlsSession) takeRestartLocked(now time.Time) bool {
	recent := h.crashRestarts[:0]
	for _, at := range h.crashRestarts {
		if now.Sub(at) < encoderRestartWindow {
			recent = append(recent, at)
		}
	}
	h.crashRestarts = recent
	if len(recent) >= encoderRestartBudget {
		return false
	}
	h.crashRestarts = append(h.crashRestarts, now)
	return true
}

// noteSourceEndLocked lowers the known source end to seconds, reporting
// whether it changed (so each new fact is logged once).
func (h *hlsSession) noteSourceEndLocked(seconds int) bool {
	if h.sourceEndKnown && seconds >= h.sourceEndSeconds {
		return false
	}
	h.sourceEndSeconds = seconds
	h.sourceEndKnown = true
	return true
}

// beyondSourceEndLocked reports whether a segment starts at or past the known
// source end.
func (h *hlsSession) beyondSourceEndLocked(segment, segSeconds int) bool {
	return h.sourceEndKnown && segment*segSeconds >= h.sourceEndSeconds
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
		ColorTransfer:    h.sourceVideo.ColorTransfer,
		ColorPrimaries:   h.sourceVideo.ColorPrimaries,
		DoViProfile:      h.sourceVideo.DoViProfile,
		DoViBLCompatID:   h.sourceVideo.DoViBLCompatID,
	}
	if h.burnSubtitleIndex >= 0 {
		rendition = rendition.WithBurnSubtitle(h.burnSubtitleIndex)
	}
	return rendition
}

// Sessions owns the lifecycle of HLS transcode sessions:
// lazily started on first segment request, restarted on far seeks and
// rendition switches, capped server-wide with idle preemption, reaped after
// idleness, all stopped on shutdown.
type Sessions struct {
	transcoder Transcoder
	dir        string
	now        func() time.Time
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
}

// sessionTeardown is the slow half of removing a session: stopping its FFmpeg
// process (which waits for the exit) and deleting its working directory. A
// caller collects it under s.mu and runs it after releasing the lock, so one
// slow teardown never stalls requests for every other session.
type sessionTeardown struct {
	proc *ffmpeg.HLSProcess
	dir  string
}

// runTeardowns stops each process, then removes each directory. Call it
// without s.mu held.
func (s *Sessions) runTeardowns(teardowns []sessionTeardown) {
	for _, t := range teardowns {
		s.stop(t.proc)
		if t.dir != "" {
			_ = os.RemoveAll(t.dir)
		}
	}
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
	return session.proc != nil || session.startPending
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

// releaseTranscodeLease releases and clears a session's transcode lease if it
// holds one. Idempotent per session — the lease's own Release is a sync.Once and
// the field is cleared — so a launch-failure rollback, a process reaper, and a
// teardown can each call it without double-counting.
func (s *Sessions) releaseTranscodeLease(session *hlsSession) {
	if session.transcodeLease != nil {
		session.transcodeLease.Release()
		session.transcodeLease = nil
	}
}

var segmentName = regexp.MustCompile(`^seg(\d{5})\.m4s$`)

// rungFileName matches the per-rendition artifacts under a rung directory.
var rungFileName = regexp.MustCompile(`^(index\.m3u8|init\.mp4|seg\d{5}\.m4s)$`)

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

// liveCount counts sessions holding a live FFmpeg process. Callers hold mu.
func (s *Sessions) liveCount() int {
	count := 0
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
func (s *Sessions) preemptableLocked(key SessionKey, priority Priority) *SessionKey {
	cutoff := s.now().Add(-preemptIdleAfter)
	var victim *SessionKey
	var oldest time.Time
	for other, session := range s.sessions {
		if other == key || !session.holdsTranscodeLocked() {
			continue
		}
		idle := session.lastAccess.Before(cutoff)
		outranked := session.priority < priority
		if !idle && !outranked {
			continue // active and of equal-or-higher rank: protected
		}
		if victim == nil || session.lastAccess.Before(oldest) {
			oldest = session.lastAccess
			otherKey := other
			victim = &otherKey
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
		return s.removeLocked(*victim), nil
	}
	if degraded {
		return nil, nil // over-cap, lowest rung only: keep the party member streaming
	}
	return nil, ErrTranscodeCapacity
}

// removeLocked forgets one session and releases its transcode lease. It returns
// the session's teardown (stop the process, delete the working directory),
// which the caller runs after releasing mu, or nil when key has no session.
// Callers hold mu.
func (s *Sessions) removeLocked(key SessionKey) *sessionTeardown {
	session, ok := s.sessions[key]
	if !ok {
		return nil
	}
	teardown := &sessionTeardown{proc: session.proc, dir: session.dir}
	// Released here rather than when the process exits: the session is gone
	// from the map, so nothing else will release it. An ensureRung start in
	// progress (startPending) holds this same lease; it sees the eviction when
	// it relocks, stops what it spawned and removes the directory again.
	s.releaseTranscodeLease(session)
	// Clear the process fields too: a request may still hold this *hlsSession
	// across an unlock window, and a torn-down session must never read back as
	// one holding a live transcode. liveLocked is the authoritative eviction
	// check (the map entry is gone below); this keeps the object itself honest.
	session.proc = nil
	session.currentRung = ""
	delete(s.sessions, key)
	return teardown
}

// liveLocked reports whether session is still the session registered for key.
// A *hlsSession pointer outlives eviction: device revocation, idle reap,
// preemption, and mode-shaping replacement all stop the process, RemoveAll the
// working dir, and delete the map entry while an in-flight request may be
// holding the pointer — either between s.session() returning and the next lock,
// or across ensureRung's off-lock frontier scan. Map identity is the single
// eviction test, because every teardown path deletes the entry and no live
// session is ever absent from the map. Callers hold mu.
func (s *Sessions) liveLocked(key SessionKey, session *hlsSession) bool {
	return s.sessions[key] == session
}

// TerminateSession aborts every in-flight transcode owned by one auth Session
// and returns how many it stopped (ADR-0012 §7: a revoked device's in-flight
// transcodes die, not just its future requests). Sessions are keyed by
// (SessionID, MediaID) — one Session may hold several live transcodes — so this
// stops all of them at once. It mirrors removeLocked (Stop the FFmpeg process,
// release the transcode slot, delete the artifacts), the same teardown idle
// reaping and preemption already use, so it introduces no new shutdown path.
// Calling it for a Session with no live transcode is a zero-stop no-op — a
// direct-play session has nothing to abort. The caller (the audited
// device-revocation mutation) invokes this after RevokeDevice has already
// blocked future auth; this closes the live transport that revocation alone
// leaves running until idle reap.
func (s *Sessions) TerminateSession(sessionID store.SessionID) int {
	s.mu.Lock()
	var teardowns []sessionTeardown
	for key := range s.sessions {
		if key.SessionID == sessionID {
			teardowns = append(teardowns, *s.removeLocked(key))
		}
	}
	s.mu.Unlock()
	s.runTeardowns(teardowns)
	return len(teardowns)
}

// NewSessions creates a transcode session manager rooted at dataDir/transcode.
func NewSessions(transcoder Transcoder, dataDir string, now func() time.Time) *Sessions {
	return NewSessionsInDir(transcoder, filepath.Join(dataDir, "transcode"), now)
}

// NewSessionsInDir creates a transcode session manager rooted at scratchDir.
// It purges every pre-existing session directory under scratchDir before
// returning: the in-memory session map is always empty at construction, so
// PurgeOrphanedTranscodeDirs' live-session confinement treats every
// session-<nonce>-<N>/ entry already on disk as an orphan of a prior process
// (a restart drops the map that would have removed them on normal end) and
// removes it, closing the window where a reused-looking directory name could
// serve a previous run's leftover segments to a new session.
func NewSessionsInDir(transcoder Transcoder, scratchDir string, now func() time.Time) *Sessions {
	if now == nil {
		now = time.Now
	}
	s := &Sessions{
		transcoder:  transcoder,
		dir:         scratchDir,
		now:         now,
		bootNonce:   newBootNonce(),
		sessions:    map[SessionKey]*hlsSession{},
		cap:         defaultTranscodeCap,
		resumeHints: map[SessionKey]resumeHint{},
	}
	_ = s.PurgeOrphanedTranscodeDirs()
	return s
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
	if caps.HEVC && !s.SupportsTranscodeCodec("hevc") {
		caps.HEVC = false
		caps.HEVCMaxHeight = 0
	}
	session, err := s.session(key, src, durationSeconds, videoWidth, videoHeight, audioCopy, degraded, caps, sourceVideo, burnSubtitleIndex, audioProfile, audioStreamIndex, priority)
	if err != nil {
		return "", err
	}
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
	isSegment := false
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
		isSegment = true
		if session.durationSeconds > 0 && segment >= segmentCount(session.durationSeconds, rungSegmentSeconds(rung)) {
			return "", errors.New("unknown stream file")
		}
		if s.beyondSourceEnd(session, segment, rungSegmentSeconds(rung)) {
			return "", ErrSourceEnded // known short source: never encode an empty segment
		}
		if run, err = s.ensureRung(ctx, key, session, rung, segment, path); err != nil {
			return "", err
		}
	}
	if err := s.waitWhileEncoderProgresses(ctx, key, session, rung, segment, isSegment, path, run, waitBudget); err != nil {
		return "", err
	}
	if isSegment && segmentLacksMedia(path) {
		// FFmpeg started past the real end of a truncated source writes a
		// segment with no media; serving it as playable would stall the player.
		s.noteSourceEnd(key, session, rung, segment*rungSegmentSeconds(rung), "")
		return "", ErrSourceEnded
	}
	if file != "index.m3u8" {
		s.markDelivered(key, session, rung.ID)
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
		err := waitForArtifact(ctx, path, run.exitedChan())
		if run != nil {
			run.waiting.Add(-1)
		}
		if !errors.Is(err, errEncoderExited) {
			return err
		}
		switch run.outcome {
		case exitStopped:
			s.mu.Lock()
			live := s.liveLocked(key, session)
			s.mu.Unlock()
			if !live {
				return ErrSessionEvicted
			}
			run = nil // replaced by another request's encoder: keep waiting
		case exitEndOfInput:
			if isSegment && s.beyondSourceEnd(session, segment, rungSegmentSeconds(rung)) {
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
		current, _, tracked := s.rungEncoder(key, session, rung)
		waitCtx, cancel := context.WithTimeout(ctx, budget)
		err := s.waitForEncoder(waitCtx, key, session, rung, segment, isSegment, path, run)
		cancel()
		if err == nil || !isSegment || ctx.Err() != nil || !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		after, base, stillTracked := s.rungEncoder(key, session, rung)
		if !tracked || !stillTracked || current != run || after != run || base > segment ||
			!segmentWrittenSince(filepath.Join(session.dir, rung.ID), base, windowStart) {
			return err
		}
	}
}

// rungEncoder returns the session's running encoder for rung and the segment it
// started at. tracked is false when the session is gone or is not encoding rung.
func (s *Sessions) rungEncoder(key SessionKey, session *hlsSession, rung Rung) (run *encoderRun, base int, tracked bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.liveLocked(key, session) || session.proc == nil || session.currentRung != rung.ID {
		return nil, 0, false
	}
	return session.run, session.baseSegment, true
}

// segmentWrittenSince reports whether rungDir holds a segment at or past base
// modified at or after since. Segments cached by earlier runs keep their older
// times, so only this run's writes count.
func segmentWrittenSince(rungDir string, base int, since time.Time) bool {
	entries, err := os.ReadDir(rungDir)
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
		if info, err := entry.Info(); err == nil && !info.ModTime().Before(since) {
			return true
		}
	}
	return false
}

// beyondSourceEnd reports whether segment starts past the session's known
// source end.
func (s *Sessions) beyondSourceEnd(session *hlsSession, segment, segSeconds int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return session.beyondSourceEndLocked(segment, segSeconds)
}

// noteSourceEnd records that the source has no media from seconds on and logs
// it once per new (lower) bound.
func (s *Sessions) noteSourceEnd(key SessionKey, session *hlsSession, rung Rung, seconds int, stderr string) {
	s.mu.Lock()
	lowered := s.liveLocked(key, session) && session.noteSourceEndLocked(seconds)
	s.mu.Unlock()
	if lowered {
		logSourceEnd(key, session, rung, seconds, stderr)
	}
}

func logSourceEnd(key SessionKey, session *hlsSession, rung Rung, seconds int, stderr string) {
	slog.Warn("media source ends before its probed duration; refusing segments past the end",
		"media", key.MediaID,
		"session_dir", filepath.Base(session.dir),
		"rung", rung.ID,
		"source_end_seconds", seconds,
		"source_end_segment", seconds/rungSegmentSeconds(rung),
		"probed_duration_seconds", session.durationSeconds,
		"stderr", stderr)
}

// markDelivered records the rendition a media request was just served from. A
// player switching back to a rendition already on disk starts no encoder, so
// the encoder start alone would keep reporting the previous rendition.
// Variant playlists do not count: a player may read several while choosing.
func (s *Sessions) markDelivered(key SessionKey, session *hlsSession, rungID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.liveLocked(key, session) {
		session.deliveredRung = rungID
	}
}

// transcodeLatencyBudget classifies a missing artifact before ensureRung
// starts or replaces FFmpeg. Cached artifacts are returned before waiting, so
// only first-start and post-start encode paths consume these numeric SLOs.
func (s *Sessions) transcodeLatencyBudget(session *hlsSession, path string) time.Duration {
	if fileReady(path) {
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
// served rendition copy the source video and transcode only the audio.
func (s *Sessions) session(key SessionKey, src string, durationSeconds, videoWidth, videoHeight int, audioCopy, degraded bool, caps ladderCaps, sourceVideo VideoSignal, burnSubtitleIndex int, audioProfile ffmpeg.AudioProfile, audioStreamIndex *int, priority Priority) (*hlsSession, error) {
	var teardowns []sessionTeardown
	s.mu.Lock()
	defer func() {
		s.mu.Unlock()
		s.runTeardowns(teardowns)
	}()
	requestedRungs := rungsForSession(videoWidth, videoHeight, audioCopy, degraded, caps)
	if existing, ok := s.sessions[key]; ok {
		if existing.src != src || existing.durationSeconds != durationSeconds || existing.degraded != degraded || !rungsEqual(existing.rungs, requestedRungs) || existing.sourceVideo != sourceVideo || existing.burnSubtitleIndex != burnSubtitleIndex || !audioProfilesEqual(existing.audioProfile, audioProfile) || !equalOptionalInt(existing.audioStreamIndex, audioStreamIndex) {
			teardowns = append(teardowns, *s.removeLocked(key)) // mode-shaping changed; old artifacts are incompatible
		} else {
			existing.lastAccess = s.now()
			if priority > existing.priority {
				existing.priority = priority // raise-only: earned standing is kept
			}
			return existing, nil
		}
	}
	s.nextID++
	dir := filepath.Join(s.dir, fmt.Sprintf("session-%s-%d", s.bootNonce, s.nextID))
	session := &hlsSession{
		dir:               dir,
		src:               src,
		durationSeconds:   durationSeconds,
		rungs:             requestedRungs,
		audioProfile:      audioProfile,
		audioStreamIndex:  audioStreamIndex,
		sourceVideo:       sourceVideo,
		burnSubtitleIndex: burnSubtitleIndex,
		lastAccess:        s.now(),
		priority:          priority,
		degraded:          degraded,
	}
	if hint, ok := s.resumeHints[key]; ok {
		delete(s.resumeHints, key)
		if s.now().Sub(hint.at) < sessionIdleTimeout {
			session.resumeSeconds = hint.seconds
		}
	}
	if err := writeSessionPlaylists(dir, requestedRungs, durationSeconds); err != nil {
		teardowns = append(teardowns, sessionTeardown{dir: dir})
		return nil, err
	}
	s.sessions[key] = session
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
// playlist each variant serves before a single segment exists.
func writeSessionPlaylists(dir string, rungs []Rung, durationSeconds int) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, masterPlaylistName), []byte(masterPlaylist(rungs)), 0o600); err != nil {
		return err
	}
	for _, rung := range rungs {
		rungDir := filepath.Join(dir, rung.ID)
		if err := os.MkdirAll(rungDir, 0o700); err != nil {
			return err
		}
		if durationSeconds > 0 {
			if err := os.WriteFile(filepath.Join(rungDir, "index.m3u8"), []byte(vodPlaylist(durationSeconds, rungSegmentSeconds(rung))), 0o600); err != nil {
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
	if _, err := os.Stat(path); err == nil {
		return nil, nil // already produced (possibly by an earlier rendition run)
	}
	session.startMu.Lock()
	defer session.startMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.liveLocked(key, session) {
		return nil, ErrSessionEvicted // torn down between s.session() and this lock
	}
	if _, err := os.Stat(path); err == nil {
		return nil, nil
	}
	rungDir := filepath.Join(session.dir, rung.ID)
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
		// session may have been evicted entirely while unlocked (liveLocked), and
		// its generation (proc/baseSegment) may have moved.
		base := session.baseSegment
		proc := session.proc
		s.mu.Unlock()
		scanned := scanFrontier(rungDir, base)
		s.mu.Lock()
		if !s.liveLocked(key, session) {
			return nil, ErrSessionEvicted // torn down while the frontier scan ran off-lock
		}
		if session.proc == proc && session.currentRung == rung.ID && session.baseSegment == base {
			if scanned > session.frontier {
				session.frontier = scanned // advance-only within this generation
			}
			if segment >= session.baseSegment && segment <= session.frontier+seekLookaheadSegments {
				return session.run, nil // the running transcode will reach it; wait outside the lock
			}
		}
	}
	var victim *sessionTeardown
	var replaced *ffmpeg.HLSProcess
	if session.proc == nil {
		// The last encoder crashed: this start is a crash restart, bounded by
		// the session budget so a source that kills FFmpeg fails fast.
		if session.lastCrashed && !session.takeRestartLocked(s.now()) {
			return nil, errEncoderFailing
		}
		var err error
		if victim, err = s.admitLocked(key, session.priority, session.degraded); err != nil {
			return nil, err
		}
		// Acquire the realtime-transcode lease BEFORE the process launches: the
		// governor preempts background generation the moment this session commits
		// to spawning, closing the window where FFmpeg competes with generation on
		// a weak CPU before generation is told to yield. A launch failure below
		// rolls the lease back.
		session.transcodeLease = s.beginTranscode()
	} else {
		// One rendition per live session: a switch or far seek replaces the
		// process, never runs a sibling. No lease is acquired here and none is
		// released: Stop() alone never touches the lease (only removeLocked, the
		// process reaper, and the launch-failure rollback do), and the liveLocked
		// guards above already rejected an evicted session — whose lease removeLocked
		// has released — so the lease this branch carries across the swap is always
		// the live one. The session stays exactly one live transcode unit.
		replaced = session.proc
		session.proc = nil
		session.currentRung = ""
	}
	// Stopping a process waits for it to exit, and spawning one can be slow too,
	// so both run with mu released: one session's encoder churn must not stall
	// requests for every other session. startPending keeps this session's slot
	// counted meanwhile, and startMu keeps a second request on this session from
	// starting another encoder.
	session.startPending = true
	s.mu.Unlock()
	if victim != nil {
		s.runTeardowns([]sessionTeardown{*victim})
	}
	s.stop(replaced)
	procCtx := context.WithoutCancel(ctx)
	var proc *ffmpeg.HLSProcess
	var err error
	if session.durationSeconds > 0 {
		proc, err = s.transcoder.StartHLSSegments(procCtx, session.src, rungDir, segment, rungSegmentSeconds(rung), session.rendition(rung))
	} else {
		proc, err = s.transcoder.StartHLS(procCtx, session.src, rungDir, session.rendition(rung))
	}
	s.mu.Lock()
	session.startPending = false
	if !s.liveLocked(key, session) {
		// Torn down while the lock was released. removeLocked already released
		// the lease and scheduled the directory's removal, possibly before the
		// replaced process exited or the new one wrote anything, so stop the new
		// process and remove the directory again.
		s.mu.Unlock()
		if err == nil {
			s.stop(proc)
		}
		_ = os.RemoveAll(session.dir)
		s.mu.Lock()
		return nil, ErrSessionEvicted
	}
	if err != nil {
		// Launch failed: this session holds no live process, so release its
		// transcode lease. This is the rollback for a fresh start and the teardown
		// for a replacement that could not relaunch — either way the session drops
		// to zero live transcodes and generation may resume.
		s.releaseTranscodeLease(session)
		return nil, err
	}
	run := &encoderRun{proc: proc}
	session.proc = proc
	session.run = run
	session.currentRung = rung.ID
	session.deliveredRung = rung.ID
	session.baseSegment = segment
	session.frontier = segment - 1 // new generation: nothing produced past base yet
	session.lastAccess = s.now()
	session.started = true
	session.resumeSeconds = 0
	if proc.Running() {
		run.exited = make(chan struct{})
		s.reapProcess(key, session, run, rung, segment)
		if proc.ReportsProgress() {
			go watchForStall(run, s.encoderStallTimeout())
		}
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
	segSeconds := rungSegmentSeconds(rung)
	segment := session.resumeSeconds / segSeconds
	if segment >= segmentCount(session.durationSeconds, segSeconds) || session.beyondSourceEndLocked(segment, segSeconds) {
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
		if !session.started {
			session.resumeSeconds = max(seconds, 0)
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
	go func() {
		defer close(run.exited)
		waitErr := run.proc.Wait()
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
		segSeconds := rungSegmentSeconds(rung)
		frontier := scanFrontier(rungDir, base)
		sourceEnd := -1
		if outcome == exitEndOfInput && session.durationSeconds > 0 {
			last := frontier
			for last >= base && segmentLacksMedia(filepath.Join(rungDir, fmt.Sprintf("seg%05d.m4s", last))) {
				last--
			}
			if end := last + 1; end < segmentCount(session.durationSeconds, segSeconds) {
				sourceEnd = end * segSeconds
			}
		}

		s.mu.Lock()
		run.outcome = outcome
		live := s.liveLocked(key, session)
		if live && session.proc == run.proc {
			session.proc = nil
			session.currentRung = ""
			session.lastCrashed = outcome == exitCrashed || outcome == exitStalled
			s.releaseTranscodeLease(session)
		}
		lowered := live && sourceEnd >= 0 && session.noteSourceEndLocked(sourceEnd)
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
		if lowered {
			logSourceEnd(key, session, rung, sourceEnd, stderr)
		}
	}()
}

// scanFrontier returns the highest segment number at or after base that exists
// in rungDir, or base-1 when none do yet. It is a pure filesystem read taking no
// session state, so callers run it off-lock (never holding s.mu) and fold the
// result back into the session's cached frontier under the lock.
func scanFrontier(rungDir string, base int) int {
	frontier := base - 1
	entries, err := os.ReadDir(rungDir)
	if err != nil {
		return frontier
	}
	for _, entry := range entries {
		match := segmentName.FindStringSubmatch(entry.Name())
		if match == nil {
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
func vodPlaylist(durationSeconds, segmentSeconds int) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:7\n")
	b.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", segmentSeconds))
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	b.WriteString(fmt.Sprintf("#EXT-X-MAP:URI=%q\n", initSegmentName))
	count := segmentCount(durationSeconds, segmentSeconds)
	for i := 0; i < count; i++ {
		length := segmentSeconds
		if i == count-1 {
			if remainder := durationSeconds - segmentSeconds*(count-1); remainder > 0 {
				length = remainder
			}
		}
		b.WriteString(fmt.Sprintf("#EXTINF:%d.000,\n", length))
		b.WriteString(fmt.Sprintf("seg%05d.m4s\n", i))
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
	var teardowns []sessionTeardown
	cutoff := s.now().Add(-sessionIdleTimeout)
	for key, session := range s.sessions {
		if session.lastAccess.Before(cutoff) {
			teardowns = append(teardowns, *s.removeLocked(key))
		}
	}
	s.mu.Unlock()
	s.runTeardowns(teardowns)
}

// sessionDirName matches a transcode session working directory
// (session-<bootNonce>-<N>), the layout session() mints under the transcode
// root, and the pre-nonce session-<N> layout an older build left on disk, which
// can still hold plaintext segments after an upgrade. The reconcile sweep uses
// it to tell a session dir apart from any other entry under the root.
var sessionDirName = regexp.MustCompile(`^session-(?:[0-9a-f]{16}-)?\d+$`)

// PurgeOrphanedTranscodeDirs removes every transcode session directory under the
// transcode root that no live session owns. It closes the crash-recovery leak of
// the 5th derivative class (ADR-0045 §reconcile, ENCPRIV-10c): a live HLS
// transcode of Encrypted-Library media writes PLAINTEXT segments to
// dataDir/transcode/session-<bootNonce>-<N>/, and a crash mid-transcode orphans that dir on
// disk — the in-memory session map that would have removed it on normal end is
// gone after the restart. Those orphans are outside the four MediaID/fingerprint-
// keyed derivative classes the reconciler otherwise sweeps (session dirs carry no
// on-disk media/library attribution — they are keyed by an in-memory counter), so
// they are purged here as a set.
//
// The sweep runs under the session lock and skips any dir a live session still
// holds, so it never deletes an in-flight session's working dir even if it runs
// after serving has begun (the runtime-unseal path). At boot the in-memory map is
// empty — a restart drops all session state before serving — so every session dir
// on disk is an orphan and is removed — which is also why NewSessionsInDir
// calls this at construction, before any session is live, to purge whatever a
// prior process on the same root left behind. Hard delete is correct: session
// dirs are ephemeral, regenerable transcode caches, and invariant #5 forbids
// leaked plaintext at rest. Any removal error is joined and returned so a
// caller with a use for it — the encrypted-derivative reconciler fails the
// unseal closed — can react; NewSessionsInDir has none, so it discards it.
func (s *Sessions) PurgeOrphanedTranscodeDirs() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	live := make(map[string]struct{}, len(s.sessions))
	for _, session := range s.sessions {
		live[session.dir] = struct{}{}
	}
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var joined error
	for _, entry := range entries {
		if !entry.IsDir() || !sessionDirName.MatchString(entry.Name()) {
			continue
		}
		dir := filepath.Join(s.dir, entry.Name())
		if _, held := live[dir]; held {
			continue // in-flight session: never delete a live plaintext working dir
		}
		joined = errors.Join(joined, os.RemoveAll(dir))
	}
	return joined
}

// Close stops every running transcode and removes its working directory.
func (s *Sessions) Close() {
	s.mu.Lock()
	var teardowns []sessionTeardown
	for key := range s.sessions {
		teardowns = append(teardowns, *s.removeLocked(key))
	}
	s.mu.Unlock()
	s.runTeardowns(teardowns)
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
	return waitForArtifact(ctx, path, nil)
}

// waitForArtifact is waitForFile that also watches the producing encoder:
// when exited closes before path is ready it returns errEncoderExited, so a
// dead encoder is noticed in milliseconds rather than at the deadline. A nil
// exited never fires.
func waitForArtifact(ctx context.Context, path string, exited <-chan struct{}) error {
	if fileReady(path) {
		return nil // fast path: already produced, no watch needed
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return pollForArtifact(ctx, path, exited)
	}
	defer watcher.Close()
	// Arm the watch on the parent dir BEFORE the post-arm stat: a file that
	// appears between arming and the stat is caught by the stat; one that appears
	// after is caught by the event. Arming after the stat would leave a window
	// where an event is missed and the file already exists — the classic
	// inotify setup race.
	if err := watcher.Add(filepath.Dir(path)); err != nil {
		return pollForArtifact(ctx, path, exited)
	}
	if fileReady(path) {
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
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("stream not ready")
		case <-exited:
			if fileReady(path) {
				return nil // the encoder's last write landed just before it exited
			}
			return errEncoderExited
		case event := <-watcher.Events:
			if filepath.Clean(event.Name) != filepath.Clean(path) {
				continue
			}
			if fileReady(path) {
				return nil
			}
			// A Create notification can precede the first write, and that Write
			// may be coalesced. Schedule exactly one post-event readiness check.
			if settleC == nil {
				settle.Reset(fileEventSettleDelay)
				settleC = settle.C
			}
		case <-watcher.Errors:
			return pollForArtifact(ctx, path, exited) // watch broke; degrade to polling
		case <-settleC:
			settleC = nil
			if fileReady(path) {
				return nil
			}
		case <-safety.C:
			if fileReady(path) {
				return nil // backstop a missed/coalesced event
			}
		}
	}
}

// pollForFile is the watcher-unavailable fallback: the original slow stat poll,
// preserving waitForFile's exact contract (ready → nil, deadline → "stream not
// ready", cancel → ctx.Err()) when an fsnotify watch cannot be established.
func pollForFile(ctx context.Context, path string) error {
	return pollForArtifact(ctx, path, nil)
}

// pollForArtifact is pollForFile that also returns errEncoderExited once
// exited closes with path still not ready.
func pollForArtifact(ctx context.Context, path string, exited <-chan struct{}) error {
	deadline := time.Now().Add(fileWaitTimeout)
	for {
		if fileReady(path) {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("stream not ready")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-exited:
			if fileReady(path) {
				return nil
			}
			return errEncoderExited
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// segmentLacksMedia reports whether the fMP4 segment at path is a complete,
// well-formed ISO-BMFF box sequence carrying no media payload (no non-empty
// mdat box). FFmpeg's HLS muxer writes such a segment — a lone styp box,
// EXTINF 0 — when it is started at or past the real end of a truncated input.
// Anything it cannot positively parse reads as media, so an unexpected layout
// is served as before rather than refused. It reads only box headers.
func segmentLacksMedia(path string) bool {
	f, err := os.Open(path) // #nosec G304 -- path is a segment file inside the session's own encoder output directory
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return false
	}
	size := info.Size()
	var header [16]byte
	for off := int64(0); off < size; {
		if size-off < 8 {
			return false // trailing bytes that are not a box
		}
		if _, err := f.ReadAt(header[:8], off); err != nil {
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
			if _, err := f.ReadAt(header[8:16], off+8); err != nil {
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
