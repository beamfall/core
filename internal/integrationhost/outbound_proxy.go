package integrationhost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/beamfall/core/internal/plugin"
)

const (
	outboundTimeout         = 15 * time.Second
	maxOutboundResponseBody = 512 << 10
	maxOutboundRedirects    = 5
)

// OutboundPolicySource resolves the current manifest declaration and its
// operator-approved subset atomically. A nil source, source error, unknown
// plugin, empty declaration, or unapproved origin denies outbound.
type OutboundPolicySource interface {
	OutboundPolicy(ctx context.Context, pluginID string) (plugin.OutboundPolicy, bool, error)
}

type ipResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

type dialContextFunc func(context.Context, string, string) (net.Conn, error)
type publicRoutabilityPredicate func(net.IP) bool

// HardenedOutboundProxy is the production OutboundProxy adapter. It composes
// the plugin manifest allowlist with an HTTPS-only, proxy-free client whose
// dialer resolves once, vets the concrete addresses, and dials only those vetted
// IPs. Redirects remain on the original declared origin and every body is
// bounded before it crosses the worker protocol.
type HardenedOutboundProxy struct {
	source   OutboundPolicySource
	resolver ipResolver
	dial     dialContextFunc
	client   *http.Client
	publicIP publicRoutabilityPredicate
}

// NewHardenedOutboundProxy builds the enforced Host API adapter. It owns its
// transport; environment proxy variables and http.DefaultClient are never used.
func NewHardenedOutboundProxy(source OutboundPolicySource) *HardenedOutboundProxy {
	publicIP := publicRoutabilityPredicate(func(net.IP) bool { return false })
	if configured, ok := source.(interface{ IsPublicRoutableIP(net.IP) bool }); ok {
		publicIP = configured.IsPublicRoutableIP
	}
	dialer := &net.Dialer{Timeout: outboundTimeout}
	p := &HardenedOutboundProxy{
		source:   source,
		resolver: net.DefaultResolver,
		dial:     dialer.DialContext,
		publicIP: publicIP,
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           p.dialContext,
		TLSHandshakeTimeout:   outboundTimeout,
		ResponseHeaderTimeout: outboundTimeout,
	}
	//io:ignore — Host.RequestOutbound writes the plugin-attributed audit row before invoking this adapter.
	p.client = &http.Client{
		Timeout:       outboundTimeout,
		Transport:     transport,
		CheckRedirect: p.checkRedirect,
	}
	return p
}

// RequestOutbound validates the plugin declaration before constructing or
// sending a request. Errors never echo a raw URL because it may contain secrets.
func (p *HardenedOutboundProxy) RequestOutbound(ctx context.Context, pluginID string, call OutboundRequest) (OutboundResponse, error) {
	if p == nil || p.source == nil || p.client == nil {
		return OutboundResponse{}, fmt.Errorf("%w: outbound proxy unavailable", ErrPermissionDenied)
	}
	parsed, err := url.Parse(call.URL)
	if err != nil {
		return OutboundResponse{}, errors.New("integrationhost: invalid outbound URL")
	}
	if err := validateOutboundURL(parsed); err != nil {
		return OutboundResponse{}, err
	}
	if err := p.requireApproved(ctx, pluginID, parsed); err != nil {
		return OutboundResponse{}, err
	}
	method := strings.ToUpper(strings.TrimSpace(call.Method))
	if method == "" {
		method = http.MethodGet
	}
	if method == http.MethodConnect {
		return OutboundResponse{}, errors.New("integrationhost: CONNECT is not allowed")
	}
	req, err := http.NewRequestWithContext(ctx, method, parsed.String(), bytes.NewReader(call.Body))
	if err != nil {
		return OutboundResponse{}, errors.New("integrationhost: invalid outbound request")
	}
	for name, values := range call.Header {
		if isHopByHopHeader(name) {
			return OutboundResponse{}, fmt.Errorf("integrationhost: outbound header %q is not allowed", name)
		}
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return OutboundResponse{}, sanitizeOutboundTransportError(parsed, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOutboundResponseBody+1))
	if err != nil {
		return OutboundResponse{}, fmt.Errorf("integrationhost: reading outbound response from %s: %w", parsed.Hostname(), err)
	}
	if len(body) > maxOutboundResponseBody {
		return OutboundResponse{}, fmt.Errorf("integrationhost: outbound response from %s exceeds the %d-byte cap", parsed.Hostname(), maxOutboundResponseBody)
	}
	return OutboundResponse{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: body}, nil
}

func (p *HardenedOutboundProxy) requireApproved(ctx context.Context, pluginID string, target *url.URL) error {
	policy, ok, err := p.source.OutboundPolicy(ctx, pluginID)
	if err != nil {
		return fmt.Errorf("integrationhost: resolve outbound policy: %w", err)
	}
	if !ok {
		return fmt.Errorf("%w: outbound policy is not registered for plugin %q", ErrPermissionDenied, pluginID)
	}
	policy, err = plugin.NewOutboundPolicy(
		plugin.OutboundDeclaration{PluginID: policy.PluginID, Origins: policy.Declared},
		policy.Approved,
	)
	if err != nil || string(policy.PluginID) != pluginID {
		return fmt.Errorf("%w: outbound policy is invalid for plugin %q", ErrPermissionDenied, pluginID)
	}
	targetOrigin, err := plugin.NormalizeOutboundOrigin(target.Scheme + "://" + target.Host)
	if err != nil {
		return errors.New("integrationhost: outbound target has an invalid origin")
	}
	if policy.Allows(targetOrigin) {
		return nil
	}
	return fmt.Errorf("%w: outbound origin %q is not operator-approved for plugin %q", ErrPermissionDenied, targetOrigin, pluginID)
}

func validateOutboundURL(parsed *url.URL) error {
	if parsed == nil || parsed.Hostname() == "" {
		return errors.New("integrationhost: outbound URL requires a host")
	}
	if parsed.Scheme != "https" {
		return errors.New("integrationhost: outbound URL requires https")
	}
	if parsed.User != nil {
		return errors.New("integrationhost: outbound URL credentials are not allowed")
	}
	if net.ParseIP(parsed.Hostname()) != nil {
		return errors.New("integrationhost: outbound URL requires a hostname")
	}
	return nil
}

func (p *HardenedOutboundProxy) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxOutboundRedirects {
		return fmt.Errorf("integrationhost: refused after %d outbound redirects", maxOutboundRedirects)
	}
	if err := validateOutboundURL(req.URL); err != nil {
		return err
	}
	if len(via) == 0 || !sameOrigin(req.URL, via[0].URL) {
		return errors.New("integrationhost: refused cross-origin outbound redirect")
	}
	return nil
}

func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	aOrigin, aErr := plugin.NormalizeOutboundOrigin(a.Scheme + "://" + a.Host)
	bOrigin, bErr := plugin.NormalizeOutboundOrigin(b.Scheme + "://" + b.Host)
	return aErr == nil && bErr == nil && aOrigin == bOrigin
}

// dialContext resolves the hostname exactly once for this connection attempt,
// rejects every non-public answer, then dials the already-vetted concrete IP.
// The transport retains the original hostname for TLS SNI/certificate checks.
func (p *HardenedOutboundProxy) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("integrationhost: refused malformed outbound dial address")
	}
	addresses, err := p.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("integrationhost: resolve outbound host %q: %w", host, err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("integrationhost: outbound host %q resolved to no addresses", host)
	}
	for _, address := range addresses {
		if !p.publicIP(address.IP) {
			return nil, fmt.Errorf("integrationhost: refused outbound host %q resolving to a non-public address", host)
		}
	}
	var dialErrors []error
	for _, address := range addresses {
		conn, dialErr := p.dial(ctx, network, net.JoinHostPort(address.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		dialErrors = append(dialErrors, dialErr)
	}
	return nil, fmt.Errorf("integrationhost: outbound dial to %q failed: %w", host, errors.Join(dialErrors...))
}

func isHopByHopHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}

func sanitizeOutboundTransportError(parsed *url.URL, err error) error {
	cause := err
	if urlErr, ok := err.(*url.Error); ok {
		cause = urlErr.Err
	}
	return fmt.Errorf("integrationhost: outbound request to %s failed: %w", parsed.Hostname(), cause)
}
