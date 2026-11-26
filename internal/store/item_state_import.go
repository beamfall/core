package store

import (
	"context"
	"database/sql"
	"math"
	"time"
)

// ItemStateImport is one imported item-state row keyed by a feed scope
// (subscription_id) plus source item GUID. Feed URLs stay on subscription rows;
// this ledger never stores raw feed URLs or credentials.
type ItemStateImport struct {
	ProfileID      ProfileID
	SubscriptionID SubscriptionID
	Source         string
	ItemGUID       string
	Played         bool
	HasProgress    bool
	Progress       float64
	Starred        bool
	// ImportRunID / ImportImporterID / ImportMatchMethod are the MIGREV-11
	// run-scoped provenance carried on this desired-state row,
	// mirroring MIGREV-7's EngagementProvenance shape so replay can stamp the
	// same audit columns onto the engagement write it eventually performs.
	// Empty on a caller that does not yet thread them (byte-for-byte the
	// pre-MIGREV-11 behavior).
	ImportRunID       string
	ImportImporterID  string
	ImportMatchMethod string
}

// RecordItemStateImport upserts one desired imported item-state row and clears
// the applied marker so a changed re-import can replay safely. The item GUID
// is stored exactly as given (CORE-0706).
func (s *Store) RecordItemStateImport(ctx context.Context, state ItemStateImport, now time.Time) error {
	if state.ProfileID == "" || state.SubscriptionID == "" || state.ItemGUID == "" {
		return ValidationError("profile ID, subscription ID, and item GUID are required")
	}
	if state.HasProgress && (state.Progress < 0 || state.Progress > 1) {
		return ValidationError("item progress must be between 0 and 1")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, plane, err := s.catalogPlaneForSubscriptionLocked(ctx, state.SubscriptionID)
	if err != nil {
		return err
	}
	_, err = plane.writer.ExecContext(ctx, `
		INSERT INTO item_state_import(profile_id, subscription_id, source, item_guid, played, has_progress, progress, starred, imported_at_ms, import_run_id, import_importer_id, import_match_method)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, subscription_id, item_guid) DO UPDATE SET
		  source = excluded.source,
		  played = excluded.played,
		  has_progress = excluded.has_progress,
		  progress = excluded.progress,
		  starred = excluded.starred,
		  imported_at_ms = excluded.imported_at_ms,
		  import_run_id = excluded.import_run_id,
		  import_importer_id = excluded.import_importer_id,
		  import_match_method = excluded.import_match_method,
		  applied_at_ms = 0,
		  applied_media_id = ''
	`, state.ProfileID, state.SubscriptionID, state.Source, state.ItemGUID, boolToInt(state.Played),
		boolToInt(state.HasProgress), state.Progress, boolToInt(state.Starred), timeToMillis(now.UTC()),
		nullString(state.ImportRunID), nullString(state.ImportImporterID), nullString(state.ImportMatchMethod))
	return err
}

// ReplayItemStateImports applies every pending imported item-state row whose
// feed-scoped GUID can be resolved through subscription_seen_entry.path to a
// concrete Media file. It never falls back to GUID-only media_external_id
// matching, because podcast GUIDs are only safe within a feed scope.
func (s *Store) ReplayItemStateImports(ctx context.Context, now time.Time) (int, error) {
	rows, err := s.pendingItemStateImports(ctx)
	if err != nil {
		return 0, err
	}
	applied := 0
	for _, row := range rows {
		complete, err := s.applyItemStateImport(ctx, row)
		if err != nil {
			return applied, err
		}
		if !complete {
			continue
		}
		marked, err := s.markItemStateImportApplied(ctx, row, now)
		if err != nil {
			return applied, err
		}
		if marked {
			applied++
		}
	}
	return applied, nil
}

type pendingItemStateImport struct {
	ItemStateImport
	MediaID         MediaID
	DurationSeconds int
}

func (s *Store) pendingItemStateImports(ctx context.Context) ([]pendingItemStateImport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	var out []pendingItemStateImport
	for _, plane := range s.allCatalogPlanesLocked() {
		filterClause := ""
		if !plane.encrypted {
			filterClause = ` AND sub.library_id NOT IN (SELECT id FROM library WHERE encrypted <> 0)`
		}
		// #nosec G202 -- filterClause is a closed literal SQL filter.
		rows, err := plane.reader.QueryContext(ctx, `
			SELECT isi.profile_id, isi.subscription_id, isi.source, isi.item_guid,
			       isi.played, isi.has_progress, isi.progress, isi.starred,
			       isi.import_run_id, isi.import_importer_id, isi.import_match_method,
			       mf.media_id, m.duration_seconds
			FROM item_state_import isi
			JOIN subscription_seen_entry se
			  ON se.subscription_id = isi.subscription_id
			 AND se.guid = isi.item_guid
			 AND se.path <> ''
			JOIN subscription sub ON sub.id = isi.subscription_id
			JOIN media_file mf ON mf.path = se.path AND mf.missing_at_ms IS NULL
			JOIN media m ON m.id = mf.media_id
			 AND m.library_id = sub.library_id
			 AND m.trashed_at_ms IS NULL
			WHERE isi.applied_at_ms = 0`+filterClause+`
			ORDER BY isi.profile_id, isi.subscription_id, isi.item_guid
		`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var row pendingItemStateImport
			var played, hasProgress, starred int
			var runID, importerID, matchMethod sql.NullString
			if err := rows.Scan(&row.ProfileID, &row.SubscriptionID, &row.Source, &row.ItemGUID,
				&played, &hasProgress, &row.Progress, &starred,
				&runID, &importerID, &matchMethod, &row.MediaID, &row.DurationSeconds); err != nil {
				_ = rows.Close()
				return nil, err
			}
			row.Played = played != 0
			row.HasProgress = hasProgress != 0
			row.Starred = starred != 0
			row.ImportRunID = runID.String
			row.ImportImporterID = importerID.String
			row.ImportMatchMethod = matchMethod.String
			out = append(out, row)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) applyItemStateImport(ctx context.Context, row pendingItemStateImport) (bool, error) {
	complete := true
	if row.Starred {
		current, err := s.applyPendingImportedStar(ctx, row)
		if err != nil || !current {
			return false, err
		}
	}
	if row.Played || row.HasProgress {
		if row.HasProgress && row.Progress > 0 && row.DurationSeconds <= 0 && !row.Played {
			complete = false
		} else if err := s.applyImportedItemProgress(ctx, row); err != nil {
			return false, err
		}
	}
	return complete, nil
}

// Hold the catalog writer while confirming the current star and its binding
// and writing the Favorite. Re-import and retention use the same writer.
func (s *Store) applyPendingImportedStar(ctx context.Context, row pendingItemStateImport) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, row.MediaID)
	if err != nil {
		return false, err
	}
	catalog, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer catalog.Rollback()
	var current bool
	err = catalog.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1`+liveSubscriptionReceipts+`
			AND m.id = ? AND EXISTS (
				SELECT 1 FROM item_state_import isi
				WHERE isi.subscription_id = se.subscription_id AND isi.item_guid = se.guid
				  AND isi.subscription_id = ? AND isi.item_guid = ? AND isi.profile_id = ?
				  AND isi.applied_at_ms = 0 AND isi.starred = 1
			)
		)
	`, row.MediaID, row.SubscriptionID, row.ItemGUID, row.ProfileID).Scan(&current)
	if err != nil || !current {
		return false, err
	}
	if err := insertImportedFavoriteSQL(ctx, s.eventsW, row.ProfileID, row.MediaID); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) insertImportedFavorite(ctx context.Context, profileID ProfileID, mediaID MediaID) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	return insertImportedFavoriteSQL(ctx, s.eventsW, profileID, mediaID)
}

func insertImportedFavoriteSQL(ctx context.Context, events sqlExecer, profileID ProfileID, mediaID MediaID) error {
	_, err := events.ExecContext(ctx, `
		INSERT OR IGNORE INTO media_favorite(profile_id, media_id, favorited_at_ms)
		VALUES (?, ?, ?)
	`, profileID, mediaID, timeToMillis(time.Now().UTC()))
	return err
}

func (s *Store) applyImportedItemProgress(ctx context.Context, row pendingItemStateImport) error {
	position := 0
	if row.HasProgress && row.DurationSeconds > 0 {
		position = int(math.Round(row.Progress * float64(row.DurationSeconds)))
	}
	existing, found, err := s.PlaybackProgressFor(ctx, row.ProfileID, row.MediaID)
	if err != nil {
		return err
	}
	if found {
		if existing.Watched {
			return nil
		}
		if !row.Played && position <= existing.PositionSeconds {
			return nil
		}
	}
	resume := true
	return s.UpsertPlaybackProgressWithProvenance(ctx, PlaybackProgress{
		ProfileID:       row.ProfileID,
		MediaID:         row.MediaID,
		PositionSeconds: position,
		DurationSeconds: row.DurationSeconds,
	}, row.Played, ResumePolicy{Class: MediaClassAudio, Override: &resume}, EngagementProvenance{
		Source:      row.Source,
		RunID:       row.ImportRunID,
		ImporterID:  row.ImportImporterID,
		MatchMethod: row.ImportMatchMethod,
	})
}

// A replay of a previously unstarred row must leave a newer star pending.
func (s *Store) markItemStateImportApplied(ctx context.Context, row pendingItemStateImport, now time.Time) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	_, plane, err := s.catalogPlaneForSubscriptionLocked(ctx, row.SubscriptionID)
	if err != nil {
		return false, err
	}
	result, err := plane.writer.ExecContext(ctx, `
		UPDATE item_state_import
		SET applied_at_ms = ?, applied_media_id = ?
		WHERE profile_id = ? AND subscription_id = ? AND item_guid = ?
		  AND applied_at_ms = 0 AND starred = ?
	`, timeToMillis(now.UTC()), row.MediaID, row.ProfileID, row.SubscriptionID, row.ItemGUID, boolToInt(row.Starred))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected != 0, err
}
