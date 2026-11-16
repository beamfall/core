# Refresh session authority between fields of one GraphQL mutation

Ticket: CORE-0742 (`queue:core:main`).

## Why

The GraphQL handler resolves the caller's `auth.Scope` once per request and hands that same scope
to every field of a mutation operation, which run in order. A field that changes the session's
authority (`switchProfile`, `reveal`, `relock`, an Admin Session change) does not change the
scope the later fields see. A session on Profile A that sends `switchProfile` to Profile B and
then `reveal` with A's Reveal PIN in one operation verifies A's PIN and marks the session, now
bound to B, as revealed. Later fields likewise keep A's Library grants, Reveal state and Admin
Session after the switch cleared them (ADR-0004: Reveal never survives a Profile switch).

## What Changes

- After a mutation field runs, the server re-resolves the session's scope from the store before
  the next field of the same operation runs, and authorizes that field again against the
  refreshed scope (manager role, Admin Session).
- Every later field acts on the Profile, Library grants, Reveal state and Admin Session the
  session holds at that point, so A's Reveal PIN is checked against B and refused.
- If the session no longer resolves (signed out, Profile disabled, outside its access schedule),
  or the later field is no longer authorized, the operation stops before that field with no
  further side effect and answers the error, as any failed mutation field does today.
- Admin API tokens carry no session and their authority cannot change during a request; their
  scope is not re-resolved. Single-field mutations and query operations are unchanged.

## Not in this change

- Rejecting mixed batches outright. Refreshing keeps legitimate sequences (for example `reveal`
  followed by a Reveal-gated field) working under the authority they actually hold.
- Rolling back earlier fields of the operation when a later one is refused. Mutation fields
  already commit one at a time; an earlier authorized field keeps its effect.
