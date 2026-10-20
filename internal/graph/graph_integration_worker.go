//go:build !beamfall_appstore

package graph

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/integrationhost"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// ADR-0058 / ADR-0070 third-party Integration Worker Plugin Surface (the graph
// half). The Console reads server-declared worker state, grant-approval state,
// and the Update Source, and requests server-owned lifecycle actions. All data
// here is server-declared (no downloaded plugin UI ever crosses the wire — the
// client renders native controls from this data, ADR-0058). Manager-gated in
// execute() like the rest of the Console config surface.

// IntegrationLifecycle is the seam to the ADR-0058 server-owned lifecycle
// controller (internal/integrationhost.Lifecycle). The composition root wires
// it; the graph layer calls it for actions whose server-side effect (the
// permission-expansion re-approval rule on apply-update) lives in the runtime,
// not the graph. Defined at the consumer.
type IntegrationLifecycle interface {
	ApplyUpdate(ctx context.Context, pluginID string) error
}

type pluginLifecycleActionHandler func(*Handler, context.Context, auth.Scope, request, string) (PluginWorkerInfo, error)

func pluginLifecycleActionHandlers() map[string]pluginLifecycleActionHandler {
	return map[string]pluginLifecycleActionHandler{
		integrationhost.ActionApplyUpdate: pluginLifecycleApplyUpdate,
		integrationhost.ActionDisable:     pluginLifecycleDisable,
		integrationhost.ActionRunAction:   pluginLifecycleRunAction,
	}
}

func advertisedPluginLifecycleActions() []string {
	handlers := pluginLifecycleActionHandlers()
	actions := make([]string, 0, len(handlers))
	for action := range handlers {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	return actions
}

// SetIntegrationActionRunner wires the worker transport used by run-action.
func (h *Handler) SetIntegrationActionRunner(r integrationhost.ActionRunner) {
	h.integrationActionRunner = r
}

// PluginWorkerInfo is the server-declared runtime state of one plugin's
// Integration Worker plus its grant-approval state, surfaced in the Plugin
// Surface status (ADR-0070 status card). It carries no secret material.
type PluginWorkerInfo struct {
	PluginID       string                  `json:"pluginID"`
	Version        string                  `json:"version,omitempty"`
	ArtifactSHA256 string                  `json:"artifactSHA256,omitempty"`
	State          string                  `json:"state"`
	LastError      string                  `json:"lastError,omitempty"`
	Installed      bool                    `json:"installed"`
	Grants         []PluginGrantInfo       `json:"grants"`
	UpdateSource   *PluginUpdateSourceInfo `json:"updateSource,omitempty"`
	// LifecycleActions is the closed set of server-owned actions a Manager/Console
	// may request for this plugin. Stable ids; the client renders
	// native controls.
	LifecycleActions []string `json:"lifecycleActions"`
}

// PluginGrantInfo is one permission grant's approval + Reveal-scope binding
// state. HighRisk and Approved drive the re-approval UI; the bound Profile / all-
// Libraries flags drive the read-scope display.
type PluginGrantInfo struct {
	Kind           string `json:"kind"`
	HighRisk       bool   `json:"highRisk"`
	Approved       bool   `json:"approved"`
	BoundProfileID string `json:"boundProfileID,omitempty"`
	AllLibraries   bool   `json:"allLibraries"`
}

// PluginUpdateSourceInfo is the operator-configured Update Source (ADR-0058 — not
// a marketplace). The pin material is surfaced so an operator can confirm what
// is trusted; it carries no secret.
type PluginUpdateSourceInfo struct {
	Kind     string `json:"kind"`
	Location string `json:"location"`
	PinKind  string `json:"pinKind"`
	Pin      string `json:"pin"`
}

// pluginWorkerInfo assembles the server-declared worker surface for one plugin.
// A plugin with no installed worker still returns its grant/lifecycle shape so
// the Console can render "not installed" with the configurable actions.
func (h *Handler) pluginWorkerInfo(ctx context.Context, pluginID string) (PluginWorkerInfo, error) {
	info := PluginWorkerInfo{
		PluginID:         pluginID,
		State:            "not-installed",
		Grants:           []PluginGrantInfo{},
		LifecycleActions: advertisedPluginLifecycleActions(),
	}
	worker, installed, err := h.store.IntegrationWorker(ctx, pluginID)
	if err != nil {
		return PluginWorkerInfo{}, err
	}
	if installed {
		info.Installed = true
		info.Version = worker.Version
		info.ArtifactSHA256 = worker.ArtifactSHA256
		info.State = worker.State
		info.LastError = worker.LastError
	}
	grants, err := h.store.PluginGrants(ctx, pluginID)
	if err != nil {
		return PluginWorkerInfo{}, err
	}
	_, manifestBinding, manifestErr := h.verifiedWorkerManifest(ctx, pluginID)
	for _, g := range grants {
		approved := g.Approved
		if approved && integrationhost.IsHighRisk(g.Kind) {
			binding, ok, bindingErr := integrationhost.GrantApprovalBinding(h.store.IntegrationArtifactRoot(), pluginID, g.Kind)
			approved = manifestErr == nil && bindingErr == nil && ok && binding == manifestBinding && manifestBinding != ""
		}
		info.Grants = append(info.Grants, PluginGrantInfo{
			Kind:           g.Kind,
			HighRisk:       integrationhost.IsHighRisk(g.Kind),
			Approved:       approved,
			BoundProfileID: string(g.BoundProfileID),
			AllLibraries:   g.AllLibraries,
		})
	}
	src, hasSrc, err := h.store.PluginUpdateSource(ctx, pluginID)
	if err != nil {
		return PluginWorkerInfo{}, err
	}
	if hasSrc {
		info.UpdateSource = &PluginUpdateSourceInfo{
			Kind: src.Kind, Location: src.Location, PinKind: src.PinKind, Pin: src.Pin,
		}
	}
	return info, nil
}

// pluginWorkerField is the read resolver: the server-declared worker surface for
// one plugin named by the pluginID variable. Manager-gated in execute().
func (h *Handler) pluginWorkerField(ctx context.Context, req request) (PluginWorkerInfo, error) {
	pluginID := trimmedVariable(req, "pluginID")
	if pluginID == "" {
		return PluginWorkerInfo{}, clientError("pluginID is required")
	}
	if !h.knownPlugin(pluginID) {
		return PluginWorkerInfo{}, clientError("unknown plugin")
	}
	return h.pluginWorkerInfo(ctx, pluginID)
}

// approvePluginPermission approves (or revokes) a permission grant and records
// its Reveal-scope binding. High-risk grants are inert until approved here — the
// human gate ADR-0058 requires. Mutually-exclusive scope flags are rejected by
// the store (ErrGrantScopeConflict) and the DB CHECK. Manager-gated; audited.
func (h *Handler) approvePluginPermission(ctx context.Context, scope auth.Scope, req request) (PluginWorkerInfo, error) {
	pluginID := trimmedVariable(req, "pluginID")
	kind := trimmedVariable(req, "kind")
	if pluginID == "" || kind == "" {
		return PluginWorkerInfo{}, clientError("pluginID and kind are required")
	}
	if !h.knownPlugin(pluginID) {
		return PluginWorkerInfo{}, clientError("unknown plugin")
	}
	if !integrationhost.IsKnownPermission(kind) {
		return PluginWorkerInfo{}, clientError("unknown permission")
	}
	manifest, manifestBinding, err := h.verifiedWorkerManifest(ctx, pluginID)
	if err != nil {
		return PluginWorkerInfo{}, clientError("verified active plugin manifest is unavailable")
	}
	declared := false
	for _, permission := range manifest.Permissions {
		if permission.Kind == kind {
			declared = true
			break
		}
	}
	if !declared {
		return PluginWorkerInfo{}, clientError("permission is not declared by the active plugin manifest")
	}
	v := newVars(req)
	approved, _ := v.Bool("approved")
	allLibraries, _ := v.Bool("allLibraries")
	if err := v.Err(); err != nil {
		return PluginWorkerInfo{}, err
	}
	boundProfile := trimmedVariable(req, "boundProfileID")
	if boundProfile != "" && allLibraries {
		return PluginWorkerInfo{}, clientError("a grant cannot bind both a Profile and all Libraries")
	}
	grant := store.PluginGrantRow{
		PluginID:       pluginID,
		Kind:           kind,
		Approved:       approved,
		BoundProfileID: store.ProfileID(boundProfile),
		AllLibraries:   allLibraries,
	}
	if err := h.appendIntegrationActivity(ctx, scope, store.ActivityPluginGrant, pluginID); err != nil {
		return PluginWorkerInfo{}, err
	}
	if integrationhost.IsHighRisk(kind) {
		// Clear any stale marker before changing the row. A failed approval
		// write can therefore never reactivate an earlier binding.
		if err := integrationhost.SetGrantApprovalBinding(h.store.IntegrationArtifactRoot(), pluginID, kind, ""); err != nil {
			return PluginWorkerInfo{}, err
		}
	}
	if err := h.store.SetPluginGrant(ctx, grant, time.Now().UTC()); err != nil {
		return PluginWorkerInfo{}, err
	}
	if integrationhost.IsHighRisk(kind) && approved {
		if err := integrationhost.SetGrantApprovalBinding(h.store.IntegrationArtifactRoot(), pluginID, kind, manifestBinding); err != nil {
			return PluginWorkerInfo{}, err
		}
	}
	return h.pluginWorkerInfo(ctx, pluginID)
}

// setPluginUpdateSource configures the per-plugin Update Source. The
// kind/pin vocabularies are validated; this is an acquisition-adjacent surface
// pruned on App Store binaries by the existing ADR-0050 firewall (the descriptor
// read path runs server-side). Manager-gated; audited.
func (h *Handler) setPluginUpdateSource(ctx context.Context, scope auth.Scope, req request) (PluginWorkerInfo, error) {
	pluginID := trimmedVariable(req, "pluginID")
	if pluginID == "" {
		return PluginWorkerInfo{}, clientError("pluginID is required")
	}
	if !h.knownPlugin(pluginID) {
		return PluginWorkerInfo{}, clientError("unknown plugin")
	}
	kind := trimmedVariable(req, "kind")
	if kind != "url" && kind != "folder" {
		return PluginWorkerInfo{}, clientError("kind must be url or folder")
	}
	pinKind := trimmedVariable(req, "pinKind")
	if pinKind != integrationhost.PinHash && pinKind != integrationhost.PinSigningKey {
		return PluginWorkerInfo{}, clientError("pinKind must be hash or signing-key")
	}
	location := trimmedVariable(req, "location")
	pin := trimmedVariable(req, "pin")
	if location == "" || pin == "" {
		return PluginWorkerInfo{}, clientError("location and pin are required")
	}
	// A signing-key pin must carry the trusted Ed25519 public key + detached
	// signature (both hex-encoded public trust material) so the launch path can
	// verify the artifact before it is launchable. Reject an incomplete config
	// here rather than failing closed only at launch.
	signingKey := trimmedVariable(req, "signingKey")
	signature := trimmedVariable(req, "signature")
	if pinKind == integrationhost.PinSigningKey && (signingKey == "" || signature == "") {
		return PluginWorkerInfo{}, clientError("signingKey and signature are required for signing-key pins")
	}
	src := store.PluginUpdateSourceRow{
		PluginID: pluginID, Kind: kind, Location: location, PinKind: pinKind, Pin: pin,
		SigningKey: signingKey, Signature: signature,
	}
	if err := h.appendIntegrationActivity(ctx, scope, store.ActivityPluginUpdate, pluginID); err != nil {
		return PluginWorkerInfo{}, err
	}
	if err := h.store.SetPluginUpdateSource(ctx, src, time.Now().UTC()); err != nil {
		return PluginWorkerInfo{}, err
	}
	return h.pluginWorkerInfo(ctx, pluginID)
}

// runPluginLifecycleAction requests one server-owned lifecycle action. The
// server performs the change and writes status/audit (ADR-0058: the Manager
// requests, the server acts). An action outside the closed vocabulary is
// rejected — Managers cannot invent server actions. Manager-gated; audited.
func (h *Handler) runPluginLifecycleAction(ctx context.Context, scope auth.Scope, req request) (PluginWorkerInfo, error) {
	pluginID := trimmedVariable(req, "pluginID")
	action := trimmedVariable(req, "action")
	if pluginID == "" || action == "" {
		return PluginWorkerInfo{}, clientError("pluginID and action are required")
	}
	if !h.knownPlugin(pluginID) {
		return PluginWorkerInfo{}, clientError("unknown plugin")
	}
	actionHandler, ok := pluginLifecycleActionHandlers()[action]
	if !ok {
		return PluginWorkerInfo{}, clientError("unknown lifecycle action")
	}
	return actionHandler(h, ctx, scope, req, pluginID)
}

func pluginLifecycleRunAction(h *Handler, ctx context.Context, scope auth.Scope, req request, pluginID string) (PluginWorkerInfo, error) {
	actionID := trimmedVariable(req, "actionID")
	if actionID == "" {
		actionID = trimmedVariable(req, "descriptorID")
	}
	if actionID == "" {
		return PluginWorkerInfo{}, clientError("actionID is required")
	}
	if h.integrationActionRunner == nil {
		return PluginWorkerInfo{}, clientError("plugin actions are unavailable on this server")
	}
	if err := h.runPluginDescriptorAction(ctx, scope, pluginID, actionID); err != nil {
		return PluginWorkerInfo{}, err
	}
	return h.pluginWorkerInfo(ctx, pluginID)
}

func pluginLifecycleDisable(h *Handler, ctx context.Context, scope auth.Scope, _ request, pluginID string) (PluginWorkerInfo, error) {
	worker, _, err := h.store.IntegrationWorker(ctx, pluginID)
	if err != nil {
		return PluginWorkerInfo{}, err
	}
	worker.PluginID = pluginID
	worker.State = "disabled"
	if err := h.store.SetIntegrationWorker(ctx, worker, time.Now().UTC()); err != nil {
		return PluginWorkerInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityPluginUpdate, "plugin", pluginID, ""); err != nil {
		return PluginWorkerInfo{}, err
	}
	return h.pluginWorkerInfo(ctx, pluginID)
}

func pluginLifecycleApplyUpdate(h *Handler, ctx context.Context, _ auth.Scope, _ request, pluginID string) (PluginWorkerInfo, error) {
	if h.integrationLifecycle == nil {
		return PluginWorkerInfo{}, clientError("plugin updates are unavailable on this server")
	}
	if err := h.integrationLifecycle.ApplyUpdate(ctx, pluginID); err != nil {
		return PluginWorkerInfo{}, err
	}
	return h.pluginWorkerInfo(ctx, pluginID)
}

func (h *Handler) appendIntegrationActivity(ctx context.Context, scope auth.Scope, action, pluginID string) error {
	return h.store.AppendActivity(ctx, store.ActivityEntry{
		OccurredAt: time.Now().UTC(),
		Action:     action,
		SessionID:  scope.SessionID,
		DeviceID:   scope.DeviceID,
		ProfileID:  scope.ProfileID,
		TargetType: "plugin",
		TargetID:   pluginID,
		TokenID:    scope.APITokenID,
		APIKeyID:   scope.APIKeyID,
	})
}

const actionPluginRunAction = "plugin.run_action"

func (h *Handler) runPluginDescriptorAction(ctx context.Context, scope auth.Scope, pluginID, actionID string) error {
	manifest, manifestBinding, err := h.verifiedWorkerManifest(ctx, pluginID)
	if err != nil {
		return clientError("verified active plugin manifest is unavailable")
	}
	var spec plugin.ActionSpec
	for _, action := range manifest.Actions {
		if action.ID == actionID {
			spec = action
			break
		}
	}
	if spec.ID == "" {
		return clientError("unknown plugin action")
	}
	grants, err := h.store.PluginGrants(ctx, pluginID)
	if err != nil {
		return err
	}
	approved := map[string]bool{}
	for _, grant := range grants {
		if !grant.Approved {
			continue
		}
		if integrationhost.IsHighRisk(grant.Kind) {
			binding, ok, bindingErr := integrationhost.GrantApprovalBinding(h.store.IntegrationArtifactRoot(), pluginID, grant.Kind)
			if bindingErr != nil || !ok || binding != manifestBinding {
				continue
			}
		}
		approved[grant.Kind] = true
	}
	for _, kind := range spec.RequiredPermissions {
		if !integrationhost.IsKnownPermission(kind) {
			return clientError("unknown permission")
		}
		if !approved[kind] {
			return clientError(fmt.Sprintf("permission %s is not approved", kind))
		}
	}
	if err := h.integrationActionRunner.RunAction(ctx, pluginID, actionID); err != nil {
		return err
	}
	return h.logActivity(ctx, scope, actionPluginRunAction, "plugin", pluginID, "")
}

func (h *Handler) verifiedWorkerManifest(ctx context.Context, pluginID string) (plugin.PackageManifest, string, error) {
	worker, ok, err := h.store.IntegrationWorker(ctx, pluginID)
	if err != nil {
		return plugin.PackageManifest{}, "", err
	}
	if !ok || worker.ArtifactSHA256 == "" {
		return plugin.PackageManifest{}, "", errors.New("verified worker artifact is unavailable")
	}
	artifact := integrationhost.Artifact{
		PluginID:       pluginID,
		Path:           filepath.Join(h.store.IntegrationArtifactRoot(), "sha256", worker.ArtifactSHA256),
		VerifiedSHA256: worker.ArtifactSHA256,
	}
	if err := artifact.Verify(integrationhost.Pin{Kind: integrationhost.PinHash, Hash: worker.ArtifactSHA256}); err != nil {
		return plugin.PackageManifest{}, "", err
	}
	raw, err := integrationhost.ReadPackageManifest(integrationhost.VerifiedUpdateManifestPath(artifact.Path))
	if errors.Is(err, os.ErrNotExist) {
		return plugin.PackageManifest{}, "", errors.New("verified worker manifest is unavailable")
	}
	if err != nil {
		return plugin.PackageManifest{}, "", err
	}
	manifest, err := plugin.ParsePackageManifest(raw)
	if err != nil {
		return plugin.PackageManifest{}, "", err
	}
	if manifest.ID != pluginID || manifest.Tier != plugin.TierIntegration {
		return plugin.PackageManifest{}, "", errors.New("verified worker manifest identity is invalid")
	}
	binding, err := integrationhost.ManifestApprovalBinding(worker.ArtifactSHA256, raw)
	return manifest, binding, err
}

// knownPlugin reports whether the registry loaded a plugin with this id — the
// same membership guard setPluginActive uses, so a phantom plugin can never get
// a worker/grant/update-source row.
func (h *Handler) knownPlugin(pluginID string) bool {
	if h.plugins == nil {
		return false
	}
	if h.pluginSuppressed(plugin.ID(pluginID)) {
		return false
	}
	_, found := h.plugins.Bundle(plugin.ID(pluginID))
	return found
}
