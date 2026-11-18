import type { MediaCredit, SourceItem } from "@/lib/graphql/types";

// People and credits (CORE-2406, CORE-2503). A Media item's person-archetype credits
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
 * attribute, the credits without one first and then each department in
 * first-seen order, keeping the server's credit order within each group.
 * Each person is taken from the first credit that lists them. A cast credit
 * (one with a character or a billing position) that also names departments
 * (a director who also acts) is listed in the cast and, without its
 * character, under each of its departments; a merged credit joins several
 * departments with ", " (CORE-2503).
 */
export function castGroups(credits: readonly MediaCredit[] | null | undefined): CastGroup[] {
  const groups: CastGroup[] = [];
  const seen = new Set<string>();
  const add = (department: string, credit: MediaCredit) => {
    const group = groups.find((g) => g.department === department);
    if (group) group.credits.push(credit);
    else groups.push({ department, credits: [credit] });
  };
  for (const credit of credits ?? []) {
    if (credit.archetype !== "person" || !credit.entityID || !credit.title) continue;
    if (seen.has(credit.entityID)) continue;
    seen.add(credit.entityID);
    const departments = listAttribute(credit, "department");
    const cast = attribute(credit, "character") !== "" || attribute(credit, "billing") !== "";
    if (cast || departments.length === 0) add("", credit);
    if (departments.length === 0) continue;
    const {
      character: _character,
      role: _role,
      billing: _billing,
      ...crew
    } = credit.attributes ?? {};
    const entry = cast ? { ...credit, attributes: crew } : credit;
    for (const department of departments) add(department, entry);
  }
  const castFirst = (group: CastGroup) => (group.department === "" ? 0 : 1);
  return groups.sort((a, b) => castFirst(a) - castFirst(b));
}

/** The people credited with any of `jobs` (a credit's `job` may list several), in credit order, once each. */
export function creditsWithJob(
  credits: readonly MediaCredit[] | null | undefined,
  jobs: readonly string[],
): MediaCredit[] {
  const seen = new Set<string>();
  return (credits ?? []).filter((credit) => {
    if (credit.archetype !== "person" || !credit.entityID || !credit.title) return false;
    if (seen.has(credit.entityID)) return false;
    if (!listAttribute(credit, "job").some((job) => jobs.includes(job))) return false;
    seen.add(credit.entityID);
    return true;
  });
}

function attribute(credit: MediaCredit, name: string): string {
  return credit.attributes?.[name]?.trim() ?? "";
}

/** A merged credit joins one attribute's distinct values with ", " (CORE-2503). */
function listAttribute(credit: MediaCredit, name: string): string[] {
  return attribute(credit, name)
    .split(", ")
    .map((value) => value.trim())
    .filter(Boolean);
}

/** The credit's part (character, else role, else job), or "" when it names none. */
export function creditPart(credit: MediaCredit): string {
  for (const name of PART_ATTRIBUTES) {
    const value = credit.attributes?.[name]?.trim();
    if (value) return value;
  }
  return "";
}

/** The credit's headshot artwork path, or "" when it has none. */
export function creditPhoto(credit: MediaCredit): string {
  return credit.artworkSlots?.poster ?? credit.artworkSlots?.thumb ?? "";
}

/** Whether any person credit carries a headshot or a part, so a cast row has more than names. */
export function castHasDetail(credits: readonly MediaCredit[] | null | undefined): boolean {
  return castGroups(credits).some((group) =>
    group.credits.some((credit) => creditPhoto(credit) !== "" || creditPart(credit) !== ""),
  );
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
