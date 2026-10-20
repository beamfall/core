package graph

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// deleteMyEngagement is the ADR-0036 My Data hard-delete of the caller's OWN
// engagement data. These wire tests boot the real seeded store + HTTP handler
// and exercise the field over GraphQL. Engagement is seeded for the calling
// session's Profile through the same public mutations a client would use
// (reportPlayback / setFavorite / queueAdd), then deleted and read back via
// myEngagement.
//
// Seeded session model (internal/store/sqlite.go developmentSeed):
//   - session-default  : viewer  device-tv  profile-default (visible media only)
//   - session-revealed : viewer  device-tv  profile-default, Revealed (sees archive)
//   - session-manager  : manager device-mac profile-default
//   - session-managed  : viewer  device-tv  profile-managed (library-local only)
//
// session-default and session-revealed share profile-default, so engagement
// seeded as one is deleted/read by the other — the delete is profile-scoped,
// not session-scoped.

// mydataSeedEngagement seeds playback progress, a favorite, and a queue entry
// for media-alpha on the authorizing session's Profile, asserting no errors.
// Prefixed (mydata*) to avoid colliding with sibling-file helpers.
func mydataSeedEngagement(t *testing.T, handler *Handler, authorize func(*http.Request)) {
	t.Helper()
	if _, errs := post(t, handler,
		`{"query":"mutation { reportPlayback(mediaID: \"media-alpha\", positionSeconds: 30, durationSeconds: 120) { mediaID } }","variables":{"mediaID":"media-alpha","positionSeconds":30,"durationSeconds":120}}`,
		authorize); len(errs) != 0 {
		t.Fatalf("seed reportPlayback errors = %#v", errs)
	}
	if _, errs := post(t, handler,
		`{"query":"mutation { setFavorite(mediaID: \"media-alpha\", favorite: true) { id favorited } }","variables":{"mediaID":"media-alpha","favorite":true}}`,
		authorize); len(errs) != 0 {
		t.Fatalf("seed setFavorite errors = %#v", errs)
	}
	if _, errs := post(t, handler,
		`{"query":"mutation { queueAdd(mediaID: \"media-alpha\") { id } }","variables":{"mediaID":"media-alpha"}}`,
		authorize); len(errs) != 0 {
		t.Fatalf("seed queueAdd errors = %#v", errs)
	}
}

// mydataReadEngagement returns the caller's reveal-filtered engagement export.
// Passes the dev seed PIN "1234"; managed profiles (no PIN set) fall through on ErrNoRevealPath.
func mydataReadEngagement(t *testing.T, handler *Handler, authorize func(*http.Request)) MyEngagementExportInfo {
	t.Helper()
	data, errs := post(t, handler,
		`{"query":"query Q($pin: String){ myEngagement(pin: $pin){ schema playbackProgress { mediaID } favorites { mediaID } queue { mediaID } } }","variables":{"pin":"1234"}}`,
		authorize)
	if len(errs) != 0 {
		t.Fatalf("myEngagement errors = %#v", errs)
	}
	var export MyEngagementExportInfo
	mustUnmarshal(t, data["myEngagement"], &export)
	return export
}

// TestMyDataSummaryAggregatesOwnVisibleData asserts ADR-0036 §2.2 layer 1: the
// Summary aggregates the caller's own reveal-filtered engagement, computed on
// read. media-alpha (visible) is seeded for profile-default with a play +
// favorite + queue add; the relocked viewer's Summary reports plays=1,
// favorites=1, queueEntries=1 over a single visible Library, and never the
// restricted media-archive (seeded via the revealed session on the same
// Profile) — that play is hidden from the relocked Summary.
func TestMyDataSummaryAggregatesOwnVisibleData(t *testing.T) {
	// feature:my-data-panel
	handler := newTestHandler(t)
	mydataSeedEngagement(t, handler, authorizeViewer)
	// A restricted-Media play under the same Profile (revealed session required).
	if _, errs := post(t, handler,
		`{"query":"mutation { reportPlayback(mediaID: \"media-archive\", positionSeconds: 20, durationSeconds: 90) { mediaID } }","variables":{"mediaID":"media-archive","positionSeconds":20,"durationSeconds":90}}`,
		authorizeRevealed); len(errs) != 0 {
		t.Fatalf("seed archive progress errors = %#v", errs)
	}

	const q = `{"query":"query { myDataSummary { plays watched favorites queueEntries libraryCount libraryBreakdown { libraryID plays } firstActivity lastActivity } }"}`

	// Relocked viewer: archive play is hidden — plays=1, one Library.
	data, errs := post(t, handler, q, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("myDataSummary(relocked) errors = %#v", errs)
	}
	var relocked MyDataSummaryInfo
	mustUnmarshal(t, data["myDataSummary"], &relocked)
	if relocked.Plays != 1 || relocked.Favorites != 1 || relocked.QueueEntries != 1 {
		t.Fatalf("relocked summary = %+v, want plays=1 fav=1 queue=1", relocked)
	}
	if relocked.LibraryCount != 1 {
		t.Fatalf("relocked summary libraryCount = %d, want 1 (archive hidden)", relocked.LibraryCount)
	}

	// Revealed session on the same Profile: the archive play now counts (plays=2,
	// two Libraries) — the Summary is the caller's CURRENT reveal-filtered view.
	data, errs = post(t, handler, q, authorizeRevealed)
	if len(errs) != 0 {
		t.Fatalf("myDataSummary(revealed) errors = %#v", errs)
	}
	var revealed MyDataSummaryInfo
	mustUnmarshal(t, data["myDataSummary"], &revealed)
	if revealed.Plays != 2 || revealed.LibraryCount != 2 {
		t.Fatalf("revealed summary = %+v, want plays=2 libraryCount=2", revealed)
	}
}

// TestMyEngagementLogPaginatesFiltersAndRevealFilters asserts ADR-0036 §2.2
// layer 2: the event log is paginated, filterable by Library, and Reveal-
// filtered. profile-default gets media-alpha (visible) play+fav+queue and a
// media-archive (restricted) play. The relocked viewer's log shows only the 3
// local events; pagination splits them; a Library filter narrows them; naming
// the hidden Library yields nothing.
func TestMyEngagementLogPaginatesFiltersAndRevealFilters(t *testing.T) {
	// feature:my-data-panel
	handler := newTestHandler(t)
	mydataSeedEngagement(t, handler, authorizeViewer)
	if _, errs := post(t, handler,
		`{"query":"mutation { reportPlayback(mediaID: \"media-archive\", positionSeconds: 20, durationSeconds: 90) { mediaID } }","variables":{"mediaID":"media-archive","positionSeconds":20,"durationSeconds":90}}`,
		authorizeRevealed); len(errs) != 0 {
		t.Fatalf("seed archive progress errors = %#v", errs)
	}

	logQuery := func(t *testing.T, vars string, authorize func(*http.Request)) MyEngagementLogInfo {
		t.Helper()
		data, errs := post(t, handler,
			`{"query":"query Q($libraryID: String, $from: String, $to: String, $limit: Int, $offset: Int){ myEngagementLog(libraryID: $libraryID, from: $from, to: $to, limit: $limit, offset: $offset){ total events { kind mediaID libraryID occurredAt } } }","variables":`+vars+`}`,
			authorize)
		if len(errs) != 0 {
			t.Fatalf("myEngagementLog errors = %#v", errs)
		}
		var log MyEngagementLogInfo
		mustUnmarshal(t, data["myEngagementLog"], &log)
		return log
	}

	// Relocked: only the 3 local events; archive play hidden.
	all := logQuery(t, `{}`, authorizeViewer)
	if all.Total != 3 || len(all.Events) != 3 {
		t.Fatalf("relocked log = total %d / %d events, want 3/3", all.Total, len(all.Events))
	}
	for _, ev := range all.Events {
		if ev.MediaID == "media-archive" {
			t.Fatalf("relocked log leaked restricted media-archive: %+v", ev)
		}
	}

	// Revealed: 4 events (3 local + 1 archive play).
	revealed := logQuery(t, `{}`, authorizeRevealed)
	if revealed.Total != 4 {
		t.Fatalf("revealed log total = %d, want 4", revealed.Total)
	}

	// Pagination: limit 2 + offset 2 over the revealed 4-event log.
	page1 := logQuery(t, `{"limit":2,"offset":0}`, authorizeRevealed)
	page2 := logQuery(t, `{"limit":2,"offset":2}`, authorizeRevealed)
	if page1.Total != 4 || len(page1.Events) != 2 || len(page2.Events) != 2 {
		t.Fatalf("pagination = total %d, page1 %d, page2 %d; want 4/2/2", page1.Total, len(page1.Events), len(page2.Events))
	}

	// Library filter: only library-local events (3).
	local := logQuery(t, `{"libraryID":"library-local"}`, authorizeRevealed)
	if local.Total != 3 {
		t.Fatalf("library-local log total = %d, want 3", local.Total)
	}
	for _, ev := range local.Events {
		if ev.LibraryID != "library-local" {
			t.Fatalf("library filter leaked %s", ev.LibraryID)
		}
	}

	// Naming the hidden Library from a relocked session yields nothing.
	hidden := logQuery(t, `{"libraryID":"library-archive"}`, authorizeViewer)
	if hidden.Total != 0 {
		t.Fatalf("relocked naming hidden Library saw %d events, want 0", hidden.Total)
	}

	// A malformed date is a client error, not a silent no-filter.
	if _, errs := post(t, handler,
		`{"query":"query Q($from: String){ myEngagementLog(from: $from){ total } }","variables":{"from":"not-a-date"}}`,
		authorizeRevealed); len(errs) == 0 {
		t.Fatal("malformed from date was accepted — should be a client error")
	}
}

// TestDeleteMyEngagementAllRemovesOwnDataAndIsIdempotent asserts the effect of
// a scope:"all" delete by reading engagement back to empty, and confirms a
// second delete is a successful no-op (idempotent destructive op).
func TestDeleteMyEngagementAllRemovesOwnData(t *testing.T) {
	// feature:my-data-panel
	handler := newTestHandler(t)
	mydataSeedEngagement(t, handler, authorizeViewer)

	// Precondition: the rows exist for the caller's Profile.
	before := mydataReadEngagement(t, handler, authorizeViewer)
	if len(before.PlaybackProgress) != 1 || len(before.Favorites) != 1 || len(before.Queue) != 1 {
		t.Fatalf("pre-delete export = %#v, want one row in each table", before)
	}

	// Delete everything; the result echoes the scope KIND only ("all").
	data, errs := post(t, handler,
		`{"query":"mutation D($scope: String!, $pin: String){ deleteMyEngagement(scope: $scope, pin: $pin){ scope } }","variables":{"scope":"all","pin":"1234"}}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("deleteMyEngagement(all) errors = %#v", errs)
	}
	var result MyDataDeleteResult
	mustUnmarshal(t, data["deleteMyEngagement"], &result)
	if result.Scope != "all" {
		t.Fatalf("delete result scope = %q, want all", result.Scope)
	}

	// Effect: read back — every table is empty for the Profile.
	after := mydataReadEngagement(t, handler, authorizeViewer)
	if len(after.PlaybackProgress) != 0 || len(after.Favorites) != 0 || len(after.Queue) != 0 {
		t.Fatalf("post-delete export = %#v, want all tables empty", after)
	}

	// Idempotency: a second delete succeeds with no error and is a no-op.
	data, errs = post(t, handler,
		`{"query":"mutation D($scope: String!, $pin: String){ deleteMyEngagement(scope: $scope, pin: $pin){ scope } }","variables":{"scope":"all","pin":"1234"}}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("idempotent re-delete errors = %#v", errs)
	}
	mustUnmarshal(t, data["deleteMyEngagement"], &result)
	if result.Scope != "all" {
		t.Fatalf("idempotent delete scope = %q, want all", result.Scope)
	}
	stillEmpty := mydataReadEngagement(t, handler, authorizeViewer)
	if len(stillEmpty.PlaybackProgress) != 0 || len(stillEmpty.Favorites) != 0 || len(stillEmpty.Queue) != 0 {
		t.Fatalf("re-delete export = %#v, want still empty", stillEmpty)
	}
}

func TestMyDataReportsSealedLibraryExclusions(t *testing.T) {
	// feature:my-data-panel feature:encrypted-library
	handler := newTestHandler(t)
	mydataSeedEngagement(t, handler, authorizeViewer)
	handler.SetLibEncrypt(stubLibEncrypt{sealed: map[string]bool{"library-local": true}})

	data, errs := post(t, handler,
		`{"query":"query Q($pin: String){ myEngagement(pin: $pin){ complete sealedLibrariesExcluded exclusionMessage excludedLibraries { libraryID reason } playbackProgress { mediaID } } }","variables":{"pin":"1234"}}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("myEngagement sealed errors = %#v", errs)
	}
	var export MyEngagementExportInfo
	mustUnmarshal(t, data["myEngagement"], &export)
	if export.Complete || export.SealedLibrariesExcluded != 1 {
		t.Fatalf("sealed export report = complete %v excluded %d, want incomplete/1", export.Complete, export.SealedLibrariesExcluded)
	}
	if export.ExclusionMessage != "1 sealed library excluded — unseal to include" {
		t.Fatalf("sealed export message = %q", export.ExclusionMessage)
	}
	if len(export.ExcludedLibraries) != 1 || export.ExcludedLibraries[0].LibraryID != "library-local" {
		t.Fatalf("sealed export exclusions = %#v, want library-local", export.ExcludedLibraries)
	}
	if len(export.PlaybackProgress) != 0 {
		t.Fatalf("sealed export leaked playback rows: %#v", export.PlaybackProgress)
	}

	data, errs = post(t, handler,
		`{"query":"mutation D($scope: String!, $pin: String){ deleteMyEngagement(scope: $scope, pin: $pin){ scope complete sealedLibrariesExcluded exclusionMessage excludedLibraries { libraryID reason } } }","variables":{"scope":"all","pin":"1234"}}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("deleteMyEngagement sealed errors = %#v", errs)
	}
	var result MyDataDeleteResult
	mustUnmarshal(t, data["deleteMyEngagement"], &result)
	if result.Scope != "all" || result.Complete || result.SealedLibrariesExcluded != 1 {
		t.Fatalf("sealed delete result = %+v, want scope all incomplete excluded=1", result)
	}
	if result.ExclusionMessage != "1 sealed library excluded — unseal to include" {
		t.Fatalf("sealed delete message = %q", result.ExclusionMessage)
	}
	if len(result.ExcludedLibraries) != 1 || result.ExcludedLibraries[0].LibraryID != "library-local" {
		t.Fatalf("sealed delete exclusions = %#v, want library-local", result.ExcludedLibraries)
	}
}

// TestDeleteMyEngagementMediaRemovesOnlyThatItem asserts a scope:"media:<id>"
// delete removes exactly the named item across all three engagement tables and
// leaves the caller's other engagement intact. media-alpha and media-archive
// are both seeded via the revealed session (which shares profile-default), then
// only media-alpha is deleted; the revealed export still shows media-archive.
func TestDeleteMyEngagementMediaRemovesOnlyThatItem(t *testing.T) {
	// feature:my-data-panel
	handler := newTestHandler(t)
	// Seed both a visible and a restricted item under profile-default; the
	// restricted seed requires the revealed session (the relocked viewer is
	// rejected for media-archive, per the queue/favorite reveal tests).
	mydataSeedEngagement(t, handler, authorizeRevealed)
	if _, errs := post(t, handler,
		`{"query":"mutation { reportPlayback(mediaID: \"media-archive\", positionSeconds: 20, durationSeconds: 90) { mediaID } }","variables":{"mediaID":"media-archive","positionSeconds":20,"durationSeconds":90}}`,
		authorizeRevealed); len(errs) != 0 {
		t.Fatalf("seed archive progress errors = %#v", errs)
	}
	if _, errs := post(t, handler,
		`{"query":"mutation { setFavorite(mediaID: \"media-archive\", favorite: true) { favorited } }","variables":{"mediaID":"media-archive","favorite":true}}`,
		authorizeRevealed); len(errs) != 0 {
		t.Fatalf("seed archive favorite errors = %#v", errs)
	}
	if _, errs := post(t, handler,
		`{"query":"mutation { queueAdd(mediaID: \"media-archive\") { id } }","variables":{"mediaID":"media-archive"}}`,
		authorizeRevealed); len(errs) != 0 {
		t.Fatalf("seed archive queue errors = %#v", errs)
	}

	// Delete only media-alpha; reveal not required to delete one's own data, but
	// the visible item is deletable from any session on the Profile.
	data, errs := post(t, handler,
		`{"query":"mutation D($scope: String!, $pin: String){ deleteMyEngagement(scope: $scope, pin: $pin){ scope } }","variables":{"scope":"media:media-alpha","pin":"1234"}}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("deleteMyEngagement(media:media-alpha) errors = %#v", errs)
	}
	var result MyDataDeleteResult
	mustUnmarshal(t, data["deleteMyEngagement"], &result)
	if result.Scope != "media" {
		t.Fatalf("delete result scope = %q, want media (kind only, never the id)", result.Scope)
	}

	// Effect + isolation across tables: media-alpha gone everywhere, media-archive
	// survives. Read with the revealed session so the restricted survivor shows.
	export := mydataReadEngagement(t, handler, authorizeRevealed)
	for _, row := range export.PlaybackProgress {
		if row.MediaID == "media-alpha" {
			t.Fatalf("media-alpha progress survived media delete: %#v", export.PlaybackProgress)
		}
	}
	for _, row := range export.Favorites {
		if row.MediaID == "media-alpha" {
			t.Fatalf("media-alpha favorite survived media delete: %#v", export.Favorites)
		}
	}
	for _, row := range export.Queue {
		if row.MediaID == "media-alpha" {
			t.Fatalf("media-alpha queue entry survived media delete: %#v", export.Queue)
		}
	}
	if len(export.PlaybackProgress) != 1 || export.PlaybackProgress[0].MediaID != "media-archive" {
		t.Fatalf("after media delete, progress = %#v, want only media-archive", export.PlaybackProgress)
	}
	if len(export.Favorites) != 1 || export.Favorites[0].MediaID != "media-archive" {
		t.Fatalf("after media delete, favorites = %#v, want only media-archive", export.Favorites)
	}
	if len(export.Queue) != 1 || export.Queue[0].MediaID != "media-archive" {
		t.Fatalf("after media delete, queue = %#v, want only media-archive", export.Queue)
	}
}

// TestDeleteMyEngagementHiddenMediaFailsLikeUnknown asserts the Reveal contract:
// deleting a relocked session's restricted-Media engagement by id fails
// identically to deleting an unknown id (no existence oracle), AND the hidden
// row survives the failed delete. media-archive engagement is seeded via the
// revealed session (same profile-default), then a delete is attempted from the
// relocked viewer session.
func TestDeleteMyEngagementHiddenMediaFailsLikeUnknown(t *testing.T) {
	// feature:my-data-panel
	handler := newTestHandler(t)
	if _, errs := post(t, handler,
		`{"query":"mutation { reportPlayback(mediaID: \"media-archive\", positionSeconds: 20, durationSeconds: 90) { mediaID } }","variables":{"mediaID":"media-archive","positionSeconds":20,"durationSeconds":90}}`,
		authorizeRevealed); len(errs) != 0 {
		t.Fatalf("seed archive progress errors = %#v", errs)
	}

	// Relocked viewer deleting the hidden id must fail.
	_, hiddenErrs := post(t, handler,
		`{"query":"mutation D($scope: String!, $pin: String){ deleteMyEngagement(scope: $scope, pin: $pin){ scope } }","variables":{"scope":"media:media-archive","pin":"1234"}}`,
		authorizeViewer)
	if len(hiddenErrs) == 0 {
		t.Fatal("delete of hidden media succeeded from relocked session — existence oracle")
	}
	// Deleting an unknown id must fail with the SAME message (no oracle leak).
	_, unknownErrs := post(t, handler,
		`{"query":"mutation D($scope: String!, $pin: String){ deleteMyEngagement(scope: $scope, pin: $pin){ scope } }","variables":{"scope":"media:media-does-not-exist","pin":"1234"}}`,
		authorizeViewer)
	if len(unknownErrs) == 0 {
		t.Fatal("delete of unknown media succeeded")
	}
	if hiddenErrs[0].Message != unknownErrs[0].Message {
		t.Fatalf("hidden vs unknown delete error differ: %q vs %q", hiddenErrs[0].Message, unknownErrs[0].Message)
	}

	// The hidden row survived the failed delete: a revealed export still sees it.
	export := mydataReadEngagement(t, handler, authorizeRevealed)
	if len(export.PlaybackProgress) != 1 || export.PlaybackProgress[0].MediaID != "media-archive" {
		t.Fatalf("hidden row removed by failed delete: %#v", export.PlaybackProgress)
	}
}

// TestDeleteMyEngagementThrottlesWrongPIN asserts the H12 fix: the PIN-confirm
// lane is rate-limited with exponential backoff. After the
// free-attempt budget of wrong-PIN deletes the lane locks out — a later attempt
// is rejected even with the CORRECT PIN ("1234"), and nothing is deleted. It
// also confirms a correct PIN on a clean lane still succeeds (the throttle does
// not break the happy path).
func TestDeleteMyEngagementThrottlesWrongPIN(t *testing.T) {
	// feature:my-data-panel
	wrongPINDelete := `{"query":"mutation D($scope: String!, $pin: String){ deleteMyEngagement(scope: $scope, pin: $pin){ scope } }","variables":{"scope":"all","pin":"0000"}}`
	rightPINDelete := `{"query":"mutation D($scope: String!, $pin: String){ deleteMyEngagement(scope: $scope, pin: $pin){ scope } }","variables":{"scope":"all","pin":"1234"}}`

	// Correct PIN below the limit succeeds and deletes (happy path intact).
	withinLimit := newTestHandler(t)
	mydataSeedEngagement(t, withinLimit, authorizeViewer)
	if _, errs := post(t, withinLimit, rightPINDelete, authorizeViewer); len(errs) != 0 {
		t.Fatalf("correct-PIN delete below the limit errored = %#v", errs)
	}
	if s := mydataReadEngagement(t, withinLimit, authorizeViewer); len(s.PlaybackProgress) != 0 || len(s.Favorites) != 0 || len(s.Queue) != 0 {
		t.Fatalf("correct-PIN delete below limit left data: %#v", s)
	}

	// Lockout: the free-attempt budget of wrong PINs, each rejected and deleting
	// nothing, then the CORRECT PIN is still rejected (the backoff lane is locked).
	handler := newTestHandler(t)
	mydataSeedEngagement(t, handler, authorizeViewer)
	for i := 0; i < myDataDeleteBackoff.FreeAttempts; i++ {
		if _, errs := post(t, handler, wrongPINDelete, authorizeViewer); len(errs) == 0 {
			t.Fatalf("wrong-PIN delete attempt %d succeeded — PIN not enforced", i)
		}
	}
	// The data survived every rejected attempt.
	if s := mydataReadEngagement(t, handler, authorizeViewer); len(s.PlaybackProgress) != 1 || len(s.Favorites) != 1 || len(s.Queue) != 1 {
		t.Fatalf("wrong-PIN attempts deleted data: %#v", s)
	}
	// Now locked out: even the CORRECT PIN is rejected and deletes nothing.
	_, lockedErrs := post(t, handler, rightPINDelete, authorizeViewer)
	if len(lockedErrs) == 0 {
		t.Fatal("correct-PIN delete succeeded after lockout — brute-force lane unthrottled")
	}
	if lockedErrs[0].Message != "reveal PIN attempts throttled" {
		t.Fatalf("post-lockout error = %q, want %q", lockedErrs[0].Message, "reveal PIN attempts throttled")
	}
	if s := mydataReadEngagement(t, handler, authorizeViewer); len(s.PlaybackProgress) != 1 || len(s.Favorites) != 1 || len(s.Queue) != 1 {
		t.Fatalf("throttled delete still removed data: %#v", s)
	}
}

// TestDeleteMyEngagementIsProfileScopedNotManagerGated asserts the own-data
// binding: one Profile's delete never touches another Profile's data, and the
// op is NOT manager-gated (a plain viewer session may delete its own data; a
// managed Profile likewise owns and deletes only its own).
func TestDeleteMyEngagementIsProfileScopedNotManagerGated(t *testing.T) {
	// feature:my-data-panel
	handler := newTestHandler(t)
	// Seed the SAME visible Media for two distinct Profiles: profile-default
	// (via the viewer session) and profile-managed (via the managed session).
	authorizeManaged := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-managed")
		req.Header.Set("X-Beamfall-Device", "device-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	}
	mydataSeedEngagement(t, handler, authorizeViewer)
	mydataSeedEngagement(t, handler, authorizeManaged)

	// A plain VIEWER (non-manager) session deletes its own data successfully —
	// the op is not manager-gated.
	data, errs := post(t, handler,
		`{"query":"mutation D($scope: String!, $pin: String){ deleteMyEngagement(scope: $scope, pin: $pin){ scope } }","variables":{"scope":"all","pin":"1234"}}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("viewer deleteMyEngagement errors = %#v (must not be manager-gated)", errs)
	}
	var result MyDataDeleteResult
	mustUnmarshal(t, data["deleteMyEngagement"], &result)
	if result.Scope != "all" {
		t.Fatalf("viewer delete scope = %q, want all", result.Scope)
	}

	// profile-default's data is gone...
	gone := mydataReadEngagement(t, handler, authorizeViewer)
	if len(gone.PlaybackProgress) != 0 || len(gone.Favorites) != 0 || len(gone.Queue) != 0 {
		t.Fatalf("profile-default data survived its own delete: %#v", gone)
	}
	// ...but profile-managed's identical data is untouched (cross-profile
	// isolation — the resolver only ever passes scope.ProfileID).
	managed := mydataReadEngagement(t, handler, authorizeManaged)
	if len(managed.PlaybackProgress) != 1 || len(managed.Favorites) != 1 || len(managed.Queue) != 1 {
		t.Fatalf("profile-default delete clobbered profile-managed data: %#v", managed)
	}
}

// TestDeleteMyEngagementRequiresRevealPIN asserts ADR-0036 §2.2: the hard delete
// is PIN-confirmed. A profile that has a reveal PIN must supply a matching one —
// an absent or wrong PIN is rejected and deletes nothing (fail closed). A
// profile with no reveal PIN (managed kind) has nothing to confirm against and
// deletes without one (documented fall-through, see deleteMyEngagement).
func TestDeleteMyEngagementRequiresRevealPIN(t *testing.T) {
	// feature:my-data-panel
	handler := newTestHandler(t)
	mydataSeedEngagement(t, handler, authorizeViewer)

	// No PIN (old-client shape): rejected, and nothing is deleted.
	if _, errs := post(t, handler,
		`{"query":"mutation D($scope: String!){ deleteMyEngagement(scope: $scope){ scope } }","variables":{"scope":"all"}}`,
		authorizeViewer); len(errs) == 0 {
		t.Fatal("delete without a reveal PIN succeeded — My Data hard-delete is not PIN-gated")
	}
	if s := mydataReadEngagement(t, handler, authorizeViewer); len(s.PlaybackProgress) != 1 || len(s.Favorites) != 1 || len(s.Queue) != 1 {
		t.Fatalf("rejected (no-PIN) delete still removed data: %#v", s)
	}

	// Wrong PIN: rejected, still nothing deleted.
	if _, errs := post(t, handler,
		`{"query":"mutation D($scope: String!, $pin: String){ deleteMyEngagement(scope: $scope, pin: $pin){ scope } }","variables":{"scope":"all","pin":"0000"}}`,
		authorizeViewer); len(errs) == 0 {
		t.Fatal("delete with a wrong reveal PIN succeeded")
	}
	if s := mydataReadEngagement(t, handler, authorizeViewer); len(s.PlaybackProgress) != 1 {
		t.Fatalf("rejected (wrong-PIN) delete still removed data: %#v", s)
	}

	// A managed Profile has no self-service My Data delete path (ADR-0036
	// §2.6). Its guardian or the operator must use the Console mutation.
	authorizeManaged := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-managed")
		req.Header.Set("X-Beamfall-Device", "device-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	}
	mydataSeedEngagement(t, handler, authorizeManaged)
	if _, errs := post(t, handler,
		`{"query":"mutation D($scope: String!){ deleteMyEngagement(scope: $scope){ scope } }","variables":{"scope":"all"}}`,
		authorizeManaged); len(errs) == 0 || !strings.Contains(errs[0].Message, "managed profiles require guardian or operator data delete") {
		t.Fatalf("managed profile self-delete errors = %#v, want guardian/operator refusal", errs)
	}
	if s := mydataReadEngagement(t, handler, authorizeManaged); len(s.PlaybackProgress) != 1 || len(s.Favorites) != 1 || len(s.Queue) != 1 {
		t.Fatalf("rejected managed self-delete still removed data: %#v", s)
	}
}

// TestMyEngagementExportRequiresRevealPIN asserts ADR-0117: the My Data export
// is PIN-confirmed. A profile that has a reveal PIN must supply a matching one —
// an absent or wrong PIN is rejected (fail-closed). A profile with no reveal PIN
// (managed kind) has nothing to confirm against and exports without one (documented
// fall-through, parallel to deleteMyEngagement).
func TestMyEngagementExportRequiresRevealPIN(t *testing.T) {
	// feature:my-data-panel
	handler := newTestHandler(t)
	mydataSeedEngagement(t, handler, authorizeViewer)

	// No PIN: rejected.
	if _, errs := post(t, handler,
		`{"query":"query { myEngagement { schema playbackProgress { mediaID } } }"}`,
		authorizeViewer); len(errs) == 0 {
		t.Fatal("export without a reveal PIN succeeded — My Data export is not PIN-gated")
	}

	// Wrong PIN: rejected.
	if _, errs := post(t, handler,
		`{"query":"query Q($pin: String){ myEngagement(pin: $pin){ schema playbackProgress { mediaID } } }","variables":{"pin":"0000"}}`,
		authorizeViewer); len(errs) == 0 {
		t.Fatal("export with a wrong reveal PIN succeeded")
	}

	// Correct PIN: export succeeds and returns the seeded data.
	data, errs := post(t, handler,
		`{"query":"query Q($pin: String){ myEngagement(pin: $pin){ schema playbackProgress { mediaID } } }","variables":{"pin":"1234"}}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("export with correct PIN errors = %#v", errs)
	}
	var export MyEngagementExportInfo
	mustUnmarshal(t, data["myEngagement"], &export)
	if len(export.PlaybackProgress) != 1 {
		t.Fatalf("export.PlaybackProgress = %#v, want 1 entry", export.PlaybackProgress)
	}

	// Managed profile has no reveal PIN — exports without one (ErrNoRevealPath fall-through).
	authorizeManaged := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-managed")
		req.Header.Set("X-Beamfall-Device", "device-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	}
	mydataSeedEngagement(t, handler, authorizeManaged)
	if _, errs := post(t, handler,
		`{"query":"query { myEngagement { schema playbackProgress { mediaID } } }"}`,
		authorizeManaged); len(errs) != 0 {
		t.Fatalf("managed profile export errors = %#v (want fall-through without PIN)", errs)
	}
}

// TestMyEngagementExportRejectsPINlessAdult keeps ADR-0117's no-PIN policy
// separate from managed profiles: a newly-created adult Profile has no Reveal
// PIN, so the recovery-material-capable export path must fail closed. Managed
// Profiles still export engagement-only data because they hold no recovery
// material.
func TestMyEngagementExportRejectsPINlessAdult(t *testing.T) {
	// feature:my-data-panel
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	adult, err := st.CreateProfile(t.Context(), "No PIN Adult", store.ProfileAdult, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("create adult profile: %v", err)
	}
	if err := st.EnrollDevice(t.Context(),
		store.Device{ID: "device-pinless-adult", Name: "PIN-less Adult", Role: store.DeviceRoleViewer, SecretHash: store.HashDeviceSecret("pinless-adult-secret")},
		store.Session{ID: "session-pinless-adult", DeviceID: "device-pinless-adult", ProfileID: adult.ID},
	); err != nil {
		t.Fatalf("enroll PIN-less adult session: %v", err)
	}
	authorizePINlessAdult := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-pinless-adult")
		req.Header.Set("X-Beamfall-Device", "device-pinless-adult")
		req.Header.Set("X-Beamfall-Device-Secret", "pinless-adult-secret")
	}
	mydataSeedEngagement(t, handler, authorizePINlessAdult)

	data, errs := post(t, handler,
		`{"query":"query { myEngagement { schema profile { id kind } playbackProgress { mediaID } } }"}`,
		authorizePINlessAdult)
	if len(errs) == 0 {
		t.Fatalf("PIN-less adult export succeeded with payload %s; want clientError", data["myEngagement"])
	}
	if !strings.Contains(errs[0].Message, "reveal PIN must be set before exporting your data") {
		t.Fatalf("PIN-less adult export error = %#v, want no-PIN clientError", errs)
	}
	if strings.Contains(string(data["myEngagement"]), "recoveryKey") {
		t.Fatalf("PIN-less adult export payload contains recovery material: %s", data["myEngagement"])
	}

	authorizeManaged := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-managed")
		req.Header.Set("X-Beamfall-Device", "device-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	}
	mydataSeedEngagement(t, handler, authorizeManaged)
	data, errs = post(t, handler,
		`{"query":"query { myEngagement { schema profile { id kind } playbackProgress { mediaID } } }"}`,
		authorizeManaged)
	if len(errs) != 0 {
		t.Fatalf("managed profile export errors = %#v (want unchanged engagement-only export)", errs)
	}
	if strings.Contains(string(data["myEngagement"]), "recoveryKey") {
		t.Fatalf("managed export payload contains recovery material: %s", data["myEngagement"])
	}
}

const operatorDeleteMutation = `{"query":"mutation D($profileID: String!){ operatorDeleteProfileData(profileID: $profileID){ profileID } }","variables":{"profileID":"profile-managed"}}`

// authorizeManagedSession is the seeded managed-Profile viewer session, used to
// seed/read the TARGET Profile's engagement in the operator-delete tests.
func authorizeManagedSession(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", "session-managed")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
}

// TestOperatorDeleteProfileDataAdminDeletesManagedFootprint is the ADR-0036
// §2.5/§2.6 happy path: a Console operator with an open Admin Session deletes a
// managed Profile's engagement footprint. The target's engagement + per-Profile
// Saved Filters are gone, its server-global SmartCollection is DISOWNED (not
// deleted), and a DIFFERENT Profile's identical data is untouched.
func TestOperatorDeleteProfileDataAdminDeletesManagedFootprint(t *testing.T) {
	// feature:my-data-panel
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	// Seed engagement for the TARGET (profile-managed) and a bystander
	// (profile-default), plus a Saved Filter + authored SmartCollection on the
	// target via the store (the create graph mutations are exercised elsewhere).
	mydataSeedEngagement(t, handler, authorizeManagedSession)
	mydataSeedEngagement(t, handler, authorizeViewer)
	now := time.Unix(0, 0).UTC()
	savedFilter, err := st.CreateSavedFilter(t.Context(), store.SavedFilter{
		ProfileID: "profile-managed", LibraryID: "library-local", Name: "Kid Filter", FilterJSON: `{"t":"x"}`,
	}, now)
	if err != nil {
		t.Fatalf("seed saved filter: %v", err)
	}
	col, colErr := st.CreateSmartCollection(t.Context(), store.SmartCollection{
		LibraryID: "library-local", Name: "Kid Col", SourceExpr: `{"a":1}`, CreatedByProfile: "profile-managed",
	}, now)
	if colErr != nil {
		t.Fatalf("seed collection: %v", colErr)
	}

	// Operator-delete via the admin session.
	data, errs := post(t, handler, operatorDeleteMutation, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("operatorDeleteProfileData errors = %#v", errs)
	}
	var result OperatorDeleteProfileDataResult
	mustUnmarshal(t, data["operatorDeleteProfileData"], &result)
	if result.ProfileID != "profile-managed" {
		t.Fatalf("result profileID = %q, want profile-managed", result.ProfileID)
	}
	entries, err := st.ActivityEntries(t.Context(), store.ActivityQuery{
		Filter: store.LibraryFilter{AllLibraries: true},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("activity entries: %v", err)
	}
	if len(entries) == 0 ||
		entries[0].Action != store.ActivityOperatorDataDelete ||
		entries[0].ProfileID != "profile-default" ||
		entries[0].TargetType != "profile" ||
		entries[0].TargetID != "profile-managed" {
		t.Fatalf("operator delete activity entry = %#v, want operator/target audit row", entries)
	}

	// Target engagement gone.
	gone := mydataReadEngagement(t, handler, authorizeManagedSession)
	if len(gone.PlaybackProgress) != 0 || len(gone.Favorites) != 0 || len(gone.Queue) != 0 {
		t.Fatalf("target engagement survived operator delete: %#v", gone)
	}
	// Target Saved Filter gone; authored Collection DISOWNED (row survives).
	if _, err := st.SavedFilterByID(t.Context(), savedFilter.ID); err == nil {
		t.Fatal("target saved filter survived operator delete")
	}
	if _, err := st.SmartCollectionByID(t.Context(), col.ID); err != nil {
		t.Fatalf("target collection deleted, not disowned: %v", err)
	}
	// Bystander (profile-default) engagement untouched.
	bystander := mydataReadEngagement(t, handler, authorizeViewer)
	if len(bystander.PlaybackProgress) != 1 || len(bystander.Favorites) != 1 || len(bystander.Queue) != 1 {
		t.Fatalf("operator delete clobbered bystander data: %#v", bystander)
	}
}

// TestOperatorDeleteProfileDataRequiresAdminSession is the load-bearing security
// gate: a non-admin caller (a plain manager session with no Admin Session open)
// is refused. An un-admin-gated operator-delete is a security hole.
func TestOperatorDeleteProfileDataRequiresAdminSession(t *testing.T) {
	// feature:my-data-panel
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	// NOTE: no openManagerAdminSession — the manager session is NOT admin.

	_, errs := post(t, handler, operatorDeleteMutation, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("non-admin operatorDeleteProfileData errors = %#v, want admin-session gate", errs)
	}

	// A plain viewer session is likewise refused at the same gate.
	if _, errs := post(t, handler, operatorDeleteMutation, authorizeViewer); len(errs) == 0 ||
		!strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("viewer operatorDeleteProfileData errors = %#v, want admin-session gate", errs)
	}
}

// TestOperatorDeleteProfileDataCanDeleteAdultTarget pins ADR-0036 §2.5's
// "any Profile" operator-delete contract: the Admin-Session Console path can
// target an adult Profile too.
func TestOperatorDeleteProfileDataCanDeleteAdultTarget(t *testing.T) {
	// feature:my-data-panel
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	mydataSeedEngagement(t, handler, authorizeViewer)

	adultTarget := `{"query":"mutation D($profileID: String!){ operatorDeleteProfileData(profileID: $profileID){ profileID } }","variables":{"profileID":"profile-default"}}`
	data, errs := post(t, handler, adultTarget, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("adult-target operatorDeleteProfileData errors = %#v", errs)
	}
	var result OperatorDeleteProfileDataResult
	mustUnmarshal(t, data["operatorDeleteProfileData"], &result)
	if result.ProfileID != "profile-default" {
		t.Fatalf("result profileID = %q, want profile-default", result.ProfileID)
	}
	gone := mydataReadEngagement(t, handler, authorizeViewer)
	if len(gone.PlaybackProgress) != 0 || len(gone.Favorites) != 0 || len(gone.Queue) != 0 {
		t.Fatalf("adult target engagement survived operator delete: %#v", gone)
	}
}
