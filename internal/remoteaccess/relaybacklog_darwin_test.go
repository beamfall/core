package remoteaccess

import (
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// feature:self-hosted-relay — the dialled carrier writes through the bound
// below TLS, and a relay that never reads leaves the writer held with about
// relayCarrierUnsentLowat unsent. An unbounded writer keeps going until the
// send buffer is full, which autotuning puts far higher (CORE-2282).
func TestDialBoundedTLSHoldsWriter(t *testing.T) {
	carrier, _ := dialTestCarrier(t)
	bound, ok := carrier.NetConn().(*unsentBoundConn)
	if !ok {
		t.Fatalf("connection below TLS is %T", carrier.NetConn())
	}
	const chunk = 4 << 10
	var written atomic.Int64
	go func() {
		buf := make([]byte, chunk)
		for {
			n, err := carrier.Write(buf)
			written.Add(int64(n))
			if err != nil {
				return
			}
		}
	}()

	// Well inside unsentMaxWait, after which one more write is let through.
	time.Sleep(unsentMaxWait / 4)
	before := written.Load()
	time.Sleep(unsentMaxWait / 4)
	if after := written.Load(); after != before {
		t.Fatalf("writer was not held: %d more bytes accepted", after-before)
	}
	unsent, ok := bound.unsent()
	if !ok {
		t.Fatal("no connection info")
	}
	// One TLS record of chunk bytes plus its framing may pass the check.
	if limit := int64(relayCarrierUnsentLowat + 2*chunk); unsent > limit {
		t.Fatalf("%d bytes unsent, want at most %d", unsent, limit)
	}
	if unsent <= 0 {
		t.Fatalf("%d bytes unsent: the relay's window never filled", unsent)
	}
}

// feature:self-hosted-relay — a write deadline ends the bound's wait: the held
// write returns the timeout near the deadline, not after unsentMaxWait.
func TestBoundCarrierWriteHonoursDeadline(t *testing.T) {
	carrier, _ := dialTestCarrier(t)
	bound := carrier.NetConn().(*unsentBoundConn)
	buf := make([]byte, 64<<10)
	const deadline = 300 * time.Millisecond
	start := time.Now()
	if err := bound.SetWriteDeadline(start.Add(deadline)); err != nil {
		t.Fatal(err)
	}
	var err error
	for err == nil {
		_, err = bound.Write(buf)
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("write ended with %v, want a deadline error", err)
	}
	if took := time.Since(start); took > deadline+unsentMaxWait/4 {
		t.Fatalf("write returned after %v, want about %v", took, deadline)
	}
}
