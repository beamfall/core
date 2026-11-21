import type { RelatedEntityDirection, SourceItem } from "@/lib/graphql/types";
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
 * The child container a container with no Media of its own plays next: the
 * first related child group whose archetype is a container, then its first item
 * with children left to finish, else (all finished) its first. Parent groups
 * never count, so in a tree three or more containers deep a parent sorting first
 * cannot stand in for a child (CORE-2036). Null when there is no such group.
 * Only read when the Entity has no direct Media.
 */
export function nextUpChildContainer(
  groups:
    | readonly {
        archetype?: string;
        direction?: RelatedEntityDirection;
        items: readonly SourceItem[];
      }[]
    | undefined,
): SourceItem | null {
  const group = groups?.find(
    (g) => g.direction === "child" && g.archetype === "container" && g.items.length > 0,
  );
  if (!group) return null;
  const items = group.items.filter((item) => !item.locked);
  return items.find(hasChildrenLeft) ?? items[0] ?? null;
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
