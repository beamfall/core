import { House, LayoutGrid, Sparkles } from "lucide-react";
import type { CSSProperties } from "react";
import { Link } from "react-router";
import { displayString } from "@/lib/display";
import type { LibrarySummary, NavigationNode } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";

// The Viewer's left sidebar (CORE-1131): the manifest's navigation as a
// vertical list, so any number of libraries fits where a header tab strip ran
// out of room. Aggregate entries (Home, Ambient) come first with a glyph;
// library entries follow under a "Libraries" heading, each with a tile tinted
// by the library's own accent. Desktop collapses it to an icon rail; phones
// open it as a drawer. The shell owns that state and passes it in.
export function LibrarySidebar({
  id,
  navigation,
  libraries,
  activeNodeId,
  collapsed,
  drawer,
  open,
  onNavigate,
}: {
  id: string;
  navigation: NavigationNode[];
  libraries: LibrarySummary[];
  activeNodeId: string;
  collapsed: boolean;
  /** Phone layout: the sidebar is an overlay drawer, hidden unless open. */
  drawer: boolean;
  open: boolean;
  onNavigate: () => void;
}) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const accents = new Map(libraries.map((library) => [library.id, library.accent]));
  const aggregates = navigation.filter((node) => !node.libraryID);
  const libraryNodes = navigation.filter((node) => node.libraryID);
  const hidden = drawer && !open;
  const homeId = navigation.find((node) => node.view === "layout")?.id;

  function item(node: NavigationNode) {
    const title = displayString(node.title, displayLocale);
    const active = node.id === activeNodeId;
    const accent = node.libraryID ? accents.get(node.libraryID) : undefined;
    return (
      <li key={node.id}>
        <Link
          to={`/n/${node.id}`}
          aria-current={active ? "page" : undefined}
          className={"bf-side-link" + (active ? " is-active" : "")}
          title={collapsed && !drawer ? title : undefined}
          onClick={onNavigate}
        >
          {node.libraryID ? (
            <span
              aria-hidden
              className="bf-side-tile"
              style={accent ? ({ "--tile": accent } as CSSProperties) : undefined}
            >
              {Array.from(title.trim())[0]?.toUpperCase() ?? "•"}
            </span>
          ) : (
            <span aria-hidden className="bf-side-glyph">
              <AggregateIcon node={node} home={node.id === homeId} />
            </span>
          )}
          <span className="bf-side-label">{title}</span>
        </Link>
      </li>
    );
  }

  return (
    <aside
      id={id}
      className={
        "bf-sidebar" +
        (collapsed && !drawer ? " is-collapsed" : "") +
        (drawer ? " is-drawer" : "") +
        (open ? " is-open" : "")
      }
      inert={hidden}
      aria-hidden={hidden || undefined}
    >
      <nav aria-label={t("navLabel")}>
        <ul className="bf-side-list">{aggregates.map(item)}</ul>
        {libraryNodes.length > 0 && (
          <>
            <h2 className="bf-side-heading">{t("libraries")}</h2>
            <ul className="bf-side-list">{libraryNodes.map(item)}</ul>
          </>
        )}
      </nav>
    </aside>
  );
}

function AggregateIcon({ node, home }: { node: NavigationNode; home: boolean }) {
  if (home) return <House className="size-[18px]" />;
  if (node.view === "ambient") return <Sparkles className="size-[18px]" />;
  return <LayoutGrid className="size-[18px]" />;
}
