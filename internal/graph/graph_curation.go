package graph

import (
	"context"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

type MediaFileInfo struct {
	MediaID            string `json:"mediaID"`
	Path               string `json:"path"`
	ContentFingerprint string `json:"contentFingerprint"`
	VersionLabel       string `json:"versionLabel"`
	PartOrdinal        int    `json:"partOrdinal"`
	OSHash             string `json:"osHash,omitempty"`
	PHash              string `json:"pHash,omitempty"`
}

type EntityRelationInfo struct {
	Relation   string            `json:"relation"`
	TargetKind string            `json:"targetKind"`
	Entity     *EntityInfo       `json:"entity,omitempty"`
	Media      *MediaInfo        `json:"media,omitempty"`
	Attributes map[string]string `json:"attributes"`
	SortOrder  *int              `json:"sortOrder,omitempty"`
}

type MediaRelationInfo struct {
	Relation string    `json:"relation"`
	Media    MediaInfo `json:"media"`
}

type RelationWriteResult struct {
	Relation string `json:"relation"`
	SourceID string `json:"sourceID"`
	TargetID string `json:"targetID"`
}

const (
	curationPageDefault = 50
	curationPageMax     = 200
	curationOffsetMax   = 10_000
)

func (h *Handler) mediaFiles(ctx context.Context, scope auth.Scope, req request) ([]MediaFileInfo, error) {
	mediaID := store.MediaID(trimmedVariable(req, "mediaID"))
	if mediaID == "" {
		return nil, clientError("mediaID is required")
	}
	files, err := h.store.MediaFilesForMedia(ctx, mediaID, scope.LibraryFilter())
	if err != nil {
		return nil, clientError("unknown media")
	}
	out := make([]MediaFileInfo, len(files))
	for i, file := range files {
		out[i] = mediaFileInfo(file)
	}
	return out, nil
}

func (h *Handler) entityRelations(ctx context.Context, scope auth.Scope, req request) ([]EntityRelationInfo, error) {
	entityID := store.EntityID(trimmedVariable(req, "entityID"))
	relation := trimmedVariable(req, "relation")
	if entityID == "" {
		return nil, clientError("entityID is required")
	}
	limit := boundedIntVar(req, "limit", curationPageDefault, curationPageMax)
	offset := boundedNonNegativeInt(nonNegativeIntVar(req, "offset"), curationOffsetMax)
	rows, err := h.store.EntityRelationRows(ctx, entityID, relation, scope.LibraryFilter(), limit, offset)
	if err != nil {
		return nil, clientError("unknown entity")
	}
	mediaIDs := make([]store.MediaID, 0, len(rows))
	for _, row := range rows {
		if row.ToMedia != nil {
			mediaIDs = append(mediaIDs, row.ToMedia.ID)
		}
	}
	favorites, err := h.store.MediaFavoritedSet(ctx, scope.ProfileID, mediaIDs)
	if err != nil {
		return nil, err
	}
	out := make([]EntityRelationInfo, 0, len(rows))
	type kindKey struct {
		libraryID store.LibraryID
		name      string
	}
	kinds := map[kindKey]plugin.EntityKind{}
	for _, row := range rows {
		info := EntityRelationInfo{
			Relation:   row.Name,
			Attributes: row.Attributes,
			SortOrder:  row.SortOrder,
		}
		if info.Attributes == nil {
			info.Attributes = map[string]string{}
		}
		if row.ToEntity != nil {
			key := kindKey{libraryID: row.ToEntity.LibraryID, name: row.ToEntity.Kind}
			kind, ok := kinds[key]
			if !ok {
				kind = h.entityKindSpec(ctx, key.libraryID, key.name)
				kinds[key] = kind
			}
			entity := entityInfo(*row.ToEntity, kind)
			info.TargetKind = "entity"
			info.Entity = &entity
		}
		if row.ToMedia != nil {
			media := mediaInfoWithFavorite(*row.ToMedia, favorites[row.ToMedia.ID])
			info.TargetKind = "media"
			info.Media = &media
		}
		out = append(out, info)
	}
	return out, nil
}

func (h *Handler) mediaRelations(ctx context.Context, scope auth.Scope, req request) ([]MediaRelationInfo, error) {
	mediaID := store.MediaID(trimmedVariable(req, "mediaID"))
	relation := store.MediaRelationName(trimmedVariable(req, "relation"))
	if mediaID == "" || relation == "" {
		return nil, clientError("mediaID and relation are required")
	}
	limit := boundedIntVar(req, "limit", curationPageDefault, curationPageMax)
	offset := boundedNonNegativeInt(nonNegativeIntVar(req, "offset"), curationOffsetMax)
	related, err := h.store.MediaRelationsOf(ctx, mediaID, relation, scope.LibraryFilter(), limit, offset)
	if err != nil {
		return nil, clientError("unknown media")
	}
	ids := make([]store.MediaID, len(related))
	for i := range related {
		ids[i] = related[i].ID
	}
	favorites, err := h.store.MediaFavoritedSet(ctx, scope.ProfileID, ids)
	if err != nil {
		return nil, err
	}
	out := make([]MediaRelationInfo, len(related))
	for i, media := range related {
		out[i] = MediaRelationInfo{Relation: string(relation), Media: mediaInfoWithFavorite(media, favorites[media.ID])}
	}
	return out, nil
}

func mediaInfoWithFavorite(media store.Media, favorite bool) MediaInfo {
	return MediaInfo{
		ID: string(media.ID), Title: media.Title, Summary: media.Summary,
		Year: media.Year, Favorited: favorite,
	}
}

func (h *Handler) upsertEntityRelation(ctx context.Context, scope auth.Scope, req request) (RelationWriteResult, error) {
	fromEntityID := store.EntityID(trimmedVariable(req, "fromEntityID"))
	relation := trimmedVariable(req, "relation")
	toEntityID := store.EntityID(trimmedVariable(req, "toEntityID"))
	toMediaID := store.MediaID(trimmedVariable(req, "toMediaID"))
	if fromEntityID == "" || relation == "" {
		return RelationWriteResult{}, clientError("fromEntityID and relation are required")
	}
	if (toEntityID == "") == (toMediaID == "") {
		return RelationWriteResult{}, clientError("exactly one relation target is required")
	}
	source, err := h.store.Entity(ctx, fromEntityID, scope.LibraryFilter())
	if err != nil {
		return RelationWriteResult{}, clientError("unknown entity")
	}
	var targetID string
	if toEntityID != "" {
		if _, err := h.store.Entity(ctx, toEntityID, scope.LibraryFilter()); err != nil {
			return RelationWriteResult{}, clientError("unknown target entity")
		}
		targetID = string(toEntityID)
	}
	if toMediaID != "" {
		if _, err := h.store.Media(ctx, toMediaID, scope.LibraryFilter()); err != nil {
			return RelationWriteResult{}, clientError("unknown target media")
		}
		targetID = string(toMediaID)
	}
	sortOrder, err := optionalInt(req, "sortOrder")
	if err != nil {
		return RelationWriteResult{}, err
	}
	if err := h.store.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: fromEntityID,
		Name:         relation,
		ToEntityID:   toEntityID,
		ToMediaID:    toMediaID,
		SortOrder:    sortOrder,
	}); err != nil {
		return RelationWriteResult{}, clientError(err.Error())
	}
	if err := h.logActivity(ctx, scope, store.ActivityEntityRelation, "entity", string(fromEntityID), source.LibraryID); err != nil {
		return RelationWriteResult{}, err
	}
	return RelationWriteResult{Relation: relation, SourceID: string(fromEntityID), TargetID: targetID}, nil
}

func (h *Handler) linkMediaRelation(ctx context.Context, scope auth.Scope, req request) (RelationWriteResult, error) {
	fromMediaID := store.MediaID(trimmedVariable(req, "fromMediaID"))
	relation := store.MediaRelationName(trimmedVariable(req, "relation"))
	toMediaID := store.MediaID(trimmedVariable(req, "toMediaID"))
	if fromMediaID == "" || relation == "" || toMediaID == "" {
		return RelationWriteResult{}, clientError("fromMediaID, relation, and toMediaID are required")
	}
	source, err := h.store.Media(ctx, fromMediaID, scope.LibraryFilter())
	if err != nil {
		return RelationWriteResult{}, clientError("unknown media")
	}
	if _, err := h.store.Media(ctx, toMediaID, scope.LibraryFilter()); err != nil {
		return RelationWriteResult{}, clientError("unknown target media")
	}
	if err := h.store.LinkMediaRelation(ctx, fromMediaID, relation, toMediaID); err != nil {
		return RelationWriteResult{}, clientError(err.Error())
	}
	if err := h.logActivity(ctx, scope, store.ActivityMediaRelation, "media", string(fromMediaID), source.LibraryID); err != nil {
		return RelationWriteResult{}, err
	}
	return RelationWriteResult{Relation: string(relation), SourceID: string(fromMediaID), TargetID: string(toMediaID)}, nil
}

func mediaFileInfo(file store.MediaFile) MediaFileInfo {
	return MediaFileInfo{
		MediaID:            string(file.MediaID),
		Path:               file.Path,
		ContentFingerprint: file.ContentFingerprint,
		VersionLabel:       file.VersionLabel,
		PartOrdinal:        file.PartOrdinal,
		OSHash:             file.OSHash,
		PHash:              file.PHash,
	}
}

func optionalInt(req request, name string) (*int, error) {
	n, ok := newVars(req).Number(name)
	if !ok {
		return nil, nil
	}
	i := int(n)
	if float64(i) != n {
		return nil, clientError(name + " must be an integer")
	}
	return &i, nil
}
