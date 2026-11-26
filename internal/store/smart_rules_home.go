package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	smartRuleGroupMaxDepth = 2
	smartRuleGroupMaxRules = 25
)

// SmartRuleGroup is a composable boolean group ("all", "any", "none") of
// leaf rules and nested groups (up to depth 2).
type SmartRuleGroup struct {
	Match      string          `json:"match"`
	Conditions []SmartRuleNode `json:"conditions"`
}

// SmartRuleNode is either a leaf rule (Field/Op/Value) or a nested Group.
type SmartRuleNode struct {
	Field string          `json:"field,omitempty"`
	Op    string          `json:"op,omitempty"`
	Value any             `json:"value,omitempty"`
	Group *SmartRuleGroup `json:"group,omitempty"`
}

type compiledRuleGroup struct {
	match      string // "all", "any", "none"
	conditions []compiledRuleNode
}

type compiledRuleNode struct {
	rule  *compiledRule
	group *compiledRuleGroup
}

type compiledRule struct {
	base    smartRule
	textIn  []string
	numLow  float64
	numHigh float64
}

var smartExtendedBuiltinFields = map[string]smartFieldClass{
	"title":       smartFieldText,
	"library":     smartFieldText,
	"duration":    smartFieldNumber,
	"year":        smartFieldNumber,
	"playCount":   smartFieldNumber,
	"rating":      smartFieldNumber,
	"lastPlayed":  smartFieldDate,
	"addedAt":     smartFieldDate,
	"watched":     smartFieldBoolean,
	"inProgress":  smartFieldBoolean,
	"favorited":   smartFieldBoolean,
	"inWatchlist": smartFieldBoolean,
}

const smartFieldBoolean smartFieldClass = 99

func smartExtendedFieldClassOf(field string) (smartFieldClass, string, bool) {
	if class, ok := smartExtendedBuiltinFields[field]; ok {
		return class, "", true
	}
	return smartFieldClassOf(field)
}

func compileSmartRuleGroup(group SmartRuleGroup) (compiledRuleGroup, error) {
	leafCount := 0
	return compileSmartRuleGroupDepth(group, 0, &leafCount)
}

func compileSmartRuleGroupDepth(group SmartRuleGroup, depth int, leafCount *int) (compiledRuleGroup, error) {
	if depth >= smartRuleGroupMaxDepth {
		return compiledRuleGroup{}, ValidationError(fmt.Sprintf("rule groups can nest at most %d levels", smartRuleGroupMaxDepth))
	}
	match := strings.TrimSpace(group.Match)
	if match == "" {
		match = "all"
	}
	if match != "all" && match != "any" && match != "none" {
		return compiledRuleGroup{}, ValidationError(`rule group match must be "all", "any", or "none"`)
	}
	out := compiledRuleGroup{
		match:      match,
		conditions: make([]compiledRuleNode, 0, len(group.Conditions)),
	}
	for i, cond := range group.Conditions {
		if cond.Group != nil {
			sub, err := compileSmartRuleGroupDepth(*cond.Group, depth+1, leafCount)
			if err != nil {
				return compiledRuleGroup{}, err
			}
			out.conditions = append(out.conditions, compiledRuleNode{group: &sub})
			continue
		}
		*leafCount++
		if *leafCount > smartRuleGroupMaxRules {
			return compiledRuleGroup{}, ValidationError(fmt.Sprintf("at most %d rules are allowed", smartRuleGroupMaxRules))
		}
		r, err := compileExtendedRule(strings.TrimSpace(cond.Field), strings.TrimSpace(cond.Op), cond.Value)
		if err != nil {
			return compiledRuleGroup{}, ValidationError(fmt.Sprintf("rule %d: %s", i+1, err.Error()))
		}
		out.conditions = append(out.conditions, compiledRuleNode{rule: &r})
	}
	return out, nil
}

func compileExtendedRule(field, op string, value any) (compiledRule, error) {
	class, _, ok := smartExtendedFieldClassOf(field)
	if !ok {
		return compiledRule{}, ValidationError("unknown field " + strconv.Quote(field))
	}
	if class == smartFieldBoolean {
		if !slices.Contains(smartFlagOps, op) {
			return compiledRule{}, ValidationError("operator " + strconv.Quote(op) + " does not apply to " + strconv.Quote(field))
		}
		return compiledRule{base: smartRule{field: field, op: op}}, nil
	}
	if (class == smartFieldText || class == smartFieldEntity || class == smartFieldPlugin) && (op == "in" || op == "notIn") {
		items, ok := parseStringListValue(value)
		if !ok || len(items) == 0 {
			return compiledRule{}, ValidationError(strconv.Quote(field) + " needs a non-empty list of values")
		}
		return compiledRule{base: smartRule{field: field, op: op}, textIn: items}, nil
	}
	if (class == smartFieldNumber || class == smartFieldPlugin) && op == "between" {
		low, high, ok := parseBetweenValue(value)
		if !ok {
			return compiledRule{}, ValidationError(strconv.Quote(field) + " between needs [min, max]")
		}
		return compiledRule{base: smartRule{field: field, op: op}, numLow: low, numHigh: high}, nil
	}
	base, err := decodeSmartRule(field, op, value)
	if err != nil {
		return compiledRule{}, err
	}
	return compiledRule{base: base}, nil
}

func parseStringListValue(v any) ([]string, bool) {
	rawList, ok := v.([]any)
	if !ok {
		if strs, ok2 := v.([]string); ok2 {
			out := make([]string, 0, len(strs))
			for _, s := range strs {
				s = strings.TrimSpace(s)
				if s != "" && len(s) <= smartRuleMaxValueLen {
					out = append(out, s)
				}
			}
			return out, len(out) > 0
		}
		return nil, false
	}
	out := make([]string, 0, len(rawList))
	for _, item := range rawList {
		s, ok := item.(string)
		s = strings.TrimSpace(s)
		if !ok || s == "" || len(s) > smartRuleMaxValueLen {
			return nil, false
		}
		out = append(out, s)
	}
	return out, len(out) > 0
}

func parseBetweenValue(v any) (float64, float64, bool) {
	rawList, ok := v.([]any)
	if !ok || len(rawList) != 2 {
		return 0, 0, false
	}
	low, ok1 := filterNumberValue(rawList[0])
	high, ok2 := filterNumberValue(rawList[1])
	if !ok1 || !ok2 || low > high {
		return 0, 0, false
	}
	return low, high, true
}

func (g compiledRuleGroup) usesEngagement(sort smartRuleSort) bool {
	if smartExtendedEngagementField(sort.by) {
		return true
	}
	for _, c := range g.conditions {
		if c.group != nil {
			if c.group.usesEngagement(smartRuleSort{}) {
				return true
			}
			continue
		}
		if c.rule != nil && smartExtendedEngagementField(c.rule.base.field) {
			return true
		}
	}
	return false
}

func smartExtendedEngagementField(field string) bool {
	return smartEngagementField(field) || field == "watched" || field == "inProgress" || field == "favorited" || field == "inWatchlist"
}

// EvaluateSmartRules evaluates a composable rule group across one or more
// accessible Libraries for a Profile, returning the paged items and total match count.
func (s *Store) EvaluateSmartRules(
	ctx context.Context,
	filter LibraryFilter,
	libraryIDs []LibraryID,
	profileID ProfileID,
	group SmartRuleGroup,
	sort HomeShelfSort,
	seed string,
	limit, offset int,
) ([]SourceItem, int, error) {
	compiled, err := compileSmartRuleGroup(group)
	if err != nil {
		return nil, 0, err
	}
	sortExpr, _ := json.Marshal(map[string]any{
		"by":  sort.By,
		"dir": map[bool]string{true: "desc", false: "asc"}[sort.Desc],
	})
	order, err := decodeSmartRuleSort(string(sortExpr))
	if err != nil {
		return nil, 0, err
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 20
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, 0, err
	}

	targetLibs, err := s.allowedHomeLibraryIDsLocked(ctx, filter, libraryIDs)
	if err != nil {
		return nil, 0, err
	}
	if len(targetLibs) == 0 {
		return []SourceItem{}, 0, nil
	}

	keep, err := s.smartRuleProfileFilterLocked(ctx, filter, profileID)
	if err != nil {
		return nil, 0, err
	}

	query, args, err := s.smartRuleGroupQueryLocked(ctx, filter, targetLibs, profileID, compiled, order)
	if err != nil {
		return nil, 0, err
	}

	matches, err := s.smartRuleScanLocked(ctx, query, args)
	if err != nil {
		return nil, 0, err
	}
	if order.by == "random" {
		keys := make(map[MediaID][32]byte, len(matches))
		for _, item := range matches {
			keys[item.ID] = sha256.Sum256([]byte(seed + "\x00" + string(item.ID)))
		}
		slices.SortFunc(matches, func(a, b SourceItem) int {
			ka, kb := keys[a.ID], keys[b.ID]
			return bytes.Compare(ka[:], kb[:])
		})
	}
	if keep != nil {
		var err error
		matches, err = smartRuleKeep(matches, len(matches), keep)
		if err != nil {
			return nil, 0, err
		}
	}
	total := len(matches)
	return pageOf(matches, limit, offset), total, nil
}

// FilterMediaByRules filters an existing slice of SourceItems using a SmartRuleGroup,
// preserving the input slice's original order. Non-media items are preserved as-is.
func (s *Store) FilterMediaByRules(
	ctx context.Context,
	filter LibraryFilter,
	profileID ProfileID,
	items []SourceItem,
	group SmartRuleGroup,
) ([]SourceItem, error) {
	if len(items) == 0 || len(group.Conditions) == 0 {
		return items, nil
	}
	compiled, err := compileSmartRuleGroup(group)
	if err != nil {
		return nil, err
	}
	mediaIDs := make([]MediaID, 0, len(items))
	for _, it := range items {
		if it.Kind == "" || it.Kind == "media" {
			mediaIDs = append(mediaIDs, it.ID)
		}
	}
	if len(mediaIDs) == 0 {
		return items, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}

	var args []any
	with, from := "", ` FROM media m`
	if compiled.usesEngagement(smartRuleSort{}) {
		rows, err := s.smartRuleEngagementExtendedJSON(ctx, profileID)
		if err != nil {
			return nil, err
		}
		with = `WITH eng(media_id, play_count, last_played_ms, rating, watched, in_progress, favorited, in_watchlist) AS MATERIALIZED (
			SELECT json_extract(value, '$[0]'), json_extract(value, '$[1]'), json_extract(value, '$[2]'), json_extract(value, '$[3]'),
			       json_extract(value, '$[4]'), json_extract(value, '$[5]'), json_extract(value, '$[6]'), json_extract(value, '$[7]')
			FROM json_each(?)
		)
		`
		from = ` FROM eng e RIGHT JOIN media m ON m.id = e.media_id`
		args = append(args, rows)
	}
	placeholders := make([]string, len(mediaIDs))
	for i, id := range mediaIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	whereSQL, whereArgs := compileRuleGroupSQL(compiled, s.serverNow())
	args = append(args, whereArgs...)
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	args = append(args, certArgs...)

	// #nosec G202 -- placeholders and fixed SQL fragments only; all values bound.
	query := with + `SELECT m.id` + from + `
		WHERE m.id IN (` + strings.Join(placeholders, ",") + `)
		  AND m.trashed_at_ms IS NULL` + whereSQL + certClause
	rows, err := s.catalogR.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	matched := make(map[MediaID]bool, len(mediaIDs))
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		matched[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]SourceItem, 0, len(items))
	for _, it := range items {
		if (it.Kind != "" && it.Kind != "media") || matched[it.ID] {
			out = append(out, it)
		}
	}
	return out, nil
}

func (s *Store) allowedHomeLibraryIDsLocked(ctx context.Context, filter LibraryFilter, requested []LibraryID) ([]LibraryID, error) {
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	reqSet := make(map[LibraryID]bool, len(requested))
	for _, id := range requested {
		reqSet[id] = true
	}
	var allowed []LibraryID
	for _, lib := range libraries {
		if !libraryVisible(lib, filter) {
			continue
		}
		if len(requested) > 0 && !reqSet[lib.ID] {
			continue
		}
		if len(requested) == 0 && !lib.ShowOnHome {
			continue
		}
		allowed = append(allowed, lib.ID)
	}
	return allowed, nil
}

func (s *Store) smartRuleGroupQueryLocked(
	ctx context.Context,
	filter LibraryFilter,
	libraryIDs []LibraryID,
	profileID ProfileID,
	compiled compiledRuleGroup,
	order smartRuleSort,
) (string, []any, error) {
	var args []any
	with, from := "", ` FROM media m`
	if compiled.usesEngagement(order) {
		rows, err := s.smartRuleEngagementExtendedJSON(ctx, profileID)
		if err != nil {
			return "", nil, err
		}
		with = `WITH eng(media_id, play_count, last_played_ms, rating, watched, in_progress, favorited, in_watchlist) AS MATERIALIZED (
			SELECT json_extract(value, '$[0]'), json_extract(value, '$[1]'), json_extract(value, '$[2]'), json_extract(value, '$[3]'),
			       json_extract(value, '$[4]'), json_extract(value, '$[5]'), json_extract(value, '$[6]'), json_extract(value, '$[7]')
			FROM json_each(?)
		)
		`
		from = ` FROM eng e RIGHT JOIN media m ON m.id = e.media_id`
		args = append(args, rows)
	}
	libPlaceholders := make([]string, len(libraryIDs))
	for i, id := range libraryIDs {
		libPlaceholders[i] = "?"
		args = append(args, id)
	}
	whereSQL, whereArgs := compileRuleGroupSQL(compiled, s.serverNow())
	args = append(args, whereArgs...)
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	args = append(args, certArgs...)

	// #nosec G202 -- fixed SQL fragments and bound placeholders only.
	query := with + `SELECT m.id, m.title, m.library_id, m.duration_seconds` + from + `
		WHERE m.library_id IN (` + strings.Join(libPlaceholders, ",") + `)
		  AND m.trashed_at_ms IS NULL` + mediaExcludeExtrasClause + `
		  AND (NOT EXISTS (SELECT 1 FROM media_file f WHERE f.media_id = m.id)
		       OR EXISTS (SELECT 1 FROM media_file f WHERE f.media_id = m.id AND f.missing_at_ms IS NULL))` + whereSQL + certClause
	if order.by != "random" {
		orderSQL, orderArgs := smartRuleOrderSQL(order)
		query += orderSQL
		args = append(args, orderArgs...)
	}
	return query, args, nil
}

func compileRuleGroupSQL(group compiledRuleGroup, now time.Time) (string, []any) {
	clause, args := compileRuleGroupExpr(group, now)
	if clause == "" {
		return "", nil
	}
	return "\n\t\t  AND (" + clause + ")", args
}

func compileRuleGroupExpr(group compiledRuleGroup, now time.Time) (string, []any) {
	if len(group.conditions) == 0 {
		return "", nil
	}
	joiner := " AND "
	if group.match == "any" || group.match == "none" {
		joiner = " OR "
	}
	parts := make([]string, 0, len(group.conditions))
	var args []any
	for _, cond := range group.conditions {
		var part string
		var partArgs []any
		if cond.group != nil {
			part, partArgs = compileRuleGroupExpr(*cond.group, now)
		} else if cond.rule != nil {
			part, partArgs = compileExtendedRuleSQL(*cond.rule, now)
		}
		if part != "" {
			parts = append(parts, "("+part+")")
			args = append(args, partArgs...)
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	joined := strings.Join(parts, joiner)
	if group.match == "none" {
		return "NOT (" + joined + ")", args
	}
	return joined, args
}

func compileExtendedRuleSQL(r compiledRule, now time.Time) (string, []any) {
	rule := r.base
	class, name, _ := smartExtendedFieldClassOf(rule.field)
	switch class {
	case smartFieldBoolean:
		col := map[string]string{
			"watched":     "COALESCE(e.watched, 0) <> 0",
			"inProgress":  "COALESCE(e.in_progress, 0) <> 0",
			"favorited":   "COALESCE(e.favorited, 0) <> 0",
			"inWatchlist": "COALESCE(e.in_watchlist, 0) <> 0",
		}[rule.field]
		if rule.op == "isFalse" {
			return "NOT (" + col + ")", nil
		}
		return col, nil
	case smartFieldText:
		col := "m.title"
		if rule.field == "library" {
			col = "m.library_id"
		}
		if rule.op == "in" || rule.op == "notIn" {
			return smartTextInSQL(col, rule.op == "notIn", r.textIn)
		}
		return smartTextPredicateSQL(col, rule.op, rule.text)
	case smartFieldNumber:
		expr := map[string]string{
			"duration":  "m.duration_seconds",
			"year":      "COALESCE(m.year, 0)",
			"playCount": "COALESCE(e.play_count, 0)",
			"rating":    "COALESCE(e.rating, 0)",
		}[rule.field]
		if rule.op == "between" {
			return expr + " BETWEEN ? AND ?", []any{r.numLow, r.numHigh}
		}
		return expr + " " + smartComparison[rule.op] + " ?", []any{rule.num}
	case smartFieldDate:
		expr := "e.last_played_ms"
		if rule.field == "addedAt" {
			expr = "(SELECT MIN(f.last_seen_at_ms) FROM media_file f WHERE f.media_id = m.id AND f.missing_at_ms IS NULL)"
		}
		return smartDatePredicateSQL(expr, rule, now)
	case smartFieldEntity:
		if rule.op == "in" || rule.op == "notIn" {
			pred, predArgs := smartTextInSQL("e2.title", false, r.textIn)
			clause := `EXISTS (SELECT 1 FROM entity_relation r2 JOIN entity e2 ON e2.id = r2.from_entity_id
				WHERE r2.to_media_id = m.id AND e2.kind = ? AND e2.library_id = m.library_id AND e2.trashed_at_ms IS NULL AND ` + pred + `)`
			return smartNegate(clause, rule.op == "notIn"), append([]any{name}, predArgs...)
		}
		positive, negate := smartPositiveOp(rule.op)
		pred, predArgs := smartTextPredicateSQL("e2.title", positive, rule.text)
		clause := `EXISTS (SELECT 1 FROM entity_relation r2 JOIN entity e2 ON e2.id = r2.from_entity_id
			WHERE r2.to_media_id = m.id AND e2.kind = ? AND e2.library_id = m.library_id AND e2.trashed_at_ms IS NULL AND ` + pred + `)`
		return smartNegate(clause, negate), append([]any{name}, predArgs...)
	default:
		if rule.op == "in" || rule.op == "notIn" {
			pred, predArgs := smartTextInSQL("f.value", false, r.textIn)
			clause := `EXISTS (SELECT 1 FROM media_field_text f WHERE f.media_id = m.id AND f.field = ? AND ` + pred + `)`
			return smartNegate(clause, rule.op == "notIn"), append([]any{name}, predArgs...)
		}
		if rule.op == "between" {
			clause := `EXISTS (SELECT 1 FROM media_field_real f WHERE f.media_id = m.id AND f.field = ? AND f.value BETWEEN ? AND ?)`
			return clause, []any{name, r.numLow, r.numHigh}
		}
		return smartPluginPredicateSQL(name, rule, now)
	}
}

func smartTextInSQL(expr string, negate bool, values []string) (string, []any) {
	placeholders := make([]string, len(values))
	args := make([]any, len(values))
	for i, v := range values {
		placeholders[i] = "?"
		args[i] = v
	}
	op := " IN "
	if negate {
		op = " NOT IN "
	}
	return expr + " COLLATE NOCASE" + op + "(" + strings.Join(placeholders, ",") + ")", args
}

// smartRuleEngagementExtendedJSON loads per-Profile engagement (play count,
// last played, rating, watched, in_progress, favorited, in_watchlist) into
// one JSON array for CTE join with catalog.
func (s *Store) smartRuleEngagementExtendedJSON(ctx context.Context, profileID ProfileID) (string, error) {
	if profileID == "" {
		return "[]", nil
	}
	type row struct {
		playCount   int64
		lastPlayed  *int64
		rating      int64
		watched     int64
		inProgress  int64
		favorited   int64
		inWatchlist int64
	}
	byID := map[string]*row{}
	order := []string{}
	get := func(id string) *row {
		r, ok := byID[id]
		if !ok {
			r = &row{}
			byID[id] = r
			order = append(order, id)
		}
		return r
	}
	plays, err := s.eventsR.QueryContext(ctx, `
		SELECT media_id, play_count, last_played_at_ms, watched, position_seconds
		FROM playback_progress
		WHERE profile_id = ? AND (play_count > 0 OR last_played_at_ms IS NOT NULL OR watched <> 0 OR position_seconds > 0)
		LIMIT ?
	`, profileID, smartRuleEngagementCap+1)
	if err != nil {
		return "", err
	}
	for plays.Next() {
		if len(order) == smartRuleEngagementCap {
			_ = plays.Close()
			return "", errSmartRuleEngagementCap
		}
		var id string
		var playCount, watched, pos int64
		var lastPlayed *int64
		if err := plays.Scan(&id, &playCount, &lastPlayed, &watched, &pos); err != nil {
			_ = plays.Close()
			return "", err
		}
		r := get(id)
		r.playCount, r.lastPlayed = playCount, lastPlayed
		if watched != 0 {
			r.watched = 1
		} else if pos > 0 {
			r.inProgress = 1
		}
	}
	if err := plays.Close(); err != nil {
		return "", err
	}
	if err := plays.Err(); err != nil {
		return "", err
	}

	ratings, err := s.eventsR.QueryContext(ctx, `
		SELECT target_id, rating
		FROM item_rating
		WHERE profile_id = ? AND target_kind = 'media'
		LIMIT ?
	`, profileID, smartRuleEngagementCap+1)
	if err != nil {
		return "", err
	}
	for rated := 0; ratings.Next(); rated++ {
		if rated == smartRuleEngagementCap {
			_ = ratings.Close()
			return "", errSmartRuleEngagementCap
		}
		var id string
		var rating int64
		if err := ratings.Scan(&id, &rating); err != nil {
			_ = ratings.Close()
			return "", err
		}
		get(id).rating = rating
	}
	if err := ratings.Close(); err != nil {
		return "", err
	}
	if err := ratings.Err(); err != nil {
		return "", err
	}

	favs, err := s.eventsR.QueryContext(ctx, `
		SELECT media_id FROM media_favorite WHERE profile_id = ? LIMIT ?
	`, profileID, smartRuleEngagementCap+1)
	if err != nil {
		return "", err
	}
	for favs.Next() {
		var id string
		if err := favs.Scan(&id); err != nil {
			_ = favs.Close()
			return "", err
		}
		get(id).favorited = 1
	}
	if err := favs.Close(); err != nil {
		return "", err
	}
	if err := favs.Err(); err != nil {
		return "", err
	}

	wls, err := s.eventsR.QueryContext(ctx, `
		SELECT target_id FROM watchlist_item WHERE profile_id = ? AND target_kind = 'media' LIMIT ?
	`, profileID, smartRuleEngagementCap+1)
	if err != nil {
		return "", err
	}
	for wls.Next() {
		var id string
		if err := wls.Scan(&id); err != nil {
			_ = wls.Close()
			return "", err
		}
		get(id).inWatchlist = 1
	}
	if err := wls.Close(); err != nil {
		return "", err
	}
	if err := wls.Err(); err != nil {
		return "", err
	}

	out := make([][8]any, 0, len(order))
	for _, id := range order {
		r := byID[id]
		var lastPlayed any
		if r.lastPlayed != nil {
			lastPlayed = *r.lastPlayed
		}
		out = append(out, [8]any{id, r.playCount, lastPlayed, r.rating, r.watched, r.inProgress, r.favorited, r.inWatchlist})
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
