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
	t.Setenv("BEAMFALL_BASE_URL", "https://server.example.test")
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

	publicURL := "https://feeds.example.test/public.xml"
	untitledURL := "https://feeds.example.test/untitled.xml"
	privateURL := "https://feeds.example.test/private.xml"
	privateRef := "test-only-auth-reference"
	hiddenURL := "https://feeds.example.test/hidden.xml"
	for _, sub := range []store.Subscription{
		{LibraryID: "library-local", FeedURL: publicURL, Title: `Daily & <News> "Live"`},
		{LibraryID: second.ID, FeedURL: untitledURL},
		{LibraryID: second.ID, FeedURL: privateURL, Title: "Private feed", AuthRef: privateRef},
		{LibraryID: "library-archive", FeedURL: hiddenURL, Title: "Restricted feed"},
		{LibraryID: "library-local", FeedURL: "https://feeds.example.test/token.xml?token=test-only-feed-token"},
		{LibraryID: "library-local", FeedURL: "https://feeds.example.test/passkey.xml?passkey=test-only-feed-passkey"},
		{LibraryID: "library-local", FeedURL: "https://feeds.example.test/query.xml?format=xml"},
		{LibraryID: "library-local", FeedURL: "https://feeds.example.test/empty-query.xml?"},
		{LibraryID: "library-local", FeedURL: "https://feeds.example.test/fragment.xml#section"},
		{LibraryID: "library-local", FeedURL: "https://test-only-feed-user@feeds.example.test/basic.xml"},
	} {
		sub.Root = "/test-only/download/root"
		if _, err := st.AddSubscription(ctx, sub, now); err != nil {
			t.Fatal(err)
		}
	}

	public := downloadSubscriptionOPML(t, srv)
	if got := public.Header().Get("Content-Type"); got != subscriptionExportContentType {
		t.Fatalf("content type = %q", got)
	}
	if got := public.Header().Get("Content-Disposition"); got != `attachment; filename="subscriptions.opml"` {
		t.Fatalf("content disposition = %q", got)
	}
	if got := public.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache control = %q", got)
	}
	for _, forbidden := range []string{
		"AuthRef", privateURL, privateRef, hiddenURL, "test-only-feed-", "query.xml", "fragment.xml", "<!--",
		"server.example.test", "test-only/download/root", "dev-manager-secret",
	} {
		if strings.Contains(public.Body.String(), forbidden) {
			t.Fatalf("download contains %q", forbidden)
		}
	}

	var document struct {
		XMLName  xml.Name `xml:"opml"`
		Version  string   `xml:"version,attr"`
		Outlines []struct {
			Type string `xml:"type,attr"`
		} `xml:"body>outline"`
	}
	if err := xml.Unmarshal(public.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != "2.0" || len(document.Outlines) != 2 {
		t.Fatalf("expected two OPML 2.0 outlines, got %#v", document)
	}
	for _, outline := range document.Outlines {
		if outline.Type != "rss" {
			t.Fatalf("outline type = %q", outline.Type)
		}
	}

	imported := importSubscriptionOPML(t, public.Body.Bytes())
	want := map[string]string{publicURL: `Daily & <News> "Live"`, untitledURL: ""}
	if !maps.Equal(imported, want) {
		t.Fatalf("round-trip Subscriptions = %#v, want %#v", imported, want)
	}
}

func downloadSubscriptionOPML(t *testing.T, srv *Server) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "https://server.example.test"+subscriptionExportPath, nil)
	authorizeManagerCookies(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("download status = %d: %s", rec.Code, rec.Body.String())
	}
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
	srv, st := newTestServerWithStore(t)
	const libraryID = "library-local"
	const feedURL = "https://feeds.example.test/sealable.xml"
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
		if got := strings.Contains(downloadSubscriptionOPML(t, srv).Body.String(), feedURL); got != want {
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

type subscriptionLimitStore struct {
	subscriptions []store.Subscription
	reads         []store.LibraryID
}

func (*subscriptionLimitStore) Libraries(context.Context, store.LibraryFilter) ([]store.Library, error) {
	return []store.Library{{ID: "first"}, {ID: "second"}}, nil
}

func (s *subscriptionLimitStore) Subscriptions(_ context.Context, id store.LibraryID) ([]store.Subscription, error) {
	s.reads = append(s.reads, id)
	return s.subscriptions, nil
}

func TestSubscriptionExportOutlineLimit(t *testing.T) {
	const limit = 10_000
	st := &subscriptionLimitStore{}
	for i := range limit + 1 {
		st.subscriptions = append(st.subscriptions, store.Subscription{
			FeedURL: fmt.Sprintf("https://feeds.example.test/feed-%d.xml", i),
		})
	}
	srv := &Server{subscriptionExport: st}
	body, err := srv.subscriptionOPML(t.Context(), store.LibraryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Outlines []subscriptionOutline `xml:"body>outline"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Outlines) != limit || doc.Outlines[limit-1].XMLURL != st.subscriptions[limit-1].FeedURL {
		t.Fatalf("expected the first %d outlines, got %d", limit, len(doc.Outlines))
	}
	if len(st.reads) != 1 {
		t.Fatalf("read Libraries after reaching limit: %v", st.reads)
	}
}

func TestSubscriptionExportByteLimit(t *testing.T) {
	const limit = 64 << 20
	const smallURL = "https://feeds.example.test/small.xml"
	const largeURL = "https://feeds.example.test/large.xml"
	smallOutline, err := xml.Marshal(subscriptionOutline{Type: "rss", XMLURL: smallURL})
	if err != nil {
		t.Fatal(err)
	}
	largeOutline, err := xml.Marshal(subscriptionOutline{Type: "rss", XMLURL: largeURL})
	if err != nil {
		t.Fatal(err)
	}
	smallDocumentBytes := len(subscriptionOPMLStart) + len(smallOutline) + len(subscriptionOPMLEnd)
	remaining := limit - smallDocumentBytes - len(largeOutline)
	// Each ampersand takes five bytes in both the text and title attributes.
	// Leave room for a long unescaped span to exercise both encoder paths.
	plain := 8 << 10
	escaped := (remaining - 2*plain) / 10
	title := strings.Repeat("&", escaped) + strings.Repeat("x", (remaining-10*escaped)/2)
	url := largeURL + strings.Repeat("x", remaining%2)
	st := &subscriptionLimitStore{subscriptions: []store.Subscription{
		{FeedURL: smallURL},
		{FeedURL: url},
		{FeedURL: "https://feeds.example.test/after.xml"},
	}}
	srv := &Server{subscriptionExport: st}
	for _, extra := range []string{"", "x"} {
		st.reads = nil
		st.subscriptions[1].Title = title + extra
		body, err := srv.subscriptionOPML(t.Context(), store.LibraryFilter{})
		if err != nil {
			t.Fatal(err)
		}
		wantBytes := limit
		wantOutlines := 2
		if extra != "" {
			wantBytes = smallDocumentBytes
			wantOutlines = 1
		}
		if len(body) != wantBytes || bytes.Count(body, []byte("<outline ")) != wantOutlines || !bytes.HasSuffix(body, []byte(subscriptionOPMLEnd)) {
			t.Fatalf("extra title %q: expected %d complete outlines in %d bytes, got %d bytes", extra, wantOutlines, wantBytes, len(body))
		}
		if len(st.reads) != 1 {
			t.Fatalf("read Libraries after reaching byte limit: %v", st.reads)
		}
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
