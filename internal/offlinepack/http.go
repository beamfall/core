package offlinepack

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

const keyAuditOperation = "http:GET /api/pack/key/"

// ErrPackNotServable is returned when a pack's live state denies byte/material
// serving (revoked, expired, or not yet ready).
var ErrPackNotServable = errors.New("offline pack not servable")

// Scoper resolves a request session into an auth.Scope (satisfied by
// *auth.Service). The seam keeps the handler's auth coupling explicit.
type Scoper interface {
	ScopeFromRequest(ctx context.Context, request *http.Request) (auth.Scope, error)
}

// HTTPHandlers wires an *auth.Service and the Service into http.HandlerFunc
// values mountable via the server's SetProxyTee-style seam, so server never
// imports offlinepack.
type HTTPHandlers struct {
	svc   *Service
	auth  Scoper
	audit AuditRecorder
}

func NewHTTPHandlers(svc *Service, scoper Scoper) *HTTPHandlers {
	return &HTTPHandlers{
		svc:   svc,
		auth:  scoper,
		audit: newSlogAuditRecorder(nil),
	}
}

func (h *HTTPHandlers) SetAuditRecorder(recorder AuditRecorder) {
	if recorder == nil {
		recorder = newSlogAuditRecorder(nil)
	}
	h.audit = recorder
}

type AuthorityDocument struct {
	V          int               `json:"v"`
	Issuer     string            `json:"issuer"`
	ServerTime int64             `json:"server_time"`
	Keys       map[string]string `json:"keys"`
}

// Authority serves the authenticated trust bootstrap used to validate Offline
// Pack grant, manifest, and status JWS envelopes.
func (h *HTTPHandlers) Authority(w http.ResponseWriter, r *http.Request) {
	if _, err := h.auth.ScopeFromRequest(r.Context(), r); err != nil {
		http.Error(w, "invalid session", http.StatusUnauthorized)
		return
	}
	if h.svc == nil || h.svc.authority == nil {
		http.Error(w, "offline pack authority unavailable", http.StatusServiceUnavailable)
		return
	}
	document := AuthorityDocument{
		V:          1,
		Issuer:     h.svc.authority.Issuer(),
		ServerTime: h.svc.now().UTC().Unix(),
		Keys:       h.svc.authority.PublicKeyDocument(),
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(document); err != nil {
		http.Error(w, "offline pack authority unavailable", http.StatusInternalServerError)
	}
}

// authorize runs the dual check shared by the byte routes: (1) the signed
// Offline Pack grant verifies against its exact stored bindings and (2) the
// pack is the caller's and currently servable (state + expiry), with
// the per-member Reveal/seal re-checked live. A token that still verifies
// cleanly is denied here once the pack is revoked, expired, relocked, hidden, or
// sealed — denial is enforced at the serving layer, not just at token verify.
func (h *HTTPHandlers) authorize(r *http.Request, prefix string) (store.OfflinePack, auth.Scope, GrantClaims, int, string) {
	token := strings.TrimPrefix(r.URL.Path, prefix)
	token = strings.Trim(token, "/")
	return h.authorizeToken(r, token)
}

func (h *HTTPHandlers) authorizeToken(r *http.Request, token string) (store.OfflinePack, auth.Scope, GrantClaims, int, string) {
	if token == "" {
		return store.OfflinePack{}, auth.Scope{}, GrantClaims{}, http.StatusUnauthorized, "missing offline pack grant"
	}
	scope, err := h.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		return store.OfflinePack{}, auth.Scope{}, GrantClaims{}, http.StatusUnauthorized, "invalid session"
	}
	if h.svc.authority == nil {
		return store.OfflinePack{}, auth.Scope{}, GrantClaims{}, http.StatusServiceUnavailable, "offline pack authority unavailable"
	}
	authenticated, err := h.svc.authority.AuthenticateGrant(token)
	if err != nil {
		return store.OfflinePack{}, auth.Scope{}, GrantClaims{}, http.StatusUnauthorized, "invalid offline pack grant"
	}
	unbound := authenticated.Grant()
	// (2) Live pack-state re-check. Pack must be the caller's.
	pack, err := h.svc.store.OfflinePackByID(r.Context(), store.OfflinePackID(unbound.PackID), scope.ProfileID)
	if err != nil {
		return store.OfflinePack{}, auth.Scope{}, GrantClaims{}, http.StatusNotFound, "unknown offline pack"
	}
	libraryIDs := make([]string, len(pack.LibraryIDs))
	for i := range pack.LibraryIDs {
		libraryIDs[i] = string(pack.LibraryIDs[i])
	}
	if pack.State == store.OfflinePackRevoked || pack.State == store.OfflinePackExpired {
		// Terminal denial wins over the now-stale revocation generation in the
		// presented grant, while still disclosing nothing before ownership.
		return store.OfflinePack{}, auth.Scope{}, GrantClaims{}, http.StatusGone, "offline pack revoked"
	}
	grant, _, _, err := h.svc.authority.VerifyAuthenticatedGrant(authenticated, GrantExpectations{
		Issuer: h.svc.authority.Issuer(), PackID: string(pack.ID), ProfileID: string(scope.ProfileID),
		LibraryIDs: libraryIDs, DeviceEnrollmentID: string(scope.DeviceID),
		DeviceKeyThumbprint: pack.DeviceKeyThumbprint, ManifestRevision: pack.ManifestRevision,
		ManifestSHA256: pack.ManifestSHA256, Rev: packRevocations(pack), TrustedNow: h.svc.now(),
	})
	if err != nil {
		return store.OfflinePack{}, auth.Scope{}, GrantClaims{}, http.StatusUnauthorized, "invalid offline pack grant"
	}
	if !h.svc.servable(pack) {
		// Not-yet-ready or lazily-expired: deny without a terminal verdict.
		return store.OfflinePack{}, auth.Scope{}, GrantClaims{}, http.StatusForbidden, "offline pack not servable"
	}
	// Live Reveal re-check: a restricted pack requires the session to currently
	// hold reveal. Re-derive from live membership so a member hidden after issue
	// also denies.
	restricted, err := h.svc.packRestricted(r.Context(), pack, scope.LibraryFilter())
	if err != nil {
		return store.OfflinePack{}, auth.Scope{}, GrantClaims{}, http.StatusInternalServerError, "offline pack unavailable"
	}
	if restricted && !scope.Revealed {
		return store.OfflinePack{}, auth.Scope{}, GrantClaims{}, http.StatusForbidden, "offline pack requires reveal"
	}
	// Live Sealed re-check (ADR-0098 area 2): a Sealed Library serves NOTHING.
	// Re-derive Sealed state from the encryption seam so a member sealed AFTER
	// the grant was minted is denied, fail-closed and unconditionally (Reveal
	// does NOT lift Sealed, unlike restricted).
	sealed, err := h.svc.PackHasSealedMember(r.Context(), pack)
	if err != nil {
		return store.OfflinePack{}, auth.Scope{}, GrantClaims{}, http.StatusInternalServerError, "offline pack unavailable"
	}
	if sealed {
		return store.OfflinePack{}, auth.Scope{}, GrantClaims{}, http.StatusForbidden, "offline pack sealed"
	}
	return pack, scope, grant, http.StatusOK, ""
}

// Manifest serves the encrypted-at-rest-eligible manifest blob for a pack after
// the dual check. Mount at /api/pack/manifest/.
func (h *HTTPHandlers) Manifest(w http.ResponseWriter, r *http.Request) {
	pack, _, _, status, msg := h.authorize(r, "/api/pack/manifest/")
	if status != http.StatusOK {
		http.Error(w, msg, status)
		return
	}
	w.Header().Set("Content-Type", "application/jose")
	_, _ = w.Write([]byte(pack.Manifest))
}

// Content serves one frozen Media member's encrypted BFOP bytes.
func (h *HTTPHandlers) Content(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/pack/content/")
	token, mediaIDRaw, ok := strings.Cut(rest, "/")
	if !ok || token == "" || mediaIDRaw == "" {
		http.Error(w, "missing stream token", http.StatusUnauthorized)
		return
	}
	pack, scope, _, status, msg := h.authorizeToken(r, token)
	if status != http.StatusOK {
		http.Error(w, msg, status)
		return
	}
	mediaID := store.MediaID(strings.Trim(mediaIDRaw, "/"))
	items, err := h.svc.store.OfflinePackItems(r.Context(), pack.ID)
	if err != nil {
		http.Error(w, "offline pack unavailable", http.StatusInternalServerError)
		return
	}
	var found store.OfflinePackItem
	for _, item := range items {
		if item.MediaID == mediaID {
			found = item
			break
		}
	}
	if found.MediaID == "" {
		http.Error(w, "unknown offline pack item", http.StatusNotFound)
		return
	}
	mediaRows, err := h.svc.store.VisibleMediaBatch(r.Context(), []store.MediaID{mediaID}, scope.PlaybackFilter())
	if err != nil {
		http.Error(w, "media unavailable", http.StatusInternalServerError)
		return
	}
	media, ok := mediaRows[mediaID]
	if !ok {
		http.Error(w, "media unavailable", http.StatusNotFound)
		return
	}
	_ = media
	f, err := os.Open(found.CiphertextPath) //nolint:gosec // store-owned pack path
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		http.Error(w, "media file unavailable", status)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "media file unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.beamfall.offline-pack")
	http.ServeContent(w, r, filepath.Base(found.CiphertextPath), info.ModTime(), f)
}

// Key serves the fixed 153-byte BFKW envelope only after the pack checks, an
// active Admin Session check, and a mandatory audit record. Audit failure is a
// fail-closed serving failure: key material is never written first.
func (h *HTTPHandlers) Key(w http.ResponseWriter, r *http.Request) {
	pack, scope, _, status, msg := h.authorize(r, "/api/pack/key/")
	if status != http.StatusOK {
		h.writeKeyError(w, r, scope, status, msg, AuditRefused)
		return
	}
	if !scope.Admin {
		h.writeKeyError(
			w,
			r,
			scope,
			http.StatusForbidden,
			"offline pack key requires admin session",
			AuditRefused,
		)
		return
	}
	if len(pack.WrappedCEK) != WrappedEnvelopeSize {
		h.writeKeyError(w, r, scope, http.StatusNotFound, "no key material", AuditError)
		return
	}
	if err := h.recordKeyAudit(r.Context(), scope, AuditSuccess); err != nil {
		http.Error(w, "offline pack key audit unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.beamfall.offline-pack-key")
	_, _ = w.Write(pack.WrappedCEK)
}

func (h *HTTPHandlers) writeKeyError(
	w http.ResponseWriter,
	r *http.Request,
	scope auth.Scope,
	status int,
	message string,
	outcome AuditOutcome,
) {
	if err := h.recordKeyAudit(r.Context(), scope, outcome); err != nil {
		http.Error(w, "offline pack key audit unavailable", http.StatusServiceUnavailable)
		return
	}
	http.Error(w, message, status)
}

func (h *HTTPHandlers) recordKeyAudit(
	ctx context.Context,
	scope auth.Scope,
	outcome AuditOutcome,
) error {
	recorder := h.audit
	if recorder == nil {
		recorder = newSlogAuditRecorder(nil)
	}
	return recorder.RecordAudit(ctx, AuditRecord{
		Actor: AuditActor{
			Authenticated: scope.SessionID != "" || scope.APITokenID != "" || scope.APIKeyID != "",
			SessionID:     string(scope.SessionID),
			DeviceID:      string(scope.DeviceID),
			ProfileID:     string(scope.ProfileID),
			APITokenID:    scope.APITokenID,
			APIKeyID:      scope.APIKeyID,
		},
		Operation: keyAuditOperation,
		Outcome:   outcome,
	})
}

func (h *HTTPHandlers) Status(w http.ResponseWriter, r *http.Request) {
	token := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/pack/status/"), "/")
	scope, err := h.auth.ScopeFromRequest(r.Context(), r)
	if err != nil || token == "" || h.svc.authority == nil {
		http.Error(w, "invalid offline pack status request", http.StatusUnauthorized)
		return
	}
	var grant GrantClaims
	if _, err := verifyCompact(token, GrantType, h.svc.authority.PublicKeys(), &grant, grantClaimKeys); err != nil {
		http.Error(w, "invalid offline pack grant", http.StatusUnauthorized)
		return
	}
	if err := validateGrantShape(grant); err != nil || grant.ProfileID != string(scope.ProfileID) ||
		grant.DeviceEnrollmentID != string(scope.DeviceID) {
		http.Error(w, "invalid offline pack grant", http.StatusUnauthorized)
		return
	}
	pack, err := h.svc.store.OfflinePackByID(r.Context(), store.OfflinePackID(grant.PackID), scope.ProfileID)
	if err != nil {
		http.Error(w, "unknown offline pack", http.StatusNotFound)
		return
	}
	envelope, err := h.svc.IssueStatus(r.Context(), scope, pack.ID)
	if err != nil {
		http.Error(w, "offline pack status unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/jose")
	_, _ = w.Write([]byte(envelope)) //nolint:gosec // G705: compact-JWS envelope served as application/jose, never rendered as HTML
}

// ParseMediaRefs splits a comma/space-separated list of Media ids into a
// deduplicated, order-preserving slice. Used by the generic explicit-media
// resolver.
func ParseMediaRefs(ref string) []store.MediaID {
	fields := strings.FieldsFunc(ref, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
	out := make([]store.MediaID, 0, len(fields))
	seen := map[store.MediaID]bool{}
	for _, f := range fields {
		id := store.MediaID(strings.TrimSpace(f))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
