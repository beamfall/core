## ADDED Requirements

### Requirement: Chrome paints before the session settles

The web viewer SHALL render its app chrome and a loading skeleton while the session bootstrap is
pending, and SHALL NOT mount any Viewer route until a session is bound.

#### Scenario: Cold load

- **WHEN** the viewer loads and the session bootstrap has not settled
- **THEN** the app chrome and a skeleton render, and no route-local request is issued

#### Scenario: Unenrolled browser

- **WHEN** the session bootstrap reports that the browser is not enrolled
- **THEN** the viewer routes to the setup wizard or the enroll page exactly as before

### Requirement: Identity-scoped viewer data cache

Loaded section items and entity details SHALL be cached in memory only, keyed by the bound
identity (device role, Profile and Reveal state), bounded in size, and cleared at every
identity-epoch transition (Profile switch, Reveal, relock, device-role change, refresh, Reveal
expiry, compatibility block) and on any authorization rejection.

#### Scenario: Return to a loaded section

- **WHEN** a section that was already loaded for the current identity mounts again
- **THEN** its cached items render without a skeleton and are revalidated in the background

#### Scenario: Different identity

- **WHEN** data was cached for one identity and a different identity is bound
- **THEN** none of the cached data is rendered

#### Scenario: Abandoned view

- **WHEN** a section or entity view unmounts before its fetch settles
- **THEN** the fetch is aborted and its result is neither rendered nor cached

### Requirement: Entity page starts without waiting

The entity page SHALL render the entity title from the identity-scoped cache or from
identity-matched navigation state while the entity loads, and SHALL start requests that do not
depend on the entity result in parallel with it.

#### Scenario: Entity missing or inaccessible

- **WHEN** the entity request resolves to no entity
- **THEN** only the leak-safe not-available surface renders, and no preview title remains
