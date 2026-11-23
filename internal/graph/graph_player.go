package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/store"
)

// PlayerBindingInfo is the Session's current durable player binding status
// (ADR-0240 D2). A bound device lacking the player-display capability may read
// only this field and receives UpdateRequired=true.
type PlayerBindingInfo struct {
	Bound          bool   `json:"bound"`
	ProfileID      string `json:"profileID,omitempty"`
	DisplayName    string `json:"displayName,omitempty"`
	Generation     int    `json:"generation"`
	WalkUpEnabled  bool   `json:"walkUpEnabled"`
	UpdateRequired bool   `json:"updateRequired"`
}

func (h *Handler) playerBinding(scope auth.Scope) PlayerBindingInfo {
	bound := scope.IsPlayerBound()
	return PlayerBindingInfo{
		Bound:          bound,
		ProfileID:      string(scope.PlayerProfileID),
		DisplayName:    scope.PlayerDisplayName,
		Generation:     scope.PlayerBindingGeneration,
		WalkUpEnabled:  scope.WalkUpEnabled,
		UpdateRequired: bound && !scope.HasClientCapability(string(capabilities.CapabilityPlayerDisplay)),
	}
}

func (h *Handler) setDevicePlayerBinding(ctx context.Context, scope auth.Scope, req request) (DeviceInfo, error) {
	deviceID := trimmedVariable(req, "deviceID")
	profileID := trimmedVariable(req, "profileID")
	displayName := trimmedVariable(req, "displayName")
	if deviceID == "" {
		return DeviceInfo{}, clientError("deviceID is required")
	}
	if profileID == "" {
		return DeviceInfo{}, clientError("profileID is required")
	}
	byDevice, err := h.transcodeSessionIDsByDevice(ctx, scope)
	if err != nil {
		return DeviceInfo{}, err
	}
	device, err := h.store.SetDevicePlayerBinding(ctx, store.DeviceID(deviceID), store.ProfileID(profileID), displayName)
	if err != nil {
		return DeviceInfo{}, err
	}
	h.abortDeviceTranscodes(byDevice[store.DeviceID(deviceID)])
	if err := h.logActivity(ctx, scope, store.ActivityDevicePlayerBinding, "device", deviceID, ""); err != nil {
		return DeviceInfo{}, err
	}
	signedIn, err := h.store.SignedInDeviceIDs(ctx)
	if err != nil {
		return DeviceInfo{}, err
	}
	return deviceInfo(device, device.ID == scope.DeviceID, signedIn[device.ID]), nil
}

func (h *Handler) clearDevicePlayerBinding(ctx context.Context, scope auth.Scope, req request) (DeviceInfo, error) {
	deviceID := trimmedVariable(req, "deviceID")
	if deviceID == "" {
		return DeviceInfo{}, clientError("deviceID is required")
	}
	byDevice, err := h.transcodeSessionIDsByDevice(ctx, scope)
	if err != nil {
		return DeviceInfo{}, err
	}
	device, err := h.store.ClearDevicePlayerBinding(ctx, store.DeviceID(deviceID))
	if err != nil {
		return DeviceInfo{}, err
	}
	h.abortDeviceTranscodes(byDevice[store.DeviceID(deviceID)])
	if err := h.logActivity(ctx, scope, store.ActivityDevicePlayerBinding, "device", deviceID, ""); err != nil {
		return DeviceInfo{}, err
	}
	signedIn, err := h.store.SignedInDeviceIDs(ctx)
	if err != nil {
		return DeviceInfo{}, err
	}
	return deviceInfo(device, device.ID == scope.DeviceID, signedIn[device.ID]), nil
}

func (h *Handler) setPlayerWalkUp(ctx context.Context, scope auth.Scope, req request) (ProfileInfo, error) {
	profileID := trimmedVariable(req, "profileID")
	if profileID == "" {
		return ProfileInfo{}, clientError("profileID is required")
	}
	v := newVars(req)
	enabled, ok := v.Bool("enabled")
	if err := v.Err(); err != nil {
		return ProfileInfo{}, err
	}
	if !ok {
		return ProfileInfo{}, clientError("enabled is required")
	}
	profile, err := h.store.SetPlayerWalkUp(ctx, store.ProfileID(profileID), enabled)
	if err != nil {
		return ProfileInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityPlayerWalkUp, "profile", profileID, ""); err != nil {
		return ProfileInfo{}, err
	}
	return profileInfo(profile), nil
}

// requireWalkUpOrCommandedMedia enforces ADR-0240 D7 on per-Media reads for
// player-bound Sessions: when walk-up is off, only Media named in a play-media
// session-control command addressed to this Session in the last 12 hours may
// be read.
func (h *Handler) requireWalkUpOrCommandedMedia(ctx context.Context, scope auth.Scope, mediaID store.MediaID) error {
	if !scope.IsPlayerBound() || scope.WalkUpEnabled {
		return nil
	}
	if h.store == nil || scope.SessionID == "" || mediaID == "" {
		return clientError("walk-up browsing is disabled")
	}
	ok, err := h.store.IsPlayerCommandedMedia(ctx, scope.SessionID, mediaID, time.Now().UTC())
	if err != nil {
		return err
	}
	if !ok {
		return clientError("walk-up browsing is disabled")
	}
	return nil
}

var walkUpBlockedFields = map[string]bool{
	"navigationmanifest":  true,
	"manifest":            true,
	"homelayout":          true,
	"sourceitems":         true,
	"libraryitems":        true,
	"libraryfolder":       true,
	"libraryentities":     true,
	"search":              true,
	"searchentities":      true,
	"searchresult":        true,
	"searchfeeds":         true,
	"semanticcapability":  true,
	"entity":              true,
	"entityrelations":     true,
	"mediaforentity":      true,
	"collections":         true,
	"smartcollections":    true,
	"channels":            true,
	"stations":            true,
	"stationgenerate":     true,
	"ambientqueue":        true,
	"preplayqueue":        true,
	"subscriptionentries": true,
	"subscriptionfollow":  true,
	"relatedmedia":        true,
	"similarmedia":        true,
}

var playerBlockedSettingsFields = map[string]bool{
	"setclientpref":   true,
	"sethomelayout":   true,
	"resethomelayout": true,
}
