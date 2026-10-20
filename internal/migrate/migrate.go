// Package migrate runs the Migration Wizard import job: the generic
// handler that executes an enqueued import run. It resolves a source parser for
// the run, asks it to yield foreign items + their data categories, runs the
// match chain to map each foreign item to a core Media by file path (ADR-0028
// §6 — by path regardless of which plugin owns the Library), and applies the
// mapped behavioral state append-only.
//
// The package is domain-agnostic (invariant #1): it speaks only Media,
// engagement, and the fixed import-category vocabulary by shape. The source
// parsers themselves (Plex/Emby/Jellyfin/NFO/OPML) are plugin-provided code
// that ships with the relevant plugins; this package owns the seam they
// plug into, never a source-system name.
//
// The importer seam's data types (ForeignItem, Scrobble, …) and the Source
// interface are published store-free in plugin-sdk so
// a plugin *module* can implement the importer; this package consumes them via
// aliases and adapts ExternalID -> store.MediaExternalID at its match boundary.
package migrate

import (
	"github.com/beamfall/core/internal/store"

	sdk "github.com/beamfall/plugin-sdk"
)

// JobKind is the job-substrate kind for a Migration Wizard import run
// (ADR-0037 §4: the import pipeline is a standard ADR-0013 job). It is the
// core-owned class name the wizard enqueues under; the same constant the graph
// wizard's runImport uses. A job-class name, never a source-system name.
const JobKind store.JobKind = "migration"

// Data categories — the fixed Migration Wizard vocabulary.
// Published store-free in plugin-sdk so plugin importers can report them via
// Source.Categories(); re-exported here as aliases so the handler branches on
// one shared spelling, never two (ARCHITECTURE §0).
const (
	CategoryWatchState    = sdk.CategoryWatchState
	CategoryRatings       = sdk.CategoryRatings
	CategoryCollections   = sdk.CategoryCollections
	CategorySubscriptions = sdk.CategorySubscriptions
	CategoryItemState     = sdk.CategoryItemState
)

// The importer seam's data types + the Source interface live in plugin-sdk so
// plugin modules can implement the importer across the module boundary (Go
// internal visibility forbids importing core internal/). Core consumes them as
// aliases — the handler keeps using migrate.ForeignItem, migrate.Source, etc.
// unchanged. ExternalID is the store-free seam shape (scheme+value); the
// handler adapts it to store.MediaExternalID at the external-ID match sites.
type (
	ExternalID      = sdk.ExternalID
	ForeignItem     = sdk.ForeignItem
	Subscription    = sdk.Subscription
	ItemState       = sdk.ItemState
	Scrobble        = sdk.ScrobbleRecord
	WatchState      = sdk.WatchState
	SmartCollection = sdk.SmartCollection
	Source          = sdk.Source
)

// SourceResolver maps an enqueued run's (pluginID, importerID, sourcePath) to
// the Source that parses it. The app wires a concrete resolver over the
// activated plugin registry (the plugins declare ImporterSpecs and provide the
// matching compiled parsers); this package never hardcodes a source name.
// sourcePath is the operator-supplied location the run carries (Payload.SourcePath
// — an export-file path or connection string): the resolver hands it to the
// path-needing constructor so the Source knows what to read. A resolver that
// cannot service the pair returns ok=false, and the run fails with a clear "no
// parser" error rather than silently importing nothing.
type SourceResolver interface {
	Resolve(pluginID, importerID string, categories []string, sourcePath string) (Source, bool)
}
