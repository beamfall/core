import type { MediaCredit, SourceItem } from "@/lib/graphql/types";

// Order and roles carried on a credit relation's attributes (CORE-3014): a
// container that sequences its Media through a relation attribute (a book's
// place in a series, "4.5"), and a person credited with roles on each Media
// ("author, narrator"). The names are the relation attribute names a bundle
// declares, read off the credit; nothing here names a plugin or a kind.

/** The relation attribute that places a Media in its container's sequence. */
export const SEQUENCE_ATTRIBUTE = "sequence";
/** The relation attribute that lists a person's comma-separated roles on a Media. */
export const ROLE_ATTRIBUTE = "role";

/** The named attribute on the item's credit from `entityID`, or "" when absent. */
export function creditAttribute(
  credits: readonly MediaCredit[] | undefined,
  entityID: string,
  name: string,
): string {
  for (const credit of credits ?? []) {
    const value = credit.entityID === entityID ? credit.attributes?.[name]?.trim() : "";
    if (value) return value;
  }
  return "";
}

/** A comma-separated role list, trimmed and lower-cased, de-duplicated in order. */
export function creditRoles(value: string): string[] {
  const roles: string[] = [];
  for (const part of value.split(",")) {
    const role = part.trim().toLowerCase();
    if (role && !roles.includes(role)) roles.push(role);
  }
  return roles;
}

/** A plain decimal sequence ("4", "4.5"); anything else ("IV", "1e3", "4a") shows as stored. */
const DECIMAL_SEQUENCE = /^\d+(?:\.(\d{1,20}))?$/;

/**
 * A sequence for display: a decimal in the locale's form with every stored
 * fraction digit ("4,5" in French, "4.25" never rounded), else as stored.
 */
export function sequenceLabel(sequence: string, locale: string): string {
  const match = DECIMAL_SEQUENCE.exec(sequence);
  if (!match) return sequence;
  const digits = match[1]?.length ?? 0;
  return Number(sequence).toLocaleString(locale, {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
    useGrouping: false,
  });
}

/** A Media's place in a container its credit sequences it in. */
export interface SequencePosition {
  /** The container Entity. */
  id: string;
  title: string;
  /** The relation's sequence attribute, as stored ("4.5"). */
  sequence: string;
}

/**
 * The container an audio Media's credit places in a sequence, from the
 * relation's sequence attribute: the first such credit, or null.
 */
export function sequencePosition(
  media: { mediaClass?: string; credits?: readonly MediaCredit[] } | null | undefined,
): SequencePosition | null {
  if (media?.mediaClass !== "audio") return null;
  for (const credit of media.credits ?? []) {
    if (credit.archetype !== "container") continue;
    const sequence = creditAttribute([credit], credit.entityID, SEQUENCE_ATTRIBUTE);
    if (sequence) return { id: credit.entityID, title: credit.title, sequence };
  }
  return null;
}

/**
 * The item after this one in the sequencing container, from a container
 * context whose nearest container is that one; null otherwise, so another
 * container's order is never offered as the next in sequence.
 */
export function nextInSequence(
  position: SequencePosition | null,
  context: { container: { id: string } | null; next: SourceItem | null },
): SourceItem | null {
  return position && context.container?.id === position.id ? context.next : null;
}

/** Roles in their display order; any other role follows in the order first seen. */
const ROLE_ORDER = ["author", "narrator", "translator"];

export interface RoleGroup {
  /** The role, or "" for Media credited without one. */
  role: string;
  items: SourceItem[];
}

/**
 * A person's Media grouped by their role on each, in listing order within a
 * group. Media credited with several roles appear under each; Media with no
 * role read go last under "". Empty when no Media carries a role.
 */
export function roleGroups(
  items: readonly SourceItem[],
  roles: Readonly<Record<string, readonly string[]>>,
): RoleGroup[] {
  const groups = new Map<string, SourceItem[]>();
  let anyRole = false;
  for (const item of items) {
    if (item.kind !== "media") continue;
    const itemRoles = roles[item.id]?.length ? roles[item.id] : [""];
    for (const role of itemRoles) {
      anyRole ||= role !== "";
      const group = groups.get(role);
      if (group) group.push(item);
      else groups.set(role, [item]);
    }
  }
  if (!anyRole) return [];
  const rank = (role: string) => {
    if (role === "") return Number.MAX_SAFE_INTEGER;
    const at = ROLE_ORDER.indexOf(role);
    return at >= 0 ? at : ROLE_ORDER.length;
  };
  // Map iteration keeps first-seen order; the sort is stable for equal ranks.
  return [...groups.entries()]
    .map(([role, groupItems]) => ({ role, items: groupItems }))
    .sort((a, b) => rank(a.role) - rank(b.role));
}

/** Seconds left of a resume point, never negative. */
export function secondsLeft(progress: { positionSeconds: number; durationSeconds: number }) {
  return Math.max(0, progress.durationSeconds - progress.positionSeconds);
}
