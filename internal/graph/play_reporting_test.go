package graph

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// reportMinute records two progress reports a minute apart through the
// graph's own progress write, so the session's Device and held play method
// are recorded the way a player's reports record them.
func reportMinute(t *testing.T, handler *Handler, scope auth.Scope, media store.MediaID) {
	t.Helper()
	started := time.Now().Add(-time.Minute)
	for i, at := range []time.Time{started, time.Now()} {
		if _, err := handler.RecordPlaybackProgress(t.Context(), scope, store.PlaybackProgress{
			MediaID: media, PositionSeconds: 30 + 60*i, DurationSeconds: 3600, EventTimeMS: at.UnixMilli(),
		}, false); err != nil {
			t.Fatalf("RecordPlaybackProgress: %v", err)
		}
	}
}

// feature:play-history
func TestPlayReportingRecordsDeviceAndScopesViewers(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	handler.playbackObserver = fixedObserver{"session-default": {Method: "transcode"}}
	viewer := auth.Scope{SessionID: "session-default", DeviceID: "device-tv", ProfileID: "profile-default", AllLibraries: true, Revealed: true}
	managed := auth.Scope{SessionID: "session-managed", DeviceID: "device-tv", ProfileID: "profile-managed", AllLibraries: true}
	reportMinute(t, handler, viewer, "media-alpha")
	// The restricted item: session-default has not revealed it, so the
	// viewer's own reads leave this play out.
	reportMinute(t, handler, viewer, "media-archive")
	reportMinute(t, handler, managed, "media-alpha")

	myHistory := `{"query":"query H($playMethod: String){ myPlayHistory(playMethod:$playMethod){ profileID mediaID title deviceID deviceName deviceClient playMethod watchedSeconds } }","variables":%s}`
	data, errs := post(t, handler, fmt.Sprintf(myHistory, `{}`), authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("myPlayHistory errors = %#v", errs)
	}
	var plays []PlayInfo
	if err := json.Unmarshal(data["myPlayHistory"], &plays); err != nil {
		t.Fatalf("decode myPlayHistory: %v", err)
	}
	if len(plays) != 1 {
		t.Fatalf("myPlayHistory = %#v, want only the caller's visible play", plays)
	}
	if p := plays[0]; p.ProfileID != "profile-default" || p.MediaID != "media-alpha" || p.DeviceID != "device-tv" ||
		p.DeviceName != "TV" || p.DeviceClient != string(store.DeviceSurfaceCoreWebUI) || p.PlayMethod != "transcode" || p.WatchedSeconds != 60 {
		t.Fatalf("viewer play = %#v", p)
	}
	data, errs = post(t, handler, fmt.Sprintf(myHistory, `{}`), authorizeManagedSession)
	if len(errs) != 0 {
		t.Fatalf("managed myPlayHistory errors = %#v", errs)
	}
	if err := json.Unmarshal(data["myPlayHistory"], &plays); err != nil || len(plays) != 1 ||
		plays[0].ProfileID != "profile-managed" || plays[0].PlayMethod != "unknown" {
		t.Fatalf("managed myPlayHistory = %#v (err %v), want only its own play, method unknown", plays, err)
	}
	data, _ = post(t, handler, fmt.Sprintf(myHistory, `{"playMethod":"direct"}`), authorizeViewer)
	if err := json.Unmarshal(data["myPlayHistory"], &plays); err != nil || len(plays) != 0 {
		t.Fatalf("direct-filtered myPlayHistory = %#v (err %v), want none", plays, err)
	}
	if _, errs = post(t, handler, fmt.Sprintf(myHistory, `{"playMethod":"teleport"}`), authorizeViewer); len(errs) == 0 {
		t.Fatal("myPlayHistory accepted an unknown play method")
	}

	myStats := `{"query":"{ myPlayStats(windowDays:30){ totalPlays totalWatchedSeconds profiles { profileID } devices { deviceID deviceName plays } methods { playMethod plays } days { day plays } } }"}`
	data, errs = post(t, handler, myStats, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("myPlayStats errors = %#v", errs)
	}
	var mine PlayStatsInfo
	if err := json.Unmarshal(data["myPlayStats"], &mine); err != nil {
		t.Fatalf("decode myPlayStats: %v", err)
	}
	if mine.TotalPlays != 1 || mine.TotalWatchedSeconds != 60 || len(mine.Profiles) != 1 || mine.Profiles[0].ProfileID != "profile-default" ||
		len(mine.Devices) != 1 || mine.Devices[0].DeviceName != "TV" || len(mine.Methods) != 1 || mine.Methods[0].PlayMethod != "transcode" ||
		len(mine.Days) != 1 {
		t.Fatalf("myPlayStats = %#v", mine)
	}

	// The Console sees every profile's plays, the hidden one untitled, and
	// filters and breaks them down over a date range.
	stats := `{"query":"query S($since: String, $until: String, $windowDays: Int, $playMethod: String){ playStats(since:$since, until:$until, windowDays:$windowDays, playMethod:$playMethod, utcOffsetMinutes:60){ windowDays since totalPlays profiles { profileID } methods { playMethod plays } devices { deviceID plays } days { day plays } } }","variables":%s}`
	if _, errs = post(t, handler, fmt.Sprintf(stats, `{"windowDays":30}`), authorizeManager); len(errs) == 0 || errs[0].Message != "admin session required" {
		t.Fatalf("non-admin playStats errors = %#v, want admin-session gate", errs)
	}
	openManagerAdminSession(t, st)
	since := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	data, errs = post(t, handler, fmt.Sprintf(stats, `{"since":"`+since+`"}`), authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("playStats errors = %#v", errs)
	}
	var all PlayStatsInfo
	if err := json.Unmarshal(data["playStats"], &all); err != nil {
		t.Fatalf("decode playStats: %v", err)
	}
	if all.WindowDays != 0 || all.Since != since || all.TotalPlays != 3 || len(all.Profiles) != 2 ||
		len(all.Methods) != 2 || all.Methods[0].PlayMethod != "transcode" || all.Methods[0].Plays != 2 ||
		len(all.Devices) != 1 || all.Devices[0].Plays != 3 {
		t.Fatalf("playStats = %#v", all)
	}
	data, errs = post(t, handler, fmt.Sprintf(stats, `{"since":"`+since+`","playMethod":"unknown"}`), authorizeManager)
	if err := json.Unmarshal(data["playStats"], &all); len(errs) != 0 || err != nil || all.TotalPlays != 1 {
		t.Fatalf("unknown-method playStats = %#v (errs %#v, err %v), want the managed play", all, errs, err)
	}
	history := `{"query":"query H($deviceID: String, $mediaID: String){ playHistory(deviceID:$deviceID, mediaID:$mediaID){ profileID mediaID title playMethod } }","variables":%s}`
	data, errs = post(t, handler, fmt.Sprintf(history, `{"deviceID":"device-tv","mediaID":"media-archive"}`), authorizeManager)
	if err := json.Unmarshal(data["playHistory"], &plays); len(errs) != 0 || err != nil || len(plays) != 1 ||
		plays[0].ProfileID != "profile-default" || plays[0].Title != "" {
		t.Fatalf("filtered playHistory = %#v (errs %#v, err %v), want the hidden play untitled", plays, errs, err)
	}

	for _, refused := range []string{
		`{}`,
		`{"windowDays":14}`,
		`{"windowDays":30,"since":"` + since + `"}`,
		`{"since":"2024-01-01T00:00:00Z","until":"2025-06-01T00:00:00Z"}`,
		`{"since":"` + since + `","until":"2000-01-01T00:00:00Z"}`,
	} {
		if _, errs = post(t, handler, fmt.Sprintf(stats, refused), authorizeManager); len(errs) == 0 {
			t.Fatalf("playStats accepted %s", refused)
		}
	}
}
