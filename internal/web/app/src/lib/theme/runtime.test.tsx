// The run-time theme seam (CORE-1179): presets and theme documents re-skin the app through the
// --theme-* custom properties on <html> without a reload; the scheme choice is device-local.
import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RegisterProvider, useRegister } from "@/lib/register";
import { isThemeRejection } from "./resolver";
import {
  APPEARANCE_STORAGE_KEY,
  DEFAULT_APPEARANCE,
  loadAppearance,
  resolveAppearance,
} from "./runtime";
import { THEME_DEFAULT_PRESET, THEME_PRESETS } from "./theme.generated";

const root = () => document.documentElement;
const role = (scheme: string, name: string) =>
  root().style.getPropertyValue(`--theme-${scheme}-${name}`);

let systemLight = false;
const listeners = new Set<() => void>();

beforeEach(() => {
  localStorage.clear();
  root().removeAttribute("style");
  delete root().dataset.theme;
  systemLight = false;
  listeners.clear();
  vi.stubGlobal("matchMedia", (query: string) => ({
    get matches() {
      return query === "(prefers-color-scheme: light)" && systemLight;
    },
    media: query,
    addEventListener: (_: string, fn: () => void) => listeners.add(fn),
    removeEventListener: (_: string, fn: () => void) => listeners.delete(fn),
  }));
});

afterEach(() => vi.unstubAllGlobals());

let api: ReturnType<typeof useRegister>;
function Probe() {
  api = useRegister();
  return <span data-testid="theme">{api.theme}</span>;
}
const mount = () =>
  render(
    <RegisterProvider>
      <Probe />
    </RegisterProvider>,
  );

describe("appearance storage", () => {
  it("defaults to dark and the default preset", () => {
    expect(loadAppearance()).toEqual({ scheme: "dark", preset: THEME_DEFAULT_PRESET });
  });

  it("drops unknown or corrupt values field by field", () => {
    localStorage.setItem(APPEARANCE_STORAGE_KEY, JSON.stringify({ scheme: "dim", preset: "moss" }));
    expect(loadAppearance()).toEqual({ scheme: "dark", preset: "moss" });
    localStorage.setItem(APPEARANCE_STORAGE_KEY, "{not json");
    expect(loadAppearance()).toEqual(DEFAULT_APPEARANCE);
  });
});

describe("shipped presets", () => {
  it("resolve to their generated role values in both schemes", () => {
    for (const preset of THEME_PRESETS) {
      const resolved = resolveAppearance({ preset: preset.id });
      if (isThemeRejection(resolved)) throw new Error(preset.id);
      expect(resolved.dark).toEqual(preset.dark);
      expect(resolved.light).toEqual(preset.light);
    }
  });
});

describe("RegisterProvider theme seam", () => {
  it("applies the stored preset on mount", () => {
    localStorage.setItem(
      APPEARANCE_STORAGE_KEY,
      JSON.stringify({ scheme: "dark", preset: "ember" }),
    );
    mount();
    const ember = THEME_PRESETS.find((p) => p.id === "ember");
    expect(role("dark", "accent")).toBe(ember?.dark.accent);
    expect(role("light", "bg-root")).toBe(ember?.light["bg-root"]);
  });

  it("re-skins in place when the preset changes, and remembers it on this device", () => {
    mount();
    const tide = THEME_PRESETS.find((p) => p.id === "tide");
    act(() => api.setPreset("tide"));
    expect(role("dark", "accent")).toBe(tide?.dark.accent);
    expect(JSON.parse(localStorage.getItem(APPEARANCE_STORAGE_KEY) ?? "{}").preset).toBe("tide");
    act(() => api.setPreset("not-a-preset"));
    expect(api.preset).toBe("tide");
  });

  it("applies a theme document without a reload and keeps it on rejection", () => {
    mount();
    let result: ReturnType<typeof api.applyThemeDocument> | undefined;
    act(() => {
      result = api.applyThemeDocument({
        schema_version: 3,
        color_overrides: { dark: { accent: "#e07a5f" }, light: { accent: "#e07a5f" } },
      });
    });
    expect(result && isThemeRejection(result)).toBe(false);
    expect(role("dark", "accent")).toBe("#e07a5f");

    act(() => {
      result = api.applyThemeDocument({
        schema_version: 3,
        color_overrides: { dark: { nope: 1 } },
      });
    });
    expect(result && isThemeRejection(result)).toBe(true);
    expect(role("dark", "accent")).toBe("#e07a5f");

    act(() => api.setPreset(THEME_DEFAULT_PRESET));
    expect(role("dark", "accent")).toBe(THEME_PRESETS[0].dark.accent);
  });

  it("shows light, dark or the system scheme and persists the choice", () => {
    mount();
    expect(screen.getByTestId("theme").textContent).toBe("dark");
    expect(root().dataset.theme).toBeUndefined();

    act(() => api.setSchemePreference("light"));
    expect(root().dataset.theme).toBe("light");
    expect(JSON.parse(localStorage.getItem(APPEARANCE_STORAGE_KEY) ?? "{}").scheme).toBe("light");

    act(() => api.setSchemePreference("system"));
    expect(root().dataset.theme).toBeUndefined();
    systemLight = true;
    act(() => {
      for (const fn of listeners) fn();
    });
    expect(root().dataset.theme).toBe("light");
    expect(screen.getByTestId("theme").textContent).toBe("light");
  });

  it("keeps the scheme in every register", () => {
    mount();
    act(() => api.setSchemePreference("light"));
    act(() => api.setRegister("manage"));
    expect(root().dataset.register).toBe("manage");
    expect(root().dataset.theme).toBe("light");
    act(() => api.setRegister("viewer"));
    expect(root().dataset.theme).toBe("light");
  });
});
