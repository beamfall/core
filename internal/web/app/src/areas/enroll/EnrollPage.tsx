import { ArrowRight, ChevronRight } from "lucide-react";
import { type FormEvent, useEffect, useId, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ApiError, fetchSetupStatus, type SignInSecurityMode } from "@/lib/api";
import { adminSignIn, enroll } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import {
  AuthHeading,
  AuthShell,
  AUTH_FIELD as FIELD,
  AUTH_FIELD_LABEL as FIELD_LABEL,
  FormFeedback,
} from "./AuthShell";
import { PairingCodeHelp } from "./PairingCodeHelp";

// First contact (ADR-0007 §2): a browser enrolls as a VIEWER device with a
// pairing code minted on a manager device. Faithful to the vanilla enroll-view
// (internal/web/static/app.js): the pairing code is trimmed, the device name
// defaults to "Web Browser", and a successful enroll reloads to BASE_URL so the
// session spine re-bootstraps against the freshly-bound session.
//
// The pairing code is a CREDENTIAL: it is never logged and never echoed back
// into the error message. The server returns 401 for an invalid/expired code;
// every other failure is a generic "enrollment failed" — matching the vanilla
// app, which is deliberately careful not to leak why a code was rejected.
//
// The code is consumed the moment the server accepts it, so the success is
// committed (status line + redirect) as soon as enroll() resolves; the
// compatibility rebind that follows can only produce a NOTICE, never an
// enrollment error — the fresh load re-runs the handshake anyway.
//
// The page also has to answer "what is a pairing code and where do I get one?"
// for someone who has never seen this server (CORE-1196): PairingCodeHelp says
// so in plain words under the form.
export function EnrollPage() {
  const t = useT();
  const codeID = useId();
  const deviceNameID = useId();
  const errorID = useId();
  const codeHintID = useId();
  const deviceNameHintID = useId();
  const errorRef = useRef<HTMLParagraphElement>(null);
  const [code, setCode] = useState("");
  const [deviceName, setDeviceName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // The owner's sign-in security choice (CORE-1110). Strict turns off
  // admin-password sign-in, so the form stays hidden; it also stays hidden
  // until the probe settles so it never flashes on a Strict server.
  const [securityMode, setSecurityMode] = useState<SignInSecurityMode | null>(null);

  useEffect(() => {
    let cancelled = false;
    void fetchSetupStatus().then((status) => {
      if (!cancelled) setSecurityMode(status.securityMode);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      // Trim the code (a pasted code often carries whitespace); fall back to
      // the localized "Web Browser" device name when the operator leaves it
      // blank, exactly as the vanilla enroll form does.
      const rebind = await enroll(code.trim(), deviceName.trim() || t("webBrowser"));
      // Success: reload to BASE_URL so SessionProvider re-bootstraps the
      // now-enrolled session (the vanilla app calls init(); the React app
      // re-runs the whole spine from a fresh load). A failed rebind is its own
      // notice — the browser IS enrolled — and never blocks the redirect.
      setStatus(rebind === "bound" ? t("enrollConnected") : t("enrollHandshakeIncomplete"));
      window.location.assign(import.meta.env.BASE_URL);
    } catch (err) {
      // 401 = invalid/expired pairing code; anything else is a generic
      // failure. A thrown non-ApiError (network down) is "could not reach".
      const message =
        err instanceof ApiError
          ? err.status === 401
            ? t("pairingCodeInvalid")
            : t("enrollmentFailed")
          : t("couldNotReachServer");
      setError(message);
      setBusy(false);
      // Move focus to the error so assistive tech announces the failure.
      requestAnimationFrame(() => errorRef.current?.focus());
    }
    // No finally{setBusy(false)}: on success the page is navigating away, so
    // the form must stay disabled until the reload lands.
  }

  return (
    <AuthShell>
      <AuthHeading title={t("enrollTitle")} lead={t("signInLead")} />
      <form onSubmit={onSubmit} className="space-y-5" noValidate>
        <div>
          <Label htmlFor={codeID} className={FIELD_LABEL}>
            {t("pairingCode")}
          </Label>
          <Input
            id={codeID}
            value={code}
            onChange={(e) => setCode(e.target.value)}
            className={cn(FIELD, "text-center font-mono text-xl tracking-[0.3em]")}
            // The pairing code is a credential: no autofill, no spellcheck,
            // no autocapitalize that would mangle a case-sensitive code.
            autoComplete="off"
            autoCapitalize="off"
            autoCorrect="off"
            spellCheck={false}
            autoFocus
            required
            aria-invalid={error != null}
            aria-describedby={error ? `${codeHintID} ${errorID}` : codeHintID}
          />
          <p id={codeHintID} className="mb-0 mt-2 text-dense text-muted-foreground">
            {t("signInCodeHint")}
          </p>
        </div>
        <div>
          <Label htmlFor={deviceNameID} className={FIELD_LABEL}>
            {t("deviceName")}
          </Label>
          <Input
            id={deviceNameID}
            value={deviceName}
            onChange={(e) => setDeviceName(e.target.value)}
            className={FIELD}
            placeholder={t("webBrowser")}
            autoComplete="off"
            aria-describedby={deviceNameHintID}
          />
          <p id={deviceNameHintID} className="mb-0 mt-2 text-dense text-muted-foreground">
            {t("signInDeviceNameHint")}
          </p>
        </div>
        <FormFeedback status={status} error={error} errorID={errorID} errorRef={errorRef} />
        <Button
          type="submit"
          disabled={busy}
          className="h-11 w-full rounded-[10px] text-[15px] font-semibold"
        >
          {t("connect")}
          <ArrowRight aria-hidden className="size-4" />
        </Button>
      </form>
      <div className="mt-7 border-t border-white/10 pt-6">
        <PairingCodeHelp />
        {securityMode === "home" && <AdminSignInForm deviceName={deviceName} />}
      </div>
    </AuthShell>
  );
}

// Owner recovery (CORE-1106): sign this browser in with the admin password set
// during first-run setup. The server enrolls a MANAGER device on the owner
// profile. Every refusal is the same 401, so the message never says whether
// the password was wrong or sign-in is unavailable. The password is never
// logged or echoed.
function AdminSignInForm({ deviceName }: { deviceName: string }) {
  const t = useT();
  const passwordID = useId();
  const errorID = useId();
  const errorRef = useRef<HTMLParagraphElement>(null);
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      const rebind = await adminSignIn(password, deviceName.trim() || t("webBrowser"));
      setStatus(rebind === "bound" ? t("enrollConnected") : t("enrollHandshakeIncomplete"));
      window.location.assign(import.meta.env.BASE_URL);
    } catch (err) {
      setError(adminSignInErrorMessage(err, t));
      setPassword("");
      setBusy(false);
      requestAnimationFrame(() => errorRef.current?.focus());
    }
  }

  // Owner recovery is the rare path, so it sits behind a disclosure; a failed
  // attempt keeps it open because the open state lives on the element.
  return (
    <details className="group mt-5">
      <summary className="-mx-1 flex cursor-pointer list-none items-center gap-1.5 rounded-md px-1 py-1 text-sm font-medium text-foreground/90 transition-colors duration-150 hover:text-foreground focus-visible:outline-2 focus-visible:outline-primary [&::-webkit-details-marker]:hidden">
        <ChevronRight
          aria-hidden
          className="size-4 text-muted-foreground transition-transform duration-150 group-open:rotate-90"
        />
        {t("adminSignInTitle")}
      </summary>
      <form onSubmit={onSubmit} className="mt-3 space-y-4" noValidate>
        <p className="m-0 text-sm leading-relaxed text-muted-foreground">{t("adminSignInLead")}</p>
        <div>
          <Label htmlFor={passwordID} className={FIELD_LABEL}>
            {t("adminPasswordLabel")}
          </Label>
          <Input
            id={passwordID}
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className={FIELD}
            autoComplete="current-password"
            required
            aria-invalid={error != null}
            aria-describedby={error ? errorID : undefined}
          />
        </div>
        <FormFeedback status={status} error={error} errorID={errorID} errorRef={errorRef} />
        <Button
          type="submit"
          variant="outline"
          disabled={busy || password === ""}
          className="h-11 w-full rounded-[10px] border-white/15 bg-transparent text-[15px] font-semibold hover:bg-white/[0.06]"
        >
          {t("adminSignIn")}
        </Button>
      </form>
    </details>
  );
}

function adminSignInErrorMessage(err: unknown, t: ReturnType<typeof useT>): string {
  if (!(err instanceof ApiError)) return t("couldNotReachServer");
  if (err.status === 429) return t("tooManyAttempts");
  if (err.status === 403) return t("adminSignInNeedsSecure");
  return t("adminSignInFailed");
}
