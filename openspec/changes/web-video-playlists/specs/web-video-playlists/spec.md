## ADDED Requirements

### Requirement: Collection feed through sourceItems

The `sourceItems` query SHALL accept kind `collection` with a Collection id as `value` and SHALL
return that Collection's members in curated order, paged by `limit` and `offset`. Members the
session cannot see SHALL be omitted, and an unknown, trashed or invisible Collection SHALL return
an empty feed.

#### Scenario: Curated order

- **WHEN** a manager reorders a Collection and a viewer asks for its feed
- **THEN** the members come back in the new order

#### Scenario: Hidden member

- **WHEN** a Collection holds a member the session's parental rating hides
- **THEN** the feed omits that member and keeps the others in order

### Requirement: Web viewer lists collections

The web viewer SHALL offer a `/collections` page listing every Collection the viewer can see,
grouped by library, each linking to its own page and showing its entry count. Managers SHALL be
able to create a Collection in a chosen library from that page.

#### Scenario: Manager creates a collection

- **WHEN** a manager enters a name, picks a library and creates the collection
- **THEN** the collection is created in that library and its page opens

#### Scenario: Viewer without manager role

- **WHEN** a viewer without the manager role opens the page
- **THEN** the collections are listed and no create form is shown

### Requirement: Web viewer collection page with ordered entries

The web viewer SHALL offer a page per Collection that shows its entries in curated order and a
Play all action that opens the first entry with the Collection as the play queue. Managers SHALL be
able to move entries up or down, remove entries and add Media from the same library. A move SHALL
keep members the viewer cannot see in their places.

#### Scenario: Play all

- **WHEN** a viewer chooses Play all
- **THEN** the first entry plays and the play queue holds the entries in curated order

#### Scenario: Reorder around a hidden member

- **WHEN** a manager moves a visible entry past a neighbour while a hidden member sits between them
- **THEN** the saved order swaps the two visible entries and the hidden member keeps its position

#### Scenario: Failed save

- **WHEN** a reorder, remove or add fails
- **THEN** the page shows an alert and does not report success
