import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, renderHook, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setIdentityEpochSignal } from "@/lib/api";
import { setContinueWatchingHidden } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import {
  clearContinueWatchingRemoved,
  continueWatchingKeepsOut,
  continueWatchingReadSeq,
  removeFromContinueWatching,
  useContinueWatchingRemoved,
} from "./continueWatching";
import { HiddenUndoNotice } from "./HiddenControls";

vi.mock("@/lib/graphql", () => ({
  clearAllMutes: vi.fn(async () => 0),
  clearMute: vi.fn(async () => undefined),
  fetchClientPrefs: vi.fn(async () => []),
  fetchMutes: vi.fn(async () => []),
  setClientPref: vi.fn(async () => undefined),
  setMute: vi.fn(async () => undefined),
  setContinueWatchingHidden: vi.fn(),
}));

const state = { id: "m1", kind: "media", watched: false, watchlisted: false, rating: 0 };

beforeEach(() => {
  vi.mocked(setContinueWatchingHidden).mockReset().mockResolvedValue(state);
});

afterEach(() => {
  cleanup();
  act(() => clearContinueWatchingRemoved());
  setIdentityEpochSignal();
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe("removeFromContinueWatching", () => {
  it("keeps the card out of reads that started before its write settled", async () => {
    const write = deferred<typeof state>();
    vi.mocked(setContinueWatchingHidden).mockReturnValue(write.promise);
    const { result } = renderHook(() => useContinueWatchingRemoved());
    const before = continueWatchingReadSeq();
    let removal!: Promise<void>;
    act(() => {
      removal = removeFromContinueWatching("m1", "Pilot");
    });
    expect(setContinueWatchingHidden).toHaveBeenCalledWith("m1", true, undefined);
    // In flight: no read can bring the card back yet.
    const during = continueWatchingReadSeq();
    expect(continueWatchingKeepsOut(result.current, "m1", during)).toBe(true);
    await act(async () => {
      write.resolve(state);
      await removal;
    });
    // Reads started before the write settled stay overridden; a read started
    // after it is the server's answer, whatever it lists.
    expect(continueWatchingKeepsOut(result.current, "m1", before)).toBe(true);
    expect(continueWatchingKeepsOut(result.current, "m1", during)).toBe(true);
    expect(continueWatchingKeepsOut(result.current, "m1", continueWatchingReadSeq())).toBe(false);
    expect(continueWatchingKeepsOut(result.current, "m2", before)).toBe(false);
  });

  it("puts the card back and says so when the write fails", async () => {
    vi.mocked(setContinueWatchingHidden).mockRejectedValue(new Error("offline"));
    render(
      <I18nProvider initialLocale="en">
        <HiddenUndoNotice />
      </I18nProvider>,
    );
    const { result } = renderHook(() => useContinueWatchingRemoved());
    await act(() => removeFromContinueWatching("m1", "Pilot"));
    expect(result.current.has("m1")).toBe(false);
    expect(screen.getByRole("status")).toHaveTextContent("That didn't save. Try again.");
    expect(screen.queryByRole("button", { name: "Undo" })).toBeNull();
  });
});

describe("Continue Watching Undo", () => {
  it("restores the Media to the row, naming the Profile it was removed for", async () => {
    const { rerender } = render(
      <I18nProvider initialLocale="en">
        <HiddenUndoNotice profileID="p1" />
      </I18nProvider>,
    );
    const { result } = renderHook(() => useContinueWatchingRemoved());
    await act(() => removeFromContinueWatching("m1", "Pilot"));
    expect(setContinueWatchingHidden).toHaveBeenLastCalledWith("m1", true, "p1");
    // A re-render for the same Profile keeps the notice; the server checks
    // the Profile, so another tab's switch cannot redirect the Undo.
    rerender(
      <I18nProvider initialLocale="en">
        <HiddenUndoNotice profileID="p1" />
      </I18nProvider>,
    );
    expect(screen.getByRole("status")).toHaveTextContent("Removed “Pilot” from Continue Watching.");
    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    await vi.waitFor(() =>
      expect(setContinueWatchingHidden).toHaveBeenLastCalledWith("m1", false, "p1"),
    );
    await vi.waitFor(() => expect(screen.queryByRole("status")).not.toBeInTheDocument());
    expect(result.current.has("m1")).toBe(false);
  });

  it("forgets pending removals when another Profile is bound", async () => {
    const { rerender } = render(
      <I18nProvider initialLocale="en">
        <HiddenUndoNotice profileID="p1" />
      </I18nProvider>,
    );
    const { result } = renderHook(() => useContinueWatchingRemoved());
    await act(() => removeFromContinueWatching("m1", "Pilot"));
    rerender(
      <I18nProvider initialLocale="en">
        <HiddenUndoNotice profileID="p2" />
      </I18nProvider>,
    );
    expect(result.current.size).toBe(0);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("drops a removal that settles after a Profile switch, and its notice", async () => {
    setIdentityEpochSignal(new AbortController().signal);
    const write = deferred<typeof state>();
    vi.mocked(setContinueWatchingHidden).mockReturnValue(write.promise);
    const { rerender } = render(
      <I18nProvider initialLocale="en">
        <HiddenUndoNotice profileID="p1" />
      </I18nProvider>,
    );
    const { result } = renderHook(() => useContinueWatchingRemoved());
    let removal!: Promise<void>;
    act(() => {
      removal = removeFromContinueWatching("m1", "Pilot");
    });
    // The Profile changes while the write is pending.
    setIdentityEpochSignal(new AbortController().signal);
    rerender(
      <I18nProvider initialLocale="en">
        <HiddenUndoNotice profileID="p2" />
      </I18nProvider>,
    );
    await act(async () => {
      write.resolve(state);
      await removal;
    });
    expect(result.current.size).toBe(0);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("writes nothing when Undo is pressed after the Profile changed", async () => {
    setIdentityEpochSignal(new AbortController().signal);
    render(
      <I18nProvider initialLocale="en">
        <HiddenUndoNotice />
      </I18nProvider>,
    );
    await act(() => removeFromContinueWatching("m1", "Pilot"));
    vi.mocked(setContinueWatchingHidden).mockClear();
    // The epoch moves without the notice being cleared (it outlived its Profile).
    setIdentityEpochSignal(new AbortController().signal);
    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    await vi.waitFor(() => expect(screen.queryByRole("status")).not.toBeInTheDocument());
    expect(setContinueWatchingHidden).not.toHaveBeenCalled();
  });
});
