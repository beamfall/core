package store

import (
	"context"
	"sort"
	"time"
)

// Play history for the Console dashboard. A play is derived, not stored: it is
// one run of playback-progress events for a Profile and Media with no gap
// longer than playGap between consecutive events. Nothing new is persisted —
// the events are the ones the engagement log already keeps.

const (
	// playGap is the silence that ends one play and starts the next.
	playGap = 30 * time.Minute
	// PlayHistoryMaxLimit bounds one page of play history.
	PlayHistoryMaxLimit = 500
	// playStatsRanks is how many profiles and items a ranking returns.
	playStatsRanks = 10
)

// PlayHistoryQuery narrows the play history. Zero fields do not filter.
type PlayHistoryQuery struct {
	ProfileID ProfileID
	LibraryID LibraryID
	Since     time.Time
	Until     time.Time
	Limit     int
}

// Play is one derived play. Title, LibraryID and LibraryTitle are empty when
// the Media is outside the caller's visibility; the play is still reported so
// activity is never understated.
type Play struct {
	ProfileID      ProfileID
	ProfileName    string
	MediaID        MediaID
	Title          string
	LibraryID      LibraryID
	LibraryTitle   string
	StartedAt      time.Time
	WatchedSeconds int
	Finished       bool
}

// PlayStats ranks who played most and what was played most over a window.
type PlayStats struct {
	Profiles []PlayStatsProfile
	Items    []PlayStatsItem
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
	startMS   int64
	endMS     int64
	duration  int
	finished  bool
}

// watchedSeconds is the wall-clock span of the play, never longer than the
// item itself. Position deltas are not used: a finished play reports position
// zero, and a seek would count as watching.
func (p derivedPlay) watchedSeconds() int {
	seconds := int((p.endMS - p.startMS) / 1000)
	if p.duration > 0 && seconds > p.duration {
		return p.duration
	}
	return seconds
}

// playedAtMS is when a progress report was observed: the client's own
// epoch-millisecond event time when it sent one, so a report held back while a
// device was offline stays in the play it belongs to, and the server arrival
// time otherwise. A client clock ahead of the server is not believed, so no
// play is ever dated in the future.
const playedAtMS = `CASE
		WHEN COALESCE(json_extract(payload, '$.eventTimeMs'), 0) BETWEEN 1 AND occurred_at_ms
		THEN json_extract(payload, '$.eventTimeMs')
		ELSE occurred_at_ms END`

// derivedPlaysLocked lists the plays that started in [since, until), newest
// first, each measured whole even when it runs past either bound. A zero bound
// is open; an empty profileID covers every Profile.
//
// Runs are grouped before the window applies: a play that began just before
// since is left out, not cut into a shorter play starting at since. Reports
// from one gap before since are enough to tell, because a run starting at or
// after since has no report within one gap before its first.
func (s *Store) derivedPlaysLocked(ctx context.Context, profileID ProfileID, since, until time.Time, visit func(derivedPlay) bool) error {
	query := `
		WITH raw AS (
			SELECT profile_id, media_id, ` + playedAtMS + ` AS at_ms,
			       COALESCE(json_extract(payload, '$.durationSeconds'), 0) AS duration,
			       COALESCE(json_extract(payload, '$.watched'), 0) AS watched
			FROM engagement_event
			WHERE kind = ? AND media_id IS NOT NULL
			  AND json_extract(payload, '$.operation') = ?`
	args := []any{playbackProjectionEventKind, playbackProjectionOperationProgress}
	if profileID != "" {
		query += ` AND profile_id = ?`
		args = append(args, profileID)
	}
	if !since.IsZero() {
		// A report is never dated after it arrived, so the arrival index can
		// still bound the scan, one gap early.
		query += ` AND occurred_at_ms >= ?`
		args = append(args, timeToMillis(since.UTC().Add(-playGap)))
	}
	query += `
		), ev AS (
			SELECT *, LAG(at_ms) OVER (PARTITION BY profile_id, media_id ORDER BY at_ms) AS prev_ms
			FROM raw
		), run AS (
			SELECT *, SUM(CASE WHEN prev_ms IS NULL OR at_ms - prev_ms > ? THEN 1 ELSE 0 END)
			            OVER (PARTITION BY profile_id, media_id ORDER BY at_ms) AS run_no
			FROM ev
		)
		SELECT profile_id, media_id, MIN(at_ms), MAX(at_ms), MAX(duration), MAX(watched)
		FROM run
		GROUP BY profile_id, media_id, run_no
		HAVING 1 = 1`
	args = append(args, playGap.Milliseconds())
	if !since.IsZero() {
		query += ` AND MIN(at_ms) >= ?`
		args = append(args, timeToMillis(since.UTC()))
	}
	if !until.IsZero() {
		query += ` AND MIN(at_ms) < ?`
		args = append(args, timeToMillis(until.UTC()))
	}
	query += `
		ORDER BY MIN(at_ms) DESC, profile_id, media_id`
	rows, err := s.eventsR.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var p derivedPlay
		var finished int
		if err := rows.Scan(&p.profileID, &p.mediaID, &p.startMS, &p.endMS, &p.duration, &finished); err != nil {
			return err
		}
		p.finished = finished != 0
		if !visit(p) {
			break
		}
	}
	return rows.Err()
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

// playHistoryChunk is how many plays a Library-filtered read resolves against
// the catalog at a time.
const playHistoryChunk = PlayHistoryMaxLimit

// playHistoryLocked resolves up to limit plays no older than since.
func (s *Store) playHistoryLocked(ctx context.Context, filter LibraryFilter, q PlayHistoryQuery, since time.Time, limit int) ([]Play, error) {
	// Visibility lives in the catalog database and plays in the events
	// database. Without a Library filter the newest limit plays are the page.
	// With one, plays are resolved a chunk at a time and kept only when their
	// Media is in that Library, until the page fills or the window runs out,
	// so a quiet Library is never crowded out by a busy one.
	var raw, pending []derivedPlay
	visible := map[MediaID]Media{}
	var resolveErr error
	resolve := func() bool {
		ids := make([]MediaID, 0, len(pending))
		for _, p := range pending {
			if _, done := visible[p.mediaID]; !done {
				ids = append(ids, p.mediaID)
			}
		}
		found, err := s.visibleMediaBatchSQL(ctx, ids, filter)
		if err != nil {
			resolveErr = err
			return false
		}
		for _, id := range ids {
			visible[id] = found[id] // a zero Media marks one the caller cannot see
		}
		for _, p := range pending {
			if len(raw) == limit {
				break
			}
			if q.LibraryID == "" || visible[p.mediaID].LibraryID == q.LibraryID {
				raw = append(raw, p)
			}
		}
		pending = pending[:0]
		return len(raw) < limit
	}
	err := s.derivedPlaysLocked(ctx, q.ProfileID, since, q.Until, func(p derivedPlay) bool {
		pending = append(pending, p)
		if q.LibraryID == "" {
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
	plays := make([]Play, 0, len(raw))
	for _, p := range raw {
		media := visible[p.mediaID]
		play := Play{
			ProfileID:      p.profileID,
			ProfileName:    names[p.profileID],
			MediaID:        p.mediaID,
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

// PlayStats ranks Profiles and Media by play count since the given instant.
// Every play counts toward its Profile; only Media visible through filter are
// ranked as items.
func (s *Store) PlayStats(ctx context.Context, filter LibraryFilter, since time.Time) (PlayStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PlayStats{}, err
	}
	profiles := map[ProfileID]*PlayStatsProfile{}
	items := map[MediaID]*PlayStatsItem{}
	err := s.derivedPlaysLocked(ctx, "", since, time.Time{}, func(p derivedPlay) bool {
		profile := profiles[p.profileID]
		if profile == nil {
			profile = &PlayStatsProfile{ProfileID: p.profileID}
			profiles[p.profileID] = profile
		}
		profile.Plays++
		profile.WatchedSeconds += p.watchedSeconds()
		item := items[p.mediaID]
		if item == nil {
			item = &PlayStatsItem{MediaID: p.mediaID}
			items[p.mediaID] = item
		}
		item.Plays++
		item.WatchedSeconds += p.watchedSeconds()
		return true
	})
	if err != nil {
		return PlayStats{}, err
	}
	names, err := s.profileNamesLocked(ctx)
	if err != nil {
		return PlayStats{}, err
	}
	ids := make([]MediaID, 0, len(items))
	for id := range items {
		ids = append(ids, id)
	}
	visible, err := s.visibleMediaBatchSQL(ctx, ids, filter)
	if err != nil {
		return PlayStats{}, err
	}
	stats := PlayStats{Profiles: make([]PlayStatsProfile, 0, len(profiles)), Items: make([]PlayStatsItem, 0, len(visible))}
	for _, profile := range profiles {
		profile.ProfileName = names[profile.ProfileID]
		stats.Profiles = append(stats.Profiles, *profile)
	}
	for id, item := range items {
		media, ok := visible[id]
		if !ok {
			continue
		}
		item.Title = media.Title
		item.LibraryID = media.LibraryID
		stats.Items = append(stats.Items, *item)
	}
	sort.Slice(stats.Profiles, func(i, j int) bool {
		a, b := stats.Profiles[i], stats.Profiles[j]
		if a.Plays != b.Plays {
			return a.Plays > b.Plays
		}
		if a.WatchedSeconds != b.WatchedSeconds {
			return a.WatchedSeconds > b.WatchedSeconds
		}
		return a.ProfileID < b.ProfileID
	})
	sort.Slice(stats.Items, func(i, j int) bool {
		a, b := stats.Items[i], stats.Items[j]
		if a.Plays != b.Plays {
			return a.Plays > b.Plays
		}
		if a.WatchedSeconds != b.WatchedSeconds {
			return a.WatchedSeconds > b.WatchedSeconds
		}
		return a.MediaID < b.MediaID
	})
	stats.Profiles = stats.Profiles[:min(len(stats.Profiles), playStatsRanks)]
	stats.Items = stats.Items[:min(len(stats.Items), playStatsRanks)]
	return stats, nil
}
