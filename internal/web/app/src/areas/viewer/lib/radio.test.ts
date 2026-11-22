import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, graphql, setIdentityEpochSignal } from "@/lib/api";
import { activeRadio, startRadio, stopRadio, topUpRadio } from "./radio";

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return { ...actual, graphql: vi.fn() };
});

const candidates = (...ids: string[]) =>
  ids.map((id) => ({ mediaID: id, title: id, explanation: `Why ${id}` }));

const queued = (...ids: string[]) => ids.map((id) => ({ id }));

let epoch: AbortController;

beforeEach(() => {
  epoch = new AbortController();
  setIdentityEpochSignal(epoch.signal);
});

afterEach(() => {
  vi.mocked(graphql).mockReset();
  stopRadio();
  setIdentityEpochSignal(undefined);
});

async function started(...ids: string[]) {
  vi.mocked(graphql)
    .mockResolvedValueOnce({ stationGenerate: { candidates: candidates(...ids), explanation: "" } })
    .mockResolvedValueOnce({ stationStart: queued(...ids) });
  const result = await startRadio({ profileID: "p1", seedKind: "media", seedID: "m1" });
  if (result.status !== "started") throw new Error("not started");
  vi.mocked(graphql).mockReset();
  return result.station;
}

describe("radio", () => {
  it("starts with the previewed seed, bound to the starting Profile", async () => {
    vi.mocked(graphql)
      .mockResolvedValueOnce({
        stationGenerate: { candidates: candidates("a", "b"), explanation: "" },
      })
      .mockResolvedValueOnce({ stationStart: queued("a", "b") });

    const result = await startRadio({
      profileID: "p1",
      seedKind: "entity",
      seedID: "e1",
      rules: { decades: [1990] },
    });

    expect(result).toMatchObject({ status: "started", firstMediaID: "a" });
    const [, preview] = vi.mocked(graphql).mock.calls[0];
    const [startQuery, start] = vi.mocked(graphql).mock.calls[1];
    expect(startQuery).toContain("stationStart");
    expect(startQuery).toContain("expectedProfileID");
    expect(start).toMatchObject({
      seedKind: "entity",
      seedID: "e1",
      seed: preview?.seed,
      rulesJSON: '{"decades":[1990]}',
      confirmReplace: false,
      expectedProfileID: "p1",
    });
    expect(activeRadio("p1")?.reasons).toEqual({ a: "Why a", b: "Why b" });
    expect(activeRadio("p2")).toBeNull();
  });

  // Review finding P2: play what the server queued, not the preview.
  it("plays and explains only the Media the server queued", async () => {
    vi.mocked(graphql)
      .mockResolvedValueOnce({
        stationGenerate: { candidates: candidates("gone", "b", "c"), explanation: "" },
      })
      .mockResolvedValueOnce({ stationStart: queued("b", "c") });

    const result = await startRadio({ profileID: "p1", seedKind: "entity", seedID: "e1" });

    expect(result).toMatchObject({ status: "started", firstMediaID: "b" });
    expect(activeRadio("p1")?.reasons).toEqual({ b: "Why b", c: "Why c" });
  });

  it("asks before replacing a queue that holds the listener's own items", async () => {
    vi.mocked(graphql)
      .mockResolvedValueOnce({ stationGenerate: { candidates: candidates("a"), explanation: "" } })
      .mockRejectedValueOnce(
        new ApiError("queue has items; pass confirmReplace to replace it", 400),
      );

    const result = await startRadio({ profileID: "p1", seedKind: "media", seedID: "m1" });

    expect(result).toEqual({ status: "needs-confirm" });
    expect(activeRadio("p1")).toBeNull();
  });

  it("reports an empty Station without touching the queue", async () => {
    vi.mocked(graphql).mockResolvedValueOnce({
      stationGenerate: { candidates: [], explanation: "Nothing matched." },
    });

    const result = await startRadio({ profileID: "p1", seedKind: "media", seedID: "m1" });

    expect(result).toEqual({ status: "empty", message: "Nothing matched." });
    expect(graphql).toHaveBeenCalledTimes(1);
  });

  // Review finding P1: titles and reasons never reach storage, and a relock
  // forgets the Station.
  it("keeps the Station in memory and drops it when the identity epoch ends", async () => {
    const setItem = vi.spyOn(Storage.prototype, "setItem");
    await started("a");
    expect(activeRadio("p1")).not.toBeNull();
    expect(setItem).not.toHaveBeenCalled();
    setItem.mockRestore();

    epoch.abort();

    expect(activeRadio("p1")).toBeNull();
  });

  it("tops up with unqueued items, excluding the queue on the server and naming the Profile", async () => {
    const station = await started("a");
    vi.mocked(graphql)
      .mockResolvedValueOnce({
        stationGenerate: { candidates: candidates("a", "b", "c", "d"), explanation: "" },
      })
      .mockResolvedValue({ queueAdd: [] });

    const added = await topUpRadio(station, new Set(["a", "b"]));

    expect(added).toBe(2);
    const [, generateVars] = vi.mocked(graphql).mock.calls[0];
    expect(JSON.parse(String(generateVars?.rulesJSON))).toEqual({ excludeQueued: true });
    const adds = vi.mocked(graphql).mock.calls.slice(1);
    expect(adds.map(([, vars]) => vars?.mediaID)).toEqual(["c", "d"]);
    expect(adds.every(([, vars]) => vars?.expectedProfileID === "p1")).toBe(true);
    expect(adds[0][0]).toContain("expectedProfileID");
    expect(activeRadio("p1")?.reasons).toMatchObject({ c: "Why c", d: "Why d" });
  });

  // Review finding P2: a stop while a top-up generates writes nothing.
  it("stops an in-flight top-up before its next queue write", async () => {
    const station = await started("a");
    let release: (value: unknown) => void = () => {};
    vi.mocked(graphql)
      .mockReturnValueOnce(new Promise((resolve) => (release = resolve)))
      .mockResolvedValue({ queueAdd: [] });

    const pending = topUpRadio(station, new Set(["a"]));
    stopRadio();
    release({ stationGenerate: { candidates: candidates("b", "c"), explanation: "" } });

    expect(await pending).toBe(0);
    expect(graphql).toHaveBeenCalledTimes(1);
  });

  it("stops an in-flight top-up when a new Station starts", async () => {
    const station = await started("a");
    let release: (value: unknown) => void = () => {};
    vi.mocked(graphql).mockReturnValueOnce(new Promise((resolve) => (release = resolve)));
    const pending = topUpRadio(station, new Set(["a"]));
    vi.mocked(graphql)
      .mockResolvedValueOnce({ stationGenerate: { candidates: candidates("x"), explanation: "" } })
      .mockResolvedValueOnce({ stationStart: queued("x") });
    await startRadio({ profileID: "p1", seedKind: "media", seedID: "m2", confirmReplace: true });
    vi.mocked(graphql).mockResolvedValue({ queueAdd: [] });

    release({ stationGenerate: { candidates: candidates("b"), explanation: "" } });

    expect(await pending).toBe(0);
    const writes = vi.mocked(graphql).mock.calls.filter(([query]) => query.includes("queueAdd"));
    expect(writes).toHaveLength(0);
    expect(activeRadio("p1")?.seedID).toBe("m2");
  });

  // Review round 2, P1: a start overtaken by a relock or Profile switch
  // activates nothing, so its titles never reach the next identity.
  it("cancels a start whose identity epoch ended before the reply", async () => {
    let release: (value: unknown) => void = () => {};
    vi.mocked(graphql)
      .mockResolvedValueOnce({ stationGenerate: { candidates: candidates("a"), explanation: "" } })
      .mockReturnValueOnce(new Promise((resolve) => (release = resolve)));
    const pending = startRadio({ profileID: "p1", seedKind: "media", seedID: "m1" });
    await vi.waitFor(() => expect(graphql).toHaveBeenCalledTimes(2));

    epoch.abort();
    const next = new AbortController();
    setIdentityEpochSignal(next.signal);
    release({ stationStart: queued("a") });

    expect(await pending).toEqual({ status: "cancelled" });
    expect(activeRadio("p1")).toBeNull();
  });

  // Review round 2, P2: Stop while a start waits keeps radio stopped, and a
  // start ends the playing Station at once.
  it("keeps radio stopped when Stop lands while a start waits", async () => {
    const playing = await started("a");
    let release: (value: unknown) => void = () => {};
    vi.mocked(graphql)
      .mockResolvedValueOnce({ stationGenerate: { candidates: candidates("x"), explanation: "" } })
      .mockReturnValueOnce(new Promise((resolve) => (release = resolve)));
    const pending = startRadio({ profileID: "p1", seedKind: "media", seedID: "m2" });
    expect(activeRadio("p1")).toBeNull();
    expect(await topUpRadio(playing, new Set(["a"]))).toBe(0);
    await vi.waitFor(() => expect(graphql).toHaveBeenCalledTimes(2));

    stopRadio();
    release({ stationStart: queued("x") });

    expect(await pending).toEqual({ status: "cancelled" });
    expect(activeRadio("p1")).toBeNull();
  });

  // Review round 2, P2: what earlier builds kept in sessionStorage is cleared.
  it("clears the Station an earlier build left in sessionStorage", async () => {
    sessionStorage.setItem("beamfall.radio", '{"reasons":{"a":"Secret title"}}');
    vi.resetModules();
    await import("./radio");
    expect(sessionStorage.getItem("beamfall.radio")).toBeNull();
  });
});
