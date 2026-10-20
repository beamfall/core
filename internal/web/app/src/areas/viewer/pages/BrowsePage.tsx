import { useParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import type { NavigationNode, Section } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { LayoutView } from "../components/LayoutView";
import { RevealControls } from "../components/RevealControls";
import type { SeeAllTarget } from "../components/SectionView";
import { ViewerActions } from "../components/ViewerActions";
import { seeAllTargetFor } from "../lib/seeall";

// Browse view, ported from the vanilla renderNavigation / selectEntry /
// renderLayout (app.js). Resolves a NavigationNode to its Layout and renders it
// scoped to the node's libraryID. Home ("/") binds to the first layout
// NavigationNode (the aggregate Home entry); "/n/:nodeId" binds to a named one.
// Defensive: only the "layout" view type is renderable (others are skipped).
export function BrowsePage({ home = false }: { home?: boolean }) {
  const t = useT();
  const { nodeId } = useParams();
  const { manifest, loading } = useSession();

  if (loading && !manifest) {
    return (
      <AppShell actions={<ViewerActions />}>
        <p className="text-sm text-[var(--text-muted)]">{t("loading")}</p>
      </AppShell>
    );
  }

  const layoutNodes: NavigationNode[] = (manifest?.navigation || []).filter(
    (entry) => entry.view === "layout",
  );
  const node = home
    ? layoutNodes[0]
    : layoutNodes.find((entry) => entry.id === nodeId) || layoutNodes[0];
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
      <LayoutView
        layout={layout}
        libraryID={libraryID}
        buildSeeAll={buildSeeAll}
        navigationID={node?.id || "home"}
      />
    </AppShell>
  );
}
