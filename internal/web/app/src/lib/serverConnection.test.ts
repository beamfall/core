import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import {
  reportEventStreamState,
  resetServerConnectionForTest,
  useServerConnectionState,
  useServerRecoveryEpoch,
} from "./serverConnection";

afterEach(() => {
  resetServerConnectionForTest();
});

describe("serverConnection", () => {
  it("tracks reconnecting state and increments recoveryEpoch only on reconnecting -> live", () => {
    const state = renderHook(() => useServerConnectionState());
    const epoch = renderHook(() => useServerRecoveryEpoch());

    expect(state.result.current).toBe("live");
    expect(epoch.result.current).toBe(0);

    // live -> live is a no-op
    expect(reportEventStreamState("live")).toBe(false);
    state.rerender();
    epoch.rerender();
    expect(epoch.result.current).toBe(0);

    // live -> reconnecting
    expect(reportEventStreamState("reconnecting")).toBe(false);
    state.rerender();
    epoch.rerender();
    expect(state.result.current).toBe("reconnecting");
    expect(epoch.result.current).toBe(0);

    // reconnecting -> live increments recoveryEpoch
    expect(reportEventStreamState("live")).toBe(true);
    state.rerender();
    epoch.rerender();
    expect(state.result.current).toBe("live");
    expect(epoch.result.current).toBe(1);
  });
});
