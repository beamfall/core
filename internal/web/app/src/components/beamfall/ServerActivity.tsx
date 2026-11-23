import { Activity, RefreshCw } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link, useLocation } from "react-router";
import { useAnyRescanActive } from "@/components/beamfall/LibraryRescan";
import { fetchAdminSession, fetchJobs } from "@/lib/graphql";
import { useT } from "@/lib/i18n";

// Header activity link for managers (CORE-1131): one click from any page to
// the job queue, with a live count while jobs run. Both the jobs read and
// `/console/operations` need an Admin Session, so while the Console is locked
// or its Admin Session has expired the link is hidden rather than leading to a
// password prompt (CORE-1157); unlocking brings it back on the next poll.
// Without a known-active Admin Session the poll asks only for the session's
// standing (a plain session read) and skips the jobs read the server would
// refuse (CORE-2318); a known session is trusted until its expiry or a refused
// read.
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
  // Fires at the known expiry whatever read is in flight, so the link never
  // outlives the Admin Session while a slow read is pending (CORE-1157). It
  // lives outside the poll effect so a rescan starting or ending just before
  // expiry (which re-runs that effect) does not cancel it.
  const expiryTimer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(expiryTimer.current), []);

  // A rescan starting on this page restarts the effect, so its job is counted
  // at once and followed at the active cadence.
  useEffect(() => {
    let cancelled = false;
    let timer: number | undefined;
    let reading = false;
    // Epoch ms until which the Admin Session is known active; 0 when unknown.
    let adminUntil = 0;
    let adminExpiresAt: number | null = null;
    async function adminActive(): Promise<boolean> {
      const now = Date.now();
      if (now < adminUntil) return true;
      adminUntil = 0;
      adminExpiresAt = null;
      const session = await fetchAdminSession();
      const settled = Date.now();
      const expires = session.expiresAt ? Date.parse(session.expiresAt) : Number.NaN;
      if (!session.active || (!Number.isNaN(expires) && expires <= settled)) {
        return false;
      }
      adminExpiresAt = Number.isNaN(expires) ? null : expires;
      window.clearTimeout(expiryTimer.current);
      if (adminExpiresAt !== null) {
        expiryTimer.current = window.setTimeout(() => setRunning(null), adminExpiresAt - settled);
      }
      adminUntil = adminExpiresAt ?? settled + IDLE_POLL_MS;
      return true;
    }
    async function refresh() {
      window.clearTimeout(timer);
      // A hidden tab reads nothing and schedules nothing; becoming visible
      // again starts the next read.
      if (reading || document.visibilityState === "hidden") return;
      // At the known expiry the link goes at once; the session check that
      // follows only decides whether it comes back (CORE-1157).
      if (adminExpiresAt !== null && Date.now() >= adminExpiresAt) setRunning(null);
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
        adminExpiresAt = null;
      }
      reading = false;
      if (cancelled) return;
      setRunning(count);
      const active = rescanning || (count ?? 0) > 0;
      const pollMs = active ? ACTIVE_POLL_MS : IDLE_POLL_MS;
      const delay =
        adminExpiresAt !== null
          ? Math.min(pollMs, Math.max(0, adminExpiresAt - Date.now()))
          : pollMs;
      timer = window.setTimeout(() => void refresh(), delay);
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

  if (running === null) return null;

  const busy = rescanning || running > 0;
  const label = running > 0 ? t("serverActivityRunning", { count: running }) : t("serverActivity");
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
      {running > 0 && <span className="tabular-nums">{running}</span>}
    </Link>
  );
}
