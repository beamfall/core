package graph

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/l10n"
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
	filter, err := h.sealedFilter(ctx, scope.LibraryFilter())
	if err != nil {
		return PlayQueueInfo{}, err
	}
	queue, err := h.store.PlayQueue(ctx, scope.ProfileID, filter)
	if err != nil {
		return PlayQueueInfo{}, err
	}
	return playQueueInfo(queue), nil
}

func playQueueInfo(queue store.PlayQueue) PlayQueueInfo {
	result := PlayQueueInfo{
		Entries: make([]PlayQueueEntryInfo, len(queue.Entries)), CurrentEntryID: queue.CurrentEntryID,
		PositionMS: queue.PositionMS, Shuffle: queue.Shuffle, RepeatMode: queue.RepeatMode,
	}
	for i, entry := range queue.Entries {
		result.Entries[i] = PlayQueueEntryInfo{ID: entry.ID, MediaID: entry.Item.ID}
	}
	return result
}

func (h *Handler) writePlayQueue(ctx context.Context, scope auth.Scope, fields map[string]bool, req request) (PlayQueueInfo, error) {
	if fields["queueaddmany"] || fields["queueplaynext"] {
		queue, err := h.writeAuthorizedPlayQueue(ctx, scope, fields, req)
		if fields["queueplaynext"] && errors.Is(err, store.ErrQueueMediaNotFound) {
			return PlayQueueInfo{}, clientError("media is not queued")
		}
		if err != nil {
			return PlayQueueInfo{}, queueWriteError(err)
		}
		return playQueueInfo(queue), nil
	}
	err := h.withExpectedProfile(ctx, scope, req, func() error {
		return h.applyPlayQueueWrite(ctx, scope, fields, req)
	})
	if err != nil {
		return PlayQueueInfo{}, err
	}
	return h.playQueue(ctx, scope)
}

func (h *Handler) writeAuthorizedPlayQueue(ctx context.Context, scope auth.Scope, fields map[string]bool, req request) (store.PlayQueue, error) {
	expected, err := checkExpectedProfile(scope, req)
	if err != nil {
		return store.PlayQueue{}, err
	}
	filter, err := h.sealedFilter(ctx, scope.LibraryFilter())
	if err != nil {
		return store.PlayQueue{}, err
	}
	authorization := h.auth.QueueWriteAuthorization(scope, expected, filter)
	if fields["queueplaynext"] {
		return h.store.QueuePlayNext(ctx, authorization, trimmedVariable(req, "entryID"))
	}
	if fields["queueaddmany"] {
		v := newVars(req)
		values, _ := v.StringList("mediaIDs", "Media IDs")
		if err := v.Err(); err != nil {
			return store.PlayQueue{}, err
		}
		ids := make([]store.MediaID, len(values))
		for i, id := range values {
			ids[i] = store.MediaID(strings.TrimSpace(id))
		}
		return h.store.QueueAddMany(ctx, authorization, ids, store.QueuePlacement(trimmedVariable(req, "placement")))
	}
	return store.PlayQueue{}, clientError("invalid queue mutation")
}

func queueWriteError(err error) error {
	switch {
	case errors.Is(err, store.ErrQueueMediaNotFound):
		return clientError("unknown media")
	case errors.Is(err, store.ErrQueueMediaLocked):
		return &l10n.Error{Key: l10n.QueueMediaLocked, Err: clientError("This item is locked.")}
	case errors.Is(err, store.ErrSessionProfileMismatch):
		return errProfileMismatch
	case errors.Is(err, store.ErrProfileDisabled):
		return clientError("profile is disabled")
	case errors.Is(err, store.ErrDeviceRevoked), errors.Is(err, store.ErrSessionExpired),
		errors.Is(err, store.ErrSessionNotFound), errors.Is(err, store.ErrQueueProfileOutsideSchedule):
		return clientError("invalid session")
	default:
		return err
	}
}

func (h *Handler) applyPlayQueueWrite(ctx context.Context, scope auth.Scope, fields map[string]bool, req request) error {
	filter, err := h.sealedFilter(ctx, scope.LibraryFilter())
	if err != nil {
		return err
	}
	profile := scope.ProfileID
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
