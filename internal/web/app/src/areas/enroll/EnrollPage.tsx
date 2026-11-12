import {
  AlertCircle,
  ArrowRight,
  KeyRound,
  type LucideIcon,
  MonitorSmartphone,
} from "lucide-react";
import { type FormEvent, type RefObject, useEffect, useId, useRef, useState } from "react";
import beamfallLogo from "@/assets/beamfall-logo.png";
import { CinemaBackdrop } from "@/components/beamfall/CinemaBackdrop";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ApiError, fetchSetupStatus, type SignInSecurityMode } from "@/lib/api";
import { adminSignIn, enroll } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import { PairingCodeHelp } from "./PairingCodeHelp";

// The house form look (CORE-1114 setup wizard), shared by both sign-in paths.
const FIELD = "h-11 rounded-xl border-white/10 bg-white/[0.04] text-[15px]";
const FIELD_LABEL = "mb-2 block text-[13px] font-medium text-foreground/80";

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
    <main className="theme-cinema relative isolate flex min-h-screen flex-col bg-background text-foreground">
      <CinemaBackdrop />
      <header className="flex items-center gap-2.5 px-5 py-4 sm:px-8 sm:py-6">
        <img src={beamfallLogo} alt="" width={28} height={28} className="shrink-0" />
        <span className="text-base font-semibold tracking-tight">{t("brand")}</span>
      </header>

      <div className="flex flex-1 items-start justify-center px-4 pb-12 pt-2 sm:items-center sm:px-8 sm:pt-0">
        <div className="animate-in fade-in slide-in-from-bottom-2 w-full max-w-[460px] rounded-2xl border border-white/10 bg-card/70 p-6 shadow-[0_30px_80px_-20px_rgba(0,0,0,0.7)] backdrop-blur-xl duration-300 sm:p-8">
          <CardHeading
            icon={MonitorSmartphone}
            title={t("enrollTitle")}
            lead={t("signInLead")}
            level={1}
          />
          <form onSubmit={onSubmit} className="space-y-5" noValidate>
            <div>
              <Label htmlFor={codeID} className={FIELD_LABEL}>
                {t("pairingCode")}
              </Label>
              <Input
                id={codeID}
                value={code}
                onChange={(e) => setCode(e.target.value)}
                className={cn(FIELD, "h-14 text-center font-mono text-xl tracking-[0.3em]")}
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
              <p id={codeHintID} className="mb-0 mt-2 text-xs text-muted-foreground">
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
              <p id={deviceNameHintID} className="mb-0 mt-2 text-xs text-muted-foreground">
                {t("signInDeviceNameHint")}
              </p>
            </div>
            <Button
              type="submit"
              disabled={busy}
              className="h-11 w-full rounded-full text-[15px] font-semibold shadow-[0_8px_30px_-8px_var(--accent)]"
            >
              {t("connect")}
              <ArrowRight aria-hidden className="size-4" />
            </Button>
            <FormFeedback status={status} error={error} errorID={errorID} errorRef={errorRef} />
          </form>
          <PairingCodeHelp />
          {securityMode === "home" && <AdminSignInForm deviceName={deviceName} />}
        </div>
      </div>
    </main>
  );
}

function CardHeading({
  icon: Icon,
  title,
  lead,
  level,
  id,
}: {
  icon: LucideIcon;
  title: string;
  lead: string;
  level: 1 | 2;
  id?: string;
}) {
  const Heading = level === 1 ? "h1" : "h2";
  return (
    <div className="mb-6 flex gap-4">
      <span
        className={cn(
          "flex shrink-0 items-center justify-center rounded-xl bg-primary/15 text-primary ring-1 ring-primary/25",
          level === 1 ? "size-11 shadow-[0_0_28px_-6px_var(--accent)]" : "size-9",
        )}
      >
        <Icon aria-hidden className={level === 1 ? "size-5" : "size-4"} />
      </span>
      <div>
        <Heading
          id={id}
          className={cn(
            "m-0 font-semibold tracking-tight",
            level === 1 ? "text-2xl sm:text-[26px] sm:leading-tight" : "text-base",
          )}
        >
          {title}
        </Heading>
        <p className="mb-0 mt-1.5 text-sm leading-relaxed text-muted-foreground">{lead}</p>
      </div>
    </div>
  );
}

// The status line and the focusable error alert under each form.
function FormFeedback({
  status,
  error,
  errorID,
  errorRef,
}: {
  status: string | null;
  error: string | null;
  errorID: string;
  errorRef: RefObject<HTMLParagraphElement | null>;
}) {
  return (
    <>
      {status && (
        <p role="status" className="m-0 text-center text-sm text-muted-foreground">
          {status}
        </p>
      )}
      {error && (
        <p
          ref={errorRef}
          id={errorID}
          role="alert"
          tabIndex={-1}
          className="m-0 flex items-center gap-2 rounded-xl border border-destructive/30 bg-destructive/10 px-3.5 py-2.5 text-sm text-destructive outline-none focus-visible:ring-2 focus-visible:ring-destructive/40"
        >
          <AlertCircle aria-hidden className="size-4 shrink-0" />
          {error}
        </p>
      )}
    </>
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

  return (
    <section className="mt-8 border-t border-white/10 pt-7" aria-labelledby={`${passwordID}-title`}>
      <CardHeading
        icon={KeyRound}
        title={t("adminSignInTitle")}
        lead={t("adminSignInLead")}
        level={2}
        id={`${passwordID}-title`}
      />
      <form onSubmit={onSubmit} className="space-y-5" noValidate>
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
        <Button
          type="submit"
          variant="secondary"
          disabled={busy || password === ""}
          className="h-11 w-full rounded-full border border-white/10 bg-white/[0.06] text-[15px] hover:bg-white/[0.1]"
        >
          {t("adminSignIn")}
        </Button>
        <FormFeedback status={status} error={error} errorID={errorID} errorRef={errorRef} />
      </form>
    </section>
  );
}

function adminSignInErrorMessage(err: unknown, t: ReturnType<typeof useT>): string {
  if (!(err instanceof ApiError)) return t("couldNotReachServer");
  if (err.status === 429) return t("tooManyAttempts");
  if (err.status === 403) return t("adminSignInNeedsSecure");
  return t("adminSignInFailed");
}
