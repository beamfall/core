package graph

import (
	"context"
	"errors"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/l10n"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// PIPE-1 graph half: the Unmatched-queue read + the "Fix match"
// mutation. The scan-time pipeline (internal/identify) applies a confident match
// autonomously and parks a low-confidence one in the Unmatched queue; these
// resolvers are the operator surface ADR-0003 §4 requires for v1 — review the
// queue, then re-identify-and-pin a corrected match so the fix survives rescans.
// New resolver bodies live here (PIPE-1 collision-avoidance); only the central
// dispatch in graph.go gains a minimal additive case.

// UnmatchedItemInfo is one Unmatched-queue row on the wire — the Console's
// "couldn't auto-match these" list. Generic shape (invariant #1): pluginID
// carries the species, never a domain word. Confidence/Threshold let the Console
// render "best guess was <title> at 0.62, below the 0.75 cut" so the operator
// knows why it queued.
type UnmatchedItemInfo struct {
	MediaID        string  `json:"mediaID"`
	Title          string  `json:"title"`
	PluginID       string  `json:"pluginID"`
	ScraperID      string  `json:"scraperID"`
	Confidence     float64 `json:"confidence"`
	Threshold      float64 `json:"threshold"`
	CandidateID    string  `json:"candidateID,omitempty"`
	CandidateTitle string  `json:"candidateTitle,omitempty"`
	QueuedAt       string  `json:"queuedAt,omitempty"`
	// FailureReason is the ADR-0068 §A taxonomy word explaining why the item is
	// parked rather than matched: "no-candidates" | "below-threshold" |
	// "ambiguous". Shape words (invariant #1) the Console renders verbatim.
	FailureReason string `json:"failureReason,omitempty"`
}

// candidateReasonUnscored is the per-candidate counterpart to the ADR-0068 §A
// record taxonomy: the plugin registered no MatchScorer, so no confidence exists
// to compare against a threshold. It is deliberately NOT a store taxonomy
// constant — the record-level taxonomy explains why identity FAILED, this
// explains why one candidate carries no score.
const candidateReasonUnscored = "unscored"

// unmatchedFailureReason maps one queue row onto the ADR-0068 §A failure-reason
// taxonomy. The queue only ever holds items the pipeline refused to auto-apply,
// so the three shapes are exhaustive: no candidate at all, a best candidate that
// scored under the plugin's threshold, or a best candidate that cleared the
// threshold and was still parked — which is precisely the fail-closed ambiguous
// tie bestCandidate refuses to break (internal/identify/pipeline.go).
func unmatchedFailureReason(item store.UnmatchedItem) string {
	if item.CandidateID == "" {
		return store.UnmatchedReasonNoCandidates
	}
	if item.Confidence < item.Threshold {
		return store.UnmatchedReasonBelowThreshold
	}
	return store.UnmatchedReasonAmbiguous
}

// unmatchedQueue lists the Unmatched-queue items the caller may see (manager-
// gated, reveal-and-Library filtered in the store read). The store joins the
// live title into the visibility-filtered page, avoiding one Media query per
// queue item.
func (h *Handler) unmatchedQueue(ctx context.Context, scope auth.Scope, req request) ([]UnmatchedItemInfo, error) {
	limit := boundedIntVar(req, "limit", curationPageDefault, curationPageMax)
	offset := boundedNonNegativeInt(nonNegativeIntVar(req, "offset"), curationOffsetMax)
	items, err := h.store.UnmatchedItems(ctx, scope.LibraryFilter(), limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]UnmatchedItemInfo, 0, len(items))
	for _, item := range items {
		info := UnmatchedItemInfo{
			MediaID:        string(item.MediaID),
			Title:          item.Title,
			PluginID:       item.PluginID,
			ScraperID:      item.ScraperID,
			Confidence:     item.Confidence,
			Threshold:      item.Threshold,
			CandidateID:    item.CandidateID,
			CandidateTitle: item.CandidateTitle,
			FailureReason:  unmatchedFailureReason(item),
		}
		if !item.QueuedAt.IsZero() {
			info.QueuedAt = item.QueuedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, info)
	}
	return out, nil
}

// dismissUnmatched removes a Media item from the Unmatched queue without
// pinning a match — operator acknowledgment that the item should remain
// unidentified. The queue row is cleared; a future rescan may re-queue it if
// the pipeline confidence is still below threshold. Manager-gated; the Media
// must be visible to the caller.
func (h *Handler) dismissUnmatched(ctx context.Context, scope auth.Scope, req request) (bool, error) {
	mediaID := trimmedVariable(req, "mediaID")
	if mediaID == "" {
		return false, clientError("mediaID is required")
	}
	if _, err := h.store.Media(ctx, store.MediaID(mediaID), scope.LibraryFilter()); err != nil {
		return false, clientError("unknown media")
	}
	removed, err := h.store.ClearUnmatched(ctx, store.MediaID(mediaID))
	if err != nil {
		return false, err
	}
	return removed, nil
}

// addMediaExternalID manually attaches an external identifier to a Media item.
// Useful when the identification pipeline could not auto-match — the operator
// asserts the correct scheme/value pair so downstream processes (re-scrape,
// enrichment, migration matching) can pick it up. Manager-gated; the Media
// must be visible to the caller.
func (h *Handler) addMediaExternalID(ctx context.Context, scope auth.Scope, req request) (MediaInfo, error) {
	mediaID := trimmedVariable(req, "mediaID")
	scheme := trimmedVariable(req, "scheme")
	value := trimmedVariable(req, "value")
	if mediaID == "" || scheme == "" || value == "" {
		return MediaInfo{}, clientError("mediaID, scheme, and value are required")
	}
	filter := scope.LibraryFilter()
	media, err := h.store.Media(ctx, store.MediaID(mediaID), filter)
	if err != nil {
		return MediaInfo{}, clientError("unknown media")
	}
	if err := h.store.AddMediaExternalID(ctx, store.MediaID(mediaID), scheme, value); err != nil {
		return MediaInfo{}, err
	}
	return h.mediaInfo(ctx, scope, media)
}

// fixMatch re-identifies one Media item with an operator-chosen candidate and
// PINS the resulting match so a later rescan leaves it alone (FREEZE-3 / ADR-0009
// §7 — "Fix match re-identifies and pins against rescans", ADR-0003 §4). It
// reuses the existing identify.Apply (the same additive, file-untouching write as
// identifyApply), sets the FREEZE-3 match_pinned column on every file backing the
// Media (the pin is per content fingerprint), and clears the item's Unmatched-
// queue row now that it is resolved. Manager-gated; the Media is reveal-and-
// Library filtered like every other mutation target.
func (h *Handler) fixMatch(ctx context.Context, scope auth.Scope, req request) (MediaInfo, error) {
	mediaID := trimmedVariable(req, "mediaID")
	recordID := trimmedVariable(req, "recordID")
	importRunID := trimmedVariable(req, "importRunID")
	if recordID != "" || importRunID != "" {
		if mediaID == "" || recordID == "" || importRunID == "" {
			return MediaInfo{}, clientError("mediaID, recordID, and importRunID are required")
		}
		return h.associateImportUnmatchedRecord(ctx, scope, mediaID, recordID, importRunID)
	}
	pluginID := trimmedVariable(req, "pluginID")
	scraperID := trimmedVariable(req, "scraperID")
	candidateID := trimmedVariable(req, "candidateID")
	if mediaID == "" || pluginID == "" || scraperID == "" || candidateID == "" {
		return MediaInfo{}, clientError("mediaID, pluginID, scraperID, and candidateID are required")
	}
	ordinal, err := fixMatchOrdinal(req)
	if err != nil {
		return MediaInfo{}, err
	}
	vars := newVars(req)
	clearLocks, _ := vars.StringList("clearLocks", "strings")
	if err := vars.Err(); err != nil {
		return MediaInfo{}, err
	}
	filter := scope.LibraryFilter()
	media, err := h.fixMatchMedia(ctx, scope, store.MediaID(mediaID))
	if err != nil {
		return MediaInfo{}, err
	}
	// The identification, match pin, Unmatched-queue resolution and the
	// fix-match audit revision commit together on the Media's own plane.
	if err := h.identify.ApplyFixMatchRequest(ctx, identify.FixMatchRequest{
		MediaID: media.ID, PluginID: pluginID, ScraperID: scraperID, CandidateID: candidateID,
		ClearLocks: clearLocks, Ordinal: ordinal, Actor: scope.ProfileID, Filter: filter,
	}); err != nil {
		if errors.Is(err, store.ValidationError("unknown lockable field")) {
			return MediaInfo{}, clientError("unknown lockable field")
		}
		return MediaInfo{}, err
	}
	if len(clearLocks) != 0 {
		if err := h.logActivity(ctx, scope, store.ActivityMediaFieldLock, "media", mediaID, media.LibraryID); err != nil {
			return MediaInfo{}, err
		}
	}
	media, err = h.store.Media(ctx, media.ID, filter)
	if err != nil {
		return MediaInfo{}, clientError("unknown media")
	}
	return h.mediaInfo(ctx, scope, media)
}

func fixMatchOrdinal(req request) (*store.MediaOrdinal, error) {
	v := newVars(req)
	sequence, hasSequence := v.String("sequence")
	if v.Err() != nil || utf8.RuneCountInString(sequence) > 32 {
		return nil, &l10n.Error{
			Key: l10n.FixMatchInvalidSequence,
			Err: clientError("sequence must be a string of at most 32 characters"),
		}
	}
	index, hasIndex := v.Number("index")
	if v.Err() != nil || index != math.Trunc(index) || index < 0 || index > math.MaxInt32 {
		return nil, &l10n.Error{
			Key: l10n.FixMatchInvalidIndex,
			Err: clientError("index must be an integer from 0 to 2147483647"),
		}
	}
	if !hasSequence && !hasIndex {
		return nil, nil
	}
	return &store.MediaOrdinal{Sequence: sequence, Index: int(index)}, nil
}

func (h *Handler) clearMediaOrdinal(ctx context.Context, scope auth.Scope, req request) (bool, error) {
	media, err := h.fixMatchMedia(ctx, scope, store.MediaID(trimmedVariable(req, "mediaID")))
	if err != nil {
		return false, err
	}
	cleared, err := h.identify.ClearMediaOrdinal(ctx, media.ID)
	if err != nil {
		return false, err
	}
	if cleared {
		h.logMediaActivity(ctx, scope, store.ActivityMediaFieldLock, media.ID)
	}
	return cleared, nil
}

// FixMatchSourceInfo describes a searchable scraper available to this Media.
type FixMatchSourceInfo struct {
	PluginID         string   `json:"pluginID"`
	ScraperID        string   `json:"scraperID"`
	Label            string   `json:"label"`
	SupportsChildren bool     `json:"supportsChildren"`
	ChildLevels      []string `json:"childLevels" graphshape:"nonnull"`
	AcceptsPastedID  bool     `json:"acceptsPastedID"`
	IsDefault        bool     `json:"isDefault"`
}

func (h *Handler) fixMatchSources(ctx context.Context, scope auth.Scope, req request) ([]FixMatchSourceInfo, error) {
	media, err := h.fixMatchMedia(ctx, scope, store.MediaID(trimmedVariable(req, "mediaID")))
	if err != nil {
		return nil, err
	}
	sources := []FixMatchSourceInfo{}
	pluginID, err := h.store.LibraryPluginID(ctx, media.LibraryID)
	if err != nil {
		return nil, err
	}
	if pluginID == "" || h.plugins == nil {
		return sources, nil
	}
	bundle, ok := h.plugins.Bundle(plugin.ID(pluginID))
	if !ok {
		return sources, nil
	}
	current, _, err := h.store.MediaMetadataProvenance(ctx, media.ID)
	if err != nil {
		return nil, err
	}
	for _, scraper := range bundle.Scrapers {
		if scraper.SearchTemplate == "" || plugin.LocalFileScraperKind(scraper.Kind) {
			continue
		}
		label := scraper.DisplayName.Resolve("")
		if label == "" {
			label = scraper.ID
		}
		levels := make([]string, len(scraper.Children))
		for i, listing := range scraper.Children {
			levels[i] = listing.Level
		}
		source := FixMatchSourceInfo{
			PluginID:         pluginID,
			ScraperID:        scraper.ID,
			Label:            label,
			SupportsChildren: len(levels) > 0,
			ChildLevels:      levels,
			AcceptsPastedID:  scraper.LookupTemplate != "" || len(scraper.LookupByIDTemplate) != 0,
		}
		if current.PluginID == pluginID && current.ScraperID == scraper.ID {
			sources = slices.Insert(sources, 0, source)
		} else {
			sources = append(sources, source)
		}
	}
	if len(sources) != 0 {
		sources[0].IsDefault = true
	}
	return sources, nil
}

// FixMatchCurrentInfo pairs the applied candidate with the item's stored title and year.
type FixMatchCurrentInfo struct {
	PluginID    string `json:"pluginID"`
	ScraperID   string `json:"scraperID"`
	CandidateID string `json:"candidateID"`
	Title       string `json:"title"`
	Year        int    `json:"year"`
}

func (h *Handler) fixMatchCurrent(ctx context.Context, scope auth.Scope, req request) (*FixMatchCurrentInfo, error) {
	media, err := h.fixMatchMedia(ctx, scope, store.MediaID(trimmedVariable(req, "mediaID")))
	if err != nil {
		return nil, err
	}
	current, found, err := h.store.MediaMetadataProvenance(ctx, media.ID)
	if err != nil || !found {
		return nil, err
	}
	return &FixMatchCurrentInfo{
		PluginID: current.PluginID, ScraperID: current.ScraperID, CandidateID: current.CandidateID,
		Title: media.Title, Year: media.Year,
	}, nil
}

func (h *Handler) fixMatchMedia(ctx context.Context, scope auth.Scope, mediaID store.MediaID) (store.Media, error) {
	if mediaID == "" {
		return store.Media{}, clientError("mediaID is required")
	}
	media, err := h.store.Media(ctx, mediaID, scope.LibraryFilter())
	if err != nil {
		return store.Media{}, clientError("unknown media")
	}
	// Candidate provenance is only supported in the shared catalog.
	encrypted, err := h.store.LibraryEncrypted(ctx, media.LibraryID)
	if err != nil {
		return store.Media{}, err
	}
	if encrypted {
		return store.Media{}, &l10n.Error{
			Key: l10n.FixMatchEncryptedLibrary,
			Err: clientError("fix match is unavailable in encrypted libraries"),
		}
	}
	return media, nil
}

func (h *Handler) associateImportUnmatchedRecord(ctx context.Context, scope auth.Scope, mediaID, recordID, importRunID string) (MediaInfo, error) {
	if !scope.Revealed {
		return MediaInfo{}, clientError("unknown unmatched record")
	}
	filter := scope.LibraryFilter()
	if _, err := h.fixMatchMedia(ctx, scope, store.MediaID(mediaID)); err != nil {
		return MediaInfo{}, err
	}
	ok, err := h.store.AssociateImportUnmatchedRecord(ctx, recordID, importRunID, store.MediaID(mediaID), filter, time.Now().UTC())
	if err != nil {
		return MediaInfo{}, err
	}
	if !ok {
		return MediaInfo{}, clientError("unknown unmatched record")
	}
	media, err := h.store.Media(ctx, store.MediaID(mediaID), filter)
	if err != nil {
		return MediaInfo{}, clientError("unknown media")
	}
	return h.mediaInfo(ctx, scope, media)
}

// FixMatchCandidateInfo is one SCORED Fix-Match candidate on the wire
// (METAPARITY-4, ADR-0068 §A). NGAUDIT-C4's identifySearch returns a bare pick
// list; parity needs the operator to see, per candidate, how confident the
// plugin's scorer is, which source produced it, and — when it is not the
// obvious answer — the shape word saying why. Confidence/Threshold/Reason are
// the scorer's own verdict, never re-derived here.
type FixMatchCandidateInfo struct {
	ID      string            `json:"id"`
	Title   string            `json:"title"`
	Summary string            `json:"summary,omitempty"`
	Year    int               `json:"year,omitempty"`
	Artwork map[string]string `json:"artwork,omitempty"`
	// PluginID/ScraperID are the "why this candidate" source attribution: which
	// plugin's scraper produced the row. Species lives in the id (invariant #1).
	PluginID   string  `json:"pluginID"`
	ScraperID  string  `json:"scraperID"`
	Confidence float64 `json:"confidence"`
	Threshold  float64 `json:"threshold"`
	// Scored is false when the plugin registered no MatchScorer — Confidence and
	// Threshold are then meaningless and the Console must not render them as 0%.
	Scored bool `json:"scored"`
	// Reason is "" when the candidate clears the threshold unambiguously;
	// otherwise the taxonomy shape word ("below-threshold" | "ambiguous" |
	// "unscored") that explains why it is not the automatic answer.
	Reason string `json:"reason,omitempty"`
}

// fixMatchCandidates runs the scraper search and returns the candidates SCORED
// against the operator's query, best first. It is the read the
// Fix-Match panel uses in place of the bare identifySearch list. Reveal-and-
// Library gated on mediaID exactly like identifySearch — a hidden item's
// fingerprints must not seed an outbound query.
func (h *Handler) fixMatchCandidates(ctx context.Context, scope auth.Scope, req request) ([]FixMatchCandidateInfo, error) {
	pluginID := trimmedVariable(req, "pluginID")
	scraperID := trimmedVariable(req, "scraperID")
	mediaID := trimmedVariable(req, "mediaID")
	query := trimmedVariable(req, "query")
	if pluginID == "" || scraperID == "" {
		return nil, clientError("pluginID and scraperID are required")
	}
	if query == "" && mediaID == "" {
		return nil, clientError("query or mediaID is required")
	}
	year, err := optionalInt(req, "year")
	if err != nil {
		return nil, err
	}
	parsed := identify.ParsedMedia{Title: query}
	if year != nil {
		if *year <= 0 {
			return nil, clientError("year must be positive")
		}
		parsed.Year = *year
	}
	if mediaID != "" {
		if _, err := h.fixMatchMedia(ctx, scope, store.MediaID(mediaID)); err != nil {
			return nil, err
		}
	}
	candidates, err := h.identify.SearchYear(ctx, pluginID, scraperID, query, store.MediaID(mediaID), parsed.Year)
	if err != nil {
		return nil, err
	}
	return h.scoreFixMatchCandidates(pluginID, scraperID, parsed, candidates), nil
}

func (h *Handler) fixMatchChildren(ctx context.Context, scope auth.Scope, req request) ([]scrape.ChildRow, error) {
	if _, err := h.fixMatchMedia(ctx, scope, store.MediaID(trimmedVariable(req, "mediaID"))); err != nil {
		return nil, err
	}
	v := newVars(req)
	candidateID, _ := v.String("candidateID")
	parent, hasParent := v.String("parentKey")
	if err := v.Err(); err != nil {
		return nil, err
	}
	level, err := optionalInt(req, "level")
	if err != nil {
		return nil, err
	}
	var parentKey *string
	if hasParent {
		parentKey = &parent
	}
	var levelNumber int
	if level != nil {
		levelNumber = *level
	}
	rows, err := h.identify.ListChildren(ctx, trimmedVariable(req, "pluginID"), trimmedVariable(req, "scraperID"), candidateID, levelNumber, parentKey)
	return rows, fixMatchChildError(err)
}

func fixMatchChildError(err error) error {
	var key l10n.Key
	switch err {
	case scrape.ErrNoChildListing:
		key = l10n.FixMatchNoChildListing
	case scrape.ErrNoSuchChildLevel:
		key = l10n.FixMatchNoChildLevel
	case scrape.ErrChildCandidate:
		key = l10n.FixMatchInvalidCandidate
	case scrape.ErrChildParentExtra:
		key = l10n.FixMatchExtraParent
	case scrape.ErrChildParentMissing:
		key = l10n.FixMatchMissingParent
	case scrape.ErrChildParentKey:
		key = l10n.FixMatchInvalidParent
	case scrape.ErrChildParentRow:
		key = l10n.FixMatchUnknownParent
	case scrape.ErrChildParentAmbig:
		key = l10n.FixMatchAmbiguousParent
	case scrape.ErrChildOrdinal:
		key = l10n.FixMatchInvalidParentOrdinal
	default:
		return err
	}
	return &l10n.Error{Key: key, Err: clientError(err.Error())}
}

// scoreFixMatchCandidates scores every candidate against the operator's query
// (and year, when given) as the parsed guess, orders them best-first, and stamps each with its taxonomy
// reason. The tie rule mirrors identify.bestCandidate's fail-closed posture: a
// top score shared by two or more candidates is "ambiguous", so the Console
// never presents a coin-flip as a confident answer.
func (h *Handler) scoreFixMatchCandidates(pluginID, scraperID string, parsed identify.ParsedMedia, candidates []identify.Candidate) []FixMatchCandidateInfo {
	decisions := make([]identify.MatchDecision, len(candidates))
	for i, candidate := range candidates {
		decisions[i] = h.identify.ScoreMatch(pluginID, candidate, parsed)
	}
	order := make([]int, len(candidates))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return decisions[order[a]].Score > decisions[order[b]].Score
	})
	out := make([]FixMatchCandidateInfo, 0, len(order))
	for rank, index := range order {
		candidate, decision := candidates[index], decisions[index]
		out = append(out, FixMatchCandidateInfo{
			ID:         candidate.ID,
			Title:      candidate.Title,
			Summary:    candidate.Summary,
			Year:       candidate.Year,
			Artwork:    candidate.Artwork,
			PluginID:   pluginID,
			ScraperID:  scraperID,
			Confidence: decision.Score,
			Threshold:  decision.Threshold,
			Scored:     decision.Scored,
			Reason:     candidateReason(decision, rank == 0 && !tiedAtTop(decisions, order)),
		})
	}
	return out
}

// tiedAtTop reports whether the two best-scoring candidates share a score — the
// ADR-0068 fail-closed ambiguity the pipeline refuses to auto-apply.
func tiedAtTop(decisions []identify.MatchDecision, order []int) bool {
	return len(order) > 1 && decisions[order[0]].Score == decisions[order[1]].Score
}

// candidateReason names why one candidate is not the automatic answer, or ""
// when it is: the sole candidate at or above its plugin's threshold.
func candidateReason(decision identify.MatchDecision, soleTop bool) string {
	switch {
	case !decision.Scored:
		return candidateReasonUnscored
	case decision.Score < decision.Threshold:
		return store.UnmatchedReasonBelowThreshold
	case !soleTop:
		return store.UnmatchedReasonAmbiguous
	}
	return ""
}

// identifyLookup fetches ONE candidate by its scraper-native id, without writing
// (METAPARITY-4 paste-an-id). The operator who already knows the external id —
// pasted from the provider — skips the search entirely; the fetched candidate
// then flows through the same preview-then-apply path as a searched one. Read-
// only and reveal-gated on the optional mediaID like identifySearch.
func (h *Handler) identifyLookup(ctx context.Context, scope auth.Scope, req request) (IdentifyCandidate, error) {
	pluginID := trimmedVariable(req, "pluginID")
	scraperID := trimmedVariable(req, "scraperID")
	candidateID := trimmedVariable(req, "candidateID")
	mediaID := trimmedVariable(req, "mediaID")
	if pluginID == "" || scraperID == "" || candidateID == "" {
		return IdentifyCandidate{}, clientError("pluginID, scraperID, and candidateID are required")
	}
	if mediaID != "" {
		if _, err := h.store.Media(ctx, store.MediaID(mediaID), scope.LibraryFilter()); err != nil {
			return IdentifyCandidate{}, clientError("unknown media")
		}
	}
	candidate, err := h.identify.Lookup(ctx, pluginID, scraperID, candidateID, store.MediaID(mediaID))
	if err != nil {
		return IdentifyCandidate{}, err
	}
	return IdentifyCandidate{
		ID:      candidate.ID,
		Title:   candidate.Title,
		Summary: candidate.Summary,
		Year:    candidate.Year,
		Artwork: candidate.Artwork,
	}, nil
}

// FixMatchFieldDiff is one field's before/after in the preview-before-apply diff.
// Field is the shape-named core metadata field; Current is what
// the Media row holds today and Proposed is what applying the candidate would
// write. Both are rendered as text so the Console needs no per-field typing.
type FixMatchFieldDiff struct {
	Field    string `json:"field"`
	Current  string `json:"current"`
	Proposed string `json:"proposed"`
	Changed  bool   `json:"changed"`
}

// fixMatchPreview shows what applying a candidate WOULD change, writing nothing.
// It looks the candidate up through the same engine path Apply
// uses and diffs the three core descriptive fields Apply writes onto the Media
// row (title/summary/year, identify.Apply). A field the candidate does not carry
// is reported unchanged rather than as a proposed blanking — Apply skips absent
// fields, so the preview must too or it would promise a deletion that never
// happens. Manager-gated and reveal-and-Library filtered like fixMatch itself.
func (h *Handler) fixMatchPreview(ctx context.Context, scope auth.Scope, req request) ([]FixMatchFieldDiff, error) {
	mediaID := trimmedVariable(req, "mediaID")
	pluginID := trimmedVariable(req, "pluginID")
	scraperID := trimmedVariable(req, "scraperID")
	candidateID := trimmedVariable(req, "candidateID")
	if mediaID == "" || pluginID == "" || scraperID == "" || candidateID == "" {
		return nil, clientError("mediaID, pluginID, scraperID, and candidateID are required")
	}
	media, err := h.fixMatchMedia(ctx, scope, store.MediaID(mediaID))
	if err != nil {
		return nil, err
	}
	ordinal, err := fixMatchOrdinal(req)
	if err != nil {
		return nil, err
	}
	// clearLocks mirrors fixMatch so the preview runs the lookup Apply will run,
	// including a same-candidate Apply that clears the operator ordinal.
	vars := newVars(req)
	clearLocks, _ := vars.StringList("clearLocks", "strings")
	if err := vars.Err(); err != nil {
		return nil, err
	}
	candidate, title, err := h.identify.PreviewLookup(ctx, pluginID, scraperID, candidateID, media.ID, clearLocks, ordinal)
	if err != nil {
		return nil, err
	}
	// A field a sidecar locks keeps its value whatever the candidate says:
	// Apply cannot clear that lock, so the preview proposes no change.
	locks, err := h.store.MediaFieldLocks(ctx, media.ID, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	diffs := []FixMatchFieldDiff{
		fieldDiff("title", media.Title, title),
		fieldDiff("summary", media.Summary, candidate.Summary),
		fieldDiff("year", yearText(media.Year), yearText(candidate.Year)),
	}
	for _, lock := range locks {
		if !lock.Local {
			continue
		}
		for i := range diffs {
			if diffs[i].Field == lock.Field {
				diffs[i] = fieldDiff(lock.Field, diffs[i].Current, "")
			}
		}
	}
	return diffs, nil
}

// FixMatchLockedFieldInfo carries a manually locked value alongside the preview.
type FixMatchLockedFieldInfo struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

func (h *Handler) fixMatchLockedFields(ctx context.Context, scope auth.Scope, req request) ([]FixMatchLockedFieldInfo, error) {
	media, err := h.fixMatchMedia(ctx, scope, store.MediaID(trimmedVariable(req, "mediaID")))
	if err != nil {
		return nil, err
	}
	filter := scope.LibraryFilter()
	locks, err := h.store.MediaFieldLocks(ctx, media.ID, filter)
	if err != nil {
		return nil, err
	}
	fields := []FixMatchLockedFieldInfo{}
	ordinal, err := h.store.MediaOperatorOrdinal(ctx, media.ID)
	if err != nil {
		return nil, err
	}
	if ordinal != nil {
		value := strconv.Itoa(ordinal.Index)
		if ordinal.Sequence != "" {
			value = ordinal.Sequence + " / " + value
		}
		fields = append(fields, FixMatchLockedFieldInfo{Field: store.MediaOrdinalLockField, Value: value})
	}
	var pluginValues map[string]string
	for _, lock := range locks {
		// A sidecar's lock is not one Fix Match clears: clearLocks lifts only
		// manual locks, so listing it would promise a clear that never lands.
		if !lock.Locked || lock.Local || (ordinal != nil && lock.Field == store.MediaOrdinalLockField) {
			continue
		}
		var value string
		switch lock.Field {
		case "title":
			value = media.Title
		case "summary":
			value = media.Summary
		case "year":
			value = yearText(media.Year)
		case "sortTitle":
			value, err = h.store.MediaSortTitle(ctx, media.ID, filter)
			if err != nil {
				return nil, err
			}
		case store.ArtworkLockField("poster"), store.ArtworkLockField("backdrop"), store.ArtworkLockField("thumb"):
			slot := strings.ToLower(strings.TrimPrefix(lock.Field, "artwork"))
			value = h.mediaArtworkSlots(media.ID)[slot]
		default:
			if pluginValues == nil {
				stored, err := h.mediaFieldsQuery(ctx, scope, req)
				if err != nil {
					return nil, err
				}
				pluginValues = make(map[string]string, len(stored))
				for _, field := range stored {
					pluginValues[field.Name] = field.Value
				}
			}
			var ok bool
			value, ok = pluginValues[lock.Field]
			if !ok {
				continue
			}
		}
		fields = append(fields, FixMatchLockedFieldInfo{Field: lock.Field, Value: value})
	}
	return fields, nil
}

// fieldDiff pairs one field's current and proposed text. An empty proposal is
// "no value carried", so it reports the current value unchanged.
func fieldDiff(field, current, proposed string) FixMatchFieldDiff {
	if proposed == "" {
		return FixMatchFieldDiff{Field: field, Current: current, Proposed: current}
	}
	return FixMatchFieldDiff{Field: field, Current: current, Proposed: proposed, Changed: proposed != current}
}

// yearText renders a year for the diff; the zero year is "no year", not "0".
func yearText(year int) string {
	if year == 0 {
		return ""
	}
	return strconv.Itoa(year)
}
