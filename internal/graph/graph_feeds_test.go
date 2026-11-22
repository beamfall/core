package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// fakeSubscriptionCatalogue is an in-memory feed catalogue for graph tests.
type fakeSubscriptionCatalogue struct {
	mu       sync.Mutex
	policies map[store.SubscriptionID]store.SubscriptionPolicy
	entries  map[store.SubscriptionID][]store.SubscriptionEntry
}

func newFakeSubscriptionCatalogue() *fakeSubscriptionCatalogue {
	return &fakeSubscriptionCatalogue{
		policies: map[store.SubscriptionID]store.SubscriptionPolicy{},
		entries:  map[store.SubscriptionID][]store.SubscriptionEntry{},
	}
}

func (f *fakeSubscriptionCatalogue) SubscriptionDownloadPolicy(_ context.Context, id store.SubscriptionID) (store.SubscriptionPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	policy, ok := f.policies[id]
	if !ok {
		policy = store.SubscriptionPolicy{Policy: store.DownloadPolicyLatest, LatestN: store.DefaultDownloadLatestN}
	}
	return policy, nil
}

func (f *fakeSubscriptionCatalogue) SubscriptionEntries(_ context.Context, id store.SubscriptionID) ([]store.SubscriptionEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.SubscriptionEntry(nil), f.entries[id]...), nil
}

func (f *fakeSubscriptionCatalogue) SetSubscriptionDownloadPolicy(_ context.Context, id store.SubscriptionID, policy store.SubscriptionPolicy) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.policies[id] = policy
	return nil
}

type fakeFeedProber struct {
	probe FeedProbe
	err   error
	calls int
}

func (p *fakeFeedProber) ProbeFeed(_ context.Context, _ string) (FeedProbe, error) {
	p.calls++
	return p.probe, p.err
}

type recordingPoller struct {
	mu  sync.Mutex
	ids []store.SubscriptionID
}

func (p *recordingPoller) PollSubscriptionNow(_ context.Context, id store.SubscriptionID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ids = append(p.ids, id)
}

func (p *recordingPoller) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.ids)
}

func authorizeManagedProfile(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", "session-managed")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
}

const secretFeedURL = "https://feeds.example.com/daily.xml?token=hush" // #nosec G101 -- test fixture, not a credential

type feedFixture struct {
	st        *store.Store
	handler   *Handler
	catalogue *fakeSubscriptionCatalogue
	prober    *fakeFeedProber
	poller    *recordingPoller
}

func newFeedFixture(t *testing.T) feedFixture {
	t.Helper()
	st := storetest.Open(t)
	if err := st.SetLibraryRoots(context.Background(), "library-local", []string{"/media/Root"}); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)
	catalogue := newFakeSubscriptionCatalogue()
	handler.feedCatalogue = catalogue
	prober := &fakeFeedProber{probe: FeedProbe{Title: "Daily News!", EntryCount: 3, CatalogBytes: 3000}}
	poller := &recordingPoller{}
	handler.SetFeedProber(prober)
	handler.SetSubscriptionPoller(poller)
	handler.SetFeedErrorClassifier(func(error) store.FeedErrorClass { return store.FeedErrorDNS })
	return feedFixture{st: st, handler: handler, catalogue: catalogue, prober: prober, poller: poller}
}

// rawGraph posts a query and returns the raw body and the decoded response.
func rawGraph(t *testing.T, handler *Handler, authorize func(*http.Request), query string, variables map[string]any) (string, graphTestResponse) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(payload))
	authorize(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp graphTestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return rec.Body.String(), resp
}

func wantGraphError(t *testing.T, resp graphTestResponse, code ErrorCode, contains string) {
	t.Helper()
	if len(resp.Errors) == 0 {
		t.Fatalf("no error, want %s %q", code, contains)
	}
	got := resp.Errors[0]
	if got.Extensions.Code != code || !strings.Contains(got.Message, contains) {
		t.Fatalf("error = %s %q, want %s containing %q", got.Extensions.Code, got.Message, code, contains)
	}
}

const followFeedMutation = `mutation Follow($feedURL: String!, $libraryID: String, $downloadPolicy: String, $latestN: Int, $confirmEverything: Boolean) {
  followFeed(feedURL: $feedURL, libraryID: $libraryID, downloadPolicy: $downloadPolicy, latestN: $latestN, confirmEverything: $confirmEverything) {
    id libraryID feedURL root title followed downloadPolicy downloadLatestN entryCount downloadedCount catalogBytes
    healthStatus lastErrorClass failingSince nextPollAt lastPolledAt
  }
}`

func TestFollowFeedGatesProbesAndIsIdempotent(t *testing.T) {
	fx := newFeedFixture(t)

	_, resp := rawGraph(t, fx.handler, authorizeManagedProfile, followFeedMutation,
		map[string]any{"feedURL": secretFeedURL, "libraryID": "library-local"})
	wantGraphError(t, resp, ErrorCodeForbidden, "adult profile required to follow feeds")

	var first SubscriptionInfo
	execGraph(t, fx.handler, authorizeViewer, followFeedMutation,
		map[string]any{"feedURL": secretFeedURL, "libraryID": "library-local"}, "followFeed", &first)
	if first.ID == "" || first.LibraryID != "library-local" || first.Title != "Daily News!" || !first.Followed {
		t.Fatalf("followFeed = %+v", first)
	}
	if first.FeedURL != "" || first.Root != "" {
		t.Fatalf("viewer followFeed leaked manager fields: feedURL=%q root=%q", first.FeedURL, first.Root)
	}
	if first.DownloadPolicy != "latest" || first.DownloadLatestN != 3 || first.HealthStatus != string(store.FeedHealthOK) || first.LastPolledAt != "" {
		t.Fatalf("default policy/health = %+v", first)
	}
	if fx.poller.count() != 1 {
		t.Fatalf("poller kicks = %d, want 1", fx.poller.count())
	}

	var again SubscriptionInfo
	execGraph(t, fx.handler, authorizeManager, followFeedMutation,
		map[string]any{"feedURL": secretFeedURL, "libraryID": "library-local", "downloadPolicy": "stream-only"}, "followFeed", &again)
	if again.ID != first.ID {
		t.Fatalf("second follow id = %q, want %q (idempotent)", again.ID, first.ID)
	}
	if again.Root != "/media/Root/Daily News" || again.FeedURL != secretFeedURL {
		t.Fatalf("manager view root=%q feedURL=%q", again.Root, again.FeedURL)
	}
	if again.DownloadPolicy != "latest" {
		t.Fatalf("follow of an existing feed changed its policy to %q", again.DownloadPolicy)
	}
	if fx.poller.count() != 1 {
		t.Fatalf("poller kicks after re-follow = %d, want 1", fx.poller.count())
	}
}

func TestFollowFeedRejectsManagedProfileOnManagerDevice(t *testing.T) {
	fx := newFeedFixture(t)
	scope := auth.Scope{DeviceRole: store.DeviceRoleManager, Profile: store.ProfileManaged}
	_, err := fx.handler.followFeed(t.Context(), scope, request{})
	if err == nil || err.Error() != "adult profile required to follow feeds" || fx.prober.calls != 0 {
		t.Fatalf("followFeed = %v, probes = %d", err, fx.prober.calls)
	}
}

func TestFollowFeedPicksTheSingleFeedLibraryOrAsks(t *testing.T) {
	fx := newFeedFixture(t)
	_, resp := rawGraph(t, fx.handler, authorizeViewer, followFeedMutation, map[string]any{"feedURL": secretFeedURL})
	wantGraphError(t, resp, ErrorCodeInvalid, "libraryID is required")

	if _, _, err := fx.st.GetOrCreateSubscription(context.Background(), store.Subscription{
		LibraryID: "library-local", FeedURL: "https://feeds.example.com/other.xml", Root: "/media/Root/Other",
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var followed SubscriptionInfo
	execGraph(t, fx.handler, authorizeViewer, followFeedMutation, map[string]any{"feedURL": secretFeedURL}, "followFeed", &followed)
	if followed.LibraryID != "library-local" {
		t.Fatalf("libraryID = %q, want the single feed library", followed.LibraryID)
	}
}

func TestFollowFeedNeverEchoesTheFeedURL(t *testing.T) {
	fx := newFeedFixture(t)
	fx.prober.err = errors.New("lookup failed for " + secretFeedURL)
	body, resp := rawGraph(t, fx.handler, authorizeViewer, followFeedMutation,
		map[string]any{"feedURL": secretFeedURL, "libraryID": "library-local"})
	wantGraphError(t, resp, ErrorCodeInvalid, "feed could not be read: dns")
	if strings.Contains(body, "hush") || strings.Contains(body, "feeds.example.com") {
		t.Fatalf("probe error leaked the feed URL: %s", body)
	}

	credentialed := "https://u:p@feeds.example.com/private.xml" // #nosec G101 -- test fixture, not a credential
	body, resp = rawGraph(t, fx.handler, authorizeViewer, followFeedMutation,
		map[string]any{"feedURL": credentialed, "libraryID": "library-local"})
	wantGraphError(t, resp, ErrorCodeInvalid, "feedURL must be a public http or https address")
	if strings.Contains(body, "hush") {
		t.Fatalf("credentialed URL echoed: %s", body)
	}
	if fx.prober.calls != 1 {
		t.Fatalf("prober calls = %d, want 1 (credentialed URL never probed)", fx.prober.calls)
	}
}

func TestFollowFeedEverythingNeedsConfirmation(t *testing.T) {
	fx := newFeedFixture(t)
	_, resp := rawGraph(t, fx.handler, authorizeViewer, followFeedMutation,
		map[string]any{"feedURL": secretFeedURL, "libraryID": "library-local", "downloadPolicy": "everything"})
	wantGraphError(t, resp, ErrorCodeInvalid, "about 3000 bytes")

	var info SubscriptionInfo
	execGraph(t, fx.handler, authorizeViewer, followFeedMutation,
		map[string]any{"feedURL": secretFeedURL, "libraryID": "library-local", "downloadPolicy": "everything", "confirmEverything": true},
		"followFeed", &info)
	if info.DownloadPolicy != "everything" {
		t.Fatalf("policy = %q", info.DownloadPolicy)
	}
}

func followTestFeed(t *testing.T, fx feedFixture) SubscriptionInfo {
	t.Helper()
	var info SubscriptionInfo
	execGraph(t, fx.handler, authorizeManager, followFeedMutation,
		map[string]any{"feedURL": secretFeedURL, "libraryID": "library-local"}, "followFeed", &info)
	return info
}

func TestRefreshSubscriptionRejectsManagedProfileAndAllowsManager(t *testing.T) {
	fx := newFeedFixture(t)
	sub := followTestFeed(t, fx)
	const refresh = `mutation R($id: String!) { refreshSubscription(id: $id) { id healthStatus } }`
	variables := map[string]any{"id": sub.ID}
	_, resp := rawGraph(t, fx.handler, authorizeManagedProfile, refresh, variables)
	wantGraphError(t, resp, ErrorCodeForbidden, "adult profile or manager role required")

	var refreshed SubscriptionRefreshInfo
	execGraph(t, fx.handler, authorizeManager, refresh, variables, "refreshSubscription", &refreshed)
	if refreshed.ID != sub.ID || refreshed.HealthStatus != string(store.FeedHealthOK) {
		t.Fatalf("manager refresh = %+v", refreshed)
	}

	_, resp = rawGraph(t, fx.handler, authorizeManager, refresh, variables)
	wantGraphError(t, resp, ErrorCodeThrottled, "subscription was refreshed recently; try again later")
}

func TestSetSubscriptionDownloadPolicyIsManagerGatedAndConfirmsEverything(t *testing.T) {
	fx := newFeedFixture(t)
	sub := followTestFeed(t, fx)
	id := store.SubscriptionID(sub.ID)
	fx.catalogue.entries[id] = []store.SubscriptionEntry{
		{SubscriptionID: id, MediaID: "m1", EnclosureLength: 1000},
		{SubscriptionID: id, MediaID: "m2", EnclosureLength: 2500},
	}
	const setPolicy = `mutation P($id: String!, $downloadPolicy: String!, $latestN: Int, $confirmEverything: Boolean) {
  setSubscriptionDownloadPolicy(id: $id, downloadPolicy: $downloadPolicy, latestN: $latestN, confirmEverything: $confirmEverything) { id downloadPolicy downloadLatestN }
}`

	_, resp := rawGraph(t, fx.handler, authorizeViewer, setPolicy, map[string]any{"id": sub.ID, "downloadPolicy": "new-only"})
	if len(resp.Errors) == 0 || resp.Errors[0].Extensions.Code != ErrorCodeForbidden {
		t.Fatalf("viewer setSubscriptionDownloadPolicy errors = %+v, want FORBIDDEN", resp.Errors)
	}

	_, resp = rawGraph(t, fx.handler, authorizeManager, setPolicy, map[string]any{"id": sub.ID, "downloadPolicy": "everything"})
	wantGraphError(t, resp, ErrorCodeInvalid, "about 3500 bytes")

	_, resp = rawGraph(t, fx.handler, authorizeManager, setPolicy, map[string]any{"id": sub.ID, "downloadPolicy": "sometimes"})
	wantGraphError(t, resp, ErrorCodeInvalid, "downloadPolicy is not a supported policy")

	kicks := fx.poller.count()
	var info SubscriptionInfo
	execGraph(t, fx.handler, authorizeManager, setPolicy,
		map[string]any{"id": sub.ID, "downloadPolicy": "everything", "confirmEverything": true}, "setSubscriptionDownloadPolicy", &info)
	if info.DownloadPolicy != "everything" {
		t.Fatalf("policy = %q", info.DownloadPolicy)
	}
	execGraph(t, fx.handler, authorizeManager, setPolicy,
		map[string]any{"id": sub.ID, "downloadPolicy": "latest", "latestN": 5}, "setSubscriptionDownloadPolicy", &info)
	if info.DownloadPolicy != "latest" || info.DownloadLatestN != 5 {
		t.Fatalf("policy = %+v", info)
	}
	if fx.poller.count() != kicks+2 {
		t.Fatalf("poller kicks = %d, want %d", fx.poller.count(), kicks+2)
	}
}

func TestSubscriptionEntriesAndCountsNeverCarryEnclosureURLs(t *testing.T) {
	fx := newFeedFixture(t)
	sub := followTestFeed(t, fx)
	id := store.SubscriptionID(sub.ID)
	published := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	created, err := fx.st.UpsertSubscriptionCatalog(t.Context(), id, []store.FeedCatalogEntry{
		{GUID: "g1", Title: "Newest"},
		{GUID: "g2", Title: "Older", Position: 1},
	}, published)
	if err != nil || len(created) != 2 {
		t.Fatalf("seed Media: %d entries, %v", len(created), err)
	}
	fx.catalogue.entries[id] = []store.SubscriptionEntry{
		{SubscriptionID: id, GUID: "g1", MediaID: created[0].MediaID, Title: "Newest", FeedPosition: 0, PublishedAt: published,
			EnclosureURL: "https://cdn.example.com/a.mp3?sig=hush", EnclosureLength: 1000, DownloadState: store.EntryDownloadDownloaded, Downloaded: true},
		{SubscriptionID: id, GUID: "g2", MediaID: created[1].MediaID, Title: "Older", FeedPosition: 1,
			EnclosureURL: "https://cdn.example.com/b.mp3?sig=hush", EnclosureLength: 2000, DownloadState: store.EntryDownloadNone},
	}

	body, resp := rawGraph(t, fx.handler, authorizeViewer,
		`query E($subscriptionID: String!) { subscriptionEntries(subscriptionID: $subscriptionID) { mediaID title publishedAt feedPosition enclosureLength downloadState availability } }`,
		map[string]any{"subscriptionID": sub.ID})
	if len(resp.Errors) > 0 {
		t.Fatalf("errors = %+v", resp.Errors)
	}
	if strings.Contains(body, "cdn.example.com") || strings.Contains(body, "hush") {
		t.Fatalf("subscriptionEntries leaked an enclosure URL: %s", body)
	}
	var entries []SubscriptionEntryInfo
	if err := json.Unmarshal(resp.Data["subscriptionEntries"], &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].MediaID != string(created[0].MediaID) || entries[1].MediaID != string(created[1].MediaID) {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Availability != "downloaded" || entries[1].Availability != "not-downloaded" || entries[0].PublishedAt != "2026-11-01T09:00:00Z" {
		t.Fatalf("entries = %+v", entries)
	}

	_, resp = rawGraph(t, fx.handler, authorizeViewer,
		`query E($subscriptionID: String!) { subscriptionEntries(subscriptionID: $subscriptionID) { mediaID } }`,
		map[string]any{"subscriptionID": "missing"})
	wantGraphError(t, resp, ErrorCodeNotFound, "unknown subscription")

	var listed []SubscriptionInfo
	execGraph(t, fx.handler, authorizeManager,
		`query S($libraryID: String!) { subscriptions(libraryID: $libraryID) { id entryCount downloadedCount catalogBytes healthStatus } }`,
		map[string]any{"libraryID": "library-local"}, "subscriptions", &listed)
	if len(listed) != 1 || listed[0].EntryCount != 2 || listed[0].DownloadedCount != 1 || listed[0].CatalogBytes != 3000 || listed[0].HealthStatus != string(store.FeedHealthOK) {
		t.Fatalf("subscriptions = %+v", listed)
	}
}

func TestSubscriptionEntriesUseLibraryMediaVisibility(t *testing.T) {
	fx := newFeedFixture(t)
	fx.handler.feedCatalogue = nil
	ctx := t.Context()
	library, err := fx.st.CreateLibrary(ctx, "Feed Library", false, "example-feeds", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sub, err := fx.st.AddSubscription(ctx, store.Subscription{
		LibraryID: library.ID, FeedURL: secretFeedURL, Root: "/media/Feed",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	created, err := fx.st.UpsertSubscriptionCatalog(ctx, sub.ID, []store.FeedCatalogEntry{
		{GUID: "blocked", Title: "Blocked"},
		{GUID: "trash", Title: "Trash", Position: 1},
		{GUID: "visible", Title: "Visible", Position: 2},
	}, now)
	if err != nil || len(created) != 3 {
		t.Fatalf("seed feed: %d entries, %v", len(created), err)
	}
	for _, entry := range created[1:] {
		if err := fx.st.SetMediaFields(ctx, entry.MediaID, []store.MediaField{
			{Name: "certification", Type: store.EntityFieldText, Text: "allowed"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	scope := auth.Scope{AllLibraries: true, Certification: []store.CertificationConstraint{
		{PluginID: "example-feeds", Allowed: []string{"allowed"}},
	}}
	if err := fx.st.TrashMedia(ctx, created[1].MediaID, scope.LibraryFilter()); err != nil {
		t.Fatal(err)
	}
	items, err := fx.handler.subscriptionEntries(ctx, scope, request{
		Variables: map[string]any{"subscriptionID": string(sub.ID)},
	})
	if err != nil || len(items) != 1 || items[0].MediaID != string(created[2].MediaID) {
		t.Fatalf("subscriptionEntries = %+v, %v", items, err)
	}
}

func TestSanitizeFeedFolder(t *testing.T) {
	for in, want := range map[string]string{
		"Daily News!":      "Daily News",
		"  ../..//etc  ":   "etc",
		"":                 "feed",
		"Café Talk":        "Caf-Talk",
		"a__b  c":          "a_b c",
		"???":              "feed",
		"Talk: The Return": "Talk-The Return",
	} {
		if got := sanitizeFeedFolder(in); got != want {
			t.Errorf("sanitizeFeedFolder(%q) = %q, want %q", in, got, want)
		}
	}
}
