import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setIdentityEpochSignal } from "@/lib/api";
import { fetchClientPrefs, setClientPref } from "@/lib/graphql/viewer";
import {
  applySidebarPrefs,
  EMPTY_SIDEBAR_PREFS,
  PERSONAL_ENTRIES,
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
  setIdentityEpochSignal(undefined);
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

describe("Your stuff choice (CORE-2827)", () => {
  // A value written before the group existed, exactly as CORE-2435 stored it.
  const stored = '{"order":["lib-b","lib-a"],"hidden":["lib-c"]}';

  it("still parses a value stored before the group existed", () => {
    const prefs = parseSidebarPrefs(stored);
    expect(prefs).toEqual({ order: ["lib-b", "lib-a"], hidden: ["lib-c"] });
    expect(prefs.personal).toBeUndefined();
    // The group then shows every entry, in default order.
    const { pinned, unpinned } = applySidebarPrefs(
      [...PERSONAL_ENTRIES],
      (entry) => entry.id,
      prefs.personal ?? EMPTY_SIDEBAR_PREFS,
    );
    expect(pinned.map((entry) => entry.id)).toEqual(["playlists", "favorites", "history"]);
    expect(unpinned).toEqual([]);
  });

  it("serializes a choice that leaves the group alone exactly as before", () => {
    expect(serializeSidebarPrefs(parseSidebarPrefs(stored))).toBe(stored);
    expect(
      serializeSidebarPrefs({ ...parseSidebarPrefs(stored), personal: { order: [], hidden: [] } }),
    ).toBe(stored);
  });

  it("round-trips the group's order and hidden entries beside the library choice", () => {
    const prefs = {
      order: [],
      hidden: [],
      personal: { order: ["history", "playlists"], hidden: ["favorites"] },
    };
    const raw = serializeSidebarPrefs(prefs);
    expect(raw).not.toBe("");
    expect(parseSidebarPrefs(raw)).toEqual(prefs);
  });

  it.each(['{"personal":"x"}', '{"personal":null}', '{"personal":{"order":[1]}}'])(
    "reads a malformed group choice %j as none",
    (raw) => {
      expect(parseSidebarPrefs(raw)).toEqual(EMPTY_SIDEBAR_PREFS);
    },
  );
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

  it("holds editing after a failed read and retries it on the next mount", async () => {
    vi.mocked(fetchClientPrefs)
      .mockRejectedValueOnce(new DOMException("aborted", "AbortError"))
      .mockResolvedValueOnce([
        { key: SIDEBAR_LIBRARIES_PREF_KEY, value: '{"order":["b"],"hidden":[]}' },
      ]);
    const first = renderHook(() => useSidebarPrefs("p1"));
    await waitFor(() => expect(fetchClientPrefs).toHaveBeenCalledTimes(1));
    await act(async () => {});
    // Never read, so not editable: Done would overwrite the saved choice.
    expect(first.result.current[2]).toBe(false);
    expect(first.result.current[0]).toBe(EMPTY_SIDEBAR_PREFS);
    first.unmount();
    const second = renderHook(() => useSidebarPrefs("p1"));
    await waitFor(() => expect(second.result.current[0]).toEqual({ order: ["b"], hidden: [] }));
    expect(second.result.current[2]).toBe(true);
    expect(fetchClientPrefs).toHaveBeenCalledTimes(2);
  });

  it("writes saves in order across remounts, so the server ends on the last", async () => {
    vi.mocked(fetchClientPrefs).mockResolvedValue([]);
    let finishA: () => void = () => {};
    vi.mocked(setClientPref)
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            finishA = () => resolve({ key: SIDEBAR_LIBRARIES_PREF_KEY });
          }),
      )
      .mockResolvedValueOnce({ key: SIDEBAR_LIBRARIES_PREF_KEY });
    const first = renderHook(() => useSidebarPrefs("p1"));
    await waitFor(() => expect(first.result.current[2]).toBe(true));
    let saveA: Promise<void> = Promise.resolve();
    act(() => {
      saveA = first.result.current[1]({ order: ["a"], hidden: [] });
    });
    first.unmount();
    const second = renderHook(() => useSidebarPrefs("p1"));
    let saveB: Promise<void> = Promise.resolve();
    act(() => {
      saveB = second.result.current[1]({ order: ["b"], hidden: [] });
    });
    await act(async () => {});
    // B waits for A instead of landing first and being overwritten by it.
    expect(setClientPref).toHaveBeenCalledTimes(1);
    await act(async () => {
      finishA();
      await saveA;
      await saveB;
    });
    expect(vi.mocked(setClientPref).mock.calls.map((call) => call[1])).toEqual([
      '{"order":["a"],"hidden":[]}',
      '{"order":["b"],"hidden":[]}',
    ]);
    expect(second.result.current[0]).toEqual({ order: ["b"], hidden: [] });
  });

  it("lets a failed read retry after a failed save rolls back", async () => {
    vi.mocked(fetchClientPrefs)
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce([
        { key: SIDEBAR_LIBRARIES_PREF_KEY, value: '{"order":["c"],"hidden":[]}' },
      ]);
    vi.mocked(setClientPref).mockRejectedValueOnce(new Error("offline"));
    const first = renderHook(() => useSidebarPrefs("p1"));
    await waitFor(() => expect(fetchClientPrefs).toHaveBeenCalledTimes(1));
    await act(async () => {
      await expect(first.result.current[1]({ order: ["a"], hidden: [] })).rejects.toThrow(
        "offline",
      );
    });
    expect(first.result.current[0]).toBe(EMPTY_SIDEBAR_PREFS);
    first.unmount();
    const second = renderHook(() => useSidebarPrefs("p1"));
    await waitFor(() => expect(second.result.current[0]).toEqual({ order: ["c"], hidden: [] }));
    expect(second.result.current[2]).toBe(true);
    expect(fetchClientPrefs).toHaveBeenCalledTimes(2);
  });

  it("drops a queued save once the identity changes instead of writing to the next Profile", async () => {
    const epochA = new AbortController();
    setIdentityEpochSignal(epochA.signal);
    vi.mocked(fetchClientPrefs).mockResolvedValue([]);
    vi.mocked(setClientPref).mockImplementationOnce(
      () =>
        new Promise((_resolve, reject) => {
          epochA.signal.addEventListener("abort", () =>
            reject(new DOMException("aborted", "AbortError")),
          );
        }),
    );
    const first = renderHook(() => useSidebarPrefs("pA"));
    await waitFor(() => expect(first.result.current[2]).toBe(true));
    let saveA: Promise<void> = Promise.resolve();
    await act(async () => {
      saveA = first.result.current[1]({ order: ["a"], hidden: [] });
    });
    // A's request is out; B will queue behind it.
    expect(setClientPref).toHaveBeenCalledTimes(1);
    first.unmount();
    const second = renderHook(() => useSidebarPrefs("pA"));
    let saveB: Promise<void> = Promise.resolve();
    act(() => {
      saveB = second.result.current[1]({ order: ["b"], hidden: [] });
    });
    second.unmount();
    // Profile switch: the spine aborts epoch A and starts epoch B.
    epochA.abort();
    setIdentityEpochSignal(new AbortController().signal);
    renderHook(() => useSidebarPrefs("pB"));
    await act(async () => {
      await expect(saveA).rejects.toThrow("aborted");
      await expect(saveB).rejects.toThrow("Profile changed");
    });
    // Only A's original request went out; B never ran under Profile B.
    expect(setClientPref).toHaveBeenCalledTimes(1);
  });

  it("does nothing without a Profile", async () => {
    const { result } = renderHook(() => useSidebarPrefs(null));
    await act(() => result.current[1]({ order: ["a"], hidden: [] }));
    expect(fetchClientPrefs).not.toHaveBeenCalled();
    expect(setClientPref).not.toHaveBeenCalled();
    expect(result.current[0]).toBe(EMPTY_SIDEBAR_PREFS);
  });
});
