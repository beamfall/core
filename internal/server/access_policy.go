package server

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/beamfall/core/internal/auth"
)

func (s *Server) requestAccessClass(r *http.Request, method string) (AccessClass, bool) {
	if method == "" || s.mux == nil {
		return AccessClassUnclassified, false
	}
	probe := r.Clone(r.Context())
	probe.Method = method
	_, registered := s.mux.Handler(probe)
	_, pattern, ok := strings.Cut(registered, " ")
	if !ok {
		return AccessClassUnclassified, false
	}
	class, ok := s.accessClasses[method+" "+pattern]
	return class, ok
}

func applyAccessPolicy(w http.ResponseWriter, r *http.Request, class AccessClass) bool {
	policy, ok := accessPolicies[class]
	if !ok {
		writeError(w, r, http.StatusForbidden, "route access policy unavailable")
		return false
	}
	if !applyCORS(w, r, policy) {
		return false
	}
	if r.Method == http.MethodOptions {
		return true
	}
	if policy.requireCredential && !requestHasCredential(r) {
		writeError(w, r, http.StatusUnauthorized, "required credential missing")
		return false
	}
	if policy.requireCookieCSRF && requestNeedsCookieCSRF(r) && r.Header.Get(auth.CSRFHeader) == "" {
		writeError(w, r, http.StatusForbidden, "missing CSRF header")
		return false
	}
	return true
}

func requestHasCredential(r *http.Request) bool {
	if strings.TrimSpace(r.Header.Get("Authorization")) != "" {
		return true
	}
	if strings.TrimSpace(r.Header.Get("X-Beamfall-Session")) != "" {
		return true
	}
	_, err := r.Cookie(auth.SessionCookie)
	return err == nil
}

func requestNeedsCookieCSRF(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	_, err := r.Cookie(auth.SessionCookie)
	return err == nil
}

func applyCORS(w http.ResponseWriter, r *http.Request, policy accessPolicy) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if policy.allowCrossOrigin {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		setCORSPreflightHeaders(w, r)
		return true
	}
	if policy.allowTokenByteCrossOrigin && tokenByteGETPath(r) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		return true
	}
	if !originAllowed(origin, r) {
		writeError(w, r, http.StatusForbidden, "origin not allowed")
		return false
	}
	w.Header().Add("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	setCORSPreflightHeaders(w, r)
	return true
}

func setCORSPreflightHeaders(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", auth.CSRFHeader+", Content-Type, Authorization")
	}
}

func tokenByteGETPath(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	path := r.URL.Path
	return strings.HasPrefix(path, "/api/media/direct/") ||
		strings.HasPrefix(path, "/api/media/artwork-candidate/") ||
		strings.HasPrefix(path, "/api/media/artwork/") ||
		strings.HasPrefix(path, "/api/entity/artwork/") ||
		strings.HasPrefix(path, "/api/media/hls/") ||
		strings.HasPrefix(path, "/api/media/assets/") ||
		strings.HasPrefix(path, "/api/media/subtitle/") ||
		strings.HasPrefix(path, "/api/media/sidecar/") ||
		strings.HasPrefix(path, "/api/media/proxy/") ||
		strings.HasPrefix(path, "/api/pack/manifest/") ||
		strings.HasPrefix(path, "/api/pack/content/") ||
		strings.HasPrefix(path, "/api/pack/status/")
}

func originAllowed(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	return host != "" && strings.EqualFold(u.Host, host)
}

func apiPath(path string) bool {
	return strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/v1/")
}
