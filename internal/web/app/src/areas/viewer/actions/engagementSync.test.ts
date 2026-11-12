import { cleanup, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { subscribeEvents } from "@/lib/api";
import { useEngagementEpoch, useEngagementEvents } from "./engagementSync";

vi.mock("@/lib/api", () => ({ subscribeEvents: vi.fn(() => () => {}) }));

afterEach(cleanup);

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
});
