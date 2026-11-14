import { describe, expect, it } from "vitest";
import {
  encodeSubtitleAppearance,
  resolveSubtitleAppearance,
  SUBTITLE_APPEARANCE_DEFAULTS,
  SUBTITLE_APPEARANCE_FIELDS,
  SUBTITLE_APPEARANCE_KEYS,
  SUBTITLE_APPEARANCE_OPTIONS,
} from "./subtitleAppearance";

describe("subtitle appearance prefs (CORE-1681)", () => {
  it("uses the subtitle.* keys native clients read", () => {
    expect(SUBTITLE_APPEARANCE_KEYS).toEqual({
      size: "subtitle.size",
      background: "subtitle.background",
      font: "subtitle.font",
      color: "subtitle.color",
      opacity: "subtitle.opacity",
      edge: "subtitle.edge",
      position: "subtitle.position",
    });
    for (const field of SUBTITLE_APPEARANCE_FIELDS) {
      expect(SUBTITLE_APPEARANCE_OPTIONS[field]).toContain(SUBTITLE_APPEARANCE_DEFAULTS[field]);
    }
  });

  it("resolves an empty list to the defaults", () => {
    expect(resolveSubtitleAppearance([])).toEqual({
      size: "1",
      background: "dark",
      font: "sans",
      color: "#ffffff",
      opacity: "1",
      edge: "none",
      position: "auto",
    });
  });

  it("reads stored values and drops unknown ones to the default", () => {
    const appearance = resolveSubtitleAppearance([
      { key: "subtitle.size", value: "1.5" },
      { key: "subtitle.background", value: "solid" },
      { key: "subtitle.font", value: "serif" },
      { key: "subtitle.color", value: "#FFFF00" },
      { key: "subtitle.opacity", value: "0.5" },
      { key: "subtitle.edge", value: "outline" },
      { key: "subtitle.position", value: "33" },
      { key: "playback.speed-default", value: "2" },
    ]);
    expect(appearance).toEqual({
      size: "1.5",
      background: "solid",
      font: "serif",
      color: "#ffffff",
      opacity: "0.5",
      edge: "outline",
      position: "auto",
    });
  });

  it("clears the key for the default or an unknown value", () => {
    expect(encodeSubtitleAppearance("color", "#ffff00")).toBe("#ffff00");
    expect(encodeSubtitleAppearance("position", "50")).toBe("50");
    expect(encodeSubtitleAppearance("color", "#ffffff")).toBe("");
    expect(encodeSubtitleAppearance("background", "dark")).toBe("");
    expect(encodeSubtitleAppearance("edge", "glow")).toBe("");
  });
});
