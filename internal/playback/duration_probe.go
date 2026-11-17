package playback

import (
	"context"
	"sync"
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

type durationProbeEntry struct {
	seconds int
	at      time.Time
}

// durationProbeCache remembers on-demand duration probes by content
// fingerprint. Its zero value is ready to use.
type durationProbeCache struct {
	mu      sync.Mutex
	entries map[string]durationProbeEntry
}

// ProbedDurationSeconds returns the runtime of the file at path, probing it on
// demand when the stored Media has none (CORE-2314). A Media imported before
// its probe could read a duration has DurationSeconds 0, and a session for it
// would get a growing playlist that ends where the encoder currently is. The
// result is cached by content fingerprint, so a playback that asks once per
// request probes the file once. It returns 0 when the transcoder cannot probe,
// the probe fails or the file declares no duration.
//
// Callers never pass an Encrypted Library's file: its bytes on disk are
// ciphertext and are never probed in place.
func (s *Sessions) ProbedDurationSeconds(ctx context.Context, fingerprint, path string) int {
	if s == nil || fingerprint == "" || path == "" {
		return 0
	}
	prober, ok := s.transcoder.(durationProber)
	if !ok {
		return 0
	}
	now := s.now()
	if seconds, ok := s.durationProbes.lookup(fingerprint, now); ok {
		return seconds
	}
	probeCtx, cancel := context.WithTimeout(ctx, durationProbeTimeout)
	defer cancel()
	seconds := 0
	if probe, err := prober.Probe(probeCtx, path); err == nil && probe.DurationSeconds > 0 {
		seconds = probe.DurationSeconds
	}
	if seconds == 0 && ctx.Err() != nil {
		// The request went away; that says nothing about the file.
		return 0
	}
	s.durationProbes.remember(fingerprint, seconds, now)
	return seconds
}

func (c *durationProbeCache) lookup(fingerprint string, now time.Time) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[fingerprint]
	if !ok {
		return 0, false
	}
	if entry.seconds == 0 && now.Sub(entry.at) >= durationProbeMissTTL {
		delete(c.entries, fingerprint)
		return 0, false
	}
	return entry.seconds, true
}

func (c *durationProbeCache) remember(fingerprint string, seconds int, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil || len(c.entries) >= durationProbeCacheLimit {
		c.entries = make(map[string]durationProbeEntry)
	}
	c.entries[fingerprint] = durationProbeEntry{seconds: seconds, at: now}
}

// probedDuration is the on-demand runtime of a video or audio Media's
// negotiated file, or 0. An Encrypted Library's file is never probed.
func (s *Service) probedDuration(ctx context.Context, media store.Media, file store.MediaFile) int {
	if s.sessions == nil || media.Class != store.MediaClassVideo && media.Class != store.MediaClassAudio {
		return 0
	}
	encrypted, err := s.store.LibraryEncrypted(ctx, media.LibraryID)
	if err != nil || encrypted {
		return 0
	}
	return s.sessions.ProbedDurationSeconds(ctx, file.ContentFingerprint, file.Path)
}
