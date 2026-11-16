import { useCallback, useEffect, useRef, useState } from "react";
import { withAdminEpoch } from "@/areas/console/adminEpoch";
import { ErrorLine, ManageSection } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { ApiError, getIdentityEpochSignal } from "@/lib/api";
import {
  acknowledgeBackupKey,
  fetchAdminSession,
  fetchBackupKeyStatus,
  revealBackupKey,
} from "@/lib/graphql";
import type { BackupKeyStatusInfo, RevealedBackupKeyInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";

// Backup key (CORE-2032): while the server's bootstrap backup key is not yet
// saved, the owner reveals it here in an Admin Session, saves it, and confirms.
// The key lives only in this component's state: it is never written to
// storage, a URL, a shared cache or a log, and it is dropped on confirm, on
// unmount, on an identity change and the moment the Admin Session ends.
//
// The section renders on /console/backups, where AdminGate publishes the
// Admin-Session epoch, and on /manage/backups, where no epoch is published
// (the Manage gate only prompts after a refusal). So the revealed key keeps
// its own session lifecycle on every route: it reads the Admin Session's
// expiry, drops the key when that window lapses, and re-reads the session
// while the key is on screen so a lock from anywhere drops it too.

// How often the Admin Session is re-read while the key is on screen.
export const BACKUP_KEY_SESSION_RECHECK_MS = 15_000;

// useAdminScope composes this component's lifetime with the current
// Admin-Session epoch. A signal it returns aborts on unmount or when the
// Admin Session ends; `mounted` guards writes from abort listeners.
function useAdminScope() {
  const lifetime = useRef<AbortController | null>(null);
  const mounted = useRef(false);
  useEffect(() => {
    const controller = new AbortController();
    lifetime.current = controller;
    mounted.current = true;
    return () => {
      mounted.current = false;
      controller.abort();
    };
  }, []);
  const scope = useCallback(() => {
    const controller = lifetime.current ?? new AbortController();
    return withAdminEpoch(controller.signal);
  }, []);
  return { scope, mounted };
}

export function BackupKeySection() {
  const t = useT();
  const { scope, mounted } = useAdminScope();
  const [status, setStatus] = useState<BackupKeyStatusInfo | null>(null);
  const [revealed, setRevealed] = useState<RevealedBackupKeyInfo | null>(null);
  const [saved, setSaved] = useState(false);
  const [copied, setCopied] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState<"" | "reveal" | "confirm">("");
  const [error, setError] = useState("");

  const loadStatus = useCallback(() => {
    const signal = scope();
    fetchBackupKeyStatus(signal)
      .then((next) => {
        if (!signal.aborted) setStatus(next);
      })
      .catch(() => {
        if (!signal.aborted) setError(t("backupKeyUnavailable"));
      });
  }, [scope, t]);

  useEffect(() => {
    loadStatus();
  }, [loadStatus]);

  const forgetKey = useCallback(() => {
    setRevealed(null);
    setSaved(false);
    setCopied(false);
  }, []);

  // While a key is on screen, bind it to the Admin Session: the Console epoch
  // (when published), the identity epoch, the session's own expiry and a
  // periodic re-read. Any of them ending drops the key; a failed read does too.
  const revealedEscrow = revealed?.escrowID ?? "";
  useEffect(() => {
    if (!revealedEscrow) return;
    const own = new AbortController();
    const identity = getIdentityEpochSignal();
    const watch = AbortSignal.any(
      identity ? [scope(), identity, own.signal] : [scope(), own.signal],
    );
    let disposed = false;
    let expiryTimer: number | undefined;
    const drop = () => {
      if (!disposed && mounted.current) forgetKey();
    };
    const check = () => {
      fetchAdminSession(watch)
        .then((session) => {
          if (watch.aborted) return;
          const left = session.expiresAt ? Date.parse(session.expiresAt) - Date.now() : Number.NaN;
          if (!session.active || !(left > 0)) {
            drop();
            return;
          }
          window.clearTimeout(expiryTimer);
          if (left <= 2_147_483_647) expiryTimer = window.setTimeout(drop, left);
        })
        .catch(() => {
          if (!watch.aborted) drop();
        });
    };
    watch.addEventListener("abort", drop);
    check();
    const recheck = window.setInterval(check, BACKUP_KEY_SESSION_RECHECK_MS);
    return () => {
      disposed = true;
      window.clearTimeout(expiryTimer);
      window.clearInterval(recheck);
      own.abort();
    };
  }, [revealedEscrow, scope, mounted, forgetKey]);

  const onReveal = async () => {
    setError("");
    setBusy("reveal");
    const signal = scope();
    try {
      const next = await revealBackupKey(signal);
      if (signal.aborted) return;
      setRevealed(next);
    } catch (err) {
      if (!signal.aborted) setError(refusal(err, t("backupKeyUnavailable")));
    } finally {
      if (!signal.aborted) setBusy("");
    }
  };

  const onConfirm = async () => {
    if (!revealed) return;
    setError("");
    setBusy("confirm");
    const signal = scope();
    try {
      const next = await acknowledgeBackupKey(revealed.escrowID, signal);
      if (signal.aborted) return;
      forgetKey();
      setStatus(next);
      setConfirmed(true);
    } catch (err) {
      if (signal.aborted) return;
      setError(refusal(err, t("backupKeyFailed")));
      // A refusal means this key can no longer be confirmed (it changed or the
      // window lapsed): drop it and reload the window the server now reports.
      if (err instanceof ApiError) {
        forgetKey();
        loadStatus();
      }
    } finally {
      if (!signal.aborted) setBusy("");
    }
  };

  const onCopy = async () => {
    if (!revealed || !navigator.clipboard) return;
    try {
      await navigator.clipboard.writeText(revealed.backupKey);
      if (mounted.current) setCopied(true);
    } catch {
      // Clipboard refused (permissions); the key stays on screen to copy by hand.
    }
  };

  if (!confirmed && !status?.pending && !error) return null;

  return (
    <ManageSection title={t("backupKeyHeading")} lead={t("backupKeyLead")}>
      <div className="space-y-3">
        {confirmed ? (
          <p role="status" className="text-sm">
            {t("backupKeyConfirmed")}
          </p>
        ) : status?.pending ? (
          <div className="space-y-3 rounded-md border border-[var(--state-warning)]/45 bg-[var(--state-warning)]/5 px-3 py-3">
            <p className="text-sm font-semibold">{t("backupKeyPending")}</p>
            {revealed ? (
              <div className="space-y-3">
                <p className="text-sm font-semibold">{t("backupKeyLabel")}</p>
                <p
                  aria-label={t("backupKeyLabel")}
                  className="manage-cell-num break-all rounded-md border bg-muted/40 px-3 py-2 text-sm"
                >
                  {revealed.backupKey}
                </p>
                <div className="flex flex-wrap gap-2">
                  <Button type="button" variant="outline" onClick={onCopy}>
                    {copied ? t("backupKeyCopied") : t("backupKeyCopy")}
                  </Button>
                  <Button type="button" variant="outline" onClick={() => window.print()}>
                    {t("backupKeyPrint")}
                  </Button>
                </div>
                <p className="text-sm text-muted-foreground">{t("backupKeyNotStored")}</p>
                <label className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={saved}
                    onChange={(e) => setSaved(e.target.checked)}
                  />
                  {t("backupKeySavedCheck")}
                </label>
                <Button type="button" onClick={onConfirm} disabled={busy !== "" || !saved}>
                  {busy === "confirm" ? t("backupKeyConfirming") : t("backupKeyConfirm")}
                </Button>
              </div>
            ) : (
              <Button type="button" onClick={onReveal} disabled={busy !== ""}>
                {busy === "reveal" ? t("backupKeyRevealing") : t("backupKeyReveal")}
              </Button>
            )}
          </div>
        ) : null}
        <ErrorLine message={error} />
      </div>
    </ManageSection>
  );
}

// A server refusal is admin-safe text; anything else falls back to page copy.
function refusal(err: unknown, fallback: string): string {
  return err instanceof ApiError && err.message ? err.message : fallback;
}
