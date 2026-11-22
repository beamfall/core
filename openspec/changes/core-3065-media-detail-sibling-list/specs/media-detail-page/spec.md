## MODIFIED Requirements

### Requirement: More in the nearest container

For an item in an ordered container that the page does not list as numbered rows, the page SHALL
show a rail headed "More in" and the nearest container's title listing that container's other items
in order, and the More with and Related rows SHALL leave out that container's items.

#### Scenario: Container rail replaces repeats

- **WHEN** the item's container holds video items m1, m2 and m3 and the page shows m1, whose first
  billed person also appears in m2
- **THEN** a "More in" rail lists m2 and m3 and the More with row does not list m2

## ADDED Requirements

### Requirement: Numbered container list on the item page

When the nearest container is one the Entity page lists as numbered rows (a `container` archetype
whose member Media are all class `audio`), the page SHALL show that same list, every item included,
headed by the container's title linked to its page, with the current item's row marked as the
current page, and SHALL NOT show the "More in" rail. The More with and Related rows SHALL still leave
out that container's items.

#### Scenario: Audio container lists with the item marked

- **WHEN** the page shows m1, an audio item whose container holds audio items m0 and m1 under stored
  sequence 1 and m2 under sequence 2
- **THEN** the page lists "Disc 1" with m0 and m1 and "Disc 2" with m2, the m1 row carries
  `aria-current="page"`, and no "More in" rail shows

#### Scenario: Video container keeps the rail

- **WHEN** the page shows an item whose container's items are class `video`
- **THEN** the "More in" rail shows and no numbered list does
