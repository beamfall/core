import { useSyncExternalStore } from "react";
import type { EventStreamState } from "./eventStream";

let connectionState: EventStreamState = "live";
let recoveryEpoch = 0;
const listeners = new Set<() => void>();

function emit(): void {
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/**
 * Record the live state of the server event stream.
 * Returns `true` when transitioning from `"reconnecting"` back to `"live"`.
 */
export function reportEventStreamState(next: EventStreamState): boolean {
  const recovered = connectionState === "reconnecting" && next === "live";
  if (connectionState === next && !recovered) return false;
  connectionState = next;
  if (recovered) recoveryEpoch += 1;
  emit();
  return recovered;
}

/** Current event-stream connection state ("live" or "reconnecting"). */
export function useServerConnectionState(): EventStreamState {
  return useSyncExternalStore(
    subscribe,
    () => connectionState,
    () => connectionState,
  );
}

/** Counter that increments whenever the event stream recovers from "reconnecting" to "live". */
export function useServerRecoveryEpoch(): number {
  return useSyncExternalStore(
    subscribe,
    () => recoveryEpoch,
    () => recoveryEpoch,
  );
}

/** Reset connection state between unit tests. */
export function resetServerConnectionForTest(): void {
  connectionState = "live";
  recoveryEpoch = 0;
  emit();
}
