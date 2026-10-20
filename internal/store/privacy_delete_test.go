package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestProfileFootprintDeleteResumesAfterCatalogFailure(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	if err := st.SetMediaFavorite(ctx, "profile-default", "media-alpha", true, filter); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSavedFilter(ctx, SavedFilter{
		ProfileID: "profile-default", LibraryID: "library-local", Name: "Delete me", FilterJSON: `{}`,
	}, time.Unix(100, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		CREATE TEMP TRIGGER fail_profile_catalog_delete
		BEFORE DELETE ON saved_filter
		BEGIN
		  SELECT RAISE(ABORT, 'injected catalog delete failure');
		END`); err != nil {
		t.Fatal(err)
	}
	activity := ActivityEntry{
		OperationID: "privacy-delete-test",
		OccurredAt:  time.Unix(101, 0).UTC(),
		Action:      ActivityMyDataDelete,
		ProfileID:   "profile-default",
		TargetType:  "all",
	}
	err := st.DeleteMyEngagementAudited(ctx, "profile-default", MyDataDeleteAll, "", filter, activity)
	if err == nil || !strings.Contains(err.Error(), "injected catalog delete failure") {
		t.Fatalf("delete err = %v", err)
	}
	if favorited, err := st.MediaFavorited(ctx, "profile-default", "media-alpha"); err != nil || favorited {
		t.Fatalf("favorite after events phase = %v, err=%v", favorited, err)
	}
	var pending int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM server_config WHERE key = ?`, profileDeleteIntentKey("profile-default")).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("pending intents = %d, want 1", pending)
	}
	if _, err := st.catalogW.ExecContext(ctx, `DROP TRIGGER fail_profile_catalog_delete`); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteMyEngagementAudited(ctx, "profile-default", MyDataDeleteAll, "", filter, ActivityEntry{
		OccurredAt: time.Unix(102, 0).UTC(), Action: ActivityMyDataDelete, ProfileID: "profile-default", TargetType: "all",
	}); err != nil {
		t.Fatalf("resume delete: %v", err)
	}
	filters, err := st.SavedFilters(ctx, "profile-default", "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if len(filters) != 0 {
		t.Fatalf("saved filters survived: %+v", filters)
	}
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM server_config WHERE key = ?`, profileDeleteIntentKey("profile-default")).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("pending intents after resume = %d", pending)
	}
	entries, err := st.ActivityEntries(ctx, ActivityQuery{Filter: filter, ProfileID: "profile-default", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if entry.Action == ActivityMyDataDelete {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("delete activity count = %d, want 1", count)
	}
}
