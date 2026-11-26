import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { useEffect, useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { OFFERED_LOCALE_TAGS } from "@/l10n/locales";
import { I18nProvider } from "@/lib/i18n";
import { STRINGS } from "@/strings";
import { beginQueueAdvance, resetQueueCoordinatorForTest, useMusicQueue } from "../lib/musicQueue";
import { resetPlayNextForTest, setActivePlayingMediaID } from "../lib/watchQueue";
import { MusicQueuePanel } from "./MusicQueuePanel";

const viewer = vi.hoisted(() => ({
  profileID: "profile-a",
  revealed: true,
}));
vi.mock("@/lib/session", () => ({
  useSession: () => ({
    session: { profileID: viewer.profileID },
    revealed: viewer.revealed,
  }),
  useViewerIdentity: () => `${viewer.profileID}:${viewer.revealed}`,
}));

function QueueButton({ onPlay }: { onPlay: () => void }) {
  const [open, setOpen] = useState(false);
  const { view, refresh } = useMusicQueue();
  useEffect(() => {
    if (open) void refresh();
  }, [open, refresh]);
  return (
    <MusicQueuePanel
      open={open}
      onOpenChange={setOpen}
      currentID="track-202"
      onPlay={onPlay}
      onRetry={() => void refresh()}
      queue={view}
    />
  );
}

function renderQueuePanel(onPlay = vi.fn()) {
  render(
    <I18nProvider initialLocale="en">
      <QueueButton onPlay={onPlay} />
    </I18nProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: STRINGS.en.musicQueueTitle }));
  return onPlay;
}

function page(
  rows: object[],
  currentSession = { profileID: viewer.profileID, revealed: viewer.revealed },
) {
  return new Response(JSON.stringify({ data: { currentSession, sourceItems: rows } }));
}

afterEach(() => {
  vi.unstubAllGlobals();
  viewer.profileID = "profile-a";
  viewer.revealed = true;
});

describe("MusicQueuePanel", () => {
  it("reads all pages in server order and sends zero mutations when opened", async () => {
    const firstPage = Array.from({ length: 200 }, (_, i) => ({
      id: `track-${i + 1}`,
      title: `Track ${i + 1}`,
      mediaClass: "audio",
    }));
    firstPage[0] = { ...firstPage[0], title: "Film", mediaClass: "video" };
    const requests = vi
      .fn()
      .mockImplementationOnce(async () => page(firstPage))
      .mockImplementationOnce(async () =>
        page([
          { ...firstPage[199], locked: true },
          { id: "track-201", title: "Private title", mediaClass: "audio", locked: true },
          { id: "track-202", title: "Track 202", mediaClass: "audio" },
        ]),
      );
    vi.stubGlobal("fetch", requests);
    const onPlay = renderQueuePanel();
    const panel = screen.getByRole("dialog", { name: STRINGS.en.musicQueueTitle });
    expect(await within(panel).findByText("Track 202")).toHaveAttribute("aria-current", "true");
    const displayed = within(panel).getAllByRole("listitem");
    expect(displayed).toHaveLength(202);
    expect(displayed[0]).toHaveTextContent("Film");
    expect(displayed[199]).toHaveTextContent(STRINGS.en.musicQueueLockedItem);
    expect(displayed[200]).toHaveTextContent(STRINGS.en.musicQueueLockedItem);
    expect(screen.queryByText("Track 200")).toBeNull();
    expect(screen.queryByText("Private title")).toBeNull();
    expect(requests.mock.calls).toHaveLength(2);
    for (const [index, [path, request]] of requests.mock.calls.entries()) {
      const body = JSON.parse(request.body);
      expect(path).toBe("/api/graphql");
      expect(body.query).toMatch(/^query MusicQueue\b/);
      expect(body.query).toContain('sourceItems(kind: "queue"');
      expect(body.query).toContain("currentSession { profileID revealed");
      expect(body.variables).toEqual({ query: "queue", limit: 200, offset: index * 199 });
    }
    expect(onPlay).not.toHaveBeenCalled();
  });

  it("restarts all pages when a removal shifts the next page", async () => {
    const original = Array.from({ length: 202 }, (_, i) => ({
      id: `track-${i + 1}`,
      title: `Track ${i + 1}`,
      mediaClass: "audio",
    }));
    const shifted = original.slice(1);
    const requests = vi.fn(async (_path: string, request: RequestInit) => {
      const { limit, offset } = JSON.parse(String(request.body)).variables;
      const rows = requests.mock.calls.length === 1 ? original : shifted;
      return page(rows.slice(offset, offset + limit));
    });
    vi.stubGlobal("fetch", requests);
    renderQueuePanel();
    expect(await screen.findByText("Track 201")).toBeInTheDocument();
    expect(screen.queryByText("Track 1")).not.toBeInTheDocument();
    const titles = screen.getAllByRole("listitem").map((item) => item.textContent);
    expect(titles).toEqual(shifted.map((row) => row.title));
    expect(
      requests.mock.calls.map(([, request]) => JSON.parse(String(request.body)).variables.offset),
    ).toEqual([0, 199, 0, 199]);
  });

  it.each([
    ["profile switch", { profileID: "profile-b", revealed: true }],
    ["relock", { profileID: "profile-a", revealed: false }],
  ])("discards every page when a remote %s happens between pages", async (_, changedSession) => {
    const requests = vi
      .fn()
      .mockImplementationOnce(async () =>
        page(
          Array.from({ length: 200 }, (_, i) => ({
            id: `track-${i}`,
            title: `First page ${i}`,
            mediaClass: "audio",
          })),
        ),
      )
      .mockImplementationOnce(async () =>
        page([{ id: "other", title: "Other identity track", mediaClass: "audio" }], changedSession),
      );
    vi.stubGlobal("fetch", requests);
    const onPlay = renderQueuePanel();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      STRINGS.en.playerTerminalUnavailable,
    );
    expect(requests).toHaveBeenCalledTimes(2);
    expect(screen.queryByText("First page 0")).toBeNull();
    expect(screen.queryByText("Other identity track")).toBeNull();
    expect(screen.queryByRole("button", { name: STRINGS.en.musicQueuePlay })).toBeNull();
    expect(onPlay).not.toHaveBeenCalled();
  });

  it("shows the empty state and no Play action", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => page([])),
    );
    renderQueuePanel();
    expect(await screen.findByText(STRINGS.en.musicQueueEmpty)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: STRINGS.en.musicQueuePlay })).toBeNull();
  });

  it.each(["relock", "profile switch"])("drops the open list immediately on %s", async (change) => {
    const request = vi
      .fn()
      .mockImplementationOnce(async () =>
        page([{ id: "private", title: "Restricted track", mediaClass: "audio" }]),
      )
      .mockImplementationOnce(async () => page([]));
    vi.stubGlobal("fetch", request);
    const onPlay = vi.fn();
    const tree = () => (
      <I18nProvider initialLocale="en">
        <QueueButton onPlay={onPlay} />
      </I18nProvider>
    );
    const view = render(tree());
    fireEvent.click(screen.getByRole("button", { name: STRINGS.en.musicQueueTitle }));
    expect(await screen.findByText("Restricted track")).toBeInTheDocument();

    if (change === "relock") viewer.revealed = false;
    else viewer.profileID = "profile-b";
    view.rerender(tree());
    expect(screen.queryByText("Restricted track")).toBeNull();
    expect(screen.queryByRole("button", { name: STRINGS.en.musicQueuePlay })).toBeNull();
    expect(await screen.findByText(STRINGS.en.musicQueueEmpty)).toBeInTheDocument();
    expect(request).toHaveBeenCalledTimes(2);
  });

  it("localizes read failures and retries the read", async () => {
    const request = vi
      .fn()
      .mockRejectedValueOnce(new Error("network"))
      .mockImplementationOnce(async () => page([]));
    vi.stubGlobal("fetch", request);
    renderQueuePanel();
    expect(await screen.findByRole("alert")).toHaveTextContent(STRINGS.en.musicQueueReadFailed);
    fireEvent.click(screen.getByRole("button", { name: STRINGS.en.tryAgain }));
    await waitFor(() =>
      expect(screen.getByRole("status")).toHaveTextContent(STRINGS.en.musicQueueEmpty),
    );
  });

  it("provides all queue strings in every offered locale", () => {
    for (const locale of OFFERED_LOCALE_TAGS) {
      const strings = STRINGS[locale as keyof typeof STRINGS];
      for (const key of [
        "musicQueueTitle",
        "musicQueuePlay",
        "musicQueueEmpty",
        "musicQueueReadFailed",
        "musicQueueLockedItem",
      ] as const) {
        expect(strings[key], `${locale}: ${key}`).toBeTruthy();
        if (locale !== "en") expect(strings[key], `${locale}: ${key}`).not.toBe(STRINGS.en[key]);
      }
    }
  });

  it("reorders with drag and keyboard using positions among all visible server rows and refuses dragging across current into played history (CORE-2846)", async () => {
    const initial = [
      { id: "history-1", title: "History", mediaClass: "audio" },
      { id: "film", title: "Film", mediaClass: "video" },
      { id: "track-202", title: "Current", mediaClass: "audio" },
      { id: "track-a", title: "Alpha", mediaClass: "audio" },
      { id: "locked-1", title: "Hidden", mediaClass: "audio", locked: true },
      { id: "track-b", title: "Beta", mediaClass: "audio" },
    ];
    const reordered = [
      initial[0]!,
      initial[1]!,
      initial[2]!,
      initial[5]!,
      initial[3]!,
      initial[4]!,
    ];
    let serverRows = [...initial];
    const mutations: Array<{ query: string; variables: Record<string, unknown> }> = [];
    const requests = vi.fn(async (_path: string, request: RequestInit) => {
      const body = JSON.parse(String(request.body));
      if (String(body.query).includes("queueMove")) {
        mutations.push({ query: body.query, variables: body.variables });
        serverRows = [...reordered];
        return new Response(
          JSON.stringify({ data: { queueMove: [{ id: body.variables.mediaID }] } }),
        );
      }
      return page(serverRows);
    });
    vi.stubGlobal("fetch", requests);

    const view = render(
      <I18nProvider initialLocale="en">
        <QueueButton onPlay={vi.fn()} />
      </I18nProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: STRINGS.en.musicQueueTitle }));
    expect(await screen.findByText("Beta")).toBeInTheDocument();

    // Dragging an upcoming track (Beta) onto played history (History, before Current) is refused.
    fireEvent.dragStart(screen.getByText("Beta"));
    fireEvent.dragOver(screen.getByText("History"));
    fireEvent.drop(screen.getByText("History"));
    expect(mutations).toHaveLength(0);

    // Keyboard reorder on Beta (visible index 5) up to Alpha (visible index 3).
    fireEvent.keyDown(screen.getByText("Beta"), { key: "ArrowUp" });
    await waitFor(() => expect(mutations).toHaveLength(1));
    expect(mutations[0]?.variables).toEqual({
      mediaID: "track-b",
      position: 3,
      expectedProfileID: "profile-a",
    });
    const keyboardOrder = screen.getAllByRole("listitem").map((li) => li.textContent);

    // Reset and perform the same reorder via drag and drop onto Alpha (visible index 3).
    serverRows = [...initial];
    view.unmount();
    renderQueuePanel();
    expect(await screen.findByText("Beta")).toBeInTheDocument();
    const beta = screen.getByText("Beta");
    const alpha = screen.getByText("Alpha");
    fireEvent.dragStart(beta);
    fireEvent.dragOver(alpha);
    fireEvent.drop(alpha);
    await waitFor(() => expect(mutations).toHaveLength(2));
    expect(mutations[1]?.variables).toEqual(mutations[0]?.variables);
    const dragOrder = screen.getAllByRole("listitem").map((li) => li.textContent);
    expect(dragOrder).toEqual(keyboardOrder);
  });

  it("removes and clears upcoming while keeping current, video, locked and concurrent additions, and rereads on lost response or partial failure (CORE-2846)", async () => {
    let serverRows = [
      { id: "film", title: "Film", mediaClass: "video" },
      { id: "track-202", title: "Current", mediaClass: "audio" },
      { id: "track-a", title: "Alpha", mediaClass: "audio" },
      { id: "locked-1", title: "Locked", mediaClass: "audio", locked: true },
      { id: "track-b", title: "Beta", mediaClass: "audio" },
      { id: "track-c", title: "Gamma", mediaClass: "audio" },
    ];
    let serverEntries = [
      "film",
      "track-202",
      "track-a",
      "locked-1",
      "track-b",
      "track-b",
      "track-c",
    ];
    let removeCount = 0;
    const removedIDs: string[] = [];
    const requests = vi.fn(async (_path: string, request: RequestInit) => {
      const body = JSON.parse(String(request.body));
      if (String(body.query).includes("queueRemove")) {
        removeCount++;
        const id = String(body.variables.mediaID);
        removedIDs.push(id);
        const removeFirstEntry = (target: string) => {
          const idx = serverEntries.indexOf(target);
          if (idx >= 0) serverEntries.splice(idx, 1);
          if (!serverEntries.includes(target)) {
            serverRows = serverRows.filter((row) => row.id !== target);
          }
        };
        if (removeCount === 1) {
          // Committed remove whose HTTP response is lost.
          removeFirstEntry(id);
          throw new Error("connection lost after commit");
        }
        if (removeCount === 2 || removeCount === 3) {
          // Duplicate Beta entries are both removed; on the second Beta removal another device adds Delta.
          removeFirstEntry(id);
          if (removeCount === 3) {
            serverRows = [...serverRows, { id: "track-d", title: "Delta", mediaClass: "audio" }];
            serverEntries = [...serverEntries, "track-d"];
          }
          return new Response(JSON.stringify({ data: { queueRemove: [] } }));
        }
        // Final clear-upcoming removal (Gamma) fails on the server.
        throw new Error("final clear removal failed");
      }
      return new Response(
        JSON.stringify({
          data: {
            currentSession: { profileID: viewer.profileID, revealed: viewer.revealed },
            sourceItems: serverRows,
            playQueue: {
              currentEntryID: "e-track-202",
              entries: serverEntries.map((mediaID, index) => ({
                id: `e-${mediaID}-${index}`,
                mediaID,
              })),
            },
          },
        }),
      );
    });
    vi.stubGlobal("fetch", requests);

    renderQueuePanel();
    expect(await screen.findByText("Alpha")).toBeInTheDocument();

    // 1. Committed remove of Alpha whose response is lost -> rereads server state without Alpha.
    fireEvent.click(screen.getByRole("button", { name: "Remove Alpha" }));
    await waitFor(() => expect(screen.queryByText("Alpha")).toBeNull());
    expect(screen.getByRole("alert")).toHaveTextContent(STRINGS.en.musicQueueWriteFailed);

    // 2. Clear upcoming captures [Beta, Beta, Gamma] from playQueue.entries; both Beta entries are removed, Gamma fails, concurrent Delta survives.
    fireEvent.click(screen.getByRole("button", { name: STRINGS.en.musicQueueClearUpcoming }));
    await waitFor(() => expect(screen.queryByText("Beta")).toBeNull());
    expect(removedIDs).toEqual(["track-a", "track-b", "track-b", "track-c"]);
    const remaining = screen.getAllByRole("listitem").map((li) => li.textContent);
    expect(remaining).toEqual([
      "Film",
      "Current",
      STRINGS.en.musicQueueLockedItem,
      "Gamma",
      "Delta",
    ]);
  });

  it("saves 201 queued tracks as a playlist in order and reuses the created playlist on retry (CORE-2847)", async () => {
    const firstPage = Array.from({ length: 200 }, (_, i) => ({
      id: `track-${i + 1}`,
      title: `Track ${i + 1}`,
      mediaClass: "audio",
      libraryID: "lib-music",
    }));
    const secondPage = [
      firstPage[199]!,
      { id: "track-201", title: "Track 201", mediaClass: "audio", libraryID: "lib-music" },
    ];
    let createCalls = 0;
    let addCalls = 0;
    let addedMediaIDs: string[] = [];
    const requests = vi.fn(async (_path: string, request: RequestInit) => {
      const body = JSON.parse(String(request.body));
      const query = String(body.query);
      if (query.includes("createCollection(")) {
        createCalls++;
        return new Response(
          JSON.stringify({
            data: {
              createCollection: {
                id: "pl-201",
                libraryID: body.variables.libraryID,
                name: body.variables.name,
                sortOrder: 1,
                memberCount: 0,
                members: [],
              },
            },
          }),
        );
      }
      if (query.includes("addCollectionMedia(")) {
        addCalls++;
        if (addCalls === 1) {
          throw new Error("member add failed");
        }
        addedMediaIDs = body.variables.mediaIDs;
        return new Response(
          JSON.stringify({
            data: {
              addCollectionMedia: {
                id: body.variables.collectionID,
                libraryID: "lib-music",
                name: "Evening Mix",
                sortOrder: 1,
                memberCount: addedMediaIDs.length,
                members: [],
              },
            },
          }),
        );
      }
      return page(body.variables.offset === 0 ? firstPage : secondPage);
    });
    vi.stubGlobal("fetch", requests);

    renderQueuePanel();
    expect(await screen.findByText("Track 201")).toBeInTheDocument();

    fireEvent.change(screen.getByRole("textbox", { name: STRINGS.en.musicQueuePlaylistName }), {
      target: { value: "Evening Mix" },
    });
    fireEvent.click(screen.getByRole("button", { name: STRINGS.en.musicQueueSaveAsPlaylist }));

    // First attempt: playlist created, member add failed.
    expect(await screen.findByRole("alert")).toHaveTextContent(STRINGS.en.musicQueueSaveFailed);
    expect(createCalls).toBe(1);
    expect(addCalls).toBe(1);

    // Retry: reuses pl-201 without creating a second playlist.
    fireEvent.click(screen.getByRole("button", { name: STRINGS.en.musicQueueSaveAsPlaylist }));
    expect(await screen.findByRole("status")).toHaveTextContent("Saved playlist Evening Mix.");
    expect(createCalls).toBe(1);
    expect(addCalls).toBe(2);
    expect(addedMediaIDs).toEqual(Array.from({ length: 201 }, (_, i) => `track-${i + 1}`));
  });

  it("refuses saving on pending or failed queue read, mixed libraries, or repeated tracks [A,B,A] without creating a playlist (CORE-2847)", async () => {
    let createCalls = 0;
    // 1. Pending read
    vi.stubGlobal(
      "fetch",
      vi.fn(
        () =>
          new Promise<Response>(() => {
            /* stays pending */
          }),
      ),
    );
    const pendingView = render(
      <I18nProvider initialLocale="en">
        <QueueButton onPlay={vi.fn()} />
      </I18nProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: STRINGS.en.musicQueueTitle }));
    fireEvent.click(screen.getByRole("button", { name: STRINGS.en.musicQueueSaveAsPlaylist }));
    expect(screen.getByRole("alert")).toHaveTextContent(STRINGS.en.musicQueueSaveReadPending);
    pendingView.unmount();

    // 2. Mixed libraries
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_path: string, request: RequestInit) => {
        const body = JSON.parse(String(request.body));
        if (String(body.query).includes("createCollection(")) createCalls++;
        return page([
          { id: "a", title: "A", mediaClass: "audio", libraryID: "lib-1" },
          { id: "b", title: "B", mediaClass: "audio", libraryID: "lib-2" },
        ]);
      }),
    );
    const mixedView = render(
      <I18nProvider initialLocale="en">
        <QueueButton onPlay={vi.fn()} />
      </I18nProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: STRINGS.en.musicQueueTitle }));
    expect(await screen.findByText("B")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: STRINGS.en.musicQueueSaveAsPlaylist }));
    expect(screen.getByRole("alert")).toHaveTextContent(STRINGS.en.musicQueueSaveMixedLibraries);
    expect(createCalls).toBe(0);
    mixedView.unmount();

    // 3. Repeated track [A, B, A]: legacy sourceItems deduplicates to [A, B] while playQueue.entries carries [A, B, A].
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_path: string, request: RequestInit) => {
        const body = JSON.parse(String(request.body));
        if (String(body.query).includes("createCollection(")) createCalls++;
        return new Response(
          JSON.stringify({
            data: {
              currentSession: { profileID: viewer.profileID, revealed: viewer.revealed },
              sourceItems: [
                { id: "a", title: "A", mediaClass: "audio", libraryID: "lib-1" },
                { id: "b", title: "B", mediaClass: "audio", libraryID: "lib-1" },
              ],
              playQueue: {
                currentEntryID: "entry-1",
                entries: [
                  { id: "entry-1", mediaID: "a" },
                  { id: "entry-2", mediaID: "b" },
                  { id: "entry-3", mediaID: "a" },
                ],
              },
            },
          }),
        );
      }),
    );
    renderQueuePanel();
    expect(await screen.findByText("B")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: STRINGS.en.musicQueueSaveAsPlaylist }));
    expect(screen.getByRole("alert")).toHaveTextContent(STRINGS.en.musicQueueSaveDuplicateTrack);
    expect(createCalls).toBe(0);
  });

  it("renders single-line flex rows, non-wrapping action container, and distinct playlist placeholder", async () => {
    const longTitle =
      "A Very Long Music Track Title That Must Truncate Cleanly Instead Of Wrapping Action Icons";
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        page([
          { id: "track-202", title: "Current Track", mediaClass: "audio", libraryID: "lib-1" },
          { id: "b", title: longTitle, mediaClass: "audio", libraryID: "lib-1" },
          { id: "c", title: "Third Track", mediaClass: "audio", libraryID: "lib-1" },
        ]),
      ),
    );
    renderQueuePanel();
    const panel = screen.getByRole("dialog", { name: STRINGS.en.musicQueueTitle });
    const titleEl = await within(panel).findByText(longTitle);
    expect(titleEl).toHaveClass("bf-music-queue-row-title");
    const rowEl = titleEl.closest("li");
    expect(rowEl).toHaveClass("bf-music-queue-row");
    expect(
      rowEl?.querySelector(".bf-music-queue-actions")?.querySelectorAll("button"),
    ).toHaveLength(3);
    const currentEl = within(panel).getByText("Current Track");
    expect(currentEl).toHaveAttribute("aria-current", "true");
    expect(currentEl.closest("li")).toHaveAttribute("data-current", "true");
    const playlistInput = within(panel).getByRole("textbox", {
      name: STRINGS.en.musicQueuePlaylistName,
    });
    expect(playlistInput).toHaveAttribute("placeholder", STRINGS.en.musicQueuePlaylistName);
    expect(playlistInput).not.toHaveAttribute("placeholder", STRINGS.en.musicQueueTitle);
  });

  it("refuses Remove and excludes newly current track from Clear upcoming when queued behind an advance (CORE-2845)", async () => {
    const removedIDs: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_path: string, request: RequestInit) => {
        const body = JSON.parse(String(request.body));
        const q = String(body.query ?? "");
        if (q.includes("queueRemove")) {
          removedIDs.push(String(body.variables?.mediaID ?? ""));
          return new Response(JSON.stringify({ data: { queueRemove: { id: "ok" } } }));
        }
        return new Response(
          JSON.stringify({
            data: {
              currentSession: { profileID: viewer.profileID, revealed: viewer.revealed },
              sourceItems: [
                { id: "track-202", title: "Track A", mediaClass: "audio", libraryID: "lib-1" },
                { id: "b", title: "Track B", mediaClass: "audio", libraryID: "lib-1" },
                { id: "c", title: "Track C", mediaClass: "audio", libraryID: "lib-1" },
              ],
            },
          }),
        );
      }),
    );

    try {
      setActivePlayingMediaID("track-202");
      renderQueuePanel();
      await screen.findByText("Track B");

      // Advance A -> B begins while user clicks Remove on Track B.
      const endAdvance = beginQueueAdvance();
      fireEvent.click(screen.getByRole("button", { name: "Remove Track B" }));
      await Promise.resolve();
      expect(removedIDs).toEqual([]);

      // Advance commits to Track B before releasing the advance lock.
      setActivePlayingMediaID("b");
      endAdvance();

      await waitFor(() =>
        expect(screen.getByRole("button", { name: "Clear upcoming" })).toBeEnabled(),
      );
      expect(removedIDs).toEqual([]);

      // Repeat for Clear upcoming captured on Track A then advancing to Track B.
      setActivePlayingMediaID("track-202");
      const endAdvance2 = beginQueueAdvance();
      fireEvent.click(screen.getByRole("button", { name: "Clear upcoming" }));
      await Promise.resolve();

      setActivePlayingMediaID("b");
      endAdvance2();

      await waitFor(() => expect(removedIDs).toEqual(["c"]));
    } finally {
      resetPlayNextForTest();
      resetQueueCoordinatorForTest();
    }
  });

  it("refuses a queued Move when an advance turns the source row into played history (CORE-2845)", async () => {
    const mutations: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_path: string, request: RequestInit) => {
        const body = JSON.parse(String(request.body));
        const q = String(body.query ?? "");
        if (q.includes("queueMove") || q.includes("queueRemove")) {
          mutations.push(
            `${q.includes("queueMove") ? "move" : "remove"}:${body.variables?.mediaID}`,
          );
          return new Response(JSON.stringify({ data: {} }));
        }
        return new Response(
          JSON.stringify({
            data: {
              currentSession: { profileID: viewer.profileID, revealed: viewer.revealed },
              sourceItems: [
                { id: "track-202", title: "Track A", mediaClass: "audio", libraryID: "lib-1" },
                { id: "b", title: "Track B", mediaClass: "audio", libraryID: "lib-1" },
                { id: "c", title: "Track C", mediaClass: "audio", libraryID: "lib-1" },
                { id: "d", title: "Track D", mediaClass: "audio", libraryID: "lib-1" },
              ],
            },
          }),
        );
      }),
    );

    try {
      setActivePlayingMediaID("track-202");
      renderQueuePanel();
      const rowB = (await screen.findByText("Track B")).closest("li")!;
      const rowD = screen.getByText("Track D").closest("li")!;

      const endAdvance = beginQueueAdvance();
      fireEvent.dragStart(rowB);
      fireEvent.drop(rowD);
      fireEvent.click(screen.getByRole("button", { name: "Remove Track B" }));
      await Promise.resolve();

      // Advance skips over Track B directly to Track C, making Track B played history.
      setActivePlayingMediaID("c");
      endAdvance();

      await waitFor(() =>
        expect(screen.getByRole("button", { name: "Clear upcoming" })).toBeEnabled(),
      );
      expect(mutations).toEqual([]);
    } finally {
      resetPlayNextForTest();
      resetQueueCoordinatorForTest();
    }
  });
});
