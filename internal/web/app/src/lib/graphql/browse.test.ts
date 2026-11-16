import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchMediaRow, fetchMediaRows } from "./browse";

// Request-shape tests for the listening row reads (CORE-1635): the plain row
// read sends only media(id), and the positioned read adds the Media's stored
// fields under the same $id and keeps only core's generic ordinals.

interface Recorded {
  query: string;
  variables: Record<string, unknown>;
}

const recorded: Recorded[] = [];
let fields: { name: string; value: string }[] | null = [];

beforeEach(() => {
  recorded.length = 0;
  fields = [
    { name: "sequence", value: "2" },
    { name: "index", value: "7" },
    { name: "title", value: "Track Seven" },
  ];
  vi.stubGlobal(
    "fetch",
    vi.fn((_url: string, init?: RequestInit) => {
      const body = JSON.parse(init?.body as string) as Recorded;
      recorded.push(body);
      const media = {
        id: body.variables.id,
        title: "Track Seven",
        durationSeconds: 61,
        mediaClass: "audio",
        credits: [],
      };
      const data = body.query.includes("mediaFields") ? { media, mediaFields: fields } : { media };
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ data }),
      } as Response);
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("listening row reads", () => {
  it("reads only the row unless positions are asked for", async () => {
    const row = await fetchMediaRow("m-1");
    expect(recorded[0].query).not.toContain("mediaFields");
    expect(recorded[0].variables).toEqual({ id: "m-1" });
    expect(row).toEqual(expect.objectContaining({ id: "m-1", title: "Track Seven" }));
    expect(row).not.toHaveProperty("sequence");
  });

  it("reads the stored sequence and index with the row", async () => {
    const rows = await fetchMediaRows(["m-1"], undefined, { positions: true });
    expect(recorded[0].query).toContain("mediaFields(mediaID: $id) { name value }");
    expect(recorded[0].variables).toEqual({ id: "m-1" });
    expect(rows["m-1"]).toEqual(expect.objectContaining({ sequence: 2, index: 7 }));
  });

  it("leaves out positions that are missing, zero or not numbers", async () => {
    fields = [
      { name: "sequence", value: "0" },
      { name: "index", value: "side" },
    ];
    const row = await fetchMediaRow("m-1", undefined, { positions: true });
    expect(row).not.toHaveProperty("sequence");
    expect(row).not.toHaveProperty("index");
    fields = null;
    expect(await fetchMediaRow("m-1", undefined, { positions: true })).toEqual(
      expect.objectContaining({ id: "m-1" }),
    );
  });
});
