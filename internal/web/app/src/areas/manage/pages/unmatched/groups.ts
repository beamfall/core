import type { UnmatchedItemInfo } from "@/lib/graphql/types";

/**
 * An UnmatchedGroup is every queued row that failed the same way against the
 * same best guess: one plugin and scraper, one candidate (or candidate title
 * when the scraper returned no id). Items under one parent folder usually land
 * in one group, so one fix can resolve all of them.
 */
export interface UnmatchedGroup {
  key: string;
  pluginID: string;
  scraperID: string;
  candidateID: string;
  candidateTitle: string;
  /** The taxonomy word most rows in the group carry ("" when none did). */
  reason: string;
  /** The best confidence any row in the group reached. */
  confidence: number;
  threshold: number;
  items: UnmatchedItemInfo[];
}

/** groupKey is the identity rows must share to be fixed together. */
export function groupKey(item: UnmatchedItemInfo): string {
  const guess = item.candidateID
    ? `id:${item.candidateID}`
    : item.candidateTitle
      ? `title:${item.candidateTitle}`
      : `media:${item.mediaID}`;
  return `${item.pluginID}\u0000${item.scraperID}\u0000${guess}`;
}

/**
 * groupUnmatched folds queue rows into groups, largest first, keeping queue
 * order inside a group and between groups of equal size.
 */
export function groupUnmatched(items: UnmatchedItemInfo[]): UnmatchedGroup[] {
  const byKey = new Map<string, UnmatchedGroup>();
  for (const item of items) {
    const key = groupKey(item);
    let group = byKey.get(key);
    if (!group) {
      group = {
        key,
        pluginID: item.pluginID,
        scraperID: item.scraperID,
        candidateID: item.candidateID ?? "",
        candidateTitle: item.candidateTitle ?? "",
        reason: "",
        confidence: item.confidence,
        threshold: item.threshold,
        items: [],
      };
      byKey.set(key, group);
    }
    group.items.push(item);
    group.confidence = Math.max(group.confidence, item.confidence);
  }
  const groups = [...byKey.values()];
  for (const group of groups) group.reason = commonReason(group.items);
  return groups
    .map((group, index) => ({ group, index }))
    .sort((a, b) => b.group.items.length - a.group.items.length || a.index - b.index)
    .map(({ group }) => group);
}

function commonReason(items: UnmatchedItemInfo[]): string {
  const counts = new Map<string, number>();
  for (const item of items) {
    const reason = item.failureReason ?? "";
    counts.set(reason, (counts.get(reason) ?? 0) + 1);
  }
  let best = "";
  let bestCount = 0;
  for (const [reason, count] of counts) {
    if (count > bestCount) {
      best = reason;
      bestCount = count;
    }
  }
  return best;
}

/** What a folder name says about the entry its files belong to. */
export interface FolderHint {
  /** The folder name as it appears on disk. */
  folder: string;
  /** The name with the year, qualifier and any id tag removed. */
  title: string;
  year?: number;
  /** A bracketed non-year word such as "US" that can lower a title match. */
  qualifier?: string;
}

const YEAR = /[([]\s*((?:18|19|20)\d{2})\s*[)\]]/;
const BARE_TRAILING_YEAR = /[\s._-]((?:18|19|20)\d{2})$/;
const QUALIFIER = /[([]\s*([^()[\]\d][^()[\]]{0,15}?)\s*[)\]]/;
const ID_TAG = /[{[][a-z]+[-=][^}\]]+[}\]]/gi;
// "Disc 2", "Part 03", "S01": a folder that only numbers its siblings says
// nothing about the entry's name, so the walk goes one level higher.
const NUMBERED_SUBFOLDER = /^\p{L}{1,12}[\s._-]*\d{1,3}$/u;

/**
 * folderHint reads the nearest ancestor folder of a file path that names the
 * entry: the first one carrying a year, else the first that is not a numbered
 * subfolder. It returns undefined for a path with no usable folder.
 */
export function folderHint(path: string | undefined): FolderHint | undefined {
  if (!path) return undefined;
  const parts = path.split(/[\\/]+/).filter(Boolean);
  const folders = parts.slice(0, -1).reverse().slice(0, 3);
  const folder =
    folders.find((name) => parseFolderName(name).year !== undefined) ??
    folders.find((name) => !NUMBERED_SUBFOLDER.test(name));
  if (!folder) return undefined;
  return parseFolderName(folder);
}

/** parseFolderName splits one folder name into title, year and qualifier. */
export function parseFolderName(folder: string): FolderHint {
  let rest = folder.replace(ID_TAG, " ");
  let year: number | undefined;
  const bracketed = rest.match(YEAR);
  if (bracketed) {
    year = Number(bracketed[1]);
    rest = rest.replace(bracketed[0], " ");
  } else {
    const bare = rest.trim().match(BARE_TRAILING_YEAR);
    if (bare) {
      year = Number(bare[1]);
      rest = rest.trim().slice(0, -bare[0].length);
    }
  }
  let qualifier: string | undefined;
  const q = rest.match(QUALIFIER);
  if (q) {
    qualifier = q[1].trim();
    rest = rest.replace(q[0], " ");
  }
  const title = rest.replace(/[._]+/g, " ").replace(/\s+/g, " ").trim();
  return { folder, title: title || folder, year, qualifier };
}
