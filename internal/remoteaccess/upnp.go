package remoteaccess

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// UPnP IGD client: SSDP discovery, the device description, and the
// WANIPConnection / WANPPPConnection SOAP actions. The SSDP search, the HTTP
// transport and the local-address lookup are injectable so tests never touch
// a real network.

const (
	ssdpAddr          = "239.255.255.250:1900"
	ssdpWait          = 2 * time.Second
	upnpMaxBody       = 64 << 10
	upnpMappingLabel  = "Beamfall"
	upnpErrConflict   = 718
	upnpErrOnlyPerm   = 725
	upnpRequestBudget = 5 * time.Second
)

var ssdpSearchTargets = []string{
	"urn:schemas-upnp-org:device:InternetGatewayDevice:2",
	"urn:schemas-upnp-org:device:InternetGatewayDevice:1",
}

// upnpServiceTypes are the WAN connection services that carry port mapping,
// in preference order.
var upnpServiceTypes = []string{
	"urn:schemas-upnp-org:service:WANIPConnection:2",
	"urn:schemas-upnp-org:service:WANIPConnection:1",
	"urn:schemas-upnp-org:service:WANPPPConnection:1",
}

type upnpGateway struct {
	controlURL  string
	serviceType string
	localIP     netip.Addr
	client      *http.Client
}

func (g *upnpGateway) Protocol() string { return PortMapProtocolUPnP }

func (g *upnpGateway) ExternalAddress(ctx context.Context) (netip.Addr, error) {
	out, err := g.call(ctx, "GetExternalIPAddress", nil)
	if err != nil {
		return netip.Addr{}, err
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(out["NewExternalIPAddress"]))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("remoteaccess: upnp external address: %w", err)
	}
	return addr, nil
}

func (g *upnpGateway) AddMapping(ctx context.Context, internalPort, externalPort uint16, lifetime time.Duration) (uint16, time.Duration, error) {
	secs := int64(lifetime / time.Second)
	if secs < 0 {
		secs = 0
	}
	add := func(lease int64) error {
		_, err := g.call(ctx, "AddPortMapping", [][2]string{
			{"NewRemoteHost", ""},
			{"NewExternalPort", strconv.Itoa(int(externalPort))},
			{"NewProtocol", "TCP"},
			{"NewInternalPort", strconv.Itoa(int(internalPort))},
			{"NewInternalClient", g.localIP.String()},
			{"NewEnabled", "1"},
			{"NewPortMappingDescription", upnpMappingLabel},
			{"NewLeaseDuration", strconv.FormatInt(lease, 10)},
		})
		return err
	}
	err := add(secs)
	var refused *GatewayRefusedError
	if secs != 0 && errors.As(err, &refused) && refused.Code == upnpErrOnlyPerm {
		// IGD v1 gateways that only support permanent leases; the
		// mapper refreshes a permanent mapping periodically instead.
		secs = 0
		err = add(0)
	}
	if err != nil {
		return 0, 0, err
	}
	return externalPort, time.Duration(secs) * time.Second, nil
}

func (g *upnpGateway) DeleteMapping(ctx context.Context, _ uint16, externalPort uint16) error {
	_, err := g.call(ctx, "DeletePortMapping", [][2]string{
		{"NewRemoteHost", ""},
		{"NewExternalPort", strconv.Itoa(int(externalPort))},
		{"NewProtocol", "TCP"},
	})
	return err
}

// call posts one SOAP action and returns the response arguments by name. A
// SOAP fault becomes a GatewayRefusedError carrying the UPnP errorCode.
func (g *upnpGateway) call(ctx context.Context, action string, args [][2]string) (map[string]string, error) {
	var body bytes.Buffer
	body.WriteString(`<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body>`)
	fmt.Fprintf(&body, `<u:%s xmlns:u="%s">`, action, g.serviceType)
	for _, kv := range args {
		fmt.Fprintf(&body, "<%s>", kv[0])
		_ = xml.EscapeText(&body, []byte(kv[1]))
		fmt.Fprintf(&body, "</%s>", kv[0])
	}
	fmt.Fprintf(&body, `</u:%s></s:Body></s:Envelope>`, action)

	ctx, cancel := context.WithTimeout(ctx, upnpRequestBudget)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.controlURL, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", fmt.Sprintf(`"%s#%s"`, g.serviceType, action))
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remoteaccess: upnp %s: %w", action, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, upnpMaxBody))
	if err != nil {
		return nil, fmt.Errorf("remoteaccess: upnp %s: %w", action, err)
	}
	values := soapLeafValues(raw)
	if resp.StatusCode != http.StatusOK {
		if code, convErr := strconv.Atoi(strings.TrimSpace(values["errorCode"])); convErr == nil {
			return nil, &GatewayRefusedError{Protocol: PortMapProtocolUPnP, Code: code, Reason: strings.TrimSpace(values["errorDescription"])}
		}
		return nil, fmt.Errorf("remoteaccess: upnp %s: HTTP %d", action, resp.StatusCode)
	}
	return values, nil
}

// soapLeafValues maps each leaf element's local name to its text. SOAP
// responses here are flat argument lists, so the last value per name wins.
func soapLeafValues(raw []byte) map[string]string {
	out := map[string]string{}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var name string
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name = t.Name.Local
			text.Reset()
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			if name == t.Name.Local {
				out[name] = text.String()
			}
			name = ""
		}
	}
}

// upnpDiscoverer finds an IGD with SSDP and resolves its control URL.
type upnpDiscoverer struct {
	search  func(ctx context.Context) ([]string, error)
	client  *http.Client
	localIP func(ctx context.Context, host string) (netip.Addr, error)
}

func newUPnPDiscoverer() *upnpDiscoverer {
	return &upnpDiscoverer{
		search: ssdpSearch,
		client: &http.Client{
			Timeout: upnpRequestBudget,
			// A description or control URL never legitimately redirects; a
			// redirect could point the request off the LAN.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		localIP: localIPToward,
	}
}

func (d *upnpDiscoverer) Discover(ctx context.Context) (Gateway, error) {
	locations, err := d.search(ctx)
	if err != nil {
		return nil, err
	}
	lastErr := ErrNoGateway
	for _, loc := range locations {
		gw, err := d.resolve(ctx, loc)
		if err != nil {
			lastErr = err
			continue
		}
		return gw, nil
	}
	return nil, lastErr
}

func (d *upnpDiscoverer) resolve(ctx context.Context, location string) (*upnpGateway, error) {
	locURL, err := lanURL(location)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, locURL.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remoteaccess: upnp description: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remoteaccess: upnp description: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, upnpMaxBody))
	if err != nil {
		return nil, err
	}
	serviceType, control, err := findWANService(raw)
	if err != nil {
		return nil, err
	}
	base := locURL
	var desc struct {
		URLBase string `xml:"URLBase"`
	}
	if xml.Unmarshal(raw, &desc) == nil && strings.TrimSpace(desc.URLBase) != "" {
		if u, perr := url.Parse(strings.TrimSpace(desc.URLBase)); perr == nil {
			base = u
		}
	}
	ref, err := url.Parse(control)
	if err != nil {
		return nil, err
	}
	controlURL, err := lanURL(base.ResolveReference(ref).String())
	if err != nil {
		return nil, err
	}
	local, err := d.localIP(ctx, controlURL.Hostname())
	if err != nil {
		return nil, err
	}
	return &upnpGateway{controlURL: controlURL.String(), serviceType: serviceType, localIP: local, client: d.client}, nil
}

// upnpDevice is the recursive device tree of an IGD description.
type upnpDevice struct {
	Services []struct {
		ServiceType string `xml:"serviceType"`
		ControlURL  string `xml:"controlURL"`
	} `xml:"serviceList>service"`
	Devices []upnpDevice `xml:"deviceList>device"`
}

// findWANService returns the preferred WAN connection service in the
// description.
func findWANService(raw []byte) (serviceType, controlURL string, err error) {
	var root struct {
		Device upnpDevice `xml:"device"`
	}
	if err := xml.Unmarshal(raw, &root); err != nil {
		return "", "", fmt.Errorf("remoteaccess: upnp description: %w", err)
	}
	found := map[string]string{}
	var walk func(upnpDevice)
	walk = func(dev upnpDevice) {
		for _, s := range dev.Services {
			st := strings.TrimSpace(s.ServiceType)
			if _, seen := found[st]; !seen && strings.TrimSpace(s.ControlURL) != "" {
				found[st] = strings.TrimSpace(s.ControlURL)
			}
		}
		for _, child := range dev.Devices {
			walk(child)
		}
	}
	walk(root.Device)
	for _, st := range upnpServiceTypes {
		if c, ok := found[st]; ok {
			return st, c, nil
		}
	}
	return "", "", fmt.Errorf("%w: upnp device has no WAN connection service", ErrNoGateway)
}

// lanURL accepts only http URLs whose host is a private or link-local IP
// literal: SSDP answers are unauthenticated, so a spoofed LOCATION must not
// make the server fetch an arbitrary host.
func lanURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("remoteaccess: upnp url: %w", err)
	}
	if u.Scheme != "http" {
		return nil, fmt.Errorf("remoteaccess: upnp url scheme %q is not http", u.Scheme)
	}
	addr, err := netip.ParseAddr(u.Hostname())
	if err != nil || (!addr.IsPrivate() && !addr.IsLinkLocalUnicast()) {
		return nil, fmt.Errorf("remoteaccess: upnp url host %q is not a LAN address", u.Hostname())
	}
	return u, nil
}

// ssdpSearch multicasts M-SEARCH for an IGD and collects LOCATION headers
// until the wait ends.
func ssdpSearch(ctx context.Context) ([]string, error) {
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return nil, fmt.Errorf("%w: ssdp: %v", ErrNoGateway, err)
	}
	defer func() { _ = conn.Close() }()
	dst, err := net.ResolveUDPAddr("udp4", ssdpAddr)
	if err != nil {
		return nil, err
	}
	for _, st := range ssdpSearchTargets {
		msg := "M-SEARCH * HTTP/1.1\r\nHOST: " + ssdpAddr + "\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: " + st + "\r\n\r\n"
		if _, err := conn.WriteTo([]byte(msg), dst); err != nil {
			return nil, fmt.Errorf("%w: ssdp: %v", ErrNoGateway, err)
		}
	}
	deadline := time.Now().Add(ssdpWait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetReadDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })
	defer stop()
	var locations []string
	seen := map[string]bool{}
	buf := make([]byte, 2048)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			break
		}
		if loc := ssdpLocation(buf[:n]); loc != "" && !seen[loc] {
			seen[loc] = true
			locations = append(locations, loc)
		}
	}
	if len(locations) == 0 {
		return nil, ErrNoGateway
	}
	return locations, nil
}

// ssdpLocation extracts LOCATION from an SSDP response for an IGD search.
func ssdpLocation(b []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(b))
	var location string
	igd := false
	for sc.Scan() {
		name, value, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToUpper(strings.TrimSpace(name)) {
		case "LOCATION":
			location = value
		case "ST":
			igd = strings.Contains(value, "InternetGatewayDevice")
		}
	}
	if !igd {
		return ""
	}
	return location
}

// localIPToward returns the local address the OS would use to reach host. A
// UDP "connect" sends no packet.
func localIPToward(ctx context.Context, host string) (netip.Addr, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp4", net.JoinHostPort(host, "1900"))
	if err != nil {
		return netip.Addr{}, err
	}
	defer func() { _ = conn.Close() }()
	ap, err := netip.ParseAddrPort(conn.LocalAddr().String())
	if err != nil {
		return netip.Addr{}, err
	}
	return ap.Addr().Unmap(), nil
}
