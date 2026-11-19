## ADDED Requirements

### Requirement: Category counts use the visible browse unit

The Categories pivot SHALL count distinct outermost container IDs when every visible member of a category belongs to the same plugin-declared browse container kind. It SHALL name that unit using the plugin's browse label. Raw Media counts SHALL remain available. A category of loose Media, mixed units, or a kind without a browse label SHALL retain its item count. Visibility filtering SHALL precede counting.

#### Scenario: Several children of two containers

- **WHEN** a category contains several visible Media from two outermost containers of one browse kind
- **THEN** its tile shows two with the plugin's label, irrespective of the number of children

#### Scenario: Loose Media

- **WHEN** a category contains three visible Media without containers
- **THEN** its tile shows three items
