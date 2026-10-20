package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// EntityKindOrdering identifies the registered number field that supplies an
// ordered container kind's child ordinals.
type EntityKindOrdering struct {
	Kind        string
	SourceField string
}

// RegisterEntityKindOrderings installs valid ordering declarations against
// the already-registered field-index set. A declaration that does not name a
// registered number field remains inactive.
func (s *Store) RegisterEntityKindOrderings(ctx context.Context, orderings []EntityKindOrdering) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, ordering := range orderings {
		if !entityIndexNamePattern.MatchString(ordering.Kind) {
			return fmt.Errorf("entity kind ordering kind %q must be a lowercase slug", ordering.Kind)
		}
		if !entityIndexNamePattern.MatchString(ordering.SourceField) {
			return fmt.Errorf("entity kind ordering source field %q must be a lowercase slug", ordering.SourceField)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	for _, ordering := range orderings {
		fields := s.entityIndexes[ordering.Kind]
		source, ok := fields[ordering.SourceField]
		if !ok || source.Type != EntityFieldNumber {
			continue
		}
		for name, field := range fields {
			field.orderingSource = false
			fields[name] = field
		}
		source.orderingSource = true
		fields[ordering.SourceField] = source
	}
	return nil
}

// EntityKindOrdering returns one active ordering declaration.
func (s *Store) EntityKindOrdering(kind string) (EntityKindOrdering, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.entityKindOrderingLocked(kind)
}

func (s *Store) entityKindOrderingLocked(kind string) (EntityKindOrdering, bool) {
	for field, spec := range s.entityIndexes[kind] {
		if spec.orderingSource && spec.Type == EntityFieldNumber {
			return EntityKindOrdering{Kind: kind, SourceField: field}, true
		}
	}
	return EntityKindOrdering{}, false
}

type relatedEntityOrder struct {
	relationOrder    int64
	ordinal          float64
	hasRelationOrder bool
	hasOrdinal       bool
}

func (s *Store) orderRelatedEntities(ctx context.Context, parentID EntityID, relationName string, related []RelatedEntity) ([]RelatedEntity, error) {
	ordering, ok, err := s.relatedEntityOrdering(ctx, parentID)
	if err != nil || !ok || len(related) < 2 {
		return related, err
	}
	orders, err := s.relatedEntityOrders(ctx, parentID, relationName, ordering)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(related, func(i, j int) bool {
		left := orders[related[i].Entity.ID]
		right := orders[related[j].Entity.ID]
		if left.hasRelationOrder != right.hasRelationOrder {
			return left.hasRelationOrder
		}
		if left.hasRelationOrder && left.relationOrder != right.relationOrder {
			return left.relationOrder < right.relationOrder
		}
		if !left.hasRelationOrder && left.hasOrdinal != right.hasOrdinal {
			return left.hasOrdinal
		}
		if !left.hasRelationOrder && left.hasOrdinal && left.ordinal != right.ordinal {
			return left.ordinal < right.ordinal
		}
		if related[i].Entity.Title != related[j].Entity.Title {
			return related[i].Entity.Title < related[j].Entity.Title
		}
		return related[i].Entity.ID < related[j].Entity.ID
	})
	return related, nil
}

func (s *Store) relatedEntityOrdering(ctx context.Context, parentID EntityID) (EntityKindOrdering, bool, error) {
	var kind string
	if err := s.catalogR.QueryRowContext(ctx, `SELECT kind FROM entity WHERE id = ?`, parentID).Scan(&kind); err != nil {
		return EntityKindOrdering{}, false, err
	}
	ordering, ok := s.entityKindOrderingLocked(kind)
	if !ok {
		return EntityKindOrdering{}, false, nil
	}
	return ordering, true, nil
}

func (s *Store) relatedEntityOrders(ctx context.Context, parentID EntityID, relationName string, ordering EntityKindOrdering) (map[EntityID]relatedEntityOrder, error) {
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT r.to_entity_id, r.sort_order, f.value
		FROM entity_relation r
		LEFT JOIN entity_field_real f
		  ON f.entity_id = r.to_entity_id AND f.kind = ? AND f.field = ?
		WHERE r.from_entity_id = ? AND r.relation = ? AND r.to_entity_id IS NOT NULL
	`, ordering.Kind, ordering.SourceField, parentID, relationName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	orders := map[EntityID]relatedEntityOrder{}
	for rows.Next() {
		var (
			id         EntityID
			relation   sql.NullInt64
			fieldValue sql.NullFloat64
		)
		if err := rows.Scan(&id, &relation, &fieldValue); err != nil {
			return nil, err
		}
		orders[id] = relatedEntityOrder{
			relationOrder:    relation.Int64,
			ordinal:          fieldValue.Float64,
			hasRelationOrder: relation.Valid,
			hasOrdinal:       fieldValue.Valid,
		}
	}
	return orders, rows.Err()
}
