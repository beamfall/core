import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearMute, fetchClientPrefs, fetchMutes, setClientPref, setMute } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { PREF_KEYS } from "../lib/preferences";
import { HiddenAwareEmpty, HiddenUndoNotice, HideWatchedToggle } from "./HiddenControls";
import { hideItem, muteFor } from "./hiddenItems";

vi.mock("@/lib/graphql", () => ({
  clearAllMutes: vi.fn(async () => 0),
  clearMute: vi.fn(async () => undefined),
  fetchClientPrefs: vi.fn(),
  fetchMutes: vi.fn(async () => []),
  setClientPref: vi.fn(async () => undefined),
  setMute: vi.fn(async () => undefined),
}));

beforeEach(() => {
  vi.mocked(setMute).mockClear();
  vi.mocked(clearMute).mockClear();
  vi.mocked(setClientPref).mockClear();
  vi.mocked(fetchClientPrefs).mockReset().mockResolvedValue([]);
  vi.mocked(fetchMutes).mockReset().mockResolvedValue([]);
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

function renderIn(node: React.ReactNode) {
  return render(<I18nProvider initialLocale="en">{node}</I18nProvider>);
}

describe("muteFor", () => {
  it("hides one Media item or a whole Entity tree", () => {
    expect(muteFor({ mediaID: "m1" })).toEqual({ kind: "media", targetID: "m1" });
    expect(muteFor({ entityID: "show-1" })).toEqual({ kind: "entity", targetID: "show-1" });
  });
});

describe("HiddenUndoNotice", () => {
  it("renders nothing until something is hidden", () => {
    const { container } = render(<HiddenUndoNotice />);
    expect(container).toBeEmptyDOMElement();
  });

  it("offers Undo after a hide and clears the Mute", async () => {
    renderIn(<HiddenUndoNotice />);
    await act(() => hideItem({ entityID: "show-1" }, "The Show"));
    expect(setMute).toHaveBeenCalledWith("entity", "show-1");
    expect(screen.getByRole("status")).toHaveTextContent("Hid “The Show”.");
    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    await vi.waitFor(() => expect(clearMute).toHaveBeenCalledWith("entity", "show-1"));
    await vi.waitFor(() => expect(screen.queryByRole("status")).not.toBeInTheDocument());
  });

  it("drops the notice when another Profile is bound", async () => {
    const { rerender } = renderIn(<HiddenUndoNotice profileID="p1" />);
    await act(() => hideItem({ mediaID: "m1" }, "Pilot"));
    expect(screen.getByRole("status")).toBeInTheDocument();
    rerender(
      <I18nProvider initialLocale="en">
        <HiddenUndoNotice profileID="p2" />
      </I18nProvider>,
    );
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("drops the notice when the viewer remounts for another Profile", async () => {
    const { rerender } = renderIn(<HiddenUndoNotice key="p1" profileID="p1" />);
    await act(() => hideItem({ mediaID: "m1" }, "Pilot"));
    expect(screen.getByRole("status")).toBeInTheDocument();
    rerender(
      <I18nProvider initialLocale="en">
        <HiddenUndoNotice key="p2" profileID="p2" />
      </I18nProvider>,
    );
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("dismisses itself after a while", async () => {
    vi.useFakeTimers();
    renderIn(<HiddenUndoNotice />);
    await act(() => hideItem({ mediaID: "m1" }, "Pilot"));
    expect(screen.getByRole("status")).toBeInTheDocument();
    act(() => vi.advanceTimersByTime(8000));
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });
});

describe("HideWatchedToggle", () => {
  it("reads the Profile preference and saves a change", async () => {
    vi.mocked(fetchClientPrefs).mockResolvedValue([{ key: PREF_KEYS.hideWatched, value: "on" }]);
    renderIn(<HideWatchedToggle />);
    const toggle = screen.getByRole("button", { name: "Hide watched" });
    await vi.waitFor(() => expect(toggle).toHaveAttribute("aria-pressed", "true"));
    vi.mocked(fetchClientPrefs).mockResolvedValue([]);
    fireEvent.click(toggle);
    await vi.waitFor(() => expect(setClientPref).toHaveBeenCalledWith(PREF_KEYS.hideWatched, ""));
    await vi.waitFor(() => expect(toggle).toHaveAttribute("aria-pressed", "false"));
  });
});

describe("HiddenAwareEmpty", () => {
  const empty = () =>
    renderIn(
      <MemoryRouter>
        <HiddenAwareEmpty fallback={<p>Library empty</p>} />
      </MemoryRouter>,
    );

  it("shows the library empty state when nothing is hidden", async () => {
    empty();
    expect(await screen.findByText("Library empty")).toBeInTheDocument();
  });

  it("says the Profile's hides emptied the page and links to settings", async () => {
    vi.mocked(fetchMutes).mockResolvedValue([{ kind: "entity", targetID: "show-1" }] as never);
    empty();
    expect(
      await screen.findByText("Everything here is hidden on this profile."),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Show hidden items" })).toHaveAttribute(
      "href",
      "/settings",
    );
    expect(screen.queryByText("Library empty")).not.toBeInTheDocument();
  });

  it("counts Hide watched as a reason", async () => {
    vi.mocked(fetchClientPrefs).mockResolvedValue([
      { key: PREF_KEYS.hideWatched, value: "on" },
    ] as never);
    empty();
    expect(
      await screen.findByText("Everything here is hidden on this profile."),
    ).toBeInTheDocument();
  });
});
