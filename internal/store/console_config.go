package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Console-owned runtime config. The bootstrap config
// (internal/config) reads file/env-shaped infra settings at process start;
// this is the operational state the Console reads and writes at runtime, held
// in the catalog database. Two config domains share one key-value table
// (server_config): the console resource governor and the hardware-transcode
// policy. Each domain has a typed read/patch pair below; the closed key
// vocabulary is validated on every write, so an unknown key can never reach
// the table.

// server_config keys. The vocabulary is closed: only these keys are ever
// written, and SetServerConfig rejects anything else.
const (
	// console resource governance.
	configBackgroundWorkers = "console.background_workers"
	// events.db retention compaction: operator-owned runtime window.
	configEventsRetentionDays = "events.retention_days"
	// hardware transcode policy.
	configTranscodeHardwareEnabled = "transcode.hardware_enabled"
	configTranscodeCodec           = "transcode.codec"
	configTranscodeQuality         = "transcode.quality"
	configTranscodeDevice          = "transcode.device"
	// opt-in update awareness. Default OFF: the version check is
	// inert until the operator opts in (in the setup wizard's final step). The
	// channel selects which release manifest to read.
	configUpdateCheckEnabled = "updatecheck.enabled"
	configUpdateCheckChannel = "updatecheck.channel"
	// opt-in local observability. Default OFF: the OTel exporter is
	// not constructed until the operator flips this runtime switch.
	configObservabilityEnabled = "observability.enabled"
	// qBittorrent ingest connection + routing. The
	// Console's setQBittorrentIngest mutation persists the endpoint URL, the
	// optional username, and the category->Library routing map (JSON) here. The
	// PASSWORD is NEVER stored here — it lives in the secure store / a 0600
	// secret file (invariant #5), resolved by the composition root.
	configQBittorrentURL      = "qbittorrent.url"
	configQBittorrentUsername = "qbittorrent.username"
	configQBittorrentRouting  = "qbittorrent.routing"
	// opt-in Enrichment / semantic search (ADR-0041 §2.5 opt-in). Default OFF:
	// the first-party CPU EmbeddingProvider is inert until the operator opts in,
	// so operators who do not want AI features incur no indexing overhead. When
	// off, no embeddings are written and semantic search reports unavailable.
	configEnrichmentEnabled = "enrichment.enabled"
	// remote enrollment toggle. Default OFF (no override → disabled):
	// the enroll redeem path refuses off-LAN pairing-code enrollments until the
	// operator explicitly enables them.
	configRemoteEnrollmentEnabled = "enrollment.remote_enabled"
)

// capabilityConfigPrefix namespaces the runtime Workflow-capability enabled
// overrides. A `capability.<id>.enabled` row is the operator's
// Console/Manager toggle for one Workflow capability; absent means "use the
// capability's compiled-in default". The id vocabulary is validated by the
// caller against the loaded capability registry; the store validates only the
// key slug shape so a malformed id can never reach the table.
const capabilityConfigPrefix = "capability."

const capabilityConfigSuffix = ".enabled"

func capabilityConfigKey(id string) (string, error) {
	if !pluginConfigKeyPartPattern.MatchString(id) {
		return "", ValidationError("capability id must be a lowercase slug")
	}
	return capabilityConfigPrefix + id + capabilityConfigSuffix, nil
}

// ConsoleConfig is the typed console resource-governance config
// plus the opt-in update-awareness settings. Defaults apply when
// no override row exists.
type ConsoleConfig struct {
	// BackgroundWorkers is the background-generation worker-pool size. 0 means
	// "use the server default" (it is never persisted as 0).
	BackgroundWorkers int
	// UpdateCheckEnabled gates the opt-in version check. Default
	// OFF: the check makes NO outbound request until the operator opts in. There
	// is no telemetry — the request only READS the latest published version
	// (invariant #5).
	UpdateCheckEnabled bool
	// UpdateCheckChannel selects which release channel's version manifest the
	// check reads: "stable" (the default) or "beta". Closed vocabulary.
	UpdateCheckChannel string
	// RemoteEnrollmentEnabled gates pairing-code enrollment from off-LAN clients.
	// Default OFF: a missing override refuses remote enrollment.
	// When ON, off-LAN enrollments are accepted.
	RemoteEnrollmentEnabled bool
	// EventsRetentionDays is the age cutoff for events.db compaction. Defaults to
	// the server bootstrap value, falling back to DefaultEventsRetention.
	EventsRetentionDays int
	// ObservabilityEnabled gates ADR-0146's OTel metrics layer. Default OFF:
	// coarse ADR-0013 /metrics remains, but no OTel exporter runs.
	ObservabilityEnabled bool
}

// defaultBackgroundWorkers is the low-power default for the resource governor
// (ADR-0013 §2 — sane on weak hardware; the operator raises it).
const defaultBackgroundWorkers = 1

// defaultUpdateCheckChannel is the release channel the version check reads when
// the operator has not chosen one.
const defaultUpdateCheckChannel = "stable"

// updateCheckChannels is the closed channel vocabulary the server validates on
// write — a free-text channel is rejected.
var updateCheckChannels = map[string]bool{"stable": true, "beta": true}

// ConsoleConfigPatch is a partial console-config write: a nil field leaves the
// stored value unchanged. The graph layer builds it from the present variables.
type ConsoleConfigPatch struct {
	BackgroundWorkers       *int
	UpdateCheckEnabled      *bool
	UpdateCheckChannel      *string
	RemoteEnrollmentEnabled *bool
	EventsRetentionDays     *int
	ObservabilityEnabled    *bool
}

// TranscodeConfig is the typed hardware-transcode policy. The
// vocabulary is neutral and domain-agnostic.
type TranscodeConfig struct {
	// HardwareEnabled gates hardware-accelerated transcode sessions; when off,
	// transcode runs in software only.
	HardwareEnabled bool
	// Codec is the output video codec policy ("h264", "hevc"). Empty means the
	// server default.
	Codec string
	// Quality is the output quality policy ("low", "balanced", "high"). Empty
	// means the server default.
	Quality string
	// Device is the operator-selected acceleration device hint (e.g. a
	// /dev/dri render node or an index). Empty means automatic selection.
	Device string
}

// TranscodeConfigPatch is a partial transcode-config write: a nil field leaves
// the stored value unchanged.
type TranscodeConfigPatch struct {
	HardwareEnabled *bool
	Codec           *string
	Quality         *string
	Device          *string
}

// transcodeCodecs and transcodeQualities are the closed value vocabularies the
// server validates on write — a faith-based free-text dropdown is rejected.
var transcodeCodecs = map[string]bool{"h264": true, "hevc": true}
var transcodeQualities = map[string]bool{"low": true, "balanced": true, "high": true}

// ConsoleConfig reads the console resource-governance config, applying defaults
// for any absent key.
func (s *Store) ConsoleConfig(ctx context.Context) (ConsoleConfig, error) {
	return s.ConsoleConfigWithDefaults(ctx, ConsoleConfig{BackgroundWorkers: defaultBackgroundWorkers})
}

// ConsoleConfigWithDefaults reads the Console resource-governance config using
// the supplied defaults for keys that have not yet been persisted. The app uses
// this to let bootstrap config seed worker sizing before the Console writes its
// DB-owned operational value; Console/API reads use ConsoleConfig's low-power
// defaults.
func (s *Store) ConsoleConfigWithDefaults(ctx context.Context, defaults ConsoleConfig) (ConsoleConfig, error) {
	values, err := s.serverConfigValues(ctx)
	if err != nil {
		return ConsoleConfig{}, err
	}
	cfg := ConsoleConfig{
		BackgroundWorkers:       defaults.BackgroundWorkers,
		UpdateCheckChannel:      defaultUpdateCheckChannel,
		RemoteEnrollmentEnabled: false,
		EventsRetentionDays:     defaults.EventsRetentionDays,
	}
	if cfg.BackgroundWorkers < 1 {
		cfg.BackgroundWorkers = defaultBackgroundWorkers
	}
	if cfg.EventsRetentionDays < 1 {
		cfg.EventsRetentionDays = int(DefaultEventsRetention / (24 * time.Hour))
	}
	if raw, ok := values[configBackgroundWorkers]; ok {
		if n, convErr := strconv.Atoi(raw); convErr == nil {
			cfg.BackgroundWorkers = n
		}
	}
	if raw, ok := values[configEventsRetentionDays]; ok {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			cfg.EventsRetentionDays = n
		}
	}
	if raw, ok := values[configUpdateCheckEnabled]; ok {
		cfg.UpdateCheckEnabled = raw == "1"
	}
	if raw, ok := values[configUpdateCheckChannel]; ok && updateCheckChannels[raw] {
		cfg.UpdateCheckChannel = raw
	}
	if raw, ok := values[configRemoteEnrollmentEnabled]; ok {
		cfg.RemoteEnrollmentEnabled = raw == "1"
	}
	if raw, ok := values[configObservabilityEnabled]; ok {
		cfg.ObservabilityEnabled = raw == "1"
	}
	return cfg, nil
}

// SetConsoleConfig applies a partial console-config write, then returns the
// resolved config. An out-of-range value is a ValidationError (client-safe).
func (s *Store) SetConsoleConfig(ctx context.Context, patch ConsoleConfigPatch, now time.Time) (ConsoleConfig, error) {
	writes := map[string]string{}
	if patch.BackgroundWorkers != nil {
		if *patch.BackgroundWorkers < 1 {
			return ConsoleConfig{}, ValidationError("backgroundWorkers must be at least 1")
		}
		writes[configBackgroundWorkers] = strconv.Itoa(*patch.BackgroundWorkers)
	}
	if patch.EventsRetentionDays != nil {
		if *patch.EventsRetentionDays < 1 {
			return ConsoleConfig{}, ValidationError("eventsRetentionDays must be at least 1")
		}
		writes[configEventsRetentionDays] = strconv.Itoa(*patch.EventsRetentionDays)
	}
	if patch.UpdateCheckEnabled != nil {
		writes[configUpdateCheckEnabled] = boolToConfig(*patch.UpdateCheckEnabled)
	}
	if patch.RemoteEnrollmentEnabled != nil {
		writes[configRemoteEnrollmentEnabled] = boolToConfig(*patch.RemoteEnrollmentEnabled)
	}
	if patch.ObservabilityEnabled != nil {
		writes[configObservabilityEnabled] = boolToConfig(*patch.ObservabilityEnabled)
	}
	if patch.UpdateCheckChannel != nil {
		if !updateCheckChannels[*patch.UpdateCheckChannel] {
			return ConsoleConfig{}, ValidationError("channel must be one of stable, beta")
		}
		writes[configUpdateCheckChannel] = *patch.UpdateCheckChannel
	}
	if err := s.setServerConfigValues(ctx, writes, now); err != nil {
		return ConsoleConfig{}, err
	}
	return s.ConsoleConfig(ctx)
}

// TranscodeConfig reads the hardware-transcode policy, applying defaults for
// any absent key. HardwareEnabled defaults off — opt-in after the Console
// probes what works.
func (s *Store) TranscodeConfig(ctx context.Context) (TranscodeConfig, error) {
	values, err := s.serverConfigValues(ctx)
	if err != nil {
		return TranscodeConfig{}, err
	}
	cfg := TranscodeConfig{}
	if raw, ok := values[configTranscodeHardwareEnabled]; ok {
		cfg.HardwareEnabled = raw == "1"
	}
	cfg.Codec = values[configTranscodeCodec]
	cfg.Quality = values[configTranscodeQuality]
	cfg.Device = values[configTranscodeDevice]
	return cfg, nil
}

// SetTranscodeConfig applies a partial transcode-config write, then returns the
// resolved policy. A codec or quality outside the closed vocabulary is a
// ValidationError. An empty Codec/Quality/Device clears the override back to the
// server default.
func (s *Store) SetTranscodeConfig(ctx context.Context, patch TranscodeConfigPatch, now time.Time) (TranscodeConfig, error) {
	writes := map[string]string{}
	clears := []string{}
	if patch.HardwareEnabled != nil {
		writes[configTranscodeHardwareEnabled] = boolToConfig(*patch.HardwareEnabled)
	}
	if patch.Codec != nil {
		if *patch.Codec == "" {
			clears = append(clears, configTranscodeCodec)
		} else if !transcodeCodecs[*patch.Codec] {
			return TranscodeConfig{}, ValidationError("codec must be one of h264, hevc")
		} else {
			writes[configTranscodeCodec] = *patch.Codec
		}
	}
	if patch.Quality != nil {
		if *patch.Quality == "" {
			clears = append(clears, configTranscodeQuality)
		} else if !transcodeQualities[*patch.Quality] {
			return TranscodeConfig{}, ValidationError("quality must be one of low, balanced, high")
		} else {
			writes[configTranscodeQuality] = *patch.Quality
		}
	}
	if patch.Device != nil {
		if *patch.Device == "" {
			clears = append(clears, configTranscodeDevice)
		} else {
			writes[configTranscodeDevice] = *patch.Device
		}
	}
	if err := s.setServerConfigValues(ctx, writes, now); err != nil {
		return TranscodeConfig{}, err
	}
	if err := s.clearServerConfigValues(ctx, clears); err != nil {
		return TranscodeConfig{}, err
	}
	return s.TranscodeConfig(ctx)
}

// QBittorrentRouteConfig is one persisted category->Library mapping (ADR-0020
// §3, FEAT-10). It carries no credential.
type QBittorrentRouteConfig struct {
	Category  string `json:"category"`
	LibraryID string `json:"libraryID"`
}

// QBittorrentConfig is the persisted qBittorrent ingest connection + routing.
// It carries NO password: the secret lives in the
// secure store (invariant #5), never in this table. Configured reports whether
// an endpoint URL has been set so callers can tell "never configured" from
// "configured with no routes".
type QBittorrentConfig struct {
	Configured bool
	URL        string
	Username   string
	Routes     []QBittorrentRouteConfig
}

// QBittorrentConfigPatch is a partial write: a nil field leaves the stored
// value unchanged. Password is intentionally absent — the secure store owns it.
type QBittorrentConfigPatch struct {
	URL      *string
	Username *string
	Routes   *[]QBittorrentRouteConfig
}

// QBittorrentConfig reads the persisted ingest connection + routing. Configured
// is false when no URL has been written (a fresh server with no ingest set up).
func (s *Store) QBittorrentConfig(ctx context.Context) (QBittorrentConfig, error) {
	values, err := s.serverConfigValues(ctx)
	if err != nil {
		return QBittorrentConfig{}, err
	}
	cfg := QBittorrentConfig{Routes: []QBittorrentRouteConfig{}}
	if raw, ok := values[configQBittorrentURL]; ok && raw != "" {
		cfg.Configured = true
		cfg.URL = raw
	}
	cfg.Username = values[configQBittorrentUsername]
	if raw, ok := values[configQBittorrentRouting]; ok && raw != "" {
		var routes []QBittorrentRouteConfig
		if jsonErr := json.Unmarshal([]byte(raw), &routes); jsonErr == nil && routes != nil {
			cfg.Routes = routes
		}
	}
	return cfg, nil
}

// SetQBittorrentConfig applies a partial write, then returns the resolved
// config. The URL must be a non-empty http/https endpoint; routing keys
// (category) and values (libraryID) must be non-empty. An invalid value is a
// ValidationError (client-safe). The password is NOT a field here — the caller
// writes it to the secure store before persisting routing (invariant #5).
func (s *Store) SetQBittorrentConfig(ctx context.Context, patch QBittorrentConfigPatch, now time.Time) (QBittorrentConfig, error) {
	writes := map[string]string{}
	if patch.URL != nil {
		url := strings.TrimSpace(*patch.URL)
		if url == "" {
			return QBittorrentConfig{}, ValidationError("qBittorrent URL must not be empty")
		}
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			return QBittorrentConfig{}, ValidationError("qBittorrent URL must be an http:// or https:// endpoint")
		}
		writes[configQBittorrentURL] = url
	}
	if patch.Username != nil {
		writes[configQBittorrentUsername] = strings.TrimSpace(*patch.Username)
	}
	if patch.Routes != nil {
		routes := *patch.Routes
		seen := map[string]bool{}
		for _, route := range routes {
			category := strings.TrimSpace(route.Category)
			libraryID := strings.TrimSpace(route.LibraryID)
			if category == "" {
				return QBittorrentConfig{}, ValidationError("routing category must not be empty")
			}
			if libraryID == "" {
				return QBittorrentConfig{}, ValidationError("routing libraryID must not be empty")
			}
			if seen[category] {
				return QBittorrentConfig{}, ValidationError("routing category " + category + " is mapped more than once")
			}
			seen[category] = true
		}
		encoded, err := json.Marshal(routes)
		if err != nil {
			return QBittorrentConfig{}, err
		}
		writes[configQBittorrentRouting] = string(encoded)
	}
	if err := s.setServerConfigValues(ctx, writes, now); err != nil {
		return QBittorrentConfig{}, err
	}
	return s.QBittorrentConfig(ctx)
}

func boolToConfig(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// CapabilityStates reads every Workflow-capability enabled override (ADR-0124
// §5) into a map of capability id -> enabled. A capability absent from the map
// has no override and uses its compiled-in default. Probe-derived capabilities
// never have a row here — they are auto-detected, never operator-flipped.
func (s *Store) CapabilityStates(ctx context.Context) (map[string]bool, error) {
	values, err := s.serverConfigValues(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for key, value := range values {
		rest, ok := strings.CutPrefix(key, capabilityConfigPrefix)
		if !ok {
			continue
		}
		id, ok := strings.CutSuffix(rest, capabilityConfigSuffix)
		if !ok || id == "" {
			continue
		}
		out[id] = value == "1"
	}
	return out, nil
}

// SetCapabilityEnabled records the operator's Console/Manager enabled override
// for one Workflow capability. Membership validation (is this a
// real, operator-flippable Workflow capability?) belongs to the caller, which
// holds the capability registry; the store persists only the override under the
// closed `capability.<id>.enabled` key.
func (s *Store) SetCapabilityEnabled(ctx context.Context, id string, enabled bool, now time.Time) error {
	key, err := capabilityConfigKey(id)
	if err != nil {
		return err
	}
	return s.setServerConfigValues(ctx, map[string]string{key: boolToConfig(enabled)}, now)
}

const pluginConfigPrefix = "plugin."

var pluginConfigKeyPartPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func pluginConfigKey(pluginID, name string) (string, error) {
	if !pluginConfigKeyPartPattern.MatchString(pluginID) {
		return "", ValidationError("pluginID must be a lowercase slug")
	}
	if !pluginConfigKeyPartPattern.MatchString(name) {
		return "", ValidationError("plugin config key must be a lowercase slug")
	}
	return pluginConfigPrefix + pluginID + "." + name, nil
}

// PluginConfigValues reads the persisted non-secret config values for one
// plugin. The caller owns validating pluginID against the loaded registry.
func (s *Store) PluginConfigValues(ctx context.Context, pluginID string) (map[string]string, error) {
	if !pluginConfigKeyPartPattern.MatchString(pluginID) {
		return nil, ValidationError("pluginID must be a lowercase slug")
	}
	values, err := s.serverConfigValues(ctx)
	if err != nil {
		return nil, err
	}
	prefix := pluginConfigPrefix + pluginID + "."
	out := map[string]string{}
	for key, value := range values {
		if name, ok := strings.CutPrefix(key, prefix); ok {
			out[name] = value
		}
	}
	return out, nil
}

// SetPluginConfigValues persists non-secret plugin configuration under the
// existing server_config key-value table. Secrets deliberately do not flow
// through this helper.
func (s *Store) SetPluginConfigValues(ctx context.Context, pluginID string, values map[string]string, clears []string, now time.Time) error {
	writes := map[string]string{}
	for name, value := range values {
		key, err := pluginConfigKey(pluginID, name)
		if err != nil {
			return err
		}
		writes[key] = value
	}
	clearKeys := make([]string, 0, len(clears))
	for _, name := range clears {
		key, err := pluginConfigKey(pluginID, name)
		if err != nil {
			return err
		}
		clearKeys = append(clearKeys, key)
	}
	if err := s.setServerConfigValues(ctx, writes, now); err != nil {
		return err
	}
	return s.clearServerConfigValues(ctx, clearKeys)
}

// serverConfigValues reads every server_config row into a map.
func (s *Store) serverConfigValues(ctx context.Context) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `SELECT key, value FROM server_config`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		values[key] = value
	}
	return values, rows.Err()
}

// setServerConfigValues upserts the given key->value writes in one transaction.
// An empty map is a no-op.
func (s *Store) setServerConfigValues(ctx context.Context, writes map[string]string, now time.Time) error {
	if len(writes) == 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Deterministic write order keeps tests and logs stable.
	keys := make([]string, 0, len(writes))
	for key := range writes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO server_config(key, value, updated_at_ms)
			VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET
				value = excluded.value,
				updated_at_ms = excluded.updated_at_ms`,
			key, writes[key], timeToMillis(now)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// clearServerConfigValues deletes the given keys (reverting them to the server
// default). An empty slice is a no-op.
func (s *Store) clearServerConfigValues(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `DELETE FROM server_config WHERE key = ?`, key); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PluginActivation reports whether a plugin is active. A plugin with no row is
// active by default (loading a bundle historically activated it); a row with
// active = 0 deactivates it. The caller resolves which plugins exist from the
// registry; this only reports the Console's override.
func (s *Store) PluginActivation(ctx context.Context, pluginID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	var active int
	err := s.catalogR.QueryRowContext(ctx,
		`SELECT active FROM plugin_activation WHERE plugin_id = ?`, pluginID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return active != 0, nil
}

// EnrichmentEnabled reports the opt-in Enrichment / semantic-search flag.
// Default OFF — an absent row, or any value other than "1",
// means the operator has not opted in, so the first-party EmbeddingProvider
// stays inert and writes no embeddings.
func (s *Store) EnrichmentEnabled(ctx context.Context) (bool, error) {
	values, err := s.serverConfigValues(ctx)
	if err != nil {
		return false, err
	}
	return values[configEnrichmentEnabled] == "1", nil
}

// SetEnrichmentEnabled flips the opt-in Enrichment flag. Turning
// it off does NOT delete already-written embeddings (they are Core-owned, harmless
// at rest); it only stops new indexing and makes semantic search report
// unavailable on the next provider availability check.
func (s *Store) SetEnrichmentEnabled(ctx context.Context, enabled bool, now time.Time) error {
	return s.setServerConfigValues(ctx, map[string]string{
		configEnrichmentEnabled: boolToConfig(enabled),
	}, now)
}

// PluginActivations reads every activation override row into a map. Plugins
// absent from the map are active by default.
func (s *Store) PluginActivations(ctx context.Context) (map[string]bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `SELECT plugin_id, active FROM plugin_activation`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		var active int
		if err := rows.Scan(&id, &active); err != nil {
			return nil, err
		}
		out[id] = active != 0
	}
	return out, rows.Err()
}

// SetPluginActivation records the Console's active/inactive override for one
// plugin. Membership validation (is this a real plugin?) belongs to the caller,
// which holds the registry; the store only persists the override.
func (s *Store) SetPluginActivation(ctx context.Context, pluginID string, active bool, now time.Time) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO plugin_activation(plugin_id, active, updated_at_ms)
		VALUES (?, ?, ?)
		ON CONFLICT(plugin_id) DO UPDATE SET
			active = excluded.active,
			updated_at_ms = excluded.updated_at_ms`,
		pluginID, boolInt(active), timeToMillis(now))
	return err
}
