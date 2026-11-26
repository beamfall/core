import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as api from "@/lib/api";
import { ApiError, setExpectedProfile } from "@/lib/api";
import {
  addCollectionMedia,
  clearMute,
  fetchCollections,
  fetchItemState,
  fetchMedia,
  fetchMutes,
  fetchTagAssignments,
  setContinueWatchingHidden,
  setMute,
} from "@/lib/graphql";
import type { MediaCredit } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import type { Locale } from "@/strings";
import { resetQueueCoordinatorForTest } from "../lib/musicQueue";
import {
  resetPlayNextForTest,
  setActivePlayingMediaID,
  setActivePlayNextHandler,
} from "../lib/watchQueue";
import { MediaItemCardMenu } from "./MediaItemCardMenu";

vi.mock("@/lib/graphql", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql")>()),
  addCollectionMedia: vi.fn(),
  fetchMutes: vi.fn(async () => []),
  setMute: vi.fn(async () => undefined),
  clearMute: vi.fn(async () => undefined),
  clearAllMutes: vi.fn(async () => 0),
  fetchCollections: vi.fn(),
  fetchItemState: vi.fn(),
  fetchMedia: vi.fn(),
  fetchTagAssignments: vi.fn(),
  setContinueWatchingHidden: vi.fn(),
}));

vi.mock("@/lib/session", () => ({
  useViewerIdentity: () => "viewer-1",
  useSession: () => ({ manifest: { libraries: [{ id: "lib-1" }] }, isManager: true }),
}));

beforeEach(() => {
  vi.mocked(fetchMedia).mockReset().mockResolvedValue(null);
  vi.mocked(fetchItemState).mockReset().mockResolvedValue({
    id: "t1",
    kind: "media",
    watched: false,
    watchlisted: false,
    rating: 0,
  });
  vi.mocked(fetchCollections)
    .mockReset()
    .mockResolvedValue([
      {
        id: "p1",
        libraryID: "lib-1",
        name: "Test List",
        sortOrder: 0,
        memberCount: 0,
        members: [],
      },
    ]);
  vi.mocked(fetchTagAssignments).mockReset().mockResolvedValue({
    targetType: "media",
    targetID: "t1",
    libraryID: "lib-1",
    tags: [],
  });
  vi.mocked(addCollectionMedia)
    .mockReset()
    .mockResolvedValue(undefined as never);
});

afterEach(cleanup);

const artist: MediaCredit = {
  entityID: "artist/adele",
  title: "Adele",
  archetype: "person",
  kind: "performer",
  relation: "credit",
};
const album: MediaCredit = {
  entityID: "record-21",
  title: "21",
  archetype: "container",
  kind: "record",
  relation: "credit",
};

function CurrentPath() {
  return <output aria-label="Current path">{useLocation().pathname}</output>;
}

function renderMenu(
  isAudio: boolean,
  onDetails?: () => void,
  locale: Locale = "en",
  credits?: readonly MediaCredit[],
  continueWatching = false,
  onReveal?: () => void,
) {
  render(
    <I18nProvider initialLocale={locale}>
      <MemoryRouter>
        <MediaItemCardMenu
          title="Track One"
          primaryLabel="Play"
          isMedia
          isAudio={isAudio}
          credits={credits}
          href="https://example.test/watch/t1"
          target={{ mediaID: "t1" }}
          onPrimary={() => {}}
          onDetails={onDetails}
          continueWatching={continueWatching}
          onReveal={onReveal}
        />
        <CurrentPath />
      </MemoryRouter>
    </I18nProvider>,
  );
  expect(fetchMedia).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button"));
}

describe("MediaItemCardMenu wording", () => {
  it.each([
    ["en", "Details"],
    ["fr", "Détails"],
    ["de", "Details"],
    ["es", "Detalles"],
    ["pt", "Detalhes"],
  ] as const)("localizes the detail action in %s", async (locale, label) => {
    const details = vi.fn();
    renderMenu(false, details, locale);
    fireEvent.click(await screen.findByRole("menuitem", { name: label }));
    expect(details).toHaveBeenCalledOnce();
  });

  it("offers Reveal details only for a guarded item (CORE-3057)", async () => {
    const reveal = vi.fn();
    renderMenu(false, undefined, "en", undefined, false, reveal);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Reveal details" }));
    expect(reveal).toHaveBeenCalledOnce();
    cleanup();
    renderMenu(false);
    await screen.findByRole("menuitem", { name: "Mark watched" });
    expect(screen.queryByRole("menuitem", { name: "Reveal details" })).toBeNull();
  });

  it("uses listening wording for audio Media", async () => {
    renderMenu(true);
    expect(await screen.findByRole("menuitem", { name: "Mark played" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Mark watched" })).not.toBeInTheDocument();
  });

  it("keeps watching wording for other Media", async () => {
    renderMenu(false);
    expect(await screen.findByRole("menuitem", { name: "Mark watched" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Mark played" })).not.toBeInTheDocument();
  });

  it.each([
    ["Go to artist", "/entity/artist%2Fadele"],
    ["Go to album", "/entity/record-21"],
  ])("navigates with %s using credits already loaded by the listing", async (label, path) => {
    renderMenu(true, undefined, "en", [artist, album]);
    const link = await screen.findByRole("menuitem", { name: label });
    expect(link).toHaveAttribute("href", path);
    fireEvent.click(link);
    expect(screen.getByLabelText("Current path")).toHaveTextContent(path);
    expect(fetchMedia).not.toHaveBeenCalled();
  });

  it("loads credits when an audio card opens and omits an unlinked destination", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({
      id: "t1",
      title: "Track One",
      favorited: false,
      mediaClass: "audio",
      credits: [artist],
    });
    renderMenu(true);
    expect(await screen.findByRole("menuitem", { name: "Go to artist" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Go to album" })).toBeNull();
    expect(fetchMedia).toHaveBeenCalledWith("t1", expect.any(AbortSignal));
  });

  it("keeps video menus free of audio destinations", async () => {
    renderMenu(false, undefined, "en", [artist, album]);
    await screen.findByRole("menuitem", { name: "Mark watched" });
    expect(screen.queryByRole("menuitem", { name: /^Go to/ })).toBeNull();
    expect(fetchMedia).not.toHaveBeenCalled();
  });

  it("offers Start radio from audio Media only (CORE-2772)", async () => {
    renderMenu(true, undefined, "en", [artist, album]);
    expect(await screen.findByRole("menuitem", { name: "Start radio" })).toBeInTheDocument();
    cleanup();
    renderMenu(false);
    await screen.findByRole("menuitem", { name: "Mark watched" });
    expect(screen.queryByRole("menuitem", { name: "Start radio" })).toBeNull();
  });

  it("adds the Media to a playlist from the menu", async () => {
    renderMenu(true);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Add to playlist" }));
    fireEvent.click(await screen.findByRole("button", { name: /Test List/ }));
    await vi.waitFor(() => expect(addCollectionMedia).toHaveBeenCalledWith("p1", ["t1"]));
    await vi.waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Test List"));
  });
});

describe("MediaItemCardMenu hide (CORE-2836)", () => {
  beforeEach(() => {
    vi.mocked(setMute).mockClear();
    vi.mocked(clearMute).mockClear();
  });

  it("hides the item for this Profile through a media Mute", async () => {
    vi.mocked(fetchMutes).mockResolvedValue([]);
    renderMenu(false);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Hide" }));
    await vi.waitFor(() => expect(setMute).toHaveBeenCalledWith("media", "t1"));
  });

  it("offers Unhide for an item the Profile already hid", async () => {
    vi.mocked(fetchMutes).mockResolvedValue([
      { kind: "media", targetID: "t1", includeDescendants: false, mutedAt: "2026-11-20T00:00:00Z" },
    ]);
    renderMenu(false);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Unhide" }));
    await vi.waitFor(() => expect(clearMute).toHaveBeenCalledWith("media", "t1"));
  });
});

describe("MediaItemCardMenu Continue Watching (CORE-2017)", () => {
  beforeEach(() => {
    vi.mocked(setContinueWatchingHidden).mockReset().mockResolvedValue({
      id: "t1",
      kind: "media",
      watched: false,
      watchlisted: false,
      rating: 0,
    });
  });

  it("removes the Media from the row and keeps its progress", async () => {
    renderMenu(false, undefined, "en", undefined, true);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Remove from Continue Watching" }));
    await vi.waitFor(() =>
      expect(setContinueWatchingHidden).toHaveBeenCalledWith("t1", true, undefined),
    );
  });

  it("is offered only on the Continue Watching row", async () => {
    renderMenu(false);
    await screen.findByRole("menuitem", { name: "Mark watched" });
    expect(screen.queryByRole("menuitem", { name: "Remove from Continue Watching" })).toBeNull();
  });
});

function deferredPromise<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("MediaItemCardMenu queue actions (CORE-2845)", () => {
  afterEach(() => {
    resetPlayNextForTest();
    resetQueueCoordinatorForTest();
    setExpectedProfile(undefined);
    vi.restoreAllMocks();
  });

  function installServerQueueApi(
    initialIDs: string[] = ["current-1"],
    onMutation?: (
      op: "QueueAddAt" | "QueueNext",
      vars: Record<string, unknown>,
    ) => Promise<unknown>,
  ) {
    let ids = [...initialIDs];
    const snapshot = () => ({
      currentSession: { profileID: "profile-a", revealed: true },
      sourceItems: ids.map((id) => ({ id, title: `Title ${id}`, mediaClass: "audio" })),
      playQueue: {
        currentEntryID: ids[0] ? `e-0-${ids[0]}` : null,
        entries: ids.map((id, idx) => ({ id: `e-${idx}-${id}`, mediaID: id })),
      },
    });
    return vi.spyOn(api, "graphql").mockImplementation(async (query, variables) => {
      const vars = (variables ?? {}) as Record<string, unknown>;
      if (query.startsWith("query MusicQueue")) {
        return snapshot();
      }
      if (query.startsWith("mutation QueueAddAt")) {
        if (onMutation) {
          const override = await onMutation("QueueAddAt", vars);
          if (override !== undefined) return override;
        }
        const mediaID = String(vars.mediaID);
        const position = typeof vars.position === "number" ? vars.position : ids.length;
        ids = ids.filter((id) => id !== mediaID);
        ids.splice(position, 0, mediaID);
        return { queueAdd: { id: mediaID } };
      }
      if (query.startsWith("mutation QueueNext")) {
        if (onMutation) {
          const override = await onMutation("QueueNext", vars);
          if (override !== undefined) return override;
        }
        return { queueAddMany: { currentEntryID: snapshot().playQueue.currentEntryID } };
      }
      return {};
    });
  }

  it("acknowledges Play next only after confirmed server state and passes expectedProfileID", async () => {
    setExpectedProfile("profile-a");
    const gate = deferredPromise<unknown>();
    const graphql = installServerQueueApi(["current-1"], async () => {
      await gate.promise;
      return undefined;
    });

    renderMenu(true);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Play next" }));

    await vi.waitFor(() =>
      expect(graphql).toHaveBeenCalledWith(
        expect.stringContaining("mutation QueueAddAt"),
        expect.objectContaining({ mediaID: "t1", position: 1, expectedProfileID: "profile-a" }),
      ),
    );
    expect(document.querySelector(".bf-card-action-status")).toBeNull();

    gate.resolve(undefined);
    expect(await screen.findByText("Track One plays next.")).toHaveAttribute("role", "status");
  });

  it("acknowledges Add to queue only after server confirmation", async () => {
    setExpectedProfile("profile-a");
    const gate = deferredPromise<unknown>();
    installServerQueueApi(["current-1", "other-2"], async () => {
      await gate.promise;
      return undefined;
    });

    renderMenu(true);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Add to queue" }));
    expect(document.querySelector(".bf-card-action-status")).toBeNull();

    gate.resolve(undefined);
    expect(await screen.findByText("Track One added to the queue.")).toHaveAttribute(
      "role",
      "status",
    );
  });

  it("routes active-player edits with placement and reports failures with role=alert", async () => {
    const gate = deferredPromise<boolean>();
    const handler = vi.fn(async () => gate.promise);
    setActivePlayNextHandler(handler);

    renderMenu(true);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Add to queue" }));

    await vi.waitFor(() =>
      expect(handler).toHaveBeenCalledWith(
        { id: "t1", title: "Track One", mediaClass: "audio" },
        "viewer-1",
        "end",
      ),
    );
    expect(document.querySelector(".bf-card-action-status")).toBeNull();

    gate.resolve(true);
    expect(await screen.findByText("Track One added to the queue.")).toHaveAttribute(
      "role",
      "status",
    );

    handler.mockRejectedValueOnce(
      new ApiError("Profile changed", 409, {
        errors: [{ message: "mismatch", extensions: { reason: "PROFILE_MISMATCH" } }],
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: "More actions for Track One" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Play next" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Could not update the Play Queue.");
    expect(document.querySelector(".bf-card-action-status")).toBeNull();
  });

  it("disables Play next and Add to queue for the currently playing track", async () => {
    setActivePlayingMediaID("t1");
    renderMenu(true);

    const playNext = await screen.findByRole("menuitem", { name: "Play next" });
    const addToQueue = await screen.findByRole("menuitem", { name: "Add to queue" });
    expect(playNext).toHaveAttribute("aria-disabled", "true");
    expect(addToQueue).toHaveAttribute("aria-disabled", "true");
  });

  it("hides Play next and Add to queue when lock state is unknown, locked, or item is an image", async () => {
    const cases = [
      {
        title: "Unknown Track",
        props: { isAudio: true, lockState: "unknown" as const },
        target: { mediaID: "u1" },
      },
      {
        title: "Locked Track",
        props: { isAudio: true, locked: true },
        target: { mediaID: "l1" },
      },
      {
        title: "Cover Photo",
        props: { mediaClass: "image" },
        target: { mediaID: "i1" },
      },
    ];

    for (const c of cases) {
      const view = render(
        <I18nProvider>
          <MemoryRouter>
            <MediaItemCardMenu
              title={c.title}
              primaryLabel="Play"
              isMedia
              href={`https://example.test/watch/${c.target.mediaID}`}
              target={c.target}
              {...c.props}
            />
          </MemoryRouter>
        </I18nProvider>,
      );
      fireEvent.click(screen.getByRole("button", { name: `More actions for ${c.title}` }));
      await screen.findByRole("menuitem", { name: "Copy link" });
      expect(screen.queryByRole("menuitem", { name: "Play next" })).toBeNull();
      expect(screen.queryByRole("menuitem", { name: "Add to queue" })).toBeNull();
      view.unmount();
    }
  });
});
