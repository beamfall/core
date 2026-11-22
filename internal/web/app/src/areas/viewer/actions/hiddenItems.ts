import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import {
  clearAllMutes,
  clearMute,
  fetchClientPrefs,
  fetchMutes,
  setClientPref,
  setMute,
} from "@/lib/graphql";
import type { ItemTarget, MuteInfo } from "@/lib/graphql/types";
import { encodeBool, PREF_KEYS, PREF_ON } from "../lib/preferences";
import { notifyEngagementChanged, useEngagementEpoch } from "./engagementSync";

// Hide (CORE-2836): a viewer hides a Media item, or a whole show (an Entity and
// everything under it), for their own Profile. Hiding is a per-Profile Mute on
// the server (ADR-0054), so it holds on every client and other Profiles never
// see it. Search and direct links still reach a hidden item, which is how it is
// shown again.

/** The Mute that hides a target: kind "media" for one Media item, "entity" for an Entity's tree. */
export function muteFor(target: ItemTarget): { kind: string; targetID: string } {
  return "mediaID" in target
    ? { kind: "media", targetID: target.mediaID }
    : { kind: "entity", targetID: target.entityID };
}

function isMuteOf(mute: MuteInfo, target: ItemTarget): boolean {
  const want = muteFor(target);
  return mute.kind === want.kind && mute.targetID === want.targetID;
}

/** Hide a target for the calling Profile and offer an Undo. */
export async function hideItem(target: ItemTarget, title: string): Promise<void> {
  const { kind, targetID } = muteFor(target);
  await setMute(kind, targetID);
  notifyEngagementChanged();
  announceUndo({ kind: "hidden", target, title });
}

/** Show a hidden target again. */
export async function unhideItem(target: ItemTarget): Promise<void> {
  const { kind, targetID } = muteFor(target);
  await clearMute(kind, targetID);
  notifyEngagementChanged();
}

export interface HiddenItems {
  /** The Profile's Mutes; null until the first load settles. */
  mutes: MuteInfo[] | null;
  failed: boolean;
  isHidden: (target: ItemTarget) => boolean;
  /** Show every hidden item again; resolves false when the write failed. */
  unhideAll: () => Promise<boolean>;
}

/**
 * The calling Profile's hidden items, reloaded whenever engagement changes here
 * or on another client. Pass `enabled: false` to hold the load until a menu opens.
 */
export function useHiddenItems({ enabled = true } = {}): HiddenItems {
  const epoch = useEngagementEpoch();
  const [mutes, setMutes] = useState<MuteInfo[] | null>(null);
  const [failed, setFailed] = useState(false);

  // The epoch is a dependency by intent: a change made elsewhere reloads the list.
  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    fetchMutes(controller.signal)
      .then((next) => {
        setMutes(next);
        setFailed(false);
      })
      .catch(() => {
        if (!controller.signal.aborted) setFailed(true);
      });
    return () => controller.abort();
  }, [enabled, epoch]);

  const isHidden = useCallback(
    (target: ItemTarget) => mutes?.some((mute) => isMuteOf(mute, target)) ?? false,
    [mutes],
  );
  const unhideAll = useCallback(async () => {
    try {
      await clearAllMutes();
      setMutes([]);
      notifyEngagementChanged();
      return true;
    } catch {
      setFailed(true);
      return false;
    }
  }, []);
  return { mutes, failed, isHidden, unhideAll };
}

/**
 * A key that changes whenever the Profile's hidden items change, here or on
 * another client; null until the list has loaded. A browse that keeps its
 * pages across engagement changes starts over when it moves, since a hide
 * can drop any card from any read.
 */
export function useHiddenKey(): string | null {
  const { mutes } = useHiddenItems();
  return useMemo(
    () =>
      mutes === null
        ? null
        : mutes
            .map((mute) => `${mute.kind}:${mute.targetID}:${mute.includeDescendants}`)
            .sort()
            .join("|"),
    [mutes],
  );
}

// Stands in for the first key when engagement changed before it loaded.
const UNSEEN_CHANGE = "\u0000unseen-change";

/**
 * Whether a hide changed since the last call: seen holds the key last
 * observed and is updated. The first loaded key is only recorded, unless
 * `engaged` reported an engagement change while the key was still loading:
 * a hide made then lands in that first key, after the browse read its pages,
 * so the first key counts as moved (CORE-2854).
 */
export function hiddenKeyMoved(
  seen: { current: string | null },
  key: string | null,
  engaged = false,
): boolean {
  if (key === null) {
    if (engaged && seen.current === null) seen.current = UNSEEN_CHANGE;
    return false;
  }
  if (seen.current === key) return false;
  const moved = seen.current !== null;
  seen.current = key;
  return moved;
}

export interface ItemHide {
  /** Null until the Profile's hidden list has loaded. */
  hidden: boolean | null;
  pending: boolean;
  failed: boolean;
  toggle: () => Promise<void>;
}

/** Hide or show one item, for a menu entry or a button. */
export function useItemHide(target: ItemTarget, title: string, { enabled = true } = {}): ItemHide {
  const { mutes, isHidden, failed: loadFailed } = useHiddenItems({ enabled });
  const [pending, setPending] = useState(false);
  const [failed, setFailed] = useState(false);
  const hidden = mutes === null ? null : isHidden(target);
  const targetRef = useRef(target);
  targetRef.current = target;
  const toggle = useCallback(async () => {
    setPending(true);
    try {
      if (hidden) await unhideItem(targetRef.current);
      else await hideItem(targetRef.current, title);
      setFailed(false);
    } catch {
      setFailed(true);
    } finally {
      setPending(false);
    }
  }, [hidden, title]);
  return { hidden, pending, failed: failed || loadFailed, toggle };
}

// --- Undo notice -----------------------------------------------------------

export interface HiddenNotice {
  id: number;
  /**
   * What the Undo reverses: "hidden" is a Hide (a Mute), "continue" a removal
   * from Continue Watching (CORE-2017).
   */
  kind: "hidden" | "continue";
  target: ItemTarget;
  title: string;
  /** The change itself did not save: the notice says so and offers no Undo. */
  failed?: boolean;
  /** The identity epoch the change was made in: an Undo from another writes nothing. */
  epoch?: AbortSignal;
  /** The Profile the change was made for: the server refuses an Undo for another. */
  profileID?: string;
}

let notice: HiddenNotice | null = null;
let noticeSeq = 0;
const noticeListeners = new Set<() => void>();

function emitNotice(next: HiddenNotice | null): void {
  notice = next;
  for (const listener of noticeListeners) listener();
}

/** Show the Undo notice for a change just made, replacing any earlier one. */
export function announceUndo(next: Omit<HiddenNotice, "id">): void {
  noticeSeq += 1;
  emitNotice({ ...next, id: noticeSeq });
}

/** Dismiss the Undo notice, if it still shows the given hide. */
export function dismissHiddenNotice(id: number): void {
  if (notice?.id === id) emitNotice(null);
}

/** Drop the Undo notice outright: the Profile it belonged to is no longer bound. */
export function clearHiddenNotice(): void {
  if (notice) emitNotice(null);
}

/** The most recent undoable change, until it is undone, dismissed or times out. */
export function useHiddenNotice(): HiddenNotice | null {
  return useSyncExternalStore(
    (listener) => {
      noticeListeners.add(listener);
      return () => noticeListeners.delete(listener);
    },
    () => notice,
    () => notice,
  );
}

// --- Hide watched ----------------------------------------------------------

export interface HideWatched {
  /** Null until the Profile's preferences have loaded. */
  on: boolean | null;
  failed: boolean;
  /**
   * Counts changes to the setting made on this client, shared by every reader,
   * so a browse can start over when the setting flips under it.
   */
  version: number;
  set: (on: boolean) => Promise<void>;
}

let hideWatchedVersion = 0;
const hideWatchedListeners = new Set<() => void>();

function subscribeHideWatched(listener: () => void): () => void {
  hideWatchedListeners.add(listener);
  return () => hideWatchedListeners.delete(listener);
}

function bumpHideWatched(): void {
  hideWatchedVersion += 1;
  for (const listener of hideWatchedListeners) listener();
}

const readHideWatchedVersion = () => hideWatchedVersion;

/**
 * The Profile's "Hide watched" preference: with it on, the server leaves Media
 * the Profile has finished out of catalog shelves and library browse. Changing
 * it reloads every engagement-derived view.
 */
export function useHideWatched(): HideWatched {
  const epoch = useEngagementEpoch();
  const [on, setOn] = useState<boolean | null>(null);
  const [failed, setFailed] = useState(false);
  const version = useSyncExternalStore(
    subscribeHideWatched,
    readHideWatchedVersion,
    readHideWatchedVersion,
  );

  useEffect(() => {
    const controller = new AbortController();
    fetchClientPrefs()
      .then((prefs) => {
        if (controller.signal.aborted) return;
        setOn(prefs.some((p) => p.key === PREF_KEYS.hideWatched && p.value === PREF_ON));
      })
      .catch(() => {
        if (!controller.signal.aborted) setFailed(true);
      });
    return () => controller.abort();
  }, [epoch]);

  const set = useCallback(async (next: boolean) => {
    setOn(next);
    try {
      await setClientPref(PREF_KEYS.hideWatched, encodeBool(next));
      setFailed(false);
    } catch {
      setFailed(true);
    }
    // Reload either way: on success the views follow the new setting, on
    // failure the toggle returns to the stored one.
    bumpHideWatched();
    notifyEngagementChanged();
  }, []);
  return { on, failed, version, set };
}
