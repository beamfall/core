package playback

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

// durationProber is the probe half of *ffmpeg.Runner. Sessions type-asserts
// its transcoder to it, so a transcoder without a prober (a test double)
// simply never probes.
type durationProber interface {
	Probe(ctx context.Context, path string) (ffmpeg.Probe, error)
}

const (
	// durationProbeTimeout bounds one on-demand probe on the playback path.
	durationProbeTimeout = 15 * time.Second
	// durationProbeMissTTL is how long a probe that found no duration (or
	// failed) is remembered before the file is probed again.
	durationProbeMissTTL = 10 * time.Minute
	// durationProbeCacheLimit caps the remembered results; the cache resets
	// when it fills.
	durationProbeCacheLimit = 4096
)

var errDurationChanged = errors.New("source duration changed during setup")

type durationProbeEntry struct {
	generation string
	seconds    int
	at         time.Time
}

type durationKey struct {
	path, generation string
}

type retainedDuration struct {
	source         sourceCapture
	seconds, users int
}

type durationFlight struct {
	generation string
	users      int
	seconds    int // winning positive held until the last overlapping probe finishes
}

func (s *Sessions) probedSourceDuration(ctx context.Context, binding *LiveBinding, op *libraryOperation, mediaID store.MediaID, fingerprint, path string) int {
	if fingerprint == "" || path == "" || ctx.Err() != nil {
		return 0
	}
	source := s.claimSource(path, nil)
	defer s.releaseSource(source)
	if source.generation == "" || !source.info.Mode().IsRegular() {
		return 0
	}
	now := s.now()
	s.mu.Lock()
	if ctx.Err() != nil || s.libraryOperationErrorLocked(op) != nil || binding != nil && !binding.validLocked() {
		s.mu.Unlock()
		return 0
	}
	entry := s.durationProbes[path]
	matches := entry.generation == source.generation
	if binding != nil {
		if binding.duration == nil || !binding.duration.source.matches(source) {
			if binding.duration != nil {
				s.releaseDurationLocked(binding.duration)
			}
			binding.duration = s.retainDurationLocked(source)
		}
	}
	owner := source.owner
	if source.supersededLocked() || source.order < owner.durationPublished && source.generation != owner.durationGeneration {
		s.mu.Unlock()
		return 0
	}
	owner.durationPublished = max(owner.durationPublished, source.order)
	owner.durationGeneration = source.generation
	if prior := s.durationFlights[path]; prior != nil && prior.generation != source.generation {
		delete(s.durationFlights, path)
	}
	if binding != nil {
		if matches && entry.seconds > 0 {
			binding.duration.seconds = entry.seconds
		}
		if binding.duration.seconds > 0 {
			seconds := binding.duration.seconds
			s.mu.Unlock()
			return seconds
		}
	}
	if matches && (entry.seconds > 0 || now.Sub(entry.at) < durationProbeMissTTL) {
		s.mu.Unlock()
		return entry.seconds
	}
	prober, ok := s.transcoder.(durationProber)
	if !ok {
		s.mu.Unlock()
		return 0
	}
	if s.durationFlights == nil {
		s.durationFlights = make(map[string]*durationFlight)
	}
	flight := s.durationFlights[path]
	if flight == nil || flight.generation != source.generation {
		flight = &durationFlight{generation: source.generation}
		s.durationFlights[path] = flight
	}
	flight.users++
	s.mu.Unlock()

	probeCtx, cancel := context.WithTimeout(ctx, durationProbeTimeout)
	defer cancel()
	seconds := 0
	if probe, err := prober.Probe(probeCtx, path); err == nil && probe.DurationSeconds > 0 {
		seconds = probe.DurationSeconds
	}
	valid := source.matches(captureSource(path, nil, s.statSource))
	// Only the first positive result for this flight writes back. Keep the
	// catalog I/O outside the Sessions lock while the Library claim is held.
	var writeBack int
	s.mu.Lock()
	defer func() {
		s.mu.Unlock()
		if writeBack > 0 && s.recordDuration != nil && mediaID != "" {
			if err := s.recordDuration(ctx, op.library, mediaID, fingerprint, path, writeBack); err != nil {
				slog.WarnContext(ctx, "could not record probed runtime", "media", mediaID, "error", err)
			}
		}
	}()
	if s.durationFlights[path] != flight {
		return 0
	}
	flight.users--
	if flight.users == 0 {
		delete(s.durationFlights, path)
	}
	if !valid || ctx.Err() != nil || s.libraryOperationErrorLocked(op) != nil || source.supersededLocked() ||
		binding != nil && !binding.validLocked() {
		return 0
	}
	entry = s.durationProbes[path]
	retained := s.durationRetained[durationKey{path, source.generation}]
	switch {
	case flight.seconds > 0:
		seconds = flight.seconds
	case retained != nil && retained.seconds > 0:
		seconds = retained.seconds
	case entry.generation == source.generation && entry.seconds > 0:
		seconds = entry.seconds
	default:
		writeBack = seconds
	}
	flight.seconds = seconds
	if seconds > 0 && retained != nil {
		retained.seconds = seconds
	}
	if entry.generation == source.generation && entry.seconds > 0 {
		return seconds
	}
	if s.durationProbes == nil || len(s.durationProbes) >= durationProbeCacheLimit && entry.generation == "" {
		s.durationProbes = make(map[string]durationProbeEntry)
	}
	s.durationProbes[path] = durationProbeEntry{source.generation, seconds, now}
	return seconds
}

func (s *Sessions) retainDurationLocked(source sourceCapture) *retainedDuration {
	key := durationKey{source.path, source.generation}
	if s.durationRetained == nil {
		s.durationRetained = make(map[durationKey]*retainedDuration)
	}
	retained := s.durationRetained[key]
	if retained == nil {
		source.owner, source.order = nil, 0
		retained = &retainedDuration{source: source}
		s.durationRetained[key] = retained
	}
	retained.users++
	return retained
}

func (s *Sessions) releaseDurationLocked(retained *retainedDuration) {
	retained.users--
	if retained.users == 0 {
		delete(s.durationRetained, durationKey{retained.source.path, retained.source.generation})
	}
}

// probedDuration is the on-demand runtime of a video or audio Media's
// negotiated file, or 0. An Encrypted Library's file is never probed.
func (s *Service) probedDuration(ctx context.Context, op *libraryOperation, media store.Media, file store.MediaFile) int {
	if s.sessions == nil || media.Class != store.MediaClassVideo && media.Class != store.MediaClassAudio {
		return 0
	}
	encrypted, err := s.store.LibraryEncrypted(ctx, media.LibraryID)
	if err != nil || encrypted {
		return 0
	}
	return s.sessions.probedSourceDuration(ctx, nil, op, media.ID, file.ContentFingerprint, file.Path)
}
