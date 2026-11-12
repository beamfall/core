import { Bell, LayoutDashboard, Menu } from "lucide-react";
import { type ReactNode, useCallback, useEffect, useId, useState } from "react";
import { Link, useLocation } from "react-router";
import { ServerActivity } from "@/components/beamfall/ServerActivity";
import { Badge } from "@/components/ui/badge";
import { getBeamfallHost } from "@/lib/beamfallHost";
import { fetchNotifications } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { useRegister } from "@/lib/register";
import { useSession } from "@/lib/session";
import { AccountMenu } from "./AccountMenu";
import { LibrarySidebar } from "./LibrarySidebar";
import "./chrome.css";

const NOTIFICATION_BADGE_LIMIT = 100;
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
  const [unreadNotifications, setUnreadNotifications] = useState(0);
  const beamfallHost = getBeamfallHost();
  const sidebarId = useId();
  const phone = usePhoneLayout();
  const [collapsed, setCollapsed] = useState(readCollapsedPref);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const hasSidebar = register === "viewer" && !!manifest && manifest.navigation.length > 0;

  // The phone drawer closes on navigation, on Escape, and when the layout
  // grows past phone width.
  useEffect(() => {
    setDrawerOpen(false);
  }, [location.pathname]);
  useEffect(() => {
    if (!phone) setDrawerOpen(false);
  }, [phone]);
  useEffect(() => {
    if (!drawerOpen) return;
    function onKey(event: KeyboardEvent) {
      if (event.key === "Escape") setDrawerOpen(false);
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [drawerOpen]);

  function toggleSidebar() {
    if (phone) {
      setDrawerOpen((open) => !open);
      return;
    }
    setCollapsed((was) => {
      writeCollapsedPref(!was);
      return !was;
    });
  }

  const refreshUnreadNotifications = useCallback(async () => {
    if (register !== "viewer" || !session || needsEnroll) {
      setUnreadNotifications(0);
      return;
    }
    try {
      const rows = await fetchNotifications({
        unreadOnly: true,
        limit: NOTIFICATION_BADGE_LIMIT,
      });
      setUnreadNotifications(rows.length);
    } catch {
      setUnreadNotifications(0);
    }
  }, [needsEnroll, register, session]);

  useEffect(() => {
    void refreshUnreadNotifications();
  }, [refreshUnreadNotifications]);

  useEffect(() => {
    function refreshIfForeground() {
      if (document.visibilityState === "hidden") return;
      void refreshUnreadNotifications();
    }
    window.addEventListener("focus", refreshIfForeground);
    window.addEventListener("beamfall:notifications-changed", refreshIfForeground);
    document.addEventListener("visibilitychange", refreshIfForeground);
    return () => {
      window.removeEventListener("focus", refreshIfForeground);
      window.removeEventListener("beamfall:notifications-changed", refreshIfForeground);
      document.removeEventListener("visibilitychange", refreshIfForeground);
    };
  }, [refreshUnreadNotifications]);

  // The active nav tab: a manifest node whose route matches the current path.
  // Home ("/") is the aggregate first layout node; "/n/:id" binds the named one.
  const activeNodeId = (() => {
    const match = location.pathname.match(/^\/n\/([^/]+)/);
    if (match) return decodeURIComponent(match[1]);
    if (location.pathname === "/") {
      const first = (manifest?.navigation || []).find((n) => n.view === "layout");
      return first?.id ?? "";
    }
    return "";
  })();

  const unreadLabel =
    unreadNotifications >= NOTIFICATION_BADGE_LIMIT
      ? `${NOTIFICATION_BADGE_LIMIT}+`
      : unreadNotifications;

  if (beamfallHost) {
    return <main className="bf-main px-4 py-6">{children}</main>;
  }

  return (
    <div className="bf-shell min-h-screen text-foreground">
      <header className={"bf-header" + (revealed ? " is-revealed" : "")}>
        {hasSidebar && (
          <button
            type="button"
            className="bf-icon-button bf-menu-button"
            aria-label={t("navLabel")}
            aria-controls={sidebarId}
            aria-expanded={phone ? drawerOpen : !collapsed}
            onClick={toggleSidebar}
          >
            <Menu aria-hidden className="size-5" />
          </button>
        )}
        <Link to="/" className="bf-brand" aria-label={t("brand")}>
          <span aria-hidden className="bf-mark" />
          <span aria-hidden className="bf-wordmark" />
        </Link>

        <div className="bf-actions">
          {actions}
          {revealed && (
            <Badge variant="outline" className="bf-chip-revealed">
              {t("revealed")}
            </Badge>
          )}
          {isManager && <ServerActivity />}
          {isManager && (
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
            <Link
              to="/notifications"
              className={
                "bf-icon-button" + (location.pathname === "/notifications" ? " is-active" : "")
              }
              aria-label={t("notificationsUnreadAria", { count: unreadLabel })}
              title={t("notifications")}
            >
              <Bell aria-hidden className="size-[18px]" />
              {unreadNotifications > 0 && (
                <span className="bf-notification-count" aria-hidden>
                  {unreadLabel}
                </span>
              )}
            </Link>
          )}
          {session && <AccountMenu />}
        </div>
      </header>

      <div className="bf-body">
        {hasSidebar && manifest && (
          <>
            <LibrarySidebar
              id={sidebarId}
              navigation={manifest.navigation}
              libraries={manifest.libraries}
              activeNodeId={activeNodeId}
              collapsed={collapsed}
              drawer={phone}
              open={drawerOpen}
              onNavigate={() => setDrawerOpen(false)}
            />
            {phone && drawerOpen && (
              <button
                type="button"
                tabIndex={-1}
                aria-hidden
                className="bf-sidebar-scrim"
                onClick={() => setDrawerOpen(false)}
              />
            )}
          </>
        )}
        <div className="bf-content">
          {needsEnroll && (
            <div role="status" className="bf-banner">
              {t("enrollLead")}
            </div>
          )}

          <main className="bf-main px-4 py-6">{children}</main>
          <footer className="bf-source-footer">
            <a href="/source" target="_blank" rel="noreferrer noopener">
              {t("coreSourceOffer")}
            </a>
          </footer>
        </div>
      </div>
    </div>
  );
}
