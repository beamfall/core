package graph

import (
	"context"

	"github.com/beamfall/core/internal/auth"
)

// Remote access (ADR-0012 §1 direct HTTPS + ACME; ADR-0018/0025 self-hosted
// relay carrier), graph half. These fields are manager-gated operator Console
// status/config: they expose the configured public domain, certificate state,
// relay endpoint, and carrier connection state — never any secret. The carrier
// token is never read here (it lives in a 0600 file, resolved only at dial
// time), and no relayed bytes ever reach this surface (ADR-0018 byte-blindness).

// RemoteAccessProvider is the consumer-defined seam the app wires the
// remoteaccess package into. Nil (the default) means remote access is honestly
// unavailable: the resolver reports both paths disabled rather than guessing.
type RemoteAccessProvider interface {
	// DirectHTTPS reports the direct-HTTPS/ACME status. ok is false when the
	// path is not enabled (LAN-only HTTP).
	DirectHTTPS(ctx context.Context) (info DirectHTTPSStatus, ok bool)
	// Relay reports the self-hosted relay carrier status. ok is false when no
	// relay endpoint is configured.
	Relay(ctx context.Context) (info RelayStatus, ok bool)
	// Endpoints returns the ordered remote-access URL set in the canonical path
	// order (LAN → direct → overlay → self-hosted-relay → hosted; ADR-0012 §1 /
	// 0018 §3 / 0025 §3, SEC-10). A LAN-only server returns only its LAN URL(s).
	// The list is data only — a machine-readable connection contract clients
	// select from (relay kinds last), never UI. It carries no secret (no carrier
	// token, no relayed bytes — byte-blindness).
	Endpoints(ctx context.Context) []RemoteEndpoint
}

// RemoteEndpoint is one reachable base URL plus the path kind it represents
// (ADR-0012 §1 dual-URL connection model). kind is named by SHAPE — one of the
// closed SEC-10 vocab "lan", "direct", "overlay", "self-hosted-relay", "hosted"
// — never by domain (invariant #1). Clients try candidates in emitted order,
// relay last.
type RemoteEndpoint struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

// ConnectionEndpoints is the authenticated-device endpoint-discovery
// contract. Version is the producer-owned wire version; Endpoints is ordered
// by connection preference. Keeping this envelope separate from
// RemoteAccessInfo prevents device sessions from observing manager-only remote
// access configuration and status.
type ConnectionEndpoints struct {
	Version   string           `json:"version"`
	Endpoints []RemoteEndpoint `json:"endpoints"`
}

// ConnectionEndpointsVersion is the released producer contract consumed by
// native clients. Additive changes bump the minor version; breaking changes
// require a new major and a dual-publish window.
const ConnectionEndpointsVersion = "1.0.0"

// DirectHTTPSStatus is the operator-Console view of the direct-HTTPS/ACME path.
type DirectHTTPSStatus struct {
	// Enabled is always true when present (ok=true); it lets the client render
	// a single status object with an explicit flag.
	Enabled bool `json:"enabled"`
	// Domain is the public FQDN ACME issues a certificate for.
	Domain string `json:"domain"`
	// CertState is the honest certificate state: "pending" (enabled, no cert
	// cached yet — issued on first inbound handshake) or "issued".
	CertState string `json:"certState"`
}

// RelayStatus is the operator-Console view of the self-hosted relay carrier
// path. It carries no token and no relayed-connection detail.
type RelayStatus struct {
	Enabled bool `json:"enabled"`
	// Endpoint is the relay carrier address the server dials out to.
	Endpoint string `json:"endpoint"`
	// PublicHost is the SNI host the relay routes inbound connections by.
	PublicHost string `json:"publicHost"`
	// ConnectionState is the honest carrier state: "disabled", "disconnected",
	// "connecting", "connected", or "error".
	ConnectionState string `json:"connectionState"`
	// LastError is the last dial/registration failure reason, token-free. Empty
	// unless ConnectionState is "error".
	LastError string `json:"lastError,omitempty"`
}

// RemoteAccessInfo is the combined manager-Console remote-access view: the two
// independent path-status objects (each present only when configured) plus the
// ordered Endpoints set clients select from.
type RemoteAccessInfo struct {
	DirectHTTPS *DirectHTTPSStatus `json:"directHTTPS,omitempty"`
	Relay       *RelayStatus       `json:"relay,omitempty"`
	// PlainHTTPPublicWarning is true for this Console request when it reached
	// the plain-HTTP listener from a non-LAN source.
	PlainHTTPPublicWarning bool `json:"plainHTTPPublicWarning"`
	// Endpoints is the ordered remote-access URL set in the canonical path order
	// (LAN → direct → overlay → self-hosted-relay → hosted; ADR-0012 §1 dual-URL
	// model, SEC-10). Always present (a LAN-only server returns just its LAN URL);
	// data only, never UI; relay kinds last.
	Endpoints []RemoteEndpoint `json:"endpoints"`
}

const plainHTTPPublicWarningContextKey = "beamfall.plainHTTPPublicWarning"

func plainHTTPPublicWarning(ctx context.Context) bool {
	warning, _ := ctx.Value(plainHTTPPublicWarningContextKey).(bool)
	return warning
}

// SetRemoteAccess wires the remote-access status provider. Nil (the default)
// leaves the remoteAccess field reporting both paths disabled — honest for a
// LAN-only server.
func (h *Handler) SetRemoteAccess(provider RemoteAccessProvider) {
	h.remoteAccess = provider
}

// remoteAccessInfo resolves the manager-Console remote-access status. With no
// provider wired, both paths are absent (a LAN-only server). The provider's ok
// flags decide which paths are present — a disabled path is omitted, never
// faked.
func (h *Handler) remoteAccessInfo(ctx context.Context) RemoteAccessInfo {
	info := RemoteAccessInfo{PlainHTTPPublicWarning: plainHTTPPublicWarning(ctx)}
	if h.remoteAccess == nil {
		return info
	}
	if direct, ok := h.remoteAccess.DirectHTTPS(ctx); ok {
		direct.Enabled = true
		info.DirectHTTPS = &direct
	}
	if relay, ok := h.remoteAccess.Relay(ctx); ok {
		relay.Enabled = true
		info.Relay = &relay
	}
	info.Endpoints = h.remoteAccess.Endpoints(ctx)
	return info
}

// connectionEndpoints resolves the refreshable, secret-free connection
// contract for any authenticated device. Authentication is enforced by the
// shared scoped-field gate before execute calls this resolver.
func (h *Handler) connectionEndpoints(ctx context.Context) ConnectionEndpoints {
	result := ConnectionEndpoints{
		Version:   ConnectionEndpointsVersion,
		Endpoints: make([]RemoteEndpoint, 0),
	}
	if h.remoteAccess != nil {
		result.Endpoints = append(result.Endpoints, h.remoteAccess.Endpoints(ctx)...)
	}
	return result
}

// resolveRemoteAccess is the dispatch entry for the manager-gated
// remoteAccess field. It is called from execute() under the manager-role gate.
func (h *Handler) resolveRemoteAccess(ctx context.Context, scope auth.Scope) (RemoteAccessInfo, error) {
	return h.remoteAccessInfo(ctx), nil
}
