package graph

import (
	"context"
	"sort"
	"strings"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// qBittorrent ingest, graph half. This field is a manager-gated
// operator-Console read of the qBittorrent ingest config + status feed: whether
// ingest is configured, the explicit category→Library routing map (no
// inference — ADR-0020 §3), and the recent per-torrent status feed from the
// plugin's private ledger. It exposes no credential: the qBittorrent URL is
// operator config, the password lives in a 0600 secret file and never reaches
// this surface, and each status row carries only a secret-free Detail.

// QBittorrentIngestProvider is the consumer-defined seam the app
// wires the qbittorrent package into. Nil (the default) means ingest is honestly
// unconfigured: the resolver reports disabled rather than guessing.
type QBittorrentIngestProvider interface {
	// Config reports whether ingest is configured and the category→Library
	// routing map (ADR-0020 §3 — explicit routing only). ok is false when no
	// qBittorrent endpoint is configured.
	Config(ctx context.Context) (config QBittorrentConfig, ok bool)
	// Status returns the recent per-torrent ingest status feed, newest first.
	Status(ctx context.Context) []QBittorrentStatusEntry
}

// QBittorrentConfig is the operator-Console view of the ingest config. It
// carries no credential — only the configured-flag and the routing map.
type QBittorrentConfig struct {
	Enabled bool `json:"enabled"`
	// Routes is the explicit category→Library-ID map. An empty map means
	// every torrent is unrouted (ignored, never guessed).
	Routes []QBittorrentRoute `json:"routes"`
}

// QBittorrentRoute is one explicit category→Library mapping.
type QBittorrentRoute struct {
	Category  string `json:"category"`
	LibraryID string `json:"libraryID"`
}

// QBittorrentStatusEntry is one row of the ingest status feed,
// in the closed outcome vocabulary. No torrent payload or credential — only the
// release name, category, outcome, and a secret-free detail.
type QBittorrentStatusEntry struct {
	ReleaseName string `json:"releaseName"`
	Category    string `json:"category"`
	// Outcome is the closed vocabulary: imported / unrouted / already-in-catalog
	// / error.
	Outcome   string `json:"outcome"`
	LibraryID string `json:"libraryID,omitempty"`
	Detail    string `json:"detail,omitempty"`
	At        string `json:"at,omitempty"`
}

// QBittorrentIngestInfo is the combined manager-Console ingest view: the config
// plus the recent status feed.
type QBittorrentIngestInfo struct {
	Config QBittorrentConfig        `json:"config"`
	Status []QBittorrentStatusEntry `json:"status"`
}

// QBittorrentIngestConfigurer is the write-side seam the app wires
// the qbittorrent reconfigure path into. The composition
// root implements it: persist routing + connection to the config store, write
// the password to the secure store (never the DB, never a log — invariant #5),
// validate routing via the plugin's ValidateRouting, and reconfigure the
// running ingestor without a restart. Nil (the default) means the build cannot
// edit ingest config.
type QBittorrentIngestConfigurer interface {
	// Configure applies the new connection + routing + password. password is the
	// plaintext to store in the secure store; clearPassword true with an empty
	// password clears any stored secret (an anonymous endpoint). It returns the
	// ValidateRouting error verbatim on a rejected routing — a client-safe
	// validation message — leaving the running config untouched.
	Configure(ctx context.Context, in QBittorrentIngestInput) error
}

// QBittorrentIngestInput is the resolved, validated-shape input the configurer
// applies. Password is plaintext bound for the secure store only.
type QBittorrentIngestInput struct {
	URL         string
	Username    string
	Password    string
	SetPassword bool // false leaves the stored secret unchanged
	Routes      []QBittorrentRoute
}

// SetQBittorrentIngest wires the qBittorrent ingest status provider. Nil (the
// default) leaves the qbittorrentIngest field reporting disabled — honest for a
// server with no ingest configured.
func (h *Handler) SetQBittorrentIngest(provider QBittorrentIngestProvider) {
	h.qbittorrentIngest = provider
}

// SetQBittorrentIngestConfigurer wires the write-side reconfigure seam. Nil
// (the default) leaves setQBittorrentIngest reporting the feature unavailable.
func (h *Handler) SetQBittorrentIngestConfigurer(configurer QBittorrentIngestConfigurer) {
	h.qbittorrentConfigurer = configurer
}

// setQBittorrentIngest is the manager-Console mutation:
// it reconfigures qBittorrent ingest — connection (host/port URL + optional
// username/password) and the explicit category->Library routing — and applies
// it to the running ingestor without a restart. The password rides the wire
// inbound only and is handed straight to the secure store by the configurer; it
// is never persisted to the DB and never returned. Routing is validated by the
// plugin's ValidateRouting; an invalid routing is rejected with the validation
// error and the running config is left untouched. Admin-Session-gated, matching
// the read (qbittorrentIngest) and the other Console config mutations.
func (h *Handler) setQBittorrentIngest(ctx context.Context, scope auth.Scope, req request) (QBittorrentIngestInfo, error) {
	if h.qbittorrentConfigurer == nil {
		return QBittorrentIngestInfo{}, clientError("qBittorrent ingest configuration is unavailable on this server")
	}
	v := newVars(req)
	url, hasURL := v.String("url")
	if err := v.Err(); err != nil {
		return QBittorrentIngestInfo{}, err
	}
	if !hasURL || strings.TrimSpace(url) == "" {
		return QBittorrentIngestInfo{}, clientError("url is required")
	}
	username, _ := v.String("username")
	password, hasPassword := v.String("password")
	if err := v.Err(); err != nil {
		return QBittorrentIngestInfo{}, err
	}
	routes, err := parseQBittorrentRoutes(req)
	if err != nil {
		return QBittorrentIngestInfo{}, err
	}
	in := QBittorrentIngestInput{
		URL:         strings.TrimSpace(url),
		Username:    strings.TrimSpace(username),
		Password:    password,
		SetPassword: hasPassword,
		Routes:      routes,
	}
	if err := h.qbittorrentConfigurer.Configure(ctx, in); err != nil {
		return QBittorrentIngestInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "qbittorrent-ingest", "", ""); err != nil {
		return QBittorrentIngestInfo{}, err
	}
	return h.qbittorrentIngestInfo(ctx, scope)
}

// parseQBittorrentRoutes reads the routes variable: a list of
// {category, libraryID} objects. An absent list is an empty routing (every
// torrent unrouted — the honest default, not an error). A malformed element is
// a client error.
func parseQBittorrentRoutes(req request) ([]QBittorrentRoute, error) {
	raw, ok := req.Variables["routes"]
	if !ok || raw == nil {
		return []QBittorrentRoute{}, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, clientError("routes must be a list of {category, libraryID}")
	}
	routes := make([]QBittorrentRoute, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, clientError("routes must be a list of {category, libraryID}")
		}
		category, _ := obj["category"].(string)
		libraryID, _ := obj["libraryID"].(string)
		if strings.TrimSpace(category) == "" || strings.TrimSpace(libraryID) == "" {
			return nil, clientError("each route needs a non-empty category and libraryID")
		}
		routes = append(routes, QBittorrentRoute{
			Category:  strings.TrimSpace(category),
			LibraryID: strings.TrimSpace(libraryID),
		})
	}
	return routes, nil
}

// qbittorrentIngestInfo resolves the manager-Console ingest status. With no
// provider wired, ingest reports disabled and an empty feed.
func (h *Handler) qbittorrentIngestInfo(ctx context.Context, scope auth.Scope) (QBittorrentIngestInfo, error) {
	info := QBittorrentIngestInfo{
		Config: QBittorrentConfig{Routes: []QBittorrentRoute{}},
		Status: []QBittorrentStatusEntry{},
	}
	if h.qbittorrentIngest == nil {
		return info, nil
	}
	if config, ok := h.qbittorrentIngest.Config(ctx); ok {
		config.Enabled = true
		if config.Routes == nil {
			config.Routes = []QBittorrentRoute{}
		}
		sort.SliceStable(config.Routes, func(i, j int) bool {
			return config.Routes[i].Category < config.Routes[j].Category
		})
		info.Config = config
	}
	if status := h.qbittorrentIngest.Status(ctx); status != nil {
		info.Status = status
	}
	return info, nil
}
