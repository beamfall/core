// Package discovery advertises a Beamfall server on the local network.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/brutella/dnssd"
)

const (
	serviceType = "_beamfall._tcp"
	domain      = "local."
)

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
func (a *Advertiser) Start(name string, port int) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("discovery instance name is required")
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("discovery port %d is out of range", port)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.server != nil {
		return nil
	}
	server, err := a.register(name, serviceType, domain, port, txtRecords())
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

func txtRecords() []string {
	return []string{"version", "path=/"}
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

// registerDNSSD advertises the service via github.com/brutella/dnssd. It
// preserves the current service-type/domain/port advertisement; TXT records
// are carried as the same key set as before ("version", "path=/"), each
// rendered as "key=value" because dnssd's Service.Text is a map and always
// serializes bare keys with an (empty) value — no Beamfall client parses the
// "version" value today, so this is wire-compatible for discovery purposes.
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
