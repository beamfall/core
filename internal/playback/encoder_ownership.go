package playback

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

type encoderStart struct {
	ctx       context.Context
	session   *hlsSession
	run       *encoderRun
	done      chan struct{}
	err       error
	permitted bool
	limit     *int
}

func (s *Sessions) profileCountLocked(profile store.ProfileID, reuse *hlsSession) int {
	count := 0
	for owner := range s.charges {
		if owner != reuse && owner.profile == profile {
			count++
		}
	}
	return count
}

func (s *Sessions) activeProfileTranscodes(profile store.ProfileID, key SessionKey) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.profileCountLocked(profile, s.sessions[key])
}

func sessionSlotCost(h *hlsSession) int {
	if h == nil || len(h.rungs) == 0 {
		return 1
	}
	allLight := true
	maxHeight := 0
	for _, r := range h.rungs {
		if !r.AudioOnly && !r.CopyVideo {
			allLight = false
		}
		if r.Height > maxHeight {
			maxHeight = r.Height
		}
	}
	if allLight {
		return 0
	}
	if maxHeight >= 2160 {
		return 2
	}
	return 1
}

func (s *Sessions) reserveEncoderLocked(h *hlsSession) {
	if s.charges == nil {
		s.charges = make(map[*hlsSession]int)
	}
	s.charges[h] = sessionSlotCost(h)
	h.charged = true
}

func (s *Sessions) releaseEncoderLocked(h *hlsSession) {
	delete(s.charges, h)
	h.charged = false
	h.releaseTranscodeLeaseLocked()
}

func (s *Sessions) profileAllowsLocked(start *encoderStart) bool {
	return start.limit == nil || s.profileCountLocked(start.session.profile, start.session) < *start.limit
}

func (s *Sessions) liveStartLocked(start *encoderStart) bool {
	h := start.session
	return h.state == sessionRunning && h.start == start
}

func (s *Sessions) startErrorLocked(ctx context.Context, start *encoderStart) error {
	if !s.liveStartLocked(start) {
		return ErrSessionEvicted
	}
	return ctx.Err()
}

func (s *Sessions) admitEncoderLocked(start *encoderStart, now time.Time) (*sessionTeardown, error) {
	h := start.session
	if !s.profileAllowsLocked(start) {
		return nil, ErrTranscodeCapacity
	}
	cost := sessionSlotCost(h)
	live := s.liveCount()
	if live == 0 || live+cost <= s.cap {
		s.reserveEncoderLocked(h)
		return nil, nil
	}
	if live <= s.cap || h.degraded {
		if victim := s.preemptableLocked(h.key, h.priority, now); victim != nil {
			victim.run.transfer = start
			return s.transitionLocked(victim, detachSession{cleanupOnRelease: true}).teardown, nil
		}
	}
	if h.degraded {
		s.reserveEncoderLocked(h)
		return nil, nil
	}
	return nil, ErrTranscodeCapacity
}

func (s *Sessions) permitEncoder(ctx context.Context, start *encoderStart) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := start.session
	if err := s.startErrorLocked(ctx, start); err != nil {
		return err
	}
	if !h.charged {
		return ErrTranscodeCapacity
	}
	s.acceptEndpointFailureLocked(start.run)
	if start.run.hasReason {
		return errEncoderFailing
	}
	if start.permitted {
		return nil
	}
	start.permitted = true
	if h.transcodeLease == nil {
		h.transcodeLease = s.beginTranscode()
	}
	return nil
}

func (s *Sessions) handoffEncoder(key SessionKey, start *encoderStart, proc *ffmpeg.HLSProcess, rung Rung, segment int) {
	h, run := start.session, start.run
	if !proc.Running() {
		return
	}
	s.mu.Lock()
	run.proc = proc
	live := s.liveStartLocked(start)
	if live {
		h.run, h.proc = run, proc
		h.currentRung = rung.ID
		h.baseSegment, h.frontier = segment, segment-1
		h.started = true
		h.resumeSeconds = 0
	} else {
		run.acceptReasonLocked(exitStopped)
		if teardown := h.teardown; teardown != nil {
			teardown.lateChildReap = run.reaped
		}
	}
	h.scratch.users++
	s.mu.Unlock()
	if !live {
		proc.RequestStop()
	}
	s.reapProcess(key, h, run, rung, segment)
	if !live {
		<-run.reaped
		return
	}
	if proc.ReportsProgress() {
		go s.watchForStall(run, s.encoderStallTimeout())
	}
}

func (s *Sessions) finishEncoderStart(start *encoderStart, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := start.session
	if start.run.proc == nil && !start.run.outputOwner && h.writing == start.run {
		h.writing = nil
		close(start.run.reaped)
		close(start.run.exited)
	}
	if start.run.proc == nil && h.proc == nil {
		s.acceptEndpointFailureLocked(start.run)
		if start.run.hasReason && start.run.outcome == exitCrashed && h.state == sessionRunning {
			h.lastCrashed = true
			h.failedRuns++
			h.transcodeFailed = h.failedRuns >= encoderMaxFailedRuns
		}
		s.releaseEncoderLocked(h)
	}
	h.start = nil
	start.err = err
	s.starting--
	close(start.done)
	s.wakeCloseLocked()
}

func (run *encoderRun) acceptReasonLocked(reason encoderExit) {
	if !run.hasReason {
		run.hasReason = true
		run.outcome = reason
	}
}

func (s *Sessions) acceptEndpointFailureLocked(run *encoderRun) bool {
	if run == nil || run.live == nil {
		return false
	}
	select {
	case <-run.live.Failed():
		run.acceptReasonLocked(exitCrashed)
		return run.outcome == exitCrashed
	default:
		return false
	}
}

func (s *Sessions) childReapedLocked(run *encoderRun) {
	h := run.session
	if h.run == run {
		h.proc = nil
		h.currentRung = ""
	}
	if run.carry == nil || !s.liveStartLocked(run.carry) || run.carry.ctx.Err() != nil {
		s.releaseEncoderLocked(h)
	}
	if target := run.transfer; target != nil && s.liveStartLocked(target) && target.ctx.Err() == nil &&
		(s.liveCount() < s.cap || target.session.degraded) && s.profileAllowsLocked(target) {
		s.reserveEncoderLocked(target.session)
	}
	close(run.reaped)
}

func waitEncoderOwner(ctx context.Context, h *hlsSession, done <-chan struct{}) error {
	select {
	case <-h.cutoffCtx.Done():
		return ErrSessionEvicted
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (s *Sessions) startEncoder(ctx context.Context, key SessionKey, start *encoderStart, old *encoderRun, stopOld bool, rung Rung, segment int, isSegment bool) (*encoderRun, error) {
	h, run := start.session, start.run
	stopPreparationCancel := context.AfterFunc(ctx, func() {
		s.mu.Lock()
		unpermitted := s.liveStartLocked(start) && !start.permitted
		if unpermitted && h.proc == nil {
			// A carried child still owns the charge until its reaper sees cancellation.
			s.releaseEncoderLocked(h)
		}
		s.mu.Unlock()
		if unpermitted {
			run.cancel()
		}
	})
	defer stopPreparationCancel()
	if old != nil {
		if stopOld {
			if old.live != nil {
				old.live.Abort()
			}
			if s.stopEncoder != nil {
				go s.stop(old.proc)
			} else {
				old.proc.RequestStop()
			}
		}
		if err := waitEncoderOwner(ctx, h, old.exited); err != nil {
			return nil, err
		}
	}
	for _, done := range h.predecessorWait {
		if err := waitEncoderOwner(ctx, h, done); err != nil {
			return nil, err
		}
	}

	s.mu.Lock()
	if err := s.startErrorLocked(ctx, start); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if isSegment && h.beyondSourceEndLocked(rung, segment) {
		s.mu.Unlock()
		return nil, ErrSourceEnded
	}
	newCharge := !h.charged
	profile, loader := h.profile, h.loadProfile
	s.mu.Unlock()
	if newCharge && profile != "" && loader != nil {
		policy, err := loader(run.ctx, profile)
		if err != nil {
			if h.cutoffCtx.Err() != nil {
				return nil, ErrSessionEvicted
			}
			return nil, err
		}
		start.limit = policy.MaxConcurrentStreams
	}

	now := s.now()
	s.mu.Lock()
	if err := s.startErrorLocked(ctx, start); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if h.transcodeFailed {
		s.mu.Unlock()
		return nil, ErrTranscodeFailed
	}
	var victim *sessionTeardown
	var err error
	if !h.charged {
		victim, err = s.admitEncoderLocked(start, now)
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if victim != nil {
		go s.runTeardowns([]*sessionTeardown{victim})
		if err := waitEncoderOwner(ctx, h, victim.run.reaped); err != nil {
			return nil, err
		}
	}
	now = s.now()
	s.mu.Lock()
	if err := s.startErrorLocked(ctx, start); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if !h.charged {
		s.mu.Unlock()
		return nil, ErrTranscodeCapacity
	}
	if h.lastCrashed && !h.takeRestartLocked(now) {
		s.mu.Unlock()
		return nil, errEncoderFailing
	}
	s.mu.Unlock()

	for {
		s.mu.Lock()
		if err := s.startErrorLocked(ctx, start); err != nil {
			s.mu.Unlock()
			return nil, err
		}
		if h.inspections == 0 {
			h.writing = run
			h.artifactVersion++
			s.mu.Unlock()
			break
		}
		done := h.inspectionDone
		s.mu.Unlock()
		if err := waitEncoderOwner(ctx, h, done); err != nil {
			return nil, err
		}
	}
	if h.durationSeconds > 0 && h.keyframeTimeline(rung) == nil {
		segment = h.primed.restartSegment(segment)
	}
	// A request abandoning its wait must not kill an encoder that already
	// serves the session. The run context lifetime belongs to the instance.
	handoff := func(proc *ffmpeg.HLSProcess) { s.handoffEncoder(key, start, proc, rung, segment) }
	spawnCtx := ffmpeg.WithPlaybackSpawn(run.ctx, func() error { return s.permitEncoder(ctx, start) }, handoff)
	if _, owned := s.transcoder.(*ffmpeg.Runner); !owned && h.secure == nil {
		if err := s.permitEncoder(ctx, start); err != nil {
			return nil, err
		}
	}
	proc, err := s.launchEncoder(spawnCtx, start, rung, segment)
	if err == nil && proc.Running() && run.proc == nil {
		handoff(proc)
	}
	if run.proc == nil {
		s.settleUnspawned(run)
	}
	s.mu.Lock()
	live := s.liveStartLocked(start)
	canceled := !start.permitted && ctx.Err() != nil
	s.mu.Unlock()
	if !live {
		return nil, ErrSessionEvicted
	}
	if canceled {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	if run.proc == nil {
		s.mu.Lock()
		if s.liveStartLocked(start) {
			h.started = true
			h.resumeSeconds = 0
		}
		s.mu.Unlock()
		return nil, nil
	}
	return run, nil
}

// settleUnspawned releases capacity immediately. An endpoint can still own
// a handler and the same-directory writer barrier after a childless launch.
func (s *Sessions) settleUnspawned(run *encoderRun) {
	h := run.session
	s.mu.Lock()
	s.acceptEndpointFailureLocked(run)
	s.releaseEncoderLocked(h)
	live := run.live
	if live != nil {
		run.outputOwner = true
		if h.state == sessionRunning {
			h.run = run
		}
		h.scratch.users++
		close(run.reaped)
	}
	s.mu.Unlock()
	if live == nil {
		run.cancel()
		return
	}
	live.Abort()
	go func() {
		defer s.releaseScratch(h.scratch)
		defer run.cancel()
		live.Revoke()
		s.mu.Lock()
		if h.writing == run {
			h.writing = nil
		}
		close(run.exited)
		s.mu.Unlock()
	}()
}
