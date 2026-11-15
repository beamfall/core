// Package discovery advertises a Beamfall server on the local network.
package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/brutella/dnssd"
)

const (
	serviceType = "_beamfall._tcp"
	domain      = "local."

	// serverIDDomain separates the discovery id derivation from every other
	// use of the server key pin.
	serverIDDomain = "beamfall-discovery-server-id/v1\x00"
	serverIDBytes  = 16
)

// Record is the per-server data carried in the TXT record next to the fixed
// "version" and "path=/" entries.
type Record struct {
	// Scheme is what the advertised port answers to a LAN client: "https"
	// when direct TLS is on, otherwise "http".
	Scheme string
	// ServerID is a stable, public identifier for this server. It is not a
	// credential and is not key material; see ServerID.
	ServerID string
}

// ServerID derives the discovery id from the server's public key pin. The id
// is a one-way, domain-separated hash of public data, so it stays stable for
// as long as the server keeps its identity key and reveals nothing a LAN peer
// could not already read from the TLS handshake.
func ServerID(serverKeyPin string) string {
	sum := sha256.Sum256([]byte(serverIDDomain + serverKeyPin))
	return hex.EncodeToString(sum[:serverIDBytes])
}

type registration interface {
	Shutdown()
}

type registerFunc func(instance, service, domain string, port int, text []string) (registration, error)

// Advertiser owns one mDNS/DNS-SD registration.
type Advertiser struct {
	mu       sync.Mutex
	register registerFunc
	server   registration
}

// New returns an advertiser that uses github.com/brutella/dnssd.
func New() *Advertiser {
	return &Advertiser{register: registerDNSSD}
}

func newWithRegister(register registerFunc) *Advertiser {
	return &Advertiser{register: register}
}

// Start advertises the Beamfall HTTP endpoint. Calling Start while already
// started is a no-op, so callers can pair it freely with Stop during shutdown.
func (a *Advertiser) Start(name string, port int, rec Record) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("discovery instance name is required")
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("discovery port %d is out of range", port)
	}
	if rec.Scheme != "http" && rec.Scheme != "https" {
		return fmt.Errorf("discovery scheme %q must be http or https", rec.Scheme)
	}
	if !validServerID(rec.ServerID) {
		return errors.New("discovery server id must be 1 to 64 lowercase hex characters")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.server != nil {
		return nil
	}
	server, err := a.register(name, serviceType, domain, port, txtRecords(rec))
	if err != nil {
		return err
	}
	a.server = server
	return nil
}

// Stop deregisters the service. It is safe to call more than once.
func (a *Advertiser) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.server == nil {
		return
	}
	a.server.Shutdown()
	a.server = nil
}

// txtRecords never carries setup state: advertising that a server is
// unclaimed would tell everyone on the LAN it is open to be claimed.
func txtRecords(rec Record) []string {
	return []string{"version", "path=/", "scheme=" + rec.Scheme, "id=" + rec.ServerID}
}

func validServerID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// dnssdRegistration adapts brutella/dnssd's context-driven responder to the
// Shutdown-based registration seam the rest of this package expects.
type dnssdRegistration struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (r *dnssdRegistration) Shutdown() {
	r.cancel()
	<-r.done
}

// registerDNSSD advertises the service via github.com/brutella/dnssd. TXT
// entries are rendered as "key=value" because dnssd's Service.Text is a map
// and always serializes bare keys with an (empty) value — no Beamfall client
// parses the "version" value today, so this is wire-compatible for discovery
// purposes.
func registerDNSSD(instance, service, dom string, port int, text []string) (registration, error) {
	cfg := dnssd.Config{
		Name:   instance,
		Type:   service,
		Domain: strings.TrimSuffix(dom, "."),
		Port:   port,
		Text:   textMap(text),
	}
	svc, err := dnssd.NewService(cfg)
	if err != nil {
		return nil, err
	}

	responder, err := dnssd.NewResponder()
	if err != nil {
		return nil, err
	}
	handle, err := responder.Add(svc)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = responder.Respond(ctx)
	}()

	return &dnssdRegistration{
		cancel: func() {
			cancel()
			responder.Remove(handle)
		},
		done: done,
	}, nil
}

// textMap converts the package's raw TXT-string list ("key" or "key=value")
// into brutella/dnssd's map[string]string TXT representation.
func textMap(text []string) map[string]string {
	m := make(map[string]string, len(text))
	for _, entry := range text {
		key, value, _ := strings.Cut(entry, "=")
		m[key] = value
	}
	return m
}
