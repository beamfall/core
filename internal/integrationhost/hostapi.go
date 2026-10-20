package integrationhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/beamfall/core/internal/causation"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// ErrPermissionDenied is returned by every Host API method whose backing
// permission is not held-and-approved by the calling plugin. It is the negative
// path the isolation tests target: an ungranted (or unapproved high-risk) call
// fails closed.
var ErrPermissionDenied = errors.New("integrationhost: permission denied")

// Deps bundles the narrow Core seams a Host serves from. A nil seam means the
// corresponding permission is unavailable on this server (the method fails as
// permission-denied even if granted) — the same dormant-capability posture the
// webhook/scrobble plugins use.
type Deps struct {
	Catalog  CatalogReader
	Events   EventReader
	Profiles ProfileLibraryReader
	Secrets  SecretStore
	Audit    Auditor
	Jobs     JobEnqueuer
	Ingest   IngestRequester
	FileMut  FileMutationRequester
	Outbound OutboundProxy
	// Causation is the structural ADR-0125 promotion marker and the shared
	// server-owned guard every mutating Host method spends through. A non-nil
	// mutation seam with a nil cap still fails closed at runtime.
	Causation *causation.CausationCap
	Status    StatusWriter
	Linker    ProfileLinker
}

// StatusWriter records a worker's status / dead-letter write (status-write
// permission, ADR-0058). The worker reports operational status and dead-letter
// entries through this seam; it never touches a status table directly. A nil
// seam leaves the capability dormant (the method fails permission-denied even
// when granted), the same posture every other Host seam uses until the
// composition root adapts it.
type StatusWriter interface {
	WriteStatus(ctx context.Context, pluginID, kind, detail string) error
}

// ProfileLinker records a per-Profile account link a worker establishes on the
// operator's behalf (profile-link permission, high-risk, ADR-0058). The worker
// links an external account to a Profile; it never writes Profile rows directly.
// A nil seam leaves the capability dormant.
type ProfileLinker interface {
	LinkProfile(ctx context.Context, pluginID string, profileID store.ProfileID, ref string) error
}

// ActionRunner dispatches one operator-requested Plugin Action Descriptor to a
// worker-side implementation. The graph layer validates the descriptor and its
// required grants before this seam is called; implementations still receive the
// plugin id so a worker transport can bind the request to the same plugin-scoped
// Host API channel used by the supervisor.
type ActionRunner interface {
	RunAction(ctx context.Context, pluginID, actionID string) error
}

// GrantSource resolves a plugin's current grants. The supervisor backs this with
// the store; tests back it with a fixed map. A grant counts only if Approved —
// a declared-but-unapproved high-risk grant denies the method.
type GrantSource interface {
	Grants(ctx context.Context, pluginID string) ([]Grant, error)
}

type ManifestGrantSource interface {
	GrantSource
	DeclaredPermissions(ctx context.Context, pluginID string) ([]string, error)
}

// Host serves the Integration Host API for one connected worker. It is bound to
// a single pluginID at handshake time so a worker can never act as another
// plugin. Every method checks the plugin's approved grant for the backing
// permission, then delegates to the matching Core seam, auditing high-risk
// methods per call.
type Host struct {
	pluginID string
	deps     Deps
	grants   GrantSource
	now      func() time.Time
}

// NewHost binds a Host to one plugin. now defaults to time.Now.
func NewHost(pluginID string, deps Deps, grants GrantSource, now func() time.Time) *Host {
	if now == nil {
		now = time.Now
	}
	return &Host{pluginID: pluginID, deps: deps, grants: grants, now: now}
}

// authorize returns the plugin's approved grant for kind, or ErrPermissionDenied
// if the plugin lacks an approved grant of that kind. High-risk kinds require
// Approved; this is the single gate every method funnels through.
func (h *Host) authorize(ctx context.Context, kind string) (Grant, error) {
	if !IsKnownPermission(kind) {
		return Grant{}, fmt.Errorf("%w: unknown permission %q", ErrPermissionDenied, kind)
	}
	manifest, ok := h.grants.(ManifestGrantSource)
	if !ok {
		return Grant{}, fmt.Errorf("%w: verified manifest unavailable", ErrPermissionDenied)
	}
	declared, err := manifest.DeclaredPermissions(ctx, h.pluginID)
	if err != nil {
		return Grant{}, err
	}
	if !containsPermission(declared, kind) {
		return Grant{}, fmt.Errorf("%w: %q not declared by verified manifest", ErrPermissionDenied, kind)
	}
	grants, err := h.grants.Grants(ctx, h.pluginID)
	if err != nil {
		return Grant{}, err
	}
	for _, g := range grants {
		if g.Kind != kind {
			continue
		}
		if !g.Approved {
			return Grant{}, fmt.Errorf("%w: %q not approved", ErrPermissionDenied, kind)
		}
		return g, nil
	}
	return Grant{}, fmt.Errorf("%w: %q not granted", ErrPermissionDenied, kind)
}

func containsPermission(permissions []string, kind string) bool {
	for _, permission := range permissions {
		if permission == kind {
			return true
		}
	}
	return false
}

// auditHighRisk writes one audit record for a high-risk method call. Low-risk
// methods are not audited per call (status writes carry their own record).
func (h *Host) auditHighRisk(ctx context.Context, action, targetType, targetID string) error {
	if h.deps.Audit == nil {
		return nil
	}
	return h.deps.Audit.AppendActivity(ctx, store.ActivityEntry{
		Action:     action,
		OccurredAt: h.now().UTC(),
		TargetType: targetType,
		TargetID:   targetID,
	})
}

// ReadCatalog serves Reveal-filtered Library reads. The grant's Reveal-scope
// binding (Profile-bound or operator-level) becomes the store.LibraryFilter;
// the read can never see content the binding excludes. Audited (high-risk).
func (h *Host) ReadCatalog(ctx context.Context) ([]store.Library, error) {
	grant, err := h.authorize(ctx, PermReadCatalog)
	if err != nil {
		return nil, err
	}
	if h.deps.Catalog == nil {
		return nil, fmt.Errorf("%w: read-catalog unavailable", ErrPermissionDenied)
	}
	filter, err := h.resolveFilter(ctx, grant)
	if err != nil {
		return nil, err
	}
	if err := h.auditHighRisk(ctx, store.ActivityPluginCatalogRead, "plugin", h.pluginID); err != nil {
		return nil, err
	}
	return h.deps.Catalog.Libraries(ctx, filter)
}

// SubscribeEvents returns the next batch of outbox events after afterSeq,
// Reveal-filtered through the grant's binding at delivery. (Catalog-level
// filtering of event payloads is the SSE-surface model; here the binding gates
// which events a worker may observe — restricted-Library events are withheld
// from a worker whose filter excludes that Library.)
func (h *Host) SubscribeEvents(ctx context.Context, afterSeq int64, limit int) ([]SubscribedEvent, error) {
	grant, err := h.authorize(ctx, PermEventSubscribe)
	if err != nil {
		return nil, err
	}
	if h.deps.Events == nil {
		return nil, fmt.Errorf("%w: event-subscribe unavailable", ErrPermissionDenied)
	}
	filter, err := h.resolveFilter(ctx, grant)
	if err != nil {
		return nil, err
	}
	events, err := h.deps.Events.ReadEventOutboxFiltered(ctx, afterSeq, limit, filter)
	if err != nil {
		return nil, err
	}
	out := make([]SubscribedEvent, 0, len(events))
	for _, event := range events {
		token := h.deps.Events.CausationToken(event)
		if token == "" {
			token, err = h.causationTokenFor(fmt.Sprintf("outbox:%d", event.Seq), event.OccurredAt)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, SubscribedEvent{OutboxEvent: event, CausationToken: token})
	}
	return out, nil
}

// SubscribedEvent preserves the existing outbox event JSON fields and adds the
// opaque server-issued token a worker must echo on any resulting mutation.
type SubscribedEvent struct {
	store.OutboxEvent
	CausationToken causation.Token `json:"causationToken"`
}

func (h *Host) causationTokenFor(root string, startedAt time.Time) (causation.Token, error) {
	if h.deps.Causation == nil {
		return "", nil
	}
	return h.deps.Causation.TokenFor(root, startedAt)
}

func (h *Host) admitMutation(ctx context.Context, action string) error {
	if h.deps.Causation == nil {
		return fmt.Errorf("%w: %s containment unavailable", ErrPermissionDenied, action)
	}
	return h.deps.Causation.Admit(h.pluginID, causation.TokenFromContext(ctx), action)
}

// resolveFilter turns a read grant into a store.LibraryFilter, reading the bound
// Profile's Library set from the store for a Profile-bound grant. The auth.Scope
// resolution is the reference shape; this package reads profile.Library directly.
func (h *Host) resolveFilter(ctx context.Context, grant Grant) (store.LibraryFilter, error) {
	if grant.AllLibraries {
		return grant.libraryFilter(nil), nil
	}
	if grant.BoundProfileID == "" {
		// No binding at all: fail closed (empty set, sees nothing).
		return grant.libraryFilter(nil), nil
	}
	if h.deps.Profiles == nil {
		return store.LibraryFilter{}, fmt.Errorf("%w: profile binding unavailable", ErrPermissionDenied)
	}
	profile, err := h.deps.Profiles.Profile(ctx, grant.BoundProfileID)
	if err != nil {
		return store.LibraryFilter{}, err
	}
	return grant.libraryFilter(profile.Library), nil
}

// GetSecret reads one plugin-scoped secret. The ref is namespaced per plugin so
// a worker can never read another plugin's or a Core secret. Never logged.
func (h *Host) GetSecret(ctx context.Context, ref string) (string, error) {
	if _, err := h.authorize(ctx, PermPluginSecret); err != nil {
		return "", err
	}
	if h.deps.Secrets == nil {
		return "", fmt.Errorf("%w: plugin-secret unavailable", ErrPermissionDenied)
	}
	scoped, err := h.scopedRef(ref)
	if err != nil {
		return "", err
	}
	return h.deps.Secrets.Secret(scoped)
}

// SetSecret writes one plugin-scoped secret (same namespacing as GetSecret).
func (h *Host) SetSecret(ctx context.Context, ref, value string) error {
	if _, err := h.authorize(ctx, PermPluginSecret); err != nil {
		return err
	}
	if h.deps.Secrets == nil {
		return fmt.Errorf("%w: plugin-secret unavailable", ErrPermissionDenied)
	}
	scoped, err := h.scopedRef(ref)
	if err != nil {
		return err
	}
	return h.deps.Secrets.SetSecret(scoped, value)
}

// scopedRef namespaces a worker-supplied secret ref under its plugin so it can
// never collide with or read another plugin's / a Core secret.
func (h *Host) scopedRef(ref string) (string, error) {
	scoped := plugin.ScopedCredentialRef(plugin.ID(h.pluginID), ref)
	if scoped == "" {
		return "", fmt.Errorf("%w: invalid plugin credential ref", ErrPermissionDenied)
	}
	return scoped, nil
}

// EnqueueJob hands a plugin-owned job to the Core job substrate.
func (h *Host) EnqueueJob(ctx context.Context, kind string, payload []byte, runAfter time.Time) (store.JobID, error) {
	if _, err := h.authorize(ctx, PermPluginJob); err != nil {
		return "", err
	}
	if h.deps.Jobs == nil {
		return "", fmt.Errorf("%w: plugin-job unavailable", ErrPermissionDenied)
	}
	if err := h.admitMutation(ctx, methodEnqueueJob); err != nil {
		return "", err
	}
	return h.deps.Jobs.EnqueuePluginJob(ctx, h.pluginID, kind, payload, runAfter)
}

// RequestIngest submits an ingest handoff (audited, high-risk).
func (h *Host) RequestIngest(ctx context.Context, libraryID store.LibraryID) error {
	if _, err := h.authorize(ctx, PermRequestIngest); err != nil {
		return err
	}
	if h.deps.Ingest == nil {
		return fmt.Errorf("%w: request-ingest unavailable", ErrPermissionDenied)
	}
	if err := h.admitMutation(ctx, methodRequestIngest); err != nil {
		return err
	}
	if err := h.auditHighRisk(ctx, store.ActivityPluginIngestRequest, "plugin", h.pluginID); err != nil {
		return err
	}
	return h.deps.Ingest.RequestIngest(ctx, h.pluginID, libraryID)
}

// RequestFileMutation submits an audited file-mutation request. The worker never
// writes the filesystem directly; this routes through the audited mutation path.
func (h *Host) RequestFileMutation(ctx context.Context, req FileMutationRequest) error {
	if _, err := h.authorize(ctx, PermRequestFileMutation); err != nil {
		return err
	}
	if h.deps.FileMut == nil {
		return fmt.Errorf("%w: request-file-mutation unavailable", ErrPermissionDenied)
	}
	if err := h.admitMutation(ctx, methodRequestFileMutation); err != nil {
		return err
	}
	if err := h.auditHighRisk(ctx, store.ActivityPluginFileMutationRequest, "plugin", h.pluginID); err != nil {
		return err
	}
	return h.deps.FileMut.RequestFileMutation(ctx, h.pluginID, req)
}

// WriteStatus records a worker's status / dead-letter write (status-write
// permission). Low-risk: not audited per call (the status record is the trace).
// A nil Status seam leaves the capability dormant (permission-denied).
func (h *Host) WriteStatus(ctx context.Context, kind, detail string) error {
	if _, err := h.authorize(ctx, PermStatusWrite); err != nil {
		return err
	}
	if h.deps.Status == nil {
		return fmt.Errorf("%w: status-write unavailable", ErrPermissionDenied)
	}
	return h.deps.Status.WriteStatus(ctx, h.pluginID, kind, detail)
}

// LinkProfile records a per-Profile account link (profile-link permission,
// audited high-risk). A nil Linker seam leaves the capability dormant.
func (h *Host) LinkProfile(ctx context.Context, profileID store.ProfileID, ref string) error {
	if _, err := h.authorize(ctx, PermProfileLink); err != nil {
		return err
	}
	if h.deps.Linker == nil {
		return fmt.Errorf("%w: profile-link unavailable", ErrPermissionDenied)
	}
	if h.deps.Audit != nil {
		if err := h.deps.Audit.AppendActivity(ctx, store.ActivityEntry{
			Action:     store.ActivityPluginProfileLink,
			OccurredAt: h.now().UTC(),
			ProfileID:  profileID,
			TargetType: "plugin",
			TargetID:   h.pluginID,
		}); err != nil {
			return err
		}
	}
	return h.deps.Linker.LinkProfile(ctx, h.pluginID, profileID, ref)
}

// Outbound size budgets. maxOutboundBodyBytes is a cheap early-out on the request
// body; maxOutboundFrameBytes caps the fully-marshalled response payload (headers
// included) below maxFrameBytes, leaving envelope headroom so a large-header +
// near-cap-body response is refused before it can overflow a wire frame.
const (
	maxOutboundBodyBytes  = 512 << 10 // 512 KiB request body: outbound is small control/API traffic
	maxOutboundFrameBytes = 768 << 10 // marshalled response cap, safely under maxFrameBytes (1 MiB)
)

// RequestOutbound carries one operator-declared outbound HTTP call for the worker
// through the enforced Host proxy. The worker never dials directly: the Host
// performs the fetch via the Outbound seam, which owns the ADR-0012 §9 SSRF +
// allowlist enforcement (see OutboundProxy). This method is the single chokepoint
// every outbound call funnels through, so its gates are the real guarantees.
//
// Fail-closed order: authorize the coarse declared-outbound grant; require the
// Outbound seam (nil ⇒ dormant permission-denied); require a ledger (nil Audit ⇒
// denied; the audit row is written *before* the proxy is invoked, and an append
// error aborts the call); cap the request body; then delegate. The hardened proxy
// independently requires the exact target in the current manifest-declared AND
// operator-approved origin intersection before transport.
func (h *Host) RequestOutbound(ctx context.Context, req OutboundRequest) (OutboundResponse, error) {
	if _, err := h.authorize(ctx, PermDeclaredOutbound); err != nil {
		return OutboundResponse{}, err
	}
	if h.deps.Outbound == nil {
		return OutboundResponse{}, fmt.Errorf("%w: declared-outbound unavailable", ErrPermissionDenied)
	}
	if h.deps.Audit == nil {
		return OutboundResponse{}, fmt.Errorf("%w: outbound requires an audit ledger", ErrPermissionDenied)
	}
	if len(req.Body) > maxOutboundBodyBytes {
		return OutboundResponse{}, fmt.Errorf("integrationhost: outbound request body too large (%d)", len(req.Body))
	}
	if err := h.admitMutation(ctx, methodRequestOutbound); err != nil {
		return OutboundResponse{}, err
	}
	// Ledger first: the plugin-attributed audit row (no URL/host/header/body — they
	// may carry secrets, invariant 5) must be written before the proxy is invoked.
	if err := h.deps.Audit.AppendActivity(ctx, store.ActivityEntry{
		Action:     store.ActivityPluginOutboundRequest,
		OccurredAt: h.now().UTC(),
		TargetType: "plugin",
		TargetID:   h.pluginID,
	}); err != nil {
		return OutboundResponse{}, err
	}
	resp, err := h.deps.Outbound.RequestOutbound(ctx, h.pluginID, req)
	if err != nil {
		return OutboundResponse{}, err
	}
	marshalled, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		return OutboundResponse{}, fmt.Errorf("integrationhost: outbound response marshal failed: %w", marshalErr)
	}
	if len(marshalled) > maxOutboundFrameBytes {
		return OutboundResponse{}, fmt.Errorf("integrationhost: outbound response too large (%d)", len(marshalled))
	}
	return resp, nil
}
