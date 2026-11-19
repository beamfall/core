# feature-coverage-ledger Specification

## Purpose
Defines the surface feature ledger: every feature records an explicit decision for every client surface (supported with proof, planned, unsupported with a reason, or undecided), so the registry can say what ships on each surface in a release and every gap links to a ticket.

## Requirements

### Requirement: Explicit surface decisions

The feature registry SHALL record, for every feature and every surface other than `server`, one of
supported (in `applies` with a proof), planned (in `applies` without a proof), unsupported (an
`unsupported:` reason) or undecided (none of these), and `featurecheck` SHALL report the count of
each state per surface.

#### Scenario: Unsupported surface with a reason

- **WHEN** a feature lists `androidtv: Remote-only input.` under `unsupported:` and androidtv is
  not in its `applies`
- **THEN** the cell counts as unsupported for androidtv

#### Scenario: Unsupported surface that also applies

- **WHEN** a feature lists a surface both in `applies` and under `unsupported:`
- **THEN** `featurecheck` fails registry parsing with exit 2

#### Scenario: Full-parity exclusion without an owner decision

- **WHEN** a feature lists `macos` under `unsupported:`, `macos` is in `fullParity`, and the
  reason has no `(owner YYYY-MM-DD)`
- **THEN** `featurecheck` fails registry parsing with exit 2

#### Scenario: Ticket or release outside applies

- **WHEN** a feature names a ticket or a release for a surface not in its `applies`
- **THEN** `featurecheck` fails registry parsing with exit 2

### Requirement: Release feature gate

`featurecheck -release X.Y` SHALL exit non-zero unless every cell whose `release:` version is at
or before X.Y is proven, and SHALL count a cell whose target root is absent as unproven.

#### Scenario: Release-targeted cell without proof

- **WHEN** a feature has `release: {tvos: "1.1"}`, no tvOS proof, and `-release 1.1` runs
- **THEN** `featurecheck` exits 1 and names the feature and surface

#### Scenario: Later release is not checked

- **WHEN** the same feature is checked with `-release 1.0`
- **THEN** the tvOS cell does not fail the run

### Requirement: Decision and ticket ratchet

The coverage ratchet SHALL accept per-surface `maxUndecided` and `maxUnticketed` ceilings, and
`featurecheck -ratchet` SHALL fail when a surface's undecided cells, or its planned, unproven
cells with no ticket on an assessable surface, exceed the ceiling. The ceilings SHALL be refused
for `server`.

#### Scenario: New feature leaves surfaces undecided

- **WHEN** a feature is added without deciding a surface whose undecided count is at its ceiling
- **THEN** the ratchet fails for that surface

#### Scenario: Gap listing

- **WHEN** `featurecheck -gaps` runs
- **THEN** it lists every unticketed planned cell, grouped by surface and area
