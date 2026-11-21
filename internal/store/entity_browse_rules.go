package store

import "strings"

// EntityBrowseSource matches an incoming relation from an Entity of FromKind,
// or, when FromKind is empty, an outgoing relation to Media. ExcludeMediaRelation
// requires that Media to have no incoming Entity relation of the given name.
type EntityBrowseSource struct {
	Relation             string
	FromKind             string
	ExcludeMediaRelation string
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
			clauses = append(clauses, `EXISTS (
				SELECT 1 FROM entity_relation r CROSS JOIN entity src ON src.id = r.from_entity_id
				WHERE r.to_entity_id = e.id AND r.relation = ?
					AND src.kind = ? AND src.library_id = e.library_id AND src.trashed_at_ms IS NULL
			)`)
			args = append(args, source.Relation, source.FromKind)
			continue
		}
		clause := `EXISTS (
			SELECT 1 FROM entity_relation r JOIN media m ON m.id = r.to_media_id
			WHERE r.from_entity_id = e.id AND r.relation = ?
				AND m.library_id = e.library_id AND m.trashed_at_ms IS NULL`
		args = append(args, source.Relation)
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
