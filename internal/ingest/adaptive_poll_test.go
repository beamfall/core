package ingest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func formatRFC1123Z(t time.Time) string {
	return t.UTC().Format(time.RFC1123Z)
}

func rssWithDates(channelExtras string, pubDates ...time.Time) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd" xmlns:podcast="https://podcastindex.org/namespace/1.0" xmlns:sy="http://purl.org/rss/1.0/modules/syndication/">`)
	b.WriteString(`<channel><title>Feed</title>`)
	b.WriteString(channelExtras)
	for i, ts := range pubDates {
		fmt.Fprintf(&b, `<item><title>Item %d</title><guid>g-%d</guid>`, i, i)
		if !ts.IsZero() {
			fmt.Fprintf(&b, `<pubDate>%s</pubDate>`, formatRFC1123Z(ts))
		}
		b.WriteString(`</item>`)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

func regularDates(newest time.Time, step time.Duration, count int) []time.Time {
	out := make([]time.Time, count)
	for i := range count {
		out[i] = newest.Add(-time.Duration(i) * step)
	}
	return out
}

func TestAdaptivePollIntervalSourcesAndPrecedence(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	recentDaily := regularDates(now.Add(-time.Hour), 24*time.Hour, 10)   // cadence = 12h
	recentHourly := regularDates(now.Add(-time.Hour), time.Hour, 10)     // cadence = 30m
	recentWeekly := regularDates(now.Add(-time.Hour), 7*24*time.Hour, 6) // cadence = 84h -> 24h
	recentTwoHours := regularDates(now.Add(-time.Hour), 2*time.Hour, 10) // cadence = 1h

	cases := []struct {
		name         string
		cacheControl string
		body         string
		want         time.Duration
	}{
		{
			name: "podcast updateFrequency rrule",
			body: rssWithDates(`<podcast:updateFrequency rrule="FREQ=HOURLY;INTERVAL=4">Every 4 hours</podcast:updateFrequency>`, recentDaily...),
			want: 4 * time.Hour,
		},
		{
			name: "podcast updateFrequency text",
			body: rssWithDates(`<podcast:updateFrequency>Every 3 hours</podcast:updateFrequency>`, recentDaily...),
			want: 3 * time.Hour,
		},
		{
			name: "podcast updateFrequency complete marks daily",
			body: rssWithDates(`<podcast:updateFrequency complete="true">Weekly</podcast:updateFrequency><ttl>30</ttl>`, recentHourly...),
			want: 24 * time.Hour,
		},
		{
			name: "rss ttl",
			body: rssWithDates(`<ttl>45</ttl>`, recentDaily...),
			want: 45 * time.Minute,
		},
		{
			name: "sy updatePeriod and updateFrequency",
			body: rssWithDates(`<sy:updatePeriod>hourly</sy:updatePeriod><sy:updateFrequency>2</sy:updateFrequency>`, recentDaily...),
			want: 30 * time.Minute,
		},
		{
			name: "precedence updateFrequency over ttl and sy",
			body: rssWithDates(`<podcast:updateFrequency>Every 6 hours</podcast:updateFrequency><ttl>30</ttl><sy:updatePeriod>hourly</sy:updatePeriod>`, recentDaily...),
			want: 6 * time.Hour,
		},
		{
			name: "precedence ttl over sy",
			body: rssWithDates(`<ttl>90</ttl><sy:updatePeriod>daily</sy:updatePeriod>`, recentDaily...),
			want: 90 * time.Minute,
		},
		{
			name:         "precedence explicit hint over cache-control max-age",
			cacheControl: "public, max-age=14400",
			body:         rssWithDates(`<ttl>30</ttl>`, recentDaily...),
			want:         30 * time.Minute,
		},
		{
			name:         "cache-control max-age alone",
			cacheControl: "public, max-age=7200",
			body:         rssWithDates(""),
			want:         2 * time.Hour,
		},
		{
			name:         "cache-control max-age as lower bound over faster observed cadence",
			cacheControl: "public, max-age=7200",
			body:         rssWithDates("", recentHourly...),
			want:         2 * time.Hour,
		},
		{
			name:         "observed cadence longer than short cache-control max-age",
			cacheControl: "public, max-age=300",
			body:         rssWithDates("", recentDaily...),
			want:         12 * time.Hour,
		},
		{
			name:         "no-cache ignores max-age and falls back to observed cadence",
			cacheControl: "no-cache, max-age=7200",
			body:         rssWithDates("", recentTwoHours...),
			want:         time.Hour,
		},
		{
			name: "observed cadence uses last 10 pubDates regardless of feed order",
			body: func() string {
				dates := append(
					regularDates(now.Add(-60*24*time.Hour), 10*24*time.Hour, 5),
					regularDates(now.Add(-time.Hour), 4*time.Hour, 10)...,
				)
				return rssWithDates("", dates...)
			}(),
			want: 2 * time.Hour,
		},
		{
			name: "observed cadence deduplicates simultaneous batch drops",
			body: func() string {
				same := now.Add(-time.Hour)
				dates := []time.Time{
					same, same, same, same, same, same,
					same.Add(-24 * time.Hour),
					same.Add(-48 * time.Hour),
					same.Add(-72 * time.Hour),
				}
				return rssWithDates("", dates...)
			}(),
			want: 12 * time.Hour,
		},
		{
			name: "observed cadence atom feed",
			body: fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Atom Feed</title>
  <entry><id>a1</id><title>One</title><published>%s</published></entry>
  <entry><id>a2</id><title>Two</title><published>%s</published></entry>
  <entry><id>a3</id><title>Three</title><published>%s</published></entry>
</feed>`,
				now.Add(-2*time.Hour).Format(time.RFC3339),
				now.Add(-8*time.Hour).Format(time.RFC3339),
				now.Add(-14*time.Hour).Format(time.RFC3339),
			),
			want: 3 * time.Hour,
		},
		{
			name: "clamp minimum 15 minutes from ttl",
			body: rssWithDates(`<ttl>5</ttl>`, recentDaily...),
			want: MinPollInterval,
		},
		{
			name:         "clamp minimum 15 minutes from max-age",
			cacheControl: "max-age=60",
			body:         rssWithDates(""),
			want:         MinPollInterval,
		},
		{
			name: "clamp minimum 15 minutes from rapid observed cadence",
			body: rssWithDates("", regularDates(now.Add(-time.Minute), 10*time.Minute, 6)...),
			want: MinPollInterval,
		},
		{
			name: "clamp maximum 24 hours from ttl",
			body: rssWithDates(`<ttl>2880</ttl>`),
			want: MaxPollInterval,
		},
		{
			name: "clamp maximum 24 hours from weekly observed cadence",
			body: rssWithDates("", recentWeekly...),
			want: MaxPollInterval,
		},
		{
			name:         "dormant feed drops to daily even with ttl and max-age",
			cacheControl: "max-age=300",
			body:         rssWithDates(`<ttl>30</ttl><sy:updatePeriod>hourly</sy:updatePeriod>`, regularDates(now.Add(-181*24*time.Hour), time.Hour, 10)...),
			want:         MaxPollInterval,
		},
		{
			name: "dormant single-entry feed drops to daily",
			body: rssWithDates("", now.Add(-180*24*time.Hour)),
			want: MaxPollInterval,
		},
		{
			name: "just under dormant threshold stays active",
			body: rssWithDates(`<ttl>30</ttl>`, now.Add(-179*24*time.Hour)),
			want: 30 * time.Minute,
		},
		{
			name: "default interval when no hints or pubDates",
			body: rssWithDates(""),
			want: DefaultPollInterval,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.cacheControl != "" {
					w.Header().Set("Cache-Control", tc.cacheControl)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)

			st := storetest.Open(t)
			sub := addHealthSubscription(t, st, server.URL+"/feed.xml")
			clock := &testClock{now: now}
			poller := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), clock)

			if _, err := poller.pollSubscription(context.Background(), sub); err != nil {
				t.Fatalf("pollSubscription: %v", err)
			}
			got := reloadSubscription(t, st, sub.ID).Health.NextPollAt.Sub(now)
			if got != tc.want {
				t.Fatalf("next poll interval = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAdaptivePollIntervalPreservedAcrossNotModifiedAndDormancy(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	lastPub := start.Add(-179*24*time.Hour - 12*time.Hour)
	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>Feed</title><ttl>360</ttl><item><title>Latest</title><guid>g1</guid><pubDate>%s</pubDate><enclosure url="https://cdn.example.com/1.mp3" type="audio/mpeg"/></item><item><title>Prev</title><guid>g0</guid><pubDate>%s</pubDate><enclosure url="https://cdn.example.com/0.mp3" type="audio/mpeg"/></item></channel></rss>`,
		formatRFC1123Z(lastPub), formatRFC1123Z(lastPub.Add(-24*time.Hour)))

	const etag = `"v1"`
	var status304Count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == etag {
			status304Count.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	st := storetest.Open(t)
	ctx := context.Background()
	sub := addHealthSubscription(t, st, server.URL+"/feed.xml")
	clock := &testClock{now: start}
	poller := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), clock)

	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	if got := sub.Health.NextPollAt.Sub(start); got != 6*time.Hour {
		t.Fatalf("initial interval = %v, want 6h", got)
	}

	t2 := start.Add(6 * time.Hour)
	clock.Set(t2)
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	if got := sub.Health.NextPollAt.Sub(t2); got != 6*time.Hour {
		t.Fatalf("304 active interval = %v, want 6h", got)
	}

	t3 := start.Add(12 * time.Hour)
	clock.Set(t3)
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	if got := sub.Health.NextPollAt.Sub(t3); got != MaxPollInterval {
		t.Fatalf("304 dormant interval = %v, want %v", got, MaxPollInterval)
	}
	if status304Count.Load() != 2 {
		t.Fatalf("304 count = %d, want 2", status304Count.Load())
	}
}

func TestAdaptivePollIntervalNotModifiedUpdatesMaxAge(t *testing.T) {
	start := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	body := rssWithDates("", start.Add(-time.Hour))
	const etag = `"v1"`
	var currentCacheControl atomic.Value
	currentCacheControl.Store("public, max-age=900")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cc, _ := currentCacheControl.Load().(string); cc != "" {
			w.Header().Set("Cache-Control", cc)
		}
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	st := storetest.Open(t)
	ctx := context.Background()
	sub := addHealthSubscription(t, st, server.URL+"/feed.xml")
	clock := &testClock{now: start}
	poller := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), clock)

	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	if got := sub.Health.NextPollAt.Sub(start); got != 15*time.Minute {
		t.Fatalf("initial 200 interval = %v, want 15m", got)
	}

	currentCacheControl.Store("public, max-age=43200")
	t2 := start.Add(15 * time.Minute)
	clock.Set(t2)
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	if got := sub.Health.NextPollAt.Sub(t2); got != 12*time.Hour {
		t.Fatalf("304 raised max-age interval = %v, want 12h", got)
	}

	// Bare 304 omitting Cache-Control keeps the stored 12h max-age.
	currentCacheControl.Store("")
	t3 := t2.Add(12 * time.Hour)
	clock.Set(t3)
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	if got := sub.Health.NextPollAt.Sub(t3); got != 12*time.Hour {
		t.Fatalf("bare 304 interval = %v, want 12h", got)
	}

	// Cache-Control: no-cache clears stored max-age back to default.
	currentCacheControl.Store("no-cache")
	t4 := t3.Add(12 * time.Hour)
	clock.Set(t4)
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	if got := sub.Health.NextPollAt.Sub(t4); got != DefaultPollInterval {
		t.Fatalf("304 no-cache interval = %v, want %v", got, DefaultPollInterval)
	}

	currentCacheControl.Store("public, max-age=1800")
	t5 := t4.Add(DefaultPollInterval)
	clock.Set(t5)
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	if got := sub.Health.NextPollAt.Sub(t5); got != 30*time.Minute {
		t.Fatalf("304 lowered max-age interval = %v, want 30m", got)
	}
}

func TestAdaptivePollIntervalNotModifiedAfterTransientFailureKeepsTTL(t *testing.T) {
	start := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>Feed</title><ttl>720</ttl><item><title>Only</title><guid>g1</guid><pubDate>%s</pubDate><enclosure url="https://cdn.example.com/1.mp3" type="audio/mpeg"/></item></channel></rss>`,
		formatRFC1123Z(start.Add(-time.Hour)))
	const etag = `"v1"`
	var failNext atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failNext.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	st := storetest.Open(t)
	ctx := context.Background()
	sub := addHealthSubscription(t, st, server.URL+"/feed.xml")
	clock := &testClock{now: start}
	poller := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), clock)

	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	if got := sub.Health.NextPollAt.Sub(start); got != 12*time.Hour {
		t.Fatalf("initial interval = %v, want 12h", got)
	}

	failNext.Store(true)
	t2 := start.Add(12 * time.Hour)
	clock.Set(t2)
	if _, err := poller.pollSubscription(ctx, sub); err == nil {
		t.Fatal("expected 503 poll failure")
	}
	sub = reloadSubscription(t, st, sub.ID)

	failNext.Store(false)
	t3 := sub.Health.NextPollAt
	clock.Set(t3)
	restarted := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), clock)
	if _, err := restarted.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	if got := sub.Health.NextPollAt.Sub(t3); got != 12*time.Hour {
		t.Fatalf("304 after transient failure interval = %v, want 12h", got)
	}
}

func TestAdaptivePollInterval200And304ParityOnRollingUnguidedFeed(t *testing.T) {
	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	var currentIndex atomic.Int32
	var etag atomic.Value
	etag.Store(`"w0"`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		curETag, _ := etag.Load().(string)
		if r.Header.Get("If-None-Match") == curETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		idx := int(currentIndex.Load())
		pub := start.Add(time.Duration(idx) * 7 * 24 * time.Hour)
		body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>Weekly Unguided Roll</title><item><title>Post %d</title><pubDate>%s</pubDate></item></channel></rss>`,
			idx, formatRFC1123Z(pub))
		w.Header().Set("ETag", curETag)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	st := storetest.Open(t)
	ctx := context.Background()
	sub := addHealthSubscription(t, st, server.URL+"/feed.xml")
	clock := &testClock{now: start}
	poller := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), clock)

	var lastPoll time.Time
	for week := range 3 {
		currentIndex.Store(int32(week))
		etag.Store(fmt.Sprintf(`"w%d"`, week))
		lastPoll = start.Add(time.Duration(week)*7*24*time.Hour + time.Hour)
		clock.Set(lastPoll)
		sub = reloadSubscription(t, st, sub.ID)
		if _, err := poller.pollSubscription(ctx, sub); err != nil {
			t.Fatalf("week %d poll: %v", week, err)
		}
	}
	sub = reloadSubscription(t, st, sub.ID)
	wantWeekly := MaxPollInterval
	if got := sub.Health.NextPollAt.Sub(lastPoll); got != wantWeekly {
		t.Fatalf("rolling unguided 200 interval = %v, want %v", got, wantWeekly)
	}

	// Next poll returns 304 and must evaluate the exact same cadence history.
	t304 := lastPoll.Add(wantWeekly)
	clock.Set(t304)
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatalf("304 poll: %v", err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	if got := sub.Health.NextPollAt.Sub(t304); got != wantWeekly {
		t.Fatalf("rolling unguided 304 interval = %v, want %v", got, wantWeekly)
	}
}

func TestAdaptivePollIntervalUpgradeWithoutPollStatePreservesTTLBeyondMaxAge(t *testing.T) {
	start := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	const etag = `"v1"`
	var cc atomic.Value
	cc.Store("public, max-age=14400") // 4h
	var seenInm []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenInm = append(seenInm, r.Header.Get("If-None-Match"))
		if header, _ := cc.Load().(string); header != "" {
			w.Header().Set("Cache-Control", header)
		}
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write([]byte(rssWithDates(`<ttl>30</ttl>`, start.Add(-time.Hour))))
	}))
	t.Cleanup(server.Close)

	st := storetest.Open(t)
	ctx := context.Background()
	sub := addHealthSubscription(t, st, server.URL+"/feed.xml")
	// Simulate a pre-poll_state healthy poll that scheduled 30m from <ttl>30</ttl>.
	if err := st.RecordSubscriptionPollSuccess(ctx, sub, etag, "", start, start.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)

	t2 := start.Add(30 * time.Minute)
	clock := &testClock{now: t2}
	poller := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), clock)
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	if len(seenInm) != 1 || seenInm[0] != "" {
		t.Fatalf("upgrade poll without poll_state sent If-None-Match=%v, want empty unconditional refetch", seenInm)
	}
	if got := sub.Health.NextPollAt.Sub(t2); got != 30*time.Minute {
		t.Fatalf("upgrade 200 refetch interval with max-age=4h = %v, want 30m from <ttl>", got)
	}
}

func TestAdaptivePollInterval200UsesCataloguedCadenceOnSingleEntryFeed(t *testing.T) {
	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	var currentIndex atomic.Int32

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		idx := int(currentIndex.Load())
		pub := start.Add(time.Duration(idx) * 7 * 24 * time.Hour)
		body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>Weekly Single</title><item><title>Ep %d</title><guid>ep-%d</guid><pubDate>%s</pubDate><enclosure url="%s/ep-%d.mp3" type="audio/mpeg"/></item></channel></rss>`,
			idx, idx, formatRFC1123Z(pub), server.URL, idx)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	st := storetest.Open(t)
	ctx := context.Background()
	sub := addHealthSubscription(t, st, server.URL+"/feed.xml")
	if err := st.SetSubscriptionDownloadPolicy(ctx, sub.ID, store.SubscriptionPolicy{Policy: store.DownloadPolicyStreamOnly}); err != nil {
		t.Fatal(err)
	}
	clock := &testClock{now: start}
	poller := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), clock)

	var lastPoll time.Time
	for week := range 10 {
		currentIndex.Store(int32(week))
		lastPoll = start.Add(time.Duration(week)*7*24*time.Hour + time.Hour)
		clock.Set(lastPoll)
		sub = reloadSubscription(t, st, sub.ID)
		if _, err := poller.pollSubscription(ctx, sub); err != nil {
			t.Fatalf("week %d poll: %v", week, err)
		}
	}
	sub = reloadSubscription(t, st, sub.ID)
	if got := sub.Health.NextPollAt.Sub(lastPoll); got != MaxPollInterval {
		t.Fatalf("single-entry weekly feed interval after 10 publications = %v, want %v", got, MaxPollInterval)
	}
}

func TestAdaptivePollIntervalNotModifiedSeesTextOnlyEntriesForDormancy(t *testing.T) {
	start := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	oldEnclosurePub := start.Add(-200 * 24 * time.Hour)
	recentTextPub := start.Add(-30 * time.Minute)

	for _, tc := range []struct {
		name     string
		textItem string
	}{
		{
			name:     "guided text-only item",
			textItem: fmt.Sprintf(`<item><title>Update</title><guid>text-1</guid><pubDate>%s</pubDate></item>`, formatRFC1123Z(recentTextPub)),
		},
		{
			name:     "unguided text-only item",
			textItem: fmt.Sprintf(`<item><title>Update</title><pubDate>%s</pubDate></item>`, formatRFC1123Z(recentTextPub)),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const etag = `"v1"`
			body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>Feed</title><ttl>30</ttl>%s<item><title>Old Audio</title><guid>enc-1</guid><pubDate>%s</pubDate><enclosure url="https://cdn.example.com/old.mp3" type="audio/mpeg"/></item></channel></rss>`,
				tc.textItem, formatRFC1123Z(oldEnclosurePub))

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("If-None-Match") == etag {
					w.WriteHeader(http.StatusNotModified)
					return
				}
				w.Header().Set("ETag", etag)
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)

			st := storetest.Open(t)
			ctx := context.Background()
			sub := addHealthSubscription(t, st, server.URL+"/feed.xml")
			if err := st.SetSubscriptionDownloadPolicy(ctx, sub.ID, store.SubscriptionPolicy{Policy: store.DownloadPolicyStreamOnly}); err != nil {
				t.Fatal(err)
			}
			clock := &testClock{now: start}
			poller := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), clock)

			if _, err := poller.pollSubscription(ctx, sub); err != nil {
				t.Fatal(err)
			}
			sub = reloadSubscription(t, st, sub.ID)
			if got := sub.Health.NextPollAt.Sub(start); got != 30*time.Minute {
				t.Fatalf("200 interval = %v, want 30m", got)
			}

			t2 := start.Add(30 * time.Minute)
			clock.Set(t2)
			if _, err := poller.pollSubscription(ctx, sub); err != nil {
				t.Fatal(err)
			}
			sub = reloadSubscription(t, st, sub.ID)
			if got := sub.Health.NextPollAt.Sub(t2); got != 30*time.Minute {
				t.Fatalf("304 interval = %v, want 30m", got)
			}
		})
	}
}

func TestAdaptivePollLoad500FeedsReducesRequestsByAtLeastHalf(t *testing.T) {
	const (
		feedCount = 500
		simHours  = 48
		tickStep  = MinPollInterval
	)
	start := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)

	type feedFixture struct {
		etag         string
		cacheControl string
		body         string
	}
	fixtures := make([]feedFixture, feedCount)
	for i := range feedCount {
		etag := fmt.Sprintf(`"feed-%d"`, i)
		var cc, body string
		switch {
		case i < 150:
			age := time.Duration(185+i*2) * 24 * time.Hour
			extras := ""
			switch i % 3 {
			case 0:
				extras = `<ttl>60</ttl>`
			case 1:
				extras = `<sy:updatePeriod>hourly</sy:updatePeriod>`
				cc = "max-age=300"
			}
			body = rssWithDates(extras, regularDates(start.Add(-age), 7*24*time.Hour, 5)...)
		case i < 300:
			step := 7 * 24 * time.Hour
			if i%3 == 0 {
				step = 14 * 24 * time.Hour
			}
			if i%2 == 0 {
				cc = "public, max-age=300"
			}
			body = rssWithDates("", regularDates(start.Add(-24*time.Hour), step, 8)...)
		case i < 375:
			if i%3 == 0 {
				body = rssWithDates(`<sy:updatePeriod>daily</sy:updatePeriod>`, regularDates(start.Add(-6*time.Hour), 24*time.Hour, 8)...)
			} else {
				body = rssWithDates("", regularDates(start.Add(-6*time.Hour), 24*time.Hour, 10)...)
			}
		case i < 425:
			switch i % 3 {
			case 0:
				body = rssWithDates(`<podcast:updateFrequency>Every 6 hours</podcast:updateFrequency>`, regularDates(start.Add(-2*time.Hour), 6*time.Hour, 6)...)
			case 1:
				body = rssWithDates(`<ttl>240</ttl>`, regularDates(start.Add(-2*time.Hour), 4*time.Hour, 6)...)
			default:
				cc = "max-age=14400"
				body = rssWithDates("", regularDates(start.Add(-time.Hour), 2*time.Hour, 6)...)
			}
		case i < 475:
			if i%2 == 0 {
				body = rssWithDates(`<ttl>30</ttl>`, regularDates(start.Add(-15*time.Minute), time.Hour, 10)...)
			} else {
				body = rssWithDates("", regularDates(start.Add(-30*time.Minute), 2*time.Hour, 10)...)
			}
		default:
			body = rssWithDates("")
		}
		fixtures[i] = feedFixture{etag: etag, cacheControl: cc, body: body}
	}

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		idStr := strings.TrimPrefix(r.URL.Path, "/feed-")
		idStr = strings.TrimSuffix(idStr, ".xml")
		idx, err := strconv.Atoi(idStr)
		if err != nil || idx < 0 || idx >= len(fixtures) {
			http.NotFound(w, r)
			return
		}
		f := fixtures[idx]
		if f.cacheControl != "" {
			w.Header().Set("Cache-Control", f.cacheControl)
		}
		w.Header().Set("ETag", f.etag)
		if r.Header.Get("If-None-Match") == f.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write([]byte(f.body))
	}))
	t.Cleanup(server.Close)

	st := storetest.Open(t)
	ctx := context.Background()
	root := t.TempDir()
	subs := make([]store.Subscription, feedCount)
	for i := range feedCount {
		sub, err := st.AddSubscription(ctx, store.Subscription{
			LibraryID: "library-local",
			FeedURL:   fmt.Sprintf("%s/feed-%d.xml", server.URL, i),
			Root:      root,
		}, start)
		if err != nil {
			t.Fatal(err)
		}
		subs[i] = sub
	}

	var clockMu sync.Mutex
	now := start
	poller := NewPoller(st, scrape.New(scrape.WithInsecureLoopback()))
	poller.now = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	poller.jitter = func() float64 { return 0.5 }

	ticks := int((simHours * time.Hour) / tickStep)
	for tick := 0; tick < ticks; tick++ {
		tickTime := start.Add(time.Duration(tick) * tickStep)
		clockMu.Lock()
		now = tickTime
		clockMu.Unlock()

		for i := range subs {
			if !subs[i].Health.Due(tickTime) {
				continue
			}
			if _, err := poller.pollSubscription(ctx, subs[i]); err != nil {
				t.Fatalf("poll feed %d at tick %d: %v", i, tick, err)
			}
			updated, err := st.Subscription(ctx, subs[i].ID)
			if err != nil {
				t.Fatal(err)
			}
			subs[i] = updated
		}
	}

	gotRequests := requests.Load()
	fixedHourlyRequests := int64(feedCount * simHours)
	maxAllowed := fixedHourlyRequests / 2
	t.Logf("500 feeds over %dh: adaptive=%d requests vs fixed_hourly=%d (%.1f%% reduction)",
		simHours, gotRequests, fixedHourlyRequests, 100*float64(fixedHourlyRequests-gotRequests)/float64(fixedHourlyRequests))
	if gotRequests > maxAllowed {
		t.Fatalf("adaptive requests = %d, want <= %d (at least 50%% fewer than fixed hourly %d)",
			gotRequests, maxAllowed, fixedHourlyRequests)
	}
}

func TestAdaptivePollRefetchesUnconditionallyOnPollStateMismatchOrURLChange(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	var seenIfNoneMatch []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inm := r.Header.Get("If-None-Match")
		seenIfNoneMatch = append(seenIfNoneMatch, inm)
		if inm == `"v2"` {
			w.Header().Set("ETag", `"v2"`)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v2"`)
		_, _ = w.Write([]byte(rssWithDates(`<ttl>20</ttl>`, start.Add(-time.Hour))))
	}))
	t.Cleanup(server.Close)

	st := storetest.Open(t)
	sub := addHealthSubscription(t, st, server.URL+"/feed-a.xml")
	clock := &testClock{now: start}
	poller := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), clock)

	// Poll 1 populates poll_state bound to "v2" and feed-a.xml.
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)

	// Simulate rollback/drift where subscription.ETag differs from poll_state's ETag.
	if err := st.SaveSubscriptionPollState(ctx, sub.ID, store.SubscriptionPollState{
		FeedURLHash:    store.PollFeedURLHash(sub.FeedURL),
		ETag:           `"old-v1"`,
		UpdateInterval: time.Hour,
	}, start); err != nil {
		t.Fatal(err)
	}
	clock.now = start.Add(20 * time.Minute)
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	if len(seenIfNoneMatch) != 2 || seenIfNoneMatch[1] != "" {
		t.Fatalf("ETag mismatch sent If-None-Match=%q, want unconditional empty header", seenIfNoneMatch)
	}
	sub = reloadSubscription(t, st, sub.ID)

	// Steady bound poll sends If-None-Match="v2" and hits 304 cleanly.
	clock.now = clock.now.Add(20 * time.Minute)
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	if len(seenIfNoneMatch) != 3 || seenIfNoneMatch[2] != `"v2"` {
		t.Fatalf("bound poll sent %v, want If-None-Match=\"v2\"", seenIfNoneMatch)
	}

	// Changing FeedURL without clearing subscription.ETag also forces unconditional refetch.
	sub.FeedURL = server.URL + "/feed-b.xml"
	clock.now = clock.now.Add(20 * time.Minute)
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	if len(seenIfNoneMatch) != 4 || seenIfNoneMatch[3] != "" {
		t.Fatalf("FeedURL change sent If-None-Match=%q, want empty", seenIfNoneMatch)
	}
}

func TestAdaptivePollUndatedFeedEntryDoesNotMaskStoredDate(t *testing.T) {
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	pub1 := now.Add(-10 * 24 * time.Hour)
	pub2 := now.Add(-20 * 24 * time.Hour)

	doc := scrape.FeedDocument{
		Entries: []scrape.FeedEntry{
			{GUID: "ep-1"}, // later response omitted pubDate
			{GUID: "ep-2", PublishedAt: pub2},
		},
	}
	stored := []store.SubscriptionEntry{
		{GUID: "ep-1", PublishedAt: pub1},
		{GUID: "ep-2", PublishedAt: pub2},
	}
	retained, pubDates := mergeFeedEntryDates(doc, stored, nil)
	if len(retained) != 2 || len(pubDates) != 2 {
		t.Fatalf("retained=%+v pubDates=%v, want ep-1 date preserved from stored", retained, pubDates)
	}
	foundPub1 := false
	for _, r := range retained {
		if r.PublishedAt.Equal(pub1) {
			foundPub1 = true
			break
		}
	}
	if !foundPub1 {
		t.Fatalf("undated ep-1 shadowed stored timestamp %v: got %+v", pub1, retained)
	}
}

func TestAdaptivePollSteady304UsesPollStateWithoutQueryingEntries(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	const etag = `"v1"`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		// 6-day cadence => 3-day adaptive interval (distinct from DefaultPollInterval=1h and MaxPollInterval=24h? Wait clamped to 24h; let's use 4h cadence => 2h interval).
		_, _ = w.Write([]byte(rssWithDates("", start.Add(-2*time.Hour), start.Add(-6*time.Hour), start.Add(-10*time.Hour))))
	}))
	t.Cleanup(server.Close)

	st := storetest.Open(t)
	sub := addHealthSubscription(t, st, server.URL+"/feed.xml")
	clock := &testClock{now: start}
	poller := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), clock)
	var entryLoads atomic.Int32
	poller.testLoadEntryPollDates = func(ctx context.Context, id store.SubscriptionID) ([]store.SubscriptionEntry, error) {
		entryLoads.Add(1)
		return st.SubscriptionEntryPollDates(ctx, id)
	}

	// Poll 1: 200 seeds poll_state with 2h observed cadence (loads poll dates once).
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	if got := sub.Health.NextPollAt.Sub(start); got != 2*time.Hour {
		t.Fatalf("Poll 1 interval = %v, want 2h", got)
	}
	if entryLoads.Load() != 1 {
		t.Fatalf("Poll 1 entry loads = %d, want 1", entryLoads.Load())
	}

	// Poll 2: bound 304 must use poll_state directly with zero subscription_entry queries.
	clock.now = start.Add(2 * time.Hour)
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	if got := sub.Health.NextPollAt.Sub(clock.now); got != 2*time.Hour {
		t.Fatalf("steady 304 interval = %v, want 2h from poll_state", got)
	}
	if entryLoads.Load() != 1 {
		t.Fatalf("steady 304 queried subscription_entry %d extra times, want 0", entryLoads.Load()-1)
	}
}

func TestAdaptivePollEmptySubscriptionBecomesDormantAfter180Days(t *testing.T) {
	ctx := context.Background()
	addedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := addedAt.Add(200 * 24 * time.Hour)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<rss><channel><title>Empty</title></channel></rss>`))
	}))
	t.Cleanup(server.Close)

	st := storetest.Open(t)
	sub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   server.URL + "/empty.xml",
		Root:      t.TempDir(),
	}, addedAt)
	if err != nil {
		t.Fatal(err)
	}
	poller := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), &testClock{now: now})
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	if got := sub.Health.NextPollAt.Sub(now); got != MaxPollInterval {
		t.Fatalf("200-day-old empty feed interval = %v, want %v (dormant)", got, MaxPollInterval)
	}
}

func TestAdaptivePollUndatedUpgradeRefetchPreservesCataloguedDate(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	dormantPub := now.Add(-200 * 24 * time.Hour)
	includeDate := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if includeDate {
			_, _ = w.Write([]byte(`<rss><channel><title>Old</title><item><guid>ep-old</guid><title>Ep Old</title><pubDate>` +
				dormantPub.Format(time.RFC1123Z) + `</pubDate><enclosure url="https://example.com/ep.mp3" length="10" type="audio/mpeg"/></item></channel></rss>`))
			return
		}
		_, _ = w.Write([]byte(`<rss><channel><title>Old</title><item><guid>ep-old</guid><title>Ep Old</title><enclosure url="https://example.com/ep.mp3" length="10" type="audio/mpeg"/></item></channel></rss>`))
	}))
	t.Cleanup(server.Close)

	st := storetest.Open(t)
	sub := addHealthSubscription(t, st, server.URL+"/feed.xml")
	poller := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), &testClock{now: now})
	// Poll 1 catalogues ep-old with dormantPub.
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	// Simulate pre-poll_state upgrade: delete poll_state and clear date in second response.
	if err := st.SaveSubscriptionPollState(ctx, sub.ID, store.SubscriptionPollState{}, now); err != nil {
		t.Fatal(err)
	}
	includeDate = false
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	if got := sub.Health.NextPollAt.Sub(now); got != MaxPollInterval {
		t.Fatalf("interval after undated refresh = %v, want %v (dormant date preserved)", got, MaxPollInterval)
	}
}

func TestAdaptivePollURLChangeDiscardsForeignPollHistory(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	dormantPub := now.Add(-200 * 24 * time.Hour)
	var oldServerURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/old.mp3":
			_, _ = w.Write([]byte("fake mp3 bytes"))
		case "/old.xml":
			w.Header().Set("ETag", `"old"`)
			_, _ = w.Write([]byte(`<rss><channel><title>Old</title><item><guid>ep-old</guid><title>Ep Old</title><pubDate>` +
				dormantPub.Format(time.RFC1123Z) + `</pubDate><enclosure url="` + oldServerURL + `/old.mp3" length="14" type="audio/mpeg"/></item></channel></rss>`))
		case "/new-empty.xml":
			_, _ = w.Write([]byte(`<rss><channel><title>Fresh Empty</title></channel></rss>`))
		}
	}))
	oldServerURL = server.URL
	t.Cleanup(server.Close)

	st := storetest.Open(t)
	sub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   server.URL + "/old.xml",
		Root:      t.TempDir(),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	poller := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), &testClock{now: now})
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	// Point Subscription to a fresh empty feed URL; foreign dormant history must
	// not apply on the first or subsequent polls.
	for _, round := range []string{"poll 1 on new URL", "poll 2 on new URL"} {
		sub.FeedURL = server.URL + "/new-empty.xml"
		if _, err := poller.pollSubscription(ctx, sub); err != nil {
			t.Fatalf("%s: %v", round, err)
		}
		sub = reloadSubscription(t, st, sub.ID)
		if got := sub.Health.NextPollAt.Sub(now); got != DefaultPollInterval {
			t.Fatalf("%s interval = %v, want %v (no foreign history inherited)", round, got, DefaultPollInterval)
		}
	}

	// Simulate a persisted URL move (e.g. via move confirmation) so sub.FeedURL in DB
	// is now /new-empty.xml; subsequent polls still ignore pre-move ep-old rows in DB.
	movedAt := now.Add(time.Hour)
	for range store.MovedFeedObservations {
		if _, err := st.RecordSubscriptionMoveObservation(ctx, sub.ID, server.URL+"/new-empty.xml", movedAt); err != nil {
			t.Fatal(err)
		}
	}
	sub = reloadSubscription(t, st, sub.ID)
	clock := &testClock{now: movedAt}
	pollerMoved := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), clock)
	for _, round := range []string{"post-move poll 1", "post-move poll 2"} {
		if _, err := pollerMoved.pollSubscription(ctx, sub); err != nil {
			t.Fatalf("%s: %v", round, err)
		}
		sub = reloadSubscription(t, st, sub.ID)
		if got := sub.Health.NextPollAt.Sub(clock.now); got != DefaultPollInterval {
			t.Fatalf("%s interval = %v, want %v (pre-move catalogue history must stay ignored)", round, got, DefaultPollInterval)
		}
		clock.now = clock.now.Add(time.Hour)
	}
}

func TestAdaptivePollActiveUndatedFeedIsNotMarkedDormantAfter180Days(t *testing.T) {
	ctx := context.Background()
	oldAdded := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	now := oldAdded.Add(200 * 24 * time.Hour)
	clock := &testClock{now: oldAdded}
	useNewEntry := false
	var baseURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/u1.mp3" {
			_, _ = w.Write([]byte("fake mp3 bytes"))
			return
		}
		if r.Header.Get("If-None-Match") == `"undated-v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if !useNewEntry {
			_, _ = w.Write([]byte(`<rss><channel><title>Undated Empty</title></channel></rss>`))
			return
		}
		w.Header().Set("ETag", `"undated-v1"`)
		_, _ = w.Write([]byte(`<rss><channel><title>Undated Active</title><ttl>15</ttl><item><guid>u-1</guid><title>Today</title><enclosure url="` + baseURL + `/u1.mp3" length="14" type="audio/mpeg"/></item></channel></rss>`))
	}))
	baseURL = server.URL
	t.Cleanup(server.Close)

	st := storetest.Open(t)
	sub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   server.URL + "/feed.xml",
		Root:      t.TempDir(),
	}, oldAdded)
	if err != nil {
		t.Fatal(err)
	}
	poller := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), clock)

	// Genuinely empty feed 200 days after AddedAt is dormant.
	clock.now = now
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	if got := sub.Health.NextPollAt.Sub(now); got != MaxPollInterval {
		t.Fatalf("empty 200d feed interval = %v, want %v", got, MaxPollInterval)
	}

	// Publishing a fresh undated entry reactivates its explicit 15m TTL interval.
	useNewEntry = true
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	if got := sub.Health.NextPollAt.Sub(now); got != 15*time.Minute {
		t.Fatalf("active undated feed interval = %v, want 15m", got)
	}

	// Subsequent 304 retains the non-dormant 15m schedule.
	clock.now = now.Add(15 * time.Minute)
	if _, err := poller.pollSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	if got := sub.Health.NextPollAt.Sub(clock.now); got != 15*time.Minute {
		t.Fatalf("304 undated active feed interval = %v, want 15m", got)
	}
}

func TestAdaptivePollDormancyMixedDatedTextOnlyAndCataloguedRecovery(t *testing.T) {
	ctx := context.Background()
	oldAdded := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	day0 := oldAdded
	day200 := oldAdded.Add(200 * 24 * time.Hour)
	oldDated := day200.Add(-200 * 24 * time.Hour)

	// 1. Dated entry older than 180d + fresh undated enclosure today stays active (15m TTL).
	mode := "mixed"
	var baseURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ep.mp3":
			_, _ = w.Write([]byte("audio bytes"))
		case "/mixed.xml":
			_, _ = w.Write([]byte(`<rss><channel><title>Mixed</title><ttl>15</ttl>` +
				`<item><guid>old-dated</guid><title>Old</title><pubDate>` + oldDated.Format(time.RFC1123Z) + `</pubDate><enclosure url="` + baseURL + `/ep.mp3" length="11" type="audio/mpeg"/></item>` +
				`<item><guid>fresh-undated</guid><title>New Undated</title><enclosure url="` + baseURL + `/ep.mp3" length="11" type="audio/mpeg"/></item>` +
				`</channel></rss>`))
		case "/text.xml":
			_, _ = w.Write([]byte(`<rss><channel><title>Text</title><ttl>15</ttl><item><guid>txt-1</guid><title>Text Only ` + mode + `</title></item></channel></rss>`))
		case "/recover.xml":
			_, _ = w.Write([]byte(`<rss><channel><title>Recover</title><ttl>15</ttl><item><guid>rec-1</guid><title>Existing</title><enclosure url="` + baseURL + `/ep.mp3" length="11" type="audio/mpeg"/></item></channel></rss>`))
		}
	}))
	baseURL = server.URL
	t.Cleanup(server.Close)

	st := storetest.Open(t)
	clock := &testClock{now: day200}
	poller := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), clock)

	mixedSub, err := st.AddSubscription(ctx, store.Subscription{LibraryID: "library-local", FeedURL: baseURL + "/mixed.xml", Root: t.TempDir()}, oldAdded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := poller.pollSubscription(ctx, mixedSub); err != nil {
		t.Fatal(err)
	}
	mixedSub = reloadSubscription(t, st, mixedSub.ID)
	if got := mixedSub.Health.NextPollAt.Sub(day200); got != 15*time.Minute {
		t.Fatalf("mixed old-dated + new-undated interval = %v, want 15m", got)
	}

	// 2. Repeated HTTP 200 with unchanged text-only undated item does not refresh LastNewEntryAt.
	clock.now = day0
	textSub, err := st.AddSubscription(ctx, store.Subscription{LibraryID: "library-local", FeedURL: baseURL + "/text.xml", Root: t.TempDir()}, oldAdded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := poller.pollSubscription(ctx, textSub); err != nil {
		t.Fatal(err)
	}
	clock.now = day200
	mode = "unchanged"
	textSub = reloadSubscription(t, st, textSub.ID)
	if _, err := poller.pollSubscription(ctx, textSub); err != nil {
		t.Fatal(err)
	}
	textSub = reloadSubscription(t, st, textSub.ID)
	if got := textSub.Health.NextPollAt.Sub(day200); got != MaxPollInterval {
		t.Fatalf("repeated unchanged text-only undated interval at day200 = %v, want %v (dormant)", got, MaxPollInterval)
	}

	// 3. Recovery after catalogue committed rec-1 at day200 while poll_state still held LastNewEntryAt = day0.
	clock.now = day0
	recSub, err := st.AddSubscription(ctx, store.Subscription{LibraryID: "library-local", FeedURL: baseURL + "/recover.xml", Root: t.TempDir()}, oldAdded)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordSubscriptionPollSuccessWithState(ctx, recSub, "", "", &store.SubscriptionPollState{
		FeedURLHash:    store.PollFeedURLHash(recSub.FeedURL),
		LastNewEntryAt: day0,
	}, day0, day0.Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSubscriptionCatalogWithInitialDownloads(ctx, recSub.ID, []store.FeedCatalogEntry{
		{GUID: "rec-1", Title: "New", EnclosureURL: baseURL + "/ep.mp3", EnclosureLength: 11, Position: 0},
	}, day200, nil); err != nil {
		t.Fatal(err)
	}
	clock.now = day200
	recSub = reloadSubscription(t, st, recSub.ID)
	if _, err := poller.pollSubscription(ctx, recSub); err != nil {
		t.Fatal(err)
	}
	recSub = reloadSubscription(t, st, recSub.ID)
	if got := recSub.Health.NextPollAt.Sub(day200); got != 15*time.Minute {
		t.Fatalf("recovery with newer catalogued FirstSeenAt interval = %v, want 15m", got)
	}
}

func TestAdaptivePoll513UndatedTextItemsAndNewUndatedRSSTextPost(t *testing.T) {
	ctx := context.Background()
	day0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	day200 := day0.Add(200 * 24 * time.Hour)

	var b strings.Builder
	b.WriteString(`<rss><channel><title>Many Undated</title><ttl>15</ttl>`)
	for i := 0; i < 513; i++ {
		fmt.Fprintf(&b, `<item><guid>u-%d</guid><title>Item %d</title></item>`, i, i)
	}
	b.WriteString(`</channel></rss>`)
	feed513 := b.String()

	hasNewTextPost := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/many.xml":
			_, _ = w.Write([]byte(feed513))
		case "/textpost.xml":
			extra := ""
			if hasNewTextPost {
				extra = `<item><title>Fresh Text Post</title><link>https://example.org/posts/2</link></item>`
			}
			_, _ = w.Write([]byte(`<rss><channel><title>Text</title><ttl>15</ttl><item><title>Initial Post</title><link>https://example.org/posts/1</link></item>` + extra + `</channel></rss>`))
		}
	}))
	t.Cleanup(server.Close)

	st := storetest.Open(t)
	clock := &testClock{now: day0}
	poller := healthPoller(st, scrape.New(scrape.WithInsecureLoopback()), clock)

	manySub, err := st.AddSubscription(ctx, store.Subscription{LibraryID: "library-local", FeedURL: server.URL + "/many.xml", Root: t.TempDir()}, day0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := poller.pollSubscription(ctx, manySub); err != nil {
		t.Fatal(err)
	}
	clock.now = day200
	manySub = reloadSubscription(t, st, manySub.ID)
	if _, err := poller.pollSubscription(ctx, manySub); err != nil {
		t.Fatal(err)
	}
	manySub = reloadSubscription(t, st, manySub.ID)
	if got := manySub.Health.NextPollAt.Sub(day200); got != MaxPollInterval {
		t.Fatalf("513 unchanged undated items interval at day200 = %v, want %v (dormant)", got, MaxPollInterval)
	}

	// Dormant text-only RSS feed with no GUID/enclosure must wake from 24h to 15m TTL
	// when a new undated RSS text post appears.
	clock.now = day0
	textSub, err := st.AddSubscription(ctx, store.Subscription{LibraryID: "library-local", FeedURL: server.URL + "/textpost.xml", Root: t.TempDir()}, day0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := poller.pollSubscription(ctx, textSub); err != nil {
		t.Fatal(err)
	}
	clock.now = day200
	textSub = reloadSubscription(t, st, textSub.ID)
	if _, err := poller.pollSubscription(ctx, textSub); err != nil {
		t.Fatal(err)
	}
	textSub = reloadSubscription(t, st, textSub.ID)
	if got := textSub.Health.NextPollAt.Sub(day200); got != MaxPollInterval {
		t.Fatalf("dormant text-post feed before new item = %v, want %v", got, MaxPollInterval)
	}
	hasNewTextPost = true
	if _, err := poller.pollSubscription(ctx, textSub); err != nil {
		t.Fatal(err)
	}
	textSub = reloadSubscription(t, st, textSub.ID)
	if got := textSub.Health.NextPollAt.Sub(day200); got != 15*time.Minute {
		t.Fatalf("new undated RSS text post without GUID/enclosure interval = %v, want 15m", got)
	}

	// Additional dormancy edge cases: (a) removing 1 of 4097 old undated text posts,
	// (b) removing pubDate from an existing dated GUID, and (c) editing only description
	// behind a stable permalink must all stay dormant (24h) at day200.
	var b4097 strings.Builder
	b4097.WriteString(`<rss><channel><title>4097</title><ttl>15</ttl>`)
	for i := 0; i < 4097; i++ {
		fmt.Fprintf(&b4097, `<item><guid>g-%d</guid><title>T %d</title></item>`, i, i)
	}
	b4097.WriteString(`</channel></rss>`)
	var b4096 strings.Builder
	b4096.WriteString(`<rss><channel><title>4097</title><ttl>15</ttl>`)
	for i := 1; i < 4097; i++ {
		fmt.Fprintf(&b4096, `<item><guid>g-%d</guid><title>T %d</title></item>`, i, i)
	}
	b4096.WriteString(`</channel></rss>`)

	dropFirst := false
	stripDate := false
	editDesc := false
	edgeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/4097.xml":
			if dropFirst {
				_, _ = w.Write([]byte(b4096.String()))
			} else {
				_, _ = w.Write([]byte(b4097.String()))
			}
		case "/stripdate.xml":
			pub := `<pubDate>` + day0.Format(time.RFC1123Z) + `</pubDate>`
			if stripDate {
				pub = ""
			}
			_, _ = w.Write([]byte(`<rss><channel><title>D</title><ttl>15</ttl><item><guid>dated-1</guid><title>Ep</title>` + pub + `<enclosure url="https://example.org/a.mp3" length="10" type="audio/mpeg"/></item></channel></rss>`))
		case "/permalink.xml":
			desc := "old description"
			if editDesc {
				desc = "updated description"
			}
			_, _ = w.Write([]byte(`<rss><channel><title>P</title><ttl>15</ttl><item><title>Stable Post</title><link>https://example.org/posts/stable</link><description>` + desc + `</description></item></channel></rss>`))
		}
	}))
	t.Cleanup(edgeServer.Close)

	for _, tc := range []struct {
		name   string
		path   string
		mutate func()
	}{
		{"drop first of 4097", "/4097.xml", func() { dropFirst = true }},
		{"strip pubDate from existing GUID", "/stripdate.xml", func() { stripDate = true }},
		{"edit description behind stable permalink", "/permalink.xml", func() { editDesc = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock.now = day0
			sub, err := st.AddSubscription(ctx, store.Subscription{LibraryID: "library-local", FeedURL: edgeServer.URL + tc.path, Root: t.TempDir()}, day0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := poller.pollSubscription(ctx, sub); err != nil {
				t.Fatal(err)
			}
			clock.now = day200
			tc.mutate()
			sub = reloadSubscription(t, st, sub.ID)
			if _, err := poller.pollSubscription(ctx, sub); err != nil {
				t.Fatal(err)
			}
			sub = reloadSubscription(t, st, sub.ID)
			if got := sub.Health.NextPollAt.Sub(day200); got != MaxPollInterval {
				t.Fatalf("%s interval at day200 = %v, want %v (dormant)", tc.name, got, MaxPollInterval)
			}
		})
	}
}
