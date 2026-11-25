package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// MediaMetadataProvenance names the plugin scraper whose identification last
// supplied a Media item's metadata. Readers resolve the scraper's declared
// attribution from the active bundle; the store keeps only the identifiers.
type MediaMetadataProvenance struct {
	PluginID  string
	ScraperID string
	// CandidateID is the provider candidate the identification applied, so a
	// per-item refresh can re-run the same lookup. It is "" for records
	// written before candidates were kept.
	CandidateID string
}

// SetMediaMetadataProvenance records which plugin scraper supplied a Media
// item's metadata and the candidate it applied, replacing any earlier record.
// Deleting the Media row deletes the record (ON DELETE CASCADE).
func (s *Store) SetMediaMetadataProvenance(ctx context.Context, mediaID MediaID, pluginID, scraperID, candidateID string) error {
	if mediaID == "" {
		return ValidationError("media ID is required")
	}
	provenance, err := normalizeMediaMetadataProvenance(MediaMetadataProvenance{
		PluginID: pluginID, ScraperID: scraperID, CandidateID: candidateID,
	})
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if err != nil {
		return err
	}
	return upsertMediaMetadataProvenance(ctx, plane.writer, mediaID, provenance)
}

func normalizeMediaMetadataProvenance(provenance MediaMetadataProvenance) (MediaMetadataProvenance, error) {
	provenance.PluginID = strings.TrimSpace(provenance.PluginID)
	provenance.ScraperID = strings.TrimSpace(provenance.ScraperID)
	provenance.CandidateID = strings.TrimSpace(provenance.CandidateID)
	if provenance.PluginID == "" || provenance.ScraperID == "" {
		return MediaMetadataProvenance{}, ValidationError("plugin and scraper IDs are required")
	}
	return provenance, nil
}

func upsertMediaMetadataProvenance(ctx context.Context, db sqlExecer, mediaID MediaID, provenance MediaMetadataProvenance) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO media_metadata_provenance(media_id, plugin_id, scraper_id, candidate_id, updated_at_ms)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(media_id) DO UPDATE SET
			plugin_id = excluded.plugin_id,
			scraper_id = excluded.scraper_id,
			candidate_id = excluded.candidate_id,
			updated_at_ms = excluded.updated_at_ms`,
		mediaID, provenance.PluginID, provenance.ScraperID, provenance.CandidateID, timeToMillis(time.Now().UTC()))
	return err
}

// ApplyMediaIdentification commits the replacement metadata, selected lock
// clears, candidate provenance and plugin fields together at operator
// precedence on the Media's own plane, as one unreplayable identification
// revision (CommitMediaIdentification with a fresh request ID).
func (s *Store) ApplyMediaIdentification(ctx context.Context, id MediaID, metadata MediaMetadata, provenance MediaMetadataProvenance, fields []MediaField) error {
	_, err := s.CommitMediaIdentification(ctx, id, IdentificationCommit{Metadata: metadata, Provenance: provenance, Fields: fields})
	return err
}

// MediaMetadataProvenance returns the recorded metadata provenance for a Media
// item; ok is false when none has been recorded. Callers gate visibility of
// the Media item itself before reading.
func (s *Store) MediaMetadataProvenance(ctx context.Context, mediaID MediaID) (MediaMetadataProvenance, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return MediaMetadataProvenance{}, false, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if errors.Is(err, errPlaneMediaNotFound) {
		return MediaMetadataProvenance{}, false, nil
	}
	if err != nil {
		return MediaMetadataProvenance{}, false, err
	}
	var record MediaMetadataProvenance
	err = plane.reader.QueryRowContext(ctx,
		`SELECT plugin_id, scraper_id, candidate_id FROM media_metadata_provenance WHERE media_id = ?`, mediaID).
		Scan(&record.PluginID, &record.ScraperID, &record.CandidateID)
	if errors.Is(err, sql.ErrNoRows) {
		return MediaMetadataProvenance{}, false, nil
	}
	if err != nil {
		return MediaMetadataProvenance{}, false, err
	}
	return record, true, nil
}
