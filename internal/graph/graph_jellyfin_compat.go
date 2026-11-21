package graph

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Jellyfin compatibility Console settings (ADR-0171 §2.2-§2.4, CORE-2683).
// The fields are registered only outside App Store builds
// (graph_jellyfin_compat_register.go); an App Store binary neither advertises
// nor dispatches them.

// JellyfinCompatController is the seam the app wires the compatibility
// settings controller into: it stores a change and applies it to the live
// route group.
type JellyfinCompatController interface {
	Settings(ctx context.Context) (store.JellyfinCompatConfig, error)
	Configure(ctx context.Context, patch store.JellyfinCompatPatch) (store.JellyfinCompatConfig, error)
}

// SetJellyfinCompatController wires the compatibility settings controller.
// Without one the fields report the settings unavailable.
func (h *Handler) SetJellyfinCompatController(c JellyfinCompatController) {
	h.jellyfinCompat = c
}

// JellyfinCompatInfo is the Console view of the compatibility settings.
type JellyfinCompatInfo struct {
	Enabled           bool                        `json:"enabled"`
	AllowedCIDRs      []string                    `json:"allowedCIDRs"`
	TrustedProxyCIDRs []string                    `json:"trustedProxyCIDRs"`
	Libraries         []JellyfinCompatLibraryInfo `json:"libraries"`
	Profiles          []JellyfinCompatProfileInfo `json:"profiles"`
}

// JellyfinCompatLibraryInfo is one Library an operator may share. Restricted
// Libraries never appear.
type JellyfinCompatLibraryInfo struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Allowed bool   `json:"allowed"`
}

// JellyfinCompatProfileInfo is one enabled adult Profile and whether it may
// sign in to compatibility tools.
type JellyfinCompatProfileInfo struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	IntegrationSignIn bool   `json:"integrationSignIn"`
}

func (h *Handler) jellyfinCompatInfo(ctx context.Context) (JellyfinCompatInfo, error) {
	if h.jellyfinCompat == nil {
		return JellyfinCompatInfo{}, clientError("compatibility settings are unavailable on this server")
	}
	cfg, err := h.jellyfinCompat.Settings(ctx)
	if err != nil {
		return JellyfinCompatInfo{}, err
	}
	return h.jellyfinCompatView(ctx, cfg)
}

func (h *Handler) jellyfinCompatView(ctx context.Context, cfg store.JellyfinCompatConfig) (JellyfinCompatInfo, error) {
	info := JellyfinCompatInfo{
		Enabled:           cfg.Enabled,
		AllowedCIDRs:      cfg.AllowedCIDRs,
		TrustedProxyCIDRs: cfg.TrustedProxyCIDRs,
		Libraries:         []JellyfinCompatLibraryInfo{},
		Profiles:          []JellyfinCompatProfileInfo{},
	}
	allowed := make(map[store.LibraryID]bool, len(cfg.LibraryIDs))
	for _, id := range cfg.LibraryIDs {
		allowed[id] = true
	}
	libraries, err := h.store.JellyfinCompatSelectableLibraries(ctx)
	if err != nil {
		return JellyfinCompatInfo{}, err
	}
	for _, library := range libraries {
		info.Libraries = append(info.Libraries, JellyfinCompatLibraryInfo{ID: string(library.ID), Title: library.Title, Allowed: allowed[library.ID]})
	}
	profiles, err := h.store.Profiles(ctx)
	if err != nil {
		return JellyfinCompatInfo{}, err
	}
	for _, profile := range profiles {
		if profile.Kind != store.ProfileAdult || profile.Disabled {
			continue
		}
		info.Profiles = append(info.Profiles, JellyfinCompatProfileInfo{ID: string(profile.ID), Name: profile.Name, IntegrationSignIn: profile.IntegrationSignIn})
	}
	return info, nil
}

func (h *Handler) setJellyfinCompat(ctx context.Context, scope auth.Scope, req request) (JellyfinCompatInfo, error) {
	if h.jellyfinCompat == nil {
		return JellyfinCompatInfo{}, clientError("compatibility settings are unavailable on this server")
	}
	v := newVars(req)
	var patch store.JellyfinCompatPatch
	if enabled, ok := v.Bool("enabled"); ok {
		patch.Enabled = &enabled
	}
	if cidrs, ok := v.StringList("allowedCIDRs", "strings"); ok {
		patch.AllowedCIDRs = &cidrs
	}
	if cidrs, ok := v.StringList("trustedProxyCIDRs", "strings"); ok {
		patch.TrustedProxyCIDRs = &cidrs
	}
	if ids, ok := v.StringList("libraryIDs", "strings"); ok {
		libraries := make([]store.LibraryID, len(ids))
		for i, id := range ids {
			libraries[i] = store.LibraryID(strings.TrimSpace(id))
		}
		patch.LibraryIDs = &libraries
	}
	if err := v.Err(); err != nil {
		return JellyfinCompatInfo{}, err
	}
	if patch == (store.JellyfinCompatPatch{}) {
		return JellyfinCompatInfo{}, clientError("setJellyfinCompat needs at least one setting to change")
	}
	cfg, err := h.jellyfinCompat.Configure(ctx, patch)
	if err != nil {
		return JellyfinCompatInfo{}, err
	}
	_ = h.logActivity(ctx, scope, store.ActivityConfigUpdate, "jellyfin-compat", "", "")
	return h.jellyfinCompatView(ctx, cfg)
}

func (h *Handler) setProfileIntegrationSignIn(ctx context.Context, scope auth.Scope, req request) (JellyfinCompatProfileInfo, error) {
	v := newVars(req)
	id, _ := v.String("profileID")
	enabled, hasEnabled := v.Bool("enabled")
	if err := v.Err(); err != nil {
		return JellyfinCompatProfileInfo{}, err
	}
	if !hasEnabled {
		// A missing flag must not read as "off": that would revoke sessions.
		return JellyfinCompatProfileInfo{}, clientError("enabled is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return JellyfinCompatProfileInfo{}, clientError("profileID is required")
	}
	profile, err := h.store.SetProfileIntegrationSignIn(ctx, store.ProfileID(id), enabled)
	if err != nil {
		return JellyfinCompatProfileInfo{}, err
	}
	_ = h.logActivity(ctx, scope, store.ActivityConfigUpdate, "profile-integration-sign-in", id, "")
	return JellyfinCompatProfileInfo{ID: string(profile.ID), Name: profile.Name, IntegrationSignIn: profile.IntegrationSignIn}, nil
}

// JellyfinCompatKeyInfo is the Console view of one durable integration key
// (ADR-0171 §2.4, CORE-2725). It never carries the token.
type JellyfinCompatKeyInfo struct {
	ID          string   `json:"id"`
	ProfileID   string   `json:"profileID"`
	ProfileName string   `json:"profileName"`
	Name        string   `json:"name"`
	Scopes      []string `json:"scopes"`
	CreatedAt   string   `json:"createdAt"`
	LastUsedAt  string   `json:"lastUsedAt,omitempty"`
	RevokedAt   string   `json:"revokedAt,omitempty"`
}

// CreatedJellyfinCompatKeyInfo carries the key's token exactly once, from the
// create mutation's response. It is never readable again.
type CreatedJellyfinCompatKeyInfo struct {
	Token string                `json:"token"`
	Key   JellyfinCompatKeyInfo `json:"key"`
}

// jellyfinCompatKeyScopes is the fixed authority of a minted key: catalog
// reads and library refresh, never the admin setup scope.
var jellyfinCompatKeyScopes = []store.IntegrationScope{store.IntegrationScopeRead, store.IntegrationScopeRefresh}

func (h *Handler) jellyfinCompatKeys(ctx context.Context) ([]JellyfinCompatKeyInfo, error) {
	keys, err := h.store.CompatIntegrationKeys(ctx)
	if err != nil {
		return nil, err
	}
	names, err := h.profileNames(ctx)
	if err != nil {
		return nil, err
	}
	infos := make([]JellyfinCompatKeyInfo, 0, len(keys))
	for _, key := range keys {
		infos = append(infos, jellyfinCompatKeyInfo(key, names[key.ProfileID]))
	}
	return infos, nil
}

func (h *Handler) createJellyfinCompatKey(ctx context.Context, scope auth.Scope, req request) (CreatedJellyfinCompatKeyInfo, error) {
	profileID := store.ProfileID(trimmedVariable(req, "profileID"))
	name := trimmedVariable(req, "name")
	if profileID == "" || name == "" {
		return CreatedJellyfinCompatKeyInfo{}, clientError("profileID and name are required")
	}
	// Read the names before minting: once the key commits, the response is the
	// only copy of its token, so nothing after the mint may fail the request.
	names, err := h.profileNames(ctx)
	if err != nil {
		return CreatedJellyfinCompatKeyInfo{}, err
	}
	key, token, err := h.store.CreateCompatIntegrationKey(ctx, profileID, name, jellyfinCompatKeyScopes, time.Now().UTC())
	if err != nil {
		return CreatedJellyfinCompatKeyInfo{}, compatKeyClientError(err)
	}
	if err := h.logActivity(ctx, scope, store.ActivityKeyCreate, "integration-key", key.ID, ""); err != nil {
		return CreatedJellyfinCompatKeyInfo{}, err
	}
	return CreatedJellyfinCompatKeyInfo{Token: token, Key: jellyfinCompatKeyInfo(key, names[key.ProfileID])}, nil
}

func (h *Handler) revokeJellyfinCompatKey(ctx context.Context, scope auth.Scope, req request) (JellyfinCompatKeyInfo, error) {
	id := trimmedVariable(req, "id")
	if id == "" {
		return JellyfinCompatKeyInfo{}, clientError("id is required")
	}
	key, err := h.store.RevokeCompatIntegrationKey(ctx, id, time.Now().UTC())
	if err != nil {
		return JellyfinCompatKeyInfo{}, compatKeyClientError(err)
	}
	if err := h.logActivity(ctx, scope, store.ActivityKeyRevoke, "integration-key", key.ID, ""); err != nil {
		return JellyfinCompatKeyInfo{}, err
	}
	names, err := h.profileNames(ctx)
	if err != nil {
		return JellyfinCompatKeyInfo{}, err
	}
	return jellyfinCompatKeyInfo(key, names[key.ProfileID]), nil
}

func (h *Handler) profileNames(ctx context.Context) (map[store.ProfileID]string, error) {
	profiles, err := h.store.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[store.ProfileID]string, len(profiles))
	for _, profile := range profiles {
		names[profile.ID] = profile.Name
	}
	return names, nil
}

func compatKeyClientError(err error) error {
	var verr store.ValidationError
	if errors.As(err, &verr) {
		return clientError(string(verr))
	}
	if errors.Is(err, store.ErrProfileDisabled) {
		return clientError("only an enabled adult profile can hold an integration key")
	}
	return err
}

func jellyfinCompatKeyInfo(key store.CompatIntegrationKey, profileName string) JellyfinCompatKeyInfo {
	scopes := make([]string, len(key.Scopes))
	for i, s := range key.Scopes {
		scopes[i] = string(s)
	}
	info := JellyfinCompatKeyInfo{
		ID: key.ID, ProfileID: string(key.ProfileID), ProfileName: profileName, Name: key.Name,
		Scopes: scopes, CreatedAt: key.CreatedAt.UTC().Format(time.RFC3339),
	}
	if !key.LastUsedAt.IsZero() {
		info.LastUsedAt = key.LastUsedAt.UTC().Format(time.RFC3339)
	}
	if key.Revoked() {
		info.RevokedAt = key.RevokedAt.UTC().Format(time.RFC3339)
	}
	return info
}
