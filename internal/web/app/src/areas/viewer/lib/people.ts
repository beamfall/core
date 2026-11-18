import type { MediaCredit, SourceItem } from "@/lib/graphql/types";

// People and credits (CORE-2406). A Media item's person-archetype credits
// render as a cast row; a person's Media render as a filmography. The
// attribute names read here are the relation attributes plugins declare for
// credits; a credit without them still renders with its name alone. Nothing
// here names a plugin kind.

export interface CastGroup {
  /** The credits' shared `department` attribute; "" for credits without one. */
  department: string;
  credits: MediaCredit[];
}

/** The attribute names whose value describes a person's part, in preference order. */
const PART_ATTRIBUTES = ["character", "role", "job"] as const;

/**
 * Groups a Media item's person-archetype credits by their `department`
 * attribute in first-seen order, keeping the server's credit (billing) order
 * within each group. Each person appears once, in the first group that lists
 * them.
 */
export function castGroups(credits: readonly MediaCredit[] | null | undefined): CastGroup[] {
  const groups: CastGroup[] = [];
  const seen = new Set<string>();
  for (const credit of credits ?? []) {
    if (credit.archetype !== "person" || !credit.entityID || !credit.title) continue;
    if (seen.has(credit.entityID)) continue;
    seen.add(credit.entityID);
    const department = credit.attributes?.department?.trim() ?? "";
    const group = groups.find((g) => g.department === department);
    if (group) group.credits.push(credit);
    else groups.push({ department, credits: [credit] });
  }
  return groups;
}

/** The credit's part (character, else role, else job), or "" when it names none. */
export function creditPart(credit: MediaCredit): string {
  for (const name of PART_ATTRIBUTES) {
    const value = credit.attributes?.[name]?.trim();
    if (value) return value;
  }
  return "";
}

/** The first letter of a name, for a headshot frame with no photo. */
export function nameInitial(name: string): string {
  const first = name.trim().codePointAt(0);
  return first === undefined ? "" : String.fromCodePoint(first).toLocaleUpperCase();
}

/** A person's Media newest year first; undated items last, otherwise in server order. */
export function filmographyOrder(items: readonly SourceItem[]): SourceItem[] {
  return items
    .map((item, index) => ({ item, index }))
    .sort((a, b) => {
      const ay = a.item.year ?? Number.NEGATIVE_INFINITY;
      const by = b.item.year ?? Number.NEGATIVE_INFINITY;
      if (ay !== by) return by - ay;
      return a.index - b.index;
    })
    .map(({ item }) => item);
}

/** The item's first billed person: its first person-archetype credit, or null. */
export function firstBilledPerson(
  credits: readonly MediaCredit[] | null | undefined,
): MediaCredit | null {
  return castGroups(credits)[0]?.credits[0] ?? null;
}
