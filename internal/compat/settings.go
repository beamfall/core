package compat

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/beamfall/core/internal/store"
)

// SettingsController keeps the stored compatibility settings and the live
// route group in step (CORE-2683). Writes go to the store first; the handler
// takes the resolved result, so the next request follows the operator's
// change without a restart.
type SettingsController struct {
	mu       sync.Mutex
	store    *store.Store
	defaults store.JellyfinCompatConfig
	handler  *Handler
}

// NewSettingsController reads the stored settings, with the bootstrap config
// as the fallback for every key never written, and applies them to handler.
// Bootstrap CIDRs must lie in the same local trust zones a Console write is
// held to (ADR-0171 §2.4); anything else refuses start-up rather than serving
// a wider network than the Console could ever configure.
func NewSettingsController(ctx context.Context, st *store.Store, defaults store.JellyfinCompatConfig, handler *Handler) (*SettingsController, error) {
	for _, cidr := range slices.Concat(defaults.AllowedCIDRs, defaults.TrustedProxyCIDRs) {
		if _, err := store.NormalizeCompatTrustZoneCIDR(cidr); err != nil {
			return nil, fmt.Errorf("jellyfin compatibility bootstrap: %w", err)
		}
	}
	c := &SettingsController{store: st, defaults: defaults, handler: handler}
	cfg, err := c.Settings(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.apply(cfg); err != nil {
		return nil, err
	}
	return c, nil
}

// Settings returns the resolved compatibility settings.
func (c *SettingsController) Settings(ctx context.Context) (store.JellyfinCompatConfig, error) {
	return c.store.JellyfinCompatConfig(ctx, c.defaults)
}

// Configure validates and stores a partial change, then applies the result to
// the live route group.
func (c *SettingsController) Configure(ctx context.Context, patch store.JellyfinCompatPatch) (store.JellyfinCompatConfig, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cfg, err := c.store.SetJellyfinCompatConfig(ctx, patch, c.defaults, time.Now().UTC())
	if err != nil {
		return store.JellyfinCompatConfig{}, err
	}
	return cfg, c.apply(cfg)
}

func (c *SettingsController) apply(cfg store.JellyfinCompatConfig) error {
	return c.handler.Apply(Settings{Enabled: cfg.Enabled, AllowedCIDRs: cfg.AllowedCIDRs, TrustedProxyCIDRs: cfg.TrustedProxyCIDRs})
}
