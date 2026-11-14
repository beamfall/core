# Give every GraphQL error a code from a closed vocabulary

Ticket: CORE-0053 (`queue:core:main`, COREAPI-AUDIT-0822-6).

## Why

`docs/architecture/ARCHITECTURE.md` §7 declares that GraphQL errors carry
`extensions.code ∈ {NOT_FOUND, FORBIDDEN, UNAUTHENTICATED, THROTTLED, INVALID, CONFLICT, INTERNAL}`,
but the `graphError` the handler emits carries only `message` and `path`. Clients must
string-match English prose to tell re-auth from throttling from not-found, and the error shape
is absent from the published wire contract, so rewording a message breaks clients with no drift
gate to catch it.

## What Changes

- Every GraphQL error carries `extensions.code` from the closed set. One function in
  `internal/graph` maps each error to its code: client-caused errors by their stable message,
  store validation errors to `INVALID`, and every other failure to `INTERNAL`.
- The ARCHITECTURE §6 denial-of-existence rule holds: content the caller cannot see answers
  `NOT_FOUND`, never `FORBIDDEN`. `FORBIDDEN` is only for role, tier, session-kind and Reveal
  gates.
- A conformance test enumerates every error site in `internal/graph` and fails when one has no
  code or a client error message is not mapped.
- The client-core-wire contract gains a `graphqlErrors` section with the error shape and the
  code vocabulary, published as minor version 2.4.0. Consumer repins are follow-up work.
