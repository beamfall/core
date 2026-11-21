## ADDED Requirements

### Requirement: Server-initiated entries name their system actor

An activity entry the server writes on its own, with no session, Profile, token or key, SHALL name
its system actor from a closed vocabulary, derived from the action and not stored. A `media.merge`
entry with none of those SHALL have the system actor `scan`; a `media.merge` a session, token or key
made SHALL have none. `activityLog` and `activityEntry` SHALL expose it as `systemActor`, and the
activity sentence SHALL name the library scan as the actor.

#### Scenario: An automatic merge reads as the scan's

- **WHEN** a scan merges verified copies and the operator opens the activity log
- **THEN** the `media.merge` row has `systemActor` `scan` and reads as done by the library scan
