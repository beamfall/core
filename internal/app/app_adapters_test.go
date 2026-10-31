package app

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/jobs"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	scrobble "github.com/beamfall/plugin-sdk/scrobble"
)

func TestListenPortReadsTCPAndHostPortAddrs(t *testing.T) {
	for _, tt := range []struct {
		name string
		addr net.Addr
		want int
	}{
		{name: "tcp addr", addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8443}, want: 8443},
		{name: "non-tcp host:port", addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5353}, want: 5353},
		{name: "no port", addr: &net.UnixAddr{Name: "/run/beamfall.sock", Net: "unix"}, want: 0},
		{name: "non-numeric port", addr: &net.UnixAddr{Name: "localhost:http", Net: "unix"}, want: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := listenPort(tt.addr); got != tt.want {
				t.Fatalf("listenPort(%v) = %d, want %d", tt.addr, got, tt.want)
			}
		})
	}
}

type stubLocalExtractor struct {
	result scanpkg.LocalMetadata
	err    error
}

func (s stubLocalExtractor) ExtractLocal(context.Context, string, string, string) (scanpkg.LocalMetadata, error) {
	return s.result, s.err
}

// TestValidatingLocalExtractorKeepsOnlyDeclaredRelations: a local extractor
// may only contribute Entity links its bundle declares; undeclared kind or
// relation pairs are dropped and the declared cardinality overrides whatever
// the extractor claimed.
func TestValidatingLocalExtractorKeepsOnlyDeclaredRelations(t *testing.T) {
	inner := stubLocalExtractor{result: scanpkg.LocalMetadata{
		Fields:             map[string]any{"camera_make": "Acme"},
		ImpliedMediaFields: map[string]string{"year": "2024"},
		Entities: []scanpkg.LocalEntity{
			{Kind: "album", Title: "Declared", Relation: "album-track", Cardinality: "many"},
			{Kind: "album", Title: "Wrong relation", Relation: "undeclared"},
			{Kind: "studio", Title: "Wrong kind", Relation: "album-track"},
		},
	}}
	extractor := validatingLocalExtractor{inner: inner, relations: map[string]string{"album\x00album-track": "one"}}

	got, err := extractor.ExtractLocal(context.Background(), "audio", "/root", "/root/a.flac")
	if err != nil {
		t.Fatalf("ExtractLocal: %v", err)
	}
	if len(got.Entities) != 1 || got.Entities[0].Title != "Declared" {
		t.Fatalf("entities = %+v, want only the declared album link", got.Entities)
	}
	if got.Entities[0].Cardinality != "one" {
		t.Fatalf("cardinality = %q, want declared %q", got.Entities[0].Cardinality, "one")
	}
	if got.Fields["camera_make"] != "Acme" || got.ImpliedMediaFields["year"] != "2024" {
		t.Fatalf("fields not passed through: %+v / %+v", got.Fields, got.ImpliedMediaFields)
	}
}

func TestValidatingLocalExtractorPropagatesInnerError(t *testing.T) {
	boom := errors.New("extract failed")
	inner := stubLocalExtractor{
		result: scanpkg.LocalMetadata{Entities: []scanpkg.LocalEntity{{Kind: "album", Relation: "album-track"}}},
		err:    boom,
	}
	extractor := validatingLocalExtractor{inner: inner, relations: map[string]string{"album\x00album-track": "one"}}
	got, err := extractor.ExtractLocal(context.Background(), "audio", "/root", "/root/a.flac")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want inner error", err)
	}
	if len(got.Entities) != 0 || got.Fields != nil {
		t.Fatalf("failed extraction leaked metadata: %+v", got)
	}
}

type stubScrobbleDeadLetters struct{ entries []scrobble.DeadLetter }

func (s stubScrobbleDeadLetters) Record(scrobble.DeadLetter) error { return nil }
func (s stubScrobbleDeadLetters) Entries() []scrobble.DeadLetter   { return s.entries }

type stubScrobbleSkips struct{ entries []scrobble.SkipReason }

func (s stubScrobbleSkips) Skip(scrobble.SkipReason)       {}
func (s stubScrobbleSkips) Entries() []scrobble.SkipReason { return s.entries }

type stubScrobbleLinks struct{}

func (stubScrobbleLinks) LinksFor(context.Context, string) ([]scrobble.Link, error) { return nil, nil }
func (stubScrobbleLinks) LibraryEligible(context.Context, string) (bool, error)     { return false, nil }

// TestScrobbleStatusSourceTranslatesFeeds: the status adapter maps the
// dispatcher's dead-letter and skip feeds field by field, rendering a set
// timestamp as RFC3339 UTC and an unset one as the empty string.
func TestScrobbleStatusSourceTranslatesFeeds(t *testing.T) {
	at := time.Date(2026, 3, 4, 10, 30, 0, 0, time.FixedZone("UTC+2", 2*60*60))
	dispatcher, err := scrobble.New(scrobble.Config{
		DeadLetter: stubScrobbleDeadLetters{entries: []scrobble.DeadLetter{
			{ProfileID: "profile-a", Service: "boundary-test", MediaID: "media-a", Attempts: 5, LastError: "status 503", At: at},
			{ProfileID: "profile-b", Service: "boundary-test", MediaID: "media-b", Attempts: 1, LastError: "status 400"},
		}},
		Skips: stubScrobbleSkips{entries: []scrobble.SkipReason{
			{ProfileID: "profile-a", MediaID: "media-c", Service: "boundary-test", Reason: "no external id", At: at},
			{ProfileID: "profile-b", MediaID: "media-d", Reason: "library not scrobble-eligible"},
		}},
	}, []scrobble.Adapter{boundaryScrobbleAdapter{declared: []string{"https://allowed.example"}}}, stubScrobbleLinks{}, memSecretStore{}, discardLogger())
	if err != nil {
		t.Fatalf("scrobble.New: %v", err)
	}
	source := scrobbleStatusSource{dispatcher: dispatcher}

	dead := source.DeadLetters()
	wantDead := []graph.ScrobbleDeadLetterInfo{
		{ProfileID: "profile-a", Service: "boundary-test", MediaID: "media-a", Attempts: 5, LastError: "status 503", At: "2026-03-04T08:30:00Z"},
		{ProfileID: "profile-b", Service: "boundary-test", MediaID: "media-b", Attempts: 1, LastError: "status 400", At: ""},
	}
	if len(dead) != len(wantDead) || dead[0] != wantDead[0] || dead[1] != wantDead[1] {
		t.Fatalf("dead letters = %+v, want %+v", dead, wantDead)
	}

	skips := source.Skips()
	wantSkips := []graph.ScrobbleSkipInfo{
		{ProfileID: "profile-a", MediaID: "media-c", Service: "boundary-test", Reason: "no external id", At: "2026-03-04T08:30:00Z"},
		{ProfileID: "profile-b", MediaID: "media-d", Reason: "library not scrobble-eligible", At: ""},
	}
	if len(skips) != len(wantSkips) || skips[0] != wantSkips[0] || skips[1] != wantSkips[1] {
		t.Fatalf("skips = %+v, want %+v", skips, wantSkips)
	}
}

// TestQBittorrentIngestAdapterStatusMapsLedgerFeed: the Console status read
// mirrors the persisted ingest ledger newest first, with UTC RFC3339 times and
// an empty time for an entry that never recorded one.
func TestQBittorrentIngestAdapterStatusMapsLedgerFeed(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	srv := fakeQBTEndpoint(t)
	cfg := config.Config{DataDir: t.TempDir(), QBittorrentURL: srv.URL}
	ledger := `{"version":1,"processed":{},"status":[
		{"torrent_hash":"h1","release_name":"Old.Release","category":"films","outcome":"unrouted","detail":"category not mapped","at":"0001-01-01T00:00:00Z"},
		{"torrent_hash":"h2","release_name":"New.Release","category":"films","outcome":"imported","library_id":"library-local","at":"2026-05-06T09:00:00+02:00"}
	]}`
	if err := os.WriteFile(filepath.Join(cfg.DataDir, "qbittorrent-ingest.json"), []byte(ledger), 0o600); err != nil {
		t.Fatal(err)
	}
	ing, err := buildQBittorrentIngestor(ctx, cfg, st, discardLogger(), nil)
	if err != nil {
		t.Fatalf("buildQBittorrentIngestor: %v", err)
	}

	got := qbittorrentIngestAdapter{ingestor: ing}.Status(ctx)
	want := []graph.QBittorrentStatusEntry{
		{ReleaseName: "New.Release", Category: "films", Outcome: "imported", LibraryID: "library-local", At: "2026-05-06T07:00:00Z"},
		{ReleaseName: "Old.Release", Category: "films", Outcome: "unrouted", Detail: "category not mapped", At: ""},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("status = %+v, want %+v", got, want)
	}
	if feed := (qbittorrentIngestAdapter{}).Status(ctx); feed != nil {
		t.Fatalf("absent ingestor status = %+v, want nil", feed)
	}
}

// authQBTEndpoint is a fakeQBTEndpoint variant that requires a login with the
// given password and records every password it was offered.
type authQBTEndpoint struct {
	*httptest.Server
	mu      sync.Mutex
	offered []string
}

func newAuthQBTEndpoint(t *testing.T, password string) *authQBTEndpoint {
	t.Helper()
	endpoint := &authQBTEndpoint{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		offered := r.FormValue("password")
		endpoint.mu.Lock()
		endpoint.offered = append(endpoint.offered, offered)
		endpoint.mu.Unlock()
		if r.FormValue("username") != "admin" || offered != password {
			_, _ = w.Write([]byte("Fails."))
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: "session", Path: "/"})
		_, _ = w.Write([]byte("Ok."))
	})
	authed := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if _, err := r.Cookie("SID"); err != nil {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("/api/v2/torrents/categories", authed(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"films":{"name":"films","savePath":"/downloads/films"}}`))
	}))
	mux.HandleFunc("/api/v2/app/defaultSavePath", authed(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("/downloads"))
	}))
	endpoint.Server = httptest.NewServer(mux)
	t.Cleanup(endpoint.Close)
	return endpoint
}

func (e *authQBTEndpoint) passwords() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.offered...)
}

func newTestQBittorrentConfigurer(t *testing.T, url string) (qbittorrentConfigurer, *store.Store) {
	t.Helper()
	st := storetest.Open(t)
	cfg := config.Config{DataDir: t.TempDir(), QBittorrentURL: url}
	ing, err := buildQBittorrentIngestor(context.Background(), cfg, st, slog.Default(), nil)
	if err != nil {
		t.Fatalf("buildQBittorrentIngestor: %v", err)
	}
	return qbittorrentConfigurer{ingestor: ing, store: st, secrets: secretFiles{dir: filepath.Join(cfg.DataDir, "secrets")}}, st
}

func requireValidationError(t *testing.T, err error, contains string) {
	t.Helper()
	var verr store.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v (%T), want store.ValidationError", err, err)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("err = %q, want it to mention %q", err, contains)
	}
}

func TestQBittorrentConfigurerRejectsWithoutRunningIngestor(t *testing.T) {
	err := qbittorrentConfigurer{}.Configure(context.Background(), graph.QBittorrentIngestInput{URL: "http://127.0.0.1:1"})
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("err = %v, want not-running refusal", err)
	}
	var verr store.ValidationError
	if errors.As(err, &verr) {
		t.Fatalf("missing ingestor is a server condition, not operator input: %v", err)
	}
}

func TestQBittorrentConfigurerRejectsInvalidOrUnreachableEndpoint(t *testing.T) {
	srv := fakeQBTEndpoint(t)
	configurer, st := newTestQBittorrentConfigurer(t, srv.URL)
	ctx := context.Background()

	err := configurer.Configure(ctx, graph.QBittorrentIngestInput{URL: "ftp://qbt.example"})
	requireValidationError(t, err, "scheme")

	err = configurer.Configure(ctx, graph.QBittorrentIngestInput{URL: "http://127.0.0.1:1"})
	requireValidationError(t, err, "unreachable")

	persisted, err := st.QBittorrentConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Configured {
		t.Fatalf("rejected configure persisted %+v", persisted)
	}
}

// TestQBittorrentConfigurerPersistsAcceptedConfigAndReusesStoredPassword: an
// accepted configure stores the password in the secret file and the routing in
// the config store; a later routing-only edit (SetPassword false) logs in with
// the stored password rather than an empty one.
func TestQBittorrentConfigurerPersistsAcceptedConfigAndReusesStoredPassword(t *testing.T) {
	endpoint := newAuthQBTEndpoint(t, "s3cret")
	configurer, st := newTestQBittorrentConfigurer(t, endpoint.URL)
	ctx := context.Background()
	routes := []graph.QBittorrentRoute{{Category: "films", LibraryID: "library-local"}}

	if err := configurer.Configure(ctx, graph.QBittorrentIngestInput{
		URL: endpoint.URL, Username: "admin", Password: "s3cret", SetPassword: true, Routes: routes,
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if stored, err := configurer.secrets.Secret("qbittorrent"); err != nil || stored != "s3cret" {
		t.Fatalf("stored password = %q, %v", stored, err)
	}
	persisted, err := st.QBittorrentConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.Configured || persisted.URL != endpoint.URL || persisted.Username != "admin" ||
		len(persisted.Routes) != 1 || persisted.Routes[0].Category != "films" || persisted.Routes[0].LibraryID != "library-local" {
		t.Fatalf("persisted config = %+v", persisted)
	}
	if got := configurer.ingestor.Routes(); got["films"] != "library-local" {
		t.Fatalf("live routes = %v, want films -> library-local", got)
	}

	before := len(endpoint.passwords())
	if err := configurer.Configure(ctx, graph.QBittorrentIngestInput{
		URL: endpoint.URL, Username: "admin", Routes: routes,
	}); err != nil {
		t.Fatalf("routing-only Configure: %v", err)
	}
	offered := endpoint.passwords()[before:]
	if len(offered) == 0 || offered[0] != "s3cret" {
		t.Fatalf("routing-only edit offered passwords %q, want the stored one", offered)
	}
}

func TestQBittorrentConfigurerRejectedRoutingPersistsNothing(t *testing.T) {
	endpoint := newAuthQBTEndpoint(t, "s3cret")
	configurer, st := newTestQBittorrentConfigurer(t, endpoint.URL)
	ctx := context.Background()

	err := configurer.Configure(ctx, graph.QBittorrentIngestInput{
		URL: endpoint.URL, Username: "admin", Password: "s3cret", SetPassword: true,
		Routes: []graph.QBittorrentRoute{{Category: "missing", LibraryID: "library-local"}},
	})
	requireValidationError(t, err, "does not exist")
	if _, err := configurer.secrets.Secret("qbittorrent"); err == nil {
		t.Fatal("rejected routing stored the password")
	}
	if persisted, err := st.QBittorrentConfig(ctx); err != nil || persisted.Configured {
		t.Fatalf("rejected routing persisted %+v, %v", persisted, err)
	}
}

// TestRunPendingJobsDrainsRequestedClassesAndSurfacesFailures: the harness
// drain runs only the requested classes, counts what it ran, reports zero once
// the queue is empty, and returns a job failure instead of hiding it.
func TestRunPendingJobsDrainsRequestedClassesAndSurfacesFailures(t *testing.T) {
	root := t.TempDir()
	clip := filepath.Join(root, "clip.mp4")
	if err := os.WriteFile(clip, []byte("clip bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	settled := time.Now().Add(-time.Hour)
	if err := os.Chtimes(clip, settled, settled); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer beamfall.Close()
	// Boot queues its own maintenance sweeps; drain them, then an empty queue
	// reports zero.
	if _, err := beamfall.RunPendingJobs(ctx); err != nil {
		t.Fatalf("boot drain: %v", err)
	}
	if n, err := beamfall.RunPendingJobs(ctx); err != nil || n != 0 {
		t.Fatalf("idle drain = %d, %v; want 0, nil", n, err)
	}

	job, err := scanpkg.NewJob(scanpkg.JobRequest{ID: "job-scan-drain", LibraryID: "library-local", Root: root, CreatedAt: time.Now().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := beamfall.store.EnqueueJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	otherClass := store.JobClassInteractive
	if job.Class == store.JobClassInteractive {
		otherClass = store.JobClassBackground
	}
	if n, err := beamfall.RunPendingJobs(ctx, otherClass); err != nil || n != 0 {
		t.Fatalf("drain of class %q = %d, %v; want the %q scan left queued", otherClass, n, err, job.Class)
	}
	n, err := beamfall.RunPendingJobs(ctx)
	if err != nil || n < 1 {
		t.Fatalf("default drain = %d, %v; want at least the scan", n, err)
	}
	items, err := beamfall.store.SourceItems(ctx, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}, store.SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSourceTitle(items, "clip") {
		t.Fatalf("drained scan did not import the clip: %#v", items)
	}

	orphan := job
	orphan.ID = "job-no-handler"
	orphan.Kind = "no-such-kind"
	orphan.DedupeKey = ""
	if _, err := beamfall.store.EnqueueJob(ctx, orphan); err != nil {
		t.Fatal(err)
	}
	n, err = beamfall.RunPendingJobs(ctx)
	if !errors.Is(err, jobs.ErrNoHandler) || n != 0 {
		t.Fatalf("drain with unhandled job = %d, %v; want 0, ErrNoHandler", n, err)
	}
}
