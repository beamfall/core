package app

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/beamfall/core/internal/integrationhost"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/plugin"
)

// recordingOutboundProxy stands in for the allowlisted transport so the test can
// observe exactly what the production adapter hands the egress seam.
type recordingOutboundProxy struct {
	providerID string
	request    integrationhost.OutboundRequest
	response   integrationhost.OutboundResponse
	err        error
}

func (p *recordingOutboundProxy) RequestOutbound(_ context.Context, providerID string, req integrationhost.OutboundRequest) (integrationhost.OutboundResponse, error) {
	p.providerID = providerID
	p.request = req
	return p.response, p.err
}

// providerOutboundPolicySource is the operator-approval source the real
// HardenedOutboundProxy consults: every listed origin is declared and approved.
type providerOutboundPolicySource map[string][]string

func (s providerOutboundPolicySource) OutboundPolicy(_ context.Context, providerID string) (plugin.OutboundPolicy, bool, error) {
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

// A provider subtitle download must reach the egress proxy through the same
// wiring composition performs, and the payload must come back to the caller.
func TestProviderSubtitleFetchRoutesThroughWiredEgressAdapter(t *testing.T) {
	proxy := &recordingOutboundProxy{response: integrationhost.OutboundResponse{
		StatusCode: http.StatusOK,
		Body:       []byte("WEBVTT\n"),
	}}
	handler := &mediahttp.Handler{}
	wireProviderSubtitleFetch(handler, proxy)

	body, err := handler.FetchProviderSubtitle(context.Background(), "provider", "https://allowed.example.test/subtitle.vtt")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "WEBVTT\n" {
		t.Fatalf("payload = %q, want the egress response body", body)
	}
	if proxy.providerID != "provider" {
		t.Fatalf("provider id = %q, want %q", proxy.providerID, "provider")
	}
	if proxy.request.Method != http.MethodGet || proxy.request.URL != "https://allowed.example.test/subtitle.vtt" {
		t.Fatalf("egress request = %+v", proxy.request)
	}
}

// The paired negative: with the adapter unwired the seam reports unavailable, so
// the positive case above cannot pass on any path that skips the wiring.
func TestProviderSubtitleFetchUnavailableWithoutWiredAdapter(t *testing.T) {
	handler := &mediahttp.Handler{}
	if _, err := handler.FetchProviderSubtitle(context.Background(), "provider", "https://allowed.example.test/subtitle.vtt"); !errors.Is(err, mediahttp.ErrProviderSubtitleFetchUnavailable) {
		t.Fatalf("unwired fetch err = %v, want unavailable", err)
	}
	// A nil proxy must not produce a fetcher that dials: composition with no
	// egress leaves the seam unavailable rather than degrading to a raw client.
	wireProviderSubtitleFetch(handler, nil)
	if _, err := handler.FetchProviderSubtitle(context.Background(), "provider", "https://allowed.example.test/subtitle.vtt"); !errors.Is(err, mediahttp.ErrProviderSubtitleFetchUnavailable) {
		t.Fatalf("nil-proxy fetch err = %v, want unavailable", err)
	}
}

// The refusal the seam exists to preserve, asserted against the real production
// proxy through the production adapter: a non-allowlisted origin (and a
// same-host different-port origin) is denied before any transport runs.
func TestWiredProviderSubtitleFetchRefusesNonAllowlistedOrigin(t *testing.T) {
	handler := &mediahttp.Handler{}
	wireProviderSubtitleFetch(handler, integrationhost.NewHardenedOutboundProxy(providerOutboundPolicySource{
		"provider": {"https://allowed.example.test"},
	}))
	for _, target := range []string{
		"https://blocked.example.test/subtitle.zip",
		"https://allowed.example.test:8443/subtitle.zip",
	} {
		if _, err := handler.FetchProviderSubtitle(context.Background(), "provider", target); !errors.Is(err, integrationhost.ErrPermissionDenied) {
			t.Fatalf("FetchProviderSubtitle(%q) err = %v, want permission denied", target, err)
		}
	}
}
