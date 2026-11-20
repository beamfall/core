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
// LibraryIDs is the stored allowlist as written; JellyfinCompatEligibleLibraries
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
	// Resolve first and return the merged result, so a successful write always
	// hands the caller the config it stored: no read after the commit can fail
	// and leave the live route group behind the stored settings.
	cfg, err := s.JellyfinCompatConfig(ctx, defaults)
	if err != nil {
		return JellyfinCompatConfig{}, err
	}
	writes := map[string]string{}
	if patch.Enabled != nil {
		writes[configJellyfinCompatEnabled] = boolToConfig(*patch.Enabled)
		cfg.Enabled = *patch.Enabled
	}
	for _, list := range []struct {
		key    string
		values *[]string
		into   *[]string
	}{
		{configJellyfinCompatAllowedCIDRs, patch.AllowedCIDRs, &cfg.AllowedCIDRs},
		{configJellyfinCompatTrustedProxies, patch.TrustedProxyCIDRs, &cfg.TrustedProxyCIDRs},
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
		*list.into = normalized
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
		cfg.LibraryIDs = ids
	}
	if err := s.setServerConfigValues(ctx, writes, now); err != nil {
		return JellyfinCompatConfig{}, err
	}
	return cfg, nil
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

// JellyfinCompatEligibleLibraries returns the Libraries a compatibility tool
// may act on now: on the stored allowlist, live, and not Reveal-restricted. It
// is read at use time, once per request, so restricting or trashing a Library
// after it was allowlisted takes it out of reach at once.
func (s *Store) JellyfinCompatEligibleLibraries(ctx context.Context) (map[LibraryID]bool, error) {
	cfg, err := s.JellyfinCompatConfig(ctx, JellyfinCompatConfig{})
	if err != nil {
		return nil, err
	}
	selectable, err := s.compatSelectableLibraries(ctx)
	if err != nil {
		return nil, err
	}
	eligible := make(map[LibraryID]bool, len(cfg.LibraryIDs))
	for _, id := range cfg.LibraryIDs {
		if selectable[id] {
			eligible[id] = true
		}
	}
	return eligible, nil
}

// SetProfileIntegrationSignIn turns a Profile's integration sign-in opt-in on
// or off (ADR-0171 §2.4). Only an adult, enabled Profile may opt in. Turning it
// off revokes the Profile's integration sessions in the same transaction. The
// write touches only the opt-in column and re-checks eligibility in its WHERE
// clause, so a concurrent Profile edit is neither overwritten nor raced.
func (s *Store) SetProfileIntegrationSignIn(ctx context.Context, id ProfileID, enabled bool) (Profile, error) {
	if id == "" {
		return Profile{}, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Profile{}, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return Profile{}, err
	}
	defer tx.Rollback()
	query := `UPDATE profile SET integration_sign_in = 0 WHERE id = ?`
	args := []any{id}
	if enabled {
		query = `UPDATE profile SET integration_sign_in = 1 WHERE id = ? AND kind = ? AND disabled = 0`
		args = append(args, ProfileAdult)
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return Profile{}, err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return Profile{}, err
	} else if changed == 0 {
		if _, err := s.profileSQL(ctx, id); err != nil {
			return Profile{}, err
		}
		return Profile{}, ValidationError("only an enabled adult profile can sign in to compatibility tools")
	}
	if !enabled {
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_session WHERE profile_id = ? AND kind = ?`, id, SessionKindIntegration); err != nil {
			return Profile{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Profile{}, err
	}
	return s.profileSQL(ctx, id)
}
