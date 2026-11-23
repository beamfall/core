package graph

import (
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

func TestPlayedFilterVars(t *testing.T) {
	after := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	got, err := playedFilterVars(request{Variables: map[string]any{
		"playedAfter":  after.Format(time.RFC3339),
		"playedBefore": "2026-11-08T05:00:00-05:00",
	}}, "coltrane")
	if err != nil {
		t.Fatal(err)
	}
	want := store.PlayedFilter{Query: "coltrane", PlayedAfter: after.UnixMilli(), PlayedBefore: time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC).UnixMilli()}
	if got != want {
		t.Fatalf("playedFilterVars = %#v, want %#v", got, want)
	}
	if got, err := playedFilterVars(request{}, ""); err != nil || got != (store.PlayedFilter{}) {
		t.Fatalf("absent bounds = %#v, %v; want unbounded", got, err)
	}
	for _, bad := range []string{"yesterday", "2026-11-01", "1969-12-31T00:00:00Z"} {
		if _, err := playedFilterVars(request{Variables: map[string]any{"playedBefore": bad}}, ""); err == nil {
			t.Errorf("playedBefore %q accepted; want a client error", bad)
		}
	}
}
