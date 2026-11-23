import { useCallback, useEffect, useRef, useState } from "react";
import { useParams, useSearchParams } from "react-router";
import { QrCode } from "@/components/beamfall/QrCode";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import { localClockMS } from "@/lib/clockEstimate";
import {
  calibrateWatchPartyClock,
  fetchPartyStage,
  type PartyEntry,
  type PartyPlayback,
  type PartyScreenKind,
  type PartyStage,
  partyAdvance,
  partyScreenRegister,
  partyScreenReport,
} from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { sameAdjustment, songSecondsPerStreamSecond } from "../lib/playback";
import { anchorTargetMS, POLL_MS, REPORT_MS, SEEK_DRIFT_MS } from "../party/partySync";
import { useVisiblePoll } from "../party/useVisiblePoll";
import { usePlayer } from "../player/usePlayer";
import "../party/party.css";

const FOLLOW_MS = 250;

/** Whether a stage poll failed because the server has no such live session. */
function isPartyGone(error: unknown): boolean {
  return error instanceof ApiError && /party session not found/i.test(error.message);
}

function storageKey(sessionID: string, kind: PartyScreenKind): string {
  return `beamfall.partyScreen.${kind}.${sessionID}`;
}

function readScreenID(sessionID: string, kind: PartyScreenKind): string | null {
  try {
    return window.sessionStorage.getItem(storageKey(sessionID, kind));
  } catch {
    return null;
  }
}

function writeScreenID(sessionID: string, kind: PartyScreenKind, screenID: string | null) {
  try {
    if (screenID) window.sessionStorage.setItem(storageKey(sessionID, kind), screenID);
    else window.sessionStorage.removeItem(storageKey(sessionID, kind));
  } catch {
    // Without storage a reload registers a fresh screen; the old one drops on its own.
  }
}

/**
 * A party stage screen (CORE-2946 pop-out, CORE-2947 joined): registers as a
 * screen of the session, follows the shared anchor with the ordinary player,
 * reports its offset, and shows the join code and up-next between songs.
 */
export function PartyStagePage() {
  const t = useT();
  const { sessionID = "" } = useParams();
  const [search] = useSearchParams();
  const kind: PartyScreenKind = search.get("joined") === "1" ? "joined" : "popout";
  const [screenID, setScreenID] = useState<string | null>(() => readScreenID(sessionID, kind));
  const [registerFailed, setRegisterFailed] = useState(false);
  const [stage, setStage] = useState<PartyStage | undefined>(undefined);
  const [stageFailed, setStageFailed] = useState(false);
  // serverClock − localClock: the calibrated estimate, else the poll's serverNowMS.
  const calibratedRef = useRef<number | null>(null);
  const fallbackRef = useRef(0);
  const rttRef = useRef(0);
  // Song ms this screen is ahead (+) or behind (−) the anchor; 0 while idle.
  const driftRef = useRef(0);

  const serverNow = useCallback(
    () => localClockMS() + (calibratedRef.current ?? fallbackRef.current),
    [],
  );

  useEffect(() => {
    if (screenID || !sessionID) return;
    let live = true;
    partyScreenRegister({
      sessionID,
      name: kind === "popout" ? t("partyHostStagePopoutName") : t("partyHostStageJoinedName"),
      kind,
      carrier: "local",
    })
      .then((screen) => {
        if (!live) return;
        writeScreenID(sessionID, kind, screen.id);
        setScreenID(screen.id);
        setRegisterFailed(false);
      })
      .catch(() => {
        if (live) setRegisterFailed(true);
      });
    return () => {
      live = false;
    };
  }, [screenID, sessionID, kind, t]);

  useEffect(() => {
    calibrateWatchPartyClock()
      .then((estimate) => {
        calibratedRef.current = estimate.offsetMS;
        rttRef.current = estimate.roundTripMS;
      })
      .catch(() => {});
  }, []);

  useVisiblePoll(
    async (signal) => {
      const sentAt = localClockMS();
      try {
        if (!screenID) return;
        const next = await fetchPartyStage(sessionID, screenID, signal);
        const receivedAt = localClockMS();
        fallbackRef.current = next.serverNowMS - (sentAt + receivedAt) / 2;
        if (calibratedRef.current === null) rttRef.current = receivedAt - sentAt;
        setStage(next);
        setStageFailed(false);
      } catch (error) {
        if (signal.aborted) throw error;
        setStageFailed(true);
        // The session ended or never existed: drop the stage so its player
        // unmounts. Any other failure (a network blip) keeps playing.
        if (isPartyGone(error)) setStage(undefined);
        throw error;
      }
    },
    POLL_MS,
    // ADR-0240 D10: no stage read before this screen's registration completes.
    sessionID !== "" && screenID !== null,
  );

  useEffect(() => {
    if (!screenID) return;
    const report = () => {
      partyScreenReport(screenID, driftRef.current, rttRef.current)
        .then((known) => {
          // The server no longer knows this screen (dropped or expired): register again.
          if (!known) {
            writeScreenID(sessionID, kind, null);
            setScreenID(null);
          }
        })
        .catch(() => {});
    };
    const first = window.setTimeout(report, POLL_MS);
    const timer = window.setInterval(report, REPORT_MS);
    return () => {
      window.clearTimeout(first);
      window.clearInterval(timer);
    };
  }, [screenID, sessionID, kind]);

  const current = stage?.current ?? null;

  return (
    <main className="bf-party-stage">
      <h1 className="sr-only">{t("partyHostStageTitle")}</h1>
      {registerFailed && (
        <p role="alert" className="bf-party-stage-alert">
          {t("partyHostStageRegisterError")}
        </p>
      )}
      {stage === undefined ? (
        stageFailed ? (
          <p role="alert" className="bf-party-stage-idle">
            {t("partyHostStageUnavailable")}
          </p>
        ) : null
      ) : current ? (
        <StagePlayer
          key={current.id}
          entry={current}
          playback={stage.playback}
          serverNow={serverNow}
          driftRef={driftRef}
        />
      ) : (
        <StageIdle stage={stage} driftRef={driftRef} />
      )}
      {stage && current && stage.ticker && stage.upNext.length > 0 && (
        <div className="bf-party-ticker" role="region" aria-label={t("partyHostUpNext")}>
          <span className="bf-party-ticker-label">{t("partyHostUpNext")}</span>
          <span className="truncate">
            {stage.upNext
              .map((entry) =>
                t("partyHostTickerItem", { title: entry.title, name: entry.participantName }),
              )
              .join("  ·  ")}
          </span>
        </div>
      )}
    </main>
  );
}

function StageIdle({
  stage,
  driftRef,
}: {
  stage: PartyStage;
  driftRef: React.MutableRefObject<number>;
}) {
  const t = useT();
  driftRef.current = 0;
  const joinURL = `${window.location.origin}${stage.joinPath}`;
  return (
    <div className="bf-party-stage-idle">
      <div className="bf-party-stage-join">
        <QrCode
          value={joinURL}
          label={t("partyHostQRLabel", { code: stage.joinCode })}
          className="bf-party-stage-qr"
        />
        <div className="flex flex-col gap-2">
          <p className="bf-party-stage-kicker">{t("partyHostStageJoinPrompt")}</p>
          <p className="bf-party-stage-code">{stage.joinCode}</p>
        </div>
      </div>
      {stage.upNext.length > 0 && (
        <div className="flex flex-col gap-2">
          <p className="bf-party-stage-kicker">{t("partyHostUpNext")}</p>
          <ol className="flex flex-col gap-1">
            {stage.upNext.map((entry) => (
              <li key={entry.id} className="bf-party-stage-next">
                {t("partyHostTickerItem", { title: entry.title, name: entry.participantName })}
              </li>
            ))}
          </ol>
        </div>
      )}
    </div>
  );
}

function StagePlayer({
  entry,
  playback,
  serverNow,
  driftRef,
}: {
  entry: PartyEntry;
  playback: PartyPlayback | null;
  serverNow: () => number;
  driftRef: React.MutableRefObject<number>;
}) {
  const t = useT();
  const anchor = playback && playback.entryID === entry.id ? playback : null;
  // Memoised per entry (the component is keyed by it): a number, never
  // undefined, so the player never resumes from saved progress.
  const [startAtSeconds] = useState(() =>
    anchor ? anchorTargetMS(anchor, serverNow()) / 1000 : 0,
  );
  const { videoRef, state, retry, adjustment, adjust } = usePlayer(
    entry.mediaID,
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
  );
  const [needsGesture, setNeedsGesture] = useState(false);
  const advancedRef = useRef(false);
  const anchorRef = useRef(anchor);
  anchorRef.current = anchor;
  const stateRef = useRef(state);
  stateRef.current = state;

  const tempo = anchor?.tempo ?? 1;
  const pitch = anchor?.pitchSemitones ?? 0;
  useEffect(() => {
    if (state.phase !== "playing") return;
    const want = { pitchShiftSemitones: pitch, tempo };
    if (!sameAdjustment(adjustment, want)) adjust(want);
  }, [state.phase, adjustment, adjust, pitch, tempo]);

  useEffect(() => {
    const follow = () => {
      const video = videoRef.current;
      const current = anchorRef.current;
      const player = stateRef.current;
      if (!video || !current || player.phase !== "playing" || player.mediaID !== entry.mediaID) {
        driftRef.current = 0;
        return;
      }
      const rate = songSecondsPerStreamSecond(player.info);
      const targetMS = anchorTargetMS(current, serverNow());
      const actualMS = video.currentTime * 1000 * rate;
      const drift = actualMS - targetMS;
      driftRef.current = drift;
      if (!video.ended && !video.seeking && Math.abs(drift) > SEEK_DRIFT_MS) {
        video.currentTime = targetMS / 1000 / rate;
      }
      if (current.state === "playing" && video.paused && !video.ended) {
        video.play().then(
          () => setNeedsGesture(false),
          () => setNeedsGesture(true),
        );
      } else if (current.state === "paused" && !video.paused) {
        video.pause();
      }
    };
    const timer = window.setInterval(follow, FOLLOW_MS);
    return () => window.clearInterval(timer);
  }, [entry.mediaID, serverNow, videoRef, driftRef]);

  const onEnded = () => {
    if (advancedRef.current) return;
    advancedRef.current = true;
    void partyAdvance(entry.id).catch(() => {
      advancedRef.current = false;
    });
  };

  const failed = state.phase === "message" || state.phase === "terminal";
  return (
    <div className="bf-party-stage-player">
      <video ref={videoRef} className="bf-party-stage-video" playsInline onEnded={onEnded} />
      <div className="bf-party-stage-caption">
        <p className="bf-party-stage-title">{entry.title}</p>
        {entry.subtitle !== "" && <p className="bf-party-stage-singer">{entry.subtitle}</p>}
        <p className="bf-party-stage-singer">
          {t("partyHostSungBy", { name: entry.participantName })}
        </p>
      </div>
      {needsGesture && !failed && (
        <div className="bf-party-stage-overlay">
          <Button
            size="lg"
            onClick={() => {
              void videoRef.current?.play().then(
                () => setNeedsGesture(false),
                () => {},
              );
            }}
          >
            {t("partyHostStageStartSound")}
          </Button>
        </div>
      )}
      {failed && (
        <div className="bf-party-stage-overlay" role="alert">
          <p>{state.message || t("playbackFailed")}</p>
          {state.phase === "message" && (
            <Button variant="secondary" onClick={() => retry()}>
              {t("partyHostStageRetry")}
            </Button>
          )}
        </div>
      )}
    </div>
  );
}
