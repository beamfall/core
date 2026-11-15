import { displayString } from "@/lib/display";
import type { EntityFieldInfo, GenericMediaInfo } from "@/lib/graphql/types";

// Which of an item's stored fields the Viewer shows (CORE-1785).
//
// mediaFields returns every non-sensitive stored field. Some of them are
// bookkeeping, not facts a viewer reads: positions core's scan writes without a
// plugin declaration, the SDK's `class` discriminant that keys a plugin's
// class labels, and `flag` markers a hydration stage sets. The plugin SDK has
// no viewer-facing declaration yet, so the Viewer applies this generic rule and
// names no plugin field. Manage (Curation) still lists every field.

/** The SDK-conventional discriminant a plugin's class labels key on. */
const CLASS_FIELD = "class";
/** Core's regulatory label field (internal/store certification); shown as a facts chip. */
export const RATING_FIELD = "certification";
/** Fields the facts row already shows; listing them again would repeat them. */
const FACT_FIELDS = new Set([RATING_FIELD]);
/** At most this many category credits (the plugin's "category" archetype) become chips. */
const MAX_CATEGORY_FACTS = 3;

/**
 * The fields a viewer reads: declared by the plugin (they carry its label),
 * not the class discriminant, not a flag marker, not empty and not already a
 * facts chip. Order is the server's.
 */
export function viewerFacingFields(fields: readonly EntityFieldInfo[]): EntityFieldInfo[] {
  return fields.filter(
    (field) =>
      displayString(field.label).trim() !== "" &&
      field.name !== CLASS_FIELD &&
      field.type !== "flag" &&
      !FACT_FIELDS.has(field.name) &&
      field.value.trim() !== "",
  );
}

/** The item's facts, in display order, before any are formatted. */
export interface DetailFacts {
  year?: number;
  runtimeSeconds?: number;
  rating?: string;
  categories: string[];
}

/**
 * The facts chips' data: year, runtime (when the opening card knew the
 * duration), the regulatory rating and the first few category credits.
 */
export function detailFacts(
  media: GenericMediaInfo | null | undefined,
  fields: readonly EntityFieldInfo[],
  durationSeconds?: number,
): DetailFacts {
  const facts: DetailFacts = { categories: [] };
  if (media?.year) facts.year = media.year;
  if (durationSeconds && Number.isFinite(durationSeconds) && durationSeconds >= 60) {
    facts.runtimeSeconds = durationSeconds;
  }
  const rating = fields.find((field) => field.name === RATING_FIELD)?.value.trim();
  if (rating) facts.rating = rating;
  for (const credit of media?.credits ?? []) {
    if (credit.archetype !== "category" || !credit.title) continue;
    if (facts.categories.includes(credit.title)) continue;
    facts.categories.push(credit.title);
    if (facts.categories.length === MAX_CATEGORY_FACTS) break;
  }
  return facts;
}

/** Whole hours and remaining minutes of a runtime, rounded to the minute. */
export function runtimeParts(seconds: number): { hours: number; minutes: number } {
  const total = Math.round(seconds / 60);
  return { hours: Math.floor(total / 60), minutes: total % 60 };
}

const DATE_VALUE = /^(\d{4})-(\d{2})-(\d{2})(?:[T ].*)?$/;

/**
 * A field's value as a viewer reads it. A `date` field is stored as an ISO
 * date or timestamp (2025-05-09T00:00:00Z); it shows as a calendar date in the
 * display locale (May 9, 2025). The stored day is kept as written, never
 * shifted into the viewer's time zone. Anything else, and a date that does not
 * parse, shows as stored.
 */
export function fieldDisplayValue(
  field: Pick<EntityFieldInfo, "type" | "value">,
  locale?: string,
): string {
  if (field.type !== "date") return field.value;
  const match = DATE_VALUE.exec(field.value.trim());
  if (!match) return field.value;
  const [year, month, day] = [Number(match[1]), Number(match[2]), Number(match[3])];
  const date = new Date(Date.UTC(year, month - 1, day));
  if (date.getUTCMonth() !== month - 1 || date.getUTCDate() !== day) return field.value;
  const options = { dateStyle: "long", timeZone: "UTC" } as const;
  try {
    return new Intl.DateTimeFormat(locale || undefined, options).format(date);
  } catch {
    return new Intl.DateTimeFormat(undefined, options).format(date);
  }
}
