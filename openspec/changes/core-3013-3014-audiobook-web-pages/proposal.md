# Audiobook book, series and person pages; manual Next in series (CORE-3014, CORE-3013)

Tickets: CORE-3014 and CORE-3013 (`queue:core:main`).

## Why
Audiobooks hydrate authors, narrators, series with a sequence and genres (CORE-3010), but the
web pages treat a book like any other Media: no series position, no time left, no chapters, a
person page that does not split what someone wrote from what they narrated, and a series page
whose Play queues the next book, so finishing one book starts the next. ADR-0055 §2.4 forbids
auto-play between books.

## What changes
- Entity browse rules can filter an outgoing relation by one value of a relation attribute. The
  audiobook Person browse (the home "Authors" grid) lists only people credited as author.
- The web book page shows the series position from the `contains-book` sequence, a Resume
  button with time left, and the chapter list with the current chapter marked.
- The web person page groups a person's books by credit role (Wrote, Narrated, Translated).
- The web series page lists books in sequence with per-book progress, and its Play opens one
  book without a queue.
- Next in series is manual: the book page and the player's finish state offer the next book in
  sequence only once the book is finished, never queued and never auto-played.
- The audiobook plugin home gains a "Next in Series" shelf backed by the `next-up` Source.

## Impact
Web app, entity browse rules, the audiobook hook and the audiobook plugin pin. No wire or
schema change.
