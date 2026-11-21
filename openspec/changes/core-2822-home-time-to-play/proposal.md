# Home time-to-play: Up Next, folded Recently Added, track headings

Tickets: CORE-2822, CORE-2823, CORE-2824 (`queue:core:main`). Follow-ups: CORE-2840 (server-side
grouped Recently Added), CORE-2841 (entity-detail fold vs See All).

## Why

A time-to-play audit of the browse pages found three P1 gaps. After finishing an episode or a track,
the next one appeared nowhere on Home. Recently Added folded its 20-item window into a few container
cards, hiding most new films, and its "N new" counted items already watched. Track cards were headed
by their album, so a listener scanning for a song read the same album name on every card.

## What Changes

- **Manifest**: the Home layout gains a `next-up` shelf right after Continue Watching, titled from
  the l10n catalog.
- **Store**: the next-up source reads watched state from the Profile's full progress (the 90-day
  window only picks and orders engaged containers), crosses from a finished container into its next
  ordered sibling container, yields nothing for a group with a resumable child (Continue Watching
  shows it), and counts only Entity kinds of the container archetype as containers, so credit
  billing and scraped-list ordering never seed it.
- **Web Viewer**: Recently Added reads 100 items, folds them by container and shows 20 cards; a
  folded card counts the items not yet finished and names no count when all are finished. Audio
  cards are headed by their own title and lead with their container; video items keep the container
  heading.

## Impact

Home manifest golden and corpus, `internal/store` next-up, `EntityKindPolicy` gains a Container
posture lowered from the container archetype, web viewer card and shelf code.
