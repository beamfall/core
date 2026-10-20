-- Container child ordering (ADR-0003 §2 OrderedChildren, the play-next seed):
-- an optional per-relation-row position. NULL means unordered — entity-media
-- reads put NULL rows after ordered ones, then keep the title order.
ALTER TABLE entity_relation ADD COLUMN sort_order INTEGER;
