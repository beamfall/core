## ADDED Requirements

### Requirement: Library navigation node IDs carry the prefix once

The Navigation Manifest SHALL name a Library's node with the `library-` prefix exactly once: the
Library ID when it already starts with `library-`, otherwise the Library ID with the prefix added.
A server-level navigation override that names a Library node by its legacy doubled ID
(`library-` + Library ID) SHALL still hide, order and relabel that node. When an override names a
node by both IDs, the current ID SHALL win, and a legacy ID that is also a live node's ID SHALL name
that live node.

#### Scenario: A Library node ID is its Library ID

- **WHEN** the manifest lists the Library `library-films`
- **THEN** its navigation node ID is `library-films`, not `library-library-films`

#### Scenario: A legacy override still applies

- **GIVEN** a stored override that hides `library-library-films` and orders `library-library-shows` first
- **WHEN** the manifest is built
- **THEN** the `library-films` node is hidden and the `library-shows` node comes first

#### Scenario: The current ID wins over the legacy one

- **GIVEN** a stored override that labels `library-library-shows` "Series" and `library-shows` "TV"
- **WHEN** the manifest is built
- **THEN** the `library-shows` node is titled "TV"

#### Scenario: A live node's own ID is not an alias

- **GIVEN** Libraries `library-films` and `library-library-films` (titled "Library Films")
- **AND** a stored override that labels `library-library-films`
- **WHEN** the manifest is built
- **THEN** the label applies to the `library-library-films` node and not to `library-films`
