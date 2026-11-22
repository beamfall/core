import { type RefObject, useCallback, useEffect, useRef, useState } from "react";
import { loadOscillux, type Oscillux } from "./visualizer/oscillux";

// Ambient mode (CORE-3216): an OLED-safe audio stage. The choice belongs to this
// browser. While idle the stage dims and its card wanders between safe anchors so
// no pixel holds the same content for long; input or a track change wakes it.
// The wander and the dim are Oscillux curves (ADR-0228): the host only maps
// their values onto CSS custom properties.

const CHOICE_KEY = "beamfall.ambientMode";
/** Input-free time before the stage goes idle. */
export const AMBIENT_IDLE_MS = 45_000;
/** After a track change wakes an idle stage, it goes idle again this much sooner. */
export const AMBIENT_TRACK_WAKE_MS = 10_000;
/** With reduced motion the card steps to the next anchor this often instead of drifting. */
const STEP_MS = 60_000;
const DIM_TO = 0.35;
const DIM_SECONDS = 6;
// Two incommensurate periods trace a slow figure that only repeats after hours.
const DRIFT_PERIOD_X = 97;
const DRIFT_PERIOD_Y = 131;

function readChoice(): boolean {
  try {
    return window.localStorage.getItem(CHOICE_KEY) === "on";
  } catch {
    return false;
  }
}

/** Whether Ambient mode is on in this browser, and a setter that remembers it. */
export function useAmbientChoice(): [boolean, (on: boolean) => void] {
  const [on, setOn] = useState(readChoice);
  const set = useCallback((value: boolean) => {
    setOn(value);
    try {
      if (value) window.localStorage.setItem(CHOICE_KEY, "on");
      else window.localStorage.removeItem(CHOICE_KEY);
    } catch {
      // A blocked store keeps the choice for this page only.
    }
  }, []);
  return [on, set];
}

const WAKE_EVENTS = ["pointermove", "pointerdown", "keydown", "wheel", "touchstart"] as const;

/** True once `enabled` has held for AMBIENT_IDLE_MS without input; `wakeKey` changes wake it. */
export function useAmbientIdle(enabled: boolean, wakeKey: string): boolean {
  const [idle, setIdle] = useState(false);
  const timer = useRef<number | null>(null);
  const idleRef = useRef(false);
  idleRef.current = idle;

  const arm = useCallback((ms: number) => {
    if (timer.current !== null) window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setIdle(true), ms);
  }, []);

  useEffect(() => {
    if (!enabled) {
      setIdle(false);
      return;
    }
    const wake = () => {
      setIdle(false);
      arm(AMBIENT_IDLE_MS);
    };
    arm(AMBIENT_IDLE_MS);
    for (const name of WAKE_EVENTS) window.addEventListener(name, wake, { passive: true });
    return () => {
      for (const name of WAKE_EVENTS) window.removeEventListener(name, wake);
      if (timer.current !== null) window.clearTimeout(timer.current);
      timer.current = null;
    };
  }, [enabled, arm]);

  // A new track shows its full info for a moment, then the stage settles again.
  const lastKey = useRef(wakeKey);
  useEffect(() => {
    if (lastKey.current === wakeKey) return;
    lastKey.current = wakeKey;
    if (enabled && idleRef.current) {
      setIdle(false);
      arm(AMBIENT_TRACK_WAKE_MS);
    }
  }, [wakeKey, enabled, arm]);

  return enabled && idle;
}

/** A safe anchor for reduced motion: a 3 × 3 grid walked in a fixed, non-adjacent order. */
const ANCHORS: readonly (readonly [number, number])[] = [
  [0, 0],
  [1, -1],
  [-1, 1],
  [1, 1],
  [-1, -1],
  [0, 1],
  [1, 0],
  [0, -1],
  [-1, 0],
];

type OscCurve = InstanceType<Oscillux["Curve"]>;
type Curves = {
  x: OscCurve;
  y: OscCurve;
  dim: OscCurve;
  input: InstanceType<Oscillux["CurveInput"]>;
};

function makeCurves(osc: Oscillux): Curves {
  const curve = (set: (config: InstanceType<Oscillux["CurveConfig"]>) => void) => {
    const config = new osc.CurveConfig();
    config.unit = osc.CurveUnit.Seconds;
    set(config);
    const made = new osc.Curve(config);
    config.free();
    return made;
  };
  const lfo = (period: number, phase: number) =>
    curve((c) => {
      c.kind = osc.CurveKind.Lfo;
      c.shape = osc.CurveShape.Sine;
      c.period = period;
      c.phase = phase;
      c.min = -1;
      c.max = 1;
      c.flags = osc.CurveFlag.Min | osc.CurveFlag.Max;
    });
  return {
    x: lfo(DRIFT_PERIOD_X, 0),
    y: lfo(DRIFT_PERIOD_Y, 0.25),
    dim: curve((c) => {
      c.kind = osc.CurveKind.Easing;
      c.easing = osc.CurveEasing.InOut;
      c.duration = DIM_SECONDS;
      c.from = 1;
      c.to = DIM_TO;
      c.flags = osc.CurveFlag.From | osc.CurveFlag.To;
    }),
    input: new osc.CurveInput(),
  };
}

const PROPS = ["--bf-ambient-x", "--bf-ambient-y", "--bf-ambient-dim"] as const;

/**
 * While `idle`, writes the drift (`--bf-ambient-x`, `--bf-ambient-y`, each -1..1) and the dim
 * (`--bf-ambient-dim`, 1 → 0.35) onto `target` from Oscillux curves; awake, it clears them so
 * the stylesheet's resting values (and their transition) apply. Without Oscillux the card stays
 * put and only the stylesheet's own idle state applies.
 */
export function useAmbientMotion(
  target: RefObject<HTMLElement | null>,
  idle: boolean,
  reducedMotion: boolean,
): void {
  useEffect(() => {
    const el = target.current;
    if (!el || !idle) return;
    let cancelled = false;
    let frame = 0;
    let curves: Curves | null = null;
    const started = performance.now();
    const write = (x: number, y: number, dim: number) => {
      el.style.setProperty(PROPS[0], x.toFixed(4));
      el.style.setProperty(PROPS[1], y.toFixed(4));
      el.style.setProperty(PROPS[2], dim.toFixed(3));
    };
    const evaluate = (curve: OscCurve, seconds: number, fallback: number) => {
      if (!curves) return fallback;
      curves.input.elapsedSeconds = seconds;
      return curve.evaluate(curves.input) ?? fallback;
    };
    const tick = () => {
      if (cancelled || !curves) return;
      const seconds = (performance.now() - started) / 1000;
      const dim = evaluate(curves.dim, seconds, DIM_TO);
      if (reducedMotion) {
        const [ax, ay] = ANCHORS[Math.floor((seconds * 1000) / STEP_MS) % ANCHORS.length];
        write(ax, ay, dim);
      } else {
        write(evaluate(curves.x, seconds, 0), evaluate(curves.y, seconds, 0), dim);
      }
      frame = requestAnimationFrame(tick);
    };
    loadOscillux().then(
      (osc) => {
        if (cancelled) return;
        curves = makeCurves(osc);
        frame = requestAnimationFrame(tick);
      },
      () => {
        // No Oscillux, no drift: the stylesheet still dims the idle stage.
      },
    );
    return () => {
      cancelled = true;
      cancelAnimationFrame(frame);
      if (curves) {
        curves.x.free();
        curves.y.free();
        curves.dim.free();
        curves.input.free();
      }
      for (const name of PROPS) el.style.removeProperty(name);
    };
  }, [target, idle, reducedMotion]);
}
