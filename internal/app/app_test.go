package app

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/hashicorp/yamux"

	"github.com/beamfall/core/internal/artwork"
	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/governor"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/ingest"
	"github.com/beamfall/core/internal/integrationhost"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/migrate"
	"github.com/beamfall/core/internal/offlinepack"
	"github.com/beamfall/core/internal/playback/subtitleingest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/reveal"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/watcher"
	scrobble "github.com/beamfall/plugin-sdk/scrobble"
)

type memSecretStore map[string]string

func (s memSecretStore) Secret(ref string) (string, error) {
	value, ok := s[ref]
	if !ok {
		// libencrypt.SecretStore: absence must be fs.ErrNotExist so an unreadable
		// entry is never mistaken for one that was never set.
		return "", fmt.Errorf("secret not found: %w", fs.ErrNotExist)
	}
	return value, nil
}

type recordingScrobbleDoer struct {
	mu    sync.Mutex
	calls int
	done  chan struct{}
	once  sync.Once
}

func newRecordingScrobbleDoer() *recordingScrobbleDoer {
	return &recordingScrobbleDoer{done: make(chan struct{})}
}

func (d *recordingScrobbleDoer) Do(req *http.Request) (*http.Response, error) {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	d.once.Do(func() { close(d.done) })
	return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func (d *recordingScrobbleDoer) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

type boundaryScrobbleAdapter struct {
	client   scrobble.Doer
	target   string
	declared []string
}

func (a boundaryScrobbleAdapter) Service() scrobble.Service   { return "boundary-test" }
func (a boundaryScrobbleAdapter) Class() scrobble.MediaClass  { return scrobble.ClassVideo }
func (a boundaryScrobbleAdapter) IdentitySchemes() []string   { return []string{"boundary"} }
func (a boundaryScrobbleAdapter) DeclaredEndpoints() []string { return a.declared }
func (a boundaryScrobbleAdapter) Submit(ctx context.Context, _ scrobble.Scrobble) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.target, nil)
	if err != nil {
		return err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
		_ = resp.Body.Close()
	}
	return nil
}

func (s memSecretStore) SetSecret(ref, value string) error {
	if value == "" {
		delete(s, ref)
		return nil
	}
	s[ref] = value
	return nil
}

func TestNewFailsWhenIntegrationHostPermissionVocabularyDiverges(t *testing.T) {
	original := assertIntegrationHostPermissionVocabulary
	assertIntegrationHostPermissionVocabulary = func() error {
		return integrationhost.ErrPermissionVocabularyDiverged
	}
	defer func() { assertIntegrationHostPermissionVocabulary = original }()

	_, err := New(context.Background(), config.Default(), nil, nil)
	if !errors.Is(err, integrationhost.ErrPermissionVocabularyDiverged) {
		t.Fatalf("New should fail boot on permission vocabulary divergence, got %v", err)
	}
}

func TestBuildScrobbleDispatcherEnforcesDeclaredEndpoints(t *testing.T) {
	cases := []struct {
		name           string
		target         string
		declared       []string
		wantBaseCalls  int
		wantDeadLetter bool
	}{
		{
			name:          "declared origin passes",
			target:        "https://allowed.example/scrobble",
			declared:      []string{"https://allowed.example"},
			wantBaseCalls: 1,
		},
		{
			name:           "undeclared origin is dead-lettered",
			target:         "https://blocked.example/scrobble",
			declared:       []string{"https://allowed.example"},
			wantBaseCalls:  0,
			wantDeadLetter: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := storetest.Open(t)
			doer := newRecordingScrobbleDoer()
			dispatcher := newBoundaryScrobbleDispatcher(t, st, doer, tc.target, tc.declared)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan struct{})
			go func() {
				dispatcher.Run(ctx)
				close(done)
			}()

			dispatcher.Watched(scrobble.WatchedEvent{
				ProfileID:  "profile-default",
				LibraryID:  "library-local",
				MediaID:    "media-boundary",
				Class:      scrobble.ClassVideo,
				TargetHint: scrobble.TargetStandalone,
				ExternalIDs: []scrobble.ExternalID{{
					Scheme: "boundary",
					Value:  "42",
				}},
				WatchedAt: time.Unix(1_700_000_000, 0).UTC(),
			})

			if tc.wantDeadLetter {
				deadline := time.After(2 * time.Second)
				for len(dispatcher.DeadLetters()) == 0 {
					select {
					case <-deadline:
						t.Fatal("timed out waiting for undeclared scrobble dead-letter")
					case <-time.After(10 * time.Millisecond):
					}
				}
				got := dispatcher.DeadLetters()[0]
				if got.Service != "boundary-test" || !strings.Contains(got.LastError, "not declared") {
					t.Fatalf("dead-letter = %#v", got)
				}
			} else {
				select {
				case <-doer.done:
				case <-time.After(2 * time.Second):
					t.Fatal("declared scrobble did not reach base Doer")
				}
				if len(dispatcher.DeadLetters()) != 0 {
					t.Fatalf("declared scrobble dead-lettered: %#v", dispatcher.DeadLetters())
				}
			}

			cancel()
			<-done
			if got := doer.count(); got != tc.wantBaseCalls {
				t.Fatalf("base Doer calls = %d, want %d", got, tc.wantBaseCalls)
			}
		})
	}
}

func TestBuildScrobbleDispatcherTargetHintsFailClosed(t *testing.T) {
	tests := []struct {
		name string
		hint scrobble.TargetHint
	}{
		{name: "absent"},
		{name: "unknown", hint: "future-shape"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := storetest.Open(t)
			doer := newRecordingScrobbleDoer()
			dispatcher := newBoundaryScrobbleDispatcher(t, st, doer, "https://allowed.example/scrobble", []string{"https://allowed.example"})
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() {
				dispatcher.Run(ctx)
				close(done)
			}()

			dispatcher.Watched(scrobble.WatchedEvent{
				ProfileID:  "profile-default",
				LibraryID:  "library-local",
				MediaID:    "media-boundary",
				Class:      scrobble.ClassVideo,
				TargetHint: tt.hint,
				ExternalIDs: []scrobble.ExternalID{{
					Scheme: "boundary",
					Value:  "42",
				}},
				WatchedAt: time.Unix(1_700_000_000, 0).UTC(),
			})

			deadline := time.After(2 * time.Second)
			for len(dispatcher.Skips()) == 0 {
				select {
				case <-deadline:
					t.Fatal("timed out waiting for target-hint skip")
				case <-time.After(10 * time.Millisecond):
				}
			}
			got := dispatcher.Skips()[0]
			if got.Reason != "scrobble target hint is absent or unknown" {
				t.Fatalf("skip = %#v", got)
			}

			cancel()
			<-done
			if got := doer.count(); got != 0 {
				t.Fatalf("base Doer calls = %d, want 0", got)
			}
		})
	}
}

func TestScrobbleHTTPClientRefusesCredentialBearingOriginChanges(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		targetTLS  bool
	}{
		{name: "cross-origin HTTPS", statusCode: http.StatusTemporaryRedirect, targetTLS: true},
		{name: "HTTPS downgrade", statusCode: http.StatusPermanentRedirect, targetTLS: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var targetCalls int
			targetHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				targetCalls++
				w.WriteHeader(http.StatusNoContent)
			})
			var target *httptest.Server
			if tt.targetTLS {
				target = httptest.NewTLSServer(targetHandler)
			} else {
				target = httptest.NewServer(targetHandler)
			}
			defer target.Close()

			source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL+"/collect", tt.statusCode)
			}))
			defer source.Close()

			client := newScrobbleHTTPClient()
			client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 -- loopback test servers only.
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, source.URL+"/submit", strings.NewReader("access_token=secret"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer secret")
			resp, err := client.Do(req)
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "origin changed") {
				t.Fatalf("redirect error = %v, want origin-change refusal", err)
			}
			if targetCalls != 0 {
				t.Fatalf("redirect target received %d credential-bearing requests, want 0", targetCalls)
			}
		})
	}
}

func newBoundaryScrobbleDispatcher(t *testing.T, st *store.Store, doer scrobble.Doer, target string, declared []string) *scrobble.Dispatcher {
	t.Helper()
	dataDir := t.TempDir()
	cfg := config.Config{DataDir: dataDir}
	secrets := secretFiles{dir: filepath.Join(dataDir, "secrets")}
	if err := secrets.SetSecret("boundary-token", "secret-token"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}
	configPath := filepath.Join(dataDir, "scrobble-adapters.json")
	configBody := []byte(`{"adapters":[{"service":"boundary-test"}],"max_attempts":1}`)
	if err := os.WriteFile(configPath, configBody, 0o600); err != nil {
		t.Fatalf("write scrobble config: %v", err)
	}
	if err := st.UpsertScrobbleLink(t.Context(), store.ScrobbleLink{
		ProfileID:    "profile-default",
		Service:      "boundary-test",
		SecretRef:    "boundary-token",
		AccountLabel: "Boundary",
		LinkedAt:     time.Unix(1_700_000_000, 0).UTC(),
	}); err != nil {
		t.Fatalf("UpsertScrobbleLink: %v", err)
	}
	if err := st.SetScrobbleLibraryEligibility(t.Context(), "library-local", true); err != nil {
		t.Fatalf("SetScrobbleLibraryEligibility: %v", err)
	}
	registry := map[scrobble.Service]scrobble.AdapterFactory{
		"boundary-test": func(client scrobble.Doer, cfg scrobble.AdapterConfig, secrets scrobble.SecretSource) (scrobble.Adapter, error) {
			return boundaryScrobbleAdapter{client: client, target: target, declared: declared}, nil
		},
	}
	dispatcher, err := buildScrobbleDispatcherWithRegistry(cfg, st, slog.Default(), doer, registry)
	if err != nil {
		t.Fatalf("buildScrobbleDispatcherWithRegistry: %v", err)
	}
	if dispatcher == nil {
		t.Fatal("dispatcher is nil")
	}
	return dispatcher
}

func TestFeedDirectorySearcherQueriesBothDirectoriesAndMergesResults(t *testing.T) {
	var calls []string
	results, err := searchFeedDirectories(
		t.Context(),
		"beam fall",
		func(_ context.Context, query string) ([]graph.FeedSearchResult, error) {
			calls = append(calls, "itunes:"+query)
			return []graph.FeedSearchResult{{Title: "iTunes result"}}, nil
		},
		func(_ context.Context, query string) ([]graph.FeedSearchResult, error) {
			calls = append(calls, "podcast-index:"+query)
			return []graph.FeedSearchResult{{Title: "Podcast Index result"}}, nil
		},
	)
	if err != nil {
		t.Fatalf("SearchFeeds: %v", err)
	}
	if got, want := calls, []string{"itunes:beam fall", "podcast-index:beam fall"}; !slices.Equal(got, want) {
		t.Fatalf("directory calls = %v, want %v", got, want)
	}
	if len(results) != 2 {
		t.Fatalf("result count = %d, want 2", len(results))
	}
	if got, want := []string{results[0].Title, results[1].Title}, []string{"iTunes result", "Podcast Index result"}; !slices.Equal(got, want) {
		t.Fatalf("result titles = %v, want %v", got, want)
	}

	t.Run("falls back to iTunes when Podcast Index fails", func(t *testing.T) {
		results, err := searchFeedDirectories(
			t.Context(),
			"beam fall",
			func(context.Context, string) ([]graph.FeedSearchResult, error) {
				return []graph.FeedSearchResult{{Title: "iTunes fallback"}}, nil
			},
			func(context.Context, string) ([]graph.FeedSearchResult, error) {
				return nil, errors.New("Podcast Index unavailable")
			},
		)
		if err != nil {
			t.Fatalf("SearchFeeds: %v", err)
		}
		if got, want := results, []graph.FeedSearchResult{{Title: "iTunes fallback"}}; !slices.Equal(got, want) {
			t.Fatalf("results = %#v, want %#v", got, want)
		}
	})

	t.Run("propagates iTunes failure", func(t *testing.T) {
		itunesErr := errors.New("iTunes unavailable")
		podcastIndexCalled := false
		results, err := searchFeedDirectories(
			t.Context(),
			"beam fall",
			func(context.Context, string) ([]graph.FeedSearchResult, error) {
				return nil, itunesErr
			},
			func(context.Context, string) ([]graph.FeedSearchResult, error) {
				podcastIndexCalled = true
				return []graph.FeedSearchResult{{Title: "unexpected"}}, nil
			},
		)
		if !errors.Is(err, itunesErr) {
			t.Fatalf("error = %v, want %v", err, itunesErr)
		}
		if results != nil {
			t.Fatalf("results = %#v, want nil", results)
		}
		if podcastIndexCalled {
			t.Fatal("Podcast Index called after iTunes failure")
		}
	})
}

func TestAppWiresRoutesAndSurfaces(t *testing.T) {
	beamfall, err := New(context.Background(), testConfig(t), nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()

	// 53 = the 52 routes on main plus this lane's offline-pack signing-authority
	// route. This constant is rebase-hidden
	// coupling: git keeps it as common context, so every lane that adds a route
	// must bump it explicitly.
	if got, want := len(beamfall.Routes()), 53; got != want {
		t.Fatalf("len(Routes) = %d, want %d", got, want)
	}
	if got := len(beamfall.Surfaces().All()); got < 5 {
		t.Fatalf("len(Surfaces) = %d, want at least 5", got)
	}
	if !hasSurface(beamfall.Surfaces().All(), "Query.jobs") || !hasSurface(beamfall.Surfaces().All(), "Query.job") {
		t.Fatalf("job query surfaces missing: %#v", beamfall.Surfaces().All())
	}
	if !hasSurface(beamfall.Surfaces().All(), "Query.artworkCandidates") ||
		!hasSurface(beamfall.Surfaces().All(), "Mutation.pinArtworkCandidate") {
		t.Fatalf("artwork candidate surfaces missing: %#v", beamfall.Surfaces().All())
	}
}

func TestAppWiresJellyfinCompatToggle(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		want    int
	}{
		{"off hides prefix", false, http.StatusNotFound},
		{"on reaches auth boundary", true, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(t)
			cfg.JellyfinCompat.Enabled = tc.enabled
			beamfall, err := New(context.Background(), cfg, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer beamfall.Close()
			req := httptest.NewRequest(http.MethodPost, "/compat/jellyfin/Users/AuthenticateByName", strings.NewReader("{}"))
			req.RemoteAddr = "127.0.0.1:1234"
			rec := httptest.NewRecorder()
			beamfall.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestNewClosesOwnedWatcherOnLateConstructorFailure(t *testing.T) {
	realClose := closeOwnedWatcher
	closed := 0
	closeOwnedWatcher = func(service *watcher.Service) error {
		closed++
		return realClose(service)
	}
	t.Cleanup(func() { closeOwnedWatcher = realClose })
	cfg := testConfig(t)
	cfg.JellyfinCompat.AllowedCIDRs = []string{"not-a-cidr"}

	beamfall, err := New(context.Background(), cfg, nil, nil)
	if err == nil || beamfall != nil {
		t.Fatalf("New invalid late compat config = (%v, %v), want failure", beamfall, err)
	}
	if closed != 1 {
		t.Fatalf("owned watcher close count = %d, want 1", closed)
	}
}

func TestPluginScanHooksOnlyUseActiveValidatedBundles(t *testing.T) {
	if got := scanParsers(nil, nil); got != nil {
		t.Fatalf("nil registry parsers = %#v, want nil", got)
	}
	registry, err := plugin.NewRegistry([]plugin.Bundle{testPathBundle()})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	parsers := scanParsers(registry, map[string]scanpkg.FilenameParser{
		testPathPluginID:  fixtureFilenameParser{parsed: scanpkg.ParsedMedia{Title: "Active"}},
		testQueuePluginID: fixtureFilenameParser{parsed: scanpkg.ParsedMedia{Title: "Inactive"}},
	})
	if parsers[testPathPluginID] == nil {
		t.Fatal("active parser missing")
	}
	if parsers[testQueuePluginID] != nil {
		t.Fatal("inactive parser registered")
	}
}

func TestMigrationSourceResolverRequiresDeclaredActiveImporter(t *testing.T) {
	registry, err := plugin.NewRegistry([]plugin.Bundle{testPathBundle(), testSerialBundle()})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	resolver := migrationSourceResolver{plugins: registry, factories: migrationTestHooks().MigrationSourceFactories}
	if _, ok := resolver.Resolve(testPathPluginID, testPathImporterID, nil, "/tmp/path-records.json"); !ok {
		t.Fatal("declared active importer was not resolved")
	}
	if _, ok := resolver.Resolve(testPathPluginID, "made-up", nil, "/tmp/nope"); ok {
		t.Fatal("undeclared importer resolved")
	}
	if _, ok := resolver.Resolve(testEventPluginID, testEventImporterID, nil, "/tmp/activity.json"); ok {
		t.Fatal("inactive importer resolved")
	}
}

func TestLibEncryptByteAdapterDecryptsAtRestStream(t *testing.T) {
	const libraryID = store.LibraryID("library-local")
	manager := libencrypt.NewManagerWithParams(memSecretStore{}, libencrypt.TestArgon2Params)
	if _, err := manager.Provision(string(libraryID), libencrypt.UnsealModeAuto, ""); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	dek, err := manager.DEK(string(libraryID))
	if err != nil {
		t.Fatalf("DEK: %v", err)
	}
	// A freshly provisioned library is a v2-only key generation: objects are
	// context-bound v2 streams keyed by the persisted content fingerprint.
	obj := libencrypt.MediaObjectContext(string(libraryID), "sha256:app-adapter-test")
	var ciphertext bytes.Buffer
	if err := libencrypt.EncryptStreamV2(&ciphertext, strings.NewReader("plain media bytes"), dek, obj); err != nil {
		t.Fatalf("EncryptStreamV2: %v", err)
	}
	path := filepath.Join(t.TempDir(), "media.enc")
	if err := os.WriteFile(path, ciphertext.Bytes(), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	adapter := appLibEncryptDBCipher{manager: manager}
	got, err := adapter.DecryptBytes(libraryID, path, string(libencrypt.ObjectClassMediaBytes), "sha256:app-adapter-test")
	if err != nil {
		t.Fatalf("DecryptBytes: %v", err)
	}
	if string(got) != "plain media bytes" {
		t.Fatalf("DecryptBytes = %q, want plaintext", got)
	}

	// Wrong caller-supplied identity must fail before plaintext returns.
	if _, err := adapter.DecryptBytes(libraryID, path, string(libencrypt.ObjectClassMediaBytes), "sha256:some-other-object"); err == nil {
		t.Fatal("DecryptBytes with wrong object identity must fail")
	}
	// An unknown object class fails closed in context validation.
	if _, err := adapter.DecryptBytes(libraryID, path, "made-up-class", "sha256:app-adapter-test"); err == nil {
		t.Fatal("DecryptBytes with unknown object class must fail")
	}

	// Committed-v2 → v1 replay: a legacy v1 stream restored into a v2-only key
	// generation is rejected — the expected format is the generation's
	// commitment, never sniffed from the file.
	var v1 bytes.Buffer
	if err := libencrypt.EncryptStream(&v1, strings.NewReader("plain media bytes"), dek); err != nil {
		t.Fatalf("EncryptStream: %v", err)
	}
	v1Path := filepath.Join(t.TempDir(), "media-v1.enc")
	if err := os.WriteFile(v1Path, v1.Bytes(), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := adapter.DecryptBytes(libraryID, v1Path, string(libencrypt.ObjectClassMediaBytes), "sha256:app-adapter-test"); err == nil {
		t.Fatal("v1 ciphertext must be rejected in a committed v2 key generation")
	}
}

func TestRetentionSweepReconcilesSeenPathsAndKeepsNewestEntries(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	sub, older, newer := retentionFixture(t, st)
	one := 1
	if _, err := st.SetSubscriptionRetention(ctx, sub.ID, store.RetentionKeepLastN, &one); err != nil {
		t.Fatal(err)
	}

	runRetentionPurge(ctx, st, artwork.NewService(st, nil, t.TempDir()), discardLogger())

	ids, err := st.MediaIDsByExternalID(ctx, "feed-guid", "guid-old")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != older {
		t.Fatalf("reconciled ids = %v, want %s", ids, older)
	}
	trashed, err := st.TrashedMediaList(ctx, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(trashed) != 1 || trashed[0].ID != older {
		t.Fatalf("trashed = %+v, want only older media", trashed)
	}
	if _, err := st.Media(ctx, newer, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}); err != nil {
		t.Fatalf("newer media was trashed: %v", err)
	}
}

func TestRetentionSweepDeleteAfterPlayedRequiresAllFollowersAndSkipsFavorites(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	sub, older, newer := retentionFixture(t, st)
	if _, err := st.SetSubscriptionRetention(ctx, sub.ID, store.RetentionDeleteAfterPlayed, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFollow(ctx, "profile-default", sub.ID, true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPlaybackProgress(ctx, store.PlaybackProgress{
		ProfileID: "profile-default", MediaID: older, PositionSeconds: 120, DurationSeconds: 120,
	}, true, store.ResumePolicy{Class: store.MediaClassAudio}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPlaybackProgress(ctx, store.PlaybackProgress{
		ProfileID: "profile-default", MediaID: newer, PositionSeconds: 120, DurationSeconds: 120,
	}, true, store.ResumePolicy{Class: store.MediaClassAudio}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFavorite(ctx, "profile-default", newer, true, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}); err != nil {
		t.Fatal(err)
	}

	runRetentionPurge(ctx, st, artwork.NewService(st, nil, t.TempDir()), discardLogger())

	trashed, err := st.TrashedMediaList(ctx, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(trashed) != 1 || trashed[0].ID != older {
		t.Fatalf("trashed = %+v, want only played non-favorite", trashed)
	}
	if _, err := st.Media(ctx, newer, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}); err != nil {
		t.Fatalf("favorite media was trashed: %v", err)
	}
}

// openTrashDisabledStore opens a development-seeded store with the
// trash.enabled toggle OFF, returning the store and its data dir.
func openTrashDisabledStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	dataDir := t.TempDir()
	st, err := store.Open(context.Background(), dataDir, store.Options{SeedDevelopmentData: true, TrashDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, dataDir
}

func TestRetentionSweepSkipsWhenTrashDisabled(t *testing.T) {
	st, _ := openTrashDisabledStore(t)
	ctx := context.Background()
	sub, older, newer := retentionFixture(t, st)
	one := 1
	if _, err := st.SetSubscriptionRetention(ctx, sub.ID, store.RetentionKeepLastN, &one); err != nil {
		t.Fatal(err)
	}

	var logBuf strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	runRetentionPurge(ctx, st, artwork.NewService(st, nil, t.TempDir()), logger)

	trashed, err := st.TrashedMediaList(ctx, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(trashed) != 0 {
		t.Fatalf("trashed = %+v, want none: retention sweep must not trash with trash disabled", trashed)
	}
	for _, id := range []store.MediaID{older, newer} {
		if _, err := st.Media(ctx, id, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}); err != nil {
			t.Fatalf("media %s was trashed by skipped sweep: %v", id, err)
		}
	}
	if got := st.TrashDisabledSweepSkips(); got != 3 {
		t.Fatalf("TrashDisabledSweepSkips = %d, want 3", got)
	}
	if got := strings.Count(logBuf.String(), "subscription retention skipped"); got != 1 {
		t.Fatalf("skip log lines = %d, want exactly 1 per sweep; log:\n%s", got, logBuf.String())
	}
	if got := strings.Count(logBuf.String(), `msg="trash purge skipped: confirmation required"`); got != 1 {
		t.Fatalf("trash purge skip log lines = %d, want exactly 1 per sweep; log:\n%s", got, logBuf.String())
	}
	if got := strings.Count(logBuf.String(), "managed file trash purge skipped"); got != 1 {
		t.Fatalf("managed-file trash purge skip log lines = %d, want exactly 1 per sweep; log:\n%s", got, logBuf.String())
	}
	if got := strings.Count(logBuf.String(), "confirmation required"); got != 3 {
		t.Fatalf("confirmation-required log lines = %d, want one for each refused sweep; log:\n%s", got, logBuf.String())
	}
	if strings.Contains(logBuf.String(), "retention failed") || strings.Contains(logBuf.String(), `msg="trash purge failed"`) || strings.Contains(logBuf.String(), "managed file trash purge failed") {
		t.Fatalf("confirm-required outcome logged as failure:\n%s", logBuf.String())
	}
}

func TestRetentionSweepLogsManagedFileTrashFailure(t *testing.T) {
	st := storetest.Open(t)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	var logBuf strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	runRetentionPurge(context.Background(), st, artwork.NewService(st, nil, t.TempDir()), logger)

	if got := strings.Count(logBuf.String(), "managed file trash purge failed"); got != 1 {
		t.Fatalf("managed-file trash purge failure log lines = %d, want exactly 1; log:\n%s", got, logBuf.String())
	}
	if strings.Contains(logBuf.String(), "managed file trash purge skipped") {
		t.Fatalf("managed-file trash purge failure logged as skipped:\n%s", logBuf.String())
	}
}

func TestExpiredTrashPurgeSkipsWithTrashDisabled(t *testing.T) {
	st, dataDir := openTrashDisabledStore(t)
	ctx := context.Background()
	_, older, _ := retentionFixture(t, st)
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}

	// Interactive trash stays allowed with trash disabled; only autonomous
	// destruction is gated.
	if err := st.TrashMedia(ctx, older, filter); err != nil {
		t.Fatalf("interactive TrashMedia with trash disabled: %v", err)
	}
	storetest.BackdateTrashedMedia(t, dataDir, older, time.Now().UTC().Add(-store.TrashRetention-time.Hour))

	runRetentionPurge(ctx, st, artwork.NewService(st, nil, t.TempDir()), discardLogger())

	trashed, err := st.TrashedMediaList(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(trashed) != 1 || trashed[0].ID != older {
		t.Fatalf("trashed = %+v, want retained expired trash %s", trashed, older)
	}
}

func TestRetentionPurgeReconcilesCandidatePreviewCache(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(context.Background(), dataDir, store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local", "library-archive"}, Revealed: true}

	// One candidate on the media that will be purged, one on a media that stays.
	if err := st.UpsertArtworkCandidate(ctx, "media-alpha", "poster", "https://images.example.test/purged.jpg", "demo", "lookup"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertArtworkCandidate(ctx, "media-archive", "poster", "https://images.example.test/survivor.jpg", "demo", "lookup"); err != nil {
		t.Fatal(err)
	}
	purgedID := store.ArtworkCandidateID("media-alpha", "poster", "https://images.example.test/purged.jpg")
	survivorID := store.ArtworkCandidateID("media-archive", "poster", "https://images.example.test/survivor.jpg")

	art := artwork.NewService(st, nil, dataDir)
	purgedPreview := writeCandidatePreview(t, art, purgedID)
	survivorPreview := writeCandidatePreview(t, art, survivorID)

	if err := st.TrashMedia(ctx, "media-alpha", filter); err != nil {
		t.Fatal(err)
	}
	storetest.BackdateTrashedMedia(t, dataDir, "media-alpha", time.Now().UTC().Add(-store.TrashRetention-time.Hour))

	runRetentionPurge(ctx, st, art, discardLogger())

	if _, err := os.Stat(purgedPreview); !os.IsNotExist(err) {
		t.Fatalf("purged candidate preview still on disk (%v): %s", err, purgedPreview)
	}
	if _, err := os.Stat(survivorPreview); err != nil {
		t.Fatalf("surviving candidate preview removed: %v", err)
	}
}

// writeCandidatePreview seeds a cached candidate preview file at the layout the
// candidate-preview cache uses (candidate-<id>/poster.jpg under the artwork dir)
// and returns its path.
func writeCandidatePreview(t *testing.T, art *artwork.Service, candidateID string) string {
	t.Helper()
	path := art.ArtworkPath(artwork.CandidateCacheID(candidateID), artwork.SlotPoster)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAppRequiresStreamTokenKey(t *testing.T) {
	_, err := New(context.Background(), config.Default(), nil, nil)
	if err == nil {
		t.Fatal("New accepted empty stream token key")
	}
}

func TestAppDoesNotSeedDevelopmentCredentialsByDefault(t *testing.T) {
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = false
	beamfall, err := New(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	req.Header.Set("X-Beamfall-Session", "session-manager")
	req.Header.Set("X-Beamfall-Device", "device-mac")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")
	rec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAppSeedsDevelopmentCredentialsOnlyWhenEnabled(t *testing.T) {
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	req.Header.Set("X-Beamfall-Session", "session-manager")
	req.Header.Set("X-Beamfall-Device", "device-mac")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")
	rec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestDatabaseBackupHandlerPrunesToKeepN(t *testing.T) {
	ctx := context.Background()
	dest := t.TempDir()
	dataDir := t.TempDir()
	st, err := store.Open(ctx, dataDir, store.Options{SeedDevelopmentData: true, AllowedRoots: []string{dest}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetSnapshotSealer(backupcore.NewDirectorySealer(dataDir))

	handler := databaseBackupHandler(st, dest, nil)
	// Run the scheduled handler more times than the retention floor; each run
	// writes a distinct set and then prunes so only databaseBackupKeepN remain.
	for i := range databaseBackupKeepN + 3 {
		if _, err := handler(ctx, store.Job{ID: store.JobID("backup-job"), Kind: databaseBackupJobKind}); err != nil {
			t.Fatalf("handler run %d: %v", i, err)
		}
		// A distinct nanosecond timestamp per run keeps the set names ordered.
		time.Sleep(2 * time.Millisecond)
	}

	history, err := st.BackupHistory(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != databaseBackupKeepN {
		t.Fatalf("retained %d backup sets, want keep-N %d", len(history), databaseBackupKeepN)
	}
}

func TestDatabaseBackupHandlerReturnsBackupError(t *testing.T) {
	ctx := context.Background()
	dest := t.TempDir()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true, AllowedRoots: []string{dest}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// A dest outside the sanctioned allowlist makes the backup itself fail; the
	// handler must surface that error (unlike a prune failure, which is
	// non-fatal after a successful backup).
	handler := databaseBackupHandler(st, t.TempDir(), nil)
	if _, err := handler(ctx, store.Job{ID: "backup-job", Kind: databaseBackupJobKind}); err == nil {
		t.Fatal("handler swallowed a backup failure")
	}
}

// TestScheduledDatabaseBackupEnqueuesAndWritesThroughWorker is the
// scheduler→worker reachability proof for the seeded database-backup Schedule.
// The other backup tests invoke databaseBackupHandler
// directly; this one drives the REAL wiring end to end: a boot-seeded backup
// Schedule that has come DUE is enqueued by the real scheduler Tick and then
// executed by the real background worker (RunOne over JobClassBackground,
// exactly as the serve loop runs it), and asserts a backup set actually landed
// on disk under the sanctioned DataDir/backups destination.
func TestScheduledDatabaseBackupEnqueuesAndWritesThroughWorker(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true // give the store real data to back up
	beamfall, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()

	// New's Reconcile seeded the backup Schedule with LastCompletedEnqueue=now
	// (Daily(3,0)). Tick well past the next daily fire so the Schedule is DUE and
	// the real scheduler enqueues the database-backup job.
	due := time.Now().UTC().Add(48 * time.Hour)
	if err := beamfall.schedule.Tick(ctx, due); err != nil {
		t.Fatalf("schedule Tick: %v", err)
	}

	// Drain the background queue exactly as the serve loop does (line ~1547):
	// Tick enqueues every DUE background Schedule, so we run the real worker until
	// it claims and runs the database-backup job (or the queue empties).
	ranBackup := false
	for {
		job, err := beamfall.jobs.RunOne(ctx, []store.JobClass{store.JobClassBackground}, due)
		if errors.Is(err, jobs.ErrNoRunnable) { // queue drained
			break
		}
		if err != nil {
			t.Fatalf("RunOne returned error: %v", err)
		}
		if job.Kind == databaseBackupJobKind {
			ranBackup = true
		}
	}
	if !ranBackup {
		t.Fatal("scheduler never enqueued a database-backup job for the worker")
	}

	// The handler must have written a backup set to the sanctioned destination
	// the app wires it to (filepath.Join(cfg.DataDir, "backups")).
	dest := filepath.Join(cfg.DataDir, "backups")
	history, err := beamfall.store.BackupHistory(ctx, dest)
	if err != nil {
		t.Fatalf("BackupHistory: %v", err)
	}
	if len(history) == 0 {
		t.Fatalf("scheduled backup wrote no set to %s", dest)
	}
}

func TestAppRegistersScanJobHandler(t *testing.T) {
	root := t.TempDir()
	clip := filepath.Join(root, "clip.mp4")
	if err := os.WriteFile(clip, []byte("clip bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Backdate past the file-stability window so the scan treats
	// the clip as settled rather than still-streaming and imports it this pass.
	settled := time.Now().Add(-time.Hour)
	if err := os.Chtimes(clip, settled, settled); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()
	now := time.Unix(100, 0).UTC()
	job, err := scanpkg.NewJob(scanpkg.JobRequest{
		ID:        "job-scan-app",
		LibraryID: "library-local",
		Root:      root,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := beamfall.store.EnqueueJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}

	if _, err := beamfall.jobs.RunOne(context.Background(), nil, now); err != nil {
		t.Fatalf("RunOne returned error: %v", err)
	}

	items, err := beamfall.store.SourceItems(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}, store.SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSourceTitle(items, "clip") {
		t.Fatalf("sourceItems = %#v, want imported clip", items)
	}
}

// TestMigrationWizardPathSourceReachesHandlerEndToEnd proves an operator source
// path reaches a declared hook and the generic migration handler.
func TestMigrationWizardPathSourceReachesHandlerEndToEnd(t *testing.T) {
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(context.Background(), cfg, nil, []plugin.Bundle{testPathBundle()}, migrationTestHooks())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()
	ctx := context.Background()

	root := t.TempDir()
	videoPath := filepath.Join(root, "The Fixture (2024).mkv")
	if err := os.WriteFile(videoPath, []byte("fixture bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	absVideo, err := filepath.Abs(videoPath)
	if err != nil {
		t.Fatal(err)
	}
	const fingerprint = "fp-path-fixture-0001"
	if _, _, err := beamfall.store.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               absVideo,
		Title:              "The Fixture",
		Container:          "mkv",
		Codec:              "h264",
		Class:              store.MediaClassVideo,
		ContentFingerprint: fingerprint,
	}); err != nil {
		t.Fatalf("seed media at fixture video path: %v", err)
	}
	sourcePath := writeMigrationFixture(t, migrate.ForeignItem{Path: absVideo})

	// runImport is an Admin-Session Console operation; open the
	// window on the seeded manager session as a real Console client would.
	if err := beamfall.store.SetSessionAdminUntil(ctx, "session-manager", time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatalf("open admin session: %v", err)
	}

	// Drive the real runImport mutation over HTTP with the operator-supplied
	// source path — the field this change adds.
	mutation := []byte(`{"query":"mutation R($pluginID:String!,$importerID:String!,$sourcePath:String!){ runImport(pluginID:$pluginID,importerID:$importerID,sourcePath:$sourcePath){ id kind status } }","variables":{"pluginID":"` + testPathPluginID + `","importerID":"` + testPathImporterID + `","sourcePath":"` + strings.ReplaceAll(sourcePath, `\`, `\\`) + `"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(string(mutation)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Beamfall-Session", "session-manager")
	req.Header.Set("X-Beamfall-Device", "device-mac")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")
	rec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("runImport status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Data struct {
			RunImport struct {
				ID, Kind, Status string
			} `json:"runImport"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode runImport response: %v (%s)", err, rec.Body.String())
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("runImport errors = %#v", resp.Errors)
	}
	if resp.Data.RunImport.ID == "" || resp.Data.RunImport.Kind != string(migrate.JobKind) {
		t.Fatalf("runImport job = %#v, want a queued migration job", resp.Data.RunImport)
	}

	// Run jobs through the real registered handler + the real
	// migrationSourceResolver — the chain under test. Drain the queue until the migration job is
	// claimed (seeded maintenance jobs may sit ahead of it).
	for i := 0; i < 32; i++ {
		ran, err := beamfall.jobs.RunOne(ctx, nil, time.Now().UTC())
		if err != nil {
			if errors.Is(err, store.ErrNoRunnableJob) {
				break // queue drained
			}
			t.Fatalf("RunOne returned error: %v", err)
		}
		if ran.ID == store.JobID(resp.Data.RunImport.ID) {
			break
		}
	}

	// The job completed with a 1-of-1 match-rate report, and the matched file is
	// match-pinned — the effect reached only through the supplied source path.
	done, err := beamfall.store.Job(ctx, store.JobID(resp.Data.RunImport.ID))
	if err != nil {
		t.Fatalf("read migration job back: %v", err)
	}
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("migration job status = %q, want completed: %s", done.Status, done.Error)
	}
	if done.Progress.Current != 1 || done.Progress.Total != 1 {
		t.Fatalf("match-rate = %d of %d, want 1 of 1", done.Progress.Current, done.Progress.Total)
	}
	pinned, found, err := beamfall.store.MatchPinned(ctx, fingerprint)
	if err != nil || !found {
		t.Fatalf("match-pin read: found=%v err=%v", found, err)
	}
	if !pinned {
		t.Fatal("matched fixture file not pinned: the source ran but the import effect did not land")
	}
}

// TestMigrationWizardMetadataEventsApplyPlayCountsEndToEnd is the MIG-4
// end-to-end slice: it plumbs from the runImport mutation
// through the real migrationSourceResolver → migrate.Handler, asserting a
// fixture scrobble matched by metadata applies a play
// count to the importing Profile's engagement.
func TestMigrationWizardMetadataEventsApplyPlayCountsEndToEnd(t *testing.T) {
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(context.Background(), cfg, nil, []plugin.Bundle{testEventBundle()}, migrationTestHooks())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()
	ctx := context.Background()

	// Seed an audio item + its credited Entity so the metadata match chain
	// resolves it by title and relation.
	const trackFP = "fp-metadata-events-0001"
	media, _, err := beamfall.store.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/audio/boc/roygbiv.flac",
		Title:              "Roygbiv",
		Container:          "flac",
		Codec:              "flac",
		Class:              store.MediaClassAudio,
		ContentFingerprint: trackFP,
	})
	if err != nil {
		t.Fatalf("seed track: %v", err)
	}
	if err := beamfall.store.UpsertEntity(ctx, store.Entity{ID: "entity-boc", LibraryID: "library-local", Kind: "artist", Title: "Boards of Canada"}); err != nil {
		t.Fatalf("seed artist entity: %v", err)
	}
	if err := beamfall.store.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: "entity-boc", Name: "appears-on", ToMediaID: media.ID}); err != nil {
		t.Fatalf("relate artist: %v", err)
	}

	sourcePath := writeMigrationFixture(t,
		migrate.ForeignItem{Scrobble: migrate.Scrobble{Present: true, Artist: "Boards of Canada", Album: "Music Has the Right to Children", Track: "Roygbiv", PlayedAt: time.Unix(1700000000, 0).UTC()}},
		migrate.ForeignItem{Scrobble: migrate.Scrobble{Present: true, Artist: "Boards of Canada", Album: "Music Has the Right to Children", Track: "Roygbiv", PlayedAt: time.Unix(1700003600, 0).UTC()}},
	)

	if err := beamfall.store.SetSessionAdminUntil(ctx, "session-manager", time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatalf("open admin session: %v", err)
	}

	mutation := []byte(`{"query":"mutation R($pluginID:String!,$importerID:String!,$sourcePath:String!){ runImport(pluginID:$pluginID,importerID:$importerID,sourcePath:$sourcePath){ id kind status } }","variables":{"pluginID":"` + testEventPluginID + `","importerID":"` + testEventImporterID + `","sourcePath":"` + strings.ReplaceAll(sourcePath, `\`, `\\`) + `"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(string(mutation)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Beamfall-Session", "session-manager")
	req.Header.Set("X-Beamfall-Device", "device-mac")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")
	rec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("runImport status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Data struct {
			RunImport struct{ ID, Kind, Status string } `json:"runImport"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode runImport response: %v (%s)", err, rec.Body.String())
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("runImport errors = %#v", resp.Errors)
	}
	if resp.Data.RunImport.ID == "" || resp.Data.RunImport.Kind != string(migrate.JobKind) {
		t.Fatalf("runImport job = %#v, want a queued migration job", resp.Data.RunImport)
	}

	// Run through the real registered handler + resolver.
	for i := 0; i < 32; i++ {
		ran, err := beamfall.jobs.RunOne(ctx, nil, time.Now().UTC())
		if err != nil {
			if errors.Is(err, store.ErrNoRunnableJob) {
				break
			}
			t.Fatalf("RunOne returned error: %v", err)
		}
		if ran.ID == store.JobID(resp.Data.RunImport.ID) {
			break
		}
	}

	done, err := beamfall.store.Job(ctx, store.JobID(resp.Data.RunImport.ID))
	if err != nil {
		t.Fatalf("read migration job back: %v", err)
	}
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("migration job status = %q, want completed: %s", done.Status, done.Error)
	}
	if done.Progress.Current != 2 || done.Progress.Total != 2 {
		t.Fatalf("match-rate = %d of %d, want 2 of 2", done.Progress.Current, done.Progress.Total)
	}

	// The two scrobbles applied as two plays on the importing Profile's
	// engagement, with import provenance. session-manager → profile-default.
	prog, ok, err := beamfall.store.PlaybackProgressFor(ctx, "profile-default", media.ID)
	if err != nil || !ok {
		t.Fatalf("playback progress: ok=%v err=%v", ok, err)
	}
	if prog.PlayCount != 2 {
		t.Fatalf("play count = %d, want 2 (two scrobbles)", prog.PlayCount)
	}
	if prog.Provenance != "import:"+testEventPluginID {
		t.Fatalf("provenance = %q, want import:%s", prog.Provenance, testEventPluginID)
	}
}

func TestMigrationWizardExternalIDWatchStateEndToEnd(t *testing.T) {
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(context.Background(), cfg, nil, []plugin.Bundle{testPathBundle()}, migrationTestHooks())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()
	ctx := context.Background()

	media, _, err := beamfall.store.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/archive/history.mkv",
		Title:              "History",
		Container:          "mkv",
		Codec:              "h264",
		Class:              store.MediaClassVideo,
		ContentFingerprint: "fp-external-state-app",
	})
	if err != nil {
		t.Fatalf("seed movie: %v", err)
	}
	if err := beamfall.store.AddMediaExternalID(ctx, media.ID, "fixture-catalog", "12345"); err != nil {
		t.Fatalf("seed external id: %v", err)
	}
	sourcePath := writeMigrationFixture(t, migrate.ForeignItem{
		ExternalIDs: []migrate.ExternalID{{Scheme: "fixture-catalog", Value: "12345"}},
		WatchState:  migrate.WatchState{Present: true, Watched: true, OccurredAt: time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)},
	})

	done := runMigrationImportForTest(t, beamfall, testPathPluginID, testExternalImporterID, sourcePath)
	if done.Progress.Current != 1 || done.Progress.Total != 1 {
		t.Fatalf("match-rate = %d of %d, want 1 of 1", done.Progress.Current, done.Progress.Total)
	}
	progress, ok, err := beamfall.store.PlaybackProgressFor(ctx, "profile-default", media.ID)
	if err != nil || !ok {
		t.Fatalf("playback progress: ok=%v err=%v", ok, err)
	}
	if !progress.Watched || progress.Provenance != "import:"+testPathPluginID {
		t.Fatalf("progress = watched %v provenance %q, want watched import:%s", progress.Watched, progress.Provenance, testPathPluginID)
	}
}

func TestMigrationWizardExternalIDEventsApplyPlayCountsEndToEnd(t *testing.T) {
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(context.Background(), cfg, nil, []plugin.Bundle{testEventBundle()}, migrationTestHooks())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()
	ctx := context.Background()

	media, _, err := beamfall.store.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/audio/boc/roygbiv.flac",
		Title:              "Roygbiv",
		Container:          "flac",
		Codec:              "flac",
		Class:              store.MediaClassAudio,
		ContentFingerprint: "fp-external-events-app",
	})
	if err != nil {
		t.Fatalf("seed track: %v", err)
	}
	if err := beamfall.store.AddMediaExternalID(ctx, media.ID, "fixture-record", "record-roygbiv"); err != nil {
		t.Fatalf("seed recording id: %v", err)
	}
	sourcePath := writeMigrationFixture(t,
		migrate.ForeignItem{
			ExternalIDs: []migrate.ExternalID{{Scheme: "fixture-record", Value: "record-roygbiv"}},
			Scrobble:    migrate.Scrobble{Present: true, Artist: "Boards of Canada", Track: "Roygbiv", PlayedAt: time.Unix(1700000000, 0).UTC()},
		},
		migrate.ForeignItem{
			ExternalIDs: []migrate.ExternalID{{Scheme: "fixture-record", Value: "record-roygbiv"}},
			Scrobble:    migrate.Scrobble{Present: true, Artist: "Boards of Canada", Track: "Roygbiv", PlayedAt: time.Unix(1700003600, 0).UTC()},
		},
	)

	done := runMigrationImportForTest(t, beamfall, testEventPluginID, testEventImporterID, sourcePath)
	if done.Progress.Current != 2 || done.Progress.Total != 2 {
		t.Fatalf("match-rate = %d of %d, want 2 of 2", done.Progress.Current, done.Progress.Total)
	}
	progress, ok, err := beamfall.store.PlaybackProgressFor(ctx, "profile-default", media.ID)
	if err != nil || !ok {
		t.Fatalf("playback progress: ok=%v err=%v", ok, err)
	}
	if progress.PlayCount != 2 || progress.Provenance != "import:"+testEventPluginID {
		t.Fatalf("progress = play_count %d provenance %q, want 2 import:%s", progress.PlayCount, progress.Provenance, testEventPluginID)
	}
}

func TestMigrationWizardMetadataFallbackEndToEnd(t *testing.T) {
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(context.Background(), cfg, nil, []plugin.Bundle{testEventBundle()}, migrationTestHooks())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()
	ctx := context.Background()

	media, _, err := beamfall.store.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/audio/boc/roygbiv.flac",
		Title:              "Roygbiv",
		Container:          "flac",
		Codec:              "flac",
		Class:              store.MediaClassAudio,
		ContentFingerprint: "fp-metadata-fallback-app",
	})
	if err != nil {
		t.Fatalf("seed track: %v", err)
	}
	if err := beamfall.store.UpsertEntity(ctx, store.Entity{ID: "entity-boc-fixture", LibraryID: "library-local", Kind: "artist", Title: "Boards of Canada"}); err != nil {
		t.Fatalf("seed artist entity: %v", err)
	}
	if err := beamfall.store.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: "entity-boc-fixture", Name: "appears-on", ToMediaID: media.ID}); err != nil {
		t.Fatalf("relate artist: %v", err)
	}
	sourcePath := writeMigrationFixture(t, migrate.ForeignItem{Scrobble: migrate.Scrobble{
		Present: true, Artist: "Boards of Canada", Track: "Roygbiv", Album: "Music Has the Right to Children", PlayedAt: time.Unix(1700000000, 0).UTC(),
	}})

	done := runMigrationImportForTest(t, beamfall, testEventPluginID, testEventImporterID, sourcePath)
	if done.Progress.Current != 1 || done.Progress.Total != 1 {
		t.Fatalf("match-rate = %d of %d, want 1 of 1", done.Progress.Current, done.Progress.Total)
	}
	progress, ok, err := beamfall.store.PlaybackProgressFor(ctx, "profile-default", media.ID)
	if err != nil || !ok {
		t.Fatalf("playback progress: ok=%v err=%v", ok, err)
	}
	if progress.PlayCount != 1 || progress.Provenance != "import:"+testEventPluginID {
		t.Fatalf("progress = play_count %d provenance %q, want 1 import:%s", progress.PlayCount, progress.Provenance, testEventPluginID)
	}
}

func runMigrationImportForTest(t *testing.T, beamfall *App, pluginID, importerID, sourcePath string) store.Job {
	t.Helper()
	ctx := context.Background()
	if err := beamfall.store.SetSessionAdminUntil(ctx, "session-manager", time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatalf("open admin session: %v", err)
	}
	mutation := []byte(`{"query":"mutation R($pluginID:String!,$importerID:String!,$sourcePath:String!){ runImport(pluginID:$pluginID,importerID:$importerID,sourcePath:$sourcePath){ id kind status } }","variables":{"pluginID":"` + pluginID + `","importerID":"` + importerID + `","sourcePath":"` + strings.ReplaceAll(sourcePath, `\`, `\\`) + `"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(string(mutation)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Beamfall-Session", "session-manager")
	req.Header.Set("X-Beamfall-Device", "device-mac")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")
	rec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("runImport status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Data struct {
			RunImport struct{ ID, Kind, Status string } `json:"runImport"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode runImport response: %v (%s)", err, rec.Body.String())
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("runImport errors = %#v", resp.Errors)
	}
	for i := 0; i < 32; i++ {
		ran, err := beamfall.jobs.RunOne(ctx, nil, time.Now().UTC())
		if err != nil {
			if errors.Is(err, store.ErrNoRunnableJob) {
				break
			}
			t.Fatalf("RunOne returned error: %v", err)
		}
		if ran.ID == store.JobID(resp.Data.RunImport.ID) {
			break
		}
	}
	done, err := beamfall.store.Job(ctx, store.JobID(resp.Data.RunImport.ID))
	if err != nil {
		t.Fatalf("read migration job back: %v", err)
	}
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("migration job status = %q, want completed: %s", done.Status, done.Error)
	}
	return done
}

// TestMigrationWizardPodcastOPMLImportReachesSubscriptionApplyEndToEnd is the
// MIG-3B end-to-end slice: runImport carries a target Library into the real
// migrationSourceResolver, the OPML parser yields subscription items, and the
// handler creates/follows Library subscriptions without storing feed secrets in
// the deprecated auth_header plane.
func TestMigrationWizardSubscriptionApplyEndToEnd(t *testing.T) {
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(context.Background(), cfg, nil, []plugin.Bundle{testSerialBundle()}, migrationTestHooks())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()
	ctx := context.Background()

	sourcePath := writeMigrationFixture(t, migrate.ForeignItem{Subscription: migrate.Subscription{
		Present: true, FeedURL: "https://feeds.example.com/daily.xml", Title: "Daily Show",
	}})

	if err := beamfall.store.SetSessionAdminUntil(ctx, "session-manager", time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatalf("open admin session: %v", err)
	}

	root := filepath.Join(t.TempDir(), "serial")
	mutation := []byte(`{"query":"mutation R($pluginID:String!,$importerID:String!,$sourcePath:String!,$libraryID:String!,$root:String!){ runImport(pluginID:$pluginID,importerID:$importerID,sourcePath:$sourcePath,libraryID:$libraryID,root:$root){ id kind status } }","variables":{"pluginID":"` + testSerialPluginID + `","importerID":"` + testSubscriptionID + `","sourcePath":"` + strings.ReplaceAll(sourcePath, `\`, `\\`) + `","libraryID":"library-local","root":"` + strings.ReplaceAll(root, `\`, `\\`) + `"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(string(mutation)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Beamfall-Session", "session-manager")
	req.Header.Set("X-Beamfall-Device", "device-mac")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")
	rec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("runImport status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Data struct {
			RunImport struct{ ID, Kind, Status string } `json:"runImport"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode runImport response: %v (%s)", err, rec.Body.String())
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("runImport errors = %#v", resp.Errors)
	}
	if resp.Data.RunImport.ID == "" || resp.Data.RunImport.Kind != string(migrate.JobKind) {
		t.Fatalf("runImport job = %#v, want a queued migration job", resp.Data.RunImport)
	}

	for i := 0; i < 32; i++ {
		ran, err := beamfall.jobs.RunOne(ctx, nil, time.Now().UTC())
		if err != nil {
			if errors.Is(err, store.ErrNoRunnableJob) {
				break
			}
			t.Fatalf("RunOne returned error: %v", err)
		}
		if ran.ID == store.JobID(resp.Data.RunImport.ID) {
			break
		}
	}

	done, err := beamfall.store.Job(ctx, store.JobID(resp.Data.RunImport.ID))
	if err != nil {
		t.Fatalf("read migration job back: %v", err)
	}
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("migration job status = %q, want completed: %s", done.Status, done.Error)
	}
	if done.Progress.Current != 1 || done.Progress.Total != 1 {
		t.Fatalf("match-rate = %d of %d, want 1 of 1", done.Progress.Current, done.Progress.Total)
	}
	subs, err := beamfall.store.Subscriptions(ctx, "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(subs))
	}
	if subs[0].FeedURL != "https://feeds.example.com/daily.xml" || subs[0].Root != root || subs[0].Title != "Daily Show" || subs[0].AuthRef != "" {
		t.Fatalf("subscription = %+v", subs[0])
	}
	followed, err := beamfall.store.SubscriptionFollowed(ctx, "profile-default", subs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !followed {
		t.Fatal("imported subscription was not followed by importing profile")
	}
}

func TestMigrationWizardItemStateReplaysEndToEnd(t *testing.T) {
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(context.Background(), cfg, nil, []plugin.Bundle{testSerialBundle()}, migrationTestHooks())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	media, _, err := beamfall.store.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: "/media/local/serial/entry.mp3", Title: "Serial Entry",
		Container: "mp3", Codec: "mp3", Class: store.MediaClassAudio, ContentFingerprint: "fp-item-state-0001", DurationSecondsHint: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := beamfall.store.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/serial.xml",
		Root:      "/media/local/serial",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := beamfall.store.MarkSubscriptionEntrySeen(ctx, sub.ID, "guid-entry", "/media/local/serial/entry.mp3", now); err != nil {
		t.Fatal(err)
	}

	sourcePath := writeMigrationFixture(t, migrate.ForeignItem{ItemState: migrate.ItemState{
		Present: true, FeedURL: "https://feeds.example.com/serial.xml", GUID: "guid-entry", HasProgress: true, Progress: 0.5, Starred: true,
	}})
	if err := beamfall.store.SetSessionAdminUntil(ctx, "session-manager", time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatalf("open admin session: %v", err)
	}

	mutation := []byte(`{"query":"mutation R($pluginID:String!,$importerID:String!,$sourcePath:String!,$libraryID:String!,$root:String!){ runImport(pluginID:$pluginID,importerID:$importerID,sourcePath:$sourcePath,libraryID:$libraryID,root:$root){ id kind status } }","variables":{"pluginID":"` + testSerialPluginID + `","importerID":"` + testItemStateImporterID + `","sourcePath":"` + strings.ReplaceAll(sourcePath, `\`, `\\`) + `","libraryID":"library-local","root":"/media/local/serial"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(string(mutation)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Beamfall-Session", "session-manager")
	req.Header.Set("X-Beamfall-Device", "device-mac")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")
	rec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("runImport status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Data struct {
			RunImport struct{ ID, Kind, Status string } `json:"runImport"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode runImport response: %v (%s)", err, rec.Body.String())
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("runImport errors = %#v", resp.Errors)
	}
	for i := 0; i < 32; i++ {
		ran, err := beamfall.jobs.RunOne(ctx, nil, time.Now().UTC())
		if err != nil {
			if errors.Is(err, store.ErrNoRunnableJob) {
				break
			}
			t.Fatalf("RunOne returned error: %v", err)
		}
		if ran.ID == store.JobID(resp.Data.RunImport.ID) {
			break
		}
	}
	done, err := beamfall.store.Job(ctx, store.JobID(resp.Data.RunImport.ID))
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("migration job status = %q, want completed: %s", done.Status, done.Error)
	}
	progress, ok, err := beamfall.store.PlaybackProgressFor(ctx, "profile-default", media.ID)
	if err != nil || !ok {
		t.Fatalf("PlaybackProgressFor: ok=%v err=%v", ok, err)
	}
	if progress.PositionSeconds != 100 || progress.DurationSeconds != 200 || progress.Provenance != "import:"+testSerialPluginID {
		t.Fatalf("progress = %+v, want 100/200 import:%s", progress, testSerialPluginID)
	}
	favorite, err := beamfall.store.MediaFavorited(ctx, "profile-default", media.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !favorite {
		t.Fatal("starred state was not imported as favorite")
	}
}

func TestMigrationWizardResumeItemStateReplaysEndToEnd(t *testing.T) {
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(context.Background(), cfg, nil, []plugin.Bundle{testSerialBundle()}, migrationTestHooks())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	media, _, err := beamfall.store.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: "/media/local/serial/resume.mp3", Title: "Resume Entry",
		Container: "mp3", Codec: "mp3", Class: store.MediaClassAudio, ContentFingerprint: "fp-resume-state-0001", DurationSecondsHint: 240,
	})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := beamfall.store.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/resume.xml",
		Root:      "/media/local/serial",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := beamfall.store.MarkSubscriptionEntrySeen(ctx, sub.ID, "guid-resume", "/media/local/serial/resume.mp3", now); err != nil {
		t.Fatal(err)
	}

	sourcePath := writeMigrationFixture(t, migrate.ForeignItem{ItemState: migrate.ItemState{
		Present: true, FeedURL: "https://feeds.example.com/resume.xml", GUID: "guid-resume", HasProgress: true, Progress: 0.75,
	}})
	if err := beamfall.store.SetSessionAdminUntil(ctx, "session-manager", time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatalf("open admin session: %v", err)
	}

	mutation := []byte(`{"query":"mutation R($pluginID:String!,$importerID:String!,$sourcePath:String!,$libraryID:String!,$root:String!){ runImport(pluginID:$pluginID,importerID:$importerID,sourcePath:$sourcePath,libraryID:$libraryID,root:$root){ id kind status } }","variables":{"pluginID":"` + testSerialPluginID + `","importerID":"` + testItemStateImporterID + `","sourcePath":"` + strings.ReplaceAll(sourcePath, `\`, `\\`) + `","libraryID":"library-local","root":"/media/local/serial"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(string(mutation)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Beamfall-Session", "session-manager")
	req.Header.Set("X-Beamfall-Device", "device-mac")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")
	rec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("runImport status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Data struct {
			RunImport struct{ ID, Kind, Status string } `json:"runImport"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode runImport response: %v (%s)", err, rec.Body.String())
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("runImport errors = %#v", resp.Errors)
	}
	for i := 0; i < 32; i++ {
		ran, err := beamfall.jobs.RunOne(ctx, nil, time.Now().UTC())
		if err != nil {
			if errors.Is(err, store.ErrNoRunnableJob) {
				break
			}
			t.Fatalf("RunOne returned error: %v", err)
		}
		if ran.ID == store.JobID(resp.Data.RunImport.ID) {
			break
		}
	}
	done, err := beamfall.store.Job(ctx, store.JobID(resp.Data.RunImport.ID))
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("migration job status = %q, want completed: %s", done.Status, done.Error)
	}
	progress, ok, err := beamfall.store.PlaybackProgressFor(ctx, "profile-default", media.ID)
	if err != nil || !ok {
		t.Fatalf("PlaybackProgressFor: ok=%v err=%v", ok, err)
	}
	if progress.Watched || progress.PositionSeconds != 180 || progress.DurationSeconds != 240 || progress.Provenance != "import:"+testSerialPluginID {
		t.Fatalf("progress = %+v, want resume 180/240 import:%s", progress, testSerialPluginID)
	}
}

func TestAppWiresWatcherToScanLibraryMutation(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()
	if err := beamfall.store.SetLibraryRoots(context.Background(), "library-local", []string{root}); err != nil {
		t.Fatalf("set library roots: %v", err)
	}

	// scanLibrary is a Console operation gated by the Admin Session
	// tier; open the window on the seeded manager session as a real Console
	// client would via verifyAdminSession.
	if err := beamfall.store.SetSessionAdminUntil(context.Background(), "session-manager", time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatalf("open admin session: %v", err)
	}

	mutation := []byte(`{"query":"mutation ScanLibrary($libraryID: String!, $root: String!) { scanLibrary(libraryID: $libraryID, root: $root) { id kind status } }","variables":{"libraryID":"library-local","root":"` + strings.ReplaceAll(root, `\`, `\\`) + `"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(string(mutation)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Beamfall-Session", "session-manager")
	req.Header.Set("X-Beamfall-Device", "device-mac")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")
	rec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("scanLibrary status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	now := time.Now().UTC()
	if _, err := beamfall.jobs.RunOne(context.Background(), nil, now); err != nil {
		t.Fatalf("RunOne returned error: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "new-clip.mp4"), []byte("new clip bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		jobs, err := beamfall.store.ListJobs(context.Background(), store.JobStatusQueued)
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range jobs {
			if job.Kind == scanpkg.JobKind {
				var payload struct {
					LibraryID string `json:"library_id"`
					Root      string `json:"root"`
				}
				if err := json.Unmarshal(job.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				if payload.LibraryID == "library-local" && payload.Root == root {
					return
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for watcher-enqueued scan job")
}

type appBlockingWatchBackend struct {
	close  sync.Once
	closed chan struct{}
	events chan fsnotify.Event
	errs   chan error
}

func (b *appBlockingWatchBackend) Add(string) error {
	<-b.closed
	return errors.New("backend closed")
}
func (b *appBlockingWatchBackend) Close() error {
	b.close.Do(func() { close(b.closed) })
	return nil
}
func (b *appBlockingWatchBackend) Events() <-chan fsnotify.Event { return b.events }
func (b *appBlockingWatchBackend) Errors() <-chan error          { return b.errs }

func TestRunServesWhilePersistedRootWatchAddBlocks(t *testing.T) {
	cfg := testConfig(t)
	cfg.Addr = "127.0.0.1:0"
	cfg.Discovery.Enabled = false
	cfg.SeedDevelopmentData = true
	root := t.TempDir()
	st, err := store.Open(context.Background(), cfg.DataDir, store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryRoots(context.Background(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	realNewWatcherService := newWatcherService
	newWatcherService = func(opts watcher.Options) *watcher.Service {
		opts.AddTimeout = 25 * time.Millisecond
		opts.NewWatcher = func() (watcher.Backend, error) {
			return &appBlockingWatchBackend{closed: make(chan struct{}), events: make(chan fsnotify.Event), errs: make(chan error)}, nil
		}
		return watcher.NewService(opts)
	}
	t.Cleanup(func() { newWatcherService = realNewWatcherService })

	beamfall, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- beamfall.Run(ctx) }()
	addrCtx, stopWaiting := context.WithTimeout(context.Background(), 500*time.Millisecond)
	addr := beamfall.Addr(addrCtx)
	stopWaiting()
	if addr == "" {
		cancel()
		t.Fatal("server did not bind while watcher Add blocked")
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	res, err := client.Get("http://" + addr + "/web/")
	if err != nil {
		cancel()
		t.Fatalf("GET /web/: %v", err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("GET /web/ status = %d, want 200", res.StatusCode)
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		cancel()
		t.Fatalf("listener and /web/ elapsed = %s, want <2s", elapsed)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && beamfall.watcher.Status("library-local").Error == "" {
		time.Sleep(5 * time.Millisecond)
	}
	if status := beamfall.watcher.Status("library-local"); status.Active || status.Available || !strings.Contains(status.Error, "timed out") {
		cancel()
		t.Fatalf("watcher status = %#v, want scheduled-scan degradation", status)
	}
	schedules, err := beamfall.store.ListSchedules(context.Background())
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if !scheduleEnabled(schedules, "scan:library-local") {
		cancel()
		t.Fatal("scan:library-local schedule is not armed")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunEventuallyWatchesHealthyPersistedRoot(t *testing.T) {
	cfg := testConfig(t)
	cfg.Addr = "127.0.0.1:0"
	cfg.Discovery.Enabled = false
	cfg.SeedDevelopmentData = true
	root := t.TempDir()
	st, err := store.Open(context.Background(), cfg.DataDir, store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryRoots(context.Background(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	beamfall, err := New(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- beamfall.Run(ctx) }()
	if addr := beamfall.Addr(ctx); addr == "" {
		cancel()
		t.Fatal("server did not bind")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status := beamfall.watcher.Status("library-local")
		if status.Active && status.Available {
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatal("healthy persisted root never became fully watched")
}

func TestWatcherOffSwitchSuppressesEveryObservePath(t *testing.T) {
	t.Setenv("BEAMFALL_WATCHER_DISABLED", "true")
	created := 0
	realNewWatcherService := newWatcherService
	newWatcherService = func(opts watcher.Options) *watcher.Service {
		opts.NewWatcher = func() (watcher.Backend, error) {
			created++
			return nil, errors.New("must not construct backend")
		}
		return watcher.NewService(opts)
	}
	t.Cleanup(func() { newWatcherService = realNewWatcherService })
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	status := beamfall.watcher.Observe("library-local", t.TempDir())
	if created != 0 || status.Active || status.Available || !strings.Contains(status.Error, "disabled") {
		t.Fatalf("disabled Observe: created=%d status=%#v", created, status)
	}
	if !scheduleEnabled(mustSchedules(t, beamfall.store), "scan:library-local") {
		t.Fatal("watcher off switch disabled scheduled scan")
	}
	if err := beamfall.Close(); err != nil {
		t.Fatal(err)
	}
}

func mustSchedules(t *testing.T, st *store.Store) []store.Schedule {
	t.Helper()
	schedules, err := st.ListSchedules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return schedules
}

func scheduleEnabled(schedules []store.Schedule, id store.ScheduleID) bool {
	for _, schedule := range schedules {
		if schedule.ID == id {
			return schedule.Enabled
		}
	}
	return false
}

type appFakeQueue struct {
	queued []store.UnmatchedItem
}

func (q *appFakeQueue) QueueUnmatched(_ context.Context, item store.UnmatchedItem) error {
	q.queued = append(q.queued, item)
	return nil
}

func (q *appFakeQueue) ClearUnmatched(_ context.Context, mediaID store.MediaID) (bool, error) {
	return false, nil
}

func TestScanIdentifierQueuesManualPolicyMatches(t *testing.T) {
	queue := &appFakeQueue{}
	registry, err := plugin.NewRegistry([]plugin.Bundle{testQueueBundle()})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	service := identify.NewService(nil, registry, nil)
	service.SetAutoStores(queue, nil)
	adapter := &scanIdentifier{service: service, registry: registry}

	err = adapter.AutoIdentify(context.Background(), "media-fixture", testQueuePluginID, "sha256:fixture",
		scanpkg.ParsedMedia{Title: "First Steps", Class: "home-video", Year: 2017})
	if err != nil {
		t.Fatalf("AutoIdentify: %v", err)
	}
	if len(queue.queued) != 1 {
		t.Fatalf("queued rows = %d, want 1", len(queue.queued))
	}
	row := queue.queued[0]
	if row.MediaID != "media-fixture" || row.PluginID != testQueuePluginID {
		t.Fatalf("queued row = %#v", row)
	}
	if row.ScraperID != "manual" || row.CandidateID != "" || row.CandidateTitle != "First Steps" {
		t.Fatalf("manual queue row = %#v", row)
	}
}

func TestScanIdentifierDoesNotQueueAutoPolicyScraperlessPlugin(t *testing.T) {
	const pluginID = "scraperless-stub"
	queue := &appFakeQueue{}
	bundle := testQueueBundle()
	bundle.ID = plugin.ID(pluginID)
	bundle.IdentificationPolicy = ""
	registry, err := plugin.NewRegistry([]plugin.Bundle{bundle})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	service := identify.NewService(nil, registry, nil)
	service.SetAutoStores(queue, nil)
	adapter := &scanIdentifier{service: service, registry: registry}

	err = adapter.AutoIdentify(context.Background(), "media-stub", pluginID, "sha256:stub",
		scanpkg.ParsedMedia{Title: "Stub Item", Class: "home-video", Year: 2020})
	if err != nil {
		t.Fatalf("AutoIdentify: %v", err)
	}
	if len(queue.queued) != 0 {
		t.Fatalf("queued rows = %d, want 0", len(queue.queued))
	}
}

func TestScanQueuedPluginEndToEndQueuesAndClearsUnmatched(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dir := filepath.Join(root, "Summer Picnic")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	clip := filepath.Join(dir, "clip01.mp4")
	if err := os.WriteFile(clip, []byte("fixture bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(clip, old, old); err != nil {
		t.Fatal(err)
	}
	st := storetest.Open(t)
	library, err := st.CreateLibrary(ctx, "Field Notes", false, testQueuePluginID, "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	registry, err := plugin.NewRegistry([]plugin.Bundle{testQueueBundle()})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	service := identify.NewService(nil, registry, nil)
	service.SetAutoStores(st, st)
	adapter := &scanIdentifier{service: service, registry: registry}

	result, err := scanpkg.Scan(ctx, st, nil, nil, map[string]scanpkg.FilenameParser{
		testQueuePluginID: fixtureFilenameParser{
			parsed: scanpkg.ParsedMedia{Title: "First Steps", Class: "home-video", Year: 2017},
			fields: []scanpkg.ParsedField{
				{Name: "source-folder", Text: "Summer Picnic"},
				{Name: "sidecar-title", Text: "First Steps"},
				{Name: "sidecar-summary", Text: "Walking across the kitchen"},
				{Name: "camera-make", Text: "Canon"},
				{Name: "camera-model", Text: "VIXIA HF G20"},
				{Name: "capture-date", Date: time.Date(2017, 8, 19, 0, 0, 0, 0, time.UTC)},
			},
		},
	}, nil, adapter, scanpkg.Request{
		LibraryID: library.ID,
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(result.Files))
	}
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}, Revealed: true}
	media, err := st.Media(ctx, result.Files[0].MediaID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if media.Title != "First Steps" || media.Year != 2017 {
		t.Fatalf("media = title %q year %d, want First Steps/2017", media.Title, media.Year)
	}
	fields, err := st.MediaFields(ctx, media.ID, filter)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]store.MediaField{}
	for _, field := range fields {
		got[field.Name] = field
	}
	if got["class"].Text != "home-video" || got["source-folder"].Text != "Summer Picnic" || got["camera-model"].Text != "VIXIA HF G20" {
		t.Fatalf("local hint fields = %#v", got)
	}
	if field := got["capture-date"]; field.Type != store.EntityFieldDate || field.Date.Format("2006-01-02") != "2017-08-19" {
		t.Fatalf("capture-date field = %#v, want date 2017-08-19", field)
	}

	items, err := st.UnmatchedItems(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("unmatched rows = %d, want 1", len(items))
	}
	row := items[0]
	if row.MediaID != media.ID || row.PluginID != testQueuePluginID || row.ScraperID != "manual" || row.CandidateTitle != "First Steps" {
		t.Fatalf("unmatched row = %#v", row)
	}
	if ok, err := st.ClearUnmatched(ctx, media.ID); err != nil || !ok {
		t.Fatalf("ClearUnmatched: ok=%v err=%v", ok, err)
	}
	if queued, err := st.IsUnmatched(ctx, media.ID); err != nil || queued {
		t.Fatalf("IsUnmatched after clear: queued=%v err=%v", queued, err)
	}
	if ok, err := st.SetMatchPinned(ctx, result.Files[0].ContentFingerprint, true); err != nil || !ok {
		t.Fatalf("SetMatchPinned: ok=%v err=%v", ok, err)
	}
	if _, err := scanpkg.Scan(ctx, st, nil, nil, map[string]scanpkg.FilenameParser{
		testQueuePluginID: fixtureFilenameParser{
			parsed: scanpkg.ParsedMedia{Title: "First Steps", Class: "home-video", Year: 2017},
		},
	}, nil, adapter, scanpkg.Request{
		LibraryID: library.ID,
		Root:      root,
	}); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if queued, err := st.IsUnmatched(ctx, media.ID); err != nil || queued {
		t.Fatalf("IsUnmatched after pinned rescan: queued=%v err=%v", queued, err)
	}
}

func hasSourceTitle(items []store.SourceItem, title string) bool {
	for _, item := range items {
		if item.Title == title {
			return true
		}
	}
	return false
}

func hasSurface(surfaces []reveal.Surface, id string) bool {
	for _, surface := range surfaces {
		if surface.ID == id {
			return true
		}
	}
	return false
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.StreamTokenKey = []byte("12345678901234567890123456789012")
	return cfg
}

type failingListener struct {
	acceptOnce sync.Once
	failOnce   sync.Once
	accepting  chan struct{}
	returned   chan struct{}
	fail       chan struct{}
}

func newFailingListener() *failingListener {
	return &failingListener{accepting: make(chan struct{}), returned: make(chan struct{}), fail: make(chan struct{})}
}

func (l *failingListener) Accept() (net.Conn, error) {
	l.acceptOnce.Do(func() { close(l.accepting) })
	<-l.fail
	close(l.returned)
	return nil, errors.New("test listener failed")
}

func (l *failingListener) Close() error {
	l.failNow()
	return nil
}

func (l *failingListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1}
}

func (l *failingListener) failNow() {
	l.failOnce.Do(func() { close(l.fail) })
}

func (l *failingListener) waitAccept(t *testing.T) {
	t.Helper()
	select {
	case <-l.accepting:
	case <-time.After(3 * time.Second):
		t.Fatal("test listener was never accepted")
	}
}

func (l *failingListener) waitReturned(t *testing.T) {
	t.Helper()
	select {
	case <-l.returned:
	case <-time.After(3 * time.Second):
		t.Fatal("test listener Accept did not return")
	}
}

type appStubRelay struct {
	endpoint string
	caFile   string
}

func startAppStubRelay(t *testing.T) appStubRelay {
	t.Helper()
	cert, certPEM := appRelayCert(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	caFile := filepath.Join(t.TempDir(), "relay-ca.pem")
	if err := os.WriteFile(caFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var hdr [4]byte
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			return
		}
		payload := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}
		ack := []byte(`{"type":"ack","ack":{"version":1,"carrier_id":"carrier-test"}}`)
		binary.BigEndian.PutUint32(hdr[:], uint32(len(ack)))
		if _, err := conn.Write(hdr[:]); err != nil {
			return
		}
		if _, err := conn.Write(ack); err != nil {
			return
		}
		cfg := yamux.DefaultConfig()
		cfg.LogOutput = io.Discard
		session, err := yamux.Server(conn, cfg)
		if err != nil {
			return
		}
		defer session.Close()
		<-done
	}()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return appStubRelay{endpoint: net.JoinHostPort("localhost", port), caFile: caFile}
}

func appRelayCert(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, certPEM
}

func writeDirectHTTPSCache(t *testing.T, cacheDir, domain string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		DNSNames:     []string{domain},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	var cache bytes.Buffer
	if err := pem.Encode(&cache, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(&cache, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, domain), cache.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestRemoteAccessProviderOmitsDirectEndpointUntilTLSActive(t *testing.T) {
	const domain = "home.example.com"
	cfg := testConfig(t)
	cfg.Discovery.Enabled = false
	cfg.RemoteAccess.DirectHTTPS.PublicDomain = domain
	cfg.RemoteAccess.DirectHTTPS.ACMETermsAccepted = true
	cfg.RemoteAccess.DirectHTTPS.ACMECacheDir = filepath.Join(cfg.DataDir, "acme")
	writeDirectHTTPSCache(t, cfg.RemoteAccess.DirectHTTPS.ACMECacheDir, domain)

	beamfall, err := New(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()

	provider := remoteAccessProvider{direct: beamfall.directHTTPS, directTLSActive: beamfall.directTLSActive}
	if got := provider.Endpoints(context.Background()); len(got) != 0 {
		t.Fatalf("inactive direct endpoints = %+v, want none", got)
	}
	beamfall.directTLSActive.Store(true)
	got := provider.Endpoints(context.Background())
	if len(got) != 1 || got[0].URL != "https://"+domain {
		t.Fatalf("active direct endpoints = %+v, want direct https", got)
	}
}

func TestRunDirectHTTPSServesTLSHealth(t *testing.T) {
	const domain = "home.example.com"
	cfg := testConfig(t)
	cfg.Addr = "127.0.0.1:0"
	cfg.Discovery.Enabled = false
	cfg.RemoteAccess.DirectHTTPS.PublicDomain = domain
	cfg.RemoteAccess.DirectHTTPS.ACMETermsAccepted = true
	cfg.RemoteAccess.DirectHTTPS.ACMECacheDir = filepath.Join(cfg.DataDir, "acme")
	certPEM := writeDirectHTTPSCache(t, cfg.RemoteAccess.DirectHTTPS.ACMECacheDir, domain)

	beamfall, err := New(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- beamfall.Run(ctx) }()

	addrCtx, stopWaiting := context.WithTimeout(context.Background(), 5*time.Second)
	addr := beamfall.Addr(addrCtx)
	stopWaiting()
	if addr == "" {
		cancel()
		t.Fatal("server did not bind a listener")
	}

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		cancel()
		t.Fatal("failed to append direct HTTPS cert")
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs:    roots,
			ServerName: domain,
			MinVersion: tls.VersionTLS12,
		}},
	}
	res, err := client.Get("https://" + addr + "/health")
	if err != nil {
		cancel()
		t.Fatalf("GET /health over direct HTTPS: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("GET /health status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

// NET-02 — a LAN-only server (no ACME) still serves TLS on its bound port with
// the persistent self-signed identity, so EVERY advertised scheme connects: the
// LAN candidate over cleartext http and the overlay candidate over https on the
// SAME port. The https path is validated by SPKI pin (the identity a client
// captures at LAN enrollment), proving the overlay endpoint is no longer a dead
// https endpoint and that the captured pin is the served identity.
func TestRunSelfSignedServesBothSchemesAndPinMatches(t *testing.T) {
	cfg := testConfig(t)
	cfg.Addr = "127.0.0.1:0"
	cfg.Discovery.Enabled = false // LAN-only: no direct HTTPS, no ACME

	beamfall, err := New(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	// The pin a LAN-enrolling client would capture is the self-signed identity's.
	wantPin, err := beamfall.selfSigned.ServerSPKIPin(context.Background())
	if err != nil {
		t.Fatalf("ServerSPKIPin: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- beamfall.Run(ctx) }()

	addrCtx, stopWaiting := context.WithTimeout(context.Background(), 5*time.Second)
	addr := beamfall.Addr(addrCtx)
	stopWaiting()
	if addr == "" {
		cancel()
		t.Fatal("server did not bind a listener")
	}

	// LAN scheme: cleartext http connects on the bound port.
	httpClient := &http.Client{Timeout: 5 * time.Second}
	resHTTP, err := httpClient.Get("http://" + addr + "/health")
	if err != nil {
		cancel()
		t.Fatalf("GET /health over http: %v", err)
	}
	resHTTP.Body.Close()
	if resHTTP.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("http /health status = %d, want 200", resHTTP.StatusCode)
	}

	// Overlay scheme: https connects on the SAME port, validated by SPKI pin.
	httpsClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // pin-based trust, not CA/name
			VerifyConnection: func(cs tls.ConnectionState) error {
				sum := sha256.Sum256(cs.PeerCertificates[0].RawSubjectPublicKeyInfo)
				got := "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
				if got != wantPin {
					return errors.New("served SPKI does not match enrollment pin")
				}
				return nil
			},
			MinVersion: tls.VersionTLS12,
		}},
	}
	resHTTPS, err := httpsClient.Get("https://" + addr + "/health")
	if err != nil {
		cancel()
		t.Fatalf("GET /health over https (pinned self-signed): %v", err)
	}
	resHTTPS.Body.Close()
	if resHTTPS.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("https /health status = %d, want 200", resHTTPS.StatusCode)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

// NET-02/03 (NGAUDIT-B12 GAP 3/B) — the acceptance criterion "every endpoint the
// server advertises is actually connectable with the advertised scheme, serving
// the identity a client pinned at LAN enrollment" is verified against the REAL
// advertised set, deterministically covering direct + overlay + relay (not just
// whatever host NICs happen to exist). With direct HTTPS configured — the GAP 1
// case that used to wholesale-replace carrierTLS with the ACME config — this
// proves the direct FQDN endpoint handshakes with the ACME cert AND that the
// overlay/relay/LAN candidates are served the pinned self-signed identity (not
// dead-ended on the ACME HostPolicy, and not the ACME SPKI the enrollment path
// used to hand back — GAP A).
//
// Non-vacuity (GAP B): the earlier version enumerated overlay only from real host
// interfaces, so on a runner with no overlay NIC and no relay it exercised direct
// alone and passed without ever touching a self-signed-backed endpoint. Here the
// relay path is configured (so ResolveEndpoints emits a self-hosted-relay URL) and
// a synthetic overlay candidate is injected, so an overlay AND a relay https URL
// are ALWAYS enumerated and handshaken. Each self-signed-backed endpoint's served
// pin is asserted equal to the pin captured through the REAL /api/enroll LAN
// response — tying GAP A shut: if enrollment ever hands back the ACME SPKI again,
// this test fails because overlay/relay serve the self-signed SPKI.
//
// Each advertised URL is dialed against the server's actual bound listener using
// the endpoint's advertised scheme and its host as SNI: http → cleartext, https
// with the public FQDN SNI → ACME cert, https with any other SNI (overlay/relay/
// LAN IP) → pinned self-signed SPKI. That decouples "is this host IP routable in
// the test sandbox" (irrelevant) from "does the advertised scheme + identity
// handshake" (the actual contract).
func TestAdvertisedEndpointsAreConnectableWithAdvertisedScheme(t *testing.T) {
	const domain = "home.example.com"
	const relayHost = "relay.example.com"
	const reviewKey = "review-demo-key-123456"
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	cfg.ServerName = "Advertised Endpoints"
	cfg.Discovery.Enabled = false
	cfg.StreamTokenKey = demoEnrollmentTestStreamTokenKey()
	cfg.SeedProfile = "demo"
	cfg.ReviewDemoEnrollment.Enabled = true
	cfg.ReviewDemoEnrollment.ProfileID = "profile-demo-default"
	writeReviewDemoSecret(t, cfg.DataDir, reviewKey, 0o600)
	// Direct HTTPS + ACME: pre-seed the autocert cache so the FQDN serves a real
	// ACME leaf whose SPKI differs from the self-signed identity.
	cfg.RemoteAccess.DirectHTTPS.PublicDomain = domain
	cfg.RemoteAccess.DirectHTTPS.ACMETermsAccepted = true
	cfg.RemoteAccess.DirectHTTPS.ACMECacheDir = filepath.Join(cfg.DataDir, "acme")
	acmeCertPEM := writeDirectHTTPSCache(t, cfg.RemoteAccess.DirectHTTPS.ACMECacheDir, domain)
	// Self-hosted relay configured so ResolveEndpoints deterministically emits a
	// self-hosted-relay https endpoint (no dependency on host NICs). The carrier
	// dials out in the background and never blocks/fails boot, so an unreachable
	// endpoint here is harmless to Run.
	cfg.RemoteAccess.Relay.Endpoint = "relay.example.com:7443"
	cfg.RemoteAccess.Relay.PublicHost = relayHost
	writeRelayToken(t, cfg.DataDir, "relay-carrier-token-abc123")

	beamfall, err := New(context.Background(), cfg, discardLogger(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	acmePin := spkiPinOfPEM(t, acmeCertPEM)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- beamfall.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run returned error: %v", err)
		}
	})

	addrCtx, stopWaiting := context.WithTimeout(context.Background(), 5*time.Second)
	addr := beamfall.Addr(addrCtx)
	stopWaiting()
	if addr == "" {
		t.Fatal("server did not bind a listener")
	}
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	lanPort, _ := strconv.Atoi(portStr)

	// The pin a native client actually captures at LAN enrollment, through the real
	// /api/enroll response (cleartext http on the bound port → requestIsLAN → the
	// genuine LAN-enrollment moment). This is the identity every self-signed-backed
	// advertised endpoint MUST present; GAP A regressed exactly this by handing back
	// the ACME SPKI when direct HTTPS was configured.
	enrolledPin := postEnroll(t, addr, reviewKey, "Advertised iPhone").ServerKeyPin
	if enrolledPin == "" {
		t.Fatal("LAN enrollment returned empty ServerKeyPin")
	}
	if enrolledPin == acmePin {
		t.Fatal("GAP A: LAN enrollment pinned the ACME identity, but overlay/relay serve self-signed")
	}

	// The advertised set the app actually publishes: direct (FQDN) active, relay
	// configured (deterministic), overlay resolved from host NICs. Build the
	// provider exactly as app.Run wires it.
	beamfall.directTLSActive.Store(true)
	provider := remoteAccessProvider{
		direct:           beamfall.directHTTPS,
		directTLSActive:  beamfall.directTLSActive,
		relay:            beamfall.relay,
		lanPort:          lanPort,
		overlayTLSActive: true,
	}
	endpoints := provider.Endpoints(context.Background())
	// Guarantee an overlay https candidate is enumerated regardless of the runner's
	// interfaces (GAP B non-vacuity): inject a synthetic overlay endpoint whose SNI
	// is an overlay-CGNAT IP. It resolves to the self-signed identity in the SNI
	// compose exactly like a real overlay address would, so the handshake exercised
	// is identical to production's overlay path.
	syntheticOverlay := graph.RemoteEndpoint{
		Kind: "overlay",
		URL:  buildTestURL("https", "100.64.0.5", lanPort),
	}
	endpoints = append(endpoints, syntheticOverlay)

	seen := map[string]bool{}
	for _, ep := range endpoints {
		u, err := url.Parse(ep.URL)
		if err != nil {
			t.Fatalf("advertised endpoint %+v has unparseable URL: %v", ep, err)
		}
		sni := u.Hostname()
		switch u.Scheme {
		case "http":
			// LAN cleartext must connect against the bound listener.
			res, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + addr + "/health")
			if err != nil {
				t.Fatalf("advertised %s (%s): http handshake failed: %v", ep.Kind, ep.URL, err)
			}
			res.Body.Close()
			if res.StatusCode != http.StatusOK {
				t.Fatalf("advertised %s (%s): http /health = %d, want 200", ep.Kind, ep.URL, res.StatusCode)
			}
		case "https":
			// Direct (public FQDN) serves the ACME cert for browser trust; every
			// other self-signed-backed endpoint MUST serve the enrolled pin.
			want := enrolledPin
			if ep.Kind == "direct" {
				want = acmePin
			}
			got := dialTLSServedPin(t, addr, sni)
			if got != want {
				t.Fatalf("advertised %s (%s, SNI %q): served pin = %q, want %q",
					ep.Kind, ep.URL, sni, got, want)
			}
		default:
			t.Fatalf("advertised endpoint %+v has unexpected scheme %q", ep, u.Scheme)
		}
		seen[string(ep.Kind)] = true
	}
	// Non-vacuous: the direct (ACME) endpoint AND at least one self-signed-backed
	// remote endpoint (overlay) AND the relay endpoint must all have been exercised.
	for _, kind := range []string{"direct", "overlay", "self-hosted-relay"} {
		if !seen[kind] {
			t.Fatalf("advertised %q endpoint was not exercised; test is vacuous for that path", kind)
		}
	}
}

// buildTestURL forms a base URL from scheme/host/port, bracketing IPv6 hosts —
// the same shape ResolveEndpoints emits, used to inject a deterministic overlay
// candidate in the advertised-endpoints conformance test.
func buildTestURL(scheme, host string, port int) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, port)
}

// writeRelayToken writes the 0600 relay carrier token secret under the data dir so
// a configured relay can construct its RelayClient.
func writeRelayToken(t *testing.T, dataDir, token string) {
	t.Helper()
	dir := filepath.Join(dataDir, "secrets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "relay-token"), []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
}

// dialTLSServedPin completes a TLS handshake against addr with the given SNI and
// returns the served leaf's SPKI pin (HPKP form). Pin-based, not CA/name trust.
func dialTLSServedPin(t *testing.T, addr, sni string) string {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, &tls.Config{
		ServerName:         sni,
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("TLS dial addr=%s SNI=%q: %v", addr, sni, err)
	}
	defer conn.Close()
	leaf := conn.ConnectionState().PeerCertificates[0]
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

// spkiPinOfPEM returns the HPKP-form SPKI pin of the first CERTIFICATE in pemBytes.
func spkiPinOfPEM(t *testing.T, pemBytes []byte) string {
	t.Helper()
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatal("no PEM certificate to pin")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

// NET-02 (NGAUDIT-B12 GAP 4) — the SPKI a client pins is captured through the
// REAL LAN enrollment response of the running self-signed app (the actual
// /api/enroll path, not selfSigned.ServerSPKIPin directly, not a fake provider).
// The captured pin then drives a pinned reconnect: the matching pin succeeds and
// a deliberately-mismatched pin fails closed. This proves the enrollment path
// hands back the identity the server actually serves, closing the pin loop
// end-to-end.
func TestLANEnrollmentPinCaptureAndPinnedReconnect(t *testing.T) {
	const reviewKey = "review-demo-key-123456"
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	cfg.ServerName = "Pin Capture"
	cfg.Discovery.Enabled = false // LAN-only: self-signed identity backs TLS
	cfg.StreamTokenKey = demoEnrollmentTestStreamTokenKey()
	cfg.SeedProfile = "demo"
	cfg.ReviewDemoEnrollment.Enabled = true
	cfg.ReviewDemoEnrollment.ProfileID = "profile-demo-default"
	writeReviewDemoSecret(t, cfg.DataDir, reviewKey, 0o600)

	addr := bootTestApp(t, cfg)

	// Capture the pin through the real enrollment response. postEnroll POSTs to
	// http://127.0.0.1:<port>, so requestIsLAN(r) is true and serverKeyPin is
	// populated — the genuine LAN-enrollment moment.
	enroll := postEnroll(t, addr, reviewKey, "Pin Capture iPhone")
	capturedPin := enroll.ServerKeyPin
	if capturedPin == "" {
		t.Fatal("LAN enrollment returned empty ServerKeyPin; client would have nothing to pin")
	}

	// Pinned reconnect over https (overlay scheme) on the same port: the matching
	// captured pin succeeds.
	pinnedClient := func(expectPin string) *http.Client {
		return &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // pin-based trust
				VerifyConnection: func(cs tls.ConnectionState) error {
					sum := sha256.Sum256(cs.PeerCertificates[0].RawSubjectPublicKeyInfo)
					got := "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
					if got != expectPin {
						return errors.New("served SPKI does not match pinned value")
					}
					return nil
				},
				MinVersion: tls.VersionTLS12,
			}},
		}
	}

	res, err := pinnedClient(capturedPin).Get("https://" + addr + "/health")
	if err != nil {
		t.Fatalf("pinned reconnect with captured pin failed: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("pinned reconnect /health = %d, want 200", res.StatusCode)
	}

	// A mismatched pin must fail closed — the enforcement that makes the pin
	// meaningful. Flip one base64 char of the captured pin.
	badPin := mutatePin(capturedPin)
	if badPin == capturedPin {
		t.Fatal("failed to construct a distinct mismatched pin")
	}
	if _, err := pinnedClient(badPin).Get("https://" + addr + "/health"); err == nil {
		t.Fatal("reconnect with a mismatched pin SUCCEEDED; pinning is not fail-closed")
	}
}

// mutatePin returns capturedPin with its final base64 SPKI character changed, so
// the resulting pin is well-formed but does NOT match the served identity.
func mutatePin(pin string) string {
	if pin == "" {
		return pin
	}
	last := pin[len(pin)-1]
	repl := byte('A')
	if last == 'A' {
		repl = 'B'
	}
	return pin[:len(pin)-1] + string(repl)
}

func TestRunStopsJobsBeforeClosingStore(t *testing.T) {
	cfg := testConfig(t)
	// Occupy the port so ListenAndServe fails immediately — the historical
	// trigger for jobs continuing against a closed store.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	cfg.Addr = listener.Addr().String()

	beamfall, err := New(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	if err := beamfall.Run(context.Background()); err == nil {
		t.Fatal("Run succeeded on an occupied port")
	}

	// Run must have stopped the jobs goroutine and closed the store; any later
	// store operation reports ErrClosed instead of silently succeeding.
	if _, err := beamfall.store.Session(context.Background(), "session-default"); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("store read after Run err = %v, want store.ErrClosed", err)
	}
	if _, err := beamfall.store.EnqueueJob(context.Background(), store.Job{Kind: "scan"}); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("store mutation after Run err = %v, want store.ErrClosed", err)
	}
}

func TestRunStopsRelayCarrierOnHTTPServerError(t *testing.T) {
	oldListenTCP := listenTCP
	listener := newFailingListener()
	listenTCP = func(network, address string) (net.Listener, error) {
		return listener, nil
	}
	defer func() { listenTCP = oldListenTCP }()

	cfg := testConfig(t)
	cfg.Discovery.Enabled = false
	cfg.RemoteAccess.DirectHTTPS.PublicDomain = "example.com"
	cfg.RemoteAccess.DirectHTTPS.ACMETermsAccepted = true
	cfg.RemoteAccess.DirectHTTPS.ACMECacheDir = filepath.Join(cfg.DataDir, "acme")
	relay := startAppStubRelay(t)
	cfg.RemoteAccess.Relay.Endpoint = relay.endpoint
	cfg.RemoteAccess.Relay.PublicHost = "remote.example.com"
	cfg.RemoteAccess.Relay.CAFile = relay.caFile
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.DataDir, "secrets", "relay-token"), []byte("carrier-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	beamfall, err := New(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- beamfall.Run(context.Background()) }()

	deadline := time.After(3 * time.Second)
	for {
		state, _ := beamfall.relay.Status()
		if state == "connected" {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("relay never connected; last state = %q", state)
		case <-time.After(10 * time.Millisecond):
		}
	}

	listener.waitAccept(t)
	listener.failNow()
	listener.waitReturned(t)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run returned nil; want listener error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after listener failure")
	}
	if state, _ := beamfall.relay.Status(); state == "connected" {
		t.Fatal("relay carrier still connected after Run returned")
	}
}

func TestRunJobsUsesConsoleConfiguredWorkerPool(t *testing.T) {
	cfg := testConfig(t)
	cfg.GenerationWorkers = 1
	cfg.BackgroundCPUBudget = 1
	ctx := context.Background()

	seed, err := store.Open(ctx, cfg.DataDir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	workers := 2
	if _, err := seed.SetConsoleConfig(ctx, store.ConsoleConfigPatch{BackgroundWorkers: &workers}, time.Unix(1700, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	beamfall, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()
	if got := beamfall.governor.GenerationWorkers(); got != 2 {
		t.Fatalf("governor workers = %d, want persisted Console value 2", got)
	}

	const kind store.JobKind = "ops-worker-pool-test"
	entered := make(chan store.JobID, 2)
	release := make(chan struct{})
	beamfall.jobs.Register(kind, func(ctx context.Context, job store.Job) (jobs.Result, error) {
		entered <- job.ID
		select {
		case <-release:
			return jobs.Result{}, nil
		case <-ctx.Done():
			return jobs.Result{}, ctx.Err()
		}
	})
	for _, id := range []store.JobID{"ops-worker-a", "ops-worker-b"} {
		if _, err := beamfall.store.EnqueueJob(ctx, store.Job{
			ID:        id,
			Kind:      kind,
			Class:     store.JobClassBackground,
			Priority:  100,
			CreatedAt: time.Unix(1800, 0).UTC(),
			RunAfter:  time.Unix(1800, 0).UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		beamfall.runJobs(runCtx)
	}()
	defer func() {
		close(release)
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("runJobs did not stop")
		}
	}()

	claimed := map[store.JobID]bool{}
	deadline := time.After(4 * time.Second)
	for len(claimed) < 2 {
		select {
		case id := <-entered:
			claimed[id] = true
		case <-deadline:
			t.Fatalf("claimed jobs = %v, want both jobs running under worker pool", claimed)
		}
	}
	running, err := beamfall.store.ListJobs(ctx, store.JobStatusRunning)
	if err != nil {
		t.Fatal(err)
	}
	runningIDs := map[store.JobID]bool{}
	for _, job := range running {
		if job.Kind == kind {
			runningIDs[job.ID] = true
		}
	}
	if !runningIDs["ops-worker-a"] || !runningIDs["ops-worker-b"] {
		t.Fatalf("running ops jobs = %v, want both queued jobs running concurrently", runningIDs)
	}
}

func TestNewComposesGovernedPhotoScanWithArtworkService(t *testing.T) {
	cfg := testConfig(t)
	cfg.GenerationWorkers = 3
	original := scanHandlerWithWorkerGovernor
	defer func() { scanHandlerWithWorkerGovernor = original }()
	var capturedGovernor scanpkg.WorkerGovernor
	var capturedDeriver scanpkg.ImageDeriver
	scanHandlerWithWorkerGovernor = func(st *store.Store, prober scanpkg.Prober, grabber scanpkg.FrameGrabber, parsers map[string]scanpkg.FilenameParser, sidecars []string, identifier scanpkg.Identifier, extractors map[string][]scanpkg.LocalExtractor, hydrators map[string]scanpkg.MetadataHydrator, gov scanpkg.WorkerGovernor, derivers ...scanpkg.ImageDeriver) jobs.Handler {
		capturedGovernor = gov
		if len(derivers) > 0 {
			capturedDeriver = derivers[0]
		}
		return original(st, prober, grabber, parsers, sidecars, identifier, extractors, hydrators, gov, derivers...)
	}
	beamfall, err := New(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer beamfall.Close()
	if capturedGovernor == nil || capturedGovernor.GenerationWorkers() != 3 || capturedGovernor != beamfall.governor {
		t.Fatalf("scan governor = %#v, app governor = %#v", capturedGovernor, beamfall.governor)
	}
	if service, ok := capturedDeriver.(*artwork.Service); !ok || service == nil {
		t.Fatalf("scan image deriver = %T, want *artwork.Service", capturedDeriver)
	}
}

func TestAppSchedulesMaintenanceWithoutBootHerd(t *testing.T) {
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()

	schedules, err := beamfall.store.ListSchedules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"scan:library-local":   true,
		"scan:library-archive": true,
		"retention-sweep":      true,
		"database-backup":      true,
		"offline-pack-expire":  true,
		"subscription-poll":    true,
		"decode-verify":        true,
	}
	// The decode-verify Schedule seeds weekly on Sunday at 05:00 and is
	// disableable like every other seeded row.
	var decodeVerify *store.Schedule
	for i, schedule := range schedules {
		delete(want, string(schedule.ID))
		if schedule.ID == "decode-verify" {
			decodeVerify = &schedules[i]
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing schedules: %#v from %#v", want, schedules)
	}
	if decodeVerify == nil {
		t.Fatal("decode-verify schedule not seeded")
	}
	if got := decodeVerify.Trigger; got.Kind != store.ScheduleTriggerWeekly ||
		got.Weekday != int(time.Sunday) || got.Hour != 5 || got.Minute != 0 {
		t.Fatalf("decode-verify trigger = %#v, want Weekly(Sun, 05:00)", got)
	}
	if !decodeVerify.Enabled {
		t.Fatal("decode-verify schedule should seed enabled")
	}
	// Disableable: flip enabled off and confirm it persists (the safe direction —
	// disabling only stops decode-verify from running, ADR-0032 §5).
	disabled := false
	updated, err := beamfall.store.UpdateSchedule(context.Background(), "decode-verify", nil, &disabled)
	if err != nil {
		t.Fatalf("disable decode-verify: %v", err)
	}
	if updated.Enabled {
		t.Fatal("decode-verify schedule should be disableable")
	}
	jobs, err := beamfall.store.ListJobs(context.Background(), store.JobStatusQueued)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.Kind == ingest.PollJobKind {
			t.Fatalf("subscription poll was boot-enqueued outside scheduler: %#v", job)
		}
	}
}

func TestScanScheduleSpecAbortsWhenSchedulerContextCanceled(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	library, err := st.CreateLibrary(ctx, "Local", false, "", "profile-default")
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	if err := st.SetLibraryRoots(ctx, library.ID, []string{t.TempDir()}); err != nil {
		t.Fatalf("set library roots: %v", err)
	}
	spec := scanScheduleSpec(st, library.ID)
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	jobs, err := spec.BuildJobs(canceledCtx, time.Now())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("scan schedule build = %v, want context canceled", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("jobs = %d, want 0", len(jobs))
	}
}

func TestScanScheduleSpecBuildsSharedScanJob(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	library, err := st.CreateLibrary(ctx, "Local", false, "", "profile-default")
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	root := t.TempDir()
	if err := st.SetLibraryRoots(ctx, library.ID, []string{root}); err != nil {
		t.Fatalf("set library roots: %v", err)
	}
	now := time.Now().UTC()
	want, err := newScanJob(library.ID, root, now)
	if err != nil {
		t.Fatalf("new scan job: %v", err)
	}
	got, err := scanScheduleSpec(st, library.ID).BuildJobs(ctx, now)
	if err != nil {
		t.Fatalf("scan schedule build: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("jobs = %d, want 1", len(got))
	}
	if got[0].Kind != want.Kind || got[0].DedupeKey != want.DedupeKey || !bytes.Equal(got[0].Payload, want.Payload) {
		t.Fatalf("scheduled scan job = %#v payload %s, want %#v payload %s", got[0], got[0].Payload, want, want.Payload)
	}
}

func TestRetentionRunsOnlyAsScheduledJob(t *testing.T) {
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.DataDir, store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.TrashMedia(ctx, "media-alpha", store.LibraryFilter{Revealed: true, AllLibraries: true}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	storetest.BackdateTrashedMedia(t, cfg.DataDir, "media-alpha", time.Now().Add(-2*store.TrashRetention))

	beamfall, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()
	trashed, err := beamfall.store.TrashedMediaList(ctx, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(trashed) != 1 {
		t.Fatalf("boot retention purge ran directly; trash = %#v, want one old item", trashed)
	}
	now := time.Now().UTC()
	if _, err := beamfall.store.EnqueueJob(ctx, store.Job{Kind: retentionSweepJobKind, DedupeKey: string(retentionSweepJobKind), CreatedAt: now, RunAfter: now}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		job, err := beamfall.jobs.RunOne(ctx, nil, now)
		if err != nil {
			t.Fatalf("RunOne returned error: %v", err)
		}
		if job.Kind == retentionSweepJobKind {
			break
		}
	}
	trashed, err = beamfall.store.TrashedMediaList(ctx, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(trashed) != 0 {
		t.Fatalf("retention job left trash = %#v, want empty", trashed)
	}
}

func TestEventsCompactionConfigAndAuditSurviveRestart(t *testing.T) {
	cfg := testConfig(t)
	ctx := context.Background()
	beamfall, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	now := time.Now().UTC()
	retentionDays := 7
	if _, err := beamfall.store.SetConsoleConfig(ctx, store.ConsoleConfigPatch{EventsRetentionDays: &retentionDays}, now); err != nil {
		t.Fatal(err)
	}
	job, err := beamfall.store.EnqueueJob(ctx, store.Job{Kind: eventsCompactionJobKind, DedupeKey: string(eventsCompactionJobKind), CreatedAt: now, RunAfter: now})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		ran, err := beamfall.jobs.RunOne(ctx, nil, now)
		if err != nil {
			t.Fatalf("RunOne returned error: %v", err)
		}
		if ran.ID == job.ID {
			break
		}
	}
	if err := beamfall.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := store.Open(ctx, cfg.DataDir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	configAfterRestart, err := reopened.ConsoleConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if configAfterRestart.EventsRetentionDays != retentionDays {
		t.Fatalf("EventsRetentionDays after restart = %d, want %d", configAfterRestart.EventsRetentionDays, retentionDays)
	}
	events, err := reopened.ListJobEvents(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type != "compaction" {
			continue
		}
		var payload struct {
			RetentionDays int `json:"retentionDays"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.RetentionDays == retentionDays {
			return
		}
	}
	t.Fatalf("compaction audit event missing after restart: %#v", events)
}

func TestOfflinePackExpiryRunsAsScheduledJob(t *testing.T) {
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	ctx := context.Background()
	beamfall, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()

	now := time.Now().UTC()
	expiredAt := now.Add(-time.Hour)
	pack, err := beamfall.store.CreateOfflinePack(ctx, store.OfflinePack{
		ProfileID:  "profile-default",
		TargetKind: offlinepack.TargetKindMedia,
		TargetRef:  "media-alpha",
		State:      store.OfflinePackReady,
		ExpiresAt:  &expiredAt,
	}, []store.OfflinePackItem{{MediaID: "media-alpha", SortOrder: 1}}, now.Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("CreateOfflinePack: %v", err)
	}
	if _, err := beamfall.store.EnqueueJob(ctx, store.Job{Kind: offlinepack.ExpireJobKind, DedupeKey: string(offlinepack.ExpireJobKind), CreatedAt: now, RunAfter: now}); err != nil {
		t.Fatal(err)
	}
	ranExpire := false
	for i := 0; i < 10; i++ {
		job, err := beamfall.jobs.RunOne(ctx, nil, now)
		if err != nil {
			t.Fatalf("RunOne returned error: %v", err)
		}
		if job.Kind == offlinepack.ExpireJobKind {
			ranExpire = true
			break
		}
	}
	if !ranExpire {
		t.Fatalf("did not run %q", offlinepack.ExpireJobKind)
	}
	got, err := beamfall.store.OfflinePackByID(ctx, pack.ID, "profile-default")
	if err != nil {
		t.Fatalf("OfflinePackByID: %v", err)
	}
	if got.State != store.OfflinePackExpired {
		t.Fatalf("state = %q, want expired", got.State)
	}
}

func retentionFixture(t *testing.T, st *store.Store) (store.Subscription, store.MediaID, store.MediaID) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	sub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/retention-fixture",
		Root:      "/media/local/subscription",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	olderPath := "/media/local/subscription/old.mp3"
	newerPath := "/media/local/subscription/new.mp3"
	older, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               olderPath,
		Title:              "Old Entry",
		Container:          "mp3",
		Codec:              "mp3",
		ContentFingerprint: "sha256:retention-old",
		Class:              store.MediaClassAudio,
	})
	if err != nil {
		t.Fatal(err)
	}
	newer, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               newerPath,
		Title:              "New Entry",
		Container:          "mp3",
		Codec:              "mp3",
		ContentFingerprint: "sha256:retention-new",
		Class:              store.MediaClassAudio,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSubscriptionEntrySeen(ctx, sub.ID, "guid-old", olderPath, now); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSubscriptionEntrySeen(ctx, sub.ID, "guid-new", newerPath, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	return sub, older.ID, newer.ID
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestLibEncryptDBAdapterBindsLibraryIdentity proves the encrypted-library DB
// seal/unseal adapter binds the ciphertext to the owning library's identity:
// the sealed DB reopens only under the library that
// wrote it, never under another library's context, even with the same paths.
func TestLibEncryptDBAdapterBindsLibraryIdentity(t *testing.T) {
	ctx := context.Background()
	manager := libencrypt.NewManagerWithParams(memSecretStore{}, libencrypt.TestArgon2Params)
	if _, err := manager.Provision("library-a", libencrypt.UnsealModeAuto, ""); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	adapter := appLibEncryptDBCipher{manager: manager}
	dir := t.TempDir()
	working := filepath.Join(dir, "working.db")
	ciphertext := filepath.Join(dir, "encrypted_library.db.libencrypt")
	if err := os.WriteFile(working, []byte("sqlite-ish plaintext rows"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SealDBFile(ctx, "library-a", working, ciphertext); err != nil {
		t.Fatalf("SealDBFile: %v", err)
	}
	restored := filepath.Join(dir, "restored.db")
	if err := adapter.UnsealDBFile(ctx, "library-a", ciphertext, restored); err != nil {
		t.Fatalf("UnsealDBFile: %v", err)
	}
	got, err := os.ReadFile(restored) //nolint:gosec // test-owned temp path
	if err != nil || string(got) != "sqlite-ish plaintext rows" {
		t.Fatalf("restored DB = %q, %v", got, err)
	}
	// Same key, wrong identity: presenting another library's DB context under
	// the SAME DEK must fail — the identity is bound into the object key
	// derivation, not just implied by which DEK is used.
	if err := manager.DecryptFile("library-a", ciphertext, filepath.Join(dir, "swap.db"), libencrypt.LibraryDBObjectContext("library-b")); err == nil {
		t.Fatal("encrypted DB opened under another library's identity")
	}
	// And a media-bytes-class context never opens a DB-class object.
	if err := manager.DecryptFile("library-a", ciphertext, filepath.Join(dir, "class.db"), libencrypt.MediaObjectContext("library-a", "sha256:whatever")); err == nil {
		t.Fatal("encrypted DB opened under a different object class")
	}
}

// TestGovernedGenerationYieldsOnTranscodePreemption is the durable-yield proof
// for background generation preempted by a realtime transcode.
// A barrier holds the generation handler mid-run until a transcode begins; the
// handler then observes its lease context cancelled and returns its checkpoint,
// and the wrapper maps the preemption to jobs.ErrYielded with the checkpoint
// preserved and a bounded retry — never a completion or a failure.
func TestGovernedGenerationYieldsOnTranscodePreemption(t *testing.T) {
	g := governor.New()
	started := make(chan struct{})
	checkpoint := []byte(`{"cursor":"preempted"}`)
	handler := governedGeneration(g, func(ctx context.Context, _ store.Job) (jobs.Result, error) {
		close(started)
		<-ctx.Done() // block until the transcode preempts this generation
		return jobs.Result{Checkpoint: checkpoint}, ctx.Err()
	})

	type outcome struct {
		result jobs.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := handler(context.Background(), store.Job{ID: "gen-1", Checkpoint: []byte(`{"cursor":"old"}`)})
		done <- outcome{result, err}
	}()

	<-started          // the generation lease is held and the handler is running
	g.BeginTranscode() // a realtime transcode preempts it
	got := <-done

	if !errors.Is(got.err, jobs.ErrYielded) {
		t.Fatalf("err = %v, want jobs.ErrYielded (preemption must yield, never fail)", got.err)
	}
	if string(got.result.Checkpoint) != string(checkpoint) {
		t.Fatalf("Checkpoint = %s, want the handler's mid-run checkpoint preserved", got.result.Checkpoint)
	}
	if got.result.RunAfter.IsZero() {
		t.Fatal("RunAfter not set — a preempted generation must re-queue with a bounded retry")
	}
}

// TestGovernedGenerationThrottleYieldsWithoutRunning proves that when a
// transcode is already active the generation handler is never entered: the
// governor refuses admission and the wrapper yields with the job's own
// checkpoint and a bounded retry.
func TestGovernedGenerationThrottleYieldsWithoutRunning(t *testing.T) {
	g := governor.New()
	g.BeginTranscode() // a transcode already holds the CPU

	ran := false
	handler := governedGeneration(g, func(context.Context, store.Job) (jobs.Result, error) {
		ran = true
		return jobs.Result{}, nil
	})
	result, err := handler(context.Background(), store.Job{ID: "gen-2", Checkpoint: []byte(`{"cursor":"keep"}`)})
	if ran {
		t.Fatal("generation handler ran while a transcode was active — admission not gated")
	}
	if !errors.Is(err, jobs.ErrYielded) {
		t.Fatalf("err = %v, want jobs.ErrYielded", err)
	}
	if string(result.Checkpoint) != `{"cursor":"keep"}` {
		t.Fatalf("Checkpoint = %s, want the job's checkpoint carried forward", result.Checkpoint)
	}
	if result.RunAfter.IsZero() {
		t.Fatal("RunAfter not set on a throttled generation")
	}
}

// TestGovernedGenerationCompletesWhenUnpreempted proves the pass-through path: a
// generation that runs to completion with no transcode preemption returns its
// handler's result and error verbatim, releasing its budget slot.
func TestGovernedGenerationCompletesWhenUnpreempted(t *testing.T) {
	g := governor.New()
	want := jobs.Result{Checkpoint: []byte(`{"cursor":"done"}`)}
	handler := governedGeneration(g, func(context.Context, store.Job) (jobs.Result, error) {
		return want, nil
	})
	result, err := handler(context.Background(), store.Job{ID: "gen-3"})
	if err != nil {
		t.Fatalf("err = %v, want nil (completion passes through)", err)
	}
	if string(result.Checkpoint) != string(want.Checkpoint) {
		t.Fatalf("Checkpoint = %s, want %s", result.Checkpoint, want.Checkpoint)
	}
	if !g.GenerationAllowed() {
		t.Fatal("generation slot not released after the handler completed")
	}

	// An unrelated handler error still surfaces as a failure, not a yield.
	boom := errors.New("boom")
	failHandler := governedGeneration(g, func(context.Context, store.Job) (jobs.Result, error) {
		return jobs.Result{}, boom
	})
	if _, err := failHandler(context.Background(), store.Job{ID: "gen-4"}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the handler's real error preserved", err)
	}
}

// The subtitle offset-nudge sweep is both SEEDED as a Schedule and
// REGISTERED as a job handler at composition. Either half missing leaves nudge
// rows accumulating forever — a seeded schedule with no handler fails every run,
// and a handler with no schedule is never triggered — so both are asserted here
// rather than trusting the wiring by inspection.
func TestSubtitleOffsetPruneIsScheduledAndRegistered(t *testing.T) {
	cfg := testConfig(t)
	ctx := context.Background()
	beamfall, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()

	schedules, err := beamfall.store.ListSchedules(ctx)
	if err != nil {
		t.Fatalf("ListSchedules: %v", err)
	}
	var seeded *store.Schedule
	for i := range schedules {
		if schedules[i].JobKind == jobs.SubtitleOffsetPruneJobKind {
			seeded = &schedules[i]
			break
		}
	}
	if seeded == nil {
		t.Fatalf("no Schedule seeded for %q; the sweep would never run", jobs.SubtitleOffsetPruneJobKind)
	}
	if !seeded.Enabled {
		t.Fatalf("Schedule %q is seeded disabled", seeded.ID)
	}

	// The handler is registered: enqueuing the kind runs to COMPLETION. An
	// unregistered kind fails with jobs.ErrNoHandler instead.
	now := time.Now().UTC()
	enqueued, err := beamfall.store.EnqueueJob(ctx, store.Job{
		Kind:      jobs.SubtitleOffsetPruneJobKind,
		DedupeKey: string(jobs.SubtitleOffsetPruneJobKind),
		CreatedAt: now,
		RunAfter:  now,
	})
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	for range 10 {
		ran, err := beamfall.jobs.RunOne(ctx, nil, now)
		if err != nil {
			t.Fatalf("RunOne returned error: %v", err)
		}
		if ran.ID == enqueued.ID {
			return
		}
	}
	t.Fatalf("job %q never ran", jobs.SubtitleOffsetPruneJobKind)
}

func TestSubtitleSidecarGuardMapsOversizeRejection(t *testing.T) {
	err := (subtitleSidecarGuard{}).CheckSize("srt", subtitleingest.MaxTextBytes+1)
	if !errors.Is(err, jobs.ErrSubtitleSidecarTooLarge) {
		t.Fatalf("CheckSize error = %v, want ErrSubtitleSidecarTooLarge", err)
	}
}
