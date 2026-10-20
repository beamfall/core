package integrationhost

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// Plugin Lifecycle Actions are server-owned: a Manager/Console
// REQUESTS one, the server performs the filesystem/worker change and writes
// status/audit. The closed action vocabulary:
const (
	ActionCheckUpdate    = "check-update"
	ActionStageUpdate    = "stage-update"
	ActionApplyUpdate    = "apply-update"
	ActionRestartWorker  = "restart-worker"
	ActionDisable        = "disable"
	ActionTestConnection = "test-connection"
	ActionRunAction      = "run-action"
)

// WorkerState is the persisted runtime state of a plugin's worker, surfaced in
// the Plugin Surface status. Pure data.
type WorkerState struct {
	PluginID       string
	Version        string
	ArtifactSHA256 string
	SigningKeyID   string
	State          string // "installed", "running", "stopped", "update-staged", "disabled", "failed"
	LastError      string
}

// LifecycleStore persists worker/grant/update-source state. The composition root
// backs it with *store.Store; tests back it with an in-memory fake. Defined at
// the consumer.
type LifecycleStore interface {
	WorkerState(ctx context.Context, pluginID string) (WorkerState, bool, error)
	SetWorkerState(ctx context.Context, state WorkerState, now time.Time) error
	Grants(ctx context.Context, pluginID string) ([]Grant, error)
	SetGrant(ctx context.Context, grant Grant, now time.Time) error
	UpdateSource(ctx context.Context, pluginID string) (UpdateSource, bool, error)
	OutboundPolicy(ctx context.Context, pluginID string) (plugin.OutboundPolicy, bool, error)
	SetOutboundPolicy(ctx context.Context, policy plugin.OutboundPolicy, now time.Time) error
}

// UpdateSource is the operator-configured, per-plugin update origin (ADR-0058 —
// NOT a marketplace or global catalog). Pinned by artifact hash or signing key.
type UpdateSource struct {
	PluginID string
	// Kind is "url" or "folder".
	Kind string
	// Location is the URL or local folder path.
	Location string
	// PinKind is PinHash or PinSigningKey; Pin is the hex hash or signing-key id.
	PinKind    string
	Pin        string
	SigningKey string
	Signature  string
}

// VerifiedUpdateCandidate is metadata derived by the server from a candidate
// artifact after its configured Update Source pin succeeds. Graph/client input
// cannot construct the lifecycle transition directly.
type VerifiedUpdateCandidate struct {
	PluginID              string
	Version               string
	ArtifactSHA256        string
	ManifestBinding       string
	Permissions           []string
	ActiveManifestBinding string
	ActivePermissions     []string
}

type UpdateCandidateVerifier func(context.Context, string) (VerifiedUpdateCandidate, error)

type GrantApprovalBindingStore interface {
	GrantApprovalBinding(ctx context.Context, pluginID, kind string) (string, bool, error)
	SetGrantApprovalBinding(ctx context.Context, pluginID, kind, binding string) error
}

type ActiveManifestGrantSource interface {
	ManifestGrantSource
	ActiveManifestBinding(ctx context.Context, pluginID string) (string, bool, error)
}

// ErrOutboundPolicyMissing is returned when an operator attempts to approve an
// origin before the server has registered that plugin's manifest declaration.
var ErrOutboundPolicyMissing = errors.New("integrationhost: outbound declaration is not registered")

var ErrAuditUnavailable = errors.New("integrationhost: audit ledger unavailable")

var ErrUpdateVerifierUnavailable = errors.New("integrationhost: verified update candidate unavailable")

// Lifecycle performs server-owned lifecycle actions and writes status + audit.
// It is the only path that mutates worker/grant/update-source state in response
// to a Manager/Console request.
type Lifecycle struct {
	store        LifecycleStore
	audit        Auditor
	now          func() time.Time
	mu           sync.Mutex
	publicIP     func(net.IP) bool
	verifyUpdate UpdateCandidateVerifier
}

// SetPublicRoutabilityPredicate installs Core's single RFC 6890 predicate.
// The lifecycle is the policy source already shared by every production
// outbound proxy, so no integrationhost-to-scrape dependency is required.
func (l *Lifecycle) SetPublicRoutabilityPredicate(predicate func(net.IP) bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.publicIP = predicate
}

// IsPublicRoutableIP fails closed until the composition root installs Core's
// centralized predicate.
func (l *Lifecycle) IsPublicRoutableIP(ip net.IP) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.publicIP != nil && l.publicIP(ip)
}

// NewLifecycle builds the controller. now defaults to time.Now.
func NewLifecycle(ls LifecycleStore, audit Auditor, now func() time.Time) *Lifecycle {
	if now == nil {
		now = time.Now
	}
	return &Lifecycle{store: ls, audit: audit, now: now}
}

func (l *Lifecycle) SetUpdateCandidateVerifier(verifier UpdateCandidateVerifier) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.verifyUpdate = verifier
}

// ApproveGrant approves a (high-risk) permission grant and audits it. A
// previously unapproved grant becomes usable; this is the human gate ADR-0058
// requires before a high-risk Host API method works.
func (l *Lifecycle) ApproveGrant(ctx context.Context, grant Grant) error {
	grant.Approved = true
	if err := validateGrant(grant); err != nil {
		return err
	}
	manifest, ok := l.store.(ManifestGrantSource)
	if !ok {
		return fmt.Errorf("%w: verified manifest unavailable", ErrPermissionDenied)
	}
	declared, err := manifest.DeclaredPermissions(ctx, grant.PluginID)
	if err != nil {
		return err
	}
	if !containsPermission(declared, grant.Kind) {
		return fmt.Errorf("%w: %q not declared by verified manifest", ErrPermissionDenied, grant.Kind)
	}
	var bindingStore GrantApprovalBindingStore
	var binding string
	if IsHighRisk(grant.Kind) {
		active, ok := l.store.(ActiveManifestGrantSource)
		if !ok {
			return fmt.Errorf("%w: verified manifest binding unavailable", ErrPermissionDenied)
		}
		binding, ok, err = active.ActiveManifestBinding(ctx, grant.PluginID)
		if err != nil {
			return err
		}
		if !ok || !validArtifactDigest(binding) {
			return fmt.Errorf("%w: verified manifest binding unavailable", ErrPermissionDenied)
		}
		bindingStore, ok = l.store.(GrantApprovalBindingStore)
		if !ok {
			return fmt.Errorf("%w: approval binding store unavailable", ErrPermissionDenied)
		}
	}
	if err := l.writeAudit(ctx, store.ActivityPluginGrant, grant.PluginID); err != nil {
		return err
	}
	if bindingStore != nil {
		// Clear any prior marker before making the row approved. If either
		// persistence step fails, the grant remains ineffective.
		if err := bindingStore.SetGrantApprovalBinding(ctx, grant.PluginID, grant.Kind, ""); err != nil {
			return err
		}
	}
	if err := l.store.SetGrant(ctx, grant, l.now().UTC()); err != nil {
		return err
	}
	if bindingStore != nil {
		if err := bindingStore.SetGrantApprovalBinding(ctx, grant.PluginID, grant.Kind, binding); err != nil {
			return err
		}
	}
	return nil
}

// RegisterOutboundDeclaration records the current manifest claim. Approvals are
// retained only for unchanged origins; removed and newly-added origins are
// denied until an operator explicitly approves them.
func (l *Lifecycle) RegisterOutboundDeclaration(ctx context.Context, declaration plugin.OutboundDeclaration) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	current, ok, err := l.store.OutboundPolicy(ctx, string(declaration.PluginID))
	if err != nil {
		return err
	}
	var next plugin.OutboundPolicy
	if ok {
		next, err = current.Reconcile(declaration)
	} else {
		next, err = plugin.NewOutboundPolicy(declaration, nil)
	}
	if err != nil {
		return err
	}
	return l.store.SetOutboundPolicy(ctx, next, l.now().UTC())
}

// ApproveOutboundOrigin is the operator control-plane step for one declared
// origin. A source-specific request audit must succeed before consent becomes
// executable policy.
func (l *Lifecycle) ApproveOutboundOrigin(ctx context.Context, pluginID, origin string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	current, ok, err := l.store.OutboundPolicy(ctx, pluginID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w for plugin %q", ErrOutboundPolicyMissing, pluginID)
	}
	next, err := current.Approve(origin)
	if err != nil {
		return err
	}
	normalized, err := plugin.NormalizeOutboundOrigin(origin)
	if err != nil {
		return err
	}
	if current.Allows(normalized) {
		return nil
	}
	if err := l.writeOutboundPolicyRequestAudit(ctx, pluginID, normalized, "approve"); err != nil {
		return err
	}
	return l.store.SetOutboundPolicy(ctx, next, l.now().UTC())
}

// RevokeOutboundOrigin removes one origin from executable policy. Revocation is
// preceded by a source-specific request audit, so every effective revocation
// has a durable operator record. A persistence failure leaves a truthful request
// record but does not claim that the policy changed.
func (l *Lifecycle) RevokeOutboundOrigin(ctx context.Context, pluginID, origin string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	current, ok, err := l.store.OutboundPolicy(ctx, pluginID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w for plugin %q", ErrOutboundPolicyMissing, pluginID)
	}
	next, err := current.Revoke(origin)
	if err != nil {
		return err
	}
	normalized, err := plugin.NormalizeOutboundOrigin(origin)
	if err != nil {
		return err
	}
	if !current.Allows(normalized) {
		return nil
	}
	if err := l.writeOutboundPolicyRequestAudit(ctx, pluginID, normalized, "revoke"); err != nil {
		return err
	}
	return l.store.SetOutboundPolicy(ctx, next, l.now().UTC())
}

// OutboundPolicy returns the current read-only runtime policy. The proxy reloads
// it for every request, so approval and revocation take effect without restart.
func (l *Lifecycle) OutboundPolicy(ctx context.Context, pluginID string) (plugin.OutboundPolicy, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.store.OutboundPolicy(ctx, pluginID)
}

// ApplyUpdate obtains one server-verified candidate from the configured Update
// Source. Newly introduced permissions remain unapproved and stage the worker
// until reapproval; permissions absent from the candidate are revoked. Audit
// succeeds before any grant or worker state is changed.
func (l *Lifecycle) ApplyUpdate(ctx context.Context, pluginID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.verifyUpdate == nil {
		return ErrUpdateVerifierUnavailable
	}
	candidate, err := l.verifyUpdate(ctx, pluginID)
	if err != nil {
		return err
	}
	if err := validateUpdateCandidate(pluginID, candidate); err != nil {
		return err
	}
	grants, err := l.store.Grants(ctx, pluginID)
	if err != nil {
		return err
	}
	if err := l.writeAudit(ctx, store.ActivityPluginUpdate, pluginID); err != nil {
		return err
	}
	bindingStore, ok := l.store.(GrantApprovalBindingStore)
	if !ok {
		return errors.New("integrationhost: grant approval binding store unavailable")
	}
	current := make(map[string]Grant, len(grants))
	for _, g := range grants {
		current[g.Kind] = g
	}
	desired := make(map[string]bool, len(candidate.Permissions))
	active := make(map[string]bool, len(candidate.ActivePermissions))
	for _, kind := range candidate.ActivePermissions {
		active[kind] = true
	}
	requiresReapproval := false
	for _, kind := range candidate.Permissions {
		desired[kind] = true
		grant, exists := current[kind]
		if !exists {
			grant = Grant{PluginID: pluginID, Kind: kind}
		}
		if IsHighRisk(kind) {
			binding, bound, err := bindingStore.GrantApprovalBinding(ctx, pluginID, kind)
			if err != nil {
				return err
			}
			retainedApproval := exists && grant.Approved && active[kind] && bound &&
				binding == candidate.ActiveManifestBinding && candidate.ActiveManifestBinding != ""
			if !retainedApproval {
				grant.Approved = false
				requiresReapproval = true
			}
			nextBinding := ""
			if retainedApproval {
				nextBinding = candidate.ManifestBinding
			}
			if err := bindingStore.SetGrantApprovalBinding(ctx, pluginID, kind, nextBinding); err != nil {
				return err
			}
		} else if !exists {
			grant.Approved = false
		}
		if err := l.store.SetGrant(ctx, grant, l.now().UTC()); err != nil {
			return err
		}
	}
	for _, grant := range grants {
		if desired[grant.Kind] || !grant.Approved {
			continue
		}
		grant.Approved = false
		if IsHighRisk(grant.Kind) {
			if err := bindingStore.SetGrantApprovalBinding(ctx, pluginID, grant.Kind, ""); err != nil {
				return err
			}
		}
		if err := l.store.SetGrant(ctx, grant, l.now().UTC()); err != nil {
			return err
		}
	}
	state, _, err := l.store.WorkerState(ctx, pluginID)
	if err != nil {
		return err
	}
	state.PluginID = pluginID
	state.Version = candidate.Version
	state.ArtifactSHA256 = candidate.ArtifactSHA256
	state.State = "installed"
	if requiresReapproval {
		state.State = "update-staged"
	}
	state.LastError = ""
	if err := l.store.SetWorkerState(ctx, state, l.now().UTC()); err != nil {
		return err
	}
	return nil
}

func validateUpdateCandidate(pluginID string, candidate VerifiedUpdateCandidate) error {
	if candidate.PluginID != pluginID {
		return fmt.Errorf("integrationhost: verified candidate plugin id %q does not match %q", candidate.PluginID, pluginID)
	}
	if candidate.Version == "" {
		return errors.New("integrationhost: verified candidate identity is incomplete")
	}
	digest, err := hex.DecodeString(candidate.ArtifactSHA256)
	if err != nil || len(digest) != 32 || candidate.ArtifactSHA256 != strings.ToLower(candidate.ArtifactSHA256) {
		return errors.New("integrationhost: verified candidate artifact digest is not lowercase sha256")
	}
	if !validArtifactDigest(candidate.ManifestBinding) {
		return errors.New("integrationhost: verified candidate manifest binding is invalid")
	}
	if candidate.ActiveManifestBinding != "" && !validArtifactDigest(candidate.ActiveManifestBinding) {
		return errors.New("integrationhost: active manifest binding is invalid")
	}
	seen := map[string]bool{}
	for _, kind := range candidate.Permissions {
		if !IsKnownPermission(kind) {
			return fmt.Errorf("integrationhost: verified candidate declares unknown permission %q", kind)
		}
		if seen[kind] {
			return fmt.Errorf("integrationhost: verified candidate repeats permission %q", kind)
		}
		seen[kind] = true
	}
	activeSeen := map[string]bool{}
	for _, kind := range candidate.ActivePermissions {
		if !IsKnownPermission(kind) {
			return fmt.Errorf("integrationhost: active manifest declares unknown permission %q", kind)
		}
		if activeSeen[kind] {
			return fmt.Errorf("integrationhost: active manifest repeats permission %q", kind)
		}
		activeSeen[kind] = true
	}
	return nil
}

// Disable marks a worker disabled (the server stops launching it). Audited as a
// state change via the worker row; activation/deactivation itself is audited by
// the existing ActivityPluginActivation path in the Console.
func (l *Lifecycle) Disable(ctx context.Context, pluginID string) error {
	state, _, err := l.store.WorkerState(ctx, pluginID)
	if err != nil {
		return err
	}
	state.PluginID = pluginID
	state.State = "disabled"
	return l.store.SetWorkerState(ctx, state, l.now().UTC())
}

func (l *Lifecycle) writeAudit(ctx context.Context, action, pluginID string) error {
	if l.audit == nil {
		return ErrAuditUnavailable
	}
	return l.audit.AppendActivity(ctx, store.ActivityEntry{
		Action:     action,
		OccurredAt: l.now().UTC(),
		TargetType: "plugin",
		TargetID:   pluginID,
	})
}

func (l *Lifecycle) writeOutboundPolicyRequestAudit(ctx context.Context, pluginID, origin, operation string) error {
	if l.audit == nil {
		return ErrAuditUnavailable
	}
	return l.audit.AppendActivity(ctx, store.ActivityEntry{
		Action:     store.ActivityPluginGrant,
		OccurredAt: l.now().UTC(),
		TargetType: "plugin_outbound_origin." + operation + "_requested",
		TargetID:   pluginID + "|" + origin,
	})
}
