import { displayString } from "@/lib/display";
import type { DisplayString, SourceItem } from "@/lib/graphql/types";
import { artworkSlotURL } from "./artwork";
import type { LibraryItemsGrouping } from "./libraryBrowse";
import type { LibraryEntityKind } from "./libraryViews";

// The Categories pivot's plan (CORE-2505). A grouping with few values (genres)
// is featured as artwork tiles; a long-tail one (studios, networks) shows its
// most used values and a See all list, A-Z.

/** A grouping with at most this many values is featured as artwork tiles. */
export const FEATURED_MAX = 40;
/** How many of a long-tail grouping's most used values the overview shows. */
export const TOP_COUNT = 12;
/** A long-tail value needs this many items to appear in the overview. */
export const TOP_MIN_COUNT = 2;

export interface CategoryValue {
  value: string;
  /** The items that carry the value, when the server reports counts. */
  count?: number;
  /** The plugin's (plural) name for the unit `count` counts, when it is a container. */
  label?: DisplayString;
  /** The plugin's singular name for that unit, for a count of one (CORE-2885). */
  labelOne?: DisplayString;
  /** The Media count behind a container count. */
  items?: number;
}

export interface CategoryPlan {
  kind: string;
  grouping: LibraryItemsGrouping;
  values: CategoryValue[];
  featured: boolean;
  /** A long-tail grouping's most used values; empty without counts. */
  top: CategoryValue[];
}

/** A display string's non-empty entries, keyed by locale, in a comparable form. */
function displayKey(value: DisplayString): string {
  const entries = typeof value === "string" ? [["", value]] : Object.entries(value);
  return JSON.stringify(entries.filter(([, text]) => text).sort(([x], [y]) => (x < y ? -1 : 1)));
}

/** The browse kind whose label matches a category browse label, if any. */
export function matchingBrowseKind(
  label: DisplayString | undefined,
  kinds: readonly LibraryEntityKind[],
): LibraryEntityKind | undefined {
  if (!label) return undefined;
  const key = displayKey(label);
  if (key === "[]") return undefined;
  return kinds.find((entry) => entry.label && displayKey(entry.label) === key);
}

/**
 * The singular name of the container kind a browse label names. The server
 * labels a container count with the kind's browse Section title, the same
 * title `libraryEntities` reports as that kind's `label` beside its display
 * name, so the label finds the kind (CORE-2885).
 */
export function singularLabel(
  label: DisplayString,
  kinds: readonly LibraryEntityKind[],
): DisplayString | undefined {
  const kind = matchingBrowseKind(label, kinds);
  return kind && displayString(kind.kindDisplayName) ? kind.kindDisplayName : undefined;
}

/** Pairs each value with its count; counts that do not line up are dropped. */
export function categoryValues(
  grouping: LibraryItemsGrouping,
  kinds: readonly LibraryEntityKind[] = [],
): CategoryValue[] {
  const counts = grouping.counts?.length === grouping.values.length ? grouping.counts : undefined;
  const browse =
    grouping.browseCounts?.length === grouping.values.length &&
    grouping.browseLabels?.length === grouping.values.length;
  return grouping.values.map((value, i) => {
    const label = browse ? grouping.browseLabels?.[i] : undefined;
    if (label && Object.values(label).some(Boolean)) {
      return {
        value,
        count: grouping.browseCounts?.[i],
        label,
        labelOne: singularLabel(label, kinds),
        items: counts?.[i],
      };
    }
    return counts ? { value, count: counts[i] } : { value };
  });
}

/** Featured groupings lead, each kind keeping the server's order; empty kinds are left out. */
export function planCategories(
  groupings: LibraryItemsGrouping[],
  kinds: readonly LibraryEntityKind[] = [],
): CategoryPlan[] {
  const plans = groupings
    .filter((grouping) => grouping.values.length > 0)
    .map((grouping): CategoryPlan => {
      const values = categoryValues(grouping, kinds);
      const featured = values.length <= FEATURED_MAX;
      return {
        kind: grouping.kind,
        grouping,
        values,
        featured,
        top: featured ? [] : topValues(values),
      };
    });
  return [...plans.filter((plan) => plan.featured), ...plans.filter((plan) => !plan.featured)];
}

function topValues(values: CategoryValue[]): CategoryValue[] {
  return values
    .filter((entry) => (entry.count ?? 0) >= TOP_MIN_COUNT)
    .sort((a, b) => (b.count ?? 0) - (a.count ?? 0) || compareValues(a.value, b.value))
    .slice(0, TOP_COUNT);
}

/** Lower case with accents removed, so "Pathé" matches "pathe". */
export function foldForSearch(text: string): string {
  return text.normalize("NFD").replace(/\p{M}/gu, "").toLocaleLowerCase();
}

function compareValues(a: string, b: string): number {
  return a.localeCompare(b, undefined, { sensitivity: "base", numeric: true });
}

/** The A-Z initial a value files under: a letter, or "#" for a digit or symbol. */
export function categoryInitial(value: string): string {
  const first = foldForSearch(value.trim()).charAt(0).toLocaleUpperCase();
  return first >= "A" && first <= "Z" ? first : "#";
}

export interface CategoryLetterGroup {
  letter: string;
  values: CategoryValue[];
}

/** The values that match the query, A-Z under their initials, "#" first. */
export function groupByInitial(values: CategoryValue[], query = ""): CategoryLetterGroup[] {
  const needle = foldForSearch(query.trim());
  const groups = new Map<string, CategoryValue[]>();
  for (const entry of [...values].sort((a, b) => compareValues(a.value, b.value))) {
    if (needle && !foldForSearch(entry.value).includes(needle)) continue;
    const letter = categoryInitial(entry.value);
    const group = groups.get(letter);
    if (group) group.push(entry);
    else groups.set(letter, [entry]);
  }
  return [...groups.entries()]
    .sort(([a], [b]) => (a === "#" ? -1 : b === "#" ? 1 : a.localeCompare(b)))
    .map(([letter, entries]) => ({ letter, values: entries }));
}

/**
 * The first wide artwork among items not already shown on another tile, which
 * it then marks as shown: neither the picture nor its item repeats.
 */
export function takeCategoryArt(items: SourceItem[], used: Set<string>): string {
  for (const slot of ["backdrop", "thumb", "poster"]) {
    for (const item of items) {
      const url = artworkSlotURL(item, slot);
      if (!url || used.has(url) || used.has(`item:${item.id}`)) continue;
      used.add(url);
      used.add(`item:${item.id}`);
      return url;
    }
  }
  return "";
}

/** How many covers a card's mosaic shows: one, two side by side, or four. */
export const MOSAIC_MAX = 4;

/**
 * The distinct container covers among a value's items, for the mosaic a card
 * without a backdrop shows: four when there are four, else two, else one.
 */
export function categoryCovers(items: SourceItem[]): string[] {
  const covers: string[] = [];
  for (const item of items) {
    const url = item.container?.artwork?.poster ?? "";
    if (url && !covers.includes(url)) covers.push(url);
    if (covers.length === MOSAIC_MAX) break;
  }
  return mosaicCovers(covers);
}

/** Trims covers to a mosaic shape: four, two or one. */
export function mosaicCovers(covers: string[]): string[] {
  return covers.slice(0, covers.length >= MOSAIC_MAX ? MOSAIC_MAX : Math.min(covers.length, 2));
}

/** How many palette tones a card without artwork is tinted from (library-categories.css). */
export const CATEGORY_TONES = 7;

/** The palette tone for a value without artwork: the same for the same name. */
export function categoryTone(value: string): number {
  let hash = 0;
  for (const char of foldForSearch(value.trim())) {
    hash = (hash * 31 + (char.codePointAt(0) ?? 0)) >>> 0;
  }
  return hash % CATEGORY_TONES;
}

/**
 * Runs the reads at most `limit` at a time, in order, reporting each as it
 * settles. A failed read reports null; an aborted run stops starting reads.
 */
export async function runBounded<T>(
  reads: (() => Promise<T>)[],
  limit: number,
  onSettled: (index: number, value: T | null) => void,
  signal?: AbortSignal,
): Promise<void> {
  let next = 0;
  const worker = async () => {
    while (next < reads.length && !signal?.aborted) {
      const index = next++;
      let value: T | null = null;
      try {
        const read = reads[index];
        value = await read();
      } catch {
        value = null;
      }
      if (signal?.aborted) return;
      onSettled(index, value);
    }
  };
  await Promise.all(Array.from({ length: Math.min(limit, reads.length) }, worker));
}
