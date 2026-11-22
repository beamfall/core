package graph

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store/storetest"
)

const replayMutation = `mutation Replay($events: [OfflineEngagementEventInput!]!) { replayOfflineEngagement(events: $events) { applied duplicates rejected results { eventID status reason } } }`

func replayBody(t *testing.T, events ...map[string]any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": replayMutation, "variables": map[string]any{"events": events}})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func replay(t *testing.T, h *Handler, body string) OfflineEngagementReplay {
	t.Helper()
	data, errs := post(t, h, body, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("replayOfflineEngagement errors = %#v", errs)
	}
	var out OfflineEngagementReplay
	if err := json.Unmarshal(data["replayOfflineEngagement"], &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, data["replayOfflineEngagement"])
	}
	return out
}

// TestReplayOfflineEngagementAppliesOnceAndRejectsHidden pins the B7 wire
// behaviour: events apply in played-at order, a resend reads as duplicate,
// unknown Media is rejected without a record, completion fires the watched
// observer once, and one engagement-changed invalidation follows the batch.
func TestReplayOfflineEngagementAppliesOnceAndRejectsHidden(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	obs := &fakeWatchedObserver{}
	h.SetWatchedObserver(obs)
	base := time.Now().Add(-time.Hour).UnixMilli()
	before := engagementInvalidations(t, st, "profile-default")

	body := replayBody(t,
		map[string]any{"eventID": "evt-2", "mediaID": "media-alpha", "positionSeconds": 120, "durationSeconds": 120, "completed": true, "playedAtMS": base + 2000},
		map[string]any{"eventID": "evt-1", "mediaID": "media-alpha", "positionSeconds": 30, "durationSeconds": 120, "completed": false, "playedAtMS": base + 1000},
		map[string]any{"eventID": "evt-x", "mediaID": "media-does-not-exist", "positionSeconds": 1, "completed": false, "playedAtMS": base},
	)
	out := replay(t, h, body)
	if out.Applied != 2 || out.Duplicates != 0 || out.Rejected != 1 {
		t.Fatalf("counts = %+v", out)
	}
	order := []string{}
	for _, r := range out.Results {
		order = append(order, r.EventID+":"+r.Status)
	}
	if got := strings.Join(order, ","); got != "evt-x:rejected,evt-1:applied,evt-2:applied" {
		t.Fatalf("results = %s, want played-at order", got)
	}
	if out.Results[0].Reason == nil || *out.Results[0].Reason != "unknown media" {
		t.Fatalf("rejected reason = %v", out.Results[0].Reason)
	}
	progress, ok, err := st.PlaybackProgressFor(t.Context(), "profile-default", "media-alpha")
	if err != nil || !ok || !progress.Watched {
		t.Fatalf("progress = %+v ok=%v err=%v, want watched", progress, ok, err)
	}
	if obs.count() != 1 {
		t.Fatalf("watched observer fired %d times, want 1", obs.count())
	}
	if got := engagementInvalidations(t, st, "profile-default") - before; got != 1 {
		t.Fatalf("engagement invalidations = %d, want 1", got)
	}

	again := replay(t, h, body)
	if again.Applied != 0 || again.Duplicates != 2 || again.Rejected != 1 {
		t.Fatalf("resend counts = %+v, want 2 duplicates and the rejection again", again)
	}
	if obs.count() != 1 {
		t.Fatalf("resend re-fired the watched observer (%d)", obs.count())
	}
	if got := engagementInvalidations(t, st, "profile-default") - before; got != 1 {
		t.Fatalf("a duplicate-only resend emitted an invalidation (%d)", got)
	}
}

func TestReplayOfflineEngagementRefusesOversizeBatchAndBadEvents(t *testing.T) {
	h := newTestHandlerWithStore(storetest.Open(t))
	events := make([]map[string]any, 501)
	for i := range events {
		events[i] = map[string]any{"eventID": fmt.Sprintf("e-%d", i), "mediaID": "media-alpha", "positionSeconds": 1, "completed": false, "playedAtMS": 1000}
	}
	if _, errs := post(t, h, replayBody(t, events...), authorizeViewer); len(errs) == 0 {
		t.Fatal("501 events must be refused")
	}

	out := replay(t, h, replayBody(t,
		map[string]any{"eventID": "neg", "mediaID": "media-alpha", "positionSeconds": -5, "completed": false, "playedAtMS": 1000},
		map[string]any{"eventID": "nots", "mediaID": "media-alpha", "positionSeconds": 5, "completed": false, "playedAtMS": 0},
	))
	if out.Applied != 0 || out.Rejected != 2 {
		t.Fatalf("bad events = %+v, want both rejected", out)
	}
	if _, ok, _ := h.store.PlaybackProgressFor(t.Context(), "profile-default", "media-alpha"); ok {
		t.Fatal("a rejected event wrote progress")
	}
}

func TestReplayOfflineEngagementHonoursExpectedProfile(t *testing.T) {
	h := newTestHandlerWithStore(storetest.Open(t))
	body, _ := json.Marshal(map[string]any{
		"query": `mutation Replay($events: [OfflineEngagementEventInput!]!, $expectedProfileID: ID) { replayOfflineEngagement(events: $events, expectedProfileID: $expectedProfileID) { applied } }`,
		"variables": map[string]any{
			"expectedProfileID": "profile-other",
			"events":            []map[string]any{{"eventID": "e1", "mediaID": "media-alpha", "positionSeconds": 5, "completed": false, "playedAtMS": 1000}},
		},
	})
	if _, errs := post(t, h, string(body), authorizeViewer); len(errs) == 0 {
		t.Fatal("a mismatched expectedProfileID must refuse the batch")
	}
	if _, ok, _ := h.store.PlaybackProgressFor(t.Context(), "profile-default", "media-alpha"); ok {
		t.Fatal("a refused batch wrote progress")
	}
}
