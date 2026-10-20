package config

import (
	"path/filepath"
	"testing"
)

// feature:direct-https-acme — direct-HTTPS/ACME config resolves from flags and
// gates on accepted terms.
func TestRemoteAccessDirectHTTPSDefaultsOff(t *testing.T) {
	cfg := loadForTest(t, nil)
	if cfg.RemoteAccess.DirectHTTPSEnabled() {
		t.Fatal("direct HTTPS enabled with no public domain configured")
	}
	if cfg.RemoteAccess.RelayConfigured() {
		t.Fatal("relay configured with no endpoint")
	}
}

// feature:direct-https-acme — a public domain WITHOUT accepted terms must not
// enable direct HTTPS: autocert would refuse to register, so the honest state
// is "not enabled, awaiting terms" rather than a silent auto-accept.
func TestRemoteAccessDirectHTTPSRequiresTerms(t *testing.T) {
	cfg := loadForTest(t, map[string]string{
		"BEAMFALL_PUBLIC_DOMAIN": "media.example.com",
	})
	if cfg.RemoteAccess.DirectHTTPSEnabled() {
		t.Fatal("direct HTTPS enabled without accepted ACME terms")
	}
}

// feature:direct-https-acme — domain + accepted terms enables the path and
// defaults the ACME cache dir under the data dir (0700 created at use time).
func TestRemoteAccessDirectHTTPSEnabledWithTerms(t *testing.T) {
	dataDir := t.TempDir()
	cfg := loadForTest(t, map[string]string{
		"BEAMFALL_DATA_DIR":            dataDir,
		"BEAMFALL_PUBLIC_DOMAIN":       "media.example.com",
		"BEAMFALL_ACME_TERMS_ACCEPTED": "true",
	})
	if !cfg.RemoteAccess.DirectHTTPSEnabled() {
		t.Fatal("direct HTTPS not enabled with domain + accepted terms")
	}
	wantCache := filepath.Join(dataDir, "acme")
	if got := cfg.RemoteAccess.DirectHTTPS.ACMECacheDir; got != wantCache {
		t.Fatalf("ACMECacheDir = %q, want default %q", got, wantCache)
	}
}

// feature:self-hosted-relay — an endpoint marks the relay configured; the
// routed public host defaults to the direct-HTTPS public domain.
func TestRemoteAccessRelayDefaultsPublicHostToDomain(t *testing.T) {
	cfg := loadForTest(t, map[string]string{
		"BEAMFALL_PUBLIC_DOMAIN":  "media.example.com",
		"BEAMFALL_RELAY_ENDPOINT": "relay.example.com:7443",
	})
	if !cfg.RemoteAccess.RelayConfigured() {
		t.Fatal("relay not configured with endpoint set")
	}
	if got := cfg.RemoteAccess.Relay.PublicHost; got != "media.example.com" {
		t.Fatalf("relay PublicHost = %q, want default to public domain", got)
	}
}

// feature:self-hosted-relay — an explicit relay public host wins over the
// public-domain default (the relay may route a different SNI than direct).
func TestRemoteAccessRelayExplicitPublicHost(t *testing.T) {
	cfg := loadForTest(t, map[string]string{
		"BEAMFALL_PUBLIC_DOMAIN":     "media.example.com",
		"BEAMFALL_RELAY_ENDPOINT":    "relay.example.com:7443",
		"BEAMFALL_RELAY_PUBLIC_HOST": "remote.example.com",
	})
	if got := cfg.RemoteAccess.Relay.PublicHost; got != "remote.example.com" {
		t.Fatalf("relay PublicHost = %q, want explicit value", got)
	}
}

// feature:self-hosted-relay — operators can pin a private relay carrier CA.
func TestRemoteAccessRelayCAFile(t *testing.T) {
	cfg := loadForTest(t, map[string]string{
		"BEAMFALL_RELAY_CA": "/etc/beamfall-relay/carrier-ca.pem",
	})
	if got := cfg.RemoteAccess.Relay.CAFile; got != "/etc/beamfall-relay/carrier-ca.pem" {
		t.Fatalf("relay CAFile = %q, want env value", got)
	}
}

func TestRemoteAccessRelayTransport(t *testing.T) {
	t.Run("defaults to tcp", func(t *testing.T) {
		cfg := loadForTest(t, nil)
		if got := cfg.RemoteAccess.Relay.Transport; got != RelayTransportTCP {
			t.Fatalf("relay transport = %q, want %q", got, RelayTransportTCP)
		}
	})

	t.Run("quic from environment", func(t *testing.T) {
		cfg := loadForTest(t, map[string]string{"BEAMFALL_RELAY_TRANSPORT": RelayTransportQUIC})
		if got := cfg.RemoteAccess.Relay.Transport; got != RelayTransportQUIC {
			t.Fatalf("relay transport = %q, want %q", got, RelayTransportQUIC)
		}
	})

	t.Run("rejects unknown", func(t *testing.T) {
		env := map[string]string{
			"BEAMFALL_DATA_DIR":        t.TempDir(),
			"BEAMFALL_RELAY_TRANSPORT": "udp",
		}
		if _, err := Load(nil, func(name string) (string, bool) { v, ok := env[name]; return v, ok }); err == nil {
			t.Fatal("Load accepted an unknown relay transport")
		}
	})
}

func TestRemoteAccessFlagsTrimWhitespace(t *testing.T) {
	cfg, err := Load([]string{
		"--data-dir", t.TempDir(),
		"--public-domain", "  media.example.com  ",
		"--relay-endpoint", "  relay.example.com:7443  ",
		"--relay-public-host", "  remote.example.com  ",
		"--relay-ca", "  /etc/beamfall-relay/carrier-ca.pem  ",
		"--relay-transport", "  quic  ",
	}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got := cfg.RemoteAccess.DirectHTTPS.PublicDomain; got != "media.example.com" {
		t.Fatalf("PublicDomain = %q", got)
	}
	if got := cfg.RemoteAccess.Relay.Endpoint; got != "relay.example.com:7443" {
		t.Fatalf("Relay.Endpoint = %q", got)
	}
	if got := cfg.RemoteAccess.Relay.PublicHost; got != "remote.example.com" {
		t.Fatalf("Relay.PublicHost = %q", got)
	}
	if got := cfg.RemoteAccess.Relay.CAFile; got != "/etc/beamfall-relay/carrier-ca.pem" {
		t.Fatalf("Relay.CAFile = %q", got)
	}
	if got := cfg.RemoteAccess.Relay.Transport; got != RelayTransportQUIC {
		t.Fatalf("Relay.Transport = %q", got)
	}
}

// loadForTest loads a Config from a fixed env map, supplying a fresh data dir
// (so the stream-token key write does not touch the repo) when the caller did
// not set one.
func loadForTest(t *testing.T, env map[string]string) Config {
	t.Helper()
	if env == nil {
		env = map[string]string{}
	}
	if _, ok := env["BEAMFALL_DATA_DIR"]; !ok {
		env["BEAMFALL_DATA_DIR"] = t.TempDir()
	}
	cfg, err := Load(nil, func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	return cfg
}
