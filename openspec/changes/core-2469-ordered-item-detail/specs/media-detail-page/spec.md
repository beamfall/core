## ADDED Requirements

### Requirement: Ordered item fields on the Media read

The `media(id)` read SHALL carry the item's stored `sequence` and `index` ordinals, a `headline`
locale map rendered from the owning plugin's MediaSubtitle segments that name no ordinal
placeholder, the `classLabel` the plugin declares for the item's class, and the stored
`videoWidth` and `videoHeight`. Each SHALL be omitted when unknown.

#### Scenario: Ordered item with a stored title

- **WHEN** an item stores sequence 1, index 3 and the field the plugin's title segment names, and
  its class has a declared label
- **THEN** `media(id)` returns sequence 1, index 3, that title as `headline` and the class label

### Requirement: Ordered item hero

For an item with a container credit and an `index`, the page SHALL link the grouping above the
nearest container first, then show a position line of the nearest container's kind name and the
item's `sequence`, linked to that container, and the item's class label and `index`, and SHALL use
the `headline` as the H1, falling back to the title. The hero SHALL use the item's `thumb` artwork
in a wide frame when present, and the poster otherwise.

#### Scenario: Ordered item with a headline

- **WHEN** the item is credited to container "Part Two" of kind "Part", grouped under "The Whole",
  with sequence 2, index 5, class label "Chapter" and headline "Arrival"
- **THEN** the page links "The Whole", shows "Part 2 · Chapter 5" with "Part 2" linking to the
  container, and the H1 reads "Arrival"

#### Scenario: No headline

- **WHEN** the same item has no headline
- **THEN** the H1 reads the item's title

### Requirement: Air date and stream badges

The facts row SHALL show the item's first date field as a date, a resolution badge derived from
`videoWidth` and `videoHeight`, an HDR badge when the first Version reports HDR, and the default
audio track's codec and channel layout, each only when known.

#### Scenario: A UHD HDR item

- **WHEN** the item is 3840 by 2160, its first Version reports HDR10 and its default audio track is
  E-AC-3 with 6 channels
- **THEN** the facts row shows "4K", "HDR10" and "Dolby Digital Plus 5.1"

### Requirement: More in the nearest container

For an item in an ordered container, the page SHALL show a rail headed "More in" and the nearest
container's title listing that container's other items in order, and the More with and Related rows
SHALL leave out that container's items.

#### Scenario: Container rail replaces repeats

- **WHEN** the item's container holds m1, m2 and m3 and the page shows m1, whose first billed person
  also appears in m2
- **THEN** a "More in" rail lists m2 and m3 and the More with row does not list m2

### Requirement: Grouping found without an item count

When no related Entity of the nearest container qualifies by its item count, the page SHALL take
the one related Entity of another kind with the container archetype as the grouping above it.

#### Scenario: Grouping without a count

- **WHEN** the nearest container relates to one container-archetype Entity of another kind that
  reports no item count
- **THEN** the page links that Entity before the container
