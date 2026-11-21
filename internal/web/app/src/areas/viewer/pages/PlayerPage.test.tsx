import "@testing-library/jest-dom/vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import {
  MemoryRouter,
  type NavigateFunction,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from "react-router";
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  type Mock,
  type MockInstance,
  vi,
} from "vitest";
import * as api from "@/lib/api";
import { ApiError, setIdentityEpochSignal } from "@/lib/api";
import {
  acceptWatchParty,
  calibrateWatchPartyClock,
  createWatchParty,
  decideWatchPartyRequest,
  fetchClientPrefs,
  fetchMedia,
  fetchNotifications,
  fetchPlaybackAdjustmentRanges,
  fetchPrePlayQueue,
  leaveWatchParty,
  leaveWatchPartyKeepalive,
  reportWatchPartyLeadPosition,
  requestWatchPartyTransport,
  setClientPref,
  watchPartyAutoSkip,
  watchPartyTransport,
} from "@/lib/graphql";
import type { GenericMediaInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { notifyIdentityChange } from "@/lib/identityChanges";
import { RegisterProvider } from "@/lib/register";
import { clearViewerCache } from "@/lib/viewerCache";
import { STRINGS } from "@/strings";
import { TrackList } from "../components/TrackList";
import {
  fetchMedia as fetchAddressMedia,
  fetchMediaDetailFields,
  fetchMediaRelations,
  fetchMediaTracks,
  mediaLinkState,
} from "../lib/mediaDetail";
import {
  fetchPlaybackInfo,
  type PlaybackAdjustment,
  type PlaybackInfo,
  unavailableReason,
} from "../lib/playback";
import { readVersionChoice, writeVersionChoice } from "../lib/versionChoice";
import { PlayerHost } from "../player/PlayerHost";
import { PlayerTransport } from "../player/PlayerTransport";
import { useGaplessAudio } from "../player/useGaplessAudio";
import type { PlayerState } from "../player/usePlayer";
import { DEFAULT_ADJUSTMENT, usePlayer } from "../player/usePlayer";
import {
  getWatchParty,
  joinWatchParty,
  PARTY_TRANSPORT_TIMEOUT_MS,
  pendingPartyTransport,
  resetWatchPartyForTest,
  sendPartyTransport,
  startWatchParty,
  watchPartyDefersUpNext,
} from "../player/watchPartyStore";
import { deliverPlayerCommand } from "../remote/remoteControl";
import { MediaDetailPage } from "./MediaDetailPage";
import { NotificationsPage } from "./NotificationsPage";
import { buildTimestampedLink, PlayerPage } from "./PlayerPage";

const viewer = vi.hoisted(() => ({
  profileID: "profile-a",
  revealed: true,
  identity: "viewer-1",
  refresh: vi.fn(async () => {}),
}));
vi.mock("@/lib/session", () => ({
  useSession: () => ({
    profiles: [],
    revealed: viewer.revealed,
    session: { profileID: viewer.profileID },
    refresh: viewer.refresh,
  }),
  useViewerIdentity: () => viewer.identity,
}));
vi.mock("@/lib/graphql", () => ({
  fetchClientPrefs: vi.fn().mockResolvedValue([]),
  setClientPref: vi.fn().mockResolvedValue(undefined),
  fetchItemState: vi.fn().mockResolvedValue(null),
  fetchMedia: vi.fn().mockResolvedValue(null),
  fetchPlaybackAdjustmentRanges: vi.fn().mockResolvedValue(null),
  fetchPlaybackMarkers: vi.fn().mockResolvedValue([]),
  fetchPrePlayQueue: vi.fn().mockResolvedValue([]),
  fetchRelatedMedia: vi.fn().mockResolvedValue([]),
  // Off the home network: the player does not listen for a lead party.
  calibrateWatchPartyClock: vi.fn().mockRejectedValue(new Error("not on the home network")),
  createWatchParty: vi.fn(),
  acceptWatchParty: vi.fn(),
  decideWatchPartyRequest: vi.fn(),
  fetchNotifications: vi.fn(),
  leaveWatchParty: vi.fn(async () => {}),
  leaveWatchPartyKeepalive: vi.fn(),
  reportWatchPartyDrift: vi.fn().mockResolvedValue({ groupID: "p1", action: "none", rate: 0 }),
  reportWatchPartyLeadPosition: vi.fn(async () => ({})),
  requestWatchPartyTransport: vi.fn(),
  watchPartyAutoSkip: vi.fn(),
  watchPartyTransport: vi.fn(),
}));
vi.mock("../lib/mediaDetail", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/mediaDetail")>()),
  fetchMedia: vi.fn(),
  fetchMediaDetailFields: vi.fn(),
  fetchMediaRelations: vi.fn(),
  fetchMediaTracks: vi.fn(),
}));
vi.mock("../lib/playback", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/playback")>()),
  fetchPlaybackInfo: vi.fn(),
  endPlayback: vi.fn(),
}));
vi.mock("../player/PrePlayStage", () => ({
  PrePlayStage: vi.fn(({ items, onDone }: { items: { title: string }[]; onDone: () => void }) => (
    <section aria-label="pre-play stage">
      <span>{items.map((item) => item.title).join(", ")}</span>
      <button type="button" onClick={onDone}>
        Skip all
      </button>
    </section>
  )),
}));
vi.mock("../player/usePlayer", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../player/usePlayer")>()),
  usePlayer: vi.fn(),
  DEFAULT_ADJUSTMENT: { pitchShiftSemitones: 0, tempo: 1 },
}));
vi.mock("../player/PlayerTransport", () => ({ PlayerTransport: vi.fn(() => null) }));
vi.mock("../player/useGaplessAudio", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../player/useGaplessAudio")>();
  return { ...actual, useGaplessAudio: vi.fn(actual.useGaplessAudio) };
});
vi.mock("../player/watchPartyStore", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../player/watchPartyStore")>()),
  watchPartyDefersUpNext: vi.fn(() => false),
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((finish, fail) => {
    resolve = finish;
    reject = fail;
  });
  return { promise, resolve, reject };
}

function playerState(overrides: Partial<PlayerState>): PlayerState {
  return {
    phase: "loading",
    message: "",
    terminal: false,
    info: null,
    stream: null,
    decision: "",
    markers: [],
    trickplay: null,
    trickplayBaseURL: "",
    hlsActive: false,
    recovering: false,
    tracksVersion: 0,
    mediaID: "media-alpha",
    ...overrides,
  };
}

function renderPlayer(
  state: PlayerState,
  navState?: unknown,
  adjustment: PlaybackAdjustment = DEFAULT_ADJUSTMENT,
) {
  const retry = vi.fn();
  const adjust = vi.fn();
  vi.mocked(usePlayer).mockReturnValue({
    videoRef: { current: null },
    hlsRef: { current: null },
    state,
    retry,
    restart: vi.fn(),
    switchToHls: vi.fn(() => false as const),
    adjustment,
    adjust,
  });
  const tree = () => (
    <I18nProvider>
      <MemoryRouter
        initialEntries={[
          {
            pathname: "/watch/media-alpha",
            // These cases test the title's player, so they arrive as an
            // advance does: past any pre-play queue (CORE-1701).
            state: { skipPrePlay: true, ...((navState as object | undefined) ?? {}) },
          },
        ]}
      >
        <Routes>
          <Route path="/watch/*" element={<PlayerPage />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>
  );
  const view = render(tree());
  return { retry, adjust, unmount: view.unmount, rerender: () => view.rerender(tree()) };
}

function stageVideo(): HTMLVideoElement {
  const video = document.querySelector("video");
  if (!video) throw new Error("no <video> on the stage");
  return video;
}

// The <video> presented its first frame, so the loading spinner clears.
function presentFirstFrame(): HTMLVideoElement {
  const video = stageVideo();
  fireEvent.loadedData(video);
  return video;
}

describe("PlayerPage timestamp links", () => {
  afterEach(() => vi.unstubAllEnvs());

  it.each([
    ["media/alpha", "media%2Falpha"],
    ["media-sample1-a371bff46385f7ded", "sample1.a371bff46385f7ded"],
  ])("keeps a shared playback link for %s under the Vite base URL", (id, token) => {
    vi.stubEnv("BASE_URL", "/web/");
    expect(buildTimestampedLink(id, 42)).toBe(`${window.location.origin}/web/watch/${token}?t=42`);
  });
});

describe("PlayerPage auto-skip (CORE-1803)", () => {
  afterEach(() => {
    resetWatchPartyForTest();
    window.sessionStorage.clear();
    vi.useRealTimers();
    vi.restoreAllMocks();
    vi.mocked(watchPartyAutoSkip).mockReset();
    vi.mocked(watchPartyTransport).mockReset();
    vi.mocked(requestWatchPartyTransport).mockReset();
    vi.mocked(fetchClientPrefs).mockResolvedValue([]);
    vi.mocked(calibrateWatchPartyClock).mockRejectedValue(new Error("not on the home network"));
  });

  async function mountPartyPlayer(member = false, lead = false) {
    vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval", "performance"] });
    const stream = vi.spyOn(api, "subscribeEvents").mockReturnValue(() => {});
    vi.mocked(calibrateWatchPartyClock).mockResolvedValueOnce({ offsetMS: 0, roundTripMS: 1 });
    const partyID = member ? "member-play-party" : "p1";
    const publish = (
      paused: boolean,
      positionMS = 0,
      signal = "transition",
      item = "media-alpha",
    ) => {
      const anchor = { item, positionMS, presentAtMS: Date.now() };
      stream.mock.lastCall?.[0](
        new MessageEvent("group.sync", {
          data: JSON.stringify({ payload: { ...anchor, groupID: partyID, signal, paused, lead } }),
        }),
      );
      return anchor;
    };
    if (member) {
      vi.mocked(acceptWatchParty).mockImplementationOnce(async () => ({
        partyID,
        ...publish(true, 0, "roster"),
      }));
      expect(await joinWatchParty(partyID)).toBe("media-alpha");
    } else {
      vi.mocked(createWatchParty).mockResolvedValue({
        partyID,
        content: "media-alpha",
        relaxed: true,
        members: [],
      });
      expect(await startWatchParty("media-alpha", 0, !lead, lead)).toBe(true);
    }
    vi.mocked(fetchClientPrefs).mockResolvedValue([{ key: "autoskip.intro", value: "on" }]);
    const view = renderPlayer(
      playerState({
        phase: "playing",
        markers: [{ title: "Intro", segmentKind: "intro", startSeconds: 5, endSeconds: 30 }],
      }),
    );
    await act(async () => {});
    const video = stageVideo();
    Object.defineProperty(video, "readyState", { value: 4 });
    let paused = true;
    vi.spyOn(video, "paused", "get").mockImplementation(() => paused);
    const play = vi.spyOn(video, "play").mockImplementation(async () => {
      paused = false;
      fireEvent.play(video);
    });
    vi.spyOn(video, "pause").mockImplementation(() => {
      paused = true;
      fireEvent.pause(video);
    });
    return { video, play, publish, partyID, view };
  }

  it("seeks a paused member to the acknowledged skip before showing Undo", async () => {
    const { video, publish, partyID } = await mountPartyPlayer(true);
    await act(async () => {
      publish(true, 29_800);
    });
    expect(video.currentTime).toBe(29.8);
    vi.mocked(watchPartyAutoSkip).mockResolvedValueOnce({
      partyID,
      applied: true,
      item: "media-alpha",
      positionMS: 30_000,
      presentAtMS: Date.now(),
    });

    await act(async () => fireEvent.timeUpdate(video));

    expect(watchPartyAutoSkip).toHaveBeenCalledExactlyOnceWith({
      partyID,
      item: "media-alpha",
      positionMS: 30_000,
    });
    expect(video.currentTime).toBe(30);
    expect(screen.getByRole("button", { name: "Undo" })).toBeVisible();
    fireEvent.seeking(video);
    fireEvent.seeked(video);
    act(() => vi.advanceTimersByTime(500));
    expect(video.currentTime).toBe(30);
    expect(video.paused).toBe(true);
    expect(requestWatchPartyTransport).not.toHaveBeenCalled();
  });

  it("follows host Play after an approved member Pause misses its transition", async () => {
    const { video, play, publish, partyID } = await mountPartyPlayer(true);
    await act(async () => {
      publish(false);
    });
    expect(video.paused).toBe(false);
    vi.mocked(requestWatchPartyTransport).mockResolvedValueOnce({ partyID, approved: true });
    act(() => vi.advanceTimersByTime(1));
    fireEvent.pointerDown(video);
    await act(async () => video.pause());
    expect(requestWatchPartyTransport).toHaveBeenCalledExactlyOnceWith({
      partyID,
      item: "media-alpha",
      positionMS: 0,
      paused: true,
    });

    act(() => vi.advanceTimersByTime(PARTY_TRANSPORT_TIMEOUT_MS - 1));
    expect(video.paused).toBe(true);
    expect(play).toHaveBeenCalledTimes(1);
    await act(async () => {
      vi.advanceTimersByTime(1);
      publish(false, 2000);
    });
    expect(video.paused).toBe(false);
    expect(play).toHaveBeenCalledTimes(2);
    expect(requestWatchPartyTransport).toHaveBeenCalledTimes(1);
  });

  it.each(["response", "stream", "queued Pause"])(
    "lands a paused lead and its follower at the skip acknowledged by %s",
    async (ack) => {
      const { video, publish, partyID, view } = await mountPartyPlayer(false, true);
      vi.mocked(watchPartyTransport).mockImplementation(async ({ item, positionMS }) => ({
        item,
        positionMS,
        presentAtMS: Date.now(),
      }));
      await act(async () => video.play());
      vi.mocked(watchPartyTransport).mockClear();
      const skip = deferred<Awaited<ReturnType<typeof watchPartyAutoSkip>>>();
      vi.mocked(watchPartyAutoSkip).mockReturnValueOnce(skip.promise);
      video.currentTime = 10;
      fireEvent.timeUpdate(video);
      expect(watchPartyAutoSkip).toHaveBeenCalledExactlyOnceWith({
        partyID,
        item: "media-alpha",
        positionMS: 30_000,
      });
      act(() => video.pause());
      expect(video.currentTime).toBe(10);
      expect(watchPartyTransport).not.toHaveBeenCalled();

      const anchor = { item: "media-alpha", positionMS: 30_000, presentAtMS: Date.now() };
      if (ack === "stream") {
        await act(async () => {
          publish(false, anchor.positionMS);
        });
        expect(video.currentTime).toBe(30);
        expect(watchPartyTransport).not.toHaveBeenCalled();
      }
      await act(async () => {
        if (ack === "queued Pause") skip.reject(new TypeError("Network unavailable"));
        else skip.resolve({ ...anchor, partyID, applied: true });
      });
      expect(watchPartyTransport).toHaveBeenCalledExactlyOnceWith({
        partyID,
        item: anchor.item,
        positionMS: anchor.positionMS,
        paused: true,
      });
      expect(video.paused).toBe(true);
      expect(video.currentTime).toBe(30);
      const pausedAnchor = getWatchParty().timeline;
      if (!pausedAnchor) throw new Error("no paused party anchor");
      expect(pausedAnchor).toMatchObject({ positionMS: 30_000, paused: true });

      view.unmount();
      resetWatchPartyForTest();
      vi.mocked(calibrateWatchPartyClock).mockResolvedValueOnce({ offsetMS: 0, roundTripMS: 1 });
      vi.mocked(acceptWatchParty).mockImplementationOnce(async () => ({
        partyID,
        ...publish(pausedAnchor.paused, pausedAnchor.positionMS, "roster"),
      }));
      expect(await joinWatchParty(partyID, { lead: true })).toBe("media-alpha");
      await act(async () => {
        renderPlayer(playerState({ phase: "playing" }));
      });
      const follower = stageVideo();
      Object.defineProperty(follower, "readyState", { value: 4 });
      act(() => vi.advanceTimersByTime(100));
      expect(follower.paused).toBe(true);
      expect(follower.currentTime).toBe(video.currentTime);
    },
  );

  it("keeps the player paused when Play is still in flight after 17 seconds", async () => {
    const { video, play } = await mountPartyPlayer();
    const started = deferred<Awaited<ReturnType<typeof watchPartyTransport>>>();
    const paused = deferred<Awaited<ReturnType<typeof watchPartyTransport>>>();
    vi.mocked(watchPartyTransport)
      .mockReturnValueOnce(started.promise)
      .mockReturnValueOnce(paused.promise);
    act(() => vi.advanceTimersByTime(1));
    fireEvent.pointerDown(video);
    await act(async () => video.play());
    fireEvent.pointerDown(video);
    act(() => video.pause());
    expect(watchPartyTransport).toHaveBeenCalledExactlyOnceWith({
      partyID: "p1",
      item: "media-alpha",
      positionMS: 0,
      paused: false,
    });

    act(() => vi.advanceTimersByTime(17_000));
    const anchor = { item: "media-alpha", positionMS: 0, presentAtMS: Date.now() };
    await act(async () => started.resolve(anchor));
    act(() => vi.advanceTimersByTime(500));
    expect(watchPartyTransport).toHaveBeenCalledTimes(2);
    expect(watchPartyTransport).toHaveBeenLastCalledWith({
      partyID: "p1",
      item: "media-alpha",
      positionMS: 0,
      paused: true,
    });
    expect(play).toHaveBeenCalledTimes(1);
    await act(async () => paused.resolve(anchor));
    expect(getWatchParty().timeline?.paused).toBe(true);
    expect(video.paused).toBe(true);
  });

  it("applies a queued Pause and then auto-skips a scrub while an earlier Play is in flight", async () => {
    const { video, publish } = await mountPartyPlayer();
    const started = deferred<Awaited<ReturnType<typeof watchPartyTransport>>>();
    vi.mocked(watchPartyTransport)
      .mockReturnValueOnce(started.promise)
      .mockImplementation(async ({ item, positionMS }) => ({
        item,
        positionMS,
        presentAtMS: Date.now(),
      }));
    const skipped = deferred<Awaited<ReturnType<typeof watchPartyAutoSkip>>>();
    vi.mocked(watchPartyAutoSkip).mockReturnValueOnce(skipped.promise);
    act(() => vi.advanceTimersByTime(1));
    fireEvent.pointerDown(video);
    await act(async () => video.play());
    const anchor = { item: "media-alpha", positionMS: 0, presentAtMS: Date.now() };
    // The transition reached the player, but its mutation still occupies the slot.
    act(() => {
      publish(false);
    });
    fireEvent.pointerDown(video);
    act(() => video.pause());
    fireEvent.pointerDown(video);
    video.currentTime = 10;
    fireEvent.seeking(video);
    fireEvent.seeked(video);
    expect(watchPartyTransport).toHaveBeenCalledTimes(1);
    expect(watchPartyAutoSkip).not.toHaveBeenCalled();

    await act(async () => started.resolve(anchor));
    expect(watchPartyTransport).toHaveBeenCalledTimes(2);
    expect(watchPartyTransport).toHaveBeenLastCalledWith({
      partyID: "p1",
      item: "media-alpha",
      positionMS: 0,
      paused: true,
    });
    expect(watchPartyAutoSkip).toHaveBeenCalledExactlyOnceWith({
      partyID: "p1",
      item: "media-alpha",
      positionMS: 30_000,
    });
    await act(async () =>
      skipped.resolve({ ...anchor, partyID: "p1", applied: true, positionMS: 30_000 }),
    );
    expect(getWatchParty().timeline).toMatchObject({ positionMS: 30_000, paused: true });
    expect(video.paused).toBe(true);
    expect(video.currentTime).toBe(30);
  });

  it("sends a relaxed member's queued Pause before auto-skip when Play's frame is late", async () => {
    const { video, publish, partyID } = await mountPartyPlayer(true);
    expect(getWatchParty().isHost).toBe(false);
    const started = deferred<Awaited<ReturnType<typeof requestWatchPartyTransport>>>();
    const paused = deferred<Awaited<ReturnType<typeof requestWatchPartyTransport>>>();
    vi.mocked(requestWatchPartyTransport)
      .mockReturnValueOnce(started.promise)
      .mockReturnValueOnce(paused.promise);
    const skipped = deferred<Awaited<ReturnType<typeof watchPartyAutoSkip>>>();
    vi.mocked(watchPartyAutoSkip).mockReturnValueOnce(skipped.promise);

    act(() => vi.advanceTimersByTime(1));
    fireEvent.pointerDown(video);
    await act(async () => video.play());
    fireEvent.pointerDown(video);
    act(() => video.pause());
    fireEvent.pointerDown(video);
    video.currentTime = 10;
    fireEvent.seeking(video);
    fireEvent.seeked(video);
    const approved = { partyID, approved: true };
    expect(requestWatchPartyTransport).toHaveBeenCalledExactlyOnceWith({
      partyID,
      item: "media-alpha",
      positionMS: 0,
      paused: false,
    });
    expect(watchPartyAutoSkip).not.toHaveBeenCalled();

    // The server has applied Play, but the only anchor this member has seen is paused.
    await act(async () => started.resolve(approved));
    expect(getWatchParty().timeline?.paused).toBe(true);
    expect(requestWatchPartyTransport).toHaveBeenCalledTimes(2);
    expect(requestWatchPartyTransport).toHaveBeenLastCalledWith({
      partyID,
      item: "media-alpha",
      positionMS: 0,
      paused: true,
    });
    expect(watchPartyAutoSkip).not.toHaveBeenCalled();
    await act(async () => {
      publish(false);
      paused.resolve(approved);
    });
    expect(video.paused).toBe(true);
    expect(watchPartyAutoSkip).toHaveBeenCalledExactlyOnceWith({
      partyID,
      item: "media-alpha",
      positionMS: 30_000,
    });

    await act(async () => {
      publish(true);
      skipped.resolve({ ...publish(true, 30_000), partyID, applied: true });
    });
    expect(getWatchParty().timeline).toMatchObject({ positionMS: 30_000, paused: true });
    expect(video.paused).toBe(true);
    expect(video.currentTime).toBe(30);
  });

  it.each(["solo", "host", "relaxed member", "strict member"])(
    "handles an auto-skip as %s",
    async (mode) => {
      vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
      vi.mocked(fetchClientPrefs).mockResolvedValue([{ key: "autoskip.intro", value: "on" }]);
      const applied = mode !== "strict member";
      if (mode !== "solo") {
        const stream = vi.spyOn(api, "subscribeEvents").mockReturnValue(() => {});
        vi.mocked(calibrateWatchPartyClock).mockResolvedValueOnce({ offsetMS: 0, roundTripMS: 1 });
        if (mode === "host") {
          vi.mocked(createWatchParty).mockResolvedValue({
            partyID: "p1",
            content: "media-alpha",
            relaxed: true,
            members: [],
          });
          expect(await startWatchParty("media-alpha")).toBe(true);
        } else {
          vi.mocked(acceptWatchParty).mockImplementation(async () => {
            const anchor = { item: "media-alpha", positionMS: 0, presentAtMS: 0 };
            stream.mock.lastCall?.[0](
              new MessageEvent("group.sync", {
                data: JSON.stringify({
                  payload: { groupID: "p1", signal: "roster", paused: true, ...anchor },
                }),
              }),
            );
            return { partyID: "p1", ...anchor };
          });
          expect(await joinWatchParty("p1")).toBe("media-alpha");
        }
        vi.mocked(watchPartyAutoSkip).mockResolvedValue({
          partyID: "p1",
          applied,
          item: "media-alpha",
          positionMS: applied ? 30_000 : 0,
          presentAtMS: 0,
        });
      }
      await act(async () => {
        renderPlayer(
          playerState({
            phase: "playing",
            info: { adjustment: { pitchShiftSemitones: 0, tempo: 1.25 } } as PlaybackInfo,
            markers: [{ title: "Intro", segmentKind: "intro", startSeconds: 0, endSeconds: 30 }],
          }),
        );
      });
      const video = stageVideo();
      Object.defineProperty(video, "readyState", { value: 4 });
      video.currentTime = 0.1;
      fireEvent.timeUpdate(video);
      if (mode === "solo") {
        expect(watchPartyAutoSkip).not.toHaveBeenCalled();
        expect(video.currentTime).toBe(24);
      } else {
        expect(video.currentTime).toBe(0.1);
        expect(watchPartyAutoSkip).toHaveBeenCalledWith({
          partyID: "p1",
          item: "media-alpha",
          positionMS: 30_000,
        });
        await act(async () => {});
        act(() => vi.advanceTimersByTime(500));
        expect(video.currentTime).toBe(applied ? 24 : 0.1);
      }
    },
  );

  it.each([
    { order: "timeupdate first", transition: null, reject: false, target: 30 },
    { order: "seeked first", transition: 30_000, reject: false, target: 30 },
    { order: "timeupdate first", transition: 45_000, reject: false, target: 45 },
    { order: "timeupdate first", transition: null, reject: true, target: 30 },
    { order: "timeupdate first", transition: 45_000, reject: true, target: 30 },
    { order: "seeked first", transition: 30_000, reject: true, target: 30 },
  ])(
    "resolves a scrub into an intro ($order, stream position $transition, rejected $reject)",
    async ({ order, transition, reject, target }) => {
      vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "setTimeout", "clearTimeout"] });
      vi.mocked(watchPartyTransport).mockClear();
      vi.mocked(fetchClientPrefs).mockResolvedValue([{ key: "autoskip.intro", value: "on" }]);
      const stream = vi.spyOn(api, "subscribeEvents").mockReturnValue(() => {});
      vi.mocked(calibrateWatchPartyClock).mockResolvedValueOnce({ offsetMS: 0, roundTripMS: 1 });
      vi.mocked(createWatchParty).mockResolvedValue({
        partyID: "p1",
        content: "media-alpha",
        relaxed: true,
        members: [],
      });
      expect(await startWatchParty("media-alpha")).toBe(true);
      const response = deferred<Awaited<ReturnType<typeof watchPartyAutoSkip>>>();
      const applied = {
        partyID: "p1",
        applied: true,
        item: "media-alpha",
        positionMS: 30_000,
        presentAtMS: 0,
      };
      vi.mocked(watchPartyAutoSkip)
        .mockReturnValueOnce(response.promise)
        .mockResolvedValue(applied);
      vi.mocked(watchPartyTransport).mockImplementationOnce(async ({ item, positionMS }) => ({
        item,
        positionMS,
        presentAtMS: 0,
      }));
      await act(async () => {
        renderPlayer(
          playerState({
            phase: "playing",
            markers: [{ title: "Intro", segmentKind: "intro", startSeconds: 5, endSeconds: 30 }],
          }),
        );
      });

      const video = stageVideo();
      Object.defineProperty(video, "readyState", { value: 4 });
      fireEvent.pointerDown(video);
      video.currentTime = 10;
      fireEvent.seeking(video);
      if (order === "timeupdate first") fireEvent.timeUpdate(video);
      fireEvent.seeked(video);
      if (order === "seeked first") fireEvent.timeUpdate(video);

      expect(watchPartyAutoSkip).toHaveBeenCalledExactlyOnceWith({
        partyID: "p1",
        item: "media-alpha",
        positionMS: 30_000,
      });
      expect(watchPartyTransport).not.toHaveBeenCalled();
      act(() => vi.advanceTimersByTime(500));
      expect(video.currentTime).toBe(10);

      const broadcast = (positionMS: number) =>
        act(async () => {
          stream.mock.lastCall?.[0](
            new MessageEvent("group.sync", {
              data: JSON.stringify({
                payload: {
                  groupID: "p1",
                  item: "media-alpha",
                  signal: "transition",
                  positionMS,
                  presentAtMS: 0,
                  paused: true,
                },
              }),
            }),
          );
        });
      const broadcastAfterError = reject && transition === 30_000;
      if (transition !== null && !broadcastAfterError) {
        await broadcast(transition);
        // An authoritative skip can move the player while the reply is delayed.
        if (transition === 30_000) expect(video.currentTime).toBe(30);
      }
      await act(async () => {
        if (reject) response.reject(new Error("Network unavailable"));
        else response.resolve(applied);
      });
      expect(watchPartyTransport).not.toHaveBeenCalled();
      if (broadcastAfterError) await broadcast(transition);
      if (reject) {
        await act(async () => vi.advanceTimersByTimeAsync(1000));
        expect(watchPartyAutoSkip).toHaveBeenCalledTimes(broadcastAfterError ? 1 : 2);
      }
      act(() => vi.advanceTimersByTime(500));
      expect(watchPartyTransport).not.toHaveBeenCalled();
      expect(video.currentTime).toBe(target);
    },
  );

  it.each([false, true])("handles a denied auto-skip (host also skips: %s)", async (hostSkips) => {
    vi.mocked(fetchClientPrefs).mockResolvedValue([{ key: "autoskip.intro", value: "on" }]);
    const stream = vi.spyOn(api, "subscribeEvents").mockReturnValue(() => {});
    vi.mocked(calibrateWatchPartyClock).mockResolvedValueOnce({ offsetMS: 0, roundTripMS: 1 });
    const anchor = { item: "media-alpha", positionMS: 0, presentAtMS: 0 };
    vi.mocked(acceptWatchParty).mockImplementationOnce(async () => {
      stream.mock.lastCall?.[0](
        new MessageEvent("group.sync", {
          data: JSON.stringify({
            payload: { groupID: "p1", signal: "roster", paused: true, ...anchor },
          }),
        }),
      );
      return { partyID: "p1", ...anchor };
    });
    expect(await joinWatchParty("p1")).toBe("media-alpha");
    const response = deferred<Awaited<ReturnType<typeof watchPartyAutoSkip>>>();
    vi.mocked(watchPartyAutoSkip).mockReturnValueOnce(response.promise);
    vi.mocked(requestWatchPartyTransport).mockResolvedValueOnce({
      partyID: "p1",
      approved: false,
    });
    await act(async () => {
      renderPlayer(
        playerState({
          phase: "playing",
          markers: [{ title: "Intro", segmentKind: "intro", startSeconds: 5, endSeconds: 30 }],
        }),
      );
    });
    const video = stageVideo();
    Object.defineProperty(video, "readyState", { value: 4 });
    fireEvent.pointerDown(video);
    video.currentTime = 10;
    fireEvent.seeking(video);
    fireEvent.timeUpdate(video);
    fireEvent.seeked(video);
    expect(watchPartyAutoSkip).toHaveBeenCalledTimes(1);
    expect(requestWatchPartyTransport).not.toHaveBeenCalled();

    if (hostSkips) {
      await act(async () => {
        stream.mock.lastCall?.[0](
          new MessageEvent("group.sync", {
            data: JSON.stringify({
              payload: {
                ...anchor,
                groupID: "p1",
                signal: "transition",
                positionMS: 30_000,
                paused: true,
              },
            }),
          }),
        );
      });
      expect(video.currentTime).toBe(30);
      expect(screen.queryByRole("button", { name: /undo/i })).not.toBeInTheDocument();
    }
    await act(async () => response.resolve({ partyID: "p1", applied: false, ...anchor }));
    if (hostSkips) {
      expect(requestWatchPartyTransport).not.toHaveBeenCalled();
      expect(screen.queryByRole("button", { name: /undo/i })).not.toBeInTheDocument();
      return;
    }
    expect(requestWatchPartyTransport).toHaveBeenCalledExactlyOnceWith({
      partyID: "p1",
      item: "media-alpha",
      positionMS: 10_000,
      paused: true,
    });
  });

  it("drops a member's scrub when the skip reply reveals a title change before sync", async () => {
    const { video, publish, partyID } = await mountPartyPlayer(true);
    const nextAnchor = { item: "media-beta", positionMS: 12_000, presentAtMS: Date.now() };
    vi.mocked(watchPartyAutoSkip).mockResolvedValueOnce({ partyID, applied: false, ...nextAnchor });
    vi.mocked(requestWatchPartyTransport).mockResolvedValueOnce({ partyID, approved: true });

    fireEvent.pointerDown(video);
    video.currentTime = 10;
    fireEvent.seeking(video);
    await act(async () => fireEvent.seeked(video));

    expect(watchPartyAutoSkip).toHaveBeenCalledExactlyOnceWith({
      partyID,
      item: "media-alpha",
      positionMS: 30_000,
    });
    expect(getWatchParty().timeline?.item).toBe("media-alpha");
    expect(requestWatchPartyTransport).not.toHaveBeenCalled();

    await act(async () => publish(true, nextAnchor.positionMS, "transition", nextAnchor.item));
    expect(getWatchParty().timeline).toMatchObject(nextAnchor);
    expect(requestWatchPartyTransport).not.toHaveBeenCalled();
  });

  it.each([
    { delay: 1000, expired: false },
    { delay: 20_000, expired: true },
  ])(
    "handles a return to the current anchor while a skip is pending ($delay ms)",
    async ({ delay, expired }) => {
      vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "performance"] });
      vi.mocked(watchPartyTransport).mockClear();
      vi.mocked(fetchClientPrefs).mockResolvedValue([{ key: "autoskip.intro", value: "on" }]);
      vi.spyOn(api, "subscribeEvents").mockReturnValue(() => {});
      vi.mocked(calibrateWatchPartyClock).mockResolvedValueOnce({ offsetMS: 0, roundTripMS: 1 });
      vi.mocked(createWatchParty).mockResolvedValue({
        partyID: "p1",
        content: "media-alpha",
        relaxed: true,
        members: [],
      });
      expect(await startWatchParty("media-alpha")).toBe(true);
      const response = deferred<Awaited<ReturnType<typeof watchPartyAutoSkip>>>();
      vi.mocked(watchPartyAutoSkip).mockReturnValueOnce(response.promise);
      vi.mocked(watchPartyTransport).mockImplementationOnce(async ({ item, positionMS }) => ({
        item,
        positionMS,
        presentAtMS: 0,
      }));
      await act(async () => {
        renderPlayer(
          playerState({
            phase: "playing",
            markers: [{ title: "Intro", segmentKind: "intro", startSeconds: 5, endSeconds: 30 }],
          }),
        );
      });
      const video = stageVideo();
      Object.defineProperty(video, "readyState", { value: 4 });
      for (const seconds of [10, 0]) {
        act(() => vi.advanceTimersByTime(1));
        fireEvent.pointerDown(video);
        video.currentTime = seconds;
        fireEvent.seeking(video);
        fireEvent.seeked(video);
      }
      expect(watchPartyAutoSkip).toHaveBeenCalledExactlyOnceWith({
        partyID: "p1",
        item: "media-alpha",
        positionMS: 30_000,
      });
      expect(watchPartyTransport).not.toHaveBeenCalled();
      act(() => vi.advanceTimersByTime(delay));
      expect(video.currentTime).toBe(0);

      await act(async () =>
        response.resolve({
          partyID: "p1",
          applied: true,
          item: "media-alpha",
          positionMS: 30_000,
          presentAtMS: 0,
        }),
      );
      if (expired) {
        expect(watchPartyTransport).not.toHaveBeenCalled();
        act(() => vi.advanceTimersByTime(500));
        expect(video.currentTime).toBe(30);
      } else {
        expect(watchPartyTransport).toHaveBeenCalledExactlyOnceWith({
          partyID: "p1",
          item: "media-alpha",
          positionMS: 0,
          paused: true,
        });
        expect(video.currentTime).toBe(0);
      }
    },
  );

  it("holds a scrub to the auto-skip target until its anchor arrives", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "performance"] });
    vi.mocked(watchPartyTransport).mockClear();
    vi.mocked(fetchClientPrefs).mockResolvedValue([{ key: "autoskip.intro", value: "on" }]);
    const stream = vi.spyOn(api, "subscribeEvents").mockReturnValue(() => {});
    vi.mocked(calibrateWatchPartyClock).mockResolvedValueOnce({ offsetMS: 0, roundTripMS: 1 });
    vi.mocked(createWatchParty).mockResolvedValue({
      partyID: "p1",
      content: "media-alpha",
      relaxed: true,
      members: [],
    });
    expect(await startWatchParty("media-alpha")).toBe(true);
    const response = deferred<Awaited<ReturnType<typeof watchPartyAutoSkip>>>();
    vi.mocked(watchPartyAutoSkip).mockReturnValueOnce(response.promise);
    await act(async () => {
      renderPlayer(
        playerState({
          phase: "playing",
          markers: [{ title: "Intro", segmentKind: "intro", startSeconds: 5, endSeconds: 30 }],
        }),
      );
    });
    const video = stageVideo();
    Object.defineProperty(video, "readyState", { value: 4 });
    video.currentTime = 10;
    fireEvent.timeUpdate(video);
    expect(watchPartyAutoSkip).toHaveBeenCalledExactlyOnceWith({
      partyID: "p1",
      item: "media-alpha",
      positionMS: 30_000,
    });

    fireEvent.pointerDown(video);
    video.currentTime = 30;
    fireEvent.seeking(video);
    fireEvent.seeked(video);
    act(() => vi.advanceTimersByTime(500));
    expect(video.currentTime).toBe(30);

    act(() => {
      stream.mock.lastCall?.[0](
        new MessageEvent("group.sync", {
          data: JSON.stringify({
            payload: {
              groupID: "p1",
              signal: "transition",
              item: "media-alpha",
              positionMS: 12_000,
              presentAtMS: 0,
              paused: true,
            },
          }),
        }),
      );
      vi.advanceTimersByTime(500);
    });
    expect(video.currentTime).toBe(30);

    await act(async () => {
      const anchor = { item: "media-alpha", positionMS: 30_000, presentAtMS: 0 };
      stream.mock.lastCall?.[0](
        new MessageEvent("group.sync", {
          data: JSON.stringify({
            payload: { groupID: "p1", signal: "transition", paused: true, ...anchor },
          }),
        }),
      );
      response.resolve({ partyID: "p1", applied: true, ...anchor });
    });
    expect(video.currentTime).toBe(30);
    expect(getWatchParty().timeline?.positionMS).toBe(30_000);
    expect(watchPartyTransport).not.toHaveBeenCalled();
    video.currentTime = 35;
    act(() => vi.advanceTimersByTime(500));
    expect(video.currentTime).toBe(30);
  });

  it.each(["transition", "response", "transition after lost response"])(
    "holds lead reports until the skip's %s",
    async (first) => {
      vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
      vi.mocked(reportWatchPartyLeadPosition).mockClear();
      vi.mocked(watchPartyTransport).mockClear();
      vi.mocked(fetchClientPrefs).mockResolvedValue([{ key: "autoskip.intro", value: "on" }]);
      const stream = vi.spyOn(api, "subscribeEvents").mockReturnValue(() => {});
      vi.mocked(calibrateWatchPartyClock).mockResolvedValueOnce({ offsetMS: 0, roundTripMS: 1 });
      vi.mocked(createWatchParty).mockResolvedValue({
        partyID: "p1",
        content: "media-alpha",
        relaxed: false,
        members: [],
      });
      expect(await startWatchParty("media-alpha", 0, false, true)).toBe(true);

      const anchor = { item: "media-alpha", positionMS: 30_000, presentAtMS: Date.now() };
      const response = deferred<Awaited<ReturnType<typeof watchPartyAutoSkip>>>();
      vi.mocked(watchPartyAutoSkip).mockReturnValueOnce(response.promise);
      const broadcast = (positionMS: number) =>
        stream.mock.lastCall?.[0](
          new MessageEvent("group.sync", {
            data: JSON.stringify({
              payload: {
                ...anchor,
                groupID: "p1",
                signal: "transition",
                lead: true,
                positionMS,
                presentAtMS:
                  positionMS === anchor.positionMS ? anchor.presentAtMS : anchor.presentAtMS - 1,
              },
            }),
          }),
        );
      const reply = () => response.resolve({ ...anchor, partyID: "p1", applied: true });

      await act(async () => {
        renderPlayer(
          playerState({
            phase: "playing",
            markers: [{ title: "Intro", segmentKind: "intro", startSeconds: 0, endSeconds: 30 }],
          }),
        );
      });
      const video = stageVideo();
      Object.defineProperties(video, { readyState: { value: 4 }, paused: { value: false } });
      video.currentTime = 10;
      fireEvent.timeUpdate(video);
      expect(watchPartyAutoSkip).toHaveBeenCalledExactlyOnceWith({
        partyID: "p1",
        item: "media-alpha",
        positionMS: 30_000,
      });

      act(() => broadcast(12_000));
      act(() => vi.advanceTimersByTime(1000));
      expect(video.currentTime).toBe(10);
      expect(reportWatchPartyLeadPosition).not.toHaveBeenCalled();

      if (first === "transition after lost response") {
        await act(async () => response.reject(new Error("Network unavailable")));
        act(() => vi.advanceTimersByTime(1000));
        expect(video.currentTime).toBe(10);
        expect(reportWatchPartyLeadPosition).not.toHaveBeenCalled();
      }
      await act(async () => {
        if (first === "response") reply();
        else broadcast(30_000);
      });
      expect(video.currentTime).toBe(30);
      fireEvent.seeked(video);
      expect(watchPartyTransport).not.toHaveBeenCalled();
      act(() => vi.advanceTimersByTime(1000));
      expect(reportWatchPartyLeadPosition).toHaveBeenCalledExactlyOnceWith(
        "p1",
        expect.objectContaining({ positionMS: 30_000 }),
      );

      video.currentTime = 31;
      await act(async () => {
        if (first === "transition") reply();
        if (first === "response") broadcast(30_000);
      });
      expect(video.currentTime).toBe(31);
    },
  );
});

// feature:hls-error-recovery
describe("PlayerPage failure and recovery states", () => {
  beforeEach(() => vi.mocked(usePlayer).mockReset());

  it("offers Try again on a non-terminal message and routes it to the engine retry", () => {
    const { retry } = renderPlayer(
      playerState({ phase: "message", message: "Playback failed. Please try again." }),
    );
    expect(screen.getByRole("alert")).toHaveTextContent("Playback failed. Please try again.");
    const tryAgain = screen.getByRole("button", { name: "Try again" });
    expect(retry).not.toHaveBeenCalled();
    fireEvent.click(tryAgain);
    expect(retry).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("button", { name: "Back to Browse" })).not.toBeInTheDocument();
  });

  it("renders exactly one h1 on the player route (CORE-0089)", () => {
    renderPlayer(playerState({ phase: "playing" }));
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  });

  it("keeps a reveal-terminal block terminal: Back to Browse, no retry", () => {
    const { retry } = renderPlayer(
      playerState({ phase: "terminal", terminal: true, message: "Unavailable" }),
    );
    expect(screen.getByRole("button", { name: "Back to Browse" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
    expect(retry).not.toHaveBeenCalled();
  });

  it("renders a non-modal reconnecting status while hls.js recovers", () => {
    renderPlayer(playerState({ phase: "playing", hlsActive: true, recovering: true }));
    presentFirstFrame();
    expect(screen.getByRole("status")).toHaveTextContent("Reconnecting…");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("hides the reconnecting status once playback is healthy", () => {
    renderPlayer(playerState({ phase: "playing", hlsActive: true }));
    presentFirstFrame();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("offers Back to Browse, not Try again, when another attempt cannot help (CORE-1146)", () => {
    const { retry } = renderPlayer(
      playerState({
        phase: "message",
        message: "This item is not available.",
        action: "back",
      }),
    );
    expect(screen.getByRole("alert")).toHaveTextContent("This item is not available.");
    expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Back to Browse" }));
    expect(retry).not.toHaveBeenCalled();
  });

  it("keeps the server's reason behind a Details disclosure (CORE-1146, CORE-2011)", () => {
    renderPlayer(
      playerState({
        phase: "message",
        message: "Playback unavailable.",
        detail: "playback info unavailable",
      }),
    );
    const reason = screen.getByText("Message from the server: playback info unavailable");
    expect(reason.closest("details")).not.toHaveAttribute("open");
    expect(screen.getByText("Details").tagName).toBe("SUMMARY");
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
  });

  it("explains an incomplete source with Back to Browse and no Try again (CORE-2380)", () => {
    const reason = unavailableReason("media-source-incomplete", {
      sourceEndsEarly: STRINGS.en.playbackSourceEndsEarly,
    });
    const { retry } = renderPlayer(playerState({ phase: "message", ...reason }));
    expect(screen.getByRole("alert")).toHaveTextContent(STRINGS.en.playbackSourceEndsEarly);
    expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Back to Browse" }));
    expect(retry).not.toHaveBeenCalled();
  });

  it("passes copy for every unavailable reason to the engine", () => {
    renderPlayer(playerState({ phase: "loading" }));
    const messages = vi.mocked(usePlayer).mock.calls.at(-1)?.[1];
    expect(messages).toMatchObject({
      mediaFileMissing: expect.any(String),
      mediaNotAvailable: expect.any(String),
      transcodeDisallowed: expect.any(String),
      streamLimitReached:
        "This profile has reached its stream limit. Stop another stream, then try again.",
      transcodeCapacityReached: expect.any(String),
      dolbyVisionUnavailable: expect.any(String),
      hdrToneMapUnavailable: expect.any(String),
      hdrToneMapDisabled: expect.any(String),
      sourceEndsEarly: STRINGS.en.playbackSourceEndsEarly,
    });
  });

  it("shows Waiting for network instead of Reconnecting while offline (CORE-1362)", () => {
    renderPlayer(
      playerState({ phase: "playing", hlsActive: true, recovering: true, waitingForNetwork: true }),
    );
    presentFirstFrame();
    expect(screen.getByRole("status")).toHaveTextContent("Waiting for network…");
    expect(screen.queryByText("Reconnecting…")).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});

describe("PlayerPage loading spinner (CORE-1363)", () => {
  beforeEach(() => vi.mocked(usePlayer).mockReset());
  afterEach(() => vi.useRealTimers());

  it("shows while playback info loads", () => {
    renderPlayer(playerState({ phase: "loading" }));
    expect(screen.getByRole("status")).toHaveTextContent("Loading…");
  });

  it("shows from attach until the first frame", () => {
    renderPlayer(playerState({ phase: "playing" }));
    expect(screen.getByRole("status")).toHaveTextContent("Loading…");
    presentFirstFrame();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("shows when playback waits for data longer than 500 ms, and clears when it resumes", () => {
    vi.useFakeTimers();
    renderPlayer(playerState({ phase: "playing" }));
    const video = presentFirstFrame();
    Object.defineProperty(video, "paused", { configurable: true, value: false });
    fireEvent.waiting(video);
    act(() => {
      vi.advanceTimersByTime(499);
    });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(screen.getByRole("status")).toHaveTextContent("Loading…");
    fireEvent.playing(video);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("clears a stall once time moves again", () => {
    vi.useFakeTimers();
    renderPlayer(playerState({ phase: "playing" }));
    const video = presentFirstFrame();
    Object.defineProperty(video, "paused", { configurable: true, value: false });
    fireEvent.stalled(video);
    act(() => {
      vi.advanceTimersByTime(500);
    });
    expect(screen.getByRole("status")).toHaveTextContent("Loading…");
    video.currentTime = 5;
    fireEvent.timeUpdate(video);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("stays hidden for a wait while paused", () => {
    vi.useFakeTimers();
    renderPlayer(playerState({ phase: "playing" }));
    const video = presentFirstFrame();
    fireEvent.waiting(video);
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("uses the backdrop as the video poster", async () => {
    vi.mocked(fetchMedia).mockResolvedValueOnce({
      id: "media-alpha",
      title: "Alpha",
      artworkSlots: { poster: "/art/poster.jpg", backdrop: "/art/backdrop.jpg" },
      credits: [],
    } as never);
    renderPlayer(
      playerState({ phase: "playing", info: { mediaClass: "video" } as PlayerState["info"] }),
    );
    await waitFor(() => expect(stageVideo()).toHaveAttribute("poster", "/art/backdrop.jpg"));
  });
});

describe("PlayerPage audio items (CORE-1143)", () => {
  beforeEach(() => {
    vi.mocked(usePlayer).mockReset();
    vi.mocked(PlayerTransport).mockClear();
  });

  const queue = [
    { id: "media-prev", title: "Before" },
    { id: "media-alpha", title: "Alpha" },
    { id: "media-next", title: "After" },
  ];

  it("shows the video surface, not the artwork stage, for a composed-picture audio item (CORE-1656)", () => {
    renderPlayer(
      playerState({
        phase: "playing",
        info: { mediaClass: "audio", reason: "composed-picture" } as PlayerState["info"],
      }),
      { title: "Alpha" },
    );
    expect(document.querySelector(".bf-audio-stage")).toBeNull();
    expect(document.querySelector("video")).not.toBeNull();
  });

  it("shows artwork, title and credits for an audio item, and declares the queue's next item", async () => {
    vi.mocked(fetchMedia).mockResolvedValueOnce({
      id: "media-alpha",
      title: "Alpha",
      artworkSlots: { poster: "/art/alpha.jpg" },
      credits: [
        { entityID: "p1", title: "Ann", kind: "artist", archetype: "person", relation: "by" },
        { entityID: "c1", title: "Record", kind: "album", archetype: "container", relation: "on" },
      ],
    } as never);
    renderPlayer(
      playerState({ phase: "playing", info: { mediaClass: "audio" } as PlayerState["info"] }),
      { title: "Alpha", queue, queueIdentity: "viewer-1" },
    );

    expect(await screen.findByText("Ann")).toBeInTheDocument();
    expect(screen.getByText("Record")).toBeInTheDocument();
    expect(screen.getByText("2 of 3")).toBeInTheDocument();
    expect(document.querySelector(".bf-audio-artwork img")).toHaveAttribute(
      "src",
      "/art/alpha.jpg",
    );
    expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[3]).toBe("media-next");

    await waitFor(() => {
      const props = vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0];
      expect(props?.onPreviousInQueue).toBeTypeOf("function");
      expect(props?.onNextInQueue).toBeTypeOf("function");
      expect(props?.showSkipIntervalCollection).toBe("Record");
    });
  });

  it("declares an empty next item on a card queue's last item, so Play all is not resumed", () => {
    renderPlayer(
      playerState({ phase: "playing", info: { mediaClass: "audio" } as PlayerState["info"] }),
      { title: "Alpha", queue: queue.slice(0, 2), queueIdentity: "viewer-1" },
    );
    expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[3]).toBe("");
  });

  it("drops a queue listed for another viewer identity (relock or profile switch)", () => {
    renderPlayer(
      playerState({ phase: "playing", info: { mediaClass: "audio" } as PlayerState["info"] }),
      { title: "Alpha", queue, queueIdentity: "viewer-revealed" },
    );
    expect(screen.queryByText("2 of 3")).toBeNull();
    expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[3]).toBeUndefined();
    const props = vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0];
    expect(props?.onPreviousInQueue).toBeUndefined();
    expect(props?.onNextInQueue).toBeUndefined();
  });

  it("keeps the video stage, and no queue controls, for a video item without a queue", () => {
    renderPlayer(
      playerState({ phase: "playing", info: { mediaClass: "video" } as PlayerState["info"] }),
    );
    expect(document.querySelector(".bf-audio-stage")).toBeNull();
    const props = vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0];
    expect(props?.onPreviousInQueue).toBeUndefined();
    expect(props?.onNextInQueue).toBeUndefined();
    expect(props?.showSkipIntervalLabel).toBe("");
  });
});

describe("PlayerPage class-narrowed queue (CORE-1490, CORE-1309)", () => {
  beforeEach(() => {
    vi.mocked(usePlayer).mockReset();
    vi.mocked(PlayerTransport).mockClear();
  });

  const audioInfo = (next?: { mediaID: string; mediaClass: string }) =>
    ({ mediaClass: "audio", ...(next ? { next } : {}) }) as PlayerState["info"];
  const lastTransportProps = () => vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0];
  const lastPlayedID = () => vi.mocked(usePlayer).mock.calls.at(-1)?.[0];

  it("steps only through tracks of a mixed queue whose rows report their class", async () => {
    renderPlayer(playerState({ phase: "playing", info: audioInfo() }), {
      title: "Alpha",
      queue: [
        { id: "media-prev", title: "Before", mediaClass: "audio" },
        { id: "media-v1", title: "Film", mediaClass: "video" },
        { id: "media-alpha", title: "Alpha", mediaClass: "audio" },
        { id: "media-v2", title: "Clip", mediaClass: "video" },
        { id: "media-next", title: "After", mediaClass: "audio" },
      ],
      queueIdentity: "viewer-1",
    });
    expect(screen.getByText("2 of 3")).toBeInTheDocument();
    expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[3]).toBe("media-next");

    act(() => lastTransportProps()?.onNextInQueue?.());
    await waitFor(() => expect(lastPlayedID()).toBe("media-next"));
    // The narrowed queue travels with it: the videos stay out.
    expect(screen.getByText("3 of 3")).toBeInTheDocument();
  });

  it("drops the declared next item once playback-info reports it is a video", async () => {
    renderPlayer(
      playerState({
        phase: "playing",
        info: audioInfo({ mediaID: "media-v1", mediaClass: "video" }),
      }),
      {
        title: "Alpha",
        queue: [
          { id: "media-alpha", title: "Alpha" },
          { id: "media-v1", title: "Film" },
          { id: "media-t2", title: "Second" },
        ],
        queueIdentity: "viewer-1",
      },
    );
    expect(await screen.findByText("1 of 2")).toBeInTheDocument();

    act(() => lastTransportProps()?.onNextInQueue?.());
    await waitFor(() => expect(lastPlayedID()).toBe("media-t2"));
    expect(screen.getByText("2 of 2")).toBeInTheDocument();
  });

  it("never auto-advances into an issued next the learned class removed (CORE-1732)", async () => {
    // An unclassified queue of a track then a video: playback-info issues the
    // video as next (with streams) before its class is known.
    renderPlayer(
      playerState({
        phase: "playing",
        info: {
          mediaClass: "audio",
          next: {
            mediaID: "media-v1",
            mediaClass: "video",
            streams: [{ kind: "direct-play", url: "/api/media/v1" }],
          },
        } as PlayerState["info"],
      }),
      {
        title: "Alpha",
        queue: [
          { id: "media-alpha", title: "Alpha" },
          { id: "media-v1", title: "Film" },
        ],
        queueIdentity: "viewer-1",
      },
    );
    // Learning the video's class leaves the track alone in its queue.
    await waitFor(() => expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[3]).toBe(""));
    expect(lastTransportProps()?.onNextInQueue).toBeUndefined();

    fireEvent.ended(stageVideo());
    await act(async () => {});
    expect(lastPlayedID()).toBe("media-alpha");
    expect(vi.mocked(usePlayer).mock.calls.some((call) => call[0] === "media-v1")).toBe(false);
  });

  it("still advances into an issued next of the playing class without a card queue", async () => {
    renderPlayer(
      playerState({
        phase: "playing",
        info: {
          mediaClass: "audio",
          next: {
            mediaID: "media-t2",
            mediaClass: "audio",
            streams: [{ kind: "direct-play", url: "/api/media/t2" }],
          },
        } as PlayerState["info"],
      }),
      { title: "Alpha" },
    );
    fireEvent.ended(stageVideo());
    await waitFor(() => expect(lastPlayedID()).toBe("media-t2"));
  });

  it("shows the audio stage while loading when the queue entry is a track", () => {
    renderPlayer(playerState({ phase: "loading" }), {
      title: "Alpha",
      queue: [
        { id: "media-alpha", title: "Alpha", mediaClass: "audio" },
        { id: "media-v1", title: "Film", mediaClass: "video" },
      ],
      queueIdentity: "viewer-1",
    });
    const stage = document.querySelector(".bf-audio-stage");
    expect(stage).not.toBeNull();
    expect(stage).toHaveTextContent("Alpha");
    expect(stage?.querySelector("[data-visualizer-slot]")).not.toBeNull();
    // Only the track remains, so there is no "1 of 2" and no next to declare.
    expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[3]).toBe("");
  });

  it("keeps the video stage while loading a video or an item of unknown class", () => {
    renderPlayer(playerState({ phase: "loading" }), {
      title: "Alpha",
      queue: [
        { id: "media-alpha", title: "Alpha", mediaClass: "video" },
        { id: "media-t1", title: "Track" },
      ],
      queueIdentity: "viewer-1",
    });
    expect(document.querySelector(".bf-audio-stage")).toBeNull();
    // An entry of unknown class stays queued.
    expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[3]).toBe("media-t1");
  });
});

// An advance resets the engine for the next item, but the audio stage, its
// words and the transport stay up until that item's data arrives (CORE-2363).
describe("PlayerPage server Play Queue", () => {
  const a = { id: "track-a", title: "A", mediaClass: "audio" };
  const b = { id: "track-b", title: "B", mediaClass: "audio" };
  const c = { id: "track-c", title: "C", mediaClass: "audio" };
  const locked = { id: "track-locked", title: "Locked", mediaClass: "audio", locked: true };
  const videoRow = { id: "film", title: "Film", mediaClass: "video" };
  let rows = [a, locked, b, videoRow, c];
  let serverSession = { profileID: "profile-a", revealed: true };
  let queueMedia: GenericMediaInfo | null;
  let mediaSession: typeof serverSession | undefined;
  let readQueue: Mock<
    () => Promise<{ sourceItems: typeof rows; currentSession: typeof serverSession }>
  >;
  let graphql: MockInstance<typeof api.graphql>;
  let pause: MockInstance<HTMLMediaElement["pause"]>;
  let location: ReturnType<typeof useLocation>;
  let go: NavigateFunction;
  const restart = vi.fn();

  const controls = () => vi.mocked(PlayerTransport).mock.lastCall?.[0];
  const playingID = () => vi.mocked(usePlayer).mock.lastCall?.[0];
  const gapless = () => vi.mocked(useGaplessAudio).mock.lastCall?.[0];

  async function mockWithRealTransport() {
    const actual = await vi.importActual<typeof import("../player/PlayerTransport")>(
      "../player/PlayerTransport",
    );
    vi.mocked(PlayerTransport).mockImplementation((props) => <actual.PlayerTransport {...props} />);
  }

  function LocationProbe() {
    location = useLocation();
    go = useNavigate();
    return null;
  }

  function renderQueue(id = b.id, nextID = c.id, search = "?playQueue=profile-a", hosted = false) {
    const videoRef: { current: HTMLVideoElement | null } = { current: null };
    vi.mocked(usePlayer).mockImplementation((mediaID) => ({
      videoRef,
      hlsRef: { current: null },
      state: playerState({
        phase: mediaID ? "playing" : "loading",
        mediaID,
        info: mediaID
          ? {
              mediaClass: "audio",
              next: {
                mediaID: nextID,
                mediaClass: "audio",
                title: "Stale playback-info title",
                streams: [{ kind: "direct-play", url: "/api/next.mp3" }],
              },
            }
          : null,
      }),
      retry: vi.fn(),
      restart,
      switchToHls: vi.fn(() => false as const),
      adjustment: DEFAULT_ADJUSTMENT,
      adjust: vi.fn(),
    }));
    const routes = () => (
      <Routes>
        <Route path="/watch/*" element={<PlayerPage />} />
        <Route path="/search" element={<p>Search results</p>} />
      </Routes>
    );
    const tree = () => (
      <I18nProvider>
        <MemoryRouter initialEntries={[`/watch/${id}${search}`]}>
          <LocationProbe />
          {hosted ? <PlayerHost>{routes()}</PlayerHost> : routes()}
        </MemoryRouter>
      </I18nProvider>
    );
    const view = render(tree());
    return { rerender: () => view.rerender(tree()) };
  }

  beforeEach(() => {
    vi.mocked(usePlayer).mockReset();
    vi.mocked(PlayerTransport).mockClear();
    vi.mocked(useGaplessAudio).mockClear();
    vi.mocked(fetchPrePlayQueue).mockClear();
    viewer.refresh.mockClear();
    restart.mockClear();
    rows = [a, locked, b, videoRow, c];
    serverSession = { profileID: "profile-a", revealed: true };
    queueMedia = null;
    mediaSession = undefined;
    readQueue = vi.fn(async () => ({ sourceItems: rows, currentSession: serverSession }));
    graphql = vi.spyOn(api, "graphql").mockImplementation(async (query) => {
      if (query.startsWith("query MusicQueueMedia")) {
        return { media: queueMedia, currentSession: mediaSession ?? serverSession };
      }
      return readQueue();
    });
    pause = vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
  });

  afterEach(() => {
    viewer.profileID = "profile-a";
    viewer.revealed = true;
    viewer.identity = "viewer-1";
    setIdentityEpochSignal();
    graphql.mockRestore();
    pause.mockRestore();
    vi.mocked(PlayerTransport).mockReset();
    vi.mocked(fetchMedia).mockResolvedValue(null);
  });

  it("starts a server run explicitly from ordinary audio playback without serializing titles", async () => {
    renderQueue(b.id, c.id, "?t=0");
    expect(readQueue).not.toHaveBeenCalled();
    act(() => controls()?.onPlayMusicQueue?.(a.id));
    await waitFor(() => expect(playingID()).toBe(a.id));
    expect(location.search).toBe("?playQueue=profile-a");
    expect(location.state).toEqual({ selectPartyTitle: true });
    expect(vi.mocked(usePlayer).mock.lastCall?.[3]).toBe(b.id);
  });

  it("waits for scoped metadata when starting a queue on the currently playing track", async () => {
    const catalogInfo = { id: a.id, title: "Catalog track title", favorited: false, credits: [] };
    vi.mocked(fetchMedia).mockResolvedValueOnce(catalogInfo);
    const readMedia = deferred<{ media: GenericMediaInfo; currentSession: typeof serverSession }>();
    graphql.mockImplementation(async (query) =>
      query.startsWith("query MusicQueueMedia") ? readMedia.promise : readQueue(),
    );
    renderQueue(a.id, b.id, "?t=0");
    await screen.findByRole("heading", { name: catalogInfo.title });
    act(() => controls()?.onPlayMusicQueue?.(a.id));
    await waitFor(() => expect(controls()?.onNextInQueue).toBeTypeOf("function"));
    expect(screen.queryByRole("heading", { name: catalogInfo.title })).toBeNull();
    await act(async () =>
      readMedia.resolve({
        media: { ...catalogInfo, title: "Scoped title" },
        currentSession: serverSession,
      }),
    );
    expect(screen.getByRole("heading", { name: "Scoped title" })).toBeInTheDocument();
  });

  it("waits for a successful queue read before starting the requested route", async () => {
    readQueue.mockRejectedValueOnce(new Error("network"));
    renderQueue();
    expect(await screen.findByRole("alert")).toHaveTextContent(STRINGS.en.musicQueueReadFailed);
    expect(playingID()).toBe("");
    fireEvent.click(screen.getByRole("button", { name: STRINGS.en.tryAgain }));
    await waitFor(() => expect(playingID()).toBe(b.id));
  });

  it("reloads at B using only the route and moves Next to C", async () => {
    renderQueue();
    await waitFor(() => expect(controls()?.onNextInQueue).toBeTypeOf("function"));
    expect(fetchPrePlayQueue).not.toHaveBeenCalled();
    expect(vi.mocked(usePlayer).mock.lastCall?.[3]).toBe(c.id);
    expect(
      vi
        .mocked(usePlayer)
        .mock.calls.filter(([mediaID]) => mediaID === b.id)
        .every(([, , , nextID]) => nextID === c.id),
    ).toBe(true);
    act(() => controls()?.onNextInQueue?.());
    await waitFor(() => expect(playingID()).toBe(c.id));
    expect(location.search).toBe("?playQueue=profile-a");
    expect(location.state).toEqual({ selectPartyTitle: true });
  });

  it("goes Previous from B to A, skipping a locked row", async () => {
    renderQueue();
    await waitFor(() => expect(controls()?.onPreviousInQueue).toBeTypeOf("function"));
    act(() => controls()?.onPreviousInQueue?.());
    await waitFor(() => expect(playingID()).toBe(a.id));
  });

  it("uses the current row as the cursor even if it becomes locked while playing", async () => {
    renderQueue();
    await waitFor(() => expect(playingID()).toBe(b.id));
    rows = [a, { ...b, locked: true }, locked, c];
    fireEvent.ended(stageVideo());
    await waitFor(() => expect(playingID()).toBe(c.id));
  });

  it("advances to C when a row is removed before the page boundary at B", async () => {
    const prefix = Array.from({ length: 199 }, (_, i) => ({ ...a, id: `prior-${i}` }));
    rows = [...prefix, b, c, { ...c, id: "track-d" }];
    let removeBeforeSecondPage = false;
    const offsets: number[] = [];
    graphql.mockImplementation(async (query, vars) => {
      if (query.startsWith("query MusicQueueMedia")) {
        return { media: null, currentSession: serverSession };
      }
      const { offset, limit } = vars as { offset: number; limit: number };
      offsets.push(offset);
      if (offset > 0 && removeBeforeSecondPage) {
        rows = rows.slice(1);
        removeBeforeSecondPage = false;
      }
      return { sourceItems: rows.slice(offset, offset + limit), currentSession: serverSession };
    });

    renderQueue();
    await waitFor(() => expect(controls()?.onNextInQueue).toBeTypeOf("function"));
    removeBeforeSecondPage = true;
    offsets.length = 0;
    act(() => controls()?.onNextInQueue?.());
    await waitFor(() => expect(playingID()).toBe(c.id));
    expect(offsets).toEqual([0, 199, 0, 199]);
  });

  it("finishes the current track when another device removes it before ended", async () => {
    renderQueue();
    await waitFor(() => expect(controls()?.onNextInQueue).toBeTypeOf("function"));
    rows = [a, c];
    fireEvent.ended(stageVideo());
    await waitFor(() => expect(controls()?.onNextInQueue).toBeUndefined());
    expect(controls()?.onPreviousInQueue).toBeUndefined();
    expect(playingID()).toBe(b.id);
  });

  it.each([
    ["missing current", [a, c]],
    ["empty queue", []],
    ["end of queue", [a, b]],
  ])("stops automatic advance for %s, even with an issued next", async (_, serverRows) => {
    rows = serverRows;
    renderQueue();
    await waitFor(() => expect(playingID()).toBe(b.id));
    expect(vi.mocked(usePlayer).mock.lastCall?.[3]).toBe("");
    expect(controls()?.onNextInQueue).toBeUndefined();
    expect(gapless()?.enabled).toBe(false);
    fireEvent.ended(stageVideo());
    await waitFor(() => expect(readQueue).toHaveBeenCalledTimes(2));
    expect(playingID()).toBe(b.id);
  });

  it("withholds a stale gapless candidate and advances to the new server successor", async () => {
    const d = { id: "track-d", title: "D", mediaClass: "audio" };
    queueMedia = { id: b.id, title: "Current track B", favorited: false, credits: [] };
    renderQueue();
    await screen.findByRole("heading", { name: "Current track B" });
    expect(vi.mocked(usePlayer).mock.lastCall?.[3]).toBe(c.id);
    expect(gapless()?.advanceID).toBe(c.id);
    rows = [a, b, d, c];
    expect(gapless()?.waitForAdvance).toBe(true);
    fireEvent.ended(stageVideo());
    await waitFor(() => expect(playingID()).toBe(d.id));
    expect(readQueue).toHaveBeenCalledTimes(2);
  });

  it.each([false, true])(
    "waits past C’s scheduled start for the final server decision (B remains: %s)",
    async (keepCurrent) => {
      const start = vi.fn();
      const decodeAudioData = vi.fn(async () => ({ duration: 2 }));
      const clock = { currentTime: 5 };
      vi.stubGlobal(
        "AudioContext",
        class {
          state = "running";
          get currentTime() {
            return clock.currentTime;
          }
          createGain = () => ({ gain: { value: 1 }, connect: vi.fn() });
          createConstantSource = () => ({ offset: { value: 0 }, connect: vi.fn(), start: vi.fn() });
          createBufferSource = () => ({
            connect: vi.fn(),
            disconnect: vi.fn(),
            start,
            stop: vi.fn(),
          });
          close = async () => {};
          decodeAudioData = decodeAudioData;
        },
      );
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => new Response(new ArrayBuffer(8))),
      );
      const createElement = document.createElement.bind(document);
      const probe = vi.spyOn(document, "createElement").mockImplementation(((tag: string) => {
        const element = createElement(tag);
        if (element instanceof HTMLAudioElement) {
          element.load = vi.fn();
          Object.defineProperty(element, "duration", { value: 2 });
          Object.defineProperty(element, "src", {
            set: () => queueMicrotask(() => element.dispatchEvent(new Event("loadedmetadata"))),
          });
        }
        return element;
      }) as typeof document.createElement);
      try {
        renderQueue();
        const video = stageVideo();
        Object.defineProperties(video, {
          paused: { value: false },
          duration: { value: 10 },
          ended: { get: () => video.currentTime === 10 },
        });
        video.currentTime = 9.2;
        await waitFor(() => expect(decodeAudioData).toHaveBeenCalledOnce());
        expect(gapless()?.advanceID).toBe(c.id);
        const finalRead = deferred<{
          sourceItems: typeof rows;
          currentSession: typeof serverSession;
        }>();
        readQueue.mockReturnValueOnce(finalRead.promise);
        video.currentTime = 10;
        fireEvent.ended(video);
        await waitFor(() => expect(readQueue).toHaveBeenCalledTimes(2));
        // The catalog bridge would already have started C at 5.8.
        clock.currentTime = 6;
        expect(start).not.toHaveBeenCalled();
        expect(location.pathname).toBe(`/watch/${b.id}`);

        await act(async () =>
          finalRead.resolve({
            sourceItems: keepCurrent ? [a, b, c] : [a, c],
            currentSession: serverSession,
          }),
        );
        if (keepCurrent) {
          expect(start).toHaveBeenCalledExactlyOnceWith(6, 0, 2);
          expect(location.pathname).toBe(`/watch/${c.id}`);
        } else {
          expect(start).not.toHaveBeenCalled();
          expect(location.pathname).toBe(`/watch/${b.id}`);
          expect(controls()?.onNextInQueue).toBeUndefined();
        }
        expect(readQueue).toHaveBeenCalledTimes(2);
      } finally {
        probe.mockRestore();
        vi.unstubAllGlobals();
      }
    },
  );

  it.each(["Next", "ended"])("retries the failed %s without unloading B", async (trigger) => {
    renderQueue();
    await waitFor(() => expect(controls()?.onNextInQueue).toBeTypeOf("function"));
    const originalVideo = stageVideo();
    readQueue.mockRejectedValueOnce(new Error("network"));
    if (trigger === "Next") act(() => controls()?.onNextInQueue?.());
    else fireEvent.ended(originalVideo);
    expect(await screen.findByRole("alert")).toHaveTextContent(STRINGS.en.musicQueueReadFailed);
    expect(stageVideo()).toBe(originalVideo);
    expect(playingID()).toBe(b.id);
    expect(controls()?.onNextInQueue).toBeTypeOf("function");
    fireEvent.click(screen.getByRole("button", { name: STRINGS.en.tryAgain }));
    await waitFor(() => expect(playingID()).toBe(c.id));
    expect(screen.queryByText(STRINGS.en.musicQueueReadFailed)).not.toBeInTheDocument();
    expect(readQueue).toHaveBeenCalledTimes(3);
  });

  it("updates transport navigation when reopening the queue shows a newly appended track", async () => {
    await mockWithRealTransport();
    rows = [a, b];
    renderQueue();
    const queueButton = await screen.findByRole("button", { name: STRINGS.en.musicQueueTitle });
    expect(controls()?.onNextInQueue).toBeUndefined();
    fireEvent.click(queueButton);
    const panel = await screen.findByRole("dialog", { name: STRINGS.en.musicQueueTitle });
    expect(await within(panel).findByText("B")).toHaveAttribute("aria-current", "true");
    fireEvent.click(within(panel).getByRole("button", { name: STRINGS.en.dialogClose }));

    rows = [b, c];
    fireEvent.click(queueButton);
    expect(await within(screen.getByRole("dialog")).findByText("C")).toBeInTheDocument();
    expect(controls()?.onPreviousInQueue).toBeUndefined();
    expect(controls()?.onNextInQueue).toBeTypeOf("function");
    act(() => controls()?.onNextInQueue?.());
    await waitFor(() => expect(playingID()).toBe(c.id));
  });

  it("retires a pending ended read when Play queue supersedes it on the same track", async () => {
    await mockWithRealTransport();
    rows = [b, c];
    renderQueue();
    fireEvent.click(await screen.findByRole("button", { name: STRINGS.en.musicQueueTitle }));
    const playQueue = await screen.findByRole("button", { name: STRINGS.en.musicQueuePlay });
    await waitFor(() => expect(readQueue).toHaveBeenCalledTimes(2));
    const pending = deferred<{ sourceItems: typeof rows; currentSession: typeof serverSession }>();
    readQueue.mockReturnValueOnce(pending.promise);
    fireEvent.ended(stageVideo());
    await waitFor(() => expect(readQueue).toHaveBeenCalledTimes(3));
    fireEvent.click(playQueue);
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await act(async () => pending.resolve({ sourceItems: rows, currentSession: serverSession }));
    expect(playingID()).toBe(b.id);
    expect(restart).toHaveBeenCalledExactlyOnceWith(0);
    expect(screen.queryByText(STRINGS.en.musicQueueReadFailed)).not.toBeInTheDocument();
  });

  it("starts the first queue row after docking while Play queue is pending", async () => {
    await mockWithRealTransport();
    renderQueue(b.id, c.id, "?playQueue=profile-a", true);
    fireEvent.click(await screen.findByRole("button", { name: STRINGS.en.musicQueueTitle }));
    const playQueue = await screen.findByRole("button", { name: STRINGS.en.musicQueuePlay });
    const pending = deferred<Awaited<ReturnType<typeof readQueue>>>();
    readQueue.mockReturnValueOnce(pending.promise);
    fireEvent.click(playQueue);
    expect(readQueue).toHaveBeenCalledTimes(3);

    await act(async () => go("/search"));
    await screen.findByRole("region", { name: "Now playing" });
    await act(async () => pending.resolve({ sourceItems: rows, currentSession: serverSession }));

    expect(playingID()).toBe(a.id);
    expect(location.pathname).toBe("/search");
    expect(screen.getByText("Search results")).toBeInTheDocument();
  });

  it.each(["same identity", "relock", "profile switch"])(
    "revalidates an ended advance across focus and docking with %s",
    async (change) => {
      const confirm = vi.fn();
      vi.mocked(useGaplessAudio).mockReturnValue(confirm);
      try {
        renderQueue(b.id, c.id, "?playQueue=profile-a", true);
        await waitFor(() => expect(playingID()).toBe(b.id));
        type QueueResponse = Awaited<ReturnType<typeof readQueue>>;
        const pending = deferred<QueueResponse>();
        readQueue.mockReturnValueOnce(pending.promise);
        fireEvent.ended(stageVideo());
        await waitFor(() => expect(readQueue).toHaveBeenCalledTimes(2));
        const currentSession =
          change === "relock"
            ? { ...serverSession, revealed: false }
            : change === "profile switch"
              ? { ...serverSession, profileID: "profile-b" }
              : serverSession;
        const fresh = deferred<QueueResponse>();
        readQueue.mockReturnValueOnce(fresh.promise);
        fireEvent.focus(window);
        expect(readQueue).toHaveBeenCalledTimes(2);
        await act(async () => go("/search"));
        await screen.findByRole("region", { name: "Now playing" });
        await act(async () =>
          pending.resolve({ sourceItems: rows, currentSession: serverSession }),
        );
        expect(playingID()).toBe(b.id);
        expect(confirm).not.toHaveBeenCalled();
        expect(readQueue).toHaveBeenCalledTimes(3);

        await act(async () => fresh.resolve({ sourceItems: rows, currentSession }));
        if (change === "same identity") {
          expect(playingID()).toBe(c.id);
          expect(confirm).toHaveBeenCalledExactlyOnceWith(c.id);
        } else {
          expect(screen.queryByRole("region", { name: "Now playing" })).not.toBeInTheDocument();
          expect(confirm).not.toHaveBeenCalled();
        }
        expect(location.pathname).toBe("/search");
        expect(readQueue).toHaveBeenCalledTimes(3);
      } finally {
        vi.mocked(useGaplessAudio).mockReset();
      }
    },
  );

  it("keeps the docked track and retries when the ended read fails", async () => {
    renderQueue(b.id, c.id, "?playQueue=profile-a", true);
    await waitFor(() => expect(playingID()).toBe(b.id));
    const video = stageVideo();
    await act(async () => go("/search"));
    await screen.findByRole("region", { name: "Now playing" });
    readQueue.mockRejectedValueOnce(new Error("network"));
    fireEvent.ended(video);
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(STRINGS.en.musicQueueReadFailed);
    expect(stageVideo()).toBe(video);
    expect(playingID()).toBe(b.id);
    fireEvent.click(within(alert).getByRole("button", { name: STRINGS.en.tryAgain }));
    await waitFor(() => expect(playingID()).toBe(c.id));
    expect(location.pathname).toBe("/search");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("keeps a failed docked advance retryable after a successful focus refresh", async () => {
    renderQueue(b.id, c.id, "?playQueue=profile-a", true);
    await waitFor(() => expect(playingID()).toBe(b.id));
    const video = stageVideo();
    await act(async () => go("/search"));
    await screen.findByRole("region", { name: "Now playing" });
    readQueue.mockRejectedValueOnce(new Error("network"));
    fireEvent.ended(video);
    expect(await screen.findByRole("alert")).toHaveTextContent(STRINGS.en.musicQueueReadFailed);

    await act(async () => fireEvent.focus(window));
    expect(readQueue).toHaveBeenCalledTimes(3);
    expect(playingID()).toBe(b.id);
    const pending = deferred<Awaited<ReturnType<typeof readQueue>>>();
    readQueue.mockReturnValueOnce(pending.promise);
    const retry = within(screen.getByRole("alert")).getByRole("button", {
      name: STRINGS.en.tryAgain,
    });
    fireEvent.click(retry);
    fireEvent.focus(window);
    expect(screen.getByRole("alert")).toHaveTextContent(STRINGS.en.musicQueueReadFailed);
    expect(readQueue).toHaveBeenCalledTimes(4);
    await act(async () => pending.resolve({ sourceItems: rows, currentSession: serverSession }));
    await waitFor(() => expect(playingID()).toBe(c.id));
    expect(readQueue).toHaveBeenCalledTimes(5);
    expect(location.pathname).toBe("/search");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows the scoped metadata credit in playback details", async () => {
    await mockWithRealTransport();
    const metadataAttribution = {
      providerName: { en: "Fixture source" },
      text: { en: "Metadata provided by Fixture" },
      url: "https://fixture.example.test/",
    };
    graphql.mockImplementation(async (query) => {
      if (query.startsWith("query MusicQueueMedia")) {
        return {
          media: {
            id: b.id,
            title: "Track B",
            credits: [],
            metadataAttribution: query.includes("metadataAttribution { providerName text url }")
              ? metadataAttribution
              : undefined,
          },
          currentSession: serverSession,
        };
      }
      return readQueue();
    });
    renderQueue();
    await screen.findByRole("heading", { name: "Track B", level: 1 });
    fireEvent.click(screen.getByRole("button", { name: STRINGS.en.playbackSettings }));
    fireEvent.click(screen.getByRole("button", { name: STRINGS.en.playbackDetails }));
    expect(await screen.findByRole("link", { name: metadataAttribution.text.en })).toHaveAttribute(
      "href",
      metadataAttribution.url,
    );
  });

  it.each(["broadcast", "focus", "pageshow"])(
    "conceals now-playing details on %s until the live identity is checked",
    async (trigger) => {
      queueMedia = { id: b.id, title: "Restricted track", favorited: false, credits: [] };
      renderQueue();
      await waitFor(() => expect(controls()?.mediaTitle).toBe("Restricted track"));
      expect(stageVideo().paused).toBe(true);
      const pending = deferred<{
        sourceItems: typeof rows;
        currentSession: typeof serverSession;
      }>();
      readQueue.mockReturnValueOnce(pending.promise);

      if (trigger === "broadcast") notifyIdentityChange();
      else if (trigger === "focus") fireEvent.focus(window);
      else {
        fireEvent(window, new Event("pagehide"));
        expect(screen.queryByText("Restricted track")).not.toBeInTheDocument();
        fireEvent(window, new PageTransitionEvent("pageshow", { persisted: true }));
      }
      await waitFor(() => expect(readQueue).toHaveBeenCalledTimes(2));
      expect(screen.queryByText("Restricted track")).not.toBeInTheDocument();
      expect(controls()?.mediaTitle).toBe("");
      const changed =
        trigger === "focus"
          ? { profileID: "profile-a", revealed: false }
          : { profileID: "profile-b", revealed: true };
      await act(async () => pending.resolve({ sourceItems: [], currentSession: changed }));
      expect(viewer.refresh).toHaveBeenCalledOnce();
      expect(screen.queryByText("Restricted track")).not.toBeInTheDocument();
      expect(playingID()).toBe("");
    },
  );

  it("rejects now-playing metadata read under a different live profile", async () => {
    queueMedia = { id: b.id, title: "Other profile title", favorited: false, credits: [] };
    mediaSession = { profileID: "profile-b", revealed: true };
    renderQueue();
    await waitFor(() => expect(viewer.refresh).toHaveBeenCalledOnce());
    expect(document.body).not.toHaveTextContent("Other profile title");
    expect(playingID()).toBe("");
    expect(document.querySelector("video")).toBeNull();
  });

  it.each(["relock", "profile switch"])(
    "refreshes the identity and drops now-playing details on a remote %s",
    async (change) => {
      queueMedia = { id: b.id, title: "Restricted track", favorited: false, credits: [] };
      renderQueue();
      await waitFor(() => expect(controls()?.mediaTitle).toBe("Restricted track"));
      serverSession =
        change === "relock"
          ? { profileID: "profile-a", revealed: false }
          : { profileID: "profile-b", revealed: true };
      fireEvent.ended(stageVideo());
      await waitFor(() => expect(viewer.refresh).toHaveBeenCalledOnce());
      expect(document.body).not.toHaveTextContent("Restricted track");
      expect(location.pathname).toBe(`/watch/${b.id}`);
      expect(playingID()).toBe("");
    },
  );

  it.each(["relock", "profile switch"])(
    "drops live titles and stale reads on %s",
    async (change) => {
      const oldEpoch = new AbortController();
      setIdentityEpochSignal(oldEpoch.signal);
      queueMedia = {
        id: b.id,
        title: "Restricted track",
        favorited: false,
        credits: [],
      };
      const view = renderQueue();
      await waitFor(() => expect(controls()?.mediaTitle).toBe("Restricted track"));
      expect(screen.getByRole("heading", { name: "Restricted track" })).toBeInTheDocument();

      const finishing = deferred<{
        sourceItems: typeof rows;
        currentSession: typeof serverSession;
      }>();
      readQueue.mockReturnValueOnce(finishing.promise);
      fireEvent.ended(stageVideo());
      await waitFor(() => expect(readQueue).toHaveBeenCalledTimes(2));
      const newEpoch = new AbortController();
      oldEpoch.abort();
      setIdentityEpochSignal(newEpoch.signal);
      const relocking = change === "relock";
      viewer.revealed = false;
      viewer.profileID = relocking ? "profile-a" : "profile-b";
      viewer.identity = relocking ? "viewer-locked" : "viewer-other";
      const oldSession = serverSession;
      serverSession = { profileID: viewer.profileID, revealed: false };
      queueMedia = null;
      rows = [];
      view.rerender();
      await act(async () =>
        finishing.resolve({ sourceItems: [a, b, c], currentSession: oldSession }),
      );

      expect(document.body).not.toHaveTextContent("Restricted track");
      expect(location.pathname).toBe(`/watch/${b.id}`);
      if (relocking) {
        await waitFor(() => expect(playingID()).toBe(b.id));
        expect(controls()?.mediaTitle).toBe("");
        expect(controls()?.onNextInQueue).toBeUndefined();
      } else {
        expect(playingID()).toBe("");
        expect(readQueue).toHaveBeenCalledTimes(2);
      }
    },
  );
});

describe("PlayerPage audio advance (CORE-2363)", () => {
  const queue = [
    { id: "media-prev", title: "Before" },
    { id: "media-alpha", title: "Alpha" },
    { id: "media-next", title: "After" },
  ];
  const audio = { mediaClass: "audio" } as PlayerState["info"];
  const videoRef: { current: HTMLVideoElement | null } = { current: null };
  let states: Record<string, PlayerState> = {};
  let releaseNext: (info: never) => void = () => undefined;

  function track(id: string, title: string, artist: string) {
    return {
      id,
      title,
      artworkSlots: { poster: `/art/${id}.jpg` },
      credits: [{ entityID: `p-${id}`, title: artist, kind: "artist", archetype: "person" }],
    } as never;
  }

  beforeEach(() => {
    vi.mocked(usePlayer).mockReset();
    vi.mocked(PlayerTransport).mockClear();
    states = {
      "media-alpha": playerState({ phase: "playing", mediaID: "media-alpha", info: audio }),
    };
    // The engine for an item it has not loaded yet: playback-info in flight.
    vi.mocked(usePlayer).mockImplementation((mediaID) => ({
      videoRef,
      hlsRef: { current: null },
      state: states[mediaID] ?? playerState({ phase: "loading", mediaID: undefined }),
      retry: vi.fn(),
      restart: vi.fn(),
      switchToHls: vi.fn(() => false as const),
      adjustment: DEFAULT_ADJUSTMENT,
      adjust: vi.fn(),
    }));
    // The next item's media read waits until the test releases it.
    vi.mocked(fetchMedia).mockImplementation((id) =>
      id === "media-next"
        ? new Promise((resolve) => {
            releaseNext = resolve;
          })
        : Promise.resolve(track("media-alpha", "Alpha", "Ann")),
    );
  });
  afterEach(() => vi.mocked(fetchMedia).mockResolvedValue(null));

  // The queue reports no classes, so only the playing item's info names it a track.
  function renderAdvance() {
    const tree = () => (
      <I18nProvider>
        <MemoryRouter
          initialEntries={[
            {
              pathname: "/watch/media-alpha",
              state: { skipPrePlay: true, title: "Alpha", queue, queueIdentity: "viewer-1" },
            },
          ]}
        >
          <Routes>
            <Route path="/watch/*" element={<PlayerPage />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>
    );
    const view = render(tree());
    return () => view.rerender(tree());
  }

  async function advanceFromFirstTrack(): Promise<HTMLElement> {
    const stage = document.querySelector<HTMLElement>(".bf-audio-stage");
    if (!stage) throw new Error("no audio stage");
    expect(await within(stage).findByText("Ann")).toBeInTheDocument();
    presentFirstFrame();
    expect(screen.queryByRole("status")).toBeNull();
    act(() => vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0]?.onNextInQueue?.());
    await waitFor(() => expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[0]).toBe("media-next"));
    return stage;
  }

  it("keeps the stage and the item on it until the next item's data arrives, then swaps", async () => {
    renderAdvance();
    const stage = await advanceFromFirstTrack();

    // Loading the next item: the same stage, the previous words and picture,
    // no spinner, and the transport still mounted.
    expect(document.querySelector(".bf-audio-stage")).toBe(stage);
    expect(within(stage).getByText("Ann")).toBeInTheDocument();
    expect(within(stage).getByText("2 of 3")).toBeInTheDocument();
    expect(stage.querySelector(".bf-audio-artwork img")).toHaveAttribute(
      "src",
      "/art/media-alpha.jpg",
    );
    expect(screen.queryByRole("status")).toBeNull();
    expect(vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0]?.state.phase).toBe("loading");

    await act(async () => releaseNext(track("media-next", "After", "Bo")));
    expect(await within(stage).findByText("Bo")).toBeInTheDocument();
    expect(within(stage).getByText("3 of 3")).toBeInTheDocument();
    expect(document.querySelector(".bf-audio-stage")).toBe(stage);
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("swaps when the next item starts playing before its media read answers", async () => {
    const rerender = renderAdvance();
    const stage = await advanceFromFirstTrack();
    expect(within(stage).getByText("Ann")).toBeInTheDocument();

    states["media-next"] = playerState({ phase: "playing", mediaID: "media-next", info: audio });
    rerender();
    expect(document.querySelector(".bf-audio-stage")).toBe(stage);
    expect(within(stage).getByText("3 of 3")).toBeInTheDocument();
  });

  it("drops the stage once the next item's playback-info names a video", async () => {
    const rerender = renderAdvance();
    await advanceFromFirstTrack();
    expect(document.querySelector(".bf-audio-stage")).not.toBeNull();

    states["media-next"] = playerState({
      phase: "playing",
      mediaID: "media-next",
      info: { mediaClass: "video" } as PlayerState["info"],
    });
    rerender();
    expect(document.querySelector(".bf-audio-stage")).toBeNull();
  });

  it("shows a small cue, not a cover, while an audio item loads", () => {
    renderPlayer(playerState({ phase: "loading" }), {
      title: "Alpha",
      queue: [{ id: "media-alpha", title: "Alpha", mediaClass: "audio" }],
      queueIdentity: "viewer-1",
    });
    expect(document.querySelector(".bf-audio-stage")).not.toBeNull();
    const cue = screen.getByRole("status");
    expect(cue).toHaveTextContent("Loading…");
    expect(cue).not.toHaveClass("inset-0");
  });
});

describe("PlayerPage Back", () => {
  function renderAt(entries: string[]) {
    vi.mocked(usePlayer).mockReturnValue({
      videoRef: { current: null },
      hlsRef: { current: null },
      state: playerState({ phase: "playing" }),
      retry: vi.fn(),
      restart: vi.fn(),
      switchToHls: vi.fn(() => false as const),
      adjustment: DEFAULT_ADJUSTMENT,
      adjust: vi.fn(),
    });
    render(
      <I18nProvider>
        <MemoryRouter
          initialEntries={entries.map((pathname) => ({ pathname, state: { skipPrePlay: true } }))}
          initialIndex={entries.length - 1}
        >
          <Routes>
            <Route path="/" element={<p>home route</p>} />
            <Route path="/entity/:entityId" element={<p>entity route</p>} />
            <Route path="/watch/*" element={<PlayerPage />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>,
    );
  }

  it("returns to the page the player was opened from", () => {
    renderAt(["/", "/entity/e1", "/watch/media-alpha"]);
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByText("entity route")).toBeInTheDocument();
  });

  it("falls back to Browse on a deep link with no in-app history", () => {
    renderAt(["/watch/media-alpha"]);
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByText("home route")).toBeInTheDocument();
  });
});

describe("PlayerPage Version choice (CORE-1676)", () => {
  beforeEach(() => {
    vi.mocked(usePlayer).mockReset();
    vi.mocked(PlayerTransport).mockClear();
    window.localStorage.clear();
  });

  it("asks for the profile's remembered Version, and a new pick is remembered and reloads", async () => {
    writeVersionChoice("profile-a", "media-alpha", "v-remembered");
    const { retry } = renderPlayer(
      playerState({ phase: "playing", info: { mediaClass: "video" } as PlayerState["info"] }),
    );
    expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[4]).toBe("v-remembered");

    const props = vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0];
    act(() => props?.onSelectVersion?.("v-uhd"));

    expect(readVersionChoice("profile-a", "media-alpha")).toBe("v-uhd");
    expect(retry).toHaveBeenCalledOnce();
    await waitFor(() => expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[4]).toBe("v-uhd"));

    // Choosing the Version already playing does nothing.
    act(() => vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0]?.onSelectVersion?.("v-uhd"));
    expect(retry).toHaveBeenCalledOnce();
  });

  // Review finding P2-3: the detail page's Version rides the nav state with
  // its tracks, ahead of storage, so blocked storage cannot split them.
  it("plays the Version the detail page sent with its tracks, ahead of the remembered one", async () => {
    writeVersionChoice("profile-a", "media-alpha", "v-remembered");
    const { retry } = renderPlayer(
      playerState({ phase: "playing", info: { mediaClass: "video" } as PlayerState["info"] }),
      { tracks: { mediaID: "media-alpha", versionID: "v-sent", audioIndex: 2 } },
    );
    expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[4]).toBe("v-sent");
    expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[5]).toEqual({
      mediaID: "media-alpha",
      versionID: "v-sent",
      audioIndex: 2,
    });

    // A pick in the player still wins for this start.
    act(() => vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0]?.onSelectVersion?.("v-uhd"));
    expect(retry).toHaveBeenCalledOnce();
    await waitFor(() => expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[4]).toBe("v-uhd"));
  });

  it("leaves the Version to the server when the detail page sent Auto", () => {
    writeVersionChoice("profile-a", "media-alpha", "v-remembered");
    renderPlayer(
      playerState({ phase: "playing", info: { mediaClass: "video" } as PlayerState["info"] }),
      { tracks: { mediaID: "media-alpha", versionID: "" } },
    );
    expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[4]).toBe("");
  });
});

// CORE-1701: the pre-play queue plays before the title unless the viewer
// arrived by a timestamped link or an advance.
describe("PlayerPage pre-play", () => {
  beforeEach(() => {
    vi.mocked(usePlayer).mockReset();
    vi.mocked(usePlayer).mockReturnValue({
      videoRef: { current: null },
      hlsRef: { current: null },
      state: playerState({ phase: "playing" }),
      retry: vi.fn(),
      restart: vi.fn(),
      switchToHls: vi.fn(() => false as const),
      adjustment: DEFAULT_ADJUSTMENT,
      adjust: vi.fn(),
    });
    vi.mocked(fetchPrePlayQueue).mockReset();
  });

  function renderEntry(entry: { pathname: string; search?: string; state?: unknown }) {
    render(
      <I18nProvider>
        <MemoryRouter initialEntries={[entry]}>
          <Routes>
            <Route path="/watch/*" element={<PlayerPage />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>,
    );
  }

  it("plays the pre-play queue first, then the title on Skip all", async () => {
    vi.mocked(fetchPrePlayQueue).mockResolvedValue([
      { mediaID: "clip-1", title: "Intro clip", durationSeconds: 10, role: "clip", label: "" },
      {
        mediaID: "s-1",
        title: "Suggested one",
        durationSeconds: 90,
        role: "suggested",
        label: "Preview",
      },
    ]);
    renderEntry({ pathname: "/watch/media-alpha" });
    const stage = await screen.findByRole("region", { name: "pre-play stage" });
    expect(stage).toHaveTextContent("Intro clip, Suggested one");
    expect(vi.mocked(fetchPrePlayQueue).mock.calls[0]?.[0]).toBe("media-alpha");
    expect(usePlayer).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Skip all" }));
    expect(screen.queryByRole("region", { name: "pre-play stage" })).not.toBeInTheDocument();
    expect(usePlayer).toHaveBeenCalled();
  });

  it("starts the title when the queue is empty or cannot load", async () => {
    vi.mocked(fetchPrePlayQueue).mockRejectedValue(new Error("offline"));
    renderEntry({ pathname: "/watch/media-alpha" });
    await waitFor(() => expect(usePlayer).toHaveBeenCalled());
    expect(screen.queryByRole("region", { name: "pre-play stage" })).not.toBeInTheDocument();
  });

  it("skips pre-play for a timestamped link and for an advance", () => {
    renderEntry({ pathname: "/watch/media-alpha", search: "?t=42" });
    expect(fetchPrePlayQueue).not.toHaveBeenCalled();
    expect(usePlayer).toHaveBeenCalled();
  });
});

describe("PlayerPage subtitle appearance prefs (CORE-1681)", () => {
  beforeEach(() => {
    vi.mocked(usePlayer).mockReset();
    vi.mocked(setClientPref).mockReset();
  });
  afterEach(() => vi.mocked(fetchClientPrefs).mockResolvedValue([]));

  function transportProps() {
    const props = vi.mocked(PlayerTransport).mock.lastCall?.[0];
    if (!props) throw new Error("PlayerTransport not rendered");
    return props;
  }

  it("keeps a choice made before the Profile read returns", async () => {
    let answer: (prefs: { key: string; value: string }[]) => void = () => {};
    vi.mocked(fetchClientPrefs).mockReturnValue(new Promise((resolve) => (answer = resolve)));
    vi.mocked(setClientPref).mockResolvedValue({ key: "" });
    renderPlayer(playerState({ phase: "playing" }));
    act(() => transportProps().onSubtitleAppearanceChange?.("color", "#ffff00"));
    await act(async () =>
      answer([
        { key: "subtitle.color", value: "#00ff00" },
        { key: "subtitle.font", value: "serif" },
      ]),
    );
    expect(transportProps().subtitleAppearance).toMatchObject({ color: "#ffff00", font: "serif" });
  });

  it("clears every key on Reset, one write at a time", async () => {
    let finishFirst: () => void = () => {};
    vi.mocked(setClientPref)
      .mockReturnValueOnce(new Promise((resolve) => (finishFirst = () => resolve({ key: "" }))))
      .mockResolvedValue({ key: "" });
    renderPlayer(playerState({ phase: "playing" }));
    act(() => transportProps().onSubtitleAppearanceReset?.());
    await waitFor(() => expect(setClientPref).toHaveBeenCalledTimes(1));
    await act(async () => finishFirst());
    await waitFor(() => expect(setClientPref).toHaveBeenCalledTimes(7));
    expect(vi.mocked(setClientPref).mock.calls.map(([key, value]) => `${key}=${value}`)).toEqual([
      "subtitle.size=",
      "subtitle.background=",
      "subtitle.font=",
      "subtitle.color=",
      "subtitle.opacity=",
      "subtitle.edge=",
      "subtitle.position=",
    ]);
  });

  it("drops writes still queued when the identity epoch ends", async () => {
    const epoch = new AbortController();
    setIdentityEpochSignal(epoch.signal);
    let finishFirst: () => void = () => {};
    vi.mocked(setClientPref)
      .mockReturnValueOnce(new Promise((resolve) => (finishFirst = () => resolve({ key: "" }))))
      .mockResolvedValue({ key: "" });
    try {
      renderPlayer(playerState({ phase: "playing" }));
      act(() => transportProps().onSubtitleAppearanceReset?.());
      await waitFor(() => expect(setClientPref).toHaveBeenCalledTimes(1));
      epoch.abort();
      await act(async () => finishFirst());
      expect(setClientPref).toHaveBeenCalledTimes(1);
    } finally {
      setIdentityEpochSignal();
    }
  });
});

// CORE-1659: key and tempo controls for a karaoke song.
describe("PlayerPage karaoke key and tempo controls", () => {
  const ranges = {
    pitchShiftSemitonesMin: -6,
    pitchShiftSemitonesMax: 6,
    tempoMin: 0.75,
    tempoMax: 1.25,
  };
  // A synthetic karaoke song: an audio item the server composes a picture for.
  const karaokeInfo = {
    mediaClass: "audio",
    reason: "composed-picture",
    streams: [],
  } as unknown as PlaybackInfo;

  beforeEach(() => {
    vi.mocked(usePlayer).mockReset();
    vi.mocked(fetchPlaybackAdjustmentRanges).mockResolvedValue(ranges);
  });
  afterEach(() => vi.mocked(fetchPlaybackAdjustmentRanges).mockResolvedValue(null));

  it("shows key and tempo controls for a karaoke song when the server advertises them", async () => {
    renderPlayer(playerState({ phase: "playing", info: karaokeInfo }));
    const group = await screen.findByRole("group", { name: "Key and tempo" });
    expect(group).toHaveTextContent("Key 0");
    expect(group).toHaveTextContent("Tempo 100%");
    for (const name of [
      "Lower the key by a semitone",
      "Raise the key by a semitone",
      "Reset the key",
      "Slow down by 5%",
      "Speed up by 5%",
      "Reset the tempo",
    ]) {
      expect(screen.getByRole("button", { name })).toBeInTheDocument();
    }
    expect(within(group).getByRole("status")).toHaveTextContent("Key 0, Tempo 100%");
  });

  it("hides the controls when the server does not advertise the capability", async () => {
    vi.mocked(fetchPlaybackAdjustmentRanges).mockResolvedValue(null);
    renderPlayer(playerState({ phase: "playing", info: karaokeInfo }));
    await waitFor(() => expect(fetchPlaybackAdjustmentRanges).toHaveBeenCalled());
    await act(async () => {});
    expect(screen.queryByRole("group", { name: "Key and tempo" })).not.toBeInTheDocument();
  });

  it("hides the controls for a Media that is not a karaoke song", async () => {
    renderPlayer(
      playerState({
        phase: "playing",
        info: {
          mediaClass: "video",
          reason: "direct-play",
          streams: [],
        } as unknown as PlaybackInfo,
      }),
    );
    await waitFor(() => expect(fetchPlaybackAdjustmentRanges).toHaveBeenCalled());
    await act(async () => {});
    expect(screen.queryByRole("group", { name: "Key and tempo" })).not.toBeInTheDocument();
  });

  it("applies a key and a tempo step through the player", async () => {
    const { adjust } = renderPlayer(playerState({ phase: "playing", info: karaokeInfo }));
    fireEvent.click(await screen.findByRole("button", { name: "Raise the key by a semitone" }));
    expect(adjust).toHaveBeenLastCalledWith({ pitchShiftSemitones: 1, tempo: 1 });
    fireEvent.click(screen.getByRole("button", { name: "Slow down by 5%" }));
    expect(adjust).toHaveBeenLastCalledWith({ pitchShiftSemitones: 0, tempo: 0.95 });
  });

  it("shows the applied key and tempo the server echoed", async () => {
    const adjustment = { pitchShiftSemitones: 2, tempo: 0.95 };
    renderPlayer(
      playerState({ phase: "playing", info: { ...karaokeInfo, adjustment }, adjustment }),
      undefined,
      adjustment,
    );
    const group = await screen.findByRole("group", { name: "Key and tempo" });
    expect(group).toHaveTextContent("Key +2");
    expect(group).toHaveTextContent("Tempo 95%");
    expect(group).not.toHaveTextContent("Not applied");
    expect(within(group).getByRole("status")).toHaveTextContent("Key +2, Tempo 95%");
  });

  it("says Not applied when the server did not echo the adjustment", async () => {
    const adjustment = { pitchShiftSemitones: 1, tempo: 1 };
    renderPlayer(
      playerState({ phase: "playing", info: karaokeInfo, adjustment }),
      undefined,
      adjustment,
    );
    const group = await screen.findByRole("group", { name: "Key and tempo" });
    expect(group).toHaveTextContent("Key +1");
    expect(group).toHaveTextContent("Not applied");
    expect(within(group).getByRole("status")).toHaveTextContent("Key +1, Tempo 100%: not applied");
  });

  it("keeps the controls mounted while a change restarts the stream", async () => {
    const adjustment = { pitchShiftSemitones: 1, tempo: 1 };
    const { adjust, rerender } = renderPlayer(playerState({ phase: "playing", info: karaokeInfo }));
    const up = await screen.findByRole("button", { name: "Raise the key by a semitone" });
    up.focus();
    fireEvent.click(up);
    expect(adjust).toHaveBeenCalledTimes(1);
    // The restart clears state.info; the song verdict stays for this Media.
    vi.mocked(usePlayer).mockReturnValue({
      ...vi.mocked(usePlayer).mock.results.at(-1)?.value,
      state: playerState({ phase: "loading" }),
      adjustment,
    });
    rerender();
    expect(up).toHaveFocus();
    expect(screen.getByRole("group", { name: "Key and tempo" })).toHaveTextContent("Key +1");
    expect(screen.getByRole("group", { name: "Key and tempo" })).not.toHaveTextContent(
      "Not applied",
    );
  });

  it("keeps the controls, focus and notice when a change is declined to bare audio", async () => {
    const adjustment = { pitchShiftSemitones: 1, tempo: 1 };
    const { adjust, rerender } = renderPlayer(playerState({ phase: "playing", info: karaokeInfo }));
    const up = await screen.findByRole("button", { name: "Raise the key by a semitone" });
    up.focus();
    fireEvent.click(up);
    expect(adjust).toHaveBeenCalledTimes(1);
    // The server declined: plain audio, no composed picture, no adjustment echo.
    vi.mocked(usePlayer).mockReturnValue({
      ...vi.mocked(usePlayer).mock.results.at(-1)?.value,
      state: playerState({
        phase: "playing",
        info: { mediaClass: "audio", reason: "direct-play", streams: [] },
        adjustment,
      }),
      adjustment,
    });
    rerender();
    const group = screen.getByRole("group", { name: "Key and tempo" });
    expect(up).toHaveFocus();
    expect(within(group).getByRole("status")).toHaveTextContent(/not applied/i);
  });

  it("does not carry a karaoke song's controls to the next Media", async () => {
    const karaokeState = playerState({ phase: "playing", info: karaokeInfo });
    let current = karaokeState;
    vi.mocked(usePlayer).mockImplementation(() => ({
      videoRef: { current: null },
      hlsRef: { current: null },
      state: current,
      retry: vi.fn(),
      restart: vi.fn(),
      switchToHls: vi.fn(() => false as const),
      adjustment: DEFAULT_ADJUSTMENT,
      adjust: vi.fn(),
    }));
    let navigate: NavigateFunction | undefined;
    function Navigator() {
      navigate = useNavigate();
      return null;
    }
    const tree = () => (
      <I18nProvider>
        <MemoryRouter initialEntries={["/watch/media-alpha"]}>
          <Navigator />
          <Routes>
            <Route path="/watch/*" element={<PlayerPage />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>
    );
    const view = render(tree());
    await screen.findByRole("group", { name: "Key and tempo" });

    // The new route renders first with the karaoke song's playback state.
    await act(async () => navigate?.("/watch/media-beta"));
    expect(screen.queryByRole("group", { name: "Key and tempo" })).not.toBeInTheDocument();

    // Then an ordinary track loads for it.
    current = playerState({
      phase: "playing",
      mediaID: "media-beta",
      info: { mediaClass: "audio", reason: "direct-play", streams: [] } as unknown as PlaybackInfo,
    });
    view.rerender(tree());
    expect(screen.queryByRole("group", { name: "Key and tempo" })).not.toBeInTheDocument();
  });

  it("clamps to the advertised range and keeps the control at its limit focusable", async () => {
    vi.mocked(fetchPlaybackAdjustmentRanges).mockResolvedValue({
      pitchShiftSemitonesMin: -2,
      pitchShiftSemitonesMax: 2,
      tempoMin: 0.9,
      tempoMax: 1.1,
    });
    const adjustment = { pitchShiftSemitones: 2, tempo: 0.9 };
    const { adjust } = renderPlayer(
      playerState({ phase: "playing", info: { ...karaokeInfo, adjustment }, adjustment }),
      undefined,
      adjustment,
    );
    const up = await screen.findByRole("button", { name: "Raise the key by a semitone" });
    const slower = screen.getByRole("button", { name: "Slow down by 5%" });
    expect(up).toHaveAttribute("aria-disabled", "true");
    expect(up).not.toBeDisabled();
    expect(slower).toHaveAttribute("aria-disabled", "true");
    fireEvent.click(up);
    fireEvent.click(slower);
    expect(adjust).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Speed up by 5%" }));
    expect(adjust).toHaveBeenLastCalledWith({ pitchShiftSemitones: 2, tempo: 0.95 });
    fireEvent.click(screen.getByRole("button", { name: "Reset the key" }));
    expect(adjust).toHaveBeenLastCalledWith({ pitchShiftSemitones: 0, tempo: 0.9 });
  });
});

// feature:persistent-mini-player
describe("PlayerPage in the player host (CORE-1633)", () => {
  beforeEach(() => {
    vi.mocked(usePlayer).mockReset();
    vi.mocked(PlayerTransport).mockClear();
  });

  const queue = [
    { id: "media-alpha", title: "Track One" },
    { id: "media-next", title: "Track Two" },
  ];
  let go: NavigateFunction = () => undefined;
  let pathname = "";
  function Navigator() {
    go = useNavigate();
    pathname = useLocation().pathname;
    return null;
  }

  function renderHosted(
    playback: Partial<PlaybackInfo> | typeof usePlayer,
    extraState: object = {},
    initialPath = "/watch/media-alpha",
    home = <p>home route</p>,
    detail = <p>media route</p>,
  ) {
    vi.mocked(usePlayer).mockImplementation(
      typeof playback === "function"
        ? playback
        : (mediaID) => ({
            videoRef: { current: null },
            hlsRef: { current: null },
            state: playerState({ phase: "playing", mediaID, info: playback }),
            retry: vi.fn(),
            restart: vi.fn(),
            switchToHls: vi.fn(() => false as const),
            adjustment: DEFAULT_ADJUSTMENT,
            adjust: vi.fn(),
          }),
    );
    return render(
      <I18nProvider>
        <MemoryRouter
          initialEntries={[
            { pathname: "/" },
            {
              pathname: initialPath,
              state: {
                skipPrePlay: true,
                title: "Track One",
                queue,
                queueIdentity: "viewer-1",
                ...extraState,
              },
            },
          ]}
          initialIndex={1}
        >
          <Navigator />
          <PlayerHost>
            <Routes>
              <Route path="/" element={home} />
              <Route path="/search" element={<p>search route</p>} />
              <Route
                path="/notifications"
                element={
                  <RegisterProvider>
                    <NotificationsPage />
                  </RegisterProvider>
                }
              />
              <Route path="/media/*" element={<RegisterProvider>{detail}</RegisterProvider>} />
              <Route path="/watch/*" element={<PlayerPage />} />
            </Routes>
          </PlayerHost>
        </MemoryRouter>
      </I18nProvider>,
    );
  }

  const miniPlayer = () => screen.queryByRole("region", { name: "Now playing" });

  it("starts playback from a sampled track row even when the catalog is offline", async () => {
    const sampleHash = "a371bff46385f7ded";
    const item = { id: `media-sample1-${sampleHash}`, kind: "media", title: "Sampled track" };
    vi.mocked(fetchAddressMedia).mockClear().mockRejectedValueOnce(new TypeError("offline"));
    vi.mocked(fetchMedia).mockRejectedValueOnce(new TypeError("offline"));
    renderHosted(
      { mediaClass: "audio" },
      {},
      "/",
      <TrackList rows={[{ item, number: 1 }]} queue={[item]} label="Tracks" />,
    );

    expect(screen.getByRole("link", { name: /Sampled track/ })).toHaveAttribute(
      "href",
      `/media/sample1.${sampleHash}`,
    );
    fireEvent.click(screen.getByRole("button", { name: "Play Sampled track" }));
    expect(pathname).toBe(`/watch/sample1.${sampleHash}`);
    await waitFor(() =>
      expect(vi.mocked(usePlayer).mock.calls.some(([id]) => id === item.id)).toBe(true),
    );
    await waitFor(() => expect(stageVideo()).toBeInTheDocument());
    expect(fetchAddressMedia).not.toHaveBeenCalled();
    vi.mocked(fetchAddressMedia).mockReset();
  });

  it("keeps audio playing on the same media element in a docked mini player across routes", async () => {
    renderHosted({ mediaClass: "audio" });
    const video = await waitFor(() => stageVideo());
    expect(miniPlayer()).toBeNull();

    act(() => {
      void go("/search");
    });
    expect(screen.getByText("search route")).toBeInTheDocument();
    const region = await waitFor(() => {
      const found = miniPlayer();
      if (!found) throw new Error("mini player not docked");
      return found;
    });
    expect(stageVideo()).toBe(video);
    expect(document.querySelector("main")).not.toBeVisible();
    expect(within(region).getByText("Track One")).toBeInTheDocument();
    expect(within(region).getByRole("progressbar", { name: "Playback position" })).toBeVisible();
    for (const name of ["Play", "Next", "Open player", "Stop and close player"]) {
      expect(within(region).getByRole("button", { name })).toBeEnabled();
    }

    fireEvent.click(within(region).getByRole("button", { name: "Open player" }));
    await waitFor(() => expect(miniPlayer()).toBeNull());
    expect(screen.queryByText("search route")).toBeNull();
    expect(stageVideo()).toBe(video);
    expect(document.querySelector("main")).toBeVisible();
  });

  it("expands a sampled track on the same media element with the catalog offline", async () => {
    const sampleHash = "a371bff46385f7ded";
    vi.mocked(fetchAddressMedia).mockClear().mockRejectedValueOnce(new TypeError("offline"));
    vi.mocked(fetchMedia).mockRejectedValueOnce(new TypeError("offline"));
    renderHosted({ mediaClass: "audio" }, {}, `/watch/sample1.${sampleHash}`);
    const video = await waitFor(() => stageVideo());
    act(() => {
      void go("/search");
    });
    const region = await screen.findByRole("region", { name: "Now playing" });
    fireEvent.click(within(region).getByRole("button", { name: "Open player" }));
    await waitFor(() => expect(miniPlayer()).toBeNull());
    expect(pathname).toBe(`/watch/sample1.${sampleHash}`);
    expect(stageVideo()).toBe(video);
    expect(document.querySelector("main")).toBeVisible();
    expect(fetchAddressMedia).not.toHaveBeenCalled();
    vi.mocked(fetchAddressMedia).mockReset();
  });

  it("shows unavailable when a bare sampled address is forbidden", async () => {
    const sampledID = "media-sample1-a371bff46385f7ded";
    vi.mocked(fetchAddressMedia).mockClear().mockRejectedValueOnce(new ApiError("forbidden", 403));
    vi.mocked(fetchPrePlayQueue).mockClear();
    renderHosted({ mediaClass: "audio" }, {}, `/watch/${sampledID}`);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      STRINGS.en.playbackMediaNotAvailable,
    );
    expect(screen.getByRole("button", { name: "Back to Browse" })).toBeInTheDocument();
    expect(vi.mocked(fetchAddressMedia).mock.calls).toEqual([[sampledID, expect.any(AbortSignal)]]);
    expect(fetchPrePlayQueue).not.toHaveBeenCalled();
  });

  // Codex round 2, P2-2: the in-player Version pick belongs to the playback
  // start (its startID), which docking and expanding keep; a new start from
  // the detail page brings its own Version.
  it("keeps the player's Version pick across dock and expand, until a new start", async () => {
    renderHosted(
      { mediaClass: "audio" },
      { tracks: { mediaID: "media-alpha", versionID: "v-a" }, startID: "s1" },
    );
    await waitFor(() => stageVideo());
    const lastCall = () => vi.mocked(usePlayer).mock.calls.at(-1);
    expect(lastCall()?.[4]).toBe("v-a");
    expect(lastCall()?.[6]).toBe("s1");

    act(() => vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0]?.onSelectVersion?.("v-b"));
    await waitFor(() => expect(lastCall()?.[4]).toBe("v-b"));

    act(() => {
      void go("/search");
    });
    const region = await waitFor(() => {
      const found = miniPlayer();
      if (!found) throw new Error("mini player not docked");
      return found;
    });
    fireEvent.click(within(region).getByRole("button", { name: "Open player" }));
    await waitFor(() => expect(miniPlayer()).toBeNull());
    expect(lastCall()?.[4]).toBe("v-b");
    expect(lastCall()?.[6]).toBe("s1");

    act(() => {
      void go("/watch/media-alpha?t=0", {
        state: {
          skipPrePlay: true,
          tracks: { mediaID: "media-alpha", versionID: "v-c", audioIndex: 2 },
          startID: "s2",
        },
      });
    });
    await waitFor(() => expect(lastCall()?.[6]).toBe("s2"));
    expect(lastCall()?.[4]).toBe("v-c");
    expect(lastCall()?.[5]).toEqual({ mediaID: "media-alpha", versionID: "v-c", audioIndex: 2 });
  });

  it("advances to the next track in place while docked", async () => {
    renderHosted({ mediaClass: "audio" });
    await waitFor(() => stageVideo());
    act(() => {
      void go("/search");
    });
    const region = await waitFor(() => {
      const found = miniPlayer();
      if (!found) throw new Error("mini player not docked");
      return found;
    });

    fireEvent.click(within(region).getByRole("button", { name: "Next" }));
    await waitFor(() =>
      expect(vi.mocked(usePlayer).mock.calls.some(([id]) => id === "media-next")).toBe(true),
    );
    expect(screen.getByText("search route")).toBeInTheDocument();
    expect(within(miniPlayer() as HTMLElement).getByText("Track Two")).toBeInTheDocument();
  });

  it("stops and closes on the mini player's Stop", async () => {
    renderHosted({ mediaClass: "audio" });
    await waitFor(() => stageVideo());
    act(() => {
      void go("/search");
    });
    const region = await waitFor(() => {
      const found = miniPlayer();
      if (!found) throw new Error("mini player not docked");
      return found;
    });
    fireEvent.click(within(region).getByRole("button", { name: "Stop and close player" }));
    await waitFor(() => expect(miniPlayer()).toBeNull());
    expect(document.querySelector("video")).toBeNull();
  });

  it("stops and closes on a remote Stop while the audio is docked (CORE-1875)", async () => {
    renderHosted({ mediaClass: "audio" });
    await waitFor(() => stageVideo());
    act(() => {
      void go("/search");
    });
    await waitFor(() => {
      if (!miniPlayer()) throw new Error("mini player not docked");
    });
    act(() => deliverPlayerCommand({ command: "stop", mediaID: "media-alpha" }));
    await waitFor(() => expect(miniPlayer()).toBeNull());
    expect(document.querySelector("video")).toBeNull();
    expect(screen.getByText("search route")).toBeInTheDocument();
  });

  it("stops audio on a remote Stop in the full player instead of docking it (CORE-1875)", async () => {
    renderHosted({ mediaClass: "audio" });
    await waitFor(() => stageVideo());
    const props = vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0];
    act(() => props?.onRemoteStop?.());
    await waitFor(() => expect(screen.getByText("media route")).toBeInTheDocument());
    expect(miniPlayer()).toBeNull();
    expect(document.querySelector("video")).toBeNull();
  });

  it("stops a video Media when its watch route closes, keeping the full-screen route", async () => {
    renderHosted({ mediaClass: "video" });
    await waitFor(() => stageVideo());
    act(() => {
      void go("/search");
    });
    expect(screen.getByText("search route")).toBeInTheDocument();
    expect(miniPlayer()).toBeNull();
    expect(document.querySelector("video")).toBeNull();
  });

  it("drops a queued title switch when the host returns to the party title", async () => {
    const stream = vi.spyOn(api, "subscribeEvents").mockReturnValue(() => {});
    vi.mocked(calibrateWatchPartyClock).mockResolvedValueOnce({ offsetMS: 0, roundTripMS: 1 });
    vi.mocked(createWatchParty).mockResolvedValueOnce({
      partyID: "title-switch-party",
      content: "media-alpha",
      relaxed: true,
      members: [],
    });
    const first = deferred<Awaited<ReturnType<typeof watchPartyTransport>>>();
    vi.mocked(watchPartyTransport)
      .mockReset()
      .mockReturnValueOnce(first.promise)
      .mockImplementation(async ({ item, positionMS }) => ({ item, positionMS, presentAtMS: 0 }));
    const anchor = { item: "media-alpha", positionMS: 10_000, presentAtMS: 0 };
    try {
      expect(await startWatchParty("media-alpha")).toBe(true);
      await act(async () => renderHosted({ mediaClass: "video" }));
      const seek = sendPartyTransport("media-alpha", { positionMS: 10_000 });
      expect(watchPartyTransport).toHaveBeenCalledExactlyOnceWith({
        partyID: "title-switch-party",
        item: "media-alpha",
        positionMS: 10_000,
        paused: true,
      });
      await act(async () => go("/watch/media-next", { state: { skipPrePlay: true } }));
      expect(vi.mocked(usePlayer).mock.lastCall?.[0]).toBe("media-next");
      expect(pendingPartyTransport("media-next")?.itemId).toBe("media-next");
      expect(watchPartyTransport).toHaveBeenCalledTimes(1);
      await act(async () => go("/watch/media-alpha", { state: { skipPrePlay: true } }));
      expect(vi.mocked(usePlayer).mock.lastCall?.[0]).toBe("media-alpha");
      expect(pendingPartyTransport("media-next")).toBeUndefined();

      await act(async () => {
        first.resolve(anchor);
        expect(await seek).toBe(true);
      });
      expect(watchPartyTransport).toHaveBeenCalledTimes(1);
      expect(getWatchParty().timeline).toMatchObject(anchor);
    } finally {
      first.resolve(anchor);
      act(resetWatchPartyForTest);
      stream.mockRestore();
      vi.mocked(watchPartyTransport).mockReset();
    }
  });

  it.each(["response", "stream"])(
    "keeps the host's return selection after a submitted switch is acknowledged by %s",
    async (ack) => {
      const stream = vi.spyOn(api, "subscribeEvents").mockReturnValue(() => {});
      vi.mocked(calibrateWatchPartyClock).mockResolvedValueOnce({ offsetMS: 0, roundTripMS: 1 });
      const partyID = "submitted-switch-party";
      vi.mocked(createWatchParty).mockResolvedValueOnce({
        partyID,
        content: "media-alpha",
        relaxed: true,
        members: [],
      });
      const first = deferred<Awaited<ReturnType<typeof watchPartyTransport>>>();
      vi.mocked(watchPartyTransport)
        .mockReset()
        .mockReturnValueOnce(first.promise)
        .mockImplementation(async ({ item, positionMS }) => ({ item, positionMS, presentAtMS: 2 }));
      const anchor = { item: "media-next", positionMS: 0, presentAtMS: 1 };
      try {
        expect(await startWatchParty("media-alpha")).toBe(true);
        await act(async () => renderHosted({ mediaClass: "video" }));
        const next = vi.mocked(PlayerTransport).mock.lastCall?.[0].onNextInQueue;
        expect(next).toBeTypeOf("function");
        await act(async () => next?.());
        expect(pathname).toBe("/watch/media-next");
        expect(watchPartyTransport).toHaveBeenCalledExactlyOnceWith({
          partyID,
          item: "media-next",
          positionMS: 0,
          paused: false,
        });
        await act(async () => go("/watch/media-alpha", { state: { skipPrePlay: true } }));
        expect(pathname).toBe("/watch/media-alpha");
        expect(getWatchParty().timeline?.item).toBe("media-alpha");
        expect(watchPartyTransport).toHaveBeenCalledTimes(1);

        if (ack === "stream") {
          await act(async () => {
            stream.mock.lastCall?.[0](
              new MessageEvent("group.sync", {
                data: JSON.stringify({
                  payload: { ...anchor, groupID: partyID, signal: "transition", paused: false },
                }),
              }),
            );
          });
          expect(getWatchParty().timeline?.item).toBe("media-next");
          expect(pathname).toBe("/watch/media-alpha");
        }
        await act(async () => first.resolve(anchor));
        expect(watchPartyTransport).toHaveBeenCalledTimes(2);
        expect(watchPartyTransport).toHaveBeenLastCalledWith({
          partyID,
          item: "media-alpha",
          positionMS: 0,
          paused: false,
        });
        expect(pathname).toBe("/watch/media-alpha");
        expect(getWatchParty().timeline?.item).toBe("media-alpha");
      } finally {
        first.resolve(anchor);
        act(resetWatchPartyForTest);
        stream.mockRestore();
        vi.mocked(watchPartyTransport).mockReset();
      }
    },
  );

  it.each(["inaccessible", "superseded"])(
    "returns the host to the party title after an %s switch fails",
    async (kind) => {
      const stream = vi.spyOn(api, "subscribeEvents").mockReturnValue(() => {});
      vi.mocked(calibrateWatchPartyClock).mockResolvedValueOnce({ offsetMS: 0, roundTripMS: 1 });
      const partyID = "failed-switch-party";
      vi.mocked(createWatchParty).mockResolvedValueOnce({
        partyID,
        content: "media-alpha",
        relaxed: true,
        members: [],
      });
      const request = deferred<Awaited<ReturnType<typeof watchPartyTransport>>>();
      vi.mocked(watchPartyTransport).mockReset().mockReturnValueOnce(request.promise);
      const sharedTitle = kind === "superseded" ? "media-third" : "media-alpha";
      try {
        expect(await startWatchParty("media-alpha")).toBe(true);
        await act(async () => renderHosted({ mediaClass: "video" }));
        await act(async () => go("/watch/media-next", { state: { skipPrePlay: true } }));
        expect(pathname).toBe("/watch/media-next");
        expect(watchPartyTransport).toHaveBeenCalledExactlyOnceWith({
          partyID,
          item: "media-next",
          positionMS: 0,
          paused: false,
        });

        await act(async () => {
          if (kind === "superseded") {
            stream.mock.lastCall?.[0](
              new MessageEvent("group.sync", {
                data: JSON.stringify({
                  payload: {
                    groupID: partyID,
                    signal: "transition",
                    item: sharedTitle,
                    positionMS: 50_000,
                    presentAtMS: 0,
                    paused: true,
                  },
                }),
              }),
            );
            request.reject(new TypeError("Network unavailable"));
          } else {
            request.reject(
              new ApiError("cannot play this in the party: it is not visible to every member", 200),
            );
          }
        });
        expect(pathname).toBe(`/watch/${sharedTitle}`);
        expect(getWatchParty().timeline).toMatchObject({ item: sharedTitle, paused: true });
        expect(watchPartyTransport).toHaveBeenCalledTimes(1);
      } finally {
        act(resetWatchPartyForTest);
        stream.mockRestore();
        vi.mocked(watchPartyTransport).mockReset();
      }
    },
  );

  it("starts at the clicked playhead after opening Notifications during creation (CORE-1858)", async () => {
    const stream = vi.spyOn(api, "subscribeEvents").mockReturnValue(() => {});
    const creation = deferred<Awaited<ReturnType<typeof createWatchParty>>>();
    vi.mocked(calibrateWatchPartyClock).mockResolvedValue({ offsetMS: 0, roundTripMS: 1 });
    vi.mocked(createWatchParty).mockClear().mockReturnValueOnce(creation.promise);
    vi.mocked(fetchNotifications).mockResolvedValue([]);
    vi.mocked(watchPartyTransport)
      .mockClear()
      .mockImplementationOnce(async ({ item, positionMS }) => ({
        item,
        positionMS,
        presentAtMS: 0,
      }));
    try {
      await act(async () => renderHosted({ mediaClass: "video" }));
      const video = await waitFor(() => stageVideo());
      video.currentTime = 65;
      Object.defineProperty(video, "paused", { configurable: true, value: false });
      fireEvent.click(screen.getByRole("button", { name: "Watch Party" }));
      await act(async () =>
        fireEvent.click(screen.getByRole("button", { name: "Start a Watch Party" })),
      );
      expect(createWatchParty).toHaveBeenCalledTimes(1);
      expect(getWatchParty().status).toBe("starting");
      expect(watchPartyTransport).not.toHaveBeenCalled();

      await act(async () => go("/notifications"));
      expect(pathname).toBe("/notifications");
      expect(document.querySelector("video")).toBeNull();
      video.currentTime = 0;
      Object.defineProperty(video, "paused", { value: true });
      await act(async () => {
        creation.resolve({ partyID: "p1", content: "media-alpha", relaxed: true, members: [] });
      });

      expect(watchPartyTransport).toHaveBeenCalledExactlyOnceWith({
        partyID: "p1",
        item: "media-alpha",
        positionMS: 65_000,
        paused: false,
      });
      expect(getWatchParty()).toMatchObject({
        status: "active",
        timeline: { positionMS: 65_000, paused: false },
      });
    } finally {
      act(resetWatchPartyForTest);
      stream.mockRestore();
      vi.mocked(createWatchParty).mockReset();
      vi.mocked(calibrateWatchPartyClock).mockRejectedValue(new Error("not on the home network"));
      vi.mocked(watchPartyTransport).mockReset();
    }
  });

  it("keeps hosting while the viewer opens Notifications and approves a seek (CORE-1871)", async () => {
    const unsubscribe = vi.fn();
    const stream = vi.spyOn(api, "subscribeEvents").mockReturnValue(unsubscribe);
    try {
      vi.mocked(calibrateWatchPartyClock).mockResolvedValueOnce({ offsetMS: 0, roundTripMS: 1 });
      vi.mocked(createWatchParty).mockResolvedValue({
        partyID: "p1",
        content: "media-alpha",
        relaxed: false,
        members: [],
      });
      vi.mocked(fetchNotifications).mockResolvedValue([
        {
          id: "n1",
          kind: "watch-party-request",
          title: "Viewer requested a seek",
          body: "Guest",
          ref: "p1:req-1",
          createdAt: "2026-11-20T10:00:00Z",
          read: true,
        },
      ]);
      vi.mocked(decideWatchPartyRequest).mockResolvedValue({
        item: "media-alpha",
        positionMS: 45_000,
        presentAtMS: 0,
      });
      expect(await startWatchParty("media-alpha", 0, false)).toBe(true);
      renderHosted({ mediaClass: "video" });
      await waitFor(() => stageVideo());

      await act(async () => go("/notifications"));
      await screen.findByText("Viewer requested a seek");
      expect(document.querySelector("video")).toBeNull();
      await act(async () => new Promise((resolve) => setTimeout(resolve, 0)));
      expect(leaveWatchParty).not.toHaveBeenCalled();
      expect(unsubscribe).not.toHaveBeenCalled();
      expect(getWatchParty()).toMatchObject({ partyID: "p1", status: "active", isHost: true });

      await act(async () => fireEvent.click(screen.getByRole("button", { name: "Approve" })));
      expect(decideWatchPartyRequest).toHaveBeenCalledExactlyOnceWith("p1", "req-1", true);
      await act(async () => window.dispatchEvent(new Event("pagehide")));
      expect(leaveWatchPartyKeepalive).toHaveBeenCalledExactlyOnceWith("p1");
      expect(unsubscribe).toHaveBeenCalledTimes(1);
      expect(getWatchParty().status).toBe("idle");
    } finally {
      resetWatchPartyForTest();
      stream.mockRestore();
    }
  });

  it("follows the current party title and paused anchor on Back from Notifications", async () => {
    const stream = vi.spyOn(api, "subscribeEvents").mockReturnValue(() => {});
    vi.mocked(calibrateWatchPartyClock).mockResolvedValueOnce({ offsetMS: 0, roundTripMS: 1 });
    vi.mocked(createWatchParty).mockResolvedValueOnce({
      partyID: "p1",
      content: "media-alpha",
      relaxed: true,
      members: [],
    });
    vi.mocked(fetchNotifications).mockResolvedValue([]);
    vi.mocked(watchPartyTransport)
      .mockReset()
      .mockImplementation(async ({ item, positionMS }) => ({ item, positionMS, presentAtMS: 1 }));
    const anchor = { item: "media-next", positionMS: 45_000, presentAtMS: 0, paused: true };
    try {
      expect(await startWatchParty("media-alpha")).toBe(true);
      renderHosted({ mediaClass: "video" });
      await waitFor(() => stageVideo());
      // A previously selected title is now a history entry.
      vi.mocked(fetchPrePlayQueue).mockResolvedValueOnce([]);
      await act(async () => go("/watch/media-alpha"));
      await waitFor(() => stageVideo());
      await act(async () => go("/notifications"));
      expect(document.querySelector("video")).toBeNull();
      vi.mocked(fetchPrePlayQueue).mockClear();
      await act(async () => {
        stream.mock.lastCall?.[0](
          new MessageEvent("group.sync", {
            data: JSON.stringify({
              payload: { ...anchor, groupID: "p1", signal: "transition" },
            }),
          }),
        );
      });
      expect(getWatchParty().timeline).toMatchObject(anchor);

      await act(async () => go(-1));
      expect(watchPartyTransport).not.toHaveBeenCalled();
      expect(fetchPrePlayQueue).not.toHaveBeenCalled();
      await waitFor(() => expect(pathname).toBe("/watch/media-next"));
      const video = stageVideo();
      Object.defineProperty(video, "readyState", { value: 4 });
      await waitFor(() => expect(video.currentTime).toBe(45));
      expect(video.paused).toBe(true);
      expect(getWatchParty().timeline).toMatchObject(anchor);
    } finally {
      act(resetWatchPartyForTest);
      stream.mockRestore();
      vi.mocked(fetchNotifications).mockReset();
      vi.mocked(watchPartyTransport).mockReset();
    }
  });

  it.each([
    {
      name: "restores a paused lead host on Back from Notifications without sending Play",
      explicitPause: true,
    },
    {
      name: "resumes a lead host from a silence hold on Back from Notifications",
      explicitPause: false,
    },
  ])("$name", async ({ explicitPause }) => {
    const { usePlayer: realUsePlayer } =
      await vi.importActual<typeof import("../player/usePlayer")>("../player/usePlayer");
    const stream = vi.spyOn(api, "subscribeEvents").mockReturnValue(() => {});
    const play = vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(function (
      this: HTMLMediaElement,
    ) {
      Object.defineProperty(this, "paused", { configurable: true, value: false });
      this.dispatchEvent(new Event("play"));
      return Promise.resolve();
    });
    const pause = vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(function (
      this: HTMLMediaElement,
    ) {
      Object.defineProperty(this, "paused", { configurable: true, value: true });
      this.dispatchEvent(new Event("pause"));
    });
    const load = vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
    vi.mocked(calibrateWatchPartyClock).mockResolvedValueOnce({ offsetMS: 0, roundTripMS: 1 });
    vi.mocked(createWatchParty).mockResolvedValueOnce({
      partyID: "p1",
      content: "media-alpha",
      relaxed: false,
      members: [],
    });
    vi.mocked(fetchNotifications).mockResolvedValue([]);
    vi.mocked(fetchPlaybackInfo).mockResolvedValue({
      mediaClass: "video",
      streams: [{ kind: "direct-play", url: "/stream/media-alpha" }],
    });
    vi.mocked(watchPartyTransport)
      .mockReset()
      .mockImplementation(async ({ item, positionMS }) => ({
        item,
        positionMS,
        presentAtMS: Date.now(),
      }));
    let unmount: (() => void) | undefined;
    try {
      expect(await startWatchParty("media-alpha", 45_000, false, true)).toBe(true);
      vi.mocked(watchPartyTransport).mockClear();
      ({ unmount } = renderHosted(realUsePlayer));
      const first = await waitFor(() => {
        const video = stageVideo();
        expect(video).toHaveAttribute("src", "/stream/media-alpha");
        return video;
      });
      fireEvent.loadedMetadata(first);
      await act(async () => first.play());
      expect(getWatchParty().timeline).toMatchObject({ paused: false });
      if (explicitPause) {
        await act(async () => first.pause());
        expect(getWatchParty().timeline).toMatchObject({ paused: true });
      }
      play.mockClear();
      vi.mocked(watchPartyTransport).mockClear();
      await act(async () => go("/notifications"));
      expect(document.querySelector("video")).toBeNull();
      const positionMS = explicitPause ? 45_000 : 47_000;
      if (!explicitPause) {
        await act(async () => {
          stream.mock.lastCall?.[0](
            new MessageEvent("group.sync", {
              data: JSON.stringify({
                payload: {
                  signal: "transition",
                  groupID: "p1",
                  item: "media-alpha",
                  positionMS,
                  presentAtMS: Date.now() + 3000,
                  paused: true,
                  lead: true,
                  ratePermille: 1000,
                },
              }),
            }),
          );
        });
        expect(getWatchParty().timeline).toMatchObject({ paused: true, positionMS });
      }
      await act(async () => go(-1));
      const video = await waitFor(() => {
        const reopened = stageVideo();
        expect(reopened).toHaveAttribute("src", "/stream/media-alpha");
        return reopened;
      });
      expect(video).not.toBe(first);
      Object.defineProperty(video, "readyState", { value: 4 });
      fireEvent.loadedMetadata(video);
      fireEvent.seeked(video);
      await act(async () => fireEvent.canPlay(video));
      expect(video.paused).toBe(explicitPause);
      expect(video.currentTime).toBe(positionMS / 1000);
      if (explicitPause) {
        expect(play).not.toHaveBeenCalled();
        expect(watchPartyTransport).not.toHaveBeenCalled();
        await act(async () => video.play());
      } else {
        expect(play).toHaveBeenCalledTimes(1);
      }
      expect(watchPartyTransport).toHaveBeenCalledExactlyOnceWith({
        partyID: "p1",
        item: "media-alpha",
        positionMS,
        paused: false,
      });
    } finally {
      unmount?.();
      act(resetWatchPartyForTest);
      stream.mockRestore();
      load.mockRestore();
      pause.mockRestore();
      play.mockRestore();
      vi.mocked(fetchNotifications).mockReset();
      vi.mocked(fetchPlaybackInfo).mockReset();
      vi.mocked(watchPartyTransport).mockReset();
    }
  });

  it("starts over from the detail page while the paused lead player is docked", async () => {
    const { usePlayer: realUsePlayer } =
      await vi.importActual<typeof import("../player/usePlayer")>("../player/usePlayer");
    const stream = vi.spyOn(api, "subscribeEvents").mockReturnValue(() => {});
    const load = vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(function (
      this: HTMLMediaElement,
    ) {
      this.currentTime = 0;
    });
    const play = vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(function (
      this: HTMLMediaElement,
    ) {
      Object.defineProperty(this, "paused", { configurable: true, value: false });
      this.dispatchEvent(new Event("play"));
      return Promise.resolve();
    });
    const media = { id: "media-alpha", title: "Track One", mediaClass: "audio", favorited: false };
    vi.mocked(fetchAddressMedia).mockResolvedValue(media);
    vi.mocked(fetchNotifications).mockResolvedValue([]);
    vi.mocked(fetchMediaDetailFields).mockResolvedValue([]);
    vi.mocked(fetchMediaRelations).mockResolvedValue([]);
    vi.mocked(fetchMediaTracks).mockResolvedValue([]);
    vi.mocked(fetchPlaybackInfo)
      .mockReset()
      .mockResolvedValue({
        mediaClass: "audio",
        streams: [{ kind: "direct-play", url: "/stream/media-alpha" }],
      });
    vi.mocked(calibrateWatchPartyClock).mockResolvedValueOnce({ offsetMS: 0, roundTripMS: 1 });
    vi.mocked(createWatchParty).mockResolvedValueOnce({
      partyID: "p1",
      content: media.id,
      relaxed: false,
      members: [],
    });
    vi.mocked(watchPartyTransport)
      .mockReset()
      .mockImplementation(async ({ item, positionMS }) => ({ item, positionMS, presentAtMS: 1 }));
    let unmount: (() => void) | undefined;
    try {
      expect(await startWatchParty(media.id, 45_000, false, true)).toBe(true);
      ({ unmount } = renderHosted(realUsePlayer, {}, undefined, undefined, <MediaDetailPage />));
      const video = await waitFor(() => {
        const element = stageVideo();
        expect(element).toHaveAttribute("src", "/stream/media-alpha");
        return element;
      });
      fireEvent.loadedMetadata(video);
      expect(video.currentTime).toBe(45);
      expect(video.paused).toBe(true);
      expect(getWatchParty().timeline).toMatchObject({ positionMS: 45_000, paused: true });
      const card = {
        ...media,
        kind: "media",
        engagement: { positionSeconds: 45, durationSeconds: 120 },
      };
      await act(async () => go("/media/media-alpha", { state: mediaLinkState(card, "viewer-1") }));
      expect(miniPlayer()).toBeVisible();
      expect(stageVideo()).toBe(video);
      vi.mocked(watchPartyTransport).mockClear();

      fireEvent.click(
        await screen.findByRole("button", { name: "Start Track One from the beginning" }),
      );
      await waitFor(() => {
        expect(miniPlayer()).toBeNull();
        expect(fetchPlaybackInfo).toHaveBeenCalledTimes(2);
        expect(video).toHaveAttribute("src", "/stream/media-alpha");
      });
      expect(stageVideo()).toBe(video);
      fireEvent.loadedMetadata(video);
      await act(async () => fireEvent.canPlay(video));
      expect(video.currentTime).toBe(0);
      expect(video.paused).toBe(false);
      expect(watchPartyTransport).toHaveBeenCalledWith({
        partyID: "p1",
        item: media.id,
        positionMS: 0,
        paused: false,
      });
    } finally {
      unmount?.();
      act(resetWatchPartyForTest);
      clearViewerCache();
      stream.mockRestore();
      load.mockRestore();
      play.mockRestore();
      vi.mocked(fetchAddressMedia).mockReset();
      vi.mocked(fetchMediaDetailFields).mockReset();
      vi.mocked(fetchMediaRelations).mockReset();
      vi.mocked(fetchMediaTracks).mockReset();
      vi.mocked(fetchNotifications).mockReset();
      vi.mocked(fetchPlaybackInfo).mockReset();
      vi.mocked(watchPartyTransport).mockReset();
    }
  });

  it("requires a confirmed departure before accepting another party's invite in Notifications", async () => {
    const stream = vi.spyOn(api, "subscribeEvents").mockReturnValue(() => {});
    const departure = deferred<void>();
    const retry = deferred<void>();
    vi.mocked(leaveWatchParty)
      .mockClear()
      .mockReturnValueOnce(departure.promise)
      .mockReturnValueOnce(retry.promise);
    vi.mocked(calibrateWatchPartyClock).mockResolvedValue({ offsetMS: 0, roundTripMS: 1 });
    vi.mocked(createWatchParty).mockResolvedValueOnce({
      partyID: "p1",
      content: "media-alpha",
      relaxed: false,
      members: [],
    });
    vi.mocked(fetchNotifications).mockResolvedValue([
      {
        id: "invite-2",
        kind: "watch-party-invite",
        title: "Join the other party",
        body: "Guest",
        ref: "p2",
        createdAt: "2026-11-20T10:00:00Z",
        read: true,
      },
    ]);
    vi.mocked(acceptWatchParty)
      .mockClear()
      .mockImplementationOnce(async () => {
        const anchor = { partyID: "p2", item: "media-next", positionMS: 45_000, presentAtMS: 0 };
        stream.mock.lastCall?.[0](
          new MessageEvent("group.sync", {
            data: JSON.stringify({
              payload: { ...anchor, groupID: "p2", signal: "roster", paused: true },
            }),
          }),
        );
        return anchor;
      });
    try {
      expect(await startWatchParty("media-alpha")).toBe(true);
      renderHosted({ mediaClass: "video" });
      await waitFor(() => stageVideo());

      await act(async () => go("/notifications"));
      const switchParty = await screen.findByRole("button", { name: "Leave and join" });
      expect(getWatchParty().partyID).toBe("p1");
      expect(leaveWatchParty).not.toHaveBeenCalled();

      fireEvent.click(switchParty);
      expect(leaveWatchParty).toHaveBeenCalledExactlyOnceWith("p1");
      expect(acceptWatchParty).not.toHaveBeenCalled();
      expect(pathname).toBe("/notifications");
      expect(getWatchParty()).toMatchObject({ partyID: "p1", status: "active", isHost: true });

      await act(async () => departure.reject(new TypeError("Network unavailable")));
      expect(screen.getByRole("alert")).toHaveTextContent("Couldn't leave the Watch Party.");
      expect(acceptWatchParty).not.toHaveBeenCalled();
      expect(pathname).toBe("/notifications");
      expect(getWatchParty()).toMatchObject({ partyID: "p1", status: "active", isHost: true });

      fireEvent.click(screen.getByRole("button", { name: "Leave and join" }));
      expect(leaveWatchParty).toHaveBeenCalledTimes(2);
      expect(leaveWatchParty).toHaveBeenLastCalledWith("p1");
      expect(acceptWatchParty).not.toHaveBeenCalled();
      await act(async () => retry.resolve(undefined));
      expect(acceptWatchParty).toHaveBeenCalledExactlyOnceWith({
        partyID: "p2",
        clockOffsetMS: 0,
        renderLatencyMS: 0,
      });
      await waitFor(() => expect(pathname).toBe("/watch/media-next"));
      expect(getWatchParty()).toMatchObject({
        status: "active",
        partyID: "p2",
        timeline: { item: "media-next", positionMS: 45_000 },
      });
    } finally {
      act(resetWatchPartyForTest);
      stream.mockRestore();
      vi.mocked(acceptWatchParty).mockReset();
      vi.mocked(calibrateWatchPartyClock).mockRejectedValue(new Error("not on the home network"));
      vi.mocked(fetchNotifications).mockReset();
      vi.mocked(leaveWatchParty).mockClear();
    }
  });
});

// feature:watch-together-entry (CORE-1764)
describe("PlayerPage Watch together arrival", () => {
  it("opens the Watch Party panel when the detail page asks for the invite", () => {
    renderPlayer(playerState({ phase: "playing" }), { watchPartyIntent: "invite" });
    expect(screen.getByRole("region", { name: "Watch Party" })).toBeInTheDocument();
  });

  it("keeps the panel closed on an ordinary arrival", () => {
    renderPlayer(playerState({ phase: "playing" }));
    expect(screen.queryByRole("region", { name: "Watch Party" })).toBeNull();
  });
});

describe("PlayerPage Watch Party startup (CORE-1858)", () => {
  afterEach(() => {
    act(resetWatchPartyForTest);
    vi.restoreAllMocks();
    vi.mocked(watchPartyTransport).mockReset();
    vi.mocked(calibrateWatchPartyClock).mockRejectedValue(new Error("not on the home network"));
  });

  it("can leave the same party after startup transport and cleanup both fail", async () => {
    vi.spyOn(api, "subscribeEvents").mockReturnValue(() => {});
    vi.mocked(calibrateWatchPartyClock).mockResolvedValue({ offsetMS: 0, roundTripMS: 1 });
    vi.mocked(createWatchParty).mockResolvedValueOnce({
      partyID: "p1",
      content: "media-alpha",
      relaxed: true,
      members: [],
    });
    vi.mocked(watchPartyTransport).mockRejectedValueOnce(new Error("Transport unavailable"));
    vi.mocked(leaveWatchParty)
      .mockRejectedValueOnce(new Error("Network unavailable"))
      .mockResolvedValueOnce(undefined);
    await act(async () => {
      renderPlayer(playerState({ phase: "playing" }));
    });
    stageVideo().currentTime = 42;
    fireEvent.click(screen.getByRole("button", { name: "Watch Party" }));
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Start a Watch Party" }));
    });
    expect(watchPartyTransport).toHaveBeenCalledTimes(1);
    expect(leaveWatchParty).toHaveBeenCalledExactlyOnceWith("p1");
    expect(getWatchParty().partyID).toBe("p1");
    expect(screen.queryByRole("button", { name: "Invite" })).toBeNull();

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Leave party" }));
    });
    expect(leaveWatchParty).toHaveBeenNthCalledWith(2, "p1");
    expect(getWatchParty()).toMatchObject({ status: "idle", partyID: "" });
  });

  it("publishes the live playhead and pause state after delayed creation", async () => {
    vi.spyOn(api, "subscribeEvents").mockReturnValue(() => {});
    vi.mocked(calibrateWatchPartyClock).mockResolvedValue({ offsetMS: 0, roundTripMS: 1 });
    const creation = deferred<Awaited<ReturnType<typeof createWatchParty>>>();
    vi.mocked(createWatchParty).mockReturnValueOnce(creation.promise);
    vi.mocked(watchPartyTransport).mockImplementationOnce(async ({ item, positionMS }) => ({
      item,
      positionMS,
      presentAtMS: 0,
    }));
    await act(async () => {
      renderPlayer(playerState({ phase: "playing" }));
    });
    const video = stageVideo();
    Object.defineProperty(video, "paused", { configurable: true, value: false });
    video.currentTime = 42;
    fireEvent.click(screen.getByRole("button", { name: "Watch Party" }));
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Start a Watch Party" }));
    });
    expect(getWatchParty().status).toBe("starting");
    expect(watchPartyTransport).not.toHaveBeenCalled();

    await act(async () => {
      video.currentTime = 65;
      fireEvent.seeked(video);
      Object.defineProperty(video, "paused", { value: true });
      fireEvent.pause(video);
      creation.resolve({ partyID: "p1", content: "media-alpha", relaxed: true, members: [] });
    });
    expect(watchPartyTransport).toHaveBeenCalledExactlyOnceWith({
      partyID: "p1",
      item: "media-alpha",
      positionMS: 65_000,
      paused: true,
    });
    expect(getWatchParty().timeline).toMatchObject({ positionMS: 65_000, paused: true });
  });

  it("publishes a pause during initial transport before enabling sync", async () => {
    vi.spyOn(api, "subscribeEvents").mockReturnValue(() => {});
    vi.mocked(calibrateWatchPartyClock).mockResolvedValue({ offsetMS: 0, roundTripMS: 1 });
    vi.mocked(createWatchParty).mockResolvedValueOnce({
      partyID: "p1",
      content: "media-alpha",
      relaxed: true,
      members: [],
    });
    const initial = deferred<Awaited<ReturnType<typeof watchPartyTransport>>>();
    const pause = deferred<Awaited<ReturnType<typeof watchPartyTransport>>>();
    vi.mocked(watchPartyTransport)
      .mockReturnValueOnce(initial.promise)
      .mockReturnValueOnce(pause.promise);
    await act(async () => {
      renderPlayer(playerState({ phase: "playing" }));
    });
    const video = stageVideo();
    const play = vi.spyOn(video, "play").mockResolvedValue();
    Object.defineProperty(video, "readyState", { value: 4 });
    Object.defineProperty(video, "paused", { configurable: true, value: false });
    video.currentTime = 42;
    fireEvent.click(screen.getByRole("button", { name: "Watch Party" }));
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Start a Watch Party" }));
    });
    expect(watchPartyTransport).toHaveBeenCalledExactlyOnceWith({
      partyID: "p1",
      item: "media-alpha",
      positionMS: 42_000,
      paused: false,
    });

    video.currentTime = 45;
    Object.defineProperty(video, "paused", { value: true });
    fireEvent.pause(video);
    await act(async () => {
      initial.resolve({ item: "media-alpha", positionMS: 42_000, presentAtMS: 0 });
    });
    expect(watchPartyTransport).toHaveBeenNthCalledWith(2, {
      partyID: "p1",
      item: "media-alpha",
      positionMS: 45_000,
      paused: true,
    });
    expect(getWatchParty().status).toBe("starting");
    expect(play).not.toHaveBeenCalled();

    await act(async () => {
      pause.resolve({ item: "media-alpha", positionMS: 45_000, presentAtMS: 0 });
    });
    expect(getWatchParty()).toMatchObject({
      status: "active",
      timeline: { positionMS: 45_000, paused: true },
    });
    expect(play).not.toHaveBeenCalled();
  });
});

// feature:watch-party
describe("PlayerPage in a Watch Party (CORE-1766)", () => {
  beforeEach(() => {
    vi.mocked(usePlayer).mockReset();
    vi.mocked(PlayerTransport).mockClear();
  });
  afterEach(() => vi.mocked(watchPartyDefersUpNext).mockReturnValue(false));

  const queue = [
    { id: "media-prev", title: "Before" },
    { id: "media-alpha", title: "Alpha" },
    { id: "media-next", title: "After" },
  ];

  it("hands a member's next item and autoplay to the party", () => {
    vi.mocked(watchPartyDefersUpNext).mockReturnValue(true);
    renderPlayer(
      playerState({ phase: "playing", info: { mediaClass: "audio" } as PlayerState["info"] }),
      { title: "Alpha", queue, queueIdentity: "viewer-1" },
    );
    const props = vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0];
    expect(props?.onNextInQueue).toBeUndefined();
    // Only the forward step defers.
    expect(props?.onPreviousInQueue).toBeTypeOf("function");
  });

  it("keeps the host's next item, whose advance the party follows", () => {
    renderPlayer(
      playerState({ phase: "playing", info: { mediaClass: "audio" } as PlayerState["info"] }),
      { title: "Alpha", queue, queueIdentity: "viewer-1" },
    );
    expect(vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0]?.onNextInQueue).toBeTypeOf("function");
  });
});
