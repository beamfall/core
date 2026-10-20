package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSetMediaFieldsRoundTripsAllFieldTypes(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	when := time.Date(2020, 7, 4, 12, 0, 0, 0, time.UTC)
	fields := []MediaField{
		{Name: "class", Type: EntityFieldText, Text: "flight"},
		{Name: "score", Type: EntityFieldNumber, Number: 8.5},
		{Name: "verified", Type: EntityFieldFlag, Flag: true},
		{Name: "logged-at", Type: EntityFieldDate, Date: when},
		{Name: "homepage", Type: EntityFieldURL, Text: "https://example.test"},
	}
	if err := st.SetMediaFields(ctx, "media-alpha", fields); err != nil {
		t.Fatal(err)
	}

	got, err := st.MediaFields(ctx, "media-alpha", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("fields = %d, want 5", len(got))
	}
	byName := map[string]MediaField{}
	for _, field := range got {
		byName[field.Name] = field
	}
	if byName["class"].Text != "flight" || byName["class"].Type != EntityFieldText {
		t.Fatalf("class = %#v", byName["class"])
	}
	if byName["score"].Number != 8.5 {
		t.Fatalf("score = %#v", byName["score"])
	}
	if !byName["verified"].Flag {
		t.Fatalf("verified = %#v", byName["verified"])
	}
	if !byName["logged-at"].Date.Equal(when) {
		t.Fatalf("logged-at = %#v", byName["logged-at"])
	}
	if byName["homepage"].Text != "https://example.test" || byName["homepage"].Type != EntityFieldURL {
		t.Fatalf("homepage = %#v", byName["homepage"])
	}

	// Sorted by name for a deterministic shape.
	for i := 1; i < len(got); i++ {
		if got[i-1].Name > got[i].Name {
			t.Fatalf("fields not sorted: %q before %q", got[i-1].Name, got[i].Name)
		}
	}

	// Re-set fully replaces: the old fields are gone.
	if err := st.SetMediaFields(ctx, "media-alpha", []MediaField{
		{Name: "class", Type: EntityFieldText, Text: "orbit"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err = st.MediaFields(ctx, "media-alpha", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "class" || got[0].Text != "orbit" {
		t.Fatalf("after replace = %#v", got)
	}
}

func TestSetMediaFieldsValidation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	cases := []struct {
		name   string
		id     MediaID
		fields []MediaField
		want   string
	}{
		{"missing media ID", "", nil, "media ID is required"},
		{"unknown media", "media-ghost", nil, "media not found"},
		{"unnamed field", "media-alpha", []MediaField{{Type: EntityFieldText}}, "field name is required"},
		{"duplicate field", "media-alpha", []MediaField{
			{Name: "class", Type: EntityFieldText, Text: "a"},
			{Name: "class", Type: EntityFieldText, Text: "b"},
		}, "duplicate media field"},
		{"unknown type", "media-alpha", []MediaField{{Name: "class", Type: "blob"}}, "unknown media field type"},
		{"zero date", "media-alpha", []MediaField{{Name: "logged-at", Type: EntityFieldDate}}, "requires a value"},
	}
	for _, c := range cases {
		err := st.SetMediaFields(ctx, c.id, c.fields)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

func TestUpsertMediaFieldsPreservesUnnamedFields(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if err := st.SetMediaFields(ctx, "media-alpha", []MediaField{
		{Name: "class", Type: EntityFieldText, Text: "flight"},
		{Name: "score", Type: EntityFieldNumber, Number: 8.5},
	}); err != nil {
		t.Fatal(err)
	}

	// Upsert replaces only the named field; "score" survives.
	if err := st.UpsertMediaFields(ctx, "media-alpha", []MediaField{
		{Name: "class", Type: EntityFieldText, Text: "orbit"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := st.MediaFields(ctx, "media-alpha", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("fields = %#v, want class + score", got)
	}
	if got[0].Name != "class" || got[0].Text != "orbit" {
		t.Fatalf("class = %#v, want orbit", got[0])
	}
	if got[1].Name != "score" || got[1].Number != 8.5 {
		t.Fatalf("score = %#v, want preserved 8.5", got[1])
	}

	// A field whose type changes across writes leaves no stale row in the
	// old type's side table.
	if err := st.UpsertMediaFields(ctx, "media-alpha", []MediaField{
		{Name: "score", Type: EntityFieldText, Text: "high"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err = st.MediaFields(ctx, "media-alpha", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Name != "score" || got[1].Type != EntityFieldText || got[1].Text != "high" {
		t.Fatalf("after type change = %#v", got)
	}
}

func TestUpsertMediaFieldsValidation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	cases := []struct {
		name   string
		id     MediaID
		fields []MediaField
		want   string
	}{
		{"missing media ID", "", []MediaField{{Name: "class", Type: EntityFieldText}}, "media ID is required"},
		{"unknown media", "media-ghost", []MediaField{{Name: "class", Type: EntityFieldText}}, "media not found"},
		{"unnamed field", "media-alpha", []MediaField{{Type: EntityFieldText}}, "field name is required"},
		{"unknown type", "media-alpha", []MediaField{{Name: "class", Type: "blob"}}, "unknown media field type"},
	}
	for _, c := range cases {
		err := st.UpsertMediaFields(ctx, c.id, c.fields)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}

	// No fields is a no-op, not an error.
	if err := st.UpsertMediaFields(ctx, "media-alpha", nil); err != nil {
		t.Fatalf("empty upsert: %v", err)
	}
}

func TestMediaFieldProvenanceTracksWinningSource(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	field := MediaField{Name: "sequence", Type: EntityFieldNumber, Number: 1}
	if err := st.UpsertMediaFields(ctx, "media-alpha", []MediaField{field}); err != nil {
		t.Fatal(err)
	}

	got, err := st.MediaFieldProvenance(ctx, "media-alpha", field.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got != MutationSourceScan {
		t.Fatalf("scan provenance = %q, want %q", got, MutationSourceScan)
	}

	field.Number = 2
	if err := st.UpsertMediaFieldsWithSource(ctx, "media-alpha", []MediaField{field}, MutationSourceOperator); err != nil {
		t.Fatal(err)
	}
	got, err = st.MediaFieldProvenance(ctx, "media-alpha", field.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got != MutationSourceOperator {
		t.Fatalf("operator provenance = %q, want %q", got, MutationSourceOperator)
	}
}

func TestInsertMediaFieldsIfAbsentNeverClobbersIdentified(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	// Parse-hook first scan: class is absent, so it is inserted.
	if err := st.InsertMediaFieldsIfAbsent(ctx, "media-alpha", []MediaField{
		{Name: "class", Type: EntityFieldText, Text: "episode"},
	}); err != nil {
		t.Fatal(err)
	}

	// Identification overwrites with the authoritative value.
	if err := st.UpsertMediaFields(ctx, "media-alpha", []MediaField{
		{Name: "class", Type: EntityFieldText, Text: "movie"},
	}); err != nil {
		t.Fatal(err)
	}

	// Re-scan parse-hook re-runs: class now exists, so it is skipped — the
	// identified value survives.
	if err := st.InsertMediaFieldsIfAbsent(ctx, "media-alpha", []MediaField{
		{Name: "class", Type: EntityFieldText, Text: "episode"},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := st.MediaFields(ctx, "media-alpha", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "class" || got[0].Text != "movie" {
		t.Fatalf("class = %#v, want identified movie to survive", got)
	}

	// A brand-new field IS inserted (and an already-present one in the same
	// call is still skipped).
	if err := st.InsertMediaFieldsIfAbsent(ctx, "media-alpha", []MediaField{
		{Name: "class", Type: EntityFieldText, Text: "episode"},
		{Name: "code", Type: EntityFieldText, Text: "ABC-123"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err = st.MediaFields(ctx, "media-alpha", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]MediaField{}
	for _, field := range got {
		byName[field.Name] = field
	}
	if byName["class"].Text != "movie" {
		t.Fatalf("class = %#v, want movie preserved", byName["class"])
	}
	if byName["code"].Text != "ABC-123" {
		t.Fatalf("code = %#v, want new field inserted", byName["code"])
	}
}

func TestInsertMediaFieldsIfAbsentValidation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	cases := []struct {
		name   string
		id     MediaID
		fields []MediaField
		want   string
	}{
		{"missing media ID", "", []MediaField{{Name: "class", Type: EntityFieldText}}, "media ID is required"},
		{"unknown media", "media-ghost", []MediaField{{Name: "class", Type: EntityFieldText}}, "media not found"},
		{"unknown type", "media-alpha", []MediaField{{Name: "class", Type: "blob"}}, "unknown media field type"},
	}
	for _, c := range cases {
		err := st.InsertMediaFieldsIfAbsent(ctx, c.id, c.fields)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}

	// No fields is a no-op, not an error.
	if err := st.InsertMediaFieldsIfAbsent(ctx, "media-alpha", nil); err != nil {
		t.Fatalf("empty insert-if-absent: %v", err)
	}
}

func TestMediaFieldsRevealFiltering(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	// media-archive lives in the restricted library-archive (dev seed).
	if err := st.SetMediaFields(ctx, "media-archive", []MediaField{
		{Name: "class", Type: EntityFieldText, Text: "flight"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := st.MediaFields(ctx, "media-archive", LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("hidden media's fields readable without reveal")
	}
	got, err := st.MediaFields(ctx, "media-archive", LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "flight" {
		t.Fatalf("revealed fields = %#v", got)
	}

	// A Library allowlist that excludes the Media's Library hides it too.
	scoped := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}, Revealed: true}
	if _, err := st.MediaFields(ctx, "media-archive", scoped); err == nil {
		t.Fatal("out-of-scope media's fields readable")
	}
}

// TestMediaLackingFieldInLibraryReturnsOnlyUnmarked seeds a Music Library with
// two audio tracks (one carrying the hydration marker, one not) plus a non-audio
// file, and asserts the backfill stale-set query returns exactly the unmarked
// audio Media — never the marked one and never the wrong class.
func TestMediaLackingFieldInLibraryReturnsOnlyUnmarked(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Music", false, "music", "profile-default")
	if err != nil {
		t.Fatal(err)
	}

	marked, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: lib.ID, Path: "/music/marked.flac", Title: "Marked", Container: "flac", Codec: "flac",
		Class: MediaClassAudio, ContentFingerprint: "fp-marked",
	})
	if err != nil {
		t.Fatal(err)
	}
	unmarked, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: lib.ID, Path: "/music/unmarked.flac", Title: "Unmarked", Container: "flac", Codec: "flac",
		Class: MediaClassAudio, ContentFingerprint: "fp-unmarked",
	})
	if err != nil {
		t.Fatal(err)
	}
	// A non-audio file in the same Library must never appear in the audio set,
	// even though it also lacks the marker.
	if _, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: lib.ID, Path: "/music/cover.jpg", Title: "Cover", Container: "jpg", Codec: "mjpeg",
		Class: MediaClassImage, ContentFingerprint: "fp-cover",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertMediaFieldsIfAbsent(ctx, marked.ID, []MediaField{
		{Name: "music-hydrated", Type: EntityFieldFlag, Flag: true},
	}); err != nil {
		t.Fatal(err)
	}

	stale, err := st.MediaLackingFieldInLibrary(ctx, lib.ID, MediaClassAudio, "music-hydrated")
	if err != nil {
		t.Fatalf("MediaLackingFieldInLibrary: %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("stale set size = %d, want 1 (only the unmarked audio file): %v", len(stale), stale)
	}
	if _, ok := stale[unmarked.ID]; !ok {
		t.Errorf("stale set missing the unmarked audio Media %q", unmarked.ID)
	}
	if _, ok := stale[marked.ID]; ok {
		t.Errorf("stale set wrongly includes the already-marked Media %q", marked.ID)
	}
}
