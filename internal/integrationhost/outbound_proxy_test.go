package integrationhost

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
)

type staticOutboundPolicy map[string]plugin.OutboundPolicy

func (staticOutboundPolicy) IsPublicRoutableIP(ip net.IP) bool {
	return scrape.IsPublicRoutableIP(ip)
}

func (s staticOutboundPolicy) OutboundPolicy(_ context.Context, pluginID string) (plugin.OutboundPolicy, bool, error) {
	policy, ok := s[pluginID]
	return policy, ok, nil
}

func testOutboundPolicy(t *testing.T, declared, approved []string) plugin.OutboundPolicy {
	t.Helper()
	policy, err := plugin.NewOutboundPolicy(
		plugin.OutboundDeclaration{PluginID: "worker", Origins: declared},
		approved,
	)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestHardenedOutboundProxyRejectsUndeclaredTargetBeforeTransport(t *testing.T) {
	policy := testOutboundPolicy(t,
		[]string{"https://declared.example.test"},
		[]string{"https://declared.example.test"},
	)
	p := NewHardenedOutboundProxy(staticOutboundPolicy{"worker": policy})
	called := false
	p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("must not run")
	})
	_, err := p.RequestOutbound(context.Background(), "worker", OutboundRequest{URL: "https://other.example.test/path"})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("undeclared target error = %v, want permission denied", err)
	}
	if called {
		t.Fatal("transport reached for undeclared target")
	}
}

func TestHardenedOutboundProxyRejectsDeclaredButUnapprovedTargetBeforeTransport(t *testing.T) {
	policy := testOutboundPolicy(t, []string{"https://declared.example.test"}, nil)
	p := NewHardenedOutboundProxy(staticOutboundPolicy{"worker": policy})
	called := false
	p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("must not run")
	})
	_, err := p.RequestOutbound(context.Background(), "worker", OutboundRequest{URL: "https://declared.example.test/path"})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("unapproved target error = %v, want permission denied", err)
	}
	if called {
		t.Fatal("transport reached for declared but operator-unapproved target")
	}
}

func TestHardenedOutboundProxyRoutesDeclaredTargetThroughOwnedAdapter(t *testing.T) {
	policy := testOutboundPolicy(t,
		[]string{"https://api.example.test"},
		[]string{"https://api.example.test"},
	)
	p := NewHardenedOutboundProxy(staticOutboundPolicy{"worker": policy})
	var got *http.Request
	p.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		got = req
		return &http.Response{
			StatusCode: http.StatusCreated,
			Header:     http.Header{"X-Result": {"ok"}},
			Body:       io.NopCloser(strings.NewReader("created")),
			Request:    req,
		}, nil
	})
	resp, err := p.RequestOutbound(context.Background(), "worker", OutboundRequest{
		Method: "POST",
		URL:    "https://api.example.test/v1/items",
		Header: map[string][]string{"Content-Type": {"application/json"}},
		Body:   []byte(`{"name":"item"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.URL.Path != "/v1/items" || got.Method != http.MethodPost {
		t.Fatalf("adapter request = %#v", got)
	}
	if resp.StatusCode != http.StatusCreated || string(resp.Body) != "created" || http.Header(resp.Header).Get("X-Result") != "ok" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestHardenedOutboundProxyRefusesUnsafeURLAndRedirect(t *testing.T) {
	policy := testOutboundPolicy(t,
		[]string{"https://api.example.test"},
		[]string{"https://api.example.test"},
	)
	p := NewHardenedOutboundProxy(staticOutboundPolicy{"worker": policy})
	for _, target := range []string{
		"http://api.example.test/path",
		"https://127.0.0.1/path",
		"https://user:secret@api.example.test/path",
	} {
		if _, err := p.RequestOutbound(context.Background(), "worker", OutboundRequest{URL: target}); err == nil {
			t.Fatalf("unsafe URL %q accepted", target)
		}
	}
	first, _ := http.NewRequest(http.MethodGet, "https://api.example.test/start", nil)
	cross, _ := http.NewRequest(http.MethodGet, "https://other.example.test/next", nil)
	if err := p.checkRedirect(cross, []*http.Request{first}); err == nil {
		t.Fatal("cross-origin redirect accepted")
	}
	same, _ := http.NewRequest(http.MethodGet, "https://api.example.test/next", nil)
	if err := p.checkRedirect(same, []*http.Request{first}); err != nil {
		t.Fatalf("same-origin redirect rejected: %v", err)
	}
}

type fixedResolver []net.IPAddr

func (r fixedResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return append([]net.IPAddr(nil), r...), nil
}

func TestHardenedOutboundProxyDialPinsVettedIPAndRejectsPrivateAnswer(t *testing.T) {
	p := NewHardenedOutboundProxy(staticOutboundPolicy{})
	dials := 0
	p.resolver = fixedResolver{{IP: net.ParseIP("169.254.169.254")}}
	p.dial = func(context.Context, string, string) (net.Conn, error) {
		dials++
		return nil, errors.New("unexpected dial")
	}
	if _, err := p.dialContext(context.Background(), "tcp", "metadata.example.test:443"); err == nil {
		t.Fatal("metadata address accepted")
	}
	if dials != 0 {
		t.Fatalf("private answer reached dialer %d time(s)", dials)
	}

	p.resolver = fixedResolver{{IP: net.ParseIP("8.8.8.8")}}
	var dialAddress string
	p.dial = func(_ context.Context, _, address string) (net.Conn, error) {
		dialAddress = address
		return nil, errors.New("stop after observing pinned address")
	}
	_, _ = p.dialContext(context.Background(), "tcp", "public.example.test:443")
	if dialAddress != "8.8.8.8:443" {
		t.Fatalf("dial address = %q, want resolved concrete IP", dialAddress)
	}
}

func TestHardenedOutboundProxyCapsResponseAndRejectsHopHeader(t *testing.T) {
	policy := testOutboundPolicy(t,
		[]string{"https://api.example.test"},
		[]string{"https://api.example.test"},
	)
	p := NewHardenedOutboundProxy(staticOutboundPolicy{"worker": policy})
	p.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", maxOutboundResponseBody+1))),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})
	if _, err := p.RequestOutbound(context.Background(), "worker", OutboundRequest{URL: "https://api.example.test"}); err == nil {
		t.Fatal("over-cap response accepted")
	}
	if _, err := p.RequestOutbound(context.Background(), "worker", OutboundRequest{
		URL: "https://api.example.test", Header: map[string][]string{"Proxy-Authorization": {"secret"}},
	}); err == nil {
		t.Fatal("hop-by-hop credential header accepted")
	}
}

func TestSameOriginNormalizesDefaultPort(t *testing.T) {
	a, _ := url.Parse("https://api.example.test:443/a")
	b, _ := url.Parse("https://api.example.test/b")
	if !sameOrigin(a, b) {
		t.Fatal("default HTTPS port should normalize to the same origin")
	}
}
