package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/plugin"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

const (
	testSidecarSignatureField = "sidecar-signature"
	testSidecarNoteField      = "note"
)

// sidecarTestMapper reads "key=value" lines from the file beside the media
// named <path>.side: title, year and note are local values, credit a credit
// relation, and an "unreadable" line makes the read fail. It owns the credit
// relation. parsedSignature, when set, replaces the signature of the bytes it
// read, as when the sidecar changed between the scan's check and its read.
type sidecarTestMapper struct{ parsedSignature string }

func sidecarPath(path string) string { return path + ".side" }

func (sidecarTestMapper) MarkerField() string           { return testHydrationMarkerField }
func (sidecarTestMapper) ExternalIDSchemes() []string   { return nil }
func (sidecarTestMapper) SidecarSignatureField() string { return testSidecarSignatureField }
func (sidecarTestMapper) SidecarSignature(path string) string {
	info, err := os.Stat(sidecarPath(path))
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}

func (sidecarTestMapper) MapHydration(map[string]string, string) HydrationPlan {
	return HydrationPlan{MarkComplete: true}
}

func (m sidecarTestMapper) MapSidecar(path string) *HydrationLocal {
	data, err := os.ReadFile(sidecarPath(path))
	if err != nil {
		return nil
	}
	local := &HydrationLocal{
		Signature:      m.SidecarSignature(path),
		OwnedRelations: []store.WrittenRelation{{Kind: "artist", Name: "appears-on"}},
	}
	if m.parsedSignature != "" {
		local.Signature = m.parsedSignature
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, _ := strings.Cut(line, "=")
		switch key {
		case "unreadable":
			return &HydrationLocal{Unreadable: true}
		case "title":
			local.Title = value
		case "year":
			_, _ = fmt.Sscanf(value, "%d", &local.Year)
		case "note":
			local.MediaFields = append(local.MediaFields, store.MediaField{Name: testSidecarNoteField, Type: store.EntityFieldText, Text: value})
		case "credit":
			local.Relations = append(local.Relations, HydrationRelation{
				From: HydrationEntity{Kind: "artist", Title: value}, Name: "appears-on",
				Attributes: map[string]string{"credited-as": "Lead"},
			})
		}
	}
	return local
}

func sidecarTestBundle() plugin.Bundle {
	bundle := testHydrationBundle()
	bundle.MediaFields = append(bundle.MediaFields,
		plugin.FieldSpec{Name: testSidecarSignatureField, Type: "text", Label: plugin.L("Sidecar")},
		plugin.FieldSpec{Name: testSidecarNoteField, Type: "text", Label: plugin.L("Note")},
	)
	return bundle
}

func newSidecarAdapter(t *testing.T, st hydrationStore) *hydrationAdapter {
	t.Helper()
	return newSidecarAdapterFor(t, st, sidecarTestMapper{})
}

func newSidecarAdapterFor(t *testing.T, st hydrationStore, mapper sidecarTestMapper) *hydrationAdapter {
	t.Helper()
	registry, err := plugin.NewRegistry([]plugin.Bundle{sidecarTestBundle()})
	if err != nil {
		t.Fatal(err)
	}
	return newHydrationAdapters(registry, st, map[string]HydrationMapper{testHydrationPluginID: mapper})[testHydrationPluginID].(*hydrationAdapter)
}

func (r *rehydrationScan) writeSidecar(content string) {
	r.t.Helper()
	if err := os.WriteFile(sidecarPath(r.path), []byte(content), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rehydrationScan) media() store.Media {
	r.t.Helper()
	media, err := r.st.Media(context.Background(), r.mediaID, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		r.t.Fatal(err)
	}
	return media
}

func (r *rehydrationScan) locked(field string) bool {
	r.t.Helper()
	locks, err := r.st.MediaFieldLocks(context.Background(), r.mediaID, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		r.t.Fatal(err)
	}
	for _, lock := range locks {
		if lock.Field == field {
			return lock.Locked
		}
	}
	return false
}

// identifyLike writes the way an identification does: built-in and declared
// fields at the operator tier.
func (r *rehydrationScan) identifyLike(title, note string) {
	r.t.Helper()
	ctx := context.Background()
	if err := r.st.UpdateMediaMetadata(ctx, r.mediaID, store.MediaMetadata{Title: &title, Source: store.MutationSourceOperator}); err != nil {
		r.t.Fatal(err)
	}
	if err := r.st.UpsertMediaFieldsWithSource(ctx, r.mediaID, []store.MediaField{{Name: testSidecarNoteField, Type: store.EntityFieldText, Text: note}}, store.MutationSourceOperator); err != nil {
		r.t.Fatal(err)
	}
}

// TestScanWritesSidecarValuesAtLocalTier (CORE-0361): a file's sidecar values
// land locked, an identification changes none of them, a changed sidecar on an
// unchanged file is read again on the next scan, and a removed sidecar
// unlocks every value without clearing it.
func TestScanWritesSidecarValuesAtLocalTier(t *testing.T) {
	r := newRehydrationScan(t)
	r.adapter = newSidecarAdapter(t, r.counted)
	r.write("sidecar audio bytes padding padding", 3*time.Hour, map[string]string{})
	r.writeSidecar("title=Sidecar Title\nyear=2001\nnote=First note\ncredit=Person A")
	r.scan()

	if media := r.media(); media.Title != "Sidecar Title" || media.Year != 2001 {
		t.Fatalf("media = %q (%d), want the sidecar title and year", media.Title, media.Year)
	}
	if r.field(testSidecarNoteField) != "First note" || !r.locked(testSidecarNoteField) || !r.locked("title") {
		t.Fatalf("note %q locked %v title locked %v, want the locked sidecar note", r.field(testSidecarNoteField), r.locked(testSidecarNoteField), r.locked("title"))
	}
	if r.field(testSidecarSignatureField) == "" {
		t.Fatal("sidecar signature not recorded")
	}
	credits, err := r.st.EntitiesForMedia(context.Background(), r.mediaID, "appears-on", store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(credits) != 1 || credits[0].Entity.Title != "Person A" || credits[0].Attributes["credited-as"] != "Lead" {
		t.Fatalf("credits = %+v, want Person A credited as Lead", credits)
	}

	r.identifyLike("Identified Title", "Identified note")
	if r.media().Title != "Sidecar Title" || r.field(testSidecarNoteField) != "First note" {
		t.Fatalf("identification overwrote sidecar values: %q / %q", r.media().Title, r.field(testSidecarNoteField))
	}

	// An unchanged file with unchanged sidecars is not hydrated again.
	writes := r.counted.writes
	r.scan()
	if r.counted.writes != writes {
		t.Fatalf("unchanged sidecar re-hydrated: %d writes", r.counted.writes-writes)
	}

	// A changed sidecar is read again although the media file is unchanged.
	r.writeSidecar("title=Sidecar Title\nnote=Second, longer note\ncredit=Person A")
	r.scan()
	if r.field(testSidecarNoteField) != "Second, longer note" || !r.locked(testSidecarNoteField) {
		t.Fatalf("note after sidecar edit = %q (locked %v)", r.field(testSidecarNoteField), r.locked(testSidecarNoteField))
	}

	// Removing the sidecar unlocks the values but keeps them.
	if err := os.Remove(sidecarPath(r.path)); err != nil {
		t.Fatal(err)
	}
	r.scan()
	if r.media().Title != "Sidecar Title" || r.field(testSidecarNoteField) != "Second, longer note" {
		t.Fatalf("removed sidecar cleared values: %q / %q", r.media().Title, r.field(testSidecarNoteField))
	}
	if r.locked(testSidecarNoteField) || r.locked("title") || r.field(testSidecarSignatureField) != "" {
		t.Fatalf("removed sidecar: note locked %v title locked %v signature %q", r.locked(testSidecarNoteField), r.locked("title"), r.field(testSidecarSignatureField))
	}
	r.identifyLike("Identified Title", "Identified note")
	if r.media().Title != "Identified Title" || r.field(testSidecarNoteField) != "Identified note" {
		t.Fatalf("identification after release = %q / %q", r.media().Title, r.field(testSidecarNoteField))
	}
}

func TestValidateLocalRejectsOutOfBundleValues(t *testing.T) {
	adapter := newSidecarAdapter(t, newFakeHydrationStore())
	text := func(name string) store.MediaField {
		return store.MediaField{Name: name, Type: store.EntityFieldText, Text: "x"}
	}
	cases := map[string]*HydrationLocal{
		"marker":              {MediaFields: []store.MediaField{{Name: testHydrationMarkerField, Type: store.EntityFieldFlag, Flag: true}}},
		"signature":           {MediaFields: []store.MediaField{text(testSidecarSignatureField)}},
		"undeclared":          {MediaFields: []store.MediaField{text("undeclared")}},
		"type":                {MediaFields: []store.MediaField{{Name: testSidecarNoteField, Type: store.EntityFieldNumber, Number: 1}}},
		"relation":            {Relations: []HydrationRelation{{From: HydrationEntity{Kind: "artist", Title: "A"}, Name: "undeclared"}}},
		"to-entity":           {Relations: []HydrationRelation{{From: HydrationEntity{Kind: "album", Title: "A"}, Name: "album-artist", ToEntity: &HydrationEntity{Kind: "artist", Title: "B"}}}},
		"bad-pointer":         {Relations: []HydrationRelation{{From: HydrationEntity{Kind: "missing", Title: "A"}, Name: "appears-on"}}},
		"unowned":             {Relations: []HydrationRelation{{From: HydrationEntity{Kind: "artist", Title: "A"}, Name: "appears-on"}}},
		"owned-undeclared":    {OwnedRelations: []store.WrittenRelation{{Kind: "artist", Name: "undeclared"}}},
		"owned-entity-target": {OwnedRelations: []store.WrittenRelation{{Kind: "album", Name: "album-artist"}}},
	}
	for name, local := range cases {
		if _, err := adapter.validateLocal(local, testHydrationMarkerField); err == nil {
			t.Errorf("%s: validateLocal accepted %+v", name, local)
		}
	}
	got, err := adapter.validateLocal(&HydrationLocal{Title: "  Spaced  ", Year: -3, MediaFields: []store.MediaField{{}, text(testSidecarNoteField)}}, testHydrationMarkerField)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Spaced" || got.Year != 0 || len(got.MediaFields) != 1 {
		t.Fatalf("validated = %+v", got)
	}
	// A sidecar that states nothing still applies: it releases what an
	// earlier parse stated.
	if got, err := adapter.validateLocal(&HydrationLocal{}, testHydrationMarkerField); err != nil || got == nil {
		t.Fatalf("empty local = %+v, %v; want an empty parse", got, err)
	}
}

// A store that cannot write local values fails the hydration instead of
// dropping them, and a mapper's undeclared signature field fails the check.
func TestSidecarHydrationNeedsDeclaredSupport(t *testing.T) {
	ctx := context.Background()
	fake := newFakeHydrationStore()
	adapter := newSidecarAdapter(t, fake)
	path := filepath.Join(t.TempDir(), "a.flac")
	if err := os.WriteFile(sidecarPath(path), []byte("note=x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := adapter.HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{MediaID: testMedia, LibraryID: testLib, Class: store.MediaClassAudio, Path: path})
	if err == nil || !strings.Contains(err.Error(), "local metadata") {
		t.Fatalf("hydration through a store without local writes = %v", err)
	}
	if changed, err := adapter.SidecarsChanged(ctx, testMedia, path); err != nil || !changed {
		t.Fatalf("SidecarsChanged = %v, %v; want changed", changed, err)
	}

	registry, err := plugin.NewRegistry([]plugin.Bundle{testHydrationBundle()})
	if err != nil {
		t.Fatal(err)
	}
	undeclared := newHydrationAdapters(registry, storetest.Open(t), map[string]HydrationMapper{testHydrationPluginID: sidecarTestMapper{}})[testHydrationPluginID].(*hydrationAdapter)
	if _, err := undeclared.SidecarsChanged(ctx, testMedia, path); err == nil {
		t.Fatal("undeclared signature field accepted")
	}
}

func (r *rehydrationScan) credits() []string {
	r.t.Helper()
	related, err := r.st.EntitiesForMedia(context.Background(), r.mediaID, "appears-on", store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		r.t.Fatal(err)
	}
	var titles []string
	for _, entry := range related {
		titles = append(titles, entry.Entity.Title)
	}
	return titles
}

// TestSidecarUnreadableIsNotRemoval (CORE-0361): a sidecar that cannot be
// read changes nothing, keeps its old signature so the next scan retries, and
// a later readable sidecar that drops the last credit removes it.
func TestSidecarUnreadableIsNotRemoval(t *testing.T) {
	r := newRehydrationScan(t)
	r.adapter = newSidecarAdapter(t, r.counted)
	r.write("sidecar audio bytes padding padding", 3*time.Hour, map[string]string{})
	r.writeSidecar("note=First note\ncredit=Person A")
	r.scan()
	signature := r.field(testSidecarSignatureField)
	if signature == "" || len(r.credits()) != 1 {
		t.Fatalf("signature %q credits %v after the first scan", signature, r.credits())
	}

	r.writeSidecar("unreadable\nnote=Lost note")
	r.scan()
	if r.field(testSidecarNoteField) != "First note" || !r.locked(testSidecarNoteField) || len(r.credits()) != 1 {
		t.Fatalf("unreadable sidecar changed state: note %q locked %v credits %v", r.field(testSidecarNoteField), r.locked(testSidecarNoteField), r.credits())
	}
	if r.field(testSidecarSignatureField) != signature {
		t.Fatalf("unreadable sidecar signature = %q, want the old %q", r.field(testSidecarSignatureField), signature)
	}
	if changed, err := r.adapter.SidecarsChanged(context.Background(), r.mediaID, r.path); err != nil || !changed {
		t.Fatalf("SidecarsChanged after an unreadable sidecar = %v, %v; want a retry", changed, err)
	}

	r.writeSidecar("note=Second note, longer")
	r.scan()
	if r.field(testSidecarNoteField) != "Second note, longer" || len(r.credits()) != 0 {
		t.Fatalf("readable sidecar: note %q credits %v, want the new note and no credit", r.field(testSidecarNoteField), r.credits())
	}
}

// TestSidecarRecordsParsedSignature (CORE-0361): the recorded signature is
// the one the parse read, not the one the scan's check saw, so a sidecar that
// changed between the two is read again on the next scan.
func TestSidecarRecordsParsedSignature(t *testing.T) {
	r := newRehydrationScan(t)
	r.adapter = newSidecarAdapterFor(t, r.counted, sidecarTestMapper{parsedSignature: "parsed-bytes"})
	r.write("sidecar audio bytes padding padding", 3*time.Hour, map[string]string{})
	r.writeSidecar("note=First note")
	r.scan()
	if got := r.field(testSidecarSignatureField); got != "parsed-bytes" {
		t.Fatalf("recorded signature = %q, want the parse's", got)
	}
	if changed, err := r.adapter.SidecarsChanged(context.Background(), r.mediaID, r.path); err != nil || !changed {
		t.Fatalf("SidecarsChanged = %v, %v; want the newer sidecar read again", changed, err)
	}
}

// CORE-0361: a plan that only names an Entity (a name-only credit, a parent)
// must not erase the fields an earlier write stored on it.
func TestHydrationFieldlessEntityKeepsStoredFields(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	library, err := st.CreateLibrary(ctx, "Library", false, testHydrationPluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	adapter := newSidecarAdapter(t, st)
	date := store.EntityField{Name: "release-date", Type: store.EntityFieldDate, Date: time.Date(2001, 1, 2, 0, 0, 0, 0, time.UTC)}
	release := store.EntityField{Name: "musicbrainz-release-id", Type: store.EntityFieldText, Text: "release-1"}
	id, err := adapter.upsertEntity(ctx, library.ID, HydrationEntity{Kind: "album", Title: "Record", Fields: []store.EntityField{date, release}}, map[string]store.EntityID{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.upsertEntity(ctx, library.ID, HydrationEntity{Kind: "album", Title: "Record"}, map[string]store.EntityID{}); err != nil {
		t.Fatal(err)
	}
	entity, err := st.Entity(ctx, id, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(entity.Fields) != 2 {
		t.Fatalf("field-less write left %+v, want both stored fields", entity.Fields)
	}
}
