package app

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/beamfall/core/internal/remoteaccess"
)

func TestNewPortMappingNeedsAKnownPort(t *testing.T) {
	for _, port := range []int{0, -1, 70000} {
		if newPortMapping(port, nil, nil) != nil {
			t.Fatalf("newPortMapping(%d) built a mapper for an unusable port", port)
		}
	}
	p := newPortMapping(8443, nil, nil)
	if p == nil {
		t.Fatal("newPortMapping(8443) = nil")
	}
	st := p.PortMapping(context.Background())
	if st.Enabled || st.State != "disabled" || st.InternalPort != 8443 {
		t.Fatalf("initial status = %+v, want disabled on 8443", st)
	}
	if _, ok := p.LastReachability(); ok {
		t.Fatal("a self-test reported before one ran")
	}
}

func TestGraphPortMappingStatus(t *testing.T) {
	expires := time.Date(2026, 11, 15, 12, 0, 0, 0, time.FixedZone("x", 3600))
	got := graphPortMappingStatus(remoteaccess.PortMapStatus{
		Enabled:         true,
		State:           remoteaccess.PortMapMapped,
		Protocol:        remoteaccess.PortMapProtocolUPnP,
		InternalPort:    8443,
		ExternalPort:    18443,
		ExternalAddress: netip.MustParseAddr("::ffff:203.0.113.7"),
		Lease:           2 * time.Hour,
		ExpiresAt:       expires,
	})
	if got.State != "mapped" || got.Protocol != "upnp" || got.ExternalPort != 18443 || got.LeaseSeconds != 7200 {
		t.Fatalf("status = %+v", got)
	}
	if got.ExternalAddress != "203.0.113.7" || got.ExpiresAt != "2026-11-15T11:00:00Z" {
		t.Fatalf("address/expiry = %q %q", got.ExternalAddress, got.ExpiresAt)
	}

	failed := graphPortMappingStatus(remoteaccess.PortMapStatus{
		Enabled: true, State: remoteaccess.PortMapFailed, Reason: remoteaccess.PortMapReasonGatewayRefused, GatewayCode: 718,
	})
	if failed.ExternalAddress != "" || failed.ExpiresAt != "" || failed.Reason != "gateway_refused" || failed.GatewayCode != 718 {
		t.Fatalf("failed status = %+v", failed)
	}
}

func TestGraphReachabilityReport(t *testing.T) {
	got := graphReachabilityReport(remoteaccess.ReachReport{
		CheckedAt: time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC),
		Direct:    remoteaccess.ReachVerdict{Path: remoteaccess.ReachPathDirect, Reason: remoteaccess.ReachReasonDoubleNAT},
		Relay:     remoteaccess.ReachVerdict{Path: remoteaccess.ReachPathRelay, Reachable: true},
	})
	if got.CheckedAt != "2026-11-15T10:00:00Z" || len(got.Verdicts) != 2 {
		t.Fatalf("report = %+v", got)
	}
	if got.Verdicts[0].Path != "direct" || got.Verdicts[0].Reason != "double_nat" || !got.Verdicts[1].Reachable {
		t.Fatalf("verdicts = %+v", got.Verdicts)
	}
}

// TestPortMappingSelfTestWithoutMappingOrRelay proves the self-test decides
// both verdicts without dialing anything when nothing is mapped and no relay
// is configured.
func TestPortMappingSelfTestWithoutMappingOrRelay(t *testing.T) {
	p := newPortMapping(8443, nil, nil)
	report := p.CheckReachability(context.Background())
	if report.Verdicts[0].Reason != "not_mapped" || report.Verdicts[1].Reason != "relay_not_configured" {
		t.Fatalf("verdicts = %+v", report.Verdicts)
	}
	if last, ok := p.LastReachability(); !ok || last.CheckedAt != report.CheckedAt {
		t.Fatalf("last = %+v %v", last, ok)
	}
}
