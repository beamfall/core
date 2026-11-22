import { Pencil } from "lucide-react";
import { useState } from "react";
import { Navigate, useParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { displayString } from "@/lib/display";
import type { NavigationNode, Section } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { findNodeBySlug, HOME_NODE_ID, nodePath, nodeSlug } from "@/lib/nodePath";
import { useSession } from "@/lib/session";
import { HomeEditor } from "../components/HomeEditor";
import { SectionSkeleton } from "../components/HydratedSection";
import { LayoutView } from "../components/LayoutView";
import { LibraryHeader } from "../components/LibraryPivots";
import { PosterSizeControl } from "../components/PosterSizeControl";
import type { SeeAllTarget } from "../components/SectionView";
import { ViewerActions } from "../components/ViewerActions";
import { type HomeLibrarySeeAllState, seeAllTargetFor } from "../lib/seeall";

// Browse view, ported from the vanilla renderNavigation / selectEntry /
// renderLayout (app.js). Resolves a NavigationNode to its Layout and renders it
// scoped to the node's libraryID. The root ("/") binds to the first layout
// NavigationNode; "/n/:nodeId" binds to a named one.
// Only the "layout" view type is renderable. A "/n/:nodeId" that names no
// layout node (the manifest's "ambient" entry, which carries no Layout, or a
// library that is gone or hidden from this Profile) redirects to Home rather
// than drawing Home's shelves under another node's address.
export function BrowsePage() {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const { nodeId } = useParams();
  const { manifest, loading, refresh } = useSession();
  const [editingHome, setEditingHome] = useState(false);
  // The node whose Layout settled with nothing in it: an empty Library offers
  // no pivots into empty views (CORE-2322).
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
  const landingNode = layoutNodes[0];
  const node = nodeId === undefined ? landingNode : findNodeBySlug(layoutNodes, nodeId);
  if (!node && landingNode) {
    return <Navigate to="/" replace />;
  }
  // An old link names the node by its raw ID; move it to the readable address
  // (CORE-2322).
  if (nodeId !== undefined && node && nodeId !== nodeSlug(node.id)) {
    return <Navigate to={nodePath(node.id)} replace />;
  }
  const home = node?.id === HOME_NODE_ID;
  const layout = (manifest?.layouts || []).find((l) => l.id === node?.layoutID) || null;
  const libraryID = node?.libraryID || "";

  function buildSeeAll(section: Section, homeShelfLibraryID?: string): SeeAllTarget {
    if (home && homeShelfLibraryID) {
      const state: HomeLibrarySeeAllState = { libraryID: homeShelfLibraryID };
      return { href: `/seeall/${encodeURIComponent(section.id)}`, state };
    }
    return seeAllTargetFor({
      section,
      libraryID,
      entityID: "",
      returnTo: nodeId === undefined || !node ? "/" : nodePath(node.id),
      home,
    });
  }

  return (
    <AppShell actions={<ViewerActions />}>
      {node && libraryID ? (
        // A Library node leads with its name and the pivots between its views
        // (CORE-2437): these hubs are the Recommended pivot. An empty Library
        // offers no way into empty views (CORE-2322). Its shelves follow the
        // poster size, so the Library pivot's size control is here too (CORE-2893).
        <div className="pb-6">
          <LibraryHeader
            nodeID={node.id}
            heading={displayString(node.title, displayLocale)}
            aside={emptyNode !== node.id && <PosterSizeControl className="ml-auto" />}
            pivots={emptyNode !== node.id}
          />
        </div>
      ) : (
        <h1 className="sr-only">{node ? displayString(node.title, displayLocale) : t("brand")}</h1>
      )}
      {home && !editingHome && (
        // Each Profile orders and hides its own Home shelves (CORE-1972).
        <div className="flex justify-end pb-4">
          <Button variant="ghost" size="sm" onClick={() => setEditingHome(true)}>
            <Pencil aria-hidden />
            {t("homeEdit")}
          </Button>
        </div>
      )}
      {home && editingHome ? (
        <HomeEditor onSaved={refresh} onClose={() => setEditingHome(false)} />
      ) : (
        <LayoutView
          layout={layout}
          libraryID={libraryID}
          home={home}
          buildSeeAll={buildSeeAll}
          navigationID={node?.id || HOME_NODE_ID}
          onEmptyChange={(empty) => {
            const id = node?.id ?? null;
            setEmptyNode((current) => (empty ? id : current === id ? null : current));
          }}
        />
      )}
    </AppShell>
  );
}
