package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// An EOF observed by one player must reach another session through the same
// engagement invalidation that refreshes every loaded page, even for another
// Profile with its own resume state.
func TestSourceEndInvalidatesOtherSessionsContinueWatching(t *testing.T) {
	for _, observerID := range []store.SessionID{"session-revealed", "session-managed"} {
		t.Run(string(observerID), func(t *testing.T) {
			srv, st := newTestServerWithStore(t)
			ctx := context.Background()
			player, err := srv.auth.Scope(ctx, "session-default")
			if err != nil {
				t.Fatal(err)
			}
			observer, err := srv.auth.Scope(ctx, observerID)
			if err != nil {
				t.Fatal(err)
			}
			if player.SessionID == observer.SessionID {
				t.Fatal("fixture needs distinct sessions")
			}
			paths := map[store.MediaID]string{}
			for _, name := range []string{"first", "second", "third"} {
				path := filepath.Join(t.TempDir(), name+".mkv")
				if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
					t.Fatal(err)
				}
				media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
					LibraryID: "library-local", Path: path, Title: name,
					Container: "mkv", Codec: "h264", Class: store.MediaClassVideo, ContentFingerprint: name,
				})
				if err != nil {
					t.Fatal(err)
				}
				paths[media.ID] = path
				if err := st.UpsertPlaybackProgress(ctx, store.PlaybackProgress{
					ProfileID: observer.ProfileID, MediaID: media.ID, PositionSeconds: 163, DurationSeconds: 6906,
				}, false, store.ResumePolicy{Class: store.MediaClassVideo}); err != nil {
					t.Fatal(err)
				}
			}
			pages := func(kind string) []store.SourceItem {
				t.Helper()
				var loaded []store.SourceItem
				for offset := range 2 {
					items, err := st.SourceItems(ctx, observer.LibraryFilter(), store.SourceQuery{
						Kind: kind, Value: "in-progress", ProfileID: observer.ProfileID, Limit: 1, Offset: offset,
					})
					if err != nil || len(items) != 1 {
						t.Fatalf("%s page %d = %v, %v", kind, offset, items, err)
					}
					loaded = append(loaded, items...)
				}
				return loaded
			}
			loaded := pages("continue-watching")
			_ = pages("resume-progress")
			blocked := loaded[1].ID // The stale resume is on a later loaded page.
			if _, err := st.Media(ctx, blocked, player.PlaybackFilter()); err != nil {
				t.Fatal(err)
			}
			events, err := st.ReadEventOutbox(ctx, 0, 1000)
			if err != nil || len(events) == 0 {
				t.Fatalf("baseline outbox = %v, %v", events, err)
			}
			cursor := events[len(events)-1].Seq
			info, err := os.Stat(paths[blocked])
			if err != nil {
				t.Fatal(err)
			}
			if err := st.ObserveSourceEnd(ctx, blocked, paths[blocked], "", store.SourceFileGeneration(info), func() (float64, bool) {
				return 152, true
			}); err != nil {
				t.Fatal(err)
			}
			body := collectSSE(t, srv, func(req *http.Request) {
				authorizeViewer(req)
				req.Header.Set("X-Beamfall-Session", string(observer.SessionID))
				req.Header.Set("Last-Event-ID", strconv.FormatInt(cursor, 10))
			})
			if !strings.Contains(body, "event: client.invalidate\n") || !strings.Contains(body, `"reason":"engagement-changed"`) {
				t.Fatalf("other session received no engagement invalidation: %s", body)
			}
			if strings.Contains(body, string(blocked)) {
				t.Fatalf("content-free invalidation leaked Media identity: %s", body)
			}
			for _, kind := range []string{"continue-watching", "resume-progress"} {
				for _, item := range pages(kind) {
					if item.ID == blocked {
						t.Fatalf("%s still offers blocked resume after invalidation", kind)
					}
				}
			}
		})
	}
}

// TestInvalidationEventProfileGated is the PANEL-17 SSE-delivery gate: a
// server-initiated client-cache invalidation reaches ONLY the affected Profile,
// never a different Profile and never a non-Profile (admin/unbound) context. The
// signal names no Media (invariant #5: the payload carries only the affected
// Profile and a content-free Reason), so it is Profile-gated and NOT
// reveal-filtered. This is load-bearing: without the explicit branch the
// eventPermitted catch-all would deliver every non-Media aggregate to everyone.
func TestInvalidationEventProfileGated(t *testing.T) {
	srv, _ := newTestServerWithStore(t)
	ctx := context.Background()
	now := time.Unix(0, 0).UTC()

	payload, err := json.Marshal(store.InvalidationPayload{
		ProfileID: "profile-managed",
		Reason:    store.InvalidationReasonAccessChanged,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	event := store.OutboxEvent{
		Kind:          store.InvalidationKind,
		AggregateType: store.OutboxAggregateInvalidation,
		AggregateID:   "profile-managed",
		Payload:       payload,
		OccurredAt:    now,
	}

	// The affected Profile receives it — under the narrowest (unrevealed) filter,
	// proving reveal is not consulted.
	if !srv.eventPermitted(ctx, event, "profile-managed", eventsActionUnrevealedFilter()) {
		t.Fatal("eventPermitted denied an invalidation to its OWN Profile")
	}
	// A different Profile never observes it (cross-Profile gate, fail-closed).
	if srv.eventPermitted(ctx, event, "profile-default", eventsActionRevealedFilter()) {
		t.Fatal("eventPermitted leaked an invalidation to a DIFFERENT Profile")
	}
	// A non-Profile context (admin token / unbound scoped key) never observes it.
	if srv.eventPermitted(ctx, event, "", eventsActionRevealedFilter()) {
		t.Fatal("eventPermitted leaked an invalidation to a non-Profile context")
	}
	// A malformed payload fails closed.
	bad := event
	bad.Payload = []byte("{not json")
	if srv.eventPermitted(ctx, bad, "profile-managed", eventsActionUnrevealedFilter()) {
		t.Fatal("eventPermitted allowed an invalidation with a malformed payload")
	}
}

// Pausing or finishing playback on one client must emit a Profile-scoped
// engagement-changed invalidation so open pages for the same Profile refresh,
// while periodic progress reports are throttled and other Profiles never see it.
func TestPlaybackProgressInvalidatesSameProfileSessionsAndThrottles(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()

	cursorAfter := func() int64 {
		events, err := st.ReadEventOutbox(ctx, 0, 1000)
		if err != nil {
			t.Fatalf("outbox: %v", err)
		}
		if len(events) == 0 {
			return 0
		}
		return events[len(events)-1].Seq
	}
	collectFrom := func(authorize func(*http.Request), cursor int64) string {
		return collectSSE(t, srv, func(req *http.Request) {
			authorize(req)
			req.Header.Set("Last-Event-ID", strconv.FormatInt(cursor, 10))
		})
	}
	reportGraphQL := func(variables map[string]any) {
		t.Helper()
		payload, err := json.Marshal(map[string]any{
			"query":     `mutation R($mediaID: String!, $positionSeconds: Int!, $durationSeconds: Int!, $finished: Boolean, $paused: Boolean) { reportPlayback(mediaID: $mediaID, positionSeconds: $positionSeconds, durationSeconds: $durationSeconds, finished: $finished, paused: $paused) { mediaID positionSeconds watched } }`,
			"variables": variables,
		})
		if err != nil {
			t.Fatal(err)
		}
		req := newJSONRequest(http.MethodPost, "/api/graphql", strings.NewReader(string(payload)))
		authorizeViewer(req)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("reportPlayback status = %d: %s", rec.Code, rec.Body.String())
		}
	}

	// Periodic playing reports inside the throttle window produce at most one
	// invalidation across multiple beats, never one per 15s report.
	cursor := cursorAfter()
	for i, pos := range []int{15, 30, 45} {
		reportGraphQL(map[string]any{
			"mediaID":         "media-alpha",
			"positionSeconds": pos,
			"durationSeconds": 120,
			"finished":        false,
			"paused":          false,
		})
		if i == 0 {
			// Verify no Media identity leaks even if the opening report emitted.
			if body := collectFrom(authorizeRevealed, cursor); strings.Contains(body, "media-alpha") {
				t.Fatalf("invalidation leaked Media identity: %s", body)
			}
		}
	}
	periodicBody := collectFrom(authorizeRevealed, cursor)
	if got := strings.Count(periodicBody, `"reason":"engagement-changed"`); got > 1 {
		t.Fatalf("3 periodic reports emitted %d engagement invalidations, want throttled <= 1: %s", got, periodicBody)
	}

	// Pausing on client 1 (even right after periodic reports) must immediately
	// emit a Profile-scoped invalidation to another session on the same Profile,
	// and never to another Profile.
	cursor = cursorAfter()
	reportGraphQL(map[string]any{
		"mediaID":         "media-alpha",
		"positionSeconds": 50,
		"durationSeconds": 120,
		"finished":        false,
		"paused":          true,
	})
	pauseBody := collectFrom(authorizeRevealed, cursor)
	if !strings.Contains(pauseBody, "event: client.invalidate\n") || !strings.Contains(pauseBody, `"reason":"engagement-changed"`) {
		t.Fatalf("same-Profile session missed pause invalidation: %s", pauseBody)
	}
	if managedBody := collectFrom(authorizeManaged, cursor); strings.Contains(managedBody, "client.invalidate") {
		t.Fatalf("different Profile received pause invalidation: %s", managedBody)
	}

	// Finishing via /v1/media/{mediaID}/progress must also invalidate the same
	// Profile and stay invisible to other Profiles.
	cursor = cursorAfter()
	rec := v1DeviceRequest(t, srv, http.MethodPut, "/v1/media/media-alpha/progress", `{"positionSeconds":120,"durationSeconds":120,"finished":true}`, "", authorizeViewer)
	if rec.Code != http.StatusOK {
		t.Fatalf("v1 progress status = %d: %s", rec.Code, rec.Body.String())
	}
	finishBody := collectFrom(authorizeRevealed, cursor)
	if !strings.Contains(finishBody, "event: client.invalidate\n") || !strings.Contains(finishBody, `"reason":"engagement-changed"`) {
		t.Fatalf("same-Profile session missed finish invalidation: %s", finishBody)
	}
	if managedBody := collectFrom(authorizeManaged, cursor); strings.Contains(managedBody, "client.invalidate") {
		t.Fatalf("different Profile received finish invalidation: %s", managedBody)
	}
}
