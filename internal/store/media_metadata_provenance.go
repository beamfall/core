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
}

// SetMediaMetadataProvenance records which plugin scraper supplied a Media
// item's metadata, replacing any earlier record. Deleting the Media row
// deletes the record (ON DELETE CASCADE).
func (s *Store) SetMediaMetadataProvenance(ctx context.Context, mediaID MediaID, pluginID, scraperID string) error {
	pluginID = strings.TrimSpace(pluginID)
	scraperID = strings.TrimSpace(scraperID)
	if mediaID == "" {
		return ValidationError("media ID is required")
	}
	if pluginID == "" || scraperID == "" {
		return ValidationError("plugin and scraper IDs are required")
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
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO media_metadata_provenance(media_id, plugin_id, scraper_id, updated_at_ms)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(media_id) DO UPDATE SET
			plugin_id = excluded.plugin_id,
			scraper_id = excluded.scraper_id,
			updated_at_ms = excluded.updated_at_ms`,
		mediaID, pluginID, scraperID, timeToMillis(time.Now().UTC()))
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
	var record MediaMetadataProvenance
	err := s.catalogR.QueryRowContext(ctx,
		`SELECT plugin_id, scraper_id FROM media_metadata_provenance WHERE media_id = ?`, mediaID).
		Scan(&record.PluginID, &record.ScraperID)
	if errors.Is(err, sql.ErrNoRows) {
		return MediaMetadataProvenance{}, false, nil
	}
	if err != nil {
		return MediaMetadataProvenance{}, false, err
	}
	return record, true, nil
}
