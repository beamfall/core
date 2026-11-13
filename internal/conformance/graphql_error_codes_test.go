package conformance

import (
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

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/store"
)

// graphqlErrorSentinels resolves the clientError(<sentinel>.Error()) sites:
// their message is a store sentinel's text, so the gate classifies that text.
var graphqlErrorSentinels = map[string]error{
	"store.ErrJobNotFound": store.ErrJobNotFound,
}

// TestGraphQLErrorSitesCarryCodes enumerates every place internal/graph builds
// a GraphQL error or a client error (ARCHITECTURE §7, CORE-0053) and fails on
// one that leaves without a code from the closed vocabulary:
//
//   - a graphError literal must set Extensions;
//   - newGraphError/writeGraphError must name an ErrorCode constant or derive
//     the code through errorCode;
//   - every clientError message visible in source (a literal, a concatenation,
//     a fmt.Sprintf format, or a known store sentinel) must be mapped by
//     graph.ClientErrorCode. Only re-wrapped error text (x.Error(), string(x))
//     is dynamic; it is mapped at runtime, INVALID when no rule matches.
func TestGraphQLErrorSitesCarryCodes(t *testing.T) {
	dir := requireRoot(t, "internal/graph")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	constants := map[string]bool{}
	for _, code := range graph.ErrorCodes() {
		constants[errorCodeConstName(code)] = true
	}
	var failures []string
	sites, dynamic := 0, 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		rel := vocabRelPath(t, path)
		fail := func(node ast.Node, format string, args ...any) {
			failures = append(failures, fmt.Sprintf("%s:%d: %s", rel, fset.Position(node.Pos()).Line, fmt.Sprintf(format, args...)))
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CompositeLit:
				if ident, ok := node.Type.(*ast.Ident); ok && ident.Name == "graphError" {
					sites++
					if !compositeSetsKey(node, "Extensions") {
						fail(node, "graphError literal without Extensions; build it with newGraphError")
					}
				}
			case *ast.CallExpr:
				ident, ok := node.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				switch ident.Name {
				case "newGraphError", "writeGraphError":
					sites++
					codeArg := 0
					if ident.Name == "writeGraphError" {
						codeArg = 2
					}
					if len(node.Args) <= codeArg || !isErrorCodeExpr(node.Args[codeArg], constants) {
						fail(node, "%s without an ErrorCode constant or errorCode(err)", ident.Name)
					}
				case "clientError":
					sites++
					if len(node.Args) != 1 {
						fail(node, "clientError with %d arguments", len(node.Args))
						return true
					}
					message, ok := clientErrorMessage(node.Args[0])
					if !ok {
						if isRewrappedErrorText(node.Args[0]) {
							dynamic++
							return true
						}
						fail(node, "clientError message is neither visible in source nor re-wrapped error text")
						return true
					}
					if code, mapped := graph.ClientErrorCode(message); !mapped {
						fail(node, "clientError %q has no code mapping (add it to clientErrorCodes in internal/graph/error_codes.go); fallback would be %s", message, code)
					}
				}
			}
			return true
		})
	}
	sort.Strings(failures)
	for _, failure := range failures {
		t.Error(failure)
	}
	// The package has hundreds of client error sites; a scan that finds a
	// handful has stopped seeing them.
	if sites < 500 {
		t.Fatalf("scanned only %d error sites in internal/graph; the enumeration is broken", sites)
	}
	t.Logf("checked %d error sites (%d re-wrap dynamic error text)", sites, dynamic)
}

// TestGraphQLErrorCodesKeepDenialOfExistence pins the §6 rule on the mapping
// itself: content the caller cannot see answers NOT_FOUND, never FORBIDDEN.
func TestGraphQLErrorCodesKeepDenialOfExistence(t *testing.T) {
	for _, message := range []string{
		"unknown media",
		"unknown library",
		"unknown entity",
		"unknown collection",
		"unknown session",
		"unknown group",
		"not found",
		"cannot host a party for content you cannot see",
		"cannot join: this party's content is not visible to your profile",
	} {
		if code, mapped := graph.ClientErrorCode(message); !mapped || code != graph.ErrorCodeNotFound {
			t.Errorf("ClientErrorCode(%q) = %s, mapped=%v; want NOT_FOUND", message, code, mapped)
		}
	}
}

// TestGraphQLErrorCodesClassifyRepresentativeMessages pins one message per
// family so a reordered or widened rule cannot silently move a family.
func TestGraphQLErrorCodesClassifyRepresentativeMessages(t *testing.T) {
	for message, want := range map[string]graph.ErrorCode{
		"reveal PIN attempts throttled":                      graph.ErrorCodeThrottled,
		"unknown top-level field(s): x":                      graph.ErrorCodeInvalid,
		"unknown delete scope":                               graph.ErrorCodeInvalid,
		"a session in a profile is required to join a group": graph.ErrorCodeForbidden,
		"reveal required to purge trash":                     graph.ErrorCodeForbidden,
		"permission x is not approved":                       graph.ErrorCodeForbidden,
		"device revoked":                                     graph.ErrorCodeUnauthenticated,
		"store unavailable":                                  graph.ErrorCodeInternal,
		"library is already provisioned":                     graph.ErrorCodeConflict,
		"mediaID is required":                                graph.ErrorCodeInvalid,
		"x must be an integer":                               graph.ErrorCodeInvalid,
		"queue has items; pass confirmReplace to replace it": graph.ErrorCodeConflict,
	} {
		if code, mapped := graph.ClientErrorCode(message); !mapped || code != want {
			t.Errorf("ClientErrorCode(%q) = %s, mapped=%v; want %s", message, code, mapped, want)
		}
	}
}

func TestGraphQLErrorCodeVocabularyIsClosed(t *testing.T) {
	want := []graph.ErrorCode{"NOT_FOUND", "FORBIDDEN", "UNAUTHENTICATED", "THROTTLED", "INVALID", "CONFLICT", "INTERNAL"}
	got := graph.ErrorCodes()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ErrorCodes() = %v, want %v (ARCHITECTURE §7)", got, want)
	}
}

func errorCodeConstName(code graph.ErrorCode) string {
	var b strings.Builder
	b.WriteString("ErrorCode")
	for _, part := range strings.Split(string(code), "_") {
		b.WriteString(part[:1] + strings.ToLower(part[1:]))
	}
	return b.String()
}

func compositeSetsKey(lit *ast.CompositeLit, key string) bool {
	for _, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			if ident, ok := kv.Key.(*ast.Ident); ok && ident.Name == key {
				return true
			}
		}
	}
	return false
}

func isErrorCodeExpr(expr ast.Expr, constants map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		// writeGraphError's own body forwards its code parameter.
		return constants[e.Name] || e.Name == "code"
	case *ast.CallExpr:
		ident, ok := e.Fun.(*ast.Ident)
		return ok && ident.Name == "errorCode"
	}
	return false
}

var formatVerb = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z]`)

// clientErrorMessage renders the message a clientError argument produces, with
// every runtime operand replaced by "x"; ok is false when no part of it is
// visible in source.
func clientErrorMessage(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(e.Value)
		return value, err == nil
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		left, lok := clientErrorMessage(e.X)
		right, rok := clientErrorMessage(e.Y)
		if !lok && !rok {
			return "", false
		}
		if !lok {
			left = "x"
		}
		if !rok {
			right = "x"
		}
		return left + right, true
	case *ast.CallExpr:
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "fmt" && sel.Sel.Name == "Sprintf" && len(e.Args) > 0 {
				format, ok := clientErrorMessage(e.Args[0])
				if !ok {
					return "", false
				}
				return formatVerb.ReplaceAllString(format, "x"), true
			}
			// store.ErrX.Error()
			if sel.Sel.Name == "Error" && len(e.Args) == 0 {
				if inner, ok := sel.X.(*ast.SelectorExpr); ok {
					if pkg, ok := inner.X.(*ast.Ident); ok {
						if sentinel, ok := graphqlErrorSentinels[pkg.Name+"."+inner.Sel.Name]; ok {
							return sentinel.Error(), true
						}
					}
				}
			}
		}
	}
	return "", false
}

// isRewrappedErrorText reports err.Error() or string(verr): text of an error
// value the resolver already classified as caused by the request.
func isRewrappedErrorText(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "string" && len(call.Args) == 1 {
		_, isIdent := call.Args[0].(*ast.Ident)
		return isIdent
	}
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" && len(call.Args) == 0 {
		_, isIdent := sel.X.(*ast.Ident)
		return isIdent
	}
	return false
}
