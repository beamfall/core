# Fix Match from any matched item

## Why

CORE-3125 implements the Core operations for ADR-0068 amendment H and §E.

## What Changes

- List searchable scrapers for the item's owning plugin, prioritizing the recorded match.
- Read the current match and the item's manually locked field values.
- Allow an operator to clear selected locks when applying a replacement match. A failed lookup
  or invalid field name preserves all locks.
- Verify that a match applied to an already identified item survives auto-identification across
  its file fingerprints.

The Console UI and scraper child support are follow-up work. These reads and the optional
mutation argument are additive. A source with no searchable scraper returns an empty list.
Rollback removes the new API additions. A successfully applied replacement is a durable edit.

## Impact

Core GraphQL, identification, and the management and client-core-wire contract artifacts.
Acceptance evidence comes from focused GraphQL, identification and contract checks.
