import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchSourceItems } from "@/lib/graphql";
import type { Section } from "@/lib/graphql/types";
import { fetchSectionItems } from "./sections";

vi.mock("@/lib/graphql", () => ({ fetchSourceItems: vi.fn().mockResolvedValue([]) }));

const section: Section = {
  id: "sec-recent",
  kind: "shelf",
  title: { en: "Recent" },
  artworkSlot: "poster",
  source: { kind: "recent" } as Section["source"],
};

afterEach(() => {
  vi.mocked(fetchSourceItems).mockClear();
});

// Home's aggregate Sections leave out Libraries a manager hid from Home; the
// flag rides the SourceItems variables only when Home asks for it (CORE-2360).
describe("fetchSectionItems Home scope", () => {
  it("sends home: true when hydrating Home", async () => {
    await fetchSectionItems(section, { libraryID: "", entityID: "", home: true });

    expect(fetchSourceItems).toHaveBeenCalledWith(
      expect.objectContaining({ kind: "recent", libraryID: "", home: true }),
      undefined,
    );
  });

  it("leaves the home variable out everywhere else", async () => {
    await fetchSectionItems(section, { libraryID: "lib-a", entityID: "" });
    await fetchSectionItems(section, { libraryID: "", entityID: "", home: false });

    for (const [vars] of vi.mocked(fetchSourceItems).mock.calls) {
      expect(vars).not.toHaveProperty("home");
    }
  });
});

// A Source's declared seed (random order) and value (resume-progress and
// availability bands) echo back on sourceItems; neither is sent when the
// Source declares neither (CORE-0203).
describe("fetchSectionItems Source seed and value", () => {
  it("sends the declared seed and value", async () => {
    await fetchSectionItems(
      { ...section, source: { kind: "random", seed: "s1" } },
      { libraryID: "lib-a", entityID: "" },
    );
    await fetchSectionItems(
      { ...section, source: { kind: "resume-progress", value: "started" } },
      { libraryID: "lib-a", entityID: "" },
    );

    const [[random], [banded]] = vi.mocked(fetchSourceItems).mock.calls;
    expect(random).toMatchObject({ kind: "random", seed: "s1" });
    expect(random).not.toHaveProperty("value");
    expect(banded).toMatchObject({ kind: "resume-progress", value: "started" });
    expect(banded).not.toHaveProperty("seed");
  });

  it("sends neither when the Source declares neither", async () => {
    await fetchSectionItems(section, { libraryID: "lib-a", entityID: "" });

    const [[vars]] = vi.mocked(fetchSourceItems).mock.calls;
    expect(vars).not.toHaveProperty("seed");
    expect(vars).not.toHaveProperty("value");
  });
});
