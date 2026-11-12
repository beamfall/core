import { useEffect, useSyncExternalStore } from "react";

// One counter that moves whenever the bound Profile's watched, watchlist or
// rating state changes — in this tab (an item action just succeeded) or on
// another signed-in client (the server's Profile-gated `client.invalidate`
// event). Anything that renders engagement-derived data reads the counter and
// revalidates when it moves, so nothing needs a manual refresh.

const INVALIDATE_KIND = "client.invalidate";
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
 * Subscribe to one named kind on the SSE event stream. The server frames every
 * outbox event with an `event: <kind>` line, which the browser delivers only to
 * a listener registered for that kind — never to `onmessage`. Returns an
 * unsubscribe function; a runtime without EventSource gets a no-op.
 */
function subscribeEventKind(kind: string, onEvent: (event: MessageEvent) => void): () => void {
  if (typeof EventSource === "undefined") return () => {};
  const source = new EventSource("/api/events", { withCredentials: true });
  source.addEventListener(kind, onEvent as EventListener);
  return () => source.close();
}

/** Mount once per viewer session: turns the server's invalidation events into epoch changes. */
export function useEngagementEvents(): void {
  useEffect(
    () =>
      subscribeEventKind(INVALIDATE_KIND, (event) => {
        if (isEngagementInvalidation(event.data)) notifyEngagementChanged();
      }),
    [],
  );
}
