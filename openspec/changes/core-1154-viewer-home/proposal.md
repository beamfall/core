# Desktop Viewer Home shelves and artwork

Ticket CORE-1154.

## Why

The Core-hosted Viewer combines new items from every library in one shelf. It
has no Home hero, and landscape art is clipped in portrait card frames.

## What changes

- Keep the manifest's Continue Watching shelf and show recent additions under
  their library names. Fetch each shelf through its own library-scoped Home
  source. The server applies the Home visibility preference to scoped and
  aggregate Home feeds.
- Use the existing hero for a recent, playable item with artwork. Collapse it
  when no eligible image loads.
- Fit Home card frames to loaded artwork and keep the existing fallback frame
  for missing artwork.
