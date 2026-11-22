package partyqueue

import (
	"slices"
	"testing"
)

func ids(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, entry := range entries {
		out[i] = entry.ID
	}
	return out
}

func TestOrder(t *testing.T) {
	tests := []struct {
		name     string
		fairness Fairness
		waiting  []Entry
		lastTurn map[string]int64
		want     []string
	}{
		{
			name:     "round-robin interleaves a later participant",
			fairness: RoundRobin,
			waiting: []Entry{
				{ID: "a1", ParticipantID: "A", AddedAtMS: 1},
				{ID: "a2", ParticipantID: "A", AddedAtMS: 2},
				{ID: "a3", ParticipantID: "A", AddedAtMS: 3},
				{ID: "b1", ParticipantID: "B", AddedAtMS: 4},
			},
			want: []string{"a1", "b1", "a2", "a3"},
		},
		{
			name:     "round-robin counts a pinned entry as its participant's turn",
			fairness: RoundRobin,
			waiting: []Entry{
				{ID: "a1", ParticipantID: "A", AddedAtMS: 1, Pin: 1},
				{ID: "a2", ParticipantID: "A", AddedAtMS: 2},
				{ID: "b1", ParticipantID: "B", AddedAtMS: 3},
			},
			want: []string{"a1", "b1", "a2"},
		},
		{
			name:     "fifo keeps arrival order",
			fairness: FIFO,
			waiting: []Entry{
				{ID: "b1", ParticipantID: "B", AddedAtMS: 4},
				{ID: "a1", ParticipantID: "A", AddedAtMS: 1},
				{ID: "a2", ParticipantID: "A", AddedAtMS: 2},
			},
			want: []string{"a1", "a2", "b1"},
		},
		{
			name:     "longest without a turn goes first",
			fairness: RoundRobin,
			waiting: []Entry{
				{ID: "a1", ParticipantID: "A", AddedAtMS: 50},
				{ID: "b1", ParticipantID: "B", AddedAtMS: 60},
				{ID: "c1", ParticipantID: "C", AddedAtMS: 70},
			},
			lastTurn: map[string]int64{"A": 100, "B": 20},
			want:     []string{"b1", "c1", "a1"},
		},
		{
			name:     "pin keeps first place as others arrive",
			fairness: RoundRobin,
			waiting: []Entry{
				{ID: "a1", ParticipantID: "A", AddedAtMS: 1},
				{ID: "b1", ParticipantID: "B", AddedAtMS: 2},
				{ID: "c1", ParticipantID: "C", AddedAtMS: 3, Pin: 1},
				{ID: "d1", ParticipantID: "D", AddedAtMS: 4},
			},
			want: []string{"c1", "a1", "b1", "d1"},
		},
		{
			name:     "pin past the end lands last and colliding pins shift",
			fairness: FIFO,
			waiting: []Entry{
				{ID: "a1", ParticipantID: "A", AddedAtMS: 1},
				{ID: "b1", ParticipantID: "B", AddedAtMS: 2, Pin: 2},
				{ID: "c1", ParticipantID: "C", AddedAtMS: 3, Pin: 2},
				{ID: "d1", ParticipantID: "D", AddedAtMS: 4, Pin: 9},
			},
			want: []string{"a1", "b1", "c1", "d1"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ids(Order(test.fairness, test.waiting, test.lastTurn))
			if !slices.Equal(got, test.want) {
				t.Fatalf("Order = %v, want %v", got, test.want)
			}
		})
	}
}

func TestChangeoverAndMedian(t *testing.T) {
	tests := []struct {
		gaps []int
		want int
	}{
		{nil, DefaultChangeoverSeconds},
		{[]int{10, 30, 50}, 30},
		{[]int{10, 20}, 15},
		{[]int{-5}, 0},
		{[]int{400, 500}, MaxChangeoverSeconds},
	}
	for _, test := range tests {
		if got := Changeover(test.gaps); got != test.want {
			t.Errorf("Changeover(%v) = %d, want %d", test.gaps, got, test.want)
		}
	}
	if got := Median(nil); got != 0 {
		t.Errorf("Median(nil) = %d", got)
	}
}

func TestETAs(t *testing.T) {
	tests := []struct {
		name      string
		durations []int
		timing    Timing
		want      []int
	}{
		{
			name:      "default changeover",
			durations: []int{180, 180},
			timing:    Timing{HasCurrent: true, CurrentRemainingSeconds: 60, ChangeoverSeconds: Changeover(nil)},
			want:      []int{80, 280},
		},
		{
			name:      "nothing current starts now",
			durations: []int{100, 50, 10},
			timing:    Timing{ChangeoverSeconds: 10},
			want:      []int{0, 110, 170},
		},
		{
			name:      "unknown duration counts as the median",
			durations: []int{0, 100},
			timing:    Timing{HasCurrent: true, ChangeoverSeconds: 5, FallbackDurationSeconds: 200},
			want:      []int{5, 210},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ETAs(test.durations, test.timing); !slices.Equal(got, test.want) {
				t.Fatalf("ETAs = %v, want %v", got, test.want)
			}
		})
	}
}
