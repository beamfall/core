package app

import (
	"context"
	"crypto/tls"
	"io"
	"testing"
	"time"

	"github.com/beamfall/core/internal/hostmetrics"
)

// A relayed client reaches the server through the carrier's loopback listener,
// not the bound port, so that listener must feed the Console network graph.
func TestRelayBackendListenerCountsRelayedTraffic(t *testing.T) {
	cert, _ := appRelayCert(t)
	metrics := hostmetrics.New(10*time.Millisecond, time.Minute)
	ln, err := relayBackendListener(metrics, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go metrics.Run(ctx)
	// Rates are measured from the first sample on, so let it land first.
	for start := time.Now(); len(metrics.Samples()) == 0; time.Sleep(5 * time.Millisecond) {
		if time.Since(start) > 5*time.Second {
			t.Fatal("no first host sample")
		}
	}

	served := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			served <- err
			return
		}
		defer func() { _ = conn.Close() }()
		if _, err := io.ReadFull(conn, make([]byte, 5)); err != nil {
			served <- err
			return
		}
		_, err = conn.Write(make([]byte, 64<<10))
		served <- err
	}()

	client, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{InsecureSkipVerify: true}) // #nosec G402 -- loopback test peer with a throwaway certificate.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if _, err := client.Write([]byte("ping!")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(client, make([]byte, 64<<10)); err != nil {
		t.Fatal(err)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var sent, received float64
		for _, s := range metrics.Samples() {
			sent += s.SentBytesPerSecond
			received += s.ReceivedBytesPerSecond
		}
		if sent > 0 && received > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("relayed traffic never reached the host network samples")
}
