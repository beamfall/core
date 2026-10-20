package mediahttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

var ErrProviderSubtitleFetchUnavailable = errors.New("mediahttp: provider subtitle fetch unavailable")

type ProviderSubtitleRequest struct {
	Method string
	URL    string
}

type ProviderSubtitleResponse struct {
	StatusCode int
	Body       []byte
}

type ProviderSubtitleEgress interface {
	RequestOutbound(ctx context.Context, providerID string, req ProviderSubtitleRequest) (ProviderSubtitleResponse, error)
}

// ProviderSubtitleFetcher carries provider subtitle downloads through the
// allowlisted egress seam. It never owns a raw HTTP client, so provider fetches
// cannot introduce a silent raw-client gap outside egress-lint governance.
type ProviderSubtitleFetcher struct {
	egress ProviderSubtitleEgress
}

func NewProviderSubtitleFetcher(egress ProviderSubtitleEgress) *ProviderSubtitleFetcher {
	return &ProviderSubtitleFetcher{egress: egress}
}

// SetProviderSubtitleFetcher wires the provider subtitle download seam. The
// composition root is the only caller: it owns the adapter that carries this
// package's request/response types onto the allowlisted egress proxy, so this
// package keeps importing nothing outside its permitted dependency row. Nil
// (the default) leaves provider downloads unavailable rather than falling back
// to a raw client.
func (h *Handler) SetProviderSubtitleFetcher(fetcher *ProviderSubtitleFetcher) {
	h.providerSubtitles = fetcher
}

// FetchProviderSubtitle downloads one provider-hosted subtitle payload through
// the wired egress seam. It refuses when the seam is unwired, so no caller can
// reach a provider origin except through the operator-approved allowlist.
func (h *Handler) FetchProviderSubtitle(ctx context.Context, providerID, rawURL string) ([]byte, error) {
	if h == nil || h.providerSubtitles == nil {
		return nil, ErrProviderSubtitleFetchUnavailable
	}
	return h.providerSubtitles.Fetch(ctx, providerID, rawURL)
}

func (f *ProviderSubtitleFetcher) Fetch(ctx context.Context, providerID, rawURL string) ([]byte, error) {
	if f == nil || f.egress == nil {
		return nil, ErrProviderSubtitleFetchUnavailable
	}
	response, err := f.egress.RequestOutbound(ctx, providerID, ProviderSubtitleRequest{
		Method: http.MethodGet,
		URL:    rawURL,
	})
	if err != nil {
		return nil, fmt.Errorf("mediahttp: provider subtitle fetch: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("mediahttp: provider subtitle fetch returned status %d", response.StatusCode)
	}
	return response.Body, nil
}
