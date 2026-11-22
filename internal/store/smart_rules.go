package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Rule sources for Smart Collections (CORE-2771). A Smart Collection whose
// sourceExpr has kind "rules" is a rule-based list: up to smartRuleMaxRules
// rules over generic Media fields, joined with match all/any, sorted by its
// sortExpr and capped by its limit. It is evaluated on every read, for the
// reading Profile, so it follows library and play-history changes without a
// refresh job. Field names are generic: built-in Media columns, per-Profile
// engagement, plugin Media fields ("field:<name>") and plugin entity
// relations ("entity:<kind>"); core never names a domain kind.
const (
	smartRuleSourceKind = "rules"
	smartRuleMaxRules   = 20
	smartRuleMaxLimit   = 5000
	// smartRuleScanCap bounds the match set a random sort shuffles in Go.
	smartRuleScanCap = 50000
	// smartRuleEngagementCap bounds the Profile engagement rows read from the
	// events database for one evaluation.
	smartRuleEngagementCap = 250000
	smartRuleMaxValueLen   = 500
	smartRuleMaxDays       = 36500
)

type smartRuleSet struct {
	matchAny bool
	rules    []smartRule
	limit    int
}

type smartRule struct {
	field string
	op    string
	text  string
	num   float64
	when  int64 // epoch ms for before/after
	days  int   // inLast/notInLast
}

type smartRuleSort struct {
	by   string
	desc bool
}

type smartFieldClass int

const (
	smartFieldText smartFieldClass = iota
	smartFieldNumber
	smartFieldDate
	smartFieldPlugin
	smartFieldEntity
)

var smartBuiltinFields = map[string]smartFieldClass{
	"title":      smartFieldText,
	"duration":   smartFieldNumber,
	"year":       smartFieldNumber,
	"playCount":  smartFieldNumber,
	"rating":     smartFieldNumber,
	"lastPlayed": smartFieldDate,
}

var (
	smartTextOps   = []string{"is", "isNot", "contains", "notContains", "beginsWith"}
	smartNumberOps = []string{"is", "isNot", "gt", "gte", "lt", "lte"}
	smartDateOps   = []string{"inLast", "notInLast", "before", "after"}
	smartFlagOps   = []string{"isTrue", "isFalse"}
	smartSortKeys  = []string{"title", "duration", "year", "added", "playCount", "lastPlayed", "rating", "random"}
)

func smartFieldClassOf(field string) (smartFieldClass, string, bool) {
	if class, ok := smartBuiltinFields[field]; ok {
		return class, "", true
	}
	if name, ok := strings.CutPrefix(field, "field:"); ok && strings.TrimSpace(name) != "" {
		return smartFieldPlugin, strings.TrimSpace(name), true
	}
	if kind, ok := strings.CutPrefix(field, "entity:"); ok && strings.TrimSpace(kind) != "" {
		return smartFieldEntity, strings.TrimSpace(kind), true
	}
	return 0, "", false
}

// isSmartRuleSourceExpr reports whether a stored sourceExpr is a rule source.
// A sourceExpr that is not valid JSON is not one; the generic decoder reports it.
func isSmartRuleSourceExpr(expr string) bool {
	var head struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(expr), &head); err != nil {
		return false
	}
	return strings.TrimSpace(head.Kind) == smartRuleSourceKind
}

func decodeSmartRuleSet(expr string) (smartRuleSet, error) {
	var raw struct {
		Kind  string `json:"kind"`
		Match string `json:"match"`
		Rules []struct {
			Field string `json:"field"`
			Op    string `json:"op"`
			Value any    `json:"value"`
		} `json:"rules"`
		Limit *json.Number `json:"limit"`
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(expr)))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return smartRuleSet{}, ValidationError("sourceExpr is not valid JSON")
	}
	set := smartRuleSet{limit: smartRuleMaxLimit}
	switch raw.Match {
	case "", "all":
	case "any":
		set.matchAny = true
	default:
		return smartRuleSet{}, ValidationError(`rules match must be "all" or "any"`)
	}
	if len(raw.Rules) > smartRuleMaxRules {
		return smartRuleSet{}, ValidationError("a rule source allows at most " + strconv.Itoa(smartRuleMaxRules) + " rules")
	}
	if raw.Limit != nil {
		limit, err := raw.Limit.Int64()
		if err != nil || limit < 1 || limit > smartRuleMaxLimit {
			return smartRuleSet{}, ValidationError("rules limit must be between 1 and " + strconv.Itoa(smartRuleMaxLimit))
		}
		set.limit = int(limit)
	}
	for i, r := range raw.Rules {
		rule, err := decodeSmartRule(strings.TrimSpace(r.Field), strings.TrimSpace(r.Op), r.Value)
		if err != nil {
			return smartRuleSet{}, ValidationError("rule " + strconv.Itoa(i+1) + ": " + err.Error())
		}
		set.rules = append(set.rules, rule)
	}
	return set, nil
}

func decodeSmartRule(field, op string, value any) (smartRule, error) {
	class, _, ok := smartFieldClassOf(field)
	if !ok {
		return smartRule{}, ValidationError("unknown field " + strconv.Quote(field))
	}
	rule := smartRule{field: field, op: op}
	var allowed []string
	switch class {
	case smartFieldText, smartFieldEntity:
		allowed = smartTextOps
	case smartFieldNumber:
		allowed = smartNumberOps
	case smartFieldDate:
		allowed = smartDateOps
	case smartFieldPlugin:
		allowed = slices.Concat(smartTextOps, smartNumberOps[2:], smartDateOps, smartFlagOps)
	}
	if !slices.Contains(allowed, op) {
		return smartRule{}, ValidationError("operator " + strconv.Quote(op) + " does not apply to " + strconv.Quote(field))
	}
	switch {
	case slices.Contains(smartFlagOps, op):
		return rule, nil
	case op == "inLast" || op == "notInLast":
		days, ok := filterNumberValue(value)
		if !ok || days < 1 || days > smartRuleMaxDays || days != float64(int(days)) {
			return smartRule{}, ValidationError(op + " needs a whole number of days")
		}
		rule.days = int(days)
	case op == "before" || op == "after":
		when, ok := parseFilterTime(value)
		if !ok {
			return smartRule{}, ValidationError(op + " needs a date")
		}
		rule.when = when.UnixMilli()
	case class == smartFieldNumber || slices.Contains(smartNumberOps[2:], op):
		number, ok := filterNumberValue(value)
		if !ok {
			return smartRule{}, ValidationError(strconv.Quote(field) + " needs a number")
		}
		rule.num = number
	case class == smartFieldPlugin && (op == "is" || op == "isNot"):
		// A plugin field compares as a number when the value is one.
		if number, ok := filterNumberValue(value); ok {
			rule.num = number
			rule.op = op + "#"
			return rule, nil
		}
		fallthrough
	default:
		text, ok := value.(string)
		text = strings.TrimSpace(text)
		if !ok || text == "" || len(text) > smartRuleMaxValueLen {
			return smartRule{}, ValidationError(strconv.Quote(field) + " needs a text value")
		}
		rule.text = text
	}
	return rule, nil
}

func decodeSmartRuleSort(expr string) (smartRuleSort, error) {
	expr = strings.TrimSpace(expr)
	sort := smartRuleSort{by: "title"}
	if expr == "" || expr == "{}" {
		return sort, nil
	}
	var raw struct {
		By  string `json:"by"`
		Dir string `json:"dir"`
	}
	if err := json.Unmarshal([]byte(expr), &raw); err != nil {
		return smartRuleSort{}, ValidationError("sortExpr is not valid JSON")
	}
	by := strings.TrimSpace(raw.By)
	switch {
	case by == "":
	case slices.Contains(smartSortKeys, by):
		sort.by = by
	case strings.HasPrefix(by, "field:") && strings.TrimSpace(strings.TrimPrefix(by, "field:")) != "":
		sort.by = by
	default:
		return smartRuleSort{}, ValidationError("unknown sort key " + strconv.Quote(by))
	}
	switch raw.Dir {
	case "", "asc":
	case "desc":
		sort.desc = true
	default:
		return smartRuleSort{}, ValidationError(`sort dir must be "asc" or "desc"`)
	}
	return sort, nil
}

// validateSmartRuleExprs checks a rule source and its sort before they are
// stored. Other source kinds keep their existing, read-time validation.
func validateSmartRuleExprs(sourceExpr, sortExpr string) error {
	if !isSmartRuleSourceExpr(sourceExpr) {
		return nil
	}
	if _, err := decodeSmartRuleSet(sourceExpr); err != nil {
		return err
	}
	_, err := decodeSmartRuleSort(sortExpr)
	return err
}

func (r smartRuleSet) usesEngagement(sort smartRuleSort) bool {
	if smartEngagementField(sort.by) {
		return true
	}
	for _, rule := range r.rules {
		if smartEngagementField(rule.field) {
			return true
		}
	}
	return false
}

func smartEngagementField(field string) bool {
	return field == "playCount" || field == "lastPlayed" || field == "rating"
}

// smartRuleItemsLocked evaluates a rule source for the Library the caller
// already narrowed filter to. It returns the [offset, offset+limit) window of
// the sorted match list, which the rule limit caps as a whole.
func (s *Store) smartRuleItemsLocked(ctx context.Context, filter LibraryFilter, libraryID LibraryID, profileID ProfileID, collectionID SmartCollectionID, sourceExpr, sortExpr string, limit, offset int) ([]SourceItem, error) {
	set, err := decodeSmartRuleSet(sourceExpr)
	if err != nil {
		return nil, err
	}
	order, err := decodeSmartRuleSort(sortExpr)
	if err != nil {
		return nil, err
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= set.limit || limit <= 0 {
		return []SourceItem{}, nil
	}
	limit = min(limit, set.limit-offset)
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	visible := false
	for _, library := range libraries {
		if library.ID == libraryID && libraryVisible(library, filter) {
			visible = true
		}
	}
	if !visible {
		return []SourceItem{}, nil
	}

	var args []any
	var with, join string
	if set.usesEngagement(order) {
		rows, err := s.smartRuleEngagementJSON(ctx, profileID)
		if err != nil {
			return nil, err
		}
		with = `WITH eng(media_id, play_count, last_played_ms, rating) AS MATERIALIZED (
			SELECT json_extract(value, '$[0]'), json_extract(value, '$[1]'), json_extract(value, '$[2]'), json_extract(value, '$[3]')
			FROM json_each(?)
		)
		`
		join = ` LEFT JOIN eng e ON e.media_id = m.id`
		args = append(args, rows)
	}
	args = append(args, libraryID)
	where, whereArgs := smartRuleWhereSQL(set, libraryID, s.serverNow())
	args = append(args, whereArgs...)
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	args = append(args, certArgs...)

	// #nosec G202 -- only fixed SQL fragments and generated placeholders are joined; every value is bound.
	query := with + `SELECT m.id, m.title, m.library_id, m.duration_seconds
		FROM media m` + join + `
		WHERE m.library_id = ?
		  AND m.trashed_at_ms IS NULL` + mediaExcludeExtrasClause + `
		  AND (NOT EXISTS (SELECT 1 FROM media_file f WHERE f.media_id = m.id)
		       OR EXISTS (SELECT 1 FROM media_file f WHERE f.media_id = m.id AND f.missing_at_ms IS NULL))` + where + certClause
	random := order.by == "random"
	if random {
		query += ` LIMIT ?`
		args = append(args, smartRuleScanCap)
	} else {
		orderSQL, orderArgs := smartRuleOrderSQL(order)
		query += orderSQL + ` LIMIT ? OFFSET ?`
		args = append(args, orderArgs...)
		args = append(args, limit, offset)
	}
	rows, err := s.catalogR.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SourceItem{}
	for rows.Next() {
		var item SourceItem
		var durationSeconds int
		if err := rows.Scan(&item.ID, &item.Title, &item.LibraryID, &durationSeconds); err != nil {
			return nil, err
		}
		item.Kind = "media"
		item.Summary = mediaDurationSummary(durationSeconds)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !random {
		return items, nil
	}
	// A daily stable shuffle: pages of the same day agree, the next day reshuffles.
	seed := string(collectionID) + "\x00" + s.serverNow().UTC().Format(time.DateOnly)
	keys := make(map[MediaID][32]byte, len(items))
	for _, item := range items {
		keys[item.ID] = sha256.Sum256([]byte(seed + "\x00" + string(item.ID)))
	}
	slices.SortFunc(items, func(a, b SourceItem) int {
		ka, kb := keys[a.ID], keys[b.ID]
		return bytes.Compare(ka[:], kb[:])
	})
	items = items[:min(len(items), set.limit)]
	if offset >= len(items) {
		return []SourceItem{}, nil
	}
	return items[offset:min(len(items), offset+limit)], nil
}

// smartRuleEngagementJSON reads the Profile's play counts, last-played times
// and media ratings from the events database as one JSON array of
// [mediaID, playCount, lastPlayedMS|null, rating] rows. The catalog query
// joins it as a CTE, since the two databases cannot be joined directly. An
// empty Profile has no engagement.
func (s *Store) smartRuleEngagementJSON(ctx context.Context, profileID ProfileID) (string, error) {
	if profileID == "" {
		return "[]", nil
	}
	type row struct {
		playCount  int64
		lastPlayed *int64
		rating     int64
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
		SELECT media_id, play_count, last_played_at_ms
		FROM playback_progress
		WHERE profile_id = ? AND (play_count > 0 OR last_played_at_ms IS NOT NULL)
		LIMIT ?
	`, profileID, smartRuleEngagementCap)
	if err != nil {
		return "", err
	}
	for plays.Next() {
		var id string
		var playCount int64
		var lastPlayed *int64
		if err := plays.Scan(&id, &playCount, &lastPlayed); err != nil {
			_ = plays.Close()
			return "", err
		}
		r := get(id)
		r.playCount, r.lastPlayed = playCount, lastPlayed
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
	`, profileID, smartRuleEngagementCap)
	if err != nil {
		return "", err
	}
	for ratings.Next() {
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
	out := make([][4]any, 0, len(order))
	for _, id := range order {
		r := byID[id]
		var lastPlayed any
		if r.lastPlayed != nil {
			lastPlayed = *r.lastPlayed
		}
		out = append(out, [4]any{id, r.playCount, lastPlayed, r.rating})
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// smartRuleWhereSQL compiles the rules into one AND clause for the media
// query, joined internally with AND (match all) or OR (match any).
func smartRuleWhereSQL(set smartRuleSet, libraryID LibraryID, now time.Time) (string, []any) {
	if len(set.rules) == 0 {
		return "", nil
	}
	joiner := " AND "
	if set.matchAny {
		joiner = " OR "
	}
	parts := make([]string, 0, len(set.rules))
	var args []any
	for _, rule := range set.rules {
		clause, ruleArgs := smartRulePredicateSQL(rule, libraryID, now)
		parts = append(parts, "("+clause+")")
		args = append(args, ruleArgs...)
	}
	return "\n\t\t  AND (" + strings.Join(parts, joiner) + ")", args
}

var smartComparison = map[string]string{"is": "=", "isNot": "<>", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}

func smartRulePredicateSQL(rule smartRule, libraryID LibraryID, now time.Time) (string, []any) {
	class, name, _ := smartFieldClassOf(rule.field)
	switch class {
	case smartFieldText:
		return smartTextPredicateSQL("m.title", rule.op, rule.text)
	case smartFieldNumber:
		expr := map[string]string{
			"duration":  "m.duration_seconds",
			"year":      "COALESCE(m.year, 0)",
			"playCount": "COALESCE(e.play_count, 0)",
			"rating":    "COALESCE(e.rating, 0)",
		}[rule.field]
		return expr + " " + smartComparison[rule.op] + " ?", []any{rule.num}
	case smartFieldDate:
		return smartDatePredicateSQL("e.last_played_ms", rule, now)
	case smartFieldEntity:
		positive, negate := smartPositiveOp(rule.op)
		pred, predArgs := smartTextPredicateSQL("e2.title", positive, rule.text)
		clause := `EXISTS (SELECT 1 FROM entity_relation r2 JOIN entity e2 ON e2.id = r2.from_entity_id
			WHERE r2.to_media_id = m.id AND e2.kind = ? AND e2.library_id = ? AND e2.trashed_at_ms IS NULL AND ` + pred + `)`
		return smartNegate(clause, negate), append([]any{name, libraryID}, predArgs...)
	default:
		return smartPluginPredicateSQL(name, rule, now)
	}
}

// smartPositiveOp maps a negated operator to its positive form so plugin
// fields and relations negate with NOT EXISTS: an item without the value
// matches "is not".
func smartPositiveOp(op string) (string, bool) {
	switch op {
	case "isNot":
		return "is", true
	case "notContains":
		return "contains", true
	case "isNot#":
		return "is#", true
	case "isFalse":
		return "isTrue", true
	case "notInLast":
		return "inLast", true
	}
	return op, false
}

func smartNegate(clause string, negate bool) string {
	if negate {
		return "NOT " + clause
	}
	return clause
}

func smartTextPredicateSQL(expr, op, value string) (string, []any) {
	switch op {
	case "isNot":
		return expr + " <> ? COLLATE NOCASE", []any{value}
	case "contains":
		return "instr(lower(" + expr + "), lower(?)) > 0", []any{value}
	case "notContains":
		return "instr(lower(" + expr + "), lower(?)) = 0", []any{value}
	case "beginsWith":
		return "instr(lower(" + expr + "), lower(?)) = 1", []any{value}
	default:
		return expr + " = ? COLLATE NOCASE", []any{value}
	}
}

func smartDatePredicateSQL(expr string, rule smartRule, now time.Time) (string, []any) {
	switch rule.op {
	case "inLast":
		return expr + " >= ?", []any{now.Add(-time.Duration(rule.days) * 24 * time.Hour).UnixMilli()}
	case "notInLast":
		return "(" + expr + " IS NULL OR " + expr + " < ?)", []any{now.Add(-time.Duration(rule.days) * 24 * time.Hour).UnixMilli()}
	case "before":
		return expr + " < ?", []any{rule.when}
	default:
		return expr + " > ?", []any{rule.when}
	}
}

func smartPluginPredicateSQL(name string, rule smartRule, now time.Time) (string, []any) {
	positive, negate := smartPositiveOp(rule.op)
	var table, pred string
	var predArgs []any
	switch {
	case positive == "isTrue":
		table, pred = "media_field_int", "f.value <> 0"
	case slices.Contains(smartDateOps, positive):
		table = "media_field_date"
		pred, predArgs = smartDatePredicateSQL("f.value_ms", smartRule{op: positive, days: rule.days, when: rule.when}, now)
	case positive == "is#" || smartComparison[positive] != "" && positive != "is":
		table = "media_field_real"
		pred, predArgs = "f.value "+smartComparison[strings.TrimSuffix(positive, "#")]+" ?", []any{rule.num}
	default:
		table = "media_field_text"
		pred, predArgs = smartTextPredicateSQL("f.value", positive, rule.text)
	}
	clause := "EXISTS (SELECT 1 FROM " + table + " f WHERE f.media_id = m.id AND f.field = ? AND " + pred + ")"
	return smartNegate(clause, negate), append([]any{name}, predArgs...)
}

// smartRuleOrderSQL orders by the sort key with missing values last and the
// Media id as the tiebreaker, so pages are stable.
func smartRuleOrderSQL(order smartRuleSort) (string, []any) {
	var expr string
	var args []any
	switch order.by {
	case "duration":
		expr = "m.duration_seconds"
	case "year":
		expr = "m.year"
	case "added":
		expr = "(SELECT q.seq FROM media_insert_seq q WHERE q.media_id = m.id)"
	case "playCount":
		expr = "COALESCE(e.play_count, 0)"
	case "lastPlayed":
		expr = "e.last_played_ms"
	case "rating":
		expr = "COALESCE(e.rating, 0)"
	case "title":
		expr = "m.title COLLATE NOCASE"
	default:
		name := strings.TrimSpace(strings.TrimPrefix(order.by, "field:"))
		expr = `COALESCE(
			(SELECT f.value FROM media_field_real f WHERE f.media_id = m.id AND f.field = ?),
			(SELECT f.value_ms FROM media_field_date f WHERE f.media_id = m.id AND f.field = ?),
			(SELECT f.value FROM media_field_int f WHERE f.media_id = m.id AND f.field = ?),
			(SELECT f.value FROM media_field_text f WHERE f.media_id = m.id AND f.field = ?))`
		args = []any{name, name, name, name}
	}
	dir := " ASC"
	if order.desc {
		dir = " DESC"
	}
	// The key expression appears twice (IS NULL, then the value), so its
	// arguments do too.
	return "\n\t\tORDER BY (" + expr + ") IS NULL, " + expr + dir + ", m.id", slices.Concat(args, args)
}
