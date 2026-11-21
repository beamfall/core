## ADDED Requirements

### Requirement: Source items expose catalog ownership

Core SHALL expose optional `libraryID` for catalog-backed source rows and Station candidates,
using the owning library from the catalog read that supplies the row title. A rolled-up entity
SHALL carry its own library. A synthetic row without a single proven owner SHALL omit the field.

#### Scenario: Cross-library sources with matching titles

- **WHEN** Recently Added, Continue Watching, plain Search, or enriched Search returns items with
  matching titles from two visible libraries
- **THEN** each item carries its own library ID so a client can filter the rows by library

#### Scenario: A restricted library is unrevealed

- **WHEN** the session has yet to reveal a restricted library
- **THEN** Home and Search omit its rows and their provenance
