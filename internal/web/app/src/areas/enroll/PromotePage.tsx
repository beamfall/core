import { type FormEvent, useId, useRef, useState } from "react";
import { useNavigate } from "react-router";
import beamfallLogo from "@/assets/beamfall-logo.png";
import { CinemaBackdrop } from "@/components/beamfall/CinemaBackdrop";
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
    <main className="theme-cinema relative isolate flex min-h-screen flex-col bg-background text-foreground">
      <CinemaBackdrop />
      <header className="flex items-center gap-2.5 px-5 py-4 sm:px-8 sm:py-6">
        <img src={beamfallLogo} alt="" width={28} height={28} className="shrink-0" />
        <span className="text-base font-semibold tracking-tight">{t("brand")}</span>
      </header>

      <div className="flex flex-1 items-start justify-center px-4 pb-12 pt-2 sm:items-center sm:px-8 sm:pt-0">
        <div className="w-full max-w-[460px] rounded-2xl border border-white/10 bg-card p-6 shadow-[0_24px_60px_-28px_rgba(0,0,0,0.7)] sm:p-8">
          <h1 className="text-2xl font-semibold tracking-tight">{t("promoteTitle")}</h1>
          <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{t("promoteLead")}</p>
          <form onSubmit={onSubmit} className="mt-6 space-y-5" noValidate>
            <div>
              <Label
                htmlFor={codeID}
                className="mb-2 block text-[13px] font-medium text-foreground/80"
              >
                {t("pairingCode")}
              </Label>
              <Input
                id={codeID}
                className="h-11 rounded-xl border-white/10 bg-white/[0.04] text-[15px]"
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
              <Button
                type="submit"
                disabled={busy}
                className="h-11 rounded-xl px-5 text-[15px] font-semibold"
              >
                {t("promote")}
              </Button>
              <Button
                type="button"
                variant="ghost"
                className="h-11 rounded-xl px-4"
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
        </div>
      </div>
    </main>
  );
}
