package graph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// newTestHandlerWithPlugins builds a handler over a store and an explicit
// plugin registry so the wizard tests can control plugin activation and
// importer declarations.
func newTestHandlerWithPlugins(st *store.Store, registry *plugin.Registry) *Handler {
	return NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
		auth.NewService(st),
		st,
		registry,
	)
}

// TestFirstRunWizardSetupState is the first-run-wizard vertical slice:
// the computed setup-completion state machine that drives
// onboarding. It is derived from server truth — admin password, first Library,
// owner Profile, plugin activation — with no first-run state table. The wizard
// orchestrates the EXISTING mutations; this test drives one (createLibrary)
// through the handler and asserts setupState reflects server truth at each step.
func TestFirstRunWizardSetupState(t *testing.T) {
	// feature:first-run-wizard
	st := storetest.Open(t)

	// Before activating any plugin and before the admin password exists, the
	// dev world already has a Library and an adult (owner) Profile, so setup is
	// incomplete only on those two axes.
	handler := newTestHandlerWithPlugins(st, nil)
	state := querySetupState(t, handler)
	if state.AdminPasswordSet {
		t.Fatal("adminPasswordSet true before any password is set")
	}
	if !state.HasLibrary || !state.HasOwnerProfile {
		t.Fatalf("setupState = %#v, want the seeded Library + owner Profile present", state)
	}
	if state.PluginsActivated {
		t.Fatal("pluginsActivated true with a nil registry")
	}
	if state.Complete {
		t.Fatal("setup reported complete before the admin password and plugins")
	}

	// Setting the admin password (the wizard's first step, the existing
	// SetAdminPassword path) flips that axis.
	if err := st.SetAdminPassword(t.Context(), "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	state = querySetupState(t, handler)
	if !state.AdminPasswordSet {
		t.Fatal("adminPasswordSet false after SetAdminPassword")
	}
	if state.Complete {
		t.Fatal("setup reported complete before a plugin is activated")
	}

	// Activating a plugin (a registry with at least one bundle) flips the last
	// axis, completing setup.
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "demo",
		Version:     "0.1.0",
		DisplayName: plugin.L("Demo"),
		MediaLabel:  plugin.L("Item"),
		Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler = newTestHandlerWithPlugins(st, registry)
	state = querySetupState(t, handler)
	if !state.PluginsActivated {
		t.Fatal("pluginsActivated false after activating a bundle")
	}
	if !state.Complete {
		t.Fatalf("setupState = %#v, want complete once every step is done", state)
	}

	// The wizard orchestrates the existing createLibrary mutation rather than
	// duplicating it: an admin session (createLibrary is a Console operation,
	// ADR-0007 §4) creates a Library and setupState still reports a Library
	// present (server truth, not the request echo). The onboarding sequence sets
	// the admin password first, then opens the Admin Session window — mirrored
	// here by SetAdminPassword above + opening the window now.
	openManagerAdminSession(t, st)
	data, errs := post(t, handler,
		`{"query":"mutation C($title:String!){ createLibrary(title:$title){ id title } }","variables":{"title":"New Library"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("createLibrary errors = %#v", errs)
	}
	var created LibraryInfo
	mustUnmarshal(t, data["createLibrary"], &created)
	if created.Title != "New Library" {
		t.Fatalf("createLibrary title = %q, want %q", created.Title, "New Library")
	}
	if state = querySetupState(t, handler); !state.HasLibrary {
		t.Fatal("hasLibrary false after creating a Library through the wizard's orchestrated mutation")
	}
}

// TestFirstRunWizardSetupStateNeedsSession asserts setupState is the bootstrap
// status behind a resolved session — it is in scopedFields, so an unauthorized
// request is rejected (401) before execute runs (it never leaks completion
// state to an unenrolled caller).
func TestFirstRunWizardSetupStateNeedsSession(t *testing.T) {
	// feature:first-run-wizard
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(`{"query":"query { setupState { complete } }"}`)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (setupState is in scopedFields)", rec.Code, http.StatusUnauthorized)
	}
}

func querySetupState(t *testing.T, handler *Handler) SetupState {
	t.Helper()
	data, errs := post(t, handler, `{"query":"query { setupState { adminPasswordSet hasLibrary hasOwnerProfile pluginsActivated complete } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setupState errors = %#v", errs)
	}
	var state SetupState
	mustUnmarshal(t, data["setupState"], &state)
	return state
}

// TestMigrationWizardImporterRegistry is the migration-wizard vertical slice:
// the wizard shell over the Importer Extension Point. It asserts
// the registry surfaces the importers an activated plugin declares, that an
// import run enqueues an additive, re-runnable job dedupe-keyed on the source,
// that status reads it back, and that an unknown source or an unsupported data
// category is refused.
func TestMigrationWizardImporterRegistry(t *testing.T) {
	// feature:migration-wizard
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "filmtv",
		Version:     "0.1.0",
		DisplayName: plugin.L("Film & TV"),
		MediaLabel:  plugin.L("Item"),
		Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
		Importers: []plugin.ImporterSpec{{
			ID:          "plex",
			DisplayName: plugin.L("Plex"),
			Categories:  []string{"watch-state", "ratings", "collections"},
			Input:       "export-file",
			Conflict:    "additive",
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithPlugins(st, registry)
	openManagerAdminSession(t, st)

	// The wizard lists the plugin-declared importer; Core hardcodes no source.
	data, errs := post(t, handler, `{"query":"query { importers { pluginID id displayName categories input conflict } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("importers errors = %#v", errs)
	}
	var list []ImporterInfo
	mustUnmarshal(t, data["importers"], &list)
	if len(list) != 1 || list[0].PluginID != "filmtv" || list[0].ID != "plex" || list[0].Conflict != "additive" {
		t.Fatalf("importers = %#v, want the filmtv/plex declaration", list)
	}
	if list[0].DisplayName.Resolve("") != "Plex" {
		t.Fatalf("importer display name = %q, want Plex", list[0].DisplayName.Resolve(""))
	}

	// Running the import enqueues a migration job. The plex importer is an
	// "export-file" source, so the operator-supplied sourcePath is required
	// — it plumbs through the payload to the source parser.
	data, errs = post(t, handler,
		`{"query":"mutation R($pluginID:String!,$importerID:String!,$sourcePath:String!){ runImport(pluginID:$pluginID,importerID:$importerID,sourcePath:$sourcePath){ id kind status } }","variables":{"pluginID":"filmtv","importerID":"plex","sourcePath":"/exports/plex.db"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("runImport errors = %#v", errs)
	}
	var job JobInfo
	mustUnmarshal(t, data["runImport"], &job)
	if job.Kind != string(migrationJobKind) || job.Status != string(store.JobStatusQueued) {
		t.Fatalf("runImport job = %#v, want a queued migration job", job)
	}

	// Re-running returns the SAME job (re-runnable + additive: dedupe-keyed on
	// the source, never double-enqueued while one is active — ADR-0037 §2.4).
	data, errs = post(t, handler,
		`{"query":"mutation R($pluginID:String!,$importerID:String!,$sourcePath:String!){ runImport(pluginID:$pluginID,importerID:$importerID,sourcePath:$sourcePath){ id } }","variables":{"pluginID":"filmtv","importerID":"plex","sourcePath":"/exports/plex.db"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("runImport re-run errors = %#v", errs)
	}
	var rerun JobInfo
	mustUnmarshal(t, data["runImport"], &rerun)
	if rerun.ID != job.ID {
		t.Fatalf("re-run job ID = %q, want the same active job %q (dedupe)", rerun.ID, job.ID)
	}

	data, errs = post(t, handler,
		`{"query":"mutation R($pluginID:String!,$importerID:String!,$sourcePath:String!,$dryRun:Boolean!){ runImport(pluginID:$pluginID,importerID:$importerID,sourcePath:$sourcePath,dryRun:$dryRun){ id } }","variables":{"pluginID":"filmtv","importerID":"plex","sourcePath":"/exports/plex.db","dryRun":true}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("dry-run runImport errors = %#v", errs)
	}
	var dryRun JobInfo
	mustUnmarshal(t, data["runImport"], &dryRun)
	if dryRun.ID == job.ID {
		t.Fatalf("dry-run job ID = %q, want distinct preview job", dryRun.ID)
	}
	dryJob, err := st.Job(t.Context(), store.JobID(dryRun.ID))
	if err != nil {
		t.Fatalf("read dry-run job: %v", err)
	}
	var dryPayload migrationJobPayload
	if err := json.Unmarshal(dryJob.Payload, &dryPayload); err != nil {
		t.Fatalf("decode dry-run payload: %v", err)
	}
	if !dryPayload.DryRun {
		t.Fatalf("dry-run payload = %#v, want dry_run true", dryPayload)
	}

	// Status reads the migration job back by ID.
	data, errs = post(t, handler,
		`{"query":"query S($jobID:String!){ importStatus(jobID:$jobID){ id kind status } }","variables":{"jobID":"`+job.ID+`"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("importStatus errors = %#v", errs)
	}
	var status JobInfo
	mustUnmarshal(t, data["importStatus"], &status)
	if status.ID != job.ID || status.Kind != string(migrationJobKind) {
		t.Fatalf("importStatus = %#v, want the migration job %q", status, job.ID)
	}

	// An unknown source is refused — the wizard never enqueues an import no
	// active plugin can service.
	_, errs = post(t, handler,
		`{"query":"mutation R($pluginID:String!,$importerID:String!){ runImport(pluginID:$pluginID,importerID:$importerID){ id } }","variables":{"pluginID":"filmtv","importerID":"jellyfin"}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown importer") {
		t.Fatalf("unknown-importer errors = %#v, want refusal", errs)
	}

	// A data category the importer cannot carry is refused rather than silently
	// dropped.
	_, errs = post(t, handler,
		`{"query":"mutation R($pluginID:String!,$importerID:String!,$sourcePath:String!,$categories:[String!]!){ runImport(pluginID:$pluginID,importerID:$importerID,sourcePath:$sourcePath,categories:$categories){ id } }","variables":{"pluginID":"filmtv","importerID":"plex","sourcePath":"/exports/plex.db","categories":["subscriptions"]}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "data category") {
		t.Fatalf("unsupported-category errors = %#v, want refusal", errs)
	}

	// An "export-file" importer with no operator-supplied sourcePath is refused:
	// the source has nothing to read without one.
	_, errs = post(t, handler,
		`{"query":"mutation R($pluginID:String!,$importerID:String!){ runImport(pluginID:$pluginID,importerID:$importerID){ id } }","variables":{"pluginID":"filmtv","importerID":"plex"}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "sourcePath is required") {
		t.Fatalf("missing-sourcePath errors = %#v, want refusal", errs)
	}
}

func TestMigrationWizardSuppressesPluginImporters(t *testing.T) {
	// feature:migration-wizard
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "demo",
		Version:     "0.1.0",
		DisplayName: plugin.L("Demo"),
		MediaLabel:  plugin.L("Item"),
		Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
		Importers: []plugin.ImporterSpec{{
			ID:          "demo-import",
			DisplayName: plugin.L("Demo Import"),
			Categories:  []string{"watch-state"},
			Input:       "export-file",
			Conflict:    "additive",
		}},
	}, {
		ID:          "filmtv",
		Version:     "0.1.0",
		DisplayName: plugin.L("Film & TV"),
		MediaLabel:  plugin.L("Item"),
		Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
		Importers: []plugin.ImporterSpec{{
			ID:          "plex",
			DisplayName: plugin.L("Plex"),
			Categories:  []string{"watch-state"},
			Input:       "export-file",
			Conflict:    "additive",
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithPlugins(st, registry)
	handler.SetSuppressedPlugins([]plugin.ID{"demo"})
	openManagerAdminSession(t, st)

	data, errs := post(t, handler, `{"query":"query { importers { pluginID id } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("importers errors = %#v", errs)
	}
	var list []ImporterInfo
	mustUnmarshal(t, data["importers"], &list)
	if len(list) != 1 || list[0].PluginID != "filmtv" || list[0].ID != "plex" {
		t.Fatalf("importers = %#v, want only unsuppressed filmtv/plex", list)
	}

	_, errs = post(t, handler,
		`{"query":"mutation R($pluginID:String!,$importerID:String!,$sourcePath:String!){ runImport(pluginID:$pluginID,importerID:$importerID,sourcePath:$sourcePath){ id } }","variables":{"pluginID":"demo","importerID":"demo-import","sourcePath":"/exports/demo.db"}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown importer") {
		t.Fatalf("runImport(suppressed) errors = %#v, want unknown importer", errs)
	}
}

// credentialsImporterHandler builds a handler whose one importer reads from a
// live source ("credentials") behind a secret-store ref, with a memory
// credential store wired in. The boolean configures whether the secret is set.
func credentialsImporterHandler(t *testing.T, st *store.Store, secretSet bool) *Handler {
	t.Helper()
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "filmtv",
		Version:     "0.1.0",
		DisplayName: plugin.L("Film & TV"),
		MediaLabel:  plugin.L("Item"),
		Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
		Importers: []plugin.ImporterSpec{{
			ID:            "emby",
			DisplayName:   plugin.L("Emby"),
			Categories:    []string{"watch-state"},
			Input:         "credentials",
			Conflict:      "additive",
			CredentialRef: "emby-token",
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithPlugins(st, registry)
	creds := &memoryScraperCredentials{}
	if secretSet {
		_ = creds.SetSecret(plugin.ScopedCredentialRef("filmtv", "emby-token"), "secret-token")
	}
	handler.SetScraperCredentialStore(creds)
	return handler
}

// TestMigrationWizardCredentialsImporterGating proves the credentials-input
// handling: a live-source importer surfaces its
// credential readiness on the wire, runImport is refused until the operator has
// stored the secret, and accepted once it is — the secret itself never crosses
// the wire (only the ref + a readiness word).
func TestMigrationWizardCredentialsImporterGating(t *testing.T) {
	// feature:migration-wizard
	st := storetest.Open(t)
	openManagerAdminSession(t, st)

	// No secret yet: importers reports credentialRequired but not configured, and
	// runImport is refused.
	handler := credentialsImporterHandler(t, st, false)
	data, errs := post(t, handler, `{"query":"query { importers { id input credentialRef credentialRequired credentialConfigured credentialStatus } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("importers errors = %#v", errs)
	}
	var list []ImporterInfo
	mustUnmarshal(t, data["importers"], &list)
	if len(list) != 1 || list[0].CredentialRef != plugin.ScopedCredentialRef("filmtv", "emby-token") || !list[0].CredentialRequired || list[0].CredentialConfigured {
		t.Fatalf("importer credential state = %#v, want required+unconfigured", list)
	}

	_, errs = post(t, handler,
		`{"query":"mutation R($pluginID:String!,$importerID:String!){ runImport(pluginID:$pluginID,importerID:$importerID){ id } }","variables":{"pluginID":"filmtv","importerID":"emby"}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "credentials are required") {
		t.Fatalf("runImport without credential errors = %#v, want refusal", errs)
	}

	// Secret stored: importers reports configured, and runImport now enqueues.
	handler = credentialsImporterHandler(t, st, true)
	data, errs = post(t, handler, `{"query":"query { importers { id credentialConfigured credentialStatus } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("importers (configured) errors = %#v", errs)
	}
	mustUnmarshal(t, data["importers"], &list)
	if !list[0].CredentialConfigured || list[0].CredentialStatus != "configured" {
		t.Fatalf("importer credential state = %#v, want configured", list)
	}

	data, errs = post(t, handler,
		`{"query":"mutation R($pluginID:String!,$importerID:String!){ runImport(pluginID:$pluginID,importerID:$importerID){ id kind status } }","variables":{"pluginID":"filmtv","importerID":"emby"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("runImport with credential errors = %#v", errs)
	}
	var job JobInfo
	mustUnmarshal(t, data["runImport"], &job)
	if job.Kind != string(migrationJobKind) {
		t.Fatalf("runImport job = %#v, want a queued migration job", job)
	}
}

// TestMigrationWizardProfileLibraryMapping proves the Profile/Library mapping
// inputs: runImport accepts a valid profileID + libraryID, and
// refuses an unknown Profile or an unknown Library so a run never routes
// imported engagement to a phantom history or a Library the scope can't see.
func TestMigrationWizardProfileLibraryMapping(t *testing.T) {
	// feature:migration-wizard
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "filmtv",
		Version:     "0.1.0",
		DisplayName: plugin.L("Film & TV"),
		MediaLabel:  plugin.L("Item"),
		Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
		Importers: []plugin.ImporterSpec{{
			ID:          "plex",
			DisplayName: plugin.L("Plex"),
			Categories:  []string{"watch-state"},
			Input:       "export-file",
			Conflict:    "additive",
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithPlugins(st, registry)
	openManagerAdminSession(t, st)

	const query = `mutation R($pluginID:String!,$importerID:String!,$sourcePath:String!,$profileID:String,$libraryID:String){ runImport(pluginID:$pluginID,importerID:$importerID,sourcePath:$sourcePath,profileID:$profileID,libraryID:$libraryID){ id } }`
	body := func(vars string) string {
		return `{"query":"` + query + `","variables":` + vars + `}`
	}

	// Valid mapping: the seeded profile + a seeded Library both resolve.
	data, errs := post(t, handler, body(`{"pluginID":"filmtv","importerID":"plex","sourcePath":"/exports/plex.db","profileID":"profile-default","libraryID":"library-local"}`), authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("runImport with valid mapping errors = %#v", errs)
	}
	var job JobInfo
	mustUnmarshal(t, data["runImport"], &job)
	if job.ID == "" {
		t.Fatalf("runImport with mapping did not enqueue: %#v", job)
	}

	// Unknown Profile is refused.
	_, errs = post(t, handler, body(`{"pluginID":"filmtv","importerID":"plex","sourcePath":"/exports/plex.db","profileID":"profile-ghost","libraryID":"library-local"}`), authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown profile") {
		t.Fatalf("unknown-profile errors = %#v, want refusal", errs)
	}

	// Unknown Library is refused.
	_, errs = post(t, handler, body(`{"pluginID":"filmtv","importerID":"plex","sourcePath":"/exports/plex.db","libraryID":"library-ghost"}`), authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown library") {
		t.Fatalf("unknown-library errors = %#v, want refusal", errs)
	}
}

// TestMigrationWizardGatedByAdminSession asserts the wizard's Console operations
// require an open Admin Session (ADR-0007 §4 Console mutation class).
func TestMigrationWizardGatedByAdminSession(t *testing.T) {
	// feature:migration-wizard
	handler := newTestHandler(t)
	_, errs := post(t, handler, `{"query":"query { importers { id } }"}`, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("importers without admin session errors = %#v, want admin-session gate", errs)
	}
}

// TestMigrationWizardEmptyWithoutImportPlugin asserts the honest empty state:
// a server whose active plugins declare no Importer surfaces no sources (the
// shell + extension point with nothing plugged in yet).
func TestMigrationWizardEmptyWithoutImportPlugin(t *testing.T) {
	// feature:migration-wizard
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "noimport",
		Version:     "0.1.0",
		DisplayName: plugin.L("No Import"),
		MediaLabel:  plugin.L("Item"),
		Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithPlugins(st, registry)
	openManagerAdminSession(t, st)
	data, errs := post(t, handler, `{"query":"query { importers { id } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("importers errors = %#v", errs)
	}
	var list []ImporterInfo
	mustUnmarshal(t, data["importers"], &list)
	if len(list) != 0 {
		t.Fatalf("importers = %#v, want empty without an import-capable plugin", list)
	}
}
