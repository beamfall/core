import { useEffect, useRef, useSyncExternalStore } from "react";
import { subscribeEvents } from "@/lib/api";

// One counter that moves whenever the bound Profile's watched, watchlist or
// rating state changes — in this tab (an item action just succeeded) or on
// another signed-in client (the server's Profile-gated `client.invalidate`
// event). Anything that renders engagement-derived data reads the counter and
// revalidates when it moves, so nothing needs a manual refresh.

const INVALIDATE_KIND = "client.invalidate";
// Sent when the client's cursor is too old to replay what it missed, so any
// engagement change may have been lost.
const FULL_REFRESH_KIND = "client.fullRefresh";
const ENGAGEMENT_CHANGED = "engagement-changed";

let epoch = 0;
const listeners = new Set<() => void>();

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** Announce that the bound Profile's engagement state changed. */
export function notifyEngagementChanged(): void {
  epoch += 1;
  for (const listener of listeners) listener();
}

/** The engagement counter now, for a cache that must drop reads made before a change. */
export function readEngagementEpoch(): number {
  return epoch;
}

/** A counter that changes each time engagement state changes; use it as an effect dependency. */
export function useEngagementEpoch(): number {
  return useSyncExternalStore(
    subscribe,
    () => epoch,
    () => epoch,
  );
}

/** Whether an SSE frame is the server's engagement-changed invalidation. */
export function isEngagementInvalidation(data: unknown): boolean {
  if (typeof data !== "string") return false;
  try {
    const envelope = JSON.parse(data) as { payload?: { reason?: string } };
    return envelope.payload?.reason === ENGAGEMENT_CHANGED;
  } catch {
    return false;
  }
}

/**
 * Mount once per viewer session: turns the server's invalidation events into epoch changes.
 * `onEvent` sees every frame of the same stream, so other viewer features (the cross-device
 * remote) share this one subscription instead of opening their own.
 */
export function useEngagementEvents(onEvent?: (event: MessageEvent) => void): void {
  const handler = useRef(onEvent);
  handler.current = onEvent;
  useEffect(
    () =>
      subscribeEvents((event) => {
        if (
          event.type === FULL_REFRESH_KIND ||
          (event.type === INVALIDATE_KIND && isEngagementInvalidation(event.data))
        )
          notifyEngagementChanged();
        handler.current?.(event);
      }),
    [],
  );
}
