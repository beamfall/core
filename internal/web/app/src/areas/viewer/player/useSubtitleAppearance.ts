import { useEffect, useRef } from "react";
import type { SubtitleAppearance } from "../lib/subtitleAppearance";

// Applies the Profile's subtitle appearance (CORE-1681) to the player. Size,
// background, font, colour, opacity and edge ride CSS custom properties and a
// background class on the stage, read by the video::cue rules in
// player-deferred.css. Position rewrites each VTTCue's line (keeping the
// authored setting so "auto" restores it), for cues already loaded and, through
// cuechange, for cues hls.js adds later. hls.js renders through native
// TextTracks by default, so both routes are covered. Chromium's VTTCue has no
// lineAlign and places a cue by its top edge, so there the line is raised by
// the cue's estimated height to keep its bottom edge at the chosen point.

// The cue's line-height (player-deferred.css) and the default cue font size,
// 5% of the video's smaller dimension, which Chromium applies to the cue box.
const CUE_LINE_HEIGHT = 1.3;
const CUE_FONT_RATIO = 0.05;

const FONT_STACKS: Record<SubtitleAppearance["font"], string> = {
  sans: "system-ui, sans-serif",
  serif: 'Georgia, "Times New Roman", serif',
  mono: 'ui-monospace, "SF Mono", Menlo, Consolas, monospace',
  casual: '"Comic Sans MS", "Comic Neue", "Chalkboard SE", cursive',
};

const BACKGROUND_CLASSES = ["bf-subtitle-bg-none", "bf-subtitle-bg-dark", "bf-subtitle-bg-solid"];

function rgba(hex: string, alpha: string): string {
  const n = Number.parseInt(hex.slice(1), 16);
  return `rgb(${(n >> 16) & 255} ${(n >> 8) & 255} ${n & 255} / ${alpha})`;
}

/** The text-shadow for an edge style; the edge is black, or white behind black text. */
export function subtitleEdgeShadow(appearance: SubtitleAppearance): string {
  const edge = rgba(appearance.color === "#000000" ? "#ffffff" : "#000000", appearance.opacity);
  switch (appearance.edge) {
    case "outline":
      return [
        `-0.06em -0.06em 0 ${edge}`,
        `0.06em -0.06em 0 ${edge}`,
        `-0.06em 0.06em 0 ${edge}`,
        `0.06em 0.06em 0 ${edge}`,
      ].join(", ");
    case "drop-shadow":
      return `0.06em 0.06em 0.18em ${edge}`;
    case "raised":
      return `0.06em 0.06em 0 ${edge}`;
    case "depressed":
      return `-0.06em -0.06em 0 ${edge}`;
    default:
      return "none";
  }
}

/** The custom properties the video::cue rules read. */
export function subtitleStageVars(appearance: SubtitleAppearance): Record<string, string> {
  return {
    "--bf-subtitle-size-scale": appearance.size,
    "--bf-subtitle-color": rgba(appearance.color, appearance.opacity),
    "--bf-subtitle-font": FONT_STACKS[appearance.font],
    "--bf-subtitle-edge": subtitleEdgeShadow(appearance),
  };
}

interface AuthoredPlacement {
  line: LineAndPositionSetting;
  snapToLines: boolean;
  lineAlign: LineAlignSetting;
}

const authored = new WeakMap<TextTrackCue, AuthoredPlacement>();

/** True when VTTCue honours lineAlign (not Chromium); assumed where VTTCue is absent. */
function hasNativeLineAlign(): boolean {
  return typeof VTTCue === "undefined" || "lineAlign" in VTTCue.prototype;
}

/** One rendered cue line as a percentage of the video height, for the top-edge fallback. */
export function cueLinePercent(width: number, height: number, sizeScale: string): number {
  if (!width || !height) return 0;
  const fontPx = Math.min(width, height) * CUE_FONT_RATIO * Number(sizeScale);
  return ((fontPx * CUE_LINE_HEIGHT) / height) * 100;
}

/**
 * Place one cue: "auto" restores its authored line, a percent pins its bottom
 * edge. Pass linePercent where lineAlign is unsupported: the cue's top edge is
 * then raised by its line count times linePercent.
 */
export function placeCue(
  raw: TextTrackCue,
  position: SubtitleAppearance["position"],
  linePercent?: number,
): void {
  if (!("line" in raw)) return;
  const cue = raw as VTTCue;
  let base = authored.get(cue);
  if (!base) {
    if (position === "auto") return;
    base = { line: cue.line, snapToLines: cue.snapToLines, lineAlign: cue.lineAlign };
    authored.set(cue, base);
  }
  if (position === "auto") {
    cue.snapToLines = base.snapToLines;
    cue.line = base.line;
    cue.lineAlign = base.lineAlign;
    return;
  }
  let line = 100 - Number(position);
  if (linePercent !== undefined) {
    const lines = typeof cue.text === "string" ? cue.text.split("\n").length : 1;
    line = Math.max(0, line - lines * linePercent);
  }
  if (cue.snapToLines || cue.line !== line || cue.lineAlign !== "end") {
    cue.snapToLines = false;
    cue.line = line;
    cue.lineAlign = "end";
  }
}

export function useSubtitleAppearance({
  stageRef,
  videoRef,
  tracksVersion,
  appearance,
}: {
  stageRef: React.RefObject<HTMLElement | null>;
  videoRef: React.RefObject<HTMLVideoElement | null>;
  /** Bumped by usePlayer when the live track set changes. */
  tracksVersion: number;
  appearance: SubtitleAppearance;
}) {
  const { size, background, font, color, opacity, edge, position } = appearance;

  useEffect(() => {
    const stage = stageRef.current;
    if (!stage) return;
    const vars = subtitleStageVars({ size, background, font, color, opacity, edge, position });
    for (const [name, value] of Object.entries(vars)) stage.style.setProperty(name, value);
    stage.classList.remove(...BACKGROUND_CLASSES);
    stage.classList.add(`bf-subtitle-bg-${background}`);
  }, [stageRef, size, background, font, color, opacity, edge, position]);

  const placement = useRef({ position, linePercent: undefined as number | undefined });

  useEffect(() => {
    const video = videoRef.current;
    if (!video || !video.textTracks) return;
    const linePercent = hasNativeLineAlign()
      ? undefined
      : cueLinePercent(video.clientWidth, video.clientHeight, size);
    placement.current = { position, linePercent };
    const tracks = Array.from(video.textTracks);
    for (const track of tracks) {
      for (const cue of track.cues ? Array.from(track.cues) : []) {
        placeCue(cue, position, linePercent);
      }
    }
    // Cues added after this pass (hls.js appends as segments load) are placed
    // as they become active.
    const onCueChange = (event: Event) => {
      const track = event.target as TextTrack;
      for (const cue of track.activeCues ? Array.from(track.activeCues) : []) {
        placeCue(cue, placement.current.position, placement.current.linePercent);
      }
    };
    for (const track of tracks) track.addEventListener("cuechange", onCueChange);
    return () => {
      for (const track of tracks) track.removeEventListener("cuechange", onCueChange);
    };
  }, [videoRef, tracksVersion, position, size]);
}
