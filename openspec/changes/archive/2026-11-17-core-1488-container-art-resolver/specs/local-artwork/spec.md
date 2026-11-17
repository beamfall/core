## MODIFIED Requirements

### Requirement: Container art reaches the ordered container Entity

The scan SHALL map `season<NN>-poster` and `season-specials-poster`, found in the Media's folder
or its parent inside the library root, to the poster of the innermost ordered container Entity
with that ordinal, when the identifier resolves the Media's container Entities. The scan SHALL map
a folder `poster`, `folder` or `cover` image in the library's top-level folder that holds the
Media to the poster of the outermost container Entity. The identifier SHALL resolve the container
Entities innermost first, inside the Media's Library, from the kinds the Library's plugin declares
as containers.

#### Scenario: A container poster

- **WHEN** `Show/season01-poster.jpg` exists and a Media under `Show/` resolves to the container
  Entity with ordinal 1
- **THEN** that Entity's poster holds the bytes of `season01-poster.jpg`

#### Scenario: An outer container poster

- **WHEN** `Show/poster.jpg` exists and a Media in `Show/Season 01/` resolves to an ordered
  container Entity inside an outer container Entity
- **THEN** the outer Entity's poster holds the bytes of `poster.jpg`

#### Scenario: A folder outside the library root

- **WHEN** the library root is `Show/Season 01` and `Show/poster.jpg` exists above it
- **THEN** no container Entity receives that image
