package graph

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/integrationhost"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// newConsoleHandler builds a handler backed by a real store and a two-plugin
// registry so the console-config plugin-activation join has something to report.
func newConsoleHandler(t *testing.T, st *store.Store) *Handler {
	t.Helper()
	registry, err := newTestPluginRegistry([]plugin.Bundle{
		{
			ID: "demo", Version: "0.1.0", DisplayName: plugin.L("Demo"), MediaLabel: plugin.L("Item"),
			Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
			EntityKinds: []plugin.EntityKind{{Name: "widget", Archetype: "category"}},
			ConfigFields: []plugin.ConfigFieldSpec{
				{
					Name: "mode", Type: "text", Label: plugin.L("Mode"), Required: true, Default: "observe",
					Options: []plugin.ConfigOption{
						{Value: "observe", Label: plugin.L("Observe")},
						{Value: "apply", Label: plugin.L("Apply")},
					},
				},
				{Name: "enabled", Type: "flag", Label: plugin.L("Enabled"), Default: "0"},
				{Name: "limit", Type: "number", Label: plugin.L("Limit")},
			},
			Permissions: []plugin.PermissionSpec{
				{Kind: "declared-outbound", Label: plugin.L("Declared outbound access"), Description: plugin.L("Allows the plugin to send data to operator-approved network destinations.")},
				{Kind: "read-catalog", Label: plugin.L("Read catalog"), Description: plugin.L("Allows the plugin to read Reveal-filtered catalog metadata. Catalog reads require explicit approval and are audited."), HighRisk: true},
			},
			Actions: []plugin.ActionSpec{{
				ID:                  "test-connection",
				Label:               plugin.L("Test connection"),
				Icon:                "plug",
				Risk:                "low",
				RequiredPermissions: []string{"declared-outbound"},
				InputFields:         []plugin.ConfigFieldSpec{{Name: "dry-run", Type: "flag", Label: plugin.L("Dry run")}},
				Confirmation:        plugin.L("Run the connection test?"),
			}},
			Scrapers: []plugin.ScraperSpec{
				{ID: "alpha", DisplayName: plugin.L("Alpha"), Kind: "http-json", Endpoint: "https://metadata.example.test", AuthRef: "shared-api-key", AuthStyle: "query:key", SearchTemplate: "/search?q={query}", CandidatesPath: "items", CandidateIDPath: "id"},
				{ID: "beta", DisplayName: plugin.L("Beta"), Kind: "http-json", Endpoint: "https://metadata.example.test", AuthRef: "shared-api-key", AuthStyle: "query:key", SearchTemplate: "/tv?q={query}", CandidatesPath: "items", CandidateIDPath: "id"},
				{ID: "public", DisplayName: plugin.L("Public"), Kind: "http-json", Endpoint: "https://open.example.test", SearchTemplate: "/find?q={query}", CandidatesPath: "items", CandidateIDPath: "id"},
			},
		},
		{ID: "plain", Version: "0.1.0", DisplayName: plugin.L("Plain"), MediaLabel: plugin.L("Thing"), Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
		auth.NewService(st),
		st,
		registry,
	)
}

type memoryScraperCredentials struct {
	values map[string]string
}

type recordingObservabilityController struct {
	enabled bool
	calls   int
}

func (r *recordingObservabilityController) SetEnabled(_ context.Context, enabled bool) error {
	r.enabled = enabled
	r.calls++
	return nil
}

func (m *memoryScraperCredentials) Secret(ref string) (string, error) {
	if m.values == nil {
		m.values = map[string]string{}
	}
	secret := strings.TrimSpace(m.values[ref])
	if secret == "" {
		return "", fmt.Errorf("credential %q unavailable", ref)
	}
	return secret, nil
}

func (m *memoryScraperCredentials) SetSecret(ref, value string) error {
	if m.values == nil {
		m.values = map[string]string{}
	}
	if strings.TrimSpace(value) == "" {
		delete(m.values, ref)
		return nil
	}
	m.values[ref] = value
	return nil
}

type recordingCredentialInvalidator struct {
	refs []string
}

func (r *recordingCredentialInvalidator) InvalidateCredential(ref string) {
	r.refs = append(r.refs, ref)
}

// TestConsoleConfigAdminGate proves the console-config resolver is
// Admin-Session-gated (ADR-0007 §4 Console mutation class): a session without an
// open Admin Session is refused, a session that has opened one reads config +
// plugin activation through the real wire path (feature:console-config).
func TestConsoleConfigAdminGate(t *testing.T) {
	// feature:console-config
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)

	// A manager without an open Admin Session is denied (a viewer is too).
	_, errs := post(t, handler, `{"query":"query { consoleConfig { backgroundWorkers } }"}`, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("non-admin consoleConfig errors = %#v, want admin-session gate", errs)
	}

	// Open the Admin Session window, then the Console read is authorized.
	openManagerAdminSession(t, st)

	// Manager reads the default config and both available plugins (default-active).
	data, errs := post(t, handler,
		`{"query":"query { consoleConfig { backgroundWorkers plugins { id displayName provenance trustState active } } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("manager consoleConfig errors = %#v", errs)
	}
	var cfg ConsoleConfigInfo
	mustUnmarshal(t, data["consoleConfig"], &cfg)
	if cfg.BackgroundWorkers != 1 {
		t.Fatalf("backgroundWorkers = %d, want default 1", cfg.BackgroundWorkers)
	}
	if len(cfg.Plugins) != 2 {
		t.Fatalf("plugins = %#v, want 2 available", cfg.Plugins)
	}
	for _, p := range cfg.Plugins {
		if !p.Active {
			t.Fatalf("plugin %q active = false, want default-active", p.ID)
		}
		if p.Provenance != plugin.ProvenanceSideloaded || p.TrustState != plugin.PluginUnsigned {
			t.Fatalf("plugin %q standing = %q/%q, want Sideloaded/Unsigned", p.ID, p.Provenance, p.TrustState)
		}
	}
}

func TestConsoleConfigSuppressesPluginSurfaces(t *testing.T) {
	// feature:console-config
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	handler.SetSuppressedPlugins([]plugin.ID{"demo"})
	openManagerAdminSession(t, st)

	data, errs := post(t, handler,
		`{"query":"query { consoleConfig { plugins { id } } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("consoleConfig errors = %#v", errs)
	}
	var cfg ConsoleConfigInfo
	mustUnmarshal(t, data["consoleConfig"], &cfg)
	if len(cfg.Plugins) != 1 || cfg.Plugins[0].ID != "plain" {
		t.Fatalf("plugins = %#v, want only unsuppressed plain plugin", cfg.Plugins)
	}

	_, errs = post(t, handler,
		`{"query":"mutation A($pluginID: String!, $active: Boolean!){ setPluginActive(pluginID:$pluginID, active:$active){ plugins { id } } }","variables":{"pluginID":"demo","active":false}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown plugin") {
		t.Fatalf("setPluginActive(suppressed) errors = %#v, want unknown-plugin refusal", errs)
	}

	_, errs = post(t, handler,
		`{"query":"mutation($pluginID:String!,$values:JSON!){ setPluginConfig(pluginID:$pluginID, values:$values){ plugins { id } } }","variables":{"pluginID":"demo","values":{"mode":"apply"}}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown plugin") {
		t.Fatalf("setPluginConfig(suppressed) errors = %#v, want unknown-plugin refusal", errs)
	}
}

// TestPluginConfigSurfacePersistsAndAudits proves WEB-5I's Console plugin
// substrate exposes typed config, permissions, status, action descriptors, and
// audited config writes without leaking any secret material.
func TestPluginConfigSurfacePersistsAndAudits(t *testing.T) {
	// feature:web-5i-plugin-config
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	openManagerAdminSession(t, st)

	data, errs := post(t, handler,
		`{"query":"query { consoleConfig { plugins { id controlPolicy config { name type label required default value configured options { value label } } permissions { kind label highRisk } status { id state detail } actions { id label icon risk requiredPermissions inputFields { name type label } confirmation } } } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("consoleConfig plugin surface errors = %#v", errs)
	}
	var cfg ConsoleConfigInfo
	mustUnmarshal(t, data["consoleConfig"], &cfg)
	var demo PluginActivationInfo
	for _, p := range cfg.Plugins {
		if p.ID == "demo" {
			demo = p
		}
	}
	if demo.ID == "" {
		t.Fatalf("demo plugin missing from consoleConfig: %#v", cfg.Plugins)
	}
	if demo.ControlPolicy != "manager-enabled" {
		t.Fatalf("control policy = %q", demo.ControlPolicy)
	}
	if len(demo.Config) != 3 || demo.Config[0].Name != "mode" || demo.Config[0].Value != "observe" || demo.Config[0].Configured {
		t.Fatalf("config defaults = %#v", demo.Config)
	}
	if len(demo.Config[0].Options) != 2 || demo.Config[0].Options[1].Value != "apply" {
		t.Fatalf("mode options = %#v", demo.Config[0].Options)
	}
	if len(demo.Permissions) != 2 || demo.Permissions[1].Kind != "read-catalog" || !demo.Permissions[1].HighRisk {
		t.Fatalf("permissions = %#v", demo.Permissions)
	}
	if len(demo.Actions) != 1 || demo.Actions[0].ID != "test-connection" || demo.Actions[0].RequiredPermissions[0] != "declared-outbound" {
		t.Fatalf("actions = %#v", demo.Actions)
	}
	var sawActivation, sawEndpoint bool
	for _, status := range demo.Status {
		if status.ID == "activation" && status.State == "enabled" {
			sawActivation = true
		}
		if status.ID == "endpoint-alpha" && status.State == "declared" && status.Detail == "https://metadata.example.test" {
			sawEndpoint = true
		}
	}
	if !sawActivation || !sawEndpoint {
		t.Fatalf("status = %#v, want activation + endpoint rows", demo.Status)
	}

	body := `{"query":"mutation($pluginID:String!,$values:JSON!){ setPluginConfig(pluginID:$pluginID, values:$values){ plugins { id config { name value configured } } } }","variables":{"pluginID":"demo","values":{"mode":"apply","enabled":true,"limit":12}}}`
	data, errs = post(t, handler, body, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setPluginConfig errors = %#v", errs)
	}
	var after ConsoleConfigInfo
	mustUnmarshal(t, data["setPluginConfig"], &after)
	values := map[string]PluginConfigFieldInfo{}
	for _, p := range after.Plugins {
		if p.ID != "demo" {
			continue
		}
		for _, field := range p.Config {
			values[field.Name] = field
		}
	}
	if values["mode"].Value != "apply" || !values["mode"].Configured ||
		values["enabled"].Value != "1" || values["limit"].Value != "12" {
		t.Fatalf("persisted plugin config = %#v", values)
	}

	logData, logErrs := post(t, handler,
		`{"query":"query { activityLog { action targetType targetID } }"}`,
		authorizeManager)
	if len(logErrs) != 0 {
		t.Fatalf("activityLog errors = %#v", logErrs)
	}
	var entries []ActivityEntryInfo
	mustUnmarshal(t, logData["activityLog"], &entries)
	var audited bool
	for _, entry := range entries {
		if entry.Action == store.ActivityConfigUpdate && entry.TargetType == "plugin-config" && entry.TargetID == "demo" {
			audited = true
		}
	}
	if !audited {
		t.Fatalf("missing plugin-config audit row: %#v", entries)
	}

	_, errs = post(t, handler,
		`{"query":"mutation($pluginID:String!,$values:JSON!){ setPluginConfig(pluginID:$pluginID, values:$values){ plugins { id } } }","variables":{"pluginID":"demo","values":{"ghost":"x"}}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown plugin config field") {
		t.Fatalf("setPluginConfig ghost errors = %#v", errs)
	}
}

// TestScraperCredentialsAreAuthRefScoped proves WEB-5H's Console surface lists
// declared endpoints, manages the shared AuthRef secure-store entry, invalidates
// runtime login-token state on rotate/disable, audits changes, and never echoes
// secret bytes.
func TestScraperCredentialsAreAuthRefScoped(t *testing.T) {
	// feature:web-5h-scraper-credentials
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	authRef := plugin.ScopedCredentialRef("demo", "shared-api-key")
	secrets := &memoryScraperCredentials{}
	invalidator := &recordingCredentialInvalidator{}
	handler.SetScraperCredentialStore(secrets)
	handler.SetScraperCredentialInvalidator(invalidator)
	openManagerAdminSession(t, st)

	data, errs := post(t, handler,
		`{"query":"query { consoleConfig { plugins { id scrapers { scraperID endpoint authRef authStyle credentialRequired credentialConfigured credentialStatus credentialError } } } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("consoleConfig scrapers errors = %#v", errs)
	}
	var cfg ConsoleConfigInfo
	mustUnmarshal(t, data["consoleConfig"], &cfg)
	var sharedRows, publicRows int
	for _, p := range cfg.Plugins {
		for _, scraper := range p.Scrapers {
			switch scraper.ScraperID {
			case "alpha", "beta":
				sharedRows++
				if scraper.AuthRef != authRef || scraper.AuthStyle != "query:key" {
					t.Fatalf("shared scraper auth = %#v", scraper)
				}
				if !scraper.CredentialRequired || scraper.CredentialConfigured || scraper.CredentialStatus != "missing" || scraper.CredentialError != "missing" {
					t.Fatalf("missing shared credential status = %#v", scraper)
				}
			case "public":
				publicRows++
				if scraper.CredentialRequired || scraper.CredentialStatus != "not-required" {
					t.Fatalf("public scraper credential status = %#v", scraper)
				}
			}
		}
	}
	if sharedRows != 2 || publicRows != 1 {
		t.Fatalf("scraper rows shared=%d public=%d, want 2 and 1", sharedRows, publicRows)
	}

	body := fmt.Sprintf(`{"query":"mutation($authRef:String!,$secret:String!){ setScraperCredential(authRef:$authRef, secret:$secret) { authRef credentialConfigured credentialStatus scraperIDs } }","variables":{"authRef":%q,"secret":"super-secret"}}`, authRef)
	data, errs = post(t, handler, body, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setScraperCredential errors = %#v", errs)
	}
	if raw := string(data["setScraperCredential"]); strings.Contains(raw, "super-secret") {
		t.Fatalf("setScraperCredential echoed secret: %s", raw)
	}
	var setResult ScraperCredentialInfo
	mustUnmarshal(t, data["setScraperCredential"], &setResult)
	if setResult.AuthRef != authRef || !setResult.CredentialConfigured || setResult.CredentialStatus != "configured" || len(setResult.ScraperIDs) != 2 {
		t.Fatalf("setScraperCredential result = %#v", setResult)
	}
	if secrets.values[authRef] != "super-secret" {
		t.Fatalf("secure store %s = %q", authRef, secrets.values[authRef])
	}
	if got := strings.Join(invalidator.refs, ","); got != authRef {
		t.Fatalf("invalidated refs after set = %q", got)
	}

	data, errs = post(t, handler,
		fmt.Sprintf(`{"query":"mutation($authRef:String!){ validateScraperCredential(authRef:$authRef) { authRef credentialConfigured credentialStatus scraperIDs } }","variables":{"authRef":%q}}`, authRef),
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("validateScraperCredential errors = %#v", errs)
	}
	var validated ScraperCredentialInfo
	mustUnmarshal(t, data["validateScraperCredential"], &validated)
	if !validated.CredentialConfigured || validated.CredentialStatus != "configured" || len(validated.ScraperIDs) != 2 {
		t.Fatalf("validateScraperCredential result = %#v", validated)
	}

	data, errs = post(t, handler,
		fmt.Sprintf(`{"query":"mutation($authRef:String!){ disableScraperCredential(authRef:$authRef) { authRef credentialConfigured credentialStatus credentialError } }","variables":{"authRef":%q}}`, authRef),
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("disableScraperCredential errors = %#v", errs)
	}
	var disabled ScraperCredentialInfo
	mustUnmarshal(t, data["disableScraperCredential"], &disabled)
	if disabled.CredentialConfigured || disabled.CredentialStatus != "missing" || disabled.CredentialError != "missing" {
		t.Fatalf("disabled credential status = %#v", disabled)
	}
	if _, ok := secrets.values[authRef]; ok {
		t.Fatalf("%s survived disable", authRef)
	}
	if got := strings.Join(invalidator.refs, ","); got != authRef+","+authRef {
		t.Fatalf("invalidated refs after disable = %q", got)
	}

	logData, logErrs := post(t, handler,
		`{"query":"query { activityLog { action targetType targetID } }"}`,
		authorizeManager)
	if len(logErrs) != 0 {
		t.Fatalf("activityLog errors = %#v", logErrs)
	}
	var entries []ActivityEntryInfo
	mustUnmarshal(t, logData["activityLog"], &entries)
	var audits int
	for _, entry := range entries {
		if entry.Action == store.ActivityConfigUpdate && entry.TargetType == "scraper-credential" && entry.TargetID == authRef {
			audits++
		}
	}
	if audits != 2 {
		t.Fatalf("scraper credential audit rows = %d, want 2", audits)
	}
}

type credentialGrantSource map[string][]integrationhost.Grant

func (g credentialGrantSource) Grants(_ context.Context, pluginID string) ([]integrationhost.Grant, error) {
	return g[pluginID], nil
}

func (g credentialGrantSource) DeclaredPermissions(_ context.Context, pluginID string) ([]string, error) {
	grants := g[pluginID]
	permissions := make([]string, 0, len(grants))
	for _, grant := range grants {
		permissions = append(permissions, grant.Kind)
	}
	return permissions, nil
}

func TestScraperCredentialsAreIsolatedAcrossPluginBoundary(t *testing.T) {
	st := storetest.Open(t)
	bundle := func(id plugin.ID) plugin.Bundle {
		return plugin.Bundle{
			ID: id, Version: "0.1.0", DisplayName: plugin.L(string(id)), MediaLabel: plugin.L("Item"),
			Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
			Scrapers: []plugin.ScraperSpec{{
				ID: "source", DisplayName: plugin.L("Source"), Kind: "http-json",
				Endpoint: "https://metadata.example.test", AuthRef: "shared-key", AuthStyle: "bearer",
				SearchTemplate: "/search?q={query}", CandidatesPath: "items", CandidateIDPath: "id",
			}},
			Importers: []plugin.ImporterSpec{{
				ID: "source-import", DisplayName: plugin.L("Source import"), Categories: []string{"watch-state"},
				Input: "credentials", Conflict: "additive", CredentialRef: "shared-key",
			}},
		}
	}
	registry, err := newTestPluginRegistry([]plugin.Bundle{bundle("alpha"), bundle("beta")})
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithPlugins(st, registry)
	secrets := &memoryScraperCredentials{}
	handler.SetScraperCredentialStore(secrets)
	openManagerAdminSession(t, st)

	alphaRef := plugin.ScopedCredentialRef("alpha", "shared-key")
	body := fmt.Sprintf(`{"query":"mutation($authRef:String!,$secret:String!){ setScraperCredential(authRef:$authRef, secret:$secret) { authRef scraperIDs } }","variables":{"authRef":%q,"secret":"alpha-secret"}}`, alphaRef)
	data, errs := post(t, handler, body, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("set alpha credential errors = %#v", errs)
	}
	var result ScraperCredentialInfo
	mustUnmarshal(t, data["setScraperCredential"], &result)
	if got := strings.Join(result.ScraperIDs, ","); got != "alpha:source,alpha:source-import" {
		t.Fatalf("alpha credential targets = %q, want only alpha scraper/importer", got)
	}
	if _, errs := post(t, handler,
		`{"query":"mutation($authRef:String!,$secret:String!){ setScraperCredential(authRef:$authRef, secret:$secret) { authRef } }","variables":{"authRef":"shared-key","secret":"global-secret"}}`,
		authorizeManager); len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown scraper credential") {
		t.Fatalf("unscoped credential write errors = %#v, want unknown credential", errs)
	}

	grants := credentialGrantSource{
		"alpha": {{PluginID: "alpha", Kind: integrationhost.PermPluginSecret, Approved: true}},
		"beta":  {{PluginID: "beta", Kind: integrationhost.PermPluginSecret, Approved: true}},
	}
	alpha := integrationhost.NewHost("alpha", integrationhost.Deps{Secrets: secrets}, grants, nil)
	beta := integrationhost.NewHost("beta", integrationhost.Deps{Secrets: secrets}, grants, nil)
	if got, err := alpha.GetSecret(context.Background(), "shared-key"); err != nil || got != "alpha-secret" {
		t.Fatalf("alpha reads its credential = %q, err %v", got, err)
	}
	if _, err := beta.GetSecret(context.Background(), "shared-key"); err == nil {
		t.Fatal("beta read alpha's credential through its local AuthRef")
	}
	if _, err := beta.GetSecret(context.Background(), alphaRef); err == nil {
		t.Fatal("beta read alpha's credential by supplying alpha's scoped AuthRef")
	}
}

// TestSetConsoleConfigPersistsAndActivatesPlugin proves the two console-config
// mutations work end to end: a config write reads back, and a plugin
// deactivation is reflected in the next consoleConfig read; an unknown plugin id
// is refused (feature:console-config).
func TestSetConsoleConfigPersistsAndActivatesPlugin(t *testing.T) {
	// feature:console-config
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	observabilityController := &recordingObservabilityController{}
	handler.SetObservabilityController(observabilityController)
	openManagerAdminSession(t, st)
	demoLibrary, err := st.CreateLibrary(context.Background(), "Demo Data", false, "demo", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntity(context.Background(), store.Entity{
		ID: "deactivate-keeps-data", LibraryID: demoLibrary.ID, Kind: "widget", Title: "Widget",
	}); err != nil {
		t.Fatal(err)
	}

	// Mutate the resource-governor config.
	data, errs := post(t, handler,
		`{"query":"mutation S($backgroundWorkers: Int!, $eventsRetentionDays: Int!, $observabilityEnabled: Boolean!){ setConsoleConfig(backgroundWorkers:$backgroundWorkers, eventsRetentionDays:$eventsRetentionDays, observabilityEnabled:$observabilityEnabled){ backgroundWorkers eventsRetentionDays observabilityEnabled } }","variables":{"backgroundWorkers":8,"eventsRetentionDays":90,"observabilityEnabled":true}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setConsoleConfig errors = %#v", errs)
	}
	var after ConsoleConfigInfo
	mustUnmarshal(t, data["setConsoleConfig"], &after)
	if after.BackgroundWorkers != 8 {
		t.Fatalf("setConsoleConfig backgroundWorkers = %d, want 8", after.BackgroundWorkers)
	}
	if after.EventsRetentionDays != 90 {
		t.Fatalf("setConsoleConfig eventsRetentionDays = %d, want 90", after.EventsRetentionDays)
	}
	if !after.ObservabilityEnabled {
		t.Fatal("setConsoleConfig observabilityEnabled = false, want true")
	}
	if !observabilityController.enabled || observabilityController.calls != 1 {
		t.Fatalf("observability toggle = {%v calls=%d}, want {true calls=1}", observabilityController.enabled, observabilityController.calls)
	}

	// Deactivate the demo plugin; the read reflects it.
	data, errs = post(t, handler,
		`{"query":"mutation A($pluginID: String!, $active: Boolean!){ setPluginActive(pluginID:$pluginID, active:$active){ plugins { id active } } }","variables":{"pluginID":"demo","active":false}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setPluginActive errors = %#v", errs)
	}
	var activated ConsoleConfigInfo
	mustUnmarshal(t, data["setPluginActive"], &activated)
	demoActive := true
	plainActive := false
	for _, p := range activated.Plugins {
		switch p.ID {
		case "demo":
			demoActive = p.Active
		case "plain":
			plainActive = p.Active
		}
	}
	if demoActive {
		t.Fatal("demo plugin active = true after deactivate, want false")
	}
	if !plainActive {
		t.Fatal("plain plugin active = false, want still active")
	}
	if _, err := st.Entity(context.Background(), "deactivate-keeps-data", store.LibraryFilter{AllLibraries: true}); err != nil {
		t.Fatalf("setPluginActive(false) purged plugin data: %v", err)
	}

	// The config write also survived (read it fresh).
	data, errs = post(t, handler, `{"query":"query { consoleConfig { backgroundWorkers eventsRetentionDays observabilityEnabled } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("reread consoleConfig errors = %#v", errs)
	}
	var reread ConsoleConfigInfo
	mustUnmarshal(t, data["consoleConfig"], &reread)
	if reread.BackgroundWorkers != 8 {
		t.Fatalf("reread backgroundWorkers = %d, want 8", reread.BackgroundWorkers)
	}
	if reread.EventsRetentionDays != 90 {
		t.Fatalf("reread eventsRetentionDays = %d, want 90", reread.EventsRetentionDays)
	}
	if !reread.ObservabilityEnabled {
		t.Fatal("reread observabilityEnabled = false, want true")
	}

	// An unknown plugin id is a client error — the override table stays clean.
	_, errs = post(t, handler,
		`{"query":"mutation A($pluginID: String!, $active: Boolean!){ setPluginActive(pluginID:$pluginID, active:$active){ plugins { id } } }","variables":{"pluginID":"ghost","active":false}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown plugin") {
		t.Fatalf("setPluginActive(ghost) errors = %#v, want unknown-plugin client error", errs)
	}
	logData, logErrs := post(t, handler,
		`{"query":"query { activityLog { action } }"}`,
		authorizeManager)
	if len(logErrs) != 0 {
		t.Fatalf("activityLog errors = %#v", logErrs)
	}
	var entries []ActivityEntryInfo
	mustUnmarshal(t, logData["activityLog"], &entries)
	for _, entry := range entries {
		if entry.Action == store.ActivityPluginUninstall {
			t.Fatalf("setPluginActive(false) emitted uninstall audit: %#v", entries)
		}
	}
}

// TestUninstallPluginDataGuards covers the PANEL-8 / ADR-0104 Uninstall mutation's
// wiring + guards: it is Admin-Session-gated (the Console mutation class), it
// refuses without explicit confirmation (invariant #6: destructive ops are
// confirmed), and an unknown plugin id is a client error. The data-purge
// correctness itself is covered by store.TestPurgePluginData.
func TestUninstallPluginDataGuards(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	openManagerAdminSession(t, st)

	// Without confirm: refused, nothing purged.
	_, errs := post(t, handler,
		`{"query":"mutation U($pluginID: String!){ uninstallPluginData(pluginID:$pluginID){ plugins { id } } }","variables":{"pluginID":"demo"}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "confirm") {
		t.Fatalf("uninstall without confirm errors = %#v, want a confirmation client error", errs)
	}

	// Confirmed but unknown plugin: client error.
	_, errs = post(t, handler,
		`{"query":"mutation U($pluginID: String!, $confirm: Boolean!){ uninstallPluginData(pluginID:$pluginID, confirm:$confirm){ plugins { id } } }","variables":{"pluginID":"ghost","confirm":true}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown plugin") {
		t.Fatalf("uninstall(ghost) errors = %#v, want unknown-plugin client error", errs)
	}

	if err := st.SetPluginActivation(context.Background(), "demo", false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	data, errs := post(t, handler,
		`{"query":"mutation U($pluginID: String!, $confirm: Boolean!){ uninstallPluginData(pluginID:$pluginID, confirm:$confirm){ purged plugins { id active } } }","variables":{"pluginID":"demo","confirm":true}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("uninstall(deactivated demo) errors = %#v, want success", errs)
	}
	var cfg UninstallPluginDataResult
	mustUnmarshal(t, data["uninstallPluginData"], &cfg)
	for _, p := range cfg.Plugins {
		if p.ID == "demo" && p.Active {
			t.Fatal("demo plugin active after uninstall, want inactive")
		}
	}
}

func TestUninstallPluginDataAuditsAndCountsAcrossLibraries(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	openManagerAdminSession(t, st)
	ctx := context.Background()
	first, err := st.CreateLibrary(ctx, "Demo A", false, "demo", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateLibrary(ctx, "Demo B", false, "demo", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range []store.Entity{
		{ID: "uninstall-a", LibraryID: first.ID, Kind: "widget", Title: "A"},
		{ID: "uninstall-b", LibraryID: second.ID, Kind: "widget", Title: "B"},
	} {
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatalf("upsert %s: %v", entity.ID, err)
		}
	}
	data, errs := post(t, handler,
		`{"query":"mutation U($pluginID: String!, $confirm: Boolean!){ uninstallPluginData(pluginID:$pluginID, confirm:$confirm){ purged plugins { id active } } }","variables":{"pluginID":"demo","confirm":true}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("uninstall errors = %#v", errs)
	}
	var result UninstallPluginDataResult
	mustUnmarshal(t, data["uninstallPluginData"], &result)
	if result.Purged != 2 {
		t.Fatalf("purged = %d, want 2", result.Purged)
	}
	logData, logErrs := post(t, handler,
		`{"query":"query { activityLog { action targetType targetID } }"}`,
		authorizeManager)
	if len(logErrs) != 0 {
		t.Fatalf("activityLog errors = %#v", logErrs)
	}
	var entries []ActivityEntryInfo
	mustUnmarshal(t, logData["activityLog"], &entries)
	var uninstallEntries int
	for _, entry := range entries {
		if entry.Action != store.ActivityPluginUninstall {
			continue
		}
		uninstallEntries++
		if entry.TargetID != "demo" || entry.TargetType != "2" {
			t.Fatalf("uninstall activity = %#v, want plugin id + purged count", entry)
		}
	}
	if uninstallEntries != 1 {
		t.Fatalf("plugin.uninstall entries = %d, want 1: %#v", uninstallEntries, entries)
	}
}

// TestTranscodeConfigAdminGateAndPatch proves the transcode-config resolver
// is Admin-Session-gated (ADR-0007 §4 Operations), round-trips a partial write
// through the wire, and rejects an out-of-vocabulary codec
// (feature:transcode-config).
func TestTranscodeConfigAdminGateAndPatch(t *testing.T) {
	// feature:transcode-config
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)

	// A manager without an open Admin Session is denied.
	_, errs := post(t, handler, `{"query":"query { transcodeConfig { hardwareEnabled } }"}`, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("non-admin transcodeConfig errors = %#v, want admin-session gate", errs)
	}
	openManagerAdminSession(t, st)

	// Manager reads the default policy (HW off, empty policy).
	data, errs := post(t, handler,
		`{"query":"query { transcodeConfig { hardwareEnabled codec quality device configuredPreference hardwareAcceleration { accelerator status } } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("manager transcodeConfig errors = %#v", errs)
	}
	var cfg TranscodeConfigInfo
	mustUnmarshal(t, data["transcodeConfig"], &cfg)
	if cfg.HardwareEnabled || cfg.Codec != "" {
		t.Fatalf("default transcodeConfig = %#v, want HW off + empty policy", cfg)
	}

	// Update the policy.
	data, errs = post(t, handler,
		`{"query":"mutation T($hardwareEnabled: Boolean!, $codec: String!, $quality: String!, $device: String!){ setTranscodeConfig(hardwareEnabled:$hardwareEnabled, codec:$codec, quality:$quality, device:$device){ hardwareEnabled codec quality device } }","variables":{"hardwareEnabled":true,"codec":"hevc","quality":"high","device":"vt"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setTranscodeConfig errors = %#v", errs)
	}
	var after TranscodeConfigInfo
	mustUnmarshal(t, data["setTranscodeConfig"], &after)
	if !after.HardwareEnabled || after.Codec != "hevc" || after.Quality != "high" || after.Device != "vt" {
		t.Fatalf("setTranscodeConfig = %#v, want HW on hevc/high/vt", after)
	}

	// The write persists across a fresh read.
	data, errs = post(t, handler, `{"query":"query { transcodeConfig { hardwareEnabled codec quality device } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("reread transcodeConfig errors = %#v", errs)
	}
	var reread TranscodeConfigInfo
	mustUnmarshal(t, data["transcodeConfig"], &reread)
	if !reread.HardwareEnabled || reread.Codec != "hevc" || reread.Quality != "high" || reread.Device != "vt" {
		t.Fatalf("reread transcodeConfig = %#v, want HW on hevc/high/vt", reread)
	}

	// An out-of-vocabulary codec is rejected; prior policy survives.
	_, errs = post(t, handler,
		`{"query":"mutation T($codec: String!){ setTranscodeConfig(codec:$codec){ codec } }","variables":{"codec":"av1"}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "codec must be one of") {
		t.Fatalf("setTranscodeConfig(av1) errors = %#v, want codec-vocabulary client error", errs)
	}
}

func TestTranscodeConfigReportsSanitizedHardwareProbe(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	handler.SetHardwareAcceleration(config.HardwareAccelReport{
		Working: []string{"qsv"},
		Results: []config.HardwareAccelProbe{
			{Accelerator: "qsv", Status: "working"},
			{Accelerator: "vaapi", Status: "configured-but-failed"},
			{Accelerator: "nvenc", Status: "unavailable"},
			{Accelerator: "videotoolbox", Status: "unavailable"},
		},
	})
	openManagerAdminSession(t, st)
	device := "vaapi"
	enabled := true
	if _, err := st.SetTranscodeConfig(t.Context(), store.TranscodeConfigPatch{
		HardwareEnabled: &enabled,
		Device:          &device,
	}, time.Unix(1700, 0).UTC()); err != nil {
		t.Fatal(err)
	}

	data, errs := post(t, handler,
		`{"query":"query { transcodeConfig { hardwareEnabled device configuredPreference hardwareAcceleration { accelerator status } } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("transcodeConfig errors = %#v", errs)
	}
	var cfg TranscodeConfigInfo
	mustUnmarshal(t, data["transcodeConfig"], &cfg)
	if cfg.ConfiguredPreference != "vaapi" {
		t.Fatalf("ConfiguredPreference = %q, want vaapi", cfg.ConfiguredPreference)
	}
	got := map[string]string{}
	for _, probe := range cfg.HardwareAcceleration {
		got[probe.Accelerator] = probe.Status
	}
	if got["qsv"] != "working" || got["vaapi"] != "configured-but-failed" || got["nvenc"] != "unavailable" {
		t.Fatalf("hardwareAcceleration = %#v", cfg.HardwareAcceleration)
	}
	body := string(data["transcodeConfig"])
	for _, forbidden := range []string{"/dev/dri", "/Users/", "movie.mkv", "ffmpeg failed"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("transcodeConfig leaked %q in %s", forbidden, body)
		}
	}
}
