package store

import (
	"context"
	"strings"
	"time"
)

// maxDeviceBuildIDLength matches the device_build_binding.build_id check.
const maxDeviceBuildIDLength = 256

// DeviceBuildBinding is the exact mapping from an enrolled device to the
// first-party native build it enrolled with (ADR-0063 build provenance).
type DeviceBuildBinding struct {
	DeviceID DeviceID
	Surface  DeviceSurface
	BuildID  string
	BoundAt  time.Time
}

// BindDeviceBuild records that deviceID enrolled with buildID on a native
// surface. The caller must already have verified the build against the
// release catalog for this device's role and distribution class; the store
// only keeps the fact. Recording the same build again refreshes it, and a later
// verified build for the same device and surface replaces the earlier one.
// An unknown or revoked device is refused. The row is removed with the device.
func (s *Store) BindDeviceBuild(ctx context.Context, deviceID DeviceID, surface DeviceSurface, buildID string, at time.Time) error {
	if deviceID == "" {
		return ValidationError("device identity is required")
	}
	normalized, err := NormalizeDeviceSurface(surface)
	if err != nil {
		return err
	}
	if normalized == DeviceSurfaceCoreWebUI {
		return ValidationError("native device surface is required")
	}
	if buildID == "" || buildID != strings.TrimSpace(buildID) || len(buildID) > maxDeviceBuildIDLength {
		return ValidationError("build id must be 1-256 characters without surrounding space")
	}
	if at.IsZero() {
		return ValidationError("binding time is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO device_build_binding(device_id, surface, build_id, bound_at_ms)
		SELECT id, ?, ?, ? FROM device WHERE id = ? AND revoked = 0
		ON CONFLICT(device_id, surface) DO UPDATE SET
			build_id = excluded.build_id,
			bound_at_ms = excluded.bound_at_ms
	`, normalized, buildID, at.UnixMilli(), deviceID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ValidationError("device is unknown or revoked")
	}
	return nil
}

// DeviceBuildBindings lists the native builds deviceID is bound to, one per
// surface at most.
func (s *Store) DeviceBuildBindings(ctx context.Context, deviceID DeviceID) ([]DeviceBuildBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT surface, build_id, bound_at_ms FROM device_build_binding
		WHERE device_id = ? ORDER BY surface`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeviceBuildBinding
	for rows.Next() {
		binding := DeviceBuildBinding{DeviceID: deviceID}
		var boundAtMs int64
		if err := rows.Scan(&binding.Surface, &binding.BuildID, &boundAtMs); err != nil {
			return nil, err
		}
		binding.BoundAt = millisToTime(boundAtMs)
		out = append(out, binding)
	}
	return out, rows.Err()
}
