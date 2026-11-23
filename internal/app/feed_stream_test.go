package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/ingest"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/proxytee"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestPrivateFeedNotDownloadedEntryStreamsThroughProxyTee is the CORE-3001
// integration test: a private fixture feed's not-downloaded entry plays through
// proxy-tee with the Subscription's secure-store auth header sent upstream;
// the header never appears in logs, DB rows or the stream token; and a
// cross-host enclosure receives no header.
func TestPrivateFeedNotDownloadedEntryStreamsThroughProxyTee(t *testing.T) {
	const (
		secretCanary = "core3001-super-secret-feed-credential"
		authHeader   = "Bearer " + secretCanary
		privateBody  = "PRIVATE-ENCLOSURE-AUDIO-BYTES-0123456789"
		crossBody    = "CROSS-HOST-ENCLOSURE-AUDIO-BYTES-9876543210"
	)

	var logs lockedBuffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	prevDefault := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prevDefault) })

	prevScrape := newScrapeEngine
	newScrapeEngine = func(opts ...scrape.Option) *scrape.Engine {
		return scrape.New(append([]scrape.Option{scrape.WithInsecureLoopback()}, opts...)...)
	}
	t.Cleanup(func() { newScrapeEngine = prevScrape })

	var (
		mu              sync.Mutex
		feedAuth        []string
		privateFileAuth []string
		crossFileAuth   []string
	)

	crossOrigin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		crossFileAuth = append(crossFileAuth, r.Header.Get("Authorization"))
		mu.Unlock()
		if r.URL.Path != "/files/cross.mp3" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		http.ServeContent(w, r, "cross.mp3", time.Unix(0, 0), strings.NewReader(crossBody))
	}))
	t.Cleanup(crossOrigin.Close)

	var feedOrigin *httptest.Server
	feedOrigin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth := r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/feed.xml":
			mu.Lock()
			feedAuth = append(feedAuth, gotAuth)
			mu.Unlock()
			if gotAuth != authHeader {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>Private Feed</title>`+
				`<item><title>Private Entry</title><guid>guid-private</guid><enclosure url="%s/files/private.mp3" type="audio/mpeg" length="%d"/></item>`+
				`<item><title>Cross Entry</title><guid>guid-cross</guid><enclosure url="%s/files/cross.mp3" type="audio/mpeg" length="%d"/></item>`+
				`<item><title>Failing Entry</title><guid>guid-fail</guid><enclosure url="%s/files/broken.mp3" type="audio/mpeg" length="16"/></item>`+
				`</channel></rss>`,
				feedOrigin.URL, len(privateBody), crossOrigin.URL, len(crossBody), feedOrigin.URL)
		case "/files/private.mp3":
			mu.Lock()
			privateFileAuth = append(privateFileAuth, gotAuth)
			mu.Unlock()
			if gotAuth != authHeader {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "audio/mpeg")
			http.ServeContent(w, r, "private.mp3", time.Unix(0, 0), strings.NewReader(privateBody))
		case "/files/broken.mp3":
			http.Error(w, "upstream failure", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(feedOrigin.Close)

	ctx := t.Context()
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(ctx, cfg, logger, nil)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	closed := false
	closeApp := func() {
		if !closed {
			closed = true
			if closeErr := beamfall.Close(); closeErr != nil {
				t.Errorf("beamfall.Close: %v", closeErr)
			}
		}
	}
	t.Cleanup(closeApp)

	authorizeManager := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-manager")
		req.Header.Set("X-Beamfall-Device", "device-mac")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")
	}

	subRoot := t.TempDir()
	addBody, err := json.Marshal(map[string]any{
		"query": `mutation($libraryID: String!, $feedURL: String!, $root: String!, $authHeader: String!) {
			addSubscription(libraryID: $libraryID, feedURL: $feedURL, root: $root, authHeader: $authHeader) { id hasAuth }
		}`,
		"variables": map[string]any{
			"libraryID":  "library-local",
			"feedURL":    feedOrigin.URL + "/feed.xml",
			"root":       subRoot,
			"authHeader": authHeader,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	addReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(addBody))
	addReq.Header.Set("Content-Type", "application/json")
	authorizeManager(addReq)
	addRec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(addRec, addReq)
	if addRec.Code != http.StatusOK {
		t.Fatalf("addSubscription HTTP %d: %s", addRec.Code, addRec.Body.String())
	}
	var added struct {
		Data struct {
			AddSubscription struct {
				ID      string `json:"id"`
				HasAuth bool   `json:"hasAuth"`
			} `json:"addSubscription"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(addRec.Body.Bytes(), &added); err != nil {
		t.Fatal(err)
	}
	if len(added.Errors) != 0 || !added.Data.AddSubscription.HasAuth || added.Data.AddSubscription.ID == "" {
		t.Fatalf("addSubscription = %s", addRec.Body.String())
	}
	subID := store.SubscriptionID(added.Data.AddSubscription.ID)

	if err := beamfall.store.SetSubscriptionDownloadPolicy(ctx, subID, store.SubscriptionPolicy{
		Policy: store.DownloadPolicyStreamOnly,
	}); err != nil {
		t.Fatalf("SetSubscriptionDownloadPolicy: %v", err)
	}
	pollJob, err := ingest.NewPollJob(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := beamfall.store.EnqueueJob(ctx, pollJob); err != nil {
		t.Fatalf("EnqueueJob(poll): %v", err)
	}
	if _, err := beamfall.RunPendingJobs(ctx); err != nil {
		t.Fatalf("RunPendingJobs(poll): %v", err)
	}

	entries, err := beamfall.store.SubscriptionEntries(ctx, subID)
	if err != nil || len(entries) != 3 {
		t.Fatalf("SubscriptionEntries = %+v, %v; want 3 entries", entries, err)
	}
	byGUID := map[string]store.SubscriptionEntry{}
	for _, entry := range entries {
		if entry.Downloaded {
			t.Fatalf("entry %q unexpectedly downloaded under stream-only policy", entry.GUID)
		}
		byGUID[entry.GUID] = entry
	}

	assertNoSecret := func(surface, text string) {
		t.Helper()
		if strings.Contains(text, secretCanary) || strings.Contains(text, authHeader) {
			t.Fatalf("%s leaked private-feed auth secret: %s", surface, text)
		}
	}

	playEntry := func(mediaID store.MediaID) (string, string) {
		t.Helper()
		infoPayload, err := json.Marshal(map[string]any{
			"mediaID": string(mediaID),
			"capabilityProfile": map[string]any{
				"client":       "core-3001-test",
				"containers":   []string{"mp3"},
				"directCodecs": []string{"mp3"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		infoReq := httptest.NewRequest(http.MethodPost, "/api/playback-info", bytes.NewReader(infoPayload))
		infoReq.Header.Set("Content-Type", "application/json")
		authorizeManager(infoReq)
		infoRec := httptest.NewRecorder()
		beamfall.Handler().ServeHTTP(infoRec, infoReq)
		if infoRec.Code != http.StatusOK {
			t.Fatalf("playback-info HTTP %d: %s", infoRec.Code, infoRec.Body.String())
		}
		assertNoSecret("playback-info response", infoRec.Body.String())
		var info playback.InfoResponse
		if err := json.Unmarshal(infoRec.Body.Bytes(), &info); err != nil {
			t.Fatal(err)
		}
		if info.Decision != "direct-play" || len(info.Streams) != 1 {
			t.Fatalf("playback-info for %s = decision %q reason %q streams %d, want direct-play proxy stream",
				mediaID, info.Decision, info.Reason, len(info.Streams))
		}
		stream := info.Streams[0]
		if stream.URL != "/api/media/proxy/"+stream.Token {
			t.Fatalf("stream URL = %q, want /api/media/proxy/<token>", stream.URL)
		}
		assertNoSecret("stream token", stream.Token)
		if parts := strings.Split(stream.Token, "."); len(parts) > 0 {
			if decoded, decErr := base64.RawURLEncoding.DecodeString(parts[0]); decErr == nil {
				assertNoSecret("decoded stream token grant", string(decoded))
			}
		}

		proxyReq := httptest.NewRequest(http.MethodGet, stream.URL, nil)
		proxyRec := httptest.NewRecorder()
		beamfall.Handler().ServeHTTP(proxyRec, proxyReq)
		if proxyRec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", stream.URL, proxyRec.Code, proxyRec.Body.String())
		}
		for name, values := range proxyRec.Header() {
			assertNoSecret("proxy response header "+name, strings.Join(values, "\n"))
		}
		return stream.Token, proxyRec.Body.String()
	}

	_, gotPrivate := playEntry(byGUID["guid-private"].MediaID)
	if gotPrivate != privateBody {
		t.Fatalf("private enclosure body = %q, want %q", gotPrivate, privateBody)
	}
	_, gotCross := playEntry(byGUID["guid-cross"].MediaID)
	if gotCross != crossBody {
		t.Fatalf("cross-host enclosure body = %q, want %q", gotCross, crossBody)
	}

	// Exercise the error log path (502 Bad Gateway) and verify it never logs the credential.
	failPayload, err := json.Marshal(map[string]any{
		"mediaID": string(byGUID["guid-fail"].MediaID),
		"capabilityProfile": map[string]any{
			"client":       "core-3001-test",
			"containers":   []string{"mp3"},
			"directCodecs": []string{"mp3"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	failInfoReq := httptest.NewRequest(http.MethodPost, "/api/playback-info", bytes.NewReader(failPayload))
	failInfoReq.Header.Set("Content-Type", "application/json")
	authorizeManager(failInfoReq)
	failInfoRec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(failInfoRec, failInfoReq)
	var failInfo playback.InfoResponse
	if err := json.Unmarshal(failInfoRec.Body.Bytes(), &failInfo); err != nil || len(failInfo.Streams) != 1 {
		t.Fatalf("failing entry playback-info = %s, %v", failInfoRec.Body.String(), err)
	}
	failProxyRec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(failProxyRec, httptest.NewRequest(http.MethodGet, failInfo.Streams[0].URL, nil))
	if failProxyRec.Code != http.StatusBadGateway {
		t.Fatalf("failing upstream status = %d, want 502", failProxyRec.Code)
	}
	assertNoSecret("502 proxy response", failProxyRec.Body.String())

	mu.Lock()
	gotFeedAuth := append([]string(nil), feedAuth...)
	gotPrivateAuth := append([]string(nil), privateFileAuth...)
	gotCrossAuth := append([]string(nil), crossFileAuth...)
	mu.Unlock()

	if len(gotFeedAuth) != 1 || gotFeedAuth[0] != authHeader {
		t.Fatalf("feed auth headers = %q, want [%q]", gotFeedAuth, authHeader)
	}
	if len(gotPrivateAuth) != 1 || gotPrivateAuth[0] != authHeader {
		t.Fatalf("same-host enclosure auth headers = %q, want [%q]", gotPrivateAuth, authHeader)
	}
	if len(gotCrossAuth) != 1 || gotCrossAuth[0] != "" {
		t.Fatalf("cross-host enclosure auth headers = %q, want [\"\"] (no header)", gotCrossAuth)
	}

	subRow, err := beamfall.store.Subscription(ctx, subID)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecret("subscription row", fmt.Sprintf("%+v", subRow))
	entriesAfter, err := beamfall.store.SubscriptionEntries(ctx, subID)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecret("subscription_entry rows", fmt.Sprintf("%+v", entriesAfter))

	closeApp()
	assertNoSecret("server logs", logs.String())

	wantSecretFile := filepath.Join(cfg.DataDir, "secrets", "subscription-auth-"+string(subID))
	foundSecretFile := false
	if err := filepath.WalkDir(cfg.DataDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		if path == wantSecretFile {
			foundSecretFile = true
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		assertNoSecret("persisted file "+path, string(raw))
		return nil
	}); err != nil {
		t.Fatalf("walk DataDir: %v", err)
	}
	if !foundSecretFile {
		t.Fatalf("expected secure-store secret file %s to exist", wantSecretFile)
	}
}

type recordingOrigin struct {
	lastAuth string
}

func (o *recordingOrigin) OpenOriginRange(_ context.Context, rawURL, rangeHeader string) (scrape.OriginResponse, error) {
	return o.OpenOriginRangeAuth(context.Background(), rawURL, rangeHeader, "")
}

func (o *recordingOrigin) OpenOriginRangeAuth(_ context.Context, _, _, authHeader string) (scrape.OriginResponse, error) {
	o.lastAuth = authHeader
	return scrape.OriginResponse{
		Body:        io.NopCloser(strings.NewReader("0123456789")),
		StatusCode:  http.StatusPartialContent,
		ContentType: "audio/mpeg",
		TotalSize:   100,
	}, nil
}

func TestFeedStreamRegistrarSameSiteScopingAndMissingSecret(t *testing.T) {
	ctx := t.Context()
	dataDir := storetest.MigratedDataDir(t)
	st := storetest.OpenAt(t, dataDir)
	authService := auth.NewService(st)
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	origin := &recordingOrigin{}
	tee, err := proxytee.New(st, authService, tokens, origin, filepath.Join(dataDir, "proxy-archive"))
	if err != nil {
		t.Fatalf("proxytee.New: %v", err)
	}
	t.Cleanup(func() { _ = tee.Close() })

	secrets := secretFiles{dir: filepath.Join(dataDir, "secrets"), dataDir: dataDir}
	registrar := feedStreamRegistrar{tee: tee, secrets: secrets}

	const secretHeader = "Bearer same-site-secret"
	sub1, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.test/private.xml",
		AuthRef:   "subscription-auth-sub-1",
		Root:      t.TempDir(),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	subMissing, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.test/missing-secret.xml",
		AuthRef:   "subscription-auth-missing",
		Root:      t.TempDir(),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := secrets.SetSecret("subscription-auth-sub-1", secretHeader); err != nil {
		t.Fatal(err)
	}

	scope, err := authService.Scope(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		key       string
		feedURL   string
		originURL string
		wantAuth  string
	}{
		{
			name:      "same-site different host sends header",
			key:       store.SubscriptionEntryStreamKey(sub1.ID, "guid-same-site"),
			feedURL:   "https://feeds.example.test/private.xml",
			originURL: "https://cdn.example.test/audio.mp3",
			wantAuth:  secretHeader,
		},
		{
			name:      "cross-site host gets no header",
			key:       store.SubscriptionEntryStreamKey(sub1.ID, "guid-cross-site"),
			feedURL:   "https://feeds.example.test/private.xml",
			originURL: "https://cdn.other.test/audio.mp3",
			wantAuth:  "",
		},
		{
			name:      "public suffix sibling gets no header",
			key:       store.SubscriptionEntryStreamKey(sub1.ID, "guid-psl"),
			feedURL:   "https://a.github.io/private.xml",
			originURL: "https://b.github.io/audio.mp3",
			wantAuth:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seededID := storetest.SeedFilelessGUIDMedia(t, dataDir, "library-local", tc.key, tc.name)
			mediaID, err := registrar.RegisterFeedStream(ctx, playback.FeedStream{
				LibraryID: "library-local",
				Key:       tc.key,
				Title:     tc.name,
				Container: "mp3",
				Codec:     "mp3",
				Class:     store.MediaClassAudio,
				OriginURL: tc.originURL,
				FeedURL:   tc.feedURL,
				AuthRef:   "subscription-auth-sub-1",
			})
			if err != nil || mediaID != seededID {
				t.Fatalf("RegisterFeedStream = %q, %v; want %q", mediaID, err, seededID)
			}
			token, err := tokens.Issue(ctx, streamtoken.Grant{
				Target:    streamtoken.TargetMediaProxy,
				MediaID:   mediaID,
				DeviceID:  scope.DeviceID,
				SessionID: scope.SessionID,
				ProfileID: scope.ProfileID,
				ExpiresAt: now.Add(time.Hour),
			})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+token, nil)
			req.Header.Set("Range", "bytes=0-9")
			rec := httptest.NewRecorder()
			tee.ServeProxy(rec, req)
			if rec.Code != http.StatusPartialContent {
				t.Fatalf("ServeProxy = %d: %s", rec.Code, rec.Body.String())
			}
			if origin.lastAuth != tc.wantAuth {
				t.Fatalf("upstream Authorization = %q, want %q", origin.lastAuth, tc.wantAuth)
			}
		})
	}

	if _, err := registrar.RegisterFeedStream(ctx, playback.FeedStream{
		LibraryID: "library-local",
		Key:       store.SubscriptionEntryStreamKey(subMissing.ID, "guid-1"),
		OriginURL: "https://feeds.example.test/audio.mp3",
		FeedURL:   "https://feeds.example.test/private.xml",
		AuthRef:   "subscription-auth-missing",
	}); err == nil {
		t.Fatal("RegisterFeedStream succeeded with missing secret ref on same-site origin, want error")
	}

	// A missing or unreadable secret must not block an anonymous cross-site fetch.
	crossMissingKey := store.SubscriptionEntryStreamKey(subMissing.ID, "guid-cross")
	crossMissingID := storetest.SeedFilelessGUIDMedia(t, dataDir, "library-local", crossMissingKey, "Cross Missing Secret")
	origin.lastAuth = "stale"
	gotCrossID, err := registrar.RegisterFeedStream(ctx, playback.FeedStream{
		LibraryID: "library-local",
		Key:       crossMissingKey,
		Title:     "Cross Missing Secret",
		Container: "mp3",
		Codec:     "mp3",
		Class:     store.MediaClassAudio,
		OriginURL: "https://cdn.other.test/audio.mp3",
		FeedURL:   "https://feeds.example.test/private.xml",
		AuthRef:   "subscription-auth-missing",
	})
	if err != nil || gotCrossID != crossMissingID {
		t.Fatalf("RegisterFeedStream(cross-site, missing secret) = %q, %v; want %q, nil", gotCrossID, err, crossMissingID)
	}
	token, err := tokens.Issue(ctx, streamtoken.Grant{
		Target:    streamtoken.TargetMediaProxy,
		MediaID:   gotCrossID,
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+token, nil)
	req.Header.Set("Range", "bytes=0-9")
	rec := httptest.NewRecorder()
	tee.ServeProxy(rec, req)
	if rec.Code != http.StatusPartialContent || origin.lastAuth != "" {
		t.Fatalf("cross-site proxy with missing secret: status=%d auth=%q, want 206 with empty auth", rec.Code, origin.lastAuth)
	}
}

func TestRemovedSubscriptionRevokesUnexpiredProxyTokenAuth(t *testing.T) {
	const (
		authHeader  = "Bearer core3001-removal-secret"
		privateBody = "PRIVATE-ENCLOSURE-BYTES-0123456789ABCDEF"
	)

	prevScrape := newScrapeEngine
	newScrapeEngine = func(opts ...scrape.Option) *scrape.Engine {
		return scrape.New(append([]scrape.Option{scrape.WithInsecureLoopback()}, opts...)...)
	}
	t.Cleanup(func() { newScrapeEngine = prevScrape })

	var (
		mu             sync.Mutex
		enclosureAuths []string
	)
	var feedOrigin *httptest.Server
	feedOrigin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth := r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/feed.xml":
			if gotAuth != authHeader {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>Private</title>`+
				`<item><title>Entry 1</title><guid>guid-1</guid><enclosure url="%s/files/1.mp3" type="audio/mpeg" length="%d"/></item>`+
				`</channel></rss>`, feedOrigin.URL, len(privateBody))
		case "/files/1.mp3":
			mu.Lock()
			enclosureAuths = append(enclosureAuths, gotAuth)
			mu.Unlock()
			if gotAuth != authHeader {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "audio/mpeg")
			http.ServeContent(w, r, "1.mp3", time.Unix(0, 0), strings.NewReader(privateBody))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(feedOrigin.Close)

	ctx := t.Context()
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(ctx, cfg, slog.New(slog.DiscardHandler), nil)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = beamfall.Close() })

	authorizeManager := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-manager")
		req.Header.Set("X-Beamfall-Device", "device-mac")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")
	}

	addBody, _ := json.Marshal(map[string]any{
		"query": `mutation($libraryID: String!, $feedURL: String!, $root: String!, $authHeader: String!) {
			addSubscription(libraryID: $libraryID, feedURL: $feedURL, root: $root, authHeader: $authHeader) { id }
		}`,
		"variables": map[string]any{
			"libraryID":  "library-local",
			"feedURL":    feedOrigin.URL + "/feed.xml",
			"root":       t.TempDir(),
			"authHeader": authHeader,
		},
	})
	addReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(addBody))
	addReq.Header.Set("Content-Type", "application/json")
	authorizeManager(addReq)
	addRec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(addRec, addReq)
	if addRec.Code != http.StatusOK {
		t.Fatalf("addSubscription HTTP %d: %s", addRec.Code, addRec.Body.String())
	}
	var added struct {
		Data struct {
			AddSubscription struct {
				ID string `json:"id"`
			} `json:"addSubscription"`
		} `json:"data"`
	}
	if err := json.Unmarshal(addRec.Body.Bytes(), &added); err != nil || added.Data.AddSubscription.ID == "" {
		t.Fatalf("addSubscription = %s, %v", addRec.Body.String(), err)
	}
	subID := store.SubscriptionID(added.Data.AddSubscription.ID)

	if err := beamfall.store.SetSubscriptionDownloadPolicy(ctx, subID, store.SubscriptionPolicy{
		Policy: store.DownloadPolicyStreamOnly,
	}); err != nil {
		t.Fatal(err)
	}
	pollJob, err := ingest.NewPollJob(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := beamfall.store.EnqueueJob(ctx, pollJob); err != nil {
		t.Fatal(err)
	}
	if _, err := beamfall.RunPendingJobs(ctx); err != nil {
		t.Fatal(err)
	}
	entries, err := beamfall.store.SubscriptionEntries(ctx, subID)
	if err != nil || len(entries) != 1 {
		t.Fatalf("SubscriptionEntries = %+v, %v", entries, err)
	}

	infoPayload, _ := json.Marshal(map[string]any{
		"mediaID": string(entries[0].MediaID),
		"capabilityProfile": map[string]any{
			"client":       "core-3001-test",
			"containers":   []string{"mp3"},
			"directCodecs": []string{"mp3"},
		},
	})
	infoReq := httptest.NewRequest(http.MethodPost, "/api/playback-info", bytes.NewReader(infoPayload))
	infoReq.Header.Set("Content-Type", "application/json")
	authorizeManager(infoReq)
	infoRec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(infoRec, infoReq)
	var info playback.InfoResponse
	if err := json.Unmarshal(infoRec.Body.Bytes(), &info); err != nil || len(info.Streams) != 1 {
		t.Fatalf("playback-info = %s, %v", infoRec.Body.String(), err)
	}
	proxyURL := info.Streams[0].URL

	// Initial range request succeeds with the private feed's Authorization header.
	rangeReq := httptest.NewRequest(http.MethodGet, proxyURL, nil)
	rangeReq.Header.Set("Range", "bytes=0-9")
	rangeRec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(rangeRec, rangeReq)
	if rangeRec.Code != http.StatusPartialContent {
		t.Fatalf("initial range GET = %d: %s", rangeRec.Code, rangeRec.Body.String())
	}

	// Remove the subscription.
	rmBody, _ := json.Marshal(map[string]any{
		"query":     `mutation($id: String!) { removeSubscription(id: $id) { id } }`,
		"variables": map[string]any{"id": string(subID)},
	})
	rmReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(rmBody))
	rmReq.Header.Set("Content-Type", "application/json")
	authorizeManager(rmReq)
	rmRec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(rmRec, rmReq)
	if rmRec.Code != http.StatusOK {
		t.Fatalf("removeSubscription HTTP %d: %s", rmRec.Code, rmRec.Body.String())
	}

	// Reusing the unexpired proxy token must not send the cached Authorization header.
	afterReq := httptest.NewRequest(http.MethodGet, proxyURL, nil)
	afterReq.Header.Set("Range", "bytes=10-19")
	afterRec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(afterRec, afterReq)
	if afterRec.Code != http.StatusNotFound {
		t.Fatalf("range GET after removeSubscription = %d, want 404", afterRec.Code)
	}

	mu.Lock()
	gotAuths := append([]string(nil), enclosureAuths...)
	mu.Unlock()
	if len(gotAuths) != 1 {
		t.Fatalf("enclosure requests = %d (%q), want 1 (only before removal)", len(gotAuths), gotAuths)
	}
}
