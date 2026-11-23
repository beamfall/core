import { Lock, LockOpen } from "lucide-react";
import { type FormEvent, type ReactNode, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import type { StringKey } from "@/strings";
import { createCarriedError } from "../lib/carriedError";
import { prepareRenderFocusRestore } from "../lib/renderFocus";
import { usePlayerHost } from "../player/PlayerHost";

const revealFailure = createCarriedError();
const relockFailure = createCarriedError();

// The Reveal contract affordances (ADR-0004), rendered into the AppShell action
// cluster. When relocked, a "Restrictions" control opens a PIN dialog that calls
// reveal(pin); when revealed, a "Relock" control drops restricted content from
// every view at once (no PIN). The AppShell renders the "Revealed" chip itself,
// so this component only owns the entry/exit controls.
//
// The PIN goes into reveal(pin) and nowhere else — never logged or persisted,
// and the field is cleared as soon as it is read. Error copy stays calm and
// fixed; the throttle message does not leak security internals.
// A profile with no PIN (or a managed profile) has no reveal path: say so
// rather than blaming the PIN that was typed (CORE-1608). When the session says
// no Restrictions PIN is set the entry control is not rendered at all, since no
// PIN could succeed (CORE-2462); Relock always stays reachable.
export function revealErrorKey(err: unknown): StringKey {
  const message = err instanceof Error ? err.message.toLowerCase() : "";
  if (message.includes("throttled")) return "tooManyAttempts";
  if (message.includes("no reveal path")) return "revealNoPin";
  return "pinIncorrect";
}

export function RevealPinDialog({
  open,
  onOpenChange,
  title,
  description,
  hint,
  submitLabel,
  busy = false,
  error = null,
  onSubmitPin,
  trigger,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  hint?: string;
  submitLabel: string;
  busy?: boolean;
  error?: string | null;
  onSubmitPin: (pin: string) => void | Promise<void>;
  trigger?: ReactNode;
}) {
  const t = useT();
  const [pin, setPin] = useState("");

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    const entered = pin;
    setPin("");
    if (!entered) return;
    void onSubmitPin(entered);
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setPin("");
        onOpenChange(next);
      }}
    >
      {trigger}
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
          {hint && <p className="m-0 text-xs text-muted-foreground">{hint}</p>}
        </DialogHeader>
        <form onSubmit={handleSubmit} className="flex flex-col gap-3">
          <label className="flex flex-col gap-1 text-sm">
            {t("pin")}
            <Input
              type="password"
              inputMode="numeric"
              autoComplete="off"
              value={pin}
              onChange={(e) => setPin(e.target.value)}
              autoFocus
            />
          </label>
          {error && <p className="text-sm text-[var(--state-danger)]">{error}</p>}
          <DialogFooter>
            <Button type="submit" disabled={busy}>
              {submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function RevealControls() {
  const t = useT();
  const { session, revealed, reveal, relock } = useSession();
  const playerHost = usePlayerHost();
  // A server that predates revealPinSet leaves it absent: keep the control.
  const pinSet = session?.revealPinSet !== false;
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [relockError, setRelockError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const failure = revealFailure.usePending();
  const pendingRelockFailure = relockFailure.usePending();

  // A failed Reveal is reported through revealFailure: the epoch unmounts this
  // instance while the PIN is checked, so the dialog and its error reopen here.
  useEffect(() => {
    if (!failure) return;
    revealFailure.carry(null);
    setError(t(failure));
    setOpen(true);
  }, [failure, t]);

  useEffect(() => {
    if (!pendingRelockFailure) return;
    relockFailure.carry(null);
    setRelockError(t(pendingRelockFailure));
  }, [pendingRelockFailure, t]);

  useEffect(() => {
    if (!revealed) setRelockError(null);
  }, [revealed]);

  async function onReveal(entered: string) {
    setError(null);
    revealFailure.carry(null);
    setBusy(true);
    try {
      prepareRenderFocusRestore();
      const ok = await reveal(entered);
      if (ok) {
        setOpen(false);
      } else {
        revealFailure.carry("pinIncorrect");
      }
    } catch (err) {
      revealFailure.carry(revealErrorKey(err));
    } finally {
      setBusy(false);
    }
  }

  async function onRelock() {
    // Relock is a privacy action: it ends all playback, full or docked, before
    // the session changes. Nothing the old session was playing outlives it.
    playerHost?.stop();
    setRelockError(null);
    relockFailure.carry(null);
    setBusy(true);
    try {
      prepareRenderFocusRestore();
      await relock();
    } catch {
      relockFailure.carry("relockFailed");
    } finally {
      setBusy(false);
    }
  }

  if (revealed) {
    return (
      <>
        <button
          type="button"
          className="bf-icon-button bf-reveal-open"
          onClick={onRelock}
          disabled={busy}
          aria-label={t("relock")}
          title={t("relock")}
        >
          <LockOpen aria-hidden className="size-[18px]" />
        </button>
        {relockError && (
          <span role="alert" className="text-xs text-[var(--state-danger)]">
            {relockError}
          </span>
        )}
      </>
    );
  }

  if (!pinSet) return null;

  return (
    <RevealPinDialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        setError(null);
      }}
      title={t("restrictionsTitle")}
      description={t("restrictionsLead")}
      hint={t("restrictionsNoPinHint")}
      submitLabel={t("reveal")}
      busy={busy}
      error={error}
      onSubmitPin={onReveal}
      trigger={
        <button
          type="button"
          className="bf-icon-button"
          onClick={() => setOpen(true)}
          aria-label={t("restrictions")}
          title={t("restrictions")}
        >
          <Lock aria-hidden className="size-[18px]" />
        </button>
      }
    />
  );
}
