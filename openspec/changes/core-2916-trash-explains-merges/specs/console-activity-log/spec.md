## ADDED Requirements

### Requirement: Server-initiated entries name their system actor

An activity entry the server writes on its own, with no session, token or key, SHALL be able to
carry a system actor from a closed vocabulary. The automatic Version merge's `media.merge` entry
SHALL carry the system actor `scan`. `activityLog` and `activityEntry` SHALL expose it as
`systemActor`, and the activity sentence SHALL name the library scan as the actor.

#### Scenario: An automatic merge reads as the scan's

- **WHEN** a scan merges verified copies and the operator opens the activity log
- **THEN** the `media.merge` row has `systemActor` `scan` and reads as done by the library scan
