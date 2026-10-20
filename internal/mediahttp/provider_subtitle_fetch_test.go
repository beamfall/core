package mediahttp_test

import (
	"context"
	"errors"
	"testing"

	"github.com/beamfall/core/internal/integrationhost"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/plugin"
)

type providerOutboundAllowlist map[string][]string

// OutboundPolicy adapts the map-based allowlist to integrationhost.OutboundPolicySource:
// every listed origin is both declared and approved for that provider.
func (s providerOutboundAllowlist) OutboundPolicy(_ context.Context, providerID string) (plugin.OutboundPolicy, bool, error) {
	origins, ok := s[providerID]
	if !ok {
		return plugin.OutboundPolicy{}, false, nil
	}
	policy, err := plugin.NewOutboundPolicy(
		plugin.OutboundDeclaration{PluginID: plugin.ID(providerID), Origins: origins},
		origins,
	)
	return policy, true, err
}

type providerSubtitleEgressAdapter struct {
	proxy integrationhost.OutboundProxy
}

func (a providerSubtitleEgressAdapter) RequestOutbound(ctx context.Context, providerID string, req mediahttp.ProviderSubtitleRequest) (mediahttp.ProviderSubtitleResponse, error) {
	response, err := a.proxy.RequestOutbound(ctx, providerID, integrationhost.OutboundRequest{
		Method: req.Method,
		URL:    req.URL,
	})
	return mediahttp.ProviderSubtitleResponse{
		StatusCode: response.StatusCode,
		Body:       response.Body,
	}, err
}

func TestProviderSubtitleFetcherRefusesNonAllowlistedOrigin(t *testing.T) {
	proxy := integrationhost.NewHardenedOutboundProxy(providerOutboundAllowlist{
		"provider": {"https://allowed.example.test"},
	})
	fetcher := mediahttp.NewProviderSubtitleFetcher(providerSubtitleEgressAdapter{proxy: proxy})
	for _, target := range []string{
		"https://blocked.example.test/subtitle.zip",
		"https://allowed.example.test:8443/subtitle.zip",
	} {
		if _, err := fetcher.Fetch(context.Background(), "provider", target); !errors.Is(err, integrationhost.ErrPermissionDenied) {
			t.Fatalf("Fetch(%q) err = %v, want permission denied", target, err)
		}
	}
}
