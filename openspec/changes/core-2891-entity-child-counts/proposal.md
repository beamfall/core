# Say what an Entity card holds, in the plugin's words

Ticket: CORE-2891 (`queue:core:main`).

## Why

On the web music Library's Artists view every card's line reads "47 items". The owner: "items
isn't very useful … maybe some graphics and album count." `libraryEntities` already counts each
listed Entity's child Entities, but it sums every child kind into one `childCount` and names none
of them, so a client cannot say "4 Albums" without hard-coding a domain word, which core must not
do.

## What changes

- `libraryEntities` answers `childKinds`: one entry per child kind of the listed kind (the kinds
  that declare a relation to it, less its parent kinds), each with `kind` and the plugin's
  `kindDisplayName` and `kindPluralDisplayName`, in the plugin's declaration order.
- Each listed item carries `childCounts`: `{kind, count}` for each child kind it has at least one
  visible linked Entity of, in the order of `childKinds`. `childCount` stays their sum.
- The web Entity card line, for an Entity with no parents, names each child count with the
  plugin's label ("4 Albums", "1 Album"), choosing singular or plural with `Intl.PluralRules` for
  the viewer's locale; with no child counts it keeps the Media count.
- Wire goldens regenerated; the lander publishes the new contract version.

## Non-goals

- No track count beside the album count: the plugin SDK declares a singular `MediaLabel` but no
  plural, so "47 tracks" needs an SDK field first.
- No new icons or graphics on the card.
- No change to which Entities are counted or visible.

## Failure modes

- A child kind with no plugin display names: the card falls back to the kind name.
- A plugin that declares no child kinds for the listed kind: `childKinds` and every item's
  `childCounts` are empty, and the card shows the Media count as before.
