package store

import (
	"context"
	"testing"
	"time"
)

// My Data Summary + Event log (ADR-0036 §2.2 layers 1 & 2) store tests. Both
// reads are COMPUTED ON READ over the caller's own engagement projections and
// Reveal-filtered. These tests seed one Profile's plays across two Libraries
// (the seeded library-local + the restricted library-archive) over a date span,
// then assert: (a) the Summary aggregates only that Profile's visible data;
// (b) the Event log paginates + filters by Library and date range; (c) another
// Profile's events never leak into either.
//
// Plays carry chosen event times (writePlayAt sets updated_at_ms, the recency
// key the My-Data event time reads) so the date span is deterministic. The
// favorite and queue events land at wall-clock now(); the queue API owns the
// queue table (TestPlayQueueWritesStayBehindQueueAPI), so this file never names
// it and never asserts the queue event's exact time — only that it is counted.

// writePlayAt writes one playback_progress row for a Profile/Media with a
// chosen updated_at_ms (the recency key the My-Data event time reads), so a
// date span can be laid out deterministically without depending on wall clock.
func writePlayAt(t *testing.T, st *Store, profile ProfileID, media MediaID, at time.Time, watched bool) {
	t.Helper()
	w := 0
	if watched {
		w = 1
	}
	if _, err := st.eventsW.ExecContext(context.Background(), `
		INSERT INTO playback_progress(profile_id, media_id, position_seconds, duration_seconds, watched, updated_at_ms, play_count)
		VALUES (?, ?, 10, 100, ?, ?, 1)
		ON CONFLICT(profile_id, media_id) DO UPDATE SET watched = excluded.watched, updated_at_ms = excluded.updated_at_ms
	`, profile, media, w, timeToMillis(at)); err != nil {
		t.Fatalf("seed play row: %v", err)
	}
}

// TestMyDataSummaryAndEventLog is the ADR-0036 §2.2 layers 1 & 2 store test.
func TestMyDataSummaryAndEventLog(t *testing.T) {
	// feature:my-data-panel
	st := openSeeded(t)
	ctx := context.Background()
	const profile ProfileID = "profile-default"

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	// Two plays in library-local (media-alpha = visible) at day 0 and day 2, and
	// one play in library-archive (media-archive = RESTRICTED) at day 1.
	writePlayAt(t, st, profile, "media-alpha", base, true)
	writePlayAt(t, st, profile, "media-archive", base.Add(day), false)
	// A second local Media so library-local carries two distinct plays.
	localTwo := importQueueMedia(t, st, "summary-local-two")
	writePlayAt(t, st, profile, localTwo, base.Add(2*day), true)

	// A favorite (pinned into the span at day 0.5) and a queue add (lands at
	// now(), past the span — its time is never asserted, only that it counts).
	if err := st.SetMediaFavorite(ctx, profile, "media-alpha", true, LibraryFilter{Revealed: true, AllLibraries: true}); err != nil {
		t.Fatalf("seed favorite: %v", err)
	}
	if _, err := st.eventsW.ExecContext(ctx, `UPDATE media_favorite SET favorited_at_ms = ? WHERE profile_id = ? AND media_id = ?`,
		timeToMillis(base.Add(12*time.Hour)), profile, "media-alpha"); err != nil {
		t.Fatalf("pin favorite time: %v", err)
	}
	if err := st.QueueAdd(ctx, profile, "media-alpha", nil, LibraryFilter{Revealed: true, AllLibraries: true}); err != nil {
		t.Fatalf("seed queue add: %v", err)
	}

	// Another Profile's identical-Media engagement, which must NEVER leak.
	const other ProfileID = "profile-managed"
	writePlayAt(t, st, other, "media-alpha", base.Add(3*day), true)
	if err := st.SetMediaFavorite(ctx, other, "media-alpha", true, LibraryFilter{AllLibraries: true}); err != nil {
		t.Fatalf("seed other favorite: %v", err)
	}

	revealed := LibraryFilter{Revealed: true, AllLibraries: true}
	relocked := LibraryFilter{AllLibraries: true} // sees only library-local (unrestricted)

	// (a) Summary, revealed: 3 plays (2 local + 1 archive), 2 watched, 1 favorite,
	// 1 queue entry, 2 Libraries. FirstActivity is the day-0 play.
	sum, err := st.MyDataSummary(ctx, profile, revealed)
	if err != nil {
		t.Fatalf("MyDataSummary(revealed): %v", err)
	}
	if sum.Plays != 3 || sum.Watched != 2 || sum.Favorites != 1 || sum.QueueEntries != 1 {
		t.Fatalf("revealed summary counts = %+v, want plays=3 watched=2 fav=1 queue=1", sum)
	}
	if sum.LibraryCount != 2 {
		t.Fatalf("revealed summary libraryCount = %d, want 2", sum.LibraryCount)
	}
	if !sum.FirstActivity.Equal(base) {
		t.Fatalf("revealed firstActivity = %v, want %v", sum.FirstActivity, base)
	}
	if sum.LastActivity.Before(base.Add(2 * day)) {
		t.Fatalf("revealed lastActivity = %v, want >= day 2 (%v)", sum.LastActivity, base.Add(2*day))
	}
	localPlays := 0
	for _, b := range sum.LibraryBreakdown {
		if b.LibraryID == "library-local" {
			localPlays = b.Plays
		}
	}
	if localPlays != 2 {
		t.Fatalf("revealed library-local plays = %d, want 2 (breakdown=%+v)", localPlays, sum.LibraryBreakdown)
	}

	// (c) Cross-profile isolation: the other Profile's day-3 play/favorite never
	// inflate this Profile's totals (asserted by the exact counts above: plays=3
	// not 4, favorites=1 not 2).

	// (a + reveal): relocked, library-archive drops — 2 plays, both local, 1
	// Library, both watched.
	relockedSum, err := st.MyDataSummary(ctx, profile, relocked)
	if err != nil {
		t.Fatalf("MyDataSummary(relocked): %v", err)
	}
	if relockedSum.Plays != 2 || relockedSum.LibraryCount != 1 {
		t.Fatalf("relocked summary = %+v, want plays=2 libraryCount=1 (archive hidden)", relockedSum)
	}
	if relockedSum.Watched != 2 {
		t.Fatalf("relocked watched = %d, want 2 (both local plays watched)", relockedSum.Watched)
	}

	// (b) Event log: revealed, no filter — 5 events (3 plays + 1 fav + 1 queue),
	// newest first.
	page, err := st.MyEngagementLog(ctx, profile, revealed, MyEngagementLogQuery{})
	if err != nil {
		t.Fatalf("MyEngagementLog(all): %v", err)
	}
	if page.Total != 5 || len(page.Events) != 5 {
		t.Fatalf("revealed log = total %d / %d events, want 5/5", page.Total, len(page.Events))
	}
	for i := 1; i < len(page.Events); i++ {
		if page.Events[i].OccurredAt.After(page.Events[i-1].OccurredAt) {
			t.Fatalf("event log not newest-first at %d: %+v", i, page.Events)
		}
	}

	// (b) Pagination: limit 2 / offset 0 then offset 2; total stays 5, no overlap.
	first, err := st.MyEngagementLog(ctx, profile, revealed, MyEngagementLogQuery{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("MyEngagementLog(page1): %v", err)
	}
	second, err := st.MyEngagementLog(ctx, profile, revealed, MyEngagementLogQuery{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("MyEngagementLog(page2): %v", err)
	}
	if first.Total != 5 || len(first.Events) != 2 || len(second.Events) != 2 {
		t.Fatalf("pagination = total %d, page1 %d, page2 %d; want 5/2/2", first.Total, len(first.Events), len(second.Events))
	}
	if first.Events[1].OccurredAt.Before(second.Events[0].OccurredAt) {
		t.Fatalf("pages overlap or misordered: %v then %v", first.Events, second.Events)
	}

	// (b) Library filter: only library-local events (2 plays + fav + queue = 4).
	localOnly, err := st.MyEngagementLog(ctx, profile, revealed, MyEngagementLogQuery{LibraryID: "library-local"})
	if err != nil {
		t.Fatalf("MyEngagementLog(library-local): %v", err)
	}
	if localOnly.Total != 4 {
		t.Fatalf("library-local log total = %d, want 4 (no archive play)", localOnly.Total)
	}
	for _, ev := range localOnly.Events {
		if ev.LibraryID != "library-local" {
			t.Fatalf("library filter leaked %s into library-local page", ev.LibraryID)
		}
	}

	// (b) Date-range filter: a window of day -0.5 .. day 1.25 admits exactly the
	// day-0 alpha play, the day-0.5 favorite, and the day-1 archive play — and
	// EXCLUDES the day-2 local-two play and the now() queue add (range bounds are
	// inclusive and exercised against events on both sides).
	from := base.Add(-12 * time.Hour)
	to := base.Add(day + 6*time.Hour)
	ranged, err := st.MyEngagementLog(ctx, profile, revealed, MyEngagementLogQuery{From: from, To: to})
	if err != nil {
		t.Fatalf("MyEngagementLog(date range): %v", err)
	}
	if ranged.Total != 3 {
		t.Fatalf("date-range log total = %d, want 3 (day0 play + day0.5 fav + day1 archive play): %+v", ranged.Total, ranged.Events)
	}
	for _, ev := range ranged.Events {
		if ev.OccurredAt.Before(from) || ev.OccurredAt.After(to) {
			t.Fatalf("date-range event out of bounds: %v not in %v..%v", ev.OccurredAt, from, to)
		}
	}

	// (c) Event log isolation: the other Profile's day-3 play never appears.
	for _, ev := range page.Events {
		if ev.OccurredAt.Equal(base.Add(3 * day)) {
			t.Fatalf("other Profile's day-3 event leaked into the log: %+v", ev)
		}
	}

	// (b + reveal): relocked log drops the archive play (4 events, all local).
	relockedLog, err := st.MyEngagementLog(ctx, profile, relocked, MyEngagementLogQuery{})
	if err != nil {
		t.Fatalf("MyEngagementLog(relocked): %v", err)
	}
	if relockedLog.Total != 4 {
		t.Fatalf("relocked log total = %d, want 4 (archive play hidden)", relockedLog.Total)
	}
	for _, ev := range relockedLog.Events {
		if ev.MediaID == "media-archive" {
			t.Fatalf("relocked log leaked restricted media-archive: %+v", ev)
		}
	}

	// (b): naming the hidden Library in the filter cannot widen visibility — a
	// relocked session asking for the restricted Library gets nothing.
	hiddenAsk, err := st.MyEngagementLog(ctx, profile, relocked, MyEngagementLogQuery{LibraryID: "library-archive"})
	if err != nil {
		t.Fatalf("MyEngagementLog(relocked, library-archive): %v", err)
	}
	if hiddenAsk.Total != 0 {
		t.Fatalf("relocked session naming hidden Library saw %d events, want 0", hiddenAsk.Total)
	}
}
