package store

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// feature:field-locks

// TestLocalTierLocksUntilReleased (CORE-0361): values a sidecar supplies land
// at the local tier, which an identification cannot overwrite and the lock
// listing reports as locked. Releasing the tier keeps every value but hands
// the field back to the scan tier, so the next identification fills it; a
// manual edit keeps its lock through both.
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
	if err := st.ApplyLocalMetadata(ctx, media.ID, LocalMetadata{
		Metadata:       MediaMetadata{Title: &title, SortTitle: &sortTitle},
		Fields:         []MediaField{{Name: "tagline", Type: EntityFieldText, Text: "From the sidecar"}},
		SignatureField: "sidecar-signature", Signature: "sig-1",
	}); err != nil {
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

	// A manual edit still outranks the sidecar, and a re-apply keeps it.
	manual := "Manual Title"
	if err := st.UpdateMediaMetadata(ctx, media.ID, MediaMetadata{Title: &manual, Source: MutationSourceManual}); err != nil {
		t.Fatal(err)
	}
	// ClearLocks on a sidecar parse is ignored: a sidecar never lifts a lock.
	if err := st.ApplyLocalMetadata(ctx, media.ID, LocalMetadata{
		Metadata:       MediaMetadata{Title: &title, SortTitle: &sortTitle, ClearLocks: []string{"title"}},
		Fields:         []MediaField{{Name: "tagline", Type: EntityFieldText, Text: "From the sidecar"}},
		SignatureField: "sidecar-signature", Signature: "sig-2",
	}); err != nil {
		t.Fatal(err)
	}
	if got := provenanceSource(t, st, media.ID, "title"); got != "manual" {
		t.Fatalf("title provenance after manual edit = %q, want manual", got)
	}
	if err := st.ReleaseLocalMetadata(ctx, media.ID, "sidecar-signature"); err != nil {
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
	if got := mediaFieldText(t, st, media.ID, "sidecar-signature"); got != "" {
		t.Fatalf("signature after release = %q, want cleared", got)
	}

	if err := st.UpsertMediaFieldsWithSource(ctx, media.ID, []MediaField{{Name: "tagline", Type: EntityFieldText, Text: "Identified"}}, MutationSourceOperator); err != nil {
		t.Fatal(err)
	}
	assertLocalTier(t, st, media.ID, manual, "Identified", false)

	if err := st.ReleaseLocalMetadata(ctx, "", "sidecar-signature"); err == nil {
		t.Fatal("release without a Media ID accepted")
	}
	if err := st.ApplyLocalMetadata(ctx, media.ID, LocalMetadata{SignatureField: "sidecar-signature"}); err == nil {
		t.Fatal("apply without a signature accepted")
	}
}

// TestApplyLocalMetadataDropsFieldsTheSidecarNoLongerStates (CORE-0361): a
// field the previous parse wrote and the new parse omits drops to the scan
// tier with its value, so it stops reading as locked.
func TestApplyLocalMetadataDropsFieldsTheSidecarNoLongerStates(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if err := st.ApplyLocalMetadata(ctx, "media-alpha", LocalMetadata{
		Fields:         []MediaField{{Name: "tagline", Type: EntityFieldText, Text: "From the sidecar"}},
		SignatureField: "sidecar-signature", Signature: "sig-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyLocalMetadata(ctx, "media-alpha", LocalMetadata{
		SignatureField: "sidecar-signature", Signature: "sig-2",
	}); err != nil {
		t.Fatal(err)
	}
	if got := provenanceSource(t, st, "media-alpha", "tagline"); got != "scan" {
		t.Fatalf("omitted field provenance = %q, want scan", got)
	}
	if got := mediaFieldText(t, st, "media-alpha", "tagline"); got != "From the sidecar" {
		t.Fatalf("omitted field value = %q, want kept", got)
	}
	if got := mediaFieldText(t, st, "media-alpha", "sidecar-signature"); got != "sig-2" {
		t.Fatalf("signature = %q, want sig-2", got)
	}
}

// TestApplyLocalMetadataOwnsOnlyLocalRelationRows (CORE-0361): a sidecar
// apply replaces only its own local rows of each relation it owns. A manual
// row survives a sidecar that omits it, a relation the sidecar now states as
// empty loses its last local row, a scraper upsert cannot take a local row
// over while an operator can, and a release hands the rows to an unowned
// writer without deleting them.
func TestApplyLocalMetadataOwnsOnlyLocalRelationRows(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	for _, id := range []EntityID{"entity-a", "entity-b", "entity-manual"} {
		if err := st.UpsertEntity(ctx, widgetEntity(id, "library-local")); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertEntityRelation(ctx, EntityRelation{FromEntityID: "entity-manual", Name: "member-of", ToMediaID: "media-alpha"}); err != nil {
		t.Fatal(err)
	}
	owned := []WrittenRelation{{Kind: "widget", Name: "member-of"}}
	apply := func(signature string, sources ...EntityID) {
		t.Helper()
		relations := make([]EntityRelation, 0, len(sources))
		for _, id := range sources {
			relations = append(relations, EntityRelation{FromEntityID: id, Name: "member-of", ToMediaID: "media-alpha"})
		}
		if err := st.ApplyLocalMetadata(ctx, "media-alpha", LocalMetadata{
			Relations: relations, OwnedRelations: owned,
			SignatureField: "sidecar-signature", Signature: signature,
		}); err != nil {
			t.Fatal(err)
		}
	}
	apply("sig-1", "entity-a", "entity-b")
	if got := relationWriters(t, st, "media-alpha", "member-of"); got != "entity-a=local,entity-b=local,entity-manual=" {
		t.Fatalf("after first apply = %q", got)
	}
	// A scraper re-link leaves a local row local; an operator takes it over.
	if err := st.UpsertEntityRelation(ctx, EntityRelation{FromEntityID: "entity-a", Name: "member-of", ToMediaID: "media-alpha", Writer: "scraper:demo/item"}); err != nil {
		t.Fatal(err)
	}
	if got := relationWriters(t, st, "media-alpha", "member-of"); got != "entity-a=local,entity-b=local,entity-manual=" {
		t.Fatalf("scraper took over a local row: %q", got)
	}
	apply("sig-2", "entity-b")
	if got := relationWriters(t, st, "media-alpha", "member-of"); got != "entity-b=local,entity-manual=" {
		t.Fatalf("after edit = %q, want entity-a removed and the manual row kept", got)
	}
	apply("sig-3")
	if got := relationWriters(t, st, "media-alpha", "member-of"); got != "entity-manual=" {
		t.Fatalf("after emptying = %q, want only the manual row", got)
	}
	apply("sig-4", "entity-a", "entity-manual")
	if got := relationWriters(t, st, "media-alpha", "member-of"); got != "entity-a=local,entity-manual=" {
		t.Fatalf("sidecar took over the manual row: %q", got)
	}
	if err := st.UpsertEntityRelation(ctx, EntityRelation{FromEntityID: "entity-a", Name: "member-of", ToMediaID: "media-alpha"}); err != nil {
		t.Fatal(err)
	}
	if got := relationWriters(t, st, "media-alpha", "member-of"); got != "entity-a=,entity-manual=" {
		t.Fatalf("operator re-link did not take the row over: %q", got)
	}
	apply("sig-5", "entity-b")
	if err := st.ReleaseLocalMetadata(ctx, "media-alpha", "sidecar-signature"); err != nil {
		t.Fatal(err)
	}
	if got := relationWriters(t, st, "media-alpha", "member-of"); got != "entity-a=,entity-b=local-released,entity-manual=" {
		t.Fatalf("after release = %q, want entity-b kept under an unowned writer", got)
	}
}

// TestRestoredSidecarReclaimsReleasedRows (CORE-0361 r1): a row a removed
// sidecar released goes back to the local writer when a restored sidecar
// lists it again, so a later edit that drops it removes it. A released row
// the restored sidecar does not list, and a scraper row, stay unowned.
func TestRestoredSidecarReclaimsReleasedRows(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	for _, id := range []EntityID{"entity-a", "entity-b", "entity-c"} {
		if err := st.UpsertEntity(ctx, widgetEntity(id, "library-local")); err != nil {
			t.Fatal(err)
		}
	}
	owned := []WrittenRelation{{Kind: "widget", Name: "member-of"}}
	apply := func(signature string, sources ...EntityID) {
		t.Helper()
		relations := make([]EntityRelation, 0, len(sources))
		for _, id := range sources {
			relations = append(relations, EntityRelation{FromEntityID: id, Name: "member-of", ToMediaID: "media-alpha"})
		}
		if err := st.ApplyLocalMetadata(ctx, "media-alpha", LocalMetadata{
			Relations: relations, OwnedRelations: owned,
			SignatureField: "sidecar-signature", Signature: signature,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertEntityRelation(ctx, EntityRelation{FromEntityID: "entity-c", Name: "member-of", ToMediaID: "media-alpha", Writer: "scraper:demo/item"}); err != nil {
		t.Fatal(err)
	}
	apply("sig-1", "entity-a", "entity-b")
	if err := st.ReleaseLocalMetadata(ctx, "media-alpha", "sidecar-signature"); err != nil {
		t.Fatal(err)
	}
	apply("sig-2", "entity-a", "entity-c")
	if got := relationWriters(t, st, "media-alpha", "member-of"); got != "entity-a=local,entity-b=local-released,entity-c=scraper:demo/item" {
		t.Fatalf("after restore = %q, want entity-a reclaimed and the others kept", got)
	}
	apply("sig-3")
	if got := relationWriters(t, st, "media-alpha", "member-of"); got != "entity-b=local-released,entity-c=scraper:demo/item" {
		t.Fatalf("after the edit = %q, want the reclaimed row removed", got)
	}
}

// TestRestoredSidecarReplacesReleasedSingleRow (CORE-3175): a restored
// sidecar naming another source for a single-cardinality relation replaces
// the row its released predecessor left, while a scraper's holder keeps the
// relation from the sidecar.
func TestRestoredSidecarReplacesReleasedSingleRow(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	for _, id := range []EntityID{"entity-a", "entity-b", "entity-c"} {
		if err := st.UpsertEntity(ctx, widgetEntity(id, "library-local")); err != nil {
			t.Fatal(err)
		}
	}
	owned := []WrittenRelation{{Kind: "widget", Name: "member-of"}}
	apply := func(media MediaID, signature string, source EntityID) {
		t.Helper()
		if err := st.ApplyLocalMetadata(ctx, media, LocalMetadata{
			Relations:      []EntityRelation{{FromEntityID: source, Name: "member-of", ToMediaID: media, Cardinality: "one"}},
			OwnedRelations: owned, SignatureField: "sidecar-signature", Signature: signature,
		}); err != nil {
			t.Fatal(err)
		}
	}
	apply("media-alpha", "sig-a", "entity-a")
	if err := st.ReleaseLocalMetadata(ctx, "media-alpha", "sidecar-signature"); err != nil {
		t.Fatal(err)
	}
	apply("media-alpha", "sig-b", "entity-b")
	if got := relationWriters(t, st, "media-alpha", "member-of"); got != "entity-b=local" {
		t.Fatalf("after restore = %q, want the released row replaced by entity-b", got)
	}
	if got := mediaFieldText(t, st, "media-alpha", "sidecar-signature"); got != "sig-b" {
		t.Fatalf("signature = %q, want sig-b", got)
	}
	other, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/held.mp4", Title: "held",
		Container: "mp4", Codec: "h264", ContentFingerprint: "fp-held",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntityRelation(ctx, EntityRelation{FromEntityID: "entity-c", Name: "member-of", ToMediaID: other.ID, Writer: "scraper:demo/item"}); err != nil {
		t.Fatal(err)
	}
	apply(other.ID, "sig-b", "entity-b")
	if got := relationWriters(t, st, other.ID, "member-of"); got != "entity-c=scraper:demo/item" {
		t.Fatalf("held media = %q, want the scraper holder kept", got)
	}

	// Single cardinality is per relation name: a released row from another
	// source kind gives way too (Codex r3).
	gadget := widgetEntity("entity-g", "library-local")
	gadget.Kind = "gadget"
	if err := st.UpsertEntity(ctx, gadget); err != nil {
		t.Fatal(err)
	}
	both := []WrittenRelation{{Kind: "gadget", Name: "member-of"}, {Kind: "widget", Name: "member-of"}}
	if err := st.ApplyLocalMetadata(ctx, "media-alpha", LocalMetadata{
		Relations:      []EntityRelation{{FromEntityID: "entity-g", Name: "member-of", ToMediaID: "media-alpha", Cardinality: "one"}},
		OwnedRelations: both, SignatureField: "sidecar-signature", Signature: "sig-g",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReleaseLocalMetadata(ctx, "media-alpha", "sidecar-signature"); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyLocalMetadata(ctx, "media-alpha", LocalMetadata{
		Relations:      []EntityRelation{{FromEntityID: "entity-a", Name: "member-of", ToMediaID: "media-alpha", Cardinality: "one"}},
		OwnedRelations: both, SignatureField: "sidecar-signature", Signature: "sig-a2",
	}); err != nil {
		t.Fatal(err)
	}
	if got := relationWriters(t, st, "media-alpha", "member-of"); got != "entity-a=local" {
		t.Fatalf("after cross-kind restore = %q, want the released gadget row replaced by entity-a", got)
	}
}

// TestReleaseClearsIdentifiedMarks (CORE-0361 r1): a released built-in field
// drops its legacy identified mark with its lock, so a scan import replaces
// the value again; a field the sidecar did not write keeps its mark.
func TestReleaseClearsIdentifiedMarks(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	imported := ImportMediaFile{
		LibraryID: "library-local", Path: "/media/released.mp4", Title: "released",
		Container: "mp4", Codec: "h264", ContentFingerprint: "fp-released",
	}
	media, _, err := st.ImportMediaFile(ctx, imported)
	if err != nil {
		t.Fatal(err)
	}
	year := 1999
	if err := st.UpdateMediaMetadata(ctx, media.ID, MediaMetadata{Year: &year, Source: MutationSourceOperator}); err != nil {
		t.Fatal(err)
	}
	title, summary := "Sidecar Title", "Sidecar summary"
	if err := st.ApplyLocalMetadata(ctx, media.ID, LocalMetadata{
		Metadata:       MediaMetadata{Title: &title, Summary: &summary},
		SignatureField: "sidecar-signature", Signature: "sig-1",
	}); err != nil {
		t.Fatal(err)
	}
	// A re-apply that no longer states the summary releases it alone.
	if err := st.ApplyLocalMetadata(ctx, media.ID, LocalMetadata{
		Metadata:       MediaMetadata{Title: &title},
		SignatureField: "sidecar-signature", Signature: "sig-2",
	}); err != nil {
		t.Fatal(err)
	}
	if got := identifiedMarks(t, st, media.ID); got != "1,0,1" {
		t.Fatalf("identified marks after dropping the summary = %s, want 1,0,1", got)
	}
	if err := st.ReleaseLocalMetadata(ctx, media.ID, "sidecar-signature"); err != nil {
		t.Fatal(err)
	}
	if got := identifiedMarks(t, st, media.ID); got != "0,0,1" {
		t.Fatalf("identified marks after release = %s, want 0,0,1", got)
	}
	imported.Title = "From The File Name"
	if _, _, err := st.ImportMediaFile(ctx, imported); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := st.catalogR.QueryRowContext(ctx, `SELECT title FROM media WHERE id = ?`, media.ID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "From The File Name" {
		t.Fatalf("title after a rescan = %q, want the file-name title", got)
	}
}

// identifiedMarks returns one Media's title, summary and year identified
// marks as "t,s,y".
func identifiedMarks(t *testing.T, st *Store, id MediaID) string {
	t.Helper()
	var title, summary, year int
	if err := st.catalogR.QueryRowContext(context.Background(),
		`SELECT title_identified, summary_identified, year_identified FROM media WHERE id = ?`, id,
	).Scan(&title, &summary, &year); err != nil {
		t.Fatal(err)
	}
	return strings.Join([]string{strconv.Itoa(title), strconv.Itoa(summary), strconv.Itoa(year)}, ",")
}

// TestApplyLocalMetadataFailureKeepsPreviousState (CORE-0361): the release
// of the previous parse and the new writes share one transaction, so a
// rejected apply leaves the fields locked and the old signature recorded.
func TestApplyLocalMetadataFailureKeepsPreviousState(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if err := st.ApplyLocalMetadata(ctx, "media-alpha", LocalMetadata{
		Fields:         []MediaField{{Name: "tagline", Type: EntityFieldText, Text: "From the sidecar"}},
		SignatureField: "sidecar-signature", Signature: "sig-1",
	}); err != nil {
		t.Fatal(err)
	}
	err := st.ApplyLocalMetadata(ctx, "media-alpha", LocalMetadata{
		Relations:      []EntityRelation{{FromEntityID: "entity-missing", Name: "member-of", ToMediaID: "media-alpha"}},
		OwnedRelations: []WrittenRelation{{Kind: "widget", Name: "member-of"}},
		SignatureField: "sidecar-signature", Signature: "sig-2",
	})
	if err == nil {
		t.Fatal("apply with a missing source entity accepted")
	}
	if got := provenanceSource(t, st, "media-alpha", "tagline"); got != "local" {
		t.Fatalf("tagline provenance after a failed apply = %q, want local", got)
	}
	if got := mediaFieldText(t, st, "media-alpha", "sidecar-signature"); got != "sig-1" {
		t.Fatalf("signature after a failed apply = %q, want sig-1", got)
	}
}

// TestApplyLocalMetadataSignatureIgnoresLocks (CORE-0361): the signature is
// bookkeeping, so a lock an operator put on it does not stop it advancing.
func TestApplyLocalMetadataSignatureIgnoresLocks(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if err := st.ApplyLocalMetadata(ctx, "media-alpha", LocalMetadata{SignatureField: "sidecar-signature", Signature: "sig-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetMediaFieldLock(ctx, "media-alpha", "sidecar-signature", true, LibraryFilter{AllLibraries: true, Revealed: true}); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyLocalMetadata(ctx, "media-alpha", LocalMetadata{SignatureField: "sidecar-signature", Signature: "sig-2"}); err != nil {
		t.Fatal(err)
	}
	if got := mediaFieldText(t, st, "media-alpha", "sidecar-signature"); got != "sig-2" {
		t.Fatalf("locked signature = %q, want sig-2", got)
	}
}

func mediaFieldText(t *testing.T, st *Store, id MediaID, name string) string {
	t.Helper()
	fields, err := st.MediaFields(context.Background(), id, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range fields {
		if field.Name == name {
			return field.Text
		}
	}
	return ""
}

// relationWriters lists one Media's rows under name as source=writer, sorted.
func relationWriters(t *testing.T, st *Store, id MediaID, name string) string {
	t.Helper()
	rows, err := st.catalogR.QueryContext(context.Background(),
		`SELECT from_entity_id, COALESCE(writer, '') FROM entity_relation WHERE to_media_id = ? AND relation = ? ORDER BY from_entity_id`,
		id, name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var source, writer string
		if err := rows.Scan(&source, &writer); err != nil {
			t.Fatal(err)
		}
		out = append(out, source+"="+writer)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(out, ",")
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
