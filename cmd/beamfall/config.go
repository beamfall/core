package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// export-config / apply-config (ADR-0013 §5 GitOps seeding). The seedable
// surface is the database-owned operational config the Console exposes: the
// background-worker pool size, the per-plugin activation state and the
// Libraries (title, plugin and scan roots). The bootstrap, file/env-shaped infra
// config (ports, data dir, ACME, relay, qBittorrent) and every secret (admin
// password hashes, keys, API tokens) are intentionally NOT part of this
// document, so secrets can never reach the export (invariant #5).
//
// Both subcommands run in one of two modes:
//
//   - online (default): the Bearer-auth GraphQL the scan/backup commands use
//     (cliGraphQL), against the admin-gated consoleConfig / setConsoleConfig /
//     setPluginActive / libraries / createLibrary / setLibraryRoots resolvers;
//   - offline (--data-dir): directly against the data directory while the
//     server is stopped, like `token create` and `reset-admin-password`. This is
//     the scripted-install path: filesystem access to the data directory is the
//     authority, so no admin session is needed.
//
// export dumps a stable, diffable JSON document; apply reads such a document
// and applies it idempotently — it first reads the live state and only issues
// the writes whose values differ, so re-running with the same input writes
// nothing (a true no-op). Libraries are additive: apply creates a missing
// Library and adds missing roots, and never removes a Library or a root. The
// whole document is parsed and validated before any write, so partial or
// invalid input fails cleanly without half-applying.

// configDocumentVersion is the schema version of the exported document. It lets
// apply-config reject a document it does not understand rather than silently
// mis-applying a future shape. The optional `libraries` list is an additive
// field of version 1: every document written before it is still valid.
const configDocumentVersion = 1

// configDocument is the GitOps-seedable configuration document. Field order is
// stable, plugins are sorted by id and libraries by title then plugin, so two
// exports of the same server diff cleanly. It carries operational settings
// only — never secrets.
type configDocument struct {
	Version           int                    `json:"version"`
	BackgroundWorkers int                    `json:"backgroundWorkers"`
	Plugins           []configPluginDocItem  `json:"plugins"`
	Libraries         []configLibraryDocItem `json:"libraries,omitempty"`
}

// configPluginDocItem is one plugin's seedable state: its id and whether it is
// active. The display name is deliberately omitted — it is registry-derived
// presentation, not seedable input, and including it would make the document
// churn on locale/registry changes that apply cannot act on.
type configPluginDocItem struct {
	ID     string `json:"id"`
	Active bool   `json:"active"`
}

// configLibraryDocItem is one Library: its title, the plugin it is bound to and
// its scan roots (absolute, clean, sorted). A Library is identified by title
// (case-sensitive) plus plugin; the server-derived Library ID is not part of
// the document. Restricted Libraries are never exported (their existence is
// Reveal-gated), but apply still matches against them so it never duplicates
// one.
type configLibraryDocItem struct {
	Title  string   `json:"title"`
	Plugin string   `json:"plugin"`
	Roots  []string `json:"roots"`
}

func (l configLibraryDocItem) key() configLibraryKey {
	return configLibraryKey{title: l.Title, plugin: l.Plugin}
}

type configLibraryKey struct{ title, plugin string }

// configLiveLibrary is a Library as the backend reports it: the document shape
// plus the ID apply needs to add roots, and whether it is restricted.
type configLiveLibrary struct {
	configLibraryDocItem
	ID         string
	Restricted bool
}

// configSnapshot is the live seedable state read from a backend.
type configSnapshot struct {
	BackgroundWorkers int
	Plugins           []configPluginDocItem
	Libraries         []configLiveLibrary
}

// document renders the snapshot as a stable export document.
func (s configSnapshot) document() configDocument {
	doc := configDocument{
		Version:           configDocumentVersion,
		BackgroundWorkers: s.BackgroundWorkers,
		Plugins:           append([]configPluginDocItem{}, s.Plugins...),
	}
	sort.Slice(doc.Plugins, func(i, j int) bool { return doc.Plugins[i].ID < doc.Plugins[j].ID })
	for _, lib := range s.Libraries {
		if lib.Restricted {
			continue
		}
		roots := append([]string{}, lib.Roots...)
		sort.Strings(roots)
		doc.Libraries = append(doc.Libraries, configLibraryDocItem{Title: lib.Title, Plugin: lib.Plugin, Roots: roots})
	}
	sort.SliceStable(doc.Libraries, func(i, j int) bool {
		if doc.Libraries[i].Title != doc.Libraries[j].Title {
			return doc.Libraries[i].Title < doc.Libraries[j].Title
		}
		return doc.Libraries[i].Plugin < doc.Libraries[j].Plugin
	})
	return doc
}

// configBackend is where export/apply read and write: the running server
// (online) or the data directory (offline).
type configBackend interface {
	snapshot(ctx context.Context) (configSnapshot, error)
	setBackgroundWorkers(ctx context.Context, n int) error
	setPluginActive(ctx context.Context, pluginID string, active bool) error
	createLibrary(ctx context.Context, lib configLibraryDocItem) error
	setLibraryRoots(ctx context.Context, libraryID string, roots []string) error
}

// configModeFlags registers the shared mode flags and returns a function that
// opens the chosen backend (and a close func) after parsing.
func configModeFlags(fs *flag.FlagSet) func(ctx context.Context) (configBackend, func(), error) {
	var opts cliHTTPOptions
	var dataDir string
	addHTTPFlags(fs, &opts)
	fs.StringVar(&dataDir, "data-dir", "", "work offline against this data directory (server must be stopped) instead of --server")
	return func(ctx context.Context) (configBackend, func(), error) {
		if dataDir == "" {
			return httpConfigBackend{opts: opts}, func() {}, nil
		}
		if err := refuseServerLock(dataDir); err != nil {
			return nil, nil, err
		}
		st, err := store.Open(ctx, dataDir, store.Options{})
		if err != nil {
			return nil, nil, err
		}
		return &storeConfigBackend{st: st, bundles: firstPartyBundles()}, func() { _ = st.Close() }, nil
	}
}

const configUsageHeader = `The config document is JSON:

  {
    "version": 1,
    "backgroundWorkers": 2,
    "plugins": [{"id": "music", "active": true}],
    "libraries": [{"title": "Music", "plugin": "music", "roots": ["/media/music"]}]
  }

"libraries" is optional. apply-config creates a missing Library (matched by
title, case-sensitive, plus plugin) and adds missing roots to an existing one;
it never removes a Library or a root. An empty "plugin" is a pluginless
Library. Roots must be absolute, clean paths.
Re-applying the same document changes nothing.

Online mode (default) needs an admin-authorized --token. Offline mode
(--data-dir) writes the data directory directly and refuses to run while the
server is running.

Flags:
`

func setConfigUsage(fs *flag.FlagSet, summary string) {
	fs.Usage = func() {
		out := fs.Output()
		_, _ = fmt.Fprintf(out, "Usage: %s [flags]\n\n%s\n\n%s", fs.Name(), summary, configUsageHeader)
		fs.PrintDefaults()
	}
}

// exportConfig dumps the server's seedable configuration as a stable, diffable
// JSON document to stdout.
func exportConfig(ctx context.Context, args []string, stdout io.Writer) error {
	fs := commandFlagSet("beamfall export-config", stdout)
	setConfigUsage(fs, "Export the seedable console config (workers, plugins, libraries).")
	open := configModeFlags(fs)
	if helped, err := parseCommandFlags(fs, args); err != nil || helped {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	backend, closeBackend, err := open(ctx)
	if err != nil {
		return err
	}
	defer closeBackend()
	snap, err := backend.snapshot(ctx)
	if err != nil {
		return err
	}
	if stdout == nil {
		return nil
	}
	encoded, err := json.MarshalIndent(snap.document(), "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\n", encoded)
	return err
}

// applyConfig reads a configuration document (from --file or stdin) and applies
// it idempotently. It validates the whole document first, then reads the live
// state and issues only the writes whose values differ, so re-running with the
// same input is a no-op. A partial or invalid document fails before any write.
func applyConfig(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	var file string
	fs := commandFlagSet("beamfall apply-config", stdout)
	setConfigUsage(fs, "Apply a console config document idempotently (additive for libraries).")
	open := configModeFlags(fs)
	fs.StringVar(&file, "file", "", "configuration document to apply (default: stdin)")
	if helped, err := parseCommandFlags(fs, args); err != nil || helped {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}

	raw, err := readConfigInput(file, stdin)
	if err != nil {
		return err
	}
	desired, err := parseConfigDocument(raw)
	if err != nil {
		return err
	}
	backend, closeBackend, err := open(ctx)
	if err != nil {
		return err
	}
	defer closeBackend()
	changed, err := applyConfigDocument(ctx, backend, desired)
	if err != nil {
		return err
	}
	if stdout != nil {
		if changed == 0 {
			_, _ = fmt.Fprintln(stdout, "config already up to date; nothing to apply")
		} else {
			_, _ = fmt.Fprintf(stdout, "applied %d config change(s)\n", changed)
		}
	}
	return nil
}

// configRootsWrite is one planned root addition to an existing Library.
type configRootsWrite struct {
	libraryID string
	roots     []string
	added     int
}

// applyConfigDocument diffs desired against the backend's live state and
// writes only what differs. Every check that can fail runs before the first
// write, so an invalid document never half-applies.
func applyConfigDocument(ctx context.Context, backend configBackend, desired configDocument) (int, error) {
	current, err := backend.snapshot(ctx)
	if err != nil {
		return 0, err
	}
	currentPlugins := make(map[string]bool, len(current.Plugins))
	currentPluginExists := make(map[string]bool, len(current.Plugins))
	for _, p := range current.Plugins {
		currentPlugins[p.ID] = p.Active
		currentPluginExists[p.ID] = true
	}

	// A plugin the server does not know is a hard error so apply never
	// half-applies (the server would reject the write mid-batch otherwise).
	for _, p := range desired.Plugins {
		if !currentPluginExists[p.ID] {
			return 0, fmt.Errorf("unknown plugin %q: not loaded on this server", p.ID)
		}
	}
	live := make(map[configLibraryKey][]configLiveLibrary, len(current.Libraries))
	for _, lib := range current.Libraries {
		live[lib.key()] = append(live[lib.key()], lib)
	}
	var creates []configLibraryDocItem
	var rootWrites []configRootsWrite
	for _, lib := range desired.Libraries {
		// An empty plugin is a pluginless Library, which export-config emits and
		// the Console can create.
		if lib.Plugin != "" && !currentPluginExists[lib.Plugin] {
			return 0, fmt.Errorf("library %q: unknown plugin %q: not loaded on this server", lib.Title, lib.Plugin)
		}
		matches := live[lib.key()]
		switch len(matches) {
		case 0:
			creates = append(creates, lib)
		case 1:
			if write, ok := mergeLibraryRoots(matches[0], lib.Roots); ok {
				rootWrites = append(rootWrites, write)
			}
		default:
			return 0, fmt.Errorf("library %q (plugin %q) matches %d existing libraries; rename all but one in the Console first", lib.Title, lib.Plugin, len(matches))
		}
	}

	var changed int
	if desired.BackgroundWorkers != current.BackgroundWorkers {
		if err := backend.setBackgroundWorkers(ctx, desired.BackgroundWorkers); err != nil {
			return changed, err
		}
		changed++
	}
	for _, p := range desired.Plugins {
		if currentPlugins[p.ID] == p.Active {
			continue
		}
		if err := backend.setPluginActive(ctx, p.ID, p.Active); err != nil {
			return changed, err
		}
		changed++
	}
	for _, lib := range creates {
		if err := backend.createLibrary(ctx, lib); err != nil {
			return changed, fmt.Errorf("creating library %q: %w", lib.Title, err)
		}
		changed++
	}
	for _, write := range rootWrites {
		if err := backend.setLibraryRoots(ctx, write.libraryID, write.roots); err != nil {
			return changed, fmt.Errorf("adding roots to library %q: %w", write.libraryID, err)
		}
		changed += write.added
	}
	return changed, nil
}

// mergeLibraryRoots returns the union of a live Library's roots and the desired
// roots, and false when nothing is missing. Roots are never removed.
func mergeLibraryRoots(lib configLiveLibrary, desired []string) (configRootsWrite, bool) {
	have := make(map[string]bool, len(lib.Roots))
	union := append([]string{}, lib.Roots...)
	for _, root := range lib.Roots {
		have[root] = true
	}
	added := 0
	for _, root := range desired {
		if !have[root] {
			have[root] = true
			union = append(union, root)
			added++
		}
	}
	if added == 0 {
		return configRootsWrite{}, false
	}
	sort.Strings(union)
	return configRootsWrite{libraryID: lib.ID, roots: union, added: added}, true
}

// httpConfigBackend talks to the running server over the admin-gated GraphQL
// resolvers the Console uses.
type httpConfigBackend struct {
	opts cliHTTPOptions
}

const consoleConfigQuery = "query ConsoleConfig { consoleConfig { backgroundWorkers plugins { id active } } libraries { id title pluginID restricted roots } }"

func (b httpConfigBackend) snapshot(ctx context.Context) (configSnapshot, error) {
	var resp struct {
		ConsoleConfig struct {
			BackgroundWorkers int                   `json:"backgroundWorkers"`
			Plugins           []configPluginDocItem `json:"plugins"`
		} `json:"consoleConfig"`
		Libraries []struct {
			ID         string   `json:"id"`
			Title      string   `json:"title"`
			PluginID   *string  `json:"pluginID"`
			Restricted bool     `json:"restricted"`
			Roots      []string `json:"roots"`
		} `json:"libraries"`
	}
	if err := cliGraphQL(ctx, b.opts, consoleConfigQuery, map[string]any{}, &resp); err != nil {
		return configSnapshot{}, err
	}
	snap := configSnapshot{
		BackgroundWorkers: resp.ConsoleConfig.BackgroundWorkers,
		Plugins:           resp.ConsoleConfig.Plugins,
	}
	for _, lib := range resp.Libraries {
		item := configLiveLibrary{ID: lib.ID, Restricted: lib.Restricted}
		item.Title = lib.Title
		if lib.PluginID != nil {
			item.Plugin = *lib.PluginID
		}
		item.Roots = append([]string{}, lib.Roots...)
		snap.Libraries = append(snap.Libraries, item)
	}
	return snap, nil
}

func (b httpConfigBackend) setBackgroundWorkers(ctx context.Context, n int) error {
	return cliGraphQL(ctx, b.opts,
		"mutation SetConsoleConfig($backgroundWorkers: Int!) { setConsoleConfig(backgroundWorkers: $backgroundWorkers) { backgroundWorkers } }",
		map[string]any{"backgroundWorkers": n}, nil)
}

func (b httpConfigBackend) setPluginActive(ctx context.Context, pluginID string, active bool) error {
	return cliGraphQL(ctx, b.opts,
		"mutation SetPluginActive($pluginID: String!, $active: Boolean!) { setPluginActive(pluginID: $pluginID, active: $active) { backgroundWorkers } }",
		map[string]any{"pluginID": pluginID, "active": active}, nil)
}

func (b httpConfigBackend) createLibrary(ctx context.Context, lib configLibraryDocItem) error {
	return cliGraphQL(ctx, b.opts,
		"mutation CreateLibrary($title: String!, $pluginID: String, $roots: [String!]) { createLibrary(title: $title, pluginID: $pluginID, roots: $roots) { id } }",
		map[string]any{"title": lib.Title, "pluginID": optionalString(lib.Plugin), "roots": lib.Roots}, nil)
}

// optionalString maps "" to a GraphQL null.
func optionalString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func (b httpConfigBackend) setLibraryRoots(ctx context.Context, libraryID string, roots []string) error {
	return cliGraphQL(ctx, b.opts,
		"mutation SetLibraryRoots($libraryID: String!, $roots: [String!]!) { setLibraryRoots(libraryID: $libraryID, roots: $roots) { id } }",
		map[string]any{"libraryID": libraryID, "roots": roots}, nil)
}

// storeConfigBackend reads and writes the data directory directly (offline
// mode). Known plugins are the compiled-in first-party bundles, the same set
// the server loads; activation defaults to active when no override row exists,
// exactly as the consoleConfig resolver reports it.
type storeConfigBackend struct {
	st      *store.Store
	bundles []plugin.Bundle
}

func (b *storeConfigBackend) snapshot(ctx context.Context) (configSnapshot, error) {
	cfg, err := b.st.ConsoleConfig(ctx)
	if err != nil {
		return configSnapshot{}, err
	}
	overrides, err := b.st.PluginActivations(ctx)
	if err != nil {
		return configSnapshot{}, err
	}
	snap := configSnapshot{BackgroundWorkers: cfg.BackgroundWorkers}
	for _, bundle := range b.bundles {
		active := true
		if state, ok := overrides[string(bundle.ID)]; ok {
			active = state
		}
		snap.Plugins = append(snap.Plugins, configPluginDocItem{ID: string(bundle.ID), Active: active})
	}
	libraries, err := b.st.Libraries(ctx, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return configSnapshot{}, err
	}
	for _, lib := range libraries {
		roots, err := b.st.LibraryRoots(ctx, lib.ID)
		if err != nil {
			return configSnapshot{}, err
		}
		item := configLiveLibrary{ID: string(lib.ID), Restricted: lib.IsRestricted}
		item.Title = lib.Title
		if lib.PluginID != nil {
			item.Plugin = *lib.PluginID
		}
		item.Roots = roots
		snap.Libraries = append(snap.Libraries, item)
	}
	return snap, nil
}

func (b *storeConfigBackend) setBackgroundWorkers(ctx context.Context, n int) error {
	_, err := b.st.SetConsoleConfig(ctx, store.ConsoleConfigPatch{BackgroundWorkers: &n}, time.Now().UTC())
	return err
}

func (b *storeConfigBackend) setPluginActive(ctx context.Context, pluginID string, active bool) error {
	return b.st.SetPluginActivation(ctx, pluginID, active, time.Now().UTC())
}

// createLibrary uses the Console's creation path (CreateLibraryWithRoots): the
// plugin's restricted default applies, and the Library is granted to the owner
// Profile (else the first enabled adult Profile), as the Console grants it to
// the admin creating it. Before first-run setup there is no Profile; the
// Library is created ungranted and first-run grants it to the new owner.
func (b *storeConfigBackend) createLibrary(ctx context.Context, lib configLibraryDocItem) error {
	restricted := false
	for _, bundle := range b.bundles {
		if string(bundle.ID) == lib.Plugin && bundle.LibraryDefaults.Restricted {
			restricted = true
		}
	}
	grantTo, err := b.grantProfile(ctx)
	if err != nil {
		return err
	}
	if grantTo == "" {
		_, err = b.st.CreateUngrantedLibraryWithRoots(ctx, lib.Title, restricted, lib.Plugin, lib.Roots, "")
		return err
	}
	_, err = b.st.CreateLibraryWithRoots(ctx, lib.Title, restricted, lib.Plugin, grantTo, lib.Roots, "")
	return err
}

func (b *storeConfigBackend) grantProfile(ctx context.Context) (store.ProfileID, error) {
	profiles, err := b.st.Profiles(ctx)
	if err != nil {
		return "", err
	}
	if len(profiles) == 0 {
		return "", nil
	}
	owner, ownerErr := b.st.OwnerProfile(ctx)
	if ownerErr == nil {
		return owner.ID, nil
	}
	// No owner row (or it could not be read): fall back to an enabled adult.
	for _, p := range profiles {
		if p.Kind == store.ProfileAdult && !p.Disabled {
			return p.ID, nil
		}
	}
	return "", fmt.Errorf("no owner or enabled adult profile to grant the library to: %w", ownerErr)
}

func (b *storeConfigBackend) setLibraryRoots(ctx context.Context, libraryID string, roots []string) error {
	return b.st.SetLibraryRoots(ctx, store.LibraryID(libraryID), roots)
}

// readConfigInput reads the document body from a file path, or from stdin when
// no path is given.
func readConfigInput(file string, stdin io.Reader) ([]byte, error) {
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("reading config document: %w", err)
		}
		return data, nil
	}
	if stdin == nil {
		return nil, errors.New("no config document: provide --file or pipe one on stdin")
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return nil, fmt.Errorf("reading config document from stdin: %w", err)
	}
	return data, nil
}

// parseConfigDocument decodes and validates a configuration document. Unknown
// JSON fields, an unsupported version, a negative worker count, or a duplicate
// plugin id are hard errors so an invalid document is rejected whole rather than
// half-applied.
func parseConfigDocument(raw []byte) (configDocument, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var doc configDocument
	if err := dec.Decode(&doc); err != nil {
		return configDocument{}, fmt.Errorf("invalid config document: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return configDocument{}, errors.New("invalid config document: multiple JSON values")
		}
		return configDocument{}, fmt.Errorf("invalid config document: %w", err)
	}
	if doc.Version != configDocumentVersion {
		return configDocument{}, fmt.Errorf("unsupported config document version %d (expected %d)", doc.Version, configDocumentVersion)
	}
	if doc.BackgroundWorkers < 1 {
		return configDocument{}, fmt.Errorf("backgroundWorkers must be at least 1, got %d", doc.BackgroundWorkers)
	}
	seen := make(map[string]bool, len(doc.Plugins))
	for _, p := range doc.Plugins {
		if p.ID == "" {
			return configDocument{}, errors.New("plugin id must not be empty")
		}
		if seen[p.ID] {
			return configDocument{}, fmt.Errorf("duplicate plugin id %q in config document", p.ID)
		}
		seen[p.ID] = true
	}
	if err := validateConfigLibraries(doc.Libraries); err != nil {
		return configDocument{}, err
	}
	return doc, nil
}

// validateConfigLibraries rejects a library entry without a title or plugin, a
// root that is not an absolute clean path, a duplicate root, and a duplicate
// title+plugin entry. Roots are sorted in place so apply diffs stably.
func validateConfigLibraries(libraries []configLibraryDocItem) error {
	seen := make(map[configLibraryKey]bool, len(libraries))
	for i := range libraries {
		lib := &libraries[i]
		if strings.TrimSpace(lib.Title) == "" || lib.Title != strings.TrimSpace(lib.Title) {
			return fmt.Errorf("library %d: title must be non-empty with no surrounding whitespace", i)
		}
		if seen[lib.key()] {
			return fmt.Errorf("duplicate library %q (plugin %q) in config document", lib.Title, lib.Plugin)
		}
		seen[lib.key()] = true
		roots := make(map[string]bool, len(lib.Roots))
		for _, root := range lib.Roots {
			if !filepath.IsAbs(root) || filepath.Clean(root) != root || strings.TrimSpace(root) != root {
				return fmt.Errorf("library %q: root %q must be an absolute, clean path with no surrounding whitespace", lib.Title, root)
			}
			if roots[root] {
				return fmt.Errorf("library %q: duplicate root %q", lib.Title, root)
			}
			roots[root] = true
		}
		if lib.Roots == nil {
			lib.Roots = []string{}
		}
		sort.Strings(lib.Roots)
	}
	return nil
}
