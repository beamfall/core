import { useState } from "react";
import { Link, Navigate, useParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { buttonVariants } from "@/components/ui/button";
import { displayString } from "@/lib/display";
import type { NavigationNode, Section } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { findNodeBySlug, nodePath, nodeSlug } from "@/lib/nodePath";
import { useSession } from "@/lib/session";
import { SectionSkeleton } from "../components/HydratedSection";
import { LayoutView } from "../components/LayoutView";
import type { SeeAllTarget } from "../components/SectionView";
import { ViewerActions } from "../components/ViewerActions";
import { seeAllTargetFor } from "../lib/seeall";

// Browse view, ported from the vanilla renderNavigation / selectEntry /
// renderLayout (app.js). Resolves a NavigationNode to its Layout and renders it
// scoped to the node's libraryID. Home ("/") binds to the first layout
// NavigationNode (the aggregate Home entry); "/n/:nodeId" binds to a named one.
// Only the "layout" view type is renderable. A "/n/:nodeId" that names no
// layout node (the manifest's "ambient" entry, which carries no Layout, or a
// library that is gone or hidden from this Profile) redirects to Home rather
// than drawing Home's shelves under another node's address.
export function BrowsePage({ home = false }: { home?: boolean }) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const { nodeId } = useParams();
  const { manifest, loading } = useSession();
  // The node whose Layout settled with nothing in it: an empty Library offers
  // no "Browse all" into an empty grid (CORE-2322).
  const [emptyNode, setEmptyNode] = useState<string | null>(null);

  if (loading && !manifest) {
    return (
      <AppShell actions={<ViewerActions />}>
        <div aria-busy="true" className="flex flex-col gap-10">
          <span className="sr-only">{t("loading")}</span>
          <SectionSkeleton hero />
          <SectionSkeleton hero={false} />
        </div>
      </AppShell>
    );
  }

  const layoutNodes: NavigationNode[] = (manifest?.navigation || []).filter(
    (entry) => entry.view === "layout",
  );
  const node = home ? layoutNodes[0] : findNodeBySlug(layoutNodes, nodeId);
  if (!home && !node && layoutNodes.length > 0) {
    return <Navigate to="/" replace />;
  }
  // An old link names the node by its raw ID; move it to the readable address
  // (CORE-2322).
  if (!home && node && nodeId !== nodeSlug(node.id)) {
    return <Navigate to={nodePath(node.id)} replace />;
  }
  const layout = (manifest?.layouts || []).find((l) => l.id === node?.layoutID) || null;
  const libraryID = node?.libraryID || "";

  function buildSeeAll(section: Section): SeeAllTarget {
    return seeAllTargetFor({
      section,
      libraryID,
      entityID: "",
      returnTo: home || !node ? "/" : nodePath(node.id),
    });
  }

  return (
    <AppShell actions={<ViewerActions />}>
      {node && libraryID ? (
        // A Library node leads with its name and the way into the full,
        // sortable library (CORE-1190); the hubs below stay as they were.
        <div className="flex flex-wrap items-center justify-between gap-3 pb-6">
          <h1 className="bf-page-title">{displayString(node.title, displayLocale)}</h1>
          {emptyNode !== node.id && (
            <Link
              className={buttonVariants({ variant: "outline", size: "sm" })}
              to={nodePath(node.id, "/all")}
            >
              {t("libraryBrowseAll")}
            </Link>
          )}
        </div>
      ) : (
        <h1 className="sr-only">{node ? displayString(node.title, displayLocale) : t("brand")}</h1>
      )}
      <LayoutView
        layout={layout}
        libraryID={libraryID}
        buildSeeAll={buildSeeAll}
        navigationID={node?.id || "home"}
        onEmptyChange={(empty) => {
          const id = node?.id ?? null;
          setEmptyNode((current) => (empty ? id : current === id ? null : current));
        }}
      />
    </AppShell>
  );
}
