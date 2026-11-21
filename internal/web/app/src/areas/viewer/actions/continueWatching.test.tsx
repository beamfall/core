import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, renderHook, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setContinueWatchingHidden } from "@/lib/graphql";
import type { SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import {
  clearContinueWatchingRemoved,
  removeFromContinueWatching,
  settleContinueWatching,
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
});

function item(id: string): SourceItem {
  return { id, kind: "media", title: id } as SourceItem;
}

describe("removeFromContinueWatching", () => {
  it("drops the card at once and keeps it out until a fresh read confirms", async () => {
    const { result } = renderHook(() => useContinueWatchingRemoved());
    await act(() => removeFromContinueWatching("m1", "Pilot"));
    expect(setContinueWatchingHidden).toHaveBeenCalledWith("m1", true);
    expect(result.current.has("m1")).toBe(true);
    // A read started before the write still lists it: the card stays out.
    act(() => settleContinueWatching([item("m1"), item("m2")]));
    expect(result.current.has("m1")).toBe(true);
    // The server's row no longer lists it: playing it again can bring it back.
    act(() => settleContinueWatching([item("m2")]));
    expect(result.current.has("m1")).toBe(false);
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
  it("restores the Media to the row", async () => {
    render(
      <I18nProvider initialLocale="en">
        <HiddenUndoNotice />
      </I18nProvider>,
    );
    const { result } = renderHook(() => useContinueWatchingRemoved());
    await act(() => removeFromContinueWatching("m1", "Pilot"));
    expect(screen.getByRole("status")).toHaveTextContent("Removed “Pilot” from Continue Watching.");
    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    await vi.waitFor(() => expect(setContinueWatchingHidden).toHaveBeenLastCalledWith("m1", false));
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
});
