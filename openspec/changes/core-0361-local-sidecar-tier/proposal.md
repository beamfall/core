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
- A hydration plan may carry a local part: built-in title, sort title, summary and year, declared
  Media fields and Media relations. The seam validates it against the bundle like the rest of the
  plan, releases the Media's previous local fields to the scan tier and writes the new ones at the
  local tier, replacing the Media's rows for the relation names it carries.
- A mapper may name a declared text Media field holding a signature of the sidecars it reads. The
  scan asks the hydrator whether the signature changed for an unchanged file and re-hydrates it
  when it did. The seam records the signature at the scan tier after the other writes.
- When a Media's sidecars are gone, the seam releases its local fields to the scan tier: the values
  stay and become unlocked, so a later identification can replace them.
- A field-less Entity in a plan no longer erases the fields an earlier write stored, and an Entity
  with fields merges them over the stored ones instead of replacing the whole set.

## Non-goals

- No wire change, no migration and no SDK change. Entity fields and relation rows carry no source
  tier, so an identification can still rewrite container fields and add relation rows.
