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
import {
  ApiError,
  isAuthRejection,
  setExpectedIdentity,
  setExpectedProfile,
  setIdentityEpochReset,
  setIdentityEpochSignal,
  setProfileMismatchHandler,
} from "@/lib/api";
import { takeBeamfallHostInitialMessages } from "@/lib/beamfallHost";
import {
  advanceCompatibilityEpoch,
  type CompatHandshakeResponse,
  isBlocked,
  isCompatibilityRefusal,
  performHandshake,
  rebindCompatibility,
  subscribeCompatibilityBlock,
} from "@/lib/compat";
import {
  fetchCurrentSession,
  fetchManifest,
  fetchProfiles,
  promote as promoteMutation,
  relock as relockMutation,
  renewSession,
  reveal as revealMutation,
  switchProfile as switchProfileMutation,
} from "@/lib/graphql";
import type { NavigationManifest, ProfileSummary, SessionInfo } from "@/lib/graphql/types";
import { setManifestRefresher, setSessionRefresher } from "@/lib/manifestRefresh";

// A lapsed session renews once from the device-secret cookies before the
// browser is shown the enroll screen (CORE-1106). Any other failure, or a
// refused renewal, keeps the original error.
async function fetchSessionRenewingOnce(signal: AbortSignal): Promise<SessionInfo> {
  try {
    return await fetchCurrentSession(signal);
  } catch (err) {
    if (!isUnauthorized(err)) throw err;
    if (!(await renewSession(signal))) throw err;
    return fetchCurrentSession(signal);
  }
}

function isUnauthorized(err: unknown): boolean {
  return err instanceof ApiError && err.status === 401;
}

// The handshake is the first request a load makes, so a lapsed session cookie
// is refused there before the session read is reached. It gets the same
// one-time renewal and one retry; `renewed` tells the caller the renewal is
// spent, so the session read that follows does not renew again.
async function handshakeRenewingOnce(
  signal: AbortSignal,
): Promise<{ compatibility: CompatHandshakeResponse; renewed: boolean }> {
  try {
    return { compatibility: await performHandshake(), renewed: false };
  } catch (err) {
    if (!isUnauthorized(err)) throw err;
    if (!(await renewSession(signal))) throw err;
    return { compatibility: await performHandshake(), renewed: true };
  }
}

import { clearViewerCache, viewerIdentityKey } from "@/lib/viewerCache";

// The app-wide session spine every area consumes. Bootstraps the bound
// device session, the Navigation Manifest, and the Profile picker, and owns
// the Reveal contract (ADR-0004). The browser cannot read its own Device Role
// from the cookie (HttpOnly) — it asks the server here.
//
// Device Role and Reveal state are visible STATE, never hidden modes: the
// header renders them. An unenrolled browser surfaces `needsEnroll` so the
// router can route to /enroll.

export interface SessionState {
  session: SessionInfo | null;
  manifest: NavigationManifest | null;
  profiles: ProfileSummary[];
  loading: boolean;
  needsEnroll: boolean;
  error: string | null;
  /** Convenience: deviceRole is the manager role (store.DeviceRoleManager). */
  isManager: boolean;
  revealed: boolean;
  refresh: () => Promise<void>;
  promote: (code: string) => Promise<void>;
  reveal: (pin: string) => Promise<boolean>;
  relock: () => Promise<void>;
  /** Rebinds the session; `pin` is the target adult Profile's entry PIN. */
  switchProfile: (profileID: string, pin?: string) => Promise<void>;
  /** Re-reads the roster in place, applying any confirmed entry PIN before the read. */
  refreshProfiles: (profileIDWithSavedEntryPin?: string) => Promise<void>;
  /**
   * Re-reads currentSession in place inside the current identity epoch,
   * applying any confirmed subtitle preferences before the read.
   */
  refreshSession: (confirmed?: { profileID: string; subtitleLanguages: string[] }) => Promise<void>;
}

const SessionContext = createContext<SessionState | null>(null);

export function SessionProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<SessionInfo | null>(null);
  const sessionRef = useRef<SessionInfo | null>(null);
  sessionRef.current = session;
  const [manifest, setManifest] = useState<NavigationManifest | null>(null);
  const [profiles, setProfiles] = useState<ProfileSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [needsEnroll, setNeedsEnroll] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // One identity epoch per load(): a device-role promotion, Profile switch,
  // Reveal, relock, or manual refresh aborts the in-flight epoch's fetches and
  // clears every scoped field (session/manifest/profiles) and the in-memory
  // viewer data cache (viewerCache.ts) before issuing the next epoch's fetches.
  // This is the stale-callback guard ADR-0082 §50/§58 requires — a slow
  // session/manifest/profiles fetch issued for the *previous* epoch can never
  // overwrite the new epoch's state (its AbortSignal has already fired), and
  // no previous-epoch data is left on screen while the next epoch loads.
  const controllerRef = useRef<AbortController | null>(null);
  const refreshSeqRef = useRef(0);
  const profileRefreshSeq = useRef(0);
  const sessionRefreshSeq = useRef(0);

  const beginEpoch = useCallback(() => {
    controllerRef.current?.abort();
    advanceCompatibilityEpoch();
    const controller = new AbortController();
    controllerRef.current = controller;
    setIdentityEpochSignal(controller.signal);
    clearViewerCache();
    setSession(null);
    setManifest(null);
    setProfiles([]);
    setNeedsEnroll(false);
    setError(null);
    setLoading(true);
    return controller.signal;
  }, []);

  const loadEpoch = useCallback(async (signal: AbortSignal) => {
    const current = () => !signal.aborted;
    // The reads of a new epoch name no Profile until the session is read, so a
    // reload after another tab's switch is never refused. A mutation that opens
    // the epoch (Reveal, relock, a Profile switch) runs before this and still
    // names the Profile this tab shows, so a stale tab's PIN or switch is
    // refused instead of acting on the Profile the session moved to.
    setExpectedProfile();
    setExpectedIdentity();
    try {
      const { compatibility, renewed } = await handshakeRenewingOnce(signal);
      if (!current()) return;
      if (isBlocked()) {
        clearViewerCache();
        setSession(null);
        setManifest(null);
        setProfiles([]);
        throw new Error(compatibility.messageClass);
      }
      const readSession = renewed ? fetchCurrentSession : fetchSessionRenewingOnce;
      let s: SessionInfo;
      try {
        s = await readSession(signal);
      } catch (err) {
        if (!isCompatibilityRefusal(err)) throw err;
        await rebindCompatibility();
        if (!current()) return;
        s = await readSession(signal);
      }
      if (!current()) return;
      // From here on every request names this Profile, and the server refuses
      // one after another tab switched the shared session (CORE-2923).
      setExpectedProfile(s.profileID);
      setExpectedIdentity({
        profileID: s.profileID,
        revealed: s.revealed,
        deviceRole: s.deviceRole,
        profile: s.profile,
      });
      // Manifest + profiles are best-effort; a failure here is not fatal to
      // having a session unless either response says this identity is no
      // longer authorized. Hold every scoped value locally until both settle
      // so a late 401/403 cannot expose a partially refreshed identity.
      const [m, p] = await Promise.allSettled([fetchManifest(signal), fetchProfiles(signal)]);
      if (!current()) return;
      if (
        (m.status === "rejected" && isAuthRejection(m.reason)) ||
        (p.status === "rejected" && isAuthRejection(p.reason))
      ) {
        setExpectedProfile();
        setExpectedIdentity();
        clearViewerCache();
        setSession(null);
        setManifest(null);
        setProfiles([]);
        setNeedsEnroll(true);
        setError(null);
        return;
      }
      setSession(s);
      setNeedsEnroll(false);
      if (m.status === "fulfilled") setManifest(m.value);
      if (p.status === "fulfilled") setProfiles(p.value);
    } catch (err) {
      if (!current()) return;
      if (isCompatibilityRefusal(err)) {
        const messageClass = Reflect.get(err.payload as object, "messageClass");
        setError(typeof messageClass === "string" ? messageClass : "compat-client-too-old");
      } else if (isAuthRejection(err)) {
        clearViewerCache();
        setNeedsEnroll(true);
        setSession(null);
        setManifest(null);
        setProfiles([]);
        setError(null);
      } else {
        setError(err instanceof Error ? err.message : "Failed to load session");
      }
    } finally {
      if (current()) setLoading(false);
    }
  }, []);

  const load = useCallback(async () => loadEpoch(beginEpoch()), [beginEpoch, loadEpoch]);

  // Read by the epoch reset below, which must stay referentially stable (it is
  // registered once per mount) while still seeing the live Reveal state.
  const revealedRef = useRef(false);
  revealedRef.current = session?.revealed ?? false;
  const hasSessionRef = useRef(false);
  hasSessionRef.current = Boolean(session);

  useEffect(
    () =>
      subscribeCompatibilityBlock((result) => {
        controllerRef.current?.abort();
        setIdentityEpochSignal();
        setExpectedProfile();
        setExpectedIdentity();
        clearViewerCache();
        setSession(null);
        setManifest(null);
        setProfiles([]);
        setNeedsEnroll(false);
        setError(result.messageClass);
        setLoading(false);
      }),
    [],
  );

  useEffect(() => {
    takeBeamfallHostInitialMessages();
    // A Reveal-terminal response (401/403/410 on a revealed resource) is an
    // identity-epoch transition, not a player-local error: the server has
    // already ended the revealed identity, so the revealed session/manifest/
    // profiles must not stay reusable after the viewer backs out. Registering
    // here makes every such observer route through the same reset.
    //
    // Gated on a reveal actually being in force. That is both the honest
    // precondition (there is no revealed state to clear otherwise) and what
    // makes the reset self-terminating: the new epoch reads back revealed=false,
    // so a player that remounts and observes the same terminal status cannot
    // drive a reload loop.
    setIdentityEpochReset(() => {
      if (!revealedRef.current) return;
      void load();
    });
    // A request refused because another tab switched the shared session to
    // another Profile (CORE-2923): read the identity again, so this tab shows
    // the Profile the session is bound to instead of acting as the old one.
    // api.ts calls this only for the Profile this tab still shows, and the new
    // epoch's loadEpoch names none until it has read the session, so it cannot loop.
    setProfileMismatchHandler(() => {
      void load();
    });
    void load();
    return () => {
      controllerRef.current?.abort();
      clearViewerCache();
      advanceCompatibilityEpoch();
      setIdentityEpochSignal();
      setExpectedProfile();
      setExpectedIdentity();
      setIdentityEpochReset();
      setProfileMismatchHandler();
    };
  }, [load]);

  // A manifest re-read inside the current identity epoch (see
  // lib/manifestRefresh): no scoped state is cleared, a failure keeps the
  // manifest already on screen, and a result that arrives after the epoch
  // ended, or while a load has the manifest cleared, is dropped.
  useEffect(() => {
    setManifestRefresher(() => {
      const signal = controllerRef.current?.signal;
      if (!signal || signal.aborted) return;
      // Only the latest request may apply: a slow earlier read must not
      // overwrite the manifest a later write (delete, then Undo) refreshed.
      refreshSeqRef.current += 1;
      const seq = refreshSeqRef.current;
      fetchManifest(signal).then(
        (next) => {
          if (!signal.aborted && seq === refreshSeqRef.current) {
            setManifest((current) => (hasSessionRef.current ? next : current));
          }
        },
        () => {},
      );
    });
    setSessionRefresher((origin) => {
      if (
        origin?.epoch &&
        (origin.epoch.aborted || origin.epoch !== controllerRef.current?.signal)
      ) {
        return;
      }
      if (origin?.profileID !== undefined && sessionRef.current?.profileID !== origin.profileID) {
        return;
      }
      void load();
    });
    return () => {
      setManifestRefresher();
      setSessionRefresher();
    };
  }, [load]);

  const reveal = useCallback(
    async (pin: string) => {
      const signal = beginEpoch();
      try {
        return (await revealMutation(pin, signal)).revealed;
      } finally {
        if (!signal.aborted) await loadEpoch(signal);
      }
    },
    [beginEpoch, loadEpoch],
  );

  const promote = useCallback(
    async (code: string) => {
      const signal = beginEpoch();
      try {
        await promoteMutation(code, signal);
      } finally {
        if (!signal.aborted) await loadEpoch(signal);
      }
    },
    [beginEpoch, loadEpoch],
  );

  const relock = useCallback(async () => {
    const signal = beginEpoch();
    try {
      await relockMutation(signal);
    } finally {
      if (!signal.aborted) await loadEpoch(signal);
    }
  }, [beginEpoch, loadEpoch]);

  const switchProfile = useCallback(
    async (profileID: string, pin?: string) => {
      const signal = beginEpoch();
      try {
        await switchProfileMutation(profileID, signal, pin);
      } finally {
        if (!signal.aborted) await loadEpoch(signal);
      }
    },
    [beginEpoch, loadEpoch],
  );

  const refreshProfiles = useCallback(async (profileIDWithSavedEntryPin?: string) => {
    const signal = controllerRef.current?.signal;
    if (!signal || signal.aborted) return;
    const seq = ++profileRefreshSeq.current;
    if (profileIDWithSavedEntryPin !== undefined) {
      setProfiles((current) =>
        current.map((profile) =>
          profile.id === profileIDWithSavedEntryPin ? { ...profile, entryPinSet: true } : profile,
        ),
      );
    }
    const next = await fetchProfiles(signal);
    if (!signal.aborted && seq === profileRefreshSeq.current) setProfiles(next);
  }, []);

  const refreshSession = useCallback<SessionState["refreshSession"]>(
    async (confirmed) => {
      const signal = controllerRef.current?.signal;
      if (!signal || signal.aborted) return;
      const seq = ++sessionRefreshSeq.current;
      if (confirmed) {
        setSession((current) =>
          current?.profileID === confirmed.profileID
            ? { ...current, subtitleLanguages: confirmed.subtitleLanguages }
            : current,
        );
      }
      const next = await fetchCurrentSession(signal);
      // A load that cleared the session owns the next value; never resurrect it.
      if (signal.aborted || seq !== sessionRefreshSeq.current || !sessionRef.current) return;
      // Another tab switched Profiles meanwhile: the manifest and viewer cache
      // belong to the old Profile, so take the full identity reload instead.
      if (next.profileID !== sessionRef.current.profileID) {
        await load();
        return;
      }
      setExpectedIdentity({
        profileID: next.profileID,
        revealed: next.revealed,
        deviceRole: next.deviceRole,
        profile: next.profile,
      });
      setSession(next);
    },
    [load],
  );

  const value = useMemo<SessionState>(
    () => ({
      session,
      manifest,
      profiles,
      loading,
      needsEnroll,
      error,
      // The server's canonical value is lowercase "manager"
      // (store.DeviceRoleManager); matching "MANAGER" left the Console
      // unreachable and the MANAGER affordances hidden.
      isManager: session?.deviceRole === "manager",
      revealed: session?.revealed ?? false,
      refresh: load,
      promote,
      reveal,
      relock,
      switchProfile,
      refreshProfiles,
      refreshSession,
    }),
    [
      session,
      manifest,
      profiles,
      loading,
      needsEnroll,
      error,
      load,
      promote,
      reveal,
      relock,
      switchProfile,
      refreshProfiles,
      refreshSession,
    ],
  );

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

/**
 * The viewer data cache scope for the bound identity (see viewerCache.ts), or
 * null when no session is bound or no SessionProvider is mounted — callers
 * then fetch uncached, exactly as before the cache existed.
 */
export function useViewerIdentity(): string | null {
  return viewerIdentityKey(useContext(SessionContext)?.session ?? null);
}

export function useSession(): SessionState {
  const ctx = useContext(SessionContext);
  if (!ctx) throw new Error("useSession must be used within <SessionProvider>");
  return ctx;
}
