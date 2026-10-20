package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	retentionRulesConfigKey      = "retention.rules"
	retentionCandidateKeyPrefix  = "retention.candidates:"
	retentionDefaultCollection   = "Leaving soon"
	retentionSourcePageSize      = 200
	retentionRuleIDRandomBytes   = 8
	retentionRuleDefaultGraceDay = 7
)

// RetentionRule is the Console-authored ADR-0051 retention policy. It persists
// in server_config JSON so RET-2 does not move the frozen schema.
type RetentionRule struct {
	ID                  string
	Name                string
	SmartCollectionID   SmartCollectionID
	SourceExpr          string
	TargetLibraryID     LibraryID
	GraceDays           int
	RevealedAtAuthoring bool
	Enabled             bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// RetentionRulePatch is a partial update. Nil fields preserve current values.
type RetentionRulePatch struct {
	Name                *string
	SourceExpr          *string
	TargetLibraryID     *LibraryID
	GraceDays           *int
	RevealedAtAuthoring *bool
	Enabled             *bool
}

type retentionRulesConfig struct {
	Rules []retentionRuleJSON `json:"rules"`
}

type retentionRuleJSON struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	SmartCollectionID   string `json:"smartCollectionID"`
	SourceExpr          string `json:"sourceExpr"`
	TargetLibraryID     string `json:"targetLibraryID"`
	GraceDays           int    `json:"graceDays"`
	RevealedAtAuthoring bool   `json:"revealedAtAuthoring"`
	Enabled             bool   `json:"enabled"`
	CreatedAtMS         int64  `json:"createdAtMS"`
	UpdatedAtMS         int64  `json:"updatedAtMS"`
}

type retentionCandidate struct {
	FirstMatchedAt time.Time `json:"-"`
	LastSeenAt     time.Time `json:"-"`
	HeldAt         time.Time `json:"-"`
}

type retentionCandidateJSON struct {
	FirstMatchedAtMS int64 `json:"firstMatchedAtMS"`
	LastSeenAtMS     int64 `json:"lastSeenAtMS"`
	HeldAtMS         int64 `json:"heldAtMS,omitempty"`
}

type retentionCandidatesConfig struct {
	Candidates map[string]retentionCandidateJSON `json:"candidates"`
}

// CreateRetentionRule persists a retention rule and creates its leaving-soon
// Smart Collection. The Source expression is decoded up front so scheduled
// retention never discovers malformed policy during an autonomous sweep.
func (s *Store) CreateRetentionRule(ctx context.Context, rule RetentionRule, now time.Time) (RetentionRule, error) {
	rule.Name = strings.TrimSpace(rule.Name)
	if rule.Name == "" {
		rule.Name = retentionDefaultCollection
	}
	if rule.TargetLibraryID == "" {
		return RetentionRule{}, ValidationError("target library ID is required")
	}
	if err := s.libraryExists(ctx, rule.TargetLibraryID); err != nil {
		return RetentionRule{}, err
	}
	if rule.SourceExpr == "" {
		rule.SourceExpr = `{"kind":"all-items"}`
	}
	query, err := decodeRetentionSourceExpr(rule.SourceExpr)
	if err != nil {
		return RetentionRule{}, err
	}
	if query.LibraryID != "" && query.LibraryID != rule.TargetLibraryID {
		return RetentionRule{}, ValidationError("source libraryID must match target library")
	}
	if rule.GraceDays < 0 {
		return RetentionRule{}, ValidationError("graceDays must be non-negative")
	}
	if rule.GraceDays == 0 {
		rule.GraceDays = retentionRuleDefaultGraceDay
	}
	id, err := randomRetentionRuleID()
	if err != nil {
		return RetentionRule{}, err
	}
	now = now.UTC()
	collection, err := s.CreateSmartCollection(ctx, SmartCollection{
		LibraryID:  rule.TargetLibraryID,
		Name:       rule.Name,
		SourceExpr: rule.SourceExpr,
	}, now)
	if err != nil {
		return RetentionRule{}, err
	}
	rule.ID = id
	rule.SmartCollectionID = collection.ID
	rule.CreatedAt = now
	rule.UpdatedAt = now
	rule.Enabled = true
	rules, err := s.retentionRules(ctx)
	if err != nil {
		return RetentionRule{}, err
	}
	rules = append(rules, rule)
	if err := s.saveRetentionRules(ctx, rules, now); err != nil {
		return RetentionRule{}, err
	}
	return rule, nil
}

// RetentionRules returns all configured retention rules in creation order.
func (s *Store) RetentionRules(ctx context.Context) ([]RetentionRule, error) {
	return s.retentionRules(ctx)
}

func (s *Store) retentionRules(ctx context.Context) ([]RetentionRule, error) {
	values, err := s.serverConfigValues(ctx)
	if err != nil {
		return nil, err
	}
	raw := values[retentionRulesConfigKey]
	if strings.TrimSpace(raw) == "" {
		return []RetentionRule{}, nil
	}
	var cfg retentionRulesConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, err
	}
	out := make([]RetentionRule, 0, len(cfg.Rules))
	for _, row := range cfg.Rules {
		out = append(out, retentionRuleFromJSON(row))
	}
	return out, nil
}

func (s *Store) saveRetentionRules(ctx context.Context, rules []RetentionRule, now time.Time) error {
	cfg := retentionRulesConfig{Rules: make([]retentionRuleJSON, 0, len(rules))}
	for _, rule := range rules {
		cfg.Rules = append(cfg.Rules, retentionRuleToJSON(rule))
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.setServerConfigValues(ctx, map[string]string{retentionRulesConfigKey: string(raw)}, now)
}

// UpdateRetentionRule applies a partial rule update and mirrors display/source
// changes onto the owned Smart Collection.
func (s *Store) UpdateRetentionRule(ctx context.Context, id string, patch RetentionRulePatch, now time.Time) (RetentionRule, error) {
	rules, err := s.retentionRules(ctx)
	if err != nil {
		return RetentionRule{}, err
	}
	for i := range rules {
		if rules[i].ID != id {
			continue
		}
		updated := rules[i]
		if patch.Name != nil {
			updated.Name = strings.TrimSpace(*patch.Name)
			if updated.Name == "" {
				return RetentionRule{}, ValidationError("name is required")
			}
		}
		if patch.SourceExpr != nil {
			updated.SourceExpr = strings.TrimSpace(*patch.SourceExpr)
			if updated.SourceExpr == "" {
				return RetentionRule{}, ValidationError("sourceExpr is required")
			}
		}
		if patch.TargetLibraryID != nil {
			updated.TargetLibraryID = *patch.TargetLibraryID
			if updated.TargetLibraryID == "" {
				return RetentionRule{}, ValidationError("target library ID is required")
			}
		}
		if patch.GraceDays != nil {
			if *patch.GraceDays < 0 {
				return RetentionRule{}, ValidationError("graceDays must be non-negative")
			}
			updated.GraceDays = *patch.GraceDays
		}
		if patch.RevealedAtAuthoring != nil {
			updated.RevealedAtAuthoring = *patch.RevealedAtAuthoring
		}
		if patch.Enabled != nil {
			updated.Enabled = *patch.Enabled
		}
		if err := s.validateRetentionRule(ctx, updated); err != nil {
			return RetentionRule{}, err
		}
		updated.UpdatedAt = now.UTC()
		if _, err := s.UpdateSmartCollection(ctx, SmartCollection{
			ID:         updated.SmartCollectionID,
			Name:       updated.Name,
			SourceExpr: updated.SourceExpr,
		}); err != nil {
			return RetentionRule{}, err
		}
		rules[i] = updated
		if err := s.saveRetentionRules(ctx, rules, now); err != nil {
			return RetentionRule{}, err
		}
		return updated, nil
	}
	return RetentionRule{}, ErrSmartCollectionNotFound
}

// DeleteRetentionRule removes the rule, its candidate cache, and its owned
// Smart Collection.
func (s *Store) DeleteRetentionRule(ctx context.Context, id string, now time.Time) (RetentionRule, error) {
	rules, err := s.retentionRules(ctx)
	if err != nil {
		return RetentionRule{}, err
	}
	for i, rule := range rules {
		if rule.ID != id {
			continue
		}
		if _, err := s.DeleteSmartCollection(ctx, rule.SmartCollectionID); err != nil && !errors.Is(err, ErrSmartCollectionNotFound) {
			return RetentionRule{}, err
		}
		rules = append(rules[:i], rules[i+1:]...)
		if err := s.saveRetentionRules(ctx, rules, now); err != nil {
			return RetentionRule{}, err
		}
		if err := s.clearServerConfigValues(ctx, []string{retentionCandidateKey(id)}); err != nil {
			return RetentionRule{}, err
		}
		return rule, nil
	}
	return RetentionRule{}, ErrSmartCollectionNotFound
}

// SetRetentionCandidateHold toggles the operator hold for one current or prior
// candidate. A held candidate is exempt from autonomous retention, without
// pausing the rest of the rule. The hold is manager-tier operator state,
// distinct from the per-Profile Favorite-class keep (SetMediaKeep): either one
// exempts the item from the sweep, but only the hold is rule-scoped.
//
// Object authorization: the rule and the Media item both resolve
// through the caller's LibraryFilter, and the item must be a current or prior
// candidate of that rule. Unknown, hidden, cross-Library, and non-candidate
// IDs all refuse with the same not-found errors the read paths use, so the
// mutation is not an existence oracle and never creates retention state for
// an unauthorized or unmatched ID.
func (s *Store) SetRetentionCandidateHold(ctx context.Context, ruleID string, mediaID MediaID, hold bool, filter LibraryFilter, now time.Time) error {
	if ruleID == "" || mediaID == "" {
		return ValidationError("rule ID and media ID are required")
	}
	rule, err := s.retentionRuleByID(ctx, ruleID)
	if err != nil {
		return err
	}
	media, err := s.Media(ctx, mediaID, filter)
	if err != nil {
		return err
	}
	if media.LibraryID != rule.TargetLibraryID {
		return ValidationError("media not found")
	}
	candidates, err := s.retentionCandidates(ctx, rule.ID)
	if err != nil {
		return err
	}
	c, isCandidate := candidates[mediaID]
	if !isCandidate {
		matches, err := s.retentionRuleMatches(ctx, rule, mediaID)
		if err != nil {
			return err
		}
		if !matches {
			return ValidationError("media is not a retention candidate")
		}
	}
	if c.FirstMatchedAt.IsZero() {
		c.FirstMatchedAt = now.UTC()
	}
	c.LastSeenAt = now.UTC()
	if hold {
		c.HeldAt = now.UTC()
	} else {
		c.HeldAt = time.Time{}
	}
	candidates[mediaID] = c
	return s.saveRetentionCandidates(ctx, rule.ID, candidates, now)
}

// retentionRuleByID resolves one configured rule. A missing ID returns the
// same not-found error the rule mutations use, so callers cannot probe rule
// existence through this path.
func (s *Store) retentionRuleByID(ctx context.Context, ruleID string) (RetentionRule, error) {
	rules, err := s.retentionRules(ctx)
	if err != nil {
		return RetentionRule{}, err
	}
	for _, rule := range rules {
		if rule.ID == ruleID {
			return rule, nil
		}
	}
	return RetentionRule{}, ErrSmartCollectionNotFound
}

// retentionRuleMatches reports whether mediaID is currently in the rule's live
// source set — the same membership the autonomous sweep computes.
func (s *Store) retentionRuleMatches(ctx context.Context, rule RetentionRule, mediaID MediaID) (bool, error) {
	query, err := decodeRetentionSourceExpr(rule.SourceExpr)
	if err != nil {
		return false, err
	}
	query.LibraryID = rule.TargetLibraryID
	matches, err := s.retentionSourceMediaSet(ctx, rule, query)
	if err != nil {
		return false, err
	}
	return matches[mediaID], nil
}

// ApplyRetentionRules runs the ADR-0051 autonomous retention sweep.
func (s *Store) ApplyRetentionRules(ctx context.Context, now time.Time) (trashed int, skipped bool, err error) {
	rules, err := s.retentionRules(ctx)
	if err != nil {
		return 0, false, err
	}
	if len(rules) == 0 {
		return 0, false, nil
	}
	if s.trashDisabled {
		s.trashDisabledSweepSkips.Add(1)
		return 0, true, ErrTrashDisabledConfirmationRequired
	}
	now = now.UTC()
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if err := s.validateRetentionRule(ctx, rule); err != nil {
			return trashed, false, err
		}
		query, err := decodeRetentionSourceExpr(rule.SourceExpr)
		if err != nil {
			return trashed, false, err
		}
		query.LibraryID = rule.TargetLibraryID
		matches, err := s.retentionSourceMediaSet(ctx, rule, query)
		if err != nil {
			return trashed, false, err
		}
		candidates, err := s.retentionCandidates(ctx, rule.ID)
		if err != nil {
			return trashed, false, err
		}
		for id := range candidates {
			if !matches[id] {
				delete(candidates, id)
			}
		}
		due := make([]MediaID, 0, len(matches))
		for id := range matches {
			c := candidates[id]
			if c.FirstMatchedAt.IsZero() {
				c.FirstMatchedAt = now
			}
			c.LastSeenAt = now
			candidates[id] = c
			if !c.HeldAt.IsZero() {
				continue
			}
			kept, err := s.mediaFavoritedByAny(ctx, id)
			if err != nil {
				return trashed, false, err
			}
			if kept {
				continue
			}
			if !c.FirstMatchedAt.Add(time.Duration(rule.GraceDays) * 24 * time.Hour).After(now) {
				due = append(due, id)
			}
		}
		sort.Slice(due, func(i, j int) bool { return due[i] < due[j] })
		filter := LibraryFilter{AllowedIDs: []LibraryID{rule.TargetLibraryID}, Revealed: rule.RevealedAtAuthoring}
		for _, id := range due {
			if err := s.trashMediaAt(ctx, id, filter, now); err != nil {
				return trashed, false, err
			}
			delete(candidates, id)
			trashed++
			if err := s.AppendActivity(ctx, ActivityEntry{
				OccurredAt: now,
				Action:     ActivityRetentionTrash,
				TargetType: "retention_rule",
				TargetID:   rule.ID,
				LibraryID:  rule.TargetLibraryID,
			}); err != nil {
				return trashed, false, err
			}
		}
		if err := s.saveRetentionCandidates(ctx, rule.ID, candidates, now); err != nil {
			return trashed, false, err
		}
	}
	return trashed, false, nil
}

func (s *Store) retentionSourceMediaSet(ctx context.Context, rule RetentionRule, query SourceQuery) (map[MediaID]bool, error) {
	out := map[MediaID]bool{}
	filter := LibraryFilter{AllowedIDs: []LibraryID{rule.TargetLibraryID}, Revealed: rule.RevealedAtAuthoring}
	for offset := 0; ; offset += retentionSourcePageSize {
		query.Limit = retentionSourcePageSize
		query.Offset = offset
		items, err := s.SourceItems(ctx, filter, query)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item.Kind == "" || item.Kind == "media" {
				out[item.ID] = true
			}
		}
		if len(items) < retentionSourcePageSize {
			break
		}
	}
	return out, nil
}

func (s *Store) validateRetentionRule(ctx context.Context, rule RetentionRule) error {
	if rule.ID == "" {
		return ValidationError("rule ID is required")
	}
	if rule.Name == "" {
		return ValidationError("name is required")
	}
	if rule.TargetLibraryID == "" {
		return ValidationError("target library ID is required")
	}
	if rule.GraceDays < 0 {
		return ValidationError("graceDays must be non-negative")
	}
	if err := s.libraryExists(ctx, rule.TargetLibraryID); err != nil {
		return err
	}
	query, err := decodeRetentionSourceExpr(rule.SourceExpr)
	if err != nil {
		return err
	}
	if query.LibraryID != "" && query.LibraryID != rule.TargetLibraryID {
		return ValidationError("source libraryID must match target library")
	}
	return nil
}

func decodeRetentionSourceExpr(expr string) (SourceQuery, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return SourceQuery{Kind: "all-items"}, nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(expr), &raw); err != nil {
		return SourceQuery{}, ValidationError("sourceExpr is not valid JSON")
	}
	query := SourceQuery{Kind: stringFromRaw(raw["kind"])}
	if query.Kind == "" {
		query.Kind = "all-items"
	}
	switch query.Kind {
	case "continue-watching", "next-up", "recently-played", "most-played", "queue", "resume-progress":
		return SourceQuery{}, ValidationError("retention source cannot be profile-dependent")
	case "entity-items", "directory":
		return SourceQuery{}, ValidationError("retention source must yield media")
	}
	if _, ok := raw["limit"]; ok {
		return SourceQuery{}, ValidationError("retention source cannot set limit")
	}
	if _, ok := raw["offset"]; ok {
		return SourceQuery{}, ValidationError("retention source cannot set offset")
	}
	query.EntityKind = stringFromRaw(raw["entityKind"])
	query.EntityID = EntityID(stringFromRaw(raw["entityID"]))
	query.LibraryID = LibraryID(stringFromRaw(raw["libraryID"]))
	query.Seed = stringFromRaw(raw["seed"])
	query.Value = strings.TrimSpace(stringFromRaw(raw["value"]))
	if fields, ok := raw["fieldFilters"].(map[string]any); ok {
		query.FieldFilters = fields
	}
	if entityRaw, ok := raw["entityFilter"].(map[string]any); ok {
		kind := strings.TrimSpace(stringFromRaw(entityRaw["kind"]))
		name := strings.TrimSpace(stringFromRaw(entityRaw["name"]))
		if kind == "" || name == "" {
			return SourceQuery{}, ValidationError("entityFilter requires kind and name")
		}
		query.EntityFilter = &EntityFilter{Kind: kind, Name: name}
	}
	return query, nil
}

func stringFromRaw(value any) string {
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func (s *Store) retentionCandidates(ctx context.Context, ruleID string) (map[MediaID]retentionCandidate, error) {
	values, err := s.serverConfigValues(ctx)
	if err != nil {
		return nil, err
	}
	raw := values[retentionCandidateKey(ruleID)]
	if strings.TrimSpace(raw) == "" {
		return map[MediaID]retentionCandidate{}, nil
	}
	var cfg retentionCandidatesConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, err
	}
	out := map[MediaID]retentionCandidate{}
	for rawID, row := range cfg.Candidates {
		out[MediaID(rawID)] = retentionCandidate{
			FirstMatchedAt: millisToTime(row.FirstMatchedAtMS),
			LastSeenAt:     millisToTime(row.LastSeenAtMS),
			HeldAt:         millisToTime(row.HeldAtMS),
		}
	}
	return out, nil
}

func (s *Store) saveRetentionCandidates(ctx context.Context, ruleID string, candidates map[MediaID]retentionCandidate, now time.Time) error {
	if len(candidates) == 0 {
		return s.clearServerConfigValues(ctx, []string{retentionCandidateKey(ruleID)})
	}
	cfg := retentionCandidatesConfig{Candidates: map[string]retentionCandidateJSON{}}
	for id, c := range candidates {
		row := retentionCandidateJSON{
			FirstMatchedAtMS: timeToMillis(c.FirstMatchedAt),
			LastSeenAtMS:     timeToMillis(c.LastSeenAt),
		}
		if !c.HeldAt.IsZero() {
			row.HeldAtMS = timeToMillis(c.HeldAt)
		}
		cfg.Candidates[string(id)] = row
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.setServerConfigValues(ctx, map[string]string{retentionCandidateKey(ruleID): string(raw)}, now)
}

func retentionCandidateKey(ruleID string) string {
	return retentionCandidateKeyPrefix + ruleID
}

func retentionRuleFromJSON(row retentionRuleJSON) RetentionRule {
	return RetentionRule{
		ID:                  row.ID,
		Name:                row.Name,
		SmartCollectionID:   SmartCollectionID(row.SmartCollectionID),
		SourceExpr:          row.SourceExpr,
		TargetLibraryID:     LibraryID(row.TargetLibraryID),
		GraceDays:           row.GraceDays,
		RevealedAtAuthoring: row.RevealedAtAuthoring,
		Enabled:             row.Enabled,
		CreatedAt:           millisToTime(row.CreatedAtMS),
		UpdatedAt:           millisToTime(row.UpdatedAtMS),
	}
}

func retentionRuleToJSON(rule RetentionRule) retentionRuleJSON {
	return retentionRuleJSON{
		ID:                  rule.ID,
		Name:                rule.Name,
		SmartCollectionID:   string(rule.SmartCollectionID),
		SourceExpr:          rule.SourceExpr,
		TargetLibraryID:     string(rule.TargetLibraryID),
		GraceDays:           rule.GraceDays,
		RevealedAtAuthoring: rule.RevealedAtAuthoring,
		Enabled:             rule.Enabled,
		CreatedAtMS:         timeToMillis(rule.CreatedAt),
		UpdatedAtMS:         timeToMillis(rule.UpdatedAt),
	}
}

func randomRetentionRuleID() (string, error) {
	raw := make([]byte, retentionRuleIDRandomBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("random retention rule id: %w", err)
	}
	return "retention-" + hex.EncodeToString(raw), nil
}
