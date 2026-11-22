## ADDED Requirements

### Requirement: Old doubled Library node addresses still resolve

The web viewer SHALL resolve an address that names a Library node by its legacy doubled ID
(`/n/library-library-films`) to the node now named `library-films`, and SHALL read a Library pivot
remembered under the legacy node ID when none is remembered under the current one. An exact match
on a live node's ID SHALL win over the legacy collapse.

#### Scenario: An old link opens the Library

- **GIVEN** the manifest has the node `library-films`
- **WHEN** the viewer opens `/n/library-library-films`
- **THEN** it shows the `library-films` node

#### Scenario: A pivot remembered before the change is kept

- **GIVEN** the pivot `/all` was remembered for `library-library-films`
- **AND** nothing is remembered for `library-films`
- **WHEN** the viewer reads the remembered pivot for `library-films`
- **THEN** it is `/all`
