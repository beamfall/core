package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/migrate"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/foreignimport/foreignimporttest"
	"github.com/beamfall/core/internal/store/storetest"
	books "github.com/beamfall/plugin-audiobook"
	familyarchive "github.com/beamfall/plugin-familyarchive"
	filmtv "github.com/beamfall/plugin-filmtv"
	karaoke "github.com/beamfall/plugin-karaoke"
	music "github.com/beamfall/plugin-music"
	photos "github.com/beamfall/plugin-photos"
	feeds "github.com/beamfall/plugin-podcasts"
	sdk "github.com/beamfall/plugin-sdk"
	scrobble "github.com/beamfall/plugin-sdk/scrobble"
)

func assertExactPluginDependencyModules(t *testing.T, tags string, want []string) {
	t.Helper()
	args := []string{"list"}
	if tags != "" {
		args = append(args, "-tags="+tags)
	}
	args = append(args, "-deps", ".")
	out, err := exec.Command("go", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("go list plugin dependency closure: %v\n%s", err, out)
	}
	modules := map[string]bool{}
	for _, dependency := range strings.Fields(string(out)) {
		const prefix = "github.com/beamfall/plugin-"
		if !strings.HasPrefix(dependency, prefix) {
			continue
		}
		module := strings.SplitN(strings.TrimPrefix(dependency, "github.com/beamfall/"), "/", 2)[0]
		modules[module] = true
	}
	got := make([]string, 0, len(modules))
	for module := range modules {
		got = append(got, module)
	}
	sort.Strings(got)
	want = append([]string(nil), want...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plugin dependency modules = %v, want exactly %v", got, want)
	}
}

func TestFirstPartyParserHooksHaveExactPluginIdentities(t *testing.T) {
	want := map[string]reflect.Type{
		books.PluginID:         reflect.TypeOf(books.New()),
		familyarchive.PluginID: reflect.TypeOf(familyarchive.New()),
		filmtv.PluginID:        reflect.TypeOf(filmtv.New()),
		karaoke.PluginID:       reflect.TypeOf(karaoke.New()),
	}
	hooks := firstPartyHooks()
	if len(hooks.Parsers) != len(want) {
		t.Fatalf("parser hooks = %v, want exactly %v", hooks.Parsers, want)
	}
	for pluginID, wantType := range want {
		parser, ok := hooks.Parsers[pluginID]
		if !ok {
			t.Errorf("parser hook missing or mis-keyed for %q", pluginID)
			continue
		}
		if gotType := reflect.TypeOf(parser); gotType != wantType {
			t.Errorf("parser hook %q type = %v, want %v", pluginID, gotType, wantType)
		}
	}
}

func TestFirstPartyOfflinePackHooksHaveExactPluginAndResolverIdentities(t *testing.T) {
	want := music.PackResolvers(nil)
	hooks := firstPartyHooks()
	if len(hooks.OfflinePackResolvers) != len(want) {
		t.Fatalf("offline-pack hooks = %d, want exactly %d", len(hooks.OfflinePackResolvers), len(want))
	}
	seen := map[string]bool{}
	for _, hook := range hooks.OfflinePackResolvers {
		if hook.PluginID != music.PluginID {
			t.Errorf("offline-pack hook %q owner = %q, want %q", hook.Kind, hook.PluginID, music.PluginID)
		}
		wantResolver, ok := want[hook.Kind]
		if !ok {
			t.Errorf("offline-pack hook has absent or mis-keyed kind %q", hook.Kind)
			continue
		}
		if seen[hook.Kind] {
			t.Errorf("offline-pack hook kind %q is duplicated", hook.Kind)
			continue
		}
		seen[hook.Kind] = true
		if hook.New == nil {
			t.Errorf("offline-pack hook %q has nil factory", hook.Kind)
			continue
		}
		resolver, ok := hook.New(nil).(musicPackResolver)
		if !ok {
			t.Errorf("offline-pack hook %q factory returned %T, want musicPackResolver", hook.Kind, hook.New(nil))
			continue
		}
		if gotType, wantType := reflect.TypeOf(resolver.inner), reflect.TypeOf(wantResolver); gotType != wantType {
			t.Errorf("offline-pack hook %q resolver type = %v, want %v", hook.Kind, gotType, wantType)
		}
	}
	for kind := range want {
		if !seen[kind] {
			t.Errorf("offline-pack resolver %q is missing or mis-keyed", kind)
		}
	}
}

func TestFirstPartyImporterFactoriesMatchBundleDeclarations(t *testing.T) {
	hooks := firstPartyHooks()
	want := map[app.MigrationSourceKey]bool{}
	for _, bundle := range firstPartyBundles() {
		for _, importer := range bundle.Importers {
			key := app.MigrationSourceKey{PluginID: string(bundle.ID), ImporterID: importer.ID}
			if want[key] {
				t.Fatalf("duplicate first-party importer declaration: %+v", key)
			}
			want[key] = true
		}
	}
	if len(hooks.MigrationSourceFactories) != len(want) {
		t.Fatalf("migration factories = %d, want %d declared first-party importers", len(hooks.MigrationSourceFactories), len(want))
	}
	for key := range want {
		if hooks.MigrationSourceFactories[key] == nil {
			t.Errorf("declared first-party importer has no factory: %+v", key)
		}
	}
	for key := range hooks.MigrationSourceFactories {
		if !want[key] {
			t.Errorf("migration factory is absent or mis-keyed in first-party bundle declarations: %+v", key)
		}
	}
}

func TestFirstPartyImporterFactoriesReachRealLocalParsers(t *testing.T) {
	podcastID := string(feeds.Bundle().ID)
	cases := []struct {
		name       string
		key        app.MigrationSourceKey
		categories []string
		fixture    func(*testing.T) string
		assert     func(*testing.T, []migrate.ForeignItem)
	}{
		{
			name: "audiobook export",
			key:  app.MigrationSourceKey{PluginID: books.PluginID, ImporterID: books.ImporterID},
			fixture: func(t *testing.T) string {
				return writeImporterFixture(t, "book-export.json", `{"mediaProgress":[{"mediaItemId":"book-1","mediaPath":"/library/book.m4b","currentTime":91,"duration":3600,"isFinished":false}]}`)
			},
			assert: func(t *testing.T, items []migrate.ForeignItem) {
				if len(items) != 1 || items[0].Path != "/library/book.m4b" || !items[0].WatchState.Present {
					t.Fatalf("audiobook items = %#v", items)
				}
			},
		},
		{
			name: "nfo tree",
			key:  app.MigrationSourceKey{PluginID: filmtv.PluginID, ImporterID: "nfo"},
			fixture: func(t *testing.T) string {
				root := t.TempDir()
				writeImporterFile(t, filepath.Join(root, "fixture.mkv"), "bytes")
				writeImporterFile(t, filepath.Join(root, "fixture.nfo"), `<movie><title>Fixture</title><uniqueid type="tmdb">12345</uniqueid></movie>`)
				return root
			},
			assert: func(t *testing.T, items []migrate.ForeignItem) {
				if len(items) != 1 || filepath.Base(items[0].Path) != "fixture.mkv" {
					t.Fatalf("nfo items = %#v", items)
				}
			},
		},
		{
			name: "trakt export",
			key:  app.MigrationSourceKey{PluginID: filmtv.PluginID, ImporterID: filmtv.TraktImporterID},
			fixture: func(t *testing.T) string {
				return writeImporterFixture(t, "trakt.csv", "title,watched_at,tmdb,imdb,tvdb\nFixture,2024-03-04T05:06:07Z,12345,,\n")
			},
			assert: func(t *testing.T, items []migrate.ForeignItem) {
				if len(items) != 1 || !items[0].WatchState.Present || len(items[0].ExternalIDs) != 1 || items[0].ExternalIDs[0].Value != "12345" {
					t.Fatalf("trakt items = %#v", items)
				}
			},
		},
		{
			name: "lastfm export",
			key:  app.MigrationSourceKey{PluginID: music.PluginID, ImporterID: music.LastfmImporterID},
			fixture: func(t *testing.T) string {
				return writeImporterFixture(t, "lastfm.csv", "Fixture Artist,Fixture Record,Fixture Track,1700000000\n")
			},
			assert: func(t *testing.T, items []migrate.ForeignItem) {
				if len(items) != 1 || !items[0].Scrobble.Present || items[0].Scrobble.Track != "Fixture Track" {
					t.Fatalf("lastfm items = %#v", items)
				}
			},
		},
		{
			name: "listenbrainz export",
			key:  app.MigrationSourceKey{PluginID: music.PluginID, ImporterID: music.ListenBrainzImporterID},
			fixture: func(t *testing.T) string {
				return writeImporterFixture(t, "listenbrainz.json", `{"payload":{"listens":[{"listened_at":1700000000,"track_metadata":{"artist_name":"Fixture Artist","track_name":"Fixture Track","additional_info":{"recording_mbid":"recording-1"}}}]}}`)
			},
			assert: func(t *testing.T, items []migrate.ForeignItem) {
				if len(items) != 1 || !items[0].Scrobble.Present || len(items[0].ExternalIDs) != 1 || items[0].ExternalIDs[0].Value != "recording-1" {
					t.Fatalf("listenbrainz items = %#v", items)
				}
			},
		},
		{
			name:       "navidrome export",
			key:        app.MigrationSourceKey{PluginID: music.PluginID, ImporterID: music.NavidromeImporterID},
			categories: []string{migrate.CategoryCollections, migrate.CategoryWatchState},
			fixture: func(t *testing.T) string {
				return writeImporterFixture(t, "navidrome.json", `{"playlists":[{"name":"Fixture Playlist","tracks":[{"artist":"Fixture Artist","title":"Fixture Track"}]}],"plays":[{"artist":"Fixture Artist","track":"Fixture Track","play_count":2,"last_played":"2024-03-04T05:06:07Z"}]}`)
			},
			assert: func(t *testing.T, items []migrate.ForeignItem) {
				var collections, scrobbles int
				for _, item := range items {
					if item.SmartCollection.Present {
						collections++
					}
					if item.Scrobble.Present {
						scrobbles++
					}
				}
				if collections != 1 || scrobbles != 2 {
					t.Fatalf("navidrome items = %#v, want 1 collection + 2 scrobbles", items)
				}
			},
		},
		{
			name: "local tag and m3u export",
			key:  app.MigrationSourceKey{PluginID: music.PluginID, ImporterID: music.LocalM3UImporterID},
			fixture: func(t *testing.T) string {
				return writeLocalM3UFixture(t)
			},
			assert: func(t *testing.T, items []migrate.ForeignItem) {
				if len(items) != 1 || filepath.Base(items[0].Path) != "fixture.mp3" {
					t.Fatalf("local m3u items = %#v", items)
				}
				member := items[0].CollectionMember
				if !member.Present || member.Name != "Fixture Playlist" || member.Position != 0 {
					t.Fatalf("local m3u membership = %#v", member)
				}
				if len(items[0].ExternalIDs) != 1 || items[0].ExternalIDs[0].Scheme != "musicbrainz-recording" || items[0].ExternalIDs[0].Value != "recording-1" {
					t.Fatalf("local m3u external IDs = %#v", items[0].ExternalIDs)
				}
			},
		},
		{
			name: "immich album export",
			key:  app.MigrationSourceKey{PluginID: photos.PluginID, ImporterID: photos.ImmichImporterID},
			fixture: func(t *testing.T) string {
				return writeImporterFixture(t, "immich.json", `[{"albumName":"Fixture Album","assets":[{"id":"asset-1","originalPath":"/library/photo.jpg"}]}]`)
			},
			assert: func(t *testing.T, items []migrate.ForeignItem) {
				if len(items) != 1 || !items[0].CollectionMember.Present || items[0].CollectionMember.Name != "Fixture Album" || items[0].CollectionMember.Position != 0 {
					t.Fatalf("immich items = %#v", items)
				}
			},
		},
		{
			name: "opml export",
			key:  app.MigrationSourceKey{PluginID: podcastID, ImporterID: feeds.OPMLImporterID},
			fixture: func(t *testing.T) string {
				return writeImporterFixture(t, "subscriptions.opml", `<?xml version="1.0"?><opml version="2.0"><body><outline text="Fixture" xmlUrl="https://feeds.example.test/fixture.xml"/></body></opml>`)
			},
			assert: func(t *testing.T, items []migrate.ForeignItem) {
				if len(items) != 1 || !items[0].Subscription.Present || items[0].Subscription.FeedURL != "https://feeds.example.test/fixture.xml" {
					t.Fatalf("opml items = %#v", items)
				}
			},
		},
		{
			name:       "overcast export",
			key:        app.MigrationSourceKey{PluginID: podcastID, ImporterID: feeds.OvercastImporterID},
			categories: []string{migrate.CategoryItemState},
			fixture: func(t *testing.T) string {
				return writeImporterFixture(t, "overcast.opml", `<?xml version="1.0"?><opml version="2.0"><body><outline text="Fixture" xmlUrl="https://feeds.example.test/fixture.xml"><outline text="Item" guid="item-1" progress="0.5"/></outline></body></opml>`)
			},
			assert: func(t *testing.T, items []migrate.ForeignItem) {
				if len(items) != 1 || !items[0].ItemState.Present || items[0].ItemState.GUID != "item-1" {
					t.Fatalf("overcast items = %#v", items)
				}
			},
		},
		{
			name:       "antennapod export",
			key:        app.MigrationSourceKey{PluginID: podcastID, ImporterID: feeds.AntennaPodImporterID},
			categories: []string{migrate.CategoryItemState},
			fixture: func(t *testing.T) string {
				return foreignimporttest.WriteAntennaPodFixture(t, t.TempDir(), "https://feeds.example.test/fixture.xml", "item-1", false, 90, 180)
			},
			assert: func(t *testing.T, items []migrate.ForeignItem) {
				if len(items) != 1 || !items[0].ItemState.Present || !items[0].ItemState.HasProgress || items[0].ItemState.Progress != 0.5 {
					t.Fatalf("antennapod items = %#v", items)
				}
			},
		},
	}

	hooks := firstPartyHooks()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			factory := hooks.MigrationSourceFactories[tc.key]
			if factory == nil {
				t.Fatalf("first-party importer factory missing or mis-keyed: %+v", tc.key)
			}
			source := factory(tc.categories, tc.fixture(t))
			if source == nil {
				t.Fatalf("first-party importer factory returned nil: %+v", tc.key)
			}
			items := []migrate.ForeignItem{}
			err := sdk.StreamSource(t.Context(), source, "", func(_ context.Context, page []migrate.ForeignItem, _ string) error {
				items = append(items, page...)
				return nil
			})
			if err != nil {
				t.Fatalf("source.Stream: %v", err)
			}
			tc.assert(t, items)
		})
	}
}

func TestFirstPartyNavidromeImporterForwardsCategorySelections(t *testing.T) {
	fixture := writeImporterFixture(t, "navidrome.json", `{"playlists":[{"name":"Fixture Playlist","tracks":[{"artist":"Fixture Artist","title":"Fixture Track"}]}],"plays":[{"artist":"Fixture Artist","track":"Fixture Track","play_count":2,"last_played":"2024-03-04T05:06:07Z"}]}`)
	factory := firstPartyHooks().MigrationSourceFactories[app.MigrationSourceKey{
		PluginID:   music.PluginID,
		ImporterID: music.NavidromeImporterID,
	}]
	if factory == nil {
		t.Fatal("Navidrome importer factory missing or mis-keyed")
	}

	tests := []struct {
		name            string
		selected        []string
		wantCategories  []string
		wantCollections int
		wantScrobbles   int
	}{
		{
			name:            "collections only",
			selected:        []string{migrate.CategoryCollections},
			wantCategories:  []string{migrate.CategoryCollections},
			wantCollections: 1,
		},
		{
			name:           "watch state only",
			selected:       []string{migrate.CategoryWatchState},
			wantCategories: []string{migrate.CategoryWatchState},
			wantScrobbles:  2,
		},
		{
			name:            "both",
			selected:        []string{migrate.CategoryCollections, migrate.CategoryWatchState},
			wantCategories:  []string{migrate.CategoryCollections, migrate.CategoryWatchState},
			wantCollections: 1,
			wantScrobbles:   2,
		},
		{name: "empty", selected: []string{}, wantCategories: []string{}},
		{name: "nil", selected: nil, wantCategories: []string{}},
		{name: "unknown only", selected: []string{"unknown"}, wantCategories: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := factory(tt.selected, fixture)
			if got := source.Categories(); !reflect.DeepEqual(got, tt.wantCategories) {
				t.Fatalf("source.Categories() = %v, want %v", got, tt.wantCategories)
			}
			items := []migrate.ForeignItem{}
			err := sdk.StreamSource(t.Context(), source, "", func(_ context.Context, page []migrate.ForeignItem, _ string) error {
				items = append(items, page...)
				return nil
			})
			if err != nil {
				t.Fatalf("source.Stream: %v", err)
			}
			if got, want := len(items), tt.wantCollections+tt.wantScrobbles; got != want {
				t.Fatalf("len(streamed items) = %d, want %d", got, want)
			}
			var collections, scrobbles int
			for _, item := range items {
				if item.SmartCollection.Present {
					collections++
				}
				if item.Scrobble.Present {
					scrobbles++
				}
			}
			if collections != tt.wantCollections || scrobbles != tt.wantScrobbles {
				t.Fatalf("items = %d collections + %d scrobbles, want %d + %d", collections, scrobbles, tt.wantCollections, tt.wantScrobbles)
			}
		})
	}
}

var errInertScrobbleDoer = errors.New("inert scrobble doer")

type inertScrobbleDoer struct{ calls int }

func (d *inertScrobbleDoer) Do(*http.Request) (*http.Response, error) {
	d.calls++
	return nil, errInertScrobbleDoer
}

type inertScrobbleSecrets map[string]string

func (s inertScrobbleSecrets) Secret(ref string) (string, error) {
	value, ok := s[ref]
	if !ok {
		return "", fmt.Errorf("missing inert secret %q", ref)
	}
	return value, nil
}

func TestFirstPartyScrobbleFactoriesUseInjectedDoer(t *testing.T) {
	const baseURL = "http://127.0.0.1:1"
	tests := []struct {
		pluginID string
		service  scrobble.Service
		config   scrobble.AdapterConfig
	}{
		{pluginID: filmtv.PluginID, service: filmtv.ScrobbleServiceTrakt, config: scrobble.AdapterConfig{BaseURL: baseURL, ClientIDRef: "client-id"}},
		{pluginID: music.PluginID, service: music.ServiceLastFM, config: scrobble.AdapterConfig{BaseURL: baseURL, APIKeyRef: "api-key", APISecretRef: "api-secret"}},
		{pluginID: music.PluginID, service: music.ServiceListenBrainz, config: scrobble.AdapterConfig{BaseURL: baseURL}},
	}
	hooks := firstPartyHooks()
	if len(hooks.ScrobbleAdapters) != len(tests) {
		t.Fatalf("scrobble adapter hooks = %d, want %d", len(hooks.ScrobbleAdapters), len(tests))
	}
	byKey := map[string]app.ScrobbleAdapterHook{}
	for _, hook := range hooks.ScrobbleAdapters {
		byKey[hook.PluginID+"\x00"+string(hook.Service)] = hook
	}
	doer := &inertScrobbleDoer{}
	secrets := inertScrobbleSecrets{"client-id": "client", "api-key": "key", "api-secret": "secret"}
	for _, tc := range tests {
		t.Run(tc.pluginID+"/"+string(tc.service), func(t *testing.T) {
			hook, ok := byKey[tc.pluginID+"\x00"+string(tc.service)]
			if !ok || hook.Factory == nil {
				t.Fatalf("scrobble adapter factory missing or mis-keyed for %s/%s", tc.pluginID, tc.service)
			}
			adapter, err := hook.Factory(doer, tc.config, secrets)
			if err != nil {
				t.Fatalf("construct adapter: %v", err)
			}
			if adapter.Service() != tc.service || len(adapter.IdentitySchemes()) == 0 || len(adapter.DeclaredEndpoints()) != 1 || adapter.DeclaredEndpoints()[0] != baseURL {
				t.Fatalf("adapter metadata = service %q schemes %v endpoints %v", adapter.Service(), adapter.IdentitySchemes(), adapter.DeclaredEndpoints())
			}
			err = adapter.Submit(t.Context(), scrobble.Scrobble{
				Event: scrobble.WatchedEvent{
					Class:     adapter.Class(),
					Title:     "Fixture",
					WatchedAt: time.Unix(1_700_000_000, 0).UTC(),
				},
				Identity: scrobble.ExternalID{Scheme: adapter.IdentitySchemes()[0], Value: "1"},
				Secret:   "profile-token",
			})
			if !errors.Is(err, errInertScrobbleDoer) {
				t.Fatalf("Submit error = %v, want injected inert doer", err)
			}
		})
	}
	if doer.calls != len(tests) {
		t.Fatalf("injected doer calls = %d, want %d; adapter bypassed composition seam", doer.calls, len(tests))
	}
}

func writeImporterFixture(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	writeImporterFile(t, path, content)
	return path
}

func writeImporterFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeLocalM3UFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	audioPath := filepath.Join(dir, "fixture.mp3")
	if err := os.WriteFile(audioPath, id3v23Fixture(), 0o600); err != nil {
		t.Fatal(err)
	}
	playlistPath := filepath.Join(dir, "Fixture Playlist.m3u")
	writeImporterFile(t, playlistPath, "#EXTM3U\nfixture.mp3\n")
	return playlistPath
}

func id3v23Fixture() []byte {
	var body []byte
	addFrame := func(id, value string) {
		data := append([]byte{0}, value...)
		size := len(data)
		if size < 0 || size > math.MaxUint32 {
			panic("fixture frame too large")
		}
		frame := make([]byte, 10)
		copy(frame, id)
		binary.BigEndian.PutUint32(frame[4:8], uint32(size))
		body = append(body, frame...)
		body = append(body, data...)
	}
	addFrame("TIT2", "Fixture Track")
	addFrame("TPE1", "Fixture Artist")
	addFrame("TALB", "Fixture Album")
	addFrame("TXXX", "MusicBrainz Track Id\x00recording-1")
	bodySize := len(body)
	if bodySize < 0 || bodySize > math.MaxUint8 {
		panic("fixture tag body too large for one-byte ID3 size")
	}
	header := []byte{'I', 'D', '3', 3, 0, 0, 0, 0, 0, byte(bodySize)}
	return append(header, body...)
}

// staticSourceResolver returns one pre-built source for any payload, mirroring
// how the composition root resolves the registered factory for a migration job.
type staticSourceResolver struct{ source migrate.Source }

func (r staticSourceResolver) Resolve(pluginID, importerID string, categories []string, sourcePath string) (migrate.Source, bool) {
	return r.source, true
}

// TestLocalM3UDryRunReportIncludesImport drives the registered local tag/M3U
// importer through the real migration job handler in dry-run mode: the fixture
// item path-matches seeded media and appears in the operator report's match
// rate, the collections category surfaces on the report line, and the preview
// writes nothing durable.
func TestLocalM3UDryRunReportIncludesImport(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	playlistPath := writeLocalM3UFixture(t)
	audioPath := filepath.Join(filepath.Dir(playlistPath), "fixture.mp3")
	if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               audioPath,
		Title:              "Fixture Track",
		Container:          "mp3",
		Codec:              "mp3",
		Class:              store.MediaClassAudio,
		ContentFingerprint: "fp-local-m3u-0001",
	}); err != nil {
		t.Fatalf("seed fixture media: %v", err)
	}

	hooks := firstPartyHooks()
	key := app.MigrationSourceKey{PluginID: music.PluginID, ImporterID: music.LocalM3UImporterID}
	factory := hooks.MigrationSourceFactories[key]
	if factory == nil {
		t.Fatalf("local m3u factory missing: %+v", key)
	}
	source := factory([]string{migrate.CategoryCollections}, playlistPath)

	registry := jobs.NewRegistry(st, "test-worker")
	registry.Register(migrate.JobKind, migrate.Handler(st, staticSourceResolver{source: source}))
	payload, err := json.Marshal(migrate.Payload{
		Version:    1,
		PluginID:   music.PluginID,
		ImporterID: music.LocalM3UImporterID,
		Categories: []string{migrate.CategoryCollections},
		DryRun:     true,
		ProfileID:  "profile-default",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	enqueued, err := st.EnqueueJob(ctx, store.Job{
		Kind:      migrate.JobKind,
		Class:     store.JobClassBackground,
		DedupeKey: "local-m3u-dry-run",
		Payload:   payload,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("enqueue migration job: %v", err)
	}
	if _, err := registry.RunOne(ctx, nil, now); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	done, err := st.Job(ctx, enqueued.ID)
	if err != nil {
		t.Fatalf("read job back: %v", err)
	}

	if done.Progress.Current != 1 || done.Progress.Total != 1 {
		t.Fatalf("dry-run match-rate = %d of %d, want 1 of 1: %q", done.Progress.Current, done.Progress.Total, done.Progress.Message)
	}
	if !strings.Contains(done.Progress.Message, "collections=") {
		t.Fatalf("dry-run report = %q, want the collections category surfaced", done.Progress.Message)
	}
	collections, err := st.Collections(ctx, "library-local", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 0 {
		t.Fatalf("dry-run created %d collection(s), want none", len(collections))
	}
}
