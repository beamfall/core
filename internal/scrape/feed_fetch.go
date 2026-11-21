package scrape

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// FeedResult is one Subscription feed fetch. PermanentURL is set only when
// the fetch was redirected and every hop was permanent (301 or 308): it is
// the URL the origin says the feed now lives at. A temporary hop (302, 303,
// 307) is followed but never reported, so the Subscription keeps its URL.
type FeedResult struct {
	ConditionalResult
	PermanentURL string
}

// FetchFeed fetches a Subscription feed with conditional-GET validators and
// follows its redirects by hand so the caller can tell a permanent move from
// a temporary one (CORE-2955). Every hop is re-vetted against the SSRF rules
// and dialled through the same guarded transport. A hop may change origin,
// because feeds move between hosts, but the Authorization header is sent to
// the feed's original origin only, so a redirect never carries the private
// feed credential elsewhere.
func (e *Engine) FetchFeed(ctx context.Context, rawURL, etag, lastModified, authHeader string) (FeedResult, error) {
	current, err := url.Parse(rawURL)
	if err != nil {
		// Never wrap the parse error: it embeds the raw URL.
		return FeedResult{}, fmt.Errorf("scrape: invalid request URL")
	}
	origin := normalizedRedirectOrigin(current)
	client := *e.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	permanent := true
	for hop := 0; ; hop++ {
		if err := e.vetURL(current); err != nil {
			return FeedResult{}, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current.String(), nil)
		if err != nil {
			return FeedResult{}, fmt.Errorf("scrape: building request: %w", err)
		}
		req.Header.Set("User-Agent", userAgent)
		sameOrigin := normalizedRedirectOrigin(current) == origin
		if sameOrigin && strings.TrimSpace(authHeader) != "" {
			req.Header.Set("Authorization", authHeader)
		}
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		if lastModified != "" {
			req.Header.Set("If-Modified-Since", lastModified)
		}
		resp, err := e.requestWith429Retry(ctx, req, client.Do)
		if err != nil {
			return FeedResult{}, sanitizedTransportError(current, err)
		}
		next, isRedirect := redirectTarget(current, resp)
		if isRedirect {
			_ = resp.Body.Close()
			if next == nil {
				return FeedResult{}, &statusError{host: current.Hostname(), code: resp.StatusCode}
			}
			if hop >= maxRedirects {
				return FeedResult{}, fmt.Errorf("scrape: refused after %d redirects", maxRedirects)
			}
			if resp.StatusCode != http.StatusMovedPermanently && resp.StatusCode != http.StatusPermanentRedirect {
				permanent = false
			}
			current = next
			continue
		}
		result, err := e.readFeedResponse(current, resp, etag, lastModified)
		_ = resp.Body.Close()
		if err != nil {
			return FeedResult{}, err
		}
		if hop > 0 && permanent {
			result.PermanentURL = current.String()
		}
		return result, nil
	}
}

func redirectTarget(current *url.URL, resp *http.Response) (*url.URL, bool) {
	switch resp.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
	default:
		return nil, false
	}
	location := strings.TrimSpace(resp.Header.Get("Location"))
	if location == "" {
		return nil, true
	}
	next, err := current.Parse(location)
	if err != nil {
		return nil, true
	}
	next.Fragment = ""
	return next, true
}

func (e *Engine) readFeedResponse(current *url.URL, resp *http.Response, etag, lastModified string) (FeedResult, error) {
	if resp.StatusCode == http.StatusNotModified {
		return FeedResult{ConditionalResult: ConditionalResult{ETag: etag, LastModified: lastModified, NotModified: true}}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return FeedResult{}, &statusError{host: current.Hostname(), code: resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, e.maxResponseBytes+1))
	if err != nil {
		return FeedResult{}, sanitizedTransportError(current, err)
	}
	if int64(len(data)) > e.maxResponseBytes {
		return FeedResult{}, fmt.Errorf("%w: response from %s exceeds the %d-byte cap", ErrResponseTooLarge, current.Hostname(), e.maxResponseBytes)
	}
	return FeedResult{ConditionalResult: ConditionalResult{
		Data:         data,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}}, nil
}

// ErrResponseTooLarge marks a response body past the engine's size cap.
var ErrResponseTooLarge = errors.New("scrape: response exceeds the size cap")

// HTTPStatus reports the HTTP status of a non-success response error.
func HTTPStatus(err error) (int, bool) {
	var status *statusError
	if errors.As(err, &status) {
		return status.code, true
	}
	return 0, false
}

// IsRefused reports whether err is the engine's SSRF guard refusing a URL,
// a redirect or a dial.
func IsRefused(err error) bool {
	return err != nil && strings.Contains(err.Error(), "scrape: refused")
}

// PermitsURL reports whether the engine would fetch raw: an absolute URL that
// passes the SSRF URL rules. Dial-time address checks still apply on fetch.
func (e *Engine) PermitsURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && e.vetURL(parsed) == nil
}

// FeedNewURL returns the new feed URL a feed document declares for itself,
// or "". Feeds announce a move with a channel element whose local name is
// new-feed-url or newFeedUrl, whatever namespace prefix carries it.
func FeedNewURL(data []byte) string {
	root, err := parseXML(data)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(firstNewFeedURL(root))
}

func firstNewFeedURL(node *xmlNode) string {
	for _, child := range node.children {
		name := child.name
		if i := strings.LastIndex(name, ":"); i >= 0 {
			name = name[i+1:]
		}
		switch name {
		case "new-feed-url", "newFeedUrl":
			return child.text
		case "item", "entry":
			// Entries never relocate the feed.
			continue
		}
		if found := firstNewFeedURL(child); found != "" {
			return found
		}
	}
	return ""
}
