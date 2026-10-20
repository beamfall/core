import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchDuplicateGroups, fetchHealthDrilldown, runHealthBulkOpSelected } from "./manage";

interface Recorded {
  query: string;
  variables: Record<string, unknown>;
}

const recorded: Recorded[] = [];

beforeEach(() => {
  recorded.length = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn((_url: string, init?: RequestInit) => {
      const body = init?.body
        ? (JSON.parse(init.body as string) as Recorded)
        : { query: "", variables: {} };
      recorded.push(body);

      let data: Record<string, unknown>;
      if (body.query.includes("libraryDuplicateGroups")) {
        data = {
          libraryDuplicateGroups: [
            {
              items: [
                { mediaID: "media-1", libraryID: "library-local", title: "Keep" },
                { mediaID: "media-2", libraryID: "library-local", title: "Merge" },
              ],
            },
          ],
        };
      } else if (body.query.includes("bulkMergeDuplicatesSelected")) {
        data = { bulkMergeDuplicatesSelected: { affected: 1 } };
      } else {
        data = {
          libraryHealthItems: [
            { mediaID: "media-1", libraryID: "library-local", title: "Needs work" },
          ],
        };
      }

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

describe("fetchHealthDrilldown", () => {
  it("sends the per-Library dimension query with the server wire dimension", async () => {
    const rows = await fetchHealthDrilldown("library-local", "missing-artwork");

    expect(rows).toEqual([{ mediaID: "media-1", libraryID: "library-local", title: "Needs work" }]);
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("libraryHealthItems");
    expect(recorded[0].query).toContain("$dimension");
    expect(recorded[0].variables).toEqual({
      libraryID: "library-local",
      dimension: "missing-artwork",
      limit: 500,
    });
  });
});

describe("fetchDuplicateGroups", () => {
  it("sends the duplicate-group query for the selected Library", async () => {
    const groups = await fetchDuplicateGroups("library-local");

    expect(groups[0].items).toHaveLength(2);
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("libraryDuplicateGroups");
    expect(recorded[0].variables).toEqual({ libraryID: "library-local" });
  });
});

describe("runHealthBulkOpSelected", () => {
  it("suffixes the selected mutation and forwards the checked Media IDs", async () => {
    const result = await runHealthBulkOpSelected("bulkMergeDuplicates", "library-local", [
      "media-2",
    ]);

    expect(result).toEqual({ affected: 1 });
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("bulkMergeDuplicatesSelected");
    expect(recorded[0].query).toContain("$mediaIDs");
    expect(recorded[0].variables).toEqual({
      libraryID: "library-local",
      mediaIDs: ["media-2"],
    });
  });
});
