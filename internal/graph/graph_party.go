package graph

import (
	"context"
	"errors"
	"math"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Party session Graph operations (CORE-1662). The caller's Profile is the
// host for every operation except partyStage, partyScreenRegister and
// partyScreenReport, which any signed-in Profile on the server may call.

// PartyEntryInfo is one party entry.
type PartyEntryInfo struct {
	ID              string        `json:"id"`
	MediaID         store.MediaID `json:"mediaID"`
	Title           string        `json:"title"`
	Subtitle        string        `json:"subtitle"`
	DurationSeconds int           `json:"durationSeconds"`
	ArtworkURL      string        `json:"artworkURL"`
	ParticipantID   string        `json:"participantID"`
	ParticipantName string        `json:"participantName"`
	State           string        `json:"state"`
	Pinned          bool          `json:"pinned"`
	Position        int           `json:"position"`
	ETASeconds      int           `json:"etaSeconds"`
	PitchSemitones  int           `json:"pitchSemitones"`
	AddedAt         string        `json:"addedAt"`
	LastPlayedAt    string        `json:"lastPlayedAt"`
	Ineligible      bool          `json:"ineligible,omitempty"`
	Label           string        `json:"label,omitempty"`
}

// PartyParticipantInfo is one participant still in the session.
type PartyParticipantInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Guest      bool   `json:"guest"`
	Waiting    int    `json:"waiting"`
	LastTurnAt string `json:"lastTurnAt"`
}

// PartyPlaybackInfo is the current entry's shared playback anchor.
type PartyPlaybackInfo struct {
	EntryID        string  `json:"entryID"`
	State          string  `json:"state"`
	PositionMS     int64   `json:"positionMS"`
	AnchorAtMS     int64   `json:"anchorAtMS"`
	Tempo          float64 `json:"tempo"`
	PitchSemitones int     `json:"pitchSemitones"`
}

// PartyScreenInfo is one active stage screen.
type PartyScreenInfo struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Kind                string `json:"kind"`
	Carrier             string `json:"carrier"`
	Lead                bool   `json:"lead"`
	Ticker              bool   `json:"ticker"`
	OffsetMS            int64  `json:"offsetMS"`
	RTTMS               int64  `json:"rttMS"`
	ReportedAt          string `json:"reportedAt"`
	PlayerDisplay       bool   `json:"playerDisplay,omitempty"`
	PlayerDisplayHandle string `json:"playerDisplayHandle,omitempty"`
}

// PartyAvailableDisplayInfo is one controlled player display offered to the
// party host's Screens list (ADR-0240 D10).
type PartyAvailableDisplayInfo struct {
	Handle            string            `json:"handle"`
	DisplayName       string            `json:"displayName"`
	Presence          string            `json:"presence"`
	Joined            bool              `json:"joined"`
	CoversLibraries   bool              `json:"coversLibraries"`
	MissingLibraryIDs []store.LibraryID `json:"missingLibraryIDs"`
}

// PartyScreenJoinResult is returned when the host joins a player display to the
// party, reporting the screen, updated session, and Library coverage.
type PartyScreenJoinResult struct {
	Screen            PartyScreenInfo   `json:"screen"`
	Session           *PartySessionInfo `json:"session"`
	CoversLibraries   bool              `json:"coversLibraries"`
	MissingLibraryIDs []store.LibraryID `json:"missingLibraryIDs"`
}

// PartySessionInfo is a party session as its host sees it.
type PartySessionInfo struct {
	ID                string                      `json:"id"`
	JoinCode          string                      `json:"joinCode"`
	JoinPath          string                      `json:"joinPath"`
	Fairness          string                      `json:"fairness"`
	AutoAdd           bool                        `json:"autoAdd"`
	JoinOpen          bool                        `json:"joinOpen"`
	LibraryIDs        []store.LibraryID           `json:"libraryIDs"`
	StartedAt         string                      `json:"startedAt"`
	ExpiresAt         string                      `json:"expiresAt"`
	ChangeoverSeconds int                         `json:"changeoverSeconds"`
	Current           *PartyEntryInfo             `json:"current"`
	Playback          *PartyPlaybackInfo          `json:"playback"`
	Entries           []PartyEntryInfo            `json:"entries"`
	Requests          []PartyEntryInfo            `json:"requests"`
	Recent            []PartyEntryInfo            `json:"recent"`
	Removed           []PartyEntryInfo            `json:"removed"`
	Participants      []PartyParticipantInfo      `json:"participants"`
	Screens           []PartyScreenInfo           `json:"screens"`
	AvailableDisplays []PartyAvailableDisplayInfo `json:"availableDisplays"`
	Skipped           []PartyEntryInfo            `json:"skipped"`
}

// PartyStageInfo is what a stage screen renders.
type PartyStageInfo struct {
	SessionID   string             `json:"sessionID"`
	ServerNowMS int64              `json:"serverNowMS"`
	Current     *PartyEntryInfo    `json:"current"`
	Playback    *PartyPlaybackInfo `json:"playback"`
	UpNext      []PartyEntryInfo   `json:"upNext"`
	Ticker      bool               `json:"ticker"`
	Lead        bool               `json:"lead"`
	JoinCode    string             `json:"joinCode"`
	JoinPath    string             `json:"joinPath"`
}

// partyBooleanResult models the boolean-leaf party mutations (partyEnd,
// partyScreenReport): they carry no nested fields.
type partyBooleanResult struct{}

// PartyJoinPath is the web route a guest opens to join with code, under the
// path prefix of BEAMFALL_BASE_URL when the server is mounted below the root.
func PartyJoinPath(code string) string { return partyBasePath() + "/web/join/" + code }

var partyBasePath = sync.OnceValue(func() string { return partyBasePathOf(os.Getenv("BEAMFALL_BASE_URL")) })

// partyBasePathOf returns the path prefix of a base URL, normalized as the
// server mounts it: "" when unset, unparseable or at the root.
func partyBasePathOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.TrimSuffix(path.Clean("/"+strings.TrimPrefix(u.Path, "/")), "/")
}

// PartyTime renders an optional party timestamp as RFC 3339, "" when unset.
func PartyTime(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339)
}

// partyError maps store refusals to wire-safe client errors.
func partyError(err error) error {
	var input store.PartyInputError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &input):
		return clientError(input.Error())
	case errors.Is(err, store.ErrPartyNotFound):
		return clientError("party session not found")
	case errors.Is(err, store.ErrPartyDisplayAlreadyJoined):
		return clientError("display already in a party")
	case errors.Is(err, store.ErrPartyScreenIneligibleLead):
		return clientError("screen cannot play the current entry")
	case errors.Is(err, store.ErrDisplayInParty):
		return reasonedClientError{message: "display in party", reason: ErrorReasonDisplayInParty}
	}
	return err
}

func (h *Handler) partyArtworkURL(id store.MediaID) string {
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

func (h *Handler) partyEntryInfo(entry store.PartyEntryView) PartyEntryInfo {
	info := PartyEntryInfo{
		ID: entry.ID, MediaID: entry.MediaID, Title: entry.Title, Subtitle: entry.Subtitle,
		DurationSeconds: entry.DurationSeconds, ParticipantID: entry.ParticipantID,
		ParticipantName: entry.ParticipantName, State: entry.State, Pinned: entry.Pinned,
		Position: entry.Position, ETASeconds: entry.ETASeconds, PitchSemitones: entry.PitchSemitones,
		AddedAt: PartyTime(entry.AddedAt), LastPlayedAt: PartyTime(entry.LastPlayedAt),
		Ineligible: entry.Ineligible, Label: entry.Label,
	}
	if !entry.Ineligible && entry.Title != "" {
		info.ArtworkURL = h.partyArtworkURL(entry.MediaID)
	}
	return info
}

func (h *Handler) partyEntryInfos(entries []store.PartyEntryView) []PartyEntryInfo {
	out := make([]PartyEntryInfo, len(entries))
	for i, entry := range entries {
		out[i] = h.partyEntryInfo(entry)
	}
	return out
}

func (h *Handler) partyCurrent(entry *store.PartyEntryView) *PartyEntryInfo {
	if entry == nil {
		return nil
	}
	info := h.partyEntryInfo(*entry)
	return &info
}

func partyPlaybackInfo(playback *store.PartyPlaybackView) *PartyPlaybackInfo {
	if playback == nil {
		return nil
	}
	return &PartyPlaybackInfo{
		EntryID: playback.EntryID, State: playback.State, PositionMS: playback.PositionMS,
		AnchorAtMS: playback.AnchorAtMS, Tempo: playback.Tempo, PitchSemitones: playback.PitchSemitones,
	}
}

func (h *Handler) partyScreenInfo(screen store.PartyScreenView) PartyScreenInfo {
	info := PartyScreenInfo{
		ID: screen.ID, Name: screen.Name, Kind: screen.Kind, Carrier: screen.Carrier, Lead: screen.Lead,
		Ticker: screen.Ticker, OffsetMS: screen.OffsetMS, RTTMS: screen.RTTMS, ReportedAt: PartyTime(screen.ReportedAt),
	}
	if screen.DeviceID != "" {
		info.PlayerDisplay = true
		info.PlayerDisplayHandle = h.playerDisplayHandle(screen.DeviceID)
	}
	return info
}

func (h *Handler) partySessionInfo(ctx context.Context, view store.PartyView) *PartySessionInfo {
	session := view.Session
	info := &PartySessionInfo{
		ID: session.ID, JoinCode: session.JoinCode, JoinPath: PartyJoinPath(session.JoinCode),
		Fairness: session.Fairness, AutoAdd: session.AutoAdd, JoinOpen: session.JoinOpen,
		LibraryIDs: append([]store.LibraryID{}, session.LibraryIDs...),
		StartedAt:  PartyTime(session.StartedAt), ExpiresAt: PartyTime(session.ExpiresAt),
		ChangeoverSeconds: view.ChangeoverSeconds,
		Current:           h.partyCurrent(view.Current), Playback: partyPlaybackInfo(view.Playback),
		Entries: h.partyEntryInfos(view.Waiting), Requests: h.partyEntryInfos(view.Requests),
		Recent: h.partyEntryInfos(view.Recent), Removed: h.partyEntryInfos(view.Removed),
		Skipped:           h.partyEntryInfos(view.Skipped),
		Participants:      make([]PartyParticipantInfo, len(view.Participants)),
		Screens:           make([]PartyScreenInfo, len(view.Screens)),
		AvailableDisplays: h.partyAvailableDisplays(ctx, session.HostProfileID, session.LibraryIDs, view.Screens),
	}
	for i, participant := range view.Participants {
		info.Participants[i] = PartyParticipantInfo{
			ID: participant.ID, Name: participant.Name, Guest: participant.Guest,
			Waiting: participant.Waiting, LastTurnAt: PartyTime(participant.LastTurnAt),
		}
	}
	for i, screen := range view.Screens {
		info.Screens[i] = h.partyScreenInfo(screen)
	}
	return info
}

func (h *Handler) partyAvailableDisplays(ctx context.Context, hostProfileID store.ProfileID, partyLibraries []store.LibraryID, joinedScreens []store.PartyScreenView) []PartyAvailableDisplayInfo {
	out := []PartyAvailableDisplayInfo{}
	if hostProfileID == "" || h.store == nil {
		return out
	}
	bound, err := h.store.PlayerBoundDevices(ctx)
	if err != nil || len(bound) == 0 {
		return out
	}
	joinedByDevice := make(map[store.DeviceID]bool, len(joinedScreens))
	for _, scr := range joinedScreens {
		if scr.DeviceID != "" {
			joinedByDevice[scr.DeviceID] = true
		}
	}
	now := h.now().UTC()
	activeBySession := map[store.SessionID]bool{}
	if granted, err := h.store.GrantedSessions(ctx, now, store.LibraryFilter{AllLibraries: true, Revealed: true}); err == nil {
		for _, g := range granted {
			activeBySession[g.SessionID] = true
		}
	}
	for _, d := range bound {
		ok, err := h.store.IsPlayerController(ctx, d.PlayerProfileID, hostProfileID)
		if err != nil || !ok {
			continue
		}
		displayName := d.DisplayName
		if displayName == "" {
			displayName = d.DeviceName
		}
		sessionLive := d.SessionID != "" && (d.SessionExpiresAt.IsZero() || d.SessionExpiresAt.After(now))
		online := sessionLive && h.isPlayerSessionOnline(d.SessionID, now)
		presence := playerPresenceOffline
		if online {
			if activeBySession[d.SessionID] {
				presence = playerPresencePlaying
			} else {
				presence = playerPresenceReady
			}
		}
		covers, missing, covErr := h.store.PartyLibraryCoverage(ctx, d.PlayerProfileID, partyLibraries)
		if covErr != nil {
			continue
		}
		if missing == nil {
			missing = []store.LibraryID{}
		}
		out = append(out, PartyAvailableDisplayInfo{
			Handle:            h.playerDisplayHandle(d.DeviceID),
			DisplayName:       displayName,
			Presence:          presence,
			Joined:            joinedByDevice[d.DeviceID],
			CoversLibraries:   covers,
			MissingLibraryIDs: missing,
		})
	}
	slices.SortFunc(out, func(a, b PartyAvailableDisplayInfo) int {
		if a.DisplayName != b.DisplayName {
			return strings.Compare(a.DisplayName, b.DisplayName)
		}
		return strings.Compare(a.Handle, b.Handle)
	})
	return out
}

func (h *Handler) partyScreenPlaybackFilters(ctx context.Context, partySessionID string) map[string]store.LibraryFilter {
	if partySessionID == "" || h.store == nil {
		return nil
	}
	screens, err := h.store.PartyActiveScreens(ctx, partySessionID)
	if err != nil || len(screens) == 0 {
		return nil
	}
	filters := make(map[string]store.LibraryFilter, len(screens))
	for _, scr := range screens {
		if scr.DeviceID != "" && h.auth != nil {
			if pb, err := h.auth.PlayerProfilePlaybackFilter(ctx, scr.ProfileID); err == nil {
				if sealed, serr := h.sealedFilter(ctx, pb); serr == nil {
					filters[scr.ID] = sealed
				}
			}
			continue
		}
		if scr.AuthSessionID != "" && h.auth != nil {
			if sc, err := h.auth.Scope(ctx, scr.AuthSessionID); err == nil {
				if sealed, serr := h.sealedFilter(ctx, sc.PlaybackFilter()); serr == nil {
					filters[scr.ID] = sealed
				}
			}
		}
	}
	return filters
}

func (h *Handler) partyHostFilter(ctx context.Context, scope auth.Scope) (store.LibraryFilter, error) {
	return h.sealedFilter(ctx, scope.LibraryFilter())
}

// partySessionRead resolves partySession: the caller's live session or null.
func (h *Handler) partySessionRead(ctx context.Context, scope auth.Scope) (*PartySessionInfo, error) {
	if scope.IsPlayerBound() {
		return nil, clientError("player display cannot host a party")
	}
	filter, err := h.partyHostFilter(ctx, scope)
	if err != nil {
		return nil, err
	}
	view, ok, err := h.store.PartySessionForHost(ctx, scope.ProfileID, filter)
	if err != nil || !ok {
		return nil, err
	}
	if screenFilters := h.partyScreenPlaybackFilters(ctx, view.Session.ID); len(screenFilters) > 0 {
		view, ok, err = h.store.PartySessionForHostWithFilters(ctx, scope.ProfileID, filter, screenFilters)
		if err != nil || !ok {
			return nil, err
		}
	}
	return h.partySessionInfo(ctx, view), nil
}

// partyStageRead resolves partyStage for the caller's own screen registration.
func (h *Handler) partyStageRead(ctx context.Context, scope auth.Scope, req request) (PartyStageInfo, error) {
	sessionID := trimmedVariable(req, "sessionID")
	if sessionID == "" {
		return PartyStageInfo{}, clientError("sessionID is required")
	}
	screenID := trimmedVariable(req, "screenID")
	if screenID == "" {
		return PartyStageInfo{}, clientError("screenID is required")
	}
	libraryFilter, err := h.sealedFilter(ctx, scope.LibraryFilter())
	if err != nil {
		return PartyStageInfo{}, err
	}
	playbackFilter, err := h.sealedFilter(ctx, scope.PlaybackFilter())
	if err != nil {
		return PartyStageInfo{}, err
	}
	screenFilters := h.partyScreenPlaybackFilters(ctx, sessionID)
	stage, err := h.store.PartyStage(ctx, sessionID, screenID, scope.SessionID, libraryFilter, playbackFilter, screenFilters)
	if err != nil {
		return PartyStageInfo{}, partyError(err)
	}
	return PartyStageInfo{
		SessionID: stage.SessionID, ServerNowMS: stage.ServerNowMS,
		Current: h.partyCurrent(stage.Current), Playback: partyPlaybackInfo(stage.Playback),
		UpNext: h.partyEntryInfos(stage.UpNext), Ticker: stage.Ticker, Lead: stage.Lead,
		JoinCode: stage.JoinCode, JoinPath: PartyJoinPath(stage.JoinCode),
	}, nil
}

// partyHostWrite runs one host mutation under the expected-Profile guard and
// returns the host's session view after it.
func (h *Handler) partyHostWrite(ctx context.Context, scope auth.Scope, req request, write func(store.ProfileID, store.LibraryFilter) error) (*PartySessionInfo, error) {
	if scope.IsPlayerBound() {
		return nil, clientError("player display cannot host a party")
	}
	err := h.withExpectedProfile(ctx, scope, req, func() error {
		filter, err := h.partyHostFilter(ctx, scope)
		if err != nil {
			return err
		}
		return partyError(write(scope.ProfileID, filter))
	})
	if err != nil {
		return nil, err
	}
	info, err := h.partySessionRead(ctx, scope)
	if err == nil && info == nil {
		err = clientError("party session not found")
	}
	return info, err
}

// partyStartWrite resolves partyStart; a live session is returned unchanged.
func (h *Handler) partyStartWrite(ctx context.Context, scope auth.Scope, req request) (*PartySessionInfo, error) {
	return h.partyHostWrite(ctx, scope, req, func(host store.ProfileID, filter store.LibraryFilter) error {
		v := newVars(req)
		libraries, _ := v.StringList("libraryIDs", "Library IDs")
		fairness, _ := v.String("fairness")
		autoAdd, _ := v.Bool("autoAdd")
		if err := v.Err(); err != nil {
			return err
		}
		ids := make([]store.LibraryID, 0, len(libraries))
		for _, id := range libraries {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, store.LibraryID(id))
			}
		}
		if len(ids) == 0 {
			return clientError("libraryIDs is required")
		}
		hostName := ""
		if profile, err := h.store.Profile(ctx, host); err == nil {
			hostName = profile.Name
		}
		_, err := h.store.StartPartySession(ctx, store.PartyStartInput{
			Host: host, HostName: hostName, LibraryIDs: ids, Fairness: fairness, AutoAdd: autoAdd, Filter: filter,
		})
		return err
	})
}

// partyUpdateWrite resolves partyUpdate.
func (h *Handler) partyUpdateWrite(ctx context.Context, scope auth.Scope, req request) (*PartySessionInfo, error) {
	return h.partyHostWrite(ctx, scope, req, func(host store.ProfileID, filter store.LibraryFilter) error {
		v := newVars(req)
		var input store.PartyUpdateInput
		if fairness, ok := v.String("fairness"); ok {
			input.Fairness = &fairness
		}
		if autoAdd, ok := v.Bool("autoAdd"); ok {
			input.AutoAdd = &autoAdd
		}
		if joinOpen, ok := v.Bool("joinOpen"); ok {
			input.JoinOpen = &joinOpen
		}
		if err := v.Err(); err != nil {
			return err
		}
		return h.store.UpdatePartySession(ctx, host, filter, input)
	})
}

// partyRotateCodeWrite resolves partyRotateCode.
func (h *Handler) partyRotateCodeWrite(ctx context.Context, scope auth.Scope, req request) (*PartySessionInfo, error) {
	return h.partyHostWrite(ctx, scope, req, func(host store.ProfileID, filter store.LibraryFilter) error {
		return h.store.RotatePartyJoinCode(ctx, host, filter)
	})
}

// partyAddWrite resolves partyAdd: the host adds an entry for a participant.
func (h *Handler) partyAddWrite(ctx context.Context, scope auth.Scope, req request) (*PartySessionInfo, error) {
	return h.partyHostWrite(ctx, scope, req, func(host store.ProfileID, filter store.LibraryFilter) error {
		return h.store.AddPartyEntry(ctx, host, filter, store.MediaID(trimmedVariable(req, "mediaID")),
			trimmedVariable(req, "participantID"), trimmedVariable(req, "participantName"))
	})
}

// partyReviewWrite resolves partyReview: approve or decline a pending request.
func (h *Handler) partyReviewWrite(ctx context.Context, scope auth.Scope, req request) (*PartySessionInfo, error) {
	return h.partyHostWrite(ctx, scope, req, func(host store.ProfileID, filter store.LibraryFilter) error {
		v := newVars(req)
		approve, ok := v.Bool("approve")
		if err := v.Err(); err != nil {
			return err
		}
		if !ok {
			return clientError("approve is required")
		}
		return h.store.ReviewPartyEntry(ctx, host, filter, trimmedVariable(req, "entryID"), approve)
	})
}

// partyMoveWrite resolves partyMove: pin a waiting entry at a 1-based position.
func (h *Handler) partyMoveWrite(ctx context.Context, scope auth.Scope, req request) (*PartySessionInfo, error) {
	return h.partyHostWrite(ctx, scope, req, func(host store.ProfileID, filter store.LibraryFilter) error {
		v := newVars(req)
		position, ok := v.Number("position")
		if err := v.Err(); err != nil {
			return err
		}
		if !ok || position < 1 || position > math.MaxInt32 || position != math.Trunc(position) {
			return clientError("position must be a positive integer")
		}
		return h.store.MovePartyEntry(ctx, host, filter, trimmedVariable(req, "entryID"), int(position))
	})
}

// partyRemoveWrite resolves partyRemove.
func (h *Handler) partyRemoveWrite(ctx context.Context, scope auth.Scope, req request) (*PartySessionInfo, error) {
	return h.partyHostWrite(ctx, scope, req, func(host store.ProfileID, filter store.LibraryFilter) error {
		return h.store.RemovePartyEntry(ctx, host, filter, trimmedVariable(req, "entryID"))
	})
}

// partyRestoreWrite resolves partyRestore.
func (h *Handler) partyRestoreWrite(ctx context.Context, scope auth.Scope, req request) (*PartySessionInfo, error) {
	return h.partyHostWrite(ctx, scope, req, func(host store.ProfileID, filter store.LibraryFilter) error {
		return h.store.RestorePartyEntry(ctx, host, filter, trimmedVariable(req, "entryID"))
	})
}

// partyAdvanceWrite resolves partyAdvance; a stale fromEntryID is a no-op. A
// Profile advancing another host's session through its live screen gets null,
// since it hosts no session of its own.
func (h *Handler) partyAdvanceWrite(ctx context.Context, scope auth.Scope, req request) (*PartySessionInfo, error) {
	err := h.withExpectedProfile(ctx, scope, req, func() error {
		filter, err := h.partyHostFilter(ctx, scope)
		if err != nil {
			return err
		}
		return partyError(h.store.AdvanceParty(ctx, scope.ProfileID, filter, trimmedVariable(req, "fromEntryID")))
	})
	if err != nil {
		return nil, err
	}
	return h.partySessionRead(ctx, scope)
}

// partyPlaybackWrite resolves partyPlayback: the lead screen's anchor.
func (h *Handler) partyPlaybackWrite(ctx context.Context, scope auth.Scope, req request) (*PartySessionInfo, error) {
	return h.partyHostWrite(ctx, scope, req, func(host store.ProfileID, filter store.LibraryFilter) error {
		v := newVars(req)
		input := store.PartyPlaybackInput{EntryID: trimmedVariable(req, "entryID")}
		input.State, _ = v.String("state")
		positionMS, hasPosition := v.Number("positionMS")
		if tempo, ok := v.Number("tempo"); ok {
			input.Tempo = &tempo
		}
		pitch, hasPitch := v.Number("pitchSemitones")
		if err := v.Err(); err != nil {
			return err
		}
		if !hasPosition || positionMS < 0 || positionMS > 1<<53-1 || positionMS != math.Trunc(positionMS) {
			return clientError("positionMS must be a nonnegative integer")
		}
		input.PositionMS = int64(positionMS)
		if hasPitch {
			if pitch != math.Trunc(pitch) || pitch < -12 || pitch > 12 {
				return clientError("pitchSemitones must be an integer from -12 to 12")
			}
			semitones := int(pitch)
			input.PitchSemitones = &semitones
		}
		return h.store.SetPartyPlayback(ctx, host, filter, input)
	})
}

// partyRemoveParticipantWrite resolves partyRemoveParticipant.
func (h *Handler) partyRemoveParticipantWrite(ctx context.Context, scope auth.Scope, req request) (*PartySessionInfo, error) {
	return h.partyHostWrite(ctx, scope, req, func(host store.ProfileID, filter store.LibraryFilter) error {
		return h.store.RemovePartyParticipant(ctx, host, filter, trimmedVariable(req, "participantID"))
	})
}

// partyScreenTickerWrite resolves partyScreenTicker.
func (h *Handler) partyScreenTickerWrite(ctx context.Context, scope auth.Scope, req request) (*PartySessionInfo, error) {
	return h.partyHostWrite(ctx, scope, req, func(host store.ProfileID, filter store.LibraryFilter) error {
		v := newVars(req)
		ticker, ok := v.Bool("ticker")
		if err := v.Err(); err != nil {
			return err
		}
		if !ok {
			return clientError("ticker is required")
		}
		return h.store.SetPartyScreenTicker(ctx, host, filter, trimmedVariable(req, "screenID"), ticker)
	})
}

// partyScreenDropWrite resolves partyScreenDrop.
func (h *Handler) partyScreenDropWrite(ctx context.Context, scope auth.Scope, req request) (*PartySessionInfo, error) {
	return h.partyHostWrite(ctx, scope, req, func(host store.ProfileID, filter store.LibraryFilter) error {
		return h.store.DropPartyScreen(ctx, host, filter, trimmedVariable(req, "screenID"))
	})
}

// partyEndWrite resolves partyEnd; it reports whether a session was live.
func (h *Handler) partyEndWrite(ctx context.Context, scope auth.Scope, req request) (bool, error) {
	if scope.IsPlayerBound() {
		return false, clientError("player display cannot host a party")
	}
	ended := false
	err := h.withExpectedProfile(ctx, scope, req, func() error {
		var err error
		ended, err = h.store.EndPartySession(ctx, scope.ProfileID)
		return err
	})
	return ended, err
}

// partyScreenRegisterWrite resolves partyScreenRegister for any non-player signed-in Profile.
func (h *Handler) partyScreenRegisterWrite(ctx context.Context, scope auth.Scope, req request) (PartyScreenInfo, error) {
	if scope.IsPlayerBound() {
		return PartyScreenInfo{}, clientError("player display cannot register a stage screen")
	}
	var screen store.PartyScreenView
	err := h.withExpectedProfile(ctx, scope, req, func() error {
		var err error
		screen, err = h.store.RegisterPartyScreen(ctx, trimmedVariable(req, "sessionID"), scope.ProfileID, scope.SessionID,
			trimmedVariable(req, "name"), trimmedVariable(req, "kind"), trimmedVariable(req, "carrier"))
		return partyError(err)
	})
	if err != nil {
		return PartyScreenInfo{}, err
	}
	return h.partyScreenInfo(screen), nil
}

// partyScreenReportWrite resolves partyScreenReport for the registering Session.
func (h *Handler) partyScreenReportWrite(ctx context.Context, scope auth.Scope, req request) (bool, error) {
	v := newVars(req)
	offsetMS, hasOffset := v.Number("offsetMS")
	rttMS, hasRTT := v.Number("rttMS")
	if err := v.Err(); err != nil {
		return false, err
	}
	if !hasOffset || offsetMS != math.Trunc(offsetMS) || math.Abs(offsetMS) > 1<<40 {
		return false, clientError("offsetMS must be an integer")
	}
	if !hasRTT || rttMS != math.Trunc(rttMS) || rttMS < 0 || rttMS > 1<<40 {
		return false, clientError("rttMS must be a nonnegative integer")
	}
	err := h.withExpectedProfile(ctx, scope, req, func() error {
		return partyError(h.store.ReportPartyScreen(ctx, trimmedVariable(req, "screenID"), scope.ProfileID, scope.SessionID, int64(offsetMS), int64(rttMS)))
	})
	return err == nil, err
}

// partyScreenAddDisplayWrite joins a controlled player display to the host's
// live party as a follower and reports Library coverage on the join result.
func (h *Handler) partyScreenAddDisplayWrite(ctx context.Context, scope auth.Scope, req request) (PartyScreenJoinResult, error) {
	if scope.IsPlayerBound() {
		return PartyScreenJoinResult{}, clientError("player display cannot host a party")
	}
	handle := trimmedVariable(req, "handle")
	if handle == "" {
		return PartyScreenJoinResult{}, clientError("handle is required")
	}
	carrier := trimmedVariable(req, "carrier")
	var joinView store.PartyScreenJoinView
	err := h.withExpectedProfile(ctx, scope, req, func() error {
		filter, err := h.partyHostFilter(ctx, scope)
		if err != nil {
			return err
		}
		bound, err := h.store.PlayerBoundDevices(ctx)
		if err != nil {
			return err
		}
		var target *store.PlayerDisplay
		for i := range bound {
			if h.playerDisplayHandle(bound[i].DeviceID) == handle {
				target = &bound[i]
				break
			}
		}
		if target == nil {
			return clientError("display not found")
		}
		isCtrl, err := h.store.IsPlayerController(ctx, target.PlayerProfileID, scope.ProfileID)
		if err != nil {
			return err
		}
		if !isCtrl {
			return clientError("display not found")
		}
		now := h.now().UTC()
		sessionLive := target.SessionID != "" && (target.SessionExpiresAt.IsZero() || target.SessionExpiresAt.After(now))
		if !sessionLive || !h.isPlayerSessionOnline(target.SessionID, now) {
			return reasonedClientError{message: "display offline", reason: ErrorReasonDisplayOffline}
		}
		joinView, err = h.store.AddPartyPlayerDisplay(ctx, scope.ProfileID, filter, *target, carrier)
		return partyError(err)
	})
	if err != nil {
		return PartyScreenJoinResult{}, err
	}
	sessionInfo, err := h.partySessionRead(ctx, scope)
	if err == nil && sessionInfo == nil {
		err = clientError("party session not found")
	}
	if err != nil {
		return PartyScreenJoinResult{}, err
	}
	missing := joinView.MissingLibraryIDs
	if missing == nil {
		missing = []store.LibraryID{}
	}
	return PartyScreenJoinResult{
		Screen:            h.partyScreenInfo(joinView.Screen),
		Session:           sessionInfo,
		CoversLibraries:   joinView.CoversLibraries,
		MissingLibraryIDs: missing,
	}, nil
}

// partyScreenLeadWrite chooses screenID as the lead screen if eligible for the
// current entry.
func (h *Handler) partyScreenLeadWrite(ctx context.Context, scope auth.Scope, req request) (*PartySessionInfo, error) {
	return h.partyHostWrite(ctx, scope, req, func(host store.ProfileID, filter store.LibraryFilter) error {
		screenID := trimmedVariable(req, "screenID")
		if screenID == "" {
			return clientError("screenID is required")
		}
		view, ok, err := h.store.PartySessionForHost(ctx, host, filter)
		if err != nil {
			return err
		}
		if !ok {
			return store.ErrPartyNotFound
		}
		screenFilters := h.partyScreenPlaybackFilters(ctx, view.Session.ID)
		return h.store.SetPartyScreenLead(ctx, host, filter, screenID, screenFilters)
	})
}
