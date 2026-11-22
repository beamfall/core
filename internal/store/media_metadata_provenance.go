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
	if ok, err := s.mediaExistsSQL(ctx, mediaID); err != nil {
		return err
	} else if !ok {
		return ValidationError("media not found")
	}
	return upsertMediaMetadataProvenance(ctx, s.catalogW, mediaID, provenance)
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
// clears, candidate provenance and plugin fields together at operator precedence.
// Identification is supported on the shared catalog, which owns provenance.
func (s *Store) ApplyMediaIdentification(ctx context.Context, id MediaID, metadata MediaMetadata, provenance MediaMetadataProvenance, fields []MediaField) error {
	if err := validateMediaMetadata(id, metadata); err != nil {
		return err
	}
	if err := validateMediaFields(fields); err != nil {
		return err
	}
	provenance, err := normalizeMediaMetadataProvenance(provenance)
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	metadata.Source = MutationSourceOperator
	if err := updateMediaMetadataTx(ctx, tx, id, metadata); err != nil {
		return err
	}
	if err := upsertMediaMetadataProvenance(ctx, tx, id, provenance); err != nil {
		return err
	}
	if _, err := upsertMediaFieldsTx(ctx, tx, id, fields, MutationSourceOperator); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.emitMediaUpdated(ctx, id)
	return nil
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
	var record MediaMetadataProvenance
	err := s.catalogR.QueryRowContext(ctx,
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
