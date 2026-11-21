import { type FormEvent, type Ref, useEffect, useId, useRef, useState } from "react";
import { ErrorLine } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useT } from "@/lib/i18n";

const directions = [
  { key: "ArrowUp", label: "pinDirectionUp", digit: "2", symbol: "↑" },
  { key: "ArrowLeft", label: "pinDirectionLeft", digit: "4", symbol: "←" },
  { key: "ArrowRight", label: "pinDirectionRight", digit: "6", symbol: "→" },
  { key: "ArrowDown", label: "pinDirectionDown", digit: "8", symbol: "↓" },
] as const;

const minimumSteps = 6;
type PinMode = "digits" | "directions";

export function PinSetupForm({
  submitLabel,
  onSave,
  onSaved,
  onCancel,
  saveError,
}: {
  submitLabel: string;
  onSave: (pin: string) => Promise<unknown>;
  onSaved: () => void | Promise<void>;
  onCancel?: () => void;
  saveError?: (error: unknown) => string;
}) {
  const t = useT();
  const id = useId();
  const [mode, setMode] = useState<PinMode>("digits");
  const [pin, setPin] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const usingDirections = mode === "directions";
  const firstDirection = useRef<HTMLButtonElement>(null);
  const value = pin.trim();
  const canSubmit =
    !busy &&
    (usingDirections
      ? value.length >= minimumSteps && confirm.length >= minimumSteps
      : value !== "");
  const showTVWarning =
    !usingDirections &&
    [...value].some((digit) => !directions.some((entry) => entry.digit === digit));

  useEffect(() => {
    if (usingDirections) firstDirection.current?.focus();
  }, [usingDirections]);

  function changeMode(nextMode: PinMode) {
    if (mode === nextMode) return;
    setMode(nextMode);
    setPin("");
    setConfirm("");
    setError("");
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!canSubmit) return;
    setError("");
    if (value !== confirm.trim()) {
      setError(t("restrictionsPinMismatch"));
      return;
    }
    setPin("");
    setConfirm("");
    setBusy(true);
    try {
      await onSave(value);
    } catch (err) {
      setError(saveError?.(err) ?? t("restrictionsPinNotSaved"));
      setBusy(false);
      return;
    }
    setBusy(false);
    await onSaved();
  }

  return (
    <form onSubmit={submit} className="space-y-2">
      <div className="flex flex-wrap gap-2">
        <Button
          type="button"
          size="sm"
          variant={usingDirections ? "outline" : "default"}
          aria-pressed={!usingDirections}
          disabled={busy}
          onClick={() => changeMode("digits")}
        >
          {t("pinSetupDigits")}
        </Button>
        <Button
          type="button"
          size="sm"
          variant={usingDirections ? "default" : "outline"}
          aria-pressed={usingDirections}
          disabled={busy}
          onClick={() => changeMode("directions")}
        >
          {t("pinSetupDirections")}
        </Button>
      </div>
      <p className="m-0 text-sm text-muted-foreground">{t("pinSetupSwipePrivacy")}</p>
      {usingDirections && (
        <p id={`${id}-directions-help`} className="m-0 text-sm text-muted-foreground">
          {t("pinSetupDirectionsHelp")}
        </p>
      )}
      <div className="flex flex-wrap items-end gap-3">
        {usingDirections ? (
          <>
            <DirectionField
              label={t("restrictionsNewPin")}
              helpID={`${id}-directions-help`}
              value={pin}
              setValue={setPin}
              disabled={busy}
              firstButtonRef={firstDirection}
            />
            <DirectionField
              label={t("restrictionsConfirmPin")}
              helpID={`${id}-directions-help`}
              value={confirm}
              setValue={setConfirm}
              disabled={busy}
            />
          </>
        ) : (
          <>
            <div className="space-y-1">
              <Label htmlFor={`${id}-pin`}>{t("restrictionsNewPin")}</Label>
              <Input
                id={`${id}-pin`}
                type="password"
                inputMode="numeric"
                autoComplete="new-password"
                aria-describedby={showTVWarning ? `${id}-tv-warning` : undefined}
                value={pin}
                onChange={(event) => setPin(event.target.value)}
                className="w-40"
                disabled={busy}
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
                onChange={(event) => setConfirm(event.target.value)}
                className="w-40"
                disabled={busy}
              />
            </div>
          </>
        )}
      </div>
      <p id={`${id}-tv-warning`} aria-live="polite" className="m-0 text-sm text-muted-foreground">
        {showTVWarning && t("pinSetupTVWarning")}
      </p>
      <div className="flex flex-wrap gap-2">
        <Button type="submit" disabled={!canSubmit}>
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

function DirectionField({
  label,
  helpID,
  value,
  setValue,
  disabled,
  firstButtonRef,
}: {
  label: string;
  helpID: string;
  value: string;
  setValue: React.Dispatch<React.SetStateAction<string>>;
  disabled: boolean;
  firstButtonRef?: Ref<HTMLButtonElement>;
}) {
  const t = useT();
  return (
    <fieldset
      aria-describedby={helpID}
      disabled={disabled}
      className="min-w-0 max-w-full space-y-1"
      onKeyDown={(event) => {
        const direction = directions.find((entry) => entry.key === event.key);
        if (!direction || event.altKey || event.ctrlKey || event.metaKey) return;
        event.preventDefault();
        if (!event.repeat && !disabled) setValue((pin) => pin + direction.digit);
      }}
    >
      <legend className="text-sm font-medium">{label}</legend>
      <p className="min-h-9 break-all rounded-md border px-2 py-1 font-mono tracking-wider">
        {[...value]
          .map((digit) => directions.find((entry) => entry.digit === digit)?.symbol)
          .join("")}
      </p>
      <div className="flex flex-wrap gap-1">
        {directions.map(({ key, label, digit, symbol }, index) => (
          <Button
            key={key}
            ref={index === 0 ? firstButtonRef : undefined}
            type="button"
            variant="outline"
            size="icon"
            aria-label={t(label)}
            onClick={() => setValue((pin) => pin + digit)}
          >
            {symbol}
          </Button>
        ))}
        <Button
          type="button"
          variant="ghost"
          size="icon"
          aria-label={t("pinSetupRemoveStep")}
          disabled={!value}
          onClick={() => setValue((pin) => pin.slice(0, -1))}
        >
          ⌫
        </Button>
      </div>
    </fieldset>
  );
}
