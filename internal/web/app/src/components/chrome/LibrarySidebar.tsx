import { House, LayoutGrid } from "lucide-react";
import type { CSSProperties } from "react";
import { Link } from "react-router";
import { displayString } from "@/lib/display";
import type { LibrarySummary, NavigationNode } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { libraryIcon } from "@/lib/libraryIcons";

// The Viewer's left sidebar (CORE-1131): the manifest's navigation as a
// vertical list, so any number of libraries fits where a header tab strip ran
// out of room. Aggregate entries (Home, Ambient) come first with a glyph;
// library entries follow under a "Libraries" heading, each with a tile tinted
// by the library's own accent and carrying its icon (CORE-1200), or a monogram
// when the server names no icon this client knows. Desktop collapses it to an
// icon rail; phones open it as a drawer. The shell owns that state and passes
// it in.
//
// Only views this client renders get an entry: BrowsePage draws "layout" nodes
// and sends anything else to Home, so an Ambient entry would be a link that
// goes nowhere of its own.
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
  const icons = new Map(libraries.map((library) => [library.id, libraryIcon(library.icon)]));
  const rendered = navigation.filter((node) => node.view === "layout");
  const aggregates = rendered.filter((node) => !node.libraryID);
  const libraryNodes = rendered.filter((node) => node.libraryID);
  const hidden = drawer && !open;
  const homeId = rendered[0]?.id;
  // Only tiles without an icon carry a monogram, so only those can collide.
  const lettered = libraryNodes.filter((node) => !icons.get(node.libraryID ?? ""));
  const monograms = libraryMonograms(
    lettered.map((node) => displayString(node.title, displayLocale)),
  );
  const monogramFor = new Map(lettered.map((node, index) => [node.id, monograms[index]]));

  function item(node: NavigationNode) {
    const title = displayString(node.title, displayLocale);
    const active = node.id === activeNodeId;
    const accent = node.libraryID ? accents.get(node.libraryID) : undefined;
    const Icon = node.libraryID ? icons.get(node.libraryID) : undefined;
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
              {Icon ? <Icon className="size-4" strokeWidth={2.25} /> : monogramFor.get(node.id)}
            </span>
          ) : (
            <span aria-hidden className="bf-side-glyph">
              {node.id === homeId ? (
                <House className="size-[18px]" />
              ) : (
                <LayoutGrid className="size-[18px]" />
              )}
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

// Library tile monograms: what a tile carries when the manifest names no icon
// this client knows (an older server, or a name newer than this build). The
// tile is then drawn from the title: its initial, or two characters wherever
// initials would collide, so "Podcasts" and "Photos" read "Po" and "Ph" rather
// than a pair of identical tiles.
export function libraryMonograms(titles: string[]): string[] {
  const initials = titles.map((title) => monogram(title, false));
  return titles.map((title, index) =>
    initials.indexOf(initials[index]) !== initials.lastIndexOf(initials[index])
      ? monogram(title, true)
      : initials[index],
  );
}

function monogram(title: string, long: boolean): string {
  const words = title.trim().split(/\s+/).filter(Boolean);
  const [first = "•", second = ""] = Array.from(words[0] ?? "");
  if (!long) return first.toUpperCase();
  // Several words: their initials ("Home Videos" is "HV"). One word: its
  // first two characters.
  const next = words.length > 1 ? (Array.from(words[1])[0]?.toUpperCase() ?? "") : second;
  return first.toUpperCase() + next;
}
