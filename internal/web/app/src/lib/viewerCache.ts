import type { SessionInfo } from "@/lib/graphql/types";

// In-memory viewer data cache: already loaded section items and entity
// details, so returning to a view renders at once and revalidates behind it.
//
// Identity isolation (ADR-0004 / ADR-0082): every entry is keyed by the bound
// identity — device role, Profile and Reveal state — so a lookup for one
// identity can never return another identity's data, and SessionProvider
// clears the whole cache at every identity-epoch transition and on any
// authorization rejection. Nothing here is ever persisted: the session cookie
// is HttpOnly and shared across tabs, so a stored copy could not be matched to
// the current identity before a server round trip.

/** Upper bound on retained entries; the least recently used is evicted first. */
export const VIEWER_CACHE_MAX_ENTRIES = 64;

const entries = new Map<string, unknown>();

/** The cache scope for a bound session; null when no identity is bound. */
export function viewerIdentityKey(session: SessionInfo | null): string | null {
  if (!session) return null;
  return JSON.stringify([session.deviceRole, session.profileID, session.profile, session.revealed]);
}

function entryKey(identity: string, key: string): string {
  return `${identity}\u0000${key}`;
}

export function readViewerCache<T>(identity: string | null, key: string): T | undefined {
  if (!identity) return undefined;
  const k = entryKey(identity, key);
  if (!entries.has(k)) return undefined;
  const value = entries.get(k) as T;
  // Refresh recency: Map iteration order is insertion order.
  entries.delete(k);
  entries.set(k, value);
  return value;
}

export function writeViewerCache<T>(identity: string | null, key: string, value: T): void {
  if (!identity) return;
  const k = entryKey(identity, key);
  entries.delete(k);
  entries.set(k, value);
  while (entries.size > VIEWER_CACHE_MAX_ENTRIES) {
    const oldest = entries.keys().next().value;
    if (oldest === undefined) break;
    entries.delete(oldest);
  }
}

export function deleteViewerCache(identity: string | null, key: string): void {
  if (!identity) return;
  entries.delete(entryKey(identity, key));
}

/** The entries cached for one identity as [key, value] pairs, without touching recency. */
export function viewerCacheEntries(identity: string | null): Array<[string, unknown]> {
  if (!identity) return [];
  const prefix = entryKey(identity, "");
  const out: Array<[string, unknown]> = [];
  for (const [k, value] of entries) {
    if (k.startsWith(prefix)) out.push([k.slice(prefix.length), value]);
  }
  return out;
}

/** Drops every entry for every identity (identity-epoch transitions, auth rejection). */
export function clearViewerCache(): void {
  entries.clear();
}

/** Test-only view of the retained entry count. */
export function viewerCacheSize(): number {
  return entries.size;
}
