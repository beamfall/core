package graph

import (
	"context"
	"errors"
	"strings"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/l10n"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

type EntitySearchInfo struct {
	ID        string `json:"id"`
	LibraryID string `json:"libraryID"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
}

func (h *Handler) entitySearch(ctx context.Context, scope auth.Scope, req request) ([]EntitySearchInfo, error) {
	vars := newVars(req)
	query, hasQuery := vars.String("query")
	kind := trimmedVariable(req, "kind")
	if kind == "" || !hasQuery {
		return nil, relationInputError(clientError("kind and query are required"))
	}
	entities, err := h.store.SearchEntitiesByName(ctx, scope.LibraryFilter(), kind, strings.TrimSpace(query), boundedIntVar(req, "limit", 50, 50))
	if err != nil {
		return nil, err
	}
	out := make([]EntitySearchInfo, 0, len(entities))
	for _, entity := range entities {
		out = append(out, EntitySearchInfo{ID: string(entity.ID), LibraryID: string(entity.LibraryID), Kind: entity.Kind, Title: entity.Title})
	}
	return out, nil
}

func (h *Handler) setMediaRelation(ctx context.Context, scope auth.Scope, req request) (RelationWriteResult, error) {
	media, err := h.operatorRelationMedia(ctx, scope, req)
	if err != nil {
		return RelationWriteResult{}, err
	}
	name := trimmedVariable(req, "relation")
	if name == "" {
		return RelationWriteResult{}, relationInputError(clientError("relation is required"))
	}
	entity, err := h.operatorRelationEntity(ctx, scope, req, media.LibraryID)
	if err != nil {
		return RelationWriteResult{}, err
	}
	bundle := h.relationBundle(ctx, media.LibraryID)
	spec, declared := declaredMediaRelation(bundle, entity.Kind, name)
	if !declared {
		return RelationWriteResult{}, relationInputError(clientError("invalid relation for entity kind"))
	}
	attributes, err := operatorRelationAttributes(req, spec)
	if err != nil {
		return RelationWriteResult{}, err
	}
	order, err := optionalInt(req, "sortOrder")
	if err != nil {
		return RelationWriteResult{}, relationInputError(err)
	}
	operationID, err := newMutationOperationID()
	if err != nil {
		return RelationWriteResult{}, err
	}
	relation := store.EntityRelation{FromEntityID: entity.ID, Name: name, ToMediaID: media.ID,
		Cardinality: spec.Cardinality, Attributes: attributes, SortOrder: order}
	entry := mutationAuditEntry(scope, operationID, "", "media", string(media.ID))
	id, err := h.store.SetOperatorMediaRelation(ctx, relation, entity, scope.LibraryFilter(), entry)
	if err != nil {
		return RelationWriteResult{}, operatorRelationStoreError(err)
	}
	return RelationWriteResult{Relation: name, SourceID: string(id), TargetID: string(media.ID)}, nil
}

func (h *Handler) removeMediaRelation(ctx context.Context, scope auth.Scope, req request) (RelationWriteResult, error) {
	media, err := h.operatorRelationMedia(ctx, scope, req)
	if err != nil {
		return RelationWriteResult{}, err
	}
	name := trimmedVariable(req, "relation")
	id := store.EntityID(trimmedVariable(req, "entityID"))
	if name == "" || id == "" {
		return RelationWriteResult{}, relationInputError(clientError("relation and entityID are required"))
	}
	operationID, err := newMutationOperationID()
	if err != nil {
		return RelationWriteResult{}, err
	}
	entry := mutationAuditEntry(scope, operationID, "", "media", string(media.ID))
	// A relation dropped by an upgraded bundle remains removable.
	if err := h.store.RemoveOperatorMediaRelation(ctx, store.EntityRelation{
		FromEntityID: id, Name: name, ToMediaID: media.ID,
	}, scope.LibraryFilter(), entry); err != nil {
		return RelationWriteResult{}, operatorRelationStoreError(err)
	}
	return RelationWriteResult{Relation: name, SourceID: string(id), TargetID: string(media.ID)}, nil
}

func (h *Handler) operatorRelationMedia(ctx context.Context, scope auth.Scope, req request) (store.Media, error) {
	id := store.MediaID(trimmedVariable(req, "mediaID"))
	if id == "" {
		return store.Media{}, relationInputError(clientError("mediaID is required"))
	}
	media, err := h.store.Media(ctx, id, scope.LibraryFilter())
	if err != nil {
		return store.Media{}, relationInputError(clientError("unknown media"))
	}
	return media, nil
}

func (h *Handler) operatorRelationEntity(ctx context.Context, scope auth.Scope, req request, libraryID store.LibraryID) (store.Entity, error) {
	vars := newVars(req)
	id, hasID := vars.String("entityID")
	newValue, hasNew := vars.present("newEntity")
	if vars.Err() != nil || hasID == hasNew {
		return store.Entity{}, relationInputError(clientError("exactly one of entityID and newEntity is required"))
	}
	if hasID {
		entity, err := h.store.Entity(ctx, store.EntityID(strings.TrimSpace(id)), scope.LibraryFilter())
		if err != nil {
			return store.Entity{}, relationInputError(clientError("unknown entity"))
		}
		return store.Entity{ID: entity.ID, Kind: entity.Kind}, nil
	}
	object, ok := newValue.(map[string]any)
	if !ok {
		return store.Entity{}, relationInputError(clientError("newEntity must be an object"))
	}
	kind, _ := object["kind"].(string)
	name, _ := object["name"].(string)
	kind, name = strings.TrimSpace(kind), strings.TrimSpace(name)
	if kind == "" || name == "" {
		return store.Entity{}, relationInputError(clientError("newEntity kind and name are required"))
	}
	return store.Entity{ID: identify.EntityIDForName(libraryID, kind, name), Kind: kind, Title: name}, nil
}

func (h *Handler) relationBundle(ctx context.Context, libraryID store.LibraryID) plugin.Bundle {
	id, err := h.store.LibraryPluginID(ctx, libraryID)
	if err != nil {
		return plugin.Bundle{}
	}
	bundle, _ := h.plugins.Bundle(plugin.ID(id))
	return bundle
}

func declaredMediaRelation(bundle plugin.Bundle, kindName, name string) (plugin.RelationSpec, bool) {
	for _, kind := range bundle.EntityKinds {
		if kind.Name != kindName {
			continue
		}
		for _, spec := range kind.Relations {
			if spec.Name == name && spec.ToKind == plugin.MediaKindName {
				return spec, true
			}
		}
	}
	return plugin.RelationSpec{}, false
}

func operatorRelationAttributes(req request, spec plugin.RelationSpec) (map[string]string, error) {
	raw := req.Variables["attributes"]
	if raw == nil {
		return nil, nil
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, relationInputError(clientError("attributes must be an object"))
	}
	attributes := make(map[string]string, len(object))
	for name, value := range object {
		text, ok := value.(string)
		if !ok {
			return nil, relationInputError(clientError("attribute values must be strings"))
		}
		if _, declared := plugin.RelationAttributeType(spec, name); !declared {
			return nil, relationInputError(clientError("invalid relation attribute"))
		}
		attributes[name] = text
	}
	return attributes, nil
}

func operatorRelationStoreError(err error) error {
	var validation store.ValidationError
	if errors.Is(err, store.ErrEncryptedLibraryDBDisabled) {
		return relationInputError(clientError("unknown media"))
	}
	if errors.As(err, &validation) {
		return relationInputError(err)
	}
	return err
}

func relationInputError(err error) error {
	return &l10n.Error{Key: l10n.InvalidMediaRelation, Err: err}
}
