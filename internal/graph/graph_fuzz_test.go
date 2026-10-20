package graph

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/store/storetest"
)

func FuzzRequestDecodeAndFieldScan(f *testing.F) {
	seeds := []string{
		`query { serverInfo { name version } }`,
		`query SourceItems($kind: String!) { sourceItems(source: { kind: $kind }) { id title } }`,
		`query { alias: serverInfo { name } ... on Query { currentSession { id } } }`,
		`query { serverInfo `,
		`query A { serverInfo } query A { currentSession { id } }`,
		`query { ...A } fragment A on Query { ...B } fragment B on Query { ...A }`,
		`{ serverInfo } query Named { currentSession { id } }`,
	}
	deep := "query { serverInfo {" + strings.Repeat(" nested {", 64) + " name " + strings.Repeat("}", 64) + "} }"
	seeds = append(seeds, deep)
	// Cost-budget seeds: documents at and
	// over the depth, node, occurrence, and fragment-amplification budgets.
	overDeep := "query { serverInfo {" + strings.Repeat(" nested {", 80) + " name " + strings.Repeat("}", 80) + "} }"
	aliasStorm := "query { " + strings.Repeat("a: serverInfo ", 80) + "}"
	wideSubtree := "query { serverInfo { " + strings.Repeat("leaf ", 4200) + "} }"
	amplified := "query { serverInfo { " + strings.Repeat("...Wide ", 40) + "} } " +
		"fragment Wide on Query { " + strings.Repeat("leaf ", 128) + "}"
	var spreads, fragmentDefs strings.Builder
	for i := range 80 {
		fmt.Fprintf(&spreads, "...F%d ", i)
		fmt.Fprintf(&fragmentDefs, "fragment F%d on Query { leaf%d } ", i, i)
	}
	fragmentStorm := "query { serverInfo { " + spreads.String() + "} } " + fragmentDefs.String()
	seeds = append(seeds, overDeep, aliasStorm, wideSubtree, amplified, fragmentStorm)
	for _, query := range seeds {
		body, _ := json.Marshal(request{Query: query})
		f.Add(body)
	}
	f.Add([]byte(`not json`))

	st := storetest.Open(f)
	handler := NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, nil),
		auth.NewService(st),
		st,
		nil,
	)
	f.Fuzz(func(t *testing.T, data []byte) {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(data))
		handler.ServeHTTP(httptest.NewRecorder(), req)
	})
}
