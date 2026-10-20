// Package identify implements manual identification: a manager
// searches a plugin-declared scraper for candidates, a human picks one, and
// the chosen candidate's lookup document is applied to the Media row's
// descriptive metadata (title, summary, year).
//
// Artwork application is best-effort: Apply imports Core-known artwork slots
// but unknown plugin slot names stay display-only until Core learns them.
package identify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/beamfall/plugin-sdk"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// Engine executes declarative ScraperSpecs — the seam over *scrape.Engine,
// defined at the consumer.
type Engine interface {
	Search(ctx context.Context, spec plugin.ScraperSpec, query string, vars ...scrape.Variables) ([]scrape.Result, error)
	Lookup(ctx context.Context, spec plugin.ScraperSpec, id string, vars ...scrape.Variables) (scrape.Result, error)
}

// Bundles resolves activated plugin bundles; *plugin.Registry satisfies it.
type Bundles interface {
	Bundle(id plugin.ID) (plugin.Bundle, bool)
}

// MetadataStore is the metadata write seam; *store.Store satisfies it.
type MetadataStore interface {
	UpdateMediaMetadata(ctx context.Context, id store.MediaID, metadata store.MediaMetadata) error
}

// ArtworkCandidateStore persists scraped artwork alternatives. It is optional
// because identification can run metadata-only in tests and early bootstrap
// wiring; production callers wire *store.Store.
type ArtworkCandidateStore interface {
	UpsertArtworkCandidate(ctx context.Context, mediaID store.MediaID, slot, sourceURL, pluginID, scraperID string) error
	MarkArtworkCandidateApplied(ctx context.Context, candidateID string) error
}

// Fetcher downloads one remote resource through a hardened client;
// *scrape.Engine satisfies it (the same SSRF gate as scraper requests).
type Fetcher interface {
	FetchResource(ctx context.Context, rawURL string) ([]byte, error)
}

// ArtworkStore stores fetched artwork; *artwork.Service satisfies it.
// HasEntityPoster is the cheap existence probe that keeps re-applies from
// re-downloading entity posters that already landed.
type ArtworkStore interface {
	ImportArtwork(ctx context.Context, id store.MediaID, slot string, data []byte) error
	HasArtwork(id store.MediaID, slot string) bool
	IsGeneratedArtwork(id store.MediaID, slot string) bool
	HasEntityPoster(id store.EntityID) bool
	ImportEntityPoster(ctx context.Context, id store.EntityID, data []byte) error
}

type embeddedArtworkStore interface {
	ImportEmbeddedPoster(ctx context.Context, id store.MediaID) (bool, error)
}

// FieldStore is the Media field write seam; *store.Store satisfies it.
// Upsert (not full-replace) semantics: fields other writers set survive.
// Identification writes at the operator (identified) tier so a manual edit is
// never clobbered by a re-identify / scheduled re-match.
type FieldStore interface {
	UpsertMediaFieldsWithSource(ctx context.Context, id store.MediaID, fields []store.MediaField, source store.MutationSource) error
}

// FingerprintStore is the content-identity read seam;
// *store.Store satisfies it. The values feed the engine's fingerprint
// variables ({oshash}, {phash}, {duration}).
type FingerprintStore interface {
	MediaFingerprints(ctx context.Context, id store.MediaID) (store.MediaFingerprints, error)
}

// EntityStore is the entity hydration write seam; *store.Store satisfies it.
// MediaLibrary scopes hydrated Entities to the identified Media's Library
// (Entities are Library-scoped so reveal filtering applies).
type EntityStore interface {
	MediaLibrary(ctx context.Context, id store.MediaID) (store.LibraryID, error)
	UpsertEntity(ctx context.Context, entity store.Entity) error
	UpsertEntityRelation(ctx context.Context, relation store.EntityRelation) error
}

// Candidate is one identification candidate from a scraper search.
type Candidate = sdk.Candidate

// Service runs the manual identification flow: Search → a human picks →
// Apply.
type Service struct {
	engine       Engine
	bundles      Bundles
	store        MetadataStore
	candidates   ArtworkCandidateStore
	fetcher      Fetcher
	artwork      ArtworkStore
	fields       FieldStore
	entities     EntityStore
	fingerprints FingerprintStore
	// scorers binds each plugin's MatchScorer + auto-apply threshold by plugin
	// ID; empty until the composition root registers one.
	scorers map[string]scorerBinding
	// queue parks below-threshold scan-time matches; pins
	// reads the FREEZE-3 import match-pin so rescans respect an operator fix. Both
	// nil until SetAutoStores wires them — the manual flow never touches them.
	queue             UnmatchedStore
	pins              MatchPinStore
	optionalAuthSkips sync.Map
}

// NewService wires the identification service.
func NewService(engine Engine, bundles Bundles, store MetadataStore) *Service {
	return &Service{engine: engine, bundles: bundles, store: store}
}

// SetArtwork wires scraped-artwork application: Apply downloads the chosen
// candidate's poster through the fetcher and stores it. Nil (the default)
// keeps Apply metadata-only.
func (s *Service) SetArtwork(fetcher Fetcher, artwork ArtworkStore) {
	s.fetcher = fetcher
	s.artwork = artwork
}

// SetArtworkCandidates wires persistence for the candidate picker.
func (s *Service) SetArtworkCandidates(store ArtworkCandidateStore) {
	s.candidates = store
}

// SetFields wires implied-Media-field application: when the chosen scraper
// declares ImpliedMediaFields, Apply writes them after the metadata write.
// Nil (the default) makes Apply error on such scrapers rather than silently
// drop declared values.
func (s *Service) SetFields(fields FieldStore) {
	s.fields = fields
}

// SetEntities wires entity hydration: when the chosen scraper's EntityMaps
// resolved entities from the lookup document, Apply upserts each as an Entity
// in the Media's Library and links the declared Media relation. Nil (the
// default) makes Apply error on such results rather than silently drop
// declared hydrations.
func (s *Service) SetEntities(entities EntityStore) {
	s.entities = entities
}

// SetFingerprints wires fingerprint-variable sourcing: Search and Apply read
// the Media item's content-identity values and pass them to the engine for
// {oshash}/{phash}/{duration} substitution. Nil (the default) substitutes
// them empty.
func (s *Service) SetFingerprints(fingerprints FingerprintStore) {
	s.fingerprints = fingerprints
}

// Search resolves the plugin's scraper spec and returns the engine's
// candidates mapped onto the core metadata fields. mediaID is the Media item
// being identified; it sources the engine's fingerprint variables and may be
// empty (free-text-only search). The query may be empty when mediaID is set —
// a fingerprint-driven scraper queries by content identity, not text.
func (s *Service) Search(ctx context.Context, pluginID, scraperID, query string, mediaID store.MediaID) ([]Candidate, error) {
	if strings.TrimSpace(query) == "" && mediaID == "" {
		return nil, errors.New("identify: query or media ID is required")
	}
	_, spec, err := s.spec(pluginID, scraperID)
	if err != nil {
		return nil, err
	}
	vars, err := s.variables(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	vars.PluginID = pluginID
	results, err := s.engine.Search(ctx, spec, query, vars)
	if err != nil {
		return nil, err
	}
	candidates := make([]Candidate, len(results))
	for i, result := range results {
		candidates[i] = candidateFromResult(result)
	}
	return candidates, nil
}

// Lookup resolves ONE candidate by its scraper-native id and returns it WITHOUT
// writing anything (METAPARITY-4, ADR-0068 §A). It is the read half Apply hides
// inside itself, exposed for the two Fix-Match surfaces that need a candidate
// before any mutation: the paste-an-id direct fetch, and the preview-before-apply
// diff. mediaID is optional and only sources the engine's fingerprint variables,
// exactly as in Search.
func (s *Service) Lookup(ctx context.Context, pluginID, scraperID, candidateID string, mediaID store.MediaID) (Candidate, error) {
	if strings.TrimSpace(candidateID) == "" {
		return Candidate{}, errors.New("identify: candidate ID is required")
	}
	_, spec, err := s.spec(pluginID, scraperID)
	if err != nil {
		return Candidate{}, err
	}
	vars, err := s.variables(ctx, mediaID)
	if err != nil {
		return Candidate{}, err
	}
	vars.PluginID = pluginID
	result, err := s.engine.Lookup(ctx, spec, candidateID, vars)
	if err != nil {
		return Candidate{}, err
	}
	return candidateFromResult(result), nil
}

// variables loads mediaID's content-identity values for the engine's
// fingerprint substitution. Empty mediaID or no wired store substitutes
// empty values; an unknown Media item is an error — a typo'd ID should not
// silently degrade into an empty-fingerprint query.
func (s *Service) variables(ctx context.Context, mediaID store.MediaID) (scrape.Variables, error) {
	if mediaID == "" || s.fingerprints == nil {
		return scrape.Variables{}, nil
	}
	fp, err := s.fingerprints.MediaFingerprints(ctx, mediaID)
	if err != nil {
		return scrape.Variables{}, err
	}
	return scrape.Variables{
		OSHash:          fp.OSHash,
		PHash:           fp.PHash,
		DurationSeconds: fp.DurationSeconds,
	}, nil
}

// Apply looks up the chosen candidate and writes its title/summary/year onto
// the Media row. The engine already returns transformed values; a year that
// does not parse to an int is omitted, like an unresolved path. Visibility
// gating belongs to the caller — this is the server-internal write path.
func (s *Service) Apply(ctx context.Context, mediaID store.MediaID, pluginID, scraperID, candidateID string) error {
	if mediaID == "" {
		return errors.New("identify: media ID is required")
	}
	if strings.TrimSpace(candidateID) == "" {
		return errors.New("identify: candidate ID is required")
	}
	bundle, spec, err := s.spec(pluginID, scraperID)
	if err != nil {
		return err
	}
	vars, err := s.variables(ctx, mediaID)
	if err != nil {
		return err
	}
	vars.PluginID = pluginID
	result, err := s.engine.Lookup(ctx, spec, candidateID, vars)
	if err != nil {
		return err
	}
	metadata := metadataFromResult(result)
	if metadata.Title == nil && metadata.Summary == nil && metadata.Year == nil {
		return fmt.Errorf("identify: scraper %q returned no usable fields", scraperID)
	}
	if err := s.store.UpdateMediaMetadata(ctx, mediaID, metadata); err != nil {
		return err
	}
	if err := s.applyMediaFields(ctx, mediaID, bundle, spec, result); err != nil {
		return err
	}
	if err := s.applyEntities(ctx, mediaID, bundle, spec, result); err != nil {
		return err
	}
	s.applyArtwork(ctx, mediaID, pluginID, scraperID, result)
	return nil
}

// applyMediaFields writes the scraper's plugin-declared Media field values
// onto the Media row, from two routes that share the BF-24 namespacing
// contract (plugin.MediaFieldKeyPrefix):
//
//   - ImpliedMediaFields: constants the choice of scraper implies (ADR-0028
//     §1's per-file class assignment — picking a particular scraper IS the
//     classification it stands for). Always text.
//   - "media.<name>" FieldMap keys: values resolved from the lookup document,
//     parsed to the declared FieldSpec type. An unparseable value is skipped
//     and logged, never failing the identification — the same posture the
//     engine takes for numeric coercion and unresolved paths.
//
// These fields are identification-owned and written at the operator
// (identified) tier via UpsertMediaFieldsWithSource: a re-identify (also
// operator) heals them idempotently, but a manual edit (higher tier) is
// preserved — the source-precedence record subsumes the per-field locks.
// The bare built-in keys (title/summary/year) are written by
// UpdateMediaMetadata, not here.
//
// Unlike artwork this is a local store write, so failures propagate: a
// half-applied identification (metadata without its plugin fields) should be
// visible, and re-running Apply heals it.
func (s *Service) applyMediaFields(ctx context.Context, mediaID store.MediaID, bundle plugin.Bundle, spec plugin.ScraperSpec, result scrape.Result) error {
	mediaFieldTypes := bundle.MediaFieldTypes()
	// values collects field name → typed value, last writer wins. A document
	// "media.<name>" value overrides the implied constant of the same name —
	// the document is more specific than the scraper-choice default.
	values := map[string]store.MediaField{}
	for key, constant := range spec.ImpliedMediaFields {
		route, err := plugin.ClassifyMediaFieldKey(key, mediaFieldTypes)
		if err != nil {
			// Validation accepts the bundle before activation, so a wired
			// bundle's keys always classify; a malformed test bundle is the
			// only way here — skip rather than write a mis-keyed field.
			slog.Debug("identify: implied field key did not classify", "scraper", spec.ID, "key", key, "error", err.Error())
			continue
		}
		values[route.MediaField] = store.MediaField{Name: route.MediaField, Type: store.EntityFieldText, Text: constant}
	}
	for key := range result.Fields {
		route, err := plugin.ClassifyMediaFieldKey(key, mediaFieldTypes)
		if err != nil || route.Builtin || route.CandidateSignal {
			continue // built-ins go through UpdateMediaMetadata; candidate-signal keys (popularity/sequence/index) feed the scorer only; bad keys can't occur on a validated bundle
		}
		// The scrape engine stores already-coerced values (string/number/
		// flag) keyed by the FieldMap key; fieldString renders any of them
		// back to its string form — the same seam built-in title/summary/year
		// reads through — so mediaFieldFromValue re-parses to the declared
		// FieldSpec type. (The scraper's own FieldTypes coercion is keyed by
		// the raw "media.<name>" key and is independent of the FieldSpec type.)
		raw := fieldString(result.Fields, key)
		field, ok := mediaFieldFromValue(route.MediaField, route.Type, raw)
		if !ok {
			slog.Debug("identify: media field value not parseable, skipped",
				"scraper", spec.ID, "field", route.MediaField, "type", route.Type, "value", raw)
			continue
		}
		values[route.MediaField] = field
	}
	if len(values) == 0 {
		return nil
	}
	if s.fields == nil {
		return fmt.Errorf("identify: scraper %q writes media fields but no field store is wired", spec.ID)
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make([]store.MediaField, len(names))
	for i, name := range names {
		fields[i] = values[name]
	}
	return s.fields.UpsertMediaFieldsWithSource(ctx, mediaID, fields, store.MutationSourceOperator)
}

// mediaFieldFromValue parses one scraped string into a typed MediaField for
// the named field of the declared type. An unparseable value yields ok=false
// (the caller skips and logs); blank values are dropped too — an absent field
// is cleaner than an empty one. Date and number inputs arrive already
// normalized by the scraper's Transforms (date-from-rfc822 → "YYYY-MM-DD",
// seconds-from-clock → integer seconds), so the layouts here mirror those.
func mediaFieldFromValue(name, fieldType, raw string) (store.MediaField, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return store.MediaField{}, false
	}
	switch store.EntityFieldType(fieldType) {
	case store.EntityFieldText, store.EntityFieldURL:
		return store.MediaField{Name: name, Type: store.EntityFieldType(fieldType), Text: value}, true
	case store.EntityFieldNumber:
		number, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return store.MediaField{}, false
		}
		return store.MediaField{Name: name, Type: store.EntityFieldNumber, Number: number}, true
	case store.EntityFieldFlag:
		flag, err := strconv.ParseBool(value)
		if err != nil {
			return store.MediaField{}, false
		}
		return store.MediaField{Name: name, Type: store.EntityFieldFlag, Flag: flag}, true
	case store.EntityFieldDate:
		for _, layout := range []string{"2006-01-02", time.RFC3339, time.RFC3339Nano} {
			if when, err := time.Parse(layout, value); err == nil {
				return store.MediaField{Name: name, Type: store.EntityFieldDate, Date: when.UTC()}, true
			}
		}
		return store.MediaField{}, false
	default:
		return store.MediaField{}, false
	}
}

// applyEntities upserts the lookup document's hydrated Entities into the
// Media's Library and links each to the Media through its declared relation
// (ADR-0003 §3's field → Entity Kind relation mapping). Like applyImpliedFields
// this is a local store write, so failures propagate: a half-hydrated
// identification stays visible and re-running Apply heals it — every write is
// idempotent because the Entity ID is derived from (library, kind, name) and
// relation rows upsert on their (from, name, to) key. Each relation carries
// the cardinality its RelationSpec declares so the store can enforce "one"
// (re-identifying a Media moves it to the new container instead of linking
// both). SortOrder comes from array-backed EntityMap results; scalar maps stay
// unordered. Entity posters ride along best-effort (applyEntityPoster) and
// never fail hydration.
func (s *Service) applyEntities(ctx context.Context, mediaID store.MediaID, bundle plugin.Bundle, spec plugin.ScraperSpec, result scrape.Result) error {
	if len(result.Entities) == 0 {
		return nil
	}
	if s.entities == nil {
		return fmt.Errorf("identify: scraper %q hydrates entities but no entity store is wired", spec.ID)
	}
	library, err := s.entities.MediaLibrary(ctx, mediaID)
	if err != nil {
		return err
	}
	cardinality := relationCardinalities(bundle)
	ordinalFields := relationOrdinalFields(spec)
	for _, hydrated := range result.Entities {
		id := entityIDForName(library, hydrated.Kind, hydrated.Name)
		if err := s.entities.UpsertEntity(ctx, store.Entity{
			ID:        id,
			LibraryID: library,
			Kind:      hydrated.Kind,
			Title:     hydrated.Name,
			Fields:    entityFields(bundle, hydrated),
		}); err != nil {
			return err
		}
		if err := s.entities.UpsertEntityRelation(ctx, store.EntityRelation{
			FromEntityID: id,
			Name:         hydrated.Relation,
			ToMediaID:    mediaID,
			Cardinality:  cardinality[hydrated.Kind+"\x00"+hydrated.Relation],
			SortOrder:    relationSortOrder(hydrated, ordinalFields),
		}); err != nil {
			return err
		}
		if err := s.applyEntityParent(ctx, library, id, hydrated); err != nil {
			return err
		}
		s.applyEntityPoster(ctx, id, hydrated.ArtworkURL)
	}
	return nil
}

// entityFields parses one hydrated Entity's raw FieldMap values against its
// Kind's declared FieldSpec types — the same value→typed-field split Media
// fields take (mediaFieldFromValue). An unparseable or undeclared value is
// skipped and logged, never failing hydration: the FieldMap posture. Returned
// fields are name-sorted so re-applies produce a stable field set.
func entityFields(bundle plugin.Bundle, hydrated scrape.EntityResult) []store.EntityField {
	if len(hydrated.Fields) == 0 {
		return nil
	}
	fieldTypes := bundle.EntityFieldTypes(hydrated.Kind)
	names := make([]string, 0, len(hydrated.Fields))
	for name := range hydrated.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make([]store.EntityField, 0, len(names))
	for _, name := range names {
		fieldType, declared := fieldTypes[name]
		if !declared {
			// Validation accepts the bundle before activation, so a wired
			// bundle's FieldMap keys always name a declared field; only a
			// malformed test bundle reaches here — skip rather than write
			// an untyped field.
			slog.Debug("identify: entity field not declared on kind, skipped",
				"kind", hydrated.Kind, "field", name)
			continue
		}
		field, ok := mediaFieldFromValue(name, fieldType, hydrated.Fields[name])
		if !ok {
			slog.Debug("identify: entity field value not parseable, skipped",
				"kind", hydrated.Kind, "field", name, "type", fieldType, "value", hydrated.Fields[name])
			continue
		}
		fields = append(fields, store.EntityField(field))
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// applyEntityParent hydrates a hydrated Entity's same-Kind parent (hierarchy)
// and links child→parent under the declared parent relation. The parent is a
// bare same-Kind Entity keyed by (library, kind, parent-name) — the same
// idempotent keying as the child, so re-applies and a parent that also appears
// as its own hydrated Entity converge on one row. No parent name means a
// root-level Entity; nothing is written.
func (s *Service) applyEntityParent(ctx context.Context, library store.LibraryID, childID store.EntityID, hydrated scrape.EntityResult) error {
	if hydrated.ParentName == "" || hydrated.ParentRelation == "" {
		return nil
	}
	parentID := entityIDForName(library, hydrated.Kind, hydrated.ParentName)
	if parentID == childID {
		return nil // an Entity is not its own parent; skip the self-link
	}
	if err := s.entities.UpsertEntity(ctx, store.Entity{
		ID:        parentID,
		LibraryID: library,
		Kind:      hydrated.Kind,
		Title:     hydrated.ParentName,
	}); err != nil {
		return err
	}
	return s.entities.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: childID,
		Name:         hydrated.ParentRelation,
		ToEntityID:   parentID,
	})
}

// relationCardinalities indexes the bundle's declared relation cardinalities
// by kind\x00relation (the EntityMap addressing). Undeclared pairs resolve to
// "" — the store's default-many.
func relationCardinalities(bundle plugin.Bundle) map[string]string {
	cardinality := map[string]string{}
	for _, kind := range bundle.EntityKinds {
		for _, relation := range kind.Relations {
			if relation.Cardinality != "" {
				cardinality[kind.Name+"\x00"+relation.Name] = relation.Cardinality
			}
		}
	}
	return cardinality
}

// relationOrdinalFields indexes the scraper's declared OrdinalField by
// kind\x00relation (the EntityMap addressing, the relationCardinalities shape):
// for an EntityMap that declares one, the FieldMap key whose resolved value is
// the relation's ordinal. A pair with no OrdinalField is absent from the map.
func relationOrdinalFields(spec plugin.ScraperSpec) map[string]string {
	fields := map[string]string{}
	for _, entityMap := range spec.EntityMaps {
		if entityMap.OrdinalField != "" {
			fields[entityMap.Kind+"\x00"+entityMap.Relation] = entityMap.OrdinalField
		}
	}
	return fields
}

// relationSortOrder resolves a hydrated Entity's relation sort position. When
// its EntityMap declared an OrdinalField and the resolved value parses as an
// integer, that value is the ordinal (a plugin-supplied position number) — the
// source that makes OrderedChildren and next-up land in the intended order
// rather than mere document position. A missing or non-numeric ordinal falls
// back to the array-index SortOrder the scrape engine set, the FieldMap "omit,
// never error" posture.
func relationSortOrder(hydrated scrape.EntityResult, ordinalFields map[string]string) *int {
	field, ok := ordinalFields[hydrated.Kind+"\x00"+hydrated.Relation]
	if !ok {
		return hydrated.SortOrder
	}
	raw, present := hydrated.Fields[field]
	if !present {
		return hydrated.SortOrder
	}
	ordinal, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return hydrated.SortOrder
	}
	return &ordinal
}

// applyEntityPoster downloads and stores one hydrated Entity's poster.
// Best-effort like applyPoster: the entity write already succeeded, so
// artwork failures only log. Entities that already carry a poster are
// skipped — re-applying an identification must not re-download artwork
// (the same idempotence posture as the entity writes themselves).
func (s *Service) applyEntityPoster(ctx context.Context, id store.EntityID, artworkURL string) {
	if artworkURL == "" || s.fetcher == nil || s.artwork == nil {
		return
	}
	if s.artwork.HasEntityPoster(id) {
		return
	}
	data, err := s.fetcher.FetchResource(ctx, artworkURL)
	if err != nil {
		slog.Debug("identify: entity poster fetch failed", "entity", string(id), "error", err.Error())
		return
	}
	if err := s.artwork.ImportEntityPoster(ctx, id, data); err != nil {
		slog.Debug("identify: entity poster import failed", "entity", string(id), "error", err.Error())
	}
}

// EntityIDForName derives the durable Entity ID for one hydrated Entity:
// the same (Library, kind, name) always maps to the same Entity, which is
// what makes hydration idempotent across scrapers and re-applies (the same
// keying posture as mediaIDForFingerprint). The truncated 128-bit form is
// identity-bearing and persisted: do not widen or change it, or re-applies
// would silently fork existing Entities.
//
// Exported so every entity-hydration path (identification here, scan-side music
// tag hydration in the composition root — ADR-0098 area 1) derives Entity IDs
// from the one canonical formula; a divergent re-implementation would fork
// existing Artist/Album/Genre rows.
func EntityIDForName(library store.LibraryID, kind, name string) store.EntityID {
	sum := sha256.Sum256([]byte(string(library) + "\x00" + kind + "\x00" + name))
	return store.EntityID("entity-" + hex.EncodeToString(sum[:16]))
}

// entityIDForName is the internal alias kept so existing call sites read
// unchanged; it delegates to the exported canonical derivation.
func entityIDForName(library store.LibraryID, kind, name string) store.EntityID {
	return EntityIDForName(library, kind, name)
}

// applyArtwork downloads and stores known looked-up artwork slots.
// Best-effort: the metadata write already succeeded, so artwork failures
// only log — a missing poster regenerates from frames on the next sweep
// anyway, and scraped backdrops/thumbs can be retried by re-applying.
func (s *Service) applyArtwork(ctx context.Context, mediaID store.MediaID, pluginID, scraperID string, result scrape.Result) {
	if len(result.Artwork) == 0 || s.fetcher == nil || s.artwork == nil {
		return
	}
	for _, slot := range artwork.KnownSlots {
		rawURL := result.Artwork[slot]
		if rawURL == "" {
			continue
		}
		if s.candidates != nil {
			if err := s.candidates.UpsertArtworkCandidate(ctx, mediaID, slot, rawURL, pluginID, scraperID); err != nil {
				slog.Debug("identify: artwork candidate upsert failed",
					"media", string(mediaID), "slot", slot, "error", err.Error())
			}
		}
		if slot == artwork.SlotPoster && (!s.artwork.HasArtwork(mediaID, slot) || s.artwork.IsGeneratedArtwork(mediaID, slot)) {
			if embedded, ok := s.artwork.(embeddedArtworkStore); ok {
				found, err := embedded.ImportEmbeddedPoster(ctx, mediaID)
				if err != nil {
					slog.Debug("identify: embedded artwork import failed", "media", string(mediaID), "error", err.Error())
				} else if found {
					continue
				}
			}
		}
		if slot == artwork.SlotPoster && s.artwork.HasArtwork(mediaID, slot) && !s.artwork.IsGeneratedArtwork(mediaID, slot) {
			continue
		}
		data, err := s.fetcher.FetchResource(ctx, rawURL)
		if err != nil {
			slog.Debug("identify: artwork fetch failed",
				"media", string(mediaID), "slot", slot, "error", err.Error())
			continue
		}
		if err := s.artwork.ImportArtwork(ctx, mediaID, slot, data); err != nil {
			slog.Debug("identify: artwork import failed",
				"media", string(mediaID), "slot", slot, "error", err.Error())
			continue
		}
		if s.candidates != nil {
			id := store.ArtworkCandidateID(mediaID, slot, rawURL)
			if err := s.candidates.MarkArtworkCandidateApplied(ctx, id); err != nil {
				slog.Debug("identify: artwork candidate apply mark failed",
					"media", string(mediaID), "slot", slot, "error", err.Error())
			}
		}
	}
}

// spec resolves pluginID → activated bundle → declared scraper. The bundle
// rides along because Apply reads its EntityKinds (relation cardinality).
func (s *Service) spec(pluginID, scraperID string) (plugin.Bundle, plugin.ScraperSpec, error) {
	bundle, ok := s.bundles.Bundle(plugin.ID(pluginID))
	if !ok {
		return plugin.Bundle{}, plugin.ScraperSpec{}, fmt.Errorf("identify: unknown plugin %q", pluginID)
	}
	for _, scraper := range bundle.Scrapers {
		if scraper.ID == scraperID {
			return bundle, scraper, nil
		}
	}
	return plugin.Bundle{}, plugin.ScraperSpec{}, fmt.Errorf("identify: plugin %q declares no scraper %q", pluginID, scraperID)
}

func candidateFromResult(result scrape.Result) Candidate {
	candidate := Candidate{
		ID:      result.ID,
		Title:   fieldString(result.Fields, "title"),
		Summary: fieldString(result.Fields, "summary"),
	}
	if year, ok := fieldYear(result.Fields, "year"); ok {
		candidate.Year = year
	}
	// Ordinal (Sequence/Index) and the popularity tiebreak flow through the same
	// declared-FieldMap mechanism as title/year: a scraper that carries them
	// populates these; a search that does not leaves them zero (the non-signal
	// the scorer treats as "no ordinal" / "no popularity"). Shape-named, never by
	// domain (invariant #1).
	candidate.Sequence = fieldInt(result.Fields, "sequence")
	candidate.Index = fieldInt(result.Fields, "index")
	candidate.Popularity = fieldFloat(result.Fields, "popularity")
	if len(result.Artwork) > 0 {
		candidate.Artwork = result.Artwork
	}
	return candidate
}

func metadataFromResult(result scrape.Result) store.MediaMetadata {
	var metadata store.MediaMetadata
	if title := strings.TrimSpace(fieldString(result.Fields, "title")); title != "" {
		metadata.Title = &title
	}
	if summary := strings.TrimSpace(fieldString(result.Fields, "summary")); summary != "" {
		metadata.Summary = &summary
	}
	if year, ok := fieldYear(result.Fields, "year"); ok {
		metadata.Year = &year
	}
	return metadata
}

func fieldString(fields map[string]any, name string) string {
	switch value := fields[name].(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		if value == float64(int(value)) {
			return strconv.Itoa(int(value))
		}
		return strconv.FormatFloat(value, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(value)
	default:
		return ""
	}
}

func fieldYear(fields map[string]any, name string) (int, bool) {
	switch value := fields[name].(type) {
	case int:
		return positiveYear(value)
	case int64:
		if value > int64(^uint(0)>>1) {
			return 0, false
		}
		return positiveYear(int(value))
	case float64:
		if value != float64(int(value)) {
			return 0, false
		}
		return positiveYear(int(value))
	default:
		return parseYear(fieldString(fields, name))
	}
}

func positiveYear(year int) (int, bool) {
	if year <= 0 {
		return 0, false
	}
	return year, true
}

// fieldInt reads a scraper field as a non-negative integer, tolerating the
// numeric and string JSON shapes fieldString already normalizes. 0 (the
// scorer's non-signal) covers absent, non-numeric, and negative values.
func fieldInt(fields map[string]any, name string) int {
	switch value := fields[name].(type) {
	case int:
		return nonNegative(value)
	case int64:
		return nonNegative(int(value))
	case float64:
		return nonNegative(int(value))
	default:
		n, err := strconv.Atoi(strings.TrimSpace(fieldString(fields, name)))
		if err != nil {
			return 0
		}
		return nonNegative(n)
	}
}

func nonNegative(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// fieldFloat reads a scraper field as a non-negative float (the popularity
// tiebreak signal). Absent, non-numeric, or negative values are 0 — the
// scorer's "no popularity" non-signal.
func fieldFloat(fields map[string]any, name string) float64 {
	switch value := fields[name].(type) {
	case float64:
		return nonNegativeFloat(value)
	case int:
		return nonNegativeFloat(float64(value))
	case int64:
		return nonNegativeFloat(float64(value))
	default:
		f, err := strconv.ParseFloat(strings.TrimSpace(fieldString(fields, name)), 64)
		if err != nil {
			return 0
		}
		return nonNegativeFloat(f)
	}
}

func nonNegativeFloat(f float64) float64 {
	if f < 0 {
		return 0
	}
	return f
}

// parseYear parses a scraper year field; unparseable values are omitted, the
// same posture the engine takes for transform failures.
func parseYear(value string) (int, bool) {
	year, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || year <= 0 {
		return 0, false
	}
	return year, true
}
