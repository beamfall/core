package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Per-Profile engagement-counter surface (plugin-declared counters ride the
// generic event log), graph half. Like the Mute and My-Data
// surfaces, every field here operates on the CALLER's OWN Profile: the profile
// id always comes from scope.ProfileID, never from a variable. There is
// deliberately no other-profile parameter on any of these fields — a session
// can only ever read and mutate its own counters.
//
// Core stays domain-agnostic (invariant #1). The `kind` is a plugin-declared,
// opaque engagement-event kind: the resolvers accept any kind an activated
// plugin declared (plugin.Registry.HasEngagementEventKind) and never hardcode a
// kind string. The store API (engagement_event.go) is the generic append-only
// event log; an increment is a +1 event, a remove-last correction is a
// compensating -1 event, and the displayed counter is the net SUM(value) over
// the kind for the target — never an UPDATE/DELETE of history.

// EngagementCounterInfo is one Profile+target+kind counter the caller reads: the
// net running count (SUM of event values), the raw number of events on the log
// (history depth, including corrections), and the most recent event time. Media
// and/or Entity identify the target; an absent counter reads back as zero.
type EngagementCounterInfo struct {
	Kind           string `json:"kind"`
	MediaID        string `json:"mediaID,omitempty"`
	EntityID       string `json:"entityID,omitempty"`
	Count          int    `json:"count"`
	Events         int    `json:"events"`
	LastOccurredAt string `json:"lastOccurredAt,omitempty"`
}

// EngagementCounterEventInfo is one row of a counter's history (newest first):
// the signed value (+1 increment, -1 remove-last correction) and the true event
// instant. The full timeline is returned, including compensating corrections, so
// a client can render or replay the history rather than only the net count.
type EngagementCounterEventInfo struct {
	Value      int    `json:"value"`
	OccurredAt string `json:"occurredAt"`
}

// engagementCounter reads the caller's own counter for one kind + target.
// The kind must be one an activated plugin declared; the target
// is a Media id, an Entity id, or both (the per-Entity roll-up is the ADR-0027
// "entity aggregates" read — a per-Entity total across the Profile's events;
// the domain naming of those Entities is the plugin's, never Core's). Own-
// Profile-only is enforced at the store boundary: scope.ProfileID
// is the only profile the read can ever name. When `history` is true the full
// event timeline is included; otherwise only the aggregate is returned.
func (h *Handler) engagementCounter(ctx context.Context, scope auth.Scope, req request) (EngagementCounterResult, error) {
	kind, target, err := h.parseEngagementTarget(req)
	if err != nil {
		return EngagementCounterResult{}, err
	}
	if err := h.requireVisibleEngagementTarget(ctx, scope, target); err != nil {
		return EngagementCounterResult{}, err
	}
	agg, err := h.store.EngagementEventAggregateFor(ctx, scope.ProfileID, target, kind)
	if err != nil {
		return EngagementCounterResult{}, err
	}
	result := EngagementCounterResult{
		Counter: counterInfo(kind, target, agg),
		History: []EngagementCounterEventInfo{},
	}
	if !boolVar(req, "history") {
		return result, nil
	}
	events, err := h.store.EngagementEventsFor(ctx, scope.ProfileID, target, kind)
	if err != nil {
		return EngagementCounterResult{}, err
	}
	result.History = make([]EngagementCounterEventInfo, 0, len(events))
	for _, ev := range events {
		result.History = append(result.History, EngagementCounterEventInfo{
			Value:      ev.Value,
			OccurredAt: ev.OccurredAt.UTC().Format(time.RFC3339),
		})
	}
	return result, nil
}

// EngagementCounterResult is the read shape: the aggregate counter plus, when
// requested, its full newest-first history.
type EngagementCounterResult struct {
	Counter EngagementCounterInfo        `json:"counter"`
	History []EngagementCounterEventInfo `json:"history"`
}

// recordEngagement appends one increment (+1) event for the caller's OWN Profile
// on the named kind + target. The kind must be plugin-declared;
// the target is a Media and/or an Entity (so a single increment can attribute to
// both the Media and its associated Entity for the entity roll-up). It
// returns the resulting net counter so a client can reconcile its optimistic
// toggle without a refetch.
func (h *Handler) recordEngagement(ctx context.Context, scope auth.Scope, req request) (EngagementCounterInfo, error) {
	return h.appendEngagement(ctx, scope, req, 1)
}

// removeLastEngagement appends a compensating (-1) event for the caller's OWN
// Profile (ADR-0027 D1 "remove-last correction"). It is append-only — history is
// never edited, so the cancelled increment survives in the timeline and the net
// count simply falls by one. Removing below zero is allowed (the net is the
// honest SUM); a client should gate the action on a positive count.
func (h *Handler) removeLastEngagement(ctx context.Context, scope auth.Scope, req request) (EngagementCounterInfo, error) {
	return h.appendEngagement(ctx, scope, req, -1)
}

// appendEngagement is the shared write path for the increment and remove-last
// correction: it validates the kind + target, appends an event with the given
// signed value, and reads back the resulting net aggregate. Own-Profile-only is
// structural — scope.ProfileID is the only profile id passed to the store.
func (h *Handler) appendEngagement(ctx context.Context, scope auth.Scope, req request, value int) (EngagementCounterInfo, error) {
	kind, target, err := h.parseEngagementTarget(req)
	if err != nil {
		return EngagementCounterInfo{}, err
	}
	if err := h.requireVisibleEngagementTarget(ctx, scope, target); err != nil {
		return EngagementCounterInfo{}, err
	}
	if _, err := h.store.AppendEngagementEvent(ctx, store.EngagementEvent{
		ProfileID: scope.ProfileID,
		MediaID:   target.MediaID,
		EntityID:  target.EntityID,
		Kind:      kind,
		Value:     value,
	}); err != nil {
		return EngagementCounterInfo{}, err
	}
	agg, err := h.store.EngagementEventAggregateFor(ctx, scope.ProfileID, target, kind)
	if err != nil {
		return EngagementCounterInfo{}, err
	}
	return counterInfo(kind, target, agg), nil
}

// requireVisibleEngagementTarget resolves every caller-supplied target through
// the current Library/Reveal/certification scope before either events-plane
// reads or writes. One generic error keeps hidden and nonexistent targets
// indistinguishable and prevents the engagement log from becoming an oracle.
func (h *Handler) requireVisibleEngagementTarget(ctx context.Context, scope auth.Scope, target store.EngagementEventTarget) error {
	filter := scope.LibraryFilter()
	if target.MediaID != "" {
		if _, err := h.store.Media(ctx, target.MediaID, filter); err != nil {
			return clientError("unknown engagement target")
		}
	}
	if target.EntityID != "" {
		if _, err := h.store.Entity(ctx, target.EntityID, filter); err != nil {
			return clientError("unknown engagement target")
		}
	}
	return nil
}

// counterInfo projects a store aggregate onto the wire counter shape.
func counterInfo(kind string, target store.EngagementEventTarget, agg store.EngagementEventAggregate) EngagementCounterInfo {
	info := EngagementCounterInfo{
		Kind:     kind,
		MediaID:  string(target.MediaID),
		EntityID: string(target.EntityID),
		Count:    agg.Sum,
		Events:   agg.Count,
	}
	if !agg.LastOccurredAt.IsZero() {
		info.LastOccurredAt = agg.LastOccurredAt.UTC().Format(time.RFC3339)
	}
	return info
}

// parseEngagementTarget reads and validates the shared kind + target arguments.
// The kind must be non-empty AND declared by an activated plugin — Core never
// accepts an unknown kind (invariant #1: the kind vocabulary is plugin-owned, so
// a kind no plugin declared is a client error, not a free-form write). The
// target must name at least one of a Media id or an Entity id (mirroring the
// store write contract). The profile is never a parameter — the caller's own
// scope.ProfileID is the only profile these resolvers ever touch.
func (h *Handler) parseEngagementTarget(req request) (string, store.EngagementEventTarget, error) {
	kind := trimmedVariable(req, "kind")
	if kind == "" {
		return "", store.EngagementEventTarget{}, clientError("kind is required")
	}
	if !h.plugins.HasEngagementEventKind(kind) {
		return "", store.EngagementEventTarget{}, clientError("unknown engagement kind")
	}
	target := store.EngagementEventTarget{
		MediaID:  store.MediaID(trimmedVariable(req, "mediaID")),
		EntityID: store.EntityID(trimmedVariable(req, "entityID")),
	}
	if target.MediaID == "" && target.EntityID == "" {
		return "", store.EngagementEventTarget{}, clientError("a media or entity target is required")
	}
	return kind, target, nil
}
