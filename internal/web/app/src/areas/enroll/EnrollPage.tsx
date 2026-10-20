import { type FormEvent, useId, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ApiError } from "@/lib/api";
import { enroll } from "@/lib/graphql";
import { useT } from "@/lib/i18n";

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
export function EnrollPage() {
  const t = useT();
  const codeID = useId();
  const deviceNameID = useId();
  const errorID = useId();
  const errorRef = useRef<HTMLParagraphElement>(null);
  const [code, setCode] = useState("");
  const [deviceName, setDeviceName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

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
    <main className="mx-auto max-w-md px-4 py-12">
      <h1 className="text-2xl font-semibold">{t("enrollTitle")}</h1>
      <p className="mt-2 text-sm text-muted-foreground">{t("enrollLead")}</p>
      <form onSubmit={onSubmit} className="mt-6 space-y-4" noValidate>
        <div className="space-y-1.5">
          <Label htmlFor={codeID}>{t("pairingCode")}</Label>
          <Input
            id={codeID}
            value={code}
            onChange={(e) => setCode(e.target.value)}
            // The pairing code is a credential: no autofill, no spellcheck,
            // no autocapitalize that would mangle a case-sensitive code.
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
        <div className="space-y-1.5">
          <Label htmlFor={deviceNameID}>{t("deviceName")}</Label>
          <Input
            id={deviceNameID}
            value={deviceName}
            onChange={(e) => setDeviceName(e.target.value)}
            placeholder={t("webBrowser")}
            autoComplete="off"
          />
        </div>
        <Button type="submit" disabled={busy}>
          {t("connect")}
        </Button>
        {status && (
          <p role="status" className="text-sm text-muted-foreground">
            {status}
          </p>
        )}
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
