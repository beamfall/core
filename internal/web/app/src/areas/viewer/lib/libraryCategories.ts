import type { SourceItem } from "@/lib/graphql/types";
import { artworkSlotURL } from "./artwork";
import type { LibraryItemsGrouping } from "./libraryBrowse";

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
}

export interface CategoryPlan {
  kind: string;
  grouping: LibraryItemsGrouping;
  values: CategoryValue[];
  featured: boolean;
  /** A long-tail grouping's most used values; empty without counts. */
  top: CategoryValue[];
}

/** Pairs each value with its count; counts that do not line up are dropped. */
export function categoryValues(grouping: LibraryItemsGrouping): CategoryValue[] {
  const counts = grouping.counts?.length === grouping.values.length ? grouping.counts : undefined;
  return grouping.values.map((value, i) => (counts ? { value, count: counts[i] } : { value }));
}

/** Featured groupings lead, each kind keeping the server's order; empty kinds are left out. */
export function planCategories(groupings: LibraryItemsGrouping[]): CategoryPlan[] {
  const plans = groupings
    .filter((grouping) => grouping.values.length > 0)
    .map((grouping): CategoryPlan => {
      const values = categoryValues(grouping);
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
        value = await reads[index]();
      } catch {
        value = null;
      }
      if (signal?.aborted) return;
      onSettled(index, value);
    }
  };
  await Promise.all(Array.from({ length: Math.min(limit, reads.length) }, worker));
}
