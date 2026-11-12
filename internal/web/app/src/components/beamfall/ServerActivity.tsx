import { Activity, RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { Link } from "react-router";
import { useAnyRescanActive } from "@/components/beamfall/LibraryRescan";
import { fetchJobs } from "@/lib/graphql";
import { useT } from "@/lib/i18n";

// Header activity link for managers (CORE-1131): one click from any page to
// the job queue, with a live count while jobs run. The jobs read needs an Admin
// Session, so while the Console is locked the count is unknown (null), never a
// claimed zero; unlocking it brings the count back on the next poll.
//
// The jobs read returns the whole job history (the wire has no status filter,
// limit or running count), and this header sits on every page, so the read is
// kept rare: nothing is read while the tab is hidden, an idle server is asked
// only every IDLE_POLL_MS, and the fast cadence runs only while work is known
// to be under way.

const ACTIVE_POLL_MS = 5000;
const IDLE_POLL_MS = 30000;

export function ServerActivity() {
  const t = useT();
  const rescanning = useAnyRescanActive();
  const [running, setRunning] = useState<number | null>(null);

  // A rescan starting on this page restarts the effect, so its job is counted
  // at once and followed at the active cadence.
  useEffect(() => {
    let cancelled = false;
    let timer: number | undefined;
    let reading = false;
    async function refresh() {
      window.clearTimeout(timer);
      // A hidden tab reads nothing and schedules nothing; becoming visible
      // again starts the next read.
      if (reading || document.visibilityState === "hidden") return;
      reading = true;
      let count: number | null = null;
      try {
        const jobs = await fetchJobs();
        count = jobs.filter((job) => job.status === "running").length;
      } catch {
        // unknown: the read was refused or failed
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
