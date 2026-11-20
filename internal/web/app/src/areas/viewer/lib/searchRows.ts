import type { SearchResultItem } from "@/lib/graphql/types";
import type { StringKey } from "@/strings";
import { mediaPath } from "./mediaAddress";

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

// Entity kind names are scoped to a plugin, so two plugins can give one name
// different labels; the label is part of the key so a heading always fits its rows.
function groupKey(item: SearchResultItem, label: string): string {
  if (item.kind === "media") return `media:${item.mediaClass ?? ""}`;
  return `entity:${item.entityKind ?? ""}:${label}`;
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
    const label = searchRowTypeLabel(item, t, locales);
    const key = groupKey(item, label);
    let group = groups.get(key);
    if (!group) {
      group = { key, label, rows: [] };
      groups.set(key, group);
    }
    group.rows.push(item);
  }
  return [...groups.values()];
}

/** The rows a container in the results holds, nested under it (CORE-2525). */
export interface NestedSearchRows {
  rows: SearchResultItem[];
  /** By container Entity id, in the server's order. */
  nested: Map<string, SearchResultItem[]>;
}

/**
 * Moves rows found only through their container's name under that container
 * when the container is itself a result (CORE-2525): searching a show's name
 * lists the show once, with its matching episodes folded beneath it, instead
 * of a wall of episodes. A row whose own title has the term stays in its group.
 */
export function nestUnderContainers(
  items: SearchResultItem[],
  term: string,
  locale = "en",
): NestedSearchRows {
  const containers = new Set(items.filter((item) => item.kind === "entity").map((item) => item.id));
  const needle = term.trim().toLocaleLowerCase(locale);
  const rows: SearchResultItem[] = [];
  const nested = new Map<string, SearchResultItem[]>();
  for (const item of items) {
    const container = item.container?.id;
    const ownMatch = needle !== "" && item.title.toLocaleLowerCase(locale).includes(needle);
    if (item.kind !== "media" || !container || !containers.has(container) || ownMatch) {
      rows.push(item);
      continue;
    }
    const under = nested.get(container);
    if (under) under.push(item);
    else nested.set(container, [item]);
  }
  return { rows, nested };
}

/**
 * Where "open" takes a row. An Entity opens its page; a Media row opens the
 * player because the web app has no Media page yet (CORE-1144) — change it here.
 */
export function searchRowOpenPath(item: SearchResultItem): string {
  const id = encodeURIComponent(item.id);
  return item.kind === "media" ? mediaPath("watch", item.id) : `/entity/${id}`;
}

/** Where a row's Play control goes, or "" when the row is not playable. */
export function searchRowPlayPath(item: SearchResultItem): string {
  return item.kind === "media" ? mediaPath("watch", item.id) : "";
}

export function searchPagePath(term: string): string {
  return `/search?q=${encodeURIComponent(term)}`;
}

/**
 * Legacy servers do not report hasMore. Keep their fifty-row count honest;
 * current servers explicitly say when the final page has arrived (CORE-2321).
 */
export const SEARCH_RESULT_CAP = 50;

export function searchCountLabel(
  t: (key: StringKey, values?: Record<string, string | number>) => string,
  count: number,
  query: string,
  hasMore?: boolean,
): string {
  return t(
    (hasMore ?? count >= SEARCH_RESULT_CAP) ? "searchResultCountCapped" : "searchResultCount",
    {
      count,
      query,
    },
  );
}

/** Keep loaded pages distinct and container children in ordinal order. */
export function mergeSearchPages(
  previous: SearchResultItem[],
  page: SearchResultItem[],
): SearchResultItem[] {
  const distinct = new Map(previous.map((item) => [`${item.kind}:${item.id}`, item]));
  for (const item of page) distinct.set(`${item.kind}:${item.id}`, item);
  const groups = new Map<string, SearchResultItem[]>();
  for (const item of distinct.values()) {
    const key = item.container?.id ? `entity:${item.container.id}` : `${item.kind}:${item.id}`;
    const group = groups.get(key);
    if (group) group.push(item);
    else groups.set(key, [item]);
  }
  return [...groups.values()].flatMap((group) =>
    group.sort((a, b) => {
      if (a.kind !== b.kind) return a.kind === "entity" ? -1 : 1;
      return (a.sequence ?? 0) - (b.sequence ?? 0) || (a.index ?? 0) - (b.index ?? 0);
    }),
  );
}
