import { nodePath } from "./nodePath";

// The last Library pivot a viewer used, per Library navigation node (CORE-2826).
// It is a per-viewer convenience, so it lives in this browser only; a blocked or
// cleared store, or a value that is no longer a pivot, means Recommended.

/** Each pivot's sub-path under the Library's address; "" is Recommended. */
export const LIBRARY_PIVOT_PATHS = ["", "/all", "/collections", "/categories"] as const;
export type LibraryPivotPath = (typeof LIBRARY_PIVOT_PATHS)[number];

const PIVOT_KEY = "beamfall.libraryPivot.";

/** The pivot an address under a Library node shows. */
export function pivotOfPath(pathname: string): LibraryPivotPath {
  const match = /^\/n\/[^/]+(\/[^/]+)?\/?$/.exec(pathname);
  return LIBRARY_PIVOT_PATHS.find((path) => path === (match?.[1] ?? "")) ?? "";
}

export function readLibraryPivot(nodeID: string): LibraryPivotPath {
  let stored: string | null = null;
  try {
    stored = window.localStorage.getItem(PIVOT_KEY + nodeID);
  } catch {
    // A blocked store falls back to Recommended.
  }
  return LIBRARY_PIVOT_PATHS.find((path) => path === stored) ?? "";
}

export function writeLibraryPivot(nodeID: string, pivot: LibraryPivotPath): void {
  try {
    window.localStorage.setItem(PIVOT_KEY + nodeID, pivot);
  } catch {
    // Remembering the pivot is optional.
  }
}

/** Where a link that names only the Library goes: its remembered pivot. */
export function rememberedLibraryPath(nodeID: string): string {
  return nodePath(nodeID, readLibraryPivot(nodeID));
}
