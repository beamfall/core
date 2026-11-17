package remoteaccess

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/config"
)

// TestCarryForRelayLatencyHarness is not a test of its own: it runs this
// package's RelayClient as the home carrier for the relay repository's latency
// harness (beamfall-relay script/netem-latency.sh with CARRIER_CMD, see its
// docs/relay-latency-budget.md), so the control-stream figure is measured with
// core's client sending the media (CORE-2282). It is skipped unless
// CARRIER_DIR names the directory the harness writes.
//
// The harness writes relay-token (the shared secret), relay-ca.pem and, last,
// carrier.env with RELAY_ENDPOINT, BACKEND_ADDR and PUBLIC_HOST. The client
// dials with its production path. CARRIER_UNBOUNDED=1 dials without the unsent
// backlog bound, for the "before" figure.
func TestCarryForRelayLatencyHarness(t *testing.T) {
	dir := os.Getenv("CARRIER_DIR")
	if dir == "" {
		t.Skip("CARRIER_DIR is not set; this only runs under the relay latency harness")
	}
	env := waitForCarrierEnv(t, dir)

	client, err := NewRelayClient(config.RemoteAccessConfig{Relay: config.RelayConfig{
		Endpoint:   env["RELAY_ENDPOINT"],
		PublicHost: env["PUBLIC_HOST"],
		CAFile:     filepath.Join(dir, "relay-ca.pem"),
		Transport:  config.RelayTransportTCP,
	}}, dir)
	if err != nil || client == nil {
		t.Fatalf("NewRelayClient: client=%v err=%v", client, err)
	}
	if os.Getenv("CARRIER_UNBOUNDED") == "1" {
		client.dial = func(ctx context.Context, endpoint string) (net.Conn, error) {
			roots, err := loadRelayRoots(filepath.Join(dir, "relay-ca.pem"))
			if err != nil {
				return nil, err
			}
			tlsConfig, err := relayTLSConfig(endpoint, roots, false)
			if err != nil {
				return nil, err
			}
			return (&tls.Dialer{Config: tlsConfig}).DialContext(ctx, "tcp", endpoint)
		}
	}
	client.SetBackendAddr(env["BACKEND_ADDR"])

	session, err := client.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	// Carry returns when the harness finishes and its relay closes the carrier.
	t.Logf("carrier ended: %v", client.Carry(context.Background(), session))
}

func waitForCarrierEnv(t *testing.T, dir string) map[string]string {
	t.Helper()
	path := filepath.Join(dir, "carrier.env")
	for deadline := time.Now().Add(time.Minute); ; {
		data, err := os.ReadFile(path) // #nosec G304 G703 -- the harness's own scratch directory.
		if err == nil {
			env := map[string]string{}
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				key, value, _ := strings.Cut(line, "=")
				env[key] = value
			}
			return env
		}
		if time.Now().After(deadline) {
			t.Fatalf("no carrier.env in CARRIER_DIR after a minute: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
