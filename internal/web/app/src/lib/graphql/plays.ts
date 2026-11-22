// Shared by the Console's play reads and a viewer's own (CORE-1703).
import type { PlayStatsInfo } from "./types";

export const PLAY_FIELDS =
  "profileID profileName mediaID title libraryID libraryTitle deviceID deviceName deviceClient playMethod startedAt watchedSeconds finished";

export const PLAY_STATS_FIELDS =
  "windowDays since until totalPlays totalWatchedSeconds profiles { profileID profileName plays watchedSeconds } items { mediaID title libraryID plays watchedSeconds } devices { deviceID deviceName deviceClient plays watchedSeconds } methods { playMethod plays watchedSeconds } days { day plays watchedSeconds }";

/** The set filters only: the server reads an absent variable as no filter. */
export function playVariables(filter: object): Record<string, unknown> {
  return Object.fromEntries(Object.entries(filter).filter(([, v]) => v !== undefined && v !== ""));
}

/** The browser's current offset from UTC, so days fall on local midnight. */
export function utcOffsetMinutes(): number {
  return -new Date().getTimezoneOffset();
}

/** A stats read with every list present, whatever the server left out. */
export function normalizePlayStats(stats: PlayStatsInfo | null | undefined): PlayStatsInfo {
  return {
    windowDays: stats?.windowDays ?? 0,
    since: stats?.since ?? "",
    until: stats?.until ?? "",
    totalPlays: stats?.totalPlays ?? 0,
    totalWatchedSeconds: stats?.totalWatchedSeconds ?? 0,
    profiles: stats?.profiles ?? [],
    items: stats?.items ?? [],
    devices: stats?.devices ?? [],
    methods: stats?.methods ?? [],
    days: stats?.days ?? [],
  };
}
