import { useCallback, useLayoutEffect, useRef, useState } from "react";
import { ApiError } from "@/lib/api";
import { withAdminEpoch } from "./adminEpoch";

function isAuthRejection(error: unknown): boolean {
  if (!(error instanceof ApiError)) return false;
  return error.status === 401 || error.status === 403;
}

// A tiny load/refresh state machine shared by every Console screen. Each screen
// fetches one GraphQL op; this owns the data/loading/error spine and a manual
// refresh, so the screens stay declarative. Errors degrade to a localized
// "unavailable" line — the Console never surfaces a raw server message.
//
// Each run owns an AbortController: a rerun (deps change) or unmount aborts
// the previous request's transport-level signal instead of merely discarding
// its result, so a screen removed by AdminGate (Profile switch, an expired
// session, or an explicit lock) actually cancels its in-flight fetch.
export interface AsyncState<T> {
  data: T | null;
  loading: boolean;
  /** True once a fetch has rejected since the last successful load. */
  failed: boolean;
  refresh: () => Promise<void>;
  /** Replace data through the same abort and generation fence as a fetch. */
  replace: (producer: (signal: AbortSignal) => Promise<T>) => Promise<void>;
  setData: (next: T) => void;
}

export function useAsync<T>(
  fetcher: (signal: AbortSignal) => Promise<T>,
  deps: unknown[] = [],
): AsyncState<T> {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  // Always call the LATEST fetcher via a ref, and let `deps` alone drive the
  // effect below. Memoizing `run` on `deps` via useCallback(fetcher, deps) is
  // a trap: when a caller's fetcher closure happens to be referentially
  // stable across renders, useCallback still returns that same reference on
  // a deps change, so a `[run]`-keyed effect silently never re-fires — deps
  // stops being the refetch trigger it's documented to be.
  const fetcherRef = useRef(fetcher);
  fetcherRef.current = fetcher;

  // Monotonic epoch. A dependency change (deps) or unmount bumps it; a fetch
  // whose epoch is no longer current is dropped instead of writing stale data
  // (or writing to an unmounted screen) — the same stale-callback guard the
  // session spine (lib/session.tsx) applies to identity-epoch transitions.
  // Manual refreshes keep rendering last-good rows while they load. The
  // dependency boundary below clears scoped rows before paint because a
  // dependency change can select a different identity or resource.
  const epoch = useRef(0);
  const request = useRef<AbortController | null>(null);

  const beginRequest = useCallback(() => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    const started = ++epoch.current;
    // The signal handed to the fetcher carries BOTH boundaries: this hook's own
    // (dependency change, manual refresh, unmount) and the Admin-Session epoch
    // (expiry, lock, Profile switch). Console screens render only under an
    // Admin Session, so an ended session invalidates their rows exactly the way
    // an ended identity epoch invalidates the session spine's — and it must
    // abort the transport, not merely drop the response.
    const signal = withAdminEpoch(controller.signal);
    const current = () => epoch.current === started && !signal.aborted;
    return { current, signal };
  }, []);

  const refresh = useCallback(async () => {
    const { current, signal } = beginRequest();
    setLoading(true);
    setFailed(false);
    try {
      const next = await fetcherRef.current(signal);
      if (!current()) return;
      setData(next);
    } catch (error) {
      if (!current()) return;
      // Authorization loss invalidates scoped rows. Other failures retain the
      // last-good result while failed=true so a transient outage does not
      // replace useful Console state with an empty flash.
      if (isAuthRejection(error)) setData(null);
      setFailed(true);
    } finally {
      if (current()) setLoading(false);
    }
  }, [beginRequest]);

  const replace = useCallback(
    async (producer: (signal: AbortSignal) => Promise<T>) => {
      const { current, signal } = beginRequest();
      const next = await producer(signal);
      if (current()) setData(next);
    },
    [beginRequest],
  );

  useLayoutEffect(() => {
    // A dependency change selects a different scoped resource (and may be an
    // identity transition), so no row or error from the departed dependency
    // may remain visible. Manual refreshes keep their last-good data; only the
    // effect-driven dependency boundary clears it.
    setData(null);
    setLoading(true);
    setFailed(false);
    void refresh();
    return () => {
      request.current?.abort();
      epoch.current++;
    };
    // `deps` (the caller's declared dependency array), not `refresh` — see
    // the fetcherRef comment above for why keying off refresh is unsound.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  return { data, loading, failed, refresh, replace, setData };
}
