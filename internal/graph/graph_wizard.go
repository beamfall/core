package graph

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/store"
)

// migrationJobKind is the job-substrate kind for a Migration Wizard import
// (ADR-0037 §4: the import pipeline is a standard job). Core enqueues and
// reports these; the data transformation itself is the plugin Importer's job
// handler, registered when the source plugin ships. The kind is core-owned (a
// job class name, never a source-system name).
const migrationJobKind store.JobKind = "migration"

// SetupState reports first-run completion (ADR-0007 §2, feature
// first-run-wizard): the computed status of each onboarding step the Console
// wizard drives — admin password → first Library → owner Profile → plugin
// activation. It is a derived view over existing tables and the activated
// plugin registry; there is no first-run state table. Complete is true only
// when every step is done, so a fresh server's root URL knows to open the
// wizard and a configured one knows to skip it.
type SetupState struct {
	AdminPasswordSet bool `json:"adminPasswordSet"`
	HasLibrary       bool `json:"hasLibrary"`
	HasOwnerProfile  bool `json:"hasOwnerProfile"`
	PluginsActivated bool `json:"pluginsActivated"`
	Complete         bool `json:"complete"`
}

// setupState computes first-run completion from server truth. It never gates on
// Device Role or Admin Session: it IS the bootstrap status a fresh browser
// device reads before any of those tiers can exist. Reading completion leaks no
// secret — it reveals only whether onboarding finished, the same fact the
// wizard's presence already conveys.
func (h *Handler) setupState(ctx context.Context) (SetupState, error) {
	state := SetupState{PluginsActivated: len(h.plugins.IDs()) > 0}

	adminSet, err := h.store.AdminPasswordSet(ctx)
	if err != nil {
		return SetupState{}, err
	}
	state.AdminPasswordSet = adminSet

	// Library presence is asked over the hidden world (the wizard runs before
	// any Profile exists, so there is no scope to filter by); the owner Profile
	// is the first adult Profile. Both are plain existence checks. A nil
	// AllowedIDs with Revealed shows every Library, restricted included.
	libraries, err := h.store.Libraries(ctx, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		return SetupState{}, err
	}
	state.HasLibrary = len(libraries) > 0

	profiles, err := h.store.Profiles(ctx)
	if err != nil {
		return SetupState{}, err
	}
	for _, profile := range profiles {
		if profile.Kind == store.ProfileAdult {
			state.HasOwnerProfile = true
			break
		}
	}

	state.Complete = state.AdminPasswordSet && state.HasLibrary && state.HasOwnerProfile && state.PluginsActivated
	return state, nil
}

// ImporterInfo is one available Migration Wizard source: the
// wire shape of a plugin-declared ImporterSpec. PluginID names the plugin that
// declared it so the wizard can group sources by domain.
type ImporterInfo struct {
	PluginID    string              `json:"pluginID"`
	ID          string              `json:"id"`
	DisplayName plugin.LocaleString `json:"displayName"`
	Categories  []string            `json:"categories"`
	Input       string              `json:"input"`
	Conflict    string              `json:"conflict"`
	// CredentialRef is the secret-store entry the operator must populate before a
	// live-source ("credentials") importer can run (parallel to the scraper
	// credential surface). Empty for an "export-file" importer. The secret itself
	// is never on the wire (invariant #5) — only the ref and whether it is set.
	CredentialRef string `json:"credentialRef"`
	// CredentialRequired is true when this importer reads from a live source
	// behind a secret, so the wizard knows to collect one before enabling Run.
	CredentialRequired bool `json:"credentialRequired"`
	// CredentialConfigured is true when a secret has been stored under
	// CredentialRef and is readable, so the wizard can render "configured" and
	// allow Run. False (with CredentialRequired true) blocks the run client-side.
	CredentialConfigured bool `json:"credentialConfigured"`
	// CredentialStatus is a coarse readiness word (not-required / configured /
	// missing / invalid / unreadable / unavailable) the wizard renders as a chip.
	CredentialStatus string `json:"credentialStatus"`
}

// importers lists every Importer declared by an activated plugin (ADR-0037
// §2.1: the wizard's source menu). Core hardcodes no source name — it reads the
// activated bundles. A plugin that declares none simply contributes nothing, so
// a server with no import-capable plugin yields an empty list (the honest
// "nothing to migrate from yet" state).
func (h *Handler) importers() []ImporterInfo {
	infos := []ImporterInfo{}
	for _, id := range h.visiblePluginIDs() {
		bundle, ok := h.plugins.Bundle(id)
		if !ok {
			continue
		}
		for _, spec := range bundle.Importers {
			cred := h.scraperCredentialStatus(spec.CredentialRef)
			infos = append(infos, ImporterInfo{
				PluginID:             string(bundle.ID),
				ID:                   spec.ID,
				DisplayName:          spec.DisplayName,
				Categories:           spec.Categories,
				Input:                spec.Input,
				Conflict:             spec.Conflict,
				CredentialRef:        spec.CredentialRef,
				CredentialRequired:   spec.CredentialRef != "",
				CredentialConfigured: cred.configured,
				CredentialStatus:     cred.status,
			})
		}
	}
	return infos
}

// findImporter resolves a (pluginID, importerID) pair against the activated
// registry. A run is refused for an unknown pair so the wizard can never
// enqueue an import for a source no active plugin can service.
func (h *Handler) findImporter(pluginID, importerID string) (plugin.ImporterSpec, bool) {
	if h.pluginSuppressed(plugin.ID(pluginID)) {
		return plugin.ImporterSpec{}, false
	}
	bundle, ok := h.plugins.Bundle(plugin.ID(pluginID))
	if !ok {
		return plugin.ImporterSpec{}, false
	}
	for _, spec := range bundle.Importers {
		if spec.ID == importerID {
			return spec, true
		}
	}
	return plugin.ImporterSpec{}, false
}

// migrationJobPayload is the import job's stored request. It records the source
// the user chose; the plugin's Importer handler reads it to drive the import.
// Credentials are never stored here — an "export-file" importer references an
// uploaded file, a "credentials" importer references a config/secret entry; the
// payload carries identifiers, never secrets.
type migrationJobPayload struct {
	Version    int      `json:"version"`
	PluginID   string   `json:"plugin_id"`
	ImporterID string   `json:"importer_id"`
	Categories []string `json:"categories"`
	DryRun     bool     `json:"dry_run,omitempty"`
	// ProfileID is the operator's Profile — the Profile whose engagement the
	// handler's watch-state import writes to (watch-state is per-Profile, ADR-0037
	// §2.2). The handler refuses a run with no Profile rather than guessing whose
	// history the import is.
	ProfileID string `json:"profile_id"`
	// SourcePath is the operator-supplied location the chosen source reads from
	// (ADR-0037 §2.1: "collects credentials or export files"): an export-file
	// path or connection string. It carries the location only — never a secret
	// (invariant #5). The plugin's source parser interprets it; Core passes it
	// through to migrate.SourceResolver.Resolve so a path-needing parser can be
	// built. Required for an "export-file" importer (it has nothing to read
	// without one); optional otherwise.
	SourcePath string `json:"source_path"`
	// LibraryID is the target Library for subscription-list imports (required for
	// those) and otherwise optionally scopes the match chain to a single target
	// Library (the Profile/Library mapping the operator chose). Empty means match
	// across every Library (the default — ADR-0028 §6: match by path regardless of
	// which plugin owns the Library). When set, the handler only matches files
	// whose owning Library is this one, so an operator importing one server's
	// history can route it to the matching Library rather than the whole catalog.
	LibraryID string `json:"library_id,omitempty"`
	// Root is the optional target download root for imported feed subscriptions.
	Root string `json:"root,omitempty"`
}

// runImport enqueues an additive, re-runnable import job for one declared
// Importer. The job is dedupe-keyed on the source and target
// inputs so a re-run while a prior equivalent import is still active returns
// that job rather than double-enqueueing — the re-runnable contract at the orchestration layer. The
// import's additive semantics (fill gaps, advance strictly-ahead positions,
// never overwrite) are the plugin handler's responsibility; Core's job is the
// shell. The selected categories default to the importer's full declared set.
func (h *Handler) runImport(ctx context.Context, scope auth.Scope, req request) (JobInfo, error) {
	pluginID := trimmedVariable(req, "pluginID")
	importerID := trimmedVariable(req, "importerID")
	if pluginID == "" || importerID == "" {
		return JobInfo{}, clientError("pluginID and importerID are required")
	}
	spec, ok := h.findImporter(pluginID, importerID)
	if !ok {
		return JobInfo{}, clientError("unknown importer")
	}
	// SourcePath is the operator-supplied export-file path / connection string
	// the source reads from. An "export-file" importer has
	// nothing to read without one, so it is required for that input form;
	// "credentials" importers carry their location differently and may omit it.
	sourcePath := trimmedVariable(req, "sourcePath")
	if spec.Input == "export-file" && sourcePath == "" {
		return JobInfo{}, clientError("sourcePath is required for this importer")
	}
	// A live-source ("credentials") importer cannot run until its secret is
	// stored (invariant #5: the run references the secret-store ref, never the
	// secret itself). Refuse the run rather than enqueue a job that the parser
	// will fail to authenticate — the wizard collects the credential first.
	if spec.Input == "credentials" {
		if h.scraperCredentials == nil {
			return JobInfo{}, clientError("credential storage is unavailable on this server")
		}
		if _, err := h.scraperCredentials.Secret(spec.CredentialRef); err != nil {
			return JobInfo{}, clientError("credentials are required for this importer")
		}
	}
	root := trimmedVariable(req, "root")
	v := newVars(req)
	categories, hasCategories := v.StringList("categories", "categories")
	dryRun, _ := v.Bool("dryRun")
	if err := v.Err(); err != nil {
		return JobInfo{}, err
	}
	if !hasCategories {
		categories = spec.Categories
	} else {
		// A requested category the importer cannot carry is a client error,
		// not a silent drop — it would otherwise import less than the user
		// asked without telling them.
		for _, requested := range categories {
			if !containsString(spec.Categories, requested) {
				return JobInfo{}, clientError("importer does not support the requested data category")
			}
		}
	}
	// Profile/Library mapping: the operator maps the import to a
	// target Profile (whose engagement watch-state writes to) and optionally a
	// target Library (the match scope). The Profile defaults to the operator's own
	// (scope.ProfileID); an explicit profileID overrides it — an admin-gated
	// Console choice — but must name a real Profile so the run never writes to a
	// phantom history. An explicit libraryID must be one the scope can see.
	profileID := trimmedVariable(req, "profileID")
	if profileID == "" {
		profileID = string(scope.ProfileID)
	} else if err := h.requireProfile(ctx, profileID); err != nil {
		return JobInfo{}, err
	}
	libraryID := trimmedVariable(req, "libraryID")
	// Subscription-list / item-state imports attach to a target Library, so they
	// require one; other categories may omit it (empty = match across every
	// Library).
	if requiresSubscriptionTarget(categories) && libraryID == "" {
		return JobInfo{}, clientError("libraryID is required for subscription imports")
	}
	if libraryID != "" {
		visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
		if err != nil {
			return JobInfo{}, err
		}
		if !visible {
			return JobInfo{}, clientError("unknown library")
		}
	}
	payload, err := json.Marshal(migrationJobPayload{
		Version:    1,
		PluginID:   pluginID,
		ImporterID: importerID,
		Categories: categories,
		DryRun:     dryRun,
		ProfileID:  profileID,
		SourcePath: sourcePath,
		LibraryID:  libraryID,
		Root:       root,
	})
	if err != nil {
		return JobInfo{}, err
	}
	created, err := h.store.EnqueueJob(ctx, store.Job{
		Kind:      migrationJobKind,
		Class:     store.JobClassBackground,
		DedupeKey: migrationDedupeKey(pluginID, importerID, libraryID, root, sourcePath, categories, dryRun),
		Payload:   payload,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return JobInfo{}, err
	}
	return h.jobInfo(ctx, scope, created), nil
}

func requiresSubscriptionTarget(categories []string) bool {
	return containsString(categories, "subscriptions") || containsString(categories, "item-state")
}

func migrationDedupeKey(pluginID, importerID, libraryID, root, sourcePath string, categories []string, dryRun bool) string {
	return strings.Join([]string{
		pluginID,
		importerID,
		libraryID,
		root,
		sourcePath,
		strings.Join(categories, ","),
		strconv.FormatBool(dryRun),
	}, "\x1f")
}

// importStatus reports one migration job's progress (ADR-0037 §2.3: the
// match-rate report rides on the job's progress). It is the same read as job(),
// scoped to the migration kind so the wizard's status view cannot be pointed at
// an unrelated job.
func (h *Handler) importStatus(ctx context.Context, scope auth.Scope, req request) (ImportStatusInfo, error) {
	id := trimmedVariable(req, "jobID")
	if id == "" {
		return ImportStatusInfo{}, clientError("jobID is required")
	}
	job, err := h.store.Job(ctx, store.JobID(id))
	if err != nil {
		return ImportStatusInfo{}, clientError(store.ErrJobNotFound.Error())
	}
	if job.Kind != migrationJobKind {
		return ImportStatusInfo{}, clientError("not a migration job")
	}
	return h.importStatusInfo(ctx, scope, job)
}

// ScanEstimateInfo is the first-scan wizard's pre-scan projection on the wire
// (OPS-4 / ADR-0013 §2: "the wizard estimates and says what's deferrable — play
// immediately; sprites arrive overnight"). It is a computed view over a cheap
// filesystem walk — no scan runs, nothing is stored, the schema is untouched.
// The split is the product surface: estimatedFiles + estimatedInlineSeconds
// describe the must-do-now scan that gates playability, while deferrableWork
// enumerates the background asset backlog that does not.
type ScanEstimateInfo struct {
	EstimatedFiles             int                  `json:"estimatedFiles"`
	EstimatedVideoFiles        int                  `json:"estimatedVideoFiles"`
	EstimatedAudioFiles        int                  `json:"estimatedAudioFiles"`
	EstimatedInlineSeconds     int                  `json:"estimatedInlineSeconds"`
	InlineWorkGatesPlayability bool                 `json:"inlineWorkGatesPlayability"`
	DeferrableWork             []DeferrableWorkInfo `json:"deferrableWork"`
}

// DeferrableWorkInfo is one background asset-generation backlog kind (ADR-0033
// Generated Asset family). Kind is a generic asset-shape name (invariant #1);
// playabilityGating is always false — the machine-readable "this can wait."
type DeferrableWorkInfo struct {
	Kind              string `json:"kind"`
	EstimatedCount    int    `json:"estimatedCount"`
	PlayabilityGating bool   `json:"playabilityGating"`
}

// scanEstimate computes the first-scan wizard estimate for a root the operator
// is about to scan, or for an existing Library's persisted roots. It
// accepts a raw `root` path (the wizard's pre-Library case — it runs before a
// Library row exists) or a `libraryID` whose visible roots are walked (the
// re-estimate-an-existing-Library case), mirroring scanLibrary's root
// resolution. The walk is cheap (count + extension classify, no hash/probe), so
// the wizard can call it interactively. An admin-gated read: it discloses a
// filesystem layout, an operator-only concern (wired in execute()).
func (h *Handler) scanEstimate(ctx context.Context, scope auth.Scope, req request) (ScanEstimateInfo, error) {
	root := trimmedVariable(req, "root")
	libraryID := trimmedVariable(req, "libraryID")
	var roots []string
	switch {
	case root != "":
		roots = []string{root}
	case libraryID != "":
		// Resolve an existing Library's roots, but only one the scope can see —
		// the same visibility gate scanLibrary applies before touching
		// Library-scoped state.
		visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
		if err != nil {
			return ScanEstimateInfo{}, err
		}
		if !visible {
			return ScanEstimateInfo{}, clientError("unknown library")
		}
		persisted, err := h.store.LibraryRoots(ctx, store.LibraryID(libraryID))
		if err != nil {
			return ScanEstimateInfo{}, err
		}
		roots = persisted
	default:
		return ScanEstimateInfo{}, clientError("root or libraryID is required")
	}
	est, err := scan.EstimateRoots(ctx, roots)
	if err != nil {
		return ScanEstimateInfo{}, err
	}
	work := make([]DeferrableWorkInfo, 0, len(est.DeferrableWork))
	for _, w := range est.DeferrableWork {
		work = append(work, DeferrableWorkInfo{
			Kind:              w.Kind,
			EstimatedCount:    w.EstimatedCount,
			PlayabilityGating: w.PlayabilityGating,
		})
	}
	return ScanEstimateInfo{
		EstimatedFiles:             est.EstimatedFiles,
		EstimatedVideoFiles:        est.EstimatedVideoFiles,
		EstimatedAudioFiles:        est.EstimatedAudioFiles,
		EstimatedInlineSeconds:     est.EstimatedInlineSeconds,
		InlineWorkGatesPlayability: est.InlineWorkGatesPlayability,
		DeferrableWork:             work,
	}, nil
}

// requireProfile resolves an operator-chosen target Profile for an import run,
// returning a client error for an unknown or disabled Profile. It exists so a
// profileID override on runImport never routes imported engagement to a phantom
// or disabled history — the run writes to a real, active Profile or is refused.
func (h *Handler) requireProfile(ctx context.Context, profileID string) error {
	profile, err := h.store.Profile(ctx, store.ProfileID(profileID))
	if err != nil {
		return clientError("unknown profile")
	}
	if profile.Disabled {
		return clientError("profile is disabled")
	}
	return nil
}

func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
