import { cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { EntityInfo, SourceItem } from "@/lib/graphql/types";

const fetchMediaRows = vi.hoisted(() => vi.fn());
vi.mock("@/lib/graphql/browse", () => ({ fetchMediaRows }));

const { useListingRows } = await import("./useListingRows");

const person = { id: "person-a", title: "Test Artist", archetype: "person" } as EntityInfo;
const audio = (id: string): SourceItem => ({
  id,
  kind: "media",
  title: id,
  mediaClass: "audio",
  byline: ["Test Artist"],
});

afterEach(() => {
  cleanup();
  fetchMediaRows.mockReset();
});

describe("useListingRows (CORE-2922)", () => {
  it("builds every audio row from the listing without a media(id) read", () => {
    const items = [audio("t1"), audio("t2")];
    const { result } = renderHook(() => useListingRows(person, items));
    expect(fetchMediaRows).not.toHaveBeenCalled();
    expect(result.current.wanted).toBe(true);
    expect(Object.keys(result.current.rows ?? {})).toEqual(["t1", "t2"]);
  });

  it("reads only the Media the listing cannot row, and waits for them", async () => {
    const unknown: SourceItem = { id: "t2", kind: "media", title: "t2" };
    fetchMediaRows.mockResolvedValue({ t2: { id: "t2", title: "t2", favorited: false } });
    const items = [audio("t1"), unknown];
    const { result } = renderHook(() => useListingRows(person, items));
    expect(result.current.rows).toBeNull();
    await waitFor(() => expect(result.current.rows).not.toBeNull());
    expect(fetchMediaRows).toHaveBeenCalledTimes(1);
    expect(fetchMediaRows.mock.calls[0][0]).toEqual(["t2"]);
    expect(Object.keys(result.current.rows ?? {}).sort()).toEqual(["t1", "t2"]);
  });
});
