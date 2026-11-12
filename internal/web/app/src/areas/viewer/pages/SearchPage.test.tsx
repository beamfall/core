import type { ReactNode } from "react";
import "@testing-library/jest-dom/vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchClientPrefs, fetchSemanticCapability, searchResult } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { notifyEngagementChanged } from "../actions/engagementSync";
import { SearchPage } from "./SearchPage";

vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <>{children}</>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));
vi.mock("../components/MediaItemCard", () => ({
  MediaItemCard: ({ item, queue }: { item: { title: string }; queue?: { id: string }[] }) => (
    <li data-queue={queue?.map((entry) => entry.id).join(",")}>{item.title}</li>
  ),
}));

vi.mock("@/lib/graphql", () => ({
  searchResult: vi.fn(),
  fetchSemanticCapability: vi.fn(),
  fetchClientPrefs: vi.fn(),
}));

// Radix Select drives pointer-capture / scrollIntoView APIs that jsdom does not
// implement; stub them so the Select can be opened in tests (standard workaround).
beforeEach(() => {
  if (!Element.prototype.hasPointerCapture) {
    Element.prototype.hasPointerCapture = () => false;
  }
  if (!Element.prototype.setPointerCapture) {
    Element.prototype.setPointerCapture = () => {};
  }
  if (!Element.prototype.releasePointerCapture) {
    Element.prototype.releasePointerCapture = () => {};
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {};
  }
  vi.mocked(searchResult).mockReset();
  vi.mocked(fetchSemanticCapability).mockReset();
  vi.mocked(fetchClientPrefs).mockReset();
  vi.mocked(fetchSemanticCapability).mockResolvedValue([]);
  vi.mocked(fetchClientPrefs).mockResolvedValue([]);
  vi.mocked(searchResult).mockResolvedValue({
    mode: "keyword",
    items: [{ id: "m1", kind: "media", title: "Hit", mode: "keyword", score: 0 }],
    restrictedResultsPresent: false,
  });
});

function renderPage(entry = "/search") {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[entry]}>
        <SearchPage />
      </MemoryRouter>
    </I18nProvider>,
  );
}

describe("SearchPage mode exposure", () => {
  it("hides the mode toggle when only keyword is available", async () => {
    renderPage();
    await waitFor(() => expect(fetchSemanticCapability).toHaveBeenCalled());
    expect(screen.queryByLabelText("Search mode")).toBeNull();
  });

  it("shows the semantic option when the plugin is active", async () => {
    vi.mocked(fetchSemanticCapability).mockResolvedValue([
      { libraryID: "l1", pluginActive: true, state: "ready" },
    ]);
    renderPage();
    expect(await screen.findByLabelText("Search mode")).toBeInTheDocument();
  });

  it("searches as the user types and renders results with a count", async () => {
    const user = userEvent.setup();
    renderPage();
    expect(screen.queryByRole("heading", { name: "Search results" })).toBeNull();
    await user.type(screen.getByLabelText("Search"), "hello");
    expect(await screen.findByText("Hit")).toBeInTheDocument();
    expect(searchResult).toHaveBeenLastCalledWith("hello", "keyword", expect.any(AbortSignal));
    expect(screen.getByRole("heading", { name: "Search results" })).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("1 result for “hello”");
  });

  it("runs the search named in the URL and clears it", async () => {
    const user = userEvent.setup();
    renderPage("/search?q=Hit");
    expect(screen.getByLabelText("Search")).toHaveValue("Hit");
    expect(await screen.findByText("Hit")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Clear search" }));
    expect(screen.getByLabelText("Search")).toHaveValue("");
    expect(screen.getByText("Search across your libraries")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Search results" })).toBeNull();
  });

  it("renders restricted-result empties exactly like normal no-match empties", async () => {
    vi.mocked(searchResult).mockResolvedValue({
      mode: "keyword",
      items: [],
      restrictedResultsPresent: true,
    });
    const user = userEvent.setup();
    renderPage();
    await user.type(screen.getByLabelText("Search"), "x");
    expect(await screen.findByText("No results for “x”")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(
      "No results for “x”Check the spelling or try fewer words.Clear search",
    );
    expect(screen.queryByText(/hidden by your access settings/i)).toBeNull();
  });

  it("surfaces the keyword-fallback notice when semantic degrades", async () => {
    vi.mocked(fetchSemanticCapability).mockResolvedValue([
      { libraryID: "l1", pluginActive: true, state: "empty" },
    ]);
    vi.mocked(searchResult).mockResolvedValue({
      mode: "keyword",
      items: [],
      restrictedResultsPresent: false,
    });
    const user = userEvent.setup();
    renderPage();
    await screen.findByLabelText("Search mode");
    // Select semantic, then search.
    await user.click(screen.getByLabelText("Search mode"));
    await user.click(await screen.findByRole("option", { name: "Semantic" }));
    await user.type(screen.getByLabelText("Search"), "x");
    expect(await screen.findByText(/showing keyword results/i)).toBeInTheDocument();
    expect(searchResult).toHaveBeenLastCalledWith("x", "semantic", expect.any(AbortSignal));
  });

  it("cancels a slower previous search and ignores its results", async () => {
    const user = userEvent.setup();
    let resolveSlow: (value: Awaited<ReturnType<typeof searchResult>>) => void = () => {};
    let slowSignal: AbortSignal | undefined;
    vi.mocked(searchResult).mockImplementation((query, _mode, signal) => {
      if (query === "slow") {
        slowSignal = signal;
        return new Promise((resolve) => {
          resolveSlow = resolve;
        });
      }
      return Promise.resolve({
        mode: "keyword",
        items: [{ id: "fast", kind: "media", title: "Fast", mode: "keyword", score: 0 }],
        restrictedResultsPresent: false,
      });
    });

    renderPage();
    const input = screen.getByLabelText("Search");
    await user.type(input, "slow");
    await waitFor(() => expect(slowSignal).toBeDefined());
    await user.clear(input);
    await user.type(input, "fast");
    expect(await screen.findByText("Fast")).toBeInTheDocument();
    expect(slowSignal?.aborted).toBe(true);

    resolveSlow({
      mode: "keyword",
      items: [{ id: "slow", kind: "media", title: "Slow", mode: "keyword", score: 0 }],
      restrictedResultsPresent: false,
    });
    await waitFor(() => expect(screen.queryByText("Slow")).toBeNull());
    expect(screen.getByText("Fast")).toBeInTheDocument();
  });

  it("hands the playable results to each card as the player's queue (CORE-1143)", async () => {
    vi.mocked(searchResult).mockResolvedValue({
      mode: "keyword",
      items: [
        { id: "m1", kind: "media", title: "One", mode: "keyword", score: 0 },
        { id: "e1", kind: "entity", title: "Album", mode: "keyword", score: 0 },
        { id: "m2", kind: "media", title: "Two", mode: "keyword", score: 0 },
      ],
      restrictedResultsPresent: false,
    });
    const user = userEvent.setup();
    renderPage();
    await user.type(screen.getByLabelText("Search"), "hello");
    expect(await screen.findByText("One")).toHaveAttribute("data-queue", "m1,m2");
    expect(screen.getByText("Album")).toHaveAttribute("data-queue", "m1,m2");
  });

  it("refreshes the shown results in place when engagement changes", async () => {
    const user = userEvent.setup();
    renderPage();
    act(() => notifyEngagementChanged());
    expect(searchResult).not.toHaveBeenCalled();

    await user.type(screen.getByLabelText("Search"), "hello");
    expect(await screen.findByText("Hit")).toBeInTheDocument();

    vi.mocked(searchResult).mockResolvedValue({
      mode: "keyword",
      items: [{ id: "m1", kind: "media", title: "Hit, Finished", mode: "keyword", score: 0 }],
      restrictedResultsPresent: false,
    });
    act(() => notifyEngagementChanged());

    expect(await screen.findByText("Hit, Finished")).toBeInTheDocument();
    expect(vi.mocked(searchResult).mock.calls.at(-1)?.slice(0, 2)).toEqual(["hello", "keyword"]);
  });
});
