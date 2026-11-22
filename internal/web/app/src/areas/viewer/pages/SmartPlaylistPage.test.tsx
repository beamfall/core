import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  fetchSmartCollectionItems,
  fetchSmartCollections,
  updateSmartCollection,
} from "@/lib/graphql";
import type { SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import type { PlayerNavState } from "../lib/watchQueue";
import { SmartPlaylistPage } from "./SmartPlaylistPage";

const session = vi.hoisted(() => ({ isManager: false }));

vi.mock("@/lib/session", () => ({
  useSession: () => ({
    isManager: session.isManager,
    manifest: {
      libraries: [{ id: "lib-a", title: "Sounds", pluginID: null, previewClips: false }],
      layouts: [],
    },
  }),
  useViewerIdentity: () => "viewer-1",
}));
vi.mock("@/lib/graphql", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/graphql")>();
  return {
    ...actual,
    fetchSmartCollections: vi.fn(),
    fetchSmartCollectionItems: vi.fn(),
    updateSmartCollection: vi.fn(),
  };
});
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));

const RULES = '{"kind":"rules","match":"all","rules":[{"field":"playCount","op":"gte","value":2}]}';

function item(id: string, title: string, locked = false): SourceItem {
  return { id, kind: "", title, durationSeconds: 90, locked } as SourceItem;
}

function WatchProbe() {
  const location = useLocation();
  const state = location.state as PlayerNavState | null;
  return (
    <p data-testid="watch">
      {location.pathname}|{(state?.queue ?? []).map((entry) => entry.id).join(",")}
    </p>
  );
}

function renderPage() {
  render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/playlists/smart/smart-1"]}>
        <Routes>
          <Route path="/playlists/smart/:smartId" element={<SmartPlaylistPage />} />
          <Route path="/watch/*" element={<WatchProbe />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  session.isManager = false;
  vi.mocked(fetchSmartCollections).mockResolvedValue([
    {
      id: "smart-1",
      libraryID: "lib-a",
      name: "Played often",
      sourceExpr: RULES,
      sortExpr: '{"by":"playCount","dir":"desc"}',
    },
  ]);
  vi.mocked(fetchSmartCollectionItems).mockResolvedValue([
    item("m-1", "First tone"),
    item("m-2", "Second tone", true),
    item("m-3", "Third tone"),
  ]);
});

afterEach(() => {
  cleanup();
  vi.mocked(fetchSmartCollections).mockReset();
  vi.mocked(fetchSmartCollectionItems).mockReset();
  vi.mocked(updateSmartCollection).mockReset();
});

// feature:collection-editor (CORE-2771 smart playlists)
describe("SmartPlaylistPage", () => {
  it("lists the rule matches and plays them, leaving locked items out of the queue", async () => {
    renderPage();

    expect(await screen.findByRole("heading", { level: 1, name: "Played often" })).toBeVisible();
    expect(await screen.findByText("3 items")).toBeVisible();
    expect(screen.getByText("Updates automatically")).toBeVisible();
    expect(screen.getByRole("button", { name: "Play Second tone" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Edit rules" })).toBeNull();
    expect(fetchSmartCollectionItems).toHaveBeenCalledWith(
      "smart-1",
      200,
      0,
      expect.any(AbortSignal),
    );

    await userEvent.click(screen.getByRole("button", { name: "Play all" }));
    expect(screen.getByTestId("watch")).toHaveTextContent("/watch/m-1|m-1,m-3");
  });

  it("says when nothing matches and when the playlist is gone", async () => {
    vi.mocked(fetchSmartCollectionItems).mockResolvedValue([]);
    renderPage();
    expect(await screen.findByText("Nothing matches these rules right now.")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Play all" })).toBeNull();

    cleanup();
    vi.mocked(fetchSmartCollections).mockResolvedValue([]);
    renderPage();
    expect(await screen.findByText("This playlist isn't available.")).toBeVisible();
  });

  it("lets a manager edit the rules and rereads the matches", async () => {
    session.isManager = true;
    vi.mocked(updateSmartCollection).mockImplementation(async (input) => ({
      id: input.id,
      libraryID: "lib-a",
      name: input.name ?? "",
      sourceExpr: input.sourceExpr ?? "",
      sortExpr: input.sortExpr ?? "",
    }));
    renderPage();

    await userEvent.click(await screen.findByRole("button", { name: "Edit rules" }));
    const value = screen.getByRole("spinbutton", { name: "Value for rule 1" });
    expect(value).toHaveValue(2);
    await userEvent.clear(value);
    await userEvent.type(value, "5");
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Match" }), "any");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(updateSmartCollection).toHaveBeenCalled());
    expect(updateSmartCollection).toHaveBeenCalledWith({
      id: "smart-1",
      name: "Played often",
      sourceExpr:
        '{"kind":"rules","match":"any","rules":[{"field":"playCount","op":"gte","value":5}]}',
      sortExpr: '{"by":"playCount","dir":"desc"}',
    });
    await waitFor(() => expect(fetchSmartCollectionItems).toHaveBeenCalledTimes(2));
  });
});
