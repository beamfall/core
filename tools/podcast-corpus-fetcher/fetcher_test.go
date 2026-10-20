package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFetcherRefusesHTTPOptOutBeforeDownloadingAudio(t *testing.T) {
	var audioRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed":
			w.Header().Set("TDM-Reservation", "1")
			fmt.Fprint(w, feedXML(serverURL(r), "episode-1"))
		case "/audio":
			audioRequests++
			fmt.Fprint(w, "must-not-download")
		}
	}))
	defer server.Close()

	fetcher := testFetcher(server)
	_, err := fetcher.Fetch(context.Background(), testRequest(t, server.URL))
	if !errors.Is(err, ErrOptOut) {
		t.Fatalf("Fetch error = %v, want ErrOptOut", err)
	}
	if audioRequests != 0 {
		t.Fatalf("audio requests = %d, want 0", audioRequests)
	}
}

func TestFetcherRefusesRSSOptOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<?xml version="1.0"?><rss xmlns:tdm="https://www.w3.org/tdmrep/"><channel><tdm:reservation>1</tdm:reservation></channel></rss>`)
	}))
	defer server.Close()

	_, err := testFetcher(server).Fetch(context.Background(), testRequest(t, server.URL))
	if !errors.Is(err, ErrOptOut) {
		t.Fatalf("Fetch error = %v, want ErrOptOut", err)
	}
}

func TestFetcherPinsHashedSingleVantageSnapshot(t *testing.T) {
	var mu sync.Mutex
	var audioRequests int
	var remoteHosts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed":
			fmt.Fprint(w, feedXML(serverURL(r), "episode-1"))
		case "/audio":
			mu.Lock()
			audioRequests++
			ordinal := audioRequests
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				t.Errorf("SplitHostPort(%q): %v", r.RemoteAddr, err)
			}
			remoteHosts = append(remoteHosts, host)
			mu.Unlock()
			fmt.Fprintf(w, "variant-%d", ordinal)
		}
	}))
	defer server.Close()

	fetcher := testFetcher(server)
	base := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	var ticks int
	fetcher.now = func() time.Time {
		ticks++
		return base.Add(time.Duration(ticks) * time.Second)
	}
	var slept []time.Duration
	fetcher.sleep = func(_ context.Context, interval time.Duration) error {
		slept = append(slept, interval)
		return nil
	}

	req := testRequest(t, server.URL)
	result, err := fetcher.Fetch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(result.Path, 0o700) }) //nolint:gosec // cleanup needs directory write and execute permission
	if audioRequests != 2 {
		t.Fatalf("audio requests = %d, want exactly 2", audioRequests)
	}
	if !reflect.DeepEqual(slept, []time.Duration{req.Interval}) {
		t.Fatalf("sleep intervals = %v, want [%v]", slept, req.Interval)
	}
	if len(remoteHosts) != 2 || remoteHosts[0] != remoteHosts[1] {
		t.Fatalf("remote hosts = %v, want one network vantage", remoteHosts)
	}
	if filepath.Base(result.Path) != result.SnapshotSHA256 {
		t.Fatalf("snapshot path %q is not content-addressed by %q", result.Path, result.SnapshotSHA256)
	}

	manifestBytes, err := os.ReadFile(filepath.Join(result.Path, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	wantHash, err := verifyManifestHash(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SnapshotSHA256 != wantHash || result.SnapshotSHA256 != wantHash {
		t.Fatalf("snapshot hashes manifest=%q result=%q want=%q", manifest.SnapshotSHA256, result.SnapshotSHA256, wantHash)
	}
	if !manifest.SingleVantage || len(manifest.Items) != 3 {
		t.Fatalf("manifest single_vantage=%v items=%d", manifest.SingleVantage, len(manifest.Items))
	}
	for _, item := range manifest.Items {
		if len(item.SHA256) != 64 || item.SourceURL == "" || item.FinalURL == "" || item.FetchedAt == "" {
			t.Fatalf("incomplete provenance: %+v", item)
		}
	}
	if manifest.Items[1].SHA256 == manifest.Items[2].SHA256 {
		t.Fatal("time-separated variants unexpectedly have the same digest")
	}
	if err := os.WriteFile(filepath.Join(result.Path, "manifest.json"), []byte("mutate"), 0o600); err == nil {
		t.Fatal("immutable snapshot manifest accepted an overwrite")
	}
}

func TestFetcherRequiresPolicyPermitForEverySourceHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, feedXML("https://unreviewed.example", "episode-1"))
	}))
	defer server.Close()

	_, err := testFetcher(server).Fetch(context.Background(), testRequest(t, server.URL))
	if err == nil || !strings.Contains(err.Error(), "lacks an acknowledged platform-policy permit") {
		t.Fatalf("Fetch error = %v, want policy permit refusal", err)
	}
}

func TestSingleVantageHasNoRoutingOrRegionConfiguration(t *testing.T) {
	typ := reflect.TypeOf(Request{})
	banned := []string{"proxy", "sourceip", "geo", "region", "vpn", "rotate", "vantage"}
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		for _, fragment := range banned {
			if strings.Contains(name, fragment) {
				t.Fatalf("Request exposes forbidden routing control %q", typ.Field(i).Name)
			}
		}
	}
	transport, ok := NewFetcher().transport.(*http.Transport)
	if !ok {
		t.Fatalf("production transport type = %T, want *http.Transport", NewFetcher().transport)
	}
	if transport.Proxy != nil {
		t.Fatal("production transport permits environment-controlled routing")
	}
	mainBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	mainSource := strings.ToLower(string(mainBytes))
	for _, flagName := range []string{"proxy", "source-ip", "geo", "region", "vpn", "rotate", "vantage"} {
		if strings.Contains(mainSource, `"`+flagName+`"`) {
			t.Fatalf("CLI exposes forbidden routing flag %q", flagName)
		}
	}
}

func testFetcher(server *httptest.Server) *Fetcher {
	return &Fetcher{
		transport: server.Client().Transport,
		now:       time.Now,
		sleep: func(context.Context, time.Duration) error {
			return nil
		},
		maxFeed:  defaultMaxFeed,
		maxAudio: defaultMaxAudio,
	}
}

func testRequest(t *testing.T, serverURL string) Request {
	t.Helper()
	host := strings.TrimPrefix(serverURL, "http://")
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	return Request{
		FeedURL: serverURL + "/feed", EpisodeGUID: "episode-1", OutputRoot: t.TempDir(),
		Interval: time.Hour, AllowedHosts: []string{host}, PolicyAcknowledged: true,
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}

func feedXML(baseURL, guid string) string {
	return fmt.Sprintf(`<?xml version="1.0"?><rss><channel><title>Test</title><item><guid>%s</guid><title>Episode</title><enclosure url="%s/audio"/></item></channel></rss>`, guid, baseURL)
}
