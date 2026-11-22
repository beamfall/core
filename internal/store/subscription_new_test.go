package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
)

// newEntriesFixture catalogues a first poll, has profile-default follow the
// Subscription after it, then catalogues two later entries. It returns the
// store, the Subscription and the entries the second poll created.
func newEntriesFixture(t *testing.T, libraryID LibraryID, route NewEntryRoute) (*Store, Subscription, []SubscriptionEntry) {
	t.Helper()
	ctx := context.Background()
	st := openSeeded(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	sub, err := st.AddSubscription(ctx, Subscription{
		LibraryID: libraryID,
		FeedURL:   "https://feeds.example.com/new.xml",
		Root:      "/media/local/incoming",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSubscriptionCatalog(ctx, sub.ID, catalogueEntries(3), now); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordSubscriptionPollSuccess(ctx, sub, "", "", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	followedAt := now.Add(time.Minute)
	if err := st.SetFollow(ctx, "profile-default", sub.ID, true, followedAt); err != nil {
		t.Fatal(err)
	}
	if route != NewEntryRouteNew {
		if err := st.SetFollowNewEntryRoute(ctx, "profile-default", sub.ID, route); err != nil {
			t.Fatal(err)
		}
	}
	later := catalogueEntries(5)
	for i := range later[:2] {
		later[i].GUID = later[i].GUID + "-new"
		later[i].PublishedAt = followedAt.Add(time.Duration(2-i) * time.Hour)
	}
	all, err := st.UpsertSubscriptionCatalog(ctx, sub.ID, append(later[:2:2], catalogueEntries(3)...), now.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var created []SubscriptionEntry
	for _, entry := range all {
		if entry.GUID == later[0].GUID || entry.GUID == later[1].GUID {
			created = append(created, entry)
		}
	}
	if len(created) != 2 {
		t.Fatalf("created entries = %d, want 2", len(created))
	}
	return st, sub, created
}

func subscriptionNewIDs(t *testing.T, st *Store, profileID ProfileID, filter LibraryFilter) []MediaID {
	t.Helper()
	items, err := st.SourceItems(context.Background(), filter, SourceQuery{Kind: "subscription-new", ProfileID: profileID})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]MediaID, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}

func TestSubscriptionNewIsPerProfileAndDropsPlayedEntries(t *testing.T) {
	st, _, created := newEntriesFixture(t, "library-local", NewEntryRouteNew)
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}

	got := subscriptionNewIDs(t, st, "profile-default", filter)
	if len(got) != 2 || got[0] != created[0].MediaID || got[1] != created[1].MediaID {
		t.Fatalf("follower sees %v, want newest-first %v, %v", got, created[0].MediaID, created[1].MediaID)
	}
	if other := subscriptionNewIDs(t, st, "profile-managed", filter); len(other) != 0 {
		t.Fatalf("non-follower sees %v, want none", other)
	}
	if none, err := st.SourceItems(context.Background(), filter, SourceQuery{Kind: "subscription-new"}); err != nil || len(none) != 0 {
		t.Fatalf("no-profile query = %v, %v; want empty", none, err)
	}

	if _, err := st.SetItemWatched(context.Background(), "profile-default", ItemTarget{MediaID: created[0].MediaID}, true, filter); err != nil {
		t.Fatal(err)
	}
	got = subscriptionNewIDs(t, st, "profile-default", filter)
	if len(got) != 1 || got[0] != created[1].MediaID {
		t.Fatalf("after mark played = %v, want only %v", got, created[1].MediaID)
	}
}

func TestSubscriptionNewHonoursLibraryAndRevealFunnels(t *testing.T) {
	st, _, created := newEntriesFixture(t, "library-archive", NewEntryRouteNew)
	if got := subscriptionNewIDs(t, st, "profile-default", LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}}); len(got) != 0 {
		t.Fatalf("restricted Library without reveal = %v, want none", got)
	}
	if got := subscriptionNewIDs(t, st, "profile-default", LibraryFilter{AllowedIDs: []LibraryID{"library-local"}, Revealed: true}); len(got) != 0 {
		t.Fatalf("Library outside the allow-list = %v, want none", got)
	}
	got := subscriptionNewIDs(t, st, "profile-default", LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true})
	if len(got) != len(created) {
		t.Fatalf("revealed = %v, want %d entries", got, len(created))
	}
}

func queueMediaIDsFor(t *testing.T, st *Store, profileID ProfileID) []MediaID {
	t.Helper()
	queue, err := st.PlayQueue(context.Background(), profileID, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]MediaID, len(queue.Entries))
	for i, entry := range queue.Entries {
		ids[i] = entry.Item.ID
	}
	return ids
}

func TestRouteNewSubscriptionEntriesAppendsToFollowerQueue(t *testing.T) {
	st, sub, created := newEntriesFixture(t, "library-local", NewEntryRouteQueueEnd)
	ctx := context.Background()
	if route, err := st.FollowNewEntryRoute(ctx, "profile-default", sub.ID); err != nil || route != NewEntryRouteQueueEnd {
		t.Fatalf("route = %q, %v; want queue-end", route, err)
	}
	if err := st.SetFollowNewEntryRoute(ctx, "profile-managed", sub.ID, NewEntryRouteQueueEnd); err != ErrNotFollowing {
		t.Fatalf("route without follow = %v, want ErrNotFollowing", err)
	}
	// Two passes, as after a poll that repeated: the watermark keeps each
	// entry to one Play Queue add.
	for range 2 {
		if err := st.RouteNewSubscriptionEntries(ctx, sub.ID, testQueueCertification); err != nil {
			t.Fatal(err)
		}
	}
	// Oldest first: created[1] was published before created[0].
	got := queueMediaIDsFor(t, st, "profile-default")
	if len(got) != 2 || got[0] != created[1].MediaID || got[1] != created[0].MediaID {
		t.Fatalf("queue = %v, want %v then %v", got, created[1].MediaID, created[0].MediaID)
	}
	if managed := queueMediaIDsFor(t, st, "profile-managed"); len(managed) != 0 {
		t.Fatalf("non-follower queue = %v, want empty", managed)
	}
}

func TestRouteNewSubscriptionEntriesReplaysAfterAnInterruptedPass(t *testing.T) {
	st, sub, created := newEntriesFixture(t, "library-local", NewEntryRouteQueueFront)
	ctx := context.Background()
	followers, err := st.queueRoutedFollowers(ctx, sub.ID)
	if err != nil || len(followers) != 1 {
		t.Fatalf("followers = %v, %v; want one", followers, err)
	}
	// A pass that read the follow before a re-route commits nothing.
	if _, err := st.eventsW.ExecContext(ctx, `UPDATE follow SET new_entry_route = 'queue-end'`); err != nil {
		t.Fatal(err)
	}
	if err := st.routeFollowerEntries(ctx, followers[0], testQueueCertification); !errors.Is(err, errFollowChanged) {
		t.Fatalf("stale pass = %v, want errFollowChanged", err)
	}
	if got := queueMediaIDsFor(t, st, "profile-default"); len(got) != 0 {
		t.Fatalf("stale pass queued %v, want nothing", got)
	}
	// The next poll's pass picks the entries up under the current route.
	if err := st.RouteNewSubscriptionEntries(ctx, sub.ID, testQueueCertification); err != nil {
		t.Fatal(err)
	}
	if got := queueMediaIDsFor(t, st, "profile-default"); len(got) != len(created) {
		t.Fatalf("replayed queue = %v, want %d entries", got, len(created))
	}
}

func TestRouteNewSubscriptionEntriesSkipsMutedAndPlayed(t *testing.T) {
	st, sub, created := newEntriesFixture(t, "library-local", NewEntryRouteQueueEnd)
	ctx := context.Background()
	if err := st.SetMute(ctx, "profile-default", MuteKindMedia, string(created[0].MediaID), false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetItemWatched(ctx, "profile-default", ItemTarget{MediaID: created[1].MediaID}, true, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.RouteNewSubscriptionEntries(ctx, sub.ID, testQueueCertification); err != nil {
		t.Fatal(err)
	}
	if got := queueMediaIDsFor(t, st, "profile-default"); len(got) != 0 {
		t.Fatalf("queue = %v, want muted and played entries left out", got)
	}
}

func TestSwitchingToAQueueRouteLeavesListedEntriesInTheSource(t *testing.T) {
	st, sub, created := newEntriesFixture(t, "library-local", NewEntryRouteNew)
	ctx := context.Background()
	if err := st.SetFollowNewEntryRoute(ctx, "profile-default", sub.ID, NewEntryRouteQueueEnd); err != nil {
		t.Fatal(err)
	}
	if err := st.RouteNewSubscriptionEntries(ctx, sub.ID, testQueueCertification); err != nil {
		t.Fatal(err)
	}
	if got := queueMediaIDsFor(t, st, "profile-default"); len(got) != 0 {
		t.Fatalf("queue = %v, want entries listed before the switch left out", got)
	}
	if got := subscriptionNewIDs(t, st, "profile-default", LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}); len(got) != len(created) {
		t.Fatalf("source = %v, want %d entries", got, len(created))
	}
}

func TestRepeatedFollowKeepsTheNewEntryBoundary(t *testing.T) {
	st, sub, created := newEntriesFixture(t, "library-local", NewEntryRouteNew)
	ctx := context.Background()
	if err := st.SetFollow(ctx, "profile-default", sub.ID, true, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if got := subscriptionNewIDs(t, st, "profile-default", LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}); len(got) != len(created) {
		t.Fatalf("after repeated follow = %v, want %d entries", got, len(created))
	}
}

func TestSubscriptionNewSpansManyFollowsAndHistoricalDates(t *testing.T) {
	st, _, created := newEntriesFixture(t, "library-local", NewEntryRouteNew)
	ctx := context.Background()
	// A follow that predates 1970 still bounds by date: negative publish
	// times are real dates, not the missing-date sentinel.
	if _, err := st.eventsW.ExecContext(ctx, `UPDATE follow SET followed_at_ms = ?`, time.Date(1950, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE subscription_entry SET published_at_ms = ? WHERE media_id = ?`,
		time.Date(1940, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(), created[0].MediaID); err != nil {
		t.Fatal(err)
	}
	// More follows than one query binds.
	for i := range 2 * newEntryFollowChunk {
		if _, err := st.eventsW.ExecContext(ctx, `INSERT INTO follow(profile_id, subscription_id, followed_at_ms) VALUES ('profile-default', ?, 0)`, fmt.Sprintf("sub-filler-%04d", i)); err != nil {
			t.Fatal(err)
		}
	}
	got := subscriptionNewIDs(t, st, "profile-default", LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if len(got) != 4 || slices.Contains(got, created[0].MediaID) {
		t.Fatalf("source = %v, want the 3 back-catalogue entries and %v, not the 1940 entry", got, created[1].MediaID)
	}
}

// TestPlayedMediaAfterCloseReturnsErrClosed pins the CORE-2959 review: the
// catalogue's played read holds the store lock and refuses a closed store.
func TestPlayedMediaAfterCloseReturnsErrClosed(t *testing.T) {
	st := openSeeded(t)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PlayedMedia(context.Background(), "profile-default", []MediaID{"media-1"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("PlayedMedia after Close err = %v, want ErrClosed", err)
	}
}
