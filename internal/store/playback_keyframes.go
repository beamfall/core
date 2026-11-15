package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PlaybackKeyframes is a source file's probed video keyframe index, cached for
// audio-copy HLS (CORE-0021). SizeBytes and ModTimeNs name the file generation
// it was probed from; Keyframes are offsets from the container start,
// ascending.
type PlaybackKeyframes struct {
	Path      string
	SizeBytes int64
	ModTimeNs int64
	Duration  time.Duration
	Keyframes []time.Duration
}

// PlaybackKeyframes returns the cached keyframe index for path when it was
// probed from the same file generation (size and modification time); ok is
// false when there is no row or the file has changed since.
func (s *Store) PlaybackKeyframes(ctx context.Context, path string, sizeBytes, modTimeNs int64) (PlaybackKeyframes, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PlaybackKeyframes{}, false, err
	}
	var (
		durationUs int64
		encoded    string
	)
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT duration_us, keyframes_us FROM playback_keyframes
		WHERE path = ? AND size_bytes = ? AND mod_time_ns = ?`,
		path, sizeBytes, modTimeNs).Scan(&durationUs, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return PlaybackKeyframes{}, false, nil
	}
	if err != nil {
		return PlaybackKeyframes{}, false, err
	}
	var micros []int64
	if err := json.Unmarshal([]byte(encoded), &micros); err != nil {
		return PlaybackKeyframes{}, false, fmt.Errorf("decode playback keyframes for %q: %w", path, err)
	}
	keyframes := make([]time.Duration, len(micros))
	for i, us := range micros {
		keyframes[i] = time.Duration(us) * time.Microsecond
	}
	return PlaybackKeyframes{
		Path:      path,
		SizeBytes: sizeBytes,
		ModTimeNs: modTimeNs,
		Duration:  time.Duration(durationUs) * time.Microsecond,
		Keyframes: keyframes,
	}, true, nil
}

// PutPlaybackKeyframes records a probed keyframe index, replacing any earlier
// index for the same path.
func (s *Store) PutPlaybackKeyframes(ctx context.Context, index PlaybackKeyframes) error {
	if index.Path == "" {
		return ValidationError("path is required")
	}
	if index.Duration <= 0 || len(index.Keyframes) == 0 {
		return ValidationError("a keyframe index needs a duration and at least one keyframe")
	}
	micros := make([]int64, len(index.Keyframes))
	for i, kf := range index.Keyframes {
		if kf < 0 || (i > 0 && kf <= index.Keyframes[i-1]) {
			return ValidationError("keyframes must be non-negative and strictly ascending")
		}
		micros[i] = kf.Microseconds()
	}
	encoded, err := json.Marshal(micros)
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err = s.catalogW.ExecContext(ctx, `
		INSERT INTO playback_keyframes(path, size_bytes, mod_time_ns, duration_us, keyframes_us, probed_at_ms)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			size_bytes = excluded.size_bytes,
			mod_time_ns = excluded.mod_time_ns,
			duration_us = excluded.duration_us,
			keyframes_us = excluded.keyframes_us,
			probed_at_ms = excluded.probed_at_ms`,
		index.Path, index.SizeBytes, index.ModTimeNs, index.Duration.Microseconds(), string(encoded),
		timeToMillis(time.Now().UTC()))
	return err
}
