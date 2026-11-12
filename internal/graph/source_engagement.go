package graph

import "github.com/beamfall/core/internal/store"

// SourceItemEngagement is the acting Profile's playback state for one
// SourceItem, so a card can draw progress, a finished mark or a remaining
// count without a second request. Absent means the Profile has no state for
// the item. Named by shape (invariants 1-2): positions and counts, no domain.
type SourceItemEngagement struct {
	// PositionSeconds and DurationSeconds are present while the item is
	// resumable: started past the resume floor and not finished.
	PositionSeconds int `json:"positionSeconds,omitempty"`
	DurationSeconds int `json:"durationSeconds,omitempty"`
	// Completed is true once the Profile finished the item; for an ordered
	// container, once every visible ordered child is finished.
	Completed bool `json:"completed,omitempty"`
	// TotalCount and RemainingCount are present on an ordered container: its
	// visible ordered children, and how many the Profile has not finished.
	// They never count a child this scope cannot see (ADR-0004).
	TotalCount     int `json:"totalCount,omitempty"`
	RemainingCount int `json:"remainingCount,omitempty"`
}

func sourceItemEngagement(state *store.SourceEngagement) *SourceItemEngagement {
	if state == nil {
		return nil
	}
	return &SourceItemEngagement{
		PositionSeconds: state.PositionSeconds,
		DurationSeconds: state.DurationSeconds,
		Completed:       state.Completed,
		TotalCount:      state.TotalCount,
		RemainingCount:  state.RemainingCount,
	}
}
