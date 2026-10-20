package graph

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestLibraryHealthDashboardAdminGated is the Console vertical slice for the
// Library Health Dashboard: the five-dimension counts and the bulk
// operations are Admin-Session gated, the dimensions count the right Media, and
// a bulk-merge routes through the audited Trash path (invariant #6 — the merged
// duplicate is recoverable, never hard-deleted).
//
// feature:library-health
// feature:unmatched-queue
// feature:dedupe-review
func TestLibraryHealthDashboardAdminGated(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	if err := st.SetAdminPassword(ctx, "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// Seed two duplicate pairs (shared oshash per pair) and an unmatched item into
	// library-local, which the manager's adult Profile can see.
	importHealth := func(id, path, oshash string) store.MediaID {
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID:           "library-local",
			Path:                path,
			Title:               id,
			Container:           "mp4",
			Codec:               "h264",
			ContentFingerprint:  "sha256:" + id,
			DurationSecondsHint: 100,
			OSHash:              oshash,
		})
		if err != nil {
			t.Fatalf("ImportMediaFile(%s): %v", id, err)
		}
		return media.ID
	}
	dupA := importHealth("dupa", "/m/dupa.mp4", "ffff000000000001")
	dupB := importHealth("dupb", "/m/dupb.mp4", "ffff000000000001")
	otherDupA := importHealth("otherdupa", "/m/otherdupa.mp4", "ffff000000000002")
	otherDupB := importHealth("otherdupb", "/m/otherdupb.mp4", "ffff000000000002")
	_ = importHealth("lonely", "/m/lonely.mp4", "ffff000000000009")

	handler := newTestHandlerWithStore(st)

	// Admin gate: before re-auth, the dashboard is denied.
	_, errs := post(t, handler, `{"query":"query { libraryHealth { libraryID counts } }"}`, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("pre-admin libraryHealth errors = %#v, want admin gate", errs)
	}

	// Open the Admin Session window.
	if _, errs := post(t, handler,
		`{"query":"mutation V($password:String!){ verifyAdminSession(password:$password){ active } }","variables":{"password":"correct horse battery"}}`,
		authorizeManager); len(errs) != 0 {
		t.Fatalf("admin re-auth errors = %#v", errs)
	}

	// Counts: library-local has at least the duplicate pairs (4) and our
	// unmatched items present.
	data, errs := post(t, handler, `{"query":"query { libraryHealth { libraryID title counts } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("libraryHealth errors = %#v", errs)
	}
	var health []LibraryHealthInfo
	mustUnmarshal(t, data["libraryHealth"], &health)
	var local *LibraryHealthInfo
	for i := range health {
		if health[i].LibraryID == "library-local" {
			local = &health[i]
		}
	}
	if local == nil {
		t.Fatal("library-local missing from libraryHealth")
	}
	if local.Counts["duplicate"] < 4 {
		t.Errorf("duplicate count = %d, want >=4", local.Counts["duplicate"])
	}
	if _, ok := local.Counts["unmatched"]; !ok {
		t.Error("counts missing the unmatched dimension key")
	}

	// Drill-down: the duplicate-group resolver returns the pair, survivor first.
	data, errs = post(t, handler,
		`{"query":"query Q($libraryID:String!){ libraryDuplicateGroups(libraryID:$libraryID){ items { mediaID } } }","variables":{"libraryID":"library-local"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("libraryDuplicateGroups errors = %#v", errs)
	}
	var groups []DuplicateGroupInfo
	mustUnmarshal(t, data["libraryDuplicateGroups"], &groups)
	if len(groups) != 2 || len(groups[0].Items) != 2 || len(groups[1].Items) != 2 {
		t.Fatalf("groups = %#v, want two pairs", groups)
	}
	survivor := groups[0].Items[0].MediaID

	// Bulk-merge: keep the survivor, trash the redundant copy through Trash.
	data, errs = post(t, handler,
		`{"query":"mutation M($libraryID:String!,$keepMediaID:String!){ bulkMergeDuplicates(libraryID:$libraryID,keepMediaID:$keepMediaID){ affected } }","variables":{"libraryID":"library-local","keepMediaID":"`+survivor+`"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("bulkMergeDuplicates errors = %#v", errs)
	}
	var merged BulkOpResult
	mustUnmarshal(t, data["bulkMergeDuplicates"], &merged)
	if merged.Affected != 1 {
		t.Fatalf("merge affected = %d, want 1", merged.Affected)
	}

	// The trashed duplicate is recoverable (invariant #6): it appears in the
	// audited trash list, and it is the one that was NOT the survivor.
	trash, err := st.TrashedMediaList(ctx, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}})
	if err != nil {
		t.Fatalf("TrashedMediaList: %v", err)
	}
	trashedDup := false
	for _, e := range trash {
		if string(e.ID) != survivor && (e.ID == dupA || e.ID == dupB) {
			trashedDup = true
		}
	}
	if !trashedDup {
		t.Error("merged duplicate not in trash — must be recoverable, not hard-deleted")
	}

	// After the merge the first duplicate pair is resolved, but the independent
	// duplicate cluster remains untouched.
	data, errs = post(t, handler,
		`{"query":"query Q($libraryID:String!){ libraryDuplicateGroups(libraryID:$libraryID){ items { mediaID } } }","variables":{"libraryID":"library-local"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("post-merge libraryDuplicateGroups errors = %#v", errs)
	}
	mustUnmarshal(t, data["libraryDuplicateGroups"], &groups)
	if len(groups) != 1 || len(groups[0].Items) != 2 {
		t.Fatalf("post-merge groups = %#v, want the independent pair", groups)
	}
	remaining := map[string]bool{}
	for _, item := range groups[0].Items {
		remaining[item.MediaID] = true
	}
	if !remaining[string(otherDupA)] || !remaining[string(otherDupB)] {
		t.Errorf("post-merge groups = %#v, want independent pair %s/%s", groups, otherDupA, otherDupB)
	}
}
