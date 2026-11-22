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

/** An image slide's shape, read from its decoded pixels (the wire carries no dimensions). */
export type SlideShape = "portrait" | "landscape";

/** One stage's worth of slides: one, or two portraits side by side. `start` indexes `slides`. */
export interface SlideGroup {
  start: number;
  slides: Slide[];
}

/**
 * Splits slides into what each stage shows, in order. On a landscape screen a
 * portrait image pairs with the next slide when that is a portrait image too;
 * a lone portrait, a clip and a slide of unknown shape show alone. The last
 * slide never pairs with the first. Layout only: this decides what shares the
 * stage, never how it is drawn.
 */
export function slideGroups(
  slides: readonly Slide[],
  shapeOf: (slide: Slide) => SlideShape | undefined,
  pairPortraits: boolean,
): SlideGroup[] {
  const portrait = (slide: Slide | undefined) =>
    !!slide && !slide.clip && shapeOf(slide) === "portrait";
  const groups: SlideGroup[] = [];
  for (let start = 0; start < slides.length; ) {
    const size = pairPortraits && portrait(slides[start]) && portrait(slides[start + 1]) ? 2 : 1;
    groups.push({ start, slides: slides.slice(start, start + size) });
    start += size;
  }
  return groups;
}

/**
 * Whether the group starting at `start` is known: the slide's shape is read
 * and, when it is a portrait that could pair, so are the next one's and those
 * of the portraits running back to it (pairing counts from the queue's start,
 * so an unread one before it could shift the pair). A stage waits for this so
 * a pair never lands as a lone portrait first.
 */
export function slideGroupSettled(
  slides: readonly Slide[],
  start: number,
  shapeOf: (slide: Slide) => SlideShape | undefined,
  pairPortraits: boolean,
): boolean {
  const slide = slides[start];
  if (!pairPortraits || !slide || slide.clip) return true;
  const shape = shapeOf(slide);
  if (shape !== "portrait") return shape !== undefined;
  if (unreadPortraitBefore(slides, start, shapeOf) !== null) return false;
  const next = slides[start + 1];
  return !next || next.clip || shapeOf(next) !== undefined;
}

/**
 * The nearest slide before `start` whose shape is still unread, walking back
 * through portraits; null once a landscape, a clip or the queue's start ends
 * the run.
 */
export function unreadPortraitBefore(
  slides: readonly Slide[],
  start: number,
  shapeOf: (slide: Slide) => SlideShape | undefined,
): number | null {
  for (let at = start - 1; at >= 0; at -= 1) {
    const slide = slides[at];
    if (!slide || slide.clip) return null;
    const shape = shapeOf(slide);
    if (shape === undefined) return at;
    if (shape !== "portrait") return null;
  }
  return null;
}

/** The Dark Room window's defaults, as `HH:MM` local times. */
export const DEFAULT_DARK_ROOM_START = "22:00";
export const DEFAULT_DARK_ROOM_END = "07:00";

/** Minutes after midnight for an `HH:MM` time, or null when it is not one. */
export function clockMinutes(value: unknown): number | null {
  if (typeof value !== "string") return null;
  const match = /^([01]\d|2[0-3]):([0-5]\d)$/.exec(value);
  return match ? Number(match[1]) * 60 + Number(match[2]) : null;
}

/**
 * Whether the local time `now` falls in the Dark Room window from `start` up to
 * `end` (`HH:MM`). An end before the start crosses midnight; equal or
 * unreadable times make an empty window.
 */
export function inDarkRoomWindow(now: Date, start: string, end: string): boolean {
  const from = clockMinutes(start);
  const until = clockMinutes(end);
  if (from === null || until === null || from === until) return false;
  const minute = now.getHours() * 60 + now.getMinutes();
  return from < until ? minute >= from && minute < until : minute >= from || minute < until;
}

/**
 * The first moment after `from` at which the Dark Room window opens or closes,
 * or null when the window is empty. A manual dim lasts until then.
 */
export function nextDarkRoomBoundary(from: Date, start: string, end: string): Date | null {
  const opens = clockMinutes(start);
  const closes = clockMinutes(end);
  if (opens === null || closes === null || opens === closes) return null;
  let next: Date | null = null;
  for (const minutes of [opens, closes]) {
    const at = new Date(from);
    at.setHours(Math.floor(minutes / 60), minutes % 60, 0, 0);
    if (at.getTime() <= from.getTime()) at.setDate(at.getDate() + 1);
    if (!next || at.getTime() < next.getTime()) next = at;
  }
  return next;
}
