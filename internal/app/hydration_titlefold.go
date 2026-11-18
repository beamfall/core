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
// the cached lookup of an Entity's title variants, the read that finds a
// Media's previous holder, and the recoverable, audited merge that folds a
// newer variant into the established one. *store.Store implements it; a store
// that does not leaves fold merging off.
type hydrationFoldStore interface {
	MediaHolderLinks(ctx context.Context, libraryID store.LibraryID, mediaID store.MediaID) ([]store.EntityLink, error)
	MergeEntities(ctx context.Context, winnerID, loserID store.EntityID, filter store.LibraryFilter, now time.Time) (store.BulkOpBatch, error)
	CopyEntityItemState(ctx context.Context, from, to store.EntityID) error
	AppendActivity(ctx context.Context, entry store.ActivityEntry) error
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

// redirectToEstablishedFoldTitles rewrites each Entity of a fold kind in the
// plan to the title of the established Entity it folds equal to: the oldest
// live Entity of its kind (under the same scope Entity, for a scoped kind)
// whose title folds equal and whose id is the one hydration derives from that
// title. The plan then lands on that Entity, so a new spelling, such as the
// folder's replacing the one file names gave, or a merged-away title still
// spelled by a file, joins the item people already have, with its id and
// everything keyed to it (artwork, locks, item state), instead of absorbing
// it. Unscoped Entities are settled first, then the scopes that name them,
// then the scoped Entities under the settled scopes. A title with no live
// variant is kept: it is a new item.
func (a *hydrationAdapter) redirectToEstablishedFoldTitles(ctx context.Context, libraryID store.LibraryID, relations []HydrationRelation) ([]HydrationRelation, error) {
	if a.foldStore == nil || len(a.foldKinds) == 0 {
		return relations, nil
	}
	renamed := map[string]string{}
	checked := map[string]bool{}
	settle := func(scoped bool) error {
		for _, mapped := range relations {
			entities := []HydrationEntity{mapped.From}
			if mapped.ToEntity != nil {
				entities = append(entities, *mapped.ToEntity)
			}
			for _, entity := range entities {
				key := foldRenameKey(entity)
				if (entity.Scope != "") != scoped || entity.Title == "" || !a.foldKinds[entity.Kind] || checked[key] {
					continue
				}
				checked[key] = true
				title, ok, err := a.establishedFoldTitle(ctx, libraryID, entity)
				if err != nil {
					return fmt.Errorf("resolve title variants of %q: %w", entity.Title, err)
				}
				if ok && title != entity.Title {
					renamed[key] = title
				}
			}
		}
		return nil
	}
	if err := settle(false); err != nil {
		return nil, err
	}
	relations = a.renameFoldEntities(relations, renamed)
	if err := settle(true); err != nil {
		return nil, err
	}
	return a.renameFoldEntities(relations, renamed), nil
}

// establishedFoldTitle reads the established variant of a planned Entity (see
// redirectToEstablishedFoldTitles). ok is false when none is live.
func (a *hydrationAdapter) establishedFoldTitle(ctx context.Context, libraryID store.LibraryID, entity HydrationEntity) (string, bool, error) {
	scopeRelation, scopeTarget := "", store.EntityID("")
	if entity.Scope != "" {
		scopeRelation = a.entityScopes[entity.Kind]
		scopeTarget = identify.EntityIDForName(libraryID, a.scopeKind(entity.Kind), entity.Scope)
	}
	variants, err := a.foldStore.LiveFoldEqualEntities(ctx, libraryID, entity.Kind, entity.Title, scopeRelation, scopeTarget)
	if err != nil {
		return "", false, err
	}
	for _, variant := range variants {
		if variant.ID == identify.EntityIDForScopedName(libraryID, entity.Kind, entity.Scope, variant.Title) ||
			(entity.Scope != "" && variant.ID == identify.EntityIDForName(libraryID, entity.Kind, variant.Title)) {
			return variant.Title, true, nil
		}
	}
	return "", false, nil
}

func (a *hydrationAdapter) scopeKind(kind string) string {
	return a.declaredRelationSpecs[kind+"\x00"+a.entityScopes[kind]].ToKind
}

func foldRenameKey(entity HydrationEntity) string {
	return entity.Kind + "\x00" + entity.Scope + "\x00" + entity.Title
}

// renameFoldEntities applies the settled titles to every Entity of the plan
// and to the scopes that name a renamed Entity.
func (a *hydrationAdapter) renameFoldEntities(relations []HydrationRelation, renamed map[string]string) []HydrationRelation {
	if len(renamed) == 0 {
		return relations
	}
	rename := func(entity HydrationEntity) HydrationEntity {
		if entity.Scope != "" {
			if title, ok := renamed[foldRenameKey(HydrationEntity{Kind: a.scopeKind(entity.Kind), Title: entity.Scope})]; ok {
				entity.Scope = title
			}
		}
		if title, ok := renamed[foldRenameKey(entity)]; ok {
			entity.Title = title
		}
		return entity
	}
	out := make([]HydrationRelation, len(relations))
	for i, mapped := range relations {
		mapped.From = rename(mapped.From)
		if mapped.ToEntity != nil {
			to := rename(*mapped.ToEntity)
			mapped.ToEntity = &to
		}
		out[i] = mapped
	}
	return out
}

// mergeFoldedHolders merges each previous holder of the Media into the Entity
// this hydration resolved, when the holder is a newer variant of it: the same
// kind, a fold-equal title, and added after it (redirectToEstablishedFoldTitles
// already pointed the plan at the established variant, so an older holder is
// not hydration's and is left alone). It runs before the Media's relations
// are replaced, so the Media still sits on the previous holder: the merge
// moves and journals it like every other member, and an undo restores it. A
// previous holder that points (through a relation the plan also writes from
// the same Entity) at an Entity that is not a newer variant of the plan's
// target belongs elsewhere and is left alone; one whose targets are has those
// targets merged first. An error fails the hydration before the Media moves
// or its fingerprint is recorded, so the next hydration finds the previous
// holder again and retries.
func (a *hydrationAdapter) mergeFoldedHolders(ctx context.Context, libraryID store.LibraryID, relations []HydrationRelation, written []store.EntityRelation, holders []foldHolder) error {
	for _, holder := range holders {
		winner := foldPlanned{entity: relations[holder.index].From, id: written[holder.index].FromEntityID}
		if winner.id == "" {
			continue
		}
		targets := map[string]foldPlanned{}
		for i, mapped := range relations {
			if mapped.ToEntity == nil || !sameHydrationEntity(mapped.From, winner.entity) || written[i].ToEntityID == "" {
				continue
			}
			targets[mapped.Name] = foldPlanned{entity: *mapped.ToEntity, id: written[i].ToEntityID}
		}
		for _, loser := range holder.before {
			if loser == winner.id {
				continue
			}
			err := a.mergeFoldedEntity(ctx, libraryID, winner, loser, targets)
			if errors.Is(err, serverrole.ErrAttachedAuthorityRefused) {
				continue // an attached server leaves catalog merges to the main one
			}
			if err != nil {
				return fmt.Errorf("merge title variant %s into %s: %w", loser, winner.id, err)
			}
		}
	}
	return nil
}

// foldPlanned is an Entity of the plan and the id it was written under.
type foldPlanned struct {
	entity HydrationEntity
	id     store.EntityID
}

func (a *hydrationAdapter) mergeFoldedEntity(ctx context.Context, libraryID store.LibraryID, winner foldPlanned, loser store.EntityID, targets map[string]foldPlanned) error {
	if newer, err := a.newerFoldVariant(ctx, libraryID, winner, loser); err != nil || !newer {
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
			if old == target.id {
				continue
			}
			newer, err := a.newerFoldVariant(ctx, libraryID, target, old)
			if err != nil {
				return err
			}
			if !newer {
				return nil // the old holder belongs to another item: keep both
			}
			parents = append(parents, pair{winner: target.id, loser: old})
		}
	}
	for _, parent := range parents {
		if err := a.mergeEntity(ctx, libraryID, parent.winner, parent.loser); err != nil {
			return err
		}
	}
	return a.mergeEntity(ctx, libraryID, winner.id, loser)
}

// newerFoldVariant reports whether other is a live Entity of the planned
// Entity's fold kind, with a fold-equal title, added after it: one the
// planned Entity may absorb without losing an older item's id.
func (a *hydrationAdapter) newerFoldVariant(ctx context.Context, libraryID store.LibraryID, planned foldPlanned, other store.EntityID) (bool, error) {
	if !a.foldKinds[planned.entity.Kind] || planned.entity.Title == "" {
		return false, nil
	}
	variants, err := a.foldStore.LiveFoldEqualEntities(ctx, libraryID, planned.entity.Kind, planned.entity.Title, "", "")
	if err != nil {
		return false, err
	}
	seen := false
	for _, variant := range variants {
		switch variant.ID {
		case planned.id:
			seen = true
		case other:
			return seen, nil
		}
	}
	return false, nil
}

// mergeFoldSiblings merges into each Entity of a fold kind the plan wrote the
// newer live Entities of its kind whose titles fold equal to its own (for a
// scoped kind, only those under the same scope Entity), so split variants of
// one item, such as files hydrated before the mapper settled one spelling,
// become the established item instead of staying beside it. An older variant
// is never absorbed: the plan was pointed at the oldest hydration-named one
// (redirectToEstablishedFoldTitles), and one older than that is not
// hydration's. Parents go before the children scoped by them, so an undo,
// newest first, takes the children back before their parents. It runs after
// the plan is written, so each merge journals the Media's new relations and
// an undo keeps them.
//
// An Entity checked without error is remembered for the adapter's lifetime,
// so a scan reads each one's siblings once; a failed merge leaves it
// unchecked and fails the hydration, so the next hydration of any of its
// Media retries.
func (a *hydrationAdapter) mergeFoldSiblings(ctx context.Context, libraryID store.LibraryID, relations []HydrationRelation, written []store.EntityRelation) error {
	var unscoped, scoped []foldPlanned
	seen := map[store.EntityID]bool{}
	add := func(entity HydrationEntity, id store.EntityID) {
		if id == "" || seen[id] || a.foldChecked[id] || !a.foldKinds[entity.Kind] || entity.Title == "" {
			return
		}
		seen[id] = true
		if entity.Scope == "" {
			unscoped = append(unscoped, foldPlanned{entity: entity, id: id})
		} else {
			scoped = append(scoped, foldPlanned{entity: entity, id: id})
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
		variants, err := a.foldStore.LiveFoldEqualEntities(ctx, libraryID, survivor.entity.Kind, survivor.entity.Title, scopeRelation, scopeTarget)
		if err != nil {
			return fmt.Errorf("read title variants of %s: %w", survivor.id, err)
		}
		after := false
		for _, variant := range variants {
			if variant.ID == survivor.id {
				after = true
				continue
			}
			if !after {
				continue // older: the established item stays
			}
			err := a.mergeEntity(ctx, libraryID, survivor.id, variant.ID)
			if errors.Is(err, serverrole.ErrAttachedAuthorityRefused) {
				return nil // an attached server leaves catalog merges to the main one
			}
			if err != nil {
				return fmt.Errorf("merge title variant %s into %s: %w", variant.ID, survivor.id, err)
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

// mergeEntity folds loser into winner through the recoverable entity merge,
// after copying the loser's item state, and records the merge's batch in
// activity history as a title-fold repair with no actor, so it can be found
// and undone like a manual merge. The merge is committed before the record is
// written; a failed record is logged with the batch id, not retried.
func (a *hydrationAdapter) mergeEntity(ctx context.Context, libraryID store.LibraryID, winner, loser store.EntityID) error {
	if err := a.foldStore.CopyEntityItemState(ctx, loser, winner); err != nil {
		return err
	}
	now := time.Now().UTC()
	batch, err := a.foldStore.MergeEntities(ctx, winner, loser, metadataLibraryFilter, now)
	if err != nil || batch.ID == "" {
		return err
	}
	if err := a.foldStore.AppendActivity(ctx, store.ActivityEntry{
		OperationID: store.ActivityEntityTitleFoldMerge + ":" + batch.ID,
		OccurredAt:  now,
		Action:      store.ActivityEntityTitleFoldMerge,
		TargetType:  "batch",
		TargetID:    batch.ID,
		LibraryID:   libraryID,
	}); err != nil {
		slog.ErrorContext(ctx, "title fold merge not recorded in activity history", "batch", batch.ID, "winner", winner, "loser", loser, "error", err)
	}
	return nil
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
