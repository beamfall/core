import type { SourceItem } from "@/lib/graphql/types";

// The art frame a card or thumbnail draws its artwork in (CORE-2362). Listening
// artwork is square, so an audio Media item, or an Entity whose Media are all
// audio, frames it 1:1; everything else keeps the 2:3 poster frame. The class
// comes from the core media class the server reports, never a content kind, and
// every place a card renders (Home rows, search, Library grids, detail pages)
// asks this one rule.

const AUDIO_CLASS = "audio";

/** Whether an item's artwork is framed square. */
export function hasSquareArt(item: Pick<SourceItem, "mediaClass"> | undefined): boolean {
  return item?.mediaClass === AUDIO_CLASS;
}

/** The art frame's class names for an item: "bf-poster", plus the square modifier for audio. */
export function artFrameClass(item: Pick<SourceItem, "mediaClass"> | undefined): string {
  return hasSquareArt(item) ? "bf-poster bf-poster-square" : "bf-poster";
}

/** The names under a card's title: who made it, for a listening item that says. */
export function cardByline(item: Pick<SourceItem, "byline">): string {
  return (item.byline ?? []).filter(Boolean).join(", ");
}
