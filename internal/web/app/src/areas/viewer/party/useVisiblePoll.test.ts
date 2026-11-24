import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useVisiblePoll } from "./useVisiblePoll";

function setHidden(hidden: boolean) {
  Object.defineProperty(document, "hidden", { configurable: true, get: () => hidden });
}

afterEach(() => {
  setHidden(false);
  vi.useRealTimers();
});

describe("useVisiblePoll", () => {
  it("stops polling in a hidden page unless told to keep going", async () => {
    vi.useFakeTimers();
    setHidden(true);
    const paused = vi.fn(async () => {});
    const kept = vi.fn(async () => {});
    renderHook(() => {
      useVisiblePoll(paused, 1000);
      useVisiblePoll(kept, 1000, true, true);
    });

    await vi.advanceTimersByTimeAsync(3500);
    expect(paused).toHaveBeenCalledTimes(1);
    expect(kept).toHaveBeenCalledTimes(4);
  });
});
