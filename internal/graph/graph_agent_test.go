package graph

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/logtail"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func tokenAuthorizer(t *testing.T, st *store.Store, access store.APITokenAccess) func(*http.Request) {
	t.Helper()
	_, token, err := st.CreateAPITokenWithAccess(t.Context(), string(access), access, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+token) }
}

// TestAgentCatalogListsEveryFieldWithTiers (CORE-2096): the catalog is
// readable by every token tier, lists every registered field exactly once, and
// reports the tiers the dispatcher would actually admit.
func TestAgentCatalogListsEveryFieldWithTiers(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	const body = `{"query":"query { agentCatalog { guide fields { name kind adminSession tokenTiers arguments resultFields } } }"}`
	for _, access := range tokenTiers {
		data, errs := post(t, handler, body, tokenAuthorizer(t, st, access))
		if len(errs) != 0 {
			t.Fatalf("%s token agentCatalog errors = %#v", access, errs)
		}
		var catalog AgentCatalogInfo
		if err := json.Unmarshal(data["agentCatalog"], &catalog); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(catalog.Guide, "/api/graphql") || len(catalog.Fields) != len(fieldHandlerRegistry) {
			t.Fatalf("catalog guide/fields = %q / %d, want %d fields", catalog.Guide, len(catalog.Fields), len(fieldHandlerRegistry))
		}
		byName := map[string]AgentCatalogField{}
		for _, field := range catalog.Fields {
			byName[field.Name] = field
		}
		jobs := byName["jobs"]
		if jobs.Kind != "query" || !jobs.AdminSession || !slices.Equal(jobs.TokenTiers, []string{"diagnostics", "agent"}) {
			t.Fatalf("jobs entry = %+v", jobs)
		}
		if mint := byName["createApiToken"]; mint.Kind != "mutation" || len(mint.TokenTiers) != 0 {
			t.Fatalf("createApiToken entry = %+v, want a mutation no tier may run", mint)
		}
		if logs := byName["serverLogs"]; !logs.ArgumentsKnown || !slices.Contains(logs.Arguments, "requestId: string optional") || !slices.Contains(logs.ResultFields, "lines") {
			t.Fatalf("serverLogs entry = %+v", logs)
		}
		for _, name := range []string{"job", "jobCancel", "jobEvents", "jobRetry"} {
			if entry := byName[name]; !entry.ArgumentsKnown || !slices.Equal(entry.Arguments, []string{"id: string"}) {
				t.Fatalf("%s entry = %+v, want the required id argument", name, entry)
			}
		}
		for _, field := range catalog.Fields {
			if !field.ArgumentsKnown && len(field.Arguments) != 0 {
				t.Fatalf("%s lists arguments but reports them unknown", field.Name)
			}
		}
	}
}

// TestAgentArgumentHintsDoNotShadowWireSchema: a hint is only for a field the
// nested wire schema does not describe, and names a registered field.
func TestAgentArgumentHintsDoNotShadowWireSchema(t *testing.T) {
	for name, args := range agentArgumentHints {
		if _, ok := fieldArguments[name]; ok {
			t.Errorf("agentArgumentHints[%q] duplicates fieldArguments", name)
		}
		if _, ok := fieldHandlerRegistry[name]; !ok {
			t.Errorf("agentArgumentHints[%q] names no registered field", name)
		}
		if !slices.IsSortedFunc(args, func(a, b NestedArgument) int { return strings.Compare(a.Name, b.Name) }) {
			t.Errorf("agentArgumentHints[%q] is not sorted", name)
		}
	}
}

// TestLogLineHasAttrIgnoresQuotedText: text inside a quoted value is never an
// attribute, and a quoted attribute value is unquoted before comparing.
func TestLogLineHasAttrIgnoresQuotedText(t *testing.T) {
	cases := []struct {
		line, key, value string
		want             bool
	}{
		{`level=INFO msg="remote level=ERROR happened"`, "level", "ERROR", false},
		{`level=INFO msg="remote level=ERROR happened"`, "level", "INFO", true},
		{`level=WARN msg="see requestID=abc" requestID=xyz`, "requestID", "abc", false},
		{`level=WARN msg="see requestID=abc" requestID=xyz`, "requestID", "xyz", true},
		{`level=ERROR msg="say \"hi\" requestID=q" requestID="a b"`, "requestID", "a b", true},
		{`level=ERROR msg="say \"hi\" requestID=q" requestID="a b"`, "requestID", "q", false},
		{`level=ERROR requestID=abcd`, "requestID", "abc", false},
		{`time=x bare level=DEBUG`, "level", "DEBUG", true},
		{`msg="unterminated level=ERROR`, "level", "ERROR", false},
		{`level=INFO msg="real" "x=foo"="remote level=ERROR happened"`, "level", "ERROR", false},
		{`level=INFO msg="real" "x=foo"="remote level=ERROR happened"`, "x=foo", "remote level=ERROR happened", true},
		{`=stray level=WARN`, "level", "WARN", true},
		{`level=INFO requestID=`, "requestID", "abc", false},
		{`level=INFO requestID=`, "requestID", "", true},
		{`level=`, "level", "INFO", false},
	}
	for _, tc := range cases {
		if got := logLineHasAttr(tc.line, tc.key, tc.value); got != tc.want {
			t.Errorf("logLineHasAttr(%q, %q, %q) = %v, want %v", tc.line, tc.key, tc.value, got, tc.want)
		}
	}
}

// TestCappedWriterBoundsProfileMemory: the writer keeps at most its limit and
// fails the write that crosses it, so a profile stops formatting at the cap.
func TestCappedWriterBoundsProfileMemory(t *testing.T) {
	w := &cappedWriter{limit: 10}
	if n, err := w.Write([]byte("12345")); n != 5 || err != nil {
		t.Fatalf("first write = %d, %v", n, err)
	}
	if n, err := w.Write([]byte("6789012")); n != 5 || err != errProfileCapReached {
		t.Fatalf("crossing write = %d, %v", n, err)
	}
	if w.buf.String() != "1234567890" || !w.truncated || w.buf.Cap() > 64 {
		t.Fatalf("buffer = %q (cap %d) truncated=%v", w.buf.String(), w.buf.Cap(), w.truncated)
	}
}

// TestGoroutineProfileStaysWithinCap: a goroutine dump larger than the cap is
// truncated to it and flagged.
func TestGoroutineProfileStaysWithinCap(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	for range 3000 {
		go func() { <-release }()
	}
	profile := profileText("goroutine")
	if len(profile.Text) > diagnosticsProfileMaxBytes || !profile.Truncated || !strings.HasPrefix(profile.Text, "goroutine ") {
		t.Fatalf("goroutine profile = %d bytes truncated=%v prefix %.40q", len(profile.Text), profile.Truncated, profile.Text)
	}
}

// TestServerLogsFilters (CORE-2097): contains, level and requestId each narrow
// the kept lines, and together they must all match.
func TestServerLogsFilters(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	ring := logtail.NewRing(20)
	_, _ = ring.Write([]byte(strings.Join([]string{
		`level=INFO msg=request requestID=abc status=200`,
		`level=ERROR msg="request failed" requestID=abc status=500`,
		`level=ERROR msg="request failed" requestID=abcd status=500`,
		`level=INFO msg=scan done`,
	}, "\n") + "\n"))
	handler.SetServerLogs(ring)
	authorize := tokenAuthorizer(t, st, store.APITokenAccessDiagnostics)

	read := func(vars string) []string {
		t.Helper()
		body := `{"query":"query L($contains:String,$level:String,$requestId:String,$limit:Int){ serverLogs(contains:$contains, level:$level, requestId:$requestId, limit:$limit) { lines } }","variables":` + vars + `}`
		data, errs := post(t, handler, body, authorize)
		if len(errs) != 0 {
			t.Fatalf("serverLogs %s errors = %#v", vars, errs)
		}
		var info ServerLogsInfo
		if err := json.Unmarshal(data["serverLogs"], &info); err != nil {
			t.Fatal(err)
		}
		return info.Lines
	}
	if got := read(`{"requestId":"abc"}`); len(got) != 2 || !strings.Contains(got[1], "status=500") {
		t.Fatalf("requestId=abc lines = %q", got)
	}
	if got := read(`{"level":"error"}`); len(got) != 2 {
		t.Fatalf("level=error lines = %q", got)
	}
	if got := read(`{"level":"ERROR","requestId":"abc"}`); len(got) != 1 {
		t.Fatalf("level+requestId lines = %q", got)
	}
	if got := read(`{"contains":"scan"}`); len(got) != 1 {
		t.Fatalf("contains=scan lines = %q", got)
	}
	if got := read(`{"level":"ERROR","limit":1}`); len(got) != 1 || !strings.Contains(got[0], "abcd") {
		t.Fatalf("level=ERROR limit 1 lines = %q, want the newest match", got)
	}
}

// TestDiagnosticsBundle (CORE-2097): a diagnostics or agent token reads the
// bundle with a goroutine dump, a cli token is refused, and an unknown profile
// fails naming the allowed set.
func TestDiagnosticsBundle(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	const body = `{"query":"query B($profiles:[String!]){ diagnosticsBundle(profiles:$profiles) { version goVersion goroutines uptimeSeconds memory { heapAlloc } logs { available } profiles { name text truncated } } }","variables":{"profiles":["goroutine","heap"]}}`

	if _, errs := post(t, handler, body, tokenAuthorizer(t, st, store.APITokenAccessCLI)); len(errs) == 0 || errs[0].Message != "admin session required" {
		t.Fatalf("cli token diagnosticsBundle errors = %#v, want admin gate", errs)
	}
	data, errs := post(t, handler, body, tokenAuthorizer(t, st, store.APITokenAccessAgent))
	if len(errs) != 0 {
		t.Fatalf("agent token diagnosticsBundle errors = %#v", errs)
	}
	var bundle DiagnosticsBundleInfo
	if err := json.Unmarshal(data["diagnosticsBundle"], &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.GoVersion == "" || bundle.Goroutines == 0 || bundle.Memory.HeapAlloc == 0 || len(bundle.Profiles) != 2 {
		t.Fatalf("bundle = %+v", bundle)
	}
	if bundle.Profiles[0].Name != "goroutine" || !strings.Contains(bundle.Profiles[0].Text, "goroutine ") {
		t.Fatalf("goroutine profile = %.200q", bundle.Profiles[0].Text)
	}
	for _, profile := range bundle.Profiles {
		if len(profile.Text) > diagnosticsProfileMaxBytes {
			t.Fatalf("profile %s is %d bytes, over the bound", profile.Name, len(profile.Text))
		}
	}

	bad := `{"query":"query B($profiles:[String!]){ diagnosticsBundle(profiles:$profiles) { version } }","variables":{"profiles":["cpu"]}}`
	_, errs = post(t, handler, bad, tokenAuthorizer(t, st, store.APITokenAccessDiagnostics))
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "allowed: allocs, block, goroutine") {
		t.Fatalf("cpu profile errors = %#v, want the allowed list", errs)
	}
}
