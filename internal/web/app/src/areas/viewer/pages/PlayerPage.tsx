import { AlertCircle, LoaderCircle } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useLocation, useNavigate, useNavigationType } from "react-router";
import { MAIN_CONTENT_ID, SkipLink } from "@/components/chrome/SkipLink";
import { Button } from "@/components/ui/button";
import { getIdentityEpochSignal } from "@/lib/api";
import { displayString } from "@/lib/display";
import { useDocumentTitle } from "@/lib/documentTitle";
import {
  fetchClientPrefs,
  fetchMedia,
  fetchPlaybackAdjustmentRanges,
  fetchPrePlayQueue,
  setClientPref,
} from "@/lib/graphql";
import type {
  ClientPref,
  GenericMediaInfo,
  MediaCredit,
  PlaybackAdjustmentRanges,
  PrePlayItem,
} from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useSession, useViewerIdentity } from "@/lib/session";
import { mediaPath } from "../lib/mediaAddress";
import {
  MUSIC_QUEUE_PARAM,
  musicQueueNeighbours,
  playableMusic,
  useMusicQueue,
} from "../lib/musicQueue";
import {
  adjustmentApplied,
  hasPicture,
  isComposedPictureServed,
  type PlaybackNext,
  sameAdjustment,
  songSecondsPerStreamSecond,
} from "../lib/playback";
import { previousInPlayQueue } from "../lib/playQueue";
import {
  resolvePreferences,
  type SkipInterval,
  skipIntervalOverrideKey,
  skipIntervalOverrideValue,
} from "../lib/preferences";
import { sequencePosition } from "../lib/relationOrder";
import { noteAutoAdvance } from "../lib/stillWatching";
import {
  encodeSubtitleAppearance,
  resolveSubtitleAppearance,
  SUBTITLE_APPEARANCE_KEYS,
  type SubtitleAppearanceField,
} from "../lib/subtitleAppearance";
import { useCanonicalMediaAddress, useMediaAddress } from "../lib/useCanonicalMediaAddress";
import { readVersionChoice, writeVersionChoice } from "../lib/versionChoice";
import {
  type PlayQueueItem,
  queueForPlaying,
  queueNeighbours,
  queueOfClass,
  readPlayerNavState,
  withQueueClasses,
} from "../lib/watchQueue";
import { readWatchPartyIntent } from "../lib/watchTogether";
import { AudioStage } from "../player/AudioStage";
import { KaraokeControls } from "../player/KaraokeControls";
import { MiniPlayer } from "../player/MiniPlayer";
import { NextInSequenceCard } from "../player/NextInSequenceCard";
import { type HostedPlayerProps, usePlayerHost, type WatchEntry } from "../player/PlayerHost";
import { PlayerTransport } from "../player/PlayerTransport";
import { PrePlayStage } from "../player/PrePlayStage";
import { SkippedOverlay } from "../player/SkippedOverlay";
import { StillWatchingPrompt } from "../player/StillWatchingPrompt";
import { UpNextCard } from "../player/UpNextCard";
import { type PartySeekAutoSkip, useAutoSkip } from "../player/useAutoSkip";
import { useCaptionLoad } from "../player/useCaptionLoad";
import { useChromeAutoHide } from "../player/useChromeAutoHide";
import { decodableStream, handBack, useGaplessAudio } from "../player/useGaplessAudio";
import { useMediaLoading } from "../player/useMediaLoading";
import { useMediaSession } from "../player/useMediaSession";
import { usePlayer } from "../player/usePlayer";
import { useSequenceFinish } from "../player/useSequenceFinish";
import { useStillWatching } from "../player/useStillWatching";
import { useUpNext } from "../player/useUpNext";
import { useWatchParty } from "../player/useWatchParty";
import { WatchPartyPanel, WatchPartySyncIndicator } from "../player/WatchPartyPanel";
import {
  getPartyTransportEpoch,
  getWatchParty,
  watchPartyDefersUpNext,
} from "../player/watchPartyStore";

function hostTitleSelectionEpoch(): number | undefined {
  const { status, isHost } = getWatchParty();
  return status === "active" && isHost ? getPartyTransportEpoch() : undefined;
}

// The player (/watch/*), ported from the vanilla play() + player view
// (app.js / index.html). Negotiates playback-info, routes to direct play or
// hls.js, mounts the custom transport, reports progress, and surfaces the
// reveal-terminal / unavailable states with a Back-to-browse affordance. A
// relock while watching is a reveal-terminal state (ADR-0004): the engine
// blocks the stream and this page offers an honest Back control.
//
// The stage owns the idle chrome auto-hide (the transport fades while playback
// runs, reveals on any pointer/key/focus, never while paused or while a menu is
// open) and the Up Next countdown card (auto-advance to the server-issued next
// Media). Both are deferred-pass features layered over the core engine.
//
// Before the title starts, the page asks the server for its pre-play queue
// (CORE-1701) and plays those items first. A timestamped link (?t=) and an
// advance from the queue or Up Next go straight to the title.
//
// Under the viewer's player host (CORE-1633) the route hands the title to the
// host, which keeps the engine above the routes so an audio Media plays on in
// the docked mini player once the listener leaves; without a host (a bare
// route) the page mounts the player itself.
export function PlayerPage() {
  const t = useT();
  const location = useLocation();
  const navigate = useNavigate();
  const navigationType = useNavigationType();
  // A readable address (CORE-2329) or a bare ID: the token names the item.
  const { id: mediaId } = useMediaAddress();
  const host = usePlayerHost();
  const { titleSelectionEpoch, rejoiningPlayback } = useMemo(() => {
    // Catalog playback pushes an entry. Player advances replace it and carry
    // the selection explicitly. Back, Forward and reload restore playback.
    const state = location.state as { selectPartyTitle?: boolean } | null;
    const selectsTitle =
      navigationType !== "POP" && (state?.selectPartyTitle ?? navigationType === "PUSH");
    const titleSelectionEpoch = selectsTitle ? hostTitleSelectionEpoch() : undefined;
    return {
      titleSelectionEpoch,
      rejoiningPlayback: titleSelectionEpoch === undefined && getWatchParty().status === "active",
    };
  }, [location, navigationType]);
  const params = new URLSearchParams(location.search);
  const skip = params.has("t") || params.has(MUSIC_QUEUE_PARAM);
  const carried =
    !!location.state && (location.state as { skipPrePlay?: unknown }).skipPrePlay === true;
  const bypass = skip || carried || !mediaId || rejoiningPlayback;
  const [decided, setDecided] = useState<{ mediaId: string; items: PrePlayItem[] } | null>(null);

  useEffect(() => {
    if (bypass || !mediaId) return;
    const controller = new AbortController();
    Promise.resolve()
      .then(() => fetchPrePlayQueue(mediaId, controller.signal))
      .catch((): PrePlayItem[] => [])
      .then((items) => {
        if (!controller.signal.aborted) setDecided({ mediaId, items: items ?? [] });
      });
    return () => controller.abort();
  }, [bypass, mediaId]);

  const finish = useCallback(() => {
    if (mediaId) setDecided({ mediaId, items: [] });
  }, [mediaId]);

  const current = decided && decided.mediaId === mediaId ? decided : null;
  const feature = !!mediaId && (bypass || (current && current.items.length === 0));
  // Once the address resolves, a pre-play queue (or the wait for one) ends
  // whatever the host plays. The address may resolve to the docked player.
  useLayoutEffect(() => {
    if (host && mediaId !== null && !feature) host.stop();
  }, [host, mediaId, feature]);
  if (mediaId === "") {
    return (
      <main className="flex min-h-screen flex-col items-center justify-center gap-3 bf-media bg-scrim">
        <p className="text-on-scrim/75" role="alert">
          {t("playbackMediaNotAvailable")}
        </p>
        <Button onClick={() => navigate("/", { replace: true })}>{t("backToBrowse")}</Button>
      </main>
    );
  }
  if (mediaId && feature) {
    const entry = { mediaId, state: location.state, search: location.search, titleSelectionEpoch };
    return host ? <AttachToHost host={host} entry={entry} /> : <FeaturePlayer entry={entry} />;
  }
  if (current) {
    return (
      <main className="flex min-h-screen flex-col bf-media bg-scrim">
        <h1 className="sr-only">{t("prePlayRegion")}</h1>
        <PrePlayStage key={current.mediaId} items={current.items} onDone={finish} />
      </main>
    );
  }
  return (
    <main className="flex min-h-screen items-center justify-center bf-media bg-scrim">
      <div role="status">
        <LoaderCircle aria-hidden className="size-10 animate-spin text-on-scrim/80" />
        <span className="sr-only">{t("playerLoading")}</span>
      </div>
    </main>
  );
}

// The watch route's side of the host: it shows this entry in the full player
// while the route is open and lets the host decide what carries on after.
function AttachToHost({
  host,
  entry,
}: {
  host: NonNullable<ReturnType<typeof usePlayerHost>>;
  entry: WatchEntry;
}) {
  const { mediaId, state, search, titleSelectionEpoch } = entry;
  useLayoutEffect(() => {
    host.attach({ mediaId, state, search, titleSelectionEpoch });
  }, [host, mediaId, state, search, titleSelectionEpoch]);
  useLayoutEffect(() => () => host.detach(), [host]);
  return null;
}

/** The player the host mounts above the routes (CORE-1633). */
export function HostedPlayer(props: HostedPlayerProps) {
  return <FeaturePlayer {...props} />;
}

function FeaturePlayer({
  entry,
  docked = false,
  onAudioChange,
  onAdvanceDocked,
  onStop,
}: Partial<Omit<HostedPlayerProps, "entry">> & { entry: WatchEntry }) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const navigate = useNavigate();
  const location = useLocation();
  const mediaId = entry.mediaId;
  const { revealed, session } = useSession();
  const profileID = session?.profileID;
  const identity = useViewerIdentity();
  const epoch = getIdentityEpochSignal();
  const params = new URLSearchParams(entry.search);
  const queueProfile = params.get(MUSIC_QUEUE_PARAM);
  const serverQueueRun = queueProfile !== null;
  const queueMatchesProfile = !!profileID && queueProfile === profileID;
  const [musicQueueOpen, setMusicQueueOpen] = useState(false);
  const { view: musicQueue, refresh: refreshMusicQueue, readMedia, isCurrent } = useMusicQueue();
  const queueAllowed = !serverQueueRun || (queueMatchesProfile && !musicQueue?.invalid);
  const queueReady = queueAllowed && (!serverQueueRun || musicQueue?.rows !== undefined);
  const queueReadFailed = serverQueueRun && queueAllowed && musicQueue?.failed;
  const serverItems = useMemo(() => musicQueue?.rows?.filter(playableMusic), [musicQueue?.rows]);
  const serverNeighbours = useMemo(
    () => musicQueueNeighbours(musicQueue?.rows, mediaId),
    [musicQueue?.rows, mediaId],
  );
  const queueRoute = useMemo(
    () => ({ mediaId, queueProfile, identity, epoch, revealed, profileID }),
    [mediaId, queueProfile, identity, epoch, revealed, profileID],
  );

  const needsInitialQueueRead = serverQueueRun && queueMatchesProfile && !musicQueue;
  useEffect(() => {
    if (needsInitialQueueRead) void refreshMusicQueue();
  }, [needsInitialQueueRead, refreshMusicQueue]);
  useEffect(() => {
    if (musicQueueOpen && queueAllowed) void refreshMusicQueue();
  }, [musicQueueOpen, queueAllowed, refreshMusicQueue]);
  const stageRef = useRef<HTMLDivElement | null>(null);
  const [menuOpen, setMenuOpen] = useState(false);
  // Watch together on the detail page (CORE-1764) arrives with the party started
  // and asks for the invite panel open.
  const [partyOpen, setPartyOpen] = useState(() => readWatchPartyIntent(location.state));
  const [clientPrefs, setClientPrefs] = useState<ClientPref[]>([]);
  // The media(id) read, by the Media it answered for: null info when it found
  // nothing or failed. Only the read for this Media counts, so a new item
  // never shows the previous item's title, artwork or credits.
  const [mediaRead, setMediaRead] = useState<{
    route: typeof queueRoute;
    info: GenericMediaInfo | null;
  } | null>(null);
  const mediaReadIsCurrent = mediaRead?.route === queueRoute && !epoch?.aborted;
  const queueTitlesAllowed = !serverQueueRun || queueReady;
  const media = queueTitlesAllowed && mediaReadIsCurrent ? mediaRead.info : null;

  // The catalog card / hero (and Up Next) carry the Media title forward in nav
  // state, with the siblings it was shown with as the play queue. The media(id)
  // read below fills in the title for a deep link, plus artwork and credits.
  const navState = serverQueueRun ? {} : readPlayerNavState(entry.state, identity);
  const queueIdentity = navState.queueIdentity;
  const navTitle = navState.title || media?.title || "";
  // Docked, the browse page under the mini player names the tab (CORE-2322).
  // A Revealed session may be playing a restricted title, which stays out of
  // the browser history (tab titles are kept there).
  useDocumentTitle(docked ? null : serverQueueRun ? "" : revealed ? null : navTitle, t("brand"));
  useCanonicalMediaAddress("watch", media, !docked && !serverQueueRun);
  const startAtSeconds = parseTimestampParam(params.get("t"));
  // Media classes playback-info has reported (this item's and its declared
  // next item's), by Media id (CORE-1490).
  const [learnedClasses, setLearnedClasses] = useState<Record<string, string>>({});
  // An item that plays on its own neither queues its siblings nor is queued
  // after one (CORE-3147).
  const navQueue = useMemo(
    () => (navState.queue ? queueForPlaying(navState.queue, mediaId || "") : undefined),
    [navState.queue, mediaId],
  );
  const classedQueue = useMemo(
    () => (navQueue ? withQueueClasses(navQueue, learnedClasses) : undefined),
    [navQueue, learnedClasses],
  );
  const entryClass = serverQueueRun
    ? "audio"
    : classedQueue?.find((item) => item.id === mediaId)?.mediaClass;
  const declaredNextID = serverQueueRun
    ? (serverNeighbours.next?.id ?? "")
    : classedQueue
      ? (queueNeighbours(queueOfClass(classedQueue, entryClass, mediaId || ""), mediaId || "").next
          ?.id ?? "")
      : undefined;
  // The Version this profile picked for this Media (CORE-1676): a pick made in
  // this player during this playback start, else the one the detail page sent
  // with its track picks (CORE-2442; "" is Auto), else a pick made in this
  // player earlier, else the one remembered from an earlier visit. The detail
  // page's pick rides the nav state so blocked storage cannot split it from its
  // tracks. A start is its startID, which docking and expanding keep, so the
  // player's own pick survives the mini player and gives way to a new start.
  const [versionPick, setVersionPick] = useState<{
    mediaID: string;
    id: string;
    startID: string | undefined;
  } | null>(null);
  const navVersion = navState.tracks?.mediaID === mediaId ? navState.tracks.versionID : undefined;
  const ownPick = versionPick?.mediaID === mediaId ? versionPick : null;
  const selectedVersion =
    ownPick && (navVersion === undefined || ownPick.startID === navState.startID)
      ? ownPick.id
      : (navVersion ?? readVersionChoice(profileID, mediaId || ""));

  const { videoRef, hlsRef, state, retry, restart, switchToHls, adjustment, adjust } = usePlayer(
    queueReady ? mediaId : "",
    {
      couldNotReachServer: t("couldNotReachServer"),
      playbackUnavailable: t("playbackUnavailable"),
      transcodedPlaybackUnsupported: t("transcodedPlaybackUnsupported"),
      playbackUnsupportedCodec: t("playbackUnsupportedCodec"),
      playbackFailed: t("playbackFailed"),
      terminalUnavailable: t("playerTerminalUnavailable"),
      mediaFileMissing: t("playbackMediaFileMissing"),
      mediaNotAvailable: t("playbackMediaNotAvailable"),
      transcodeDisallowed: t("playbackTranscodeDisallowed"),
      streamLimitReached: t("playbackStreamLimitReached"),
      transcodeCapacityReached: t("playbackTranscodeCapacityReached"),
      dolbyVisionUnavailable: t("playbackDolbyVisionUnavailable"),
      hdrToneMapUnavailable: t("playbackHdrToneMapUnavailable"),
      hdrToneMapDisabled: t("playbackHdrToneMapDisabled"),
      sourceEndsEarly: t("playbackSourceEndsEarly"),
    },
    startAtSeconds,
    // "" when a card queue drives playback but has nothing after this item, so
    // an earlier Play all queue is left rather than resumed.
    declaredNextID,
    selectedVersion,
    navState.tracks,
    navState.startID,
    entry.titleSelectionEpoch === undefined,
  );
  // A new Version is another file: remember it, then reload at the playhead.
  const selectVersion = useCallback(
    (versionID: string) => {
      if (!mediaId || versionID === selectedVersion) return;
      writeVersionChoice(profileID, mediaId, versionID);
      setVersionPick({ mediaID: mediaId, id: versionID, startID: navState.startID });
      retry();
    },
    [mediaId, profileID, selectedVersion, retry, navState.startID],
  );

  // The queue stays within the playing item's class (CORE-1490): a track
  // started from a shelf that mixes tracks and videos steps only through the
  // tracks. The narrowed queue (with what was learned) is the one carried to
  // the next item, so a dropped entry stays dropped.
  const playingClass = state.info?.mediaClass || entryClass;
  const catalogQueue = useMemo(
    () => (classedQueue ? [...queueOfClass(classedQueue, playingClass, mediaId || "")] : undefined),
    [classedQueue, playingClass, mediaId],
  );
  const queue = serverQueueRun ? serverItems : catalogQueue;
  const { previous: previousInQueue, next: nextInQueue } = serverQueueRun
    ? serverNeighbours
    : queueNeighbours(catalogQueue, mediaId);
  const info = state.info;
  const infoIsCurrent = !!state.info && state.mediaID === mediaId;
  // An image reached through a watch link (a row or deep link that could not
  // know its class) belongs in the image viewer (CORE-0542). Leaving the
  // watch route detaches the host, which drops a non-audio entry.
  const imageEntry =
    !docked &&
    !serverQueueRun &&
    !!mediaId &&
    ((infoIsCurrent && state.info?.mediaClass === "image") ||
      media?.mediaClass === "image" ||
      entryClass === "image");
  useEffect(() => {
    if (imageEntry && mediaId) {
      navigate(mediaPath("view", mediaId), { replace: true, state: entry.state });
    }
  }, [imageEntry, mediaId, navigate, entry.state]);
  const mediaIdRef = useRef(mediaId);
  mediaIdRef.current = mediaId;
  useEffect(() => {
    // Keyed on the info document alone: a new route renders once with the
    // previous item's info before the engine resets, which must not be
    // credited to the new id.
    const id = mediaIdRef.current;
    const learned: Record<string, string> = {};
    if (id && info?.mediaClass) learned[id] = info.mediaClass;
    if (info?.next?.mediaID && info.next.mediaClass) {
      learned[info.next.mediaID] = info.next.mediaClass;
    }
    if (Object.keys(learned).length === 0) return;
    setLearnedClasses((known) =>
      Object.entries(learned).every(([key, value]) => known[key] === value)
        ? known
        : { ...known, ...learned },
    );
  }, [info]);

  const playing = queueReady && state.phase === "playing";
  // Song seconds per stream second under an applied tempo (CORE-1659).
  const songScale = songSecondsPerStreamSecond(state.info);

  // Key and tempo controls (CORE-1659): only when the server advertises the
  // ranges and plays this Media as a karaoke song (an audio item served as a composed
  // picture). A key
  // or tempo change restarts the stream, which clears state.info for a moment,
  // so the song verdict is kept per Media and the controls keep their focus.
  const [adjustmentRanges, setAdjustmentRanges] = useState<PlaybackAdjustmentRanges | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    fetchPlaybackAdjustmentRanges(controller.signal).then((ranges) => {
      if (!controller.signal.aborted) setAdjustmentRanges(ranges);
    });
    return () => controller.abort();
  }, []);
  // Once a Media is established as a karaoke song it stays one until the Media
  // changes: a declined key or tempo change can come back as bare audio, and
  // dropping the verdict then would unmount the focused controls and the
  // "Not applied" notice. Only info loaded for this Media counts: the first
  // render after a route change still carries the previous Media's info.
  const karaokeRef = useRef<{ mediaID: string; karaoke: boolean } | null>(null);
  const established = karaokeRef.current?.mediaID === (mediaId || "") && karaokeRef.current.karaoke;
  if (state.info && state.mediaID === (mediaId || "") && !established) {
    karaokeRef.current = {
      mediaID: mediaId || "",
      karaoke: state.info.mediaClass === "audio" && isComposedPictureServed(state.info),
    };
  }
  const karaokeSong = karaokeRef.current?.mediaID === (mediaId || "") && karaokeRef.current.karaoke;
  const showKaraokeControls =
    adjustmentRanges !== null && karaokeSong && state.phase !== "terminal";
  // Judged only once the stream that was asked for this adjustment is playing,
  // so a restart in flight never flashes "Not applied".
  const karaokeApplied =
    playing && state.info && sameAdjustment(state.adjustment, adjustment)
      ? adjustmentApplied(state.info, adjustment)
      : null;
  const showSpinner = useMediaLoading(videoRef, state.phase === "loading" || playing);
  const [videoNode, setVideoNode] = useState<HTMLVideoElement | null>(null);
  const bindVideo = useCallback(
    (node: HTMLVideoElement | null) => {
      videoRef.current = node;
      setVideoNode(node);
    },
    [videoRef],
  );
  const captionLoad = useCaptionLoad(videoNode);
  const isAudio = state.info?.mediaClass === "audio";
  // Before playback-info answers, a queue entry that reports its class already
  // routes a track to the audio stage instead of an empty video frame
  // (CORE-1309). A song whose composed picture is served shows the picture
  // instead (CORE-1656).
  const itemShowsAudioStage = state.info
    ? isAudio && !hasPicture(state.info)
    : entryClass === "audio";
  // An advance resets the engine for the next item (resume, markers, progress
  // beacon), which clears its playback-info while the next one loads. The audio
  // stage, and the transport over it, stay up across that load (CORE-2363):
  // until this item's playback-info says otherwise, an advance from the audio
  // stage is taken to land on it again.
  const infoPending = !infoIsCurrent && (state.phase === "loading" || state.phase === "playing");
  const [audioStageUp, setAudioStageUp] = useState(false);
  if (!infoPending && audioStageUp !== itemShowsAudioStage) setAudioStageUp(itemShowsAudioStage);
  const advancing = infoPending && audioStageUp;
  const showAudioStage = itemShowsAudioStage || advancing;
  const credits = media?.credits ?? NO_CREDITS;
  // The per-collection skip interval belongs to the item's container Entity
  // (the show, the album …), named by its title; an item with none has no row.
  const collection = credits.find((credit) => credit.archetype === "container");
  const collectionID = collection?.entityID || "";
  const resolvedPrefs = resolvePreferences(clientPrefs, collectionID);
  const showSkipInterval = collectionID ? skipIntervalOverrideValue(clientPrefs, collectionID) : "";
  const subtitleAppearance = useMemo(() => resolveSubtitleAppearance(clientPrefs), [clientPrefs]);
  const collectionTitle = collection
    ? collection.title || displayString(collection.kindDisplayName, displayLocale)
    : "";
  const showSkipIntervalLabel = collection
    ? t("collectionSkipIntervalLabel", { title: collectionTitle })
    : "";
  // A video shows its backdrop until the first frame (CORE-1363).
  const videoPoster =
    media && state.info && !isAudio
      ? media.artworkSlots?.backdrop || media.artworkSlots?.thumb || ""
      : "";
  const artworkURL = media
    ? media.artworkSlots?.poster || media.artworkSlots?.thumb || media.artworkSlots?.backdrop || ""
    : "";
  const queueIndex = queue && mediaId ? queue.findIndex((item) => item.id === mediaId) : -1;
  const queuePosition =
    queue && queue.length > 1 && queueIndex >= 0
      ? t("queuePosition", { position: queueIndex + 1, total: queue.length })
      : "";
  // The words and picture the audio stage and the mini player show. Until the
  // next item's data arrives (its media read, or its playback starting) they
  // keep the item on screen, then change together through the track-change
  // motion, so an advance never passes through an empty card (CORE-2218,
  // CORE-2363).
  const liveStage: StageItem = {
    key: mediaId || navTitle,
    title: navTitle,
    artworkURL,
    credits,
    queuePosition,
  };
  const stageReady = !mediaId || mediaReadIsCurrent || (playing && infoIsCurrent);
  const [heldStage, setHeldStage] = useState<StageItem | null>(null);
  if (stageReady || serverQueueRun) {
    const keep = showAudioStage && !serverQueueRun ? liveStage : null;
    if (!sameStageItem(keep, heldStage)) setHeldStage(keep);
  }
  const stage = serverQueueRun || stageReady || !heldStage ? liveStage : heldStage;

  const { chromeHidden, reveal } = useChromeAutoHide({
    stageRef,
    videoRef,
    menuOpen: menuOpen || partyOpen,
  });

  // Advance to another Media's watch route, carrying its title and the queue
  // forward. The new item resets the engine (resume, markers, trickplay,
  // progress beacon) while this player stays mounted; `replace` keeps Back
  // returning to the catalog rather than stepping through every track played.
  // Docked in the mini player, the advance stays on the page the listener is
  // browsing.
  const playItem = useCallback(
    (id: string, title: string, selectPartyTitle = true, musicProfile?: string) => {
      const search = musicProfile
        ? `?${new URLSearchParams({ [MUSIC_QUEUE_PARAM]: musicProfile })}`
        : "";
      const state = musicProfile
        ? { selectPartyTitle }
        : {
            title,
            skipPrePlay: true,
            selectPartyTitle,
            ...(catalogQueue ? { queue: catalogQueue, queueIdentity } : {}),
          };
      if (docked && onAdvanceDocked) {
        const titleSelectionEpoch = selectPartyTitle ? hostTitleSelectionEpoch() : undefined;
        onAdvanceDocked({ mediaId: id, state, search, titleSelectionEpoch });
        return;
      }
      navigate(`${mediaPath("watch", id)}${search}`, { replace: true, state });
    },
    [navigate, catalogQueue, queueIdentity, docked, onAdvanceDocked],
  );

  // Watch Party (CORE-1677): the party moves every member's player to the
  // title it is on and keeps the <video> on its shared timeline.
  const followParty = useCallback((id: string) => playItem(id, "", false), [playItem]);
  const playerAttached = queueReady && state.phase !== "message" && state.phase !== "terminal";
  const partySeekAutoSkip = useRef<PartySeekAutoSkip | null>(null);
  const watchParty = useWatchParty({
    videoRef,
    attached: playerAttached,
    mediaId: mediaId || "",
    titleSelectionEpoch: entry.titleSelectionEpoch,
    onFollow: followParty,
    songScale,
    partySeekAutoSkip,
  });
  // A party member's Up Next and autoplay defer to the party (CORE-1766).
  const partyLeads = watchPartyDefersUpNext(watchParty.party);

  // Up Next auto-advance: the server-issued next Media (a queue's next item
  // when one was declared, else the server's own next-item pick).
  const advanceToNext = useCallback(
    (next: PlaybackNext) => {
      const queued = queue?.find((item) => item.id === next.mediaID);
      playItem(next.mediaID, next.title || queued?.title || "");
    },
    [playItem, queue],
  );

  const goToQueueItem = useCallback(
    (item: PlayQueueItem | undefined) => {
      if (item) playItem(item.id, item.title);
    },
    [playItem],
  );
  // The server's next issuance, unless the class filter has excluded it
  // (CORE-1732): playback-info is not asked again when a learned class changes
  // the declared next item, so a card queue's issuance of another class stays
  // on the info document after the queue dropped it.
  const reportedNext = state.info?.next ?? undefined;
  const issuedNext = serverQueueRun
    ? state.mediaID === mediaId && reportedNext?.mediaID === serverNeighbours.next?.id
      ? reportedNext
      : undefined
    : admissibleNext(reportedNext, catalogQueue, playingClass, learnedClasses);
  // Decode early, then let the ended read authorize both the sound and the route.
  const confirmGaplessAdvance = useGaplessAudio({
    videoRef,
    mediaID: mediaId || "",
    stream: state.stream,
    next: serverQueueRun ? issuedNext : reportedNext,
    advanceID: nextInQueue?.id ?? issuedNext?.mediaID,
    enabled:
      playing &&
      showAudioStage &&
      !karaokeSong &&
      !partyLeads &&
      state.mediaID === (mediaId || "") &&
      (serverQueueRun ? !!issuedNext : !!(nextInQueue || issuedNext?.streams)),
    waitForAdvance: serverQueueRun,
  });
  type QueueAction = "start" | "next" | "previous" | "ended";
  const [failedAction, setFailedAction] = useState<{
    action: QueueAction;
    route: typeof queueRoute;
  }>();
  const retryAction = failedAction?.route === queueRoute && !epoch?.aborted && failedAction.action;
  const pendingQueueAction = useRef<{ action: QueueAction } | null>(null);
  const activeQueueRoute = useRef(queueRoute);
  const playItemRef = useRef(playItem);
  useLayoutEffect(() => {
    playItemRef.current = playItem;
  }, [playItem]);
  useLayoutEffect(() => {
    activeQueueRoute.current = queueRoute;
    return () => {
      pendingQueueAction.current = null;
    };
  }, [queueRoute]);

  const runQueueAction = useCallback(
    async (action: QueueAction) => {
      if (
        !profileID ||
        (action !== "start" && !queueMatchesProfile) ||
        activeQueueRoute.current !== queueRoute ||
        !isCurrent()
      ) {
        return;
      }
      const request = { action };
      pendingQueueAction.current = request;
      const rows = await refreshMusicQueue(true);
      if (pendingQueueAction.current !== request) return;
      pendingQueueAction.current = null;
      if (rows === undefined || !isCurrent()) return;
      if (rows === null) {
        setFailedAction({ action, route: queueRoute });
        return;
      }
      setFailedAction(undefined);
      if (action === "start") {
        const first = rows.find(playableMusic);
        if (!first) return;
        setMusicQueueOpen(false);
        if (first.id === mediaId) {
          restart(0);
          if (queueMatchesProfile) return;
        }
        playItemRef.current(first.id, "", true, profileID);
      } else {
        const neighbours = musicQueueNeighbours(rows, mediaId);
        const target = (action === "previous" ? neighbours.previous : neighbours.next)?.id;
        if (!target) return;
        if (action === "ended") confirmGaplessAdvance(target);
        playItemRef.current(target, "", true, profileID);
      }
    },
    [
      profileID,
      queueMatchesProfile,
      queueRoute,
      isCurrent,
      refreshMusicQueue,
      mediaId,
      restart,
      confirmGaplessAdvance,
    ],
  );
  const retryMusicQueue = () =>
    retryAction ? void runQueueAction(retryAction) : void refreshMusicQueue();
  const playNext = useMemo(() => {
    if (partyLeads) return undefined;
    if (serverQueueRun) {
      return nextInQueue ? () => void runQueueAction("next") : undefined;
    }
    if (nextInQueue) return () => goToQueueItem(nextInQueue);
    if (issuedNext?.streams) return () => advanceToNext(issuedNext);
    return undefined;
  }, [
    partyLeads,
    serverQueueRun,
    nextInQueue,
    issuedNext,
    runQueueAction,
    goToQueueItem,
    advanceToNext,
  ]);
  // A card queue names its own previous item; without one, a Play all queue
  // that holds this item may (stepping back keeps that queue going).
  const playAllPrevious = useMemo(
    () => (serverQueueRun || queue || !mediaId ? null : previousInPlayQueue(mediaId)),
    [serverQueueRun, queue, mediaId],
  );
  const playPrevious = useMemo(() => {
    if (serverQueueRun) {
      return previousInQueue ? () => void runQueueAction("previous") : undefined;
    }
    if (previousInQueue) return () => goToQueueItem(previousInQueue);
    if (playAllPrevious) return () => playItem(playAllPrevious.id, playAllPrevious.title);
    return undefined;
  }, [serverQueueRun, runQueueAction, previousInQueue, playAllPrevious, goToQueueItem, playItem]);

  // Audio moves straight on to the next item when one ends (no countdown card,
  // which suits a film or an episode, not a track).
  // "Are you still watching?" (CORE-1689): after the Profile's run of
  // countdown-started items, the prompt takes the place of the next autoplay.
  // The audio path has no countdown, so it never counts or asks.
  const stillWatching = useStillWatching({
    videoRef,
    mediaID: mediaId || "",
    after: resolvedPrefs.stillWatchingAfter,
    next: issuedNext,
    enabled: !serverQueueRun && !isAudio && !partyLeads,
    onContinue: advanceToNext,
  });
  const upNext = useUpNext({
    videoRef,
    next:
      serverQueueRun || isAudio || partyLeads || stillWatching.armed || stillWatching.prompting
        ? null
        : issuedNext,
    onAdvance: advanceToNext,
    onAutoAdvance: (next) => noteAutoAdvance(next.mediaID),
  });
  useLayoutEffect(() => {
    const video = videoRef.current;
    if (!isAudio || !playing || !video || partyLeads) return;
    if (!serverQueueRun && !playNext) return;
    const advance = () => {
      // Let the explicit start finish its read before changing tracks.
      if (pendingQueueAction.current?.action === "start") {
        if (!serverQueueRun) handBack(video);
        return;
      }
      if (serverQueueRun) void runQueueAction("ended");
      else playNext?.();
    };
    video.addEventListener("ended", advance);
    return () => video.removeEventListener("ended", advance);
  }, [isAudio, playing, partyLeads, serverQueueRun, runQueueAction, playNext, videoRef]);
  // A sequenced item that ends with nothing to advance to offers the next one
  // in its sequence, as a choice only (CORE-3013, ADR-0055 §2.4).
  const sequenceNext = useSequenceFinish({
    video: videoNode,
    media,
    mediaID: mediaId || "",
    enabled: isAudio && !docked && !serverQueueRun && !partyLeads && !playNext,
  });
  const [dismissedFinish, setDismissedFinish] = useState<string | null>(null);
  const finishKey = sequenceNext ? `${mediaId}\n${sequenceNext.item.id}` : "";
  // Playing again clears a dismissal, so the next end offers it again.
  if (!sequenceNext && dismissedFinish !== null) setDismissedFinish(null);
  const showSequenceNext = !!sequenceNext && dismissedFinish !== finishKey;
  // The card's appearance reveals chrome once; the card itself stays visible
  // and clickable regardless of auto-hide (player-deferred.css).
  const upNextVisible = upNext.visible || stillWatching.prompting || showSequenceNext;
  useEffect(() => {
    if (upNextVisible) reveal();
  }, [upNextVisible, reveal]);

  // Auto-skip (ADR-0048 Decision 6): composes with, never replaces, Up Next's
  // separate end-of-file auto-advance above.
  const autoSkip = useAutoSkip({
    videoRef,
    attached: playerAttached,
    markers: state.markers,
    mediaID: mediaId || "",
    prefs: {
      intro: resolvedPrefs.autoSkipIntro,
      recap: resolvedPrefs.autoSkipRecap,
      credits: resolvedPrefs.autoSkipCredits,
    },
    songScale,
    autoSkipLead: watchParty.autoSkipLead,
    seekToPartySkip: watchParty.seekToPartySkip,
    partySeekAutoSkip,
  });

  // Browser Media Session (ADR-0084 §43): mirror transport state to the OS and
  // route OS transport intents back through the engine-owned <video> and the
  // same next-item advance the Up Next card uses. A surface, not authority.
  const people = personLine(credits);
  useMediaSession({
    videoRef,
    active: playing,
    title: navTitle,
    artist: people,
    album: collection?.title,
    artworkURL,
    onNextTrack: playNext,
    onPreviousTrack: playPrevious,
    songScale,
  });

  // A relock during playback drops restricted content from every view at once
  // (ADR-0004). If the session relocks while this restricted Media is playing,
  // pause the stream so nothing keeps rendering behind the terminal block.
  useEffect(() => {
    if (!revealed && videoRef.current) {
      videoRef.current.pause();
    }
  }, [revealed, videoRef]);
  // Docked, there is no block to show: a relock (revealed turning false)
  // closes the mini player. Only the transition counts; ordinary browsing is
  // unrevealed, and docking must not pause it.
  const wasRevealed = useRef(revealed);
  useEffect(() => {
    if (wasRevealed.current && !revealed && docked) onStop?.();
    wasRevealed.current = revealed;
  }, [revealed, docked, onStop]);

  // The host keeps an audio Media playing once the watch route closes
  // (CORE-1633); anything else stops with the route. Docked, a terminal block,
  // a playback error, or an advance into a Media that is not audio ends
  // playback instead.
  const audioMedia = state.info ? isAudio : entryClass === "audio" || advancing;
  useEffect(() => {
    onAudioChange?.(audioMedia);
  }, [audioMedia, onAudioChange]);
  const dockedEnds =
    docked &&
    (!queueAllowed ||
      state.phase === "terminal" ||
      state.phase === "message" ||
      (infoIsCurrent && state.info?.mediaClass !== "audio"));
  useEffect(() => {
    if (dockedEnds) onStop?.();
  }, [dockedEnds, onStop]);

  // Back to the full player from the mini player: the watch route for the
  // same entry, which re-attaches to the playing engine.
  const openFullPlayer = useCallback(() => {
    const carried =
      !serverQueueRun && entry.state && typeof entry.state === "object" ? entry.state : {};
    navigate(`${mediaPath("watch", mediaId)}${entry.search}`, {
      state: { ...carried, skipPrePlay: true, selectPartyTitle: false },
    });
  }, [navigate, mediaId, entry.state, entry.search, serverQueueRun]);

  // Queue metadata comes from an uncached read for this route and identity.
  useEffect(() => {
    if (!mediaId || !queueTitlesAllowed) return;
    const controller = new AbortController();
    const settle = (info: GenericMediaInfo | null) => {
      if (!controller.signal.aborted && !epoch?.aborted) {
        setMediaRead({ route: queueRoute, info });
      }
    };
    const read = serverQueueRun ? readMedia : fetchMedia;
    void read(mediaId, controller.signal).then(settle, () => settle(null));
    return () => controller.abort();
  }, [mediaId, queueTitlesAllowed, epoch, queueRoute, serverQueueRun, readMedia]);

  // Keys edited in this identity epoch keep their local value over a Profile
  // read, which may predate the write. A Profile switch starts a new epoch.
  const editedPrefKeys = useRef({ epoch: getIdentityEpochSignal(), keys: new Set<string>() });
  const editedKeys = useCallback(() => {
    const epoch = getIdentityEpochSignal();
    if (editedPrefKeys.current.epoch !== epoch) editedPrefKeys.current = { epoch, keys: new Set() };
    return editedPrefKeys.current.keys;
  }, []);
  useEffect(() => {
    let current = true;
    void fetchClientPrefs()
      .then((prefs) => {
        if (!current) return;
        const edited = editedKeys();
        setClientPrefs((local) => [
          ...prefs.filter((pref) => !edited.has(pref.key)),
          ...local.filter((pref) => edited.has(pref.key)),
        ]);
      })
      .catch(() => {});
    return () => {
      current = false;
    };
  }, [mediaId, editedKeys]);

  const setShowSkipInterval = useCallback(
    (value: SkipInterval | "") => {
      if (!collectionID) return;
      const key = skipIntervalOverrideKey(collectionID);
      setClientPrefs((prefs) => [
        ...prefs.filter((pref) => pref.key !== key),
        ...(value ? [{ key, value }] : []),
      ]);
      void setClientPref(key, value).catch(() => {});
    },
    [collectionID],
  );

  // Subtitle appearance roams with the Profile (subtitle.* keys, CORE-1681).
  // Writes go out one at a time so a slow earlier write cannot land last, and
  // a write still queued when its identity epoch ends is dropped.
  const prefWrites = useRef<Promise<unknown>>(Promise.resolve());
  const storePrefs = useCallback(
    (entries: ClientPref[]) => {
      const keys = new Set(entries.map((entry) => entry.key));
      const edited = editedKeys();
      for (const key of keys) edited.add(key);
      setClientPrefs((prefs) => [
        ...prefs.filter((pref) => !keys.has(pref.key)),
        ...entries.filter((entry) => entry.value),
      ]);
      const epoch = getIdentityEpochSignal();
      for (const { key, value } of entries) {
        prefWrites.current = prefWrites.current
          .then(() => (epoch?.aborted ? undefined : setClientPref(key, value)))
          .catch(() => {});
      }
    },
    [editedKeys],
  );
  const setSubtitleAppearance = useCallback(
    (field: SubtitleAppearanceField, value: string) =>
      storePrefs([
        { key: SUBTITLE_APPEARANCE_KEYS[field], value: encodeSubtitleAppearance(field, value) },
      ]),
    [storePrefs],
  );
  // Reset clears every key, including any the Profile read has not returned yet.
  const resetSubtitleAppearance = useCallback(
    () => storePrefs(Object.values(SUBTITLE_APPEARANCE_KEYS).map((key) => ({ key, value: "" }))),
    [storePrefs],
  );

  const shareAt = useCallback(
    (seconds: number) => {
      if (!mediaId) return;
      const href = buildTimestampedLink(mediaId, seconds);
      const title = navTitle || t("brand");
      if (navigator.share) {
        void navigator.share({ title, url: href }).catch(() => {});
        return;
      }
      void navigator.clipboard?.writeText(href).catch(() => {});
    },
    [mediaId, navTitle, t],
  );

  function back() {
    if (location.key !== "default") navigate(-1);
    else navigate("/", { replace: true });
  }

  // A remote stop ends playback on a known page. Going back could land on an
  // earlier watch route (a remote play-media pushes one) and start that player.
  // The host would dock a closing audio route and keep it playing, so the stop
  // ends host playback first (CORE-1875).
  function stopRemotely() {
    onStop?.();
    navigate(mediaId ? mediaPath("media", mediaId) : "/", { replace: true });
  }

  const queueFailureNotice = queueReady && !musicQueueOpen && (retryAction || queueReadFailed) && (
    <div
      className={`${docked ? "fixed" : "absolute"} bottom-24 left-1/2 z-10 flex -translate-x-1/2 items-center gap-3 rounded-md bg-scrim/80 px-3 py-2 text-sm text-on-scrim`}
      role="alert"
    >
      <span>{t("musicQueueReadFailed")}</span>
      <button
        type="button"
        className="rounded-sm underline underline-offset-2"
        onClick={retryMusicQueue}
      >
        {t("tryAgain")}
      </button>
    </div>
  );

  // Docked, the full player stays mounted but hidden, so its media element
  // (and with it playback) outlives the watch route; only the mini player
  // shows. The fragment keeps the element in the same place in both modes.
  return (
    <>
      <main hidden={docked} className="flex min-h-screen flex-col bf-media bg-scrim">
        <SkipLink />
        <h1 className="sr-only">{navTitle || t("brand")}</h1>
        <div className="bf-player-bar">
          <Button variant="outline" size="sm" onClick={back}>
            {t("back")}
          </Button>
          {showKaraokeControls && adjustmentRanges ? (
            <KaraokeControls
              ranges={adjustmentRanges}
              adjustment={adjustment}
              applied={karaokeApplied}
              onChange={adjust}
            />
          ) : null}
          {mediaId && (
            <WatchPartyPanel
              party={watchParty.party}
              open={partyOpen}
              onOpenChange={setPartyOpen}
              onStart={watchParty.start}
              onLeave={watchParty.leave}
            />
          )}
        </div>

        {!queueAllowed ||
        (queueReadFailed && !queueReady) ||
        (queueReady && (state.phase === "message" || state.phase === "terminal")) ? (
          <div
            id={docked ? undefined : MAIN_CONTENT_ID}
            tabIndex={-1}
            className="flex flex-1 items-center justify-center p-6"
          >
            <div className="flex max-w-md flex-col items-center gap-3 text-center">
              <AlertCircle aria-hidden className="size-7 text-[var(--state-danger)]" />
              {navTitle ? (
                <h2 className="m-0 text-xl font-semibold text-on-scrim">{navTitle}</h2>
              ) : null}
              <p className="m-0 text-[15px] leading-relaxed text-on-scrim/75" role="alert">
                {!queueAllowed
                  ? t("playerTerminalUnavailable")
                  : queueReadFailed
                    ? t("musicQueueReadFailed")
                    : state.message}
              </p>
              {queueReady && state.detail ? (
                <details className="text-sm text-on-scrim/55">
                  <summary className="cursor-pointer">{t("playbackErrorDetails")}</summary>
                  <p className="m-0 mt-1 break-words">
                    {t("playbackServerReason", { reason: state.detail })}
                  </p>
                </details>
              ) : null}
              <div className="mt-3">
                {queueReadFailed ? (
                  <Button onClick={() => void refreshMusicQueue()}>{t("tryAgain")}</Button>
                ) : !queueAllowed || state.terminal || state.action === "back" ? (
                  <Button onClick={back}>{t("backToBrowse")}</Button>
                ) : (
                  <Button onClick={() => retry()}>{t("tryAgain")}</Button>
                )}
              </div>
            </div>
          </div>
        ) : (
          <div
            ref={stageRef}
            id={docked ? undefined : MAIN_CONTENT_ID}
            tabIndex={-1}
            className={"bf-player-stage" + (playing && chromeHidden ? " is-chrome-hidden" : "")}
          >
            {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
            <video ref={bindVideo} playsInline controls={false} poster={videoPoster || undefined} />
            {showAudioStage && (
              <AudioStage
                key={serverQueueRun ? (media ? mediaId : "") : undefined}
                title={stage.title}
                artworkURL={stage.artworkURL}
                credits={stage.credits}
                queuePosition={stage.queuePosition}
                videoRef={videoRef}
                stream={state.stream}
                trackKey={stage.key}
                next={
                  issuedNext
                    ? { key: issuedNext.mediaID, stream: decodableStream(issuedNext) }
                    : null
                }
              />
            )}
            {(showSpinner || !queueReady) && (
              // Over the audio card a wait is a small cue, not a cover (CORE-2363).
              <div
                className={
                  showAudioStage
                    ? "pointer-events-none absolute left-1/2 top-4 z-10 -translate-x-1/2 rounded-full bg-scrim/70 p-2"
                    : "pointer-events-none absolute inset-0 flex items-center justify-center"
                }
                role="status"
              >
                <LoaderCircle
                  aria-hidden
                  className={`${showAudioStage ? "size-5" : "size-10"} animate-spin text-on-scrim/80`}
                />
                <span className="sr-only">{t("playerLoading")}</span>
              </div>
            )}
            {captionLoad.status === "preparing" && (
              <div
                className="absolute bottom-24 left-1/2 z-10 -translate-x-1/2 rounded-md bg-scrim/80 px-3 py-2 text-sm text-on-scrim"
                role="status"
              >
                {t("subtitlesPreparing")}
              </div>
            )}
            {captionLoad.status === "failed" && (
              <div
                className="absolute bottom-24 left-1/2 z-10 flex -translate-x-1/2 items-center gap-3 rounded-md bg-scrim/80 px-3 py-2 text-sm text-on-scrim"
                role="alert"
              >
                <span>{t("subtitlesLoadFailed")}</span>
                <button
                  type="button"
                  className="rounded-sm underline underline-offset-2"
                  onClick={captionLoad.retry}
                >
                  {t("tryAgain")}
                </button>
              </div>
            )}
            {!docked && queueFailureNotice}
            {state.waitingForNetwork ? (
              <p
                className="pointer-events-none absolute left-1/2 top-4 -translate-x-1/2 rounded-md bg-scrim/70 px-3 py-1 text-sm text-on-scrim"
                role="status"
              >
                {t("playerWaitingForNetwork")}
              </p>
            ) : (
              state.recovering && (
                <p
                  className="pointer-events-none absolute left-1/2 top-4 -translate-x-1/2 rounded-md bg-scrim/70 px-3 py-1 text-sm text-on-scrim"
                  role="status"
                >
                  {t("reconnecting")}
                </p>
              )
            )}
            <WatchPartySyncIndicator
              playback={watchParty.playback}
              driftMS={watchParty.driftMS}
              needsTap={watchParty.needsTap}
              onResume={watchParty.resume}
            />
            {(playing || (queueReady && advancing)) && !docked && (
              <PlayerTransport
                videoRef={videoRef}
                hlsRef={hlsRef}
                state={state}
                mediaTitle={navTitle}
                mediaID={mediaId || ""}
                profileID={session?.profileID ?? ""}
                metadataAttribution={media?.metadataAttribution}
                stageRef={stageRef}
                skipInterval={Number(resolvedPrefs.skipInterval)}
                showSkipInterval={showSkipInterval}
                showSkipIntervalLabel={showSkipIntervalLabel}
                showSkipIntervalCollection={collectionTitle}
                onShowSkipIntervalChange={setShowSkipInterval}
                subtitleAppearance={subtitleAppearance}
                onSubtitleAppearanceChange={setSubtitleAppearance}
                onSubtitleAppearanceReset={resetSubtitleAppearance}
                onShare={shareAt}
                onPreviousInQueue={playPrevious}
                onNextInQueue={playNext}
                onPlayMusicQueue={
                  isAudio && profileID && !partyLeads
                    ? () => void runQueueAction("start")
                    : undefined
                }
                onRetryMusicQueue={retryMusicQueue}
                musicQueueOpen={musicQueueOpen}
                musicQueue={musicQueue}
                onMusicQueueOpenChange={setMusicQueueOpen}
                serverQueueNextID={serverQueueRun ? (issuedNext?.mediaID ?? "") : undefined}
                audioStage={showAudioStage}
                onStop={back}
                onRemoteStop={stopRemotely}
                onRemoteRestart={restart}
                switchToHls={switchToHls}
                onSelectVersion={selectVersion}
                onRenegotiate={retry}
                onMenuOpenChange={setMenuOpen}
              />
            )}
            {playing && upNext.visible && (
              <UpNextCard
                title={upNext.title}
                countdown={upNext.countdown}
                onPlay={upNext.advance}
                onDismiss={upNext.dismiss}
              />
            )}
            {playing && sequenceNext && showSequenceNext && (
              <NextInSequenceCard
                next={sequenceNext}
                identity={identity}
                onDismiss={() => setDismissedFinish(finishKey)}
              />
            )}
            {playing && sequencePosition(media) && (
              // Persistent for a sequenced item, so the offer's arrival is announced without moving focus.
              <p className="sr-only" role="status" aria-live="polite">
                {sequenceNext && showSequenceNext
                  ? t("mediaDetailNextInSeriesLabel", { title: sequenceNext.item.title })
                  : ""}
              </p>
            )}
            {playing && stillWatching.prompting && (
              <StillWatchingPrompt
                title={stillWatching.title}
                onContinue={stillWatching.continueWatching}
                onStop={stillWatching.stop}
              />
            )}
            {playing && autoSkip.skipped && (
              <SkippedOverlay
                segment={autoSkip.skipped}
                onUndo={autoSkip.undo}
                onDismiss={autoSkip.dismiss}
              />
            )}
          </div>
        )}
      </main>
      {docked && queueFailureNotice}
      {docked && queueAllowed && (
        <MiniPlayer
          videoRef={videoRef}
          title={stage.title}
          artist={personLine(stage.credits)}
          artworkURL={stage.artworkURL}
          mediaID={mediaId}
          songScale={songScale}
          onNext={playNext}
          onPrevious={playPrevious}
          onOpen={openFullPlayer}
          onStop={() => onStop?.()}
          onRestart={restart}
          waveform={{
            infoMediaID: state.mediaID,
            url: state.info?.assets?.waveform,
            nextMediaID: (serverQueueRun ? issuedNext : reportedNext)?.mediaID,
            nextURL: (serverQueueRun ? issuedNext : reportedNext)?.assets?.waveform,
          }}
        />
      )}
    </>
  );
}

const NO_CREDITS: readonly MediaCredit[] = [];

/** What the audio stage shows for one item. */
interface StageItem {
  key: string;
  title: string;
  artworkURL: string;
  credits: readonly MediaCredit[];
  queuePosition: string;
}

function sameStageItem(a: StageItem | null, b: StageItem | null): boolean {
  if (!a || !b) return a === b;
  return (
    a.key === b.key &&
    a.title === b.title &&
    a.artworkURL === b.artworkURL &&
    a.credits === b.credits &&
    a.queuePosition === b.queuePosition
  );
}

function personLine(credits: readonly MediaCredit[]): string {
  return credits
    .filter((credit) => credit.archetype === "person")
    .map((credit) => credit.title)
    .join(", ");
}

// A card queue keeps to the playing item's class (CORE-1490), so an issued
// next item of another class is not played, whether the issuance or a later
// playback-info read reported that class. Without a card queue the server's
// pick stands.
function admissibleNext(
  next: PlaybackNext | null | undefined,
  queue: readonly PlayQueueItem[] | undefined,
  playingClass: string | undefined,
  learnedClasses: Readonly<Record<string, string>>,
): PlaybackNext | undefined {
  if (!next) return undefined;
  if (!queue || !playingClass) return next;
  const nextClass = next.mediaClass || learnedClasses[next.mediaID];
  return nextClass && nextClass !== playingClass ? undefined : next;
}

// undefined when the link names no position, so playback resumes from saved
// progress; an explicit ?t=0 starts from the beginning.
function parseTimestampParam(raw: string | null): number | undefined {
  if (raw === null || raw.trim() === "") return undefined;
  const seconds = Number(raw);
  return Number.isFinite(seconds) && seconds >= 0 ? Math.floor(seconds) : undefined;
}

export function buildTimestampedLink(mediaId: string, seconds: number): string {
  const url = new URL(
    `${import.meta.env.BASE_URL}${mediaPath("watch", mediaId).slice(1)}`,
    window.location.origin,
  );
  url.searchParams.set("t", String(Math.max(0, Math.floor(seconds))));
  return url.href;
}
