# Provider-supplied sort title for library browse

Ticket: CORE-1768 (`queue:core:main`). Depends on CORE-1685 (letter rail) and builds on the sort
title CORE-1715 added.

## Why

Board C of the list controls asks the A-Z rail to ignore a leading article. The rail and the grid
must key on the same value, or a jump lands in the wrong place. CORE-1715 added an optional sort
title that an admin can set, and browse and the rail already sort and bucket by it. A metadata
provider still cannot supply one, so every item without an admin edit sorts on its raw title.

## What Changes

- Identification reads a `sortTitle` field from a provider's lookup result and writes it as the
  item's sort title at identification rank. Field locks still apply: a locked admin value is kept.
- If a later identification or refresh returns no sort title, the sort title the provider wrote
  before is retracted, so a re-match does not keep the previous candidate's key. A sort title an
  admin entered is never retracted, whether locked or unlocked.
- Browse and its letters keep keying on the sort title when the item has one and on the title
  otherwise. A store test checks this order and these buckets.
- Core adds no article list or other language rule. A plugin (or an admin) decides the key.
- Nothing new on the wire: the editable `sortTitle` in `editMediaMetadata` already exists.

## Follow-up outside core

Plugin SDK v0.3.5 accepts only `title`, `summary` and `year` as bare FieldMap keys, so no bundle
can map `sortTitle` yet. The SDK has to add it to its built-in Media fields, ship a release, and
core has to repin before plugins can supply the value. Core reads the key as soon as a result
carries it.
