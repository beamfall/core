import { Bell, LayoutDashboard, Menu } from "lucide-react";
import { type CSSProperties, type ReactNode, useEffect, useId, useRef, useState } from "react";
import { Link, useLocation } from "react-router";
import { ServerActivity } from "@/components/beamfall/ServerActivity";
import { Dialog as DialogPrimitive } from "@/components/ui/astryx-primitives";
import { Badge } from "@/components/ui/badge";
import { ActionTooltip } from "@/components/ui/tooltip";
import { getBeamfallHost } from "@/lib/beamfallHost";
import { useHeadingDocumentTitle } from "@/lib/documentTitle";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { findNodeBySlug } from "@/lib/nodePath";
import { useRegister } from "@/lib/register";
import { formatServerBuild, useServerBuild } from "@/lib/serverBuild";
import { useSession } from "@/lib/session";
import { AccountMenu } from "./AccountMenu";
import { LibrarySidebar, type SidebarGroup, sidebarGroupId } from "./LibrarySidebar";
import { PhoneTabBar } from "./PhoneTabBar";
import { usePosterSizeRoot } from "./posterSize";
import { SidebarResizer } from "./SidebarResizer";
import { MAIN_CONTENT_ID, SkipLink } from "./SkipLink";
import { useSidebarPrefs } from "./sidebarPrefs";
import { readSidebarWidth, writeSidebarWidth } from "./sidebarWidth";
import { NOTIFICATION_BADGE_LIMIT, useUnreadNotifications } from "./unreadNotifications";
import "./chrome.css";

const PHONE_QUERY = "(max-width: 760px)";
const SIDEBAR_PREF = "beamfall.sidebarCollapsed";

// The desktop collapse choice is a per-browser convenience: storage may be
// unavailable (private window), so every access falls back silently.
function readCollapsedPref(): boolean {
  try {
    const stored = window.localStorage.getItem(SIDEBAR_PREF);
    if (stored != null) return stored === "1";
  } catch {
    // fall through to the width default
  }
  return (
    typeof window.matchMedia === "function" && window.matchMedia("(max-width: 1100px)").matches
  );
}

function writeCollapsedPref(collapsed: boolean) {
  try {
    window.localStorage.setItem(SIDEBAR_PREF, collapsed ? "1" : "0");
  } catch {
    // a convenience only
  }
}

function usePhoneLayout(): boolean {
  const [phone, setPhone] = useState(
    () => typeof window.matchMedia === "function" && window.matchMedia(PHONE_QUERY).matches,
  );
  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const query = window.matchMedia(PHONE_QUERY);
    const onChange = () => setPhone(query.matches);
    query.addEventListener("change", onChange);
    return () => query.removeEventListener("change", onChange);
  }, []);
  return phone;
}

function ServerBuildLabel({ hidden }: { hidden: boolean }) {
  const build = useServerBuild();
  const t = useT();
  const locale = useDisplayLocale();
  if (hidden || !build) return null;
  const { label, details } = formatServerBuild(build, locale, t);
  return (
    <span className="bf-build-label" role="note" aria-label={details} title={details}>
      <span aria-hidden="true">{label}</span>
    </span>
  );
}

// The shared app chrome (ADR-0007): a register-aware header + content slot in
// the Cinema Dark / Workbench design language. Device Role and Reveal state are
// visible chips, never a hidden mode. Areas render their content inside
// <AppShell>; the header reads the session spine, so it is identical wiring
// across Viewer/Manage/Console.
export function AppShell({
  children,
  actions,
}: {
  children: ReactNode;
  /** Optional area-specific header actions, rendered before the icons (the Viewer search). */
  actions?: ReactNode;
}) {
  const t = useT();
  const location = useLocation();
  const { register } = useRegister();
  const { session, manifest, isManager, revealed, needsEnroll } = useSession();
  // Cached across remounts: every route mounts its own AppShell, and the bell
  // must not refetch the inbox on each navigation.
  const unreadNotifications = useUnreadNotifications(
    register === "viewer" && session && !needsEnroll
      ? `${session.profileID}:${session.revealed ? "revealed" : "locked"}`
      : null,
  );
  const beamfallHost = getBeamfallHost();
  // The poster size roams with the Profile in the Viewer (CORE-2914).
  usePosterSizeRoot(register === "viewer" && session && !needsEnroll ? session.profileID : null);
  const sidebarId = useId();
  const phone = usePhoneLayout();
  const [collapsed, setCollapsed] = useState(readCollapsedPref);
  const [drawerOpen, setDrawerOpen] = useState(false);
  // The group a phone tab opened the drawer at (CORE-2827), and the control
  // that opened it, which gets focus back when it closes.
  const [drawerGroup, setDrawerGroup] = useState<SidebarGroup | null>(null);
  const drawerOpenerRef = useRef<HTMLElement | null>(null);
  // The expanded desktop width the person dragged to (CORE-2734).
  const [sidebarWidth, setSidebarWidth] = useState(readSidebarWidth);
  const [resizing, setResizing] = useState(false);
  const menuButtonRef = useRef<HTMLButtonElement>(null);
  const hasSidebar = register === "viewer" && !!manifest && manifest.navigation.length > 0;
  // The Profile's own pin and order choice over the server navigation (CORE-2435).
  const [sidebarPrefs, saveSidebarPrefs, sidebarPrefsReady] = useSidebarPrefs(
    hasSidebar && session && !needsEnroll ? session.profileID : null,
  );
  const sidebarEditable = hasSidebar && !!session && !needsEnroll;
  // Phone width trades the sidebar for a drawer and a bottom tab bar (CORE-2827).
  const tabBar = phone && hasSidebar;
  // The Console is reached by an Admin Session, which the server grants to the
  // adult owner Profile whatever the Device Role (auth.BeginAdminSession). The
  // Profile kind is the eligibility the client can see, so the link follows it
  // and an owner on a viewer-role Device still gets a way in.
  const canOpenConsole = session?.profile === "adult";
  // The tab names the page the shell is showing (CORE-2322).
  useHeadingDocumentTitle(MAIN_CONTENT_ID, t("brand"), !revealed);

  // The phone drawer is a modal dialog: the dialog primitive moves focus in,
  // traps it, closes on Escape or a press outside, and hands focus back to the
  // menu button. It also closes on navigation and when the layout grows past
  // phone width.
  useEffect(() => {
    setDrawerOpen(false);
  }, [location.pathname]);
  useEffect(() => {
    if (!phone) setDrawerOpen(false);
  }, [phone]);
  useEffect(() => {
    if (!drawerOpen) setDrawerGroup(null);
  }, [drawerOpen]);
  function openDrawerAt(group: SidebarGroup, opener: HTMLButtonElement) {
    drawerOpenerRef.current = opener;
    setDrawerGroup(group);
    setDrawerOpen(true);
  }
  // A tab opens the drawer with focus on its group's first entry, or on the
  // group's own control (More, Edit) when every entry is unpinned.
  function drawerInitialFocus(): HTMLElement | true {
    if (!drawerGroup) return true;
    const group = document.getElementById(sidebarGroupId(sidebarId, drawerGroup));
    return (
      group?.querySelector<HTMLElement>("ul a") ??
      group?.querySelector<HTMLElement>("a, button") ??
      true
    );
  }
  function toggleSidebar() {
    if (phone) {
      drawerOpenerRef.current = menuButtonRef.current;
      setDrawerGroup(null);
      setDrawerOpen((open) => !open);
      return;
    }
    setCollapsed((was) => {
      writeCollapsedPref(!was);
      return !was;
    });
  }

  // The active nav tab: a manifest node whose route matches the current path.
  // Home ("/") is the aggregate first layout node; "/n/:id" binds the named one.
  const homeNodeId = (manifest?.navigation || []).find((n) => n.view === "layout")?.id ?? "";
  const activeNodeId = (() => {
    const match = location.pathname.match(/^\/n\/([^/]+)/);
    if (match) {
      const segment = decodeURIComponent(match[1]);
      return findNodeBySlug(manifest?.navigation || [], segment)?.id ?? segment;
    }
    return location.pathname === "/" ? homeNodeId : "";
  })();

  const unreadLabel =
    unreadNotifications >= NOTIFICATION_BADGE_LIMIT
      ? `${NOTIFICATION_BADGE_LIMIT}+`
      : unreadNotifications;

  if (beamfallHost) {
    return (
      <>
        <SkipLink />
        <main id={MAIN_CONTENT_ID} tabIndex={-1} className="bf-main px-4 py-6">
          {children}
        </main>
      </>
    );
  }

  return (
    <div className={"bf-shell min-h-screen text-foreground" + (tabBar ? " has-tabbar" : "")}>
      <SkipLink />
      <header className={"bf-header" + (revealed ? " is-revealed" : "")}>
        {hasSidebar && (
          <ActionTooltip label={t("navLabel")} side="bottom">
            <button
              ref={menuButtonRef}
              type="button"
              className="bf-icon-button bf-menu-button"
              aria-label={t("navLabel")}
              aria-haspopup={phone ? "dialog" : undefined}
              aria-controls={!phone || drawerOpen ? sidebarId : undefined}
              aria-expanded={phone ? drawerOpen : !collapsed}
              onClick={toggleSidebar}
            >
              <Menu aria-hidden className="size-5" />
            </button>
          </ActionTooltip>
        )}
        <Link to="/" className="bf-brand" aria-label={t("brand")}>
          <span aria-hidden className="bf-mark" />
          <span aria-hidden className="bf-wordmark" />
        </Link>
        <ServerBuildLabel hidden={phone} />

        <div className="bf-actions">
          {actions}
          {revealed && (
            <Badge variant="outline" className="bf-chip-revealed">
              {t("revealed")}
            </Badge>
          )}
          {!phone && isManager && <ServerActivity />}
          {!phone && canOpenConsole && (
            <Link
              to="/console"
              className={
                "bf-icon-button" + (location.pathname.startsWith("/console") ? " is-active" : "")
              }
            >
              <LayoutDashboard aria-hidden className="size-[18px]" />
              <span className="bf-icon-label">{t("consoleHeading")}</span>
            </Link>
          )}
          {register === "viewer" && session && !needsEnroll && (
            <ActionTooltip label={t("notifications")} side="bottom">
              <Link
                to="/notifications"
                className={
                  "bf-icon-button" + (location.pathname === "/notifications" ? " is-active" : "")
                }
                aria-label={t("notificationsUnreadAria", { count: unreadLabel })}
              >
                <Bell aria-hidden className="size-[18px]" />
                {unreadNotifications > 0 && (
                  <span className="bf-notification-count" aria-hidden>
                    {unreadLabel}
                  </span>
                )}
              </Link>
            </ActionTooltip>
          )}
          {session && <AccountMenu showActivity={phone} />}
        </div>
      </header>

      <div className={"bf-body" + (resizing ? " is-resizing" : "")}>
        {hasSidebar &&
          manifest &&
          (phone ? (
            <DialogPrimitive.Root open={drawerOpen} onOpenChange={setDrawerOpen}>
              <DialogPrimitive.Portal>
                <DialogPrimitive.Backdrop className="bf-sidebar-scrim" />
                <DialogPrimitive.Popup
                  aria-label={t("navLabel")}
                  initialFocus={drawerInitialFocus}
                  finalFocus={drawerOpenerRef}
                  render={
                    <LibrarySidebar
                      id={sidebarId}
                      navigation={manifest.navigation}
                      libraries={manifest.libraries}
                      activeNodeId={activeNodeId}
                      collapsed={false}
                      drawer
                      onNavigate={() => setDrawerOpen(false)}
                      prefs={sidebarPrefs}
                      prefsReady={sidebarPrefsReady}
                      onSavePrefs={sidebarEditable ? saveSidebarPrefs : undefined}
                      showPersonal={sidebarEditable}
                      pathname={location.pathname}
                    />
                  }
                />
              </DialogPrimitive.Portal>
            </DialogPrimitive.Root>
          ) : (
            <LibrarySidebar
              id={sidebarId}
              navigation={manifest.navigation}
              libraries={manifest.libraries}
              activeNodeId={activeNodeId}
              collapsed={collapsed}
              drawer={false}
              onNavigate={() => setDrawerOpen(false)}
              prefs={sidebarPrefs}
              prefsReady={sidebarPrefsReady}
              onSavePrefs={sidebarEditable ? saveSidebarPrefs : undefined}
              showPersonal={sidebarEditable}
              pathname={location.pathname}
              style={{ "--bf-sidebar-width": `${sidebarWidth}px` } as CSSProperties}
            />
          ))}
        {hasSidebar && manifest && !phone && !collapsed && (
          <SidebarResizer
            sidebarId={sidebarId}
            width={sidebarWidth}
            onChange={setSidebarWidth}
            onCommit={writeSidebarWidth}
            onResizingChange={setResizing}
          />
        )}
        <div className="bf-content">
          {needsEnroll && (
            <div role="status" className="bf-banner">
              {t("enrollLead")}
            </div>
          )}

          <main id={MAIN_CONTENT_ID} tabIndex={-1} className="bf-main px-4 py-6">
            {children}
          </main>
        </div>
      </div>
      {tabBar && (
        <PhoneTabBar
          pathname={location.pathname}
          onHome={homeNodeId !== "" && activeNodeId === homeNodeId}
          sidebarId={sidebarId}
          drawerGroup={drawerOpen ? drawerGroup : null}
          showPersonal={sidebarEditable}
          onOpenGroup={openDrawerAt}
        />
      )}
    </div>
  );
}
