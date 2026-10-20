//go:build io_e2e

package ioe2e

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/store"
)

// TestDestructiveLifecycleJourney is the core-owned destructive-lifecycle E2E
// (hard invariant 6; ADR-0064/0078; CORE-AUDIT-16). It proves the four legs the
// destructive-lifecycle-e2e scenario names, each against the real GraphQL wire
// (graph.Handler) or the audited store mutations that back it:
//
//  1. trash-enabled delete → restore round-trips a Media item through the
//     recoverable server-side Trash (restoreMedia brings it back).
//  2. the delete and the restore each emit an audit record with {op, actor,
//     reversible}: action=media.trash/media.restore, actor=session/device, and
//     reversible=true (routed through Trash, recoverable via Restore).
//  3. restore collision: a managed-path restore whose original path is now
//     occupied is REJECTED (never a silent overwrite of the live occupant).
//  4. trash-disabled destructive-operation rejection: the autonomous purge
//     sweep skips while Trash is disabled (destruction is not autonomous), yet
//     the human-confirmed purge still proceeds — destruction only "unless
//     explicitly confirmed".
//
// scenario:destructive-lifecycle-e2e
// io:fs-trash-move
func TestDestructiveLifecycleJourney(t *testing.T) {
	t.Run("trash_enabled_delete_restore_and_audit", testTrashEnabledDeleteRestoreAndAudit)
	t.Run("restore_collision_rejected", testRestoreCollisionRejected)
	t.Run("trash_disabled_autonomous_rejection", testTrashDisabledAutonomousRejection)
}

// audit record on the wire: {op=action, actor=session/device/profile, reversible}.
type journeyAuditEntry struct {
	Action     string `json:"action"`
	Reversible bool   `json:"reversible"`
	SessionID  string `json:"sessionID"`
	DeviceID   string `json:"deviceID"`
	ProfileID  string `json:"profileID"`
	TargetType string `json:"targetType"`
	TargetID   string `json:"targetID"`
}

// Leg 1 + 2: trash-enabled delete/restore over the wire, and the audit
// {op, actor, reversible} each destructive mutation writes.
func testTrashEnabledDeleteRestoreAndAudit(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := basicJourneyHandler(st)

	const mediaID = "media-alpha" // dev-seed Media in library-local (sqlite.go seed)

	// Audited delete: media-alpha lands in the recoverable Trash.
	trashed := postJourneyGraph(t, h, authorizeJourneyManager,
		`mutation($mediaID:String!){ trashMedia(mediaID:$mediaID){ id } }`,
		map[string]any{"mediaID": mediaID})
	trashed.mustHaveNoErrors(t)
	if !journeyTrashContains(t, trashed, "trashMedia", mediaID) {
		t.Fatalf("after trashMedia, trash does not contain %s: %s", mediaID, trashed.body)
	}

	// Audit shape for the delete: op=media.trash, actor set, reversible=true.
	trashAudit := latestAuditFor(t, h, store.ActivityMediaTrash, mediaID)
	assertAuditActor(t, trashAudit, "media.trash")
	if !trashAudit.Reversible {
		t.Fatalf("media.trash audit reversible=false, want true (Trash-routed delete is recoverable): %+v", trashAudit)
	}

	// Recoverable: restoreMedia pulls it back out of the Trash.
	restored := postJourneyGraph(t, h, authorizeJourneyManager,
		`mutation($mediaID:String!){ restoreMedia(mediaID:$mediaID){ id } }`,
		map[string]any{"mediaID": mediaID})
	restored.mustHaveNoErrors(t)
	if journeyTrashContains(t, restored, "restoreMedia", mediaID) {
		t.Fatalf("after restoreMedia, trash still contains %s: delete was not recoverable: %s", mediaID, restored.body)
	}

	// The restored Media is live again (visible on a catalog read).
	if _, err := st.Media(ctx, mediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}); err != nil {
		t.Fatalf("restored media not visible on a live read: %v", err)
	}

	// Audit shape for the restore: op=media.restore, actor set, reversible=true.
	restoreAudit := latestAuditFor(t, h, store.ActivityMediaRestore, mediaID)
	assertAuditActor(t, restoreAudit, "media.restore")
	if !restoreAudit.Reversible {
		t.Fatalf("media.restore audit reversible=false, want true: %+v", restoreAudit)
	}
}

// Leg 3: a managed-path restore whose original path is re-occupied is rejected;
// the live occupant is never silently overwritten (ADR-0078 restore semantics).
func testRestoreCollisionRejected(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	root := t.TempDir()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	clip := filepath.Join(root, "clip.mp4")
	if err := os.WriteFile(clip, []byte("original bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := basicJourneyHandler(st)

	// Audited delete-to-Trash of a managed file.
	del := postJourneyGraph(t, h, authorizeJourneyManager,
		`mutation($libraryID:String!,$path:String!){ deleteManagedPath(libraryID:$libraryID,path:$path){ trashID originalPath } }`,
		map[string]any{"libraryID": "library-local", "path": clip})
	trashID := del.trashID(t)
	if _, err := os.Lstat(clip); !os.IsNotExist(err) {
		t.Fatalf("deleteManagedPath left the original path in place: %v", err)
	}

	// A NEW live file now occupies the original path — the restore collision.
	if err := os.WriteFile(clip, []byte("new occupant bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Restore must be REJECTED, not silently overwrite the live occupant.
	restore := postJourneyGraph(t, h, authorizeJourneyManager,
		`mutation($libraryID:String!,$trashID:String!){ restoreManagedPath(libraryID:$libraryID,trashID:$trashID){ path } }`,
		map[string]any{"libraryID": "library-local", "trashID": trashID})
	if len(restore.Errors) == 0 {
		t.Fatalf("restoreManagedPath into an occupied path succeeded: silent overwrite: %s", restore.body)
	}

	// The live occupant's bytes are untouched — the collision never clobbered them.
	got, err := os.ReadFile(clip)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new occupant bytes" {
		t.Fatalf("collision overwrote the live occupant: bytes=%q", got)
	}
}

// Leg 4: with Trash disabled, the autonomous purge sweep skips (destruction is
// not autonomous), but the human-confirmed purge still proceeds — destructive
// operations are rejected "unless explicitly confirmed" (invariant 6; ADR-0078
// amendment). Human-confirmed trash also stays audited and recoverable meanwhile.
func testTrashDisabledAutonomousRejection(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true, TrashDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := basicJourneyHandler(st)

	const mediaID = "media-alpha"

	// Human-confirmed trash is still legal with Trash disabled (JBF-2 asymmetry):
	// it routes through the audited soft-delete, staying recoverable.
	trashed := postJourneyGraph(t, h, authorizeJourneyManager,
		`mutation($mediaID:String!){ trashMedia(mediaID:$mediaID){ id } }`,
		map[string]any{"mediaID": mediaID})
	trashed.mustHaveNoErrors(t)
	if !journeyTrashContains(t, trashed, "trashMedia", mediaID) {
		t.Fatalf("human-confirmed trashMedia did not route through audited Trash with Trash disabled: %s", trashed.body)
	}
	// The delete is audited even with Trash disabled.
	assertAuditActor(t, latestAuditFor(t, h, store.ActivityMediaTrash, mediaID), "media.trash")

	// Autonomous purge sweep is REJECTED (skipped) while Trash is disabled: no
	// unattended destruction, and the trashed item survives, still restorable.
	farFuture := time.Now().Add(2 * store.TrashRetention).UTC()
	purged, skipped, err := st.SweepPurgeTrashedBefore(ctx, farFuture)
	if !errors.Is(err, store.ErrTrashDisabledConfirmationRequired) || !skipped || purged != 0 || st.TrashDisabledSweepSkips() < 1 {
		t.Fatalf("autonomous purge with Trash disabled = purged %d skipped %v skips %d err %v, want confirmation-required skip", purged, skipped, st.TrashDisabledSweepSkips(), err)
	}
	survivors, err := st.TrashedMediaList(ctx, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}})
	if err != nil {
		t.Fatal(err)
	}
	if !trashedListContains(survivors, mediaID) {
		t.Fatalf("autonomous-skip path hard-deleted %s instead of retaining it: %#v", mediaID, survivors)
	}

	// The human-confirmed purge (the explicit-confirmation path the Console
	// fronts) DOES proceed with Trash disabled — rejection is only for the
	// autonomous sweep, not a confirmed operator purge.
	confirmed, err := st.PurgeTrashedBefore(ctx, farFuture)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed < 1 {
		t.Fatalf("human-confirmed purge with Trash disabled purged %d, want >=1", confirmed)
	}
}

// ── journey-local helpers ─────────────────────────────────────────────────────

// journeyTrashContains reports whether a trashMedia/restoreMedia mutation result
// (a []TrashEntry under the given field) lists the media id.
func journeyTrashContains(t *testing.T, resp journeyGraphResponse, field, mediaID string) bool {
	t.Helper()
	raw, ok := resp.Data[field]
	if !ok {
		t.Fatalf("%s missing from response data: %s", field, resp.body)
	}
	var entries []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("decode %s entries: %v\n%s", field, err, resp.body)
	}
	for _, e := range entries {
		if e.ID == mediaID {
			return true
		}
	}
	return false
}

func trashedListContains(list []store.TrashedMedia, mediaID string) bool {
	for _, e := range list {
		if string(e.ID) == mediaID {
			return true
		}
	}
	return false
}

// trashID decodes a deleteManagedPath result and returns its trashID.
func (r journeyGraphResponse) trashID(t *testing.T) string {
	t.Helper()
	r.mustHaveNoErrors(t)
	raw, ok := r.Data["deleteManagedPath"]
	if !ok {
		t.Fatalf("deleteManagedPath missing from response: %s", r.body)
	}
	var out struct {
		TrashID string `json:"trashID"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode deleteManagedPath: %v\n%s", err, r.body)
	}
	if out.TrashID == "" {
		t.Fatalf("deleteManagedPath returned empty trashID: %s", r.body)
	}
	return out.TrashID
}

// latestAuditFor reads the activity log over the wire and returns the newest
// audit entry matching action + targetID (the record the destructive mutation
// just wrote). The activityLog resolver returns entries newest-first, so the
// first match is the one this journey produced.
func latestAuditFor(t *testing.T, h *graph.Handler, action, targetID string) journeyAuditEntry {
	t.Helper()
	resp := postJourneyGraph(t, h, authorizeJourneyManager,
		`query{ activityLog{ action reversible sessionID deviceID profileID targetType targetID } }`, nil)
	resp.mustHaveNoErrors(t)
	raw, ok := resp.Data["activityLog"]
	if !ok {
		t.Fatalf("activityLog missing from response: %s", resp.body)
	}
	var entries []journeyAuditEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("decode activityLog: %v\n%s", err, resp.body)
	}
	for _, e := range entries {
		if e.Action == action && e.TargetID == targetID {
			return e
		}
	}
	t.Fatalf("no %s audit entry for target %s in activity log: %s", action, targetID, resp.body)
	return journeyAuditEntry{}
}

// assertAuditActor asserts the audit record names the acting operator — the
// {actor} half of {op, actor, reversible}. The manager journey acts as the
// dev-seeded Console session/device (authorizeJourneyManager).
func assertAuditActor(t *testing.T, entry journeyAuditEntry, wantAction string) {
	t.Helper()
	if entry.Action != wantAction {
		t.Fatalf("audit action = %q, want %q", entry.Action, wantAction)
	}
	if entry.SessionID != "session-manager" || entry.DeviceID != "device-mac" {
		t.Fatalf("audit actor = {session %q device %q}, want the manager operator {session-manager, device-mac}: %+v", entry.SessionID, entry.DeviceID, entry)
	}
}
