import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import { setAdminEpochSignal } from "./adminEpoch";
import { useAsync } from "./useAsync";

const postUnmountWrites = vi.hoisted(() => ({ armed: false, count: 0 }));

vi.mock("react", async (importOriginal) => {
  const react = await importOriginal<typeof import("react")>();
  return {
    ...react,
    useState<T>(initial: T | (() => T)) {
      const [value, setValue] = react.useState(initial);
      const trackedSetValue: typeof setValue = (next) => {
        if (postUnmountWrites.armed) postUnmountWrites.count++;
        setValue(next);
      };
      return [value, trackedSetValue] as const;
    },
  };
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (err: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("useAsync epoch guard", () => {
  beforeEach(() => {
    postUnmountWrites.armed = false;
    postUnmountWrites.count = 0;
    setAdminEpochSignal();
  });

  afterEach(() => {
    setAdminEpochSignal();
  });

  // The Admin-Session epoch is a boundary this hook does not own and cannot
  // see from its own controller, so it must be composed INTO the signal the
  // fetcher receives — otherwise the request outlives the session that
  // authorized it, and its response is written into a locked Console.
  it("aborts and drops a fetch when the Admin-Session epoch ends", async () => {
    const admin = new AbortController();
    setAdminEpochSignal(admin.signal);
    const pending = deferred<string>();
    const fetcher = vi
      .fn<(signal?: AbortSignal) => Promise<string>>()
      .mockReturnValue(pending.promise);

    const view = renderHook(() => useAsync(fetcher));
    const signal = fetcher.mock.calls[0]?.[0];
    expect(signal).toBeInstanceOf(AbortSignal);
    expect(signal).not.toBe(admin.signal);
    expect(signal?.aborted).toBe(false);

    admin.abort();
    expect(signal?.aborted).toBe(true);

    postUnmountWrites.armed = true;
    await act(async () => {
      pending.resolve("prior-admin-epoch rows");
      await pending.promise;
    });
    postUnmountWrites.armed = false;
    expect(postUnmountWrites.count).toBe(0);
    expect(view.result.current.data).toBeNull();
  });

  it("hands the fetcher its own signal when no Admin-Session epoch is published", async () => {
    const pending = deferred<string>();
    const fetcher = vi
      .fn<(signal?: AbortSignal) => Promise<string>>()
      .mockReturnValue(pending.promise);

    const view = renderHook(() => useAsync(fetcher));
    const signal = fetcher.mock.calls[0]?.[0];
    expect(signal?.aborted).toBe(false);
    view.unmount();
    expect(signal?.aborted).toBe(true);
  });

  it("drops a stale resolve from a superseded fetch after deps change", async () => {
    const first = deferred<string>();
    const firstFetcher = vi
      .fn<(signal?: AbortSignal) => Promise<string>>()
      .mockReturnValue(first.promise);
    const replacementFetcher = vi
      .fn<(signal?: AbortSignal) => Promise<string>>()
      .mockResolvedValue("b");

    const view = renderHook(
      (dep: number) => useAsync(dep === 0 ? firstFetcher : replacementFetcher, [dep]),
      { initialProps: 0 },
    );
    const firstSignal = firstFetcher.mock.calls[0]?.[0];
    view.rerender(1);
    expect(firstSignal?.aborted).toBe(true);
    await waitFor(() => expect(view.result.current.data).toBe("b"));
    expect(replacementFetcher).toHaveBeenCalledTimes(1);

    await act(async () => {
      first.resolve("a");
      await first.promise;
    });

    expect(view.result.current.data).toBe("b");
  });

  it("drops a stale reject from a superseded fetch after deps change", async () => {
    const first = deferred<string>();
    const firstFetcher = vi
      .fn<(signal?: AbortSignal) => Promise<string>>()
      .mockReturnValue(first.promise);
    const replacementFetcher = vi
      .fn<(signal?: AbortSignal) => Promise<string>>()
      .mockResolvedValue("b");

    const view = renderHook(
      (dep: number) => useAsync(dep === 0 ? firstFetcher : replacementFetcher, [dep]),
      { initialProps: 0 },
    );
    const firstSignal = firstFetcher.mock.calls[0]?.[0];
    view.rerender(1);
    expect(firstSignal?.aborted).toBe(true);
    await waitFor(() => expect(view.result.current.data).toBe("b"));
    expect(replacementFetcher).toHaveBeenCalledTimes(1);

    await act(async () => {
      first.reject(new Error("stale failure"));
      await first.promise.catch(() => {});
    });

    expect(view.result.current.data).toBe("b");
    expect(view.result.current.failed).toBe(false);
  });

  it("clears prior scoped data and error state when a dependency changes", async () => {
    const replacement = deferred<string>();
    const departedFetcher = vi
      .fn<(signal?: AbortSignal) => Promise<string>>()
      .mockResolvedValueOnce("departed")
      .mockRejectedValueOnce(new Error("departed refresh failed"));
    const replacementFetcher = vi
      .fn<(signal?: AbortSignal) => Promise<string>>()
      .mockReturnValue(replacement.promise);

    const view = renderHook(
      (dep: string) => useAsync(dep === "profile-a" ? departedFetcher : replacementFetcher, [dep]),
      { initialProps: "profile-a" },
    );
    await waitFor(() => expect(view.result.current.data).toBe("departed"));
    await act(async () => {
      await view.result.current.refresh();
    });
    expect(view.result.current.data).toBe("departed");
    expect(view.result.current.failed).toBe(true);
    const departedSignal = departedFetcher.mock.calls[1]?.[0];

    view.rerender("profile-b");

    expect(departedSignal?.aborted).toBe(true);
    expect(view.result.current.data).toBeNull();
    expect(view.result.current.loading).toBe(true);
    expect(view.result.current.failed).toBe(false);
    expect(replacementFetcher).toHaveBeenCalledTimes(1);

    await act(async () => {
      replacement.reject(new Error("replacement failed"));
      await replacement.promise.catch(() => {});
    });

    expect(view.result.current.data).toBeNull();
    expect(view.result.current.loading).toBe(false);
    expect(view.result.current.failed).toBe(true);
  });

  it.each([401, 403])("clears last-good data after a %i auth rejection", async (status) => {
    const fetcher = vi
      .fn<(signal?: AbortSignal) => Promise<string>>()
      .mockResolvedValueOnce("departed identity data")
      .mockRejectedValueOnce(new ApiError("auth rejected", status));

    const view = renderHook(() => useAsync(fetcher));
    await waitFor(() => expect(view.result.current.data).toBe("departed identity data"));

    await act(async () => {
      await view.result.current.refresh();
    });

    expect(view.result.current.data).toBeNull();
    expect(view.result.current.loading).toBe(false);
    expect(view.result.current.failed).toBe(true);
  });

  it("retains last-good data after a non-auth rejection", async () => {
    const fetcher = vi
      .fn<(signal?: AbortSignal) => Promise<string>>()
      .mockResolvedValueOnce("last good")
      .mockRejectedValueOnce(new ApiError("temporary failure", 500));

    const view = renderHook(() => useAsync(fetcher));
    await waitFor(() => expect(view.result.current.data).toBe("last good"));

    await act(async () => {
      await view.result.current.refresh();
    });

    expect(view.result.current.data).toBe("last good");
    expect(view.result.current.loading).toBe(false);
    expect(view.result.current.failed).toBe(true);
  });

  it("keeps only the later result across two overlapping manual refreshes", async () => {
    const first = deferred<string>();
    const fetcher = vi
      .fn<(signal?: AbortSignal) => Promise<string>>()
      .mockResolvedValueOnce("initial")
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce("second");

    const view = renderHook(() => useAsync(fetcher));
    await waitFor(() => expect(view.result.current.data).toBe("initial"));

    let refreshTwo!: Promise<void>;
    await act(async () => {
      void view.result.current.refresh();
      const firstSignal = fetcher.mock.calls[1]?.[0];
      refreshTwo = view.result.current.refresh();
      expect(firstSignal?.aborted).toBe(true);
      await refreshTwo;
    });
    await waitFor(() => expect(view.result.current.data).toBe("second"));

    await act(async () => {
      first.resolve("first-late");
      await first.promise;
    });

    expect(view.result.current.data).toBe("second");
  });

  it.each(["resolve", "reject"] as const)(
    "keeps the current request loading when a stale request settles via %s",
    async (outcome) => {
      const stale = deferred<string>();
      const current = deferred<string>();
      const fetcher = vi
        .fn<(signal?: AbortSignal) => Promise<string>>()
        .mockResolvedValueOnce("initial")
        .mockReturnValueOnce(stale.promise)
        .mockReturnValueOnce(current.promise);

      const view = renderHook(() => useAsync(fetcher));
      await waitFor(() => expect(view.result.current.data).toBe("initial"));

      let staleRefresh!: Promise<void>;
      let currentRefresh!: Promise<void>;
      act(() => {
        staleRefresh = view.result.current.refresh();
        currentRefresh = view.result.current.refresh();
      });
      const staleSignal = fetcher.mock.calls[1]?.[0];
      const currentSignal = fetcher.mock.calls[2]?.[0];
      expect(staleSignal?.aborted).toBe(true);
      expect(currentSignal?.aborted).toBe(false);
      expect(view.result.current.loading).toBe(true);

      await act(async () => {
        if (outcome === "resolve") stale.resolve("stale");
        else stale.reject(new Error("stale failure"));
        await stale.promise.catch(() => {});
        await staleRefresh;
      });

      expect(view.result.current.data).toBe("initial");
      expect(view.result.current.loading).toBe(true);
      expect(view.result.current.failed).toBe(false);

      await act(async () => {
        current.resolve("current");
        await currentRefresh;
      });
      expect(view.result.current.data).toBe("current");
      expect(view.result.current.loading).toBe(false);
      expect(view.result.current.failed).toBe(false);
    },
  );

  it("keeps the last-good data visible while a refresh is in flight (no empty flash)", async () => {
    const pending = deferred<string>();
    const fetcher = vi
      .fn<(signal?: AbortSignal) => Promise<string>>()
      .mockResolvedValueOnce("initial")
      .mockReturnValueOnce(pending.promise);

    const view = renderHook(() => useAsync(fetcher));
    await waitFor(() => expect(view.result.current.data).toBe("initial"));

    act(() => {
      void view.result.current.refresh();
    });

    expect(view.result.current.data).toBe("initial");
    expect(view.result.current.loading).toBe(true);

    await act(async () => {
      pending.resolve("resolved");
      await pending.promise;
    });
    expect(view.result.current.data).toBe("resolved");
  });

  it("does not write state after unmount", async () => {
    const pending = deferred<string>();
    const fetcher = vi
      .fn<(signal?: AbortSignal) => Promise<string>>()
      .mockReturnValueOnce(pending.promise);

    const view = renderHook(() => useAsync(fetcher));
    const signal = fetcher.mock.calls[0]?.[0];
    view.unmount();
    expect(signal?.aborted).toBe(true);

    postUnmountWrites.armed = true;
    await act(async () => {
      pending.resolve("late");
      await pending.promise;
    });
    postUnmountWrites.armed = false;
    expect(postUnmountWrites.count).toBe(0);
  });

  it("does not write state after a late rejection following unmount", async () => {
    const pending = deferred<string>();
    const fetcher = vi
      .fn<(signal?: AbortSignal) => Promise<string>>()
      .mockReturnValueOnce(pending.promise);

    const view = renderHook(() => useAsync(fetcher));
    const signal = fetcher.mock.calls[0]?.[0];
    view.unmount();
    expect(signal?.aborted).toBe(true);

    postUnmountWrites.armed = true;
    await act(async () => {
      pending.reject(new Error("late rejection"));
      await pending.promise.catch(() => {});
    });
    postUnmountWrites.armed = false;
    expect(postUnmountWrites.count).toBe(0);
  });
});
