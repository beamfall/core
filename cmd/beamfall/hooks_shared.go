package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/migrate"
	"github.com/beamfall/core/internal/offlinepack"
	"github.com/beamfall/core/internal/plugin"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
	books "github.com/beamfall/plugin-audiobook"
	enrichment "github.com/beamfall/plugin-enrichment"
	familyarchive "github.com/beamfall/plugin-familyarchive"
	filmtv "github.com/beamfall/plugin-filmtv"
	karaoke "github.com/beamfall/plugin-karaoke"
	music "github.com/beamfall/plugin-music"
	photos "github.com/beamfall/plugin-photos"
	feeds "github.com/beamfall/plugin-podcasts"
)

func firstPartyHooksForBundles(bundles []plugin.Bundle) app.Hooks {
	active := map[string]bool{}
	for _, bundle := range bundles {
		active[string(bundle.ID)] = true
	}
	hooks := app.Hooks{
		Parsers:                  map[string]scanpkg.FilenameParser{},
		IdentifyScorers:          map[string]app.IdentifyScorerHook{},
		MigrationSourceFactories: map[app.MigrationSourceKey]app.MigrationSourceFactory{},
		LocalExtractors:          map[string][]scanpkg.LocalExtractor{},
		HydrationMappers:         map[string]app.HydrationMapper{},
		EnrichmentProvider: func(enabled bool) app.EnrichmentProvider {
			return enrichmentProvider{Provider: enrichment.New(enabled)}
		},
	}
	if active[filmtv.PluginID] {
		hooks.Parsers[filmtv.PluginID] = filmtv.New()
		hooks.IdentifyScorers[filmtv.PluginID] = app.IdentifyScorerHook{Scorer: filmtv.NewScorer(), Threshold: filmtv.AutoApplyThreshold}
		addFilmTVMigrationSources(&hooks)
		hooks.ScrobbleAdapters = append(hooks.ScrobbleAdapters, app.ScrobbleAdapterHook{PluginID: filmtv.PluginID, Service: filmtv.ScrobbleServiceTrakt, Factory: filmtv.TraktScrobbleFactory})
	}
	if active[books.PluginID] {
		hooks.Parsers[books.PluginID] = books.New()
		hooks.MigrationSourceFactories[app.MigrationSourceKey{PluginID: books.PluginID, ImporterID: books.ImporterID}] = func(_ []string, path string) migrate.Source {
			return books.NewImportSource(path)
		}
	}
	if active[familyarchive.PluginID] {
		hooks.Parsers[familyarchive.PluginID] = familyarchive.New()
	}
	if active[karaoke.PluginID] {
		hooks.Parsers[karaoke.PluginID] = karaoke.New()
	}
	if active[photos.PluginID] {
		hooks.LocalExtractors[photos.PluginID] = []scanpkg.LocalExtractor{photosLocalExtractor{inner: photos.New()}}
		hooks.MigrationSourceFactories[app.MigrationSourceKey{PluginID: photos.PluginID, ImporterID: photos.ImmichImporterID}] = func(_ []string, path string) migrate.Source {
			return photos.NewImmichSource(path)
		}
	}
	if active[music.PluginID] {
		addMusicHooks(&hooks)
	}
	if active[string(feeds.Bundle().ID)] {
		addPodcastHooks(&hooks)
	}
	return hooks
}

func addFilmTVMigrationSources(hooks *app.Hooks) {
	add := func(importerID string, factory app.MigrationSourceFactory) {
		hooks.MigrationSourceFactories[app.MigrationSourceKey{PluginID: filmtv.PluginID, ImporterID: importerID}] = factory
	}
	add("plex", func(_ []string, path string) migrate.Source { return filmtv.NewPlexSource(path) })
	add("emby", func(_ []string, path string) migrate.Source { return filmtv.NewEmbySource(path) })
	add("jellyfin", func(_ []string, path string) migrate.Source { return filmtv.NewJellyfinSource(path) })
	add("nfo", func(_ []string, path string) migrate.Source { return filmtv.NewNFOSource(path) })
	add(filmtv.TraktImporterID, func(_ []string, path string) migrate.Source { return filmtv.NewTraktSource(path) })
}

func addMusicHooks(hooks *app.Hooks) {
	hooks.HydrationMappers[music.PluginID] = musicHydrationMapper{}
	hooks.MigrationSourceFactories[app.MigrationSourceKey{PluginID: music.PluginID, ImporterID: music.LastfmImporterID}] = func(_ []string, path string) migrate.Source { return music.NewLastfmSource(path) }
	hooks.MigrationSourceFactories[app.MigrationSourceKey{PluginID: music.PluginID, ImporterID: music.ListenBrainzImporterID}] = func(_ []string, path string) migrate.Source { return music.NewListenBrainzSource(path) }
	hooks.MigrationSourceFactories[app.MigrationSourceKey{PluginID: music.PluginID, ImporterID: music.NavidromeImporterID}] = func(categories []string, path string) migrate.Source {
		return music.NewNavidromeSourceForCategories(path, categories)
	}
	hooks.MigrationSourceFactories[app.MigrationSourceKey{PluginID: music.PluginID, ImporterID: music.LocalM3UImporterID}] = func(_ []string, path string) migrate.Source { return music.NewLocalM3USource(path) }
	hooks.ScrobbleAdapters = append(hooks.ScrobbleAdapters,
		app.ScrobbleAdapterHook{PluginID: music.PluginID, Service: music.ServiceLastFM, Factory: music.LastFMScrobbleFactory},
		app.ScrobbleAdapterHook{PluginID: music.PluginID, Service: music.ServiceListenBrainz, Factory: music.ListenBrainzScrobbleFactory},
	)
	for kind, resolver := range music.PackResolvers(nil) {
		kind, resolver := kind, resolver
		hooks.OfflinePackResolvers = append(hooks.OfflinePackResolvers, app.OfflinePackResolverHook{
			PluginID: music.PluginID,
			Kind:     kind,
			New: func(st *store.Store) offlinepack.TargetResolver {
				return musicPackResolver{store: st, inner: resolver}
			},
		})
	}
}

func addPodcastHooks(hooks *app.Hooks) {
	pluginID := string(feeds.Bundle().ID)
	add := func(importerID string, factory app.MigrationSourceFactory) {
		hooks.MigrationSourceFactories[app.MigrationSourceKey{PluginID: pluginID, ImporterID: importerID}] = factory
	}
	add(feeds.OPMLImporterID, func(_ []string, path string) migrate.Source { return feeds.NewOPMLSource(path) })
	add(feeds.OvercastImporterID, func(categories []string, path string) migrate.Source {
		return feeds.NewOvercastSource(path, categories)
	})
	add(feeds.AntennaPodImporterID, func(categories []string, path string) migrate.Source {
		return feeds.NewAntennaPodSource(path, categories)
	})
	hooks.FeedSearcher = &app.FeedSearcherHook{
		PluginID:          pluginID,
		RequiredScraperID: "itunes-directory",
		New: func(engine *scrape.Engine, secrets app.SecretSource) graph.FeedSearcher {
			return feedDirectorySearcher{engine: engine, secrets: secrets}
		},
	}
}

type photosLocalExtractor struct{ inner *photos.LocalExtractor }

func (e photosLocalExtractor) ExtractLocal(ctx context.Context, class, root, path string) (scanpkg.LocalMetadata, error) {
	result, err := e.inner.ExtractLocal(ctx, class, root, path)
	if err != nil {
		return scanpkg.LocalMetadata{}, err
	}
	entities := make([]scanpkg.LocalEntity, 0, len(result.Entities))
	for _, entity := range result.Entities {
		entities = append(entities, scanpkg.LocalEntity{
			Kind: entity.Kind, Title: entity.Title, Relation: entity.Relation,
			Cardinality: entity.Cardinality, SortOrder: entity.SortOrder,
		})
	}
	return scanpkg.LocalMetadata{Fields: result.Fields, ImpliedMediaFields: result.ImpliedMediaFields, Entities: entities}, nil
}

type musicHydrationMapper struct{}

func (musicHydrationMapper) MarkerField() string { return music.HydratedField }

func (musicHydrationMapper) ExternalIDSchemes() []string {
	return []string{music.SchemeMusicBrainzRecording}
}

func (musicHydrationMapper) MapHydration(tags map[string]string, path string) app.HydrationPlan {
	hydration := music.MapHydration(tags, path)
	plan := app.HydrationPlan{}
	addMediaField := func(field store.MediaField) {
		plan.MarkComplete = true
		plan.MediaFields = append(plan.MediaFields, field)
	}
	if hydration.Title != "" {
		addMediaField(store.MediaField{Name: "title", Type: store.EntityFieldText, Text: hydration.Title})
	}
	if hydration.Album != "" {
		addMediaField(store.MediaField{Name: "album", Type: store.EntityFieldText, Text: hydration.Album})
	}
	if hydration.TrackNumber > 0 {
		addMediaField(store.MediaField{Name: "track-number", Type: store.EntityFieldNumber, Number: float64(hydration.TrackNumber)})
	} else if hydration.TrackNumber != 0 {
		plan.MarkComplete = true
	}
	if hydration.DiscNumber > 0 {
		addMediaField(store.MediaField{Name: "disc-number", Type: store.EntityFieldNumber, Number: float64(hydration.DiscNumber)})
	} else if hydration.DiscNumber != 0 {
		plan.MarkComplete = true
	}
	if hydration.ReleaseYear > 0 {
		addMediaField(store.MediaField{Name: "release-year", Type: store.EntityFieldNumber, Number: float64(hydration.ReleaseYear)})
	} else if hydration.ReleaseYear != 0 {
		plan.MarkComplete = true
	}
	if !hydration.ReleaseDate.IsZero() {
		addMediaField(store.MediaField{Name: "release-date", Type: store.EntityFieldDate, Date: hydration.ReleaseDate.UTC()})
	}
	if hydration.RecordingMBID != "" {
		addMediaField(store.MediaField{Name: "musicbrainz-recording-id", Type: store.EntityFieldText, Text: hydration.RecordingMBID})
		plan.ExternalIDs = append(plan.ExternalIDs, app.HydrationExternalID{Scheme: music.SchemeMusicBrainzRecording, Value: hydration.RecordingMBID})
	}
	if hydration.ReleaseMBID != "" {
		addMediaField(store.MediaField{Name: "musicbrainz-release-id", Type: store.EntityFieldText, Text: hydration.ReleaseMBID})
	}

	if len(hydration.Credits) != 0 {
		plan.MarkComplete = true
	}
	seenCredits := map[string]bool{}
	for _, credit := range hydration.Credits {
		if credit.Artist == "" || seenCredits[credit.Artist] {
			continue
		}
		seenCredits[credit.Artist] = true
		attributes := map[string]string{}
		if credit.CreditedAs != "" {
			attributes["credited-as"] = credit.CreditedAs
		}
		plan.Relations = append(plan.Relations, app.HydrationRelation{
			From:       app.HydrationEntity{Kind: "artist", Title: credit.Artist},
			Name:       "appears-on",
			Attributes: attributes,
		})
	}

	if hydration.AlbumArtist != "" {
		plan.MarkComplete = true
	}
	if hydration.Album != "" {
		album := app.HydrationEntity{Kind: "album", Title: hydration.Album}
		if !hydration.ReleaseDate.IsZero() {
			album.Fields = append(album.Fields, store.EntityField{Name: "release-date", Type: store.EntityFieldDate, Date: hydration.ReleaseDate.UTC()})
		}
		if hydration.ReleaseMBID != "" {
			album.Fields = append(album.Fields, store.EntityField{Name: "musicbrainz-release-id", Type: store.EntityFieldText, Text: hydration.ReleaseMBID})
		}
		plan.Relations = append(plan.Relations, app.HydrationRelation{
			From:        album,
			Name:        "album-track",
			Cardinality: "one",
			SortOrder:   musicTrackSortOrder(hydration.DiscNumber, hydration.TrackNumber),
		})
		if hydration.AlbumArtist != "" {
			plan.Relations = append(plan.Relations, app.HydrationRelation{
				From:             album,
				Name:             "album-artist",
				RequiresRelation: "album-track",
				ToEntity:         &app.HydrationEntity{Kind: "artist", Title: hydration.AlbumArtist},
			})
		}
	}

	if len(hydration.Genres) != 0 {
		plan.MarkComplete = true
	}
	seenGenres := map[string]bool{}
	for _, genre := range hydration.Genres {
		if genre == "" || seenGenres[genre] {
			continue
		}
		seenGenres[genre] = true
		plan.Relations = append(plan.Relations, app.HydrationRelation{
			From: app.HydrationEntity{Kind: "genre", Title: genre},
			Name: "classifies",
		})
	}
	if hydration.ArtistMBID != "" {
		plan.MarkComplete = true
	}
	return plan
}

func musicTrackSortOrder(disc, track int) *int {
	if disc <= 0 && track <= 0 {
		return nil
	}
	if disc <= 0 {
		disc = 1
	}
	order := disc*1000 + track
	return &order
}

type enrichmentProvider struct{ *enrichment.Provider }

func (enrichmentProvider) Kind() store.EmbeddingKind {
	return store.EmbeddingKind(enrichment.KindString)
}
func (p enrichmentProvider) EmbedDocument(ctx context.Context, text string) ([]float32, error) {
	return p.EmbedQuery(ctx, text)
}

type packStore interface {
	MediaForEntity(context.Context, store.EntityID, store.LibraryFilter) ([]store.SourceItem, error)
	CollectionMemberMediaIDs(context.Context, store.CollectionID, store.LibraryFilter) ([]store.MediaID, error)
	VisibleMediaBatch(context.Context, []store.MediaID, store.LibraryFilter) (map[store.MediaID]store.Media, error)
}

type packMediaReader struct {
	store  packStore
	filter store.LibraryFilter
}

func (r packMediaReader) EntityMembers(ctx context.Context, ref string) ([]string, error) {
	items, err := r.store.MediaForEntity(ctx, store.EntityID(ref), r.filter)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, string(item.ID))
	}
	return ids, nil
}

func (r packMediaReader) CollectionMembers(ctx context.Context, ref string) ([]string, error) {
	mediaIDs, err := r.store.CollectionMemberMediaIDs(ctx, store.CollectionID(ref), r.filter)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(mediaIDs))
	for i, id := range mediaIDs {
		ids[i] = string(id)
	}
	return ids, nil
}

func (r packMediaReader) VisibleSubset(ctx context.Context, ids []string) ([]string, error) {
	mediaIDs := make([]store.MediaID, len(ids))
	for i, id := range ids {
		mediaIDs[i] = store.MediaID(id)
	}
	visible, err := r.store.VisibleMediaBatch(ctx, mediaIDs, r.filter)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := visible[store.MediaID(id)]; ok {
			out = append(out, id)
		}
	}
	return out, nil
}

type musicPackResolver struct {
	store packStore
	inner music.PackResolver
}

func (r musicPackResolver) Resolve(ctx context.Context, ref string, filter store.LibraryFilter) (offlinepack.ResolvedTarget, error) {
	resolved, err := r.inner.Resolve(ctx, ref, packMediaReader{store: r.store, filter: filter})
	if err != nil {
		return offlinepack.ResolvedTarget{}, err
	}
	mediaIDs := make([]store.MediaID, len(resolved.MediaIDs))
	for i, id := range resolved.MediaIDs {
		mediaIDs[i] = store.MediaID(id)
	}
	return offlinepack.ResolvedTarget{MediaIDs: mediaIDs, Label: resolved.Label}, nil
}

type feedDirectorySearcher struct {
	engine  *scrape.Engine
	secrets app.SecretSource
}

func (s feedDirectorySearcher) SearchFeeds(ctx context.Context, query string) ([]graph.FeedSearchResult, error) {
	results, err := s.searchITunes(ctx, query)
	if err != nil {
		return nil, err
	}
	if richer, richerErr := s.searchPodcastIndex(ctx, query); richerErr == nil {
		results = append(results, richer...)
	}
	return results, nil
}

func (s feedDirectorySearcher) searchITunes(ctx context.Context, query string) ([]graph.FeedSearchResult, error) {
	endpoint, template, ok := feedDirectoryScraper("itunes-directory")
	if !ok {
		return nil, errors.New("feed directory scraper is not registered")
	}
	rawURL := strings.TrimRight(endpoint, "/") + strings.ReplaceAll(template, "{query}", url.QueryEscape(query))
	data, err := s.engine.FetchDocument(ctx, rawURL, nil)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Results []struct {
			Title       string `json:"collectionName"`
			FeedURL     string `json:"feedUrl"`
			ArtworkURL  string `json:"artworkUrl600"`
			Description string `json:"description"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	results := make([]graph.FeedSearchResult, 0, len(parsed.Results))
	for _, item := range parsed.Results {
		results = append(results, graph.FeedSearchResult{Title: item.Title, FeedURL: item.FeedURL, ArtworkURL: item.ArtworkURL, Description: item.Description})
	}
	return results, nil
}

func feedDirectoryScraper(id string) (string, string, bool) {
	for _, scraper := range feeds.Bundle().Scrapers {
		if scraper.ID == id {
			return scraper.Endpoint, scraper.SearchTemplate, true
		}
	}
	return "", "", false
}

func (s feedDirectorySearcher) searchPodcastIndex(ctx context.Context, query string) ([]graph.FeedSearchResult, error) {
	key, err := s.secrets.Secret("podcast-index-key")
	if err != nil {
		return nil, fmt.Errorf("podcast-index-key: %w", err)
	}
	secret, err := s.secrets.Secret("podcast-index-secret")
	if err != nil {
		return nil, fmt.Errorf("podcast-index-secret: %w", err)
	}
	results, err := feeds.NewClient(key, secret, s.engine).Search(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]graph.FeedSearchResult, 0, len(results))
	for _, feed := range results {
		out = append(out, graph.FeedSearchResult{Title: feed.Title, FeedURL: feed.FeedURL, ArtworkURL: feed.ArtworkURL, Description: feed.Description})
	}
	return out, nil
}
