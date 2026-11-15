import {
  AlertCircle,
  CheckCircle2,
  Pause,
  Play,
  RefreshCw,
  SkipBack,
  SkipForward,
  Square,
  Volume2,
  VolumeX,
} from "lucide-react";
import { type FormEvent, useCallback, useEffect, useId, useState } from "react";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { controlSession, fetchProfileSessions } from "@/lib/graphql";
import type { ControlSessionVars, ProfileSessionInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { ViewerActions } from "../components/ViewerActions";
import { formatClockTime } from "../lib/transport";
import "../remote/remote.css";

type Command = Omit<ControlSessionVars, "sessionID">;
type Notice = { tone: "ok" | "error"; text: string };

/**
 * The cross-device remote (CORE-1678): the Profile's other active sessions,
 * each with transport controls, volume and a message to show on that device.
 */
export function RemotePage() {
  const t = useT();
  const [sessions, setSessions] = useState<ProfileSessionInfo[] | null>(null);
  const [loadError, setLoadError] = useState(false);
  const [notice, setNotice] = useState<Notice | null>(null);

  const refresh = useCallback(() => {
    setLoadError(false);
    fetchProfileSessions()
      .then((all) => setSessions(all.filter((session) => !session.current)))
      .catch(() => setLoadError(true));
  }, []);

  useEffect(refresh, [refresh]);

  const send = useCallback(
    async (sessionID: string, command: Command): Promise<boolean> => {
      try {
        await controlSession({ sessionID, ...command });
        setNotice({ tone: "ok", text: t("remoteSent") });
        if (command.command === "stop") refresh();
        return true;
      } catch {
        setNotice({ tone: "error", text: t("remoteSendError") });
        return false;
      }
    },
    [refresh, t],
  );

  return (
    <AppShell actions={<ViewerActions />}>
      <div className="mydata mx-auto flex max-w-4xl flex-col gap-6">
        <header className="mydata-hero flex flex-wrap items-end justify-between gap-4">
          <div className="flex min-w-0 flex-col gap-1.5">
            <h1 className="bf-page-title">{t("remoteTitle")}</h1>
            <p className="bf-lead max-w-2xl">{t("remoteLead")}</p>
          </div>
          <Button variant="outline" size="sm" onClick={refresh}>
            <RefreshCw className="size-3.5" aria-hidden />
            {t("remoteRefresh")}
          </Button>
        </header>

        {notice && <RemoteNotice notice={notice} />}

        <section className="mydata-panel" aria-label={t("remoteTitle")}>
          {loadError ? (
            <RemoteNotice notice={{ tone: "error", text: t("remoteLoadError") }} />
          ) : sessions === null ? null : sessions.length === 0 ? (
            <p className="mydata-muted">{t("remoteEmpty")}</p>
          ) : (
            <ul className="mydata-list">
              {sessions.map((session) => (
                <RemoteDevice key={session.sessionID} session={session} send={send} />
              ))}
            </ul>
          )}
        </section>
      </div>
    </AppShell>
  );
}

function RemoteNotice({ notice }: { notice: Notice }) {
  const Icon = notice.tone === "ok" ? CheckCircle2 : AlertCircle;
  return (
    <p
      className="mydata-notice"
      data-tone={notice.tone}
      role={notice.tone === "error" ? "alert" : "status"}
    >
      <Icon className="size-4 shrink-0" aria-hidden />
      <span>{notice.text}</span>
    </p>
  );
}

function RemoteDevice({
  session,
  send,
}: {
  session: ProfileSessionInfo;
  send: (sessionID: string, command: Command) => Promise<boolean>;
}) {
  const t = useT();
  const volumeID = useId();
  const [volume, setVolume] = useState(100);
  const [text, setText] = useState("");
  const device = session.deviceName || t("remoteDeviceUnnamed");
  const run = (command: Command) => void send(session.sessionID, command);
  const commitVolume = () => run({ command: "set-volume", volume });

  async function onMessage(event: FormEvent) {
    event.preventDefault();
    const message = text.trim();
    if (!message) return;
    if (await send(session.sessionID, { command: "display-message", text: message })) setText("");
  }

  const controls: { label: string; icon: typeof Play; command: Command }[] = [
    {
      label: t("remotePrevious"),
      icon: SkipBack,
      command: { command: "skip-media", direction: "previous" },
    },
    { label: t("remotePause"), icon: Pause, command: { command: "pause" } },
    { label: t("remoteResume"), icon: Play, command: { command: "resume" } },
    {
      label: t("remoteNext"),
      icon: SkipForward,
      command: { command: "skip-media", direction: "next" },
    },
    { label: t("remoteStop"), icon: Square, command: { command: "stop" } },
    { label: t("remoteMute"), icon: VolumeX, command: { command: "mute" } },
    { label: t("remoteUnmute"), icon: Volume2, command: { command: "unmute" } },
  ];

  return (
    <li className="bf-remote-device">
      <div className="min-w-0">
        <p className="bf-remote-device-name">{device}</p>
        <p className="bf-remote-device-now">
          {session.title || t("remotePlayingHidden")}
          {session.durationSeconds > 0 && (
            <>
              {" · "}
              {t("remotePosition", {
                position: formatClockTime(session.positionSeconds),
                duration: formatClockTime(session.durationSeconds),
              })}
            </>
          )}
        </p>
      </div>
      <div className="bf-remote-controls">
        {controls.map(({ label, icon: Icon, command }) => (
          <Button
            key={label}
            variant="outline"
            size="icon-sm"
            aria-label={`${label}: ${device}`}
            title={label}
            onClick={() => run(command)}
          >
            <Icon aria-hidden />
          </Button>
        ))}
        <label className="bf-remote-volume" htmlFor={volumeID}>
          {t("remoteVolume")}
          <input
            id={volumeID}
            type="range"
            min={0}
            max={100}
            step={5}
            value={volume}
            onChange={(event) => setVolume(Number(event.target.value))}
            onPointerUp={commitVolume}
            onKeyUp={commitVolume}
          />
        </label>
      </div>
      <form className="bf-remote-send" onSubmit={onMessage}>
        <Input
          value={text}
          maxLength={256}
          aria-label={t("remoteMessageLabel", { device })}
          onChange={(event) => setText(event.target.value)}
        />
        <Button type="submit" variant="outline" size="sm" disabled={!text.trim()}>
          {t("remoteMessageSend")}
        </Button>
      </form>
    </li>
  );
}
