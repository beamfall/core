package playback

import (
	"context"
	"errors"
	"os"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

var errSetupUnavailable = errors.New("live playback setup unavailable")

// LiveBinding carries the exact instance selected at signed-grant entry.
type LiveBinding struct {
	manager     *Sessions
	session     *hlsSession
	profile     store.ProfileID
	library     store.LibraryID
	duration    *retainedDuration
	attached    bool
	loadProfile func(context.Context, store.ProfileID) (store.Profile, error)
}

type setupOperation struct {
	done     chan struct{}
	source   sourceCapture
	shape    setupInputs
	picture  *ComposedPicture
	compared bool
	finished bool
	err      error
}

type setupInputs struct {
	videoWidth, videoHeight                  int
	duration, burnSubtitle                   int
	audioCopy, degraded, composed, audioOnly bool
	caps                                     ladderCaps
	video                                    VideoSignal
	audio                                    ffmpeg.AudioProfile
	stream                                   *int
	adjust                                   ffmpeg.AudioAdjust
}

func (x setupInputs) matches(y setupInputs) bool {
	return x.videoWidth == y.videoWidth && x.videoHeight == y.videoHeight &&
		x.duration == y.duration && x.burnSubtitle == y.burnSubtitle &&
		x.audioCopy == y.audioCopy && x.degraded == y.degraded &&
		x.composed == y.composed && x.audioOnly == y.audioOnly &&
		x.caps == y.caps && x.video == y.video && audioProfilesEqual(x.audio, y.audio) &&
		equalOptionalInt(x.stream, y.stream) && x.adjust == y.adjust
}

func newSessionReservation(key SessionKey) *hlsSession {
	ctx, cancel := context.WithCancel(context.Background())
	return &hlsSession{key: key, cutoffCtx: ctx, cancelCutoff: cancel}
}

// BindLive selects a head before scope verification or source I/O.
func (s *Sessions) BindLive(key SessionKey) (*LiveBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrSessionEvicted
	}
	session := s.sessions[key]
	if session == nil || session.state >= sessionFailed {
		session = newSessionReservation(key)
		s.sessions[key] = session
	}
	session.binders++
	return &LiveBinding{manager: s, session: session}, nil
}

// Attach records the verified scope. The lookup runs only for an encoder start acquiring capacity.
func (b *LiveBinding) Attach(library store.LibraryID, profile store.ProfileID, loadProfile func(context.Context, store.ProfileID) (store.Profile, error)) error {
	s := b.manager
	s.mu.Lock()
	defer s.mu.Unlock()
	h := b.session
	if s.closed || h.state >= sessionRevoking || h.state != sessionFailed && s.sessions[h.key] != h {
		return ErrSessionEvicted
	}
	if h.admitted && (h.library != library || h.profile != profile) {
		return ErrSessionEvicted
	}
	h.admitted, h.library, h.profile = true, library, profile
	b.library, b.profile, b.loadProfile, b.attached = library, profile, loadProfile, true
	return nil
}

// Check reports cutoff, including for a binding to a failed setup.
func (b *LiveBinding) Check() error {
	s := b.manager
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || b.session.state >= sessionRevoking {
		return ErrSessionEvicted
	}
	return nil
}

func (b *LiveBinding) validLocked() bool {
	s, h := b.manager, b.session
	return !s.closed && h.state < sessionFailed && s.sessions[h.key] == h
}

// Release drops the binding and any source result it retained.
func (b *LiveBinding) Release() {
	s := b.manager
	s.mu.Lock()
	defer s.mu.Unlock()
	h := b.session
	h.binders--
	if h.binders == 0 {
		delete(s.failedHeads, h)
	}
	if b.duration != nil {
		s.releaseDurationLocked(b.duration)
	}
	if h.binders == 0 && h.state != sessionRunning && h.setup == nil && s.sessions[h.key] == h {
		delete(s.sessions, h.key)
	}
}

// Context cancels verification or source preparation when the bound instance is cut off.
func (b *LiveBinding) Context(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(b.session.cutoffCtx, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

// ProbedDurationSeconds registers the live spawn permit before source stat or
// FFprobe preparation and retains the generation's result until shape commit.
func (b *LiveBinding) ProbedDurationSeconds(ctx context.Context, fingerprint, path string) int {
	if !b.attached {
		return 0
	}
	ctx, cancel := b.Context(ctx)
	defer cancel()
	ctx = b.probeContext(ctx)
	return b.manager.probedSourceDuration(ctx, b, fingerprint, path)
}

type probeOwner struct {
	done      chan struct{}
	handedOff bool
}

func (b *LiveBinding) probeContext(ctx context.Context) context.Context {
	s, h := b.manager, b.session
	var active *probeOwner
	return ffmpeg.WithPlaybackProbe(ctx, func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if h.state >= sessionFailed || s.closed || s.sessions[h.key] != h {
			return ErrSessionEvicted
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		active = &probeOwner{done: make(chan struct{})}
		if h.probes == nil {
			h.probes = make(map[*probeOwner]struct{})
		}
		h.probes[active] = struct{}{}
		s.probing++
		return nil
	}, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		active.handedOff = h.state < sessionRevoking && !s.closed
		return active.handedOff
	}, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(h.probes, active)
		s.probing--
		close(active.done)
		s.wakeCloseLocked()
	})
}

func (b *LiveBinding) claimSetup(ctx context.Context, source sourceCapture, shape setupInputs) (*setupOperation, error) {
	s := b.manager
	for {
		s.mu.Lock()
		h := b.session
		if h.state == sessionFailed && !s.closed {
			setup := h.failedSetup
			if !setup.compared || setup.source.generation == "" || source.generation == "" {
				s.mu.Unlock()
				return nil, errSetupUnavailable
			}
			if setup.source.matches(source) && setup.shape.matches(shape) {
				s.mu.Unlock()
				return nil, setup.err
			}
			if s.sessions[h.key] != h {
				s.mu.Unlock()
				return nil, errSetupUnavailable
			}
			if err := ctx.Err(); err != nil {
				s.mu.Unlock()
				return nil, err
			}
			replacement := newSessionReservation(h.key)
			replacement.admitted, replacement.library, replacement.profile = true, b.library, b.profile
			replacement.binders, h.binders = 1, h.binders-1
			if h.binders == 0 {
				delete(s.failedHeads, h)
			}
			b.session, s.sessions[h.key] = replacement, replacement
			h = replacement
		} else if !b.validLocked() {
			s.mu.Unlock()
			return nil, ErrSessionEvicted
		}
		if err := ctx.Err(); err != nil {
			s.mu.Unlock()
			return nil, err
		}
		other := h.setup
		if other == nil {
			own := &setupOperation{done: make(chan struct{}), source: source, shape: shape}
			h.setup = own
			s.preparing++
			s.mu.Unlock()
			return own, nil
		}
		s.mu.Unlock()
		select {
		case <-h.cutoffCtx.Done():
			return nil, ErrSessionEvicted
		case <-ctx.Done():
			if err := b.Check(); err != nil {
				return nil, err
			}
			return nil, ctx.Err()
		case <-other.done:
			if err := b.Check(); err != nil {
				return nil, err
			}
			if other.err != nil {
				if errors.Is(other.err, context.Canceled) || errors.Is(other.err, context.DeadlineExceeded) {
					continue
				}
				if other.compared && other.source.generation != "" && source.generation != "" {
					if other.source.matches(source) && other.shape.matches(shape) {
						return nil, other.err
					}
					// A verified mismatch can retry against this exact head.
					continue
				}
				return nil, errSetupUnavailable
			}
		}
	}
}

func (b *LiveBinding) publishSetupPicture(claim *setupOperation, picture *ComposedPicture) error {
	s := b.manager
	s.mu.Lock()
	defer s.mu.Unlock()
	if !b.validLocked() || b.session.setup != claim {
		return ErrSessionEvicted
	}
	claim.picture, claim.compared = picture, true
	return nil
}

func (b *LiveBinding) finishSetup(h *hlsSession, claim *setupOperation, err error) {
	s := b.manager
	s.mu.Lock()
	defer s.mu.Unlock()
	b.recordSetupLocked(h, claim, err)
	s.preparing--
	s.wakeCloseLocked()
}

func (b *LiveBinding) recordSetupLocked(h *hlsSession, claim *setupOperation, err error) {
	if claim == nil || claim.finished {
		return
	}
	claim.err, claim.finished = err, true
	h.setup = nil
	if err != nil && h.state == sessionStarting &&
		!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		h.state, h.failedSetup = sessionFailed, claim
		s := b.manager
		if s.failedHeads == nil {
			s.failedHeads = make(map[*hlsSession]struct{})
		}
		s.failedHeads[h] = struct{}{}
	}
	close(claim.done)
}

type sourceCapture struct {
	path       string
	info       os.FileInfo
	generation string
}

func captureSource(path string, secure *EncryptedSource, stat func(string) (os.FileInfo, error)) sourceCapture {
	c := sourceCapture{path: path}
	if secure != nil {
		c.generation = store.EncryptedSourceGeneration(secure.Fingerprint)
	} else {
		if stat == nil {
			stat = os.Stat
		}
		c.info, _ = stat(path)
		c.generation = store.SourceFileGeneration(c.info)
	}
	return c
}

func (c sourceCapture) matches(other sourceCapture) bool {
	return c.path == other.path && c.generation == other.generation &&
		(c.info == nil && other.info == nil || sameSourceFile(c.info, other.info))
}

func (h *hlsSession) sourceCapture() sourceCapture {
	return sourceCapture{h.src, h.sourceInfo, h.sourceGeneration}
}

// Preparation builds the immutable shape off to the side. Initial promotion
// preserves the reservation identity held by existing binders.
func promoteReservation(reservation, prepared *hlsSession) *hlsSession {
	reservation.predecessorWait = prepared.predecessorWait
	reservation.dir = prepared.dir
	reservation.scratch = prepared.scratch
	reservation.src = prepared.src
	reservation.sourceInfo = prepared.sourceInfo
	reservation.sourceGeneration = prepared.sourceGeneration
	reservation.sourceDurationSeconds = prepared.sourceDurationSeconds
	reservation.durationSeconds = prepared.durationSeconds
	reservation.timeline = prepared.timeline
	reservation.rungs = prepared.rungs
	reservation.audioProfile = prepared.audioProfile
	reservation.audioStreamIndex = prepared.audioStreamIndex
	reservation.sourceVideo = prepared.sourceVideo
	reservation.burnSubtitleIndex = prepared.burnSubtitleIndex
	reservation.audioSource = prepared.audioSource
	reservation.primed = prepared.primed
	reservation.priority = prepared.priority
	reservation.degraded = prepared.degraded
	reservation.adjust = prepared.adjust
	reservation.secure = prepared.secure
	reservation.live = prepared.live
	reservation.encodePolicy = prepared.encodePolicy
	return reservation
}
