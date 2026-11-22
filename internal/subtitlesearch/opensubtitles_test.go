package subtitlesearch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureEgress answers from recorded files and records every request. It is
// the only transport these tests use, so no test makes a live call.
type fixtureEgress struct {
	t        *testing.T
	routes   map[string]Response
	requests []Request
	ids      []string
}

func (f *fixtureEgress) Do(_ context.Context, providerID string, req Request) (Response, error) {
	f.requests = append(f.requests, req)
	f.ids = append(f.ids, providerID)
	key := req.Method + " " + strings.SplitN(req.URL, "?", 2)[0]
	resp, ok := f.routes[key]
	if !ok {
		return Response{}, fmt.Errorf("no fixture for %s", key)
	}
	return resp, nil
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name)) // #nosec G304 -- fixed testdata names.
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func staticKey(key string) KeySource { return func() (string, error) { return key, nil } }

func newFixtureProvider(t *testing.T) (*OpenSubtitles, *fixtureEgress) {
	egress := &fixtureEgress{t: t, routes: map[string]Response{
		"GET https://api.opensubtitles.com/api/v1/subtitles":                                              {StatusCode: 200, Body: fixture(t, "search.json")},
		"POST https://api.opensubtitles.com/api/v1/download":                                              {StatusCode: 200, Body: fixture(t, "download.json")},
		"GET https://www.opensubtitles.com/download/0123456789ABCDEF/subfile/Sample.Title.2019.1080p.srt": {StatusCode: 200, Body: fixture(t, "sample.srt")},
	}}
	return NewOpenSubtitles(egress, staticKey("test-key"), "Beamfall vtest"), egress
}

func TestSearchURLIsSortedAndGeneric(t *testing.T) {
	got, ok := SearchURL(Query{
		OSHash: "8E245D9679D31E12", Title: "Sample Title", Year: 2019,
		Sequence: 2, Index: 5, Languages: []string{"fr", "EN", "en", "<x>"},
	})
	if !ok {
		t.Fatal("expected a URL")
	}
	want := "https://api.opensubtitles.com/api/v1/subtitles?episode_number=5&languages=en%2Cfr&moviehash=8e245d9679d31e12&query=sample+title&season_number=2&year=2019"
	if got != want {
		t.Fatalf("SearchURL\n got %s\nwant %s", got, want)
	}
	if _, ok := SearchURL(Query{OSHash: "nothex"}); ok {
		t.Fatal("a query with no valid hash and no title must not build a URL")
	}
}

func TestSearchParsesFixture(t *testing.T) {
	provider, egress := newFixtureProvider(t)
	results, err := provider.Search(context.Background(), Query{OSHash: "8e245d9679d31e12", Title: "Sample Title"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results (bad language dropped), got %+v", results)
	}
	if results[0].FileID != "9001" || results[0].Language != "en" || results[0].Rating != 6.5 || results[0].Downloads != 120 || results[0].HashMatch {
		t.Fatalf("first result: %+v", results[0])
	}
	if r := results[1]; r.Language != "pt-br" || !r.SDH || !r.HashMatch || r.Release != "sample.ptbr.srt" {
		t.Fatalf("second result: %+v", r)
	}
	req := egress.requests[0]
	if req.Header["Api-Key"][0] != "test-key" || req.Header["User-Agent"][0] != "Beamfall vtest" {
		t.Fatalf("headers: %v", req.Header)
	}
	if egress.ids[0] != OpenSubtitlesID {
		t.Fatalf("policy ID %q", egress.ids[0])
	}
}

func TestDownloadFollowsLinkAndChecksSRT(t *testing.T) {
	provider, egress := newFixtureProvider(t)
	svc := NewService(provider)
	body, err := svc.Download(context.Background(), OpenSubtitlesID, "9001")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Hello there.") {
		t.Fatalf("body %q", body)
	}
	if len(egress.requests) != 2 || string(egress.requests[0].Body) != `{"file_id":9001,"sub_format":"srt"}` {
		t.Fatalf("requests: %+v", egress.requests)
	}
	if _, ok := egress.requests[1].Header["Api-Key"]; ok {
		t.Fatal("the file fetch must not carry the API key")
	}
}

func TestDownloadRefusesForeignLinkAndNonSubtitle(t *testing.T) {
	provider, egress := newFixtureProvider(t)
	egress.routes["POST https://api.opensubtitles.com/api/v1/download"] = Response{StatusCode: 200, Body: []byte(`{"link":"https://evil.example/x.srt"}`)}
	if _, err := provider.Download(context.Background(), "9001"); err == nil {
		t.Fatal("a link on an unknown origin must be refused")
	}
	if len(egress.requests) != 1 {
		t.Fatalf("no request may go to the foreign link: %+v", egress.requests)
	}

	provider, egress = newFixtureProvider(t)
	egress.routes["GET https://www.opensubtitles.com/download/0123456789ABCDEF/subfile/Sample.Title.2019.1080p.srt"] = Response{StatusCode: 200, Body: []byte("<html>quota</html>")}
	if _, err := NewService(provider).Download(context.Background(), OpenSubtitlesID, "9001"); !errors.Is(err, ErrNotSubtitle) {
		t.Fatalf("want ErrNotSubtitle, got %v", err)
	}
}

func TestNoKeyMakesNoRequest(t *testing.T) {
	egress := &fixtureEgress{t: t}
	missing := func() (string, error) { return "", fs.ErrNotExist }
	svc := NewService(NewOpenSubtitles(egress, missing, "Beamfall vtest"))
	if _, err := svc.Search(context.Background(), Query{Title: "x"}); !errors.Is(err, ErrNoKey) {
		t.Fatalf("want ErrNoKey, got %v", err)
	}
	if st := svc.Status(context.Background()); len(st) != 1 || st[0].Ready {
		t.Fatalf("status %+v", st)
	}
	if len(egress.requests) != 0 {
		t.Fatal("no key must mean no request")
	}
}

func TestDisabledServiceMakesNoRequest(t *testing.T) {
	var svc *Service
	if svc.Enabled() {
		t.Fatal("nil service is disabled")
	}
	if _, err := NewService().Search(context.Background(), Query{Title: "x"}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("want ErrDisabled, got %v", err)
	}
}

func TestServiceOrdersHashMatchesFirst(t *testing.T) {
	provider, _ := newFixtureProvider(t)
	results, err := NewService(provider).Search(context.Background(), Query{Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].HashMatch || results[0].FileID != "9002" {
		t.Fatalf("hash match must come first: %+v", results)
	}
}

func TestProviderErrorStatus(t *testing.T) {
	provider, egress := newFixtureProvider(t)
	egress.routes["GET https://api.opensubtitles.com/api/v1/subtitles"] = Response{StatusCode: http.StatusTooManyRequests}
	var perr *ProviderError
	if _, err := provider.Search(context.Background(), Query{Title: "x"}); !errors.As(err, &perr) || perr.Status != 429 {
		t.Fatalf("want ProviderError 429, got %v", err)
	}
}

func TestLooksLikeSRT(t *testing.T) {
	cases := map[string]bool{
		"1\n00:00:01,000 --> 00:00:02,000\nhi\n":         true,
		"\ufeff1\n0:00:01.000 --> 0:00:02.000 X:1\nhi\n": true,
		"WEBVTT\n\nnot srt":                              false,
		"":                                               false,
		"1\n00:00:01,000 --> 00:00:02,000\n\x00":         false,
	}
	for in, want := range cases {
		if got := LooksLikeSRT([]byte(in)); got != want {
			t.Errorf("LooksLikeSRT(%q) = %v, want %v", in, got, want)
		}
	}
}
