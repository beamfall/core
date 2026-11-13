import { type FormEvent, useId, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ApiError } from "@/lib/api";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { cn } from "@/lib/utils";
import {
  AuthHeading,
  AuthShell,
  AUTH_FIELD as FIELD,
  AUTH_FIELD_LABEL as FIELD_LABEL,
  FormFeedback,
} from "./AuthShell";

// Browser→MANAGER promotion (ADR-0007 §3): redeem a MANAGER pairing code for
// this already-enrolled device. Cookies are untouched — the Device Role changes
// server-side. The session spine owns the promotion so it clears and aborts
// every previous-role request before the mutation, then reloads the new role;
// the manager affordances (Manage, Console) light up everywhere at once.
//
// Per ADR-0007, PINs never grant admin and a pairing code is the only browser
// promotion path; the manager password is a separate Admin-Session credential
// (see AdminSessionGate), not used here. The pairing code is a CREDENTIAL: never
// logged, never echoed back into the error message. The server distinguishes
// 401 (invalid/expired) from 403 (a valid VIEWER code that does not grant
// MANAGER); every other failure is a generic "promotion failed".
export function PromotePage() {
  const t = useT();
  const navigate = useNavigate();
  const { promote } = useSession();
  const codeID = useId();
  const errorID = useId();
  const errorRef = useRef<HTMLParagraphElement>(null);
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      await promote(code.trim());
      navigate("/", { replace: true });
    } catch (err) {
      const message =
        err instanceof ApiError
          ? err.status === 401
            ? t("pairingCodeInvalid")
            : err.status === 403
              ? t("pairingCodeManagerOnly")
              : t("promotionFailed")
          : t("couldNotReachServer");
      setError(message);
      setBusy(false);
      requestAnimationFrame(() => errorRef.current?.focus());
    }
    // On success we navigate away; keep the form disabled until then.
  }

  return (
    <AuthShell>
      <AuthHeading title={t("promoteTitle")} lead={t("promoteLead")} />
      <form onSubmit={onSubmit} className="space-y-5" noValidate>
        <div>
          <Label htmlFor={codeID} className={FIELD_LABEL}>
            {t("pairingCode")}
          </Label>
          <Input
            id={codeID}
            className={cn(FIELD, "text-center font-mono text-xl tracking-[0.3em]")}
            value={code}
            onChange={(e) => setCode(e.target.value)}
            autoComplete="off"
            autoCapitalize="off"
            autoCorrect="off"
            spellCheck={false}
            autoFocus
            required
            aria-invalid={error != null}
            aria-describedby={error ? errorID : undefined}
          />
        </div>
        <FormFeedback error={error} errorID={errorID} errorRef={errorRef} />
        <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <Button
            type="button"
            variant="ghost"
            className="h-11 rounded-[10px] px-5"
            disabled={busy}
            onClick={() => navigate("/", { replace: true })}
          >
            {t("cancel")}
          </Button>
          <Button
            type="submit"
            disabled={busy}
            className="h-11 rounded-[10px] px-6 text-[15px] font-semibold sm:min-w-[140px]"
          >
            {t("promote")}
          </Button>
        </div>
      </form>
    </AuthShell>
  );
}
