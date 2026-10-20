// Package scrape executes declarative ScraperSpecs: fetch a
// remote document over one of the fixed transport kinds (http-json,
// http-graphql, rss) and map response paths to core fields and artwork slots.
// No plugin code runs here — specs are pure data, and this executor is what
// makes "data-only, safe by construction" true: every outbound request passes
// the ADR-0012 §9 SSRF gate (https only, no literal IPs, no private ranges,
// no cross-host redirects, size + time caps), including auth-style query
// parameters, headers, and the login-bearer token exchange.
//
// The identification flow is Search (free text → candidates)
// → a human or scorer picks one → Lookup (candidate ID → full document).
package scrape

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/plugin"
)

// Result is one scraper response mapped onto the core vocabulary: field name
// → value, artwork slot → image URL. Paths that did not resolve — and mapped
// values a declared transform could not parse — are absent.
type Result struct {
	// ID is set on Search candidates only: the spec's CandidateIDPath value,
	// the handle a chosen candidate feeds back into Lookup. Empty when the
	// spec declares no CandidateIDPath or the path did not resolve.
	ID      string
	Fields  map[string]any
	Artwork map[string]string
	// Entities are the spec's EntityMaps resolutions: one entry per hydrated
	// Entity, in declared-map then document order. Lookup documents only —
	// Search candidates never carry entities. Nil when the spec declares no
	// EntityMaps or none of their paths resolved.
	Entities []EntityResult
}

// EntityResult is one hydrated Entity from a spec's EntityMaps: the declared
// Kind and Media-targeted Relation plus the name resolved from the document.
type EntityResult struct {
	Kind     string
	Relation string
	Name     string
	// SortOrder is set to the source array index when the EntityMap Path
	// resolves to an array. Nil means the map resolved a scalar node.
	SortOrder *int
	// ArtworkURL is the absolute image URL the map's ArtworkPath resolved to
	// (after ArtworkBaseURL composition, the ArtworkMap treatment). Empty when
	// the map declares no ArtworkPath or the path did not resolve.
	ArtworkURL string
	// Fields are the map's FieldMap resolutions: declared field name → the
	// element-relative path's scalar string value, still raw (the consumer
	// parses each against the Kind's declared FieldSpec type, the same split as
	// Media FieldMap values). Nil when the map declares no FieldMap or none of
	// its paths resolved.
	Fields map[string]string
	// ParentName is the map's ParentNamePath resolution: the name of this
	// Entity's same-Kind parent (hierarchy). Empty when the map declares no
	// ParentNamePath or the path did not resolve.
	ParentName string
	// ParentRelation is the relation name the consumer links this Entity to its
	// parent under, copied from the EntityMap. Empty when there is no parent.
	ParentRelation string
}

// CredentialSource resolves an AuthRef to its secret. The engine never reads
// secret files or config itself (CLAUDE.md invariant 5) — the consumer wires
// in whatever store actually holds credentials.
type CredentialSource interface {
	Secret(ref string) (string, error)
}

// ConfigSource resolves persisted, non-secret configuration for one plugin.
type ConfigSource interface {
	PluginConfigValues(ctx context.Context, pluginID string) (map[string]string, error)
}

// Option configures an Engine at construction.
type Option func(*Engine)

// WithCredentials supplies the secret resolver used when a spec carries a
// non-empty AuthRef. Specs with an AuthRef fail without one.
func WithCredentials(source CredentialSource) Option {
	return func(e *Engine) { e.credentials = source }
}

// WithConfigSource supplies non-secret plugin configuration used by
// {config.<name>} substitutions.
func WithConfigSource(source ConfigSource) Option {
	return func(e *Engine) { e.config = source }
}

// WithTimeout overrides the per-request timeout (default 15s).
func WithTimeout(timeout time.Duration) Option {
	return func(e *Engine) { e.timeout = timeout }
}

// WithMaxResponseBytes overrides the response size cap (default 5 MiB).
func WithMaxResponseBytes(limit int64) Option {
	return func(e *Engine) { e.maxResponseBytes = limit }
}

// WithInsecureLoopback permits plain-http requests to loopback addresses
// (http://127.0.0.1:…) so tests can target httptest servers.
//
// TESTS ONLY. Never pass this option in production wiring: it bypasses the
// https requirement and the literal-IP/loopback denial for loopback targets.
// Every other SSRF rule (private ranges, redirect pinning, caps) still holds.
func WithInsecureLoopback() Option {
	return func(e *Engine) { e.insecureLoopback = true }
}

const (
	defaultMaxResponseBytes = 5 << 20
	defaultTimeout          = 15 * time.Second
	maxRedirects            = 3
	// retryAfterRetries is the small 429 cap: one initial request plus three
	// retries, enough to respect etiquette without stalling forever.
	retryAfterRetries = 3
	retryBackoffBase  = 25 * time.Millisecond
	retryBackoffCap   = 250 * time.Millisecond
	// maxCandidates bounds Search results — identification needs a short
	// pick list, not a remote service's whole result set.
	maxCandidates = 25
	// loginTokenTTL is how long a login-bearer token is reused before the
	// exchange runs again. Conservative: real issuers (TheTVDB v4) mint
	// month-long tokens, and the spec has no way to declare expiry.
	loginTokenTTL = time.Hour
	// userAgent identifies Beamfall on every engine request — remote-source
	// etiquette (MusicBrainz requires a descriptive User-Agent and throttles
	// anonymous clients harder without one).
	userAgent = "beamfall/dev (+https://beamfall.com)"
)

// Engine executes ScraperSpecs. Construct once with New and share; it is safe
// for concurrent use.
type Engine struct {
	credentials      CredentialSource
	config           ConfigSource
	timeout          time.Duration
	maxResponseBytes int64
	insecureLoopback bool
	client           *http.Client
	// streamClient serves DownloadResource: same hardened dialer and redirect
	// pinning, but no whole-request timeout — media-sized bodies outlive any
	// fixed wall clock, and ctx bounds the download instead.
	streamClient *http.Client
	clock        requestClock

	mu          sync.Mutex
	tokens      map[string]cachedToken // login-bearer cache, keyed endpoint+authref
	lastRequest map[string]time.Time   // rate-limit anchor per scraper ID
}

type cachedToken struct {
	token   string
	expires time.Time
}

type requestClock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

func (realClock) After(wait time.Duration) <-chan time.Time {
	return time.After(wait)
}

// New builds an Engine with the ADR-0012 §9 hardened HTTP client.
func New(opts ...Option) *Engine {
	e := &Engine{
		timeout:          defaultTimeout,
		maxResponseBytes: defaultMaxResponseBytes,
		tokens:           map[string]cachedToken{},
		lastRequest:      map[string]time.Time{},
		clock:            realClock{},
	}
	for _, opt := range opts {
		opt(e)
	}
	dialer := &net.Dialer{
		Timeout: e.timeout,
		// Control runs on the concrete IP the socket is about to connect to,
		// after DNS resolution — the DNS-rebinding-safe choke point.
		Control: e.controlDial,
	}
	transport := &http.Transport{
		Proxy:                 nil, // never route scraper traffic through env proxies
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   e.timeout,
		ResponseHeaderTimeout: e.timeout,
	}
	e.client = &http.Client{
		Timeout:       e.timeout,
		CheckRedirect: e.checkRedirect,
		Transport:     transport,
	}
	e.streamClient = &http.Client{
		// No Timeout: it covers the whole body read, which would cut large
		// downloads short. Headers/TLS/dial stay capped via the transport.
		CheckRedirect: e.checkRedirect,
		Transport:     transport,
	}
	return e
}

// Variables are the fingerprint substitution values sourced
// from the media file being identified: {oshash}, {phash}, {duration}. The
// engine always substitutes all three placeholders in both templates — an
// unknown value substitutes the empty string, so a fingerprint placeholder
// never leaks literally to a remote API.
type Variables struct {
	OSHash          string
	PHash           string
	DurationSeconds int
	// PluginID scopes {config.<name>} substitutions to the active bundle.
	PluginID string
}

// substitutions lists the placeholder→value pairs the fingerprint variables
// contribute on top of a template's primary placeholder.
func (v Variables) substitutions(page int) [][2]string {
	duration := ""
	if v.DurationSeconds > 0 {
		duration = strconv.Itoa(v.DurationSeconds)
	}
	pageValue := ""
	if page > 0 {
		pageValue = strconv.Itoa(page)
	}
	return [][2]string{{"{oshash}", v.OSHash}, {"{phash}", v.PHash}, {"{duration}", duration}, {"{page}", pageValue}}
}

var configPlaceholderPattern = regexp.MustCompile(`\{config\.([a-z0-9][a-z0-9-]*)\}`)

func (e *Engine) configValues(ctx context.Context, vars Variables) (map[string]string, error) {
	if e.config == nil || vars.PluginID == "" {
		return nil, nil
	}
	values, err := e.config.PluginConfigValues(ctx, vars.PluginID)
	if err != nil {
		return nil, fmt.Errorf("scrape: resolving config for plugin %q: %w", vars.PluginID, err)
	}
	return values, nil
}

func substituteConfig(value string, config map[string]string, escape func(string) string) string {
	return configPlaceholderPattern.ReplaceAllStringFunc(value, func(placeholder string) string {
		match := configPlaceholderPattern.FindStringSubmatch(placeholder)
		return escape(config[match[1]])
	})
}

// variablesArg collapses the optional trailing Variables argument.
func variablesArg(vars []Variables) Variables {
	if len(vars) > 0 {
		return vars[0]
	}
	return Variables{}
}

// Lookup executes spec's LookupTemplate for one item: every {id} is replaced
// with id, the fingerprint variables substitute (empty when not supplied),
// the document is fetched, and FieldMap/ArtworkMap paths are resolved
// against it. Paths that do not resolve are omitted, not errors.
func (e *Engine) Lookup(ctx context.Context, spec plugin.ScraperSpec, id string, vars ...Variables) (Result, error) {
	if spec.LookupTemplate == "" {
		return Result{}, fmt.Errorf("scrape: scraper %q declares no lookup template", spec.ID)
	}
	v := variablesArg(vars)
	config, err := e.configValues(ctx, v)
	if err != nil {
		return Result{}, err
	}
	switch spec.Kind {
	case "http-json":
		root, err := e.fetchJSONDocument(ctx, spec, spec.LookupTemplate, "{id}", id, v, config, 0)
		if err != nil {
			return Result{}, err
		}
		result := mapJSON(spec, spec.FieldMap, spec.ArtworkMap, root, config)
		result.Entities, err = e.resolveEntityMaps(ctx, spec, root, v, config)
		if err != nil {
			return Result{}, err
		}
		return result, nil
	case "http-graphql":
		data, err := e.fetchGraphQLData(ctx, spec, spec.LookupTemplate, "{id}", id, v, config, 0)
		if err != nil {
			return Result{}, err
		}
		result := mapJSON(spec, spec.FieldMap, spec.ArtworkMap, data, config)
		result.Entities, err = e.resolveEntityMaps(ctx, spec, data, v, config)
		if err != nil {
			return Result{}, err
		}
		return result, nil
	case "rss":
		return e.lookupRSS(ctx, spec, id, v, config)
	default:
		return Result{}, fmt.Errorf("scrape: scraper %q has unknown kind %q", spec.ID, spec.Kind)
	}
}

// Search executes spec's SearchTemplate with {query} (and the fingerprint
// variables, when supplied) substituted and returns
// one Result per element of the CandidatesPath array (capped), each with the
// search maps (SearchFieldMap/SearchArtworkMap when declared, else the shared
// FieldMap/ArtworkMap) resolved relative to that element and ID carrying the
// CandidateIDPath value that feeds Lookup.
func (e *Engine) Search(ctx context.Context, spec plugin.ScraperSpec, query string, vars ...Variables) ([]Result, error) {
	if spec.SearchTemplate == "" {
		return nil, fmt.Errorf("scrape: scraper %q declares no search template", spec.ID)
	}
	v := variablesArg(vars)
	config, err := e.configValues(ctx, v)
	if err != nil {
		return nil, err
	}
	switch spec.Kind {
	case "http-json", "http-graphql":
	case "rss":
		return nil, fmt.Errorf("scrape: scraper %q: rss scrapers do not search", spec.ID)
	default:
		return nil, fmt.Errorf("scrape: scraper %q has unknown kind %q", spec.ID, spec.Kind)
	}
	fieldMap, artworkMap := searchMaps(spec)
	results := make([]Result, 0, maxCandidates)
	for page := 1; page <= searchMaxPages(spec) && len(results) < maxCandidates; page++ {
		var root any
		var err error
		switch spec.Kind {
		case "http-json":
			root, err = e.fetchJSONDocument(ctx, spec, spec.SearchTemplate, "{query}", query, v, config, page)
		case "http-graphql":
			root, err = e.fetchGraphQLData(ctx, spec, spec.SearchTemplate, "{query}", query, v, config, page)
		}
		if err != nil {
			return nil, err
		}
		node, ok := resolveJSONNode(root, substituteConfig(spec.CandidatesPath, config, identity))
		if !ok {
			return nil, fmt.Errorf("scrape: scraper %q: candidates path %q did not resolve", spec.ID, spec.CandidatesPath)
		}
		elements, ok := node.([]any)
		if !ok {
			return nil, fmt.Errorf("scrape: scraper %q: candidates path %q is not an array", spec.ID, spec.CandidatesPath)
		}
		if len(elements) == 0 {
			break
		}
		if remaining := maxCandidates - len(results); len(elements) > remaining {
			elements = elements[:remaining]
		}
		for _, element := range elements {
			result := mapJSON(spec, fieldMap, artworkMap, element, config)
			if spec.CandidateIDPath != "" {
				result.ID, _ = resolveJSONPath(element, substituteConfig(spec.CandidateIDPath, config, identity))
			}
			results = append(results, result)
		}
	}
	return results, nil
}

func searchMaxPages(spec plugin.ScraperSpec) int {
	if spec.MaxPages <= 0 {
		return 1
	}
	return spec.MaxPages
}

// searchMaps picks the maps Search resolves against one candidate element:
// the search-scoped maps when declared, else the shared lookup maps — each
// falls back independently, so a source may override fields while reusing
// artwork (or vice versa). Same-shape sources declare one pair and are done.
func searchMaps(spec plugin.ScraperSpec) (fieldMap, artworkMap map[string]string) {
	fieldMap, artworkMap = spec.FieldMap, spec.ArtworkMap
	if spec.SearchFieldMap != nil {
		fieldMap = spec.SearchFieldMap
	}
	if spec.SearchArtworkMap != nil {
		artworkMap = spec.SearchArtworkMap
	}
	return fieldMap, artworkMap
}

// fetchJSONDocument runs one authorized http-json request built from
// template and decodes the response.
func (e *Engine) fetchJSONDocument(ctx context.Context, spec plugin.ScraperSpec, template, placeholder, value string, vars Variables, config map[string]string, page int) (any, error) {
	body, err := e.fetchAuthorized(ctx, spec, http.MethodGet, buildURL(spec.Endpoint, template, placeholder, value, vars, config, page), nil, "")
	if err != nil {
		return nil, err
	}
	root, err := decodeJSON(body)
	if err != nil {
		return nil, fmt.Errorf("scrape: scraper %q: %w", spec.ID, err)
	}
	return root, nil
}

var graphQLValueEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`, "\f", `\f`)

// fetchGraphQLData POSTs the substituted query and returns the response's
// `data` value, refusing responses that carry GraphQL errors.
func (e *Engine) fetchGraphQLData(ctx context.Context, spec plugin.ScraperSpec, template, placeholder, value string, vars Variables, config map[string]string, page int) (any, error) {
	query := template
	for _, pair := range append([][2]string{{placeholder, value}}, vars.substitutions(page)...) {
		query = strings.ReplaceAll(query, pair[0], graphQLValueEscaper.Replace(pair[1]))
	}
	query = substituteConfig(query, config, graphQLValueEscaper.Replace)
	payload, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return nil, fmt.Errorf("scrape: scraper %q: %w", spec.ID, err)
	}
	body, err := e.fetchAuthorized(ctx, spec, http.MethodPost, spec.Endpoint, payload, "application/json")
	if err != nil {
		return nil, err
	}
	root, err := decodeJSON(body)
	if err != nil {
		return nil, fmt.Errorf("scrape: scraper %q: %w", spec.ID, err)
	}
	doc, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("scrape: scraper %q: response is not a JSON object", spec.ID)
	}
	if errs, ok := doc["errors"].([]any); ok && len(errs) > 0 {
		return nil, fmt.Errorf("scrape: scraper %q: GraphQL response carries errors", spec.ID)
	}
	data, ok := doc["data"]
	if !ok {
		return nil, fmt.Errorf("scrape: scraper %q: GraphQL response has no data", spec.ID)
	}
	return data, nil
}

func (e *Engine) lookupRSS(ctx context.Context, spec plugin.ScraperSpec, id string, vars Variables, config map[string]string) (Result, error) {
	var rawURL string
	if strings.TrimSpace(spec.LookupTemplate) == "{id}" {
		// The template is exactly "{id}": the id IS the feed URL.
		rawURL = id
	} else {
		rawURL = buildURL(spec.Endpoint, spec.LookupTemplate, "{id}", id, vars, config, 0)
	}
	body, err := e.fetchAuthorized(ctx, spec, http.MethodGet, rawURL, nil, "")
	if err != nil {
		return Result{}, err
	}
	root, err := parseXML(body)
	if err != nil {
		return Result{}, fmt.Errorf("scrape: scraper %q: %w", spec.ID, err)
	}
	return mapResult(spec, spec.FieldMap, spec.ArtworkMap, config, func(path string) (any, bool) {
		value, ok := resolveXMLPath(root, path)
		return value, ok
	}), nil
}

// buildURL substitutes the primary placeholder and the fingerprint variables
// context-sensitively: path-escaped in the path, query-escaped after the
// first "?" — so a free-text search term containing "&" or "=" cannot inject
// extra query parameters.
func buildURL(endpoint, template, placeholder, value string, vars Variables, config map[string]string, page int) string {
	pairs := append([][2]string{{placeholder, value}}, vars.substitutions(page)...)
	path, query, hasQuery := strings.Cut(template, "?")
	for _, pair := range pairs {
		path = strings.ReplaceAll(path, pair[0], escapePathPlaceholder(pair[1]))
		if hasQuery {
			query = strings.ReplaceAll(query, pair[0], url.QueryEscape(pair[1]))
		}
	}
	path = substituteConfig(path, config, url.PathEscape)
	if hasQuery {
		query = substituteConfig(query, config, url.QueryEscape)
	}
	if !hasQuery {
		return endpoint + path
	}
	return endpoint + path + "?" + query
}

func identity(value string) string { return value }

func escapePathPlaceholder(value string) string {
	parts := strings.Split(value, "/")
	for i, part := range parts {
		escaped := url.PathEscape(part)
		switch part {
		case ".":
			escaped = "%2E"
		case "..":
			escaped = "%2E%2E"
		}
		parts[i] = escaped
	}
	return strings.Join(parts, "/")
}

func mapJSON(spec plugin.ScraperSpec, fieldMap, artworkMap map[string]string, node any, config map[string]string) Result {
	return mapResult(spec, fieldMap, artworkMap, config, func(path string) (any, bool) {
		return resolveJSONScalar(node, path)
	})
}

// mapResult resolves one document (or candidate element) through the given
// maps. Transforms apply by core field name regardless of which map carried
// the path — one transform vocabulary for both templates.
func mapResult(spec plugin.ScraperSpec, fieldMap, artworkMap map[string]string, config map[string]string, resolve func(string) (any, bool)) Result {
	result := Result{Fields: map[string]any{}, Artwork: map[string]string{}}
	for field, path := range fieldMap {
		path = substituteConfig(path, config, identity)
		raw, ok := resolve(path)
		if !ok {
			continue
		}
		value, ok := scalarString(raw)
		if !ok {
			continue
		}
		if kind := spec.Transforms[field]; kind != "" {
			if value, ok = transform(kind, value); !ok {
				continue // unparseable input is omitted, like an unresolved path
			}
		}
		typed, ok := coerceFieldValue(spec, field, value)
		if !ok {
			continue
		}
		result.Fields[field] = typed
	}
	for slot, path := range artworkMap {
		path = substituteConfig(path, config, identity)
		if raw, ok := resolve(path); ok {
			value, ok := scalarString(raw)
			if !ok {
				continue
			}
			result.Artwork[slot] = renderArtworkURL(spec.ArtworkBaseURL, spec.ArtworkURLTemplates[slot], value)
		}
	}
	return result
}

func coerceFieldValue(spec plugin.ScraperSpec, field string, value string) (any, bool) {
	switch fieldType(spec, field) {
	case "", "text", "date", "url":
		return value, true
	case "number":
		number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			logSkippedField(spec, field, "number", value)
			return nil, false
		}
		return number, true
	case "flag":
		flag, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			logSkippedField(spec, field, "flag", value)
			return nil, false
		}
		return flag, true
	default:
		return value, true
	}
}

func fieldType(spec plugin.ScraperSpec, field string) string {
	if fieldType := spec.FieldTypes[field]; fieldType != "" {
		return fieldType
	}
	switch field {
	case "year":
		return "number"
	default:
		return ""
	}
}

func logSkippedField(spec plugin.ScraperSpec, field, fieldType, value string) {
	slog.Default().Warn("scrape skipped unparseable field",
		"scraper", spec.ID,
		"field", field,
		"type", fieldType,
		"value", value,
	)
}

// resolveEntityMaps resolves the spec's EntityMaps against one JSON lookup
// document (the same path machinery FieldMap uses): a Path landing on an
// array hydrates one Entity per element, anything else hydrates one. Names
// come from NamePath within each element, or the element/value itself when
// NamePath is empty; ArtworkPath, FieldMap paths, and ParentNamePath resolve
// element-relative the same way (ArtworkPath composes with ArtworkBaseURL like
// ArtworkMap values; FieldMap values stay raw for the consumer to type-parse).
// Unresolved paths and blank values are omitted, never errors — the FieldMap
// posture. Validation already keeps EntityMaps off rss specs, whose documents
// are not JSON.
func (e *Engine) resolveEntityMaps(ctx context.Context, spec plugin.ScraperSpec, root any, vars Variables, config map[string]string) ([]EntityResult, error) {
	var entities []EntityResult
	for _, entityMap := range spec.EntityMaps {
		node, ok := resolveJSONNode(root, substituteConfig(entityMap.Path, config, identity))
		if !ok {
			continue
		}
		elements, isList := node.([]any)
		if !isList {
			elements = []any{node}
		}
		lookups := 0
		for i, element := range elements {
			source := element
			if entityMap.LookupTemplate != "" {
				id, ok := resolveJSONPath(element, substituteConfig(entityMap.LookupIDPath, config, identity))
				if !ok || strings.TrimSpace(id) == "" {
					continue
				}
				if entityMap.MaxLookups > 0 && lookups >= entityMap.MaxLookups {
					break
				}
				lookups++
				var err error
				source, err = e.fetchJSONDocument(ctx, spec, entityMap.LookupTemplate, "{id}", id, vars, config, 0)
				if err != nil {
					return nil, err
				}
			}
			var name string
			if entityMap.NamePath == "" {
				name, ok = jsonScalar(source)
			} else {
				name, ok = resolveJSONPath(source, substituteConfig(entityMap.NamePath, config, identity))
			}
			if name = strings.TrimSpace(name); !ok || name == "" {
				continue
			}
			entity := EntityResult{Kind: entityMap.Kind, Relation: entityMap.Relation, Name: name}
			if isList {
				order := i
				entity.SortOrder = &order
			}
			if entityMap.ArtworkPath != "" {
				if value, ok := resolveJSONPath(source, substituteConfig(entityMap.ArtworkPath, config, identity)); ok && value != "" {
					entity.ArtworkURL = renderArtworkURL(spec.ArtworkBaseURL, entityMap.ArtworkURLTemplate, value)
				}
			}
			for field, path := range entityMap.FieldMap {
				path = substituteConfig(path, config, identity)
				value, ok := resolveJSONPath(source, path)
				if !ok || strings.TrimSpace(value) == "" {
					continue
				}
				if entity.Fields == nil {
					entity.Fields = map[string]string{}
				}
				entity.Fields[field] = value
			}
			if entityMap.ParentNamePath != "" {
				if value, ok := resolveJSONPath(source, substituteConfig(entityMap.ParentNamePath, config, identity)); ok {
					if parent := strings.TrimSpace(value); parent != "" {
						entity.ParentName = parent
						entity.ParentRelation = entityMap.ParentRelation
					}
				}
			}
			entities = append(entities, entity)
		}
	}
	return entities, nil
}

// composeArtworkURL prefixes ArtworkBaseURL onto site-relative artwork paths
// (TMDB's "/abc.jpg"); absolute URLs pass through untouched.
func composeArtworkURL(base, value string) string {
	if base == "" || strings.Contains(value, "://") {
		return value
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(value, "/")
}

func renderArtworkURL(base, tmpl, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if tmpl != "" {
		return strings.ReplaceAll(tmpl, "{value}", url.PathEscape(value))
	}
	return composeArtworkURL(base, value)
}

// fetchAuthorized runs one rate-limited, authorized request for spec. When a
// login-bearer spec gets 401 back, the cached token is treated as expired:
// invalidate it, re-login once (inside the second authorizer call), and retry
// the request once. A second 401 surfaces as the error.
func (e *Engine) fetchAuthorized(ctx context.Context, spec plugin.ScraperSpec, method, rawURL string, payload []byte, contentType string) ([]byte, error) {
	attempt := func() ([]byte, error) {
		authorize, err := e.authorizer(ctx, spec)
		if err != nil {
			return nil, err
		}
		if err := e.waitTurn(ctx, spec); err != nil {
			return nil, err
		}
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		return e.fetch(ctx, method, rawURL, body, contentType, authorize)
	}
	data, err := attempt()
	var status *statusError
	if spec.AuthStyle == "login-bearer" && errors.As(err, &status) && status.code == http.StatusUnauthorized {
		e.invalidateToken(spec)
		return attempt()
	}
	return data, err
}

// waitTurn enforces spec's MinRequestIntervalMS per scraper ID: it blocks
// until at least that interval has passed since this scraper's previous
// request, then claims the slot. Specs without an interval pass straight
// through. Cancelling ctx ends the wait.
func (e *Engine) waitTurn(ctx context.Context, spec plugin.ScraperSpec) error {
	if spec.MinRequestIntervalMS <= 0 {
		return nil
	}
	interval := time.Duration(spec.MinRequestIntervalMS) * time.Millisecond
	for {
		e.mu.Lock()
		now := e.clock.Now()
		wait := interval - now.Sub(e.lastRequest[spec.ID])
		if wait <= 0 { // the zero time (no prior request) lands here too
			e.lastRequest[spec.ID] = now
			e.mu.Unlock()
			return nil
		}
		e.mu.Unlock()
		if err := e.wait(ctx, wait, fmt.Sprintf("scraper %q rate-limit wait", spec.ID)); err != nil {
			return err
		}
	}
}

// Credential resolves a secure-store ref to its literal secret through the
// configured CredentialSource (invariant #5: the engine never reads secret
// files itself). It is the resolver the subscription poller uses to turn a
// Subscription's stored auth_ref into the private-feed Authorization header at
// fetch time, so the credential never lives in the catalog DB. An empty ref
// returns ("", nil) — an anonymous feed needs no secret. A non-empty ref with
// no CredentialSource, or an unresolvable ref, is an error at use time.
func (e *Engine) Credential(ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	if e.credentials == nil {
		return "", fmt.Errorf("scrape: credential %q requested but no CredentialSource is configured", ref)
	}
	return e.credentials.Secret(ref)
}

// CredentialAvailable reports whether ref currently resolves to a non-empty
// secret. Identification uses it to omit unkeyed OptionalAuth scrapers.
func (e *Engine) CredentialAvailable(ref string) bool {
	secret, err := e.Credential(ref)
	return err == nil && strings.TrimSpace(secret) != ""
}

// InvalidateCredential drops every cached login-bearer token derived from one
// AuthRef. Credential rotation/disable is AuthRef-scoped; a shared ref can feed
// multiple scraper endpoints, so delete all endpoint+ref cache entries.
func (e *Engine) InvalidateCredential(ref string) {
	if ref == "" {
		return
	}
	suffix := "\x00" + ref
	e.mu.Lock()
	defer e.mu.Unlock()
	for key := range e.tokens {
		if strings.HasSuffix(key, suffix) {
			delete(e.tokens, key)
		}
	}
}

// authorizer resolves spec's AuthRef and returns the request-mutation step
// for its AuthStyle (one of the fixed plugin.AuthStyleForms), or nil for
// unauthenticated specs. The login-bearer exchange happens here, through the
// same vetted fetch path as every other scraper request.
func (e *Engine) authorizer(ctx context.Context, spec plugin.ScraperSpec) (func(*http.Request), error) {
	if spec.AuthRef == "" {
		return nil, nil
	}
	if e.credentials == nil {
		return nil, fmt.Errorf("scrape: spec requires credential %q but no CredentialSource is configured", spec.AuthRef)
	}
	secret, err := e.credentials.Secret(spec.AuthRef)
	if err != nil {
		return nil, fmt.Errorf("scrape: resolving credential %q: %w", spec.AuthRef, err)
	}
	style := spec.AuthStyle
	if style == "" {
		style = "bearer"
	}
	switch {
	case style == "bearer":
		return func(req *http.Request) {
			req.Header.Set("Authorization", "Bearer "+secret)
		}, nil
	case style == "login-bearer":
		token, err := e.loginToken(ctx, spec, secret)
		if err != nil {
			return nil, err
		}
		return func(req *http.Request) {
			req.Header.Set("Authorization", "Bearer "+token)
		}, nil
	case strings.HasPrefix(style, "header:"):
		name := strings.TrimPrefix(style, "header:")
		return func(req *http.Request) {
			req.Header.Set(name, secret)
		}, nil
	case strings.HasPrefix(style, "query:"):
		param := strings.TrimPrefix(style, "query:")
		return func(req *http.Request) {
			query := req.URL.Query()
			query.Set(param, secret)
			req.URL.RawQuery = query.Encode()
		}, nil
	}
	return nil, fmt.Errorf("scrape: scraper %q has unknown auth style %q", spec.ID, spec.AuthStyle)
}

// loginToken runs (or reuses) the declarative login-bearer exchange: POST
// {SecretField: secret} as JSON to Endpoint+Login.Path, read the token at
// Login.TokenPath. Tokens are cached per endpoint+AuthRef for loginTokenTTL.
func (e *Engine) loginToken(ctx context.Context, spec plugin.ScraperSpec, secret string) (string, error) {
	if spec.Login == nil {
		return "", fmt.Errorf("scrape: scraper %q uses login-bearer without a Login declaration", spec.ID)
	}
	key := spec.Endpoint + "\x00" + spec.AuthRef
	e.mu.Lock()
	cached, ok := e.tokens[key]
	e.mu.Unlock()
	if ok && e.clock.Now().Before(cached.expires) {
		return cached.token, nil
	}
	payload, err := json.Marshal(map[string]string{spec.Login.SecretField: secret})
	if err != nil {
		return "", fmt.Errorf("scrape: scraper %q login: %w", spec.ID, err)
	}
	// The login exchange is a real request to the source: it waits its
	// rate-limit turn like any other.
	if err := e.waitTurn(ctx, spec); err != nil {
		return "", err
	}
	// The exchange itself carries no authorizer — the secret travels in the
	// body — and passes the same SSRF gate and caps as every request.
	body, err := e.fetch(ctx, http.MethodPost, spec.Endpoint+spec.Login.Path, bytes.NewReader(payload), "application/json", nil)
	if err != nil {
		return "", err
	}
	root, err := decodeJSON(body)
	if err != nil {
		return "", fmt.Errorf("scrape: scraper %q login: %w", spec.ID, err)
	}
	token, ok := resolveJSONPath(root, spec.Login.TokenPath)
	if !ok || token == "" {
		return "", fmt.Errorf("scrape: scraper %q login response has no token at %q", spec.ID, spec.Login.TokenPath)
	}
	e.mu.Lock()
	e.tokens[key] = cachedToken{token: token, expires: e.clock.Now().Add(loginTokenTTL)}
	e.mu.Unlock()
	return token, nil
}

// invalidateToken drops spec's cached login-bearer token so the next
// authorizer call runs the exchange again — the 401-means-expired path.
func (e *Engine) invalidateToken(spec plugin.ScraperSpec) {
	e.mu.Lock()
	delete(e.tokens, spec.Endpoint+"\x00"+spec.AuthRef)
	e.mu.Unlock()
}

// fetch performs one hardened request and returns the capped response body.
// authorize runs after the URL passes the SSRF gate; it may add headers or
// query parameters but never changes scheme or host.
func (e *Engine) fetch(ctx context.Context, method, rawURL string, body io.Reader, contentType string, authorize func(*http.Request)) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("scrape: invalid request URL: %w", err)
	}
	if err := e.vetURL(parsed); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, parsed.String(), body)
	if err != nil {
		return nil, fmt.Errorf("scrape: %w", err)
	}
	// Source etiquette: every request identifies the app (MusicBrainz
	// rejects or throttles UA-less clients).
	req.Header.Set("User-Agent", userAgent)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if authorize != nil {
		authorize(req)
	}
	resp, err := e.requestWith429Retry(ctx, req, func(attempt *http.Request) (*http.Response, error) {
		return e.client.Do(attempt)
	})
	if err != nil {
		return nil, sanitizedTransportError(parsed, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &statusError{host: parsed.Hostname(), code: resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, e.maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("scrape: reading response: %w", err)
	}
	if int64(len(data)) > e.maxResponseBytes {
		return nil, fmt.Errorf("scrape: response from %s exceeds the %d-byte cap", parsed.Hostname(), e.maxResponseBytes)
	}
	return data, nil
}

func (e *Engine) wait(ctx context.Context, wait time.Duration, label string) error {
	if wait <= 0 {
		return nil
	}
	timer := e.clock.After(wait)
	select {
	case <-ctx.Done():
		return fmt.Errorf("scrape: %s: %w", label, ctx.Err())
	case <-timer:
		return nil
	}
}

func (e *Engine) requestWith429Retry(ctx context.Context, req *http.Request, do func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	var retryAfter string
	for attempt := 0; ; attempt++ {
		attemptReq, err := cloneRequestForRetry(ctx, req, attempt)
		if err != nil {
			return nil, err
		}
		resp, err := do(attemptReq)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}
		retryAfter = resp.Header.Get("Retry-After")
		resp.Body.Close()
		if attempt >= retryAfterRetries {
			return nil, &statusError{host: req.URL.Hostname(), code: resp.StatusCode, retryAfter: retryAfter}
		}
		wait := retryBackoff(attempt)
		if parsed, ok := parseRetryAfter(retryAfter, e.clock.Now()); ok {
			wait = parsed
		}
		if err := e.wait(ctx, wait, "retry wait"); err != nil {
			return nil, err
		}
	}
}

func cloneRequestForRetry(ctx context.Context, req *http.Request, attempt int) (*http.Request, error) {
	attemptReq := req.Clone(ctx)
	if attempt == 0 || req.Body == nil {
		return attemptReq, nil
	}
	if req.GetBody == nil {
		return nil, fmt.Errorf("scrape: request body cannot be retried")
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("scrape: rebuilding request body: %w", err)
	}
	attemptReq.Body = body
	return attemptReq, nil
}

func retryBackoff(attempt int) time.Duration {
	wait := retryBackoffBase << attempt
	if wait > retryBackoffCap {
		return retryBackoffCap
	}
	return wait
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, true
	}
	if when, err := time.Parse(http.TimeFormat, value); err == nil {
		wait := when.Sub(now)
		if wait < 0 {
			wait = 0
		}
		return wait, true
	}
	return 0, false
}

// statusError is a non-2xx response, kept structured so fetchAuthorized can
// recognize the 401-means-expired-token case. Message unchanged from the old
// flat error.
type statusError struct {
	host       string
	code       int
	retryAfter string
}

func (s *statusError) Error() string {
	return fmt.Sprintf("scrape: %s returned status %d", s.host, s.code)
}

func decodeJSON(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}
	return root, nil
}

// FetchResource GETs one unauthenticated resource (scraped artwork URLs)
// through the same SSRF-vetted client and response cap every scraper request
// uses. Callers own interpreting the bytes.
func (e *Engine) FetchResource(ctx context.Context, rawURL string) ([]byte, error) {
	return e.fetch(ctx, http.MethodGet, rawURL, nil, "", func(*http.Request) {})
}
