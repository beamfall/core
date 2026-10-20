package qbittorrent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeQBT is an httptest-backed qBittorrent Web API for client tests: it issues
// a session cookie on login and refuses unauthenticated data requests with 403,
// exercising the login + re-login paths.
type fakeQBT struct {
	username        string
	password        string
	categories      string // raw JSON body for /torrents/categories
	completed       string // raw JSON body for /torrents/info?filter=completed
	files           string // raw JSON body for /torrents/files
	defaultSavePath string // raw text body for /app/defaultSavePath
	loginHits       int
	requireAuth     bool
}

func (f *fakeQBT) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		f.loginHits++
		_ = r.ParseForm()
		if r.FormValue("username") != f.username || r.FormValue("password") != f.password {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Fails."))
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: "session-token", Path: "/"})
		_, _ = w.Write([]byte("Ok."))
	})
	authed := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if f.requireAuth {
				if cookie, err := r.Cookie("SID"); err != nil || cookie.Value != "session-token" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
			}
			_, _ = w.Write([]byte(body))
		}
	}
	mux.HandleFunc("/api/v2/torrents/categories", authed(f.categories))
	mux.HandleFunc("/api/v2/torrents/info", authed(f.completed))
	mux.HandleFunc("/api/v2/torrents/files", authed(f.files))
	mux.HandleFunc("/api/v2/app/defaultSavePath", authed(f.defaultSavePath))
	return mux
}

func newTestClient(t *testing.T, fake *fakeQBT) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	client, err := NewClient(Credentials{BaseURL: srv.URL, Username: fake.username, Password: fake.password})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	// Use the httptest server's client (its cookie jar/transport) but keep the
	// hardened default jar by swapping only when needed; here we attach a jar so
	// the session cookie round-trips.
	client.SetHTTPClient(&http.Client{Jar: mustJar(t)})
	return client, srv
}

func mustJar(t *testing.T) http.CookieJar {
	t.Helper()
	jar := newCookieJar()
	if jar == nil {
		t.Fatal("cookie jar unavailable")
	}
	return jar
}

func TestClientLoginSetsSession(t *testing.T) {
	fake := &fakeQBT{username: "admin", password: "secret", requireAuth: true, categories: `{}`}
	client, _ := newTestClient(t, fake)
	if err := client.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if fake.loginHits != 1 {
		t.Fatalf("expected 1 login, got %d", fake.loginHits)
	}
}

func TestClientLoginRejectsBadCredentials(t *testing.T) {
	fake := &fakeQBT{username: "admin", password: "secret", requireAuth: true}
	client, srv := newTestClient(t, fake)
	bad, err := NewClient(Credentials{BaseURL: srv.URL, Username: "admin", Password: "wrong"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	bad.SetHTTPClient(&http.Client{Jar: mustJar(t)})
	err = bad.Login(context.Background())
	if !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("expected ErrAuthRequired, got %v", err)
	}
	_ = client
}

func TestClientCategories(t *testing.T) {
	fake := &fakeQBT{
		username: "admin", password: "secret", requireAuth: true,
		categories: `{"films":{"name":"films","savePath":"/downloads/films"},"audio":{"name":"","savePath":"/downloads/audio"}}`,
	}
	client, _ := newTestClient(t, fake)
	categories, err := client.Categories(context.Background())
	if err != nil {
		t.Fatalf("Categories: %v", err)
	}
	if len(categories) != 2 {
		t.Fatalf("expected 2 categories, got %d", len(categories))
	}
	names := map[string]string{}
	for _, c := range categories {
		names[c.Name] = c.SavePath
	}
	if names["films"] != "/downloads/films" {
		t.Errorf("films save path: got %q", names["films"])
	}
	// The blank-name value is keyed by the map key, not the value's Name field.
	if names["audio"] != "/downloads/audio" {
		t.Errorf("audio category not keyed by map name: %v", names)
	}
}

// TestClientDefaultSavePath: the global default save path comes back as plain
// text and is trimmed — it completes the category save paths as the endpoint's
// download roots captured at configuration time.
func TestClientDefaultSavePath(t *testing.T) {
	fake := &fakeQBT{
		username: "admin", password: "secret", requireAuth: true,
		defaultSavePath: "/downloads\n",
	}
	client, _ := newTestClient(t, fake)
	got, err := client.DefaultSavePath(context.Background())
	if err != nil {
		t.Fatalf("DefaultSavePath: %v", err)
	}
	if got != "/downloads" {
		t.Fatalf("default save path = %q, want /downloads", got)
	}
}

func TestClientCompletedTorrents(t *testing.T) {
	fake := &fakeQBT{
		username: "admin", password: "secret", requireAuth: true,
		completed: `[{"hash":"ABCDEF","name":"Release.Name.2024","category":"films","content_path":"/downloads/films/Release","save_path":"/downloads/films","progress":1,"state":"stalledUP"}]`,
	}
	client, _ := newTestClient(t, fake)
	torrents, err := client.CompletedTorrents(context.Background())
	if err != nil {
		t.Fatalf("CompletedTorrents: %v", err)
	}
	if len(torrents) != 1 || torrents[0].Hash != "ABCDEF" || torrents[0].Category != "films" {
		t.Fatalf("unexpected torrents: %+v", torrents)
	}
}

func TestClientReLoginsOnExpiredSession(t *testing.T) {
	// requireAuth with a fresh client whose jar starts empty: the first data
	// request is unauthenticated (403), the client re-logs in and retries.
	fake := &fakeQBT{
		username: "admin", password: "secret", requireAuth: true,
		categories: `{}`,
	}
	client, _ := newTestClient(t, fake)
	// Force loggedIn=true without a cookie so the first request hits 403 and
	// triggers the re-login path.
	client.loggedIn = true
	if _, err := client.Categories(context.Background()); err != nil {
		t.Fatalf("Categories after expiry: %v", err)
	}
	if fake.loginHits < 1 {
		t.Fatalf("expected a re-login, got %d login hits", fake.loginHits)
	}
}

func TestClientAnonymousSkipsLogin(t *testing.T) {
	fake := &fakeQBT{requireAuth: false, categories: `{}`}
	client, srv := newTestClient(t, fake)
	anon, err := NewClient(Credentials{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	anon.SetHTTPClient(&http.Client{Jar: mustJar(t)})
	if _, err := anon.Categories(context.Background()); err != nil {
		t.Fatalf("anonymous Categories: %v", err)
	}
	if fake.loginHits != 0 {
		t.Fatalf("anonymous client should not log in, got %d", fake.loginHits)
	}
	_ = client
}

func TestNewClientRejectsBadURL(t *testing.T) {
	for _, url := range []string{"", "ftp://host", "://nohost", "http://"} {
		if _, err := NewClient(Credentials{BaseURL: url}); err == nil {
			t.Errorf("NewClient(%q) expected error", url)
		}
	}
}
