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
	"GET /health":                   "public liveness",
	"GET /metrics":                  "public operator surface",
	"GET /source":                   "public source offer",
	"GET /source/ffmpeg":            "public GPL source offer",
	"GET /api/docs":                 "public API reference",
	"GET /api/docs/openapi.json":    "public API reference",
	"GET /{$}":                      "public process root",
	"GET /web/":                     "public embedded bundle",
	"GET /api/setup":                "pre-subject setup",
	"POST /api/setup":               "pre-subject setup",
	"POST /api/enroll":              "pre-subject enrollment",
	"POST /api/party/join":          "pre-subject party guest",
	"GET /api/party/guest":          "pre-subject party guest",
	"GET /api/party/guest/search":   "pre-subject party guest",
	"POST /api/party/guest/request": "pre-subject party guest",
	"POST /api/party/guest/cancel":  "pre-subject party guest",
	"POST /api/web/enroll":          "pre-subject web enrollment",
	"POST /api/web/logout":          "credential recovery",
	"POST /api/web/session/renew":   "credential recovery",
	"POST /api/web/admin-signin":    "pre-subject web sign-in",
	"GET /api/moderation/contact":   "public moderation contact",
	"POST /api/compat/handshake":    "compatibility handshake",
	"POST /api/test/faults":         "test-only fault control",

	// Device-initiated pairing (CORE-0316).
	"POST /api/pairing-requests":            "pre-subject enrollment",
	"POST /api/pairing-requests/poll":       "pre-subject enrollment",
	"POST /api/pairing-requests/regenerate": "pre-subject enrollment",
	"POST /api/pairing-requests/cancel":     "pre-subject enrollment",
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
	case enrolledRole(scope) == store.DeviceRoleManager:
		return capabilities.EnrollmentRoleManager
	case enrolledRole(scope) == store.DeviceRoleViewer:
		return capabilities.EnrollmentRoleViewer
	default:
		return ""
	}
}

// enrolledRole is the Device's enrolled role for build provenance: a
// player-bound Device authorizes as viewer but keeps its enrolled build class.
func enrolledRole(scope auth.Scope) store.DeviceRole {
	if scope.EnrolledDeviceRole != "" {
		return scope.EnrolledDeviceRole
	}
	return scope.DeviceRole
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
		DeviceID                string   `json:"deviceID"`
		DeviceRole              string   `json:"deviceRole"`
		DistributionClass       string   `json:"distributionClass"`
		ProfileID               string   `json:"profileID"`
		Profile                 string   `json:"profile"`
		Revealed                bool     `json:"revealed"`
		Admin                   bool     `json:"admin"`
		APIKeyID                string   `json:"apiKeyID"`
		APIScopes               []string `json:"apiScopes"`
		LibraryIDs              []string `json:"libraryIDs"`
		AllLibraries            bool     `json:"allLibraries"`
		PlayerProfileID         string   `json:"playerProfileID,omitempty"`
		PlayerBindingGeneration int      `json:"playerBindingGeneration,omitempty"`
		EnrolledDeviceRole      string   `json:"enrolledDeviceRole,omitempty"`
	}{
		DeviceID:                string(scope.DeviceID),
		DeviceRole:              string(scope.DeviceRole),
		DistributionClass:       string(scope.DistributionClass),
		ProfileID:               string(scope.ProfileID),
		Profile:                 string(scope.Profile),
		Revealed:                scope.Revealed,
		Admin:                   scope.Admin,
		APIKeyID:                scope.APIKeyID,
		APIScopes:               apiScopes,
		LibraryIDs:              libraryIDs,
		AllLibraries:            scope.AllLibraries,
		PlayerProfileID:         string(scope.PlayerProfileID),
		PlayerBindingGeneration: scope.PlayerBindingGeneration,
	}
	// A player-bound Device authorizes as viewer but passes provenance on its
	// enrolled role, so a change to that role must invalidate the decision.
	if enrolled := enrolledRole(scope); enrolled != scope.DeviceRole {
		canonical.EnrolledDeviceRole = string(enrolled)
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
		if errors.Is(err, auth.ErrProfileMismatch) {
			writeProfileMismatch(w, r)
			return
		}
		if err != nil {
			next(w, r)
			return
		}
		subject := compatSubject(scope)
		if subject == "" {
			next(w, r)
			return
		}
		allowed, result, record := s.compatAuthorize(r, scope)
		if !allowed {
			writeCompatRefusal(w, r, result)
			return
		}
		generation := ""
		if record.DecisionID != "" {
			generation = strconv.Itoa(record.Key.Generation)
			w.Header().Set("Cache-Control", "no-store")
		}
		// Hand the decision identity downstream so a route that returns a
		// reusable cache entry keys it on the decision, not just the content
		// version. An empty generation means no decision record
		// could be named, and the route falls back to uncacheable.
		ctx := mediahttp.WithCompatBinding(r.Context(), generation, subject, record.DecisionID)
		if record.DecisionID != "" {
			ctx = auth.WithClientCapabilities(ctx, record.Contract.RequestedCapabilities)
		} else {
			ctx = auth.WithClientCapabilities(ctx, nil)
		}
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) compatAuthorize(r *http.Request, scope auth.Scope) (bool, capabilities.Result, capabilities.Record) {
	s.devBuilds.refresh()
	subject := compatSubject(scope)
	if s.compat.isRefused(subject) {
		return false, compatDecisionRequired(), capabilities.Record{}
	}
	policy := s.compat.snapshot()
	credentialClass := compatCredentialClass(r, scope)
	derivedSurface := ""
	if credentialClass == compatCredentialWeb {
		derivedSurface = "core-web-ui"
	} else if credentialClass == compatCredentialNative && scope.DeviceID != "" {
		surface, err := s.persistedCompatSurface(r.Context(), scope)
		if err != nil {
			return false, compatDecisionRequired(), capabilities.Record{}
		}
		derivedSurface = string(surface)
	}
	key := capabilities.ResolveKey{
		Subject:         subject,
		Distribution:    capabilities.Distribution(scope.DistributionClass),
		AuthFingerprint: compatAuthFingerprint(scope),
		Generation:      policy.Generation,
		DerivedSurface:  derivedSurface,
	}
	now := s.compat.now()
	record, err := s.compat.registry.Resolve(subject, key, now)
	if err == nil {
		return compatServable(record.Result), record.Result, record
	}
	if credentialClass == compatCredentialNative && !capabilities.IsNativeSurface(capabilities.Surface(derivedSurface)) && errors.Is(err, capabilities.ErrNoDecision) {
		return true, capabilities.Result{}, capabilities.Record{}
	}
	if credentialClass != compatCredentialWeb {
		return false, compatDecisionRequired(), capabilities.Record{}
	}
	if errors.Is(err, capabilities.ErrNoDecision) {
		return s.acceptEmbeddedDecision(scope, policy)
	}
	if errors.Is(err, capabilities.ErrAuthChanged) {
		// The session's own grants, Profile or device role changed under a
		// servable decision. The web UI's contract is the server's embedded
		// bundle, so re-deriving under the new fingerprint proves exactly what
		// the original decision did. Resolve reports the auth mismatch ahead of
		// expiry and generation, so check the record under its own fingerprint
		// first: an auth change must not renew an expired decision or a
		// submitted one from an earlier generation.
		key.AuthFingerprint = record.Key.AuthFingerprint
		record, err = s.compat.registry.Resolve(subject, key, now)
		if err == nil {
			return s.rebindEmbeddedDecision(scope, policy, record)
		}
	}
	if errors.Is(err, capabilities.ErrStaleGeneration) && !record.Submitted {
		return s.acceptEmbeddedDecision(scope, policy)
	}
	if record.Result.Decision != "" && !compatServable(record.Result) {
		return false, record.Result, capabilities.Record{}
	}
	return false, compatDecisionRequired(), capabilities.Record{}
}

// rebindEmbeddedDecision moves a web session's valid decision to its current
// auth fingerprint, keeping its submitted status and expiry. The registry
// applies the change only to the exact record that was resolved, so a
// concurrent refusal, handshake or generation change is never overwritten;
// the request is then refused and the client resubmits.
func (s *Server) rebindEmbeddedDecision(scope auth.Scope, policy capabilities.Policy, prior capabilities.Record) (bool, capabilities.Result, capabilities.Record) {
	enrollment := compatEnrollment(scope)
	enrollment.EmbeddedUI = true
	result := capabilities.Evaluate(capabilities.EmbeddedUIContract(), enrollment, policy)
	if !compatServable(result) {
		return false, result, capabilities.Record{}
	}
	record, err := s.compat.registry.RebindAuth(prior, compatAuthFingerprint(scope), s.compat.now())
	if err != nil {
		return false, compatDecisionRequired(), capabilities.Record{}
	}
	return true, result, record
}

func (s *Server) acceptEmbeddedDecision(scope auth.Scope, policy capabilities.Policy) (bool, capabilities.Result, capabilities.Record) {
	contract := capabilities.EmbeddedUIContract()
	enrollment := compatEnrollment(scope)
	enrollment.EmbeddedUI = true
	distribution := enrollment.Distribution
	for range 3 {
		result := capabilities.Evaluate(contract, enrollment, policy)
		if !compatServable(result) {
			return false, result, capabilities.Record{}
		}
		record, accepted, err := s.compat.registry.AcceptDerived(capabilities.BindingKey{
			Subject:         compatSubject(scope),
			Surface:         contract.Surface,
			Distribution:    distribution,
			AuthFingerprint: compatAuthFingerprint(scope),
			Generation:      policy.Generation,
		}, contract, result, s.compat.now(), compatProvisionalTTL)
		if err == nil {
			return accepted, result, record
		}
		if !errors.Is(err, capabilities.ErrStaleGeneration) {
			return false, compatDecisionRequired(), capabilities.Record{}
		}
		policy = s.compat.snapshot()
	}
	return false, compatDecisionRequired(), capabilities.Record{}
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

// compatRefusalGraphQLCode mirrors internal/graph.ErrorCodeForbidden;
// internal/server may not import internal/graph.
const compatRefusalGraphQLCode = "FORBIDDEN"

// compatGraphQLErrorEnvelope keeps the REST refusal fields at the top level,
// which existing clients read from any 403, and adds the GraphQL errors list.
type compatGraphQLErrorEnvelope struct {
	compatHandshakeResponse
	Data   any                  `json:"data"`
	Errors []compatGraphQLError `json:"errors"`
}

type compatGraphQLError struct {
	Message    string                       `json:"message"`
	Extensions compatGraphQLErrorExtensions `json:"extensions"`
}

type compatGraphQLErrorExtensions struct {
	Code         string `json:"code"`
	Decision     string `json:"decision"`
	ReasonCode   string `json:"reasonCode,omitempty"`
	MessageClass string `json:"messageClass"`
}

func compatRefusalMessage(decision capabilities.Decision) string {
	switch decision {
	case capabilities.DecisionServerTooOld:
		return "server update required"
	case capabilities.DecisionServerInstallationInconsistent:
		return "server installation is inconsistent"
	default:
		return "client update required"
	}
}

func writeCompatRefusal(w http.ResponseWriter, r *http.Request, result capabilities.Result) {
	w.Header().Set("Cache-Control", "no-store")
	decision := string(result.Decision)
	reasonCode := string(result.ReasonCode)
	messageClass := compatMessageClass(decision)
	if r != nil && r.URL != nil && r.URL.Path == graphQLPath {
		writeJSON(w, http.StatusForbidden, compatGraphQLErrorEnvelope{
			compatHandshakeResponse: compatHandshakeResponse{
				Decision:     decision,
				ReasonCode:   reasonCode,
				MessageClass: messageClass,
			},
			Data: nil,
			Errors: []compatGraphQLError{{
				Message: compatRefusalMessage(result.Decision),
				Extensions: compatGraphQLErrorExtensions{
					Code:         compatRefusalGraphQLCode,
					Decision:     decision,
					ReasonCode:   reasonCode,
					MessageClass: messageClass,
				},
			}},
		})
		return
	}
	writeJSON(w, http.StatusForbidden, compatHandshakeResponse{
		Decision:     decision,
		ReasonCode:   reasonCode,
		MessageClass: messageClass,
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
