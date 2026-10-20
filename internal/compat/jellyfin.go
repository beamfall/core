// Package compat owns the removable, config-gated compatibility route group.
package compat

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

const Prefix = "/compat/jellyfin/"

type Config struct {
	Enabled           bool
	AllowedCIDRs      []string
	TrustedProxyCIDRs []string
}

type Handler struct {
	enabled        bool
	allowed        []*net.IPNet
	trustedProxies []*net.IPNet
	auth           *auth.Service
	next           http.Handler
	relayed        func(*http.Request) bool
}

func New(cfg Config, authService *auth.Service, next http.Handler) (*Handler, error) {
	allowed, err := parseCIDRs(cfg.AllowedCIDRs)
	if err != nil {
		return nil, err
	}
	trusted, err := parseCIDRs(cfg.TrustedProxyCIDRs)
	if err != nil {
		return nil, err
	}
	if next == nil {
		next = http.NotFoundHandler()
	}
	return &Handler{enabled: cfg.Enabled, allowed: allowed, trustedProxies: trusted, auth: authService, next: next}, nil
}

func (h *Handler) SetRelayedRequestPredicate(predicate func(*http.Request) bool) {
	h.relayed = predicate
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, Prefix) || !h.enabled {
		http.NotFound(w, r)
		return
	}
	if h.relayed != nil && h.relayed(r) {
		http.NotFound(w, r)
		return
	}
	if !containsIP(h.allowed, h.clientIP(r)) {
		http.Error(w, "compatibility route unavailable", http.StatusForbidden)
		return
	}
	defer func() {
		if recover() != nil {
			http.Error(w, "compatibility handler failed", http.StatusInternalServerError)
		}
	}()

	switch {
	case r.Method == http.MethodPost && r.URL.Path == Prefix+"Users/AuthenticateByName":
		h.authenticateByName(w, r)
	case r.Method == http.MethodPost && r.URL.Path == Prefix+"Sessions/Logout":
		h.logout(w, r)
	default:
		h.next.ServeHTTP(w, r)
	}
}

func (h *Handler) clientIP(r *http.Request) net.IP {
	peer := remoteIP(r.RemoteAddr)
	if !containsIP(h.trustedProxies, peer) {
		return peer
	}
	forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if strings.Contains(forwarded, ",") {
		return nil
	}
	return net.ParseIP(forwarded)
}

func remoteIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = strings.Trim(remoteAddr, "[]")
	}
	return net.ParseIP(host)
}

func parseCIDRs(values []string) ([]*net.IPNet, error) {
	parsed := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, network)
	}
	return parsed, nil
}

func containsIP(networks []*net.IPNet, ip net.IP) bool {
	for _, network := range networks {
		if ip != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

type authenticateRequest struct {
	Username string `json:"Username"`
	Password string `json:"Pw"`
}

func (h *Handler) authenticateByName(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var request authenticateRequest
	scopes := []store.IntegrationScope{store.IntegrationScopeRead, store.IntegrationScopeRefresh}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request) != nil {
		// Route malformed payloads through the same authentication refusal lane;
		// otherwise JSON decoding would expose a faster enumeration class.
		_, _ = h.auth.AuthenticateIntegrationByName(r.Context(), "", "", scopes)
		writeAuthenticationFailure(w)
		return
	}
	session, err := h.auth.AuthenticateIntegrationByName(r.Context(), request.Username, request.Password, scopes)
	if err != nil {
		writeAuthenticationFailure(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"AccessToken": session.Token,
		"User":        map[string]string{"Id": string(session.ProfileID)},
		"ExpiresAt":   session.ExpiresAt,
	})
}

func writeAuthenticationFailure(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte("{\"error\":\"authentication failed\"}\n"))
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.Header.Get("X-Emby-Token"))
	if token == "" || h.auth.RevokeIntegrationSession(r.Context(), token) != nil {
		http.Error(w, "invalid integration session", http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
