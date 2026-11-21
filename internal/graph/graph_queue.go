package graph

import (
	"context"
	"math"
	"strings"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

type PlayQueueEntryInfo struct {
	ID      string        `json:"id"`
	MediaID store.MediaID `json:"mediaID"`
}

type PlayQueueInfo struct {
	Entries        []PlayQueueEntryInfo  `json:"entries"`
	CurrentEntryID *string               `json:"currentEntryID"`
	PositionMS     int64                 `json:"positionMS"`
	Shuffle        bool                  `json:"shuffle"`
	RepeatMode     store.QueueRepeatMode `json:"repeatMode"`
}

func (h *Handler) playQueue(ctx context.Context, scope auth.Scope) (PlayQueueInfo, error) {
	queue, err := h.store.PlayQueue(ctx, scope.ProfileID, scope.LibraryFilter())
	if err != nil {
		return PlayQueueInfo{}, err
	}
	result := PlayQueueInfo{
		Entries: make([]PlayQueueEntryInfo, len(queue.Entries)), CurrentEntryID: queue.CurrentEntryID,
		PositionMS: queue.PositionMS, Shuffle: queue.Shuffle, RepeatMode: queue.RepeatMode,
	}
	for i, entry := range queue.Entries {
		result.Entries[i] = PlayQueueEntryInfo{ID: entry.ID, MediaID: entry.Item.ID}
	}
	return result, nil
}

func (h *Handler) writePlayQueue(ctx context.Context, scope auth.Scope, fields map[string]bool, req request) (PlayQueueInfo, error) {
	err := h.withExpectedProfile(ctx, scope, req, func() error {
		return h.applyPlayQueueWrite(ctx, scope, fields, req)
	})
	if err != nil {
		return PlayQueueInfo{}, err
	}
	return h.playQueue(ctx, scope)
}

func (h *Handler) applyPlayQueueWrite(ctx context.Context, scope auth.Scope, fields map[string]bool, req request) error {
	profile, filter := scope.ProfileID, scope.LibraryFilter()
	if fields["queueaddmany"] {
		v := newVars(req)
		values, _ := v.StringList("mediaIDs", "Media IDs")
		if err := v.Err(); err != nil {
			return err
		}
		ids := make([]store.MediaID, len(values))
		for i, id := range values {
			ids[i] = store.MediaID(strings.TrimSpace(id))
		}
		return h.store.QueueAddMany(ctx, profile, ids, store.QueuePlacement(trimmedVariable(req, "placement")), filter)
	}
	if fields["queuemoveentry"] {
		position, err := queueInteger(req, "position", math.MaxInt32)
		if err != nil {
			return err
		}
		return h.store.QueueMoveEntry(ctx, profile, trimmedVariable(req, "entryID"), int(position), filter)
	}
	if fields["queueremoveentry"] {
		return h.store.QueueRemoveEntry(ctx, profile, trimmedVariable(req, "entryID"), filter)
	}
	if fields["queueplaynext"] {
		return h.store.QueuePlayNext(ctx, profile, trimmedVariable(req, "entryID"), filter)
	}
	if fields["queuesetcurrent"] {
		positionMS, err := queueInteger(req, "positionMS", 1<<53-1)
		if err != nil {
			return err
		}
		v := newVars(req)
		entryID, supplied := v.String("entryID")
		if err := v.Err(); err != nil {
			return err
		}
		entryID = strings.TrimSpace(entryID)
		if supplied && entryID == "" {
			return clientError("entryID is required")
		}
		return h.store.QueueSetCurrent(ctx, profile, entryID, positionMS, filter)
	}
	if fields["queuesetshuffle"] {
		v := newVars(req)
		enabled, ok := v.Bool("enabled")
		if err := v.Err(); err != nil {
			return err
		}
		if !ok {
			return clientError("enabled is required")
		}
		return h.store.QueueSetShuffle(ctx, profile, enabled, filter)
	}
	if fields["queuesetrepeat"] {
		return h.store.QueueSetRepeat(ctx, profile, store.QueueRepeatMode(trimmedVariable(req, "mode")), filter)
	}
	return clientError("invalid queue mutation")
}

func queueInteger(req request, name string, maximum float64) (int64, error) {
	v := newVars(req)
	number, ok := v.Number(name)
	if err := v.Err(); err != nil {
		return 0, err
	}
	if !ok || number < 0 || number > maximum || number != math.Trunc(number) {
		return 0, clientError(name + " must be a nonnegative integer")
	}
	return int64(number), nil
}
