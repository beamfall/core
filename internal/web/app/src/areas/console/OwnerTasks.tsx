import {
  Activity,
  ChevronRight,
  FolderPlus,
  KeyRound,
  type LucideIcon,
  RefreshCw,
  SearchCheck,
  Users,
} from "lucide-react";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { RescanAllButton, useAnyRescanActive } from "@/components/beamfall/LibraryRescan";
import { StatusChip } from "@/components/beamfall/workbench";
import {
  fetchConsoleConfig,
  fetchLibraries,
  fetchUnmatchedQueue,
  fetchWatcherStatus,
} from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { groupProviderKeys, keyNeeded } from "@/lib/providerKeys";
import { Section } from "./Section";
import { useAsync } from "./useAsync";

// Console home task list (CORE-1131): the owner's common jobs as rows with
// live status. Each status line is best-effort; a failed read just leaves the
// row without one.

function TaskText({ title, lead }: { title: string; lead: string }) {
  return (
    <span className="console-task-text">
      <span className="console-task-title">{title}</span>
      <span className="console-task-lead">{lead}</span>
    </span>
  );
}

function TaskLink({
  to,
  icon: Icon,
  title,
  lead,
  status,
}: {
  to: string;
  icon: LucideIcon;
  title: string;
  lead: string;
  status?: ReactNode;
}) {
  return (
    <Link to={to} className="console-task">
      <Icon className="console-task-icon" aria-hidden />
      <TaskText title={title} lead={lead} />
      <span className="console-task-end">
        {status}
        <ChevronRight className="size-4" aria-hidden />
      </span>
    </Link>
  );
}

export function OwnerTasks() {
  const t = useT();
  const scanning = useAnyRescanActive();
  const libraries = useAsync(fetchLibraries);
  const unmatched = useAsync(fetchUnmatchedQueue);
  const config = useAsync(fetchConsoleConfig);
  const watchers = useAsync(fetchWatcherStatus);
  const allWatched =
    (watchers.data ?? []).length > 0 &&
    (watchers.data ?? []).every((watcher) => watcher.state === "watching");

  const neededKeys = config.data
    ? config.data.plugins
        .filter((plugin) => plugin.active)
        .flatMap((plugin) => groupProviderKeys(plugin.scrapers ?? []))
        .filter(keyNeeded).length
    : null;

  return (
    <Section heading="ownerTasksHeading" lead="ownerTasksLead">
      <div className="console-task-list">
        <div className="console-task">
          <RefreshCw className="console-task-icon" aria-hidden />
          <TaskText
            title={t("taskRescan")}
            lead={allWatched ? t("autoScanAll") : t("taskRescanLead")}
          />
          <span className="console-task-end">
            {scanning ? (
              <StatusChip tone="cyan">{t("rescanScanning")}</StatusChip>
            ) : (
              libraries.data && (
                <span className="hidden text-[12.5px] sm:inline">
                  {t("taskRescanCount", { count: libraries.data.length })}
                </span>
              )
            )}
            <RescanAllButton libraries={libraries.data ?? []} variant="outline" />
          </span>
        </div>
        <TaskLink
          to="/console/libraries"
          icon={FolderPlus}
          title={t("taskAddLibrary")}
          lead={t("taskAddLibraryLead")}
        />
        <TaskLink
          to="/manage/unmatched"
          icon={SearchCheck}
          title={t("fixUnmatched")}
          lead={t("taskUnmatchedLead")}
          status={
            unmatched.data && (
              <StatusChip tone={unmatched.data.length > 0 ? "amber" : "green"}>
                {t("taskUnmatchedCount", { count: unmatched.data.length })}
              </StatusChip>
            )
          }
        />
        <TaskLink
          to="/console/plugins"
          icon={KeyRound}
          title={t("taskProviderKeys")}
          lead={t("taskProviderKeysLead")}
          status={
            neededKeys !== null && (
              <StatusChip tone={neededKeys > 0 ? "amber" : "green"}>
                {neededKeys > 0
                  ? t("taskProviderKeysNeeded", { count: neededKeys })
                  : t("taskProviderKeysOk")}
              </StatusChip>
            )
          }
        />
        <TaskLink
          to="/console/access"
          icon={Users}
          title={t("taskPeople")}
          lead={t("taskPeopleLead")}
        />
        <TaskLink
          to="/console/operations"
          icon={Activity}
          title={t("taskActivity")}
          lead={t("taskActivityLead")}
          status={scanning ? <StatusChip tone="cyan">{t("rescanScanning")}</StatusChip> : undefined}
        />
      </div>
    </Section>
  );
}
