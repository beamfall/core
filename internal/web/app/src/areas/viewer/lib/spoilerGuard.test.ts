import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setIdentityEpochSignal } from "@/lib/api";
import { fetchClientPrefs, setClientPref } from "@/lib/graphql";
import type { SourceItem } from "@/lib/graphql/types";
import {
  applySpoilerPref,
  containerPrefKey,
  isSpoilerCandidate,
  resetSpoilerGuardForTest,
  revealItem,
  setContainerRevealed,
  sharedGuardContainer,
  toggleItemReveal,
  useSpoilerGuard,
} from "./spoilerGuard";

const mocks = vi.hoisted(() => ({ identity: "p1" as string | null }));
vi.mock("@/lib/graphql", () => ({ fetchClientPrefs: vi.fn(), setClientPref: vi.fn() }));
vi.mock("@/lib/session", () => ({ useViewerIdentity: () => mocks.identity }));

const unplayed: SourceItem = { id: "e1", kind: "media", title: "Pilot", mediaClass: "video" };

beforeEach(() => {
  mocks.identity = "p1";
  vi.mocked(fetchClientPrefs).mockReset().mockResolvedValue([]);
  vi.mocked(setClientPref)
    .mockReset()
    .mockResolvedValue(undefined as never);
});

afterEach(() => {
  cleanup();
  resetSpoilerGuardForTest();
});

describe("isSpoilerCandidate (CORE-3057)", () => {
  it("guards only unplayed video Media inside a container", () => {
    expect(isSpoilerCandidate(unplayed, "s1")).toBe(true);
    expect(isSpoilerCandidate(unplayed)).toBe(false);
    expect(isSpoilerCandidate({ ...unplayed, mediaClass: "audio" }, "s1")).toBe(false);
    expect(isSpoilerCandidate({ ...unplayed, kind: "entity" }, "s1")).toBe(false);
    expect(isSpoilerCandidate({ ...unplayed, engagement: { completed: true } }, "s1")).toBe(false);
    expect(isSpoilerCandidate({ ...unplayed, engagement: { positionSeconds: 30 } }, "s1")).toBe(
      false,
    );
  });
});

describe("useSpoilerGuard (CORE-3057)", () => {
  async function loaded() {
    const hook = renderHook(() => useSpoilerGuard());
    await waitFor(() => expect(hook.result.current.guardOn).not.toBeNull());
    return hook;
  }

  it("hides candidates before and after prefs load, and never outside a container", async () => {
    const pending = new Promise<never>(() => {});
    vi.mocked(fetchClientPrefs).mockReturnValueOnce(pending);
    const { result } = renderHook(() => useSpoilerGuard());
    expect(result.current.guardOn).toBeNull();
    expect(result.current.state(unplayed, "s1")).toBe("hidden");
    expect(result.current.state(unplayed)).toBe("none");
  });

  it("turns off for the Profile with the global pref", async () => {
    vi.mocked(fetchClientPrefs).mockResolvedValue([
      { key: "profile.show-unplayed-details", value: "on" },
    ]);
    const { result } = await loaded();
    expect(result.current.guardOn).toBe(false);
    expect(result.current.state(unplayed, "s1")).toBe("none");
  });

  it("reveals a stored container and only that container", async () => {
    vi.mocked(fetchClientPrefs).mockResolvedValue([{ key: containerPrefKey("s/1"), value: "on" }]);
    const { result } = await loaded();
    expect(result.current.guardOn).toBe(true);
    expect(result.current.state(unplayed, "s/1")).toBe("container");
    expect(result.current.containerRevealed("s/1")).toBe(true);
    expect(result.current.state(unplayed, "s2")).toBe("hidden");
  });

  it("reveals one item for the session without storing it", async () => {
    const { result } = await loaded();
    act(() => toggleItemReveal("e1"));
    expect(result.current.state(unplayed, "s1")).toBe("revealed");
    act(() => revealItem("e1"));
    expect(result.current.state(unplayed, "s1")).toBe("revealed");
    act(() => toggleItemReveal("e1"));
    expect(result.current.state(unplayed, "s1")).toBe("hidden");
    expect(setClientPref).not.toHaveBeenCalled();
  });

  it("stores a container reveal and undoes it when the write fails", async () => {
    const { result } = await loaded();
    let saved: boolean | undefined;
    await act(async () => {
      saved = await setContainerRevealed("s1", true);
    });
    expect(saved).toBe(true);
    expect(setClientPref).toHaveBeenCalledWith(containerPrefKey("s1"), "on");
    expect(result.current.state(unplayed, "s1")).toBe("container");

    vi.mocked(setClientPref).mockRejectedValueOnce(new Error("offline"));
    await act(async () => {
      saved = await setContainerRevealed("s1", false);
    });
    expect(saved).toBe(false);
    expect(setClientPref).toHaveBeenLastCalledWith(containerPrefKey("s1"), "");
    expect(result.current.state(unplayed, "s1")).toBe("container");
  });

  it("folds a pref saved in Profile Settings into the guard", async () => {
    const { result } = await loaded();
    act(() => applySpoilerPref("p1", "profile.show-unplayed-details", "on"));
    expect(result.current.state(unplayed, "s1")).toBe("none");
    act(() => applySpoilerPref("p1", "profile.show-unplayed-details", ""));
    expect(result.current.state(unplayed, "s1")).toBe("hidden");
  });

  it("ignores a pref another identity saved", async () => {
    const { result } = await loaded();
    act(() => applySpoilerPref("p2", "profile.show-unplayed-details", "on"));
    expect(result.current.state(unplayed, "s1")).toBe("hidden");
  });

  it("drops a read that a Settings save overtook", async () => {
    let first: (prefs: never[]) => void = () => {};
    vi.mocked(fetchClientPrefs)
      .mockReturnValueOnce(new Promise((resolve) => (first = resolve)))
      .mockResolvedValueOnce([{ key: "profile.show-unplayed-details", value: "on" }] as never);
    const { result } = renderHook(() => useSpoilerGuard());
    await waitFor(() => expect(fetchClientPrefs).toHaveBeenCalledOnce());
    act(() => applySpoilerPref("p1", "profile.show-unplayed-details", "on"));
    await waitFor(() => expect(result.current.guardOn).toBe(false));
    await act(async () => first([]));
    expect(result.current.guardOn).toBe(false);
  });

  it("keeps every candidate hidden and ignores reveals until prefs load", () => {
    vi.mocked(fetchClientPrefs).mockReturnValueOnce(new Promise<never>(() => {}));
    const { result } = renderHook(() => useSpoilerGuard());
    act(() => toggleItemReveal("e1"));
    expect(result.current.state(unplayed, "s1")).toBe("hidden");
  });

  // Show then Hide before either save settles; writes run in order.
  async function showThenHide(first: () => Promise<never>, second: () => Promise<never>) {
    vi.mocked(setClientPref).mockImplementationOnce(first).mockImplementationOnce(second);
    let writes: Promise<boolean[]> = Promise.resolve([]);
    act(() => {
      writes = Promise.all([setContainerRevealed("s1", true), setContainerRevealed("s1", false)]);
    });
    let results: boolean[] = [];
    await act(async () => {
      results = await writes;
    });
    return results;
  }
  const ok = async () => undefined as never;
  const offline = async (): Promise<never> => {
    throw new Error("offline");
  };

  it("keeps the latest choice when an older container write fails", async () => {
    const { result } = await loaded();
    expect(await showThenHide(offline, ok)).toEqual([false, true]);
    expect(result.current.state(unplayed, "s1")).toBe("hidden");
  });

  it("returns to the stored value when every overlapping write fails", async () => {
    const { result } = await loaded();
    await showThenHide(offline, offline);
    expect(result.current.state(unplayed, "s1")).toBe("hidden");
  });

  it("returns to the value an earlier write stored when the latest fails", async () => {
    const { result } = await loaded();
    await showThenHide(ok, offline);
    expect(result.current.state(unplayed, "s1")).toBe("container");
  });

  it("drops a queued container write once the identity epoch ends", async () => {
    await loaded();
    setIdentityEpochSignal(new AbortController().signal);
    let settleFirst: () => void = () => {};
    vi.mocked(setClientPref).mockReturnValueOnce(
      new Promise((resolve) => (settleFirst = () => resolve(undefined as never))),
    );
    let writes: Promise<boolean[]> = Promise.resolve([]);
    act(() => {
      writes = Promise.all([setContainerRevealed("s1", true), setContainerRevealed("s1", false)]);
    });
    await waitFor(() => expect(setClientPref).toHaveBeenCalledOnce());
    setIdentityEpochSignal(new AbortController().signal);
    let results: boolean[] = [];
    await act(async () => {
      settleFirst();
      results = await writes;
    });
    setIdentityEpochSignal();
    expect(results).toEqual([true, false]);
    expect(setClientPref).toHaveBeenCalledOnce();
  });

  it("offers one container toggle only when every candidate shares a container", () => {
    const a = { ...unplayed, id: "a", container: { id: "showA", title: "A" } };
    const b = { ...unplayed, id: "b", container: { id: "showB", title: "B" } };
    expect(sharedGuardContainer([a, { ...a, id: "a2" }], "page")).toBe("showA");
    expect(sharedGuardContainer([a, b], "page")).toBeUndefined();
    expect(sharedGuardContainer([unplayed], "page")).toBe("page");
  });

  it("drops another identity's prefs and session reveals", async () => {
    vi.mocked(fetchClientPrefs).mockResolvedValue([
      { key: "profile.show-unplayed-details", value: "on" },
    ]);
    const hook = await loaded();
    act(() => toggleItemReveal("e1"));
    vi.mocked(fetchClientPrefs).mockReturnValue(new Promise<never>(() => {}));
    mocks.identity = "p2";
    hook.rerender();
    expect(hook.result.current.guardOn).toBeNull();
    expect(hook.result.current.state(unplayed, "s1")).toBe("hidden");
  });

  it("does not fetch while disabled", () => {
    renderHook(() => useSpoilerGuard(false));
    expect(fetchClientPrefs).not.toHaveBeenCalled();
  });
});
