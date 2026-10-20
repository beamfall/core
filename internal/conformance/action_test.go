package conformance

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/graph"
)

// allowlistedActions maps a mutation field name to the reason it is genuinely
// tested but cannot be detected by the GraphQL-body scan below (e.g. the
// mutation body is assembled by a helper from a variable, so the field name
// never appears as a literal next to the word "mutation"). Add an entry ONLY
// after confirming a real behavior test exists; the stale-allowlist check
// fails the build if an entry stops being necessary.
//
// Currently empty: every mutation in graph.MutationFields() is invoked with a
// literal GraphQL mutation body in internal/graph/*_test.go.
var allowlistedActions = map[string]string{}

// TestEveryActionHasBehaviorTest is the action-completeness gate (ADR-0040 /
// the server-action-conformance plan): every state-changing GraphQL field
// reported by graph.MutationFields() must have at least one behavior test that
// invokes it through a GraphQL mutation. It scans internal/graph/*_test.go for
// a mutation invocation of each field.
//
// Detection is deliberately tolerant — for this bootstrap a false PASS (a
// mutation we count as tested when the test is weak) is preferable to a false
// FAIL (blocking the build over a body the scanner can't parse). A field is
// "tested" when its name appears as a whole GraphQL identifier inside a
// mutation document: either on a line that also contains the keyword
// "mutation", or within a few lines after a line that opens a mutation. The
// allowlist covers anything genuinely tested only via a helper-built body.
func TestEveryActionHasBehaviorTest(t *testing.T) {
	root := repoRoot(t)
	testDir := filepath.Join(root, "internal", "graph")

	entries, err := os.ReadDir(testDir)
	if err != nil {
		t.Fatalf("read %s: %v", testDir, err)
	}
	var blob strings.Builder
	scanned := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(testDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		blob.Write(data)
		blob.WriteByte('\n')
		scanned++
	}
	if scanned == 0 {
		t.Fatal("no internal/graph/*_test.go files found to scan for mutation coverage")
	}

	lines := strings.Split(blob.String(), "\n")
	// A line is "in a mutation context" if it contains the keyword "mutation",
	// or is within the windowFollow lines after such a line — covering
	// multi-line / concatenated mutation documents.
	const windowFollow = 6
	inMutationContext := make([]bool, len(lines))
	remaining := 0
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), "mutation") {
			remaining = windowFollow
		}
		if remaining > 0 {
			inMutationContext[i] = true
			remaining--
		}
	}

	mutations := graph.MutationFields()
	detected := make(map[string]bool, len(mutations))
	for _, field := range mutations {
		// Whole-identifier, case-insensitive: the GraphQL field name is
		// camelCase in the body, lowercase in the registry key.
		re := regexp.MustCompile(`(?i)(^|[^a-zA-Z0-9_])` + regexp.QuoteMeta(field) + `($|[^a-zA-Z0-9_])`)
		for i, line := range lines {
			if !inMutationContext[i] {
				continue
			}
			if re.MatchString(line) {
				detected[field] = true
				break
			}
		}
	}

	var undetected []string
	for _, field := range mutations {
		if detected[field] {
			continue
		}
		if _, ok := allowlistedActions[field]; ok {
			continue
		}
		undetected = append(undetected, field)
	}
	sort.Strings(undetected)
	if len(undetected) > 0 {
		t.Errorf("%d mutation(s) in graph.MutationFields() have no detected behavior test in internal/graph/*_test.go (add a test, or allowlist with a reason): %v",
			len(undetected), undetected)
	}

	// Stale-allowlist check: an allowlist entry that the scanner DID detect, or
	// that names a field no longer in MutationFields(), is dead and must be
	// removed so the allowlist never silently hides a real gap.
	mutationSet := make(map[string]bool, len(mutations))
	for _, m := range mutations {
		mutationSet[m] = true
	}
	for field := range allowlistedActions {
		if !mutationSet[field] {
			t.Errorf("allowlistedActions has %q which is not in graph.MutationFields(); remove the stale entry", field)
			continue
		}
		if detected[field] {
			t.Errorf("allowlistedActions has %q but the scan DID detect a test for it; remove the unnecessary allowlist entry", field)
		}
	}
}
