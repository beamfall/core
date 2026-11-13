import { AlertCircle, LoaderCircle } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useLocation, useNavigate, useParams } from "react-router";
import { Button } from "@/components/ui/button";
import { displayString } from "@/lib/display";
import { fetchClientPrefs, fetchMedia, setClientPref } from "@/lib/graphql";
import type { ClientPref, GenericMediaInfo } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useSession, useViewerIdentity } from "@/lib/session";
import type { PlaybackNext } from "../lib/playback";
import { previousInPlayQueue } from "../lib/playQueue";
import {
  resolvePreferences,
  type SkipInterval,
  skipIntervalOverrideKey,
  skipIntervalOverrideValue,
} from "../lib/preferences";
import { type PlayQueueItem, queueNeighbours, readPlayerNavState } from "../lib/watchQueue";
import { AudioStage } from "../player/AudioStage";
import { PlayerTransport } from "../player/PlayerTransport";
import { SkippedOverlay } from "../player/SkippedOverlay";
import { UpNextCard } from "../player/UpNextCard";
import { useAutoSkip } from "../player/useAutoSkip";
import { useChromeAutoHide } from "../player/useChromeAutoHide";
import { useMediaLoading } from "../player/useMediaLoading";
import { useMediaSession } from "../player/useMediaSession";
import { usePlayer } from "../player/usePlayer";
import { useUpNext } from "../player/useUpNext";

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
export function PlayerPage() {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const navigate = useNavigate();
  const location = useLocation();
  const { mediaId } = useParams();
  const { revealed } = useSession();
  const identity = useViewerIdentity();
  const stageRef = useRef<HTMLDivElement | null>(null);
  const [menuOpen, setMenuOpen] = useState(false);
  const [clientPrefs, setClientPrefs] = useState<ClientPref[]>([]);
  const [media, setMedia] = useState<GenericMediaInfo | null>(null);

  // The catalog card / hero (and Up Next) carry the Media title forward in nav
  // state, with the siblings it was shown with as the play queue. The media(id)
  // read below fills in the title for a deep link, plus artwork and credits.
  const navState = readPlayerNavState(location.state, identity);
  const queue = navState.queue;
  const queueIdentity = navState.queueIdentity;
  const navTitle = navState.title || media?.title || "";
  const startAtSeconds = parseTimestampParam(new URLSearchParams(location.search).get("t"));
  const { previous: previousInQueue, next: nextInQueue } = queueNeighbours(queue, mediaId || "");

  const { videoRef, hlsRef, state, retry, switchToHls } = usePlayer(
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
      sourceEndsEarly: t("playbackSourceEndsEarly"),
    },
    startAtSeconds,
    // "" when a card queue drives playback but has nothing after this item, so
    // an earlier Play all queue is left rather than resumed.
    queue ? (nextInQueue?.id ?? "") : undefined,
  );

  const playing = state.phase === "playing";
  const showSpinner = useMediaLoading(videoRef, state.phase === "loading" || playing);
  const isAudio = state.info?.mediaClass === "audio";
  const credits = media?.credits ?? [];
  // The per-collection skip interval belongs to the item's container Entity
  // (the show, the album …), named by its title; an item with none has no row.
  const collection = credits.find((credit) => credit.archetype === "container");
  const collectionID = collection?.entityID || "";
  const resolvedPrefs = resolvePreferences(clientPrefs, collectionID);
  const showSkipInterval = collectionID ? skipIntervalOverrideValue(clientPrefs, collectionID) : "";
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
    menuOpen,
  });

  // Advance to another Media's watch route, carrying its title and the queue
  // forward. A fresh route remounts the engine cleanly (resume, markers,
  // trickplay, progress beacon); `replace` keeps Back returning to the catalog
  // rather than stepping through every track played.
  const playItem = useCallback(
    (id: string, title: string) => {
      navigate(`/watch/${encodeURIComponent(id)}`, {
        replace: true,
        state: { title, ...(queue ? { queue, queueIdentity } : {}) },
      });
    },
    [navigate, queue, queueIdentity],
  );

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
  const issuedNext = state.info?.next;
  const playNext = useMemo(() => {
    if (nextInQueue) return () => goToQueueItem(nextInQueue);
    if (issuedNext?.streams) return () => advanceToNext(issuedNext);
    return undefined;
  }, [nextInQueue, issuedNext, goToQueueItem, advanceToNext]);
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
  const upNext = useUpNext({
    videoRef,
    next: isAudio ? null : state.info?.next,
    onAdvance: advanceToNext,
  });
  useEffect(() => {
    const video = videoRef.current;
    if (!isAudio || !playing || !video || !playNext) return;
    video.addEventListener("ended", playNext);
    return () => video.removeEventListener("ended", playNext);
  }, [isAudio, playing, playNext, videoRef]);

  // The card's appearance reveals chrome once; the card itself stays visible
  // and clickable regardless of auto-hide (player-deferred.css).
  const upNextVisible = upNext.visible;
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
  });

  // A relock during playback drops restricted content from every view at once
  // (ADR-0004). If the session relocks while this restricted Media is playing,
  // pause the stream so nothing keeps rendering behind the terminal block.
  useEffect(() => {
    if (!revealed && videoRef.current) {
      videoRef.current.pause();
    }
  }, [revealed, videoRef]);

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

  useEffect(() => {
    let current = true;
    void fetchClientPrefs()
      .then((prefs) => {
        if (current) setClientPrefs(prefs);
      })
      .catch(() => {});
    return () => {
      current = false;
    };
  }, [mediaId]);

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
    navigate("/");
  }

  return (
    <main className="flex min-h-screen flex-col bg-[oklch(8%_0.012_255)]">
      <h1 className="sr-only">{navTitle || t("brand")}</h1>
      <div className="bf-player-bar">
        <Button variant="outline" size="sm" onClick={back}>
          {t("back")}
        </Button>
      </div>

      {state.phase === "message" || state.phase === "terminal" ? (
        <div className="flex flex-1 items-center justify-center p-6">
          <div className="flex max-w-md flex-col items-center gap-3 text-center">
            <AlertCircle aria-hidden className="size-7 text-[var(--state-danger)]" />
            {navTitle ? <h2 className="m-0 text-xl font-semibold text-white">{navTitle}</h2> : null}
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
          className={"bf-player-stage" + (playing && chromeHidden ? " is-chrome-hidden" : "")}
        >
          {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
          <video ref={videoRef} playsInline controls={false} poster={videoPoster || undefined} />
          {isAudio && (
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
          {playing && (
            <PlayerTransport
              videoRef={videoRef}
              hlsRef={hlsRef}
              state={state}
              mediaTitle={navTitle}
              mediaID={mediaId || ""}
              stageRef={stageRef}
              skipInterval={Number(resolvedPrefs.skipInterval)}
              showSkipInterval={showSkipInterval}
              showSkipIntervalLabel={showSkipIntervalLabel}
              showSkipIntervalCollection={collectionTitle}
              onShowSkipIntervalChange={setShowSkipInterval}
              onShare={shareAt}
              onPreviousInQueue={playPrevious}
              onNextInQueue={playNext}
              switchToHls={switchToHls}
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
  );
}

function parseTimestampParam(raw: string | null): number {
  if (!raw) return 0;
  const seconds = Number(raw);
  return Number.isFinite(seconds) && seconds > 0 ? Math.floor(seconds) : 0;
}

export function buildTimestampedLink(mediaId: string, seconds: number): string {
  const url = new URL(
    `${import.meta.env.BASE_URL}watch/${encodeURIComponent(mediaId)}`,
    window.location.origin,
  );
  url.searchParams.set("t", String(Math.max(0, Math.floor(seconds))));
  return url.href;
}
