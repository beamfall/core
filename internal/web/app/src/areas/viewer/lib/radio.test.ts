import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, graphql } from "@/lib/api";
import { activeRadio, startRadio, stopRadio, topUpRadio } from "./radio";

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return { ...actual, graphql: vi.fn() };
});

const candidates = (...ids: string[]) =>
  ids.map((id) => ({ mediaID: id, title: id, explanation: `Why ${id}` }));

afterEach(() => {
  vi.mocked(graphql).mockReset();
  stopRadio();
});

describe("radio", () => {
  it("starts with the previewed seed and remembers why each item was queued", async () => {
    vi.mocked(graphql)
      .mockResolvedValueOnce({
        stationGenerate: { candidates: candidates("a", "b"), explanation: "" },
      })
      .mockResolvedValueOnce({ stationStart: [] });

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
    expect(start).toMatchObject({
      seedKind: "entity",
      seedID: "e1",
      seed: preview?.seed,
      rulesJSON: '{"decades":[1990]}',
      confirmReplace: false,
    });
    expect(activeRadio("p1")?.reasons).toEqual({ a: "Why a", b: "Why b" });
    expect(activeRadio("p2")).toBeNull();
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

  it("tops up with items the queue does not hold yet", async () => {
    vi.mocked(graphql)
      .mockResolvedValueOnce({ stationGenerate: { candidates: candidates("a"), explanation: "" } })
      .mockResolvedValueOnce({ stationStart: [] });
    const started = await startRadio({ profileID: "p1", seedKind: "media", seedID: "m1" });
    if (started.status !== "started") throw new Error("not started");
    vi.mocked(graphql).mockReset();
    vi.mocked(graphql)
      .mockResolvedValueOnce({
        stationGenerate: { candidates: candidates("a", "b", "c", "d"), explanation: "" },
      })
      .mockResolvedValue({ queueAdd: [] });

    const added = await topUpRadio(started.station, new Set(["a", "b"]));

    expect(added).toBe(2);
    const adds = vi
      .mocked(graphql)
      .mock.calls.slice(1)
      .map(([, vars]) => vars?.mediaID);
    expect(adds).toEqual(["c", "d"]);
    expect(activeRadio("p1")?.reasons).toMatchObject({ c: "Why c", d: "Why d" });
  });
});
