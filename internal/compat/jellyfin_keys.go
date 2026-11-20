package compat

import (
	"encoding/json"
	"net"
	"net/http"

	"github.com/beamfall/core/internal/store"
)

// The Auth/Keys routes let a setup wizard (Seerr) finish without minting a
// credential: ADR-0171 §2.4 forbids standing access derived from the admin
// password. Only the bounded admin setup session may call them, and the one
// key listed is the caller's own setup token, which expires with that session.

// createAPIKey accepts a key request from the setup session and creates nothing.
func (h *Handler) createAPIKey(w http.ResponseWriter, r *http.Request, _ net.IP) {
	if _, ok := h.integrationScope(w, r, integrationToken(r.Header), store.IntegrationScopeSetup); !ok {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listAPIKeys lists the setup session's own token, named after the Client
// parameter of the caller's MediaBrowser authorization header.
func (h *Handler) listAPIKeys(w http.ResponseWriter, r *http.Request, _ net.IP) {
	token := integrationToken(r.Header)
	if _, ok := h.integrationScope(w, r, token, store.IntegrationScopeSetup); !ok {
		return
	}
	type apiKey struct {
		AccessToken string `json:"AccessToken"`
		AppName     string `json:"AppName"`
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"Items":            []apiKey{{AccessToken: token, AppName: mediaBrowserClient(r.Header, token)}},
		"TotalRecordCount": 1,
		"StartIndex":       0,
	})
}

// mediaBrowserClient returns the Client parameter of the MediaBrowser
// authorization header that carries token, or else of the first one that
// names a Client.
func mediaBrowserClient(header http.Header, token string) string {
	names := []string{"X-Emby-Authorization", "Authorization"}
	for _, name := range names {
		if value := header.Get(name); mediaBrowserToken(value) == token {
			if client := mediaBrowserValue(value, "Client"); client != "" {
				return client
			}
		}
	}
	for _, name := range names {
		if client := mediaBrowserValue(header.Get(name), "Client"); client != "" {
			return client
		}
	}
	return ""
}
