package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// CORE-1715: an admin edit writes at the manual source and so locks the
// fields it touches; a later identification or refresh (operator source)
// lands only on unlocked fields, and clearing a lock lets it land again.
func TestManualEditLocksFieldsAgainstIdentification(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	filter := store.LibraryFilter{Revealed: true, AllLibraries: true}

	if err := st.UpdateMediaMetadata(ctx, "media-alpha", store.MediaMetadata{
		Title: ptr("Admin Title"), SortTitle: ptr("Title, Admin"), Source: store.MutationSourceManual,
	}); err != nil {
		t.Fatalf("manual edit: %v", err)
	}
	locks := lockMap(t, st, ctx, filter)
	if !locks["title"] || !locks["sortTitle"] || locks["summary"] || locks["year"] {
		t.Fatalf("locks after manual edit = %#v, want title and sortTitle only", locks)
	}

	// An identification-rank write: the locked title and sort title survive,
	// the unlocked summary and year land.
	if err := st.UpdateMediaMetadata(ctx, "media-alpha", store.MediaMetadata{
		Title: ptr("Provider Title"), SortTitle: ptr("Provider Sort"), Summary: ptr("Provider summary."), Year: ptr(2001),
	}); err != nil {
		t.Fatalf("operator write: %v", err)
	}
	media, err := st.Media(ctx, "media-alpha", filter)
	if err != nil {
		t.Fatal(err)
	}
	if media.Title != "Admin Title" || media.Summary != "Provider summary." || media.Year != 2001 {
		t.Fatalf("media after operator write = %#v", media)
	}
	if sortTitle, err := st.MediaSortTitle(ctx, "media-alpha", filter); err != nil || sortTitle != "Title, Admin" {
		t.Fatalf("sort title after operator write = %q, %v", sortTitle, err)
	}
	if locks := lockMap(t, st, ctx, filter); locks["summary"] || locks["year"] {
		t.Fatalf("an operator write must not lock: %#v", locks)
	}

	// Unlocking the title lets the next identification replace it.
	if _, err := st.SetMediaFieldLock(ctx, "media-alpha", "title", false, filter); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateMediaMetadata(ctx, "media-alpha", store.MediaMetadata{Title: ptr("Provider Title")}); err != nil {
		t.Fatal(err)
	}
	if media, _ := st.Media(ctx, "media-alpha", filter); media.Title != "Provider Title" {
		t.Fatalf("title after unlock = %q, want the provider value", media.Title)
	}

	// Locking the summary pins the provider value against the next write.
	if _, err := st.SetMediaFieldLock(ctx, "media-alpha", "summary", true, filter); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateMediaMetadata(ctx, "media-alpha", store.MediaMetadata{Summary: ptr("Later summary.")}); err != nil {
		t.Fatal(err)
	}
	if media, _ := st.Media(ctx, "media-alpha", filter); media.Summary != "Provider summary." {
		t.Fatalf("locked summary = %q, want it kept", media.Summary)
	}
}

// CORE-1768: a provider-supplied sort title lands at identification rank. A
// later identification without one retracts it, but never an admin's value,
// locked or unlocked.
func TestProviderSortTitleFollowsIdentificationAndSparesAdminValue(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	filter := store.LibraryFilter{Revealed: true, AllLibraries: true}
	sortTitle := func() string {
		t.Helper()
		got, err := st.MediaSortTitle(ctx, "media-alpha", filter)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	identifyWith := func(value string) {
		t.Helper()
		if err := st.UpdateMediaMetadata(ctx, "media-alpha", store.MediaMetadata{SortTitle: ptr(value), Source: store.MutationSourceOperator}); err != nil {
			t.Fatal(err)
		}
	}

	// No sort title yet: a retraction is a no-op and records nothing.
	identifyWith("")
	if got := sortTitle(); got != "" {
		t.Fatalf("sort title after empty identification = %q", got)
	}
	if source, err := st.MediaFieldProvenance(ctx, "media-alpha", "sortTitle"); err != nil || source != "" {
		t.Fatalf("provenance after empty identification = %q, %v; want none", source, err)
	}

	identifyWith("Provider Key")
	if got := sortTitle(); got != "Provider Key" {
		t.Fatalf("provider sort title = %q", got)
	}
	if locks := lockMap(t, st, ctx, filter); locks["sortTitle"] {
		t.Fatal("a provider sort title must not lock the field")
	}
	identifyWith("")
	if got := sortTitle(); got != "" {
		t.Fatalf("sort title after a candidate without one = %q, want it retracted", got)
	}

	// An admin value survives a provider retraction while locked and after
	// the lock is cleared; an unlocked one still yields to a provider value.
	if err := st.UpdateMediaMetadata(ctx, "media-alpha", store.MediaMetadata{SortTitle: ptr("Admin Key"), Source: store.MutationSourceManual}); err != nil {
		t.Fatal(err)
	}
	identifyWith("")
	identifyWith("Provider Key")
	if got := sortTitle(); got != "Admin Key" {
		t.Fatalf("locked sort title = %q, want the admin value", got)
	}
	if _, err := st.SetMediaFieldLock(ctx, "media-alpha", "sortTitle", false, filter); err != nil {
		t.Fatal(err)
	}
	identifyWith("")
	if got := sortTitle(); got != "Admin Key" {
		t.Fatalf("unlocked admin sort title after retraction = %q, want it kept", got)
	}
	identifyWith("Provider Key")
	if got := sortTitle(); got != "Provider Key" {
		t.Fatalf("unlocked sort title after provider value = %q", got)
	}
}

func TestSortTitleRoundTripAndClear(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	filter := store.LibraryFilter{Revealed: true, AllLibraries: true}

	if got, err := st.MediaSortTitle(ctx, "media-alpha", filter); err != nil || got != "" {
		t.Fatalf("initial sort title = %q, %v", got, err)
	}
	if err := st.UpdateMediaMetadata(ctx, "media-alpha", store.MediaMetadata{SortTitle: ptr("  Sort Key  "), Source: store.MutationSourceManual}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.MediaSortTitle(ctx, "media-alpha", filter); got != "Sort Key" {
		t.Fatalf("sort title = %q, want trimmed value", got)
	}
	if err := st.UpdateMediaMetadata(ctx, "media-alpha", store.MediaMetadata{SortTitle: ptr(""), Source: store.MutationSourceManual}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.MediaSortTitle(ctx, "media-alpha", filter); got != "" {
		t.Fatalf("cleared sort title = %q", got)
	}
	if _, err := st.MediaSortTitle(ctx, "media-missing", filter); !errors.As(err, new(store.ValidationError)) {
		t.Fatalf("unknown media sort title err = %v, want a validation error", err)
	}
}

func TestFieldLocksCoverArtworkAndHeldPluginFields(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	filter := store.LibraryFilter{Revealed: true, AllLibraries: true}

	locks, err := st.SetMediaFieldLock(ctx, "media-alpha", store.ArtworkLockField("poster"), true, filter)
	if err != nil {
		t.Fatal(err)
	}
	if got := locksByField(locks); !got["artworkPoster"] || got["artworkBackdrop"] {
		t.Fatalf("artwork locks = %#v", got)
	}
	if source, err := st.MediaFieldProvenance(ctx, "media-alpha", "artworkPoster"); err != nil || source != store.MutationSourceManual {
		t.Fatalf("poster lock provenance = %q, %v", source, err)
	}

	// A plugin field is lockable only once the item holds it.
	if _, err := st.SetMediaFieldLock(ctx, "media-alpha", "label", true, filter); !errors.As(err, new(store.ValidationError)) {
		t.Fatalf("lock of an absent plugin field err = %v, want a validation error", err)
	}
	if err := st.UpsertMediaFieldsWithSource(ctx, "media-alpha", []store.MediaField{{Name: "label", Type: store.EntityFieldText, Text: "scan value"}}, store.MutationSourceScan); err != nil {
		t.Fatal(err)
	}
	locks, err = st.SetMediaFieldLock(ctx, "media-alpha", "label", true, filter)
	if err != nil {
		t.Fatal(err)
	}
	if !locksByField(locks)["label"] {
		t.Fatalf("plugin field lock missing from %#v", locks)
	}
	if err := st.UpsertMediaFieldsWithSource(ctx, "media-alpha", []store.MediaField{{Name: "label", Type: store.EntityFieldText, Text: "provider value"}}, store.MutationSourceOperator); err != nil {
		t.Fatal(err)
	}
	fields, err := st.MediaFields(ctx, "media-alpha", filter)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range fields {
		if field.Name == "label" && field.Text != "scan value" {
			t.Fatalf("locked plugin field = %q, want the scan value kept", field.Text)
		}
	}
}

func TestLibraryBrowseSortsBySortTitle(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	filter := store.LibraryFilter{Revealed: true, AllLibraries: true}
	for _, item := range []struct{ path, title string }{
		{"/sort/a.mp4", "Alpha Item"},
		{"/sort/b.mp4", "Bravo Item"},
		{"/sort/c.mp4", "Charlie Item"},
	} {
		if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: "library-local", Path: item.path, Title: item.title,
			Container: "mp4", Codec: "h264", ContentFingerprint: "fp" + item.path,
		}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := st.LibraryBrowse(ctx, filter, store.LibraryBrowseQuery{LibraryID: "library-local", Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	var alphaID store.MediaID
	for _, item := range before.Items {
		if item.Title == "Alpha Item" {
			alphaID = item.ID
		}
	}
	if alphaID == "" {
		t.Fatal("browse lacks the imported item")
	}
	if err := st.UpdateMediaMetadata(ctx, alphaID, store.MediaMetadata{SortTitle: ptr("Zulu"), Source: store.MutationSourceManual}); err != nil {
		t.Fatal(err)
	}
	after, err := st.LibraryBrowse(ctx, filter, store.LibraryBrowseQuery{LibraryID: "library-local", Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	last := after.Items[len(after.Items)-1]
	if last.ID != alphaID || last.Title != "Alpha Item" {
		t.Fatalf("last browse item = %#v, want the sort-titled item with its own title", last)
	}
	if final := after.Letters[len(after.Letters)-1]; final.Letter != "Z" {
		t.Fatalf("last letter = %#v, want Z from the sort title", final)
	}
}

func lockMap(t *testing.T, st *store.Store, ctx context.Context, filter store.LibraryFilter) map[string]bool {
	t.Helper()
	locks, err := st.MediaFieldLocks(ctx, "media-alpha", filter)
	if err != nil {
		t.Fatal(err)
	}
	return locksByField(locks)
}

func locksByField(locks []store.MediaFieldLock) map[string]bool {
	out := map[string]bool{}
	for _, lock := range locks {
		out[lock.Field] = lock.Locked
	}
	return out
}
