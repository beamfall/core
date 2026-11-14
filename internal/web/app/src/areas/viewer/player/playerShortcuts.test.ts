import { describe, expect, it } from "vitest";
import {
  FRAME_SECONDS,
  frameStepTarget,
  nextTrackId,
  resolvePlayerShortcut,
  SHORTCUT_SHEET,
  stepSpeed,
  subtitleToggleId,
} from "./playerShortcuts";

const key = (k: string, init: Partial<KeyboardEvent> = {}) =>
  resolvePlayerShortcut({
    key: k,
    altKey: false,
    ctrlKey: false,
    metaKey: false,
    shiftKey: false,
    ...init,
  });

describe("resolvePlayerShortcut (CORE-1682)", () => {
  it("maps the YouTube-style keys", () => {
    expect(key("j")?.action).toBe("jumpBack");
    expect(key("L")?.action).toBe("jumpForward");
    expect(key(",")?.action).toBe("frameBack");
    expect(key(".")?.action).toBe("frameForward");
    expect(key("c")?.action).toBe("toggleSubtitles");
    expect(key("s")?.action).toBe("nextSubtitle");
    expect(key("a")?.action).toBe("nextAudio");
    expect(key("PageUp")?.action).toBe("previousChapter");
    expect(key("PageDown")?.action).toBe("nextChapter");
  });

  it("reads digits as percent seeks, shifted or not", () => {
    expect(key("0")).toEqual({ action: "seekPercent", percent: 0 });
    expect(key("7")).toEqual({ action: "seekPercent", percent: 70 });
    // AZERTY layouts type digits with Shift.
    expect(key("3", { shiftKey: true })).toEqual({ action: "seekPercent", percent: 30 });
  });

  it("accepts Shift only where the character carries it", () => {
    expect(key("?", { shiftKey: true })?.action).toBe("help");
    expect(key(">", { shiftKey: true })?.action).toBe("faster");
    // A layout with an unshifted < key.
    expect(key("<")?.action).toBe("slower");
    expect(key("N", { shiftKey: true })?.action).toBe("nextItem");
    expect(key("P", { shiftKey: true })?.action).toBe("previousItem");
    expect(key("n")).toBeNull();
    expect(key("J", { shiftKey: true })).toBeNull();
    expect(key("ArrowRight", { shiftKey: true })).toBeNull();
  });

  it("leaves browser chords alone", () => {
    expect(key("l", { ctrlKey: true })).toBeNull();
    expect(key("5", { metaKey: true })).toBeNull();
    expect(key("?", { altKey: true, shiftKey: true })).toBeNull();
  });

  it("lists every sheet row once", () => {
    const labels = SHORTCUT_SHEET.map((row) => row.label);
    expect(new Set(labels).size).toBe(labels.length);
  });
});

describe("stepSpeed", () => {
  const speeds = [0.5, 0.75, 1, 1.25, 1.5, 2];
  it("steps through the menu values and stops at the ends", () => {
    expect(stepSpeed(speeds, 1, 1)).toBe(1.25);
    expect(stepSpeed(speeds, 1, -1)).toBe(0.75);
    expect(stepSpeed(speeds, 2, 1)).toBeNull();
    expect(stepSpeed(speeds, 0.5, -1)).toBeNull();
    // An off-menu rate snaps to the neighbouring value.
    expect(stepSpeed(speeds, 1.1, 1)).toBe(1.25);
    expect(stepSpeed(speeds, 1.1, -1)).toBe(1);
  });
});

describe("track helpers", () => {
  const subs = (selected: string) =>
    ["off", "0", "1"].map((id) => ({ id, selected: id === selected, disabled: false }));

  it("cycles to the next selectable track, wrapping through off", () => {
    expect(nextTrackId(subs("off"))).toBe("0");
    expect(nextTrackId(subs("1"))).toBe("off");
    expect(
      nextTrackId([
        { id: "0", selected: true, disabled: false },
        { id: "1", selected: false, disabled: true },
        { id: "2", selected: false, disabled: false },
      ]),
    ).toBe("2");
    expect(nextTrackId([{ id: "0", selected: true, disabled: false }])).toBeNull();
    expect(nextTrackId([])).toBeNull();
  });

  it("toggles captions off and back to the remembered track", () => {
    expect(subtitleToggleId(subs("1"), "1")).toBe("off");
    expect(subtitleToggleId(subs("off"), "1")).toBe("1");
    expect(subtitleToggleId(subs("off"), null)).toBe("0");
    expect(subtitleToggleId(subs("off"), "9")).toBe("0");
    expect(subtitleToggleId([{ id: "off", selected: true, disabled: false }], null)).toBeNull();
  });
});

describe("frameStepTarget", () => {
  it("lands mid-way into the neighbouring measured frame", () => {
    const clock = { frame: 1 / 24, mediaTime: 10 };
    expect(frameStepTarget(clock, 10.01, 1)).toBeCloseTo(10 + 1 / 24 + 1 / 48);
    expect(frameStepTarget(clock, 10.01, -1)).toBeCloseTo(10 - 1 / 24 + 1 / 48);
  });

  it("steps the playhead itself when the shown frame is unknown or stale", () => {
    expect(frameStepTarget({ frame: null, mediaTime: null }, 5, 1)).toBeCloseTo(5 + FRAME_SECONDS);
    expect(frameStepTarget({ frame: 1 / 60, mediaTime: 2 }, 5, -1)).toBeCloseTo(5 - 1 / 60);
  });
});
