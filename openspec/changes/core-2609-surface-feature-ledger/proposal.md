# Surface feature ledger: an explicit decision per surface, a release gate and gap tickets

Ticket: CORE-2609 (`queue:core:main`), the first slice of the surface feature ledger.
Decision: ADR-0237 (proposed), which amends ADR-0213 §3.

## Why

`testing/features.yaml` says which targets a feature applies to, but a surface missing from
`applies` could mean "never", "nobody decided" or "forgot". About 1,000 of the 1,737 cells
are in that state, including most of the macOS and Windows columns, against the owner's rule
that those viewers support everything the server supports. The registry has no release
dimension, so it cannot say what ships where in a release, and a missing cell links to no ticket.

## What Changes

- Each feature may carry three blocks of `surface: value` lines:
  - `unsupported:` gives the reason a surface will not get the feature;
  - `tickets:` names the ticket tracking a planned cell;
  - `release:` names the first release the feature appears in on that surface.
- A top-level `fullParity: [web, macos, windows]` list names the surfaces that must support
  everything. Excluding a feature on one of them needs the owner's dated decision in the reason.
- `featurecheck` classifies every feature × surface cell as one of four states:
  - supported: in `applies` and proven;
  - planned: in `applies`, not yet proven;
  - unsupported;
  - undecided.

  It prints the counts per surface. `server` is a proof target, not a surface.
- `featurecheck -release X.Y` fails unless every cell with `release <= X.Y` is proven. An
  absent target root fails closed.
- `featurecheck -gaps` lists unticketed planned cells grouped by surface and area, which is the
  input for filing gap tickets.
- The ratchet gains per-surface `maxUndecided` and `maxUnticketed` ceilings, set at today's
  values. The gate step's command line is unchanged.

## Non-goals

- Crediting proofs per surface instead of per repo (CORE-2610).
- Mapping GraphQL fields to features (CORE-2611).
- The owner's 1.0 release and exclusion decisions (CORE-2612).
- Filing gap tickets (CORE-2613).
- A generated release manifest that clients read (CORE-2614).

## Failure modes

- A malformed block (inline map, duplicate surface, entry without a colon, empty block) fails
  registry parsing with exit 2.
- An unsupported surface also in `applies`, a ticket or release outside `applies`, a malformed
  ticket ID or version, or a full-parity exclusion without `(owner YYYY-MM-DD)` fails registry
  parsing.
- A new feature, or a new surface in `applies`, with no ticket raises the unticketed count. A new
  feature that leaves surfaces undecided raises the undecided count. Either fails the ratchet once
  it passes the ceiling.
