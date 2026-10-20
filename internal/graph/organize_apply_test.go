package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// feature:file-operations — the organize APPLY path over the wire:
// a manager-gated mutation that moves templated files on disk as one undo-able
// batch. The preview path is covered by TestOrganizePreviewOverGraphQL; this
// asserts the apply EFFECT (bytes + record repoint), the MANAGER gate (a viewer
// is rejected), and that the returned batch id reverses through undoBatch. These
// run end to end through the HTTP handler. Reuses importGraphFile (graph_fileops_test.go)
// and graphPost (graph_bulkedit_test.go) — both package-visible; do not redefine.

func TestOrganizeApplyMovesFileAndUndoRestoresOverGraphQL(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	root := t.TempDir()
	if err := st.SetLibraryRoots(t.Context(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(root, "raw", "x.mp4")
	mediaID := importGraphFile(t, st, "library-local", oldPath, "Atlas", "00000000ccc10001")
	if err := st.SetMediaFields(t.Context(), mediaID, []store.MediaField{
		{Name: "collection", Type: store.EntityFieldText, Text: "Voyages"},
	}); err != nil {
		t.Fatal(err)
	}
	wantNew := filepath.Join(root, "Voyages", "Atlas.mp4")
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}

	// Apply: returns a non-empty batch id + the organize kind + 1 affected.
	apply := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($libraryID:String!,$template:String!){ organizeApply(libraryID:$libraryID, template:$template){ batchID kind affected } }","variables":{"libraryID":"library-local","template":"{collection}/{title}"}}`)
	var applyResp struct {
		Data struct {
			OrganizeApply BatchResultInfo `json:"organizeApply"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(apply, &applyResp); err != nil {
		t.Fatalf("decode apply: %v (%s)", err, apply)
	}
	if len(applyResp.Errors) != 0 {
		t.Fatalf("apply errors = %#v", applyResp.Errors)
	}
	result := applyResp.Data.OrganizeApply
	if result.BatchID == "" || result.Kind != "organize" || result.Affected != 1 {
		t.Fatalf("apply result = %#v, want organize batch with 1 affected", result)
	}

	// Effect: bytes moved on disk and the Media record repoints to the new path.
	if _, err := os.Stat(wantNew); err != nil {
		t.Fatalf("file not at new path after apply: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old path still exists after apply: %v", err)
	}
	file, err := st.PrimaryMediaFile(t.Context(), mediaID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if file.Path != wantNew {
		t.Fatalf("record path after apply = %q, want %q", file.Path, wantNew)
	}

	// The returned batch id reverses the apply: bytes move back, record repoints.
	undo := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($batchID:String!){ undoBatch(batchID:$batchID){ batchID affected } }","variables":{"batchID":"`+result.BatchID+`"}}`)
	var undoResp struct {
		Data struct {
			UndoBatch BatchResultInfo `json:"undoBatch"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(undo, &undoResp); err != nil {
		t.Fatalf("decode undo: %v (%s)", err, undo)
	}
	if len(undoResp.Errors) != 0 || undoResp.Data.UndoBatch.Affected != 1 {
		t.Fatalf("undo = %#v errors=%#v, want 1 affected", undoResp.Data.UndoBatch, undoResp.Errors)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("file not restored to old path after undo: %v", err)
	}
	if _, err := os.Stat(wantNew); !os.IsNotExist(err) {
		t.Fatalf("new path still exists after undo: %v", err)
	}
	file, err = st.PrimaryMediaFile(t.Context(), mediaID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if file.Path != oldPath {
		t.Fatalf("record path after undo = %q, want %q", file.Path, oldPath)
	}
}

func TestBatchRenameManagedPathsAppliesAndUndoRestoresOverGraphQL(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryRoots(t.Context(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	oldA := filepath.Join(canonicalRoot, "a.mp4")
	oldB := filepath.Join(canonicalRoot, "b.mp4")
	mediaA := importGraphFile(t, st, "library-local", oldA, "Alpha", "00000000ccc30001")
	mediaB := importGraphFile(t, st, "library-local", oldB, "Beta", "00000000ccc30002")
	newA := filepath.Join(canonicalRoot, "alpha-renamed.mp4")
	newB := filepath.Join(canonicalRoot, "beta-renamed.mp4")
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}

	apply := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($libraryID:String!,$renames:[ManagedPathRenameInput!]!){ batchRenameManagedPaths(libraryID:$libraryID, renames:$renames){ batchID kind affected } }","variables":{"libraryID":"library-local","renames":[{"path":"`+oldA+`","name":"alpha-renamed.mp4"},{"path":"`+oldB+`","name":"beta-renamed.mp4"}]}}`)
	var applyResp struct {
		Data struct {
			BatchRenameManagedPaths BatchResultInfo `json:"batchRenameManagedPaths"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(apply, &applyResp); err != nil {
		t.Fatalf("decode apply: %v (%s)", err, apply)
	}
	if len(applyResp.Errors) != 0 {
		t.Fatalf("apply errors = %#v", applyResp.Errors)
	}
	result := applyResp.Data.BatchRenameManagedPaths
	if result.BatchID == "" || result.Kind != "move" || result.Affected != 2 {
		t.Fatalf("apply result = %#v, want move batch with 2 affected", result)
	}
	for _, path := range []string{newA, newB} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("renamed file missing at %s: %v", path, err)
		}
	}
	for mediaID, want := range map[store.MediaID]string{mediaA: newA, mediaB: newB} {
		file, err := st.PrimaryMediaFile(t.Context(), mediaID, filter)
		if err != nil {
			t.Fatal(err)
		}
		if file.Path != want {
			t.Fatalf("record path after apply = %q, want %q", file.Path, want)
		}
	}

	undo := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($batchID:String!){ undoBatch(batchID:$batchID){ batchID affected } }","variables":{"batchID":"`+result.BatchID+`"}}`)
	var undoResp struct {
		Data struct {
			UndoBatch BatchResultInfo `json:"undoBatch"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(undo, &undoResp); err != nil {
		t.Fatalf("decode undo: %v (%s)", err, undo)
	}
	if len(undoResp.Errors) != 0 || undoResp.Data.UndoBatch.Affected != 2 {
		t.Fatalf("undo = %#v errors=%#v, want 2 affected", undoResp.Data.UndoBatch, undoResp.Errors)
	}
	for _, path := range []string{oldA, oldB} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("restored file missing at %s: %v", path, err)
		}
	}
}

// TestOrganizeApplyRequiresManagerRole asserts the manager gate (graph.go:1091):
// a viewer session is rejected with the real error string and never touches disk.
func TestOrganizeApplyRequiresManagerRole(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	root := t.TempDir()
	if err := st.SetLibraryRoots(t.Context(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(root, "raw", "y.mp4")
	importGraphFile(t, st, "library-local", oldPath, "Untouched", "00000000ccc20001")

	resp := graphPost(t, handler, authorizeViewer,
		`{"query":"mutation($libraryID:String!,$template:String!){ organizeApply(libraryID:$libraryID, template:$template){ batchID } }","variables":{"libraryID":"library-local","template":"{title}"}}`)
	var decoded response
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v (%s)", err, resp)
	}
	if len(decoded.Errors) != 1 || decoded.Errors[0].Message != "manager role required" {
		t.Fatalf("errors = %#v, want manager role required", decoded.Errors)
	}
	// The rejected apply must not have moved bytes.
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("viewer apply moved the file despite the gate: %v", err)
	}
}
