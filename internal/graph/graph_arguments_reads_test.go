package graph

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// resolverReadAnalysis is a syntactic call graph of this package's production
// sources, enough to find every resolver variable read without type checking.
type resolverReadAnalysis struct {
	funcs     map[string]*ast.FuncDecl   // package functions by name
	methods   map[string][]*ast.FuncDecl // methods by name, any receiver
	imports   map[*ast.FuncDecl]map[string]bool
	accessors map[*ast.FuncDecl]map[int]string // param index -> kind
	direct    map[*ast.FuncDecl][]guardedRead
	dynamic   map[*ast.FuncDecl][]string
	escapes   map[*ast.FuncDecl][]string // Variables or an accessor used as a value
	calls     map[*ast.FuncDecl][]guardedCall
}

// fieldGuard is the set of top-level fields an `if fields["x"] { ... }` body
// runs for; nil means the code runs for every field reaching it.
type fieldGuard map[string]bool

func (g fieldGuard) admits(field string) bool { return g == nil || g[field] }

type guardedRead struct {
	name, kind string
	guard      fieldGuard
}

type guardedCall struct {
	decl  *ast.FuncDecl
	guard fieldGuard
}

// accessorKinds names the JSON kind each typed variable helper expects.
var accessorKinds = map[string]string{
	"stringVar":         "string",
	"trimmedVariable":   "string",
	"boolVar":           "boolean",
	"intVar":            "int",
	"boundedIntVar":     "int",
	"nonNegativeIntVar": "int",
	"stringSliceVar":    "list",
	"stringMapVar":      "map",
	"anyMapVar":         "object",
	"Bool":              "boolean",
	"Number":            "number",
	"String":            "string",
	"StringList":        "list",
}

func analyzeResolverReads(t testing.TB) *resolverReadAnalysis {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	a := &resolverReadAnalysis{
		funcs:     map[string]*ast.FuncDecl{},
		methods:   map[string][]*ast.FuncDecl{},
		imports:   map[*ast.FuncDecl]map[string]bool{},
		accessors: map[*ast.FuncDecl]map[int]string{},
		direct:    map[*ast.FuncDecl][]guardedRead{},
		dynamic:   map[*ast.FuncDecl][]string{},
		escapes:   map[*ast.FuncDecl][]string{},
		calls:     map[*ast.FuncDecl][]guardedCall{},
	}
	fset := token.NewFileSet()
	var decls []*ast.FuncDecl
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		imported := map[string]bool{}
		for _, spec := range file.Imports {
			name := filepath.Base(strings.Trim(spec.Path.Value, `"`))
			if spec.Name != nil {
				name = spec.Name.Name
			}
			imported[name] = true
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			decls = append(decls, fn)
			a.imports[fn] = imported
			if fn.Recv == nil {
				a.funcs[fn.Name.Name] = fn
			} else {
				a.methods[fn.Name.Name] = append(a.methods[fn.Name.Name], fn)
			}
		}
	}
	for _, fn := range decls {
		a.calls[fn] = a.references(fn)
	}
	// Accessors: functions whose string parameter becomes a variable name,
	// directly or by forwarding to another accessor. Iterate to a fixed point.
	for changed := true; changed; {
		changed = false
		for _, fn := range decls {
			params := paramIndexes(fn)
			a.walkReads(fn, func(key ast.Expr, kind string, _ fieldGuard) {
				if ident, ok := key.(*ast.Ident); ok {
					if index, ok := params[ident.Name]; ok {
						if a.accessors[fn] == nil {
							a.accessors[fn] = map[int]string{}
						}
						if _, seen := a.accessors[fn][index]; !seen {
							a.accessors[fn][index] = kind
							changed = true
						}
					}
				}
			})
		}
	}
	for _, fn := range decls {
		params := paramIndexes(fn)
		a.walkReads(fn, func(key ast.Expr, kind string, guard fieldGuard) {
			if lit, ok := key.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				name, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				a.direct[fn] = append(a.direct[fn], guardedRead{name: name, kind: kind, guard: guard})
				return
			}
			if ident, ok := key.(*ast.Ident); ok {
				if _, ok := params[ident.Name]; ok {
					return
				}
			}
			a.dynamic[fn] = append(a.dynamic[fn], fset.Position(key.Pos()).String())
		})
		a.walkEscapes(fn, func(node ast.Node) {
			a.escapes[fn] = append(a.escapes[fn], fset.Position(node.Pos()).String())
		})
	}
	return a
}

// walkEscapes reports each place fn hands request variables on in a form the
// read analysis cannot follow: the Variables map (or the vars helper's values)
// used other than by indexing, and an accessor function named other than as a
// call.
func (a *resolverReadAnalysis) walkEscapes(fn *ast.FuncDecl, report func(ast.Node)) {
	var stack []ast.Node
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		defer func() { stack = append(stack, node) }()
		var parent ast.Node
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		switch node := node.(type) {
		case *ast.SelectorExpr:
			if node.Sel.Name != "Variables" && node.Sel.Name != "values" {
				return true
			}
			if index, ok := parent.(*ast.IndexExpr); ok && index.X == node {
				return true
			}
			if assign, ok := parent.(*ast.AssignStmt); ok && len(assign.Lhs) == 1 && assign.Lhs[0] == node {
				return true
			}
			report(node)
		case *ast.Ident:
			if call, ok := parent.(*ast.CallExpr); ok && call.Fun == node {
				return true
			}
			if _, ok := parent.(*ast.SelectorExpr); ok {
				return true // a field or method name; methods are not told apart from fields untyped
			}
			for _, callee := range a.resolve(fn, node) {
				if len(a.accessors[callee]) > 0 {
					report(node)
					return true
				}
			}
		}
		return true
	})
}

func mergeReadKind(prior, next string) string {
	if prior == "" || prior == "any" {
		return next
	}
	return prior
}

func paramIndexes(fn *ast.FuncDecl) map[string]int {
	out := map[string]int{}
	index := 0
	for _, field := range fn.Type.Params.List {
		if len(field.Names) == 0 {
			index++
			continue
		}
		for _, name := range field.Names {
			out[name.Name] = index
			index++
		}
	}
	return out
}

// resolve maps a call or reference expression to this package's declarations.
// Method references resolve by name to every receiver, which over-approximates.
func (a *resolverReadAnalysis) resolve(fn *ast.FuncDecl, expr ast.Expr) []*ast.FuncDecl {
	switch expr := expr.(type) {
	case *ast.Ident:
		if decl, ok := a.funcs[expr.Name]; ok {
			return []*ast.FuncDecl{decl}
		}
	case *ast.SelectorExpr:
		if pkg, ok := expr.X.(*ast.Ident); ok && a.imports[fn][pkg.Name] {
			return nil
		}
		return a.methods[expr.Sel.Name]
	case *ast.IndexExpr: // generic instantiation
		return a.resolve(fn, expr.X)
	case *ast.IndexListExpr:
		return a.resolve(fn, expr.X)
	}
	return nil
}

func (a *resolverReadAnalysis) references(fn *ast.FuncDecl) []guardedCall {
	var out []guardedCall
	var stack []ast.Node
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		var targets []*ast.FuncDecl
		switch node := node.(type) {
		case *ast.Ident:
			targets = a.resolve(fn, node)
		case *ast.SelectorExpr:
			targets = a.resolve(fn, node)
		}
		guard := guardOf(stack)
		for _, target := range targets {
			out = append(out, guardedCall{decl: target, guard: guard})
		}
		stack = append(stack, node)
		return true
	})
	return out
}

// guardOf intersects the field guards of every if-body enclosing the node
// whose ancestors are stack.
func guardOf(stack []ast.Node) fieldGuard {
	var guard fieldGuard
	for i := 0; i+1 < len(stack); i++ {
		stmt, ok := stack[i].(*ast.IfStmt)
		if !ok || stack[i+1] != stmt.Body {
			continue
		}
		fields := conditionFields(stmt.Cond)
		if fields == nil {
			continue
		}
		if guard == nil {
			guard = fields
			continue
		}
		next := fieldGuard{}
		for field := range guard {
			if fields[field] {
				next[field] = true
			}
		}
		guard = next
	}
	return guard
}

// conditionFields returns the fields a condition requires one of: a
// disjunction of fields["x"] tests, or a conjunction containing one.
func conditionFields(cond ast.Expr) fieldGuard {
	switch cond := cond.(type) {
	case *ast.ParenExpr:
		return conditionFields(cond.X)
	case *ast.IndexExpr:
		if ident, ok := cond.X.(*ast.Ident); ok && ident.Name == "fields" {
			if lit, ok := cond.Index.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				name, _ := strconv.Unquote(lit.Value)
				return fieldGuard{name: true}
			}
		}
		if selector, ok := cond.X.(*ast.SelectorExpr); ok && selector.Sel.Name == "fields" {
			if lit, ok := cond.Index.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				name, _ := strconv.Unquote(lit.Value)
				return fieldGuard{name: true}
			}
		}
	case *ast.BinaryExpr:
		left, right := conditionFields(cond.X), conditionFields(cond.Y)
		switch cond.Op {
		case token.LOR:
			if left == nil || right == nil {
				return nil
			}
			for field := range right {
				left[field] = true
			}
			return left
		case token.LAND:
			if left != nil {
				return left
			}
			return right
		}
	}
	return nil
}

// walkReads reports every variable-name expression fn reads: indexing a
// request's Variables (or the vars helper's values), and each argument passed
// in an accessor's name position.
func (a *resolverReadAnalysis) walkReads(fn *ast.FuncDecl, report func(key ast.Expr, kind string, guard fieldGuard)) {
	var stack []ast.Node
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		defer func() { stack = append(stack, node) }()
		switch node := node.(type) {
		case *ast.IndexExpr:
			selector, ok := node.X.(*ast.SelectorExpr)
			if !ok || (selector.Sel.Name != "Variables" && selector.Sel.Name != "values") {
				return true
			}
			report(node.Index, assertedKind(stack, node), guardOf(append(stack, node)))
		case *ast.CallExpr:
			for _, callee := range a.resolve(fn, node.Fun) {
				for index, kind := range a.accessors[callee] {
					if index >= len(node.Args) {
						continue
					}
					if known, ok := accessorKinds[callee.Name.Name]; ok {
						kind = known
					}
					report(node.Args[index], kind, guardOf(append(stack, node)))
				}
			}
		}
		return true
	})
}

// assertedKind reads the shape a raw Variables lookup is asserted to.
func assertedKind(stack []ast.Node, index *ast.IndexExpr) string {
	if len(stack) == 0 {
		return "any"
	}
	assert, ok := stack[len(stack)-1].(*ast.TypeAssertExpr)
	if !ok || assert.X != index || assert.Type == nil {
		return "any"
	}
	switch typ := assert.Type.(type) {
	case *ast.Ident:
		switch typ.Name {
		case "string":
			return "string"
		case "bool":
			return "boolean"
		case "float64":
			return "number"
		}
	case *ast.ArrayType:
		return "list"
	case *ast.MapType:
		return "object"
	}
	return "any"
}

// transitiveReads unions the reads of every declaration reachable from roots
// for one field, skipping code guarded to other fields.
func (a *resolverReadAnalysis) transitiveReads(field string, roots []*ast.FuncDecl) (map[string]string, []string) {
	reads := map[string]string{}
	var dynamic []string
	seen := map[*ast.FuncDecl]bool{}
	queue := slices.Clone(roots)
	for len(queue) > 0 {
		fn := queue[0]
		queue = queue[1:]
		if seen[fn] {
			continue
		}
		seen[fn] = true
		for _, read := range a.direct[fn] {
			if read.guard.admits(field) {
				reads[read.name] = mergeReadKind(reads[read.name], read.kind)
			}
		}
		for _, position := range a.dynamic[fn] {
			dynamic = append(dynamic, fn.Name.Name+" "+position)
		}
		for _, call := range a.calls[fn] {
			if call.guard.admits(field) {
				queue = append(queue, call.decl)
			}
		}
	}
	return reads, dynamic
}

// fieldResolverReads maps each registered top-level field to the variables
// its dispatch blocks can read.
func fieldResolverReads(t testing.TB) (map[string]map[string]string, map[string][]string) {
	t.Helper()
	a := analyzeResolverReads(t)
	reads := map[string]map[string]string{}
	dynamic := map[string][]string{}
	for field, handler := range fieldHandlerRegistry {
		var roots []*ast.FuncDecl
		for _, order := range handler.run {
			name := fmt.Sprintf("dispatchFieldBlock%03d", order)
			decl, ok := a.funcs[name]
			if !ok {
				t.Fatalf("field %s order %d has no %s", field, order, name)
			}
			roots = append(roots, decl)
		}
		reads[field], dynamic[field] = a.transitiveReads(field, roots)
	}
	return reads, dynamic
}

// dynamicArgumentReads lists the functions that read request variables
// through a computed key, with every name each one reads. The analysis cannot
// see those names, so a function that gains one must list it here too.
var dynamicArgumentReads = map[string][]string{
	"sessionControlArgsFromVariables": {"direction", "mediaID", "text"},
	"profileSettingsFromVariables":    {"audioLanguages", "disabled", "preferSDH", "subtitleLanguages", "transcodeAllowed"},
	"setTranscodeConfig":              {"codec", "device", "quality"},
}

// rangedTableNames returns the string names in the literal tables fn ranges
// over: map keys, and the leading string of each struct element.
func rangedTableNames(fn *ast.FuncDecl) []string {
	var names []string
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		loop, ok := node.(*ast.RangeStmt)
		if !ok {
			return true
		}
		table, ok := loop.X.(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, elt := range table.Elts {
			var key ast.Expr
			switch elt := elt.(type) {
			case *ast.KeyValueExpr:
				key = elt.Key
			case *ast.CompositeLit:
				if len(elt.Elts) > 0 {
					key = elt.Elts[0]
				}
			}
			if lit, ok := key.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if name, err := strconv.Unquote(lit.Value); err == nil {
					names = append(names, name)
				}
			}
		}
		return true
	})
	slices.Sort(names)
	return names
}

// TestDynamicArgumentReadsMatchTheirTables: each dynamicArgumentReads entry
// lists exactly the names its function's ranged tables hold, so a name added
// to one of those tables cannot go unlisted.
func TestDynamicArgumentReadsMatchTheirTables(t *testing.T) {
	a := analyzeResolverReads(t)
	for name, want := range dynamicArgumentReads {
		fns := a.methods[name]
		if fn, ok := a.funcs[name]; ok {
			fns = append(fns, fn)
		}
		if len(fns) != 1 {
			t.Errorf("dynamicArgumentReads names %q, which matches %d functions", name, len(fns))
			continue
		}
		want = slices.Sorted(slices.Values(want))
		if got := rangedTableNames(fns[0]); !slices.Equal(got, want) {
			t.Errorf("%s ranges over %q, dynamicArgumentReads lists %q", name, got, want)
		}
	}
}

// variableHandoffs are the functions allowed to pass the Variables map on:
// prepareOperation builds each field's inputs from it, and newVars wraps it for
// the vars accessors the analysis follows.
var variableHandoffs = map[string]bool{"prepareOperation": true, "newVars": true}

// TestResolverVariablesDoNotEscapeAnalysis: request variables reach resolvers
// only through indexing and accessor calls the analysis follows. A function
// that passes the Variables map on, or takes an accessor as a value, hides its
// reads; read the variables directly instead.
func TestResolverVariablesDoNotEscapeAnalysis(t *testing.T) {
	a := analyzeResolverReads(t)
	for fn, sites := range a.escapes {
		if variableHandoffs[fn.Name.Name] {
			continue
		}
		for _, site := range sites {
			t.Errorf("%s hands request variables on at %s, out of the argument analysis's sight", fn.Name.Name, site)
		}
	}
}

// TestFieldArgumentRegistryCoversResolverReads: fieldArgumentRegistry names
// exactly the dispatched fields, keeps every wire-schema argument, and lists
// every argument each field's resolvers read, with a matching kind, and no
// argument they never read (CORE-2107).
func TestFieldArgumentRegistryCoversResolverReads(t *testing.T) {
	for field := range fieldHandlerRegistry {
		if _, ok := fieldArgumentRegistry[field]; !ok {
			t.Errorf("fieldArgumentRegistry has no entry for dispatched field %q", field)
		}
	}
	for field := range fieldArgumentRegistry {
		if _, ok := fieldHandlerRegistry[field]; !ok && !suppressedDispatchFields[field] {
			t.Errorf("fieldArgumentRegistry[%q] names no dispatched field", field)
		}
	}
	reads, dynamic := fieldResolverReads(t)
	for field, args := range fieldArgumentRegistry {
		if suppressedDispatchFields[field] {
			continue // an App Store build prunes its resolvers; the default build checks it
		}
		listed := map[string]NestedArgument{}
		for _, arg := range args {
			if _, dup := listed[arg.Name]; dup {
				t.Errorf("fieldArgumentRegistry[%q] lists %q twice", field, arg.Name)
			}
			listed[arg.Name] = arg
		}
		wire := map[string]bool{}
		for _, arg := range fieldArguments[field] {
			wire[arg.Name] = true
			if got, ok := listed[arg.Name]; !ok || got.Kind != arg.Kind || got.Optional != arg.Optional {
				t.Errorf("fieldArgumentRegistry[%q] drops or changes wire argument %q", field, arg.Name)
			}
		}
		read := map[string]string{}
		for name, kind := range reads[field] {
			read[name] = kind
		}
		for _, site := range dynamic[field] {
			fn, _, _ := strings.Cut(site, " ")
			names, ok := dynamicArgumentReads[fn]
			if !ok {
				t.Errorf("%s reads a request variable through a computed key at %s; list its names in dynamicArgumentReads", field, site)
				continue
			}
			for _, name := range names {
				read[name] = mergeReadKind(read[name], "any")
			}
		}
		for name, kind := range read {
			arg, ok := listed[name]
			switch {
			case !ok:
				t.Errorf("field %q reads argument %q (%s), which fieldArgumentRegistry does not list", field, name, kind)
			case !wire[name] && kind != "any" && arg.Kind != kind:
				t.Errorf("fieldArgumentRegistry[%q] lists %q as %s, resolver reads it as %s", field, name, arg.Kind, kind)
			}
		}
		for name := range listed {
			if _, ok := read[name]; !ok && !wire[name] {
				t.Errorf("fieldArgumentRegistry[%q] lists %q, which no resolver of the field reads", field, name)
			}
		}
	}
}
