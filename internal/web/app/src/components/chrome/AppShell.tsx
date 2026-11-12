import { Bell, LayoutDashboard } from "lucide-react";
import { type ReactNode, useCallback, useEffect, useState } from "react";
import { Link, useLocation } from "react-router";
import { ServerActivity } from "@/components/beamfall/ServerActivity";
import { Badge } from "@/components/ui/badge";
import { getBeamfallHost } from "@/lib/beamfallHost";
import { displayString } from "@/lib/display";
import { fetchNotifications } from "@/lib/graphql";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useRegister } from "@/lib/register";
import { useSession } from "@/lib/session";
import { AccountMenu } from "./AccountMenu";
import "./chrome.css";

const NOTIFICATION_BADGE_LIMIT = 100;

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
  const displayLocale = useDisplayLocale();
  const location = useLocation();
  const { register } = useRegister();
  const { session, manifest, isManager, revealed, needsEnroll } = useSession();
  const [unreadNotifications, setUnreadNotifications] = useState(0);
  const beamfallHost = getBeamfallHost();

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
        <Link to="/" className="bf-brand" aria-label={t("brand")}>
          <span aria-hidden className="bf-mark" />
          <span aria-hidden className="bf-wordmark" />
        </Link>

        {register === "viewer" && manifest && manifest.navigation.length > 0 && (
          <nav aria-label={t("navLabel")} className="bf-nav">
            {manifest.navigation.map((node) => (
              <Link
                key={node.id}
                to={`/n/${node.id}`}
                aria-current={node.id === activeNodeId ? "page" : undefined}
                className={"bf-nav-link" + (node.id === activeNodeId ? " is-active" : "")}
              >
                {displayString(node.title, displayLocale)}
              </Link>
            ))}
          </nav>
        )}

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
  );
}
