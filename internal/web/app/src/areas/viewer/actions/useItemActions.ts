import { useCallback, useEffect, useRef, useState } from "react";
import { fetchItemState, setRating, setWatched, setWatchlisted } from "@/lib/graphql";
import type { ItemStateInfo, ItemTarget } from "@/lib/graphql/types";
import { notifyEngagementChanged, useEngagementEpoch } from "./engagementSync";

/** The target for a feed item: a "media" item is one Media, anything else is an Entity. */
export function itemTargetFor(item: { id: string; kind: string }): ItemTarget {
  return item.kind === "media" ? { mediaID: item.id } : { entityID: item.id };
}

function targetKey(target: ItemTarget): string {
  return "mediaID" in target ? `media:${target.mediaID}` : `entity:${target.entityID}`;
}

export interface ItemActions {
  /** The Profile's state for the item; null until the first load settles. */
  state: ItemStateInfo | null;
  /** True while an action is in flight. */
  pending: boolean;
  /** True when the last load or action failed; cleared by the next success. */
  failed: boolean;
  setWatched: (watched: boolean) => Promise<void>;
  setWatchlisted: (watchlisted: boolean) => Promise<void>;
  /** 1–10 sets the rating; 0 clears it. */
  setRating: (rating: number) => Promise<void>;
}

/**
 * The calling Profile's watched, watchlist and rating state for one item, with
 * the actions that change it. The state follows changes made on other signed-in
 * clients. Pass `enabled: false` to hold the load until a menu actually opens.
 */
export function useItemActions(target: ItemTarget, { enabled = true } = {}): ItemActions {
  const key = targetKey(target);
  const epoch = useEngagementEpoch();
  const [state, setState] = useState<ItemStateInfo | null>(null);
  const [pending, setPending] = useState(false);
  const [failed, setFailed] = useState(false);
  const targetRef = useRef(target);
  targetRef.current = target;
  const keyRef = useRef(key);

  // The epoch is a dependency by intent: a change made elsewhere reloads the state.
  useEffect(() => {
    if (keyRef.current !== key) {
      keyRef.current = key;
      setState(null);
    }
    if (!enabled) return;
    const controller = new AbortController();
    fetchItemState(targetRef.current, controller.signal)
      .then((next) => {
        setState(next);
        setFailed(false);
      })
      .catch(() => {
        if (!controller.signal.aborted) setFailed(true);
      });
    return () => controller.abort();
  }, [key, enabled, epoch]);

  const run = useCallback(async (action: (target: ItemTarget) => Promise<ItemStateInfo>) => {
    const startedFor = keyRef.current;
    setPending(true);
    try {
      const next = await action(targetRef.current);
      if (keyRef.current === startedFor) setState(next);
      setFailed(false);
      notifyEngagementChanged();
    } catch {
      setFailed(true);
    } finally {
      setPending(false);
    }
  }, []);

  return {
    state,
    pending,
    failed,
    setWatched: useCallback((watched: boolean) => run((t) => setWatched(t, watched)), [run]),
    setWatchlisted: useCallback(
      (watchlisted: boolean) => run((t) => setWatchlisted(t, watchlisted)),
      [run],
    ),
    setRating: useCallback((rating: number) => run((t) => setRating(t, rating)), [run]),
  };
}
