import { act, fireEvent, render, renderHook, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import manifest from "../../../../../vendor/oscillux/visuals.json";
import { VisualControls } from "../VisualControls";
import { VisualSettings } from "../VisualSettings";
import {
  getVisualChoice,
  resetVisualChoiceForTest,
  setShownVisual,
  setVisualChoice,
  stepVisual,
  toggleVisualizer,
  useVisualChoice,
} from "./visualChoice";
import { nextInRotation, steppedVisual, VISUALS, visualById } from "./visuals";

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
    expect(VISUALS).toHaveLength(63);
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

  it("shows a select with rotate, album, off and every visual by name", () => {
    vi.stubGlobal("WebGL2RenderingContext", class {});
    vi.stubGlobal("OfflineAudioContext", class {});
    vi.stubGlobal("ResizeObserver", class {});
    render(
      <I18nProvider initialLocale="en">
        <VisualSettings />
      </I18nProvider>,
    );
    const select = screen.getByRole("combobox");
    const options = Array.from(select.querySelectorAll("option"));
    expect(options).toHaveLength(VISUALS.length + 3);
    expect(options.map((o) => o.value).slice(0, 3)).toEqual(["rotate", "album", "off"]);
    expect(options[2].textContent).toBe("Off");
    expect(options[3].textContent).toBe(VISUALS[0].name);
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

describe("visualizer off and stepping (CORE-2814)", () => {
  const calm = VISUALS.filter((v) => v.calm);
  const last = VISUALS[VISUALS.length - 1];

  beforeEach(() => {
    window.localStorage.clear();
    resetVisualChoiceForTest();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("keeps off in this browser and turns back on to the choice before it", () => {
    setVisualChoice("album");
    toggleVisualizer();
    expect(getVisualChoice()).toBe("off");
    expect(window.localStorage.getItem(KEY)).toBe("off");
    resetVisualChoiceForTest();
    expect(getVisualChoice()).toBe("off");
    // A reload forgets the choice before off, so on means rotate.
    toggleVisualizer();
    expect(getVisualChoice()).toBe("rotate");
    setVisualChoice(VISUALS[5].id);
    toggleVisualizer();
    toggleVisualizer();
    expect(getVisualChoice()).toBe(VISUALS[5].id);
  });

  it("steps through the catalog in order and wraps at both ends", () => {
    expect(steppedVisual(VISUALS[0].id, 1, false).id).toBe(VISUALS[1].id);
    expect(steppedVisual(VISUALS[1].id, -1, false).id).toBe(VISUALS[0].id);
    expect(steppedVisual(last.id, 1, false).id).toBe(VISUALS[0].id);
    expect(steppedVisual(VISUALS[0].id, -1, false).id).toBe(last.id);
    expect(steppedVisual(null, 1, false).id).toBe(VISUALS[0].id);
    expect(steppedVisual(null, -1, false).id).toBe(last.id);
  });

  it("steps only between calm visuals under reduced motion", () => {
    expect(calm.length).toBeGreaterThan(1);
    expect(steppedVisual(calm[0].id, 1, true).id).toBe(calm[1].id);
    expect(steppedVisual(calm[0].id, -1, true).id).toBe(calm[calm.length - 1].id);
    const busy = VISUALS.find((v) => !v.calm);
    expect(busy).toBeDefined();
    expect(steppedVisual(busy?.id ?? null, 1, true).calm).toBe(true);
  });

  it("pins the visual next to the one on screen, whatever the choice", () => {
    setShownVisual(VISUALS[7].id);
    stepVisual(1, false);
    expect(getVisualChoice()).toBe(VISUALS[8].id);
    setShownVisual(VISUALS[8].id);
    stepVisual(-1, false);
    stepVisual(-1, false);
    // Without a newer report from the Visualizer, a step moves from the pinned choice.
    expect(getVisualChoice()).toBe(VISUALS[7].id);
  });

  it("turns back on from off to the visual shown last, or the first", () => {
    setVisualChoice("off");
    stepVisual(1, false);
    expect(getVisualChoice()).toBe(VISUALS[0].id);
    setShownVisual(VISUALS[9].id);
    setVisualChoice("off");
    stepVisual(-1, false);
    expect(getVisualChoice()).toBe(VISUALS[9].id);
    const busy = VISUALS.find((v) => !v.calm);
    setShownVisual(busy?.id ?? "");
    setVisualChoice("off");
    stepVisual(1, true);
    expect(visualById(getVisualChoice())?.calm).toBe(true);
  });

  it("names the visual on screen and steps and toggles from the transport control", () => {
    vi.stubGlobal("WebGL2RenderingContext", class {});
    vi.stubGlobal("OfflineAudioContext", class {});
    vi.stubGlobal("ResizeObserver", class {});
    vi.stubGlobal("matchMedia", () => ({ matches: false }));
    setShownVisual(VISUALS[2].id);
    render(
      <I18nProvider initialLocale="en">
        <VisualControls />
      </I18nProvider>,
    );
    const group = screen.getByRole("group", { name: "Visualizer" });
    const buttons = Array.from(group.querySelectorAll("button"));
    expect(buttons.map((b) => b.getAttribute("aria-label"))).toEqual([
      "Previous visual",
      `Visualizer: ${VISUALS[2].name}`,
      "Next visual",
    ]);
    const toggle = screen.getByRole("button", { name: `Visualizer: ${VISUALS[2].name}` });
    expect(toggle.getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(screen.getByRole("button", { name: "Next visual" }));
    expect(getVisualChoice()).toBe(VISUALS[3].id);
    fireEvent.click(toggle);
    expect(getVisualChoice()).toBe("off");
    const off = screen.getByRole("button", { name: "Visualizer: Off" });
    expect(off.getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(screen.getByRole("button", { name: "Previous visual" }));
    expect(getVisualChoice()).toBe(VISUALS[2].id);
  });

  it("renders no transport control where the browser cannot draw a visual", () => {
    vi.stubGlobal("WebGL2RenderingContext", undefined);
    const { container } = render(
      <I18nProvider initialLocale="en">
        <VisualControls />
      </I18nProvider>,
    );
    expect(container.firstChild).toBeNull();
  });
});
