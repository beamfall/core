package app

import (
	"testing"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/updatecheck"
)

// TestUpdateCheckAdapterStateTokens pins the graph wire tokens to
// updatecheck.State's own tokens. The adapter is the only place the two
// vocabularies meet, and it carries the state as a plain string so the graph
// package stays free of an updatecheck dependency — which means nothing but
// this test stops the two sides from drifting apart silently.
func TestUpdateCheckAdapterStateTokens(t *testing.T) {
	cases := []struct {
		state updatecheck.State
		want  string
	}{
		{updatecheck.StateUnavailable, graph.UpdateStateUnavailable},
		{updatecheck.StateDisabled, graph.UpdateStateDisabled},
		{updatecheck.StateUpToDate, graph.UpdateStateUpToDate},
		{updatecheck.StateUpdateAvailable, graph.UpdateStateUpdateAvailable},
	}
	for _, c := range cases {
		if got := c.state.String(); got != c.want {
			t.Errorf("updatecheck state %d token = %q, want graph token %q", int(c.state), got, c.want)
		}
	}
}

// TestUpdateCheckAdapterUnavailableIsNotUpToDate proves the adapter cannot turn
// a failed check into a reassuring one: the zero-value Status the service
// returns on a refused or unreachable manifest crosses the seam as the
// unavailable token, which is distinct from the up-to-date token.
func TestUpdateCheckAdapterUnavailableIsNotUpToDate(t *testing.T) {
	var zero updatecheck.Status
	if got := zero.State.String(); got != graph.UpdateStateUnavailable {
		t.Fatalf("zero Status state token = %q, want %q", got, graph.UpdateStateUnavailable)
	}
	if zero.IsUpToDate() {
		t.Fatal("zero Status must never report up to date")
	}
}
