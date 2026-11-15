package server

import (
	"bytes"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"

	apidocs "github.com/beamfall/core/docs/api"
)

// Paths at which a running server publishes its developer API reference
// (CORE-1714). They sit outside /v1/ because they describe that surface rather
// than belong to it.
const (
	apiDocsPath        = "/api/docs"
	apiDocsOpenAPIPath = "/api/docs/openapi.json"
	// apiDocsOpenAPIHref links the reference page to the document relative to
	// the page's own URL, so the link survives a reverse-proxy path prefix.
	apiDocsOpenAPIHref = "docs/openapi.json"
)

type apiDocsOperation struct {
	Method        string
	Path          string
	ID            string
	Summary       string
	Description   string
	RequiredScope string
	Credentials   []string
}

type apiDocsPage struct {
	Title       string
	Version     string
	Description string
	SpecPath    string
	SpecHref    string
	Operations  []apiDocsOperation
}

var (
	apiDocsOnce sync.Once
	apiDocsHTML []byte
	apiDocsErr  error
)

var apiDocsMethodOrder = map[string]int{"get": 0, "post": 1, "put": 2, "patch": 3, "delete": 4}

var apiDocsTemplate = template.Must(template.New("apidocs").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} {{.Version}}</title>
<style>
body{font:16px/1.5 system-ui,sans-serif;max-width:60rem;margin:0 auto;padding:1rem;color:#1b1b1f;background:#fff}
@media (prefers-color-scheme:dark){body{color:#e6e6ea;background:#141418}a{color:#9cc3ff}}
code{font-family:ui-monospace,monospace}
section{border-top:1px solid #8884;padding:.75rem 0}
.method{font-weight:700;text-transform:uppercase;margin-right:.5rem}
.meta{opacity:.75;font-size:.9rem}
</style>
</head>
<body>
<h1>{{.Title}} <small>{{.Version}}</small></h1>
<p>{{.Description}}</p>
<p>Machine-readable document: <a href="{{.SpecHref}}"><code>{{.SpecPath}}</code></a></p>
{{range .Operations}}<section id="{{.ID}}">
<h2><span class="method">{{.Method}}</span><code>{{.Path}}</code></h2>
{{if .Summary}}<p><strong>{{.Summary}}</strong></p>{{end}}
{{if .Description}}<p>{{.Description}}</p>{{end}}
<p class="meta">{{if .RequiredScope}}Scope: <code>{{.RequiredScope}}</code>. {{end}}{{if .Credentials}}Credentials: {{range $i, $c := .Credentials}}{{if $i}}, {{end}}<code>{{$c}}</code>{{end}}.{{end}}</p>
</section>
{{end}}</body>
</html>
`))

// renderAPIDocs builds the browsable reference from the embedded OpenAPI
// document, so the page and the machine-readable document cannot disagree.
func renderAPIDocs(spec []byte) ([]byte, error) {
	var doc struct {
		Info struct {
			Title       string `json:"title"`
			Version     string `json:"version"`
			Description string `json:"description"`
		} `json:"info"`
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(spec, &doc); err != nil {
		return nil, err
	}
	page := apiDocsPage{
		Title:       doc.Info.Title,
		Version:     doc.Info.Version,
		Description: doc.Info.Description,
		SpecPath:    apiDocsOpenAPIPath,
		SpecHref:    apiDocsOpenAPIHref,
	}
	for path, item := range doc.Paths {
		for method, raw := range item {
			if _, ok := apiDocsMethodOrder[method]; !ok {
				continue // path-level keys such as "parameters"
			}
			var op struct {
				OperationID   string   `json:"operationId"`
				Summary       string   `json:"summary"`
				Description   string   `json:"description"`
				RequiredScope string   `json:"x-required-scope"`
				Credentials   []string `json:"x-credentials"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				return nil, err
			}
			page.Operations = append(page.Operations, apiDocsOperation{
				Method:        strings.ToUpper(method),
				Path:          path,
				ID:            op.OperationID,
				Summary:       op.Summary,
				Description:   op.Description,
				RequiredScope: op.RequiredScope,
				Credentials:   op.Credentials,
			})
		}
	}
	sort.Slice(page.Operations, func(i, j int) bool {
		a, b := page.Operations[i], page.Operations[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return apiDocsMethodOrder[strings.ToLower(a.Method)] < apiDocsMethodOrder[strings.ToLower(b.Method)]
	})
	var out bytes.Buffer
	if err := apiDocsTemplate.Execute(&out, page); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func setAPIDocsHeaders(h http.Header, contentType string) {
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
}

func (s *Server) apiDocsOpenAPI(w http.ResponseWriter, _ *http.Request) {
	setAPIDocsHeaders(w.Header(), "application/json")
	_, _ = w.Write(apidocs.OpenAPI())
}

func (s *Server) apiDocsReference(w http.ResponseWriter, r *http.Request) {
	apiDocsOnce.Do(func() { apiDocsHTML, apiDocsErr = renderAPIDocs(apidocs.OpenAPI()) })
	if apiDocsErr != nil {
		slog.ErrorContext(r.Context(), "render API reference", "err", apiDocsErr)
		http.Error(w, "API reference unavailable", http.StatusInternalServerError)
		return
	}
	setAPIDocsHeaders(w.Header(), "text/html; charset=utf-8")
	_, _ = w.Write(apiDocsHTML)
}
