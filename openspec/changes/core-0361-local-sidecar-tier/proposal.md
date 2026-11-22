# Sidecar metadata lands at a locked local tier

Ticket: CORE-0361 (`queue:core:main`).

## Why

A plugin can read metadata an operator keeps in sidecar files next to the media (written by
another media manager). Those values are the operator's own curation, so a later provider
identification must not overwrite them, and editing the sidecar must refresh them even when the
media file itself is unchanged. Today the hydration seam writes only at the scan tier, which any
identification outranks, and a rescan revisits an unchanged file only for a backfill.

## What Changes

- A new `local` mutation source ranks below a manual edit and above an operator identification:
  manual > local > operator > scan > hydrated > machine-derived. Fields it wrote read as locked.
- A mapper may read sidecars (`HydrationSidecarMapper`): it names a declared text Media field for
  the sidecar signature, signs a path's sidecars without reading them, and maps a path's sidecars
  to a local part: built-in title, sort title, summary and year, declared Media fields, Media
  relations, the relations the sidecar format owns, the signature of the bytes it parsed, or an
  unreadable flag.
- The seam applies a local part in one store transaction on the Media's own catalog (an unsealed
  encrypted library included): it releases the Media's previous local fields to the scan tier,
  writes the new ones at the local tier, reconciles the Media's local-writer relation rows under
  each owned relation (a relation now empty is emptied) and records the plan's signature. A
  failure leaves the previous state and the old signature, so the next scan retries.
- Relation rows a sidecar wrote carry the writer `local`. Reconciling touches only those rows:
  an operator's row and another writer's row stay, and a scraper's upsert does not take a local
  row over.
- An unreadable sidecar is not a removed one: the seam releases nothing and records nothing, and
  the next scan retries. A removed sidecar (none on any of the Media's files) releases the local
  fields and relation rows, keeping their values, in one transaction that records the empty
  signature.
- The Media's sidecar is read beside the first of its files, in path order, that has one, so a
  sidecar beside a secondary version hydrates the Media. The scan checks sidecars for every file
  it visits, an attached version included, and a sidecar refresh needs no media probe.
- The signature field is the seam's: the seam records it whatever lock the field carries.
- A field-less Entity in a plan no longer erases the fields an earlier write stored.

## Cut from this change

- Container sidecars (a show's or a container's own sidecar) are not imported. Entity fields carry
  no source tier, so a later identification would overwrite them and a manual edit could not
  outrank them. CORE-3165 carries that work and its reproductions.
- An encrypted library's catalog holds no sort title, so a sidecar's sort title is not written
  there (CORE-1868). Its other values are.

## Non-goals

- No wire change, no migration and no SDK change.
