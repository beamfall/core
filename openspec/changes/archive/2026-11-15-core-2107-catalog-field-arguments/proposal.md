# Catalog the arguments of every GraphQL field

Ticket: CORE-2107 (`queue:core:main`). Follows CORE-2096 (`agentCatalog`).

## Why

`agentCatalog` reported arguments only for the fields in the wire nested-schema argument map and
four hand-written hints, so most fields came back with `argumentsKnown=false`. An agent then had to
guess argument names or read the source. The wire map is also incomplete for some fields it does
cover (for example `entityRelations` takes `limit` and `offset`).

## What Changes

- A checked-in argument registry (`internal/graph/graph_arguments.go`) lists, for every top-level
  field in the dispatcher registry, the argument names, kinds and whether each is optional. Fields
  that take no arguments have an empty entry. Entries for fields in the wire argument map start
  from that map and add the arguments it omits.
- `agentCatalog` reads the registry, so every field reports `argumentsKnown=true`. The hand-written
  hints are removed.
- A conformance test parses the package's resolvers, follows each field's dispatch path and fails
  when a resolver reads a request argument the registry does not list for that field, when the
  registry and the dispatcher name different fields, or when the registry drops a wire-map
  argument. Reads through a computed key are allowed only from a listed set of call sites with
  their names.
- `docs/api/agents.md` drops the caveat that argument metadata is incomplete.

## Not in this change

- The dispatcher still passes request variables to every field, so an unlisted argument is not
  rejected at runtime: variables are shared across the fields of one operation and filtering them
  would break existing clients. The guarantee is the conformance test.
- The wire nested-schema argument map and its goldens are unchanged; no contract version moves.
