import type { LibraryHealthInfo } from "@/lib/graphql/types";
import type { StringKey } from "@/strings";

// Overview "Needs attention" from Library health (CORE-2310): the owner sees
// the same counts Library Health reports, summed over every Library, so the
// Overview never says nothing needs attention while Health lists problems.

/** One Library health dimension with items in it, summed over all Libraries. */
export interface HealthAttention {
  dimension: string;
  label: StringKey;
  count: number;
  /** Where the owner deals with it. */
  to: string;
}

// The dimensions an owner acts on, in the order Library Health lists them.
const HEALTH_DIMENSIONS: { dimension: string; label: StringKey; to: string }[] = [
  { dimension: "unmatched", label: "healthUnmatched", to: "/manage/unmatched" },
  { dimension: "broken", label: "healthBroken", to: "/console/health" },
  { dimension: "scrape-failure", label: "healthScrapeFailure", to: "/console/health" },
  { dimension: "missing-artwork", label: "healthMissingArtwork", to: "/console/health" },
];

export function healthAttention(libraries: LibraryHealthInfo[] | null): HealthAttention[] {
  if (!libraries) return [];
  return HEALTH_DIMENSIONS.map((entry) => ({
    ...entry,
    count: libraries.reduce((sum, library) => sum + (library.counts?.[entry.dimension] ?? 0), 0),
  })).filter((entry) => entry.count > 0);
}

/** Items still waiting for a match over all Libraries, or null when unknown. */
export function unmatchedTotal(libraries: LibraryHealthInfo[] | null): number | null {
  if (!libraries) return null;
  return libraries.reduce((sum, library) => sum + (library.counts?.unmatched ?? 0), 0);
}

/** The Libraries page link that opens a Library's folders (CORE-2309). */
export function libraryFoldersLink(libraryID: string): string {
  return `/console/libraries#folders-${libraryID}`;
}
