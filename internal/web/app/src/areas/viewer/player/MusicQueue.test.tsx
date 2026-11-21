import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useRef, useState } from "react";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LOCALE_DECLARATION } from "@/l10n/locales";
import { setIdentityEpochSignal } from "@/lib/api";
import { I18nProvider } from "@/lib/i18n";
import { PARTIAL_CATALOGS } from "@/strings";
import { musicQueueStrings } from "@/strings/music-queue";
import { MediaItemCardMenu } from "../components/MediaItemCardMenu";
import { MusicQueueNotice } from "../components/MusicQueueNotice";
import { DEFAULT_BROWSE_STATE } from "../lib/libraryBrowse";
import { MusicQueueProfile, updateMusicQueue, useMusicQueueSnapshot } from "../lib/musicQueue";
import {
  finishPlayAllTracks,
  hasUnloadedPlayAllTracks,
  playAllTracks,
  startPlayQueue,
} from "../lib/playQueue";
import type { PlayQueueItem } from "../lib/watchQueue";
import { PlayerTransport } from "./PlayerTransport";
import { useMusicPlayback } from "./useMusicPlayback";
import type { PlayerState } from "./usePlayer";

vi.mock("@/lib/session", () => ({
  useSession: () => ({ isManager: true }),
  useViewerIdentity: () => "viewer-a",
}));

const profileID = "profile-a";
function track(id: string, title = id, libraryID = "library-a") {
  return { id, title, libraryID, mediaClass: "audio" };
}
const current = track("alpha", "Alpha");
const first = track("beta", "Beta");
const second = track("gamma", "Gamma");
const extra = track("delta", "Delta");
const state: PlayerState = {
  phase: "playing",
  message: "",
  terminal: false,
  info: null,
  stream: null,
  decision: "",
  markers: [],
  trickplay: null,
  trickplayBaseURL: "",
  hlsActive: false,
  tracksVersion: 0,
};
type Operation = { query: string; variables: Record<string, unknown> };
let rows: (PlayQueueItem & { locked?: boolean })[];
let requests: Operation[];
let rejectWrite: string | undefined;
let beforeWrite: (() => void) | undefined;
let mediaReadError: string | undefined;
let libraryTracks: PlayQueueItem[];
let libraryReadError: boolean;
let epoch: AbortController;

const writes = () => requests.filter(({ query }) => /queue(Add|Move|Remove|Clear)\(/.test(query));

beforeEach(() => {
  rows = [];
  requests = [];
  rejectWrite = undefined;
  beforeWrite = undefined;
  mediaReadError = undefined;
  libraryTracks = [];
  libraryReadError = false;
  epoch = new AbortController();
  setIdentityEpochSignal(epoch.signal);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      const operation = JSON.parse(String(init.body)) as Operation;
      requests.push(operation);
      const { query, variables } = operation;
      const response = (data: unknown) => Response.json({ data });
      if (query.includes("sourceItems(")) {
        expect(variables.query).toBe("queue");
        const offset = Number(variables.offset);
        return response({ sourceItems: rows.slice(offset, offset + Number(variables.limit)) });
      }
      if (query.includes("libraryItems(")) {
        const offset = Number(variables.offset);
        if (offset && libraryReadError) return Response.json({}, { status: 503 });
        return response({
          libraryItems: {
            total: libraryTracks.length,
            items: libraryTracks.slice(offset, offset + Number(variables.limit)).map((row) => ({
              ...row,
              kind: "media",
            })),
          },
        });
      }
      if (query.includes("query MediaRow")) {
        const id = String(variables.id);
        if (mediaReadError === id) return Response.json({}, { status: 503 });
        return response({
          media: [current, first, second, extra].find((item) => item.id === id) ?? null,
        });
      }
      const field = query.match(/(queueAdd|queueMove|queueRemove|queueClear)\(/)?.[1];
      if (field) {
        beforeWrite?.();
        expect(variables.expectedProfileID).toBe(profileID);
        if (rejectWrite)
          return Response.json({
            errors: [{ message: "write refused", extensions: { reason: rejectWrite } }],
          });
        const id = String(variables.mediaID);
        const entry = rows.find((item) => item.id === id);
        if (field === "queueClear") rows = [];
        else {
          rows = rows.filter((item) => item.id !== id);
          if (field !== "queueRemove") {
            rows.splice(
              Number(variables.position ?? rows.length),
              0,
              entry ?? [first, second, extra].find((item) => item.id === id) ?? track(id),
            );
          }
        }
        return response({ [field]: rows.slice(0, 50) });
      }
      if (query.includes("createCollection(")) {
        return response({ createCollection: { id: "playlist-1" } });
      }
      if (query.includes("addCollectionMedia(")) {
        return response({ addCollectionMedia: { id: "playlist-1" } });
      }
      if (query.includes("tagAssignments(")) {
        return response({ tagAssignments: { libraryID: "library-a" } });
      }
      return response({ itemState: null, media: null });
    }),
  );
});

afterEach(() => {
  cleanup();
  epoch.abort();
  setIdentityEpochSignal();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function Player({ catalog = [current] }: { catalog?: PlayQueueItem[] }) {
  const [item, setItem] = useState<PlayQueueItem>(current);
  const [run, setRun] = useState<string>();
  const stageRef = useRef<HTMLDivElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  const hlsRef = useRef(null);
  const playAll = playAllTracks(item.id);
  const music = useMusicPlayback({
    current: item,
    audio: true,
    identity: "viewer-a",
    profileID,
    continuing: run,
    catalog: playAll ?? catalog,
    catalogIncomplete: hasUnloadedPlayAllTracks(item.id),
    fullCatalog: playAll ? () => finishPlayAllTracks(item.id) : undefined,
    adopted: false,
    onQueueEdit: () => {},
  });
  return (
    <div ref={stageRef}>
      <video ref={videoRef} aria-label="Test media">
        <track kind="captions" />
      </video>
      <PlayerTransport
        videoRef={videoRef}
        hlsRef={hlsRef}
        state={state}
        stageRef={stageRef}
        profileID={profileID}
        mediaID={item.id}
        mediaTitle={item.title}
        musicQueue={music}
        onPreviousInQueue={
          music.previous
            ? () => {
                if (!music.previous) return;
                music.prepareAdvance(music.previous.id, false, true);
                setRun(music.run);
                setItem(music.previous);
              }
            : undefined
        }
        onNextInQueue={
          music.next
            ? () => {
                if (!music.next) return;
                music.prepareAdvance(music.next.id, !music.upcoming.length);
                setRun(music.run);
                setItem(music.next);
              }
            : undefined
        }
      />
    </div>
  );
}

function setup(options: { player?: boolean; catalog?: PlayQueueItem[]; card?: boolean } = {}) {
  const { player = true, catalog, card } = options;
  render(
    <I18nProvider>
      <MemoryRouter>
        <MusicQueueProfile.Provider value={profileID}>
          {card && (
            <MediaItemCardMenu
              title="Delta"
              primaryLabel="Play"
              isMedia
              isAudio
              href="https://example.invalid/media/delta"
              target={{ mediaID: extra.id }}
              onPrimary={() => {}}
            />
          )}
          {player && <Player catalog={catalog} />}
          <MusicQueueNotice profileID={profileID} />
        </MusicQueueProfile.Provider>
      </MemoryRouter>
    </I18nProvider>,
  );
  return userEvent.setup();
}

const button = (name: string) => screen.getByRole("button", { name });
const panel = () => screen.getByRole("region", { name: "Play queue" });
const titles = () =>
  within(panel())
    .queryAllByRole("listitem")
    .map((row) => row.querySelector("span.min-w-0")?.textContent);

describe("music queue controls", () => {
  it.each([
    ["Play next", "Added to play next.", 1],
    ["Add to queue", "Added to the queue.", undefined],
  ])("handles %s from an audio card menu", async (label, toast, position) => {
    rows = [current, first];
    const user = setup({ card: true, catalog: [current, first, second] });
    button("More actions for Delta").focus();
    await user.keyboard("{Enter}");
    await user.click(await screen.findByRole("menuitem", { name: label }));
    expect(await screen.findByText(toast)).toBeInTheDocument();
    expect(writes().map(({ variables }) => variables)).toEqual([
      { mediaID: second.id, expectedProfileID: profileID },
      {
        mediaID: extra.id,
        expectedProfileID: profileID,
        ...(position === undefined ? {} : { position }),
      },
    ]);
    await user.click(button("Play queue"));
    expect(titles()).toEqual(
      position === undefined ? ["Beta", "Gamma", "Delta"] : ["Delta", "Beta", "Gamma"],
    );
  });

  it("moves by keyboard and drag, removes and clears while preserving the current row", async () => {
    rows = [current, first, second, extra];
    const user = setup();
    await user.click(button("Play queue"));
    const up = await screen.findByRole("button", { name: "Move up: Gamma" });
    expect(button("Close")).toHaveFocus();
    up.focus();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(titles()).toEqual(["Gamma", "Beta", "Delta"]));
    expect(up).toHaveFocus();
    expect(writes().at(-1)?.variables).toEqual({
      mediaID: second.id,
      position: 1,
      expectedProfileID: profileID,
    });

    const list = within(panel()).getAllByRole("listitem");
    const handle = list[2].querySelector("[draggable=true]");
    if (!handle) throw new Error("Missing queue drag handle");
    fireEvent.dragStart(handle, { dataTransfer: { setData: vi.fn() } });
    fireEvent.dragOver(list[0]);
    fireEvent.drop(list[0]);
    await waitFor(() => expect(titles()).toEqual(["Delta", "Gamma", "Beta"]));
    expect(writes().at(-1)?.variables).toEqual({
      mediaID: extra.id,
      position: 1,
      expectedProfileID: profileID,
    });

    await user.click(button("Remove from queue: Gamma"));
    await waitFor(() => expect(titles()).toEqual(["Delta", "Beta"]));
    expect(writes().at(-1)?.query).toContain("queueRemove(");
    await user.click(button("Clear upcoming"));
    expect(await screen.findByText("No upcoming tracks.")).toBeInTheDocument();
    expect(rows).toEqual([current]);
    expect(panel()).toHaveTextContent("Now playing Alpha");
    await user.keyboard("{Escape}");
    expect(button("Play queue")).toHaveFocus();
  });

  it("clears only the captured upcoming tracks when another device appends a row", async () => {
    rows = [first, second];
    const video = { ...track("video"), mediaClass: "video" };
    const locked = { ...track("locked"), locked: true };
    beforeWrite = () => {
      rows.push(video, locked);
      beforeWrite = undefined;
    };
    const user = setup();
    await user.click(button("Play queue"));
    expect(await screen.findByText("Gamma")).toBeInTheDocument();
    await user.click(button("Clear upcoming"));
    expect(await screen.findByText("No upcoming tracks.")).toBeInTheDocument();
    expect(
      writes().map(({ query, variables }) => [query.includes("queueRemove("), variables.mediaID]),
    ).toEqual([
      [true, first.id],
      [true, second.id],
    ]);
    expect(rows).toEqual([video, locked]);
  });

  it("keeps the catalog continuation when a candidate's class read fails", async () => {
    const user = setup({
      catalog: [current, first, { id: second.id, title: second.title }],
    });
    mediaReadError = second.id;
    await user.click(button("Play queue"));
    await user.click(button("Remove from queue: Beta"));
    expect(await screen.findByText("Couldn’t update the play queue.")).toBeInTheDocument();
    expect(titles()).toEqual(["Beta", "Gamma"]);
    expect(writes()).toHaveLength(0);

    mediaReadError = undefined;
    await user.click(button("Remove from queue: Beta"));
    await waitFor(() => expect(titles()).toEqual(["Gamma"]));
    expect(rows.map(({ id }) => id)).toEqual([second.id]);
  });

  it.each([
    ["next", ["Alpha", "Gamma"]],
    ["append", ["Gamma", "Alpha"]],
  ] as const)("plays a previously heard track explicitly requeued with %s", async (kind, order) => {
    rows = [current, first, second];
    const user = setup();
    await user.click(button("Play queue"));
    await screen.findByText("Beta");
    await user.click(button("Next"));
    expect(panel()).toHaveTextContent("Now playing Beta");
    await act(() =>
      updateMusicQueue(profileID, { kind, mediaID: current.id, title: current.title }),
    );
    expect(titles()).toEqual(order);
    expect(button("Previous")).toBeEnabled();
    if (kind === "append") await user.click(button("Next"));
    await user.click(button("Next"));
    expect(panel()).toHaveTextContent("Now playing Alpha");
    expect(titles()).toEqual(kind === "append" ? [] : ["Gamma"]);
  });

  it("refreshes every queue page after an edit", async () => {
    rows = Array.from({ length: 201 }, (_, index) => track(`song-${index}`));
    function LastTrack() {
      const { rows } = useMusicQueueSnapshot(profileID);
      return <output>{rows.at(-1)?.title}</output>;
    }
    render(<LastTrack />);
    await act(() =>
      updateMusicQueue(profileID, { kind: "append", mediaID: extra.id, title: extra.title }),
    );
    expect(screen.getByText("Delta")).toBeInTheDocument();
    expect(requests.filter(({ variables }) => variables.offset === 200)).toHaveLength(2);
  });

  it("retains played history, restores the unplayed order and cycles repeat modes", async () => {
    vi.spyOn(Math, "random").mockReturnValue(0);
    const user = setup({ catalog: [track("history", "History"), current, first, second, extra] });
    await user.click(button("Play queue"));
    expect(titles()).toEqual(["Beta", "Gamma", "Delta"]);
    await user.click(button("Shuffle queue"));
    expect(button("Shuffle queue")).toHaveAttribute("aria-pressed", "true");
    expect(titles()).toEqual(["Gamma", "Delta", "Beta"]);
    expect(panel()).toHaveTextContent("Now playing Alpha");

    await user.click(button("Next"));
    expect(panel()).toHaveTextContent("Now playing Gamma");
    await user.click(button("Shuffle queue"));
    expect(titles()).toEqual(["Beta", "Delta"]);
    expect(button("Shuffle queue")).toHaveAttribute("aria-pressed", "false");

    await user.click(button("Repeat off"));
    await user.click(button("Repeat all"));
    expect(button("Repeat one")).toBeInTheDocument();
    await user.click(button("Next"));
    expect(panel()).toHaveTextContent("Now playing Beta");
    await user.click(button("Next"));
    expect(panel()).toHaveTextContent("Now playing Delta");
    await user.click(button("Repeat one"));
    expect(button("Repeat off")).toBeInTheDocument();
    await user.click(button("Repeat off"));
    await user.click(button("Next"));
    expect(panel()).toHaveTextContent("Now playing History");
    expect(writes()).toHaveLength(0);
  });

  it("saves the displayed queue order via the playlist name form", async () => {
    rows = [second, first];
    const user = setup();
    await user.click(button("Play queue"));
    expect(await screen.findByText("Gamma")).toBeInTheDocument();
    await user.click(button("Save queue as playlist"));
    const name = screen.getByRole("textbox", { name: "Playlist name" });
    expect(name).toHaveFocus();
    await user.type(name, "Road songs");
    await user.click(button("Save"));
    expect(await screen.findByText("Queue saved as a playlist.")).toBeInTheDocument();
    expect(requests.find(({ query }) => query.includes("createCollection("))?.variables).toEqual({
      libraryID: "library-a",
      name: "Road songs",
    });
    expect(requests.find(({ query }) => query.includes("addCollectionMedia("))?.variables).toEqual({
      collectionID: "playlist-1",
      mediaIDs: [current.id, second.id, first.id],
    });
  });

  it("loads the next Play all page before saving the playlist in order", async () => {
    libraryTracks = [
      current,
      ...Array.from({ length: 200 }, (_, index) => track(`song-${index + 1}`)),
    ];
    expect(await startPlayQueue({ libraryID: "library-a", state: DEFAULT_BROWSE_STATE })).toEqual({
      id: current.id,
      title: current.title,
    });
    const user = setup();
    await user.click(button("Play queue"));
    await user.click(button("Save queue as playlist"));
    await user.type(screen.getByRole("textbox", { name: "Playlist name" }), "Whole library");
    expect(
      requests
        .filter(({ query }) => query.includes("libraryItems("))
        .map(({ variables }) => variables.offset),
    ).toEqual([0]);
    await user.click(button("Save"));
    expect(await screen.findByText("Queue saved as a playlist.")).toBeInTheDocument();
    expect(
      requests
        .filter(({ query }) => query.includes("libraryItems("))
        .map(({ variables }) => variables.offset),
    ).toEqual([0, 200]);
    expect(requests.find(({ query }) => query.includes("addCollectionMedia("))?.variables).toEqual({
      collectionID: "playlist-1",
      mediaIDs: libraryTracks.map(({ id }) => id),
    });
  });

  it("shuffles every Play all page and restores the original order on toggle off", async () => {
    libraryTracks = [
      current,
      ...Array.from({ length: 200 }, (_, index) => track(`song-${index + 1}`)),
    ];
    await startPlayQueue({ libraryID: "library-a", state: DEFAULT_BROWSE_STATE });
    const user = setup();
    await user.click(button("Play queue"));
    expect(titles()[0]).toBe("song-1");
    libraryReadError = true;
    await user.click(button("Shuffle queue"));
    expect(await screen.findByText("Couldn’t load the play queue.")).toBeInTheDocument();
    expect(button("Shuffle queue")).toHaveAttribute("aria-pressed", "false");

    libraryReadError = false;
    vi.spyOn(Math, "random").mockReturnValueOnce(0).mockReturnValue(0.999);
    await user.click(button("Shuffle queue"));
    await waitFor(() => expect(titles()[0]).toBe("song-200"));
    expect(panel()).toHaveTextContent("Now playing Alpha");
    await user.click(button("Shuffle queue"));
    expect(titles()).toEqual(libraryTracks.slice(1).map(({ title }) => title));
  });

  it("explains the single-library constraint before creating a playlist", async () => {
    rows = [track("other-library", "Other", "library-b")];
    const user = setup();
    await user.click(button("Play queue"));
    await screen.findByText("Other");
    await user.click(button("Save queue as playlist"));
    await user.type(screen.getByRole("textbox", { name: "Playlist name" }), "Mixed");
    await user.click(button("Save"));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "A playlist can only contain tracks from one library.",
    );
    expect(requests.some(({ query }) => query.includes("createCollection("))).toBe(false);
  });

  it("drops PROFILE_MISMATCH without retrying or announcing success", async () => {
    rejectWrite = "PROFILE_MISMATCH";
    setup({ player: false });
    await act(() =>
      updateMusicQueue(profileID, { kind: "next", mediaID: extra.id, title: extra.title }),
    );
    expect(writes()).toHaveLength(1);
    expect(rows).toEqual([]);
    expect(screen.queryByText("Added to play next.")).not.toBeInTheDocument();
  });

  it("serializes edits and restores their server order after the identity cache is cleared", async () => {
    const result = () => screen.getByRole("status").textContent;
    function QueueProbe() {
      const { rows } = useMusicQueueSnapshot(profileID);
      return <output>{rows.map(({ title }) => title).join(", ")}</output>;
    }
    render(<QueueProbe />);
    await act(async () => {
      const append = updateMusicQueue(profileID, {
        kind: "append",
        mediaID: first.id,
        title: first.title,
      });
      const next = updateMusicQueue(profileID, {
        kind: "next",
        mediaID: extra.id,
        title: extra.title,
      });
      await Promise.all([append, next]);
    });
    expect(result()).toBe("Delta, Beta");
    expect(writes().map(({ variables }) => variables.mediaID)).toEqual([first.id, extra.id]);
    act(() => epoch.abort());
    expect(result()).toBe("");
    epoch = new AbortController();
    setIdentityEpochSignal(epoch.signal);
    await act(() => updateMusicQueue(profileID, { kind: "refresh" }));
    expect(result()).toBe("Delta, Beta");
  });

  it("ships every queue string, including errors, in each shipped locale", () => {
    for (const { tag, status } of LOCALE_DECLARATION.locales) {
      if (status !== "shipped") continue;
      for (const key of Object.keys(
        musicQueueStrings.en,
      ) as (keyof typeof musicQueueStrings.en)[]) {
        expect(PARTIAL_CATALOGS[tag as keyof typeof PARTIAL_CATALOGS][key]).toBeTruthy();
      }
    }
  });
});
