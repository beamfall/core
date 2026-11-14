import { Link, Navigate, useParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { buttonVariants } from "@/components/ui/button";
import { displayString } from "@/lib/display";
import type { NavigationNode, Section } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { SectionSkeleton } from "../components/HydratedSection";
import { LayoutView } from "../components/LayoutView";
import { RevealControls } from "../components/RevealControls";
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
  const node = home ? layoutNodes[0] : layoutNodes.find((entry) => entry.id === nodeId);
  if (!home && !node && layoutNodes.length > 0) {
    return <Navigate to="/" replace />;
  }
  const layout = (manifest?.layouts || []).find((l) => l.id === node?.layoutID) || null;
  const libraryID = node?.libraryID || "";

  function buildSeeAll(section: Section): SeeAllTarget {
    return seeAllTargetFor({
      section,
      libraryID,
      entityID: "",
      returnTo: home ? "/" : `/n/${nodeId ?? ""}`,
    });
  }

  return (
    <AppShell
      actions={
        <>
          <ViewerActions />
          <RevealControls />
        </>
      }
    >
      {node && libraryID ? (
        // A Library node leads with its name and the way into the full,
        // sortable library (CORE-1190); the hubs below stay as they were.
        <div className="flex flex-wrap items-center justify-between gap-3 pb-6">
          <h1 className="bf-page-title">{displayString(node.title, displayLocale)}</h1>
          <Link
            className={buttonVariants({ variant: "outline", size: "sm" })}
            to={`/n/${encodeURIComponent(node.id)}/all`}
          >
            {t("libraryBrowseAll")}
          </Link>
        </div>
      ) : (
        <h1 className="sr-only">{node ? displayString(node.title, displayLocale) : t("brand")}</h1>
      )}
      <LayoutView
        layout={layout}
        libraryID={libraryID}
        buildSeeAll={buildSeeAll}
        navigationID={node?.id || "home"}
      />
    </AppShell>
  );
}
