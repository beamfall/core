import { type FormEvent, useId, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ApiError } from "@/lib/api";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";

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
    <main className="mx-auto max-w-md px-4 py-12">
      <h1 className="text-2xl font-semibold">{t("promoteTitle")}</h1>
      <p className="mt-2 text-sm text-muted-foreground">{t("promoteLead")}</p>
      <form onSubmit={onSubmit} className="mt-6 space-y-4" noValidate>
        <div className="space-y-1.5">
          <Label htmlFor={codeID}>{t("pairingCode")}</Label>
          <Input
            id={codeID}
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
        <div className="flex items-center gap-2">
          <Button type="submit" disabled={busy}>
            {t("promote")}
          </Button>
          <Button
            type="button"
            variant="ghost"
            disabled={busy}
            onClick={() => navigate("/", { replace: true })}
          >
            {t("cancel")}
          </Button>
        </div>
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
    </main>
  );
}
