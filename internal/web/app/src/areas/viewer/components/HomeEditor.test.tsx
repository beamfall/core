import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchHomeLayout, resetHomeLayout, setHomeLayout } from "@/lib/graphql/viewer";
import { I18nProvider } from "@/lib/i18n";
import { HomeEditor } from "./HomeEditor";

vi.mock("@/lib/graphql/viewer", () => ({
  fetchHomeLayout: vi.fn(),
  setHomeLayout: vi.fn(),
  resetHomeLayout: vi.fn(),
}));

const layout = {
  customized: false,
  sections: [
    { id: "continue", kind: "shelf", title: { en: "Continue" }, hidden: false },
    { id: "recent", kind: "shelf", title: { en: "Recent" }, hidden: false },
    { id: "watchlist", kind: "shelf", title: { en: "Watchlist" }, hidden: false },
  ],
};

function renderEditor() {
  vi.mocked(fetchHomeLayout).mockResolvedValue(layout);
  const onSaved = vi.fn();
  const onClose = vi.fn();
  render(
    <I18nProvider>
      <HomeEditor onSaved={onSaved} onClose={onClose} />
    </I18nProvider>,
  );
  return { onSaved, onClose };
}

function rowNames() {
  return screen.getAllByRole("listitem").map((row) => row.getAttribute("data-section-id"));
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("HomeEditor (CORE-1972)", () => {
  it("reorders with buttons and the keyboard, hides a shelf and saves", async () => {
    const { onSaved, onClose } = renderEditor();
    vi.mocked(setHomeLayout).mockResolvedValue({ ...layout, customized: true });

    fireEvent.click(await screen.findByRole("button", { name: "Move Recent up" }));
    expect(rowNames()).toEqual(["recent", "continue", "watchlist"]);
    expect(screen.getByRole("button", { name: "Move Recent up" })).toBeDisabled();

    const watchlistRow = screen.getAllByRole("listitem")[2];
    fireEvent.keyDown(watchlistRow, { key: "ArrowUp", altKey: true });
    expect(rowNames()).toEqual(["recent", "watchlist", "continue"]);

    fireEvent.click(within(watchlistRow).getByRole("switch", { name: "Show Watchlist on Home" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(setHomeLayout).toHaveBeenCalledWith(["recent", "watchlist", "continue"], ["watchlist"]);
    expect(onSaved).toHaveBeenCalled();
  });

  it("reorders by drag and drop", async () => {
    renderEditor();
    await screen.findByRole("button", { name: "Move Recent up" });
    const [first, , last] = screen.getAllByRole("listitem");
    fireEvent.dragStart(last);
    fireEvent.dragOver(first);
    fireEvent.drop(first);
    expect(rowNames()).toEqual(["watchlist", "continue", "recent"]);
  });

  it("resets to the default", async () => {
    const { onSaved, onClose } = renderEditor();
    vi.mocked(resetHomeLayout).mockResolvedValue(layout);
    fireEvent.click(await screen.findByRole("button", { name: "Reset to default" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(resetHomeLayout).toHaveBeenCalled();
    expect(onSaved).toHaveBeenCalled();
    expect(setHomeLayout).not.toHaveBeenCalled();
  });

  it("keeps editing and says so when saving fails", async () => {
    const { onClose } = renderEditor();
    vi.mocked(setHomeLayout).mockRejectedValue(new Error("offline"));
    fireEvent.click(await screen.findByRole("button", { name: "Save" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Your Home could not be saved");
    expect(onClose).not.toHaveBeenCalled();
  });
});
