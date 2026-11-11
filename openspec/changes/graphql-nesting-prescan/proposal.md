# Bound GraphQL document nesting before parsing

Ticket: CORE-0052 (`queue:core:main`; roadmap item COREAPI-AUDIT-0822-1).

## Why

`POST /api/graphql` reaches `graphquery.PrepareOperation` without a credential. It calls
`parser.ParseQuery` before any request-cost budget runs, and the parser recurses once per nested
selection set, list or input object. A body of nested braces inside the 256 KiB cap grows one
goroutine's stack by well over a hundred megabytes, and nothing bounds concurrent anonymous
requests. The work done for an unauthenticated request must be bounded by a small constant before
the parser runs.

## What Changes

- `PrepareOperation` (and the `TopLevelFields` helper) run a linear, allocation-free lexical scan of
  the document before `parser.ParseQuery`. The scan skips strings, block strings and comments.
- Selection-set nesting deeper than `maxSelectionDepth` (64) is rejected with the existing
  `GraphQL selection depth exceeds 64` error, now before parsing.
- List, input-object and list-type nesting inside arguments and variable definitions deeper than
  the new `maxValueDepth` (64) budget is rejected with the existing `invalid GraphQL document`
  error.
- A closing brace or bracket with no matching opener is rejected with `invalid GraphQL document`;
  the parser rejected such documents already.

## Non-goals

- A process memory limit (`debug.SetMemoryLimit`) or a connection limit. Both belong to the
  composition root and are separate work.
- Changing the request body cap.

## Failure modes

- The lexical depth of a fragment definition counts from its own braces, so the scan is a
  necessary bound; validation still checks composed depth through fragment spreads.
- Unselected operations and unreachable fragments are now subject to the lexical bound although
  validation never visits them. Such documents were accepted before only if they also parsed.

## Acceptance evidence

- `internal/graphquery/nesting_test.go` covers boundary and over-limit documents, string and comment
  skipping, rejection before the parser runs, and peak allocation under 4 MiB for 256 KiB
  pathological documents.
- `FuzzDocumentNesting` checks the scan against the parsed AST and is listed in
  `testing/fuzz-targets.json`.
- Existing depth and selection tests in `internal/graph` and `internal/graphquery` still pass.

## Rollback

Revert the change. No stored state or wire contract depends on it.

## Capabilities

### New Capabilities

- `graphql-document-budgets`: request-cost budgets applied to GraphQL documents before parsing.

### Modified Capabilities

None.
