import type { SourceItem } from "@/lib/graphql/types";
import { artworkSlotURL } from "./artwork";
import { resumeProgress } from "./mediaDetail";

// A container Entity's children (CORE-1786): the ordered Media list
// mediaForEntity returns, grouped by the plugin's kind in first-seen order and
// read for the one child to play next. Nothing here names a plugin kind.

export interface ChildGroup {
  kind: string;
  items: SourceItem[];
}

/** Groups children by kind, keeping the server's order within and across groups. */
export function groupChildrenByKind(items: readonly SourceItem[]): ChildGroup[] {
  const groups: ChildGroup[] = [];
  for (const item of items) {
    const group = groups.find((g) => g.kind === item.kind);
    if (group) group.items.push(item);
    else groups.push({ kind: item.kind, items: [item] });
  }
  return groups;
}

export interface NextUpChild {
  item: SourceItem;
  /** True when the child has a saved position to resume from. */
  resume: boolean;
}

/**
 * The child the container's primary action plays: the first one in progress,
 * else the first not yet finished, else (all finished) the first. Locked and
 * non-playable children are skipped. Null when nothing can play.
 */
export function nextUpChild(items: readonly SourceItem[]): NextUpChild | null {
  const playable = items.filter((item) => item.kind === "media" && !item.locked);
  const inProgress = playable.find((item) => resumeProgress(item));
  if (inProgress) return { item: inProgress, resume: true };
  const unfinished = playable.find((item) => item.engagement?.completed !== true);
  if (unfinished) return { item: unfinished, resume: false };
  return playable.length > 0 ? { item: playable[0], resume: false } : null;
}

/** The child's still: a thumb slot first, then its backdrop, then its poster. */
export function childThumbURL(item: SourceItem): string {
  return (
    artworkSlotURL(item, "thumb") ||
    artworkSlotURL(item, "backdrop") ||
    artworkSlotURL(item, "poster")
  );
}

/**
 * The child containers a container with no Media of its own plays through, in
 * the order to try them: the first related group whose archetype is a
 * container, its unlocked items with children left to finish first, then the
 * rest. Empty when there is no such group. Ids in `skip` (containers already
 * visited) are left out, so a group of only those does not count. Related
 * groups carry no relation direction, so in a tree three or more containers
 * deep a parent group sorting first could stand in for a child (CORE-2036).
 */
export function childContainerCandidates(
  groups: readonly { archetype?: string; items: readonly SourceItem[] }[] | undefined,
  skip?: ReadonlySet<string>,
): SourceItem[] {
  for (const group of groups ?? []) {
    if (group.archetype !== "container") continue;
    const items = skip ? group.items.filter((item) => !skip.has(item.id)) : group.items;
    if (items.length > 0) return playOrder(items);
  }
  return [];
}

/**
 * The child container a container with no Media of its own plays next: the
 * first of childContainerCandidates. Null when there is none.
 */
export function nextUpChildContainer(
  groups: readonly { archetype?: string; items: readonly SourceItem[] }[] | undefined,
): SourceItem | null {
  return childContainerCandidates(groups)[0] ?? null;
}

/**
 * The container cards a rolled-up Media list plays through, in the order to
 * try them (CORE-3058): its unlocked cards with children left first, then the
 * rest. Empty when the list has no container card. The page moves on when one
 * turns out to hold nothing it can play (only locked Media, say).
 */
export function rolledContainerCandidates(items: readonly SourceItem[]): SourceItem[] {
  return playOrder(items.filter((item) => item.kind === "entity"));
}

/** Unlocked containers, those with children left first, else in order. */
function playOrder(items: readonly SourceItem[]): SourceItem[] {
  const unlocked = items.filter((item) => !item.locked);
  return [
    ...unlocked.filter(hasChildrenLeft),
    ...unlocked.filter((item) => !hasChildrenLeft(item)),
  ];
}

/** Whether the next-up child is a finished item played again (nothing is left). */
export function nextUpFinished(next: NextUpChild): boolean {
  return !next.resume && next.item.engagement?.completed === true;
}

/** Whether a rolled-up Media list has a container card with children left. */
export function rolledContainerLeft(items: readonly SourceItem[]): boolean {
  return items.some((item) => item.kind === "entity" && !item.locked && hasChildrenLeft(item));
}

/**
 * Whether a container still has children to finish. The server omits zero
 * counts, so a container with a total but no remaining count is finished; one
 * with no counts at all is unknown and counts as unfinished.
 */
function hasChildrenLeft(item: SourceItem): boolean {
  const engagement = item.engagement;
  if (engagement?.completed === true) return false;
  if (engagement?.remainingCount !== undefined) return engagement.remainingCount > 0;
  return !engagement?.totalCount;
}
