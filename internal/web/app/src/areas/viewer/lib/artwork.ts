import type { SourceItem } from "@/lib/graphql/types";

// Artwork resolution, ported verbatim in behavior from the vanilla app
// (internal/web/static/app.js artworkSlotURL / artworkBucketURL).
//
// SourceItem.artworkSlots is a map keyed exactly poster/backdrop/thumb (ADR-0033
// artwork slots); the poster slot also rides the legacy item.artwork.poster
// field. Unknown slots are silently ignored. Returns "" when the requested slot
// has no artwork — the caller then leaves the frame's accent fallback gradient
// (never requests a missing image).

export function artworkSlotURL(item: SourceItem, slot: string): string {
  if (item.artworkSlots && typeof item.artworkSlots[slot] === "string") {
    return item.artworkSlots[slot];
  }
  if (slot === "poster" && item.artwork && item.artwork.poster) {
    return item.artwork.poster;
  }
  return "";
}

// Appends the derivative bucket query the server keys cached artwork on
// (ADR-0014 artwork derivative buckets): w320 for cards, w1280 for the hero.
export function artworkBucketURL(url: string, bucket: string): string {
  if (!url) {
    return "";
  }
  return `${url}${url.includes("?") ? "&" : "?"}bucket=${bucket}`;
}
