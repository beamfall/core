# Desktop Viewer Home shelves and artwork

Ticket CORE-1154.

## Why

The Core-hosted Viewer combines new items from every library in one shelf. It
has no Home hero, and landscape art is clipped in portrait card frames.

## What changes

- Keep the manifest's Continue Watching shelf and show recent additions under
  their library names. Fetch each shelf through its own library-scoped Home
  source. Until CORE-1496 supplies scoped Home visibility, use an item in
  common with the aggregate Home feed as evidence that a library can appear.
  Omit libraries whose visibility cannot be confirmed within that window.
- Use the existing hero for a recent, playable item with artwork. Collapse it
  when no eligible image loads. Consider artwork from both the aggregate feed
  and the visible library shelves.
- Fit Home card frames to loaded artwork and keep the existing fallback frame
  for missing artwork.
