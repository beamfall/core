package store

import (
	"container/heap"
	"context"
	"sort"
)

const (
	myEngagementLogMaxOffset = 10_000
	myEngagementLogIDChunk   = 400
)

type myEngagementRankedEvent struct {
	event MyEngagementEvent
	rank  int
}

// myEngagementWorstHeap keeps the least desirable retained event at index 0,
// allowing each newly scanned event to replace it in O(log(offset+limit)).
type myEngagementWorstHeap []myEngagementRankedEvent

func (h myEngagementWorstHeap) Len() int { return len(h) }

func (h myEngagementWorstHeap) Less(i, j int) bool {
	return myEngagementBefore(h[j], h[i])
}

func (h myEngagementWorstHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *myEngagementWorstHeap) Push(value any) {
	*h = append(*h, value.(myEngagementRankedEvent))
}

func (h *myEngagementWorstHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	*h = old[:last]
	return value
}

func (s *Store) myEngagementLogBoundedLocked(ctx context.Context, profileID ProfileID, filter LibraryFilter, query MyEngagementLogQuery) (MyEngagementLogPage, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = myEngagementLogDefaultLimit
	}
	limit = min(limit, myEngagementLogMaxLimit)
	offset := min(max(query.Offset, 0), myEngagementLogMaxOffset)
	want := offset + limit

	top := make(myEngagementWorstHeap, 0, want)
	heap.Init(&top)
	total := 0
	var after MediaID
	for {
		ids, events, err := s.myEngagementChunkLocked(ctx, profileID, after, query)
		if err != nil {
			return MyEngagementLogPage{}, err
		}
		if len(ids) == 0 {
			break
		}
		after = ids[len(ids)-1]
		visible, err := s.visibleMediaBatchSQL(ctx, ids, filter)
		if err != nil {
			return MyEngagementLogPage{}, err
		}
		for _, ranked := range events {
			media, ok := visible[ranked.event.MediaID]
			if !ok {
				continue
			}
			if query.LibraryID != "" && media.LibraryID != query.LibraryID {
				continue
			}
			ranked.event.LibraryID = media.LibraryID
			total++
			retainMyEngagementCandidate(&top, ranked, want)
		}
	}

	candidates := make([]myEngagementRankedEvent, len(top))
	copy(candidates, top)
	sort.Slice(candidates, func(i, j int) bool {
		return myEngagementBefore(candidates[i], candidates[j])
	})
	page := MyEngagementLogPage{Total: total, Events: []MyEngagementEvent{}}
	if offset >= len(candidates) {
		return page, nil
	}
	for _, candidate := range candidates[offset:] {
		page.Events = append(page.Events, candidate.event)
	}
	return page, nil
}

func retainMyEngagementCandidate(top *myEngagementWorstHeap, candidate myEngagementRankedEvent, limit int) {
	if top.Len() < limit {
		heap.Push(top, candidate)
		return
	}
	if myEngagementBefore(candidate, (*top)[0]) {
		(*top)[0] = candidate
		heap.Fix(top, 0)
	}
}

func myEngagementBefore(left, right myEngagementRankedEvent) bool {
	if !left.event.OccurredAt.Equal(right.event.OccurredAt) {
		return left.event.OccurredAt.After(right.event.OccurredAt)
	}
	if left.rank != right.rank {
		return left.rank < right.rank
	}
	return left.event.MediaID < right.event.MediaID
}
