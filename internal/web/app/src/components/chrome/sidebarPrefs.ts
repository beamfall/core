import { useCallback, useEffect, useSyncExternalStore } from "react";
import { getIdentityEpochSignal } from "@/lib/api";
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
  /**
   * The "Your stuff" group's choice (CORE-2827), by entry ID: its order, and
   * the entries unpinned, which leave the sidebar (the account menu keeps
   * them). Absent from values stored before the group existed, and left out
   * of the stored value while it is empty, so older clients read it unchanged.
   */
  personal?: SidebarListPrefs;
}

export interface SidebarListPrefs {
  order: string[];
  hidden: string[];
}

export const EMPTY_SIDEBAR_PREFS: SidebarLibraryPrefs = { order: [], hidden: [] };

// The "Your stuff" entries, in default order: the Profile's own lists, at the
// routes the account menu already links.
export const PERSONAL_ENTRIES = [
  { id: "playlists", path: "/playlists" },
  { id: "favorites", path: "/favorites" },
  { id: "history", path: "/history" },
] as const;

export type PersonalEntry = (typeof PERSONAL_ENTRIES)[number];

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
    const prefs: SidebarLibraryPrefs = {
      order: stringList(record.order),
      hidden: stringList(record.hidden),
    };
    const personal = listPrefs(record.personal);
    if (personal) prefs.personal = personal;
    return prefs;
  } catch {
    return EMPTY_SIDEBAR_PREFS;
  }
}

// A stored group choice, or nothing when it is absent, malformed or empty.
function listPrefs(value: unknown): SidebarListPrefs | undefined {
  if (!value || typeof value !== "object") return undefined;
  const record = value as Record<string, unknown>;
  const list = { order: stringList(record.order), hidden: stringList(record.hidden) };
  return list.order.length > 0 || list.hidden.length > 0 ? list : undefined;
}

/** Serialize a choice; an empty choice clears the preference. */
export function serializeSidebarPrefs(prefs: SidebarLibraryPrefs): string {
  const personal = listPrefs(prefs.personal);
  if (prefs.order.length === 0 && prefs.hidden.length === 0 && !personal) return "";
  return JSON.stringify(
    personal
      ? { order: prefs.order, hidden: prefs.hidden, personal }
      : { order: prefs.order, hidden: prefs.hidden },
  );
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
  // Only a successful read (or write) makes the choice editable: an edit
  // begun from server order would overwrite a saved choice it never read. A
  // failed read is retried on the next mount, since a reveal or relock
  // aborts in-flight requests.
  state: "loading" | "loaded" | "failed";
  // What the server last confirmed holding, from a read or a write; a failed
  // save rolls back to it.
  confirmed: SidebarLibraryPrefs | null;
  // Set while the Profile has unconfirmed saves: a read still in flight must
  // not overwrite them.
  dirty: boolean;
  // Writes run one after another, in save order, so the server ends on the
  // same choice as the cache even across remounts.
  writes: Promise<unknown>;
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

function freshEntry(profileID: string): Entry {
  return {
    profileID,
    prefs: EMPTY_SIDEBAR_PREFS,
    state: "loading",
    confirmed: null,
    dirty: false,
    // A refetch for the same Profile keeps queueing behind earlier writes.
    writes: entry?.profileID === profileID ? entry.writes : Promise.resolve(),
  };
}

function load(profileID: string) {
  if (entry?.profileID === profileID && (entry.dirty || entry.state !== "failed")) return;
  const current = freshEntry(profileID);
  entry = current;
  emit();
  fetchClientPrefs()
    .then((prefs) => {
      if (entry !== current) return;
      const read = parseSidebarPrefs(
        prefs.find((pref) => pref.key === SIDEBAR_LIBRARIES_PREF_KEY)?.value,
      );
      // A write confirmed meanwhile is newer than this read.
      if (!current.confirmed) {
        current.confirmed = read;
        if (!current.dirty) current.prefs = read;
      }
      current.state = "loaded";
      emit();
    })
    .catch(() => {
      // Keep the server order for now; the next mount tries again.
      if (entry !== current || current.state === "loaded") return;
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
 * choice has been read so it can be edited. With no Profile the choice is
 * empty, ready, and saving does nothing.
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
    () => !profileID || (entry?.profileID === profileID && entry.state === "loaded"),
    () => !profileID,
  );
  const save = useCallback(
    async (next: SidebarLibraryPrefs) => {
      if (!profileID) return;
      if (entry?.profileID !== profileID) entry = freshEntry(profileID);
      const current = entry;
      current.prefs = next;
      current.dirty = true;
      emit();
      // A queued write must not run under a later identity: once a Profile
      // switch, reveal or relock starts a new epoch, the session it would
      // carry belongs to someone else.
      const epoch = getIdentityEpochSignal();
      const write = current.writes.then(() => {
        if (getIdentityEpochSignal() !== epoch || epoch?.aborted || entry !== current) {
          throw new DOMException("The Profile changed before the save ran", "AbortError");
        }
        return setClientPref(SIDEBAR_LIBRARIES_PREF_KEY, serializeSidebarPrefs(next));
      });
      current.writes = write.catch(() => {});
      try {
        await write;
      } catch (error) {
        // Roll back unless a later save has replaced this one; its write is
        // queued behind this and settles the choice itself. Earlier writes
        // have settled, so nothing is left unconfirmed and a failed read may
        // be retried.
        if (entry === current && current.prefs === next) {
          current.prefs = current.confirmed ?? EMPTY_SIDEBAR_PREFS;
          current.dirty = false;
          emit();
        }
        throw error;
      }
      if (entry !== current) return;
      current.confirmed = next;
      current.state = "loaded";
      if (current.prefs === next) current.dirty = false;
      emit();
    },
    [profileID],
  );
  return [prefs, save, ready];
}
