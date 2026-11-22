package graph

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestSubscriptionNewDropsSealedLibraryEntries pins the CORE-2958 review: new
// entries of a followed Subscription leave the subscription-new Source as soon
// as their Library seals.
func TestSubscriptionNewDropsSealedLibraryEntries(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	added := time.Now().UTC().Add(-time.Hour)
	library, err := st.CreateLibrary(ctx, "Sealable Feeds", false, "feedkit", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	sub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: library.ID, FeedURL: "https://feeds.example.com/sealable", Root: "/media/sealable", Title: "Sealable Feed",
	}, added)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetFollow(ctx, "profile-default", sub.ID, true, added); err != nil {
		t.Fatal(err)
	}
	entries := make([]store.FeedCatalogEntry, 2)
	for i := range entries {
		entries[i] = store.FeedCatalogEntry{
			GUID:            fmt.Sprintf("sealable-%d", i),
			Title:           fmt.Sprintf("Entry %d", i),
			PublishedAt:     added.Add(time.Duration(i+1) * time.Minute),
			EnclosureURL:    fmt.Sprintf("https://cdn.example.com/sealable-%d.m4a", i),
			EnclosureType:   "audio/x-m4a",
			EnclosureLength: 1000,
			Position:        i,
		}
	}
	if _, err := st.UpsertSubscriptionCatalog(ctx, sub.ID, entries, added.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)
	mgr := libencrypt.NewManagerWithParams(newMemSecretStore(), libencrypt.TestArgon2Params)
	handler.SetLibEncrypt(mgr)
	if _, err := mgr.Provision(string(library.ID), libencrypt.UnsealModeAuto, ""); err != nil {
		t.Fatal(err)
	}
	const query = `query { sourceItems(kind: "subscription-new") { id } }`
	var open []SourceItem
	execGraph(t, handler, authorizeManager, query, nil, "sourceItems", &open)
	if len(open) != len(entries) {
		t.Fatalf("unsealed source = %+v, want %d entries", open, len(entries))
	}
	mgr.Seal(string(library.ID))
	var sealed []SourceItem
	execGraph(t, handler, authorizeManager, query, nil, "sourceItems", &sealed)
	if len(sealed) != 0 {
		t.Fatalf("sealed source = %+v, want none", sealed)
	}
}
