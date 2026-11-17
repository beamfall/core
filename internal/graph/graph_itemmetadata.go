package graph

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/store"
)

// Per-item metadata editor, graph half (CORE-1715). A manager reads one
// visible Media's editable built-in metadata with its field locks, edits it
// (an edit writes at the manual source, which locks every field it touches),
// and refreshes it: the identification provider that last supplied the
// metadata is asked again for the same candidate and its values land only on
// unlocked fields. Choosing a different candidate stays with Fix Match.
// Plugin-declared fields are edited through bulkEditApply, which also writes
// at the manual source. Reads and writes are reveal-and-Library filtered, so a
// hidden Media is "not found".

// MediaMetadataInfo is one Media's editable built-in metadata on the wire.
type MediaMetadataInfo struct {
	MediaID   string `json:"mediaID"`
	Title     string `json:"title"`
	SortTitle string `json:"sortTitle"`
	Summary   string `json:"summary"`
	Year      int    `json:"year"`
	// Refreshable reports whether a per-item refresh can re-run a provider:
	// false until an identification recorded the candidate it applied.
	Refreshable bool            `json:"refreshable"`
	Locks       []FieldLockInfo `json:"locks"`
}

// mediaMetadata resolves the editor read.
func (h *Handler) mediaMetadata(ctx context.Context, scope auth.Scope, req request) (MediaMetadataInfo, error) {
	if h.store == nil {
		return MediaMetadataInfo{}, clientError("store unavailable")
	}
	mediaID := trimmedVariable(req, "mediaID")
	if mediaID == "" {
		return MediaMetadataInfo{}, clientError("mediaID is required")
	}
	return h.mediaMetadataInfo(ctx, scope, store.MediaID(mediaID))
}

// editMediaMetadata resolves the edit: every supplied field is written at the
// manual source and so locked; an absent field is left alone. An empty
// sortTitle clears it, and year 0 clears the year.
func (h *Handler) editMediaMetadata(ctx context.Context, scope auth.Scope, req request) (MediaMetadataInfo, error) {
	if h.store == nil {
		return MediaMetadataInfo{}, clientError("store unavailable")
	}
	mediaID := store.MediaID(trimmedVariable(req, "mediaID"))
	if mediaID == "" {
		return MediaMetadataInfo{}, clientError("mediaID is required")
	}
	v := newVars(req)
	metadata := store.MediaMetadata{Source: store.MutationSourceManual}
	if title, ok := v.String("title"); ok {
		metadata.Title = &title
	}
	if sortTitle, ok := v.String("sortTitle"); ok {
		metadata.SortTitle = &sortTitle
	}
	if summary, ok := v.String("summary"); ok {
		metadata.Summary = &summary
	}
	if year, ok := v.Number("year"); ok {
		if year != math.Trunc(year) || year < 0 || year > 9999 {
			return MediaMetadataInfo{}, clientError("year must be a whole number from 0 to 9999")
		}
		y := int(year)
		metadata.Year = &y
	}
	if err := v.Err(); err != nil {
		return MediaMetadataInfo{}, err
	}
	if metadata.Title == nil && metadata.SortTitle == nil && metadata.Summary == nil && metadata.Year == nil {
		return MediaMetadataInfo{}, clientError("at least one field is required")
	}
	if metadata.Title != nil && strings.TrimSpace(*metadata.Title) == "" {
		return MediaMetadataInfo{}, clientError("title cannot be empty")
	}
	if _, err := h.store.Media(ctx, mediaID, scope.LibraryFilter()); err != nil {
		return MediaMetadataInfo{}, clientError("unknown media")
	}
	if err := h.store.UpdateMediaMetadata(ctx, mediaID, metadata); err != nil {
		return MediaMetadataInfo{}, err
	}
	h.logMediaActivity(ctx, scope, store.ActivityMediaMetadataEdit, mediaID)
	return h.mediaMetadataInfo(ctx, scope, mediaID)
}

// refreshMediaMetadata resolves the per-item refresh: it re-applies the
// candidate the last identification applied, through the same provider. The
// write is at the operator source, which a manual (locked) field outranks, so
// locked fields and locked artwork slots keep their values.
func (h *Handler) refreshMediaMetadata(ctx context.Context, scope auth.Scope, req request) (MediaMetadataInfo, error) {
	if h.store == nil {
		return MediaMetadataInfo{}, clientError("store unavailable")
	}
	if h.identify == nil {
		return MediaMetadataInfo{}, clientError("identification unavailable")
	}
	mediaID := store.MediaID(trimmedVariable(req, "mediaID"))
	if mediaID == "" {
		return MediaMetadataInfo{}, clientError("mediaID is required")
	}
	if _, err := h.store.Media(ctx, mediaID, scope.LibraryFilter()); err != nil {
		return MediaMetadataInfo{}, clientError("unknown media")
	}
	if err := h.identify.Refresh(ctx, mediaID); err != nil {
		if errors.Is(err, identify.ErrNothingToRefresh) {
			return MediaMetadataInfo{}, clientError("no identification to refresh; use Fix Match first")
		}
		return MediaMetadataInfo{}, err
	}
	h.logMediaActivity(ctx, scope, store.ActivityMediaMetadataRefresh, mediaID)
	return h.mediaMetadataInfo(ctx, scope, mediaID)
}

// logMediaActivity audits an editor write against the Media's Library, the
// same reveal-filtered shape as a field-lock change. The write already
// landed, so a missing Library or a failed append only skips the audit row.
func (h *Handler) logMediaActivity(ctx context.Context, scope auth.Scope, kind string, mediaID store.MediaID) {
	library, err := h.store.MediaLibrary(ctx, mediaID)
	if err != nil {
		return
	}
	_ = h.logActivity(ctx, scope, kind, "media", string(mediaID), library)
}

func (h *Handler) mediaMetadataInfo(ctx context.Context, scope auth.Scope, mediaID store.MediaID) (MediaMetadataInfo, error) {
	media, err := h.store.Media(ctx, mediaID, scope.LibraryFilter())
	if err != nil {
		return MediaMetadataInfo{}, clientError("unknown media")
	}
	sortTitle, err := h.store.MediaSortTitle(ctx, mediaID, scope.LibraryFilter())
	if err != nil {
		return MediaMetadataInfo{}, err
	}
	locks, err := h.store.MediaFieldLocks(ctx, mediaID, scope.LibraryFilter())
	if err != nil {
		return MediaMetadataInfo{}, err
	}
	record, ok, err := h.store.MediaMetadataProvenance(ctx, mediaID)
	if err != nil {
		return MediaMetadataInfo{}, err
	}
	return MediaMetadataInfo{
		MediaID:     string(media.ID),
		Title:       media.Title,
		SortTitle:   sortTitle,
		Summary:     media.Summary,
		Year:        media.Year,
		Refreshable: ok && record.CandidateID != "" && h.identify != nil,
		Locks:       fieldLockInfos(locks),
	}, nil
}
