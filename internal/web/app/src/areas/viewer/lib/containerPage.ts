import { displayString } from "@/lib/display";
import type {
  EntityFieldInfo,
  EntityInfo,
  RelatedEntityGroup,
  SourceItem,
} from "@/lib/graphql/types";
import { ancestorItems } from "./containerContext";
import { resumeProgress } from "./mediaDetail";

// What a container Entity page reads beyond its hero (CORE-2510, CORE-2512,
// CORE-2522): the ancestor it sits under, its siblings in that ancestor, its
// Media rows' ordinals, the On Deck line, similar containers and the order of
// catalog shelves. Everything comes from archetypes, kinds as opaque strings,
// plugin subtitles and engagement; nothing names a plugin kind.

/** The separator plugin subtitles are joined with (MediaSubtitleSeparator). */
const SUBTITLE_SEPARATOR = " · ";

/** The field type whose value is free text. */
const TEXT_FIELD_TYPE = "text";

/**
 * The length from which a free-text field reads as prose rather than a short
 * fact (a studio, a rating): long enough to need the overview's clamp.
 */
const OVERVIEW_MIN_LENGTH = 120;

/**
 * The container this one sits under: the first related container of another
 * kind holding at least as many items. Only a container with Media of its own
 * has one; without them every larger container would qualify.
 */
export function ancestorOf(
  entity: EntityInfo | null | undefined,
  ownMediaCount: number,
): SourceItem | null {
  if (!entity || entity.archetype !== "container" || ownMediaCount <= 0) return null;
  const containers = (entity.relatedEntities ?? []).filter(
    // A related item's own kind is the generic "entity"; its group names the Kind.
    (group) => group.archetype === "container" && group.kind !== entity.kind,
  );
  return ancestorItems(containers, entity, ownMediaCount)[0] ?? null;
}

/** Container groups without the given item, dropping groups left empty. */
export function withoutItem(
  groups: readonly RelatedEntityGroup[],
  id: string | undefined,
): RelatedEntityGroup[] {
  if (!id) return [...groups];
  return groups
    .map((group) => {
      const items = group.items.filter((item) => item.id !== id);
      if (items.length === group.items.length) return group;

      let start = 0;
      const sections = group.sections
        ?.filter((section) => section.count > 0)
        .map((section) => {
          const end = start + section.count;
          const removed = group.items.slice(start, end).filter((item) => item.id === id).length;
          start = end;
          return { ...section, count: section.count - removed };
        })
        .filter((section) => section.count > 0);
      return { ...group, items, sections };
    })
    .filter((group) => group.items.length > 0);
}

/**
 * A related group's heading: the plugin's plural display name ("Albums"), then
 * its display name, then the opaque kind (CORE-2831).
 */
export function groupHeading(group: RelatedEntityGroup, locale: string): string {
  return (
    displayString(group.kindPluralDisplayName, locale) ||
    displayString(group.kindDisplayName, locale) ||
    group.kind
  );
}

/** One heading's share of a related group: its key, heading, size and cards. */
export interface RelatedSection {
  key: string;
  title: string;
  count: number;
  items: SourceItem[];
}

/**
 * A related group as the headed runs it lists (CORE-2769): one per server
 * section (a plugin-declared field value such as a release type), titled by
 * the value's display name, then the value; items without a value head with
 * the group's own heading. A group without sections, or with sections that do
 * not cover its items exactly, is one run under the group heading.
 */
export function groupSections(group: RelatedEntityGroup, locale: string): RelatedSection[] {
  const heading = groupHeading(group, locale);
  const whole = [
    { key: group.kind, title: heading, count: group.items.length, items: group.items },
  ];
  const sections = group.sections ?? [];
  const covered = sections.reduce((sum, section) => sum + Math.max(0, section.count), 0);
  if (sections.length === 0 || covered !== group.items.length) return whole;
  const out: RelatedSection[] = [];
  const keys = new Set<string>();
  let start = 0;
  for (const section of sections) {
    if (section.count <= 0) continue;
    const key = `${group.kind}:${section.value}`;
    out.push({
      key: keys.has(key) ? `${key}:${group.items[start].id}` : key,
      title: section.value ? displayString(section.displayName, locale) || section.value : heading,
      count: section.count,
      items: group.items.slice(start, start + section.count),
    });
    keys.add(key);
    start += section.count;
  }
  return out;
}

/** The ancestor's related Entities of the page's kind, in the server's order, each once. */
export function siblingContainers(
  ancestor: EntityInfo | null | undefined,
  entity: { id: string; kind: string } | null | undefined,
): { group: RelatedEntityGroup; items: SourceItem[] } | null {
  if (!ancestor || !entity) return null;
  const group = (ancestor.relatedEntities ?? []).find(
    (candidate) =>
      candidate.kind === entity.kind && candidate.items.some((item) => item.id === entity.id),
  );
  if (!group) return null;
  const seen = new Set<string>();
  const items = group.items.filter((item) => {
    if (seen.has(item.id)) return false;
    seen.add(item.id);
    return true;
  });
  return items.length > 1 ? { group, items } : null;
}

function subtitleParts(item: SourceItem, locale: string): string[] {
  return displayString(item.subtitle, locale)
    .split(SUBTITLE_SEPARATOR)
    .map((part) => part.trim())
    .filter(Boolean);
}

/**
 * Each item's ordinal for a row: its subtitle parts without its own title and,
 * when two or more siblings carry subtitles, without the parts every one of
 * them shares. Items with nothing left map to "".
 */
export function rowOrdinals(items: readonly SourceItem[], locale: string): Map<string, string> {
  const parts = items.map((item) => subtitleParts(item, locale));
  const withParts = parts.filter((list) => list.length > 0);
  const shared =
    withParts.length > 1
      ? new Set(withParts[0].filter((part) => withParts.every((list) => list.includes(part))))
      : new Set<string>();
  const out = new Map<string, string>();
  items.forEach((item, index) => {
    const own = parts[index].filter((part) => part !== item.title && !shared.has(part));
    out.set(item.id, own.join(SUBTITLE_SEPARATOR));
  });
  return out;
}

/** The On Deck name of the next child: its subtitle, with the title added only when missing. */
export function nextUpName(item: SourceItem, locale: string): string {
  const parts = subtitleParts(item, locale);
  if (parts.length === 0) return item.title;
  if (!parts.includes(item.title)) parts.push(item.title);
  return parts.join(SUBTITLE_SEPARATOR);
}

/**
 * The short ordinal a primary action names its next-up child by (CORE-2826):
 * the plugin subtitle's parts other than the item's title, joined by spaces
 * ("S1 · E3" reads "S1 E3"); "" when the subtitle has nothing else.
 */
export function nextUpOrdinal(item: SourceItem, locale: string): string {
  return subtitleParts(item, locale)
    .filter((part) => part !== item.title)
    .join(" ");
}

/** The runtime a row shows: the item's own, else the engagement's. */
export function itemRuntimeSeconds(item: SourceItem): number | undefined {
  const seconds = item.durationSeconds ?? item.engagement?.durationSeconds;
  return seconds && seconds >= 60 ? seconds : undefined;
}

/**
 * Splits the hero fields into an overview and the rest when the Entity has no
 * builtin summary: its first long free-text field reads as the overview
 * (clamped by DetailOverview) instead of printing in full among the fields.
 * Short text fields stay facts.
 */
export function textOverview(
  summary: string,
  fields: readonly EntityFieldInfo[],
): { overview: string; fields: EntityFieldInfo[] } {
  if (summary) return { overview: summary, fields: [...fields] };
  const index = fields.findIndex(
    (field) => field.type === TEXT_FIELD_TYPE && field.value.trim().length >= OVERVIEW_MIN_LENGTH,
  );
  if (index < 0) return { overview: "", fields: [...fields] };
  return {
    overview: fields[index].value.trim(),
    fields: fields.filter((_, i) => i !== index),
  };
}

/** The related groups whose Entities a similar container is found through. */
export function similarityGroups(entity: EntityInfo | null | undefined): RelatedEntityGroup[] {
  return (entity?.relatedEntities ?? []).filter(
    (group) => group.archetype === "category" || group.archetype === "person",
  );
}

/**
 * Other Entities of the page's kind reached through its category and person
 * Entities, ranked by how many of them each shares, then by first appearance.
 * The page's own Entity never appears.
 */
export function similarContainers(
  entity: { id: string; kind: string },
  through: readonly (EntityInfo | null)[],
  limit: number,
): SourceItem[] {
  const scores = new Map<string, { item: SourceItem; shared: number; first: number }>();
  let order = 0;
  for (const shared of through) {
    const reached = new Set<string>();
    // A related item's own kind is the generic "entity"; its group names the Entity kind.
    for (const group of shared?.relatedEntities ?? []) {
      if (group.kind !== entity.kind) continue;
      for (const item of group.items) {
        if (item.id === entity.id || reached.has(item.id)) continue;
        reached.add(item.id);
        const entry = scores.get(item.id);
        if (entry) entry.shared += 1;
        else scores.set(item.id, { item, shared: 1, first: order++ });
      }
    }
  }
  return [...scores.values()]
    .sort((a, b) => b.shared - a.shared || a.first - b.first)
    .slice(0, limit)
    .map((entry) => entry.item);
}

/** The Source kinds whose shelves list the catalog and are ranked on the client. */
const RANKED_SHELF_SOURCES = new Set(["entity-items", "all-items"]);

/** Whether a Section is a catalog shelf the client ranks. */
export function isRankedShelf(section: { kind: string; source: { kind: string } }): boolean {
  return section.kind === "shelf" && RANKED_SHELF_SOURCES.has(section.source.kind);
}

/** 0 in progress, 1 unstarted (or unknown), 2 finished. */
function engagementTier(item: SourceItem): number {
  const engagement = item.engagement;
  if (!engagement) return 1;
  if (engagement.completed === true) return 2;
  if (resumeProgress(item)) return 0;
  const total = engagement.totalCount ?? 0;
  if (total > 0) {
    const remaining = engagement.remainingCount ?? 0;
    if (remaining === 0) return 2;
    if (remaining < total) return 0;
  }
  return 1;
}

/**
 * A catalog shelf's order: in progress first, then unstarted, then finished;
 * newer year first within a tier, then the server's order.
 */
export function rankShelfItems(items: readonly SourceItem[]): SourceItem[] {
  return items
    .map((item, index) => ({ item, index, tier: engagementTier(item) }))
    .sort((a, b) => a.tier - b.tier || (b.item.year ?? 0) - (a.item.year ?? 0) || a.index - b.index)
    .map((entry) => entry.item);
}
