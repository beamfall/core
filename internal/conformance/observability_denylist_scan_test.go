package conformance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/observability"
)

// TestObservabilityInstrumentationHonorsDecision9Denylist is ADR-0146
// Consequences (b): a static scan of every instrumentation call site enforces
// the Decision-9 label/attribute denylist (no titles, paths, library/profile/
// account IDs, tokens, or client IPs), so §9 is machine-enforced rather than
// aspirational. Core is metrics-only (Decision 2: no trace SDK), so the call
// sites are RecordGauge / recordGauge invocations; every literal label key (and
// any literal value) must clear observability.ForbiddenLabelKey/Value, the same
// predicate RecordGauge applies at runtime. Dynamic label maps (e.g. the
// server's nil-safe forwarder) are enforced at runtime by validateLabels, which
// TestDecision9Denylist proves — static + runtime together cover Decision 9.
func TestObservabilityInstrumentationHonorsDecision9Denylist(t *testing.T) {
	scanned := 0
	callSites := 0
	for _, root := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(requireRoot(t, root), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || strings.HasSuffix(path, "_test.go") || !strings.HasSuffix(path, ".go") {
				return nil
			}
			rel := vocabRelPath(t, path)
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			scanned++
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isRecordGaugeCall(call) {
					return true
				}
				callSites++
				pos := fset.Position(call.Pos())
				auditRecordGaugeLabels(t, call, rel, pos.Line)
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned == 0 {
		t.Fatal("Decision-9 instrumentation scan walked zero files")
	}
	if callSites == 0 {
		t.Fatal("Decision-9 instrumentation scan found no RecordGauge call sites — the scan would pass vacuously")
	}
}

// isRecordGaugeCall reports whether call is an observability instrumentation
// site: a method call named RecordGauge (the Controller/interface method) or
// recordGauge (the server's nil-safe forwarder).
func isRecordGaugeCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.Sel.Name == "RecordGauge" || sel.Sel.Name == "recordGauge"
}

// auditRecordGaugeLabels enforces that the labels argument (the last argument)
// is nil or a statically analyzable string map whose keys/values clear the
// Decision-9 denylist.
func auditRecordGaugeLabels(t *testing.T, call *ast.CallExpr, rel string, line int) {
	t.Helper()
	if len(call.Args) == 0 {
		return
	}
	labels := call.Args[len(call.Args)-1]
	if ident, ok := labels.(*ast.Ident); ok && ident.Name == "nil" {
		return
	}
	lit, ok := labels.(*ast.CompositeLit)
	if !ok {
		// A dynamic label map (e.g. the server's recordGauge forwarder passing
		// through an already-constructed map) cannot be inspected statically;
		// validateLabels enforces Decision 9 on it at runtime, which
		// TestDecision9Denylist proves. Every map *literal* — where a sensitive
		// key would actually be authored — is checked below.
		return
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key := stringLiteral(kv.Key)
		if key == "" {
			t.Fatalf("%s:%d uses a non-literal label key; Decision-9 requires literal, low-cardinality keys", rel, line)
		}
		if observability.ForbiddenLabelKey(key) {
			t.Fatalf("%s:%d instrumentation label key %q violates ADR-0146 Decision 9 (title/path/id/token/client-ip)", rel, line, key)
		}
		if value := stringLiteral(kv.Value); value != "" && observability.ForbiddenLabelValue(value) {
			t.Fatalf("%s:%d instrumentation label value %q violates ADR-0146 Decision 9", rel, line, value)
		}
	}
}

// stringLiteral returns the unquoted value of a string-literal expression, or ""
// when the expression is not a plain string literal (a dynamic value the static
// scan cannot inspect; validateLabels backstops those at runtime).
func stringLiteral(expr ast.Expr) string {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return value
}
