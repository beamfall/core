package plugin

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/beamfall/plugin-sdk/catalogindex"
)

const (
	CatalogURL             = "https://registry.beamfall.com/catalog-index.v1.json"
	catalogResponseMaxSize = 1 << 20
)

var (
	ErrCatalogUnavailable = errors.New("plugin catalog unavailable")
	ErrCatalogCorrupt     = errors.New("plugin catalog integrity failure")
	ErrCatalogRollback    = errors.New("plugin catalog sequence rollback")
)

type CatalogAvailability struct {
	Entries []catalogindex.Entry
	Stale   bool
}

type CatalogConsumer struct {
	cachePath string
	roots     PluginTrustRoots
	client    *http.Client
}

func NewCatalogConsumer(cachePath string, roots PluginTrustRoots) *CatalogConsumer {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &CatalogConsumer{
		cachePath: cachePath,
		roots:     roots,
		client: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (c *CatalogConsumer) Check(ctx context.Context, installed []ID) (CatalogAvailability, error) {
	cachedRaw, cached, cacheErr := c.loadCache()
	candidateRaw, err := c.fetch(ctx)
	if err != nil {
		return filterCatalog(cached, installed, cachedRaw != nil), errors.Join(cacheErr, err)
	}
	candidate, err := catalogindex.ParseAndVerify(candidateRaw, c.registryKeys(), 0)
	if err != nil {
		return filterCatalog(cached, installed, cachedRaw != nil), errors.Join(cacheErr, fmt.Errorf("%w: %v", ErrCatalogCorrupt, err))
	}
	if cachedRaw != nil && candidate.Sequence < cached.Sequence {
		return filterCatalog(cached, installed, true), ErrCatalogRollback
	}
	if cachedRaw != nil && candidate.Sequence == cached.Sequence {
		return filterCatalog(cached, installed, false), cacheErr
	}
	if err := writeStateFile(c.cachePath, candidateRaw); err != nil {
		return filterCatalog(cached, installed, cachedRaw != nil), errors.Join(cacheErr, fmt.Errorf("cache plugin catalog: %w", err))
	}
	return filterCatalog(candidate, installed, false), cacheErr
}

func (c *CatalogConsumer) loadCache() ([]byte, catalogindex.Index, error) {
	raw, err := os.ReadFile(c.cachePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, catalogindex.Index{}, nil
	}
	if err != nil {
		return nil, catalogindex.Index{}, fmt.Errorf("read plugin catalog cache: %w", err)
	}
	index, err := catalogindex.ParseAndVerify(raw, c.registryKeys(), 0)
	if err != nil {
		return nil, catalogindex.Index{}, fmt.Errorf("%w: cached index: %v", ErrCatalogCorrupt, err)
	}
	return raw, index, nil
}

func (c *CatalogConsumer) fetch(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CatalogURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCatalogUnavailable, err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCatalogUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP status %d", ErrCatalogUnavailable, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, catalogResponseMaxSize+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read response: %v", ErrCatalogUnavailable, err)
	}
	if len(raw) > catalogResponseMaxSize {
		return nil, fmt.Errorf("%w: response exceeds %d bytes", ErrCatalogCorrupt, catalogResponseMaxSize)
	}
	return raw, nil
}

func filterCatalog(index catalogindex.Index, installed []ID, stale bool) CatalogAvailability {
	ids := make(map[string]struct{}, len(installed))
	for _, id := range installed {
		ids[string(id)] = struct{}{}
	}
	return CatalogAvailability{Entries: catalogindex.Filter(index, ids), Stale: stale}
}

// registryKeys projects the release-bundled registry trust roots onto the
// verification-key map the SDK catalog verifier consumes.
func (c *CatalogConsumer) registryKeys() map[string]ed25519.PublicKey {
	keys := make(map[string]ed25519.PublicKey, len(c.roots.Registries))
	for id, root := range c.roots.Registries {
		keys[id] = root.PublicKey
	}
	return keys
}
