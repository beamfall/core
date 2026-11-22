package graph

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Offline engagement replay statuses (ADR-0157 Amendment B7). Every status is
// terminal: a client drops the event from its outbox whatever it reads.
const (
	offlineEngagementApplied   = "applied"
	offlineEngagementDuplicate = "duplicate"
	offlineEngagementRejected  = "rejected"
)

// OfflineEngagementReplay summarises one replayOfflineEngagement call.
type OfflineEngagementReplay struct {
	Applied    int                            `json:"applied"`
	Duplicates int                            `json:"duplicates"`
	Rejected   int                            `json:"rejected"`
	Results    []OfflineEngagementEventResult `json:"results"`
}

// OfflineEngagementEventResult is the outcome of one replayed event. Reason is
// set only for a rejected event.
type OfflineEngagementEventResult struct {
	EventID string  `json:"eventID"`
	Status  string  `json:"status"`
	Reason  *string `json:"reason"`
}

// offlineEngagementInput is one decoded OfflineEngagementEventInput.
type offlineEngagementInput struct {
	eventID         string
	mediaID         string
	positionSeconds int
	durationSeconds int
	completed       bool
	playedAtMS      int64
}

// replayOfflineEngagement applies the play sessions a Device recorded while
// offline for the session's Profile (CORE-3182, ADR-0157 Amendment B7).
// Events apply in played-at order, ties by event ID, each exactly once per
// (Profile, Device, event ID). Media the Profile cannot see now, or an
// invalid event, is rejected and not recorded, so it cannot probe hidden
// Media and a corrected resend still applies. One engagement-changed
// invalidation follows if anything applied.
func (h *Handler) replayOfflineEngagement(ctx context.Context, scope auth.Scope, req request) (OfflineEngagementReplay, error) {
	raw, ok := req.Variables["events"].([]any)
	if !ok {
		if req.Variables["events"] != nil {
			return OfflineEngagementReplay{}, clientError("events must be a list")
		}
		raw = nil
	}
	if len(raw) > store.MaxOfflineEngagementBatch {
		return OfflineEngagementReplay{}, clientError("at most 500 events per call")
	}
	inputs := make([]offlineEngagementInput, 0, len(raw))
	for _, item := range raw {
		fields, ok := item.(map[string]any)
		if !ok {
			return OfflineEngagementReplay{}, clientError("each event must be an object")
		}
		eventID, _ := fields["eventID"].(string)
		mediaID, _ := fields["mediaID"].(string)
		completed, _ := fields["completed"].(bool)
		inputs = append(inputs, offlineEngagementInput{
			eventID:         eventID,
			mediaID:         strings.TrimSpace(mediaID),
			positionSeconds: int(offlineEngagementNumber(fields["positionSeconds"])),
			durationSeconds: int(offlineEngagementNumber(fields["durationSeconds"])),
			completed:       completed,
			playedAtMS:      offlineEngagementNumber(fields["playedAtMS"]),
		})
	}
	sort.SliceStable(inputs, func(i, j int) bool {
		if inputs[i].playedAtMS != inputs[j].playedAtMS {
			return inputs[i].playedAtMS < inputs[j].playedAtMS
		}
		return inputs[i].eventID < inputs[j].eventID
	})
	result := OfflineEngagementReplay{Results: make([]OfflineEngagementEventResult, 0, len(inputs))}
	err := h.withExpectedProfile(ctx, scope, req, func() error {
		for _, in := range inputs {
			status, reason, err := h.applyOfflineEngagement(ctx, scope, in)
			if err != nil {
				return err
			}
			entry := OfflineEngagementEventResult{EventID: in.eventID, Status: status}
			switch status {
			case offlineEngagementApplied:
				result.Applied++
			case offlineEngagementDuplicate:
				result.Duplicates++
			default:
				result.Rejected++
				entry.Reason = &reason
			}
			result.Results = append(result.Results, entry)
		}
		return nil
	})
	if result.Applied > 0 {
		// Events that applied before a failure are committed, so caches must
		// still learn of them.
		h.notifyEngagementChanged(ctx, scope)
	}
	if err != nil {
		return OfflineEngagementReplay{}, err
	}
	return result, nil
}

// applyOfflineEngagement applies one event and returns its status, the reason
// for a rejection, or an error that fails the whole call (a store fault, not a
// bad event). Applied events stay applied, so the client's retry reads them as
// duplicates.
func (h *Handler) applyOfflineEngagement(ctx context.Context, scope auth.Scope, in offlineEngagementInput) (string, string, error) {
	switch {
	case strings.TrimSpace(in.eventID) == "":
		return offlineEngagementRejected, "eventID is required", nil
	case in.mediaID == "":
		return offlineEngagementRejected, "mediaID is required", nil
	case in.playedAtMS <= 0:
		return offlineEngagementRejected, "playedAtMS is required", nil
	case in.positionSeconds < 0 || in.durationSeconds < 0:
		return offlineEngagementRejected, "positions cannot be negative", nil
	}
	media, err := h.store.Media(ctx, store.MediaID(in.mediaID), scope.PlaybackFilter())
	if err != nil {
		return offlineEngagementRejected, "unknown media", nil
	}
	duration := in.durationSeconds
	if duration <= 0 {
		duration = media.DurationSeconds
	}
	outcome, err := h.store.ApplyOfflineEngagementEvent(ctx, store.OfflineEngagementEvent{
		EventID: in.eventID,
		Progress: store.PlaybackProgress{
			ProfileID:       scope.ProfileID,
			MediaID:         media.ID,
			PositionSeconds: in.positionSeconds,
			DurationSeconds: duration,
			DeviceID:        scope.DeviceID,
		},
		Completed: in.completed,
		PlayedAt:  time.UnixMilli(in.playedAtMS).UTC(),
		Policy:    h.resumePolicy(ctx, media),
	})
	var invalid store.ValidationError
	switch {
	case errors.As(err, &invalid):
		return offlineEngagementRejected, invalid.Error(), nil
	case err != nil:
		return "", "", err
	case outcome.Duplicate:
		return offlineEngagementDuplicate, "", nil
	}
	if outcome.BecameWatched && h.watchedObserver != nil {
		h.notifyWatched(ctx, scope.ProfileID, media)
	}
	return offlineEngagementApplied, "", nil
}

// offlineEngagementNumber reads a whole-number event field; JSON numbers decode
// as float64. A missing or non-numeric value reads as zero, which the
// per-event checks reject where the field is required.
func offlineEngagementNumber(raw any) int64 {
	switch v := raw.(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	default:
		return 0
	}
}
