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
import {
  endAdminSession,
  fetchAdminSession,
  fetchSetupState,
  verifyAdminSession,
} from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import type { StringKey } from "@/strings";
import { setAdminEpochSignal } from "./adminEpoch";
import "./AdminGate.css";
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
/** Wording for a surface other than the Console that reuses this gate. */
export interface AdminGateCopy {
  /** Headline and lead of the plain "no Admin Session yet" state. */
  title: StringKey;
  lead: StringKey;
  /** The unlock button, for every state that offers the password form. */
  submit: StringKey;
}

export function AdminGate({
  children,
  sessionBar,
  copy,
}: {
  children: ReactNode;
  /** Replaces the Console wording; the unlock flow itself is unchanged. */
  copy?: AdminGateCopy;
  /** Slot for the active-session bar, rendered by the area shell when active. */
  sessionBar?: (lock: () => void) => ReactNode;
}) {
  const t = useT();
  const { session } = useSession();
  const isManager = session?.deviceRole === "manager";
  const profileKind = session?.profile;
  const currentProfileID = session?.profileID;

  const [active, setActive] = useState<boolean | null>(null);
  const [password, setPassword] = useState("");
  const [rejected, setRejected] = useState(false);
  const [busy, setBusy] = useState(false);
  // CORE-1132: no admin password exists yet, so the locked card offers to choose
  // one. Read once per epoch; an unread or failed read means "set", which is the
  // ordinary unlock form and the safe default (the server never sets one unasked).
  const [needsFirstPassword, setNeedsFirstPassword] = useState(false);
  const [confirmPassword, setConfirmPassword] = useState("");
  const [mismatch, setMismatch] = useState(false);
  // The Profile bound and the expiry captured at unlock time, so a later
  // Profile switch or a lapsed window drops the gate to its reason-coded state.
  const [boundProfileID, setBoundProfileID] = useState("");
  const [expiresAt, setExpiresAt] = useState<string | undefined>(undefined);
  const [, setExpiryTick] = useState(0);
  const requestRef = useRef<AbortController | null>(null);
  const requestGeneration = useRef(0);
  // Only the Console's gate publishes the Admin-Session epoch. A surface that
  // reuses the gate keeps its page, and the requests that page is waiting on,
  // across a prompt; ending them with the session would strand that page.
  const ownsEpoch = copy === undefined;

  // Every call site below is an Admin-Session epoch boundary — the mount/Profile
  // read, an unlock, an explicit lock, the expiry timer, and unmount — so the
  // gate's own request controller IS the epoch. Aborting it aborts the Console
  // work issued under the ending session, not just the gate's own fetch.
  const invalidateRequest = useCallback(() => {
    requestGeneration.current++;
    requestRef.current?.abort();
    requestRef.current = null;
    if (ownsEpoch) setAdminEpochSignal();
  }, [ownsEpoch]);

  const beginRequest = useCallback(() => {
    invalidateRequest();
    const controller = new AbortController();
    const generation = requestGeneration.current;
    requestRef.current = controller;
    if (ownsEpoch) setAdminEpochSignal(controller.signal);
    return {
      controller,
      isCurrent: () =>
        requestGeneration.current === generation &&
        requestRef.current === controller &&
        !controller.signal.aborted,
    };
  }, [invalidateRequest, ownsEpoch]);

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
    setConfirmPassword("");
    setMismatch(false);
    setRejected(false);
    setBusy(false);
    setBoundProfileID("");
    setExpiresAt(undefined);
    setNeedsFirstPassword(false);
    fetchAdminSession(request.controller.signal)
      .then(async (s) => {
        // Only a locked, eligible caller can ever be offered the set-password step.
        let first = false;
        if (!s.active && isManager && profileKind === "adult") {
          first = await Promise.resolve()
            .then(() => fetchSetupState(request.controller.signal))
            .then((state) => state.adminPasswordSet === false)
            .catch(() => false);
        }
        if (!request.isCurrent()) return;
        setNeedsFirstPassword(first);
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
  }, [beginRequest, currentProfileID, invalidateRequest, isManager, profileKind]);

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

  // The mount-time setup-state read can fail transiently on a passwordless
  // server, leaving the ordinary unlock form that can never succeed. After a
  // rejected unlock, read it again so the set-password step appears once the
  // read works; a failed read changes nothing.
  async function recheckFirstPassword(request: {
    controller: AbortController;
    isCurrent: () => boolean;
  }) {
    if (!isManager || profileKind !== "adult") return;
    try {
      const state = await fetchSetupState(request.controller.signal);
      if (request.isCurrent() && state.adminPasswordSet === false) setNeedsFirstPassword(true);
    } catch {
      // Keep the ordinary form.
    }
  }

  async function onUnlock(e: FormEvent) {
    e.preventDefault();
    if (!password) return;
    const creating = needsFirstPassword;
    if (creating && password !== confirmPassword) {
      setMismatch(true);
      return;
    }
    setMismatch(false);
    setBusy(true);
    setRejected(false);
    const request = beginRequest();
    try {
      const s = await verifyAdminSession(password, request.controller.signal, creating);
      if (!request.isCurrent()) return;
      setActive(s.active);
      setPassword("");
      setConfirmPassword("");
      if (s.active) {
        setNeedsFirstPassword(false);
        setBoundProfileID(currentProfileID ?? "");
        setExpiresAt(s.expiresAt);
      } else {
        setRejected(true);
        if (!creating) await recheckFirstPassword(request);
      }
    } catch {
      if (!request.isCurrent()) return;
      // The server returns one generic rejection for every failure (wrong
      // password, ineligible Profile, throttle) — surface a single message and
      // never infer the server-side reason (ADR-0085 §47 no-oracle).
      setRejected(true);
      setPassword("");
      setConfirmPassword("");
      if (!creating) await recheckFirstPassword(request);
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
        copy={copy}
        onUnlock={onUnlock}
        password={password}
        setPassword={setPassword}
        firstPassword={needsFirstPassword && reason === "no-session"}
        confirmPassword={confirmPassword}
        setConfirmPassword={setConfirmPassword}
        mismatch={mismatch}
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
const AUTOFILL_NEW = "new-password";
const AUTOFILL_CURRENT = "current-password";

function LockedCard({
  reason,
  copy,
  onUnlock,
  password,
  setPassword,
  firstPassword,
  confirmPassword,
  setConfirmPassword,
  mismatch,
  busy,
  rejected,
}: {
  reason: LockReason;
  copy?: AdminGateCopy;
  onUnlock: (e: FormEvent) => void;
  password: string;
  setPassword: (v: string) => void;
  /** No admin password exists: choose one (with a confirmation) instead of unlocking. */
  firstPassword: boolean;
  confirmPassword: string;
  setConfirmPassword: (v: string) => void;
  mismatch: boolean;
  busy: boolean;
  rejected: boolean;
}) {
  const t = useT();
  const ownCopy = reason === "no-session" ? copy : undefined;
  const titleKey: StringKey = firstPassword
    ? "consoleFirstPasswordTitle"
    : (ownCopy?.title ?? LOCK_REASON_TITLE[reason]);
  const leadKey: StringKey = firstPassword
    ? "consoleFirstPasswordLead"
    : (ownCopy?.lead ?? LOCK_REASON_LEAD[reason]);
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
                  autoComplete={firstPassword ? AUTOFILL_NEW : AUTOFILL_CURRENT}
                  autoFocus
                />
              </div>
              {firstPassword && (
                <div className="space-y-2">
                  <Label htmlFor="admin-password-confirm">{t("setupAdminPasswordConfirm")}</Label>
                  <Input
                    id="admin-password-confirm"
                    type="password"
                    value={confirmPassword}
                    onChange={(e) => setConfirmPassword(e.target.value)}
                    autoComplete="new-password"
                  />
                </div>
              )}
              {mismatch && (
                <p role="alert" className="text-sm text-[var(--red-500)]">
                  {t("consoleFirstPasswordMismatch")}
                </p>
              )}
              {rejected && (
                <p role="alert" className="text-sm text-[var(--red-500)]">
                  {t(firstPassword ? "consoleFirstPasswordRejected" : "adminUnlockRejected")}
                </p>
              )}
              <Button
                type="submit"
                disabled={busy || !password || (firstPassword && !confirmPassword)}
              >
                {t(
                  firstPassword
                    ? "consoleFirstPasswordSubmit"
                    : (copy?.submit ?? "adminUnlockSubmit"),
                )}
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
