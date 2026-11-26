package scrape

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseFeedUpdateHints(t *testing.T) {
	cases := []struct {
		name         string
		xml          string
		wantInterval time.Duration
		wantComplete bool
	}{
		{
			name:         "podcast updateFrequency rrule hourly",
			xml:          `<rss xmlns:podcast="https://podcastindex.org/namespace/1.0"><channel><title>F</title><podcast:updateFrequency rrule="FREQ=HOURLY;INTERVAL=3">Every 3 hours</podcast:updateFrequency></channel></rss>`,
			wantInterval: 3 * time.Hour,
		},
		{
			name:         "podcast updateFrequency rrule daily byhour",
			xml:          `<rss xmlns:podcast="https://podcastindex.org/namespace/1.0"><channel><title>F</title><podcast:updateFrequency rrule="FREQ=DAILY;BYHOUR=6,18">Twice daily</podcast:updateFrequency></channel></rss>`,
			wantInterval: 12 * time.Hour,
		},
		{
			name:         "podcast updateFrequency rrule daily interval>1 with byhour",
			xml:          `<rss xmlns:podcast="https://podcastindex.org/namespace/1.0"><channel><title>F</title><podcast:updateFrequency rrule="FREQ=DAILY;INTERVAL=2;BYHOUR=0,6,12,18">Four times every two days</podcast:updateFrequency></channel></rss>`,
			wantInterval: 12 * time.Hour,
		},
		{
			name:         "podcast updateFrequency rrule weekly interval>1 with byday and byhour",
			xml:          `<rss xmlns:podcast="https://podcastindex.org/namespace/1.0"><channel><title>F</title><podcast:updateFrequency rrule="FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,TU,WE,TH,FR,SA,SU;BYHOUR=0,6,12,18">Four times daily on active weeks</podcast:updateFrequency></channel></rss>`,
			wantInterval: 12 * time.Hour,
		},
		{
			name:         "podcast updateFrequency text ISO duration",
			xml:          `<rss xmlns:podcast="https://podcastindex.org/namespace/1.0"><channel><title>F</title><podcast:updateFrequency>PT45M</podcast:updateFrequency></channel></rss>`,
			wantInterval: 45 * time.Minute,
		},
		{
			name:         "podcast updateFrequency text keyword",
			xml:          `<rss xmlns:podcast="https://podcastindex.org/namespace/1.0"><channel><title>F</title><podcast:updateFrequency>Twice daily</podcast:updateFrequency></channel></rss>`,
			wantInterval: 12 * time.Hour,
		},
		{
			name:         "podcast updateFrequency complete",
			xml:          `<rss xmlns:podcast="https://podcastindex.org/namespace/1.0"><channel><title>F</title><podcast:updateFrequency complete="true">Weekly</podcast:updateFrequency><ttl>15</ttl></channel></rss>`,
			wantInterval: 24 * time.Hour,
			wantComplete: true,
		},
		{
			name:         "rss ttl",
			xml:          `<rss><channel><title>F</title><ttl>90</ttl></channel></rss>`,
			wantInterval: 90 * time.Minute,
		},
		{
			name:         "rss ttl invalid falls through to sy",
			xml:          `<rss xmlns:sy="http://purl.org/rss/1.0/modules/syndication/"><channel><title>F</title><ttl>0</ttl><sy:updatePeriod>hourly</sy:updatePeriod><sy:updateFrequency>4</sy:updateFrequency></channel></rss>`,
			wantInterval: 15 * time.Minute,
		},
		{
			name:         "sy updatePeriod custom prefix",
			xml:          `<rss xmlns:syn="http://purl.org/rss/1.0/modules/syndication/"><channel><title>F</title><syn:updatePeriod>daily</syn:updatePeriod><syn:updateFrequency>3</syn:updateFrequency></channel></rss>`,
			wantInterval: 8 * time.Hour,
		},
		{
			name:         "sy updatePeriod namespace declared on element itself",
			xml:          `<rss><channel><title>F</title><s:updatePeriod xmlns:s="http://purl.org/rss/1.0/modules/syndication/">daily</s:updatePeriod></channel></rss>`,
			wantInterval: 24 * time.Hour,
		},
		{
			name:         "sy default namespace declared on element itself",
			xml:          `<rss><channel><title>F</title><updatePeriod xmlns="http://purl.org/rss/1.0/modules/syndication/">daily</updatePeriod><updateFrequency xmlns="http://purl.org/rss/1.0/modules/syndication/">2</updateFrequency></channel></rss>`,
			wantInterval: 12 * time.Hour,
		},
		{
			name:         "sy updateFrequency is not confused with podcast updateFrequency",
			xml:          `<rss xmlns:sy="http://purl.org/rss/1.0/modules/syndication/"><channel><title>F</title><sy:updatePeriod>daily</sy:updatePeriod><sy:updateFrequency>2</sy:updateFrequency></channel></rss>`,
			wantInterval: 12 * time.Hour,
		},
		{
			name:         "precedence updateFrequency over ttl and sy",
			xml:          `<rss xmlns:podcast="https://podcastindex.org/namespace/1.0" xmlns:sy="http://purl.org/rss/1.0/modules/syndication/"><channel><title>F</title><sy:updatePeriod>hourly</sy:updatePeriod><ttl>30</ttl><podcast:updateFrequency>Every 2 hours</podcast:updateFrequency></channel></rss>`,
			wantInterval: 2 * time.Hour,
		},
		{
			name:         "precedence ttl over sy",
			xml:          `<rss xmlns:sy="http://purl.org/rss/1.0/modules/syndication/"><channel><title>F</title><sy:updatePeriod>daily</sy:updatePeriod><ttl>45</ttl></channel></rss>`,
			wantInterval: 45 * time.Minute,
		},
		{
			name:         "unparseable updateFrequency falls through to ttl",
			xml:          `<rss xmlns:podcast="https://podcastindex.org/namespace/1.0"><channel><title>F</title><podcast:updateFrequency>whenever</podcast:updateFrequency><ttl>20</ttl></channel></rss>`,
			wantInterval: 20 * time.Minute,
		},
		{
			name:         "atom feed updateFrequency",
			xml:          `<feed xmlns="http://www.w3.org/2005/Atom" xmlns:podcast="https://podcastindex.org/namespace/1.0"><title>F</title><podcast:updateFrequency>Every 6 hours</podcast:updateFrequency></feed>`,
			wantInterval: 6 * time.Hour,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := ParseFeed([]byte(tc.xml))
			if err != nil {
				t.Fatalf("ParseFeed: %v", err)
			}
			if doc.UpdateInterval != tc.wantInterval || doc.Complete != tc.wantComplete {
				t.Fatalf("got (%v, complete=%t), want (%v, complete=%t)", doc.UpdateInterval, doc.Complete, tc.wantInterval, tc.wantComplete)
			}
		})
	}
}

func TestFetchFeedCacheControlMaxAge(t *testing.T) {
	cases := []struct {
		name    string
		headers []string
		status  int
		want    time.Duration
	}{
		{
			name:    "single max-age",
			headers: []string{"public, max-age=3600"},
			status:  http.StatusOK,
			want:    time.Hour,
		},
		{
			name:    "quoted and case-insensitive max-age",
			headers: []string{`Max-Age="1800", s-maxage=7200`},
			status:  http.StatusOK,
			want:    30 * time.Minute,
		},
		{
			name:    "escaped quote in preceding directive does not hide max-age",
			headers: []string{`ext="a\"b", max-age=43200`},
			status:  http.StatusOK,
			want:    12 * time.Hour,
		},
		{
			name:    "escaped backslash before closing quote does not hide max-age",
			headers: []string{`ext="a\\", max-age=43200`},
			status:  http.StatusOK,
			want:    12 * time.Hour,
		},
		{
			name:    "no-cache suppresses max-age",
			headers: []string{"no-cache, max-age=3600"},
			status:  http.StatusOK,
			want:    0,
		},
		{
			name:    "no-store in second header suppresses max-age",
			headers: []string{"max-age=3600", "no-store"},
			status:  http.StatusOK,
			want:    0,
		},
		{
			name:    "304 preserves max-age",
			headers: []string{"max-age=7200"},
			status:  http.StatusNotModified,
			want:    2 * time.Hour,
		},
		{
			name:    "huge max-age does not overflow",
			headers: []string{"max-age=999999999999999"},
			status:  http.StatusOK,
			want:    maxParsedPollInterval,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for _, h := range tc.headers {
					w.Header().Add("Cache-Control", h)
				}
				w.WriteHeader(tc.status)
				if tc.status == http.StatusOK {
					_, _ = w.Write([]byte(`<rss><channel><title>F</title></channel></rss>`))
				}
			}))
			t.Cleanup(server.Close)

			engine := New(WithInsecureLoopback())
			res, err := engine.FetchFeed(t.Context(), server.URL+"/feed.xml", "", "", "")
			if err != nil {
				t.Fatalf("FetchFeed: %v", err)
			}
			if res.MaxAge != tc.want {
				t.Fatalf("MaxAge = %v, want %v", res.MaxAge, tc.want)
			}
		})
	}
}
