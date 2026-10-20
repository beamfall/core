package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"
)

// Live sessions dashboard, store half. The operator Console lists
// currently-active playback sessions — which Profile/Device is playing which
// Media, the resume position, and direct-play vs transcode state — over the
// EXISTING Stream Token ledger (migrate/events/0011). No separate session
// table: a live playback is exactly an outstanding media Stream Token, so the
// ledger that ADR-0036 already maintains is the authoritative read view (the
// ADR-0010 §"disk budget" note names the live-sessions dashboard as this
// model's read view). Reads here are manager-scoped at the graph layer and
// reveal-filtered by the operator's own LibraryFilter, exactly like the other
// Console reads (trash, libraries): a session playing Media the operator cannot
// currently see is still reported (so active load is never understated), but
// with its media-identifying fields suppressed — the same hidden==shape rule the
// stream-token ledger already follows.

// ActiveSession is one currently-playing session for the Console dashboard.
// It collapses the per-(target, media) ledger rows for one
// (session, media) pair into a single playback: Transcoding is true when an
// HLS-transcode token is outstanding, false for direct play only. Title and
// LibraryID are empty when the Media is outside the operator's current
// visibility (a restricted Library the operator has not revealed) — the row is
// still counted, never dropped.
type ActiveSession struct {
	SessionID       SessionID
	DeviceID        DeviceID
	DeviceName      string
	DeviceRole      DeviceRole
	ProfileID       ProfileID
	ProfileName     string
	MediaID         MediaID
	Title           string
	LibraryID       LibraryID
	Transcoding     bool
	PositionSeconds int
	DurationSeconds int
	ExpiresAt       time.Time
}

// activeLedgerRow is one unexpired media Stream Token from the ledger, before
// catalog resolution.
type activeLedgerRow struct {
	sessionID SessionID
	profileID ProfileID
	deviceID  DeviceID
	mediaID   MediaID
	hls       bool
	expiresAt time.Time
}

// ActiveSessions returns every currently-active playback session for the
// operator Console. The read spans both databases — the ledger
// lives in events, Device/Profile/Media in catalog — so it gathers the ledger
// first, then resolves catalog details in a second pass (the two stores are
// separate connections; SQLite cannot join across them). Expired ledger rows
// are filtered out by the same lazy-sweep the My Data read uses, so the
// dashboard shows only what is actually live. Media outside the operator's
// LibraryFilter keep their row (active load is never understated) but lose their
// identifying fields.
func (s *Store) ActiveSessions(ctx context.Context, now time.Time, filter LibraryFilter) ([]ActiveSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}

	rows, err := s.activeLedgerRows(ctx, now)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []ActiveSession{}, nil
	}

	// Resolve Media titles through the operator's visibility filter. A hidden
	// (or trashed) Media simply does not appear in visible, so its row keeps the
	// zero Title/LibraryID — indistinguishable from any other hidden row.
	mediaIDs := make([]MediaID, 0, len(rows))
	for _, r := range rows {
		mediaIDs = append(mediaIDs, r.mediaID)
	}
	visible, err := s.visibleMediaBatchSQL(ctx, mediaIDs, filter)
	if err != nil {
		return nil, err
	}

	// Resolve Device name/role for each distinct session (joined to its Device).
	sessionInfo, err := s.sessionDeviceInfo(ctx, rows)
	if err != nil {
		return nil, err
	}

	// Resolve Profile names once per distinct profile.
	profileNames, err := s.profileNames(ctx, rows)
	if err != nil {
		return nil, err
	}

	// Collapse to one row per (session, media); OR the HLS flag across targets
	// so a session that probed direct then fell back to transcode reads as
	// transcoding. Keyed by session+media; keep the latest expiry seen.
	type key struct {
		session SessionID
		media   MediaID
	}
	collapsed := map[key]*ActiveSession{}
	order := make([]key, 0, len(rows))
	for _, r := range rows {
		k := key{r.sessionID, r.mediaID}
		entry, ok := collapsed[k]
		if !ok {
			info := sessionInfo[r.sessionID]
			entry = &ActiveSession{
				SessionID:   r.sessionID,
				DeviceID:    r.deviceID,
				DeviceName:  info.name,
				DeviceRole:  info.role,
				ProfileID:   r.profileID,
				ProfileName: profileNames[r.profileID],
				MediaID:     r.mediaID,
				ExpiresAt:   r.expiresAt,
			}
			if media, shown := visible[r.mediaID]; shown {
				entry.Title = media.Title
				entry.LibraryID = media.LibraryID
				entry.DurationSeconds = media.DurationSeconds
			}
			collapsed[k] = entry
			order = append(order, k)
		}
		if r.hls {
			entry.Transcoding = true
		}
		if r.expiresAt.After(entry.ExpiresAt) {
			entry.ExpiresAt = r.expiresAt
		}
	}

	// Resume position is the Profile's per-Media playback projection. It is the
	// operator's view of where the session is — exact live position is not
	// tracked server-side (the canonical timeline is client-driven, ADR-0010),
	// so the last reported progress is the honest answer.
	for _, entry := range collapsed {
		if entry.Title == "" {
			continue // hidden Media: do not leak a position either
		}
		pos, dur, ok, err := s.playbackPosition(ctx, entry.ProfileID, entry.MediaID)
		if err != nil {
			return nil, err
		}
		if ok {
			entry.PositionSeconds = pos
			if dur > 0 {
				entry.DurationSeconds = dur
			}
		}
	}

	out := make([]ActiveSession, 0, len(order))
	for _, k := range order {
		out = append(out, *collapsed[k])
	}
	// Stable order for a deterministic dashboard read: by session, then media.
	sort.Slice(out, func(i, j int) bool {
		if out[i].SessionID != out[j].SessionID {
			return out[i].SessionID < out[j].SessionID
		}
		return out[i].MediaID < out[j].MediaID
	})
	return out, nil
}

// activeLedgerRows reads the unexpired media Stream Tokens from the ledger
// (events DB), lazily sweeping expired rows like MyStreamTokens does. Only the
// media-byte targets count as a live playback — artwork-candidate tokens are
// not playback and are excluded. Generated-asset tokens (trickplay, preview
// clip) ride alongside a playback but do not by themselves prove one, so they
// too are excluded; a session shows up only once it holds a direct or HLS token.
func (s *Store) activeLedgerRows(ctx context.Context, now time.Time) ([]activeLedgerRow, error) {
	nowMS := timeToMillis(now)
	_, _ = s.eventsW.ExecContext(ctx, `DELETE FROM stream_token_ledger WHERE expires_at_ms <= ?`, nowMS)
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT session_id, profile_id, device_id, target, media_id, expires_at_ms
		FROM stream_token_ledger
		WHERE expires_at_ms > ? AND target IN ('media-direct', 'media-hls')
		ORDER BY session_id, media_id, target
	`, nowMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []activeLedgerRow
	for rows.Next() {
		var r activeLedgerRow
		var target string
		var expires int64
		if err := rows.Scan(&r.sessionID, &r.profileID, &r.deviceID, &target, &r.mediaID, &expires); err != nil {
			return nil, err
		}
		r.hls = target == "media-hls"
		r.expiresAt = millisToTime(expires)
		out = append(out, r)
	}
	return out, rows.Err()
}

type sessionDevice struct {
	name string
	role DeviceRole
}

// sessionDeviceInfo resolves the Device name/role for each distinct session in
// the ledger rows, joining auth_session to device (catalog DB). A session whose
// row is already gone (terminated between the ledger read and this resolve)
// resolves to an empty device — the ledger's own device_id still attributes the
// row, so the dashboard degrades honestly rather than failing.
func (s *Store) sessionDeviceInfo(ctx context.Context, rows []activeLedgerRow) (map[SessionID]sessionDevice, error) {
	want := map[SessionID]bool{}
	for _, r := range rows {
		want[r.sessionID] = true
	}
	out := make(map[SessionID]sessionDevice, len(want))
	for id := range want {
		var name, role string
		err := s.catalogR.QueryRowContext(ctx, `
			SELECT d.name, d.role
			FROM auth_session a JOIN device d ON d.id = a.device_id
			WHERE a.id = ?
		`, id).Scan(&name, &role)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, err
		}
		out[id] = sessionDevice{name: name, role: DeviceRole(role)}
	}
	return out, nil
}

// profileNames resolves the display name for each distinct profile in the rows.
func (s *Store) profileNames(ctx context.Context, rows []activeLedgerRow) (map[ProfileID]string, error) {
	want := map[ProfileID]bool{}
	for _, r := range rows {
		want[r.profileID] = true
	}
	out := make(map[ProfileID]string, len(want))
	for id := range want {
		var name string
		err := s.catalogR.QueryRowContext(ctx, `SELECT name FROM profile WHERE id = ?`, id).Scan(&name)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, err
		}
		out[id] = name
	}
	return out, nil
}

// playbackPosition reads the session Profile's resume projection for one Media.
// ok is false when no progress row exists yet (playback just started).
func (s *Store) playbackPosition(ctx context.Context, profileID ProfileID, mediaID MediaID) (position, duration int, ok bool, err error) {
	row := s.eventsR.QueryRowContext(ctx, `
		SELECT position_seconds, duration_seconds
		FROM playback_progress
		WHERE profile_id = ? AND media_id = ?
	`, profileID, mediaID)
	if scanErr := row.Scan(&position, &duration); scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			return 0, 0, false, nil
		}
		return 0, 0, false, scanErr
	}
	return position, duration, true, nil
}

// TerminateSession ends one active Session (ADR-0007 §4 "kill session"): it
// deletes the auth_session row so the next request from that session fails
// auth (every request re-resolves the session via Store.Session, which returns
// "session not found" for a missing row — auth.Service.scope), and clears the
// session's outstanding Stream Token ledger rows so the dashboard stops showing
// it immediately. Deleting a session is additive — no existing call path does a
// plain session delete, so this introduces no behavioral change for any other
// surface. An unknown id is a ValidationError, not a silent no-op, so the
// operator learns the session was already gone.
func (s *Store) TerminateSession(ctx context.Context, id SessionID) error {
	if id == "" {
		return ValidationError("session ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx, `DELETE FROM auth_session WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ValidationError("session not found")
	}
	// Clear the terminated session's ledger rows so the live dashboard reflects
	// the kill at once rather than waiting for token expiry. Best-effort: a
	// failure here does not un-terminate the session (the auth row is already
	// gone), and the lazy sweep removes the rows at expiry regardless.
	_, _ = s.eventsW.ExecContext(ctx, `DELETE FROM stream_token_ledger WHERE session_id = ?`, id)
	return nil
}
