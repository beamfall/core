import { useLayoutEffect, useMemo, useRef, useState } from "react";

/** Horizontal pixels per bar (bar plus gap) at the measured track width. */
const BAR_PITCH_PX = 3;
/** Bar count used before the track has been measured (and in tests). */
const DEFAULT_BARS = 160;
/** Silence still draws a hairline, so the bar never reads as empty. */
const MIN_BAR = 0.06;

/**
 * Folds the envelope into `count` bars, keeping each bucket's peak so short
 * transients stay visible at any width.
 */
export function foldPeaks(points: readonly number[], count: number): number[] {
  if (count <= 0) return [];
  if (points.length === 0) return new Array<number>(count).fill(MIN_BAR);
  const bars: number[] = [];
  for (let i = 0; i < count; i++) {
    const start = Math.floor((i * points.length) / count);
    const end = Math.max(start + 1, Math.floor(((i + 1) * points.length) / count));
    let peak = 0;
    for (let j = start; j < end && j < points.length; j++) peak = Math.max(peak, points[j]);
    bars.push(Math.max(MIN_BAR, Math.min(1, peak)));
  }
  return bars;
}

function barsPath(bars: readonly number[]): string {
  // One path in a viewBox of `bars.length` x 1: each bar is centred in its
  // unit column, 60% wide, mirrored around the middle line.
  let d = "";
  for (let i = 0; i < bars.length; i++) {
    const h = bars[i];
    d += `M${(i + 0.2).toFixed(2)} ${((1 - h) / 2).toFixed(3)}h0.6v${h.toFixed(3)}h-0.6z`;
  }
  return d;
}

/**
 * The music seek bar's waveform (CORE-2401): unplayed peaks underneath and the
 * played peaks on top, clipped at the playhead so progress needs no redraw.
 * Purely visual; the slider around it carries the role, value and keyboard.
 * With `points` null it draws a flat placeholder of the same shape.
 */
export function WaveformBars({
  points,
  progressPercent,
}: {
  points: readonly number[] | null;
  progressPercent: number;
}) {
  const ref = useRef<HTMLSpanElement>(null);
  const [width, setWidth] = useState(0);

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver((entries) => {
      const next = Math.round(entries[0]?.contentRect.width ?? 0);
      setWidth((w) => (w === next ? w : next));
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  const count = width > 0 ? Math.max(24, Math.floor(width / BAR_PITCH_PX)) : DEFAULT_BARS;
  const path = useMemo(() => barsPath(foldPeaks(points ?? [], count)), [points, count]);
  const played = Math.max(0, Math.min(100, progressPercent));

  return (
    <span
      ref={ref}
      aria-hidden="true"
      className={`bf-waveform${points ? "" : " is-placeholder"}`}
      data-testid="scrubber-waveform"
    >
      <svg
        className="bf-waveform-layer is-unplayed"
        viewBox={`0 0 ${count} 1`}
        preserveAspectRatio="none"
      >
        <path d={path} />
      </svg>
      <svg
        className="bf-waveform-layer is-played"
        viewBox={`0 0 ${count} 1`}
        preserveAspectRatio="none"
        style={{ clipPath: `inset(0 ${(100 - played).toFixed(3)}% 0 0)` }}
      >
        <path d={path} />
      </svg>
    </span>
  );
}
