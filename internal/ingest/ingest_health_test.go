package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/scan"
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
			rows := stored
			if tc.policy.Policy == store.DownloadPolicyLatest {
				rows = nil // A first catalogue timestamps its entries at AddedAt.
			}
			got := selectByPolicy(tc.policy, true, tc.serial, added, policyEntries(), rows)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("selected %v, want %v", got, tc.want)
			}
		})
	}
	t.Run("interrupted initial catalogue", func(t *testing.T) {
		got := selectByPolicy(latest, true, false, added, policyEntries(), stored)
		want := []string{"b", "d", "a", "e"}
		if !slices.Equal(got, want) {
			t.Fatalf("selected %v, want later arrivals plus the initial limit %v", got, want)
		}
	})
}

func TestLatestDownloadsEveryArrivalAfterFirstPoll(t *testing.T) {
	fs := newFeedServer(t)
	st, sub, poller := healthFixture(t, fs.server.URL+"/feed.xml")
	ctx := t.Context()
	if err := st.SetSubscriptionDownloadPolicy(ctx, sub.ID, store.SubscriptionPolicy{Policy: store.DownloadPolicyLatest, LatestN: 1}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.mp3", "two.mp3", "three.mp3", "four.mp3"} {
		fs.enclosures[name] = []byte(name)
	}
	initial := fs.item("g1", "One", "one.mp3") + fs.item("g2", "Two", "two.mp3")
	fs.setFeed(initial)
	// Persist the initial selection and simulate a restart before any download.
	document, err := scrape.ParseFeed([]byte(fs.feed))
	if err != nil {
		t.Fatal(err)
	}
	selected, catalogued, err := poller.catalogue(ctx, sub, document, poller.now())
	if err != nil || !catalogued || len(selected) != 1 || !selected["g1"] {
		t.Fatalf("initial selection = %v, catalogued = %v, err = %v", selected, catalogued, err)
	}
	fs.setFeed(fs.item("g2", "Two", "two.mp3") + fs.item("g1", "One", "one.mp3"))
	sub, err = st.Subscription(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := poller.pollSubscription(ctx, sub); err != nil || count != 1 || fs.hits("one.mp3") != 1 {
		t.Fatalf("first poll count = %d, %v", count, err)
	}

	// Place both new entries below the old ones so top-N selection would miss them.
	fs.setFeed(initial + fs.item("g3", "Three", "three.mp3") + fs.item("g4", "Four", "four.mp3"))
	advancePollClock(poller)
	sub, err = st.Subscription(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := poller.pollSubscription(ctx, sub); err != nil || count != 2 {
		t.Fatalf("second poll count = %d, %v; want both arrivals", count, err)
	}
	if fs.hits("two.mp3") != 0 || fs.hits("three.mp3") != 1 || fs.hits("four.mp3") != 1 {
		t.Fatal("later poll did not restrict downloads to the two arrivals")
	}
}

func TestLatestLimitsFirstCatalogueAfterPreviousPoll(t *testing.T) {
	fs := newFeedServer(t)
	st, sub, poller := healthFixture(t, fs.server.URL+"/feed.xml")
	ctx := t.Context()
	now := poller.now()
	if err := st.RecordSubscriptionPollSuccess(ctx, sub, "", "", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSubscriptionDownloadPolicy(ctx, sub.ID, store.SubscriptionPolicy{Policy: store.DownloadPolicyLatest, LatestN: 1}); err != nil {
		t.Fatal(err)
	}
	sub = reloadSubscription(t, st, sub.ID)
	entries, err := st.SubscriptionEntries(ctx, sub.ID)
	if err != nil || len(entries) != 0 || sub.LastPolledAt.IsZero() {
		t.Fatalf("expected a previously polled feed without catalogue entries: %v", err)
	}
	for _, name := range []string{"one.mp3", "two.mp3", "three.mp3"} {
		fs.enclosures[name] = []byte(name)
	}
	initial := fs.item("g1", "One", "one.mp3") + fs.item("g2", "Two", "two.mp3")
	fs.setFeed(initial)
	advancePollClock(poller)
	if count, err := poller.pollSubscription(ctx, sub); err != nil || count != 1 {
		t.Fatalf("backfill downloaded %d entries, want one: %v", count, err)
	}
	entries, err = st.SubscriptionEntries(ctx, sub.ID)
	if err != nil || len(entries) != 2 {
		t.Fatalf("backfill catalogued %d entries, want two: %v", len(entries), err)
	}
	fs.setFeed(initial + fs.item("g3", "Three", "three.mp3"))
	advancePollClock(poller)
	if count, err := poller.pollSubscription(ctx, reloadSubscription(t, st, sub.ID)); err != nil || count != 1 {
		t.Fatalf("next poll downloaded %d entries, want one: %v", count, err)
	}
	if fs.hits("one.mp3") != 1 || fs.hits("two.mp3") != 0 || fs.hits("three.mp3") != 1 {
		t.Fatal("latest did not download just the top initial entry and the later arrival")
	}
}

type ingestEncryptedDBCipher struct{}

func (ingestEncryptedDBCipher) UnsealDBFile(_ context.Context, _ store.LibraryID, ciphertextPath, workingPath string) error {
	src, err := os.Open(ciphertextPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer src.Close()
	if err := os.MkdirAll(filepath.Dir(workingPath), 0o700); err != nil {
		return err
	}
	dst, err := os.OpenFile(workingPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func (ingestEncryptedDBCipher) SealDBFile(_ context.Context, _ store.LibraryID, workingPath, ciphertextPath string) error {
	src, err := os.Open(workingPath)
	if err != nil {
		return err
	}
	defer src.Close()
	if err := os.MkdirAll(filepath.Dir(ciphertextPath), 0o700); err != nil {
		return err
	}
	dst, err := os.OpenFile(ciphertextPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Remove(workingPath)
}

func (ingestEncryptedDBCipher) EncryptObject(libraryID store.LibraryID, src io.Reader, ciphertextPath, objectClass, immutableObjectID string) error {
	if objectClass != "media-bytes" {
		return fmt.Errorf("unexpected object class %q", objectClass)
	}
	plain, err := io.ReadAll(src)
	if err != nil {
		return err
	}
	cipher := make([]byte, len(plain))
	for i, b := range plain {
		cipher[i] = b ^ 0xA5
	}
	header := fmt.Sprintf("ENC|%s|%s|", libraryID, immutableObjectID)
	return os.WriteFile(ciphertextPath, append([]byte(header), cipher...), 0o600)
}

func TestEncryptedFeedPollAndDownloadsRouteWhenUnsealedAndRefuseWhenSealed(t *testing.T) {
	// CORE-0706 / CORE-3511: an unsealed Encrypted Library's feed poll and
	// enclosure downloads encrypt directly at rest on the owning plane (never
	// plaintext on disk and never leaking roots into events-DB scan jobs);
	// missing encryptor or sealed state fails closed before download/fetch.
	for _, policy := range []store.DownloadPolicy{
		store.DownloadPolicyStreamOnly, store.DownloadPolicyNewOnly,
		store.DownloadPolicyLatest, store.DownloadPolicyEverything,
	} {
		t.Run(string(policy), func(t *testing.T) {
			ctx := t.Context()
			fs := newFeedServer(t)
			st, err := store.Open(ctx, storetest.MigratedDataDir(t), store.Options{
				SeedDevelopmentData:               true,
				EnableEncryptedLibraryDBLifecycle: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = st.Close() }()
			lib, err := st.CreateLibrary(ctx, "Encrypted Feeds", false, "", "profile-default")
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SetLibraryBacking(ctx, lib.ID, true, true); err != nil {
				t.Fatal(err)
			}
			if err := st.UnsealEncryptedLibraryDB(ctx, lib.ID, ingestEncryptedDBCipher{}); err != nil {
				t.Fatal(err)
			}
			subRoot := t.TempDir()
			sub, err := st.AddSubscription(ctx, store.Subscription{
				LibraryID: lib.ID,
				FeedURL:   fs.server.URL + "/feed.xml",
				Root:      subRoot,
			}, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SetSubscriptionDownloadPolicy(ctx, sub.ID, store.SubscriptionPolicy{Policy: policy, LatestN: 1}); err != nil {
				t.Fatal(err)
			}
			poller := NewPoller(st, scrape.New(scrape.WithInsecureLoopback()))
			poller.jitter = func() float64 { return 0.5 }
			poller.SetEncryptedIngest(ingestEncryptedDBCipher{})
			fs.enclosures["one.mp3"] = []byte("one.mp3")
			fs.enclosures["two.mp3"] = []byte("two.mp3")
			fs.setFeed(fs.item("g1", "One", "one.mp3"))

			wantCount := 1
			if policy == store.DownloadPolicyStreamOnly || policy == store.DownloadPolicyNewOnly {
				wantCount = 0
			}
			if count, err := poller.pollSubscription(ctx, sub); err != nil || count != wantCount {
				t.Fatalf("unsealed encrypted poll (%s) = %d, %v; want %d, nil", policy, count, err, wantCount)
			}
			entries, err := st.SubscriptionEntries(ctx, sub.ID)
			if err != nil || len(entries) != 1 || entries[0].GUID != "g1" {
				t.Fatalf("entries after first poll = %+v, %v", entries, err)
			}
			checkDownloadedCiphertext := func(guid string, plain []byte) {
				t.Helper()
				seenEntries, err := st.SubscriptionEntries(ctx, sub.ID)
				if err != nil {
					t.Fatal(err)
				}
				var matched store.SubscriptionEntry
				for _, e := range seenEntries {
					if e.GUID == guid {
						matched = e
					}
				}
				if matched.DownloadState != store.EntryDownloadDownloaded || matched.MediaID == "" || matched.DownloadPath == "" {
					t.Fatalf("entry %q not imported cleanly: %+v", guid, matched)
				}
				onDisk, err := os.ReadFile(matched.DownloadPath)
				if err != nil {
					t.Fatalf("ReadFile(%s): %v", matched.DownloadPath, err)
				}
				if bytes.Contains(onDisk, plain) || !bytes.HasPrefix(onDisk, []byte("ENC|"+string(lib.ID)+"|sha256:")) {
					t.Fatalf("enclosure on disk was not encrypted ciphertext: %q", onDisk)
				}
				sum := sha256.Sum256(plain)
				wantFP := "sha256:" + hex.EncodeToString(sum[:])
				files, err := st.MediaFilesForMedia(ctx, matched.MediaID, store.LibraryFilter{Revealed: true, AllLibraries: true})
				if err != nil || len(files) != 1 || files[0].ContentFingerprint != wantFP {
					t.Fatalf("imported files = %+v, %v; want fingerprint %s", files, err, wantFP)
				}
			}
			if wantCount == 1 {
				checkDownloadedCiphertext("g1", fs.enclosures["one.mp3"])
			}
			if policy == store.DownloadPolicyNewOnly {
				fs.setFeed(fs.item("g2", "Two", "two.mp3") + fs.item("g1", "One", "one.mp3"))
				if count, err := poller.pollSubscription(ctx, reloadSubscription(t, st, sub.ID)); err != nil || count != 1 {
					t.Fatalf("second new-only encrypted poll = %d, %v; want 1, nil", count, err)
				}
				if fs.hits("two.mp3") != 1 {
					t.Fatalf("two.mp3 hits = %d, want 1", fs.hits("two.mp3"))
				}
				checkDownloadedCiphertext("g2", fs.enclosures["two.mp3"])
			}
			queued, err := st.ListJobs(ctx, store.JobStatusQueued)
			if err != nil {
				t.Fatal(err)
			}
			for _, j := range queued {
				if j.Kind == scan.JobKind {
					t.Fatalf("encrypted subscription enqueued shared scan job %+v", j)
				}
			}

			// Sealing drops access and stops subsequent polls before any network fetch.
			fs.mu.Lock()
			hitsBeforeSeal := fs.feedHits
			fs.mu.Unlock()
			if err := st.SealEncryptedLibraryDB(ctx, lib.ID, ingestEncryptedDBCipher{}); err != nil {
				t.Fatal(err)
			}
			fs.setFeed(fs.item("g3", "Three", "two.mp3"))
			if count, err := poller.pollSubscription(ctx, sub); err == nil || count != 0 {
				t.Fatalf("sealed encrypted poll = %d, %v; want 0, error", count, err)
			}
			fs.mu.Lock()
			hitsAfterSeal := fs.feedHits
			fs.mu.Unlock()
			if hitsAfterSeal != hitsBeforeSeal {
				t.Fatalf("sealed poll hit feed server (%d -> %d)", hitsBeforeSeal, hitsAfterSeal)
			}
		})
	}

	t.Run("missing-encryptor-refuses", func(t *testing.T) {
		ctx := t.Context()
		fs := newFeedServer(t)
		st, err := store.Open(ctx, storetest.MigratedDataDir(t), store.Options{
			SeedDevelopmentData:               true,
			EnableEncryptedLibraryDBLifecycle: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = st.Close() }()
		lib, err := st.CreateLibrary(ctx, "Encrypted Missing Ingestor", false, "", "profile-default")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetLibraryBacking(ctx, lib.ID, true, true); err != nil {
			t.Fatal(err)
		}
		if err := st.UnsealEncryptedLibraryDB(ctx, lib.ID, ingestEncryptedDBCipher{}); err != nil {
			t.Fatal(err)
		}
		subRoot := t.TempDir()
		sub, err := st.AddSubscription(ctx, store.Subscription{
			LibraryID: lib.ID,
			FeedURL:   fs.server.URL + "/feed.xml",
			Root:      subRoot,
		}, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetSubscriptionDownloadPolicy(ctx, sub.ID, store.SubscriptionPolicy{Policy: store.DownloadPolicyEverything}); err != nil {
			t.Fatal(err)
		}
		fs.enclosures["one.mp3"] = []byte("one.mp3")
		fs.setFeed(fs.item("g1", "One", "one.mp3"))

		unwired := NewPoller(st, scrape.New(scrape.WithInsecureLoopback()))
		unwired.jitter = func() float64 { return 0.5 }
		if _, err := unwired.pollSubscription(ctx, sub); err == nil {
			t.Fatal("encrypted download without encryptor succeeded unexpectedly")
		}
		if fs.hits("one.mp3") != 0 {
			t.Fatalf("missing encryptor fetched enclosure (%d hits)", fs.hits("one.mp3"))
		}
		if entries, err := os.ReadDir(subRoot); err != nil || len(entries) != 0 {
			t.Fatalf("subRoot entries after refused download = %v, %v; want empty", entries, err)
		}
	})

	t.Run("import-failure-stays-retryable", func(t *testing.T) {
		ctx := t.Context()
		fs := newFeedServer(t)
		st, err := store.Open(ctx, storetest.MigratedDataDir(t), store.Options{
			SeedDevelopmentData:               true,
			EnableEncryptedLibraryDBLifecycle: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = st.Close() }()
		lib, err := st.CreateLibrary(ctx, "Encrypted Retryable", false, "", "profile-default")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetLibraryBacking(ctx, lib.ID, true, true); err != nil {
			t.Fatal(err)
		}
		if err := st.UnsealEncryptedLibraryDB(ctx, lib.ID, ingestEncryptedDBCipher{}); err != nil {
			t.Fatal(err)
		}
		subRoot := t.TempDir()
		sub, err := st.AddSubscription(ctx, store.Subscription{
			LibraryID: lib.ID,
			FeedURL:   fs.server.URL + "/feed.xml",
			Root:      subRoot,
		}, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetSubscriptionDownloadPolicy(ctx, sub.ID, store.SubscriptionPolicy{Policy: store.DownloadPolicyEverything}); err != nil {
			t.Fatal(err)
		}
		fs.enclosures["one.mp3"] = []byte("one.mp3")
		fs.setFeed(fs.item("g1", "One", "one.mp3"))

		poller := NewPoller(st, scrape.New(scrape.WithInsecureLoopback()))
		poller.jitter = func() float64 { return 0.5 }
		poller.SetEncryptedIngest(ingestEncryptedDBCipher{})
		poller.fault = func(point string) error {
			if point == "encrypted-before-import" {
				return errors.New("injected import fault")
			}
			return nil
		}
		if count, err := poller.pollSubscription(ctx, sub); err == nil || count != 0 {
			t.Fatalf("first poll with import fault = %d, %v; want 0, error", count, err)
		}
		if entries, err := os.ReadDir(subRoot); err != nil || len(entries) != 0 {
			t.Fatalf("subRoot entries after failed import = %v, %v; want empty", entries, err)
		}
		entries, err := st.SubscriptionEntries(ctx, sub.ID)
		if err != nil || len(entries) != 1 || entries[0].DownloadState != store.EntryDownloadFailed {
			t.Fatalf("entry after failed import = %+v, %v; want EntryDownloadFailed", entries, err)
		}

		poller.fault = nil
		if count, err := poller.pollSubscription(ctx, reloadSubscription(t, st, sub.ID)); err != nil || count != 1 {
			t.Fatalf("retry poll after clearing fault = %d, %v; want 1, nil", count, err)
		}
		entries, err = st.SubscriptionEntries(ctx, sub.ID)
		if err != nil || len(entries) != 1 || entries[0].DownloadState != store.EntryDownloadDownloaded {
			t.Fatalf("entry after retry poll = %+v, %v; want EntryDownloadDownloaded", entries, err)
		}
	})
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
		{status(http.StatusGone), store.FeedErrorHTTP4xx},
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
	mux.HandleFunc("/feed.xml", func(w http.ResponseWriter, r *http.Request) {
		sf.mu.Lock()
		code, body := sf.code, sf.body
		sf.mu.Unlock()
		if code == http.StatusOK {
			const etag = `"feed-v1"`
			w.Header().Set("ETag", etag)
			if r.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/permanent.xml", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/feed.xml", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/temporary.xml", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/feed.xml", http.StatusFound)
	})
	mux.HandleFunc("/permanent-prefix.xml", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/temporary.xml", http.StatusPermanentRedirect)
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

func TestPollMovesToPermanentPrefixAfterTwoObservations(t *testing.T) {
	sf := newStatusFeed(t)
	sf.set(http.StatusOK, `<rss><channel><title>Feed</title></channel></rss>`)
	st, sub, poller := healthFixture(t, sf.server.URL+"/permanent-prefix.xml")
	for _, want := range []string{sub.FeedURL, sf.server.URL + "/temporary.xml"} {
		if _, err := poller.pollSubscription(t.Context(), sub); err != nil {
			t.Fatal(err)
		}
		sub = reloadSubscription(t, st, sub.ID)
		if sub.FeedURL != want {
			t.Fatal("poll did not honor the two-observation move to the permanent prefix")
		}
	}
}

func TestPollBacksOffTemporaryDestinationGoneOrMissing(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		status     int
		retire     bool
	}{
		{"temporary gone", "/permanent-prefix.xml", http.StatusGone, false},
		{"temporary missing", "/temporary.xml", http.StatusNotFound, false},
		{"permanent gone", "/permanent.xml", http.StatusGone, true},
		{"direct gone", "/feed.xml", http.StatusGone, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sf := newStatusFeed(t)
			sf.set(tc.status, "")
			st, sub, poller := healthFixture(t, sf.server.URL+tc.path)
			now := time.Now().UTC().Truncate(time.Millisecond)
			poller.now = func() time.Time { return now }
			_, err := poller.pollSubscription(t.Context(), sub)
			if status, ok := scrape.HTTPStatus(err); !ok || status != tc.status || scrape.IsTransient(err) == tc.retire {
				t.Fatalf("status/transient classification = %d/%v", status, scrape.IsTransient(err))
			}
			health := reloadSubscription(t, st, sub.ID).Health
			if tc.retire {
				if health.RetiredAt.IsZero() {
					t.Fatal("canonical 410 did not retire the feed")
				}
			} else if !health.RetiredAt.IsZero() || health.LastErrorClass != store.FeedErrorHTTP4xx ||
				health.ConsecutiveFailures != 1 || !health.NextPollAt.Equal(now.Add(failureBackoffBase)) {
				t.Fatalf("temporary destination did not back off: %+v", health)
			}
		})
	}
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

func TestPollRejectsMalformedFeedDocuments(t *testing.T) {
	sf := newStatusFeed(t)
	st, sub, poller := healthFixture(t, sf.server.URL+"/feed.xml")
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Millisecond)
	poller.now = func() time.Time { return start }

	for _, body := range []string{
		`<rss><channel><title a=></title></channel></rss>`,
		`<html><body><h1>Publisher error page</h1></body></html>`,
	} {
		sf.set(http.StatusOK, body)
		if _, err := poller.pollSubscription(ctx, sub); err == nil {
			t.Fatal("invalid feed polled clean")
		}
		health := reloadSubscription(t, st, sub.ID).Health
		if health.LastErrorClass != store.FeedErrorParse {
			t.Fatalf("parse class = %q", health.LastErrorClass)
		}
	}

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
		health := reloadSubscription(t, st, sub.ID).Health
		if health.NextPollAt.After(poller.now()) {
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
		sf.set(http.StatusOK, feedWith(newURL))
		st, sub, poller := healthFixture(t, sf.server.URL+"/temporary.xml")
		for range 3 {
			if _, err := poller.pollSubscription(ctx, sub); err != nil {
				t.Fatal(err)
			}
		}
		if got, _ := st.Subscription(ctx, sub.ID); got.FeedURL != sub.FeedURL {
			t.Fatal("temporary destination supplied a persistent feed move")
		}
		sf.set(http.StatusOK, `<rss><channel><title>T</title></channel></rss>`)
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
	if stored := reloadSubscription(t, st, sub.ID); stored.LastPolledAt.IsZero() {
		t.Fatal("background poll did not record a poll time")
	}
	if err := poller.PollSubscriptionNow(context.Background(), "missing"); err == nil {
		t.Fatal("unknown subscription accepted")
	}
}
