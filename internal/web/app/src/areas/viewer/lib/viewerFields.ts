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
/**
 * The label to show for that rating (CORE-2399). It may sit outside the ordered
 * policy scale (a TV rating), so it wins over RATING_FIELD for display only.
 */
export const RATING_DISPLAY_FIELD = "certification-display";
/** A provider's 0-10 community score and its vote count (CORE-2399); the field label names the source. */
export const SCORE_FIELD = "community-rating";
export const SCORE_VOTES_FIELD = "community-rating-votes";
/** Fields the facts row already shows; listing them again would repeat them. */
const FACT_FIELDS = new Set([RATING_FIELD, RATING_DISPLAY_FIELD, SCORE_FIELD, SCORE_VOTES_FIELD]);
/** At most this many category credits (the plugin's "category" archetype) become chips. */
const MAX_CATEGORY_FACTS = 3;

/** What the page already shows, so a field that only repeats it is dropped. */
export interface ShownFacts {
  /** The heading: a field equal to it (an "original" title) adds nothing. */
  title?: string;
  /** The year chip: a number equal to it, or a date that is only that year, repeats it. */
  year?: number;
  /** The runtime chip: a bare number of minutes equal to it repeats it without a unit. */
  runtimeSeconds?: number;
}

// A content address or provenance record ("sha256:9f2c…;cel;plan=…") is
// bookkeeping a hydration stage writes, never a fact a viewer reads (CORE-2319).
const INTERNAL_IDENTIFIER = /(?:^|[\s;:=])(?:sha\d+|blake\d*|md5):[0-9a-f]{16,}|;cel;/i;

/** isInternalIdentifier reports a value that is an internal content address. */
export function isInternalIdentifier(value: string): boolean {
  return INTERNAL_IDENTIFIER.test(value);
}

const YEAR_ONLY_DATE = /^(\d{4})-01-01(?:[T ]00:00(?::00(?:\.0+)?)?(?:Z|[+-]00:?00)?)?$/;

/**
 * A stored date that is only a year: January 1 of the year the year chip
 * already shows. Sources that know just the year store it that way, so
 * printing "January 1, 2013" invents a day nobody recorded.
 */
function isYearOnlyDate(value: string, year: number | undefined): boolean {
  const match = YEAR_ONLY_DATE.exec(value.trim());
  return !!match && !!year && Number(match[1]) === year;
}

function normalized(value: string): string {
  return value.trim().toLocaleLowerCase();
}

/** A field whose value the heading or a facts chip already shows. */
function repeatsShownFact(field: EntityFieldInfo, shown: ShownFacts): boolean {
  const value = field.value.trim();
  if (shown.title && field.type === "text" && normalized(value) === normalized(shown.title)) {
    return true;
  }
  if (field.type === "date") return isYearOnlyDate(value, shown.year);
  if (field.type !== "number") return false;
  const number = Number(value);
  if (!Number.isFinite(number)) return false;
  if (shown.year && number === shown.year) return true;
  if (shown.runtimeSeconds && number === Math.round(shown.runtimeSeconds / 60)) return true;
  // An ordinal the heading carries ("Season 1" with Number 1).
  return (
    !!shown.title &&
    normalized(shown.title)
      .split(/[^\p{L}\p{N}]+/u)
      .includes(value)
  );
}

/**
 * The fields a viewer reads: declared by the plugin (they carry its label),
 * not the class discriminant, not a flag marker, not empty, not an internal
 * identifier and not something the heading or a facts chip already shows.
 * Order is the server's.
 */
export function viewerFacingFields(
  fields: readonly EntityFieldInfo[],
  shown: ShownFacts = {},
): EntityFieldInfo[] {
  return fields.filter(
    (field) =>
      displayString(field.label).trim() !== "" &&
      field.name !== CLASS_FIELD &&
      field.type !== "flag" &&
      !FACT_FIELDS.has(field.name) &&
      field.value.trim() !== "" &&
      !isInternalIdentifier(field.value) &&
      !repeatsShownFact(field, shown),
  );
}

/** The item's facts, in display order, before any are formatted. */
export interface DetailFacts {
  year?: number;
  runtimeSeconds?: number;
  rating?: string;
  score?: DetailScore;
  categories: string[];
}

/** A community score out of 10, with its vote count and the plugin's field label. */
export interface DetailScore {
  value: number;
  votes: number;
  label?: EntityFieldInfo["label"];
}

/**
 * The facts chips' data: year, runtime (when the opening card knew the
 * duration), the rating, the community score when anyone voted, and the
 * first few category credits.
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
  const value = (name: string) => fields.find((field) => field.name === name)?.value.trim() ?? "";
  const rating = value(RATING_DISPLAY_FIELD) || value(RATING_FIELD);
  if (rating) facts.rating = rating;
  const score = fields.find((field) => field.name === SCORE_FIELD);
  const scoreValue = Number(score?.value);
  const votes = Number(value(SCORE_VOTES_FIELD));
  if (score && scoreValue > 0 && scoreValue <= 10 && votes > 0) {
    facts.score = { value: scoreValue, votes, label: score.label };
  }
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
  // Date.UTC reads years 0-99 as 1900-1999, so the year is checked too.
  if (
    date.getUTCFullYear() !== year ||
    date.getUTCMonth() !== month - 1 ||
    date.getUTCDate() !== day
  ) {
    return field.value;
  }
  const options = { dateStyle: "long", timeZone: "UTC" } as const;
  try {
    return new Intl.DateTimeFormat(locale || undefined, options).format(date);
  } catch {
    return new Intl.DateTimeFormat(undefined, options).format(date);
  }
}

/** A 0-10 score with one decimal in the display locale (CORE-2399, CORE-2400). */
export function formatScore(value: number, locale: string): string {
  const options = { minimumFractionDigits: 1, maximumFractionDigits: 1 };
  try {
    return new Intl.NumberFormat(locale || "en", options).format(value);
  } catch {
    return new Intl.NumberFormat("en", options).format(value);
  }
}
