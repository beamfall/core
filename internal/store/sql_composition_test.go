package store

import (
	"strings"
	"testing"
)

// The store composes several queries by concatenating generated SQL fragments
// (certification, media-field and entity-relation filters). Those fragments
// must stay closed: caller-supplied values appear only as bound args, never in
// the SQL text. These tests pin that for the builders feeding the concatenation.

const sqlProbe = `x'); DROP TABLE media; --`

func TestCertificationFilterSQLNeverInlinesValues(t *testing.T) {
	constraints := []CertificationConstraint{{
		PluginID:     sqlProbe,
		Allowed:      []string{sqlProbe, "PG"},
		AllowUnrated: true,
	}}
	granted := []MediaID{MediaID(sqlProbe)}

	for name, build := range map[string]func() (string, []any){
		"library-scoped": func() (string, []any) { return certificationFilterSQL(constraints, true, granted) },
		"plane-scoped":   func() (string, []any) { return planeCertificationFilterSQL(sqlProbe, constraints, true, granted) },
	} {
		t.Run(name, func(t *testing.T) {
			clause, args := build()
			if strings.Contains(clause, sqlProbe) || strings.Contains(clause, "DROP") {
				t.Fatalf("clause inlines a caller value: %q", clause)
			}
			if got, want := strings.Count(clause, "?"), len(args); got != want {
				t.Fatalf("placeholders = %d, args = %d", got, want)
			}
		})
	}
}

func TestMediaFieldFilterSQLNeverInlinesValues(t *testing.T) {
	clause, args, err := mediaFieldFilterSQL(map[string]any{
		sqlProbe:  sqlProbe,
		"flag":    true,
		"number":  map[string]any{"gte": 1, "lte": 2},
		"awarded": map[string]any{"gte": "2001-01-01", "lte": "2002-01-01"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(clause, sqlProbe) || strings.Contains(clause, "DROP") {
		t.Fatalf("clause inlines a caller value: %q", clause)
	}
	if got, want := strings.Count(clause, "?"), len(args); got != want {
		t.Fatalf("placeholders = %d, args = %d", got, want)
	}
}

func TestEntityRelationFilterSQLNeverInlinesValues(t *testing.T) {
	clause, args := entityRelationFilterSQL(&EntityFilter{Kind: sqlProbe, Name: sqlProbe}, []LibraryID{LibraryID(sqlProbe)})
	if strings.Contains(clause, sqlProbe) || strings.Contains(clause, "DROP") {
		t.Fatalf("clause inlines a caller value: %q", clause)
	}
	if got, want := strings.Count(clause, "?"), len(args); got != want {
		t.Fatalf("placeholders = %d, args = %d", got, want)
	}
}

func TestDimensionPredicateIsClosed(t *testing.T) {
	for _, dim := range []HealthDimension{HealthUnmatched, HealthBroken, HealthScrapeFailure, HealthMissingArtwork, HealthDuplicate} {
		if got := dimensionPredicate(dim); got == "" || strings.Contains(got, "?") {
			t.Fatalf("dimensionPredicate(%q) = %q, want a non-empty fragment with no placeholders", dim, got)
		}
	}
}
