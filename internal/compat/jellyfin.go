// Package compat owns the removable, config-gated compatibility route group.
package compat

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

const Prefix = "/compat/jellyfin/"

type jellyfinRoute struct {
	method string
	path   string
}

var jellyfinRoutes = map[jellyfinRoute]func(*Handler, http.ResponseWriter, *http.Request, net.IP){
	{http.MethodPost, Prefix + "Users/AuthenticateByName"}: (*Handler).authenticateByName,
	{http.MethodPost, Prefix + "Sessions/Logout"}:          (*Handler).logout,
	{http.MethodGet, Prefix + "System/Info"}:               (*Handler).systemInfo,
	{http.MethodGet, Prefix + "System/Info/Public"}:        (*Handler).publicSystemInfo,
	{http.MethodGet, Prefix + "System/Configuration"}:      (*Handler).systemConfiguration,
	{http.MethodGet, Prefix + "Items"}:                     (*Handler).emptyItems,
	{http.MethodPost, Prefix + "Auth/Keys"}:                (*Handler).createAPIKey,
	{http.MethodGet, Prefix + "Auth/Keys"}:                 (*Handler).listAPIKeys,
}

// notifierRoutes are the routes the *arr Emby/Jellyfin connection calls. They
// are served at the prefix and under the mediabrowser/ segment Lidarr 3.x adds.
var notifierRoutes = map[jellyfinRoute]func(*Handler, http.ResponseWriter, *http.Request, net.IP){
	{http.MethodPost, "Notifications/Admin"}:   (*Handler).adminNotification,
	{http.MethodGet, "Library/MediaFolders"}:   (*Handler).emptyItems,
	{http.MethodPost, "Library/Media/Updated"}: (*Handler).mediaUpdated,
	{http.MethodPost, "Library/Refresh"}:       (*Handler).refreshLibrary,
}

func init() {
	for route, serve := range notifierRoutes {
		for _, base := range []string{Prefix, Prefix + "mediabrowser/"} {
			jellyfinRoutes[jellyfinRoute{route.method, base + route.path}] = serve
		}
	}
}

type Config struct {
	Enabled           bool
	AllowedCIDRs      []string
	TrustedProxyCIDRs []string
	ServerID          string
	ServerName        string
	ReleaseVersion    string
	SetupRequired     func() bool
	EnqueueRefresh    func(ctx context.Context, scope auth.Scope, paths []string)
}

type Handler struct {
	enabled        bool
	allowed        []*net.IPNet
	trustedProxies []*net.IPNet
	auth           *auth.Service
	next           http.Handler
	relayed        func(*http.Request) bool
	serverID       string
	serverName     string
	releaseVersion string
	setupRequired  func() bool
	enqueueRefresh func(ctx context.Context, scope auth.Scope, paths []string)
	refreshMu      sync.Mutex
	background     sync.WaitGroup // media update scans queued after the response
	nextRefresh    time.Time
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
	return &Handler{
		enabled: cfg.Enabled, allowed: allowed, trustedProxies: trusted, auth: authService, next: next,
		serverID: cfg.ServerID, serverName: cfg.ServerName, releaseVersion: cfg.ReleaseVersion,
		setupRequired: cfg.SetupRequired, enqueueRefresh: cfg.EnqueueRefresh,
	}, nil
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
	clientIP := h.clientIP(r)
	if clientIP.IsUnspecified() || !containsIP(h.allowed, clientIP) {
		http.Error(w, "compatibility route unavailable", http.StatusForbidden)
		return
	}
	defer func() {
		if recover() != nil {
			http.Error(w, "compatibility handler failed", http.StatusInternalServerError)
		}
	}()

	if serve := jellyfinRoutes[jellyfinRoute{r.Method, r.URL.Path}]; serve != nil {
		serve(h, w, r, clientIP)
		return
	}
	h.next.ServeHTTP(w, r)
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

func (h *Handler) authenticateByName(w http.ResponseWriter, r *http.Request, clientIP net.IP) {
	defer r.Body.Close()
	var request authenticateRequest
	scopes := []store.IntegrationScope{store.IntegrationScopeRead, store.IntegrationScopeRefresh}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request) != nil {
		// Route malformed payloads through the same authentication refusal lane;
		// otherwise JSON decoding would expose a faster enumeration class.
		_, _ = h.auth.AuthenticateIntegrationByName(r.Context(), "", "", clientIP.String(), scopes)
		writeAuthenticationFailure(w)
		return
	}
	session, err := h.auth.AuthenticateIntegrationByName(r.Context(), request.Username, request.Password, clientIP.String(), scopes)
	if err != nil {
		writeAuthenticationFailure(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(authenticationResult{ // #nosec G117 -- the bearer is the sign-in result, sent only to its caller with no-store.
		AccessToken: session.Token,
		ServerID:    h.serverID,
		ExpiresAt:   session.ExpiresAt,
		User: compatUser{
			ID: string(session.ProfileID), Name: session.ProfileName,
			ServerID: h.serverID, ServerName: h.serverName,
			Policy: compatUserPolicy{IsAdministrator: session.Setup},
		},
	})
}

// authenticationResult is the AuthenticateByName body. It names the signed-in
// Profile and the server, and nothing else from the Profile.
type authenticationResult struct {
	AccessToken string     `json:"AccessToken"`
	ServerID    string     `json:"ServerId"`
	ExpiresAt   time.Time  `json:"ExpiresAt"`
	User        compatUser `json:"User"`
}

type compatUser struct {
	ID         string           `json:"Id"`
	Name       string           `json:"Name"`
	ServerID   string           `json:"ServerId"`
	ServerName string           `json:"ServerName,omitempty"`
	Policy     compatUserPolicy `json:"Policy"`
}

// compatUserPolicy reports IsAdministrator only for the bounded admin setup
// session, which is what setup wizards such as Seerr check.
type compatUserPolicy struct {
	IsAdministrator bool `json:"IsAdministrator"`
}

func writeAuthenticationFailure(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte("{\"error\":\"authentication failed\"}\n"))
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request, _ net.IP) {
	token := integrationToken(r.Header)
	if token == "" || h.auth.RevokeIntegrationSession(r.Context(), token) != nil {
		writeAuthenticationFailure(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
