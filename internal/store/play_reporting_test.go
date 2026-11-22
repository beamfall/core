package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// deviceReport appends one progress event from a Device, carrying the play
// method the server chose for it, the way the graph's progress write records
// it (CORE-1703).
func deviceReport(t *testing.T, st *Store, profile ProfileID, media MediaID, device DeviceID, method string, at time.Time, position int) {
	t.Helper()
	payload, err := json.Marshal(playbackProjectionEvent{
		Operation: playbackProjectionOperationProgress, ProfileID: string(profile), MediaID: string(media),
		PositionSeconds: position, DurationSeconds: 3600, UpdatedAtMS: timeToMillis(at),
		DeviceID: string(device), PlayMethod: method,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("play-report-%s-%s-%s-%d", profile, media, device, at.UnixNano())
	if _, err := st.eventsW.Exec(`
		INSERT INTO engagement_event(id, profile_id, media_id, kind, payload, occurred_at_ms, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, profile, media, playbackProjectionEventKind, string(payload), timeToMillis(at), timeToMillis(at)); err != nil {
		t.Fatalf("insert progress event: %v", err)
	}
}

// devicePlaying appends the reports a Device sends every 15 seconds while it
// plays continuously from one instant to the other, both included.
func devicePlaying(t *testing.T, st *Store, profile ProfileID, media MediaID, device DeviceID, method string, from, to time.Time, fromPos int) {
	t.Helper()
	for at := from; !at.After(to); at = at.Add(15 * time.Second) {
		deviceReport(t, st, profile, media, device, method, at, fromPos+int(at.Sub(from)/time.Second))
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
	devicePlaying(t, st, "profile-default", "media-alpha", "dev-web", PlayMethodDirect, day, day.Add(10*time.Minute), 0)
	devicePlaying(t, st, "profile-default", "media-alpha", "dev-tv", PlayMethodRemux, day.Add(12*time.Minute), day.Add(20*time.Minute-15*time.Second), 600)
	devicePlaying(t, st, "profile-default", "media-alpha", "dev-tv", PlayMethodTranscode, day.Add(20*time.Minute), day.Add(32*time.Minute), 1080)
	progressAt(t, st, "profile-managed", "media-alpha", day.Add(time.Hour), false)
	playing(t, st, "profile-managed", "media-alpha", day.Add(time.Hour), day.Add(time.Hour+5*time.Minute), time.Time{}, 0, 3600)
	progressAtPosition(t, st, "profile-managed", "media-alpha", day.Add(time.Hour+5*time.Minute), 300, false)
	devicePlaying(t, st, "profile-managed", "media-archive", "dev-web", PlayMethodDirect, day.Add(3*time.Hour+40*time.Minute), day.Add(3*time.Hour+50*time.Minute), 0)
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
		DeviceID: "dev-tv", PlayMethod: PlayMethodRemux, SessionID: "session-secret", Paused: &paused,
	}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	var device, method, stream, payload string
	var gotPaused bool
	if err := st.eventsR.QueryRow(`
		SELECT json_extract(payload, '$.deviceId'), json_extract(payload, '$.playMethod'),
		       json_extract(payload, '$.streamKey'), json_extract(payload, '$.paused'), payload
		FROM engagement_event WHERE kind = ? AND media_id = 'media-alpha'
		ORDER BY occurred_at_ms DESC LIMIT 1`, playbackProjectionEventKind).Scan(&device, &method, &stream, &gotPaused, &payload); err != nil {
		t.Fatal(err)
	}
	// The stream is named by a key derived from the session, never the
	// session ID, which is a credential.
	if device != "dev-tv" || method != PlayMethodRemux || stream != playStreamKey("session-secret") || stream == "" ||
		!gotPaused || strings.Contains(payload, "session-secret") {
		t.Fatalf("event device=%q method=%q stream=%q paused=%v payload=%s", device, method, stream, gotPaused, payload)
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
	devicePlaying(t, st, "profile-default", "media-alpha", "dev-tv", PlayMethodDirect, day, day.Add(10*time.Minute), 0)
	devicePlaying(t, st, "profile-default", "media-alpha", "dev-phone", PlayMethodTranscode, day.Add(11*time.Minute), day.Add(16*time.Minute), 600)
	devicePlaying(t, st, "profile-default", "media-alpha", "dev-tv", PlayMethodDirect, day.Add(17*time.Minute), day.Add(27*time.Minute), 900)

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

// streamReport is one progress report as a native client sends it since
// CORE-1703: the stream it belongs to, the position it reported, whether it
// said it was paused (nil: it did not say), the method held when the server
// received it, and when that was. clientAt is the client's own clock, zero
// when it sent none.
type streamReport struct {
	device   DeviceID
	stream   string
	method   string
	received time.Time
	clientAt time.Time
	position int
	paused   *bool
}

func addStreamReport(t *testing.T, st *Store, profile ProfileID, media MediaID, r streamReport) {
	t.Helper()
	event := playbackProjectionEvent{
		Operation: playbackProjectionOperationProgress, ProfileID: string(profile), MediaID: string(media),
		PositionSeconds: r.position, DurationSeconds: 3600, UpdatedAtMS: timeToMillis(r.received),
		DeviceID: string(r.device), PlayMethod: r.method, ReportedPositionSeconds: &r.position,
		StreamKey: r.stream, Paused: r.paused,
	}
	if !r.clientAt.IsZero() {
		at := timeToMillis(r.clientAt)
		event.EventTimeMS = &at
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("stream-report-%s-%s-%s-%d", profile, media, r.stream, r.received.UnixNano())
	if _, err := st.eventsW.Exec(`
		INSERT INTO engagement_event(id, profile_id, media_id, kind, payload, occurred_at_ms, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, profile, media, playbackProjectionEventKind, string(payload), timeToMillis(r.received), timeToMillis(r.received)); err != nil {
		t.Fatalf("insert progress event: %v", err)
	}
}

// streamPlaying appends the playing reports a stream sends every interval
// from r.received to until, both included, its position advancing with time.
func streamPlaying(t *testing.T, st *Store, profile ProfileID, media MediaID, r streamReport, until time.Time, interval time.Duration) {
	t.Helper()
	from, fromPos := r.received, r.position
	playing := false
	r.paused = &playing
	for at := from; !at.After(until); at = at.Add(interval) {
		r.received, r.position = at, fromPos+int(at.Sub(from)/time.Second)
		addStreamReport(t, st, profile, media, r)
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
	tv := streamReport{device: "dev-tv", stream: "stream-tv", method: PlayMethodDirect}
	phone := streamReport{device: "dev-phone", stream: "stream-phone", method: PlayMethodTranscode}

	tv.received, phone.received, phone.position = day, day.Add(5*time.Second), 1000
	streamPlaying(t, st, "profile-default", "media-alpha", tv, day.Add(10*time.Minute), 15*time.Second)
	streamPlaying(t, st, "profile-default", "media-alpha", phone, day.Add(10*time.Minute+5*time.Second), 15*time.Second)
	plays, err := st.PlayHistory(ctx, all, PlayHistoryQuery{PlayFilter: PlayFilter{MediaID: "media-alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plays) != 2 || plays[0].DeviceID != "dev-phone" || plays[0].WatchedSeconds != 600 || plays[0].Method != PlayMethodTranscode ||
		plays[1].DeviceID != "dev-tv" || plays[1].WatchedSeconds != 600 || plays[1].Method != PlayMethodDirect {
		t.Fatalf("concurrent plays = %#v, want the phone's and the TV's, 600s each", plays)
	}

	tv.received, tv.position = day, 0
	streamPlaying(t, st, "profile-default", "media-archive", tv, day.Add(10*time.Minute), 15*time.Second)
	phone.received, phone.position = day.Add(11*time.Minute), 600
	streamPlaying(t, st, "profile-default", "media-archive", phone, day.Add(16*time.Minute), 15*time.Second)
	tv.received, tv.position = day.Add(17*time.Minute), 900
	streamPlaying(t, st, "profile-default", "media-archive", tv, day.Add(27*time.Minute), 15*time.Second)
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
	web := streamReport{device: "dev-web", stream: "stream-web", method: PlayMethodDirect, received: day}
	streamPlaying(t, st, "profile-default", "media-alpha", web, day.Add(5*time.Minute), 15*time.Second)
	web.paused, web.position = &paused, 300
	web.received = day.Add(5*time.Minute + 5*time.Second) // the pause
	addStreamReport(t, st, "profile-default", "media-alpha", web)
	web.received, web.position = day.Add(5*time.Minute+50*time.Second), 1500 // the paused seek
	addStreamReport(t, st, "profile-default", "media-alpha", web)
	web.received = day.Add(6 * time.Minute) // resumed
	streamPlaying(t, st, "profile-default", "media-alpha", web, day.Add(11*time.Minute), 15*time.Second)

	audio := streamReport{device: "dev-web", stream: "stream-audio", method: PlayMethodDirect, received: day.Add(time.Hour)}
	for at := time.Duration(0); at <= 15*time.Minute; at += 15 * time.Second {
		// Five minutes played, ten paused, heartbeats throughout.
		audio.received, audio.position = day.Add(time.Hour+at), int(min(at, 5*time.Minute)/time.Second)
		addStreamReport(t, st, "profile-default", "media-archive", audio)
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
	// Codex CORE-1703 r1: the web player sends only the latest report while
	// one is in flight, so a slow server can see 100 and then 400 five
	// minutes later. That is five minutes of playing, not a pause.
	st := openSeeded(t)
	ctx := context.Background()
	day := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	playing := false
	web := streamReport{device: "dev-web", stream: "stream-web", method: PlayMethodDirect, received: day, position: 100, paused: &playing}
	addStreamReport(t, st, "profile-default", "media-alpha", web)
	web.received, web.position = day.Add(5*time.Minute), 400
	addStreamReport(t, st, "profile-default", "media-alpha", web)
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
	// for most of its time, and the client's clock, two minutes slow, never
	// enters into it.
	st := openSeeded(t)
	ctx := context.Background()
	day := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	slow := -2 * time.Minute
	playing := false
	tv := streamReport{device: "dev-tv", stream: "stream-tv", method: PlayMethodDirect, paused: &playing}
	for at := time.Duration(0); at < 5*time.Minute; at += 15 * time.Second {
		tv.received, tv.clientAt, tv.position = day.Add(at), day.Add(at+slow), int(at/time.Second)
		addStreamReport(t, st, "profile-default", "media-alpha", tv)
	}
	tv.method = PlayMethodTranscode
	tv.received, tv.clientAt, tv.position = day.Add(6*time.Minute), day.Add(4*time.Minute+slow), 240
	addStreamReport(t, st, "profile-default", "media-alpha", tv)
	for at := 6*time.Minute + 15*time.Second; at <= 7*time.Minute; at += 15 * time.Second {
		tv.received, tv.clientAt, tv.position = day.Add(at), day.Add(at+slow), int(at/time.Second)
		addStreamReport(t, st, "profile-default", "media-alpha", tv)
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
	// where the previous one stopped, is four plays of 60 seconds, not two
	// plays that span the other device's stretches. Reports that name no
	// stream split the same way by Device.
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllLibraries: true}
	day := time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC)
	for media, streams := range map[MediaID][2]string{"media-alpha": {"stream-tv", "stream-phone"}, "media-archive": {"", ""}} {
		for i, device := range []DeviceID{"dev-tv", "dev-phone", "dev-tv", "dev-phone"} {
			r := streamReport{device: device, stream: streams[i%2], method: PlayMethodDirect,
				received: day.Add(time.Duration(i) * 2 * time.Minute), position: 60 * i}
			streamPlaying(t, st, "profile-default", media, r, r.received.Add(time.Minute), 15*time.Second)
		}
		plays, err := st.PlayHistory(ctx, all, PlayHistoryQuery{PlayFilter: PlayFilter{MediaID: media}})
		if err != nil {
			t.Fatal(err)
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
func TestPlayHistoryKeepsTheLegacyRuleForOldReports(t *testing.T) {
	// Codex CORE-1703 r2: reports written before they recorded the client's
	// position read as they always did. Five minutes played, a 20-minute
	// pause, a seek from 300 to 1500, then a minute more: the gap over a
	// minute earns nothing, so 360 seconds, not 1,560.
	st := openSeeded(t)
	ctx := context.Background()
	day := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	devicePlaying(t, st, "profile-default", "media-alpha", "dev-tv", "", day, day.Add(5*time.Minute), 0)
	devicePlaying(t, st, "profile-default", "media-alpha", "dev-tv", "", day.Add(25*time.Minute), day.Add(26*time.Minute), 1500)
	plays, err := st.PlayHistory(ctx, LibraryFilter{AllLibraries: true}, PlayHistoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plays) != 1 || plays[0].WatchedSeconds != 360 {
		t.Fatalf("plays = %#v, want one play of 360s", plays)
	}
}

// feature:play-history
func TestPlayHistoryOrdersStreamlessReportsByReceipt(t *testing.T) {
	// Codex CORE-1703 r2: a Device that records its position but names no
	// stream reports 0, 60 and 120 a minute apart, and its clock jumps back
	// before the third report. Receipt order still credits 120 seconds.
	st := openSeeded(t)
	ctx := context.Background()
	day := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	playing := false
	r := streamReport{device: "dev-tv", method: PlayMethodDirect, paused: &playing}
	for i, clientAt := range []time.Time{day, day.Add(time.Minute), day.Add(-5 * time.Minute)} {
		r.received, r.clientAt, r.position = day.Add(time.Duration(i)*time.Minute), clientAt, 60*i
		addStreamReport(t, st, "profile-default", "media-alpha", r)
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
	r := streamReport{device: "dev-tv", stream: "stream-tv", received: day}
	streamPlaying(t, st, "profile-default", "media-alpha", r, day.Add(10*time.Minute), 15*time.Second)
	r.method, r.received, r.position = PlayMethodTranscode, day.Add(10*time.Minute+15*time.Second), 615
	addStreamReport(t, st, "profile-default", "media-alpha", r)
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
		devicePlaying(t, st, "profile-default", "media-alpha", device, PlayMethodDirect, at, at.Add(time.Minute), 0)
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
