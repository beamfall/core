import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as api from "@/lib/api";
import { setExpectedProfile } from "@/lib/api";
import {
  fetchCollections,
  fetchItemState,
  fetchMedia,
  fetchMutes,
  fetchTagAssignments,
} from "@/lib/graphql";
import type { SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { resetQueueCoordinatorForTest } from "../lib/musicQueue";
import { resetPlayNextForTest, setActivePlayingMediaID } from "../lib/watchQueue";
import { TrackList } from "./TrackList";

vi.mock("@/lib/graphql", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql")>()),
  fetchCollections: vi.fn(),
  fetchItemState: vi.fn(),
  fetchMedia: vi.fn(),
  fetchMutes: vi.fn(),
  fetchTagAssignments: vi.fn(),
}));

vi.mock("@/lib/session", () => ({
  useViewerIdentity: () => "viewer-1",
  useSession: () => ({ manifest: { libraries: [{ id: "lib-1" }] }, isManager: false }),
}));

function makeTrack(id: string, title: string, locked = false): SourceItem {
  return {
    id,
    kind: "media",
    title,
    mediaClass: "audio",
    locked,
    durationSeconds: 180,
  } as unknown as SourceItem;
}

describe("TrackList queue actions (CORE-2845)", () => {
  beforeEach(() => {
    vi.mocked(fetchMedia).mockResolvedValue(null);
    vi.mocked(fetchItemState).mockResolvedValue({
      id: "t1",
      kind: "media",
      watched: false,
      watchlisted: false,
      rating: 0,
    });
    vi.mocked(fetchCollections).mockResolvedValue([]);
    vi.mocked(fetchMutes).mockResolvedValue([]);
    vi.mocked(fetchTagAssignments).mockResolvedValue({
      targetType: "media",
      targetID: "t1",
      libraryID: "lib-1",
      tags: [],
    });
  });

  afterEach(() => {
    cleanup();
    resetPlayNextForTest();
    resetQueueCoordinatorForTest();
    setExpectedProfile(undefined);
    vi.restoreAllMocks();
  });

  it("offers Play next and Add to queue on unlocked rows and disables the active track", async () => {
    setExpectedProfile("profile-a");
    let holdWrite!: () => void;
    let queueIDs = ["t1"];
    vi.spyOn(api, "graphql").mockImplementation(async (query, variables) => {
      if (query.startsWith("query MusicQueue")) {
        return {
          currentSession: { profileID: "profile-a", revealed: true },
          sourceItems: queueIDs.map((id) => ({ id, title: `Title ${id}`, mediaClass: "audio" })),
          playQueue: {
            currentEntryID: "e-0",
            entries: queueIDs.map((id, i) => ({ id: `e-${i}`, mediaID: id })),
          },
        };
      }
      if (query.startsWith("mutation QueueAddAt")) {
        await new Promise<void>((resolve) => {
          holdWrite = resolve;
        });
        const mediaID = String((variables as Record<string, unknown>).mediaID);
        queueIDs = [...queueIDs, mediaID];
        return { queueAdd: { id: mediaID } };
      }
      return {};
    });

    setActivePlayingMediaID("t1");
    render(
      <I18nProvider>
        <MemoryRouter>
          <TrackList
            label="Album tracks"
            queue={[
              { id: "t1", title: "Track One", mediaClass: "audio" },
              { id: "t2", title: "Track Two", mediaClass: "audio" },
            ]}
            rows={[
              { number: 1, item: makeTrack("t1", "Track One"), durationSeconds: 180 },
              { number: 2, item: makeTrack("t2", "Track Two"), durationSeconds: 200 },
              { number: 3, item: makeTrack("t3", "Locked Track", true), durationSeconds: 150 },
            ]}
          />
        </MemoryRouter>
      </I18nProvider>,
    );

    // Locked track exposes no action controls.
    expect(screen.queryByRole("button", { name: "More actions for Locked Track" })).toBeNull();

    // Active playing track has disabled queue actions.
    fireEvent.click(screen.getByRole("button", { name: "More actions for Track One" }));
    const playNextOne = await screen.findByRole("menuitem", { name: "Play next" });
    const addToQueueOne = await screen.findByRole("menuitem", { name: "Add to queue" });
    expect(playNextOne).toHaveAttribute("aria-disabled", "true");
    expect(addToQueueOne).toHaveAttribute("aria-disabled", "true");

    // Close the active track menu before opening Track Two's menu.
    fireEvent.keyDown(playNextOne, { key: "Escape" });
    await vi.waitFor(() => expect(screen.queryByRole("menu")).toBeNull());

    // Unlocked non-playing track waits for confirmed write before status appears.
    fireEvent.click(screen.getByRole("button", { name: "More actions for Track Two" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Add to queue" }));
    await vi.waitFor(() => expect(holdWrite).toBeTypeOf("function"));
    expect(screen.queryByRole("status")).toBeNull();

    holdWrite();
    expect(await screen.findByRole("status")).toHaveTextContent("Track Two added to the queue.");
  });
});
