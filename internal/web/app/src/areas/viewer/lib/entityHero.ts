import { displayString } from "@/lib/display";
import type {
  EntityFieldInfo,
  EntityInfo,
  MediaCredit,
  RelatedEntityGroup,
  SourceItem,
} from "@/lib/graphql/types";
import { type DetailFacts, detailFacts, viewerFacingFields } from "./viewerFields";

// The parent-entity hero (CORE-2441): what an Entity page's header and rows
// read from the Entity, its related groups and its own Media. Everything is
// drawn from data present: the builtin `year` and `summary` field names, the
// shared rating and score fields, and the groups' fixed archetypes. Nothing
// here names a plugin kind.

/** The builtin field whose text is the hero's overview. */
export const SUMMARY_FIELD = "summary";
/** The builtin field whose number is the hero's year chip. */
export const YEAR_FIELD = "year";

function fieldValue(fields: readonly EntityFieldInfo[], name: string): string {
  return fields.find((field) => field.name === name)?.value.trim() ?? "";
}

/** The Entity's overview text, or "" when it declares none. */
export function entitySummary(entity: EntityInfo | null | undefined): string {
  return entity ? fieldValue(entity.fields, SUMMARY_FIELD) : "";
}

/** The Entity's year, from its `year` field when that is a plausible whole year. */
export function entityYear(entity: EntityInfo | null | undefined): number | undefined {
  const year = Number(entity ? fieldValue(entity.fields, YEAR_FIELD) : "");
  return Number.isInteger(year) && year > 0 ? year : undefined;
}

/**
 * The hero's facts: the year field, the rating and community score fields,
 * and the first few category-archetype related Entities, read the way the
 * Media detail page reads a Media item's facts.
 */
export function entityFacts(entity: EntityInfo | null | undefined): DetailFacts {
  if (!entity) return { categories: [] };
  const credits: MediaCredit[] = (entity.relatedEntities ?? [])
    .filter((group) => group.archetype === "category")
    .flatMap((group) =>
      group.items.map((item) => ({
        entityID: item.id,
        title: item.title,
        kind: group.kind,
        archetype: "category",
        relation: "",
      })),
    );
  return detailFacts(
    { id: entity.id, title: entity.title, favorited: false, year: entityYear(entity), credits },
    entity.fields,
  );
}

/** The fields the hero lists: viewer-facing, without the overview or what a chip shows. */
export function heroFields(
  entity: EntityInfo | null | undefined,
  year: number | undefined,
): EntityFieldInfo[] {
  if (!entity) return [];
  return viewerFacingFields(entity.fields, { title: entity.title, year }).filter(
    (field) => field.name !== SUMMARY_FIELD && field.name !== YEAR_FIELD,
  );
}

function playable(item: SourceItem): boolean {
  return item.kind === "media" && !item.locked;
}

/**
 * Whether an Entity page offers Mark watched, the watchlist toggle and a
 * rating: only a container is something a viewer watches. A person or a
 * category is watched through, so its page keeps Hide alone (CORE-3058).
 */
export function entityOffersWatchActions(archetype: string | undefined): boolean {
  return archetype === "container";
}

/**
 * How many of the Entity's own playable Media are not finished yet (0 when
 * none are left or none are known), plus the children left in each container
 * card the Entity's rolled-up Media list stands in for its held Media
 * (CORE-3058). Related container groups are never summed: they carry
 * relations in both directions with no direction on the wire, so a parent
 * cannot be told from a child. A container of containers shows no count until
 * the wire carries the Entity's own engagement. An item reached by two
 * relations counts once.
 */
export function unwatchedCount(items: readonly SourceItem[] | null | undefined): number {
  const unfinished = new Set<string>();
  let rolledUp = 0;
  for (const item of items ?? []) {
    if (unfinished.has(item.id)) continue;
    if (playable(item) && item.engagement?.completed !== true) {
      unfinished.add(item.id);
    } else if (item.kind === "entity" && !item.locked && item.engagement?.remainingCount) {
      unfinished.add(item.id);
      rolledUp += item.engagement.remainingCount - 1;
    }
  }
  return unfinished.size + rolledUp;
}

/** The hero's rows: child containers first, then people, then everything else. */
export interface HeroGroups {
  containers: RelatedEntityGroup[];
  people: RelatedEntityGroup[];
  other: RelatedEntityGroup[];
}

/** Splits related groups by archetype, keeping the server's order within each. */
export function heroGroups(groups: readonly RelatedEntityGroup[] | null | undefined): HeroGroups {
  const split: HeroGroups = { containers: [], people: [], other: [] };
  for (const group of groups ?? []) {
    if (group.items.length === 0) continue;
    if (group.archetype === "container") split.containers.push(group);
    else if (group.archetype === "person") split.people.push(group);
    else split.other.push(group);
  }
  return split;
}

/**
 * Person groups' items as credits, so they render through the cast row. Each
 * group's display name rides as the credit's department, so several person
 * groups read as labelled rows of one cast row.
 */
export function groupCredits(groups: readonly RelatedEntityGroup[], locale: string): MediaCredit[] {
  return groups.flatMap((group) => {
    const department = displayString(group.kindDisplayName, locale) || group.kind;
    return group.items.map((item) => ({
      entityID: item.id,
      title: item.title,
      kind: group.kind,
      kindDisplayName: group.kindDisplayName,
      archetype: "person",
      relation: "",
      attributes: { department },
      artworkSlots: item.artworkSlots,
    }));
  });
}

/**
 * A child container card carrying its "N items" count in its subline, after
 * any subtitle it already has. Unchanged when its count is unknown.
 */
export function withItemCount(
  item: SourceItem,
  countText: (count: number) => string,
  locale: string,
): SourceItem {
  const total = item.engagement?.totalCount;
  if (!total || total <= 0) return item;
  const subtitle = displayString(item.subtitle, locale).trim();
  const count = countText(total);
  return { ...item, subtitle: subtitle ? `${subtitle} · ${count}` : count };
}
