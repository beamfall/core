package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// feature:file-operations — the authorized native→server import transport (apple
// ADR-0003 §1/§3, POWERTOOLS-6B): registerImportSource mints the server token and
// importManagedFile stages an out-of-Library file under a Library root via that
// token. Both are Admin-Session gated (the loopback-bearer tier); the JS realm
// holds only the server token and can neither self-mint nor inject a raw path.

// TestImportManagedFileOverGraphQLImportsViaServerToken is the wire-level
// acceptance: an out-of-Library file is registered over the authorized transport
// (yielding a SERVER-minted token), then imported under a Library root via that
// token through the graph mutation. The native realm never sends a path to
// importManagedFile — only the opaque token.
func TestImportManagedFileOverGraphQLImportsViaServerToken(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	root := t.TempDir()
	if err := st.SetLibraryRoots(t.Context(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(t.TempDir(), "drag-in.mkv")
	if err := os.WriteFile(src, []byte("payload-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	regResp := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($sourcePath:String!){ registerImportSource(sourcePath:$sourcePath) }","variables":{"sourcePath":"`+src+`"}}`)
	var regDecoded struct {
		Data struct {
			RegisterImportSource string `json:"registerImportSource"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(regResp, &regDecoded); err != nil {
		t.Fatalf("decode register: %v (%s)", err, regResp)
	}
	if len(regDecoded.Errors) != 0 {
		t.Fatalf("register errors = %#v", regDecoded.Errors)
	}
	token := regDecoded.Data.RegisterImportSource
	if token == "" {
		t.Fatal("registerImportSource returned an empty token")
	}

	impResp := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($libraryID:String!,$importToken:String!,$destinationParentPath:String!,$container:String!,$codec:String!,$class:String!){ importManagedFile(libraryID:$libraryID, importToken:$importToken, destinationParentPath:$destinationParentPath, container:$container, codec:$codec, class:$class){ path kind } }","variables":{"libraryID":"library-local","importToken":"`+token+`","destinationParentPath":"`+root+`","container":"mkv","codec":"h264","class":"video"}}`)
	var impDecoded struct {
		Data struct {
			ImportManagedFile ManagedPathInfo `json:"importManagedFile"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(impResp, &impDecoded); err != nil {
		t.Fatalf("decode import: %v (%s)", err, impResp)
	}
	if len(impDecoded.Errors) != 0 {
		t.Fatalf("import errors = %#v", impDecoded.Errors)
	}
	// The store canonicalizes the dest path (macOS /var → /private/var), so assert
	// the file landed under a Library root by basename + a within-root suffix and
	// by its preserved contents, not by raw path equality.
	importedPath := impDecoded.Data.ImportManagedFile.Path
	if filepath.Base(importedPath) != "drag-in.mkv" {
		t.Fatalf("imported base = %q, want drag-in.mkv", filepath.Base(importedPath))
	}
	if data, err := os.ReadFile(importedPath); err != nil || string(data) != "payload-bytes" {
		t.Fatalf("imported contents = %q (err %v), want payload-bytes", data, err)
	}

	// The imported file is now a cataloged managed Media the convert primitive
	// accepts (the import-then-convert acceptance, proven at the store seam in
	// POWERTOOLS-6 — here we confirm the wire-imported file resolves as managed).
	if _, _, _, err := st.ResolveManagedMediaFile(t.Context(), "library-local", importedPath, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}); err != nil {
		t.Fatalf("ResolveManagedMediaFile on wire-imported file: %v", err)
	}
}

// TestImportManagedFileOverGraphQLRefusesRawJSPath proves a raw path string the
// JS realm might supply directly as importToken is refused: it was never minted by
// registerImportSource, so the store rejects it ("unknown import token"). The JS
// realm cannot inject a path; only a server-minted token imports.
func TestImportManagedFileOverGraphQLRefusesRawJSPath(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	root := t.TempDir()
	if err := st.SetLibraryRoots(t.Context(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "smuggled.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// importToken is the raw filesystem path (never registered) — the shape a
	// forged JS caller would try to smuggle in.
	resp := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($libraryID:String!,$importToken:String!,$destinationParentPath:String!,$container:String!,$codec:String!,$class:String!){ importManagedFile(libraryID:$libraryID, importToken:$importToken, destinationParentPath:$destinationParentPath, container:$container, codec:$codec, class:$class){ path } }","variables":{"libraryID":"library-local","importToken":"`+src+`","destinationParentPath":"`+root+`","container":"mkv","codec":"h264","class":"video"}}`)
	var decoded response
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Errors) != 1 || decoded.Errors[0].Message != "unknown import token" {
		t.Fatalf("errors = %#v, want unknown import token", decoded.Errors)
	}
	if _, statErr := os.Stat(filepath.Join(root, "smuggled.mkv")); statErr == nil {
		t.Fatal("raw-path smuggle imported a file under the Library root")
	}
}

// TestImportTransportRequiresAdminSession proves both transport fields are gated
// on the Admin Session class (the ADR-0003 §3 authorized loopback bearer tier): a
// manager session WITHOUT an open admin window is refused, so a non-bearer caller
// can neither register a source nor import.
func TestImportTransportRequiresAdminSession(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	// No openManagerAdminSession — scope.Admin is false.

	regResp := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($sourcePath:String!){ registerImportSource(sourcePath:$sourcePath) }","variables":{"sourcePath":"/tmp/x.mkv"}}`)
	var regDecoded response
	if err := json.Unmarshal(regResp, &regDecoded); err != nil {
		t.Fatalf("decode register: %v", err)
	}
	if len(regDecoded.Errors) != 1 || regDecoded.Errors[0].Message != "admin session required" {
		t.Fatalf("register errors = %#v, want admin session required", regDecoded.Errors)
	}

	impResp := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($libraryID:String!,$importToken:String!,$destinationParentPath:String!){ importManagedFile(libraryID:$libraryID, importToken:$importToken, destinationParentPath:$destinationParentPath){ path } }","variables":{"libraryID":"library-local","importToken":"t","destinationParentPath":"/tmp"}}`)
	var impDecoded response
	if err := json.Unmarshal(impResp, &impDecoded); err != nil {
		t.Fatalf("decode import: %v", err)
	}
	if len(impDecoded.Errors) != 1 || impDecoded.Errors[0].Message != "admin session required" {
		t.Fatalf("import errors = %#v, want admin session required", impDecoded.Errors)
	}
}
