package integrationhost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/beamfall/core/internal/causation"
	"github.com/beamfall/core/internal/store"
)

// Scoped Integration Worker writes and the playback-state read (CORE-1695).
//
// Every method here funnels through scopedCall, which fails closed in this
// order: the verified manifest declares the kind and the owner approved the
// grant (authorize); the method's seam and the shared Scope seam are wired; an
// audit ledger is wired; a profile-bound kind has a grant bound to one Profile;
// the grant's binding resolves into a Library filter. The caller then proves
// every target is visible through that filter; a write is then admitted by the
// causation cap, which binds the outbox rows it will append to the caller's
// chain (admitWrite, ADR-0125); the audit row is written, and only then is the
// seam called. A rejected call reserves nothing in the cap, and an audit
// failure aborts the call and releases its reservation, so no write lands
// unrecorded.

// ErrTargetNotFound is returned when a target media, collection or Library does
// not exist or lies outside the grant's Library scope. The two cases are not
// distinguished, so a Worker cannot probe for content it cannot see.
var ErrTargetNotFound = errors.New("integrationhost: target not found")

// Per-call bounds. A worker batch larger than these is refused before any
// lookup; artwork bytes must fit a 1 MiB wire frame after base64 encoding.
const (
	maxWriteMediaIDs     = 500
	maxMetadataFields    = 64
	maxCollectionNameLen = 200
	maxArtworkBytes      = 700 << 10
)

// ScopeReader resolves write targets through the grant's Library filter.
type ScopeReader interface {
	Media(ctx context.Context, id store.MediaID, filter store.LibraryFilter) (store.Media, error)
	LibraryVisible(ctx context.Context, id store.LibraryID, filter store.LibraryFilter) (bool, error)
	CollectionByID(ctx context.Context, id store.CollectionID, includeTrash bool) (store.Collection, error)
}

// MetadataWriter applies an undoable field edit (write-metadata permission).
type MetadataWriter interface {
	BulkEditApply(ctx context.Context, ids []store.MediaID, fields []store.MediaField, filter store.LibraryFilter, now time.Time) (store.BulkOpBatch, error)
}

// CollectionWriter creates collections and adds media (write-collections).
type CollectionWriter interface {
	CreateCollection(ctx context.Context, c store.Collection, now time.Time) (store.Collection, error)
	AddCollectionMedia(ctx context.Context, collectionID store.CollectionID, ids []store.MediaID, filter store.LibraryFilter, now time.Time) (store.Collection, error)
}

// ArtworkWriter imports artwork into a slot (write-artwork). A slot locked by
// local artwork is refused with the artwork service's slot-locked error.
type ArtworkWriter interface {
	ImportArtwork(ctx context.Context, id store.MediaID, slot string, data []byte) error
}

// WatchStateWriter marks one media item watched or unwatched for one Profile
// (write-watch-state). The composition root adapts the engagement write and
// the client invalidation, which it stamps with now: the Host binds that
// invalidation row (aggregate invalidation, the Profile's id, now) to the
// caller's causation chain. It notifies no other watched-state observers.
type WatchStateWriter interface {
	SetWatched(ctx context.Context, profileID store.ProfileID, mediaID store.MediaID, watched bool, filter store.LibraryFilter, now time.Time) error
}

// PlaybackReader reads one Profile's progress for one media item
// (read-playback-state).
type PlaybackReader interface {
	PlaybackProgressFor(ctx context.Context, profileID store.ProfileID, mediaID store.MediaID) (store.PlaybackProgress, bool, error)
}

// WriteRequest carries the arguments of the scoped write and playback-read wire
// methods. Each method reads only the fields it needs.
type WriteRequest struct {
	MediaID      string          `json:"mediaID,omitempty"`
	MediaIDs     []string        `json:"mediaIDs,omitempty"`
	CollectionID string          `json:"collectionID,omitempty"`
	LibraryID    string          `json:"libraryID,omitempty"`
	Name         string          `json:"name,omitempty"`
	Slot         string          `json:"slot,omitempty"`
	Image        []byte          `json:"image,omitempty"`
	Fields       []MetadataField `json:"fields,omitempty"`
	Watched      bool            `json:"watched,omitempty"`
}

// MetadataField is one field edit. Type is one of text, url, number, flag or
// date; the value field matching the type is used.
type MetadataField struct {
	Name   string     `json:"name"`
	Type   string     `json:"type"`
	Text   string     `json:"text,omitempty"`
	Number float64    `json:"number,omitempty"`
	Flag   bool       `json:"flag,omitempty"`
	Date   *time.Time `json:"date,omitempty"`
}

// PlaybackState is the playback-state read result. Found is false when the
// Profile has no progress for the media item.
type PlaybackState struct {
	MediaID         string    `json:"mediaID"`
	Found           bool      `json:"found"`
	PositionSeconds int       `json:"positionSeconds"`
	DurationSeconds int       `json:"durationSeconds"`
	Watched         bool      `json:"watched"`
	PlayCount       int       `json:"playCount"`
	UpdatedAt       time.Time `json:"updatedAt,omitempty"`
}

// scope is the resolved authority for one scoped call.
type scope struct {
	filter  store.LibraryFilter
	profile store.ProfileID
}

// maxStampRetries bounds how often admitWrite restamps a write's rows after a
// descendant-key collision; each retry stamps them 1 ms later. It exceeds one
// worker's action window, so a worker's own burst on one item cannot run out.
const maxStampRetries = 64

func (h *Host) scopedCall(ctx context.Context, kind string, seamWired bool) (scope, error) {
	grant, err := h.authorize(ctx, kind)
	if err != nil {
		return scope{}, err
	}
	if !seamWired || h.deps.Scope == nil {
		return scope{}, fmt.Errorf("%w: %s unavailable", ErrPermissionDenied, kind)
	}
	if h.deps.Audit == nil {
		return scope{}, fmt.Errorf("%w: %s requires an audit ledger", ErrPermissionDenied, kind)
	}
	if RequiresBoundProfile(kind) && (grant.BoundProfileID == "" || grant.AllLibraries) {
		return scope{}, fmt.Errorf("%w: %s requires a grant bound to one Profile", ErrPermissionDenied, kind)
	}
	filter, err := h.resolveFilter(ctx, grant)
	if err != nil {
		return scope{}, err
	}
	return scope{filter: filter, profile: grant.BoundProfileID}, nil
}

// admitWrite admits a validated scoped write through the shared causation cap.
// keysAt names the outbox rows the seam will append when it stamps them with a
// given time. Each key must be unbound, so on a collision with a row of another
// mutation the stamp moves 1 ms later and admission is asked again. It returns
// the stamp the seam must use and a release that unbinds the rows when the call
// stops before its seam runs.
func (h *Host) admitWrite(ctx context.Context, method string, keysAt func(time.Time) []string) (time.Time, func(), error) {
	if h.deps.Causation == nil {
		return time.Time{}, nil, fmt.Errorf("%w: %s containment unavailable", ErrPermissionDenied, method)
	}
	token := causation.TokenFromContext(ctx)
	now := h.now().UTC()
	for attempt := 0; ; attempt++ {
		keys := keysAt(now)
		err := h.deps.Causation.AdmitWithDescendants(h.pluginID, token, method, keys)
		if err == nil {
			return now, func() { h.deps.Causation.ReleaseDescendants(token, keys) }, nil
		}
		if !errors.Is(err, causation.ErrDescendantCollision) || attempt == maxStampRetries {
			return time.Time{}, nil, err
		}
		now = now.Add(time.Millisecond)
	}
}

// auditThenWrite writes the audit row for an admitted write, releasing its
// reservation when the audit fails so the seam never runs unrecorded.
func (h *Host) auditThenWrite(ctx context.Context, action string, sc scope, library store.LibraryID, release func()) error {
	if err := h.auditScoped(ctx, action, sc, library); err != nil {
		release()
		return err
	}
	return nil
}

// visibleLibrary resolves every media id through the filter and returns the one
// Library they share. Any id that is missing or hidden is ErrTargetNotFound; ids
// from more than one Library are refused so each call audits one Library.
func (h *Host) visibleLibrary(ctx context.Context, sc scope, ids []store.MediaID) (store.LibraryID, error) {
	var library store.LibraryID
	for _, id := range ids {
		if id == "" {
			return "", fmt.Errorf("%w: empty media id", ErrTargetNotFound)
		}
		media, err := h.deps.Scope.Media(ctx, id, sc.filter)
		if err != nil {
			return "", ErrTargetNotFound
		}
		if library != "" && media.LibraryID != library {
			return "", errors.New("integrationhost: media must share one Library per call")
		}
		library = media.LibraryID
	}
	return library, nil
}

func (h *Host) auditScoped(ctx context.Context, action string, sc scope, library store.LibraryID) error {
	return h.deps.Audit.AppendActivity(ctx, store.ActivityEntry{
		Action:     action,
		OccurredAt: h.now().UTC(),
		ProfileID:  sc.profile,
		TargetType: "plugin",
		TargetID:   h.pluginID,
		LibraryID:  library,
	})
}

// noOutboxRows admits a mutation whose seam appends no outbox row: the
// collection writes and the artwork import emit no event a Worker receives.
func noOutboxRows(time.Time) []string { return nil }

func mediaIDs(raw []string) ([]store.MediaID, error) {
	if len(raw) == 0 {
		return nil, errors.New("integrationhost: media ids are required")
	}
	if len(raw) > maxWriteMediaIDs {
		return nil, fmt.Errorf("integrationhost: too many media ids (%d > %d)", len(raw), maxWriteMediaIDs)
	}
	ids := make([]store.MediaID, len(raw))
	for i, id := range raw {
		ids[i] = store.MediaID(id)
	}
	return ids, nil
}

func metadataFields(raw []MetadataField) ([]store.MediaField, error) {
	if len(raw) == 0 {
		return nil, errors.New("integrationhost: metadata fields are required")
	}
	if len(raw) > maxMetadataFields {
		return nil, fmt.Errorf("integrationhost: too many metadata fields (%d > %d)", len(raw), maxMetadataFields)
	}
	fields := make([]store.MediaField, 0, len(raw))
	for _, f := range raw {
		field := store.MediaField{Name: f.Name, Type: store.EntityFieldType(f.Type), Text: f.Text, Number: f.Number, Flag: f.Flag}
		switch field.Type {
		case store.EntityFieldText, store.EntityFieldURL, store.EntityFieldNumber, store.EntityFieldFlag:
		case store.EntityFieldDate:
			if f.Date == nil {
				return nil, fmt.Errorf("integrationhost: date field %q has no date", f.Name)
			}
			field.Date = f.Date.UTC()
		default:
			return nil, fmt.Errorf("integrationhost: field %q has unknown type %q", f.Name, f.Type)
		}
		fields = append(fields, field)
	}
	return fields, nil
}

// WriteMetadata applies an undoable field edit to media in one Library the
// grant reveals (write-metadata, audited high-risk).
func (h *Host) WriteMetadata(ctx context.Context, rawIDs []string, rawFields []MetadataField) (store.BulkOpBatch, error) {
	ids, err := mediaIDs(rawIDs)
	if err != nil {
		return store.BulkOpBatch{}, err
	}
	fields, err := metadataFields(rawFields)
	if err != nil {
		return store.BulkOpBatch{}, err
	}
	sc, err := h.scopedCall(ctx, PermWriteMetadata, h.deps.Metadata != nil)
	if err != nil {
		return store.BulkOpBatch{}, err
	}
	library, err := h.visibleLibrary(ctx, sc, ids)
	if err != nil {
		return store.BulkOpBatch{}, err
	}
	now, release, err := h.admitWrite(ctx, methodWriteMetadata, func(now time.Time) []string {
		// BulkEditApply appends one media.updated row per id, stamped now.
		keys := make([]string, len(ids))
		for i, id := range ids {
			keys[i] = descendantKey(store.OutboxAggregateMedia, string(id), now)
		}
		return keys
	})
	if err != nil {
		return store.BulkOpBatch{}, err
	}
	if err := h.auditThenWrite(ctx, store.ActivityPluginMetadataWrite, sc, library, release); err != nil {
		return store.BulkOpBatch{}, err
	}
	return h.deps.Metadata.BulkEditApply(ctx, ids, fields, sc.filter, now)
}

// CreateCollection creates a top-level collection in a Library the grant
// reveals (write-collections, audited high-risk).
func (h *Host) CreateCollection(ctx context.Context, libraryID, name string) (store.Collection, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxCollectionNameLen {
		return store.Collection{}, fmt.Errorf("integrationhost: collection name must be 1-%d bytes", maxCollectionNameLen)
	}
	sc, err := h.scopedCall(ctx, PermWriteCollections, h.deps.Collections != nil)
	if err != nil {
		return store.Collection{}, err
	}
	library := store.LibraryID(libraryID)
	visible, err := h.deps.Scope.LibraryVisible(ctx, library, sc.filter)
	if err != nil || !visible {
		return store.Collection{}, ErrTargetNotFound
	}
	now, release, err := h.admitWrite(ctx, methodCreateCollection, noOutboxRows)
	if err != nil {
		return store.Collection{}, err
	}
	if err := h.auditThenWrite(ctx, store.ActivityPluginCollectionWrite, sc, library, release); err != nil {
		return store.Collection{}, err
	}
	return h.deps.Collections.CreateCollection(ctx, store.Collection{LibraryID: library, Name: name}, now)
}

// AddCollectionMedia adds visible media to a collection in a Library the grant
// reveals (write-collections, audited high-risk).
func (h *Host) AddCollectionMedia(ctx context.Context, collectionID string, rawIDs []string) (store.Collection, error) {
	ids, err := mediaIDs(rawIDs)
	if err != nil {
		return store.Collection{}, err
	}
	sc, err := h.scopedCall(ctx, PermWriteCollections, h.deps.Collections != nil)
	if err != nil {
		return store.Collection{}, err
	}
	if collectionID == "" {
		return store.Collection{}, ErrTargetNotFound
	}
	collection, err := h.deps.Scope.CollectionByID(ctx, store.CollectionID(collectionID), false)
	if err != nil {
		return store.Collection{}, ErrTargetNotFound
	}
	visible, err := h.deps.Scope.LibraryVisible(ctx, collection.LibraryID, sc.filter)
	if err != nil || !visible {
		return store.Collection{}, ErrTargetNotFound
	}
	library, err := h.visibleLibrary(ctx, sc, ids)
	if err != nil {
		return store.Collection{}, err
	}
	if library != collection.LibraryID {
		return store.Collection{}, errors.New("integrationhost: media must be in the collection's Library")
	}
	now, release, err := h.admitWrite(ctx, methodAddCollectionMedia, noOutboxRows)
	if err != nil {
		return store.Collection{}, err
	}
	if err := h.auditThenWrite(ctx, store.ActivityPluginCollectionWrite, sc, library, release); err != nil {
		return store.Collection{}, err
	}
	return h.deps.Collections.AddCollectionMedia(ctx, collection.ID, ids, sc.filter, now)
}

// SetArtwork imports artwork into one slot of a visible media item
// (write-artwork, audited high-risk). Locked slots are never replaced.
func (h *Host) SetArtwork(ctx context.Context, mediaID, slot string, image []byte) error {
	if len(image) == 0 || len(image) > maxArtworkBytes {
		return fmt.Errorf("integrationhost: artwork must be 1-%d bytes", maxArtworkBytes)
	}
	sc, err := h.scopedCall(ctx, PermWriteArtwork, h.deps.Artwork != nil)
	if err != nil {
		return err
	}
	library, err := h.visibleLibrary(ctx, sc, []store.MediaID{store.MediaID(mediaID)})
	if err != nil {
		return err
	}
	_, release, err := h.admitWrite(ctx, methodSetArtwork, noOutboxRows)
	if err != nil {
		return err
	}
	if err := h.auditThenWrite(ctx, store.ActivityPluginArtworkWrite, sc, library, release); err != nil {
		return err
	}
	return h.deps.Artwork.ImportArtwork(ctx, store.MediaID(mediaID), slot, image)
}

// SetWatched marks one visible media item watched or unwatched for the grant's
// bound Profile (write-watch-state, audited high-risk).
func (h *Host) SetWatched(ctx context.Context, mediaID string, watched bool) error {
	sc, err := h.scopedCall(ctx, PermWriteWatchState, h.deps.WatchState != nil)
	if err != nil {
		return err
	}
	library, err := h.visibleLibrary(ctx, sc, []store.MediaID{store.MediaID(mediaID)})
	if err != nil {
		return err
	}
	now, release, err := h.admitWrite(ctx, methodSetWatched, func(now time.Time) []string {
		// The adapter appends the bound Profile's invalidation row, stamped now.
		return []string{descendantKey(store.OutboxAggregateInvalidation, string(sc.profile), now)}
	})
	if err != nil {
		return err
	}
	if err := h.auditThenWrite(ctx, store.ActivityPluginWatchStateWrite, sc, library, release); err != nil {
		return err
	}
	return h.deps.WatchState.SetWatched(ctx, sc.profile, store.MediaID(mediaID), watched, sc.filter, now)
}

// ReadPlaybackState reads the grant's bound Profile's progress for one visible
// media item (read-playback-state, audited high-risk).
func (h *Host) ReadPlaybackState(ctx context.Context, mediaID string) (PlaybackState, error) {
	sc, err := h.scopedCall(ctx, PermReadPlaybackState, h.deps.Playback != nil)
	if err != nil {
		return PlaybackState{}, err
	}
	library, err := h.visibleLibrary(ctx, sc, []store.MediaID{store.MediaID(mediaID)})
	if err != nil {
		return PlaybackState{}, err
	}
	if err := h.auditScoped(ctx, store.ActivityPluginPlaybackStateRead, sc, library); err != nil {
		return PlaybackState{}, err
	}
	progress, found, err := h.deps.Playback.PlaybackProgressFor(ctx, sc.profile, store.MediaID(mediaID))
	if err != nil {
		return PlaybackState{}, err
	}
	state := PlaybackState{MediaID: mediaID, Found: found}
	if found {
		state.PositionSeconds = progress.PositionSeconds
		state.DurationSeconds = progress.DurationSeconds
		state.Watched = progress.Watched
		state.PlayCount = progress.PlayCount
		state.UpdatedAt = progress.UpdatedAt
	}
	return state, nil
}

// dispatchWrite routes the scoped write and playback-read wire methods. The
// caller has already attached the request's causation token to ctx.
func (s *Supervisor) dispatchWrite(ctx context.Context, method string, req *WriteRequest) hostResponse {
	if req == nil {
		req = &WriteRequest{}
	}
	switch method {
	case methodWriteMetadata:
		batch, err := s.host.WriteMetadata(ctx, req.MediaIDs, req.Fields)
		return jsonResp(batch, err)
	case methodCreateCollection:
		collection, err := s.host.CreateCollection(ctx, req.LibraryID, req.Name)
		return jsonResp(collection, err)
	case methodAddCollectionMedia:
		collection, err := s.host.AddCollectionMedia(ctx, req.CollectionID, req.MediaIDs)
		return jsonResp(collection, err)
	case methodSetArtwork:
		return jsonResp(struct{}{}, s.host.SetArtwork(ctx, req.MediaID, req.Slot, req.Image))
	case methodSetWatched:
		return jsonResp(struct{}{}, s.host.SetWatched(ctx, req.MediaID, req.Watched))
	case methodReadPlaybackState:
		state, err := s.host.ReadPlaybackState(ctx, req.MediaID)
		return jsonResp(state, err)
	default:
		return hostResponse{Err: "unknown method"}
	}
}
