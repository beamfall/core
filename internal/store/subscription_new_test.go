package store

import (
	"context"
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

func TestRouteNewSubscriptionEntriesAppendsToFollowerQueue(t *testing.T) {
	st, sub, created := newEntriesFixture(t, "library-local", NewEntryRouteQueueEnd)
	ctx := context.Background()
	if route, err := st.FollowNewEntryRoute(ctx, "profile-default", sub.ID); err != nil || route != NewEntryRouteQueueEnd {
		t.Fatalf("route = %q, %v; want queue-end", route, err)
	}
	if err := st.SetFollowNewEntryRoute(ctx, "profile-managed", sub.ID, NewEntryRouteQueueEnd); err != ErrNotFollowing {
		t.Fatalf("route without follow = %v, want ErrNotFollowing", err)
	}
	if err := st.RouteNewSubscriptionEntries(ctx, sub.ID, created, testQueueCertification); err != nil {
		t.Fatal(err)
	}
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	queue, err := st.PlayQueue(ctx, "profile-default", filter)
	if err != nil {
		t.Fatal(err)
	}
	// Oldest first: created[1] was published before created[0].
	if len(queue.Entries) != 2 || queue.Entries[0].Item.ID != created[1].MediaID || queue.Entries[1].Item.ID != created[0].MediaID {
		t.Fatalf("queue = %+v, want %v then %v", queue.Entries, created[1].MediaID, created[0].MediaID)
	}
	managed, err := st.PlayQueue(ctx, "profile-managed", filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(managed.Entries) != 0 {
		t.Fatalf("non-follower queue = %+v, want empty", managed.Entries)
	}
}
