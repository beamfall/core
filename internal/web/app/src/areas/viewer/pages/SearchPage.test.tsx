import type { ReactNode } from "react";
import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchClientPrefs, fetchSemanticCapability, searchResult } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { SearchPage } from "./SearchPage";

vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <>{children}</>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));
vi.mock("../components/MediaItemCard", () => ({
  MediaItemCard: ({ item }: { item: { title: string } }) => <li>{item.title}</li>,
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

describe("SearchPage header query", () => {
  it("runs the search the header field opened the page with", async () => {
    renderPage("/search?q=blue%20planet");
    await waitFor(() => expect(searchResult).toHaveBeenCalledWith("blue planet", "keyword"));
    expect(screen.getByLabelText("Search")).toHaveValue("blue planet");
    expect(await screen.findByText("Hit")).toBeInTheDocument();
  });

  // The real header field beside the page, as AppShell renders it.
  async function renderWithHeaderSearch() {
    const { ViewerActions: HeaderSearch } = await vi.importActual<
      typeof import("../components/ViewerActions")
    >("../components/ViewerActions");
    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/search"]}>
          <HeaderSearch />
          <SearchPage />
        </MemoryRouter>
      </I18nProvider>,
    );
    const pageForm = screen.getByRole("button", { name: "Clear" }).closest("form");
    if (!pageForm) throw new Error("search form missing");
    return {
      headerField: within(screen.getByRole("search")).getByLabelText("Search"),
      pageField: within(pageForm).getByLabelText("Search"),
    };
  }

  it("runs the same header query again after Clear", async () => {
    const user = userEvent.setup();
    const { headerField, pageField } = await renderWithHeaderSearch();

    await user.type(headerField, "blue planet{Enter}");
    expect(await screen.findByText("Hit")).toBeInTheDocument();
    expect(searchResult).toHaveBeenCalledTimes(1);

    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(screen.queryByText("Hit")).toBeNull();
    expect(pageField).toHaveValue("");

    await user.type(headerField, "blue planet{Enter}");
    expect(await screen.findByText("Hit")).toBeInTheDocument();
    expect(searchResult).toHaveBeenCalledTimes(2);
    expect(searchResult).toHaveBeenLastCalledWith("blue planet", "keyword");
    expect(pageField).toHaveValue("blue planet");
  });

  it("runs the same header query again after a search typed on the page", async () => {
    const user = userEvent.setup();
    const { headerField, pageField } = await renderWithHeaderSearch();

    await user.type(headerField, "blue planet{Enter}");
    await waitFor(() => expect(searchResult).toHaveBeenCalledTimes(1));

    await user.clear(pageField);
    await user.type(pageField, "other{Enter}");
    await waitFor(() => expect(searchResult).toHaveBeenLastCalledWith("other", "keyword"));

    await user.type(headerField, "blue planet{Enter}");
    await waitFor(() => expect(searchResult).toHaveBeenCalledTimes(3));
    expect(searchResult).toHaveBeenLastCalledWith("blue planet", "keyword");
    expect(pageField).toHaveValue("blue planet");
  });
});

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

  it("runs a keyword search through searchResult and renders results", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.type(screen.getByLabelText("Search"), "hello");
    await user.click(screen.getByRole("button", { name: "Search" }));
    expect(searchResult).toHaveBeenCalledWith("hello", "keyword");
    expect(await screen.findByText("Hit")).toBeInTheDocument();
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
    await user.click(screen.getByRole("button", { name: "Search" }));
    expect(await screen.findByText("No matches.")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(
      "No matches.Try a different title or keyword.",
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
    await user.click(screen.getByRole("button", { name: "Search" }));
    expect(searchResult).toHaveBeenCalledWith("x", "semantic");
    expect(await screen.findByText(/showing keyword results/i)).toBeInTheDocument();
  });

  it("ignores stale results from a slower previous search", async () => {
    const user = userEvent.setup();
    let resolveSlow: (value: Awaited<ReturnType<typeof searchResult>>) => void = () => {};
    vi.mocked(searchResult).mockImplementation((query) => {
      if (query === "slow") {
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
    const form = screen.getByRole("button", { name: "Search" }).closest("form");
    if (!form) throw new Error("search form missing");

    await user.type(input, "slow");
    fireEvent.submit(form);
    await user.clear(input);
    await user.type(input, "fast");
    fireEvent.submit(form);
    expect(await screen.findByText("Fast")).toBeInTheDocument();

    resolveSlow({
      mode: "keyword",
      items: [{ id: "slow", kind: "media", title: "Slow", mode: "keyword", score: 0 }],
      restrictedResultsPresent: false,
    });
    await waitFor(() => expect(screen.queryByText("Slow")).toBeNull());
    expect(screen.getByText("Fast")).toBeInTheDocument();
  });
});
