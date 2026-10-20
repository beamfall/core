//go:build io

package ioe2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/beamfall/core/internal/qbittorrent"
)

// qbitDouble is a tiny qBittorrent Web API v2 double: it records hits per path
// and serves minimal valid login / categories / torrents responses.
type qbitDouble struct {
	loginHits      int32
	categoriesHits int32
	torrentsHits   int32
	gotUser        string
	gotPass        string
}

func (d *qbitDouble) server(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&d.loginHits, 1)
		_ = r.ParseForm()
		d.gotUser = r.PostFormValue("username")
		d.gotPass = r.PostFormValue("password")
		// qBittorrent returns the literal "Ok." with 200 on success.
		_, _ = w.Write([]byte("Ok."))
	})
	mux.HandleFunc("/api/v2/torrents/categories", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&d.categoriesHits, 1)
		_, _ = w.Write([]byte(`{"movies":{"name":"movies","savePath":"/dl/movies"}}`))
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&d.torrentsHits, 1)
		_, _ = w.Write([]byte(`[{"hash":"abc","name":"Some.Release","category":"movies","progress":1}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestEgress_qbittorrent_login drives the REAL qbittorrent.Client login against a
// loopback double and asserts the credential POST to /api/v2/auth/login.
//
// io:egress-qbittorrent-login
func TestEgress_qbittorrent_login(t *testing.T) {
	d := &qbitDouble{}
	srv := d.server(t)

	client, err := qbittorrent.NewClient(qbittorrent.Credentials{
		BaseURL:  srv.URL,
		Username: "operator",
		Password: "hunter2",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.SetHTTPClient(srv.Client())

	if err := client.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got := atomic.LoadInt32(&d.loginHits); got != 1 {
		t.Fatalf("login double hit %d times, want 1", got)
	}
	if d.gotUser != "operator" || d.gotPass != "hunter2" {
		t.Errorf("login form = (%q,%q), want (operator,hunter2)", d.gotUser, d.gotPass)
	}
}

// TestEgress_qbittorrent_query drives the REAL qbittorrent.Client category +
// completed-torrent queries against a loopback double and asserts the GETs and
// the parsed responses.
//
// io:egress-qbittorrent-query
func TestEgress_qbittorrent_query(t *testing.T) {
	d := &qbitDouble{}
	srv := d.server(t)

	client, err := qbittorrent.NewClient(qbittorrent.Credentials{
		BaseURL:  srv.URL,
		Username: "operator",
		Password: "hunter2",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.SetHTTPClient(srv.Client())

	cats, err := client.Categories(context.Background())
	if err != nil {
		t.Fatalf("Categories: %v", err)
	}
	if len(cats) != 1 || cats[0].Name != "movies" || cats[0].SavePath != "/dl/movies" {
		t.Errorf("categories = %+v, want one movies/%q", cats, "/dl/movies")
	}
	if got := atomic.LoadInt32(&d.categoriesHits); got != 1 {
		t.Errorf("categories double hit %d times, want 1", got)
	}

	torrents, err := client.CompletedTorrents(context.Background())
	if err != nil {
		t.Fatalf("CompletedTorrents: %v", err)
	}
	if len(torrents) != 1 || torrents[0].Hash != "abc" || torrents[0].Category != "movies" {
		t.Errorf("torrents = %+v, want one abc/movies", torrents)
	}
	if got := atomic.LoadInt32(&d.torrentsHits); got != 1 {
		t.Errorf("torrents double hit %d times, want 1", got)
	}
}
