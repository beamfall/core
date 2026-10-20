package graph

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// TestGraphQLCostBudgetsRejectBeforeAuthAndAudit locks the pre-scope cost
// gate: a document over any cost budget is
// rejected with exactly one stable client-safe error before authentication
// runs, before the mutation audit lifecycle allocates anything, and before
// any resolver dispatches. The requests below carry no session headers: if
// authentication ran first they would fail with HTTP 401 "invalid session",
// so the observed budget error is itself proof of the pre-auth ordering.
func TestGraphQLCostBudgetsRejectBeforeAuthAndAudit(t *testing.T) {
	overDepth := "serverInfo"
	for range 65 {
		overDepth = "child { " + overDepth + " }"
	}
	amplified := "query { serverInfo { " + strings.Repeat("...Wide ", 33) + "} } " +
		"fragment Wide on Query { " + strings.Repeat("leaf ", 128) + "}"
	cases := map[string]struct {
		document string
		message  string
	}{
		"over-depth query": {
			document: "query { " + overDepth + " }",
			message:  "GraphQL selection depth exceeds 64",
		},
		"alias-repetition mutation": {
			document: "mutation { " + strings.Repeat("a: startLibraryScan ", 65) + "}",
			message:  "GraphQL expanded field work exceeds 64",
		},
		"fragment-amplification query": {
			document: amplified,
			message:  "GraphQL selection work exceeds 4096",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			handler := newTestHandler(t)
			audits := 0
			handler.mutationAuditAppend = func(context.Context, store.ActivityEntry) error {
				audits++
				return nil
			}
			body, err := json.Marshal(request{Query: tc.document})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(string(body)))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d (a 401 would mean auth ran before the cost gate)", rec.Code, http.StatusOK)
			}
			var resp struct {
				Data   json.RawMessage `json:"data"`
				Errors []graphError    `json:"errors"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if len(resp.Errors) != 1 || resp.Errors[0].Message != tc.message {
				t.Fatalf("errors = %#v, want exactly one %q", resp.Errors, tc.message)
			}
			if len(resp.Data) != 0 {
				t.Fatalf("data = %s, want absent", resp.Data)
			}
			if audits != 0 {
				t.Fatalf("audit appends = %d, want 0 (rejection must precede audit allocation)", audits)
			}
		})
	}
}
