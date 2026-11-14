import { type ReactNode, useRef, useState } from "react";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";

// Every destructive Console op (terminate a session, revoke a token/key, run a
// health bulk op) routes through a confirm dialog (HARD invariant #6 / task
// constraint). The label IS the action; `confirm` is the localized prompt.
//
// The dialog closes only once the action resolves. A rejected action keeps it
// open, says so (with the reason when the error carries one) and turns the
// action into a retry, so a failed destructive action never looks like a no-op.
export function ConfirmButton({
  label,
  actionLabel,
  confirm,
  onConfirm,
  variant = "destructive",
  size,
  disabled,
  children,
}: {
  label: ReactNode;
  /** Confirm-dialog action text when `label` is icon-only; defaults to `label`. */
  actionLabel?: ReactNode;
  confirm: StringKey;
  onConfirm: () => void | Promise<void>;
  variant?: "destructive" | "outline" | "secondary" | "default" | "ghost";
  size?: "sm" | "default";
  disabled?: boolean;
  /** Optional confirm-dialog body beyond the prompt (e.g. a name preview). */
  children?: ReactNode;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  // null: no failure. Otherwise the reason, which may be empty.
  const [error, setError] = useState<string | null>(null);
  const inFlight = useRef(false);

  function onOpenChange(next: boolean) {
    // An action in flight cannot be cancelled, so the dialog waits for its answer.
    if (inFlight.current) return;
    setOpen(next);
    if (next) setError(null);
  }

  async function run() {
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setError(null);
    try {
      await onConfirm();
    } catch (err) {
      setError(err instanceof Error ? err.message : "");
      return;
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
    setOpen(false);
  }

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogTrigger asChild>
        <Button
          variant={variant === "destructive" ? "danger" : variant}
          size={size}
          disabled={disabled}
        >
          {label}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent aria-busy={busy || undefined}>
        <AlertDialogHeader>
          <AlertDialogTitle>{t(confirm)}</AlertDialogTitle>
          <AlertDialogDescription className={children ? undefined : "sr-only"}>
            {children || t(confirm)}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div role="alert" className="text-sm text-destructive empty:hidden">
          {error !== null && (
            <>
              <p className="m-0 font-medium">{t("requestFailed")}</p>
              {error && <p className="m-0 mt-0.5 break-words text-xs">{error}</p>}
            </>
          )}
        </div>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>{t("cancel")}</AlertDialogCancel>
          {/* A plain button, not a dialog Close: the dialog stays open until the
              action answers, so a failure can say why. aria-disabled keeps focus
              on the button while the action runs. */}
          <Button
            type="button"
            variant={variant === "destructive" ? "destructive" : "default"}
            aria-disabled={busy || undefined}
            onClick={() => void run()}
          >
            {error !== null ? t("tryAgain") : (actionLabel ?? label)}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
