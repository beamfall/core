import { Activity, RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { Link } from "react-router";
import { useAnyRescanActive } from "@/components/beamfall/LibraryRescan";
import { fetchJobs } from "@/lib/graphql";
import { useT } from "@/lib/i18n";

// Header activity link for managers (CORE-1131): one click from any page to
// the job queue, with a live count while jobs run. Polls only while the tab is
// visible. The jobs read needs an Admin Session, so while the Console is
// locked the count is unknown (null), never a claimed zero; unlocking it
// brings the count back on the next poll.

const POLL_MS = 5000;

export function ServerActivity() {
  const t = useT();
  const rescanning = useAnyRescanActive();
  const [running, setRunning] = useState<number | null>(null);

  useEffect(() => {
    let cancelled = false;
    async function refresh() {
      if (document.visibilityState === "hidden") return;
      try {
        const jobs = await fetchJobs();
        if (!cancelled) setRunning(jobs.filter((job) => job.status === "running").length);
      } catch {
        if (!cancelled) setRunning(null);
      }
    }
    void refresh();
    const timer = window.setInterval(() => void refresh(), POLL_MS);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, []);

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
