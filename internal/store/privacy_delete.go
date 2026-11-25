package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

const profileDeleteIntentPrefix = "privacy.profile-delete:"

type profileDeletePhase string

const (
	profileDeleteIntent         profileDeletePhase = "intent"
	profileDeleteAuditDurable   profileDeletePhase = "audit-durable"
	profileDeleteEventsDeleted  profileDeletePhase = "events-deleted"
	profileDeleteCatalogDeleted profileDeletePhase = "catalog-deleted"
)

type profileDeleteState struct {
	OperationID string             `json:"operationID"`
	ProfileID   ProfileID          `json:"profileID"`
	Phase       profileDeletePhase `json:"phase"`
	Activity    ActivityEntry      `json:"activity"`
}

// DeleteMyEngagementAudited performs the ADR-0036 audit-before-delete order.
// A one-Media erase is one events transaction. A full Profile erase uses the
// durable cross-file state machine below because catalog.db and events.db are
// intentionally independent WAL files.
func (s *Store) DeleteMyEngagementAudited(ctx context.Context, profileID ProfileID, scope MyDataDeleteScope, mediaID MediaID, filter LibraryFilter, activity ActivityEntry) error {
	if profileID == "" {
		return ValidationError("profile ID is required")
	}
	switch scope {
	case MyDataDeleteAll:
	case MyDataDeleteMedia:
		if mediaID == "" {
			return ValidationError("media ID is required")
		}
	default:
		return ValidationError("unknown delete scope")
	}
	if scope == MyDataDeleteMedia {
		s.mu.RLock()
		defer s.mu.RUnlock()
		if err := s.guardLocked(); err != nil {
			return err
		}
		if _, err := s.visibleMediaSQL(ctx, mediaID, filter); err != nil {
			return err
		}
		entry, err := s.prepareDeleteActivity(activity, ActivityMyDataDelete, profileID, string(scope), "")
		if err != nil {
			return err
		}
		return s.deleteProfileMediaAuditedLocked(ctx, profileID, mediaID, entry)
	}
	// Every ordinary store operation holds the shared side of this barrier.
	// Full-Profile erasure takes it exclusively so no engagement write can land
	// between the events and catalog phases and survive a successful delete.
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	entry, err := s.prepareDeleteActivity(activity, ActivityMyDataDelete, profileID, string(scope), "")
	if err != nil {
		return err
	}
	return s.startOrResumeProfileDeleteLocked(ctx, profileID, entry)
}

// DeleteProfileFootprintAudited is the operator/guardian full-footprint path.
func (s *Store) DeleteProfileFootprintAudited(ctx context.Context, profileID ProfileID, activity ActivityEntry) error {
	if profileID == "" {
		return ValidationError("profile ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	var exists int
	err := s.catalogR.QueryRowContext(ctx, `SELECT 1 FROM profile WHERE id = ?`, profileID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ValidationError("profile not found")
	}
	if err != nil {
		return err
	}
	entry, err := s.prepareDeleteActivity(activity, ActivityOperatorDataDelete, activity.ProfileID, "profile", string(profileID))
	if err != nil {
		return err
	}
	return s.startOrResumeProfileDeleteLocked(ctx, profileID, entry)
}

func (s *Store) prepareDeleteActivity(entry ActivityEntry, action string, profileID ProfileID, targetType, targetID string) (ActivityEntry, error) {
	if entry.OperationID == "" {
		id, err := newPrivacyDeleteOperationID()
		if err != nil {
			return ActivityEntry{}, err
		}
		entry.OperationID = id
	}
	if entry.OccurredAt.IsZero() {
		entry.OccurredAt = s.serverNow().UTC()
	}
	if entry.Action == "" {
		entry.Action = action
	}
	if entry.ProfileID == "" {
		entry.ProfileID = profileID
	}
	if entry.TargetType == "" {
		entry.TargetType = targetType
	}
	if entry.TargetID == "" {
		entry.TargetID = targetID
	}
	if err := validateActivityEntry(&entry); err != nil {
		return ActivityEntry{}, err
	}
	return entry, nil
}

func newPrivacyDeleteOperationID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "privacy-delete-" + hex.EncodeToString(raw[:]), nil
}

func (s *Store) deleteProfileMediaAuditedLocked(ctx context.Context, profileID ProfileID, mediaID MediaID, activity ActivityEntry) error {
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := appendActivitySQL(ctx, tx, activity); err != nil {
		return err
	}
	for _, table := range []string{"playback_progress", "media_favorite", "engagement_event", "offline_engagement_event"} {
		// #nosec G202 -- table is drawn from the closed literal list above.
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE profile_id = ? AND media_id = ?`, profileID, mediaID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM version_merge_playback WHERE profile_id = ? AND (source_id = ? OR media_id = ?)`,
		profileID, mediaID, mediaID); err != nil {
		return err
	}
	for _, table := range []string{"watchlist_item", "item_rating"} {
		// #nosec G202 -- table is drawn from the closed literal list above.
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE profile_id = ? AND target_kind = 'media' AND target_id = ?`, profileID, mediaID); err != nil {
			return err
		}
	}
	if err := deleteMyQueueMedia(ctx, tx, profileID, mediaID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) startOrResumeProfileDeleteLocked(ctx context.Context, profileID ProfileID, activity ActivityEntry) error {
	state, err := s.loadOrCreateProfileDeleteStateLocked(ctx, profileID, activity)
	if err != nil {
		return err
	}
	return s.resumeProfileDeleteStateLocked(ctx, state)
}

func profileDeleteIntentKey(profileID ProfileID) string {
	return profileDeleteIntentPrefix + string(profileID)
}

func (s *Store) loadOrCreateProfileDeleteStateLocked(ctx context.Context, profileID ProfileID, activity ActivityEntry) (profileDeleteState, error) {
	key := profileDeleteIntentKey(profileID)
	state, err := s.profileDeleteStateLocked(ctx, key)
	if err == nil {
		return state, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return profileDeleteState{}, err
	}
	state = profileDeleteState{OperationID: activity.OperationID, ProfileID: profileID, Phase: profileDeleteIntent, Activity: activity}
	raw, err := json.Marshal(state)
	if err != nil {
		return profileDeleteState{}, err
	}
	_, err = s.catalogW.ExecContext(ctx, `
		INSERT INTO server_config(key, value, updated_at_ms)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO NOTHING`, key, string(raw), timeToMillis(s.serverNow().UTC()))
	if err != nil {
		return profileDeleteState{}, err
	}
	return s.profileDeleteStateLocked(ctx, key)
}

func (s *Store) profileDeleteStateLocked(ctx context.Context, key string) (profileDeleteState, error) {
	var raw string
	if err := s.catalogW.QueryRowContext(ctx, `SELECT value FROM server_config WHERE key = ?`, key).Scan(&raw); err != nil {
		return profileDeleteState{}, err
	}
	var state profileDeleteState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return profileDeleteState{}, fmt.Errorf("decode profile delete intent %q: %w", key, err)
	}
	if err := validateProfileDeleteState(key, state); err != nil {
		return profileDeleteState{}, err
	}
	return state, nil
}

func validateProfileDeleteState(key string, state profileDeleteState) error {
	if state.OperationID == "" || state.ProfileID == "" || state.Activity.OperationID != state.OperationID || key != profileDeleteIntentKey(state.ProfileID) {
		return fmt.Errorf("invalid profile delete intent %q", key)
	}
	switch state.Phase {
	case profileDeleteIntent, profileDeleteAuditDurable, profileDeleteEventsDeleted, profileDeleteCatalogDeleted:
		return nil
	default:
		return fmt.Errorf("invalid profile delete phase %q in intent %q", state.Phase, key)
	}
}

func (s *Store) resumeProfileDeleteStateLocked(ctx context.Context, state profileDeleteState) error {
	for {
		switch state.Phase {
		case profileDeleteIntent:
			if err := appendActivitySQL(ctx, s.eventsW, state.Activity); err != nil {
				return err
			}
			state.Phase = profileDeleteAuditDurable
			if err := s.saveProfileDeleteStateLocked(ctx, state); err != nil {
				return err
			}
		case profileDeleteAuditDurable:
			if err := s.deleteProfileEventsLocked(ctx, state.ProfileID); err != nil {
				return err
			}
			state.Phase = profileDeleteEventsDeleted
			if err := s.saveProfileDeleteStateLocked(ctx, state); err != nil {
				return err
			}
		case profileDeleteEventsDeleted:
			state.Phase = profileDeleteCatalogDeleted
			if err := s.deleteProfileCatalogAndSaveStateLocked(ctx, state); err != nil {
				return err
			}
		case profileDeleteCatalogDeleted:
			if err := s.eraseProfileFromUnsealedPlanesLocked(ctx); err != nil {
				return err
			}
			_, err := s.catalogW.ExecContext(ctx, `DELETE FROM server_config WHERE key = ?`, profileDeleteIntentKey(state.ProfileID))
			return err
		default:
			return fmt.Errorf("unknown profile delete phase %q", state.Phase)
		}
	}
}

func (s *Store) saveProfileDeleteStateLocked(ctx context.Context, state profileDeleteState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx, `UPDATE server_config SET value = ?, updated_at_ms = ? WHERE key = ?`,
		string(raw), timeToMillis(s.serverNow().UTC()), profileDeleteIntentKey(state.ProfileID))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("profile delete intent disappeared")
	}
	return nil
}

func (s *Store) deleteProfileEventsLocked(ctx context.Context, profileID ProfileID) error {
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"playback_progress", "media_favorite", "engagement_event", "watchlist_item", "item_rating", "version_merge_playback", "offline_engagement_event"} {
		// #nosec G202 -- table is drawn from the closed literal list above.
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE profile_id = ?`, profileID); err != nil {
			return err
		}
	}
	if err := deleteMyQueueAll(ctx, tx, profileID); err != nil {
		return err
	}
	if err := deletePartyProfileTx(ctx, tx, profileID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) deleteProfileCatalogAndSaveStateLocked(ctx context.Context, state profileDeleteState) error {
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deleteMyCatalogFootprintTx(ctx, tx, state.ProfileID); err != nil {
		return err
	}
	if err := recordPlaneErasuresTx(ctx, tx, state.ProfileID, timeToMillis(s.serverNow().UTC())); err != nil {
		return err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE server_config SET value = ?, updated_at_ms = ? WHERE key = ?`,
		string(raw), timeToMillis(s.serverNow().UTC()), profileDeleteIntentKey(state.ProfileID))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("profile delete intent disappeared")
	}
	return tx.Commit()
}

func deleteMyCatalogFootprintTx(ctx context.Context, tx *sql.Tx, profileID ProfileID) error {
	for _, table := range []string{"saved_filter", "saved_station", "offline_pack", "offline_pack_build", "access_request", "access_grant"} {
		// #nosec G202 -- table is drawn from the closed literal list above.
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE profile_id = ?`, profileID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM server_config WHERE key LIKE ?`, pluginProfileLinkPrefix+"%."+string(profileID)); err != nil {
		return err
	}
	// The Profile's own Home layout and its history (CORE-1972).
	for _, table := range []string{"nav_override", "nav_override_history"} {
		// #nosec G202 -- table is drawn from the closed literal list above.
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE scope = 'profile' AND scope_id = ?`, profileID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE media_operator_ordinal SET actor_profile_id = NULL WHERE actor_profile_id = ?`, profileID); err != nil {
		return err
	}
	for _, table := range []string{"marker", "smart_collection"} {
		// #nosec G202 -- table is drawn from the closed literal list above.
		if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET created_by_profile_id = NULL WHERE created_by_profile_id = ?`, profileID); err != nil {
			return err
		}
	}
	return nil
}

// resumeProfileFootprintDeletes completes crash-stranded privacy deletions at
// boot after both databases are migrated and readable.
func (s *Store) resumeProfileFootprintDeletes(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	rows, err := s.catalogR.QueryContext(ctx, `SELECT key, value FROM server_config WHERE key LIKE ? ORDER BY key`, profileDeleteIntentPrefix+"%")
	if err != nil {
		return err
	}
	var states []profileDeleteState
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return errors.Join(err, rows.Close())
		}
		var state profileDeleteState
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			return errors.Join(fmt.Errorf("decode profile delete intent %q: %w", key, err), rows.Close())
		}
		if err := validateProfileDeleteState(key, state); err != nil {
			return errors.Join(err, rows.Close())
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, state := range states {
		if err := s.resumeProfileDeleteStateLocked(ctx, state); err != nil {
			return err
		}
	}
	return nil
}

// privacyPlaneErasePrefix keys a Profile footprint erase that an Encrypted
// Library's plane still owes. The catalog phase records one marker per
// Encrypted Library in the same transaction that erases the shared footprint;
// an unsealed plane applies it before the intent clears, and a sealed plane
// applies it at its next unseal (CORE-0768, CORE-0769).
const privacyPlaneErasePrefix = "privacy.plane-erase:"

type planeErasure struct {
	key       string
	LibraryID LibraryID `json:"libraryID"`
	ProfileID ProfileID `json:"profileID"`
	// CutoffMS bounds the erase to rows created before the delete, so rows the
	// Profile creates later in a still-sealed plane survive its next unseal.
	CutoffMS int64 `json:"cutoffMS"`
}

func planeErasurePrefix(libraryID LibraryID) string {
	return privacyPlaneErasePrefix + string(libraryID) + ":"
}

func recordPlaneErasuresTx(ctx context.Context, tx *sql.Tx, profileID ProfileID, cutoffMS int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM library WHERE encrypted != 0 ORDER BY id`)
	if err != nil {
		return err
	}
	var libraries []LibraryID
	for rows.Next() {
		var id LibraryID
		if err := rows.Scan(&id); err != nil {
			return errors.Join(err, rows.Close())
		}
		libraries = append(libraries, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, libraryID := range libraries {
		raw, err := json.Marshal(planeErasure{LibraryID: libraryID, ProfileID: profileID, CutoffMS: cutoffMS})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO server_config(key, value, updated_at_ms) VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at_ms = excluded.updated_at_ms`,
			planeErasurePrefix(libraryID)+string(profileID), string(raw), cutoffMS); err != nil {
			return err
		}
	}
	return nil
}

// pendingPlaneErasuresLocked lists the erasures owed by one Library's plane.
// The prefix is compared with substr rather than LIKE so an ID holding a
// wildcard cannot match another Library's markers.
func (s *Store) pendingPlaneErasuresLocked(ctx context.Context, libraryID LibraryID) ([]planeErasure, error) {
	prefix := planeErasurePrefix(libraryID)
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT key, value FROM server_config WHERE substr(key, 1, ?) = ? ORDER BY key`, len(prefix), prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []planeErasure
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		var erasure planeErasure
		if err := json.Unmarshal([]byte(raw), &erasure); err != nil {
			return nil, fmt.Errorf("decode plane erasure %q: %w", key, err)
		}
		if erasure.LibraryID != libraryID || key != prefix+string(erasure.ProfileID) {
			return nil, fmt.Errorf("plane erasure %q does not match its key", key)
		}
		erasure.key = key
		out = append(out, erasure)
	}
	return out, rows.Err()
}

// applyPlaneErasuresLocked erases each owed Profile footprint from the plane
// in one plane transaction. The markers are left for the caller to clear only
// once that erase can no longer be discarded.
func (s *Store) applyPlaneErasuresLocked(ctx context.Context, plane catalogPlane) ([]string, error) {
	erasures, err := s.pendingPlaneErasuresLocked(ctx, plane.libraryID)
	if err != nil || len(erasures) == 0 {
		return nil, err
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	keys := make([]string, 0, len(erasures))
	for _, e := range erasures {
		for _, table := range []string{"saved_filter", "saved_station"} {
			// #nosec G202 -- table is drawn from the closed literal list above.
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE profile_id = ? AND created_at_ms <= ?`, e.ProfileID, e.CutoffMS); err != nil {
				return nil, err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE smart_collection SET created_by_profile_id = NULL
			WHERE created_by_profile_id = ? AND created_at_ms <= ?`, e.ProfileID, e.CutoffMS); err != nil {
			return nil, err
		}
		keys = append(keys, e.key)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return keys, nil
}

func deletePlaneErasureMarkers(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, keys []string) error {
	for _, key := range keys {
		if _, err := exec.ExecContext(ctx, `DELETE FROM server_config WHERE key = ?`, key); err != nil {
			return err
		}
	}
	return nil
}

// eraseProfileFromUnsealedPlanesLocked applies owed erasures to every live
// plane. A live plane's writes are durable, so its markers clear at once; a
// crash before that repeats an idempotent erase.
func (s *Store) eraseProfileFromUnsealedPlanesLocked(ctx context.Context) error {
	for _, plane := range s.unsealedEncryptedPlanesLocked() {
		keys, err := s.applyPlaneErasuresLocked(ctx, plane)
		if err != nil {
			return err
		}
		if err := deletePlaneErasureMarkers(ctx, s.catalogW, keys); err != nil {
			return err
		}
	}
	return nil
}

// planeErasureEvacuationLocked applies owed erasures to a plane being
// unsealed. Its markers clear in the shared evacuation transaction, so an
// unseal that unwinds and discards the working plane keeps them for the retry.
func (s *Store) planeErasureEvacuationLocked(ctx context.Context, plane catalogPlane) (evacuationPlan, error) {
	keys, err := s.applyPlaneErasuresLocked(ctx, plane)
	if err != nil || len(keys) == 0 {
		return evacuationPlan{}, err
	}
	return evacuationPlan{deleteRows: func(ctx context.Context, tx *sql.Tx) error {
		return deletePlaneErasureMarkers(ctx, tx, keys)
	}}, nil
}
