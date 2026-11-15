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
