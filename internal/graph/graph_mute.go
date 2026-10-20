package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Per-Profile Mute / "Not interested", graph half. Every field
// here operates on the CALLER's OWN Profile: the profile id always comes from
// scope.ProfileID, never from a variable. There is deliberately no other-profile
// parameter on any of these fields — a session can only ever set, clear, and
// list its own mutes (ADR-0054 §2.4: a Mute is always self-authored and
// self-cleared). A Mute carries no safety weight, so — unlike a Console
// curation op — these are NEVER manager-gated and never surface in the operator
// Console; a managed (kid) Profile may mute and unmute freely. The viewer-facing
// management list, export, and clear-all are the My-Data citizen of §2.8.

// MyMuteInfo is one "Not interested" entry the Profile has authored for itself:
// the target species (entity|tag), the target id, whether the
// Mute includes hierarchical descendants, and when it was set.
type MyMuteInfo struct {
	Kind               string `json:"kind"`
	TargetID           string `json:"targetID"`
	IncludeDescendants bool   `json:"includeDescendants"`
	MutedAt            string `json:"mutedAt"`
}

// muteInfo projects one store MuteTarget onto the wire shape.
func muteInfo(m store.MuteTarget) MyMuteInfo {
	return MyMuteInfo{
		Kind:               string(m.Kind),
		TargetID:           m.TargetID,
		IncludeDescendants: m.IncludeDescendants,
		MutedAt:            m.MutedAt.UTC().Format(time.RFC3339),
	}
}

// mutes returns the caller's own mute list, newest first. Own-
// Profile-only is enforced at the store boundary: scope.ProfileID is the only
// profile the read can ever name.
func (h *Handler) mutes(ctx context.Context, scope auth.Scope) ([]MyMuteInfo, error) {
	targets, err := h.store.MutedTargets(ctx, scope.ProfileID)
	if err != nil {
		return nil, err
	}
	infos := make([]MyMuteInfo, len(targets))
	for i, t := range targets {
		infos[i] = muteInfo(t)
	}
	return infos, nil
}

// MuteResult echoes the resolved target after a set/clear. It carries the
// target identity and the resulting muted state — enough for the client to
// reconcile its optimistic toggle without a refetch.
type MuteResult struct {
	Kind               string `json:"kind"`
	TargetID           string `json:"targetID"`
	IncludeDescendants bool   `json:"includeDescendants"`
	Muted              bool   `json:"muted"`
}

// setMute records a Mute on one Entity or Tag for the caller's OWN Profile.
// `kind` is the closed vocabulary entity|tag; `targetID`
// is the Entity or Tag id; `includeDescendants` honors the hierarchical-category
// case the enforcement predicate consumes. Own-Profile-only is
// structural: scope.ProfileID is the only profile id passed to the store.
func (h *Handler) setMute(ctx context.Context, scope auth.Scope, req request) (MuteResult, error) {
	kind, targetID, includeDescendants, err := parseMuteTarget(req)
	if err != nil {
		return MuteResult{}, err
	}
	if err := h.store.SetMute(ctx, scope.ProfileID, kind, targetID, includeDescendants, true); err != nil {
		return MuteResult{}, err
	}
	return MuteResult{
		Kind:               string(kind),
		TargetID:           targetID,
		IncludeDescendants: includeDescendants,
		Muted:              true,
	}, nil
}

// clearMute removes a Mute on one Entity or Tag for the caller's OWN Profile
// (ADR-0054 §2.4: self-cleared). Clearing a target that is not muted is a no-op
// that still resolves cleanly — the toggle is idempotent. The
// includeDescendants flag is irrelevant to a delete (the store deletes by
// profile+kind+target), so it is not read here.
func (h *Handler) clearMute(ctx context.Context, scope auth.Scope, req request) (MuteResult, error) {
	kind, targetID, _, err := parseMuteTarget(req)
	if err != nil {
		return MuteResult{}, err
	}
	if err := h.store.SetMute(ctx, scope.ProfileID, kind, targetID, false, false); err != nil {
		return MuteResult{}, err
	}
	return MuteResult{
		Kind:     string(kind),
		TargetID: targetID,
		Muted:    false,
	}, nil
}

// ClearAllMutesResult echoes the count of mutes cleared — the My-Data "clear
// all" self-service reset. The count lets the client confirm the
// reset without a follow-up list read.
type ClearAllMutesResult struct {
	Cleared int `json:"cleared"`
}

// clearAllMutes removes every Mute for the caller's OWN Profile — the My-Data
// "clear all" reset. It reads the current list first only to
// report how many were cleared; the delete is profile-scoped (scope.ProfileID),
// so it can never touch another Profile's mutes.
func (h *Handler) clearAllMutes(ctx context.Context, scope auth.Scope) (ClearAllMutesResult, error) {
	existing, err := h.store.MutedTargets(ctx, scope.ProfileID)
	if err != nil {
		return ClearAllMutesResult{}, err
	}
	if err := h.store.ClearMutes(ctx, scope.ProfileID); err != nil {
		return ClearAllMutesResult{}, err
	}
	return ClearAllMutesResult{Cleared: len(existing)}, nil
}

// parseMuteTarget reads and validates the shared set/clear target arguments: a
// `kind` in the closed entity|tag vocabulary, a non-empty `targetID`, and the
// optional `includeDescendants` flag. An empty id or an out-of-vocabulary kind
// is a stable client error (mirroring the store's own validation, surfaced here
// as a client error so the client gets a 200-with-error rather than a generic
// validation failure).
func parseMuteTarget(req request) (store.MuteKind, string, bool, error) {
	kindRaw := trimmedVariable(req, "kind")
	targetID := trimmedVariable(req, "targetID")
	if targetID == "" {
		return "", "", false, clientError("targetID is required")
	}
	kind := store.MuteKind(kindRaw)
	if kind != store.MuteKindEntity && kind != store.MuteKindTag {
		return "", "", false, clientError("kind must be entity or tag")
	}
	return kind, targetID, boolVar(req, "includeDescendants"), nil
}
