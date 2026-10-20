package app

import (
	"context"

	"github.com/beamfall/core/internal/integrationhost"
	"github.com/beamfall/core/internal/mediahttp"
)

// The production provider-subtitle egress adapter.
//
// mediahttp owns ProviderSubtitleEgress plus its own request/response types so
// that package imports nothing outside its permitted dependency row
// (ARCHITECTURE.md §4: store, artwork, genassets, playback, streamtoken, auth).
// Go's structural typing therefore leaves integrationhost.OutboundProxy short of
// satisfying that interface — the argument types differ — so the translation
// lives here, in the composition root, the one package allowed to import both.
//
// The adapter carries only data across the seam. It never builds an HTTP client:
// every byte still travels on HardenedOutboundProxy's owned, allowlist-enforcing
// transport, so provider downloads cannot open a raw-client gap outside
// egress-lint governance.

type providerSubtitleEgress struct {
	proxy integrationhost.OutboundProxy
}

func (e providerSubtitleEgress) RequestOutbound(ctx context.Context, providerID string, req mediahttp.ProviderSubtitleRequest) (mediahttp.ProviderSubtitleResponse, error) {
	response, err := e.proxy.RequestOutbound(ctx, providerID, integrationhost.OutboundRequest{
		Method: req.Method,
		URL:    req.URL,
	})
	if err != nil {
		return mediahttp.ProviderSubtitleResponse{}, err
	}
	return mediahttp.ProviderSubtitleResponse{
		StatusCode: response.StatusCode,
		Body:       response.Body,
	}, nil
}

// newProviderSubtitleFetcher builds the fetcher over the allowlisted egress
// proxy. A nil proxy yields a nil fetcher, whose Fetch reports the seam
// unavailable rather than dialing anything.
func newProviderSubtitleFetcher(proxy integrationhost.OutboundProxy) *mediahttp.ProviderSubtitleFetcher {
	if proxy == nil {
		return nil
	}
	return mediahttp.NewProviderSubtitleFetcher(providerSubtitleEgress{proxy: proxy})
}

// wireProviderSubtitleFetch binds the provider subtitle download seam on the
// media handler. It is the single production wiring path, so a test that drives
// this function exercises exactly what composition performs.
func wireProviderSubtitleFetch(handler *mediahttp.Handler, proxy integrationhost.OutboundProxy) {
	handler.SetProviderSubtitleFetcher(newProviderSubtitleFetcher(proxy))
}
