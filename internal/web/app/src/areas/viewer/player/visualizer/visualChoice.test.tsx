import { act, render, renderHook, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import manifest from "../../../../../vendor/oscillux/visuals.json";
import { VisualSettings } from "../VisualSettings";
import {
  getVisualChoice,
  resetVisualChoiceForTest,
  setVisualChoice,
  useVisualChoice,
} from "./visualChoice";
import { nextInRotation, VISUALS, visualById } from "./visuals";

const KEY = "beamfall.visualChoice";

describe("visual choice and rotation (CORE-2368)", () => {
  beforeEach(() => {
    window.localStorage.clear();
    resetVisualChoiceForTest();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("offers every visual the pinned Oscillux release ships for web", () => {
    expect(VISUALS).toHaveLength(61);
    expect(VISUALS.map((v) => v.id)).toEqual(manifest.visuals.map((v) => v.id));
    expect(new Set(VISUALS.map((v) => v.id)).size).toBe(VISUALS.length);
    expect(VISUALS.some((v) => v.calm)).toBe(true);
  });

  it("defaults to rotate and keeps a choice in this browser", () => {
    expect(getVisualChoice()).toBe("rotate");
    const id = VISUALS[3].id;
    setVisualChoice(id);
    expect(window.localStorage.getItem(KEY)).toBe(id);
    resetVisualChoiceForTest();
    expect(getVisualChoice()).toBe(id);
    setVisualChoice("album");
    expect(window.localStorage.getItem(KEY)).toBe("album");
    setVisualChoice("rotate");
    expect(window.localStorage.getItem(KEY)).toBeNull();
  });

  it("reads an unknown stored visual as rotate", () => {
    window.localStorage.setItem(KEY, "not-a-visual");
    expect(getVisualChoice()).toBe("rotate");
  });

  it("re-renders hook users when the choice changes", () => {
    const { result } = renderHook(() => useVisualChoice());
    act(() => setVisualChoice("album"));
    expect(result.current).toBe("album");
  });

  it("rotates to a different visual, and only calm ones under reduced motion", () => {
    for (let i = 0; i < 20; i++) {
      const r = i / 20;
      expect(nextInRotation(VISUALS[0].id, false, () => r).id).not.toBe(VISUALS[0].id);
      const calm = nextInRotation(null, true, () => r);
      expect(visualById(calm.id)?.calm).toBe(true);
    }
    expect(nextInRotation(null, false, () => 0.999999).id).toBe(VISUALS[VISUALS.length - 1].id);
  });

  it("shows a select with rotate, album and every visual by name", () => {
    vi.stubGlobal("WebGL2RenderingContext", class {});
    vi.stubGlobal("OfflineAudioContext", class {});
    render(
      <I18nProvider initialLocale="en">
        <VisualSettings />
      </I18nProvider>,
    );
    const select = screen.getByRole("combobox");
    const options = Array.from(select.querySelectorAll("option"));
    expect(options).toHaveLength(VISUALS.length + 2);
    expect(options.map((o) => o.value).slice(0, 2)).toEqual(["rotate", "album"]);
    expect(options[2].textContent).toBe(VISUALS[0].name);
  });

  it("renders nothing where the browser cannot draw a visual", () => {
    vi.stubGlobal("WebGL2RenderingContext", undefined);
    const { container } = render(
      <I18nProvider initialLocale="en">
        <VisualSettings />
      </I18nProvider>,
    );
    expect(container.querySelector("select")).toBeNull();
  });
});
