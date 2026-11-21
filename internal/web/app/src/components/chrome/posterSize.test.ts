import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setIdentityEpochSignal } from "@/lib/api";
import { fetchClientPrefs, setClientPref } from "@/lib/graphql/viewer";
import {
  POSTER_SIZE_KEY,
  POSTER_SIZE_PREF_KEY,
  readPosterSize,
  resetPosterSizeSync,
  setPosterSize,
  usePosterSizeRoot,
} from "./posterSize";

vi.mock("@/lib/graphql/viewer", () => ({
  fetchClientPrefs: vi.fn(),
  setClientPref: vi.fn(),
}));

const saved = (value: string) => [{ key: POSTER_SIZE_PREF_KEY, value }];

beforeEach(() => {
  localStorage.clear();
  vi.mocked(setClientPref).mockResolvedValue({ key: POSTER_SIZE_PREF_KEY });
});

afterEach(() => {
  setIdentityEpochSignal(undefined);
  resetPosterSizeSync();
  localStorage.clear();
  vi.mocked(fetchClientPrefs).mockReset();
  vi.mocked(setClientPref).mockReset();
});

describe("poster size roaming with the Profile (CORE-2914)", () => {
  it("draws the Profile's saved size in a browser that held another", async () => {
    localStorage.setItem(POSTER_SIZE_KEY, "large");
    vi.mocked(fetchClientPrefs).mockResolvedValue(saved("small"));

    renderHook(() => usePosterSizeRoot("p1"));

    await waitFor(() => expect(readPosterSize()).toBe("small"));
    expect(document.documentElement.dataset.posterSize).toBe("small");
    // Adopting the Profile's size is not a pick: nothing is written back.
    expect(setClientPref).not.toHaveBeenCalled();
  });

  it.each([[[]], [saved("huge")]])(
    "keeps the browser's value and saves nothing when the Profile has no usable size (%j)",
    async (prefs) => {
      localStorage.setItem(POSTER_SIZE_KEY, "medium");
      vi.mocked(fetchClientPrefs).mockResolvedValue(prefs);

      renderHook(() => usePosterSizeRoot("p1"));

      await waitFor(() => expect(fetchClientPrefs).toHaveBeenCalled());
      await Promise.resolve();
      expect(readPosterSize()).toBe("medium");
      expect(setClientPref).not.toHaveBeenCalled();
    },
  );

  it("saves each pick to the Profile in order", async () => {
    vi.mocked(fetchClientPrefs).mockResolvedValue([]);
    renderHook(() => usePosterSizeRoot("p1"));

    setPosterSize("medium");
    setPosterSize("small");

    expect(readPosterSize()).toBe("small");
    await waitFor(() => expect(setClientPref).toHaveBeenCalledTimes(2));
    expect(vi.mocked(setClientPref).mock.calls).toEqual([
      [POSTER_SIZE_PREF_KEY, "medium"],
      [POSTER_SIZE_PREF_KEY, "small"],
    ]);
  });

  it("lets a pick made while the Profile's read is in flight win over the older read", async () => {
    let resolveRead: (prefs: { key: string; value: string }[]) => void = () => {};
    vi.mocked(fetchClientPrefs).mockReturnValue(
      new Promise((resolve) => {
        resolveRead = resolve;
      }),
    );
    renderHook(() => usePosterSizeRoot("p1"));

    setPosterSize("medium");
    resolveRead(saved("small"));
    await waitFor(() => expect(setClientPref).toHaveBeenCalledWith(POSTER_SIZE_PREF_KEY, "medium"));
    expect(readPosterSize()).toBe("medium");
  });

  it("saves nothing without a Profile", async () => {
    renderHook(() => usePosterSizeRoot(null));

    setPosterSize("small");

    expect(readPosterSize()).toBe("small");
    await Promise.resolve();
    expect(fetchClientPrefs).not.toHaveBeenCalled();
    expect(setClientPref).not.toHaveBeenCalled();
  });

  it("drops a save queued behind another once the identity changes", async () => {
    vi.mocked(fetchClientPrefs).mockResolvedValue([]);
    const first = new AbortController();
    setIdentityEpochSignal(first.signal);
    let releaseFirst: () => void = () => {};
    vi.mocked(setClientPref).mockReturnValueOnce(
      new Promise((resolve) => {
        releaseFirst = () => resolve({ key: POSTER_SIZE_PREF_KEY });
      }),
    );
    renderHook(() => usePosterSizeRoot("p1"));

    setPosterSize("medium");
    setPosterSize("small");
    await waitFor(() => expect(setClientPref).toHaveBeenCalledTimes(1));
    // A reveal, relock or Profile switch starts a new epoch.
    first.abort();
    setIdentityEpochSignal(new AbortController().signal);
    releaseFirst();
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(setClientPref).toHaveBeenCalledTimes(1);
  });

  it("never writes one Profile's pick under the next Profile", async () => {
    vi.mocked(fetchClientPrefs).mockResolvedValue([]);
    let releaseFirst: () => void = () => {};
    vi.mocked(setClientPref).mockReturnValueOnce(
      new Promise((resolve) => {
        releaseFirst = () => resolve({ key: POSTER_SIZE_PREF_KEY });
      }),
    );
    const { rerender } = renderHook(({ id }) => usePosterSizeRoot(id), {
      initialProps: { id: "p1" as string | null },
    });

    setPosterSize("medium");
    setPosterSize("small");
    await waitFor(() => expect(setClientPref).toHaveBeenCalledTimes(1));
    rerender({ id: "p2" });
    releaseFirst();
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(setClientPref).toHaveBeenCalledTimes(1);
    // The next Profile with no saved size keeps the browser's value.
    expect(readPosterSize()).toBe("small");
  });

  it("keeps the browser's pick when the save fails, and reads again after a failed read", async () => {
    vi.mocked(fetchClientPrefs).mockRejectedValueOnce(new Error("offline"));
    vi.mocked(setClientPref).mockRejectedValueOnce(new Error("offline"));
    const first = renderHook(() => usePosterSizeRoot("p1"));
    await waitFor(() => expect(fetchClientPrefs).toHaveBeenCalledTimes(1));

    setPosterSize("small");
    await waitFor(() => expect(setClientPref).toHaveBeenCalledTimes(1));
    expect(readPosterSize()).toBe("small");

    // The next shell mount reads again; the pick made meanwhile still wins.
    first.unmount();
    vi.mocked(fetchClientPrefs).mockResolvedValue(saved("large"));
    renderHook(() => usePosterSizeRoot("p1"));
    await waitFor(() => expect(fetchClientPrefs).toHaveBeenCalledTimes(2));
    await Promise.resolve();
    expect(readPosterSize()).toBe("small");
  });

  it("keeps a queued save when a retried read replaces a failed one", async () => {
    vi.mocked(fetchClientPrefs).mockRejectedValueOnce(new Error("offline"));
    let releaseFirst: () => void = () => {};
    vi.mocked(setClientPref).mockReturnValueOnce(
      new Promise((resolve) => {
        releaseFirst = () => resolve({ key: POSTER_SIZE_PREF_KEY });
      }),
    );
    const first = renderHook(() => usePosterSizeRoot("p1"));
    await waitFor(() => expect(fetchClientPrefs).toHaveBeenCalledTimes(1));
    await new Promise((resolve) => setTimeout(resolve, 0));

    setPosterSize("medium");
    setPosterSize("small");
    await waitFor(() => expect(setClientPref).toHaveBeenCalledTimes(1));
    first.unmount();
    vi.mocked(fetchClientPrefs).mockResolvedValue([]);
    renderHook(() => usePosterSizeRoot("p1"));
    releaseFirst();

    await waitFor(() => expect(setClientPref).toHaveBeenCalledTimes(2));
    expect(setClientPref).toHaveBeenLastCalledWith(POSTER_SIZE_PREF_KEY, "small");
  });

  it("finishes queued saves and keeps the pick across a Console visit", async () => {
    vi.mocked(fetchClientPrefs).mockResolvedValue(saved("large"));
    let releaseFirst: () => void = () => {};
    vi.mocked(setClientPref).mockReturnValueOnce(
      new Promise((resolve) => {
        releaseFirst = () => resolve({ key: POSTER_SIZE_PREF_KEY });
      }),
    );
    const { rerender } = renderHook(({ id }) => usePosterSizeRoot(id), {
      initialProps: { id: "p1" as string | null },
    });
    await waitFor(() => expect(fetchClientPrefs).toHaveBeenCalledTimes(1));

    setPosterSize("medium");
    setPosterSize("small");
    await waitFor(() => expect(setClientPref).toHaveBeenCalledTimes(1));
    rerender({ id: null });
    // A pick in the Console stays the browser's own.
    setPosterSize("medium");
    setPosterSize("small");
    releaseFirst();
    await waitFor(() => expect(setClientPref).toHaveBeenCalledTimes(2));
    expect(setClientPref).toHaveBeenLastCalledWith(POSTER_SIZE_PREF_KEY, "small");

    // Back in the Viewer the loaded Profile is not read again over the pick.
    rerender({ id: "p1" });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(fetchClientPrefs).toHaveBeenCalledTimes(1);
    expect(setClientPref).toHaveBeenCalledTimes(2);
    expect(readPosterSize()).toBe("small");
  });

  it("lets another tab's change win over a read still in flight", async () => {
    let resolveRead: (prefs: { key: string; value: string }[]) => void = () => {};
    vi.mocked(fetchClientPrefs).mockReturnValue(
      new Promise((resolve) => {
        resolveRead = resolve;
      }),
    );
    renderHook(() => usePosterSizeRoot("p1"));

    // Another tab picks Small and saves it.
    localStorage.setItem(POSTER_SIZE_KEY, "small");
    window.dispatchEvent(new StorageEvent("storage", { key: POSTER_SIZE_KEY, newValue: "small" }));
    resolveRead(saved("large"));
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(readPosterSize()).toBe("small");
    expect(setClientPref).not.toHaveBeenCalled();
  });
});
