package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// deviceReport appends one progress event from a Device, carrying the play
// method the server chose for it, the way the graph's progress write records
// it (CORE-1703). With a playback ID it is a playing report as the web player
// sends it; without one it has only what reports before CORE-1703 had, plus
// the Device and method.
func deviceReport(t *testing.T, st *Store, profile ProfileID, media MediaID, device DeviceID, playback, method string, at time.Time, position int) {
	t.Helper()
	event := playbackProjectionEvent{
		Operation: playbackProjectionOperationProgress, ProfileID: string(profile), MediaID: string(media),
		PositionSeconds: position, DurationSeconds: 3600, UpdatedAtMS: timeToMillis(at),
		DeviceID: string(device), PlayMethod: method,
	}
	if playback != "" {
		playing := false
		event.PlaybackID, event.ReportedPositionSeconds, event.Paused = playback, &position, &playing
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("play-report-%s-%s-%s-%s-%d", profile, media, device, playback, at.UnixNano())
	if _, err := st.eventsW.Exec(`
		INSERT INTO engagement_event(id, profile_id, media_id, kind, payload, occurred_at_ms, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, profile, media, playbackProjectionEventKind, string(payload), timeToMillis(at), timeToMillis(at)); err != nil {
		t.Fatalf("insert progress event: %v", err)
	}
}

// devicePlaying appends the reports a Device sends every 15 seconds while it
// plays continuously from one instant to the other, both included.
func devicePlaying(t *testing.T, st *Store, profile ProfileID, media MediaID, device DeviceID, playback, method string, from, to time.Time, fromPos int) {
	t.Helper()
	for at := from; !at.After(to); at = at.Add(15 * time.Second) {
		deviceReport(t, st, profile, media, device, playback, method, at, fromPos+int(at.Sub(from)/time.Second))
	}
}

func addPlayDevice(t *testing.T, st *Store, id DeviceID, name string, surface DeviceSurface) {
	t.Helper()
	if _, err := st.catalogW.Exec(
		"INSERT INTO device(id, name, role, distribution_class, device_surface, secret_hash, revoked) VALUES (?, ?, ?, ?, ?, ?, 0)",
		id, name, DeviceRoleViewer, DeviceDistributionDirect, surface, "hash-"+string(id)); err != nil {
		t.Fatalf("insert device: %v", err)
	}
}

// seedReportedPlays records four plays on 2026-11-10 (UTC):
//   - 20:00–20:10 profile-default, media-alpha, dev-web, direct (600 s)
//   - 20:12–20:32 profile-default continues on dev-tv, remuxed for eight
//     minutes then transcoded: a separate play, method transcode (1200 s)
//   - 21:00–21:05 profile-managed, media-alpha, recorded before devices were:
//     no device, method unknown (300 s)
//   - 23:40–23:50 profile-managed, the restricted media-archive, dev-web,
//     direct (600 s)
func seedReportedPlays(t *testing.T, st *Store) time.Time {
	t.Helper()
	addPlayDevice(t, st, "dev-web", "Office browser", DeviceSurfaceCoreWebUI)
	addPlayDevice(t, st, "dev-tv", "Living room", DeviceSurfaceTVOSViewer)
	day := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
	devicePlaying(t, st, "profile-default", "media-alpha", "dev-web", "play-web", PlayMethodDirect, day, day.Add(10*time.Minute), 0)
	devicePlaying(t, st, "profile-default", "media-alpha", "dev-tv", "play-tv", PlayMethodRemux, day.Add(12*time.Minute), day.Add(20*time.Minute-15*time.Second), 600)
	devicePlaying(t, st, "profile-default", "media-alpha", "dev-tv", "play-tv", PlayMethodTranscode, day.Add(20*time.Minute), day.Add(32*time.Minute), 1080)
	progressAt(t, st, "profile-managed", "media-alpha", day.Add(time.Hour), false)
	playing(t, st, "profile-managed", "media-alpha", day.Add(time.Hour), day.Add(time.Hour+5*time.Minute), time.Time{}, 0, 3600)
	progressAtPosition(t, st, "profile-managed", "media-alpha", day.Add(time.Hour+5*time.Minute), 300, false)
	devicePlaying(t, st, "profile-managed", "media-archive", "dev-web", "play-archive", PlayMethodDirect, day.Add(3*time.Hour+40*time.Minute), day.Add(3*time.Hour+50*time.Minute), 0)
	return day
}

// feature:play-history
func TestPlayHistoryRecordsDeviceAndMethod(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllLibraries: true}
	day := seedReportedPlays(t, st)

	plays, err := st.PlayHistory(ctx, all, PlayHistoryQuery{Since: day.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(plays) != 4 {
		t.Fatalf("plays = %+v, want 4 (a device switch starts a new play)", plays)
	}
	archive, legacy, tv, web := plays[0], plays[1], plays[2], plays[3]
	if web.DeviceID != "dev-web" || web.DeviceName != "Office browser" || web.DeviceClient != string(DeviceSurfaceCoreWebUI) || web.Method != PlayMethodDirect || web.WatchedSeconds != 600 {
		t.Fatalf("web play = %+v", web)
	}
	if tv.DeviceID != "dev-tv" || tv.DeviceName != "Living room" || tv.DeviceClient != string(DeviceSurfaceTVOSViewer) || tv.Method != PlayMethodTranscode || tv.WatchedSeconds != 1200 {
		t.Fatalf("tv play = %+v, want the most demanding method of the run", tv)
	}
	if !tv.StartedAt.Equal(day.Add(12 * time.Minute)) {
		t.Fatalf("tv play started %v", tv.StartedAt)
	}
	if legacy.DeviceID != "" || legacy.DeviceName != "" || legacy.Method != PlayMethodUnknown || legacy.WatchedSeconds != 300 {
		t.Fatalf("legacy play = %+v", legacy)
	}
	if archive.MediaID != "media-archive" || archive.Title != "" || archive.Method != PlayMethodDirect {
		t.Fatalf("archive play = %+v, want it listed untitled", archive)
	}

	for _, tc := range []struct {
		name   string
		filter PlayFilter
		want   []MediaID
		device DeviceID
	}{
		{"transcode", PlayFilter{Method: PlayMethodTranscode}, []MediaID{"media-alpha"}, "dev-tv"},
		{"unknown", PlayFilter{Method: PlayMethodUnknown}, []MediaID{"media-alpha"}, ""},
		{"device", PlayFilter{DeviceID: "dev-tv"}, []MediaID{"media-alpha"}, "dev-tv"},
		{"item", PlayFilter{MediaID: "media-archive"}, []MediaID{"media-archive"}, "dev-web"},
		{"device and profile", PlayFilter{DeviceID: "dev-web", ProfileID: "profile-default"}, []MediaID{"media-alpha"}, "dev-web"},
	} {
		got, err := st.PlayHistory(ctx, all, PlayHistoryQuery{PlayFilter: tc.filter, Since: day.Add(-time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(tc.want) || got[0].MediaID != tc.want[0] || got[0].DeviceID != tc.device {
			t.Fatalf("%s: plays = %+v", tc.name, got)
		}
	}
}

// feature:play-history
func TestPlayStatsBreaksDownByDeviceMethodAndDay(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllLibraries: true}
	day := seedReportedPlays(t, st)

	stats, err := st.PlayStats(ctx, all, PlayStatsQuery{Since: day.Add(-time.Hour), UTCOffset: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Plays != 4 || stats.WatchedSeconds != 2700 {
		t.Fatalf("totals = %d plays, %d s; want 4, 2700", stats.Plays, stats.WatchedSeconds)
	}
	if len(stats.Items) != 1 || stats.Items[0].MediaID != "media-alpha" || stats.Items[0].Plays != 3 {
		t.Fatalf("items = %+v, want only the visible item ranked", stats.Items)
	}
	wantDevices := []PlayStatsDevice{
		{DeviceID: "dev-web", DeviceName: "Office browser", DeviceClient: string(DeviceSurfaceCoreWebUI), Plays: 2, WatchedSeconds: 1200},
		{DeviceID: "dev-tv", DeviceName: "Living room", DeviceClient: string(DeviceSurfaceTVOSViewer), Plays: 1, WatchedSeconds: 1200},
		{DeviceID: "", Plays: 1, WatchedSeconds: 300},
	}
	if fmt.Sprint(stats.Devices) != fmt.Sprint(wantDevices) {
		t.Fatalf("devices = %+v, want %+v", stats.Devices, wantDevices)
	}
	wantMethods := []PlayStatsMethod{{PlayMethodDirect, 2, 1200}, {PlayMethodTranscode, 1, 1200}, {PlayMethodUnknown, 1, 300}}
	if fmt.Sprint(stats.Methods) != fmt.Sprint(wantMethods) {
		t.Fatalf("methods = %+v, want %+v", stats.Methods, wantMethods)
	}
	// 23:40 UTC is 00:40 the next day an hour east.
	wantDays := []PlayStatsDay{{"2026-11-10", 3, 2100}, {"2026-11-11", 1, 600}}
	if fmt.Sprint(stats.Days) != fmt.Sprint(wantDays) {
		t.Fatalf("days = %+v, want %+v", stats.Days, wantDays)
	}
	var profiles int
	for _, p := range stats.Profiles {
		profiles += p.WatchedSeconds
	}
	if profiles != stats.WatchedSeconds {
		t.Fatalf("profile watched seconds sum to %d, total %d", profiles, stats.WatchedSeconds)
	}

	ranged, err := st.PlayStats(ctx, all, PlayStatsQuery{Since: day, Until: day.Add(3 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if ranged.Plays != 3 || len(ranged.Days) != 1 || ranged.Days[0].Day != "2026-11-10" {
		t.Fatalf("ranged = %+v, want the late play left out", ranged)
	}
	transcoded, err := st.PlayStats(ctx, all, PlayStatsQuery{PlayFilter: PlayFilter{Method: PlayMethodTranscode}, Since: day})
	if err != nil {
		t.Fatal(err)
	}
	if transcoded.Plays != 1 || transcoded.WatchedSeconds != 1200 || len(transcoded.Devices) != 1 || transcoded.Devices[0].DeviceID != "dev-tv" {
		t.Fatalf("transcoded = %+v", transcoded)
	}
	inLibrary, err := st.PlayStats(ctx, all, PlayStatsQuery{PlayFilter: PlayFilter{LibraryID: "library-local"}, Since: day})
	if err != nil {
		t.Fatal(err)
	}
	if inLibrary.Plays != 3 {
		t.Fatalf("library-local plays = %d, want 3", inLibrary.Plays)
	}
}

// feature:play-history
func TestPlayReadsVisibleOnlyDropHiddenMedia(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllLibraries: true}
	day := seedReportedPlays(t, st)
	own := PlayFilter{ProfileID: "profile-managed", VisibleOnly: true}

	plays, err := st.PlayHistory(ctx, all, PlayHistoryQuery{PlayFilter: own, Since: day.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(plays) != 1 || plays[0].MediaID != "media-alpha" || plays[0].Title == "" {
		t.Fatalf("visible plays = %+v, want only media-alpha", plays)
	}
	stats, err := st.PlayStats(ctx, all, PlayStatsQuery{PlayFilter: own, Since: day.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Plays != 1 || stats.WatchedSeconds != 300 || len(stats.Devices) != 1 || stats.Devices[0].DeviceID != "" {
		t.Fatalf("visible stats = %+v, want the hidden play uncounted", stats)
	}
	revealed, err := st.PlayHistory(ctx, LibraryFilter{AllLibraries: true, Revealed: true}, PlayHistoryQuery{PlayFilter: own, Since: day.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(revealed) != 2 {
		t.Fatalf("revealed plays = %+v, want both", revealed)
	}
}

// feature:play-history
func TestPlaybackProgressRecordsDeviceAndMethod(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	paused := true
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: "profile-default", MediaID: "media-alpha", PositionSeconds: 30, DurationSeconds: 3600,
		DeviceID: "dev-tv", PlayMethod: PlayMethodRemux, PlaybackID: "playback-1", Paused: &paused,
	}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	var device, method, playback, payload string
	var gotPaused bool
	if err := st.eventsR.QueryRow(`
		SELECT json_extract(payload, '$.deviceId'), json_extract(payload, '$.playMethod'),
		       json_extract(payload, '$.playbackId'), json_extract(payload, '$.paused'), payload
		FROM engagement_event WHERE kind = ? AND media_id = 'media-alpha'
		ORDER BY occurred_at_ms DESC LIMIT 1`, playbackProjectionEventKind).Scan(&device, &method, &playback, &gotPaused, &payload); err != nil {
		t.Fatal(err)
	}
	if device != "dev-tv" || method != PlayMethodRemux || playback != "playback-1" || !gotPaused {
		t.Fatalf("event device=%q method=%q playback=%q paused=%v payload=%s", device, method, playback, gotPaused, payload)
	}
}

// feature:play-history
func TestPlayHistorySplitsEveryDeviceSwitch(t *testing.T) {
	// Codex CORE-1703 r0: TV, then phone, then TV again within the play gap
	// is three plays, and the second TV play does not bridge the phone's time.
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllLibraries: true}
	addPlayDevice(t, st, "dev-tv", "Living room", DeviceSurfaceTVOSViewer)
	addPlayDevice(t, st, "dev-phone", "Phone", DeviceSurfaceCoreWebUI)
	day := time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC)
	devicePlaying(t, st, "profile-default", "media-alpha", "dev-tv", "play-tv-1", PlayMethodDirect, day, day.Add(10*time.Minute), 0)
	devicePlaying(t, st, "profile-default", "media-alpha", "dev-phone", "play-phone", PlayMethodTranscode, day.Add(11*time.Minute), day.Add(16*time.Minute), 600)
	devicePlaying(t, st, "profile-default", "media-alpha", "dev-tv", "play-tv-2", PlayMethodDirect, day.Add(17*time.Minute), day.Add(27*time.Minute), 900)

	plays, err := st.PlayHistory(ctx, all, PlayHistoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		device  DeviceID
		watched int
	}{{"dev-tv", 600}, {"dev-phone", 300}, {"dev-tv", 600}}
	if len(plays) != len(want) {
		t.Fatalf("plays = %#v, want three: TV, phone, TV", plays)
	}
	for i, w := range want {
		// Newest first.
		got := plays[len(plays)-1-i]
		if got.DeviceID != w.device || got.WatchedSeconds != w.watched {
			t.Fatalf("play %d = %s %ds, want %s %ds", i, got.DeviceID, got.WatchedSeconds, w.device, w.watched)
		}
	}
	tv, err := st.PlayHistory(ctx, all, PlayHistoryQuery{PlayFilter: PlayFilter{DeviceID: "dev-tv"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(tv) != 2 || tv[0].WatchedSeconds != 600 || tv[1].WatchedSeconds != 600 {
		t.Fatalf("TV-filtered plays = %#v, want the two TV plays, 600s each", tv)
	}
}

// clientReport is one progress report as a client sends it since CORE-1703:
// the playback ID of the play it belongs to (empty: it sent none), the
// position it reported, whether it said it was paused (nil: it did not say),
// the method held when the server received it, and when that was. clientAt is
// the client's own clock, zero when it sent none, and played how long it says
// it has played under the playback ID, nil when it sent none.
type clientReport struct {
	device   DeviceID
	playback string
	method   string
	received time.Time
	clientAt time.Time
	position int
	paused   *bool
	played   *int64
}

// playedSeconds is a client's played count of the given seconds.
func playedSeconds(seconds int) *int64 {
	ms := int64(seconds) * 1000
	return &ms
}

func addClientReport(t *testing.T, st *Store, profile ProfileID, media MediaID, r clientReport) {
	t.Helper()
	event := playbackProjectionEvent{
		Operation: playbackProjectionOperationProgress, ProfileID: string(profile), MediaID: string(media),
		PositionSeconds: r.position, DurationSeconds: 3600, UpdatedAtMS: timeToMillis(r.received),
		DeviceID: string(r.device), PlayMethod: r.method, ReportedPositionSeconds: &r.position,
		PlaybackID: r.playback, Paused: r.paused, PlayedMS: r.played,
	}
	if !r.clientAt.IsZero() {
		at := timeToMillis(r.clientAt)
		event.EventTimeMS = &at
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("client-report-%s-%s-%s-%d-%d", profile, media, r.playback, r.received.UnixNano(), r.position)
	if _, err := st.eventsW.Exec(`
		INSERT INTO engagement_event(id, profile_id, media_id, kind, payload, occurred_at_ms, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, profile, media, playbackProjectionEventKind, string(payload), timeToMillis(r.received), timeToMillis(r.received)); err != nil {
		t.Fatalf("insert progress event: %v", err)
	}
}

// clientPlaying appends the playing reports a client sends every interval
// from r.received to until, both included, its position advancing with time.
func clientPlaying(t *testing.T, st *Store, profile ProfileID, media MediaID, r clientReport, until time.Time, interval time.Duration) {
	t.Helper()
	from, fromPos := r.received, r.position
	playing := false
	r.paused = &playing
	for at := from; !at.After(until); at = at.Add(interval) {
		r.received, r.position = at, fromPos+int(at.Sub(from)/time.Second)
		addClientReport(t, st, profile, media, r)
	}
}

// feature:play-history
func TestPlayHistoryKeepsConcurrentStreamsApart(t *testing.T) {
	// Codex CORE-1703 r1: a TV and a phone playing the same item on one
	// Profile at once, each reporting every 15 seconds and 5 seconds apart,
	// are two plays, not dozens of one-report runs. A TV, then the phone,
	// then the TV again is still three plays.
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllLibraries: true}
	addPlayDevice(t, st, "dev-tv", "Living room", DeviceSurfaceTVOSViewer)
	addPlayDevice(t, st, "dev-phone", "Phone", DeviceSurfaceCoreWebUI)
	day := time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC)
	tv := clientReport{device: "dev-tv", playback: "play-tv", method: PlayMethodDirect}
	phone := clientReport{device: "dev-phone", playback: "play-phone", method: PlayMethodTranscode}

	tv.received, phone.received, phone.position = day, day.Add(5*time.Second), 1000
	clientPlaying(t, st, "profile-default", "media-alpha", tv, day.Add(10*time.Minute), 15*time.Second)
	clientPlaying(t, st, "profile-default", "media-alpha", phone, day.Add(10*time.Minute+5*time.Second), 15*time.Second)
	plays, err := st.PlayHistory(ctx, all, PlayHistoryQuery{PlayFilter: PlayFilter{MediaID: "media-alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plays) != 2 || plays[0].DeviceID != "dev-phone" || plays[0].WatchedSeconds != 600 || plays[0].Method != PlayMethodTranscode ||
		plays[1].DeviceID != "dev-tv" || plays[1].WatchedSeconds != 600 || plays[1].Method != PlayMethodDirect {
		t.Fatalf("concurrent plays = %#v, want the phone's and the TV's, 600s each", plays)
	}

	tv.received, tv.position = day, 0
	clientPlaying(t, st, "profile-default", "media-archive", tv, day.Add(10*time.Minute), 15*time.Second)
	phone.received, phone.position = day.Add(11*time.Minute), 600
	clientPlaying(t, st, "profile-default", "media-archive", phone, day.Add(16*time.Minute), 15*time.Second)
	tv.playback, tv.received, tv.position = "play-tv-again", day.Add(17*time.Minute), 900
	clientPlaying(t, st, "profile-default", "media-archive", tv, day.Add(27*time.Minute), 15*time.Second)
	handoff, err := st.PlayHistory(ctx, all, PlayHistoryQuery{PlayFilter: PlayFilter{MediaID: "media-archive"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(handoff) != 3 || handoff[0].WatchedSeconds != 600 || handoff[1].WatchedSeconds != 300 || handoff[2].WatchedSeconds != 600 {
		t.Fatalf("handoff plays = %#v, want TV, phone, TV: 600s, 300s, 600s", handoff)
	}
}

// feature:play-history
func TestPlayHistoryCreditsNothingAfterAPausedReport(t *testing.T) {
	// Codex CORE-1703 r1: paused at 300, then 45 seconds later a seek to
	// 1500 while still paused, earns nothing. Paused heartbeats that repeat
	// the position earn nothing either, even from a client that does not say
	// it is paused.
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllLibraries: true}
	day := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	paused := true
	web := clientReport{device: "dev-web", playback: "play-web", method: PlayMethodDirect, received: day}
	clientPlaying(t, st, "profile-default", "media-alpha", web, day.Add(5*time.Minute), 15*time.Second)
	web.paused, web.position = &paused, 300
	web.received = day.Add(5*time.Minute + 5*time.Second) // the pause
	addClientReport(t, st, "profile-default", "media-alpha", web)
	web.received, web.position = day.Add(5*time.Minute+50*time.Second), 1500 // the paused seek
	addClientReport(t, st, "profile-default", "media-alpha", web)
	web.received = day.Add(6 * time.Minute) // resumed
	clientPlaying(t, st, "profile-default", "media-alpha", web, day.Add(11*time.Minute), 15*time.Second)

	audio := clientReport{device: "dev-web", playback: "play-audio", method: PlayMethodDirect, received: day.Add(time.Hour)}
	for at := time.Duration(0); at <= 15*time.Minute; at += 15 * time.Second {
		// Five minutes played, ten paused, heartbeats throughout.
		audio.received, audio.position = day.Add(time.Hour+at), int(min(at, 5*time.Minute)/time.Second)
		addClientReport(t, st, "profile-default", "media-archive", audio)
	}

	plays, err := st.PlayHistory(ctx, all, PlayHistoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plays) != 2 || plays[0].MediaID != "media-archive" || plays[0].WatchedSeconds != 300 ||
		plays[1].MediaID != "media-alpha" || plays[1].WatchedSeconds != 600 {
		t.Fatalf("plays = %#v, want 600s and 300s: neither the paused seek nor the paused heartbeats count", plays)
	}
}

// feature:play-history
func TestPlayHistoryCreditsCoalescedReports(t *testing.T) {
	// Codex CORE-1703 r1: reports lost on the way mean the server can see
	// 100 and then 400 five minutes later. That is five minutes of playing,
	// not a pause.
	st := openSeeded(t)
	ctx := context.Background()
	day := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	playing := false
	web := clientReport{device: "dev-web", playback: "play-web", method: PlayMethodDirect, received: day, position: 100, paused: &playing}
	addClientReport(t, st, "profile-default", "media-alpha", web)
	web.received, web.position = day.Add(5*time.Minute), 400
	addClientReport(t, st, "profile-default", "media-alpha", web)
	plays, err := st.PlayHistory(ctx, LibraryFilter{AllLibraries: true}, PlayHistoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plays) != 1 || plays[0].WatchedSeconds != 300 {
		t.Fatalf("plays = %#v, want one play of 300s", plays)
	}
}

// feature:play-history
func TestPlayHistoryMethodCoversMostOfThePlay(t *testing.T) {
	// Codex CORE-1703 r1: played direct from 10:00, the server switched the
	// stream to transcode at 10:05, and a report the client observed at
	// 10:04 arrived at 10:06, so it records transcode. The play was direct
	// for most of its time; the client's clock runs two minutes slow.
	st := openSeeded(t)
	ctx := context.Background()
	day := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	slow := -2 * time.Minute
	playing := false
	tv := clientReport{device: "dev-tv", playback: "play-tv", method: PlayMethodDirect, paused: &playing}
	for at := time.Duration(0); at < 5*time.Minute; at += 15 * time.Second {
		tv.received, tv.clientAt, tv.position = day.Add(at), day.Add(at+slow), int(at/time.Second)
		addClientReport(t, st, "profile-default", "media-alpha", tv)
	}
	tv.method = PlayMethodTranscode
	tv.received, tv.clientAt, tv.position = day.Add(6*time.Minute), day.Add(4*time.Minute+slow), 240
	addClientReport(t, st, "profile-default", "media-alpha", tv)
	for at := 6*time.Minute + 15*time.Second; at <= 7*time.Minute; at += 15 * time.Second {
		tv.received, tv.clientAt, tv.position = day.Add(at), day.Add(at+slow), int(at/time.Second)
		addClientReport(t, st, "profile-default", "media-alpha", tv)
	}
	plays, err := st.PlayHistory(ctx, LibraryFilter{AllLibraries: true}, PlayHistoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plays) != 1 || plays[0].Method != PlayMethodDirect {
		t.Fatalf("plays = %#v, want one direct play", plays)
	}
	direct, err := st.PlayHistory(ctx, LibraryFilter{AllLibraries: true}, PlayHistoryQuery{PlayFilter: PlayFilter{Method: PlayMethodDirect}})
	if err != nil {
		t.Fatal(err)
	}
	if len(direct) != 1 {
		t.Fatalf("direct-filtered plays = %#v, want the play", direct)
	}
}

// feature:play-history
func TestPlayHistorySplitsRepeatedHandoffs(t *testing.T) {
	// Codex CORE-1703 r2: a minute each on a TV, a phone, the TV and the
	// phone, each stretch starting two minutes after the last and resuming
	// where the previous one stopped, is four plays of 60 seconds when each
	// stretch has its own playback ID. Reports with no playback ID group as
	// they did before CORE-1703: one play, the same 240 seconds.
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllLibraries: true}
	day := time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC)
	for media, withIDs := range map[MediaID]bool{"media-alpha": true, "media-archive": false} {
		for i, device := range []DeviceID{"dev-tv", "dev-phone", "dev-tv", "dev-phone"} {
			r := clientReport{device: device, method: PlayMethodDirect,
				received: day.Add(time.Duration(i) * 2 * time.Minute), position: 60 * i}
			if withIDs {
				r.playback = fmt.Sprintf("play-%d", i)
			}
			clientPlaying(t, st, "profile-default", media, r, r.received.Add(time.Minute), 15*time.Second)
		}
		plays, err := st.PlayHistory(ctx, all, PlayHistoryQuery{PlayFilter: PlayFilter{MediaID: media}})
		if err != nil {
			t.Fatal(err)
		}
		if !withIDs {
			if len(plays) != 1 || plays[0].WatchedSeconds != 240 {
				t.Fatalf("%s plays = %#v, want one play of 240s", media, plays)
			}
			continue
		}
		if len(plays) != 4 {
			t.Fatalf("%s plays = %#v, want four", media, plays)
		}
		for i, p := range plays {
			want := DeviceID("dev-phone")
			if i%2 == 1 {
				want = "dev-tv"
			}
			if p.DeviceID != want || p.WatchedSeconds != 60 {
				t.Fatalf("%s play %d = %#v, want 60s on %s", media, i, p, want)
			}
		}
	}
}

// feature:play-history
func TestPlayHistoryShortHandoffKeepsEveryPlay(t *testing.T) {
	// Codex CORE-1703 r3: 60 seconds on a TV, 45 on a phone, then the TV
	// resumes 50 seconds after its last report and plays 60 more. Three
	// playback IDs, three plays, 165 seconds; the TV's two plays never bridge
	// the phone's time.
	st := openSeeded(t)
	ctx := context.Background()
	day := time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC)
	stretches := []clientReport{
		{device: "dev-tv", playback: "play-a", received: day, position: 0},
		{device: "dev-phone", playback: "play-b", received: day.Add(65 * time.Second), position: 60},
		{device: "dev-tv", playback: "play-c", received: day.Add(110 * time.Second), position: 105},
	}
	for i, r := range stretches {
		r.method = PlayMethodDirect
		length := []time.Duration{60, 45, 60}[i] * time.Second
		clientPlaying(t, st, "profile-default", "media-alpha", r, r.received.Add(length), 15*time.Second)
	}
	plays, err := st.PlayHistory(ctx, LibraryFilter{AllLibraries: true}, PlayHistoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, p := range plays {
		total += p.WatchedSeconds
	}
	if len(plays) != 3 || total != 165 || plays[0].WatchedSeconds != 60 || plays[1].WatchedSeconds != 45 {
		t.Fatalf("plays = %#v, want three plays totalling 165s", plays)
	}
}

// feature:play-history
func TestPlayHistoryCreditsADelayedReport(t *testing.T) {
	// Codex CORE-1703 r3: a TV and a phone each play 180 seconds at once.
	// One TV report is lost to a 75-second network stall, so the TV's next
	// report arrives 75 seconds and 75 positions after the last. The TV
	// still gets its 180 seconds.
	st := openSeeded(t)
	ctx := context.Background()
	day := time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC)
	playing := false
	phone := clientReport{device: "dev-phone", playback: "play-phone", method: PlayMethodDirect, received: day.Add(5 * time.Second)}
	clientPlaying(t, st, "profile-default", "media-alpha", phone, phone.received.Add(180*time.Second), 15*time.Second)
	tv := clientReport{device: "dev-tv", playback: "play-tv", method: PlayMethodDirect, paused: &playing}
	for _, at := range []int{0, 15, 30, 45, 60, 135, 150, 165, 180} {
		tv.received, tv.clientAt, tv.position = day.Add(time.Duration(at)*time.Second), day.Add(time.Duration(at)*time.Second), at
		addClientReport(t, st, "profile-default", "media-alpha", tv)
	}
	plays, err := st.PlayHistory(ctx, LibraryFilter{AllLibraries: true}, PlayHistoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plays) != 2 || plays[0].WatchedSeconds != 180 || plays[1].WatchedSeconds != 180 {
		t.Fatalf("plays = %#v, want the phone's and the TV's, 180s each", plays)
	}
}

// playHistoryOf returns every play recorded so far.
func playHistoryOf(t *testing.T, st *Store) []Play {
	t.Helper()
	plays, err := st.PlayHistory(context.Background(), LibraryFilter{AllLibraries: true}, PlayHistoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	return plays
}

// feature:play-history
func TestPlayHistoryCreditsPlayedTimeWhenTheClockGoesBack(t *testing.T) {
	// Codex CORE-1703 r6: the device clock is set back two minutes after the
	// first report, so the four heartbeats over the next minute carry event
	// times only a millisecond apart. Their played count still grows by 15
	// seconds each, so the play counts and earns its 60 seconds.
	st := openSeeded(t)
	day := time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC)
	playing := false
	r := clientReport{device: "dev-web", playback: "play-web", method: PlayMethodDirect, paused: &playing}
	for i := range 5 {
		at := 15 * i
		r.received, r.position, r.played = day.Add(time.Duration(at)*time.Second), at, playedSeconds(at)
		r.clientAt = day.Add(time.Duration(i) * time.Millisecond)
		addClientReport(t, st, "profile-default", "media-alpha", r)
	}
	if plays := playHistoryOf(t, st); len(plays) != 1 || plays[0].WatchedSeconds != 60 {
		t.Fatalf("plays = %#v, want one play of 60s", plays)
	}
}

// feature:play-history
func TestPlayHistoryCreditsNothingForADelayedSeek(t *testing.T) {
	// Codex CORE-1703 r6: after a minute of play the viewer seeks from 60 to
	// 180, and that report arrives 45 seconds after the one before. The seek
	// added nothing to the played count, so it earns nothing, and the 15
	// seconds played after it count: 75 in all, not 120.
	st := openSeeded(t)
	day := time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC)
	playing := false
	r := clientReport{device: "dev-web", playback: "play-web", method: PlayMethodDirect, paused: &playing}
	for _, step := range []struct{ received, position, played int }{
		{0, 0, 0}, {15, 15, 15}, {30, 30, 30}, {45, 45, 45}, {60, 60, 60}, {105, 180, 60}, {120, 195, 75},
	} {
		r.received, r.clientAt = day.Add(time.Duration(step.received)*time.Second), day.Add(time.Duration(step.received)*time.Second)
		r.position, r.played = step.position, playedSeconds(step.played)
		addClientReport(t, st, "profile-default", "media-alpha", r)
	}
	if plays := playHistoryOf(t, st); len(plays) != 1 || plays[0].WatchedSeconds != 75 {
		t.Fatalf("plays = %#v, want one play of 75s", plays)
	}
}

// feature:play-history
func TestPlayHistoryStepsAResumedPlayFromCountsBeforeSince(t *testing.T) {
	// Codex CORE-1703 r7: a play runs 600 seconds, pauses for an hour and
	// resumes for 60 more under the same playback ID. Listing from just
	// before the resume must credit the resumed play its 60 seconds, not the
	// whole 660 its count reached because the earlier counts fell outside
	// the window.
	st := openSeeded(t)
	day := time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC)
	playing := false
	r := clientReport{device: "dev-web", playback: "play-web", method: PlayMethodDirect, paused: &playing}
	report := func(at, played int) {
		r.received, r.clientAt = day.Add(time.Duration(at)*time.Second), day.Add(time.Duration(at)*time.Second)
		r.position, r.played = played, playedSeconds(played)
		addClientReport(t, st, "profile-default", "media-alpha", r)
	}
	for at := 0; at <= 600; at += 15 {
		report(at, at)
	}
	for at := 4200; at <= 4260; at += 15 {
		report(at, at-3600)
	}
	plays, err := st.PlayHistory(context.Background(), LibraryFilter{AllLibraries: true},
		PlayHistoryQuery{Since: day.Add(4000 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if len(plays) != 1 || plays[0].WatchedSeconds != 60 {
		t.Fatalf("plays since the pause = %#v, want one play of 60s", plays)
	}
	if all := playHistoryOf(t, st); len(all) != 2 || all[0].WatchedSeconds != 60 || all[1].WatchedSeconds != 600 {
		t.Fatalf("plays = %#v, want plays of 60s and 600s", all)
	}
}

// feature:play-history
func TestPlayHistoryLetsPlayedCountsAloneDecide(t *testing.T) {
	// Codex CORE-1703 r7: once a playback ID sends played counts, a report of
	// it without one adds nothing. Counts of 0 and 60 seconds with a report
	// between that omits its count earn 60 seconds, not 90.
	st := openSeeded(t)
	day := time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC)
	playing := false
	r := clientReport{device: "dev-web", playback: "play-web", method: PlayMethodDirect, paused: &playing}
	for _, step := range []struct {
		at     int
		played *int64
	}{{0, playedSeconds(0)}, {30, nil}, {60, playedSeconds(60)}} {
		r.received, r.clientAt = day.Add(time.Duration(step.at)*time.Second), day.Add(time.Duration(step.at)*time.Second)
		r.position, r.played = step.at, step.played
		addClientReport(t, st, "profile-default", "media-alpha", r)
	}
	if plays := playHistoryOf(t, st); len(plays) != 1 || plays[0].WatchedSeconds != 60 {
		t.Fatalf("plays = %#v, want one play of 60s", plays)
	}
}

// feature:play-history
func TestPlayHistoryCreditsARepeatedReportOnce(t *testing.T) {
	// A report at 15 seconds played is sent again and arrives late, after the
	// report at 30. Each played second is credited once: 45 in all.
	st := openSeeded(t)
	day := time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC)
	playing := false
	r := clientReport{device: "dev-web", playback: "play-web", method: PlayMethodDirect, paused: &playing}
	for i, played := range []int{0, 15, 30, 15, 45} {
		r.received, r.position, r.played = day.Add(time.Duration(15*i)*time.Second), played, playedSeconds(played)
		addClientReport(t, st, "profile-default", "media-alpha", r)
	}
	if plays := playHistoryOf(t, st); len(plays) != 1 || plays[0].WatchedSeconds != 45 {
		t.Fatalf("plays = %#v, want one play of 45s", plays)
	}
}

// feature:play-history
func TestPlayHistoryIgnoresANegativePlayedCount(t *testing.T) {
	// A played count below zero is not believed, so it cannot lower the
	// baseline the next report is measured from: 30 seconds, not 1,030.
	st := openSeeded(t)
	day := time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC)
	playing := false
	negative := int64(-1_000_000)
	r := clientReport{device: "dev-web", playback: "play-web", method: PlayMethodDirect, paused: &playing, received: day, played: &negative}
	addClientReport(t, st, "profile-default", "media-alpha", r)
	r.received, r.position, r.played = day.Add(30*time.Second), 30, playedSeconds(30)
	addClientReport(t, st, "profile-default", "media-alpha", r)
	if plays := playHistoryOf(t, st); len(plays) != 1 || plays[0].WatchedSeconds != 30 {
		t.Fatalf("plays = %#v, want one play of 30s", plays)
	}
}

// feature:play-history
func TestPlayHistoryCountsAPlaybackByItsPlayedTime(t *testing.T) {
	// A play is opened, played for 10 seconds and left paused, its paused
	// heartbeats running on for ten minutes. Ten minutes of reports are not
	// a play: it played 10 seconds, short of the 30-second minimum.
	st := openSeeded(t)
	day := time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC)
	paused := true
	r := clientReport{device: "dev-web", playback: "play-web", method: PlayMethodDirect, paused: &paused}
	for at := 0; at <= 600; at += 15 {
		r.received, r.clientAt = day.Add(time.Duration(at)*time.Second), day.Add(time.Duration(at)*time.Second)
		r.position, r.played = min(at, 10), playedSeconds(min(at, 10))
		addClientReport(t, st, "profile-default", "media-alpha", r)
	}
	if plays := playHistoryOf(t, st); len(plays) != 0 {
		t.Fatalf("plays = %#v, want none", plays)
	}
}

// stalledReports appends a play that reports every 15 seconds from 0 to 300,
// then a report at 300 whose request stalls for 30 seconds, and after it the
// reports queued behind it, which arrive back to back. The device clock was
// set back while the request stalled, so the queued reports' event times are
// milliseconds apart too. Each queued report is given as its played count in
// seconds, position and paused.
func stalledReports(t *testing.T, st *Store, day time.Time, queued []struct {
	played, position int
	paused           bool
}) {
	t.Helper()
	playing := false
	r := clientReport{device: "dev-web", playback: "play-web", method: PlayMethodDirect, paused: &playing}
	for at := 0; at < 300; at += 15 {
		r.received, r.clientAt, r.position = day.Add(time.Duration(at)*time.Second), day.Add(time.Duration(at)*time.Second), at
		r.played = playedSeconds(at)
		addClientReport(t, st, "profile-default", "media-alpha", r)
	}
	arrived := day.Add(330 * time.Second)
	r.received, r.clientAt, r.position, r.played = arrived, day.Add(300*time.Second), 300, playedSeconds(300)
	addClientReport(t, st, "profile-default", "media-alpha", r)
	for i, q := range queued {
		paused := q.paused
		r.received = arrived.Add(time.Duration(i+1) * 10 * time.Millisecond)
		r.clientAt = day.Add(10*time.Second + time.Duration(i)*time.Millisecond)
		r.position, r.paused, r.played = q.position, &paused, playedSeconds(q.played)
		addClientReport(t, st, "profile-default", "media-alpha", r)
	}
}

// feature:play-history
func TestPlayHistoryCreditsReportsQueuedBehindAStall(t *testing.T) {
	// Codex CORE-1703 r3 and r6: the report at 300 stalls, and the device
	// clock is set back; a pause at 302, a resume eight seconds later and a
	// heartbeat 14 seconds after that queue behind it and arrive, with event
	// times, within milliseconds. Their played counts carry the 16 seconds
	// played: 316 in all.
	st := openSeeded(t)
	day := time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC)
	stalledReports(t, st, day, []struct {
		played, position int
		paused           bool
	}{{302, 302, true}, {302, 302, false}, {316, 316, false}})
	if plays := playHistoryOf(t, st); len(plays) != 1 || plays[0].WatchedSeconds != 316 {
		t.Fatalf("plays = %#v, want one play of 316s", plays)
	}
}

// feature:play-history
func TestPlayHistoryCreditsNothingForAPausedSeekQueuedBehindAStall(t *testing.T) {
	// Codex CORE-1703 r3: the report at 300 stalls; a pause at 301, a paused
	// seek to 3599, play, and the finish at 3600 a second later queue behind
	// it. Only the seconds played count: 302 in all.
	st := openSeeded(t)
	day := time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC)
	stalledReports(t, st, day, []struct {
		played, position int
		paused           bool
	}{{301, 301, true}, {301, 3599, true}, {301, 3599, false}, {302, 3600, false}})
	if plays := playHistoryOf(t, st); len(plays) != 1 || plays[0].WatchedSeconds != 302 {
		t.Fatalf("plays = %#v, want one play of 302s", plays)
	}
}

// feature:play-history
func TestPlayHistoryKeepsTheLegacyRuleForOldReports(t *testing.T) {
	// Codex CORE-1703 r2: reports written before they recorded the client's
	// position read as they always did. Five minutes played, a 20-minute
	// pause, a seek from 300 to 1500, then a minute more: the gap over a
	// minute earns nothing, so 360 seconds, not 1,560.
	st := openSeeded(t)
	ctx := context.Background()
	day := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	devicePlaying(t, st, "profile-default", "media-alpha", "dev-tv", "", "", day, day.Add(5*time.Minute), 0)
	devicePlaying(t, st, "profile-default", "media-alpha", "dev-tv", "", "", day.Add(25*time.Minute), day.Add(26*time.Minute), 1500)
	plays, err := st.PlayHistory(ctx, LibraryFilter{AllLibraries: true}, PlayHistoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plays) != 1 || plays[0].WatchedSeconds != 360 {
		t.Fatalf("plays = %#v, want one play of 360s", plays)
	}
}

// feature:play-history
func TestPlayHistoryOrdersPlaybackReportsByReceipt(t *testing.T) {
	// Codex CORE-1703 r2: one play reports 0, 60 and 120 a minute apart, and
	// the client's clock jumps back before the third report. Receipt order
	// still credits 120 seconds.
	st := openSeeded(t)
	ctx := context.Background()
	day := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	playing := false
	r := clientReport{device: "dev-tv", playback: "play-tv", method: PlayMethodDirect, paused: &playing}
	for i, clientAt := range []time.Time{day, day.Add(time.Minute), day.Add(-5 * time.Minute)} {
		r.received, r.clientAt, r.position = day.Add(time.Duration(i)*time.Minute), clientAt, 60*i
		addClientReport(t, st, "profile-default", "media-alpha", r)
	}
	plays, err := st.PlayHistory(ctx, LibraryFilter{AllLibraries: true}, PlayHistoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plays) != 1 || plays[0].WatchedSeconds != 120 {
		t.Fatalf("plays = %#v, want one play of 120s", plays)
	}
}

// feature:play-history
func TestPlayHistoryTrailingMethodDoesNotTakeThePlay(t *testing.T) {
	// Codex CORE-1703 r2: ten minutes with no held method, then one last
	// report that records transcode. The play stays unknown, and none of its
	// time lands in the transcode breakdown.
	st := openSeeded(t)
	ctx := context.Background()
	day := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	r := clientReport{device: "dev-tv", playback: "play-tv", received: day}
	clientPlaying(t, st, "profile-default", "media-alpha", r, day.Add(10*time.Minute), 15*time.Second)
	r.method, r.received, r.position = PlayMethodTranscode, day.Add(10*time.Minute+15*time.Second), 615
	addClientReport(t, st, "profile-default", "media-alpha", r)
	stats, err := st.PlayStats(ctx, LibraryFilter{AllLibraries: true}, PlayStatsQuery{Since: day.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	want := []PlayStatsMethod{{PlayMethodUnknown, 1, 615}}
	if fmt.Sprint(stats.Methods) != fmt.Sprint(want) {
		t.Fatalf("methods = %+v, want %+v", stats.Methods, want)
	}
}

// feature:play-history
func TestPlayStatsDeviceAndMethodBreakdownsAddUp(t *testing.T) {
	// Codex CORE-1703 r0: rankings keep the top ten, but the Device and
	// method breakdowns list every entry so they sum to the totals.
	st := openSeeded(t)
	ctx := context.Background()
	day := time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)
	for i := range 11 {
		device := DeviceID(fmt.Sprintf("dev-%02d", i))
		at := day.Add(time.Duration(i) * time.Hour)
		devicePlaying(t, st, "profile-default", "media-alpha", device, string(device), PlayMethodDirect, at, at.Add(time.Minute), 0)
	}
	stats, err := st.PlayStats(ctx, LibraryFilter{AllLibraries: true}, PlayStatsQuery{Since: day, Until: day.Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	devicePlays, methodPlays := 0, 0
	for _, d := range stats.Devices {
		devicePlays += d.Plays
	}
	for _, m := range stats.Methods {
		methodPlays += m.Plays
	}
	if stats.Plays != 11 || len(stats.Devices) != 11 || devicePlays != 11 || methodPlays != 11 {
		t.Fatalf("plays = %d, devices = %d summing to %d, methods sum to %d; want 11 throughout",
			stats.Plays, len(stats.Devices), devicePlays, methodPlays)
	}
}
