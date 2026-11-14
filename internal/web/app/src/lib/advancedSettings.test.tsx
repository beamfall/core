import "@testing-library/jest-dom/vitest";
import { act, render, renderHook, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  ADVANCED_SETTINGS_KEY,
  Advanced,
  AdvancedSettingsSwitch,
  setAdvancedSettings,
  useAdvancedSettings,
  useShowAdvanced,
} from "./advancedSettings";
import { I18nProvider } from "./i18n";

afterEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
  setAdvancedSettings(false);
  localStorage.clear();
});

describe("advanced settings preference (CORE-1117)", () => {
  it("defaults to Normal", () => {
    const { result } = renderHook(() => useAdvancedSettings());
    expect(result.current[0]).toBe(false);
  });

  it("persists per browser and updates every reader", () => {
    const first = renderHook(() => useAdvancedSettings());
    const second = renderHook(() => useAdvancedSettings());

    act(() => first.result.current[1](true));

    expect(localStorage.getItem(ADVANCED_SETTINGS_KEY)).toBe("1");
    expect(first.result.current[0]).toBe(true);
    expect(second.result.current[0]).toBe(true);
  });

  it("follows a change made in another tab", () => {
    const { result } = renderHook(() => useAdvancedSettings());

    act(() => {
      localStorage.setItem(ADVANCED_SETTINGS_KEY, "1");
      window.dispatchEvent(new StorageEvent("storage", { key: ADVANCED_SETTINGS_KEY }));
    });

    expect(result.current[0]).toBe(true);
  });

  it("still switches for the session when storage refuses writes", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    const { result } = renderHook(() => useAdvancedSettings());

    act(() => result.current[1](true));

    expect(result.current[0]).toBe(true);
  });

  it("reads Normal when storage cannot be read", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    const { result } = renderHook(() => useAdvancedSettings());
    expect(result.current[0]).toBe(false);
  });
});

describe("useShowAdvanced", () => {
  it("shows a non-default control in Normal and keeps it after it returns to default", () => {
    const { result, rerender } = renderHook(({ set }) => useShowAdvanced(set), {
      initialProps: { set: false },
    });
    expect(result.current).toBe(false);

    rerender({ set: true });
    expect(result.current).toBe(true);

    rerender({ set: false });
    expect(result.current).toBe(true);
  });

  it("shows everything in Advanced", () => {
    setAdvancedSettings(true);
    const { result } = renderHook(() => useShowAdvanced());
    expect(result.current).toBe(true);
  });
});

describe("AdvancedSettingsSwitch", () => {
  it("reveals Advanced content when switched on", async () => {
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <AdvancedSettingsSwitch />
        <Advanced>
          <p>Expert control</p>
        </Advanced>
        <Advanced keep>
          <p>Configured control</p>
        </Advanced>
      </I18nProvider>,
    );

    expect(screen.queryByText("Expert control")).toBeNull();
    expect(screen.getByText("Configured control")).toBeVisible();

    await user.click(screen.getByRole("switch", { name: "Show advanced settings" }));

    expect(screen.getByText("Expert control")).toBeVisible();
    expect(localStorage.getItem(ADVANCED_SETTINGS_KEY)).toBe("1");
  });
});
