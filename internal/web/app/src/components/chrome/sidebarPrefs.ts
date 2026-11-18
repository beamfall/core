import { useCallback, useEffect, useSyncExternalStore } from "react";
import { fetchClientPrefs, setClientPref } from "@/lib/graphql/viewer";

// The Profile's own sidebar library choice (CORE-2435): which libraries stay
// pinned and in what order. It roams with the Profile in profile_client_prefs
// and layers over the server-wide navigation override, which the server has
// already applied to the manifest navigation: this choice can only reorder or
// unpin what the server still lists.
export const SIDEBAR_LIBRARIES_PREF_KEY = "web.sidebar-libraries";

export interface SidebarLibraryPrefs {
  /** Library IDs in the Profile's order; unnamed libraries follow in server order. */
  order: string[];
  /** Library IDs the Profile unpinned; they move under More. */
  hidden: string[];
}

export const EMPTY_SIDEBAR_PREFS: SidebarLibraryPrefs = { order: [], hidden: [] };

function stringList(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return [...new Set(value.filter((v): v is string => typeof v === "string" && v !== ""))];
}

/** Parse a stored value; anything malformed reads as no choice. */
export function parseSidebarPrefs(raw: string | null | undefined): SidebarLibraryPrefs {
  if (!raw) return EMPTY_SIDEBAR_PREFS;
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object") return EMPTY_SIDEBAR_PREFS;
    const record = parsed as Record<string, unknown>;
    return { order: stringList(record.order), hidden: stringList(record.hidden) };
  } catch {
    return EMPTY_SIDEBAR_PREFS;
  }
}

/** Serialize a choice; an empty choice clears the preference. */
export function serializeSidebarPrefs(prefs: SidebarLibraryPrefs): string {
  if (prefs.order.length === 0 && prefs.hidden.length === 0) return "";
  return JSON.stringify({ order: prefs.order, hidden: prefs.hidden });
}

/**
 * Apply the Profile's choice to the server's library entries. Entries the
 * choice orders come first in its order; the rest keep server order after
 * them. IDs the server no longer lists are dropped, so a server-wide hide
 * always wins.
 */
export function applySidebarPrefs<T>(
  entries: T[],
  libraryIDOf: (entry: T) => string,
  prefs: SidebarLibraryPrefs,
): { pinned: T[]; unpinned: T[] } {
  const rank = new Map(prefs.order.map((id, index) => [id, index]));
  const ordered = entries
    .map((entry, index) => ({ entry, index, rank: rank.get(libraryIDOf(entry)) }))
    .sort((a, b) => {
      if (a.rank !== undefined && b.rank !== undefined) return a.rank - b.rank;
      if (a.rank !== undefined) return -1;
      if (b.rank !== undefined) return 1;
      return a.index - b.index;
    })
    .map(({ entry }) => entry);
  const hidden = new Set(prefs.hidden);
  return {
    pinned: ordered.filter((entry) => !hidden.has(libraryIDOf(entry))),
    unpinned: ordered.filter((entry) => hidden.has(libraryIDOf(entry))),
  };
}

// The loaded choice, held outside React so it survives AppShell remounts
// (every route mounts its own shell). One entry, for one Profile.
interface Entry {
  profileID: string;
  prefs: SidebarLibraryPrefs;
  // A failed read is retried on the next mount: an aborted first load (a
  // reveal or relock cancels in-flight requests) must not pin server order.
  state: "loading" | "loaded" | "failed";
  // Set once the Profile saves: a read still in flight must not overwrite it.
  dirty: boolean;
}

let entry: Entry | null = null;
const listeners = new Set<() => void>();

function emit() {
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

function isReady(current: Entry) {
  return current.dirty || current.state !== "loading";
}

function load(profileID: string) {
  if (entry?.profileID === profileID && (entry.dirty || entry.state !== "failed")) return;
  const current: Entry = {
    profileID,
    prefs: EMPTY_SIDEBAR_PREFS,
    state: "loading",
    dirty: false,
  };
  entry = current;
  emit();
  fetchClientPrefs()
    .then((prefs) => {
      if (entry !== current || current.dirty) return;
      current.prefs = parseSidebarPrefs(
        prefs.find((pref) => pref.key === SIDEBAR_LIBRARIES_PREF_KEY)?.value,
      );
      current.state = "loaded";
      emit();
    })
    .catch(() => {
      // Keep the server order for now; the next mount tries again.
      if (entry !== current) return;
      current.state = "failed";
      emit();
    });
}

/** Test seam: forget the cached choice. */
export function resetSidebarPrefsCache() {
  entry = null;
}

/**
 * The signed-in Profile's sidebar choice, a saver, and whether the saved
 * choice has settled (loaded, failed or since saved). With no Profile the
 * choice is empty, ready, and saving does nothing.
 */
export function useSidebarPrefs(
  profileID: string | null,
): [SidebarLibraryPrefs, (prefs: SidebarLibraryPrefs) => Promise<void>, boolean] {
  useEffect(() => {
    if (profileID) load(profileID);
  }, [profileID]);
  const prefs = useSyncExternalStore(
    subscribe,
    () => (profileID && entry?.profileID === profileID ? entry.prefs : EMPTY_SIDEBAR_PREFS),
    () => EMPTY_SIDEBAR_PREFS,
  );
  const ready = useSyncExternalStore(
    subscribe,
    () => !profileID || (entry?.profileID === profileID && isReady(entry)),
    () => !profileID,
  );
  const save = useCallback(
    async (next: SidebarLibraryPrefs) => {
      if (!profileID) return;
      if (entry?.profileID !== profileID) {
        entry = { profileID, prefs: EMPTY_SIDEBAR_PREFS, state: "loading", dirty: false };
      }
      const current = entry;
      const previous = current.prefs;
      current.prefs = next;
      current.dirty = true;
      emit();
      try {
        await setClientPref(SIDEBAR_LIBRARIES_PREF_KEY, serializeSidebarPrefs(next));
      } catch (error) {
        if (entry === current && current.prefs === next) {
          current.prefs = previous;
          emit();
        }
        throw error;
      }
    },
    [profileID],
  );
  return [prefs, save, ready];
}
