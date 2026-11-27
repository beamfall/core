package graph

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/beamfall/core/internal/artwork"
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

// PlayerControllersInfo is a Player Profile's controller policy (ADR-0240 D4).
type PlayerControllersInfo struct {
	ProfileID string   `json:"profileID"`
	AllAdults bool     `json:"allAdults"`
	Include   []string `json:"include"`
	Exclude   []string `json:"exclude"`
}

// PlayerDisplayInfo is one Player Profile display visible to an authorized
// controller on playerDisplays (ADR-0240 D6, D9).
type PlayerDisplayInfo struct {
	Handle          string                 `json:"handle"`
	SessionID       string                 `json:"sessionID,omitempty"`
	PlayerProfileID string                 `json:"playerProfileID"`
	DisplayName     string                 `json:"displayName"`
	Presence        string                 `json:"presence"`
	LastSeenAt      string                 `json:"lastSeenAt,omitempty"`
	CurrentItem     *PlayerDisplayItemInfo `json:"currentItem,omitempty"`
	InParty         bool                   `json:"inParty,omitempty"`
}

// PlayerDisplayItemInfo is the current item playing on a display, filtered
// per observing controller under ADR-0240 D6.
type PlayerDisplayItemInfo struct {
	MediaID         string `json:"mediaID,omitempty"`
	Title           string `json:"title,omitempty"`
	LibraryID       string `json:"libraryID,omitempty"`
	ArtworkURL      string `json:"artworkURL,omitempty"`
	PrivateItem     bool   `json:"privateItem,omitempty"`
	Label           string `json:"label,omitempty"`
	PositionSeconds int    `json:"positionSeconds"`
	DurationSeconds int    `json:"durationSeconds"`
}

const (
	playerPresenceOffline = "offline"
	playerPresenceReady   = "ready"
	playerPresencePlaying = "playing"

	playerPresenceHeartbeatTimeout = 60 * time.Second
	playerControlAttemptAction     = "player.control"
)

var playerControlBackoff = store.AuthBackoff{
	Window:       10 * time.Second,
	FreeAttempts: 30,
	BaseDelay:    10 * time.Second,
	MaxDelay:     10 * time.Second,
}

type playerStreamPresence struct {
	openCount     int
	lastHeartbeat time.Time
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
	return h.profileInfoForScope(ctx, scope, profile)
}

func playerControllersInfo(c store.PlayerControllers) PlayerControllersInfo {
	include := make([]string, len(c.Include))
	for i, id := range c.Include {
		include[i] = string(id)
	}
	exclude := make([]string, len(c.Exclude))
	for i, id := range c.Exclude {
		exclude[i] = string(id)
	}
	return PlayerControllersInfo{
		ProfileID: string(c.ProfileID),
		AllAdults: c.AllAdults,
		Include:   include,
		Exclude:   exclude,
	}
}

func (h *Handler) playerControllers(ctx context.Context, req request) (PlayerControllersInfo, error) {
	profileID := trimmedVariable(req, "profileID")
	if profileID == "" {
		return PlayerControllersInfo{}, clientError("profileID is required")
	}
	ctrl, err := h.store.PlayerControllers(ctx, store.ProfileID(profileID))
	if err != nil {
		return PlayerControllersInfo{}, err
	}
	return playerControllersInfo(ctrl), nil
}

func (h *Handler) setPlayerControllers(ctx context.Context, scope auth.Scope, req request) (ProfileInfo, error) {
	profileID := trimmedVariable(req, "profileID")
	if profileID == "" {
		return ProfileInfo{}, clientError("profileID is required")
	}
	v := newVars(req)
	allAdults, hasAllAdults := v.Bool("allAdults")
	rawInclude, _ := v.StringList("include", "include")
	rawExclude, _ := v.StringList("exclude", "exclude")
	if err := v.Err(); err != nil {
		return ProfileInfo{}, err
	}
	targetID := store.ProfileID(profileID)
	if !hasAllAdults {
		cur, err := h.store.PlayerControllers(ctx, targetID)
		if err != nil {
			return ProfileInfo{}, err
		}
		allAdults = cur.AllAdults
	}
	include := make([]store.ProfileID, len(rawInclude))
	for i, id := range rawInclude {
		include[i] = store.ProfileID(id)
	}
	exclude := make([]store.ProfileID, len(rawExclude))
	for i, id := range rawExclude {
		exclude[i] = store.ProfileID(id)
	}
	ctrl, err := h.store.SetPlayerControllers(ctx, targetID, allAdults, include, exclude)
	if err != nil {
		return ProfileInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityPlayerControllers, "profile", profileID, ""); err != nil {
		return ProfileInfo{}, err
	}
	profile, err := h.store.Profile(ctx, targetID)
	if err != nil {
		return ProfileInfo{}, err
	}
	info := profileInfo(profile)
	c := playerControllersInfo(ctrl)
	info.PlayerControllers = &c
	return info, nil
}

func (h *Handler) playerStreamOpened(session store.SessionID) {
	if session == "" {
		return
	}
	now := h.now().UTC()
	h.playerPresenceMu.Lock()
	if h.playerStreams == nil {
		h.playerStreams = make(map[store.SessionID]playerStreamPresence)
	}
	st := h.playerStreams[session]
	st.openCount++
	st.lastHeartbeat = now
	h.playerStreams[session] = st
	h.playerPresenceMu.Unlock()

	if h.store != nil {
		_ = h.store.TouchDeviceLastSeenBySession(context.Background(), session, now)
	}
}

func (h *Handler) playerStreamClosed(session store.SessionID) {
	if session == "" {
		return
	}
	now := h.now().UTC()
	h.playerPresenceMu.Lock()
	if h.playerStreams == nil {
		h.playerPresenceMu.Unlock()
		return
	}
	st, ok := h.playerStreams[session]
	if !ok {
		h.playerPresenceMu.Unlock()
		return
	}
	if st.openCount > 0 {
		st.openCount--
	}
	if st.openCount == 0 {
		delete(h.playerStreams, session)
	} else {
		h.playerStreams[session] = st
	}
	h.playerPresenceMu.Unlock()

	if st.openCount == 0 && h.store != nil {
		_ = h.store.TouchDeviceLastSeenBySession(context.Background(), session, now)
	}
}

// EventStreamHeartbeat records a live keepalive/event touch on session's
// event stream and updates the bound display's stored last-seen timestamp
// (ADR-0240 D9).
func (h *Handler) EventStreamHeartbeat(session store.SessionID) {
	if session == "" {
		return
	}
	now := h.now().UTC()
	h.playerPresenceMu.Lock()
	if h.playerStreams == nil {
		h.playerStreams = make(map[store.SessionID]playerStreamPresence)
	}
	st := h.playerStreams[session]
	if st.openCount == 0 {
		st.openCount = 1
	}
	st.lastHeartbeat = now
	h.playerStreams[session] = st
	h.playerPresenceMu.Unlock()

	if h.store != nil {
		_ = h.store.TouchDeviceLastSeenBySession(context.Background(), session, now)
	}
}

func (h *Handler) isPlayerSessionOnline(session store.SessionID, now time.Time) bool {
	if session == "" {
		return false
	}
	h.playerPresenceMu.Lock()
	st, ok := h.playerStreams[session]
	h.playerPresenceMu.Unlock()
	if !ok || st.openCount <= 0 || st.lastHeartbeat.IsZero() {
		return false
	}
	return now.Sub(st.lastHeartbeat) <= playerPresenceHeartbeatTimeout
}

func (h *Handler) playerSessionLastSeen(session store.SessionID, stored time.Time) time.Time {
	if session == "" {
		return stored
	}
	h.playerPresenceMu.Lock()
	st, ok := h.playerStreams[session]
	h.playerPresenceMu.Unlock()
	if ok && st.lastHeartbeat.After(stored) {
		return st.lastHeartbeat
	}
	return stored
}

func (h *Handler) checkPlayerControlRateLimit(ctx context.Context, controllerProfileID store.ProfileID, now time.Time) error {
	if controllerProfileID == "" || h.store == nil {
		return nil
	}
	_, lockedUntil, err := h.store.ReserveAuthAttempt(ctx, playerControlAttemptAction, []string{string(controllerProfileID)}, now, playerControlBackoff)
	if err != nil {
		return err
	}
	if !lockedUntil.IsZero() {
		return clientError("player control attempts throttled")
	}
	return nil
}

func (h *Handler) playerArtworkURL(id store.MediaID) string {
	slots := h.mediaArtworkSlots(id)
	if url, ok := slots[artwork.SlotPoster]; ok {
		return url
	}
	for _, slot := range artwork.KnownSlots {
		if url, ok := slots[slot]; ok {
			return url
		}
	}
	return ""
}

func (h *Handler) playerDisplayHandle(deviceID store.DeviceID) string {
	if h.auth != nil {
		return h.auth.PlayerDisplayHandle(deviceID)
	}
	return string(deviceID)
}

// declaresPlayerControl reports whether the caller's client declared
// player-control. Older controllers never see player rows (ADR-0240 D11).
func declaresPlayerControl(scope auth.Scope) bool {
	return scope.HasClientCapability(string(capabilities.CapabilityPlayerControl))
}

func (h *Handler) playerDisplays(ctx context.Context, scope auth.Scope) ([]PlayerDisplayInfo, error) {
	if scope.ProfileID == "" || scope.IsPlayerBound() || !declaresPlayerControl(scope) {
		return []PlayerDisplayInfo{}, nil
	}
	bound, err := h.store.PlayerBoundDevices(ctx)
	if err != nil {
		return nil, err
	}
	if len(bound) == 0 {
		return []PlayerDisplayInfo{}, nil
	}
	now := h.now().UTC()
	controlled := make([]store.PlayerDisplay, 0, len(bound))
	for _, d := range bound {
		ok, err := h.store.IsPlayerController(ctx, d.PlayerProfileID, scope.ProfileID)
		if err != nil {
			return nil, err
		}
		if ok {
			controlled = append(controlled, d)
		}
	}
	if len(controlled) == 0 {
		return []PlayerDisplayInfo{}, nil
	}

	// Load active granted playbacks for controlled display sessions without
	// suppressing hidden titles yet so position/duration remain available for
	// "Private item" under D6, then check visibility per observing controller.
	activeBySession := map[store.SessionID]store.ActiveSession{}
	var mediaIDs []store.MediaID
	granted, err := h.store.GrantedSessions(ctx, now, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return nil, err
	}
	for _, g := range granted {
		if _, exists := activeBySession[g.SessionID]; !exists {
			activeBySession[g.SessionID] = g
			mediaIDs = append(mediaIDs, g.MediaID)
		}
	}
	visibleMedia := map[store.MediaID]store.Media{}
	if len(mediaIDs) > 0 {
		callerFilter, err := h.sealedFilter(ctx, scope.LibraryFilter())
		if err != nil {
			return nil, err
		}
		visibleMedia, err = h.store.VisibleMediaBatch(ctx, mediaIDs, callerFilter)
		if err != nil {
			return nil, err
		}
	}

	inPartyByDevice, err := h.store.PlayerDevicesInLiveParty(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]PlayerDisplayInfo, 0, len(controlled))
	for _, d := range controlled {
		displayName := d.DisplayName
		if displayName == "" {
			displayName = d.DeviceName
		}
		sessionLive := d.SessionID != "" && (d.SessionExpiresAt.IsZero() || d.SessionExpiresAt.After(now))
		online := sessionLive && h.isPlayerSessionOnline(d.SessionID, now)
		presence := playerPresenceOffline
		var currentItem *PlayerDisplayItemInfo
		if online {
			if active, playing := activeBySession[d.SessionID]; playing {
				presence = playerPresencePlaying
				if media, visible := visibleMedia[active.MediaID]; visible {
					currentItem = &PlayerDisplayItemInfo{
						MediaID:         string(active.MediaID),
						Title:           media.Title,
						LibraryID:       string(media.LibraryID),
						ArtworkURL:      h.playerArtworkURL(active.MediaID),
						PositionSeconds: active.PositionSeconds,
						DurationSeconds: active.DurationSeconds,
					}
				} else {
					currentItem = &PlayerDisplayItemInfo{
						PrivateItem:     true,
						Label:           "Private item",
						PositionSeconds: active.PositionSeconds,
						DurationSeconds: active.DurationSeconds,
					}
				}
			} else {
				presence = playerPresenceReady
			}
		}
		lastSeen := h.playerSessionLastSeen(d.SessionID, d.LastSeenAt)
		lastSeenStr := ""
		if !lastSeen.IsZero() {
			lastSeenStr = lastSeen.UTC().Format(time.RFC3339)
		}
		sessionIDStr := ""
		if sessionLive {
			sessionIDStr = string(d.SessionID)
		}
		out = append(out, PlayerDisplayInfo{
			Handle:          h.playerDisplayHandle(d.DeviceID),
			SessionID:       sessionIDStr,
			PlayerProfileID: string(d.PlayerProfileID),
			DisplayName:     displayName,
			Presence:        presence,
			LastSeenAt:      lastSeenStr,
			CurrentItem:     currentItem,
			InParty:         inPartyByDevice[d.DeviceID],
		})
	}
	slices.SortFunc(out, func(a, b PlayerDisplayInfo) int {
		if a.DisplayName != b.DisplayName {
			if a.DisplayName < b.DisplayName {
				return -1
			}
			return 1
		}
		if a.Handle < b.Handle {
			return -1
		}
		if a.Handle > b.Handle {
			return 1
		}
		return 0
	})
	return out, nil
}

// playerProfiles lists the household's Player Profiles with their walk-up
// setting and controllers list for the Console (ADR-0240 D1, D4, D7). The
// picker's profiles query leaves them out, so this owner read is the only
// listing. Disabled Player Profiles are hidden, as on profiles.
func (h *Handler) playerProfiles(ctx context.Context, scope auth.Scope) ([]ProfileInfo, error) {
	all, err := h.store.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	out := []ProfileInfo{}
	for _, profile := range all {
		if profile.Kind != store.ProfilePlayer || profile.Disabled {
			continue
		}
		info, err := h.profileInfoForScope(ctx, scope, profile)
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, nil
}

func (h *Handler) profileInfoForScope(ctx context.Context, scope auth.Scope, profile store.Profile) (ProfileInfo, error) {
	info := profileInfo(profile)
	if scope.Admin && profile.Kind == store.ProfilePlayer && h.store != nil {
		ctrl, err := h.store.PlayerControllers(ctx, profile.ID)
		if err != nil {
			if !errors.Is(err, store.ValidationError("profile not found")) {
				return ProfileInfo{}, err
			}
		} else {
			c := playerControllersInfo(ctrl)
			info.PlayerControllers = &c
		}
	}
	return info, nil
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
	if ok {
		return nil
	}
	partyOK, err := h.store.IsPlayerPartyCurrentMedia(ctx, scope.SessionID, mediaID)
	if err != nil {
		return err
	}
	if !partyOK {
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
	"trendingfeeds":       true,
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
