import {
  ExternalLink,
  MonitorUp,
  Pause,
  Play,
  QrCode as QrIcon,
  RefreshCw,
  SkipForward,
} from "lucide-react";
import { useState } from "react";
import { Link, useHref } from "react-router";
import { QrCode } from "@/components/beamfall/QrCode";
import { Button } from "@/components/ui/button";
import {
  type PartySession,
  partyAdvance,
  partyPlayback,
  partyRotateCode,
  partyStart,
} from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { PartyPeople, PartyRotation } from "../party/HostLive";
import { anchorTargetMS, stagePath, stageWindowName } from "../party/partySync";
import { type RunPartyOp, usePartyHost } from "../party/usePartyHost";
import { KaraokeSongbook } from "./KaraokeSongbook";
import "../party/party.css";
import "./karaoke-library.css";

// The karaoke Library's Songbook pivot (CORE-3362): the songbook beside the
// party rail, so a host finds songs, adds them for singers and runs the
// rotation on one page. The full host page (/party) keeps the stage screens,
// tempo and key, and the session settings.
export function KaraokeLibraryView({ libraryID }: { libraryID: string }) {
  const t = useT();
  const { session, loadError, notice, serverNow, run } = usePartyHost();
  return (
    <div className="bf-karaoke">
      <KaraokeSongbook libraryID={libraryID} session={session} run={run} />
      <aside className="bf-karaoke-rail" aria-label={t("karaokeRailLabel")}>
        {notice && (
          <p
            role={notice.tone === "error" ? "alert" : "status"}
            className="bf-party-notice"
            data-tone={notice.tone}
          >
            {notice.text}
          </p>
        )}
        {loadError && session === undefined ? (
          <p role="alert" className="bf-party-notice" data-tone="error">
            {t("partyHostLoadError")}
          </p>
        ) : session === undefined ? null : session === null ? (
          <PartyStart libraryID={libraryID} run={run} />
        ) : (
          <>
            <PartyJoin session={session} run={run} />
            <OnStage session={session} run={run} serverNow={serverNow} />
            <PartyRotation session={session} run={run} />
            <PartyPeople session={session} run={run} />
            <Link to="/party" className="bf-party-link bf-karaoke-full">
              {t("karaokeFullControls")}
            </Link>
          </>
        )}
      </aside>
    </div>
  );
}

function PartyStart({ libraryID, run }: { libraryID: string; run: RunPartyOp }) {
  const t = useT();
  const [starting, setStarting] = useState(false);
  return (
    <section className="bf-party-panel flex flex-col gap-3">
      <h2 className="bf-party-h2">{t("partyHostTitle")}</h2>
      <p className="bf-party-muted">{t("karaokePartyLead")}</p>
      <div className="flex flex-wrap items-center gap-3">
        <Button
          disabled={starting}
          onClick={async () => {
            setStarting(true);
            await run(() =>
              partyStart({ libraryIDs: [libraryID], fairness: "round-robin", autoAdd: false }),
            );
            setStarting(false);
          }}
        >
          <Play aria-hidden />
          {t("partyHostStart")}
        </Button>
        <Link to="/party" className="bf-party-link">
          {t("karaokePartyOptions")}
        </Link>
      </div>
    </section>
  );
}

function PartyJoin({ session, run }: { session: PartySession; run: RunPartyOp }) {
  const t = useT();
  const [showQR, setShowQR] = useState(false);
  const [blocked, setBlocked] = useState(false);
  const stageHref = useHref(stagePath(session.id));
  const joinURL = `${window.location.origin}${session.joinPath}`;
  const popOut = () => {
    const opened = window.open(
      stageHref,
      stageWindowName(session.id),
      "popup,width=1280,height=720",
    );
    setBlocked(!opened);
  };
  return (
    <section className="bf-party-panel bf-karaoke-join flex flex-col gap-3">
      <div className="flex items-center justify-between gap-3">
        <span className="bf-karaoke-live">{t("karaokeLive")}</span>
        <Button variant="outline" size="sm" onClick={popOut}>
          <MonitorUp aria-hidden />
          {t("partyHostPopOut")}
        </Button>
      </div>
      <div className="flex flex-col gap-1">
        <span className="bf-party-label">{t("partyHostJoinCode")}</span>
        <strong className="bf-party-code bf-karaoke-code">{session.joinCode}</strong>
        {!session.joinOpen && <span className="bf-party-muted">{t("partyHostJoinClosed")}</span>}
      </div>
      <div className="flex flex-wrap gap-2">
        <Button
          variant="outline"
          size="sm"
          aria-expanded={showQR}
          onClick={() => setShowQR((value) => !value)}
        >
          <QrIcon aria-hidden />
          {showQR ? t("partyHostHideQR") : t("partyHostShowQR")}
        </Button>
        <Button variant="ghost" size="sm" onClick={() => void run(partyRotateCode)}>
          <RefreshCw aria-hidden />
          {t("partyHostRotateCode")}
        </Button>
      </div>
      {showQR && (
        <div className="bf-party-qr">
          <QrCode value={joinURL} label={t("partyHostQRLabel", { code: session.joinCode })} />
        </div>
      )}
      {blocked && (
        <p role="alert" className="bf-party-notice" data-tone="error">
          {t("partyHostPopupBlocked")}{" "}
          <a href={stageHref} target="_blank" rel="noopener" className="bf-party-link">
            {t("partyHostOpenStage")}
            <ExternalLink className="ml-1 inline size-3.5" aria-hidden />
          </a>
        </p>
      )}
    </section>
  );
}

function OnStage({
  session,
  run,
  serverNow,
}: {
  session: PartySession;
  run: RunPartyOp;
  serverNow: () => number;
}) {
  const t = useT();
  const { current, playback } = session;
  if (!current) {
    return (
      <section className="bf-party-panel flex flex-col gap-3">
        <h2 className="bf-party-h2">{t("partyHostNowSinging")}</h2>
        <p className="bf-party-muted">
          {session.entries.length > 0 ? t("partyHostIdleReady") : t("partyHostIdleEmpty")}
        </p>
        {session.entries.length > 0 && (
          <div>
            <Button onClick={() => void run(() => partyAdvance())}>
              <Play aria-hidden />
              {t("partyHostStartFirst")}
            </Button>
          </div>
        )}
      </section>
    );
  }
  const live = playback && playback.entryID === current.id ? playback : null;
  const playing = live?.state === "playing";
  const toggle = () =>
    run(() =>
      partyPlayback({
        entryID: current.id,
        state: playing ? "paused" : "playing",
        positionMS: Math.round(live ? anchorTargetMS(live, serverNow()) : 0),
        tempo: live?.tempo ?? 1,
        pitchSemitones: live?.pitchSemitones ?? 0,
      }),
    );
  return (
    <section className="bf-party-panel bf-karaoke-stage flex flex-col gap-3">
      <h2 className="bf-party-label">{t("partyHostNowSinging")}</h2>
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="bf-karaoke-singer">{current.participantName}</span>
        <span className="bf-karaoke-song">
          {current.title}
          {current.subtitle && <span className="bf-party-muted"> · {current.subtitle}</span>}
        </span>
      </div>
      <div className="flex flex-wrap gap-2">
        <Button variant="outline" size="sm" onClick={() => void toggle()}>
          {playing ? <Pause aria-hidden /> : <Play aria-hidden />}
          {playing ? t("partyHostPause") : t("partyHostPlay")}
        </Button>
        <Button size="sm" onClick={() => void run(() => partyAdvance(current.id))}>
          <SkipForward aria-hidden />
          {t("karaokeNextSinger")}
        </Button>
      </div>
    </section>
  );
}
