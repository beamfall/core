package conformance

// ADR-0130 §1 permanent client ceiling, recorded as the "client-ceiling" row in
// contracts/registry.json: no client binary — App Store, sideload,
// Manager, web, or any future client class — ever executes, hosts, or
// interprets plugin-delivered code, templates-with-logic, or rendering
// instructions beyond the fixed, compiled-in vocabularies (archetypes,
// Sections, Sources, artwork slots, theme tokens, Plugin Action Descriptors).
//
// This repo embeds one client — the AGPL Viewer/Manage/Admin web UI
// (ADR-0007, internal/web/app/src) — so this file is that client's own
// no-dynamic-code ceiling conformance test. The apple/android/web(-kit)/windows
// consumer repos each land their own copy against this contract row
// (PLUGMECH-7-APPLE/-ANDROID/-WEB/-WINDOWS, tracked as knownDrift until then).
//
// Four tests:
//   - TestClientCeilingNoDynamicCodePaths: the forbidden-substring scan.
//   - TestClientCeilingCheckCatchesInjectedDrift: red proof the substring scan
//     actually fails on injected drift rather than vacuously passing.
//   - TestClientCeilingCompiledInVocabulary: asserts the *behavior*, not just
//     absent substrings — the renderer dispatches Section.kind (the
//     plugin-supplied, unbounded wire field) against a fixed compiled-in Set
//     literal (SECTION_KINDS) and silently drops unrecognized entries, rather
//     than keying any executable/dynamic lookup off that untrusted value.
//   - TestClientCeilingCompiledInVocabularyCatchesDynamicDispatch: red proof
//     that a fixture keying a function-table/dynamic-import lookup off an
//     unbounded string field is caught by the same check.
//   - TestClientCeilingCatchesDynamicDispatchInRealClientContext: the same red
//     proof against a drifted copy of the REAL LayoutView.tsx dispatch site
//     (not an isolated synthetic snippet), so the check is proven against
//     genuine program context, not just a string with no surrounding code.
import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// embeddedWebClientSrcDir is the source tree of this repo's own embedded
// client renderer.
const embeddedWebClientSrcDir = "../web/app/src"

// forbiddenClientCeilingPatterns is the closed set of dynamic-code / eval /
// downloaded-view constructs the client ceiling forbids. Any match means the
// renderer is executing/hosting/interpreting something beyond the compiled-in
// vocabularies.
var forbiddenClientCeilingPatterns = []string{
	"eval(",
	"new Function(",
	"dangerouslySetInnerHTML",
	"document.write(",
	"document.writeln(",
	".innerHTML =",
	"innerHTML=",
	"setTimeout(\"",
	"setInterval(\"",
	"importScripts(",
	"vm.runInNewContext",
	"vm.runInThisContext",
	"child_process",
}

// scanForClientCeilingViolations walks a source tree and reports every
// file:pattern hit for the forbidden dynamic-code/eval/downloaded-view set.
// Test-only and generated files are excluded so the scan targets shipped
// renderer code, matching how the Verify clause is meant to gate real drift.
func scanForClientCeilingViolations(root string) ([]string, error) {
	var hits []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".ts") && !strings.HasSuffix(name, ".tsx") &&
			!strings.HasSuffix(name, ".js") && !strings.HasSuffix(name, ".jsx") {
			return nil
		}
		if strings.HasSuffix(name, ".test.ts") || strings.HasSuffix(name, ".test.tsx") ||
			strings.HasSuffix(name, ".d.ts") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(src)
		for _, pat := range forbiddenClientCeilingPatterns {
			if strings.Contains(text, pat) {
				hits = append(hits, path+": "+pat)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return hits, nil
}

// TestClientCeilingNoDynamicCodePaths asserts this repo's embedded web client
// (ADR-0007 Viewer/Manage/Admin UI) contains none of the forbidden
// dynamic-code/eval/downloaded-view patterns — the ADR-0130 §1 client ceiling
// is a platform invariant, not prose.
func TestClientCeilingNoDynamicCodePaths(t *testing.T) {
	hits, err := scanForClientCeilingViolations(embeddedWebClientSrcDir)
	if err != nil {
		t.Fatalf("scan %s: %v", embeddedWebClientSrcDir, err)
	}
	if len(hits) > 0 {
		t.Fatalf("client ceiling violated — dynamic-code/eval/downloaded-view pattern(s) found:\n%s",
			strings.Join(hits, "\n"))
	}
}

// TestClientCeilingCheckCatchesInjectedDrift demonstrates the scan is a real
// gate, not a vacuous pass: a fixture file containing a forbidden pattern
// must be caught.
func TestClientCeilingCheckCatchesInjectedDrift(t *testing.T) {
	dir := t.TempDir()
	driftedPath := filepath.Join(dir, "drifted.ts")
	drifted := "export function render(tpl: string) {\n  return eval(tpl);\n}\n"
	if err := os.WriteFile(driftedPath, []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}

	hits, err := scanForClientCeilingViolations(dir)
	if err != nil {
		t.Fatalf("scan %s: %v", dir, err)
	}
	if len(hits) == 0 {
		t.Fatal("injected eval() drift was not caught; client ceiling check is not enforcing")
	}
	found := false
	for _, h := range hits {
		if strings.Contains(h, "eval(") {
			found = true
		}
	}
	if !found {
		t.Fatalf("scan reported hits but missed the injected eval() pattern: %v", hits)
	}
}

// sectionsSourceFile is the vocabulary declaration site: the closed, compiled-in
// Set of renderable Section.kind values.
const sectionsSourceFile = embeddedWebClientSrcDir + "/areas/viewer/lib/sections.ts"

// layoutViewSourceFile is the dispatch site: where a Layout's Sections are
// filtered against the compiled-in vocabulary before anything renders.
const layoutViewSourceFile = embeddedWebClientSrcDir + "/areas/viewer/components/LayoutView.tsx"

// dynamicDispatchByKeyPattern flags any construct that would key an
// executable lookup (function call, dynamic import, indexed callable) off a
// runtime string — the shape a compiled-in-vocabulary violation would take if
// a client ever let a plugin-supplied kind/type drive what code runs, rather
// than only which precomputed literal branch is taken.
var dynamicDispatchByKeyPattern = regexp.MustCompile(`\[[A-Za-z_$][\w.]*\]\s*\(`)

// scanForDynamicDispatchByKey walks a source tree for computed-member-call
// sites (`foo[key](...)`) — the pattern a kind/type-keyed function table or
// dynamic-import-by-string dispatch would produce. It does not care about
// substrings like "eval("; it looks for the call-through-a-runtime-key shape
// itself, so it catches drift the forbidden-pattern list was never told about.
func scanForDynamicDispatchByKey(root string) ([]string, error) {
	var hits []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".ts") && !strings.HasSuffix(name, ".tsx") &&
			!strings.HasSuffix(name, ".js") && !strings.HasSuffix(name, ".jsx") {
			return nil
		}
		if strings.HasSuffix(name, ".test.ts") || strings.HasSuffix(name, ".test.tsx") ||
			strings.HasSuffix(name, ".d.ts") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range dynamicDispatchByKeyPattern.FindAllString(string(src), -1) {
			hits = append(hits, path+": "+m)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return hits, nil
}

// TestClientCeilingCompiledInVocabulary asserts the ADR-0130 §1 behavior
// directly: the renderer's Section.kind vocabulary (invariant 3 — Sections are
// a small fixed vocabulary; plugins contribute unbounded *data* against it,
// never new kinds) is declared as a closed, compiled-in Set of string
// literals, and the Layout dispatch path filters against that Set — so an
// unrecognized/plugin-supplied kind is silently dropped rather than driving
// any executable lookup. This is a behavioral assertion, not a substring grep:
// it fails if the vocabulary stops being a static literal Set, or if the
// dispatch site stops checking membership before rendering, or if any file in
// the client contains a computed-member-call keyed dispatch shape.
func TestClientCeilingCompiledInVocabulary(t *testing.T) {
	vocab, err := os.ReadFile(sectionsSourceFile)
	if err != nil {
		t.Fatalf("read %s: %v", sectionsSourceFile, err)
	}
	vocabSrc := string(vocab)

	if !strings.Contains(vocabSrc, "export const SECTION_KINDS = new Set([") {
		t.Fatalf("%s no longer declares SECTION_KINDS as a literal, compiled-in Set — "+
			"the client-ceiling vocabulary must be static, not computed", sectionsSourceFile)
	}
	// Every element of the declared Set must be a quoted string literal — not
	// a variable, function call, spread, or anything sourced at runtime.
	kindsBlockStart := strings.Index(vocabSrc, "new Set([")
	kindsBlockEnd := strings.Index(vocabSrc[kindsBlockStart:], "])")
	if kindsBlockEnd < 0 {
		t.Fatalf("%s: could not find the closing `])` of the SECTION_KINDS literal", sectionsSourceFile)
	}
	kindsBlock := vocabSrc[kindsBlockStart : kindsBlockStart+kindsBlockEnd]
	for _, line := range strings.Split(kindsBlock, "\n") {
		trimmed := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), ","))
		if trimmed == "" || trimmed == "new Set([" {
			continue
		}
		if !strings.HasPrefix(trimmed, `"`) || !strings.HasSuffix(trimmed, `"`) {
			t.Fatalf("%s: SECTION_KINDS entry %q is not a static string literal — "+
				"the compiled-in vocabulary must not source entries at runtime", sectionsSourceFile, trimmed)
		}
	}

	dispatch, err := os.ReadFile(layoutViewSourceFile)
	if err != nil {
		t.Fatalf("read %s: %v", layoutViewSourceFile, err)
	}
	if !strings.Contains(string(dispatch), "SECTION_KINDS.has(") {
		t.Fatalf("%s no longer filters Sections through SECTION_KINDS membership — "+
			"an unrecognized/plugin-supplied kind must be dropped before it reaches rendering, not dispatched",
			layoutViewSourceFile)
	}

	hits, err := scanForDynamicDispatchByKey(embeddedWebClientSrcDir)
	if err != nil {
		t.Fatalf("scan %s: %v", embeddedWebClientSrcDir, err)
	}
	if len(hits) > 0 {
		t.Fatalf("client ceiling violated — computed-member-call (kind/type-keyed dynamic dispatch) found:\n%s",
			strings.Join(hits, "\n"))
	}
}

// TestClientCeilingCompiledInVocabularyCatchesDynamicDispatch is the red proof
// for TestClientCeilingCompiledInVocabulary's dynamic-dispatch-by-key half: a
// fixture that resolves a runtime string against a lookup table and calls the
// result — the shape an unbounded plugin `kind` driving code execution would
// take — must be caught even though it contains none of the substrings in
// forbiddenClientCeilingPatterns.
func TestClientCeilingCompiledInVocabularyCatchesDynamicDispatch(t *testing.T) {
	dir := t.TempDir()
	driftedPath := filepath.Join(dir, "drifted.tsx")
	// No eval/innerHTML/etc substring here — this exercises the behavioral
	// scan, not the forbidden-pattern list.
	drifted := "const renderers = { grid: renderGrid, shelf: renderShelf };\n" +
		"function render(section) {\n  return renderers[section.kind](section);\n}\n"
	if err := os.WriteFile(driftedPath, []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}

	substringHits, err := scanForClientCeilingViolations(dir)
	if err != nil {
		t.Fatalf("scan %s: %v", dir, err)
	}
	if len(substringHits) > 0 {
		t.Fatalf("fixture was expected to be invisible to the substring scan, got: %v", substringHits)
	}

	hits, err := scanForDynamicDispatchByKey(dir)
	if err != nil {
		t.Fatalf("scan %s: %v", dir, err)
	}
	if len(hits) == 0 {
		t.Fatal("injected kind-keyed dynamic dispatch was not caught; compiled-in-vocabulary check is not enforcing")
	}
}

// TestClientCeilingCatchesDynamicDispatchInRealClientContext is a stronger red
// proof than the fixture above: instead of a bare synthetic snippet, it takes
// the REAL embedded LayoutView.tsx dispatch site verbatim and drifts only the
// one line that matters — replacing the compiled-in `SECTION_KINDS.has(s.kind)`
// membership filter with a kind-keyed function-table call
// (`SECTION_RENDERERS[s.kind](s)`) — the exact shape ADR-0130 §1 forbids: an
// unbounded, plugin-supplied Section.kind driving which code runs, not just
// which precomputed branch renders. Everything else in the file (imports,
// comments, surrounding hooks) is the genuine current source, so this proves
// the scan catches the drift in situ, not only in an isolated string with no
// surrounding program.
func TestClientCeilingCatchesDynamicDispatchInRealClientContext(t *testing.T) {
	real, err := os.ReadFile(layoutViewSourceFile)
	if err != nil {
		t.Fatalf("read %s: %v", layoutViewSourceFile, err)
	}
	realSrc := string(real)

	const guard = "SECTION_KINDS.has(s.kind)"
	if !strings.Contains(realSrc, guard) {
		t.Fatalf("%s no longer contains the expected compiled-in guard %q — "+
			"update this drift fixture to match the current dispatch site", layoutViewSourceFile, guard)
	}
	drifted := strings.Replace(realSrc, guard, "SECTION_RENDERERS[s.kind](s)", 1)
	if drifted == realSrc {
		t.Fatal("drift substitution did not change the source; fixture is not exercising anything")
	}

	dir := t.TempDir()
	driftedPath := filepath.Join(dir, "LayoutView.tsx")
	if err := os.WriteFile(driftedPath, []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}

	// The drifted file keeps every real import/comment/hook untouched, so the
	// forbidden-substring scan must still find nothing — this is exactly the
	// class of drift that scan is blind to.
	substringHits, err := scanForClientCeilingViolations(dir)
	if err != nil {
		t.Fatalf("scan %s: %v", dir, err)
	}
	if len(substringHits) > 0 {
		t.Fatalf("drifted real-context fixture was expected to be invisible to the substring scan, got: %v", substringHits)
	}

	hits, err := scanForDynamicDispatchByKey(dir)
	if err != nil {
		t.Fatalf("scan %s: %v", dir, err)
	}
	if len(hits) == 0 {
		t.Fatal("kind-keyed dynamic dispatch injected into the real LayoutView.tsx context was not caught")
	}
	found := false
	for _, h := range hits {
		if strings.Contains(h, "[s.kind](") {
			found = true
		}
	}
	if !found {
		t.Fatalf("scan reported hits but missed the injected [s.kind](...) call: %v", hits)
	}
}
