package store

import "context"

// SignedInDeviceIDs returns the devices that still hold an enrolled session
// (CORE-1607). Signing a browser out deletes its session row but leaves the
// device itself unrevoked, and with no row RenewDeviceSession refuses it, so a
// device missing from this set must enroll again: Console shows it as signed
// out rather than active. An expired row still counts, since the device can
// renew it.
func (s *Store) SignedInDeviceIDs(ctx context.Context) (map[DeviceID]bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT DISTINCT device_id FROM auth_session
		WHERE device_id IS NOT NULL AND (kind = ? OR kind IS NULL OR kind = '')`,
		SessionKindEnrolled,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[DeviceID]bool{}
	for rows.Next() {
		var id DeviceID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// SessionIDsByDevice returns every auth session row grouped by its device, of
// any kind and whether or not it has expired (CORE-2345). Device revocation
// uses it to find the sessions that may own a live transcode even after
// endPlayback or grant expiry cleared their stream-token ledger rows. Within a
// device the ids are in id order.
func (s *Store) SessionIDsByDevice(ctx context.Context) (map[DeviceID][]SessionID, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT device_id, id FROM auth_session
		WHERE device_id IS NOT NULL AND device_id <> ''
		ORDER BY device_id, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[DeviceID][]SessionID{}
	for rows.Next() {
		var deviceID DeviceID
		var sessionID SessionID
		if err := rows.Scan(&deviceID, &sessionID); err != nil {
			return nil, err
		}
		out[deviceID] = append(out[deviceID], sessionID)
	}
	return out, rows.Err()
}
