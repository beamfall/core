# import-user-mapping Specification

## Purpose
How an import maps source users to server users: the mappings an import mutation accepts and how they shape job identity.

## Requirements

### Requirement: Source-user mappings on the import mutation

`runImport` SHALL accept an optional list of source-user-to-Profile mappings and SHALL forward it into
the migration job payload, so a per-user importer writes each source user's state to the mapped Profile.

#### Scenario: Two source users, two Profiles

- **WHEN** an admin runs an import mapping two source users to two distinct Profiles
- **THEN** the migration handler writes each user's watch state to that user's Profile only

#### Scenario: Unmapped source user

- **WHEN** the source reports a user absent from the mapping
- **THEN** that user's items are skipped and reported, and nothing is written to the run-level Profile

#### Scenario: Invalid mapping

- **WHEN** a mapping entry is malformed, has a blank or duplicate source user ID, or names an unknown or
  disabled Profile
- **THEN** the request fails with a client error and no job is enqueued

#### Scenario: Legacy request

- **WHEN** no mapping is supplied
- **THEN** the job payload and dedupe behaviour are unchanged

### Requirement: Mapping is part of job identity

The import job dedupe key SHALL include the canonical mapping.

#### Scenario: Reordered equivalent mapping

- **WHEN** two requests carry the same pairs in a different order
- **THEN** they resolve to the same active job

#### Scenario: Changed mapping

- **WHEN** a request changes a source user's target Profile or adds or removes a pair
- **THEN** it enqueues a distinct job
