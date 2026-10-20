package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// ChannelInfo is the wire shape for one Channel. A Channel is a
// GENERIC STRUCTURAL object — it wraps an opaque source expression (a
// smart-collection reference or an inline query) plus an ordering and repeat
// policy — never a domain entity (invariants #1/#2). The wire media entity a
// tuned Channel yields stays `Media`; this type carries no domain vocabulary.
type ChannelInfo struct {
	ID         string `json:"id"`
	LibraryID  string `json:"libraryID"`
	ProfileID  string `json:"profileID"`
	Name       string `json:"name"`
	SourceExpr string `json:"sourceExpr"`
	Ordering   string `json:"ordering"`
	Repeat     string `json:"repeat"`
	CreatedAt  string `json:"createdAt,omitempty"`
}

func channelInfo(c store.Channel) ChannelInfo {
	return ChannelInfo{
		ID:         string(c.ID),
		LibraryID:  string(c.LibraryID),
		ProfileID:  string(c.ProfileID),
		Name:       c.Name,
		SourceExpr: c.SourceExpr,
		Ordering:   string(c.Ordering),
		Repeat:     string(c.Repeat),
		CreatedAt:  formatOptionalTime(c.CreatedAt),
	}
}

// channels returns the Channels visible to the current Profile in a Library:
// the Profile's own per-Profile Channels plus every server-shared Channel.
func (h *Handler) channels(ctx context.Context, scope auth.Scope, req request) ([]ChannelInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return nil, clientError("libraryID is required")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, clientError("unknown library")
	}
	channels, err := h.store.Channels(ctx, scope.ProfileID, store.LibraryID(libraryID))
	if err != nil {
		return nil, err
	}
	infos := make([]ChannelInfo, len(channels))
	for i, c := range channels {
		infos[i] = channelInfo(c)
	}
	return infos, nil
}

// createChannel creates a per-Profile Channel for a visible Library, edited via
// the existing collection-editor surface (no new editor). Manager role is
// required (gated in the dispatch block), mirroring smart-collection edits.
func (h *Handler) createChannel(ctx context.Context, scope auth.Scope, req request) (ChannelInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	name := stringVar(req, "name")
	if libraryID == "" {
		return ChannelInfo{}, clientError("libraryID is required")
	}
	if name == "" {
		return ChannelInfo{}, clientError("name is required")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return ChannelInfo{}, err
	}
	if !visible {
		return ChannelInfo{}, clientError("unknown library")
	}
	c, err := h.store.CreateChannel(ctx, store.Channel{
		ProfileID:  scope.ProfileID,
		LibraryID:  store.LibraryID(libraryID),
		Name:       name,
		SourceExpr: stringVar(req, "sourceExpr"),
		Ordering:   store.ChannelOrdering(trimmedVariable(req, "ordering")),
		Repeat:     store.ChannelRepeat(trimmedVariable(req, "repeat")),
	}, time.Now().UTC())
	if err != nil {
		return ChannelInfo{}, err
	}
	return channelInfo(c), nil
}

// channelEditable reports whether scope may edit c. A server-shared Channel
// (empty ProfileID) is editable by any session that can see its Library; a
// per-Profile Channel is editable only by its owning Profile. Library
// visibility alone is not sufficient, because Profiles sharing a Library must
// not reach each other's private Channels. The rule itself lives in the store
// (store.ChannelUsableByProfile) so the edit seam and the tune path share one
// definition of Channel ownership.
func channelEditable(scope auth.Scope, c store.Channel) bool {
	return store.ChannelUsableByProfile(scope.ProfileID, c)
}

// updateChannel edits a Channel's metadata, source expression, and policies for
// a visible Library the calling Profile owns the Channel in.
func (h *Handler) updateChannel(ctx context.Context, scope auth.Scope, req request) (ChannelInfo, error) {
	id := trimmedVariable(req, "id")
	if id == "" {
		return ChannelInfo{}, clientError("id is required")
	}
	c, err := h.store.ChannelByID(ctx, store.ChannelID(id))
	if err != nil {
		return ChannelInfo{}, clientError("unknown channel")
	}
	visible, err := h.visibleLibrary(ctx, scope, c.LibraryID)
	if err != nil {
		return ChannelInfo{}, err
	}
	if !visible {
		return ChannelInfo{}, clientError("unknown channel")
	}
	if !channelEditable(scope, c) {
		return ChannelInfo{}, clientError("unknown channel")
	}
	updated, err := h.store.UpdateChannel(ctx, store.Channel{
		ID:         store.ChannelID(id),
		Name:       stringVar(req, "name"),
		SourceExpr: stringVar(req, "sourceExpr"),
		Ordering:   store.ChannelOrdering(trimmedVariable(req, "ordering")),
		Repeat:     store.ChannelRepeat(trimmedVariable(req, "repeat")),
	})
	if err != nil {
		return ChannelInfo{}, err
	}
	return channelInfo(updated), nil
}

// deleteChannel removes a Channel from a visible Library the calling Profile
// owns the Channel in.
func (h *Handler) deleteChannel(ctx context.Context, scope auth.Scope, req request) (ChannelInfo, error) {
	id := trimmedVariable(req, "id")
	if id == "" {
		return ChannelInfo{}, clientError("id is required")
	}
	c, err := h.store.ChannelByID(ctx, store.ChannelID(id))
	if err != nil {
		return ChannelInfo{}, clientError("unknown channel")
	}
	visible, err := h.visibleLibrary(ctx, scope, c.LibraryID)
	if err != nil {
		return ChannelInfo{}, err
	}
	if !visible {
		return ChannelInfo{}, clientError("unknown channel")
	}
	if !channelEditable(scope, c) {
		return ChannelInfo{}, clientError("unknown channel")
	}
	removed, err := h.store.DeleteChannel(ctx, store.ChannelID(id))
	if err != nil {
		return ChannelInfo{}, clientError("unknown channel")
	}
	if err := h.logActivity(ctx, scope, store.ActivityChannelDelete, "channel", string(removed.ID), removed.LibraryID); err != nil {
		return ChannelInfo{}, err
	}
	return channelInfo(removed), nil
}
