package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// The served document must be the contract-checked docs/api/openapi.json,
// byte for byte (CORE-1714).
func TestAPIDocsServesContractOpenAPIDocument(t *testing.T) {
	want, err := os.ReadFile("../../docs/api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, apiDocsOpenAPIPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), want) {
		t.Fatal("served OpenAPI document differs from docs/api/openapi.json")
	}
}

func TestAPIDocsReferenceListsEveryOperation(t *testing.T) {
	spec, err := os.ReadFile("../../docs/api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Info  struct{ Title, Version string } `json:"info"`
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(spec, &doc); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, apiDocsPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("Content-Security-Policy = %q", csp)
	}
	// The relative link must land on the document from the page, with or
	// without a reverse-proxy path prefix in front of both.
	for _, prefix := range []string{"", "/beamfall"} {
		page, _ := url.Parse("http://host" + prefix + apiDocsPath)
		if got := page.ResolveReference(&url.URL{Path: apiDocsOpenAPIHref}).Path; got != prefix+apiDocsOpenAPIPath {
			t.Fatalf("link from %s resolves to %s, want %s", page.Path, got, prefix+apiDocsOpenAPIPath)
		}
	}
	body := rec.Body.String()
	for _, want := range []string{doc.Info.Title, doc.Info.Version, `href="` + apiDocsOpenAPIHref + `"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("reference page missing %q", want)
		}
	}
	if strings.Contains(body, "<script") {
		t.Fatal("reference page must not carry script")
	}
	ops := 0
	for path, item := range doc.Paths {
		for method, op := range item {
			if _, ok := apiDocsMethodOrder[method]; !ok {
				continue
			}
			ops++
			if !strings.Contains(body, `id="`+op.OperationID+`"`) || !strings.Contains(body, "<code>"+path+"</code>") {
				t.Fatalf("reference page missing %s %s (%s)", method, path, op.OperationID)
			}
		}
	}
	if ops == 0 {
		t.Fatal("spec has no operations")
	}
}

func TestAPIDocsRoutesNeedNoCredential(t *testing.T) {
	for _, pattern := range []string{"GET " + apiDocsPath, "GET " + apiDocsOpenAPIPath} {
		if got := exactRouteClasses[pattern]; got != AccessInternalOnly {
			t.Fatalf("%s class = %q, want %q", pattern, got, AccessInternalOnly)
		}
	}
}
