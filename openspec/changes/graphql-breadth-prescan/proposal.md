# Bound GraphQL document breadth before parsing

Ticket: CORE-0371 (`queue:core:main`; roadmap item COREAPI-GRAPHQL-BREADTH-PREPARSE-COST-1).

## Why

The pre-parse scan added for CORE-0052 bounds nesting depth only. `POST /api/graphql` still hands
`parser.ParseQuery` any shallow document inside the 256 KiB body cap without a credential, and the
parser allocates per token: a depth-1 document of 256 KiB (`{a a a …}`) allocates about 38 MB
during parse, more than the unguarded nested case did. Other shallow shapes at the cap allocate
12–49 MB each: aliases, arguments, list values, input-object fields, directives, variable
definitions, fragment spreads, comments (the parser keeps every comment) and repeated operations.
The pre-parse cost of an unauthenticated request is still linear in body size on the breadth axis.

## What Changes

- The pre-parse scan also counts lexical tokens (names, numbers, strings, block strings,
  punctuators and comments; commas and whitespace are ignored, as in the lexer). The count never
  falls below the number of tokens the parser reads.
- A document of more than `maxDocumentTokens` (8192) tokens is rejected before parsing with the
  existing `invalid GraphQL document` error, the refusal the parser and the value-depth guard
  already return. No new error is introduced.
- The budget is 2 × `maxDocumentSelections`, so every document that fits the existing selection-work
  budget still passes the token budget. The worst accepted document allocates about 2.5 MB, under
  the 4 MiB pre-parse budget CORE-0052 pinned for nested documents.

A token budget is chosen over lowering the body cap because the parser's allocation depends on the
token count, not the byte count: a lower body cap would still admit the worst shapes at a smaller
size, and would also refuse legitimate documents with long string arguments.

## Non-goals

- Changing the request body cap or the post-parse selection, occurrence and fragment budgets.
- Block-string line splitting, which allocates per line inside a single token (about 5 MB for a
  256 KiB block string of newlines). It is a separate axis and separate work.

## Failure modes

- Documents rejected by the new budget were accepted before only if they also passed validation;
  no document within `maxDocumentSelections` and a realistic argument load reaches 8192 tokens.
- Malformed input may be over-counted (a float counts as more than one token), which only rejects
  earlier documents the parser would also reject or that are already over budget.

## Acceptance evidence

- `internal/graphquery/nesting_test.go` covers token counting, the exact boundary, rejection of
  256 KiB breadth documents before the parser runs, and peak allocation under 4 MiB for both the
  refused 256 KiB documents and the largest accepted document of each shape.
- `FuzzDocumentNesting` also checks that the token count bounds the tokens the parser reads.
- Existing depth tests in `internal/graph` and `internal/graphquery` still pass.

## Rollback

Revert the change. No stored state or wire contract depends on it.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `graphql-document-budgets`: adds a pre-parse token budget.
