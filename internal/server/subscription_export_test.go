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
		privateURL   = "https://feeds.example.test/private.xml"
		privateRef   = "test-only-auth-reference"
		privateTitle = "Private feed test-only-title-token"
		hiddenURL    = "https://feeds.example.test/hidden.xml"
		passkeyURL   = "https://feeds.example.test/passkey.xml?passkey=test-only-feed-passkey" //nolint:gosec // synthetic feed URL for the credential exclusion test
		queryURL     = "https://feeds.example.test/query.xml?format=xml"
		emptyQuery   = "https://feeds.example.test/empty-query.xml?"
		fragmentURL  = "https://feeds.example.test/fragment.xml#section"
		userinfoURL  = "https://test-only-feed-user@feeds.example.test/basic.xml"
		serverURL    = "https://canonical.example.test:8443/internal.xml"
		requestURL   = "https://SERVER.example.test./local.xml"
		pathTokenURL = "https://feeds.example.test/weekly-digest/feed.xml" //nolint:gosec // synthetic path credential for the exclusion test
		privatePath  = "https://feeds.example.test/early-access/feed.xml"
	)
	t.Setenv("BEAMFALL_BASE_URL", "https://canonical.example.test")
	t.Setenv("BEAMFALL_SUBSCRIPTION_EXPORT_URLS", strings.Join([]string{
		publicURL, untitledURL, privateURL, hiddenURL, passkeyURL, queryURL, emptyQuery,
		fragmentURL, userinfoURL, serverURL, requestURL,
	}, "\n"))
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
		{LibraryID: second.ID, FeedURL: privateURL, Title: privateTitle, AuthRef: privateRef},
		{LibraryID: "library-archive", FeedURL: hiddenURL, Title: "Restricted feed"},
		{LibraryID: "library-local", FeedURL: "https://feeds.example.test/token.xml?token=test-only-feed-token"},
		{LibraryID: "library-local", FeedURL: passkeyURL},
		{LibraryID: second.ID, FeedURL: passkeyURL, AuthRef: privateRef},
		{LibraryID: "library-local", FeedURL: queryURL},
		{LibraryID: "library-local", FeedURL: emptyQuery},
		{LibraryID: "library-local", FeedURL: fragmentURL},
		{LibraryID: "library-local", FeedURL: userinfoURL},
		{LibraryID: "library-local", FeedURL: serverURL},
		{LibraryID: "library-local", FeedURL: requestURL},
		{LibraryID: "library-local", FeedURL: pathTokenURL},
		{LibraryID: second.ID, FeedURL: privatePath, AuthRef: privateRef},
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
			"AuthRef", privateRef, privateTitle, hiddenURL, "test-only-feed-", "passkey", "query.xml", "fragment.xml", "<!--",
			"canonical.example.test", "server.example.test", "test-only/download/root", "dev-manager-secret",
			"weekly-digest", "early-access",
		} {
			if strings.Contains(strings.ToLower(download.Body.String()), strings.ToLower(forbidden)) {
				t.Fatalf("download contains %q", forbidden)
			}
		}
	}

	var document struct {
		XMLName  xml.Name              `xml:"opml"`
		Version  string                `xml:"version,attr"`
		Outlines []subscriptionOutline `xml:"body>outline"`
	}
	if err := xml.Unmarshal(public.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != "2.0" || len(document.Outlines) != 2 {
		t.Fatalf("expected two OPML 2.0 outlines, got %#v", document)
	}
	for _, outline := range document.Outlines {
		if outline.Type != "rss" || outline.XMLURL == privateURL {
			t.Fatalf("unexpected public outline: %#v", outline)
		}
	}
	document.Outlines = nil
	if err := xml.Unmarshal(private.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Outlines) != 3 || !slices.ContainsFunc(document.Outlines, func(outline subscriptionOutline) bool {
		return outline.Type == "rss" && outline.XMLURL == privateURL && outline.Text == privateURL && outline.Title == ""
	}) {
		t.Fatalf("expected the private URL as the third feed without its stored title, got %#v", document.Outlines)
	}

	imported := importSubscriptionOPML(t, public.Body.Bytes())
	want := map[string]string{publicURL: `Daily & <News> "Live"`, untitledURL: ""}
	if !maps.Equal(imported, want) {
		t.Fatalf("round-trip Subscriptions = %#v, want %#v", imported, want)
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
	t.Setenv("BEAMFALL_SUBSCRIPTION_EXPORT_URLS", feedURL)
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
	t.Setenv("BEAMFALL_SUBSCRIPTION_EXPORT_URLS", visibleURL+" "+restoredURL)
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
	subs := make([]store.Subscription, limit+1)
	approved := make(map[string]struct{}, len(subs))
	for i := range limit + 1 {
		feedURL := fmt.Sprintf("https://feeds.example.test/feed-%d.xml", i)
		subs[i] = store.Subscription{FeedURL: feedURL, Title: fmt.Sprintf("Feed %d", i)}
		approved[feedURL] = struct{}{}
	}
	st := subscriptionLimitStore{"first": subs[:limit]}
	srv := newTestServer(t)
	srv.subscriptionExport = st
	srv.subscriptionURLs = approved
	body := downloadSubscriptionOPML(t, srv, "").Body.Bytes()
	imported := importSubscriptionOPML(t, body)
	if len(imported) != limit {
		t.Fatalf("pinned importer stored %d Subscriptions, want %d", len(imported), limit)
	}
	for _, sub := range subs[:limit] {
		if got := imported[sub.FeedURL]; got != sub.Title {
			t.Fatalf("round-trip title for %s = %q, want %q", sub.FeedURL, got, sub.Title)
		}
	}
	st["second"] = subs[limit:]
	assertSubscriptionExportLimitError(t, srv)
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
	srv.subscriptionURLs = map[string]struct{}{smallURL: {}, feedURL: {}, feedURL + "x": {}}
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
		t.Fatalf("expected an error without an attachment, got status %d and %d body bytes", rec.Code, rec.Body.Len())
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
