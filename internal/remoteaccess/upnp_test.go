package remoteaccess

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// roundTripFunc serves HTTP in memory; no socket is opened.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func httpReply(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"text/xml"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

const testIGDDescription = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
 <device>
  <deviceType>urn:schemas-upnp-org:device:InternetGatewayDevice:1</deviceType>
  <deviceList><device>
   <deviceType>urn:schemas-upnp-org:device:WANDevice:1</deviceType>
   <deviceList><device>
    <deviceType>urn:schemas-upnp-org:device:WANConnectionDevice:1</deviceType>
    <serviceList>
     <service>
      <serviceType>urn:schemas-upnp-org:service:WANPPPConnection:1</serviceType>
      <controlURL>/ctl/PPPConn</controlURL>
     </service>
     <service>
      <serviceType>urn:schemas-upnp-org:service:WANIPConnection:1</serviceType>
      <controlURL>/ctl/IPConn</controlURL>
     </service>
    </serviceList>
   </device></deviceList>
  </device></deviceList>
 </device>
</root>`

func soapFault(code int, desc string) string {
	return `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>` +
		strconv.Itoa(code) + `</errorCode><errorDescription>` + desc + `</errorDescription></UPnPError></detail></s:Fault></s:Body></s:Envelope>`
}

type soapCall struct {
	action string
	body   string
	url    string
}

// fakeIGD is an in-memory UPnP gateway behind a RoundTripper.
type fakeIGD struct {
	mu    sync.Mutex
	calls []soapCall
	reply func(action string, n int) *http.Response
}

func (f *fakeIGD) transport(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodGet {
		if r.URL.String() != "http://192.168.1.1:5000/rootDesc.xml" {
			return httpReply(http.StatusNotFound, ""), nil
		}
		return httpReply(http.StatusOK, testIGDDescription), nil
	}
	raw, _ := io.ReadAll(r.Body)
	soapAction := r.Header.Get("SOAPAction")
	action := soapAction[strings.LastIndex(soapAction, "#")+1 : len(soapAction)-1]
	f.mu.Lock()
	f.calls = append(f.calls, soapCall{action: action, body: string(raw), url: r.URL.String()})
	n := 0
	for _, c := range f.calls {
		if c.action == action {
			n++
		}
	}
	f.mu.Unlock()
	if f.reply != nil {
		if resp := f.reply(action, n); resp != nil {
			return resp, nil
		}
	}
	if action == "GetExternalIPAddress" {
		return httpReply(http.StatusOK, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetExternalIPAddressResponse xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1"><NewExternalIPAddress>203.0.113.7</NewExternalIPAddress></u:GetExternalIPAddressResponse></s:Body></s:Envelope>`), nil
	}
	return httpReply(http.StatusOK, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body/></s:Envelope>`), nil
}

func (f *fakeIGD) discoverer(locations ...string) *upnpDiscoverer {
	return &upnpDiscoverer{
		search: func(context.Context) ([]string, error) { return locations, nil },
		client: &http.Client{Transport: roundTripFunc(f.transport)},
		localIP: func(context.Context, string) (netip.Addr, error) {
			return netip.MustParseAddr("192.168.1.20"), nil
		},
	}
}

func discoverFakeIGD(t *testing.T, f *fakeIGD) *upnpGateway {
	t.Helper()
	gw, err := f.discoverer("http://192.168.1.1:5000/rootDesc.xml").Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return gw.(*upnpGateway)
}

func TestUPnPDiscoverPrefersWANIPConnection(t *testing.T) {
	gw := discoverFakeIGD(t, &fakeIGD{})
	if gw.controlURL != "http://192.168.1.1:5000/ctl/IPConn" {
		t.Fatalf("controlURL = %q", gw.controlURL)
	}
	if gw.serviceType != "urn:schemas-upnp-org:service:WANIPConnection:1" || gw.Protocol() != PortMapProtocolUPnP {
		t.Fatalf("service = %q", gw.serviceType)
	}
}

func TestUPnPAddMapping(t *testing.T) {
	f := &fakeIGD{}
	gw := discoverFakeIGD(t, f)
	ext, lease, err := gw.AddMapping(context.Background(), 8443, 8443, 2*time.Hour)
	if err != nil || ext != 8443 || lease != 2*time.Hour {
		t.Fatalf("AddMapping = %d %v %v", ext, lease, err)
	}
	call := f.calls[0]
	for _, want := range []string{
		"<NewExternalPort>8443</NewExternalPort>",
		"<NewProtocol>TCP</NewProtocol>",
		"<NewInternalPort>8443</NewInternalPort>",
		"<NewInternalClient>192.168.1.20</NewInternalClient>",
		"<NewLeaseDuration>7200</NewLeaseDuration>",
	} {
		if !strings.Contains(call.body, want) {
			t.Fatalf("AddPortMapping body lacks %s:\n%s", want, call.body)
		}
	}
	if call.action != "AddPortMapping" || call.url != "http://192.168.1.1:5000/ctl/IPConn" {
		t.Fatalf("call = %+v", call)
	}
}

func TestUPnPFallsBackToPermanentLease(t *testing.T) {
	f := &fakeIGD{reply: func(action string, n int) *http.Response {
		if action == "AddPortMapping" && n == 1 {
			return httpReply(http.StatusInternalServerError, soapFault(725, "OnlyPermanentLeasesSupported"))
		}
		return nil
	}}
	gw := discoverFakeIGD(t, f)
	_, lease, err := gw.AddMapping(context.Background(), 8443, 8443, 2*time.Hour)
	if err != nil || lease != 0 {
		t.Fatalf("lease = %v err = %v, want permanent", lease, err)
	}
	if !strings.Contains(f.calls[1].body, "<NewLeaseDuration>0</NewLeaseDuration>") {
		t.Fatalf("retry body = %s", f.calls[1].body)
	}
}

func TestUPnPRefusal(t *testing.T) {
	f := &fakeIGD{reply: func(action string, _ int) *http.Response {
		if action == "AddPortMapping" {
			return httpReply(http.StatusInternalServerError, soapFault(718, "ConflictInMappingEntry"))
		}
		return nil
	}}
	gw := discoverFakeIGD(t, f)
	_, _, err := gw.AddMapping(context.Background(), 8443, 8443, time.Hour)
	var refused *GatewayRefusedError
	if !errors.As(err, &refused) || refused.Code != 718 || refused.Reason != "ConflictInMappingEntry" {
		t.Fatalf("err = %v, want refusal 718", err)
	}
}

func TestUPnPExternalAddressAndDelete(t *testing.T) {
	f := &fakeIGD{}
	gw := discoverFakeIGD(t, f)
	addr, err := gw.ExternalAddress(context.Background())
	if err != nil || addr != netip.MustParseAddr("203.0.113.7") {
		t.Fatalf("addr = %v err = %v", addr, err)
	}
	if err := gw.DeleteMapping(context.Background(), 8443, 8443); err != nil {
		t.Fatalf("DeleteMapping: %v", err)
	}
	last := f.calls[len(f.calls)-1]
	if last.action != "DeletePortMapping" || !strings.Contains(last.body, "<NewExternalPort>8443</NewExternalPort>") {
		t.Fatalf("delete call = %+v", last)
	}
}

func TestUPnPRejectsNonLANLocations(t *testing.T) {
	f := &fakeIGD{}
	for _, loc := range []string{
		"http://203.0.113.9/rootDesc.xml",
		"https://192.168.1.1/rootDesc.xml",
		"http://router.example.test/rootDesc.xml",
	} {
		if _, err := f.discoverer(loc).Discover(context.Background()); err == nil {
			t.Fatalf("accepted location %q", loc)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("made SOAP calls: %+v", f.calls)
	}
}

func TestSSDPLocation(t *testing.T) {
	resp := "HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=120\r\nST: urn:schemas-upnp-org:device:InternetGatewayDevice:1\r\nLocation: http://192.168.1.1:5000/rootDesc.xml\r\n\r\n"
	if got := ssdpLocation([]byte(resp)); got != "http://192.168.1.1:5000/rootDesc.xml" {
		t.Fatalf("location = %q", got)
	}
	other := "HTTP/1.1 200 OK\r\nST: urn:schemas-upnp-org:device:MediaRenderer:1\r\nLOCATION: http://192.168.1.30/desc.xml\r\n\r\n"
	if got := ssdpLocation([]byte(other)); got != "" {
		t.Fatalf("non-IGD location = %q", got)
	}
}

func TestParseProcNetRoute(t *testing.T) {
	raw := "Iface\tDestination\tGateway \tFlags\n" +
		"eth0\t0001A8C0\t00000000\t0001\n" +
		"eth0\t00000000\t0101A8C0\t0003\n"
	gw, ok := parseProcNetRoute(raw)
	if !ok || gw != netip.MustParseAddr("192.168.1.1") {
		t.Fatalf("gateway = %v %v", gw, ok)
	}
}

type stubGateway struct {
	fakeGateway
	addrErr error
}

func (s *stubGateway) ExternalAddress(context.Context) (netip.Addr, error) {
	return s.external, s.addrErr
}

func TestDiscovererFallsBackToNATPMP(t *testing.T) {
	silent := &stubGateway{addrErr: ErrNoGateway}
	answering := &stubGateway{fakeGateway: fakeGateway{protocol: PortMapProtocolNATPMP}}
	byAddr := map[netip.Addr]Gateway{
		netip.MustParseAddr("10.0.0.1"):    silent,
		netip.MustParseAddr("192.168.1.1"): answering,
	}
	d := &portMapDiscoverer{
		upnp: &fakeDiscoverer{err: ErrNoGateway},
		candidates: func() []netip.Addr {
			return []netip.Addr{netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("192.168.1.1")}
		},
		natpmp: func(a netip.Addr) Gateway { return byAddr[a] },
	}
	gw, err := d.Discover(context.Background())
	if err != nil || gw != answering {
		t.Fatalf("Discover = %v, %v", gw, err)
	}

	d.candidates = func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("10.0.0.1")} }
	if _, err := d.Discover(context.Background()); !errors.Is(err, ErrNoGateway) {
		t.Fatalf("err = %v, want ErrNoGateway", err)
	}
}

func TestDiscovererPrefersUPnP(t *testing.T) {
	upnp := &fakeGateway{protocol: PortMapProtocolUPnP}
	d := &portMapDiscoverer{
		upnp:       &fakeDiscoverer{gw: upnp},
		candidates: func() []netip.Addr { t.Fatal("NAT-PMP probed although UPnP answered"); return nil },
	}
	if gw, err := d.Discover(context.Background()); err != nil || gw != upnp {
		t.Fatalf("Discover = %v, %v", gw, err)
	}
}
