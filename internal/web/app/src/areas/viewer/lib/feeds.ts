import { useEffect, useState } from "react";
import { ApiError, graphqlErrorCode } from "@/lib/api";
import { isCompatibilityRefusal } from "@/lib/compat";
import {
  type FeedSubscription,
  libraryHasFeedSubscriptions,
  type SubscriptionEntry,
  type SubscriptionHealth,
} from "@/lib/graphql";
import type { StringKey } from "@/strings";

// Display rules for followed feeds (CORE-2957): download policies, health
// (CORE-2955 failure classes) and catalogue availability. Words come from the
// strings catalogue; the wire values stay the server's closed vocabularies.

/** The latest-N count the server applies when a follow names none. */
export const DEFAULT_LATEST_N = 3;

/** Policies a follow can start with. Everything needs a confirmation that
 * shows the catalogue size, so it is offered only on a followed feed. */
export const FOLLOW_POLICIES = ["latest", "new-only", "stream-only"] as const;
export const ALL_POLICIES = ["latest", "new-only", "everything", "stream-only"] as const;

const POLICY_LABELS: Record<string, StringKey> = {
  latest: "feedsPolicyLatest",
  "new-only": "feedsPolicyNewOnly",
  everything: "feedsPolicyEverything",
  "stream-only": "feedsPolicyStreamOnly",
};

const POLICY_HINTS: Record<string, StringKey> = {
  latest: "feedsPolicyLatestHint",
  "new-only": "feedsPolicyNewOnlyHint",
  everything: "feedsPolicyEverythingHint",
  "stream-only": "feedsPolicyStreamOnlyHint",
};

export function policyLabelKey(policy: string): StringKey {
  return POLICY_LABELS[policy] ?? "feedsPolicyLatest";
}

export function policyHintKey(policy: string): StringKey {
  return POLICY_HINTS[policy] ?? "feedsPolicyLatestHint";
}

const ERROR_CLASS_LABELS: Record<string, StringKey> = {
  dns: "feedsErrorDNS",
  tls: "feedsErrorTLS",
  "http-4xx": "feedsErrorHTTP4xx",
  "http-5xx": "feedsErrorHTTP5xx",
  parse: "feedsErrorParse",
  "ssrf-blocked": "feedsErrorSSRF",
  auth: "feedsErrorAuth",
  gone: "feedsErrorGone",
  network: "feedsErrorNetwork",
};

/** The plain-words cause of a failing feed, by its lastErrorClass. */
export function errorClassKey(errorClass: string | null | undefined): StringKey {
  return (errorClass && ERROR_CLASS_LABELS[errorClass]) || "feedsErrorUnknown";
}

/** The cause class of a refused follow: the server answers "feed could not
 * be read: <class>" with the same classes as lastErrorClass. "" when the
 * error carries no known class. */
export function followErrorClass(err: unknown): string {
  const message = err instanceof Error ? err.message : "";
  const match = /feed could not be read: ([a-z0-9-]+)/.exec(message);
  return match && ERROR_CLASS_LABELS[match[1]] ? match[1] : "";
}

/** How loudly a feed's health shows: quiet for ok and pending, a failure
 * line for failing, amber for warning, its own state for retired. */
export type HealthTone = "quiet" | "failing" | "warning" | "retired";

export function healthTone(health: SubscriptionHealth | null | undefined): HealthTone {
  switch (health?.healthStatus) {
    case "failing":
      return "failing";
    case "warning":
      return "warning";
    case "retired":
      return "retired";
    default:
      return "quiet";
  }
}

/** The availability badge of one catalogue entry. */
export function entryAvailabilityKey(entry: SubscriptionEntry): StringKey {
  if (entry.availability === "downloaded") return "feedsDownloaded";
  if (entry.downloadState === "queued") return "feedsDownloading";
  return "feedsNotDownloaded";
}

export const CATALOGUE_FILTERS = ["all", "unplayed", "downloaded"] as const;
export type CatalogueFilter = (typeof CATALOGUE_FILTERS)[number];
export const CATALOGUE_SORTS = ["newest", "oldest"] as const;
export type CatalogueSort = (typeof CATALOGUE_SORTS)[number];
export const NEW_ENTRY_ROUTES = ["new", "queue-end", "queue-front"] as const;

/** The catalogue under one filter and sort (CORE-2959). Newest puts dated
 * entries newest first, then undated ones in feed order (feeds publish newest
 * first); Oldest reverses it, so a serial reads from its start. */
export function catalogueView(
  entries: readonly SubscriptionEntry[],
  filter: CatalogueFilter,
  sort: CatalogueSort,
): SubscriptionEntry[] {
  const kept = entries.filter((entry) => {
    if (filter === "unplayed") return !entry.played;
    if (filter === "downloaded") return entry.availability === "downloaded";
    return true;
  });
  const newestFirst = [...kept].sort((a, b) => {
    const at = parseWireTime(a.publishedAt)?.getTime();
    const bt = parseWireTime(b.publishedAt)?.getTime();
    if (at !== undefined && bt !== undefined && at !== bt) return bt - at;
    if ((at === undefined) !== (bt === undefined)) return at === undefined ? 1 : -1;
    return a.feedPosition - b.feedPosition;
  });
  return sort === "oldest" ? newestFirst.reverse() : newestFirst;
}

const BYTE_UNITS = ["byte", "kilobyte", "megabyte", "gigabyte", "terabyte"] as const;

/** A byte count in the display locale's words ("41 GB"); "" when unknown. */
export function formatBytes(bytes: number, locale: string): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "";
  let value = bytes;
  let unit = 0;
  while (value >= 1000 && unit < BYTE_UNITS.length - 1) {
    value /= 1000;
    unit += 1;
  }
  return new Intl.NumberFormat(locale, {
    style: "unit",
    unit: BYTE_UNITS[unit],
    unitDisplay: "short",
    maximumFractionDigits: value < 10 && unit > 0 ? 1 : 0,
  }).format(value);
}

/** A wire timestamp as a Date, or null when absent or unparsable. */
export function parseWireTime(value: string | null | undefined): Date | null {
  if (!value) return null;
  const at = new Date(value);
  return Number.isNaN(at.getTime()) ? null : at;
}

/** Whether a refresh was refused for coming too soon after the last poll. */
export function isRateLimited(err: unknown): boolean {
  if (graphqlErrorCode(err) === "THROTTLED") return true;
  return err instanceof ApiError && /throttled|rate.?limit|too soon/i.test(err.message);
}

/** Whether a read was refused for this device's role (a non-manager device). */
export function isForbidden(err: unknown): boolean {
  if (isCompatibilityRefusal(err)) return false;
  return graphqlErrorCode(err) === "FORBIDDEN";
}

/** A typed search box value that is a feed address rather than search words. */
export function asFeedURL(input: string): string | null {
  const value = input.trim();
  if (!/^https?:\/\//i.test(value)) return null;
  try {
    const url = new URL(value);
    return url.hostname ? url.toString() : null;
  } catch {
    return null;
  }
}

// Whether each Library has any followed feed, behind its Following pivot.
// One probe per Library per page load; a refused or failed probe hides the
// pivot. A follow marks the Library so the pivot appears at once.
const libraryFeeds = new Map<string, Promise<boolean>>();
const listeners = new Set<() => void>();

export function markLibraryHasFeeds(libraryID: string): void {
  libraryFeeds.set(libraryID, Promise.resolve(true));
  for (const listener of listeners) listener();
}

/** Test seam: forget every probe. */
export function resetLibraryFeedsCache(): void {
  libraryFeeds.clear();
}

export function useLibraryHasFeeds(libraryID: string | undefined): boolean {
  const [has, setHas] = useState(false);
  const [generation, setGeneration] = useState(0);
  useEffect(() => {
    const bump = () => setGeneration((n) => n + 1);
    listeners.add(bump);
    return () => {
      listeners.delete(bump);
    };
  }, []);
  useEffect(() => {
    if (!libraryID) {
      setHas(false);
      return;
    }
    let probe = libraryFeeds.get(libraryID);
    if (!probe) {
      probe = libraryHasFeedSubscriptions(libraryID).catch(() => false);
      libraryFeeds.set(libraryID, probe);
    }
    let live = true;
    void probe.then((value) => {
      if (live) setHas(value);
    });
    return () => {
      live = false;
    };
  }, [libraryID, generation]);
  return has;
}

/** Replace one Subscription in a list by id, or add it at the end. */
export function upsertSubscription(
  list: readonly FeedSubscription[],
  next: FeedSubscription,
): FeedSubscription[] {
  const at = list.findIndex((item) => item.id === next.id);
  if (at < 0) return [...list, next];
  const copy = [...list];
  copy[at] = next;
  return copy;
}
