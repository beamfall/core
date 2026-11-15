package scrape

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/beamfall/core/internal/plugin"
)

// referencePlaceholderPattern matches a {ref.<name>:<path>} map-path
// placeholder: the scalar at <path> in the scraper's named reference
// document (CORE-1128).
var referencePlaceholderPattern = regexp.MustCompile(`\{ref\.([a-z0-9][a-z0-9-]*):([^{}]*)\}`)

// referenceMaps returns m with every {ref.<name>:<path>} placeholder replaced
// by the value it reads, so a source's own ids (artwork type ids, say) are
// looked up instead of hard-coded. An alternative whose placeholder does not
// resolve, because the document is undeclared, unavailable or lacks the
// path, is dropped, and an entry left with no alternative is omitted: like an
// unresolved map path, it is never an error. Maps without placeholders are
// returned unchanged and fetch nothing.
func (e *Engine) referenceMaps(ctx context.Context, spec plugin.ScraperSpec, config map[string]string, m map[string]string) map[string]string {
	if !mapHasReferences(m) {
		return m
	}
	documents := e.extensions(spec).ReferenceDocuments
	// fetched memoizes this call's documents by URL, failures included, so
	// an unavailable document costs one request however many paths read it.
	fetched := map[string]any{}
	out := make(map[string]string, len(m))
	for key, path := range m {
		if !strings.Contains(path, "{ref.") {
			out[key] = path
			continue
		}
		path = substituteConfig(path, config, identity)
		var kept []string
		for _, alternative := range pathAlternatives(path) {
			if resolved, ok := e.substituteReferences(ctx, spec, documents, config, fetched, alternative); ok {
				kept = append(kept, resolved)
			}
		}
		if len(kept) > 0 {
			out[key] = strings.Join(kept, "||")
		}
	}
	return out
}

func mapHasReferences(m map[string]string) bool {
	for _, path := range m {
		if strings.Contains(path, "{ref.") {
			return true
		}
	}
	return false
}

// substituteReferences replaces each placeholder in one path alternative,
// reporting false when any of them does not resolve.
func (e *Engine) substituteReferences(ctx context.Context, spec plugin.ScraperSpec, documents map[string]plugin.ReferenceDocument, config map[string]string, fetched map[string]any, path string) (string, bool) {
	resolved := true
	out := referencePlaceholderPattern.ReplaceAllStringFunc(path, func(placeholder string) string {
		match := referencePlaceholderPattern.FindStringSubmatch(placeholder)
		declaration, ok := documents[match[1]]
		if !ok {
			resolved = false
			return ""
		}
		rawURL := buildURL(spec.Endpoint, declaration.Path, "{id}", "", Variables{}, config, 0)
		document, seen := fetched[rawURL]
		if !seen {
			document, _ = e.referenceDocument(ctx, spec, declaration, rawURL)
			fetched[rawURL] = document
		}
		if document == nil {
			resolved = false
			return ""
		}
		value, ok := resolveJSONPath(document, match[2])
		if !ok || value == "" {
			resolved = false
			return ""
		}
		return value
	})
	if !resolved || strings.Contains(out, "{ref.") {
		return "", false
	}
	return out, true
}

// referenceDocument returns the decoded document for declaration, fetched
// with the scraper's authorization and reused until its cache window ends.
// A failed fetch is logged and not cached, so the next lookup retries it.
// The log names the scraper and declared path only: a transport error can
// carry the request URL, which a query-style credential is part of.
func (e *Engine) referenceDocument(ctx context.Context, spec plugin.ScraperSpec, declaration plugin.ReferenceDocument, rawURL string) (any, bool) {
	now := e.clock.Now()
	e.mu.Lock()
	cached, ok := e.references[rawURL]
	e.mu.Unlock()
	if ok && now.Before(cached.expires) {
		return cached.document, true
	}
	body, err := e.fetchAuthorized(ctx, spec, http.MethodGet, rawURL, nil, "")
	if err != nil {
		slog.Warn("scrape: reference document unavailable", "scraper", spec.ID, "path", declaration.Path)
		return nil, false
	}
	document, err := decodeJSON(body)
	if err != nil {
		slog.Warn("scrape: reference document is not JSON", "scraper", spec.ID, "path", declaration.Path)
		return nil, false
	}
	hours := declaration.CacheHours
	if hours <= 0 {
		hours = defaultReferenceCacheHours
	}
	e.mu.Lock()
	e.references[rawURL] = cachedReference{document: document, expires: now.Add(time.Duration(hours) * time.Hour)}
	e.mu.Unlock()
	return document, true
}
