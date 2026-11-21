package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func catalogueFixture(t *testing.T) (*Store, Subscription, time.Time) {
	t.Helper()
	st := openSeeded(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	sub, err := st.AddSubscription(context.Background(), Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/catalogue.xml?token=hush",
		Root:      "/media/local/incoming",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	return st, sub, now
}

func catalogueEntries(n int) []FeedCatalogEntry {
	entries := make([]FeedCatalogEntry, n)
	for i := range entries {
		entries[i] = FeedCatalogEntry{
			GUID:            fmt.Sprintf("guid-%02d", i),
			Title:           fmt.Sprintf("Entry %02d", i),
			Description:     "notes",
			PublishedAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n-i),
			DurationSeconds: 600,
			EnclosureURL:    fmt.Sprintf("https://cdn.example.com/%02d.m4a?sig=secret", i),
			EnclosureType:   "audio/x-m4a",
			EnclosureLength: 1000,
			Position:        i,
			FeedTitle:       "Catalogue Feed",
		}
	}
	return entries
}

func TestUpsertSubscriptionCatalogCreatesFileLessMediaIdempotently(t *testing.T) {
	st, sub, now := catalogueFixture(t)
	ctx := context.Background()
	entries := catalogueEntries(40)

	created, err := st.UpsertSubscriptionCatalog(ctx, sub.ID, entries, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("UpsertSubscriptionCatalog: %v", err)
	}
	if len(created) != 40 {
		t.Fatalf("created %d entries, want 40", len(created))
	}
	first := created[0]
	if want := SubscriptionEntryMediaID(sub.LibraryID, sub.ID, "guid-00"); first.MediaID != want {
		t.Fatalf("MediaID = %q, want %q", first.MediaID, want)
	}
	if !first.FirstSeenAt.Equal(sub.AddedAt) {
		t.Fatalf("back catalogue FirstSeenAt = %v, want AddedAt %v", first.FirstSeenAt, sub.AddedAt)
	}
	media, err := st.Media(ctx, first.MediaID, LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatalf("Media: %v", err)
	}
	if media.Title != "Entry 00" || media.Class != MediaClassAudio || media.Container != "m4a" || media.Codec != "aac" || media.DurationSeconds != 600 {
		t.Fatalf("media = %+v, want feed facts", media)
	}
	ids, err := st.MediaExternalIDs(ctx, first.MediaID)
	if err != nil {
		t.Fatal(err)
	}
	gotSchemes := map[string]string{}
	for _, id := range ids {
		gotSchemes[id.Scheme] = id.Value
	}
	if gotSchemes[ProvisionalExternalIDScheme] != SubscriptionEntryStreamKey(sub.ID, "guid-00") || gotSchemes[subscriptionFeedGUIDScheme] != "guid-00" {
		t.Fatalf("external ids = %+v", ids)
	}

	st.mu.RLock()
	items, err := st.availabilitySQL(ctx, "not-downloaded", LibraryFilter{AllLibraries: true, Revealed: true}, 100, 0)
	st.mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	inBand := map[MediaID]bool{}
	for _, item := range items {
		inBand[item.ID] = true
	}
	for _, entry := range created {
		if !inBand[entry.MediaID] {
			t.Fatalf("entry %s not in not-downloaded band", entry.GUID)
		}
	}

	// A re-poll creates nothing and refreshes known entries.
	entries[0].Title = "Entry 00 (renamed)"
	entries[0].Position = 5
	again, err := st.UpsertSubscriptionCatalog(ctx, sub.ID, entries, now.Add(time.Hour))
	if err != nil || len(again) != 0 {
		t.Fatalf("second upsert = %d created, %v; want 0, nil", len(again), err)
	}
	listed, err := st.SubscriptionEntries(ctx, sub.ID)
	if err != nil || len(listed) != 40 {
		t.Fatalf("SubscriptionEntries = %d, %v", len(listed), err)
	}
	var renamed SubscriptionEntry
	for _, entry := range listed {
		if entry.GUID == "guid-00" {
			renamed = entry
		}
	}
	if renamed.Title != "Entry 00 (renamed)" || renamed.FeedPosition != 5 || renamed.Downloaded || renamed.DownloadState != EntryDownloadNone {
		t.Fatalf("refreshed entry = %+v", renamed)
	}

	// Later arrivals are stamped with the poll time, after AddedAt.
	later := append([]FeedCatalogEntry{{GUID: "guid-new", Title: "New", Position: 0}}, entries...)
	fresh, err := st.UpsertSubscriptionCatalog(ctx, sub.ID, later, now.Add(2*time.Hour))
	if err != nil || len(fresh) != 1 || !fresh[0].FirstSeenAt.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("new arrival = %+v, %v", fresh, err)
	}
	got, err := st.Subscription(ctx, sub.ID)
	if err != nil || got.Title != "Catalogue Feed" {
		t.Fatalf("subscription title = %q, %v; want feed title", got.Title, err)
	}
}

func TestScanImportBindsDownloadToEntryMedia(t *testing.T) {
	st, sub, now := catalogueFixture(t)
	ctx := context.Background()
	created, err := st.UpsertSubscriptionCatalog(ctx, sub.ID, catalogueEntries(2), now)
	if err != nil {
		t.Fatal(err)
	}
	target := "/media/local/incoming/entry-00.m4a"
	if err := st.SetSubscriptionEntryDownload(ctx, sub.ID, "guid-00", EntryDownloadQueued, target); err != nil {
		t.Fatalf("SetSubscriptionEntryDownload: %v", err)
	}
	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          "library-local",
		Path:               target,
		Title:              "entry-00",
		Container:          "m4a",
		Codec:              "aac",
		Class:              MediaClassAudio,
		ContentFingerprint: "fp-catalogue-bind-0001",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	if media.ID != created[0].MediaID {
		t.Fatalf("imported media %q, want entry media %q", media.ID, created[0].MediaID)
	}
	if err := st.SetSubscriptionAuthRef(ctx, sub.ID, "feed-auth-ref"); err != nil {
		t.Fatal(err)
	}
	// Playback attaches the stream through these fields (pod/feed-play contract).
	entry, owner, ok, err := st.SubscriptionEntryForMedia(ctx, media.ID)
	if err != nil || !ok || owner.ID != sub.ID || owner.LibraryID != sub.LibraryID || owner.AuthRef != "feed-auth-ref" {
		t.Fatalf("SubscriptionEntryForMedia = %v %v %v", ok, owner.ID, err)
	}
	if entry.GUID != "guid-00" || entry.EnclosureURL != catalogueEntries(1)[0].EnclosureURL {
		t.Fatal("SubscriptionEntryForMedia lost the entry GUID or enclosure")
	}
	if !entry.Downloaded || entry.DownloadState != EntryDownloadDownloaded || entry.Title != "Entry 00" {
		t.Fatalf("bound entry = %+v, want downloaded with feed title", entry)
	}

	// A file at an unrecorded path still mints its own Media.
	other, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/local/incoming/other.mp3", Title: "other", Container: "mp3", Codec: "mp3",
		Class: MediaClassAudio, ContentFingerprint: "fp-catalogue-bind-0002",
	})
	if err != nil || other.ID == created[1].MediaID {
		t.Fatalf("unrelated import = %q, %v", other.ID, err)
	}
	if _, _, ok, err := st.SubscriptionEntryForMedia(ctx, other.ID); ok || err != nil {
		t.Fatalf("unrelated media is a feed entry: %v %v", ok, err)
	}
}

func TestSubscriptionCatalogueHashesURLGUIDsAndKeepsStreamIdentity(t *testing.T) {
	st, sub, now := catalogueFixture(t)
	ctx := t.Context()
	const guid = "https://cdn.example.com/audio.mp3?signature=test-secret"
	rows := []FeedCatalogEntry{{GUID: guid, EnclosureURL: guid}}
	created, err := st.UpsertSubscriptionCatalog(ctx, sub.ID, rows, now)
	if err != nil || len(created) != 1 {
		t.Fatalf("upsert: %d entries, %v", len(created), err)
	}
	id := created[0].MediaID
	if id != SubscriptionEntryMediaID(sub.LibraryID, sub.ID, guid) {
		t.Fatal("catalogue changed the stream identity")
	}
	media, err := st.Media(ctx, id, LibraryFilter{AllLibraries: true})
	if err != nil || media.Title != "Untitled" {
		t.Fatalf("untitled Media lookup failed or returned an unsafe title: %v", err)
	}
	ids, err := st.MediaExternalIDs(ctx, id)
	if err != nil || len(ids) != 2 {
		t.Fatalf("external IDs: count=%d err=%v", len(ids), err)
	}
	for _, external := range ids {
		if strings.Contains(external.Value, "://") || strings.Contains(external.Value, "test-secret") ||
			!strings.Contains(external.Value, "urn:beamfall:feed-entry:") {
			t.Errorf("external-ID scheme %s did not use an opaque value", external.Scheme)
		}
	}
	path := "/media/local/incoming/play.partial"
	registered, _, err := st.RegisterProvisionalMedia(ctx, ProvisionalMedia{
		LibraryID: sub.LibraryID, GUID: SubscriptionEntryStreamKey(sub.ID, guid), Title: media.Title, TeePath: path,
	})
	if err != nil || registered != id {
		t.Fatalf("register = %q, %v, want existing Media", registered, err)
	}
	if err := st.MarkSubscriptionEntrySeen(ctx, sub.ID, guid, path, now); err != nil {
		t.Fatal(err)
	}
	if added, _, err := st.ReconcileSubscriptionSeenEntries(ctx); err != nil || added != 0 {
		t.Fatalf("reconcile added %d IDs, err=%v; expected existing opaque ID", added, err)
	}
	after, err := st.MediaExternalIDs(ctx, id)
	if err != nil || !slices.Equal(after, ids) {
		t.Fatal("registering and reconciling changed the stored external IDs")
	}

	// Simulate a historical file-less item no longer present in the feed.
	legacyGUID := guid + "-old"
	legacy, err := st.UpsertSubscriptionCatalog(ctx, sub.ID, []FeedCatalogEntry{{GUID: legacyGUID}}, now)
	if err != nil || len(legacy) != 1 {
		t.Fatalf("seed legacy Media: %d entries, %v", len(legacy), err)
	}
	legacyID := legacy[0].MediaID
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE media SET title = ? WHERE id = ?`, legacyGUID, legacyID); err != nil {
		t.Fatal(err)
	}
	for _, external := range []MediaExternalID{
		{Scheme: subscriptionFeedGUIDScheme, Value: legacyGUID},
		{Scheme: ProvisionalExternalIDScheme, Value: SubscriptionEntryStreamKey(sub.ID, legacyGUID)},
	} {
		if _, err := st.catalogW.ExecContext(ctx, `INSERT INTO media_external_id(media_id, scheme, value) VALUES (?, ?, ?)`, legacyID, external.Scheme, external.Value); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := st.ReconcileSubscriptionSeenEntries(ctx); err != nil {
		t.Fatal(err)
	}
	media, err = st.Media(ctx, legacyID, LibraryFilter{AllLibraries: true})
	after, loadErr := st.MediaExternalIDs(ctx, legacyID)
	if err != nil || loadErr != nil || media.Title != "Untitled" || len(after) != 2 {
		t.Fatalf("legacy row remained unsanitized: media err=%v, IDs=%d err=%v", err, len(after), loadErr)
	}
	for _, external := range after {
		if strings.Contains(external.Value, "test-secret") {
			t.Fatal("legacy URL remained in stored external IDs")
		}
	}
}

func TestUpsertSubscriptionCatalogBindsPreCatalogueDownload(t *testing.T) {
	st, sub, now := catalogueFixture(t)
	ctx := context.Background()
	path := "/media/local/incoming/old.mp3"
	old, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: path, Title: "old", Container: "mp3", Codec: "mp3", Class: MediaClassAudio,
		ContentFingerprint: "fp-catalogue-legacy-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSubscriptionEntrySeen(ctx, sub.ID, "guid-00", path, now); err != nil {
		t.Fatal(err)
	}
	created, err := st.UpsertSubscriptionCatalog(ctx, sub.ID, catalogueEntries(1), now)
	if err != nil || len(created) != 1 {
		t.Fatalf("upsert = %d, %v", len(created), err)
	}
	if created[0].MediaID != old.ID || !created[0].Downloaded || created[0].DownloadPath != path {
		t.Fatalf("legacy entry = %+v, want bound to %q", created[0], old.ID)
	}
}

func TestSubscriptionRefreshWindowAndDueList(t *testing.T) {
	st, sub, now := catalogueFixture(t)
	ctx := context.Background()
	if err := st.RecordSubscriptionPollSuccess(ctx, sub.ID, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	due, err := st.DueSubscriptions(ctx, now)
	if err != nil || len(due) != 0 {
		t.Fatalf("due before refresh = %d, %v", len(due), err)
	}
	if err := st.RequestSubscriptionRefresh(ctx, sub.ID, now); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	due, err = st.DueSubscriptions(ctx, now)
	if err != nil || len(due) != 1 {
		t.Fatalf("due after refresh = %d, %v", len(due), err)
	}
	if err := st.RequestSubscriptionRefresh(ctx, sub.ID, now.Add(4*time.Minute)); !errors.Is(err, ErrSubscriptionRefreshTooSoon) {
		t.Fatalf("refresh within window = %v, want ErrSubscriptionRefreshTooSoon", err)
	}
	if err := st.RequestSubscriptionRefresh(ctx, sub.ID, now.Add(5*time.Minute)); err != nil {
		t.Fatalf("refresh after window: %v", err)
	}
	if err := st.RequestSubscriptionRefresh(ctx, "subscription-missing", now); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("missing refresh = %v", err)
	}
}

func TestSubscriptionHealthPersistence(t *testing.T) {
	st, sub, now := catalogueFixture(t)
	ctx := context.Background()
	if err := st.RecordSubscriptionPollFailure(ctx, sub.ID, FeedErrorDNS, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordSubscriptionPollFailure(ctx, sub.ID, FeedErrorNetwork, now.Add(time.Hour), now.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, health, err := st.SubscriptionPolicyAndHealth(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if health.LastErrorClass != FeedErrorNetwork || !health.FailingSince.Equal(now) || health.ConsecutiveFailures != 2 || !health.NextPollAt.Equal(now.Add(3*time.Hour)) {
		t.Fatalf("health after failures = %+v", health)
	}
	if err := st.RecordSubscriptionPollSuccess(ctx, sub.ID, now.Add(3*time.Hour), now.Add(4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, health, _ = st.SubscriptionPolicyAndHealth(ctx, sub.ID)
	if health.LastErrorClass != "" || !health.FailingSince.IsZero() || health.ConsecutiveFailures != 0 || !health.LastSuccessAt.Equal(now.Add(3*time.Hour)) {
		t.Fatalf("health after success = %+v", health)
	}
	if err := st.RecordSubscriptionPollFailure(ctx, sub.ID, FeedErrorGone, now.Add(5*time.Hour), now.Add(6*time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, health, _ = st.SubscriptionPolicyAndHealth(ctx, sub.ID)
	if !health.RetiredAt.Equal(now.Add(5 * time.Hour)) {
		t.Fatalf("RetiredAt = %v", health.RetiredAt)
	}
	if due, err := st.DueSubscriptions(ctx, now.Add(48*time.Hour)); err != nil || len(due) != 0 {
		t.Fatalf("retired subscription is due: %d %v", len(due), err)
	}
	if err := st.RecordSubscriptionPollFailure(ctx, sub.ID, "bogus", now, now); err == nil {
		t.Fatal("unknown error class accepted")
	}
}

func TestSubscriptionDownloadPolicyRoundTrip(t *testing.T) {
	st, sub, _ := catalogueFixture(t)
	ctx := context.Background()
	policy, _, err := st.SubscriptionPolicyAndHealth(ctx, sub.ID)
	if err != nil || policy.Policy != DownloadPolicyNewOnly || policy.LatestN != DefaultDownloadLatestN {
		t.Fatalf("default policy = %+v, %v", policy, err)
	}
	if err := st.SetSubscriptionDownloadPolicy(ctx, sub.ID, SubscriptionPolicy{Policy: DownloadPolicyLatest, LatestN: 5}); err != nil {
		t.Fatal(err)
	}
	policy, _, _ = st.SubscriptionPolicyAndHealth(ctx, sub.ID)
	if policy.Policy != DownloadPolicyLatest || policy.LatestN != 5 {
		t.Fatalf("policy = %+v", policy)
	}
	if err := st.SetSubscriptionDownloadPolicy(ctx, sub.ID, SubscriptionPolicy{Policy: DownloadPolicyStreamOnly}); err != nil {
		t.Fatal(err)
	}
	policy, _, _ = st.SubscriptionPolicyAndHealth(ctx, sub.ID)
	if policy.Policy != DownloadPolicyStreamOnly || policy.LatestN != DefaultDownloadLatestN {
		t.Fatalf("policy = %+v", policy)
	}
	if err := st.SetSubscriptionDownloadPolicy(ctx, sub.ID, SubscriptionPolicy{Policy: "sometimes"}); err == nil {
		t.Fatal("invalid policy accepted")
	}
}

func TestObserveSubscriptionFeedMove(t *testing.T) {
	st, sub, now := catalogueFixture(t)
	ctx := context.Background()
	moved := "https://feeds.example.com/moved.xml?token=hush"
	other := "https://feeds.example.com/other.xml"

	observe := func(url string) bool {
		t.Helper()
		ok, err := st.ObserveSubscriptionFeedMove(ctx, sub.ID, url)
		if err != nil {
			t.Fatalf("ObserveSubscriptionFeedMove: %v", err)
		}
		return ok
	}
	feedURL := func() string {
		t.Helper()
		got, err := st.Subscription(ctx, sub.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.FeedURL
	}

	if observe(moved) || feedURL() != sub.FeedURL {
		t.Fatal("one observation rewrote the feed URL")
	}
	if observe(other) || observe(moved) || observe("") || observe(moved) {
		t.Fatal("alternating observations rewrote the feed URL")
	}
	if !observe(moved) || feedURL() != moved {
		t.Fatalf("two consistent observations: feed URL = %q", feedURL())
	}

	// A move onto a URL another Subscription follows keeps the old URL.
	if _, err := st.AddSubscription(ctx, Subscription{LibraryID: "library-local", FeedURL: other, Root: "/media/local/incoming"}, now); err != nil {
		t.Fatal(err)
	}
	first, second := observe(other), observe(other)
	if first || second || feedURL() != moved {
		t.Fatalf("colliding move rewrote the feed URL to %q", feedURL())
	}
}
