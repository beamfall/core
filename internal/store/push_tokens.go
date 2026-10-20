package store

import (
	"context"
	"time"
)

// Device push-token persistence. The push seam used to
// be a no-op that discarded the token; a persisting registrar stores one current
// token per enrolled device so the dispatcher can fan a time-sensitive event out
// to a Profile's devices. The token is an opaque routing handle issued by
// APNs/FCM/the browser Push service — not an invariant-#5 operator credential —
// so it lives in the catalog DB; the credentials that SIGN the dispatch stay in
// the 0600 secure store. Schema: 0061_device_push_token.sql.

// DevicePushToken is one enrolled device's current push registration. Platform
// is the closed three-value vocabulary (apns | fcm | webpush). The registrar
// validates Platform before writing; the store treats it as an opaque string.
type DevicePushToken struct {
	DeviceID  DeviceID
	ProfileID ProfileID
	Platform  string
	Token     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UpsertDevicePushToken stores (or refreshes) the push token for one enrolled
// device. It is an upsert keyed on device_id: re-registering from the same
// device replaces the row in place (ADR-0039 §2.5: "refreshed on app launch"),
// so a rotated token never accumulates stale duplicates. created_at_ms is
// preserved across a refresh; updated_at_ms advances. The foreign keys
// (device, profile) are ON DELETE CASCADE, so a *deleted* device or deleted
// Profile drops its token automatically. NOTE: device *revocation* is a flag
// flip (device.revoked = 1), not a row delete, so it does NOT cascade —
// DevicePushTokensForProfile must exclude revoked devices explicitly (it does).
func (s *Store) UpsertDevicePushToken(ctx context.Context, t DevicePushToken) error {
	if t.DeviceID == "" {
		return ValidationError("device ID is required")
	}
	if t.ProfileID == "" {
		return ValidationError("profile ID is required")
	}
	if t.Platform == "" {
		return ValidationError("push platform is required")
	}
	if t.Token == "" {
		return ValidationError("push token is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	nowMS := time.Now().UTC().UnixMilli()
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO device_push_token
			(device_id, profile_id, platform, token, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET
			profile_id    = excluded.profile_id,
			platform      = excluded.platform,
			token         = excluded.token,
			updated_at_ms = excluded.updated_at_ms
	`, string(t.DeviceID), string(t.ProfileID), t.Platform, t.Token, nowMS, nowMS)
	return err
}

// DevicePushTokensForProfile returns every current push token registered to the
// given Profile's enrolled devices — the dispatch fan-out read (ADR-0039 §2.3:
// "fanned out as a push notification to all enrolled devices of every adult
// Profile"). The caller is the dispatcher at the composition root, not a
// session, so this is not reveal-filtered: a push token carries no library or
// playback content. Returns an empty slice when the Profile has no registered
// devices.
//
// Revoked devices are excluded: revocation flips
// device.revoked in place rather than deleting the device row, so the
// device_push_token ON DELETE CASCADE never fires for a revocation — a revoked
// device would otherwise keep receiving the parental-approval fan-out (a
// confused-deputy path). The JOIN on device with revoked = 0 closes that.
func (s *Store) DevicePushTokensForProfile(ctx context.Context, profileID ProfileID) ([]DevicePushToken, error) {
	if profileID == "" {
		return nil, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT t.device_id, t.profile_id, t.platform, t.token, t.created_at_ms, t.updated_at_ms
		FROM device_push_token t
		JOIN device d ON d.id = t.device_id
		WHERE t.profile_id = ? AND d.revoked = 0
		ORDER BY t.device_id`, string(profileID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DevicePushToken{}
	for rows.Next() {
		var (
			t                    DevicePushToken
			deviceID, profID     string
			createdMS, updatedMS int64
		)
		if err := rows.Scan(&deviceID, &profID, &t.Platform, &t.Token, &createdMS, &updatedMS); err != nil {
			return nil, err
		}
		t.DeviceID = DeviceID(deviceID)
		t.ProfileID = ProfileID(profID)
		t.CreatedAt = time.UnixMilli(createdMS).UTC()
		t.UpdatedAt = time.UnixMilli(updatedMS).UTC()
		out = append(out, t)
	}
	return out, rows.Err()
}
