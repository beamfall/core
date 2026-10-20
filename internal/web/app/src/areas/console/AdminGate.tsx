import { Lock } from "lucide-react";
import {
  type FormEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { endAdminSession, fetchAdminSession, verifyAdminSession } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import type { StringKey } from "@/strings";
import { setAdminEpochSignal } from "./adminEpoch";
import { LOCK_REASON_LEAD, LOCK_REASON_TITLE, type LockReason, lockReason } from "./lib/lockReason";

// The Console is gated by an ADMIN SESSION (ADR-0007 §4 / ADR-0085), never by a
// Profile PIN, and never by Reveal. Until the admin password verifies, this
// renders an explicit LOCKED state that says WHY (not adult, no Admin Session,
// expired, Profile switched) — never a confusing redirect (ADR-0085 §30). Once
// active it renders the Console plus a visible lock affordance: admin is visible
// STATE, never a hidden mode.
//
// Admin Session is orthogonal to Reveal (ADR-0085 §32): being admin does not
// reveal restricted content and Reveal does not grant admin. The locked card
// states this so the two locks are not conflated.
export function AdminGate({
  children,
  sessionBar,
}: {
  children: ReactNode;
  /** Slot for the active-session bar, rendered by the area shell when active. */
  sessionBar?: (lock: () => void) => ReactNode;
}) {
  const t = useT();
  const { session } = useSession();
  const profileKind = session?.profile;
  const currentProfileID = session?.profileID;

  const [active, setActive] = useState<boolean | null>(null);
  const [password, setPassword] = useState("");
  const [rejected, setRejected] = useState(false);
  const [busy, setBusy] = useState(false);
  // The Profile bound and the expiry captured at unlock time, so a later
  // Profile switch or a lapsed window drops the gate to its reason-coded state.
  const [boundProfileID, setBoundProfileID] = useState("");
  const [expiresAt, setExpiresAt] = useState<string | undefined>(undefined);
  const [, setExpiryTick] = useState(0);
  const requestRef = useRef<AbortController | null>(null);
  const requestGeneration = useRef(0);

  // Every call site below is an Admin-Session epoch boundary — the mount/Profile
  // read, an unlock, an explicit lock, the expiry timer, and unmount — so the
  // gate's own request controller IS the epoch. Aborting it aborts the Console
  // work issued under the ending session, not just the gate's own fetch.
  const invalidateRequest = useCallback(() => {
    requestGeneration.current++;
    requestRef.current?.abort();
    requestRef.current = null;
    setAdminEpochSignal();
  }, []);

  const beginRequest = useCallback(() => {
    invalidateRequest();
    const controller = new AbortController();
    const generation = requestGeneration.current;
    requestRef.current = controller;
    setAdminEpochSignal(controller.signal);
    return {
      controller,
      isCurrent: () =>
        requestGeneration.current === generation &&
        requestRef.current === controller &&
        !controller.signal.aborted,
    };
  }, [invalidateRequest]);

  // Resolve the current Admin Session once on mount. An aborted or failed
  // read is treated as "locked" — the password is the only way in. The query
  // includes the same expiry the server enforces so a reload-restored session
  // can still show and client-expire its short owner re-auth window. The
  // AbortController guards the same stale-callback hazard as the session
  // spine (lib/session.tsx): if this gate unmounts before the fetch resolves,
  // the aborted signal drops the response instead of writing to unmounted
  // state.
  useLayoutEffect(() => {
    const request = beginRequest();
    // Profile/Reveal transitions temporarily remove or replace the bound
    // Profile. Clear every Admin-scoped field before reading authority for the
    // new identity; in particular, aborting an unlock must not strand busy or
    // retain its password/rejection in the next epoch.
    setActive(null);
    setPassword("");
    setRejected(false);
    setBusy(false);
    setBoundProfileID("");
    setExpiresAt(undefined);
    fetchAdminSession(request.controller.signal)
      .then((s) => {
        if (!request.isCurrent()) return;
        setActive(s.active);
        if (s.active) {
          setBoundProfileID(currentProfileID ?? "");
          setExpiresAt(s.expiresAt);
        }
      })
      .catch(() => {
        if (request.isCurrent()) setActive(false);
      });
    return invalidateRequest;
  }, [beginRequest, currentProfileID, invalidateRequest]);

  useEffect(() => {
    if (!active || !expiresAt) return;
    const delay = Math.max(0, Date.parse(expiresAt) - Date.now());
    if (delay > 2_147_483_647) return;
    const timer = window.setTimeout(() => {
      invalidateRequest();
      setExpiryTick((value) => value + 1);
    }, delay);
    return () => window.clearTimeout(timer);
  }, [active, expiresAt, invalidateRequest]);

  async function onUnlock(e: FormEvent) {
    e.preventDefault();
    if (!password) return;
    setBusy(true);
    setRejected(false);
    const request = beginRequest();
    try {
      const s = await verifyAdminSession(password, request.controller.signal);
      if (!request.isCurrent()) return;
      setActive(s.active);
      setPassword("");
      if (s.active) {
        setBoundProfileID(currentProfileID ?? "");
        setExpiresAt(s.expiresAt);
      } else {
        setRejected(true);
      }
    } catch {
      if (!request.isCurrent()) return;
      // The server returns one generic rejection for every failure (wrong
      // password, ineligible Profile, throttle) — surface a single message and
      // never infer the server-side reason (ADR-0085 §47 no-oracle).
      setRejected(true);
      setPassword("");
    } finally {
      if (request.isCurrent()) setBusy(false);
    }
  }

  async function lock() {
    const request = beginRequest();
    setActive(false);
    setBoundProfileID("");
    setExpiresAt(undefined);
    try {
      await endAdminSession(request.controller.signal);
    } catch {
      // best-effort; the local lock still takes effect.
    }
  }

  if (active === null) {
    return <p className="text-sm text-muted-foreground">{t("loading")}</p>;
  }

  // The client-derivable lock reason: a non-adult Profile, an absent session, a
  // switched Profile, or a lapsed window. null = legitimately unlocked.
  const reason: LockReason | null = lockReason({
    profileKind,
    adminActive: active,
    boundProfileID,
    currentProfileID,
    expiresAt,
  });

  if (reason) {
    return (
      <LockedCard
        reason={reason}
        onUnlock={onUnlock}
        password={password}
        setPassword={setPassword}
        busy={busy}
        rejected={rejected}
      />
    );
  }

  return (
    <>
      {sessionBar?.(lock)}
      {children}
    </>
  );
}

// The reason-coded locked surface. A managed Profile sees the not-adult reason
// with NO password form (it can never unlock); every other reason offers the
// re-auth form. Both carry the explicit Admin≠Reveal distinction line.
function LockedCard({
  reason,
  onUnlock,
  password,
  setPassword,
  busy,
  rejected,
}: {
  reason: LockReason;
  onUnlock: (e: FormEvent) => void;
  password: string;
  setPassword: (v: string) => void;
  busy: boolean;
  rejected: boolean;
}) {
  const t = useT();
  const titleKey: StringKey = LOCK_REASON_TITLE[reason];
  const leadKey: StringKey = LOCK_REASON_LEAD[reason];
  const canReauth = reason !== "not-adult";

  return (
    <div className="console-gate">
      <Card>
        <CardHeader>
          <div className="console-gate-mark" aria-hidden>
            <Lock className="h-4 w-4" />
          </div>
          <CardTitle>{t(titleKey)}</CardTitle>
          <CardDescription>{t(leadKey)}</CardDescription>
        </CardHeader>
        <CardContent>
          {canReauth && (
            <form onSubmit={onUnlock} className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="admin-password">{t("adminPasswordLabel")}</Label>
                <Input
                  id="admin-password"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  autoComplete="current-password"
                  autoFocus
                />
              </div>
              {rejected && (
                <p role="alert" className="text-sm text-[var(--red-500)]">
                  {t("adminUnlockRejected")}
                </p>
              )}
              <Button type="submit" disabled={busy || !password}>
                {t("adminUnlockSubmit")}
              </Button>
            </form>
          )}
          {/* Admin Session is server-administration authority and is separate
              from Reveal (ADR-0085 §32) — stated so the two locks aren't
              conflated. */}
          <p className="mt-4 text-xs text-[var(--text-muted)]">{t("adminRevealDistinction")}</p>
        </CardContent>
      </Card>
    </div>
  );
}
