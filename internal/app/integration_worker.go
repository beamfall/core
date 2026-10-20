package app

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/causation"
	"github.com/beamfall/core/internal/integrationhost"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// Integration Worker runtime wiring. The composition root adapts
// *store.Store and the 0600 secret store onto the narrow integrationhost seams,
// exactly as it does for the webhook and scrobble Integration Plugins. The
// runtime is dormant until an operator installs a worker artifact (no installed
// worker → no launch), so a server with no third-party plugins pays nothing.
//
// The adapters live here, not in integrationhost, so that package imports only
// store + plugin (the arch whitelist row): it never depends on the composition
// root, and the Reveal/secret/audit seams are satisfied from the one place that
// already owns *store.Store and the secret files.

// integrationGrantStore adapts *store.Store to integrationhost.GrantSource and
// integrationhost.LifecycleStore, mapping the store-native rows onto the runtime
// contract types.
type integrationGrantStore struct {
	st                  *store.Store
	declaredPermissions []string
	manifestBinding     string
}

func (g integrationGrantStore) Grants(ctx context.Context, pluginID string) ([]integrationhost.Grant, error) {
	rows, err := g.st.PluginGrants(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	out := make([]integrationhost.Grant, 0, len(rows))
	for _, r := range rows {
		approved := r.Approved
		if approved && integrationhost.IsHighRisk(r.Kind) && g.declaredPermissions != nil {
			binding, ok, bindingErr := integrationhost.GrantApprovalBinding(g.st.IntegrationArtifactRoot(), pluginID, r.Kind)
			if bindingErr != nil {
				return nil, bindingErr
			}
			approved = ok && binding == g.manifestBinding && g.manifestBinding != ""
		}
		out = append(out, integrationhost.Grant{
			PluginID:       r.PluginID,
			Kind:           r.Kind,
			Approved:       approved,
			BoundProfileID: r.BoundProfileID,
			AllLibraries:   r.AllLibraries,
		})
	}
	return out, nil
}

func (g integrationGrantStore) DeclaredPermissions(context.Context, string) ([]string, error) {
	return append([]string(nil), g.declaredPermissions...), nil
}

func (g integrationGrantStore) ActiveManifestBinding(context.Context, string) (string, bool, error) {
	return g.manifestBinding, g.manifestBinding != "", nil
}

func (g integrationGrantStore) GrantApprovalBinding(_ context.Context, pluginID, kind string) (string, bool, error) {
	return integrationhost.GrantApprovalBinding(g.st.IntegrationArtifactRoot(), pluginID, kind)
}

func (g integrationGrantStore) SetGrantApprovalBinding(_ context.Context, pluginID, kind, binding string) error {
	return integrationhost.SetGrantApprovalBinding(g.st.IntegrationArtifactRoot(), pluginID, kind, binding)
}

func (g integrationGrantStore) SetGrant(ctx context.Context, grant integrationhost.Grant, now time.Time) error {
	return g.st.SetPluginGrant(ctx, store.PluginGrantRow{
		PluginID:       grant.PluginID,
		Kind:           grant.Kind,
		Approved:       grant.Approved,
		BoundProfileID: grant.BoundProfileID,
		AllLibraries:   grant.AllLibraries,
	}, now)
}

func (g integrationGrantStore) OutboundPolicy(ctx context.Context, pluginID string) (plugin.OutboundPolicy, bool, error) {
	values, err := g.st.PluginConfigValues(ctx, pluginID)
	if err != nil {
		return plugin.OutboundPolicy{}, false, err
	}
	raw, ok := values[plugin.ReservedOutboundPolicyConfigKey]
	if !ok {
		return plugin.OutboundPolicy{}, false, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var policy plugin.OutboundPolicy
	if err := decoder.Decode(&policy); err != nil {
		return plugin.OutboundPolicy{}, false, fmt.Errorf("integrationhost: decode outbound policy for plugin %q: %w", pluginID, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return plugin.OutboundPolicy{}, false, fmt.Errorf("integrationhost: outbound policy for plugin %q has trailing JSON", pluginID)
		}
		return plugin.OutboundPolicy{}, false, fmt.Errorf("integrationhost: decode outbound policy for plugin %q: %w", pluginID, err)
	}
	normalized, err := plugin.NewOutboundPolicy(
		plugin.OutboundDeclaration{PluginID: policy.PluginID, Origins: policy.Declared},
		policy.Approved,
	)
	if err != nil {
		return plugin.OutboundPolicy{}, false, fmt.Errorf("integrationhost: validate outbound policy for plugin %q: %w", pluginID, err)
	}
	if normalized.PluginID != plugin.ID(pluginID) {
		return plugin.OutboundPolicy{}, false, fmt.Errorf("integrationhost: outbound policy plugin %q does not match %q", normalized.PluginID, pluginID)
	}
	return normalized, true, nil
}

func (g integrationGrantStore) SetOutboundPolicy(ctx context.Context, policy plugin.OutboundPolicy, now time.Time) error {
	normalized, err := plugin.NewOutboundPolicy(
		plugin.OutboundDeclaration{PluginID: policy.PluginID, Origins: policy.Declared},
		policy.Approved,
	)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	return g.st.SetPluginConfigValues(ctx, string(normalized.PluginID), map[string]string{
		plugin.ReservedOutboundPolicyConfigKey: string(raw),
	}, nil, now)
}

func (g integrationGrantStore) WorkerState(ctx context.Context, pluginID string) (integrationhost.WorkerState, bool, error) {
	row, ok, err := g.st.IntegrationWorker(ctx, pluginID)
	if err != nil || !ok {
		return integrationhost.WorkerState{}, ok, err
	}
	return integrationhost.WorkerState{
		PluginID:       row.PluginID,
		Version:        row.Version,
		ArtifactSHA256: row.ArtifactSHA256,
		SigningKeyID:   row.SigningKeyID,
		State:          row.State,
		LastError:      row.LastError,
	}, true, nil
}

func (g integrationGrantStore) SetWorkerState(ctx context.Context, state integrationhost.WorkerState, now time.Time) error {
	return g.st.SetIntegrationWorker(ctx, store.IntegrationWorkerRow{
		PluginID:       state.PluginID,
		Version:        state.Version,
		ArtifactSHA256: state.ArtifactSHA256,
		SigningKeyID:   state.SigningKeyID,
		State:          state.State,
		LastError:      state.LastError,
	}, now)
}

func (g integrationGrantStore) UpdateSource(ctx context.Context, pluginID string) (integrationhost.UpdateSource, bool, error) {
	row, ok, err := g.st.PluginUpdateSource(ctx, pluginID)
	if err != nil || !ok {
		return integrationhost.UpdateSource{}, ok, err
	}
	return integrationhost.UpdateSource{
		PluginID:   row.PluginID,
		Kind:       row.Kind,
		Location:   row.Location,
		PinKind:    row.PinKind,
		Pin:        row.Pin,
		SigningKey: row.SigningKey,
		Signature:  row.Signature,
	}, true, nil
}

// IntegrationEventReader adapts the store's worker-filtered event tail to the
// integrationhost.EventReader seam (ADR-0058 event-subscribe). Exported so the
// supervisor wiring (and its tests) build the Host deps from the one place that
// owns *store.Store — the Reveal filter the worker is bound to flows straight
// into the existing store delivery boundary.
type IntegrationEventReader struct {
	Store *store.Store
}

func (e IntegrationEventReader) ReadEventOutboxFiltered(ctx context.Context, afterSeq int64, limit int, filter store.LibraryFilter) ([]store.OutboxEvent, error) {
	return e.Store.ReadEventOutboxFiltered(ctx, afterSeq, limit, filter)
}

// CausationToken returns no inherited lineage while every worker mutation seam
// remains dormant: all currently reachable outbox rows are chain roots.
func (IntegrationEventReader) CausationToken(store.OutboxEvent) causation.Token { return "" }

type integrationStatusWriter struct {
	st *store.Store
}

func (w integrationStatusWriter) WriteStatus(ctx context.Context, pluginID, kind, detail string) error {
	kind = strings.TrimSpace(kind)
	detail = strings.TrimSpace(detail)
	if kind == "" {
		return store.ValidationError("status kind is required")
	}
	state, ok, err := w.st.IntegrationWorker(ctx, pluginID)
	if err != nil {
		return err
	}
	if !ok {
		state.PluginID = pluginID
	}
	state.State = kind
	state.LastError = detail
	return w.st.SetIntegrationWorker(ctx, state, time.Now().UTC())
}

type integrationProfileLinker struct {
	st *store.Store
}

func (l integrationProfileLinker) LinkProfile(ctx context.Context, pluginID string, profileID store.ProfileID, ref string) error {
	return l.st.SetPluginProfileLink(ctx, pluginID, profileID, ref, time.Now().UTC())
}

// IntegrationHostDeps builds the Host dependency seams from *store.Store, the
// 0600 secret store, and the shared server lifecycle authority. Profiles,
// catalog, events, secrets, audit, status writes, profile links, the shared
// causation cap, and the SSRF-hardened outbound proxy (per-origin operator
// approval enforced by the lifecycle policy source) are wired. Ingest/file-
// mutation/jobs remain nil until their Core handoff paths are promoted (the
// Host returns permission-denied for an unavailable capability even when
// granted — the dormant-capability posture).
// Exported so a worker-install/launch path (and its tests) constructs the Host
// without re-deriving the adapters.
func IntegrationHostDeps(st *store.Store, secrets secretFiles, lifecycle *integrationhost.Lifecycle) integrationhost.Deps {
	return integrationHostDeps(st, secrets, lifecycle, causation.NewCausationCap(causation.DefaultLimits(), time.Now))
}

func integrationHostDeps(st *store.Store, secrets secretFiles, lifecycle *integrationhost.Lifecycle, cap *causation.CausationCap) integrationhost.Deps {
	return integrationhost.Deps{
		Catalog:   st,
		Events:    IntegrationEventReader{Store: st},
		Profiles:  st,
		Secrets:   secrets,
		Audit:     st,
		Causation: cap,
		Outbound:  integrationhost.NewHardenedOutboundProxy(lifecycle),
		Status:    integrationStatusWriter{st: st},
		Linker:    integrationProfileLinker{st: st},
	}
}

// integrationLifecycle builds the server-owned lifecycle controller. It is the
// only path that mutates worker/grant/update-source state in response to a
// Manager/Console request, writing status + audit.
func integrationLifecycle(st *store.Store) *integrationhost.Lifecycle {
	lifecycle := integrationhost.NewLifecycle(integrationGrantStore{st: st}, st, time.Now)
	lifecycle.SetPublicRoutabilityPredicate(scrape.IsPublicRoutableIP)
	lifecycle.SetUpdateCandidateVerifier(func(ctx context.Context, pluginID string) (integrationhost.VerifiedUpdateCandidate, error) {
		return verifyIntegrationUpdateCandidate(ctx, st, pluginID)
	})
	return lifecycle
}

func verifyIntegrationUpdateCandidate(ctx context.Context, st *store.Store, pluginID string) (integrationhost.VerifiedUpdateCandidate, error) {
	activeManifest, activeBinding, hasActiveManifest, err := loadVerifiedWorkerManifest(ctx, st, pluginID)
	if err != nil {
		return integrationhost.VerifiedUpdateCandidate{}, err
	}
	src, ok, err := st.PluginUpdateSource(ctx, pluginID)
	if err != nil {
		return integrationhost.VerifiedUpdateCandidate{}, err
	}
	if !ok {
		return integrationhost.VerifiedUpdateCandidate{}, errors.New("integrationhost: update source is not configured")
	}
	pin, err := integrationArtifactPin(src.PinKind, src.Pin, src.SigningKey, src.Signature)
	if err != nil {
		return integrationhost.VerifiedUpdateCandidate{}, err
	}
	manifestRaw, found, err := readIntegrationPackageManifest(src.Location, pluginID)
	if err != nil {
		return integrationhost.VerifiedUpdateCandidate{}, err
	}
	if !found {
		return integrationhost.VerifiedUpdateCandidate{}, errors.New("integrationhost: verified update candidate has no package manifest")
	}
	source := integrationhost.Artifact{PluginID: pluginID, Path: filepath.Join(src.Location, pluginID)}
	artifact, err := integrationhost.InstallVerifiedUpdateCandidate(st.IntegrationArtifactRoot(), source, manifestRaw, pin)
	if err != nil {
		return integrationhost.VerifiedUpdateCandidate{}, err
	}
	manifest, err := parseIntegrationPackageManifest(manifestRaw, pluginID)
	if err != nil {
		return integrationhost.VerifiedUpdateCandidate{}, err
	}
	digest, err := integrationhost.HashFile(artifact.Path)
	if err != nil {
		return integrationhost.VerifiedUpdateCandidate{}, err
	}
	manifestBinding, err := integrationhost.ManifestApprovalBinding(digest, manifestRaw)
	if err != nil {
		return integrationhost.VerifiedUpdateCandidate{}, err
	}
	var activePermissions []string
	if hasActiveManifest {
		activePermissions = packagePermissionKinds(activeManifest)
	}
	return integrationhost.VerifiedUpdateCandidate{
		PluginID:              manifest.ID,
		Version:               manifest.Version,
		ArtifactSHA256:        digest,
		ManifestBinding:       manifestBinding,
		Permissions:           packagePermissionKinds(manifest),
		ActiveManifestBinding: activeBinding,
		ActivePermissions:     activePermissions,
	}, nil
}

func loadVerifiedWorkerManifest(ctx context.Context, st *store.Store, pluginID string) (plugin.PackageManifest, string, bool, error) {
	worker, ok, err := st.IntegrationWorker(ctx, pluginID)
	if err != nil || !ok || worker.ArtifactSHA256 == "" {
		return plugin.PackageManifest{}, "", false, err
	}
	artifact := integrationhost.Artifact{
		PluginID: pluginID, Path: filepath.Join(st.IntegrationArtifactRoot(), "sha256", worker.ArtifactSHA256),
		VerifiedSHA256: worker.ArtifactSHA256,
	}
	if err := artifact.Verify(integrationhost.Pin{Kind: integrationhost.PinHash, Hash: worker.ArtifactSHA256}); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return plugin.PackageManifest{}, "", false, nil
		}
		return plugin.PackageManifest{}, "", false, err
	}
	raw, err := integrationhost.ReadPackageManifest(integrationhost.VerifiedUpdateManifestPath(artifact.Path))
	if errors.Is(err, os.ErrNotExist) {
		return plugin.PackageManifest{}, "", false, nil
	}
	if err != nil {
		return plugin.PackageManifest{}, "", false, err
	}
	manifest, err := parseIntegrationPackageManifest(raw, pluginID)
	if err != nil {
		return plugin.PackageManifest{}, "", false, err
	}
	binding, err := integrationhost.ManifestApprovalBinding(worker.ArtifactSHA256, raw)
	if err != nil {
		return plugin.PackageManifest{}, "", false, err
	}
	return manifest, binding, true, nil
}

func integrationArtifactPin(kind, value, signingKey, signature string) (integrationhost.Pin, error) {
	switch kind {
	case integrationhost.PinHash:
		return integrationhost.Pin{Kind: integrationhost.PinHash, Hash: value}, nil
	case integrationhost.PinSigningKey:
		key, err := hex.DecodeString(signingKey)
		if err != nil {
			return integrationhost.Pin{}, fmt.Errorf("%w: decode signing key: %v", integrationhost.ErrVerification, err)
		}
		sig, err := hex.DecodeString(signature)
		if err != nil {
			return integrationhost.Pin{}, fmt.Errorf("%w: decode signature: %v", integrationhost.ErrVerification, err)
		}
		return integrationhost.Pin{
			Kind:       integrationhost.PinSigningKey,
			SigningKey: ed25519.PublicKey(key),
			Signature:  sig,
		}, nil
	default:
		return integrationhost.Pin{}, errUnverifiablePin
	}
}

func packagePermissionKinds(manifest plugin.PackageManifest) []string {
	kinds := make([]string, 0, len(manifest.Permissions))
	for _, permission := range manifest.Permissions {
		kinds = append(kinds, permission.Kind)
	}
	return kinds
}

// integrationManager supervises the installed third-party Integration Workers
// over the shared jobs lifecycle. It is the production launch path:
// at Run it discovers every installed, enabled worker, builds a per-plugin Host
// from the wired seams, and supervises each one out-of-process. It is dormant
// when no worker is installed (buildIntegrationManager returns nil), exactly
// like the webhook/scrobble plugins.
type integrationManager struct {
	st              *store.Store
	deps            integrationhost.Deps
	pluginID        string
	artifact        integrationhost.Artifact
	permissions     []string
	manifestBinding string
	logger          *slog.Logger
	shimPath        string

	mu         sync.Mutex
	supervisor *integrationhost.Supervisor
	lease      *integrationhost.GenerationLease
	cancelRun  context.CancelCauseFunc
	fence      integrationhost.GenerationFence
	running    bool
	stopBound  time.Duration

	runSupervisor func(context.Context) error
	waitRestart   func(context.Context, time.Duration) error
	runAction     func(context.Context, string, string) error
	workers       map[string]*integrationManager
}

// errUnverifiablePin refuses a worker whose Update Source has no recognized pin
// kind — there is no way to verify it, so it is never launched.
var errUnverifiablePin = errors.New("integrationhost: worker has no verifiable pin — refusing to launch")

const pluginActivationPollInterval = 50 * time.Millisecond

const integrationWorkerStopBound = 6 * time.Second

var (
	errIntegrationManagerRunning = errors.New("integrationhost: worker manager already running")
	errIntegrationWorkerStop     = errors.New("integrationhost: timed out waiting for worker retirement")
)

// buildIntegrationManager returns a manager for every installed, enabled,
// verified worker among the known plugins, or nil when none is installable
// (dormant). Discovery is store-driven: a worker row exists only after an
// operator installed one through the Console lifecycle flow. With no install path
// shipping artifacts in v1 this is normally dormant — no overhead on a server
// with no third-party plugins.
func buildIntegrationManager(ctx context.Context, st *store.Store, plugins *plugin.Registry, secrets secretFiles, lifecycle *integrationhost.Lifecycle, logger *slog.Logger, shimPath string) (*integrationManager, error) {
	if plugins == nil {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	cap := causation.NewCausationCap(causation.DefaultLimits(), time.Now)
	workers := make(map[string]*integrationManager)
	for _, id := range plugins.IDs() {
		m, err := buildIntegrationManagerForWithCausation(ctx, st, string(id), secrets, lifecycle, logger, shimPath, cap)
		if err != nil {
			var quarantine *integrationWorkerQuarantineError
			if !errors.As(err, &quarantine) {
				return nil, err
			}
			if err := quarantineIntegrationWorker(ctx, st, string(id), quarantine.err); err != nil {
				return nil, err
			}
			logger.Error("integration worker quarantined", "plugin_id", id, "error", quarantine.err)
			continue
		}
		if m != nil {
			workers[string(id)] = m
		}
	}
	if len(workers) == 0 {
		return nil, nil
	}
	if len(workers) == 1 {
		for _, worker := range workers {
			return worker, nil
		}
	}
	return &integrationManager{st: st, logger: logger, workers: workers}, nil
}

type integrationWorkerQuarantineError struct {
	err error
}

func (e *integrationWorkerQuarantineError) Error() string { return e.err.Error() }
func (e *integrationWorkerQuarantineError) Unwrap() error { return e.err }

func quarantineIntegrationWorkerError(err error) error {
	return &integrationWorkerQuarantineError{err: err}
}

func quarantineIntegrationWorker(ctx context.Context, st *store.Store, pluginID string, cause error) error {
	worker, ok, err := st.IntegrationWorker(ctx, pluginID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	worker.State = "failed"
	worker.LastError = cause.Error()
	return st.SetIntegrationWorker(ctx, worker, time.Now().UTC())
}

// buildIntegrationManagerFor builds a manager for one plugin's installed worker,
// verifying the artifact against its Update Source pin BEFORE the supervisor will
// launch it (ADR-0058: install/verify by hash or signing key). A worker that is
// disabled, unverifiable, or missing its pinned source is not launched.
func buildIntegrationManagerFor(ctx context.Context, st *store.Store, pluginID string, secrets secretFiles, lifecycle *integrationhost.Lifecycle, logger *slog.Logger, shimPath string) (*integrationManager, error) {
	cap := causation.NewCausationCap(causation.DefaultLimits(), time.Now)
	return buildIntegrationManagerForWithCausation(ctx, st, pluginID, secrets, lifecycle, logger, shimPath, cap)
}

func buildIntegrationManagerForWithCausation(ctx context.Context, st *store.Store, pluginID string, secrets secretFiles, lifecycle *integrationhost.Lifecycle, logger *slog.Logger, shimPath string, cap *causation.CausationCap) (*integrationManager, error) {
	worker, ok, err := st.IntegrationWorker(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	if !ok || worker.State == "disabled" || worker.State == "failed" {
		return nil, nil // dormant: nothing installed/enabled to supervise
	}
	src, hasSrc, err := st.PluginUpdateSource(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	if !hasSrc {
		return nil, nil // no pinned source → unverifiable → not launchable
	}
	if lifecycle == nil {
		return nil, errors.New("integrationhost: lifecycle unavailable")
	}
	sourceArtifact := integrationhost.Artifact{
		PluginID: pluginID,
		Version:  worker.Version,
		Path:     filepath.Join(src.Location, pluginID),
	}
	artifactRoot := st.IntegrationArtifactRoot()
	if worker.ArtifactSHA256 != "" {
		object := sourceArtifact
		object.Path = filepath.Join(artifactRoot, "sha256", worker.ArtifactSHA256)
		object.VerifiedSHA256 = worker.ArtifactSHA256
		if _, statErr := os.Stat(object.Path); statErr == nil {
			if err := object.Verify(integrationhost.Pin{Kind: integrationhost.PinHash, Hash: worker.ArtifactSHA256}); err != nil {
				return nil, quarantineIntegrationWorkerError(err)
			}
			return buildIntegrationManagerForArtifact(ctx, st, pluginID, object, secrets, lifecycle, logger, shimPath, cap)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return nil, quarantineIntegrationWorkerError(statErr)
		}
	}
	// Verify the on-disk artifact against the operator's pin before it is ever
	// launchable. Fail CLOSED on an unverifiable pin: a worker is never launched
	// without verification.
	pin, err := integrationArtifactPin(src.PinKind, src.Pin, src.SigningKey, src.Signature)
	if err != nil {
		return nil, quarantineIntegrationWorkerError(err)
	}
	manifestRaw, hasManifest, err := readIntegrationPackageManifest(src.Location, pluginID)
	if err != nil {
		return nil, quarantineIntegrationWorkerError(err)
	}
	var artifact integrationhost.Artifact
	if hasManifest {
		artifact, err = integrationhost.InstallVerifiedUpdateCandidate(artifactRoot, sourceArtifact, manifestRaw, pin)
	} else {
		artifact, err = integrationhost.InstallVerifiedObject(artifactRoot, sourceArtifact, pin)
	}
	if err != nil {
		return nil, quarantineIntegrationWorkerError(err)
	}
	digest, err := integrationhost.HashFile(artifact.Path)
	if err != nil {
		return nil, quarantineIntegrationWorkerError(err)
	}
	if worker.ArtifactSHA256 != digest {
		worker.ArtifactSHA256 = digest
		if err := st.SetIntegrationWorker(ctx, worker, time.Now().UTC()); err != nil {
			return nil, err
		}
	}
	return buildIntegrationManagerForArtifact(ctx, st, pluginID, artifact, secrets, lifecycle, logger, shimPath, cap)
}

func buildIntegrationManagerForArtifact(ctx context.Context, st *store.Store, pluginID string, artifact integrationhost.Artifact, secrets secretFiles, lifecycle *integrationhost.Lifecycle, logger *slog.Logger, shimPath string, cap *causation.CausationCap) (*integrationManager, error) {
	// The package manifest is a plugin claim, never an authority source. Read
	// and reconcile it only after the worker artifact passes its operator pin so
	// an unverified install cannot mutate executable policy.
	manifest, manifestBinding, found, err := loadActiveIntegrationPackageManifest(artifact.Path, artifact.VerifiedSHA256, pluginID)
	if err != nil {
		return nil, quarantineIntegrationWorkerError(err)
	}
	declaration := plugin.OutboundDeclaration{PluginID: plugin.ID(pluginID)}
	var permissions []string
	if found {
		declaration.Origins = manifest.Outbound
		permissions = packagePermissionKinds(manifest)
	}
	worker, ok, err := st.IntegrationWorker(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	if ok && worker.State == "update-staged" {
		if !found {
			return nil, nil
		}
		approved, err := candidatePermissionsApproved(ctx, st, pluginID, permissions, manifestBinding)
		if err != nil {
			return nil, err
		}
		if !approved {
			return nil, nil
		}
		worker.State = "installed"
		if err := st.SetIntegrationWorker(ctx, worker, time.Now().UTC()); err != nil {
			return nil, err
		}
	}
	if err := lifecycle.RegisterOutboundDeclaration(ctx, declaration); err != nil {
		return nil, quarantineIntegrationWorkerError(err)
	}
	return &integrationManager{
		st:              st,
		deps:            integrationHostDeps(st, secrets, lifecycle, cap),
		pluginID:        pluginID,
		artifact:        artifact,
		permissions:     permissions,
		manifestBinding: manifestBinding,
		logger:          logger,
		shimPath:        shimPath,
	}, nil
}

func candidatePermissionsApproved(ctx context.Context, st *store.Store, pluginID string, permissions []string, manifestBinding string) (bool, error) {
	grants, err := st.PluginGrants(ctx, pluginID)
	if err != nil {
		return false, err
	}
	approved := make(map[string]bool, len(grants))
	for _, grant := range grants {
		approved[grant.Kind] = grant.Approved
	}
	for _, permission := range permissions {
		if !approved[permission] {
			return false, nil
		}
		if integrationhost.IsHighRisk(permission) {
			binding, ok, err := integrationhost.GrantApprovalBinding(st.IntegrationArtifactRoot(), pluginID, permission)
			if err != nil {
				return false, err
			}
			if !ok || binding != manifestBinding || manifestBinding == "" {
				return false, nil
			}
		}
	}
	return true, nil
}

const integrationPackageManifestSuffix = ".manifest.json"

func loadActiveIntegrationPackageManifest(artifactPath, artifactSHA256, pluginID string) (plugin.PackageManifest, string, bool, error) {
	raw, err := integrationhost.ReadPackageManifest(integrationhost.VerifiedUpdateManifestPath(artifactPath))
	if err == nil {
		manifest, parseErr := parseIntegrationPackageManifest(raw, pluginID)
		if parseErr != nil {
			return plugin.PackageManifest{}, "", false, parseErr
		}
		binding, bindingErr := integrationhost.ManifestApprovalBinding(artifactSHA256, raw)
		return manifest, binding, bindingErr == nil, bindingErr
	}
	if errors.Is(err, os.ErrNotExist) {
		return plugin.PackageManifest{}, "", false, nil
	}
	return plugin.PackageManifest{}, "", false, err
}

func readIntegrationPackageManifest(root, pluginID string) ([]byte, bool, error) {
	path := filepath.Join(root, pluginID+integrationPackageManifestSuffix)
	raw, err := integrationhost.ReadPackageManifest(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

func parseIntegrationPackageManifest(raw []byte, pluginID string) (plugin.PackageManifest, error) {
	manifest, err := plugin.ParsePackageManifest(raw)
	if err != nil {
		return plugin.PackageManifest{}, err
	}
	if manifest.ID != pluginID {
		return plugin.PackageManifest{}, fmt.Errorf("integrationhost: package manifest id %q does not match worker %q", manifest.ID, pluginID)
	}
	if manifest.Tier != plugin.TierIntegration {
		return plugin.PackageManifest{}, fmt.Errorf("integrationhost: package manifest for worker %q has tier %q", pluginID, manifest.Tier)
	}
	return manifest, nil
}

func (m *integrationManager) RunAction(ctx context.Context, pluginID, actionID string) error {
	if m == nil {
		return fmt.Errorf("integrationhost: worker unavailable")
	}
	if len(m.workers) != 0 {
		worker := m.workers[pluginID]
		if worker == nil {
			return fmt.Errorf("integrationhost: worker unavailable")
		}
		return worker.RunAction(ctx, pluginID, actionID)
	}
	if m.runAction != nil {
		return m.runAction(ctx, pluginID, actionID)
	}
	active, err := m.st.PluginActivation(ctx, pluginID)
	if err != nil {
		return err
	}
	if !active {
		return integrationhost.ErrPluginDeactivated
	}
	m.mu.Lock()
	sup := m.supervisor
	m.mu.Unlock()
	if sup == nil {
		return fmt.Errorf("integrationhost: worker unavailable")
	}
	return sup.RunAction(ctx, pluginID, actionID)
}
