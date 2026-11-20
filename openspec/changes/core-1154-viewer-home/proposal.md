# Desktop Viewer Home shelves and artwork

Ticket CORE-1154.

## Why

The Core-hosted Viewer combines new items from every library in one shelf. It
has no Home hero, and landscape art is clipped in portrait card frames.

## What changes

- Keep the manifest's Continue Watching shelf and show recent additions under
  their library names. Confirm each item's Home visibility with the aggregate
  Home source until the server honors the Home flag on library-scoped feeds.
- Use the existing hero for a recent, playable item with artwork. Collapse it
  when no eligible image loads.
- Fit Home card frames to loaded artwork and keep the existing fallback frame
  for missing artwork.
