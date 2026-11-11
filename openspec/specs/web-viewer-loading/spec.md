# web-viewer-loading Specification

## Purpose
Make the web viewer appear immediately: the chrome paints before the session settles, viewer data is cached per identity, and entity pages show their title and start independent requests while the entity loads.

## Requirements

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

### Requirement: Resolved route chunks render without a Suspense fallback

A lazy web route whose chunk is already resolved SHALL render its content on the first read without
committing a Suspense fallback, and a route whose chunk is not resolved SHALL still render the
route fallback.

#### Scenario: Cold load with a fast chunk

- **WHEN** the matched initial area chunk resolves within the entry's bounded wait
- **THEN** the area renders on the first render and its requests start without a fallback delay

#### Scenario: Player opened after preload

- **WHEN** `preloadPlayer()` has resolved and the viewer opens a watch route
- **THEN** the player page renders without suspending

#### Scenario: Slow network

- **WHEN** the route chunk has not resolved
- **THEN** the route fallback renders until it does
