package scrape

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMalformedFeedRedirectErrorOmitsLocation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://[broken/next?signature=redirect-secret")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	engine := New(WithInsecureLoopback())
	_, err := engine.FetchFeed(t.Context(), server.URL+"/feed?token=feed-secret", "", "", "")
	if err == nil {
		t.Fatal("malformed redirect succeeded")
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) || strings.Contains(err.Error(), "://") || strings.Contains(err.Error(), "secret") {
		t.Fatal("malformed redirect error exposed a URL")
	}
}

func TestParseFeedRequiresFeedRoot(t *testing.T) {
	for _, input := range []string{"<html><rss/></html>", "<html><body/></html>", "", "<rss/><html/>"} {
		if _, err := ParseFeed([]byte(input)); err == nil {
			t.Errorf("accepted non-feed document %q", input)
		}
	}
	for _, input := range []string{"<rss/>", "<feed xmlns=\"http://www.w3.org/2005/Atom\"/>", "<rdf:RDF xmlns:rdf=\"http://www.w3.org/1999/02/22-rdf-syntax-ns#\"/>"} {
		if _, err := ParseFeed([]byte(input)); err != nil {
			t.Errorf("rejected feed root %q: %v", input, err)
		}
	}
}

func TestFeedTemporaryCrossOriginRedirects(t *testing.T) {
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != "" {
			t.Error("cross-origin hop forwarded request credentials or referrer")
		}
		_, _ = w.Write([]byte("<rss/>"))
	}))
	defer target.Close()
	for _, code := range []int{http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL, code)
			}))
			defer source.Close()
			engine := New(WithInsecureLoopback())
			start := reached.Load()
			rawURL := source.URL + "/feed?token=signed"
			got, err := engine.FetchFeed(t.Context(), rawURL, "", "", "")
			if err != nil || string(got.Data) != "<rss/>" || got.PermanentURL != "" || !got.RedirectedTemporarily {
				t.Fatalf("anonymous redirect: body=%q permanent=%t err=%v", got.Data, got.PermanentURL != "", err)
			}
			for _, credential := range []struct{ url, header string }{
				{rawURL, "Bearer test"},
				{strings.Replace(rawURL, "://", "://user:pass@", 1), ""},
			} {
				_, err := engine.FetchFeed(t.Context(), credential.url, "", "", credential.header)
				if !IsRefused(err) {
					t.Fatalf("credentialed redirect: got %v, want refusal", err)
				}
			}
			if reached.Load() != start+1 {
				t.Fatal("credentialed request reached the cross-origin destination")
			}
		})
	}
}

func TestParseFeedChannelMetadataAndEntryFacts(t *testing.T) {
	const doc = `<?xml version="1.0"?>
<rss xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd" xmlns:p20="https://example.org/ns">
<channel>
  <title>Channel Title</title>
  <itunes:type>serial</itunes:type>
  <p20:newFeedUrl>https://new.example.com/feed.xml</p20:newFeedUrl>
  <image><title>Image Title</title></image>
  <item>
    <title>First</title><guid>g1</guid>
    <description>About first</description>
    <pubDate>Mon, 02 Jan 2006 15:04:05 -0700</pubDate>
    <itunes:duration>1:02:03</itunes:duration>
    <enclosure url="https://cdn.example.com/1.mp3" type="audio/mpeg" length="12345"/>
  </item>
  <item><title>Second</title><guid>g2</guid><itunes:duration>95</itunes:duration></item>
</channel></rss>`
	document, err := ParseFeed([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if document.Title != "Channel Title" || !document.Serial || document.NewFeedURL != "https://new.example.com/feed.xml" {
		t.Fatalf("channel = %q serial=%v new=%q", document.Title, document.Serial, document.NewFeedURL)
	}
	if len(document.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(document.Entries))
	}
	first := document.Entries[0]
	wantPublished := time.Date(2006, 1, 2, 22, 4, 5, 0, time.UTC)
	if first.Description != "About first" || !first.PublishedAt.Equal(wantPublished) || first.DurationSeconds != 3723 || first.EnclosureLength != 12345 {
		t.Fatalf("first entry = %+v", first)
	}
	if document.Entries[1].DurationSeconds != 95 {
		t.Fatalf("second duration = %d", document.Entries[1].DurationSeconds)
	}
	episodic, err := ParseFeed([]byte(`<rss><channel><title>T</title><itunes:type>episodic</itunes:type></channel></rss>`))
	if err != nil || episodic.Serial || episodic.NewFeedURL != "" {
		t.Fatalf("episodic = %+v, %v", episodic, err)
	}
}

func TestFetchFeedRecordsPermanentRedirects(t *testing.T) {
	t.Parallel()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<rss/>"))
	}))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/permanent":
			http.Redirect(w, r, "/moved", http.StatusMovedPermanently)
		case "/permanent-then-temporary":
			http.Redirect(w, r, "/moved-permanently", http.StatusPermanentRedirect)
		case "/moved-permanently":
			http.Redirect(w, r, "/moved", http.StatusFound)
		case "/temporary":
			http.Redirect(w, r, "/moved", http.StatusTemporaryRedirect)
		case "/cross-origin":
			http.Redirect(w, r, other.URL+"/elsewhere", http.StatusMovedPermanently)
		case "/gone":
			w.WriteHeader(http.StatusGone)
		default:
			_, _ = w.Write([]byte("<rss/>"))
		}
	}))
	defer server.Close()
	engine := New(WithInsecureLoopback())
	ctx := context.Background()

	result, err := engine.FetchFeed(ctx, server.URL+"/permanent", "", "", "")
	if err != nil || result.PermanentURL != server.URL+"/moved" || string(result.Data) != "<rss/>" {
		t.Fatalf("301 = %q %q %v", result.PermanentURL, result.Data, err)
	}
	result, err = engine.FetchFeed(ctx, server.URL+"/permanent-then-temporary", "", "", "")
	if err != nil || result.PermanentURL != server.URL+"/moved-permanently" || !result.RedirectedTemporarily {
		t.Fatalf("308 then 302 did not preserve the permanent prefix: %v", err)
	}
	result, err = engine.FetchFeed(ctx, server.URL+"/temporary", "", "", "")
	if err != nil || result.PermanentURL != "" {
		t.Fatalf("307 = %q %v", result.PermanentURL, err)
	}
	result, err = engine.FetchFeed(ctx, server.URL+"/cross-origin", "", "", "")
	if err != nil || result.PermanentURL != other.URL+"/elsewhere" {
		t.Fatalf("cross-origin 301 = %q %v, want the successfully fetched target", result.PermanentURL, err)
	}
	_, err = engine.FetchFeed(ctx, server.URL+"/gone", "", "", "")
	if code, ok := HTTPStatus(err); !ok || code != http.StatusGone || IsRefused(err) {
		t.Fatalf("HTTPStatus = %d %v (%v)", code, ok, err)
	}
}

func TestCheckURLAppliesPreDialRules(t *testing.T) {
	engine := New()
	for _, bad := range []string{"http://feeds.example.com/x", "https://10.0.0.1/x", "https:///x", "::bad"} {
		if err := engine.CheckURL(bad); !IsRefused(err) {
			t.Fatalf("CheckURL(%q) = %v, want refusal", bad, err)
		}
	}
	if err := engine.CheckURL("https://feeds.example.com/x"); err != nil {
		t.Fatalf("CheckURL(public) = %v", err)
	}
}
