import { useCallback, useEffect, useRef, useState } from "react";
import { useLocation, useNavigate, useParams } from "react-router";
import { Button } from "@/components/ui/button";
import { fetchClientPrefs, setClientPref } from "@/lib/graphql";
import type { ClientPref } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import type { PlaybackNext } from "../lib/playback";
import {
  resolvePreferences,
  type SkipInterval,
  skipIntervalOverrideKey,
  skipIntervalOverrideValue,
} from "../lib/preferences";
import { PlayerTransport } from "../player/PlayerTransport";
import { SkippedOverlay } from "../player/SkippedOverlay";
import { UpNextCard } from "../player/UpNextCard";
import { useAutoSkip } from "../player/useAutoSkip";
import { useChromeAutoHide } from "../player/useChromeAutoHide";
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
  const navigate = useNavigate();
  const location = useLocation();
  const { mediaId } = useParams();
  const { revealed } = useSession();
  const stageRef = useRef<HTMLDivElement | null>(null);
  const [menuOpen, setMenuOpen] = useState(false);
  const [clientPrefs, setClientPrefs] = useState<ClientPref[]>([]);

  // The catalog card / hero (and Up Next) carry the Media title forward in nav
  // state — the playback-info wire does not report the current Media's title, so
  // this is the only honest source for the info overlay heading. Absent (a deep
  // link to /watch/:id) it falls back to a generic label.
  const navTitle = (location.state as { title?: string } | null)?.title || "";
  const startAtSeconds = parseTimestampParam(new URLSearchParams(location.search).get("t"));

  const { videoRef, hlsRef, state, retry } = usePlayer(
    mediaId || "",
    {
      couldNotReachServer: t("couldNotReachServer"),
      playbackUnavailable: t("playbackUnavailable"),
      transcodedPlaybackUnsupported: t("transcodedPlaybackUnsupported"),
      playbackUnsupportedCodec: t("playbackUnsupportedCodec"),
      playbackFailed: t("playbackFailed"),
      terminalUnavailable: t("playerTerminalUnavailable"),
    },
    startAtSeconds,
  );

  const playing = state.phase === "playing";
  const resolvedPrefs = resolvePreferences(clientPrefs, mediaId || "");
  const showSkipInterval = mediaId ? skipIntervalOverrideValue(clientPrefs, mediaId) : "";

  const { chromeHidden, reveal } = useChromeAutoHide({
    stageRef,
    videoRef,
    menuOpen,
  });

  // Up Next auto-advance: navigate to the issued next Media's watch route,
  // carrying its title forward so the info overlay can name it. A fresh route
  // remounts the engine cleanly (resume, markers, trickplay, progress beacon).
  const advanceToNext = useCallback(
    (next: PlaybackNext) => {
      navigate(`/watch/${encodeURIComponent(next.mediaID)}`, {
        state: { title: next.title || "" },
      });
    },
    [navigate],
  );

  const upNext = useUpNext({
    videoRef,
    next: state.info?.next,
    onAdvance: advanceToNext,
  });

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
  useMediaSession({
    videoRef,
    active: playing,
    title: navTitle,
    next: state.info?.next,
    onNext: advanceToNext,
  });

  // A relock during playback drops restricted content from every view at once
  // (ADR-0004). If the session relocks while this restricted Media is playing,
  // pause the stream so nothing keeps rendering behind the terminal block.
  useEffect(() => {
    if (!revealed && videoRef.current) {
      videoRef.current.pause();
    }
  }, [revealed, videoRef]);

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
      if (!mediaId) return;
      const key = skipIntervalOverrideKey(mediaId);
      setClientPrefs((prefs) => [
        ...prefs.filter((pref) => pref.key !== key),
        ...(value ? [{ key, value }] : []),
      ]);
      void setClientPref(key, value).catch(() => {});
    },
    [mediaId],
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
      <div className="bf-player-bar">
        <Button variant="outline" size="sm" onClick={back}>
          {t("back")}
        </Button>
      </div>

      {state.phase === "message" || state.phase === "terminal" ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-4 p-6 text-center">
          <p className="text-[var(--red-500)]" role="alert">
            {state.message}
          </p>
          {state.terminal ? (
            <Button onClick={back}>{t("backToBrowse")}</Button>
          ) : (
            <Button onClick={retry}>{t("tryAgain")}</Button>
          )}
        </div>
      ) : (
        <div
          ref={stageRef}
          className={"bf-player-stage" + (playing && chromeHidden ? " is-chrome-hidden" : "")}
        >
          {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
          <video ref={videoRef} playsInline controls={false} />
          {state.recovering && (
            <p
              className="pointer-events-none absolute left-1/2 top-4 -translate-x-1/2 rounded-md bg-black/70 px-3 py-1 text-sm text-white"
              role="status"
            >
              {t("reconnecting")}
            </p>
          )}
          {playing && (
            <PlayerTransport
              videoRef={videoRef}
              hlsRef={hlsRef}
              state={state}
              mediaTitle={navTitle}
              stageRef={stageRef}
              skipInterval={Number(resolvedPrefs.skipInterval)}
              showSkipInterval={showSkipInterval}
              onShowSkipIntervalChange={setShowSkipInterval}
              onShare={shareAt}
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
