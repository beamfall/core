# Carry the audience score's source label on the client wire (CORE-2415)

## Why

CORE-2398 put a bare 0-10 `audienceScore` on cards and the `media(id)` read. Web names the score's
source from the plugin's declared field label ("TMDB rating") and speaks its vote count; tvOS has
only the number and cannot say where it comes from.

## What changes

- `SourceItem` (and so search cards) and `MediaInfo` gain optional `audienceScoreLabel`, the owning
  Library plugin's declared label for its `community-rating` Media field (an ADR-0034 locale map),
  and optional `audienceScoreVotes`, the item's `community-rating-votes` number field.
- Both are set only when `audienceScore` is. A plugin-less Library, or one that declares no label,
  omits the label; an item without a positive vote count omits the votes.
- Additive client-core-wire minor.

## Impact

`internal/store` (rating columns), `internal/graph` (card and media annotation), the wire
contract artifacts and the core contract registry entry. Clients re-vendor and may render the
label; apple-ui and Apple follow in their own changes.
