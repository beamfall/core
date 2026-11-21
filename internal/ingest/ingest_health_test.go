package ingest

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func policyEntries() []scrape.FeedEntry {
	day := func(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC) }
	return []scrape.FeedEntry{
		{GUID: "a", PublishedAt: day(2)},
		{GUID: "b", PublishedAt: day(5)},
		{GUID: "c"},
		{GUID: "d", PublishedAt: day(4)},
		{GUID: "e", PublishedAt: day(1)},
	}
}

func TestSelectByPolicy(t *testing.T) {
	added := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	stored := map[string]store.SubscriptionEntry{
		"a": {FirstSeenAt: added},
		"b": {FirstSeenAt: added.Add(time.Hour)},
		"c": {FirstSeenAt: added},
		"d": {FirstSeenAt: added.Add(2 * time.Hour)},
		"e": {FirstSeenAt: added.Add(-time.Hour)},
	}
	latest := store.SubscriptionPolicy{Policy: store.DownloadPolicyLatest, LatestN: 2}
	cases := []struct {
		name   string
		policy store.SubscriptionPolicy
		serial bool
		want   []string
	}{
		{"latest picks the newest by date", latest, false, []string{"b", "d"}},
		{"latest on a serial feed picks the first in feed order", latest, true, []string{"a", "b"}},
		{"latest puts undated entries last", store.SubscriptionPolicy{Policy: store.DownloadPolicyLatest, LatestN: 5}, false, []string{"b", "d", "a", "e", "c"}},
		{"latest defaults N", store.SubscriptionPolicy{Policy: store.DownloadPolicyLatest}, false, []string{"b", "d", "a"}},
		{"new-only picks entries first seen after AddedAt", store.SubscriptionPolicy{Policy: store.DownloadPolicyNewOnly}, false, []string{"b", "d"}},
		{"everything", store.SubscriptionPolicy{Policy: store.DownloadPolicyEverything}, false, []string{"a", "b", "c", "d", "e"}},
		{"stream-only", store.SubscriptionPolicy{Policy: store.DownloadPolicyStreamOnly}, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := selectByPolicy(tc.policy, tc.serial, added, policyEntries(), stored)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("selected %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClassifyFeedError(t *testing.T) {
	engine := scrape.New()
	refused := engine.CheckURL("http://feeds.example.com/x")
	status := func(code int) error {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
		defer server.Close()
		_, err := scrape.New(scrape.WithInsecureLoopback()).FetchConditional(context.Background(), server.URL, "", "")
		return err
	}
	cases := []struct {
		err  error
		want store.FeedErrorClass
	}{
		{nil, ""},
		{fmt.Errorf("wrapped: %w", &net.DNSError{Err: "no such host", Name: "feeds.example.com", IsNotFound: true}), store.FeedErrorDNS},
		{fmt.Errorf("wrapped: %w", x509.UnknownAuthorityError{}), store.FeedErrorTLS},
		{&tls.CertificateVerificationError{Err: errors.New("bad cert")}, store.FeedErrorTLS},
		{status(http.StatusNotFound), store.FeedErrorHTTP4xx},
		{status(http.StatusUnauthorized), store.FeedErrorAuth},
		{status(http.StatusForbidden), store.FeedErrorAuth},
		{status(http.StatusGone), store.FeedErrorGone},
		{status(http.StatusBadGateway), store.FeedErrorHTTP5xx},
		{&feedParseError{err: errors.New("bad xml")}, store.FeedErrorParse},
		{refused, store.FeedErrorSSRFBlocked},
		{errors.New("connection reset"), store.FeedErrorNetwork},
	}
	for _, tc := range cases {
		if got := ClassifyFeedError(tc.err); got != tc.want {
			t.Errorf("ClassifyFeedError(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestFailureBackoffBounds(t *testing.T) {
	for failures := 1; failures <= 12; failures++ {
		base := DefaultPollInterval << (failures - 1)
		if base > maxPollBackoff {
			base = maxPollBackoff
		}
		for _, random := range []float64{0, 0.5, 0.999999} {
			got := failureBackoff(DefaultPollInterval, failures, random)
			low := time.Duration(float64(base) * 0.8)
			high := min(time.Duration(float64(base)*1.2), maxPollBackoff)
			if got < low || got > high {
				t.Fatalf("failures=%d random=%v: %v outside [%v, %v]", failures, random, got, low, high)
			}
		}
	}
	if got := failureBackoff(DefaultPollInterval, 40, 0.999999); got != maxPollBackoff {
		t.Fatalf("capped backoff = %v, want %v", got, maxPollBackoff)
	}
}

// statusFeed serves a feed whose HTTP status and body are switchable.
type statusFeed struct {
	mu     sync.Mutex
	code   int
	body   string
	server *httptest.Server
}

func newStatusFeed(t *testing.T) *statusFeed {
	t.Helper()
	sf := &statusFeed{code: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc("/feed.xml", func(w http.ResponseWriter, _ *http.Request) {
		sf.mu.Lock()
		code, body := sf.code, sf.body
		sf.mu.Unlock()
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/permanent.xml", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/feed.xml", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/temporary.xml", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/feed.xml", http.StatusFound)
	})
	sf.server = httptest.NewServer(mux)
	t.Cleanup(sf.server.Close)
	return sf
}

func (sf *statusFeed) set(code int, body string) {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	sf.code, sf.body = code, body
}

func healthFixture(t *testing.T, feedURL string) (*store.Store, store.Subscription, *Poller) {
	t.Helper()
	st := storetest.Open(t)
	sub, err := st.AddSubscription(context.Background(), store.Subscription{
		LibraryID: "library-local",
		FeedURL:   feedURL,
		Root:      t.TempDir(),
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	poller := NewPoller(st, scrape.New(scrape.WithInsecureLoopback()))
	poller.jitter = func() float64 { return 0.5 }
	return st, sub, poller
}

func TestPollFailureBacksOffAndGoneRetires(t *testing.T) {
	sf := newStatusFeed(t)
	st, sub, poller := healthFixture(t, sf.server.URL+"/feed.xml")
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Millisecond)
	poller.now = func() time.Time { return start }

	sf.set(http.StatusBadGateway, "")
	for range 2 {
		if _, err := poller.pollSubscription(ctx, sub); err == nil {
			t.Fatal("5xx poll succeeded")
		}
	}
	_, health, err := st.SubscriptionPolicyAndHealth(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if health.LastErrorClass != store.FeedErrorHTTP5xx || health.ConsecutiveFailures != 2 || !health.FailingSince.Equal(start) {
		t.Fatalf("health = %+v", health)
	}
	if want := start.Add(2 * DefaultPollInterval); !health.NextPollAt.Equal(want) {
		t.Fatalf("next poll = %v, want %v (2 failures, mid jitter)", health.NextPollAt, want)
	}

	// Recovery clears the failure streak and schedules one interval out.
	sf.set(http.StatusOK, `<rss><channel><title>Back</title></channel></rss>`)
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatalf("recovered poll: %v", err)
	}
	_, health, _ = st.SubscriptionPolicyAndHealth(ctx, sub.ID)
	if health.LastErrorClass != "" || health.ConsecutiveFailures != 0 || !health.NextPollAt.Equal(start.Add(DefaultPollInterval)) {
		t.Fatalf("recovered health = %+v", health)
	}

	// Malformed XML is a parse failure.
	sf.set(http.StatusOK, `<rss><channel><title a=></title></channel></rss>`)
	if _, err := poller.pollSubscription(ctx, sub); err == nil {
		t.Fatal("malformed feed polled clean")
	}
	_, health, _ = st.SubscriptionPolicyAndHealth(ctx, sub.ID)
	if health.LastErrorClass != store.FeedErrorParse {
		t.Fatalf("parse class = %q", health.LastErrorClass)
	}

	sf.set(http.StatusGone, "")
	if _, err := poller.pollSubscription(ctx, sub); err == nil {
		t.Fatal("410 poll succeeded")
	}
	_, health, _ = st.SubscriptionPolicyAndHealth(ctx, sub.ID)
	if health.LastErrorClass != store.FeedErrorGone || health.RetiredAt.IsZero() {
		t.Fatalf("gone health = %+v", health)
	}
	due, err := st.DueSubscriptions(ctx, start.Add(48*time.Hour))
	if err != nil || len(due) != 0 {
		t.Fatalf("retired feed still due: %d, %v", len(due), err)
	}
	if err := poller.PollSubscriptionNow(ctx, sub.ID); err != nil {
		t.Fatal(err)
	}
	poller.background.Wait()
}

func TestPollCataloguesEveryEntryAndDownloadsOnlyNewOnes(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	fs := newFeedServer(t)
	fs.enclosures["one.mp3"] = []byte("one")
	fs.enclosures["two.mp3"] = []byte("two")
	fs.enclosures["three.mp3"] = []byte("three")
	fs.setFeed(fs.item("guid-1", "One", "one.mp3") + fs.item("guid-2", "Two", "two.mp3"))
	root := t.TempDir()
	sub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local", FeedURL: fs.server.URL + "/feed.xml", Root: root,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	poller := NewPoller(st, scrape.New(scrape.WithInsecureLoopback()))

	// The default policy (new-only) catalogues the back catalogue file-less.
	if count, err := poller.pollSubscription(ctx, sub); err != nil || count != 0 {
		t.Fatalf("first poll = %d, %v; want catalogue only", count, err)
	}
	entries, err := st.SubscriptionEntries(ctx, sub.ID)
	if err != nil || len(entries) != 2 {
		t.Fatalf("catalogue = %d entries, %v", len(entries), err)
	}
	for _, entry := range entries {
		if entry.DownloadState != store.EntryDownloadNone || entry.Downloaded {
			t.Fatalf("back catalogue entry %s = %s", entry.GUID, entry.DownloadState)
		}
	}
	if got, _ := st.Subscription(ctx, sub.ID); got.Title != "Feed" {
		t.Fatalf("subscription title = %q, want the channel title", got.Title)
	}

	// A later arrival downloads and is queued against its catalogued Media.
	fs.setFeed(fs.item("guid-3", "Three", "three.mp3") + fs.item("guid-1", "One", "one.mp3") + fs.item("guid-2", "Two", "two.mp3"))
	advancePollClock(poller)
	if count, err := poller.pollSubscription(ctx, sub); err != nil || count != 1 {
		t.Fatalf("second poll = %d, %v; want the new entry", count, err)
	}
	if fs.hits("one.mp3") != 0 || fs.hits("three.mp3") != 1 {
		t.Fatalf("downloads: one=%d three=%d", fs.hits("one.mp3"), fs.hits("three.mp3"))
	}
	entries, _ = st.SubscriptionEntries(ctx, sub.ID)
	var fresh store.SubscriptionEntry
	for _, entry := range entries {
		if entry.GUID == "guid-3" {
			fresh = entry
		}
	}
	if fresh.DownloadState != store.EntryDownloadQueued || fresh.DownloadPath == "" || fresh.FeedPosition != 0 {
		t.Fatalf("new entry = state %s path set %v position %d", fresh.DownloadState, fresh.DownloadPath != "", fresh.FeedPosition)
	}

	// stream-only downloads nothing, even for a new arrival.
	if err := st.SetSubscriptionDownloadPolicy(ctx, sub.ID, store.SubscriptionPolicy{Policy: store.DownloadPolicyStreamOnly}); err != nil {
		t.Fatal(err)
	}
	fs.enclosures["four.mp3"] = []byte("four")
	fs.setFeed(fs.item("guid-4", "Four", "four.mp3") + fs.item("guid-3", "Three", "three.mp3"))
	advancePollClock(poller)
	if count, err := poller.pollSubscription(ctx, sub); err != nil || count != 0 || fs.hits("four.mp3") != 0 {
		t.Fatalf("stream-only poll = %d, %v", count, err)
	}
}

func TestPollTracksFeedMoves(t *testing.T) {
	sf := newStatusFeed(t)
	newURL := sf.server.URL + "/moved.xml"
	otherURL := sf.server.URL + "/other.xml"
	feedWith := func(target string) string {
		return `<rss xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd"><channel><title>T</title><itunes:new-feed-url>` + target + `</itunes:new-feed-url></channel></rss>`
	}
	ctx := context.Background()

	t.Run("one observation does not rewrite, two do", func(t *testing.T) {
		st, sub, poller := healthFixture(t, sf.server.URL+"/feed.xml")
		sf.set(http.StatusOK, feedWith(newURL))
		if _, err := poller.pollSubscription(ctx, sub); err != nil {
			t.Fatal(err)
		}
		if got, _ := st.Subscription(ctx, sub.ID); got.FeedURL != sub.FeedURL || got.ETag != "" {
			t.Fatal("one observation rewrote the feed URL or stored validators")
		}
		if _, err := poller.pollSubscription(ctx, sub); err != nil {
			t.Fatal(err)
		}
		got, _ := st.Subscription(ctx, sub.ID)
		if got.FeedURL != newURL {
			t.Fatal("two consistent observations did not move the feed")
		}
		_, health, _ := st.SubscriptionPolicyAndHealth(ctx, sub.ID)
		if health.NextPollAt.After(poller.clock()) {
			t.Fatalf("moved feed next poll = %v, want due now", health.NextPollAt)
		}
	})

	t.Run("alternating observations reset", func(t *testing.T) {
		st, sub, poller := healthFixture(t, sf.server.URL+"/feed.xml")
		for _, target := range []string{newURL, otherURL, newURL, "", newURL} {
			body := feedWith(target)
			if target == "" {
				body = `<rss><channel><title>T</title></channel></rss>`
			}
			sf.set(http.StatusOK, body)
			if _, err := poller.pollSubscription(ctx, sub); err != nil {
				t.Fatal(err)
			}
			if got, _ := st.Subscription(ctx, sub.ID); got.FeedURL != sub.FeedURL {
				t.Fatalf("feed moved after observing %q", target)
			}
		}
	})

	t.Run("refused target is no observation", func(t *testing.T) {
		st, sub, poller := healthFixture(t, sf.server.URL+"/feed.xml")
		sf.set(http.StatusOK, feedWith("http://10.0.0.1/feed.xml"))
		for range 3 {
			if _, err := poller.pollSubscription(ctx, sub); err != nil {
				t.Fatal(err)
			}
		}
		if got, _ := st.Subscription(ctx, sub.ID); got.FeedURL != sub.FeedURL {
			t.Fatal("feed moved to an SSRF-refused URL")
		}
	})

	t.Run("permanent redirect moves, temporary does not", func(t *testing.T) {
		sf.set(http.StatusOK, `<rss><channel><title>T</title></channel></rss>`)
		st, sub, poller := healthFixture(t, sf.server.URL+"/temporary.xml")
		for range 3 {
			if _, err := poller.pollSubscription(ctx, sub); err != nil {
				t.Fatal(err)
			}
		}
		if got, _ := st.Subscription(ctx, sub.ID); got.FeedURL != sub.FeedURL {
			t.Fatal("302 rewrote the feed URL")
		}
		st, sub, poller = healthFixture(t, sf.server.URL+"/permanent.xml")
		if _, err := poller.pollSubscription(ctx, sub); err != nil {
			t.Fatal(err)
		}
		if got, _ := st.Subscription(ctx, sub.ID); got.FeedURL != sub.FeedURL {
			t.Fatal("one 301 rewrote the feed URL")
		}
		if _, err := poller.pollSubscription(ctx, sub); err != nil {
			t.Fatal(err)
		}
		if got, _ := st.Subscription(ctx, sub.ID); got.FeedURL != sf.server.URL+"/feed.xml" {
			t.Fatal("two 301s did not move the feed")
		}
	})
}

func TestProbeFeed(t *testing.T) {
	sf := newStatusFeed(t)
	sf.set(http.StatusOK, `<rss><channel><title>Probe</title>
<item><guid>a</guid><enclosure url="https://cdn.example.com/a.mp3" length="100"/></item>
<item><guid>b</guid><enclosure url="https://cdn.example.com/b.mp3" length="250"/></item>
<item><guid>c</guid></item>
</channel></rss>`)
	_, _, poller := healthFixture(t, sf.server.URL+"/feed.xml")
	title, count, size, err := poller.ProbeFeed(context.Background(), sf.server.URL+"/feed.xml")
	if err != nil || title != "Probe" || count != 2 || size != 350 {
		t.Fatalf("ProbeFeed = %q %d %d %v", title, count, size, err)
	}
	sf.set(http.StatusForbidden, "")
	if _, _, _, err := poller.ProbeFeed(context.Background(), sf.server.URL+"/feed.xml"); ClassifyFeedError(err) != store.FeedErrorAuth {
		t.Fatalf("probe 403 class = %q", ClassifyFeedError(err))
	}
}

func TestPollSubscriptionNowRunsInBackground(t *testing.T) {
	sf := newStatusFeed(t)
	sf.set(http.StatusOK, `<rss><channel><title>Now</title></channel></rss>`)
	st, sub, poller := healthFixture(t, sf.server.URL+"/feed.xml")
	if err := poller.PollSubscriptionNow(context.Background(), sub.ID); err != nil {
		t.Fatal(err)
	}
	poller.background.Wait()
	_, health, err := st.SubscriptionPolicyAndHealth(context.Background(), sub.ID)
	if err != nil || health.LastSuccessAt.IsZero() {
		t.Fatalf("background poll did not run: %+v %v", health, err)
	}
	if err := poller.PollSubscriptionNow(context.Background(), "missing"); err == nil {
		t.Fatal("unknown subscription accepted")
	}
}
