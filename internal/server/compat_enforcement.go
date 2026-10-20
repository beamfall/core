package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/store"
)

const (
	compatCredentialAnonymous = "anonymous"
	compatCredentialNative    = "native"
	compatCredentialWeb       = "web"
)

type deviceSurfaceStore interface {
	Device(context.Context, store.DeviceID) (store.Device, error)
	BindDeviceSurface(context.Context, store.DeviceID, store.DeviceSurface) error
}

var compatExemptRoutes = map[string]string{
	"GET /health":                 "public liveness",
	"GET /metrics":                "public operator surface",
	"GET /source":                 "public source offer",
	"GET /{$}":                    "public process root",
	"GET /web/":                   "public embedded bundle",
	"GET /api/setup":              "pre-subject setup",
	"POST /api/setup":             "pre-subject setup",
	"POST /api/enroll":            "pre-subject enrollment",
	"POST /api/web/enroll":        "pre-subject web enrollment",
	"POST /api/web/logout":        "credential recovery",
	"GET /api/moderation/contact": "public moderation contact",
	"POST /api/compat/handshake":  "compatibility handshake",
	"POST /api/test/faults":       "test-only fault control",
}

func compatRouteExempt(method, pattern string) bool {
	if strings.HasPrefix(pattern, "/v1/") {
		return true
	}
	_, ok := compatExemptRoutes[method+" "+pattern]
	return ok
}

func compatCredentialClass(r *http.Request, scope auth.Scope) string {
	if scope.APITokenID != "" || scope.APIKeyID != "" {
		return compatCredentialNative
	}
	if scope.SessionID != "" && strings.TrimSpace(r.Header.Get("X-Beamfall-Session")) != "" {
		return compatCredentialNative
	}
	if scope.SessionID != "" {
		return compatCredentialWeb
	}
	return compatCredentialAnonymous
}

func compatSubject(scope auth.Scope) string {
	switch {
	case scope.SessionID != "":
		return "session:" + string(scope.SessionID)
	case scope.APIKeyID != "":
		return "api-key:" + scope.APIKeyID
	case scope.APITokenID != "":
		return "admin-token:" + scope.APITokenID
	default:
		return ""
	}
}

// compatEnrollment derives the enrollment a Client Contract arrived on. Every
// field comes from server-held credential state, never from the request body,
// so a client cannot name the enrollment class its build is entitled to.
func compatEnrollment(scope auth.Scope) capabilities.Enrollment {
	return capabilities.Enrollment{
		ID:           compatSubject(scope),
		Role:         compatEnrollmentRole(scope),
		Distribution: capabilities.Distribution(scope.DistributionClass),
	}
}

func (s *Server) persistedCompatSurface(ctx context.Context, scope auth.Scope) (capabilities.Surface, error) {
	if s.deviceSurfaces == nil || scope.DeviceID == "" {
		return "", errors.New("device surface store unavailable")
	}
	device, err := s.deviceSurfaces.Device(ctx, scope.DeviceID)
	if err != nil {
		return "", err
	}
	surface, err := store.NormalizeDeviceSurface(device.Surface)
	if err != nil {
		return "", err
	}
	return capabilities.Surface(surface), nil
}

// compatEnrollmentRole maps a credential to the closed enrollment vocabulary.
// A credential outside the vocabulary yields an empty role, which provenance
// refuses.
func compatEnrollmentRole(scope auth.Scope) capabilities.EnrollmentRole {
	switch {
	case scope.APITokenID != "":
		return capabilities.EnrollmentRoleAdminToken
	case scope.APIKeyID != "":
		return capabilities.EnrollmentRoleAPIKey
	case scope.DeviceRole == store.DeviceRoleManager:
		return capabilities.EnrollmentRoleManager
	case scope.DeviceRole == store.DeviceRoleViewer:
		return capabilities.EnrollmentRoleViewer
	default:
		return ""
	}
}

func compatAuthFingerprint(scope auth.Scope) string {
	apiScopes := make([]string, len(scope.APIScopes))
	for i, apiScope := range scope.APIScopes {
		apiScopes[i] = string(apiScope)
	}
	sort.Strings(apiScopes)
	libraryIDs := make([]string, len(scope.LibraryIDs))
	for i, libraryID := range scope.LibraryIDs {
		libraryIDs[i] = string(libraryID)
	}
	sort.Strings(libraryIDs)
	canonical := struct {
		DeviceID          string   `json:"deviceID"`
		DeviceRole        string   `json:"deviceRole"`
		DistributionClass string   `json:"distributionClass"`
		ProfileID         string   `json:"profileID"`
		Profile           string   `json:"profile"`
		Revealed          bool     `json:"revealed"`
		Admin             bool     `json:"admin"`
		APIKeyID          string   `json:"apiKeyID"`
		APIScopes         []string `json:"apiScopes"`
		LibraryIDs        []string `json:"libraryIDs"`
		AllLibraries      bool     `json:"allLibraries"`
	}{
		DeviceID:          string(scope.DeviceID),
		DeviceRole:        string(scope.DeviceRole),
		DistributionClass: string(scope.DistributionClass),
		ProfileID:         string(scope.ProfileID),
		Profile:           string(scope.Profile),
		Revealed:          scope.Revealed,
		Admin:             scope.Admin,
		APIKeyID:          scope.APIKeyID,
		APIScopes:         apiScopes,
		LibraryIDs:        libraryIDs,
		AllLibraries:      scope.AllLibraries,
	}
	encoded, _ := json.Marshal(canonical)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (state *compatState) markRefused(subject string) {
	state.mu.Lock()
	state.refused[subject] = struct{}{}
	state.mu.Unlock()
}

func (state *compatState) clearRefused(subject string) {
	state.mu.Lock()
	delete(state.refused, subject)
	state.mu.Unlock()
}

func (state *compatState) isRefused(subject string) bool {
	state.mu.RLock()
	_, refused := state.refused[subject]
	state.mu.RUnlock()
	return refused
}

func (s *Server) requireCompatDecision(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, err := s.auth.ScopeFromRequest(r.Context(), r)
		if err != nil {
			next(w, r)
			return
		}
		subject := compatSubject(scope)
		if subject == "" {
			next(w, r)
			return
		}
		bound, result, generation := s.compatAuthorize(r, scope)
		if !bound {
			writeCompatRefusal(w, result)
			return
		}
		if generation != "" {
			w.Header().Set("Cache-Control", "no-store")
		}
		// Hand the decision identity downstream so a route that returns a
		// reusable cache entry keys it on the decision, not just the content
		// version. An empty generation means no decision record
		// could be named, and the route falls back to uncacheable.
		next(w, r.WithContext(mediahttp.WithCompatBinding(r.Context(), generation, subject)))
	}
}

func (s *Server) compatAuthorize(r *http.Request, scope auth.Scope) (bool, capabilities.Result, string) {
	subject := compatSubject(scope)
	if s.compat.isRefused(subject) {
		return false, compatDecisionRequired(), ""
	}
	policy := s.compat.snapshot()
	credentialClass := compatCredentialClass(r, scope)
	derivedSurface := ""
	if credentialClass == compatCredentialWeb {
		derivedSurface = "core-web-ui"
	} else if credentialClass == compatCredentialNative && scope.DeviceID != "" {
		surface, err := s.persistedCompatSurface(r.Context(), scope)
		if err != nil {
			return false, compatDecisionRequired(), ""
		}
		derivedSurface = string(surface)
	}
	record, err := s.compat.registry.Resolve(subject, capabilities.ResolveKey{
		Subject:         subject,
		Distribution:    capabilities.Distribution(scope.DistributionClass),
		AuthFingerprint: compatAuthFingerprint(scope),
		Generation:      policy.Generation,
		DerivedSurface:  derivedSurface,
	}, s.compat.now())
	if err == nil {
		return compatServable(record.Result), record.Result, strconv.Itoa(record.Key.Generation)
	}
	if credentialClass == compatCredentialNative && !capabilities.IsNativeSurface(capabilities.Surface(derivedSurface)) && errors.Is(err, capabilities.ErrNoDecision) {
		return true, capabilities.Result{}, ""
	}
	if credentialClass != compatCredentialWeb {
		return false, compatDecisionRequired(), ""
	}
	if errors.Is(err, capabilities.ErrNoDecision) {
		return s.acceptEmbeddedDecision(scope, policy)
	}
	if errors.Is(err, capabilities.ErrStaleGeneration) && !record.Submitted {
		return s.acceptEmbeddedDecision(scope, policy)
	}
	if record.Result.Decision != "" && !compatServable(record.Result) {
		return false, record.Result, ""
	}
	return false, compatDecisionRequired(), ""
}

func (s *Server) acceptEmbeddedDecision(scope auth.Scope, policy capabilities.Policy) (bool, capabilities.Result, string) {
	contract := capabilities.EmbeddedUIContract()
	enrollment := compatEnrollment(scope)
	enrollment.EmbeddedUI = true
	distribution := enrollment.Distribution
	for range 3 {
		result := capabilities.Evaluate(contract, enrollment, policy)
		if !compatServable(result) {
			return false, result, ""
		}
		record, accepted, err := s.compat.registry.AcceptDerived(capabilities.BindingKey{
			Subject:         compatSubject(scope),
			Surface:         contract.Surface,
			Distribution:    distribution,
			AuthFingerprint: compatAuthFingerprint(scope),
			Generation:      policy.Generation,
		}, contract, result, s.compat.now(), compatProvisionalTTL)
		if err == nil {
			return accepted, result, strconv.Itoa(record.Key.Generation)
		}
		if !errors.Is(err, capabilities.ErrStaleGeneration) {
			return false, compatDecisionRequired(), ""
		}
		policy = s.compat.snapshot()
	}
	return false, compatDecisionRequired(), ""
}

func compatServable(result capabilities.Result) bool {
	return result.Decision == "compatible" || result.Decision == "degraded"
}

func compatDecisionRequired() capabilities.Result {
	return capabilities.Result{
		Decision:   capabilities.Decision("client-too-old"),
		ReasonCode: capabilities.ReasonCode("unknown-build"),
	}
}

func writeCompatRefusal(w http.ResponseWriter, result capabilities.Result) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusForbidden, compatHandshakeResponse{
		Decision:     string(result.Decision),
		ReasonCode:   string(result.ReasonCode),
		MessageClass: compatMessageClass(string(result.Decision)),
	})
}

func (s *Server) compatEventGuard(r *http.Request) beforeEventEmit {
	return func(ctx context.Context) (store.ProfileID, store.LibraryFilter, error) {
		scope, err := s.auth.ScopeFromRequest(ctx, r)
		if err != nil {
			return "", store.LibraryFilter{}, err
		}
		allowed, _, _ := s.compatAuthorize(r, scope)
		if !allowed {
			return "", store.LibraryFilter{}, errors.New("compatibility decision no longer valid")
		}
		return scope.ProfileID, scope.LibraryFilter(), nil
	}
}
