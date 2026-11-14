package graph

import (
	"context"
	"encoding/json"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// MetadataAttributionInfo is the credit a metadata source's terms require,
// declared by the plugin scraper that supplied the data (CORE-1126). Text and
// URL are plugin data; Core carries no source names. ProviderName is the
// supplying scraper's display name and is set only on a Media read.
type MetadataAttributionInfo struct {
	ProviderName plugin.LocaleString `json:"providerName,omitempty"`
	Text         plugin.LocaleString `json:"text"`
	URL          string              `json:"url"`
}

func scraperAttributionInfo(scraper plugin.ScraperSpec) *MetadataAttributionInfo {
	if scraper.Attribution == nil {
		return nil
	}
	return &MetadataAttributionInfo{Text: scraper.Attribution.Text, URL: scraper.Attribution.URL}
}

// mediaMetadataAttribution resolves the attribution of the scraper that last
// identified a Media item from the ACTIVE bundle. No provenance, an inactive
// plugin, a retired scraper or a scraper without an attribution all yield nil:
// attribution decorates the read, it never gates it.
func (h *Handler) mediaMetadataAttribution(ctx context.Context, mediaID store.MediaID) *MetadataAttributionInfo {
	provenance, ok, err := h.store.MediaMetadataProvenance(ctx, mediaID)
	if err != nil || !ok {
		return nil
	}
	bundle, ok := h.plugins.Bundle(plugin.ID(provenance.PluginID))
	if !ok {
		return nil
	}
	for _, scraper := range bundle.Scrapers {
		if scraper.ID != provenance.ScraperID {
			continue
		}
		info := scraperAttributionInfo(scraper)
		if info != nil {
			info.ProviderName = scraper.DisplayName
		}
		return info
	}
	return nil
}

// metadataCredits lists the attribution of every active plugin scraper that
// declares one, in activation then declaration order, each identical text and
// link pair once, so a viewer credits surface can honour every source's terms.
func (h *Handler) metadataCredits(context.Context, auth.Scope, request) ([]MetadataAttributionInfo, error) {
	credits := []MetadataAttributionInfo{}
	seen := map[string]bool{}
	for _, id := range h.plugins.IDs() {
		bundle, ok := h.plugins.Bundle(id)
		if !ok {
			continue
		}
		for _, scraper := range bundle.Scrapers {
			info := scraperAttributionInfo(scraper)
			if info == nil {
				continue
			}
			key, err := json.Marshal(info)
			if err != nil {
				return nil, err
			}
			if seen[string(key)] {
				continue
			}
			seen[string(key)] = true
			credits = append(credits, *info)
		}
	}
	return credits, nil
}
