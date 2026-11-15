# Media detail lockup: one primary action, viewer-facing fields, parent and Next

Ticket: CORE-1785 (`queue:core:main`).

## Why

The web Media detail page lists every stored field, including bookkeeping a viewer never reads
(core's undeclared index and sequence positions, the SDK class discriminant, hydration flag
markers). Its header is a translucent card inside a blurred wash, which nests a card in a card
and hand-writes a blur effect that belongs to Oscillux. An episode-like item has no way up to the
Entity that holds it, nor to the next item in that Entity's order.

## What Changes

- The page is one stage surface: the item's backdrop slot full-bleed behind a `--bg-root`
  gradient scrim, the poster (`--radius-card`) and copy laid on it directly. The backdrop fades in
  by opacity over `--duration-normal`/`--ease-standard`, with no fade under reduce-motion. No blur.
- One filled Play/Resume (44px, the only accent fill), then ghost secondaries: Start over (when a
  position is known; opens the player with `?t=0`), Next, and the existing Play on control.
- A facts row of chips under the title: year, runtime (when the opening card carried a duration),
  the regulatory rating field, and up to three category-archetype credits.
- The overview is clamped to three lines with a More/Less toggle when it is long.
- The Details list shows only viewer-facing fields: those the plugin declared with a label, other
  than the `class` discriminant, `flag` fields and fields promoted to the facts row. Manage
  (Curation) still lists every field.
- Container-archetype credits link to their Entity page. The nearest container's ordered Media
  list gives Next, and an ordered container related to it that holds at least as many items is
  linked as the grouping above it.
- `media(id)` is read with its existing `artworkSlots` and `credits`, so deep links paint artwork.
- Phone width: one column, a small poster, Play full width.

## Non-goals

- A resolution/HDR chip: `media(id)` carries no stream facts and negotiating playback info on the
  detail page is too heavy. Follow-up.
- A plugin-declared audience flag on fields (plugin SDK `FieldSpec`); the Viewer's rule is generic
  until one exists.
- No GraphQL schema or wire-contract change; only existing fields are selected.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `media-detail-page`: viewer-facing fields only, the lockup and single accent action, facts,
  Start over, container links and Next.
