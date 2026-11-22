import { nodePath } from "./nodePath";

// The last Library pivot a viewer used, per Library navigation node (CORE-2826).
// It is a per-viewer convenience, so it lives in this browser only; a blocked or
// cleared store, or a value that is no longer a pivot, means Recommended.

/** Each pivot's sub-path under the Library's address; "" is Recommended and
 * "/feeds" is Following (CORE-2957). */
export const LIBRARY_PIVOT_PATHS = ["", "/all", "/collections", "/categories", "/feeds"] as const;
export type LibraryPivotPath = (typeof LIBRARY_PIVOT_PATHS)[number];

const LEGACY_PIVOT_KEY = "beamfall.libraryPivot.library-";
// Use a distinct namespace: a current Library ID can equal another
// Library's old navigation node ID.
const PIVOT_KEY = "beamfall.libraryPivotByLibrary.";

/** The pivot an address under a Library node shows. */
export function pivotOfPath(pathname: string): LibraryPivotPath {
  const match = /^\/n\/[^/]+(\/[^/]+)?\/?$/.exec(pathname);
  return LIBRARY_PIVOT_PATHS.find((path) => path === (match?.[1] ?? "")) ?? "";
}

export function readLibraryPivot(libraryID: string): LibraryPivotPath {
  let stored: string | null = null;
  try {
    // A pivot remembered before CORE-2343 sits under the node's old doubled ID.
    stored =
      window.localStorage.getItem(PIVOT_KEY + libraryID) ??
      window.localStorage.getItem(LEGACY_PIVOT_KEY + libraryID);
  } catch {
    // A blocked store falls back to Recommended.
  }
  return LIBRARY_PIVOT_PATHS.find((path) => path === stored) ?? "";
}

export function writeLibraryPivot(libraryID: string, pivot: LibraryPivotPath): void {
  try {
    window.localStorage.setItem(PIVOT_KEY + libraryID, pivot);
  } catch {
    // Remembering the pivot is optional.
  }
}

/** Where a link that names only the Library goes: its remembered pivot. */
export function rememberedLibraryPath(nodeID: string, libraryID: string): string {
  return nodePath(nodeID, readLibraryPivot(libraryID), libraryID);
}
