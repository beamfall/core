package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"time"
)

// Play history for the Console dashboard and My Data. A play is derived, not
// stored: it is one run of playback-progress events for a Profile, Media and
// stream with no gap longer than playGap between consecutive events, lasting
// at least playMinSpan (see derivedPlaysLocked). Nothing new is persisted — the
// events are the ones the engagement log already keeps, each carrying the
// reporting Device, its stream and the play method the server chose for it
// (CORE-1703).

const (
	// playGap is the silence that ends one play and starts the next.
	playGap = 30 * time.Minute
	// PlayHistoryMaxLimit bounds one page of play history.
	PlayHistoryMaxLimit = 500
	// playStatsRanks is how many profiles and items a ranking returns.
	playStatsRanks = 10
	// playMinSpan is the shortest run that counts as a play (CORE-2316): a
	// report or two from opening and closing an item is not a play. Items
	// shorter than four times this need a quarter of their length instead.
	playMinSpan = 30 * time.Second
	// playFinishedFraction and playFinishedTail are how near the end a play
	// must reach to be Finished: this fraction of the item, or within the tail.
	playFinishedFraction = 0.8
	playFinishedTail     = 30
)

// Play methods a play reports. A play's method is the one that covered most of
// its watched time; reports written before CORE-1703 recorded none.
const (
	PlayMethodDirect    = "direct"
	PlayMethodRemux     = "remux"
	PlayMethodTranscode = "transcode"
	PlayMethodUnknown   = "unknown"
)

// playMethods orders the methods by rank: an index is the SQL rank below.
var playMethods = []string{PlayMethodUnknown, PlayMethodDirect, PlayMethodRemux, PlayMethodTranscode}

// ValidPlayMethod reports whether method names a play method.
func ValidPlayMethod(method string) bool {
	return playMethodRank(method) >= 0
}

func playMethodRank(method string) int {
	for rank, m := range playMethods {
		if m == method {
			return rank
		}
	}
	return -1
}

// playMethodRankSQL ranks a report's recorded method (an index into
// playMethods).
const playMethodRankSQL = `CASE json_extract(payload, '$.playMethod')
			WHEN 'transcode' THEN 3 WHEN 'remux' THEN 2 WHEN 'direct' THEN 1 ELSE 0 END`

// PlayFilter narrows which plays a history or stats read covers. Zero fields
// do not filter. VisibleOnly drops plays of Media the caller cannot see, as a
// viewer's own reads do; without it such plays still count, untitled.
type PlayFilter struct {
	ProfileID   ProfileID
	LibraryID   LibraryID
	MediaID     MediaID
	DeviceID    DeviceID
	Method      string
	VisibleOnly bool
}

// needsVisibility reports whether every play must be resolved against the
// caller's visibility before it is kept.
func (f PlayFilter) needsVisibility() bool {
	return f.LibraryID != "" || f.VisibleOnly
}

// keeps reports whether a play of media passes the visibility half of f;
// media is zero when the caller cannot see it.
func (f PlayFilter) keeps(media Media) bool {
	if f.VisibleOnly && media.ID == "" {
		return false
	}
	return f.LibraryID == "" || media.LibraryID == f.LibraryID
}

// PlayHistoryQuery narrows the play history. Zero fields do not filter.
type PlayHistoryQuery struct {
	PlayFilter
	Since time.Time
	Until time.Time
	Limit int
}

// PlayStatsQuery selects the plays a stats read aggregates. Since is
// required. UTCOffset places the day buckets in the caller's local days.
type PlayStatsQuery struct {
	PlayFilter
	Since     time.Time
	Until     time.Time
	UTCOffset time.Duration
}

// Play is one derived play. Title, LibraryID and LibraryTitle are empty when
// the Media is outside the caller's visibility; the play is still reported so
// activity is never understated.
type Play struct {
	ProfileID    ProfileID
	ProfileName  string
	MediaID      MediaID
	Title        string
	LibraryID    LibraryID
	LibraryTitle string
	// DeviceID is the reporting Device, empty for plays recorded before
	// devices were; DeviceName and DeviceClient (its surface) are empty when
	// the Device is unknown or gone.
	DeviceID       DeviceID
	DeviceName     string
	DeviceClient   string
	Method         string
	StartedAt      time.Time
	WatchedSeconds int
	Finished       bool
}

// PlayStats totals the plays over a range and breaks them down: who played
// most, what was played most, on which devices, by which method and per day.
type PlayStats struct {
	Plays          int
	WatchedSeconds int
	Profiles       []PlayStatsProfile
	Items          []PlayStatsItem
	Devices        []PlayStatsDevice
	Methods        []PlayStatsMethod
	Days           []PlayStatsDay
}

// PlayStatsDevice is one Device's activity in the range.
type PlayStatsDevice struct {
	DeviceID       DeviceID
	DeviceName     string
	DeviceClient   string
	Plays          int
	WatchedSeconds int
}

// PlayStatsMethod is one play method's activity in the range.
type PlayStatsMethod struct {
	Method         string
	Plays          int
	WatchedSeconds int
}

// PlayStatsDay is one local day's activity; Day is YYYY-MM-DD. Days without
// plays are omitted.
type PlayStatsDay struct {
	Day            string
	Plays          int
	WatchedSeconds int
}

// PlayStatsProfile is one Profile's activity in the window.
type PlayStatsProfile struct {
	ProfileID      ProfileID
	ProfileName    string
	Plays          int
	WatchedSeconds int
}

// PlayStatsItem is one visible Media's activity in the window.
type PlayStatsItem struct {
	MediaID        MediaID
	Title          string
	LibraryID      LibraryID
	Plays          int
	WatchedSeconds int
}

type derivedPlay struct {
	profileID ProfileID
	mediaID   MediaID
	deviceID  DeviceID
	method    int // index into playMethods
	startMS   int64
	endMS     int64
	duration  int
	finished  bool
	// firstPos and lastPos are the lowest and highest position the run's
	// unwatched reports carried. A watched report stores position zero, so
	// only unwatched ones say how far playback got.
	firstPos int
	lastPos  int
	// playedMS is how long the run actually played; see playedStepMS.
	playedMS int64
}

// counts reports whether the run lasted long enough to be a play.
func (p derivedPlay) counts() bool {
	minSpan := playMinSpan
	if p.duration > 0 {
		minSpan = min(minSpan, time.Duration(p.duration)*time.Second/4)
	}
	return time.Duration(p.endMS-p.startMS)*time.Millisecond >= minSpan
}

// reachedEnd reports whether the run got near the end of the item: its
// highest reported position, or where it started plus how long it ran, is
// within playFinishedFraction or playFinishedTail of the end. An item of
// unknown length cannot be judged, so the watched flag stands alone.
func (p derivedPlay) reachedEnd() bool {
	if p.duration <= 0 {
		return true
	}
	reached := max(p.lastPos, p.firstPos+int((p.endMS-p.startMS)/1000))
	return float64(reached) >= playFinishedFraction*float64(p.duration) || p.duration-reached <= playFinishedTail
}

// watchedSeconds is how long the play actually played, never longer than the
// item itself (see playedStepMS).
func (p derivedPlay) watchedSeconds() int {
	seconds := int(p.playedMS / 1000)
	if p.duration > 0 && seconds > p.duration {
		return p.duration
	}
	return seconds
}

// playedAtMS is when a progress report was observed: the client's own
// epoch-millisecond event time when it sent one, so a report held back while a
// device was offline stays in the play it belongs to, and the server arrival
// time otherwise. A client clock ahead of the server is not believed, so no
// play is ever dated in the future. It dates plays and orders reports that
// name no stream; it is never compared with a server time.
const playedAtMS = `CASE
		WHEN COALESCE(json_extract(payload, '$.eventTimeMs'), 0) BETWEEN 1 AND occurred_at_ms
		THEN json_extract(payload, '$.eventTimeMs')
		ELSE occurred_at_ms END`

// playStreamKey is the stream a native report belongs to: the reporting auth
// session, which the held playback decision and stream activity are keyed by
// too. Reports carry no stream ID of their own, and the session ID is a
// credential, so the event keeps only a hash of it. Empty without a session.
func playStreamKey(sessionID SessionID) string {
	if sessionID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("play-stream\x00" + string(sessionID)))
	return hex.EncodeToString(sum[:12])
}

// playedStepMS is how much one report adds to its run's watched time: the
// span from the report before it in the same run.
//
// A report that records the client's position (every native report since
// CORE-1703) adds the position's advance, capped at the time between the two
// arrivals at the server, and nothing when the earlier report said the player
// was paused. A pause earns nothing whether the client stays silent or keeps
// reporting, a seek made while paused earns nothing, a forward seek earns no
// more than the time it took, and a report the client held back while another
// was in flight earns its real advance. A client that does not say whether it
// was paused reads as playing.
//
// Older reports keep the CORE-1307 rule: the gap between their observed times,
// but no more than the stored position advanced when both sides have one. The
// first report of a run adds nothing.
const playedStepMS = `CASE
		WHEN starts = 1 THEN 0
		WHEN reported IS NOT NULL AND prev_reported IS NOT NULL
		THEN CASE WHEN prev_paused = 1 THEN 0
		          ELSE MAX(MIN(recv_ms - prev_recv, (reported - prev_reported) * 1000), 0) END
		WHEN watched = 0 AND prev_watched = 0 AND (position > 0 OR prev_position > 0)
		THEN MIN(at_ms - prev_ms, MAX(position - prev_position, 0) * 1000)
		ELSE at_ms - prev_ms END`

// derivedPlaysLocked lists the plays that started in [since, until), newest
// first, each measured whole even when it runs past either bound. A zero bound
// is open. The Profile, Media, Device and method parts of f apply here; the
// Library and visibility parts are the caller's.
//
// A run is one stream's reports for a Profile and Media: the stream key when a
// report has one, its Device otherwise. Streams never interleave into each
// other, so two devices playing at once are two plays. A stream's reports are
// taken in the order the server received them, or by observed time when they
// name no stream, and a run ends at a silence longer than playGap, or where
// another stream of the same Profile and Media played a whole run of at least
// playMinSpan inside the silence: TV, then phone, then TV again is three
// plays. That holds exactly when the latest-starting such run to end before
// a report started after the report before it (no run of the report's own
// stream can), which one ordered pass finds.
//
// A play's method is the one that covered most of its watched time, each
// step credited to the method recorded on the report it starts from (ties go
// to the more demanding method). A play with no credited time under a known
// method reports the most demanding one recorded.
//
// Runs are grouped before the window applies: a play that began just before
// since is left out, not cut into a shorter play starting at since. Reports
// from one gap before since are enough to tell, because a run starting at or
// after since has no report within one gap before its first.
func (s *Store) derivedPlaysLocked(ctx context.Context, f PlayFilter, since, until time.Time, visit func(derivedPlay) bool) error {
	query := `
		WITH src AS (
			SELECT profile_id, media_id, ` + playDeviceSQL + ` AS device_id,
			       COALESCE(json_extract(payload, '$.streamKey'), '') AS stream,
			       ` + playMethodRankSQL + ` AS method,
			       ` + playedAtMS + ` AS at_ms,
			       occurred_at_ms AS recv_ms, rowid AS seq,
			       COALESCE(json_extract(payload, '$.durationSeconds'), 0) AS duration,
			       COALESCE(json_extract(payload, '$.watched'), 0) AS watched,
			       COALESCE(json_extract(payload, '$.positionSeconds'), 0) AS position,
			       json_extract(payload, '$.reportedPositionSeconds') AS reported,
			       json_extract(payload, '$.paused') AS paused
			FROM engagement_event
			WHERE kind = ? AND media_id IS NOT NULL
			  AND json_extract(payload, '$.operation') = ?`
	args := []any{playbackProjectionEventKind, playbackProjectionOperationProgress}
	if f.ProfileID != "" {
		query += ` AND profile_id = ?`
		args = append(args, f.ProfileID)
	}
	if f.MediaID != "" {
		query += ` AND media_id = ?`
		args = append(args, f.MediaID)
	}
	if !since.IsZero() {
		// A report is never dated after it arrived, so the arrival index can
		// still bound the scan, one gap early.
		query += ` AND occurred_at_ms >= ?`
		args = append(args, timeToMillis(since.UTC().Add(-playGap)))
	}
	query += `
		), raw AS (
			SELECT *, CASE WHEN stream <> '' THEN 's' || stream ELSE 'd' || device_id END AS part,
			       CASE WHEN stream <> '' THEN recv_ms ELSE at_ms END AS ord_ms
			FROM src
		), ev AS (
			SELECT *, LAG(ord_ms) OVER w AS prev_ord, LAG(at_ms) OVER w AS prev_ms,
			       LAG(recv_ms) OVER w AS prev_recv, LAG(position) OVER w AS prev_position,
			       LAG(watched) OVER w AS prev_watched, LAG(reported) OVER w AS prev_reported,
			       LAG(paused) OVER w AS prev_paused, LAG(method) OVER w AS prev_method
			FROM raw
			WINDOW w AS (PARTITION BY profile_id, media_id, part ORDER BY ord_ms, seq)
		), gap AS (
			SELECT *, SUM(CASE WHEN prev_ord IS NULL OR ord_ms - prev_ord > ? THEN 1 ELSE 0 END)
			            OVER (PARTITION BY profile_id, media_id, part ORDER BY ord_ms, seq
			                  ROWS UNBOUNDED PRECEDING) AS gap_no
			FROM ev
		), other AS (
			SELECT profile_id, media_id, MIN(ord_ms) AS first_ms, MAX(ord_ms) AS last_ms
			FROM gap
			GROUP BY profile_id, media_id, part, gap_no
			HAVING MAX(ord_ms) - MIN(ord_ms) >= ?
		), inside AS (
			SELECT seq, marker, MAX(first_ms) OVER (PARTITION BY profile_id, media_id ORDER BY at, marker
			                                        ROWS UNBOUNDED PRECEDING) AS latest_first
			FROM (SELECT profile_id, media_id, seq, ord_ms AS at, 0 AS marker, NULL AS first_ms FROM gap
			      UNION ALL
			      SELECT profile_id, media_id, NULL, last_ms, 1, first_ms FROM other)
		), mark AS (
			SELECT gap.*, CASE
			       WHEN prev_ord IS NULL OR ord_ms - prev_ord > ? THEN 1
			       WHEN inside.latest_first > prev_ord THEN 1
			       ELSE 0 END AS starts
			FROM gap JOIN inside ON inside.seq = gap.seq AND inside.marker = 0
		), run AS (
			SELECT *, SUM(starts) OVER (PARTITION BY profile_id, media_id, part ORDER BY ord_ms, seq
			                            ROWS UNBOUNDED PRECEDING) AS run_no
			FROM mark
		), step AS (
			SELECT *, ` + playedStepMS + ` AS step_ms
			FROM run
		), credit AS (
			SELECT profile_id, media_id, part, run_no, prev_method AS method,
			       ROW_NUMBER() OVER (PARTITION BY profile_id, media_id, part, run_no
			                          ORDER BY SUM(step_ms) DESC, prev_method DESC) AS pick
			FROM step
			WHERE starts = 0 AND prev_method > 0 AND step_ms > 0
			GROUP BY profile_id, media_id, part, run_no, prev_method
		), play AS (
			SELECT profile_id, media_id, part, run_no, MAX(device_id) AS device_id, MAX(method) AS top_method,
			       MIN(at_ms) AS start_ms, MAX(at_ms) AS end_ms, MAX(duration) AS duration, MAX(watched) AS watched,
			       COALESCE(MIN(CASE WHEN watched = 0 THEN position END), 0) AS first_pos,
			       COALESCE(MAX(CASE WHEN watched = 0 THEN position END), 0) AS last_pos,
			       SUM(step_ms) AS played_ms
			FROM step
			GROUP BY profile_id, media_id, part, run_no
		), result AS (
			SELECT play.*, COALESCE(credit.method, play.top_method) AS play_method
			FROM play LEFT JOIN credit
			  ON credit.profile_id = play.profile_id AND credit.media_id = play.media_id
			 AND credit.part = play.part AND credit.run_no = play.run_no AND credit.pick = 1
		)
		SELECT profile_id, media_id, device_id, play_method, start_ms, end_ms, duration, watched,
		       first_pos, last_pos, played_ms
		FROM result
		WHERE 1 = 1`
	args = append(args, playGap.Milliseconds(), playMinSpan.Milliseconds(), playGap.Milliseconds())
	if f.DeviceID != "" {
		// After grouping: dropping other Devices' reports first would join
		// this Device's runs across a switch away and back.
		query += ` AND device_id = ?`
		args = append(args, f.DeviceID)
	}
	if f.Method != "" {
		query += ` AND play_method = ?`
		args = append(args, max(playMethodRank(f.Method), 0))
	}
	if !since.IsZero() {
		query += ` AND start_ms >= ?`
		args = append(args, timeToMillis(since.UTC()))
	}
	if !until.IsZero() {
		query += ` AND start_ms < ?`
		args = append(args, timeToMillis(until.UTC()))
	}
	query += `
		ORDER BY start_ms DESC, profile_id, media_id, device_id`
	rows, err := s.eventsR.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var p derivedPlay
		var finished int
		if err := rows.Scan(&p.profileID, &p.mediaID, &p.deviceID, &p.method, &p.startMS, &p.endMS, &p.duration, &finished, &p.firstPos, &p.lastPos, &p.playedMS); err != nil {
			return err
		}
		if !p.counts() {
			continue
		}
		// The watched flag alone is not enough (CORE-2316): a client can
		// report an item finished moments after opening it.
		p.finished = finished != 0 && p.reachedEnd()
		if !visit(p) {
			break
		}
	}
	return rows.Err()
}

// playDeviceSQL is a report's Device; reports recorded before CORE-1703 have
// none and group together.
const playDeviceSQL = `COALESCE(json_extract(payload, '$.deviceId'), '')`

// playDevice is how a play names its Device.
type playDevice struct {
	name   string
	client string
}

func (s *Store) playDevicesLocked(ctx context.Context) (map[DeviceID]playDevice, error) {
	rows, err := s.catalogR.QueryContext(ctx, `SELECT id, name, device_surface FROM device`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	devices := map[DeviceID]playDevice{}
	for rows.Next() {
		var id DeviceID
		var device playDevice
		if err := rows.Scan(&id, &device.name, &device.client); err != nil {
			return nil, err
		}
		devices[id] = device
	}
	return devices, rows.Err()
}

func (s *Store) profileNamesLocked(ctx context.Context) (map[ProfileID]string, error) {
	rows, err := s.catalogR.QueryContext(ctx, `SELECT id, name FROM profile`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[ProfileID]string{}
	for rows.Next() {
		var id ProfileID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		names[id] = name
	}
	return names, rows.Err()
}

// playHistoryReach is how far back an open-ended history read looks before it
// widens. Progress events are kept indefinitely, so reading the newest plays
// must not sessionize the whole log when recent activity already fills a page.
var playHistoryReach = []time.Duration{30 * 24 * time.Hour, 365 * 24 * time.Hour}

// PlayHistory lists plays newest first. Media titles resolve through filter,
// the caller's own visibility; filtering by Library keeps only plays whose
// Media the caller can see in that Library.
func (s *Store) PlayHistory(ctx context.Context, filter LibraryFilter, q PlayHistoryQuery) ([]Play, error) {
	limit := q.Limit
	if limit <= 0 || limit > PlayHistoryMaxLimit {
		limit = PlayHistoryMaxLimit
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	if q.Since.IsZero() {
		end := q.Until
		if end.IsZero() {
			end = s.serverNow()
		}
		for _, reach := range playHistoryReach {
			bound := end.Add(-reach)
			plays, err := s.playHistoryLocked(ctx, filter, q, bound, limit)
			if err != nil {
				return nil, err
			}
			// Every play on a bounded page started after the bound and is
			// measured whole, and every play the bound left out started
			// earlier, so a full page is exactly the page an unbounded read
			// returns.
			if len(plays) == limit {
				return plays, nil
			}
		}
	}
	return s.playHistoryLocked(ctx, filter, q, q.Since, limit)
}

// playHistoryChunk is how many plays a visibility-filtered read resolves
// against the catalog at a time.
const playHistoryChunk = PlayHistoryMaxLimit

// playHistoryLocked resolves up to limit plays no older than since.
func (s *Store) playHistoryLocked(ctx context.Context, filter LibraryFilter, q PlayHistoryQuery, since time.Time, limit int) ([]Play, error) {
	// Visibility lives in the catalog database and plays in the events
	// database. Without a Library or visibility filter the newest limit plays
	// are the page. With one, plays are resolved a chunk at a time and kept
	// only when they pass it, until the page fills or the window runs out, so
	// a quiet Library is never crowded out by a busy one.
	var raw, pending []derivedPlay
	visible := map[MediaID]Media{}
	var resolveErr error
	resolve := func() bool {
		if err := s.resolvePlayMediaLocked(ctx, filter, pending, visible); err != nil {
			resolveErr = err
			return false
		}
		for _, p := range pending {
			if len(raw) == limit {
				break
			}
			if q.keeps(visible[p.mediaID]) {
				raw = append(raw, p)
			}
		}
		pending = pending[:0]
		return len(raw) < limit
	}
	err := s.derivedPlaysLocked(ctx, q.PlayFilter, since, q.Until, func(p derivedPlay) bool {
		pending = append(pending, p)
		if !q.needsVisibility() {
			return len(pending) < limit
		}
		if len(pending) < playHistoryChunk {
			return true
		}
		return resolve()
	})
	if err != nil {
		return nil, err
	}
	if resolveErr == nil && len(pending) > 0 {
		resolve()
	}
	if resolveErr != nil {
		return nil, resolveErr
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	libraryTitles := make(map[LibraryID]string, len(libraries))
	for _, library := range libraries {
		libraryTitles[library.ID] = library.Title
	}
	names, err := s.profileNamesLocked(ctx)
	if err != nil {
		return nil, err
	}
	devices, err := s.playDevicesLocked(ctx)
	if err != nil {
		return nil, err
	}
	plays := make([]Play, 0, len(raw))
	for _, p := range raw {
		media := visible[p.mediaID]
		device := devices[p.deviceID]
		play := Play{
			ProfileID:      p.profileID,
			ProfileName:    names[p.profileID],
			MediaID:        p.mediaID,
			DeviceID:       p.deviceID,
			DeviceName:     device.name,
			DeviceClient:   device.client,
			Method:         playMethods[p.method],
			StartedAt:      millisToTime(p.startMS),
			WatchedSeconds: p.watchedSeconds(),
			Finished:       p.finished,
		}
		if media.ID != "" {
			play.Title = media.Title
			play.LibraryID = media.LibraryID
			play.LibraryTitle = libraryTitles[media.LibraryID]
		}
		plays = append(plays, play)
	}
	return plays, nil
}

// resolvePlayMediaLocked adds the plays' Media not yet in visible, recording a
// zero Media for each one the caller cannot see.
func (s *Store) resolvePlayMediaLocked(ctx context.Context, filter LibraryFilter, plays []derivedPlay, visible map[MediaID]Media) error {
	ids := make([]MediaID, 0, len(plays))
	for _, p := range plays {
		if _, done := visible[p.mediaID]; !done {
			visible[p.mediaID] = Media{}
			ids = append(ids, p.mediaID)
		}
	}
	found, err := s.visibleMediaBatchSQL(ctx, ids, filter)
	if err != nil {
		return err
	}
	for id, media := range found {
		visible[id] = media
	}
	return nil
}

// PlayStats totals and breaks down the plays that started in the query's
// range. Every play that passes the query counts toward the totals, its
// Profile, Device, method and day; only Media visible through filter are
// ranked as items.
func (s *Store) PlayStats(ctx context.Context, filter LibraryFilter, q PlayStatsQuery) (PlayStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PlayStats{}, err
	}
	var plays []derivedPlay
	if err := s.derivedPlaysLocked(ctx, q.PlayFilter, q.Since, q.Until, func(p derivedPlay) bool {
		plays = append(plays, p)
		return true
	}); err != nil {
		return PlayStats{}, err
	}
	visible := map[MediaID]Media{}
	for start := 0; start < len(plays); start += playHistoryChunk {
		if err := s.resolvePlayMediaLocked(ctx, filter, plays[start:min(start+playHistoryChunk, len(plays))], visible); err != nil {
			return PlayStats{}, err
		}
	}
	names, err := s.profileNamesLocked(ctx)
	if err != nil {
		return PlayStats{}, err
	}
	devices, err := s.playDevicesLocked(ctx)
	if err != nil {
		return PlayStats{}, err
	}
	zone := time.FixedZone("", int(q.UTCOffset/time.Second))
	var stats PlayStats
	profiles := map[ProfileID]*PlayStatsProfile{}
	items := map[MediaID]*PlayStatsItem{}
	byDevice := map[DeviceID]*PlayStatsDevice{}
	methods := map[string]*PlayStatsMethod{}
	days := map[string]*PlayStatsDay{}
	for _, p := range plays {
		media := visible[p.mediaID]
		if !q.keeps(media) {
			continue
		}
		watched := p.watchedSeconds()
		stats.Plays++
		stats.WatchedSeconds += watched
		profile := profiles[p.profileID]
		if profile == nil {
			profile = &PlayStatsProfile{ProfileID: p.profileID, ProfileName: names[p.profileID]}
			profiles[p.profileID] = profile
		}
		profile.Plays++
		profile.WatchedSeconds += watched
		if media.ID != "" {
			item := items[p.mediaID]
			if item == nil {
				item = &PlayStatsItem{MediaID: p.mediaID, Title: media.Title, LibraryID: media.LibraryID}
				items[p.mediaID] = item
			}
			item.Plays++
			item.WatchedSeconds += watched
		}
		device := byDevice[p.deviceID]
		if device == nil {
			known := devices[p.deviceID]
			device = &PlayStatsDevice{DeviceID: p.deviceID, DeviceName: known.name, DeviceClient: known.client}
			byDevice[p.deviceID] = device
		}
		device.Plays++
		device.WatchedSeconds += watched
		method := methods[playMethods[p.method]]
		if method == nil {
			method = &PlayStatsMethod{Method: playMethods[p.method]}
			methods[method.Method] = method
		}
		method.Plays++
		method.WatchedSeconds += watched
		key := millisToTime(p.startMS).In(zone).Format(time.DateOnly)
		day := days[key]
		if day == nil {
			day = &PlayStatsDay{Day: key}
			days[key] = day
		}
		day.Plays++
		day.WatchedSeconds += watched
	}
	stats.Profiles = rankPlays(profiles, func(p PlayStatsProfile) (int, int, string) {
		return p.Plays, p.WatchedSeconds, string(p.ProfileID)
	})
	stats.Items = rankPlays(items, func(i PlayStatsItem) (int, int, string) {
		return i.Plays, i.WatchedSeconds, string(i.MediaID)
	})
	stats.Devices = sortPlays(byDevice, func(d PlayStatsDevice) (int, int, string) {
		return d.Plays, d.WatchedSeconds, string(d.DeviceID)
	})
	stats.Methods = sortPlays(methods, func(m PlayStatsMethod) (int, int, string) {
		return m.Plays, m.WatchedSeconds, m.Method
	})
	stats.Days = make([]PlayStatsDay, 0, len(days))
	for _, day := range days {
		stats.Days = append(stats.Days, *day)
	}
	sort.Slice(stats.Days, func(i, j int) bool { return stats.Days[i].Day < stats.Days[j].Day })
	return stats, nil
}

// rankPlays orders entries by plays, then watched seconds, then id, and keeps
// the top playStatsRanks.
func rankPlays[K comparable, V any](entries map[K]*V, key func(V) (plays, watched int, id string)) []V {
	ranked := sortPlays(entries, key)
	return ranked[:min(len(ranked), playStatsRanks)]
}

// sortPlays orders every entry the way rankPlays does, keeping them all: the
// Device and method breakdowns add up to the totals.
func sortPlays[K comparable, V any](entries map[K]*V, key func(V) (plays, watched int, id string)) []V {
	ranked := make([]V, 0, len(entries))
	for _, entry := range entries {
		ranked = append(ranked, *entry)
	}
	sort.Slice(ranked, func(i, j int) bool {
		ap, aw, aid := key(ranked[i])
		bp, bw, bid := key(ranked[j])
		if ap != bp {
			return ap > bp
		}
		if aw != bw {
			return aw > bw
		}
		return aid < bid
	})
	return ranked
}
