import { type FormEvent, useState } from "react";
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
import { prepareRenderFocusRestore } from "../lib/renderFocus";

// The Reveal contract affordances (ADR-0004), rendered into the AppShell action
// cluster. When relocked, a "Restrictions" control opens a PIN dialog that calls
// reveal(pin); when revealed, a "Relock" control drops restricted content from
// every view at once (no PIN). The AppShell renders the "Revealed" chip itself,
// so this component only owns the entry/exit controls.
//
// The PIN goes into reveal(pin) and nowhere else — never logged or persisted,
// and the field is cleared as soon as it is read. Error copy stays calm and
// fixed; the throttle message does not leak security internals.
export function RevealControls() {
  const t = useT();
  const { revealed, reveal, relock } = useSession();
  const [open, setOpen] = useState(false);
  const [pin, setPin] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function onReveal(e: FormEvent) {
    e.preventDefault();
    setError(null);
    const entered = pin;
    setPin("");
    if (!entered) return;
    setBusy(true);
    try {
      prepareRenderFocusRestore();
      const ok = await reveal(entered);
      if (ok) {
        setOpen(false);
      } else {
        setError(t("pinIncorrect"));
      }
    } catch (err) {
      setError(
        err instanceof Error && err.message.includes("throttled")
          ? t("tooManyAttempts")
          : t("pinIncorrect"),
      );
    } finally {
      setBusy(false);
    }
  }

  async function onRelock() {
    setBusy(true);
    try {
      prepareRenderFocusRestore();
      await relock();
    } finally {
      setBusy(false);
    }
  }

  if (revealed) {
    return (
      <Button variant="outline" size="sm" onClick={onRelock} disabled={busy}>
        {t("relock")}
      </Button>
    );
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        setError(null);
        setPin("");
      }}
    >
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
        {t("restrictions")}
      </Button>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("restrictionsTitle")}</DialogTitle>
          <DialogDescription>{t("restrictionsLead")}</DialogDescription>
        </DialogHeader>
        <form onSubmit={onReveal} className="flex flex-col gap-3">
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
          {error && <p className="text-sm text-[var(--red-500)]">{error}</p>}
          <DialogFooter>
            <Button type="submit" disabled={busy}>
              {t("reveal")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
