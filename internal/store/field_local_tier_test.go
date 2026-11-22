package store

import (
	"context"
	"testing"
)

// feature:field-locks

// TestLocalTierLocksUntilReleased (CORE-0361): values a sidecar supplies land
// at the local tier, which an identification cannot overwrite and the lock
// listing reports as locked. Releasing the tier keeps every value but hands
// the field back to the scan tier, so the next identification fills it.
func TestLocalTierLocksUntilReleased(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/local-tier.mp4", Title: "local-tier",
		Container: "mp4", Codec: "h264", ContentFingerprint: "fp-local-tier",
	})
	if err != nil {
		t.Fatal(err)
	}
	title, sortTitle := "Sidecar Title", "Sidecar Sort"
	if err := st.UpdateMediaMetadata(ctx, media.ID, MediaMetadata{Title: &title, SortTitle: &sortTitle, Source: MutationSourceLocal}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertMediaFieldsWithSource(ctx, media.ID, []MediaField{{Name: "tagline", Type: EntityFieldText, Text: "From the sidecar"}}, MutationSourceLocal); err != nil {
		t.Fatal(err)
	}

	identified := "Identified Title"
	if err := st.UpdateMediaMetadata(ctx, media.ID, MediaMetadata{Title: &identified, Source: MutationSourceOperator}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertMediaFieldsWithSource(ctx, media.ID, []MediaField{{Name: "tagline", Type: EntityFieldText, Text: "Identified"}}, MutationSourceOperator); err != nil {
		t.Fatal(err)
	}
	assertLocalTier(t, st, media.ID, title, "From the sidecar", true)

	// A manual edit still outranks the sidecar.
	manual := "Manual Title"
	if err := st.UpdateMediaMetadata(ctx, media.ID, MediaMetadata{Title: &manual, Source: MutationSourceManual}); err != nil {
		t.Fatal(err)
	}
	if got := provenanceSource(t, st, media.ID, "title"); got != "manual" {
		t.Fatalf("title provenance after manual edit = %q, want manual", got)
	}
	// The release keeps the manual lock; the sidecar's fields go to the scan tier.
	if err := st.ReleaseMediaFieldSource(ctx, media.ID, MutationSourceLocal); err != nil {
		t.Fatal(err)
	}
	if got := provenanceSource(t, st, media.ID, "title"); got != "manual" {
		t.Fatalf("title provenance after release = %q, want manual", got)
	}
	for _, field := range []string{"sortTitle", "tagline"} {
		if got := provenanceSource(t, st, media.ID, field); got != "scan" {
			t.Fatalf("%s provenance after release = %q, want scan", field, got)
		}
	}
	assertLocalTier(t, st, media.ID, manual, "From the sidecar", false)

	if err := st.UpsertMediaFieldsWithSource(ctx, media.ID, []MediaField{{Name: "tagline", Type: EntityFieldText, Text: "Identified"}}, MutationSourceOperator); err != nil {
		t.Fatal(err)
	}
	assertLocalTier(t, st, media.ID, manual, "Identified", false)

	for _, source := range []MutationSource{MutationSourceManual, MutationSourceScan} {
		if err := st.ReleaseMediaFieldSource(ctx, media.ID, source); err == nil {
			t.Fatalf("release of %s tier accepted", source)
		}
	}
	if err := st.ReleaseMediaFieldSource(ctx, "", MutationSourceLocal); err == nil {
		t.Fatal("release without a Media ID accepted")
	}
}

func assertLocalTier(t *testing.T, st *Store, id MediaID, wantTitle, wantTagline string, taglineLocked bool) {
	t.Helper()
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	stored, err := st.Media(ctx, id, filter)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Title != wantTitle {
		t.Fatalf("title = %q, want %q", stored.Title, wantTitle)
	}
	fields, err := st.MediaFields(ctx, id, filter)
	if err != nil {
		t.Fatal(err)
	}
	tagline := ""
	for _, field := range fields {
		if field.Name == "tagline" {
			tagline = field.Text
		}
	}
	if tagline != wantTagline {
		t.Fatalf("tagline = %q, want %q", tagline, wantTagline)
	}
	locks, err := st.MediaFieldLocks(ctx, id, filter)
	if err != nil {
		t.Fatal(err)
	}
	locked := false
	for _, lock := range locks {
		if lock.Field == "tagline" {
			locked = lock.Locked
		}
	}
	if locked != taglineLocked {
		t.Fatalf("tagline locked = %v, want %v (locks %+v)", locked, taglineLocked, locks)
	}
}

// TestSourceRankValuesMatchGo keeps the inline SQL rank table in lockstep with
// metadataSourceRank, local tier included.
func TestSourceRankValuesMatchGo(t *testing.T) {
	st := openSeeded(t)
	rows, err := st.catalogR.QueryContext(context.Background(), sourceRankValues())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var src string
		var rank int
		if err := rows.Scan(&src, &rank); err != nil {
			t.Fatal(err)
		}
		if want := metadataSourceRank(MutationSource(src)); rank != want {
			t.Fatalf("SQL rank(%s) = %d, Go rank = %d", src, rank, want)
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen != 6 {
		t.Fatalf("SQL rank rows = %d, want 6", seen)
	}
}
