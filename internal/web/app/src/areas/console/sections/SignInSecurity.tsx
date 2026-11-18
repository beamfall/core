import { type FormEvent, useEffect, useId, useRef, useState } from "react";
import { HelpTip } from "@/components/beamfall/workbench";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ApiError, fetchSetupStatus, type SignInSecurityMode } from "@/lib/api";
import { setSignInSecurityMode } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import { Section } from "../Section";

// Sign-in security (CORE-1110, ADR-0007): switch between Home (the owner may
// sign a browser in with the admin password) and Strict (only paired devices
// sign in; recovery is `beamfall pairing-code` on the host). The server needs
// a MANAGER device and the admin password re-entered in the same request; a
// wrong password is throttled like every other admin re-auth. The password is
// a CREDENTIAL: never logged, never echoed, cleared after every attempt.

const MODES: { mode: SignInSecurityMode; label: StringKey; detail: StringKey }[] = [
  { mode: "home", label: "setupSecurityHome", detail: "setupSecurityHomeDetail" },
  { mode: "strict", label: "setupSecurityStrict", detail: "setupSecurityStrictDetail" },
];

function refusalMessage(err: unknown): StringKey {
  if (!(err instanceof ApiError)) return "couldNotReachServer";
  switch (err.status) {
    case 401:
      return "adminUnlockRejected";
    case 403:
      return "signinSecurityNotManager";
    case 429:
      return "tooManyAttempts";
    default:
      return "signinSecurityFailed";
  }
}

export function SignInSecurity() {
  const t = useT();
  const passwordID = useId();
  const errorID = useId();
  const hintID = useId();
  const errorRef = useRef<HTMLParagraphElement>(null);
  const [current, setCurrent] = useState<SignInSecurityMode | null>(null);
  const [selected, setSelected] = useState<SignInSecurityMode>("home");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<StringKey | null>(null);
  const [saved, setSaved] = useState(false);
  // A failed probe leaves the mode unknown: show the error with a retry and
  // keep the switch and Save disabled rather than guessing Home. Bumping
  // loadAttempt re-runs the probe.
  const [loadFailed, setLoadFailed] = useState(false);
  const [loadAttempt, setLoadAttempt] = useState(0);
  // Home to Strict removes password sign-in, so a locked-out owner's only way
  // back is a pairing code minted on the server (CORE-1608). Confirm first.
  const [confirmStrict, setConfirmStrict] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setLoadFailed(false);
    void fetchSetupStatus().then((status) => {
      if (cancelled) return;
      if (status.securityMode === null) {
        setLoadFailed(true);
        return;
      }
      setCurrent(status.securityMode);
      setSelected(status.securityMode);
    });
    return () => {
      cancelled = true;
    };
  }, [loadAttempt]);

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (busy || !password) return;
    if (current === "home" && selected === "strict") {
      setConfirmStrict(true);
      return;
    }
    void apply();
  }

  async function apply() {
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      const next = await setSignInSecurityMode(selected, password);
      setCurrent(next);
      setSelected(next);
      setSaved(true);
    } catch (err) {
      setError(refusalMessage(err));
      requestAnimationFrame(() => errorRef.current?.focus());
    } finally {
      setPassword("");
      setBusy(false);
    }
  }

  return (
    <Section heading="signinSecurityHeading" lead="signinSecurityLead" collapsible>
      {loadFailed && (
        <div
          role="alert"
          className="mb-4 flex max-w-xl items-center gap-3 text-sm text-destructive"
        >
          <span>{t("couldNotReachServer")}</span>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => setLoadAttempt((n) => n + 1)}
          >
            {t("tryAgain")}
          </Button>
        </div>
      )}
      <form onSubmit={onSubmit} className="grid max-w-xl gap-5" noValidate>
        <fieldset
          className="m-0 grid gap-3 border-0 p-0"
          disabled={current === null}
          data-setting="console.access.signinSecurity"
        >
          <legend className="sr-only">{t("signinSecurityHeading")}</legend>
          {MODES.map(({ mode, label, detail }) => (
            <div key={mode} className="flex items-start gap-1">
              <label className="flex items-start gap-2 text-sm">
                <input
                  type="radio"
                  name="signin-security-mode"
                  value={mode}
                  checked={selected === mode}
                  onChange={() => setSelected(mode)}
                  className="mt-1"
                />
                <span className="grid gap-0.5">
                  <span className="flex items-center gap-2 font-medium text-foreground">
                    {t(label)}
                    {mode === "home" && (
                      <span className="text-xs font-normal text-[var(--accent)]">
                        {t("setupSecurityRecommended")}
                      </span>
                    )}
                  </span>
                  <span className="text-xs text-muted-foreground">{t(detail)}</span>
                </span>
              </label>
              {mode === "strict" && <HelpTip label={t(label)} text="accessHelpStrictTip" />}
            </div>
          ))}
        </fieldset>
        <div className="space-y-1.5" data-setting="console.access.adminPassword">
          <Label htmlFor={passwordID}>{t("adminPasswordLabel")}</Label>
          <Input
            id={passwordID}
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            required
            aria-invalid={error != null}
            aria-describedby={error ? `${hintID} ${errorID}` : hintID}
          />
          <p id={hintID} className="text-xs text-muted-foreground">
            {t("accessHelpAdminPasswordHint")}
          </p>
        </div>
        <Button
          type="submit"
          disabled={busy || !password || current === null || selected === current}
        >
          {t("signinSecuritySave")}
        </Button>
        {saved && (
          <p role="status" className="text-sm text-muted-foreground">
            {t("signinSecuritySaved")}
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
            {t(error)}
          </p>
        )}
      </form>
      <AlertDialog open={confirmStrict} onOpenChange={setConfirmStrict}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("signinSecurityStrictConfirmTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("signinSecurityStrictConfirmBody")} <code>beamfall pairing-code</code>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
            <Button
              type="button"
              onClick={() => {
                setConfirmStrict(false);
                void apply();
              }}
            >
              {t("signinSecurityStrictConfirm")}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Section>
  );
}
