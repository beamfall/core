## ADDED Requirements

### Requirement: Ordered items sit together in order

Search SHALL keep the Media rows of one result page that share an ordered container root (the
container-archetype Entity the item's ordered container links to, or the container itself)
together at the position of the first one, ordered by `sequence` then `index`, ties keeping rank
order.

#### Scenario: Scattered items regroup

- **WHEN** a search page ranks container X's items with ordinals 2·1, then an unrelated item, then
  1·2 and 1·1
- **THEN** the page lists 1·1, 1·2 and 2·1 together first, then the unrelated item
