package graph

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// Library Health Dashboard bulk operations, the two not covered by
// graph_health_test.go's bulk-merge slice: bulkRescanUnmatched (enqueues a scan
// job over the Library's roots, idempotent by library+root dedupe) and the
// DESTRUCTIVE bulkTrashBroken (soft-deletes the broken set through the audited
// Trash path — invariant #6, recoverable, never hard-deleted). Both are
// Admin-Session gated like every other Health field (graph.go ~898).
//
// These tests build their fixtures only through public store APIs:
//   - a broken Media: import a file, then re-import the SAME path with a
//     DIFFERENT content fingerprint. upsertMediaFileTx marks the prior file's
//     missing_at_ms, leaving the original Media with no available file (the
//     "broken" dimension predicate) while the second import is a distinct,
//     healthy Media.
//   - an unmatched-rescan target: seed a single root on library-local (the dev
//     seed gives it none), so bulkRescanUnmatched has a root to enqueue.

// bulkOpsOpenAdmin seeds the admin password and opens the Admin Session window
// for a manager session. Uniquely prefixed so it never collides with a sibling
// file's helpers in this package.
func bulkOpsOpenAdmin(t *testing.T, handler *Handler) {
	t.Helper()
	if _, errs := post(t, handler,
		`{"query":"mutation V($password:String!){ verifyAdminSession(password:$password){ active } }","variables":{"password":"correct horse battery"}}`,
		authorizeManager); len(errs) != 0 {
		t.Fatalf("admin re-auth errors = %#v", errs)
	}
}

// bulkOpsImport imports one Media file into a Library and returns its Media ID.
func bulkOpsImport(t *testing.T, st *store.Store, libraryID, title, path, fingerprint, oshash string) store.MediaID {
	t.Helper()
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:           store.LibraryID(libraryID),
		Path:                path,
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

// TestBulkRescanUnmatchedAdminGatedEnqueuesAndIsIdempotent verifies the gate,
// the enqueue effect (a scan job per root, read back from the job queue), and
// the library+root dedupe (a second rescan does not double-enqueue).
//
// feature:library-health
// feature:unmatched-queue
func TestBulkRescanUnmatchedAdminGatedEnqueuesAndIsIdempotent(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	if err := st.SetAdminPassword(ctx, "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// The dev seed gives library-local no roots; bulkRescanUnmatched needs at
	// least one. Seed exactly one for a deterministic affected count.
	if err := st.SetLibraryRoots(ctx, "library-local", []string{"/media/local"}); err != nil {
		t.Fatalf("SetLibraryRoots: %v", err)
	}
	// An unmatched item (title never identified) is the rescan target backlog;
	// the resolver enqueues a root scan regardless, but seed one so the
	// fixture matches the operation's intent.
	_ = bulkOpsImport(t, st, "library-local", "lonely", "/media/local/lonely.mp4", "sha256:rescan-lonely", "aaaa000000000051")

	handler := newTestHandlerWithStore(st)

	rescan := `{"query":"mutation R($libraryID:String!){ bulkRescanUnmatched(libraryID:$libraryID){ affected } }","variables":{"libraryID":"library-local"}}`

	// Gate: without an Admin Session the mutation is rejected.
	_, errs := post(t, handler, rescan, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("pre-admin bulkRescanUnmatched errors = %#v, want admin gate", errs)
	}

	// Baseline: no queued jobs before the first rescan.
	baseQueued, err := st.ListJobs(ctx, store.JobStatusQueued)
	if err != nil {
		t.Fatalf("baseline ListJobs: %v", err)
	}
	if len(baseQueued) != 0 {
		t.Fatalf("baseline queued jobs = %d, want 0", len(baseQueued))
	}

	bulkOpsOpenAdmin(t, handler)

	// With the window open: one root => one job enqueued.
	data, errs := post(t, handler, rescan, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("bulkRescanUnmatched errors = %#v", errs)
	}
	var first BulkOpResult
	mustUnmarshal(t, data["bulkRescanUnmatched"], &first)
	if first.Affected != 1 {
		t.Fatalf("rescan affected = %d, want 1 (one root)", first.Affected)
	}

	// Effect read-back: exactly one queued scan job now exists, for our root.
	queued, err := st.ListJobs(ctx, store.JobStatusQueued)
	if err != nil {
		t.Fatalf("post-rescan ListJobs: %v", err)
	}
	if len(queued) != 1 {
		t.Fatalf("queued jobs after rescan = %d, want 1", len(queued))
	}
	if queued[0].Kind != "scan" {
		t.Errorf("enqueued job kind = %q, want scan", queued[0].Kind)
	}
	if !strings.Contains(string(queued[0].Payload), "library-local") ||
		!strings.Contains(string(queued[0].Payload), "/media/local") {
		t.Errorf("enqueued job payload = %s, want library-local + root", queued[0].Payload)
	}

	// Idempotency: a second rescan dedupes by library+root (the job is still
	// queued), so the affected count is unchanged and no second job is queued.
	data, errs = post(t, handler, rescan, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("second bulkRescanUnmatched errors = %#v", errs)
	}
	var second BulkOpResult
	mustUnmarshal(t, data["bulkRescanUnmatched"], &second)
	if second.Affected != 1 {
		t.Fatalf("second rescan affected = %d, want 1", second.Affected)
	}
	requeued, err := st.ListJobs(ctx, store.JobStatusQueued)
	if err != nil {
		t.Fatalf("post-second-rescan ListJobs: %v", err)
	}
	if len(requeued) != 1 {
		t.Errorf("queued jobs after second rescan = %d, want 1 (deduped)", len(requeued))
	}
}

// TestBulkTrashBrokenAdminGatedTrashesAndIsIdempotent verifies the gate, the
// effect (the broken Media is soft-deleted into the audited trash, leaving the
// healthy listing intact), and — because the op is DESTRUCTIVE — that a second
// bulkTrashBroken is a safe no-op.
//
// feature:library-health
func TestBulkTrashBrokenAdminGatedTrashesAndIsIdempotent(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	if err := st.SetAdminPassword(ctx, "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// Seed a broken Media: import the file, then re-import the SAME path with a
	// different fingerprint. The path-matching prior file gets missing_at_ms
	// set, so brokenID's only file is gone (the "broken" dimension); the
	// re-import is a distinct, healthy Media that must NOT be trashed.
	brokenID := bulkOpsImport(t, st, "library-local", "broken", "/media/local/broken.mp4", "sha256:broken-v1", "bbbb000000000061")
	healthyID := bulkOpsImport(t, st, "library-local", "broken-replacement", "/media/local/broken.mp4", "sha256:broken-v2", "bbbb000000000062")
	if healthyID == brokenID {
		t.Fatalf("re-import collapsed onto the broken Media (%s); fixture is wrong", brokenID)
	}

	handler := newTestHandlerWithStore(st)

	trashBroken := `{"query":"mutation T($libraryID:String!){ bulkTrashBroken(libraryID:$libraryID){ affected } }","variables":{"libraryID":"library-local"}}`
	localFilter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}

	// Gate: without an Admin Session the destructive mutation is rejected.
	_, errs := post(t, handler, trashBroken, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("pre-admin bulkTrashBroken errors = %#v, want admin gate", errs)
	}

	// Nothing trashed before the gate is open.
	preTrash, err := st.TrashedMediaList(ctx, localFilter)
	if err != nil {
		t.Fatalf("pre TrashedMediaList: %v", err)
	}
	if len(preTrash) != 0 {
		t.Fatalf("pre-op trash = %d entries, want 0", len(preTrash))
	}

	bulkOpsOpenAdmin(t, handler)

	// Confirm the broken set is exactly the one broken Media before trashing.
	brokenItems, err := st.LibraryHealthItems(ctx, "library-local", store.HealthBroken, localFilter, 0)
	if err != nil {
		t.Fatalf("LibraryHealthItems(broken): %v", err)
	}
	foundBroken := false
	for _, it := range brokenItems {
		if it.MediaID == brokenID {
			foundBroken = true
		}
		if it.MediaID == healthyID {
			t.Fatalf("healthy re-import %s appeared in the broken set", healthyID)
		}
	}
	if !foundBroken {
		t.Fatalf("broken Media %s not in the broken dimension; fixture is wrong (items=%#v)", brokenID, brokenItems)
	}

	// Trash the broken set.
	data, errs := post(t, handler, trashBroken, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("bulkTrashBroken errors = %#v", errs)
	}
	var trashed BulkOpResult
	mustUnmarshal(t, data["bulkTrashBroken"], &trashed)
	if trashed.Affected != len(brokenItems) || trashed.Affected < 1 {
		t.Fatalf("trashBroken affected = %d, want %d (the broken set)", trashed.Affected, len(brokenItems))
	}

	// Effect read-back: the broken Media is now recoverable in the audited
	// trash (invariant #6 — soft-deleted, not hard-deleted).
	postTrash, err := st.TrashedMediaList(ctx, localFilter)
	if err != nil {
		t.Fatalf("post TrashedMediaList: %v", err)
	}
	brokenInTrash := false
	for _, e := range postTrash {
		if e.ID == brokenID {
			brokenInTrash = true
		}
		if e.ID == healthyID {
			t.Errorf("healthy re-import %s was trashed — only the broken set should be", healthyID)
		}
	}
	if !brokenInTrash {
		t.Errorf("broken Media %s not in trash — bulkTrashBroken must route through Trash, recoverable", brokenID)
	}

	// And it has left the live broken listing.
	stillBroken, err := st.LibraryHealthItems(ctx, "library-local", store.HealthBroken, localFilter, 0)
	if err != nil {
		t.Fatalf("post LibraryHealthItems(broken): %v", err)
	}
	for _, it := range stillBroken {
		if it.MediaID == brokenID {
			t.Errorf("broken Media %s still in the live broken listing after trash", brokenID)
		}
	}

	// Idempotency (DESTRUCTIVE): a second bulkTrashBroken is a safe no-op —
	// the only broken item is already trashed, so nothing remains to trash and
	// the trash contents are unchanged.
	data, errs = post(t, handler, trashBroken, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("second bulkTrashBroken errors = %#v", errs)
	}
	var resend BulkOpResult
	mustUnmarshal(t, data["bulkTrashBroken"], &resend)
	if resend.Affected != 0 {
		t.Errorf("second bulkTrashBroken affected = %d, want 0 (already trashed)", resend.Affected)
	}
	afterResend, err := st.TrashedMediaList(ctx, localFilter)
	if err != nil {
		t.Fatalf("post-resend TrashedMediaList: %v", err)
	}
	if len(afterResend) != len(postTrash) {
		t.Errorf("trash count changed on re-send: %d -> %d, want stable no-op", len(postTrash), len(afterResend))
	}
}
