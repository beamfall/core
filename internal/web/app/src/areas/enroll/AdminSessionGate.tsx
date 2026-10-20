import {
  type FormEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { endAdminSession, fetchAdminSession, verifyAdminSession } from "@/lib/graphql";
import { useT } from "@/lib/i18n";

// Reusable Admin-Session gate (ADR-0007 §4). An Admin Session is the
// short-lived, owner-adult + admin-password credential that unlocks Console
// operations; it is SEPARATE from the Device Role (a MANAGER browser still must
// open an Admin Session) and PINs never grant it. This component is the single
// place that surfaces the credential gate, so Console (and any other admin
// surface) imports it instead of re-implementing the unlock flow.
//
// Faithful to the vanilla console-unlock (internal/web/static/app.js): on mount
// it asks the server whether a session is already active; when active it renders
// the gated children plus a Lock affordance; when inactive it renders the
// password form. The admin password is a CREDENTIAL — never logged, cleared from
// state on every submit. The server returns ONE generic rejection for every
// failure (wrong password, ineligible Profile, throttle), so we surface a single
// fixed message and never reveal which condition failed.

export interface AdminSessionRenderProps {
  /** End the active Admin Session (the Lock affordance). */
  endAdminSession: () => Promise<void>;
}

export function AdminSessionGate({
  children,
  /** Optional override for the gated-content lead (defaults to the shared key). */
  activeLead,
}: {
  /**
   * The admin-gated content. Rendered only while the Admin Session is active.
   * Receives `endAdminSession` so the consumer can place its own Lock control
   * (a default Lock button is also rendered above the children).
   */
  children: ReactNode | ((props: AdminSessionRenderProps) => ReactNode);
  activeLead?: string;
}) {
  const t = useT();
  const passwordID = useId();
  const errorID = useId();
  const errorRef = useRef<HTMLParagraphElement>(null);
  // null = still resolving the initial adminSession query; the gate shows
  // nothing actionable until the server's verdict lands (admin is never a
  // hidden mode — but it is also never assumed-active before we know).
  const [active, setActive] = useState<boolean | null>(null);
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  // A handshake that failed AFTER the password was accepted: the session is
  // live, so this is a notice on the unlocked view, never an unlock error.
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const info = await fetchAdminSession();
      setActive(info.active);
    } catch {
      // A failed query is treated as locked: the password is the only way in.
      setActive(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const lock = useCallback(async () => {
    try {
      await endAdminSession();
    } catch {
      // Best-effort; the local lock still takes effect below.
    }
    setActive(false);
    setNotice(null);
  }, []);

  async function onUnlock(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    const candidate = password;
    if (!candidate) return;
    setBusy(true);
    setError(null);
    try {
      const info = await verifyAdminSession(candidate);
      // Clear the credential from state regardless of the verdict.
      setPassword("");
      setActive(info.active);
      setNotice(info.rebind === "rebind-failed" ? t("adminUnlockHandshakeIncomplete") : null);
      if (!info.active) {
        setError(t("adminUnlockRejected"));
        requestAnimationFrame(() => errorRef.current?.focus());
      }
    } catch {
      setPassword("");
      setError(t("adminUnlockRejected"));
      requestAnimationFrame(() => errorRef.current?.focus());
    } finally {
      setBusy(false);
    }
  }

  // Initial query still in flight: render nothing rather than flash the form.
  if (active === null) {
    return null;
  }

  if (active) {
    const rendered =
      typeof children === "function" ? children({ endAdminSession: lock }) : children;
    return (
      <div>
        <div className="mb-4 flex items-center justify-between gap-2">
          <p className="text-sm text-muted-foreground">{activeLead ?? t("consoleActiveLead")}</p>
          <Button type="button" variant="outline" size="sm" onClick={lock}>
            {t("adminLock")}
          </Button>
        </div>
        {notice && (
          <p role="status" className="mb-4 text-sm text-muted-foreground">
            {notice}
          </p>
        )}
        {rendered}
      </div>
    );
  }

  return (
    <form onSubmit={onUnlock} className="max-w-md space-y-4" noValidate>
      <div className="space-y-1.5">
        <Label htmlFor={passwordID}>{t("adminPasswordLabel")}</Label>
        <Input
          id={passwordID}
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          // The admin password is a credential, but it IS a password: leave
          // the browser's current-password autofill available, and never
          // spellcheck/autocapitalize it.
          autoComplete="current-password"
          autoCapitalize="off"
          autoCorrect="off"
          spellCheck={false}
          autoFocus
          required
          aria-invalid={error != null}
          aria-describedby={error ? errorID : undefined}
        />
      </div>
      <Button type="submit" disabled={busy}>
        {t("adminUnlockSubmit")}
      </Button>
      {error && (
        <p
          ref={errorRef}
          id={errorID}
          role="alert"
          tabIndex={-1}
          className="text-sm text-destructive outline-none"
        >
          {error}
        </p>
      )}
    </form>
  );
}
