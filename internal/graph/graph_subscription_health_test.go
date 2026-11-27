package graph

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestSubscriptionHealthAndRefreshOverGraph pins CORE-2955: the Graph carries
// feed health, a follower may refresh a Subscription, a non-follower viewer
// sees it as unknown, and a second refresh inside five minutes is refused.
func TestSubscriptionHealthAndRefreshOverGraph(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	sub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/health?token=feed-secret",
		Root:      "/media/health",
		Title:     "Health Feed",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	failingSince := now.Add(-8 * 24 * time.Hour)
	if err := st.RecordSubscriptionPollFailure(ctx, sub, store.FeedErrorHTTP5xx, failingSince, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)

	var listed []SubscriptionInfo
	execGraph(t, handler, authorizeManager,
		`query { subscriptions(libraryID: "library-local") { id healthStatus lastErrorClass failingSince nextPollAt } }`,
		nil, "subscriptions", &listed)
	if len(listed) != 1 || listed[0].HealthStatus != "warning" || listed[0].LastErrorClass != "http-5xx" ||
		listed[0].FailingSince == "" || listed[0].NextPollAt == "" {
		t.Fatalf("subscriptions = %+v, want a warning http-5xx feed with failingSince and nextPollAt", listed)
	}

	refresh := `mutation Refresh($id: String!) { refreshSubscription(id: $id) { id title healthStatus lastErrorClass nextPollAt } }`
	resp := postGraphJSON(t, handler, authorizeViewer, refresh, map[string]any{"id": string(sub.ID)})
	if len(resp.Errors) == 0 || !strings.Contains(resp.Errors[0].Message, "unknown subscription") {
		t.Fatalf("non-follower refresh errors = %+v, want unknown subscription", resp.Errors)
	}

	var followed FollowInfo
	execGraph(t, handler, authorizeViewer,
		`mutation Follow($id: String!) { setFollow(subscriptionID: $id, on: true) { id followed } }`,
		map[string]any{"id": string(sub.ID)}, "setFollow", &followed)
	var refreshed SubscriptionRefreshInfo
	execGraph(t, handler, authorizeViewer, refresh, map[string]any{"id": string(sub.ID)}, "refreshSubscription", &refreshed)
	if refreshed.ID != string(sub.ID) || refreshed.HealthStatus != "warning" || refreshed.LastErrorClass != "http-5xx" {
		t.Fatalf("refresh echo = %+v", refreshed)
	}
	stored, err := st.Subscription(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Health.Due(time.Now().UTC()) {
		t.Fatalf("refreshed subscription not due: %+v", stored.Health)
	}
	jobs, err := st.ListJobs(ctx, store.JobStatusQueued)
	if err != nil {
		t.Fatal(err)
	}
	if !hasJobKind(jobs, "subscription-poll") {
		t.Fatalf("queued jobs = %+v, want the subscription poll", jobs)
	}

	resp = postGraphJSON(t, handler, authorizeViewer, refresh, map[string]any{"id": string(sub.ID)})
	if len(resp.Errors) == 0 || !strings.Contains(resp.Errors[0].Message, "refreshed recently") {
		t.Fatalf("second refresh errors = %+v, want the rate limit", resp.Errors)
	}

	resp = postGraphJSON(t, handler, authorizeViewer, `mutation { refreshAllSubscriptions { refreshed } }`, nil)
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "manager role required" {
		t.Fatalf("viewer refresh-all errors = %+v", resp.Errors)
	}
	var all SubscriptionRefreshAllInfo
	execGraph(t, handler, authorizeManager, `mutation { refreshAllSubscriptions { refreshed } }`, nil, "refreshAllSubscriptions", &all)
	if all.Refreshed != 0 {
		t.Fatalf("refresh all inside the gap refreshed %d, want 0", all.Refreshed)
	}
}

func hasJobKind(jobs []store.Job, kind store.JobKind) bool {
	for _, job := range jobs {
		if job.Kind == kind {
			return true
		}
	}
	return false
}

// TestRefreshAllSubscriptionsHonoursLibraryScope pins the CORE-2955 review
// fix: refresh-all neither refreshes nor counts a feed in a Library the
// Manager cannot see, so the count never reveals that Library's feeds.
func TestRefreshAllSubscriptionsHonoursLibraryScope(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	added := time.Now().UTC()
	hiddenLibrary, err := st.CreateLibrary(ctx, "Hidden Feeds", false, "feedkit", "profile-managed")
	if err != nil {
		t.Fatal(err)
	}
	visible, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local", FeedURL: "https://feeds.example.com/visible", Root: "/media/visible", Title: "Visible Feed",
	}, added)
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: hiddenLibrary.ID, FeedURL: "https://feeds.example.com/hidden", Root: "/media/hidden", Title: "Hidden Feed",
	}, added)
	if err != nil {
		t.Fatal(err)
	}
	later := added.Add(time.Hour)
	for _, subscription := range []store.Subscription{visible, hidden} {
		if err := st.RecordSubscriptionPollSuccess(ctx, subscription, "", "", added, later); err != nil {
			t.Fatal(err)
		}
	}
	handler := newTestHandlerWithStore(st)

	var all SubscriptionRefreshAllInfo
	execGraph(t, handler, authorizeManager, `mutation { refreshAllSubscriptions { refreshed } }`, nil, "refreshAllSubscriptions", &all)
	if all.Refreshed != 1 {
		t.Fatalf("refresh all refreshed %d, want only the visible Library's feed", all.Refreshed)
	}
	now := time.Now().UTC()
	for id, wantDue := range map[store.SubscriptionID]bool{visible.ID: true, hidden.ID: false} {
		stored, err := st.Subscription(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got := stored.Health.Due(now); got != wantDue {
			t.Fatalf("subscription %s due = %v, want %v (health %+v)", id, got, wantDue, stored.Health)
		}
	}
}

// TestRefreshAllSubscriptionsSkipsSealedLibrary pins the CORE-2955 delta
// review: a Library the Manager is granted but which is Sealed is hidden, so
// refresh-all neither refreshes nor counts its feeds.
func TestRefreshAllSubscriptionsSkipsSealedLibrary(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	added := time.Now().UTC()
	sealedLibrary, err := st.CreateLibrary(ctx, "Sealed Feeds", false, "feedkit", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	visible, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local", FeedURL: "https://feeds.example.com/open", Root: "/media/open", Title: "Open Feed",
	}, added)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: sealedLibrary.ID, FeedURL: "https://feeds.example.com/sealed", Root: "/media/sealed", Title: "Sealed Feed",
	}, added)
	if err != nil {
		t.Fatal(err)
	}
	later := added.Add(time.Hour)
	for _, subscription := range []store.Subscription{visible, sealed} {
		if err := st.RecordSubscriptionPollSuccess(ctx, subscription, "", "", added, later); err != nil {
			t.Fatal(err)
		}
	}
	handler := newTestHandlerWithStore(st)
	mgr := libencrypt.NewManagerWithParams(newMemSecretStore(), libencrypt.TestArgon2Params)
	handler.SetLibEncrypt(mgr)
	if _, err := mgr.Provision(string(sealedLibrary.ID), libencrypt.UnsealModeAuto, ""); err != nil {
		t.Fatal(err)
	}
	mgr.Seal(string(sealedLibrary.ID))

	var all SubscriptionRefreshAllInfo
	execGraph(t, handler, authorizeManager, `mutation { refreshAllSubscriptions { refreshed } }`, nil, "refreshAllSubscriptions", &all)
	if all.Refreshed != 1 {
		t.Fatalf("refresh all refreshed %d, want only the unsealed Library's feed", all.Refreshed)
	}
	stored, err := st.Subscription(ctx, sealed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Health.Due(time.Now().UTC()) {
		t.Fatalf("sealed Library's feed was refreshed: %+v", stored.Health)
	}
}

// TestSealedLibrarySubscriptionsReadAsMissing pins the CORE-2955 delta
// review: a Manager granted a Sealed Library cannot list, refresh or add its
// Subscriptions, and is refused exactly as for a Library that does not exist.
func TestSealedLibrarySubscriptionsReadAsMissing(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	sealedLibrary, err := st.CreateLibrary(ctx, "Sealed Feeds", false, "feedkit", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	sub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: sealedLibrary.ID, FeedURL: "https://feeds.example.com/sealed", Root: "/media/sealed", Title: "Sealed Feed",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)
	mgr := libencrypt.NewManagerWithParams(newMemSecretStore(), libencrypt.TestArgon2Params)
	handler.SetLibEncrypt(mgr)
	if _, err := mgr.Provision(string(sealedLibrary.ID), libencrypt.UnsealModeAuto, ""); err != nil {
		t.Fatal(err)
	}
	mgr.Seal(string(sealedLibrary.ID))

	refusal := func(query string, vars map[string]any) string {
		t.Helper()
		resp := postGraphJSON(t, handler, authorizeManager, query, vars)
		if len(resp.Errors) == 0 {
			t.Fatalf("%s %v succeeded, want a refusal", query, vars)
		}
		return resp.Errors[0].Message
	}
	list := `query List($library: String!) { subscriptions(libraryID: $library) { id } }`
	add := `mutation Add($library: String!) { addSubscription(libraryID: $library, feedURL: "https://feeds.example.com/new") { id } }`
	refresh := `mutation Refresh($id: String!) { refreshSubscription(id: $id) { id } }`
	for _, tc := range []struct {
		name          string
		query         string
		sealed, unset map[string]any
	}{
		{"list", list, map[string]any{"library": string(sealedLibrary.ID)}, map[string]any{"library": "library-missing"}},
		{"add", add, map[string]any{"library": string(sealedLibrary.ID)}, map[string]any{"library": "library-missing"}},
		{"refresh", refresh, map[string]any{"id": string(sub.ID)}, map[string]any{"id": "subscription-missing"}},
	} {
		if got, want := refusal(tc.query, tc.sealed), refusal(tc.query, tc.unset); got != want {
			t.Errorf("%s on a sealed Library refused with %q, want the missing-Library refusal %q", tc.name, got, want)
		}
	}
}

// TestSealedFilterFailsClosedWhenSecureStoreRefusesReads pins CORE-3033: when
// the secure store refuses to read a sealed Library's key bundle, sealedFilter
// distinguishes that read failure from a missing bundle (unprovisioned open
// Libraries stay visible) and fails closed so the sealed Library's
// Subscriptions and Media cannot be listed, removed or refreshed.
func TestSealedFilterFailsClosedWhenSecureStoreRefusesReads(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	added := time.Now().UTC()

	sealedLibrary, err := st.CreateLibrary(ctx, "Sealed Vault", false, "feedkit", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	openSub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local", FeedURL: "https://feeds.example.com/open", Root: "/media/open", Title: "Open Feed",
	}, added)
	if err != nil {
		t.Fatal(err)
	}
	sealedSub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: sealedLibrary.ID, FeedURL: "https://feeds.example.com/sealed", Root: "/media/sealed", Title: "Sealed Feed",
	}, added)
	if err != nil {
		t.Fatal(err)
	}
	later := added.Add(time.Hour)
	for _, subscription := range []store.Subscription{openSub, sealedSub} {
		if err := st.RecordSubscriptionPollSuccess(ctx, subscription, "", "", added, later); err != nil {
			t.Fatal(err)
		}
	}

	sealedMedia, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          sealedLibrary.ID,
		Path:               "/media/sealed/item.mp4",
		Title:              "Sealed Item",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:core-3033-sealed-item",
	})
	if err != nil {
		t.Fatal(err)
	}
	col, err := st.CreateCollection(ctx, store.Collection{LibraryID: sealedLibrary.ID, Name: "Sealed Set"}, added)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddCollectionMedia(ctx, col.ID, []store.MediaID{sealedMedia.ID}, store.LibraryFilter{AllLibraries: true, Revealed: true}, added); err != nil {
		t.Fatal(err)
	}

	handler := newTestHandlerWithStore(st)
	var seededQueue PlayQueueInfo
	execGraph(t, handler, authorizeManager,
		`mutation ($ids: [String!]!) { queueAddMany(mediaIDs: $ids, placement: "end") `+queueStateSelection+" }",
		map[string]any{"ids": []string{"media-alpha", string(sealedMedia.ID)}}, "queueAddMany", &seededQueue)
	if len(seededQueue.Entries) != 2 {
		t.Fatalf("seeded queue entries = %+v, want 2", seededQueue.Entries)
	}
	sealedEntryID := seededQueue.Entries[1].ID

	secrets := newMemSecretStore()
	mgr := libencrypt.NewManagerWithParams(secrets, libencrypt.TestArgon2Params)
	handler.SetLibEncrypt(mgr)
	if _, err := mgr.Provision(string(sealedLibrary.ID), libencrypt.UnsealModeAuto, ""); err != nil {
		t.Fatal(err)
	}
	mgr.Seal(string(sealedLibrary.ID))

	errReadRefused := errors.New("secure store read refused")
	// Refuse reads for stored secrets while missing refs ("library-local")
	// continue returning fs.ErrNotExist.
	secrets.failGet = func(ref string) error {
		if _, ok := secrets.values[ref]; ok {
			return errReadRefused
		}
		return nil
	}

	// Open unprovisioned Library ("library-local", missing bundle -> fs.ErrNotExist)
	// stays accessible: missing key bundle is distinguished from a read failure.
	var openListed []SubscriptionInfo
	execGraph(t, handler, authorizeManager,
		`query { subscriptions(libraryID: "library-local") { id } }`,
		nil, "subscriptions", &openListed)
	if len(openListed) != 1 || openListed[0].ID != string(openSub.ID) {
		t.Fatalf("open Library subscriptions = %+v, want [%s]", openListed, openSub.ID)
	}

	// Sealed Library Subscriptions cannot be listed, refreshed or removed.
	for _, tc := range []struct {
		name  string
		query string
		vars  map[string]any
		want  string
	}{
		{
			name:  "list subscriptions",
			query: `query List($library: String!) { subscriptions(libraryID: $library) { id } }`,
			vars:  map[string]any{"library": string(sealedLibrary.ID)},
			want:  "unknown library",
		},
		{
			name:  "refresh subscription",
			query: `mutation Refresh($id: String!) { refreshSubscription(id: $id) { id } }`,
			vars:  map[string]any{"id": string(sealedSub.ID)},
			want:  "unknown subscription",
		},
		{
			name:  "remove subscription",
			query: `mutation Remove($id: String!) { removeSubscription(id: $id) { id } }`,
			vars:  map[string]any{"id": string(sealedSub.ID)},
			want:  "unknown subscription",
		},
	} {
		resp := postGraphJSON(t, handler, authorizeManager, tc.query, tc.vars)
		if len(resp.Errors) == 0 || resp.Errors[0].Message != tc.want {
			t.Fatalf("%s with unreadable bundle: errors = %+v, want %q", tc.name, resp.Errors, tc.want)
		}
	}

	var all SubscriptionRefreshAllInfo
	execGraph(t, handler, authorizeManager, `mutation { refreshAllSubscriptions { refreshed } }`, nil, "refreshAllSubscriptions", &all)
	if all.Refreshed != 1 {
		t.Fatalf("refreshAllSubscriptions refreshed %d, want 1 (only open Library)", all.Refreshed)
	}
	storedSealedSub, err := st.Subscription(ctx, sealedSub.ID)
	if err != nil {
		t.Fatalf("sealed subscription was removed or lost: %v", err)
	}
	if storedSealedSub.Health.Due(time.Now().UTC()) {
		t.Fatalf("sealed subscription became due on refreshAllSubscriptions: %+v", storedSealedSub.Health)
	}

	// Sealed Library Media cannot be listed (playQueue, collections) or removed
	// (queueRemoveEntry, queueRemove).
	var visibleQueue PlayQueueInfo
	execGraph(t, handler, authorizeManager, "{ playQueue "+queueStateSelection+" }", nil, "playQueue", &visibleQueue)
	if len(visibleQueue.Entries) != 1 || visibleQueue.Entries[0].MediaID != "media-alpha" {
		t.Fatalf("playQueue with unreadable bundle = %+v, want only media-alpha", visibleQueue.Entries)
	}

	var cols []CollectionInfo
	execGraph(t, handler, authorizeManager,
		`query ListCols($library: String!) { collections(libraryID: $library) { id memberCount members { mediaID } } }`,
		map[string]any{"library": string(sealedLibrary.ID)}, "collections", &cols)
	if len(cols) != 1 || cols[0].MemberCount != 0 || len(cols[0].Members) != 0 {
		t.Fatalf("collections with unreadable bundle = %+v, want sealed Media hidden", cols)
	}

	execGraph(t, handler, authorizeManager,
		`mutation ($id: String!) { queueRemoveEntry(entryID: $id) `+queueStateSelection+" }",
		map[string]any{"id": sealedEntryID}, "queueRemoveEntry", &visibleQueue)
	var remaining []SourceItem
	execGraph(t, handler, authorizeManager,
		`mutation ($mediaID: String!) { queueRemove(mediaID: $mediaID) { id } }`,
		map[string]any{"mediaID": string(sealedMedia.ID)}, "queueRemove", &remaining)

	rawQueue, err := st.PlayQueue(ctx, "profile-default", store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rawQueue.Entries) != 2 || rawQueue.Entries[1].Item.ID != sealedMedia.ID {
		t.Fatalf("sealed Media was removed from queue while bundle unreadable: %+v", rawQueue.Entries)
	}

	// Also fail closed when the secure store refuses ALL reads (including
	// unprovisioned refs).
	secrets.failGet = func(string) error { return errReadRefused }
	for _, tc := range []struct {
		name  string
		query string
		vars  map[string]any
	}{
		{
			name:  "list subscriptions (all reads refused)",
			query: `query List($library: String!) { subscriptions(libraryID: $library) { id } }`,
			vars:  map[string]any{"library": string(sealedLibrary.ID)},
		},
		{
			name:  "refresh subscription (all reads refused)",
			query: `mutation Refresh($id: String!) { refreshSubscription(id: $id) { id } }`,
			vars:  map[string]any{"id": string(sealedSub.ID)},
		},
		{
			name:  "remove subscription (all reads refused)",
			query: `mutation Remove($id: String!) { removeSubscription(id: $id) { id } }`,
			vars:  map[string]any{"id": string(sealedSub.ID)},
		},
	} {
		resp := postGraphJSON(t, handler, authorizeManager, tc.query, tc.vars)
		if len(resp.Errors) == 0 {
			t.Fatalf("%s succeeded when all secure-store reads refused", tc.name)
		}
	}
	execGraph(t, handler, authorizeManager, "{ playQueue "+queueStateSelection+" }", nil, "playQueue", &visibleQueue)
	if len(visibleQueue.Entries) != 0 {
		t.Fatalf("playQueue when all secure-store reads refused = %+v, want empty", visibleQueue.Entries)
	}
}

func TestSealedFilterKeepsSubscriptionsHiddenWhenSecretsDirDisappears(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	added := time.Now().UTC()

	sealedLibrary, err := st.CreateLibrary(ctx, "Encrypted Vault", false, "feedkit", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	openSub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local", FeedURL: "https://feeds.example.com/open", Root: "/media/open", Title: "Open Feed",
	}, added)
	if err != nil {
		t.Fatal(err)
	}
	sealedSub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: sealedLibrary.ID, FeedURL: "https://feeds.example.com/sealed", Root: "/media/sealed", Title: "Sealed Feed",
	}, added)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryBacking(ctx, sealedLibrary.ID, false, true); err != nil {
		t.Fatal(err)
	}

	handler := newTestHandlerWithStore(st)
	secrets := newMemSecretStore()
	mgr := libencrypt.NewManagerWithParams(secrets, libencrypt.TestArgon2Params)
	handler.SetLibEncrypt(mgr)
	if _, err := mgr.Provision(string(sealedLibrary.ID), libencrypt.UnsealModeAuto, ""); err != nil {
		t.Fatal(err)
	}
	if err := mgr.AutoUnseal(string(sealedLibrary.ID)); err != nil {
		t.Fatal(err)
	}
	mgr.Seal(string(sealedLibrary.ID))

	// Remove all secret entries (simulating the secrets directory disappearing
	// while the server is running so Secret() returns fs.ErrNotExist).
	clear(secrets.values)

	var openListed []SubscriptionInfo
	execGraph(t, handler, authorizeManager,
		`query { subscriptions(libraryID: "library-local") { id } }`,
		nil, "subscriptions", &openListed)
	if len(openListed) != 1 || openListed[0].ID != string(openSub.ID) {
		t.Fatalf("open Library subscriptions = %+v, want [%s]", openListed, openSub.ID)
	}

	for _, tc := range []struct {
		name  string
		query string
		vars  map[string]any
		want  string
	}{
		{
			name:  "list subscriptions",
			query: `query List($library: String!) { subscriptions(libraryID: $library) { id } }`,
			vars:  map[string]any{"library": string(sealedLibrary.ID)},
			want:  "unknown library",
		},
		{
			name:  "remove subscription",
			query: `mutation Remove($id: String!) { removeSubscription(id: $id) { id } }`,
			vars:  map[string]any{"id": string(sealedSub.ID)},
			want:  "unknown subscription",
		},
	} {
		resp := postGraphJSON(t, handler, authorizeManager, tc.query, tc.vars)
		if len(resp.Errors) == 0 || resp.Errors[0].Message != tc.want {
			t.Fatalf("%s with missing secrets dir: errors = %+v, want %q", tc.name, resp.Errors, tc.want)
		}
	}
	if _, err := st.Subscription(ctx, sealedSub.ID); err != nil {
		t.Fatalf("sealed subscription was removed when secrets dir disappeared: %v", err)
	}
}
