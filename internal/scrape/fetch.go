package scrape

// Ingest-facing additions to the hardened engine: conditional
// feed fetches and streamed resource downloads. Both run through the same
// ADR-0012 §9 SSRF gate as every scraper request — https only, no literal
// IPs, no private ranges, and origin checks on redirects.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ConditionalResult is one conditional-GET response: either NotModified (the
// validators still hold; Data is nil) or the full body plus the validators to
// send next time.
type ConditionalResult struct {
	Data         []byte
	ETag         string
	LastModified string
	NotModified  bool
}

func (e *Engine) FetchDocument(ctx context.Context, rawURL string, headers map[string]string) ([]byte, error) {
	data, _, err := e.fetchDocument(ctx, rawURL, headers, e.maxResponseBytes, func(req *http.Request) (*http.Response, error) {
		return e.requestWithRetry(ctx, req, e.client.Do)
	})
	return data, err
}

// ErrDocumentThrottled reports a document request refused by the upstream.
var ErrDocumentThrottled = errors.New("scrape: upstream throttled document request")

var documentQueryKeySeparators = strings.NewReplacer("-", "", "_", "", ".", "")

// CheckDocumentURL applies the polling pre-dial rules and rejects credentials
// in public directory and discovery URLs. The dialer checks addresses on fetch.
func (e *Engine) CheckDocumentURL(u *url.URL) error {
	if u.User != nil {
		return errors.New("scrape: refused URL with user credentials")
	}
	params, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return errors.New("scrape: refused invalid URL query")
	}
	for key := range params {
		switch documentQueryKeySeparators.Replace(strings.ToLower(key)) {
		case "token", "accesstoken", "refreshtoken", "authtoken", "bearertoken", "idtoken", "apitoken", "oauthtoken",
			"securitytoken", "sessiontoken", "jwt", "auth", "authorization", "credential", "credentials",
			"apikey", "key", "accesskey", "accesskeyid", "subscriptionkey", "secret", "clientsecret", "oauthtokensecret",
			"password", "passwd", "sessionid", "sig", "signature", "xamzcredential", "xamzsecuritytoken", "xamzsignature",
			"xgoogcredential", "xgoogsignature":
			return errors.New("scrape: refused URL with query credentials")
		}
	}
	return e.vetURL(u)
}

// FetchDocumentLimited fetches a capped document and returns its final URL.
// Each request, including allowed redirect hops, needs admission. An upstream
// 429 returns immediately, leaving retries to the caller's request budget.
func (e *Engine) FetchDocumentLimited(ctx context.Context, rawURL string, headers map[string]string, maxBytes int64, admit func() error) ([]byte, *url.URL, error) {
	client := *e.client
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := e.checkRedirect(req, via); err != nil {
			return err
		}
		if err := e.CheckDocumentURL(req.URL); err != nil {
			return err
		}
		return admit()
	}
	data, finalURL, err := e.fetchDocument(ctx, rawURL, headers, min(e.maxResponseBytes, maxBytes), func(req *http.Request) (*http.Response, error) {
		if err := e.CheckDocumentURL(req.URL); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := admit(); err != nil {
			return nil, err
		}
		// The shared agent takes precedence over a caller-supplied header.
		req.Header.Set("User-Agent", e.userAgent)
		return client.Do(req) // #nosec G704 -- URL checked above, redirects pinned, and transport vets the resolved IP.
	})
	var status *statusError
	if errors.As(err, &status) && status.code == http.StatusTooManyRequests {
		return nil, nil, ErrDocumentThrottled
	}
	return data, finalURL, err
}

func (e *Engine) fetchDocument(ctx context.Context, rawURL string, headers map[string]string, maxBytes int64, do func(*http.Request) (*http.Response, error)) ([]byte, *url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, fmt.Errorf("scrape: invalid request URL")
	}
	if err := e.vetURL(parsed); err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, nil, safeFetchError("building request", err)
	}
	req.Header.Set("User-Agent", e.userAgent)
	for name, value := range headers {
		if strings.TrimSpace(value) != "" {
			req.Header.Set(name, value)
		}
	}
	resp, err := do(req)
	if err != nil {
		return nil, nil, sanitizedTransportError(parsed, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, nil, &statusError{host: parsed.Hostname(), code: resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, nil, safeFetchError("reading response", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, nil, fmt.Errorf("scrape: response from %s exceeds the %d-byte cap", parsed.Hostname(), maxBytes)
	}
	finalURL := parsed
	if resp.Request != nil {
		finalURL = resp.Request.URL
	}
	return data, finalURL, nil
}

// FetchConditional GETs one resource with If-None-Match/If-Modified-Since
// validators (either may be ""). A 304 returns NotModified with the caller's
// validators echoed back; a 2xx returns the capped body and the response's
// validators. The standard response cap applies — feeds are documents, not
// media bytes.
func (e *Engine) FetchConditional(ctx context.Context, rawURL, etag, lastModified string) (ConditionalResult, error) {
	return e.FetchConditionalAuth(ctx, rawURL, etag, lastModified, "")
}

func (e *Engine) FetchConditionalAuth(ctx context.Context, rawURL, etag, lastModified string, authHeader string) (ConditionalResult, error) {
	result, err := e.FetchFeed(ctx, rawURL, etag, lastModified, authHeader)
	return result.ConditionalResult, err
}

// EnclosureDownloadLimit is the per-enclosure archive budget. Declared
// lengths are optional and untrusted.
const EnclosureDownloadLimit int64 = 2 << 30

// DownloadResource streams one resource into dst through the vetted client,
// refusing bodies larger than maxBytes (maxBytes <= 0 means no cap; callers
// own their budget — media downloads are far past the scraper document cap).
// Unlike scraper fetches, the body read has no wall-clock timeout: large
// downloads take as long as they take, bounded by ctx.
func (e *Engine) DownloadResource(ctx context.Context, rawURL string, dst io.Writer, maxBytes int64) (int64, error) {
	return e.DownloadResourceAuth(ctx, rawURL, dst, maxBytes, "")
}

func (e *Engine) DownloadResourceAuth(ctx context.Context, rawURL string, dst io.Writer, maxBytes int64, authHeader string) (int64, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		// Never wrap the parse error: it embeds the raw URL.
		return 0, fmt.Errorf("scrape: invalid request URL")
	}
	if err := e.vetURL(parsed); err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return 0, safeFetchError("building request", err)
	}
	req.Header.Set("User-Agent", e.userAgent)
	if strings.TrimSpace(authHeader) != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := e.requestWithRetry(ctx, req, func(attempt *http.Request) (*http.Response, error) {
		return e.streamClient.Do(attempt)
	})
	if err != nil {
		return 0, sanitizedTransportError(parsed, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return 0, &statusError{host: parsed.Hostname(), code: resp.StatusCode}
	}
	body := resp.Body
	reader := io.Reader(body)
	if maxBytes > 0 {
		reader = io.LimitReader(body, maxBytes+1)
	}
	written, err := io.Copy(dst, reader)
	if err != nil {
		return written, safeFetchError("streaming response", err)
	}
	if maxBytes > 0 && written > maxBytes {
		return written, fmt.Errorf("scrape: response from %s exceeds the %d-byte download budget", parsed.Hostname(), maxBytes)
	}
	return written, nil
}

// OriginResponse is a live, vetted origin fetch the proxy-tee streams to a
// client while teeing to disk. Body is the open response body —
// the caller MUST Close it. StatusCode is the origin's status (206 for a
// satisfied range, 200 for a full body); ContentType/ContentRange/AcceptRanges
// are echoed for the proxy to forward, and TotalSize is the full resource size
// when the origin declared it (Content-Range total, else Content-Length on a
// 200), 0 when unknown.
type OriginResponse struct {
	Body         io.ReadCloser
	StatusCode   int
	ContentType  string
	ContentRange string
	AcceptRanges string
	TotalSize    int64
}

// OpenOriginRange opens rawURL for streaming through the SAME ADR-0012 §9 SSRF
// gate as every other server-side fetch (vetURL: https only, no literal IPs, no
// private ranges; the dialer's controlDial re-checks the resolved IP for
// DNS-rebinding; checkStreamRedirect re-vets every hop). It forwards the client's Range header
// to the origin so the proxy can honour client seeks, and returns the open body
// for the caller to copy + tee. The origin URL never leaves the server — only
// the streamed bytes do. The caller owns Body and must Close it.
func (e *Engine) OpenOriginRange(ctx context.Context, rawURL, rangeHeader string) (OriginResponse, error) {
	return e.OpenOriginRangeAuth(ctx, rawURL, rangeHeader, "")
}

// OpenOriginRangeAuth opens rawURL like OpenOriginRange and sends authHeader
// as the upstream Authorization header when non-empty.
func (e *Engine) OpenOriginRangeAuth(ctx context.Context, rawURL, rangeHeader, authHeader string) (OriginResponse, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		// Never wrap the parse error: it embeds the raw URL, which can carry
		// private-feed credentials (invariant #5/#7).
		return OriginResponse{}, fmt.Errorf("scrape: invalid request URL")
	}
	if err := e.vetURL(parsed); err != nil {
		return OriginResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return OriginResponse{}, safeFetchError("building request", err)
	}
	req.Header.Set("User-Agent", e.userAgent)
	if strings.TrimSpace(rangeHeader) != "" {
		req.Header.Set("Range", rangeHeader)
	}
	if strings.TrimSpace(authHeader) != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := e.streamClient.Do(req)
	if err != nil {
		return OriginResponse{}, sanitizedTransportError(parsed, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		resp.Body.Close()
		return OriginResponse{}, &statusError{host: parsed.Hostname(), code: resp.StatusCode}
	}
	return OriginResponse{
		Body:         resp.Body,
		StatusCode:   resp.StatusCode,
		ContentType:  resp.Header.Get("Content-Type"),
		ContentRange: resp.Header.Get("Content-Range"),
		AcceptRanges: resp.Header.Get("Accept-Ranges"),
		TotalSize:    originTotalSize(resp),
	}, nil
}

// originTotalSize reports the full resource size: the total from a 206's
// Content-Range ("bytes a-b/total"), else a 200's Content-Length, else 0
// (unknown — the origin used chunked/streamed encoding).
func originTotalSize(resp *http.Response) int64 {
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		if i := strings.LastIndex(cr, "/"); i >= 0 {
			if total, err := strconv.ParseInt(strings.TrimSpace(cr[i+1:]), 10, 64); err == nil {
				return total
			}
		}
	}
	if resp.StatusCode == http.StatusOK && resp.ContentLength > 0 {
		return resp.ContentLength
	}
	return 0
}

// sanitizedTransportError keeps status and refusal diagnostics and never
// formats transport text. net/http can include a malformed Location in the
// underlying error, even after the outer *url.Error is stripped.
func sanitizedTransportError(parsed *url.URL, err error) error {
	for {
		urlErr, ok := err.(*url.Error)
		if !ok {
			break
		}
		err = urlErr.Err
	}
	var refused *refusedError
	var status *statusError
	switch {
	case errors.As(err, &refused):
		return refused
	case errors.As(err, &status):
		return status
	default:
		return safeFetchError("request to "+parsed.Hostname(), err)
	}
}

// fetchError retains typed causes for classification without printing them.
type fetchError struct {
	operation string
	cause     error
}

func (e *fetchError) Error() string        { return "scrape: " + e.operation + " failed" }
func (e *fetchError) Is(target error) bool { return errors.Is(e.cause, target) }
func (e *fetchError) As(target any) bool {
	if _, isURL := target.(**url.Error); isURL {
		return false
	}
	return errors.As(e.cause, target)
}

func safeFetchError(operation string, cause error) error {
	return &fetchError{operation: operation, cause: cause}
}

// FeedEntry is one feed item relevant to ingest: its identity GUID, display
// title, and the enclosure to download.
type FeedEntry struct {
	GUID  string
	Title string
	// DerivedGUID is true when the feed omitted an explicit GUID or Atom ID.
	DerivedGUID bool
	// EnclosureURL is the media payload URL ("" for entries with no
	// enclosure). Never an identity key — ad insertion rewrites it freely.
	EnclosureURL string
	// EnclosureType is the declared MIME type ("" when the feed omits it).
	EnclosureType string
	// EnclosureLength is the declared byte length (0 when absent or invalid).
	EnclosureLength int64
	// Description is the entry summary ("" when absent).
	Description string
	// PublishedAt is the parsed publication time (zero when absent or
	// unparseable).
	PublishedAt time.Time
	// DurationSeconds is itunes:duration as seconds (0 when absent).
	DurationSeconds int64
	// ArtworkURL is the entry's own itunes:image ("" when absent).
	ArtworkURL string
}

// FeedDocument is a parsed feed: channel metadata plus its entries in
// document order.
type FeedDocument struct {
	Title string
	// Serial is true when the channel declares itunes:type "serial": its
	// entries are meant to be consumed in order from the first.
	Serial bool
	// NewFeedURL is the channel's declared new location (a direct child named
	// new-feed-url or newFeedUrl in any namespace: the iTunes and Index
	// extensions), "" when absent. Secret class: never logged.
	NewFeedURL string
	// Description is the channel summary, Link its website and Author its
	// itunes:author ("" when absent).
	Description string
	Link        string
	Author      string
	// ArtworkURL is the channel's itunes:image, else its RSS image url
	// ("" when absent).
	ArtworkURL string
	Entries    []FeedEntry
}

// ParseFeedEntries extracts every entry's GUID, title, and enclosure from an
// RSS or Atom document, in document order. Entries without an explicit
// identity need an enclosure and derive their GUID from the item link or
// publication metadata (ADR-0030 §3).
func ParseFeedEntries(data []byte) ([]FeedEntry, error) {
	document, err := ParseFeed(data)
	if err != nil {
		return nil, err
	}
	return document.Entries, nil
}

// ParseFeed parses an RSS or Atom document into its channel metadata and
// entries (see ParseFeedEntries for entry identity).
func ParseFeed(data []byte) (FeedDocument, error) {
	root, err := parseXML(data)
	if err != nil || len(root.children) != 1 {
		return FeedDocument{}, errors.New("scrape: invalid feed document")
	}
	name := root.children[0].name
	switch name[strings.LastIndexByte(name, ':')+1:] {
	case "rss", "feed", "RDF":
	default:
		return FeedDocument{}, errors.New("scrape: unsupported feed root element")
	}
	var document FeedDocument
	channel := root
	if channels := findElements([]*xmlNode{root}, "channel"); len(channels) > 0 {
		channel = channels[0]
	} else if feeds := findElements([]*xmlNode{root}, "feed"); len(feeds) > 0 {
		channel = feeds[0]
	}
	document.Title = directChildText(channel, "title")
	document.Description = directChildText(channel, "description")
	if document.Description == "" {
		document.Description = directChildText(channel, "itunes:summary")
	}
	document.Link = directChildText(channel, "link")
	document.Author = directChildText(channel, "itunes:author")
	document.ArtworkURL = channelArtworkURL(channel)
	document.Serial = strings.EqualFold(directChildText(channel, "itunes:type"), "serial")
	for _, child := range channel.children {
		local := child.name[strings.LastIndexByte(child.name, ':')+1:]
		if local == "new-feed-url" || local == "newFeedUrl" {
			if document.NewFeedURL = strings.TrimSpace(child.text); document.NewFeedURL != "" {
				break
			}
		}
	}
	if items := findElements([]*xmlNode{root}, "item"); len(items) > 0 {
		document.Entries = rssEntries(items)
		return document, nil
	}
	// No <item> elements: either an Atom feed or an empty RSS one — Atom's
	// <entry> lookup returns the empty list for both.
	document.Entries = atomEntries(findElements([]*xmlNode{root}, "entry"))
	return document, nil
}

// channelArtworkURL is the channel's itunes:image href, else its RSS
// <image><url>.
func channelArtworkURL(channel *xmlNode) string {
	if href := directChildAttr(channel, "itunes:image", "href"); href != "" {
		return href
	}
	for _, child := range channel.children {
		if child.name == "image" {
			if u := directChildText(child, "url"); u != "" {
				return u
			}
		}
	}
	return ""
}

func directChildAttr(scope *xmlNode, name, attr string) string {
	for _, child := range scope.children {
		if child.name == name {
			if value := strings.TrimSpace(child.attrs[attr]); value != "" {
				return value
			}
		}
	}
	return ""
}

// directChildText is the trimmed text of scope's first direct child named
// name, so channel metadata never picks up an entry's element.
func directChildText(scope *xmlNode, name string) string {
	for _, child := range scope.children {
		if child.name == name {
			return strings.TrimSpace(child.text)
		}
	}
	return ""
}

func parseFeedTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range feedPublicationLayouts {
		if stamp, err := time.Parse(layout, value); err == nil {
			return stamp.UTC()
		}
	}
	return time.Time{}
}

func parseFeedDuration(value string) int64 {
	seconds, ok := secondsFromClock(value)
	if !ok {
		return 0
	}
	n, _ := strconv.ParseInt(seconds, 10, 64)
	return n
}

func parseEnclosureLength(value string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func rssEntries(items []*xmlNode) []FeedEntry {
	entries := make([]FeedEntry, 0, len(items))
	for _, item := range items {
		entry := FeedEntry{
			GUID:            childText(item, "guid"),
			Title:           childText(item, "title"),
			Description:     childText(item, "description"),
			PublishedAt:     parseFeedTime(childText(item, "pubDate")),
			DurationSeconds: parseFeedDuration(childText(item, "itunes:duration")),
			ArtworkURL:      directChildAttr(item, "itunes:image", "href"),
		}
		if entry.Description == "" {
			entry.Description = childText(item, "itunes:summary")
		}
		if enclosures := findElements([]*xmlNode{item}, "enclosure"); len(enclosures) > 0 {
			entry.EnclosureURL = enclosures[0].attrs["url"]
			entry.EnclosureType = enclosures[0].attrs["type"]
			entry.EnclosureLength = parseEnclosureLength(enclosures[0].attrs["length"])
		}
		if entry.GUID == "" && entry.EnclosureURL != "" {
			entry.deriveGUID(childText(item, "link"), childText(item, "pubDate"))
		}
		if entry.GUID == "" {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

// atomEntries maps Atom's vocabulary onto the same FeedEntry shape: <id> is
// the identity, and the enclosure is the first <link rel="enclosure">.
func atomEntries(items []*xmlNode) []FeedEntry {
	entries := make([]FeedEntry, 0, len(items))
	for _, item := range items {
		entry := FeedEntry{
			GUID:            childText(item, "id"),
			Title:           childText(item, "title"),
			Description:     childText(item, "summary"),
			PublishedAt:     parseFeedTime(childText(item, "published")),
			DurationSeconds: parseFeedDuration(childText(item, "itunes:duration")),
			ArtworkURL:      directChildAttr(item, "itunes:image", "href"),
		}
		if entry.PublishedAt.IsZero() {
			entry.PublishedAt = parseFeedTime(childText(item, "updated"))
		}
		for _, link := range findElements([]*xmlNode{item}, "link") {
			if link.attrs["rel"] == "enclosure" {
				entry.EnclosureURL = link.attrs["href"]
				entry.EnclosureType = link.attrs["type"]
				entry.EnclosureLength = parseEnclosureLength(link.attrs["length"])
				break
			}
		}
		if entry.GUID == "" && entry.EnclosureURL != "" {
			entry.deriveGUID(atomEntryLink(item), childText(item, "published"))
		}
		if entry.GUID == "" {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

func atomEntryLink(item *xmlNode) string {
	for _, link := range item.children {
		rel := link.attrs["rel"]
		if link.name == "link" && (rel == "" || rel == "alternate") {
			if href := strings.TrimSpace(link.attrs["href"]); href != "" {
				return href
			}
		}
	}
	return ""
}

var feedPublicationLayouts = append([]string{time.RFC3339Nano}, rfc822Layouts...)

func (entry *FeedEntry) deriveGUID(link, published string) {
	key := "link\x00" + link
	if link == "" {
		title := strings.ToLower(strings.Join(strings.Fields(entry.Title), " "))
		if title == "" && published == "" {
			return
		}
		for _, layout := range feedPublicationLayouts {
			if stamp, err := time.ParseInLocation(layout, published, time.UTC); err == nil {
				published = stamp.UTC().Format(time.RFC3339Nano)
				break
			}
		}
		key = "title-date\x00" + title + "\x00" + published
	}
	// Hash links as well as metadata so URL credentials stay out of receipts.
	entry.GUID = fmt.Sprintf("urn:beamfall:feed-entry:%x", sha256.Sum256([]byte(key)))
	entry.DerivedGUID = true
}

func childText(scope *xmlNode, name string) string {
	matched := findElements([]*xmlNode{scope}, name)
	if len(matched) == 0 {
		return ""
	}
	return strings.TrimSpace(matched[0].text)
}
