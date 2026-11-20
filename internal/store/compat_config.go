package store

import (
	"context"
	"encoding/json"
	"net"
	"slices"
	"strings"
	"time"
)

// Jellyfin compatibility settings (ADR-0171 §2.2-§2.4, CORE-2683). The Console
// writes them at runtime; a key never written falls back to the bootstrap
// config the caller passes as defaults, so a deployment that set the bootstrap
// flags keeps its behaviour until an operator changes it in the Console.
const (
	configJellyfinCompatEnabled        = "compat.jellyfin.enabled"
	configJellyfinCompatAllowedCIDRs   = "compat.jellyfin.allowed_cidrs"
	configJellyfinCompatTrustedProxies = "compat.jellyfin.trusted_proxy_cidrs"
	configJellyfinCompatLibraries      = "compat.jellyfin.libraries"
)

// JellyfinCompatConfig is the resolved compatibility configuration.
// LibraryIDs is the stored allowlist as written; JellyfinCompatLibraryEligible
// decides eligibility at use time.
type JellyfinCompatConfig struct {
	Enabled           bool
	AllowedCIDRs      []string
	TrustedProxyCIDRs []string
	LibraryIDs        []LibraryID
}

// JellyfinCompatPatch is a partial write: a nil field leaves the stored value
// (or the bootstrap fallback) unchanged.
type JellyfinCompatPatch struct {
	Enabled           *bool
	AllowedCIDRs      *[]string
	TrustedProxyCIDRs *[]string
	LibraryIDs        *[]LibraryID
}

// compatTrustZones are the only address ranges a compatibility CIDR may sit
// in: loopback, RFC 1918, link-local and IPv6 unique-local. The overlay range
// 100.64.0.0/10 and public space are outside every zone (ADR-0171 §2.4).
var compatTrustZones = func() []*net.IPNet {
	zones := []string{
		"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16",
		"::1/128", "fc00::/7", "fe80::/10",
	}
	parsed := make([]*net.IPNet, 0, len(zones))
	for _, zone := range zones {
		_, network, err := net.ParseCIDR(zone)
		if err != nil {
			panic(err)
		}
		parsed = append(parsed, network)
	}
	return parsed
}()

// NormalizeCompatTrustZoneCIDR parses a CIDR and returns its canonical network
// form when the whole range lies inside one trust zone. Anything else is a
// ValidationError.
func NormalizeCompatTrustZoneCIDR(value string) (string, error) {
	value = strings.TrimSpace(value)
	_, network, err := net.ParseCIDR(value)
	if err != nil {
		return "", ValidationError("compatibility network " + quoteForError(value) + " is not a CIDR range")
	}
	ones, bits := network.Mask.Size()
	for _, zone := range compatTrustZones {
		zoneOnes, zoneBits := zone.Mask.Size()
		if bits == zoneBits && ones >= zoneOnes && zone.Contains(network.IP) {
			return network.String(), nil
		}
	}
	return "", ValidationError("compatibility network " + network.String() + " is outside the local trust zones (loopback, private, link-local or unique-local)")
}

func quoteForError(value string) string {
	if len(value) > 64 {
		value = value[:64]
	}
	return "\"" + value + "\""
}

// JellyfinCompatConfig reads the compatibility settings, falling back to
// defaults for every key never written.
func (s *Store) JellyfinCompatConfig(ctx context.Context, defaults JellyfinCompatConfig) (JellyfinCompatConfig, error) {
	values, err := s.serverConfigValues(ctx)
	if err != nil {
		return JellyfinCompatConfig{}, err
	}
	cfg := JellyfinCompatConfig{
		Enabled:           defaults.Enabled,
		AllowedCIDRs:      slices.Clone(defaults.AllowedCIDRs),
		TrustedProxyCIDRs: slices.Clone(defaults.TrustedProxyCIDRs),
		LibraryIDs:        slices.Clone(defaults.LibraryIDs),
	}
	if raw, ok := values[configJellyfinCompatEnabled]; ok {
		cfg.Enabled = raw == "1"
	}
	decodeList(values, configJellyfinCompatAllowedCIDRs, &cfg.AllowedCIDRs)
	decodeList(values, configJellyfinCompatTrustedProxies, &cfg.TrustedProxyCIDRs)
	decodeList(values, configJellyfinCompatLibraries, &cfg.LibraryIDs)
	if cfg.AllowedCIDRs == nil {
		cfg.AllowedCIDRs = []string{}
	}
	if cfg.TrustedProxyCIDRs == nil {
		cfg.TrustedProxyCIDRs = []string{}
	}
	if cfg.LibraryIDs == nil {
		cfg.LibraryIDs = []LibraryID{}
	}
	return cfg, nil
}

func decodeList[T any](values map[string]string, key string, into *[]T) {
	raw, ok := values[key]
	if !ok {
		return
	}
	var list []T
	if json.Unmarshal([]byte(raw), &list) == nil {
		*into = list
	}
}

// SetJellyfinCompatConfig validates and applies a partial write, then returns
// the resolved config. CIDRs must lie in a trust zone, and every allowlisted
// Library must exist, not be trashed and not be Reveal-restricted. Any invalid
// value refuses the whole write.
func (s *Store) SetJellyfinCompatConfig(ctx context.Context, patch JellyfinCompatPatch, defaults JellyfinCompatConfig, now time.Time) (JellyfinCompatConfig, error) {
	writes := map[string]string{}
	if patch.Enabled != nil {
		writes[configJellyfinCompatEnabled] = boolToConfig(*patch.Enabled)
	}
	for _, list := range []struct {
		key    string
		values *[]string
	}{
		{configJellyfinCompatAllowedCIDRs, patch.AllowedCIDRs},
		{configJellyfinCompatTrustedProxies, patch.TrustedProxyCIDRs},
	} {
		if list.values == nil {
			continue
		}
		normalized := []string{}
		for _, value := range *list.values {
			cidr, err := NormalizeCompatTrustZoneCIDR(value)
			if err != nil {
				return JellyfinCompatConfig{}, err
			}
			if !slices.Contains(normalized, cidr) {
				normalized = append(normalized, cidr)
			}
		}
		encoded, err := json.Marshal(normalized)
		if err != nil {
			return JellyfinCompatConfig{}, err
		}
		writes[list.key] = string(encoded)
	}
	if patch.LibraryIDs != nil {
		eligible, err := s.compatSelectableLibraries(ctx)
		if err != nil {
			return JellyfinCompatConfig{}, err
		}
		ids := []LibraryID{}
		for _, id := range *patch.LibraryIDs {
			if !eligible[id] {
				return JellyfinCompatConfig{}, ValidationError("library " + quoteForError(string(id)) + " cannot be shared with compatibility tools")
			}
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		encoded, err := json.Marshal(ids)
		if err != nil {
			return JellyfinCompatConfig{}, err
		}
		writes[configJellyfinCompatLibraries] = string(encoded)
	}
	if err := s.setServerConfigValues(ctx, writes, now); err != nil {
		return JellyfinCompatConfig{}, err
	}
	return s.JellyfinCompatConfig(ctx, defaults)
}

// JellyfinCompatSelectableLibraries lists the Libraries an operator may put on
// the compatibility allowlist: every live Library that is not
// Reveal-restricted. Restricted Libraries never appear.
func (s *Store) JellyfinCompatSelectableLibraries(ctx context.Context) ([]Library, error) {
	return s.Libraries(ctx, LibraryFilter{AllLibraries: true})
}

func (s *Store) compatSelectableLibraries(ctx context.Context) (map[LibraryID]bool, error) {
	libraries, err := s.JellyfinCompatSelectableLibraries(ctx)
	if err != nil {
		return nil, err
	}
	eligible := make(map[LibraryID]bool, len(libraries))
	for _, library := range libraries {
		eligible[library.ID] = true
	}
	return eligible, nil
}

// JellyfinCompatLibraryEligible reports whether a compatibility tool may act on
// the Library now: it must be on the stored allowlist, live, and not
// Reveal-restricted. It is checked at use time, so restricting or trashing a
// Library after it was allowlisted takes it out of reach at once.
func (s *Store) JellyfinCompatLibraryEligible(ctx context.Context, id LibraryID) (bool, error) {
	cfg, err := s.JellyfinCompatConfig(ctx, JellyfinCompatConfig{})
	if err != nil || !slices.Contains(cfg.LibraryIDs, id) {
		return false, err
	}
	eligible, err := s.compatSelectableLibraries(ctx)
	if err != nil {
		return false, err
	}
	return eligible[id], nil
}

// SetProfileIntegrationSignIn turns a Profile's integration sign-in opt-in on
// or off (ADR-0171 §2.4). Only an adult, enabled Profile may opt in. Turning it
// off revokes the Profile's integration sessions.
func (s *Store) SetProfileIntegrationSignIn(ctx context.Context, id ProfileID, enabled bool) (Profile, error) {
	if id == "" {
		return Profile{}, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Profile{}, err
	}
	profile, err := s.profileSQL(ctx, id)
	if err != nil {
		return Profile{}, err
	}
	if enabled && (profile.Kind != ProfileAdult || profile.Disabled) {
		return Profile{}, ValidationError("only an enabled adult profile can sign in to compatibility tools")
	}
	profile.IntegrationSignIn = enabled
	if err := s.updateProfileSettingsSQL(ctx, profile); err != nil {
		return Profile{}, err
	}
	return s.profileSQL(ctx, id)
}
