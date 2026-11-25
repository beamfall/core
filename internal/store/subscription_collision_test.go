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

// TestEncryptedLibrarySubscriptionsAreRefused refuses new Subscriptions and
// every entry or title write for a legacy one in an Encrypted Library, so no
// feed URL, GUID, path or title of it reaches the shared catalog.
func TestEncryptedLibrarySubscriptionsAreRefused(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "encrypted-subscription")
	defer func() { _ = st.Close() }()
	now := time.Now().UTC()
	const feedURL = "https://feeds.example.com/private-feed-7c1f.xml"
	if _, err := st.AddSubscription(ctx, Subscription{LibraryID: media.LibraryID, FeedURL: feedURL, Root: "/enc/feeds"}, now); !errors.Is(err, ErrEncryptedLibrarySubscription) {
		t.Fatalf("AddSubscription error = %v, want ErrEncryptedLibrarySubscription", err)
	}
	if _, _, err := st.GetOrCreateSubscription(ctx, Subscription{LibraryID: media.LibraryID, FeedURL: feedURL, Root: "/enc/feeds"}, now); !errors.Is(err, ErrEncryptedLibrarySubscription) {
		t.Fatalf("GetOrCreateSubscription error = %v, want ErrEncryptedLibrarySubscription", err)
	}
	var n int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM subscription WHERE feed_url = ?`, feedURL).Scan(&n); err != nil || n != 0 {
		t.Fatalf("refused subscription rows = %d, %v", n, err)
	}

	// A Subscription an Encrypted Library left in catalog.db before the refusal.
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
	_, checks["FillSubscriptionTitle"] = st.FillSubscriptionTitle(ctx, id, "Private Feed Title 7c1f")
	for name, err := range checks {
		if !errors.Is(err, ErrEncryptedLibrarySubscription) {
			t.Errorf("%s error = %v, want ErrEncryptedLibrarySubscription", name, err)
		}
	}
	var leaked int
	if err := st.catalogR.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM subscription_seen_entry WHERE subscription_id = ?)
		     + (SELECT COUNT(*) FROM subscription WHERE id = ? AND title <> '')
		     + (SELECT COUNT(*) FROM media_external_id WHERE value = ?)
	`, id, id, privateGUID).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("catalog rows written for the encrypted Subscription = %d, %v", leaked, err)
	}
}
