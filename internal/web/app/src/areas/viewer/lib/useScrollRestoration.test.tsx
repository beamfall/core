import { act, cleanup, render, waitFor } from "@testing-library/react";
import { MemoryRouter, type NavigateFunction, useNavigate } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetScrollPositionsForTests, useScrollRestoration } from "./useScrollRestoration";

let navigate!: NavigateFunction;
let scrollHeight = 0;

function Harness() {
  navigate = useNavigate();
  useScrollRestoration();
  return null;
}

function scrollWindowTo(y: number) {
  Object.defineProperty(window, "scrollY", { configurable: true, value: y });
  window.dispatchEvent(new Event("scroll"));
}

beforeEach(() => {
  scrollHeight = 5000;
  Object.defineProperty(document.documentElement, "scrollHeight", {
    configurable: true,
    get: () => scrollHeight,
  });
  Object.defineProperty(window, "innerHeight", { configurable: true, value: 800 });
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  render(
    <MemoryRouter initialEntries={["/browse"]}>
      <Harness />
    </MemoryRouter>,
  );
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  resetScrollPositionsForTests();
  scrollWindowTo(0);
});

describe("useScrollRestoration", () => {
  it("starts a new navigation at the top", () => {
    scrollWindowTo(1200);
    act(() => navigate("/entity/e1"));
    expect(window.scrollTo).toHaveBeenLastCalledWith(0, 0);
  });

  it("restores the saved offset on Back", () => {
    scrollWindowTo(1200);
    act(() => navigate("/entity/e1"));
    scrollWindowTo(0);
    vi.mocked(window.scrollTo).mockClear();

    act(() => navigate(-1));

    expect(window.scrollTo).toHaveBeenCalledWith(0, 1200);
  });

  it("waits for hydrating content to grow tall enough before restoring", async () => {
    scrollWindowTo(1200);
    act(() => navigate("/entity/e1"));
    scrollWindowTo(0);
    vi.mocked(window.scrollTo).mockClear();
    scrollHeight = 900; // skeletons only: 1200 is not reachable yet

    act(() => navigate(-1));
    expect(window.scrollTo).not.toHaveBeenCalled();
    // The clamped short page must not overwrite the saved offset meanwhile.
    scrollWindowTo(100);

    scrollHeight = 5000;
    await waitFor(() => expect(window.scrollTo).toHaveBeenCalledWith(0, 1200));
  });

  it("abandons a pending restore once the user scrolls themselves", async () => {
    scrollWindowTo(1200);
    act(() => navigate("/entity/e1"));
    scrollWindowTo(0);
    vi.mocked(window.scrollTo).mockClear();
    scrollHeight = 900;

    act(() => navigate(-1));
    window.dispatchEvent(new Event("wheel"));
    scrollHeight = 5000;

    await new Promise((resolve) => window.setTimeout(resolve, 50));
    expect(window.scrollTo).not.toHaveBeenCalled();
  });
});
