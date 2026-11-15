package remoteaccess

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"
)

// NAT-PMP client (RFC 6886). Requests go over UDP to the gateway's port 5351.
// The transport is a dial function so tests exchange datagrams over an
// in-memory pipe.

const (
	natPMPPort        = 5351
	natPMPVersion     = 0
	natPMPOpExternal  = 0
	natPMPOpMapTCP    = 2
	natPMPRespOffset  = 128
	natPMPInitialRTO  = 250 * time.Millisecond
	natPMPMaxAttempts = 6 // 250ms doubling: about 16s before giving up (RFC 6886 §3.1 allows 9)
)

type natPMPGateway struct {
	dial       func(ctx context.Context) (net.Conn, error)
	initialRTO time.Duration
	attempts   int
}

// newNATPMPGateway is a NAT-PMP client for the gateway at addr.
func newNATPMPGateway(addr netip.Addr) *natPMPGateway {
	target := netip.AddrPortFrom(addr, natPMPPort).String()
	return &natPMPGateway{
		dial: func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp4", target)
		},
		initialRTO: natPMPInitialRTO,
		attempts:   natPMPMaxAttempts,
	}
}

func (g *natPMPGateway) Protocol() string { return PortMapProtocolNATPMP }

func (g *natPMPGateway) ExternalAddress(ctx context.Context) (netip.Addr, error) {
	resp, err := g.roundTrip(ctx, []byte{natPMPVersion, natPMPOpExternal}, natPMPOpExternal, 12)
	if err != nil {
		return netip.Addr{}, err
	}
	addr, _ := netip.AddrFromSlice(resp[8:12])
	return addr, nil
}

func (g *natPMPGateway) AddMapping(ctx context.Context, internalPort, externalPort uint16, lifetime time.Duration) (uint16, time.Duration, error) {
	if lifetime <= 0 || lifetime > portMapRequestedLease {
		// NAT-PMP has no permanent lease (lifetime 0 means delete), and the
		// mapper never asks for more than the requested lease.
		lifetime = portMapRequestedLease
	}
	secs := uint32(lifetime / time.Second) // #nosec G115 -- bounded to portMapRequestedLease above

	resp, err := g.roundTrip(ctx, natPMPMapRequest(internalPort, externalPort, secs), natPMPOpMapTCP, 16)
	if err != nil {
		return 0, 0, err
	}
	if got := binary.BigEndian.Uint16(resp[8:10]); got != internalPort {
		return 0, 0, fmt.Errorf("remoteaccess: nat-pmp answered for internal port %d, asked %d", got, internalPort)
	}
	mapped := binary.BigEndian.Uint16(resp[10:12])
	granted := time.Duration(binary.BigEndian.Uint32(resp[12:16])) * time.Second
	if mapped == 0 || granted == 0 {
		return 0, 0, errors.New("remoteaccess: nat-pmp granted an empty mapping")
	}
	return mapped, granted, nil
}

func (g *natPMPGateway) DeleteMapping(ctx context.Context, internalPort, _ uint16) error {
	// RFC 6886 §3.4: delete is a map request with external port 0 and
	// lifetime 0.
	_, err := g.roundTrip(ctx, natPMPMapRequest(internalPort, 0, 0), natPMPOpMapTCP, 16)
	return err
}

func natPMPMapRequest(internalPort, externalPort uint16, lifetimeSecs uint32) []byte {
	req := make([]byte, 12)
	req[0] = natPMPVersion
	req[1] = natPMPOpMapTCP
	binary.BigEndian.PutUint16(req[4:6], internalPort)
	binary.BigEndian.PutUint16(req[6:8], externalPort)
	binary.BigEndian.PutUint32(req[8:12], lifetimeSecs)
	return req
}

// roundTrip sends req with RFC 6886 retransmission (initial 250ms, doubling)
// until a matching response arrives, ctx ends, or the attempts run out.
func (g *natPMPGateway) roundTrip(ctx context.Context, req []byte, op byte, wantLen int) ([]byte, error) {
	conn, err := g.dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: nat-pmp dial: %v", ErrNoGateway, err)
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	buf := make([]byte, 64)
	rto := g.initialRTO
	for attempt := 0; attempt < g.attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(rto)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, err
		}
		if _, err := conn.Write(req); err != nil {
			if isTimeout(err) {
				rto *= 2
				continue
			}
			return nil, fmt.Errorf("%w: nat-pmp send: %v", ErrNoGateway, err)
		}
		for {
			n, err := conn.Read(buf)
			if err != nil {
				if isTimeout(err) {
					break
				}
				return nil, fmt.Errorf("%w: nat-pmp receive: %v", ErrNoGateway, err)
			}
			resp, ok, perr := parseNATPMPResponse(buf[:n], op, wantLen)
			if perr != nil {
				return nil, perr
			}
			if ok {
				return resp, nil
			}
			// A stray or mismatched datagram: keep listening until the deadline.
		}
		rto *= 2
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%w: nat-pmp gateway did not answer", ErrNoGateway)
}

// parseNATPMPResponse checks the version and opcode, then the result code.
// ok is false for a datagram that answers some other request.
func parseNATPMPResponse(b []byte, op byte, wantLen int) ([]byte, bool, error) {
	if len(b) < 4 || b[0] != natPMPVersion || b[1] != natPMPRespOffset+op {
		return nil, false, nil
	}
	if code := binary.BigEndian.Uint16(b[2:4]); code != 0 {
		return nil, false, &GatewayRefusedError{Protocol: PortMapProtocolNATPMP, Code: int(code), Reason: natPMPResultText(code)}
	}
	if len(b) < wantLen {
		return nil, false, fmt.Errorf("remoteaccess: nat-pmp response is %d bytes, want %d", len(b), wantLen)
	}
	return b[:wantLen], true, nil
}

func natPMPResultText(code uint16) string {
	switch code {
	case 1:
		return "unsupported version"
	case 2:
		return "not authorized or refused"
	case 3:
		return "network failure"
	case 4:
		return "out of resources"
	case 5:
		return "unsupported opcode"
	default:
		return "unknown result"
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
