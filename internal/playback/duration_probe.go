package playback

import (
	"context"
	"errors"
	"log/slog"
	"strings"
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
	writes     map[durationWriteTarget]durationWriteState
}
type durationWriteTarget struct {
	library     store.LibraryID
	media       store.MediaID
	fingerprint string
}

type durationWriteState uint8

const (
	durationWritePending durationWriteState = iota + 1
	durationWriteVerified
	durationWriteComplete
	durationWriteMismatch
)

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
	done       chan struct{}
}

func (s *Sessions) probedSourceDuration(ctx context.Context, binding *LiveBinding, op *libraryOperation, mediaID store.MediaID, fingerprint, path string) (result int) {
	if fingerprint == "" || path == "" || ctx.Err() != nil {
		return 0
	}
	source := s.claimSource(path, nil)
	defer s.releaseSource(source)
	if source.generation == "" || !source.info.Mode().IsRegular() {
		return 0
	}
	// Legacy fingerprints require reading the whole file. Use the probed
	// runtime for playback and leave the catalog write to the next scan.
	if s.recordDuration != nil && mediaID != "" && strings.HasPrefix(fingerprint, "sample1:") {
		defer func() {
			if result > 0 {
				s.persistProbedDuration(ctx, op, source, durationWriteTarget{op.library, mediaID, fingerprint}, result)
			}
		}()
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
			if !matches || entry.seconds <= 0 {
				s.cacheDurationLocked(source, seconds, now)
			}
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
	leader := false
	if flight == nil || flight.generation != source.generation {
		flight = &durationFlight{generation: source.generation, done: make(chan struct{})}
		s.durationFlights[path] = flight
		leader = true
	}
	flight.users++
	s.mu.Unlock()

	seconds := 0
	valid := true
	if leader {
		probeCtx, cancel := context.WithTimeout(ctx, durationProbeTimeout)
		if probe, err := prober.Probe(probeCtx, path); err == nil && probe.DurationSeconds > 0 {
			seconds = probe.DurationSeconds
		}
		cancel()
		valid = source.matches(captureSource(path, nil, s.statSource))
		s.mu.Lock()
		if valid && seconds > 0 {
			flight.seconds = seconds
		}
		close(flight.done)
	} else {
		select {
		case <-flight.done:
		case <-ctx.Done():
		}
		s.mu.Lock()
		seconds = flight.seconds
	}
	defer s.mu.Unlock()
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
	}
	flight.seconds = seconds
	if seconds > 0 && retained != nil {
		retained.seconds = seconds
	}
	if entry.generation == source.generation && entry.seconds > 0 {
		return seconds
	}
	s.cacheDurationLocked(source, seconds, now)
	return seconds
}

func (s *Sessions) cacheDurationLocked(source sourceCapture, seconds int, now time.Time) {
	if s.durationProbes == nil || len(s.durationProbes) >= durationProbeCacheLimit && s.durationProbes[source.path].generation == "" {
		s.durationProbes = make(map[string]durationProbeEntry)
	}
	s.durationProbes[source.path] = durationProbeEntry{generation: source.generation, seconds: seconds, at: now}
}

func (s *Sessions) persistProbedDuration(ctx context.Context, op *libraryOperation, source sourceCapture, target durationWriteTarget, seconds int) {
	s.mu.Lock()
	entry := s.durationProbes[source.path]
	state := entry.writes[target]
	if (state != 0 && state != durationWriteVerified) || entry.generation != source.generation || ctx.Err() != nil || s.libraryOperationErrorLocked(op) != nil || source.supersededLocked() {
		s.mu.Unlock()
		return
	}
	if entry.writes == nil {
		entry.writes = make(map[durationWriteTarget]durationWriteState)
		s.durationProbes[source.path] = entry
	}
	entry.writes[target] = durationWritePending
	s.mu.Unlock()

	// Verify the bytes without holding the Sessions lock. The Library claim
	// stays held through the write, and the store rechecks catalog eligibility.
	verified := state == durationWriteVerified
	var err error
	if !verified {
		match := s.matchDurationFile
		if match == nil {
			match = store.MediaFileMatchesSampleFingerprint
		}
		verified, err = match(ctx, source.path, source.generation, target.fingerprint)
	}
	stable := ctx.Err() == nil && source.matches(captureSource(source.path, nil, s.statSource))
	var updated bool
	if err == nil && verified && stable {
		s.mu.Lock()
		current := s.libraryOperationErrorLocked(op) == nil && !source.supersededLocked() && source.owner.durationGeneration == source.generation
		s.mu.Unlock()
		if current {
			updated, err = s.recordDuration(ctx, target.library, target.media, target.fingerprint, source.path, seconds)
		}
	}
	s.mu.Lock()
	switch {
	case updated && err == nil:
		entry.writes[target] = durationWriteComplete
	case stable && verified:
		// A later request may find the same catalog file eligible for writing.
		entry.writes[target] = durationWriteVerified
	case stable && err == nil:
		entry.writes[target] = durationWriteMismatch
	default:
		delete(entry.writes, target)
	}
	s.mu.Unlock()
	if err != nil {
		slog.WarnContext(ctx, "could not record probed runtime", "media", target.media, "error", err)
	}
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
func (s *Service) probedDuration(ctx context.Context, op *libraryOperation, media store.Media, file store.MediaFile, primary bool) int {
	if s.sessions == nil || media.Class != store.MediaClassVideo && media.Class != store.MediaClassAudio {
		return 0
	}
	encrypted, err := s.store.LibraryEncrypted(ctx, media.LibraryID)
	if err != nil || encrypted {
		return 0
	}
	var mediaID store.MediaID
	if primary && file.PartOrdinal == 0 && strings.HasPrefix(file.ContentFingerprint, "sample1:") {
		multipart, err := s.store.MediaVersionHasAdditionalParts(ctx, media.ID, file.VersionLabel)
		if err == nil && !multipart {
			mediaID = media.ID
		}
	}
	return s.sessions.probedSourceDuration(ctx, nil, op, mediaID, file.ContentFingerprint, file.Path)
}
