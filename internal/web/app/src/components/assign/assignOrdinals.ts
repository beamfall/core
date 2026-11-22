import type { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";

export interface Ordinal {
  sequence: string;
  index: number;
}

export const maxOrdinalIndex = 2147483647;

export function ordinalIndex(value: string) {
  const index = Number(value);
  return /^\d+$/.test(value) && index <= maxOrdinalIndex ? index : undefined;
}

export function itemIndex(item: { title: string; index?: number }) {
  if (item.index != null) return item.index;
  const number = item.title.match(/s\d+e(\d+)/i)?.[1] ?? item.title.match(/(\d+)\s*$/)?.[1];
  return number === undefined ? undefined : ordinalIndex(number);
}

export function lockedOrdinal(value: string): Ordinal | undefined {
  const separator = value.lastIndexOf(" / ");
  const index = ordinalIndex(separator < 0 ? value : value.slice(separator + 3));
  if (index === undefined) return undefined;
  return { sequence: separator < 0 ? "" : value.slice(0, separator), index };
}

const levelKeys: Record<string, StringKey> = {
  season: "assignLevelSeason",
  episode: "assignLevelEpisode",
  disc: "assignLevelDisc",
  track: "assignLevelTrack",
  release: "assignLevelRelease",
};

export function levelLabel(level: string, t: ReturnType<typeof useT>) {
  if (Object.hasOwn(levelKeys, level)) return t(levelKeys[level]);
  const words = level.replaceAll("-", " ");
  return words.charAt(0).toUpperCase() + words.slice(1);
}

export function ordinalLabels(levels: readonly string[], t: ReturnType<typeof useT>) {
  return {
    sequence: levels.length > 1 ? levelLabel(levels.at(-2)!, t) : t("assignSequence"),
    index: levels.length ? levelLabel(levels.at(-1)!, t) : t("assignIndex"),
  };
}

export function formatOrdinal(ordinal: Ordinal, labels: ReturnType<typeof ordinalLabels>) {
  const index = `${labels.index} ${ordinal.index}`;
  return ordinal.sequence ? `${labels.sequence} ${ordinal.sequence} · ${index}` : index;
}
