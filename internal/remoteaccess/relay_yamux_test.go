package remoteaccess

import (
	"testing"
	"time"

	"github.com/beamfall/core/internal/config"
)

// relayNodeYamux holds the beamfall/relay carrier's yamux settings from its
// internal/protocol/yamux.go (protocol.YamuxConfig and
// DefaultYamuxStreamWindowBytes). The relay's
// TestDefaultYamuxStreamWindowSustainsTarget and
// TestAbruptYamuxCloseTimeoutIsBounded pin the same values on that side;
// change both repositories together.
var relayNodeYamux = struct {
	streamWindowBytes      uint32
	streamCloseTimeout     time.Duration
	connectionWriteTimeout time.Duration
	keepAliveInterval      time.Duration
}{
	streamWindowBytes:      4 * 1024 * 1024,
	streamCloseTimeout:     30 * time.Second,
	connectionWriteTimeout: 30 * time.Second,
	keepAliveInterval:      30 * time.Second,
}

// TestRelayYamuxConfigMatchesRelay (CORE-1386): core's carrier config must
// agree with the relay's. Core used to hardcode a 1 MiB window, capping relayed
// uploads near 10 MB/s at 50 ms, and left StreamCloseTimeout at yamux's five
// minutes against the relay's 30s.
func TestRelayYamuxConfigMatchesRelay(t *testing.T) {
	cfg := relayYamuxConfig(0)
	if cfg.MaxStreamWindowSize != relayNodeYamux.streamWindowBytes {
		t.Errorf("MaxStreamWindowSize = %d, want relay default %d", cfg.MaxStreamWindowSize, relayNodeYamux.streamWindowBytes)
	}
	if cfg.StreamCloseTimeout != relayNodeYamux.streamCloseTimeout {
		t.Errorf("StreamCloseTimeout = %v, want relay %v", cfg.StreamCloseTimeout, relayNodeYamux.streamCloseTimeout)
	}
	if cfg.ConnectionWriteTimeout != relayNodeYamux.connectionWriteTimeout {
		t.Errorf("ConnectionWriteTimeout = %v, want relay %v", cfg.ConnectionWriteTimeout, relayNodeYamux.connectionWriteTimeout)
	}
	if !cfg.EnableKeepAlive || cfg.KeepAliveInterval != relayNodeYamux.keepAliveInterval {
		t.Errorf("keepalive = %v every %v, want on every %v", cfg.EnableKeepAlive, cfg.KeepAliveInterval, relayNodeYamux.keepAliveInterval)
	}
}

// TestRelayYamuxConfigHonorsOverride (CORE-1386): an operator window reaches
// the carrier session, so core can match a relay running a non-default
// --yamux-stream-window-bytes.
func TestRelayYamuxConfigHonorsOverride(t *testing.T) {
	if got := relayYamuxConfig(8 << 20).MaxStreamWindowSize; got != 8<<20 {
		t.Fatalf("override window = %d, want %d", got, 8<<20)
	}
	if got := relayYamuxConfig(config.MinRelayYamuxStreamWindowBytes).MaxStreamWindowSize; got != config.MinRelayYamuxStreamWindowBytes {
		t.Fatalf("minimum window = %d, want %d", got, config.MinRelayYamuxStreamWindowBytes)
	}
}
