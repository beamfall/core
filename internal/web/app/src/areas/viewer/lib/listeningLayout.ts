import type { EntityInfo, Layout, SourceItem } from "@/lib/graphql/types";
import { AUDIO_CLASS, hasKnownNonAudio, isAudioListing } from "./trackList";

// When an Entity page uses the listening layout over a plugin's detail Layout
// (CORE-2362). A detail Layout whose Sections only list the Entity's own Media
// (every Section an entity-media Source) says nothing the listening layout
// does not: an audio container's numbered track list or an audio person's top
// tracks. So for a "container" or "person" Entity whose Media all report class
// "audio", the page lists them the listening way instead. Like the rest of the
// listening rules (lib/trackList), this reads archetypes, Source kinds and the
// Media class the server reports, never a plugin's kind names.

const ENTITY_MEDIA_SOURCE = "entity-media";

/** Whether every Section of a Layout lists the Entity's own Media. */
export function layoutListsOnlyMedia(layout: Layout): boolean {
  return (
    layout.sections.length > 0 &&
    layout.sections.every((section) => section.source.kind === ENTITY_MEDIA_SOURCE)
  );
}

/**
 * Whether the page lists the Entity's Media the listening way over its detail
 * Layout: false as soon as anything known rules it out, null while the Entity
 * or its Media are still loading. `previewClass` is the shared Media class the
 * link that opened the page carried, so a page known not to be audio renders
 * its Layout without waiting.
 */
export function listensOverLayout(
  layout: Layout | null,
  entity: EntityInfo | null,
  items: readonly SourceItem[] | null,
  previewClass?: string,
): boolean | null {
  if (!layout || !layoutListsOnlyMedia(layout)) return false;
  if (previewClass && previewClass !== AUDIO_CLASS) return false;
  if (items && hasKnownNonAudio(items)) return false;
  if (entity && entity.archetype !== "container" && entity.archetype !== "person") return false;
  if (!entity || !items) return null;
  return isAudioListing(items, {});
}

/** The Entity's related "person" Entities, in order, each once. */
export function entityPeople(entity: EntityInfo | null): SourceItem[] {
  const seen = new Set<string>();
  const out: SourceItem[] = [];
  for (const group of entity?.relatedEntities ?? []) {
    if (group.archetype !== "person") continue;
    for (const item of group.items) {
      if (seen.has(item.id)) continue;
      seen.add(item.id);
      out.push(item);
    }
  }
  return out;
}

/** A shuffled copy (Fisher–Yates); `random` returns values in [0, 1). */
export function shuffled<T>(items: readonly T[], random: () => number = Math.random): T[] {
  const out = [...items];
  for (let i = out.length - 1; i > 0; i -= 1) {
    const j = Math.floor(random() * (i + 1));
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}
