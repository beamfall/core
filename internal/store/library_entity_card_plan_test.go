package store

import (
	"context"
	"strings"
	"testing"
)

// TestEntityCardQueriesSeekTheListedEntities pins the plans of the per-card
// reads behind a browse page (CORE-2894): with a bound id list, the media
// counts seek each owner's relations and the card details seek each source by
// id, so neither reads every Media or Entity in the Library per page.
func TestEntityCardQueriesSeekTheListedEntities(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	plan := func(query string, args ...any) string {
		t.Helper()
		rows, err := st.catalogR.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			out = append(out, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(out, "; ")
	}
	const owners = 100
	args := make([]any, 0, owners+2)
	args = append(args, releaseDateField)
	for i := range owners {
		args = append(args, "entity-"+string(rune('a'+i%26)))
	}
	counts := plan(libraryEntityMediaCountsSQL(owners, ""), append(args[1:owners+1:owners+1], "lib")...)
	if r, m := strings.Index(counts, "SEARCH r "), strings.Index(counts, "SEARCH m "); r < 0 || m < 0 || r > m || !strings.Contains(counts, "(from_entity_id=?") {
		t.Fatalf("media count plan = %q, want relations sought by owner before Media", counts)
	}
	details := plan(entityCardDetailsSQL(owners, "?"), append(args, "lib")...)
	if !strings.Contains(details, "SEARCH source USING INDEX sqlite_autoindex_entity_1 (id=?)") || strings.Contains(details, "entity_library_kind_idx") {
		t.Fatalf("card detail plan = %q, want sources sought by id", details)
	}
}
