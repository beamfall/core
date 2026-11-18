import { displayString } from "@/lib/display";
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
 * The line under a card's title: the plugin's subtitle first, which tells
 * siblings apart (CORE-1226), then its year when the title repeats in the
 * list, then who made it (an audio card's byline, CORE-2362), then its
 * summary, never repeating a part. A card headed by its container leads with
 * the item's own name instead (cardHeading).
 */
export function cardSubline(
  item: SourceItem,
  showYear: boolean,
  locale = "en",
  lead = displayString(item.subtitle, locale),
): string {
  const parts = [
    lead,
    showYear && item.year ? String(item.year) : "",
    cardByline(item),
    item.summary,
  ];
  return [...new Set(parts.filter(Boolean))].join(" · ");
}

/**
 * What a card is headed by and the line that leads under it (CORE-2524). An
 * item a container holds (an episode in its show) is headed by the
 * container's title, and leads with its own subtitle, else its own title, so
 * the card reads as the container plus one compact line instead of repeating
 * the item's code in both. Inside the container's own page the container goes
 * without saying, so nameContainer is false there and the card keeps its own
 * title.
 */
export function cardHeading(
  item: SourceItem,
  nameContainer: boolean,
  locale = "en",
): { heading: string; lead: string } {
  const subtitle = displayString(item.subtitle, locale);
  const container = item.container?.title.trim();
  if (nameContainer && container) {
    return { heading: container, lead: subtitle || item.title };
  }
  return { heading: item.title, lead: subtitle };
}
