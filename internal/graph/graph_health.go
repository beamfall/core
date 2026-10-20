package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/store"
)

// Library Health Dashboard resolvers. Dispatch for these fields is
// registered as a self-contained, Admin-Session-gated group in graph.go's
// execute(); the methods live here to keep the chokepoint edit localized (a
// sibling agent is concurrently adding its own dispatch blocks).
//
// Every read is reveal-and-Library filtered through scope.LibraryFilter(), like
// the rest of the contract. Every bulk mutation routes through the audited,
// Trash-ON path (invariant #6): bulk-trash-broken and bulk-merge-duplicates
// soft-delete via store.TrashMedia and log one media.trash activity entry per
// item, so nothing is hard-deleted and everything is restorable.

// LibraryHealthInfo is one Library's per-dimension counts on the wire. Counts
// is keyed by the fixed dimension vocabulary (unmatched/broken/scrape-failure/
// missing-artwork/duplicate). The aggregate summary row is the element-wise sum
// the client folds over the list (ADR-0042: no server-side aggregate score).
type LibraryHealthInfo struct {
	LibraryID string         `json:"libraryID"`
	Title     string         `json:"title"`
	Counts    map[string]int `json:"counts"`
}

// HealthItemInfo is one Media surfaced in a dimension drill-down — operator
// identity only, no domain metadata.
type HealthItemInfo struct {
	MediaID   string `json:"mediaID"`
	LibraryID string `json:"libraryID"`
	Title     string `json:"title"`
}

// DuplicateGroupInfo is one cluster of byte-identical Media. Items[0] is the
// suggested canonical survivor (lowest Media ID); a merge trashes the rest.
type DuplicateGroupInfo struct {
	Items []HealthItemInfo `json:"items"`
}

// BulkOpResult reports a bulk operation's outcome: how many Media were affected
// (rescanned / trashed / merged-away).
type BulkOpResult struct {
	Affected int `json:"affected"`
}

func healthItemInfo(item store.HealthItem) HealthItemInfo {
	return HealthItemInfo{
		MediaID:   string(item.MediaID),
		LibraryID: string(item.LibraryID),
		Title:     item.Title,
	}
}

// libraryHealth returns per-Library counts for every Library the caller can see.
func (h *Handler) libraryHealth(ctx context.Context, scope auth.Scope) ([]LibraryHealthInfo, error) {
	if h.store == nil {
		return []LibraryHealthInfo{}, nil
	}
	counts, err := h.store.LibraryHealthCounts(ctx, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	out := make([]LibraryHealthInfo, 0, len(counts))
	for _, c := range counts {
		wire := make(map[string]int, len(c.Counts))
		for dim, n := range c.Counts {
			wire[string(dim)] = n
		}
		out = append(out, LibraryHealthInfo{
			LibraryID: string(c.LibraryID),
			Title:     c.Title,
			Counts:    wire,
		})
	}
	return out, nil
}

// libraryHealthItems lists the Media in one Library exhibiting a dimension
// (drill-down). A malformed dimension answers with a stable client message; a
// hidden or unknown Library yields an empty list (never an existence oracle).
func (h *Handler) libraryHealthItems(ctx context.Context, scope auth.Scope, req request) ([]HealthItemInfo, error) {
	if h.store == nil {
		return []HealthItemInfo{}, nil
	}
	libraryID := trimmedVariable(req, "libraryID")
	dimension := trimmedVariable(req, "dimension")
	if libraryID == "" || dimension == "" {
		return nil, clientError("libraryID and dimension are required")
	}
	items, err := h.store.LibraryHealthItems(ctx, store.LibraryID(libraryID), store.HealthDimension(dimension), scope.LibraryFilter(), boundedIntVar(req, "limit", 200, 200))
	if err != nil {
		return nil, err
	}
	out := make([]HealthItemInfo, len(items))
	for i, item := range items {
		out[i] = healthItemInfo(item)
	}
	return out, nil
}

// libraryDuplicateGroups returns the duplicate clusters in one visible Library.
func (h *Handler) libraryDuplicateGroups(ctx context.Context, scope auth.Scope, req request) ([]DuplicateGroupInfo, error) {
	if h.store == nil {
		return []DuplicateGroupInfo{}, nil
	}
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return nil, clientError("libraryID is required")
	}
	groups, err := h.store.HealthDuplicateGroups(ctx, store.LibraryID(libraryID), scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	out := make([]DuplicateGroupInfo, len(groups))
	for i, group := range groups {
		items := make([]HealthItemInfo, len(group))
		for j, item := range group {
			items[j] = healthItemInfo(item)
		}
		out[i] = DuplicateGroupInfo{Items: items}
	}
	return out, nil
}

func (h *Handler) refreshHealthCache(ctx context.Context) error {
	if h.store == nil {
		return nil
	}
	return h.store.RefreshLibraryHealthCache(ctx, time.Now().UTC())
}

func (h *Handler) selectedHealthItems(ctx context.Context, scope auth.Scope, libraryID store.LibraryID, dim store.HealthDimension, mediaIDs []string) ([]store.HealthItem, error) {
	visible, err := h.visibleLibrary(ctx, scope, libraryID)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, clientError("unknown library")
	}
	items, err := h.store.LibraryHealthItems(ctx, libraryID, dim, scope.LibraryFilter(), 500)
	if err != nil {
		return nil, err
	}
	selected := map[store.MediaID]bool{}
	for _, id := range mediaIDs {
		selected[store.MediaID(id)] = true
	}
	out := []store.HealthItem{}
	for _, item := range items {
		if selected[item.MediaID] {
			out = append(out, item)
		}
	}
	return out, nil
}

func (h *Handler) remediateSelectedIdentification(ctx context.Context, scope auth.Scope, libraryID store.LibraryID, dim store.HealthDimension, mediaIDs []string) (BulkOpResult, error) {
	items, err := h.selectedHealthItems(ctx, scope, libraryID, dim, mediaIDs)
	if err != nil {
		return BulkOpResult{}, err
	}
	if len(items) == 0 {
		return BulkOpResult{Affected: 0}, nil
	}
	pluginID, err := h.store.LibraryPluginID(ctx, libraryID)
	if err != nil {
		return BulkOpResult{}, err
	}
	if h.identify != nil && pluginID != "" {
		for _, item := range items {
			if err := h.remediateSelectedMediaIdentification(ctx, scope, item.MediaID, pluginID); err != nil {
				return BulkOpResult{}, err
			}
		}
		if err := h.refreshHealthCache(ctx); err != nil {
			return BulkOpResult{}, err
		}
	}
	if err := h.logActivity(ctx, scope, store.ActivityLibraryRoots, "library", string(libraryID), libraryID); err != nil {
		return BulkOpResult{}, err
	}
	return BulkOpResult{Affected: len(items)}, nil
}

func (h *Handler) remediateSelectedMediaIdentification(ctx context.Context, scope auth.Scope, mediaID store.MediaID, pluginID string) error {
	media, err := h.store.Media(ctx, mediaID, scope.LibraryFilter())
	if err != nil {
		return err
	}
	file, err := h.store.PrimaryMediaFile(ctx, mediaID, scope.LibraryFilter())
	if err != nil {
		return err
	}
	_, err = h.identify.AutoIdentify(ctx, mediaID, pluginID, file.ContentFingerprint, identify.ParsedMedia{
		Title: media.Title,
		Class: string(media.Class),
		Year:  media.Year,
	})
	return err
}

// bulkRescanUnmatched enqueues a scan job for each persisted root of one visible
// Library, re-running the standard scan/identify pipeline over the unmatched
// backlog. It reuses the durable scan-job path (dedupes by library+root), so a
// rescan already queued is not duplicated. Returns the number of jobs enqueued.
func (h *Handler) bulkRescanUnmatched(ctx context.Context, scope auth.Scope, req request) (BulkOpResult, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return BulkOpResult{}, clientError("libraryID is required")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return BulkOpResult{}, err
	}
	if !visible {
		return BulkOpResult{}, clientError("unknown library")
	}
	roots, err := h.store.LibraryRoots(ctx, store.LibraryID(libraryID))
	if err != nil {
		return BulkOpResult{}, err
	}
	if len(roots) == 0 {
		return BulkOpResult{}, clientError("library has no roots")
	}
	enqueued := 0
	for _, root := range roots {
		job, err := scan.NewJob(scan.JobRequest{
			LibraryID: store.LibraryID(libraryID),
			Root:      root,
			CreatedAt: time.Now().UTC(),
		})
		if err != nil {
			return BulkOpResult{}, err
		}
		if _, err := h.store.EnqueueJob(ctx, job); err != nil {
			return BulkOpResult{}, err
		}
		if h.scanObserver != nil {
			h.scanObserver.Observe(store.LibraryID(libraryID), root)
		}
		enqueued++
	}
	if err := h.logActivity(ctx, scope, store.ActivityLibraryRoots, "library", libraryID, store.LibraryID(libraryID)); err != nil {
		return BulkOpResult{}, err
	}
	return BulkOpResult{Affected: enqueued}, nil
}

// bulkTrashBroken soft-deletes every broken Media in one visible Library through
// the audited Trash path (invariant #6 — recoverable, never hard-deleted). Each
// item is trashed individually and audited with a media.trash entry, exactly
// like a single trash mutation.
func (h *Handler) bulkTrashBroken(ctx context.Context, scope auth.Scope, req request) (BulkOpResult, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return BulkOpResult{}, clientError("libraryID is required")
	}
	return h.bulkTrash(ctx, scope, store.LibraryID(libraryID), store.HealthBroken, nil)
}

// bulkMergeDuplicates resolves a duplicate cluster by keeping the survivor and
// trashing every other member, through the same audited Trash path. The survivor
// (keepMediaID) must be one of the Library's duplicate Media; the rest of the
// matching duplicate set is trashed. Routes through Trash so a wrong merge is
// always reversible.
func (h *Handler) bulkMergeDuplicates(ctx context.Context, scope auth.Scope, req request) (BulkOpResult, error) {
	libraryID := trimmedVariable(req, "libraryID")
	keep := trimmedVariable(req, "keepMediaID")
	if libraryID == "" || keep == "" {
		return BulkOpResult{}, clientError("libraryID and keepMediaID are required")
	}
	if h.store == nil {
		return BulkOpResult{}, clientError("store unavailable")
	}
	batch, err := h.store.MergeApply(ctx, store.LibraryID(libraryID), store.MediaID(keep), scope.LibraryFilter(), time.Now().UTC())
	if err != nil {
		return BulkOpResult{}, err
	}
	if batch.ID != "" {
		if err := h.logActivity(ctx, scope, store.ActivityMediaMerge, "batch", batch.ID, store.LibraryID(libraryID)); err != nil {
			return BulkOpResult{Affected: batch.Affected}, err
		}
	}
	if batch.Affected > 0 {
		if err := h.refreshHealthCache(ctx); err != nil {
			return BulkOpResult{Affected: batch.Affected}, err
		}
	}
	return BulkOpResult{Affected: batch.Affected}, nil
}

// bulkTrash is the shared engine for trash-oriented health remediation: it
// fetches the dimension's live Media in a visible Library and trashes each
// (sparing the keep set) via the audited store.TrashMedia path, logging one
// media.trash entry per item. Duplicate merges use MergeApply so they stay
// cluster-scoped and undo-batched.
func (h *Handler) bulkTrash(ctx context.Context, scope auth.Scope, libraryID store.LibraryID, dim store.HealthDimension, keep map[store.MediaID]bool) (BulkOpResult, error) {
	if h.store == nil {
		return BulkOpResult{}, clientError("store unavailable")
	}
	visible, err := h.visibleLibrary(ctx, scope, libraryID)
	if err != nil {
		return BulkOpResult{}, err
	}
	if !visible {
		return BulkOpResult{}, clientError("unknown library")
	}
	items, err := h.store.LibraryHealthBulkItems(ctx, libraryID, dim, scope.LibraryFilter())
	if err != nil {
		return BulkOpResult{}, err
	}
	trashed := 0
	for _, item := range items {
		if keep[item.MediaID] {
			continue
		}
		if err := h.store.TrashMedia(ctx, item.MediaID, scope.LibraryFilter()); err != nil {
			return BulkOpResult{Affected: trashed}, err
		}
		if err := h.logActivity(ctx, scope, store.ActivityMediaTrash, "media", string(item.MediaID), item.LibraryID); err != nil {
			return BulkOpResult{Affected: trashed}, err
		}
		trashed++
	}
	if trashed > 0 {
		if err := h.refreshHealthCache(ctx); err != nil {
			return BulkOpResult{Affected: trashed}, err
		}
	}
	return BulkOpResult{Affected: trashed}, nil
}

// bulkRescrapeScrapeFailures re-enqueues a scan job for each root of one
// visible Library, re-running the identification pipeline over the
// scrape-failure backlog (unmatched items that produced no external ID).
// Uses the same durable scan-job path as bulkRescanUnmatched (dedupes by
// library+root). Returns the count of scrape-failure items for operator
// visibility; the job count equals the number of library roots.
func (h *Handler) bulkRescrapeScrapeFailures(ctx context.Context, scope auth.Scope, req request) (BulkOpResult, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return BulkOpResult{}, clientError("libraryID is required")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return BulkOpResult{}, err
	}
	if !visible {
		return BulkOpResult{}, clientError("unknown library")
	}
	roots, err := h.store.LibraryRoots(ctx, store.LibraryID(libraryID))
	if err != nil {
		return BulkOpResult{}, err
	}
	if len(roots) == 0 {
		return BulkOpResult{}, clientError("library has no roots")
	}
	// Count scrape-failure items so the operator knows the backlog size.
	items, err := h.store.LibraryHealthItems(ctx, store.LibraryID(libraryID), store.HealthScrapeFailure, scope.LibraryFilter(), 500)
	if err != nil {
		return BulkOpResult{}, err
	}
	for _, root := range roots {
		job, err := scan.NewJob(scan.JobRequest{
			LibraryID: store.LibraryID(libraryID),
			Root:      root,
			CreatedAt: time.Now().UTC(),
		})
		if err != nil {
			return BulkOpResult{}, err
		}
		if _, err := h.store.EnqueueJob(ctx, job); err != nil {
			return BulkOpResult{}, err
		}
		if h.scanObserver != nil {
			h.scanObserver.Observe(store.LibraryID(libraryID), root)
		}
	}
	if err := h.logActivity(ctx, scope, store.ActivityLibraryRoots, "library", libraryID, store.LibraryID(libraryID)); err != nil {
		return BulkOpResult{}, err
	}
	return BulkOpResult{Affected: len(items)}, nil
}

// bulkRescanUnmatchedSelected retries identification for selected unmatched
// Media only. It never falls back to a whole-root scan.
func (h *Handler) bulkRescanUnmatchedSelected(ctx context.Context, scope auth.Scope, req request) (BulkOpResult, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return BulkOpResult{}, clientError("libraryID is required")
	}
	mediaIDs := stringSliceVar(req, "mediaIDs")
	if len(mediaIDs) == 0 {
		return BulkOpResult{}, clientError("mediaIDs is required")
	}
	return h.remediateSelectedIdentification(ctx, scope, store.LibraryID(libraryID), store.HealthUnmatched, mediaIDs)
}

// bulkTrashBrokenSelected soft-deletes a caller-supplied subset of broken Media
// in one visible Library through the audited Trash path. IDs not in the broken
// dimension for this Library are silently ignored. Returns the count trashed.
func (h *Handler) bulkTrashBrokenSelected(ctx context.Context, scope auth.Scope, req request) (BulkOpResult, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return BulkOpResult{}, clientError("libraryID is required")
	}
	mediaIDs := stringSliceVar(req, "mediaIDs")
	if len(mediaIDs) == 0 {
		return BulkOpResult{}, clientError("mediaIDs is required")
	}
	include := map[store.MediaID]bool{}
	for _, id := range mediaIDs {
		include[store.MediaID(id)] = true
	}
	return h.bulkTrashSelected(ctx, scope, store.LibraryID(libraryID), store.HealthBroken, include)
}

// bulkRescrapeScrapeFailuresSelected retries identification for selected
// scrape-failure Media only. It never falls back to a whole-root scan.
func (h *Handler) bulkRescrapeScrapeFailuresSelected(ctx context.Context, scope auth.Scope, req request) (BulkOpResult, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return BulkOpResult{}, clientError("libraryID is required")
	}
	mediaIDs := stringSliceVar(req, "mediaIDs")
	if len(mediaIDs) == 0 {
		return BulkOpResult{}, clientError("mediaIDs is required")
	}
	return h.remediateSelectedIdentification(ctx, scope, store.LibraryID(libraryID), store.HealthScrapeFailure, mediaIDs)
}

// bulkFetchMissingArtworkSelected enqueues an artwork job scoped to the selected
// missing-artwork Media IDs. It never enqueues the global artwork sweep.
func (h *Handler) bulkFetchMissingArtworkSelected(ctx context.Context, scope auth.Scope, req request) (BulkOpResult, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return BulkOpResult{}, clientError("libraryID is required")
	}
	mediaIDs := stringSliceVar(req, "mediaIDs")
	if len(mediaIDs) == 0 {
		return BulkOpResult{}, clientError("mediaIDs is required")
	}
	items, err := h.selectedHealthItems(ctx, scope, store.LibraryID(libraryID), store.HealthMissingArtwork, mediaIDs)
	if err != nil {
		return BulkOpResult{}, err
	}
	if len(items) == 0 {
		return BulkOpResult{Affected: 0}, nil
	}
	selected := make([]store.MediaID, 0, len(items))
	for _, item := range items {
		selected = append(selected, item.MediaID)
	}
	job, err := artwork.NewSelectedSweepJob(selected, time.Now().UTC())
	if err != nil {
		return BulkOpResult{}, err
	}
	if _, err := h.store.EnqueueJob(ctx, job); err != nil {
		return BulkOpResult{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityLibraryRoots, "library", libraryID, store.LibraryID(libraryID)); err != nil {
		return BulkOpResult{}, err
	}
	return BulkOpResult{Affected: len(items)}, nil
}

// bulkMergeDuplicatesSelected trashes the non-survivor members of selected
// duplicate clusters by routing each fully selected cluster through MergeApply.
// IDs not in a visible duplicate cluster are silently ignored. Partial cluster
// selections are no-ops so unselected candidates are never silently trashed.
func (h *Handler) bulkMergeDuplicatesSelected(ctx context.Context, scope auth.Scope, req request) (BulkOpResult, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return BulkOpResult{}, clientError("libraryID is required")
	}
	if h.store == nil {
		return BulkOpResult{}, clientError("store unavailable")
	}
	mediaIDs := stringSliceVar(req, "mediaIDs")
	if len(mediaIDs) == 0 {
		return BulkOpResult{}, clientError("mediaIDs is required")
	}
	const maxSelectedDuplicateIDs = 200
	if len(mediaIDs) > maxSelectedDuplicateIDs {
		return BulkOpResult{}, clientError("mediaIDs exceeds maximum of 200")
	}
	selected := map[store.MediaID]bool{}
	selectedIDs := make([]store.MediaID, 0, len(mediaIDs))
	for _, id := range mediaIDs {
		mediaID := store.MediaID(id)
		if selected[mediaID] {
			continue
		}
		selected[mediaID] = true
		selectedIDs = append(selectedIDs, mediaID)
	}
	clusters, err := h.store.SelectedDuplicateClusters(ctx, store.LibraryID(libraryID), selectedIDs, scope.LibraryFilter())
	if err != nil {
		return BulkOpResult{}, err
	}
	affected := 0
	for _, cluster := range clusters {
		if len(cluster.Members) < 2 {
			continue
		}
		allMembersSelected := true
		for _, member := range cluster.Members {
			if member == cluster.Survivor {
				continue
			}
			if !selected[member] {
				allMembersSelected = false
				break
			}
		}
		if !allMembersSelected {
			continue
		}
		batch, err := h.store.MergeApply(ctx, store.LibraryID(libraryID), cluster.Survivor, scope.LibraryFilter(), time.Now().UTC())
		if err != nil {
			return BulkOpResult{Affected: affected}, err
		}
		if batch.ID != "" {
			if err := h.logActivity(ctx, scope, store.ActivityMediaMerge, "batch", batch.ID, store.LibraryID(libraryID)); err != nil {
				return BulkOpResult{Affected: affected + batch.Affected}, err
			}
		}
		affected += batch.Affected
	}
	if affected > 0 {
		if err := h.refreshHealthCache(ctx); err != nil {
			return BulkOpResult{Affected: affected}, err
		}
	}
	return BulkOpResult{Affected: affected}, nil
}

// bulkTrashSelected trashes the Media IDs in include (which must be in the
// given dimension for libraryID) through the audited Trash path. IDs absent
// from the dimension's live set are silently skipped. Returns the count trashed.
func (h *Handler) bulkTrashSelected(ctx context.Context, scope auth.Scope, libraryID store.LibraryID, dim store.HealthDimension, include map[store.MediaID]bool) (BulkOpResult, error) {
	if h.store == nil {
		return BulkOpResult{}, clientError("store unavailable")
	}
	visible, err := h.visibleLibrary(ctx, scope, libraryID)
	if err != nil {
		return BulkOpResult{}, err
	}
	if !visible {
		return BulkOpResult{}, clientError("unknown library")
	}
	items, err := h.store.LibraryHealthItems(ctx, libraryID, dim, scope.LibraryFilter(), 500)
	if err != nil {
		return BulkOpResult{}, err
	}
	trashed := 0
	for _, item := range items {
		if !include[item.MediaID] {
			continue
		}
		if err := h.store.TrashMedia(ctx, item.MediaID, scope.LibraryFilter()); err != nil {
			return BulkOpResult{Affected: trashed}, err
		}
		if err := h.logActivity(ctx, scope, store.ActivityMediaTrash, "media", string(item.MediaID), item.LibraryID); err != nil {
			return BulkOpResult{Affected: trashed}, err
		}
		trashed++
	}
	if trashed > 0 {
		if err := h.refreshHealthCache(ctx); err != nil {
			return BulkOpResult{Affected: trashed}, err
		}
	}
	return BulkOpResult{Affected: trashed}, nil
}

// bulkFetchMissingArtwork enqueues the global artwork-sweep job, which fills
// in missing poster candidates for every Media across all visible Libraries.
// The sweep is globally deduped (one queued sweep covers the whole catalog),
// so multiple concurrent calls do not produce redundant jobs. Returns the
// count of missing-artwork items in the named Library as Affected so the
// operator sees the local backlog size.
func (h *Handler) bulkFetchMissingArtwork(ctx context.Context, scope auth.Scope, req request) (BulkOpResult, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return BulkOpResult{}, clientError("libraryID is required")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return BulkOpResult{}, err
	}
	if !visible {
		return BulkOpResult{}, clientError("unknown library")
	}
	// Count missing-artwork items for the operator-visible Affected total.
	items, err := h.store.LibraryHealthItems(ctx, store.LibraryID(libraryID), store.HealthMissingArtwork, scope.LibraryFilter(), 500)
	if err != nil {
		return BulkOpResult{}, err
	}
	// Enqueue the globally-deduped artwork sweep (covers the full catalog,
	// including this library's missing-artwork items).
	job, err := artwork.NewSweepJob(time.Now().UTC())
	if err != nil {
		return BulkOpResult{}, err
	}
	if _, err := h.store.EnqueueJob(ctx, job); err != nil {
		return BulkOpResult{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityLibraryRoots, "library", libraryID, store.LibraryID(libraryID)); err != nil {
		return BulkOpResult{}, err
	}
	return BulkOpResult{Affected: len(items)}, nil
}
