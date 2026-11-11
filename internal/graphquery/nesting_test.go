package graphquery

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// maxRequestBody mirrors maxGraphQLBody in package graph (which imports this
// package): the largest document an anonymous /api/graphql request can carry.
const maxRequestBody = 256 << 10

// nestedSelections returns a document whose selection sets nest depth levels.
func nestedSelections(depth int) string {
	return "query { " + strings.Repeat("a { ", depth-1) + "leaf" + strings.Repeat(" }", depth)
}

// nestedList returns a document whose argument list nests depth levels.
func nestedList(depth int) string {
	return "query { f(a: " + strings.Repeat("[", depth) + "1" + strings.Repeat("]", depth) + ") }"
}

// nestedObject returns a document whose input object nests depth levels.
func nestedObject(depth int) string {
	return "query { f(a: " + strings.Repeat("{b: ", depth) + "1" + strings.Repeat("}", depth) + ") }"
}

// nestedListType returns a document whose variable type nests depth levels.
func nestedListType(depth int) string {
	return "query($v: " + strings.Repeat("[", depth) + "Int" + strings.Repeat("]", depth) + ") { f(a: $v) }"
}

func TestScanNesting(t *testing.T) {
	tests := map[string]struct {
		query string
		want  nesting
	}{
		"shorthand":            {`{ serverInfo }`, nesting{selection: 1}},
		"nested fields":        {nestedSelections(3), nesting{selection: 3}},
		"inline fragment":      {`query { ... on Query { a { b } } }`, nesting{selection: 3}},
		"fragment definition":  {`query { ...F } fragment F on Query { a { b } }`, nesting{selection: 2}},
		"argument list":        {nestedList(3), nesting{selection: 1, value: 3}},
		"input object":         {nestedObject(2), nesting{selection: 1, value: 2}},
		"list type":            {nestedListType(2), nesting{selection: 1, value: 2}},
		"default value":        {`query($v: [Int] = [[1]]) { f(a: $v) }`, nesting{selection: 1, value: 2}},
		"directive argument":   {`query { f @include(if: {a: {b: true}}) }`, nesting{selection: 1, value: 2}},
		"variable directive":   {`query($a: Int @d(x: 0), $b: [Int] = [{k: [{k: 0}]}]) { f }`, nesting{selection: 1, value: 4}},
		"string braces":        {`query { f(a: "{{[[\"{{") }`, nesting{selection: 1}},
		"block string braces":  {`query { f(a: """{{ \""" [[ "" }}""") }`, nesting{selection: 1}},
		"block string quotes":  {`query { f(a: """x""""") }`, nesting{selection: 1}},
		"comment braces":       {"query { # {{{[[[\n f }", nesting{selection: 1}},
		"comment ends at CR":   {"query { # x\r f { g } }", nesting{selection: 2}},
		"extra closing brace":  {`query { f } }`, nesting{selection: 1, unbalanced: true}},
		"extra closing square": {`query { f(a: 1]) }`, nesting{selection: 1, unbalanced: true}},
		"value closes in args": {`query { f(a: }) }`, nesting{selection: 1, unbalanced: true}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := scanNesting(tc.query); got != tc.want {
				t.Fatalf("scanNesting(%q) = %+v, want %+v", tc.query, got, tc.want)
			}
		})
	}
}

func TestCountTokens(t *testing.T) {
	tests := map[string]struct {
		query string
		want  int
	}{
		"empty":                {``, 0},
		"shorthand":            {`{ serverInfo }`, 3},
		"commas and spaces":    {"{ a, b,,\t\r\n c }", 5},
		"punctuators":          {`query($v: [Int!]! = [1]) @d { ...F ... on Q { f } }`, 27},
		"alias and arguments":  {`{ x: f(a: 1, b: "s", c: """b""", d: $v) }`, 20},
		"negative float":       {`{ f(a: -1.5e+3) }`, 8},
		"name after number":    {`{ f(a: 10A, b: 0x1) }`, 13},
		"comments":             {"# one\n{ # two\r f }", 5},
		"strings hide tokens":  {`{ f(a: "{ x y }", b: """ ( ) """) }`, 11},
		"rejected bytes":       {"{ a ~ \x00 .. b }", 8},
		"breadth unit":         {strings.Repeat("a ", 10), 10},
		"unterminated string":  {`{ f(a: "x y`, 6},
		"unterminated comment": {"{ a # b c", 3},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := countTokens(tc.query); got != tc.want {
				t.Fatalf("countTokens(%q) = %d, want %d", tc.query, got, tc.want)
			}
		})
	}
}

// countParses replaces the parser for one test and reports how many times
// it ran. Callers must not be parallel.
func countParses(t *testing.T) *int {
	t.Helper()
	calls := 0
	original := parseQuery
	parseQuery = func(source *ast.Source) (*ast.QueryDocument, error) {
		calls++
		return original(source)
	}
	t.Cleanup(func() { parseQuery = original })
	return &calls
}

func TestPrepareOperationRejectsDeepNestingBeforeParsing(t *testing.T) {
	tests := map[string]struct {
		query   string
		message string
	}{
		"selection sets": {nestedSelections(maxSelectionDepth + 1), "GraphQL selection depth exceeds 64"},
		"unreachable fragment": {
			"query { a } fragment F on Query { " + strings.Repeat("a { ", maxSelectionDepth) + "b" + strings.Repeat(" }", maxSelectionDepth) + " }",
			"GraphQL selection depth exceeds 64",
		},
		"bare braces":     {strings.Repeat("{", maxSelectionDepth+1), "GraphQL selection depth exceeds 64"},
		"argument list":   {nestedList(maxValueDepth + 1), "invalid GraphQL document"},
		"input object":    {nestedObject(maxValueDepth + 1), "invalid GraphQL document"},
		"list type":       {nestedListType(maxValueDepth + 1), "invalid GraphQL document"},
		"unbalanced":      {`query { f } } { g }`, "invalid GraphQL document"},
		"unbalanced list": {`query { f(a: [1]]) }`, "invalid GraphQL document"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			calls := countParses(t)
			_, err := PrepareOperation(tc.query, "", nil)
			if err == nil || err.Error() != tc.message {
				t.Fatalf("error = %v, want %q", err, tc.message)
			}
			if *calls != 0 {
				t.Fatalf("parser ran %d times, want 0", *calls)
			}
			if fields := TopLevelFields(tc.query); len(fields) != 0 || *calls != 0 {
				t.Fatalf("TopLevelFields = %v after %d parses, want empty without parsing", fields, *calls)
			}
		})
	}
}

// commented returns `query { f }` padded with comments to tokens tokens.
func commented(tokens int) string {
	return "query { f }" + strings.Repeat("\n#", tokens-4)
}

func TestPrepareOperationRejectsBroadDocumentsBeforeParsing(t *testing.T) {
	documents := map[string]string{"one token over": commented(maxDocumentTokens + 1)}
	for name, query := range breadthDocuments(fill) {
		documents[name] = query
	}
	for name, query := range documents {
		t.Run(name, func(t *testing.T) {
			calls := countParses(t)
			_, err := PrepareOperation(query, "", nil)
			if err == nil || err.Error() != "invalid GraphQL document" {
				t.Fatalf("error = %v, want %q", err, "invalid GraphQL document")
			}
			if *calls != 0 {
				t.Fatalf("parser ran %d times, want 0", *calls)
			}
			if fields := TopLevelFields(query); len(fields) != 0 || *calls != 0 {
				t.Fatalf("TopLevelFields = %v after %d parses, want empty without parsing", fields, *calls)
			}
		})
	}
}

func TestPrepareOperationAcceptsNestingAtLimits(t *testing.T) {
	tests := map[string]string{
		"token count":    commented(maxDocumentTokens),
		"selection sets": nestedSelections(maxSelectionDepth),
		"argument list":  nestedList(maxValueDepth),
		"input object":   nestedObject(maxValueDepth),
		"list type":      nestedListType(maxValueDepth),
		"braces in strings and comments": "query { f(a: \"" + strings.Repeat("{[", 100) + "\", b: \"\"\"" +
			strings.Repeat("{[", 100) + "\"\"\") # " + strings.Repeat("{[", 100) + "\n}",
	}
	for name, query := range tests {
		t.Run(name, func(t *testing.T) {
			calls := countParses(t)
			if _, err := PrepareOperation(query, "", nil); err != nil {
				t.Fatalf("PrepareOperation rejected an at-limit document: %v", err)
			}
			if *calls != 1 {
				t.Fatalf("parser ran %d times, want 1", *calls)
			}
		})
	}
}

// preparePeakBytes reports an upper bound on the memory PrepareOperation
// takes for query: heap bytes allocated plus the growth of goroutine stacks.
// It runs the call on a fresh goroutine with the collector off, so a grown
// stack is neither shrunk nor its allocation hidden before it is measured.
func preparePeakBytes(query string) int64 {
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	runtime.GC()
	result := make(chan int64)
	go func() {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, _ = PrepareOperation(query, "", nil)
		runtime.ReadMemStats(&after)
		heap := int64(after.TotalAlloc - before.TotalAlloc)               // #nosec G115 -- test measurement, far below int64 range
		stack := max(int64(after.StackInuse)-int64(before.StackInuse), 0) // #nosec G115 -- test measurement, far below int64 range
		result <- heap + stack
	}()
	return <-result
}

// fill repeats unit after prefix until the document reaches the request cap.
func fill(prefix, unit string) string {
	return prefix + strings.Repeat(unit, (maxRequestBody-len(prefix))/len(unit))
}

// fillTokens repeats unit after prefix while the document stays within
// maxDocumentTokens: the largest document of that shape the scan accepts.
func fillTokens(prefix, unit string) string {
	return prefix + strings.Repeat(unit, (maxDocumentTokens-countTokens(prefix))/countTokens(unit))
}

// breadthDocuments returns depth-one documents built by repeat, one per
// shape the parser allocates for per token.
func breadthDocuments(repeat func(prefix, unit string) string) map[string]string {
	return map[string]string{
		"fields":           repeat("{", "a "),
		"aliases":          repeat("{", "a:b "),
		"arguments":        repeat("{f(", "a:1 "),
		"list values":      repeat("{f(a:[", "1 "),
		"object fields":    repeat("{f(a:{", "a:1 "),
		"directives":       repeat("{f", "@a "),
		"variables":        repeat("query(", "$a:I "),
		"variable values":  repeat("{f(a:[", "$a "),
		"strings":          repeat("{f(a:[", `"" `),
		"fragment spreads": repeat("{", "...a "),
		"comments":         repeat("{", "#\n"),
		"operations":       repeat("", "{a}"),
	}
}

func TestPrepareOperationPathologicalDocumentPeakAllocation(t *testing.T) {
	const budget = 4 << 20
	documents := map[string]string{
		"nested selections": fill("query {", "a{"),
		"bare braces":       fill("", "{"),
		"nested lists":      fill("query { f(a: ", "["),
		"nested objects":    fill("query { f(a: ", "{a:"),
		"nested list types": fill("query($v: ", "["),
	}
	for name, query := range breadthDocuments(fill) {
		documents["breadth "+name] = query
	}
	for name, query := range breadthDocuments(fillTokens) {
		documents["accepted breadth "+name] = query
	}
	for name, query := range documents {
		t.Run(name, func(t *testing.T) {
			if got := preparePeakBytes(query); got >= budget {
				t.Fatalf("PrepareOperation on a %d-byte document took %d bytes, want < %d", len(query), got, budget)
			}
		})
	}
}

// astNesting computes the nesting scanNesting must report for a parsed
// document: the deepest selection set over every operation and fragment, and
// the deepest list, input object, or list type in any argument or variable.
func astNesting(doc *ast.QueryDocument) nesting {
	var n nesting
	for _, operation := range doc.Operations {
		n.selection = max(n.selection, selectionNesting(operation.SelectionSet))
		n.value = max(n.value, variablesNesting(operation.VariableDefinitions), directivesNesting(operation.Directives), selectionValueNesting(operation.SelectionSet))
	}
	for _, fragment := range doc.Fragments {
		n.selection = max(n.selection, selectionNesting(fragment.SelectionSet))
		n.value = max(n.value, variablesNesting(fragment.VariableDefinition), directivesNesting(fragment.Directives), selectionValueNesting(fragment.SelectionSet))
	}
	return n
}

func selectionNesting(selections ast.SelectionSet) int {
	if len(selections) == 0 {
		return 0
	}
	deepest := 0
	for _, selection := range selections {
		switch selection := selection.(type) {
		case *ast.Field:
			deepest = max(deepest, selectionNesting(selection.SelectionSet))
		case *ast.InlineFragment:
			deepest = max(deepest, selectionNesting(selection.SelectionSet))
		}
	}
	return deepest + 1
}

func selectionValueNesting(selections ast.SelectionSet) int {
	deepest := 0
	for _, selection := range selections {
		switch selection := selection.(type) {
		case *ast.Field:
			deepest = max(deepest, argumentsNesting(selection.Arguments), directivesNesting(selection.Directives), selectionValueNesting(selection.SelectionSet))
		case *ast.InlineFragment:
			deepest = max(deepest, directivesNesting(selection.Directives), selectionValueNesting(selection.SelectionSet))
		case *ast.FragmentSpread:
			deepest = max(deepest, directivesNesting(selection.Directives))
		}
	}
	return deepest
}

func variablesNesting(definitions ast.VariableDefinitionList) int {
	deepest := 0
	for _, definition := range definitions {
		deepest = max(deepest, typeNesting(definition.Type), valueNesting(definition.DefaultValue), directivesNesting(definition.Directives))
	}
	return deepest
}

func directivesNesting(directives ast.DirectiveList) int {
	deepest := 0
	for _, directive := range directives {
		deepest = max(deepest, argumentsNesting(directive.Arguments))
	}
	return deepest
}

func argumentsNesting(arguments ast.ArgumentList) int {
	deepest := 0
	for _, argument := range arguments {
		deepest = max(deepest, valueNesting(argument.Value))
	}
	return deepest
}

func typeNesting(typ *ast.Type) int {
	if typ == nil || typ.Elem == nil {
		return 0
	}
	return 1 + typeNesting(typ.Elem)
}

func valueNesting(value *ast.Value) int {
	if value == nil || (value.Kind != ast.ListValue && value.Kind != ast.ObjectValue) {
		return 0
	}
	deepest := 0
	for _, child := range value.Children {
		deepest = max(deepest, valueNesting(child.Value))
	}
	return deepest + 1
}

// FuzzDocumentNesting checks the pre-parse scanner against the parser: on
// every document within budget that parses, the lexical nesting equals the
// nesting of the parsed AST, and no document the scanner calls unbalanced
// parses, and the parser reads no more tokens than countTokens reports.
// Documents over budget are never handed to the parser.
func FuzzDocumentNesting(f *testing.F) {
	seeds := []string{
		`{ serverInfo }`,
		`query Q($v: [[Int!]] = [[1]]) @d(a: {b: [2]}) { f(a: {c: "}{"}) { ... on Q { g } ...F } }`,
		`fragment F on Query { a { b(c: """ ]} \""" """) } }`,
		"query { # }}}\n f }",
		"query { # x\x01 f }",
		`query { f(a: "\"{") }`,
		`query { f(a: """x""""") }`,
		`query { f } }`,
		`query { f(a: [1}) }`,
		`query ($a: Int @d(x: 0), $b: [Int] = ` + strings.Repeat("[{k:", 33) + `0) { f }`,
		nestedSelections(maxSelectionDepth),
		nestedList(maxValueDepth + 1),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, query string) {
		n := scanNesting(query)
		tokens := countTokens(query)
		err := checkNesting(query)
		overBudget := n.selection > maxSelectionDepth || n.value > maxValueDepth || tokens > maxDocumentTokens
		if (err != nil) != (overBudget || n.unbalanced) {
			t.Fatalf("checkNesting(%q) = %v for %+v", query, err, n)
		}
		if overBudget {
			return
		}
		if _, limitErr := parser.ParseQueryWithTokenLimit(&ast.Source{Input: query}, tokens); limitErr != nil && strings.Contains(limitErr.Error(), "exceeded token limit") {
			t.Fatalf("countTokens(%q) = %d, below the tokens the parser read", query, tokens)
		}
		doc, parseErr := parser.ParseQuery(&ast.Source{Input: query})
		if n.unbalanced {
			if parseErr == nil {
				t.Fatalf("unbalanced document %q parsed", query)
			}
			return
		}
		if parseErr != nil {
			return
		}
		if want := astNesting(doc); n != want {
			t.Fatalf("scanNesting(%q) = %+v, parsed document has %+v", query, n, want)
		}
	})
}
