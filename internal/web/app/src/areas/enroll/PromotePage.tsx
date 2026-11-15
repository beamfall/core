import { type FormEvent, useId, useRef, useState } from "react";
import { useNavigate } from "react-router";
import {
  FieldHint,
  FieldInput,
  FieldLabel,
  GlassCard,
  PageShell,
  PrimaryAction,
  SecondaryAction,
  SurfaceHeading,
} from "@/components/beamfall/surface";
import { ApiError } from "@/lib/api";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { FormFeedback, normalizePairingCode, PAIRING_CODE_INPUT } from "./AuthShell";
import { pairingCodeErrorMessage } from "./EnrollPage";

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
// MANAGER); the hint under the field says where manager codes are minted.
export function PromotePage() {
  const t = useT();
  const navigate = useNavigate();
  const { promote } = useSession();
  const codeID = useId();
  const errorID = useId();
  const hintID = useId();
  const errorRef = useRef<HTMLParagraphElement>(null);
  const codeRef = useRef<HTMLInputElement>(null);
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    if (!code) {
      showError(t("pairingCodeRequired"));
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await promote(code);
      navigate("/", { replace: true });
    } catch (err) {
      setBusy(false);
      showError(
        err instanceof ApiError && err.status === 403
          ? t("promoteNeedsManagerCode")
          : pairingCodeErrorMessage(err, t, "promoteRetry"),
      );
    }
    // On success we navigate away; keep the form disabled until then.
  }

  // The alert announces the problem; focus returns to the code, selected.
  function showError(message: string) {
    setError(message);
    requestAnimationFrame(() => {
      codeRef.current?.focus();
      codeRef.current?.select();
    });
  }

  return (
    <PageShell>
      <GlassCard>
        <SurfaceHeading title={t("promoteTitle")} lead={t("promoteLead")} />
        <form onSubmit={onSubmit} className="space-y-5" noValidate>
          <div>
            <FieldLabel htmlFor={codeID}>{t("pairingCode")}</FieldLabel>
            <FieldInput
              id={codeID}
              ref={codeRef}
              className={PAIRING_CODE_INPUT}
              value={code}
              onChange={(e) => {
                setCode(normalizePairingCode(e.target.value));
                setError(null);
              }}
              autoComplete="off"
              autoCapitalize="off"
              autoCorrect="off"
              spellCheck={false}
              autoFocus
              required
              aria-invalid={error != null}
              aria-describedby={error ? `${hintID} ${errorID}` : hintID}
            />
            <FieldHint id={hintID} className="mt-2">
              {t("promoteCodeHint", {
                page: t("consoleAccessGroup"),
                action: t("deviceAddHeading"),
                kind: t("deviceMintManager"),
              })}
            </FieldHint>
          </div>
          <FormFeedback error={error} errorID={errorID} errorRef={errorRef} />
          <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
            <SecondaryAction disabled={busy} onClick={() => navigate("/", { replace: true })}>
              {t("cancel")}
            </SecondaryAction>
            <PrimaryAction type="submit" disabled={busy} className="sm:min-w-[140px]">
              {t("promote")}
            </PrimaryAction>
          </div>
        </form>
      </GlassCard>
    </PageShell>
  );
}
