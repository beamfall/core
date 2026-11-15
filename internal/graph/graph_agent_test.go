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
		if logs := byName["serverLogs"]; !slices.Contains(logs.Arguments, "requestId: string optional") || !slices.Contains(logs.ResultFields, "lines") {
			t.Fatalf("serverLogs entry = %+v", logs)
		}
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
