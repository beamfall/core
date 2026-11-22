import { useCallback, useEffect, useSyncExternalStore } from "react";
import { fetchClientPrefs, setClientPref } from "@/lib/graphql";
import type { ClientPref, SourceItem } from "@/lib/graphql/types";
import { useViewerIdentity } from "@/lib/session";
import { encodeBool, PREF_KEYS, PREF_ON } from "./preferences";

// Spoiler guard (CORE-3057): an unplayed video Media item that a container
// holds hides its still and summary until the Profile plays it, turns the
// guard off (PREF_KEYS.showUnplayedDetails), reveals its container (a
// persisted client pref per container) or reveals the item for this session
// only (memory, cleared by a reload or a new identity). The rule reads only
// the core media class, container and engagement fields.

/** How the guard treats one item in one container context. */
export type SpoilerState =
  /** Not guarded: played, not video, no container, or the guard is off. */
  | "none"
  /** Guarded: draw the placeholder and hide the summary. */
  | "hidden"
  /** Revealed for this session by the Profile. */
  | "revealed"
  /** Revealed because the Profile showed its whole container. */
  | "container";

interface Snapshot {
  /** The identity these prefs belong to; null until a load starts. */
  identity: string | null;
  loaded: boolean;
  showAll: boolean;
  containers: ReadonlySet<string>;
  items: ReadonlySet<string>;
}

const EMPTY: Snapshot = {
  identity: null,
  loaded: false,
  showAll: false,
  containers: new Set(),
  items: new Set(),
};

let snapshot: Snapshot = EMPTY;
const listeners = new Set<() => void>();

function emit(next: Snapshot) {
  snapshot = next;
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

function read() {
  return snapshot;
}

const CONTAINER_PREFIX = `${PREF_KEYS.showUnplayedDetails}.container.`;

/** The client pref that reveals every child of one container. */
export function containerPrefKey(containerID: string): string {
  return `${CONTAINER_PREFIX}${encodeURIComponent(containerID)}`;
}

function containerOfKey(key: string): string | null {
  if (!key.startsWith(CONTAINER_PREFIX)) return null;
  try {
    return decodeURIComponent(key.slice(CONTAINER_PREFIX.length));
  } catch {
    return null;
  }
}

function fromPrefs(prefs: ClientPref[]): Pick<Snapshot, "showAll" | "containers"> {
  const containers = new Set<string>();
  let showAll = false;
  for (const pref of prefs) {
    if (pref.value !== PREF_ON) continue;
    if (pref.key === PREF_KEYS.showUnplayedDetails) showAll = true;
    const id = containerOfKey(pref.key);
    if (id) containers.add(id);
  }
  return { showAll, containers };
}

// The identity whose prefs are loaded or loading; reset on a failed load so a
// later mount retries. Until a load succeeds every candidate stays guarded.
let loading: string | null = null;

function ensure(identity: string) {
  if (loading === identity) return;
  loading = identity;
  // Session reveals belong to one identity.
  const items = snapshot.identity === identity ? snapshot.items : new Set<string>();
  emit({ ...EMPTY, identity, items });
  Promise.resolve()
    .then(() => fetchClientPrefs())
    .then((prefs) => {
      if (loading !== identity) return;
      emit({ ...snapshot, ...fromPrefs(prefs ?? []), loaded: true });
    })
    .catch(() => {
      if (loading === identity) loading = null;
    });
}

/** Whether the Profile has any progress on the item. */
export function isPlayed(item: SourceItem): boolean {
  const engagement = item.engagement;
  return engagement?.completed === true || (engagement?.positionSeconds ?? 0) > 0;
}

/** Whether the guard can apply to the item at all in this container context. */
export function isSpoilerCandidate(item: SourceItem, containerID?: string): boolean {
  return (
    item.kind === "media" && item.mediaClass === "video" && Boolean(containerID) && !isPlayed(item)
  );
}

function stateOf(snap: Snapshot, item: SourceItem, containerID?: string): SpoilerState {
  if (!containerID || !isSpoilerCandidate(item, containerID)) return "none";
  if (snap.loaded && snap.showAll) return "none";
  if (snap.loaded && snap.containers.has(containerID)) return "container";
  if (snap.items.has(item.id)) return "revealed";
  return "hidden";
}

export interface SpoilerGuard {
  /** The guard's state for an item; containerID is the outermost container holding it. */
  state: (item: SourceItem, containerID?: string) => SpoilerState;
  /** Whether a container is revealed for the Profile; false until prefs load. */
  containerRevealed: (containerID: string) => boolean;
  /** Whether the guard is on for the Profile; null until prefs load. */
  guardOn: boolean | null;
}

/**
 * The calling Profile's spoiler guard. Pass `enabled: false` when nothing on
 * screen is a candidate, so no prefs are fetched.
 */
export function useSpoilerGuard(enabled = true): SpoilerGuard {
  const identity = useViewerIdentity() ?? "";
  const snap = useSyncExternalStore(subscribe, read, read);
  useEffect(() => {
    if (enabled) ensure(identity);
  }, [enabled, identity]);
  // Another identity's snapshot is never applied, not even for one render.
  const current = snap.identity === identity ? snap : EMPTY;
  const state = useCallback(
    (item: SourceItem, containerID?: string) => stateOf(current, item, containerID),
    [current],
  );
  const containerRevealed = useCallback(
    (containerID: string) => current.loaded && current.containers.has(containerID),
    [current],
  );
  return { state, containerRevealed, guardOn: current.loaded ? !current.showAll : null };
}

/** Reveal or re-hide one item until the page reloads; nothing is stored. */
export function toggleItemReveal(itemID: string) {
  const items = new Set(snapshot.items);
  if (items.has(itemID)) items.delete(itemID);
  else items.add(itemID);
  emit({ ...snapshot, items });
}

/** Reveal one item until the page reloads. */
export function revealItem(itemID: string) {
  if (!snapshot.items.has(itemID)) toggleItemReveal(itemID);
}

function withContainer(snap: Snapshot, containerID: string, on: boolean): Snapshot {
  const containers = new Set(snap.containers);
  if (on) containers.add(containerID);
  else containers.delete(containerID);
  return { ...snap, containers };
}

/**
 * Reveal or re-hide every child of a container for the Profile, stored as its
 * client pref. The change shows at once and is undone when the write fails;
 * resolves false then.
 */
export async function setContainerRevealed(containerID: string, on: boolean): Promise<boolean> {
  const before = snapshot;
  emit(withContainer(snapshot, containerID, on));
  try {
    await setClientPref(containerPrefKey(containerID), encodeBool(on));
    return true;
  } catch {
    if (snapshot.identity === before.identity) {
      emit(withContainer(snapshot, containerID, before.containers.has(containerID)));
    }
    return false;
  }
}

/** Fold a client pref written elsewhere on this page (Profile Settings) into the guard. */
export function applySpoilerPref(key: string, value: string) {
  if (!snapshot.loaded) return;
  const on = value === PREF_ON;
  if (key === PREF_KEYS.showUnplayedDetails) {
    emit({ ...snapshot, showAll: on });
    return;
  }
  const id = containerOfKey(key);
  if (id) emit(withContainer(snapshot, id, on));
}

/** Test seam: forget every load and reveal. */
export function resetSpoilerGuardForTest() {
  loading = null;
  emit(EMPTY);
}
