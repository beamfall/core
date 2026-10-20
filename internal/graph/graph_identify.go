package graph

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/identify"
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
	filter := scope.LibraryFilter()
	if _, err := h.store.Media(ctx, store.MediaID(mediaID), filter); err != nil {
		return MediaInfo{}, clientError("unknown media")
	}
	if err := h.identify.Apply(ctx, store.MediaID(mediaID), pluginID, scraperID, candidateID); err != nil {
		return MediaInfo{}, err
	}
	// Pin every file backing the Media so the corrected match survives rescans
	// (a Media may carry several files across Versions/Parts, each keyed by its
	// own content fingerprint, FREEZE-3 / ADR-0009 §7).
	files, err := h.store.MediaFilesForMedia(ctx, store.MediaID(mediaID), filter)
	if err != nil {
		return MediaInfo{}, err
	}
	for _, file := range files {
		if file.ContentFingerprint == "" {
			continue
		}
		if _, err := h.store.SetMatchPinned(ctx, file.ContentFingerprint, true); err != nil {
			return MediaInfo{}, err
		}
	}
	// The item is resolved — drop its Unmatched-queue row if one was parked.
	if _, err := h.store.ClearUnmatched(ctx, store.MediaID(mediaID)); err != nil {
		return MediaInfo{}, err
	}
	media, err := h.store.Media(ctx, store.MediaID(mediaID), filter)
	if err != nil {
		return MediaInfo{}, clientError("unknown media")
	}
	return h.mediaInfo(ctx, scope, media)
}

func (h *Handler) associateImportUnmatchedRecord(ctx context.Context, scope auth.Scope, mediaID, recordID, importRunID string) (MediaInfo, error) {
	if !scope.Revealed {
		return MediaInfo{}, clientError("unknown unmatched record")
	}
	filter := scope.LibraryFilter()
	if _, err := h.store.Media(ctx, store.MediaID(mediaID), filter); err != nil {
		return MediaInfo{}, clientError("unknown media")
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
	if mediaID != "" {
		if _, err := h.store.Media(ctx, store.MediaID(mediaID), scope.LibraryFilter()); err != nil {
			return nil, clientError("unknown media")
		}
	}
	candidates, err := h.identify.Search(ctx, pluginID, scraperID, query, store.MediaID(mediaID))
	if err != nil {
		return nil, err
	}
	return h.scoreFixMatchCandidates(pluginID, scraperID, query, candidates), nil
}

// scoreFixMatchCandidates scores every candidate against the operator's query as
// the parsed guess, orders them best-first, and stamps each with its taxonomy
// reason. The tie rule mirrors identify.bestCandidate's fail-closed posture: a
// top score shared by two or more candidates is "ambiguous", so the Console
// never presents a coin-flip as a confident answer.
func (h *Handler) scoreFixMatchCandidates(pluginID, scraperID, query string, candidates []identify.Candidate) []FixMatchCandidateInfo {
	parsed := identify.ParsedMedia{Title: query}
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
	media, err := h.store.Media(ctx, store.MediaID(mediaID), scope.LibraryFilter())
	if err != nil {
		return nil, clientError("unknown media")
	}
	candidate, err := h.identify.Lookup(ctx, pluginID, scraperID, candidateID, store.MediaID(mediaID))
	if err != nil {
		return nil, err
	}
	return []FixMatchFieldDiff{
		fieldDiff("title", media.Title, candidate.Title),
		fieldDiff("summary", media.Summary, candidate.Summary),
		fieldDiff("year", yearText(media.Year), yearText(candidate.Year)),
	}, nil
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
