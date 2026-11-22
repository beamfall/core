import { useCallback, useEffect, useSyncExternalStore } from "react";
import { getIdentityEpochSignal } from "@/lib/api";
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
// Each load has a generation, so a read that a newer load or a save
// superseded is dropped.
let loading: string | null = null;
let generation = 0;

function load(identity: string) {
  loading = identity;
  const mine = ++generation;
  // Session reveals belong to one identity.
  const items = snapshot.identity === identity ? snapshot.items : new Set<string>();
  emit({ ...EMPTY, identity, items });
  Promise.resolve()
    .then(() => fetchClientPrefs())
    .then((prefs) => {
      if (generation !== mine) return;
      emit({ ...snapshot, ...fromPrefs(prefs ?? []), loaded: true });
    })
    .catch(() => {
      if (generation === mine) loading = null;
    });
}

function ensure(identity: string) {
  if (loading !== identity) load(identity);
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

/**
 * The container an item's guard is keyed on: the outermost container the
 * server reports for it, else the page's container, so a container reveal
 * reads the same key on every surface.
 */
export function guardContainerOf(item: SourceItem, fallback?: string): string | undefined {
  return item.container?.id ?? fallback;
}

/**
 * The guard key every candidate among items shares, if any is guarded. Items
 * from several containers share none, so one toggle never reveals another
 * container's children.
 */
export function sharedGuardContainer(
  items: readonly SourceItem[],
  fallback?: string,
): string | undefined {
  let shared: string | undefined;
  for (const item of items) {
    const key = guardContainerOf(item, fallback);
    if (!isSpoilerCandidate(item, key)) continue;
    if (shared !== undefined && shared !== key) return undefined;
    shared = key;
  }
  return shared;
}

function stateOf(snap: Snapshot, item: SourceItem, containerID?: string): SpoilerState {
  if (!containerID || !isSpoilerCandidate(item, containerID)) return "none";
  if (!snap.loaded) return "hidden";
  if (snap.showAll) return "none";
  if (snap.containers.has(containerID)) return "container";
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

/**
 * Reveal or re-hide one item until the page reloads; nothing is stored. A
 * no-op until the Profile's prefs load, so the guard fails closed.
 */
export function toggleItemReveal(itemID: string) {
  if (!snapshot.loaded) return;
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

// Writes per identity and container key run one after another, so the server
// keeps the latest choice. Each key remembers the value its last successful
// write stored (or the value before its first write) and its queue's tail.
const writeTail = new Map<string, Promise<void>>();
const confirmed = new Map<string, boolean>();

/**
 * Reveal or re-hide every child of a container for the Profile, stored as its
 * client pref. The change shows at once. When the latest write for that
 * container fails, the guard returns to the last value the server stored;
 * resolves false on failure.
 */
export async function setContainerRevealed(containerID: string, on: boolean): Promise<boolean> {
  const before = snapshot;
  const key = `${before.identity ?? ""}\n${containerID}`;
  if (!writeTail.has(key)) confirmed.set(key, before.containers.has(containerID));
  emit(withContainer(snapshot, containerID, on));
  // A queued write is dropped once the identity epoch ends, so a Profile
  // switch never stores the previous Profile's choice for the next one.
  const epoch = getIdentityEpochSignal();
  const write = (writeTail.get(key) ?? Promise.resolve()).then(() => {
    if (getIdentityEpochSignal() !== epoch || epoch?.aborted) {
      throw new Error("identity changed");
    }
    return setClientPref(containerPrefKey(containerID), encodeBool(on));
  });
  const tail = write.then(
    () => undefined,
    () => undefined,
  );
  writeTail.set(key, tail);
  try {
    await write;
    confirmed.set(key, on);
    return true;
  } catch {
    if (writeTail.get(key) === tail && snapshot.identity === before.identity) {
      emit(withContainer(snapshot, containerID, confirmed.get(key) ?? false));
    }
    return false;
  } finally {
    if (writeTail.get(key) === tail) {
      writeTail.delete(key);
      confirmed.delete(key);
    }
  }
}

/**
 * Fold a client pref that `identity` saved elsewhere on this page (Profile
 * Settings) into the guard. Another identity's snapshot is left alone; a load
 * still in flight for this identity is restarted so its older read cannot
 * overwrite the save.
 */
export function applySpoilerPref(identity: string | null, key: string, value: string) {
  if (snapshot.identity !== (identity ?? "")) return;
  if (!snapshot.loaded) {
    if (loading === snapshot.identity) load(snapshot.identity);
    return;
  }
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
  generation += 1;
  writeTail.clear();
  confirmed.clear();
  emit(EMPTY);
}
