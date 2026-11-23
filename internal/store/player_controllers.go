package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// PlayerControllers is a Player Profile's controller policy (ADR-0240 D4).
// AllAdults defaults to true; Include and Exclude are explicit Profile IDs.
type PlayerControllers struct {
	ProfileID ProfileID
	AllAdults bool
	Include   []ProfileID
	Exclude   []ProfileID
}

// PlayerDisplay is one durable player-bound Device and its current enrolled
// Session (if any) for controlled display listing (ADR-0240 D6, D9).
type PlayerDisplay struct {
	DeviceID          DeviceID
	DeviceName        string
	PlayerProfileID   ProfileID
	DisplayName       string
	BindingGeneration int
	SessionID         SessionID
	SessionExpiresAt  time.Time
	LastSeenAt        time.Time
}

// PlayerControlGuard pins the target Player Profile, binding generation, and
// controller Profile that must still hold at commit time.
type PlayerControlGuard struct {
	PlayerProfileID   ProfileID
	CallerProfileID   ProfileID
	BindingGeneration int
}

// ErrPlayerControlUnauthorized reports that a cross-Profile player control
// target was unbound, rebound, or the caller ceased to be a controller before
// commit.
var ErrPlayerControlUnauthorized = errors.New("player control unauthorized")

// PlayerControllers returns the stored controllers policy for a Player Profile.
func (s *Store) PlayerControllers(ctx context.Context, profileID ProfileID) (PlayerControllers, error) {
	if profileID == "" {
		return PlayerControllers{}, ValidationError("profileID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PlayerControllers{}, err
	}
	return readPlayerControllers(ctx, s.catalogR, profileID)
}

func readPlayerControllers(ctx context.Context, q sqlReader, profileID ProfileID) (PlayerControllers, error) {
	var kind ProfileKind
	var allAdults int
	err := q.QueryRowContext(ctx, `
		SELECT kind, player_controllers_all_adults
		FROM profile WHERE id = ?
	`, profileID).Scan(&kind, &allAdults)
	if errors.Is(err, sql.ErrNoRows) {
		return PlayerControllers{}, ValidationError("profile not found")
	}
	if err != nil {
		return PlayerControllers{}, err
	}
	if kind != ProfilePlayer {
		return PlayerControllers{}, ValidationError("controllers apply to player profiles only")
	}
	rows, err := q.QueryContext(ctx, `
		SELECT profile_id, mode
		FROM player_controller
		WHERE player_profile_id = ?
		ORDER BY profile_id
	`, profileID)
	if err != nil {
		return PlayerControllers{}, err
	}
	defer rows.Close()
	include := []ProfileID{}
	exclude := []ProfileID{}
	for rows.Next() {
		var id ProfileID
		var mode string
		if err := rows.Scan(&id, &mode); err != nil {
			return PlayerControllers{}, err
		}
		switch mode {
		case "include":
			include = append(include, id)
		case "exclude":
			exclude = append(exclude, id)
		}
	}
	if err := rows.Err(); err != nil {
		return PlayerControllers{}, err
	}
	return PlayerControllers{
		ProfileID: profileID,
		AllAdults: allAdults != 0,
		Include:   include,
		Exclude:   exclude,
	}, nil
}

// SetPlayerControllers replaces the controllers policy on a Player Profile.
// Target must be kind player; listed Profiles must exist and must not be kind
// player; a Profile cannot appear in both include and exclude.
func (s *Store) SetPlayerControllers(ctx context.Context, profileID ProfileID, allAdults bool, include, exclude []ProfileID) (PlayerControllers, error) {
	if profileID == "" {
		return PlayerControllers{}, ValidationError("profileID is required")
	}
	normInclude, err := normalizeControllerList(include)
	if err != nil {
		return PlayerControllers{}, err
	}
	normExclude, err := normalizeControllerList(exclude)
	if err != nil {
		return PlayerControllers{}, err
	}
	inSet := make(map[ProfileID]bool, len(normInclude))
	for _, id := range normInclude {
		inSet[id] = true
	}
	for _, id := range normExclude {
		if inSet[id] {
			return PlayerControllers{}, ValidationError("profile cannot be in both include and exclude lists")
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PlayerControllers{}, err
	}

	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return PlayerControllers{}, err
	}
	defer tx.Rollback()

	var kind ProfileKind
	err = tx.QueryRowContext(ctx, `SELECT kind FROM profile WHERE id = ?`, profileID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return PlayerControllers{}, ValidationError("profile not found")
	}
	if err != nil {
		return PlayerControllers{}, err
	}
	if kind != ProfilePlayer {
		return PlayerControllers{}, ValidationError("controllers apply to player profiles only")
	}

	validateListed := func(ids []ProfileID) error {
		for _, id := range ids {
			var listedKind ProfileKind
			err := tx.QueryRowContext(ctx, `SELECT kind FROM profile WHERE id = ?`, id).Scan(&listedKind)
			if errors.Is(err, sql.ErrNoRows) {
				return ValidationError("profile not found")
			}
			if err != nil {
				return err
			}
			if listedKind == ProfilePlayer {
				return ValidationError("player profile cannot be a controller")
			}
		}
		return nil
	}
	if err := validateListed(normInclude); err != nil {
		return PlayerControllers{}, err
	}
	if err := validateListed(normExclude); err != nil {
		return PlayerControllers{}, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE profile SET player_controllers_all_adults = ? WHERE id = ?
	`, boolToInt(allAdults), profileID); err != nil {
		return PlayerControllers{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM player_controller WHERE player_profile_id = ?`, profileID); err != nil {
		return PlayerControllers{}, err
	}
	for _, id := range normInclude {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO player_controller(player_profile_id, profile_id, mode) VALUES (?, ?, 'include')
		`, profileID, id); err != nil {
			return PlayerControllers{}, err
		}
	}
	for _, id := range normExclude {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO player_controller(player_profile_id, profile_id, mode) VALUES (?, ?, 'exclude')
		`, profileID, id); err != nil {
			return PlayerControllers{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return PlayerControllers{}, err
	}
	return readPlayerControllers(ctx, s.catalogR, profileID)
}

func normalizeControllerList(ids []ProfileID) ([]ProfileID, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	seen := make(map[ProfileID]bool, len(ids))
	out := make([]ProfileID, 0, len(ids))
	for _, raw := range ids {
		id := ProfileID(strings.TrimSpace(string(raw)))
		if id == "" {
			return nil, ValidationError("controller profile ID cannot be empty")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// IsPlayerController reports whether callerProfileID is currently a controller
// for playerProfileID, evaluated live against both Profiles' current kind and
// disabled state (ADR-0240 D4). Player Profiles are never controllers.
func (s *Store) IsPlayerController(ctx context.Context, playerProfileID, callerProfileID ProfileID) (bool, error) {
	if playerProfileID == "" || callerProfileID == "" {
		return false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	return isPlayerControllerLocked(ctx, s.catalogR, playerProfileID, callerProfileID)
}

func isPlayerControllerLocked(ctx context.Context, q queryRower, playerProfileID, callerProfileID ProfileID) (bool, error) {
	if playerProfileID == "" || callerProfileID == "" {
		return false, nil
	}
	var playerKind ProfileKind
	var playerDisabled, allAdults int
	err := q.QueryRowContext(ctx, `
		SELECT kind, disabled, player_controllers_all_adults
		FROM profile WHERE id = ?
	`, playerProfileID).Scan(&playerKind, &playerDisabled, &allAdults)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if playerKind != ProfilePlayer || playerDisabled != 0 {
		return false, nil
	}

	var callerKind ProfileKind
	var callerDisabled int
	err = q.QueryRowContext(ctx, `
		SELECT kind, disabled FROM profile WHERE id = ?
	`, callerProfileID).Scan(&callerKind, &callerDisabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if callerKind == ProfilePlayer || callerDisabled != 0 {
		return false, nil
	}

	var mode string
	err = q.QueryRowContext(ctx, `
		SELECT mode FROM player_controller
		WHERE player_profile_id = ? AND profile_id = ?
	`, playerProfileID, callerProfileID).Scan(&mode)
	switch {
	case err == nil:
		if mode == "exclude" {
			return false, nil
		}
		if mode == "include" {
			return true, nil
		}
		return false, nil
	case errors.Is(err, sql.ErrNoRows):
		return allAdults != 0 && callerKind == ProfileAdult, nil
	default:
		return false, err
	}
}

// PlayerBoundDevices returns every non-revoked Device bound to an active Player
// Profile along with its latest enrolled Session (if any) and stored last-seen
// timestamp.
func (s *Store) PlayerBoundDevices(ctx context.Context) ([]PlayerDisplay, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT d.id, d.name, d.player_profile_id, d.player_display_name, d.player_binding_generation, d.last_seen_at_ms,
		       COALESCE(a.id, ''), a.expires_at_ms
		FROM device d
		JOIN profile p ON p.id = d.player_profile_id
		LEFT JOIN auth_session a ON a.id = (
			SELECT s.id
			FROM auth_session s
			WHERE s.device_id = d.id
			  AND s.profile_id = d.player_profile_id
			  AND (s.kind = ? OR s.kind IS NULL OR s.kind = '')
			ORDER BY COALESCE(s.expires_at_ms, 9223372036854775807) DESC, s.id DESC
			LIMIT 1
		)
		WHERE d.revoked = 0
		  AND d.player_profile_id IS NOT NULL AND d.player_profile_id <> ''
		  AND p.disabled = 0 AND p.kind = ?
		ORDER BY d.player_display_name, d.name, d.id
	`, SessionKindEnrolled, ProfilePlayer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlayerDisplay{}
	for rows.Next() {
		var d PlayerDisplay
		var lastSeenMS int64
		var sessionID string
		var expiresAtMS sql.NullInt64
		if err := rows.Scan(
			&d.DeviceID, &d.DeviceName, &d.PlayerProfileID, &d.DisplayName, &d.BindingGeneration, &lastSeenMS,
			&sessionID, &expiresAtMS,
		); err != nil {
			return nil, err
		}
		d.SessionID = SessionID(sessionID)
		if expiresAtMS.Valid {
			d.SessionExpiresAt = millisToTime(expiresAtMS.Int64)
		}
		if lastSeenMS > 0 {
			d.LastSeenAt = millisToTime(lastSeenMS)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// TouchDeviceLastSeenBySession advances last_seen_at_ms for the player-bound
// Device holding sessionID.
func (s *Store) TouchDeviceLastSeenBySession(ctx context.Context, sessionID SessionID, now time.Time) error {
	if sessionID == "" {
		return nil
	}
	now = normalizeTime(now)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		UPDATE device
		SET last_seen_at_ms = MAX(last_seen_at_ms, ?)
		WHERE id = (SELECT device_id FROM auth_session WHERE id = ?)
		  AND player_profile_id IS NOT NULL AND player_profile_id <> ''
	`, timeToMillis(now), sessionID)
	return err
}

func (s *Store) verifyPlayerControlLocked(ctx context.Context, target SessionID, guard PlayerControlGuard) error {
	if target == "" || guard.PlayerProfileID == "" || guard.CallerProfileID == "" {
		return ErrPlayerControlUnauthorized
	}
	var sessionProfile ProfileID
	var revoked, generation int
	var playerProfileID string
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT s.profile_id, d.revoked, COALESCE(d.player_profile_id, ''), d.player_binding_generation
		FROM auth_session s
		JOIN device d ON d.id = s.device_id
		WHERE s.id = ?
	`, target).Scan(&sessionProfile, &revoked, &playerProfileID, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPlayerControlUnauthorized
	}
	if err != nil {
		return err
	}
	if revoked != 0 ||
		sessionProfile != guard.PlayerProfileID ||
		ProfileID(playerProfileID) != guard.PlayerProfileID ||
		generation != guard.BindingGeneration {
		return ErrPlayerControlUnauthorized
	}
	ok, err := isPlayerControllerLocked(ctx, s.catalogR, guard.PlayerProfileID, guard.CallerProfileID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrPlayerControlUnauthorized
	}
	return nil
}
