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
	EntityPosterRules              []EntityPosterRule
	EntityBrowseRules              []EntityBrowseRule
	EntityEnrichments              []EntityEnrichment
	OfflinePackResolvers           []OfflinePackResolverHook
	OfflinePackAttestationVerifier offlinepack.DeviceKeyAttestationVerifier
	EnrichmentProvider             EnrichmentProviderFactory
	ScrobbleAdapters               []ScrobbleAdapterHook
	FeedSearcher                   *FeedSearcherHook
	PrePlayPolicy                  *PrePlayPolicyHook
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

// PrePlayPolicyHook contributes the graph.PrePlayPolicy behind prePlayQueue
// (CORE-1701). It is used only while PluginID's bundle is active.
type PrePlayPolicyHook struct {
	PluginID string
	New      func(*store.Store) graph.PrePlayPolicy
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

// HydrationLocal is one parse of a Media's local sidecar: the built-in title,
// sort title, summary and year (empty or zero leaves one as it is), declared
// Media fields, and Media-target relations, all applied at the locked local
// tier (store.MutationSourceLocal) in one transaction. OwnedRelations names
// the relations the sidecar format owns: the Media's local rows of each are
// replaced by Relations, so an owned relation with no rows is emptied, while
// rows an operator or another writer owns stay. Signature identifies the
// bytes that were parsed. Unreadable reports a sidecar that exists but could
// not be read or parsed: nothing is written or released and the next scan
// retries.
type HydrationLocal struct {
	Title          string
	SortTitle      string
	Summary        string
	Year           int
	MediaFields    []store.MediaField
	Relations      []HydrationRelation
	OwnedRelations []store.WrittenRelation
	Signature      string
	Unreadable     bool
}

// HydrationSidecarMapper is implemented by a HydrationMapper that also reads
// local sidecars kept beside the media. SidecarSignature identifies the
// sidecar of a media path: "" only when there is none, and an error when the
// lookup failed, which the seam treats as an unreadable sidecar, never a
// removed one. The seam records the selected media path with the signature of
// each applied parse in the declared text Media field SidecarSignatureField,
// so a scan re-reads an unchanged file whose sidecar changed. MapSidecar
// parses the sidecar of a path, nil when there is none; a readable sidecar
// with no values is a signed, empty parse that still states its owned
// relations. When the sidecar is gone, the Media's local values are released
// to the scan tier and its local relation rows to an unowned writer: kept,
// but unlocked.
type HydrationSidecarMapper interface {
	SidecarSignatureField() string
	SidecarSignature(path string) (string, error)
	MapSidecar(path string) *HydrationLocal
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
	Kind  string
	Title string
	// Scope makes Title unique only within it: the same Kind and Title under
	// two scopes are two Entities. Empty keeps the title-only identity. A
	// mapper that sets it must declare the kind through HydrationScopeMapper.
	Scope  string
	Fields []store.EntityField
}

// HydrationEntityScope declares that Entities of Kind are scoped, and that the
// entity→entity Relation from them records the scope: its target is the
// unscoped Entity whose title is HydrationEntity.Scope. The adapter reads that
// relation to keep using a title-only Entity written before the kind was
// scoped instead of creating a twin.
type HydrationEntityScope struct {
	Kind     string
	Relation string
}

// HydrationScopeMapper is implemented by a HydrationMapper whose plans carry
// scoped Entities.
type HydrationScopeMapper interface {
	EntityScopes() []HydrationEntityScope
}

// HydrationTitleFoldMapper is implemented by a HydrationMapper whose Entity
// titles come from file names, where one title can be spelled in different
// case or punctuation. For each listed kind, a Media hydrated again that moves
// from one Entity of the kind to another whose title folds equal
// (store.FoldEntityTitle) merges the old Entity into the new one, with its
// item state, through the recoverable entity merge. Listed kinds that are not
// scoped also get a one-time startup repair that reopens the Media of
// fold-equal Entities, so a library hydrated before the mapper settled one
// spelling is merged by the next scan.
type HydrationTitleFoldMapper interface {
	TitleFoldKinds() []string
}

// HydrationMediaFieldsMapper is implemented by a HydrationMapper that names
// every Media field its HydrationPlan may carry; a plan with any other Media
// field is rejected. The scan then knows which older scan-tier values the
// hydrator cannot have written, so the filename parser may reconcile them
// (CORE-2554). A mapper without it may write any declared Media field.
type HydrationMediaFieldsMapper interface {
	HydratedMediaFields() []string
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

func activePrePlayPolicy(registry *plugin.Registry, hook *PrePlayPolicyHook, st *store.Store) graph.PrePlayPolicy {
	if hook == nil || hook.New == nil || st == nil {
		return nil
	}
	if _, ok := activeBundle(registry, hook.PluginID); !ok {
		return nil
	}
	return hook.New(st)
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
