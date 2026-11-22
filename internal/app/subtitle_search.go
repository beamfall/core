package app

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/integrationhost"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/subtitlesearch"
)

// Online subtitle search composition (CORE-1690).
//
// The operator opts in per provider (config SubtitleSearchProviders). Nothing
// here is constructed otherwise, so a default server makes no outbound
// subtitle request. When enabled, every byte rides HardenedOutboundProxy under
// a core-owned static policy naming exactly the provider's origins; the API key
// is read per request from the operator's 0600 secrets directory, never from
// config. Beamfall ships no OpenSubtitles key (owner decision 2026-11-21), so
// the release-injected BundledCredentials never reach this path.

// subtitleSearchPolicy is the outbound policy source for the enabled providers.
type subtitleSearchPolicy struct {
	origins map[string][]string
}

func (p subtitleSearchPolicy) OutboundPolicy(_ context.Context, providerID string) (plugin.OutboundPolicy, bool, error) {
	origins, ok := p.origins[providerID]
	if !ok {
		return plugin.OutboundPolicy{}, false, nil
	}
	return plugin.OutboundPolicy{PluginID: plugin.ID(providerID), Declared: origins, Approved: origins}, true, nil
}

// IsPublicRoutableIP lets the proxy dial public addresses only, like scrapes.
func (subtitleSearchPolicy) IsPublicRoutableIP(ip net.IP) bool {
	return scrape.IsPublicRoutableIP(ip)
}

// subtitleSearchEgress carries subtitlesearch requests on the hardened proxy.
type subtitleSearchEgress struct {
	proxy integrationhost.OutboundProxy
}

func (e subtitleSearchEgress) Do(ctx context.Context, providerID string, req subtitlesearch.Request) (subtitlesearch.Response, error) {
	response, err := e.proxy.RequestOutbound(ctx, providerID, integrationhost.OutboundRequest{
		Method: req.Method, URL: req.URL, Header: req.Header, Body: req.Body,
	})
	if err != nil {
		return subtitlesearch.Response{}, err
	}
	return subtitlesearch.Response{StatusCode: response.StatusCode, Body: response.Body}, nil
}

// subtitleSearchAdapter presents the service through mediahttp's own types.
type subtitleSearchAdapter struct {
	service *subtitlesearch.Service
}

func (a subtitleSearchAdapter) Status(ctx context.Context) []mediahttp.SubtitleSearchProvider {
	statuses := a.service.Status(ctx)
	out := make([]mediahttp.SubtitleSearchProvider, 0, len(statuses))
	for _, s := range statuses {
		out = append(out, mediahttp.SubtitleSearchProvider{ID: s.ID, Ready: s.Ready})
	}
	return out
}

func (a subtitleSearchAdapter) Search(ctx context.Context, q mediahttp.SubtitleSearchQuery) ([]mediahttp.SubtitleSearchResult, error) {
	results, err := a.service.Search(ctx, subtitlesearch.Query{
		OSHash: q.OSHash, ByteSize: q.ByteSize, Title: q.Title, Year: q.Year,
		Sequence: q.Sequence, Index: q.Index, Languages: q.Languages,
	})
	if err != nil {
		return nil, subtitleSearchError(err)
	}
	out := make([]mediahttp.SubtitleSearchResult, 0, len(results))
	for _, r := range results {
		out = append(out, mediahttp.SubtitleSearchResult{
			Provider: r.Provider, FileID: r.FileID, Language: r.Language, Release: r.Release,
			Rating: r.Rating, Downloads: r.Downloads, HashMatch: r.HashMatch, SDH: r.SDH, Forced: r.Forced,
		})
	}
	return out, nil
}

func (a subtitleSearchAdapter) Download(ctx context.Context, provider, fileID string) ([]byte, error) {
	body, err := a.service.Download(ctx, provider, fileID)
	if errors.Is(err, subtitlesearch.ErrDisabled) {
		return nil, mediahttp.ErrSubtitleSearchUnknownProvider
	}
	if err != nil {
		return nil, subtitleSearchError(err)
	}
	return body, nil
}

func subtitleSearchError(err error) error {
	switch {
	case errors.Is(err, subtitlesearch.ErrNoKey):
		return mediahttp.ErrSubtitleSearchNoKey
	case errors.Is(err, subtitlesearch.ErrNotSubtitle):
		return mediahttp.ErrSubtitleSearchNotSubtitle
	}
	return err
}

// subtitleSearchUserAgent is the identifying User-Agent OpenSubtitles requires
// ("<App> v<version>"). It carries no server or user identity.
func subtitleSearchUserAgent() string {
	version := strings.TrimSpace(capabilities.ReleaseVersion)
	if version == "" {
		version = "dev"
	}
	if version[0] >= '0' && version[0] <= '9' {
		version = "v" + version
	}
	return "Beamfall " + version
}

// newSubtitleSearcher builds the searcher for the enabled providers, or nil
// when none is enabled (the routes then refuse and nothing dials out).
func newSubtitleSearcher(providers []string, secrets interface{ Secret(string) (string, error) }) mediahttp.SubtitleSearcher {
	policy := subtitleSearchPolicy{origins: map[string][]string{}}
	var built []subtitlesearch.Provider
	proxy := integrationhost.NewHardenedOutboundProxy(policy)
	egress := subtitleSearchEgress{proxy: proxy}
	for _, id := range providers {
		if id != subtitlesearch.OpenSubtitlesID {
			continue
		}
		policy.origins[id] = append([]string{subtitlesearch.OpenSubtitlesAPIOrigin}, subtitlesearch.OpenSubtitlesDownloadOrigins...)
		key := func() (string, error) { return secrets.Secret(subtitlesearch.OpenSubtitlesKeyRef) }
		built = append(built, subtitlesearch.NewOpenSubtitles(egress, key, subtitleSearchUserAgent()))
	}
	if len(built) == 0 {
		return nil
	}
	return subtitleSearchAdapter{service: subtitlesearch.NewService(built...)}
}

// wireSubtitleSearch is the single production wiring path for CORE-1690. It
// takes the operator's secrets files, not the bundled-credential source, so a
// key can only come from the operator.
func wireSubtitleSearch(handler *mediahttp.Handler, providers []string, secrets secretFiles) {
	if searcher := newSubtitleSearcher(providers, secrets); searcher != nil {
		handler.SetSubtitleSearch(searcher)
	}
}
