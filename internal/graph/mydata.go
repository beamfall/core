package graph

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// My Data / Privacy Panel, graph half. Every field here operates on
// the CALLER's own Profile: the profile id always comes from scope.ProfileID,
// never from a variable. There is deliberately no other-profile parameter on
// any of these fields — a session can only ever see, export, and delete its
// own data. These fields are NEVER manager-gated and never surface in the
// operator Console.

// MyEngagementExportInfo is the self-describing export payload the client saves
// as a download. The schema discriminant lets the client (and any
// future importer) recognize the shape; no server-side file is ever written.
type MyEngagementExportInfo struct {
	Schema                  string                       `json:"schema"`
	ExportedAt              string                       `json:"exportedAt"`
	Complete                bool                         `json:"complete"`
	SealedLibrariesExcluded int                          `json:"sealedLibrariesExcluded"`
	ExclusionMessage        string                       `json:"exclusionMessage,omitempty"`
	ExcludedLibraries       []MyDataExcludedLibraryInfo  `json:"excludedLibraries"`
	Profile                 MyProfileMetaInfo            `json:"profile"`
	PlaybackProgress        []MyPlaybackProgressInfo     `json:"playbackProgress"`
	Favorites               []MyFavoriteInfo             `json:"favorites"`
	Queue                   []MyQueueEntryInfo           `json:"queue"`
	SavedFilters            []MySavedFilterInfo          `json:"savedFilters"`
	Collections             []MyCollectionInfo           `json:"collections"`
	Markers                 []MyMarkerInfo               `json:"markers"`
	Mutes                   []MyMuteInfo                 `json:"mutes"`
	EngagementEvents        []MyEngagementEventEntryInfo `json:"engagementEvents"`
	// Connections is the ADR-0036 "Where your data goes" list: active
	// third-party account links for this Profile (scrobble links and Integration
	// Plugin profile links). It names the service/plugin + an optional account
	// label/ref, NEVER a token (invariant #5: credentials live only in the 0600
	// secure store and are excluded by construction — MyScrobbleConnectionInfo
	// has no field for them).
	Connections []MyScrobbleConnectionInfo `json:"connections"`
}

type MyDataExcludedLibraryInfo struct {
	LibraryID string `json:"libraryID"`
	Reason    string `json:"reason"`
}

// MyScrobbleConnectionInfo is one exported third-party link for the "Where your
// data goes" transparency row. NON-SECRET by construction: it has no
// token/secret field, mirroring the store-side MyScrobbleConnection.
type MyScrobbleConnectionInfo struct {
	Service      string `json:"service"`
	AccountLabel string `json:"accountLabel,omitempty"`
	LinkedAt     string `json:"linkedAt"`
}

// MyEngagementEventEntryInfo is one exported row of the raw engagement event log:
// the plugin-declared kind, the signed
// value, the Media and/or Entity target ids, an optional opaque payload, and the
// event time. Media is identified by id only, mirroring the rest of My Data.
type MyEngagementEventEntryInfo struct {
	Kind       string `json:"kind"`
	MediaID    string `json:"mediaID,omitempty"`
	EntityID   string `json:"entityID,omitempty"`
	Value      int    `json:"value"`
	Payload    string `json:"payload,omitempty"`
	OccurredAt string `json:"occurredAt"`
}

// MyProfileMetaInfo is the exported Profile metadata: display
// name, kind, and the creation date (RFC3339, UTC). CreatedAt is omitted for
// Profiles predating migration 0069, whose stored created_at_ms is 0.
type MyProfileMetaInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// formatOptionalMs renders an epoch-millisecond timestamp as an RFC3339 UTC
// string, or "" when the value is non-positive (0 = the field was never
// recorded — e.g. a Profile predating migration 0069), so the omitempty wire
// tag drops it rather than emitting a 1970 instant.
func formatOptionalMs(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// MySavedFilterInfo is one exported Saved Filter (name + serialized expression).
type MySavedFilterInfo struct {
	Name       string `json:"name"`
	LibraryID  string `json:"libraryID"`
	FilterJSON string `json:"filterJSON"`
	CreatedAt  string `json:"createdAt"`
}

// MyCollectionInfo is one exported Collection the Profile authored (name +
// ordered/membership expression).
type MyCollectionInfo struct {
	Name       string `json:"name"`
	LibraryID  string `json:"libraryID"`
	SourceExpr string `json:"sourceExpr"`
	CreatedAt  string `json:"createdAt"`
}

// MyMarkerInfo is one exported authored Marker (ADR-0036 §2.7: media
// fingerprint + timestamp + kind + tag list).
type MyMarkerInfo struct {
	MediaID      string   `json:"mediaID"`
	Title        string   `json:"title"`
	StartSeconds float64  `json:"startSeconds"`
	EndSeconds   *float64 `json:"endSeconds,omitempty"`
	SegmentKind  string   `json:"segmentKind,omitempty"`
	Tags         []string `json:"tags"`
}

// myEngagementSchema is the export payload's schema discriminant.
const myEngagementSchema = "beamfall.mydata.v1"

// My Data PIN-throttle lanes (ADR-0012 §3 brute-force economics). Both the
// export and the hard-delete confirm verify the reveal PIN, so each gets its
// own rolling-window lockout — separate action lanes keep a delete lockout
// from coupling to the export lane and vice versa. Failures age out of the
// window, so a clean window after backoff (or success) is the reset.
const (
	myDataExportAttemptAction = "mydata.export"
	myDataExportAttemptWindow = 5 * time.Minute
	myDataDeleteAttemptAction = "mydata.delete"
	myDataDeleteAttemptWindow = 5 * time.Minute
)

// myDataExportBackoff is the My-Data export-confirm lane's exponential-backoff
// curve. Mirrors the delete lane's curve.
var myDataExportBackoff = store.AuthBackoff{
	Window:       myDataExportAttemptWindow,
	FreeAttempts: 5,
	BaseDelay:    2 * time.Second,
	MaxDelay:     myDataExportAttemptWindow,
}

// myDataDeleteBackoff is the My-Data delete-confirm lane's exponential-backoff
// curve. It verifies the same reveal PIN as reveal(pin), so it
// gets the same growing-delay lockout — otherwise it is a second, unthrottled
// brute-force lane against the reveal PIN at offline speed. Mirrors the reveal
// lane's curve.
var myDataDeleteBackoff = store.AuthBackoff{
	Window:       myDataDeleteAttemptWindow,
	FreeAttempts: 5,
	BaseDelay:    2 * time.Second,
	MaxDelay:     myDataDeleteAttemptWindow,
}

// MyPlaybackProgressInfo is one exported resume/play-count projection row.
type MyPlaybackProgressInfo struct {
	MediaID         string `json:"mediaID"`
	PositionSeconds int    `json:"positionSeconds"`
	DurationSeconds int    `json:"durationSeconds"`
	Watched         bool   `json:"watched"`
	PlayCount       int    `json:"playCount"`
	UpdatedAt       string `json:"updatedAt"`
}

// MyFavoriteInfo is one exported favorite row.
type MyFavoriteInfo struct {
	MediaID     string `json:"mediaID"`
	FavoritedAt string `json:"favoritedAt"`
}

// MyQueueEntryInfo is one exported play-queue row, in queue order.
type MyQueueEntryInfo struct {
	MediaID string `json:"mediaID"`
	AddedAt string `json:"addedAt"`
}

// myEngagement returns the caller's reveal-filtered engagement export. The
// reveal filter is the caller's CURRENT scope — events referencing restricted
// Media appear only while the session is revealed (the Reveal contract
// outranks export completeness; a revealed session exports everything).
//
// ADR-0117: export is PIN-confirmed (step-up gate). A profile that has a
// reveal PIN must supply a matching one — an absent or wrong PIN is rejected
// and nothing is exported (fail closed). A managed profile still falls through
// on ErrNoRevealPath because it cannot hold recovery material; an adult profile
// without a configured Reveal PIN is rejected instead of exporting ungated.
// Throttled on a separate per-Profile lane from the delete lane.
func (h *Handler) myEngagement(ctx context.Context, scope auth.Scope, req request) (MyEngagementExportInfo, error) {
	// ADR-0117 step-up gate: verify reveal PIN before serving the export.
	now := time.Now().UTC()
	until, err := h.store.AuthBackoffUntil(ctx, myDataExportAttemptAction, string(scope.ProfileID), now, myDataExportBackoff)
	if err != nil {
		return MyEngagementExportInfo{}, err
	}
	if now.Before(until) {
		return MyEngagementExportInfo{}, clientError("reveal PIN attempts throttled")
	}
	verifyErr := h.store.VerifyRevealPIN(ctx, scope.ProfileID, trimmedVariable(req, "pin"))
	switch {
	case verifyErr == nil, errors.Is(verifyErr, store.ErrRevealPINMismatch):
		if recordErr := h.store.RecordAuthAttempt(ctx, store.AuthAttempt{
			OccurredAt: now,
			Action:     myDataExportAttemptAction,
			Key:        string(scope.ProfileID),
			Success:    verifyErr == nil,
		}); recordErr != nil {
			return MyEngagementExportInfo{}, recordErr
		}
	}
	switch {
	case verifyErr == nil:
		// verified
	case errors.Is(verifyErr, store.ErrNoRevealPath):
		profile, err := h.store.Profile(ctx, scope.ProfileID)
		if err != nil {
			return MyEngagementExportInfo{}, err
		}
		if profile.Kind != store.ProfileManaged {
			return MyEngagementExportInfo{}, clientError("reveal PIN must be set before exporting your data")
		}
		// managed profiles do not hold recovery material, so engagement-only export proceeds
	case errors.Is(verifyErr, store.ErrRevealPINMismatch):
		return MyEngagementExportInfo{}, clientError("reveal PIN required to export your data")
	default:
		return MyEngagementExportInfo{}, verifyErr
	}

	filter, err := h.sealedFilter(ctx, scope.LibraryFilter())
	if err != nil {
		return MyEngagementExportInfo{}, err
	}
	export, err := h.store.ExportMyEngagement(ctx, scope.ProfileID, filter)
	if err != nil {
		return MyEngagementExportInfo{}, err
	}
	info := MyEngagementExportInfo{
		Schema:                  myEngagementSchema,
		ExportedAt:              time.Now().UTC().Format(time.RFC3339),
		Complete:                export.Complete,
		SealedLibrariesExcluded: len(export.ExcludedLibraries),
		ExclusionMessage:        myDataExclusionMessage(len(export.ExcludedLibraries)),
		ExcludedLibraries:       make([]MyDataExcludedLibraryInfo, 0, len(export.ExcludedLibraries)),
		Profile: MyProfileMetaInfo{
			ID:        string(export.Profile.ID),
			Name:      export.Profile.Name,
			Kind:      export.Profile.Kind,
			CreatedAt: formatOptionalMs(export.Profile.CreatedAtMs),
		},
		PlaybackProgress: make([]MyPlaybackProgressInfo, 0, len(export.PlaybackProgress)),
		Favorites:        make([]MyFavoriteInfo, 0, len(export.Favorites)),
		Queue:            make([]MyQueueEntryInfo, 0, len(export.Queue)),
		SavedFilters:     make([]MySavedFilterInfo, 0, len(export.SavedFilters)),
		Collections:      make([]MyCollectionInfo, 0, len(export.Collections)),
		Markers:          make([]MyMarkerInfo, 0, len(export.Markers)),
		Mutes:            make([]MyMuteInfo, 0, len(export.Mutes)),
		EngagementEvents: make([]MyEngagementEventEntryInfo, 0, len(export.EngagementEvents)),
		Connections:      make([]MyScrobbleConnectionInfo, 0, len(export.Connections)),
	}
	for _, row := range export.ExcludedLibraries {
		info.ExcludedLibraries = append(info.ExcludedLibraries, MyDataExcludedLibraryInfo{
			LibraryID: string(row.LibraryID),
			Reason:    row.Reason,
		})
	}
	for _, row := range export.PlaybackProgress {
		info.PlaybackProgress = append(info.PlaybackProgress, MyPlaybackProgressInfo{
			MediaID:         string(row.MediaID),
			PositionSeconds: row.PositionSeconds,
			DurationSeconds: row.DurationSeconds,
			Watched:         row.Watched,
			PlayCount:       row.PlayCount,
			UpdatedAt:       row.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	for _, row := range export.Favorites {
		info.Favorites = append(info.Favorites, MyFavoriteInfo{
			MediaID:     string(row.MediaID),
			FavoritedAt: row.FavoritedAt.UTC().Format(time.RFC3339),
		})
	}
	for _, row := range export.Queue {
		info.Queue = append(info.Queue, MyQueueEntryInfo{
			MediaID: string(row.MediaID),
			AddedAt: row.AddedAt.UTC().Format(time.RFC3339),
		})
	}
	for _, row := range export.SavedFilters {
		info.SavedFilters = append(info.SavedFilters, MySavedFilterInfo{
			Name:       row.Name,
			LibraryID:  string(row.LibraryID),
			FilterJSON: row.FilterJSON,
			CreatedAt:  row.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	for _, row := range export.Collections {
		info.Collections = append(info.Collections, MyCollectionInfo{
			Name:       row.Name,
			LibraryID:  string(row.LibraryID),
			SourceExpr: row.SourceExpr,
			CreatedAt:  row.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	for _, row := range export.Markers {
		info.Markers = append(info.Markers, MyMarkerInfo{
			MediaID:      string(row.MediaID),
			Title:        row.Title,
			StartSeconds: row.StartSeconds,
			EndSeconds:   row.EndSeconds,
			SegmentKind:  row.SegmentKind,
			Tags:         row.Tags,
		})
	}
	for _, row := range export.Connections {
		info.Connections = append(info.Connections, MyScrobbleConnectionInfo{
			Service:      row.Service,
			AccountLabel: row.AccountLabel,
			LinkedAt:     row.LinkedAt.UTC().Format(time.RFC3339),
		})
	}
	for _, row := range export.Mutes {
		info.Mutes = append(info.Mutes, muteInfo(row))
	}
	for _, row := range export.EngagementEvents {
		info.EngagementEvents = append(info.EngagementEvents, MyEngagementEventEntryInfo{
			Kind:       row.Kind,
			MediaID:    string(row.MediaID),
			EntityID:   string(row.EntityID),
			Value:      row.Value,
			Payload:    row.Payload,
			OccurredAt: row.OccurredAt.UTC().Format(time.RFC3339),
		})
	}
	return info, nil
}

func myDataExclusionMessage(count int) string {
	if count <= 0 {
		return ""
	}
	if count == 1 {
		return "1 sealed library excluded — unseal to include"
	}
	return strconv.Itoa(count) + " sealed libraries excluded — unseal to include"
}

// My Data Summary + Event log (ADR-0036 §2.2 layers 1 & 2), graph half. Both
// resolvers operate on the CALLER's own Profile: scope.ProfileID + the caller's
// current scope.LibraryFilter() (Reveal). There is no other-profile parameter
// and these are never manager-gated — the same own-data-only posture as
// myEngagement. The aggregates are COMPUTED ON READ in the store; nothing is
// stored (no server-side analytics).

// MyLibraryBreakdownInfo is one per-Library play tally in the Summary.
type MyLibraryBreakdownInfo struct {
	LibraryID string `json:"libraryID"`
	Plays     int    `json:"plays"`
}

// MyDataSummaryInfo is the ADR-0036 §2.2 layer-1 aggregate over the caller's own
// reveal-filtered engagement: total plays, watched plays, favorites, queue
// entries, the per-Library breakdown, and the activity date range (empty when
// there is no engagement).
type MyDataSummaryInfo struct {
	Plays            int                      `json:"plays"`
	Watched          int                      `json:"watched"`
	Favorites        int                      `json:"favorites"`
	QueueEntries     int                      `json:"queueEntries"`
	LibraryCount     int                      `json:"libraryCount"`
	LibraryBreakdown []MyLibraryBreakdownInfo `json:"libraryBreakdown"`
	FirstActivity    string                   `json:"firstActivity,omitempty"`
	LastActivity     string                   `json:"lastActivity,omitempty"`
}

// myDataSummary returns the caller's reveal-filtered engagement Summary
// (ADR-0036 §2.2 layer 1). Own-Profile-only + Reveal are enforced at the store
// boundary by passing scope.ProfileID + scope.LibraryFilter() and nothing else.
func (h *Handler) myDataSummary(ctx context.Context, scope auth.Scope) (MyDataSummaryInfo, error) {
	summary, err := h.store.MyDataSummary(ctx, scope.ProfileID, scope.LibraryFilter())
	if err != nil {
		return MyDataSummaryInfo{}, err
	}
	info := MyDataSummaryInfo{
		Plays:            summary.Plays,
		Watched:          summary.Watched,
		Favorites:        summary.Favorites,
		QueueEntries:     summary.QueueEntries,
		LibraryCount:     summary.LibraryCount,
		LibraryBreakdown: make([]MyLibraryBreakdownInfo, 0, len(summary.LibraryBreakdown)),
		FirstActivity:    formatOptionalTime(summary.FirstActivity),
		LastActivity:     formatOptionalTime(summary.LastActivity),
	}
	for _, row := range summary.LibraryBreakdown {
		info.LibraryBreakdown = append(info.LibraryBreakdown, MyLibraryBreakdownInfo{
			LibraryID: string(row.LibraryID),
			Plays:     row.Plays,
		})
	}
	return info, nil
}

// MyEngagementEventInfo is one row of the event log (ADR-0036 §2.2 layer 2):
// the event kind, the owning Library, and the event time. Media is identified
// by id only — no title, mirroring the rest of My Data.
type MyEngagementEventInfo struct {
	Kind       string `json:"kind"`
	MediaID    string `json:"mediaID"`
	LibraryID  string `json:"libraryID"`
	OccurredAt string `json:"occurredAt"`
}

// MyEngagementLogInfo is one page of the caller's event log plus the total
// count of events matching the filter across all pages (for pagination).
type MyEngagementLogInfo struct {
	Events []MyEngagementEventInfo `json:"events"`
	Total  int                     `json:"total"`
}

// myEngagementLog returns one paginated, Library/date-filtered page of the
// caller's reveal-filtered engagement event log (ADR-0036 §2.2 layer 2).
// Own-Profile-only + Reveal are enforced at the store boundary (scope.ProfileID
// + scope.LibraryFilter()); the Library/date filters narrow only the already-
// visible set, so naming a hidden Library can never widen the page. `from`/`to`
// are optional RFC3339 instants (inclusive); a malformed value is a client
// error. `limit`/`offset` page the result.
func (h *Handler) myEngagementLog(ctx context.Context, scope auth.Scope, req request) (MyEngagementLogInfo, error) {
	query := store.MyEngagementLogQuery{
		LibraryID: store.LibraryID(trimmedVariable(req, "libraryID")),
		Limit:     boundedIntVar(req, "limit", 50, 500),
		Offset:    nonNegativeIntVar(req, "offset"),
	}
	from, err := parseMyDataInstant(req, "from")
	if err != nil {
		return MyEngagementLogInfo{}, err
	}
	to, err := parseMyDataInstant(req, "to")
	if err != nil {
		return MyEngagementLogInfo{}, err
	}
	query.From = from
	query.To = to

	page, err := h.store.MyEngagementLog(ctx, scope.ProfileID, scope.LibraryFilter(), query)
	if err != nil {
		return MyEngagementLogInfo{}, err
	}
	info := MyEngagementLogInfo{
		Events: make([]MyEngagementEventInfo, 0, len(page.Events)),
		Total:  page.Total,
	}
	for _, ev := range page.Events {
		info.Events = append(info.Events, MyEngagementEventInfo{
			Kind:       string(ev.Kind),
			MediaID:    string(ev.MediaID),
			LibraryID:  string(ev.LibraryID),
			OccurredAt: ev.OccurredAt.UTC().Format(time.RFC3339),
		})
	}
	return info, nil
}

// parseMyDataInstant reads an optional RFC3339 date/time variable for the event
// log filter. Absent or blank yields the zero time (no bound); a present but
// unparseable value is a stable client error rather than a silent no-filter.
func parseMyDataInstant(req request, name string) (time.Time, error) {
	raw := trimmedVariable(req, name)
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, clientError(name + " must be an RFC3339 date-time")
	}
	return parsed, nil
}

// MyDataDeleteResult echoes the resolved delete scope kind after the hard
// delete. It carries the scope kind only — never the media id, mirroring the
// activity entry: the response must not memorialize what was deleted either.
type MyDataDeleteResult struct {
	Scope                   string                      `json:"scope"`
	Complete                bool                        `json:"complete"`
	SealedLibrariesExcluded int                         `json:"sealedLibrariesExcluded"`
	ExclusionMessage        string                      `json:"exclusionMessage,omitempty"`
	ExcludedLibraries       []MyDataExcludedLibraryInfo `json:"excludedLibraries"`
}

// deleteMyEngagement hard-DELETEs the caller's own adult-Profile engagement
// data. Managed Profiles have no self-service My Data delete path;
// their guardian or the operator acts through operatorDeleteProfileData.
// The `scope` variable is a closed vocabulary: "all" or "media:<id>".
//
//   - "all" removes every engagement row for the Profile, including rows that
//     reference currently-hidden Media — deleting your own data must not
//     require revealing first.
//   - "media:<id>" resolves the id through the caller's visibility filter FIRST
//     (in the store), so a hidden id fails identically to an unknown one;
//     delete-by-id is otherwise an existence oracle for hidden Media.
//
// On success it appends exactly one activity entry recording the SCOPE KIND
// only ("all" or "media") and never the media id.
func (h *Handler) deleteMyEngagement(ctx context.Context, scope auth.Scope, req request) (MyDataDeleteResult, error) {
	raw := trimmedVariable(req, "scope")
	if raw == "" {
		return MyDataDeleteResult{}, clientError("scope is required")
	}
	deleteScope, mediaID, err := parseMyDataDeleteScope(raw)
	if err != nil {
		return MyDataDeleteResult{}, err
	}
	profile, err := h.store.Profile(ctx, scope.ProfileID)
	if err != nil {
		return MyDataDeleteResult{}, err
	}
	if profile.Kind == store.ProfileManaged {
		return MyDataDeleteResult{}, clientError("managed profiles require guardian or operator data delete")
	}
	// ADR-0036 §2.2: the hard delete is PIN-confirmed. Verify the caller's
	// reveal PIN BEFORE any deletion — fail closed, so a profile that has a PIN
	// cannot delete without supplying a matching one (an empty/absent pin
	// verifies as a mismatch). An adult who never set a reveal PIN has nothing
	// to confirm against and falls through. Closing that gap needs a general
	// (non-reveal) profile PIN, which does not exist yet; ADR-0036 §2.2 says
	// "PIN-confirmed" without specifying which PIN gates a profile that has
	// none — a product decision, not a code one.
	// Throttle the PIN-verifying lane: without this, the confirm
	// is an unbounded, offline-speed brute-force surface against the reveal PIN.
	// Reuse the auth-attempt window/lockout used by reveal(pin), on a dedicated
	// per-Profile lane (see myDataDeleteAttempt* constants).
	now := time.Now().UTC()
	until, err := h.store.AuthBackoffUntil(ctx, myDataDeleteAttemptAction, string(scope.ProfileID), now, myDataDeleteBackoff)
	if err != nil {
		return MyDataDeleteResult{}, err
	}
	if now.Before(until) {
		return MyDataDeleteResult{}, clientError("reveal PIN attempts throttled")
	}
	verifyErr := h.store.VerifyRevealPIN(ctx, scope.ProfileID, trimmedVariable(req, "pin"))
	// Record an attempt only when there was a real PIN to verify against: a
	// match (success) or a mismatch (failure). ErrNoRevealPath means the Profile
	// has no PIN to brute-force, so it neither counts nor records — it would
	// otherwise pollute the lane for a non-existent secret.
	switch {
	case verifyErr == nil, errors.Is(verifyErr, store.ErrRevealPINMismatch):
		if recordErr := h.store.RecordAuthAttempt(ctx, store.AuthAttempt{
			OccurredAt: now,
			Action:     myDataDeleteAttemptAction,
			Key:        string(scope.ProfileID),
			Success:    verifyErr == nil,
		}); recordErr != nil {
			return MyDataDeleteResult{}, recordErr
		}
	}
	switch {
	case verifyErr == nil, errors.Is(verifyErr, store.ErrNoRevealPath):
		// verified, or no reveal PIN exists to confirm against
	case errors.Is(verifyErr, store.ErrRevealPINMismatch):
		return MyDataDeleteResult{}, clientError("reveal PIN required to delete your data")
	default:
		return MyDataDeleteResult{}, verifyErr
	}
	filter, err := h.sealedFilter(ctx, scope.LibraryFilter())
	if err != nil {
		return MyDataDeleteResult{}, err
	}
	// Record the scope KIND only — never the media id. The activity log must
	// not memorialize what the user deleted; that would defeat the deletion.
	// No LibraryID: this is the user's own act on their own data, not a
	// content reference, so it stays visible across reveal state.
	if err := h.store.DeleteMyEngagementAudited(ctx, scope.ProfileID, deleteScope, mediaID, filter, store.ActivityEntry{
		OccurredAt: time.Now().UTC(),
		Action:     store.ActivityMyDataDelete,
		SessionID:  scope.SessionID,
		DeviceID:   scope.DeviceID,
		ProfileID:  scope.ProfileID,
		TargetType: string(deleteScope),
	}); err != nil {
		return MyDataDeleteResult{}, err
	}
	excluded := sealedLibraryInfos(filter.SealedIDs)
	return MyDataDeleteResult{
		Scope:                   string(deleteScope),
		Complete:                len(excluded) == 0,
		SealedLibrariesExcluded: len(excluded),
		ExclusionMessage:        myDataExclusionMessage(len(excluded)),
		ExcludedLibraries:       excluded,
	}, nil
}

func sealedLibraryInfos(ids []store.LibraryID) []MyDataExcludedLibraryInfo {
	if len(ids) == 0 {
		return nil
	}
	sorted := append([]store.LibraryID(nil), ids...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	out := make([]MyDataExcludedLibraryInfo, 0, len(sorted))
	for _, id := range sorted {
		out = append(out, MyDataExcludedLibraryInfo{
			LibraryID: string(id),
			Reason:    "sealed library excluded; unseal to include",
		})
	}
	return out
}

// parseMyDataDeleteScope parses the closed delete-scope vocabulary into a store
// scope and (for "media:<id>") the target id. An unknown or malformed value is
// a client error with a stable message.
func parseMyDataDeleteScope(raw string) (store.MyDataDeleteScope, store.MediaID, error) {
	if raw == "all" {
		return store.MyDataDeleteAll, "", nil
	}
	if id, ok := strings.CutPrefix(raw, "media:"); ok {
		id = strings.TrimSpace(id)
		if id == "" {
			return "", "", clientError("media id is required")
		}
		return store.MyDataDeleteMedia, store.MediaID(id), nil
	}
	return "", "", clientError("unknown delete scope")
}

// OperatorDeleteProfileDataResult echoes the target Profile id after a Console
// operator-delete. Unlike the self-delete result, the
// operator path DOES name the target Profile: this is an operator action with
// an operator-visible audit trail, not the subject's own privacy view.
type OperatorDeleteProfileDataResult struct {
	ProfileID string `json:"profileID"`
}

// operatorDeleteProfileData is the Console operator-delete and
// the managed-Profile guardian path (§2.6). It hard-DELETEs a TARGET Profile's
// engagement footprint on an operator's behalf, reusing the exact MYDATA-1
// footprint-delete semantics (DeleteProfileFootprint == DeleteMyEngagement(all)
// with the target parameterized): same recoverability posture, same
// disown-shared-contributions rule.
//
// GATING (load-bearing — an un-admin-gated operator-delete is a security hole):
// the dispatch in graph.go requires scope.Admin (the Admin Session, ADR-0007 §4)
// BEFORE this runs, exactly like every other Console mutation. This is NOT a
// self-service path; the self-service path is deleteMyEngagement, which is
// own-Profile-only and never admin-gated.
//
// GUARDIAN PATH (§2.6): the schema has no stored supervisor→managed link (the
// `profile` table is id/name/kind only — see docs/agent-memory/questions.md),
// so the guardian cannot be gated on a real relationship link. Per §2.6 ("Their
// guardian ... or the server operator acts through the Console"), both the
// operator and the guardian act through this same Admin-Session-gated Console
// mutation; the guardian-link gate is recorded as a schema gap, not invented.
func (h *Handler) operatorDeleteProfileData(ctx context.Context, scope auth.Scope, req request) (OperatorDeleteProfileDataResult, error) {
	target := trimmedVariable(req, "profileID")
	if target == "" {
		return OperatorDeleteProfileDataResult{}, clientError("profileID is required")
	}
	// Operator audit trail: who (scope.ProfileID, via logActivity) deleted whose
	// data (target Profile id in TargetID). Unlike the self-delete activity, this
	// names the target — it is GDPR/offboarding evidence, not the subject's view.
	if err := h.store.DeleteProfileFootprintAudited(ctx, store.ProfileID(target), store.ActivityEntry{
		OccurredAt: time.Now().UTC(),
		Action:     store.ActivityOperatorDataDelete,
		SessionID:  scope.SessionID,
		DeviceID:   scope.DeviceID,
		ProfileID:  scope.ProfileID,
		TargetType: "profile",
		TargetID:   target,
		TokenID:    scope.APITokenID,
		APIKeyID:   scope.APIKeyID,
	}); err != nil {
		return OperatorDeleteProfileDataResult{}, err
	}
	return OperatorDeleteProfileDataResult{ProfileID: target}, nil
}

// MySessionInfo is one of the caller's active Sessions (read-only here;
// revocation stays on the existing session surfaces — ADR-0036).
type MySessionInfo struct {
	ID         string `json:"id"`
	DeviceID   string `json:"deviceID"`
	DeviceName string `json:"deviceName"`
	DeviceRole string `json:"deviceRole"`
	Current    bool   `json:"current"`
	ExpiresAt  string `json:"expiresAt,omitempty"`
}

// MyStreamTokenInfo is one of the caller's outstanding Stream Tokens. It
// carries the target KIND and expiry ONLY — never the token value, and never
// any identifying field for the referenced Media. A token for hidden Media has
// the SAME shape as one for visible Media (target kind "media", no title): the
// only observable difference is whether it is counted, never what it points at.
type MyStreamTokenInfo struct {
	TargetKind string `json:"targetKind"`
	ExpiresAt  string `json:"expiresAt"`
}

// MySessionsInfo is the read-only My Data view of the caller's active Sessions
// and outstanding Stream Tokens, including Pack-subject Offline Pack tokens
// (ADR-0036 + ADR-0098 area 6).
type MySessionsInfo struct {
	Sessions     []MySessionInfo     `json:"sessions"`
	StreamTokens []MyStreamTokenInfo `json:"streamTokens"`
	// PackTokens is the caller's outstanding Pack-subject Stream Tokens. Like
	// StreamTokens they are shape-identical for restricted and visible packs
	// (target kind + expiry only, no pack-identifying field), so a relocked
	// session cannot learn which restricted pack a live token references.
	PackTokens []MyStreamTokenInfo `json:"packTokens"`
}

// mySessions returns the caller's active Sessions and outstanding Stream
// Tokens. Both are the caller's own Profile's only. Stream-token rows are
// shape-identical whether the referenced Media is visible or hidden — the
// store never attaches a title or any media-identifying field, so a relocked
// session cannot read which hidden Media its live tokens point at.
func (h *Handler) mySessions(ctx context.Context, scope auth.Scope) (MySessionsInfo, error) {
	sessions, err := h.store.MyProfileSessions(ctx, scope.ProfileID)
	if err != nil {
		return MySessionsInfo{}, err
	}
	tokens, err := h.store.MyStreamTokensVisible(ctx, scope.ProfileID, scope.Revealed, time.Now().UTC())
	if err != nil {
		return MySessionsInfo{}, err
	}
	packTokens, err := h.store.MyPackTokensVisible(ctx, scope.ProfileID, scope.Revealed, time.Now().UTC())
	if err != nil {
		return MySessionsInfo{}, err
	}
	info := MySessionsInfo{
		Sessions:     make([]MySessionInfo, 0, len(sessions)),
		StreamTokens: make([]MyStreamTokenInfo, 0, len(tokens)),
		PackTokens:   make([]MyStreamTokenInfo, 0, len(packTokens)),
	}
	for _, s := range sessions {
		info.Sessions = append(info.Sessions, MySessionInfo{
			ID:         string(s.ID),
			DeviceID:   string(s.DeviceID),
			DeviceName: s.DeviceName,
			DeviceRole: string(s.DeviceRole),
			Current:    s.ID == scope.SessionID,
			ExpiresAt:  formatOptionalTime(s.ExpiresAt),
		})
	}
	for _, t := range tokens {
		info.StreamTokens = append(info.StreamTokens, MyStreamTokenInfo{
			TargetKind: t.TargetKind,
			ExpiresAt:  t.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
	for _, t := range packTokens {
		info.PackTokens = append(info.PackTokens, MyStreamTokenInfo{
			TargetKind: t.TargetKind,
			ExpiresAt:  t.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
	return info, nil
}
