package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Device administration, graph half. Admin-Session-gated reads and
// mutations the Console uses to see and revoke enrolled browsers/devices. The
// redeem (enroll), self-promote, and pairing-code mint flows live elsewhere
// (REST); this file is the operator-facing administration over already-enrolled
// devices: the device LIST and REVOKE-ALL. Single-device revoke is revokeDevice
// (graph_livesessions.go). Gated on scope.Admin in the execute() dispatch.

// DeviceInfo is one enrolled device on the wire. The device secret
// hash is a CREDENTIAL and is NEVER serialized — only the operator-meaningful
// identity, role, revoked state, and whether this is the requesting browser.
type DeviceInfo struct {
	DeviceID string `json:"deviceID"`
	Name     string `json:"name"`
	// Role is the device capability class: "viewer" or "manager" (store.DeviceRole).
	Role string `json:"role"`
	// Revoked is true once the device credential has been revoked.
	Revoked bool `json:"revoked"`
	// Current is true for the device making this request — the Console must never
	// offer to revoke the operator's own browser (that would lock them out).
	Current bool `json:"current"`
}

// devices lists every enrolled device for the Console administration list. The
// secret hash is dropped at the boundary; `current` marks the requesting device.
func (h *Handler) devices(ctx context.Context, scope auth.Scope) ([]DeviceInfo, error) {
	list, err := h.store.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DeviceInfo, 0, len(list))
	for _, d := range list {
		out = append(out, DeviceInfo{
			DeviceID: string(d.ID),
			Name:     d.Name,
			Role:     string(d.Role),
			Revoked:  d.Revoked,
			Current:  d.ID == scope.DeviceID,
		})
	}
	return out, nil
}

func (h *Handler) setDeviceRole(ctx context.Context, scope auth.Scope, req request) (DeviceInfo, error) {
	deviceID := store.DeviceID(trimmedVariable(req, "deviceID"))
	role := store.DeviceRole(trimmedVariable(req, "role"))
	if deviceID == "" {
		return DeviceInfo{}, clientError("deviceID is required")
	}
	if deviceID == scope.DeviceID {
		return DeviceInfo{}, clientError("cannot change the current device role")
	}
	device, err := h.store.SetDeviceRole(ctx, deviceID, role)
	if err != nil {
		return DeviceInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityDeviceRole, "device", string(deviceID), ""); err != nil {
		return DeviceInfo{}, err
	}
	return DeviceInfo{
		DeviceID: string(device.ID),
		Name:     device.Name,
		Role:     string(device.Role),
		Revoked:  device.Revoked,
		Current:  false,
	}, nil
}

// RevokeAllDevicesResult reports how many devices were revoked and how many
// in-flight transcodes were aborted across them (ADR-0081 revoke-all).
type RevokeAllDevicesResult struct {
	RevokedCount      int `json:"revokedCount"`
	TranscodesAborted int `json:"transcodesAborted"`
}

// revokeAllDevices revokes every enrolled device EXCEPT the requesting one (the
// operator's own browser is never revoked from here — that would lock them
// out), and skips devices already revoked. Each revocation propagates exactly
// like single-device revoke (graph_livesessions.go): the Revoked flag fails the
// device's next per-event scope re-evaluation, and any in-flight transcode it
// owns is aborted. ActiveSessions is enumerated once and fanned out across
// devices. Each revocation is audited (ActivityDeviceRevoke). Admin-gated by the
// caller.
func (h *Handler) revokeAllDevices(ctx context.Context, scope auth.Scope) (RevokeAllDevicesResult, error) {
	list, err := h.store.ListDevices(ctx)
	if err != nil {
		return RevokeAllDevicesResult{}, err
	}
	var activeByDevice map[store.DeviceID][]store.SessionID
	if h.transcodeTerminator != nil {
		active, err := h.store.ActiveSessions(ctx, time.Now().UTC(), scope.LibraryFilter())
		if err != nil {
			return RevokeAllDevicesResult{}, err
		}
		activeByDevice = activeSessionIDsByDevice(active)
	}
	result := RevokeAllDevicesResult{}
	for _, d := range list {
		if d.ID == scope.DeviceID || d.Revoked {
			continue
		}
		if err := h.store.RevokeDevice(ctx, d.ID); err != nil {
			return RevokeAllDevicesResult{}, err
		}
		result.RevokedCount++
		result.TranscodesAborted += h.abortDeviceTranscodes(activeByDevice[d.ID])
		if err := h.logActivity(ctx, scope, store.ActivityDeviceRevoke, "device", string(d.ID), ""); err != nil {
			return RevokeAllDevicesResult{}, err
		}
	}
	return result, nil
}
