package server

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/migrate"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	feeds "github.com/beamfall/plugin-podcasts"
)

func TestSubscriptionExportOPML(t *testing.T) {
	const (
		publicURL    = "https://feeds.example.test/public.xml"
		untitledURL  = "https://feeds.example.test/untitled.xml"
		queryURL     = "https://feeds.example.test/query.xml?format=xml&page=2"
		slugURL      = "https://feeds.example.test/shows/The-Best-Cooking-Podcast-of-2024-and-Beyond/feed.xml"
		objectIDURL  = "https://feeds.example.test/public/shows/5e7b777ba085cbe7192b0607"
		fragmentURL  = "https://feeds.example.test/fragment.xml#section"
		privateURL   = "https://feeds.example.test/private.xml"
		privateRef   = "test-only-auth-reference"
		privateTitle = "Private feed for test-only-subscriber"
		userinfoURL  = "https://test-only-feed-user:test-only-feed-pass@feeds.example.test/basic.xml" //nolint:gosec // synthetic feed credential for the exclusion test
		tokenURL     = "https://feeds.example.test/token.xml?format=xml&token=test-only-feed-token"   //nolint:gosec // synthetic feed URL for the credential exclusion test
		passkeyURL   = "https://feeds.example.test/passkey.xml?PassKey=test-only-feed-passkey"        //nolint:gosec // synthetic feed URL for the credential exclusion test
		opaqueURL    = "https://feeds.example.test/opaque.xml?u=Zt9q4XWm%2BbR7k/Lp3%3D&lang=en;session=test-only-feed-session"
		uuidPathURL  = "https://feeds.example.test/private/0f8fad5b-d9cb-469f-a165-70867728950e.rss"
		opaquePath   = "https://feeds.example.test/episodes/aB3dE5fG7hJ9kL1mN3pQ5rS7tU9vW1xY.xml"
		hiddenURL    = "https://feeds.example.test/hidden.xml"
		serverURL    = "https://canonical.example.test:8443/internal.xml"
		requestURL   = "https://SERVER.example.test./local.xml"
	)
	t.Setenv("BEAMFALL_BASE_URL", "https://canonical.example.test")
	srv, st := newTestServerWithStore(t)
	ctx := t.Context()
	now := time.Unix(0, 0).UTC()
	if err := st.SetSessionAdminUntil(ctx, "session-manager", now); err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateLibrary(ctx, "Second Library", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}

	for _, sub := range []store.Subscription{
		{LibraryID: "library-local", FeedURL: publicURL, Title: `Daily & <News> "Live"`},
		{LibraryID: second.ID, FeedURL: untitledURL},
		{LibraryID: "library-local", FeedURL: queryURL, Title: "Query"},
		{LibraryID: "library-local", FeedURL: slugURL, Title: "Slug"},
		{LibraryID: "library-local", FeedURL: objectIDURL, Title: "Object ID"},
		{LibraryID: "library-local", FeedURL: fragmentURL, Title: "Fragment"},
		{LibraryID: second.ID, FeedURL: privateURL, Title: privateTitle, AuthRef: privateRef},
		{LibraryID: "library-local", FeedURL: userinfoURL, Title: privateTitle},
		{LibraryID: "library-local", FeedURL: tokenURL, Title: privateTitle},
		{LibraryID: "library-local", FeedURL: passkeyURL, Title: privateTitle},
		{LibraryID: "library-local", FeedURL: opaqueURL, Title: privateTitle},
		{LibraryID: "library-local", FeedURL: uuidPathURL, Title: privateTitle},
		{LibraryID: second.ID, FeedURL: opaquePath, Title: privateTitle, AuthRef: privateRef},
		{LibraryID: "library-archive", FeedURL: hiddenURL, Title: "Restricted feed"},
		{LibraryID: "library-local", FeedURL: serverURL, Title: "Own server"},
		{LibraryID: second.ID, FeedURL: requestURL, Title: "Request host", AuthRef: privateRef},
	} {
		sub.Root = "/test-only/download/root"
		if _, err := st.AddSubscription(ctx, sub, now); err != nil {
			t.Fatal(err)
		}
	}

	public := downloadSubscriptionOPML(t, srv, "")
	if got := public.Header().Get("Content-Type"); got != subscriptionExportContentType {
		t.Fatalf("content type = %q", got)
	}
	if got := public.Header().Get("Content-Disposition"); got != `attachment; filename="subscriptions.opml"` {
		t.Fatalf("content disposition = %q", got)
	}
	if got := public.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache control = %q", got)
	}
	private := downloadSubscriptionOPML(t, srv, "?includePrivate=true")
	for _, download := range []*httptest.ResponseRecorder{public, private} {
		for _, forbidden := range []string{
			"AuthRef", privateRef, privateTitle, hiddenURL, "test-only-feed-", "passkey=", "token=", "Zt9q4XWm", "session=",
			"@", "#section", "<!--", "0f8fad5b", "aB3dE5fG7h", "canonical.example.test", "server.example.test",
			"test-only/download/root", "dev-manager-secret",
		} {
			if strings.Contains(strings.ToLower(download.Body.String()), strings.ToLower(forbidden)) {
				t.Fatalf("download contains %q", forbidden)
			}
		}
	}

	wantPublic := map[string]string{
		publicURL:   `Daily & <News> "Live"`,
		untitledURL: "",
		queryURL:    "Query",
		slugURL:     "Slug",
		objectIDURL: "Object ID",
		"https://feeds.example.test/fragment.xml": "Fragment",
	}
	assertSubscriptionOutlines(t, public.Body.Bytes(), wantPublic, nil)
	wantPrivate := []string{
		privateURL,
		"https://feeds.example.test/basic.xml",
		"https://feeds.example.test/token.xml?format=xml",
		"https://feeds.example.test/passkey.xml",
		"https://feeds.example.test/opaque.xml",
	}
	assertSubscriptionOutlines(t, private.Body.Bytes(), wantPublic, wantPrivate)

	imported := importSubscriptionOPML(t, public.Body.Bytes())
	if !maps.Equal(imported, wantPublic) {
		t.Fatalf("round-trip Subscriptions = %#v, want %#v", imported, wantPublic)
	}
}

// assertSubscriptionOutlines checks an export holds exactly the public
// URL-to-title outlines plus private outlines that use the URL as their text.
func assertSubscriptionOutlines(t *testing.T, body []byte, public map[string]string, private []string) {
	t.Helper()
	var document struct {
		XMLName  xml.Name              `xml:"opml"`
		Version  string                `xml:"version,attr"`
		Outlines []subscriptionOutline `xml:"body>outline"`
	}
	if err := xml.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != "2.0" || len(document.Outlines) != len(public)+len(private) {
		t.Fatalf("expected %d OPML 2.0 outlines, got %#v", len(public)+len(private), document)
	}
	for _, outline := range document.Outlines {
		title, isPublic := public[outline.XMLURL]
		switch {
		case outline.Type != "rss":
			t.Fatalf("outline type = %q", outline.Type)
		case isPublic && (outline.Text != title || outline.Title != title):
			t.Fatalf("public outline %#v, want title %q", outline, title)
		case !isPublic && (!slices.Contains(private, outline.XMLURL) || outline.Text != outline.XMLURL || outline.Title != ""):
			t.Fatalf("unexpected private outline %#v", outline)
		}
	}
}

func TestSubscriptionExportExcludesOwnHostIPLiterals(t *testing.T) {
	const otherURL = "http://[2001:db8::3]/other.xml"
	srv := newTestServer(t)
	baseURL, err := url.Parse("https://[2001:db8:0:0:0:0:0:1]:8443")
	if err != nil {
		t.Fatal(err)
	}
	srv.baseURL = baseURL
	srv.subscriptionExport = subscriptionLimitStore{"first": {
		// The compressed spelling of the expanded base URL address.
		{FeedURL: "http://[2001:db8::1]/base.xml", Title: "Base"},
		// The expanded spelling of the compressed request host address.
		{FeedURL: "https://[2001:0db8:0000:0000:0000:0000:0000:0002]/request.xml", Title: "Request"},
		{FeedURL: "http://[::ffff:192.0.2.10]/mapped.xml", Title: "Mapped"},
		{FeedURL: otherURL, Title: "Other"},
	}}
	for _, requestHost := range []string{"[2001:db8::2]:8080", "[2001:0db8:0:0:0:0:0:2]"} {
		body, err := srv.subscriptionOPML(t.Context(), store.LibraryFilter{}, true, requestHost)
		if err != nil {
			t.Fatal(err)
		}
		assertSubscriptionOutlines(t, body, map[string]string{otherURL: "Other", "http://[::ffff:192.0.2.10]/mapped.xml": "Mapped"}, nil)
	}
	body, err := srv.subscriptionOPML(t.Context(), store.LibraryFilter{}, true, "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("mapped.xml")) {
		t.Fatalf("export included the IPv4-mapped spelling of the request host: %s", body)
	}
}

func downloadSubscriptionOPML(t *testing.T, srv *Server, query string) *httptest.ResponseRecorder {
	t.Helper()
	rec := requestSubscriptionOPML(srv, query)
	if rec.Code != http.StatusOK {
		t.Fatalf("download status = %d: %s", rec.Code, rec.Body.String())
	}
	return rec
}

func requestSubscriptionOPML(srv *Server, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "https://server.example.test"+subscriptionExportPath+query, nil)
	authorizeManagerCookies(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

type subscriptionTestSecrets map[string]string

func (s subscriptionTestSecrets) Secret(ref string) (string, error) {
	if value, ok := s[ref]; ok {
		return value, nil
	}
	return "", os.ErrNotExist
}

func (s subscriptionTestSecrets) SetSecret(ref, value string) error {
	if value == "" {
		delete(s, ref)
	} else {
		s[ref] = value
	}
	return nil
}

func TestSubscriptionExportRespectsRuntimeSeal(t *testing.T) {
	const libraryID = "library-local"
	const feedURL = "https://feeds.example.test/sealable.xml"
	srv, st := newTestServerWithStore(t)
	_, err := st.AddSubscription(t.Context(), store.Subscription{
		LibraryID: libraryID, FeedURL: feedURL, Root: t.TempDir(),
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	secrets := subscriptionTestSecrets{}
	manager := libencrypt.NewManagerWithParams(secrets, libencrypt.TestArgon2Params)
	if _, err := manager.Provision(libraryID, libencrypt.UnsealModeAuto, ""); err != nil {
		t.Fatal(err)
	}
	srv.subscriptionSealed = func(id store.LibraryID) bool { return manager.SealedEncrypted(string(id)) }
	assertExported := func(want bool) {
		t.Helper()
		if got := strings.Contains(downloadSubscriptionOPML(t, srv, "").Body.String(), feedURL); got != want {
			t.Fatalf("export contains feed = %v, want %v", got, want)
		}
	}
	assertExported(true)
	manager.Seal(libraryID)
	assertExported(false)
	manager = libencrypt.NewManagerWithParams(secrets, libencrypt.TestArgon2Params)
	assertExported(false)
	if err := manager.AutoUnseal(libraryID); err != nil {
		t.Fatal(err)
	}
	assertExported(true)
}

func TestSubscriptionExportExcludesLibraryRestoredAfterEnumeration(t *testing.T) {
	const visibleURL = "https://feeds.example.test/visible.xml"
	const restoredURL = "https://feeds.example.test/restored.xml"
	const restoredTitle = "Sealed Library feed"
	srv, st := newTestServerWithStore(t)
	ctx := t.Context()
	restored, err := st.CreateLibrary(ctx, "Restored Library", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	visible, err := st.CreateLibrary(ctx, "Visible Library", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, sub := range []store.Subscription{
		{LibraryID: restored.ID, FeedURL: restoredURL, Title: restoredTitle, Root: root},
		{LibraryID: visible.ID, FeedURL: visibleURL, Root: root},
	} {
		if _, err := st.AddSubscription(ctx, sub, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.DeleteLibrary(ctx, restored.ID); err != nil {
		t.Fatal(err)
	}
	didRestore := false
	srv.subscriptionSealed = func(id store.LibraryID) bool {
		// The first seal check runs after enumeration. Returning true for an
		// original Library used to trigger a second query that admitted restored.
		if id == "library-local" {
			if err := st.RestoreLibrary(ctx, restored.ID); err != nil {
				t.Fatal(err)
			}
			didRestore = true
			return true
		}
		return id == restored.ID
	}
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local", visible.ID, restored.ID}}
	body, err := srv.subscriptionOPML(ctx, filter, false, "server.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if !didRestore || !bytes.Contains(body, []byte(visibleURL)) {
		t.Fatalf("expected a restore during export and the visible feed, got %s", body)
	}
	if bytes.Contains(body, []byte(restoredURL)) || bytes.Contains(body, []byte(restoredTitle)) {
		t.Fatalf("export included the restored sealed Library: %s", body)
	}
}

type subscriptionLimitStore map[store.LibraryID][]store.Subscription

func (subscriptionLimitStore) Libraries(context.Context, store.LibraryFilter) ([]store.Library, error) {
	return []store.Library{{ID: "first"}, {ID: "second"}}, nil
}

func (s subscriptionLimitStore) Subscriptions(_ context.Context, id store.LibraryID) ([]store.Subscription, error) {
	return s[id], nil
}

func TestSubscriptionExportOutlineLimit(t *testing.T) {
	const limit = 10_000
	srv, st := newTestServerWithStore(t)
	ctx := t.Context()
	now := time.Unix(0, 0).UTC()
	addFeed := func(i int) store.Subscription {
		t.Helper()
		sub, err := st.AddSubscription(ctx, store.Subscription{
			LibraryID: "library-local", Root: "/test-only/download/root",
			FeedURL: fmt.Sprintf("https://feeds.example.test/feed-%d.xml", i), Title: fmt.Sprintf("Feed %d", i),
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		return sub
	}
	subs := make([]store.Subscription, limit)
	for i := range limit {
		subs[i] = addFeed(i)
	}
	body := downloadSubscriptionOPML(t, srv, "").Body.Bytes()
	// Check the limit before the slow import: the test client's provisional
	// compatibility record expires in wall-clock time.
	addFeed(limit)
	assertSubscriptionExportLimitError(t, srv)
	imported := importSubscriptionOPML(t, body)
	if len(imported) != limit {
		t.Fatalf("pinned importer stored %d Subscriptions, want %d", len(imported), limit)
	}
	for _, sub := range subs {
		if got := imported[sub.FeedURL]; got != sub.Title {
			t.Fatalf("round-trip title for %s = %q, want %q", sub.FeedURL, got, sub.Title)
		}
	}
}

func TestSubscriptionExportByteLimit(t *testing.T) {
	const limit = 64 << 20
	const smallURL = "https://feeds.example.test/small.xml"
	const largeURL = "https://feeds.example.test/large.xml"
	smallOutline, err := xml.Marshal(subscriptionOutline{Type: "rss", Text: "Small", Title: "Small", XMLURL: smallURL})
	if err != nil {
		t.Fatal(err)
	}
	largeOutline, err := xml.Marshal(subscriptionOutline{Type: "rss", Title: "x", XMLURL: largeURL})
	if err != nil {
		t.Fatal(err)
	}
	// The large outline already has a one-byte placeholder in its title.
	remaining := limit - len(subscriptionOPMLStart) - len(smallOutline) - len(largeOutline) - len(subscriptionOPMLEnd) + 1
	// Each ampersand takes five bytes in both the text and title attributes.
	// Leave room for a long unescaped span to exercise both encoder paths.
	plain := 8 << 10
	escaped := (remaining - 2*plain) / 10
	title := strings.Repeat("&", escaped) + strings.Repeat("x", (remaining-10*escaped)/2)
	feedURL := largeURL + strings.Repeat("x", remaining%2)
	st := subscriptionLimitStore{
		"first":  {{FeedURL: smallURL, Title: "Small"}},
		"second": {{FeedURL: feedURL, Title: title}},
	}
	srv := newTestServer(t)
	srv.subscriptionExport = st
	body := downloadSubscriptionOPML(t, srv, "").Body.Bytes()
	if len(body) != limit {
		t.Fatalf("export size = %d bytes, want %d", len(body), limit)
	}
	imported := importSubscriptionOPML(t, body)
	if len(imported) != 2 || imported[smallURL] != "Small" || imported[feedURL] != title {
		t.Fatal("pinned importer did not recover both URLs and titles from the exact-limit export")
	}
	st["second"][0].FeedURL += "x"
	assertSubscriptionExportLimitError(t, srv)
}

func assertSubscriptionExportLimitError(t *testing.T, srv *Server) {
	t.Helper()
	rec := requestSubscriptionOPML(srv, "")
	if rec.Code != http.StatusInternalServerError || rec.Header().Get("Content-Disposition") != "" ||
		!strings.Contains(rec.Body.String(), "could not export subscriptions") || strings.Contains(rec.Body.String(), "<opml") {
		t.Fatalf("expected an error without an attachment, got status %d: %.200s", rec.Code, rec.Body.String())
	}
}

type opmlTestResolver struct{}

func (opmlTestResolver) Resolve(_, _ string, _ []string, path string) (migrate.Source, bool) {
	return feeds.NewOPMLSource(path), true
}

func importSubscriptionOPML(t *testing.T, body []byte) map[string]string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "subscriptions.opml")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	st := storetest.Open(t)
	payload, err := json.Marshal(migrate.Payload{
		Version: 1, PluginID: string(feeds.Bundle().ID), ImporterID: feeds.OPMLImporterID,
		Categories: []string{migrate.CategorySubscriptions}, SourcePath: path,
		ProfileID: "profile-default", LibraryID: "library-local", Root: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	job, err := st.EnqueueJob(ctx, store.Job{
		Kind: migrate.JobKind, Class: store.JobClassBackground,
		DedupeKey: "test-opml-import", Payload: payload, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry(st, "test-opml-import")
	registry.Register(migrate.JobKind, migrate.Handler(st, opmlTestResolver{}))
	if _, err := registry.RunOne(ctx, nil, now); err != nil {
		t.Fatal(err)
	}
	job, err = st.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != store.JobStatusCompleted {
		t.Fatalf("import status = %s: %s", job.Status, job.Error)
	}
	subscriptions, err := st.AllSubscriptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	imported := make(map[string]string, len(subscriptions))
	for _, sub := range subscriptions {
		imported[sub.FeedURL] = sub.Title
	}
	return imported
}

func TestSubscriptionExportRequiresManager(t *testing.T) {
	srv := newTestServer(t)
	for _, tc := range []struct {
		name      string
		authorize func(*http.Request)
		status    int
	}{
		{name: "anonymous", status: http.StatusUnauthorized},
		{name: "viewer", authorize: authorizeViewer, status: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, subscriptionExportPath, nil)
			if tc.authorize != nil {
				tc.authorize(req)
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if rec.Header().Get("Content-Disposition") != "" {
				t.Fatal("authorization failure returned a download")
			}
		})
	}
}
