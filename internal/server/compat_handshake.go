package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/store"
)

type compatHandshakeResponse struct {
	Decision             string   `json:"decision"`
	DecisionID           string   `json:"decisionID,omitempty"`
	ReasonCode           string   `json:"reasonCode,omitempty"`
	DisabledCapabilities []string `json:"disabledCapabilities,omitempty"`
	MessageClass         string   `json:"messageClass"`
}

func (s *Server) compatHandshake(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !compatHasCredential(r) {
		writeJSON(w, http.StatusOK, compatHandshakeResponse{
			Decision:     "bootstrap",
			MessageClass: compatMessageClass("bootstrap"),
		})
		return
	}
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}
	subject := compatSubject(scope)
	if subject == "" {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	var contract capabilities.ClientContract
	if err := json.NewDecoder(r.Body).Decode(&contract); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	contract = normalizeCompatContract(contract)
	credentialClass := compatCredentialClass(r, scope)
	derivedSurface := capabilities.Surface("")
	bindNativeSurface := false
	if credentialClass == compatCredentialWeb {
		contract.Surface = "core-web-ui"
		contract = compatBindEmbeddedArtifact(contract)
		derivedSurface = capabilities.SurfaceCoreWebUI
	} else if credentialClass == compatCredentialNative && scope.DeviceID != "" {
		derivedSurface, err = s.persistedCompatSurface(r.Context(), scope)
		if err != nil {
			writeError(w, r, http.StatusServiceUnavailable, "device surface unavailable")
			return
		}
		if derivedSurface == capabilities.SurfaceCoreWebUI && capabilities.IsNativeSurface(capabilities.Surface(contract.Surface)) {
			derivedSurface = ""
			bindNativeSurface = true
		}
	}
	record, result, accepted, err := s.acceptCompatHandshake(r.Context(), scope, contract, derivedSurface, credentialClass == compatCredentialWeb, bindNativeSurface)
	if err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "compatibility policy changed; retry")
		return
	}
	response := compatHandshakeResponse{
		Decision:             string(result.Decision),
		ReasonCode:           string(result.ReasonCode),
		DisabledCapabilities: append([]string(nil), result.DisabledCapabilities...),
		MessageClass:         compatMessageClass(string(result.Decision)),
	}
	if accepted {
		s.compat.clearRefused(subject)
		response.DecisionID = record.DecisionID
	} else {
		s.compat.markRefused(subject)
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) acceptCompatHandshake(ctx context.Context, scope auth.Scope, contract capabilities.ClientContract, derivedSurface capabilities.Surface, embeddedUI bool, bindNativeSurface bool) (capabilities.Record, capabilities.Result, bool, error) {
	enrollment := compatEnrollment(scope)
	enrollment.EmbeddedUI = embeddedUI
	enrollment.Surface = derivedSurface
	distribution := enrollment.Distribution
	for range 3 {
		policy := s.compat.snapshot()
		result := capabilities.Evaluate(contract, enrollment, policy)
		if result.Servable() && bindNativeSurface {
			if err := s.deviceSurfaces.BindDeviceSurface(ctx, scope.DeviceID, store.DeviceSurface(contract.Surface)); err != nil {
				return capabilities.Record{}, capabilities.Result{}, false, err
			}
			enrollment.Surface = capabilities.Surface(contract.Surface)
			result = capabilities.Evaluate(contract, enrollment, policy)
		}
		record, accepted, err := s.compat.registry.Accept(capabilities.BindingKey{
			Subject:         compatSubject(scope),
			Surface:         contract.Surface,
			Distribution:    distribution,
			AuthFingerprint: compatAuthFingerprint(scope),
			Generation:      policy.Generation,
		}, contract, result, s.compat.now(), compatDecisionTTL)
		if !errors.Is(err, capabilities.ErrStaleGeneration) {
			return record, result, accepted, err
		}
	}
	return capabilities.Record{}, capabilities.Result{}, false, capabilities.ErrStaleGeneration
}

func compatHasCredential(r *http.Request) bool {
	if strings.TrimSpace(r.Header.Get("Authorization")) != "" {
		return true
	}
	if strings.TrimSpace(r.Header.Get("X-Beamfall-Session")) != "" {
		return true
	}
	if _, err := r.Cookie(auth.SessionCookie); err == nil {
		return true
	}
	return false
}

// compatBindEmbeddedArtifact supplies the server-known artifact digest for the
// Core Web UI compiled into this binary. That bundle is part of the server
// itself, so its digest is a server fact and the browser has nothing to attest;
// every other build id keeps the digest it presented. Callers apply this only
// to the web credential class, alongside the derived-surface pin — a native
// credential claiming the embedded build id still has to present the digest.
func compatBindEmbeddedArtifact(contract capabilities.ClientContract) capabilities.ClientContract {
	embedded := capabilities.EmbeddedUIContract()
	if contract.BuildID != embedded.BuildID {
		return contract
	}
	contract.ArtifactHash = embedded.ArtifactHash
	return contract
}

func normalizeCompatContract(contract capabilities.ClientContract) capabilities.ClientContract {
	contract.Surface = strings.TrimSpace(contract.Surface)
	contract.AppVersion = strings.TrimSpace(contract.AppVersion)
	contract.BuildID = strings.TrimSpace(contract.BuildID)
	contract.ArtifactHash = strings.ToLower(strings.TrimSpace(contract.ArtifactHash))
	contract.RenderingStamps = normalizedStrings(contract.RenderingStamps)
	contract.FixedVocabularies = normalizedStrings(contract.FixedVocabularies)
	contract.RequestedCapabilities = normalizedStrings(contract.RequestedCapabilities)
	return contract
}

func normalizedStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = struct{}{}
		}
	}
	normalized := make([]string, 0, len(set))
	for value := range set {
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized
}

func compatMessageClass(decision string) string {
	switch decision {
	case "bootstrap":
		return "compat-bootstrap"
	case "compatible":
		return "compat-compatible"
	case "degraded":
		return "compat-degraded"
	case "server-too-old":
		return "compat-server-too-old"
	case "server-installation-inconsistent":
		return "compat-server-installation-inconsistent"
	default:
		return "compat-client-too-old"
	}
}
