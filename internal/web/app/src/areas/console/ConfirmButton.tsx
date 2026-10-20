import { type ReactNode, useState } from "react";
import {
  AlertDialog,
  AlertDialogAction,
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
export function ConfirmButton({
  label,
  confirm,
  onConfirm,
  variant = "destructive",
  size,
  disabled,
  children,
}: {
  label: ReactNode;
  confirm: StringKey;
  onConfirm: () => void | Promise<void>;
  variant?: "destructive" | "outline" | "secondary" | "default";
  size?: "sm" | "default";
  disabled?: boolean;
  /** Optional confirm-dialog body beyond the prompt (e.g. a name preview). */
  children?: ReactNode;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);

  async function run() {
    setBusy(true);
    try {
      await onConfirm();
    } finally {
      setBusy(false);
      setOpen(false);
    }
  }

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger asChild>
        <Button variant={variant} size={size} disabled={disabled}>
          {label}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t(confirm)}</AlertDialogTitle>
          <AlertDialogDescription className={children ? undefined : "sr-only"}>
            {children || t(confirm)}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>{t("cancel")}</AlertDialogCancel>
          <AlertDialogAction
            disabled={busy}
            onClick={(e) => {
              e.preventDefault();
              void run();
            }}
          >
            {label}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
