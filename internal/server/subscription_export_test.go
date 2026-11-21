package server

import (
	"encoding/json"
	"encoding/xml"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/jobs"
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

	publicURL := "https://feeds.example.test/public.xml?a=1&b=2"
	untitledURL := "https://feeds.example.test/untitled.xml"
	privateURL := "https://feeds.example.test/private.xml"
	privateRef := "test-only-auth-reference"
	hiddenURL := "https://feeds.example.test/hidden.xml"
	embeddedToken := "test-only-feed-token"
	embeddedPassword := "test-only-feed-password"
	for _, sub := range []store.Subscription{
		{LibraryID: "library-local", FeedURL: publicURL, Title: `Daily & <News> "Live"`},
		{LibraryID: second.ID, FeedURL: untitledURL},
		{LibraryID: second.ID, FeedURL: privateURL, Title: "Private feed", AuthRef: privateRef},
		{LibraryID: "library-archive", FeedURL: hiddenURL, Title: "Restricted feed"},
		{LibraryID: "library-local", FeedURL: "https://feeds.example.test/token.xml?ACCESS%5fTOKEN=" + embeddedToken},
		{LibraryID: "library-local", FeedURL: "https://user:" + embeddedPassword + "@feeds.example.test/basic.xml"},
	} {
		sub.Root = "/test-only/download/root"
		if _, err := st.AddSubscription(ctx, sub, now); err != nil {
			t.Fatal(err)
		}
	}

	download := func(query string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "https://server.example.test"+subscriptionExportPath+query, nil)
		for name, value := range map[string]string{
			auth.SessionCookie:      "session-manager",
			auth.DeviceCookie:       "device-mac",
			auth.DeviceSecretCookie: "dev-manager-secret",
		} {
			req.AddCookie(&http.Cookie{
				Name: name, Value: value, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
			})
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("download status = %d: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != subscriptionExportContentType {
			t.Fatalf("content type = %q", got)
		}
		if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="subscriptions.opml"` {
			t.Fatalf("content disposition = %q", got)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("cache control = %q", got)
		}
		for _, forbidden := range []string{
			"AuthRef", privateRef, hiddenURL, embeddedToken, embeddedPassword,
			"server.example.test", "test-only/download/root", "dev-manager-secret",
		} {
			if strings.Contains(rec.Body.String(), forbidden) {
				t.Fatalf("download contains %q", forbidden)
			}
		}
		return rec
	}

	public := download("")
	if strings.Contains(public.Body.String(), privateURL) {
		t.Fatal("default download contains the private feed URL")
	}
	for _, query := range []string{
		"?includePrivate=false", "?includePrivate=true&includePrivate=false",
		"?includePrivate=true&includePrivate=%zz",
	} {
		if strings.Contains(download(query).Body.String(), privateURL) {
			t.Fatal("download includes a private feed without an unambiguous opt-in")
		}
	}

	private := download("?includePrivate=true")
	var document struct {
		XMLName  xml.Name `xml:"opml"`
		Version  string   `xml:"version,attr"`
		Outlines []struct {
			Type string `xml:"type,attr"`
			URL  string `xml:"xmlUrl,attr"`
		} `xml:"body>outline"`
	}
	if err := xml.Unmarshal(private.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != "2.0" || len(document.Outlines) != 3 {
		t.Fatalf("expected three OPML 2.0 outlines, got %#v", document)
	}
	foundPrivate := false
	for _, outline := range document.Outlines {
		if outline.Type != "rss" {
			t.Fatalf("outline type = %q", outline.Type)
		}
		foundPrivate = foundPrivate || outline.URL == privateURL
	}
	if !foundPrivate {
		t.Fatal("opt-in download omits the private feed URL")
	}

	imported := importSubscriptionOPML(t, public.Body.Bytes())
	want := map[string]string{publicURL: `Daily & <News> "Live"`, untitledURL: ""}
	if !maps.Equal(imported, want) {
		t.Fatalf("round-trip Subscriptions = %#v, want %#v", imported, want)
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
			req := httptest.NewRequest(http.MethodGet, subscriptionExportPath+"?includePrivate=true", nil)
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
