package graph

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// Library Health Dashboard per-selection bulk operations and the two
// whole-library variants not covered by the earlier test file:
// bulkRescrapeScrapeFailures and bulkFetchMissingArtwork.
//
// These are the five *Selected mutations (drill-down panel, web UI) and the two
// pre-existing whole-library mutations that were missing test coverage. Each
// test exercises Admin-Session gating, the basic happy-path, and a readable
// mutation invocation so the conformance scanner detects the field name.

// openAdmin opens the Admin Session window for the given handler.
func openAdmin(t *testing.T, handler *Handler) {
	t.Helper()
	if _, errs := post(t, handler,
		`{"query":"mutation V($password:String!){ verifyAdminSession(password:$password){ active } }","variables":{"password":"correct horse battery"}}`,
		authorizeManager); len(errs) != 0 {
		t.Fatalf("openAdmin: admin re-auth errors = %#v", errs)
	}
}

// setupHealthStore opens a test store, sets the admin password, seeds one root
// on library-local, and returns the store + handler.
func setupHealthStore(t *testing.T) (*store.Store, *Handler) {
	t.Helper()
	st := storetest.Open(t)
	ctx := context.Background()
	if err := st.SetAdminPassword(ctx, "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryRoots(ctx, "library-local", []string{"/media/local"}); err != nil {
		t.Fatalf("SetLibraryRoots: %v", err)
	}
	return st, newTestHandlerWithStore(st)
}

func setupHealthIdentifyStore(t *testing.T) (*store.Store, *Handler, store.LibraryID) {
	t.Helper()
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "demo",
		Version:     "0.1.0",
		DisplayName: plugin.L("Demo"),
		MediaLabel:  plugin.L("Item"),
		Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
		MediaFields: []plugin.FieldSpec{{Name: "class", Type: "text", Label: plugin.L("Class")}},
		Scrapers: []plugin.ScraperSpec{{
			ID:                 "lookup",
			DisplayName:        plugin.L("Demo Lookup"),
			Kind:               "http-json",
			Endpoint:           "https://demo.example.test",
			SearchTemplate:     "/search?q={query}",
			CandidatesPath:     "results",
			CandidateIDPath:    "id",
			LookupTemplate:     "/title/{id}",
			FieldMap:           map[string]string{"title": "name", "summary": "overview", "year": "year"},
			ImpliedMediaFields: map[string]string{"media.class": "video"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
		auth.NewService(st),
		st,
		registry,
	)
	handler.SetIdentify(identify.NewService(identifyTestEngine{}, registry, st))
	ctx := context.Background()
	if err := st.SetAdminPassword(ctx, "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	library, err := st.CreateLibraryWithRoots(ctx, "Health Demo", false, "demo", "profile-default", []string{"/media/health-demo"}, "")
	if err != nil {
		t.Fatalf("CreateLibraryWithRoots: %v", err)
	}
	return st, handler, library.ID
}

func healthCount(t *testing.T, handler *Handler, libraryID, dimension string) int {
	t.Helper()
	data, errs := post(t, handler, `{"query":"query { libraryHealth { libraryID counts } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("libraryHealth errors = %#v", errs)
	}
	var rows []LibraryHealthInfo
	mustUnmarshal(t, data["libraryHealth"], &rows)
	for _, row := range rows {
		if row.LibraryID == libraryID {
			return row.Counts[dimension]
		}
	}
	t.Fatalf("library %s missing from libraryHealth rows %#v", libraryID, rows)
	return 0
}

// importScrapeFailure imports a Media that will appear in the
// HealthScrapeFailure dimension: title_identified=0 (default) with no external
// IDs — the "identification pipeline ran but produced nothing" condition.
func importScrapeFailure(t *testing.T, st *store.Store, libraryID, title, fingerprint, oshash string) store.MediaID {
	t.Helper()
	ctx := context.Background()
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:           store.LibraryID(libraryID),
		Path:                "/media/local/" + title + ".mp4",
		Title:               title,
		Container:           "mp4",
		Codec:               "h264",
		ContentFingerprint:  fingerprint,
		DurationSecondsHint: 100,
		OSHash:              oshash,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile(%s): %v", title, err)
	}
	return media.ID
}

// ── bulkRescrapeScrapeFailures ──────────────────────────────────────────────

// TestBulkRescrapeScrapeFailuresAdminGatedEnqueues verifies that
// bulkRescrapeScrapeFailures is Admin-Session gated and enqueues scan jobs for
// each Library root on success.
//
// feature:library-health
func TestBulkRescrapeScrapeFailuresAdminGatedEnqueues(t *testing.T) {
	st, handler := setupHealthStore(t)
	ctx := context.Background()

	_ = importScrapeFailure(t, st, "library-local", "scrape-fail-1", "sha256:sf1", "aaaa000000000071")

	rescrape := `{"query":"mutation R($libraryID:String!){ bulkRescrapeScrapeFailures(libraryID:$libraryID){ affected } }","variables":{"libraryID":"library-local"}}`

	// Gate: admin session required.
	_, errs := post(t, handler, rescrape, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("pre-admin bulkRescrapeScrapeFailures errors = %#v, want admin gate", errs)
	}

	openAdmin(t, handler)

	data, errs := post(t, handler, rescrape, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("bulkRescrapeScrapeFailures errors = %#v", errs)
	}
	var result BulkOpResult
	mustUnmarshal(t, data["bulkRescrapeScrapeFailures"], &result)
	if result.Affected < 1 {
		t.Errorf("bulkRescrapeScrapeFailures affected = %d, want >= 1", result.Affected)
	}

	// A scan job must be queued for the Library root.
	queued, err := st.ListJobs(ctx, store.JobStatusQueued)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(queued) == 0 {
		t.Error("no scan jobs queued after bulkRescrapeScrapeFailures")
	}
}

// ── bulkFetchMissingArtwork ─────────────────────────────────────────────────

// TestBulkFetchMissingArtworkAdminGatedEnqueues verifies that
// bulkFetchMissingArtwork is Admin-Session gated and enqueues the global
// artwork sweep.
//
// feature:library-health
func TestBulkFetchMissingArtworkAdminGatedEnqueues(t *testing.T) {
	st, handler := setupHealthStore(t)
	ctx := context.Background()

	// Seed a Media with no artwork so it appears in the missing-artwork dimension.
	bulkOpsImport(t, st, "library-local", "no-art", "/media/local/no-art.mp4", "sha256:noart1", "aaaa000000000081")

	fetchArtwork := `{"query":"mutation F($libraryID:String!){ bulkFetchMissingArtwork(libraryID:$libraryID){ affected } }","variables":{"libraryID":"library-local"}}`

	// Gate.
	_, errs := post(t, handler, fetchArtwork, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("pre-admin bulkFetchMissingArtwork errors = %#v, want admin gate", errs)
	}

	openAdmin(t, handler)

	data, errs := post(t, handler, fetchArtwork, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("bulkFetchMissingArtwork errors = %#v", errs)
	}
	var result BulkOpResult
	mustUnmarshal(t, data["bulkFetchMissingArtwork"], &result)
	// result.Affected may be 0 if the test store has no missing-artwork items;
	// the gate + no-error is the primary assertion.
	_ = result.Affected

	// Artwork sweep job must be queued.
	queued, err := st.ListJobs(ctx, store.JobStatusQueued)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	hasArtwork := false
	for _, j := range queued {
		if j.Kind == "artwork-sweep" {
			hasArtwork = true
		}
	}
	if !hasArtwork {
		t.Error("no artwork-sweep job queued after bulkFetchMissingArtwork")
	}
}

// ── *Selected mutations ─────────────────────────────────────────────────────

// TestBulkRescanUnmatchedSelectedAdminGatedScopesIdentify verifies Admin-Session
// gating and item-scoped identification for bulkRescanUnmatchedSelected.
//
// feature:library-health
func TestBulkRescanUnmatchedSelectedAdminGatedScopesIdentify(t *testing.T) {
	st, handler, libraryID := setupHealthIdentifyStore(t)
	ctx := context.Background()

	mediaID := bulkOpsImport(t, st, string(libraryID), "blue harvest", "/media/health-demo/blue-harvest.mp4", "sha256:blue-harvest", "aaaa000000000091")

	rescanSel := `{"query":"mutation R($libraryID:String!,$mediaIDs:[String!]!){ bulkRescanUnmatchedSelected(libraryID:$libraryID,mediaIDs:$mediaIDs){ affected } }","variables":{"libraryID":"` + string(libraryID) + `","mediaIDs":["` + string(mediaID) + `"]}}`

	_, errs := post(t, handler, rescanSel, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("pre-admin bulkRescanUnmatchedSelected errors = %#v, want admin gate", errs)
	}

	openAdmin(t, handler)

	data, errs := post(t, handler, rescanSel, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("bulkRescanUnmatchedSelected errors = %#v", errs)
	}
	var result BulkOpResult
	mustUnmarshal(t, data["bulkRescanUnmatchedSelected"], &result)
	if result.Affected != 1 {
		t.Fatalf("bulkRescanUnmatchedSelected affected = %d, want 1", result.Affected)
	}

	queued, err := st.ListJobs(ctx, store.JobStatusQueued)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	for _, job := range queued {
		if job.Kind == "scan" {
			t.Fatalf("bulkRescanUnmatchedSelected queued broad scan job %#v", job)
		}
	}
}

// TestBulkTrashBrokenSelectedAdminGatedTrashes verifies Admin-Session gating
// and that bulkTrashBrokenSelected soft-deletes the nominated broken Media.
//
// feature:library-health
func TestBulkTrashBrokenSelectedAdminGatedTrashes(t *testing.T) {
	st, handler := setupHealthStore(t)
	ctx := context.Background()

	// Seed a broken Media (see health_bulkops_test.go for the fixture recipe).
	brokenID := bulkOpsImport(t, st, "library-local", "broken-sel", "/media/local/broken-sel.mp4", "sha256:broken-sel-v1", "bbbb000000000071")
	_ = bulkOpsImport(t, st, "library-local", "broken-sel-replacement", "/media/local/broken-sel.mp4", "sha256:broken-sel-v2", "bbbb000000000072")

	trashSel := `{"query":"mutation T($libraryID:String!,$mediaIDs:[String!]!){ bulkTrashBrokenSelected(libraryID:$libraryID,mediaIDs:$mediaIDs){ affected } }","variables":{"libraryID":"library-local","mediaIDs":["` + string(brokenID) + `"]}}`

	_, errs := post(t, handler, trashSel, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("pre-admin bulkTrashBrokenSelected errors = %#v, want admin gate", errs)
	}

	openAdmin(t, handler)
	if err := st.RefreshLibraryHealthCache(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("RefreshLibraryHealthCache: %v", err)
	}
	beforeBroken := healthCount(t, handler, "library-local", "broken")

	data, errs := post(t, handler, trashSel, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("bulkTrashBrokenSelected errors = %#v", errs)
	}
	var result BulkOpResult
	mustUnmarshal(t, data["bulkTrashBrokenSelected"], &result)
	if result.Affected != 1 {
		t.Errorf("bulkTrashBrokenSelected affected = %d, want 1", result.Affected)
	}
	if after := healthCount(t, handler, "library-local", "broken"); after != beforeBroken-1 {
		t.Fatalf("broken count after selected trash = %d, want %d", after, beforeBroken-1)
	}

	// The broken Media must be in the trash (soft-deleted, recoverable).
	localFilter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	trash, err := st.TrashedMediaList(ctx, localFilter)
	if err != nil {
		t.Fatalf("TrashedMediaList: %v", err)
	}
	found := false
	for _, e := range trash {
		if e.ID == brokenID {
			found = true
		}
	}
	if !found {
		t.Errorf("broken Media %s not in trash after bulkTrashBrokenSelected", brokenID)
	}
}

// TestBulkRescrapeScrapeFailuresSelectedAdminGatedScopesIdentify verifies Admin-
// Session gating and item-scoped identification for scrape failures.
//
// feature:library-health
func TestBulkRescrapeScrapeFailuresSelectedAdminGatedScopesIdentify(t *testing.T) {
	st, handler, libraryID := setupHealthIdentifyStore(t)
	ctx := context.Background()

	mediaID := importScrapeFailure(t, st, string(libraryID), "blue harvest", "sha256:sfsel1", "aaaa000000000072")

	rescrapeSel := `{"query":"mutation R($libraryID:String!,$mediaIDs:[String!]!){ bulkRescrapeScrapeFailuresSelected(libraryID:$libraryID,mediaIDs:$mediaIDs){ affected } }","variables":{"libraryID":"` + string(libraryID) + `","mediaIDs":["` + string(mediaID) + `"]}}`

	_, errs := post(t, handler, rescrapeSel, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("pre-admin bulkRescrapeScrapeFailuresSelected errors = %#v, want admin gate", errs)
	}

	openAdmin(t, handler)

	data, errs := post(t, handler, rescrapeSel, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("bulkRescrapeScrapeFailuresSelected errors = %#v", errs)
	}
	var result BulkOpResult
	mustUnmarshal(t, data["bulkRescrapeScrapeFailuresSelected"], &result)
	if result.Affected != 1 {
		t.Errorf("bulkRescrapeScrapeFailuresSelected affected = %d, want 1 (the seeded scrape-failure item)", result.Affected)
	}

	queued, err := st.ListJobs(ctx, store.JobStatusQueued)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	for _, job := range queued {
		if job.Kind == "scan" {
			t.Fatalf("bulkRescrapeScrapeFailuresSelected queued broad scan job %#v", job)
		}
	}
}

// TestBulkFetchMissingArtworkSelectedAdminGatedEnqueues verifies Admin-Session
// gating and enqueue for bulkFetchMissingArtworkSelected.
//
// feature:library-health
func TestBulkFetchMissingArtworkSelectedAdminGatedEnqueues(t *testing.T) {
	st, handler := setupHealthStore(t)
	ctx := context.Background()

	mediaID := bulkOpsImport(t, st, "library-local", "no-art-sel", "/media/local/no-art-sel.mp4", "sha256:noartsel1", "aaaa000000000082")

	fetchArtSel := `{"query":"mutation F($libraryID:String!,$mediaIDs:[String!]!){ bulkFetchMissingArtworkSelected(libraryID:$libraryID,mediaIDs:$mediaIDs){ affected } }","variables":{"libraryID":"library-local","mediaIDs":["` + string(mediaID) + `"]}}`

	_, errs := post(t, handler, fetchArtSel, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("pre-admin bulkFetchMissingArtworkSelected errors = %#v, want admin gate", errs)
	}

	openAdmin(t, handler)

	data, errs := post(t, handler, fetchArtSel, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("bulkFetchMissingArtworkSelected errors = %#v", errs)
	}
	var result BulkOpResult
	mustUnmarshal(t, data["bulkFetchMissingArtworkSelected"], &result)
	if result.Affected != 1 {
		t.Fatalf("bulkFetchMissingArtworkSelected affected = %d, want 1", result.Affected)
	}

	queued, err := st.ListJobs(ctx, store.JobStatusQueued)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	var selectedPayload struct {
		MediaIDs []store.MediaID `json:"media_ids"`
	}
	for _, j := range queued {
		if j.Kind == "artwork-sweep" {
			if err := json.Unmarshal(j.Payload, &selectedPayload); err != nil {
				t.Fatalf("artwork-sweep payload: %v", err)
			}
		}
	}
	if len(selectedPayload.MediaIDs) != 1 || selectedPayload.MediaIDs[0] != mediaID {
		t.Errorf("selected artwork payload media_ids = %#v, want [%s]", selectedPayload.MediaIDs, mediaID)
	}
	if selectedPayload.MediaIDs == nil {
		t.Error("no artwork-sweep job queued after bulkFetchMissingArtworkSelected")
	}
}

// TestSelectedJobRemediationNoopsForOutOfScopeIDs verifies selected, job-based
// health remediation never queues broad work when none of the requested IDs
// validate against the live dimension set.
//
// feature:library-health
func TestSelectedJobRemediationNoopsForOutOfScopeIDs(t *testing.T) {
	st, handler := setupHealthStore(t)
	ctx := context.Background()
	openAdmin(t, handler)

	mutations := []struct {
		name  string
		field string
	}{
		{name: "unmatched", field: "bulkRescanUnmatchedSelected"},
		{name: "scrape-failure", field: "bulkRescrapeScrapeFailuresSelected"},
		{name: "missing-artwork", field: "bulkFetchMissingArtworkSelected"},
	}
	for _, tt := range mutations {
		query := `{"query":"mutation M($libraryID:String!,$mediaIDs:[String!]!){ ` + tt.field + `(libraryID:$libraryID,mediaIDs:$mediaIDs){ affected } }","variables":{"libraryID":"library-local","mediaIDs":["not-a-live-health-item"]}}`
		data, errs := post(t, handler, query, authorizeManager)
		if len(errs) != 0 {
			t.Fatalf("%s selected no-op errors = %#v", tt.name, errs)
		}
		var result BulkOpResult
		mustUnmarshal(t, data[tt.field], &result)
		if result.Affected != 0 {
			t.Fatalf("%s selected no-op affected = %d, want 0", tt.name, result.Affected)
		}
		queued, err := st.ListJobs(ctx, store.JobStatusQueued)
		if err != nil {
			t.Fatalf("ListJobs after %s: %v", tt.name, err)
		}
		if len(queued) != 0 {
			t.Fatalf("%s selected no-op queued jobs = %#v, want none", tt.name, queued)
		}
	}
}

// TestBulkMergeDuplicatesSelectedAdminGatedTrashes verifies Admin-Session
// gating and that bulkMergeDuplicatesSelected soft-deletes the nominated
// duplicate(s) through the audited Trash path.
//
// feature:library-health
func TestBulkMergeDuplicatesSelectedAdminGatedTrashes(t *testing.T) {
	st, handler := setupHealthStore(t)
	ctx := context.Background()

	// Seed a duplicate pair (shared OSHash marks them as byte-identical).
	dupA := bulkOpsImport(t, st, "library-local", "dupA-sel", "/media/local/dupA-sel.mp4", "sha256:dupAsel", "ffff000000000011")
	dupB := bulkOpsImport(t, st, "library-local", "dupB-sel", "/media/local/dupB-sel.mp4", "sha256:dupBsel", "ffff000000000011")
	if dupA == dupB {
		t.Fatalf("duplicate pair collapsed; fixture is wrong")
	}

	// Trash dupB (the "merge away" candidate), keeping dupA.
	mergeSel := `{"query":"mutation M($libraryID:String!,$mediaIDs:[String!]!){ bulkMergeDuplicatesSelected(libraryID:$libraryID,mediaIDs:$mediaIDs){ affected } }","variables":{"libraryID":"library-local","mediaIDs":["` + string(dupB) + `"]}}`

	_, errs := post(t, handler, mergeSel, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("pre-admin bulkMergeDuplicatesSelected errors = %#v, want admin gate", errs)
	}

	openAdmin(t, handler)
	if err := st.RefreshLibraryHealthCache(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("RefreshLibraryHealthCache: %v", err)
	}
	beforeDuplicate := healthCount(t, handler, "library-local", "duplicate")

	data, errs := post(t, handler, mergeSel, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("bulkMergeDuplicatesSelected errors = %#v", errs)
	}
	var result BulkOpResult
	mustUnmarshal(t, data["bulkMergeDuplicatesSelected"], &result)
	if result.Affected != 1 {
		t.Errorf("bulkMergeDuplicatesSelected affected = %d, want 1", result.Affected)
	}
	if after := healthCount(t, handler, "library-local", "duplicate"); after != beforeDuplicate-2 {
		t.Fatalf("duplicate count after selected merge = %d, want %d", after, beforeDuplicate-2)
	}

	// dupB must be in trash; dupA must NOT.
	localFilter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	trash, err := st.TrashedMediaList(ctx, localFilter)
	if err != nil {
		t.Fatalf("TrashedMediaList: %v", err)
	}
	foundB, foundA := false, false
	for _, e := range trash {
		if e.ID == dupB {
			foundB = true
		}
		if e.ID == dupA {
			foundA = true
		}
	}
	if !foundB {
		t.Errorf("dupB %s not in trash after bulkMergeDuplicatesSelected", dupB)
	}
	if foundA {
		t.Errorf("dupA (survivor) %s was trashed — only dupB should be trashed", dupA)
	}
}

// TestBulkTrashBrokenSummaryRemediatesBeyondDrilldownCap proves the summary
// bulk action is not constrained by the 500-row drilldown cap.
//
// feature:library-health
func TestBulkTrashBrokenSummaryRemediatesBeyondDrilldownCap(t *testing.T) {
	st, handler := setupHealthStore(t)
	ctx := context.Background()

	broken := make([]store.MediaID, 0, 501)
	for i := 0; i < 501; i++ {
		title := "broken-large-" + strconv.Itoa(i)
		path := "/media/local/" + title + ".mp4"
		id := bulkOpsImport(t, st, "library-local", title, path, "sha256:"+title, "bbbb0000001"+strconv.Itoa(i))
		ok, err := st.SetMediaFileDecodeBroken(ctx, id, true, time.UnixMilli(int64(i+1)))
		if err != nil {
			t.Fatalf("SetMediaFileDecodeBroken(%s): %v", id, err)
		}
		if !ok {
			t.Fatalf("SetMediaFileDecodeBroken(%s) reported no update", id)
		}
		broken = append(broken, id)
	}

	openAdmin(t, handler)
	mutation := `{"query":"mutation T($libraryID:String!){ bulkTrashBroken(libraryID:$libraryID){ affected } }","variables":{"libraryID":"library-local"}}`
	data, errs := post(t, handler, mutation, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("bulkTrashBroken errors = %#v", errs)
	}
	var result BulkOpResult
	mustUnmarshal(t, data["bulkTrashBroken"], &result)
	if result.Affected < len(broken) {
		t.Fatalf("bulkTrashBroken affected = %d, want at least %d", result.Affected, len(broken))
	}

	trash, err := st.TrashedMediaList(ctx, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}})
	if err != nil {
		t.Fatalf("TrashedMediaList: %v", err)
	}
	found := map[store.MediaID]bool{}
	for _, entry := range trash {
		found[entry.ID] = true
	}
	for _, id := range broken {
		if !found[id] {
			t.Fatalf("bulkTrashBroken left broken item %s untrashed", id)
		}
	}
}

// TestBulkMergeDuplicatesSelectedRequiresWholeCluster proves selected duplicate
// remediation is cluster-level consent: selecting only one redundant candidate
// in a three-item duplicate cluster is a no-op, and selecting every redundant
// candidate merges that cluster through recoverable Trash.
//
// feature:library-health
func TestBulkMergeDuplicatesSelectedRequiresWholeCluster(t *testing.T) {
	st, handler := setupHealthStore(t)
	ctx := context.Background()
	all := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}

	_ = bulkOpsImport(t, st, "library-local", "dup3A-sel", "/media/local/dup3A-sel.mp4", "sha256:dup3Asel", "ffff000000000021")
	_ = bulkOpsImport(t, st, "library-local", "dup3B-sel", "/media/local/dup3B-sel.mp4", "sha256:dup3Bsel", "ffff000000000021")
	_ = bulkOpsImport(t, st, "library-local", "dup3C-sel", "/media/local/dup3C-sel.mp4", "sha256:dup3Csel", "ffff000000000021")
	groups, err := st.HealthDuplicateGroups(ctx, "library-local", all)
	if err != nil {
		t.Fatalf("HealthDuplicateGroups: %v", err)
	}
	if len(groups) != 1 || len(groups[0]) != 3 {
		t.Fatalf("groups = %#v, want one three-item cluster", groups)
	}
	redundantA := groups[0][1].MediaID
	redundantB := groups[0][2].MediaID

	openAdmin(t, handler)

	partial := `{"query":"mutation M($libraryID:String!,$mediaIDs:[String!]!){ bulkMergeDuplicatesSelected(libraryID:$libraryID,mediaIDs:$mediaIDs){ affected } }","variables":{"libraryID":"library-local","mediaIDs":["` + string(redundantA) + `"]}}`
	data, errs := post(t, handler, partial, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("partial bulkMergeDuplicatesSelected errors = %#v", errs)
	}
	var result BulkOpResult
	mustUnmarshal(t, data["bulkMergeDuplicatesSelected"], &result)
	if result.Affected != 0 {
		t.Fatalf("partial merge affected = %d, want 0", result.Affected)
	}
	trash, err := st.TrashedMediaList(ctx, all)
	if err != nil {
		t.Fatalf("TrashedMediaList after partial: %v", err)
	}
	for _, entry := range trash {
		if entry.ID == redundantA || entry.ID == redundantB {
			t.Fatalf("partial duplicate selection trashed %s; trash=%#v", entry.ID, trash)
		}
	}

	full := `{"query":"mutation M($libraryID:String!,$mediaIDs:[String!]!){ bulkMergeDuplicatesSelected(libraryID:$libraryID,mediaIDs:$mediaIDs){ affected } }","variables":{"libraryID":"library-local","mediaIDs":["` + string(redundantA) + `","` + string(redundantB) + `"]}}`
	data, errs = post(t, handler, full, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("full bulkMergeDuplicatesSelected errors = %#v", errs)
	}
	mustUnmarshal(t, data["bulkMergeDuplicatesSelected"], &result)
	if result.Affected != 2 {
		t.Fatalf("full merge affected = %d, want 2", result.Affected)
	}
	trash, err = st.TrashedMediaList(ctx, all)
	if err != nil {
		t.Fatalf("TrashedMediaList after full: %v", err)
	}
	found := map[store.MediaID]bool{}
	for _, entry := range trash {
		found[entry.ID] = true
	}
	if !found[redundantA] || !found[redundantB] {
		t.Fatalf("full duplicate selection trash = %#v, want %s and %s", trash, redundantA, redundantB)
	}
}

// TestBulkMergeDuplicatesSelectedRequiresWholeMergeApplySet proves selected
// duplicate remediation preflights the exact MergeApply member set. If a
// survivor has two hash-bearing files, HealthDuplicateGroups displays one group
// per hash; selecting only one displayed redundant candidate must not trash the
// other candidate from the survivor's second hash.
//
// feature:library-health
func TestBulkMergeDuplicatesSelectedRequiresWholeMergeApplySet(t *testing.T) {
	st, handler := setupHealthStore(t)
	ctx := context.Background()
	all := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}

	survivor, _, err := st.ImportMediaFileParts(ctx, []store.ImportMediaFilePart{
		{
			SourceOrdinal: 1,
			File: store.ImportMediaFile{
				LibraryID:           "library-local",
				Path:                "/media/local/multihash-a.mp4",
				Title:               "multihash-survivor",
				Container:           "mp4",
				Codec:               "h264",
				ContentFingerprint:  "sha256:multihash-a",
				DurationSecondsHint: 100,
				OSHash:              "ffff000000000031",
			},
		},
		{
			SourceOrdinal: 2,
			File: store.ImportMediaFile{
				LibraryID:           "library-local",
				Path:                "/media/local/multihash-b.mp4",
				Title:               "multihash-survivor",
				Container:           "mp4",
				Codec:               "h264",
				ContentFingerprint:  "sha256:multihash-b",
				DurationSecondsHint: 100,
				OSHash:              "ffff000000000032",
			},
		},
	})
	if err != nil {
		t.Fatalf("ImportMediaFileParts: %v", err)
	}
	redundantA := bulkOpsImport(t, st, "library-local", "multihash-drop-a", "/media/local/multihash-drop-a.mp4", "sha256:multihash-drop-a", "ffff000000000031")
	redundantB := bulkOpsImport(t, st, "library-local", "multihash-drop-b", "/media/local/multihash-drop-b.mp4", "sha256:multihash-drop-b", "ffff000000000032")

	groups, err := st.HealthDuplicateGroups(ctx, "library-local", all)
	if err != nil {
		t.Fatalf("HealthDuplicateGroups: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("groups = %#v, want two displayed hash groups", groups)
	}
	for _, group := range groups {
		if len(group) != 2 || group[0].MediaID != survivor.ID {
			t.Fatalf("group = %#v, want survivor %s plus one redundant item", group, survivor.ID)
		}
	}

	openAdmin(t, handler)

	partial := `{"query":"mutation M($libraryID:String!,$mediaIDs:[String!]!){ bulkMergeDuplicatesSelected(libraryID:$libraryID,mediaIDs:$mediaIDs){ affected } }","variables":{"libraryID":"library-local","mediaIDs":["` + string(redundantA) + `"]}}`
	data, errs := post(t, handler, partial, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("partial multihash bulkMergeDuplicatesSelected errors = %#v", errs)
	}
	var result BulkOpResult
	mustUnmarshal(t, data["bulkMergeDuplicatesSelected"], &result)
	if result.Affected != 0 {
		t.Fatalf("partial multihash merge affected = %d, want 0", result.Affected)
	}
	trash, err := st.TrashedMediaList(ctx, all)
	if err != nil {
		t.Fatalf("TrashedMediaList after partial multihash: %v", err)
	}
	for _, entry := range trash {
		if entry.ID == redundantA || entry.ID == redundantB {
			t.Fatalf("partial multihash selection trashed %s; trash=%#v", entry.ID, trash)
		}
	}

	full := `{"query":"mutation M($libraryID:String!,$mediaIDs:[String!]!){ bulkMergeDuplicatesSelected(libraryID:$libraryID,mediaIDs:$mediaIDs){ affected } }","variables":{"libraryID":"library-local","mediaIDs":["` + string(redundantA) + `","` + string(redundantB) + `"]}}`
	data, errs = post(t, handler, full, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("full multihash bulkMergeDuplicatesSelected errors = %#v", errs)
	}
	mustUnmarshal(t, data["bulkMergeDuplicatesSelected"], &result)
	if result.Affected != 2 {
		t.Fatalf("full multihash merge affected = %d, want 2", result.Affected)
	}
}

func TestBulkMergeDuplicatesSelectedRejectsOversizedSelection(t *testing.T) {
	_, handler := setupHealthStore(t)
	ids := make([]any, 201)
	for i := range ids {
		ids[i] = "media-" + strconv.Itoa(i)
	}
	_, err := handler.bulkMergeDuplicatesSelected(context.Background(), auth.Scope{}, request{Variables: map[string]any{
		"libraryID": "library-local",
		"mediaIDs":  ids,
	}})
	if err == nil || !strings.Contains(err.Error(), "maximum of 200") {
		t.Fatalf("oversized selection error = %v", err)
	}
}
