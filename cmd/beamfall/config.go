package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// export-config / apply-config (ADR-0013 §5 GitOps seeding). The seedable
// surface is the database-owned operational config the Console exposes over the
// existing manager-gated GraphQL resolvers (internal/graph consoleConfig /
// setConsoleConfig / setPluginActive): the background-worker pool size and the
// per-plugin activation state. The bootstrap, file/env-shaped infra config
// (ports, data dir, ACME, relay, qBittorrent) and every secret (admin password
// hashes, keys, API tokens) are intentionally NOT part of this document — the
// resolvers never expose them, so secrets can never reach the export
// (invariant #5).
//
// Both subcommands speak the same Bearer-auth GraphQL the scan/backup commands
// use (cliGraphQL). export dumps a stable, diffable JSON document; apply reads
// such a document and applies it idempotently — it first reads the live config
// and only issues the mutations whose values differ, so re-running with the
// same input sends nothing (a true no-op). The whole document is parsed and
// validated before any mutation is sent, so partial or invalid input fails
// cleanly without half-applying.

// configDocumentVersion is the schema version of the exported document. It lets
// apply-config reject a document it does not understand rather than silently
// mis-applying a future shape.
const configDocumentVersion = 1

// configDocument is the GitOps-seedable configuration document. Field order is
// stable and plugins are sorted by id so two exports of the same server diff
// cleanly. It carries operational settings only — never secrets.
type configDocument struct {
	Version           int                   `json:"version"`
	BackgroundWorkers int                   `json:"backgroundWorkers"`
	Plugins           []configPluginDocItem `json:"plugins"`
}

// configPluginDocItem is one plugin's seedable state: its id and whether it is
// active. The display name is deliberately omitted — it is registry-derived
// presentation, not seedable input, and including it would make the document
// churn on locale/registry changes that apply cannot act on.
type configPluginDocItem struct {
	ID     string `json:"id"`
	Active bool   `json:"active"`
}

// consoleConfigWire mirrors the consoleConfig GraphQL resolver's wire shape for
// the fields apply/export act on. Only id + active are read from plugins.
type consoleConfigWire struct {
	BackgroundWorkers int `json:"backgroundWorkers"`
	Plugins           []struct {
		ID     string `json:"id"`
		Active bool   `json:"active"`
	} `json:"plugins"`
}

const consoleConfigQuery = "query ConsoleConfig { consoleConfig { backgroundWorkers plugins { id active } } }"

// fetchConsoleConfig reads the live seedable config over the manager-gated
// consoleConfig resolver and returns it as a stable document (plugins sorted by
// id).
func fetchConsoleConfig(ctx context.Context, opts cliHTTPOptions) (configDocument, error) {
	var resp struct {
		ConsoleConfig consoleConfigWire `json:"consoleConfig"`
	}
	if err := cliGraphQL(ctx, opts, consoleConfigQuery, map[string]any{}, &resp); err != nil {
		return configDocument{}, err
	}
	doc := configDocument{
		Version:           configDocumentVersion,
		BackgroundWorkers: resp.ConsoleConfig.BackgroundWorkers,
		Plugins:           make([]configPluginDocItem, 0, len(resp.ConsoleConfig.Plugins)),
	}
	for _, p := range resp.ConsoleConfig.Plugins {
		doc.Plugins = append(doc.Plugins, configPluginDocItem{ID: p.ID, Active: p.Active})
	}
	sort.Slice(doc.Plugins, func(i, j int) bool { return doc.Plugins[i].ID < doc.Plugins[j].ID })
	return doc, nil
}

// exportConfig dumps the server's seedable configuration as a stable, diffable
// JSON document to stdout.
func exportConfig(ctx context.Context, args []string, stdout io.Writer) error {
	var opts cliHTTPOptions
	fs := commandFlagSet("beamfall export-config", stdout)
	addHTTPFlags(fs, &opts)
	if helped, err := parseCommandFlags(fs, args); err != nil || helped {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	doc, err := fetchConsoleConfig(ctx, opts)
	if err != nil {
		return err
	}
	if stdout == nil {
		return nil
	}
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\n", encoded)
	return err
}

// applyConfig reads a configuration document (from --file or stdin) and applies
// it idempotently. It validates the whole document first, then reads the live
// config and issues only the mutations whose values differ, so re-running with
// the same input is a no-op. A partial or invalid document fails before any
// mutation is sent.
func applyConfig(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	var opts cliHTTPOptions
	var file string
	fs := commandFlagSet("beamfall apply-config", stdout)
	addHTTPFlags(fs, &opts)
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

	// Read the live config once so apply can diff and stay idempotent.
	current, err := fetchConsoleConfig(ctx, opts)
	if err != nil {
		return err
	}
	currentPlugins := make(map[string]bool, len(current.Plugins))
	currentPluginExists := make(map[string]bool, len(current.Plugins))
	for _, p := range current.Plugins {
		currentPlugins[p.ID] = p.Active
		currentPluginExists[p.ID] = true
	}

	// Validate the whole document against the live server before any write: a
	// plugin the server does not know is a hard error so apply never half-applies
	// (the server would reject setPluginActive mid-batch otherwise).
	for _, p := range desired.Plugins {
		if !currentPluginExists[p.ID] {
			return fmt.Errorf("unknown plugin %q: not loaded on this server", p.ID)
		}
	}

	var changed int
	if desired.BackgroundWorkers != current.BackgroundWorkers {
		if err := cliGraphQL(ctx, opts,
			"mutation SetConsoleConfig($backgroundWorkers: Int!) { setConsoleConfig(backgroundWorkers: $backgroundWorkers) { backgroundWorkers } }",
			map[string]any{"backgroundWorkers": desired.BackgroundWorkers},
			nil,
		); err != nil {
			return err
		}
		changed++
	}
	for _, p := range desired.Plugins {
		if currentPlugins[p.ID] == p.Active {
			continue
		}
		if err := cliGraphQL(ctx, opts,
			"mutation SetPluginActive($pluginID: String!, $active: Boolean!) { setPluginActive(pluginID: $pluginID, active: $active) { backgroundWorkers } }",
			map[string]any{"pluginID": p.ID, "active": p.Active},
			nil,
		); err != nil {
			return err
		}
		changed++
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
	return doc, nil
}
