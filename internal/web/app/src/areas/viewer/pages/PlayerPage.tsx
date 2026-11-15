import { AlertCircle, LoaderCircle } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useLocation, useNavigate, useParams } from "react-router";
import { MAIN_CONTENT_ID, SkipLink } from "@/components/chrome/SkipLink";
import { Button } from "@/components/ui/button";
import { getIdentityEpochSignal } from "@/lib/api";
import { displayString } from "@/lib/display";
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
  PlaybackAdjustmentRanges,
  PrePlayItem,
} from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useSession, useViewerIdentity } from "@/lib/session";
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
import { noteAutoAdvance } from "../lib/stillWatching";
import {
  encodeSubtitleAppearance,
  resolveSubtitleAppearance,
  SUBTITLE_APPEARANCE_KEYS,
  type SubtitleAppearanceField,
} from "../lib/subtitleAppearance";
import { readVersionChoice, writeVersionChoice } from "../lib/versionChoice";
import {
  type PlayQueueItem,
  queueNeighbours,
  queueOfClass,
  readPlayerNavState,
  withQueueClasses,
} from "../lib/watchQueue";
import { readWatchPartyIntent } from "../lib/watchTogether";
import { AudioStage } from "../player/AudioStage";
import { KaraokeControls } from "../player/KaraokeControls";
import { MiniPlayer } from "../player/MiniPlayer";
import { type HostedPlayerProps, usePlayerHost, type WatchEntry } from "../player/PlayerHost";
import { PlayerTransport } from "../player/PlayerTransport";
import { PrePlayStage } from "../player/PrePlayStage";
import { SkippedOverlay } from "../player/SkippedOverlay";
import { StillWatchingPrompt } from "../player/StillWatchingPrompt";
import { UpNextCard } from "../player/UpNextCard";
import { useAutoSkip } from "../player/useAutoSkip";
import { useChromeAutoHide } from "../player/useChromeAutoHide";
import { useMediaLoading } from "../player/useMediaLoading";
import { useMediaSession } from "../player/useMediaSession";
import { usePlayer } from "../player/usePlayer";
import { useStillWatching } from "../player/useStillWatching";
import { useUpNext } from "../player/useUpNext";
import { useWatchParty } from "../player/useWatchParty";
import { WatchPartyPanel, WatchPartySyncIndicator } from "../player/WatchPartyPanel";
import { watchPartyDefersUpNext } from "../player/watchPartyStore";

// The player (/watch/:mediaId), ported from the vanilla play() + player view
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
  const { mediaId } = useParams();
  const host = usePlayerHost();
  const skip = new URLSearchParams(location.search).has("t");
  const carried =
    !!location.state && (location.state as { skipPrePlay?: unknown }).skipPrePlay === true;
  const bypass = skip || carried || !mediaId;
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
  const feature = bypass || (current && current.items.length === 0);
  // A pre-play queue (or the wait for one) ends whatever the host plays: the
  // route is about to start another title.
  useLayoutEffect(() => {
    if (host && !feature) host.stop();
  }, [host, feature]);
  if (feature) {
    const entry = { mediaId: mediaId || "", state: location.state, search: location.search };
    return host ? <AttachToHost host={host} entry={entry} /> : <FeaturePlayer entry={entry} />;
  }
  if (current) {
    return (
      <main className="flex min-h-screen flex-col bg-[oklch(8%_0.012_255)]">
        <h1 className="sr-only">{t("prePlayRegion")}</h1>
        <PrePlayStage key={current.mediaId} items={current.items} onDone={finish} />
      </main>
    );
  }
  return (
    <main className="flex min-h-screen items-center justify-center bg-[oklch(8%_0.012_255)]">
      <div role="status">
        <LoaderCircle aria-hidden className="size-10 animate-spin text-white/80" />
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
  const { mediaId, state, search } = entry;
  useLayoutEffect(() => {
    host.attach({ mediaId, state, search });
  }, [host, mediaId, state, search]);
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
  const stageRef = useRef<HTMLDivElement | null>(null);
  const [menuOpen, setMenuOpen] = useState(false);
  // Watch together on the detail page (CORE-1764) arrives with the party started
  // and asks for the invite panel open.
  const [partyOpen, setPartyOpen] = useState(() => readWatchPartyIntent(location.state));
  const [clientPrefs, setClientPrefs] = useState<ClientPref[]>([]);
  const [media, setMedia] = useState<GenericMediaInfo | null>(null);

  // The catalog card / hero (and Up Next) carry the Media title forward in nav
  // state, with the siblings it was shown with as the play queue. The media(id)
  // read below fills in the title for a deep link, plus artwork and credits.
  const navState = readPlayerNavState(entry.state, identity);
  const queueIdentity = navState.queueIdentity;
  const navTitle = navState.title || media?.title || "";
  const startAtSeconds = parseTimestampParam(new URLSearchParams(entry.search).get("t"));
  // Media classes playback-info has reported (this item's and its declared
  // next item's), by Media id (CORE-1490).
  const [learnedClasses, setLearnedClasses] = useState<Record<string, string>>({});
  const navQueue = navState.queue;
  const classedQueue = useMemo(
    () => (navQueue ? withQueueClasses(navQueue, learnedClasses) : undefined),
    [navQueue, learnedClasses],
  );
  const entryClass = classedQueue?.find((item) => item.id === mediaId)?.mediaClass;
  const declaredNextID = classedQueue
    ? (queueNeighbours(queueOfClass(classedQueue, entryClass, mediaId || ""), mediaId || "").next
        ?.id ?? "")
    : undefined;
  // The Version this profile picked for this Media (CORE-1676): a pick made in
  // this player, else the one remembered from an earlier visit.
  const [versionPick, setVersionPick] = useState<{ mediaID: string; id: string } | null>(null);
  const selectedVersion =
    versionPick && versionPick.mediaID === mediaId
      ? versionPick.id
      : readVersionChoice(profileID, mediaId || "");

  const { videoRef, hlsRef, state, retry, restart, switchToHls, adjustment, adjust } = usePlayer(
    mediaId || "",
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
  );
  // A new Version is another file: remember it, then reload at the playhead.
  const selectVersion = useCallback(
    (versionID: string) => {
      if (!mediaId || versionID === selectedVersion) return;
      writeVersionChoice(profileID, mediaId, versionID);
      setVersionPick({ mediaID: mediaId, id: versionID });
      retry();
    },
    [mediaId, profileID, selectedVersion, retry],
  );

  // The queue stays within the playing item's class (CORE-1490): a track
  // started from a shelf that mixes tracks and videos steps only through the
  // tracks. The narrowed queue (with what was learned) is the one carried to
  // the next item, so a dropped entry stays dropped.
  const playingClass = state.info?.mediaClass || entryClass;
  const queue = useMemo(
    () => (classedQueue ? [...queueOfClass(classedQueue, playingClass, mediaId || "")] : undefined),
    [classedQueue, playingClass, mediaId],
  );
  const { previous: previousInQueue, next: nextInQueue } = queueNeighbours(queue, mediaId || "");
  const info = state.info;
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

  const playing = state.phase === "playing";
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
  const isAudio = state.info?.mediaClass === "audio";
  // Before playback-info answers, a queue entry that reports its class already
  // routes a track to the audio stage instead of an empty video frame
  // (CORE-1309). A song whose composed picture is served shows the picture
  // instead (CORE-1656).
  const showAudioStage = state.info ? isAudio && !hasPicture(state.info) : entryClass === "audio";
  const credits = media?.credits ?? [];
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

  const { chromeHidden, reveal } = useChromeAutoHide({
    stageRef,
    videoRef,
    menuOpen: menuOpen || partyOpen,
  });

  // Advance to another Media's watch route, carrying its title and the queue
  // forward. A fresh route remounts the engine cleanly (resume, markers,
  // trickplay, progress beacon); `replace` keeps Back returning to the catalog
  // rather than stepping through every track played. Docked in the mini
  // player, the advance stays on the page the listener is browsing.
  const playItem = useCallback(
    (id: string, title: string) => {
      const state = { title, skipPrePlay: true, ...(queue ? { queue, queueIdentity } : {}) };
      if (docked && onAdvanceDocked) {
        onAdvanceDocked({ mediaId: id, state, search: "" });
        return;
      }
      navigate(`/watch/${encodeURIComponent(id)}`, { replace: true, state });
    },
    [navigate, queue, queueIdentity, docked, onAdvanceDocked],
  );

  // Watch Party (CORE-1677): the party moves every member's player to the
  // title it is on and keeps the <video> on its shared timeline.
  const followParty = useCallback((id: string) => playItem(id, ""), [playItem]);
  const watchParty = useWatchParty({
    videoRef,
    attached: state.phase !== "message" && state.phase !== "terminal",
    mediaId: mediaId || "",
    onFollow: followParty,
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
  const issuedNext = admissibleNext(state.info?.next, queue, playingClass, learnedClasses);
  const playNext = useMemo(() => {
    if (partyLeads) return undefined;
    if (nextInQueue) return () => goToQueueItem(nextInQueue);
    if (issuedNext?.streams) return () => advanceToNext(issuedNext);
    return undefined;
  }, [partyLeads, nextInQueue, issuedNext, goToQueueItem, advanceToNext]);
  // A card queue names its own previous item; without one, a Play all queue
  // that holds this item may (stepping back keeps that queue going).
  const playAllPrevious = useMemo(
    () => (queue || !mediaId ? null : previousInPlayQueue(mediaId)),
    [queue, mediaId],
  );
  const playPrevious = useMemo(() => {
    if (previousInQueue) return () => goToQueueItem(previousInQueue);
    if (playAllPrevious) return () => playItem(playAllPrevious.id, playAllPrevious.title);
    return undefined;
  }, [previousInQueue, playAllPrevious, goToQueueItem, playItem]);

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
    enabled: !isAudio && !partyLeads,
    onContinue: advanceToNext,
  });
  const upNext = useUpNext({
    videoRef,
    next:
      isAudio || partyLeads || stillWatching.armed || stillWatching.prompting ? null : issuedNext,
    onAdvance: advanceToNext,
    onAutoAdvance: (next) => noteAutoAdvance(next.mediaID),
  });
  useEffect(() => {
    const video = videoRef.current;
    if (!isAudio || !playing || !video || !playNext) return;
    video.addEventListener("ended", playNext);
    return () => video.removeEventListener("ended", playNext);
  }, [isAudio, playing, playNext, videoRef]);

  // The card's appearance reveals chrome once; the card itself stays visible
  // and clickable regardless of auto-hide (player-deferred.css).
  const upNextVisible = upNext.visible || stillWatching.prompting;
  useEffect(() => {
    if (upNextVisible) reveal();
  }, [upNextVisible, reveal]);

  // Auto-skip (ADR-0048 Decision 6): composes with, never replaces, Up Next's
  // separate end-of-file auto-advance above.
  const autoSkip = useAutoSkip({
    videoRef,
    markers: state.markers,
    mediaID: mediaId || "",
    prefs: {
      intro: resolvedPrefs.autoSkipIntro,
      recap: resolvedPrefs.autoSkipRecap,
      credits: resolvedPrefs.autoSkipCredits,
    },
    songScale,
  });

  // Browser Media Session (ADR-0084 §43): mirror transport state to the OS and
  // route OS transport intents back through the engine-owned <video> and the
  // same next-item advance the Up Next card uses. A surface, not authority.
  const people = credits
    .filter((credit) => credit.archetype === "person")
    .map((credit) => credit.title)
    .join(", ");
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
  // a failure the mini player cannot offer a way out of, or an advance into a
  // Media that is not audio ends playback instead.
  const audioMedia = state.info ? isAudio : entryClass === "audio";
  useEffect(() => {
    onAudioChange?.(audioMedia);
  }, [audioMedia, onAudioChange]);
  const infoIsCurrent = !!state.info && state.mediaID === mediaId;
  const dockedEnds =
    docked &&
    (state.phase === "terminal" ||
      state.phase === "message" ||
      (infoIsCurrent && state.info?.mediaClass !== "audio"));
  useEffect(() => {
    if (dockedEnds) onStop?.();
  }, [dockedEnds, onStop]);

  // Back to the full player from the mini player: the watch route for the
  // same entry, which re-attaches to the playing engine.
  const openFullPlayer = useCallback(() => {
    const carried = entry.state && typeof entry.state === "object" ? entry.state : {};
    navigate(`/watch/${encodeURIComponent(mediaId)}${entry.search}`, {
      state: { ...carried, skipPrePlay: true },
    });
  }, [navigate, mediaId, entry.state, entry.search]);

  // The media(id) read: title for a deep link, artwork and credits for the
  // audio surface, and the container Entity for the skip-interval row.
  useEffect(() => {
    setMedia(null);
    if (!mediaId) return;
    const controller = new AbortController();
    void fetchMedia(mediaId, controller.signal)
      .then((info) => {
        if (!controller.signal.aborted) setMedia(info);
      })
      .catch(() => {});
    return () => controller.abort();
  }, [mediaId]);

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
    navigate(mediaId ? `/media/${encodeURIComponent(mediaId)}` : "/", { replace: true });
  }

  // Docked, the full player stays mounted but hidden, so its media element
  // (and with it playback) outlives the watch route; only the mini player
  // shows. The fragment keeps the element in the same place in both modes.
  return (
    <>
      <main hidden={docked} className="flex min-h-screen flex-col bg-[oklch(8%_0.012_255)]">
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

        {state.phase === "message" || state.phase === "terminal" ? (
          <div
            id={docked ? undefined : MAIN_CONTENT_ID}
            tabIndex={-1}
            className="flex flex-1 items-center justify-center p-6"
          >
            <div className="flex max-w-md flex-col items-center gap-3 text-center">
              <AlertCircle aria-hidden className="size-7 text-[var(--state-danger)]" />
              {navTitle ? (
                <h2 className="m-0 text-xl font-semibold text-white">{navTitle}</h2>
              ) : null}
              <p className="m-0 text-[15px] leading-relaxed text-white/75" role="alert">
                {state.message}
              </p>
              {state.detail ? (
                <p className="m-0 text-sm text-white/55">
                  {t("playbackServerReason", { reason: state.detail })}
                </p>
              ) : null}
              <div className="mt-3">
                {state.terminal || state.action === "back" ? (
                  <Button onClick={back}>{t("backToBrowse")}</Button>
                ) : (
                  <Button onClick={retry}>{t("tryAgain")}</Button>
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
            <video ref={videoRef} playsInline controls={false} poster={videoPoster || undefined} />
            {showAudioStage && (
              <AudioStage
                title={navTitle}
                artworkURL={artworkURL}
                credits={credits}
                queuePosition={queuePosition}
              />
            )}
            {showSpinner && (
              <div
                className="pointer-events-none absolute inset-0 flex items-center justify-center"
                role="status"
              >
                <LoaderCircle aria-hidden className="size-10 animate-spin text-white/80" />
                <span className="sr-only">{t("playerLoading")}</span>
              </div>
            )}
            {state.waitingForNetwork ? (
              <p
                className="pointer-events-none absolute left-1/2 top-4 -translate-x-1/2 rounded-md bg-black/70 px-3 py-1 text-sm text-white"
                role="status"
              >
                {t("playerWaitingForNetwork")}
              </p>
            ) : (
              state.recovering && (
                <p
                  className="pointer-events-none absolute left-1/2 top-4 -translate-x-1/2 rounded-md bg-black/70 px-3 py-1 text-sm text-white"
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
            {playing && !docked && (
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
                onStop={back}
                onRemoteStop={stopRemotely}
                onRemoteRestart={restart}
                switchToHls={switchToHls}
                onSelectVersion={selectVersion}
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
      {docked && (
        <MiniPlayer
          videoRef={videoRef}
          title={navTitle}
          artist={people}
          artworkURL={artworkURL}
          mediaID={mediaId}
          songScale={songScale}
          onNext={playNext}
          onPrevious={playPrevious}
          onOpen={openFullPlayer}
          onStop={() => onStop?.()}
          onRestart={restart}
        />
      )}
    </>
  );
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
    `${import.meta.env.BASE_URL}watch/${encodeURIComponent(mediaId)}`,
    window.location.origin,
  );
  url.searchParams.set("t", String(Math.max(0, Math.floor(seconds))));
  return url.href;
}
