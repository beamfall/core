import type { ReactNode } from "react";
import { useEffect } from "react";
import { NavLink, useLocation } from "react-router";
import { useT } from "@/lib/i18n";
import { type LibraryPivotPath, pivotOfPath, writeLibraryPivot } from "@/lib/libraryPivot";
import { nodePath } from "@/lib/nodePath";
import type { StringKey } from "@/strings";
import { useLibraryHasFeeds } from "../lib/feeds";
import { useIsKaraokeLibrary } from "../lib/karaoke";

// The pivots under a Library's name (CORE-2437): Recommended (the plugin's hub
// Layout), Library (the full browse), Collections and Categories. Each is a
// link with its own address, so a pivot can be shared and reached with Back;
// NavLink marks the open one with aria-current="page", which also drives its
// selected look. The pivot shown is remembered per Library (CORE-2826), so the
// sidebar can reopen it.
const PIVOTS: { path: LibraryPivotPath; label: StringKey }[] = [
  { path: "", label: "libraryPivotRecommended" },
  { path: "/all", label: "libraryPivotLibrary" },
  { path: "/collections", label: "libraryPivotCollections" },
  { path: "/categories", label: "libraryPivotCategories" },
];

// A karaoke Library opens on its Songbook (CORE-3362): search, the singers and
// the rotation in one place, in place of Recommended and the full browse.
// Collections and Categories stay as the secondary ways in.
const KARAOKE_PIVOTS: { path: LibraryPivotPath; label: StringKey }[] = [
  { path: "", label: "karaokeSongbookPivot" },
  { path: "/collections", label: "libraryPivotCollections" },
  { path: "/categories", label: "libraryPivotCategories" },
];

// Following (CORE-2957) lists the Library's followed feeds. The wire has no
// per-Library "can follow feeds" signal yet, so it shows once the Library has
// a followed feed; its page stays reachable by address before that.
const FEEDS_PIVOT: { path: LibraryPivotPath; label: StringKey } = {
  path: "/feeds",
  label: "feedsPivot",
};

export function LibraryPivots({ nodeID, libraryID }: { nodeID: string; libraryID: string }) {
  const t = useT();
  const { pathname } = useLocation();
  const hasFeeds = useLibraryHasFeeds(libraryID);
  const onFeeds = pivotOfPath(pathname) === FEEDS_PIVOT.path;
  const base = useIsKaraokeLibrary(libraryID) ? KARAOKE_PIVOTS : PIVOTS;
  const pivots = hasFeeds || onFeeds ? [...base, FEEDS_PIVOT] : base;
  useEffect(() => {
    writeLibraryPivot(libraryID, pivotOfPath(pathname));
  }, [libraryID, pathname]);
  return (
    <nav aria-label={t("libraryPivotsLabel")} className="bf-pivots">
      {pivots.map((pivot) => (
        <NavLink
          key={pivot.path}
          to={nodePath(nodeID, pivot.path, libraryID)}
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
  libraryID,
  heading,
  aside,
  pivots = true,
}: {
  nodeID: string;
  /** The Library behind the node, for the pivots that depend on its contents. */
  libraryID: string;
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
      {pivots && <LibraryPivots nodeID={nodeID} libraryID={libraryID} />}
    </div>
  );
}
