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
  fetchDevices,
  fetchLibraries,
  fetchPlayHistory,
  fetchPlayStats,
  fetchProfilesAdmin,
} from "@/lib/graphql";
import type { PlayMethod } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { playDayLabel, playMethodLabel, watched } from "@/lib/playReporting";
import type { StringKey } from "@/strings";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

// Play history and rankings (CORE-1195, CORE-1703): what was played on this
// server, by whom, on which device, how it was delivered, and what gets played
// most. Read from this server's own event log; nothing leaves it.

const ALL = "all";
const WINDOWS = [7, 30, 90] as const;
type WindowDays = (typeof WINDOWS)[number];
const METHODS: PlayMethod[] = ["direct", "remux", "transcode", "unknown"];

/** The filters the rankings and the history table share. */
interface PlayFilters {
  profileID: string;
  libraryID: string;
  deviceID: string;
  playMethod: string;
  /** A local calendar day (yyyy-mm-dd); empty uses the rankings' window. */
  from: string;
  to: string;
}

const NO_FILTERS: PlayFilters = {
  profileID: ALL,
  libraryID: ALL,
  deviceID: ALL,
  playMethod: ALL,
  from: "",
  to: "",
};

/** The filters as read variables: ALL and empty mean no filter. */
function filterVariables(f: PlayFilters) {
  const set = (v: string) => (v === ALL || v === "" ? undefined : v);
  return {
    profileID: set(f.profileID),
    libraryID: set(f.libraryID),
    deviceID: set(f.deviceID),
    playMethod: set(f.playMethod) as PlayMethod | undefined,
    since: dayStart(f.from),
    until: dayStart(f.to, 1),
  };
}

/** The start of a local calendar day (yyyy-mm-dd), `addDays` days on, as RFC 3339. */
export function dayStart(day: string, addDays = 0): string | undefined {
  const [y, m, d] = day.split("-").map(Number);
  if (!y || !m || !d) return undefined;
  return new Date(y, m - 1, d + addDays).toISOString();
}

function FilterSelect({
  id,
  label,
  value,
  onChange,
  all,
  options,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  all: string;
  options: { id: string; name: string }[];
}) {
  return (
    <ManageField id={id} label={label}>
      <Select value={value} onValueChange={onChange}>
        <SelectTrigger id={id} className="w-44">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={ALL}>{all}</SelectItem>
          {options.map((o) => (
            <SelectItem key={o.id} value={o.id}>
              {o.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </ManageField>
  );
}

/** The date range picker and the profile, library, device and play method
 * filters, shared by the rankings and the history table. */
function FilterBar({
  value,
  onChange,
}: {
  value: PlayFilters;
  onChange: (next: PlayFilters) => void;
}) {
  const t = useT();
  const profiles = useAsync(fetchProfilesAdmin);
  const libraries = useAsync(fetchLibraries);
  const devices = useAsync(fetchDevices);
  const set = (key: keyof PlayFilters) => (v: string) => onChange({ ...value, [key]: v });
  return (
    <div className="console-history-filters">
      <FilterSelect
        id="history-profile"
        label={t("profile")}
        value={value.profileID}
        onChange={set("profileID")}
        all={t("playHistoryAllProfiles")}
        options={(profiles.data ?? []).map((p) => ({ id: p.id, name: p.name }))}
      />
      <FilterSelect
        id="history-library"
        label={t("library")}
        value={value.libraryID}
        onChange={set("libraryID")}
        all={t("playHistoryAllLibraries")}
        options={(libraries.data ?? []).map((l) => ({ id: l.id, name: l.title }))}
      />
      <FilterSelect
        id="history-device"
        label={t("playHistoryDevice")}
        value={value.deviceID}
        onChange={set("deviceID")}
        all={t("playHistoryAllDevices")}
        options={(devices.data ?? []).map((d) => ({ id: d.deviceID, name: d.name }))}
      />
      <FilterSelect
        id="history-method"
        label={t("playHistoryMethod")}
        value={value.playMethod}
        onChange={set("playMethod")}
        all={t("playHistoryAllMethods")}
        options={METHODS.map((m) => ({ id: m, name: t(playMethodLabel(m)) }))}
      />
      <ManageField id="history-from" label={t("playHistoryFrom")}>
        <Input
          id="history-from"
          type="date"
          className="w-40"
          value={value.from}
          max={value.to || undefined}
          onChange={(e) => set("from")(e.target.value)}
        />
      </ManageField>
      <ManageField id="history-to" label={t("playHistoryTo")}>
        <Input
          id="history-to"
          type="date"
          className="w-40"
          value={value.to}
          min={value.from || undefined}
          onChange={(e) => set("to")(e.target.value)}
        />
      </ManageField>
    </div>
  );
}

function HistoryTable({ filters }: { filters: PlayFilters }) {
  const t = useT();
  const { data, loading, failed } = useAsync(
    (signal) => fetchPlayHistory(filterVariables(filters), signal),
    [filters],
  );
  const plays = data ?? [];

  return (
    <Section heading="playHistoryHeading" lead="playHistoryLead">
      {loading ? (
        <Skeleton className="h-40 w-full" />
      ) : failed ? (
        <ErrorState message="playHistoryUnavailable" />
      ) : plays.length === 0 ? (
        <EmptyState message="playHistoryEmpty" />
      ) : (
        <DataTable
          columns={[
            { key: "started", label: t("playHistoryStarted") },
            { key: "profile", label: t("profile") },
            { key: "title", label: t("title") },
            { key: "library", label: t("library") },
            { key: "device", label: t("playHistoryDevice") },
            { key: "method", label: t("playHistoryMethod") },
            { key: "watched", label: t("playHistoryWatched") },
            { key: "state", label: "" },
          ]}
        >
          {plays.map((p) => (
            <TableRow key={`${p.profileID}:${p.mediaID}:${p.deviceID ?? ""}:${p.startedAt}`}>
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
              <TableCell>{p.deviceName || t("playHistoryUnknownDevice")}</TableCell>
              <TableCell>{t(playMethodLabel(p.playMethod))}</TableCell>
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
 * written beside it so nothing depends on reading bar length alone. Unnumbered
 * for a list in its own order, such as days. */
function RankList({
  heading,
  rows,
  numbered = true,
}: {
  heading: StringKey;
  rows: Rank[];
  numbered?: boolean;
}) {
  const t = useT();
  const most = Math.max(1, ...rows.map((r) => r.plays));
  return (
    <div className="console-card console-card-pad">
      <h3 className="console-chart-title">{t(heading)}</h3>
      <ol className="console-rank-list">
        {rows.map((row, i) => (
          <li key={row.id} className="console-rank-row">
            <span className="console-rank-pos console-mono">{numbered ? i + 1 : ""}</span>
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

function Rankings({ filters, onClearRange }: { filters: PlayFilters; onClearRange: () => void }) {
  const t = useT();
  const [days, setDays] = useState<WindowDays>(30);
  // A chosen From day overrides the quick window.
  const ranged = filters.from !== "";
  const { data, loading, failed } = useAsync(
    (signal) => {
      const { since, until, ...rest } = filterVariables(filters);
      return fetchPlayStats(
        ranged ? { ...rest, since, until } : { ...rest, windowDays: days },
        signal,
      );
    },
    [filters, days, ranged],
  );
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
  const devices = (data?.devices ?? []).map((d) => ({
    id: d.deviceID || "-",
    name: d.deviceName || t("playHistoryUnknownDevice"),
    plays: d.plays,
    watchedSeconds: d.watchedSeconds,
  }));
  const methods = (data?.methods ?? []).map((m) => ({
    id: m.playMethod,
    name: t(playMethodLabel(m.playMethod)),
    plays: m.plays,
    watchedSeconds: m.watchedSeconds,
  }));
  const byDay = (data?.days ?? []).map((d) => ({
    id: d.day,
    name: playDayLabel(d.day),
    plays: d.plays,
    watchedSeconds: d.watchedSeconds,
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
              variant={!ranged && w === days ? "default" : "outline"}
              aria-pressed={!ranged && w === days}
              onClick={() => {
                setDays(w);
                if (ranged) onClearRange();
              }}
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
      ) : !data || data.totalPlays === 0 ? (
        <EmptyState message="playStatsEmpty" />
      ) : (
        <>
          <p className="console-play-totals console-mono">
            {t("playStatsTotal")}: {t("playStatsPlays", { count: data.totalPlays })} ·{" "}
            {watched(data.totalWatchedSeconds)}
          </p>
          <div className="console-rank-grid">
            <RankList heading="playStatsTopUsers" rows={users} />
            <RankList heading="playStatsMostPlayed" rows={items} />
            <RankList heading="playStatsDevices" rows={devices} />
            <RankList heading="playStatsMethods" rows={methods} />
            <RankList heading="playStatsByDay" rows={byDay} numbered={false} />
          </div>
        </>
      )}
    </Section>
  );
}

export function PlayHistory() {
  const [filters, setFilters] = useState<PlayFilters>(NO_FILTERS);
  return (
    <div className="space-y-8">
      <FilterBar value={filters} onChange={setFilters} />
      <Rankings
        filters={filters}
        onClearRange={() => setFilters({ ...filters, from: "", to: "" })}
      />
      <HistoryTable filters={filters} />
    </div>
  );
}
