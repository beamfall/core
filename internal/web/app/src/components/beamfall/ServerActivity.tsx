import { Activity, RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, useLocation } from "react-router";
import { useAnyRescanActive } from "@/components/beamfall/LibraryRescan";
import { fetchAdminSession, fetchJobs } from "@/lib/graphql";
import { useT } from "@/lib/i18n";

// Header activity link for managers (CORE-1131): one click from any page to
// the job queue, with a live count while jobs run. The jobs read needs an Admin
// Session, so while the Console is locked the count is unknown (null), never a
// claimed zero; unlocking it brings the count back on the next poll. Without a
// known-active Admin Session the poll asks only for the session's standing (a
// plain session read) and skips the jobs read the server would refuse
// (CORE-2318); a known session is trusted until its expiry or a refused read.
//
// The jobs read returns the whole job history (the wire has no status filter,
// limit or running count), and this header sits on every page, so the read is
// kept rare: nothing is read while the tab is hidden, an idle server is asked
// only every IDLE_POLL_MS, and the fast cadence runs only while work is known
// to be under way.

const ACTIVE_POLL_MS = 5000;
const IDLE_POLL_MS = 30000;

export function ServerActivity({ menuItem = false }: { menuItem?: boolean } = {}) {
  const t = useT();
  const location = useLocation();
  const rescanning = useAnyRescanActive();
  const [running, setRunning] = useState<number | null>(null);

  // A rescan starting on this page restarts the effect, so its job is counted
  // at once and followed at the active cadence.
  useEffect(() => {
    let cancelled = false;
    let timer: number | undefined;
    let reading = false;
    // Epoch ms until which the Admin Session is known active; 0 when unknown.
    let adminUntil = 0;
    async function adminActive(): Promise<boolean> {
      if (Date.now() < adminUntil) return true;
      const session = await fetchAdminSession();
      const expires = session.expiresAt ? Date.parse(session.expiresAt) : Number.NaN;
      adminUntil = session.active
        ? Number.isNaN(expires)
          ? Date.now() + IDLE_POLL_MS
          : expires
        : 0;
      return session.active;
    }
    async function refresh() {
      window.clearTimeout(timer);
      // A hidden tab reads nothing and schedules nothing; becoming visible
      // again starts the next read.
      if (reading || document.visibilityState === "hidden") return;
      reading = true;
      let count: number | null = null;
      try {
        if (await adminActive()) {
          // A refusal stays possible (the session ended early); it must not
          // open the Manage area's in-place unlock over the page (CORE-1197).
          const jobs = await fetchJobs(undefined, { adminUnlock: false });
          count = jobs.filter((job) => job.status === "running").length;
        }
      } catch {
        // unknown: the read was refused or failed; ask for the session again
        adminUntil = 0;
      }
      reading = false;
      if (cancelled) return;
      setRunning(count);
      const active = rescanning || (count ?? 0) > 0;
      timer = window.setTimeout(() => void refresh(), active ? ACTIVE_POLL_MS : IDLE_POLL_MS);
    }
    function onVisibility() {
      if (document.visibilityState !== "hidden") void refresh();
    }
    void refresh();
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [rescanning]);

  const count = running ?? 0;
  const busy = rescanning || count > 0;
  const label = count > 0 ? t("serverActivityRunning", { count }) : t("serverActivity");
  if (menuItem) {
    return (
      <Link
        to="/console/operations"
        className="bf-account-item bf-activity-link"
        aria-current={location.pathname === "/console/operations" ? "page" : undefined}
      >
        {busy ? (
          <RefreshCw aria-hidden className="size-[18px] animate-spin" />
        ) : (
          <Activity aria-hidden className="size-[18px]" />
        )}
        {label}
      </Link>
    );
  }
  return (
    <Link
      to="/console/operations"
      className="bf-icon-button bf-activity-link"
      aria-label={label}
      title={label}
    >
      {busy ? (
        <RefreshCw aria-hidden className="size-[18px] animate-spin" />
      ) : (
        <Activity aria-hidden className="size-[18px]" />
      )}
      {count > 0 && <span className="tabular-nums">{count}</span>}
    </Link>
  );
}
