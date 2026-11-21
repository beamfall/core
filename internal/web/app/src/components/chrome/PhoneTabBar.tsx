import { House, LibraryBig, Search, Star } from "lucide-react";
import { Link } from "react-router";
import { useT } from "@/lib/i18n";
import type { SidebarGroup } from "./LibrarySidebar";
import { PERSONAL_ENTRIES } from "./sidebarPrefs";

// The phone tab bar (CORE-2827): at phone width the sidebar is a drawer behind
// the menu button, so the bottom of the screen carries the main sections one
// tap away. Home and Search are links to their pages; Libraries and Your stuff
// open the navigation drawer with focus on that group, since a tab per library
// would not fit. The shell renders it only at phone width and pads the page so
// it never covers content.
export function PhoneTabBar({
  pathname,
  onHome,
  sidebarId,
  drawerGroup,
  showPersonal,
  onOpenGroup,
}: {
  pathname: string;
  /** The open page is Home, at "/" or at its own node address. */
  onHome: boolean;
  /** The drawer the group tabs open, while it is open. */
  sidebarId: string;
  /** The group the open drawer was opened at, or null while it is closed. */
  drawerGroup: SidebarGroup | null;
  /** Offer the signed-in Profile's Your stuff tab. */
  showPersonal: boolean;
  onOpenGroup: (group: SidebarGroup, opener: HTMLButtonElement) => void;
}) {
  const t = useT();
  const onLibrary = !onHome && pathname.startsWith("/n/");
  const onPersonal = PERSONAL_ENTRIES.some(
    (entry) => pathname === entry.path || pathname.startsWith(`${entry.path}/`),
  );

  function link(to: string, label: string, Icon: typeof House, current: boolean) {
    return (
      <li>
        <Link
          to={to}
          className={"bf-tab" + (current ? " is-active" : "")}
          aria-current={current ? "page" : undefined}
        >
          <Icon aria-hidden className="size-5" />
          <span className="bf-tab-label">{label}</span>
        </Link>
      </li>
    );
  }

  function groupButton(group: SidebarGroup, label: string, Icon: typeof House, active: boolean) {
    const open = drawerGroup === group;
    return (
      <li>
        <button
          type="button"
          className={"bf-tab" + (active ? " is-active" : "")}
          aria-haspopup="dialog"
          aria-expanded={open}
          aria-controls={open ? sidebarId : undefined}
          onClick={(event) => onOpenGroup(group, event.currentTarget)}
        >
          <Icon aria-hidden className="size-5" />
          <span className="bf-tab-label">{label}</span>
        </button>
      </li>
    );
  }

  return (
    <nav aria-label={t("tabBarLabel")} className="bf-tabbar">
      <ul className={"bf-tab-list" + (showPersonal ? "" : " is-short")}>
        {link("/", t("tabHome"), House, onHome)}
        {link("/search", t("search"), Search, pathname === "/search")}
        {groupButton("libraries", t("libraries"), LibraryBig, onLibrary)}
        {showPersonal && groupButton("personal", t("yourStuff"), Star, onPersonal)}
      </ul>
    </nav>
  );
}
