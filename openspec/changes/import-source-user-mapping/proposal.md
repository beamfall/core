# Carry source-user mappings through the import mutation

Ticket: CORE-0905 (`queue:core:main`). ADR-0037 section 5, ADR-0178 section 4.

## Why

`migrate.Payload` already carries `SourceUserProfileIDs` and the handler writes each source user's
per-Profile state to the mapped Profile, but `runImport` neither accepts nor forwards that map. A
per-user importer therefore cannot be driven through the API, and the dedupe key ignores any mapping.

## What Changes

- `runImport` accepts an optional `sourceUserMappings` variable: a list of `{sourceUserID, profileID}`
  pairs. Each pair is validated and forwarded into the job payload as `source_user_profile_ids`.
- Malformed entries, blank or duplicate source user IDs, and unknown or disabled Profiles are client
  errors. Admin authorization is the existing `runImport` gate.
- The dedupe key includes the canonical (sorted) mapping, so equivalent reordered mappings dedupe to one
  active job and a changed mapping or target Profile never aliases another.
- Source users left out of the mapping stay skipped and reported by the existing handler; they are
  never written to the run-level Profile.

## Non-goals

No schema, SDK interface, migration or new import pipeline. Legacy requests without a mapping are
unchanged.
