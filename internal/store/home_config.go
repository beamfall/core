package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	HomeConfigScopeProfile        = "profile"
	HomeConfigScopeServerTemplate = "server_template"

	HomeConfigCurrentVersion = 1
	MaxHomeShelves           = 40
	MaxHomeShelfTitleLen     = 120
	MinHomeShelfLimit        = 4
	MaxHomeShelfLimit        = 50
)

var (
	homeHeroModes       = []string{"auto", "shelf"}
	homeShelfStyles     = []string{"shelf", "grid", "list"}
	homeArtworkSlots    = []string{"auto", "poster", "landscape", "square"}
	homeSourceTypes     = []string{"builtin", "rules", "playlist", "collection", "saved-filter", "library", "entity"}
	homeBuiltinKinds    = []string{"continue-watching", "next-up", "continue-listening", "watchlist", "recently-added", "most-played", "favorites", "random"}
	homeRandomSeedModes = []string{"", "session", "hourly", "daily"}
)

// HomeConfig is a Profile's or Server Template's customizable Home definition.
type HomeConfig struct {
	Version int            `json:"version"`
	Hero    HomeHeroConfig `json:"hero"`
	Shelves []HomeShelf    `json:"shelves"`
}

// HomeHeroConfig controls the Home hero banner.
type HomeHeroConfig struct {
	Enabled bool             `json:"enabled"`
	Mode    string           `json:"mode"`
	Source  *HomeShelfSource `json:"source,omitempty"`
}

// HomeShelf is one composable block on Home.
type HomeShelf struct {
	ID            string           `json:"id"`
	Title         string           `json:"title"`
	Hidden        bool             `json:"hidden"`
	HideWhenEmpty bool             `json:"hideWhenEmpty"`
	Display       HomeShelfDisplay `json:"display"`
	Source        HomeShelfSource  `json:"source"`
}

// HomeShelfDisplay controls how a shelf renders.
type HomeShelfDisplay struct {
	Style       string `json:"style"`
	ArtworkSlot string `json:"artworkSlot"`
	Limit       int    `json:"limit"`
}

// HomeShelfSort orders a shelf's items.
type HomeShelfSort struct {
	By             string `json:"by"`
	Desc           bool   `json:"desc"`
	RandomSeedMode string `json:"randomSeedMode,omitempty"`
}

// HomeShelfSource defines what media a HomeShelf displays.
type HomeShelfSource struct {
	Type             string          `json:"type"`
	BuiltinKind      string          `json:"builtinKind,omitempty"`
	LibraryIDs       []LibraryID     `json:"libraryIDs,omitempty"`
	Rules            *SmartRuleGroup `json:"rules,omitempty"`
	Sort             *HomeShelfSort  `json:"sort,omitempty"`
	RollUpContainers *bool           `json:"rollUpContainers,omitempty"`
	PlaylistID       string          `json:"playlistID,omitempty"`
	CollectionID     string          `json:"collectionID,omitempty"`
	SavedFilterID    string          `json:"savedFilterID,omitempty"`
	EntityKind       string          `json:"entityKind,omitempty"`
	EntityID         string          `json:"entityID,omitempty"`
	EntityName       string          `json:"entityName,omitempty"`
}

// DefaultHomeConfig returns the built-in system default Home configuration.
func DefaultHomeConfig() HomeConfig {
	return HomeConfig{
		Version: HomeConfigCurrentVersion,
		Hero: HomeHeroConfig{
			Enabled: true,
			Mode:    "auto",
		},
		Shelves: []HomeShelf{
			{
				ID:            "continue",
				Title:         "Continue Watching",
				HideWhenEmpty: true,
				Display:       HomeShelfDisplay{Style: "shelf", ArtworkSlot: "auto", Limit: 20},
				Source:        HomeShelfSource{Type: "builtin", BuiltinKind: "continue-watching"},
			},
			{
				ID:            "next-up",
				Title:         "Up Next",
				HideWhenEmpty: true,
				Display:       HomeShelfDisplay{Style: "shelf", ArtworkSlot: "auto", Limit: 20},
				Source:        HomeShelfSource{Type: "builtin", BuiltinKind: "next-up"},
			},
			{
				ID:            "continue-listening",
				Title:         "Continue Listening",
				HideWhenEmpty: true,
				Display:       HomeShelfDisplay{Style: "shelf", ArtworkSlot: "auto", Limit: 20},
				Source:        HomeShelfSource{Type: "builtin", BuiltinKind: "continue-listening"},
			},
			{
				ID:            "watchlist",
				Title:         "Watchlist",
				HideWhenEmpty: true,
				Display:       HomeShelfDisplay{Style: "shelf", ArtworkSlot: "auto", Limit: 20},
				Source:        HomeShelfSource{Type: "builtin", BuiltinKind: "watchlist"},
			},
			{
				ID:            "recent",
				Title:         "Recently Added",
				HideWhenEmpty: true,
				Display:       HomeShelfDisplay{Style: "shelf", ArtworkSlot: "auto", Limit: 20},
				Source:        HomeShelfSource{Type: "builtin", BuiltinKind: "recently-added"},
			},
			{
				ID:            "your-most-played",
				Title:         "Your Most-Played",
				HideWhenEmpty: true,
				Display:       HomeShelfDisplay{Style: "shelf", ArtworkSlot: "auto", Limit: 20},
				Source:        HomeShelfSource{Type: "builtin", BuiltinKind: "most-played"},
			},
		},
	}
}

// ValidateAndNormalizeHomeConfig validates and normalizes a HomeConfig in place.
func ValidateAndNormalizeHomeConfig(cfg *HomeConfig) error {
	if cfg == nil {
		return ValidationError("home config is required")
	}
	if cfg.Version == 0 {
		cfg.Version = HomeConfigCurrentVersion
	}
	if cfg.Version != HomeConfigCurrentVersion {
		return ValidationError(fmt.Sprintf("unsupported home config version %d", cfg.Version))
	}
	if cfg.Hero.Mode == "" {
		cfg.Hero.Mode = "auto"
	}
	if !slices.Contains(homeHeroModes, cfg.Hero.Mode) {
		return ValidationError("invalid hero mode: " + cfg.Hero.Mode)
	}
	if cfg.Hero.Mode == "shelf" {
		if cfg.Hero.Source == nil {
			return ValidationError("hero source is required when mode is shelf")
		}
		if err := validateAndNormalizeHomeSource(cfg.Hero.Source); err != nil {
			return ValidationError("hero source: " + err.Error())
		}
	} else {
		cfg.Hero.Source = nil
	}
	if len(cfg.Shelves) > MaxHomeShelves {
		return ValidationError(fmt.Sprintf("at most %d shelves are allowed on Home", MaxHomeShelves))
	}
	seenIDs := make(map[string]bool, len(cfg.Shelves))
	for i := range cfg.Shelves {
		if err := ValidateAndNormalizeHomeShelf(&cfg.Shelves[i]); err != nil {
			return ValidationError(fmt.Sprintf("shelf %d: %s", i+1, err.Error()))
		}
		if seenIDs[cfg.Shelves[i].ID] {
			return ValidationError("duplicate shelf id: " + cfg.Shelves[i].ID)
		}
		seenIDs[cfg.Shelves[i].ID] = true
	}
	return nil
}

// ValidateAndNormalizeHomeShelf validates and normalizes one HomeShelf.
func ValidateAndNormalizeHomeShelf(shelf *HomeShelf) error {
	if shelf == nil {
		return ValidationError("shelf is required")
	}
	shelf.ID = strings.TrimSpace(shelf.ID)
	if shelf.ID == "" {
		id, err := randomHomeShelfID()
		if err != nil {
			return err
		}
		shelf.ID = id
	}
	if len(shelf.ID) > 64 {
		return ValidationError("shelf id is too long")
	}
	shelf.Title = strings.TrimSpace(shelf.Title)
	if len(shelf.Title) > MaxHomeShelfTitleLen {
		return ValidationError(fmt.Sprintf("shelf title exceeds %d characters", MaxHomeShelfTitleLen))
	}
	if shelf.Display.Style == "" {
		shelf.Display.Style = "shelf"
	}
	if !slices.Contains(homeShelfStyles, shelf.Display.Style) {
		return ValidationError("invalid display style: " + shelf.Display.Style)
	}
	if shelf.Display.ArtworkSlot == "" {
		shelf.Display.ArtworkSlot = "auto"
	}
	if !slices.Contains(homeArtworkSlots, shelf.Display.ArtworkSlot) {
		return ValidationError("invalid artwork slot: " + shelf.Display.ArtworkSlot)
	}
	if shelf.Display.Limit == 0 {
		if shelf.Display.Style == "shelf" {
			shelf.Display.Limit = 20
		} else {
			shelf.Display.Limit = 12
		}
	}
	if shelf.Display.Limit < MinHomeShelfLimit || shelf.Display.Limit > MaxHomeShelfLimit {
		return ValidationError(fmt.Sprintf("shelf limit must be between %d and %d", MinHomeShelfLimit, MaxHomeShelfLimit))
	}
	if err := validateAndNormalizeHomeSource(&shelf.Source); err != nil {
		return err
	}
	if shelf.Title == "" {
		shelf.Title = defaultTitleForHomeSource(shelf.Source)
	}
	return nil
}

func validateAndNormalizeHomeSource(src *HomeShelfSource) error {
	if src == nil {
		return ValidationError("source is required")
	}
	src.Type = strings.TrimSpace(src.Type)
	if !slices.Contains(homeSourceTypes, src.Type) {
		return ValidationError("invalid source type: " + src.Type)
	}
	// Deduplicate libraryIDs preserving order.
	if len(src.LibraryIDs) > 0 {
		dedup := make([]LibraryID, 0, len(src.LibraryIDs))
		seen := make(map[LibraryID]bool, len(src.LibraryIDs))
		for _, id := range src.LibraryIDs {
			id = LibraryID(strings.TrimSpace(string(id)))
			if id != "" && !seen[id] {
				dedup = append(dedup, id)
				seen[id] = true
			}
		}
		src.LibraryIDs = dedup
	}
	switch src.Type {
	case "builtin":
		src.BuiltinKind = strings.TrimSpace(src.BuiltinKind)
		if !slices.Contains(homeBuiltinKinds, src.BuiltinKind) {
			return ValidationError("invalid builtinKind: " + src.BuiltinKind)
		}
	case "rules":
		if src.Rules == nil {
			src.Rules = &SmartRuleGroup{Match: "all"}
		}
	case "playlist":
		src.PlaylistID = strings.TrimSpace(src.PlaylistID)
		if src.PlaylistID == "" {
			return ValidationError("playlistID is required")
		}
	case "collection":
		src.CollectionID = strings.TrimSpace(src.CollectionID)
		if src.CollectionID == "" {
			return ValidationError("collectionID is required")
		}
	case "saved-filter":
		src.SavedFilterID = strings.TrimSpace(src.SavedFilterID)
		if src.SavedFilterID == "" {
			return ValidationError("savedFilterID is required")
		}
	case "library":
		if len(src.LibraryIDs) == 0 {
			return ValidationError("libraryIDs is required for library source")
		}
	case "entity":
		src.EntityKind = strings.TrimSpace(src.EntityKind)
		src.EntityID = strings.TrimSpace(src.EntityID)
		src.EntityName = strings.TrimSpace(src.EntityName)
		if src.EntityKind == "" || (src.EntityID == "" && src.EntityName == "") {
			return ValidationError("entityKind and entityID or entityName are required")
		}
	}
	if src.Rules != nil {
		if _, err := compileSmartRuleGroup(*src.Rules); err != nil {
			return err
		}
	}
	if src.Sort != nil {
		sortExpr, _ := json.Marshal(map[string]any{
			"by":  src.Sort.By,
			"dir": map[bool]string{true: "desc", false: "asc"}[src.Sort.Desc],
		})
		if _, err := decodeSmartRuleSort(string(sortExpr)); err != nil {
			return err
		}
		if !slices.Contains(homeRandomSeedModes, src.Sort.RandomSeedMode) {
			return ValidationError("invalid randomSeedMode: " + src.Sort.RandomSeedMode)
		}
	}
	return nil
}

func defaultTitleForHomeSource(src HomeShelfSource) string {
	switch src.Type {
	case "builtin":
		switch src.BuiltinKind {
		case "continue-watching":
			return "Continue Watching"
		case "next-up":
			return "Up Next"
		case "continue-listening":
			return "Continue Listening"
		case "watchlist":
			return "Watchlist"
		case "recently-added":
			return "Recently Added"
		case "most-played":
			return "Your Most-Played"
		case "favorites":
			return "Favorites"
		case "random":
			return "Discover"
		}
	case "rules":
		return "Smart Shelf"
	case "playlist":
		return "Playlist"
	case "collection":
		return "Collection"
	case "saved-filter":
		return "Saved Filter"
	case "library":
		return "Library"
	case "entity":
		if src.EntityName != "" {
			return src.EntityName
		}
		return "Featured"
	}
	return "Shelf"
}

func randomHomeShelfID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "hs_" + hex.EncodeToString(b[:]), nil
}

// HomeConfig reads the stored HomeConfig for the given scope and scopeID.
func (s *Store) HomeConfig(ctx context.Context, scope, scopeID string) (HomeConfig, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return HomeConfig{}, false, err
	}
	var raw string
	err := s.catalogR.QueryRowContext(ctx,
		`SELECT data_json FROM home_config WHERE scope = ? AND scope_id = ?`,
		scope, scopeID,
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return HomeConfig{}, false, nil
	}
	if err != nil {
		return HomeConfig{}, false, err
	}
	var cfg HomeConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return HomeConfig{}, false, err
	}
	return cfg, true, nil
}

// SetHomeConfig validates and persists a HomeConfig for the given scope and scopeID.
func (s *Store) SetHomeConfig(ctx context.Context, scope, scopeID string, cfg HomeConfig, now time.Time) error {
	if scope != HomeConfigScopeProfile && scope != HomeConfigScopeServerTemplate {
		return ValidationError("invalid home config scope: " + scope)
	}
	if scope == HomeConfigScopeProfile && strings.TrimSpace(scopeID) == "" {
		return ValidationError("profile scope_id is required")
	}
	if scope == HomeConfigScopeServerTemplate {
		scopeID = ""
	}
	if err := ValidateAndNormalizeHomeConfig(&cfg); err != nil {
		return err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err = s.catalogW.ExecContext(ctx, `
		INSERT INTO home_config (scope, scope_id, version, data_json, updated_at_ms)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(scope, scope_id) DO UPDATE SET
			version = excluded.version,
			data_json = excluded.data_json,
			updated_at_ms = excluded.updated_at_ms
	`, scope, scopeID, cfg.Version, string(raw), now.UTC().UnixMilli())
	return err
}

// DeleteHomeConfig removes the stored HomeConfig for the given scope and scopeID.
func (s *Store) DeleteHomeConfig(ctx context.Context, scope, scopeID string) error {
	if scope == HomeConfigScopeServerTemplate {
		scopeID = ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx,
		`DELETE FROM home_config WHERE scope = ? AND scope_id = ?`,
		scope, scopeID,
	)
	return err
}
