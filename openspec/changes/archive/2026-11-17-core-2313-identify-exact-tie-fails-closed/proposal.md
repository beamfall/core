# An exact top-score identification tie is queued for review

Ticket: CORE-2313 (`queue:core:main`).

## Why

Automatic identification scores every search candidate with the plugin's scorer and applies the
best one when it clears the plugin's threshold. When two different items share the top score, the
pipeline broke the tie with the provider's popularity and applied the more popular one. On the
owner's server two "The Office" series scored the same and the more popular one was attached
without review, so a library item could silently take the wrong series. Popularity says nothing
about which item a file is.

## What Changes

- `identify.Service` no longer consults popularity. When two or more candidates with different
  provider IDs share the top score, the item is not applied: it is queued in the Unmatched queue
  with the first top candidate, like a below-threshold result.
- Candidates with the same provider ID at the top score are the same item and still apply.
- Distinct scores are unchanged: the highest-scoring candidate applies when it clears the threshold.

## Impact

- Code: `internal/identify` (`bestCandidate`) and its tests.
- No store, migration, GraphQL or wire-contract change. Plugins and their scorers are unchanged.
