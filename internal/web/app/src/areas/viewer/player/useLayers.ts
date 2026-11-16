import { useEffect, useState } from "react";

// Transition layers for the audio stage (CORE-2218). Each change of `key` adds
// a layer for the new item and marks the older ones as leaving, so the outgoing
// artwork or words can animate out while the incoming ones animate in. Leaving
// layers are dropped `leaveMs` after the incoming layer is ready (an image
// waits for its load), so the stage never flashes empty between two items. An
// empty key means "nothing": the old layers leave and no new one comes in.
export interface Layer<T> {
  id: number;
  value: T;
  leaving: boolean;
}

interface State<T> {
  key: string;
  layers: Layer<T>[];
  next: number;
}

/** Leaving layers kept at most; a burst of skips keeps only the newest ones. */
const MAX_LEAVING = 2;

export function useLayers<T>(
  key: string,
  value: T,
  leaveMs: number,
  /** Whether the incoming layer (by id) is ready to cover the leaving ones. */
  isReady: (id: number) => boolean = () => true,
): Layer<T>[] {
  const [state, setState] = useState<State<T>>(() => ({
    key,
    layers: key ? [{ id: 0, value, leaving: false }] : [],
    next: 1,
  }));

  let current = state;
  if (key !== state.key) {
    const leaving = state.layers.map((l) => ({ ...l, leaving: true })).slice(-MAX_LEAVING);
    current = {
      key,
      layers: key ? [...leaving, { id: state.next, value, leaving: false }] : leaving,
      next: state.next + 1,
    };
    setState(current);
  }

  const hasLeaving = current.layers.some((l) => l.leaving);
  const incoming = current.layers.find((l) => !l.leaving);
  const ready = !incoming || isReady(incoming.id);
  const generation = current.next;
  useEffect(() => {
    if (!hasLeaving || !ready) return;
    const timer = setTimeout(() => {
      setState((s) =>
        s.next === generation ? { ...s, layers: s.layers.filter((l) => !l.leaving) } : s,
      );
    }, leaveMs);
    return () => clearTimeout(timer);
  }, [hasLeaving, ready, leaveMs, generation]);

  // The incoming layer always shows the latest value; leaving ones keep theirs.
  return current.layers.map((l) => (l.leaving ? l : { ...l, value }));
}
