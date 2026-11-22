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

// MaxContainerDepth bounds the container chain walk: a plugin's container
// kinds nest a few levels, and a relation cycle must not loop.
const MaxContainerDepth = 8

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
	relations := ContainerRelations(bundle)
	holders, err := containers.MediaHolderLinks(ctx, library, mediaID)
	if err != nil {
		return nil, err
	}
	var chain []ContainerEntity
	for _, link := range holders {
		if relations.Holds(link.Kind, link.Relation) {
			chain = append(chain, ContainerEntity{ID: link.EntityID, Kind: link.Kind})
			break
		}
	}
	if len(chain) == 0 {
		return nil, nil
	}
	seen := map[store.EntityID]bool{chain[0].ID: true}
	for len(chain) < MaxContainerDepth {
		current := &chain[len(chain)-1]
		targets, err := containers.EntityTargetLinks(ctx, library, current.ID)
		if err != nil {
			return nil, err
		}
		var next *store.EntityLink
		for i, link := range targets {
			if !seen[link.EntityID] && relations.Nests(current.Kind, link.Relation, link.Kind) {
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

// ContainerRelationIndex is a bundle's container relations by source kind.
type ContainerRelationIndex struct {
	// holders: kind\x00relation for a container kind's single-holder Media
	// relations.
	holders map[string]bool
	// nesting: kind\x00relation → target kind for a container kind's
	// relations to another container kind.
	nesting map[string]string
}

// ContainerRelations indexes a bundle's container relations: each container
// kind's single-holder Media relations and its relations to another container
// kind.
func ContainerRelations(bundle plugin.Bundle) ContainerRelationIndex {
	containerKinds := containerKindNames(bundle)
	index := ContainerRelationIndex{holders: map[string]bool{}, nesting: map[string]string{}}
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

// HolderRelations names the relations any container kind holds Media through,
// sorted, so a reader can fetch only the relation rows Holds can accept.
func (i ContainerRelationIndex) HolderRelations() []string {
	var names []string
	for key := range i.holders {
		_, relation, _ := strings.Cut(key, "\x00")
		names = append(names, relation)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// Holds reports whether relation is a single-holder Media relation of the
// container kind.
func (i ContainerRelationIndex) Holds(kind, relation string) bool {
	return i.holders[kind+"\x00"+relation]
}

// Nests reports whether relation leads from the container kind to the
// container targetKind.
func (i ContainerRelationIndex) Nests(kind, relation, targetKind string) bool {
	return i.nesting[kind+"\x00"+relation] == targetKind
}

// ContainerNestings lists, in declaration order, each relation by which a
// container kind of the bundle nests in another container kind, so an item
// action on a container reaches its parts. Every declared target is kept,
// even when one relation names several; a relation between two Entities of
// one kind is a parent link only under an EntityMap.ParentRelation, so it is
// left out.
func ContainerNestings(pluginID string, bundle plugin.Bundle) []store.ContainerNest {
	containerKinds := containerKindNames(bundle)
	var nests []store.ContainerNest
	for _, kind := range bundle.EntityKinds {
		if !containerKinds[kind.Name] {
			continue
		}
		for _, relation := range kind.Relations {
			if containerKinds[relation.ToKind] && relation.ToKind != kind.Name {
				nests = append(nests, store.ContainerNest{PluginID: pluginID, ChildKind: kind.Name, Relation: relation.Name, ParentKind: relation.ToKind})
			}
		}
	}
	return nests
}

// OutermostContainerKinds names the container kinds a container chain ends
// at: kinds that declare no relation to another container kind yet hold Media
// themselves or are nested into by another container kind. A container kind
// outside every chain is not one.
func OutermostContainerKinds(bundle plugin.Bundle) map[string]bool {
	index := ContainerRelations(bundle)
	inChain := map[string]bool{}
	nests := map[string]bool{}
	for key := range index.holders {
		inChain[strings.SplitN(key, "\x00", 2)[0]] = true
	}
	for key, target := range index.nesting {
		nests[strings.SplitN(key, "\x00", 2)[0]] = true
		inChain[target] = true
	}
	outermost := map[string]bool{}
	for kind := range inChain {
		if !nests[kind] {
			outermost[kind] = true
		}
	}
	return outermost
}

func containerKindNames(bundle plugin.Bundle) map[string]bool {
	names := map[string]bool{}
	for _, kind := range bundle.EntityKinds {
		if kind.Archetype == containerArchetype {
			names[kind.Name] = true
		}
	}
	return names
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

// entityRelationReplacer is the optional EntityStore extension that rewrites a
// source Entity's relations by name; *store.Store satisfies it.
type entityRelationReplacer interface {
	ReplaceEntityRelations(ctx context.Context, fromID store.EntityID, names []string, relations []store.EntityRelation) error
}

// applyContainerLinks relates the container Entity to each Entity a sibling
// map hydrated, under the container relation its ContainerRelations entry
// names, keeping the sibling's sort order. A link is written only when the
// bundle declares that relation on the container kind toward the linked
// Entity's kind. The result's links replace the container's previous links
// under every container relation the map and the bundle declare, so a
// corrected match drops the old cast and genres, and an empty cast clears it;
// a store without the replace extension only adds links.
func (s *Service) applyContainerLinks(ctx context.Context, library store.LibraryID, bundle plugin.Bundle, target store.EntityID, result scrape.EntityResult) error {
	declared := map[string]plugin.RelationSpec{}
	for _, kind := range bundle.EntityKinds {
		if kind.Name == result.Kind {
			for _, relation := range kind.Relations {
				declared[relation.Name] = relation
			}
		}
	}
	var names []string
	for _, name := range result.LinkRelations {
		if _, ok := declared[name]; ok {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	rows := containerRelationRows(library, target, declared, result.Links)
	if replacer, ok := s.entities.(entityRelationReplacer); ok {
		return replacer.ReplaceEntityRelations(ctx, target, names, rows)
	}
	for _, row := range rows {
		if err := s.entities.UpsertEntityRelation(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

// containerRelationRows turns a container's declared links into relation rows.
// A person credited in both cast and crew arrives as two links to one Entity;
// it keeps one row with the smaller ordinal, so billing order holds.
func containerRelationRows(library store.LibraryID, target store.EntityID, declared map[string]plugin.RelationSpec, links []scrape.EntityLink) []store.EntityRelation {
	var rows []store.EntityRelation
	seen := map[[2]string]int{}
	for _, link := range links {
		relation, ok := declared[link.Relation]
		if !ok || relation.ToKind != link.Kind {
			continue
		}
		to := entityIDForName(library, link.Kind, link.Name)
		key := [2]string{link.Relation, string(to)}
		if i, dup := seen[key]; dup {
			if cur := rows[i].SortOrder; link.SortOrder != nil && (cur == nil || *link.SortOrder < *cur) {
				rows[i].SortOrder = link.SortOrder
			}
			continue
		}
		seen[key] = len(rows)
		rows = append(rows, store.EntityRelation{
			FromEntityID: target,
			Name:         link.Relation,
			ToEntityID:   to,
			Cardinality:  relation.Cardinality,
			SortOrder:    link.SortOrder,
		})
	}
	return rows
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
