import type { SourceItem } from "@/lib/graphql/types";
import { cardByline } from "./cardArt";

function titleKey(title: string): string {
  return title.trim().toLocaleLowerCase();
}

/**
 * The titles that appear more than once in a list of cards (CORE-2320): two
 * remakes, or a film and its namesake, otherwise read as the same card. Those
 * cards show their year so the viewer can tell them apart.
 */
export function repeatedTitles(items: readonly SourceItem[]): ReadonlySet<string> {
  const seen = new Set<string>();
  const repeated = new Set<string>();
  for (const item of items) {
    const key = titleKey(item.title);
    if (!key) continue;
    if (seen.has(key)) repeated.add(key);
    else seen.add(key);
  }
  return repeated;
}

/** Whether a card's title repeats in its list and the card has a year to tell it apart. */
export function showsYear(item: SourceItem, repeated: ReadonlySet<string>): boolean {
  return !!item.year && item.year > 0 && repeated.has(titleKey(item.title));
}

/**
 * The line under a card's title: its year first when the title repeats in the
 * list, then who made it (an audio card's byline, CORE-2362), then its
 * summary, never repeating a part.
 */
export function cardSubline(item: SourceItem, showYear: boolean): string {
  const parts = [showYear && item.year ? String(item.year) : "", cardByline(item), item.summary];
  return [...new Set(parts.filter(Boolean))].join(" · ");
}
