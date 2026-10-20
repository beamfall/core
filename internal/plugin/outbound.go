package plugin

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ReservedOutboundPolicyConfigKey is the Core-owned plugin-config key used to
// persist the manifest declaration/operator approval intersection. Plugin
// manifests cannot claim it as an ordinary configuration field.
const ReservedOutboundPolicyConfigKey = "core-outbound-policy"

// OutboundDeclaration is the manifest-owned list of HTTPS origins an
// Integration Plugin may ask the Host API to contact. Origins are data, not
// executable policy: the Integration Host still requires an approved grant and
// applies its SSRF gate on every request and redirect.
type OutboundDeclaration struct {
	PluginID ID
	Origins  []string
}

// OutboundAllowlist is the immutable, activated source of plugin-declared
// outbound origins. It validates and normalizes manifest claims without a
// dependency from this package back to the Integration Host.
type OutboundAllowlist struct {
	byPlugin map[ID][]string
}

// OutboundPolicy is the server-owned policy for one plugin. Declared contains
// the manifest's claims; Approved is the operator-approved subset. Both contain
// normalized exact origins. The transport allows only origins present in both.
type OutboundPolicy struct {
	PluginID ID       `json:"pluginID"`
	Declared []string `json:"declared"`
	Approved []string `json:"approved"`
}

// NewOutboundAllowlist validates and owns the declarations. An origin is exact:
// scheme + hostname + optional explicit port. Paths, queries, fragments,
// credentials, literal IPs, duplicates, and non-HTTPS schemes are refused so a
// manifest cannot smuggle request-specific data into the policy source.
func NewOutboundAllowlist(declarations []OutboundDeclaration) (*OutboundAllowlist, error) {
	a := &OutboundAllowlist{byPlugin: make(map[ID][]string, len(declarations))}
	for _, declaration := range declarations {
		pluginID := string(declaration.PluginID)
		if !slugPattern.MatchString(pluginID) {
			return nil, fmt.Errorf("plugin outbound declaration has invalid plugin ID %q", pluginID)
		}
		if _, exists := a.byPlugin[declaration.PluginID]; exists {
			return nil, fmt.Errorf("duplicate outbound declaration for plugin %q", pluginID)
		}
		seen := make(map[string]bool, len(declaration.Origins))
		origins := make([]string, 0, len(declaration.Origins))
		for _, raw := range declaration.Origins {
			origin, err := NormalizeOutboundOrigin(raw)
			if err != nil {
				return nil, fmt.Errorf("plugin %s outbound origin: %w", pluginID, err)
			}
			if seen[origin] {
				return nil, fmt.Errorf("plugin %s has duplicate outbound origin %q", pluginID, origin)
			}
			seen[origin] = true
			origins = append(origins, origin)
		}
		a.byPlugin[declaration.PluginID] = origins
	}
	return a, nil
}

// DeclaredOutbound returns a defensive copy of pluginID's exact declared
// origins. An undeclared plugin returns an empty list and therefore denies all.
func (a *OutboundAllowlist) DeclaredOutbound(_ context.Context, pluginID string) ([]string, error) {
	if a == nil {
		return nil, nil
	}
	return append([]string(nil), a.byPlugin[ID(pluginID)]...), nil
}

// NewOutboundPolicy validates and owns one declaration plus its operator
// approvals. An approval outside the declaration is rejected: plugin claim and
// operator consent are independent requirements.
func NewOutboundPolicy(declaration OutboundDeclaration, approvals []string) (OutboundPolicy, error) {
	allowlist, err := NewOutboundAllowlist([]OutboundDeclaration{declaration})
	if err != nil {
		return OutboundPolicy{}, err
	}
	declared, err := allowlist.DeclaredOutbound(context.Background(), string(declaration.PluginID))
	if err != nil {
		return OutboundPolicy{}, err
	}
	declaredSet := make(map[string]bool, len(declared))
	for _, origin := range declared {
		declaredSet[origin] = true
	}
	approvedSet := make(map[string]bool, len(approvals))
	for _, raw := range approvals {
		origin, err := NormalizeOutboundOrigin(raw)
		if err != nil {
			return OutboundPolicy{}, fmt.Errorf("plugin %s outbound approval: %w", declaration.PluginID, err)
		}
		if !declaredSet[origin] {
			return OutboundPolicy{}, fmt.Errorf("plugin %s outbound origin %q is approved but not declared", declaration.PluginID, origin)
		}
		if approvedSet[origin] {
			return OutboundPolicy{}, fmt.Errorf("plugin %s has duplicate outbound approval %q", declaration.PluginID, origin)
		}
		approvedSet[origin] = true
	}
	approved := make([]string, 0, len(approvedSet))
	for _, origin := range declared {
		if approvedSet[origin] {
			approved = append(approved, origin)
		}
	}
	return OutboundPolicy{
		PluginID: declaration.PluginID,
		Declared: declared,
		Approved: approved,
	}, nil
}

// Reconcile installs a new manifest declaration set while retaining approval
// only for unchanged origins. Removed origins lose approval; newly declared
// origins default to denied.
func (p OutboundPolicy) Reconcile(declaration OutboundDeclaration) (OutboundPolicy, error) {
	allowlist, err := NewOutboundAllowlist([]OutboundDeclaration{declaration})
	if err != nil {
		return OutboundPolicy{}, err
	}
	declared, err := allowlist.DeclaredOutbound(context.Background(), string(declaration.PluginID))
	if err != nil {
		return OutboundPolicy{}, err
	}
	declaredSet := make(map[string]bool, len(declared))
	for _, origin := range declared {
		declaredSet[origin] = true
	}
	retained := make([]string, 0, len(p.Approved))
	for _, origin := range p.Approved {
		if declaredSet[origin] {
			retained = append(retained, origin)
		}
	}
	return NewOutboundPolicy(declaration, retained)
}

// Approve returns a policy with one exact declared origin operator-approved.
func (p OutboundPolicy) Approve(raw string) (OutboundPolicy, error) {
	origin, err := NormalizeOutboundOrigin(raw)
	if err != nil {
		return OutboundPolicy{}, err
	}
	if !contains(p.Declared, origin) {
		return OutboundPolicy{}, fmt.Errorf("plugin %s outbound origin %q is not declared", p.PluginID, origin)
	}
	if contains(p.Approved, origin) {
		return p, nil
	}
	approved := append(append([]string(nil), p.Approved...), origin)
	return NewOutboundPolicy(OutboundDeclaration{PluginID: p.PluginID, Origins: p.Declared}, approved)
}

// Revoke returns a policy with one origin no longer approved.
func (p OutboundPolicy) Revoke(raw string) (OutboundPolicy, error) {
	origin, err := NormalizeOutboundOrigin(raw)
	if err != nil {
		return OutboundPolicy{}, err
	}
	approved := make([]string, 0, len(p.Approved))
	for _, current := range p.Approved {
		if current != origin {
			approved = append(approved, current)
		}
	}
	return NewOutboundPolicy(OutboundDeclaration{PluginID: p.PluginID, Origins: p.Declared}, approved)
}

// Allows reports whether an exact origin is both declared and operator-approved.
func (p OutboundPolicy) Allows(raw string) bool {
	origin, err := NormalizeOutboundOrigin(raw)
	if err != nil {
		return false
	}
	return contains(p.Declared, origin) && contains(p.Approved, origin)
}

// NormalizeOutboundOrigin validates and canonicalizes one declared origin.
func NormalizeOutboundOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid origin %q", raw)
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return "", fmt.Errorf("origin %q must use https", raw)
	}
	if parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("origin %q must contain only scheme and host", raw)
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" || net.ParseIP(hostname) != nil {
		return "", fmt.Errorf("origin %q must use a hostname, not a literal IP", raw)
	}
	port := parsed.Port()
	if port == "443" {
		port = ""
	}
	host := hostname
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	}
	return "https://" + host, nil
}
