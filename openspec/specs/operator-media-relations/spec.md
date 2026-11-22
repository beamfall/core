# operator-media-relations Specification

## Purpose
Lets managers correct how Media links to Entities (sets, credits and other declared
relations) from the web core, with automated hydration preserving operator links and every
change recoverable through attributed before and after rows.

## Requirements

### Requirement: Managers control Entity to Media links
The server SHALL authorize relation writes with the same manager role and Media visibility
as Fix Match. It SHALL validate sets against the Media's active bundle, reuse a live
same-kind Entity with an equivalent normalized name when creating by name, and keep before
and after relation rows with actor attribution for recovery.

#### Scenario: A single-valued relation is changed
- **WHEN** a manager sets a declared single-valued relation on visible Media
- **THEN** the selected Entity is the only link under that relation name
- **AND** automated hydration preserves that link and its attributes

#### Scenario: A multi-valued relation is edited
- **WHEN** a manager adds or updates a link on a declared multi-valued relation
- **THEN** hydration preserves that link and may replace other unlocked links

#### Scenario: A manager removes a link
- **WHEN** the manager removes the specified link from visible Media
- **THEN** hydration may later re-create a provider-derived link

### Requirement: Managers can find and inspect relations
The server SHALL offer a visible Entity name-prefix search by kind, capped at 50 results.
The Media-side relation read SHALL indicate operator-locked links, and SHALL flag such a
link as undeclared when the active bundle no longer declares it.

#### Scenario: A bundle drops a relation declaration
- **WHEN** a manager reads the Media's relations after the bundle drops a declaration
- **THEN** the existing locked link remains visible with the undeclared flag
