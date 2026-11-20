package compat

import (
	"encoding/json"
	"net"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/store"
)

const refreshInterval = time.Minute

type publicSystemInfo struct {
	ID                     string `json:"Id"`
	ProductName            string `json:"ProductName"`
	Version                string `json:"Version"`
	ServerName             string `json:"ServerName,omitempty"`
	OperatingSystem        string `json:"OperatingSystem"`
	StartupWizardCompleted bool   `json:"StartupWizardCompleted"`
}

type systemInfo struct {
	publicSystemInfo
	SystemArchitecture string `json:"SystemArchitecture"`
	CanSelfRestart     bool   `json:"CanSelfRestart"`
}

func (h *Handler) identity() publicSystemInfo {
	return publicSystemInfo{
		ID: h.serverID, ProductName: "Beamfall", Version: capabilities.ReleaseVersion,
		ServerName: h.serverName, OperatingSystem: runtime.GOOS,
		StartupWizardCompleted: h.setupRequired == nil || !h.setupRequired(),
	}
}

func (h *Handler) publicSystemInfo(w http.ResponseWriter, _ *http.Request, _ net.IP) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.identity())
}

func (h *Handler) systemInfo(w http.ResponseWriter, r *http.Request, _ net.IP) {
	if _, ok := h.integrationScope(w, r, integrationToken(r.Header), store.IntegrationScopeRead); !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(systemInfo{
		publicSystemInfo:   h.identity(),
		SystemArchitecture: runtime.GOARCH,
	})
}

func (h *Handler) refreshLibrary(w http.ResponseWriter, r *http.Request, _ net.IP) {
	scope, ok := h.integrationScope(w, r, integrationToken(r.Header), store.IntegrationScopeRefresh)
	if !ok {
		return
	}
	if h.enqueueRefresh != nil && h.allowRefresh() {
		h.enqueueRefresh(r.Context(), scope)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) allowRefresh() bool {
	h.refreshMu.Lock()
	defer h.refreshMu.Unlock()
	now := time.Now()
	if now.Before(h.nextRefresh) {
		return false
	}
	h.nextRefresh = now.Add(refreshInterval)
	return true
}

func (h *Handler) integrationScope(w http.ResponseWriter, r *http.Request, token string, required store.IntegrationScope) (auth.Scope, bool) {
	if token != "" {
		scope, err := h.auth.IntegrationScope(r.Context(), token, required)
		if err == nil {
			return scope, true
		}
	}
	writeAuthenticationFailure(w)
	return auth.Scope{}, false
}

func integrationToken(header http.Header) string {
	if token := strings.TrimSpace(header.Get("X-Emby-Token")); token != "" {
		return token
	}
	for _, name := range []string{"X-Emby-Authorization", "Authorization"} {
		if token := mediaBrowserToken(header.Get(name)); token != "" {
			return token
		}
	}
	return ""
}

// Match one quoted parameter at a time so a comma or an escaped quote in a
// client label cannot be mistaken for a separate Token parameter.
var mediaBrowserParam = regexp.MustCompile(`^[ \t]*([A-Za-z][A-Za-z0-9_-]*)[ \t]*=[ \t]*"((?:[^"\\]|\\.)*)"[ \t]*(,|$)`)

func mediaBrowserToken(header string) string {
	header = strings.TrimSpace(header)
	schemeEnd := strings.IndexAny(header, " \t")
	if schemeEnd < 0 || !strings.EqualFold(header[:schemeEnd], "MediaBrowser") {
		return ""
	}
	var token string
	var seenToken bool
	for params := header[schemeEnd:]; params != ""; {
		match := mediaBrowserParam.FindStringSubmatch(params)
		if match == nil {
			return ""
		}
		if strings.EqualFold(match[1], "Token") {
			if seenToken {
				return ""
			}
			token, seenToken = match[2], true
		}
		params = params[len(match[0]):]
		if match[3] == "," && params == "" {
			return ""
		}
	}
	return token
}
