package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/remoteaccess"
	"github.com/beamfall/core/internal/store"
)

// portMapCloseBound caps how long shutdown waits to remove a router mapping.
// A silent gateway must never hold the server up; a mapping left behind
// expires on its own lease.
const portMapCloseBound = 3 * time.Second

// portMapping bundles automatic router port mapping and the reachability
// self-test (CORE-1969). It implements graph.PortMappingController.
type portMapping struct {
	mapper    *remoteaccess.PortMapper
	responder *remoteaccess.ProbeResponder
	prober    *remoteaccess.ReachProber
	relay     *remoteaccess.RelayClient

	mu   sync.Mutex
	last *remoteaccess.ReachReport
}

// newPortMapping builds the mapper for the LAN port. It returns nil when the
// port is unknown (ephemeral ":0"), since there is nothing to forward. No
// network traffic happens until the operator opts in.
func newPortMapping(lanPort int, relay *remoteaccess.RelayClient, logger *slog.Logger) *portMapping {
	if lanPort <= 0 || lanPort > 65535 {
		return nil
	}
	responder := remoteaccess.NewProbeResponder()
	return &portMapping{
		mapper:    remoteaccess.NewPortMapper(remoteaccess.NewGatewayDiscoverer(), uint16(lanPort), logger),
		responder: responder,
		prober:    remoteaccess.NewReachProber(responder),
		relay:     relay,
	}
}

// run restores the persisted opt-in, maintains the mapping until ctx ends,
// then removes it within portMapCloseBound.
func (p *portMapping) run(ctx context.Context, st *store.Store, logger *slog.Logger) {
	enabled, err := st.PortMappingEnabled(ctx)
	if err != nil {
		logger.Warn("port mapping: read setting", "error", err)
	}
	if enabled {
		p.mapper.SetEnabled(ctx, true)
	}
	p.mapper.Run(ctx)
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), portMapCloseBound)
	defer cancel()
	if err := p.mapper.Close(closeCtx); err != nil {
		logger.Warn("port mapping: removal on shutdown", "error", err)
	}
}

func (p *portMapping) PortMapping(context.Context) graph.PortMappingStatus {
	return graphPortMappingStatus(p.mapper.Status())
}

func (p *portMapping) SetPortMapping(ctx context.Context, enabled bool) graph.PortMappingStatus {
	return graphPortMappingStatus(p.mapper.SetEnabled(ctx, enabled))
}

func (p *portMapping) CheckReachability(ctx context.Context) graph.ReachabilityReport {
	in := remoteaccess.ReachInputs{Mapping: p.mapper.Status()}
	if p.relay != nil {
		state, _ := p.relay.Status()
		in.RelayConfigured = true
		in.RelayConnected = state == remoteaccess.RelayConnected
		in.RelayPublicHost = p.relay.PublicHost()
	}
	report := p.prober.Check(ctx, in)
	p.mu.Lock()
	p.last = &report
	p.mu.Unlock()
	return graphReachabilityReport(report)
}

func (p *portMapping) LastReachability() (graph.ReachabilityReport, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.last == nil {
		return graph.ReachabilityReport{}, false
	}
	return graphReachabilityReport(*p.last), true
}

func graphPortMappingStatus(s remoteaccess.PortMapStatus) graph.PortMappingStatus {
	out := graph.PortMappingStatus{
		Enabled:      s.Enabled,
		State:        string(s.State),
		Protocol:     s.Protocol,
		InternalPort: int(s.InternalPort),
		ExternalPort: int(s.ExternalPort),
		LeaseSeconds: int(s.Lease / time.Second),
		Reason:       s.Reason,
		GatewayCode:  s.GatewayCode,
	}
	if s.ExternalAddress.IsValid() {
		out.ExternalAddress = s.ExternalAddress.Unmap().String()
	}
	if !s.ExpiresAt.IsZero() {
		out.ExpiresAt = s.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return out
}

func graphReachabilityReport(r remoteaccess.ReachReport) graph.ReachabilityReport {
	verdict := func(v remoteaccess.ReachVerdict) graph.ReachabilityVerdict {
		return graph.ReachabilityVerdict{Path: v.Path, Reachable: v.Reachable, Method: v.Method, Reason: v.Reason}
	}
	return graph.ReachabilityReport{
		CheckedAt: r.CheckedAt.UTC().Format(time.RFC3339),
		Verdicts:  []graph.ReachabilityVerdict{verdict(r.Direct), verdict(r.Relay)},
	}
}
