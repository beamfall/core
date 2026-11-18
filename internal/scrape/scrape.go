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
	"io/fs"
	"log/slog"
	"maps"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
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
	// SortOrder is the relation row's sort position: the map's OrdinalField
	// value when it parses as an integer, else the source array index when the
	// EntityMap Path resolves to an array, plus the map's OrdinalOffset. Nil
	// means the map resolved a scalar node and declared no usable ordinal.
	SortOrder *int
	// RelationAttributes are the map's RelationAttributeMap resolutions:
	// declared relation attribute → the Path element's scalar value (never a
	// LookupTemplate document's). Nil when the map declares none or none
	// resolved.
	RelationAttributes map[string]string
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
	// Container marks a container map's result: it hydrates the existing
	// container Entity of Kind that holds the looked-up Media, so it carries
	// no Name, Relation or parent and the consumer never creates an Entity
	// from it.
	Container bool
	// Artwork holds a container result's ArtworkMap resolutions: artwork slot
	// → absolute image URL, composed like Media artwork. Nil when the map
	// declares no ArtworkMap or none of its paths resolved.
	Artwork map[string]string
	// Title is a container result's TitlePath resolution: the display title
	// that replaces the container Entity's title. Empty keeps the title.
	Title string
	// Links are a container result's ContainerRelations resolutions: the
	// Entities sibling maps hydrated in the same lookup, each under the
	// container relation it is linked by.
	Links []EntityLink
	// LinkRelations lists, sorted, the container relations the map declares:
	// Links holds every link under them this lookup resolved, so a relation
	// with no Links resolved to none.
	LinkRelations []string
	// containerRelations is the container map's ContainerRelations, which
	// linkContainerResults resolves into Links once every map has resolved.
	containerRelations map[string]string
}

// EntityLink is one container → Entity link a ContainerRelations entry
// resolved: the container relation name and the hydrated sibling Entity's
// kind, name and sort order.
type EntityLink struct {
	Relation  string
	Kind      string
	Name      string
	SortOrder *int
}

// CredentialSource resolves an AuthRef to its secret. The engine never reads
// secret files or config itself (CLAUDE.md invariant 5) — the consumer wires
// in whatever store actually holds credentials.
type CredentialSource interface {
	Secret(ref string) (string, error)
}

// CredentialMissingError reports that a scraper needs a credential that is not
// configured: no CredentialSource, or the source reports the secret absent
// (fs.ErrNotExist). It carries only the AuthRef, never a secret or file path,
// so callers can report it once as a setup hint instead of a per-request
// filesystem error (CORE-1101). Other resolution failures (a bad file mode,
// an empty file) stay ordinary errors.
type CredentialMissingError struct {
	AuthRef string
	err     error
}

func (e *CredentialMissingError) Error() string {
	return fmt.Sprintf("scrape: credential %q is not configured", e.AuthRef)
}

func (e *CredentialMissingError) Unwrap() error { return e.err }

// MissingCredential names the absent credential ref (scan's
// MissingCredentialError view).
func (e *CredentialMissingError) MissingCredential() string { return e.AuthRef }

// resolveCredential reads ref from source, mapping an absent secret to
// CredentialMissingError and wrapping any other failure with context.
func resolveCredential(source CredentialSource, ref, context string) (string, error) {
	if source == nil {
		return "", &CredentialMissingError{AuthRef: ref}
	}
	secret, err := source.Secret(ref)
	if errors.Is(err, fs.ErrNotExist) {
		return "", &CredentialMissingError{AuthRef: ref, err: err}
	}
	if err != nil {
		return "", fmt.Errorf("scrape: %sresolving credential %q: %w", context, ref, err)
	}
	return secret, nil
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
	// exchange runs again. TheTVDB v4's swagger documents its tokens as valid
	// for one month (read 2026-11-15); reuse stops a few days short of that,
	// a 401 still forces a fresh exchange, and rotating or disabling any
	// credential the exchange sent drops the token (InvalidateCredential).
	loginTokenTTL = 27 * 24 * time.Hour
	// defaultReferenceCacheHours is how long a reference document is reused
	// when its declaration sets no CacheHours: a week, the usual guidance for
	// static lookup endpoints.
	defaultReferenceCacheHours = 7 * 24
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
	tokens      map[string]cachedToken     // login-bearer cache, keyed endpoint+authref
	lastRequest map[string]time.Time       // rate-limit anchor per scraper ID
	references  map[string]cachedReference // reference documents, keyed by URL

	// extensions reads the scraper declarations the pinned plugin-sdk does
	// not carry yet; New sets plugin.ScraperExtensionsOf.
	extensions func(plugin.ScraperSpec) plugin.ScraperExtensions
}

type cachedReference struct {
	document any
	expires  time.Time
}

type cachedToken struct {
	token   string
	expires time.Time
	refs    []string // every AuthRef the login exchange sent
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
		references:       map[string]cachedReference{},
		extensions:       plugin.ScraperExtensionsOf,
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

// Variables are the substitution values sourced from the media file being
// identified: {oshash}, {phash}, {duration} and {year}. The engine always
// substitutes every placeholder in both templates — an unknown value
// substitutes the empty string, so a placeholder never leaks literally to a
// remote API, and a query parameter whose whole value is {year} is dropped
// when the year is unknown.
type Variables struct {
	OSHash          string
	PHash           string
	DurationSeconds int
	// Year is the item's release year when identification knows it (parsed
	// from the file name or carried by the item); zero when unknown.
	Year int
	// PluginID scopes {config.<name>} substitutions to the active bundle.
	PluginID string
	// Sequence and Index are the item's parsed ordinal ({sequence}, {index}):
	// the plugin parser's sequence text and index. An empty Sequence or a
	// non-positive Index is unknown.
	Sequence string
	Index    int
}

// sequenceValue is the {sequence} substitution: the parsed sequence, a
// numeric one without leading zeros so it matches a source's numbers ("01"
// selects season_number 1). Empty when unknown.
func (v Variables) sequenceValue() string {
	sequence := strings.TrimSpace(v.Sequence)
	if sequence == "" || strings.Trim(sequence, "0123456789") != "" {
		return sequence
	}
	if trimmed := strings.TrimLeft(sequence, "0"); trimmed != "" {
		return trimmed
	}
	return "0"
}

// indexValue is the {index} substitution; empty when unknown.
func (v Variables) indexValue() string {
	if v.Index <= 0 {
		return ""
	}
	return strconv.Itoa(v.Index)
}

// namesUnknownOrdinal reports whether text names {sequence} or {index} while
// that value is unknown, so a caller can skip it rather than resolve it blank.
func (v Variables) namesUnknownOrdinal(text string) bool {
	return (strings.Contains(text, "{sequence}") && v.sequenceValue() == "") ||
		(strings.Contains(text, "{index}") && v.indexValue() == "")
}

// substituteOrdinals replaces {sequence} and {index} in an entity map path.
func (v Variables) substituteOrdinals(path string) string {
	return strings.NewReplacer("{sequence}", v.sequenceValue(), "{index}", v.indexValue()).Replace(path)
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
	year := ""
	if v.Year > 0 {
		year = strconv.Itoa(v.Year)
	}
	return [][2]string{{"{oshash}", v.OSHash}, {"{phash}", v.PHash}, {"{duration}", duration}, {"{page}", pageValue}, {"{year}", year}, {"{sequence}", v.sequenceValue()}, {"{index}", v.indexValue()}}
}

// dropUnknownYear removes query parameters whose whole value is {year} when
// the year is unknown, so the source sees no filter rather than an empty one.
func dropUnknownYear(query string, vars Variables) string {
	if vars.Year > 0 || !strings.Contains(query, "{year}") {
		return query
	}
	params := strings.Split(query, "&")
	kept := params[:0]
	for _, param := range params {
		if _, value, _ := strings.Cut(param, "="); value == "{year}" {
			continue
		}
		kept = append(kept, param)
	}
	return strings.Join(kept, "&")
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
	if spec.Kind == "rss" {
		return e.lookupRSS(ctx, spec, id, v, config)
	}
	return e.lookupDocument(ctx, spec, spec.LookupTemplate, id, v, config)
}

// LookupByID executes spec's LookupByIDTemplate for provider (an external id
// namespace the source can resolve) with {id} replaced by id, and maps the
// document exactly like Lookup. When the scraper declares an id-lookup record
// path for provider, the document only resolves the scraper's own record id and the
// result is the LookupTemplate lookup of that id, so an external id yields
// the same full record as a search match. A provider the scraper does not
// declare is an error; rss scrapers have no id lookups.
func (e *Engine) LookupByID(ctx context.Context, spec plugin.ScraperSpec, provider, id string, vars ...Variables) (Result, error) {
	template := spec.LookupByIDTemplate[provider]
	if template == "" {
		return Result{}, fmt.Errorf("scrape: scraper %q declares no %q id lookup", spec.ID, provider)
	}
	if spec.Kind == "rss" {
		return Result{}, fmt.Errorf("scrape: scraper %q: rss scrapers have no id lookups", spec.ID)
	}
	v := variablesArg(vars)
	config, err := e.configValues(ctx, v)
	if err != nil {
		return Result{}, err
	}
	recordIDPath := e.extensions(spec).LookupByIDRecordIDPaths[provider]
	if recordIDPath == "" {
		return e.lookupDocument(ctx, spec, template, id, v, config)
	}
	if spec.Kind != "http-json" || spec.LookupTemplate == "" {
		return Result{}, fmt.Errorf("scrape: scraper %q: an id-lookup record path needs an http-json LookupTemplate", spec.ID)
	}
	root, err := e.fetchJSONDocument(ctx, spec, template, "{id}", id, v, config, 0)
	if err != nil {
		return Result{}, err
	}
	raw, ok := resolveAlternatives(recordIDPath, func(path string) (any, bool) { return resolveJSONScalar(root, path) })
	recordID, _ := jsonScalar(raw)
	if !ok || recordID == "" {
		return Result{}, fmt.Errorf("scrape: scraper %q: %q id %q resolved to no record", spec.ID, provider, id)
	}
	return e.lookupDocument(ctx, spec, spec.LookupTemplate, recordID, v, config)
}

// lookupDocument fetches one http-json or http-graphql document from template
// and maps it through FieldMap, ArtworkMap and the entity maps.
func (e *Engine) lookupDocument(ctx context.Context, spec plugin.ScraperSpec, template, id string, v Variables, config map[string]string) (Result, error) {
	var root any
	var err error
	switch spec.Kind {
	case "http-json":
		root, err = e.fetchJSONDocument(ctx, spec, template, "{id}", id, v, config, 0)
	case "http-graphql":
		root, err = e.fetchGraphQLData(ctx, spec, template, "{id}", id, v, config, 0)
	default:
		return Result{}, fmt.Errorf("scrape: scraper %q has unknown kind %q", spec.ID, spec.Kind)
	}
	if err != nil {
		return Result{}, err
	}
	fieldMap, artworkMap := e.referenceMaps(ctx, spec, config, spec.FieldMap), e.referenceMaps(ctx, spec, config, spec.ArtworkMap)
	result := mapJSON(spec, fieldMap, artworkMap, root, config)
	result.Entities, err = e.resolveEntityMaps(ctx, spec, root, v, config)
	if err != nil {
		return Result{}, err
	}
	linkContainerResults(result.Entities)
	if template == spec.LookupTemplate {
		if err := e.overlayItemLookup(ctx, spec, id, v, config, &result); err != nil {
			return Result{}, err
		}
	}
	return result, nil
}

// overlayItemLookup fetches the scraper's per-item document for the record id
// and the item's ordinal and lets its ItemFieldMap and ItemArtworkMap values
// override the record's. A template naming an ordinal the item does not have
// skips the fetch, and a failed fetch keeps the record's mapping: the item
// document refines an identification, it never fails one. Only a cancelled
// context is returned.
func (e *Engine) overlayItemLookup(ctx context.Context, spec plugin.ScraperSpec, id string, v Variables, config map[string]string, result *Result) error {
	if spec.ItemLookupTemplate == "" || spec.Kind != "http-json" || v.namesUnknownOrdinal(spec.ItemLookupTemplate) {
		return nil
	}
	root, err := e.fetchJSONDocument(ctx, spec, spec.ItemLookupTemplate, "{id}", id, v, config, 0)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		slog.Debug("scrape: item lookup failed, keeping the record mapping", "scraper", spec.ID, "error", err.Error())
		return nil
	}
	item := mapJSON(spec, e.referenceMaps(ctx, spec, config, spec.ItemFieldMap), e.referenceMaps(ctx, spec, config, spec.ItemArtworkMap), root, config)
	for field, value := range item.Fields {
		result.Fields[field] = value
	}
	for slot, value := range item.Artwork {
		result.Artwork[slot] = value
	}
	return nil
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
	fieldMap, artworkMap = e.referenceMaps(ctx, spec, config, fieldMap), e.referenceMaps(ctx, spec, config, artworkMap)
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
				if raw, ok := resolveAlternatives(substituteConfig(spec.CandidateIDPath, config, identity), func(path string) (any, bool) {
					return resolveJSONScalar(element, path)
				}); ok {
					result.ID, _ = scalarString(raw)
				}
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
	query = dropUnknownYear(query, vars)
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
		raw, ok := resolveAlternatives(path, resolve)
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
		if raw, ok := resolveAlternatives(path, resolve); ok {
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
		if vars.namesUnknownOrdinal(entityMap.Path) {
			continue
		}
		node, ok := resolveJSONNode(root, vars.substituteOrdinals(substituteConfig(entityMap.Path, config, identity)))
		if !ok {
			continue
		}
		if entityMap.Container {
			if entity, ok := containerEntityResult(spec, entityMap, node, config); ok {
				entities = append(entities, entity)
			}
			continue
		}
		elements, isList := node.([]any)
		if !isList {
			elements = []any{node}
		}
		lookups := 0
		for i, element := range elements {
			if !elementMatchesFilter(element, entityMap.ElementFilter, config) {
				continue
			}
			source := element
			// Past MaxLookups the element itself is the source, as if the
			// map declared no LookupTemplate (it needs no lookup ID then):
			// the cap bounds requests, never which elements hydrate.
			if entityMap.LookupTemplate != "" && (entityMap.MaxLookups <= 0 || lookups < entityMap.MaxLookups) {
				id, ok := resolveJSONPath(element, substituteConfig(entityMap.LookupIDPath, config, identity))
				if !ok || strings.TrimSpace(id) == "" {
					continue
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
			for attribute, path := range entityMap.RelationAttributeMap {
				value, ok := resolveJSONPath(element, substituteConfig(path, config, identity))
				if value = strings.TrimSpace(value); !ok || value == "" {
					continue
				}
				if entity.RelationAttributes == nil {
					entity.RelationAttributes = map[string]string{}
				}
				entity.RelationAttributes[attribute] = value
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
			entity.SortOrder = entitySortOrder(entityMap, entity, i, isList)
			entities = append(entities, entity)
		}
	}
	return entities, nil
}

// elementMatchesFilter reports whether one EntityMap element passes the map's
// ElementFilter: every filter path, resolved against the element, must equal
// one of its accepted values. An empty filter keeps every element.
func elementMatchesFilter(element any, filter map[string][]string, config map[string]string) bool {
	for path, accepted := range filter {
		value, ok := resolveJSONPath(element, substituteConfig(path, config, identity))
		if !ok || !slices.Contains(accepted, strings.TrimSpace(value)) {
			return false
		}
	}
	return true
}

// entitySortOrder resolves a hydrated Entity's relation sort position. When
// the map declares an OrdinalField whose FieldMap or RelationAttributeMap value
// parses as an integer, that value is the position (a plugin-supplied
// installment or billing number); otherwise an array element's index is. The
// map's OrdinalOffset is added either way. A scalar node with no usable
// ordinal stays unordered (nil) — the FieldMap "omit, never error" posture.
func entitySortOrder(entityMap plugin.EntityMap, entity EntityResult, index int, isList bool) *int {
	position, ok := index, isList
	if entityMap.OrdinalField != "" {
		raw, present := entity.Fields[entityMap.OrdinalField]
		if !present {
			raw, present = entity.RelationAttributes[entityMap.OrdinalField]
		}
		if ordinal, err := strconv.Atoi(strings.TrimSpace(raw)); present && err == nil {
			position, ok = ordinal, true
		}
	}
	if !ok {
		return nil
	}
	position += entityMap.OrdinalOffset
	return &position
}

// containerEntityResult maps a container map's node (the first element when it
// resolved a list) to its artwork and raw fields. A node that yields neither is
// no result.
func containerEntityResult(spec plugin.ScraperSpec, entityMap plugin.EntityMap, node any, config map[string]string) (EntityResult, bool) {
	if elements, isList := node.([]any); isList {
		if len(elements) == 0 {
			return EntityResult{}, false
		}
		node = elements[0]
	}
	entity := EntityResult{Kind: entityMap.Kind, Container: true, containerRelations: entityMap.ContainerRelations}
	if entityMap.ArtworkPath != "" {
		if value, ok := resolveJSONPath(node, substituteConfig(entityMap.ArtworkPath, config, identity)); ok && value != "" {
			entity.ArtworkURL = renderArtworkURL(spec.ArtworkBaseURL, entityMap.ArtworkURLTemplate, value)
		}
	}
	for slot, path := range entityMap.ArtworkMap {
		value, ok := resolveJSONPath(node, substituteConfig(path, config, identity))
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		if entity.Artwork == nil {
			entity.Artwork = map[string]string{}
		}
		entity.Artwork[slot] = renderArtworkURL(spec.ArtworkBaseURL, spec.ArtworkURLTemplates[slot], value)
	}
	if entityMap.TitlePath != "" {
		if value, ok := resolveJSONPath(node, substituteConfig(entityMap.TitlePath, config, identity)); ok {
			entity.Title = strings.TrimSpace(value)
		}
	}
	for field, path := range entityMap.FieldMap {
		value, ok := resolveJSONPath(node, substituteConfig(path, config, identity))
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		if entity.Fields == nil {
			entity.Fields = map[string]string{}
		}
		entity.Fields[field] = value
	}
	return entity, entity.ArtworkURL != "" || len(entity.Fields) > 0 || len(entity.Artwork) > 0 || entity.Title != "" || len(entityMap.ContainerRelations) > 0
}

// linkContainerResults fills each container result's Links from its map's
// ContainerRelations: every non-container result in the same lookup whose
// relation is the one a container relation names.
func linkContainerResults(entities []EntityResult) {
	for i := range entities {
		container := &entities[i]
		if len(container.containerRelations) > 0 {
			container.LinkRelations = slices.Sorted(maps.Keys(container.containerRelations))
		}
		for _, relation := range container.LinkRelations {
			sibling := container.containerRelations[relation]
			for _, hydrated := range entities {
				if hydrated.Container || hydrated.Relation != sibling {
					continue
				}
				container.Links = append(container.Links, EntityLink{Relation: relation, Kind: hydrated.Kind, Name: hydrated.Name, SortOrder: hydrated.SortOrder})
			}
		}
	}
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
// multiple scraper endpoints, and a login can send several refs (the AuthRef
// plus its SecretFields), so delete every token whose exchange used ref.
func (e *Engine) InvalidateCredential(ref string) {
	if ref == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for key, cached := range e.tokens {
		if slices.Contains(cached.refs, ref) {
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
	secret, err := resolveCredential(e.credentials, spec.AuthRef, "")
	if err != nil {
		return nil, err
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
// {SecretField: secret} plus one key per Login.SecretFields entry as JSON to
// Endpoint+Login.Path, read the token at Login.TokenPath. Tokens are cached
// per endpoint+AuthRef for loginTokenTTL.
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
	fields := map[string]string{spec.Login.SecretField: secret}
	refs := []string{spec.AuthRef}
	// SecretFields adds further body keys, each resolved like the scraper's own
	// AuthRef; a missing one fails the exchange rather than logging in without it.
	for field, ref := range spec.Login.SecretFields {
		extra, err := resolveCredential(e.credentials, ref, fmt.Sprintf("scraper %q login field %q: ", spec.ID, field))
		if err != nil {
			return "", err
		}
		fields[field] = extra
		refs = append(refs, ref)
	}
	payload, err := json.Marshal(fields)
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
	response, err := e.fetch(ctx, http.MethodPost, spec.Endpoint+spec.Login.Path, bytes.NewReader(payload), "application/json", nil)
	if err != nil {
		return "", err
	}
	root, err := decodeJSON(response)
	if err != nil {
		return "", fmt.Errorf("scrape: scraper %q login: %w", spec.ID, err)
	}
	token, ok := resolveJSONPath(root, spec.Login.TokenPath)
	if !ok || token == "" {
		return "", fmt.Errorf("scrape: scraper %q login response has no token at %q", spec.ID, spec.Login.TokenPath)
	}
	e.mu.Lock()
	e.tokens[key] = cachedToken{token: token, expires: e.clock.Now().Add(loginTokenTTL), refs: refs}
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
