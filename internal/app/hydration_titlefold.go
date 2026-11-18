package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/serverrole"
	"github.com/beamfall/core/internal/store"
)

// hydrationFoldStore is the store surface behind HydrationTitleFoldMapper:
// the reads that find a Media's previous holder and the recoverable merge
// that folds it into the new one. *store.Store implements it; a store that
// does not leaves fold merging off.
type hydrationFoldStore interface {
	MediaHolderLinks(ctx context.Context, libraryID store.LibraryID, mediaID store.MediaID) ([]store.EntityLink, error)
	Entity(ctx context.Context, id store.EntityID, filter store.LibraryFilter) (store.Entity, error)
	MergeEntities(ctx context.Context, winnerID, loserID store.EntityID, filter store.LibraryFilter, now time.Time) (store.BulkOpBatch, error)
	CopyEntityItemState(ctx context.Context, from, to store.EntityID) error
	LiveFoldEqualEntityTitle(ctx context.Context, libraryID store.LibraryID, id store.EntityID) (string, bool, error)
	LiveFoldEqualEntities(ctx context.Context, libraryID store.LibraryID, kind, title, scopeRelation string, scopeTarget store.EntityID) ([]store.Entity, error)
	EntityTitleFoldApplies(ctx context.Context, libraryID store.LibraryID) (bool, error)
}

// titleFoldOn reports whether this hydration merges title variants: the
// mapper declares fold kinds, the store supports it and the Library is not
// encrypted (see store.EntityTitleFoldApplies).
func (a *hydrationAdapter) titleFoldOn(ctx context.Context, libraryID store.LibraryID) (bool, error) {
	if a.foldStore == nil || len(a.foldKinds) == 0 {
		return false, nil
	}
	return a.foldStore.EntityTitleFoldApplies(ctx, libraryID)
}

func declaredTitleFoldKinds(mapper HydrationMapper) map[string]bool {
	folded, ok := mapper.(HydrationTitleFoldMapper)
	if !ok {
		return nil
	}
	kinds := map[string]bool{}
	for _, kind := range folded.TitleFoldKinds() {
		if kind != "" {
			kinds[kind] = true
		}
	}
	return kinds
}

// foldHolder is a Media-target relation of a fold kind and the Entities that
// held the Media through it before this hydration wrote it.
type foldHolder struct {
	index  int
	before []store.EntityID
}

// priorFoldHolders reads, before the plan is written, which Entities hold the
// Media through each Media-target relation whose source kind folds. A read
// error fails the hydration: moving the Media without it would lose track of
// the holder to merge.
func (a *hydrationAdapter) priorFoldHolders(ctx context.Context, mediaID store.MediaID, libraryID store.LibraryID, relations []HydrationRelation) ([]foldHolder, error) {
	if a.foldStore == nil || len(a.foldKinds) == 0 {
		return nil, nil
	}
	var links []store.EntityLink
	read := false
	var holders []foldHolder
	for i, mapped := range relations {
		if mapped.ToEntity != nil || !a.foldKinds[mapped.From.Kind] {
			continue
		}
		if !read {
			var err error
			if links, err = a.foldStore.MediaHolderLinks(ctx, libraryID, mediaID); err != nil {
				return nil, fmt.Errorf("read previous holders: %w", err)
			}
			read = true
		}
		holder := foldHolder{index: i}
		for _, link := range links {
			if link.Relation == mapped.Name && link.Kind == mapped.From.Kind {
				holder.before = append(holder.before, link.EntityID)
			}
		}
		if len(holder.before) > 0 {
			holders = append(holders, holder)
		}
	}
	return holders, nil
}

// redirectMergedFoldTitles rewrites the plan's unscoped Entities of a fold
// kind whose title names an Entity in trash to the live Entity it folds equal
// to (see store.LiveFoldEqualEntityTitle), and the scopes that name them, so
// a file still spelling a merged-away title joins the merged Entity instead of
// landing in trash. A title with no Entity yet is kept: it is a new item.
func (a *hydrationAdapter) redirectMergedFoldTitles(ctx context.Context, libraryID store.LibraryID, relations []HydrationRelation) ([]HydrationRelation, error) {
	if a.foldStore == nil || len(a.foldKinds) == 0 {
		return relations, nil
	}
	renamed := map[string]string{}
	checked := map[string]bool{}
	check := func(entity HydrationEntity) error {
		key := entity.Kind + "\x00" + entity.Title
		if entity.Scope != "" || entity.Title == "" || !a.foldKinds[entity.Kind] || checked[key] {
			return nil
		}
		checked[key] = true
		live, ok, err := a.foldStore.LiveFoldEqualEntityTitle(ctx, libraryID, identify.EntityIDForName(libraryID, entity.Kind, entity.Title))
		if err != nil {
			return fmt.Errorf("resolve merged title %q: %w", entity.Title, err)
		}
		if ok {
			renamed[key] = live
		}
		return nil
	}
	for _, mapped := range relations {
		if err := check(mapped.From); err != nil {
			return nil, err
		}
		if mapped.ToEntity != nil {
			if err := check(*mapped.ToEntity); err != nil {
				return nil, err
			}
		}
	}
	if len(renamed) == 0 {
		return relations, nil
	}
	out := make([]HydrationRelation, len(relations))
	for i, mapped := range relations {
		mapped.From = a.redirectEntity(mapped.From, renamed)
		if mapped.ToEntity != nil {
			to := a.redirectEntity(*mapped.ToEntity, renamed)
			mapped.ToEntity = &to
		}
		out[i] = mapped
	}
	return out, nil
}

func (a *hydrationAdapter) redirectEntity(entity HydrationEntity, renamed map[string]string) HydrationEntity {
	if entity.Scope == "" {
		if title, ok := renamed[entity.Kind+"\x00"+entity.Title]; ok {
			entity.Title = title
		}
		return entity
	}
	scopeKind := a.declaredRelationSpecs[entity.Kind+"\x00"+a.entityScopes[entity.Kind]].ToKind
	if title, ok := renamed[scopeKind+"\x00"+entity.Scope]; ok {
		entity.Scope = title
	}
	return entity
}

// mergeFoldedHolders merges each previous holder of the Media into the Entity
// this hydration resolved, when the two are one item spelled differently: the
// same kind and fold-equal titles. It runs before the Media's relations are
// replaced, so the Media still sits on the previous holder: the merge moves and
// journals it like every other member, and an undo restores it. A previous
// holder that points (through a relation the plan also writes from the same
// Entity) at an Entity that does not fold to the plan's target belongs
// elsewhere and is left alone; one whose targets fold to the plan's target has
// those targets merged first. Merges go through the recoverable, undoable
// entity merge after the item state is copied. An error fails the hydration
// before the Media moves or its fingerprint is recorded, so the next hydration
// finds the previous holder again and retries.
func (a *hydrationAdapter) mergeFoldedHolders(ctx context.Context, libraryID store.LibraryID, relations []HydrationRelation, written []store.EntityRelation, holders []foldHolder) error {
	for _, holder := range holders {
		winner := written[holder.index].FromEntityID
		if winner == "" {
			continue
		}
		from := relations[holder.index].From
		targets := map[string]store.EntityID{}
		for i, mapped := range relations {
			if mapped.ToEntity == nil || !sameHydrationEntity(mapped.From, from) || written[i].ToEntityID == "" {
				continue
			}
			targets[mapped.Name] = written[i].ToEntityID
		}
		for _, loser := range holder.before {
			if loser == winner {
				continue
			}
			err := a.mergeFoldedEntity(ctx, libraryID, winner, loser, targets)
			if errors.Is(err, serverrole.ErrAttachedAuthorityRefused) {
				continue // an attached server leaves catalog merges to the main one
			}
			if err != nil {
				return fmt.Errorf("merge title variant %s into %s: %w", loser, winner, err)
			}
		}
	}
	return nil
}

func (a *hydrationAdapter) mergeFoldedEntity(ctx context.Context, libraryID store.LibraryID, winner, loser store.EntityID, targets map[string]store.EntityID) error {
	if same, err := a.foldEqual(ctx, winner, loser); err != nil || !same {
		return err
	}
	type pair struct{ winner, loser store.EntityID }
	var parents []pair
	for relation, target := range targets {
		before, _, err := a.store.EntityRelationTargets(ctx, libraryID, loser, relation)
		if err != nil {
			return err
		}
		for _, old := range before {
			if old == target {
				continue
			}
			same, err := a.foldEqual(ctx, target, old)
			if err != nil {
				return err
			}
			if !same {
				return nil // the old holder belongs to another item: keep both
			}
			parents = append(parents, pair{winner: target, loser: old})
		}
	}
	for _, parent := range parents {
		if err := a.mergeEntity(ctx, parent.winner, parent.loser); err != nil {
			return err
		}
	}
	return a.mergeEntity(ctx, winner, loser)
}

// mergeFoldSiblings merges into each Entity of a fold kind the plan wrote the
// other live Entities of its kind whose titles fold equal to its own (for a
// scoped kind, only those under the same scope Entity), so a spelling that
// differs from an existing item's, such as the folder's spelling replacing the
// one file names gave, or one of several split variants hydrated again,
// becomes that item instead of staying beside it. Parents go before the
// children scoped by them. It runs after the plan is written, so each merge
// journals the Media's new relations and an undo keeps them. The merged-away
// titles then redirect to the survivor (redirectMergedFoldTitles).
//
// An Entity checked without error is remembered for the adapter's lifetime,
// so a scan reads each one's siblings once; a failed merge leaves it
// unchecked and fails the hydration, so the next hydration of any of its
// Media retries.
func (a *hydrationAdapter) mergeFoldSiblings(ctx context.Context, libraryID store.LibraryID, relations []HydrationRelation, written []store.EntityRelation) error {
	type planned struct {
		entity HydrationEntity
		id     store.EntityID
	}
	var unscoped, scoped []planned
	seen := map[store.EntityID]bool{}
	add := func(entity HydrationEntity, id store.EntityID) {
		if id == "" || seen[id] || a.foldChecked[id] || !a.foldKinds[entity.Kind] || entity.Title == "" {
			return
		}
		seen[id] = true
		if entity.Scope == "" {
			unscoped = append(unscoped, planned{entity: entity, id: id})
		} else {
			scoped = append(scoped, planned{entity: entity, id: id})
		}
	}
	for i, mapped := range relations {
		add(mapped.From, written[i].FromEntityID)
		if mapped.ToEntity != nil {
			add(*mapped.ToEntity, written[i].ToEntityID)
		}
	}
	for _, survivor := range append(unscoped, scoped...) {
		scopeRelation, scopeTarget := "", store.EntityID("")
		if survivor.entity.Scope != "" {
			scopeRelation = a.entityScopes[survivor.entity.Kind]
			for i, mapped := range relations {
				if mapped.ToEntity != nil && mapped.Name == scopeRelation && sameHydrationEntity(mapped.From, survivor.entity) {
					scopeTarget = written[i].ToEntityID
				}
			}
			if scopeTarget == "" {
				continue
			}
		}
		others, err := a.foldStore.LiveFoldEqualEntities(ctx, libraryID, survivor.entity.Kind, survivor.entity.Title, scopeRelation, scopeTarget)
		if err != nil {
			return fmt.Errorf("read title variants of %s: %w", survivor.id, err)
		}
		for _, other := range others {
			if other.ID == survivor.id {
				continue
			}
			err := a.mergeEntity(ctx, survivor.id, other.ID)
			if errors.Is(err, serverrole.ErrAttachedAuthorityRefused) {
				return nil // an attached server leaves catalog merges to the main one
			}
			if err != nil {
				return fmt.Errorf("merge title variant %s into %s: %w", other.ID, survivor.id, err)
			}
		}
		if a.foldChecked == nil {
			a.foldChecked = map[store.EntityID]bool{}
		}
		a.foldChecked[survivor.id] = true
	}
	return nil
}

func sameHydrationEntity(left, right HydrationEntity) bool {
	return left.Kind == right.Kind && left.Title == right.Title && left.Scope == right.Scope
}

// foldEqual reports whether two live Entities are one item spelled
// differently: the same fold kind and fold-equal, non-empty titles.
func (a *hydrationAdapter) foldEqual(ctx context.Context, first, second store.EntityID) (bool, error) {
	left, err := a.foldStore.Entity(ctx, first, metadataLibraryFilter)
	if err != nil {
		return false, err
	}
	right, err := a.foldStore.Entity(ctx, second, metadataLibraryFilter)
	if store.IsEntityNotFound(err) {
		return false, nil // gone or trashed: nothing to merge
	}
	if err != nil {
		return false, err
	}
	folded := store.FoldEntityTitle(left.Title)
	return left.Kind == right.Kind && a.foldKinds[left.Kind] && folded != "" && folded == store.FoldEntityTitle(right.Title), nil
}

func (a *hydrationAdapter) mergeEntity(ctx context.Context, winner, loser store.EntityID) error {
	if err := a.foldStore.CopyEntityItemState(ctx, loser, winner); err != nil {
		return err
	}
	_, err := a.foldStore.MergeEntities(ctx, winner, loser, metadataLibraryFilter, time.Now().UTC())
	return err
}

// titleFoldRepairer is the store write behind the one-time title-fold repair.
type titleFoldRepairer interface {
	ReopenFoldedEntityTitles(ctx context.Context, pluginID, kind, markerField string, now time.Time) (int, error)
}

// reopenFoldedHydrationTitles runs the one-time title-fold repair for every
// unscoped kind an active mapper folds, so Entities a library split by title
// spelling before the mapper settled one are hydrated again and merged.
func reopenFoldedHydrationTitles(ctx context.Context, st entityScopeSplitter, pluginID string, mapper HydrationMapper, logger *slog.Logger) {
	repairer, ok := st.(titleFoldRepairer)
	if !ok || mapper.MarkerField() == "" {
		return
	}
	scoped := declaredEntityScopes(mapper)
	for kind := range declaredTitleFoldKinds(mapper) {
		if scoped[kind] != "" {
			continue // a scoped kind repeats its titles across scopes by design
		}
		reopened, err := repairer.ReopenFoldedEntityTitles(ctx, pluginID, kind, mapper.MarkerField(), time.Now().UTC())
		switch {
		case errors.Is(err, serverrole.ErrAttachedAuthorityRefused):
		case err != nil:
			logger.Warn("entity title fold repair failed; retrying next start", "plugin", pluginID, "kind", kind, "error", err)
		case reopened > 0:
			logger.Info("entity title fold repair reopened entities for the next scan", "plugin", pluginID, "kind", kind, "entities", reopened)
		}
	}
}
