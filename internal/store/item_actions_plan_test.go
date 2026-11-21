package store

import (
	"context"
	"strings"
	"testing"
)

// TestEntityTreeMediaWalksOutFromTheRoot pins the plan of the read behind an
// Entity's item state: the tree is walked from the root by relation and the
// Media are sought by id, so neither step reads every Entity or Media in the
// visible Libraries.
func TestEntityTreeMediaWalksOutFromTheRoot(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	certClause, certArgs := certificationFilterSQL(nil, false, nil)
	args := append([]any{"entity-root", "album-track", "season-of", "lib-a", "lib-b", "lib-a", "lib-b"}, certArgs...)
	rows, err := st.catalogR.QueryContext(ctx, "EXPLAIN QUERY PLAN "+entityTreeMediaSQL("?, ?", "?, ?", certClause), args...)
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
		"SEARCH r USING INDEX entity_relation_to_entity_idx (to_entity_id=?",
		"SEARCH child USING INDEX sqlite_autoindex_entity_1 (id=?)",
		"SEARCH r USING COVERING INDEX entity_relation_media_uq (from_entity_id=?)",
		"SEARCH m USING INDEX sqlite_autoindex_media_1 (id=?)",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("entity tree plan = %q, want %q", plan, want)
		}
	}
	for _, scan := range []string{"entity_library_kind_idx", "media_library_title_nocase_idx"} {
		if strings.Contains(plan, scan) {
			t.Fatalf("entity tree plan = %q, reads a Library index %q", plan, scan)
		}
	}
}
