import { fetchSourceItems } from "@/lib/graphql";
import type { FieldFilters, Section, SourceItem } from "@/lib/graphql/types";

// The six renderable Section kinds (manifest.Section.kind), copied exactly from
// the vanilla app.js SECTION_KINDS set. Unknown kinds are silently skipped and
// empty sections hidden (ADR-0014).
// biome-ignore format: conformance requires one static string literal per line.
export const SECTION_KINDS = new Set([
  "hero",
  "shelf",
  "grid",
  "list",
  "info",
  "filter-bar",
]);

// The vanilla MAX_RENDERED_SECTIONS cap (app.js): a Layout renders at most this
// many known Sections; the rest are dropped (a diagnostic in the original).
export const MAX_RENDERED_SECTIONS = 24;

// See All fetches bounded pages and accumulates them into the windowed grid;
// only its viewport slice mounts even as multiple pages are retained.
export const SEEALL_PAGE = 60;

export interface SectionFetchContext {
  libraryID: string;
  /** The context entity for entity-media Sources; "" elsewhere (server ignores it). */
  entityID: string;
  /** A filter-bar selection merged over the Source's declared fieldFilters; null when none. */
  extraFilters?: FieldFilters | null;
  /** Overrides the per-kind default limit (See All pages SEEALL_PAGE). */
  paging?: { limit: number; offset: number } | null;
}

/**
 * Hydrate one Section through its Source. Ported from the vanilla
 * fetchSectionItems (app.js): the Source's declared fieldFilters echo back
 * verbatim with a view-time filter-bar selection merged over them; entityID
 * rides the variables for entity-media Sources; shelves stay shallow (20) while
 * grids/lists page deeper (100), and hero takes the single feature item.
 */
export async function fetchSectionItems(
  section: Section,
  ctx: SectionFetchContext,
): Promise<SourceItem[]> {
  const source = section.source || { kind: "" };
  const merged: FieldFilters = {
    ...(source.fieldFilters || {}),
    ...(ctx.extraFilters || {}),
  };
  const limit = ctx.paging
    ? ctx.paging.limit
    : section.kind === "hero"
      ? 1
      : section.kind === "shelf"
        ? 20
        : 100;
  return fetchSourceItems({
    kind: source.kind || "",
    entityKind: source.entityKind || "",
    libraryID: ctx.libraryID || "",
    entityID: ctx.entityID || "",
    fieldFilters: Object.keys(merged).length ? merged : null,
    entityFilter: source.entityFilter || null,
    limit,
    offset: ctx.paging ? ctx.paging.offset : 0,
  });
}
