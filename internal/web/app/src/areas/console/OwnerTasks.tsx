import {
  Activity,
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
import { cn } from "@/lib/utils";
import { Section } from "./Section";
import { useAsync } from "./useAsync";

// Console home task list (CORE-1131): the owner's common jobs as tiles with
// live status, in the setup wizard's tile style (CORE-1114). Each status line
// is best-effort; a failed read just leaves the tile without a chip.

const TILE =
  "group flex flex-col gap-3 rounded-xl border border-white/10 bg-white/[0.03] p-4 transition-all duration-200";
const LINK_TILE = `${TILE} hover:border-white/25 hover:bg-white/[0.06] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary`;

function TileHead({
  icon: Icon,
  title,
  status,
}: {
  icon: LucideIcon;
  title: string;
  status?: ReactNode;
}) {
  return (
    <div className="flex items-start gap-3">
      <span className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-primary/15 text-primary ring-1 ring-primary/25 group-hover:shadow-[0_8px_24px_-10px_var(--accent)]">
        <Icon aria-hidden className="size-5" />
      </span>
      <div className="min-w-0 flex-1">
        <h3 className="m-0 text-sm font-semibold">{title}</h3>
        {status && <div className="mt-1">{status}</div>}
      </div>
    </div>
  );
}

function TaskLink({
  to,
  icon,
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
    <Link to={to} className={cn(LINK_TILE, "text-foreground no-underline")}>
      <TileHead icon={icon} title={title} status={status} />
      <p className="m-0 text-[12.5px] leading-snug text-muted-foreground">{lead}</p>
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
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        <div className={TILE}>
          <TileHead
            icon={RefreshCw}
            title={t("taskRescan")}
            status={
              scanning ? (
                <StatusChip tone="cyan">{t("rescanScanning")}</StatusChip>
              ) : libraries.data ? (
                <span className="text-xs text-muted-foreground">
                  {t("taskRescanCount", { count: libraries.data.length })}
                </span>
              ) : null
            }
          />
          <p className="m-0 text-[12.5px] leading-snug text-muted-foreground">
            {allWatched ? t("autoScanAll") : t("taskRescanLead")}
          </p>
          <div>
            <RescanAllButton libraries={libraries.data ?? []} variant="default" />
          </div>
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
