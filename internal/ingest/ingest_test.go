package ingest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// feedServer serves a mutable RSS document plus enclosure payloads; the
// handler counts feed and enclosure fetches so conditional-GET behavior and
// download idempotence are observable. Counter access is mutex-guarded so
// concurrent-poll tests stay race-clean.
type feedServer struct {
	server     *httptest.Server
	mu         sync.Mutex
	feed       string
	feedHits   int
	feedAuth   string
	fileAuth   string
	enclosures map[string][]byte
	fileHits   map[string]int
}

func newFeedServer(t *testing.T) *feedServer {
	t.Helper()
	fs := &feedServer{enclosures: map[string][]byte{}, fileHits: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/feed.xml", func(w http.ResponseWriter, r *http.Request) {
		fs.mu.Lock()
		fs.feedHits++
		fs.feedAuth = r.Header.Get("Authorization")
		feed := fs.feed
		fs.mu.Unlock()
		w.Header().Set("ETag", fmt.Sprintf("W/\"v%d\"", len(feed)))
		if r.Header.Get("If-None-Match") == fmt.Sprintf("W/\"v%d\"", len(feed)) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write([]byte(feed))
	})
	mux.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/files/")
		fs.mu.Lock()
		fs.fileAuth = r.Header.Get("Authorization")
		fs.fileHits[name]++
		payload, ok := fs.enclosures[name]
		fs.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(payload)
	})
	fs.server = httptest.NewServer(mux)
	t.Cleanup(fs.server.Close)
	return fs
}

// hits reports how many times one enclosure was fetched.
func (fs *feedServer) hits(name string) int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.fileHits[name]
}

// fakeCredentials resolves AuthRefs to secrets the way the app's 0600
// secretFiles store does, so the poll path exercises the real
// ref → secure-store → header resolution (invariant #5) without touching disk.
type fakeCredentials map[string]string

func (c fakeCredentials) Secret(ref string) (string, error) {
	secret, ok := c[ref]
	if !ok {
		return "", fmt.Errorf("no secret for ref %q", ref)
	}
	return secret, nil
}

func TestPollSendsSubscriptionAuthorizationHeader(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	fs := newFeedServer(t)
	fs.enclosures["one.mp3"] = []byte("first-payload")
	fs.setFeed(fs.item("guid-1", "First Entry", "one.mp3"))

	// The credential lives in the secure store keyed by a ref; only the ref is
	// persisted on the Subscription (never the literal header).
	creds := fakeCredentials{"feed-auth-ref": "Bearer secret"}
	added, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   fs.server.URL + "/feed.xml",
		Root:      t.TempDir(),
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSubscriptionAuthRef(ctx, added.ID, "feed-auth-ref"); err != nil {
		t.Fatal(err)
	}
	poller := NewPoller(st, scrape.New(scrape.WithInsecureLoopback(), scrape.WithCredentials(creds)))
	runPoll(t, st, Handler(poller))
	if fs.feedAuth != "Bearer secret" || fs.fileAuth != "Bearer secret" {
		t.Fatalf("auth headers = feed %q file %q, want both", fs.feedAuth, fs.fileAuth)
	}
}

func (fs *feedServer) setFeed(items string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.feed = `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>Feed</title>` + items + `</channel></rss>`
}

func (fs *feedServer) item(guid, title, file string) string {
	return fmt.Sprintf(
		`<item><title>%s</title><guid>%s</guid><enclosure url="%s/files/%s" type="audio/mpeg"/></item>`,
		title, guid, fs.server.URL, file)
}

// runPoll drives the claimed poll job through the yield loop until it
// completes, the way the registry would.
func runPoll(t *testing.T, st *store.Store, handler jobs.Handler) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	job, err := NewPollJob(now)
	if err != nil {
		t.Fatal(err)
	}
	created, err := st.EnqueueJob(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	// Earlier polls may have left scan jobs queued; drain until the poll job
	// comes up (this harness has no scan handler).
	var claimed store.Job
	for {
		claimed, err = st.ClaimNextJob(ctx, "test-worker", nil, now)
		if err != nil {
			t.Fatal(err)
		}
		if claimed.ID == created.ID {
			break
		}
		if err := st.CompleteJob(ctx, claimed.ID, claimed.LeaseOwner, now); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; ; i++ {
		if i > 100 {
			t.Fatal("poll job did not complete after 100 yields")
		}
		result, err := handler(ctx, claimed)
		if errors.Is(err, jobs.ErrYielded) {
			claimed.Checkpoint = result.Checkpoint
			continue
		}
		if err != nil {
			t.Fatalf("poll handler returned error: %v", err)
		}
		if err := st.CompleteJob(ctx, claimed.ID, claimed.LeaseOwner, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		return
	}
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}

// readTestFile reads a file under a test-owned root; the single gosec
// suppression lives here instead of at every call site.
func readTestFile(path string) ([]byte, error) {
	return os.ReadFile(path) // #nosec G304 -- test-owned temp path
}

func TestPollDownloadsNewEntriesAndEnqueuesScan(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	fs := newFeedServer(t)
	fs.enclosures["one.mp3"] = []byte("first-payload")
	fs.setFeed(fs.item("guid-1", "First Entry", "one.mp3"))

	root := t.TempDir()
	sub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   fs.server.URL + "/feed.xml",
		Root:      root,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	poller := NewPoller(st, scrape.New(scrape.WithInsecureLoopback()))
	runPoll(t, st, Handler(poller))

	// The enclosure landed under the Subscription root, named from the title.
	target := filepath.Join(root, "First Entry.mp3")
	data, err := readTestFile(target)
	if err != nil {
		t.Fatalf("downloaded file: %v (dir: %v)", err, dirEntries(t, root))
	}
	if string(data) != "first-payload" {
		t.Fatalf("downloaded content = %q", data)
	}
	// No temp file left behind.
	for _, name := range dirEntries(t, root) {
		if strings.HasSuffix(name, ".partial") {
			t.Fatalf("leftover temp file %q", name)
		}
	}

	// The GUID is marked seen and the poll bookkeeping advanced.
	seen, err := st.SubscriptionSeenGUIDs(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !seen["guid-1"] {
		t.Fatalf("seen = %v, want guid-1", seen)
	}
	polled, err := st.Subscription(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if polled.LastPolledAt.IsZero() || polled.ETag == "" {
		t.Fatalf("poll bookkeeping missing: %+v", polled)
	}

	// The standard Library scan was enqueued over the Subscription root.
	jobsList, err := st.ListJobs(ctx, store.JobStatusQueued)
	if err != nil {
		t.Fatal(err)
	}
	scanJobs := 0
	for _, job := range jobsList {
		if job.Kind == scan.JobKind {
			scanJobs++
			if !strings.Contains(string(job.Payload), `"library-local"`) || !strings.Contains(string(job.Payload), root) {
				t.Fatalf("scan payload = %s", job.Payload)
			}
		}
	}
	if scanJobs != 1 {
		t.Fatalf("scan jobs queued = %d, want 1", scanJobs)
	}
}

func TestPollRecoversFromStalePartialDownload(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	fs := newFeedServer(t)
	fs.enclosures["one.mp3"] = []byte("fresh-payload")
	fs.setFeed(fs.item("guid-1", "First Entry", "one.mp3"))

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "First Entry.mp3.partial"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   fs.server.URL + "/feed.xml",
		Root:      root,
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	runPoll(t, st, Handler(NewPoller(st, scrape.New(scrape.WithInsecureLoopback()))))

	data, err := readTestFile(filepath.Join(root, "First Entry-2.mp3"))
	if err != nil {
		t.Fatalf("download beside stale partial: %v (dir: %v)", err, dirEntries(t, root))
	}
	if string(data) != "fresh-payload" {
		t.Fatalf("downloaded content = %q, want fresh payload", data)
	}
}

func TestPollDedupesByGUIDAcrossPolls(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	fs := newFeedServer(t)
	fs.enclosures["one.mp3"] = []byte("first-payload")
	fs.setFeed(fs.item("guid-1", "First Entry", "one.mp3"))

	root := t.TempDir()
	if _, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   fs.server.URL + "/feed.xml",
		Root:      root,
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	poller := NewPoller(st, scrape.New(scrape.WithInsecureLoopback()))
	handler := Handler(poller)
	runPoll(t, st, handler)
	if got := len(dirEntries(t, root)); got != 1 {
		t.Fatalf("files after first poll = %d, want 1", got)
	}

	// Same GUID, rewritten enclosure URL (ad-insertion style): no re-download.
	fs.enclosures["one-rewritten.mp3"] = []byte("restuffed-payload")
	fs.setFeed(fs.item("guid-1", "First Entry", "one-rewritten.mp3") + `<extra/>`)
	runPoll(t, st, handler)
	if entries := dirEntries(t, root); len(entries) != 1 {
		t.Fatalf("files after second poll = %v, want the original only", entries)
	}

	// A genuinely new GUID downloads — alongside, not over, the old file.
	fs.enclosures["two.mp3"] = []byte("second-payload")
	fs.setFeed(fs.item("guid-1", "First Entry", "one-rewritten.mp3") + fs.item("guid-2", "First Entry", "two.mp3"))
	runPoll(t, st, handler)
	entries := dirEntries(t, root)
	if len(entries) != 2 {
		t.Fatalf("files after third poll = %v, want 2", entries)
	}
	// The duplicate title got a suffix instead of clobbering.
	data, err := readTestFile(filepath.Join(root, "First Entry-2.mp3"))
	if err != nil {
		t.Fatalf("suffixed file: %v (dir: %v)", err, entries)
	}
	if string(data) != "second-payload" {
		t.Fatalf("suffixed content = %q", data)
	}
}

func TestPollHonorsConditionalGet(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	fs := newFeedServer(t)
	fs.enclosures["one.mp3"] = []byte("first-payload")
	fs.setFeed(fs.item("guid-1", "First Entry", "one.mp3"))

	root := t.TempDir()
	sub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   fs.server.URL + "/feed.xml",
		Root:      root,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	poller := NewPoller(st, scrape.New(scrape.WithInsecureLoopback()))
	handler := Handler(poller)
	runPoll(t, st, handler)
	first, err := st.Subscription(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Unchanged feed: the server answers 304 and the poll still counts.
	runPoll(t, st, handler)
	second, err := st.Subscription(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !second.LastPolledAt.After(first.LastPolledAt) && !second.LastPolledAt.Equal(first.LastPolledAt) {
		t.Fatalf("second poll did not update LastPolledAt: %v vs %v", second.LastPolledAt, first.LastPolledAt)
	}
	if second.ETag != first.ETag {
		t.Fatalf("304 poll changed the stored ETag: %q vs %q", second.ETag, first.ETag)
	}
	if fs.feedHits != 2 {
		t.Fatalf("feed hits = %d, want 2", fs.feedHits)
	}
	if got := len(dirEntries(t, root)); got != 1 {
		t.Fatalf("files after 304 poll = %d, want 1", got)
	}
}

func TestPollSurvivesADeadFeed(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	fs := newFeedServer(t)
	fs.enclosures["one.mp3"] = []byte("payload")
	fs.setFeed(fs.item("guid-1", "Entry", "one.mp3"))

	deadRoot := t.TempDir()
	liveRoot := t.TempDir()
	// The dead feed sorts first by ID order only by luck; add both and let
	// the poller visit whichever order IDs land in — the point is the live
	// one downloads either way.
	if _, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   fs.server.URL + "/missing-feed.xml",
		Root:      deadRoot,
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   fs.server.URL + "/feed.xml",
		Root:      liveRoot,
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	poller := NewPoller(st, scrape.New(scrape.WithInsecureLoopback()))
	runPoll(t, st, Handler(poller))

	if got := len(dirEntries(t, liveRoot)); got != 1 {
		t.Fatalf("live subscription files = %d, want 1", got)
	}
	if got := len(dirEntries(t, deadRoot)); got != 0 {
		t.Fatalf("dead subscription files = %d, want 0", got)
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"Plain Title":            "Plain Title",
		"../../etc/passwd":       "etc-passwd",
		"a/b\\c:d*e":             "a-b-c-d-e",
		"  trimmed  ":            "trimmed",
		"":                       "",
		"dots...and---dashes":    "dots.and-dashes",
		strings.Repeat("x", 200): strings.Repeat("x", 120),
	}
	for input, want := range cases {
		if got := sanitizeFilename(input); got != want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEnclosureExtension(t *testing.T) {
	cases := []struct {
		entry scrape.FeedEntry
		want  string
	}{
		{scrape.FeedEntry{EnclosureType: "audio/mpeg"}, "mp3"},
		{scrape.FeedEntry{EnclosureType: "audio/mp4; charset=binary"}, "m4a"},
		{scrape.FeedEntry{EnclosureURL: "https://cdn.example.com/x/file.OGG?token=secret"}, "ogg"},
		{scrape.FeedEntry{EnclosureURL: "https://cdn.example.com/file"}, "bin"},
		{scrape.FeedEntry{EnclosureURL: "https://cdn.example.com/file.with-long-ext.something"}, "bin"},
	}
	for _, tc := range cases {
		if got := enclosureExtension(tc.entry); got != tc.want {
			t.Errorf("enclosureExtension(%+v) = %q, want %q", tc.entry, got, tc.want)
		}
	}
}
