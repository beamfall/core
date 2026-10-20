package app

import (
	"context"
	"errors"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/migrate"
	"github.com/beamfall/core/internal/offlinepack"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
	scrobble "github.com/beamfall/plugin-sdk/scrobble"
)

// Hooks contains compiled plugin-module behavior supplied by the executable
// composition root. Core validates and activates these contributions against
// the post-override plugin registry before use.
type Hooks struct {
	Parsers                        map[string]scan.FilenameParser
	IdentifyScorers                map[string]IdentifyScorerHook
	MigrationSourceFactories       map[MigrationSourceKey]MigrationSourceFactory
	LocalExtractors                map[string][]scan.LocalExtractor
	HydrationMappers               map[string]HydrationMapper
	OfflinePackResolvers           []OfflinePackResolverHook
	OfflinePackAttestationVerifier offlinepack.DeviceKeyAttestationVerifier
	EnrichmentProvider             EnrichmentProviderFactory
	ScrobbleAdapters               []ScrobbleAdapterHook
	FeedSearcher                   *FeedSearcherHook
}

type IdentifyScorerHook struct {
	Scorer    identify.MatchScorer
	Threshold float64
}

type MigrationSourceKey struct {
	PluginID   string
	ImporterID string
}

type MigrationSourceFactory func(categories []string, sourcePath string) migrate.Source

type OfflinePackResolverHook struct {
	PluginID string
	Kind     string
	New      func(*store.Store) offlinepack.TargetResolver
}

type EnrichmentProvider interface {
	Available() bool
	Kind() store.EmbeddingKind
	EmbedQuery(context.Context, string) ([]float32, error)
	EmbedDocument(context.Context, string) ([]float32, error)
}

type EnrichmentProviderFactory func(enabled bool) EnrichmentProvider

type ScrobbleAdapterHook struct {
	PluginID string
	Service  scrobble.Service
	Factory  scrobble.AdapterFactory
}

type SecretSource interface {
	Secret(ref string) (string, error)
}

type FeedSearcherHook struct {
	PluginID          string
	RequiredScraperID string
	New               func(*scrape.Engine, SecretSource) graph.FeedSearcher
}

// HydrationPlan is a shape-neutral write intent supplied by a compiled plugin
// contribution. Core validates every field and relation against the active
// bundle before executing it.
type HydrationPlan struct {
	MediaFields  []store.MediaField
	Relations    []HydrationRelation
	ExternalIDs  []HydrationExternalID
	MarkComplete bool
}

func (h HydrationPlan) Empty() bool {
	if h.MarkComplete {
		return false
	}
	if len(h.MediaFields) != 0 {
		return false
	}
	if len(h.Relations) != 0 {
		return false
	}
	return len(h.ExternalIDs) == 0
}

type HydrationEntity struct {
	Kind   string
	Title  string
	Fields []store.EntityField
}

// HydrationRelation targets the current Media when ToEntity is nil.
type HydrationRelation struct {
	From             HydrationEntity
	Name             string
	RequiresRelation string
	ToEntity         *HydrationEntity
	Attributes       map[string]string
	Cardinality      string
	SortOrder        *int
}

type HydrationExternalID struct {
	Scheme string
	Value  string
}

type HydrationMapper interface {
	MapHydration(tags map[string]string, path string) HydrationPlan
	MarkerField() string
	// ExternalIDSchemes is the immutable composition-time allowlist for
	// HydrationPlan.ExternalIDs. Core snapshots it when activating the mapper.
	ExternalIDSchemes() []string
}

func resolveHooks(values []Hooks) (Hooks, error) {
	if len(values) > 1 {
		return Hooks{}, errors.New("app.New accepts at most one Hooks value")
	}
	if len(values) == 0 {
		return Hooks{}, nil
	}
	return values[0], nil
}

func activeFeedSearcher(registry *plugin.Registry, hook *FeedSearcherHook, engine *scrape.Engine, secrets SecretSource) graph.FeedSearcher {
	if hook == nil || hook.New == nil {
		return nil
	}
	bundle, ok := activeBundle(registry, hook.PluginID)
	if !ok || !declaresScraper(bundle, hook.RequiredScraperID) {
		return nil
	}
	return hook.New(engine, secrets)
}

func declaresScraper(bundle plugin.Bundle, scraperID string) bool {
	for _, scraper := range bundle.Scrapers {
		if scraper.ID == scraperID {
			return true
		}
	}
	return false
}

func registerOfflinePackResolvers(service *offlinepack.Service, st *store.Store, registry *plugin.Registry, hooks []OfflinePackResolverHook) {
	if service == nil || st == nil {
		return
	}
	seen := map[string]bool{}
	for _, hook := range hooks {
		if hook.Kind == "" || hook.New == nil || seen[hook.Kind] {
			continue
		}
		if _, ok := activeBundle(registry, hook.PluginID); !ok {
			continue
		}
		resolver := hook.New(st)
		if resolver == nil {
			continue
		}
		service.RegisterResolver(hook.Kind, resolver)
		seen[hook.Kind] = true
	}
}
