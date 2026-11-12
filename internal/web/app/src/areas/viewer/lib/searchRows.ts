import type { SearchResultItem } from "@/lib/graphql/types";
import type { StringKey } from "@/strings";

// Presentation helpers shared by the header search and the search page. Core
// is domain-agnostic: a row's type is its core media class (a Media row) or its
// plugin-declared Entity kind, never a hardcoded content vocabulary.

/** How many rows the header panel shows before pointing at the full page. */
export const HEADER_SEARCH_ROWS = 8;

const MEDIA_CLASS_LABEL: Record<string, StringKey> = {
  video: "searchTypeVideo",
  audio: "searchTypeAudio",
  image: "searchTypeImage",
};

export interface SearchRowGroup {
  key: string;
  label: string;
  rows: SearchResultItem[];
}

type Translate = (key: StringKey) => string;

function localized(names: Record<string, string> | undefined, locales: string[]): string {
  if (!names) return "";
  for (const tag of locales) {
    const hit = names[tag] || names[tag.split("-")[0]];
    if (hit) return hit;
  }
  return names.en || Object.values(names)[0] || "";
}

function groupKey(item: SearchResultItem): string {
  if (item.kind === "media") return `media:${item.mediaClass ?? ""}`;
  return `entity:${item.entityKind ?? ""}`;
}

/** The row's type as the viewer reads it, e.g. "Video" or a plugin's kind name. */
export function searchRowTypeLabel(
  item: SearchResultItem,
  t: Translate,
  locales: string[],
): string {
  if (item.kind === "media") {
    const key = MEDIA_CLASS_LABEL[item.mediaClass ?? ""];
    return key ? t(key) : item.mediaClass || t("searchTypeOther");
  }
  const named = localized(item.entityKindDisplayName, locales);
  if (named) return named;
  // A kind with no display name is a plugin identifier ("container"); sentence-case it.
  const kind = item.entityKind ?? "";
  return kind ? kind.charAt(0).toLocaleUpperCase(locales) + kind.slice(1) : t("searchTypeOther");
}

/** Year and Library, the detail that tells two same-titled rows apart. */
export function searchRowDetail(item: SearchResultItem): string {
  return [item.year ? String(item.year) : "", item.libraryTitle ?? ""].filter(Boolean).join(" · ");
}

/** Groups rows by type, keeping the server's ranking order within and across groups. */
export function groupSearchRows(
  items: SearchResultItem[],
  t: Translate,
  locales: string[],
): SearchRowGroup[] {
  const groups = new Map<string, SearchRowGroup>();
  for (const item of items) {
    const key = groupKey(item);
    let group = groups.get(key);
    if (!group) {
      group = { key, label: searchRowTypeLabel(item, t, locales), rows: [] };
      groups.set(key, group);
    }
    group.rows.push(item);
  }
  return [...groups.values()];
}

/**
 * Where "open" takes a row. An Entity opens its page; a Media row opens the
 * player because the web app has no Media page yet (CORE-1144) — change it here.
 */
export function searchRowOpenPath(item: SearchResultItem): string {
  const id = encodeURIComponent(item.id);
  return item.kind === "media" ? `/watch/${id}` : `/entity/${id}`;
}

/** Where a row's Play control goes, or "" when the row is not playable. */
export function searchRowPlayPath(item: SearchResultItem): string {
  return item.kind === "media" ? `/watch/${encodeURIComponent(item.id)}` : "";
}

export function searchPagePath(term: string): string {
  return `/search?q=${encodeURIComponent(term)}`;
}

// The catalog guard compares placeholders across locales, which rules out an
// inline plural with translated branches, so the count has one key per form.
export function searchCountLabel(
  t: (key: StringKey, values?: Record<string, string | number>) => string,
  count: number,
  query: string,
): string {
  return count === 1
    ? t("searchResultCountOne", { query })
    : t("searchResultCountOther", { count, query });
}
