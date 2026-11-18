import type { MediaCredit, SourceItem } from "@/lib/graphql/types";
import { castGroups } from "./people";

// The Related row of a Media detail page (CORE-2444), ranked on the client
// from existing mediaForEntity reads until a declarable server source kind
// exists. Signals are the item's category-archetype credits (genres and the
// like) and its first billed people; an item qualifies only by sharing a
// category, so a lone shared person does not repeat the "More with" row.

/** How many billed people count toward the ranking. */
export const RELATED_PEOPLE = 3;
/** How many category credits are read; with RELATED_PEOPLE it bounds the reads per page. */
export const RELATED_CATEGORIES = 4;
/** The longest Related row. */
export const RELATED_LIMIT = 20;

export interface RelatedSignals {
  categories: MediaCredit[];
  people: MediaCredit[];
}

/** The credits whose Media the Related row reads. */
export function relatedSignals(credits: readonly MediaCredit[] | null | undefined): RelatedSignals {
  const seen = new Set<string>();
  const categories: MediaCredit[] = [];
  for (const credit of credits ?? []) {
    if (credit.archetype !== "category" || !credit.entityID || seen.has(credit.entityID)) continue;
    seen.add(credit.entityID);
    if (categories.length < RELATED_CATEGORIES) categories.push(credit);
  }
  const people = castGroups(credits)
    .flatMap((group) => group.credits)
    .slice(0, RELATED_PEOPLE);
  return { categories, people };
}

/**
 * Ranks the Media read for each signal: items sharing at least one category,
 * most shared signals first, ties in first-seen order, the current item left
 * out.
 */
export function rankRelated(
  mediaID: string,
  byCategory: readonly (readonly SourceItem[])[],
  byPerson: readonly (readonly SourceItem[])[],
): SourceItem[] {
  const scored = new Map<
    string,
    { item: SourceItem; score: number; order: number; category: boolean }
  >();
  const add = (items: readonly SourceItem[], category: boolean) => {
    const counted = new Set<string>();
    for (const item of items) {
      if (!item?.id || item.id === mediaID || counted.has(item.id)) continue;
      counted.add(item.id);
      const entry = scored.get(item.id);
      if (entry) {
        entry.score += 1;
        entry.category ||= category;
      } else {
        scored.set(item.id, { item, score: 1, order: scored.size, category });
      }
    }
  };
  for (const items of byCategory) add(items, true);
  for (const items of byPerson) add(items, false);
  return [...scored.values()]
    .filter((entry) => entry.category)
    .sort((a, b) => b.score - a.score || a.order - b.order)
    .slice(0, RELATED_LIMIT)
    .map((entry) => entry.item);
}
