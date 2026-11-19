package server

import (
	"context"
	"encoding/json"
	"net/http"
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
