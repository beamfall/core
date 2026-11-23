# Episode cards on Home show their show

Ticket: CORE-3320 (`queue:core:main`).

## Why

On the web Home, a Continue Watching episode draws a frame from the episode instead of its show's
poster. In Recently Added, a show with one new episode is not folded (folding needed two items), so
its card is the episode: it draws the episode's still and opens the episode instead of the show's
details page.

## What Changes

- Recently Added folds a lone new video into its container card ("1 new"), with the container's
  poster, opening the container. A lone non-video item, such as one new album track, stays as it is
  (CORE-1490 relies on it).
- Continue Watching draws each item with its container's poster when the container has one. The
  card still opens and resumes the item.

## Non-goals

- Native clients; they get their own tickets if they show the same thing.
- Changing the card's thumb, title or subtitle.
