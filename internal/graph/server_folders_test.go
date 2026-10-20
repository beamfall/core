package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/store/storetest"
)

func TestBrowseServerFoldersOverGraphQL(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "media"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	openManagerAdminSession(t, st)

	resp := graphPost(t, handler, authorizeManager,
		`{"query":"query($path:String,$depth:Int){ browseServerFolders(path:$path, depth:$depth){ name path kind children { name path kind } } }","variables":{"path":"`+root+`","depth":1}}`)
	var decoded struct {
		Data struct {
			BrowseServerFolders FolderEntryInfo `json:"browseServerFolders"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v (%s)", err, resp)
	}
	if len(decoded.Errors) != 0 {
		t.Fatalf("errors = %#v", decoded.Errors)
	}
	tree := decoded.Data.BrowseServerFolders
	if tree.Path == "" || tree.Kind != "folder" || len(tree.Children) != 1 {
		t.Fatalf("browseServerFolders = %#v, want one directory child and the file excluded", tree)
	}
	if tree.Children[0].Name != "media" || tree.Children[0].Path == "" || tree.Children[0].Kind != "folder" {
		t.Fatalf("child = %#v, want the media directory", tree.Children[0])
	}
}

func TestBrowseServerFoldersRequiresManagerRoleAndAdminSession(t *testing.T) {
	handler := newTestHandler(t)
	resp := graphPost(t, handler, authorizeViewer,
		`{"query":"query{ browseServerFolders{ name } }"}`)
	var decoded response
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Errors) != 1 || decoded.Errors[0].Message != "manager role required" {
		t.Fatalf("errors = %#v, want manager role required", decoded.Errors)
	}
	resp = graphPost(t, handler, authorizeManager,
		`{"query":"query{ browseServerFolders{ name } }"}`)
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Errors) != 1 || decoded.Errors[0].Message != "admin session required" {
		t.Fatalf("errors = %#v, want admin session required", decoded.Errors)
	}
}
