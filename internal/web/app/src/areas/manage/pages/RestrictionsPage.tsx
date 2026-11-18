import { useId, useState } from "react";
import { Link } from "react-router";
import { ErrorLine, ManageSection, StatusBadge } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { setRevealPin } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";

// Restrictions (ADR-0004 Reveal contract, ported from index.html
// restrictions-view): the PIN reveals restricted content for this session.
// Reveal/Relock state is visible STATE, never a hidden mode — the Revealed chip
// renders it. The PIN goes into the mutation and nowhere else; the field clears
// as soon as it is read. Error copy stays calm and fixed and never exposes
// security internals.
//
// Setting up (CORE-2463): with no PIN set the page explains the two steps and
// offers Set PIN instead of an unlock field; once unlocked it offers Change
// PIN. Which Libraries are restricted is chosen in Console › Libraries.
export function RestrictionsPage() {
  const t = useT();
  const { session, revealed, reveal, relock, refreshSession } = useSession();
  // A server that predates revealPinSet (CORE-2462) leaves it absent: assume a
  // PIN is set so the page keeps its unlock field.
  const pinSet = session?.revealPinSet !== false;
  const [pin, setPin] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [changing, setChanging] = useState(false);
  const [saved, setSaved] = useState(false);

  const onReveal = async (event: React.FormEvent) => {
    event.preventDefault();
    setError("");
    setSaved(false);
    const value = pin;
    setPin("");
    if (!value) return;
    setBusy(true);
    try {
      const ok = await reveal(value);
      if (!ok) setError(t("pinIncorrect"));
    } catch (err) {
      setError(
        err instanceof Error && err.message === "reveal attempts throttled"
          ? t("tooManyAttempts")
          : t("pinIncorrect"),
      );
    } finally {
      setBusy(false);
    }
  };

  const onRelock = async () => {
    setBusy(true);
    setError("");
    setSaved(false);
    try {
      await relock();
    } finally {
      setBusy(false);
    }
  };

  const onPinSaved = async () => {
    setChanging(false);
    setSaved(true);
    // In place, not refresh(): a full reload unmounts this page and loses the
    // confirmation. A failed re-read keeps the page as it is.
    await refreshSession().catch(() => {});
  };

  const librariesLink = (
    <p className="mt-3 text-sm">
      <Link to="/console/libraries" className="underline underline-offset-2">
        {t("restrictionsLibrariesLink")}
      </Link>
    </p>
  );

  return (
    <ManageSection
      actions={revealed ? <StatusBadge state="revealed">{t("revealed")}</StatusBadge> : undefined}
    >
      {!pinSet ? (
        <div data-setting="manage.restrictions.pin">
          <p className="m-0 text-sm text-muted-foreground">{t("restrictionsSetupLead")}</p>
          <ol className="my-2 list-decimal space-y-1 ps-5 text-sm text-muted-foreground">
            <li>{t("restrictionsSetupStepPin")}</li>
            <li>{t("restrictionsSetupStepLibrary")}</li>
          </ol>
          <PinForm submitLabel={t("restrictionsSetPin")} onSaved={onPinSaved} />
        </div>
      ) : revealed ? (
        <div className="space-y-3" data-setting="manage.restrictions.pin">
          <div className="flex flex-wrap gap-3">
            <Button type="button" variant="outline" onClick={onRelock} disabled={busy}>
              {t("relock")}
            </Button>
            {!changing && (
              <Button
                type="button"
                variant="outline"
                onClick={() => {
                  setSaved(false);
                  setChanging(true);
                }}
              >
                {t("restrictionsChangePin")}
              </Button>
            )}
          </div>
          {changing && (
            <PinForm
              submitLabel={t("restrictionsChangePin")}
              onSaved={onPinSaved}
              onCancel={() => setChanging(false)}
            />
          )}
          {librariesLink}
        </div>
      ) : (
        <form
          onSubmit={onReveal}
          className="flex items-end gap-3"
          data-setting="manage.restrictions.pin"
        >
          <div className="space-y-1">
            <Label htmlFor="reveal-pin">{t("pin")}</Label>
            <Input
              id="reveal-pin"
              type="password"
              inputMode="numeric"
              autoComplete="off"
              value={pin}
              onChange={(e) => setPin(e.target.value)}
              className="w-40"
            />
          </div>
          <Button type="submit" disabled={busy}>
            {t("reveal")}
          </Button>
        </form>
      )}
      {saved && (
        <p role="status" className="mt-2 text-sm">
          {t("restrictionsPinSaved")}
        </p>
      )}
      <ErrorLine message={error} />
    </ManageSection>
  );
}

// The new PIN and its confirmation. Both fields clear once read, so the PIN
// lives only in the setRevealPin call.
function PinForm({
  submitLabel,
  onSaved,
  onCancel,
}: {
  submitLabel: string;
  onSaved: () => Promise<void>;
  onCancel?: () => void;
}) {
  const t = useT();
  const id = useId();
  const [pin, setPinValue] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const onSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    setError("");
    const value = pin.trim();
    const again = confirm.trim();
    if (!value) return;
    if (value !== again) {
      setError(t("restrictionsPinMismatch"));
      return;
    }
    setPinValue("");
    setConfirm("");
    setBusy(true);
    try {
      await setRevealPin(value);
    } catch (err) {
      setError(
        err instanceof Error && err.message.includes("no reveal path")
          ? t("restrictionsPinNoRevealPath")
          : t("restrictionsPinNotSaved"),
      );
      setBusy(false);
      return;
    }
    setBusy(false);
    await onSaved();
  };

  return (
    <form onSubmit={onSubmit} className="space-y-2">
      <div className="flex flex-wrap items-end gap-3">
        <div className="space-y-1">
          <Label htmlFor={`${id}-pin`}>{t("restrictionsNewPin")}</Label>
          <Input
            id={`${id}-pin`}
            type="password"
            inputMode="numeric"
            autoComplete="new-password"
            value={pin}
            onChange={(e) => setPinValue(e.target.value)}
            className="w-40"
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor={`${id}-confirm`}>{t("restrictionsConfirmPin")}</Label>
          <Input
            id={`${id}-confirm`}
            type="password"
            inputMode="numeric"
            autoComplete="new-password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            className="w-40"
          />
        </div>
        <Button type="submit" disabled={busy || !pin.trim()}>
          {submitLabel}
        </Button>
        {onCancel && (
          <Button type="button" variant="ghost" onClick={onCancel} disabled={busy}>
            {t("cancel")}
          </Button>
        )}
      </div>
      <ErrorLine message={error} />
    </form>
  );
}
