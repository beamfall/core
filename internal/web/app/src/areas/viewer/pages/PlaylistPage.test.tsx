import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as api from "@/lib/api";
import { fetchCollections } from "@/lib/graphql";
import { fetchMediaRows } from "@/lib/graphql/browse";
import type { CollectionInfo, GenericMediaInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { fetchEntryLocks } from "../lib/collections";
import { resetQueueCoordinatorForTest } from "../lib/musicQueue";
import { type PlayerNavState, resetPlayNextForTest } from "../lib/watchQueue";
import { PlaylistPage } from "./PlaylistPage";

vi.mock("@/lib/session", () => ({
  useSession: () => ({
    isManager: false,
    session: { profileID: "profile-a", revealed: true },
    manifest: {
      libraries: [{ id: "lib-a", title: "Sounds", pluginID: null, previewClips: false }],
    },
  }),
  useViewerIdentity: () => "viewer-1",
}));
vi.mock("@/lib/graphql", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/graphql")>();
  return { ...actual, fetchCollections: vi.fn() };
});
vi.mock("@/lib/graphql/browse", () => ({ fetchMediaRows: vi.fn() }));
vi.mock("../lib/collections", () => ({ fetchEntryLocks: vi.fn() }));
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));

const playlist: CollectionInfo = {
  id: "pl-1",
  libraryID: "lib-a",
  name: "Evening list",
  sortOrder: 0,
  memberCount: 3,
  members: [
    { mediaID: "m-1", sortOrder: 0 },
    { mediaID: "m-2", sortOrder: 1 },
    { mediaID: "m-3", sortOrder: 2 },
  ],
};

function row(id: string, title: string): GenericMediaInfo {
  return { id, title, durationSeconds: 60 } as GenericMediaInfo;
}

function WatchProbe() {
  const location = useLocation();
  const state = location.state as PlayerNavState | null;
  return (
    <p data-testid="watch">
      {location.pathname}|{(state?.queue ?? []).map((item) => item.id).join(",")}
    </p>
  );
}

function renderPage() {
  render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/playlists/pl-1"]}>
        <Routes>
          <Route path="/playlists/:playlistId" element={<PlaylistPage />} />
          <Route path="/watch/*" element={<WatchProbe />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.mocked(fetchCollections).mockResolvedValue([playlist] as never);
  vi.mocked(fetchMediaRows).mockResolvedValue({
    "m-1": row("m-1", "First tone"),
    "m-2": row("m-2", "Second tone"),
    "m-3": row("m-3", "Third tone"),
  } as never);
});

afterEach(() => {
  cleanup();
  vi.mocked(fetchCollections).mockReset();
  vi.mocked(fetchMediaRows).mockReset();
  vi.mocked(fetchEntryLocks).mockReset();
});

describe("PlaylistPage locked entries (CORE-1876)", () => {
  it("lists a locked entry as unplayable and leaves it out of the Play all queue", async () => {
    vi.mocked(fetchEntryLocks).mockResolvedValue(
      new Map([
        ["m-1", false],
        ["m-2", true],
        ["m-3", false],
      ]),
    );
    renderPage();

    const locked = await screen.findByRole("button", { name: "Play Second tone" });
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Play First tone" })).toBeEnabled(),
    );
    expect(locked).toBeDisabled();
    expect(fetchEntryLocks).toHaveBeenCalledWith("pl-1", 3, expect.any(AbortSignal));

    await userEvent.click(screen.getByRole("button", { name: "Play all" }));
    expect(screen.getByTestId("watch")).toHaveTextContent("/watch/m-1|m-1,m-3");
  });

  it("fails closed on a member with no lock state (CORE-2843)", async () => {
    // The lock feed drops muted entries, so m-2 may or may not be locked.
    vi.mocked(fetchEntryLocks).mockResolvedValue(
      new Map([
        ["m-1", false],
        ["m-3", false],
      ]),
    );
    renderPage();

    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Play First tone" })).toBeEnabled(),
    );
    expect(screen.getByRole("button", { name: "Play Second tone" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Play all" }));
    expect(screen.getByTestId("watch")).toHaveTextContent("/watch/m-1|m-1,m-3");
  });

  it("plays nothing when the lock read fails (CORE-2843)", async () => {
    vi.mocked(fetchEntryLocks).mockRejectedValue(new Error("offline"));
    renderPage();

    const first = await screen.findByRole("button", { name: "Play First tone" });
    await waitFor(() => expect(fetchEntryLocks).toHaveBeenCalled());
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(first).toBeDisabled();
    expect(screen.getByRole("button", { name: "Play Second tone" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Play Third tone" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Play all" })).toBeNull();
  });

  it("plays nothing until the lock state is known", async () => {
    vi.mocked(fetchEntryLocks).mockReturnValue(new Promise(() => {}));
    renderPage();

    expect(await screen.findByRole("button", { name: "Play First tone" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Play all" })).toBeNull();
  });
});

describe("PlaylistPage CORE-2845 row queue actions and lock safety", () => {
  afterEach(() => {
    resetPlayNextForTest();
    resetQueueCoordinatorForTest();
    vi.restoreAllMocks();
  });

  it("acknowledges Play next and Add to queue on unlocked rows only after server confirmation and surfaces errors", async () => {
    vi.mocked(fetchEntryLocks).mockResolvedValue(
      new Map([
        ["m-1", false],
        ["m-2", false],
        ["m-3", false],
      ]),
    );
    let releaseWrite!: () => void;
    let shouldFail = false;
    let ids = ["other-0"];
    const graphql = vi.spyOn(api, "graphql").mockImplementation(async (query, variables) => {
      if (query.startsWith("query MusicQueue")) {
        return {
          currentSession: { profileID: "profile-a", revealed: true },
          sourceItems: ids.map((id) => ({ id, title: `Title ${id}`, mediaClass: "audio" })),
          playQueue: {
            currentEntryID: "e-0",
            entries: ids.map((id, i) => ({ id: `e-${i}`, mediaID: id })),
          },
        };
      }
      if (query.startsWith("mutation QueueAddAt")) {
        if (shouldFail) throw new Error("server down");
        await new Promise<void>((resolve) => {
          releaseWrite = resolve;
        });
        const mediaID = String((variables as Record<string, unknown>).mediaID);
        ids = [...ids, mediaID];
        return { queueAdd: { id: mediaID } };
      }
      return {};
    });

    const user = userEvent.setup();
    renderPage();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Play First tone" })).toBeEnabled(),
    );

    await user.click(screen.getByRole("button", { name: "More actions for First tone" }));
    await user.click(await screen.findByRole("menuitem", { name: "Play next" }));

    await waitFor(() =>
      expect(graphql).toHaveBeenCalledWith(
        expect.stringContaining("mutation QueueAddAt"),
        expect.objectContaining({ mediaID: "m-1", position: 1, expectedProfileID: "profile-a" }),
      ),
    );
    expect(screen.queryByRole("status")).toBeNull();

    releaseWrite();
    expect(await screen.findByRole("status")).toHaveTextContent("First tone plays next.");

    shouldFail = true;
    await user.click(screen.getByRole("button", { name: "More actions for Third tone" }));
    await user.click(await screen.findByRole("menuitem", { name: "Add to queue" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not update the Play Queue.");
  });

  it("hides Play next and Add to queue when lock state is pending, rejected, missing, or locked", async () => {
    const user = userEvent.setup();

    // 1. Pending locks
    vi.mocked(fetchEntryLocks).mockReturnValue(new Promise(() => {}));
    renderPage();
    await user.click(await screen.findByRole("button", { name: "More actions for First tone" }));
    await screen.findByRole("menuitem", { name: "Copy link" });
    expect(screen.queryByRole("menuitem", { name: "Play next" })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: "Add to queue" })).toBeNull();
    cleanup();

    // 2. Rejected lock read
    vi.mocked(fetchEntryLocks).mockRejectedValue(new Error("offline"));
    renderPage();
    await waitFor(() => expect(fetchEntryLocks).toHaveBeenCalled());
    await user.click(await screen.findByRole("button", { name: "More actions for First tone" }));
    await screen.findByRole("menuitem", { name: "Copy link" });
    expect(screen.queryByRole("menuitem", { name: "Play next" })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: "Add to queue" })).toBeNull();
    cleanup();

    // 3. Missing member (unknown) & locked member (true) vs unlocked member (false)
    vi.mocked(fetchEntryLocks).mockResolvedValue(
      new Map([
        ["m-1", false],
        ["m-3", true],
      ]),
    );
    renderPage();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Play First tone" })).toBeEnabled(),
    );

    await user.click(screen.getByRole("button", { name: "More actions for Second tone" }));
    await screen.findByRole("menuitem", { name: "Copy link" });
    expect(screen.queryByRole("menuitem", { name: "Play next" })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: "Add to queue" })).toBeNull();

    await user.click(screen.getByRole("button", { name: "More actions for Third tone" }));
    await screen.findByRole("menuitem", { name: "Copy link" });
    expect(screen.queryByRole("menuitem", { name: "Play next" })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: "Add to queue" })).toBeNull();

    await user.click(screen.getByRole("button", { name: "More actions for First tone" }));
    expect(await screen.findByRole("menuitem", { name: "Play next" })).toBeInTheDocument();
    expect(await screen.findByRole("menuitem", { name: "Add to queue" })).toBeInTheDocument();
  });
});
