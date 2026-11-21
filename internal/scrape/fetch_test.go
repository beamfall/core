package scrape

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

const ingestTestFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd">
  <channel>
    <title>Test Feed</title>
    <item>
      <title>First Entry</title>
      <guid isPermaLink="false">guid-first</guid>
      <enclosure url="https://cdn.example.com/first.mp3" type="audio/mpeg" length="123"/>
    </item>
    <item>
      <title>No GUID Entry</title>
      <enclosure url="https://cdn.example.com/second.m4a" type="audio/mp4"/>
    </item>
    <item>
      <title>Text Only</title>
    </item>
  </channel>
</rss>`

func TestParseFeedEntries(t *testing.T) {
	entries, err := ParseFeedEntries([]byte(ingestTestFeed))
	if err != nil {
		t.Fatalf("ParseFeedEntries returned error: %v", err)
	}
	// The text-only item has neither GUID nor enclosure: skipped.
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2: %+v", len(entries), entries)
	}
	if entries[0].GUID != "guid-first" || entries[0].DerivedGUID || entries[0].Title != "First Entry" ||
		entries[0].EnclosureURL != "https://cdn.example.com/first.mp3" ||
		entries[0].EnclosureType != "audio/mpeg" {
		t.Fatalf("entries[0] = %+v", entries[0])
	}
	if entries[1].GUID == "" || entries[1].GUID == entries[1].EnclosureURL ||
		!entries[1].DerivedGUID || entries[1].EnclosureType != "audio/mp4" {
		t.Fatalf("entries[1] = %+v", entries[1])
	}

	if _, err := ParseFeedEntries([]byte("not xml <<<")); err == nil {
		t.Fatal("ParseFeedEntries accepted invalid XML")
	}
}

func TestFetchConditional(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("If-None-Match") == `W/"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `W/"v1"`)
		w.Header().Set("Last-Modified", "Wed, 11 Jun 2026 12:00:00 GMT")
		_, _ = w.Write([]byte(ingestTestFeed))
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	first, err := engine.FetchConditional(context.Background(), server.URL, "", "")
	if err != nil {
		t.Fatalf("first fetch returned error: %v", err)
	}
	if first.NotModified || !bytes.Equal(first.Data, []byte(ingestTestFeed)) {
		t.Fatalf("first = %+v", first)
	}
	if first.ETag != `W/"v1"` || first.LastModified != "Wed, 11 Jun 2026 12:00:00 GMT" {
		t.Fatalf("first validators = %q %q", first.ETag, first.LastModified)
	}

	second, err := engine.FetchConditional(context.Background(), server.URL, first.ETag, first.LastModified)
	if err != nil {
		t.Fatalf("second fetch returned error: %v", err)
	}
	if !second.NotModified || second.Data != nil {
		t.Fatalf("second = %+v, want NotModified", second)
	}
	// The caller's validators echo back so bookkeeping stays stable.
	if second.ETag != first.ETag || second.LastModified != first.LastModified {
		t.Fatalf("second validators = %q %q", second.ETag, second.LastModified)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestFetchConditionalAndDownloadResourceSendAuthorizationHeader(t *testing.T) {
	var feedAuth, fileAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed.xml":
			feedAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(ingestTestFeed))
		case "/file.mp3":
			fileAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte("payload"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	if _, err := engine.FetchConditionalAuth(context.Background(), server.URL+"/feed.xml", "", "", "Bearer secret"); err != nil {
		t.Fatalf("FetchConditionalAuth returned error: %v", err)
	}
	var sink bytes.Buffer
	if _, err := engine.DownloadResourceAuth(context.Background(), server.URL+"/file.mp3", &sink, 100, "Bearer secret"); err != nil {
		t.Fatalf("DownloadResourceAuth returned error: %v", err)
	}
	if feedAuth != "Bearer secret" || fileAuth != "Bearer secret" {
		t.Fatalf("auth headers = feed %q file %q, want both", feedAuth, fileAuth)
	}
}

func TestFetchConditionalRefusesPlainHTTPWithoutLoopbackGate(t *testing.T) {
	engine := New()
	_, err := engine.FetchConditional(context.Background(), "http://feeds.example.com/secret?token=hush", "", "")
	if err == nil {
		t.Fatal("plain-http fetch succeeded, want refusal")
	}
	if strings.Contains(err.Error(), "hush") || strings.Contains(err.Error(), "token") {
		t.Fatalf("error echoes the URL credentials: %v", err)
	}
}

func TestDownloadResource(t *testing.T) {
	payload := bytes.Repeat([]byte("beamfall"), 1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	var sink bytes.Buffer
	written, err := engine.DownloadResource(context.Background(), server.URL, &sink, int64(len(payload)))
	if err != nil {
		t.Fatalf("DownloadResource returned error: %v", err)
	}
	if written != int64(len(payload)) || !bytes.Equal(sink.Bytes(), payload) {
		t.Fatalf("written = %d, payload mismatch", written)
	}

	// One byte under the body size: the budget refuses the download.
	sink.Reset()
	if _, err := engine.DownloadResource(context.Background(), server.URL, &sink, int64(len(payload))-1); err == nil {
		t.Fatal("over-budget download succeeded, want error")
	}
}

func TestParseFeedEntriesAtom(t *testing.T) {
	feed := []byte(`<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Sample Feed</title>
  <entry>
    <id>urn:uuid:entry-one</id>
    <title>First Entry</title>
    <link rel="alternate" href="https://example.test/one"/>
    <link rel="enclosure" href="https://media.example.test/one.mp3" type="audio/mpeg"/>
  </entry>
  <entry>
    <title>No Identity Or Enclosure</title>
  </entry>
  <entry>
    <title>Enclosure Only</title>
    <link rel="enclosure" href="https://media.example.test/two.mp3" type="audio/mpeg"/>
  </entry>
</feed>`)
	entries, err := ParseFeedEntries(feed)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %#v, want 2 (identity-less entry skipped)", entries)
	}
	if entries[0].GUID != "urn:uuid:entry-one" || entries[0].DerivedGUID || entries[0].Title != "First Entry" {
		t.Fatalf("entries[0] = %#v", entries[0])
	}
	if entries[0].EnclosureURL != "https://media.example.test/one.mp3" || entries[0].EnclosureType != "audio/mpeg" {
		t.Fatalf("entries[0] enclosure = %#v", entries[0])
	}
	if entries[1].GUID == "" || entries[1].GUID == entries[1].EnclosureURL || !entries[1].DerivedGUID {
		t.Fatalf("entries[1] = %#v", entries[1])
	}
}

func TestFeedEntriesKeepDerivedGUIDsWhenEnclosuresRotate(t *testing.T) {
	for _, tc := range []struct {
		name, feed, link, published, equivalent, later string
	}{
		{
			name: "RSS",
			feed: `<rss><channel><item><title>{title}</title>{link}<pubDate>{date}</pubDate>
				<enclosure url="{url}"/></item></channel></rss>`,
			link:       `<link>https://example.test/entry</link>`,
			published:  "Tue, 10 Nov 2026 12:00:00 GMT",
			equivalent: "Tue, 10 Nov 2026 07:00:00 -0500",
			later:      "Wed, 11 Nov 2026 12:00:00 GMT",
		},
		{
			name: "Atom",
			feed: `<feed><entry><title>{title}</title><published>{date}</published>
				<link rel="enclosure" href="{url}"/>{link}
				<link rel="self" href="https://example.test/ignore"/>
				<updated>{url}</updated></entry></feed>`,
			link:       `<link href="https://example.test/entry"/>`,
			published:  "2026-11-10T12:00:00Z",
			equivalent: "2026-11-10T07:00:00-05:00",
			later:      "2026-11-11T12:00:00Z",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parse := func(title, link, date, enclosure string) FeedEntry {
				t.Helper()
				raw := strings.NewReplacer("{title}", title, "{link}", link, "{date}", date, "{url}", enclosure).Replace(tc.feed)
				entries, err := ParseFeedEntries([]byte(raw))
				if err != nil || len(entries) != 1 {
					t.Fatalf("ParseFeedEntries = %v, %v, want one entry", entries, err)
				}
				entry := entries[0]
				if !entry.DerivedGUID || entry.GUID == "" || entry.GUID == enclosure || entry.EnclosureURL != enclosure {
					t.Fatalf("invalid derived identity or enclosure: %+v", entry)
				}
				return entry
			}
			const firstURL = "https://cdn.example.test/first.mp3"
			const nextURL = "https://cdn.example.test/replacement.mp3"
			fromLink := parse("First Title", tc.link, tc.published, firstURL)
			if rotated := parse("Edited Title", tc.link, tc.later, nextURL); rotated.GUID != fromLink.GUID {
				t.Errorf("same item link gave different GUIDs: %q and %q", fromLink.GUID, rotated.GUID)
			}
			fromMetadata := parse("First Title", "", tc.published, firstURL)
			if rotated := parse(" FIRST   title ", "", tc.equivalent, nextURL); rotated.GUID != fromMetadata.GUID {
				t.Errorf("equivalent metadata gave different GUIDs: %q and %q", fromMetadata.GUID, rotated.GUID)
			}
			if later := parse("First Title", "", tc.later, nextURL); later.GUID == fromMetadata.GUID {
				t.Errorf("different publication dates gave the same GUID: %q", later.GUID)
			}
		})
	}
}

// TestDownloadResourceStreamRedirects: enclosures bounce through measurement
// hosts, so an anonymous payload fetch follows a cross-origin redirect, while
// a credentialed one stays pinned to its origin (CORE-0745).
func TestDownloadResourceStreamRedirects(t *testing.T) {
	var cdnHits int
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cdnHits++
		_, _ = w.Write([]byte("payload"))
	}))
	defer cdn.Close()
	// Four measurement hops (more than maxRedirects) before the CDN.
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hop, _ := strconv.Atoi(r.URL.Query().Get("hop"))
		if hop < 4 {
			http.Redirect(w, r, fmt.Sprintf("/file.mp3?hop=%d", hop+1), http.StatusFound)
			return
		}
		http.Redirect(w, r, cdn.URL+"/file.mp3", http.StatusFound)
	}))
	defer tracker.Close()

	engine := New(WithInsecureLoopback())
	var sink bytes.Buffer
	if _, err := engine.DownloadResource(context.Background(), tracker.URL+"/file.mp3", &sink, 100); err != nil {
		t.Fatalf("anonymous cross-origin redirect: %v", err)
	}
	if sink.String() != "payload" || cdnHits != 1 {
		t.Fatalf("got %q after %d CDN hits", sink.String(), cdnHits)
	}
	sink.Reset()
	if _, err := engine.DownloadResourceAuth(context.Background(), tracker.URL+"/file.mp3", &sink, 100, "Bearer secret"); err == nil {
		t.Fatal("credentialed cross-origin redirect succeeded, want refusal")
	}
	if cdnHits != 1 {
		t.Fatalf("credentialed redirect reached the other origin (%d hits)", cdnHits)
	}
}
