package graph

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/l10n"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// Stations (ADR-0098 area 5). A "Station" is Beamfall Music PRODUCT LANGUAGE
// over a generated Play Queue — it is NEVER a Core schema type (invariant
// #1/#2). This file is the whole Station generator: it lives at the GRAPH layer
// (not the store) because materialization must subtract Sealed Library IDs
// before candidate collection, and Sealed/Unsealed state is obtained through
// app/graph composition via the libEncrypt seam (ADR-0098 §Stations,
// graph_libencrypt.go) — the store has no view of Sealed state. The generator
// composes existing, already-reveal-filtered store reads; it adds no schema.
//
// Order of operations is load-bearing: subtract Sealed Library IDs
// FIRST, then collect candidates, then apply hard Source filters, then rank,
// then materialize / explain. A Sealed (or hidden / restricted-unrevealed /
// non-music) Library's Media must never reach candidates, the window, or an
// explanation.

// stationWindowSize is the default rolling-window size materialized into the
// Play Queue. Stations conceptually generate an endless queue; the server
// materializes a bounded rolling window (ADR-0098 §Stations).
const stationWindowSize = 50

// stationSeedKinds is the closed set of seed kinds (ADR-0098 §Stations: Track,
// Artist, Album, Genre, or Collection). Track is a Media id; Artist/Album/Genre
// are Music Plugin Entity ids; Collection is a Collection id. The names are
// generic shape labels, not domain types. "media" and "entity" are the generic
// spellings of a Media and an Entity seed, so a client never has to name a
// plugin's Entity kind (CORE-2772).
var stationSeedKinds = map[string]bool{
	"track": true, "artist": true, "album": true, "genre": true, "collection": true,
	"media": true, "entity": true,
}

// stationSeed is the parsed seed for a generation request.
type stationSeed struct {
	kind string
	id   string
}

// StationCandidate is the wire shape for one generated Station item. It is a
// Station-specific type, deliberately NOT the shared SourceItem (whose wire
// shape is pinned by TestNestedSchemaSourceItemShape) — it carries an
// Explanation, which SourceItem does not and must not. The Explanation is a
// product-safe string: it never reveals another Profile's behavior, a hidden /
// Sealed / restricted Library name, provider secrets, or a raw score vector
// (ADR-0098 §Stations).
type StationCandidate struct {
	MediaID     string `json:"mediaID"`
	LibraryID   string `json:"libraryID,omitempty"`
	Title       string `json:"title"`
	Explanation string `json:"explanation,omitempty"`
}

// StationGenerateResult is the read-only generation preview returned by
// stationGenerate: the rolling window of candidates plus a Station-level
// explanation (e.g. the scarcity note when few candidates survived filtering).
type StationGenerateResult struct {
	Candidates  []StationCandidate `json:"candidates"`
	Explanation string             `json:"explanation,omitempty"`
}

// generateStation runs the generator: subtract Sealed/non-music Libraries, then
// collect → hard-filter → rank → window. It is the single code path both
// stationGenerate (preview) and stationStart (materialize) consume, so a
// previewed window and a started window are produced identically. rules and
// tuning are the parsed (executing) Source filter and ranking hints.
func (h *Handler) generateStation(ctx context.Context, scope auth.Scope, seed stationSeed, rules stationRules, tuning stationTuning, rankSeed uint64) ([]StationCandidate, string, error) {
	locale := l10n.FromContext(ctx)
	// 1. Sealed + non-music subtraction FIRST: build a filter scoped
	//    to the caller's visible, music-plugin, NON-Sealed Libraries only.
	filter, err := h.sealedFreeStationFilter(ctx, scope)
	if err != nil {
		return nil, "", err
	}
	if len(filter.AllowedIDs) == 0 {
		// No music Library is currently both visible and unsealed — the Station
		// has no candidate space. Empty window, scarcity explanation; reveals no
		// Library name.
		return []StationCandidate{}, stationScarcityExplanation(locale, 0), nil
	}
	// 2. Collect candidates for the seed through reveal-filtered store reads,
	//    each constrained to the sealed-free music filter: the seed's own Media,
	//    then Media sharing an Entity with them (CORE-2772). Candidates are full
	//    Media rows so the hard filter + ranking can read real fields.
	//    Media already queued are left out when the rules ask, before any
	//    sampling, so each top-up reaches past the last one.
	queued, err := h.stationQueued(ctx, scope, filter, rules)
	if err != nil {
		return nil, "", err
	}
	pool, err := h.collectStationCandidates(ctx, scope, filter, seed, rules, queued, rankSeed)
	if err != nil {
		return nil, "", err
	}
	// 3. Hard Source filters BEFORE ranking. rules.admits is the hard
	//    predicate: an item it rejects is removed from the candidate set here, so
	//    it can never appear in the window or an explanation. (The SEALED / HIDDEN
	//    / RESTRICTED / non-music suppression is an additional hard filter already
	//    enforced upstream by sealedFreeStationFilter + the per-read LibraryFilter.)
	candidates := make([]store.Media, 0, len(pool.candidates))
	for _, m := range pool.candidates {
		if !queued[m.ID] && rules.admits(m, pool.links[m.ID]) {
			candidates = append(candidates, m)
		}
	}
	// 4. Rank: deterministic given rankSeed (debug reproducibility). A track
	//    seed leads, tuning hints bias the order, similarity orders the rest.
	//    Tuning is RANKING-ONLY — it reorders the post-filter set and can never
	//    admit an item rule.admits already rejected.
	played := h.stationPlayed(ctx, scope, filter, candidates)
	scored := scoreStationCandidates(pool, candidates, played, rankSeed)
	rankStationCandidates(scored, tuning)
	// 5. Rolling window.
	if len(scored) > stationWindowSize {
		scored = scored[:stationWindowSize]
	}
	out := make([]StationCandidate, len(scored))
	for i, c := range scored {
		out[i] = StationCandidate{
			MediaID:     string(c.item.ID),
			LibraryID:   string(c.item.LibraryID),
			Title:       c.item.Title,
			Explanation: c.reason.text(locale),
		}
	}
	return out, stationScarcityExplanation(locale, len(out)), nil
}

// stationQueued returns the Media in the acting Profile's Play Queue when the
// rules exclude them, and nothing otherwise.
func (h *Handler) stationQueued(ctx context.Context, scope auth.Scope, filter store.LibraryFilter, rules stationRules) (map[store.MediaID]bool, error) {
	queued := map[store.MediaID]bool{}
	if !rules.ExcludeQueued {
		return queued, nil
	}
	q, err := h.store.PlayQueue(ctx, scope.ProfileID, filter)
	if err != nil {
		return nil, err
	}
	for _, entry := range q.Entries {
		queued[entry.Item.ID] = true
	}
	return queued, nil
}

// sealedFreeStationFilter returns a LibraryFilter restricted to the caller's
// visible Libraries that are (a) bound to a station-capable plugin and (b) NOT Sealed.
// This single chokepoint enforces three ADR-0098 constraints at once: Stations
// never cross ineligible Libraries; Sealed Library IDs are subtracted before
// candidate collection; and hidden / restricted-unrevealed Libraries are already
// excluded because the enumeration runs through scope.LibraryFilter(). When the
// libEncrypt seam is absent (no encrypted-library key model wired) no Library is
// Sealed, so only the station-capable plugin filter applies.
func (h *Handler) sealedFreeStationFilter(ctx context.Context, scope auth.Scope) (store.LibraryFilter, error) {
	libraries, err := h.store.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		return store.LibraryFilter{}, err
	}
	eligible := map[string]bool{}
	for _, id := range h.plugins.FeatureEligiblePluginIDs(plugin.FeatureAudioStation) {
		eligible[string(id)] = true
	}
	allowed := make([]store.LibraryID, 0, len(libraries))
	for _, lib := range libraries {
		if lib.PluginID == nil || !eligible[*lib.PluginID] {
			continue
		}
		if h.librarySealed(lib.ID) {
			continue
		}
		allowed = append(allowed, lib.ID)
	}
	// AllLibraries is deliberately false: the Station only ever sees the explicit
	// sealed-free music set, never the AllLibraries sentinel. Certification bounds
	// from the caller's scope are preserved and enforced as playback enforces
	// them: a locked item is never picked, and an item the managed Profile was
	// granted is (CORE-2772).
	return store.LibraryFilter{
		AllowedIDs:      allowed,
		AllLibraries:    false,
		Revealed:        scope.Revealed,
		Certification:   scope.Certification,
		GrantedMediaIDs: scope.GrantedMediaIDs,
		PlaybackGate:    true,
	}, nil
}

// Similarity bounds (CORE-2772). The generator reads only catalog data the
// store already holds — Entity links (credits, containers, categories), release
// year and the acting Profile's play history. Audio similarity is the separate
// sonic-mixes work and never happens here.
const (
	// stationSeedMediaCap bounds how many of a large seed's Media (a genre
	// with thousands of items) feed the seed profile.
	stationSeedMediaCap = 200
	// stationExpandEntities is how many of the seed profile's strongest
	// Entities are expanded into candidates.
	stationExpandEntities = 12
	// stationPerEntityCap bounds how many Media one expanded Entity adds.
	stationPerEntityCap = 100
	// stationEntityReadPage is the Source's largest page; stationEntityReadCap
	// bounds how many of one Entity's Media are read before sampling.
	stationEntityReadPage = 200
	stationEntityReadCap  = 2000
	// stationSeedEntityWeight keeps an Entity seed at the top of its own
	// profile, so its Media are always expanded and ranked strongly.
	stationSeedEntityWeight = 10
)

// stationPool is the collected candidate space plus the seed profile the
// ranker scores it against.
type stationPool struct {
	seed       stationSeed
	seedMedia  map[store.MediaID]bool
	seedEntity store.EntityID
	// profile weighs each Entity linked to the seed's Media by archetype.
	profile    map[store.EntityID]float64
	archetypes map[store.EntityID]string
	seedYear   int
	candidates []store.Media
	links      map[store.MediaID][]store.MediaEntityLink
}

// collectStationCandidates gathers candidate Media (full rows, so the hard
// filter + ranking can read real fields) for the seed: the seed's own Media,
// then Media sharing the seed profile's strongest Entities, plus Media of any
// Entity a rule requires. Every id is resolved through the sealed-free music
// filter so no Sealed/hidden/non-music Media can enter the pool. Duplicate ids
// are collapsed keeping first appearance.
func (h *Handler) collectStationCandidates(ctx context.Context, scope auth.Scope, filter store.LibraryFilter, seed stationSeed, rules stationRules, queued map[store.MediaID]bool, rankSeed uint64) (stationPool, error) {
	pool := stationPool{seed: seed, seedMedia: map[store.MediaID]bool{}, profile: map[store.EntityID]float64{}, archetypes: map[store.EntityID]string{}}
	var seedIDs []store.MediaID
	switch seed.kind {
	case "track", "media":
		// Resolve the seed itself through the sealed-free music filter: a seed
		// Media outside that scope is "unknown" — never an oracle for a hidden/
		// sealed/non-music id.
		media, err := h.store.Media(ctx, store.MediaID(seed.id), filter)
		if err != nil {
			return pool, clientError("unknown station seed")
		}
		seedIDs = append(seedIDs, media.ID)
	case "artist", "album", "genre", "entity":
		items, err := h.stationEntityMedia(ctx, scope, filter, store.EntityID(seed.id))
		if err != nil {
			return pool, clientError("unknown station seed")
		}
		for _, it := range items {
			seedIDs = append(seedIDs, it.ID)
		}
		pool.seedEntity = store.EntityID(seed.id)
	case "collection":
		// MUSIC-4's canonical ordered-membership read (ADR-0098 area 4): it returns
		// the ordered static membership with Reveal/Sealed visibility already
		// applied under the sealed-free MUSIC filter, so non-music / Sealed / hidden
		// members are dropped (never gapped) before they can enter the candidate
		// pool. The MediaByIDs batch below re-resolves the same filter to fetch full
		// Media rows for the hard filter + ranking.
		members, err := h.store.ResolveCollectionMembers(ctx, store.CollectionID(seed.id), filter)
		if err != nil {
			return pool, clientError("unknown station seed")
		}
		for _, m := range members {
			seedIDs = append(seedIDs, m.MediaID)
		}
	default:
		return pool, clientError("seedKind must be one of media, entity, collection, track, artist, album, genre")
	}
	seedIDs = sampleStationIDs(seedIDs, stationSeedMediaCap, rankSeed)

	// The seed profile: every Entity linked to a visible seed Media, weighed by
	// its archetype, plus the median release year.
	seedRows, err := h.store.MediaByIDs(ctx, seedIDs, filter)
	if err != nil {
		return pool, err
	}
	visibleSeed := make([]store.MediaID, 0, len(seedIDs))
	var years []int
	for _, id := range seedIDs {
		m, ok := seedRows[id]
		if !ok || pool.seedMedia[id] {
			continue
		}
		pool.seedMedia[id] = true
		visibleSeed = append(visibleSeed, id)
		if m.Year > 0 {
			years = append(years, m.Year)
		}
	}
	if len(years) > 0 {
		sort.Ints(years)
		pool.seedYear = years[len(years)/2]
	}
	seedLinks, err := h.store.MediaEntityLinks(ctx, visibleSeed)
	if err != nil {
		return pool, err
	}
	archetypeOf := h.stationArchetypeLookup(ctx)
	for _, id := range visibleSeed {
		for _, link := range seedLinks[id] {
			archetype := archetypeOf(link.LibraryID, link.Kind)
			pool.archetypes[link.EntityID] = archetype
			pool.profile[link.EntityID] += stationArchetypeWeight(archetype)
		}
	}
	if pool.seedEntity != "" {
		pool.profile[pool.seedEntity] += stationSeedEntityWeight
	}

	// Expand the strongest Entities. A rule naming Entities expands only those:
	// every candidate must be linked to one, and a genre rule must find that
	// genre's Media even when the seed never touched it.
	expand := strongestStationEntities(pool.profile, stationExpandEntities)
	if len(rules.AnyEntityIDs) > 0 {
		expand = expand[:0]
		for _, id := range rules.AnyEntityIDs {
			expand = append(expand, store.EntityID(id))
		}
	}
	ids := append([]store.MediaID{}, visibleSeed...)
	expanded := map[store.EntityID]bool{}
	for _, entityID := range expand {
		if expanded[entityID] {
			continue
		}
		expanded[entityID] = true
		items, err := h.stationEntityMedia(ctx, scope, filter, entityID)
		if err != nil {
			// An Entity this scope cannot see adds nothing; it never errors the
			// Station, so a rule naming one is not an oracle either.
			continue
		}
		// The year and length rules and the queued exclusion apply before
		// sampling, so a large Entity's few matching Media are never sampled away.
		related := make([]store.MediaID, 0, len(items))
		for _, it := range items {
			if !queued[it.ID] && rules.admitsFields(it.Year, it.DurationSeconds) {
				related = append(related, it.ID)
			}
		}
		ids = append(ids, sampleStationIDs(related, stationPerEntityCap, rankSeed)...)
	}

	// One batch read resolves every candidate id to a full Media row through the
	// sealed-free MUSIC filter. A CollectionMembers list may mix non-music Media
	// (it carries no library-kind); resolving through this filter drops every
	// non-music / Sealed / hidden member (ADR-0098: never cross those Libraries).
	visible, err := h.store.MediaByIDs(ctx, ids, filter)
	if err != nil {
		return pool, err
	}
	seen := make(map[store.MediaID]bool, len(ids))
	pool.candidates = make([]store.Media, 0, len(ids))
	kept := make([]store.MediaID, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if m, ok := visible[id]; ok {
			pool.candidates = append(pool.candidates, m)
			kept = append(kept, id)
		}
	}
	pool.links, err = h.store.MediaEntityLinks(ctx, kept)
	if err != nil {
		return pool, err
	}
	for _, links := range pool.links {
		for _, link := range links {
			if _, ok := pool.archetypes[link.EntityID]; !ok {
				pool.archetypes[link.EntityID] = archetypeOf(link.LibraryID, link.Kind)
			}
		}
	}
	return pool, nil
}

// stationEntityMedia reads an Entity's Media through the entity-media Source
// with the acting Profile, the read Related Media and browse take, so Mute
// suppression and the Library, Reveal and certification filter apply as they do
// there. Extras are left out, and at most stationEntityReadCap Media are read.
func (h *Handler) stationEntityMedia(ctx context.Context, scope auth.Scope, filter store.LibraryFilter, entityID store.EntityID) ([]store.SourceItem, error) {
	var out []store.SourceItem
	for len(out) < stationEntityReadCap {
		page, err := h.store.SourceItems(ctx, filter, store.SourceQuery{
			Kind:          "entity-media",
			EntityID:      entityID,
			Limit:         stationEntityReadPage,
			Offset:        len(out),
			ProfileID:     scope.ProfileID,
			ExcludeExtras: true,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < stationEntityReadPage {
			break
		}
	}
	return out, nil
}

// stationArchetypeLookup maps an Entity kind to its plugin-declared archetype
// (person, container, category, ...), caching each Library's bundle. Core
// reasons about archetypes, never plugin kind names.
func (h *Handler) stationArchetypeLookup(ctx context.Context) func(store.LibraryID, string) string {
	byLibrary := map[store.LibraryID]map[string]string{}
	return func(libraryID store.LibraryID, kind string) string {
		kinds, ok := byLibrary[libraryID]
		if !ok {
			kinds = map[string]string{}
			if bundle, found := h.libraryBundle(ctx, libraryID); found {
				for _, k := range bundle.EntityKinds {
					kinds[k.Name] = k.Archetype
				}
			}
			byLibrary[libraryID] = kinds
		}
		return kinds[kind]
	}
}

// stationArchetypeWeight is how strongly sharing an Entity of an archetype
// ties two Media together: a shared credit (person) most, a shared container
// next, a shared category (genre, mood) least.
func stationArchetypeWeight(archetype string) float64 {
	switch archetype {
	case "person":
		return 3
	case "container":
		return 2
	case "category":
		return 1.5
	}
	return 1
}

// strongestStationEntities returns up to n profile Entities, strongest first,
// ties broken by id so the order is stable.
func strongestStationEntities(profile map[store.EntityID]float64, n int) []store.EntityID {
	ids := make([]store.EntityID, 0, len(profile))
	for id := range profile {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if profile[ids[i]] != profile[ids[j]] {
			return profile[ids[i]] > profile[ids[j]]
		}
		return ids[i] < ids[j]
	})
	if len(ids) > n {
		ids = ids[:n]
	}
	return ids
}

// sampleStationIDs keeps at most n ids, chosen deterministically for the rank
// seed so a large Entity contributes a different-but-reproducible slice.
func sampleStationIDs(ids []store.MediaID, n int, rankSeed uint64) []store.MediaID {
	if len(ids) <= n {
		return ids
	}
	sampled := append([]store.MediaID{}, ids...)
	sort.SliceStable(sampled, func(i, j int) bool {
		return stationRankKey(sampled[i], rankSeed) < stationRankKey(sampled[j], rankSeed)
	})
	return sampled[:n]
}

// stationPlayed returns the candidates the acting Profile has finished: a
// light familiarity signal from its own play history, never another Profile's.
func (h *Handler) stationPlayed(ctx context.Context, scope auth.Scope, filter store.LibraryFilter, candidates []store.Media) map[store.MediaID]bool {
	played := map[store.MediaID]bool{}
	if len(candidates) == 0 {
		return played
	}
	items := make([]store.SourceItem, len(candidates))
	for i, m := range candidates {
		items[i] = store.SourceItem{ID: m.ID, Kind: "media"}
	}
	// Play history only nudges the order; a failed read leaves it out rather
	// than failing the Station.
	if err := h.store.AnnotateEngagement(ctx, scope.ProfileID, filter, items, nil); err != nil {
		return played
	}
	for _, it := range items {
		if it.Engagement != nil && it.Engagement.Completed {
			played[it.ID] = true
		}
	}
	return played
}

// stationScored is one ranked candidate with the reason it was picked.
type stationScored struct {
	item   store.Media
	lead   bool
	score  float64
	key    uint64
	reason stationReason
}

// scoreStationCandidates scores each admitted candidate against the seed
// profile: shared Entities by archetype, the seed Entity itself, closeness to
// the seed's era and the Profile's play history, plus a small rank-seed jitter
// so consecutive generations vary. A track seed leads its own window.
func scoreStationCandidates(pool stationPool, candidates []store.Media, played map[store.MediaID]bool, rankSeed uint64) []stationScored {
	out := make([]stationScored, len(candidates))
	for i, m := range candidates {
		key := stationRankKey(m.ID, rankSeed)
		c := stationScored{item: m, key: key, score: float64(key%1000) / 1000 * 1.5}
		var best store.MediaEntityLink
		bestWeight := 0.0
		for _, link := range pool.links[m.ID] {
			if _, shared := pool.profile[link.EntityID]; !shared {
				continue
			}
			weight := stationArchetypeWeight(pool.archetypes[link.EntityID])
			if link.EntityID == pool.seedEntity {
				weight += 3
			}
			c.score += weight
			if weight > bestWeight {
				best, bestWeight = link, weight
			}
		}
		era := 0
		if pool.seedYear > 0 && m.Year > 0 {
			switch gap := abs(m.Year - pool.seedYear); {
			case gap <= 5:
				era = 2
			case gap <= 10:
				era = 1
			}
		}
		c.score += float64(era)
		if played[m.ID] {
			c.score++
		}
		switch {
		case pool.seedMedia[m.ID] && (pool.seed.kind == "track" || pool.seed.kind == "media"):
			c.lead = true
			c.reason = stationReason{key: l10n.StationReasonSeed}
		case pool.seedMedia[m.ID] && pool.seed.kind == "collection":
			c.reason = stationReason{key: l10n.StationReasonSeed}
		case bestWeight > 0 && best.EntityID == pool.seedEntity:
			c.reason = stationReason{key: l10n.StationReasonFrom, args: l10n.Args{"title": best.Title}}
		case bestWeight > 0 && pool.archetypes[best.EntityID] == "category":
			c.reason = stationReason{key: l10n.StationReasonCategory, args: l10n.Args{"title": best.Title}}
		case bestWeight > 0:
			c.reason = stationReason{key: l10n.StationReasonShared, args: l10n.Args{"title": best.Title}}
		case era > 0:
			c.reason = stationReason{key: l10n.StationReasonEra, args: l10n.Args{"decade": strconv.Itoa(pool.seedYear / 10 * 10)}}
		case played[m.ID]:
			c.reason = stationReason{key: l10n.StationReasonPlayed}
		default:
			c.reason = stationReason{key: l10n.StationReasonPicked}
		}
		out[i] = c
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// stationReason is a candidate's localized, product-safe explanation: it names
// the seed, a visible Entity the candidate shares with it, an era or the
// listener's own history — never a Library, another Profile or a score.
type stationReason struct {
	key  l10n.Key
	args l10n.Args
}

func (r stationReason) text(locale string) string { return l10n.Text(locale, r.key, r.args) }

// rankStationCandidates orders candidates deterministically for a given
// rankSeed (same seed → same order: debug reproducibility, ADR-0098). A track
// seed leads; then the tuning bias; then the similarity score; then the rank
// key. Tuning reorders the post-filter set ONLY and can never admit a filtered
// item (the rejected items are already removed before this runs).
func rankStationCandidates(candidates []stationScored, tuning stationTuning) {
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.lead != b.lead {
			return a.lead
		}
		if ba, bb := tuning.bias(a.item), tuning.bias(b.item); ba != bb {
			// Higher bias ranks earlier.
			return ba > bb
		}
		if a.score != b.score {
			return a.score > b.score
		}
		return a.key < b.key
	})
}

func stationRankKey(id store.MediaID, rankSeed uint64) uint64 {
	hsh := fnv.New64a()
	var buf [8]byte
	for i := 0; i < 8; i++ {
		buf[i] = byte(rankSeed >> (8 * i))
	}
	_, _ = hsh.Write(buf[:])
	_, _ = hsh.Write([]byte(id))
	return hsh.Sum64()
}

// stationRules is the parsed, EXECUTING hard Source filter (ADR-0098: hard
// filters apply before ranking). Year and duration read fields present on every
// Media; Decades keeps candidates whose year falls in a listed decade (1990
// means 1990–1999); AnyEntityIDs keeps candidates linked to at least one listed
// Entity — how a client asks for a genre or mood without naming plugin kinds
// (CORE-2772). A nil/zero field means "no constraint on that axis".
type stationRules struct {
	MinYear            int      `json:"minYear"`
	MaxYear            int      `json:"maxYear"`
	MinDurationSeconds int      `json:"minDurationSeconds"`
	MaxDurationSeconds int      `json:"maxDurationSeconds"`
	Decades            []int    `json:"decades"`
	AnyEntityIDs       []string `json:"anyEntityIDs"`
	// ExcludeQueued drops Media already in the acting Profile's Play Queue, so
	// a top-up reaches past what the Station already queued.
	ExcludeQueued bool `json:"excludeQueued"`
}

// admits reports whether a Media, with its Entity links, passes every hard
// filter. An item it rejects is removed from the candidate set before ranking,
// so it can never reach the window or an explanation.
func (r stationRules) admits(m store.Media, links []store.MediaEntityLink) bool {
	if len(r.AnyEntityIDs) > 0 && !slices.ContainsFunc(links, func(link store.MediaEntityLink) bool {
		return slices.Contains(r.AnyEntityIDs, string(link.EntityID))
	}) {
		return false
	}
	return r.admitsFields(m.Year, m.DurationSeconds)
}

// admitsFields applies the rules that read only a Media's year and length.
func (r stationRules) admitsFields(year, durationSeconds int) bool {
	if len(r.Decades) > 0 && (year == 0 || !slices.Contains(r.Decades, year/10*10)) {
		return false
	}
	if r.MinYear > 0 && (year == 0 || year < r.MinYear) {
		return false
	}
	if r.MaxYear > 0 && year > r.MaxYear {
		return false
	}
	if r.MinDurationSeconds > 0 && durationSeconds < r.MinDurationSeconds {
		return false
	}
	if r.MaxDurationSeconds > 0 && durationSeconds > r.MaxDurationSeconds {
		return false
	}
	return true
}

// stationTuning is the parsed, EXECUTING ranking hint set. Tuning is
// ranking-only: bias() returns a per-item score that reorders the post-filter
// candidate set; it never includes or excludes an item. PreferRecent biases
// newer releases earlier (using the Media year already present today).
type stationTuning struct {
	PreferRecent bool `json:"preferRecent"`
}

// bias returns a ranking score for a Media. Higher ranks earlier. Zero is the
// neutral score, so an empty tuning leaves the deterministic base order intact.
func (t stationTuning) bias(m store.Media) int {
	if t.PreferRecent {
		return m.Year
	}
	return 0
}

// parseStationRules / parseStationTuning decode the opaque JSON inputs. Unknown
// fields are ignored (forward-compatible with MUSIC-1's richer vocabulary);
// malformed JSON yields a client error.
func parseStationRules(raw string) (stationRules, error) {
	var r stationRules
	if strings.TrimSpace(raw) == "" {
		return r, nil
	}
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return stationRules{}, clientError("rulesJSON is not valid JSON")
	}
	return r, nil
}

func parseStationTuning(raw string) (stationTuning, error) {
	var t stationTuning
	if strings.TrimSpace(raw) == "" {
		return t, nil
	}
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return stationTuning{}, clientError("tuningJSON is not valid JSON")
	}
	return t, nil
}

// stationScarcityExplanation is the Station-level explanation. When few items
// survived filtering it says so generically, never disclosing which Libraries
// were Sealed/hidden or why an item was dropped.
func stationScarcityExplanation(locale string, n int) string {
	if n == 0 {
		return l10n.Text(locale, l10n.StationEmpty, nil)
	}
	if n < stationWindowSize {
		return l10n.Text(locale, l10n.StationShort, nil)
	}
	return ""
}

// stationRankSeedFor derives the ranking seed. A client-supplied debug seed
// (server-local diagnostic / test reproducibility) pins the order; otherwise the
// seed is time-based so a fresh start re-shuffles. The seed flows from
// stationGenerate to stationStart unchanged, so a previewed window can be
// committed identically by passing the same seed.
func stationRankSeedFor(req request) uint64 {
	if raw := strings.TrimSpace(trimmedVariable(req, "seed")); raw != "" {
		hsh := fnv.New64a()
		_, _ = hsh.Write([]byte(raw))
		return hsh.Sum64()
	}
	return uint64(time.Now().UnixNano())
}

// parseStationSeed reads + validates the seed variables.
func parseStationSeed(req request) (stationSeed, error) {
	kind := strings.ToLower(strings.TrimSpace(trimmedVariable(req, "seedKind")))
	id := strings.TrimSpace(trimmedVariable(req, "seedID"))
	if kind == "" || id == "" {
		return stationSeed{}, clientError("seedKind and seedID are required")
	}
	if !stationSeedKinds[kind] {
		return stationSeed{}, clientError("seedKind must be one of media, entity, collection, track, artist, album, genre")
	}
	return stationSeed{kind: kind, id: id}, nil
}

// stationGenerate is the READ-ONLY generation preview (no queue mutation): it
// returns the rolling window the client can preview before the user commits to
// replacing their queue.
func (h *Handler) stationGenerate(ctx context.Context, scope auth.Scope, req request) (StationGenerateResult, error) {
	seed, rules, tuning, err := parseStationRequest(req)
	if err != nil {
		return StationGenerateResult{}, err
	}
	candidates, explanation, err := h.generateStation(ctx, scope, seed, rules, tuning, stationRankSeedFor(req))
	if err != nil {
		return StationGenerateResult{}, err
	}
	return StationGenerateResult{Candidates: candidates, Explanation: explanation}, nil
}

// parseStationRequest reads the seed + opaque rules/tuning JSON inputs shared by
// the generate-preview and start resolvers.
func parseStationRequest(req request) (stationSeed, stationRules, stationTuning, error) {
	seed, err := parseStationSeed(req)
	if err != nil {
		return stationSeed{}, stationRules{}, stationTuning{}, err
	}
	rules, err := parseStationRules(stringVar(req, "rulesJSON"))
	if err != nil {
		return stationSeed{}, stationRules{}, stationTuning{}, err
	}
	tuning, err := parseStationTuning(stringVar(req, "tuningJSON"))
	if err != nil {
		return stationSeed{}, stationRules{}, stationTuning{}, err
	}
	return seed, rules, tuning, nil
}

// stationStart generates a window and MATERIALIZES it into the Play Queue,
// replacing the visible queue. Replacing a non-empty visible queue (user-added
// items present) requires confirmReplace=true; otherwise it makes no change and
// returns a client-safe needs-confirmation error so the client can prompt
// (ADR-0098 §Stations). Hidden queue entries are preserved by the store. Passing
// the same `seed` the preview used commits the identical window.
func (h *Handler) stationStart(ctx context.Context, scope auth.Scope, req request) ([]SourceItem, error) {
	// Refuse a write made for another Profile before resolving its seed, which
	// may be visible only to that Profile (CORE-2177).
	expected, err := checkExpectedProfile(scope, req)
	if err != nil {
		return nil, err
	}
	seed, rules, tuning, err := parseStationRequest(req)
	if err != nil {
		return nil, err
	}
	candidates, _, err := h.generateStation(ctx, scope, seed, rules, tuning, stationRankSeedFor(req))
	if err != nil {
		return nil, err
	}
	mediaIDs := make([]store.MediaID, len(candidates))
	for i, c := range candidates {
		mediaIDs[i] = store.MediaID(c.MediaID)
	}
	authorization, err := h.queueWriteAuthorization(ctx, scope, expected)
	if err != nil {
		return nil, err
	}
	committed, err := h.store.StartQueueStation(ctx, authorization, mediaIDs, boolVar(req, "confirmReplace"))
	if errors.Is(err, store.ErrQueueReplaceNeedsConfirm) {
		return nil, clientError("queue has items; pass confirmReplace to replace it")
	}
	if err != nil {
		return nil, queueWriteError(err)
	}
	return h.queueItems(ctx, committed)
}

// SavedStationInfo is the wire shape for one saved Station (ADR-0098 area 5). It
// carries the generation INPUTS only (seed/rules/tuning) — never a generated
// track order, which a Station never stores.
type SavedStationInfo struct {
	ID         string `json:"id"`
	ProfileID  string `json:"profileID"`
	LibraryID  string `json:"libraryID"`
	Name       string `json:"name"`
	SeedJSON   string `json:"seedJSON"`
	RulesJSON  string `json:"rulesJSON"`
	TuningJSON string `json:"tuningJSON"`
	CreatedAt  string `json:"createdAt,omitempty"`
}

func savedStationInfo(st store.SavedStation) SavedStationInfo {
	return SavedStationInfo{
		ID:         string(st.ID),
		ProfileID:  string(st.ProfileID),
		LibraryID:  string(st.LibraryID),
		Name:       st.Name,
		SeedJSON:   st.SeedJSON,
		RulesJSON:  st.RulesJSON,
		TuningJSON: st.TuningJSON,
		CreatedAt:  formatOptionalTime(st.CreatedAt),
	}
}

// stations returns the current Profile's saved Stations for a visible Library.
func (h *Handler) stations(ctx context.Context, scope auth.Scope, req request) ([]SavedStationInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return nil, clientError("libraryID is required")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, clientError("unknown library")
	}
	stations, err := h.store.SavedStations(ctx, scope.ProfileID, store.LibraryID(libraryID))
	if err != nil {
		return nil, err
	}
	infos := make([]SavedStationInfo, len(stations))
	for i, st := range stations {
		infos[i] = savedStationInfo(st)
	}
	return infos, nil
}

// stationSave persists a saved Station for the current Profile and a visible
// Library. Stores seed/rules/tuning; never a generated order.
func (h *Handler) stationSave(ctx context.Context, scope auth.Scope, req request) (SavedStationInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	name := stringVar(req, "name")
	if libraryID == "" {
		return SavedStationInfo{}, clientError("libraryID is required")
	}
	if name == "" {
		return SavedStationInfo{}, clientError("name is required")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return SavedStationInfo{}, err
	}
	if !visible {
		return SavedStationInfo{}, clientError("unknown library")
	}
	st, err := h.store.CreateSavedStation(ctx, store.SavedStation{
		ProfileID:  scope.ProfileID,
		LibraryID:  store.LibraryID(libraryID),
		Name:       name,
		SeedJSON:   stringVar(req, "seedJSON"),
		RulesJSON:  stringVar(req, "rulesJSON"),
		TuningJSON: stringVar(req, "tuningJSON"),
	}, time.Now().UTC())
	if err != nil {
		return SavedStationInfo{}, err
	}
	return savedStationInfo(st), nil
}

// stationDelete removes a saved Station owned by the current Profile.
func (h *Handler) stationDelete(ctx context.Context, scope auth.Scope, req request) (SavedStationInfo, error) {
	id := trimmedVariable(req, "id")
	if id == "" {
		return SavedStationInfo{}, clientError("id is required")
	}
	st, err := h.store.SavedStationByID(ctx, store.SavedStationID(id))
	if err != nil || st.ProfileID != scope.ProfileID {
		return SavedStationInfo{}, clientError("unknown saved station")
	}
	removed, err := h.store.DeleteSavedStation(ctx, store.SavedStationID(id))
	if err != nil {
		return SavedStationInfo{}, clientError("unknown saved station")
	}
	if err := h.logActivity(ctx, scope, store.ActivitySavedStationDelete, "saved_station", string(removed.ID), removed.LibraryID); err != nil {
		return SavedStationInfo{}, err
	}
	return savedStationInfo(removed), nil
}
