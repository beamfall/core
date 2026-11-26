import { useCallback, useEffect, useSyncExternalStore } from "react";
import { getIdentityEpochSignal } from "@/lib/api";
import { fetchClientPrefs, setClientPref } from "@/lib/graphql/viewer";

// The Profile's own sidebar library choice (CORE-2435): which libraries stay
// pinned and in what order. It roams with the Profile in profile_client_prefs
// and layers over the server-wide navigation override, which the server has
// already applied to the manifest navigation: this choice can only reorder or
// unpin what the server still lists.
export const SIDEBAR_LIBRARIES_PREF_KEY = "web.sidebar-libraries";

export interface SidebarLibraryGroup {
  id: string;
  name: string;
  libraries: string[];
  collapsed?: boolean;
}

export type SidebarLayoutItem =
  | { type: "library"; id: string }
  | { type: "group"; id: string }
  | { type: "divider"; id: string };

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
  /** Collapsible Library groups defined by this Profile (web-only). */
  groups?: SidebarLibraryGroup[];
  /** Top-level ordering of Libraries, groups, and dividers (web-only). */
  layout?: SidebarLayoutItem[];
}

export interface SidebarListPrefs {
  order: string[];
  hidden: string[];
}

export type SidebarResolvedNode<T> =
  | { kind: "library"; libraryID: string; entry: T }
  | {
      kind: "group";
      id: string;
      name: string;
      collapsed: boolean;
      items: { libraryID: string; entry: T }[];
    }
  | { kind: "divider"; id: string };

export interface ResolvedSidebarLibraries<T> {
  topLevel: SidebarResolvedNode<T>[];
  pinned: T[];
  unpinned: T[];
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

function parseGroups(value: unknown): SidebarLibraryGroup[] {
  if (!Array.isArray(value)) return [];
  const seenGroups = new Set<string>();
  const claimedLibraries = new Set<string>();
  const out: SidebarLibraryGroup[] = [];
  for (const item of value) {
    if (!item || typeof item !== "object") continue;
    const record = item as Record<string, unknown>;
    const id = typeof record.id === "string" ? record.id.trim() : "";
    const name = typeof record.name === "string" ? record.name.trim() : "";
    if (!id || !name || seenGroups.has(id)) continue;
    seenGroups.add(id);
    const libraries: string[] = [];
    for (const libID of stringList(record.libraries)) {
      if (claimedLibraries.has(libID)) continue;
      claimedLibraries.add(libID);
      libraries.push(libID);
    }
    const group: SidebarLibraryGroup = { id, name, libraries };
    if (record.collapsed === true) group.collapsed = true;
    out.push(group);
  }
  return out;
}

function parseLayout(
  value: unknown,
  groups: SidebarLibraryGroup[],
  fallbackOrder: string[],
): SidebarLayoutItem[] {
  const groupIDs = new Set(groups.map((group) => group.id));
  const groupedLibraries = new Set(groups.flatMap((group) => group.libraries));
  const seen = new Set<string>();
  const seenGroups = new Set<string>();
  const out: SidebarLayoutItem[] = [];

  if (Array.isArray(value)) {
    for (const item of value) {
      if (!item || typeof item !== "object") continue;
      const record = item as Record<string, unknown>;
      const type = record.type;
      const id = typeof record.id === "string" ? record.id.trim() : "";
      if (!id) continue;
      if (type !== "library" && type !== "group" && type !== "divider") continue;
      if (type === "library" && groupedLibraries.has(id)) continue;
      if (type === "group") {
        if (!groupIDs.has(id)) continue;
        seenGroups.add(id);
      }
      const key = `${type}:${id}`;
      if (seen.has(key)) continue;
      seen.add(key);
      out.push({ type, id });
    }
  }

  for (const libID of fallbackOrder) {
    if (groupedLibraries.has(libID)) continue;
    const key = `library:${libID}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({ type: "library", id: libID });
  }

  for (const group of groups) {
    if (seenGroups.has(group.id)) continue;
    seenGroups.add(group.id);
    out.push({ type: "group", id: group.id });
  }

  return out;
}

function hasSidebarStructure(groups: SidebarLibraryGroup[], layout: SidebarLayoutItem[]): boolean {
  return groups.length > 0 || layout.some((item) => item.type === "divider");
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
    const groups = parseGroups(record.groups);
    const layout = parseLayout(record.layout, groups, prefs.order);
    if (hasSidebarStructure(groups, layout)) {
      if (groups.length > 0) prefs.groups = groups;
      prefs.layout = layout;
    }
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
  const groups = parseGroups(prefs.groups);
  const layout = parseLayout(prefs.layout, groups, prefs.order);
  const structured = hasSidebarStructure(groups, layout);
  if (prefs.order.length === 0 && prefs.hidden.length === 0 && !personal && !structured) {
    return "";
  }
  const out: SidebarLibraryPrefs = {
    order: prefs.order,
    hidden: prefs.hidden,
  };
  if (personal) out.personal = personal;
  if (structured) {
    if (groups.length > 0) out.groups = groups;
    out.layout = layout;
  }
  return JSON.stringify(out);
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

/**
 * Resolve the Profile's library order, groups, dividers, and pin state against
 * the server's current library entries. Server removals win, and libraries the
 * Profile has not placed follow at top level in server order.
 */
export function resolveSidebarLibraries<T>(
  entries: T[],
  libraryIDOf: (entry: T) => string,
  prefs: SidebarLibraryPrefs,
): ResolvedSidebarLibraries<T> {
  const groups = parseGroups(prefs.groups);
  const layout = parseLayout(prefs.layout, groups, prefs.order);
  if (!hasSidebarStructure(groups, layout)) {
    const { pinned, unpinned } = applySidebarPrefs(entries, libraryIDOf, prefs);
    return {
      topLevel: pinned.map((entry) => ({
        kind: "library",
        libraryID: libraryIDOf(entry),
        entry,
      })),
      pinned,
      unpinned,
    };
  }

  const byID = new Map(entries.map((entry) => [libraryIDOf(entry), entry]));
  const hidden = new Set(prefs.hidden);
  const groupByID = new Map(groups.map((group) => [group.id, group]));
  const seenLibs = new Set<string>();
  const topLevel: SidebarResolvedNode<T>[] = [];
  const pinned: T[] = [];
  const unpinned: T[] = [];

  for (const item of layout) {
    if (item.type === "divider") {
      topLevel.push({ kind: "divider", id: item.id });
      continue;
    }
    if (item.type === "group") {
      const group = groupByID.get(item.id);
      if (!group) continue;
      const items: { libraryID: string; entry: T }[] = [];
      for (const libID of group.libraries) {
        const entry = byID.get(libID);
        if (!entry || seenLibs.has(libID)) continue;
        seenLibs.add(libID);
        if (hidden.has(libID)) {
          unpinned.push(entry);
        } else {
          items.push({ libraryID: libID, entry });
          pinned.push(entry);
        }
      }
      if (items.length > 0) {
        topLevel.push({
          kind: "group",
          id: group.id,
          name: group.name,
          collapsed: !!group.collapsed,
          items,
        });
      }
      continue;
    }
    const entry = byID.get(item.id);
    if (!entry || seenLibs.has(item.id)) continue;
    seenLibs.add(item.id);
    if (hidden.has(item.id)) {
      unpinned.push(entry);
    } else {
      topLevel.push({ kind: "library", libraryID: item.id, entry });
      pinned.push(entry);
    }
  }

  for (const entry of entries) {
    const libID = libraryIDOf(entry);
    if (seenLibs.has(libID)) continue;
    seenLibs.add(libID);
    if (hidden.has(libID)) {
      unpinned.push(entry);
    } else {
      topLevel.push({ kind: "library", libraryID: libID, entry });
      pinned.push(entry);
    }
  }

  return { topLevel, pinned, unpinned };
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
