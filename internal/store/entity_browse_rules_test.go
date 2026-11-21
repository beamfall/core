package store

import (
	"context"
	"strings"
	"testing"
)

// TestEntityBrowseSourceSQLSeeksRelationsByEntity pins the plan of the
// browse-source predicate: each subquery seeks entity_relation by the outer
// Entity and only then looks up the source Entity by id. Planned the other way
// round, a Library's every source-kind Entity is walked for each listed
// Entity, which took a 1,200-artist browse to seconds (CORE-2887).
func TestEntityBrowseSourceSQLSeeksRelationsByEntity(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	clause, args := entityBrowseSourceSQL([]EntityBrowseSource{
		{Relation: "made-by", FromKind: "box"},
		{Relation: "appears-on", ExcludeMediaRelation: "holds"},
	})
	// #nosec G202 -- the clause is fixed SQL with bound args.
	rows, err := st.catalogR.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT id FROM entity e
		WHERE library_id = ? AND kind = ? AND trashed_at_ms IS NULL AND `+clause,
		append([]any{"lib", "maker"}, args...)...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var steps []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		steps = append(steps, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(steps, "; ")
	for _, want := range []string{
		"SEARCH r USING INDEX entity_relation_to_entity_idx",
		"SEARCH incoming USING INDEX entity_relation_to_media_idx",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("plan = %q, want %q", plan, want)
		}
	}
	for _, step := range steps {
		if strings.HasPrefix(step, "SEARCH src") && !strings.Contains(step, "(id=?)") {
			t.Errorf("plan step %q scans source Entities; want a primary key lookup (plan %q)", step, plan)
		}
	}
}
