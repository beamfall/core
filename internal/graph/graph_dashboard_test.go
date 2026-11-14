package graph

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/beamfall/core/internal/hostmetrics"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type fixedObserver map[store.SessionID]PlayObservation

func (f fixedObserver) PlayObservation(sessionID store.SessionID, _ store.MediaID) (PlayObservation, bool) {
	seen, ok := f[sessionID]
	return seen, ok
}

type fixedHostMetrics []hostmetrics.Sample

func (fixedHostMetrics) Interval() time.Duration         { return 10 * time.Second }
func (f fixedHostMetrics) Samples() []hostmetrics.Sample { return f }

// feature:live-sessions
func TestLiveSessionsReportPlayMethodReasonAndBitrate(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	handler.playbackObserver = fixedObserver{
		"session-default": {Method: "remux", Reason: "container", Bitrate: 8_000_000},
	}
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-hls", "media-alpha", false)
	recordActive(t, st, "session-managed", "profile-managed", "device-tv", "media-direct", "media-alpha", false)
	openManagerAdminSession(t, st)

	data, errs := post(t, handler,
		`{"query":"query { liveSessions { sessionID playMethod playReason bitrate } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("liveSessions errors = %#v", errs)
	}
	var got []LiveSessionInfo
	if err := json.Unmarshal(data["liveSessions"], &got); err != nil {
		t.Fatalf("decode liveSessions: %v", err)
	}
	bySession := map[string]LiveSessionInfo{}
	for _, s := range got {
		bySession[s.SessionID] = s
	}
	if s := bySession["session-default"]; s.PlayMethod != "remux" || s.PlayReason != "container" || s.Bitrate != 8_000_000 {
		t.Errorf("observed session = %#v, want remux / container / 8 Mbps", s)
	}
	// No held decision: the method falls back to the Stream Token ledger.
	if s := bySession["session-managed"]; s.PlayMethod != "direct" || s.PlayReason != "" || s.Bitrate != 0 {
		t.Errorf("unobserved session = %#v, want direct with no reason or bitrate", s)
	}
}

// feature:host-metrics
func TestHostMetricsIsAdminGatedAndListsSamplesOldestFirst(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	handler.hostMetrics = fixedHostMetrics{
		{At: at, CPUPercent: 12.5, CPUKnown: true, MemoryUsedBytes: 4 << 30, MemoryTotalBytes: 16 << 30, SentBytesPerSecond: 1000, ReceivedBytesPerSecond: 50},
		{At: at.Add(10 * time.Second), MemoryUsedBytes: 5 << 30, MemoryTotalBytes: 16 << 30},
	}
	query := `{"query":"query { hostMetrics { intervalSeconds samples { at cpuPercent cpuKnown memoryUsedBytes memoryTotalBytes sentBytesPerSecond receivedBytesPerSecond } } }"}`

	_, errs := post(t, handler, query, authorizeManager)
	if len(errs) == 0 || errs[0].Message != "admin session required" {
		t.Fatalf("non-admin hostMetrics errors = %#v, want admin-session gate", errs)
	}
	openManagerAdminSession(t, st)
	data, errs := post(t, handler, query, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("hostMetrics errors = %#v", errs)
	}
	var got HostMetricsInfo
	if err := json.Unmarshal(data["hostMetrics"], &got); err != nil {
		t.Fatalf("decode hostMetrics: %v", err)
	}
	if got.IntervalSeconds != 10 || len(got.Samples) != 2 {
		t.Fatalf("hostMetrics = %#v, want 10s interval and 2 samples", got)
	}
	first, second := got.Samples[0], got.Samples[1]
	if first.At != "2026-11-11T12:00:00Z" || !first.CPUKnown || first.CPUPercent != 12.5 ||
		first.MemoryUsedBytes != 4<<30 || first.SentBytesPerSecond != 1000 || first.ReceivedBytesPerSecond != 50 {
		t.Errorf("first sample = %#v", first)
	}
	if second.CPUKnown || second.At != "2026-11-11T12:00:10Z" {
		t.Errorf("second sample = %#v, want CPU unknown ten seconds later", second)
	}
}

// feature:play-history
func TestPlayHistoryAndStatsOverGraph(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	for _, profile := range []store.ProfileID{"profile-default", "profile-managed"} {
		if err := st.UpsertPlaybackProgress(t.Context(),
			store.PlaybackProgress{ProfileID: profile, MediaID: "media-alpha", PositionSeconds: 30, DurationSeconds: 120},
			false, store.ResumePolicy{Class: store.MediaClassVideo}); err != nil {
			t.Fatalf("UpsertPlaybackProgress: %v", err)
		}
	}
	history := `{"query":"query H($profileID: String, $libraryID: String, $since: String, $until: String, $limit: Int){ playHistory(profileID:$profileID, libraryID:$libraryID, since:$since, until:$until, limit:$limit){ profileID profileName mediaID title libraryID libraryTitle startedAt watchedSeconds finished } }","variables":%s}`
	stats := `{"query":"query S($windowDays: Int!){ playStats(windowDays:$windowDays){ windowDays profiles { profileID profileName plays watchedSeconds } items { mediaID title libraryID plays watchedSeconds } } }","variables":{"windowDays":%d}}`

	_, errs := post(t, handler, fmt.Sprintf(history, `{}`), authorizeManager)
	if len(errs) == 0 || errs[0].Message != "admin session required" {
		t.Fatalf("non-admin playHistory errors = %#v, want admin-session gate", errs)
	}
	openManagerAdminSession(t, st)

	data, errs := post(t, handler, fmt.Sprintf(history, `{}`), authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("playHistory errors = %#v", errs)
	}
	var plays []PlayInfo
	if err := json.Unmarshal(data["playHistory"], &plays); err != nil {
		t.Fatalf("decode playHistory: %v", err)
	}
	if len(plays) != 2 || plays[0].Title != "Sample Media" || plays[0].LibraryID != "library-local" || plays[0].StartedAt == "" {
		t.Fatalf("playHistory = %#v, want two plays of Sample Media", plays)
	}
	data, errs = post(t, handler, fmt.Sprintf(history, `{"profileID":"profile-managed"}`), authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("filtered playHistory errors = %#v", errs)
	}
	if err := json.Unmarshal(data["playHistory"], &plays); err != nil || len(plays) != 1 || plays[0].ProfileID != "profile-managed" {
		t.Fatalf("profile-filtered playHistory = %#v (err %v), want the managed play", plays, err)
	}
	data, _ = post(t, handler, fmt.Sprintf(history, `{"until":"2000-01-01T00:00:00Z"}`), authorizeManager)
	if err := json.Unmarshal(data["playHistory"], &plays); err != nil || len(plays) != 0 {
		t.Fatalf("date-filtered playHistory = %#v (err %v), want none", plays, err)
	}
	if _, errs = post(t, handler, fmt.Sprintf(history, `{"since":"yesterday"}`), authorizeManager); len(errs) == 0 {
		t.Fatal("playHistory accepted a since that is not RFC3339")
	}

	data, errs = post(t, handler, fmt.Sprintf(stats, 30), authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("playStats errors = %#v", errs)
	}
	var got PlayStatsInfo
	if err := json.Unmarshal(data["playStats"], &got); err != nil {
		t.Fatalf("decode playStats: %v", err)
	}
	if got.WindowDays != 30 || len(got.Profiles) != 2 || len(got.Items) != 1 ||
		got.Items[0].MediaID != "media-alpha" || got.Items[0].Plays != 2 || got.Items[0].Title != "Sample Media" {
		t.Fatalf("playStats = %#v, want two profiles and media-alpha with 2 plays", got)
	}
	if _, errs = post(t, handler, fmt.Sprintf(stats, 14), authorizeManager); len(errs) == 0 {
		t.Fatal("playStats accepted a 14-day window")
	}
}
