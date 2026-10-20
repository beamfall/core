package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type PlaybackRenditionID string
type PlaybackRenditionKind string
type PlaybackRenditionStatus string

const (
	PlaybackRenditionKindHLS PlaybackRenditionKind = "hls"

	PlaybackRenditionGenerating PlaybackRenditionStatus = "generating"
	PlaybackRenditionReady      PlaybackRenditionStatus = "ready"
	PlaybackRenditionFailed     PlaybackRenditionStatus = "failed"
	PlaybackRenditionEvicted    PlaybackRenditionStatus = "evicted"
)

var (
	ErrPlaybackRenditionGenerationStale = errors.New("playback rendition generation is stale")
	ErrPlaybackRenditionInputStale      = errors.New("playback rendition input is stale")
	ErrPlaybackRenditionNotFound        = errors.New("playback rendition not found")
)

// PlaybackRendition is a disposable server-derived playback cache object,
// never a user-owned Media File Version.
type PlaybackRendition struct {
	ID                 PlaybackRenditionID
	MediaID            MediaID
	ContentFingerprint string
	Kind               PlaybackRenditionKind
	Status             PlaybackRenditionStatus
	RungID             string
	Codec              string
	Height             int
	Bandwidth          int
	Path               string
	SizeBytes          int64
	CreatedAt          time.Time
	LastUsedAt         time.Time
}

func validatePlaybackRendition(r PlaybackRendition) error {
	if r.ID == "" {
		return errors.New("playback rendition ID is required")
	}
	if r.MediaID == "" {
		return errors.New("media ID is required")
	}
	if r.ContentFingerprint == "" {
		return errors.New("content fingerprint is required")
	}
	if r.Kind != PlaybackRenditionKindHLS {
		return errors.New("unknown playback rendition kind")
	}
	switch r.Status {
	case PlaybackRenditionGenerating, PlaybackRenditionReady, PlaybackRenditionFailed, PlaybackRenditionEvicted:
	default:
		return errors.New("unknown playback rendition status")
	}
	if r.RungID == "" {
		return errors.New("playback rendition rung is required")
	}
	if r.Codec == "" {
		return errors.New("playback rendition codec is required")
	}
	if r.Height < 0 || r.Bandwidth < 0 || r.SizeBytes < 0 {
		return errors.New("playback rendition numeric fields must be non-negative")
	}
	if r.Path == "" {
		return errors.New("playback rendition path is required")
	}
	if r.CreatedAt.IsZero() || r.LastUsedAt.IsZero() {
		return errors.New("playback rendition timestamps are required")
	}
	return nil
}

func (s *Store) UpsertPlaybackRendition(ctx context.Context, r PlaybackRendition) error {
	if err := validatePlaybackRendition(r); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if exists, err := s.mediaExistsSQL(ctx, r.MediaID); err != nil {
		return err
	} else if !exists {
		return errors.New("media not found")
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO playback_rendition(
			id, media_id, content_fingerprint, kind, status, rung_id, codec,
			height, bandwidth, path, size_bytes, created_at_ms, last_used_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			media_id = excluded.media_id,
			content_fingerprint = excluded.content_fingerprint,
			kind = excluded.kind,
			status = excluded.status,
			rung_id = excluded.rung_id,
			codec = excluded.codec,
			height = excluded.height,
			bandwidth = excluded.bandwidth,
			path = excluded.path,
			size_bytes = excluded.size_bytes,
			created_at_ms = excluded.created_at_ms,
			last_used_at_ms = excluded.last_used_at_ms
	`, r.ID, r.MediaID, r.ContentFingerprint, r.Kind, r.Status, r.RungID, r.Codec, r.Height, r.Bandwidth, r.Path, r.SizeBytes, timeToMillis(r.CreatedAt.UTC()), timeToMillis(r.LastUsedAt.UTC()))
	return err
}

// StartPlaybackRenditionGeneration installs one monotonic publication epoch.
// It returns the compare-and-swap token every later state transition must carry
// plus the prior artifact path for post-publication cleanup. Concurrent or
// replacement starts serialize in SQLite; even equal or backwards caller
// clocks advance the existing epoch by one.
func (s *Store) StartPlaybackRenditionGeneration(ctx context.Context, r PlaybackRendition, jobID JobID, leaseOwner string) (int64, string, error) {
	if r.Status != PlaybackRenditionGenerating {
		return 0, "", errors.New("playback rendition generation must start as generating")
	}
	if err := validatePlaybackRendition(r); err != nil {
		return 0, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return 0, "", err
	}
	if err := s.verifyPlaybackRenditionJobLeaseLocked(ctx, jobID, leaseOwner); err != nil {
		return 0, "", err
	}
	if exists, err := s.mediaExistsSQL(ctx, r.MediaID); err != nil {
		return 0, "", err
	} else if !exists {
		return 0, "", errors.New("media not found")
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return 0, "", err
	}
	defer tx.Rollback()

	generation := timeToMillis(r.CreatedAt.UTC())
	var previous int64
	var previousPath string
	err = tx.QueryRowContext(ctx, `SELECT created_at_ms, path FROM playback_rendition WHERE id = ?`, r.ID).Scan(&previous, &previousPath)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, "", err
	}
	if generation <= previous {
		generation = previous + 1
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO playback_rendition(
			id, media_id, content_fingerprint, kind, status, rung_id, codec,
			height, bandwidth, path, size_bytes, created_at_ms, last_used_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			media_id = excluded.media_id,
			content_fingerprint = excluded.content_fingerprint,
			kind = excluded.kind,
			status = excluded.status,
			rung_id = excluded.rung_id,
			codec = excluded.codec,
			height = excluded.height,
			bandwidth = excluded.bandwidth,
			size_bytes = 0,
			created_at_ms = excluded.created_at_ms,
			last_used_at_ms = excluded.last_used_at_ms
	`, r.ID, r.MediaID, r.ContentFingerprint, r.Kind, r.Status, r.RungID, r.Codec,
		r.Height, r.Bandwidth, r.Path, generation, generation)
	if err != nil {
		return 0, "", err
	}
	if err := tx.Commit(); err != nil {
		return 0, "", err
	}
	return generation, previousPath, nil
}

// PublishPlaybackRenditionGeneration makes a verified immutable artifact
// visible only while its job lease, source input, and rendition epoch are
// current. The exclusive Store lock linearizes source replacement with the
// source predicate and ready-row CAS in one catalog transaction.
func (s *Store) PublishPlaybackRenditionGeneration(ctx context.Context, r PlaybackRendition, sourcePath string, generation int64, jobID JobID, leaseOwner string) error {
	if r.Status != PlaybackRenditionReady {
		return errors.New("playback rendition publication must be ready")
	}
	if err := validatePlaybackRendition(r); err != nil {
		return err
	}
	if generation <= 0 {
		return errors.New("playback rendition generation is required")
	}
	if strings.TrimSpace(sourcePath) == "" {
		return errors.New("playback rendition source path is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if err := s.verifyPlaybackRenditionJobLeaseLocked(ctx, jobID, leaseOwner); err != nil {
		return err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var sourceExists int
	err = tx.QueryRowContext(ctx, `
		SELECT 1 FROM media_file
		WHERE media_id = ? AND content_fingerprint = ? AND path = ? AND missing_at_ms IS NULL
	`, r.MediaID, r.ContentFingerprint, sourcePath).Scan(&sourceExists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPlaybackRenditionInputStale
	}
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE playback_rendition
		SET status = ?, path = ?, size_bytes = ?, last_used_at_ms = ?
		WHERE id = ? AND status = ? AND created_at_ms = ? AND content_fingerprint = ?
	`, PlaybackRenditionReady, r.Path, r.SizeBytes, timeToMillis(r.LastUsedAt.UTC()),
		r.ID, PlaybackRenditionGenerating, generation, r.ContentFingerprint)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrPlaybackRenditionGenerationStale
	}
	return tx.Commit()
}

// verifyPlaybackRenditionJobLeaseLocked is the lease half of the precompute
// publication transaction. Callers hold s.mu exclusively, which serializes the
// events-database lease predicate with CancelJob/lease recovery and the catalog
// generation write even though the two SQLite files cannot share a transaction.
func (s *Store) verifyPlaybackRenditionJobLeaseLocked(ctx context.Context, id JobID, leaseOwner string) error {
	if id == "" {
		return errors.New("job ID is required")
	}
	leaseOwner = strings.TrimSpace(leaseOwner)
	if leaseOwner == "" {
		return errors.New("job lease owner is required")
	}
	var one int
	err := s.eventsR.QueryRowContext(ctx,
		`SELECT 1 FROM job WHERE id = ?`+jobLiveLeaseClause,
		id, leaseOwner, timeToMillis(leaseNow()),
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrJobLeaseLost
	}
	return err
}

// FailPlaybackRenditionGeneration fails only the named epoch. A stale cleanup
// can therefore never overwrite a replacement generation's state.
func (s *Store) FailPlaybackRenditionGeneration(ctx context.Context, id PlaybackRenditionID, generation int64, now time.Time) error {
	if id == "" || generation <= 0 {
		return errors.New("playback rendition ID and generation are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		UPDATE playback_rendition
		SET status = ?, size_bytes = 0, last_used_at_ms = ?
		WHERE id = ? AND status = ? AND created_at_ms = ?
	`, PlaybackRenditionFailed, timeToMillis(now.UTC()), id, PlaybackRenditionGenerating, generation)
	return err
}

// ReadyPlaybackRenditionPaths returns the immutable generation directories
// referenced by ready rows. Startup residue recovery uses this allowlist and
// never trusts unreferenced directory names.
func (s *Store) ReadyPlaybackRenditionPaths(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `SELECT path FROM playback_rendition WHERE status = ?`, PlaybackRenditionReady)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, rows.Err()
}

func (s *Store) ReadyPlaybackRenditions(ctx context.Context, mediaID MediaID, filter LibraryFilter) ([]PlaybackRendition, error) {
	if mediaID == "" {
		return nil, errors.New("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	if _, err := s.visibleMediaSQL(ctx, mediaID, filter); err != nil {
		return nil, nil
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT id, media_id, content_fingerprint, kind, status, rung_id, codec,
		       height, bandwidth, path, size_bytes, created_at_ms, last_used_at_ms
		FROM playback_rendition
		WHERE media_id = ? AND status = ?
		ORDER BY height DESC, bandwidth DESC, id
	`, mediaID, PlaybackRenditionReady)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPlaybackRenditions(rows)
}

func (s *Store) PlaybackRendition(ctx context.Context, id PlaybackRenditionID, filter LibraryFilter) (PlaybackRendition, error) {
	if id == "" {
		return PlaybackRendition{}, errors.New("playback rendition ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PlaybackRendition{}, err
	}
	r, err := s.playbackRenditionSQL(ctx, id)
	if err != nil {
		return PlaybackRendition{}, err
	}
	if _, err := s.visibleMediaSQL(ctx, r.MediaID, filter); err != nil {
		return PlaybackRendition{}, err
	}
	return r, nil
}

func (s *Store) RecordPlaybackRenditionUse(ctx context.Context, id PlaybackRenditionID, now time.Time) error {
	if id == "" {
		return errors.New("playback rendition ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx,
		`UPDATE playback_rendition SET last_used_at_ms = ? WHERE id = ?`,
		timeToMillis(now.UTC()), id,
	)
	return err
}

// PlaybackRenditionCacheStats is the cache-health snapshot ADR-0057 requires
// the Console to surface: how many ready precomputed-rendition cache objects
// exist and the total disk they occupy. It is derived purely from the cache
// rows (server-owned, disposable), never from user Library Versions.
type PlaybackRenditionCacheStats struct {
	Ready      int
	Generating int
	Failed     int
	Evicted    int
	ReadyBytes int64
}

// PlaybackRenditionCacheHealth reports the precomputed-rendition cache health
// across every Media (ADR-0057 §"cache health visibility"): per-status counts
// plus the on-disk bytes the ready cache objects occupy, the input the
// disk-budget eviction governs. Cache objects are not Reveal-filtered because
// they are server-owned derivatives, not Library content — but the operator
// surface that calls this is Admin-Session gated.
func (s *Store) PlaybackRenditionCacheHealth(ctx context.Context) (PlaybackRenditionCacheStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PlaybackRenditionCacheStats{}, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT status, COUNT(*), COALESCE(SUM(size_bytes), 0)
		FROM playback_rendition
		GROUP BY status
	`)
	if err != nil {
		return PlaybackRenditionCacheStats{}, err
	}
	defer rows.Close()
	var stats PlaybackRenditionCacheStats
	for rows.Next() {
		var status PlaybackRenditionStatus
		var count int
		var bytes int64
		if err := rows.Scan(&status, &count, &bytes); err != nil {
			return PlaybackRenditionCacheStats{}, err
		}
		switch status {
		case PlaybackRenditionReady:
			stats.Ready = count
			stats.ReadyBytes = bytes
		case PlaybackRenditionGenerating:
			stats.Generating = count
		case PlaybackRenditionFailed:
			stats.Failed = count
		case PlaybackRenditionEvicted:
			stats.Evicted = count
		}
	}
	return stats, rows.Err()
}

func (s *Store) EvictPlaybackRenditionsOverBudget(ctx context.Context, maxBytes int64, now time.Time) ([]PlaybackRendition, error) {
	if maxBytes < 0 {
		return nil, errors.New("playback rendition budget must be non-negative")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(size_bytes), 0) FROM playback_rendition WHERE status = ?`, PlaybackRenditionReady).Scan(&total); err != nil {
		return nil, err
	}
	if total <= maxBytes {
		return nil, tx.Commit()
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, media_id, content_fingerprint, kind, status, rung_id, codec,
		       height, bandwidth, path, size_bytes, created_at_ms, last_used_at_ms
		FROM playback_rendition
		WHERE status = ?
		ORDER BY last_used_at_ms, created_at_ms, id
	`, PlaybackRenditionReady)
	if err != nil {
		return nil, err
	}
	candidates, err := scanPlaybackRenditions(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	var evicted []PlaybackRendition
	for _, r := range candidates {
		if total <= maxBytes {
			break
		}
		total -= r.SizeBytes
		r.Status = PlaybackRenditionEvicted
		r.LastUsedAt = now.UTC()
		evicted = append(evicted, r)
		if _, err := tx.ExecContext(ctx, `UPDATE playback_rendition SET status = ?, last_used_at_ms = ? WHERE id = ?`, PlaybackRenditionEvicted, timeToMillis(now.UTC()), r.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return evicted, nil
}

func (s *Store) playbackRenditionSQL(ctx context.Context, id PlaybackRenditionID) (PlaybackRendition, error) {
	row := s.catalogR.QueryRowContext(ctx, `
		SELECT id, media_id, content_fingerprint, kind, status, rung_id, codec,
		       height, bandwidth, path, size_bytes, created_at_ms, last_used_at_ms
		FROM playback_rendition
		WHERE id = ?
	`, id)
	r, err := scanPlaybackRendition(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PlaybackRendition{}, ErrPlaybackRenditionNotFound
	}
	return r, err
}

type playbackRenditionScanner interface {
	Scan(dest ...any) error
}

func scanPlaybackRendition(scanner playbackRenditionScanner) (PlaybackRendition, error) {
	var r PlaybackRendition
	var createdMS, lastUsedMS int64
	err := scanner.Scan(&r.ID, &r.MediaID, &r.ContentFingerprint, &r.Kind, &r.Status, &r.RungID, &r.Codec, &r.Height, &r.Bandwidth, &r.Path, &r.SizeBytes, &createdMS, &lastUsedMS)
	if err != nil {
		return PlaybackRendition{}, err
	}
	r.CreatedAt = millisToTime(createdMS)
	r.LastUsedAt = millisToTime(lastUsedMS)
	return r, nil
}

func scanPlaybackRenditions(rows *sql.Rows) ([]PlaybackRendition, error) {
	var out []PlaybackRendition
	for rows.Next() {
		r, err := scanPlaybackRendition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
