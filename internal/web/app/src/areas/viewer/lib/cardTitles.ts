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

/** Whether the card has audio Entity credits or a year to feature under its title. */
export function showsAudioEntityDetails(item: SourceItem): boolean {
  return (
    item.kind === "entity" &&
    item.mediaClass === "audio" &&
    !item.locked &&
    ((item.year ?? 0) > 0 || item.byline?.some(Boolean) === true)
  );
}

/**
 * The line under a card's title: the plugin's subtitle first, which tells
 * siblings apart (CORE-1226), then its year when the title repeats in the
 * list, then who made it (an audio card's byline, CORE-2362), then its
 * summary, never repeating a part. An audio card names who made it first,
 * ahead of its album. A card headed by its container leads with
 * the item's own name instead (cardHeading). Audio Entity cards prefer their
 * credits and known year when available.
 */
export function cardSubline(
  item: SourceItem,
  showYear: boolean,
  locale = "en",
  lead = displayString(item.subtitle, locale),
): string {
  const byline = cardByline(item);
  const year = item.year && item.year > 0 ? String(item.year) : "";
  if (showsAudioEntityDetails(item)) {
    return [byline, year].filter(Boolean).join(" · ");
  }
  // A Media card prints its runtime on a line of its own, so a summary that
  // only restates it ("45 sec" over "0:45") is left out.
  const summary = restatesRuntime(item) ? "" : item.summary;
  // A listener reads who made a track before its album, so an audio card
  // names its artist first and a long album title cannot crowd it out.
  const parts =
    item.mediaClass === "audio"
      ? [byline, lead, showYear ? year : "", summary]
      : [lead, showYear ? year : "", byline, summary];
  return [...new Set(parts.filter(Boolean))].join(" · ");
}

// Whether a Media item's summary is the server's running-time line
// (mediaDurationSummary: seconds under a minute, else whole minutes rounded up).
function restatesRuntime(item: SourceItem): boolean {
  const seconds = item.durationSeconds ?? 0;
  if (item.kind !== "media" || seconds <= 0 || !item.summary) return false;
  const runtime = seconds < 60 ? `${seconds} sec` : `${Math.ceil(seconds / 60)} min`;
  return item.summary === runtime;
}

/**
 * What a card is headed by and the line that leads under it (CORE-2524). An
 * item a container holds (an episode in its show) is headed by the
 * container's title, and leads with its own subtitle, else its own title, so
 * the card reads as the container plus one compact line instead of repeating
 * the item's code in both. Inside the container's own page the container goes
 * without saying, so nameContainer is false there and the card keeps its own
 * title.
 *
 * Audio is the exception (CORE-2824): a listener scans for the song, so an
 * audio item keeps its own title as the heading and leads with its container
 * (the album), never with a subtitle such as "Track 1" that would push the
 * album out. An audio episode's title is meaningful too, so it reads the same
 * way under its show; codes like S01E01 belong to video episodes, which keep
 * the container heading.
 */
export function cardHeading(
  item: SourceItem,
  nameContainer: boolean,
  locale = "en",
): { heading: string; lead: string } {
  const subtitle = displayString(item.subtitle, locale);
  const container = item.container?.title.trim();
  if (nameContainer && container && item.mediaClass === "audio") {
    return { heading: item.title, lead: container };
  }
  if (nameContainer && container) {
    return { heading: container, lead: subtitle || item.title };
  }
  return { heading: item.title, lead: subtitle };
}
