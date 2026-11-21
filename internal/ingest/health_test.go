package ingest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

const healthyFeed = `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>Feed</title></channel></rss>`

// testClock is a settable poller clock.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

func healthPoller(st *store.Store, engine *scrape.Engine, clock *testClock) *Poller {
	poller := NewPoller(st, engine)
	poller.now = clock.Now
	poller.jitter = func() float64 { return 0.5 }
	return poller
}

func addHealthSubscription(t *testing.T, st *store.Store, feedURL string) store.Subscription {
	t.Helper()
	sub, err := st.AddSubscription(context.Background(), store.Subscription{
		LibraryID: "library-local",
		FeedURL:   feedURL,
		Root:      t.TempDir(),
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

func reloadSubscription(t *testing.T, st *store.Store, id store.SubscriptionID) store.Subscription {
	t.Helper()
	sub, err := st.Subscription(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

// TestFeedFailureClasses injects one fault per error class and checks the
// recorded class, the failure run and the backed-off next poll.
func TestFeedFailureClasses(t *testing.T) {
	statusServer := func(code int, body string) string {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(server.Close)
		return server.URL + "/feed.xml"
	}
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(healthyFeed))
	}))
	t.Cleanup(tlsServer.Close)
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL + "/feed.xml"
	closed.Close()
	loopback := scrape.New(scrape.WithInsecureLoopback())

	cases := []struct {
		name   string
		url    string
		engine *scrape.Engine
		authed bool
		want   store.FeedErrorClass
	}{
		{"http-5xx", statusServer(http.StatusServiceUnavailable, ""), loopback, false, store.FeedErrorHTTP5xx},
		{"http-4xx", statusServer(http.StatusNotFound, ""), loopback, false, store.FeedErrorHTTP4xx},
		{"auth-status", statusServer(http.StatusUnauthorized, ""), loopback, false, store.FeedErrorAuth},
		{"auth-credential", statusServer(http.StatusOK, healthyFeed), loopback, true, store.FeedErrorAuth},
		{"parse", statusServer(http.StatusOK, "<rss><channel><item></rss>"), loopback, false, store.FeedErrorParse},
		{"ssrf-blocked", statusServer(http.StatusOK, healthyFeed), scrape.New(), false, store.FeedErrorSSRFBlocked},
		{"tls", tlsServer.URL + "/feed.xml", loopback, false, store.FeedErrorTLS},
		{"dns", "https://feed.invalid/feed.xml", loopback, false, store.FeedErrorDNS},
		{"network", closedURL, loopback, false, store.FeedErrorNetwork},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := storetest.Open(t)
			ctx := context.Background()
			sub := addHealthSubscription(t, st, tc.url)
			if tc.authed {
				// A stored ref the secure store cannot resolve.
				if err := st.SetSubscriptionAuthRef(ctx, sub.ID, "missing-ref"); err != nil {
					t.Fatal(err)
				}
				sub = reloadSubscription(t, st, sub.ID)
			}
			start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
			clock := &testClock{now: start}
			poller := healthPoller(st, tc.engine, clock)
			if _, err := poller.pollSubscription(ctx, sub); err == nil {
				t.Fatal("poll succeeded, want a failure")
			}
			health := reloadSubscription(t, st, sub.ID).Health
			if health.LastErrorClass != tc.want {
				t.Fatalf("class = %q, want %q", health.LastErrorClass, tc.want)
			}
			if !health.FailingSince.Equal(start) || health.ConsecutiveFailures != 1 {
				t.Fatalf("health = %+v, want one failure since %v", health, start)
			}
			if want := start.Add(failureBackoffBase); !health.NextPollAt.Equal(want) {
				t.Fatalf("next poll = %v, want %v", health.NextPollAt, want)
			}
			if health.Status(start) != store.FeedHealthFailing {
				t.Fatalf("status = %q, want failing", health.Status(start))
			}
		})
	}
}

// TestFailingFeedStillPollsDailyAfterThirtyDays drives a feed that fails for
// 30 days through the real poll job: it keeps polling, never more than a day
// apart, turns to warning after seven days, and recovers on one success.
func TestFailingFeedStillPollsDailyAfterThirtyDays(t *testing.T) {
	st := storetest.Open(t)
	var mu sync.Mutex
	failing := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if failing {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(healthyFeed))
	}))
	t.Cleanup(server.Close)
	sub := addHealthSubscription(t, st, server.URL+"/feed.xml")
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	clock := &testClock{now: start}
	handler := Handler(healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), clock))

	polls := 0
	for clock.Now().Before(start.Add(30 * 24 * time.Hour)) {
		drivePoll(t, st, handler)
		polls++
		health := reloadSubscription(t, st, sub.ID).Health
		if health.ConsecutiveFailures != polls {
			t.Fatalf("after poll %d failures = %d", polls, health.ConsecutiveFailures)
		}
		gap := health.NextPollAt.Sub(clock.Now())
		if gap <= 0 || gap > MaxPollInterval {
			t.Fatalf("poll %d next gap = %v, want (0, 24h]", polls, gap)
		}
		// Not due a minute early: the job skips it.
		clock.Set(health.NextPollAt.Add(-time.Minute))
		drivePoll(t, st, handler)
		if got := reloadSubscription(t, st, sub.ID).Health.ConsecutiveFailures; got != polls {
			t.Fatalf("feed polled before it was due (failures %d, want %d)", got, polls)
		}
		clock.Set(health.NextPollAt)
	}
	health := reloadSubscription(t, st, sub.ID).Health
	if gap := health.NextPollAt.Sub(health.FailingSince); gap < 29*24*time.Hour {
		t.Fatalf("failure run spans %v, want about 30 days", gap)
	}
	if status := health.Status(clock.Now()); status != store.FeedHealthWarning {
		t.Fatalf("status after 30 days = %q, want warning", status)
	}
	if !health.Due(clock.Now()) {
		t.Fatal("feed failing for 30 days is not due: it must never be disabled")
	}
	if polls < 30 {
		t.Fatalf("polled %d times in 30 days, want at least daily", polls)
	}

	mu.Lock()
	failing = false
	mu.Unlock()
	drivePoll(t, st, handler)
	health = reloadSubscription(t, st, sub.ID).Health
	if health.Status(clock.Now()) != store.FeedHealthOK || health.LastErrorClass != "" || health.ConsecutiveFailures != 0 {
		t.Fatalf("health after recovery = %+v", health)
	}
	if want := clock.Now().Add(DefaultPollInterval); !health.NextPollAt.Equal(want) {
		t.Fatalf("next poll after recovery = %v, want %v", health.NextPollAt, want)
	}
}

func TestGoneRetiresFeedUntilRefresh(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	t.Cleanup(server.Close)
	sub := addHealthSubscription(t, st, server.URL+"/feed.xml")
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	clock := &testClock{now: start}
	poller := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), clock)
	if _, err := poller.pollSubscription(ctx, sub); err == nil {
		t.Fatal("410 poll succeeded")
	}
	health := reloadSubscription(t, st, sub.ID).Health
	if health.Status(start) != store.FeedHealthRetired || health.Due(start.Add(30*24*time.Hour)) {
		t.Fatalf("health after 410 = %+v, want retired and never due", health)
	}
	if _, err := st.RequestSubscriptionRefresh(ctx, sub.ID, start.Add(time.Minute), 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if !reloadSubscription(t, st, sub.ID).Health.Due(start.Add(time.Minute)) {
		t.Fatal("refreshed retired feed is not due")
	}
}

// TestMovedFeedURLRewritesAfterTwoObservations checks that a permanent
// redirect or an in-feed new-URL declaration moves the Subscription only on
// the second consistent poll, and a temporary redirect never does.
func TestMovedFeedURLRewritesAfterTwoObservations(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	mux.HandleFunc("/permanent.xml", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/moved.xml", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/temporary.xml", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/moved.xml", http.StatusFound)
	})
	mux.HandleFunc("/declared.xml", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0" xmlns:itunes="urn:x"><channel><title>Feed</title><itunes:new-feed-url>%s/declared-new.xml</itunes:new-feed-url></channel></rss>`, server.URL)
	})
	serveFeed := func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(healthyFeed)) }
	mux.HandleFunc("/moved.xml", serveFeed)
	mux.HandleFunc("/declared-new.xml", serveFeed)

	cases := []struct {
		path, want string
	}{
		{"/permanent.xml", "/moved.xml"},
		{"/declared.xml", "/declared-new.xml"},
		{"/temporary.xml", "/temporary.xml"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			st := storetest.Open(t)
			ctx := context.Background()
			sub := addHealthSubscription(t, st, server.URL+tc.path)
			clock := &testClock{now: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
			poller := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), clock)
			if _, err := poller.pollSubscription(ctx, sub); err != nil {
				t.Fatal(err)
			}
			first := reloadSubscription(t, st, sub.ID)
			if first.FeedURL != server.URL+tc.path {
				t.Fatalf("feed URL rewritten after one observation: %q", first.FeedURL)
			}
			if _, err := poller.pollSubscription(ctx, first); err != nil {
				t.Fatal(err)
			}
			second := reloadSubscription(t, st, sub.ID)
			if second.FeedURL != server.URL+tc.want {
				t.Fatalf("feed URL after two polls = %q, want %q", second.FeedURL, server.URL+tc.want)
			}
			if moved := !second.Health.MovedAt.IsZero(); moved != (tc.want != tc.path) {
				t.Fatalf("movedAt = %v for %s", second.Health.MovedAt, tc.path)
			}
		})
	}
}

func TestFailureBackoffIsJitteredAndCapped(t *testing.T) {
	for failures := 1; failures <= 200; failures++ {
		for _, jitter := range []float64{0, 0.5, 0.999} {
			delay := failureBackoff(failures, jitter)
			if delay <= 0 || delay > MaxPollInterval {
				t.Fatalf("backoff(%d, %v) = %v, want (0, 24h]", failures, jitter, delay)
			}
		}
	}
	if low, high := failureBackoff(3, 0), failureBackoff(3, 0.999); low != 48*time.Minute || high < 71*time.Minute || high > 72*time.Minute {
		t.Fatalf("backoff(3) spans [%v, %v], want ±20%% around 1h", low, high)
	}
	if got := clampPollInterval(time.Minute); got != MinPollInterval {
		t.Fatalf("clamp(1m) = %v", got)
	}
	if got := clampPollInterval(72 * time.Hour); got != MaxPollInterval {
		t.Fatalf("clamp(72h) = %v", got)
	}
}
