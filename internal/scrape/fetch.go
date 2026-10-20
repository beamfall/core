package scrape

// Ingest-facing additions to the hardened engine: conditional
// feed fetches and streamed resource downloads. Both run through the same
// ADR-0012 §9 SSRF gate as every scraper request — https only, no literal
// IPs, no private ranges, host-pinned redirects.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("scrape: invalid request URL")
	}
	if err := e.vetURL(parsed); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("scrape: building request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	for name, value := range headers {
		if strings.TrimSpace(value) != "" {
			req.Header.Set(name, value)
		}
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

// FetchConditional GETs one resource with If-None-Match/If-Modified-Since
// validators (either may be ""). A 304 returns NotModified with the caller's
// validators echoed back; a 2xx returns the capped body and the response's
// validators. The standard response cap applies — feeds are documents, not
// media bytes.
func (e *Engine) FetchConditional(ctx context.Context, rawURL, etag, lastModified string) (ConditionalResult, error) {
	return e.FetchConditionalAuth(ctx, rawURL, etag, lastModified, "")
}

func (e *Engine) FetchConditionalAuth(ctx context.Context, rawURL, etag, lastModified string, authHeader string) (ConditionalResult, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		// Never wrap the parse error: it embeds the raw URL, which can carry
		// private-feed credentials.
		return ConditionalResult{}, fmt.Errorf("scrape: invalid request URL")
	}
	if err := e.vetURL(parsed); err != nil {
		return ConditionalResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return ConditionalResult{}, fmt.Errorf("scrape: building request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	if strings.TrimSpace(authHeader) != "" {
		req.Header.Set("Authorization", authHeader)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}
	resp, err := e.requestWith429Retry(ctx, req, func(attempt *http.Request) (*http.Response, error) {
		return e.client.Do(attempt)
	})
	if err != nil {
		return ConditionalResult{}, sanitizedTransportError(parsed, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return ConditionalResult{ETag: etag, LastModified: lastModified, NotModified: true}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ConditionalResult{}, &statusError{host: parsed.Hostname(), code: resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, e.maxResponseBytes+1))
	if err != nil {
		return ConditionalResult{}, fmt.Errorf("scrape: reading response: %w", err)
	}
	if int64(len(data)) > e.maxResponseBytes {
		return ConditionalResult{}, fmt.Errorf("scrape: response from %s exceeds the %d-byte cap", parsed.Hostname(), e.maxResponseBytes)
	}
	return ConditionalResult{
		Data:         data,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}, nil
}

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
		return 0, fmt.Errorf("scrape: building request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	if strings.TrimSpace(authHeader) != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := e.requestWith429Retry(ctx, req, func(attempt *http.Request) (*http.Response, error) {
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
		return written, fmt.Errorf("scrape: streaming response from %s: %w", parsed.Hostname(), err)
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
// DNS-rebinding; checkRedirect host-pins). It forwards the client's Range header
// to the origin so the proxy can honour client seeks, and returns the open body
// for the caller to copy + tee. The origin URL never leaves the server — only
// the streamed bytes do. The caller owns Body and must Close it.
func (e *Engine) OpenOriginRange(ctx context.Context, rawURL, rangeHeader string) (OriginResponse, error) {
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
		return OriginResponse{}, fmt.Errorf("scrape: building request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	if strings.TrimSpace(rangeHeader) != "" {
		req.Header.Set("Range", rangeHeader)
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

// sanitizedTransportError strips the full request URL from client errors
// (*url.Error embeds it verbatim — query strings can carry credentials) and
// keeps only the host plus the underlying cause.
func sanitizedTransportError(parsed *url.URL, err error) error {
	cause := err
	if urlErr, ok := err.(*url.Error); ok {
		cause = urlErr.Err
	}
	return fmt.Errorf("scrape: request to %s failed: %w", parsed.Hostname(), cause)
}

// FeedEntry is one feed item relevant to ingest: its identity GUID, display
// title, and the enclosure to download.
type FeedEntry struct {
	GUID  string
	Title string
	// EnclosureURL is the media payload URL ("" for entries with no
	// enclosure). Never an identity key — ad insertion rewrites it freely.
	EnclosureURL string
	// EnclosureType is the declared MIME type ("" when the feed omits it).
	EnclosureType string
}

// ParseFeedEntries extracts every entry's GUID, title, and enclosure from an
// RSS or Atom document, in document order. RSS items without a <guid> (Atom
// entries without an <id>) fall back to the enclosure URL as a last-resort
// identity; entries with neither are skipped (nothing to download, nothing
// to key on).
func ParseFeedEntries(data []byte) ([]FeedEntry, error) {
	root, err := parseXML(data)
	if err != nil {
		return nil, fmt.Errorf("scrape: invalid feed document: %w", err)
	}
	if items := findElements([]*xmlNode{root}, "item"); len(items) > 0 {
		return rssEntries(items), nil
	}
	// No <item> elements: either an Atom feed or an empty RSS one — Atom's
	// <entry> lookup returns the empty list for both.
	return atomEntries(findElements([]*xmlNode{root}, "entry")), nil
}

func rssEntries(items []*xmlNode) []FeedEntry {
	entries := make([]FeedEntry, 0, len(items))
	for _, item := range items {
		entry := FeedEntry{
			GUID:  childText(item, "guid"),
			Title: childText(item, "title"),
		}
		if enclosures := findElements([]*xmlNode{item}, "enclosure"); len(enclosures) > 0 {
			entry.EnclosureURL = enclosures[0].attrs["url"]
			entry.EnclosureType = enclosures[0].attrs["type"]
		}
		if entry.GUID == "" {
			entry.GUID = entry.EnclosureURL
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
			GUID:  childText(item, "id"),
			Title: childText(item, "title"),
		}
		for _, link := range findElements([]*xmlNode{item}, "link") {
			if link.attrs["rel"] == "enclosure" {
				entry.EnclosureURL = link.attrs["href"]
				entry.EnclosureType = link.attrs["type"]
				break
			}
		}
		if entry.GUID == "" {
			entry.GUID = entry.EnclosureURL
		}
		if entry.GUID == "" {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

func childText(scope *xmlNode, name string) string {
	matched := findElements([]*xmlNode{scope}, name)
	if len(matched) == 0 {
		return ""
	}
	return strings.TrimSpace(matched[0].text)
}
