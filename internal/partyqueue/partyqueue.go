// Package partyqueue orders a party session's waiting entries and estimates
// when each one starts (CORE-1662). It is pure: callers pass the entries,
// turn history and timing facts, and receive an order and ETAs.
package partyqueue

import (
	"cmp"
	"slices"
)

// Fairness names how waiting entries are ordered.
type Fairness string

const (
	// RoundRobin gives the next turn to the participant who has gone longest
	// without one.
	RoundRobin Fairness = "round-robin"
	// FIFO plays waiting entries oldest first.
	FIFO Fairness = "fifo"
)

// Valid reports whether f is a known fairness policy.
func (f Fairness) Valid() bool { return f == RoundRobin || f == FIFO }

const (
	// DefaultChangeoverSeconds is the allowance between entries before any
	// changeover has been observed in the session.
	DefaultChangeoverSeconds = 20
	// MaxChangeoverSeconds caps the observed allowance.
	MaxChangeoverSeconds = 120
)

// Entry is one waiting entry.
type Entry struct {
	ID            string
	ParticipantID string
	// AddedAtMS orders a participant's entries and FIFO.
	AddedAtMS int64
	// Pin is the 1-based waiting position the host moved the entry to, or 0.
	Pin int
	// DurationSeconds is 0 when unknown.
	DurationSeconds int
}

// Order returns the waiting entries in play order. lastTurnMS maps a
// participant to the time their most recent turn started; a participant
// absent from it has never had a turn and ranks by their first waiting
// entry's time. Pinned entries keep their positions and the policy fills the
// remaining positions around them.
func Order(fairness Fairness, waiting []Entry, lastTurnMS map[string]int64) []Entry {
	var pinned, free []Entry
	for _, entry := range waiting {
		if entry.Pin > 0 {
			pinned = append(pinned, entry)
		} else {
			free = append(free, entry)
		}
	}
	slots := pinSlots(pinned, len(waiting))
	if fairness == FIFO {
		free = fifo(free)
	} else {
		free = roundRobin(free, slots, lastTurnMS)
	}
	return fillSlots(slots, free)
}

func byAdded(a, b Entry) int {
	if c := cmp.Compare(a.AddedAtMS, b.AddedAtMS); c != 0 {
		return c
	}
	return cmp.Compare(a.ID, b.ID)
}

func fifo(entries []Entry) []Entry {
	out := slices.Clone(entries)
	slices.SortStableFunc(out, byAdded)
	return out
}

// roundRobin orders the free entries into the slots pinSlots left open. A
// pinned entry in a slot counts as its participant's turn, as it does when the
// queue advances, so later free picks rotate around it.
func roundRobin(entries []Entry, slots []*Entry, lastTurnMS map[string]int64) []Entry {
	queues := map[string][]Entry{}
	for _, entry := range fifo(entries) {
		queues[entry.ParticipantID] = append(queues[entry.ParticipantID], entry)
	}
	// rank is when the participant last had (or, never having had one, first
	// asked for) a turn; the smallest rank goes next.
	rank := make(map[string]int64, len(queues))
	first := make(map[string]int64, len(queues))
	turned := make(map[string]bool, len(queues))
	var latest int64
	for participant, queue := range queues {
		first[participant] = queue[0].AddedAtMS
		if at, ok := lastTurnMS[participant]; ok {
			rank[participant] = at
			turned[participant] = true
		} else {
			rank[participant] = queue[0].AddedAtMS
		}
		latest = max(latest, rank[participant])
	}
	for _, at := range lastTurnMS {
		latest = max(latest, at)
	}
	out := make([]Entry, 0, len(entries))
	for _, slot := range slots {
		if slot != nil {
			// A simulated turn starts after every real one.
			latest++
			rank[slot.ParticipantID] = latest
			turned[slot.ParticipantID] = true
			continue
		}
		next := ""
		for participant, queue := range queues {
			if len(queue) == 0 {
				continue
			}
			if next == "" || less(participant, next, rank, first, turned) {
				next = participant
			}
		}
		out = append(out, queues[next][0])
		queues[next] = queues[next][1:]
		// A simulated turn starts after every real one.
		latest++
		rank[next] = latest
		turned[next] = true
	}
	return out
}

// less orders by rank; on a tie a participant still waiting for a first turn
// goes before one who has had a turn.
func less(a, b string, rank, first map[string]int64, turned map[string]bool) bool {
	if rank[a] != rank[b] {
		return rank[a] < rank[b]
	}
	if turned[a] != turned[b] {
		return !turned[a]
	}
	if first[a] != first[b] {
		return first[a] < first[b]
	}
	return a < b
}

// pinSlots returns n slots with each pinned entry at its position.
func pinSlots(pinned []Entry, n int) []*Entry {
	slots := make([]*Entry, n)
	slices.SortStableFunc(pinned, func(a, b Entry) int {
		if c := cmp.Compare(a.Pin, b.Pin); c != 0 {
			return c
		}
		return byAdded(a, b)
	})
	for i := range pinned {
		at := min(pinned[i].Pin, n) - 1
		for at < n && slots[at] != nil {
			at++
		}
		if at == n {
			at = slices.Index(slots, nil)
		}
		slots[at] = &pinned[i]
	}
	return slots
}

// fillSlots returns the slots in order with free entries in the open ones.
func fillSlots(slots []*Entry, free []Entry) []Entry {
	out := make([]Entry, 0, len(slots))
	next := 0
	for _, slot := range slots {
		if slot != nil {
			out = append(out, *slot)
			continue
		}
		out = append(out, free[next])
		next++
	}
	return out
}

// Changeover returns the allowance between entries: the median of the
// observed gaps in seconds, DefaultChangeoverSeconds when none are known,
// clamped to 0..MaxChangeoverSeconds.
func Changeover(gapsSeconds []int) int {
	if len(gapsSeconds) == 0 {
		return DefaultChangeoverSeconds
	}
	return min(max(Median(gapsSeconds), 0), MaxChangeoverSeconds)
}

// Median returns the median of values (the mean of the middle pair, rounded
// down, for an even count) or 0 when there are none.
func Median(values []int) int {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// Timing is what ETAs needs beyond the ordered durations.
type Timing struct {
	// HasCurrent is true while an entry is current.
	HasCurrent bool
	// CurrentRemainingSeconds is the current entry's remaining time.
	CurrentRemainingSeconds int
	// ChangeoverSeconds is the allowance per changeover.
	ChangeoverSeconds int
	// FallbackDurationSeconds stands in for an unknown duration.
	FallbackDurationSeconds int
}

// ETAs returns, for each ordered waiting entry duration, the seconds until it
// starts: the current entry's remaining time plus the durations ahead plus one
// changeover per changeover. With nothing current the first entry starts now.
func ETAs(durationsSeconds []int, timing Timing) []int {
	out := make([]int, len(durationsSeconds))
	at := 0
	if timing.HasCurrent {
		at = max(timing.CurrentRemainingSeconds, 0) + timing.ChangeoverSeconds
	}
	for i, duration := range durationsSeconds {
		out[i] = at
		if duration <= 0 {
			duration = timing.FallbackDurationSeconds
		}
		at += duration + timing.ChangeoverSeconds
	}
	return out
}
