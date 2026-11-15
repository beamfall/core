package remoteaccess

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

// pipeNATPMP returns a NAT-PMP client wired to an in-memory gateway. serve
// gets each request datagram and returns the reply (nil = stay silent).
func pipeNATPMP(t *testing.T, serve func(req []byte) []byte) *natPMPGateway {
	t.Helper()
	return &natPMPGateway{
		dial: func(context.Context) (net.Conn, error) {
			client, server := net.Pipe()
			go func() {
				defer func() { _ = server.Close() }()
				buf := make([]byte, 64)
				for {
					n, err := server.Read(buf)
					if err != nil {
						return
					}
					if reply := serve(append([]byte(nil), buf[:n]...)); reply != nil {
						if _, err := server.Write(reply); err != nil {
							return
						}
					}
				}
			}()
			return client, nil
		},
		initialRTO: 20 * time.Millisecond,
		attempts:   4,
	}
}

func natPMPMapReply(result uint16, internal, external uint16, lifetime uint32) []byte {
	b := make([]byte, 16)
	b[1] = natPMPRespOffset + natPMPOpMapTCP
	binary.BigEndian.PutUint16(b[2:4], result)
	binary.BigEndian.PutUint32(b[4:8], 1234) // seconds since epoch
	binary.BigEndian.PutUint16(b[8:10], internal)
	binary.BigEndian.PutUint16(b[10:12], external)
	binary.BigEndian.PutUint32(b[12:16], lifetime)
	return b
}

func TestNATPMPAddMapping(t *testing.T) {
	var got []byte
	gw := pipeNATPMP(t, func(req []byte) []byte {
		got = req
		return natPMPMapReply(0, 8443, 18443, 3600)
	})
	ext, lease, err := gw.AddMapping(context.Background(), 8443, 8443, 2*time.Hour)
	if err != nil {
		t.Fatalf("AddMapping: %v", err)
	}
	want := []byte{0, 2, 0, 0, 0x20, 0xfb, 0x20, 0xfb, 0, 0, 0x1c, 0x20}
	if !bytes.Equal(got, want) {
		t.Fatalf("request = % x, want % x", got, want)
	}
	if ext != 18443 || lease != time.Hour {
		t.Fatalf("granted %d for %v", ext, lease)
	}
}

func TestNATPMPRefusal(t *testing.T) {
	gw := pipeNATPMP(t, func([]byte) []byte { return natPMPMapReply(2, 0, 0, 0) })
	_, _, err := gw.AddMapping(context.Background(), 8443, 8443, time.Hour)
	var refused *GatewayRefusedError
	if !errors.As(err, &refused) || refused.Code != 2 || refused.Protocol != PortMapProtocolNATPMP {
		t.Fatalf("err = %v, want refusal code 2", err)
	}
}

func TestNATPMPRetransmitsUntilAnswered(t *testing.T) {
	requests := 0
	gw := pipeNATPMP(t, func([]byte) []byte {
		requests++
		if requests < 3 {
			return nil
		}
		return natPMPMapReply(0, 8443, 8443, 7200)
	})
	if _, _, err := gw.AddMapping(context.Background(), 8443, 8443, 2*time.Hour); err != nil {
		t.Fatalf("AddMapping: %v", err)
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3", requests)
	}
}

func TestNATPMPSilentGatewayIsNoGateway(t *testing.T) {
	gw := pipeNATPMP(t, func([]byte) []byte { return nil })
	_, err := gw.ExternalAddress(context.Background())
	if !errors.Is(err, ErrNoGateway) {
		t.Fatalf("err = %v, want ErrNoGateway", err)
	}
}

func TestNATPMPIgnoresMismatchedReply(t *testing.T) {
	gw := pipeNATPMP(t, func(req []byte) []byte {
		if req[1] != natPMPOpExternal {
			return nil
		}
		// A reply to a different opcode first, then the right one would come
		// on retransmit; the stray must not be taken as the answer.
		return natPMPMapReply(0, 1, 1, 1)
	})
	if _, err := gw.ExternalAddress(context.Background()); !errors.Is(err, ErrNoGateway) {
		t.Fatalf("err = %v, want ErrNoGateway after only stray replies", err)
	}
}

func TestNATPMPDeleteMapping(t *testing.T) {
	var got []byte
	gw := pipeNATPMP(t, func(req []byte) []byte {
		got = req
		return natPMPMapReply(0, 8443, 0, 0)
	})
	if err := gw.DeleteMapping(context.Background(), 8443, 18443); err != nil {
		t.Fatalf("DeleteMapping: %v", err)
	}
	want := []byte{0, 2, 0, 0, 0x20, 0xfb, 0, 0, 0, 0, 0, 0}
	if !bytes.Equal(got, want) {
		t.Fatalf("request = % x, want % x", got, want)
	}
}

func TestNATPMPExternalAddress(t *testing.T) {
	gw := pipeNATPMP(t, func(req []byte) []byte {
		if !bytes.Equal(req, []byte{0, 0}) {
			t.Errorf("request = % x", req)
		}
		return []byte{0, 128, 0, 0, 0, 0, 0, 9, 203, 0, 113, 7}
	})
	addr, err := gw.ExternalAddress(context.Background())
	if err != nil || addr != netip.MustParseAddr("203.0.113.7") {
		t.Fatalf("addr = %v err = %v", addr, err)
	}
}

func TestNATPMPHonoursContext(t *testing.T) {
	gw := pipeNATPMP(t, func([]byte) []byte { return nil })
	gw.initialRTO = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := gw.ExternalAddress(ctx); err == nil {
		t.Fatal("silent gateway answered")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("ignored the context deadline: %v", elapsed)
	}
}
