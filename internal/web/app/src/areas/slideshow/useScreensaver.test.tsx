import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { saveLastSlideshowSource, saveScreensaverEnabled } from "./lib/screensaver";
import { useScreensaver } from "./useScreensaver";

let at = "";
function Shell() {
  useScreensaver();
  const location = useLocation();
  at = `${location.pathname}${location.search}`;
  return null;
}

function renderAt(path: string) {
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="*" element={<Shell />} />
      </Routes>
    </MemoryRouter>,
  );
}

const FIVE_MINUTES = 5 * 60_000;

describe("useScreensaver", () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "performance", "Date"] });
    saveLastSlideshowSource({ kind: "favorites", libraryID: "lib" });
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    localStorage.clear();
  });

  it("opens the last Source after five idle minutes when the option is on", () => {
    saveScreensaverEnabled(true);
    renderAt("/");
    act(() => vi.advanceTimersByTime(FIVE_MINUTES - 10_000));
    expect(at).toBe("/");
    act(() => vi.advanceTimersByTime(15_000));
    expect(at).toBe("/slideshow?source=favorites&library=lib&screensaver=1");
  });

  it("opens nothing with the option off", () => {
    renderAt("/");
    act(() => vi.advanceTimersByTime(FIVE_MINUTES * 2));
    expect(at).toBe("/");
  });

  it("restarts the count on input", () => {
    saveScreensaverEnabled(true);
    renderAt("/");
    act(() => vi.advanceTimersByTime(4 * 60_000));
    fireEvent.pointerMove(window);
    act(() => vi.advanceTimersByTime(4 * 60_000));
    expect(at).toBe("/");
  });

  it("never opens over the player", () => {
    saveScreensaverEnabled(true);
    renderAt("/watch/m1");
    act(() => vi.advanceTimersByTime(FIVE_MINUTES * 2));
    expect(at).toBe("/watch/m1");
  });
});
