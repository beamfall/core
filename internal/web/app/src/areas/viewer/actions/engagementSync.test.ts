import { cleanup, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { subscribeEvents } from "@/lib/api";
import { requestManifestRefresh } from "@/lib/manifestRefresh";
import {
  resetServerConnectionForTest,
  useServerConnectionState,
  useServerRecoveryEpoch,
} from "@/lib/serverConnection";
import { useEngagementEpoch, useEngagementEvents } from "./engagementSync";

vi.mock("@/lib/api", () => ({ subscribeEvents: vi.fn(() => () => {}) }));
vi.mock("@/lib/manifestRefresh", () => ({ requestManifestRefresh: vi.fn() }));

afterEach(() => {
  cleanup();
  resetServerConnectionForTest();
});

describe("engagement events", () => {
  it("moves the epoch on an engagement invalidation and on a full refresh, not on other events", () => {
    renderHook(() => useEngagementEvents());
    const deliver = vi.mocked(subscribeEvents).mock.calls[0][0];
    const epoch = renderHook(() => useEngagementEpoch());
    const read = () => epoch.result.current;
    const send = (type: string, data = "{}") => {
      deliver(new MessageEvent(type, { data }));
      epoch.rerender();
    };

    const start = read();
    send("client.invalidate", JSON.stringify({ payload: { reason: "library-changed" } }));
    send("playback.started");
    expect(read()).toBe(start);

    send("client.invalidate", JSON.stringify({ payload: { reason: "engagement-changed" } }));
    expect(read()).toBe(start + 1);

    // The cursor was too old to replay: a change may have been missed.
    send("client.fullRefresh");
    expect(read()).toBe(start + 2);
  });

  it("hands every frame of the one subscription to the extra handler", () => {
    vi.mocked(subscribeEvents).mockClear();
    const seen: string[] = [];
    renderHook(() => useEngagementEvents((event) => seen.push(event.type)));
    expect(subscribeEvents).toHaveBeenCalledOnce();
    const deliver = vi.mocked(subscribeEvents).mock.calls[0][0];
    deliver(new MessageEvent("session.control", { data: "{}" }));
    deliver(new MessageEvent("client.fullRefresh", { data: "{}" }));
    expect(seen).toEqual(["session.control", "client.fullRefresh"]);
  });

  it("surfaces reconnecting state and triggers manifest + engagement recovery on reconnect", () => {
    vi.mocked(subscribeEvents).mockClear();
    vi.mocked(requestManifestRefresh).mockClear();
    renderHook(() => useEngagementEvents());
    const onState = vi.mocked(subscribeEvents).mock.calls[0][2];
    const connState = renderHook(() => useServerConnectionState());
    const recoveryEpoch = renderHook(() => useServerRecoveryEpoch());
    const engagementEpoch = renderHook(() => useEngagementEpoch());
    const startEngagement = engagementEpoch.result.current;

    onState?.("reconnecting");
    connState.rerender();
    expect(connState.result.current).toBe("reconnecting");
    expect(recoveryEpoch.result.current).toBe(0);

    onState?.("live");
    connState.rerender();
    recoveryEpoch.rerender();
    engagementEpoch.rerender();
    expect(connState.result.current).toBe("live");
    expect(recoveryEpoch.result.current).toBe(1);
    expect(engagementEpoch.result.current).toBe(startEngagement + 1);
    expect(requestManifestRefresh).toHaveBeenCalledOnce();
  });
});
