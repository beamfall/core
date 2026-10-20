package conformance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// mediahttp probes some of its injected dependencies for OPTIONAL extension
// interfaces at run time (`dep.(SomeInterface)`). A probe that misses is
// silent — the route quietly degrades instead of failing — so a composition
// root that forgets to implement one ships a broken feature with a green
// suite. COREAPI-AUDIT-0822-2 is exactly that failure: only the test fake
// satisfied EncryptedByteReadSeeker, so every Range request on an encrypted
// Library answered 416 in production while the tests stayed green.
//
// This gate closes the loop for future probes too: every optional interface
// mediahttp probes for must carry a compile-time assertion in the composition
// root, which is what makes the probe's success a build-time fact.
const (
	mediahttpScanRoot     = "internal/mediahttp"
	compositionRootSource = "internal/app/app.go"
)

func TestMediahttpOptionalInterfacesAreAssertedAtCompositionRoot(t *testing.T) {
	probed := mediahttpProbedOptionalInterfaces(t)
	if len(probed) == 0 {
		t.Fatalf("no optional-interface probes found under %s — the scan stopped matching the source it audits", mediahttpScanRoot)
	}
	asserted := compositionRootMediahttpAssertions(t)
	for _, name := range probed {
		if !asserted[name] {
			t.Errorf("mediahttp probes for the optional interface %s at run time, but %s carries no compile-time assertion for it.\n"+
				"Add `var _ mediahttp.%s = <adapter>{}` beside the adapter the composition root wires, or the probe misses silently in production.",
				name, compositionRootSource, name)
		}
	}
}

func TestMediahttpEncryptedByteOptionalInterfacesAreAssertedByWiredAdapter(t *testing.T) {
	probed := mediahttpProbedEncryptedByteInterfaces(t)
	if len(probed) == 0 {
		t.Fatalf("no optional-interface probes found on mediahttp's encryptedBytes dependency — the scan stopped matching the source it audits")
	}
	wired := compositionRootEncryptedByteAdapter(t)
	asserted := compositionRootMediahttpAssertionAdapters(t)
	for _, name := range probed {
		if !asserted[name][wired] {
			t.Errorf("mediahttp probes its encryptedBytes dependency for %s, but the concrete adapter passed to SetEncryptedBytes (%s) carries no matching compile-time assertion.\n"+
				"Add `var _ mediahttp.%s = %s{}` beside the wired adapter; an assertion against another concrete type does not protect production wiring.",
				name, wired, name, wired)
		}
	}
}

// mediahttpProbedOptionalInterfaces returns the sorted names of interfaces
// DECLARED in package mediahttp that the package also type-asserts for. An
// assertion to a type declared elsewhere (io.ReaderFrom) or to a concrete type
// (a context value) is not an injected-dependency probe and is skipped.
func mediahttpProbedOptionalInterfaces(t *testing.T) []string {
	t.Helper()
	files := parseMediahttpSources(t)
	declared := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			generic, ok := decl.(*ast.GenDecl)
			if !ok || generic.Tok != token.TYPE {
				continue
			}
			for _, spec := range generic.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if _, isInterface := typeSpec.Type.(*ast.InterfaceType); isInterface {
					declared[typeSpec.Name.Name] = true
				}
			}
		}
	}
	found := map[string]bool{}
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			assertion, ok := node.(*ast.TypeAssertExpr)
			if !ok || assertion.Type == nil {
				return true
			}
			ident, ok := assertion.Type.(*ast.Ident)
			if !ok || !declared[ident.Name] {
				return true
			}
			found[ident.Name] = true
			return true
		})
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// mediahttpProbedEncryptedByteInterfaces narrows the general optional-interface
// inventory to probes on the dependency populated by SetEncryptedBytes. This
// lets the composition-root gate bind each assertion to the concrete adapter
// that production actually passes, rather than accepting an assertion against
// an unrelated type.
func mediahttpProbedEncryptedByteInterfaces(t *testing.T) []string {
	t.Helper()
	optional := map[string]bool{}
	for _, name := range mediahttpProbedOptionalInterfaces(t) {
		optional[name] = true
	}
	found := map[string]bool{}
	for _, file := range parseMediahttpSources(t) {
		ast.Inspect(file, func(node ast.Node) bool {
			assertion, ok := node.(*ast.TypeAssertExpr)
			if !ok || assertion.Type == nil {
				return true
			}
			dependency, ok := assertion.X.(*ast.SelectorExpr)
			if !ok || dependency.Sel.Name != "encryptedBytes" {
				return true
			}
			name, ok := assertion.Type.(*ast.Ident)
			if ok && optional[name.Name] {
				found[name.Name] = true
			}
			return true
		})
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// compositionRootMediahttpAssertions returns the mediahttp interface names the
// composition root pins with a blank-identifier compile-time assertion.
func compositionRootMediahttpAssertions(t *testing.T) map[string]bool {
	t.Helper()
	path := filepath.Join(repoRoot(t), filepath.FromSlash(compositionRootSource))
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", compositionRootSource, err)
	}
	asserted := map[string]bool{}
	for _, decl := range file.Decls {
		generic, ok := decl.(*ast.GenDecl)
		if !ok || generic.Tok != token.VAR {
			continue
		}
		for _, spec := range generic.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || value.Type == nil {
				continue
			}
			if len(value.Names) != 1 || value.Names[0].Name != "_" {
				continue
			}
			selector, ok := value.Type.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok || pkg.Name != "mediahttp" {
				continue
			}
			asserted[selector.Sel.Name] = true
		}
	}
	return asserted
}

// compositionRootEncryptedByteAdapter returns the concrete type passed by the
// production composition root to SetEncryptedBytes. Keeping this derived from
// the call site makes a stale assertion against an unwired type gate-red.
func compositionRootEncryptedByteAdapter(t *testing.T) string {
	t.Helper()
	file := parseCompositionRoot(t)
	found := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "SetEncryptedBytes" {
			return true
		}
		adapter, ok := concreteCompositeLiteralType(call.Args[0])
		if !ok {
			t.Fatalf("%s SetEncryptedBytes argument is not a concrete composite literal", compositionRootSource)
		}
		found[adapter] = true
		return true
	})
	if len(found) != 1 {
		t.Fatalf("%s wires %d concrete SetEncryptedBytes adapter types, want exactly 1", compositionRootSource, len(found))
	}
	for adapter := range found {
		return adapter
	}
	panic("unreachable")
}

// compositionRootMediahttpAssertionAdapters indexes both sides of each
// compile-time assertion: the mediahttp interface and the asserted concrete
// adapter. The older name-only inventory remains as an independent guard.
func compositionRootMediahttpAssertionAdapters(t *testing.T) map[string]map[string]bool {
	t.Helper()
	file := parseCompositionRoot(t)
	asserted := map[string]map[string]bool{}
	for _, decl := range file.Decls {
		generic, ok := decl.(*ast.GenDecl)
		if !ok || generic.Tok != token.VAR {
			continue
		}
		for _, spec := range generic.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != "_" || len(value.Values) != 1 {
				continue
			}
			selector, ok := value.Type.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok || pkg.Name != "mediahttp" {
				continue
			}
			adapter, ok := concreteCompositeLiteralType(value.Values[0])
			if !ok {
				continue
			}
			if asserted[selector.Sel.Name] == nil {
				asserted[selector.Sel.Name] = map[string]bool{}
			}
			asserted[selector.Sel.Name][adapter] = true
		}
	}
	return asserted
}

func parseCompositionRoot(t *testing.T) *ast.File {
	t.Helper()
	path := filepath.Join(repoRoot(t), filepath.FromSlash(compositionRootSource))
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", compositionRootSource, err)
	}
	return file
}

func concreteCompositeLiteralType(expr ast.Expr) (string, bool) {
	literal, ok := expr.(*ast.CompositeLit)
	if !ok {
		return "", false
	}
	name, ok := literal.Type.(*ast.Ident)
	if !ok {
		return "", false
	}
	return name.Name, true
}

func parseMediahttpSources(t *testing.T) []*ast.File {
	t.Helper()
	dir := requireRoot(t, mediahttpScanRoot)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", mediahttpScanRoot, err)
	}
	fileSet := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fileSet, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, parsed)
	}
	if len(files) == 0 {
		t.Fatalf("no non-test sources under %s", mediahttpScanRoot)
	}
	return files
}
