package scrape

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	sdk "github.com/beamfall/plugin-sdk"

	"github.com/beamfall/core/internal/plugin"
)

var (
	ErrNoChildListing     = errors.New("scraper declares no child listing")
	ErrNoSuchChildLevel   = errors.New("no such level")
	ErrChildCandidate     = errors.New("invalid candidateID")
	ErrChildParentExtra   = errors.New("parentKey is forbidden at level 1")
	ErrChildParentMissing = errors.New("parentKey is required after level 1")
	ErrChildParentKey     = errors.New("invalid parentKey")
	ErrChildParentRow     = errors.New("parentKey was not returned by the previous level")
	ErrChildParentAmbig   = errors.New("parentKey names rows that open different listings")
	ErrChildOrdinal       = errors.New("parent row has no valid ordinal for this level")
)

// ChildRow is one display row from a scraper's declared child listing.
type ChildRow struct {
	Key        string  `json:"key"`
	Label      string  `json:"label"`
	Sequence   *string `json:"sequence,omitempty"`
	Index      *int    `json:"index,omitempty"`
	Year       *int    `json:"year,omitempty"`
	ArtworkURL *string `json:"artworkURL,omitempty"`
}

// ListChildren walks the declared listing for a candidate. Later levels use
// only a parent row reached through the same candidate's earlier listings.
func (e *Engine) ListChildren(ctx context.Context, spec plugin.ScraperSpec, level int, candidateID string, parentKey *string, vars Variables) ([]ChildRow, error) {
	switch {
	case len(spec.Children) == 0:
		return nil, ErrNoChildListing
	case level < 1 || level > len(spec.Children):
		return nil, ErrNoSuchChildLevel
	case !sdk.ValidChildKeySegment(candidateID):
		return nil, ErrChildCandidate
	case level == 1 && parentKey != nil:
		return nil, ErrChildParentExtra
	case level > 1 && parentKey == nil:
		return nil, ErrChildParentMissing
	case parentKey != nil && !sdk.ValidChildKeySegment(*parentKey):
		return nil, ErrChildParentKey
	}

	config, err := e.configValues(ctx, vars)
	if err != nil {
		return nil, err
	}
	var parent *ChildRow
	if level > 1 {
		parent, err = e.findChildParent(ctx, spec, level-2, candidateID, *parentKey, config)
		if err != nil {
			return nil, err
		}
	}
	return e.childRows(ctx, spec, spec.Children[level-1], candidateID, parent, config)
}

// findChildParent starts at the root even after a restart or cache eviction.
// The wire key carries no ancestor, so it walks level by level, descending only
// through rows a read could accept as parentKey. At the target level it keeps
// one matching row and refuses the key when another match would open a
// different listing. The SDK allows at most three levels, so only the single
// root listing is ever retained in full.
func (e *Engine) findChildParent(ctx context.Context, spec plugin.ScraperSpec, target int, candidateID, parentKey string, config map[string]string) (*ChildRow, error) {
	opens := spec.Children[target+1]
	var match *ChildRow
	ambiguous := false
	consider := func(rows []ChildRow) {
		for _, row := range rows {
			switch {
			case row.Key != parentKey:
			case match == nil:
				match = &row
			case !slices.Equal(childParentPairs(opens, match), childParentPairs(opens, &row)):
				ambiguous = true
			}
		}
	}
	rows, err := e.childRows(ctx, spec, spec.Children[0], candidateID, nil, config)
	if err != nil {
		return nil, err
	}
	if target == 0 {
		consider(rows)
	}
	for current := 0; current < target; current++ {
		next := spec.Children[current+1]
		var deeper []ChildRow
		for _, parent := range openableParents(next, rows) {
			children, err := e.childRows(ctx, spec, next, candidateID, &parent, config)
			if errors.Is(err, ErrChildOrdinal) {
				continue // This row cannot open the next level.
			}
			if err != nil {
				return nil, err
			}
			if current+1 == target {
				consider(children)
			} else {
				deeper = append(deeper, children...)
			}
		}
		rows = deeper
	}
	switch {
	case match == nil:
		return nil, ErrChildParentRow
	case ambiguous:
		return nil, ErrChildParentAmbig
	}
	return match, nil
}

// openableParents returns, in first-seen order, one row for each key a read of
// listing would accept as parentKey: a valid path segment whose rows all
// substitute the same values into the listing's template. A key shared by
// rows that would open different listings is ambiguous and opens nothing.
func openableParents(listing sdk.ChildListing, rows []ChildRow) []ChildRow {
	first := map[string]int{}
	ambiguous := map[string]bool{}
	var order []string
	for i, row := range rows {
		if !sdk.ValidChildKeySegment(row.Key) {
			continue
		}
		j, seen := first[row.Key]
		if !seen {
			first[row.Key] = i
			order = append(order, row.Key)
		} else if !slices.Equal(childParentPairs(listing, &rows[j]), childParentPairs(listing, &row)) {
			ambiguous[row.Key] = true
		}
	}
	var parents []ChildRow
	for _, key := range order {
		if !ambiguous[key] {
			parents = append(parents, rows[first[key]])
		}
	}
	return parents
}

// childParentPairs returns the parent placeholders the listing's template
// names, with the values a parent row substitutes for them.
func childParentPairs(listing sdk.ChildListing, parent *ChildRow) [][2]string {
	var sequence, index string
	if parent.Sequence != nil {
		sequence = *parent.Sequence
	}
	if parent.Index != nil {
		index = strconv.Itoa(*parent.Index)
	}
	var pairs [][2]string
	for _, pair := range [][2]string{{"{parent}", parent.Key}, {"{sequence}", sequence}, {"{index}", index}} {
		if strings.Contains(listing.ListTemplate, pair[0]) {
			pairs = append(pairs, pair)
		}
	}
	return pairs
}

func (e *Engine) childRows(ctx context.Context, spec plugin.ScraperSpec, listing sdk.ChildListing, candidateID string, parent *ChildRow, config map[string]string) ([]ChildRow, error) {
	var pairs [][2]string
	if parent != nil {
		pairs = childParentPairs(listing, parent)
		for _, pair := range pairs {
			if !sdk.ValidChildKeySegment(pair[1]) {
				return nil, ErrChildOrdinal
			}
		}
	}

	// SDK child templates have no pagination placeholder.
	rawURL := buildURL(spec.Endpoint, listing.ListTemplate, "{id}", candidateID, Variables{}, config, 0, pairs...)
	body, err := e.fetchAuthorizedResponse(ctx, spec, http.MethodGet, rawURL, nil, "", true)
	if err != nil {
		return nil, err
	}
	root, err := decodeJSON(body)
	if err != nil {
		return nil, fmt.Errorf("scrape: scraper %q: %w", spec.ID, err)
	}
	node, _ := resolveJSONNode(root, substituteConfig(listing.ItemsPath, config, identity))
	elements, ok := node.([]any)
	if !ok {
		return nil, fmt.Errorf("scrape: scraper %q: child items path %q did not resolve to an array", spec.ID, listing.ItemsPath)
	}

	limit := listing.MaxItems
	if limit <= 0 {
		limit = 200
	}
	limit = min(limit, 500)
	rows := make([]ChildRow, 0, min(len(elements), limit))
	for _, element := range elements {
		if row, ok := mapChildRow(element, listing, spec.ArtworkBaseURL, config); ok {
			rows = append(rows, row)
			if len(rows) == limit {
				break
			}
		}
	}
	return rows, nil
}

func mapChildRow(element any, listing sdk.ChildListing, artworkBaseURL string, config map[string]string) (ChildRow, bool) {
	resolve := func(path string) (any, bool) {
		if path == "" {
			return nil, false
		}
		return resolveJSONScalar(element, substituteConfig(path, config, identity))
	}
	raw, _ := resolve(listing.Key)
	key, ok := childOrdinalText(raw)
	if !ok {
		return ChildRow{}, false
	}
	raw, _ = resolve(listing.Label)
	label, _ := scalarString(raw)
	row := ChildRow{Key: key, Label: label}
	if raw, ok := resolve(listing.Sequence); ok {
		if value, ok := childOrdinalText(raw); ok {
			row.Sequence = &value
		}
	}
	if raw, ok := resolve(listing.Index); ok {
		value, _ := childOrdinalText(raw)
		if n, err := strconv.Atoi(value); err == nil {
			row.Index = &n
		}
	}
	if raw, ok := resolve(listing.Year); ok {
		value, _ := childOrdinalText(raw)
		value = strings.TrimSpace(value)
		if year, err := strconv.Atoi(value); err == nil {
			row.Year = &year
		} else if date, ok := dateFromRFC822(value); ok {
			year, _ := strconv.Atoi(date[:4])
			row.Year = &year
		} else if date, err := time.Parse(time.RFC3339, value); err == nil {
			row.Year = new(date.Year())
		}
	}
	if raw, ok := resolve(listing.Artwork); ok {
		value, _ := raw.(string)
		artwork := renderArtworkURL(artworkBaseURL, "", value)
		if parsed, err := url.Parse(artwork); err == nil && parsed.Hostname() != "" &&
			(parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.User == nil {
			row.ArtworkURL = &artwork
		}
	}
	return row, true
}

func childOrdinalText(value any) (string, bool) {
	switch value := value.(type) {
	case string:
		return value, true
	case json.Number:
		text := value.String()
		if !strings.ContainsAny(text, ".eE") {
			if text == "-0" {
				text = "0"
			}
			return text, true
		}
	}
	return "", false
}
