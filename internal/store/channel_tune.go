package store

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"math/rand"
	"strings"
)

// decodeChannelSourceExpr decodes a Channel's opaque source expression into a
// SourceQuery. Unlike decodeSmartCollectionSourceExpr, a Channel MAY reference a
// smart collection (kind "smart-collection", value = collection ID) as well as
// carry an inline query — both source forms
// resolve through the same SourceItems evaluator. An empty expression defaults
// to the whole-Library feed.
func decodeChannelSourceExpr(expr string) (SourceQuery, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return SourceQuery{Kind: "all-items"}, nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(expr), &raw); err != nil {
		return SourceQuery{}, ValidationError("sourceExpr is not valid JSON")
	}
	query := SourceQuery{Kind: stringFromRaw(raw["kind"])}
	if query.Kind == "" {
		query.Kind = "all-items"
	}
	query.EntityKind = stringFromRaw(raw["entityKind"])
	query.EntityID = EntityID(stringFromRaw(raw["entityID"]))
	query.LibraryID = LibraryID(stringFromRaw(raw["libraryID"]))
	query.Seed = stringFromRaw(raw["seed"])
	query.Value = strings.TrimSpace(stringFromRaw(raw["value"]))
	if fields, ok := raw["fieldFilters"].(map[string]any); ok {
		query.FieldFilters = fields
	}
	if entityRaw, ok := raw["entityFilter"].(map[string]any); ok {
		kind := strings.TrimSpace(stringFromRaw(entityRaw["kind"]))
		name := strings.TrimSpace(stringFromRaw(entityRaw["name"]))
		if kind == "" || name == "" {
			return SourceQuery{}, ValidationError("entityFilter requires kind and name")
		}
		query.EntityFilter = &EntityFilter{Kind: kind, Name: name}
	}
	return query, nil
}

// ChannelUsableByProfile reports whether profileID may use Channel c — tune it,
// edit it, or delete it. A server-shared Channel (empty ProfileID) is usable by
// any session that can see its Library; a per-Profile Channel is usable only by
// its owning Profile. Library visibility alone is not sufficient, because
// Profiles sharing a Library must not reach each other's private Channels.
//
// This is the single ownership rule for Channels: the graph layer's
// channelEditable seam delegates here so the tune path and the edit/delete path
// can never drift.
func ChannelUsableByProfile(profileID ProfileID, c Channel) bool {
	if c.ProfileID == "" {
		return true
	}
	return c.ProfileID == profileID
}

// channelTuneWindow caps how many source items a single tune compiles into the
// Play Queue window. A Channel is a rolling feed, not a persisted timeline,
// so one tune installs a bounded window.
const channelTuneWindow = 200

// TuneChannel compiles a Channel into an ordinary Play Queue (ADR-0158
// Decision 3) and returns the installed, playable window. It:
//
//  1. resolves the Channel's source (a smart-collection reference or inline
//     query) through the standard SourceItems evaluator, so the source is
//     evaluated in the tuning Profile's server-side policy context (the caller's
//     LibraryFilter — Reveal/Library access/Sealed all apply, ADR-0158
//     Decision 6);
//  2. applies the Channel's ordering policy (a seeded shuffle keyed to the
//     Channel so a tune is reproducible, or the natural chronological feed
//     order); and
//  3. installs the ordered media as the Profile's Play Queue via the same
//     queue-replace path a Station uses — NO new queue verb or section kind.
//
// The result is an ordinary Play Queue: a client that has never heard of
// Channels reads and plays it unchanged. confirmReplace is threaded to the
// queue-replace confirmation contract (a non-empty visible queue needs a yes).
func (s *Store) TuneChannel(ctx context.Context, profileID ProfileID, channelID ChannelID, confirmReplace bool, filter LibraryFilter) ([]SourceItem, error) {
	if profileID == "" {
		return nil, ValidationError("profile ID is required")
	}
	channel, err := s.ChannelByID(ctx, channelID)
	if err != nil {
		return nil, err
	}
	// A Channel in a Library the caller cannot see is refused before ownership
	// is even considered — mirroring the graph layer's updateChannel/deleteChannel
	// order (visibleLibrary before channelEditable) so tuning can never resolve a
	// hidden Library's Channel to an empty window and clobber the Play Queue.
	// Denial is indistinguishable from a missing Channel so IDs cannot be probed.
	visible, err := s.LibraryVisible(ctx, channel.LibraryID, filter)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, ErrChannelNotFound
	}
	// A Channel's configuration (source expression, ordering, repeat) is honored
	// on tune, so ownership is checked on the Channel itself — the caller's
	// LibraryFilter only scopes the resolved media, not whose Channel this is.
	// Denial is indistinguishable from a missing Channel so IDs cannot be probed.
	if !ChannelUsableByProfile(profileID, channel) {
		return nil, ErrChannelNotFound
	}
	query, err := decodeChannelSourceExpr(channel.SourceExpr)
	if err != nil {
		return nil, err
	}
	if query.LibraryID != "" && query.LibraryID != channel.LibraryID {
		return nil, ValidationError("channel source libraryID must match channel library")
	}
	query.LibraryID = channel.LibraryID
	query.ProfileID = profileID
	query.Limit = channelTuneWindow
	items, err := s.SourceItems(ctx, filter, query)
	if err != nil {
		return nil, err
	}
	ordered := orderChannelItems(channel, items)
	mediaIDs := make([]MediaID, len(ordered))
	for i, item := range ordered {
		mediaIDs[i] = item.ID
	}
	if err := s.QueueReplaceWithStation(ctx, profileID, mediaIDs, confirmReplace, filter); err != nil {
		return nil, err
	}
	return s.SourceItems(ctx, filter, SourceQuery{Kind: "queue", ProfileID: profileID})
}

// orderChannelItems applies a Channel's ordering policy over the resolved source
// items. chronological keeps the source's natural feed order; shuffle applies a
// deterministic permutation seeded from the Channel ID, so re-tuning the same
// Channel reproduces the same order (the seed input for ADR-0158 Decision 5's
// simulated linearity, computed here without a persisted timeline).
func orderChannelItems(channel Channel, items []SourceItem) []SourceItem {
	if channel.Ordering != ChannelOrderingShuffle || len(items) < 2 {
		return items
	}
	ordered := make([]SourceItem, len(items))
	copy(ordered, items)
	seed := fnv.New64a()
	_, _ = seed.Write([]byte(channel.ID))
	rng := rand.New(rand.NewSource(int64(seed.Sum64()))) //nolint:gosec // G404: ID-seeded deterministic shuffle, not security-sensitive (ADR-0158 Decision 5)
	rng.Shuffle(len(ordered), func(i, j int) { ordered[i], ordered[j] = ordered[j], ordered[i] })
	return ordered
}
