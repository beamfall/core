import { useId } from "react";
import { useT } from "@/lib/i18n";
import { formatSync, SYNC_COARSE_STEP, SYNC_FINE_STEP } from "./playbackSync";

// One signed-seconds nudger (CORE-1671), shared by the subtitle offset and the
// audio delay: coarse and fine steps either way, the current value with the
// direction it moves things, and a reset back to zero. `min`/`max` disable the
// step buttons that would leave the range, so a route that can only delay never
// offers an "earlier" press it cannot honour.
const STEPS = [-SYNC_COARSE_STEP, -SYNC_FINE_STEP, SYNC_FINE_STEP, SYNC_COARSE_STEP];

export function SyncNudger({
  label,
  value,
  min,
  max,
  supported,
  onNudge,
  onReset,
}: {
  label: string;
  value: number;
  min: number;
  max: number;
  supported: boolean;
  onNudge: (delta: number) => void;
  onReset: () => void;
}) {
  const t = useT();
  const labelId = useId();
  const direction = value > 0 ? t("syncLater") : value < 0 ? t("syncEarlier") : "";

  return (
    <div className="bf-subtitle-row bf-sync-row">
      <span id={labelId}>{label}</span>
      <span className="bf-subtitle-sync" role="group" aria-labelledby={labelId}>
        {STEPS.map((step) => {
          const outOfRange = step < 0 ? value <= min : value >= max;
          const stepText = formatSync(step);
          return (
            <button
              key={step}
              type="button"
              aria-label={`${label} ${stepText}`}
              disabled={!supported || outOfRange}
              onClick={() => onNudge(step)}
            >
              {step < 0 ? "−" : "+"}
              {Math.abs(step).toFixed(1)}
            </button>
          );
        })}
      </span>
      <span className="bf-sync-readout" aria-live="polite">
        <span className="bf-subtitle-sync-value">{formatSync(value)}</span>
        {direction && <span className="bf-sync-direction">{direction}</span>}
        <button
          type="button"
          className="bf-sync-reset"
          aria-label={`${label}: ${t("reset")}`}
          disabled={value === 0}
          onClick={onReset}
        >
          {t("reset")}
        </button>
      </span>
    </div>
  );
}
