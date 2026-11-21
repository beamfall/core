## ADDED Requirements

### Requirement: The page states its purpose

The Manage page at `/manage/curation` SHALL be titled "Versions & links" and SHALL say, before any
subject is open, that it shows the files behind an item, links its extras and alternate editions,
and orders the items in a group.

#### Scenario: Manager opens the page

- **WHEN** a manager opens `/manage/curation` with no subject open
- **THEN** the heading, lead and search section explain those three jobs and invite a search

### Requirement: Subjects are found by search

The page SHALL find its subject through catalog search and SHALL NOT ask for a raw ID or a subject
type. An Entity result SHALL open the group view and a Media result the item view, and the open
subject SHALL be kept in the URL query.

#### Scenario: Manager picks a search result

- **WHEN** the manager searches and picks a Media result
- **THEN** the item view opens for it and the URL carries `open=<id>&as=media`

#### Scenario: Reload keeps the subject

- **WHEN** the page loads with `open=<id>&as=entity`
- **THEN** the group view opens for that Entity without a search

### Requirement: No duplicate field editor

The page SHALL NOT list a subject's fields; an item view SHALL link to the Item metadata page for
the item.

#### Scenario: Item view

- **WHEN** an item view is open
- **THEN** it offers an "Edit details" link to `/manage/items/<id>` and shows no fields table

### Requirement: Extras and editions link in the read direction

Linking an extra or an alternate edition SHALL record the relation from the picked item to the open
item, the direction the scanner writes and `mediaRelations` reads, so the new link is listed under
the open item.

#### Scenario: Manager adds an extra

- **WHEN** the manager picks item B under "Extras" while item A is open
- **THEN** the page calls `linkMediaRelation(fromMediaID: B, relation: "extra-of", toMediaID: A)`
- **AND** reloads A's extras

### Requirement: Grouped sections with empty states

The item view SHALL show "Files", "Extras" and "Alternate editions" as separate sections, and the
group view "Items in this group"; each SHALL carry a one-line explanation and a plain empty state,
and sections for the other subject type SHALL NOT be rendered.

#### Scenario: Item with no extras

- **WHEN** an item with no extras is open
- **THEN** the Extras section says none are linked and offers "Add an extra"
- **AND** no group-ordering section is shown

### Requirement: Group items are ordered and appended

The group view SHALL read every relation row of the group and move each row only among the rows
of its own relation. A move or an add SHALL renumber that relation's rows 0..n-1 where they differ.
"Add an item" SHALL append the picked item after the last one, using an existing link type of the
group or a typed one, and SHALL NOT rewrite an item already in that relation.

#### Scenario: Manager adds an item to a group

- **WHEN** the group's relation holds items numbered 0 and 1 and the manager adds item C
- **THEN** the page upserts the relation to C with sort order 2 and rewrites nothing else

#### Scenario: Unnumbered items

- **WHEN** a relation holds an item numbered 0 and an unnumbered one, and the manager adds item C
- **THEN** the unnumbered item is written as 1 and C as 2

#### Scenario: Item already in the group

- **WHEN** the manager picks an item already in the chosen relation
- **THEN** the page says so and writes nothing

### Requirement: Group changes save one at a time

The group view SHALL save one change at a time, with its controls disabled meanwhile, and SHALL
reload after every save, including one that failed partway.

#### Scenario: A move fails partway

- **WHEN** the first of two renumbering writes succeeds and the second fails
- **THEN** the page says the change was not saved and shows the order the server now holds

### Requirement: Re-ordering keeps relation attributes

`upsertEntityRelation` SHALL leave an existing row's attributes unchanged; only a new row is written
without attributes.

#### Scenario: Manager moves a scanned item

- **WHEN** a relation row carries attributes from the scanner and the manager moves it
- **THEN** the row's sort order changes and its attributes are kept
