import { cleanup, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { subscribeEvents } from "@/lib/api";
import { requestManifestRefresh, requestSessionRefresh } from "@/lib/manifestRefresh";
import {
  resetServerConnectionForTest,
  useServerConnectionState,
  useServerRecoveryEpoch,
} from "@/lib/serverConnection";
import { useEngagementEpoch, useEngagementEvents } from "./engagementSync";

const apiState = vi.hoisted(() => ({
  epochController: undefined as AbortController | undefined,
  expectedProfile: undefined as string | undefined,
}));

vi.mock("@/lib/api", () => ({
  subscribeEvents: vi.fn(() => () => {}),
  getIdentityEpochSignal: () => apiState.epochController?.signal,
  getExpectedProfile: () => apiState.expectedProfile,
}));
vi.mock("@/lib/manifestRefresh", () => ({
  requestManifestRefresh: vi.fn(),
  requestSessionRefresh: vi.fn(),
}));

afterEach(() => {
  cleanup();
  resetServerConnectionForTest();
  apiState.epochController = undefined;
  apiState.expectedProfile = undefined;
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

    const onAuthError = vi.mocked(subscribeEvents).mock.calls[0][3];
    onAuthError?.(401, {});
    connState.rerender();
    expect(connState.result.current).toBe("reconnecting");
    expect(requestSessionRefresh).toHaveBeenCalledOnce();
  });

  it("ignores stale stream callbacks after identity epoch aborts or Profile changes", () => {
    vi.mocked(subscribeEvents).mockClear();
    vi.mocked(requestManifestRefresh).mockClear();
    vi.mocked(requestSessionRefresh).mockClear();
    const unsubscribe = vi.fn();
    vi.mocked(subscribeEvents).mockImplementation(() => unsubscribe);

    const p1Epoch = new AbortController();
    apiState.epochController = p1Epoch;
    apiState.expectedProfile = "p1";

    const view = renderHook(
      ({ profileID }: { profileID?: string }) => useEngagementEvents(undefined, profileID),
      { initialProps: { profileID: "p1" } },
    );
    const [deliver, , onState, onAuthError] = vi.mocked(subscribeEvents).mock.calls[0]!;
    const engagementEpoch = renderHook(() => useEngagementEpoch());
    const connState = renderHook(() => useServerConnectionState());
    const startEpoch = engagementEpoch.result.current;

    // Move to a new identity epoch for p2 BEFORE delivering stale p1 callbacks.
    p1Epoch.abort();
    apiState.epochController = new AbortController();
    apiState.expectedProfile = "p2";

    deliver(new MessageEvent("client.fullRefresh"));
    onState?.("reconnecting");
    onState?.("live");
    onAuthError?.(401, {});

    engagementEpoch.rerender();
    connState.rerender();
    expect(engagementEpoch.result.current).toBe(startEpoch);
    expect(connState.result.current).toBe("live");
    expect(requestManifestRefresh).not.toHaveBeenCalled();
    expect(requestSessionRefresh).not.toHaveBeenCalled();

    // Changing profileID tears down the previous subscription and opens a fresh one.
    view.rerender({ profileID: "p2" });
    expect(unsubscribe).toHaveBeenCalledOnce();
    expect(vi.mocked(subscribeEvents)).toHaveBeenCalledTimes(2);
  });
});
