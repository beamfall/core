package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/store"
)

// PartyGuestHeader carries a party guest capability. Only the guest party
// routes read it; no other route accepts a guest token.
const PartyGuestHeader = "X-Beamfall-Party-Guest"

const (
	partyJoinAttemptAction    = "party.join"
	partyRequestAttemptAction = "party.request"
	partyGuestSearchLimit     = 30
)

// Join attempts back off per source address and per code; a successful join
// clears both lanes so guests sharing one address are not throttled.
var partyJoinBackoff = store.AuthBackoff{
	Window: 10 * time.Minute, FreeAttempts: 10, BaseDelay: 2 * time.Second, MaxDelay: 10 * time.Minute,
}

// Guest requests back off per participant and never clear on success, so a
// guest cannot flood the host's review list.
var partyRequestBackoff = store.AuthBackoff{
	Window: time.Minute, FreeAttempts: 3, BaseDelay: 5 * time.Second, MaxDelay: time.Minute,
}

// PartyGuestStore is the store surface the guest party routes use.
type PartyGuestStore interface {
	JoinParty(ctx context.Context, code, name, tokenHash string) (store.PartyGuest, error)
	PartyGuestView(ctx context.Context, tokenHash string) (store.PartyGuest, store.PartyView, error)
	SearchPartyMedia(ctx context.Context, tokenHash, query string, limit int) ([]store.PartyMediaView, error)
	RequestPartyEntry(ctx context.Context, tokenHash string, mediaID store.MediaID, pitchSemitones int) (store.PartyEntryView, error)
	CancelPartyEntry(ctx context.Context, tokenHash, entryID string) error
	ReserveAuthAttempt(ctx context.Context, action string, keys []string, now time.Time, policy store.AuthBackoff) (store.AuthAttemptReservation, time.Time, error)
	RecordAuthAttempt(ctx context.Context, attempt store.AuthAttempt) error
}

// SetPartyGuestStore wires the guest party routes; unwired, they answer 503.
func (s *Server) SetPartyGuestStore(party PartyGuestStore) {
	s.partyGuests = party
}

type partyGuestEntry struct {
	ID              string `json:"id"`
	MediaID         string `json:"mediaID,omitempty"`
	Title           string `json:"title"`
	Subtitle        string `json:"subtitle"`
	ParticipantName string `json:"participantName"`
	State           string `json:"state"`
	Position        int    `json:"position"`
	ETASeconds      int    `json:"etaSeconds"`
	PitchSemitones  int    `json:"pitchSemitones"`
}

func newPartyGuestEntry(entry store.PartyEntryView, withMedia bool) partyGuestEntry {
	out := partyGuestEntry{
		ID: entry.ID, Title: entry.Title, Subtitle: entry.Subtitle, ParticipantName: entry.ParticipantName,
		State: entry.State, Position: entry.Position, ETASeconds: entry.ETASeconds, PitchSemitones: entry.PitchSemitones,
	}
	if withMedia {
		out.MediaID = string(entry.MediaID)
	}
	return out
}

type partyGuestViewResponse struct {
	SessionID  string            `json:"sessionID"`
	JoinCode   string            `json:"joinCode"`
	AutoAdd    bool              `json:"autoAdd"`
	Current    *partyGuestEntry  `json:"current"`
	Entries    []partyGuestEntry `json:"entries"`
	Mine       []partyGuestEntry `json:"mine"`
	MaxWaiting int               `json:"maxWaiting"`
	ExpiresAt  string            `json:"expiresAt"`
}

type partyGuestMedia struct {
	MediaID         string `json:"mediaID"`
	Title           string `json:"title"`
	Subtitle        string `json:"subtitle"`
	DurationSeconds int    `json:"durationSeconds"`
	ArtworkURL      string `json:"artworkURL"`
	LastPlayedAt    string `json:"lastPlayedAt"`
}

func partyGuestTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// newPartyGuestToken returns a fresh guest token and the SHA-256 hash the
// store keeps in its place.
func newPartyGuestToken() (token, hash string, err error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", err
	}
	token = store.PartyGuestTokenPrefix + base64.RawURLEncoding.EncodeToString(raw[:])
	return token, partyGuestTokenHash(token), nil
}

func partyGuestTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Server) partyGuestStore(w http.ResponseWriter, r *http.Request) (PartyGuestStore, bool) {
	// Guest responses carry a bearer capability's private view; never cache them.
	w.Header().Set("Cache-Control", "no-store")
	if s.partyGuests == nil {
		writeError(w, r, http.StatusServiceUnavailable, "party sessions unavailable")
		return nil, false
	}
	return s.partyGuests, true
}

// partyGuestRequestHash reads the guest capability and returns its hash; a
// missing or malformed token answers 401 like an ended session.
func partyGuestRequestHash(w http.ResponseWriter, r *http.Request) (string, bool) {
	token := strings.TrimSpace(r.Header.Get(PartyGuestHeader))
	if !strings.HasPrefix(token, store.PartyGuestTokenPrefix) || len(token) > 128 {
		writeError(w, r, http.StatusUnauthorized, store.ErrPartyGuestRefused.Error())
		return "", false
	}
	return partyGuestTokenHash(token), true
}

// writePartyGuestError maps store refusals to the guest error bodies.
func writePartyGuestError(w http.ResponseWriter, r *http.Request, err error) {
	var input store.PartyInputError
	switch {
	case errors.Is(err, store.ErrPartyGuestRefused):
		writeError(w, r, http.StatusUnauthorized, store.ErrPartyGuestRefused.Error())
	case errors.Is(err, store.ErrPartyJoinRefused):
		writeError(w, r, http.StatusNotFound, store.ErrPartyJoinRefused.Error())
	case errors.Is(err, store.ErrPartyFull):
		writeError(w, r, http.StatusConflict, "full")
	case errors.Is(err, store.ErrPartyWaitingLimit):
		writeError(w, r, http.StatusConflict, "waiting-limit")
	case errors.Is(err, store.ErrPartyNotFound):
		writeError(w, r, http.StatusNotFound, "entry not found")
	case errors.As(err, &input):
		writeError(w, r, http.StatusBadRequest, input.Error())
	case errors.Is(err, store.ErrPartyInvalid):
		writeError(w, r, http.StatusBadRequest, store.ErrPartyInvalid.Error())
	default:
		writeErrorCause(w, r, http.StatusInternalServerError, "party request failed", err)
	}
}

func writePartyRateLimited(w http.ResponseWriter, r *http.Request, now, until time.Time) {
	seconds := int(math.Ceil(until.Sub(now).Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeError(w, r, http.StatusTooManyRequests, "rate-limited")
}

// reservePartyAttempt reserves one attempt on every lane, writing the 429 or
// 500 itself and returning false when the request must stop.
func reservePartyAttempt(w http.ResponseWriter, r *http.Request, party PartyGuestStore, action string, keys []string, policy store.AuthBackoff) bool {
	now := time.Now().UTC()
	_, lockedUntil, err := party.ReserveAuthAttempt(r.Context(), action, keys, now, policy)
	if err != nil {
		writeErrorCause(w, r, http.StatusInternalServerError, "party request failed", err)
		return false
	}
	if !lockedUntil.IsZero() {
		writePartyRateLimited(w, r, now, lockedUntil)
		return false
	}
	return true
}

func (s *Server) partyJoin(w http.ResponseWriter, r *http.Request) {
	party, ok := s.partyGuestStore(w, r)
	if !ok {
		return
	}
	var req struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxControlBody)).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	address := "unknown"
	if ip, ok := remoteIP(r.RemoteAddr); ok {
		address = ip.String()
	}
	codeSum := sha256.Sum256([]byte(store.NormalizePartyJoinCode(req.Code)))
	keys := []string{"party-join:addr:" + address, "party-join:code:" + hex.EncodeToString(codeSum[:])}
	if !reservePartyAttempt(w, r, party, partyJoinAttemptAction, keys, partyJoinBackoff) {
		return
	}
	token, hash, err := newPartyGuestToken()
	if err != nil {
		writeErrorCause(w, r, http.StatusInternalServerError, "party request failed", err)
		return
	}
	guest, err := party.JoinParty(r.Context(), req.Code, req.Name, hash)
	if err != nil {
		writePartyGuestError(w, r, err)
		return
	}
	for _, key := range keys {
		attempt := store.AuthAttempt{OccurredAt: time.Now().UTC(), Action: partyJoinAttemptAction, Key: key, Success: true}
		if err := party.RecordAuthAttempt(r.Context(), attempt); err != nil {
			logRequestFailure(r.Context(), r, http.StatusOK, "party join success not recorded", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"token": token, "sessionID": guest.SessionID, "participantID": guest.ParticipantID,
		"name": guest.Name, "expiresAt": partyGuestTime(guest.ExpiresAt),
	})
}

func (s *Server) partyGuestView(w http.ResponseWriter, r *http.Request) {
	party, ok := s.partyGuestStore(w, r)
	if !ok {
		return
	}
	hash, ok := partyGuestRequestHash(w, r)
	if !ok {
		return
	}
	guest, view, err := party.PartyGuestView(r.Context(), hash)
	if err != nil {
		writePartyGuestError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newPartyGuestViewResponse(guest, view))
}

func newPartyGuestViewResponse(guest store.PartyGuest, view store.PartyView) partyGuestViewResponse {
	out := partyGuestViewResponse{
		SessionID: view.Session.ID, JoinCode: view.Session.JoinCode, AutoAdd: view.Session.AutoAdd,
		Entries: []partyGuestEntry{}, Mine: []partyGuestEntry{}, MaxWaiting: store.PartyMaxGuestWaiting,
		ExpiresAt: partyGuestTime(guest.ExpiresAt),
	}
	if view.Current != nil {
		current := newPartyGuestEntry(*view.Current, false)
		out.Current = &current
	}
	for _, entry := range view.Waiting {
		out.Entries = append(out.Entries, newPartyGuestEntry(entry, false))
	}
	for _, bucket := range [][]store.PartyEntryView{view.Requests, view.Waiting, view.Declined} {
		for _, entry := range bucket {
			if entry.ParticipantID == guest.ParticipantID {
				out.Mine = append(out.Mine, newPartyGuestEntry(entry, true))
			}
		}
	}
	return out
}

func (s *Server) partyGuestSearch(w http.ResponseWriter, r *http.Request) {
	party, ok := s.partyGuestStore(w, r)
	if !ok {
		return
	}
	hash, ok := partyGuestRequestHash(w, r)
	if !ok {
		return
	}
	results, err := party.SearchPartyMedia(r.Context(), hash, strings.TrimSpace(r.URL.Query().Get("q")), partyGuestSearchLimit)
	if err != nil {
		writePartyGuestError(w, r, err)
		return
	}
	items := make([]partyGuestMedia, 0, len(results))
	for _, result := range results {
		items = append(items, partyGuestMedia{
			MediaID: string(result.MediaID), Title: result.Title, Subtitle: result.Subtitle,
			DurationSeconds: result.DurationSeconds, LastPlayedAt: partyGuestTime(result.LastPlayedAt),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) partyGuestRequest(w http.ResponseWriter, r *http.Request) {
	party, ok := s.partyGuestStore(w, r)
	if !ok {
		return
	}
	hash, ok := partyGuestRequestHash(w, r)
	if !ok {
		return
	}
	var req struct {
		MediaID        string `json:"mediaID"`
		PitchSemitones int    `json:"pitchSemitones"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxControlBody)).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	guest, _, err := party.PartyGuestView(r.Context(), hash)
	if err != nil {
		writePartyGuestError(w, r, err)
		return
	}
	if !reservePartyAttempt(w, r, party, partyRequestAttemptAction, []string{"party-request:" + guest.ParticipantID}, partyRequestBackoff) {
		return
	}
	created, err := party.RequestPartyEntry(r.Context(), hash, store.MediaID(req.MediaID), req.PitchSemitones)
	if err != nil {
		writePartyGuestError(w, r, err)
		return
	}
	guest, view, err := party.PartyGuestView(r.Context(), hash)
	if err != nil {
		writePartyGuestError(w, r, err)
		return
	}
	// The entry may already have left Mine (made current, or advanced past).
	entry := newPartyGuestEntry(created, true)
	for _, mine := range newPartyGuestViewResponse(guest, view).Mine {
		if mine.ID == created.ID {
			entry = mine
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entry": entry})
}

func (s *Server) partyGuestCancel(w http.ResponseWriter, r *http.Request) {
	party, ok := s.partyGuestStore(w, r)
	if !ok {
		return
	}
	hash, ok := partyGuestRequestHash(w, r)
	if !ok {
		return
	}
	var req struct {
		EntryID string `json:"entryID"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxControlBody)).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if err := party.CancelPartyEntry(r.Context(), hash, req.EntryID); err != nil {
		writePartyGuestError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
