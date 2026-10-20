package graph

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
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
// generic shape labels, not domain types.
var stationSeedKinds = map[string]bool{
	"track": true, "artist": true, "album": true, "genre": true, "collection": true,
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
		return []StationCandidate{}, stationScarcityExplanation(0), nil
	}
	// 2. Collect candidates for the seed through reveal-filtered store reads,
	//    each constrained to the sealed-free music filter. Candidates are full
	//    Media rows so the hard filter + tuning can read real fields.
	candidates, err := h.collectStationCandidates(ctx, scope, filter, seed)
	if err != nil {
		return nil, "", err
	}
	// 3. Hard Source filters BEFORE ranking. rules.admits is the hard
	//    predicate: an item it rejects is removed from the candidate set here, so
	//    it can never appear in the window or an explanation. (The SEALED / HIDDEN
	//    / RESTRICTED / non-music suppression is an additional hard filter already
	//    enforced upstream by sealedFreeStationFilter + the per-read LibraryFilter.)
	//    The declarative rule vocabulary MUSIC-1 will hydrate (genre, mood, decade
	//    Entity predicates) layers onto this same pass; the field predicates wired
	//    today (year / duration, present on every Media) make the pass execute now.
	kept := candidates[:0]
	for _, m := range candidates {
		if rules.admits(m) {
			kept = append(kept, m)
		}
	}
	candidates = kept
	// 4. Rank: deterministic given rankSeed (debug reproducibility), then tuning
	//    hints bias the order. Tuning is RANKING-ONLY — it reorders the post-filter
	//    set and can never admit an item rule.admits already rejected (the rejected
	//    items are gone from `candidates` before tuning ever sees them).
	rankStationCandidates(candidates, tuning, rankSeed)
	// 5. Rolling window.
	if len(candidates) > stationWindowSize {
		candidates = candidates[:stationWindowSize]
	}
	out := make([]StationCandidate, len(candidates))
	for i, c := range candidates {
		out[i] = StationCandidate{
			MediaID:     string(c.ID),
			Title:       c.Title,
			Explanation: stationItemExplanation(seed),
		}
	}
	return out, stationScarcityExplanation(len(out)), nil
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
	// from the caller's scope are preserved.
	return store.LibraryFilter{
		AllowedIDs:    allowed,
		AllLibraries:  false,
		Revealed:      scope.Revealed,
		Certification: scope.Certification,
	}, nil
}

// collectStationCandidates gathers candidate Media (full rows, so the hard
// filter + tuning can read real fields) for the seed. Every id is resolved
// through the sealed-free music filter so no Sealed/hidden/non-music Media can
// enter the pool. Duplicate ids are collapsed keeping first appearance.
func (h *Handler) collectStationCandidates(ctx context.Context, scope auth.Scope, filter store.LibraryFilter, seed stationSeed) ([]store.Media, error) {
	var ids []store.MediaID
	switch seed.kind {
	case "track":
		// Resolve the seed itself through the sealed-free music filter: a seed
		// Media outside that scope is "unknown" — never an oracle for a hidden/
		// sealed/non-music id.
		media, err := h.store.Media(ctx, store.MediaID(seed.id), filter)
		if err != nil {
			return nil, clientError("unknown station seed")
		}
		ids = append(ids, media.ID)
	case "artist", "album", "genre":
		items, err := h.store.MediaForEntity(ctx, store.EntityID(seed.id), filter)
		if err != nil {
			return nil, clientError("unknown station seed")
		}
		for _, it := range items {
			ids = append(ids, it.ID)
		}
	case "collection":
		// MUSIC-4's canonical ordered-membership read (ADR-0098 area 4): it returns
		// the ordered static membership with Reveal/Sealed visibility already
		// applied under the sealed-free MUSIC filter, so non-music / Sealed / hidden
		// members are dropped (never gapped) before they can enter the candidate
		// pool. The MediaByIDs batch below re-resolves the same filter to fetch full
		// Media rows for the hard filter + tuning.
		members, err := h.store.ResolveCollectionMembers(ctx, store.CollectionID(seed.id), filter)
		if err != nil {
			return nil, clientError("unknown station seed")
		}
		for _, m := range members {
			ids = append(ids, m.MediaID)
		}
	default:
		return nil, clientError("seedKind must be one of track, artist, album, genre, collection")
	}
	// One batch read resolves every candidate id to a full Media row through the
	// sealed-free MUSIC filter. A CollectionMembers list may mix non-music Media
	// (it carries no library-kind); resolving through this filter drops every
	// non-music / Sealed / hidden member (ADR-0098: never cross those Libraries).
	visible, err := h.store.MediaByIDs(ctx, ids, filter)
	if err != nil {
		return nil, err
	}
	seen := make(map[store.MediaID]bool, len(ids))
	out := make([]store.Media, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if m, ok := visible[id]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// rankStationCandidates orders candidates deterministically for a given
// rankSeed (same seed → same order: debug reproducibility, ADR-0098), then
// applies the tuning bias. The base key mixes the rankSeed with each Media id so
// different sessions/seeds yield different-but-stable shuffles; tuning reorders
// the post-filter set ONLY and can never admit a filtered item (the rejected
// items are already removed before this runs).
func rankStationCandidates(candidates []store.Media, tuning stationTuning, rankSeed uint64) {
	sort.SliceStable(candidates, func(i, j int) bool {
		bi := tuning.bias(candidates[i])
		bj := tuning.bias(candidates[j])
		if bi != bj {
			// Higher bias ranks earlier.
			return bi > bj
		}
		return stationRankKey(candidates[i].ID, rankSeed) < stationRankKey(candidates[j].ID, rankSeed)
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
// filters apply before ranking). The predicates wired today read fields present
// on every Media (year, duration); the declarative rule vocabulary MUSIC-1 will
// hydrate (genre/mood/decade Entity predicates) layers onto the same admits()
// pass. A nil/zero field means "no constraint on that axis".
type stationRules struct {
	MinYear            int `json:"minYear"`
	MaxYear            int `json:"maxYear"`
	MinDurationSeconds int `json:"minDurationSeconds"`
	MaxDurationSeconds int `json:"maxDurationSeconds"`
}

// admits reports whether a Media passes every hard filter. An item it rejects is
// removed from the candidate set before ranking, so it can never reach the
// window or an explanation.
func (r stationRules) admits(m store.Media) bool {
	if r.MinYear > 0 && (m.Year == 0 || m.Year < r.MinYear) {
		return false
	}
	if r.MaxYear > 0 && m.Year > r.MaxYear {
		return false
	}
	if r.MinDurationSeconds > 0 && m.DurationSeconds < r.MinDurationSeconds {
		return false
	}
	if r.MaxDurationSeconds > 0 && m.DurationSeconds > r.MaxDurationSeconds {
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

// stationItemExplanation is the per-item, product-safe explanation. It names the
// seed shape only — never another Profile's behavior, a hidden/Sealed Library
// name, secrets, or a score vector.
func stationItemExplanation(seed stationSeed) string {
	switch seed.kind {
	case "track":
		return "from your track seed"
	case "artist":
		return "from your artist seed"
	case "album":
		return "from your album seed"
	case "genre":
		return "from your genre seed"
	case "collection":
		return "from your collection seed"
	}
	return ""
}

// stationScarcityExplanation is the Station-level explanation. When few items
// survived filtering it says so generically, never disclosing which Libraries
// were Sealed/hidden or why an item was dropped.
func stationScarcityExplanation(n int) string {
	if n == 0 {
		return "No tracks are available to start this station right now."
	}
	if n < stationWindowSize {
		return "Only a few tracks matched, so this station is short."
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
		return stationSeed{}, clientError("seedKind must be one of track, artist, album, genre, collection")
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
	err = h.store.QueueReplaceWithStation(ctx, scope.ProfileID, mediaIDs, boolVar(req, "confirmReplace"), scope.LibraryFilter())
	if errors.Is(err, store.ErrQueueReplaceNeedsConfirm) {
		return nil, clientError("queue has items; pass confirmReplace to replace it")
	}
	if err != nil {
		return nil, err
	}
	return h.queueItems(ctx, scope)
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
