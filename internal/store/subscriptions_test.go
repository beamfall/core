package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionRoundTrip(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	added, err := st.AddSubscription(ctx, Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/daily?token=secret-token",
		Root:      "/media/local/incoming",
		Title:     "Daily Feed",
	}, now)
	if err != nil {
		t.Fatalf("AddSubscription returned error: %v", err)
	}
	if added.ID == "" || !strings.HasPrefix(string(added.ID), "subscription-") {
		t.Fatalf("added.ID = %q, want subscription- prefix", added.ID)
	}
	if !added.AddedAt.Equal(now) {
		t.Fatalf("added.AddedAt = %v, want %v", added.AddedAt, now)
	}
	if !added.LastPolledAt.IsZero() {
		t.Fatalf("added.LastPolledAt = %v, want zero", added.LastPolledAt)
	}

	listed, err := st.Subscriptions(ctx, "library-local")
	if err != nil {
		t.Fatalf("Subscriptions returned error: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("len(listed) = %d, want 1", len(listed))
	}
	if listed[0].ID != added.ID || listed[0].FeedURL != added.FeedURL || listed[0].Root != "/media/local/incoming" || listed[0].Title != "Daily Feed" {
		t.Fatalf("listed[0] = %+v", listed[0])
	}

	got, err := st.Subscription(ctx, added.ID)
	if err != nil {
		t.Fatalf("Subscription returned error: %v", err)
	}
	if got.LibraryID != "library-local" {
		t.Fatalf("got.LibraryID = %q", got.LibraryID)
	}

	all, err := st.AllSubscriptions(ctx)
	if err != nil {
		t.Fatalf("AllSubscriptions returned error: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("len(all) = %d, want 1", len(all))
	}

	removed, err := st.RemoveSubscription(ctx, added.ID)
	if err != nil {
		t.Fatalf("RemoveSubscription returned error: %v", err)
	}
	if removed.ID != added.ID {
		t.Fatalf("removed.ID = %q, want %q", removed.ID, added.ID)
	}
	if _, err := st.Subscription(ctx, added.ID); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("Subscription after remove = %v, want ErrSubscriptionNotFound", err)
	}
	if _, err := st.RemoveSubscription(ctx, added.ID); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("second remove = %v, want ErrSubscriptionNotFound", err)
	}
}

func TestSubscriptionRetentionTrashDisabledRequiresConfirmation(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, TrashDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	trashed, skipped, err := st.ApplySubscriptionRetentionPolicies(ctx)
	if !errors.Is(err, ErrTrashDisabledConfirmationRequired) || !skipped || trashed != 0 {
		t.Fatalf("ApplySubscriptionRetentionPolicies trash disabled trashed=%d skipped=%v err=%v", trashed, skipped, err)
	}
	if got := st.TrashDisabledSweepSkips(); got != 1 {
		t.Fatalf("TrashDisabledSweepSkips = %d, want 1", got)
	}
}

func TestAddSubscriptionValidation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()

	cases := []struct {
		name         string
		subscription Subscription
	}{
		{"missing library", Subscription{FeedURL: "https://feeds.example.com/a", Root: "/media"}},
		{"missing root", Subscription{LibraryID: "library-local", FeedURL: "https://feeds.example.com/a"}},
		{"missing feed URL", Subscription{LibraryID: "library-local", Root: "/media"}},
		{"relative feed URL", Subscription{LibraryID: "library-local", FeedURL: "/feed.xml", Root: "/media"}},
		{"non-http scheme", Subscription{LibraryID: "library-local", FeedURL: "file:///etc/passwd", Root: "/media"}},
		{"unknown library", Subscription{LibraryID: "library-nope", FeedURL: "https://feeds.example.com/a", Root: "/media"}},
	}
	for _, tc := range cases {
		if _, err := st.AddSubscription(ctx, tc.subscription, now); err == nil {
			t.Errorf("%s: AddSubscription succeeded, want error", tc.name)
		} else if strings.Contains(err.Error(), "feeds.example.com") || strings.Contains(err.Error(), "passwd") {
			// Feed URLs can carry credentials; validation errors must never
			// echo them.
			t.Errorf("%s: error echoes the feed URL: %v", tc.name, err)
		}
	}

	// Duplicate (library, feed URL) refused — and the error stays generic.
	if _, err := st.AddSubscription(ctx, Subscription{
		LibraryID: "library-local", FeedURL: "https://feeds.example.com/dup?token=hush", Root: "/media",
	}, now); err != nil {
		t.Fatalf("first add returned error: %v", err)
	}
	_, err := st.AddSubscription(ctx, Subscription{
		LibraryID: "library-local", FeedURL: "https://feeds.example.com/dup?token=hush", Root: "/media",
	}, now)
	if err == nil {
		t.Fatal("duplicate add succeeded, want error")
	}
	if strings.Contains(err.Error(), "hush") {
		t.Fatalf("duplicate error echoes the feed URL: %v", err)
	}
}

func TestAddSubscriptionDefaultsRootFromFirstLibraryRoot(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	base := t.TempDir()
	first := filepath.Join(base, "a")
	second := filepath.Join(base, "b")
	if err := st.SetLibraryRoots(ctx, "library-local", []string{second, first}); err != nil {
		t.Fatal(err)
	}

	added, err := st.AddSubscription(ctx, Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/default-root",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("AddSubscription returned error: %v", err)
	}
	if added.Root != first {
		t.Fatalf("Root = %q, want first library root %q", added.Root, first)
	}

	stored, err := st.Subscription(ctx, added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Root != first {
		t.Fatalf("stored Root = %q, want %q", stored.Root, first)
	}
}

func TestAddSubscriptionExplicitRootWins(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	libraryRoot := filepath.Join(t.TempDir(), "library")
	if err := st.SetLibraryRoots(ctx, "library-local", []string{libraryRoot}); err != nil {
		t.Fatal(err)
	}

	added, err := st.AddSubscription(ctx, Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/explicit-root",
		Root:      "/media/explicit",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("AddSubscription returned error: %v", err)
	}
	if added.Root != "/media/explicit" {
		t.Fatalf("Root = %q, want explicit root", added.Root)
	}
}

func TestGetOrCreateSubscriptionIdempotent(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

	created, inserted, err := st.GetOrCreateSubscription(ctx, Subscription{
		LibraryID: "library-local",
		FeedURL:   " https://feeds.example.com/import.xml?token=secret ",
		Root:      "/media/local/imported",
		Title:     "Imported Feed",
	}, now)
	if err != nil {
		t.Fatalf("GetOrCreateSubscription create returned error: %v", err)
	}
	if !inserted {
		t.Fatal("inserted = false, want true for first import")
	}

	got, inserted, err := st.GetOrCreateSubscription(ctx, Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/import.xml?token=secret",
		Root:      "/media/local/ignored",
		Title:     "Ignored Rename",
	}, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("GetOrCreateSubscription existing returned error: %v", err)
	}
	if inserted {
		t.Fatal("inserted = true, want false for existing import")
	}
	if got.ID != created.ID {
		t.Fatalf("existing ID = %q, want %q", got.ID, created.ID)
	}
	if got.Title != "Imported Feed" || got.Root != "/media/local/imported" {
		t.Fatalf("existing row was overwritten: %+v", got)
	}

	listed, err := st.Subscriptions(ctx, "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 {
		t.Fatalf("subscriptions = %d, want one idempotent row", len(listed))
	}
}

func TestSubscriptionPollBookkeeping(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	added, err := st.AddSubscription(ctx, Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/daily",
		Root:      "/media/local/incoming",
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	polled := now.Add(time.Hour)
	if err := st.MarkSubscriptionPolled(ctx, added.ID, `W/"abc"`, "Wed, 11 Jun 2026 12:00:00 GMT", polled); err != nil {
		t.Fatalf("MarkSubscriptionPolled returned error: %v", err)
	}
	got, err := st.Subscription(ctx, added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastPolledAt.Equal(polled) || got.ETag != `W/"abc"` || got.LastModified != "Wed, 11 Jun 2026 12:00:00 GMT" {
		t.Fatalf("after poll: %+v", got)
	}
	if err := st.MarkSubscriptionPolled(ctx, "subscription-missing", "", "", polled); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("MarkSubscriptionPolled missing = %v, want ErrSubscriptionNotFound", err)
	}

	// Seen-entry GUID bookkeeping: insert, idempotent re-insert, read back.
	for _, entry := range []struct {
		guid string
		path string
	}{
		{"guid-1", "/media/local/incoming/one.mp3"},
		{"guid-2", "/media/local/incoming/two.mp3"},
		{"guid-1", "/media/local/incoming/ignored.mp3"},
	} {
		if err := st.MarkSubscriptionEntrySeen(ctx, added.ID, entry.guid, entry.path, polled); err != nil {
			t.Fatalf("MarkSubscriptionEntrySeen(%s) returned error: %v", entry.guid, err)
		}
	}
	seen, err := st.SubscriptionSeenGUIDs(ctx, added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || !seen["guid-1"] || !seen["guid-2"] {
		t.Fatalf("seen = %v", seen)
	}
	var storedPath string
	if err := st.catalogR.QueryRowContext(ctx, `
		SELECT path FROM subscription_seen_entry WHERE subscription_id = ? AND guid = ?
	`, added.ID, "guid-1").Scan(&storedPath); err != nil {
		t.Fatal(err)
	}
	if storedPath != "/media/local/incoming/one.mp3" {
		t.Fatalf("stored path = %q, want first downloaded path", storedPath)
	}

	// Removing the Subscription cascades the seen entries away.
	if _, err := st.RemoveSubscription(ctx, added.ID); err != nil {
		t.Fatal(err)
	}
	seen, err = st.SubscriptionSeenGUIDs(ctx, added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 0 {
		t.Fatalf("seen after remove = %v, want empty", seen)
	}
}

func TestItemStateImportReplaysAfterSeenEntryReconciliation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	sub, err := st.AddSubscription(ctx, Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/state.xml?token=hush",
		Root:      "/media/local/incoming",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordItemStateImport(ctx, ItemStateImport{
		ProfileID:         "profile-default",
		SubscriptionID:    sub.ID,
		Source:            "import:podcasts",
		ItemGUID:          "guid-state",
		HasProgress:       true,
		Progress:          0.75,
		Starred:           true,
		ImportRunID:       "run-1",
		ImportImporterID:  "podcasts-importer",
		ImportMatchMethod: "path",
	}, now); err != nil {
		t.Fatalf("RecordItemStateImport returned error: %v", err)
	}

	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:           "library-local",
		Path:                "/media/local/incoming/state.mp3",
		Title:               "State",
		Container:           "mp3",
		Codec:               "mp3",
		Class:               MediaClassAudio,
		ContentFingerprint:  "fp-item-state-0001",
		DurationSecondsHint: 120,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile returned error: %v", err)
	}
	if err := st.MarkSubscriptionEntrySeen(ctx, sub.ID, "guid-state", "/media/local/incoming/state.mp3", now); err != nil {
		t.Fatalf("MarkSubscriptionEntrySeen returned error: %v", err)
	}
	if resolved, skipped, err := st.ReconcileSubscriptionSeenEntries(ctx); err != nil || resolved != 1 || skipped != 0 {
		t.Fatalf("ReconcileSubscriptionSeenEntries = resolved %d skipped %d err %v, want 1/0/nil", resolved, skipped, err)
	}
	progress, ok, err := st.PlaybackProgressFor(ctx, "profile-default", media.ID)
	if err != nil || !ok {
		t.Fatalf("PlaybackProgressFor: ok=%v err=%v", ok, err)
	}
	if progress.PositionSeconds != 90 || progress.DurationSeconds != 120 || progress.Provenance != "import:podcasts" {
		t.Fatalf("progress = %+v, want 90/120 import provenance", progress)
	}
	if progress.ImportRunID != "run-1" || progress.ImportImporterID != "podcasts-importer" || progress.ImportMatchMethod != "path" {
		t.Fatalf("progress run-scoped provenance = %+v, want run-1/podcasts-importer/path", progress)
	}
	favorite, err := st.MediaFavorited(ctx, "profile-default", media.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !favorite {
		t.Fatal("starred item was not imported as a favorite")
	}
	firstUpdated := progress.UpdatedAt
	if resolved, skipped, err := st.ReconcileSubscriptionSeenEntries(ctx); err != nil || resolved != 0 || skipped != 0 {
		t.Fatalf("second reconcile = resolved %d skipped %d err %v, want 0/0/nil", resolved, skipped, err)
	}
	progress, _, err = st.PlaybackProgressFor(ctx, "profile-default", media.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !progress.UpdatedAt.Equal(firstUpdated) {
		t.Fatalf("replay refreshed progress timestamp: %v -> %v", firstUpdated, progress.UpdatedAt)
	}
}

func TestItemStateImportDoesNotUseGUIDOnlyMatchAcrossFeeds(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC)
	subA, err := st.AddSubscription(ctx, Subscription{LibraryID: "library-local", FeedURL: "https://feeds.example.com/a.xml", Root: "/media/local/a"}, now)
	if err != nil {
		t.Fatal(err)
	}
	subB, err := st.AddSubscription(ctx, Subscription{LibraryID: "library-local", FeedURL: "https://feeds.example.com/b.xml", Root: "/media/local/b"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordItemStateImport(ctx, ItemStateImport{
		ProfileID: "profile-default", SubscriptionID: subA.ID, Source: "import:podcasts",
		ItemGUID: "shared-guid", Played: true,
	}, now); err != nil {
		t.Fatal(err)
	}
	mediaB, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/local/b/shared.mp3", Title: "Wrong Feed",
		Container: "mp3", Codec: "mp3", Class: MediaClassAudio, ContentFingerprint: "fp-item-state-0002", DurationSecondsHint: 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSubscriptionEntrySeen(ctx, subB.ID, "shared-guid", "/media/local/b/shared.mp3", now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ReconcileSubscriptionSeenEntries(ctx); err != nil {
		t.Fatal(err)
	}
	if progress, ok, err := st.PlaybackProgressFor(ctx, "profile-default", mediaB.ID); err != nil || ok {
		t.Fatalf("GUID-only replay applied to wrong feed: progress=%+v ok=%v err=%v", progress, ok, err)
	}
}

func TestMIG3DRejectsArchivedGUIDOnlyFeedIdentityBackfill(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC)
	sub, err := st.AddSubscription(ctx, Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/archive.xml",
		Root:      "/media/local/archive",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordItemStateImport(ctx, ItemStateImport{
		ProfileID: "profile-default", SubscriptionID: sub.ID, Source: "import:podcasts",
		ItemGUID: "archive-guid", Played: true,
	}, now); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/local/archive/episode.mp3", Title: "Archived Episode",
		Container: "mp3", Codec: "mp3", Class: MediaClassAudio, ContentFingerprint: "fp-item-state-0003", DurationSecondsHint: 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaExternalID(ctx, media.ID, "fixture-guid", "archive-guid"); err != nil {
		t.Fatal(err)
	}
	if resolved, skipped, err := st.ReconcileSubscriptionSeenEntries(ctx); err != nil || resolved != 0 || skipped != 0 {
		t.Fatalf("GUID-only archive reconcile = resolved %d skipped %d err %v, want 0/0/nil", resolved, skipped, err)
	}
	if _, err := st.ReplayItemStateImports(ctx, now); err != nil {
		t.Fatal(err)
	}
	if progress, ok, err := st.PlaybackProgressFor(ctx, "profile-default", media.ID); err != nil || ok {
		t.Fatalf("MIG-3D archived GUID-only media received item-state: progress=%+v ok=%v err=%v", progress, ok, err)
	}
	var pending int
	if err := st.catalogR.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM item_state_import
		WHERE subscription_id = ? AND item_guid = 'archive-guid' AND applied_at_ms = 0
	`, sub.ID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("pending MIG-3D ledger rows = %d, want 1", pending)
	}
	association, err := st.AssociateSubscriptionEntryMedia(ctx, sub.ID, "archive-guid", media.ID, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("AssociateSubscriptionEntryMedia returned error: %v", err)
	}
	if association.Path != "/media/local/archive/episode.mp3" || association.ReplayedItemStateSweep != 1 {
		t.Fatalf("association = %+v, want path and one replayed sweep row", association)
	}
	progress, ok, err := st.PlaybackProgressFor(ctx, "profile-default", media.ID)
	if err != nil || !ok || !progress.Watched || progress.Provenance != "import:podcasts" {
		t.Fatalf("associated item-state progress = %+v ok=%v err=%v, want watched import", progress, ok, err)
	}
}

func TestAssociateSubscriptionEntryMediaRejectsUnsafeTargets(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 13, 30, 0, 0, time.UTC)
	sub, err := st.AddSubscription(ctx, Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/safe.xml",
		Root:      "/media/local/safe",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	local, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/local/safe/episode.mp3", Title: "Safe",
		Container: "mp3", Codec: "mp3", Class: MediaClassAudio, ContentFingerprint: "fp-assoc-safe-0001", DurationSecondsHint: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	outside, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/local/elsewhere/episode.mp3", Title: "Outside",
		Container: "mp3", Codec: "mp3", Class: MediaClassAudio, ContentFingerprint: "fp-assoc-safe-0002", DurationSecondsHint: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	archive, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-archive", Path: "/media/local/safe/archive.mp3", Title: "Wrong Library",
		Container: "mp3", Codec: "mp3", Class: MediaClassAudio, ContentFingerprint: "fp-assoc-safe-0003", DurationSecondsHint: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AssociateSubscriptionEntryMedia(ctx, sub.ID, "", local.ID, now); err == nil {
		t.Fatal("blank GUID association succeeded")
	}
	if _, err := st.AssociateSubscriptionEntryMedia(ctx, sub.ID, "outside", outside.ID, now); err == nil {
		t.Fatal("outside-root media association succeeded")
	}
	if _, err := st.AssociateSubscriptionEntryMedia(ctx, sub.ID, "wrong-library", archive.ID, now); err == nil {
		t.Fatal("cross-library media association succeeded")
	}
	if err := st.MarkSubscriptionEntrySeen(ctx, sub.ID, "conflict", "/media/local/safe/other.mp3", now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AssociateSubscriptionEntryMedia(ctx, sub.ID, "conflict", local.ID, now); err == nil {
		t.Fatal("conflicting existing path association succeeded")
	}
}

func TestSubscriptionRetentionUsesFeedScopedSeenPath(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 14, 0, 0, 0, time.UTC)
	keep := 1
	subA, err := st.AddSubscription(ctx, Subscription{
		LibraryID: "library-local", FeedURL: "https://feeds.example.com/a.xml", Root: "/media/local/a",
		RetentionKind: RetentionKeepLastN, RetentionN: &keep,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	subB, err := st.AddSubscription(ctx, Subscription{
		LibraryID: "library-local", FeedURL: "https://feeds.example.com/b.xml", Root: "/media/local/b",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	oldA, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/local/a/shared.mp3", Title: "A Old",
		Container: "mp3", Codec: "mp3", Class: MediaClassAudio, ContentFingerprint: "fp-retention-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	newA, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/local/a/new.mp3", Title: "A New",
		Container: "mp3", Codec: "mp3", Class: MediaClassAudio, ContentFingerprint: "fp-retention-0002",
	})
	if err != nil {
		t.Fatal(err)
	}
	wrongFeed, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/local/b/shared.mp3", Title: "B Shared",
		Container: "mp3", Codec: "mp3", Class: MediaClassAudio, ContentFingerprint: "fp-retention-0003",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSubscriptionEntrySeen(ctx, subA.ID, "shared-guid", "/media/local/a/shared.mp3", now); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSubscriptionEntrySeen(ctx, subA.ID, "new-guid", "/media/local/a/new.mp3", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSubscriptionEntrySeen(ctx, subB.ID, "shared-guid", "/media/local/b/shared.mp3", now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ReconcileSubscriptionSeenEntries(ctx); err != nil {
		t.Fatal(err)
	}
	trashed, skipped, err := st.ApplySubscriptionRetentionPolicies(ctx)
	if err != nil || skipped {
		t.Fatalf("ApplySubscriptionRetentionPolicies = trashed %d skipped %v err %v", trashed, skipped, err)
	}
	if trashed != 1 {
		t.Fatalf("trashed = %d, want only the old A item", trashed)
	}
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	if _, err := st.Media(ctx, oldA.ID, filter); err == nil {
		t.Fatal("old A media still visible after keep-last retention")
	}
	if _, err := st.Media(ctx, newA.ID, filter); err != nil {
		t.Fatalf("new A media missing after retention: %v", err)
	}
	if _, err := st.Media(ctx, wrongFeed.ID, filter); err != nil {
		t.Fatalf("duplicate-GUID wrong-feed media was trashed: %v", err)
	}
}

func TestMediaExternalIDsRoundTrip(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	if err := st.AddMediaExternalID(ctx, "media-alpha", "fixture-guid", "guid-1"); err != nil {
		t.Fatalf("AddMediaExternalID returned error: %v", err)
	}
	if err := st.AddMediaExternalID(ctx, "media-alpha", "fixture-guid", "guid-1"); err != nil {
		t.Fatalf("idempotent AddMediaExternalID returned error: %v", err)
	}
	if err := st.AddMediaExternalID(ctx, "media-archive", "fixture-guid", "guid-1"); err != nil {
		t.Fatalf("duplicate external id on another Media returned error: %v", err)
	}
	if err := st.AddMediaExternalID(ctx, "media-alpha", "musicbrainz.recording", "abc"); err != nil {
		t.Fatalf("dotted scheme returned error: %v", err)
	}

	ids, err := st.MediaIDsByExternalID(ctx, "fixture-guid", "guid-1")
	if err != nil {
		t.Fatal(err)
	}
	if !equalMediaIDs(ids, []MediaID{"media-alpha", "media-archive"}) {
		t.Fatalf("MediaIDsByExternalID = %v, want both media ids", ids)
	}

	external, err := st.MediaExternalIDs(ctx, "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(external) != 2 || external[0].Scheme != "fixture-guid" || external[1].Scheme != "musicbrainz.recording" {
		t.Fatalf("MediaExternalIDs = %#v, want sorted schemes", external)
	}
}

func TestMediaExternalIDValidation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	cases := []struct {
		name   string
		scheme string
		value  string
	}{
		{"missing scheme", "", "guid"},
		{"uppercase scheme", "Podcast-Guid", "guid"},
		{"underscore scheme", "podcast_guid", "guid"},
		{"missing value", "fixture-guid", ""},
	}
	for _, tc := range cases {
		if err := st.AddMediaExternalID(ctx, "media-alpha", tc.scheme, tc.value); err == nil {
			t.Errorf("%s: AddMediaExternalID succeeded, want validation error", tc.name)
		}
	}
}

func TestSubscriptionFollowAdultOnlyAndExistingTarget(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	sub, err := st.AddSubscription(ctx, Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/follow",
		Root:      "/media/local/follow",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	if err := st.SetFollow(ctx, "profile-default", sub.ID, true, time.Now().UTC()); err != nil {
		t.Fatalf("SetFollow adult returned error: %v", err)
	}
	followed, err := st.SubscriptionFollowed(ctx, "profile-default", sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !followed {
		t.Fatal("SubscriptionFollowed = false, want true")
	}
	if err := st.SetFollow(ctx, "profile-managed", sub.ID, true, time.Now().UTC()); err == nil {
		t.Fatal("managed profile followed subscription, want validation error")
	}
	if err := st.SetFollow(ctx, "profile-default", "subscription-missing", true, time.Now().UTC()); err == nil {
		t.Fatal("followed missing subscription, want validation error")
	}
	if err := st.SetFollow(ctx, "profile-default", sub.ID, false, time.Now().UTC()); err != nil {
		t.Fatalf("unfollow returned error: %v", err)
	}
	followed, err = st.SubscriptionFollowed(ctx, "profile-default", sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if followed {
		t.Fatal("SubscriptionFollowed = true after unfollow")
	}
}

func TestSubscriptionRetentionPolicyRoundTrip(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	sub, err := st.AddSubscription(ctx, Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/retention",
		Root:      "/media/local/retention",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if sub.RetentionKind != RetentionKeepAll || sub.RetentionN != nil {
		t.Fatalf("default retention = %q/%v, want keep-all nil", sub.RetentionKind, sub.RetentionN)
	}

	n := 2
	updated, err := st.SetSubscriptionRetention(ctx, sub.ID, RetentionKeepLastN, &n)
	if err != nil {
		t.Fatalf("SetSubscriptionRetention returned error: %v", err)
	}
	if updated.RetentionKind != RetentionKeepLastN || updated.RetentionN == nil || *updated.RetentionN != 2 {
		t.Fatalf("updated retention = %q/%v, want keep-last-n/2", updated.RetentionKind, updated.RetentionN)
	}
	if _, err := st.SetSubscriptionRetention(ctx, sub.ID, RetentionDeleteAfterPlayed, nil); err != nil {
		t.Fatalf("delete-after-played retention returned error: %v", err)
	}
	if _, err := st.SetSubscriptionRetention(ctx, sub.ID, RetentionKeepLastN, nil); err == nil {
		t.Fatal("keep-last-n without n succeeded, want validation error")
	}
	zero := 0
	if _, err := st.SetSubscriptionRetention(ctx, sub.ID, RetentionKeepLastN, &zero); err == nil {
		t.Fatal("keep-last-0 succeeded, want validation error (it would trash everything)")
	}
	if _, err := st.SetSubscriptionRetention(ctx, sub.ID, "forever", nil); err == nil {
		t.Fatal("unknown retention kind succeeded, want validation error")
	}
}
