# Desktop Viewer Home shelves and artwork

Ticket CORE-1154.

## Why

The Core-hosted Viewer combines new items from every library in one shelf. It
has no Home hero, and landscape art is clipped in portrait card frames.

## What changes

- Keep the manifest's Continue Watching shelf and show recent additions under
  their library names. Fetch each shelf through its own library-scoped Home
  source for libraries whose GraphQL `showOnHome` is anything other than
  `false`. Viewer sessions currently lack access to the `libraries` resolver.
  In that case, retain the server-filtered Recently Added shelf while the
  scoped visibility read is pending a server change.
- Use the existing hero for a recent, playable item with artwork. Collapse it
  when no eligible image loads. Consider artwork from the shelves shown on Home.
- Fit Home card frames to loaded artwork and keep the existing fallback frame
  for missing artwork.
