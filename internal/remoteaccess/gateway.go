package remoteaccess

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"
)

// Gateway discovery: UPnP IGD first (it names its control URL), then NAT-PMP
// at the likely default gateway. Go's standard library has no portable
// default-route lookup, so the NAT-PMP candidates are the Linux routing
// table's default gateway, else the first host address of each private IPv4
// interface network (the usual home-router address).

const (
	upnpDiscoverBudget   = 4 * time.Second
	natPMPDiscoverBudget = 4 * time.Second
	maxNATPMPCandidates  = 3
)

type portMapDiscoverer struct {
	upnp       GatewayDiscoverer
	candidates func() []netip.Addr
	natpmp     func(netip.Addr) Gateway
}

// NewGatewayDiscoverer is the production discoverer.
func NewGatewayDiscoverer() GatewayDiscoverer {
	return &portMapDiscoverer{
		upnp:       newUPnPDiscoverer(),
		candidates: natPMPCandidates,
		natpmp:     func(a netip.Addr) Gateway { return newNATPMPGateway(a) },
	}
}

func (d *portMapDiscoverer) Discover(ctx context.Context) (Gateway, error) {
	uctx, cancel := context.WithTimeout(ctx, upnpDiscoverBudget)
	gw, err := d.upnp.Discover(uctx)
	cancel()
	if err == nil {
		return gw, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	for _, addr := range d.candidates() {
		candidate := d.natpmp(addr)
		nctx, cancel := context.WithTimeout(ctx, natPMPDiscoverBudget)
		_, perr := candidate.ExternalAddress(nctx)
		cancel()
		var refused *GatewayRefusedError
		if perr == nil || errors.As(perr, &refused) {
			// A refusal still proves a NAT-PMP gateway is there; the map
			// request reports its code.
			return candidate, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, ErrNoGateway
}

func natPMPCandidates() []netip.Addr {
	if raw, err := os.ReadFile("/proc/net/route"); err == nil {
		if gw, ok := parseProcNetRoute(string(raw)); ok {
			return []netip.Addr{gw}
		}
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if gw, ok := firstHost(ipn); ok && len(out) < maxNATPMPCandidates {
				out = append(out, gw)
			}
		}
	}
	return out
}

// firstHost is network+1 of a private IPv4 interface network.
func firstHost(ipn *net.IPNet) (netip.Addr, bool) {
	addr, ok := netip.AddrFromSlice(ipn.IP)
	if !ok {
		return netip.Addr{}, false
	}
	addr = addr.Unmap()
	ones, bits := ipn.Mask.Size()
	if !addr.Is4() || !addr.IsPrivate() || bits != 32 || ones > 30 {
		return netip.Addr{}, false
	}
	gw := netip.PrefixFrom(addr, ones).Masked().Addr().Next()
	if gw == addr {
		return netip.Addr{}, false
	}
	return gw, true
}

// parseProcNetRoute returns the default route's gateway from Linux
// /proc/net/route (hex, host byte order little-endian).
func parseProcNetRoute(raw string) (netip.Addr, bool) {
	for _, line := range strings.Split(raw, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 3 || f[1] != "00000000" {
			continue
		}
		b, err := hex.DecodeString(f[2])
		if err != nil || len(b) != 4 {
			continue
		}
		var ip [4]byte
		binary.BigEndian.PutUint32(ip[:], binary.LittleEndian.Uint32(b))
		addr := netip.AddrFrom4(ip)
		if addr.IsUnspecified() {
			continue
		}
		return addr, true
	}
	return netip.Addr{}, false
}
