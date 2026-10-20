import type { DisplayString } from "@/lib/graphql/types";

// Renders a plugin/manifest display-string field (ADR-0034 locale map, or its
// English-only string shorthand) to plain text. BCP-47 best-match with English
// fallback. Text only — the caller renders it as a {value}, never as HTML
// (ADR-0012 §5); a malformed map degrades deterministically to "".
export function displayString(value: DisplayString | null | undefined, locale = "en"): string {
  if (value == null) return "";
  if (typeof value === "string") return value;
  // Locale map: exact, then primary subtag, then English, then any entry.
  if (value[locale]) return value[locale];
  const primary = locale.split("-")[0];
  if (value[primary]) return value[primary];
  if (value.en) return value.en;
  const first = Object.values(value)[0];
  return typeof first === "string" ? first : "";
}
