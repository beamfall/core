import type { ReactNode } from "react";
import { NavLink } from "react-router";
import { useT } from "@/lib/i18n";
import { nodePath } from "@/lib/nodePath";
import type { StringKey } from "@/strings";

// The pivots under a Library's name (CORE-2437): Recommended (the plugin's hub
// Layout), Library (the full browse), Collections and Categories. Each is a
// link with its own address, so a pivot can be shared and reached with Back;
// NavLink marks the open one with aria-current="page", which also drives its
// selected look.
const PIVOTS: { path: string; label: StringKey }[] = [
  { path: "", label: "libraryPivotRecommended" },
  { path: "/all", label: "libraryPivotLibrary" },
  { path: "/collections", label: "libraryPivotCollections" },
  { path: "/categories", label: "libraryPivotCategories" },
];

export function LibraryPivots({ nodeID }: { nodeID: string }) {
  const t = useT();
  return (
    <nav aria-label={t("libraryPivotsLabel")} className="bf-pivots">
      {PIVOTS.map((pivot) => (
        <NavLink
          key={pivot.path}
          to={nodePath(nodeID, pivot.path)}
          // Recommended lives at the Library's own address, which every other
          // pivot's address starts with.
          end={pivot.path === ""}
          className="bf-pivot"
        >
          {t(pivot.label)}
        </NavLink>
      ))}
    </nav>
  );
}

/** A Library page's heading block: its name, a status or actions beside it, then the pivots. */
export function LibraryHeader({
  nodeID,
  heading,
  aside,
  pivots = true,
}: {
  nodeID: string;
  heading: string;
  /** Shown on the heading's row: a count, actions. */
  aside?: ReactNode;
  pivots?: boolean;
}) {
  return (
    <div className="flex flex-col gap-3">
      <div className="flex w-full flex-wrap items-center gap-x-3 gap-y-3">
        <h1 className="bf-page-title">{heading}</h1>
        {aside}
      </div>
      {pivots && <LibraryPivots nodeID={nodeID} />}
    </div>
  );
}
