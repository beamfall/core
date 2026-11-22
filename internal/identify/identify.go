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
	"maps"
	"math"
	"slices"
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

// MetadataStore commits an identification's local Media changes together.
type MetadataStore interface {
	ApplyMediaIdentification(ctx context.Context, id store.MediaID, metadata store.MediaMetadata, provenance store.MediaMetadataProvenance, fields []store.MediaField) error
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
// re-downloading entity posters that already landed; EntityPosterInherited
// tells a poster the Entity borrowed from its own Media apart from one that
// was supplied for it, so provider artwork still replaces the former.
type ArtworkStore interface {
	ImportArtwork(ctx context.Context, id store.MediaID, slot string, data []byte) error
	HasArtwork(id store.MediaID, slot string) bool
	IsGeneratedArtwork(id store.MediaID, slot string) bool
	HasEntityPoster(id store.EntityID) bool
	EntityPosterInherited(id store.EntityID) bool
	ImportEntityPoster(ctx context.Context, id store.EntityID, data []byte) error
}

// artworkLockReader reads a field's recorded write source; *store.Store
// satisfies it. When the metadata store implements it, an artwork slot an
// admin locked (manual source on store.ArtworkLockField(slot)) is not
// refetched. A store without it never locks artwork.
type artworkLockReader interface {
	MediaFieldProvenance(ctx context.Context, id store.MediaID, field string) (store.MutationSource, error)
}

var _ artworkLockReader = (*store.Store)(nil)

type embeddedArtworkStore interface {
	ImportEmbeddedPoster(ctx context.Context, id store.MediaID) (bool, error)
}

// sidecarLockReader reports a slot locked by a local sidecar marker;
// *artwork.Service satisfies it. ImportArtwork refuses such a slot anyway,
// so the probe only saves the fetch.
type sidecarLockReader interface {
	IsArtworkLocked(id store.MediaID, slot string) bool
}

// embeddedArtworkReader tells a poster extracted from the Media file apart
// from fetched or supplied artwork; *artwork.Service satisfies it.
type embeddedArtworkReader interface {
	IsEmbeddedArtwork(id store.MediaID, slot string) bool
}

// importedArtworkReader reports artwork stored through ImportArtwork or a
// manual pick; *artwork.Service satisfies it.
type importedArtworkReader interface {
	IsImportedArtwork(id store.MediaID, slot string) bool
}

// conditionalArtworkStore rechecks an import decision under the slot's
// directory lock; *artwork.Service satisfies it.
type conditionalArtworkStore interface {
	ImportArtworkIf(ctx context.Context, id store.MediaID, slot string, data []byte, ok func() bool) (bool, error)
}

// appliedCandidateReader lists a slot's artwork candidates; *store.Store
// satisfies it.
type appliedCandidateReader interface {
	ArtworkCandidates(ctx context.Context, mediaID store.MediaID, slot string, filter store.LibraryFilter) ([]store.ArtworkCandidate, error)
}

// mediaClassReader reads a Media item's playback class; *store.Store
// satisfies it.
type mediaClassReader interface {
	MediaClass(ctx context.Context, id store.MediaID) (store.MediaClass, error)
}

var (
	_ embeddedArtworkStore    = (*artwork.Service)(nil)
	_ embeddedArtworkReader   = (*artwork.Service)(nil)
	_ importedArtworkReader   = (*artwork.Service)(nil)
	_ conditionalArtworkStore = (*artwork.Service)(nil)
	_ sidecarLockReader       = (*artwork.Service)(nil)
	_ appliedCandidateReader  = (*store.Store)(nil)
	_ mediaClassReader        = (*store.Store)(nil)
)

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
	UpsertHydratedEntityRelation(ctx context.Context, relation store.EntityRelation) error
}

// MediaTitleSource supplies a Media item's parser-owned display title: the
// per-file title a plugin's filename parser gives a file whose parsed title
// names a container shared by several files (scan.MediaTitler). It returns ""
// when the plugin's parser supplies none.
type MediaTitleSource interface {
	ParsedMediaTitle(ctx context.Context, mediaID store.MediaID, pluginID string) (string, error)
}

// IdentifiedMediaTitleSource is optionally implemented by a MediaTitleSource.
// It supplies the parser's per-file display title built from an
// identification's plugin Media field values (bare name to text), so the title
// can name what the lookup found ("S03E01 · Smells Like Mean Spirit"). It
// returns "" when the parser builds none.
type IdentifiedMediaTitleSource interface {
	IdentifiedMediaTitle(ctx context.Context, mediaID store.MediaID, pluginID string, fields map[string]string) (string, error)
}

// MediaOrdinalSource is optionally implemented by a MediaTitleSource. It
// supplies the plugin parser's ordinal for a Media item: the sequence the
// file sits in and its index within it, substituted for a scraper's
// {sequence} and {index} placeholders. It returns "" and 0 when the parser
// supplies none.
type MediaOrdinalSource interface {
	ParsedMediaOrdinal(ctx context.Context, mediaID store.MediaID, pluginID string) (sequence string, index int, err error)
}

// MediaHintSource is optionally implemented by a MediaTitleSource. It
// supplies the named search hints the plugin parser gives a Media item, read
// for a manual search so a SearchTemplate's {hint.<key>} placeholders carry
// the item's values (CORE-2333). It returns nil when the parser supplies
// none.
type MediaHintSource interface {
	ParsedMediaHints(ctx context.Context, mediaID store.MediaID, pluginID string) (map[string]string, error)
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
	entities     EntityStore
	fingerprints FingerprintStore
	titles       MediaTitleSource
	// scorers binds each plugin's MatchScorer + auto-apply threshold by plugin
	// ID; empty until the composition root registers one.
	scorers map[string]scorerBinding
	// queue parks below-threshold scan-time matches; pins
	// reads the FREEZE-3 import match-pin so rescans respect an operator fix. Both
	// nil until SetAutoStores wires them — the manual flow never touches them.
	queue             UnmatchedStore
	pins              MatchPinStore
	optionalAuthSkips sync.Map
	// writes serializes ordinal changes, lookups and hydration per Media item.
	writes mediaWriteLocks
}

// mediaWriteLocks hands out one mutex per Media item, dropping it once no
// Apply holds or waits on it.
type mediaWriteLocks struct {
	mu    sync.Mutex
	locks map[store.MediaID]*mediaWriteLock
}

type mediaWriteLock struct {
	sync.Mutex
	refs int
}

func (l *mediaWriteLocks) lock(id store.MediaID) func() {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = map[store.MediaID]*mediaWriteLock{}
	}
	entry := l.locks[id]
	if entry == nil {
		entry = &mediaWriteLock{}
		l.locks[id] = entry
	}
	entry.refs++
	l.mu.Unlock()
	entry.Lock()
	return func() {
		entry.Unlock()
		l.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(l.locks, id)
		}
		l.mu.Unlock()
	}
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

// SetMediaTitles wires parser-owned display titles. A scraper's title names
// the same container the parsed title does (the identification query), so
// when the plugin's parser supplies a per-file title for the Media, Apply
// writes that title instead of the scraper's and the identified Media stays
// distinguishable from its siblings. Nil (the default) writes the scraper's
// title.
func (s *Service) SetMediaTitles(titles MediaTitleSource) {
	s.titles = titles
}

// AppliedTitle is the title Apply writes for a scraper title: the plugin
// parser's per-file title for mediaID when one is wired and non-blank, else
// the scraper's. A blank scraper title stays blank (Apply writes no title).
// A failed title read (say, a Media whose files are all missing) keeps the
// scraper's title rather than failing the identification. The Fix-Match
// preview reads it so the proposed title is the one Apply keeps.
func (s *Service) AppliedTitle(ctx context.Context, mediaID store.MediaID, pluginID, scraperTitle string) string {
	return s.appliedTitle(ctx, mediaID, pluginID, scraperTitle, nil)
}

// appliedTitle is AppliedTitle given the identification's plugin Media field
// values. When there are some and the title source builds a non-blank title
// from them, that title wins; otherwise the parser's per-file title, else the
// scraper's.
func (s *Service) appliedTitle(ctx context.Context, mediaID store.MediaID, pluginID, scraperTitle string, fields map[string]string) string {
	scraperTitle = strings.TrimSpace(scraperTitle)
	if scraperTitle == "" || s.titles == nil || mediaID == "" {
		return scraperTitle
	}
	if source, ok := s.titles.(IdentifiedMediaTitleSource); ok && len(fields) > 0 {
		title, err := source.IdentifiedMediaTitle(ctx, mediaID, pluginID, fields)
		if err != nil {
			slog.Debug("identify: identified parser title unavailable", "media", mediaID, "error", err.Error())
		} else if title = strings.TrimSpace(title); title != "" {
			return title
		}
	}
	title, err := s.titles.ParsedMediaTitle(ctx, mediaID, pluginID)
	if err != nil {
		slog.Debug("identify: parser title unavailable, keeping scraper title", "media", mediaID, "error", err.Error())
		return scraperTitle
	}
	if title = strings.TrimSpace(title); title != "" {
		return title
	}
	return scraperTitle
}

// Search resolves the plugin's scraper spec and returns the engine's
// candidates mapped onto the core metadata fields. mediaID is the Media item
// being identified; it sources the engine's fingerprint variables and may be
// empty (free-text-only search). The query may be empty when mediaID is set —
// a fingerprint-driven scraper queries by content identity, not text.
func (s *Service) Search(ctx context.Context, pluginID, scraperID, query string, mediaID store.MediaID) ([]Candidate, error) {
	return s.search(ctx, pluginID, scraperID, query, mediaID, 0, s.mediaHints(ctx, mediaID, pluginID))
}

// SearchYear is Search with a year the operator supplied, filling a
// SearchTemplate's {year} slot with the same no-results retry without it.
// A year of zero or less behaves exactly like Search.
func (s *Service) SearchYear(ctx context.Context, pluginID, scraperID, query string, mediaID store.MediaID, year int) ([]Candidate, error) {
	return s.search(ctx, pluginID, scraperID, query, mediaID, max(year, 0), s.mediaHints(ctx, mediaID, pluginID))
}

// search is Search with the item's year when identification knows it (zero
// when not), substituted for a SearchTemplate's {year}. A year-filtered
// search that finds nothing runs once more without the year: a file's year
// can be a later release or air date, and a filter that excludes every
// candidate must not hide the match an unfiltered search finds. hints are
// the item's named search hints; a scraper whose RequiredSearchHints they do
// not all supply is skipped and finds no candidates.
func (s *Service) search(ctx context.Context, pluginID, scraperID, query string, mediaID store.MediaID, year int, hints map[string]string) ([]Candidate, error) {
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
	vars.Hints = hints
	usesYear := year > 0 && strings.Contains(spec.SearchTemplate, "{year}")
	if usesYear {
		vars.Year = year
	}
	results, err := s.engine.Search(ctx, spec, query, vars)
	if err != nil {
		return nil, err
	}
	if usesYear && len(results) == 0 {
		vars.Year = 0
		if results, err = s.engine.Search(ctx, spec, query, vars); err != nil {
			return nil, err
		}
	}
	candidates := make([]Candidate, len(results))
	for i, result := range results {
		candidates[i] = candidateFromResult(result)
	}
	return candidates, nil
}

// ListChildren reads a declared level using the active bundle and scraper.
func (s *Service) ListChildren(ctx context.Context, pluginID, scraperID, candidateID string, level int, parentKey *string) ([]scrape.ChildRow, error) {
	_, spec, err := s.spec(pluginID, scraperID)
	if err != nil {
		return nil, err
	}
	if len(spec.Children) == 0 {
		return nil, scrape.ErrNoChildListing
	}
	engine, ok := s.engine.(interface {
		ListChildren(context.Context, plugin.ScraperSpec, int, string, *string, scrape.Variables) ([]scrape.ChildRow, error)
	})
	if !ok {
		return nil, errors.New("identify: child listings unavailable")
	}
	return engine.ListChildren(ctx, spec, level, candidateID, parentKey, scrape.Variables{PluginID: pluginID})
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
	_, _, result, err := s.lookup(ctx, pluginID, scraperID, candidateID, mediaID, nil, false)
	if err != nil {
		return Candidate{}, err
	}
	return candidateFromResult(result), nil
}

// PreviewLookup is Lookup plus the title Apply would write for the candidate:
// the parser's title built from the lookup's plugin Media field values when
// the title source builds one, else AppliedTitle's. The Fix-Match preview
// reads it so the proposed title is the one Apply writes.
// clearLocks resolves the operator ordinal exactly as ApplyFixMatch does.
func (s *Service) PreviewLookup(ctx context.Context, pluginID, scraperID, candidateID string, mediaID store.MediaID, clearLocks []string, ordinal *store.MediaOrdinal) (Candidate, string, error) {
	if strings.TrimSpace(candidateID) == "" {
		return Candidate{}, "", errors.New("identify: candidate ID is required")
	}
	clearOrdinal, err := s.shouldClearOrdinal(ctx, mediaID, candidateID, clearLocks, ordinal)
	if err != nil {
		return Candidate{}, "", err
	}
	bundle, spec, result, err := s.lookup(ctx, pluginID, scraperID, candidateID, mediaID, ordinal, clearOrdinal)
	if err != nil {
		return Candidate{}, "", err
	}
	candidate := candidateFromResult(result)
	title := s.appliedTitle(ctx, mediaID, pluginID, candidate.Title, identifiedFieldTexts(bundle, spec, result))
	return candidate, title, nil
}

// lookup resolves the scraper and runs the engine's lookup for candidateID
// with mediaID's fingerprint and effective ordinal variables.
func (s *Service) lookup(ctx context.Context, pluginID, scraperID, candidateID string, mediaID store.MediaID, ordinal *store.MediaOrdinal, clearOrdinal bool) (plugin.Bundle, plugin.ScraperSpec, scrape.Result, error) {
	bundle, spec, err := s.spec(pluginID, scraperID)
	if err != nil {
		return plugin.Bundle{}, plugin.ScraperSpec{}, scrape.Result{}, err
	}
	vars, err := s.variables(ctx, mediaID)
	if err != nil {
		return plugin.Bundle{}, plugin.ScraperSpec{}, scrape.Result{}, err
	}
	vars.PluginID = pluginID
	if ordinal != nil {
		vars.Sequence, vars.Index = ordinal.Sequence, ordinal.Index
	} else {
		s.applyOrdinal(ctx, mediaID, pluginID, &vars, clearOrdinal)
	}
	result, err := s.engine.Lookup(ctx, spec, candidateID, vars)
	if err != nil {
		return plugin.Bundle{}, plugin.ScraperSpec{}, scrape.Result{}, err
	}
	return bundle, spec, result, nil
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

// mediaHints returns the plugin parser's named search hints for mediaID when
// the wired title source supplies them. A failed read searches without hints,
// as a parser that supplies none would.
func (s *Service) mediaHints(ctx context.Context, mediaID store.MediaID, pluginID string) map[string]string {
	source, ok := s.titles.(MediaHintSource)
	if !ok || mediaID == "" {
		return nil
	}
	hints, err := source.ParsedMediaHints(ctx, mediaID, pluginID)
	if err != nil {
		slog.Debug("identify: parser hints unavailable", "media", mediaID, "error", err.Error())
		return nil
	}
	return hints
}

// applyOrdinal reads the manual tier before falling back to the parser. A
// failed read leaves the ordinal unknown.
func (s *Service) applyOrdinal(ctx context.Context, mediaID store.MediaID, pluginID string, vars *scrape.Variables, clearOrdinal bool) {
	if mediaID == "" {
		return
	}
	if source, ok := s.store.(mediaOrdinalReader); ok && !clearOrdinal {
		ordinal, err := source.MediaOperatorOrdinal(ctx, mediaID)
		if err != nil {
			slog.Debug("identify: operator ordinal unavailable", "media", mediaID, "error", err)
			return
		}
		if ordinal != nil {
			vars.Sequence, vars.Index = ordinal.Sequence, ordinal.Index
			return
		}
	}
	source, ok := s.titles.(MediaOrdinalSource)
	if !ok {
		return
	}
	sequence, index, err := source.ParsedMediaOrdinal(ctx, mediaID, pluginID)
	if err != nil {
		slog.Debug("identify: parser ordinal unavailable", "media", mediaID, "error", err)
		return
	}
	vars.Sequence, vars.Index = sequence, index
}

// effectiveOperatorOrdinal is the manual-tier ordinal the lookup used: the
// edit's own, else the stored one unless the edit clears it. A failed read is
// treated as an operator ordinal of unknown value, so container hydration
// cannot write to a container it has not verified.
func (s *Service) effectiveOperatorOrdinal(ctx context.Context, mediaID store.MediaID, edit store.MediaMetadata) *store.MediaOrdinal {
	if edit.OperatorOrdinal != nil || edit.ClearOrdinal {
		return edit.OperatorOrdinal
	}
	source, ok := s.store.(mediaOrdinalReader)
	if !ok {
		return nil
	}
	ordinal, err := source.MediaOperatorOrdinal(ctx, mediaID)
	if err != nil {
		slog.Debug("identify: operator ordinal unavailable for container hydration", "media", mediaID, "error", err)
		return &store.MediaOrdinal{}
	}
	return ordinal
}

type mediaOrdinalReader interface {
	MediaOperatorOrdinal(context.Context, store.MediaID) (*store.MediaOrdinal, error)
}

// Apply looks up the chosen candidate and writes its metadata onto the Media.
// Visibility gating belongs to the caller.
func (s *Service) Apply(ctx context.Context, mediaID store.MediaID, pluginID, scraperID, candidateID string) error {
	unlock := s.writes.lock(mediaID)
	defer unlock()
	return s.apply(ctx, mediaID, pluginID, scraperID, candidateID, store.MediaMetadata{})
}

// ApplyFixMatch uses the prospective ordinal for lookup and commits the manual
// edit with the replacement. No other identification can interleave its
// lookup or change the remembered candidate while this match is being applied.
func (s *Service) ApplyFixMatch(ctx context.Context, mediaID store.MediaID, pluginID, scraperID, candidateID string, clearLocks []string, ordinal *store.MediaOrdinal, actor store.ProfileID) error {
	unlock := s.writes.lock(mediaID)
	defer unlock()
	clearOrdinal, err := s.shouldClearOrdinal(ctx, mediaID, candidateID, clearLocks, ordinal)
	if err != nil {
		return err
	}
	locks := slices.DeleteFunc(slices.Clone(clearLocks), func(field string) bool {
		return field == store.MediaOrdinalLockField
	})
	edit := store.MediaMetadata{
		ClearLocks: locks, OperatorOrdinal: ordinal, OrdinalActor: actor, ClearOrdinal: clearOrdinal,
	}
	if err := s.apply(ctx, mediaID, pluginID, scraperID, candidateID, edit); err != nil {
		return err
	}
	disposition := "kept"
	if ordinal != nil {
		disposition = "set"
	} else if clearOrdinal {
		disposition = "cleared"
	}
	slog.Info("identify: fixed match", "media", mediaID, "operatorOrdinal", disposition)
	return nil
}

func (s *Service) shouldClearOrdinal(ctx context.Context, mediaID store.MediaID, candidateID string, clearLocks []string, ordinal *store.MediaOrdinal) (bool, error) {
	if ordinal != nil {
		return false, nil
	}
	if clearLocks != nil {
		return slices.Contains(clearLocks, store.MediaOrdinalLockField), nil
	}
	reader, ok := s.store.(ProvenanceReader)
	if !ok || mediaID == "" {
		return false, nil
	}
	current, _, err := reader.MediaMetadataProvenance(ctx, mediaID)
	return current.CandidateID != candidateID, err
}

// ClearMediaOrdinal serializes unlock with lookups and writes for the same Media.
func (s *Service) ClearMediaOrdinal(ctx context.Context, mediaID store.MediaID) (bool, error) {
	unlock := s.writes.lock(mediaID)
	defer unlock()
	writer, ok := s.store.(interface {
		ClearMediaOrdinal(context.Context, store.MediaID) (bool, error)
	})
	if !ok {
		return false, errors.New("identify: operator ordinal store unavailable")
	}
	return writer.ClearMediaOrdinal(ctx, mediaID)
}

func (s *Service) apply(ctx context.Context, mediaID store.MediaID, pluginID, scraperID, candidateID string, edit store.MediaMetadata) error {
	if mediaID == "" {
		return errors.New("identify: media ID is required")
	}
	if strings.TrimSpace(candidateID) == "" {
		return errors.New("identify: candidate ID is required")
	}
	bundle, spec, result, err := s.lookup(ctx, pluginID, scraperID, candidateID, mediaID, edit.OperatorOrdinal, edit.ClearOrdinal)
	if err != nil {
		return err
	}
	metadata := metadataFromResult(result)
	if metadata.Title == nil && metadata.Summary == nil && metadata.Year == nil && metadata.SortTitle == nil {
		return fmt.Errorf("identify: scraper %q returned no usable fields", scraperID)
	}
	if metadata.SortTitle == nil {
		// A candidate without a sort title retracts one an earlier
		// identification supplied; the store leaves an admin's value alone.
		none := ""
		metadata.SortTitle = &none
	}
	if metadata.Title != nil {
		title := s.appliedTitle(ctx, mediaID, pluginID, *metadata.Title, identifiedFieldTexts(bundle, spec, result))
		metadata.Title = &title
	}
	metadata.ClearLocks = edit.ClearLocks
	metadata.OperatorOrdinal = edit.OperatorOrdinal
	metadata.OrdinalActor = edit.OrdinalActor
	metadata.ClearOrdinal = edit.ClearOrdinal
	provenance := store.MediaMetadataProvenance{PluginID: pluginID, ScraperID: scraperID, CandidateID: candidateID}
	// A request that cannot hydrate is refused before anything commits, so an
	// ordinal edit is never published by a request that could not finish.
	// Hydration then follows the commit: it reads the committed year and the
	// settled container chain, and a rejected commit leaves every link as it
	// was. Its writes are idempotent, so a retry after a failed write converges.
	if len(result.Entities) > 0 && s.entities == nil {
		return fmt.Errorf("identify: scraper %q hydrates entities but no entity store is wired", spec.ID)
	}
	manual := s.effectiveOperatorOrdinal(ctx, mediaID, edit)
	if err := s.store.ApplyMediaIdentification(ctx, mediaID, metadata, provenance, identifiedMediaFields(bundle, spec, result)); err != nil {
		return err
	}
	if err := s.applyEntities(ctx, mediaID, bundle, spec, result, manual); err != nil {
		return err
	}
	s.applyArtwork(ctx, mediaID, pluginID, scraperID, result)
	return nil
}

// identifiedMediaFields collects the scraper's plugin-declared Media field
// values from two routes that share the BF-24 namespacing
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
// The store applies these values at operator precedence alongside the built-in
// fields and provenance.
func identifiedMediaFields(bundle plugin.Bundle, spec plugin.ScraperSpec, result scrape.Result) []store.MediaField {
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
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make([]store.MediaField, len(names))
	for i, name := range names {
		fields[i] = values[name]
	}
	return fields
}

// identifiedFieldTexts is the text of the plugin-declared Media field values
// an identification writes (identifiedMediaFields' two routes, document values over
// implied constants), by bare field name. The title parser uses these values
// with blanks and values that do not parse to the declared type removed.
func identifiedFieldTexts(bundle plugin.Bundle, spec plugin.ScraperSpec, result scrape.Result) map[string]string {
	mediaFieldTypes := bundle.MediaFieldTypes()
	texts := map[string]string{}
	for key, constant := range spec.ImpliedMediaFields {
		route, err := plugin.ClassifyMediaFieldKey(key, mediaFieldTypes)
		if err != nil {
			continue
		}
		if value := strings.TrimSpace(constant); value != "" {
			texts[route.MediaField] = value
		}
	}
	for key := range result.Fields {
		route, err := plugin.ClassifyMediaFieldKey(key, mediaFieldTypes)
		if err != nil || route.Builtin || route.CandidateSignal {
			continue
		}
		raw := fieldString(result.Fields, key)
		if _, ok := mediaFieldFromValue(route.MediaField, route.Type, raw); ok {
			texts[route.MediaField] = strings.TrimSpace(raw)
		}
	}
	return texts
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
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			// NaN binds as SQL NULL and neither survives the wire, so an
			// unusable number is absent like an unparseable one.
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
				if when.IsZero() || when.UnixMilli() == 0 {
					// The store keeps dates as Unix milliseconds with 0 for
					// no value, so neither Go's zero time nor the epoch
					// round-trips; a write it would refuse later is absent.
					return store.MediaField{}, false
				}
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
// both). SortOrder and the relation attributes come from the scrape engine's
// EntityMap results; links of one Entity to the Media under one relation
// merge into one row first (mergeEntityLinks). An Entity hydrated without
// typed fields keeps the fields an earlier identification wrote. Entity
// posters ride along best-effort (applyEntityPoster) and never fail hydration.
//
// A map onto a container kind's single-holder relation places the Media in
// that container (CORE-3152). Every link records the scraper as its writer
// (relationWriter). A holder link with no ordinal from its map takes the
// Media's stored year, after field precedence, as its sort order, so the
// container lists its members in release order. A lookup whose holder map ran
// and hydrated nothing removes the Media's earlier link, but only one this
// scraper wrote (clearStaleHolderLinks). A holder link keeps an existing
// holder's stored title (writeEntity), and a container map of a kind the scraper
// links applies only to a holder it linked in this lookup: a link an operator's
// lock kept from the scraper does not count.
func (s *Service) applyEntities(ctx context.Context, mediaID store.MediaID, bundle plugin.Bundle, spec plugin.ScraperSpec, result scrape.Result, manual *store.MediaOrdinal) error {
	holders := ContainerRelations(bundle)
	writer := relationWriter(bundle, spec)
	if err := s.clearStaleHolderLinks(ctx, mediaID, spec, writer, holders, result); err != nil {
		return err
	}
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
	year, err := s.holderYear(ctx, mediaID, holders, result)
	if err != nil {
		return err
	}
	// holderLinks has a key for each kind the scraper declares a holder link
	// map onto, listing the holders this lookup linked.
	holderLinks := map[string][]store.EntityID{}
	for _, entityMap := range spec.EntityMaps {
		if !entityMap.Container && holders.Holds(entityMap.Kind, entityMap.Relation) {
			holderLinks[entityMap.Kind] = nil
		}
	}
	var containers, linked []scrape.EntityResult
	for _, hydrated := range result.Entities {
		if hydrated.Container {
			containers = append(containers, hydrated)
		} else {
			linked = append(linked, hydrated)
		}
	}
	for _, hydrated := range mergeEntityLinks(library, linked) {
		id := entityIDForName(library, hydrated.Kind, hydrated.Name)
		entity := store.Entity{
			ID:        id,
			LibraryID: library,
			Kind:      hydrated.Kind,
			Title:     hydrated.Name,
			Fields:    entityFields(bundle, hydrated),
		}
		holds := holders.Holds(hydrated.Kind, hydrated.Relation)
		if err := s.writeEntity(ctx, entity, holds); err != nil {
			return err
		}
		sortOrder := hydrated.SortOrder
		if sortOrder == nil && year != nil && holds {
			sortOrder = year
		}
		written, err := s.upsertHydratedLink(ctx, store.EntityRelation{
			FromEntityID: id,
			Name:         hydrated.Relation,
			ToMediaID:    mediaID,
			Attributes:   hydrated.RelationAttributes,
			Cardinality:  cardinality[hydrated.Kind+"\x00"+hydrated.Relation],
			SortOrder:    sortOrder,
			Writer:       writer,
		})
		if err != nil {
			return err
		}
		if holds && written {
			holderLinks[hydrated.Kind] = append(holderLinks[hydrated.Kind], id)
		}
		if err := s.applyEntityParent(ctx, library, id, hydrated); err != nil {
			return err
		}
		s.applyEntityPoster(ctx, id, hydrated.ArtworkURL)
	}
	return s.applyContainerEntities(ctx, mediaID, library, bundle, containers, holderLinks, manual)
}

// writtenRelationUpserter is the optional EntityStore extension that reports
// whether a hydrated link was written; *store.Store satisfies it.
type writtenRelationUpserter interface {
	UpsertHydratedEntityRelationWritten(ctx context.Context, relation store.EntityRelation) (bool, error)
}

// upsertHydratedLink writes one scraper link and reports whether it landed:
// false when an operator's lock kept the scraper's link out. A store without
// the reporting extension counts every successful write as landed.
func (s *Service) upsertHydratedLink(ctx context.Context, relation store.EntityRelation) (bool, error) {
	if upserter, ok := s.entities.(writtenRelationUpserter); ok {
		return upserter.UpsertHydratedEntityRelationWritten(ctx, relation)
	}
	if err := s.entities.UpsertHydratedEntityRelation(ctx, relation); err != nil {
		return false, err
	}
	return true, nil
}

// relationWriter is the writer key a scraper's Entity→Media links carry, so
// cleanup can tell them from an operator's or another scraper's links.
func relationWriter(bundle plugin.Bundle, spec plugin.ScraperSpec) string {
	return "scraper:" + string(bundle.ID) + "/" + spec.ID
}

// mediaYearReader is the optional MetadataStore extension that reads the
// Media row; *store.Store satisfies it.
type mediaYearReader interface {
	Media(ctx context.Context, id store.MediaID, filter store.LibraryFilter) (store.Media, error)
}

// holderYear is the sort order a holder link without a map ordinal takes: the
// Media's stored year, which field precedence may have kept from a manual
// edit over the scraper's value. Nil when the lookup hydrated no holder link
// or the Media has no year. A store that cannot read the Media falls back to
// the scraper's year.
func (s *Service) holderYear(ctx context.Context, mediaID store.MediaID, holders ContainerRelationIndex, result scrape.Result) (*int, error) {
	needed := false
	for _, hydrated := range result.Entities {
		if !hydrated.Container && hydrated.SortOrder == nil && holders.Holds(hydrated.Kind, hydrated.Relation) {
			needed = true
			break
		}
	}
	if !needed {
		return nil, nil
	}
	reader, ok := s.store.(mediaYearReader)
	if !ok {
		return metadataFromResult(result).Year, nil
	}
	media, err := reader.Media(ctx, mediaID, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return nil, err
	}
	if media.Year == 0 {
		return nil, nil
	}
	year := media.Year
	return &year, nil
}

// writtenRelationRemover is the optional EntityStore extension that deletes a
// Media's links one writer owns; *store.Store satisfies it.
type writtenRelationRemover interface {
	RemoveWrittenMediaRelations(ctx context.Context, mediaID store.MediaID, writer string, relations []store.WrittenRelation) error
}

// clearStaleHolderLinks removes the Media's link under each single-holder
// relation the scraper declares a non-container map onto when that map ran
// and hydrated no Entity, so a Media whose new match belongs to no container
// leaves the old one. Only a link this scraper wrote, from an Entity of the
// map's kind, is removed: an operator's link or another scraper's stays. A map
// the engine skipped (an unknown ordinal in its Path) says nothing about the
// document, so nothing under its kind and relation is removed, even when a
// sibling map onto the same relation resolved nothing. A move to another container needs nothing
// here: the relation's single cardinality replaces the old row on write. A
// scraper without such a map, or a store without the remover, changes nothing.
func (s *Service) clearStaleHolderLinks(ctx context.Context, mediaID store.MediaID, spec plugin.ScraperSpec, writer string, holders ContainerRelationIndex, result scrape.Result) error {
	remover, ok := s.entities.(writtenRelationRemover)
	if !ok {
		return nil
	}
	// resolved holds each (kind, relation) a map hydrated or the engine
	// skipped: neither may lose its link.
	resolved := map[store.WrittenRelation]bool{}
	for _, entity := range result.Entities {
		if !entity.Container {
			resolved[store.WrittenRelation{Kind: entity.Kind, Name: entity.Relation}] = true
		}
	}
	for _, i := range result.SkippedEntityMaps {
		if i >= 0 && i < len(spec.EntityMaps) {
			resolved[store.WrittenRelation{Kind: spec.EntityMaps[i].Kind, Name: spec.EntityMaps[i].Relation}] = true
		}
	}
	var stale []store.WrittenRelation
	for _, entityMap := range spec.EntityMaps {
		key := store.WrittenRelation{Kind: entityMap.Kind, Name: entityMap.Relation}
		if entityMap.Container || resolved[key] || !holders.Holds(entityMap.Kind, entityMap.Relation) ||
			slices.Contains(stale, key) {
			continue
		}
		stale = append(stale, key)
	}
	if len(stale) == 0 {
		return nil
	}
	return remover.RemoveWrittenMediaRelations(ctx, mediaID, writer, stale)
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
	return s.entities.UpsertHydratedEntityRelation(ctx, store.EntityRelation{
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

// mergeEntityLinks folds the hydrated results that link one Entity to the
// Media under one relation into a single result, in first-seen order: a
// credit list can name one person twice (two jobs, or a cast and a crew map),
// and the relation row is keyed by (from, name, to), so separate writes would
// leave only the last link's attributes. The merge keeps the first non-empty
// artwork and parent, unions typed fields (first value wins), joins distinct
// values of one relation attribute with ", " in order, and keeps the smallest
// sort position.
func mergeEntityLinks(library store.LibraryID, hydrated []scrape.EntityResult) []scrape.EntityResult {
	index := map[string]int{}
	var merged []scrape.EntityResult
	// The distinct values of each merged attribute, kept apart from the
	// joined display string: a value may itself contain ", ".
	var values []map[string][]string
	for _, result := range hydrated {
		key := string(entityIDForName(library, result.Kind, result.Name)) + "\x00" + result.Relation
		at, seen := index[key]
		if !seen {
			index[key] = len(merged)
			result.Fields = maps.Clone(result.Fields)
			result.RelationAttributes = maps.Clone(result.RelationAttributes)
			merged = append(merged, result)
			first := map[string][]string{}
			for name, value := range result.RelationAttributes {
				first[name] = []string{value}
			}
			values = append(values, first)
			continue
		}
		into := &merged[at]
		if into.ArtworkURL == "" {
			into.ArtworkURL = result.ArtworkURL
		}
		if into.ParentName == "" {
			into.ParentName, into.ParentRelation = result.ParentName, result.ParentRelation
		}
		for name, value := range result.Fields {
			if _, present := into.Fields[name]; !present {
				if into.Fields == nil {
					into.Fields = map[string]string{}
				}
				into.Fields[name] = value
			}
		}
		for name, value := range result.RelationAttributes {
			if slices.Contains(values[at][name], value) {
				continue
			}
			values[at][name] = append(values[at][name], value)
			if into.RelationAttributes == nil {
				into.RelationAttributes = map[string]string{}
			}
			into.RelationAttributes[name] = strings.Join(values[at][name], ", ")
		}
		if result.SortOrder != nil && (into.SortOrder == nil || *result.SortOrder < *into.SortOrder) {
			into.SortOrder = result.SortOrder
		}
	}
	return merged
}

// EntityEnsurer is optionally implemented by an EntityStore: EnsureEntity
// creates the Entity when it is absent and otherwise leaves it, its title and
// its typed fields as they are.
type EntityEnsurer interface {
	EnsureEntity(ctx context.Context, entity store.Entity) error
}

// EntityTitleKeeper is optionally implemented by an EntityStore:
// UpsertEntityKeepTitle replaces an Entity's fields but titles only a new
// row, keeping an existing row's stored title, trashed or not.
type EntityTitleKeeper interface {
	UpsertEntityKeepTitle(ctx context.Context, entity store.Entity) error
}

// writeEntity upserts a hydrated Entity. One hydrated without typed fields (a
// name-only credit, or an element past its map's lookup cap) must not erase
// the fields an earlier identification wrote, since UpsertEntity replaces
// them, so it only ensures the Entity exists when the store can do that.
// A holder link's name is the provider's key (an id), not a title, so a
// holder (keepTitle) titles only a new Entity: an existing one keeps the
// title the container map's TitlePath or an operator wrote (CORE-3152).
func (s *Service) writeEntity(ctx context.Context, entity store.Entity, keepTitle bool) error {
	if ensurer, ok := s.entities.(EntityEnsurer); ok && len(entity.Fields) == 0 {
		return ensurer.EnsureEntity(ctx, entity)
	}
	if keeper, ok := s.entities.(EntityTitleKeeper); ok && keepTitle {
		return keeper.UpsertEntityKeepTitle(ctx, entity)
	}
	return s.entities.UpsertEntity(ctx, entity)
}

// applyEntityPoster downloads and stores one hydrated Entity's poster.
// Best-effort like applyPoster: the entity write already succeeded, so
// artwork failures only log. Entities that already carry a poster are
// skipped — re-applying an identification must not re-download artwork
// (the same idempotence posture as the entity writes themselves). A poster
// inherited from the Entity's own Media does not count: the provider's
// artwork replaces it.
func (s *Service) applyEntityPoster(ctx context.Context, id store.EntityID, artworkURL string) {
	if artworkURL == "" || s.fetcher == nil || s.artwork == nil {
		return
	}
	if s.artwork.HasEntityPoster(id) && !s.artwork.EntityPosterInherited(id) {
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

// EntityIDForScopedName derives the Entity ID for a hydrated Entity whose title
// is only unique within a scope (the same title under two scopes is two
// Entities). An empty scope is the plain EntityIDForName id, so unscoped kinds
// and Entities written before scopes existed keep their ids. The scope follows
// the name in the hashed key, which no unscoped (kind, name) can produce
// without a NUL in the name. Persisted like EntityIDForName: do not change it.
func EntityIDForScopedName(library store.LibraryID, kind, scope, name string) store.EntityID {
	if scope == "" {
		return EntityIDForName(library, kind, name)
	}
	sum := sha256.Sum256([]byte(string(library) + "\x00" + kind + "\x00" + name + "\x00" + scope))
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
		if s.artworkLocked(ctx, mediaID, slot) {
			continue // an admin pinned this slot: keep their artwork
		}
		video := slot == artwork.SlotPoster && s.mediaIsVideo(ctx, mediaID)
		if slot == artwork.SlotPoster && !s.posterTakesProviderArtwork(ctx, mediaID, video) {
			continue
		}
		data, err := s.fetcher.FetchResource(ctx, rawURL)
		if err != nil {
			slog.Debug("identify: artwork fetch failed",
				"media", string(mediaID), "slot", slot, "error", err.Error())
			s.embeddedPosterFallback(ctx, mediaID, video)
			continue
		}
		// A lock, pin or upload may land during the fetch, so the decision is
		// made again under the slot's directory lock.
		stillWanted := func() bool {
			if s.artworkLocked(ctx, mediaID, slot) {
				return false
			}
			switch {
			case video:
				return s.videoPosterReplaceable(ctx, mediaID)
			case slot == artwork.SlotPoster:
				return !s.artwork.HasArtwork(mediaID, slot) || s.artwork.IsGeneratedArtwork(mediaID, slot)
			}
			return true
		}
		imported, err := s.importArtworkIf(ctx, mediaID, slot, data, stillWanted)
		if err != nil {
			slog.Debug("identify: artwork import failed",
				"media", string(mediaID), "slot", slot, "error", err.Error())
			s.embeddedPosterFallback(ctx, mediaID, video)
			continue
		}
		if !imported {
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

// posterTakesProviderArtwork reports whether a fetched provider poster
// should replace the Media item's current poster. A missing or generated
// poster always yields; for audio and image Media a cover embedded in the
// file is imported first and wins. For video Media (CORE-2901) the embedded
// cover is only a fallback before a frame grab, so it yields too.
func (s *Service) posterTakesProviderArtwork(ctx context.Context, mediaID store.MediaID, video bool) bool {
	if video {
		return s.videoPosterReplaceable(ctx, mediaID)
	}
	if s.artwork.HasArtwork(mediaID, artwork.SlotPoster) && !s.artwork.IsGeneratedArtwork(mediaID, artwork.SlotPoster) {
		return false
	}
	return !s.importEmbeddedPoster(ctx, mediaID)
}

// videoPosterReplaceable reports whether a fetched provider poster should
// replace a video's current poster. A sidecar lock keeps it, as does artwork
// stored through ImportArtwork or a manual pick (fetched, pinned, uploaded,
// integration-supplied). A missing poster, a frame grab and the file's
// embedded cover yield. A poster with no provenance marker predates the
// markers or was kept when its sidecar went away (local-artwork spec: a
// scrape may replace it); it yields unless an applied candidate shows it was
// fetched or pinned. Integration posters from before the markers have no
// candidate, so they are replaced once.
func (s *Service) videoPosterReplaceable(ctx context.Context, mediaID store.MediaID) bool {
	const slot = artwork.SlotPoster
	if locks, ok := s.artwork.(sidecarLockReader); ok && locks.IsArtworkLocked(mediaID, slot) {
		return false
	}
	if !s.artwork.HasArtwork(mediaID, slot) || s.artwork.IsGeneratedArtwork(mediaID, slot) {
		return true
	}
	if embedded, ok := s.artwork.(embeddedArtworkReader); ok && embedded.IsEmbeddedArtwork(mediaID, slot) {
		return true
	}
	imported, ok := s.artwork.(importedArtworkReader)
	if !ok || imported.IsImportedArtwork(mediaID, slot) {
		return false
	}
	return !s.hasAppliedCandidate(ctx, mediaID, slot)
}

// hasAppliedCandidate reports whether one of the slot's candidates is marked
// applied. A store that cannot say, or a failed read, counts as applied, so
// the poster is kept.
func (s *Service) hasAppliedCandidate(ctx context.Context, mediaID store.MediaID, slot string) bool {
	reader, ok := s.candidates.(appliedCandidateReader)
	if !ok {
		return true
	}
	candidates, err := reader.ArtworkCandidates(ctx, mediaID, slot, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		slog.Debug("identify: artwork candidate read failed", "media", string(mediaID), "slot", slot, "error", err.Error())
		return true
	}
	for _, candidate := range candidates {
		if candidate.Applied {
			return true
		}
	}
	return false
}

// importArtworkIf stores fetched artwork when stillWanted holds, rechecked
// under the slot's directory lock when the artwork store supports it, and
// reports whether it was stored.
func (s *Service) importArtworkIf(ctx context.Context, mediaID store.MediaID, slot string, data []byte, stillWanted func() bool) (bool, error) {
	if conditional, ok := s.artwork.(conditionalArtworkStore); ok {
		return conditional.ImportArtworkIf(ctx, mediaID, slot, data, stillWanted)
	}
	if !stillWanted() {
		return false, nil
	}
	return true, s.artwork.ImportArtwork(ctx, mediaID, slot, data)
}

// embeddedPosterFallback imports a video's embedded cover when the provider
// poster could not be stored and the slot holds nothing better than a frame
// grab. ImportEmbeddedPoster repeats the slot check under the directory lock.
func (s *Service) embeddedPosterFallback(ctx context.Context, mediaID store.MediaID, video bool) {
	if !video || s.artworkLocked(ctx, mediaID, artwork.SlotPoster) {
		return
	}
	if s.artwork.HasArtwork(mediaID, artwork.SlotPoster) && !s.artwork.IsGeneratedArtwork(mediaID, artwork.SlotPoster) {
		return
	}
	s.importEmbeddedPoster(ctx, mediaID)
}

// importEmbeddedPoster imports the cover embedded in the Media file and
// reports whether one landed.
func (s *Service) importEmbeddedPoster(ctx context.Context, mediaID store.MediaID) bool {
	embedded, ok := s.artwork.(embeddedArtworkStore)
	if !ok {
		return false
	}
	found, err := embedded.ImportEmbeddedPoster(ctx, mediaID)
	if err != nil {
		slog.Debug("identify: embedded artwork import failed", "media", string(mediaID), "error", err.Error())
		return false
	}
	return found
}

// mediaIsVideo reports whether mediaID is video-class Media. A store that
// cannot say, or a failed read, is treated as not video, which keeps the
// embedded-cover precedence.
func (s *Service) mediaIsVideo(ctx context.Context, mediaID store.MediaID) bool {
	classes, ok := s.store.(mediaClassReader)
	if !ok {
		return false
	}
	class, err := classes.MediaClass(ctx, mediaID)
	if err != nil {
		slog.Debug("identify: media class read failed", "media", string(mediaID), "error", err.Error())
		return false
	}
	return class == store.MediaClassVideo
}

// artworkLocked reports whether an admin locked one artwork slot. A failed
// read leaves the slot unlocked: artwork apply is best-effort either way.
func (s *Service) artworkLocked(ctx context.Context, mediaID store.MediaID, slot string) bool {
	locks, ok := s.store.(artworkLockReader)
	if !ok {
		return false
	}
	source, err := locks.MediaFieldProvenance(ctx, mediaID, store.ArtworkLockField(slot))
	if err != nil {
		slog.Debug("identify: artwork lock read failed", "media", string(mediaID), "slot", slot, "error", err.Error())
		return false
	}
	return source == store.MutationSourceManual
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
	// "sortTitle" is the provider's browse sort key (CORE-1768): the plugin,
	// not core, decides how a title sorts (for example without a leading
	// article). Plugin SDK bundles cannot map it until the SDK admits it as a
	// built-in FieldMap key; core reads it as soon as a result carries it.
	if sortTitle := strings.TrimSpace(fieldString(result.Fields, "sortTitle")); sortTitle != "" {
		metadata.SortTitle = &sortTitle
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
