import { Activity, RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { Link } from "react-router";
import { useAnyRescanActive } from "@/components/beamfall/LibraryRescan";
import { fetchJobs } from "@/lib/graphql";
import { useT } from "@/lib/i18n";

// Header activity link for managers (CORE-1131): one click from any page to
// the job queue, with a live count while jobs run. Polls only while the tab is
// visible; a failed read just hides the count.

const POLL_MS = 5000;

export function ServerActivity() {
  const t = useT();
  const rescanning = useAnyRescanActive();
  const [running, setRunning] = useState(0);

  useEffect(() => {
    let cancelled = false;
    async function refresh() {
      if (document.visibilityState === "hidden") return;
      try {
        const jobs = await fetchJobs();
        if (!cancelled) setRunning(jobs.filter((job) => job.status === "running").length);
      } catch {
        if (!cancelled) setRunning(0);
      }
    }
    void refresh();
    const timer = window.setInterval(() => void refresh(), POLL_MS);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, []);

  const busy = rescanning || running > 0;
  const label = running > 0 ? t("serverActivityRunning", { count: running }) : t("serverActivity");
  return (
    <Link to="/console/operations" className="bf-action-link" aria-label={label} title={label}>
      {busy ? (
        <RefreshCw aria-hidden className="size-4 animate-spin" />
      ) : (
        <Activity aria-hidden className="size-4" />
      )}
      {running > 0 && <span className="ml-1 tabular-nums">{running}</span>}
    </Link>
  );
}
