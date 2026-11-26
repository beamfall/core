package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// CORE-0706 slice 3: exact feed keys, durable GUID collision review, and
// Encrypted Library Subscriptions refused rather than written to the shared
// catalog in plaintext.

func collisionFixture(t *testing.T) (*Store, Subscription, MediaID, time.Time) {
	t.Helper()
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	sub, err := st.AddSubscription(ctx, Subscription{
		LibraryID: "library-local", FeedURL: "https://feeds.example.com/exact.xml", Root: "/media/local/exact",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/local/exact/episode.mp3", Title: "Exact",
		Container: "mp3", Codec: "mp3", Class: MediaClassAudio, ContentFingerprint: "fp-exact-0001", DurationSecondsHint: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, sub, media.ID, now
}

// TestSubscriptionEntryGUIDRoundTripsExactly keeps the feed GUID byte for
// byte: an association names the poller's receipt, never a trimmed sibling.
func TestSubscriptionEntryGUIDRoundTripsExactly(t *testing.T) {
	st, sub, mediaID, now := collisionFixture(t)
	ctx := context.Background()
	const guid = "  tag:example.com,2026:Ep-é \t"
	if err := st.MarkSubscriptionEntrySeen(ctx, sub.ID, guid, "/media/local/exact/episode.mp3", now); err != nil {
		t.Fatal(err)
	}
	got, err := st.AssociateSubscriptionEntryMedia(ctx, sub.ID, guid, mediaID, now)
	if err != nil {
		t.Fatalf("AssociateSubscriptionEntryMedia: %v", err)
	}
	if got.GUID != guid {
		t.Fatalf("association GUID = %q, want %q", got.GUID, guid)
	}
	seen, err := st.SubscriptionSeenGUIDs(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || !seen[guid] {
		t.Fatalf("seen GUIDs = %v, want only the exact key %q", seen, guid)
	}
}

// TestSubscriptionEntryCollisionIsDurableReview keeps a same-GUID,
// different-file offer as one review row that counts repeats, and leaves the
// existing binding untouched.
func TestSubscriptionEntryCollisionIsDurableReview(t *testing.T) {
	st, sub, mediaID, now := collisionFixture(t)
	ctx := context.Background()
	const bound = "/media/local/exact/original.mp3"
	if err := st.MarkSubscriptionEntrySeen(ctx, sub.ID, "guid-collide", bound, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		_, err := st.AssociateSubscriptionEntryMedia(ctx, sub.ID, "guid-collide", mediaID, now.Add(time.Duration(i)*time.Minute))
		if !errors.Is(err, ErrSubscriptionEntryCollision) {
			t.Fatalf("collision #%d error = %v, want ErrSubscriptionEntryCollision", i, err)
		}
	}
	collisions, err := st.SubscriptionEntryCollisions(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(collisions) != 1 {
		t.Fatalf("collisions = %#v, want one review row", collisions)
	}
	c := collisions[0]
	if c.GUID != "guid-collide" || c.BoundPath != bound || c.IncomingPath != "/media/local/exact/episode.mp3" ||
		c.IncomingMediaID != mediaID || c.Observations != 2 || !c.LastSeenAt.After(c.FirstSeenAt) {
		t.Fatalf("collision = %#v", c)
	}
	var path string
	if err := st.catalogR.QueryRowContext(ctx, `SELECT path FROM subscription_seen_entry WHERE subscription_id = ? AND guid = ?`,
		sub.ID, "guid-collide").Scan(&path); err != nil || path != bound {
		t.Fatalf("bound path = %q, %v; want %q kept", path, err, bound)
	}
}

// TestEncryptedLibrarySubscriptionsRouteToOwnPlane verifies that unsealed
// Encrypted Library Subscriptions, catalogue entries, item-state imports,
// follows, and retention live on the Library's own plane with zero catalog/
// outbox leaks, while legacy pre-evacuation catalog rows and sealed planes stay
// refused.
func TestEncryptedLibrarySubscriptionsRouteToOwnPlane(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "encrypted-subscription")
	defer func() { _ = st.Close() }()
	now := time.Now().UTC()
	const feedURL = "https://feeds.example.com/private-feed-7c1f.xml"
	sub, err := st.AddSubscription(ctx, Subscription{LibraryID: media.LibraryID, FeedURL: feedURL, Root: "/enc/feeds"}, now)
	if err != nil {
		t.Fatalf("unsealed AddSubscription: %v", err)
	}
	gotSame, created, err := st.GetOrCreateSubscription(ctx, Subscription{LibraryID: media.LibraryID, FeedURL: feedURL, Root: "/enc/feeds"}, now)
	if err != nil || created || gotSame.ID != sub.ID {
		t.Fatalf("unsealed GetOrCreateSubscription = (%q, %v, %v), want existing %q", gotSame.ID, created, err, sub.ID)
	}
	if set, err := st.FillSubscriptionTitle(ctx, sub.ID, "Private Feed Title 7c1f"); err != nil || !set {
		t.Fatalf("FillSubscriptionTitle = %v, %v", set, err)
	}
	if err := st.SetSubscriptionDownloadPolicy(ctx, sub.ID, SubscriptionPolicy{Policy: DownloadPolicyEverything}); err != nil {
		t.Fatalf("SetSubscriptionDownloadPolicy: %v", err)
	}
	const entryGUID = "guid-enc-sub-7c1f"
	entries, err := st.UpsertSubscriptionCatalog(ctx, sub.ID, []FeedCatalogEntry{{
		GUID: entryGUID, Title: "Private Episode 7c1f", PublishedAt: now,
		EnclosureURL: "https://cdn.example.com/private-7c1f.mp3", EnclosureType: "audio/mpeg", Position: 0,
	}}, now)
	if err != nil || len(entries) != 1 {
		t.Fatalf("UpsertSubscriptionCatalog = %+v, %v", entries, err)
	}
	if err := st.MarkSubscriptionFeedEntrySeen(ctx, sub.ID, entryGUID, "/enc/feeds/private-7c1f.mp3", now, 0); err != nil {
		t.Fatalf("MarkSubscriptionFeedEntrySeen: %v", err)
	}
	if err := st.RecordItemStateImport(ctx, ItemStateImport{
		ProfileID: "profile-default", SubscriptionID: sub.ID, Source: "opml", ItemGUID: entryGUID, Played: true,
	}, now); err != nil {
		t.Fatalf("RecordItemStateImport: %v", err)
	}
	// Shared catalog and outbox carry zero rows for this Subscription or entry Media.
	var leaked int
	if err := st.catalogR.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM subscription WHERE library_id = ?)
		     + (SELECT COUNT(*) FROM subscription_seen_entry WHERE subscription_id = ?)
		     + (SELECT COUNT(*) FROM subscription_entry WHERE subscription_id = ?)
		     + (SELECT COUNT(*) FROM media WHERE id = ?)
		     + (SELECT COUNT(*) FROM media_external_id WHERE value = ?)
		     + (SELECT COUNT(*) FROM item_state_import WHERE subscription_id = ?)
	`, media.LibraryID, sub.ID, sub.ID, entries[0].MediaID, feedGUIDExternalID(entryGUID), sub.ID).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("catalog rows leaked for encrypted Subscription = %d, %v", leaked, err)
	}
	if n := identificationRowCount(t, st.eventsR, `SELECT COUNT(*) FROM event_outbox WHERE aggregate_id = ?`, entries[0].MediaID); n != 0 {
		t.Fatalf("outbox rows leaked for encrypted entry Media = %d, want 0", n)
	}

	// A legacy Subscription stranded in catalog.db before evacuation stays refused.
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO subscription(id, library_id, feed_url, root, added_at_ms) VALUES ('sub-legacy-enc', ?, 'https://legacy.example.com/f.xml', '/enc/feeds', 1)
	`, media.LibraryID); err != nil {
		t.Fatal(err)
	}
	id := SubscriptionID("sub-legacy-enc")
	const privateGUID = "guid-private-7c1f"
	checks := map[string]error{
		"MarkSubscriptionEntrySeen":     st.MarkSubscriptionEntrySeen(ctx, id, privateGUID, "/enc/feeds/a.mp3", now),
		"MarkSubscriptionFeedEntrySeen": st.MarkSubscriptionFeedEntrySeen(ctx, id, privateGUID, "/enc/feeds/a.mp3", now, 0),
		"AliasSubscriptionEntryGUID":    st.AliasSubscriptionEntryGUID(ctx, id, "guid-a", privateGUID),
		"PublishSubscriptionEntry": st.PublishSubscriptionEntry(ctx, id, privateGUID, func() (string, error) {
			return "/enc/feeds/a.mp3", nil
		}),
	}
	_, checks["AssociateSubscriptionEntryMedia"] = st.AssociateSubscriptionEntryMedia(ctx, id, privateGUID, media.ID, now)
	_, checks["FillSubscriptionTitle"] = st.FillSubscriptionTitle(ctx, id, "Legacy Private Title 7c1f")
	checks["RecordItemStateImport"] = st.RecordItemStateImport(ctx, ItemStateImport{ProfileID: "profile-default", SubscriptionID: id, Source: "opml", ItemGUID: privateGUID, Played: true}, now)
	for name, err := range checks {
		if !errors.Is(err, ErrEncryptedLibrarySubscription) {
			t.Errorf("%s error = %v, want ErrEncryptedLibrarySubscription", name, err)
		}
	}

	// When sealed, the encrypted Subscription becomes unreachable and new adds fail closed.
	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Subscription(ctx, sub.ID); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("sealed Subscription = %v, want ErrSubscriptionNotFound", err)
	}
	if _, err := st.AddSubscription(ctx, Subscription{LibraryID: media.LibraryID, FeedURL: "https://feeds.example.com/sealed.xml", Root: "/enc/feeds"}, now); !errors.Is(err, ErrEncryptedLibraryDBDisabled) {
		t.Fatalf("sealed AddSubscription = %v, want ErrEncryptedLibraryDBDisabled", err)
	}
}

// TestItemStateImportGUIDIsExact replays an imported item state only through
// the receipt of its exact GUID: a whitespace sibling never applies it.
func TestItemStateImportGUIDIsExact(t *testing.T) {
	st, sub, mediaID, now := collisionFixture(t)
	ctx := context.Background()
	const guid = " item-exact\t"
	if err := st.MarkSubscriptionEntrySeen(ctx, sub.ID, "item-exact", "/media/local/exact/episode.mp3", now); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordItemStateImport(ctx, ItemStateImport{ProfileID: "profile-default", SubscriptionID: sub.ID, Source: "opml", ItemGUID: guid, Played: true}, now); err != nil {
		t.Fatal(err)
	}
	if pending, err := st.pendingItemStateImports(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("a trimmed sibling receipt resolved the import: %v, %v", pending, err)
	}
	if err := st.MarkSubscriptionEntrySeen(ctx, sub.ID, guid, "/media/local/exact/episode.mp3", now); err != nil {
		t.Fatal(err)
	}
	pending, err := st.pendingItemStateImports(ctx)
	if err != nil || len(pending) != 1 || pending[0].ItemGUID != guid || pending[0].MediaID != mediaID {
		t.Fatalf("pending = %v, %v, want the exact GUID bound to %s", pending, err, mediaID)
	}
}

// TestFeedExternalIDsRoundTripExactly stores and finds a feed GUID External ID
// byte for byte, while plugin identifiers keep their trimmed form.
func TestFeedExternalIDsRoundTripExactly(t *testing.T) {
	st, _, mediaID, _ := collisionFixture(t)
	ctx := context.Background()
	const guid = " guid-exact\t"
	if err := st.AddMediaExternalID(ctx, mediaID, subscriptionFeedGUIDScheme, guid); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaExternalID(ctx, mediaID, "plugin-ref", "  ref-1 "); err != nil {
		t.Fatal(err)
	}
	ids, err := st.MediaExternalIDs(ctx, mediaID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, id := range ids {
		got[id.Scheme] = id.Value
	}
	if got[subscriptionFeedGUIDScheme] != guid || got["plugin-ref"] != "ref-1" {
		t.Fatalf("external IDs = %q", got)
	}
	if found, err := st.MediaIDsByExternalID(ctx, subscriptionFeedGUIDScheme, guid); err != nil || len(found) != 1 || found[0] != mediaID {
		t.Fatalf("exact lookup = %v, %v", found, err)
	}
	if found, err := st.MediaIDsByExternalID(ctx, subscriptionFeedGUIDScheme, "guid-exact"); err != nil || len(found) != 0 {
		t.Fatalf("a trimmed sibling matched the exact feed GUID: %v, %v", found, err)
	}
}

// TestEncryptedSubscriptionRemoveCascadesChildRows verifies unsubscribing on an
// Encrypted Library plane removes child seen/entry/collision rows in the same
// transaction despite the absence of catalog's ON DELETE CASCADE constraints.
func TestEncryptedSubscriptionRemoveCascadesChildRows(t *testing.T) {
	ctx := t.Context()
	st, media := seededEncryptedMedia(t, "encrypted-unsubscribe-cascade")
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 11, 26, 8, 30, 0, 0, time.UTC)

	sub, err := st.AddSubscription(ctx, Subscription{
		LibraryID: media.LibraryID, FeedURL: "https://feeds.example.com/remove-enc.xml", Root: "/enc/remove",
	}, now)
	if err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}
	const guid = "enc-remove-guid"
	if err := st.MarkSubscriptionFeedEntrySeen(ctx, sub.ID, guid, "/enc/remove/original.mp3", now, 0); err != nil {
		t.Fatalf("MarkSubscriptionFeedEntrySeen: %v", err)
	}
	if _, err := st.UpsertSubscriptionCatalog(ctx, sub.ID, []FeedCatalogEntry{{
		GUID: guid, Title: "Encrypted Episode", EnclosureURL: "https://feeds.example.com/enc-remove.mp3",
	}}, now); err != nil {
		t.Fatalf("UpsertSubscriptionCatalog: %v", err)
	}
	fileMedia, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: media.LibraryID, Path: "/enc/remove/second.mp3", Title: "Second Copy", Class: MediaClassAudio,
		Container: "mp3", Codec: "mp3", ContentFingerprint: "sha256:enc-remove-second",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	if _, err := st.AssociateSubscriptionEntryMedia(ctx, sub.ID, guid, fileMedia.ID, now); !errors.Is(err, ErrSubscriptionEntryCollision) {
		t.Fatalf("AssociateSubscriptionEntryMedia = %v, want ErrSubscriptionEntryCollision", err)
	}
	if err := st.RecordItemStateImport(ctx, ItemStateImport{
		ProfileID: "profile-default", SubscriptionID: sub.ID, Source: "opml", ItemGUID: guid, Played: true,
	}, now); err != nil {
		t.Fatalf("RecordItemStateImport: %v", err)
	}

	if _, err := st.RemoveSubscription(ctx, sub.ID); err != nil {
		t.Fatalf("RemoveSubscription: %v", err)
	}
	plane, err := st.catalogPlaneForLibraryLocked(ctx, media.LibraryID)
	if err != nil {
		t.Fatalf("catalogPlaneForLibraryLocked: %v", err)
	}
	for _, table := range []string{"subscription", "subscription_seen_entry", "subscription_entry", "subscription_entry_collision", "item_state_import"} {
		col := "subscription_id"
		if table == "subscription" {
			col = "id"
		}
		var n int
		if err := plane.reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE `+col+` = ?`, sub.ID).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s rows remaining after unsubscribe = %d, %v", table, n, err)
		}
	}
	if _, _, found, err := st.SubscriptionEntryForMedia(ctx, fileMedia.ID); err != nil || found {
		t.Fatalf("SubscriptionEntryForMedia after unsubscribe = %v, %v; want false, nil", found, err)
	}
}
