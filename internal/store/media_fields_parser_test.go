package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func parserFieldsByName(t *testing.T, st *Store, id MediaID) map[string]MediaField {
	t.Helper()
	fields, err := st.MediaFields(context.Background(), id, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]MediaField{}
	for _, field := range fields {
		byName[field.Name] = field
	}
	return byName
}

func parserMarker(t *testing.T, st *Store, id MediaID, name string) int {
	t.Helper()
	var marker int
	if err := st.catalogR.QueryRowContext(context.Background(),
		"SELECT parser_owned FROM media_field_provenance WHERE media_id = ? AND field = ?", id, name).Scan(&marker); err != nil {
		t.Fatalf("parser_owned(%s): %v", name, err)
	}
	return marker
}

// TestRefreshParserMediaFieldsReplacesAndRemovesOnlyParserFields (CORE-2554).
func TestRefreshParserMediaFieldsReplacesAndRemovesOnlyParserFields(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const id MediaID = "media-alpha"
	text := func(name, value string) MediaField { return MediaField{Name: name, Type: EntityFieldText, Text: value} }
	if err := st.RefreshParserMediaFields(ctx, id, ParserFieldRefresh{Fields: []MediaField{
		text("class", "movie"), text("note", "first"), text("gone", "x"), text("curated", "parsed"), text("pinned", "parsed"),
	}}); err != nil {
		t.Fatal(err)
	}
	if got := parserMarker(t, st, id, "class"); got != parserOwnedStable {
		t.Fatalf("class marker = %d, want %d", got, parserOwnedStable)
	}
	// Identification (operator) and a manual edit take two fields over, and
	// any other provenance write clears the parser marker.
	if err := st.UpsertMediaFieldsWithSource(ctx, id, []MediaField{text("curated", "identified")}, MutationSourceOperator); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertMediaFieldsWithSource(ctx, id, []MediaField{text("pinned", "manual")}, MutationSourceManual); err != nil {
		t.Fatal(err)
	}
	if got := parserMarker(t, st, id, "curated"); got != parserOwnedNone {
		t.Fatalf("operator write left parser marker %d", got)
	}
	// A removal tombstones "note"'s parsed value.
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO media_field_rejection(media_id, field, field_type, value, rejected_at_ms)
		VALUES (?, 'note', 'text', 'first', 1)`, id); err != nil {
		t.Fatal(err)
	}

	if err := st.RefreshParserMediaFields(ctx, id, ParserFieldRefresh{Fields: []MediaField{
		text("class", "episode"), text("note", "first"), text("curated", "reparsed"), text("pinned", "reparsed"),
	}}); err != nil {
		t.Fatal(err)
	}
	fields := parserFieldsByName(t, st, id)
	if fields["class"].Text != "episode" {
		t.Fatalf("class = %#v, want replaced episode", fields["class"])
	}
	if _, ok := fields["gone"]; ok {
		t.Fatalf("a field the parser stopped emitting survived: %#v", fields["gone"])
	}
	if _, ok := fields["note"]; ok {
		t.Fatalf("tombstoned parsed value came back: %#v", fields["note"])
	}
	if fields["curated"].Text != "identified" || fields["pinned"].Text != "manual" {
		t.Fatalf("curated/pinned = %q/%q, want identified/manual", fields["curated"].Text, fields["pinned"].Text)
	}
	if source, err := st.MediaFieldProvenance(ctx, id, "gone"); err != nil || source != "" {
		t.Fatalf("removed field provenance = %q (%v), want none", source, err)
	}

	// Removing everything the parser owned leaves the curated fields alone.
	if err := st.RefreshParserMediaFields(ctx, id, ParserFieldRefresh{}); err != nil {
		t.Fatal(err)
	}
	fields = parserFieldsByName(t, st, id)
	if _, ok := fields["class"]; ok {
		t.Fatalf("class survived a parse with no class: %#v", fields["class"])
	}
	if fields["curated"].Text != "identified" || fields["pinned"].Text != "manual" {
		t.Fatalf("curated/pinned = %q/%q after empty parse", fields["curated"].Text, fields["pinned"].Text)
	}
}

// TestRefreshParserMediaFieldsHoldsProbeAndLegacyFieldsUntilAuthoritative:
// a parse without the probe leaves probe-dependent and pre-marker fields; an
// authoritative parse settles both (CORE-2554).
func TestRefreshParserMediaFieldsHoldsProbeAndLegacyFieldsUntilAuthoritative(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const id MediaID = "media-alpha"
	class := func(value string) MediaField { return MediaField{Name: "class", Type: EntityFieldText, Text: value} }
	sequence := MediaField{Name: "sequence", Type: EntityFieldNumber, Number: 3}

	// A field marked legacy (the migration marks only pre-marker scan-tier
	// classes; the store treats any legacy marker the same way).
	if err := st.UpsertMediaFields(ctx, id, []MediaField{sequence}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx,
		"UPDATE media_field_provenance SET parser_owned = ? WHERE media_id = ? AND field = 'sequence'", parserOwnedLegacy, id); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "alpha.mkv")
	if err := os.WriteFile(path, []byte("alpha"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(ctx, id, path); err != nil {
		t.Fatal(err)
	}
	paths, err := st.LibraryFilePathsWithLegacyParserFields(ctx, "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := paths[path]; !ok || len(paths) != 1 {
		t.Fatalf("legacy file paths = %v, want only %s", paths, path)
	}

	if err := st.RefreshParserMediaFields(ctx, id, ParserFieldRefresh{
		Fields:         []MediaField{class("video-track")},
		ProbeDependent: map[string]bool{"class": true},
		Authoritative:  true,
	}); err != nil {
		t.Fatal(err)
	}
	// The probed parse does not emit the legacy sequence, so it is removed.
	// Class is probe-dependent.
	if field, ok := parserFieldsByName(t, st, id)["sequence"]; ok {
		t.Fatalf("unemitted legacy sequence survived an authoritative parse: %#v", field)
	}
	if got := parserMarker(t, st, id, "class"); got != parserOwnedProbe {
		t.Fatalf("class marker = %d, want %d", got, parserOwnedProbe)
	}
	if paths, err := st.LibraryFilePathsWithLegacyParserFields(ctx, "library-local"); err != nil || len(paths) != 0 {
		t.Fatalf("legacy paths after settling = %v (%v), want none", paths, err)
	}

	// Without the probe the parse disagrees, and the probe-dependent class stays.
	if err := st.RefreshParserMediaFields(ctx, id, ParserFieldRefresh{Fields: []MediaField{class("audio-track")}}); err != nil {
		t.Fatal(err)
	}
	if got := parserFieldsByName(t, st, id)["class"].Text; got != "video-track" {
		t.Fatalf("unprobed parse class = %q, want video-track kept", got)
	}
	if err := st.RefreshParserMediaFields(ctx, id, ParserFieldRefresh{}); err != nil {
		t.Fatal(err)
	}
	if got := parserFieldsByName(t, st, id)["class"].Text; got != "video-track" {
		t.Fatalf("unprobed empty parse removed the probe-dependent class (%q)", got)
	}
	if err := st.RefreshParserMediaFields(ctx, id, ParserFieldRefresh{Fields: []MediaField{class("audio-track")}, Authoritative: true}); err != nil {
		t.Fatal(err)
	}
	if got := parserFieldsByName(t, st, id)["class"].Text; got != "audio-track" {
		t.Fatalf("authoritative parse class = %q, want audio-track", got)
	}
}

// TestRefreshParserMediaFieldsClaimsLegacyFieldOnAuthoritativeParse: a
// pre-marker class is replaced only by a probed parse that emits it (CORE-2554).
func TestRefreshParserMediaFieldsClaimsLegacyFieldOnAuthoritativeParse(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const id MediaID = "media-alpha"
	class := func(value string) MediaField { return MediaField{Name: "class", Type: EntityFieldText, Text: value} }
	if err := st.UpsertMediaFields(ctx, id, []MediaField{class("movie")}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx,
		"UPDATE media_field_provenance SET parser_owned = ? WHERE media_id = ? AND field = 'class'", parserOwnedLegacy, id); err != nil {
		t.Fatal(err)
	}
	if err := st.RefreshParserMediaFields(ctx, id, ParserFieldRefresh{Fields: []MediaField{class("episode")}}); err != nil {
		t.Fatal(err)
	}
	if got := parserFieldsByName(t, st, id)["class"].Text; got != "movie" {
		t.Fatalf("unprobed parse class = %q, want legacy movie kept", got)
	}
	if err := st.RefreshParserMediaFields(ctx, id, ParserFieldRefresh{Fields: []MediaField{class("episode")}, Authoritative: true}); err != nil {
		t.Fatal(err)
	}
	if got := parserFieldsByName(t, st, id)["class"].Text; got != "episode" {
		t.Fatalf("authoritative parse class = %q, want episode", got)
	}
	if got := parserMarker(t, st, id, "class"); got != parserOwnedStable {
		t.Fatalf("claimed class marker = %d, want %d", got, parserOwnedStable)
	}
}

// TestRefreshParserMediaFieldsRemovesLegacyClassOnAuthoritativeParse: a
// pre-marker movie class goes when a probed parse no longer emits a class,
// so a rescan takes an extra out of the movies (CORE-2554).
func TestRefreshParserMediaFieldsRemovesLegacyClassOnAuthoritativeParse(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const id MediaID = "media-alpha"
	if err := st.UpsertMediaFields(ctx, id, []MediaField{{Name: "class", Type: EntityFieldText, Text: "movie"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx,
		"UPDATE media_field_provenance SET parser_owned = ? WHERE media_id = ? AND field = 'class'", parserOwnedLegacy, id); err != nil {
		t.Fatal(err)
	}
	if err := st.RefreshParserMediaFields(ctx, id, ParserFieldRefresh{}); err != nil {
		t.Fatal(err)
	}
	if got := parserFieldsByName(t, st, id)["class"].Text; got != "movie" {
		t.Fatalf("unprobed parse class = %q, want legacy movie kept", got)
	}
	if err := st.RefreshParserMediaFields(ctx, id, ParserFieldRefresh{Authoritative: true}); err != nil {
		t.Fatal(err)
	}
	if field, ok := parserFieldsByName(t, st, id)["class"]; ok {
		t.Fatalf("legacy class survived an authoritative parse with no class: %#v", field)
	}
	if source, err := st.MediaFieldProvenance(ctx, id, "class"); err != nil || source != "" {
		t.Fatalf("removed class provenance = %q (%v), want none", source, err)
	}
}

// TestRefreshParserMediaFieldsKeepsProbeDecidedAbsence: a field a probed parse
// left out, where the parse without the probe emits it, stays out until a
// probed parse says otherwise; an unprobed parse cannot add it back, whether
// the probed parse removed it or never had it (CORE-2554).
func TestRefreshParserMediaFieldsKeepsProbeDecidedAbsence(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const id MediaID = "media-alpha"
	class := func(value string) MediaField { return MediaField{Name: "class", Type: EntityFieldText, Text: value} }
	fallback := MediaField{Name: "note", Type: EntityFieldText, Text: "fallback"}
	unprobed := ParserFieldRefresh{Fields: []MediaField{class("audio-track"), fallback}}
	assertAbsent := func(step, name string) {
		t.Helper()
		if field, ok := parserFieldsByName(t, st, id)[name]; ok {
			t.Fatalf("%s: %s = %#v, want absent", step, name, field)
		}
	}

	if err := st.RefreshParserMediaFields(ctx, id, ParserFieldRefresh{
		Fields: []MediaField{class("video-track")}, ProbeDependent: map[string]bool{"class": true}, Authoritative: true,
	}); err != nil {
		t.Fatal(err)
	}
	// The probed parse now emits no class, nor the never-held note, though
	// the unprobed parse emits both.
	probedNone := ParserFieldRefresh{ProbeAbsent: map[string]bool{"class": true, "note": true}, Authoritative: true}
	if err := st.RefreshParserMediaFields(ctx, id, probedNone); err != nil {
		t.Fatal(err)
	}
	assertAbsent("probed parse without class", "class")
	if got := parserMarker(t, st, id, "class"); got != parserOwnedProbeAbsent {
		t.Fatalf("class marker = %d, want %d", got, parserOwnedProbeAbsent)
	}
	for pass := 0; pass < 2; pass++ {
		if err := st.RefreshParserMediaFields(ctx, id, unprobed); err != nil {
			t.Fatal(err)
		}
		assertAbsent("unprobed parse", "class")
		assertAbsent("unprobed parse", "note")
	}
	// A probed parse emitting the class takes it back.
	if err := st.RefreshParserMediaFields(ctx, id, ParserFieldRefresh{
		Fields: []MediaField{class("video-track")}, ProbeDependent: map[string]bool{"class": true},
		ProbeAbsent: map[string]bool{"note": true}, Authoritative: true,
	}); err != nil {
		t.Fatal(err)
	}
	if got := parserFieldsByName(t, st, id)["class"].Text; got != "video-track" {
		t.Fatalf("probed parse class = %q, want video-track", got)
	}
	// Once the absence no longer depends on the probe, the record goes and an
	// unprobed parse adds the field again.
	if err := st.RefreshParserMediaFields(ctx, id, ParserFieldRefresh{
		Fields: []MediaField{class("video-track")}, ProbeDependent: map[string]bool{"class": true}, Authoritative: true,
	}); err != nil {
		t.Fatal(err)
	}
	if source, err := st.MediaFieldProvenance(ctx, id, "note"); err != nil || source != "" {
		t.Fatalf("settled absence provenance = %q (%v), want none", source, err)
	}
	if err := st.RefreshParserMediaFields(ctx, id, ParserFieldRefresh{Fields: []MediaField{fallback}}); err != nil {
		t.Fatal(err)
	}
	if got := parserFieldsByName(t, st, id)["note"].Text; got != "fallback" {
		t.Fatalf("note after settled absence = %q, want fallback", got)
	}
	// Another writer's value is never turned into an absence.
	if err := st.UpsertMediaFieldsWithSource(ctx, id, []MediaField{{Name: "kept", Type: EntityFieldText, Text: "x"}}, MutationSourceOperator); err != nil {
		t.Fatal(err)
	}
	if err := st.RefreshParserMediaFields(ctx, id, ParserFieldRefresh{
		Fields: []MediaField{class("video-track")}, ProbeAbsent: map[string]bool{"kept": true}, Authoritative: true,
	}); err != nil {
		t.Fatal(err)
	}
	if got := parserFieldsByName(t, st, id)["kept"].Text; got != "x" {
		t.Fatalf("operator field = %q, want x", got)
	}
}

// TestRefreshParserMediaFieldsYieldsAbsenceToApprovedValue: a value another
// writer puts under a probe-decided absence is not the parser's, so an
// authoritative parse emitting the field leaves it alone (CORE-2554).
func TestRefreshParserMediaFieldsYieldsAbsenceToApprovedValue(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const id MediaID = "media-alpha"
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	mood := func(value string) MediaField { return MediaField{Name: "mood", Type: EntityFieldText, Text: value} }
	absent := ParserFieldRefresh{ProbeAbsent: map[string]bool{"mood": true}, Authoritative: true}
	emitting := ParserFieldRefresh{Fields: []MediaField{mood("parsed")}, ProbeDependent: map[string]bool{"mood": true}, Authoritative: true}
	assertMood := func(step, want string) {
		t.Helper()
		if got := parserFieldsByName(t, st, id)["mood"]; got.Text != want {
			t.Fatalf("%s: mood = %#v, want %q", step, got, want)
		}
	}

	// Approval clears the absence.
	if err := st.RefreshParserMediaFields(ctx, id, absent); err != nil {
		t.Fatal(err)
	}
	if got := parserMarker(t, st, id, "mood"); got != parserOwnedProbeAbsent {
		t.Fatalf("mood marker = %d, want %d", got, parserOwnedProbeAbsent)
	}
	if err := st.ProposePendingAITag(ctx, PendingAITag{
		ID: "pt-mood", MediaID: id, TagKind: "mood", Value: "noir", ProposedBy: "ai",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.PromotePendingAITag(ctx, "pt-mood", filter); err != nil {
		t.Fatal(err)
	}
	if err := st.RefreshParserMediaFields(ctx, id, emitting); err != nil {
		t.Fatal(err)
	}
	assertMood("approved then probed parse", "noir")
	if err := st.RefreshParserMediaFields(ctx, id, absent); err != nil {
		t.Fatal(err)
	}
	assertMood("approved then probed parse without mood", "noir")

	// A writer that records no provenance does not leave the parser owning it.
	clear, err := st.catalogW.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := deleteMediaFieldValueTx(ctx, clear, id, "mood"); err != nil {
		t.Fatal(err)
	}
	if err := clear.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := st.RefreshParserMediaFields(ctx, id, absent); err != nil {
		t.Fatal(err)
	}
	if got := parserMarker(t, st, id, "mood"); got != parserOwnedProbeAbsent {
		t.Fatalf("mood marker = %d, want %d", got, parserOwnedProbeAbsent)
	}
	tx, err := st.catalogW.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertMediaFields(ctx, tx, id, []MediaField{mood("restored")}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := st.RefreshParserMediaFields(ctx, id, emitting); err != nil {
		t.Fatal(err)
	}
	assertMood("restored then probed parse", "restored")
}
