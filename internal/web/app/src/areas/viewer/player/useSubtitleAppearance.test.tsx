import { renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { SUBTITLE_APPEARANCE_DEFAULTS, type SubtitleAppearance } from "../lib/subtitleAppearance";
import {
  placeCue,
  subtitleEdgeShadow,
  subtitleStageVars,
  useSubtitleAppearance,
} from "./useSubtitleAppearance";

function fakeCue(): TextTrackCue {
  return { line: "auto", snapToLines: true, lineAlign: "start" } as unknown as TextTrackCue;
}

function fakeTrack(cues: TextTrackCue[]) {
  const listeners = new Set<EventListener>();
  const track = {
    cues,
    activeCues: [] as TextTrackCue[],
    addEventListener: (_: string, fn: EventListener) => listeners.add(fn),
    removeEventListener: (_: string, fn: EventListener) => listeners.delete(fn),
    fire() {
      for (const fn of listeners) fn({ target: track } as unknown as Event);
    },
    listeners,
  };
  return track;
}

const look = (patch: Partial<SubtitleAppearance>): SubtitleAppearance => ({
  ...SUBTITLE_APPEARANCE_DEFAULTS,
  ...patch,
});

describe("subtitle appearance styling (CORE-1681)", () => {
  it("maps colour, opacity, font and size to stage custom properties", () => {
    const vars = subtitleStageVars(look({ color: "#ffff00", opacity: "0.75", size: "1.25" }));
    expect(vars["--bf-subtitle-color"]).toBe("rgb(255 255 0 / 0.75)");
    expect(vars["--bf-subtitle-size-scale"]).toBe("1.25");
    expect(vars["--bf-subtitle-edge"]).toBe("none");
    expect(subtitleStageVars(look({ font: "mono" }))["--bf-subtitle-font"]).toContain("monospace");
  });

  it("draws the edge in black, or white behind black text", () => {
    expect(subtitleEdgeShadow(look({ edge: "drop-shadow" }))).toBe(
      "0.06em 0.06em 0.18em rgb(0 0 0 / 1)",
    );
    expect(subtitleEdgeShadow(look({ edge: "raised", color: "#000000" }))).toBe(
      "0.06em 0.06em 0 rgb(255 255 255 / 1)",
    );
    expect(subtitleEdgeShadow(look({ edge: "outline" })).split(", ")).toHaveLength(4);
  });

  it("pins a cue's bottom edge and restores the authored line for auto", () => {
    const cue = fakeCue() as unknown as VTTCue;
    placeCue(cue, "auto");
    expect(cue.snapToLines).toBe(true);
    placeCue(cue, "25");
    expect([cue.line, cue.snapToLines, cue.lineAlign]).toEqual([75, false, "end"]);
    placeCue(cue, "80");
    expect(cue.line).toBe(20);
    placeCue(cue, "auto");
    expect([cue.line, cue.snapToLines, cue.lineAlign]).toEqual(["auto", true, "start"]);
  });

  it("applies the look to the stage and places loaded and newly active cues", () => {
    const stage = document.createElement("div");
    stage.classList.add("bf-subtitle-bg-dark");
    const loaded = fakeCue() as unknown as VTTCue;
    const late = fakeCue() as unknown as VTTCue;
    const track = fakeTrack([loaded]);
    const video = { textTracks: [track] } as unknown as HTMLVideoElement;
    const { rerender, unmount } = renderHook(
      (appearance: SubtitleAppearance) =>
        useSubtitleAppearance({
          stageRef: { current: stage },
          videoRef: { current: video },
          tracksVersion: 1,
          appearance,
        }),
      { initialProps: look({ background: "solid", color: "#00ffff", position: "50" }) },
    );

    expect(stage.classList.contains("bf-subtitle-bg-solid")).toBe(true);
    expect(stage.classList.contains("bf-subtitle-bg-dark")).toBe(false);
    expect(stage.style.getPropertyValue("--bf-subtitle-color")).toBe("rgb(0 255 255 / 1)");
    expect(loaded.line).toBe(50);

    track.cues.push(late);
    track.activeCues = [late];
    track.fire();
    expect(late.line).toBe(50);

    rerender(look({ position: "auto" }));
    expect(loaded.line).toBe("auto");
    expect(late.line).toBe("auto");

    unmount();
    expect(track.listeners.size).toBe(0);
  });
});
