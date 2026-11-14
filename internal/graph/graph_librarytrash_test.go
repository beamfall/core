package graph

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// recordingLibraryObserver records the Library lifecycle notifications the
// graph layer sends, in order.
type recordingLibraryObserver struct {
	calls  []string
	purged []store.LibraryPurge
}

func (o *recordingLibraryObserver) LibraryCreated(_ context.Context, id store.LibraryID, _ []string) {
	o.calls = append(o.calls, "created:"+string(id))
}

func (o *recordingLibraryObserver) LibraryRootsChanged(_ context.Context, id store.LibraryID, _ []string) {
	o.calls = append(o.calls, "roots:"+string(id))
}

func (o *recordingLibraryObserver) LibraryDeleted(_ context.Context, id store.LibraryID) {
	o.calls = append(o.calls, "deleted:"+string(id))
}

func (o *recordingLibraryObserver) LibraryRestored(_ context.Context, id store.LibraryID) {
	o.calls = append(o.calls, "restored:"+string(id))
}

func (o *recordingLibraryObserver) LibraryPurged(_ context.Context, purge store.LibraryPurge) {
	o.calls = append(o.calls, "purged:"+string(purge.LibraryID))
	o.purged = append(o.purged, purge)
}

func libraryTrashHandler(t *testing.T) (*Handler, *store.Store, *recordingLibraryObserver) {
	t.Helper()
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	observer := &recordingLibraryObserver{}
	handler.libraryObserver = observer
	return handler, st, observer
}

func libraryIDRequest(id string) request {
	return request{Variables: map[string]any{"libraryID": id}}
}

func purgeLibraryRequest(id string, confirm any) request {
	variables := map[string]any{"libraryID": id}
	if confirm != nil {
		variables["confirm"] = confirm
	}
	return request{Variables: variables}
}

func activityActions(t *testing.T, st *store.Store, filter store.LibraryFilter) []string {
	t.Helper()
	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{Filter: filter})
	if err != nil {
		t.Fatal(err)
	}
	actions := make([]string, 0, len(entries))
	for _, entry := range entries {
		actions = append(actions, entry.Action+":"+entry.TargetID)
	}
	return actions
}

func mustVisible(t *testing.T, handler *Handler, scope auth.Scope, id store.LibraryID) bool {
	t.Helper()
	visible, err := handler.visibleLibrary(context.Background(), scope, id)
	if err != nil {
		t.Fatal(err)
	}
	return visible
}

func TestTrashedLibrariesListsADeletedLibrary(t *testing.T) {
	handler, st, observer := libraryTrashHandler(t)
	ctx := context.Background()
	scope := managerScope()

	listed, err := handler.trashedLibraries(ctx, scope)
	if err != nil || len(listed) != 0 {
		t.Fatalf("trashedLibraries before delete = %+v, %v; want empty", listed, err)
	}
	live, err := st.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		t.Fatal(err)
	}
	var local store.Library
	for _, library := range live {
		if library.ID == "library-local" {
			local = library
		}
	}

	if err := handler.deleteLibrary(ctx, scope, libraryIDRequest("library-local")); err != nil {
		t.Fatalf("deleteLibrary: %v", err)
	}
	if !slices.Equal(observer.calls, []string{"deleted:library-local"}) {
		t.Fatalf("observer calls after delete = %v, want the delete notification", observer.calls)
	}

	listed, err = handler.trashedLibraries(ctx, scope)
	if err != nil || len(listed) != 1 {
		t.Fatalf("trashedLibraries = %+v, %v; want one entry", listed, err)
	}
	entry := listed[0]
	if entry.ID != "library-local" || entry.Title != local.Title || entry.MediaCount == 0 {
		t.Fatalf("entry = %+v, want %q with its media counted", entry, local.Title)
	}
	trashedAt, err := time.Parse(time.RFC3339, entry.TrashedAt)
	if err != nil {
		t.Fatalf("trashedAt %q: %v", entry.TrashedAt, err)
	}
	purgeAfter, err := time.Parse(time.RFC3339, entry.PurgeAfter)
	if err != nil || !entry.Purgeable || !purgeAfter.Equal(trashedAt.Add(store.TrashRetention)) {
		t.Fatalf("purgeable %v purgeAfter %q (%v), want trashedAt + 30 days", entry.Purgeable, entry.PurgeAfter, err)
	}
}

func TestTrashedLibrariesReportsCustodialLibraryAsNotPurgeable(t *testing.T) {
	handler, st, _ := libraryTrashHandler(t)
	ctx := context.Background()
	if err := st.SetLibraryBacking(ctx, "library-local", true, false); err != nil {
		t.Fatal(err)
	}
	if err := handler.deleteLibrary(ctx, managerScope(), libraryIDRequest("library-local")); err != nil {
		t.Fatal(err)
	}
	listed, err := handler.trashedLibraries(ctx, managerScope())
	if err != nil || len(listed) != 1 || listed[0].Purgeable || listed[0].PurgeAfter != "" {
		t.Fatalf("trashedLibraries = %+v, %v; want one entry that is never purged", listed, err)
	}
	_, err = handler.purgeLibrary(ctx, managerScope(), purgeLibraryRequest("library-local", true))
	if err == nil {
		t.Fatal("purgeLibrary removed a Custodial library")
	}
	if listed, _ := handler.trashedLibraries(ctx, managerScope()); len(listed) != 1 {
		t.Fatalf("Custodial library left the trash after a refused purge: %+v", listed)
	}
}

func TestRestoreLibraryBringsItBackAuditsAndNotifies(t *testing.T) {
	handler, st, observer := libraryTrashHandler(t)
	ctx := context.Background()
	scope := managerScope()

	if err := handler.restoreLibrary(ctx, scope, libraryIDRequest("library-local")); err == nil {
		t.Fatal("restoreLibrary accepted a library that is not deleted")
	}
	if err := handler.restoreLibrary(ctx, scope, libraryIDRequest("")); err == nil {
		t.Fatal("restoreLibrary accepted an empty libraryID")
	}
	if len(observer.calls) != 0 {
		t.Fatalf("refused restores notified the observer: %v", observer.calls)
	}

	if err := handler.deleteLibrary(ctx, scope, libraryIDRequest("library-local")); err != nil {
		t.Fatal(err)
	}
	if mustVisible(t, handler, scope, "library-local") {
		t.Fatal("deleted library still visible")
	}
	if err := handler.restoreLibrary(ctx, scope, libraryIDRequest("library-local")); err != nil {
		t.Fatalf("restoreLibrary: %v", err)
	}

	if !mustVisible(t, handler, scope, "library-local") {
		t.Fatal("restored library not visible")
	}
	if _, err := st.Media(ctx, "media-alpha", scope.LibraryFilter()); err != nil {
		t.Fatalf("media of restored library unreadable: %v", err)
	}
	if listed, _ := handler.trashedLibraries(ctx, scope); len(listed) != 0 {
		t.Fatalf("trashedLibraries after restore = %+v, want empty", listed)
	}
	if !slices.Equal(observer.calls, []string{"deleted:library-local", "restored:library-local"}) {
		t.Fatalf("observer calls = %v, want delete then restore", observer.calls)
	}
	actions := activityActions(t, st, scope.LibraryFilter())
	if !slices.Contains(actions, store.ActivityLibraryRestore+":library-local") {
		t.Fatalf("activity log %v has no library.restore record", actions)
	}
}

func TestPurgeLibraryNeedsConfirmAndADeletedLibrary(t *testing.T) {
	handler, st, observer := libraryTrashHandler(t)
	ctx := context.Background()
	scope := managerScope()

	// A live Library is refused even with confirm: purge is the second step.
	if _, err := handler.purgeLibrary(ctx, scope, purgeLibraryRequest("library-local", true)); err == nil {
		t.Fatal("purgeLibrary removed a live library")
	}
	if !mustVisible(t, handler, scope, "library-local") {
		t.Fatal("refused purge hid the live library")
	}
	if err := handler.deleteLibrary(ctx, scope, libraryIDRequest("library-local")); err != nil {
		t.Fatal(err)
	}
	for name, confirm := range map[string]any{"absent": nil, "false": false} {
		_, err := handler.purgeLibrary(ctx, scope, purgeLibraryRequest("library-local", confirm))
		if err == nil || !strings.Contains(err.Error(), "confirm is required") {
			t.Fatalf("purgeLibrary with confirm %s = %v, want a confirm-required error", name, err)
		}
	}
	if _, err := handler.purgeLibrary(ctx, scope, purgeLibraryRequest("", true)); err == nil {
		t.Fatal("purgeLibrary accepted an empty libraryID")
	}
	if listed, _ := handler.trashedLibraries(ctx, scope); len(listed) != 1 {
		t.Fatalf("library left the trash after refused purges: %+v", listed)
	}
	if slices.Contains(observer.calls, "purged:library-local") {
		t.Fatalf("refused purges notified the observer: %v", observer.calls)
	}
	if _, err := st.Media(ctx, "media-alpha", scope.LibraryFilter()); err == nil {
		t.Fatal("media of a deleted library readable before restore")
	}
}

func TestPurgeLibraryReportsWhatItRemovedAuditsAndNotifies(t *testing.T) {
	handler, st, observer := libraryTrashHandler(t)
	ctx := context.Background()
	scope := managerScope()
	if err := handler.deleteLibrary(ctx, scope, libraryIDRequest("library-local")); err != nil {
		t.Fatal(err)
	}
	listed, err := handler.trashedLibraries(ctx, scope)
	if err != nil || len(listed) != 1 {
		t.Fatalf("trashedLibraries = %+v, %v; want one entry", listed, err)
	}
	mediaCount := listed[0].MediaCount

	result, err := handler.purgeLibrary(ctx, scope, purgeLibraryRequest("library-local", true))
	if err != nil {
		t.Fatalf("purgeLibrary: %v", err)
	}
	if result.LibraryID != "library-local" || result.Title == "" || result.MediaRemoved != mediaCount || mediaCount == 0 {
		t.Fatalf("result = %+v, want library-local with %d media removed", result, mediaCount)
	}
	if mustVisible(t, handler, scope, "library-local") {
		t.Fatal("purged library visible")
	}
	if listed, _ := handler.trashedLibraries(ctx, scope); len(listed) != 0 {
		t.Fatalf("trashedLibraries after purge = %+v, want empty", listed)
	}
	if len(observer.purged) != 1 || observer.purged[0].LibraryID != "library-local" || len(observer.purged[0].MediaIDs) != mediaCount {
		t.Fatalf("observer purge = %+v, want library-local with its media ids", observer.purged)
	}
	// The record must stay readable although the Library it names is gone.
	actions := activityActions(t, st, scope.LibraryFilter())
	if !slices.Contains(actions, store.ActivityLibraryPurge+":library-local") {
		t.Fatalf("activity log %v has no readable library.purge record", actions)
	}
	// A second purge finds nothing to remove.
	if _, err := handler.purgeLibrary(ctx, scope, purgeLibraryRequest("library-local", true)); err == nil {
		t.Fatal("second purgeLibrary succeeded")
	}
}

// A restricted Library in the trash is invisible to a scope that has not
// revealed it: it is not listed, cannot be restored or purged, and its purge
// record never names it to that scope.
func TestLibraryTrashHonoursTheRevealGate(t *testing.T) {
	handler, st, _ := libraryTrashHandler(t)
	ctx := context.Background()
	revealed := managerScope()
	hidden := auth.Scope{ProfileID: "profile-default", AllLibraries: true}
	private, err := st.CreateLibrary(ctx, "Private", true, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.deleteLibrary(ctx, revealed, libraryIDRequest(string(private.ID))); err != nil {
		t.Fatal(err)
	}

	if listed, err := handler.trashedLibraries(ctx, hidden); err != nil || len(listed) != 0 {
		t.Fatalf("unrevealed trashedLibraries = %+v, %v; want empty", listed, err)
	}
	if err := handler.restoreLibrary(ctx, hidden, libraryIDRequest(string(private.ID))); err == nil {
		t.Fatal("unrevealed scope restored a restricted library")
	}
	if _, err := handler.purgeLibrary(ctx, hidden, purgeLibraryRequest(string(private.ID), true)); err == nil {
		t.Fatal("unrevealed scope purged a restricted library")
	}
	if listed, _ := handler.trashedLibraries(ctx, revealed); len(listed) != 1 {
		t.Fatalf("revealed trashedLibraries = %+v, want the restricted library", listed)
	}

	if _, err := handler.purgeLibrary(ctx, revealed, purgeLibraryRequest(string(private.ID), true)); err != nil {
		t.Fatalf("revealed purgeLibrary: %v", err)
	}
	record := store.ActivityLibraryPurge + ":" + string(private.ID)
	if slices.Contains(activityActions(t, st, hidden.LibraryFilter()), record) {
		t.Fatal("purge record names a restricted library to an unrevealed scope")
	}
}
