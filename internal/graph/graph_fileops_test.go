package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// feature:file-operations — the GraphQL surface: a manager-gated, preview-first
// organize over Media files and a duplicate merge, both sharing the batch-undo
// envelope (ADR-0009 §5–6). Exercised end to end through the HTTP handler.

func importGraphFile(t *testing.T, st *store.Store, libraryID, path, title, oshash string) store.MediaID {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(title), 0o644); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(t.Context(), store.ImportMediaFile{
		LibraryID:          store.LibraryID(libraryID),
		Path:               path,
		Title:              title,
		Container:          "mp4",
		Codec:              "h264",
		Class:              store.MediaClassVideo,
		ContentFingerprint: store.HashMediaFilePath(path),
		OSHash:             oshash,
	})
	if err != nil {
		t.Fatalf("import %s: %v", path, err)
	}
	return media.ID
}

func TestOrganizePreviewOverGraphQL(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	root := t.TempDir()
	if err := st.SetLibraryRoots(t.Context(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	mediaID := importGraphFile(t, st, "library-local", filepath.Join(root, "raw", "x.mp4"), "Atlas", "0000000011110001")
	if err := st.SetMediaFields(t.Context(), mediaID, []store.MediaField{
		{Name: "collection", Type: store.EntityFieldText, Text: "Voyages"},
	}); err != nil {
		t.Fatal(err)
	}

	resp := graphPost(t, handler, authorizeManager,
		`{"query":"query($libraryID:String!,$template:String!){ organizePreview(libraryID:$libraryID, template:$template){ mediaID oldPath newPath } }","variables":{"libraryID":"library-local","template":"{collection}/{title}"}}`)
	var decoded struct {
		Data struct {
			OrganizePreview []FileChangeInfo `json:"organizePreview"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v (%s)", err, resp)
	}
	if len(decoded.Errors) != 0 {
		t.Fatalf("errors = %#v", decoded.Errors)
	}
	if len(decoded.Data.OrganizePreview) != 1 {
		t.Fatalf("preview = %#v, want 1", decoded.Data.OrganizePreview)
	}
	want := filepath.Join(root, "Voyages", "Atlas.mp4")
	if decoded.Data.OrganizePreview[0].NewPath != want {
		t.Fatalf("newPath = %q, want %q", decoded.Data.OrganizePreview[0].NewPath, want)
	}
	// Preview must not have moved bytes.
	if _, err := os.Stat(filepath.Join(root, "raw", "x.mp4")); err != nil {
		t.Fatalf("preview moved the file: %v", err)
	}
}

func TestMergeMediaOverGraphQL(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	root := t.TempDir()
	if err := st.SetLibraryRoots(t.Context(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	dup := "0000000022220001"
	keep := importGraphFile(t, st, "library-local", filepath.Join(root, "keep.mp4"), "Keep", dup)
	drop := importGraphFile(t, st, "library-local", filepath.Join(root, "drop.mp4"), "Drop", dup)

	resp := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($libraryID:String!,$keepMediaID:String!){ mergeMedia(libraryID:$libraryID, keepMediaID:$keepMediaID){ batchID kind affected } }","variables":{"libraryID":"library-local","keepMediaID":"`+string(keep)+`"}}`)
	var decoded struct {
		Data struct {
			MergeMedia BatchResultInfo `json:"mergeMedia"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v (%s)", err, resp)
	}
	if len(decoded.Errors) != 0 {
		t.Fatalf("errors = %#v", decoded.Errors)
	}
	if decoded.Data.MergeMedia.Kind != "merge" || decoded.Data.MergeMedia.Affected != 1 {
		t.Fatalf("merge = %#v, want 1 trashed", decoded.Data.MergeMedia)
	}
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	if _, err := st.Media(t.Context(), drop, filter); err == nil {
		t.Fatal("dropped duplicate still visible after merge")
	}
	if _, err := st.Media(t.Context(), keep, filter); err != nil {
		t.Fatalf("survivor missing after merge: %v", err)
	}
}

func TestMergeEntitiesOverGraphQL(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	for _, id := range []store.EntityID{"winner", "loser"} {
		if err := st.UpsertEntity(t.Context(), store.Entity{
			ID: id, LibraryID: "library-local", Kind: "widget", Title: "W " + string(id),
		}); err != nil {
			t.Fatal(err)
		}
	}

	resp := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($winnerEntityID:String!,$loserEntityID:String!){ mergeEntities(winnerEntityID:$winnerEntityID, loserEntityID:$loserEntityID){ batchID kind affected } }","variables":{"winnerEntityID":"winner","loserEntityID":"loser"}}`)
	var decoded struct {
		Data struct {
			MergeEntities BatchResultInfo `json:"mergeEntities"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v (%s)", err, resp)
	}
	if len(decoded.Errors) != 0 {
		t.Fatalf("errors = %#v", decoded.Errors)
	}
	if decoded.Data.MergeEntities.Kind != "merge-entity" || decoded.Data.MergeEntities.Affected != 1 {
		t.Fatalf("mergeEntities = %#v, want 1 merge-entity", decoded.Data.MergeEntities)
	}
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	if _, err := st.Entity(t.Context(), "loser", filter); err == nil {
		t.Fatal("loser still visible after entity merge")
	}
	if _, err := st.Entity(t.Context(), "winner", filter); err != nil {
		t.Fatalf("winner missing after entity merge: %v", err)
	}
}

func TestMergeEntitiesRequiresManagerRole(t *testing.T) {
	handler := newTestHandler(t)
	resp := graphPost(t, handler, authorizeViewer,
		`{"query":"mutation($winnerEntityID:String!,$loserEntityID:String!){ mergeEntities(winnerEntityID:$winnerEntityID, loserEntityID:$loserEntityID){ batchID } }","variables":{"winnerEntityID":"winner","loserEntityID":"loser"}}`)
	var decoded response
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Errors) != 1 || decoded.Errors[0].Message != "manager role required" {
		t.Fatalf("errors = %#v, want manager role required", decoded.Errors)
	}
}

func TestFileOpsRequireManagerRole(t *testing.T) {
	handler := newTestHandler(t)
	resp := graphPost(t, handler, authorizeViewer,
		`{"query":"query($libraryID:String!,$template:String!){ organizePreview(libraryID:$libraryID, template:$template){ mediaID } }","variables":{"libraryID":"library-local","template":"{title}"}}`)
	var decoded response
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Errors) != 1 || decoded.Errors[0].Message != "manager role required" {
		t.Fatalf("errors = %#v, want manager role required", decoded.Errors)
	}
}
