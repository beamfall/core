import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchClientPrefs, setClientPref } from "@/lib/graphql/viewer";
import {
  applySidebarPrefs,
  EMPTY_SIDEBAR_PREFS,
  parseSidebarPrefs,
  resetSidebarPrefsCache,
  SIDEBAR_LIBRARIES_PREF_KEY,
  serializeSidebarPrefs,
  useSidebarPrefs,
} from "./sidebarPrefs";

vi.mock("@/lib/graphql/viewer", () => ({
  fetchClientPrefs: vi.fn(),
  setClientPref: vi.fn(),
}));

afterEach(() => {
  resetSidebarPrefsCache();
  vi.mocked(fetchClientPrefs).mockReset();
  vi.mocked(setClientPref).mockReset();
});

const id = (value: string) => value;

describe("parseSidebarPrefs", () => {
  it("reads order and hidden, dropping duplicates and non-strings", () => {
    expect(parseSidebarPrefs('{"order":["b","a","b",3,""],"hidden":["c"]}')).toEqual({
      order: ["b", "a"],
      hidden: ["c"],
    });
  });

  it.each([undefined, "", "not json", "[]", "null", '{"order":"a"}'])(
    "reads %j as no choice",
    (raw) => {
      expect(parseSidebarPrefs(raw)).toEqual(EMPTY_SIDEBAR_PREFS);
    },
  );
});

describe("serializeSidebarPrefs", () => {
  it("round-trips a choice and clears an empty one", () => {
    const prefs = { order: ["b", "a"], hidden: ["a"] };
    expect(parseSidebarPrefs(serializeSidebarPrefs(prefs))).toEqual(prefs);
    expect(serializeSidebarPrefs(EMPTY_SIDEBAR_PREFS)).toBe("");
  });
});

describe("applySidebarPrefs", () => {
  it("keeps server order without a choice", () => {
    expect(applySidebarPrefs(["a", "b", "c"], id, EMPTY_SIDEBAR_PREFS)).toEqual({
      pinned: ["a", "b", "c"],
      unpinned: [],
    });
  });

  it("orders named libraries first and the rest after them in server order", () => {
    expect(applySidebarPrefs(["a", "b", "c", "d"], id, { order: ["c", "a"], hidden: [] })).toEqual({
      pinned: ["c", "a", "b", "d"],
      unpinned: [],
    });
  });

  it("moves unpinned libraries under More, keeping their order", () => {
    expect(
      applySidebarPrefs(["a", "b", "c"], id, { order: ["c", "b", "a"], hidden: ["b", "a"] }),
    ).toEqual({ pinned: ["c"], unpinned: ["b", "a"] });
  });

  it("ignores libraries the server no longer lists, so a server-wide hide wins", () => {
    expect(applySidebarPrefs(["a", "c"], id, { order: ["b", "c", "a"], hidden: ["b"] })).toEqual({
      pinned: ["c", "a"],
      unpinned: [],
    });
  });
});

describe("useSidebarPrefs", () => {
  it("loads the Profile's choice once and reuses it across remounts", async () => {
    vi.mocked(fetchClientPrefs).mockResolvedValue([
      { key: SIDEBAR_LIBRARIES_PREF_KEY, value: '{"order":["b"],"hidden":["a"]}' },
    ]);
    const first = renderHook(() => useSidebarPrefs("p1"));
    await waitFor(() => expect(first.result.current[0]).toEqual({ order: ["b"], hidden: ["a"] }));
    first.unmount();
    const second = renderHook(() => useSidebarPrefs("p1"));
    expect(second.result.current[0]).toEqual({ order: ["b"], hidden: ["a"] });
    expect(fetchClientPrefs).toHaveBeenCalledTimes(1);
  });

  it("saves under the Profile key and rolls back when the save fails", async () => {
    vi.mocked(fetchClientPrefs).mockResolvedValue([]);
    vi.mocked(setClientPref).mockResolvedValueOnce({ key: SIDEBAR_LIBRARIES_PREF_KEY });
    const { result } = renderHook(() => useSidebarPrefs("p1"));
    await act(() => result.current[1]({ order: ["c"], hidden: [] }));
    expect(setClientPref).toHaveBeenCalledWith(
      SIDEBAR_LIBRARIES_PREF_KEY,
      '{"order":["c"],"hidden":[]}',
    );
    expect(result.current[0]).toEqual({ order: ["c"], hidden: [] });

    vi.mocked(setClientPref).mockRejectedValueOnce(new Error("offline"));
    await act(async () => {
      await expect(result.current[1]({ order: ["a"], hidden: [] })).rejects.toThrow("offline");
    });
    expect(result.current[0]).toEqual({ order: ["c"], hidden: [] });
  });

  it("does nothing without a Profile", async () => {
    const { result } = renderHook(() => useSidebarPrefs(null));
    await act(() => result.current[1]({ order: ["a"], hidden: [] }));
    expect(fetchClientPrefs).not.toHaveBeenCalled();
    expect(setClientPref).not.toHaveBeenCalled();
    expect(result.current[0]).toBe(EMPTY_SIDEBAR_PREFS);
  });
});
