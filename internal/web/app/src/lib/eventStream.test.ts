import { afterEach, describe, expect, it, vi } from "vitest";
import { createEventStreamParser, openEventStream } from "./eventStream";

function streamResponse(chunks: string[], status = 200): Response {
  const encoder = new TextEncoder();
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk));
      controller.close();
    },
  });
  return new Response(body, { status, headers: { "content-type": "text/event-stream" } });
}

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("createEventStreamParser", () => {
  it("delivers named frames, multi-line data and frames split across chunks", () => {
    const frames: { type: string; data: string; lastEventId: string }[] = [];
    const feed = createEventStreamParser((frame) => frames.push(frame));
    feed(": keepalive\n\nid: 7\nevent: client.inval");
    feed('idate\ndata: {"a":1}\r');
    feed("\n\r\ndata: one\ndata: two\n\nevent: ignored-without-data\n\n");
    expect(frames).toEqual([
      { type: "client.invalidate", data: '{"a":1}', lastEventId: "7" },
      { type: "message", data: "one\ntwo", lastEventId: "7" },
    ]);
  });
});

describe("openEventStream", () => {
  it("delivers a named event that EventSource.onmessage would never see", async () => {
    const fetchMock = vi.fn(async (_url: string, _init: RequestInit) =>
      streamResponse(["id: 3\nevent: scan.finished\ndata: {}\n\n"]),
    );
    vi.stubGlobal("fetch", fetchMock);
    const events: MessageEvent[] = [];
    const states: string[] = [];
    const close = openEventStream("/api/events", {
      onEvent: (event) => events.push(event),
      onState: (state) => states.push(state),
    });
    await vi.waitFor(() => expect(states).toContain("reconnecting"));
    close();
    expect(events.map((event) => [event.type, event.data, event.lastEventId])).toEqual([
      ["scan.finished", "{}", "3"],
    ]);
    expect(states.slice(0, 2)).toEqual(["live", "reconnecting"]);
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ credentials: "include" });
  });

  it("reconnects from the last event id and stops once closed", async () => {
    vi.useFakeTimers();
    const fetchMock = vi
      .fn<(url: string, init: RequestInit) => Promise<Response>>()
      .mockResolvedValueOnce(streamResponse(["id: 9\nevent: a\ndata: 1\n\n"]))
      .mockResolvedValue(streamResponse(["id: 10\nevent: b\ndata: 2\n\n"]));
    vi.stubGlobal("fetch", fetchMock);
    const kinds: string[] = [];
    const errors: Event[] = [];
    const close = openEventStream("/api/events", {
      onEvent: (event) => kinds.push(event.type),
      onError: (err) => errors.push(err),
    });
    await vi.advanceTimersByTimeAsync(1000);
    expect(kinds).toEqual(["a", "b"]);
    expect(errors.length).toBeGreaterThan(0);
    expect((fetchMock.mock.calls[1]?.[1].headers as Record<string, string>)["Last-Event-ID"]).toBe(
      "9",
    );
    close();
    const calls = fetchMock.mock.calls.length;
    await vi.advanceTimersByTimeAsync(120000);
    expect(fetchMock.mock.calls.length).toBe(calls);
  });

  it("leaves no abort listener behind after each retry wait", async () => {
    vi.useFakeTimers();
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("no", { status: 503 })),
    );
    const added = vi.spyOn(AbortSignal.prototype, "addEventListener");
    const removed = vi.spyOn(AbortSignal.prototype, "removeEventListener");
    const close = openEventStream("/api/events", { onEvent: () => {} });
    await vi.advanceTimersByTimeAsync(7500);
    const waits = added.mock.calls.filter(([kind]) => kind === "abort").length;
    const dropped = removed.mock.calls.filter(([kind]) => kind === "abort").length;
    expect(waits).toBeGreaterThanOrEqual(3);
    // Only the wait still in flight may hold a listener.
    expect(waits - dropped).toBeLessThanOrEqual(1);
    close();
    added.mockRestore();
    removed.mockRestore();
  });

  it("treats a non-stream response as a failed connection", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response("{}", { status: 200, headers: { "content-type": "application/json" } }),
      ),
    );
    const states: string[] = [];
    const onEvent = vi.fn();
    const close = openEventStream("/api/events", {
      onEvent,
      onState: (state) => states.push(state),
    });
    await vi.waitFor(() => expect(states).toEqual(["reconnecting"]));
    close();
    expect(onEvent).not.toHaveBeenCalled();
  });
});
