import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { ApiError } from "@/lib/api";
import { decidePairingRequest, fetchPairingRequests, type PairingRequest } from "@/lib/graphql";
import { useT } from "@/lib/i18n";

// Pending device pairing requests (CORE-0317, CORE-2064). One provider polls
// the server while the Console is open and the tab is visible, so the Devices
// list and the any-screen prompt share one list and one decision path: an
// Approve in either clears both at once. A server without the endpoint (404)
// stops the polling and shows nothing; a non-admin session (401/403) shows an
// empty list and keeps polling in case the session is later unlocked.

export const PAIRING_POLL_MS = 5000;

export interface PairingRequestsState {
  requests: PairingRequest[];
  /** Resolves true when the server took the decision, or the request was already gone. */
  decide: (decision: "approve" | "deny", code: string) => Promise<boolean>;
}

const EMPTY: PairingRequestsState = { requests: [], decide: async () => false };

const PairingRequestsContext = createContext<PairingRequestsState>(EMPTY);

export function usePairingRequests(): PairingRequestsState {
  return useContext(PairingRequestsContext);
}

function stillPending(r: PairingRequest, now: number): boolean {
  const expires = Date.parse(r.expiresAt);
  return Number.isNaN(expires) || expires > now;
}

export function PairingRequestsProvider({ children }: { children: ReactNode }) {
  const [requests, setRequests] = useState<PairingRequest[]>([]);
  const [now, setNow] = useState(() => Date.now());
  const inFlight = useRef<AbortController | null>(null);
  const supported = useRef(true);

  const load = useCallback(async () => {
    if (!supported.current) return;
    inFlight.current?.abort();
    const controller = new AbortController();
    inFlight.current = controller;
    try {
      const next = await fetchPairingRequests(controller.signal);
      if (controller.signal.aborted) return;
      setRequests(next);
      setNow(Date.now());
    } catch (error) {
      if (controller.signal.aborted) return;
      if (error instanceof ApiError && error.status === 404) supported.current = false;
      setRequests([]);
    }
  }, []);

  useEffect(() => {
    let timer: number | undefined;
    const stop = () => {
      if (timer !== undefined) window.clearInterval(timer);
      timer = undefined;
    };
    const start = () => {
      if (timer !== undefined || !supported.current) return;
      void load();
      timer = window.setInterval(() => {
        if (!supported.current) return stop();
        void load();
      }, PAIRING_POLL_MS);
    };
    const onVisibility = () => (document.visibilityState === "hidden" ? stop() : start());
    if (document.visibilityState !== "hidden") start();
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      document.removeEventListener("visibilitychange", onVisibility);
      stop();
      inFlight.current?.abort();
    };
  }, [load]);

  const decide = useCallback(
    async (decision: "approve" | "deny", code: string) => {
      try {
        await decidePairingRequest(decision, code);
      } catch (error) {
        if (!(error instanceof ApiError && error.status === 404)) return false;
        // 404: the request expired or was decided elsewhere, which is not a
        // failure. It is one if the server still lists the request: it
        // refused the decision (Viewer pairing switched off, say).
        try {
          const next = await fetchPairingRequests();
          setRequests(next);
          setNow(Date.now());
          return !next.some((r) => r.code === code);
        } catch {
          return false;
        }
      }
      setRequests((current) => current.filter((r) => r.code !== code));
      void load();
      return true;
    },
    [load],
  );

  const value = useMemo(
    () => ({ requests: requests.filter((r) => stillPending(r, now)), decide }),
    [requests, now, decide],
  );
  return (
    <PairingRequestsContext.Provider value={value}>{children}</PairingRequestsContext.Provider>
  );
}

// The role a device asked for, in viewer words; anything else the server may
// send is shown as plain text.
export function useRoleLabel(): (role: string) => string {
  const t = useT();
  return (role) =>
    role === "manager" ? t("deviceRoleManager") : role === "viewer" ? t("deviceRoleViewer") : role;
}
