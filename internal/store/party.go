package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/beamfall/core/internal/partyqueue"
)

// Party session (CORE-1662): a host Profile runs a shared rotation that
// guests join with a short code. Rotation order and ETAs are computed on read
// by partyqueue from the stored entries, pins and turn history.

const (
	// PartyLifetime bounds a session and every guest token it issues.
	PartyLifetime = 12 * time.Hour
	// PartyMaxGuests caps the guests one session admits.
	PartyMaxGuests = 50
	// PartyMaxGuestWaiting caps a guest's pending and waiting entries.
	PartyMaxGuestWaiting = 3
	// PartyUpNext is how many waiting entries a stage screen shows.
	PartyUpNext = 3
	// PartyGuestTokenPrefix marks a guest capability. It never authenticates
	// any route other than the guest party routes.
	PartyGuestTokenPrefix = "bfg_"

	partyRecentLimit    = 20
	partyRemovedWindow  = 10 * time.Minute
	partyScreenSilence  = 2 * time.Minute
	partyNameMaxRunes   = 40
	partyCodeLength     = 4
	partyCodeAlphabet   = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
	partyCodeAttempts   = 32
	partyRetainEndedFor = 7 * 24 * time.Hour
)

// Entry states.
const (
	PartyEntryPending  = "pending"
	PartyEntryWaiting  = "waiting"
	PartyEntryCurrent  = "current"
	PartyEntryDone     = "done"
	PartyEntryRemoved  = "removed"
	PartyEntryDeclined = "declined"
	PartyEntrySkipped  = "skipped"
)

var (
	// ErrPartyNotFound reports no live session, entry, participant or screen
	// for the request.
	ErrPartyNotFound = errors.New("party session not found")
	// ErrPartyJoinRefused answers every unusable join code alike.
	ErrPartyJoinRefused = errors.New("join code not recognised")
	// ErrPartyFull reports a session at PartyMaxGuests.
	ErrPartyFull = errors.New("party session is full")
	// ErrPartyWaitingLimit reports a guest at PartyMaxGuestWaiting.
	ErrPartyWaitingLimit = errors.New("party waiting limit reached")
	// ErrPartyGuestRefused reports a revoked, expired or ended guest token.
	ErrPartyGuestRefused = errors.New("party session ended")
	// ErrPartyInvalid marks a rejected argument; PartyInputError carries the
	// client-facing message.
	ErrPartyInvalid = errors.New("invalid party request")
	// ErrDisplayInParty reports a D4 command (other than stop) targeting a
	// player display that is currently joined to a live party session.
	ErrDisplayInParty = errors.New("display in party")
	// ErrPartyDisplayAlreadyJoined reports adding a player display that is
	// already active in another live party session.
	ErrPartyDisplayAlreadyJoined = errors.New("display already in a party")
	// ErrPartyScreenIneligibleLead reports choosing a screen as lead that
	// cannot play the party's current entry.
	ErrPartyScreenIneligibleLead = errors.New("screen cannot play the current entry")
)

// PartyInputError is a rejected party argument with a client-safe message.
type PartyInputError struct{ Message string }

func (e PartyInputError) Error() string        { return e.Message }
func (e PartyInputError) Is(target error) bool { return target == ErrPartyInvalid }

func partyInvalid(message string) error { return PartyInputError{Message: message} }

// PartySession is one stored session row.
type PartySession struct {
	ID             string
	HostProfileID  ProfileID
	JoinCode       string
	Fairness       string
	AutoAdd        bool
	JoinOpen       bool
	LibraryIDs     []LibraryID
	StartedAt      time.Time
	ExpiresAt      time.Time
	CurrentEntryID string
	PlaybackState  string
	PositionMS     int64
	AnchorAtMS     int64
	Tempo          float64
	PitchSemitones int

	hostFilter LibraryFilter
}

// PartyEntryView is an entry with its Media facts resolved.
type PartyEntryView struct {
	ID              string
	MediaID         MediaID
	Title           string
	Subtitle        string
	DurationSeconds int
	ParticipantID   string
	ParticipantName string
	State           string
	Pinned          bool
	Position        int
	ETASeconds      int
	PitchSemitones  int
	AddedAt         time.Time
	LastPlayedAt    time.Time
	Ineligible      bool
	Label           string
}

// PartyParticipantView is one participant still in the session.
type PartyParticipantView struct {
	ID         string
	Name       string
	Guest      bool
	Waiting    int
	LastTurnAt time.Time
}

// PartyPlaybackView is the current entry's shared playback anchor.
type PartyPlaybackView struct {
	EntryID        string
	State          string
	PositionMS     int64
	AnchorAtMS     int64
	Tempo          float64
	PitchSemitones int
}

// PartyScreenView is one active stage screen.
type PartyScreenView struct {
	ID                string
	ProfileID         ProfileID
	AuthSessionID     SessionID
	DeviceID          DeviceID
	BindingGeneration int
	ChosenLead        bool
	Name              string
	Kind              string
	Carrier           string
	Lead              bool
	Ticker            bool
	OffsetMS          int64
	RTTMS             int64
	ReportedAt        time.Time
}

// PartyScreenJoinView is the result of adding a player display to a party.
type PartyScreenJoinView struct {
	Screen            PartyScreenView
	CoversLibraries   bool
	MissingLibraryIDs []LibraryID
}

// PartyView is a session as its host sees it.
type PartyView struct {
	Session           PartySession
	ChangeoverSeconds int
	Current           *PartyEntryView
	Playback          *PartyPlaybackView
	Waiting           []PartyEntryView
	Requests          []PartyEntryView
	Recent            []PartyEntryView
	Removed           []PartyEntryView
	Skipped           []PartyEntryView
	// Declined carries declined requests for the guest view; the host view
	// does not list them.
	Declined     []PartyEntryView
	Participants []PartyParticipantView
	Screens      []PartyScreenView
}

// PartyStageView is what a stage screen renders.
type PartyStageView struct {
	SessionID   string
	ServerNowMS int64
	Current     *PartyEntryView
	Playback    *PartyPlaybackView
	UpNext      []PartyEntryView
	Ticker      bool
	Lead        bool
	JoinCode    string
}

// PartyGuest is an authenticated guest participant.
type PartyGuest struct {
	SessionID     string
	ParticipantID string
	Name          string
	ExpiresAt     time.Time
}

// PartyMediaView is one guest search result.
type PartyMediaView struct {
	MediaID         MediaID
	Title           string
	Subtitle        string
	DurationSeconds int
	LastPlayedAt    time.Time
}

// PartyStartInput starts a session for a host.
type PartyStartInput struct {
	Host       ProfileID
	HostName   string
	LibraryIDs []LibraryID
	Fairness   string
	AutoAdd    bool
	// Filter is the host's own visibility; the session keeps it narrowed to
	// LibraryIDs for guests and screens.
	Filter LibraryFilter
}

// PartyUpdateInput changes host settings; nil fields stay.
type PartyUpdateInput struct {
	Fairness *string
	AutoAdd  *bool
	JoinOpen *bool
}

// PartyPlaybackInput sets the current entry's playback anchor.
type PartyPlaybackInput struct {
	EntryID        string
	State          string
	PositionMS     int64
	Tempo          *float64
	PitchSemitones *int
}

type partyParticipantRow struct {
	id, name    string
	guest, host bool
	removedAtMS int64
}

type partyEntryRow struct {
	id, participantID string
	mediaID           MediaID
	state             string
	pin               int
	pitchSemitones    int
	addedAtMS         int64
	currentAtMS       int64
	playedAtMS        int64
	finishedAtMS      int64
	removedAtMS       int64
}

type partyScreenRow struct {
	id                  string
	profileID           ProfileID
	authSessionID       SessionID
	deviceID            DeviceID
	bindingGeneration   int
	chosenLead          bool
	name, kind, carrier string
	ticker              bool
	offsetMS, rttMS     int64
	registeredAtMS      int64
	reportedAtMS        int64
	droppedAtMS         int64
	lastSeenMS          int64
	active              bool
	lead                bool
}

type partyState struct {
	session      PartySession
	participants map[string]partyParticipantRow
	entries      []partyEntryRow
	screens      []partyScreenRow
}

type partyQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const partySessionColumns = `session_id, host_profile_id, join_code, fairness, auto_add, join_open,
	library_ids, host_filter, started_at_ms, expires_at_ms, COALESCE(current_entry_id, ''),
	playback_state, position_ms, anchor_at_ms, tempo, pitch_semitones`

func scanPartySession(row interface{ Scan(...any) error }) (PartySession, error) {
	var session PartySession
	var libraries, filter string
	var autoAdd, joinOpen int
	var startedMS, expiresMS int64
	err := row.Scan(&session.ID, &session.HostProfileID, &session.JoinCode, &session.Fairness, &autoAdd, &joinOpen,
		&libraries, &filter, &startedMS, &expiresMS, &session.CurrentEntryID,
		&session.PlaybackState, &session.PositionMS, &session.AnchorAtMS, &session.Tempo, &session.PitchSemitones)
	if err != nil {
		return PartySession{}, err
	}
	session.AutoAdd, session.JoinOpen = autoAdd == 1, joinOpen == 1
	session.StartedAt, session.ExpiresAt = millisToTime(startedMS), millisToTime(expiresMS)
	if err := json.Unmarshal([]byte(libraries), &session.LibraryIDs); err != nil {
		return PartySession{}, err
	}
	if err := json.Unmarshal([]byte(filter), &session.hostFilter); err != nil {
		return PartySession{}, err
	}
	return session, nil
}

func newPartyID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

func newPartyJoinCode() (string, error) {
	var raw [partyCodeLength]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	code := make([]byte, partyCodeLength)
	for i, b := range raw {
		// 256 is not a multiple of the alphabet; the slight bias is harmless
		// for a rate-limited, rotatable code.
		code[i] = partyCodeAlphabet[int(b)%len(partyCodeAlphabet)]
	}
	return string(code), nil
}

// NormalizePartyJoinCode upper-cases and trims a typed join code.
func NormalizePartyJoinCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

func partyName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "", partyInvalid("name is required")
	}
	if utf8.RuneCountInString(name) > partyNameMaxRunes {
		return "", partyInvalid("name is too long")
	}
	return name, nil
}

// partyWrite runs fn in one events transaction after ending expired sessions,
// so the live-host and live-code indexes never see a stale session.
func (s *Store) partyWrite(ctx context.Context, fn func(tx *sql.Tx, nowMS int64) error) error {
	return s.partyWriteOutbox(ctx, func(tx *sql.Tx, nowMS int64, _ *bool) error {
		return fn(tx, nowMS)
	})
}

func (s *Store) partyWriteOutbox(ctx context.Context, fn func(tx *sql.Tx, nowMS int64, wroteOutbox *bool) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	return s.partyWriteOutboxLocked(ctx, fn)
}

func (s *Store) partyWriteOutboxLocked(ctx context.Context, fn func(tx *sql.Tx, nowMS int64, wroteOutbox *bool) error) error {
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	nowMS := timeToMillis(s.serverNow())
	wroteOutbox := false
	if err := s.expirePartySessionsTx(ctx, tx, nowMS, &wroteOutbox); err != nil {
		return err
	}
	if err := fn(tx, nowMS, &wroteOutbox); err != nil {
		return err
	}
	if wroteOutbox {
		return s.commitOutboxTx(tx)
	}
	return tx.Commit()
}

func (s *Store) expirePartySessionsTx(ctx context.Context, tx *sql.Tx, nowMS int64, wroteOutbox *bool) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT s.screen_id, s.session_id, s.auth_session_id, s.profile_id, p.host_profile_id, s.device_id
		FROM party_screen s
		JOIN party_session p ON p.session_id = s.session_id
		WHERE p.ended_at_ms IS NULL AND p.expires_at_ms <= ? AND s.dropped_at_ms IS NULL AND s.device_id <> ''
	`, nowMS)
	if err != nil {
		return err
	}
	var expiredDisplays []PartyStageEventPayload
	for rows.Next() {
		var p PartyStageEventPayload
		if err := rows.Scan(&p.ScreenID, &p.PartySessionID, &p.AuthSessionID, &p.ProfileID, &p.HostProfileID, &p.DeviceID); err != nil {
			_ = rows.Close()
			return err
		}
		p.Action = PartyStageActionReleased
		p.Reason = "ended"
		expiredDisplays = append(expiredDisplays, p)
	}
	if err := closeRows(rows); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE party_screen SET dropped_at_ms = ?
		WHERE dropped_at_ms IS NULL AND session_id IN (
			SELECT session_id FROM party_session WHERE ended_at_ms IS NULL AND expires_at_ms <= ?
		)`, nowMS, nowMS); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE party_session SET ended_at_ms = expires_at_ms
		WHERE ended_at_ms IS NULL AND expires_at_ms <= ?`, nowMS); err != nil {
		return err
	}
	for _, p := range expiredDisplays {
		if err := appendPartyStageEventSQL(ctx, tx, p, nowMS); err != nil {
			return err
		}
		*wroteOutbox = true
	}
	return nil
}

func livePartySessionByHost(ctx context.Context, q partyQuerier, host ProfileID, nowMS int64) (PartySession, error) {
	session, err := scanPartySession(q.QueryRowContext(ctx, `SELECT `+partySessionColumns+`
		FROM party_session WHERE host_profile_id = ? AND ended_at_ms IS NULL AND expires_at_ms > ?`, host, nowMS))
	if errors.Is(err, sql.ErrNoRows) {
		return PartySession{}, ErrPartyNotFound
	}
	return session, err
}

func livePartySessionByID(ctx context.Context, q partyQuerier, sessionID string, nowMS int64) (PartySession, error) {
	session, err := scanPartySession(q.QueryRowContext(ctx, `SELECT `+partySessionColumns+`
		FROM party_session WHERE session_id = ? AND ended_at_ms IS NULL AND expires_at_ms > ?`, sessionID, nowMS))
	if errors.Is(err, sql.ErrNoRows) {
		return PartySession{}, ErrPartyNotFound
	}
	return session, err
}

// partyHostFilterLocked narrows the host's visibility to the session's
// Libraries. Guests and screens read through it, so it never widens what the
// host can see, and it denies Media the host could not play.
func (s *Store) partyHostFilterLocked(ctx context.Context, host LibraryFilter, libraryIDs []LibraryID) (LibraryFilter, []LibraryID, error) {
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return LibraryFilter{}, nil, err
	}
	var allowed []LibraryID
	for _, library := range libraries {
		if slices.Contains(libraryIDs, library.ID) && libraryVisible(library, host) {
			allowed = append(allowed, library.ID)
		}
	}
	return LibraryFilter{
		AllowedIDs:      allowed,
		Revealed:        host.Revealed,
		SealedIDs:       slices.Clone(host.SealedIDs),
		Certification:   slices.Clone(host.Certification),
		PlaybackGate:    true,
		GrantedMediaIDs: slices.Clone(host.GrantedMediaIDs),
	}, allowed, nil
}

// partyLiveSessionLocked narrows the session's stored host visibility to the
// host Profile's current Libraries and AccessGrants, so guests and screens
// lose access as soon as the host does. It only ever narrows: a disabled or
// deleted host leaves no Libraries.
func (s *Store) partyLiveSessionLocked(ctx context.Context, session PartySession) (PartySession, error) {
	host, err := s.profileSQL(ctx, session.HostProfileID)
	var missing ValidationError
	if errors.As(err, &missing) {
		host, err = Profile{Disabled: true}, nil
	}
	if err != nil {
		return PartySession{}, err
	}
	granted, err := readGrantedMediaIDs(ctx, s.catalogR, session.HostProfileID)
	if err != nil {
		return PartySession{}, err
	}
	filter := session.hostFilter
	filter.AllowedIDs = slices.DeleteFunc(slices.Clone(filter.AllowedIDs), func(id LibraryID) bool {
		return host.Disabled || !slices.Contains(host.Library, id)
	})
	filter.GrantedMediaIDs = slices.DeleteFunc(slices.Clone(filter.GrantedMediaIDs), func(id MediaID) bool {
		return !slices.Contains(granted, id)
	})
	session.hostFilter = filter
	return session, nil
}

func encodePartyFilter(filter LibraryFilter) (string, error) {
	raw, err := json.Marshal(filter)
	return string(raw), err
}

// StartPartySession starts the host's session, or returns the live one
// unchanged when the host already has one.
func (s *Store) StartPartySession(ctx context.Context, input PartyStartInput) (string, error) {
	if input.Host == "" {
		return "", partyInvalid("profile is required")
	}
	fairness := input.Fairness
	if fairness == "" {
		fairness = string(partyqueue.RoundRobin)
	}
	if !partyqueue.Fairness(fairness).Valid() {
		return "", partyInvalid("fairness must be round-robin or fifo")
	}
	hostName, err := partyName(input.HostName)
	if err != nil {
		hostName = "Host"
	}
	var sessionID string
	err = s.partyWrite(ctx, func(tx *sql.Tx, nowMS int64) error {
		live, err := livePartySessionByHost(ctx, tx, input.Host, nowMS)
		if err == nil {
			sessionID = live.ID
			return nil
		}
		if !errors.Is(err, ErrPartyNotFound) {
			return err
		}
		filter, allowed, err := s.partyHostFilterLocked(ctx, input.Filter, input.LibraryIDs)
		if err != nil {
			return err
		}
		if len(allowed) == 0 {
			return partyInvalid("libraryIDs must name Libraries you can see")
		}
		libraries, err := json.Marshal(allowed)
		if err != nil {
			return err
		}
		encodedFilter, err := encodePartyFilter(filter)
		if err != nil {
			return err
		}
		// Ended sessions are history only; keep a week of it per host.
		if _, err := tx.ExecContext(ctx, `DELETE FROM party_session
			WHERE host_profile_id = ? AND ended_at_ms IS NOT NULL AND ended_at_ms < ?`,
			input.Host, nowMS-partyRetainEndedFor.Milliseconds()); err != nil {
			return err
		}
		if sessionID, err = newPartyID("party-"); err != nil {
			return err
		}
		code, err := freePartyJoinCode(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO party_session
			(session_id, host_profile_id, join_code, fairness, auto_add, library_ids, host_filter, started_at_ms, expires_at_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sessionID, input.Host, code, fairness, boolInt(input.AutoAdd), string(libraries), encodedFilter,
			nowMS, nowMS+PartyLifetime.Milliseconds()); err != nil {
			return err
		}
		participantID, err := newPartyID("pp-")
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO party_participant
			(participant_id, session_id, name, host, joined_at_ms) VALUES (?, ?, ?, 1, ?)`,
			participantID, sessionID, hostName, nowMS)
		return err
	})
	return sessionID, err
}

func freePartyJoinCode(ctx context.Context, tx *sql.Tx) (string, error) {
	for range partyCodeAttempts {
		code, err := newPartyJoinCode()
		if err != nil {
			return "", err
		}
		var taken int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM party_session
			WHERE join_code = ? AND ended_at_ms IS NULL`, code).Scan(&taken); err != nil {
			return "", err
		}
		if taken == 0 {
			return code, nil
		}
	}
	return "", errors.New("party join code space exhausted")
}

// partyHostWrite runs a host mutation against the host's live session and
// refreshes the session's stored visibility from the host's current one.
func (s *Store) partyHostWrite(ctx context.Context, host ProfileID, filter LibraryFilter, fn func(tx *sql.Tx, session PartySession, nowMS int64) error) error {
	return s.partyHostWriteAction(ctx, host, filter, PartyStageActionChanged, func(tx *sql.Tx, session PartySession, nowMS int64, _ *bool) error {
		return fn(tx, session, nowMS)
	})
}

func (s *Store) partyHostWriteAction(
	ctx context.Context,
	host ProfileID,
	filter LibraryFilter,
	notifyAction string,
	fn func(tx *sql.Tx, session PartySession, nowMS int64, wroteOutbox *bool) error,
) error {
	return s.partyWriteOutbox(ctx, func(tx *sql.Tx, nowMS int64, wroteOutbox *bool) error {
		session, err := livePartySessionByHost(ctx, tx, host, nowMS)
		if err != nil {
			return err
		}
		narrowed, _, err := s.partyHostFilterLocked(ctx, filter, session.LibraryIDs)
		if err != nil {
			return err
		}
		encoded, err := encodePartyFilter(narrowed)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE party_session SET host_filter = ? WHERE session_id = ?`, encoded, session.ID); err != nil {
			return err
		}
		session.hostFilter = narrowed
		if err := fn(tx, session, nowMS, wroteOutbox); err != nil {
			return err
		}
		return s.reconcileAndNotifyPartyDisplaysTx(ctx, tx, session.ID, session.HostProfileID, nowMS, notifyAction, wroteOutbox)
	})
}

// PartySessionForHost returns the host's live session as the host sees it.
func (s *Store) PartySessionForHost(ctx context.Context, host ProfileID, filter LibraryFilter) (PartyView, bool, error) {
	return s.PartySessionForHostWithFilters(ctx, host, filter, nil)
}

// PartySessionForHostWithFilters returns the host's live session with optional
// per-screen playback filters used to evaluate lead eligibility.
func (s *Store) PartySessionForHostWithFilters(ctx context.Context, host ProfileID, filter LibraryFilter, screenPlaybackFilters map[string]LibraryFilter) (PartyView, bool, error) {
	view, err := s.readPartyView(ctx, screenPlaybackFilters, func(q partyQuerier, nowMS int64) (PartySession, error) {
		session, err := livePartySessionByHost(ctx, q, host, nowMS)
		if err != nil {
			return PartySession{}, err
		}
		narrowed, _, err := s.partyHostFilterLocked(ctx, filter, session.LibraryIDs)
		session.hostFilter = narrowed
		return session, err
	})
	if errors.Is(err, ErrPartyNotFound) {
		return PartyView{}, false, nil
	}
	return view, err == nil, err
}

func (s *Store) readPartyView(ctx context.Context, screenPlaybackFilters map[string]LibraryFilter, find func(q partyQuerier, nowMS int64) (PartySession, error)) (PartyView, error) {
	view, err := func() (PartyView, error) {
		s.mu.RLock()
		defer s.mu.RUnlock()
		if err := s.guardLocked(); err != nil {
			return PartyView{}, err
		}
		nowMS := timeToMillis(s.serverNow())
		session, err := find(s.eventsR, nowMS)
		if err != nil {
			return PartyView{}, err
		}
		if session, err = s.partyLiveSessionLocked(ctx, session); err != nil {
			return PartyView{}, err
		}
		state, err := loadPartyState(ctx, s.eventsR, session, nowMS)
		if err != nil {
			return PartyView{}, err
		}
		state.screens = s.filterValidPartyScreensLocked(ctx, session.HostProfileID, state.screens)
		s.markPartyLeadLocked(ctx, &state, screenPlaybackFilters)
		return s.buildPartyViewLocked(ctx, state, nowMS)
	}()
	if err != nil {
		return PartyView{}, err
	}
	s.annotatePartySubtitles(ctx, &view)
	return view, nil
}

func loadPartyState(ctx context.Context, q partyQuerier, session PartySession, nowMS int64) (partyState, error) {
	state := partyState{session: session, participants: map[string]partyParticipantRow{}}
	rows, err := q.QueryContext(ctx, `SELECT participant_id, name, guest, host, COALESCE(removed_at_ms, 0)
		FROM party_participant WHERE session_id = ? ORDER BY joined_at_ms, participant_id`, session.ID)
	if err != nil {
		return partyState{}, err
	}
	for rows.Next() {
		var row partyParticipantRow
		var guest, host int
		if err := rows.Scan(&row.id, &row.name, &guest, &host, &row.removedAtMS); err != nil {
			_ = rows.Close()
			return partyState{}, err
		}
		row.guest, row.host = guest == 1, host == 1
		state.participants[row.id] = row
	}
	if err := closeRows(rows); err != nil {
		return partyState{}, err
	}
	rows, err = q.QueryContext(ctx, `SELECT entry_id, participant_id, media_id, state, COALESCE(pinned_position, 0),
			pitch_semitones, added_at_ms, COALESCE(current_at_ms, 0), COALESCE(played_at_ms, 0), COALESCE(finished_at_ms, 0),
			COALESCE(removed_at_ms, 0)
		FROM party_entry WHERE session_id = ? ORDER BY added_at_ms, entry_id`, session.ID)
	if err != nil {
		return partyState{}, err
	}
	for rows.Next() {
		var row partyEntryRow
		if err := rows.Scan(&row.id, &row.participantID, &row.mediaID, &row.state, &row.pin,
			&row.pitchSemitones, &row.addedAtMS, &row.currentAtMS, &row.playedAtMS, &row.finishedAtMS, &row.removedAtMS); err != nil {
			_ = rows.Close()
			return partyState{}, err
		}
		state.entries = append(state.entries, row)
	}
	if err := closeRows(rows); err != nil {
		return partyState{}, err
	}
	rows, err = q.QueryContext(ctx, `SELECT screen_id, profile_id, auth_session_id, device_id, binding_generation, chosen_lead,
			name, kind, carrier, ticker, offset_ms, rtt_ms, registered_at_ms, COALESCE(reported_at_ms, 0)
		FROM party_screen WHERE session_id = ? AND dropped_at_ms IS NULL
		ORDER BY registered_at_ms, screen_id`, session.ID)
	if err != nil {
		return partyState{}, err
	}
	for rows.Next() {
		var row partyScreenRow
		var ticker, chosenLead int
		var authSess, devID string
		if err := rows.Scan(&row.id, &row.profileID, &authSess, &devID, &row.bindingGeneration, &chosenLead,
			&row.name, &row.kind, &row.carrier, &ticker, &row.offsetMS, &row.rttMS,
			&row.registeredAtMS, &row.reportedAtMS); err != nil {
			_ = rows.Close()
			return partyState{}, err
		}
		row.authSessionID = SessionID(authSess)
		row.deviceID = DeviceID(devID)
		row.chosenLead = chosenLead == 1
		row.ticker = ticker == 1
		row.lastSeenMS = max(row.registeredAtMS, row.reportedAtMS)
		row.active = nowMS-row.lastSeenMS < partyScreenSilence.Milliseconds()
		state.screens = append(state.screens, row)
	}
	if err := closeRows(rows); err != nil {
		return partyState{}, err
	}
	markPartyLead(state.screens, nil)
	return state, nil
}

func closeRows(rows *sql.Rows) error {
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	return rows.Close()
}

// markPartyLead marks the chosen lead (if active and able to play the current
// entry), else the first eligible active popout screen, else the first eligible
// active ordinary joined screen, as the lead.
func markPartyLead(screens []partyScreenRow, canPlayCurrent func(partyScreenRow) bool) {
	for i := range screens {
		screens[i].lead = false
	}
	canLead := func(screen partyScreenRow) bool {
		if !screen.active {
			return false
		}
		if canPlayCurrent != nil && !canPlayCurrent(screen) {
			return false
		}
		return true
	}
	for i := range screens {
		if screens[i].chosenLead && canLead(screens[i]) {
			screens[i].lead = true
			return
		}
	}
	for i := range screens {
		if screens[i].kind == "popout" && canLead(screens[i]) {
			screens[i].lead = true
			return
		}
	}
	for i := range screens {
		if screens[i].kind == "joined" && screens[i].deviceID == "" && canLead(screens[i]) {
			screens[i].lead = true
			return
		}
	}
}

// waitingOrder returns the visible waiting entries in play order.
func (st partyState) waitingOrder(visible func(MediaID) bool) []partyEntryRow {
	byID := map[string]partyEntryRow{}
	var waiting []partyqueue.Entry
	for _, entry := range st.entries {
		if entry.state != PartyEntryWaiting || !visible(entry.mediaID) {
			continue
		}
		byID[entry.id] = entry
		waiting = append(waiting, partyqueue.Entry{
			ID: entry.id, ParticipantID: entry.participantID, AddedAtMS: entry.addedAtMS, Pin: entry.pin,
		})
	}
	ordered := partyqueue.Order(partyqueue.Fairness(st.session.Fairness), waiting, st.lastTurns())
	out := make([]partyEntryRow, len(ordered))
	for i, entry := range ordered {
		out[i] = byID[entry.ID]
	}
	return out
}

func (st partyState) lastTurns() map[string]int64 {
	turns := map[string]int64{}
	for _, entry := range st.entries {
		if entry.currentAtMS > 0 {
			turns[entry.participantID] = max(turns[entry.participantID], entry.currentAtMS)
		}
	}
	return turns
}

func (st partyState) changeover() int {
	var gaps []int
	for _, entry := range st.entries {
		if entry.currentAtMS > 0 && entry.playedAtMS >= entry.currentAtMS {
			gaps = append(gaps, int((entry.playedAtMS-entry.currentAtMS+500)/1000))
		}
	}
	return partyqueue.Changeover(gaps)
}

func (st partyState) currentEntry() (partyEntryRow, bool) {
	for _, entry := range st.entries {
		if entry.id == st.session.CurrentEntryID && entry.state == PartyEntryCurrent {
			return entry, true
		}
	}
	return partyEntryRow{}, false
}

// targetPositionMS is where playback is at nowMS: positionMS plus the time
// since the anchor while playing. Positions are in the media's own
// milliseconds, so wall time since the anchor advances them at tempo.
func (session PartySession) targetPositionMS(nowMS int64) int64 {
	if session.PlaybackState == "playing" && nowMS > session.AnchorAtMS {
		return session.PositionMS + int64(float64(nowMS-session.AnchorAtMS)*session.tempo())
	}
	return session.PositionMS
}

// tempo is the playback rate, with 1 standing in for an unset or invalid one.
func (session PartySession) tempo() float64 {
	if session.Tempo > 0 {
		return session.Tempo
	}
	return 1
}

func (s *Store) buildPartyViewLocked(ctx context.Context, state partyState, nowMS int64) (PartyView, error) {
	ids := make([]MediaID, 0, len(state.entries))
	for _, entry := range state.entries {
		ids = append(ids, entry.mediaID)
	}
	media, err := s.visibleMediaBatchSQL(ctx, ids, state.session.hostFilter)
	if err != nil {
		return PartyView{}, err
	}
	visible := func(id MediaID) bool { _, ok := media[id]; return ok }
	var durations []int
	for _, entry := range state.entries {
		if m, ok := media[entry.mediaID]; ok && m.DurationSeconds > 0 {
			durations = append(durations, m.DurationSeconds)
		}
	}
	fallback := partyqueue.Median(durations)
	view := PartyView{Session: state.session, ChangeoverSeconds: state.changeover()}
	entryView := func(entry partyEntryRow) PartyEntryView {
		out := PartyEntryView{
			ID: entry.id, MediaID: entry.mediaID, ParticipantID: entry.participantID,
			ParticipantName: state.participants[entry.participantID].name, State: entry.state,
			Pinned: entry.pin > 0 && entry.state == PartyEntryWaiting, PitchSemitones: entry.pitchSemitones,
			AddedAt: millisToTime(entry.addedAtMS),
		}
		if m, ok := media[entry.mediaID]; ok {
			out.Title, out.DurationSeconds = m.Title, m.DurationSeconds
		}
		out.LastPlayedAt = state.lastPlayed(entry)
		return out
	}
	timing := partyqueue.Timing{ChangeoverSeconds: view.ChangeoverSeconds, FallbackDurationSeconds: fallback}
	if current, ok := state.currentEntry(); ok {
		v := entryView(current)
		view.Current = &v
		session := state.session
		view.Playback = &PartyPlaybackView{
			EntryID: current.id, State: session.PlaybackState, PositionMS: session.PositionMS,
			AnchorAtMS: session.AnchorAtMS, Tempo: session.Tempo, PitchSemitones: session.PitchSemitones,
		}
		duration := v.DurationSeconds
		if duration <= 0 {
			duration = fallback
		}
		remainingMS := max((float64(duration)*1000-float64(session.targetPositionMS(nowMS)))/session.tempo(), 0)
		timing.HasCurrent = true
		timing.CurrentRemainingSeconds = int(math.Ceil(remainingMS / 1000))
	}
	ordered := state.waitingOrder(visible)
	orderedDurations := make([]int, len(ordered))
	for i, entry := range ordered {
		orderedDurations[i] = media[entry.mediaID].DurationSeconds
	}
	etas := partyqueue.ETAs(orderedDurations, timing)
	waitingByParticipant := map[string]int{}
	for i, entry := range ordered {
		v := entryView(entry)
		v.Position, v.ETASeconds = i+1, etas[i]
		view.Waiting = append(view.Waiting, v)
		waitingByParticipant[entry.participantID]++
	}
	removedSince := nowMS - partyRemovedWindow.Milliseconds()
	for _, entry := range state.entries {
		switch entry.state {
		case PartyEntryPending:
			view.Requests = append(view.Requests, entryView(entry))
		case PartyEntryDone:
			view.Recent = append(view.Recent, entryView(entry))
		case PartyEntrySkipped:
			view.Skipped = append(view.Skipped, entryView(entry))
		case PartyEntryDeclined:
			view.Declined = append(view.Declined, entryView(entry))
		case PartyEntryRemoved:
			if entry.removedAtMS >= removedSince {
				view.Removed = append(view.Removed, entryView(entry))
			}
		}
	}
	newestFirst := func(at func(PartyEntryView) int64) func(a, b PartyEntryView) int {
		return func(a, b PartyEntryView) int {
			if x, y := at(a), at(b); x != y {
				if x > y {
					return -1
				}
				return 1
			}
			return strings.Compare(a.ID, b.ID)
		}
	}
	finished := map[string]int64{}
	removed := map[string]int64{}
	for _, entry := range state.entries {
		finished[entry.id], removed[entry.id] = entry.finishedAtMS, entry.removedAtMS
	}
	slices.SortStableFunc(view.Recent, newestFirst(func(v PartyEntryView) int64 { return finished[v.ID] }))
	slices.SortStableFunc(view.Skipped, newestFirst(func(v PartyEntryView) int64 { return finished[v.ID] }))
	slices.SortStableFunc(view.Removed, newestFirst(func(v PartyEntryView) int64 { return removed[v.ID] }))
	view.Recent = view.Recent[:min(len(view.Recent), partyRecentLimit)]
	view.Skipped = view.Skipped[:min(len(view.Skipped), partyRecentLimit)]
	turns := state.lastTurns()
	for _, participant := range state.sortedParticipants() {
		if participant.removedAtMS > 0 {
			continue
		}
		view.Participants = append(view.Participants, PartyParticipantView{
			ID: participant.id, Name: participant.name, Guest: participant.guest,
			Waiting: waitingByParticipant[participant.id], LastTurnAt: millisToTime(turns[participant.id]),
		})
	}
	for _, screen := range state.screens {
		if screen.active {
			view.Screens = append(view.Screens, screen.view())
		}
	}
	return view, nil
}

func (st partyState) sortedParticipants() []partyParticipantRow {
	out := make([]partyParticipantRow, 0, len(st.participants))
	for _, participant := range st.participants {
		out = append(out, participant)
	}
	slices.SortFunc(out, func(a, b partyParticipantRow) int {
		if a.host != b.host {
			if a.host {
				return -1
			}
			return 1
		}
		return strings.Compare(a.id, b.id)
	})
	return out
}

// lastPlayed is when entry's Media was last current earlier in the session.
func (st partyState) lastPlayed(entry partyEntryRow) time.Time {
	before := int64(math.MaxInt64)
	if entry.currentAtMS > 0 {
		before = entry.currentAtMS
	}
	var last int64
	for _, other := range st.entries {
		if other.id != entry.id && other.mediaID == entry.mediaID && other.currentAtMS > 0 && other.currentAtMS < before {
			last = max(last, other.currentAtMS)
		}
	}
	return millisToTime(last)
}

func (row partyScreenRow) view() PartyScreenView {
	return PartyScreenView{
		ID: row.id, ProfileID: row.profileID, AuthSessionID: row.authSessionID,
		DeviceID: row.deviceID, BindingGeneration: row.bindingGeneration, ChosenLead: row.chosenLead,
		Name: row.name, Kind: row.kind, Carrier: row.carrier,
		Lead: row.lead, Ticker: row.ticker, OffsetMS: row.offsetMS, RTTMS: row.rttMS,
		ReportedAt: millisToTime(row.reportedAtMS),
	}
}

// PartySubtitler renders the subtitle of each Media the way its Library
// plugin declares, keyed by Media ID. A Media it leaves out has none.
type PartySubtitler func(ctx context.Context, ids []MediaID) map[MediaID]string

// SetPartySubtitler wires the plugin-declared subtitle renderer the party
// reads use. The store cannot read plugin bundles, so the composition root
// supplies it; unwired, entries fall back to their first linked Entity.
func (s *Store) SetPartySubtitler(render PartySubtitler) {
	s.partySubtitler.Store(&render)
}

// annotatePartySubtitles names each entry's subtitle under its title. It
// decorates the read and never fails it.
func (s *Store) annotatePartySubtitles(ctx context.Context, view *PartyView) {
	lists := [][]PartyEntryView{view.Waiting, view.Requests, view.Recent, view.Removed, view.Skipped}
	var ids []MediaID
	collect := func(entry PartyEntryView) {
		if entry.Title != "" {
			ids = append(ids, entry.MediaID)
		}
	}
	if view.Current != nil {
		collect(*view.Current)
	}
	for _, list := range lists {
		for _, entry := range list {
			collect(entry)
		}
	}
	subtitles := s.partySubtitles(ctx, ids)
	if view.Current != nil {
		view.Current.Subtitle = subtitles[view.Current.MediaID]
	}
	for _, list := range lists {
		for i := range list {
			list[i].Subtitle = subtitles[list[i].MediaID]
		}
	}
}

// partySubtitles renders each Media's plugin-declared subtitle, falling back
// to its first linked Entity's title where the plugin renders none.
func (s *Store) partySubtitles(ctx context.Context, ids []MediaID) map[MediaID]string {
	out := map[MediaID]string{}
	if len(ids) == 0 {
		return out
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if render := s.partySubtitler.Load(); render != nil && *render != nil {
		for id, subtitle := range (*render)(ctx, ids) {
			if subtitle != "" {
				out[id] = subtitle
			}
		}
	}
	var missing []MediaID
	for _, id := range ids {
		if _, ok := out[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return out
	}
	links, err := s.MediaEntityLinks(ctx, missing)
	if err != nil {
		return out
	}
	for id, list := range links {
		if len(list) > 0 {
			out[id] = list[0].Title
		}
	}
	return out
}

// UpdatePartySession changes the host's settings.
func (s *Store) UpdatePartySession(ctx context.Context, host ProfileID, filter LibraryFilter, input PartyUpdateInput) error {
	if input.Fairness != nil && !partyqueue.Fairness(*input.Fairness).Valid() {
		return partyInvalid("fairness must be round-robin or fifo")
	}
	return s.partyHostWrite(ctx, host, filter, func(tx *sql.Tx, session PartySession, _ int64) error {
		fairness, autoAdd, joinOpen := session.Fairness, session.AutoAdd, session.JoinOpen
		if input.Fairness != nil {
			fairness = *input.Fairness
		}
		if input.AutoAdd != nil {
			autoAdd = *input.AutoAdd
		}
		if input.JoinOpen != nil {
			joinOpen = *input.JoinOpen
		}
		_, err := tx.ExecContext(ctx, `UPDATE party_session SET fairness = ?, auto_add = ?, join_open = ? WHERE session_id = ?`,
			fairness, boolInt(autoAdd), boolInt(joinOpen), session.ID)
		return err
	})
}

// EndPartySession ends the host's live session, which refuses every guest
// token it issued and releases any joined player displays. It reports whether a
// session was live.
func (s *Store) EndPartySession(ctx context.Context, host ProfileID) (bool, error) {
	ended := false
	err := s.partyWriteOutbox(ctx, func(tx *sql.Tx, nowMS int64, wroteOutbox *bool) error {
		session, err := livePartySessionByHost(ctx, tx, host, nowMS)
		if errors.Is(err, ErrPartyNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT screen_id, auth_session_id, profile_id, device_id
			FROM party_screen
			WHERE session_id = ? AND dropped_at_ms IS NULL AND device_id <> ''
		`, session.ID)
		if err != nil {
			return err
		}
		var displays []PartyStageEventPayload
		for rows.Next() {
			var p PartyStageEventPayload
			if err := rows.Scan(&p.ScreenID, &p.AuthSessionID, &p.ProfileID, &p.DeviceID); err != nil {
				_ = rows.Close()
				return err
			}
			p.PartySessionID = session.ID
			p.HostProfileID = session.HostProfileID
			p.Action = PartyStageActionReleased
			p.Reason = "ended"
			displays = append(displays, p)
		}
		if err := closeRows(rows); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE party_screen SET dropped_at_ms = ?
			WHERE session_id = ? AND dropped_at_ms IS NULL`, nowMS, session.ID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE party_session SET ended_at_ms = ?
			WHERE session_id = ? AND ended_at_ms IS NULL`, nowMS, session.ID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		ended = n > 0
		if err != nil {
			return err
		}
		for _, p := range displays {
			if err := appendPartyStageEventSQL(ctx, tx, p, nowMS); err != nil {
				return err
			}
			*wroteOutbox = true
		}
		return nil
	})
	return ended, err
}

// RotatePartyJoinCode gives the host's session a new join code; the old one
// stops admitting guests.
func (s *Store) RotatePartyJoinCode(ctx context.Context, host ProfileID, filter LibraryFilter) error {
	return s.partyHostWrite(ctx, host, filter, func(tx *sql.Tx, session PartySession, _ int64) error {
		code, err := freePartyJoinCode(ctx, tx)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE party_session SET join_code = ? WHERE session_id = ?`, code, session.ID)
		return err
	})
}

// AddPartyEntry adds a waiting entry for the host, for an existing
// participant, or for a named participant created on first use.
func (s *Store) AddPartyEntry(ctx context.Context, host ProfileID, filter LibraryFilter, mediaID MediaID, participantID, participantName string) error {
	if mediaID == "" {
		return partyInvalid("mediaID is required")
	}
	return s.partyHostWrite(ctx, host, filter, func(tx *sql.Tx, session PartySession, nowMS int64) error {
		if err := s.requirePartyMediaLocked(ctx, session, mediaID); err != nil {
			return err
		}
		owner, err := resolvePartyParticipant(ctx, tx, session.ID, participantID, participantName, nowMS)
		if err != nil {
			return err
		}
		return insertPartyEntry(ctx, tx, session.ID, owner, mediaID, PartyEntryWaiting, nowMS)
	})
}

func (s *Store) requirePartyMediaLocked(ctx context.Context, session PartySession, mediaID MediaID) error {
	visible, err := s.visibleMediaIDsSQL(ctx, []MediaID{mediaID}, session.hostFilter)
	if err != nil {
		return err
	}
	if _, ok := visible[mediaID]; !ok {
		return partyInvalid("media not found")
	}
	return nil
}

func resolvePartyParticipant(ctx context.Context, tx *sql.Tx, sessionID, participantID, participantName string, nowMS int64) (string, error) {
	participantID = strings.TrimSpace(participantID)
	if participantID != "" {
		var id string
		err := tx.QueryRowContext(ctx, `SELECT participant_id FROM party_participant
			WHERE participant_id = ? AND session_id = ? AND removed_at_ms IS NULL`, participantID, sessionID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return "", partyInvalid("participant not found")
		}
		return id, err
	}
	if strings.TrimSpace(participantName) == "" {
		var id string
		err := tx.QueryRowContext(ctx, `SELECT participant_id FROM party_participant
			WHERE session_id = ? AND host = 1`, sessionID).Scan(&id)
		return id, err
	}
	name, err := partyName(participantName)
	if err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT participant_id FROM party_participant
		WHERE session_id = ? AND removed_at_ms IS NULL AND name = ? COLLATE NOCASE
		ORDER BY joined_at_ms LIMIT 1`, sessionID, name).Scan(&id)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return id, err
	}
	if id, err = newPartyID("pp-"); err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO party_participant (participant_id, session_id, name, joined_at_ms)
		VALUES (?, ?, ?, ?)`, id, sessionID, name, nowMS)
	return id, err
}

func insertPartyEntry(ctx context.Context, tx *sql.Tx, sessionID, participantID string, mediaID MediaID, state string, nowMS int64) error {
	id, err := newPartyID("pe-")
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO party_entry (entry_id, session_id, participant_id, media_id, state, added_at_ms)
		VALUES (?, ?, ?, ?, ?, ?)`, id, sessionID, participantID, mediaID, state, nowMS)
	return err
}

func partyEntryState(ctx context.Context, tx *sql.Tx, sessionID, entryID string) (string, error) {
	var state string
	err := tx.QueryRowContext(ctx, `SELECT state FROM party_entry WHERE entry_id = ? AND session_id = ?`,
		strings.TrimSpace(entryID), sessionID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", partyInvalid("entry not found")
	}
	return state, err
}

// ReviewPartyEntry approves a pending request into the rotation or declines it.
func (s *Store) ReviewPartyEntry(ctx context.Context, host ProfileID, filter LibraryFilter, entryID string, approve bool) error {
	return s.partyHostWrite(ctx, host, filter, func(tx *sql.Tx, session PartySession, nowMS int64) error {
		state, err := partyEntryState(ctx, tx, session.ID, entryID)
		if err != nil {
			return err
		}
		if state != PartyEntryPending {
			return partyInvalid("entry is not pending")
		}
		if approve {
			_, err = tx.ExecContext(ctx, `UPDATE party_entry SET state = 'waiting' WHERE entry_id = ?`, entryID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE party_entry SET state = 'declined', finished_at_ms = ? WHERE entry_id = ?`, nowMS, entryID)
		}
		return err
	})
}

// MovePartyEntry pins a waiting entry at a 1-based waiting position; the
// rotation fills around pins.
func (s *Store) MovePartyEntry(ctx context.Context, host ProfileID, filter LibraryFilter, entryID string, position int) error {
	if position < 1 {
		return partyInvalid("position must be at least 1")
	}
	return s.partyHostWrite(ctx, host, filter, func(tx *sql.Tx, session PartySession, _ int64) error {
		state, err := partyEntryState(ctx, tx, session.ID, entryID)
		if err != nil {
			return err
		}
		if state != PartyEntryWaiting {
			return partyInvalid("entry is not waiting")
		}
		var waiting int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM party_entry WHERE session_id = ? AND state = 'waiting'`,
			session.ID).Scan(&waiting); err != nil {
			return err
		}
		position = min(position, max(waiting, 1))
		// The moved entry takes the position from any entry pinned there.
		if _, err := tx.ExecContext(ctx, `UPDATE party_entry SET pinned_position = NULL
			WHERE session_id = ? AND state = 'waiting' AND pinned_position = ? AND entry_id <> ?`,
			session.ID, position, entryID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE party_entry SET pinned_position = ? WHERE entry_id = ?`, position, entryID)
		return err
	})
}

// RemovePartyEntry removes a pending or waiting entry; RestorePartyEntry can
// undo it for partyRemovedWindow.
func (s *Store) RemovePartyEntry(ctx context.Context, host ProfileID, filter LibraryFilter, entryID string) error {
	return s.partyHostWrite(ctx, host, filter, func(tx *sql.Tx, session PartySession, nowMS int64) error {
		state, err := partyEntryState(ctx, tx, session.ID, entryID)
		if err != nil {
			return err
		}
		if state != PartyEntryPending && state != PartyEntryWaiting {
			return partyInvalid("entry is not pending or waiting")
		}
		_, err = tx.ExecContext(ctx, `UPDATE party_entry
			SET state = 'removed', prior_state = state, pinned_position = NULL, removed_at_ms = ?
			WHERE entry_id = ?`, nowMS, entryID)
		return err
	})
}

// RestorePartyEntry returns a recently removed entry to its earlier state.
func (s *Store) RestorePartyEntry(ctx context.Context, host ProfileID, filter LibraryFilter, entryID string) error {
	return s.partyHostWrite(ctx, host, filter, func(tx *sql.Tx, session PartySession, nowMS int64) error {
		var state, prior string
		var removedAtMS int64
		var participantRemoved bool
		err := tx.QueryRowContext(ctx, `SELECT e.state, COALESCE(e.prior_state, ''), COALESCE(e.removed_at_ms, 0),
				p.removed_at_ms IS NOT NULL
			FROM party_entry e JOIN party_participant p ON p.participant_id = e.participant_id
			WHERE e.entry_id = ? AND e.session_id = ?`, strings.TrimSpace(entryID), session.ID).
			Scan(&state, &prior, &removedAtMS, &participantRemoved)
		if errors.Is(err, sql.ErrNoRows) {
			return partyInvalid("entry not found")
		}
		if err != nil {
			return err
		}
		if state != PartyEntryRemoved || prior == "" || participantRemoved ||
			removedAtMS < nowMS-partyRemovedWindow.Milliseconds() {
			return partyInvalid("entry cannot be restored")
		}
		_, err = tx.ExecContext(ctx, `UPDATE party_entry SET state = prior_state, prior_state = NULL, removed_at_ms = NULL
			WHERE entry_id = ?`, entryID)
		return err
	})
}

// AdvanceParty marks the current entry done and makes the next visible
// waiting entry current, skipping entries whose Media is no longer visible.
// Naming fromEntryID makes it idempotent: a fromEntryID that is not current
// leaves the session unchanged. A Profile with a live screen on another
// host's session advances that session, but only past its current entry.
func (s *Store) AdvanceParty(ctx context.Context, host ProfileID, filter LibraryFilter, fromEntryID string) error {
	fromEntryID = strings.TrimSpace(fromEntryID)
	if fromEntryID != "" {
		advanced := false
		err := s.partyWriteOutbox(ctx, func(tx *sql.Tx, nowMS int64, wroteOutbox *bool) error {
			session, err := scanPartySession(tx.QueryRowContext(ctx, `SELECT `+partySessionColumns+`
				FROM party_session WHERE current_entry_id = ? AND host_profile_id <> ?
					AND ended_at_ms IS NULL AND expires_at_ms > ?
					AND session_id IN (SELECT session_id FROM party_screen WHERE profile_id = ? AND dropped_at_ms IS NULL)`,
				fromEntryID, host, nowMS, host))
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if session, err = s.partyLiveSessionLocked(ctx, session); err != nil {
				return err
			}
			if err := s.reconcilePartyPlayerDisplaysTx(ctx, tx, session.ID, session.HostProfileID, nowMS, wroteOutbox); err != nil {
				return err
			}
			var stillRegistered int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM party_screen WHERE session_id = ? AND profile_id = ? AND dropped_at_ms IS NULL`,
				session.ID, host).Scan(&stillRegistered); err != nil {
				return err
			}
			if stillRegistered == 0 {
				return nil
			}
			advanced = true
			if err := s.advancePartyLocked(ctx, tx, session, nowMS); err != nil {
				return err
			}
			return s.notifyActivePartyDisplaysTx(ctx, tx, session.ID, session.HostProfileID, nowMS, PartyStageActionChanged, wroteOutbox)
		})
		if err != nil || advanced {
			return err
		}
	}
	return s.partyHostWrite(ctx, host, filter, func(tx *sql.Tx, session PartySession, nowMS int64) error {
		if fromEntryID != "" && fromEntryID != session.CurrentEntryID {
			return nil
		}
		return s.advancePartyLocked(ctx, tx, session, nowMS)
	})
}

func (s *Store) advancePartyLocked(ctx context.Context, tx *sql.Tx, session PartySession, nowMS int64) error {
	if session.CurrentEntryID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE party_entry SET state = 'done', finished_at_ms = ?
			WHERE entry_id = ? AND state = 'current'`, nowMS, session.CurrentEntryID); err != nil {
			return err
		}
	}
	state, err := loadPartyState(ctx, tx, session, nowMS)
	if err != nil {
		return err
	}
	var waitingIDs []MediaID
	for _, entry := range state.entries {
		if entry.state == PartyEntryWaiting {
			waitingIDs = append(waitingIDs, entry.mediaID)
		}
	}
	visible, err := s.visibleMediaIDsSQL(ctx, waitingIDs, session.hostFilter)
	if err != nil {
		return err
	}
	for _, entry := range state.entries {
		if _, ok := visible[entry.mediaID]; entry.state == PartyEntryWaiting && !ok {
			if _, err := tx.ExecContext(ctx, `UPDATE party_entry SET state = 'skipped', pinned_position = NULL,
				finished_at_ms = ? WHERE entry_id = ?`, nowMS, entry.id); err != nil {
				return err
			}
		}
	}
	ordered := state.waitingOrder(func(id MediaID) bool { _, ok := visible[id]; return ok })
	next := ""
	if len(ordered) > 0 {
		next = ordered[0].id
		if _, err := tx.ExecContext(ctx, `UPDATE party_entry SET state = 'current', pinned_position = NULL,
			current_at_ms = ? WHERE entry_id = ?`, nowMS, next); err != nil {
			return err
		}
		// Pins hold their place relative to the head of the rotation.
		if _, err := tx.ExecContext(ctx, `UPDATE party_entry SET pinned_position = MAX(pinned_position - 1, 1)
			WHERE session_id = ? AND state = 'waiting' AND pinned_position IS NOT NULL`, session.ID); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE party_session
		SET current_entry_id = NULLIF(?, ''), playback_state = 'paused', position_ms = 0, anchor_at_ms = ?,
			tempo = 1, pitch_semitones = COALESCE((SELECT pitch_semitones FROM party_entry WHERE entry_id = ?), 0)
		WHERE session_id = ?`, next, nowMS, next, session.ID)
	return err
}

// SetPartyPlayback stamps the current entry's playback state with the
// server clock as its anchor.
func (s *Store) SetPartyPlayback(ctx context.Context, host ProfileID, filter LibraryFilter, input PartyPlaybackInput) error {
	if input.State != "playing" && input.State != "paused" {
		return partyInvalid("state must be playing or paused")
	}
	if input.PositionMS < 0 {
		return partyInvalid("positionMS must not be negative")
	}
	if input.Tempo != nil && (*input.Tempo < 0.5 || *input.Tempo > 2 || math.IsNaN(*input.Tempo)) {
		return partyInvalid("tempo must be between 0.5 and 2")
	}
	if input.PitchSemitones != nil && (*input.PitchSemitones < -12 || *input.PitchSemitones > 12) {
		return partyInvalid("pitchSemitones must be between -12 and 12")
	}
	return s.partyHostWrite(ctx, host, filter, func(tx *sql.Tx, session PartySession, nowMS int64) error {
		if session.CurrentEntryID == "" || strings.TrimSpace(input.EntryID) != session.CurrentEntryID {
			return partyInvalid("entry is not current")
		}
		tempo, pitch := session.Tempo, session.PitchSemitones
		if input.Tempo != nil {
			tempo = *input.Tempo
		}
		if input.PitchSemitones != nil {
			pitch = *input.PitchSemitones
		}
		if _, err := tx.ExecContext(ctx, `UPDATE party_session
			SET playback_state = ?, position_ms = ?, anchor_at_ms = ?, tempo = ?, pitch_semitones = ?
			WHERE session_id = ?`, input.State, input.PositionMS, nowMS, tempo, pitch, session.ID); err != nil {
			return err
		}
		if input.State != "playing" {
			return nil
		}
		_, err := tx.ExecContext(ctx, `UPDATE party_entry SET played_at_ms = ?
			WHERE entry_id = ? AND played_at_ms IS NULL`, nowMS, session.CurrentEntryID)
		return err
	})
}

// RemovePartyParticipant removes a non-host participant, revoking a guest's
// token and removing their pending and waiting entries.
func (s *Store) RemovePartyParticipant(ctx context.Context, host ProfileID, filter LibraryFilter, participantID string) error {
	return s.partyHostWrite(ctx, host, filter, func(tx *sql.Tx, session PartySession, nowMS int64) error {
		var isHost int
		err := tx.QueryRowContext(ctx, `SELECT host FROM party_participant
			WHERE participant_id = ? AND session_id = ? AND removed_at_ms IS NULL`,
			strings.TrimSpace(participantID), session.ID).Scan(&isHost)
		if errors.Is(err, sql.ErrNoRows) {
			return partyInvalid("participant not found")
		}
		if err != nil {
			return err
		}
		if isHost == 1 {
			return partyInvalid("the host cannot be removed")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE party_participant SET removed_at_ms = ?, token_hash = NULL
			WHERE participant_id = ?`, nowMS, participantID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE party_entry
			SET state = 'removed', prior_state = state, pinned_position = NULL, removed_at_ms = ?
			WHERE participant_id = ? AND state IN ('pending', 'waiting')`, nowMS, participantID)
		return err
	})
}

// SetPartyScreenTicker sets a screen's ticker flag.
func (s *Store) SetPartyScreenTicker(ctx context.Context, host ProfileID, filter LibraryFilter, screenID string, ticker bool) error {
	return s.partyHostWrite(ctx, host, filter, func(tx *sql.Tx, session PartySession, _ int64) error {
		return requirePartyScreenUpdate(tx.ExecContext(ctx, `UPDATE party_screen SET ticker = ?
			WHERE screen_id = ? AND session_id = ? AND dropped_at_ms IS NULL`, boolInt(ticker), strings.TrimSpace(screenID), session.ID))
	})
}

// DropPartyScreen removes a screen from the session.
func (s *Store) DropPartyScreen(ctx context.Context, host ProfileID, filter LibraryFilter, screenID string) error {
	screenID = strings.TrimSpace(screenID)
	return s.partyHostWriteAction(ctx, host, filter, PartyStageActionChanged, func(tx *sql.Tx, session PartySession, nowMS int64, wroteOutbox *bool) error {
		var authSess, profID, devID string
		err := tx.QueryRowContext(ctx, `
			SELECT auth_session_id, profile_id, device_id
			FROM party_screen
			WHERE screen_id = ? AND session_id = ? AND dropped_at_ms IS NULL
		`, screenID, session.ID).Scan(&authSess, &profID, &devID)
		if errors.Is(err, sql.ErrNoRows) {
			return partyInvalid("screen not found")
		}
		if err != nil {
			return err
		}
		if err := requirePartyScreenUpdate(tx.ExecContext(ctx, `UPDATE party_screen SET dropped_at_ms = ?
			WHERE screen_id = ? AND session_id = ? AND dropped_at_ms IS NULL`, nowMS, screenID, session.ID)); err != nil {
			return err
		}
		if devID != "" || authSess != "" {
			if err := appendPartyStageEventSQL(ctx, tx, PartyStageEventPayload{
				PartySessionID: session.ID,
				ScreenID:       screenID,
				AuthSessionID:  SessionID(authSess),
				ProfileID:      ProfileID(profID),
				HostProfileID:  session.HostProfileID,
				DeviceID:       DeviceID(devID),
				Action:         PartyStageActionReleased,
				Reason:         "dropped",
			}, nowMS); err != nil {
				return err
			}
			*wroteOutbox = true
		}
		return nil
	})
}

func requirePartyScreenUpdate(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return partyInvalid("screen not found")
	}
	return nil
}

// RegisterPartyScreen registers a stage screen for a live session. The first
// popout is the lead, and an ordinary joined screen leads only while no popout
// is active; the lead's ticker starts off and every other screen's on.
func (s *Store) RegisterPartyScreen(ctx context.Context, sessionID string, profile ProfileID, authSessionID SessionID, name, kind, carrier string, playbackFilter LibraryFilter, screenPlaybackFilters map[string]LibraryFilter) (PartyScreenView, error) {
	if kind != "popout" && kind != "joined" {
		return PartyScreenView{}, partyInvalid("kind must be popout or joined")
	}
	if carrier != "local" && carrier != "relay" {
		return PartyScreenView{}, partyInvalid("carrier must be local or relay")
	}
	if authSessionID == "" {
		return PartyScreenView{}, partyInvalid("session is required")
	}
	name, err := partyName(name)
	if err != nil {
		return PartyScreenView{}, err
	}
	var view PartyScreenView
	err = s.partyWrite(ctx, func(tx *sql.Tx, nowMS int64) error {
		session, err := livePartySessionByID(ctx, tx, strings.TrimSpace(sessionID), nowMS)
		if err != nil {
			return err
		}
		state, err := loadPartyState(ctx, tx, session, nowMS)
		if err != nil {
			return err
		}
		state.screens = s.filterValidPartyScreensLocked(ctx, session.HostProfileID, state.screens)
		id, err := newPartyID("screen-")
		if err != nil {
			return err
		}
		candidate := partyScreenRow{id: id, profileID: profile, authSessionID: authSessionID, kind: kind, registeredAtMS: nowMS, active: true}
		screens := append(slices.Clone(state.screens), candidate)
		tmpState := state
		tmpState.screens = screens
		filters := make(map[string]LibraryFilter, len(screenPlaybackFilters)+1)
		maps.Copy(filters, screenPlaybackFilters)
		filters[id] = playbackFilter
		s.markPartyLeadLocked(ctx, &tmpState, filters)
		lead := tmpState.screens[len(tmpState.screens)-1].lead
		if _, err := tx.ExecContext(ctx, `INSERT INTO party_screen
			(screen_id, session_id, profile_id, auth_session_id, name, kind, carrier, ticker, registered_at_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, session.ID, profile, authSessionID, name, kind, carrier, boolInt(!lead), nowMS); err != nil {
			return err
		}
		view = PartyScreenView{
			ID: id, ProfileID: profile, AuthSessionID: authSessionID,
			Name: name, Kind: kind, Carrier: carrier, Lead: lead, Ticker: !lead,
		}
		return nil
	})
	return view, err
}

// ReportPartyScreen records a screen's measured clock offset and round trip.
// Only the registering Session may report.
func (s *Store) ReportPartyScreen(ctx context.Context, screenID string, profile ProfileID, authSessionID SessionID, offsetMS, rttMS int64) error {
	if rttMS < 0 {
		return partyInvalid("rttMS must not be negative")
	}
	if authSessionID == "" {
		return partyInvalid("screen not found")
	}
	return s.partyWrite(ctx, func(tx *sql.Tx, nowMS int64) error {
		return requirePartyScreenUpdate(tx.ExecContext(ctx, `UPDATE party_screen SET offset_ms = ?, rtt_ms = ?, reported_at_ms = ?
			WHERE screen_id = ? AND profile_id = ? AND auth_session_id = ? AND dropped_at_ms IS NULL
				AND session_id IN (SELECT session_id FROM party_session WHERE ended_at_ms IS NULL)`,
			offsetMS, rttMS, nowMS, strings.TrimSpace(screenID), profile, authSessionID))
	})
}

// PartyStage returns what a stage screen of a live session renders for its
// owning Session, filtered by that Session's current LibraryFilter and
// PlaybackFilter.
func (s *Store) PartyStage(ctx context.Context, sessionID, screenID string, callerSession SessionID, libraryFilter, playbackFilter LibraryFilter, screenPlaybackFilters map[string]LibraryFilter) (PartyStageView, error) {
	sessionID = strings.TrimSpace(sessionID)
	screenID = strings.TrimSpace(screenID)
	if screenID == "" {
		return PartyStageView{}, partyInvalid("screenID is required")
	}
	if callerSession == "" {
		return PartyStageView{}, partyInvalid("screen not found")
	}
	var nowMS int64
	var callerScreen partyScreenRow
	var lazyDrop bool
	var lazyDropHost ProfileID
	var lazyDropParty string
	filters := make(map[string]LibraryFilter, len(screenPlaybackFilters)+1)
	maps.Copy(filters, screenPlaybackFilters)
	filters[screenID] = playbackFilter
	view, err := s.readPartyView(ctx, filters, func(q partyQuerier, now int64) (PartySession, error) {
		nowMS = now
		session, err := livePartySessionByID(ctx, q, sessionID, now)
		if err != nil {
			return PartySession{}, err
		}
		var ticker, chosenLead int
		var authSess, devID string
		err = q.QueryRowContext(ctx, `SELECT screen_id, profile_id, auth_session_id, device_id, binding_generation, chosen_lead,
				name, kind, carrier, ticker, offset_ms, rtt_ms, registered_at_ms, COALESCE(reported_at_ms, 0)
			FROM party_screen s
			WHERE screen_id = ? AND session_id = ? AND auth_session_id = ? AND `+activePartyScreenSQL,
			screenID, session.ID, callerSession, now-partyScreenSilence.Milliseconds()).
			Scan(&callerScreen.id, &callerScreen.profileID, &authSess, &devID, &callerScreen.bindingGeneration, &chosenLead,
				&callerScreen.name, &callerScreen.kind, &callerScreen.carrier, &ticker, &callerScreen.offsetMS, &callerScreen.rttMS,
				&callerScreen.registeredAtMS, &callerScreen.reportedAtMS)
		if errors.Is(err, sql.ErrNoRows) {
			return PartySession{}, partyInvalid("screen not found")
		}
		if err != nil {
			return PartySession{}, err
		}
		callerScreen.authSessionID = SessionID(authSess)
		callerScreen.deviceID = DeviceID(devID)
		callerScreen.chosenLead = chosenLead == 1
		callerScreen.ticker = ticker == 1
		if callerScreen.deviceID != "" {
			valid, reason := s.isPartyPlayerScreenValidLocked(ctx, session.HostProfileID, callerScreen)
			if !valid {
				lazyDrop = true
				lazyDropHost = session.HostProfileID
				lazyDropParty = session.ID
				_ = reason
				return PartySession{}, partyInvalid("screen not found")
			}
		}
		return session, nil
	})
	if lazyDrop {
		_ = s.dropStalePartyDisplay(ctx, callerScreen, lazyDropParty, lazyDropHost)
	}
	if err != nil {
		return PartyStageView{}, err
	}

	stage := PartyStageView{
		SessionID: view.Session.ID, ServerNowMS: nowMS, Current: view.Current, Playback: view.Playback,
		UpNext: view.Waiting[:min(len(view.Waiting), PartyUpNext)], Ticker: callerScreen.ticker, JoinCode: view.Session.JoinCode,
	}
	for _, screen := range view.Screens {
		if screen.ID == screenID {
			stage.Ticker = screen.Ticker
			stage.Lead = screen.Lead
		}
	}

	// Per-screen eligibility (ADR-0240 D10): current picture/stream require
	// PlaybackFilter; ticker titles/artwork require LibraryFilter.
	if err := s.filterPartyStageForScreen(ctx, &stage, libraryFilter, playbackFilter); err != nil {
		return PartyStageView{}, err
	}
	return stage, nil
}

func (s *Store) partyScreenTicker(ctx context.Context, sessionID, screenID string) (bool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.guardLocked() != nil {
		return false, false
	}
	var ticker int
	err := s.eventsR.QueryRowContext(ctx, `SELECT ticker FROM party_screen
		WHERE screen_id = ? AND session_id = ? AND dropped_at_ms IS NULL`, screenID, sessionID).Scan(&ticker)
	return ticker == 1, err == nil
}

const partyGuestQuery = `SELECT p.participant_id, p.name, p.session_id,
		MIN(p.token_expires_at_ms, s.expires_at_ms)
	FROM party_participant p JOIN party_session s ON s.session_id = p.session_id
	WHERE p.token_hash = ? AND p.guest = 1 AND p.removed_at_ms IS NULL AND p.token_expires_at_ms > ?
		AND s.ended_at_ms IS NULL AND s.expires_at_ms > ?`

func partyGuestByToken(ctx context.Context, q partyQuerier, tokenHash string, nowMS int64) (PartyGuest, error) {
	if tokenHash == "" {
		return PartyGuest{}, ErrPartyGuestRefused
	}
	var guest PartyGuest
	var expiresMS int64
	err := q.QueryRowContext(ctx, partyGuestQuery, tokenHash, nowMS, nowMS).
		Scan(&guest.ParticipantID, &guest.Name, &guest.SessionID, &expiresMS)
	if errors.Is(err, sql.ErrNoRows) {
		return PartyGuest{}, ErrPartyGuestRefused
	}
	guest.ExpiresAt = millisToTime(expiresMS)
	return guest, err
}

// JoinParty admits a named guest to the live, open session holding code and
// stores only the token's hash. Every unusable code fails with
// ErrPartyJoinRefused.
func (s *Store) JoinParty(ctx context.Context, code, name, tokenHash string) (PartyGuest, error) {
	code = NormalizePartyJoinCode(code)
	name, err := partyName(name)
	if err != nil {
		return PartyGuest{}, err
	}
	if tokenHash == "" {
		return PartyGuest{}, errors.New("party token hash is required")
	}
	var guest PartyGuest
	err = s.partyWrite(ctx, func(tx *sql.Tx, nowMS int64) error {
		session, err := scanPartySession(tx.QueryRowContext(ctx, `SELECT `+partySessionColumns+`
			FROM party_session WHERE join_code = ? AND join_open = 1 AND ended_at_ms IS NULL AND expires_at_ms > ?`, code, nowMS))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPartyJoinRefused
		}
		if err != nil {
			return err
		}
		var guests int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM party_participant
			WHERE session_id = ? AND guest = 1 AND removed_at_ms IS NULL`, session.ID).Scan(&guests); err != nil {
			return err
		}
		if guests >= PartyMaxGuests {
			return ErrPartyFull
		}
		id, err := newPartyID("pp-")
		if err != nil {
			return err
		}
		tokenExpiresMS := nowMS + PartyLifetime.Milliseconds()
		if _, err := tx.ExecContext(ctx, `INSERT INTO party_participant
			(participant_id, session_id, name, guest, token_hash, token_expires_at_ms, joined_at_ms)
			VALUES (?, ?, ?, 1, ?, ?, ?)`, id, session.ID, name, tokenHash, tokenExpiresMS, nowMS); err != nil {
			return err
		}
		guest = PartyGuest{
			SessionID: session.ID, ParticipantID: id, Name: name,
			ExpiresAt: millisToTime(min(tokenExpiresMS, timeToMillis(session.ExpiresAt))),
		}
		return nil
	})
	return guest, err
}

// PartyGuestByToken authenticates a guest token hash.
func (s *Store) PartyGuestByToken(ctx context.Context, tokenHash string) (PartyGuest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PartyGuest{}, err
	}
	return partyGuestByToken(ctx, s.eventsR, tokenHash, timeToMillis(s.serverNow()))
}

// PartyGuestView returns the guest's session read through the host's stored
// visibility.
func (s *Store) PartyGuestView(ctx context.Context, tokenHash string) (PartyGuest, PartyView, error) {
	var guest PartyGuest
	view, err := s.readPartyView(ctx, nil, func(q partyQuerier, nowMS int64) (PartySession, error) {
		var err error
		if guest, err = partyGuestByToken(ctx, q, tokenHash, nowMS); err != nil {
			return PartySession{}, err
		}
		return livePartySessionByID(ctx, q, guest.SessionID, nowMS)
	})
	if errors.Is(err, ErrPartyNotFound) {
		err = ErrPartyGuestRefused
	}
	return guest, view, err
}

// SearchPartyMedia searches the guest's session Libraries within the host's
// visibility, returning at most limit Media.
func (s *Store) SearchPartyMedia(ctx context.Context, tokenHash, query string, limit int) ([]PartyMediaView, error) {
	_, view, err := s.PartyGuestView(ctx, tokenHash)
	if err != nil {
		return nil, err
	}
	filter := view.Session.hostFilter
	items, err := s.SearchMedia(ctx, filter, query, limit*2)
	if err != nil {
		return nil, err
	}
	var ids []MediaID
	for _, item := range items {
		if item.Kind == "media" {
			ids = append(ids, item.ID)
		}
	}
	media, err := func() (map[MediaID]Media, error) {
		s.mu.RLock()
		defer s.mu.RUnlock()
		if err := s.guardLocked(); err != nil {
			return nil, err
		}
		return s.visibleMediaBatchSQL(ctx, ids, filter)
	}()
	if err != nil {
		return nil, err
	}
	played, err := s.partyMediaLastCurrent(ctx, view.Session.ID)
	if err != nil {
		return nil, err
	}
	out := make([]PartyMediaView, 0, min(len(ids), limit))
	var found []MediaID
	for _, id := range ids {
		m, ok := media[id]
		if !ok || len(out) == limit {
			continue
		}
		out = append(out, PartyMediaView{MediaID: id, Title: m.Title, DurationSeconds: m.DurationSeconds, LastPlayedAt: played[id]})
		found = append(found, id)
	}
	subtitles := s.partySubtitles(ctx, found)
	for i := range out {
		out[i].Subtitle = subtitles[out[i].MediaID]
	}
	return out, nil
}

func (s *Store) partyMediaLastCurrent(ctx context.Context, sessionID string) (map[MediaID]time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.eventsR.QueryContext(ctx, `SELECT media_id, MAX(current_at_ms) FROM party_entry
		WHERE session_id = ? AND current_at_ms IS NOT NULL GROUP BY media_id`, sessionID)
	if err != nil {
		return nil, err
	}
	out := map[MediaID]time.Time{}
	for rows.Next() {
		var id MediaID
		var atMS int64
		if err := rows.Scan(&id, &atMS); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out[id] = millisToTime(atMS)
	}
	return out, closeRows(rows)
}

// RequestPartyEntry adds a guest request at the guest's chosen key: waiting
// at once under auto-add, otherwise pending host approval. It returns the new
// entry.
func (s *Store) RequestPartyEntry(ctx context.Context, tokenHash string, mediaID MediaID, pitchSemitones int) (PartyEntryView, error) {
	if mediaID == "" {
		return PartyEntryView{}, partyInvalid("mediaID is required")
	}
	if pitchSemitones < -12 || pitchSemitones > 12 {
		return PartyEntryView{}, partyInvalid("pitchSemitones must be between -12 and 12")
	}
	var created PartyEntryView
	err := s.partyWrite(ctx, func(tx *sql.Tx, nowMS int64) error {
		guest, err := partyGuestByToken(ctx, tx, tokenHash, nowMS)
		if err != nil {
			return err
		}
		session, err := livePartySessionByID(ctx, tx, guest.SessionID, nowMS)
		if err != nil {
			return ErrPartyGuestRefused
		}
		if session, err = s.partyLiveSessionLocked(ctx, session); err != nil {
			return err
		}
		media, err := s.visibleMediaBatchSQL(ctx, []MediaID{mediaID}, session.hostFilter)
		if err != nil {
			return err
		}
		m, ok := media[mediaID]
		if !ok {
			return partyInvalid("media not found")
		}
		var open int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM party_entry
			WHERE participant_id = ? AND state IN ('pending', 'waiting')`, guest.ParticipantID).Scan(&open); err != nil {
			return err
		}
		if open >= PartyMaxGuestWaiting {
			return ErrPartyWaitingLimit
		}
		state := PartyEntryPending
		if session.AutoAdd {
			state = PartyEntryWaiting
		}
		entryID, err := newPartyID("pe-")
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO party_entry (entry_id, session_id, participant_id, media_id, state,
				pitch_semitones, added_at_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, entryID, session.ID, guest.ParticipantID, mediaID, state, pitchSemitones, nowMS); err != nil {
			return err
		}
		created = PartyEntryView{
			ID: entryID, MediaID: mediaID, Title: m.Title, DurationSeconds: m.DurationSeconds,
			ParticipantID: guest.ParticipantID, ParticipantName: guest.Name, State: state, PitchSemitones: pitchSemitones,
			AddedAt: millisToTime(nowMS),
		}
		return nil
	})
	return created, err
}

// CancelPartyEntry withdraws one of the guest's own pending or waiting
// entries.
func (s *Store) CancelPartyEntry(ctx context.Context, tokenHash, entryID string) error {
	return s.partyWrite(ctx, func(tx *sql.Tx, nowMS int64) error {
		guest, err := partyGuestByToken(ctx, tx, tokenHash, nowMS)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM party_entry
			WHERE entry_id = ? AND participant_id = ? AND state IN ('pending', 'waiting')`,
			strings.TrimSpace(entryID), guest.ParticipantID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrPartyNotFound
		}
		return nil
	})
}

// deletePartyProfileTx erases a Profile's party footprint: every session it
// hosted (participants, entries and screens cascade) and the screens it
// registered on other sessions.
func deletePartyProfileTx(ctx context.Context, tx *sql.Tx, profileID ProfileID) error {
	for _, statement := range []string{
		`DELETE FROM party_entry WHERE session_id IN (SELECT session_id FROM party_session WHERE host_profile_id = ?)`,
		`DELETE FROM party_screen WHERE session_id IN (SELECT session_id FROM party_session WHERE host_profile_id = ?)`,
		`DELETE FROM party_participant WHERE session_id IN (SELECT session_id FROM party_session WHERE host_profile_id = ?)`,
		`DELETE FROM party_session WHERE host_profile_id = ?`,
		`DELETE FROM party_screen WHERE profile_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, profileID); err != nil {
			return err
		}
	}
	return nil
}

// AddPartyPlayerDisplay joins a controlled player display to the host's live
// party as a follower and reports whether the display's Libraries cover the
// party's opened Libraries.
func (s *Store) AddPartyPlayerDisplay(ctx context.Context, host ProfileID, filter LibraryFilter, display PlayerDisplay, carrier string) (PartyScreenJoinView, error) {
	if carrier == "" {
		carrier = "local"
	}
	if carrier != "local" && carrier != "relay" {
		return PartyScreenJoinView{}, partyInvalid("carrier must be local or relay")
	}
	if display.DeviceID == "" || display.PlayerProfileID == "" || display.SessionID == "" {
		return PartyScreenJoinView{}, partyInvalid("display not found")
	}
	name := strings.TrimSpace(display.DisplayName)
	if name == "" {
		name = strings.TrimSpace(display.DeviceName)
	}
	if name == "" {
		name = "Display"
	}
	if utf8.RuneCountInString(name) > partyNameMaxRunes {
		runes := []rune(name)
		name = string(runes[:partyNameMaxRunes])
	}

	var out PartyScreenJoinView
	err := s.partyHostWriteAction(ctx, host, filter, "", func(tx *sql.Tx, session PartySession, nowMS int64, wroteOutbox *bool) error {
		if err := s.reconcilePartyPlayerDisplaysTx(ctx, tx, session.ID, session.HostProfileID, nowMS, wroteOutbox); err != nil {
			return err
		}
		var otherSession string
		err := tx.QueryRowContext(ctx, `
			SELECT s.session_id
			FROM party_screen s
			JOIN party_session p ON p.session_id = s.session_id
			WHERE s.device_id = ? AND s.dropped_at_ms IS NULL AND s.session_id <> ?
				AND p.ended_at_ms IS NULL AND p.expires_at_ms > ?
			LIMIT 1
		`, display.DeviceID, session.ID, nowMS).Scan(&otherSession)
		if err == nil {
			return ErrPartyDisplayAlreadyJoined
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		covers, missing, err := s.playerDisplayLibraryCoverageLocked(ctx, display.PlayerProfileID, session.LibraryIDs)
		if err != nil {
			return err
		}
		out.CoversLibraries = covers
		out.MissingLibraryIDs = missing

		var existing partyScreenRow
		var ticker, chosenLead int
		var authSess, devID string
		err = tx.QueryRowContext(ctx, `
			SELECT screen_id, profile_id, auth_session_id, device_id, binding_generation, chosen_lead,
				name, kind, carrier, ticker, offset_ms, rtt_ms, registered_at_ms, COALESCE(reported_at_ms, 0)
			FROM party_screen
			WHERE session_id = ? AND device_id = ? AND dropped_at_ms IS NULL
			LIMIT 1
		`, session.ID, display.DeviceID).Scan(
			&existing.id, &existing.profileID, &authSess, &devID, &existing.bindingGeneration, &chosenLead,
			&existing.name, &existing.kind, &existing.carrier, &ticker, &existing.offsetMS, &existing.rttMS,
			&existing.registeredAtMS, &existing.reportedAtMS,
		)
		if err == nil {
			existing.authSessionID = SessionID(authSess)
			existing.deviceID = DeviceID(devID)
			existing.chosenLead = chosenLead == 1
			existing.ticker = ticker == 1
			out.Screen = existing.view()
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		id, err := newPartyID("screen-")
		if err != nil {
			return err
		}
		// A fresh registration cannot revive URLs issued for a former join.
		if _, err := tx.ExecContext(ctx, `UPDATE playback_grant SET cancelled_at_ms = ?
			WHERE session_id = ? AND originator_profile_id = '' AND walk_up = 0 AND ambient = 0
				AND ended_at_ms IS NULL AND cancelled_at_ms IS NULL`, nowMS, display.SessionID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO party_screen
			(screen_id, session_id, profile_id, auth_session_id, device_id, binding_generation, chosen_lead, name, kind, carrier, ticker, registered_at_ms)
			VALUES (?, ?, ?, ?, ?, ?, 0, ?, 'joined', ?, 1, ?)`,
			id, session.ID, display.PlayerProfileID, display.SessionID, display.DeviceID, display.BindingGeneration, name, carrier, nowMS); err != nil {
			return err
		}
		out.Screen = PartyScreenView{
			ID:                id,
			ProfileID:         display.PlayerProfileID,
			AuthSessionID:     display.SessionID,
			DeviceID:          display.DeviceID,
			BindingGeneration: display.BindingGeneration,
			Name:              name,
			Kind:              "joined",
			Carrier:           carrier,
			Lead:              false,
			Ticker:            true,
		}
		if err := appendPartyStageEventSQL(ctx, tx, PartyStageEventPayload{
			PartySessionID: session.ID,
			ScreenID:       id,
			AuthSessionID:  display.SessionID,
			ProfileID:      display.PlayerProfileID,
			HostProfileID:  session.HostProfileID,
			DeviceID:       display.DeviceID,
			Action:         PartyStageActionJoined,
			Lead:           false,
		}, nowMS); err != nil {
			return err
		}
		*wroteOutbox = true
		return nil
	})
	return out, err
}

// SetPartyScreenLead selects screenID as the preferred lead screen for the
// host's live party, refusing if a current entry exists that screenID cannot
// play under its current PlaybackFilter.
func (s *Store) SetPartyScreenLead(ctx context.Context, host ProfileID, filter LibraryFilter, screenID string, screenPlaybackFilters map[string]LibraryFilter) error {
	screenID = strings.TrimSpace(screenID)
	if screenID == "" {
		return partyInvalid("screenID is required")
	}
	return s.partyHostWriteAction(ctx, host, filter, PartyStageActionChanged, func(tx *sql.Tx, session PartySession, nowMS int64, wroteOutbox *bool) error {
		if err := s.reconcilePartyPlayerDisplaysTx(ctx, tx, session.ID, session.HostProfileID, nowMS, wroteOutbox); err != nil {
			return err
		}
		state, err := loadPartyState(ctx, tx, session, nowMS)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(state.screens, func(screen partyScreenRow) bool { return screen.id == screenID }) {
			return partyInvalid("screen not found")
		}
		pbFilter, has := screenPlaybackFilters[screenID]
		if !has {
			return ErrPartyScreenIneligibleLead
		}
		if current, ok := state.currentEntry(); ok {
			visible, err := s.visibleMediaIDsSQL(ctx, []MediaID{current.mediaID}, pbFilter)
			if err != nil {
				return err
			}
			if _, ok := visible[current.mediaID]; !ok {
				return ErrPartyScreenIneligibleLead
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE party_screen SET chosen_lead = 0 WHERE session_id = ?`, session.ID); err != nil {
			return err
		}
		return requirePartyScreenUpdate(tx.ExecContext(ctx, `UPDATE party_screen SET chosen_lead = 1 WHERE screen_id = ? AND session_id = ? AND dropped_at_ms IS NULL`,
			screenID, session.ID))
	})
}

// PartyLibraryCoverage reports whether playerProfileID's visible Libraries
// cover every Library in partyLibraryIDs.
func (s *Store) PartyLibraryCoverage(ctx context.Context, playerProfileID ProfileID, partyLibraryIDs []LibraryID) (bool, []LibraryID, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, nil, err
	}
	return s.playerDisplayLibraryCoverageLocked(ctx, playerProfileID, partyLibraryIDs)
}

func (s *Store) playerDisplayLibraryCoverageLocked(ctx context.Context, playerProfileID ProfileID, partyLibraryIDs []LibraryID) (bool, []LibraryID, error) {
	profile, err := s.profileSQL(ctx, playerProfileID)
	if err != nil {
		return false, nil, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return false, nil, err
	}
	byID := make(map[LibraryID]Library, len(libraries))
	for _, lib := range libraries {
		byID[lib.ID] = lib
	}
	playerFilter := LibraryFilter{AllowedIDs: profile.Library, Revealed: false}
	missing := []LibraryID{}
	for _, id := range partyLibraryIDs {
		lib, ok := byID[id]
		if !ok || !libraryVisible(lib, playerFilter) {
			missing = append(missing, id)
		}
	}
	return len(missing) == 0, missing, nil
}

func (s *Store) filterPartyStageForScreen(ctx context.Context, stage *PartyStageView, libraryFilter, playbackFilter LibraryFilter) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if stage.Current != nil && stage.Current.MediaID != "" {
		visible, err := s.visibleMediaIDsSQL(ctx, []MediaID{stage.Current.MediaID}, playbackFilter)
		if err != nil {
			return err
		}
		if _, ok := visible[stage.Current.MediaID]; !ok {
			redacted := *stage.Current
			redacted.MediaID = ""
			redacted.Title = ""
			redacted.Subtitle = ""
			redacted.DurationSeconds = 0
			redacted.LastPlayedAt = time.Time{}
			redacted.Ineligible = true
			if redacted.ParticipantName != "" {
				redacted.Label = "Up next: " + redacted.ParticipantName
			} else {
				redacted.Label = "Up next"
			}
			stage.Current = &redacted
			stage.Playback = nil
		}
	}
	if len(stage.UpNext) > 0 {
		// An ETA also exposes the current item's remaining time and every
		// preceding duration. Missing durations use a party-wide median.
		timingVisible := stage.Current == nil || stage.Current.DurationSeconds > 0
		ids := make([]MediaID, 0, len(stage.UpNext))
		for _, entry := range stage.UpNext {
			if entry.MediaID != "" {
				ids = append(ids, entry.MediaID)
			}
		}
		visible, err := s.visibleMediaIDsSQL(ctx, ids, libraryFilter)
		if err != nil {
			return err
		}
		for i := range stage.UpNext {
			entry := &stage.UpNext[i]
			_, eligible := visible[entry.MediaID]
			if !timingVisible || !eligible {
				entry.ETASeconds = 0
			}
			timingVisible = timingVisible && eligible && entry.DurationSeconds > 0
			if !eligible && entry.MediaID != "" {
				entry.MediaID = ""
				entry.Title = ""
				entry.Subtitle = ""
				entry.DurationSeconds = 0
				entry.LastPlayedAt = time.Time{}
				entry.Ineligible = true
				entry.Label = entry.ParticipantName
			}
		}
	}
	return nil
}

func (s *Store) filterValidPartyScreensLocked(ctx context.Context, hostProfileID ProfileID, screens []partyScreenRow) []partyScreenRow {
	if len(screens) == 0 {
		return screens
	}
	out := make([]partyScreenRow, 0, len(screens))
	for _, screen := range screens {
		if screen.deviceID != "" {
			valid, _ := s.isPartyPlayerScreenValidLocked(ctx, hostProfileID, screen)
			if !valid {
				continue
			}
		}
		out = append(out, screen)
	}
	return out
}

func (s *Store) markPartyLeadLocked(ctx context.Context, state *partyState, screenPlaybackFilters map[string]LibraryFilter) {
	current, hasCurrent := state.currentEntry()
	playable := map[string]bool{}
	for _, screen := range state.screens {
		if !screen.active {
			continue
		}
		pbFilter, ok := screenPlaybackFilters[screen.id]
		if !ok {
			continue
		}
		if !hasCurrent {
			playable[screen.id] = true
			continue
		}
		visible, err := s.visibleMediaIDsSQL(ctx, []MediaID{current.mediaID}, pbFilter)
		if err == nil {
			_, playable[screen.id] = visible[current.mediaID]
		}
	}
	markPartyLead(state.screens, func(screen partyScreenRow) bool {
		return playable[screen.id]
	})
}

// isPartyPlayerScreenValidLocked reports whether a joined player display screen
// is still validly bound at its join generation and controlled by hostProfileID.
// When false, the returned reason is "rebound" or "controller-removed".
func (s *Store) isPartyPlayerScreenValidLocked(ctx context.Context, hostProfileID ProfileID, screen partyScreenRow) (bool, string) {
	if screen.deviceID == "" {
		return true, ""
	}
	var revoked, generation int
	var playerProfileID string
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT revoked, COALESCE(player_profile_id, ''), player_binding_generation
		FROM device WHERE id = ?
	`, screen.deviceID).Scan(&revoked, &playerProfileID, &generation)
	if err != nil || revoked != 0 || ProfileID(playerProfileID) != screen.profileID || generation != screen.bindingGeneration {
		return false, "rebound"
	}
	isCtrl, err := isPlayerControllerLocked(ctx, s.catalogR, screen.profileID, hostProfileID)
	if err != nil || !isCtrl {
		return false, "controller-removed"
	}
	return true, ""
}

func (s *Store) reconcilePartyPlayerDisplaysTx(ctx context.Context, tx *sql.Tx, partySessionID string, hostProfileID ProfileID, nowMS int64, wroteOutbox *bool) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT screen_id, profile_id, auth_session_id, device_id, binding_generation
		FROM party_screen
		WHERE session_id = ? AND dropped_at_ms IS NULL AND device_id <> ''
	`, partySessionID)
	if err != nil {
		return err
	}
	var screens []partyScreenRow
	for rows.Next() {
		var row partyScreenRow
		var authSess, devID string
		if err := rows.Scan(&row.id, &row.profileID, &authSess, &devID, &row.bindingGeneration); err != nil {
			_ = rows.Close()
			return err
		}
		row.authSessionID = SessionID(authSess)
		row.deviceID = DeviceID(devID)
		screens = append(screens, row)
	}
	if err := closeRows(rows); err != nil {
		return err
	}
	for _, screen := range screens {
		valid, reason := s.isPartyPlayerScreenValidLocked(ctx, hostProfileID, screen)
		if valid {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE party_screen SET dropped_at_ms = ? WHERE screen_id = ? AND dropped_at_ms IS NULL`, nowMS, screen.id); err != nil {
			return err
		}
		if reason == "controller-removed" {
			if err := appendPartyStageEventSQL(ctx, tx, PartyStageEventPayload{
				PartySessionID: partySessionID,
				ScreenID:       screen.id,
				AuthSessionID:  screen.authSessionID,
				ProfileID:      screen.profileID,
				HostProfileID:  hostProfileID,
				DeviceID:       screen.deviceID,
				Action:         PartyStageActionReleased,
				Reason:         reason,
			}, nowMS); err != nil {
				return err
			}
			*wroteOutbox = true
		}
	}
	return nil
}

func (s *Store) notifyActivePartyDisplaysTx(ctx context.Context, tx *sql.Tx, partySessionID string, hostProfileID ProfileID, nowMS int64, action string, wroteOutbox *bool) error {
	if action == "" {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT screen_id, profile_id, auth_session_id, device_id
		FROM party_screen
		WHERE session_id = ? AND dropped_at_ms IS NULL AND device_id <> ''
	`, partySessionID)
	if err != nil {
		return err
	}
	var payloads []PartyStageEventPayload
	for rows.Next() {
		var p PartyStageEventPayload
		if err := rows.Scan(&p.ScreenID, &p.ProfileID, &p.AuthSessionID, &p.DeviceID); err != nil {
			_ = rows.Close()
			return err
		}
		p.PartySessionID = partySessionID
		p.HostProfileID = hostProfileID
		p.Action = action
		payloads = append(payloads, p)
	}
	if err := closeRows(rows); err != nil {
		return err
	}
	for _, p := range payloads {
		if err := appendPartyStageEventSQL(ctx, tx, p, nowMS); err != nil {
			return err
		}
		*wroteOutbox = true
	}
	return nil
}

func (s *Store) reconcileAndNotifyPartyDisplaysTx(ctx context.Context, tx *sql.Tx, partySessionID string, hostProfileID ProfileID, nowMS int64, action string, wroteOutbox *bool) error {
	if err := s.reconcilePartyPlayerDisplaysTx(ctx, tx, partySessionID, hostProfileID, nowMS, wroteOutbox); err != nil {
		return err
	}
	return s.notifyActivePartyDisplaysTx(ctx, tx, partySessionID, hostProfileID, nowMS, action, wroteOutbox)
}

func (s *Store) dropStalePartyDisplay(ctx context.Context, screen partyScreenRow, partySessionID string, hostProfileID ProfileID) error {
	return s.partyWriteOutbox(ctx, func(tx *sql.Tx, nowMS int64, wroteOutbox *bool) error {
		valid, reason := s.isPartyPlayerScreenValidLocked(ctx, hostProfileID, screen)
		if valid {
			return nil
		}
		res, err := tx.ExecContext(ctx, `UPDATE party_screen SET dropped_at_ms = ? WHERE screen_id = ? AND dropped_at_ms IS NULL`, nowMS, screen.id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		if reason == "controller-removed" {
			if err := appendPartyStageEventSQL(ctx, tx, PartyStageEventPayload{
				PartySessionID: partySessionID,
				ScreenID:       screen.id,
				AuthSessionID:  screen.authSessionID,
				ProfileID:      screen.profileID,
				HostProfileID:  hostProfileID,
				DeviceID:       screen.deviceID,
				Action:         PartyStageActionReleased,
				Reason:         reason,
			}, nowMS); err != nil {
				return err
			}
			*wroteOutbox = true
		}
		return nil
	})
}

func (s *Store) releaseIneligiblePartyDisplaysForPlayerLocked(ctx context.Context, playerProfileID ProfileID) {
	if playerProfileID == "" || s.eventsW == nil {
		return
	}
	_ = s.partyWriteOutboxLocked(ctx, func(tx *sql.Tx, nowMS int64, wroteOutbox *bool) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT s.screen_id, s.session_id, s.profile_id, s.auth_session_id, s.device_id, s.binding_generation, p.host_profile_id
			FROM party_screen s
			JOIN party_session p ON p.session_id = s.session_id
			WHERE s.profile_id = ? AND s.device_id <> '' AND s.dropped_at_ms IS NULL
				AND p.ended_at_ms IS NULL AND p.expires_at_ms > ?
		`, playerProfileID, nowMS)
		if err != nil {
			return err
		}
		type item struct {
			screen        partyScreenRow
			partyID       string
			hostProfileID ProfileID
		}
		var items []item
		for rows.Next() {
			var it item
			var authSess, devID string
			if err := rows.Scan(&it.screen.id, &it.partyID, &it.screen.profileID, &authSess, &devID, &it.screen.bindingGeneration, &it.hostProfileID); err != nil {
				_ = rows.Close()
				return err
			}
			it.screen.authSessionID = SessionID(authSess)
			it.screen.deviceID = DeviceID(devID)
			items = append(items, it)
		}
		if err := closeRows(rows); err != nil {
			return err
		}
		for _, it := range items {
			valid, reason := s.isPartyPlayerScreenValidLocked(ctx, it.hostProfileID, it.screen)
			if valid {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE party_screen SET dropped_at_ms = ? WHERE screen_id = ? AND dropped_at_ms IS NULL`, nowMS, it.screen.id); err != nil {
				return err
			}
			if reason == "controller-removed" {
				if err := appendPartyStageEventSQL(ctx, tx, PartyStageEventPayload{
					PartySessionID: it.partyID,
					ScreenID:       it.screen.id,
					AuthSessionID:  it.screen.authSessionID,
					ProfileID:      it.screen.profileID,
					HostProfileID:  it.hostProfileID,
					DeviceID:       it.screen.deviceID,
					Action:         PartyStageActionReleased,
					Reason:         reason,
				}, nowMS); err != nil {
					return err
				}
				*wroteOutbox = true
			}
		}
		return nil
	})
}

const activePartyScreenSQL = `s.dropped_at_ms IS NULL
	AND MAX(s.registered_at_ms, COALESCE(s.reported_at_ms, 0)) > ?`

// PartyScreenOwnedBySession reports whether screenID is an active screen
// belonging to authSessionID in a live party session.
func (s *Store) PartyScreenOwnedBySession(ctx context.Context, screenID string, authSessionID SessionID) (bool, error) {
	screenID = strings.TrimSpace(screenID)
	if screenID == "" || authSessionID == "" {
		return false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	nowMS := timeToMillis(s.serverNow())
	var row partyScreenRow
	var partyID string
	var hostProfileID ProfileID
	var authSess, devID string
	err := s.eventsR.QueryRowContext(ctx, `
		SELECT s.screen_id, s.session_id, s.profile_id, s.auth_session_id, s.device_id, s.binding_generation, p.host_profile_id
		FROM party_screen s
		JOIN party_session p ON p.session_id = s.session_id
		WHERE s.screen_id = ? AND s.auth_session_id = ? AND `+activePartyScreenSQL+`
			AND p.ended_at_ms IS NULL AND p.expires_at_ms > ?
	`, screenID, authSessionID, nowMS-partyScreenSilence.Milliseconds(), nowMS).Scan(&row.id, &partyID, &row.profileID, &authSess, &devID, &row.bindingGeneration, &hostProfileID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	row.authSessionID = SessionID(authSess)
	row.deviceID = DeviceID(devID)
	if row.deviceID != "" {
		valid, _ := s.isPartyPlayerScreenValidLocked(ctx, hostProfileID, row)
		return valid, nil
	}
	return true, nil
}

// IsPlayerPartyCurrentMedia reports whether authSessionID holds an active
// party stage screen on a live party whose current entry is mediaID (and for
// player displays, whose host remains a controller at the current binding
// generation).
func (s *Store) IsPlayerPartyCurrentMedia(ctx context.Context, authSessionID SessionID, mediaID MediaID) (bool, error) {
	if authSessionID == "" || mediaID == "" {
		return false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	return s.isPlayerPartyCurrentMediaLocked(ctx, s.eventsR, authSessionID, mediaID)
}

func (s *Store) isPlayerPartyCurrentMediaLocked(ctx context.Context, q queryRower, authSessionID SessionID, mediaID MediaID) (bool, error) {
	nowMS := timeToMillis(s.serverNow())
	var row partyScreenRow
	var hostProfileID ProfileID
	var authSess, devID string
	err := q.QueryRowContext(ctx, `
		SELECT s.screen_id, s.profile_id, s.auth_session_id, s.device_id, s.binding_generation, p.host_profile_id
		FROM party_screen s
		JOIN party_session p ON p.session_id = s.session_id
		JOIN party_entry e ON e.entry_id = p.current_entry_id AND e.session_id = p.session_id
		WHERE s.auth_session_id = ? AND s.device_id <> '' AND `+activePartyScreenSQL+`
			AND p.ended_at_ms IS NULL AND p.expires_at_ms > ?
			AND e.state = 'current' AND e.media_id = ?
		LIMIT 1
	`, authSessionID, nowMS-partyScreenSilence.Milliseconds(), nowMS, mediaID).Scan(&row.id, &row.profileID, &authSess, &devID, &row.bindingGeneration, &hostProfileID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	row.authSessionID = SessionID(authSess)
	row.deviceID = DeviceID(devID)
	valid, _ := s.isPartyPlayerScreenValidLocked(ctx, hostProfileID, row)
	return valid, nil
}

// IsPlayerDeviceInLiveParty reports whether deviceID or authSessionID is
// currently joined to a live party session (lazily releasing stale entries).
func (s *Store) IsPlayerDeviceInLiveParty(ctx context.Context, deviceID DeviceID, authSessionID SessionID, now time.Time) (bool, error) {
	if deviceID == "" && authSessionID == "" {
		return false, nil
	}
	var inParty bool
	err := s.partyWriteOutbox(ctx, func(tx *sql.Tx, nowMS int64, wroteOutbox *bool) error {
		var err error
		inParty, err = s.playerDeviceInLivePartyTx(ctx, tx, deviceID, authSessionID, nowMS, wroteOutbox)
		return err
	})
	return inParty, err
}

// PlayerDevicesInLiveParty returns the set of device IDs currently joined to a
// live party session.
func (s *Store) PlayerDevicesInLiveParty(ctx context.Context) (map[DeviceID]bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	nowMS := timeToMillis(s.serverNow())
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT s.screen_id, s.profile_id, s.auth_session_id, s.device_id, s.binding_generation, p.host_profile_id
		FROM party_screen s
		JOIN party_session p ON p.session_id = s.session_id
		WHERE s.device_id <> '' AND s.dropped_at_ms IS NULL
			AND p.ended_at_ms IS NULL AND p.expires_at_ms > ?
	`, nowMS)
	if err != nil {
		return nil, err
	}
	out := map[DeviceID]bool{}
	for rows.Next() {
		var row partyScreenRow
		var authSess, devID string
		var hostProfileID ProfileID
		if err := rows.Scan(&row.id, &row.profileID, &authSess, &devID, &row.bindingGeneration, &hostProfileID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		row.authSessionID = SessionID(authSess)
		row.deviceID = DeviceID(devID)
		if valid, _ := s.isPartyPlayerScreenValidLocked(ctx, hostProfileID, row); valid {
			out[row.deviceID] = true
		}
	}
	return out, closeRows(rows)
}

func (s *Store) playerDeviceInLivePartyTx(ctx context.Context, tx *sql.Tx, deviceID DeviceID, authSessionID SessionID, nowMS int64, wroteOutbox *bool) (bool, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT s.screen_id, s.session_id, s.profile_id, s.auth_session_id, s.device_id, s.binding_generation, p.host_profile_id
		FROM party_screen s
		JOIN party_session p ON p.session_id = s.session_id
		WHERE (s.device_id = ? OR (? <> '' AND s.auth_session_id = ?))
			AND s.dropped_at_ms IS NULL AND p.ended_at_ms IS NULL AND p.expires_at_ms > ?
	`, deviceID, authSessionID, authSessionID, nowMS)
	if err != nil {
		return false, err
	}
	type candidate struct {
		screen        partyScreenRow
		partyID       string
		hostProfileID ProfileID
	}
	var items []candidate
	for rows.Next() {
		var it candidate
		var authSess, devID string
		if err := rows.Scan(&it.screen.id, &it.partyID, &it.screen.profileID, &authSess, &devID, &it.screen.bindingGeneration, &it.hostProfileID); err != nil {
			_ = rows.Close()
			return false, err
		}
		it.screen.authSessionID = SessionID(authSess)
		it.screen.deviceID = DeviceID(devID)
		items = append(items, it)
	}
	if err := closeRows(rows); err != nil {
		return false, err
	}
	inParty := false
	for _, it := range items {
		valid, reason := s.isPartyPlayerScreenValidLocked(ctx, it.hostProfileID, it.screen)
		if valid {
			inParty = true
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE party_screen SET dropped_at_ms = ? WHERE screen_id = ? AND dropped_at_ms IS NULL`, nowMS, it.screen.id); err != nil {
			return false, err
		}
		if reason == "controller-removed" {
			if err := appendPartyStageEventSQL(ctx, tx, PartyStageEventPayload{
				PartySessionID: it.partyID,
				ScreenID:       it.screen.id,
				AuthSessionID:  it.screen.authSessionID,
				ProfileID:      it.screen.profileID,
				HostProfileID:  it.hostProfileID,
				DeviceID:       it.screen.deviceID,
				Action:         PartyStageActionReleased,
				Reason:         reason,
			}, nowMS); err != nil {
				return false, err
			}
			*wroteOutbox = true
		}
	}
	return inParty, nil
}

// PartyActiveScreens returns the active (non-dropped) screen rows for
// partySessionID so the graph layer can resolve auth-backed playback filters.
func (s *Store) PartyActiveScreens(ctx context.Context, partySessionID string) ([]PartyScreenView, error) {
	if partySessionID == "" {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT screen_id, profile_id, auth_session_id, device_id, binding_generation, chosen_lead
		FROM party_screen
		WHERE session_id = ? AND dropped_at_ms IS NULL
	`, partySessionID)
	if err != nil {
		return nil, err
	}
	var out []PartyScreenView
	for rows.Next() {
		var v PartyScreenView
		var authSess, devID string
		var chosenLead int
		if err := rows.Scan(&v.ID, &v.ProfileID, &authSess, &devID, &v.BindingGeneration, &chosenLead); err != nil {
			_ = rows.Close()
			return nil, err
		}
		v.AuthSessionID = SessionID(authSess)
		v.DeviceID = DeviceID(devID)
		v.ChosenLead = chosenLead == 1
		out = append(out, v)
	}
	return out, closeRows(rows)
}

func (s *Store) dropPartyScreensForStopTx(ctx context.Context, tx *sql.Tx, deviceID DeviceID, target SessionID, nowMS int64, wroteOutbox *bool) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT s.screen_id, s.session_id, s.profile_id, s.auth_session_id, s.device_id, p.host_profile_id
		FROM party_screen s
		JOIN party_session p ON p.session_id = s.session_id
		WHERE ((? <> '' AND s.device_id = ?) OR s.auth_session_id = ?) AND s.dropped_at_ms IS NULL
	`, deviceID, deviceID, target)
	if err != nil {
		return err
	}
	var payloads []PartyStageEventPayload
	for rows.Next() {
		var p PartyStageEventPayload
		if err := rows.Scan(&p.ScreenID, &p.PartySessionID, &p.ProfileID, &p.AuthSessionID, &p.DeviceID, &p.HostProfileID); err != nil {
			_ = rows.Close()
			return err
		}
		p.Action = PartyStageActionReleased
		p.Reason = "stopped"
		payloads = append(payloads, p)
	}
	if err := closeRows(rows); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE party_screen SET dropped_at_ms = ?
		WHERE ((? <> '' AND device_id = ?) OR auth_session_id = ?) AND dropped_at_ms IS NULL
	`, nowMS, deviceID, deviceID, target); err != nil {
		return err
	}
	for _, p := range payloads {
		if err := appendPartyStageEventSQL(ctx, tx, p, nowMS); err != nil {
			return err
		}
		*wroteOutbox = true
	}
	return nil
}
