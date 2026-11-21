// The web photo slideshow's data and timing (CORE-2981, photos design §3.1).
// Slides come only from what the server lists for the acting Profile and
// Reveal state; this file narrows that list and never widens it.

import type { ItemTarget, SourceItem } from "@/lib/graphql/types";
import { fetchSourceItems } from "@/lib/graphql/viewer";
import { fetchCollectionItems } from "../../viewer/lib/collections";

/** The Source kinds a slideshow can play besides an ordered Collection. */
export const SLIDESHOW_SOURCE_KINDS = ["all-items", "favorites", "on-this-day"] as const;
export type SlideshowSourceKind = (typeof SLIDESHOW_SOURCE_KINDS)[number];

/** What a slideshow plays: a Collection, or a Source kind over one Library or every Library (""). */
export type SlideshowSource =
  | { kind: "collection"; collectionID: string }
  | { kind: SlideshowSourceKind; libraryID: string };

export const MIN_INTERVAL_SECONDS = 5;
export const MAX_INTERVAL_SECONDS = 60;
export const DEFAULT_INTERVAL_SECONDS = 12;
/** A clip longer than this is a film, not a moment, and is left out. */
export const MAX_CLIP_SECONDS = 120;
/** A bound on how many items one slideshow reads, so a huge Library stays finite. */
export const MAX_SLIDESHOW_ITEMS = 2000;
const SOURCE_PAGE = 200;

/** The interval in whole seconds, held to 5–60; anything unreadable is the default. */
export function clampInterval(seconds: unknown): number {
  const value = typeof seconds === "string" ? Number(seconds) : seconds;
  if (typeof value !== "number" || !Number.isFinite(value)) return DEFAULT_INTERVAL_SECONDS;
  return Math.min(MAX_INTERVAL_SECONDS, Math.max(MIN_INTERVAL_SECONDS, Math.round(value)));
}

/** The Source an address names, or null when it names none. */
export function parseSlideshowSource(search: string | URLSearchParams): SlideshowSource | null {
  const params = typeof search === "string" ? new URLSearchParams(search) : search;
  const collectionID = params.get("collection")?.trim();
  if (collectionID) return { kind: "collection", collectionID };
  const kind = params.get("source");
  if (!kind || !(SLIDESHOW_SOURCE_KINDS as readonly string[]).includes(kind)) return null;
  return { kind: kind as SlideshowSourceKind, libraryID: params.get("library")?.trim() ?? "" };
}

/** The `/slideshow` address for a Source. */
export function slideshowPath(source: SlideshowSource): string {
  const params = new URLSearchParams();
  if (source.kind === "collection") {
    params.set("collection", source.collectionID);
  } else {
    params.set("source", source.kind);
    if (source.libraryID) params.set("library", source.libraryID);
  }
  return `/slideshow?${params.toString()}`;
}

export function sameSource(a: SlideshowSource | null, b: SlideshowSource | null): boolean {
  if (!a || !b) return a === b;
  return slideshowPath(a) === slideshowPath(b);
}

/** One slide: an image, or a short clip that plays muted. */
export interface Slide {
  item: SourceItem;
  clip: boolean;
}

function isMediaItem(item: SourceItem): boolean {
  return item.kind === "media";
}

function isClip(item: SourceItem): boolean {
  if (item.mediaClass !== "video") return false;
  return item.durationSeconds === undefined || item.durationSeconds <= MAX_CLIP_SECONDS;
}

/**
 * The slides a listed page yields, in Source order: images, and short clips
 * only when the list also holds images. Locked items and the Profile's hidden
 * items never become slides.
 */
export function slidesFrom(
  items: readonly SourceItem[],
  isHidden: (target: ItemTarget) => boolean,
): Slide[] {
  const visible = items.filter(
    (item) => isMediaItem(item) && !item.locked && !isHidden({ mediaID: item.id }),
  );
  if (!visible.some((item) => item.mediaClass === "image")) return [];
  const slides: Slide[] = [];
  for (const item of visible) {
    if (item.mediaClass === "image") slides.push({ item, clip: false });
    else if (isClip(item)) slides.push({ item, clip: true });
  }
  return slides;
}

/** Milliseconds until the slide shown at `shownAt` (with `heldMs` already shown) should change. */
export function nextDelay(shownAt: number, now: number, intervalMs: number, heldMs = 0): number {
  return Math.max(0, intervalMs - heldMs - (now - shownAt));
}

/**
 * Reads a Source's items in order, handing each growing list to `onItems` so
 * the first slide can show before the last page arrives.
 */
export async function fetchSlideshowItems(
  source: SlideshowSource,
  onItems: (items: SourceItem[]) => void,
  signal?: AbortSignal,
): Promise<SourceItem[]> {
  if (source.kind === "collection") {
    const items = await fetchCollectionItems(source.collectionID, signal);
    onItems(items);
    return items;
  }
  const items: SourceItem[] = [];
  while (items.length < MAX_SLIDESHOW_ITEMS) {
    const page = await fetchSourceItems(
      {
        kind: source.kind,
        entityKind: "",
        libraryID: source.libraryID,
        entityID: "",
        fieldFilters: null,
        entityFilter: null,
        limit: SOURCE_PAGE,
        offset: items.length,
      },
      signal,
    );
    items.push(...page);
    onItems([...items]);
    if (page.length < SOURCE_PAGE) break;
  }
  return items;
}

/** Whether a listed page holds an image, so a Slideshow action is worth offering. */
export function hasImages(items: readonly SourceItem[] | undefined): boolean {
  return !!items?.some((item) => item.mediaClass === "image" && !item.locked);
}

/** The first date-typed field's value as month and year, else its raw text. */
export function slideDate(
  fields: readonly { type: string; value: string }[],
  locale: string,
): string {
  const field = fields.find((entry) => entry.type === "date" && entry.value.trim());
  if (!field) return "";
  const raw = field.value.trim();
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) return raw;
  try {
    return new Intl.DateTimeFormat(locale, {
      month: "long",
      year: "numeric",
      timeZone: "UTC",
    }).format(parsed);
  } catch {
    return raw;
  }
}
