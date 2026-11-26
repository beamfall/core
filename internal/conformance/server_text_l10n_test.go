package conformance

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/l10n"
)

// Server text localization guard (CORE-2102). The classification lives in
// docs/architecture/server-text-localization.md: server text is either (a) a
// stable code or diagnostic the client localizes, or (b) text rendered from
// internal/l10n catalogs in the request locale. This test fails when a change
// adds user-facing English to the server outside both.

// serverTextExempt are trees that may hold English literals: the catalogs
// themselves, test fixtures and the web app (localized client-side).
var serverTextExempt = []string{
	"internal/l10n/",
	"internal/apptest/",
	"internal/web/",
}

// knownLocaleMaps lists any hand-written plugin.LocaleString literals in core
// that predate CORE-2102. All default manifest titles now render from
// internal/l10n (CORE-2123). Do not add to this list; put new text in internal/l10n.
var knownLocaleMaps = map[string]bool{}

// knownSentenceMessages are sentence-style literals passed to a message
// writer that predate CORE-2102 and are classified (a) in the inventory doc:
// clients match their exact English text. Key: file and the literal. Do not
// add to this list; give new messages a code or a catalog key.
var knownSentenceMessages = map[string]bool{
	// Mapped to a code by graph.ClientErrorCode; the web shows its own copy.
	"internal/graph/graph_watchparty.go|Watch Party is available on your home network only": true,
	// The web checks the length first and shows its own copy; a REST code is a follow-up.
	"internal/store/admin.go|Admin password must be at least {} characters.": true,
}

// textMessageWriters are the calls whose string argument reaches a user as the
// message text of a refusal.
var textMessageWriters = map[string]bool{
	"writeError":      true,
	"writeCodedError": true,
	"clientError":     true,
	"ValidationError": true,
	"Error":           true, // http.Error only; see checkMessageCall
}

// textSentence matches prose written for a person: a capitalised word (not an
// acronym such as "API") followed by at least two more words. Diagnostics in
// this code base are lowercase fragments ("media not found") that clients map
// by HTTP status or extensions.code.
var textSentence = regexp.MustCompile(`^[A-Z][a-z'’]* \S+ \S+`)

var (
	textVerb        = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z]`)
	textPlaceholder = regexp.MustCompile(`\{[a-zA-Z]+\}`)
)

var textStableCode = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// serverTextScan holds one run of the guard over a set of files.
type serverTextScan struct {
	shipped        map[string]bool
	cataloged      map[string]bool
	seenLocaleMaps map[string]bool
	seenSentences  map[string]bool
	failures       []string
}

func newServerTextScan(t *testing.T) *serverTextScan {
	scan := &serverTextScan{
		shipped:        map[string]bool{},
		cataloged:      textCatalogedTemplates(t),
		seenLocaleMaps: map[string]bool{},
		seenSentences:  map[string]bool{},
	}
	for _, tag := range l10n.Shipped() {
		scan.shipped[tag] = true
	}
	return scan
}

func (scan *serverTextScan) file(rel string, fset *token.FileSet, file *ast.File) {
	consts := textFileConsts(file)
	fail := func(n ast.Node, format string, args ...any) {
		scan.failures = append(scan.failures, fmt.Sprintf("%s:%d: ", rel, fset.Position(n.Pos()).Line)+fmt.Sprintf(format, args...))
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CompositeLit:
			checkNotificationLiteral(n, fail)
			checkLocaleMapLiteral(rel, n, scan.shipped, scan.seenLocaleMaps, fail)
		case *ast.CallExpr:
			checkMessageCall(rel, n, consts, scan.cataloged, scan.seenSentences, fail)
		}
		return true
	})
}

func TestServerTextIsCodedOrCataloged(t *testing.T) {
	root := repoRoot(t)
	scan := newServerTextScan(t)
	scanned := 0
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(requireRoot(t, dir), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			if entry.IsDir() {
				for _, prefix := range serverTextExempt {
					if rel+"/" == prefix {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			scanned++
			scan.file(rel, fset, file)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned == 0 {
		t.Fatal("server text guard scanned zero files")
	}
	failures := scan.failures
	for key := range knownLocaleMaps {
		if !scan.seenLocaleMaps[key] {
			failures = append(failures, fmt.Sprintf("knownLocaleMaps entry %q no longer exists; remove it", key))
		}
	}
	for key := range knownSentenceMessages {
		if !scan.seenSentences[key] {
			failures = append(failures, fmt.Sprintf("knownSentenceMessages entry %q no longer exists; remove it", key))
		}
	}
	sort.Strings(failures)
	for _, f := range failures {
		t.Error(f)
	}
	if len(failures) > 0 {
		t.Log("Server text must be a stable code the client localizes or a catalog key in internal/l10n; see docs/architecture/server-text-localization.md")
	}
}

// TestServerTextGuardCatchesViolations proves each rule fires, so the scan
// above cannot pass vacuously.
func TestServerTextGuardCatchesViolations(t *testing.T) {
	const src = `package probe

const badCode = "Not A Code"
const goodCode = "lan_hostname_not_allowed"

func probe(w http.ResponseWriter, r *http.Request, path string) {
	_ = store.Notification{Title: "New request"}
	_ = store.Notification{Title: storedNotificationTitle(kind)}
	_ = plugin.LocaleString{"en": "Shelf", "de": "Regal"}
	_ = map[string]string{"en": "Shelf", "fr": "Étagère"}
	_ = map[string]string{"en": "only one locale"}
	writeError(w, r, 400, "Your library could not be saved.")
	writeError(w, r, 400, "media not found")
	writeError(w, r, 400, "API key missing required scope: "+path)
	_ = clientError(fmt.Sprintf("Choose a folder under %s first", path))
	_ = store.ValidationError("The folder " + path + " doesn't exist on the server.")
	writeCodedError(w, 409, "Open it by address.", goodCode, nil)
	writeCodedError(w, 409, msg, badCode, nil)
	writeCodedError(w, 409, msg, goodCode, nil)
	http.Error(w, "Something went wrong here", 500)
	t.Error("This is not a response at all")
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "probe.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	scan := newServerTextScan(t)
	scan.file("internal/probe/probe.go", fset, file)
	want := []string{
		`probe.go:7: notification Title is the English literal "New request"`,
		`probe.go:9: hand-written locale map for "Shelf"`,
		`probe.go:10: hand-written locale map for "Shelf"`,
		`probe.go:12: user-facing sentence "Your library could not be saved."`,
		`probe.go:15: user-facing sentence "Choose a folder under {} first"`,
		`probe.go:17: writeCodedError message "Open it by address."`,
		`probe.go:17: user-facing sentence "Open it by address."`,
		`probe.go:18: writeCodedError needs a stable snake_case code`,
		`probe.go:20: user-facing sentence "Something went wrong here"`,
	}
	got := strings.Join(scan.failures, "\n")
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("guard missed %s", w)
		}
	}
	if len(scan.failures) != len(want) {
		t.Errorf("guard reported %d findings, want %d:\n%s", len(scan.failures), len(want), got)
	}
}

// checkNotificationLiteral refuses a store.Notification whose Title is
// written as an English literal; titles come from the catalog by kind.
func checkNotificationLiteral(lit *ast.CompositeLit, fail func(ast.Node, string, ...any)) {
	if textTypeName(lit.Type) != "Notification" {
		return
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Title" {
			if s, ok := textFirstLiteral(kv.Value); ok {
				fail(kv, "notification Title is the English literal %q; add a title key to internal/l10n and graph's notificationTitleKeys", s)
			}
		}
	}
}

// checkLocaleMapLiteral refuses a new hand-written translation table: a
// LocaleString literal, or any map literal keyed by two or more shipped
// locale tags.
func checkLocaleMapLiteral(rel string, lit *ast.CompositeLit, shipped map[string]bool, seen map[string]bool, fail func(ast.Node, string, ...any)) {
	isLocaleString := textTypeName(lit.Type) == "LocaleString"
	if !isLocaleString {
		if _, ok := lit.Type.(*ast.MapType); !ok {
			return
		}
	}
	tags, en := 0, ""
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := textLiteral(kv.Key)
		if !ok || !shipped[key] {
			continue
		}
		tags++
		if key == l10n.Source() {
			en, _ = textLiteral(kv.Value)
		}
	}
	if tags == 0 || (!isLocaleString && tags < 2) {
		return
	}
	id := rel + "|" + en
	seen[id] = true
	if !knownLocaleMaps[id] {
		fail(lit, "hand-written locale map for %q; render the text from an internal/l10n catalog instead", en)
	}
}

// checkMessageCall refuses a sentence-style English message passed to a
// refusal writer unless it is the source text of a catalog entry, and a
// writeCodedError with a literal message or without a stable snake_case code.
func checkMessageCall(rel string, call *ast.CallExpr, consts map[string]string, cataloged, seen map[string]bool, fail func(ast.Node, string, ...any)) {
	name := textTypeName(call.Fun)
	if !textMessageWriters[name] {
		return
	}
	if sel, ok := call.Fun.(*ast.SelectorExpr); name == "Error" && (!ok || textTypeName(sel.X) != "http") {
		return
	}
	if name == "writeCodedError" && len(call.Args) >= 4 {
		if s, ok := textFirstLiteral(call.Args[2]); ok {
			fail(call, "writeCodedError message %q is an English literal; clients branch on the code, so render the message from an internal/l10n catalog", s)
		}
		code, ok := textLiteral(call.Args[3])
		if ident, isIdent := call.Args[3].(*ast.Ident); isIdent {
			code, ok = consts[ident.Name]
		}
		if !ok || !textStableCode.MatchString(code) {
			fail(call, "writeCodedError needs a stable snake_case code (a literal or a constant in the same file), got %s", textExprString(call.Args[3]))
		}
	}
	for _, arg := range call.Args {
		template, ok := textTemplate(arg)
		if !ok || !textSentence.MatchString(template) || cataloged[template] {
			continue
		}
		id := rel + "|" + template
		seen[id] = true
		if !knownSentenceMessages[id] {
			fail(arg, "user-facing sentence %q passed to %s; give it a stable code the client localizes, or a catalog key in internal/l10n", template, name)
		}
	}
}

// textTemplate renders a message argument as its source template: literals
// verbatim, other operands of a + concatenation as {}, and a fmt.Sprintf or
// fmt.Errorf format with its verbs as {}. It reports false when no literal is
// involved.
func textTemplate(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return textLiteral(e)
	case *ast.ParenExpr:
		return textTemplate(e.X)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		left, lok := textTemplate(e.X)
		right, rok := textTemplate(e.Y)
		if !lok && !rok {
			return "", false
		}
		if !lok {
			left = "{}"
		}
		if !rok {
			right = "{}"
		}
		return left + right, true
	case *ast.CallExpr:
		if name := textTypeName(e.Fun); (name == "Sprintf" || name == "Errorf") && len(e.Args) > 0 {
			if format, ok := textLiteral(e.Args[0]); ok {
				return textVerb.ReplaceAllString(format, "{}"), true
			}
		}
	}
	return "", false
}

// textCatalogedTemplates are the source catalog's templates with each
// placeholder written as {}, the form textTemplate produces.
func textCatalogedTemplates(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(requireRoot(t, "internal/l10n/catalog"), l10n.Source()+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var catalog map[string]string
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	templates := map[string]bool{}
	for _, text := range catalog {
		templates[textPlaceholder.ReplaceAllString(text, "{}")] = true
	}
	return templates
}

// textFileConsts collects a file's string constants, for codes named by a
// constant.
func textFileConsts(file *ast.File) map[string]string {
	consts := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if i < len(vs.Values) {
					if value, ok := textLiteral(vs.Values[i]); ok {
						consts[name.Name] = value
					}
				}
			}
		}
	}
	return consts
}

// textTypeName returns the bare identifier of an expression such as store.X or X.
func textTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	}
	return ""
}

func textLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

func textFirstLiteral(expr ast.Expr) (string, bool) {
	var found string
	ok := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if ok {
			return false
		}
		if s, isLit := textLiteral(textAsExpr(n)); isLit && s != "" {
			found, ok = s, true
			return false
		}
		return true
	})
	return found, ok
}

func textAsExpr(n ast.Node) ast.Expr {
	e, _ := n.(ast.Expr)
	return e
}

func textExprString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.BasicLit:
		return e.Value
	}
	return fmt.Sprintf("%T", expr)
}
