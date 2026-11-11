## ADDED Requirements

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
