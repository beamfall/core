package store

import "strings"

// EntityBrowseSource matches an incoming relation from an Entity of FromKind,
// or, when FromKind is empty, an outgoing relation to Media. ExcludeMediaRelation
// requires that Media to have no incoming Entity relation of the given name.
// Attribute and Value, set together, require the relation's Attribute to list
// Value among its comma-separated values: a Person credited "author, narrator"
// matches Value "author" (CORE-3014).
type EntityBrowseSource struct {
	Relation             string
	FromKind             string
	ExcludeMediaRelation string
	Attribute            string
	Value                string
}

// attributeValueSQL matches one value of a relation's comma-separated
// attribute, ignoring spaces, so "author" matches "narrator, author" and not
// "co-author".
func attributeValueSQL(source EntityBrowseSource) (string, []any) {
	if source.Attribute == "" {
		return "", nil
	}
	path := `$."` + strings.ReplaceAll(source.Attribute, `"`, ``) + `"`
	value := "," + strings.ReplaceAll(source.Value, " ", "") + ","
	return ` AND instr(',' || REPLACE(COALESCE(CASE WHEN json_valid(r.attributes) THEN json_extract(r.attributes, ?) END, ''), ' ', '') || ',', ?) > 0`, []any{path, value}
}

// entityBrowseSourceSQL builds the eligibility predicate for the Entity alias e.
// Each subquery is a CROSS JOIN so SQLite keeps the relation as the outer loop
// and seeks it by e.id: left free, the planner can walk every source-kind
// Entity of the Library for each row of e instead (CORE-2887).
func entityBrowseSourceSQL(sources []EntityBrowseSource) (string, []any) {
	if len(sources) == 0 {
		return "", nil
	}
	clauses := make([]string, 0, len(sources))
	var args []any
	for _, source := range sources {
		if source.FromKind != "" {
			clause := `EXISTS (
				SELECT 1 FROM entity_relation r CROSS JOIN entity src ON src.id = r.from_entity_id
				WHERE r.to_entity_id = e.id AND r.relation = ?
					AND src.kind = ? AND src.library_id = e.library_id AND src.trashed_at_ms IS NULL`
			args = append(args, source.Relation, source.FromKind)
			attr, attrArgs := attributeValueSQL(source)
			clauses = append(clauses, clause+attr+`
			)`)
			args = append(args, attrArgs...)
			continue
		}
		clause := `EXISTS (
			SELECT 1 FROM entity_relation r JOIN media m ON m.id = r.to_media_id
			WHERE r.from_entity_id = e.id AND r.relation = ?
				AND m.library_id = e.library_id AND m.trashed_at_ms IS NULL`
		args = append(args, source.Relation)
		attr, attrArgs := attributeValueSQL(source)
		clause += attr
		args = append(args, attrArgs...)
		if source.ExcludeMediaRelation != "" {
			// An existing relation still identifies the Media's grouping when
			// its source is in Trash and can be restored.
			clause += ` AND NOT EXISTS (
				SELECT 1 FROM entity_relation incoming CROSS JOIN entity src ON src.id = incoming.from_entity_id
				WHERE incoming.to_media_id = m.id AND incoming.relation = ? AND src.library_id = e.library_id
			)`
			args = append(args, source.ExcludeMediaRelation)
		}
		clauses = append(clauses, clause+`)`)
	}
	return `(` + strings.Join(clauses, ` OR `) + `)`, args
}
