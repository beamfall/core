package graph

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// Console server config (ADR-0007 §4 Console enumeration, ADR-0013 §5). The
// graph half of the console-config feature: a manager-gated resolver that reads
// the server's runtime configuration and the per-plugin activation state, plus
// mutations to update config values and activate/deactivate plugins. All fields
// here are manager-gated in execute() (Console operations). The bootstrap,
// file/env-shaped config (internal/config) is intentionally NOT exposed or
// mutated here — only the database-owned operational state.

// ConsoleConfigInfo is the wire shape for the console runtime config plus the
// plugin-activation state. It carries operational settings only — never the
// bootstrap infra config (ports, data dir, secrets).
type ConsoleConfigInfo struct {
	// BackgroundWorkers is the resource-governor background worker-pool size
	//.
	BackgroundWorkers int `json:"backgroundWorkers"`
	// UpdateCheckEnabled is the opt-in version-check switch.
	// Default false: the check is inert (no outbound request, no telemetry —
	// invariant #5) until the operator opts in, typically in the setup wizard's
	// final step.
	UpdateCheckEnabled bool `json:"updateCheckEnabled"`
	// UpdateCheckChannel is the release channel the check reads ("stable" or
	// "beta").
	UpdateCheckChannel string `json:"updateCheckChannel"`
	// RemoteEnrollmentEnabled gates pairing-code enrollment from off-LAN clients
	//. Default true (no override → today's behavior). When false, only
	// local-network enrollments are accepted.
	RemoteEnrollmentEnabled bool `json:"remoteEnrollmentEnabled"`
	// EventsRetentionDays is the operator-owned events.db compaction window.
	EventsRetentionDays int `json:"eventsRetentionDays"`
	// ObservabilityEnabled is ADR-0146's default-off OTel metrics toggle.
	ObservabilityEnabled bool `json:"observabilityEnabled"`
	// Plugins lists every available plugin (loaded at boot) with its current
	// Console activation state.
	Plugins []PluginActivationInfo `json:"plugins"`
}

type ObservabilityController interface {
	SetEnabled(context.Context, bool) error
}

func (h *Handler) SetObservabilityController(controller ObservabilityController) {
	h.observability = controller
}

// UninstallPluginDataResult returns the refreshed console config plus the
// irreversible purge count from the confirmed Uninstall.
type UninstallPluginDataResult struct {
	ConsoleConfigInfo
	Purged int `json:"purged"`
}

// PluginActivationInfo pairs an available plugin with its activation state. The
// id and display name come from the registry (what is AVAILABLE); active comes
// from the Console override in the database (default true when no override).
type PluginActivationInfo struct {
	ID            string                   `json:"id"`
	DisplayName   plugin.LocaleString      `json:"displayName"`
	Provenance    plugin.PluginProvenance  `json:"provenance"`
	TrustState    plugin.PluginTrustState  `json:"trustState"`
	Active        bool                     `json:"active"`
	ControlPolicy string                   `json:"controlPolicy,omitempty"`
	Config        []PluginConfigFieldInfo  `json:"config"`
	Permissions   []PluginPermissionInfo   `json:"permissions"`
	Status        []PluginStatusInfo       `json:"status"`
	Actions       []PluginActionDescriptor `json:"actions"`
	Scrapers      []PluginScraperInfo      `json:"scrapers"`
}

// PluginScraperInfo exposes plugin-declared scraper endpoints and the current
// secure-store state for the scraper's shared AuthRef. It never carries secret
// bytes or host-local secure-store errors.
type PluginScraperInfo struct {
	PluginID             string `json:"pluginID"`
	ScraperID            string `json:"scraperID"`
	DisplayName          string `json:"displayName"`
	Kind                 string `json:"kind"`
	Endpoint             string `json:"endpoint"`
	AuthRef              string `json:"authRef"`
	AuthStyle            string `json:"authStyle"`
	CredentialRequired   bool   `json:"credentialRequired"`
	CredentialConfigured bool   `json:"credentialConfigured"`
	CredentialStatus     string `json:"credentialStatus"`
	CredentialError      string `json:"credentialError,omitempty"`
}

// PluginConfigFieldInfo is one typed, non-secret plugin setting plus its
// persisted value. Secrets stay behind AuthRef/secure-store seams.
type PluginConfigFieldInfo struct {
	Name       string                   `json:"name"`
	Type       string                   `json:"type"`
	Label      plugin.LocaleString      `json:"label"`
	Required   bool                     `json:"required"`
	Default    string                   `json:"default,omitempty"`
	Value      string                   `json:"value,omitempty"`
	Configured bool                     `json:"configured"`
	Options    []PluginConfigOptionInfo `json:"options,omitempty"`
}

type PluginConfigOptionInfo struct {
	Value string              `json:"value"`
	Label plugin.LocaleString `json:"label"`
}

type PluginPermissionInfo struct {
	Kind        string              `json:"kind"`
	Label       plugin.LocaleString `json:"label"`
	Description plugin.LocaleString `json:"description,omitempty"`
	HighRisk    bool                `json:"highRisk"`
}

type PluginStatusInfo struct {
	ID     string              `json:"id"`
	Label  plugin.LocaleString `json:"label"`
	State  string              `json:"state"`
	Detail string              `json:"detail,omitempty"`
}

type PluginActionDescriptor struct {
	ID                  string                  `json:"id"`
	Label               plugin.LocaleString     `json:"label"`
	Icon                string                  `json:"icon,omitempty"`
	Risk                string                  `json:"risk"`
	RequiredPermissions []string                `json:"requiredPermissions,omitempty"`
	InputFields         []PluginConfigFieldInfo `json:"inputFields,omitempty"`
	Confirmation        plugin.LocaleString     `json:"confirmation,omitempty"`
}

// ScraperCredentialInfo is the plugin-and-AuthRef-scoped credential state
// returned by credential mutations. Scrapers and importers within one plugin
// may share an AuthRef; activated bundles isolate the same local ref across
// different plugins.
type ScraperCredentialInfo struct {
	AuthRef              string   `json:"authRef"`
	CredentialConfigured bool     `json:"credentialConfigured"`
	CredentialStatus     string   `json:"credentialStatus"`
	CredentialError      string   `json:"credentialError,omitempty"`
	ScraperIDs           []string `json:"scraperIDs"`
}

// consoleConfig reads the console runtime config and the plugin-activation
// state. Plugin availability is the registry's boot-time set; activation is the
// database override (default-active for plugins with no row).
func (h *Handler) consoleConfig(ctx context.Context, _ auth.Scope, _ request) (ConsoleConfigInfo, error) {
	cfg, err := h.store.ConsoleConfig(ctx)
	if err != nil {
		return ConsoleConfigInfo{}, err
	}
	info := ConsoleConfigInfo{
		BackgroundWorkers:       cfg.BackgroundWorkers,
		UpdateCheckEnabled:      cfg.UpdateCheckEnabled,
		UpdateCheckChannel:      cfg.UpdateCheckChannel,
		RemoteEnrollmentEnabled: cfg.RemoteEnrollmentEnabled,
		EventsRetentionDays:     cfg.EventsRetentionDays,
		ObservabilityEnabled:    cfg.ObservabilityEnabled,
		Plugins:                 []PluginActivationInfo{},
	}
	overrides, err := h.store.PluginActivations(ctx)
	if err != nil {
		return ConsoleConfigInfo{}, err
	}
	if h.plugins != nil {
		for _, id := range h.visiblePluginIDs() {
			bundle, ok := h.plugins.Bundle(id)
			if !ok {
				continue
			}
			active := true
			if state, has := overrides[string(id)]; has {
				active = state
			}
			standing, ok := h.plugins.Standing(id)
			if !ok {
				return ConsoleConfigInfo{}, fmt.Errorf("plugin %q has no loader standing", id)
			}
			pluginInfo, err := h.pluginActivationInfo(ctx, string(id), bundle, standing, active)
			if err != nil {
				return ConsoleConfigInfo{}, err
			}
			info.Plugins = append(info.Plugins, pluginInfo)
		}
	}
	return info, nil
}

func (h *Handler) pluginActivationInfo(ctx context.Context, pluginID string, bundle plugin.Bundle, standing plugin.PluginStanding, active bool) (PluginActivationInfo, error) {
	cfg, err := h.store.PluginConfigValues(ctx, pluginID)
	if err != nil {
		return PluginActivationInfo{}, err
	}
	controlPolicy := bundle.ControlPolicy
	if controlPolicy == "" {
		controlPolicy = "manager-enabled"
	}
	return PluginActivationInfo{
		ID:            pluginID,
		DisplayName:   bundle.DisplayName,
		Provenance:    standing.Provenance,
		TrustState:    standing.TrustState,
		Active:        active,
		ControlPolicy: controlPolicy,
		Config:        pluginConfigFieldInfo(bundle.ConfigFields, cfg),
		Permissions:   pluginPermissionInfo(bundle.Permissions),
		Status:        h.pluginStatusInfo(active, bundle),
		Actions:       pluginActionInfo(bundle.Actions),
		Scrapers:      h.pluginScraperInfo(pluginID, bundle),
	}, nil
}

// setConsoleConfig applies a partial console-config write and returns the
// resolved config (manager-gated in execute). Absent variables leave the stored
// value unchanged.
func (h *Handler) setConsoleConfig(ctx context.Context, scope auth.Scope, req request) (ConsoleConfigInfo, error) {
	v := newVars(req)
	var patch store.ConsoleConfigPatch
	has := false
	if number, ok := v.Number("backgroundWorkers"); ok {
		workers := int(number)
		patch.BackgroundWorkers = &workers
		has = true
	}
	if enabled, ok := v.Bool("updateCheckEnabled"); ok {
		patch.UpdateCheckEnabled = &enabled
		has = true
	}
	if channel, ok := v.String("updateCheckChannel"); ok {
		patch.UpdateCheckChannel = &channel
		has = true
	}
	if enabled, ok := v.Bool("remoteEnrollmentEnabled"); ok {
		patch.RemoteEnrollmentEnabled = &enabled
		has = true
	}
	if number, ok := v.Number("eventsRetentionDays"); ok {
		days := int(number)
		patch.EventsRetentionDays = &days
		has = true
	}
	if enabled, ok := v.Bool("observabilityEnabled"); ok {
		patch.ObservabilityEnabled = &enabled
		has = true
	}
	if err := v.Err(); err != nil {
		return ConsoleConfigInfo{}, err
	}
	if !has {
		return ConsoleConfigInfo{}, clientError("no console config to update")
	}
	if _, err := h.store.SetConsoleConfig(ctx, patch, time.Now().UTC()); err != nil {
		return ConsoleConfigInfo{}, err
	}
	if patch.ObservabilityEnabled != nil && h.observability != nil {
		if err := h.observability.SetEnabled(ctx, *patch.ObservabilityEnabled); err != nil {
			return ConsoleConfigInfo{}, err
		}
	}
	if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "console", "", ""); err != nil {
		return ConsoleConfigInfo{}, err
	}
	return h.consoleConfig(ctx, scope, req)
}

func (h *Handler) pluginScraperInfo(pluginID string, bundle plugin.Bundle) []PluginScraperInfo {
	items := make([]PluginScraperInfo, 0, len(bundle.Scrapers))
	for _, scraper := range bundle.Scrapers {
		status := h.scraperCredentialStatus(scraper.AuthRef)
		authStyle := scraper.AuthStyle
		if scraper.AuthRef != "" && authStyle == "" {
			authStyle = "bearer"
		}
		items = append(items, PluginScraperInfo{
			PluginID:             pluginID,
			ScraperID:            scraper.ID,
			DisplayName:          scraper.DisplayName.Resolve(""),
			Kind:                 scraper.Kind,
			Endpoint:             scraper.Endpoint,
			AuthRef:              scraper.AuthRef,
			AuthStyle:            authStyle,
			CredentialRequired:   scraper.AuthRef != "",
			CredentialConfigured: status.configured,
			CredentialStatus:     status.status,
			CredentialError:      status.err,
		})
	}
	return items
}

func pluginConfigFieldInfo(fields []plugin.ConfigFieldSpec, values map[string]string) []PluginConfigFieldInfo {
	items := make([]PluginConfigFieldInfo, 0, len(fields))
	for _, field := range fields {
		value, configured := values[field.Name]
		if !configured {
			value = field.Default
		}
		items = append(items, PluginConfigFieldInfo{
			Name:       field.Name,
			Type:       field.Type,
			Label:      field.Label,
			Required:   field.Required,
			Default:    field.Default,
			Value:      value,
			Configured: configured,
			Options:    pluginConfigOptionInfo(field.Options),
		})
	}
	return items
}

func pluginConfigOptionInfo(options []plugin.ConfigOption) []PluginConfigOptionInfo {
	items := make([]PluginConfigOptionInfo, 0, len(options))
	for _, option := range options {
		items = append(items, PluginConfigOptionInfo{Value: option.Value, Label: option.Label})
	}
	return items
}

func pluginPermissionInfo(permissions []plugin.PermissionSpec) []PluginPermissionInfo {
	items := make([]PluginPermissionInfo, 0, len(permissions))
	for _, permission := range permissions {
		items = append(items, PluginPermissionInfo{
			Kind:        permission.Kind,
			Label:       permission.Label,
			Description: permission.Description,
			HighRisk:    permission.HighRisk,
		})
	}
	return items
}

func (h *Handler) pluginStatusInfo(active bool, bundle plugin.Bundle) []PluginStatusInfo {
	state := "enabled"
	if !active {
		state = "disabled"
	}
	items := []PluginStatusInfo{{ID: "activation", Label: plugin.L("Activation"), State: state}}
	seenCredentials := map[string]bool{}
	for _, scraper := range bundle.Scrapers {
		if scraper.Endpoint != "" {
			items = append(items, PluginStatusInfo{
				ID:     "endpoint-" + scraper.ID,
				Label:  scraper.DisplayName,
				State:  "declared",
				Detail: scraper.Endpoint,
			})
		}
		if scraper.AuthRef != "" && !seenCredentials[scraper.AuthRef] {
			seenCredentials[scraper.AuthRef] = true
			status := h.scraperCredentialStatus(scraper.AuthRef)
			items = append(items, PluginStatusInfo{
				ID:     "credential-" + scraper.AuthRef,
				Label:  plugin.L("Credential"),
				State:  status.status,
				Detail: scraper.AuthRef,
			})
		}
	}
	return items
}

func pluginActionInfo(actions []plugin.ActionSpec) []PluginActionDescriptor {
	items := make([]PluginActionDescriptor, 0, len(actions))
	for _, action := range actions {
		risk := action.Risk
		if risk == "" {
			risk = "low"
		}
		items = append(items, PluginActionDescriptor{
			ID:                  action.ID,
			Label:               action.Label,
			Icon:                action.Icon,
			Risk:                risk,
			RequiredPermissions: append([]string(nil), action.RequiredPermissions...),
			InputFields:         pluginConfigFieldInfo(action.InputFields, nil),
			Confirmation:        action.Confirmation,
		})
	}
	return items
}

type scraperCredentialStatus struct {
	configured bool
	status     string
	err        string
}

func (h *Handler) scraperCredentialStatus(authRef string) scraperCredentialStatus {
	if authRef == "" {
		return scraperCredentialStatus{status: "not-required"}
	}
	if h.scraperCredentials == nil {
		return scraperCredentialStatus{status: "unavailable", err: "unavailable"}
	}
	if _, err := h.scraperCredentials.Secret(authRef); err != nil {
		return safeScraperCredentialError(err)
	}
	return scraperCredentialStatus{configured: true, status: "configured"}
}

func safeScraperCredentialError(err error) scraperCredentialStatus {
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "unavailable"), strings.Contains(text, "no such file"):
		return scraperCredentialStatus{status: "missing", err: "missing"}
	case strings.Contains(text, "empty"), strings.Contains(text, "mode 0600"), strings.Contains(text, "invalid credential ref"):
		return scraperCredentialStatus{status: "invalid", err: "invalid"}
	default:
		return scraperCredentialStatus{status: "unreadable", err: "unreadable"}
	}
}

func (h *Handler) scraperCredentialInfo(authRef string) (ScraperCredentialInfo, error) {
	scrapers, ok := h.declaredScrapersForAuthRef(authRef)
	if !ok {
		return ScraperCredentialInfo{}, clientError("unknown scraper credential")
	}
	status := h.scraperCredentialStatus(authRef)
	return ScraperCredentialInfo{
		AuthRef:              authRef,
		CredentialConfigured: status.configured,
		CredentialStatus:     status.status,
		CredentialError:      status.err,
		ScraperIDs:           scrapers,
	}, nil
}

func (h *Handler) declaredScrapersForAuthRef(authRef string) ([]string, bool) {
	authRef = strings.TrimSpace(authRef)
	if authRef == "" || h.plugins == nil {
		return nil, false
	}
	var refs []string
	for _, id := range h.visiblePluginIDs() {
		bundle, ok := h.plugins.Bundle(id)
		if !ok {
			continue
		}
		for _, scraper := range bundle.Scrapers {
			if scraper.AuthRef == authRef {
				refs = append(refs, string(id)+":"+scraper.ID)
			}
		}
		// A Migration Wizard "credentials" importer also names a
		// secret-store ref. The wizard reuses the same credential-set surface as
		// scrapers, so an importer-declared ref is an accepted credential target
		// — without it, the wizard could surface a credential it could never save.
		for _, importer := range bundle.Importers {
			if importer.CredentialRef == authRef {
				refs = append(refs, string(id)+":"+importer.ID)
			}
		}
	}
	return refs, len(refs) > 0
}

func (h *Handler) setScraperCredential(ctx context.Context, scope auth.Scope, req request) (ScraperCredentialInfo, error) {
	authRef := trimmedVariable(req, "authRef")
	secret := stringVar(req, "secret")
	if authRef == "" {
		return ScraperCredentialInfo{}, clientError("authRef is required")
	}
	if strings.TrimSpace(secret) == "" {
		return ScraperCredentialInfo{}, clientError("secret is required")
	}
	if _, ok := h.declaredScrapersForAuthRef(authRef); !ok {
		return ScraperCredentialInfo{}, clientError("unknown scraper credential")
	}
	if h.scraperCredentials == nil {
		return ScraperCredentialInfo{}, clientError("scraper credentials are unavailable on this server")
	}
	if err := h.scraperCredentials.SetSecret(authRef, secret); err != nil {
		return ScraperCredentialInfo{}, clientError("scraper credential could not be saved")
	}
	if h.scraperCredentialInvalidator != nil {
		h.scraperCredentialInvalidator.InvalidateCredential(authRef)
	}
	if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "scraper-credential", authRef, ""); err != nil {
		return ScraperCredentialInfo{}, err
	}
	return h.scraperCredentialInfo(authRef)
}

func (h *Handler) validateScraperCredential(_ context.Context, req request) (ScraperCredentialInfo, error) {
	authRef := trimmedVariable(req, "authRef")
	if authRef == "" {
		return ScraperCredentialInfo{}, clientError("authRef is required")
	}
	return h.scraperCredentialInfo(authRef)
}

func (h *Handler) disableScraperCredential(ctx context.Context, scope auth.Scope, req request) (ScraperCredentialInfo, error) {
	authRef := trimmedVariable(req, "authRef")
	if authRef == "" {
		return ScraperCredentialInfo{}, clientError("authRef is required")
	}
	if _, ok := h.declaredScrapersForAuthRef(authRef); !ok {
		return ScraperCredentialInfo{}, clientError("unknown scraper credential")
	}
	if h.scraperCredentials == nil {
		return ScraperCredentialInfo{}, clientError("scraper credentials are unavailable on this server")
	}
	if err := h.scraperCredentials.SetSecret(authRef, ""); err != nil {
		return ScraperCredentialInfo{}, clientError("scraper credential could not be disabled")
	}
	if h.scraperCredentialInvalidator != nil {
		h.scraperCredentialInvalidator.InvalidateCredential(authRef)
	}
	if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "scraper-credential", authRef, ""); err != nil {
		return ScraperCredentialInfo{}, err
	}
	return h.scraperCredentialInfo(authRef)
}

// setPluginActive activates or deactivates one available plugin and returns the
// refreshed console config (manager-gated in execute). The pluginID must name a
// plugin the registry actually loaded — an unknown id is a client error, so the
// override table can never carry a phantom plugin.
func (h *Handler) setPluginActive(ctx context.Context, scope auth.Scope, req request) (ConsoleConfigInfo, error) {
	pluginID := trimmedVariable(req, "pluginID")
	if pluginID == "" {
		return ConsoleConfigInfo{}, clientError("pluginID is required")
	}
	v := newVars(req)
	active, ok := v.Bool("active")
	if err := v.Err(); err != nil {
		return ConsoleConfigInfo{}, err
	}
	if !ok {
		return ConsoleConfigInfo{}, clientError("active is required")
	}
	if h.plugins == nil {
		return ConsoleConfigInfo{}, clientError("unknown plugin")
	}
	if _, found := h.plugins.Bundle(plugin.ID(pluginID)); !found {
		return ConsoleConfigInfo{}, clientError("unknown plugin")
	}
	if h.pluginSuppressed(plugin.ID(pluginID)) {
		return ConsoleConfigInfo{}, clientError("unknown plugin")
	}
	if err := h.store.SetPluginActivation(ctx, pluginID, active, time.Now().UTC()); err != nil {
		return ConsoleConfigInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityPluginActivation, "plugin", pluginID, ""); err != nil {
		return ConsoleConfigInfo{}, err
	}
	return h.consoleConfig(ctx, scope, req)
}

// uninstallPluginData purges ALL stored Entity data for a plugin across the
// Libraries bound to it (PANEL-8 / ADR-0104 Uninstall) and deactivates the
// plugin so a compiled-in first-party plugin does not reload and recreate the
// data. It is the explicit, operator-confirmed purge — distinct from
// deactivation, which leaves data inert and reversible — so it
// requires `confirm: true` (invariant #6: destructive ops are confirmed). The
// plugin must be present in the loaded registry so its declared Entity Kinds are
// resolvable; it may already be deactivated. Admin-Session-gated in execute.
func (h *Handler) uninstallPluginData(ctx context.Context, scope auth.Scope, req request) (UninstallPluginDataResult, error) {
	pluginID := trimmedVariable(req, "pluginID")
	if pluginID == "" {
		return UninstallPluginDataResult{}, clientError("pluginID is required")
	}
	if !boolVar(req, "confirm") {
		return UninstallPluginDataResult{}, clientError("uninstall is destructive and must be confirmed (confirm: true)")
	}
	if h.plugins == nil {
		return UninstallPluginDataResult{}, clientError("unknown plugin")
	}
	bundle, found := h.plugins.Bundle(plugin.ID(pluginID))
	if !found {
		return UninstallPluginDataResult{}, clientError("unknown plugin")
	}
	if h.pluginSuppressed(plugin.ID(pluginID)) {
		return UninstallPluginDataResult{}, clientError("unknown plugin")
	}
	kinds := make([]string, 0, len(bundle.EntityKinds))
	for _, k := range bundle.EntityKinds {
		kinds = append(kinds, k.Name)
	}
	// Purge every Library bound to this plugin (a Library binds exactly one
	// Plugin, ADR-0003 §6, so PurgePluginData's (library, kind) scope is exact).
	libraries, err := h.store.Libraries(ctx, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return UninstallPluginDataResult{}, err
	}
	var bound []store.LibraryID
	for _, lib := range libraries {
		if lib.PluginID == nil || *lib.PluginID != pluginID {
			continue
		}
		bound = append(bound, lib.ID)
	}
	purged, err := h.store.PurgePluginData(ctx, bound, kinds)
	if err != nil {
		return UninstallPluginDataResult{}, err
	}
	// Deactivate so the purged plugin does not reload + recreate its Entities.
	if err := h.store.SetPluginActivation(ctx, pluginID, false, time.Now().UTC()); err != nil {
		return UninstallPluginDataResult{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityPluginActivation, "plugin", pluginID, ""); err != nil {
		return UninstallPluginDataResult{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityPluginUninstall, strconv.FormatInt(purged, 10), pluginID, ""); err != nil {
		return UninstallPluginDataResult{}, err
	}
	cfg, err := h.consoleConfig(ctx, scope, req)
	if err != nil {
		return UninstallPluginDataResult{}, err
	}
	return UninstallPluginDataResult{ConsoleConfigInfo: cfg, Purged: int(purged)}, nil
}

func (h *Handler) setPluginConfig(ctx context.Context, scope auth.Scope, req request) (ConsoleConfigInfo, error) {
	pluginID := trimmedVariable(req, "pluginID")
	if pluginID == "" {
		return ConsoleConfigInfo{}, clientError("pluginID is required")
	}
	if h.plugins == nil {
		return ConsoleConfigInfo{}, clientError("unknown plugin")
	}
	bundle, found := h.plugins.Bundle(plugin.ID(pluginID))
	if !found {
		return ConsoleConfigInfo{}, clientError("unknown plugin")
	}
	if h.pluginSuppressed(plugin.ID(pluginID)) {
		return ConsoleConfigInfo{}, clientError("unknown plugin")
	}
	rawValues := anyMapVar(req, "values")
	clears := stringSliceVar(req, "clear")
	if len(rawValues) == 0 && len(clears) == 0 {
		return ConsoleConfigInfo{}, clientError("values or clear is required")
	}
	fieldSpecs := map[string]plugin.ConfigFieldSpec{}
	for _, field := range bundle.ConfigFields {
		fieldSpecs[field.Name] = field
	}
	writes := map[string]string{}
	for name, raw := range rawValues {
		spec, ok := fieldSpecs[name]
		if !ok {
			return ConsoleConfigInfo{}, clientError("unknown plugin config field")
		}
		value, err := encodePluginConfigValue(spec, raw)
		if err != nil {
			return ConsoleConfigInfo{}, err
		}
		writes[name] = value
	}
	for _, name := range clears {
		spec, ok := fieldSpecs[name]
		if !ok {
			return ConsoleConfigInfo{}, clientError("unknown plugin config field")
		}
		if spec.Required && spec.Default == "" {
			return ConsoleConfigInfo{}, clientError("required plugin config field cannot be cleared")
		}
	}
	if err := h.store.SetPluginConfigValues(ctx, pluginID, writes, clears, time.Now().UTC()); err != nil {
		return ConsoleConfigInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "plugin-config", pluginID, ""); err != nil {
		return ConsoleConfigInfo{}, err
	}
	return h.consoleConfig(ctx, scope, req)
}

func encodePluginConfigValue(spec plugin.ConfigFieldSpec, raw any) (string, error) {
	var value string
	switch spec.Type {
	case "flag":
		flag, ok := raw.(bool)
		if !ok {
			return "", clientError(spec.Name + " must be a boolean")
		}
		if flag {
			value = "1"
		} else {
			value = "0"
		}
	case "number":
		number, ok := raw.(float64)
		if !ok {
			return "", clientError(spec.Name + " must be a number")
		}
		value = strconv.FormatFloat(number, 'f', -1, 64)
	default:
		text, ok := raw.(string)
		if !ok {
			return "", clientError(spec.Name + " must be a string")
		}
		value = strings.TrimSpace(text)
	}
	if spec.Required && value == "" {
		return "", clientError(spec.Name + " is required")
	}
	if len(spec.Options) > 0 {
		allowed := false
		for _, option := range spec.Options {
			if option.Value == value {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", clientError(fmt.Sprintf("%s must be one of the declared options", spec.Name))
		}
	}
	return value, nil
}
