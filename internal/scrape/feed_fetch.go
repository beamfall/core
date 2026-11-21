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

// FeedResult is one Subscription feed fetch. PermanentURL is the destination
// of the last permanent hop (301 or 308) before any temporary redirect.
type FeedResult struct {
	ConditionalResult
	PermanentURL          string
	RedirectedTemporarily bool
}

// FetchFeed fetches a Subscription feed with conditional-GET validators and
// follows its redirects by hand so the caller can tell a permanent move from
// a temporary one (CORE-2955). Every hop is re-vetted against the SSRF rules
// and dialled through the same guarded transport. A hop may change origin,
// because public feeds move between hosts. A feed carrying Authorization or
// URL userinfo remains pinned to its original origin.
func (e *Engine) FetchFeed(ctx context.Context, rawURL, etag, lastModified, authHeader string) (FeedResult, error) {
	current, err := url.Parse(rawURL)
	if err != nil {
		// Never wrap the parse error: it embeds the raw URL.
		return FeedResult{}, refusedf("scrape: refused invalid URL")
	}
	origin := normalizedRedirectOrigin(current)
	credentialed := current.User != nil || strings.TrimSpace(authHeader) != ""
	client := *e.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var permanentURL string
	temporary := false
	for hop := 0; ; hop++ {
		if err := e.vetURL(current); err != nil {
			return FeedResult{}, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current.String(), nil)
		if err != nil {
			return FeedResult{}, safeFetchError("building request", err)
		}
		req.Header.Set("User-Agent", userAgent)
		sameOrigin := normalizedRedirectOrigin(current) == origin
		if sameOrigin && strings.TrimSpace(authHeader) != "" {
			req.Header.Set("Authorization", authHeader)
		}
		if sameOrigin && etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		if sameOrigin && lastModified != "" {
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
				return FeedResult{}, refusedf("scrape: refused after %d redirects", maxRedirects)
			}
			if resp.StatusCode != http.StatusMovedPermanently && resp.StatusCode != http.StatusPermanentRedirect {
				temporary = true
			} else if !temporary {
				permanentURL = next.String()
			}
			if (credentialed || next.User != nil) && normalizedRedirectOrigin(next) != origin {
				return FeedResult{}, refusedf("scrape: refused cross-origin redirect")
			}
			credentialed = credentialed || next.User != nil
			current = next
			continue
		}
		result, err := e.readFeedResponse(current, resp, etag, lastModified, temporary)
		_ = resp.Body.Close()
		if err != nil {
			return FeedResult{}, err
		}
		result.RedirectedTemporarily = temporary
		result.PermanentURL = permanentURL
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

func (e *Engine) readFeedResponse(current *url.URL, resp *http.Response, etag, lastModified string, temporary bool) (FeedResult, error) {
	if resp.StatusCode == http.StatusNotModified {
		return FeedResult{ConditionalResult: ConditionalResult{ETag: etag, LastModified: lastModified, NotModified: true}}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return FeedResult{}, &statusError{host: current.Hostname(), code: resp.StatusCode, temporaryFeedRedirect: temporary}
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
