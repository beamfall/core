import { Minus, Plus, RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { PlaybackAdjustmentRanges } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import type { PlaybackAdjustment } from "../lib/playback";

// Key and tempo controls for a karaoke song (CORE-1659): a semitone and a 5%
// step each way plus a reset, clamped to the ranges the server advertises in
// serverInfo.playbackAdjustments. The values shown are the ones asked for;
// `applied` false says the stream plays without them, so the bar never claims
// a change the server did not make. A control at its limit stays focusable
// (aria-disabled), so stepping to the end of a range never drops keyboard focus.

/** One tempo step: 5% of the recorded speed. */
export const TEMPO_STEP = 0.05;

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

/** A tempo factor on the 1% grid, so repeated 5% steps never drift. */
function roundTempo(tempo: number): number {
  return Math.round(tempo * 100) / 100;
}

/** "+2", "0", "−1": the signed semitone shift. */
function formatSemitones(semitones: number): string {
  if (semitones > 0) return `+${semitones}`;
  if (semitones < 0) return `−${Math.abs(semitones)}`;
  return "0";
}

export function KaraokeControls({
  ranges,
  adjustment,
  applied,
  onChange,
}: {
  ranges: PlaybackAdjustmentRanges;
  /** The key and tempo asked for. */
  adjustment: PlaybackAdjustment;
  /** Whether the stream plays them; null while that is not known yet. */
  applied: boolean | null;
  onChange: (next: PlaybackAdjustment) => void;
}) {
  const t = useT();
  const keyMin = Math.ceil(ranges.pitchShiftSemitonesMin);
  const keyMax = Math.floor(ranges.pitchShiftSemitonesMax);
  const key = clamp(Math.round(adjustment.pitchShiftSemitones ?? 0), keyMin, keyMax);
  const tempo = clamp(roundTempo(adjustment.tempo || 1), ranges.tempoMin, ranges.tempoMax);

  const change = (next: { key?: number; tempo?: number }) => {
    const pitchShiftSemitones = clamp(next.key ?? key, keyMin, keyMax);
    const nextTempo = clamp(roundTempo(next.tempo ?? tempo), ranges.tempoMin, ranges.tempoMax);
    if (pitchShiftSemitones === key && nextTempo === tempo) return;
    onChange({ pitchShiftSemitones, tempo: nextTempo });
  };

  const keyText = t("karaokeKeyValue", { key: formatSemitones(key) });
  const tempoText = t("karaokeTempoValue", { percent: Math.round(tempo * 100) });
  const status = t(applied === false ? "karaokeStatusNotApplied" : "karaokeStatus", {
    key: keyText,
    tempo: tempoText,
  });

  const step = (label: string, disabled: boolean, onClick: () => void, icon: React.ReactNode) => (
    <Button
      type="button"
      variant="outline"
      size="icon-sm"
      aria-label={label}
      aria-disabled={disabled || undefined}
      className="aria-disabled:cursor-default aria-disabled:opacity-50"
      onClick={disabled ? undefined : onClick}
    >
      {icon}
    </Button>
  );

  return (
    <div
      role="group"
      aria-label={t("karaokeControlsLabel")}
      className="ml-auto flex flex-wrap items-center justify-end gap-x-4 gap-y-2 text-white"
    >
      <div className="flex items-center gap-1.5">
        {step(
          t("karaokeKeyDown"),
          key <= keyMin,
          () => change({ key: key - 1 }),
          <Minus aria-hidden />,
        )}
        <span className="min-w-[5.5rem] text-center text-sm font-medium tabular-nums">
          {keyText}
        </span>
        {step(
          t("karaokeKeyUp"),
          key >= keyMax,
          () => change({ key: key + 1 }),
          <Plus aria-hidden />,
        )}
        {step(t("karaokeKeyReset"), key === 0, () => change({ key: 0 }), <RotateCcw aria-hidden />)}
      </div>
      <div className="flex items-center gap-1.5">
        {step(
          t("karaokeTempoDown"),
          tempo <= ranges.tempoMin,
          () => change({ tempo: tempo - TEMPO_STEP }),
          <Minus aria-hidden />,
        )}
        <span className="min-w-[6.5rem] text-center text-sm font-medium tabular-nums">
          {tempoText}
        </span>
        {step(
          t("karaokeTempoUp"),
          tempo >= ranges.tempoMax,
          () => change({ tempo: tempo + TEMPO_STEP }),
          <Plus aria-hidden />,
        )}
        {step(
          t("karaokeTempoReset"),
          tempo === 1,
          () => change({ tempo: 1 }),
          <RotateCcw aria-hidden />,
        )}
      </div>
      {applied === false && (
        <span className="rounded-md bg-black/60 px-2 py-0.5 text-xs font-medium text-[var(--state-warning)]">
          {t("karaokeNotApplied")}
        </span>
      )}
      <p className="sr-only" role="status" aria-live="polite">
        {status}
      </p>
    </div>
  );
}
