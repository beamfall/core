import type { Section } from "@/lib/graphql/types";

// See-All routing context. The vanilla openSeeAll (app.js) re-queries the same
// Source the shelf queried, so the full Section + its Source scope (libraryID /
// entityID) must travel to the See-All page. The route is /seeall/:sourceId
// (sourceId = the Section.id, a stable URL anchor); the rich context that can't
// fit a path param rides react-router navigation state. A direct visit without
// state degrades gracefully (the page shows its empty fallback).
export interface SeeAllContext {
  section: Section;
  libraryID: string;
  /** "" on browse; the context entity id for a detail-Layout shelf. */
  entityID: string;
  /** The route See-All's Back returns to (browse, or an entity detail). */
  returnTo: string;
}

/** The /seeall/:sourceId target (href + navigation state) for one Section. */
export function seeAllTargetFor(ctx: SeeAllContext): {
  href: string;
  state: SeeAllContext;
} {
  return {
    href: `/seeall/${encodeURIComponent(ctx.section.id)}`,
    state: ctx,
  };
}
