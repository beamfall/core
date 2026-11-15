import { ArrowRight } from "lucide-react";
import { type FormEvent, useEffect, useId, useRef, useState } from "react";
import {
  FieldHint,
  FieldInput,
  FieldLabel,
  GlassCard,
  PageShell,
  PrimaryAction,
  SurfaceHeading,
  SurfaceSection,
} from "@/components/beamfall/surface";
import { ApiError, fetchSetupStatus, type SignInSecurityMode } from "@/lib/api";
import { adminSignIn, enroll } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { AskToJoinPanel, useAskToJoin } from "./AskToJoin";
import { FormFeedback, normalizePairingCode, PAIRING_CODE_INPUT } from "./AuthShell";
import { defaultDeviceName } from "./deviceName";
import { PairingCodeHelp } from "./PairingCodeHelp";

// First contact (ADR-0007 §2): a browser enrolls as a VIEWER device with a
// pairing code minted on a manager device. Faithful to the vanilla enroll-view
// (internal/web/static/app.js): the pairing code is trimmed, the device name
// defaults to "Web Browser", and a successful enroll reloads to BASE_URL so the
// session spine re-bootstraps against the freshly-bound session.
//
// The pairing code is a CREDENTIAL: it is never logged and never echoed back
// into the error message. The server returns 401 for an invalid OR expired code
// and deliberately does not say which, so the hint under the field covers both
// (codes work once, expire after 10 minutes, ask for a new one). 429 is the
// attempt throttle and 403 a plain-HTTP request from outside the LAN.
//
// The code is consumed the moment the server accepts it, so the success is
// committed (status line + redirect) as soon as enroll() resolves; the
// compatibility rebind that follows can only produce a NOTICE, never an
// enrollment error — the fresh load re-runs the handshake anyway.
//
// The page also has to answer "what is a pairing code and where do I get one?"
// for someone who has never seen this server (CORE-1196): PairingCodeHelp says
// so in plain words under the form.
//
// Console › Devices hands out a link of the form /web/enroll#code=XXXX
// (CORE-1607). The code rides in the fragment, which browsers never send to the
// server or put in Referer, and it is wiped from the address bar as soon as it
// has been read.
export function EnrollPage() {
  const t = useT();
  const codeID = useId();
  const deviceNameID = useId();
  const errorID = useId();
  const codeHintID = useId();
  const deviceNameHintID = useId();
  const errorRef = useRef<HTMLParagraphElement>(null);
  const codeRef = useRef<HTMLInputElement>(null);
  const [code, setCode] = useState(readCodeFromHash);
  const [deviceName, setDeviceName] = useState("");
  const [error, setError] = useState<string | null>(null);
  // True after the server refused the code: the hint then says why codes fail.
  const [rejected, setRejected] = useState(false);
  const [status, setStatus] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // The owner's sign-in security choice (CORE-1110). Strict turns off
  // admin-password sign-in, so the form stays hidden; it also stays hidden
  // until the probe settles so it never flashes on a Strict server.
  const [securityMode, setSecurityMode] = useState<SignInSecurityMode | null>(null);
  // Ask to join (CORE-2070). Typed enrollment and asking are one attempt at a
  // time: submitting a code cancels a pending ask, and asking clears the form's
  // refusal; the Ask button waits while a typed code is in flight.
  const ask = useAskToJoin();

  useEffect(() => {
    if (/(^#|&)code=/.test(window.location.hash)) {
      window.history.replaceState(
        window.history.state,
        "",
        window.location.pathname + window.location.search,
      );
    }
  }, []);

  const fallbackName = defaultDeviceName(t);

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
    if (!code) {
      showError(t("pairingCodeRequired"));
      return;
    }
    ask.cancel();
    setBusy(true);
    setError(null);
    try {
      // The code is already normalized as typed; a blank device name falls
      // back to one that tells this browser apart ("Chrome on macOS").
      const rebind = await enroll(code, deviceName.trim() || fallbackName);
      // Success: reload to BASE_URL so SessionProvider re-bootstraps the
      // now-enrolled session (the vanilla app calls init(); the React app
      // re-runs the whole spine from a fresh load). A failed rebind is its own
      // notice — the browser IS enrolled — and never blocks the redirect.
      setStatus(rebind === "bound" ? t("enrollConnected") : t("enrollHandshakeIncomplete"));
      window.location.assign(import.meta.env.BASE_URL);
    } catch (err) {
      setRejected(err instanceof ApiError && err.status === 401);
      setBusy(false);
      showError(pairingCodeErrorMessage(err, t, "enrollRetry"));
    }
    // No finally{setBusy(false)}: on success the page is navigating away, so
    // the form must stay disabled until the reload lands.
  }

  // The alert announces the problem; focus returns to the code, selected, so
  // the fix is to type or paste a new one straight away.
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
        <SurfaceHeading title={t("enrollTitle")} lead={t("signInLead")} />
        <form onSubmit={onSubmit} className="space-y-5" noValidate>
          <div>
            <FieldLabel htmlFor={codeID}>{t("pairingCode")}</FieldLabel>
            <FieldInput
              id={codeID}
              ref={codeRef}
              value={code}
              onChange={(e) => {
                setCode(normalizePairingCode(e.target.value));
                setError(null);
                setRejected(false);
              }}
              className={PAIRING_CODE_INPUT}
              // The pairing code is a credential: no autofill, no spellcheck.
              autoComplete="off"
              autoCapitalize="off"
              autoCorrect="off"
              spellCheck={false}
              autoFocus
              required
              aria-invalid={error != null}
              aria-describedby={error ? `${codeHintID} ${errorID}` : codeHintID}
            />
            <FieldHint id={codeHintID} className="mt-2">
              {t(rejected ? "pairingCodeRejectedHint" : "signInCodeHint")}
            </FieldHint>
          </div>
          <div>
            <FieldLabel htmlFor={deviceNameID}>{t("deviceName")}</FieldLabel>
            <FieldInput
              id={deviceNameID}
              value={deviceName}
              onChange={(e) => setDeviceName(e.target.value)}
              placeholder={fallbackName}
              autoComplete="off"
              aria-describedby={deviceNameHintID}
            />
            <FieldHint id={deviceNameHintID} className="mt-2">
              {t("signInDeviceNameHint")}
            </FieldHint>
          </div>
          <FormFeedback status={status} error={error} errorID={errorID} errorRef={errorRef} />
          <PrimaryAction type="submit" disabled={busy} className="w-full">
            {t("connect")}
            <ArrowRight aria-hidden className="size-4" />
          </PrimaryAction>
        </form>
        <AskToJoinPanel
          ask={ask}
          disabled={busy}
          onStart={() => {
            setError(null);
            setRejected(false);
            ask.start(deviceName.trim() || fallbackName);
          }}
        />
        {securityMode === "home" && (
          <AdminSignInForm deviceName={deviceName.trim() || fallbackName} />
        )}
        {securityMode === "strict" && <StrictRecoveryHint />}
        <SurfaceSection className="mt-7 pt-6">
          <PairingCodeHelp />
        </SurfaceSection>
      </GlassCard>
    </PageShell>
  );
}

// Owner recovery (CORE-1106): sign this browser in with the admin password set
// during first-run setup. The server enrolls a MANAGER device on the owner
// profile. Every refusal is the same 401, so the message never says whether
// the password was wrong or sign-in is unavailable. The password is never
// logged or echoed.
//
// It is a visible secondary action right under the code form (CORE-1608), so
// an owner without a pairing code finds it without scrolling; the password
// field opens in place.
function AdminSignInForm({ deviceName }: { deviceName: string }) {
  const t = useT();
  const passwordID = useId();
  const errorID = useId();
  const errorRef = useRef<HTMLParagraphElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const [open, setOpen] = useState(false);
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  // Set when the server refused a hostname a LAN browser addressed (CORE-1600):
  // the address to reopen Beamfall at, or "" when the server offered none.
  const [lanUrl, setLanUrl] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError(null);
    setLanUrl(null);
    try {
      const rebind = await adminSignIn(password, deviceName);
      setStatus(rebind === "bound" ? t("enrollConnected") : t("enrollHandshakeIncomplete"));
      window.location.assign(import.meta.env.BASE_URL);
    } catch (err) {
      const lan = lanHostnameRefusal(err);
      setLanUrl(lan);
      setError(lan === null ? adminSignInErrorMessage(err, t) : t("adminSignInUseIPAddress"));
      setPassword("");
      setBusy(false);
      requestAnimationFrame(() => errorRef.current?.focus());
    }
  }

  return (
    <div className="mt-5">
      <p className="m-0 mb-5 flex items-center gap-3 text-xs uppercase tracking-wide text-muted-foreground before:h-px before:flex-1 before:bg-foreground/10 after:h-px after:flex-1 after:bg-foreground/10">
        {t("adminSignInOr")}
      </p>
      {!open ? (
        <PrimaryAction
          type="button"
          variant="outline"
          onClick={() => {
            setOpen(true);
            requestAnimationFrame(() => passwordRef.current?.focus());
          }}
          className={OUTLINE_ACTION}
        >
          {t("adminSignInTitle")}
        </PrimaryAction>
      ) : (
        <form onSubmit={onSubmit} className="space-y-4" noValidate>
          <p className="m-0 text-sm leading-relaxed text-muted-foreground">
            {t("adminSignInLead")}
          </p>
          <div>
            <FieldLabel htmlFor={passwordID}>{t("adminPasswordLabel")}</FieldLabel>
            <FieldInput
              id={passwordID}
              ref={passwordRef}
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
              required
              aria-invalid={error != null}
              aria-describedby={error ? errorID : undefined}
            />
          </div>
          <FormFeedback status={status} error={error} errorID={errorID} errorRef={errorRef} />
          {lanUrl && (
            <p className="m-0 text-sm">
              <a href={lanUrl} className="font-medium underline underline-offset-2">
                {t("adminSignInOpenAddress", { url: lanUrl })}
              </a>
            </p>
          )}
          <PrimaryAction
            type="submit"
            variant="outline"
            disabled={busy || password === ""}
            className={OUTLINE_ACTION}
          >
            {t("adminSignIn")}
          </PrimaryAction>
        </form>
      )}
    </div>
  );
}

// The full-width quiet action under the code form (the admin password route).
const OUTLINE_ACTION = "w-full border-foreground/15 bg-transparent hover:bg-foreground/[0.06]";

// Strict mode has no password fallback (CORE-1110), so a locked-out owner is
// told the one way back in: mint a code on the server itself (CORE-1608).
function StrictRecoveryHint() {
  const t = useT();
  return (
    <p className="m-0 mt-5 text-sm leading-relaxed text-muted-foreground">
      {t("strictRecoveryHint")}{" "}
      <code className="rounded bg-foreground/[0.06] px-1.5 py-0.5 font-mono text-[13px] text-foreground">
        beamfall pairing-code
      </code>
    </p>
  );
}

// The CORE-1600 refusal: 403 with code lan_hostname_not_allowed. Returns the
// first offered address ("" when none was offered), or null for any other
// error. Only a plain http(s) URL is ever used as a link target.
export function lanHostnameRefusal(err: unknown): string | null {
  if (!(err instanceof ApiError) || err.status !== 403) return null;
  const payload = err.payload as { code?: unknown; lanUrls?: unknown } | undefined;
  if (payload?.code !== "lan_hostname_not_allowed") return null;
  const first = Array.isArray(payload.lanUrls) ? payload.lanUrls[0] : undefined;
  if (typeof first !== "string") return "";
  try {
    const url = new URL(first);
    return url.protocol === "http:" || url.protocol === "https:" ? url.href : "";
  } catch {
    return "";
  }
}

function readCodeFromHash(): string {
  const fragment = window.location.hash.replace(/^#/, "");
  return normalizePairingCode(new URLSearchParams(fragment).get("code") ?? "").slice(0, 32);
}

// Shared by enroll and promote. The server never says whether a refused code
// was wrong or expired, so neither does the message; the hint covers both.
export function pairingCodeErrorMessage(
  err: unknown,
  t: ReturnType<typeof useT>,
  fallback: "enrollRetry" | "promoteRetry",
): string {
  if (!(err instanceof ApiError)) return t("pairingCodeOffline");
  if (err.status === 401) return t("pairingCodeInvalid");
  if (err.status === 429) return t("tooManyAttempts");
  if (err.status === 403) return t("adminSignInNeedsSecure");
  return t(fallback);
}

function adminSignInErrorMessage(err: unknown, t: ReturnType<typeof useT>): string {
  if (!(err instanceof ApiError)) return t("couldNotReachServer");
  if (err.status === 429) return t("tooManyAttempts");
  if (err.status === 403) return t("adminSignInNeedsSecure");
  return t("adminSignInFailed");
}
