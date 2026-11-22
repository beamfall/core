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
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: "profile-default", MediaID: "media-alpha", PositionSeconds: 30, DurationSeconds: 3600,
		DeviceID: "dev-tv", PlayMethod: PlayMethodRemux,
	}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	var device, method string
	if err := st.eventsR.QueryRow(`
		SELECT json_extract(payload, '$.deviceId'), json_extract(payload, '$.playMethod')
		FROM engagement_event WHERE kind = ? AND media_id = 'media-alpha'
		ORDER BY occurred_at_ms DESC LIMIT 1`, playbackProjectionEventKind).Scan(&device, &method); err != nil {
		t.Fatal(err)
	}
	if device != "dev-tv" || method != PlayMethodRemux {
		t.Fatalf("event device=%q method=%q", device, method)
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

// feature:play-history
func TestPlayHistoryCountsOnlyPlayingTime(t *testing.T) {
	// Codex CORE-1703 r0: a seek made while paused, and the heartbeats of a
	// paused audio player whose stored position is zero, add nothing.
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllLibraries: true}
	base := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	report := func(media MediaID, at time.Time, position int, policy ResumePolicy) {
		t.Helper()
		if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
			ProfileID: "profile-default", MediaID: media, PositionSeconds: position, DurationSeconds: 3600,
			EventTimeMS: at.UnixMilli(),
		}, false, policy); err != nil {
			t.Fatal(err)
		}
	}
	playFor := func(media MediaID, from time.Time, fromPos int, d time.Duration, policy ResumePolicy) {
		for at := time.Duration(0); at <= d; at += 15 * time.Second {
			report(media, from.Add(at), fromPos+int(at/time.Second), policy)
		}
	}
	video := ResumePolicy{Class: MediaClassVideo}
	// Five minutes played, paused silently for 20 minutes, a seek from 300 to
	// 1500 while paused, then five more minutes.
	playFor("media-alpha", base, 0, 5*time.Minute, video)
	playFor("media-alpha", base.Add(25*time.Minute), 1500, 5*time.Minute, video)
	// Audio stores no position, but its paused heartbeats repeat the
	// position the client reported: five minutes played, ten paused with a
	// heartbeat every 15 seconds, five more played.
	audio := ResumePolicy{Class: MediaClassAudio}
	later := base.Add(90 * time.Minute)
	playFor("media-archive", later, 0, 5*time.Minute, audio)
	for at := 5 * time.Minute; at < 15*time.Minute; at += 15 * time.Second {
		report("media-archive", later.Add(at), 300, audio)
	}
	playFor("media-archive", later.Add(15*time.Minute), 300, 5*time.Minute, audio)

	plays, err := st.PlayHistory(ctx, all, PlayHistoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plays) != 2 || plays[0].MediaID != "media-archive" || plays[0].WatchedSeconds != 600 ||
		plays[1].MediaID != "media-alpha" || plays[1].WatchedSeconds != 600 {
		t.Fatalf("plays = %#v, want 600s each: neither the paused seek nor the paused heartbeats count", plays)
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
