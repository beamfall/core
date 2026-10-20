package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// ErrSavedStationNotFound means no SavedStation exists with the given ID.
var ErrSavedStationNotFound = errors.New("saved station not found")

// SavedStationID is a stable SavedStation identifier.
type SavedStationID string

// SavedStation is a named, per-Profile, per-Library saved Station (ADR-0098
// area 5). "Station" is Beamfall Music PRODUCT LANGUAGE over a generated Play
// Queue — it is NEVER a Core schema type (invariant #1/#2). This record stores
// only the Profile-owned generation inputs and product-behaviour provenance:
// SeedJSON, RulesJSON, TuningJSON are opaque client expressions stored and
// returned verbatim (same posture as SavedFilter.FilterJSON). It deliberately
// does NOT store the generated track order; saving an exact track order remains
// a Playlist/ordered Collection (ADR-0098 §Stations). Profile-owned data
// participates in My Data export/delete.
type SavedStation struct {
	ID         SavedStationID
	ProfileID  ProfileID
	LibraryID  LibraryID
	Name       string
	SeedJSON   string
	RulesJSON  string
	TuningJSON string
	CreatedAt  time.Time
}

// SavedStations returns the Profile's saved Stations for a Library, newest first.
func (s *Store) SavedStations(ctx context.Context, profileID ProfileID, libraryID LibraryID) ([]SavedStation, error) {
	if profileID == "" || libraryID == "" {
		return nil, ValidationError("profile ID and library ID are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT id, profile_id, library_id, name, seed_json, rules_json, tuning_json, created_at_ms
		FROM saved_station
		WHERE profile_id = ? AND library_id = ?
		ORDER BY created_at_ms DESC
	`, profileID, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stations []SavedStation
	for rows.Next() {
		st, err := scanSavedStation(rows)
		if err != nil {
			return nil, err
		}
		stations = append(stations, st)
	}
	if stations == nil {
		stations = []SavedStation{}
	}
	return stations, rows.Err()
}

// CreateSavedStation inserts a new SavedStation for a Profile+Library pair. The
// Library must exist (enforced by FK). Name is trimmed; empty name is rejected.
// Empty seed/rules/tuning default to "{}" so the columns are always valid JSON.
func (s *Store) CreateSavedStation(ctx context.Context, st SavedStation, now time.Time) (SavedStation, error) {
	st.Name = strings.TrimSpace(st.Name)
	if st.ProfileID == "" {
		return SavedStation{}, ValidationError("profile ID is required")
	}
	if st.LibraryID == "" {
		return SavedStation{}, ValidationError("library ID is required")
	}
	if st.Name == "" {
		return SavedStation{}, ValidationError("name is required")
	}
	st.SeedJSON = defaultJSON(st.SeedJSON)
	st.RulesJSON = defaultJSON(st.RulesJSON)
	st.TuningJSON = defaultJSON(st.TuningJSON)
	id, err := randomSavedStationID()
	if err != nil {
		return SavedStation{}, err
	}
	st.ID = id
	st.CreatedAt = now.UTC()
	createdAtMS := st.CreatedAt.UnixMilli()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return SavedStation{}, err
	}
	_, err = s.catalogW.ExecContext(ctx, `
		INSERT INTO saved_station (id, profile_id, library_id, name, seed_json, rules_json, tuning_json, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, st.ID, st.ProfileID, st.LibraryID, st.Name, st.SeedJSON, st.RulesJSON, st.TuningJSON, createdAtMS)
	if err != nil {
		return SavedStation{}, err
	}
	return st, nil
}

// SavedStationByID fetches a SavedStation by its ID without Profile restriction.
// Used by the graph layer to check ownership before deletion.
func (s *Store) SavedStationByID(ctx context.Context, id SavedStationID) (SavedStation, error) {
	if id == "" {
		return SavedStation{}, ValidationError("id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return SavedStation{}, err
	}
	st, err := scanSavedStation(s.catalogR.QueryRowContext(ctx, `
		SELECT id, profile_id, library_id, name, seed_json, rules_json, tuning_json, created_at_ms
		FROM saved_station WHERE id = ?
	`, id))
	if err != nil {
		return SavedStation{}, ErrSavedStationNotFound
	}
	return st, nil
}

// DeleteSavedStation removes a SavedStation by ID and returns the deleted row.
func (s *Store) DeleteSavedStation(ctx context.Context, id SavedStationID) (SavedStation, error) {
	if id == "" {
		return SavedStation{}, ValidationError("id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return SavedStation{}, err
	}
	st, err := scanSavedStation(s.catalogR.QueryRowContext(ctx, `
		SELECT id, profile_id, library_id, name, seed_json, rules_json, tuning_json, created_at_ms
		FROM saved_station WHERE id = ?
	`, id))
	if err != nil {
		return SavedStation{}, ErrSavedStationNotFound
	}
	if _, err := s.catalogW.ExecContext(ctx, `DELETE FROM saved_station WHERE id = ?`, id); err != nil {
		return SavedStation{}, err
	}
	return st, nil
}

func scanSavedStation(scanner rowScanner) (SavedStation, error) {
	var st SavedStation
	var createdAtMS int64
	if err := scanner.Scan(&st.ID, &st.ProfileID, &st.LibraryID, &st.Name, &st.SeedJSON, &st.RulesJSON, &st.TuningJSON, &createdAtMS); err != nil {
		return SavedStation{}, err
	}
	st.CreatedAt = time.UnixMilli(createdAtMS).UTC()
	return st, nil
}

func defaultJSON(v string) string {
	if strings.TrimSpace(v) == "" {
		return "{}"
	}
	return v
}

func randomSavedStationID() (SavedStationID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return SavedStationID("station-" + hex.EncodeToString(raw[:])), nil
}
