package store

import (
	"context"
	"errors"
	"time"
)

// Sonic analysis store surface (CORE-3073, OpenSpec core-1974-sonic-
// similarity). A Library opts in to having its audio-class Media analysed for
// a sonic feature vector; the vectors live in the ADR-0041 media_embedding
// table under SonicEmbeddingKind and SimilarMedia reads them. Analysis is
// local only and default OFF, a toggle separate from AI indexing (owner
// decision D3, CORE-3070).

// SonicEmbeddingKind is the media_embedding kind the sonic analyser writes:
// the versioned feature layout Oscillux's sonic profile defines. A new layout
// takes a new kind, so vectors of different layouts are never compared.
const SonicEmbeddingKind EmbeddingKind = "sonic-v1"

// sonicAnalysisLibraryKeyPrefix namespaces the per-Library toggle inside the
// shared server_config table, like the skip-detection toggle: an absent row is
// the default (OFF), and the frozen library schema stays untouched.
const sonicAnalysisLibraryKeyPrefix = "sonicanalysis.library."

func sonicAnalysisLibraryKey(id LibraryID) string {
	return sonicAnalysisLibraryKeyPrefix + string(id)
}

// SonicAnalysisEnabled reports whether the Library has opted in to sonic
// analysis. An absent row, or any value other than "1", means it has not.
func (s *Store) SonicAnalysisEnabled(ctx context.Context, id LibraryID) (bool, error) {
	if id == "" {
		return false, ValidationError("library ID is required")
	}
	values, err := s.serverConfigValues(ctx)
	if err != nil {
		return false, err
	}
	return values[sonicAnalysisLibraryKey(id)] == "1", nil
}

// SetSonicAnalysisEnabled flips the per-Library toggle. Turning it off stops
// further analysis but keeps vectors already written; they are content-keyed
// derived data, harmless at rest. The Library must exist.
func (s *Store) SetSonicAnalysisEnabled(ctx context.Context, id LibraryID, enabled bool, now time.Time) error {
	if id == "" {
		return ValidationError("library ID is required")
	}
	if err := s.libraryExists(ctx, id); err != nil {
		return err
	}
	return s.setServerConfigValues(ctx, map[string]string{
		sonicAnalysisLibraryKey(id): boolToConfig(enabled),
	}, now)
}

// SonicAnalysisCandidatesAfter returns, in Media ID order after the cursor,
// the live audio-class Media in sonic-enabled Libraries that have no live file
// with a vector of the given kind yet. It is a server-internal work list for
// the analysis job, not a viewer read. It reads the shared catalog only, so an
// Encrypted Library's Media, which live in that Library's own database, are
// never candidates and their derived vectors never reach the shared plane.
func (s *Store) SonicAnalysisCandidatesAfter(ctx context.Context, kind EmbeddingKind, after MediaID, limit int) ([]MediaID, error) {
	if kind == "" {
		return nil, ValidationError("embedding kind is required")
	}
	if limit <= 0 {
		return nil, errors.New("limit must be positive")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT m.id
		FROM media m
		JOIN library l ON l.id = m.library_id
		JOIN server_config c ON c.key = ? || m.library_id AND c.value = '1'
		WHERE m.id > ?
		  AND m.trashed_at_ms IS NULL
		  AND l.trashed_at_ms IS NULL
		  AND m.media_class = ?
		  AND EXISTS (
			SELECT 1 FROM media_file f
			WHERE f.media_id = m.id AND f.missing_at_ms IS NULL AND f.content_fingerprint != ''
		  )
		  AND NOT EXISTS (
			SELECT 1 FROM media_file f
			JOIN media_embedding e
			  ON e.media_fingerprint = f.content_fingerprint AND e.embedding_kind = ?
			WHERE f.media_id = m.id AND f.missing_at_ms IS NULL
		  )
		ORDER BY m.id
		LIMIT ?
	`, sonicAnalysisLibraryKeyPrefix, string(after), string(MediaClassAudio), string(kind), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MediaID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, MediaID(id))
	}
	return out, rows.Err()
}
