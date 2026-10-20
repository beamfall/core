package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/store/storetest"
)

func TestFolderTreeOverGraphQL(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inbox", "item.mp4"), []byte("media"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryRoots(t.Context(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}

	resp := graphPost(t, handler, authorizeManager,
		`{"query":"query($libraryID:String!,$depth:Int){ folderTree(libraryID:$libraryID, depth:$depth){ name kind children { name path kind children { name kind } } } }","variables":{"libraryID":"library-local","depth":2}}`)
	var decoded struct {
		Data struct {
			FolderTree FolderEntryInfo `json:"folderTree"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v (%s)", err, resp)
	}
	if len(decoded.Errors) != 0 {
		t.Fatalf("errors = %#v", decoded.Errors)
	}
	tree := decoded.Data.FolderTree
	if tree.Name != "Library roots" || tree.Kind != "folder" || len(tree.Children) != 1 {
		t.Fatalf("folderTree = %#v, want synthetic roots node", tree)
	}
	if tree.Children[0].Path != root || tree.Children[0].Children[0].Name != "inbox" {
		t.Fatalf("folderTree children = %#v", tree.Children)
	}
}

func TestFolderTreeRequiresManagerRole(t *testing.T) {
	handler := newTestHandler(t)
	resp := graphPost(t, handler, authorizeViewer,
		`{"query":"query($libraryID:String!){ folderTree(libraryID:$libraryID){ name } }","variables":{"libraryID":"library-local"}}`)
	var decoded response
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Errors) != 1 || decoded.Errors[0].Message != "manager role required" {
		t.Fatalf("errors = %#v, want manager role required", decoded.Errors)
	}
}

func TestFolderTreeRejectsPathOutsideLibraryRoot(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	root := t.TempDir()
	outside := t.TempDir()
	if err := st.SetLibraryRoots(t.Context(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}

	resp := graphPost(t, handler, authorizeManager,
		`{"query":"query($libraryID:String!,$root:String!){ folderTree(libraryID:$libraryID, root:$root){ name } }","variables":{"libraryID":"library-local","root":"`+outside+`"}}`)
	var decoded response
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Errors) != 1 || decoded.Errors[0].Message != "folder is outside the library roots" {
		t.Fatalf("errors = %#v, want containment error", decoded.Errors)
	}
}

func TestManagedPathOpsOverGraphQL(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	root := t.TempDir()
	if err := st.SetLibraryRoots(t.Context(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}

	create := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($libraryID:String!,$parentPath:String!,$name:String!){ createManagedFolder(libraryID:$libraryID, parentPath:$parentPath, name:$name){ path kind } }","variables":{"libraryID":"library-local","parentPath":"`+root+`","name":"incoming"}}`)
	var created struct {
		Data struct {
			CreateManagedFolder ManagedPathInfo `json:"createManagedFolder"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(create, &created); err != nil {
		t.Fatalf("decode create: %v (%s)", err, create)
	}
	if len(created.Errors) != 0 {
		t.Fatalf("create errors = %#v", created.Errors)
	}
	file := filepath.Join(created.Data.CreateManagedFolder.Path, "item.txt")
	if err := os.WriteFile(file, []byte("media"), 0o644); err != nil {
		t.Fatal(err)
	}
	renameResp := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($libraryID:String!,$path:String!,$name:String!){ renameManagedPath(libraryID:$libraryID, path:$path, name:$name){ path kind } }","variables":{"libraryID":"library-local","path":"`+file+`","name":"renamed.txt"}}`)
	var renamed struct {
		Data struct {
			RenameManagedPath ManagedPathInfo `json:"renameManagedPath"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(renameResp, &renamed); err != nil {
		t.Fatalf("decode rename: %v (%s)", err, renameResp)
	}
	if len(renamed.Errors) != 0 || renamed.Data.RenameManagedPath.Kind != "file" {
		t.Fatalf("rename = %#v errors=%#v", renamed.Data.RenameManagedPath, renamed.Errors)
	}
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	moveResp := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($libraryID:String!,$path:String!,$destinationParentPath:String!){ moveManagedPath(libraryID:$libraryID, path:$path, destinationParentPath:$destinationParentPath){ path kind } }","variables":{"libraryID":"library-local","path":"`+renamed.Data.RenameManagedPath.Path+`","destinationParentPath":"`+destination+`"}}`)
	var moved struct {
		Data struct {
			MoveManagedPath ManagedPathInfo `json:"moveManagedPath"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(moveResp, &moved); err != nil {
		t.Fatalf("decode move: %v (%s)", err, moveResp)
	}
	if len(moved.Errors) != 0 || moved.Data.MoveManagedPath.Kind != "file" {
		t.Fatalf("move = %#v errors=%#v", moved.Data.MoveManagedPath, moved.Errors)
	}
	deleteResp := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($libraryID:String!,$path:String!){ deleteManagedPath(libraryID:$libraryID, path:$path){ trashID originalPath kind } }","variables":{"libraryID":"library-local","path":"`+moved.Data.MoveManagedPath.Path+`"}}`)
	var deleted struct {
		Data struct {
			DeleteManagedPath ManagedTrashInfo `json:"deleteManagedPath"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(deleteResp, &deleted); err != nil {
		t.Fatalf("decode delete: %v (%s)", err, deleteResp)
	}
	if len(deleted.Errors) != 0 || deleted.Data.DeleteManagedPath.TrashID == "" {
		t.Fatalf("delete = %#v errors=%#v", deleted.Data.DeleteManagedPath, deleted.Errors)
	}
	restoreResp := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($libraryID:String!,$trashID:String!){ restoreManagedPath(libraryID:$libraryID, trashID:$trashID){ path kind } }","variables":{"libraryID":"library-local","trashID":"`+deleted.Data.DeleteManagedPath.TrashID+`"}}`)
	var restored struct {
		Data struct {
			RestoreManagedPath ManagedPathInfo `json:"restoreManagedPath"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(restoreResp, &restored); err != nil {
		t.Fatalf("decode restore: %v (%s)", err, restoreResp)
	}
	if len(restored.Errors) != 0 || restored.Data.RestoreManagedPath.Path != moved.Data.MoveManagedPath.Path {
		t.Fatalf("restore = %#v errors=%#v", restored.Data.RestoreManagedPath, restored.Errors)
	}
}

func TestManagedPathOpsRejectViewerAndRootEscape(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	root := t.TempDir()
	outside := t.TempDir()
	if err := st.SetLibraryRoots(t.Context(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}

	viewer := graphPost(t, handler, authorizeViewer,
		`{"query":"mutation($libraryID:String!,$parentPath:String!,$name:String!){ createManagedFolder(libraryID:$libraryID, parentPath:$parentPath, name:$name){ path } }","variables":{"libraryID":"library-local","parentPath":"`+root+`","name":"incoming"}}`)
	var denied response
	if err := json.Unmarshal(viewer, &denied); err != nil {
		t.Fatalf("decode viewer: %v", err)
	}
	if len(denied.Errors) != 1 || denied.Errors[0].Message != "manager role required" {
		t.Fatalf("viewer errors = %#v, want manager role required", denied.Errors)
	}

	escape := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($libraryID:String!,$parentPath:String!,$name:String!){ createManagedFolder(libraryID:$libraryID, parentPath:$parentPath, name:$name){ path } }","variables":{"libraryID":"library-local","parentPath":"`+outside+`","name":"incoming"}}`)
	var rejected response
	if err := json.Unmarshal(escape, &rejected); err != nil {
		t.Fatalf("decode escape: %v", err)
	}
	if len(rejected.Errors) != 1 || rejected.Errors[0].Message != "path is outside the library roots" {
		t.Fatalf("escape errors = %#v, want containment error", rejected.Errors)
	}
}
