## ADDED Requirements

### Requirement: Web cards are headed by their container

The web viewer SHALL head a Media card that carries `container` with the container's title and show
the item's subtitle, or its own title when it has no subtitle, as its one second line. A card shown
on an Entity page SHALL keep the item's own title.

#### Scenario: Card heading

- **WHEN** a home shelf shows an item titled "S03E01 · From Hell" with subtitle "S3 · E1 · From
  Hell" held by container "Ted Lasso"
- **THEN** the card reads "Ted Lasso" over "S3 · E1 · From Hell"

### Requirement: Recently Added groups items by container

The Recently Added shelf SHALL collapse two or more items sharing a container into one card for the
container that states how many items are new and opens the container.

#### Scenario: Recently Added grouping

- **WHEN** the Recently Added shelf receives three items held by container "Ted Lasso"
- **THEN** it shows one "Ted Lasso" card stating 3 new that opens the container

### Requirement: Search lists items under their container

The search page SHALL list a Media row whose container is also a result row, and whose own title
does not contain the search term, under that container's row, collapsed behind a count the viewer
can expand. A row whose own title contains the term SHALL stay in its type group.

#### Scenario: Search grouping

- **WHEN** a search returns the container "Ted Lasso" and 44 items it holds
- **THEN** the page shows the "Ted Lasso" row first with a count of 44 that expands to the items
