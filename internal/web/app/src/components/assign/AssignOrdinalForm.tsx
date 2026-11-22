import { useId, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useT } from "@/lib/i18n";
import { maxOrdinalIndex, type Ordinal, ordinalIndex, type ordinalLabels } from "./assignOrdinals";

export function AssignIndexInput({
  label,
  value,
  disabled,
  onChange,
}: {
  label: string;
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
}) {
  const t = useT();
  const error = useId();
  const invalid = value !== "" && ordinalIndex(value) === undefined;
  return (
    <div className="grid gap-1 text-sm">
      <label className="grid gap-1">
        {label}
        <Input
          type="number"
          min={0}
          max={maxOrdinalIndex}
          step={1}
          required
          value={value}
          disabled={disabled}
          aria-invalid={invalid}
          aria-describedby={invalid ? error : undefined}
          onChange={(event) => onChange(event.target.value)}
        />
      </label>
      {invalid && (
        <p id={error} role="alert">
          {t("assignInvalidIndex")}
        </p>
      )}
    </div>
  );
}

export function AssignOrdinalForm({
  fixedSequence,
  labels,
  disabled,
  onChange,
  onCancel,
  onPreview,
}: {
  fixedSequence?: string;
  labels: ReturnType<typeof ordinalLabels>;
  disabled: boolean;
  onChange: () => void;
  onCancel: () => void;
  onPreview: (ordinal: Ordinal) => void;
}) {
  const t = useT();
  const sequenceError = useId();
  const [sequence, setSequence] = useState(fixedSequence ?? "");
  const [number, setNumber] = useState("");
  const index = ordinalIndex(number);
  const validSequence = Array.from(sequence.trim()).length <= 32;
  const canPreview = !disabled && validSequence && index !== undefined;
  return (
    <form
      className="space-y-2 rounded-md border p-3"
      onSubmit={(event) => {
        event.preventDefault();
        if (canPreview) onPreview({ sequence: fixedSequence ?? sequence.trim(), index });
      }}
    >
      {fixedSequence !== undefined && (
        <p className="text-sm">{t("assignMissingIndex", { level: labels.index })}</p>
      )}
      <div className="flex flex-wrap gap-2">
        <div className="grid gap-1 text-sm">
          <label className="grid gap-1">
            {labels.sequence}
            <Input
              value={sequence}
              readOnly={fixedSequence !== undefined}
              disabled={disabled}
              aria-invalid={!validSequence}
              aria-describedby={validSequence ? undefined : sequenceError}
              onChange={(event) => {
                onChange();
                setSequence(event.target.value);
              }}
            />
          </label>
          {!validSequence && (
            <p id={sequenceError} role="alert">
              {t("assignInvalidSequence")}
            </p>
          )}
        </div>
        <AssignIndexInput
          label={labels.index}
          value={number}
          disabled={disabled}
          onChange={(value) => {
            onChange();
            setNumber(value);
          }}
        />
      </div>
      <div className="flex gap-2">
        <Button type="submit" size="sm" variant="outline" disabled={!canPreview}>
          {t("unmatchedPreviewLoad")}
        </Button>
        <Button type="button" size="sm" variant="ghost" disabled={disabled} onClick={onCancel}>
          {t("cancel")}
        </Button>
      </div>
    </form>
  );
}
