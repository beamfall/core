package graph

import (
	"context"
	"strings"

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
	enabled, _ := v.Bool("enabled")
	if err := v.Err(); err != nil {
		return JellyfinCompatProfileInfo{}, err
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
