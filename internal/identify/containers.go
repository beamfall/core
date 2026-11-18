package identify

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// maxContainerDepth bounds the container chain walk: a plugin's container
// kinds nest a few levels, and a relation cycle must not loop.
const maxContainerDepth = 8

// containerArchetype is the EntityKind archetype of a kind that groups Media.
const containerArchetype = "container"

// unrestrictedEntityFilter reads an Entity identification is already writing:
// the caller authorized the Media, so the read is unfiltered like the writes.
var unrestrictedEntityFilter = store.LibraryFilter{AllLibraries: true, Revealed: true}

// ContainerEntity is one container Entity holding a Media, directly or through
// an inner container. Ordinal is the sort order of its relation to the next
// container out, and Ordered says whether it has one.
type ContainerEntity struct {
	ID      store.EntityID
	Kind    string
	Ordinal int
	Ordered bool
}

// ContainerStore is the optional read seam behind container resolution;
// *store.Store satisfies it. Both link reads stay inside one Library.
type ContainerStore interface {
	LibraryPluginID(ctx context.Context, id store.LibraryID) (string, error)
	MediaHolderLinks(ctx context.Context, libraryID store.LibraryID, mediaID store.MediaID) ([]store.EntityLink, error)
	EntityTargetLinks(ctx context.Context, libraryID store.LibraryID, id store.EntityID) ([]store.EntityLink, error)
	Entity(ctx context.Context, id store.EntityID, filter store.LibraryFilter) (store.Entity, error)
}

// ContainerEntitiesForMedia returns the container Entities that hold a Media,
// innermost first, inside the Media's Library. The innermost is the container
// Entity pointing at the Media through a single-holder relation its kind
// declares; each next one is the container Entity the previous points at
// through a relation its kind declares to a container kind. Only kinds the
// Library's plugin declares as containers count. A Media with no such holder,
// or a store without the read seam, has none.
func (s *Service) ContainerEntitiesForMedia(ctx context.Context, mediaID store.MediaID) ([]ContainerEntity, error) {
	containers, ok := s.entities.(ContainerStore)
	if !ok || s.bundles == nil || mediaID == "" {
		return nil, nil
	}
	library, err := s.entities.MediaLibrary(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	pluginID, err := containers.LibraryPluginID(ctx, library)
	if err != nil || pluginID == "" {
		return nil, err
	}
	bundle, ok := s.bundles.Bundle(plugin.ID(pluginID))
	if !ok {
		return nil, nil
	}
	relations := containerRelations(bundle)
	holders, err := containers.MediaHolderLinks(ctx, library, mediaID)
	if err != nil {
		return nil, err
	}
	var chain []ContainerEntity
	for _, link := range holders {
		if relations.holds(link.Kind, link.Relation) {
			chain = append(chain, ContainerEntity{ID: link.EntityID, Kind: link.Kind})
			break
		}
	}
	if len(chain) == 0 {
		return nil, nil
	}
	seen := map[store.EntityID]bool{chain[0].ID: true}
	for len(chain) < maxContainerDepth {
		current := &chain[len(chain)-1]
		targets, err := containers.EntityTargetLinks(ctx, library, current.ID)
		if err != nil {
			return nil, err
		}
		var next *store.EntityLink
		for i, link := range targets {
			if !seen[link.EntityID] && relations.nests(current.Kind, link.Relation, link.Kind) {
				next = &targets[i]
				break
			}
		}
		if next == nil {
			break
		}
		if next.SortOrder != nil {
			current.Ordinal, current.Ordered = *next.SortOrder, true
		}
		seen[next.EntityID] = true
		chain = append(chain, ContainerEntity{ID: next.EntityID, Kind: next.Kind})
	}
	return chain, nil
}

// containerRelationIndex is a bundle's container relations by source kind.
type containerRelationIndex struct {
	// holders: kind\x00relation for a container kind's single-holder Media
	// relations.
	holders map[string]bool
	// nesting: kind\x00relation → target kind for a container kind's
	// relations to another container kind.
	nesting map[string]string
}

func containerRelations(bundle plugin.Bundle) containerRelationIndex {
	containerKinds := map[string]bool{}
	for _, kind := range bundle.EntityKinds {
		if kind.Archetype == containerArchetype {
			containerKinds[kind.Name] = true
		}
	}
	index := containerRelationIndex{holders: map[string]bool{}, nesting: map[string]string{}}
	for _, kind := range bundle.EntityKinds {
		if !containerKinds[kind.Name] {
			continue
		}
		for _, relation := range kind.Relations {
			key := kind.Name + "\x00" + relation.Name
			switch {
			case relation.ToKind == plugin.MediaKindName && relation.Cardinality == "one":
				index.holders[key] = true
			case containerKinds[relation.ToKind]:
				index.nesting[key] = relation.ToKind
			}
		}
	}
	return index
}

func (i containerRelationIndex) holds(kind, relation string) bool {
	return i.holders[kind+"\x00"+relation]
}

func (i containerRelationIndex) nests(kind, relation, targetKind string) bool {
	return i.nesting[kind+"\x00"+relation] == targetKind
}

// applyContainerEntities hydrates the existing container Entities a lookup's
// container results name: each result's fields merge into the Entity of its
// kind holding the Media (its other fields kept), a resolved title replaces
// its display title (its ID unchanged), its poster and other artwork slots are
// applied like any hydrated Entity poster, and its links relate it to the
// Entities sibling maps hydrated. A result whose kind holds no Entity for this
// Media is skipped; nothing is created.
func (s *Service) applyContainerEntities(ctx context.Context, mediaID store.MediaID, library store.LibraryID, bundle plugin.Bundle, results []scrape.EntityResult) error {
	if len(results) == 0 {
		return nil
	}
	containers, ok := s.entities.(ContainerStore)
	if !ok {
		slog.Debug("identify: container hydration skipped, store has no container reads", "media", mediaID)
		return nil
	}
	chain, err := s.ContainerEntitiesForMedia(ctx, mediaID)
	if err != nil {
		return err
	}
	for _, result := range results {
		target, ok := containerOfKind(chain, result.Kind)
		if !ok {
			slog.Debug("identify: no container entity of kind holds the media", "media", mediaID, "kind", result.Kind)
			continue
		}
		if fields := entityFields(bundle, result); len(fields) > 0 || result.Title != "" || len(result.Links) > 0 {
			existing, err := containers.Entity(ctx, target, unrestrictedEntityFilter)
			if err != nil {
				return err
			}
			if existing.LibraryID != library {
				continue
			}
			if len(fields) > 0 || result.Title != "" {
				existing.Fields = mergeEntityFields(existing.Fields, fields)
				if result.Title != "" {
					existing.Title = result.Title
				}
				if err := s.entities.UpsertEntity(ctx, existing); err != nil {
					return err
				}
			}
		}
		s.applyEntityPoster(ctx, target, result.ArtworkURL)
		s.applyEntityArtworkSlots(ctx, target, result.Artwork)
		if err := s.applyContainerLinks(ctx, library, bundle, target, result); err != nil {
			return err
		}
	}
	return nil
}

// entityArtworkImporter is the optional ArtworkStore extension that writes
// Entity artwork slots other than the poster; *artwork.Service satisfies it.
type entityArtworkImporter interface {
	HasEntityArtwork(id store.EntityID, slot string) bool
	ImportEntityArtwork(ctx context.Context, id store.EntityID, slot string, data []byte) error
}

// applyEntityArtworkSlots imports a container result's ArtworkMap images.
// Best-effort like applyEntityPoster: a slot that already exists keeps its
// image, and a failed fetch or import is logged and skipped.
func (s *Service) applyEntityArtworkSlots(ctx context.Context, id store.EntityID, artwork map[string]string) {
	if len(artwork) == 0 || s.fetcher == nil || s.artwork == nil {
		return
	}
	if poster := artwork["poster"]; poster != "" {
		s.applyEntityPoster(ctx, id, poster)
	}
	importer, ok := s.artwork.(entityArtworkImporter)
	if !ok {
		return
	}
	for _, slot := range slices.Sorted(maps.Keys(artwork)) {
		if slot == "poster" || importer.HasEntityArtwork(id, slot) {
			continue
		}
		data, err := s.fetcher.FetchResource(ctx, artwork[slot])
		if err != nil {
			slog.Debug("identify: entity artwork fetch failed", "entity", string(id), "slot", slot, "error", err.Error())
			continue
		}
		if err := importer.ImportEntityArtwork(ctx, id, slot, data); err != nil {
			slog.Debug("identify: entity artwork import failed", "entity", string(id), "slot", slot, "error", err.Error())
		}
	}
}

// applyContainerLinks relates the container Entity to each Entity a sibling
// map hydrated, under the container relation its ContainerRelations entry
// names, keeping the sibling's sort order. A link is written only when the
// bundle declares that relation on the container kind toward the linked
// Entity's kind.
func (s *Service) applyContainerLinks(ctx context.Context, library store.LibraryID, bundle plugin.Bundle, target store.EntityID, result scrape.EntityResult) error {
	if len(result.Links) == 0 {
		return nil
	}
	declared := map[string]plugin.RelationSpec{}
	for _, kind := range bundle.EntityKinds {
		if kind.Name == result.Kind {
			for _, relation := range kind.Relations {
				declared[relation.Name] = relation
			}
		}
	}
	for _, link := range result.Links {
		relation, ok := declared[link.Relation]
		if !ok || relation.ToKind != link.Kind {
			continue
		}
		if err := s.entities.UpsertEntityRelation(ctx, store.EntityRelation{
			FromEntityID: target,
			Name:         link.Relation,
			ToEntityID:   entityIDForName(library, link.Kind, link.Name),
			Cardinality:  relation.Cardinality,
			SortOrder:    link.SortOrder,
		}); err != nil {
			return err
		}
	}
	return nil
}

// containerOfKind returns the innermost chain Entity of kind.
func containerOfKind(chain []ContainerEntity, kind string) (store.EntityID, bool) {
	for _, entity := range chain {
		if strings.EqualFold(entity.Kind, kind) {
			return entity.ID, true
		}
	}
	return "", false
}

// mergeEntityFields overlays fields onto existing by name, keeping every
// existing field the overlay does not name.
func mergeEntityFields(existing, overlay []store.EntityField) []store.EntityField {
	byName := map[string]store.EntityField{}
	for _, field := range existing {
		byName[field.Name] = field
	}
	for _, field := range overlay {
		byName[field.Name] = field
	}
	names := slices.Sorted(maps.Keys(byName))
	merged := make([]store.EntityField, 0, len(names))
	for _, name := range names {
		merged = append(merged, byName[name])
	}
	return merged
}
