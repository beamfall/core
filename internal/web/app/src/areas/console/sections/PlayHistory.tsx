import { History } from "lucide-react";
import { useState } from "react";
import {
  DataTable,
  EmptyState,
  ErrorState,
  ManageField,
  StatusChip,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { TableCell, TableRow } from "@/components/ui/table";
import {
  fetchLibraries,
  fetchPlayHistory,
  fetchPlayStats,
  fetchProfilesAdmin,
} from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

// Play history and rankings (CORE-1195): what was played on this server, by
// whom, and what gets played most. Read from this server's own event log;
// nothing leaves it.

const ALL = "all";
const WINDOWS = [7, 30, 90] as const;
type WindowDays = (typeof WINDOWS)[number];

/** Seconds as a short duration: 2 h 05 min, 14 min, 40 s. */
export function watched(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (h > 0) return `${h} h ${String(m).padStart(2, "0")} min`;
  if (m > 0) return `${m} min`;
  return `${s} s`;
}

/** The start of a local calendar day (yyyy-mm-dd), `addDays` days on, as RFC 3339. */
export function dayStart(day: string, addDays = 0): string | undefined {
  const [y, m, d] = day.split("-").map(Number);
  if (!y || !m || !d) return undefined;
  return new Date(y, m - 1, d + addDays).toISOString();
}

function HistoryTable() {
  const t = useT();
  const [profileID, setProfileID] = useState(ALL);
  const [libraryID, setLibraryID] = useState(ALL);
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const profiles = useAsync(fetchProfilesAdmin);
  const libraries = useAsync(fetchLibraries);
  const { data, loading, failed } = useAsync(
    (signal) =>
      fetchPlayHistory(
        {
          profileID: profileID === ALL ? undefined : profileID,
          libraryID: libraryID === ALL ? undefined : libraryID,
          since: dayStart(from),
          until: dayStart(to, 1),
        },
        signal,
      ),
    [profileID, libraryID, from, to],
  );
  const plays = data ?? [];

  return (
    <Section heading="playHistoryHeading" lead="playHistoryLead">
      <div className="console-history-filters">
        <ManageField id="history-profile" label={t("profile")}>
          <Select value={profileID} onValueChange={setProfileID}>
            <SelectTrigger id="history-profile" className="w-44">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>{t("playHistoryAllProfiles")}</SelectItem>
              {(profiles.data ?? []).map((p) => (
                <SelectItem key={p.id} value={p.id}>
                  {p.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </ManageField>
        <ManageField id="history-library" label={t("library")}>
          <Select value={libraryID} onValueChange={setLibraryID}>
            <SelectTrigger id="history-library" className="w-44">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>{t("playHistoryAllLibraries")}</SelectItem>
              {(libraries.data ?? []).map((l) => (
                <SelectItem key={l.id} value={l.id}>
                  {l.title}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </ManageField>
        <ManageField id="history-from" label={t("playHistoryFrom")}>
          <Input
            id="history-from"
            type="date"
            className="w-40"
            value={from}
            max={to || undefined}
            onChange={(e) => setFrom(e.target.value)}
          />
        </ManageField>
        <ManageField id="history-to" label={t("playHistoryTo")}>
          <Input
            id="history-to"
            type="date"
            className="w-40"
            value={to}
            min={from || undefined}
            onChange={(e) => setTo(e.target.value)}
          />
        </ManageField>
      </div>
      {loading ? (
        <Skeleton className="h-40 w-full" />
      ) : failed ? (
        <ErrorState message="playHistoryUnavailable" />
      ) : plays.length === 0 ? (
        <EmptyState message="playHistoryEmpty" icon={History} />
      ) : (
        <DataTable
          columns={[
            { key: "started", label: t("playHistoryStarted") },
            { key: "profile", label: t("profile") },
            { key: "title", label: t("title") },
            { key: "library", label: t("library") },
            { key: "watched", label: t("playHistoryWatched") },
            { key: "state", label: "" },
          ]}
        >
          {plays.map((p) => (
            <TableRow key={`${p.profileID}:${p.mediaID}:${p.startedAt}`}>
              <TableCell className="console-mono">
                {new Date(p.startedAt).toLocaleString([], {
                  dateStyle: "medium",
                  timeStyle: "short",
                })}
              </TableCell>
              <TableCell>{p.profileName}</TableCell>
              <TableCell className="font-medium">
                {p.title ? (
                  p.title
                ) : (
                  <StatusChip tone="iris">{t("liveSessionRestricted")}</StatusChip>
                )}
              </TableCell>
              <TableCell>{p.libraryTitle}</TableCell>
              <TableCell className="console-mono">{watched(p.watchedSeconds)}</TableCell>
              <TableCell className="text-right">
                <StatusChip tone={p.finished ? "green" : "neutral"}>
                  {t(p.finished ? "playHistoryFinished" : "playHistoryPartial")}
                </StatusChip>
              </TableCell>
            </TableRow>
          ))}
        </DataTable>
      )}
    </Section>
  );
}

interface Rank {
  id: string;
  name: string;
  plays: number;
  watchedSeconds: number;
}

/** A ranked list: one thin bar per row, sized by plays, with the figures
 * written beside it so nothing depends on reading bar length alone. */
function RankList({ heading, rows }: { heading: StringKey; rows: Rank[] }) {
  const t = useT();
  const most = Math.max(1, ...rows.map((r) => r.plays));
  return (
    <div className="console-card console-card-pad">
      <h3 className="console-chart-title">{t(heading)}</h3>
      <ol className="console-rank-list">
        {rows.map((row, i) => (
          <li key={row.id} className="console-rank-row">
            <span className="console-rank-pos console-mono">{i + 1}</span>
            <span className="console-rank-name">{row.name}</span>
            <span className="console-rank-bar" aria-hidden>
              <i style={{ width: `${(row.plays / most) * 100}%` }} />
            </span>
            <span className="console-rank-num console-mono">
              {t("playStatsPlays", { count: row.plays })} · {watched(row.watchedSeconds)}
            </span>
          </li>
        ))}
      </ol>
    </div>
  );
}

function Rankings() {
  const t = useT();
  const [days, setDays] = useState<WindowDays>(30);
  const { data, loading, failed } = useAsync((signal) => fetchPlayStats(days, signal), [days]);
  const users = (data?.profiles ?? []).map((p) => ({
    id: p.profileID,
    name: p.profileName,
    plays: p.plays,
    watchedSeconds: p.watchedSeconds,
  }));
  const items = (data?.items ?? []).map((m) => ({
    id: m.mediaID,
    name: m.title,
    plays: m.plays,
    watchedSeconds: m.watchedSeconds,
  }));

  return (
    <Section
      heading="playStatsHeading"
      lead="playStatsLead"
      actions={
        <div className="console-window-toggle">
          {WINDOWS.map((w) => (
            <Button
              key={w}
              size="sm"
              variant={w === days ? "default" : "outline"}
              aria-pressed={w === days}
              onClick={() => setDays(w)}
            >
              {t("playStatsWindow", { days: w })}
            </Button>
          ))}
        </div>
      }
    >
      {loading ? (
        <Skeleton className="h-40 w-full" />
      ) : failed ? (
        <ErrorState message="playStatsUnavailable" />
      ) : users.length === 0 && items.length === 0 ? (
        <EmptyState message="playStatsEmpty" icon={History} />
      ) : (
        <div className="console-rank-grid">
          <RankList heading="playStatsTopUsers" rows={users} />
          <RankList heading="playStatsMostPlayed" rows={items} />
        </div>
      )}
    </Section>
  );
}

export function PlayHistory() {
  return (
    <div className="space-y-8">
      <Rankings />
      <HistoryTable />
    </div>
  );
}
